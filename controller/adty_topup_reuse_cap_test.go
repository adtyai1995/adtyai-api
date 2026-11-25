package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
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
