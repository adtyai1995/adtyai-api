package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupOptionBEpusdtTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("CRYPTO_SECRET", "0123456789abcdef0123456789abcdef")

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.AuditLog{}, &model.AuditIntent{}))

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedis := common.RedisEnabled
	oldMemCache := common.MemoryCacheEnabled
	oldOptionMap := common.OptionMap
	oldBEpusdt := setting.GetBEpusdtSetting()
	oldTopup := adty_setting.GetTopupSetting()

	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{
		setting.BEpusdtOptionKeyEnabled:              "false",
		setting.BEpusdtOptionKeyBaseURL:              "",
		setting.BEpusdtOptionKeySecret:               "",
		setting.BEpusdtOptionKeyTradeTypes:           `["usdt.trc20"]`,
		adty_setting.TopupSettingKeyUSDTOrderMinutes: "15",
		adty_setting.TopupSettingKeyEpayOrderMinutes: "10",
	}
	require.NoError(t, config.GlobalConfig.LoadFromDB(common.OptionMap))
	common.OptionMapRWMutex.Unlock()
	model.DB = db
	model.LOG_DB = db

	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		rawChains, _ := common.Marshal(oldBEpusdt.TradeTypes)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		_ = config.GlobalConfig.LoadFromDB(map[string]string{
			setting.BEpusdtOptionKeyEnabled:              "false",
			setting.BEpusdtOptionKeyBaseURL:              oldBEpusdt.BaseURL,
			setting.BEpusdtOptionKeySecret:               oldBEpusdt.Secret,
			setting.BEpusdtOptionKeyTradeTypes:           string(rawChains),
			adty_setting.TopupSettingKeyUSDTOrderMinutes: "15",
			adty_setting.TopupSettingKeyEpayOrderMinutes: "10",
		})
		if oldBEpusdt.Enabled {
			_ = config.GlobalConfig.LoadFromDB(map[string]string{setting.BEpusdtOptionKeyEnabled: "true"})
		}
		_ = oldTopup
		common.OptionMapRWMutex.Unlock()
		_ = sqlDB.Close()
	})
	return db
}

func callUpdateOption(t *testing.T, key string, val any) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"key": key, "value": val})
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", bytes.NewReader(body))
	c.Set("id", 1)
	c.Set("role", common.RoleRootUser)
	UpdateOption(c)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return rec.Code, resp
}

func TestGetOptionsExposesBEpusdtSecretConfigured(t *testing.T) {
	_ = setupOptionBEpusdtTestDB(t)

	getOptionMap := func() map[string]string {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/option/", nil)
		GetOptions(c)
		require.Equal(t, http.StatusOK, rec.Code)
		var resp struct {
			Success bool `json:"success"`
			Data    []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.True(t, resp.Success)
		out := map[string]string{}
		for _, item := range resp.Data {
			out[item.Key] = item.Value
		}
		return out
	}

	opts := getOptionMap()
	assert.Equal(t, "false", opts["BEpusdtSecretConfigured"])
	assert.NotContains(t, opts, setting.BEpusdtOptionKeySecret)

	_, resp := callUpdateOption(t, setting.BEpusdtOptionKeySecret, "super-secret-token")
	assert.Equal(t, true, resp["success"])

	opts = getOptionMap()
	assert.Equal(t, "true", opts["BEpusdtSecretConfigured"])
	assert.NotContains(t, opts, setting.BEpusdtOptionKeySecret)

	// Read-only computed option cannot be written.
	_, resp = callUpdateOption(t, "BEpusdtSecretConfigured", "true")
	assert.Equal(t, false, resp["success"])
}

func TestUpdateOptionBEpusdtAndTopupOrderMinutesValidation(t *testing.T) {
	_ = setupOptionBEpusdtTestDB(t)

	// usdt_order_minutes must be in [3, 60].
	for _, bad := range []any{0, 2, 61, "abc"} {
		_, resp := callUpdateOption(t, adty_setting.TopupSettingKeyUSDTOrderMinutes, bad)
		assert.Equal(t, false, resp["success"], "usdt_order_minutes=%v should fail", bad)
	}
	for _, good := range []any{3, 15, 60} {
		_, resp := callUpdateOption(t, adty_setting.TopupSettingKeyUSDTOrderMinutes, good)
		assert.Equal(t, true, resp["success"], "usdt_order_minutes=%v should succeed", good)
	}

	// epay_order_minutes must be in [1, 120].
	for _, bad := range []any{0, -1, 121, "abc"} {
		_, resp := callUpdateOption(t, adty_setting.TopupSettingKeyEpayOrderMinutes, bad)
		assert.Equal(t, false, resp["success"], "epay_order_minutes=%v should fail", bad)
	}
	for _, good := range []any{1, 10, 120} {
		_, resp := callUpdateOption(t, adty_setting.TopupSettingKeyEpayOrderMinutes, good)
		assert.Equal(t, true, resp["success"], "epay_order_minutes=%v should succeed", good)
	}

	// base_url validation.
	for _, bad := range []string{
		"ftp://pay.example.com",
		"https://",
		"https://user:pass@pay.example.com",
		"https://pay.example.com?a=1",
		"https://pay.example.com#frag",
		"not-a-url",
	} {
		_, resp := callUpdateOption(t, setting.BEpusdtOptionKeyBaseURL, bad)
		assert.Equal(t, false, resp["success"], "base_url=%q should fail", bad)
	}
	_, resp := callUpdateOption(t, setting.BEpusdtOptionKeyBaseURL, "  https://pay.example.com  ")
	assert.Equal(t, true, resp["success"])
	assert.Equal(t, "https://pay.example.com", setting.GetBEpusdtSetting().BaseURL)

	// trade_types validation & deduplication.
	for _, bad := range []string{`not-json`, `{}`, `["usdt.invalid"]`, `["usdt.trc20","btc"]`} {
		_, resp := callUpdateOption(t, setting.BEpusdtOptionKeyTradeTypes, bad)
		assert.Equal(t, false, resp["success"], "trade_types=%q should fail", bad)
	}
	_, resp = callUpdateOption(t, setting.BEpusdtOptionKeyTradeTypes, `["usdt.trc20","usdt.bep20","usdt.trc20"]`)
	assert.Equal(t, true, resp["success"])
	assert.Equal(t, []string{"usdt.trc20", "usdt.bep20"}, setting.GetBEpusdtSetting().TradeTypes)

	// Enabling BEpusdt without secret must fail.
	_, resp = callUpdateOption(t, setting.BEpusdtOptionKeyEnabled, true)
	assert.Equal(t, false, resp["success"])

	// Set secret, then enabling succeeds.
	_, resp = callUpdateOption(t, setting.BEpusdtOptionKeySecret, "valid-secret")
	assert.Equal(t, true, resp["success"])
	_, resp = callUpdateOption(t, setting.BEpusdtOptionKeyEnabled, true)
	assert.Equal(t, true, resp["success"])

	// Clearing required fields while enabled must fail.
	_, resp = callUpdateOption(t, setting.BEpusdtOptionKeyBaseURL, "")
	assert.Equal(t, false, resp["success"])
	_, resp = callUpdateOption(t, setting.BEpusdtOptionKeySecret, "")
	assert.Equal(t, false, resp["success"])
	_, resp = callUpdateOption(t, setting.BEpusdtOptionKeyTradeTypes, `[]`)
	assert.Equal(t, false, resp["success"])

	// Disabling first allows clearing.
	_, resp = callUpdateOption(t, setting.BEpusdtOptionKeyEnabled, false)
	assert.Equal(t, true, resp["success"])
	_, resp = callUpdateOption(t, setting.BEpusdtOptionKeyBaseURL, "")
	assert.Equal(t, true, resp["success"])
}
