package service

import (
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

func setupEpayPickTestDB(t *testing.T) *gorm.DB {
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

func TestPickAdtyEpayChannelCountsCommittedTodayMatchingDailyRoom(t *testing.T) {
	db := setupEpayPickTestDB(t)
	now := int64(1764040000)

	ch1 := &model.AdtyEpayChannel{
		Name: "HighPrio", GatewayURL: "https://ch1.example.com", MerchantId: "1001", MerchantKey: "k1",
		SignType: model.AdtyEpaySignTypeMD5, OrderMode: model.AdtyEpayOrderModeAPI,
		Priority: 10, Weight: 1, Status: model.AdtyEpayStatusEnabled,
	}
	require.NoError(t, db.Create(ch1).Error)
	require.NoError(t, db.Create(&model.AdtyEpayChannelMethod{
		ChannelId: ch1.Id, Method: "alipay", TypeCode: "alipay",
		DailyLimitCNY: decimal.NewFromInt(100), Enabled: true,
	}).Error)

	ch2 := &model.AdtyEpayChannel{
		Name: "Fallback", GatewayURL: "https://ch2.example.com", MerchantId: "1002", MerchantKey: "k2",
		SignType: model.AdtyEpaySignTypeMD5, OrderMode: model.AdtyEpayOrderModeAPI,
		Priority: 5, Weight: 1, Status: model.AdtyEpayStatusEnabled,
	}
	require.NoError(t, db.Create(ch2).Error)
	require.NoError(t, db.Create(&model.AdtyEpayChannelMethod{
		ChannelId: ch2.Id, Method: "alipay", TypeCode: "alipay",
		DailyLimitCNY: decimal.NewFromInt(500), Enabled: true,
	}).Error)

	// Paid today on ch1: 40 CNY.
	require.NoError(t, db.Create(&model.TopUp{
		UserId: 1, TradeNo: "EPY_PAID", PaymentProvider: model.PaymentProviderAdtyEpay,
		PaymentMethod: "alipay", Status: common.TopUpStatusSuccess, CreateTime: now - 100, CompleteTime: now - 50,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: "EPY_PAID", UserId: 1, Provider: model.PaymentProviderAdtyEpay, Method: "alipay",
		ChannelId: ch1.Id, PayAmount: decimal.NewFromInt(40), PayCurrency: "CNY",
	}).Error)

	// Unexpired pending today on ch1: 50 CNY (total committed = 90 CNY).
	require.NoError(t, db.Create(&model.TopUp{
		UserId: 1, TradeNo: "EPY_PEND_ACTIVE", PaymentProvider: model.PaymentProviderAdtyEpay,
		PaymentMethod: "alipay", Status: common.TopUpStatusPending, CreateTime: now - 60,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: "EPY_PEND_ACTIVE", UserId: 1, Provider: model.PaymentProviderAdtyEpay, Method: "alipay",
		ChannelId: ch1.Id, PayAmount: decimal.NewFromInt(50), PayCurrency: "CNY", ExpireAt: now + 600,
	}).Error)

	// Expired pending today on ch1: 80 CNY (must NOT count against daily limit!).
	require.NoError(t, db.Create(&model.TopUp{
		UserId: 1, TradeNo: "EPY_PEND_EXPIRED", PaymentProvider: model.PaymentProviderAdtyEpay,
		PaymentMethod: "alipay", Status: common.TopUpStatusPending, CreateTime: now - 1200,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: "EPY_PEND_EXPIRED", UserId: 1, Provider: model.PaymentProviderAdtyEpay, Method: "alipay",
		ChannelId: ch1.Id, PayAmount: decimal.NewFromInt(80), PayCurrency: "CNY", ExpireAt: now - 10,
	}).Error)

	// Request 20 CNY: ch1 has 90 committed + 20 = 110 > 100, so Pick must skip ch1 and choose ch2.
	picked, _, err := PickAdtyEpayChannel("alipay", decimal.NewFromInt(20), "pc", nil, now)
	require.NoError(t, err)
	assert.Equal(t, ch2.Id, picked.Id)

	// Request 10 CNY: ch1 has 90 committed + 10 = 100 <= 100 (expired 80 CNY ignored), so Pick chooses ch1.
	picked, _, err = PickAdtyEpayChannel("alipay", decimal.NewFromInt(10), "pc", nil, now)
	require.NoError(t, err)
	assert.Equal(t, ch1.Id, picked.Id)
}
