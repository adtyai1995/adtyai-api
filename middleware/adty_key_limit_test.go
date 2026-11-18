package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestKeyRateLimitRejectsTheNextRequestOverTheCap(t *testing.T) {
	require.NoError(t, i18n.Init())
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AdtyKeyLimit{}))
	oldDB := model.DB
	model.DB = db
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.DB = oldDB
		common.RedisEnabled = previousRedis
	})
	require.NoError(t, model.SaveAdtyKeyLimit(11, 1))

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Set("token_id", 11)
		c.Next()
	}, AdtyKeyRateLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })
	other := gin.New()
	other.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Set("token_id", 12)
		c.Next()
	}, AdtyKeyRateLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })

	first := httptest.NewRecorder()
	engine.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	assert.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	engine.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	assert.Equal(t, http.StatusTooManyRequests, second.Code, second.Body.String())

	untouched := httptest.NewRecorder()
	other.ServeHTTP(untouched, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	assert.Equal(t, http.StatusOK, untouched.Code, "a key without a cap is not limited")
	_ = time.Minute
}

func TestKeyRateLimitMidjourneySubmissionRoutesAndStoreOutage(t *testing.T) {
	// ISS-R1-0105: /mj and /:mode/mj submission routes enforce per-key rate limiting
	// with 429 on limit exceeded and 503 on store/config outage using Midjourney error shape.
	require.NoError(t, i18n.Init())
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AdtyKeyLimit{}))
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	resetKeyRateMemory(t)

	now := time.Unix(1_700_000_040, 0)
	SetAdtyLimitStoreForTest(t, nil, func() time.Time { return now })
	require.NoError(t, model.SaveAdtyKeyLimit(31, 1))

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	auth := func(c *gin.Context) {
		c.Set("id", 7)
		c.Set("token_id", 31)
		c.Next()
	}
	engine.POST("/mj/submit/imagine", auth, AdtyUserLimit(true), AdtyKeyRateLimit(true), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	engine.POST("/:mode/mj/submit/imagine", auth, AdtyUserLimit(true), AdtyKeyRateLimit(true), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	first := httptest.NewRecorder()
	engine.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", nil))
	assert.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	engine.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", nil))
	assert.Equal(t, http.StatusTooManyRequests, second.Code)
	assert.Contains(t, second.Body.String(), `"description"`)

	modeLimited := httptest.NewRecorder()
	engine.ServeHTTP(modeLimited, httptest.NewRequest(http.MethodPost, "/fast/mj/submit/imagine", nil))
	assert.Equal(t, http.StatusTooManyRequests, modeLimited.Code)
	assert.Contains(t, modeLimited.Body.String(), `"description"`)

	// Next minute resets the key bucket; then simulate Redis outage -> 503.
	now = now.Add(time.Minute)
	nextMin := httptest.NewRecorder()
	engine.ServeHTTP(nextMin, httptest.NewRequest(http.MethodPost, "/fast/mj/submit/imagine", nil))
	assert.Equal(t, http.StatusOK, nextMin.Code)

	common.RedisEnabled = true
	common.RDB = nil
	outage := httptest.NewRecorder()
	engine.ServeHTTP(outage, httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", nil))
	assert.Equal(t, http.StatusServiceUnavailable, outage.Code)
	assert.Contains(t, outage.Body.String(), `"description"`)
}

type rejectEvalAndExpireHook struct {
	rejectEval   bool
	rejectExpire bool
}

func (h rejectEvalAndExpireHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	name := strings.ToLower(cmd.Name())
	if h.rejectEval && (name == "eval" || name == "evalsha") {
		return ctx, errors.New("NOPERM this user has no permissions to run the 'evalsha' command")
	}
	if h.rejectExpire && name == "expire" {
		return ctx, errors.New("NOPERM this user has no permissions to run the 'expire' command")
	}
	return ctx, nil
}

func (rejectEvalAndExpireHook) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (rejectEvalAndExpireHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (rejectEvalAndExpireHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func TestKeyRateRedisAtomicTTLAndExpireFailureFailsClosedWithoutOrphanKey(t *testing.T) {
	// ISS-R1-0107: Per-minute Redis key bucket must set TTL atomically, repair missing TTL
	// when count > 1, and fail closed with 503 without retaining a TTL=-1 counter when EXPIRE fails.
	require.NoError(t, i18n.Init())
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AdtyKeyLimit{}))
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	require.NoError(t, model.SaveAdtyKeyLimit(41, 2))

	now := time.Unix(1_700_000_040, 0).Truncate(time.Minute)
	SetAdtyLimitStoreForTest(t, nil, func() time.Time { return now })

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Set("token_id", 41)
		c.Next()
	}, AdtyKeyRateLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })

	t.Run("lua expire failure returns 503 and deletes unexpirable key", func(t *testing.T) {
		redisServer, _ := useRateLimitMiniRedis(t)
		oldTTLArg := adtyKeyRateTTLArg
		adtyKeyRateTTLArg = "invalid-ttl"
		t.Cleanup(func() { adtyKeyRateTTLArg = oldTTLArg })

		first := httptest.NewRecorder()
		engine.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
		assert.Equal(t, http.StatusServiceUnavailable, first.Code)
		assert.Contains(t, first.Body.String(), "rate_limit_unavailable")

		failedKey := adtyKeyRateRedisKey(41, now)
		assert.False(t, redisServer.Exists(failedKey), "failed EXPIRE must not leave an indefinite TTL=-1 bucket")

		// Restore valid TTL and advance to the next minute: new bucket has 2m TTL, old bucket does not exist.
		adtyKeyRateTTLArg = oldTTLArg
		now = now.Add(time.Minute)
		okResp := httptest.NewRecorder()
		engine.ServeHTTP(okResp, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
		assert.Equal(t, http.StatusOK, okResp.Code)

		nextKey := adtyKeyRateRedisKey(41, now)
		assert.False(t, redisServer.Exists(failedKey))
		assert.True(t, redisServer.Exists(nextKey))
		assert.Equal(t, 2*time.Minute, redisServer.TTL(nextKey))
	})

	t.Run("acl rejecting expire returns 503 and deletes unexpirable key", func(t *testing.T) {
		redisServer, redisClient := useRateLimitMiniRedis(t)
		redisClient.AddHook(rejectEvalAndExpireHook{rejectEval: true, rejectExpire: true})

		now = now.Add(time.Minute)
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
		assert.Equal(t, http.StatusServiceUnavailable, resp.Code)
		assert.Contains(t, resp.Body.String(), "rate_limit_unavailable")
		assert.False(t, redisServer.Exists(adtyKeyRateRedisKey(41, now)), "rejected EXPIRE must not retain TTL=-1 counter")
	})

	t.Run("count greater than 1 without ttl repairs ttl", func(t *testing.T) {
		redisServer, _ := useRateLimitMiniRedis(t)
		now = now.Add(time.Minute)
		key := adtyKeyRateRedisKey(41, now)
		require.NoError(t, redisServer.Set(key, "1"))
		require.Equal(t, time.Duration(0), redisServer.TTL(key))

		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
		assert.Equal(t, http.StatusOK, resp.Code)
		assert.Equal(t, 2*time.Minute, redisServer.TTL(key), "subsequent increment must repair missing TTL")

		redisServer.FastForward(2 * time.Minute)
		assert.False(t, redisServer.Exists(key))
	})
}

// resetKeyRateMemory 清空内存分支的桶，并把 Redis 关掉（这是仓库支持的部署形态）。
func resetKeyRateMemory(t *testing.T) {
	t.Helper()
	previousRedis := common.RedisEnabled
	previousMemory, previousSwept := adtyKeyRateMemory, adtyKeyRateMemorySwept
	previousTPMMemory, previousTPMSwept := adtyKeyTPMMemory, adtyKeyTPMMemorySwept
	previousConcurrency := adtyKeyConcurrencyMemory
	common.RedisEnabled = false
	adtyKeyRateMemory, adtyKeyRateMemorySwept = map[int]adtyKeyRateBucket{}, -1
	adtyKeyTPMMemory, adtyKeyTPMMemorySwept = map[int]adtyKeyRateBucket{}, -1
	adtyKeyConcurrencyMemory = map[int]map[string]time.Time{}
	t.Cleanup(func() {
		common.RedisEnabled = previousRedis
		adtyKeyRateMemory, adtyKeyRateMemorySwept = previousMemory, previousSwept
		adtyKeyTPMMemory, adtyKeyTPMMemorySwept = previousTPMMemory, previousTPMSwept
		adtyKeyConcurrencyMemory = previousConcurrency
	})
}

// 内存桶必须按窗口过期：Redis 分支每个分钟桶有 2 分钟 TTL，内存分支不能一个 key 一分钟留一条、
// 直到进程退出。推进 15 分钟后，不再写过的 key 的旧桶必须消失，map 长度只跟「还在用的 key 数」有关。
func TestKeyRateMemoryDropsBucketsOlderThanTheWindow(t *testing.T) {
	resetKeyRateMemory(t)
	start := time.Unix(1_700_000_040, 0).Truncate(time.Minute)
	const keys = 500

	for id := 1000; id < 1000+keys; id++ {
		allowed, err := adtyKeyRateAllow(id, 10, start)
		require.NoError(t, err)
		require.True(t, allowed)
	}
	require.Len(t, adtyKeyRateMemory, keys)

	// 同一 key 在下一分钟写入时，它上一分钟的桶被当前窗口顶掉；其它 key 的上一分钟桶还在，
	// 与 Redis 那 2 分钟 TTL 一致。
	allowed, err := adtyKeyRateAllow(1000, 10, start.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, allowed)
	require.Len(t, adtyKeyRateMemory, keys)
	require.Equal(t, start.Unix()/60, adtyKeyRateMemory[1001].minute)

	for step := 2; step <= 15; step++ {
		allowed, err := adtyKeyRateAllow(1000, 10, start.Add(time.Duration(step)*time.Minute))
		require.NoError(t, err)
		require.True(t, allowed)
		require.LessOrEqual(t, len(adtyKeyRateMemory), 2,
			"第 %d 分钟写一次后 map 里只剩当前与上一分钟的桶", step)
	}
}

// 清理不能把窗口内的计数也清掉：同一分钟第 2 次请求仍然超限，下一分钟重新开始算。
func TestKeyRateMemoryCapSurvivesTheSweepWithinOneMinute(t *testing.T) {
	resetKeyRateMemory(t)
	start := time.Unix(1_700_000_040, 0).Truncate(time.Minute)

	first, err := adtyKeyRateAllow(21, 1, start)
	require.NoError(t, err)
	require.True(t, first)

	second, err := adtyKeyRateAllow(21, 1, start)
	require.NoError(t, err)
	require.False(t, second, "同一分钟内的计数不能被清理掉")

	next, err := adtyKeyRateAllow(21, 1, start.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, next, "下一分钟是新的窗口")
}

func TestKeyRateLimitEnforcesTPMAndConcurrencyIndependently(t *testing.T) {
	// ISS-R1-0100: Per-key TPM and Concurrency caps are enforced independently per key.
	require.NoError(t, i18n.Init())
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, model.EnsureAdtyKeyLimitSchema(db))
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	resetKeyRateMemory(t)

	now := time.Unix(1_700_000_040, 0).Truncate(time.Minute)
	SetAdtyLimitStoreForTest(t, nil, func() time.Time { return now })

	// Key 51: RPM=10, TPM=100, Concurrency=1
	// Key 52: RPM=10, TPM=1000, Concurrency=2
	require.NoError(t, model.SaveAdtyKeyLimits(51, 10, 100, 1))
	require.NoError(t, model.SaveAdtyKeyLimits(52, 10, 1000, 2))

	inFlightStarted := make(chan struct{})
	releaseInFlight := make(chan struct{})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/v1/chat/completions", func(c *gin.Context) {
		switch c.GetHeader("X-Token-ID") {
		case "52":
			c.Set("token_id", 52)
		default:
			c.Set("token_id", 51)
		}
		c.Next()
	}, AdtyKeyRateLimit(), func(c *gin.Context) {
		if c.GetHeader("X-Hold") == "true" {
			close(inFlightStarted)
			<-releaseInFlight
		}
		if c.GetHeader("X-Tokens") == "100" {
			c.Set("total_tokens", 100)
		}
		c.Status(http.StatusOK)
	})

	// 1. Hold 1 concurrency slot on Key 51.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set("X-Token-ID", "51")
		req.Header.Set("X-Hold", "true")
		engine.ServeHTTP(rec, req)
		done <- rec
	}()
	<-inFlightStarted

	// Concurrent request on Key 51 is rejected with 429 (concurrency limit = 1).
	concurrent51 := httptest.NewRecorder()
	req51 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req51.Header.Set("X-Token-ID", "51")
	engine.ServeHTTP(concurrent51, req51)
	assert.Equal(t, http.StatusTooManyRequests, concurrent51.Code, concurrent51.Body.String())

	// Concurrent request on Key 52 succeeds (independent key concurrency).
	concurrent52 := httptest.NewRecorder()
	req52 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req52.Header.Set("X-Token-ID", "52")
	engine.ServeHTTP(concurrent52, req52)
	assert.Equal(t, http.StatusOK, concurrent52.Code)

	// Release Key 51's held request.
	close(releaseInFlight)
	heldRec := <-done
	assert.Equal(t, http.StatusOK, heldRec.Code)

	// 2. Consume 100 tokens on Key 51 -> reaches TPM=100 cap.
	consume51 := httptest.NewRecorder()
	reqConsume := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	reqConsume.Header.Set("X-Token-ID", "51")
	reqConsume.Header.Set("X-Tokens", "100")
	engine.ServeHTTP(consume51, reqConsume)
	assert.Equal(t, http.StatusOK, consume51.Code)

	// Next request in the same minute on Key 51 is rejected with 429 (TPM exceeded).
	tpmLimited51 := httptest.NewRecorder()
	reqLimited := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	reqLimited.Header.Set("X-Token-ID", "51")
	engine.ServeHTTP(tpmLimited51, reqLimited)
	assert.Equal(t, http.StatusTooManyRequests, tpmLimited51.Code, tpmLimited51.Body.String())

	// Advancing to the next minute resets Key 51's TPM bucket.
	now = now.Add(time.Minute)
	nextMin51 := httptest.NewRecorder()
	reqNextMin := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	reqNextMin.Header.Set("X-Token-ID", "51")
	engine.ServeHTTP(nextMin51, reqNextMin)
	assert.Equal(t, http.StatusOK, nextMin51.Code)
}
