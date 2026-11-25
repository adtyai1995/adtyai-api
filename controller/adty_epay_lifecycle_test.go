package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupEpayLifecycleTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&model.Option{},
		&model.User{},
		&model.TopUp{},
		&model.AdtyTopupOrder{},
		&model.AdtyEpayChannel{},
		&model.AdtyEpayChannelMethod{},
		&model.AdtyPaymentSceneConfig{},
		&model.AdtySite{},
		&model.AdtySiteWallet{},
		&model.AdtySiteWalletLedger{},
		&model.AdtySiteStaff{},
		&model.AdtyTeam{},
		&model.AdtyTeamMember{},
		&model.AdtyTeamLedger{},
		&model.AdtyOrderPaymentEvent{},
		&model.PaymentOrphanEvent{},
		&model.AdtyFundBatch{},
		&model.AdtyBalanceLedger{},
		&model.ReferralAffiliate{},
		&model.ReferralBinding{},
		&model.ReferralCommission{},
		&model.ReferralCommissionJob{},
		&model.AdtySiteInviteCommission{},
		&model.AdtyGiftFulfillment{},
		&model.Log{},
	))

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedis := common.RedisEnabled
	oldMem := common.MemoryCacheEnabled
	oldPrice := operation_setting.GetPrice()
	oldMinTopUp := operation_setting.GetMinTopUp()
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	model.DB = db
	model.LOG_DB = db
	operation_setting.SetPrice(7.0)
	operation_setting.SetMinTopUp(1)
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMem
		operation_setting.SetPrice(oldPrice)
		operation_setting.SetMinTopUp(oldMinTopUp)
		_ = sqlDB.Close()
	})
	return db
}

func seedTestPaymentScenes(t *testing.T, db *gorm.DB) {
	t.Helper()
	scenes := []model.AdtyPaymentScene{
		{
			Key: "wallet_topup", Name: "Wallet", Business: model.AdtyBusinessWalletTopup, Enabled: true,
			Providers: []string{model.PaymentProviderAdtyBEpusdt, model.PaymentProviderAdtyEpay},
			Methods:   []string{"alipay", "usdt.trc20"}, SiteScope: "all",
			MinUSD: decimal.NewFromInt(1), MaxUSD: decimal.NewFromInt(10000),
		},
		{
			Key: "owner_margin_topup", Name: "Margin", Business: model.AdtyBusinessOwnerMarginTopup, Enabled: true,
			Providers: []string{model.PaymentProviderAdtyBEpusdt, model.PaymentProviderAdtyEpay},
			Methods:   []string{"alipay", "usdt.trc20"}, SiteScope: "all",
			MinUSD: decimal.NewFromInt(1), MaxUSD: decimal.NewFromInt(10000),
		},
		{
			Key: "team_topup", Name: "Team", Business: model.AdtyBusinessTeamTopup, Enabled: true,
			Providers: []string{model.PaymentProviderAdtyBEpusdt, model.PaymentProviderAdtyEpay},
			Methods:   []string{"alipay", "usdt.trc20"}, SiteScope: "all",
			MinUSD: decimal.NewFromInt(1), MaxUSD: decimal.NewFromInt(10000),
		},
	}
	raw, err := common.Marshal(scenes)
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.AdtyPaymentSceneConfig{
		ID:      1,
		Version: 1,
		Payload: string(raw),
	}).Error)
}

func TestCompleteAdtyEpayCallbackRejectsEmptyGatewayTradeNo(t *testing.T) {
	db := setupEpayLifecycleTestDB(t)
	now := time.Now().Unix()

	channel := &model.AdtyEpayChannel{
		Name:        "Epay-CB",
		GatewayURL:  "https://pay.example.com",
		MerchantId:  "2001",
		MerchantKey: "cb-secret",
		SignType:    model.AdtyEpaySignTypeMD5,
		OrderMode:   model.AdtyEpayOrderModeSubmit,
		Status:      model.AdtyEpayStatusEnabled,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.AdtyEpayChannelMethod{
		ChannelId: channel.Id,
		Method:    "alipay",
		TypeCode:  "alipay",
		Enabled:   true,
	}).Error)

	require.NoError(t, db.Create(&model.User{
		Id:       51,
		Username: "epay_buyer",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}).Error)
	seedPendingTopup(t, db, 51, "EPY_CB_NOTRADE", model.PaymentProviderAdtyEpay, "alipay", "wallet_topup",
		decimal.NewFromInt(10), decimal.Zero, decimal.NewFromInt(70), "CNY", now+600, channel.Id, "", "https://pay.example.com/submit")

	params := map[string]string{
		"pid":          "2001",
		"trade_no":     "   ",
		"out_trade_no": "EPY_CB_NOTRADE",
		"type":         "alipay",
		"money":        "70.00",
		"trade_status": "TRADE_SUCCESS",
	}
	err := completeAdtyEpayCallback(channel, "EPY_CB_NOTRADE", params, "{}", "127.0.0.1")
	assert.ErrorIs(t, err, model.ErrPaymentMethodMismatch)

	// Full HTTP notify with empty trade_no queues orphan review and replies success (never credits).
	signed, err := service.EpaySignChannelParams(channel, map[string]string{
		"out_trade_no": "EPY_CB_NOTRADE",
		"type":         "alipay",
		"money":        "70.00",
		"trade_status": "TRADE_SUCCESS",
	})
	require.NoError(t, err)
	q := url.Values{}
	for k, v := range signed {
		q.Set(k, v)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/user/epay/notify/"+strconv.Itoa(channel.Id)+"?"+q.Encode(), nil)
	c.Params = gin.Params{{Key: "channelId", Value: strconv.Itoa(channel.Id)}}
	AdtyEpayNotify(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "success", w.Body.String())

	var top model.TopUp
	require.NoError(t, db.Where("trade_no = ?", "EPY_CB_NOTRADE").First(&top).Error)
	assert.Equal(t, common.TopUpStatusPending, top.Status)
	var orphanCount int64
	require.NoError(t, db.Model(&model.PaymentOrphanEvent{}).Where("reference_id = ?", "EPY_CB_NOTRADE").Count(&orphanCount).Error)
	assert.Equal(t, int64(1), orphanCount)
}

func TestRequestAdtyEpayPayAutoPickDailyLimitFallbackAndOwnerMarginQuotaBypass(t *testing.T) {
	db := setupEpayLifecycleTestDB(t)
	now := time.Now().Unix()

	require.NoError(t, db.Create(&model.Option{
		Key:   adty_setting.EpaySettingKeyChannelTimeout,
		Value: "5",
	}).Error)
	require.NoError(t, db.Create(&model.Option{
		Key:   adty_setting.EpaySettingKeyTotalTimeout,
		Value: "15",
	}).Error)

	seedTestPaymentScenes(t, db)

	ch1 := &model.AdtyEpayChannel{
		Name:        "Ch1-Full",
		GatewayURL:  "https://ch1.example.com",
		MerchantId:  "1001",
		MerchantKey: "k1",
		SignType:    model.AdtyEpaySignTypeMD5,
		OrderMode:   model.AdtyEpayOrderModeSubmit,
		Priority:    100,
		Weight:      1,
		Status:      model.AdtyEpayStatusEnabled,
	}
	ch2 := &model.AdtyEpayChannel{
		Name:        "Ch2-Open",
		GatewayURL:  "https://ch2.example.com",
		MerchantId:  "1002",
		MerchantKey: "k2",
		SignType:    model.AdtyEpaySignTypeMD5,
		OrderMode:   model.AdtyEpayOrderModeSubmit,
		Priority:    50,
		Weight:      1,
		Status:      model.AdtyEpayStatusEnabled,
	}
	require.NoError(t, db.Create(ch1).Error)
	require.NoError(t, db.Create(ch2).Error)
	require.NoError(t, db.Create(&model.AdtyEpayChannelMethod{
		ChannelId:     ch1.Id,
		Method:        "alipay",
		TypeCode:      "alipay",
		DailyLimitCNY: decimal.NewFromInt(100),
		DisplayName:   "Alipay-1",
		Enabled:       true,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyEpayChannelMethod{
		ChannelId:     ch2.Id,
		Method:        "alipay",
		TypeCode:      "alipay",
		DailyLimitCNY: decimal.NewFromInt(1000),
		DisplayName:   "Alipay-2",
		Enabled:       true,
	}).Error)

	// Fill ch1's daily room with an unexpired pending order of 80 CNY (room left = 20 CNY, order is 70 CNY).
	// Even if PickAdtyEpayChannel returned ch1 in a race, RequestAdtyEpayPay retries ch2 on ErrAdtyEpayDailyLimit.
	require.NoError(t, db.Create(&model.User{
		Id:       60,
		Username: "buyer60",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}).Error)
	seedPendingTopup(t, db, 999, "EPY_FILL_CH1", model.PaymentProviderAdtyEpay, "alipay", "wallet_topup",
		decimal.NewFromInt(12), decimal.Zero, decimal.NewFromInt(80), "CNY", now+600, ch1.Id, "", "https://ch1.example.com/x")

	t.Run("auto-pick succeeds on ch2 when ch1 daily room is full", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/adty/epay/pay", strings.NewReader(`{"amount":10,"method":"alipay","device":"pc"}`))
		c.Request = c.Request.WithContext(context.Background())
		c.Set("id", 60)
		RequestAdtyEpayPay(c)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Success bool                `json:"success"`
			Data    model.AdtyTopUpView `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success, w.Body.String())
		require.NotNil(t, resp.Data.Ext)
		assert.Equal(t, ch2.Id, resp.Data.Ext.ChannelId)
	})

	t.Run("owner_margin_topup skips personal ValidateTopUpQuotaCapacity even at MaxWalletQuota", func(t *testing.T) {
		require.NoError(t, db.Create(&model.User{
			Id:       61,
			Username: "owner61",
			Role:     common.RoleCommonUser,
			Status:   common.UserStatusEnabled,
			Quota:    common.MaxWalletQuota,
		}).Error)
		site := &model.AdtySite{
			Id:          10,
			Subdomain:   "ownersite",
			OwnerUserId: 61,
			Status:      model.AdtySiteStatusActive,
		}
		require.NoError(t, db.Create(site).Error)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/adty/epay/pay", strings.NewReader(`{"business":"owner_margin_topup","amount":10,"method":"alipay","channel_id":`+strconv.Itoa(ch2.Id)+`}`))
		c.Set("id", 61)
		RequestAdtyEpayPay(c)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Success bool                `json:"success"`
			Message string              `json:"message"`
			Data    model.AdtyTopUpView `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success, w.Body.String())
		require.NotNil(t, resp.Data.Ext)
		assert.Equal(t, model.AdtyBusinessOwnerMarginTopup, resp.Data.Ext.Business)
	})
}
