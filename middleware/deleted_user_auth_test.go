package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTokenAuthRejectsKeysOfDeletedUser(t *testing.T) {
	require.NoError(t, i18n.Init())
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/v1/chat/completions", TokenAuth(), func(c *gin.Context) { c.Status(http.StatusOK) })
	router.GET("/api/usage/token", TokenAuthReadOnly(), func(c *gin.Context) { c.Status(http.StatusOK) })
	request := func(path, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "https://api.example.test"+path, nil)
		req.Host = "api.example.test"
		req.Header.Set("Authorization", "Bearer sk-"+key)
		req.Header.Set("Accept-Language", "zh-CN")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}

	prevServerAddr := system_setting.ServerAddress
	prevSiteSetting := adty_setting.GetSiteSetting()
	system_setting.ServerAddress = "https://www.example.test"
	if cfg := config.GlobalConfig.Get("adty_site_setting"); cfg != nil {
		require.NoError(t, config.UpdateConfigFromMap(cfg, map[string]string{"api_base_url": "https://api.example.test"}))
	}
	t.Cleanup(func() {
		system_setting.ServerAddress = prevServerAddr
		if cfg := config.GlobalConfig.Get("adty_site_setting"); cfg != nil {
			_ = config.UpdateConfigFromMap(cfg, map[string]string{"api_base_url": prevSiteSetting.APIBaseURL})
		}
	})

	for _, redisEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("redis=%t", redisEnabled), func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)

			prevDB, prevLogDB := model.DB, model.LOG_DB
			prevMain, prevLog := common.MainDatabaseType(), common.LogDatabaseType()
			prevRedis, prevRDB := common.RedisEnabled, common.RDB
			prevMaster := common.IsMasterNode
			model.DB = db
			model.LOG_DB = db
			common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
			common.IsMasterNode = false
			common.RedisEnabled = false
			common.RDB = nil
			t.Cleanup(func() {
				model.DB = prevDB
				model.LOG_DB = prevLogDB
				common.SetDatabaseTypes(prevMain, prevLog)
				common.RedisEnabled = prevRedis
				common.RDB = prevRDB
				common.IsMasterNode = prevMaster
				assert.NoError(t, sqlDB.Close())
			})

			if redisEnabled {
				mr := miniredis.RunT(t)
				client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				t.Cleanup(func() { _ = client.Close() })
				common.RDB = client
				common.RedisEnabled = true
			}

			t.Setenv("LOG_SQL_DSN", "")
			require.NoError(t, model.InitLogDB())
			require.NoError(t, db.AutoMigrate(
				&model.User{},
				&model.Token{},
				&model.UserSession{},
				&model.UserAccessToken{},
				&model.AdtySite{},
				&model.AdtyTeamMember{},
				&model.AdtyTeam{},
			))

			user := model.User{
				Username:    "deleted-key-owner",
				Role:        common.RoleCommonUser,
				Status:      common.UserStatusEnabled,
				Group:       "default",
				Quota:       1000,
				AuthVersion: 1,
			}
			require.NoError(t, db.Create(&user).Error)
			key := "deletedownerkey0123456789abcdefghijklmnopqrstuvwx"
			require.NoError(t, db.Create(&model.Token{
				UserId:         user.Id,
				Key:            key,
				Name:           "owner key",
				Status:         common.TokenStatusEnabled,
				ExpiredTime:    -1,
				UnlimitedQuota: true,
			}).Error)

			for _, path := range []string{"/v1/chat/completions", "/api/usage/token"} {
				resp := request(path, key)
				assert.Equal(t, http.StatusOK, resp.Code, "%s: %s", path, resp.Body.String())
			}

			require.NoError(t, db.Delete(&user).Error)
			if redisEnabled {
				require.NoError(t, model.InvalidateUserCache(user.Id))
			}

			for _, path := range []string{"/v1/chat/completions", "/api/usage/token"} {
				response := request(path, key)
				assert.Equal(t, http.StatusUnauthorized, response.Code, "%s: %s", path, response.Body.String())
				assert.Contains(t, response.Body.String(), `"message":"无效的令牌`, path)
			}
		})
	}
}

func TestAccessAndPanicLogsFollowLogRotation(t *testing.T) {
	previousMode, previousWriter, previousErrorWriter := gin.Mode(), gin.DefaultWriter, gin.DefaultErrorWriter
	previousLogDir := *common.LogDir
	gin.SetMode(gin.TestMode)
	*common.LogDir = t.TempDir()
	t.Cleanup(func() {
		gin.SetMode(previousMode)
		common.LogWriterMu.Lock()
		gin.DefaultWriter, gin.DefaultErrorWriter = previousWriter, previousErrorWriter
		common.LogWriterMu.Unlock()
		*common.LogDir = previousLogDir
	})

	logger.SetupLogger()
	router := gin.New()
	router.Use(PanicRecover())
	SetUpLogger(router)
	router.GET("/ok/:step", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.GET("/api/adty/telegram/webhook/:purpose/:secret", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.GET("/panic", func(c *gin.Context) { panic("rotation-panic-marker") })

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ok/before-rotation", nil))
	content, err := os.ReadFile(logger.GetCurrentLogPath())
	require.NoError(t, err)
	assert.Contains(t, string(content), "/ok/before-rotation")

	// Rotation closes the file the middleware saw at startup.
	logger.SetupLogger()
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ok/after-rotation", nil))
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/adty/telegram/webhook/notify/super-secret-token", nil))
	panicResponse := httptest.NewRecorder()
	router.ServeHTTP(panicResponse, httptest.NewRequest(http.MethodGet, "/panic", nil))
	require.Equal(t, http.StatusInternalServerError, panicResponse.Code)
	assert.Contains(t, panicResponse.Body.String(), `"type":"adtyai_panic"`)
	assert.NotContains(t, panicResponse.Body.String(), "rotation-panic-marker")

	content, err = os.ReadFile(logger.GetCurrentLogPath())
	require.NoError(t, err)
	assert.Contains(t, string(content), "/ok/after-rotation")
	assert.Contains(t, string(content), "/api/adty/telegram/webhook/notify/[REDACTED]")
	assert.NotContains(t, string(content), "super-secret-token")
	assert.Contains(t, string(content), "[Recovery]")
	assert.Contains(t, string(content), "rotation-panic-marker\n")
	assert.Contains(t, string(content), "middleware/deleted_user_auth_test.go")
}
