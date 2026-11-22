package service

import (
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDailySettlementSheetSumsRecordedFactsAndDoesNotCreditWallets(t *testing.T) {
	db := prepareSiteDB(t)
	t.Cleanup(func() { _ = db.Delete(&model.Option{Key: adtyBusinessTimezoneKey}).Error })
	require.NoError(t, db.Delete(&model.Option{Key: adtyBusinessTimezoneKey}).Error)
	_, site := openActiveSite(t, db, "sheet"+strconv.FormatInt(time.Now().UnixNano()%1_000_000, 10))
	_, other := openActiveSite(t, db, "other"+strconv.FormatInt(time.Now().UnixNano()%1_000_000, 10))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(10), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "sheet-earn-" + strconv.Itoa(site.Id),
	})
	require.NoError(t, err)

	now := time.Date(2026, 3, 15, 0, 30, 0, 0, time.UTC)
	start := time.Date(2026, 3, 14, 0, 0, 0, 0, mustLoc(t, "Asia/Shanghai")).Unix()
	end := time.Date(2026, 3, 15, 0, 0, 0, 0, mustLoc(t, "Asia/Shanghai")).Unix()
	stampSheetFacts(t, db, site, other, start)

	var ledgersBefore int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Count(&ledgersBefore).Error)
	var earning model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)

	count, err := GenerateAdtySiteSettlementSheets(now)
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 1)

	var sheet model.AdtySiteSettlementSheet
	require.NoError(t, db.Where("site_id = ? AND business_day = ?", site.Id, "2026-03-14").First(&sheet).Error)
	require.Equal(t, "Asia/Shanghai", sheet.Timezone)
	require.Equal(t, start, sheet.DayStart)
	require.Equal(t, end, sheet.DayEnd)
	require.Equal(t, "site-settlement:"+strconv.Itoa(site.Id)+":2026-03-14", sheet.BatchId)
	require.Equal(t, start+5000, sheet.EventWatermark)
	require.True(t, sheet.TopupFaceUSD.Equal(decimal.NewFromInt(30)))
	require.True(t, sheet.PaidUSD.Equal(decimal.NewFromInt(20)))
	var paid map[string]string
	require.NoError(t, common.UnmarshalJsonStr(sheet.PaidByCurrency, &paid))
	require.Equal(t, "70", paid["CNY"])
	require.NotContains(t, paid, "USD")
	require.True(t, sheet.ConsumptionUSD.Equal(decimal.NewFromInt(8)))
	require.True(t, sheet.SupplyCostUSD.Equal(decimal.NewFromInt(5)))
	require.True(t, sheet.SpreadUSD.Equal(decimal.NewFromInt(3)))
	require.True(t, sheet.GiftFundingUSD.Equal(decimal.NewFromInt(2)))
	require.True(t, sheet.GiftReleaseUSD.Equal(decimal.RequireFromString("0.5")))
	require.True(t, sheet.GiftReserveUSD.Equal(decimal.RequireFromString("1.5")))
	require.True(t, sheet.TrialExpenseUSD.Equal(decimal.RequireFromString("1.25")))
	require.True(t, sheet.CommissionUSD.Equal(decimal.RequireFromString("1.25")))
	require.True(t, sheet.PendingCoverageUSD.Equal(decimal.RequireFromString("2")))
	require.True(t, sheet.MarginDeltaUSD.Equal(decimal.NewFromInt(6)))
	require.True(t, sheet.EarningsDeltaUSD.Equal(decimal.RequireFromString("-0.5")))
	require.True(t, sheet.EarningsAvailableDeltaUSD.Equal(decimal.RequireFromString("-0.5")))
	require.True(t, sheet.EarningsFrozenDeltaUSD.Equal(decimal.RequireFromString("4.5")), "gift reserve 1.5 plus withdrawal freeze 3, got %s", sheet.EarningsFrozenDeltaUSD)
	require.True(t, sheet.WithdrawalUSD.Equal(decimal.NewFromInt(3)))
	require.True(t, sheet.WithdrawalFeeUSD.Equal(decimal.RequireFromString("0.2")))
	require.True(t, sheet.AdjustmentUSD.Equal(decimal.NewFromInt(-1)))
	require.Equal(t, int64(2), sheet.AnomalyCount)

	var ledgersAfter int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Count(&ledgersAfter).Error)
	require.Equal(t, ledgersBefore, ledgersAfter)
	var earningAfter model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earningAfter).Error)
	require.True(t, earningAfter.Balance.Equal(earning.Balance))

	require.NoError(t, db.Create(&model.AdtySiteEarningDaily{
		SiteId: site.Id, UserId: 3, Day: "2026-03-14", Kind: model.AdtySiteEarningUsage,
		Revenue: decimal.NewFromInt(1), Platform: decimal.Zero, Earning: decimal.NewFromInt(1), Status: model.AdtySiteEarningPending, UpdatedAt: start + 200,
	}).Error)
	again, err := GenerateAdtySiteSettlementSheets(now)
	require.NoError(t, err)
	require.GreaterOrEqual(t, again, 1)
	var refreshed model.AdtySiteSettlementSheet
	require.NoError(t, db.Where("site_id = ? AND business_day = ?", site.Id, "2026-03-14").First(&refreshed).Error)
	require.Equal(t, sheet.BatchId, refreshed.BatchId)
	require.Equal(t, sheet.Id, refreshed.Id)
	require.True(t, refreshed.SpreadUSD.Equal(decimal.NewFromInt(4)))
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Count(&ledgersAfter).Error)
	require.Equal(t, ledgersBefore, ledgersAfter)

	require.NoError(t, SaveAdtyBusinessTimezone("UTC"))
	_, err = GenerateAdtySiteSettlementSheets(now)
	require.NoError(t, err)
	require.NoError(t, db.Where("site_id = ? AND business_day = ?", site.Id, "2026-03-14").First(&refreshed).Error)
	require.Equal(t, start, refreshed.DayStart)
	require.Equal(t, "Asia/Shanghai", refreshed.Timezone)

	require.Error(t, db.Model(&model.AdtySiteSettlementSheet{}).Where("id = ?", sheet.Id).Update("batch_id", "rewritten").Error)
	require.Error(t, db.Delete(&model.AdtySiteSettlementSheet{}, sheet.Id).Error)

	own, err := ListAdtySiteSettlementSheets(site.Id)
	require.NoError(t, err)
	require.Len(t, own, 1)
	require.Equal(t, site.Id, own[0].SiteId)
	all, err := ListAllAdtySiteSettlementSheets()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(all), 2)
}

func TestDailySettlementSheetUsesShanghaiByDefaultAndPreservesExplicitTimezone(t *testing.T) {
	db := prepareSiteDB(t)
	savedLocal := time.Local
	time.Local = mustLoc(t, "America/Los_Angeles")
	t.Cleanup(func() { time.Local = savedLocal })
	t.Cleanup(func() { _ = db.Delete(&model.Option{Key: adtyBusinessTimezoneKey}).Error })
	_, site := openActiveSite(t, db, "tz"+strconv.FormatInt(time.Now().UnixNano()%1_000_000, 10))
	require.Error(t, SaveAdtyBusinessTimezone("Not/AZone"))
	require.NoError(t, SaveAdtyBusinessTimezone(""))

	now := time.Date(2026, 6, 1, 16, 30, 0, 0, time.UTC) // 2026-06-02 00:30 in Shanghai
	inside := time.Date(2026, 6, 1, 15, 59, 0, 0, time.UTC).Unix()
	outside := time.Date(2026, 6, 1, 16, 0, 0, 0, time.UTC).Unix()
	require.NoError(t, db.Create(&model.TopUp{TradeNo: "TZ-IN-" + strconv.Itoa(site.Id), UserId: 1, Status: common.TopUpStatusSuccess, CompleteTime: inside}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{TradeNo: "TZ-IN-" + strconv.Itoa(site.Id), UserId: 1, SiteId: site.Id, FaceUSD: decimal.NewFromInt(4), PaidAmount: decimal.NewFromInt(4), PaidCurrency: "USD"}).Error)
	require.NoError(t, db.Create(&model.TopUp{TradeNo: "TZ-OUT-" + strconv.Itoa(site.Id), UserId: 1, Status: common.TopUpStatusSuccess, CompleteTime: outside}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{TradeNo: "TZ-OUT-" + strconv.Itoa(site.Id), UserId: 1, SiteId: site.Id, FaceUSD: decimal.NewFromInt(9), PaidAmount: decimal.NewFromInt(9), PaidCurrency: "USD"}).Error)

	_, err := GenerateAdtySiteSettlementSheets(now)
	require.NoError(t, err)
	var sheet model.AdtySiteSettlementSheet
	require.NoError(t, db.Where("site_id = ? AND business_day = ?", site.Id, "2026-06-01").First(&sheet).Error)
	require.Equal(t, "Asia/Shanghai", sheet.Timezone)
	require.Equal(t, time.Date(2026, 6, 1, 0, 0, 0, 0, mustLoc(t, "Asia/Shanghai")).Unix(), sheet.DayStart)
	require.True(t, sheet.TopupFaceUSD.Equal(decimal.NewFromInt(4)))

	require.NoError(t, SaveAdtyBusinessTimezone("UTC"))
	_, err = GenerateAdtySiteSettlementSheets(time.Date(2026, 6, 3, 0, 30, 0, 0, time.UTC))
	require.NoError(t, err)
	require.NoError(t, db.Where("site_id = ? AND business_day = ?", site.Id, "2026-06-01").First(&sheet).Error)
	require.Equal(t, "Asia/Shanghai", sheet.Timezone, "a later timezone change does not rewrite the saved sheet")
	var utcSheet model.AdtySiteSettlementSheet
	require.NoError(t, db.Where("site_id = ? AND business_day = ?", site.Id, "2026-06-02").First(&utcSheet).Error)
	require.Equal(t, "UTC", utcSheet.Timezone)
	require.Equal(t, outside, utcSheet.DayStart, "the transition period begins at the prior Shanghai close")
}

func TestSettlementSheetCatchUpFillsMissingDaysWithoutRewritingExisting(t *testing.T) {
	db := prepareSiteDB(t)
	t.Cleanup(func() { _ = db.Delete(&model.Option{Key: adtyBusinessTimezoneKey}).Error })
	require.NoError(t, db.Delete(&model.Option{Key: adtyBusinessTimezoneKey}).Error)
	_, site := openActiveSite(t, db, "gap"+strconv.FormatInt(time.Now().UnixNano()%1_000_000, 10))
	_, err := GenerateAdtySiteSettlementSheets(time.Date(2026, 4, 2, 0, 30, 0, 0, time.UTC))
	require.NoError(t, err)
	var first model.AdtySiteSettlementSheet
	require.NoError(t, db.Where("site_id = ? AND business_day = ?", site.Id, "2026-04-01").First(&first).Error)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Count(&ledgers).Error)

	_, err = CatchUpAdtySiteSettlementSheets(time.Date(2026, 4, 5, 1, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	var kept model.AdtySiteSettlementSheet
	require.NoError(t, db.Where("site_id = ? AND business_day = ?", site.Id, "2026-04-01").First(&kept).Error)
	require.Equal(t, first.BatchId, kept.BatchId)
	require.Equal(t, first.DayStart, kept.DayStart)
	for _, day := range []string{"2026-04-02", "2026-04-03", "2026-04-04"} {
		var row model.AdtySiteSettlementSheet
		require.NoError(t, db.Where("site_id = ? AND business_day = ?", site.Id, day).First(&row).Error)
		require.Equal(t, "Asia/Shanghai", row.Timezone)
		require.Equal(t, "site-settlement:"+strconv.Itoa(site.Id)+":"+day, row.BatchId)
	}
	var after int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Count(&after).Error)
	require.Equal(t, ledgers, after)
}

func stampSheetFacts(t *testing.T, db *gorm.DB, site, other *model.AdtySite, start int64) {
	t.Helper()
	require.NoError(t, db.Create(&[]model.TopUp{
		{TradeNo: "SH-USD-" + strconv.Itoa(site.Id), UserId: 1, Status: common.TopUpStatusSuccess, CompleteTime: start + 10},
		{TradeNo: "SH-CNY-" + strconv.Itoa(site.Id), UserId: 1, Status: common.TopUpStatusSuccess, CompleteTime: start + 20},
		{TradeNo: "SH-PEND-" + strconv.Itoa(site.Id), UserId: 1, Status: common.TopUpStatusPending, CompleteTime: start + 30},
		{TradeNo: "SH-OTHER-" + strconv.Itoa(other.Id), UserId: 1, Status: common.TopUpStatusSuccess, CompleteTime: start + 40},
	}).Error)
	require.NoError(t, db.Create(&[]model.AdtyTopupOrder{
		{TradeNo: "SH-USD-" + strconv.Itoa(site.Id), SiteId: site.Id, UserId: 1, FaceUSD: decimal.NewFromInt(20), PaidAmount: decimal.NewFromInt(20), PaidCurrency: "USD"},
		{TradeNo: "SH-CNY-" + strconv.Itoa(site.Id), SiteId: site.Id, UserId: 1, FaceUSD: decimal.NewFromInt(10), PaidAmount: decimal.NewFromInt(70), PaidCurrency: "CNY"},
		{TradeNo: "SH-PEND-" + strconv.Itoa(site.Id), SiteId: site.Id, UserId: 1, FaceUSD: decimal.NewFromInt(99), PaidAmount: decimal.NewFromInt(99), PaidCurrency: "USD"},
		{TradeNo: "SH-OTHER-" + strconv.Itoa(other.Id), SiteId: other.Id, UserId: 1, FaceUSD: decimal.NewFromInt(500), PaidAmount: decimal.NewFromInt(500), PaidCurrency: "USD"},
	}).Error)
	require.NoError(t, db.Create(&[]model.AdtySiteEarningDaily{
		{SiteId: site.Id, UserId: 1, Day: "2026-03-14", Kind: model.AdtySiteEarningUsage, Revenue: decimal.NewFromInt(8), Platform: decimal.NewFromInt(5), Earning: decimal.NewFromInt(3), Status: model.AdtySiteEarningPending, UpdatedAt: start + 100},
		{SiteId: site.Id, UserId: 1, Day: "2026-03-13", Kind: model.AdtySiteEarningUsage, Revenue: decimal.NewFromInt(100), Platform: decimal.NewFromInt(100), Earning: decimal.NewFromInt(100), Status: model.AdtySiteEarningPending, UpdatedAt: start - 100},
		{SiteId: site.Id, UserId: 1, Day: "2026-03-14+1", Kind: model.AdtySiteEarningSkipped, Earning: decimal.NewFromInt(9), Status: model.AdtySiteEarningPending, Reason: model.AdtySiteRiskSelfUse},
		{SiteId: site.Id, UserId: 2, Day: "2026-03-14", Kind: model.AdtySiteEarningBonus, Earning: decimal.NewFromInt(-99), Status: model.AdtySiteEarningPending},
		{SiteId: site.Id, UserId: 2, Day: "2026-03-14", Kind: model.AdtySiteEarningBonusReversal, Earning: decimal.NewFromInt(99), Status: model.AdtySiteEarningPending},
	}).Error)
	require.NoError(t, db.Create(&model.AdtySiteTrialGrant{SiteId: site.Id, UserId: 1, AmountUSD: decimal.RequireFromString("1.25"), Day: "2026-03-14", IdempotencyKey: "trial-" + strconv.Itoa(site.Id), CreatedAt: start + 100}).Error)
	rows := []model.AdtySiteWalletLedger{
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Delta: decimal.NewFromInt(4), Type: "site_earning", IdempotencyKey: "sheet-earn-row-" + strconv.Itoa(site.Id)},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Delta: decimal.RequireFromString("-1.5"), Type: "gift_reserve", IdempotencyKey: "sheet-reserve-" + strconv.Itoa(site.Id)},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Delta: decimal.NewFromInt(-3), Type: "earning_withdraw_freeze", IdempotencyKey: "sheet-freeze-" + strconv.Itoa(site.Id)},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletOperating, Delta: decimal.NewFromInt(-2), HoldDelta: decimal.NewFromInt(-2), Type: "gift_spend", IdempotencyKey: "sheet-gift-spend-" + strconv.Itoa(site.Id)},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletOperating, HoldDelta: decimal.RequireFromString("-0.5"), Type: "gift_release", IdempotencyKey: "sheet-gift-release-" + strconv.Itoa(site.Id)},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletMargin, Delta: decimal.NewFromInt(6), Type: "margin_cycle_open", IdempotencyKey: "sheet-margin-" + strconv.Itoa(site.Id)},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletOperating, Delta: decimal.NewFromInt(-1), Type: "adjustment", IdempotencyKey: "sheet-adjust-" + strconv.Itoa(site.Id)},
	}
	require.NoError(t, db.Create(&rows).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Model(&model.AdtySiteWalletLedger{}).Where("idempotency_key = ?", "sheet-earn-row-"+strconv.Itoa(site.Id)).Update("created_at", start+10).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Model(&model.AdtySiteWalletLedger{}).Where("idempotency_key = ?", "sheet-reserve-"+strconv.Itoa(site.Id)).Update("created_at", start+20).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Model(&model.AdtySiteWalletLedger{}).Where("idempotency_key = ?", "sheet-freeze-"+strconv.Itoa(site.Id)).Update("created_at", start+30).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Model(&model.AdtySiteWalletLedger{}).Where("idempotency_key = ?", "sheet-gift-spend-"+strconv.Itoa(site.Id)).Update("created_at", start+35).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Model(&model.AdtySiteWalletLedger{}).Where("idempotency_key = ?", "sheet-gift-release-"+strconv.Itoa(site.Id)).Update("created_at", start+36).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Model(&model.AdtySiteWalletLedger{}).Where("idempotency_key = ?", "sheet-margin-"+strconv.Itoa(site.Id)).Update("created_at", start+40).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Model(&model.AdtySiteWalletLedger{}).Where("idempotency_key = ?", "sheet-adjust-"+strconv.Itoa(site.Id)).Update("created_at", start+5000).Error)
	require.NoError(t, db.Create(&[]model.AdtySiteInviteCommission{
		{SiteId: site.Id, TradeNo: "SH-COMM-" + strconv.Itoa(site.Id), AmountUSD: decimal.RequireFromString("1.25"), Status: model.AdtySiteCommissionPendingCoverage},
		{SiteId: site.Id, TradeNo: "SH-OLD-" + strconv.Itoa(site.Id), AmountUSD: decimal.RequireFromString("0.75"), Status: model.AdtySiteCommissionPendingCoverage},
		{SiteId: site.Id, TradeNo: "SH-NEW-" + strconv.Itoa(site.Id), AmountUSD: decimal.NewFromInt(9), Status: model.AdtySiteCommissionPendingCoverage},
	}).Error)
	require.NoError(t, db.Model(&model.AdtySiteInviteCommission{}).Where("trade_no = ?", "SH-COMM-"+strconv.Itoa(site.Id)).Update("created_at", start+50).Error)
	require.NoError(t, db.Model(&model.AdtySiteInviteCommission{}).Where("trade_no = ?", "SH-OLD-"+strconv.Itoa(site.Id)).Update("created_at", start-10).Error)
	require.NoError(t, db.Model(&model.AdtySiteInviteCommission{}).Where("trade_no = ?", "SH-NEW-"+strconv.Itoa(site.Id)).Update("created_at", start+86400).Error)
	require.NoError(t, db.Create(&model.AdtySiteWithdrawal{
		SiteId: site.Id, AmountUSD: decimal.NewFromInt(3), FeeUSD: decimal.RequireFromString("0.2"), NetUSD: decimal.RequireFromString("2.8"),
		Method: "alipay", Status: model.AdtySiteWithdrawPending, IdempotencyKey: "sheet-wd-" + strconv.Itoa(site.Id),
	}).Error)
	require.NoError(t, db.Model(&model.AdtySiteWithdrawal{}).Where("idempotency_key = ?", "sheet-wd-"+strconv.Itoa(site.Id)).Update("created_at", start+60).Error)
	require.NoError(t, db.Create(&[]model.AdtyAnomalyDisposition{
		{Kind: "balance_gap", Ref: strconv.Itoa(site.Id), Status: "open"},
		{Kind: "duplicate_credit", Ref: "SH-USD-" + strconv.Itoa(site.Id), Status: "open"},
		{Kind: "balance_gap", Ref: strconv.Itoa(other.Id), Status: "open"},
		{Kind: "unknown", Ref: "no-site", Status: "open"},
	}).Error)
	require.NoError(t, db.Model(&model.AdtyAnomalyDisposition{}).Where("ref = ?", strconv.Itoa(site.Id)).Update("created_at", start+70).Error)
	require.NoError(t, db.Model(&model.AdtyAnomalyDisposition{}).Where("ref = ?", "SH-USD-"+strconv.Itoa(site.Id)).Update("created_at", start+80).Error)
	require.NoError(t, db.Model(&model.AdtyAnomalyDisposition{}).Where("ref = ?", strconv.Itoa(other.Id)).Update("created_at", start+90).Error)
	require.NoError(t, db.Model(&model.AdtyAnomalyDisposition{}).Where("ref = ?", "no-site").Update("created_at", start+100).Error)
}

func TestSettlementSheetEarningsFrozenDeltaIncludesGiftSpend(t *testing.T) {
	db := prepareSiteDB(t)
	t.Cleanup(func() { _ = db.Delete(&model.Option{Key: adtyBusinessTimezoneKey}).Error })
	require.NoError(t, db.Delete(&model.Option{Key: adtyBusinessTimezoneKey}).Error)
	_, site := openActiveSite(t, db, "sheetspend"+strconv.FormatInt(time.Now().UnixNano()%1_000_000, 10))
	now := time.Date(2026, 7, 21, 0, 30, 0, 0, time.UTC)
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).Unix()
	suffix := strconv.Itoa(site.Id)
	// Grant-time bonus daily row must NOT be reported as realized GiftFundingUSD.
	require.NoError(t, db.Create(&model.AdtySiteEarningDaily{
		SiteId: site.Id, UserId: 1, Day: "2026-07-20", Kind: model.AdtySiteEarningBonus,
		Earning: decimal.NewFromInt(-50), Status: model.AdtySiteEarningPending, UpdatedAt: start + 5,
	}).Error)
	rows := []model.AdtySiteWalletLedger{
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Type: "gift_reserve", HoldDelta: decimal.NewFromInt(4), IdempotencyKey: "sheet-spend-reserve-" + suffix},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Type: "gift_release", HoldDelta: decimal.NewFromInt(-1), IdempotencyKey: "sheet-spend-release-" + suffix},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Type: "gift_spend", Delta: decimal.NewFromInt(-2), HoldDelta: decimal.NewFromInt(-2), IdempotencyKey: "sheet-spend-earn-" + suffix},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletMargin, Type: "gift_spend", Delta: decimal.NewFromInt(-3), HoldDelta: decimal.NewFromInt(-3), IdempotencyKey: "sheet-spend-margin-" + suffix},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Type: "commission_freeze", HoldDelta: decimal.RequireFromString("1.5"), IdempotencyKey: "sheet-comm-freeze-" + suffix},
		{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Type: "commission_release", HoldDelta: decimal.RequireFromString("-0.5"), IdempotencyKey: "sheet-comm-release-" + suffix},
	}
	require.NoError(t, db.Create(&rows).Error)
	for _, key := range []string{"sheet-spend-reserve-", "sheet-spend-release-", "sheet-spend-earn-", "sheet-spend-margin-", "sheet-comm-freeze-", "sheet-comm-release-"} {
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Model(&model.AdtySiteWalletLedger{}).
			Where("idempotency_key = ?", key+suffix).Update("created_at", start+10).Error)
	}

	count, err := GenerateAdtySiteSettlementSheets(now)
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 1)
	var sheet model.AdtySiteSettlementSheet
	require.NoError(t, db.Where("site_id = ? AND business_day = ?", site.Id, "2026-07-20").First(&sheet).Error)
	require.True(t, sheet.GiftReserveUSD.Equal(decimal.NewFromInt(4)))
	require.True(t, sheet.GiftFundingUSD.Equal(decimal.NewFromInt(5)), "GiftFundingUSD must sum actual gift_spend ledgers ($2 + $3), not grant-time bonus rows, got %s", sheet.GiftFundingUSD)
	require.True(t, sheet.GiftReleaseUSD.Equal(decimal.NewFromInt(1)), "GiftReleaseUSD must include gift_release wallet ledgers, got %s", sheet.GiftReleaseUSD)
	require.True(t, sheet.EarningsDeltaUSD.Equal(decimal.NewFromInt(-2)))
	require.True(t, sheet.EarningsFrozenDeltaUSD.Equal(decimal.NewFromInt(2)), "frozen delta = +4 - 1 - 2 + 1.5 - 0.5 = 2, got %s", sheet.EarningsFrozenDeltaUSD)
	require.True(t, sheet.EarningsAvailableDeltaUSD.Equal(decimal.NewFromInt(-4)), "available delta = balance delta (-2) - frozen delta (2) = -4, got %s", sheet.EarningsAvailableDeltaUSD)
	require.True(t, sheet.MarginDeltaUSD.Equal(decimal.NewFromInt(-3)))
}

func TestSettlementSheetBusinessTimeWindowFiltersEarningFactsBySavedWindow(t *testing.T) {
	db := prepareSiteDB(t)
	t.Cleanup(func() { _ = db.Delete(&model.Option{Key: adtyBusinessTimezoneKey}).Error })
	_, site := openActiveSite(t, db, "sheettzearn"+strconv.FormatInt(time.Now().UnixNano()%1_000_000, 10))
	require.NoError(t, SaveAdtyBusinessTimezone("Asia/Shanghai"))

	// Business day 2026-06-01 in Asia/Shanghai (+08:00) spans:
	// start = 2026-05-31 16:00:00 UTC, end = 2026-06-01 16:00:00 UTC.
	inWindowUTC := time.Date(2026, 5, 31, 20, 0, 0, 0, time.UTC).Unix()  // 2026-06-01 04:00 CST (in window, server UTC Day="2026-05-31")
	outWindowUTC := time.Date(2026, 6, 1, 16, 30, 0, 0, time.UTC).Unix() // 2026-06-02 00:30 CST (out of window, server UTC Day="2026-06-01")

	require.NoError(t, db.Create(&[]model.AdtySiteEarningEvent{
		{
			OperationKey: "tz-op-in-" + strconv.Itoa(site.Id), SiteId: site.Id, UserId: 1,
			Day: "2026-05-31", Quota: int(perUnit("125")), Markup: 1.25, CostFactor: 0.8,
			Projected: true, CreatedAt: inWindowUTC,
		},
		{
			OperationKey: "tz-op-out-" + strconv.Itoa(site.Id), SiteId: site.Id, UserId: 1,
			Day: "2026-06-01", Quota: int(perUnit("250")), Markup: 1.25, CostFactor: 0.8,
			Projected: true, CreatedAt: outWindowUTC,
		},
	}).Error)

	now := time.Date(2026, 6, 1, 17, 0, 0, 0, time.UTC) // 2026-06-02 01:00 CST -> generates 2026-06-01 sheet
	_, err := GenerateAdtySiteSettlementSheets(now)
	require.NoError(t, err)

	var sheet model.AdtySiteSettlementSheet
	require.NoError(t, db.Where("site_id = ? AND business_day = ?", site.Id, "2026-06-01").First(&sheet).Error)
	require.Equal(t, "Asia/Shanghai", sheet.Timezone)
	require.True(t, sheet.ConsumptionUSD.Equal(decimal.NewFromInt(125)), "only event inside [DayStart, DayEnd) must be counted, got %s", sheet.ConsumptionUSD)
	require.True(t, sheet.SupplyCostUSD.Equal(decimal.NewFromInt(100)), "got %s", sheet.SupplyCostUSD)
	require.True(t, sheet.SpreadUSD.Equal(decimal.NewFromInt(25)), "got %s", sheet.SpreadUSD)
}

func TestSettlementSheetGiftSourcesAcrossDaysPG(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "sheet-gift-sources")
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).Unix()
	require.NoError(t, db.Create(&model.AdtySiteEarningDaily{SiteId: site.Id, UserId: 1, Day: "2026-07-20", Kind: model.AdtySiteEarningBonus, Earning: decimal.NewFromInt(-10)}).Error)
	require.NoError(t, db.Create(&model.AdtySiteEarningDaily{SiteId: site.Id, UserId: 1, Day: "2026-07-21", Kind: model.AdtySiteEarningBonusReversal, Earning: decimal.NewFromInt(99)}).Error)
	for i, row := range []model.AdtySiteWalletLedger{
		{WalletKind: model.AdtySiteWalletEarnings, Type: "gift_reserve", HoldDelta: decimal.NewFromInt(6), CreatedAt: start + 1},
		{WalletKind: model.AdtySiteWalletMargin, Type: "gift_reserve", HoldDelta: decimal.NewFromInt(4), CreatedAt: start + 2},
		{WalletKind: model.AdtySiteWalletEarnings, Type: "gift_spend", Delta: decimal.NewFromInt(-2), HoldDelta: decimal.NewFromInt(-2), CreatedAt: start + 86401},
		{WalletKind: model.AdtySiteWalletMargin, Type: "gift_spend", Delta: decimal.NewFromInt(-1), HoldDelta: decimal.NewFromInt(-1), CreatedAt: start + 86402},
		{WalletKind: model.AdtySiteWalletEarnings, Type: "gift_release", HoldDelta: decimal.NewFromInt(-4), CreatedAt: start + 86403},
		{WalletKind: model.AdtySiteWalletMargin, Type: "gift_release", HoldDelta: decimal.NewFromInt(-3), CreatedAt: start + 86404},
	} {
		row.SiteId = site.Id
		row.IdempotencyKey = "sheet-source-" + strconv.Itoa(i)
		require.NoError(t, db.Create(&row).Error)
	}
	wallets := snapshotSiteWallets(t, db, site.Id)
	for range 2 {
		d1, err := summarizeAdtySiteSettlement(site.Id, "2026-07-20", start, start+86400)
		require.NoError(t, err)
		require.Equal(t, "10", d1.GiftReserveUSD.String())
		require.Equal(t, "0", d1.GiftFundingUSD.String())
		d2, err := summarizeAdtySiteSettlement(site.Id, "2026-07-21", start+86400, start+172800)
		require.NoError(t, err)
		require.Equal(t, "3", d2.GiftFundingUSD.String())
		require.Equal(t, "7", d2.GiftReleaseUSD.String())
		require.Equal(t, wallets, snapshotSiteWallets(t, db, site.Id))
	}
}

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return loc
}

func TestSettlementSheetAvailableAndFrozenNetChangesPG(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "net-hold-sheet")
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).Unix()
	for i, amounts := range []struct{ kind, delta, hold string }{
		{"gift_reserve", "0", "4"}, {"gift_release", "0", "-1"},
		{"gift_spend", "-2", "-2"}, {"commission_freeze", "0", "3"},
		{"commission_release", "0", "-1"}, {"commission_deduct", "-1", "-1"},
		{"earning_withdraw_freeze", "-5", "0"}, {"earning_withdraw_release", "2", "0"},
		{"earning_withdraw_paid", "0", "0"},
	} {
		require.NoError(t, db.Create(&model.AdtySiteWalletLedger{SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings,
			Type: amounts.kind, Delta: decimal.RequireFromString(amounts.delta), HoldDelta: decimal.RequireFromString(amounts.hold),
			CreatedAt: start + 1, IdempotencyKey: "net-hold-sheet-" + strconv.Itoa(i)}).Error)
	}
	sheet, err := summarizeAdtySiteSettlement(site.Id, "2026-07-20", start, start+86400)
	require.NoError(t, err)
	require.Equal(t, "-6", sheet.EarningsDeltaUSD.String())
	require.Equal(t, "5", sheet.EarningsFrozenDeltaUSD.String())
	require.Equal(t, "-8", sheet.EarningsAvailableDeltaUSD.String(), "net holds change available by delta-hold; legacy withdrawal holds already move delta")
}

func TestSettlementSheetUsesSavedWindowAcrossDSTAndLegacyPG(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "window-facts-sheet")
	loc := mustLoc(t, "America/New_York")
	start := time.Date(2026, 3, 8, 0, 0, 0, 0, loc).Unix()
	end := time.Date(2026, 3, 9, 0, 0, 0, 0, loc).Unix()
	require.EqualValues(t, 23*3600, end-start)
	for i, fact := range []struct {
		at, quota int64
		day       string
	}{
		{start - 1, 500_000_000, "2026-03-08"}, {start, 62_500_000, "2026-03-07"},
		{end - 1, -12_500_000, "2026-03-09"}, {end, 500_000_000, "2026-03-08"},
		{end + 10*86400, 500_000_000, "2026-03-08"},
	} {
		require.NoError(t, db.Create(&model.AdtySiteEarningEvent{OperationKey: "sheet-window-" + strconv.Itoa(i),
			SiteId: site.Id, UserId: 1, Day: fact.day, Quota: int(fact.quota), Markup: 1.25, CostFactor: .8, Projected: true, CreatedAt: fact.at}).Error)
	}
	// The operation slot duplicates durable events; unrelated legacy usage is still included.
	require.NoError(t, db.Create(&[]model.AdtySiteEarningDaily{
		{SiteId: site.Id, UserId: 1, Day: "2026-03-08+op", Kind: model.AdtySiteEarningUsage, Revenue: decimal.NewFromInt(999), UpdatedAt: start + 100},
		{SiteId: site.Id, UserId: 2, Day: "2026-03-07", Kind: model.AdtySiteEarningUsage, Revenue: decimal.NewFromInt(10), Platform: decimal.NewFromInt(8), Earning: decimal.NewFromInt(2), UpdatedAt: start + 100},
		{SiteId: site.Id, UserId: 2, Day: "2026-03-08", Kind: model.AdtySiteEarningRefund, Revenue: decimal.NewFromInt(-5), Platform: decimal.NewFromInt(-4), Earning: decimal.NewFromInt(-1), UpdatedAt: end - 1},
		{SiteId: site.Id, UserId: 3, Day: "2026-03-08", Kind: model.AdtySiteEarningUsage, Revenue: decimal.NewFromInt(888), UpdatedAt: end},
	}).Error)
	require.NoError(t, db.Create(&[]model.AdtySiteTrialGrant{
		{SiteId: site.Id, UserId: 2, Day: "2026-03-07", AmountUSD: decimal.NewFromInt(3), CreatedAt: start, IdempotencyKey: "window-trial-in"},
		{SiteId: site.Id, UserId: 3, Day: "2026-03-08", AmountUSD: decimal.NewFromInt(99), CreatedAt: end, IdempotencyKey: "window-trial-out"},
	}).Error)
	require.NoError(t, writeAdtySiteSettlementSheet(site.Id, "2026-03-08", start, end, "America/New_York", end+100))
	// Refresh must use the original saved 23-hour window even if a new timezone is supplied.
	require.NoError(t, writeAdtySiteSettlementSheet(site.Id, "2026-03-08", start-18000, end-14400, "UTC", end+200))
	var sheet model.AdtySiteSettlementSheet
	require.NoError(t, db.Where("site_id = ? AND business_day = ?", site.Id, "2026-03-08").First(&sheet).Error)
	require.Equal(t, start, sheet.DayStart)
	require.Equal(t, end, sheet.DayEnd)
	require.Equal(t, "America/New_York", sheet.Timezone)
	require.Equal(t, "105", sheet.ConsumptionUSD.String())
	require.Equal(t, "84", sheet.SupplyCostUSD.String())
	require.Equal(t, "21", sheet.SpreadUSD.String())
	require.Equal(t, "3", sheet.TrialExpenseUSD.String())
	require.Equal(t, end-1, sheet.EventWatermark)
}

func TestSettlementSheetLegacyContributionsKeepOriginalOccurrencePG(t *testing.T) {
	db := prepareSiteDB(t)
	logDB, err := model.OpenAdtyTestDatabase()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logDB.Close()) })
	model.DB = db
	require.NoError(t, model.LOG_DB.AutoMigrate(&model.Log{}))
	require.False(t, model.LOG_DB.Migrator().HasTable(&model.AdtySiteEarningFact{}), "financial facts remain in the main database")
	_, site := openActiveSite(t, db, "legacy-time-facts")
	user := siteCustomer(t, db, site, "legacy-time-facts-user")
	start := time.Date(2026, 6, 1, 16, 0, 0, 0, time.UTC).Unix()
	require.NoError(t, db.Create(&model.AdtySiteEarningDaily{SiteId: site.Id, SiteOwnerUserId: site.OwnerUserId,
		UserId: user.Id, Day: "2026-06-01", Kind: model.AdtySiteEarningUsage, Status: model.AdtySiteEarningAvailable,
		Revenue: usd("7"), Platform: usd("5"), Earning: usd("2"), UpdatedAt: start - 2}).Error)
	for _, entry := range []adtySiteEarningEntry{
		{SiteId: site.Id, UserId: user.Id, Kind: model.AdtySiteEarningUsage, Quota: 5_000_000, Revenue: usd("10"), Platform: usd("8"), Earning: usd("2"), OccurredAt: start - 1},
		{SiteId: site.Id, UserId: user.Id, Kind: model.AdtySiteEarningUsage, Quota: 10_000_000, Revenue: usd("20"), Platform: usd("16"), Earning: usd("4"), OccurredAt: start + 1},
		{SiteId: site.Id, UserId: user.Id, Kind: model.AdtySiteEarningRefund, Quota: -2_500_000, Revenue: usd("-5"), Platform: usd("-4"), Earning: usd("-1"), OccurredAt: start + 2},
	} {
		require.NoError(t, upsertAdtySiteEarningTx(db, "2026-06-01", entry))
	}
	for _, window := range []struct {
		from, to                        int64
		day, revenue, platform, earning string
	}{
		{start - 86400, start, "2026-06-01", "17", "13", "4"},
		{start, start + 86400, "2026-06-02", "15", "12", "3"},
	} {
		sheet, err := summarizeAdtySiteSettlement(site.Id, window.day, window.from, window.to)
		require.NoError(t, err)
		require.Equal(t, window.revenue, sheet.ConsumptionUSD.String())
		require.Equal(t, window.platform, sheet.SupplyCostUSD.String())
		require.Equal(t, window.earning, sheet.SpreadUSD.String())
	}
	require.NoError(t, db.AutoMigrate(model.AdtySiteTables()...))
	require.NoError(t, db.AutoMigrate(model.AdtySiteTables()...))
	var facts []model.AdtySiteEarningFact
	require.NoError(t, db.Where("site_id = ?", site.Id).Find(&facts).Error)
	require.Len(t, facts, 4, "repeat migrations retain the historical baseline and each immutable occurrence fact")
}
