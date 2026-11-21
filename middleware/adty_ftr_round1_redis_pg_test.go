package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFTRADTY2303RedisLimitStorePG is ADTY-23-03: a live Redis cache and the user rate-limit store, with a PostgreSQL 18 evidence stamp.
func TestFTRADTY2303RedisLimitStorePG(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())

	previousDB := model.DB
	opened, err := model.OpenAdtyTestDatabase()
	if errors.Is(err, model.ErrAdtyTestDSNUnset) {
		t.Fatalf("ADTY_TEST_PG_DSN is unset: %v", err)
	}
	require.NoError(t, err)
	model.DB = opened.DB
	t.Cleanup(func() {
		model.DB = previousDB
		require.NoError(t, opened.Close())
	})
	pgtest.RecordServerVersion(t, opened.DB)
	require.NoError(t, opened.DB.AutoMigrate(&model.AdtyUserLimit{}))

	previousEnabled, previousClient := common.RedisEnabled, common.RDB
	previousConn := os.Getenv("REDIS_CONN_STRING")
	var live *redis.Client
	var cacheKey string
	t.Cleanup(func() {
		if live != nil {
			ctx := context.Background()
			if cacheKey != "" {
				_ = live.Del(ctx, cacheKey).Err()
			}
			_ = ftr2303ClearLimitKeys(live, 2303)
			if live != previousClient {
				_ = live.Close()
			}
		}
		common.RedisEnabled = previousEnabled
		common.RDB = previousClient
		if previousConn == "" {
			_ = os.Unsetenv("REDIS_CONN_STRING")
		} else {
			_ = os.Setenv("REDIS_CONN_STRING", previousConn)
		}
	})

	t.Setenv("REDIS_CONN_STRING", "redis://127.0.0.1:56379/0")
	t.Setenv("SYNC_FREQUENCY", "60")
	common.RedisEnabled = true
	require.NoError(t, common.InitRedisClient())
	live = common.RDB
	require.NotNil(t, live)
	require.NoError(t, common.PingRedis())

	buf := make([]byte, 8)
	_, err = rand.Read(buf)
	require.NoError(t, err)
	suffix := hex.EncodeToString(buf)
	cacheKey = "ftr-2303-" + suffix
	want := "roundtrip-" + suffix
	require.NoError(t, common.RedisSet(cacheKey, want, time.Minute))
	got, err := common.RedisGet(cacheKey)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	setting := adty_setting.GetLimitSetting()
	savedSetting := *setting
	*setting = adty_setting.LimitSetting{Enabled: true, DefaultRPM: 1, DefaultTPM: 100, DefaultConcurrency: 2}
	t.Cleanup(func() { *setting = savedSetting })

	previousStore := adtyLimitStore
	t.Cleanup(func() {
		adtyLimitStore = previousStore
		adtyLimitStoreOnce = sync.Once{}
	})
	adtyLimitStore = nil
	adtyLimitStoreOnce = sync.Once{}

	const userID = 2303
	require.NoError(t, ftr2303ClearLimitKeys(live, userID))

	var nextCalled bool
	engine := gin.New()
	auth := func(c *gin.Context) {
		c.Set("id", userID)
		requestID := c.GetHeader(common.RequestIdKey)
		if requestID == "" {
			requestID = "ftr-2303"
		}
		c.Set(common.RequestIdKey, requestID)
		c.Next()
	}
	engine.POST("/v1/chat/completions", auth, AdtyUserLimit(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	engine.POST("/v1/fail-closed", auth, AdtyUserLimit(), func(c *gin.Context) {
		nextCalled = true
		c.Status(http.StatusOK)
	})

	if remain := 60 - time.Now().Unix()%60; remain <= 2 {
		time.Sleep(time.Duration(remain+1) * time.Second)
		require.NoError(t, ftr2303ClearLimitKeys(live, userID))
	}

	first := ftr2303Do(engine, "/v1/chat/completions", "ftr-2303-req-1")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	minute := time.Now().Unix() / 60
	rpmKey := "ADTY:RPM:" + strconv.Itoa(userID) + ":" + strconv.FormatInt(minute, 10)
	rpm, err := common.RedisGet(rpmKey)
	require.NoError(t, err, "user limit counter must live in Redis")
	assert.Equal(t, "1", rpm)

	second := ftr2303Do(engine, "/v1/chat/completions", "ftr-2303-req-2")
	assert.NotEqual(t, http.StatusOK, second.Code, second.Body.String())
	assert.Equal(t, http.StatusTooManyRequests, second.Code, second.Body.String())

	var limits int64
	require.NoError(t, model.DB.Model(&model.AdtyUserLimit{}).Where("user_id = ?", userID).Count(&limits).Error)
	assert.Zero(t, limits)
	var quotaRows int64
	if model.DB.Migrator().HasTable(&model.User{}) {
		require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Count(&quotaRows).Error)
	}
	assert.Zero(t, quotaRows, "user 2303 quota row must stay absent")

	savedClient := common.RDB
	savedEnabled := common.RedisEnabled
	defer func() {
		common.RDB = savedClient
		common.RedisEnabled = savedEnabled
	}()
	common.RedisEnabled = true
	common.RDB = nil
	adtyLimitStore = nil
	adtyLimitStoreOnce = sync.Once{}
	closed := ftr2303Do(engine, "/v1/fail-closed", "ftr-2303-failclosed")
	assert.False(t, nextCalled, "AdtyUserLimit must abort instead of calling the next handler")
	assert.NotEqual(t, http.StatusOK, closed.Code, closed.Body.String())
	assert.Equal(t, http.StatusServiceUnavailable, closed.Code, closed.Body.String())
}

func ftr2303Do(engine http.Handler, path, requestID string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set(common.RequestIdKey, requestID)
	engine.ServeHTTP(recorder, req)
	return recorder
}

func ftr2303ClearLimitKeys(client *redis.Client, userID int) error {
	if client == nil {
		return nil
	}
	ctx := context.Background()
	id := strconv.Itoa(userID)
	var keys []string
	for _, pattern := range []string{"ADTY:RPM:" + id + ":*", "ADTY:TPM:" + id + ":*", "ADTY:CONC:" + id} {
		found, err := client.Keys(ctx, pattern).Result()
		if err != nil {
			return err
		}
		keys = append(keys, found...)
	}
	if len(keys) == 0 {
		return nil
	}
	return client.Del(ctx, keys...).Err()
}
