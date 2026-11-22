package model

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestR24SiteInvitePartialMigrationPG(t *testing.T) {
	db := adtyTestDB(t)
	require.NoError(t, backfillSiteInviteWithdrawals(db))
	require.False(t, db.Migrator().HasTable(&AdtySiteInviteWithdrawal{}))
	require.NoError(t, db.AutoMigrate(&Option{}, &AdtySiteInviteWithdrawal{}))
	require.NoError(t, db.Create(&Option{Key: "adty_referral_setting.cancel_window_minutes", Value: "2"}).Error)
	one := decimal.NewFromInt(1)
	old := AdtySiteInviteWithdrawal{Status: "paid", Method: "usdt", IdempotencyKey: "old", AmountUSD: one, NetUSD: one, TxID: "wire", ReviewedBy: 45, ReviewedAt: 120, PaidAt: 130, CreatedAt: 100}
	require.NoError(t, db.Create(&old).Error)
	require.NoError(t, db.Migrator().DropColumn(&AdtySiteInviteWithdrawal{}, "PaidBy"))
	require.NoError(t, backfillSiteInviteWithdrawals(db))
	var actual AdtySiteInviteWithdrawal
	require.NoError(t, db.First(&actual, old.Id).Error)
	require.Equal(t, 45, actual.PaidBy)
	require.Zero(t, actual.ReviewedBy)
	require.Equal(t, int64(120), actual.ReviewedAt)
	require.Equal(t, int64(130), actual.PaidAt)
	require.Equal(t, "1.000000", actual.PayoutAmount)
	require.Equal(t, int64(220), actual.CancelDeadline)
	require.NoError(t, db.Model(&Option{}).Where("key = ?", "adty_referral_setting.cancel_window_minutes").Update("value", "9").Error)
	require.NoError(t, backfillSiteInviteWithdrawals(db))
	var twice AdtySiteInviteWithdrawal
	require.NoError(t, db.First(&twice, old.Id).Error)
	require.Equal(t, actual, twice)
}
