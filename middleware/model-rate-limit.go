package middleware

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/common/limiter"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const (
	ModelRequestRateLimitCountMark        = "MRRL"
	ModelRequestRateLimitSuccessCountMark = "MRRLS"
	redisSuccessMinInflightLeaseMs        = int64(5 * 60 * 1000)
	redisSuccessMaxInflightLeaseMs        = int64(24 * 60 * 60 * 1000)
)

var modelInMemoryRateLimiter common.InMemoryRateLimiter

var redisSuccessReserveScript = redis.NewScript(`
local winKey = KEYS[1]
local inflightKey = KEYS[2]
local maxCount = tonumber(ARGV[1])
local durationMs = tonumber(ARGV[2])
local leaseMs = tonumber(ARGV[3])
local reqID = ARGV[4]

if maxCount <= 0 or durationMs <= 0 then
	return 1
end

local t = redis.call('TIME')
local nowMs = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)

redis.call('ZREMRANGEBYSCORE', winKey, '-inf', nowMs - durationMs)
redis.call('ZREMRANGEBYSCORE', inflightKey, '-inf', nowMs)

local completed = redis.call('ZCARD', winKey)
local inflight = redis.call('ZCARD', inflightKey)
if completed + inflight >= maxCount then
	return 0
end

redis.call('ZADD', inflightKey, nowMs + leaseMs, reqID)
local ttlSec = math.max(1, math.ceil(leaseMs / 1000) + 60)
redis.call('EXPIRE', inflightKey, ttlSec)
return 1
`)

var redisSuccessCompleteScript = redis.NewScript(`
local winKey = KEYS[1]
local inflightKey = KEYS[2]
local reqID = ARGV[1]
local succeeded = tonumber(ARGV[2])
local durationMs = tonumber(ARGV[3])

redis.call('ZREM', inflightKey, reqID)
if succeeded == 1 and durationMs > 0 then
	local t = redis.call('TIME')
	local nowMs = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
	redis.call('ZREMRANGEBYSCORE', winKey, '-inf', nowMs - durationMs)
	redis.call('ZADD', winKey, nowMs, reqID)
	local ttlSec = math.max(1, math.ceil(durationMs / 1000) + 60)
	redis.call('EXPIRE', winKey, ttlSec)
end
return 1
`)

func reserveRedisSuccessSlot(ctx context.Context, rdb *redis.Client, winKey, inflightKey string, maxCount int, duration int64, reqID string) (bool, error) {
	if maxCount <= 0 || duration <= 0 {
		return true, nil
	}
	durationMs := duration * 1000
	leaseMs := max(durationMs, redisSuccessMinInflightLeaseMs)
	if leaseMs > redisSuccessMaxInflightLeaseMs {
		leaseMs = redisSuccessMaxInflightLeaseMs
	}
	res, err := redisSuccessReserveScript.Run(ctx, rdb, []string{winKey, inflightKey}, maxCount, durationMs, leaseMs, reqID).Int()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}

func completeRedisSuccessSlot(ctx context.Context, rdb *redis.Client, winKey, inflightKey string, duration int64, reqID string, succeeded bool) {
	flag := 0
	if succeeded {
		flag = 1
	}
	if err := redisSuccessCompleteScript.Run(ctx, rdb, []string{winKey, inflightKey}, reqID, flag, duration*1000).Err(); err != nil {
		fmt.Println(common.LogText("failed to complete redis success rate-limit reservation: %s", err.Error()))
	}
}

// Redis限流处理器
func redisRateLimitHandler(subject string, durationMinutes int, duration int64, totalMaxCount, successMaxCount int) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := context.Background()
		rdb := common.RDB
		if rdb == nil && (totalMaxCount > 0 || successMaxCount > 0) {
			abortWithOpenAiMessage(c, http.StatusInternalServerError, "rate_limit_check_failed")
			return
		}

		// 1. 检查总请求数限制并记录总请求（当totalMaxCount为0时会自动跳过，使用令牌桶限流器）
		if totalMaxCount > 0 {
			totalKey := fmt.Sprintf("rateLimit:%s", subject)
			tb := limiter.New(ctx, rdb)
			allowed, err := tb.Allow(
				ctx,
				totalKey,
				limiter.WithCapacity(rateLimitCapacity(totalMaxCount, duration)),
				limiter.WithRate(int64(totalMaxCount)),
				limiter.WithRequested(duration),
			)

			if err != nil {
				fmt.Println(common.LogText("failed to check the total request limit: %s", err.Error()))
				abortWithOpenAiMessage(c, http.StatusInternalServerError, "rate_limit_check_failed")
				return
			}

			if !allowed {
				abortWithOpenAiMessage(c, http.StatusTooManyRequests, i18n.T(c, i18n.MsgRateLimitTotalReached, map[string]any{"Minutes": durationMinutes, "Max": totalMaxCount}))
				return
			}
		}

		// 2. 检查并原子预占成功请求数槽位（防止高并发流式突发绕过 — R13-007）
		winKey := fmt.Sprintf("rateLimit:%s:{%s}:win", ModelRequestRateLimitSuccessCountMark, subject)
		inflightKey := fmt.Sprintf("rateLimit:%s:{%s}:inflight", ModelRequestRateLimitSuccessCountMark, subject)
		var reqID string
		var completeOnce sync.Once
		completeSlot := func(succeeded bool) {
			if reqID == "" {
				return
			}
			completeOnce.Do(func() {
				completeRedisSuccessSlot(ctx, rdb, winKey, inflightKey, duration, reqID, succeeded)
			})
		}
		if successMaxCount > 0 {
			reqID = common.GetUUID()
			allowed, err := reserveRedisSuccessSlot(ctx, rdb, winKey, inflightKey, successMaxCount, duration, reqID)
			if err != nil {
				fmt.Println(common.LogText("failed to check the successful request limit: %s", err.Error()))
				abortWithOpenAiMessage(c, http.StatusInternalServerError, "rate_limit_check_failed")
				return
			}
			if !allowed {
				abortWithOpenAiMessage(c, http.StatusTooManyRequests, i18n.T(c, i18n.MsgRateLimitReached, map[string]any{"Minutes": durationMinutes, "Max": successMaxCount}))
				return
			}
			defer completeSlot(false)
		}

		// 3. 处理请求
		c.Next()

		// 4. 请求结束时转正为窗口成功计数或释放占位
		completeSlot(modelRequestSucceeded(c))
	}
}

// 内存限流处理器
func memoryRateLimitHandler(subject string, durationMinutes int, duration int64, totalMaxCount, successMaxCount int) gin.HandlerFunc {
	modelInMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)

	return func(c *gin.Context) {
		totalKey := ModelRequestRateLimitCountMark + subject
		successKey := ModelRequestRateLimitSuccessCountMark + subject

		// 1. 检查总请求数限制（当totalMaxCount为0时跳过）
		if totalMaxCount > 0 && !modelInMemoryRateLimiter.Request(totalKey, totalMaxCount, duration) {
			abortWithOpenAiMessage(c, http.StatusTooManyRequests, i18n.T(c, i18n.MsgRateLimitTotalReached, map[string]any{"Minutes": durationMinutes, "Max": totalMaxCount}))
			return
		}

		var reservation *common.RateLimitReservation
		if successMaxCount > 0 {
			reservation = modelInMemoryRateLimiter.Reserve(successKey, successMaxCount, duration)
			if reservation == nil {
				abortWithOpenAiMessage(c, http.StatusTooManyRequests, i18n.T(c, i18n.MsgRateLimitReached, map[string]any{"Minutes": durationMinutes, "Max": successMaxCount}))
				return
			}
			defer reservation.Complete(false)
		}

		// 3. 处理请求
		c.Next()

		// 4. 如果请求成功，记录到实际的成功请求计数中
		reservation.Complete(modelRequestSucceeded(c))
	}
}

func modelRequestSucceeded(c *gin.Context) bool {
	status, _ := common.GetContextKeyType[*relaycommon.StreamStatus](c, constant.ContextKeyResponseStreamStatus)
	return c.Writer.Status() < 400 && !status.ResponseFailed()
}

// ModelRequestRateLimit 模型请求限流中间件
func ModelRequestRateLimit() func(c *gin.Context) {
	return func(c *gin.Context) {
		// 在每个请求时快照限流配置（并发安全 — R11-009）
		enabled, durationMinutes, totalMaxCount, successMaxCount := setting.GetModelRequestRateLimitConfig()
		if !enabled {
			c.Next()
			return
		}

		// 计算限流参数（防旧库/异常配置 0 分钟绕过限流 — R13-021）
		if durationMinutes < 1 {
			durationMinutes = 1
		}
		duration := rateLimitDurationSeconds(durationMinutes)

		// 获取分组
		group := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
		if group == "" {
			group = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
		}

		// 获取分组的限流配置。配置了独立限流的分组按用户和分组单独计数，
		// 其余分组共用该用户的全局计数
		subject := strconv.Itoa(c.GetInt("id"))
		groupTotalCount, groupSuccessCount, found := setting.GetGroupRateLimit(group)
		if found {
			totalMaxCount = groupTotalCount
			successMaxCount = groupSuccessCount
			subject += ":" + group
		}

		// 根据存储类型选择并执行限流处理器
		if common.RedisEnabled {
			redisRateLimitHandler(subject, durationMinutes, duration, totalMaxCount, successMaxCount)(c)
		} else {
			memoryRateLimitHandler(subject, durationMinutes, duration, totalMaxCount, successMaxCount)(c)
		}
	}
}

func rateLimitDurationSeconds(durationMinutes int) int64 {
	if durationMinutes <= 0 {
		return 0
	}
	minutes := int64(durationMinutes)
	if minutes > math.MaxInt64/60 {
		return math.MaxInt64
	}
	return minutes * 60
}

func rateLimitCapacity(count int, durationSeconds int64) int64 {
	if count <= 0 || durationSeconds <= 0 {
		return 0
	}
	c := int64(count)
	if c > math.MaxInt64/durationSeconds {
		return math.MaxInt64
	}
	return c * durationSeconds
}
