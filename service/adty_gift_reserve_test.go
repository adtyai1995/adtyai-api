package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReserveSiteGiftUsesEarningThenAuthorizedMargin(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "gift-freeze")
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.RequireFromString("4"), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-4",
	})
	require.NoError(t, err)
	require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.RequireFromString("20"), "gift-cycle"))
	adty_setting.AuthorizeSiteBonusMarginFill(site.Id, true)
	t.Cleanup(func() { adty_setting.AuthorizeSiteBonusMarginFill(site.Id, false) })
	ext := &model.AdtyTopupOrder{TradeNo: "GIFT-1", SiteId: site.Id, BonusUSD: decimal.RequireFromString("10"), Method: "usdt.trc20", BonusMethod: "usdt", BonusExpiresAt: 0}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return ReserveAdtySiteGiftTx(tx, ext)
	}))
	require.Equal(t, "earning+margin", ext.GiftReserveSource)
	require.True(t, ext.GiftReserveEarning.Equal(decimal.RequireFromString("4")))
	require.True(t, ext.GiftReserveMargin.Equal(decimal.RequireFromString("6")))
	require.EqualValues(t, 0, ext.BonusExpiresAt)
	earning, margin := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earning.Balance.Equal(decimal.RequireFromString("4")), "reserve must not spend earning balance")
	require.True(t, earning.FrozenUSD.Equal(decimal.RequireFromString("4")))
	require.True(t, margin.Balance.Equal(decimal.RequireFromString("20")), "reserve must not spend margin balance")
	require.True(t, margin.FrozenUSD.Equal(decimal.RequireFromString("6")))
	assertGiftLedger(t, db, site.Id, "gift_reserve", "GIFT-1", model.AdtySiteWalletEarnings, "0", "4")
	assertGiftLedger(t, db, site.Id, "gift_reserve", "GIFT-1", model.AdtySiteWalletMargin, "0", "6")
}

func TestCancelSiteGiftReleasesFreezeIdempotently(t *testing.T) {
	db := prepareSiteDB(t)
	_, site, customer, ext := seedReservedSiteGift(t, db, "gift-cancel", "10")
	earningBefore, marginBefore := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earningBefore.FrozenUSD.Equal(decimal.RequireFromString("4")))
	require.True(t, marginBefore.FrozenUSD.Equal(decimal.RequireFromString("6")))

	canceled, err := model.CancelAdtyTopUp(ext.TradeNo, customer.Id)
	require.NoError(t, err)
	require.Equal(t, ext.TradeNo, canceled.TradeNo)

	earning, margin := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earning.Balance.Equal(earningBefore.Balance))
	require.True(t, earning.FrozenUSD.IsZero())
	require.True(t, margin.Balance.Equal(marginBefore.Balance))
	require.True(t, margin.FrozenUSD.IsZero())
	assertGiftLedger(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletEarnings, "0", "-4")
	assertGiftLedger(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletMargin, "0", "-6")

	_, err = model.CancelAdtyTopUp(ext.TradeNo, customer.Id)
	require.ErrorIs(t, err, model.ErrAdtyTopUpNotCancel)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return ReleaseAdtySiteGiftTx(tx, ext)
	}))
	earningAgain, marginAgain := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earningAgain.Balance.Equal(earning.Balance))
	require.True(t, earningAgain.FrozenUSD.IsZero())
	require.True(t, marginAgain.Balance.Equal(margin.Balance))
	require.True(t, marginAgain.FrozenUSD.IsZero())
	require.EqualValues(t, 1, countGiftLedgers(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletEarnings))
	require.EqualValues(t, 1, countGiftLedgers(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletMargin))
}

func TestReplaceSiteGiftReleasesFreezeIdempotently(t *testing.T) {
	db := prepareSiteDB(t)
	_, site, customer, ext := seedReservedSiteGift(t, db, "gift-replace", "10")
	earningBefore, marginBefore := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earningBefore.FrozenUSD.Equal(decimal.RequireFromString("4")))
	require.True(t, marginBefore.FrozenUSD.Equal(decimal.RequireFromString("6")))

	replaced, err := model.ReplaceAdtyTopUpAfterLock(ext.TradeNo, customer.Id, nil)
	require.NoError(t, err)
	require.Equal(t, ext.TradeNo, replaced.TradeNo)

	earning, margin := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earning.Balance.Equal(earningBefore.Balance))
	require.True(t, earning.FrozenUSD.IsZero())
	require.True(t, margin.Balance.Equal(marginBefore.Balance))
	require.True(t, margin.FrozenUSD.IsZero())
	assertGiftLedger(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletEarnings, "0", "-4")
	assertGiftLedger(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletMargin, "0", "-6")

	_, err = model.ReplaceAdtyTopUpAfterLock(ext.TradeNo, customer.Id, nil)
	require.ErrorIs(t, err, model.ErrAdtyTopUpNotCancel)
	_, err = model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.ErrorIs(t, err, model.ErrTopUpStatusInvalid)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return ReleaseAdtySiteGiftTx(tx, ext)
	}))
	earningAgain, marginAgain := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earningAgain.Balance.Equal(earning.Balance))
	require.True(t, earningAgain.FrozenUSD.IsZero())
	require.True(t, marginAgain.Balance.Equal(margin.Balance))
	require.True(t, marginAgain.FrozenUSD.IsZero())
	require.EqualValues(t, 1, countGiftLedgers(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletEarnings))
	require.EqualValues(t, 1, countGiftLedgers(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletMargin))
}

func TestExpireSiteGiftReleasesFreezeIdempotently(t *testing.T) {
	db := prepareSiteDB(t)
	_, site, _, ext := seedReservedSiteGift(t, db, "gift-expire", "10")
	require.NoError(t, db.Model(ext).Update("expire_at", 100).Error)
	earningBefore, marginBefore := mustSiteGiftWallets(t, db, site.Id)

	expired, err := model.ExpireAdtyTopUps(common.GetTimestamp(), 50)
	require.NoError(t, err)
	require.Len(t, expired, 1)
	require.Equal(t, ext.TradeNo, expired[0].TradeNo)

	earning, margin := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earning.Balance.Equal(earningBefore.Balance))
	require.True(t, earning.FrozenUSD.IsZero())
	require.True(t, margin.Balance.Equal(marginBefore.Balance))
	require.True(t, margin.FrozenUSD.IsZero())
	assertGiftLedger(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletEarnings, "0", "-4")
	assertGiftLedger(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletMargin, "0", "-6")

	again, err := model.ExpireAdtyTopUps(common.GetTimestamp(), 50)
	require.NoError(t, err)
	require.Empty(t, again)
	earningAgain, marginAgain := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earningAgain.FrozenUSD.IsZero())
	require.True(t, marginAgain.FrozenUSD.IsZero())
	require.EqualValues(t, 1, countGiftLedgers(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletEarnings))
}

func TestPayAndSettleSiteGiftDoesNotDoubleSpend(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site, _, ext := seedReservedSiteGift(t, db, "gift-pay", "10")
	earningBefore, marginBefore := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earningBefore.Balance.Equal(decimal.RequireFromString("4")))
	require.True(t, earningBefore.FrozenUSD.Equal(decimal.RequireFromString("4")))
	require.True(t, marginBefore.Balance.Equal(decimal.RequireFromString("20")))
	require.True(t, marginBefore.FrozenUSD.Equal(decimal.RequireFromString("6")))

	_, err := model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)

	earningPaid, marginPaid := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earningPaid.Balance.Equal(earningBefore.Balance), "payment converts reserve to a guarantee hold, not a spend")
	require.True(t, earningPaid.FrozenUSD.Equal(earningBefore.FrozenUSD))
	require.True(t, marginPaid.Balance.Equal(marginBefore.Balance))
	require.True(t, marginPaid.FrozenUSD.Equal(marginBefore.FrozenUSD))

	settled, err := RunAdtySiteSettlementOnce(time.Now().AddDate(0, 0, 1))
	require.NoError(t, err)
	require.Equal(t, 1, settled)

	earningSettled, marginSettled := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earningSettled.Balance.Equal(earningBefore.Balance), "settlement must not debit the reserved gift again")
	require.True(t, earningSettled.FrozenUSD.Equal(earningBefore.FrozenUSD))
	require.True(t, marginSettled.Balance.Equal(marginBefore.Balance))
	require.True(t, marginSettled.FrozenUSD.Equal(marginBefore.FrozenUSD))
	require.EqualValues(t, 0, countLedgersByType(t, db, site.Id, "site_earning_settlement"))

	var bonus model.AdtySiteEarningDaily
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteEarningBonus).First(&bonus).Error)
	require.True(t, bonus.Earning.Equal(decimal.RequireFromString("-10")))
	require.Equal(t, model.AdtySiteEarningAvailable, bonus.Status)
	affiliate, err := Referral.EnsureAffiliate(owner.Id)
	require.NoError(t, err)
	require.InDelta(t, 0, account(t, db, affiliate.Id).AvailableAmount, 1e-9, "gift settlement must not reduce the commission book")
}

func TestLatePayAfterCancelReacquiresGiftGuarantee(t *testing.T) {
	db := prepareSiteDB(t)
	_, site, customer, ext := seedReservedSiteGift(t, db, "gift-late-reacq", "10")
	_, err := model.CancelAdtyTopUp(ext.TradeNo, customer.Id)
	require.NoError(t, err)
	earningOpen, marginOpen := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earningOpen.FrozenUSD.IsZero())
	require.True(t, marginOpen.FrozenUSD.IsZero())

	_, err = model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)

	earning, margin := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earning.Balance.Equal(earningOpen.Balance))
	require.True(t, earning.FrozenUSD.Equal(decimal.RequireFromString("4")))
	require.True(t, margin.Balance.Equal(marginOpen.Balance))
	require.True(t, margin.FrozenUSD.Equal(decimal.RequireFromString("6")))
	require.EqualValues(t, 55_000_000, userQuotaOf(t, db, customer.Id))
	require.EqualValues(t, 0, countGiftFulfillments(t, db, ext.TradeNo))
	assertGiftLedger(t, db, site.Id, "gift_reserve", ext.TradeNo, model.AdtySiteWalletEarnings, "0", "4")
	// The reacquire row is a second reserve with the same hold, distinct key.
	require.EqualValues(t, 2, countGiftLedgers(t, db, site.Id, "gift_reserve", ext.TradeNo, model.AdtySiteWalletEarnings))
}

func TestLatePayAfterExpireReacquiresGiftGuarantee(t *testing.T) {
	db := prepareSiteDB(t)
	_, site, customer, ext := seedReservedSiteGift(t, db, "gift-late-exp", "10")
	require.NoError(t, db.Model(ext).Update("expire_at", 100).Error)
	expired, err := model.ExpireAdtyTopUps(common.GetTimestamp(), 50)
	require.NoError(t, err)
	require.Len(t, expired, 1)

	_, err = model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)

	earning, margin := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earning.FrozenUSD.Equal(decimal.RequireFromString("4")))
	require.True(t, margin.FrozenUSD.Equal(decimal.RequireFromString("6")))
	require.EqualValues(t, 55_000_000, userQuotaOf(t, db, customer.Id))
	require.EqualValues(t, 0, countGiftFulfillments(t, db, ext.TradeNo))
}

func TestLatePayAfterCancelRecordsPendingGiftWhenUncovered(t *testing.T) {
	db := prepareSiteDB(t)
	_, site, customer, ext := seedReservedSiteGift(t, db, "gift-late-pend", "10")
	_, err := model.CancelAdtyTopUp(ext.TradeNo, customer.Id)
	require.NoError(t, err)
	require.NoError(t, drainSiteGiftWallets(t, db, site.Id, "gift-late-pend"))

	notifies := 0
	old := adtyNotifyGiftFulfillmentPending
	adtyNotifyGiftFulfillmentPending = func(*model.AdtyTopupOrder) { notifies++ }
	t.Cleanup(func() { adtyNotifyGiftFulfillmentPending = old })

	_, err = model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)

	earning, margin := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earning.FrozenUSD.IsZero())
	require.True(t, margin.FrozenUSD.IsZero())
	require.EqualValues(t, 50_000_000, userQuotaOf(t, db, customer.Id))
	require.EqualValues(t, 1, notifies)
	var row model.AdtyGiftFulfillment
	require.NoError(t, db.Where("trade_no = ?", ext.TradeNo).First(&row).Error)
	require.Equal(t, model.AdtyGiftFulfillmentPending, row.Status)
	require.True(t, row.BonusUSD.Equal(decimal.RequireFromString("10")))
	require.EqualValues(t, 5_000_000, row.BonusQuota)
	reloaded, err := model.GetAdtyTopupOrder(ext.TradeNo)
	require.NoError(t, err)
	require.Equal(t, model.AdtyGiftFulfillmentPending, reloaded.GiftFulfillmentStatus)

	already, err := model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)
	require.True(t, already)
	require.EqualValues(t, 50_000_000, userQuotaOf(t, db, customer.Id))
	require.EqualValues(t, 1, countGiftFulfillments(t, db, ext.TradeNo))
	require.EqualValues(t, 1, notifies)
}

// 站点保障金补不上加赠时，只有本金到账；入账说明必须写实际到账额度。
// 报下单时冻结的全额加赠，用户按日志算余额就算不平（ISS-R4-0112）。
func TestPendingGiftCreditLogShowsActuallyCreditedQuota(t *testing.T) {
	db := prepareSiteDB(t)
	_, site, customer, ext := seedReservedSiteGift(t, db, "gift-log-pend", "10")
	_, err := model.CancelAdtyTopUp(ext.TradeNo, customer.Id)
	require.NoError(t, err)
	require.NoError(t, drainSiteGiftWallets(t, db, site.Id, "gift-log-pend"))
	_, err = model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)

	credited := userQuotaOf(t, db, customer.Id)
	require.EqualValues(t, 50_000_000, credited)
	var entry model.Log
	require.NoError(t, db.Where("user_id = ? AND type = ?", customer.Id, model.LogTypeTopup).Order("id DESC").First(&entry).Error)
	require.Contains(t, entry.Content, "plus $0.00 bonus", "说明里的加赠必须是实际到账的那部分")
	require.Contains(t, entry.Content, logger.FormatQuota(credited), "说明里必须写出实际到账额度")
	require.NotContains(t, entry.Content, "plus $10.00 bonus", "不能报下单时冻结的全额加赠")
}

func TestSiteGiftBonusQuotaUsesAdtyBonusUSDForSite(t *testing.T) {
	plain, err := siteGiftBonusQuota(&model.AdtyTopupOrder{
		SiteId: 1, BonusUSD: decimal.RequireFromString("10"),
	})
	require.NoError(t, err)
	require.EqualValues(t, 5_000_000, plain)

	mainSite, err := siteGiftBonusQuota(&model.AdtyTopupOrder{BonusUSD: decimal.RequireFromString("10")})
	require.NoError(t, err)
	require.Zero(t, mainSite, "a main-site order has no site-funded gift")
}

func TestRefundPaidSiteGiftReleasesGuaranteeIdempotently(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site, _, ext := seedReservedSiteGift(t, db, "gift-refund", "10")
	earningBefore, marginBefore := mustSiteGiftWallets(t, db, site.Id)
	_, err := model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)
	earningPaid, marginPaid := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earningPaid.FrozenUSD.Equal(decimal.RequireFromString("4")))
	require.True(t, marginPaid.FrozenUSD.Equal(decimal.RequireFromString("6")))

	preview, err := model.PreviewAdtyTopUpRefund(ext.TradeNo)
	require.NoError(t, err)
	require.EqualValues(t, 50_000_000, preview.RefundQuota)
	result, err := refundAdtyTopUp(t, ext.TradeNo, "unused principal", owner.Id, "gift-refund-1")
	require.NoError(t, err)
	require.EqualValues(t, 50_000_000, result.Occupancy.CompletedQuota)

	earning, margin := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earning.Balance.Equal(earningBefore.Balance))
	require.True(t, earning.FrozenUSD.IsZero())
	require.True(t, margin.Balance.Equal(marginBefore.Balance))
	require.True(t, margin.FrozenUSD.IsZero())
	assertGiftLedger(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletEarnings, "0", "-4")
	assertGiftLedger(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletMargin, "0", "-6")
	require.EqualValues(t, 1, countGiftLedgers(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletEarnings))

	requestUSD, err := model.RefundQuotaToUSD(preview.RefundQuota)
	require.NoError(t, err)
	again, err := completeManualRefundForTest(t, ext.TradeNo, "unused principal", owner.Id, "gift-refund-1", requestUSD)
	require.NoError(t, err)
	require.EqualValues(t, result.Occupancy.CompletedQuota, again.Occupancy.CompletedQuota)
	earningAgain, marginAgain := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earningAgain.FrozenUSD.IsZero())
	require.True(t, marginAgain.FrozenUSD.IsZero())
	require.EqualValues(t, 1, countGiftLedgers(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletEarnings))
	require.EqualValues(t, 1, countGiftLedgers(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletMargin))
}

func TestRefundAfterPendingFulfillmentReturnsFullPrincipal(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site, customer, ext := seedReservedSiteGift(t, db, "gift-rf-pend", "10")
	_, err := model.CancelAdtyTopUp(ext.TradeNo, customer.Id)
	require.NoError(t, err)
	require.NoError(t, drainSiteGiftWallets(t, db, site.Id, "gift-rf-pend"))
	_, err = model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)
	require.EqualValues(t, 50_000_000, userQuotaOf(t, db, customer.Id))

	preview, err := model.PreviewAdtyTopUpRefund(ext.TradeNo)
	require.NoError(t, err)
	require.EqualValues(t, 50_000_000, preview.RefundQuota)
	result, err := refundAdtyTopUp(t, ext.TradeNo, "pending gift refund", owner.Id, "gift-rf-pend-1")
	require.NoError(t, err)
	require.EqualValues(t, 50_000_000, result.Occupancy.CompletedQuota)
	require.EqualValues(t, 0, userQuotaOf(t, db, customer.Id))
	var row model.AdtyGiftFulfillment
	require.NoError(t, db.Where("trade_no = ?", ext.TradeNo).First(&row).Error)
	require.Equal(t, model.AdtyGiftFulfillmentCanceled, row.Status)
}

func TestConsumingSiteGiftDebitsGuaranteeOnce(t *testing.T) {
	db := prepareSiteDB(t)
	_, site, customer, ext := seedReservedSiteGift(t, db, "gift-spend", "10")
	_, err := model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.ConsumeAdtyFundBatchesTx(tx, customer.Id, 50_000_000, 55_000_000)
	}))
	earning, margin := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earning.FrozenUSD.Equal(decimal.RequireFromString("4")))
	require.True(t, earning.Balance.Equal(decimal.RequireFromString("4")))
	require.True(t, margin.FrozenUSD.Equal(decimal.RequireFromString("6")))
	require.True(t, margin.Balance.Equal(decimal.RequireFromString("20")))

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.ConsumeAdtyFundBatchesTx(tx, customer.Id, 2_500_000, 5_000_000)
	}))
	earning, margin = mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earning.FrozenUSD.Equal(decimal.RequireFromString("2")))
	require.True(t, earning.Balance.Equal(decimal.RequireFromString("2")))
	require.True(t, margin.FrozenUSD.Equal(decimal.RequireFromString("3")))
	require.True(t, margin.Balance.Equal(decimal.RequireFromString("17")))

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.ConsumeAdtyFundBatchesTx(tx, customer.Id, 2_500_000, 2_500_000)
	}))
	earning, margin = mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earning.FrozenUSD.IsZero())
	require.True(t, earning.Balance.IsZero())
	require.True(t, margin.FrozenUSD.IsZero())
	require.True(t, margin.Balance.Equal(decimal.RequireFromString("14")))
	require.EqualValues(t, 2, countGiftLedgers(t, db, site.Id, "gift_spend", ext.TradeNo, model.AdtySiteWalletEarnings))
	require.EqualValues(t, 2, countGiftLedgers(t, db, site.Id, "gift_spend", ext.TradeNo, model.AdtySiteWalletMargin))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		held, err := netGiftHoldTx(tx, site.Id, ext.TradeNo)
		if err != nil {
			return err
		}
		require.True(t, held.IsZero(), "exhausting the gift batch must not leave or re-open the original reserve")
		return nil
	}))
}

func TestAllocatedGiftSettlementConsumesOriginalGuarantee(t *testing.T) {
	for _, test := range []struct {
		name          string
		principalUsed bool
		reserved      int
		actual        int
		bonusSpent    int64
		rollback      bool
	}{
		{name: "bonus_only", principalUsed: true, reserved: 2_500_000, actual: 2_500_000, bonusSpent: 2_500_000},
		{name: "mixed", reserved: 52_500_000, actual: 52_500_000, bonusSpent: 2_500_000},
		{name: "supplement_bonus", reserved: 50_000_000, actual: 52_500_000, bonusSpent: 2_500_000},
		{name: "supplement_same_source", reserved: 51_000_000, actual: 52_500_000, bonusSpent: 2_500_000},
		{name: "exhaust_bonus", reserved: 55_000_000, actual: 55_000_000, bonusSpent: 5_000_000},
		{name: "release_unused_bonus", reserved: 52_500_000, actual: 50_000_000},
		{name: "rollback_then_retry", reserved: 52_500_000, actual: 52_500_000, bonusSpent: 2_500_000, rollback: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := prepareSiteDB(t)
			pgtest.RecordServerVersion(t, db)
			_, site, customer, ext := seedReservedSiteGift(t, db, "allocated-gift", "10")
			_, err := model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
				Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
				PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
			}, "")
			require.NoError(t, err)
			if test.principalUsed {
				_, err := model.ReserveBillingOperation(model.BillingOperation{
					OperationKey: "request:gift-principal", UserId: customer.Id, SiteId: site.Id,
				}, 50_000_000)
				require.NoError(t, err)
				require.NoError(t, model.QueueBillingSettlement("request:gift-principal", 50_000_000, false))
				_, applied, err := model.ApplyBillingSettlement("request:gift-principal")
				require.NoError(t, err)
				require.True(t, applied)
			}
			const key = "request:allocated-gift"
			_, err = model.ReserveBillingOperation(model.BillingOperation{
				OperationKey: key, UserId: customer.Id, SiteId: site.Id,
			}, test.reserved)
			require.NoError(t, err)
			require.NoError(t, model.QueueBillingSettlement(key, test.actual, false))
			var beforeBatches []model.AdtyFundBatch
			require.NoError(t, db.Where("user_id = ?", customer.Id).Order("id").Find(&beforeBatches).Error)
			earningBefore, marginBefore := mustSiteGiftWallets(t, db, site.Id)
			if test.rollback {
				require.NoError(t, db.Exec("ALTER TABLE adty_site_wallet_ledgers ADD CONSTRAINT test_gift_spend_unavailable CHECK (type <> 'gift_spend')").Error)
				t.Cleanup(func() {
					require.NoError(t, db.Exec("ALTER TABLE adty_site_wallet_ledgers DROP CONSTRAINT IF EXISTS test_gift_spend_unavailable").Error)
				})
				_, _, err := model.ApplyBillingSettlement(key)
				require.Error(t, err, "a failed guarantee debit must roll back the whole settlement")
				var afterBatches []model.AdtyFundBatch
				require.NoError(t, db.Where("user_id = ?", customer.Id).Order("id").Find(&afterBatches).Error)
				require.Equal(t, beforeBatches, afterBatches)
				earning, margin := mustSiteGiftWallets(t, db, site.Id)
				require.True(t, earning.Balance.Equal(earningBefore.Balance) && earning.FrozenUSD.Equal(earningBefore.FrozenUSD))
				require.True(t, margin.Balance.Equal(marginBefore.Balance) && margin.FrozenUSD.Equal(marginBefore.FrozenUSD))
				var operation model.BillingOperation
				require.NoError(t, db.First(&operation, "operation_key = ?", key).Error)
				require.Equal(t, model.BillingSettlePending, operation.State)
				require.NoError(t, db.Exec("ALTER TABLE adty_site_wallet_ledgers DROP CONSTRAINT test_gift_spend_unavailable").Error)
			}
			row, applied, err := model.ApplyBillingSettlement(key)
			require.NoError(t, err)
			require.True(t, applied)
			require.Equal(t, test.actual, row.AppliedQuota)
			require.Zero(t, row.FundBatchGap)
			earning, margin := mustSiteGiftWallets(t, db, site.Id)
			spentEarning, spentMargin := decimal.Zero, decimal.Zero
			if test.bonusSpent == 2_500_000 {
				spentEarning, spentMargin = decimal.NewFromInt(2), decimal.NewFromInt(3)
			} else if test.bonusSpent == 5_000_000 {
				spentEarning, spentMargin = decimal.NewFromInt(4), decimal.NewFromInt(6)
			}
			require.True(t, earning.Balance.Equal(earningBefore.Balance.Sub(spentEarning)), "earning balance=%s", earning.Balance)
			require.True(t, earning.FrozenUSD.Equal(earningBefore.FrozenUSD.Sub(spentEarning)), "earning hold=%s", earning.FrozenUSD)
			require.True(t, margin.Balance.Equal(marginBefore.Balance.Sub(spentMargin)), "margin balance=%s", margin.Balance)
			require.True(t, margin.FrozenUSD.Equal(marginBefore.FrozenUSD.Sub(spentMargin)), "margin hold=%s", margin.FrozenUSD)
			var bonus model.AdtyFundBatch
			require.NoError(t, db.Where("user_id = ? AND source = ? AND kind = ?", customer.Id, ext.TradeNo, "bonus").First(&bonus).Error)
			require.EqualValues(t, 5_000_000-test.bonusSpent, bonus.Remaining)
			var giftLedgers int64
			if test.bonusSpent > 0 {
				giftLedgers = 1
			}
			require.Equal(t, giftLedgers, countGiftLedgers(t, db, site.Id, "gift_spend", ext.TradeNo, model.AdtySiteWalletEarnings))
			require.Equal(t, giftLedgers, countGiftLedgers(t, db, site.Id, "gift_spend", ext.TradeNo, model.AdtySiteWalletMargin))
			_, applied, err = model.ApplyBillingSettlement(key)
			require.NoError(t, err)
			require.False(t, applied)
			replayedEarning, replayedMargin := mustSiteGiftWallets(t, db, site.Id)
			require.True(t, replayedEarning.Balance.Equal(earning.Balance) && replayedEarning.FrozenUSD.Equal(earning.FrozenUSD))
			require.True(t, replayedMargin.Balance.Equal(margin.Balance) && replayedMargin.FrozenUSD.Equal(margin.FrozenUSD))
			require.Equal(t, giftLedgers, countGiftLedgers(t, db, site.Id, "gift_spend", ext.TradeNo, model.AdtySiteWalletEarnings))
		})
	}
}

func TestNetGiftHoldIncludesGiftSpend(t *testing.T) {
	db := prepareSiteDB(t)
	_, site, customer, ext := seedReservedSiteGift(t, db, "gift-net-hold", "10")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		held, err := netGiftHoldTx(tx, site.Id, ext.TradeNo)
		if err != nil {
			return err
		}
		require.True(t, held.Equal(decimal.RequireFromString("10")))
		earning, margin, err := netGiftHoldByKindTx(tx, site.Id, ext.TradeNo)
		if err != nil {
			return err
		}
		require.True(t, earning.Equal(decimal.RequireFromString("4")))
		require.True(t, margin.Equal(decimal.RequireFromString("6")))
		return nil
	}))

	_, err := model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.ConsumeAdtyFundBatchesTx(tx, customer.Id, 50_000_000, 55_000_000)
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.ConsumeAdtyFundBatchesTx(tx, customer.Id, 2_500_000, 5_000_000)
	}))

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		held, err := netGiftHoldTx(tx, site.Id, ext.TradeNo)
		if err != nil {
			return err
		}
		require.True(t, held.Equal(decimal.RequireFromString("5")), "net hold must include negative gift_spend HoldDelta")
		earning, margin, err := netGiftHoldByKindTx(tx, site.Id, ext.TradeNo)
		if err != nil {
			return err
		}
		require.True(t, earning.Equal(decimal.RequireFromString("2")))
		require.True(t, margin.Equal(decimal.RequireFromString("3")))
		return nil
	}))
}

func TestRefundReleaseUsesRemainingNetHoldAfterGiftSpend(t *testing.T) {
	db := prepareSiteDB(t)
	_, site, customer, ext := seedReservedSiteGift(t, db, "gift-rf-spend", "10")
	_, err := model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.ConsumeAdtyFundBatchesTx(tx, customer.Id, 50_000_000, 55_000_000)
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.ConsumeAdtyFundBatchesTx(tx, customer.Id, 2_500_000, 5_000_000)
	}))
	earning, margin := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earning.FrozenUSD.Equal(decimal.RequireFromString("2")))
	require.True(t, margin.FrozenUSD.Equal(decimal.RequireFromString("3")))
	earnBalance, marginBalance := earning.Balance, margin.Balance

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var row model.AdtyTopupOrder
		if err := tx.Where("trade_no = ?", ext.TradeNo).First(&row).Error; err != nil {
			return err
		}
		row.RefundBonusQuota = 5_000_000
		row.RefundDeductedQuota = 5_000_000
		return releaseUnusedSiteGiftGuaranteeTx(tx, &row)
	}))

	earning, margin = mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earning.FrozenUSD.IsZero())
	require.True(t, margin.FrozenUSD.IsZero())
	require.True(t, earning.Balance.Equal(earnBalance), "refund release must not re-debit already spent guarantee")
	require.True(t, margin.Balance.Equal(marginBalance), "refund release must not re-debit already spent guarantee")
	assertGiftLedger(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletEarnings, "0", "-2")
	assertGiftLedger(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletMargin, "0", "-3")
	require.EqualValues(t, 1, countGiftLedgers(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletEarnings))
	require.EqualValues(t, 1, countGiftLedgers(t, db, site.Id, "gift_release", ext.TradeNo, model.AdtySiteWalletMargin))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		held, err := netGiftHoldTx(tx, site.Id, ext.TradeNo)
		if err != nil {
			return err
		}
		require.True(t, held.IsZero())
		return nil
	}))
}

func TestQuietBonusDebitSpendsSiteGiftGuarantee(t *testing.T) {
	db := prepareSiteDB(t)
	_, site, customer, ext := seedReservedSiteGift(t, db, "gift-quiet", "10")
	_, err := model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)
	operatingBefore := mustSiteOperatingWallet(t, db, site.Id)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.ConsumeAdtyFundBatchesQuietTx(tx, customer.Id, 52_500_000, 55_000_000)
	}))
	requirePartialTopupGiftSpendWithoutOperatingDebit(t, db, site.Id, ext.TradeNo, operatingBefore)
}

func TestAdjustUserQuotaReduceReleasesSiteGiftGuarantee(t *testing.T) {
	db := prepareSiteDB(t)
	_, site, customer, ext := seedReservedSiteGift(t, db, "gift-adjust-sub", "10")
	_, err := model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)
	operatingBefore := mustSiteOperatingWallet(t, db, site.Id)

	adjusted, err := model.AdjustUserQuotaWithLedger(customer.Id, 1, common.RoleRootUser, "subtract", 52_500_000, "trim bonus-backed balance", "gift-adjust-sub-1")
	require.NoError(t, err)
	require.False(t, adjusted.Replayed)
	require.Equal(t, 55_000_000, adjusted.Before)
	require.Equal(t, 2_500_000, adjusted.After)
	require.EqualValues(t, 2_500_000, userQuotaOf(t, db, customer.Id))
	requirePartialTopupGiftSpendWithoutOperatingDebit(t, db, site.Id, ext.TradeNo, operatingBefore)
}

func TestApprovedStaffDecreaseReleasesSiteGiftGuarantee(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site, customer, ext := seedReservedSiteGift(t, db, "gift-staff-dec", "10")
	staffUser := referralUser(t, db, "gift-staff-dec-staff")
	require.NoError(t, db.Create(&model.AdtySiteStaff{
		SiteId: site.Id, OwnerUserId: owner.Id, UserId: staffUser.Id, Status: "enabled", Role: "support",
	}).Error)
	_, err := model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)
	operatingBefore := mustSiteOperatingWallet(t, db, site.Id)

	request, err := SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceDecrease, decimal.RequireFromString("105"), "gift-staff-dec-1")
	require.NoError(t, err)
	require.NoError(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", request.Id, true))
	require.EqualValues(t, 2_500_000, userQuotaOf(t, db, customer.Id))
	requirePartialTopupGiftSpendWithoutOperatingDebit(t, db, site.Id, ext.TradeNo, operatingBefore)
}

func requirePartialTopupGiftSpendWithoutOperatingDebit(t *testing.T, db *gorm.DB, siteID int, tradeNo string, operatingBefore model.AdtySiteWallet) {
	t.Helper()
	earning, margin := mustSiteGiftWallets(t, db, siteID)
	require.True(t, earning.FrozenUSD.Equal(decimal.RequireFromString("2")))
	require.True(t, earning.Balance.Equal(decimal.RequireFromString("2")))
	require.True(t, margin.FrozenUSD.Equal(decimal.RequireFromString("3")))
	require.True(t, margin.Balance.Equal(decimal.RequireFromString("17")))
	require.EqualValues(t, 1, countGiftLedgers(t, db, siteID, "gift_spend", tradeNo, model.AdtySiteWalletEarnings))
	require.EqualValues(t, 1, countGiftLedgers(t, db, siteID, "gift_spend", tradeNo, model.AdtySiteWalletMargin))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		held, err := netGiftHoldTx(tx, siteID, tradeNo)
		if err != nil {
			return err
		}
		require.True(t, held.Equal(decimal.RequireFromString("5")))
		return nil
	}))
	operating := mustSiteOperatingWallet(t, db, siteID)
	require.True(t, operating.Balance.Equal(operatingBefore.Balance), "manual or quiet reduction must not debit the operating wallet as paid usage")
	require.EqualValues(t, 0, countLedgersByType(t, db, siteID, "principal_consume"))
}

func mustSiteOperatingWallet(t *testing.T, db *gorm.DB, siteID int) model.AdtySiteWallet {
	t.Helper()
	var wallet model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", siteID, model.AdtySiteWalletOperating).First(&wallet).Error)
	return wallet
}

func drainSiteGiftWallets(t *testing.T, db *gorm.DB, siteID int, prefix string) error {
	t.Helper()
	earning, margin := mustSiteGiftWallets(t, db, siteID)
	if earning.Balance.IsPositive() {
		if _, err := ApplyAdtySiteWalletChange(siteID, model.AdtySiteWalletEarnings, earning.Balance.Neg(), AdtySiteWalletChange{
			Type: "earning_settlement", IdempotencyKey: prefix + "-drain-earn",
		}); err != nil {
			return err
		}
	}
	if margin.Balance.IsPositive() {
		if _, err := ApplyAdtySiteWalletChange(siteID, model.AdtySiteWalletMargin, margin.Balance.Neg(), AdtySiteWalletChange{
			Type: "margin_return", IdempotencyKey: prefix + "-drain-margin", AllowOverdraft: true,
		}); err != nil {
			return err
		}
	}
	return nil
}

func userQuotaOf(t *testing.T, db *gorm.DB, userID int) int {
	t.Helper()
	var user model.User
	require.NoError(t, db.First(&user, userID).Error)
	return user.Quota
}

func countGiftFulfillments(t *testing.T, db *gorm.DB, tradeNo string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&model.AdtyGiftFulfillment{}).Where("trade_no = ?", tradeNo).Count(&n).Error)
	return n
}

func seedReservedSiteGift(t *testing.T, db *gorm.DB, prefix, bonus string) (*model.User, *model.AdtySite, *model.User, *model.AdtyTopupOrder) {
	t.Helper()
	owner, site := openActiveSite(t, db, prefix)
	customer := siteCustomer(t, db, site, prefix+"-customer")
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.RequireFromString("4"), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: prefix + "-earn-4",
	})
	require.NoError(t, err)
	require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.RequireFromString("20"), prefix+"-cycle"))
	adty_setting.AuthorizeSiteBonusMarginFill(site.Id, true)
	t.Cleanup(func() { adty_setting.AuthorizeSiteBonusMarginFill(site.Id, false) })
	bonusUSD := decimal.RequireFromString(bonus)
	face := decimal.NewFromInt(100)
	credit := face.Add(bonusUSD).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Floor().IntPart()
	tradeNo := "EPU-" + prefix
	ext := &model.AdtyTopupOrder{
		TradeNo: tradeNo, UserId: customer.Id, Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
		FaceUSD: face, BonusRate: bonusUSD.Div(face), BonusUSD: bonusUSD, CreditQuota: credit,
		PayAmount: face, PayCurrency: "USDT", TopupGroupRatio: decimal.NewFromInt(1),
		ExpireAt: time.Now().Unix() + 900, SiteId: site.Id,
	}
	require.NoError(t, model.CreateAdtyTopUp(
		&model.TopUp{UserId: customer.Id, Amount: 100, TradeNo: tradeNo, PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt, Status: common.TopUpStatusPending},
		ext,
	))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := ReserveAdtySiteGiftTx(tx, ext); err != nil {
			return err
		}
		return tx.Model(ext).Updates(map[string]any{
			"gift_reserve_source":  ext.GiftReserveSource,
			"gift_reserve_earning": ext.GiftReserveEarning,
			"gift_reserve_margin":  ext.GiftReserveMargin,
		}).Error
	}))
	require.Equal(t, "earning+margin", ext.GiftReserveSource)
	return owner, site, customer, ext
}

func mustSiteGiftWallets(t *testing.T, db *gorm.DB, siteID int) (model.AdtySiteWallet, model.AdtySiteWallet) {
	t.Helper()
	var earning, margin model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", siteID, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.NoError(t, db.Where("site_id = ? AND kind = ?", siteID, model.AdtySiteWalletMargin).First(&margin).Error)
	return earning, margin
}

func assertGiftLedger(t *testing.T, db *gorm.DB, siteID int, typ, tradeNo, kind, delta, hold string) {
	t.Helper()
	var row model.AdtySiteWalletLedger
	require.NoError(t, db.Where("site_id = ? AND type = ? AND ref_id = ? AND wallet_kind = ?", siteID, typ, tradeNo, kind).First(&row).Error)
	require.True(t, row.Delta.Equal(decimal.RequireFromString(delta)), "delta %s want %s", row.Delta, delta)
	require.True(t, row.HoldDelta.Equal(decimal.RequireFromString(hold)), "hold %s want %s", row.HoldDelta, hold)
}

func countGiftLedgers(t *testing.T, db *gorm.DB, siteID int, typ, tradeNo, kind string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ? AND type = ? AND ref_id = ? AND wallet_kind = ?", siteID, typ, tradeNo, kind).Count(&n).Error)
	return n
}

func countLedgersByType(t *testing.T, db *gorm.DB, siteID int, typ string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ? AND type = ?", siteID, typ).Count(&n).Error)
	return n
}

func TestMarginTopUpFulfillsPendingSiteGift(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site, customer, ext := seedReservedSiteGift(t, db, "gift-fulfil", "10")
	_, err := model.CancelAdtyTopUp(ext.TradeNo, customer.Id)
	require.NoError(t, err)
	require.NoError(t, drainSiteGiftWallets(t, db, site.Id, "gift-fulfil"))
	_, err = model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)
	// 本金到账、赠金被扣下：pending，客户只拿到 50,000,000。
	require.EqualValues(t, 50_000_000, userQuotaOf(t, db, customer.Id))
	reloaded, err := model.GetAdtyTopupOrder(ext.TradeNo)
	require.NoError(t, err)
	require.Equal(t, model.AdtyGiftFulfillmentPending, reloaded.GiftFulfillmentStatus)

	notified := 0
	oldNotify := adtyNotifyGiftFulfillmentFulfilled
	adtyNotifyGiftFulfillmentFulfilled = func(*model.AdtyGiftFulfillment, int64) { notified++ }
	t.Cleanup(func() { adtyNotifyGiftFulfillmentFulfilled = oldNotify })

	// 保障还不够：兑现必须是空操作，pending 原样保留。
	n, err := ResumePendingAdtyGiftFulfillments(site.Id)
	require.NoError(t, err)
	require.Zero(t, n)
	require.EqualValues(t, 50_000_000, userQuotaOf(t, db, customer.Id))

	// 保留旧保证金10，再真实划转20。当前整档最低余额门独立保留，
	// 因此补发10后还必须留足档位20；本用例不以透支绕过该门。
	_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletMargin, decimal.NewFromInt(10), AdtySiteWalletChange{
		Type: "legacy_credit", IdempotencyKey: "gift-fulfil-existing-margin",
	})
	require.NoError(t, err)
	_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.RequireFromString("20"), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "gift-fulfil-topup",
	})
	require.NoError(t, err)
	setAdtyMarginTargetForTest(t, site.Id, decimal.NewFromInt(20))
	require.NoError(t, TransferAdtyEarningToMargin(site.Id, owner.Id,
		decimal.RequireFromString("20"), "gift-fulfil-transfer"))

	require.EqualValues(t, 55_000_000, userQuotaOf(t, db, customer.Id))
	require.EqualValues(t, 1, notified)
	var row model.AdtyGiftFulfillment
	require.NoError(t, db.Where("trade_no = ?", ext.TradeNo).First(&row).Error)
	require.Equal(t, model.AdtyGiftFulfillmentFulfilled, row.Status)
	require.Contains(t, row.History, "fulfilled")
	reloaded, err = model.GetAdtyTopupOrder(ext.TradeNo)
	require.NoError(t, err)
	require.Equal(t, model.AdtyGiftFulfillmentFulfilled, reloaded.GiftFulfillmentStatus)
	// 保障这次真的取回来了：保证金上留 10 USD 冻结。
	_, margin := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, margin.FrozenUSD.Equal(decimal.RequireFromString("10")))
	// 首次下单时保证金那 6 USD 的 reserve 在取消时已释放，兑现时又按 10 USD 重新取了一次。
	var reserve model.AdtySiteWalletLedger
	require.NoError(t, db.Where("site_id = ? AND type = ? AND ref_id = ? AND wallet_kind = ?", site.Id, "gift_reserve", ext.TradeNo, model.AdtySiteWalletMargin).
		Order("id desc").First(&reserve).Error)
	require.True(t, reserve.HoldDelta.Equal(decimal.RequireFromString("10")))
	// 批次跟着余额走：本单赠金批次补齐到全额。
	var batch model.AdtyFundBatch
	require.NoError(t, db.Where("user_id = ? AND source = ? AND kind = ?", customer.Id, ext.TradeNo, "bonus").First(&batch).Error)
	require.EqualValues(t, 5_000_000, batch.Original)
	require.EqualValues(t, 5_000_000, batch.Remaining)
	var credit model.AdtyBalanceLedger
	require.NoError(t, db.Where("user_id = ? AND ref_id = ? AND type = ?", customer.Id, ext.TradeNo, model.AdtyLedgerTypeGiftFulfillment).First(&credit).Error)
	require.EqualValues(t, 5_000_000, credit.DeltaQuota)

	// 重放的扫描不能再发一次：额度、通知、保障流水都不许再动。
	reserves := countGiftLedgers(t, db, site.Id, "gift_reserve", ext.TradeNo, model.AdtySiteWalletMargin)
	n, err = ResumePendingAdtyGiftFulfillments(site.Id)
	require.NoError(t, err)
	require.Zero(t, n)
	require.EqualValues(t, 55_000_000, userQuotaOf(t, db, customer.Id))
	require.EqualValues(t, 1, notified)
	require.EqualValues(t, reserves, countGiftLedgers(t, db, site.Id, "gift_reserve", ext.TradeNo, model.AdtySiteWalletMargin))
}

func TestMarginTopUpShortOfGuaranteeLeavesGiftPending(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site, customer, ext := seedReservedSiteGift(t, db, "gift-fulfil-short", "10")
	_, err := model.CancelAdtyTopUp(ext.TradeNo, customer.Id)
	require.NoError(t, err)
	require.NoError(t, drainSiteGiftWallets(t, db, site.Id, "gift-fulfil-short"))
	_, err = model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)

	// 只补 2 USD：90% 的可取额度 1.8 USD 还是不够 10 USD 赠金。
	_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.RequireFromString("2"), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "gift-fulfil-short-topup",
	})
	require.NoError(t, err)
	setAdtyMarginTargetForTest(t, site.Id, decimal.NewFromInt(2))
	require.NoError(t, TransferAdtyEarningToMargin(site.Id, owner.Id,
		decimal.RequireFromString("2"), "gift-fulfil-short-transfer"))

	require.EqualValues(t, 50_000_000, userQuotaOf(t, db, customer.Id))
	var row model.AdtyGiftFulfillment
	require.NoError(t, db.Where("trade_no = ?", ext.TradeNo).First(&row).Error)
	require.Equal(t, model.AdtyGiftFulfillmentPending, row.Status)
	_, margin := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, margin.FrozenUSD.IsZero())
}

func TestPartialAndSplitRefundsReleaseProportionalSiteGiftGuarantee(t *testing.T) {
	// Subtest A: single $40 refund on $100 principal + $10 site gift ($4 earning + $6 margin).
	t.Run("single_partial_40", func(t *testing.T) {
		db := prepareSiteDB(t)
		_, site, customer, ext := seedReservedSiteGift(t, db, "gift-part-single", "10")
		_, err := model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
			Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
			PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
		}, "")
		require.NoError(t, err)

		res, err := completeManualRefundForTest(t, ext.TradeNo, "partial 40", 1, "gift-part-single-r1", decimal.NewFromInt(40))
		require.NoError(t, err)
		require.EqualValues(t, 2_000_000, res.Refund.BonusDeductedQuota)
		require.EqualValues(t, 33_000_000, userQuotaOf(t, db, customer.Id))

		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			held, err := netGiftHoldTx(tx, site.Id, ext.TradeNo)
			require.NoError(t, err)
			require.True(t, held.Equal(decimal.RequireFromString("6")), "expected $6 remaining guarantee, got %s", held)
			return nil
		}))
		earning, margin := mustSiteGiftWallets(t, db, site.Id)
		require.True(t, earning.FrozenUSD.Add(margin.FrozenUSD).Equal(decimal.RequireFromString("6")))

		totals := earningTotals(t, db, site.Id)
		require.True(t, totals[model.AdtySiteEarningBonusReversal].Equal(usd("4")), "expected bonus_reversal $4, got %s", totals[model.AdtySiteEarningBonusReversal])
	})

	// Subtest B: split $10 + $30 refunds on $100 principal + $10 site gift produce identical cumulative $4 release & reversal.
	t.Run("split_10_and_30", func(t *testing.T) {
		db := prepareSiteDB(t)
		_, site, customer, ext := seedReservedSiteGift(t, db, "gift-part-split", "10")
		_, err := model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
			Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
			PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
		}, "")
		require.NoError(t, err)

		r1, err := completeManualRefundForTest(t, ext.TradeNo, "split 10", 1, "gift-part-split-r1", decimal.NewFromInt(10))
		require.NoError(t, err)
		require.EqualValues(t, 500_000, r1.Refund.BonusDeductedQuota)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			held, err := netGiftHoldTx(tx, site.Id, ext.TradeNo)
			require.NoError(t, err)
			require.True(t, held.Equal(decimal.RequireFromString("9")), "after $10 refund expected $9 held, got %s", held)
			return nil
		}))

		r2, err := completeManualRefundForTest(t, ext.TradeNo, "split 30", 1, "gift-part-split-r2", decimal.NewFromInt(30))
		require.NoError(t, err)
		require.EqualValues(t, 1_500_000, r2.Refund.BonusDeductedQuota)
		require.EqualValues(t, 33_000_000, userQuotaOf(t, db, customer.Id))

		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			held, err := netGiftHoldTx(tx, site.Id, ext.TradeNo)
			require.NoError(t, err)
			require.True(t, held.Equal(decimal.RequireFromString("6")), "after $10+$30 refund expected $6 held, got %s", held)
			return nil
		}))
		totals := earningTotals(t, db, site.Id)
		require.True(t, totals[model.AdtySiteEarningBonusReversal].Equal(usd("4")), "expected cumulative bonus_reversal $4, got %s", totals[model.AdtySiteEarningBonusReversal])
	})
}

func TestPendingSiteGiftPausesDuringActiveRefundAndShrinksOnPartialRefund(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site, customer, ext := seedReservedSiteGift(t, db, "gift-pend-ref", "10")
	_, err := model.CancelAdtyTopUp(ext.TradeNo, customer.Id)
	require.NoError(t, err)
	require.NoError(t, drainSiteGiftWallets(t, db, site.Id, "gift-pend-ref"))
	_, err = model.CompleteAdtyTopUp(ext.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)
	require.EqualValues(t, 50_000_000, userQuotaOf(t, db, customer.Id))

	// Replenish earnings so guarantee IS available, then apply H refund: fulfillment must pause!
	_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.RequireFromString("20"), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "gift-pend-ref-earn20",
	})
	require.NoError(t, err)
	_, err = model.ApplyAdtyTopUpManualRefund(ext.TradeNo, "partial pending", 1, "pend-ref-1", decimal.NewFromInt(40))
	require.NoError(t, err)
	n, err := ResumePendingAdtyGiftFulfillments(site.Id)
	require.NoError(t, err)
	require.Zero(t, n, "pending gift must pause while an H manual refund exists")
	require.EqualValues(t, 50_000_000, userQuotaOf(t, db, customer.Id))

	// Move refund to U (unknown payout): fulfillment must remain paused!
	_, err = model.ApproveAdtyTopUpManualRefund(ext.TradeNo, "pend-ref-1", 1, "", "")
	require.NoError(t, err)
	_, err = model.RecordAdtyTopUpManualRefundPayout(ext.TradeNo, "pend-ref-1", 1, "pend-ref-unk", model.AdtyManualRefundPayout{
		Status: model.AdtyManualRefundPayoutUnknown, Currency: "USDT", Amount: decimal.NewFromInt(40), PaidAt: 10, Voucher: "unk",
	})
	require.NoError(t, err)
	n, err = ResumePendingAdtyGiftFulfillments(site.Id)
	require.NoError(t, err)
	require.Zero(t, n, "pending gift must pause while a U manual refund exists")

	// Drain earnings so guarantee is temporarily short while completing the $40 refund.
	require.NoError(t, drainSiteGiftWallets(t, db, site.Id, "gift-pend-ref-drain2"))
	_, err = model.ReclaimAdtyTopUpManualRefund(ext.TradeNo, "pend-ref-1", 1, "")
	require.NoError(t, err)
	_, err = model.PlanAdtyTopUpManualRefundBonus(ext.TradeNo, "pend-ref-1", 1, "")
	require.NoError(t, err)
	_, err = model.PlanAdtyTopUpManualRefundCommission(ext.TradeNo, "pend-ref-1", 1, "")
	require.NoError(t, err)
	_, err = model.RecordAdtyTopUpManualRefundPayout(ext.TradeNo, "pend-ref-1", 1, "pend-ref-ok", model.AdtyManualRefundPayout{
		Status: model.AdtyManualRefundPayoutSuccess, Currency: "USDT", Amount: decimal.NewFromInt(40), PaidAt: 11, Voucher: "ok",
	})
	require.NoError(t, err)
	_, err = model.CompleteAdtyTopUpManualRefund(ext.TradeNo, "pend-ref-1", 1, "")
	require.NoError(t, err)
	require.EqualValues(t, 30_000_000, userQuotaOf(t, db, customer.Id))

	// Check pending gift shrunk to $6 (3,000,000 quota).
	var fg model.AdtyGiftFulfillment
	require.NoError(t, db.Where("trade_no = ?", ext.TradeNo).First(&fg).Error)
	require.Equal(t, model.AdtyGiftFulfillmentPending, fg.Status)
	require.True(t, fg.BonusUSD.Equal(decimal.NewFromInt(6)), "shrunk BonusUSD=%s", fg.BonusUSD)
	require.EqualValues(t, 3_000_000, fg.BonusQuota)

	// Top up only $6 earning (insufficient for original $10, sufficient for shrunk $6!).
	_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.RequireFromString("6"), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "gift-pend-ref-earn6",
	})
	require.NoError(t, err)
	_ = owner
	n, err = ResumePendingAdtyGiftFulfillments(site.Id)
	require.NoError(t, err)
	require.Equal(t, 1, n, "shrunk pending gift of $6 must fulfill with $6 guarantee")
	require.EqualValues(t, 33_000_000, userQuotaOf(t, db, customer.Id))
	require.NoError(t, db.Where("trade_no = ?", ext.TradeNo).First(&fg).Error)
	require.Equal(t, model.AdtyGiftFulfillmentFulfilled, fg.Status)
}
