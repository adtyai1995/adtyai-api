package controller

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
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
	"github.com/shopspring/decimal"
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
		&model.AdtyTopupOrder{},
		&model.AdtyPaymentSceneConfig{},
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

func generateTestRSAKeyPairPEM(t *testing.T) (privPEM, pubPEM string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privBytes, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	privPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}))
	pubPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}))
	return privPEM, pubPEM
}

func TestEpayAdminEditAllFieldsOnOrderedAndRevokedChannel(t *testing.T) {
	db := setupEpayAdminTestDB(t)

	ch := &model.AdtyEpayChannel{
		Name:           "OldGateway",
		GatewayURL:     "https://old.epay.example",
		MerchantId:     "1001",
		MerchantKey:    "old-md5-secret",
		SignType:       model.AdtyEpaySignTypeMD5,
		OrderMode:      model.AdtyEpayOrderModeAPI,
		ProbeMode:      "off",
		Status:         model.AdtyEpayStatusEnabled,
		Priority:       10,
		Weight:         1,
		UnpaidStreak:   3,
		ProbeFailCount: 2,
		Methods: []model.AdtyEpayChannelMethod{
			{Method: "alipay", TypeCode: "alipay", Enabled: true, ReturnType: "qrcode"},
		},
	}
	require.NoError(t, model.SaveAdtyEpayChannel(ch, false))

	// Existing historical order without snapshot fields yet.
	order := &model.AdtyTopupOrder{
		TradeNo:     "EPY_OLD_1",
		UserId:      10,
		Provider:    model.PaymentProviderAdtyEpay,
		Method:      "alipay",
		ChannelId:   ch.Id,
		PayAmount:   decimal.NewFromInt(50),
		PayCurrency: "CNY",
	}
	require.NoError(t, db.Create(order).Error)

	// Edit ALL fields on ordered channel (name, gateway, merchant_id, MD5 -> RSA, order_mode, methods).
	privPEM, pubPEM := generateTestRSAKeyPairPEM(t)
	updatePayload, err := common.Marshal(map[string]any{
		"name":                 "NewGatewayRSA",
		"gateway_url":          "https://new.epay.example",
		"merchant_id":          "2002",
		"sign_type":            "RSA",
		"merchant_private_key": privPEM,
		"platform_public_key":  pubPEM,
		"priority":             50,
		"weight":               5,
		"order_mode":           "submit",
		"probe_mode":           "order",
		"unpaid_threshold":     8,
		"methods": []map[string]any{
			{"method": "alipay", "display_name": "Alipay New", "type_code": "ali_v2", "min_cny": "1", "max_cny": "1000", "daily_limit_cny": "5000", "enabled": true},
			{"method": "wxpay", "display_name": "WeChat New", "type_code": "wx_v2", "min_cny": "1", "max_cny": "500", "daily_limit_cny": "2000", "enabled": true},
		},
	})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/adty/admin/epay/channels/"+strconv.Itoa(ch.Id), bytes.NewReader(updatePayload))
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(ch.Id)}}
	c.Set("id", 1)
	c.Set("role", common.RoleRootUser)
	UpdateAdtyEpayChannel(c)
	require.Contains(t, rec.Body.String(), `"success":true`, rec.Body.String())

	updated, err := model.GetAdtyEpayChannel(ch.Id)
	require.NoError(t, err)
	assert.Equal(t, "NewGatewayRSA", updated.Name)
	assert.Equal(t, "https://new.epay.example", updated.GatewayURL)
	assert.Equal(t, "2002", updated.MerchantId)
	assert.Equal(t, model.AdtyEpaySignTypeRSA, updated.SignType)
	assert.Empty(t, updated.MerchantKey)
	assert.True(t, updated.HasCredentials())
	assert.Equal(t, model.AdtyEpayOrderModeSubmit, updated.OrderMode)
	assert.Equal(t, int64(1), updated.ConfigVersion)
	assert.Equal(t, 0, updated.UnpaidStreak)
	assert.Equal(t, 0, updated.ProbeFailCount)
	assert.Len(t, updated.Methods, 2)

	// Historical order received pre-edit snapshots and no longer matches the new channel identity.
	var reloadedOrder model.AdtyTopupOrder
	require.NoError(t, db.Where("trade_no = ?", "EPY_OLD_1").First(&reloadedOrder).Error)
	assert.Equal(t, "OldGateway", reloadedOrder.ChannelName)
	assert.Equal(t, "1001", reloadedOrder.ProviderMerchantId)
	assert.Equal(t, "https://old.epay.example", reloadedOrder.CheckoutGateway)
	assert.False(t, model.AdtyEpayOrderMatchesChannelMerchantGateway(&reloadedOrder, updated))
	assert.False(t, model.AdtyEpayOrderMatchesChannelIdentity(&reloadedOrder, updated))

	// Revoke credentials, then re-edit with new MD5 credentials -> restores to disabled.
	recRevoke := httptest.NewRecorder()
	cRevoke, _ := gin.CreateTestContext(recRevoke)
	cRevoke.Request = httptest.NewRequest(http.MethodPost, "/api/adty/admin/epay/channels/"+strconv.Itoa(ch.Id)+"/revoke", nil)
	cRevoke.Params = gin.Params{{Key: "id", Value: strconv.Itoa(ch.Id)}}
	cRevoke.Set("id", 1)
	cRevoke.Set("role", common.RoleRootUser)
	RevokeAdtyEpayChannel(cRevoke)
	require.Contains(t, recRevoke.Body.String(), `"success":true`)

	revokedCh, err := model.GetAdtyEpayChannel(ch.Id)
	require.NoError(t, err)
	assert.Equal(t, model.AdtyEpayStatusRevoked, revokedCh.Status)

	restorePayload, err := common.Marshal(map[string]any{
		"name":         "RestoredMD5",
		"gateway_url":  "https://restored.epay.example",
		"merchant_id":  "3003",
		"sign_type":    "MD5",
		"merchant_key": "restored-secret",
		"priority":     1,
		"weight":       1,
		"order_mode":   "api",
		"probe_mode":   "off",
		"methods": []map[string]any{
			{"method": "alipay", "type_code": "alipay", "enabled": true},
		},
	})
	require.NoError(t, err)
	recRestore := httptest.NewRecorder()
	cRestore, _ := gin.CreateTestContext(recRestore)
	cRestore.Request = httptest.NewRequest(http.MethodPut, "/api/adty/admin/epay/channels/"+strconv.Itoa(ch.Id), bytes.NewReader(restorePayload))
	cRestore.Params = gin.Params{{Key: "id", Value: strconv.Itoa(ch.Id)}}
	cRestore.Set("id", 1)
	cRestore.Set("role", common.RoleRootUser)
	UpdateAdtyEpayChannel(cRestore)
	require.Contains(t, recRestore.Body.String(), `"success":true`, recRestore.Body.String())

	restoredCh, err := model.GetAdtyEpayChannel(ch.Id)
	require.NoError(t, err)
	assert.Equal(t, model.AdtyEpayStatusDisabled, restoredCh.Status)
	assert.Equal(t, model.AdtyEpaySignTypeMD5, restoredCh.SignType)
	assert.True(t, restoredCh.HasCredentials())
}

func TestEpayAdminDeleteChannelWithOrdersAndSafeSceneCleanup(t *testing.T) {
	db := setupEpayAdminTestDB(t)

	chA := &model.AdtyEpayChannel{
		Name:        "Futong",
		GatewayURL:  "https://a.epay.example",
		MerchantId:  "1001",
		MerchantKey: "secret-a",
		SignType:    model.AdtyEpaySignTypeMD5,
		OrderMode:   model.AdtyEpayOrderModeAPI,
		Status:      model.AdtyEpayStatusDisabled,
		Methods:     []model.AdtyEpayChannelMethod{{Method: "alipay", TypeCode: "alipay", Enabled: true}},
	}
	chB := &model.AdtyEpayChannel{
		Name:        "Backup",
		GatewayURL:  "https://b.epay.example",
		MerchantId:  "1002",
		MerchantKey: "secret-b",
		SignType:    model.AdtyEpaySignTypeMD5,
		OrderMode:   model.AdtyEpayOrderModeAPI,
		Status:      model.AdtyEpayStatusEnabled,
		Methods:     []model.AdtyEpayChannelMethod{{Method: "alipay", TypeCode: "alipay", Enabled: true}},
	}
	require.NoError(t, model.SaveAdtyEpayChannel(chA, false))
	require.NoError(t, model.SaveAdtyEpayChannel(chB, false))

	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo:   "EPY_DEL_1",
		UserId:    10,
		Provider:  model.PaymentProviderAdtyEpay,
		Method:    "alipay",
		ChannelId: chA.Id,
	}).Error)

	scenes := []model.AdtyPaymentScene{
		{
			Key: "multi_channel", Name: "Multi Channel", Business: model.AdtyBusinessWalletTopup, Enabled: true,
			Providers: []string{model.PaymentProviderAdtyEpay}, ChannelIDs: []int{chA.Id, chB.Id}, SiteScope: "all",
		},
		{
			Key: "sole_multi_provider", Name: "Sole Multi Provider", Business: model.AdtyBusinessWalletTopup, Enabled: true,
			Providers: []string{model.PaymentProviderAdtyEpay, model.PaymentProviderAdtyBEpusdt}, ChannelIDs: []int{chA.Id}, SiteScope: "all",
		},
		{
			Key: "sole_epay_only", Name: "Sole Epay Only", Business: model.AdtyBusinessWalletTopup, Enabled: true,
			Providers: []string{model.PaymentProviderAdtyEpay}, ChannelIDs: []int{chA.Id}, SiteScope: "all",
		},
	}
	rawScenes, err := common.Marshal(scenes)
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.AdtyPaymentSceneConfig{ID: 1, Version: 1, Payload: string(rawScenes)}).Error)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/adty/admin/epay/channels/"+strconv.Itoa(chA.Id), nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(chA.Id)}}
	c.Set("id", 1)
	c.Set("role", common.RoleRootUser)
	DeleteAdtyEpayChannel(c)
	require.Contains(t, rec.Body.String(), `"success":true`, rec.Body.String())

	var delResp struct {
		Success bool                              `json:"success"`
		Data    model.AdtyEpayChannelDeleteResult `json:"data"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &delResp))
	assert.Equal(t, 3, delResp.Data.UpdatedScenes)
	assert.Equal(t, 1, delResp.Data.DisabledScenes)

	// Channel A is deleted; historical order preserves channel_name snapshot.
	_, err = model.GetAdtyEpayChannel(chA.Id)
	assert.ErrorIs(t, err, model.ErrAdtyEpayChannelNotFound)

	var order model.AdtyTopupOrder
	require.NoError(t, db.Where("trade_no = ?", "EPY_DEL_1").First(&order).Error)
	assert.Equal(t, "Futong", order.ChannelName)

	// Payment scenes updated safely without widening restricted scenes.
	var cfg model.AdtyPaymentSceneConfig
	require.NoError(t, db.First(&cfg, 1).Error)
	var updatedScenes []model.AdtyPaymentScene
	require.NoError(t, common.UnmarshalJsonStr(cfg.Payload, &updatedScenes))
	require.Len(t, updatedScenes, 3)

	assert.Equal(t, []int{chB.Id}, updatedScenes[0].ChannelIDs)
	assert.True(t, updatedScenes[0].Enabled)

	assert.Equal(t, []string{model.PaymentProviderAdtyBEpusdt}, updatedScenes[1].Providers)
	assert.Empty(t, updatedScenes[1].ChannelIDs)
	assert.True(t, updatedScenes[1].Enabled)

	assert.False(t, updatedScenes[2].Enabled)
	assert.Empty(t, updatedScenes[2].ChannelIDs)
}

func TestEpayAdminReplaceChannelNewAndExisting(t *testing.T) {
	db := setupEpayAdminTestDB(t)

	chOld := &model.AdtyEpayChannel{
		Name:        "OldCh",
		GatewayURL:  "https://old.epay.example",
		MerchantId:  "1001",
		MerchantKey: "secret-old",
		SignType:    model.AdtyEpaySignTypeMD5,
		OrderMode:   model.AdtyEpayOrderModeAPI,
		Status:      model.AdtyEpayStatusEnabled,
		Methods:     []model.AdtyEpayChannelMethod{{Method: "alipay", TypeCode: "alipay", Enabled: true}},
	}
	chExisting := &model.AdtyEpayChannel{
		Name:        "StandbyCh",
		GatewayURL:  "https://standby.epay.example",
		MerchantId:  "1009",
		MerchantKey: "secret-standby",
		SignType:    model.AdtyEpaySignTypeMD5,
		OrderMode:   model.AdtyEpayOrderModeAPI,
		Status:      model.AdtyEpayStatusDisabled,
		Methods:     []model.AdtyEpayChannelMethod{{Method: "alipay", TypeCode: "alipay", Enabled: true}},
	}
	require.NoError(t, model.SaveAdtyEpayChannel(chOld, false))
	require.NoError(t, model.SaveAdtyEpayChannel(chExisting, false))

	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo:   "EPY_REP_1",
		UserId:    11,
		Provider:  model.PaymentProviderAdtyEpay,
		Method:    "alipay",
		ChannelId: chOld.Id,
	}).Error)

	rawScenes, err := common.Marshal([]model.AdtyPaymentScene{{
		Key: "wallet_topup", Name: "Wallet", Business: model.AdtyBusinessWalletTopup, Enabled: true,
		Providers: []string{model.PaymentProviderAdtyEpay}, ChannelIDs: []int{chOld.Id}, SiteScope: "all",
	}})
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.AdtyPaymentSceneConfig{ID: 1, Version: 1, Payload: string(rawScenes)}).Error)

	// 1. Replace OldCh with a brand-new channel, keeping OldCh disabled.
	body1, err := common.Marshal(map[string]any{
		"enable_target": true,
		"delete_old":    false,
		"new_channel": map[string]any{
			"name":         "FreshCh",
			"gateway_url":  "https://fresh.epay.example",
			"merchant_id":  "2001",
			"sign_type":    "MD5",
			"merchant_key": "secret-fresh",
			"priority":     10,
			"weight":       2,
			"order_mode":   "api",
			"probe_mode":   "off",
			"methods": []map[string]any{
				{"method": "alipay", "type_code": "alipay", "enabled": true},
			},
		},
	})
	require.NoError(t, err)
	rec1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(rec1)
	c1.Request = httptest.NewRequest(http.MethodPost, "/api/adty/admin/epay/channels/"+strconv.Itoa(chOld.Id)+"/replace", bytes.NewReader(body1))
	c1.Params = gin.Params{{Key: "id", Value: strconv.Itoa(chOld.Id)}}
	c1.Set("id", 1)
	c1.Set("role", common.RoleRootUser)
	ReplaceAdtyEpayChannel(c1)
	require.Contains(t, rec1.Body.String(), `"success":true`, rec1.Body.String())

	var rep1 struct {
		Success bool                               `json:"success"`
		Data    model.AdtyEpayChannelReplaceResult `json:"data"`
	}
	require.NoError(t, common.Unmarshal(rec1.Body.Bytes(), &rep1))
	assert.Equal(t, chOld.Id, rep1.Data.OldID)
	assert.Greater(t, rep1.Data.NewID, 0)
	assert.Equal(t, "FreshCh", rep1.Data.NewName)
	assert.Equal(t, 1, rep1.Data.UpdatedScenes)
	assert.False(t, rep1.Data.DeletedOld)

	oldAfter, err := model.GetAdtyEpayChannel(chOld.Id)
	require.NoError(t, err)
	assert.Equal(t, model.AdtyEpayStatusDisabled, oldAfter.Status)

	freshAfter, err := model.GetAdtyEpayChannel(rep1.Data.NewID)
	require.NoError(t, err)
	assert.Equal(t, model.AdtyEpayStatusEnabled, freshAfter.Status)

	// 2. Replace FreshCh with existing StandbyCh and delete FreshCh.
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo:   "EPY_REP_2",
		UserId:    12,
		Provider:  model.PaymentProviderAdtyEpay,
		Method:    "alipay",
		ChannelId: rep1.Data.NewID,
	}).Error)

	body2, err := common.Marshal(map[string]any{
		"target_channel_id": chExisting.Id,
		"enable_target":     true,
		"delete_old":        true,
	})
	require.NoError(t, err)
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/api/adty/admin/epay/channels/"+strconv.Itoa(rep1.Data.NewID)+"/replace", bytes.NewReader(body2))
	c2.Params = gin.Params{{Key: "id", Value: strconv.Itoa(rep1.Data.NewID)}}
	c2.Set("id", 1)
	c2.Set("role", common.RoleRootUser)
	ReplaceAdtyEpayChannel(c2)
	require.Contains(t, rec2.Body.String(), `"success":true`, rec2.Body.String())

	_, err = model.GetAdtyEpayChannel(rep1.Data.NewID)
	assert.ErrorIs(t, err, model.ErrAdtyEpayChannelNotFound)

	standbyAfter, err := model.GetAdtyEpayChannel(chExisting.Id)
	require.NoError(t, err)
	assert.Equal(t, model.AdtyEpayStatusEnabled, standbyAfter.Status)

	var cfg model.AdtyPaymentSceneConfig
	require.NoError(t, db.First(&cfg, 1).Error)
	var scenes []model.AdtyPaymentScene
	require.NoError(t, common.UnmarshalJsonStr(cfg.Payload, &scenes))
	assert.Equal(t, []int{chExisting.Id}, scenes[0].ChannelIDs)

	var order2 model.AdtyTopupOrder
	require.NoError(t, db.Where("trade_no = ?", "EPY_REP_2").First(&order2).Error)
	assert.Equal(t, "FreshCh", order2.ChannelName)

	// 3. Reject self-replacement.
	bodySelf, err := common.Marshal(map[string]any{
		"target_channel_id": chExisting.Id,
		"enable_target":     true,
		"delete_old":        false,
	})
	require.NoError(t, err)
	recSelf := httptest.NewRecorder()
	cSelf, _ := gin.CreateTestContext(recSelf)
	cSelf.Request = httptest.NewRequest(http.MethodPost, "/api/adty/admin/epay/channels/"+strconv.Itoa(chExisting.Id)+"/replace", bytes.NewReader(bodySelf))
	cSelf.Params = gin.Params{{Key: "id", Value: strconv.Itoa(chExisting.Id)}}
	cSelf.Set("id", 1)
	cSelf.Set("role", common.RoleRootUser)
	ReplaceAdtyEpayChannel(cSelf)
	assert.Contains(t, recSelf.Body.String(), `"success":false`)
}
