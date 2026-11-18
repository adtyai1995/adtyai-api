package middleware

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/adtylimit"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

// AdtyKeyRateLimit enforces a per-key request cap when one is configured.
// A key with no row is not extra-limited. This does not replace the user limit.
// When midjourney is true, 429 and 503 responses use the Midjourney error shape.
func AdtyKeyRateLimit(midjourney ...bool) gin.HandlerFunc {
	isMJ := len(midjourney) > 0 && midjourney[0]
	return func(c *gin.Context) {
		tokenID := c.GetInt("token_id")
		if tokenID <= 0 {
			c.Next()
			return
		}
		limit, err := model.GetAdtyKeyLimit(tokenID)
		if err != nil {
			common.SysError(common.LogText("adty key limit config unavailable token=%d: %s", tokenID, err.Error()))
			adtyAbortUnavailable(c, isMJ)
			return
		}
		if limit.PerMinute <= 0 {
			c.Next()
			return
		}
		now := adtyLimitNow()
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
		c.Next()
	}
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
