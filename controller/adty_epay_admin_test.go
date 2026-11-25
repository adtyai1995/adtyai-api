package controller

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupEpayAdminTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("CRYPTO_SECRET", "0123456789abcdef0123456789abcdef")
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.AdtyEpayChannel{},
		&model.AdtyEpayChannelMethod{},
		&model.AuditLog{},
		&model.AuditIntent{},
	))

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedis := common.RedisEnabled
	oldMemCache := common.MemoryCacheEnabled
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	model.DB = db
	model.LOG_DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		_ = sqlDB.Close()
	})
	return db
}

func TestEpayAdminToChannelRejectsDuplicateEffectiveTypeCode(t *testing.T) {
	baseReq := func(alipayType, wxpayType string) adtyEpayChannelRequest {
		var req adtyEpayChannelRequest
		req.Name = "Ch1"
		req.GatewayURL = "https://pay.example.com"
		req.MerchantId = "1001"
		req.MerchantKey = "secret"
		req.Methods = append(req.Methods,
			struct {
				Method        string                `json:"method"`
				DisplayName   string                `json:"display_name"`
				TypeCode      string                `json:"type_code"`
				MinCNY        common.BoundedDecimal `json:"min_cny"`
				MaxCNY        common.BoundedDecimal `json:"max_cny"`
				DailyLimitCNY common.BoundedDecimal `json:"daily_limit_cny"`
				Enabled       bool                  `json:"enabled"`
				ReturnType    string                `json:"return_type"`
			}{Method: "alipay", TypeCode: alipayType, Enabled: true},
			struct {
				Method        string                `json:"method"`
				DisplayName   string                `json:"display_name"`
				TypeCode      string                `json:"type_code"`
				MinCNY        common.BoundedDecimal `json:"min_cny"`
				MaxCNY        common.BoundedDecimal `json:"max_cny"`
				DailyLimitCNY common.BoundedDecimal `json:"daily_limit_cny"`
				Enabled       bool                  `json:"enabled"`
				ReturnType    string                `json:"return_type"`
			}{Method: "wxpay", TypeCode: wxpayType, Enabled: true},
		)
		return req
	}

	// Implicit empty type_code ("alipay") colliding with wxpay's explicit "alipay".
	dupImplicit := baseReq("", "alipay")
	_, err := dupImplicit.toChannel()
	require.Error(t, err)

	// Explicit duplicate type_code.
	dupExplicit := baseReq("shared_code", "shared_code")
	_, err = dupExplicit.toChannel()
	require.Error(t, err)

	// Distinct effective type codes succeed.
	valid := baseReq("", "wxpay_h5")
	ch, err := valid.toChannel()
	require.NoError(t, err)
	assert.Len(t, ch.Methods, 2)
}

func TestAdtyEpayMethodNotOpenNarrowed(t *testing.T) {
	for _, msg := range []string{
		"不支持该支付方式",
		"支付方式未开启",
		"该商户未开通此通道",
		"支付方式不存在",
		"通道未开启",
		"payment method not supported",
	} {
		assert.True(t, adtyEpayMethodNotOpen(errors.New(msg)), "expected match for %q", msg)
	}
	for _, msg := range []string{
		"支付方式参数错误",
		"该支付方式金额超出单笔限制",
		"支付方式请求超时",
		"签名校验失败",
	} {
		assert.False(t, adtyEpayMethodNotOpen(errors.New(msg)), "should NOT false-positive on %q", msg)
	}
}

func TestSetAdtyEpayChannelStatusBlocksEnableOnDecryptError(t *testing.T) {
	db := setupEpayAdminTestDB(t)

	// Insert a channel with an undecryptable sealed merchant_key directly via SQL.
	require.NoError(t, db.Exec(
		`INSERT INTO adty_epay_channels (name, gateway_url, merchant_id, merchant_key, sign_type, order_mode, probe_mode, status, status_reason, priority, weight) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"BrokenCh", "https://pay.example.com", "1001", "adty:secret:v1:corrupted-ciphertext",
		model.AdtyEpaySignTypeMD5, model.AdtyEpayOrderModeAPI, "off", model.AdtyEpayStatusDisabled, model.AdtyEpayReasonManual, 1, 1,
	).Error)

	var id int
	require.NoError(t, db.Raw(`SELECT id FROM adty_epay_channels WHERE name = 'BrokenCh'`).Scan(&id).Error)
	require.Greater(t, id, 0)

	// Enabling must be rejected.
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/adty/epay/channels/"+strconv.Itoa(id)+"/status", bytes.NewBufferString(`{"status":"enabled"}`))
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(id)}}
	c.Set("id", 1)
	c.Set("role", common.RoleRootUser)
	SetAdtyEpayChannelStatus(c)
	assert.Contains(t, rec.Body.String(), `"success":false`)

	ch, err := model.GetAdtyEpayChannel(id)
	require.NoError(t, err)
	assert.True(t, ch.CredentialDecryptErr)
	assert.Equal(t, model.AdtyEpayStatusDisabled, ch.Status)

	// Putting it into maintenance remains allowed.
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = httptest.NewRequest(http.MethodPut, "/api/adty/epay/channels/"+strconv.Itoa(id)+"/status", bytes.NewBufferString(`{"status":"maintenance"}`))
	c2.Params = gin.Params{{Key: "id", Value: strconv.Itoa(id)}}
	c2.Set("id", 1)
	c2.Set("role", common.RoleRootUser)
	SetAdtyEpayChannelStatus(c2)
	assert.Contains(t, rec2.Body.String(), `"success":true`)
}
