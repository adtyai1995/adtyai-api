package controller

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTopupReuseCapTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.TopUp{},
		&model.AdtyTopupOrder{},
		&model.AdtyEpayChannel{},
		&model.AdtyEpayChannelMethod{},
		&model.AdtySite{},
	))

	oldDB := model.DB
	oldRedis := common.RedisEnabled
	oldMemCache := common.MemoryCacheEnabled
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		_ = sqlDB.Close()
	})
	return db
}

func seedPendingTopup(t *testing.T, db *gorm.DB, userID int, tradeNo, provider, method, scene string, faceUSD, bonusUSD, payAmount decimal.Decimal, currency string, expireAt int64, channelID int, payAddr, payURL string) {
	t.Helper()
	creditQuota := faceUSD.Add(bonusUSD).Mul(common.QuotaPerUnitDecimal()).IntPart()
	require.NoError(t, db.Create(&model.TopUp{
		UserId:          userID,
		Amount:          faceUSD.IntPart(),
		Money:           payAmount.InexactFloat64(),
		TradeNo:         tradeNo,
		PaymentMethod:   method,
		PaymentProvider: provider,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		Business:     model.AdtyBusinessWalletTopup,
		TradeNo:      tradeNo,
		UserId:       userID,
		Provider:     provider,
		PaymentScene: scene,
		Method:       method,
		FaceUSD:      faceUSD,
		BonusUSD:     bonusUSD,
		CreditQuota:  creditQuota,
		PayAmount:    payAmount,
		PayCurrency:  currency,
		ExpireAt:     expireAt,
		ChannelId:    channelID,
		PayAddress:   payAddr,
		PayURL:       payURL,
	}).Error)
}

func newTestGinContext(userID int) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Set("id", userID)
	return c, w
}

func TestFindReusablePendingOrCheckCapBehavior(t *testing.T) {
	db := setupTopupReuseCapTestDB(t)
	now := time.Now().Unix()

	require.NoError(t, db.Create(&model.User{
		Id:       42,
		Username: "buyer",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}).Error)

	channel := &model.AdtyEpayChannel{
		Name:        "Alipay-1",
		GatewayURL:  "https://pay.example.com",
		MerchantId:  "1001",
		MerchantKey: "secret-key",
		SignType:    model.AdtyEpaySignTypeMD5,
		OrderMode:   model.AdtyEpayOrderModeAPI,
		Status:      model.AdtyEpayStatusEnabled,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.AdtyEpayChannelMethod{
		ChannelId:   channel.Id,
		Method:      "alipay",
		TypeCode:    "alipay",
		DisplayName: "Alipay-1",
		Enabled:     true,
	}).Error)

	// Seed an active Epay order and an active BEpusdt order (with gateway-adjusted 10.0007 USDT).
	seedPendingTopup(t, db, 42, "EPY_ACTIVE_1", model.PaymentProviderAdtyEpay, "alipay", "wallet_topup",
		decimal.NewFromInt(10), decimal.Zero, decimal.NewFromInt(67), "CNY", now+900, channel.Id, "", "https://pay.example.com/p/1")
	seedPendingTopup(t, db, 42, "EPU_ACTIVE_1", model.PaymentProviderAdtyBEpusdt, "usdt.bep20", "wallet_topup",
		decimal.NewFromInt(10), decimal.NewFromInt(2), decimal.RequireFromString("10.0007"), "USDT", now+900, 0, "0xabc123", "")

	t.Run("reuses matching BEpusdt order even with adjusted decimals and leaves Epay order untouched", func(t *testing.T) {
		c, _ := newTestGinContext(42)
		quote := &service.AdtyQuote{
			Business:    model.AdtyBusinessWalletTopup,
			FaceUSD:     decimal.NewFromInt(10),
			BonusUSD:    decimal.NewFromInt(2),
			CreditQuota: 12 * common.QuotaPerUnitDecimal().IntPart(),
			PayAmount:   decimal.NewFromInt(10),
			PayCurrency: "USDT",
		}
		reused, err := findReusablePendingOrCheckCap(c, adtyPendingReuseFilter{
			paymentScene: "wallet_topup",
			provider:     model.PaymentProviderAdtyBEpusdt,
			method:       "usdt.bep20",
			quote:        quote,
		})
		require.NoError(t, err)
		require.NotNil(t, reused)
		assert.Equal(t, "EPU_ACTIVE_1", reused.TradeNo)

		// Verify existing Epay order was NOT replaced or mutated.
		var epayTopUp model.TopUp
		require.NoError(t, db.Where("trade_no = ?", "EPY_ACTIVE_1").First(&epayTopUp).Error)
		assert.Equal(t, common.TopUpStatusPending, epayTopUp.Status)
	})

	t.Run("reuses matching Epay order when channel enabled and skips reuse when excluded or different channel", func(t *testing.T) {
		c, _ := newTestGinContext(42)
		quote := &service.AdtyQuote{
			Business:    model.AdtyBusinessWalletTopup,
			FaceUSD:     decimal.NewFromInt(10),
			BonusUSD:    decimal.Zero,
			CreditQuota: 10 * common.QuotaPerUnitDecimal().IntPart(),
			PayAmount:   decimal.NewFromInt(67),
			PayCurrency: "CNY",
		}
		reused, err := findReusablePendingOrCheckCap(c, adtyPendingReuseFilter{
			paymentScene:     "wallet_topup",
			provider:         model.PaymentProviderAdtyEpay,
			method:           "alipay",
			quote:            quote,
			requestedChannel: channel.Id,
		})
		require.NoError(t, err)
		require.NotNil(t, reused)
		assert.Equal(t, "EPY_ACTIVE_1", reused.TradeNo)

		// Excluded channel should NOT reuse.
		reused, err = findReusablePendingOrCheckCap(c, adtyPendingReuseFilter{
			paymentScene:     "wallet_topup",
			provider:         model.PaymentProviderAdtyEpay,
			method:           "alipay",
			quote:            quote,
			excludedChannels: map[int]bool{channel.Id: true},
		})
		require.NoError(t, err)
		assert.Nil(t, reused)
	})

	t.Run("does not reuse when bonus quote changed", func(t *testing.T) {
		c, _ := newTestGinContext(42)
		quote := &service.AdtyQuote{
			Business:    model.AdtyBusinessWalletTopup,
			FaceUSD:     decimal.NewFromInt(10),
			BonusUSD:    decimal.Zero, // changed from $2 bonus to $0
			CreditQuota: 10 * common.QuotaPerUnitDecimal().IntPart(),
			PayAmount:   decimal.NewFromInt(10),
			PayCurrency: "USDT",
		}
		reused, err := findReusablePendingOrCheckCap(c, adtyPendingReuseFilter{
			paymentScene: "wallet_topup",
			provider:     model.PaymentProviderAdtyBEpusdt,
			method:       "usdt.bep20",
			quote:        quote,
		})
		require.NoError(t, err)
		assert.Nil(t, reused)
	})

	t.Run("enforces 10 active unexpired pending cap and refreshes automatically when expired", func(t *testing.T) {
		// Seed 8 more unexpired pending orders (total 10 unexpired) + 3 already expired pending orders.
		for i := range 8 {
			seedPendingTopup(t, db, 42, fmt.Sprintf("EPU_EXTRA_%d", i), model.PaymentProviderAdtyBEpusdt, "usdt.trc20", "wallet_topup",
				decimal.NewFromInt(int64(20+i)), decimal.Zero, decimal.NewFromInt(int64(20+i)), "USDT", now+900, 0, "Taddr", "")
		}
		for i := range 3 {
			seedPendingTopup(t, db, 42, fmt.Sprintf("EPU_EXPIRED_%d", i), model.PaymentProviderAdtyBEpusdt, "usdt.trc20", "wallet_topup",
				decimal.NewFromInt(int64(50+i)), decimal.Zero, decimal.NewFromInt(int64(50+i)), "USDT", now-10, 0, "Taddr", "")
		}

		c, _ := newTestGinContext(42)
		newQuote := &service.AdtyQuote{
			Business:    model.AdtyBusinessWalletTopup,
			FaceUSD:     decimal.NewFromInt(99),
			BonusUSD:    decimal.Zero,
			CreditQuota: 99 * common.QuotaPerUnitDecimal().IntPart(),
			PayAmount:   decimal.NewFromInt(99),
			PayCurrency: "USDT",
		}
		_, err := findReusablePendingOrCheckCap(c, adtyPendingReuseFilter{
			paymentScene: "wallet_topup",
			provider:     model.PaymentProviderAdtyBEpusdt,
			method:       "usdt.polygon",
			quote:        newQuote,
		})
		require.Error(t, err)
		var msg *common.Message
		require.ErrorAs(t, err, &msg)
		assert.Equal(t, "Too many pending payment orders. Please complete or cancel an existing order first", msg.Key)

		// Even at 10 active orders, matching existing order is still cleanly reused.
		matchQuote := &service.AdtyQuote{
			Business:    model.AdtyBusinessWalletTopup,
			FaceUSD:     decimal.NewFromInt(10),
			BonusUSD:    decimal.NewFromInt(2),
			CreditQuota: 12 * common.QuotaPerUnitDecimal().IntPart(),
			PayAmount:   decimal.NewFromInt(10),
			PayCurrency: "USDT",
		}
		reused, err := findReusablePendingOrCheckCap(c, adtyPendingReuseFilter{
			paymentScene: "wallet_topup",
			provider:     model.PaymentProviderAdtyBEpusdt,
			method:       "usdt.bep20",
			quote:        matchQuote,
		})
		require.NoError(t, err)
		require.NotNil(t, reused)
		assert.Equal(t, "EPU_ACTIVE_1", reused.TradeNo)

		// Expire one active order -> count drops to 9 immediately and new checkout is allowed.
		require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("trade_no = ?", "EPU_EXTRA_0").Update("expire_at", now-1).Error)
		reused, err = findReusablePendingOrCheckCap(c, adtyPendingReuseFilter{
			paymentScene: "wallet_topup",
			provider:     model.PaymentProviderAdtyBEpusdt,
			method:       "usdt.polygon",
			quote:        newQuote,
		})
		require.NoError(t, err)
		assert.Nil(t, reused)
	})
}

func TestWriteAdtyTopUpErrorClassifiesGatewayAndMethodErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name     string
		err      error
		wantText string
	}{
		{
			name:     "payment method mismatch is not reported as database error",
			err:      model.ErrPaymentMethodMismatch,
			wantText: "This payment method is not available",
		},
		{
			name:     "payment detected returns waiting message",
			err:      service.ErrAdtyPaymentDetected,
			wantText: "A transfer has been detected. Please wait",
		},
		{
			name:     "unexpected internal error returns database error",
			err:      errors.New("pq: connection refused"),
			wantText: "Database error, please contact the administrator",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, w := newTestGinContext(1)
			writeAdtyTopUpError(c, tc.err)
			assert.Contains(t, w.Body.String(), tc.wantText)
		})
	}
}

func TestCancelAdtyTopUpOrderEpayReserveBoundary(t *testing.T) {
	db := setupTopupReuseCapTestDB(t)
	now := time.Now().Unix()

	// Unreachable HTTPS Epay gateway causes provider.Cancel -> Query to fail cleanly.
	brokenChannel := &model.AdtyEpayChannel{
		Name:        "Broken-Alipay",
		GatewayURL:  "https://127.0.0.1:1",
		MerchantId:  "1001",
		MerchantKey: "secret",
		SignType:    model.AdtyEpaySignTypeMD5,
		OrderMode:   model.AdtyEpayOrderModeAPI,
		Status:      model.AdtyEpayStatusEnabled,
	}
	require.NoError(t, db.Create(brokenChannel).Error)

	t.Run("non-site-gift epay order cancels locally even when gateway query fails", func(t *testing.T) {
		seedPendingTopup(t, db, 77, "EPY_CANCEL_NOGIFT", model.PaymentProviderAdtyEpay, "alipay", "wallet_topup",
			decimal.NewFromInt(10), decimal.Zero, decimal.NewFromInt(67), "CNY", now+900, brokenChannel.Id, "", "https://pay.example.com/1")
		c, w := newTestGinContext(77)
		c.Params = gin.Params{{Key: "trade_no", Value: "EPY_CANCEL_NOGIFT"}}
		CancelAdtyTopUpOrder(c)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"success":true`)

		var row model.TopUp
		require.NoError(t, db.Where("trade_no = ?", "EPY_CANCEL_NOGIFT").First(&row).Error)
		assert.Equal(t, model.TopUpStatusCanceled, row.Status)
	})

	t.Run("site-gift-reserved epay order stays fail-closed when gateway query fails", func(t *testing.T) {
		seedPendingTopup(t, db, 77, "EPY_CANCEL_GIFT", model.PaymentProviderAdtyEpay, "alipay", "wallet_topup",
			decimal.NewFromInt(10), decimal.NewFromInt(2), decimal.NewFromInt(67), "CNY", now+900, brokenChannel.Id, "", "https://pay.example.com/2")
		require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("trade_no = ?", "EPY_CANCEL_GIFT").Updates(map[string]any{
			"site_id":              5,
			"gift_reserve_earning": decimal.NewFromInt(2),
		}).Error)

		c, w := newTestGinContext(77)
		c.Params = gin.Params{{Key: "trade_no", Value: "EPY_CANCEL_GIFT"}}
		CancelAdtyTopUpOrder(c)
		assert.Contains(t, w.Body.String(), "Payment gateway is temporarily unavailable. Please try again later")
		assert.NotContains(t, w.Body.String(), "Database error")

		var row model.TopUp
		require.NoError(t, db.Where("trade_no = ?", "EPY_CANCEL_GIFT").First(&row).Error)
		assert.Equal(t, common.TopUpStatusPending, row.Status)
	})
}

func TestGetAdtyTopUpInfoPendingOrdersSelfDefaultsAndQuoteTrim(t *testing.T) {
	db := setupEpayLifecycleTestDB(t)
	now := time.Now().Unix()

	seedTestPaymentScenes(t, db)

	setTestBEpusdtConfig(t, true, "https://bep.example.com", "sec", []string{"usdt.trc20"})

	require.NoError(t, db.Create(&model.User{
		Id: 88, Username: "wallet88", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}).Error)

	// Seed 2 unexpired wallet pending orders, 1 expired pending order, and 1 team pending order.
	seedPendingTopup(t, db, 88, "EPU_INFO_1", model.PaymentProviderAdtyBEpusdt, "usdt.trc20", "wallet_topup",
		decimal.NewFromInt(10), decimal.Zero, decimal.NewFromInt(10), "USDT", now+600, 0, "T1", "")
	seedPendingTopup(t, db, 88, "EPU_INFO_2", model.PaymentProviderAdtyBEpusdt, "usdt.trc20", "wallet_topup",
		decimal.NewFromInt(20), decimal.Zero, decimal.NewFromInt(20), "USDT", now+900, 0, "T2", "")
	seedPendingTopup(t, db, 88, "EPU_INFO_EXP", model.PaymentProviderAdtyBEpusdt, "usdt.trc20", "wallet_topup",
		decimal.NewFromInt(30), decimal.Zero, decimal.NewFromInt(30), "USDT", now-10, 0, "T3", "")
	require.NoError(t, db.Create(&model.TopUp{
		UserId: 88, Amount: 40, Money: 40, TradeNo: "TTU_INFO_TEAM",
		PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		CreateTime: now, Status: common.TopUpStatusPending,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		Business: model.AdtyBusinessTeamTopup, TradeNo: "TTU_INFO_TEAM", UserId: 88, TeamId: 9,
		Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
		FaceUSD: decimal.NewFromInt(40), PayAmount: decimal.NewFromInt(40), PayCurrency: "USDT",
		ExpireAt: now + 900,
	}).Error)

	t.Run("GetAdtyTopUpInfo returns pending_orders and backward-compatible pending", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/adty/topup/info", nil)
		c.Set("id", 88)
		GetAdtyTopUpInfo(c)
		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool `json:"success"`
			Data    struct {
				Pending       *model.AdtyTopUpView  `json:"pending"`
				PendingOrders []model.AdtyTopUpView `json:"pending_orders"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
		require.Len(t, resp.Data.PendingOrders, 2)
		assert.Equal(t, "EPU_INFO_2", resp.Data.PendingOrders[0].TradeNo)
		assert.Equal(t, "EPU_INFO_1", resp.Data.PendingOrders[1].TradeNo)
		require.NotNil(t, resp.Data.Pending)
		assert.Equal(t, resp.Data.PendingOrders[0].TradeNo, resp.Data.Pending.TradeNo)
	})

	t.Run("GetAdtyTopUpSelf defaults to wallet_topup and excludes team_topup", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/adty/topup/self?p=1&page_size=20", nil)
		c.Set("id", 88)
		GetAdtyTopUpSelf(c)
		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool `json:"success"`
			Data    struct {
				Total int                   `json:"total"`
				Items []model.AdtyTopUpView `json:"items"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
		assert.Equal(t, 3, resp.Data.Total)
		for _, item := range resp.Data.Items {
			assert.NotEqual(t, "TTU_INFO_TEAM", item.TradeNo)
		}

		// Explicit business=all includes team orders as well.
		wAll := httptest.NewRecorder()
		cAll, _ := gin.CreateTestContext(wAll)
		cAll.Request = httptest.NewRequest(http.MethodGet, "/api/adty/topup/self?business=all&p=1&page_size=20", nil)
		cAll.Set("id", 88)
		GetAdtyTopUpSelf(cAll)
		require.NoError(t, json.Unmarshal(wAll.Body.Bytes(), &resp))
		assert.Equal(t, 4, resp.Data.Total)
	})

	t.Run("PostAdtyTopUpQuote trims req.Method before provider resolution", func(t *testing.T) {
		oldPrice := operation_setting.GetPrice()
		operation_setting.SetPrice(7.0)
		defer operation_setting.SetPrice(oldPrice)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/adty/topup/quote", strings.NewReader(`{"amount":10,"method":"  usdt.trc20  "}`))
		c.Set("id", 88)
		PostAdtyTopUpQuote(c)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Success bool `json:"success"`
			Data    struct {
				Available bool `json:"available"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success, w.Body.String())
		assert.True(t, resp.Data.Available)
	})
}

func TestFindReusablePendingRefreshesStaleRSASubmitURLAndUserCheckoutLock(t *testing.T) {
	db := setupTopupReuseCapTestDB(t)
	now := time.Now().Unix()

	require.NoError(t, db.Create(&model.User{
		Id: 91, Username: "rsa_buyer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}).Error)

	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privKey)}))
	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	require.NoError(t, err)
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))

	ch := &model.AdtyEpayChannel{
		Name:               "RSA-Personal",
		GatewayURL:         "https://rsa-personal.example.com",
		MerchantId:         "6001",
		SignType:           model.AdtyEpaySignTypeRSA,
		MerchantPrivateKey: privPEM,
		PlatformPublicKey:  pubPEM,
		OrderMode:          model.AdtyEpayOrderModeSubmit,
		Status:             model.AdtyEpayStatusEnabled,
	}
	require.NoError(t, db.Create(ch).Error)
	require.NoError(t, db.Create(&model.AdtyEpayChannelMethod{
		ChannelId: ch.Id, Method: "alipay", TypeCode: "alipay", Enabled: true,
	}).Error)

	staleTS := now - 240
	staleURL := fmt.Sprintf("https://rsa-personal.example.com/api/pay/submit?pid=6001&out_trade_no=EPY_RSA_REUSE&timestamp=%d&sign=old&sign_type=RSA", staleTS)
	seedPendingTopup(t, db, 91, "EPY_RSA_REUSE", model.PaymentProviderAdtyEpay, "alipay", "wallet_topup",
		decimal.NewFromInt(10), decimal.Zero, decimal.NewFromInt(70), "CNY", now+900, ch.Id, "", staleURL)

	c, _ := newTestGinContext(91)
	reused, err := findReusablePendingOrCheckCap(c, adtyPendingReuseFilter{
		paymentScene:     "wallet_topup",
		provider:         model.PaymentProviderAdtyEpay,
		method:           "alipay",
		device:           "pc",
		requestedChannel: ch.Id,
		quote: &service.AdtyQuote{
			Business:    model.AdtyBusinessWalletTopup,
			FaceUSD:     decimal.NewFromInt(10),
			BonusUSD:    decimal.Zero,
			CreditQuota: 10 * common.QuotaPerUnitDecimal().IntPart(),
			PayAmount:   decimal.NewFromInt(70),
			PayCurrency: "CNY",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, reused)
	require.NotNil(t, reused.Ext)
	assert.NotEqual(t, staleURL, reused.Ext.PayURL)
	parsed, err := url.Parse(reused.Ext.PayURL)
	require.NoError(t, err)
	newTS, err := strconv.ParseInt(parsed.Query().Get("timestamp"), 10, 64)
	require.NoError(t, err)
	assert.Greater(t, newTS, staleTS+120)

	persisted, err := model.GetAdtyTopupOrder("EPY_RSA_REUSE")
	require.NoError(t, err)
	assert.Equal(t, reused.Ext.PayURL, persisted.PayURL)

	// Verify lockAdtyUserCheckout enforces mutual exclusion and cleans up map entry.
	var inCritical atomic.Int32
	var maxConcurrent atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := lockAdtyUserCheckout(919191)
			defer unlock()
			cur := inCritical.Add(1)
			if cur > maxConcurrent.Load() {
				maxConcurrent.Store(cur)
			}
			time.Sleep(5 * time.Millisecond)
			inCritical.Add(-1)
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), maxConcurrent.Load())
	_, stillPresent := adtyUserCheckoutLocks.Load(919191)
	assert.False(t, stillPresent)
}
