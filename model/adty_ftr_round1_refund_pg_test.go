package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY0910GiftReclaimIdempotentPG(t *testing.T) {
	db := prepareAdtyTopUpDB(t)
	pgtest.RecordServerVersion(t, db)

	gift := decimal.RequireFromString("10")
	principal := decimal.RequireFromString("100")
	first := CumulativeGiftReclaim(gift, principal, decimal.Zero, decimal.RequireFromString("40"))
	require.True(t, first.Equal(decimal.RequireFromString("4")))
	second := CumulativeGiftReclaim(gift, principal, decimal.RequireFromString("40"), decimal.RequireFromString("60"))
	require.True(t, second.Equal(decimal.RequireFromString("6")))

	teamOrder := &AdtyTopupOrder{
		TeamId:   7,
		FaceUSD:  principal,
		BonusUSD: gift,
	}
	planned, err := adtyPlannedBonusQuota(teamOrder, nil, "team-refund", 20_000_000, decimal.RequireFromString("40"))
	require.NoError(t, err)
	require.Zero(t, planned)

	user := seedManualRefundOrder(t, "ftr0910-user", "EPU-FTR0910")
	before := userQuota(t, db, user.Id)
	applied, err := ApplyAdtyTopUpManualRefund("EPU-FTR0910", "chargeback", 1, "refund-ftr0910", decimal.RequireFromString("40"))
	require.NoError(t, err)
	replay, err := ApplyAdtyTopUpManualRefund("EPU-FTR0910", "chargeback", 1, "refund-ftr0910", decimal.RequireFromString("40"))
	require.NoError(t, err)
	require.Equal(t, applied.Refund.Id, replay.Refund.Id)
	_, err = ApplyAdtyTopUpManualRefund("EPU-FTR0910", "different-reason", 1, "refund-ftr0910", decimal.RequireFromString("50"))
	require.ErrorContains(t, err, "idempotency key reused for a different operation")
	require.EqualValues(t, before, userQuota(t, db, user.Id))

	_, err = ApproveAdtyTopUpManualRefund("EPU-FTR0910", "refund-ftr0910", 1, "", "")
	require.NoError(t, err)
	reclaim1, err := ReclaimAdtyTopUpManualRefund("EPU-FTR0910", "refund-ftr0910", 1, "")
	require.NoError(t, err)
	reclaim2, err := ReclaimAdtyTopUpManualRefund("EPU-FTR0910", "refund-ftr0910", 1, "")
	require.NoError(t, err)
	require.Equal(t, reclaim1.Ledger.Id, reclaim2.Ledger.Id)

	require.NoError(t, planAndPayoutManualRefund(t, "EPU-FTR0910", "refund-ftr0910", decimal.RequireFromString("40")))
	done1, err := CompleteAdtyTopUpManualRefund("EPU-FTR0910", "refund-ftr0910", 1, "")
	require.NoError(t, err)
	done2, err := CompleteAdtyTopUpManualRefund("EPU-FTR0910", "refund-ftr0910", 1, "")
	require.NoError(t, err)
	require.Equal(t, done1.Ledger.Id, done2.Ledger.Id)
	require.Equal(t, done1.Refund.CompletedAt, done2.Refund.CompletedAt)

	var ext AdtyTopupOrder
	require.NoError(t, db.Where("trade_no = ?", "EPU-FTR0910").First(&ext).Error)
	wantGift := CumulativeGiftReclaim(ext.BonusUSD, ext.FaceUSD, decimal.Zero, decimal.RequireFromString("40"))
	require.True(t, wantGift.Equal(decimal.RequireFromString("1")))
	require.EqualValues(t, 500_000, done2.Refund.BonusDeductedQuota)
	require.Zero(t, done2.Refund.BonusShortfallQuota)
	var bonusLot AdtyFundBatch
	require.NoError(t, db.Where("user_id = ? AND source = ? AND kind = ?", user.Id, "EPU-FTR0910", "bonus").First(&bonusLot).Error)
	require.EqualValues(t, 750_000, bonusLot.Remaining)
}

func TestFTRADTY1607ManualPartialRefundApplyPG(t *testing.T) {
	db := prepareAdtyTopUpDB(t)
	pgtest.RecordServerVersion(t, db)
	user := seedManualRefundOrder(t, "ftr1607-user", "EPU-FTR1607")
	before := userQuota(t, db, user.Id)
	require.EqualValues(t, 51_250_000, before)

	applied, err := ApplyAdtyTopUpManualRefund("EPU-FTR1607", "partial chargeback", 1, "refund-ftr1607", decimal.RequireFromString("40"))
	require.NoError(t, err)
	require.Equal(t, AdtyManualRefundStatusSubmitted, applied.Refund.Status)
	require.True(t, applied.Refund.RequestUSD.Equal(decimal.RequireFromString("40")))
	require.EqualValues(t, 20_000_000, applied.Refund.RequestQuota)
	require.EqualValues(t, before, userQuota(t, db, user.Id))

	var row AdtyManualRefund
	require.NoError(t, db.Where("refund_id = ?", "refund-ftr1607").First(&row).Error)
	require.Equal(t, AdtyManualRefundStatusSubmitted, row.Status)
	require.True(t, row.RequestUSD.Equal(decimal.RequireFromString("40")))
	require.EqualValues(t, 20_000_000, row.RequestQuota)

	_, err = ApplyAdtyTopUpManualRefund("EPU-FTR1607", "over principal", 1, "refund-ftr1607-over", decimal.RequireFromString("70"))
	require.ErrorContains(t, err, "Refund amount exceeds the remaining principal")
	require.EqualValues(t, before, userQuota(t, db, user.Id))
	var overCount int64
	require.NoError(t, db.Model(&AdtyManualRefund{}).Where("refund_id = ?", "refund-ftr1607-over").Count(&overCount).Error)
	require.Zero(t, overCount)
}

func TestFTRADTY1609GiftShortfallOnCompletePG(t *testing.T) {
	db := prepareAdtyTopUpDB(t)
	pgtest.RecordServerVersion(t, db)

	gift := decimal.RequireFromString("10")
	principal := decimal.RequireFromString("100")
	reclaim := CumulativeGiftReclaim(gift, principal, decimal.Zero, decimal.RequireFromString("40"))
	require.True(t, reclaim.Equal(decimal.RequireFromString("4")))

	user := seedManualRefundOrder(t, "ftr1609-user", "EPU-FTR1609-C")
	seedAdtyOrder(t, user.Id, "EPU-FTR1609-D", PaymentProviderAdtyBEpusdt, common.TopUpStatusPending)
	_, err := CompleteAdtyTopUp("EPU-FTR1609-D", usdtFacts("EPU-FTR1609-D", "50.01"), "")
	require.NoError(t, err)
	_, err = ReserveBillingOperation(BillingOperation{OperationKey: "op-ftr1609", UserId: user.Id}, 101_000_000)
	require.NoError(t, err)

	done, err := completeManualRefundForTest(t, "EPU-FTR1609-C", "held bonus", 1, "refund-ftr1609", decimal.RequireFromString("100"))
	require.NoError(t, err)
	require.EqualValues(t, 250_000, done.Refund.BonusDeductedQuota)
	require.EqualValues(t, 1_000_000, done.Refund.BonusShortfallQuota)
	require.Positive(t, done.Refund.BonusPlannedQuota)
	require.Less(t, done.Refund.BonusDeductedQuota, done.Refund.BonusPlannedQuota)
}

func planAndPayoutManualRefund(t *testing.T, tradeNo, refundID string, amount decimal.Decimal) error {
	t.Helper()
	if _, err := PlanAdtyTopUpManualRefundBonus(tradeNo, refundID, 1, ""); err != nil {
		return err
	}
	if _, err := PlanAdtyTopUpManualRefundCommission(tradeNo, refundID, 1, ""); err != nil {
		return err
	}
	_, err := RecordAdtyTopUpManualRefundPayout(tradeNo, refundID, 1, "", AdtyManualRefundPayout{
		Status:   AdtyManualRefundPayoutSuccess,
		Currency: "USDT",
		Amount:   amount,
		PaidAt:   14,
		Voucher:  "wire-" + refundID,
	})
	return err
}
