package model

import (
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupEpayChannelDailyRoomTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&Option{},
		&TopUp{},
		&AdtyTopupOrder{},
		&AdtyEpayChannel{},
		&AdtyEpayChannelMethod{},
	))

	oldDB := DB
	DB = db
	t.Cleanup(func() {
		DB = oldDB
		_ = sqlDB.Close()
	})
	return db
}

func TestSumAdtyEpayCommittedTodayAndLockDailyRoom(t *testing.T) {
	db := setupEpayChannelDailyRoomTestDB(t)
	now := time.Now().Unix()
	dayStart := adtyEpayDayStart(db, now)
	yesterday := dayStart - 3600

	channel := &AdtyEpayChannel{
		Name:        "Alipay-Cap",
		GatewayURL:  "https://pay.example.com",
		MerchantId:  "1001",
		MerchantKey: "secret",
		SignType:    AdtyEpaySignTypeMD5,
		OrderMode:   AdtyEpayOrderModeAPI,
		Status:      AdtyEpayStatusEnabled,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&AdtyEpayChannelMethod{
		ChannelId:     channel.Id,
		Method:        "alipay",
		TypeCode:      "alipay",
		DailyLimitCNY: decimal.NewFromInt(100),
		Enabled:       true,
	}).Error)

	seedOrder := func(tradeNo, status string, amount int64, createTime, completeTime, expireAt int64) {
		require.NoError(t, db.Create(&TopUp{
			UserId:          1,
			Amount:          amount,
			Money:           float64(amount),
			TradeNo:         tradeNo,
			PaymentMethod:   "alipay",
			PaymentProvider: PaymentProviderAdtyEpay,
			CreateTime:      createTime,
			CompleteTime:    completeTime,
			Status:          status,
		}).Error)
		require.NoError(t, db.Create(&AdtyTopupOrder{
			TradeNo:     tradeNo,
			UserId:      1,
			Provider:    PaymentProviderAdtyEpay,
			Method:      "alipay",
			ChannelId:   channel.Id,
			PayAmount:   decimal.NewFromInt(amount),
			PayCurrency: "CNY",
			ExpireAt:    expireAt,
		}).Error)
	}

	// Counted: today's success (30) + today's unexpired pending (25) + today's pending with expire_at=0 (10) = 65.
	seedOrder("EPY_OK_TODAY", common.TopUpStatusSuccess, 30, dayStart+60, dayStart+120, dayStart+660)
	seedOrder("EPY_PEND_ACTIVE", common.TopUpStatusPending, 25, dayStart+100, 0, now+300)
	seedOrder("EPY_PEND_NOEXP", common.TopUpStatusPending, 10, dayStart+120, 0, 0)

	// NOT counted: expired-but-unswept pending (40), canceled (50), failed (50), expired (50), yesterday success (80), yesterday pending (80).
	seedOrder("EPY_PEND_EXPIRED_UNSWEPT", common.TopUpStatusPending, 40, dayStart+50, 0, now-5)
	seedOrder("EPY_CANCELED", TopUpStatusCanceled, 50, dayStart+70, 0, now+300)
	seedOrder("EPY_FAILED", common.TopUpStatusFailed, 50, dayStart+80, 0, now+300)
	seedOrder("EPY_EXPIRED", common.TopUpStatusExpired, 50, dayStart+90, 0, now-10)
	seedOrder("EPY_YEST_OK", common.TopUpStatusSuccess, 80, yesterday, yesterday+60, yesterday+600)
	seedOrder("EPY_YEST_PEND", common.TopUpStatusPending, 80, yesterday, 0, now+300)

	committed, err := SumAdtyEpayCommittedToday(channel.Id, "alipay", now)
	require.NoError(t, err)
	assert.True(t, committed.Equal(decimal.NewFromInt(65)), "expected 65 committed today, got %s", committed.String())

	// Room left is 100 - 65 = 35: 35 fits, 36 fails with ErrAdtyEpayDailyLimit.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return lockAdtyEpayDailyRoom(tx, channel.Id, "alipay", decimal.NewFromInt(35), now)
	}))
	err = db.Transaction(func(tx *gorm.DB) error {
		return lockAdtyEpayDailyRoom(tx, channel.Id, "alipay", decimal.NewFromInt(36), now)
	})
	assert.ErrorIs(t, err, ErrAdtyEpayDailyLimit)
}
