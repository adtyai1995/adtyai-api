package middleware

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/adtylimit"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

// AdtyKeyRateLimit enforces per-key request (RPM), token (TPM), and concurrency
// caps when configured. A key with no row is not extra-limited. This does not
// replace the user limit. When midjourney is true, 429 and 503 responses use
// the Midjourney error shape.
func AdtyKeyRateLimit(midjourney ...bool) gin.HandlerFunc {
	isMJ := len(midjourney) > 0 && midjourney[0]
	return func(c *gin.Context) {
		tokenID := c.GetInt("token_id")
		if tokenID <= 0 {
			c.Next()
			return
		}
		limit, err := model.GetAdtyKeyLimits(tokenID)
		if err != nil {
			common.SysError(common.LogText("adty key limit config unavailable token=%d: %s", tokenID, err.Error()))
			adtyAbortUnavailable(c, isMJ)
			return
		}
		if limit.PerMinute <= 0 && limit.TPM <= 0 && limit.Concurrency <= 0 {
			c.Next()
			return
		}
		now := adtyCurrentTime()
		if limit.TPM > 0 {
			allowed, err := adtyKeyTPMAllow(tokenID, limit.TPM, now)
			if err != nil {
				common.SysError(common.LogText("adty key tpm limit store unavailable token=%d: %s", tokenID, err.Error()))
				adtyAbortUnavailable(c, isMJ)
				return
			}
			if !allowed {
				adtyRejectLimited(c, adtylimit.ReasonTPM, now, isMJ)
				return
			}
		}
		if limit.PerMinute > 0 {
			allowed, err := adtyKeyRateAllow(tokenID, limit.PerMinute, now)
			if err != nil {
				common.SysError(common.LogText("adty key limit store unavailable token=%d: %s", tokenID, err.Error()))
				adtyAbortUnavailable(c, isMJ)
				return
			}
			if !allowed {
				adtyRejectLimited(c, adtylimit.ReasonRPM, now, isMJ)
				return
			}
		}
		var release func()
		if limit.Concurrency > 0 {
			var allowed bool
			release, allowed, err = adtyKeyConcurrencyAcquire(c, tokenID, limit.Concurrency, now)
			if err != nil {
				common.SysError(common.LogText("adty key concurrency limit store unavailable token=%d: %s", tokenID, err.Error()))
				adtyAbortUnavailable(c, isMJ)
				return
			}
			if !allowed {
				adtyRejectLimited(c, adtylimit.ReasonConcurrency, now, isMJ)
				return
			}
		}
		if release != nil {
			defer release()
		}
		c.Next()
		if !c.GetBool("adty_key_tpm_recorded") {
			if usedTokens := extractContextKeyTokens(c); usedTokens > 0 {
				_ = RecordAdtyKeyTPM(tokenID, usedTokens, now)
			}
		}
	}
}

func extractContextKeyTokens(c *gin.Context) int {
	if c == nil {
		return 0
	}
	if total := c.GetInt("adty_key_tokens"); total > 0 {
		return total
	}
	if total := c.GetInt("total_tokens"); total > 0 {
		return total
	}
	prompt := c.GetInt("prompt_tokens")
	completion := c.GetInt("completion_tokens")
	if prompt+completion > 0 {
		return prompt + completion
	}
	return 0
}

// adtyKeyRateBucket is one key's counter inside a single minute window. The minute is
// stored next to the count so an expired window can be dropped instead of kept forever.
type adtyKeyRateBucket struct {
	minute int64
	count  int
}

const adtyKeyRateTTLSeconds = 120

var (
	adtyKeyRateMemory            = map[int]adtyKeyRateBucket{}
	adtyKeyRateMemorySwept int64 = -1
	adtyKeyRateMemoryMu    sync.Mutex

	adtyKeyTPMMemory            = map[int]adtyKeyRateBucket{}
	adtyKeyTPMMemorySwept int64 = -1
	adtyKeyTPMMemoryMu    sync.Mutex

	adtyKeyConcurrencySeq atomic.Uint64

	adtyKeyRateTTLArg      any = adtyKeyRateTTLSeconds
	adtyKeyRateIncrWithTTL     = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if redis.call('TTL', KEYS[1]) < 0 then
  local res = redis.pcall('EXPIRE', KEYS[1], ARGV[1])
  if type(res) == 'table' and res.err ~= nil then
    redis.pcall('DEL', KEYS[1])
    return res
  end
  if res ~= 1 or redis.call('TTL', KEYS[1]) <= 0 then
    redis.pcall('DEL', KEYS[1])
    return redis.error_reply('ERR failed to set key rate limit ttl')
  end
end
return count`)
)

func adtyKeyRateRedisKey(tokenID int, now time.Time) string {
	return fmt.Sprintf("ADTY:KEYRPM:%d:%d", tokenID, now.Unix()/60)
}

func adtyKeyTPMRedisKey(tokenID int, now time.Time) string {
	return fmt.Sprintf("ADTY:KEYTPM:%d:%d", tokenID, now.Unix()/60)
}

func adtyKeyRateAllow(tokenID, perMinute int, now time.Time) (bool, error) {
	if common.RedisEnabled {
		if common.RDB == nil {
			return false, errAdtyLimitStoreUnavailable
		}
		ctx := context.Background()
		key := adtyKeyRateRedisKey(tokenID, now)
		count, err := adtyKeyRateIncrWithTTL.Run(ctx, common.RDB, []string{key}, adtyKeyRateTTLArg).Int64()
		if err != nil {
			lowerErr := strings.ToLower(err.Error())
			if strings.Contains(lowerErr, "noperm") && strings.Contains(lowerErr, "eval") {
				return adtyKeyRateAllowFallback(ctx, key, perMinute)
			}
			return false, err
		}
		return count <= int64(perMinute), nil
	}
	minute := now.Unix() / 60
	adtyKeyRateMemoryMu.Lock()
	defer adtyKeyRateMemoryMu.Unlock()
	// The Redis branch gives every per-minute bucket a two-minute TTL. The memory branch
	// has to expire the same way, otherwise it keeps one bucket per key per minute for the
	// life of the process. Sweeping only when the minute changes keeps that to one pass per
	// minute, and the map then holds at most two windows instead of every window ever seen.
	if minute != adtyKeyRateMemorySwept {
		adtyKeyRateMemorySwept = minute
		for id, bucket := range adtyKeyRateMemory {
			if bucket.minute < minute-1 {
				delete(adtyKeyRateMemory, id)
			}
		}
	}
	bucket := adtyKeyRateMemory[tokenID]
	if bucket.minute != minute {
		bucket = adtyKeyRateBucket{minute: minute}
	}
	bucket.count++
	adtyKeyRateMemory[tokenID] = bucket
	return bucket.count <= perMinute, nil
}

func adtyKeyRateAllowFallback(ctx context.Context, key string, perMinute int) (bool, error) {
	count, err := common.RDB.Incr(ctx, key).Result()
	if err != nil {
		return false, err
	}
	ttl, err := common.RDB.TTL(ctx, key).Result()
	if err != nil {
		_ = common.RDB.Del(ctx, key).Err()
		return false, err
	}
	if ttl <= 0 {
		ok, expErr := common.RDB.Expire(ctx, key, adtyKeyRateTTLSeconds*time.Second).Result()
		if expErr != nil {
			_ = common.RDB.Del(ctx, key).Err()
			return false, expErr
		}
		if !ok {
			_ = common.RDB.Del(ctx, key).Err()
			return false, errors.New("failed to set key rate limit ttl")
		}
	}
	return count <= int64(perMinute), nil
}

// RecordAdtyKeyTPM adds consumed tokens to the per-key minute bucket.
func RecordAdtyKeyTPM(tokenID, tokens int, nowOpt ...time.Time) error {
	if tokenID <= 0 || tokens <= 0 {
		return nil
	}
	now := adtyCurrentTime()
	if len(nowOpt) > 0 {
		now = nowOpt[0]
	}
	if common.RedisEnabled {
		if common.RDB == nil {
			return errAdtyLimitStoreUnavailable
		}
		ctx := context.Background()
		key := adtyKeyTPMRedisKey(tokenID, now)
		if _, err := common.RDB.IncrBy(ctx, key, int64(tokens)).Result(); err != nil {
			return err
		}
		if ttl, err := common.RDB.TTL(ctx, key).Result(); err == nil && ttl <= 0 {
			_ = common.RDB.Expire(ctx, key, adtyKeyRateTTLSeconds*time.Second).Err()
		}
		return nil
	}
	minute := now.Unix() / 60
	adtyKeyTPMMemoryMu.Lock()
	defer adtyKeyTPMMemoryMu.Unlock()
	if minute != adtyKeyTPMMemorySwept {
		adtyKeyTPMMemorySwept = minute
		for id, bucket := range adtyKeyTPMMemory {
			if bucket.minute < minute-1 {
				delete(adtyKeyTPMMemory, id)
			}
		}
	}
	bucket := adtyKeyTPMMemory[tokenID]
	if bucket.minute != minute {
		bucket = adtyKeyRateBucket{minute: minute}
	}
	bucket.count += tokens
	adtyKeyTPMMemory[tokenID] = bucket
	return nil
}

func adtyKeyTPMAllow(tokenID, tpmLimit int, now time.Time) (bool, error) {
	var used int64
	if common.RedisEnabled {
		if common.RDB == nil {
			return false, errAdtyLimitStoreUnavailable
		}
		raw, err := common.RDB.Get(context.Background(), adtyKeyTPMRedisKey(tokenID, now)).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return false, err
		}
		if raw != "" {
			parsed, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil {
				return false, parseErr
			}
			used = parsed
		}
	} else {
		minute := now.Unix() / 60
		adtyKeyTPMMemoryMu.Lock()
		if minute != adtyKeyTPMMemorySwept {
			adtyKeyTPMMemorySwept = minute
			for id, bucket := range adtyKeyTPMMemory {
				if bucket.minute < minute-1 {
					delete(adtyKeyTPMMemory, id)
				}
			}
		}
		if bucket, ok := adtyKeyTPMMemory[tokenID]; ok && bucket.minute == minute {
			used = int64(bucket.count)
		}
		adtyKeyTPMMemoryMu.Unlock()
	}
	logUsed, err := adtyKeyTPMLogTokens(tokenID, now)
	if err != nil {
		return false, err
	}
	if logUsed > used {
		used = logUsed
	}
	return used < int64(tpmLimit), nil
}

func adtyKeyTPMLogTokens(tokenID int, now time.Time) (int64, error) {
	if !common.LogConsumeEnabled || model.LOG_DB == nil || !model.LOG_DB.Migrator().HasTable("logs") {
		return 0, nil
	}
	minuteStart := (now.Unix() / 60) * 60
	var sum int64
	if err := model.LOG_DB.Table("logs").
		Select("COALESCE(SUM(prompt_tokens + completion_tokens), 0)").
		Where("token_id = ? AND created_at >= ? AND created_at < ?", tokenID, minuteStart, minuteStart+60).
		Scan(&sum).Error; err != nil {
		return 0, err
	}
	return sum, nil
}

func init() {
	model.AdtyOnConsumeKeyTokens = func(tokenID int, tokens int) {
		if tokenID > 0 && tokens > 0 {
			_ = RecordAdtyKeyTPM(tokenID, tokens, time.Now())
		}
	}
}

func adtyKeyConcurrencyAcquire(c *gin.Context, tokenID, maxConcurrency int, now time.Time) (func(), bool, error) {
	store, err := adtyStore()
	if err != nil {
		return nil, false, err
	}
	seq := adtyKeyConcurrencySeq.Add(1)
	reqID := ""
	if c != nil {
		reqID = c.GetString(common.RequestIdKey)
	}
	member := fmt.Sprintf("%s:%d", reqID, seq)
	release, reason, err := adtylimit.CheckKey(store, tokenID, adtylimit.Limits{Concurrency: maxConcurrency}, member, now)
	if err != nil {
		return nil, false, err
	}
	if reason != adtylimit.ReasonNone {
		return nil, false, nil
	}
	return release, true, nil
}
