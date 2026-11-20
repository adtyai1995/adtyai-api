package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-16-08: approving a submitted cash refund and recording an offline payout
// leaves the refund approved. The payout does not complete internal reclaim.
func TestFTRADTY1608ApproveAndRecordOfflinePayoutPG(t *testing.T) {
	db, root := adtyCashRefundFixture(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(&AdtyBalanceLedger{}))
	require.NoError(t, db.Model(&User{}).Where("id = ?", root.Id).Update("quota", 500000).Error)
	buyer := User{
		Username: "ftr1608", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", SiteId: 0, StaffSiteId: 0,
		Quota: 500000, Email: "ftr1608@example.test", AuthVersion: 1,
	}
	require.NoError(t, db.Create(&buyer).Error)
	require.Equal(t, common.RoleCommonUser, buyer.Role)
	require.Zero(t, buyer.SiteId)
	require.NoError(t, db.Create(&TopUp{
		UserId: buyer.Id, Amount: 100, Money: 100, TradeNo: "FTR1608",
		PaymentMethod: "usdt", PaymentProvider: PaymentProviderAdtyBEpusdt, Status: common.TopUpStatusSuccess,
	}).Error)
	require.NoError(t, db.Create(&AdtyTopupOrder{
		TradeNo: "FTR1608", UserId: buyer.Id, Provider: PaymentProviderAdtyBEpusdt, Method: "usdt",
		FaceUSD: decimal.NewFromInt(100), CreditQuota: 50_000_000, TeamId: 0, SiteId: 0,
		PayAmount: decimal.NewFromInt(100), PayCurrency: "USDT",
	}).Error)

	applied, err := ApplyAdtyTopUpManualRefund("FTR1608", "customer asked", root.Id, "ftr1608", decimal.NewFromInt(40))
	require.NoError(t, err)
	require.NotNil(t, applied)
	require.Equal(t, AdtyManualRefundStatusSubmitted, applied.Refund.Status)

	approved, err := ApproveAdtyTopUpManualRefund("FTR1608", "ftr1608", root.Id, "ftr1608:approve", "approved offline")
	require.NoError(t, err)
	require.NotNil(t, approved)
	require.Equal(t, AdtyManualRefundStatusApproved, approved.Refund.Status)
	require.Positive(t, approved.Refund.ApprovedAt)

	paid, err := RecordAdtyTopUpManualRefundPayout("FTR1608", "ftr1608", root.Id, "ftr1608:payout", AdtyManualRefundPayout{
		Status:   AdtyManualRefundPayoutSuccess,
		Currency: "USDT",
		Amount:   decimal.NewFromInt(40),
		PaidAt:   1_700_000_000,
		Voucher:  "wire-ftr1608",
	})
	require.NoError(t, err)
	require.NotNil(t, paid)
	assert.Equal(t, AdtyManualRefundStatusApproved, paid.Refund.Status)
	assert.NotEqual(t, AdtyManualRefundStatusCompleted, paid.Refund.Status)
	assert.Equal(t, AdtyManualRefundPayoutSuccess, paid.Refund.PayoutStatus)
	assert.Equal(t, "USDT", paid.Refund.PayoutCurrency)
	assert.True(t, paid.Refund.PayoutAmount.Equal(decimal.NewFromInt(40)))
	assert.EqualValues(t, 1_700_000_000, paid.Refund.PayoutAt)
	assert.Equal(t, "wire-ftr1608", paid.Refund.PayoutVoucher)
	assert.Positive(t, paid.Refund.PayoutRecordedAt)

	var storedRefund AdtyManualRefund
	require.NoError(t, db.Where("refund_id = ?", "ftr1608").First(&storedRefund).Error)
	assert.Equal(t, AdtyManualRefundStatusApproved, storedRefund.Status)
	assert.Equal(t, "wire-ftr1608", storedRefund.PayoutVoucher)
	assert.Equal(t, buyer.Id, storedRefund.UserId)
	assert.Equal(t, root.Id, storedRefund.ApproveOperatorId)
	assert.Equal(t, root.Id, storedRefund.PayoutOperatorId)

	var storedBuyer User
	require.NoError(t, db.Select("id", "role", "site_id", "staff_site_id", "quota").First(&storedBuyer, buyer.Id).Error)
	assert.Equal(t, common.RoleCommonUser, storedBuyer.Role)
	assert.Zero(t, storedBuyer.SiteId)
	assert.Zero(t, storedBuyer.StaffSiteId)
	assert.Equal(t, 500000, storedBuyer.Quota)
	var storedRoot User
	require.NoError(t, db.Select("id", "role", "quota").First(&storedRoot, root.Id).Error)
	assert.Equal(t, common.RoleRootUser, storedRoot.Role)
	assert.Equal(t, 500000, storedRoot.Quota)
	var ledgers int64
	require.NoError(t, db.Model(&AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
