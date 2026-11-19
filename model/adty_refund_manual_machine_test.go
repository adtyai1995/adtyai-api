package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedManualRefundOrder(t *testing.T, name, tradeNo string) *User {
	t.Helper()
	user := seedAdtyTopUpUser(t, DB, name, 0)
	seedAdtyOrder(t, user.Id, tradeNo, PaymentProviderAdtyBEpusdt, common.TopUpStatusPending)
	_, err := CompleteAdtyTopUp(tradeNo, usdtFacts(tradeNo, "50.01"), "")
	require.NoError(t, err)
	return user
}

func TestManualRefundCapUsesCompletedInProgressAndUnknown(t *testing.T) {
	prepareAdtyTopUpDB(t)
	user := seedManualRefundOrder(t, "cap-shu", "EPU-CAP-SHU")
	_ = user

	first, err := ApplyAdtyTopUpManualRefund("EPU-CAP-SHU", "partial", 1, "refund-h-1", decimal.RequireFromString("40"))
	require.NoError(t, err)
	require.EqualValues(t, 20_000_000, first.Occupancy.InProgressQuota)
	require.Zero(t, first.Occupancy.CompletedQuota)
	require.EqualValues(t, 30_000_000, first.Occupancy.RemainingQuota)

	second, err := ApplyAdtyTopUpManualRefund("EPU-CAP-SHU", "partial", 1, "refund-u-1", decimal.RequireFromString("40"))
	require.NoError(t, err)
	require.EqualValues(t, 40_000_000, second.Occupancy.InProgressQuota)
	require.EqualValues(t, 10_000_000, second.Occupancy.RemainingQuota)

	_, err = ApplyAdtyTopUpManualRefund("EPU-CAP-SHU", "partial", 1, "refund-over", decimal.RequireFromString("30"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "remaining principal")

	_, err = ApproveAdtyTopUpManualRefund("EPU-CAP-SHU", "refund-u-1", 1, "", "")
	require.NoError(t, err)
	unknown, err := RecordAdtyTopUpManualRefundPayout("EPU-CAP-SHU", "refund-u-1", 1, "", AdtyManualRefundPayout{
		Status: AdtyManualRefundPayoutUnknown, Currency: "USDT", Amount: decimal.RequireFromString("40"), PaidAt: 1, Voucher: "pending-wire",
	})
	require.NoError(t, err)
	require.EqualValues(t, 20_000_000, unknown.Occupancy.InProgressQuota)
	require.EqualValues(t, 20_000_000, unknown.Occupancy.UnknownQuota)
	require.EqualValues(t, 10_000_000, unknown.Occupancy.RemainingQuota)

	exact, err := ApplyAdtyTopUpManualRefund("EPU-CAP-SHU", "partial", 1, "refund-exact", decimal.RequireFromString("20"))
	require.NoError(t, err)
	require.Zero(t, exact.Occupancy.RemainingQuota)
	_, err = ApplyAdtyTopUpManualRefund("EPU-CAP-SHU", "partial", 1, "refund-over-2", decimal.RequireFromString("0.01"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "remaining principal")
}

func TestManualRefundAllowsAmountAboveSpendableWithoutNegativeBalance(t *testing.T) {
	prepareAdtyTopUpDB(t)
	user := seedManualRefundOrder(t, "shortfall-user", "EPU-SHORTFALL")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("quota", 5_000_000).Error)
	require.NoError(t, DB.Model(&AdtyFundBatch{}).Where("user_id = ? AND source = ? AND kind = ?", user.Id, "EPU-SHORTFALL", "principal").Update("remaining", 5_000_000).Error)
	require.NoError(t, DB.Model(&AdtyFundBatch{}).Where("user_id = ? AND source = ? AND kind = ?", user.Id, "EPU-SHORTFALL", "bonus").Update("remaining", 0).Error)

	applied, err := ApplyAdtyTopUpManualRefund("EPU-SHORTFALL", "chargeback", 1, "refund-short-1", decimal.RequireFromString("100"))
	require.NoError(t, err)
	require.EqualValues(t, 50_000_000, applied.Refund.RequestQuota)
	require.EqualValues(t, 50_000_000, applied.Occupancy.InProgressQuota)
	require.Zero(t, applied.Occupancy.CompletedQuota)

	_, err = ApproveAdtyTopUpManualRefund("EPU-SHORTFALL", "refund-short-1", 1, "", "")
	require.NoError(t, err)
	reclaimed, err := ReclaimAdtyTopUpManualRefund("EPU-SHORTFALL", "refund-short-1", 1, "")
	require.NoError(t, err)
	require.EqualValues(t, 5_000_000, reclaimed.Refund.DeductedPrincipalQuota)
	require.EqualValues(t, 45_000_000, reclaimed.Refund.ShortfallQuota)
	require.True(t, reclaimed.Refund.ShortfallUSD.Equal(decimal.RequireFromString("90")))
	require.EqualValues(t, 50_000_000, reclaimed.Refund.RequestQuota, "authorized r is not shrunk by the shortfall")
	require.EqualValues(t, 0, userQuota(t, DB, user.Id))
	require.GreaterOrEqual(t, userQuota(t, DB, user.Id), 0)
}

func TestManualRefundInternalReclaimAndPayoutAreIndependent(t *testing.T) {
	prepareAdtyTopUpDB(t)
	seedManualRefundOrder(t, "independent-user", "EPU-INDEP")
	_, err := ApplyAdtyTopUpManualRefund("EPU-INDEP", "chargeback", 1, "refund-indep-1", decimal.RequireFromString("40"))
	require.NoError(t, err)
	_, err = ApproveAdtyTopUpManualRefund("EPU-INDEP", "refund-indep-1", 1, "", "")
	require.NoError(t, err)

	reclaimed, err := ReclaimAdtyTopUpManualRefund("EPU-INDEP", "refund-indep-1", 1, "")
	require.NoError(t, err)
	require.Greater(t, reclaimed.Refund.ReclaimedAt, int64(0))
	require.Zero(t, reclaimed.Refund.PayoutRecordedAt)
	require.NotEqual(t, AdtyManualRefundStatusCompleted, reclaimed.Refund.Status)

	_, err = CompleteAdtyTopUpManualRefund("EPU-INDEP", "refund-indep-1", 1, "")
	require.Error(t, err)

	paid, err := RecordAdtyTopUpManualRefundPayout("EPU-INDEP", "refund-indep-1", 1, "", AdtyManualRefundPayout{
		Status: AdtyManualRefundPayoutSuccess, Currency: "USDT", Amount: decimal.RequireFromString("40"), PaidAt: 11, Voucher: "wire-1",
	})
	require.NoError(t, err)
	require.Equal(t, AdtyManualRefundPayoutSuccess, paid.Refund.PayoutStatus)
	require.NotEqual(t, AdtyManualRefundStatusCompleted, paid.Refund.Status)
	require.EqualValues(t, 20_000_000, paid.Occupancy.InProgressQuota)
	require.Zero(t, paid.Occupancy.CompletedQuota)

	other, err := ApplyAdtyTopUpManualRefund("EPU-INDEP-2", "other", 1, "refund-indep-payout-only", decimal.RequireFromString("10"))
	_ = other
	_ = err

	seedManualRefundOrder(t, "payout-first-user", "EPU-PAYFIRST")
	_, err = ApplyAdtyTopUpManualRefund("EPU-PAYFIRST", "chargeback", 1, "refund-pay-1", decimal.RequireFromString("40"))
	require.NoError(t, err)
	_, err = ApproveAdtyTopUpManualRefund("EPU-PAYFIRST", "refund-pay-1", 1, "", "")
	require.NoError(t, err)
	payoutOnly, err := RecordAdtyTopUpManualRefundPayout("EPU-PAYFIRST", "refund-pay-1", 1, "", AdtyManualRefundPayout{
		Status: AdtyManualRefundPayoutSuccess, Currency: "USDT", Amount: decimal.RequireFromString("40"), PaidAt: 12, Voucher: "wire-2",
	})
	require.NoError(t, err)
	require.Zero(t, payoutOnly.Refund.ReclaimedAt)
	require.Greater(t, payoutOnly.Refund.PayoutRecordedAt, int64(0))
	_, err = CompleteAdtyTopUpManualRefund("EPU-PAYFIRST", "refund-pay-1", 1, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "Internal principal reclaim")
}

func TestManualRefundCompletesOnlyAfterRequiredSubsteps(t *testing.T) {
	prepareAdtyTopUpDB(t)
	user := seedManualRefundOrder(t, "complete-user", "EPU-COMPLETE")
	_, err := ApplyAdtyTopUpManualRefund("EPU-COMPLETE", "chargeback", 1, "refund-complete-1", decimal.RequireFromString("40"))
	require.NoError(t, err)
	_, err = CompleteAdtyTopUpManualRefund("EPU-COMPLETE", "refund-complete-1", 1, "")
	require.Error(t, err)

	_, err = ApproveAdtyTopUpManualRefund("EPU-COMPLETE", "refund-complete-1", 1, "", "")
	require.NoError(t, err)
	_, err = ReclaimAdtyTopUpManualRefund("EPU-COMPLETE", "refund-complete-1", 1, "")
	require.NoError(t, err)
	_, err = PlanAdtyTopUpManualRefundBonus("EPU-COMPLETE", "refund-complete-1", 1, "")
	require.NoError(t, err)
	_, err = PlanAdtyTopUpManualRefundCommission("EPU-COMPLETE", "refund-complete-1", 1, "")
	require.NoError(t, err)
	_, err = CompleteAdtyTopUpManualRefund("EPU-COMPLETE", "refund-complete-1", 1, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "Offline payout")

	_, err = RecordAdtyTopUpManualRefundPayout("EPU-COMPLETE", "refund-complete-1", 1, "", AdtyManualRefundPayout{
		Status: AdtyManualRefundPayoutSuccess, Currency: "USDT", Amount: decimal.RequireFromString("40"), PaidAt: 13, Voucher: "wire-complete",
	})
	require.NoError(t, err)
	done, err := CompleteAdtyTopUpManualRefund("EPU-COMPLETE", "refund-complete-1", 1, "")
	require.NoError(t, err)
	require.Equal(t, AdtyManualRefundStatusCompleted, done.Refund.Status)
	require.EqualValues(t, 20_000_000, done.Occupancy.CompletedQuota)
	require.Zero(t, done.Occupancy.InProgressQuota)
	require.EqualValues(t, 30_750_000, userQuota(t, DB, user.Id))
}

func TestManualRefundSameRefundIDIsIdempotent(t *testing.T) {
	prepareAdtyTopUpDB(t)
	user := seedManualRefundOrder(t, "idem-user", "EPU-IDEM")
	first, err := ApplyAdtyTopUpManualRefund("EPU-IDEM", "chargeback", 1, "refund-idem-1", decimal.RequireFromString("40"))
	require.NoError(t, err)
	second, err := ApplyAdtyTopUpManualRefund("EPU-IDEM", "chargeback", 1, "refund-idem-1", decimal.RequireFromString("40"))
	require.NoError(t, err)
	_, err = ApplyAdtyTopUpManualRefund("EPU-IDEM", "different-reason", 1, "refund-idem-1", decimal.RequireFromString("50"))
	require.ErrorContains(t, err, "idempotency key reused for a different operation")
	require.Equal(t, first.Refund.Id, second.Refund.Id)
	require.True(t, first.Refund.RequestUSD.Equal(decimal.RequireFromString("40")))
	require.EqualValues(t, 20_000_000, second.Occupancy.InProgressQuota)

	_, err = ApproveAdtyTopUpManualRefund("EPU-IDEM", "refund-idem-1", 1, "", "")
	require.NoError(t, err)
	reclaim1, err := ReclaimAdtyTopUpManualRefund("EPU-IDEM", "refund-idem-1", 1, "")
	require.NoError(t, err)
	reclaim2, err := ReclaimAdtyTopUpManualRefund("EPU-IDEM", "refund-idem-1", 1, "")
	require.NoError(t, err)
	require.Equal(t, reclaim1.Ledger.Id, reclaim2.Ledger.Id)
	require.EqualValues(t, 51_250_000-reclaim1.Refund.DeductedPrincipalQuota, userQuota(t, DB, user.Id))

	_, err = PlanAdtyTopUpManualRefundBonus("EPU-IDEM", "refund-idem-1", 1, "")
	require.NoError(t, err)
	_, err = PlanAdtyTopUpManualRefundCommission("EPU-IDEM", "refund-idem-1", 1, "")
	require.NoError(t, err)
	_, err = RecordAdtyTopUpManualRefundPayout("EPU-IDEM", "refund-idem-1", 1, "", AdtyManualRefundPayout{
		Status: AdtyManualRefundPayoutSuccess, Currency: "USDT", Amount: decimal.RequireFromString("40"), PaidAt: 14, Voucher: "wire-idem",
	})
	require.NoError(t, err)
	done1, err := CompleteAdtyTopUpManualRefund("EPU-IDEM", "refund-idem-1", 1, "")
	require.NoError(t, err)
	done2, err := CompleteAdtyTopUpManualRefund("EPU-IDEM", "refund-idem-1", 1, "")
	require.NoError(t, err)
	require.Equal(t, done1.Refund.CompletedAt, done2.Refund.CompletedAt)
	require.Equal(t, done1.Ledger.Id, done2.Ledger.Id)
	require.EqualValues(t, 20_000_000, done2.Occupancy.CompletedQuota)
}

func TestManualRefundRatiosUseCompletedSOnlyAndIgnoreInProgress(t *testing.T) {
	prepareAdtyTopUpDB(t)
	seedManualRefundOrder(t, "ratio2-user", "EPU-RATIO2")
	_, err := ApplyAdtyTopUpManualRefund("EPU-RATIO2", "hold", 1, "refund-ratio2-h", decimal.RequireFromString("40"))
	require.NoError(t, err)

	done, err := completeManualRefundForTest(t, "EPU-RATIO2", "done", 1, "refund-ratio2-s", decimal.RequireFromString("20"))
	require.NoError(t, err)
	require.EqualValues(t, 10_000_000, done.Occupancy.CompletedQuota)
	require.EqualValues(t, 20_000_000, done.Occupancy.InProgressQuota)

	var ext AdtyTopupOrder
	require.NoError(t, DB.Where("trade_no = ?", "EPU-RATIO2").First(&ext).Error)
	require.EqualValues(t, 10_000_000, ext.RefundQuota)
	require.True(t, ext.RefundRatio().Equal(decimal.NewFromInt(10_000_000).Div(decimal.NewFromInt(ext.FaceQuota()))))
	reclaim := CumulativeGiftReclaim(ext.BonusUSD, ext.FaceUSD, decimal.Zero, decimal.RequireFromString("20"))
	require.True(t, reclaim.Equal(decimal.RequireFromString("0.5")))
	facts, err := LoadAdtyRefundPageFacts("EPU-RATIO2", decimal.Zero)
	require.NoError(t, err)
	require.EqualValues(t, 10_000_000, facts.RefundCompletedQuota)
	require.EqualValues(t, 20_000_000, facts.RefundInProgressQuota)
	require.EqualValues(t, 30_000_000, facts.RefundOccupiedQuota)
}

func TestManualRefundHistoricalCompletedCountsInS(t *testing.T) {
	prepareAdtyTopUpDB(t)
	user := seedManualRefundOrder(t, "hist-user", "EPU-HIST")
	require.NoError(t, DB.Model(&AdtyTopupOrder{}).Where("trade_no = ?", "EPU-HIST").Updates(map[string]any{
		"refund_quota":  25_000_000,
		"refund_amount": decimal.NewFromInt(50),
		"refunded_at":   99,
		"refund_reason": "historical one-shot",
	}).Error)
	var ext AdtyTopupOrder
	require.NoError(t, DB.Where("trade_no = ?", "EPU-HIST").First(&ext).Error)
	require.EqualValues(t, 25_000_000, ext.RefundQuota)

	facts, err := LoadAdtyRefundPageFacts("EPU-HIST", decimal.Zero)
	require.NoError(t, err)
	require.EqualValues(t, 25_000_000, facts.RefundCompletedQuota)
	require.EqualValues(t, 25_000_000, facts.RefundOccupiedQuota)
	require.EqualValues(t, 25_000_000, facts.RefundRemainingQuota)
	require.Empty(t, facts.ManualRefunds)

	_, err = ApplyAdtyTopUpManualRefund("EPU-HIST", "second", 1, "refund-hist-2", decimal.RequireFromString("60"))
	require.Error(t, err)
	applied, err := ApplyAdtyTopUpManualRefund("EPU-HIST", "second", 1, "refund-hist-2", decimal.RequireFromString("50"))
	require.NoError(t, err)
	require.EqualValues(t, 25_000_000, applied.Occupancy.CompletedQuota)
	require.EqualValues(t, 25_000_000, applied.Occupancy.InProgressQuota)
	require.Zero(t, applied.Occupancy.RemainingQuota)
	require.EqualValues(t, 25_000_000, applied.Refund.RequestQuota)

	var stored AdtyTopupOrder
	require.NoError(t, DB.Where("trade_no = ?", "EPU-HIST").First(&stored).Error)
	require.EqualValues(t, 25_000_000, stored.RefundQuota, "historical completed S is not rewritten at apply")
	_ = user
}

func TestManualRefundTableMissingFailsClosed(t *testing.T) {
	prepareAdtyTopUpDB(t)
	seedManualRefundOrder(t, "missing-user", "EPU-MISSING")
	require.NoError(t, DB.Migrator().DropTable(&AdtyManualRefund{}))
	_, err := ApplyAdtyTopUpManualRefund("EPU-MISSING", "chargeback", 1, "refund-missing-1", decimal.RequireFromString("10"))
	require.ErrorIs(t, err, ErrAdtyManualRefundTableMissing)
	_, err = LoadAdtyRefundPageFacts("EPU-MISSING", decimal.Zero)
	require.ErrorIs(t, err, ErrAdtyManualRefundTableMissing)
}

func TestManualRefundUnknownPayoutCannotTerminateAndKeepsUOccupancyPG(t *testing.T) {
	prepareAdtyTopUpDB(t)
	seedManualRefundOrder(t, "unknown-term-user", "EPU-UNK-TERM")

	_, err := ApplyAdtyTopUpManualRefund("EPU-UNK-TERM", "chargeback", 1, "refund-unk-1", decimal.RequireFromString("40"))
	require.NoError(t, err)
	_, err = ApproveAdtyTopUpManualRefund("EPU-UNK-TERM", "refund-unk-1", 1, "", "")
	require.NoError(t, err)
	unk, err := RecordAdtyTopUpManualRefundPayout("EPU-UNK-TERM", "refund-unk-1", 1, "", AdtyManualRefundPayout{
		Status: AdtyManualRefundPayoutUnknown, Currency: "USDT", Amount: decimal.RequireFromString("40"), PaidAt: 10, Voucher: "wire-unk",
	})
	require.NoError(t, err)
	require.EqualValues(t, 20_000_000, unk.Occupancy.UnknownQuota)
	require.EqualValues(t, 30_000_000, unk.Occupancy.RemainingQuota)

	// ISS-R1-0014: payout_status == unknown must refuse Terminate and preserve U occupancy.
	_, err = TerminateAdtyTopUpManualRefund("EPU-UNK-TERM", "refund-unk-1", 1, "", "try terminate unknown")
	require.Error(t, err)

	facts, err := LoadAdtyRefundPageFacts("EPU-UNK-TERM", decimal.Zero)
	require.NoError(t, err)
	require.EqualValues(t, 20_000_000, facts.RefundUnknownQuota)
	require.EqualValues(t, 30_000_000, facts.RefundRemainingQuota)

	// Even if an existing row has status=terminated with payout_status=unknown, bucket remains U.
	require.Equal(t, "U", adtyManualRefundOccupancyBucket(AdtyManualRefund{
		Status: AdtyManualRefundStatusTerminated, PayoutStatus: AdtyManualRefundPayoutUnknown,
	}))

	// Once confirmed failed, terminate succeeds and releases U occupancy.
	_, err = RecordAdtyTopUpManualRefundPayout("EPU-UNK-TERM", "refund-unk-1", 1, "payout-fail", AdtyManualRefundPayout{
		Status: AdtyManualRefundPayoutFailed, Currency: "USDT", Amount: decimal.RequireFromString("40"), PaidAt: 11, Voucher: "wire-fail",
	})
	require.NoError(t, err)
	term, err := TerminateAdtyTopUpManualRefund("EPU-UNK-TERM", "refund-unk-1", 1, "term-after-fail", "confirmed failed")
	require.NoError(t, err)
	require.Equal(t, AdtyManualRefundStatusTerminated, term.Refund.Status)
	require.Zero(t, term.Occupancy.UnknownQuota)
	require.Zero(t, term.Occupancy.InProgressQuota)
	require.EqualValues(t, 50_000_000, term.Occupancy.RemainingQuota)
}

func TestManualRefundDoesNotSeizeInFlightAllocatedLotsPG(t *testing.T) {
	prepareAdtyTopUpDB(t)
	user := seedManualRefundOrder(t, "inflight-lot-user", "EPU-INFLIGHT-A")
	// Add a second order so spendable wallet balance exceeds order A's unallocated remainder.
	seedAdtyOrder(t, user.Id, "EPU-INFLIGHT-B", PaymentProviderAdtyBEpusdt, common.TopUpStatusPending)
	_, err := CompleteAdtyTopUp("EPU-INFLIGHT-B", usdtFacts("EPU-INFLIGHT-B", "50.01"), "")
	require.NoError(t, err)

	// Reserve 80 USD (40,000,000 quota): allocates 40,000,000 on EPU-INFLIGHT-A's 50,000,000 principal lot.
	_, err = ReserveBillingOperation(BillingOperation{OperationKey: "op-inflight-principal", UserId: user.Id}, 40_000_000)
	require.NoError(t, err)

	// Refund $40 (20,000,000) on EPU-INFLIGHT-A: only 10,000,000 principal is unallocated!
	_, err = ApplyAdtyTopUpManualRefund("EPU-INFLIGHT-A", "partial", 1, "refund-inf-1", decimal.RequireFromString("40"))
	require.NoError(t, err)
	_, err = ApproveAdtyTopUpManualRefund("EPU-INFLIGHT-A", "refund-inf-1", 1, "", "")
	require.NoError(t, err)
	reclaimed, err := ReclaimAdtyTopUpManualRefund("EPU-INFLIGHT-A", "refund-inf-1", 1, "")
	require.NoError(t, err)
	require.EqualValues(t, 10_000_000, reclaimed.Refund.ReclaimablePrincipalQuota)
	require.EqualValues(t, 10_000_000, reclaimed.Refund.DeductedPrincipalQuota)
	require.EqualValues(t, 10_000_000, reclaimed.Refund.ShortfallQuota)

	var lotA AdtyFundBatch
	require.NoError(t, DB.Where("user_id = ? AND source = ? AND kind = ?", user.Id, "EPU-INFLIGHT-A", "principal").First(&lotA).Error)
	require.EqualValues(t, 40_000_000, lotA.Remaining, "in-flight allocated principal must remain intact")

	require.NoError(t, QueueBillingSettlement("op-inflight-principal", 40_000_000, false))
	settled, applied, err := ApplyBillingSettlement("op-inflight-principal")
	require.NoError(t, err)
	require.True(t, applied)
	require.Zero(t, settled.FundBatchGap)

	// Now test in-flight bonus allocation protection during refund completion.
	user2 := seedManualRefundOrder(t, "inflight-bonus-user", "EPU-INFLIGHT-C")
	seedAdtyOrder(t, user2.Id, "EPU-INFLIGHT-D", PaymentProviderAdtyBEpusdt, common.TopUpStatusPending)
	_, err = CompleteAdtyTopUp("EPU-INFLIGHT-D", usdtFacts("EPU-INFLIGHT-D", "50.01"), "")
	require.NoError(t, err)
	// Reserve 202 USD (101,000,000 quota): allocates C-principal (50M) + D-principal (50M) + C-bonus (1M),
	// leaving 250,000 unallocated on C-bonus while spendable remains 1,500,000 >= planned 1,250,000.
	_, err = ReserveBillingOperation(BillingOperation{OperationKey: "op-inflight-bonus", UserId: user2.Id}, 101_000_000)
	require.NoError(t, err)

	done, err := completeManualRefundForTest(t, "EPU-INFLIGHT-C", "full-with-held-bonus", 1, "refund-inf-c1", decimal.RequireFromString("100"))
	require.NoError(t, err)
	require.EqualValues(t, 0, done.Refund.DeductedPrincipalQuota)
	require.EqualValues(t, 250_000, done.Refund.BonusDeductedQuota)
	require.EqualValues(t, 1_000_000, done.Refund.BonusShortfallQuota)

	require.NoError(t, QueueBillingSettlement("op-inflight-bonus", 101_000_000, false))
	settled2, applied2, err := ApplyBillingSettlement("op-inflight-bonus")
	require.NoError(t, err)
	require.True(t, applied2)
	require.Zero(t, settled2.FundBatchGap)
}

func TestManualRefundSplitFullRefundTailCorrectionPG(t *testing.T) {
	prepareAdtyTopUpDB(t)
	user := seedAdtyTopUpUser(t, DB, "tail-user", 0)
	tradeNo := "EPU-TAIL-30"
	require.NoError(t, CreateAdtyTopUp(
		&TopUp{UserId: user.Id, Amount: 30, Money: 30, TradeNo: tradeNo, PaymentMethod: "usdt.trc20", PaymentProvider: PaymentProviderAdtyBEpusdt, CreateTime: 1, Status: common.TopUpStatusPending},
		&AdtyTopupOrder{
			TradeNo: tradeNo, UserId: user.Id, Provider: PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
			FaceUSD: decimal.NewFromInt(30), BonusRate: decimal.RequireFromString("3.33333333"), BonusUSD: decimal.NewFromInt(1),
			CreditQuota: 15_500_000, PayAmount: decimal.NewFromInt(30), PayCurrency: "USDT", TopupGroupRatio: decimal.NewFromInt(1),
		},
	))
	_, err := CompleteAdtyTopUp(tradeNo, AdtyPaymentFacts{
		Provider:     PaymentProviderAdtyBEpusdt,
		PaidAmount:   decimal.NewFromInt(30),
		FiatAmount:   decimal.NewFromInt(30),
		PaidCurrency: "USDT",
		Source:       AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)

	r1, err := completeManualRefundForTest(t, tradeNo, "split-1", 1, "tail-r1", decimal.NewFromInt(10))
	require.NoError(t, err)
	require.EqualValues(t, 166_666, r1.Refund.BonusPlannedQuota)
	require.EqualValues(t, 166_666, r1.Refund.BonusDeductedQuota)

	r2, err := completeManualRefundForTest(t, tradeNo, "split-2", 1, "tail-r2", decimal.NewFromInt(10))
	require.NoError(t, err)
	require.EqualValues(t, 166_666, r2.Refund.BonusPlannedQuota)
	require.EqualValues(t, 166_666, r2.Refund.BonusDeductedQuota)

	r3, err := completeManualRefundForTest(t, tradeNo, "split-3", 1, "tail-r3", decimal.NewFromInt(10))
	require.NoError(t, err)
	require.EqualValues(t, 166_668, r3.Refund.BonusPlannedQuota)
	require.EqualValues(t, 166_668, r3.Refund.BonusDeductedQuota)
	require.Zero(t, r3.Refund.BonusShortfallQuota)
	require.Zero(t, userQuota(t, DB, user.Id))

	var bonusLot AdtyFundBatch
	require.NoError(t, DB.Where("user_id = ? AND source = ? AND kind = ?", user.Id, tradeNo, "bonus").First(&bonusLot).Error)
	require.Zero(t, bonusLot.Remaining)
}

const ownerRefundTrade = "online-owner-topup"

func TestOwnerTopupRefundReleaseHonorsWalletLimitPG(t *testing.T) {
	f := newOwnerRefundFixture(t)
	const id = "capacity"
	finance := f.finance.Id
	_, err := RefundAdtyTopUp(ownerRefundTrade, "apply", finance, id, 5_000_000)
	require.NoError(t, err)
	_, err = ApproveAdtyTopUpManualRefund(ownerRefundTrade, id, finance, "", "")
	require.NoError(t, err)
	_, err = ReclaimAdtyTopUpManualRefund(ownerRefundTrade, id, finance, "")
	require.NoError(t, err)
	_, err = RecordAdtyTopUpManualRefundPayout(ownerRefundTrade, id, finance, "", AdtyManualRefundPayout{Status: AdtyManualRefundPayoutFailed})
	require.NoError(t, err)
	// A later legitimate credit uses the capacity freed by reclaim, including
	// a persistent pending amount not folded into users.quota yet.
	require.NoError(t, f.db.Model(&f.owner).Updates(map[string]any{"quota": common.MaxWalletQuota - 1, "quota_pending": 1}).Error)
	_, err = TerminateAdtyTopUpManualRefund(ownerRefundTrade, id, finance, "", "failed")
	require.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
	var row AdtyManualRefund
	require.NoError(t, f.db.Where("refund_id = ?", id).First(&row).Error)
	assert.Equal(t, AdtyManualRefundStatusApproved, row.Status)
	assert.Zero(t, row.TerminatedAt)
	var share AdtyFundAllocation
	require.NoError(t, f.db.Where("user_id = ? AND source = ?", f.owner.Id, ownerRefundTrade).First(&share).Error)
	assert.EqualValues(t, 5_000_000, share.Settled)
	assert.Equal(t, share.Settled, share.Held)
	var owner User
	require.NoError(t, f.db.First(&owner, f.owner.Id).Error)
	assert.Equal(t, common.MaxWalletQuota, owner.Quota+owner.QuotaPending)
	var lot AdtyFundBatch
	require.NoError(t, f.db.First(&lot, f.lot.Id).Error)
	assert.Zero(t, lot.Remaining)
	var wallet AdtySiteWallet
	require.NoError(t, f.db.Where("site_id = ? AND kind = ?", f.site.Id, AdtySiteWalletOperating).First(&wallet).Error)
	assert.True(t, wallet.Balance.IsZero())
	var n int64
	require.NoError(t, f.db.Model(&AdtyBalanceLedger{}).Where("ref_id = ?", ownerRefundTrade).Count(&n).Error)
	assert.EqualValues(t, 1, n)
	require.NoError(t, f.db.Model(&AdtySiteWalletLedger{}).Where("ref_id = ?", ownerRefundTrade).Count(&n).Error)
	assert.EqualValues(t, 1, n)
	// The failed termination is retriable once capacity exists again.
	require.NoError(t, f.db.Model(&f.owner).Update("quota", common.MaxWalletQuota-5_000_001).Error)
	_, err = TerminateAdtyTopUpManualRefund(ownerRefundTrade, id, finance, "", "failed")
	require.NoError(t, err)
	require.NoError(t, f.db.First(&owner, f.owner.Id).Error)
	assert.Equal(t, common.MaxWalletQuota, owner.Quota+owner.QuotaPending)
}

type ownerRefundFixture struct {
	db             *gorm.DB
	owner, finance User
	site           AdtySite
	key            Token
	lot            AdtyFundBatch
}

func newOwnerRefundFixture(t *testing.T) *ownerRefundFixture {
	t.Helper()
	db := adtyTestDBWithSchema(t)
	pgtest.RecordServerVersion(t, db)
	f := &ownerRefundFixture{db: db, owner: User{Username: "owner", Quota: 5_000_000}, finance: User{Username: "finance", Role: common.RoleAdminUser}}
	require.NoError(t, db.Create(&f.owner).Error)
	require.NoError(t, db.Create(&f.finance).Error)
	f.site = AdtySite{OwnerUserId: f.owner.Id, Status: AdtySiteStatusActive, Subdomain: "owner"}
	require.NoError(t, db.Create(&f.site).Error)
	f.key = Token{Key: "self-use", UserId: f.owner.Id, OwnerSelfUseSiteId: f.site.Id, UnlimitedQuota: true}
	require.NoError(t, db.Create(&f.key).Error)
	usd := decimal.NewFromInt(10)
	require.NoError(t, db.Create(&TopUp{TradeNo: ownerRefundTrade, UserId: f.owner.Id, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderAdtyBEpusdt}).Error)
	require.NoError(t, db.Create(&AdtyTopupOrder{Business: AdtyBusinessOwnerSelfUseTopup, TradeNo: ownerRefundTrade, UserId: f.owner.Id, SiteOwnerUserId: f.owner.Id, SiteId: f.site.Id, Provider: PaymentProviderAdtyBEpusdt, FaceUSD: usd, PayAmount: usd, PaidAmount: usd, PaidCurrency: "USDT", CreditQuota: 5_000_000}).Error)
	f.lot = AdtyFundBatch{UserId: f.owner.Id, SiteId: &f.site.Id, Source: ownerRefundTrade, Kind: AdtyFundBatchKindPrincipal, Original: 5_000_000, Remaining: 5_000_000}
	require.NoError(t, db.Create(&f.lot).Error)
	require.NoError(t, db.Create(&AdtySiteWallet{SiteId: f.site.Id, Kind: AdtySiteWalletOperating, Currency: "USD", Balance: usd}).Error)
	return f
}

func (f *ownerRefundFixture) reserve(key string, quota int) error {
	_, err := ReserveBillingOperation(BillingOperation{OperationKey: "request:" + key, UserId: f.owner.Id, TokenId: f.key.Id, TokenUnlimited: true, SiteId: f.site.Id}, quota)
	return err
}

func TestOwnerTopupRefundOnlyOriginalFreePrincipalPG(t *testing.T) {
	f := newOwnerRefundFixture(t)
	// $2 consumed, $3 in an actual API reservation, and $8 from another source.
	require.NoError(t, f.db.Model(&f.lot).Update("remaining", 4_000_000).Error)
	require.NoError(t, f.db.Model(&f.owner).Update("quota", 8_000_000).Error)
	require.NoError(t, CreateAdtyFundBatchesWithSiteTx(f.db, f.owner.Id, &f.site.Id, "transfer", 4_000_000, 0))
	require.NoError(t, f.reserve("prior", 1_500_000))
	preview, err := PreviewAdtyTopUpRefund(ownerRefundTrade)
	require.NoError(t, err)
	assert.EqualValues(t, 2_500_000, preview.RefundQuota)
	assert.EqualValues(t, 2_500_000, preview.Facts.RefundRemainingQuota)
	_, err = LoadAdtyRefundPageFacts(ownerRefundTrade, decimal.NewFromInt(6))
	assert.Error(t, err)
	_, err = RefundAdtyTopUp(ownerRefundTrade, "legacy", f.finance.Id, "legacy-over", 2_500_001)
	assert.Error(t, err)
	_, err = ApplyAdtyTopUpManualRefund(ownerRefundTrade, "manual", f.finance.Id, "manual-over", decimal.NewFromInt(6))
	assert.Error(t, err)
	_, err = ApplyAdtyTopUpManualRefund(ownerRefundTrade, "dust", f.finance.Id, "dust-over", decimal.RequireFromString("5.000001"))
	assert.Error(t, err)
	var n int64
	require.NoError(t, f.db.Model(&AdtyManualRefund{}).Count(&n).Error)
	assert.Zero(t, n)
}

func TestOwnerTopupRefundStaleApprovalAndShortfallPG(t *testing.T) {
	f := newOwnerRefundFixture(t)
	_, err := RefundAdtyTopUp(ownerRefundTrade, "apply", f.finance.Id, "stale", 5_000_000)
	require.NoError(t, err)
	require.NoError(t, f.reserve("spent-before-approval", 1))
	_, err = ApproveAdtyTopUpManualRefund(ownerRefundTrade, "stale", f.finance.Id, "", "")
	require.Error(t, err)
	_, err = TerminateAdtyTopUpManualRefund(ownerRefundTrade, "stale", f.finance.Id, "", "refresh")
	require.NoError(t, err)
	_, err = RefundAdtyTopUp(ownerRefundTrade, "apply", f.finance.Id, "short", 3_000_000)
	require.NoError(t, err)
	_, err = ApproveAdtyTopUpManualRefund(ownerRefundTrade, "short", f.finance.Id, "", "")
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&f.lot).Update("remaining", 2_999_999).Error)
	require.NoError(t, CreateAdtyFundBatchesWithSiteTx(f.db, f.owner.Id, &f.site.Id, "cannot-subsidize", 5_000_000, 0))
	_, err = ReclaimAdtyTopUpManualRefund(ownerRefundTrade, "short", f.finance.Id, "")
	require.Error(t, err)
	_, err = RecordAdtyTopUpManualRefundPayout(ownerRefundTrade, "short", f.finance.Id, "", AdtyManualRefundPayout{Status: AdtyManualRefundPayoutSuccess, Currency: "USDT", Amount: decimal.NewFromInt(6), PaidAt: 1, Voucher: "receipt"})
	assert.Error(t, err, "a damaged hold cannot authorize an external payout")
	var row AdtyManualRefund
	require.NoError(t, f.db.Where("refund_id = ?", "short").First(&row).Error)
	assert.Zero(t, row.ReclaimedAt)
	assert.Zero(t, row.ShortfallQuota)
	var user User
	require.NoError(t, f.db.First(&user, f.owner.Id).Error)
	assert.Equal(t, 4_999_999, user.Quota+user.QuotaPending)
}

func TestOwnerTopupRefundPreservesOrdinaryBalancePG(t *testing.T) {
	f := newOwnerRefundFixture(t)
	require.NoError(t, f.db.Model(&f.owner).Update("quota", 5_500_000).Error)
	require.NoError(t, CreateAdtyFundBatchesWithSiteTx(f.db, f.owner.Id, nil, "personal", 500_000, 0))
	_, err := RefundAdtyTopUp(ownerRefundTrade, "apply", f.finance.Id, "personal-boundary", 5_000_000)
	require.NoError(t, err)
	_, err = ApproveAdtyTopUpManualRefund(ownerRefundTrade, "personal-boundary", f.finance.Id, "", "")
	require.NoError(t, err)
	_, err = ReserveBillingOperation(BillingOperation{OperationKey: "ordinary:too-much", UserId: f.owner.Id}, 500_001)
	require.ErrorIs(t, err, ErrBillingWalletInsufficient)
	_, err = ReserveBillingOperation(BillingOperation{OperationKey: "ordinary:allowed", UserId: f.owner.Id}, 500_000)
	require.NoError(t, err)
	var allocations []AdtyFundAllocation
	require.NoError(t, f.db.Where("operation_id = ?", "ordinary:allowed").Find(&allocations).Error)
	require.Len(t, allocations, 1)
	assert.Equal(t, "personal", allocations[0].Source)
	_, err = ReclaimAdtyTopUpManualRefund(ownerRefundTrade, "personal-boundary", f.finance.Id, "")
	require.NoError(t, err)
}

func TestOwnerTopupRefundTerminationAfterFailedPayoutPG(t *testing.T) {
	f := newOwnerRefundFixture(t)
	const id = "failed-payout"
	finance := f.finance.Id
	_, err := RefundAdtyTopUp(ownerRefundTrade, "apply", finance, id, 5_000_000)
	require.NoError(t, err)
	_, err = ApproveAdtyTopUpManualRefund(ownerRefundTrade, id, finance, "", "")
	require.NoError(t, err)
	_, err = ReclaimAdtyTopUpManualRefund(ownerRefundTrade, id, finance, "")
	require.NoError(t, err)
	_, err = RecordAdtyTopUpManualRefundPayout(ownerRefundTrade, id, finance, "", AdtyManualRefundPayout{Status: AdtyManualRefundPayoutUnknown})
	require.NoError(t, err)
	_, err = TerminateAdtyTopUpManualRefund(ownerRefundTrade, id, finance, "", "unconfirmed")
	require.Error(t, err)
	_, err = RecordAdtyTopUpManualRefundPayout(ownerRefundTrade, id, finance, "failed", AdtyManualRefundPayout{Status: AdtyManualRefundPayoutFailed})
	require.NoError(t, err)
	for range 2 {
		_, err = TerminateAdtyTopUpManualRefund(ownerRefundTrade, id, finance, "", "payment failed")
		require.NoError(t, err)
	}
	require.NoError(t, f.reserve("after-failure", 5_000_000))
	var wallet AdtySiteWallet
	require.NoError(t, f.db.Where("site_id = ? AND kind = ?", f.site.Id, AdtySiteWalletOperating).First(&wallet).Error)
	assert.Equal(t, "10", wallet.Balance.String())
}

func TestOwnerTopupRefundHoldsAndReleasesPG(t *testing.T) {
	f := newOwnerRefundFixture(t)
	const id = "hold"
	_, err := RefundAdtyTopUp(ownerRefundTrade, "apply", f.finance.Id, id, 3_000_000)
	require.NoError(t, err)
	_, err = ApproveAdtyTopUpManualRefund(ownerRefundTrade, id, f.owner.Id, "", "")
	assert.Error(t, err, "owner cannot approve their own refund")
	for range 2 {
		_, err = ApproveAdtyTopUpManualRefund(ownerRefundTrade, id, f.finance.Id, "", "")
		require.NoError(t, err)
	}
	left, err := AdtyOwnerPrincipalAvailable(f.db, f.owner.Id, f.site.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 2_000_000, left)
	assert.ErrorIs(t, f.reserve("while-held", 2_000_001), ErrBillingWalletInsufficient)
	_, err = ReserveBillingOperation(BillingOperation{OperationKey: "request:ordinary", UserId: f.owner.Id}, 1)
	assert.ErrorIs(t, err, ErrBillingWalletInsufficient)
	ok, err := TryReserveUserQuotaForOperation(f.owner.Id, 1, "request:personal")
	require.NoError(t, err)
	assert.False(t, ok, "a refund hold cannot become spendable through a personal key")
	for range 2 {
		_, err = TerminateAdtyTopUpManualRefund(ownerRefundTrade, id, f.finance.Id, "", "withdrawn")
		require.NoError(t, err)
	}
	require.NoError(t, f.reserve("released", 5_000_000))
}

func TestOwnerTopupRefundApprovalRacesAPIPG(t *testing.T) {
	f := newOwnerRefundFixture(t)
	_, err := RefundAdtyTopUp(ownerRefundTrade, "apply", f.finance.Id, "race", 5_000_000)
	require.NoError(t, err)
	start := make(chan struct{})
	approved, reserved := make(chan error, 1), make(chan error, 1)
	go func() {
		<-start
		_, err := ApproveAdtyTopUpManualRefund(ownerRefundTrade, "race", f.finance.Id, "", "")
		approved <- err
	}()
	go func() { <-start; reserved <- f.reserve("race", 5_000_000) }()
	close(start)
	approvalErr, reserveErr := <-approved, <-reserved
	require.NotEqual(t, approvalErr == nil, reserveErr == nil, "exactly one can reserve the full lot: approve=%v, API=%v", approvalErr, reserveErr)
	left, err := AdtyOwnerPrincipalAvailable(f.db, f.owner.Id, f.site.Id)
	require.NoError(t, err)
	require.Zero(t, left)
}

func TestOwnerTopupRefundReclaimsExactHoldAndRecordsPayoutPG(t *testing.T) {
	f := newOwnerRefundFixture(t)
	const id = "reclaim"
	finance := f.finance.Id
	_, err := RefundAdtyTopUp(ownerRefundTrade, "offline", finance, id, 3_000_000)
	require.NoError(t, err)
	_, err = ApproveAdtyTopUpManualRefund(ownerRefundTrade, id, finance, "", "")
	require.NoError(t, err)
	// Another source cannot repair a shortage or a changed source site.
	require.NoError(t, CreateAdtyFundBatchesWithSiteTx(f.db, f.owner.Id, &f.site.Id, "other", 3_000_000, 0))
	require.NoError(t, f.db.Model(&f.lot).Update("site_id", nil).Error)
	_, err = ReclaimAdtyTopUpManualRefund(ownerRefundTrade, id, finance, "")
	assert.Error(t, err, "a moved lot must not produce a debit or a shortfall")
	require.NoError(t, f.db.Model(&f.lot).Update("site_id", f.site.Id).Error)
	for range 2 {
		r, err := ReclaimAdtyTopUpManualRefund(ownerRefundTrade, id, finance, "")
		require.NoError(t, err)
		assert.EqualValues(t, 3_000_000, r.Refund.DeductedPrincipalQuota)
		assert.Zero(t, r.Refund.ShortfallQuota)
	}
	_, err = PlanAdtyTopUpManualRefundBonus(ownerRefundTrade, id, finance, "")
	require.NoError(t, err)
	_, err = PlanAdtyTopUpManualRefundCommission(ownerRefundTrade, id, finance, "")
	require.NoError(t, err)
	_, err = CompleteAdtyTopUpManualRefund(ownerRefundTrade, id, finance, "")
	require.Error(t, err)
	payout := AdtyManualRefundPayout{Status: AdtyManualRefundPayoutSuccess, Currency: "USDT", Amount: decimal.NewFromInt(6), PaidAt: common.GetTimestamp(), Voucher: "wire-receipt"}
	_, err = RecordAdtyTopUpManualRefundPayout(ownerRefundTrade, id, f.owner.Id, "", payout)
	assert.Error(t, err)
	_, err = RecordAdtyTopUpManualRefundPayout(ownerRefundTrade, id, finance, "", payout)
	require.NoError(t, err)
	for range 2 {
		r, err := CompleteAdtyTopUpManualRefund(ownerRefundTrade, id, finance, "")
		require.NoError(t, err)
		assert.Positive(t, r.Refund.CompletedAt)
		assert.Equal(t, "6", r.Refund.RequestUSD.String())
		assert.Equal(t, "wire-receipt", r.Refund.PayoutVoucher)
	}
	var wallet AdtySiteWallet
	require.NoError(t, f.db.Where("site_id = ? AND kind = ?", f.site.Id, AdtySiteWalletOperating).First(&wallet).Error)
	assert.Equal(t, "4", wallet.Balance.String())
	var entries []AdtySiteWalletLedger
	require.NoError(t, f.db.Where("wallet_id = ? AND ref_id = ?", wallet.Id, ownerRefundTrade).Find(&entries).Error)
	if assert.Len(t, entries, 1) {
		assert.Equal(t, "-6", entries[0].Delta.String())
	}
	var lots []AdtyFundBatch
	require.NoError(t, f.db.Order("id").Find(&lots).Error)
	require.Len(t, lots, 2)
	assert.EqualValues(t, 2_000_000, lots[0].Remaining)
	assert.EqualValues(t, 3_000_000, lots[1].Remaining)
	var owner User
	require.NoError(t, f.db.First(&owner, f.owner.Id).Error)
	assert.Equal(t, 2_000_000, owner.Quota+owner.QuotaPending)
}
