package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestR24InviteWithdrawalPG(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	pgtest.RecordServerVersion(t, db)
	site, owner, customer := availableSiteCommission(t, db, decimal.NewFromInt(9))
	require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, owner.Id, AdtySiteMarketingUpdate{MinUSD: new("0.00000001"), FeeUSD: new("0"), CNYRate: new("7")}))
	input := AdtySiteInviteWithdrawalInput{UserID: customer.Id, SiteID: site.Id, Amount: decimal.RequireFromString("1.1234565"), Method: "usdt", Network: "TRC20", AccountNo: "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb", IdempotencyKey: "cash-1"}
	row, err := CreateAdtySiteInviteWithdrawal(input)
	require.NoError(t, err)
	t.Run("payable precision", func(t *testing.T) { require.Equal(t, "1.123457", row.PayoutAmount) })
	t.Run("subunit blocked", func(t *testing.T) {
		tiny := input
		tiny.IdempotencyKey = "tiny"
		tiny.Amount = decimal.RequireFromString("0.00000049")
		_, err := CreateAdtySiteInviteWithdrawal(tiny)
		require.Error(t, err)
	})
	t.Run("deadline frozen", func(t *testing.T) {
		adty_setting.GetReferralSetting().CancelWindowMinutes = 2
		another := input
		another.IdempotencyKey = "deadline"
		w, e := CreateAdtySiteInviteWithdrawal(another)
		require.NoError(t, e)
		adty_setting.GetReferralSetting().CancelWindowMinutes = 0
		require.NoError(t, CancelAdtySiteInviteWithdrawal(customer.Id, w.Id, w.CreatedAt+119))
	})
	require.NoError(t, ReviewAdtySiteInviteWithdrawal(owner.Id, row.Id, true, ""))
	require.NoError(t, db.Model(site).Update("status", model.AdtySiteStatusClosed).Error)
	payer := owner.Id + 100
	proof := "proof.png"
	require.NoError(t, db.Create(&model.ReferralAsset{OwnerUserId: payer, Purpose: model.ReferralAssetPurposePaymentProof, StoragePath: proof, ContentType: "image/png", CreatedBy: "user"}).Error)
	signed := SignReferralAssetURL(proof, time.Now())
	require.NoError(t, MarkClosedAdtySiteInviteWithdrawalPaid(payer, row.Id, "hash", signed))
	t.Run("review retained", func(t *testing.T) {
		require.NoError(t, db.First(row, row.Id).Error)
		require.Equal(t, owner.Id, row.ReviewedBy)
		require.Equal(t, payer, row.PaidBy)
		require.Positive(t, row.ReviewedAt)
		require.Positive(t, row.PaidAt)
	})
	t.Run("signed receipt replay", func(t *testing.T) {
		require.NoError(t, MarkClosedAdtySiteInviteWithdrawalPaid(payer, row.Id, "hash", signed))
		require.NoError(t, MarkClosedAdtySiteInviteWithdrawalPaid(payer, row.Id, "hash", SignReferralAssetURL(proof, time.Now().Add(time.Minute))))
		require.NoError(t, db.Create(&model.ReferralAsset{OwnerUserId: payer, Purpose: model.ReferralAssetPurposePaymentProof, StoragePath: "other.png", ContentType: "image/png", CreatedBy: "user"}).Error)
		require.Error(t, MarkClosedAdtySiteInviteWithdrawalPaid(payer, row.Id, "hash", SignReferralAssetURL("other.png", time.Now())))
		require.Error(t, MarkClosedAdtySiteInviteWithdrawalPaid(payer, row.Id, "other hash", signed))
		var commission model.AdtySiteInviteCommission
		require.NoError(t, db.Where("inviter_id = ?", customer.Id).First(&commission).Error)
		require.True(t, commission.WithdrawnUSD.Equal(input.Amount))
	})
}

func TestR24InviteQuoteRefundAndCancelRacePG(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	site, owner, customer := availableSiteCommission(t, db, decimal.NewFromInt(8))
	require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, owner.Id, AdtySiteMarketingUpdate{MinUSD: new("0.00000001"), FeeUSD: new("0.1"), CNYRate: new("1"), CancelWindowMinutes: new("1")}))
	input := AdtySiteInviteWithdrawalInput{UserID: customer.Id, SiteID: site.Id, Amount: decimal.NewFromInt(2), Method: "usdt", Network: "TRC20", AccountNo: "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb", IdempotencyKey: "quote", RequireQuote: true}
	old, err := QuoteAdtySiteInviteWithdrawal(customer.Id, site.Id, input.Amount, "usdt")
	require.NoError(t, err)
	require.Equal(t, "1.900000", old.PayoutAmount)
	_, err = QuoteAdtySiteInviteWithdrawal(customer.Id, site.Id, decimal.RequireFromString("0.00499999"), "alipay")
	require.Error(t, err)
	cent, err := QuoteAdtySiteInviteWithdrawal(customer.Id, site.Id, decimal.RequireFromString("0.005"), "alipay")
	require.NoError(t, err)
	require.Equal(t, "0.01", cent.PayoutAmount)
	input.ExpectedQuoteID = old.QuoteID
	require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, owner.Id, AdtySiteMarketingUpdate{FeeUSD: new("0.2")}))
	_, err = CreateAdtySiteInviteWithdrawal(input)
	var change *SiteInviteQuoteChanged
	require.ErrorAs(t, err, &change)
	require.Equal(t, "1.800000", change.Quote.PayoutAmount)
	var count int64
	require.NoError(t, db.Model(&model.AdtySiteInviteWithdrawal{}).Count(&count).Error)
	require.Zero(t, count)
	input.ExpectedQuoteID = change.Quote.QuoteID
	row, err := CreateAdtySiteInviteWithdrawal(input)
	require.NoError(t, err)
	require.Equal(t, change.Quote.PayoutAmount, row.PayoutAmount)
	require.Equal(t, row.CreatedAt+60, row.CancelDeadline)
	require.True(t, SiteInviteCancellation(row, row.CancelDeadline-1).CanCancel)
	require.False(t, SiteInviteCancellation(row, row.CancelDeadline).CanCancel)
	require.Error(t, CancelAdtySiteInviteWithdrawal(customer.Id, row.Id, row.CancelDeadline))
	require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, owner.Id, AdtySiteMarketingUpdate{FeeUSD: new("0.3"), CancelWindowMinutes: new("0")}))
	var commission model.AdtySiteInviteCommission
	require.NoError(t, db.Where("inviter_id = ?", customer.Id).First(&commission).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := releaseSiteInviteCommissionFreezeTx(tx, &commission, decimal.RequireFromString("0.1234565"))
		return err
	}))
	require.NoError(t, db.First(row, row.Id).Error)
	require.Equal(t, "1.676544", row.PayoutAmount)
	replay, err := CreateAdtySiteInviteWithdrawal(input)
	require.NoError(t, err)
	require.Equal(t, row.Id, replay.Id)
	require.Equal(t, row.CancelDeadline, replay.CancelDeadline)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; results <- CancelAdtySiteInviteWithdrawal(customer.Id, row.Id, row.CancelDeadline-1) }()
	go func() { <-start; results <- ReviewAdtySiteInviteWithdrawal(owner.Id, row.Id, true, "") }()
	close(start)
	first, second := <-results, <-results
	require.NotEqual(t, first == nil, second == nil)
	require.NoError(t, db.First(row, row.Id).Error)
	require.Contains(t, []string{"canceled", "approved"}, row.Status)
	require.False(t, SiteInviteCancellation(row, row.CancelDeadline-1).CanCancel)
}
func TestR24ClosedSiteFailedPayoutRecordsBothOperatorsPG(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	site, owner, customer := availableSiteCommission(t, db, decimal.NewFromInt(3))
	require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, owner.Id, AdtySiteMarketingUpdate{MinUSD: new("1"), FeeUSD: new("0"), CNYRate: new("7")}))
	input := AdtySiteInviteWithdrawalInput{UserID: customer.Id, SiteID: site.Id, Amount: decimal.NewFromInt(2), Method: "usdt", Network: "TRC20", AccountNo: "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb", IdempotencyKey: "closed-fail"}
	row, err := CreateAdtySiteInviteWithdrawal(input)
	require.NoError(t, err)
	require.NoError(t, db.Model(site).Update("status", model.AdtySiteStatusClosed).Error)
	require.NoError(t, ReviewClosedAdtySiteInviteWithdrawal(101, row.Id, true, ""))
	require.NoError(t, ConfirmClosedSiteInvitePayoutFailure(202, row.Id, false, "wire rejected"))
	require.NoError(t, ConfirmClosedSiteInvitePayoutFailure(202, row.Id, false, "wire rejected"))
	require.Error(t, ConfirmClosedSiteInvitePayoutFailure(202, row.Id, false, "different"))
	require.NoError(t, db.First(row, row.Id).Error)
	require.Equal(t, 101, row.ReviewedBy)
	require.Equal(t, 202, row.FailureBy)
	require.Positive(t, row.ReviewedAt)
	require.Positive(t, row.FailureAt)
	require.Equal(t, "wire rejected", row.RejectReason)
	require.Zero(t, row.PaidBy)

	input.IdempotencyKey = "closed-paid"
	paid, err := CreateAdtySiteInviteWithdrawal(input)
	require.NoError(t, err)
	require.NoError(t, ReviewClosedAdtySiteInviteWithdrawal(303, paid.Id, true, ""))
	require.NoError(t, MarkClosedAdtySiteInviteWithdrawalPaid(404, paid.Id, "wire-2", "recorded"))
	require.NoError(t, MarkClosedAdtySiteInviteWithdrawalPaid(404, paid.Id, "wire-2", "recorded"))
	require.NoError(t, db.First(paid, paid.Id).Error)
	require.Equal(t, 303, paid.ReviewedBy)
	require.Equal(t, 404, paid.PaidBy)
}
