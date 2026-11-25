package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTopupTaskTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.TopUp{},
		&model.AdtyTopupOrder{},
		&model.AdtyEpayChannel{},
		&model.AdtyEpayChannelMethod{},
		&model.AdtyOrderPaymentEvent{},
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

func TestVerifyExpiredOrdersUnpaidBeforeExpireBoundedWindowCapAndSkip(t *testing.T) {
	db := setupTopupTaskTestDB(t)
	now := int64(1764000000)

	setTestBEpusdtConfig(t, true, "https://bep.example.com", "secret", []string{AdtyMethodUSDTTRC20})

	disabledEpay := &model.AdtyEpayChannel{
		Name: "DisabledCh", GatewayURL: "https://epay.example.com", MerchantId: "1001", MerchantKey: "sec",
		SignType: model.AdtyEpaySignTypeMD5, OrderMode: model.AdtyEpayOrderModeAPI, Status: model.AdtyEpayStatusDisabled,
	}
	require.NoError(t, db.Create(disabledEpay).Error)

	seedBEpusdt := func(tradeNo string, expireAt int64, payURL string) {
		require.NoError(t, db.Create(&model.TopUp{
			UserId: 1, TradeNo: tradeNo, PaymentProvider: model.PaymentProviderAdtyBEpusdt,
			PaymentMethod: AdtyMethodUSDTTRC20, Status: common.TopUpStatusPending,
		}).Error)
		require.NoError(t, db.Create(&model.AdtyTopupOrder{
			TradeNo: tradeNo, UserId: 1, Provider: model.PaymentProviderAdtyBEpusdt, Method: AdtyMethodUSDTTRC20,
			PayAmount: decimal.NewFromInt(10), PayCurrency: "USDT",
			GatewayTradeId: "GW_" + tradeNo, PayAddress: "TAddr", PayURL: payURL,
			ExpireAt: expireAt, LastQueryAt: 0,
		}).Error)
	}

	// 1. Order expired > 24h ago must NOT be queried.
	seedBEpusdt("EPU_OLD_25H", now-25*3600, "https://bep.example.com/pay/old")

	// 2. Order with mismatched BEpusdt host must be skipped by adtyOrderRequiresRemoteUnpaidConfirmAt.
	seedBEpusdt("EPU_CHANGED_HOST", now-300, "https://other-host.example.com/pay/1")

	// 3. Disabled Epay channel order must be skipped.
	require.NoError(t, db.Create(&model.TopUp{
		UserId: 1, TradeNo: "EPY_DISABLED_CH", PaymentProvider: model.PaymentProviderAdtyEpay,
		PaymentMethod: "alipay", Status: common.TopUpStatusPending,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: "EPY_DISABLED_CH", UserId: 1, Provider: model.PaymentProviderAdtyEpay, Method: "alipay",
		ChannelId: disabledEpay.Id, PayAmount: decimal.NewFromInt(10), PayCurrency: "CNY",
		ExpireAt: now - 300, LastQueryAt: 0,
	}).Error)

	// 4. 25 valid expired orders within 24h window: per-tick cap must stop at 20.
	for i := range 25 {
		seedBEpusdt(fmt.Sprintf("EPU_VALID_%02d", i), now-300-int64(i), "https://bep.example.com/pay/ok")
	}

	var queried []string
	origProvider := adtyHostedRecoveryProviderByName
	origAttempt := recordAdtyHostedPaymentAttempt
	t.Cleanup(func() {
		adtyHostedRecoveryProviderByName = origProvider
		recordAdtyHostedPaymentAttempt = origAttempt
	})
	recordAdtyHostedPaymentAttempt = func(tradeNo, provider, source, status string, operatorID int) error { return nil }
	adtyHostedRecoveryProviderByName = func(name string) any {
		return stubHostedQuerier{fn: func(ctx context.Context, ext *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error) {
			queried = append(queried, ext.TradeNo)
			return nil, nil
		}}
	}

	verifyExpiredOrdersUnpaidBeforeExpire(now)

	assert.Len(t, queried, adtyPreExpireVerifyMaxQueriesPerTick)
	assert.NotContains(t, queried, "EPU_OLD_25H")
	assert.NotContains(t, queried, "EPU_CHANGED_HOST")
	assert.NotContains(t, queried, "EPY_DISABLED_CH")
}
