package service

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCollectAdtyFundAnomaliesCoversMissingG134Kinds(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "g134-collect")
	customer := siteCustomer(t, db, site, "g134-buyer")

	unbookedTrade := "UNBOOKED-" + strconv.Itoa(site.Id)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: customer.Id, Amount: 10, TradeNo: unbookedTrade,
		PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		Status: common.TopUpStatusSuccess,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: unbookedTrade, UserId: customer.Id, SiteId: site.Id,
		Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
		FaceUSD: decimal.NewFromInt(10), CreditQuota: 5_000_000,
		PayAmount: decimal.NewFromInt(10), PayCurrency: "USDT",
	}).Error)

	giftTrade := "GIFT-SHORT-" + strconv.Itoa(site.Id)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: customer.Id, Amount: 20, TradeNo: giftTrade,
		PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		Status: common.TopUpStatusPending,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: giftTrade, UserId: customer.Id, SiteId: site.Id,
		Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
		FaceUSD: decimal.NewFromInt(20), BonusUSD: decimal.NewFromInt(3),
		CreditQuota: 11_500_000, GiftReserveEarning: decimal.NewFromInt(1),
	}).Error)

	quota := int64(1_000_000)
	k := decimal.RequireFromString("1.25")
	d := decimal.RequireFromString("0.8")
	perUnit := common.QuotaPerUnitDecimal()
	revenue := decimal.NewFromInt(quota).Div(perUnit).Round(8)
	// Historical leftover books used platform = q×d/k, written before ISS-R2-0009.
	legacyPlatform := decimal.NewFromInt(quota).Mul(d).Div(k).Div(perUnit).Round(8)
	legacyEarning := revenue.Sub(legacyPlatform)
	_, officialPlatform, officialEarning := OfficialAdtySupplySplit(quota, k)
	require.False(t, officialPlatform.Equal(legacyPlatform))
	require.False(t, officialEarning.Equal(legacyEarning))
	eventKey := "request:g134-" + strconv.Itoa(site.Id)
	earningKey := "earning-daily:2026-11-14:" + model.AdtySiteEarningUsage + ":" + strconv.Itoa(customer.Id)
	require.NoError(t, db.Create(&model.AdtySiteEarningEvent{
		OperationKey: eventKey, SiteId: site.Id, UserId: customer.Id,
		Quota: int(quota), Markup: 1.25, CostFactor: 0.8, Day: "2026-11-14",
	}).Error)
	require.NoError(t, db.Create(&model.AdtySiteEarningDaily{
		SiteId: site.Id, UserId: customer.Id, Day: "2026-11-14",
		Kind: model.AdtySiteEarningUsage, Quota: quota,
		Revenue: revenue, Platform: legacyPlatform, Earning: legacyEarning,
		Status: model.AdtySiteEarningPending,
	}).Error)

	refundTrade := "REFUND-MIS-" + strconv.Itoa(site.Id)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: customer.Id, Amount: 10, TradeNo: refundTrade,
		PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		Status: common.TopUpStatusSuccess,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: refundTrade, UserId: customer.Id, SiteId: site.Id,
		Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
		FaceUSD: decimal.NewFromInt(10), CreditQuota: 5_000_000,
		PayAmount: decimal.NewFromInt(10), PayCurrency: "USDT",
		RefundQuota: 4_000_000,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyBalanceLedger{
		UserId: customer.Id, DeltaQuota: 5_000_000, Type: model.AdtyLedgerTypeTopUp,
		RefType: "topup", RefId: refundTrade,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyManualRefund{
		RefundId: "RF-H-" + strconv.Itoa(site.Id), TradeNo: refundTrade,
		UserId: customer.Id, SiteId: site.Id, Status: model.AdtyManualRefundStatusApproved,
		RequestUSD: decimal.NewFromInt(8), RequestQuota: 4_000_000,
	}).Error)

	walletsBefore := snapshotSiteWallets(t, db, site.Id)
	siteLedgersBefore, userLedgersBefore := countSiteAndUserLedgers(t, db, site.Id, customer.Id)
	quotaBefore := loadUserQuota(t, customer.Id)

	kinds, err := CollectAdtyFundAnomalies(site.Id)
	require.NoError(t, err)
	require.Contains(t, kinds, AdtyAnomalyOrderSuccessUnbooked)
	require.Contains(t, kinds, AdtyAnomalyGiftGuaranteeShort)
	require.Contains(t, kinds, AdtyAnomalyEarningSupplyMismatch)
	require.Contains(t, kinds, AdtyAnomalyRefundAmountMismatch)

	rows, err := ListAdtyAnomalyDispositions(site.Id)
	require.NoError(t, err)
	byKind := dispositionsByKindRef(rows)
	unbooked := byKind[AdtyAnomalyOrderSuccessUnbooked+"|"+unbookedTrade]
	require.Equal(t, "open", unbooked.Status)
	require.Equal(t, site.Id, unbooked.SiteId)
	require.Equal(t, customer.Id, unbooked.AccountId)
	require.Equal(t, unbookedTrade, unbooked.OrderRef)
	require.Equal(t, "10", unbooked.Difference)
	require.Contains(t, unbooked.Evidence, "expected_credit_quota")

	gift := byKind[AdtyAnomalyGiftGuaranteeShort+"|"+giftTrade]
	require.Equal(t, site.Id, gift.SiteId)
	require.Equal(t, customer.Id, gift.AccountId)
	require.Equal(t, giftTrade, gift.OrderRef)
	require.Equal(t, "2", gift.Difference)
	require.Contains(t, gift.Evidence, "required_usd")

	earning := byKind[AdtyAnomalyEarningSupplyMismatch+"|"+earningKey]
	require.Equal(t, site.Id, earning.SiteId)
	require.Equal(t, customer.Id, earning.AccountId)
	require.Equal(t, officialEarning.Sub(legacyEarning).Abs().String(), earning.Difference)
	require.Contains(t, earning.Evidence, "official_platform")

	refund := byKind[AdtyAnomalyRefundAmountMismatch+"|"+refundTrade]
	require.Equal(t, site.Id, refund.SiteId)
	require.Equal(t, customer.Id, refund.AccountId)
	require.Equal(t, refundTrade, refund.OrderRef)
	require.Equal(t, "6", refund.Difference)
	require.Contains(t, refund.Evidence, "occupied")

	kindsAgain, err := CollectAdtyFundAnomalies(site.Id)
	require.NoError(t, err)
	require.Contains(t, kindsAgain, AdtyAnomalyOrderSuccessUnbooked)
	require.Contains(t, kindsAgain, AdtyAnomalyGiftGuaranteeShort)
	require.Contains(t, kindsAgain, AdtyAnomalyEarningSupplyMismatch)
	require.Contains(t, kindsAgain, AdtyAnomalyRefundAmountMismatch)

	var openCount int64
	require.NoError(t, db.Model(&model.AdtyAnomalyDisposition{}).
		Where("site_id = ? AND status = ? AND kind IN ?", site.Id, "open", []string{
			AdtyAnomalyOrderSuccessUnbooked, AdtyAnomalyGiftGuaranteeShort,
			AdtyAnomalyEarningSupplyMismatch, AdtyAnomalyRefundAmountMismatch,
		}).Count(&openCount).Error)
	require.EqualValues(t, 4, openCount)

	require.Equal(t, walletsBefore, snapshotSiteWallets(t, db, site.Id))
	siteLedgersAfter, userLedgersAfter := countSiteAndUserLedgers(t, db, site.Id, customer.Id)
	require.Equal(t, siteLedgersBefore, siteLedgersAfter)
	require.Equal(t, userLedgersBefore, userLedgersAfter)
	require.Equal(t, quotaBefore, loadUserQuota(t, customer.Id))
}

func TestCollectAdtyFundAnomaliesKeepsExistingKindsIdempotent(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "g134-existing")
	require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.RequireFromString("100"), "g134-cycle"))
	require.NoError(t, db.Model(&model.AdtySiteWallet{}).Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).
		Update("cycle_used_usd", "91").Error)
	require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{TradeNo: "dup-credit-g134", SiteId: site.Id, UserId: 1, Provider: "epay", Source: "callback", Outcome: "credited"}).Error)
	require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{TradeNo: "dup-credit-g134", SiteId: site.Id, UserId: 1, Provider: "epay", Source: "callback", Outcome: "credited"}).Error)
	require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{TradeNo: "dup-refund-g134", SiteId: site.Id, UserId: 1, Provider: "epay", Source: "callback", Outcome: "received", EventType: "refund.succeeded"}).Error)
	require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{TradeNo: "dup-refund-g134", SiteId: site.Id, UserId: 1, Provider: "epay", Source: "callback", Outcome: "received", EventType: "refund.succeeded"}).Error)

	walletsBefore := snapshotSiteWallets(t, db, site.Id)
	kinds, err := CollectAdtyFundAnomalies(site.Id)
	require.NoError(t, err)
	require.Contains(t, kinds, AdtyAnomalyMargin)
	require.Contains(t, kinds, AdtyAnomalyDuplicateCredit)
	require.Contains(t, kinds, AdtyAnomalyDuplicateRefund)

	rows, err := ListAdtyAnomalyDispositions(site.Id)
	require.NoError(t, err)
	byKind := dispositionsByKindRef(rows)
	margin := byKind[AdtyAnomalyMargin+"|"+strconv.Itoa(site.Id)]
	require.Equal(t, site.Id, margin.SiteId)
	require.NotEmpty(t, margin.Difference)
	require.Contains(t, margin.Evidence, "cycle_overage_usd")
	require.Contains(t, byKind[AdtyAnomalyDuplicateCredit+"|"+strconv.Itoa(site.Id)].Evidence, "dup-credit-g134")

	_, err = CollectAdtyFundAnomalies(site.Id)
	require.NoError(t, err)
	var openCount int64
	require.NoError(t, db.Model(&model.AdtyAnomalyDisposition{}).
		Where("site_id = ? AND status = ? AND kind IN ?", site.Id, "open", []string{
			AdtyAnomalyMargin, AdtyAnomalyDuplicateCredit, AdtyAnomalyDuplicateRefund,
		}).Count(&openCount).Error)
	require.EqualValues(t, 3, openCount)
	require.Equal(t, walletsBefore, snapshotSiteWallets(t, db, site.Id))
}

func TestCollectAdtyFundAnomaliesFlagsPayoutMismatchWithoutMutatingLedgers(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "g134-payout")
	customer := siteCustomer(t, db, site, "g134-payout-buyer")
	trade := "REFUND-PAY-" + strconv.Itoa(site.Id)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: customer.Id, Amount: 10, TradeNo: trade,
		PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		Status: common.TopUpStatusSuccess,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: trade, UserId: customer.Id, SiteId: site.Id,
		Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
		FaceUSD: decimal.NewFromInt(10), CreditQuota: 5_000_000,
		PayAmount: decimal.NewFromInt(10), PayCurrency: "USDT", RefundQuota: 0,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyBalanceLedger{
		UserId: customer.Id, DeltaQuota: 5_000_000, Type: model.AdtyLedgerTypeTopUp,
		RefType: "topup", RefId: trade,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyManualRefund{
		RefundId: "RF-PAY-" + strconv.Itoa(site.Id), TradeNo: trade,
		UserId: customer.Id, SiteId: site.Id, Status: model.AdtyManualRefundStatusApproved,
		RequestUSD: decimal.NewFromInt(4), RequestQuota: 2_000_000,
		ForeignReference: decimal.NewFromInt(4), PayoutStatus: model.AdtyManualRefundPayoutSuccess,
		PayoutAmount: decimal.NewFromInt(1),
	}).Error)

	siteLedgersBefore, userLedgersBefore := countSiteAndUserLedgers(t, db, site.Id, customer.Id)
	kinds, err := CollectAdtyFundAnomalies(site.Id)
	require.NoError(t, err)
	require.Contains(t, kinds, AdtyAnomalyRefundAmountMismatch)
	rows, err := ListAdtyAnomalyDispositions(site.Id)
	require.NoError(t, err)
	item := dispositionsByKindRef(rows)[AdtyAnomalyRefundAmountMismatch+"|"+trade]
	require.Equal(t, "3", item.Difference)
	require.Contains(t, item.Evidence, "paid-out amount")
	siteLedgersAfter, userLedgersAfter := countSiteAndUserLedgers(t, db, site.Id, customer.Id)
	require.Equal(t, siteLedgersBefore, siteLedgersAfter)
	require.Equal(t, userLedgersBefore, userLedgersAfter)
}

func TestLoadGiftHoldByTradeIncludesGiftSpend(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "gift-hold-spend")
	trade := "GIFT-HOLD-" + strconv.Itoa(site.Id)
	require.NoError(t, db.Create(&[]model.AdtySiteWalletLedger{
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Type: "gift_reserve", RefType: "topup", RefId: trade, HoldDelta: decimal.NewFromInt(4), IdempotencyKey: "hold-reserve-earn-" + trade},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletMargin, Type: "gift_reserve", RefType: "topup", RefId: trade, HoldDelta: decimal.NewFromInt(6), IdempotencyKey: "hold-reserve-margin-" + trade},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Type: "gift_spend", RefType: "topup", RefId: trade, Delta: decimal.NewFromInt(-2), HoldDelta: decimal.NewFromInt(-2), IdempotencyKey: "hold-spend-earn-" + trade},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletMargin, Type: "gift_spend", RefType: "topup", RefId: trade, Delta: decimal.NewFromInt(-3), HoldDelta: decimal.NewFromInt(-3), IdempotencyKey: "hold-spend-margin-" + trade},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Type: "gift_release", RefType: "topup", RefId: trade, HoldDelta: decimal.NewFromInt(-1), IdempotencyKey: "hold-release-earn-" + trade},
	}).Error)

	holds, counts, err := loadGiftHoldByTrade(site.Id, []string{trade})
	require.NoError(t, err)
	require.EqualValues(t, 5, counts[trade])
	require.True(t, holds[trade].Equal(decimal.NewFromInt(4)), "net hold must be reserve+release+spend")
}

func snapshotSiteWallets(t *testing.T, db *gorm.DB, siteID int) []model.AdtySiteWallet {
	t.Helper()
	var wallets []model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ?", siteID).Order("id").Find(&wallets).Error)
	return wallets
}

func countSiteAndUserLedgers(t *testing.T, db *gorm.DB, siteID, userID int) (siteLedgers, userLedgers int64) {
	t.Helper()
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ?", siteID).Count(&siteLedgers).Error)
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Where("user_id = ?", userID).Count(&userLedgers).Error)
	return siteLedgers, userLedgers
}

func loadUserQuota(t *testing.T, userID int) int {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.Select("quota").First(&user, userID).Error)
	return user.Quota
}

func dispositionsByKindRef(rows []model.AdtyAnomalyDisposition) map[string]model.AdtyAnomalyDisposition {
	out := map[string]model.AdtyAnomalyDisposition{}
	for _, row := range rows {
		out[row.Kind+"|"+row.Ref] = row
	}
	return out
}

func TestOperatingWalletReconciliationSeparatesPrincipalFromGift(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "g134-principal-gift")
	customer := siteCustomer(t, db, site, "g134-pg-buyer")

	// Customer recharged $100 principal (50,000,000 quota) + $10 gift (5,000,000 quota).
	// Operating wallet holds $100 (principal only); gift is backed separately.
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", customer.Id).
		Updates(map[string]any{"quota": 55_000_000, "quota_pending": 0}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.CreateAdtyFundBatchesWithSiteTx(tx, customer.Id, model.AdtyFundBatchSiteID(site.Id), "TOPUP-PG-1", 50_000_000, 5_000_000)
	}))
	var opWallet model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletOperating).First(&opWallet).Error)
	require.NoError(t, db.Model(&opWallet).Update("balance", decimal.NewFromInt(100)).Error)
	require.NoError(t, db.Create(&model.AdtySiteWalletLedger{
		SiteId: site.Id, WalletId: opWallet.Id, WalletKind: model.AdtySiteWalletOperating,
		Type: "principal_credit", RefType: "topup", RefId: "TOPUP-PG-1",
		Delta: decimal.NewFromInt(100), BalanceBefore: decimal.Zero, BalanceAfter: decimal.NewFromInt(100),
		IdempotencyKey: "op-credit-pg-1-" + strconv.Itoa(site.Id),
	}).Error)

	items, err := detectExistingAdtyFundAnomalies(site.Id)
	require.NoError(t, err)
	for _, item := range items {
		require.NotEqual(t, AdtyAnomalyBalanceLedgerGap, item.Kind, "unconsumed gift must not be counted as operating principal liability")
	}

	// Simulate $20 in-flight principal spend in quota_pending (-10,000,000) with matching $80 operating wallet.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.ConsumeAdtyFundBatchesQuietTx(tx, customer.Id, 10_000_000, 55_000_000)
	}))
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", customer.Id).
		Update("quota_pending", -10_000_000).Error)
	require.NoError(t, db.Model(&opWallet).Update("balance", decimal.NewFromInt(80)).Error)
	require.NoError(t, db.Create(&model.AdtySiteWalletLedger{
		SiteId: site.Id, WalletId: opWallet.Id, WalletKind: model.AdtySiteWalletOperating,
		Type: "principal_consume", RefType: "consume", RefId: "op-1",
		Delta: decimal.NewFromInt(-20), BalanceBefore: decimal.NewFromInt(100), BalanceAfter: decimal.NewFromInt(80),
		IdempotencyKey: "op-consume-pg-1-" + strconv.Itoa(site.Id),
	}).Error)

	items, err = detectExistingAdtyFundAnomalies(site.Id)
	require.NoError(t, err)
	for _, item := range items {
		require.NotEqual(t, AdtyAnomalyBalanceLedgerGap, item.Kind, "pending principal spend must reduce principal before gift")
	}
}

func TestGiftGuaranteeAnomaliesTrackCurrentRemainingLiability(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "g134-gift-current")
	customer := siteCustomer(t, db, site, "g134-gift-buyer")

	// 1. Credited order with $3 original bonus (1,500,000 quota): $1 consumed, $1 reclaimed on partial refund,
	// leaving $1 (500,000 quota) remaining and $1 net hold -> no anomaly.
	tradeActive := "GIFT-ACTIVE-" + strconv.Itoa(site.Id)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: customer.Id, Amount: 20, TradeNo: tradeActive,
		PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		Status: common.TopUpStatusSuccess,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: tradeActive, UserId: customer.Id, SiteId: site.Id,
		Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
		FaceUSD: decimal.NewFromInt(20), BonusUSD: decimal.NewFromInt(3),
		CreditQuota: 11_500_000, GiftReserveEarning: decimal.NewFromInt(3),
	}).Error)
	require.NoError(t, db.Create(&model.AdtyFundBatch{
		UserId: customer.Id, SiteId: model.AdtyFundBatchSiteID(site.Id),
		Kind: model.AdtyFundBatchKindBonus, Source: tradeActive,
		Original: 1_500_000, Remaining: 500_000,
	}).Error)
	require.NoError(t, db.Create(&[]model.AdtySiteWalletLedger{
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Type: "gift_reserve", RefType: "topup", RefId: tradeActive, HoldDelta: decimal.NewFromInt(3), IdempotencyKey: "gr-" + tradeActive},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Type: "gift_spend", RefType: "topup", RefId: tradeActive, Delta: decimal.NewFromInt(-1), HoldDelta: decimal.NewFromInt(-1), IdempotencyKey: "gs-" + tradeActive},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Type: "gift_release", RefType: "topup", RefId: tradeActive, HoldDelta: decimal.NewFromInt(-1), IdempotencyKey: "grel-" + tradeActive},
	}).Error)

	// 2. Fully refunded order with zero remaining bonus and zero hold -> no anomaly.
	tradeRefunded := "GIFT-REFUNDED-" + strconv.Itoa(site.Id)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: customer.Id, Amount: 20, TradeNo: tradeRefunded,
		PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		Status: model.TopUpStatusRefunded,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: tradeRefunded, UserId: customer.Id, SiteId: site.Id,
		Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
		FaceUSD: decimal.NewFromInt(20), BonusUSD: decimal.NewFromInt(3),
		CreditQuota: 11_500_000, RefundQuota: 10_000_000,
		GiftReserveEarning: decimal.NewFromInt(3),
	}).Error)
	require.NoError(t, db.Create(&[]model.AdtySiteWalletLedger{
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Type: "gift_reserve", RefType: "topup", RefId: tradeRefunded, HoldDelta: decimal.NewFromInt(3), IdempotencyKey: "gr-" + tradeRefunded},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Type: "gift_release", RefType: "topup", RefId: tradeRefunded, HoldDelta: decimal.NewFromInt(-3), IdempotencyKey: "grel-" + tradeRefunded},
	}).Error)

	// 3. Order with $2 remaining bonus lot (1,000,000 quota) but only $0.50 net hold -> flagged with Difference = 1.5.
	tradeShort := "GIFT-REM-SHORT-" + strconv.Itoa(site.Id)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: customer.Id, Amount: 20, TradeNo: tradeShort,
		PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		Status: common.TopUpStatusSuccess,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: tradeShort, UserId: customer.Id, SiteId: site.Id,
		Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
		FaceUSD: decimal.NewFromInt(20), BonusUSD: decimal.NewFromInt(3),
		CreditQuota: 11_500_000, GiftReserveEarning: decimal.NewFromInt(3),
	}).Error)
	require.NoError(t, db.Create(&model.AdtyFundBatch{
		UserId: customer.Id, SiteId: model.AdtyFundBatchSiteID(site.Id),
		Kind: model.AdtyFundBatchKindBonus, Source: tradeShort,
		Original: 1_500_000, Remaining: 1_000_000,
	}).Error)
	require.NoError(t, db.Create(&[]model.AdtySiteWalletLedger{
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Type: "gift_reserve", RefType: "topup", RefId: tradeShort, HoldDelta: decimal.RequireFromString("0.5"), IdempotencyKey: "gr-" + tradeShort},
	}).Error)

	gifts, err := detectInsufficientGiftGuarantees(site.Id)
	require.NoError(t, err)
	require.Len(t, gifts, 1)
	require.Equal(t, tradeShort, gifts[0].Ref)
	require.Equal(t, "1.5", gifts[0].Difference)
}

func TestEarningSupplyMismatchReconcilesProjectedEventsAndOpSlots(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "g134-projected-earning")
	customer := siteCustomer(t, db, site, "g134-proj-buyer")

	quota := int64(1_000_000)
	k := decimal.RequireFromString("1.25")
	rev, officialPlatform, officialEarning := OfficialAdtySupplySplit(quota, k)

	// 1. Projected event on 2026-11-15 with matching 2026-11-15+op daily row -> no mismatch.
	require.NoError(t, db.Create(&model.AdtySiteEarningEvent{
		OperationKey: "op-ok-" + strconv.Itoa(site.Id),
		SiteId:       site.Id, UserId: customer.Id,
		Quota: int(quota), Markup: 1.25, CostFactor: 0.8,
		Day: "2026-11-15", Projected: true,
	}).Error)
	require.NoError(t, db.Create(&model.AdtySiteEarningDaily{
		SiteId: site.Id, UserId: customer.Id, Day: "2026-11-15+op",
		Kind: model.AdtySiteEarningUsage, Quota: quota,
		Revenue: rev, Platform: officialPlatform, Earning: officialEarning,
		Status: model.AdtySiteEarningPending,
	}).Error)

	// 2. Projected event on 2026-11-16 with tampered 2026-11-16+op daily row -> must be detected!
	require.NoError(t, db.Create(&model.AdtySiteEarningEvent{
		OperationKey: "op-bad-" + strconv.Itoa(site.Id),
		SiteId:       site.Id, UserId: customer.Id,
		Quota: int(quota), Markup: 1.25, CostFactor: 0.8,
		Day: "2026-11-16", Projected: true,
	}).Error)
	require.NoError(t, db.Create(&model.AdtySiteEarningDaily{
		SiteId: site.Id, UserId: customer.Id, Day: "2026-11-16+op",
		Kind: model.AdtySiteEarningUsage, Quota: quota,
		Revenue: rev, Platform: officialPlatform.Sub(decimal.RequireFromString("0.2")),
		Earning: officialEarning.Add(decimal.RequireFromString("0.2")),
		Status:  model.AdtySiteEarningPending,
	}).Error)

	// 3. Projected event on 2026-11-17 whose daily row is missing -> must be detected!
	require.NoError(t, db.Create(&model.AdtySiteEarningEvent{
		OperationKey: "op-missing-" + strconv.Itoa(site.Id),
		SiteId:       site.Id, UserId: customer.Id,
		Quota: int(quota), Markup: 1.25, CostFactor: 0.8,
		Day: "2026-11-17", Projected: true,
	}).Error)

	mismatches, err := detectEarningSupplyMismatches(site.Id)
	require.NoError(t, err)
	refs := map[string]adtyFundAnomalyItem{}
	for _, m := range mismatches {
		refs[m.Ref] = m
	}
	require.NotContains(t, refs, "earning-daily:2026-11-15:usage:"+strconv.Itoa(customer.Id))
	require.Contains(t, refs, "earning-daily:2026-11-16:usage:"+strconv.Itoa(customer.Id))
	require.Equal(t, "0.2", refs["earning-daily:2026-11-16:usage:"+strconv.Itoa(customer.Id)].Difference)
	require.Contains(t, refs, "earning-daily:2026-11-17:usage:"+strconv.Itoa(customer.Id))
}

func TestOperatingReconciliationStoredLotsAndReservationsPG(t *testing.T) {
	for _, tc := range []struct {
		name                                                                       string
		principal, gift, logical, heldPrincipal, heldGift, wantPrincipal, wantGift int64
	}{
		{"principal-and-gift", 100, 10, 110, 0, 0, 100, 10},
		{"principal-already-consumed", 20, 10, 30, 0, 0, 20, 10},
		{"held-principal-still-liability", 100, 10, 90, 20, 0, 100, 10},
		{"held-gift-still-liability", 0, 10, 5, 0, 5, 0, 10},
		{"trial-gift-only", 0, 10, 10, 0, 0, 0, 10},
		{"lot-gap-is-not-principal", 90, 10, 110, 0, 0, 90, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := prepareSiteDB(t)
			_, site := openActiveSite(t, db, "principal-matrix")
			user := siteCustomer(t, db, site, "principal-matrix-customer")
			require.NoError(t, db.Model(user).Update("quota", tc.logical*500_000).Error)
			for _, lot := range []struct {
				kind         string
				amount, held int64
			}{
				{"principal", tc.principal, tc.heldPrincipal}, {"bonus", tc.gift, tc.heldGift},
			} {
				if lot.amount == 0 {
					continue
				}
				b := model.AdtyFundBatch{UserId: user.Id, SiteId: model.AdtyFundBatchSiteID(site.Id), Kind: lot.kind, Source: "matrix", Original: lot.amount * 500_000, Remaining: lot.amount * 500_000}
				require.NoError(t, db.Create(&b).Error)
				if lot.held > 0 {
					require.NoError(t, db.Create(&model.AdtyFundAllocation{UserId: user.Id, BatchId: b.Id, OperationId: "matrix-hold", Kind: lot.kind, Held: lot.held * 500_000}).Error)
				}
			}
			var wallet model.AdtySiteWallet
			require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletOperating).First(&wallet).Error)
			require.NoError(t, db.Model(&wallet).Update("balance", decimal.NewFromInt(tc.wantPrincipal)).Error)
			require.NoError(t, db.Create(&model.AdtySiteWalletLedger{SiteId: site.Id, WalletId: wallet.Id, WalletKind: wallet.Kind, Delta: decimal.NewFromInt(tc.wantPrincipal), IdempotencyKey: "matrix-wallet"}).Error)
			items, err := detectExistingAdtyFundAnomalies(site.Id)
			require.NoError(t, err)
			gap := false
			for _, item := range items {
				if item.Kind == AdtyAnomalyBalanceLedgerGap {
					gap = true
				}
			}
			require.Equal(t, tc.name == "lot-gap-is-not-principal", gap)
		})
	}
}

func TestEarningReconciliationKeepsKindsAndFrozenTeamSupplyPG(t *testing.T) {
	db := prepareSiteDB(t)
	require.NoError(t, db.AutoMigrate(&model.BillingOperation{}))
	_, site := openActiveSite(t, db, "earning-matrix")
	user := siteCustomer(t, db, site, "earning-matrix-customer")
	snapshot, err := model.EncodeRequestPriceSnapshot(&model.RequestPriceSnapshot{
		SchemaVersion: model.RequestPriceSnapshotVersion, ModelBasePrice: "1", BillingGroup: "default",
		GroupRatio: "1", SiteCostFactor: "1", SiteMarkupRatio: "0.5", BilledGroupRatio: "1.2",
		TeamApplied: true, TeamPayRatio: "0.8", SupplyPriceFloor: "1", FinalPrice: "1.2",
		Currency: model.RequestPriceCurrencyUSD, BillingUnit: model.BillingUnitPerCall,
		AcceptedAt: 1_794_787_200, RuleVersion: model.RequestPriceSnapshotVersion,
		SupplyCost: "1", SellPrice: "1.5",
	})
	require.NoError(t, err)
	require.NotNil(t, model.DecodeRequestPriceSnapshot(snapshot), "fixture must exercise the frozen snapshot")
	key := "reconcile:frozen-team"
	require.NoError(t, db.Create(&model.BillingOperation{OperationKey: key, UserId: user.Id, SiteId: site.Id, State: model.BillingSettled, PriceSnapshot: snapshot}).Error)
	require.NoError(t, db.Create(&[]model.AdtySiteEarningEvent{
		{OperationKey: key, UserId: user.Id, SiteId: site.Id, Day: "2026-11-16", Quota: 60_000_000, Markup: 1.5, CostFactor: 1, Projected: true},
		{OperationKey: "reconcile:refund", UserId: user.Id, SiteId: site.Id, Day: "2026-11-16", Quota: -12_500_000, Markup: 1.25, CostFactor: 1, Projected: true},
	}).Error)
	require.NoError(t, db.Create(&[]model.AdtySiteEarningDaily{
		{SiteId: site.Id, UserId: user.Id, Day: "2026-11-16+op", Kind: model.AdtySiteEarningUsage, Quota: 60_000_000, Revenue: decimal.NewFromInt(120), Platform: decimal.NewFromInt(100), Earning: decimal.NewFromInt(20), Status: model.AdtySiteEarningAvailable},
		{SiteId: site.Id, UserId: user.Id, Day: "2026-11-16+1", Kind: model.AdtySiteEarningRefund, Quota: -12_500_000, Revenue: decimal.NewFromInt(-25), Platform: decimal.NewFromInt(-20), Earning: decimal.NewFromInt(-5), Status: model.AdtySiteEarningAvailable},
	}).Error)
	items, err := detectEarningSupplyMismatches(site.Id)
	require.NoError(t, err)
	require.Empty(t, items)
	require.NoError(t, db.Where("operation_key = ?", "reconcile:refund").Delete(&model.AdtySiteEarningEvent{}).Error)
	items, err = detectEarningSupplyMismatches(site.Id)
	require.NoError(t, err)
	require.Len(t, items, 1, "one kind cannot mask a missing source in another kind")
	require.Contains(t, items[0].Ref, ":refund:")
}
