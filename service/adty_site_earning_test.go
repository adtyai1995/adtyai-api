package service

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/common/logsinktest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 站长收益：记账、加赠扣减、试用额度、风控、结算（任务 11.5）。

func usd(value string) decimal.Decimal { return decimal.RequireFromString(value) }

func perUnit(amount string) int64 {
	return usd(amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart()
}

func TestConfirmedSiteEarningSettlementIsImmediateAndAtomic(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "confirmed-atomic")
	customer := siteCustomer(t, db, site, "confirmed-atomic-customer")
	require.NoError(t, db.Model(customer).Update("quota", perUnit("300")).Error)
	key := "confirmed:immediate"
	require.NoError(t, model.QueueDirectBillingDelta(model.BillingOperation{OperationKey: key, UserId: customer.Id, SiteId: site.Id, SiteMarkup: 1.25, SiteCostFactor: 0.8}, int(perUnit("125"))))
	fail := true
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:confirmed-earning-failure", func(tx *gorm.DB) {
		if fail && tx.Statement.Table == "adty_site_wallet_ledgers" {
			tx.AddError(fmt.Errorf("forced earning ledger failure"))
		}
	}))
	t.Cleanup(func() { db.Callback().Create().Remove("test:confirmed-earning-failure") })
	_, _, err := model.ApplyBillingSettlement(key)
	assert.Error(t, err, "wallet posting failure must roll back the original charge")
	assert.EqualValues(t, perUnit("300"), getUserQuota(t, customer.Id))
	fail = false
	_, _, err = model.ApplyBillingSettlement(key)
	require.NoError(t, err)
	assert.Equal(t, "25", siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).String())
	assert.EqualValues(t, perUnit("175"), getUserQuota(t, customer.Id))
	for range 2 {
		_, _, err = model.ApplyBillingSettlement(key)
		require.NoError(t, err)
		require.NoError(t, projectAdtySiteEarningEvent(context.Background(), key))
		_, err = RunAdtySiteSettlementOnce(time.Now().AddDate(0, 0, 30))
		require.NoError(t, err)
	}
	assert.Equal(t, "25", siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).String(), "replay and daily summary cannot credit twice")
	var count int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ? AND type = ?", site.Id, "site_earning_settlement").Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestRiskMetadataPreservesConfirmedSiteEarning(t *testing.T) {
	for _, flag := range []string{model.AdtySiteRiskSelfUse, model.AdtySiteRiskMigration} {
		t.Run(flag, func(t *testing.T) {
			db := prepareSiteDB(t)
			_, site := openActiveSite(t, db, "risk-meta")
			customer := siteCustomer(t, db, site, "risk-meta-customer")
			require.NoError(t, db.Model(customer).Update("quota", perUnit("300")).Error)
			require.NoError(t, db.Model(&model.AdtySiteCustomer{}).Where("user_id = ?", customer.Id).Update("risk_flag", flag).Error)
			key := "risk:confirmed"
			require.NoError(t, model.QueueDirectBillingDelta(model.BillingOperation{OperationKey: key, UserId: customer.Id, SiteId: site.Id, SiteMarkup: 1.25, SiteCostFactor: 0.8}, int(perUnit("125"))))
			_, _, err := model.ApplyBillingSettlement(key)
			require.NoError(t, err)
			require.NoError(t, projectAdtySiteEarningEvent(context.Background(), key))
			assert.Equal(t, "25", siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).String())
			assert.Equal(t, "25", earningTotals(t, db, site.Id)[model.AdtySiteEarningUsage].String())
			var event model.AdtySiteEarningEvent
			require.NoError(t, db.Where("operation_key = ?", key).First(&event).Error)
			assert.Equal(t, flag, event.RiskFlag, "risk remains available for defensive review")
			for _, convert := range []bool{false, true} {
				require.NoError(t, ReviewAdtySiteCustomer(customer.Id, 1, convert))
				assert.Equal(t, "25", siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).String(), "risk review cannot create another credit")
			}
		})
	}
}

func TestConcurrentIdenticalSiteEarningsKeepBusinessIdentity(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "concurrent-earning")
	customer := siteCustomer(t, db, site, "concurrent-earning-customer")
	for i := range 2 {
		event := model.AdtySiteEarningEvent{OperationKey: fmt.Sprintf("identical:%d", i), UserId: customer.Id, SiteId: site.Id, SiteOwnerUserId: site.OwnerUserId, Quota: int(perUnit("125")), Markup: 1.25, CostFactor: 0.8, Day: time.Now().Format("2006-01-02")}
		require.NoError(t, db.Create(&event).Error)
	}
	var group sync.WaitGroup
	errors := make(chan error, 4)
	for i := range 4 {
		group.Go(func() { errors <- projectAdtySiteEarningEvent(context.Background(), fmt.Sprintf("identical:%d", i%2)) })
	}
	group.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	assert.Equal(t, "50", siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).String())
	var count int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ? AND type = ?", site.Id, "site_earning_settlement").Count(&count).Error)
	assert.EqualValues(t, 2, count, "equal amounts from different business operations remain distinct")
}

func TestCustomerRiskReviewPreservesFinanceEarningDisposition(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "finance-skip-review")
	customer := siteCustomer(t, db, site, "finance-skip-customer")
	day := time.Now().Format("2006-01-02")
	for _, tc := range []struct{ key, status string }{
		{"legacy-risk", ""}, {"finance-skip", model.AdtyEarningOriginalSkipped},
	} {
		event := model.AdtySiteEarningEvent{OperationKey: tc.key, SiteId: site.Id, SiteOwnerUserId: site.OwnerUserId, UserId: customer.Id,
			Quota: int(perUnit("125")), Markup: 1.25, CostFactor: 0.8, Day: day, Projected: true, RiskFlag: model.AdtySiteRiskSelfUse, ReviewStatus: tc.status}
		require.NoError(t, db.Create(&event).Error)
	}
	row := model.AdtySiteEarningDaily{SiteId: site.Id, SiteOwnerUserId: site.OwnerUserId, UserId: customer.Id, Day: day + "+op",
		Kind: model.AdtySiteEarningSkipped, Status: model.AdtySiteEarningPending, Reason: model.AdtySiteRiskSelfUse,
		Quota: perUnit("250"), Revenue: usd("250"), Platform: usd("200"), Earning: usd("50")}
	require.NoError(t, db.Create(&row).Error)
	for range 2 {
		require.NoError(t, ReviewAdtySiteCustomer(customer.Id, 1, true))
	}
	assert.Equal(t, "25", siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).String(), "only heuristic legacy spread is released once")
	assert.Equal(t, "25", earningTotals(t, db, site.Id)[model.AdtySiteEarningSkipped].String(), "the explicit finance exclusion remains pending")
	var excluded model.AdtySiteEarningEvent
	require.NoError(t, db.Where("operation_key = ?", "finance-skip").First(&excluded).Error)
	assert.False(t, excluded.RiskReleased)
	assert.Equal(t, model.AdtyEarningOriginalSkipped, excluded.ReviewStatus)
}

func TestTeamEarningUsesFrozenSupplyAndFinalPrice(t *testing.T) {
	for _, tc := range []struct{ name, final, billed, pay, quota, earning string }{
		{"discount", "120", "1.2", "0.8", "120", "20"},
		{"floor", "100", "1", "0.5", "100", "0"},
		{"refund", "120", "1.2", "0.8", "-60", "-10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := prepareSiteDB(t)
			_, site := openActiveSite(t, db, "team-frozen")
			customer := siteCustomer(t, db, site, "team-frozen-customer")
			snapshot, err := model.EncodeRequestPriceSnapshot(&model.RequestPriceSnapshot{
				SchemaVersion: model.RequestPriceSnapshotVersion, ModelBasePrice: "100", BillingGroup: "default",
				GroupRatio: "1", SiteCostFactor: "1", SiteMarkupRatio: "0.5", TeamPayRatio: tc.pay, TeamApplied: true,
				SupplyCost: "100", SellPrice: "150", SupplyPriceFloor: "100", FinalPrice: tc.final,
				BilledGroupRatio: tc.billed, StoredMarkupK: "1.5", Currency: "USD", BillingUnit: model.BillingUnitPerCall,
				AcceptedAt: time.Now().Unix(), RuleVersion: model.RequestPriceSnapshotVersion, SiteId: site.Id, SiteOwnerUserId: site.OwnerUserId,
			})
			require.NoError(t, err)
			op := model.BillingOperation{OperationKey: "team:frozen", UserId: customer.Id, SiteId: site.Id, SiteOwnerUserId: site.OwnerUserId, Kind: "request", State: model.BillingSettled, PriceSnapshot: snapshot}
			require.NoError(t, db.Create(&op).Error)
			event := model.AdtySiteEarningEvent{OperationKey: op.OperationKey, UserId: customer.Id, SiteId: site.Id, SiteOwnerUserId: site.OwnerUserId, Quota: int(perUnit(tc.quota)), Markup: 1.5, CostFactor: 1, Day: time.Now().Format("2006-01-02")}
			require.NoError(t, db.Create(&event).Error)
			for range 2 {
				require.NoError(t, projectAdtySiteEarningEvent(context.Background(), op.OperationKey))
			}
			var row model.AdtySiteEarningDaily
			require.NoError(t, db.Where("site_id = ?", site.Id).First(&row).Error)
			assert.Equal(t, tc.earning, row.Earning.String())
			assert.Equal(t, tc.quota, row.Platform.Add(row.Earning).String())
			start := time.Now().Truncate(24 * time.Hour).Add(-24 * time.Hour).Unix()
			sheet, err := summarizeAdtySiteSettlement(site.Id, time.Now().Format("2006-01-02"), start, start+3*86400)
			require.NoError(t, err)
			assert.Equal(t, tc.earning, sheet.SpreadUSD.String(), "the daily report uses the same frozen supply cost")
			assert.Equal(t, tc.quota, sheet.SupplyCostUSD.Add(sheet.SpreadUSD).String())
		})
	}
}

func TestFirstCustomerPricePreservesInheritedEffectiveMarkup(t *testing.T) {
	for _, tc := range []struct {
		name, requested, current string
		pending                  bool
	}{
		{"raise", "1.8", "1.5", true}, {"decrease", "1.2", "1.2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := prepareSiteDB(t)
			_, site := openActiveSite(t, db, "first-customer-price")
			sitePrice(t, db, site.Id, 0, "", "1.5")
			customer := siteCustomer(t, db, site, "first-price-customer")
			_, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, UserId: customer.Id, DefaultMarkup: dec(tc.requested), ExpectedVersion: 0, Now: time.Now().Unix()})
			require.NoError(t, err)
			var row model.AdtySitePrice
			require.NoError(t, db.Where("site_id = ? AND user_id = ?", site.Id, customer.Id).First(&row).Error)
			assert.Equal(t, tc.current, row.Markup.String())
			assert.Equal(t, tc.pending, row.PendingAt > 0)
		})
	}
}

// earningRows 取一个站的收益行，按类型汇总。
func earningTotals(t *testing.T, db *gorm.DB, siteId int) map[string]decimal.Decimal {
	t.Helper()
	var rows []model.AdtySiteEarningDaily
	require.NoError(t, db.Where("site_id = ?", siteId).Find(&rows).Error)
	totals := map[string]decimal.Decimal{}
	for _, row := range rows {
		totals[row.Kind] = totals[row.Kind].Add(row.Earning)
	}
	return totals
}

// consumeOnSite 模拟一次扣费：乘 k 之后写消费日志（钩子从 gin 上下文取 k、d）。
func consumeOnSite(t *testing.T, user *model.User, quota int64) {
	t.Helper()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	AdtyApplySiteMarkup(ctx, user.Id, "default", 1, "")
	model.RecordConsumeLog(ctx, user.Id, model.RecordConsumeLogParams{ModelName: "m", Quota: int(quota), Group: "default"})
}

func TestEarningSplitExact(t *testing.T) {
	prepareSiteDB(t)
	revenue, platform, earning := AdtySiteEarningSplit(perUnit("125"), usd("1.25"), usd("0.8"))
	assert.True(t, revenue.Equal(usd("125")), "revenue %s", revenue)
	assert.True(t, platform.Equal(usd("100")), "platform %s", platform)
	assert.True(t, earning.Equal(usd("25")), "earning %s", earning)
	assert.True(t, platform.Add(earning).Equal(revenue))
	_, officialPlatform, officialEarning := OfficialAdtySupplySplit(perUnit("125"), usd("1.25"))
	assert.True(t, platform.Equal(officialPlatform))
	assert.True(t, earning.Equal(officialEarning))
	// d is already inside the charged quota, so a different cost factor must not change the split.
	_, platform2, earning2 := AdtySiteEarningSplit(perUnit("125"), usd("1.25"), usd("1.5"))
	assert.True(t, platform2.Equal(platform))
	assert.True(t, earning2.Equal(earning))

	// 除不尽时两项相加仍然等于客户付的钱。
	revenue, platform, earning = AdtySiteEarningSplit(1_234_567, usd("1.3"), usd("0.77"))
	assert.True(t, platform.Add(earning).Equal(revenue))
}

func TestEarningUsesLoggedSnapshot(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "snapshot")
	customer := siteCustomer(t, db, site, "snapshot-customer")
	sitePrice(t, db, site.Id, 0, "", "1.25")
	consumeOnSite(t, customer, perUnit("125"))
	assert.True(t, earningTotals(t, db, site.Id)[model.AdtySiteEarningUsage].Equal(usd("25")))

	require.NoError(t, db.Model(&model.AdtySitePrice{}).Where("site_id = ?", site.Id).Update("markup", usd("2")).Error)
	_, err := AdjustAdtySite(site.Id, 1, AdtySiteAdjust{CostFactor: dec("0.5")})
	require.NoError(t, err)
	assert.True(t, earningTotals(t, db, site.Id)[model.AdtySiteEarningUsage].Equal(usd("25")), "already recorded earnings do not change")

	consumeOnSite(t, customer, perUnit("100"))
	// 新的 k 2：$100 × (2 − 1) ÷ 2 = $50。合计 $25 + $50 = $75。
	assert.True(t, earningTotals(t, db, site.Id)[model.AdtySiteEarningUsage].Equal(usd("75")))
}

// siteTask 建一个提交时带分站快照的任务。
func siteTask(t *testing.T, db *gorm.DB, user *model.User, site *model.AdtySite, quota int, k, d float64) *model.Task {
	t.Helper()
	task := &model.Task{TaskID: fmt.Sprintf("site-task-%d-%d", user.Id, time.Now().UnixNano()), UserId: user.Id, Group: "default", Quota: quota, Status: model.TaskStatusFailure}
	task.PrivateData.BillingContext = &model.TaskBillingContext{OriginModelName: "earning-task", GroupRatio: k, SiteMarkup: k, SiteCostFactor: d, SiteId: site.Id}
	require.NoError(t, db.Create(task).Error)
	return task
}

func TestMidjourneyRefundUsesSubmitSnapshot(t *testing.T) {
	db := prepareSiteDB(t)
	require.NoError(t, db.AutoMigrate(&model.Midjourney{}, &model.Channel{}))
	_, site := openActiveSite(t, db, "mj-refund")
	customer := siteCustomer(t, db, site, "mj-refund-customer")
	sitePrice(t, db, site.Id, 0, "", "3")
	task := &model.Midjourney{
		UserId: customer.Id, MjId: "mj-snap", Action: "IMAGINE", Status: "FAILURE", SubmitTime: time.Now().Unix(),
		Quota: int(perUnit("125")), BillingSiteId: site.Id, BillingSiteMarkup: 1.25, BillingSiteCostFactor: 0.8,
	}
	require.NoError(t, db.Create(task).Error)
	// This historical fixture has no request operation. The original site's
	// explicit conversion audit establishes that its charge was payable.
	require.NoError(t, ReviewAdtySiteCustomer(customer.Id, 1, true))
	require.True(t, RefundMidjourneyQuota(context.Background(), task, "upstream failed"))
	assert.True(t, earningTotals(t, db, site.Id)[model.AdtySiteEarningRefund].Equal(usd("-25")), "refund keeps the submit snapshot, not the current k of 3")
}

func TestMidjourneyStampPreservesFrozenRequestSnapshot(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "mj-freeze-window")
	customer := siteCustomer(t, db, site, "mj-freeze-window-customer")
	sitePrice(t, db, site.Id, 0, "", "3")
	task := &model.Midjourney{UserId: customer.Id, BillingSiteId: site.Id, BillingSiteMarkup: 1.25, BillingSiteCostFactor: 0.8}
	stampMidjourneySiteBilling(nil, task)
	// The stamp must not consult the site's current price even when RelayInfo
	// is available after a long upstream request.
	stampMidjourneySiteBilling(adtyWalletRelayInfo(customer.Id, 0, ""), task)
	assert.Equal(t, 1.25, task.BillingSiteMarkup)
	assert.Equal(t, 0.8, task.BillingSiteCostFactor)
}

func TestRiskCustomerRefundReconcilesNetEarning(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "risk-refund-net")
	customer := siteCustomer(t, db, site, "risk-refund-net-customer")
	require.NoError(t, db.Model(&model.AdtySiteCustomer{}).Where("user_id = ?", customer.Id).
		Updates(map[string]any{"risk_flag": "self_use", "reviewed": false}).Error)
	sitePrice(t, db, site.Id, 0, "", "1.25")
	consumeOnSite(t, customer, perUnit("125"))
	other := model.NewLogOther()
	other.SetAdmin("site_id", site.Id)
	other.SetAdmin("site_markup", 1.25)
	other.SetAdmin("site_cost_factor", 0.8)
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{UserId: customer.Id, LogType: model.LogTypeRefund, Quota: int(perUnit("125")), Other: other})
	mismatch, reason, err := adtyUsageReconcileMismatch(db, site.Id, time.Now().AddDate(0, 0, 1).Format("2006-01-02"))
	require.NoError(t, err)
	assert.False(t, mismatch, reason)
}

func TestDurableTaskRefundKeepsOriginalRiskDispositionAfterReview(t *testing.T) {
	for _, convert := range []bool{false, true} {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("convert_%t_partial_%t", convert, partial), func(t *testing.T) {
				db := prepareSiteDB(t)
				require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Token{}, &model.Channel{}, &model.Midjourney{}))
				owner, site := openActiveSite(t, db, "risk-refund-review")
				customer := siteCustomer(t, db, site, "risk-refund-review-customer")
				require.NoError(t, db.Model(customer).Update("quota", perUnit("400")).Error)
				require.NoError(t, db.Model(&model.AdtySiteCustomer{}).Where("user_id = ?", customer.Id).
					Updates(map[string]any{"risk_flag": "self_use", "reviewed": false}).Error)
				sitePrice(t, db, site.Id, 0, "", "1.25")
				chargeTask := func(amount string) *model.Task {
					c := adtyWalletRelayContext()
					AdtyApplySiteMarkup(c, customer.Id, "default", 1, "")
					info := adtyWalletRelayInfo(customer.Id, 0, "")
					info.IsPlayground = true
					quota := int(perUnit(amount))
					require.Nil(t, PreConsumeBilling(c, quota, info))
					task := &model.Task{TaskID: model.GenerateTaskID(), UserId: customer.Id, Group: "default", Quota: quota, Status: model.TaskStatusInProgress}
					task.PrivateData.BillingContext = &model.TaskBillingContext{SiteId: site.Id, SiteMarkup: 1.25, SiteCostFactor: 0.8}
					AttachTaskBillingReservation(info, task)
					require.NoError(t, task.Insert())
					require.NoError(t, info.Billing.Settle(quota))
					return task
				}
				oldTask := chargeTask("125")
				require.True(t, earningTotals(t, db, site.Id)[model.AdtySiteEarningUsage].Equal(usd("25")), "risk flag must not cancel confirmed consumption spread")
				require.True(t, siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(usd("25")), "confirmed spread must credit site earnings wallet immediately")
				require.NoError(t, ReviewAdtySiteCustomer(customer.Id, 1, convert))
				chargeTask("50") // A new request after review earns independently.
				remainingEarning := decimal.Zero
				if partial {
					RecalculateTaskQuota(context.Background(), oldTask, int(perUnit("100")), common.NewMessage("partial refund"))
					remainingEarning = usd("20")
				} else {
					require.True(t, RefundTaskQuota(context.Background(), oldTask, "upstream failed"))
				}
				require.NoError(t, RecoverBillingOperations(context.Background(), 100))
				require.NoError(t, RecoverBillingOperations(context.Background(), 100))
				_, err := RunAdtySiteSettlementOnce(time.Now().AddDate(0, 0, 30))
				require.NoError(t, err)
				affiliate, err := Referral.EnsureAffiliate(owner.Id)
				require.NoError(t, err)
				want := usd("10").Add(remainingEarning)
				// 原断言读 referral 佣金账户，那是 ISS-R4-0111 删掉的双记行为：同一笔收益
				// 同时进站点收益钱包和佣金账户，站长两本账各提一次。现在收益只进站点钱包。
				assert.True(t, siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(want),
					"refunds must reverse the original task's earning disposition, without consuming a later task's earnings")
				assert.Zero(t, account(t, db, affiliate.Id).AvailableAmount,
					"settlement must not credit the referral account (ISS-R4-0111)")
				var original model.AdtySiteEarningEvent
				require.NoError(t, db.Where("operation_key = ?", oldTask.PrivateData.BillingOperationKey).First(&original).Error)
				assert.Equal(t, "self_use", original.RiskFlag, "review preserves the original risk metadata audit")
				assert.Equal(t, convert, original.RiskReleased)
			})
		}
	}
}

func TestReviewConvertsZeroNetSkippedEarning(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "review-zero-net")
	customer := siteCustomer(t, db, site, "review-zero-net-customer")
	require.NoError(t, db.Model(&model.AdtySiteCustomer{}).Where("user_id = ?", customer.Id).
		Updates(map[string]any{"risk_flag": "self_use", "reviewed": false}).Error)
	AdtySiteEarningHook(site.Id, customer.Id, perUnit("125"), 1.25, 0.8, model.AdtySiteEarningUsage)
	AdtySiteEarningHook(site.Id, customer.Id, perUnit("125"), 1.25, 0.8, model.AdtySiteEarningRefund)
	require.NoError(t, ReviewAdtySiteCustomer(customer.Id, 1, true))
	totals := earningTotals(t, db, site.Id)
	assert.True(t, totals[model.AdtySiteEarningUsage].Add(totals[model.AdtySiteEarningRefund]).IsZero())
	assert.True(t, siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).IsZero())
}

func TestRealtimeRefundKeepsRiskDispositionWithoutInitialEarningEvent(t *testing.T) {
	for _, convert := range []bool{false, true} {
		t.Run(fmt.Sprintf("convert_%t", convert), func(t *testing.T) {
			db := prepareSiteDB(t)
			require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Token{}, &model.Channel{}, &model.Midjourney{}))
			owner, site := openActiveSite(t, db, "risk-realtime-refund")
			customer := siteCustomer(t, db, site, "risk-realtime-refund-customer")
			require.NoError(t, db.Model(customer).Update("quota", perUnit("400")).Error)
			require.NoError(t, db.Model(&model.AdtySiteCustomer{}).Where("user_id = ?", customer.Id).
				Updates(map[string]any{"risk_flag": "self_use", "reviewed": false}).Error)
			sitePrice(t, db, site.Id, 0, "", "1.25")
			seedToken(t, 9993, customer.Id, "risk-realtime", int(perUnit("400")))
			c := adtyWalletRelayContext()
			AdtyApplySiteMarkup(c, customer.Id, "default", 1, "")
			info := adtyWalletRelayInfo(customer.Id, 9993, "sk-risk-realtime")
			info.OriginModelName, info.UsingGroup, info.UserGroup = "gpt-4o-realtime-preview", "default", "default"
			require.Nil(t, PreConsumeBilling(c, 0, info))
			usage := &dto.RealtimeUsage{InputTokens: 200, OutputTokens: 100, TotalTokens: 300}
			usage.InputTokenDetails.TextTokens, usage.OutputTokenDetails.TextTokens = 200, 100
			require.NoError(t, PreWssConsumeQuota(c, info, usage))
			require.Positive(t, info.RealtimeAlreadyCharged)
			var rootEvents int64
			require.NoError(t, db.Model(&model.AdtySiteEarningEvent{}).Where("operation_key = ?", "request:"+info.RequestId).Count(&rootEvents).Error)
			require.Zero(t, rootEvents, "the zero opening reserve has no earning event to inherit from")
			require.NoError(t, ReviewAdtySiteCustomer(customer.Id, 1, convert))
			fresh := adtyWalletRelayContext()
			AdtyApplySiteMarkup(fresh, customer.Id, "default", 1, "")
			newInfo := adtyWalletRelayInfo(customer.Id, 0, "")
			newInfo.IsPlayground = true
			require.Nil(t, PreConsumeBilling(fresh, int(perUnit("50")), newInfo))
			require.NoError(t, newInfo.Billing.Settle(int(perUnit("50"))))
			require.NoError(t, SettleBilling(c, info, 0))
			require.NoError(t, RecoverBillingOperations(context.Background(), 100))
			_, err := RunAdtySiteSettlementOnce(time.Now().AddDate(0, 0, 30))
			require.NoError(t, err)
			affiliate, err := Referral.EnsureAffiliate(owner.Id)
			require.NoError(t, err)
			// The later $50 request at markup 1.25 earns $50 - $40 = $10.
			assert.Equal(t, "10", siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).String(),
				"the refund preserves the later request's earnings in the site wallet")
			assert.Zero(t, account(t, db, affiliate.Id).AvailableAmount,
				"site earnings must not also credit the referral account")
			var original model.AdtySiteEarningEvent
			require.NoError(t, db.Where("operation_key = ?", "request:"+info.RequestId).First(&original).Error)
			assert.Equal(t, "self_use", original.RiskFlag)
			assert.Equal(t, convert, original.RiskReleased)
		})
	}
}

func TestDurableEarningProjectionRecoversWithoutConsumeLogs(t *testing.T) {
	db := prepareSiteDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.Channel{}, &model.Task{}, &model.Midjourney{}))
	_, site := openActiveSite(t, db, "durable-earning")
	customer := siteCustomer(t, db, site, "durable-earning-customer")
	sitePrice(t, db, site.Id, 0, "", "1.25")
	require.NoError(t, db.Model(customer).Update("quota", perUnit("200")).Error)
	c := adtyWalletRelayContext()
	AdtyApplySiteMarkup(c, customer.Id, "default", 1, "")
	info := adtyWalletRelayInfo(customer.Id, 0, "")
	info.IsPlayground = true
	require.Nil(t, PreConsumeBilling(c, int(perUnit("125")), info))
	failProjection := true
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:earning-projection-failure", func(tx *gorm.DB) {
		if failProjection && tx.Statement.Table == "adty_site_earning_daily" {
			tx.AddError(fmt.Errorf("forced daily projection failure"))
		}
	}))
	t.Cleanup(func() { db.Callback().Update().Remove("test:earning-projection-failure") })
	saved := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = saved })
	require.Error(t, info.Billing.Settle(int(perUnit("125"))), "earning projection failure rolls back settlement, retaining its retry intent")
	assert.EqualValues(t, perUnit("75"), getUserQuota(t, customer.Id))
	var events []model.AdtySiteEarningEvent
	require.NoError(t, db.Where("site_id = ?", site.Id).Find(&events).Error)
	require.Empty(t, events, "the uncommitted earning event rolls back with settlement")
	var operation model.BillingOperation
	require.NoError(t, db.Where("operation_key = ?", "request:"+info.RequestId).First(&operation).Error)
	assert.Equal(t, model.BillingSettlePending, operation.State)
	var dailyRows int64
	require.NoError(t, db.Model(&model.AdtySiteEarningDaily{}).Where("site_id = ?", site.Id).Count(&dailyRows).Error)
	assert.Zero(t, dailyRows, "a failed projection must roll back the seed row as well as the aggregate")
	failProjection = false
	require.NoError(t, RecoverBillingOperations(context.Background(), 100))
	require.NoError(t, RecoverBillingOperations(context.Background(), 100))
	assert.True(t, earningTotals(t, db, site.Id)[model.AdtySiteEarningUsage].Equal(usd("25")))
	mismatch, reason, err := adtyUsageReconcileMismatch(db, site.Id, time.Now().AddDate(0, 0, 1).Format("2006-01-02"))
	require.NoError(t, err)
	assert.False(t, mismatch, reason)
	var logs int64
	require.NoError(t, db.Model(&model.Log{}).Where("user_id = ?", customer.Id).Count(&logs).Error)
	assert.Zero(t, logs)
}

func TestTaskRefundReversesEarningWithSnapshot(t *testing.T) {
	db := prepareSiteDB(t)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Token{}, &model.Channel{}))
	_, site := openActiveSite(t, db, "task-refund")
	customer := siteCustomer(t, db, site, "task-refund-customer")
	sitePrice(t, db, site.Id, 0, "", "3")
	task := siteTask(t, db, customer, site, int(perUnit("125")), 1.25, 0.8)
	require.NoError(t, ReviewAdtySiteCustomer(customer.Id, 1, true))
	require.True(t, RefundTaskQuota(context.Background(), task, "upstream failed"))
	// 快照 k 1.25，不按现在的 k 3：退 $125 → 站长 −$25。
	assert.True(t, earningTotals(t, db, site.Id)[model.AdtySiteEarningRefund].Equal(usd("-25")))
}

func TestNegativeTaskDeltaReversesEarning(t *testing.T) {
	db := prepareSiteDB(t)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Token{}, &model.Channel{}))
	_, site := openActiveSite(t, db, "task-delta")
	customer := siteCustomer(t, db, site, "task-delta-customer")
	task := siteTask(t, db, customer, site, int(perUnit("125")), 1.25, 0.8)
	require.NoError(t, ReviewAdtySiteCustomer(customer.Id, 1, true))
	RecalculateTaskQuota(context.Background(), task, int(perUnit("100")), common.NewMessage("adjusted"))
	assert.True(t, earningTotals(t, db, site.Id)[model.AdtySiteEarningRefund].Equal(usd("-5")), "$25 back × 0.2 = $5")
	RecalculateTaskQuota(context.Background(), task, int(perUnit("112.5")), common.NewMessage("adjusted"))
	assert.True(t, earningTotals(t, db, site.Id)[model.AdtySiteEarningUsage].Equal(usd("2.5")), "a positive delta earns")
}

func TestEarningRecordedWhenConsumeLogDisabled(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "no-log")
	customer := siteCustomer(t, db, site, "no-log-customer")
	sitePrice(t, db, site.Id, 0, "", "1.25")
	old := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = old })
	consumeOnSite(t, customer, perUnit("125"))
	var logs int64
	require.NoError(t, db.Model(&model.Log{}).Where("user_id = ?", customer.Id).Count(&logs).Error)
	assert.Zero(t, logs)
	assert.True(t, earningTotals(t, db, site.Id)[model.AdtySiteEarningUsage].Equal(usd("25")))
}

func TestChannelTestNotCounted(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "chantest")
	customer := siteCustomer(t, db, site, "chantest-customer")
	sitePrice(t, db, site.Id, 0, "", "1.25")
	// 渠道测试不经过乘 k（relay/helper.HandleGroupRatio 跳过 IsChannelTest），上下文里没有 k。
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/api/channel/test/1", nil)
	model.RecordConsumeLog(ctx, customer.Id, model.RecordConsumeLogParams{ModelName: "m", Quota: int(perUnit("10")), TokenName: "Model test"})
	assert.Empty(t, earningTotals(t, db, site.Id))
}

func TestMainSiteCustomerNoEarning(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "main-only")
	mainUser := referralUser(t, db, "main-only-customer")
	sitePrice(t, db, site.Id, 0, "", "1.25")
	consumeOnSite(t, mainUser, perUnit("100"))
	var rows int64
	require.NoError(t, db.Model(&model.AdtySiteEarningDaily{}).Count(&rows).Error)
	assert.Zero(t, rows)
}

// siteTopUp 建一笔分站客户的订单并入账，加赠比例 bonusRate%。
func siteTopUp(t *testing.T, user *model.User, site *model.AdtySite, tradeNo string, face int64, bonusRate string) {
	t.Helper()
	bonus := decimal.NewFromInt(face).Mul(usd(bonusRate)).Div(decimal.NewFromInt(100))
	credit := decimal.NewFromInt(face).Add(bonus).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Floor().IntPart()
	require.NoError(t, model.CreateAdtyTopUp(
		&model.TopUp{UserId: user.Id, Amount: face, TradeNo: tradeNo, PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt, Status: common.TopUpStatusPending},
		&model.AdtyTopupOrder{TradeNo: tradeNo, UserId: user.Id, Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
			FaceUSD: decimal.NewFromInt(face), BonusRate: usd(bonusRate), BonusUSD: bonus, CreditQuota: credit,
			PayAmount: decimal.NewFromInt(face), PayCurrency: "USDT", TopupGroupRatio: decimal.NewFromInt(1),
			ExpireAt: time.Now().Unix() + 900, SiteId: site.Id},
	))
	_, err := model.CompleteAdtyTopUp(tradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(face), FiatAmount: decimal.NewFromInt(face),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)
}

func TestSiteBonusDeductedFromEarning(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "bonus")
	customer := siteCustomer(t, db, site, "bonus-customer")
	siteTopUp(t, customer, site, "EPU-site-bonus", 100, "5")
	var stored model.User
	require.NoError(t, db.First(&stored, customer.Id).Error)
	assert.EqualValues(t, perUnit("105"), stored.Quota, "pays $100, gets $105")
	assert.True(t, earningTotals(t, db, site.Id)[model.AdtySiteEarningBonus].Equal(usd("-5")))

	consumeOnSite(t, customer, perUnit("105")) // k 1（没设定价）：售价等于供货价，价差为 0
	totals := earningTotals(t, db, site.Id)
	assert.True(t, totals[model.AdtySiteEarningUsage].Equal(usd("0")), "k=1 leaves no site markup")
	assert.True(t, totals[model.AdtySiteEarningUsage].Add(totals[model.AdtySiteEarningBonus]).Equal(usd("-5")))
	var platform struct{ Total decimal.Decimal }
	require.NoError(t, db.Model(&model.AdtySiteEarningDaily{}).Select("COALESCE(SUM(platform),0) AS total").Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteEarningUsage).Scan(&platform).Error)
	assert.True(t, platform.Total.Equal(usd("105")), "the platform keeps the full charged amount when k=1")
}

func TestTopupRefundReversesBonusDeduction(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "bonus-refund")
	customer := siteCustomer(t, db, site, "bonus-refund-customer")
	siteTopUp(t, customer, site, "EPU-site-refund", 100, "5")
	_, err := refundAdtyTopUp(t, "EPU-site-refund", "chargeback", 1, "refund-site-1")
	require.NoError(t, err)
	totals := earningTotals(t, db, site.Id)
	assert.True(t, totals[model.AdtySiteEarningBonus].Equal(usd("-5")))
	assert.True(t, totals[model.AdtySiteEarningBonusReversal].Equal(usd("5")))
}

// 赠金先花掉一部分再退款：按「先花本金」，收回的赠金是 min(G, R)，站长只拿回这部分。
// 付 $100、站长送 $10（10%），花掉 $105，余额 $5：R = $5，只收回 $5 赠金，还给站长 $5，不是 $10。
func TestTopupRefundReturnsOnlyReclaimedOwnerBonus(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "bonus-spent")
	customer := siteCustomer(t, db, site, "bonus-spent-customer")
	siteTopUp(t, customer, site, "EPU-site-bonus-spent", 100, "10")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", customer.Id).Update("quota", perUnit("5")).Error)

	preview, err := model.PreviewAdtyTopUpRefund("EPU-site-bonus-spent")
	require.NoError(t, err)
	assert.EqualValues(t, perUnit("5"), preview.DeductedQuota)
	assert.Zero(t, preview.RefundQuota, "leftover spendable is gift, so preview principal is 0")
	_, err = model.RefundAdtyTopUp("EPU-site-bonus-spent", "chargeback", 1, "refund-site-bonus-spent", preview.RefundQuota)
	require.Error(t, err, "a zero principal amount cannot complete a refund")
	totals := earningTotals(t, db, site.Id)
	assert.True(t, totals[model.AdtySiteEarningBonus].Equal(usd("-10")))
	assert.True(t, totals[model.AdtySiteEarningBonusReversal].IsZero(), "gift is not reclaimed until a principal refund completes")
}

// 客户花掉一部分后退款：赠金没花到（余额 ≥ 赠金），站长出的加赠全额还给站长，已经花掉部分的消费分成不扣。
func TestPartialTopupRefundKeepsUsageEarning(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "partial-refund")
	customer := siteCustomer(t, db, site, "partial-refund-customer")
	siteTopUp(t, customer, site, "EPU-site-partial", 100, "5")
	consumeOnSite(t, customer, perUnit("30")) // k 1：售价等于供货价，消费价差为 0
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", customer.Id).Update("quota", perUnit("75")).Error)

	result, err := refundAdtyTopUp(t, "EPU-site-partial", "chargeback", 1, "refund-site-partial")
	require.NoError(t, err)
	assert.EqualValues(t, perUnit("73.5"), (result.Refund.DeductedPrincipalQuota + result.Refund.BonusDeductedQuota), "principal $70 plus G.9 gift $5 × 70/100")
	assert.EqualValues(t, perUnit("70"), result.Occupancy.CompletedQuota, "previewed principal")
	assert.True(t, result.Refund.RequestUSD.Equal(usd("70")))
	totals := earningTotals(t, db, site.Id)
	// The refund machine reclaims $3.5 gift (G.9), so the site-owner bonus reversal and guarantee release follow 70/100 ($3.5).
	assert.True(t, totals[model.AdtySiteEarningBonusReversal].Equal(usd("3.5")), "site-owner bonus reversal must match proportional gift reclaim")
	assert.True(t, totals[model.AdtySiteEarningUsage].Equal(usd("0")), "k=1 usage earning stays at zero after the refund")
	assert.True(t, totals[model.AdtySiteEarningRefund].IsZero())
}

func TestTopUpStatsKeepUnrefundedFace(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "stats-refund")
	partial := siteCustomer(t, db, site, "stats-partial-customer")
	siteTopUp(t, partial, site, "EPU-stats-partial", 100, "5")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", partial.Id).Update("quota", perUnit("75")).Error)
	partialRefund, err := refundAdtyTopUp(t, "EPU-stats-partial", "chargeback", 1, "refund-stats-partial")
	require.NoError(t, err)
	assert.EqualValues(t, perUnit("70"), partialRefund.Occupancy.CompletedQuota)

	today := time.Now().Format("2006-01-02")
	stats, err := GetAdtySiteStats(site.Id, today, today)
	require.NoError(t, err)
	assert.True(t, stats.Total.TopUpUSD.Equal(usd("30")), "partial refund kept %s", stats.Total.TopUpUSD)
	assert.EqualValues(t, 1, stats.Total.PaidCustomers)

	spent := siteCustomer(t, db, site, "stats-zero-customer")
	siteTopUp(t, spent, site, "EPU-stats-zero", 100, "10")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", spent.Id).Update("quota", perUnit("5")).Error)
	zeroPreview, err := model.PreviewAdtyTopUpRefund("EPU-stats-zero")
	require.NoError(t, err)
	assert.Zero(t, zeroPreview.RefundQuota, "a zero spendable principal is not a completed refund")
	stats, err = GetAdtySiteStats(site.Id, today, today)
	require.NoError(t, err)
	assert.True(t, stats.Total.TopUpUSD.Equal(usd("130")), "zero principal refund kept %s", stats.Total.TopUpUSD)
	assert.EqualValues(t, 2, stats.Total.PaidCustomers)

	full := siteCustomer(t, db, site, "stats-full-customer")
	siteTopUp(t, full, site, "EPU-stats-full", 50, "0")
	fullRefund, err := refundAdtyTopUp(t, "EPU-stats-full", "chargeback", 1, "refund-stats-full")
	require.NoError(t, err)
	assert.EqualValues(t, perUnit("50"), fullRefund.Occupancy.CompletedQuota)
	stats, err = GetAdtySiteStats(site.Id, today, today)
	require.NoError(t, err)
	assert.True(t, stats.Total.TopUpUSD.Equal(usd("130")), "full refund kept %s", stats.Total.TopUpUSD)
	assert.EqualValues(t, 2, stats.Total.PaidCustomers)

	month, err := AdtySitesMonthTotals([]int{site.Id}, time.Now())
	require.NoError(t, err)
	assert.True(t, month[site.Id].TopUpUSD.Equal(usd("130")), "month %s", month[site.Id].TopUpUSD)
	overview, err := GetAdtySitesOverview(today, today)
	require.NoError(t, err)
	assert.True(t, overview.SiteTopUpUSD.Equal(usd("130")), "overview %s", overview.SiteTopUpUSD)
}

func TestSiteBonusCappedByMainPlusExtra(t *testing.T) {
	db := prepareSiteDB(t)
	setAdtyQuoteDefaults(t)
	setAdtyBonusRates(t, map[int]float64{100: 2.5, 500: 4})
	_, site := openActiveSite(t, db, "bonus-cap")
	// max_site_bonus_extra 是 2：$100 档最多 4.5%，$300 落在 $100 档，$500 档最多 6%，$50 没有主站档，最多 2%。
	assert.NoError(t, ValidateAdtySiteBonusRates(map[int]float64{100: 4.5, 300: 4.5, 500: 6, 50: 2}))
	assert.Error(t, ValidateAdtySiteBonusRates(map[int]float64{100: 4.6}))
	assert.Error(t, ValidateAdtySiteBonusRates(map[int]float64{50: 2.1}))
	assert.Error(t, ValidateAdtySiteBonusRates(map[int]float64{500: 6.5}))

	require.ErrorIs(t, SaveAdtySiteBonusRates(site.Id, map[int]float64{100: 4}), ErrAdtyAmountTierRetired)
	quote, err := AdtyQuoteForSite(AdtySiteById(site.Id), "default", 200, AdtyMethodUSDTTRC20)
	require.NoError(t, err)
	assert.True(t, quote.BonusUSD.IsZero(), "amount tiers no longer price a new order")
	quote, err = AdtyQuoteFor("default", 200, AdtyMethodUSDTTRC20)
	require.NoError(t, err)
	assert.True(t, quote.BonusUSD.IsZero())

	SetAdtyMethodBonusForTest(t, "usdt", true, "0.025")
	mainQuote, err := AdtyQuoteFor("default", 100, AdtyMethodUSDTTRC20)
	require.NoError(t, err)
	assert.True(t, mainQuote.BonusUSD.Equal(usd("2.5")))
	unauthorized, err := AdtyQuoteForSite(AdtySiteById(site.Id), "default", 100, AdtyMethodUSDTTRC20)
	require.NoError(t, err)
	assert.True(t, unauthorized.BonusUSD.IsZero(), "a site without Root authorization does not inherit the main method gift")
}

func TestTrialGrantLimitsAndLedger(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "trial")
	customer := siteCustomer(t, db, site, "trial-customer")
	stranger := referralUser(t, db, "trial-stranger")

	_, err := GrantAdtySiteTrial(site.Id, customer.Id, usd("5.01"), "k0")
	assert.Error(t, err, "above trial_grant_max_usd")
	_, err = GrantAdtySiteTrial(site.Id, stranger.Id, usd("1"), "k1")
	assert.Error(t, err, "not this site's customer")
	_, err = GrantAdtySiteTrial(site.Id, customer.Id, usd("1"), "broke")
	assert.Error(t, err, "zero withdrawable commission cannot fund a trial")

	affiliate, err := Referral.EnsureAffiliate(owner.Id)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.ReferralCommissionAccount{}).Where("affiliate_id = ?", affiliate.Id).Update("available_amount", 100).Error)

	grant, err := GrantAdtySiteTrial(site.Id, customer.Id, usd("5"), "k2")
	require.NoError(t, err)
	again, err := GrantAdtySiteTrial(site.Id, customer.Id, usd("5"), "k2")
	require.NoError(t, err)
	assert.Equal(t, grant.Id, again.Id, "replay is idempotent")
	_, err = GrantAdtySiteTrial(site.Id, customer.Id, usd("4"), "k2")
	assert.ErrorIs(t, err, model.ErrAdtyIdempotencyConflict)
	assert.InDelta(t, 95, account(t, db, affiliate.Id).AvailableAmount, 1e-9)

	require.NoError(t, db.Model(&model.AdtySite{}).Where("id = ?", site.Id).Update("status", model.AdtySiteStatusPaused).Error)
	paused, err := GrantAdtySiteTrial(site.Id, customer.Id, usd("5"), "k2")
	require.NoError(t, err)
	assert.Equal(t, grant.Id, paused.Id, "a leftover paused site still replays the same grant")
	require.NoError(t, db.Model(&model.AdtySite{}).Where("id = ?", site.Id).Update("status", model.AdtySiteStatusActive).Error)

	var stored model.User
	require.NoError(t, db.First(&stored, customer.Id).Error)
	assert.EqualValues(t, perUnit("5"), stored.Quota)
	var ledger model.AdtyBalanceLedger
	require.NoError(t, db.Where("user_id = ? AND type = ?", customer.Id, model.AdtyLedgerTypeSiteTrial).First(&ledger).Error)
	assert.EqualValues(t, perUnit("5"), ledger.DeltaQuota)
	var trialLots []model.AdtyFundBatch
	require.NoError(t, db.Where("user_id = ? AND source = ?", customer.Id, fmt.Sprintf("site-trial-%d", grant.Id)).Find(&trialLots).Error)
	require.Len(t, trialLots, 1)
	assert.Equal(t, "bonus", trialLots[0].Kind)
	assert.EqualValues(t, perUnit("5"), trialLots[0].Original)
	assert.EqualValues(t, perUnit("5"), trialLots[0].Remaining)
	assert.True(t, earningTotals(t, db, site.Id)[model.AdtySiteEarningTrial].Equal(usd("-5")))
	var trialRow model.AdtySiteEarningDaily
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteEarningTrial).First(&trialRow).Error)
	assert.Equal(t, model.AdtySiteEarningAvailable, trialRow.Status)
	assert.NotZero(t, trialRow.SettledAt)

	for i := range 3 {
		_, err := GrantAdtySiteTrial(site.Id, customer.Id, usd("5"), fmt.Sprintf("more-%d", i))
		require.NoError(t, err)
	}
	_, err = GrantAdtySiteTrial(site.Id, customer.Id, usd("0.01"), "over-daily")
	assert.Error(t, err, "trial_grant_daily_usd of $20 is used up")

	resellerSetting().TrialGrantMaxUSD = nil
	_, err = GrantAdtySiteTrial(site.Id, customer.Id, usd("1"), "unset")
	assert.ErrorIs(t, err, ErrAdtySiteTrialUnavailable)
}

func TestLeftoverPausedSiteAllowsTrialGrant(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "trial-paused")
	customer := siteCustomer(t, db, site, "trial-paused-customer")
	affiliate, err := Referral.EnsureAffiliate(owner.Id)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.ReferralCommissionAccount{}).Where("affiliate_id = ?", affiliate.Id).Update("available_amount", 100).Error)
	require.NoError(t, db.Model(&model.AdtySite{}).Where("id = ?", site.Id).Update("status", model.AdtySiteStatusPaused).Error)
	grant, err := GrantAdtySiteTrial(site.Id, customer.Id, usd("5"), "leftover-paused")
	require.NoError(t, err, "leftover paused rows must not freeze new trial grants")
	require.NotNil(t, grant)
	var stored model.User
	require.NoError(t, db.First(&stored, customer.Id).Error)
	assert.EqualValues(t, perUnit("5"), stored.Quota)
	var pausedLots []model.AdtyFundBatch
	require.NoError(t, db.Where("user_id = ? AND source = ?", customer.Id, fmt.Sprintf("site-trial-%d", grant.Id)).Find(&pausedLots).Error)
	require.Len(t, pausedLots, 1)
	assert.Equal(t, "bonus", pausedLots[0].Kind)
	assert.EqualValues(t, perUnit("5"), pausedLots[0].Remaining)
}

func TestTrialGrantConcurrentSameKey(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "trial-race")
	customer := siteCustomer(t, db, site, "trial-race-customer")
	affiliate, err := Referral.EnsureAffiliate(owner.Id)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.ReferralCommissionAccount{}).Where("affiliate_id = ?", affiliate.Id).Update("available_amount", 20).Error)

	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := make([]int64, 0, 2)
	errs := make([]error, 0)
	for range 2 {
		wg.Go(func() {
			grant, err := GrantAdtySiteTrial(site.Id, customer.Id, usd("5"), "same")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			ids = append(ids, grant.Id)
		})
	}
	wg.Wait()
	require.Empty(t, errs)
	require.Len(t, ids, 2)
	assert.Equal(t, ids[0], ids[1])
	assert.InDelta(t, 15, account(t, db, affiliate.Id).AvailableAmount, 1e-9)
	var stored model.User
	require.NoError(t, db.First(&stored, customer.Id).Error)
	assert.EqualValues(t, perUnit("5"), stored.Quota)
}

// registerSiteCustomer 模拟分站注册：带 IP 和设备 cookie，走 AdtyAfterRegister。
func registerSiteCustomer(t *testing.T, db *gorm.DB, site *model.AdtySite, name, ip, device string) *model.User {
	t.Helper()
	user := referralUser(t, db, name)
	require.NoError(t, model.AdtySetUserSiteTx(db, user.Id, site.Id))
	user.SiteId = site.Id
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/api/user/register", nil)
	ctx.Request.RemoteAddr = ip + ":1234"
	ctx.Request.Header.Set("Cookie", AdtyDeviceCookie+"="+device)
	AdtyAfterRegister(ctx, user)
	return user
}

func recordFingerprint(t *testing.T, db *gorm.DB, userId int, ip, device string) {
	t.Helper()
	require.NoError(t, db.Create(&model.AdtyUserFingerprint{UserId: userId, IpHash: HashReferralRiskValue(ip), DeviceHash: HashReferralRiskValue(device), Kind: "login", CreatedAt: time.Now().Unix()}).Error)
}

func TestSelfUseFlagSkipsEarning(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "selfuse")
	recordFingerprint(t, db, owner.Id, "203.0.113.7", "owner-device")
	sitePrice(t, db, site.Id, 0, "", "1.25")
	customer := registerSiteCustomer(t, db, site, "selfuse-customer", "203.0.113.7", "other-device")
	var row model.AdtySiteCustomer
	require.NoError(t, db.First(&row, customer.Id).Error)
	assert.Equal(t, model.AdtySiteRiskSelfUse, row.RiskFlag)

	consumeOnSite(t, customer, perUnit("125"))
	totals := earningTotals(t, db, site.Id)
	assert.True(t, totals[model.AdtySiteEarningUsage].Equal(usd("25")), "risk metadata must not cancel confirmed consumption spread")
	assert.True(t, totals[model.AdtySiteEarningSkipped].IsZero())
	assert.True(t, siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(usd("25")))
	var usage model.AdtySiteEarningDaily
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteEarningUsage).First(&usage).Error)
	assert.Equal(t, model.AdtySiteRiskSelfUse, usage.Reason)
	assert.Equal(t, model.AdtySiteEarningAvailable, usage.Status)

	clean := registerSiteCustomer(t, db, site, "clean-customer", "198.51.100.1", "fresh-device")
	var cleanRow model.AdtySiteCustomer
	require.NoError(t, db.First(&cleanRow, clean.Id).Error)
	assert.Empty(t, cleanRow.RiskFlag)
}

func TestMigrationFlagSkipsEarning(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "migrate")
	sitePrice(t, db, site.Id, 0, "", "1.25")
	payer := referralUser(t, db, "main-payer")
	require.NoError(t, db.Create(&model.TopUp{UserId: payer.Id, Amount: 10, TradeNo: "MAIN-PAID", Status: common.TopUpStatusSuccess}).Error)
	recordFingerprint(t, db, payer.Id, "192.0.2.10", "payer-device")
	customer := registerSiteCustomer(t, db, site, "migrated-customer", "192.0.2.99", "payer-device")
	var row model.AdtySiteCustomer
	require.NoError(t, db.First(&row, customer.Id).Error)
	assert.Equal(t, model.AdtySiteRiskMigration, row.RiskFlag)
	consumeOnSite(t, customer, perUnit("10"))
	var usage model.AdtySiteEarningDaily
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteEarningUsage).First(&usage).Error)
	assert.Equal(t, model.AdtySiteRiskMigration, usage.Reason)
	assert.True(t, earningTotals(t, db, site.Id)[model.AdtySiteEarningUsage].Equal(usd("2")), "migration risk flag must not cancel confirmed spread")
	assert.True(t, siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(usd("2")))

	resellerSetting().MigrationCheckEnabled = false
	other := registerSiteCustomer(t, db, site, "migrated-unchecked", "192.0.2.98", "payer-device")
	var otherRow model.AdtySiteCustomer
	require.NoError(t, db.First(&otherRow, other.Id).Error)
	assert.Empty(t, otherRow.RiskFlag, "the check can be switched off")
}

func TestReviewedCustomerEarns(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "reviewed")
	recordFingerprint(t, db, owner.Id, "203.0.113.8", "owner-device-2")
	customer := registerSiteCustomer(t, db, site, "reviewed-customer", "203.0.113.8", "x")
	consumeOnSite(t, customer, perUnit("10"))
	require.NoError(t, ReviewAdtySiteCustomer(customer.Id, 1, true))
	consumeOnSite(t, customer, perUnit("10"))
	totals := earningTotals(t, db, site.Id)
	assert.True(t, totals[model.AdtySiteEarningSkipped].IsZero(), "pending skipped rows were converted")
	assert.True(t, totals[model.AdtySiteEarningUsage].Equal(usd("0")), "k=1 leaves no site markup on the converted rows")
}

func TestSettleAfterFreezeIntoAccount(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "settle")
	customer := siteCustomer(t, db, site, "settle-customer")
	sitePrice(t, db, site.Id, 0, "", "1.25")
	saved := adtySiteEarningDayFn
	t.Cleanup(func() { adtySiteEarningDayFn = saved })
	adtySiteEarningDayFn = func() string { return "2026-10-01" }
	consumeOnSite(t, customer, perUnit("125"))
	assert.True(t, siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(usd("25")),
		"confirmed spread must credit site earnings wallet immediately without freeze_days delay")

	adtySiteEarningDayFn = func() string { return "2026-10-05" }
	consumeOnSite(t, customer, perUnit("12.5"))
	assert.True(t, siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(usd("27.5")))

	day := func(value string) time.Time {
		parsed, err := time.ParseInLocation("2006-01-02 15:04", value, time.Local)
		require.NoError(t, err)
		return parsed
	}
	settled, err := RunAdtySiteSettlementOnce(day("2026-10-07 04:00"))
	require.NoError(t, err)
	assert.Zero(t, settled, "already-settled earnings must not be double-credited by settlement task")

	affiliate, err := Referral.EnsureAffiliate(owner.Id)
	require.NoError(t, err)
	assert.True(t, siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(usd("27.5")))
	var ledger model.AdtySiteWalletLedger
	require.NoError(t, db.Where("site_id = ? AND type = ?", site.Id, "site_earning_settlement").First(&ledger).Error)
	assert.True(t, ledger.Delta.Equal(usd("25")))
	assert.Zero(t, account(t, db, affiliate.Id).AvailableAmount,
		"settlement must not credit the referral account (ISS-R4-0111)")

	adtySiteEarningDayFn = func() string { return "2026-10-01" }
	consumeOnSite(t, customer, perUnit("12.5"))
	settled, err = RunAdtySiteSettlementOnce(day("2026-10-13 04:00"))
	require.NoError(t, err)
	assert.Zero(t, settled)
	assert.True(t, siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(usd("30")),
		"$25 + $2.5 + $2.5")
}

func TestLeftoverPausedSiteStillSettles(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "paused-settle")
	customer := siteCustomer(t, db, site, "paused-customer")
	saved := adtySiteEarningDayFn
	t.Cleanup(func() { adtySiteEarningDayFn = saved })
	adtySiteEarningDayFn = func() string { return "2026-09-01" }
	consumeOnSite(t, customer, perUnit("10"))
	require.NoError(t, db.Model(&model.AdtySite{}).Where("id = ?", site.Id).Update("status", model.AdtySiteStatusPaused).Error)
	_, err := RunAdtySiteSettlementOnce(time.Date(2026, 10, 1, 4, 0, 0, 0, time.Local))
	require.NoError(t, err)
	var row model.AdtySiteEarningDaily
	require.NoError(t, db.Where("site_id = ?", site.Id).First(&row).Error)
	assert.Equal(t, model.AdtySiteEarningAvailable, row.Status)
}

func TestNegativeRowsSettleWithoutFreeze(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "neg-now")
	customer := siteCustomer(t, db, site, "neg-now-customer")
	saved := adtySiteEarningDayFn
	t.Cleanup(func() { adtySiteEarningDayFn = saved })
	adtySiteEarningDayFn = func() string { return "2026-10-05" }
	siteTopUp(t, customer, site, "EPU-neg-now", 100, "10")
	consumeOnSite(t, customer, perUnit("10"))
	settled, err := RunAdtySiteSettlementOnce(time.Date(2026, 10, 6, 4, 0, 0, 0, time.Local))
	require.NoError(t, err)
	assert.Equal(t, 1, settled)
	var bonus, usage model.AdtySiteEarningDaily
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteEarningBonus).First(&bonus).Error)
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteEarningUsage).First(&usage).Error)
	assert.Equal(t, model.AdtySiteEarningAvailable, bonus.Status)
	assert.Equal(t, model.AdtySiteEarningAvailable, usage.Status)
	affiliate, err := Referral.EnsureAffiliate(owner.Id)
	require.NoError(t, err)
	assert.InDelta(t, 0, account(t, db, affiliate.Id).AvailableAmount, 1e-9, "bonus settlement must not reduce the commission book")
}

func TestBonusSettlementDoesNotReduceCommissionAvailable(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "negative")
	customer := siteCustomer(t, db, site, "negative-customer")
	saved := adtySiteEarningDayFn
	t.Cleanup(func() { adtySiteEarningDayFn = saved })
	adtySiteEarningDayFn = func() string { return "2026-09-01" }
	siteTopUp(t, customer, site, "EPU-negative", 100, "10")
	consumeOnSite(t, customer, perUnit("10")) // k=1 价差为 0，加赠 −$10
	affiliate, err := Referral.EnsureAffiliate(owner.Id)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.ReferralCommissionAccount{}).Where("affiliate_id = ?", affiliate.Id).Update("available_amount", 25).Error)
	_, err = RunAdtySiteSettlementOnce(time.Date(2026, 10, 1, 4, 0, 0, 0, time.Local))
	require.NoError(t, err)
	assert.InDelta(t, 25, account(t, db, affiliate.Id).AvailableAmount, 1e-9, "gift settlement must not reduce the commission book")
	withdrawal, err := Referral.CreateWithdrawal(ReferralWithdrawalCreateInput{UserId: owner.Id, Amount: usd("25"), AccountType: model.ReferralWithdrawToUSDT,
		AccountNetwork: "TRC20", AccountNo: "T" + "A1B2C3D4E5F6G7H8J9K1L2M3N4P5Q6R7S", IdempotencyKey: "neg-1"})
	require.NoError(t, err)
	assert.Equal(t, model.ReferralWithdrawalStatusPending, withdrawal.Status)
}

func TestSiteOwnerTransfersToBalanceInstantly(t *testing.T) {
	db := prepareSiteDB(t)
	owner, _ := openActiveSite(t, db, "owner-transfer")
	affiliate, err := Referral.EnsureAffiliate(owner.Id)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.ReferralAffiliate{}).Where("id = ?", affiliate.Id).Update("site_owner_since", time.Now().Unix()).Error)
	require.NoError(t, db.Model(&model.ReferralCommissionAccount{}).Where("affiliate_id = ?", affiliate.Id).Update("available_amount", 100).Error)
	var before model.User
	require.NoError(t, db.First(&before, owner.Id).Error)
	// 站长收益和邀请佣金一样：转入余额不用审核，立即到账（任务 6.4）。
	transfer, err := Referral.CreateWithdrawal(ReferralWithdrawalCreateInput{UserId: owner.Id, Amount: usd("30"), AccountType: model.ReferralWithdrawToBalance, IdempotencyKey: "ot-1"})
	require.NoError(t, err)
	assert.Equal(t, model.ReferralWithdrawalStatusPaid, transfer.Status)
	var after model.User
	require.NoError(t, db.First(&after, owner.Id).Error)
	assert.Equal(t, before.Quota+int(perUnit("30")), after.Quota)
	// 提现到 USDT 照常人工审核。
	withdrawal, err := Referral.CreateWithdrawal(ReferralWithdrawalCreateInput{UserId: owner.Id, Amount: usd("30"), AccountType: model.ReferralWithdrawToUSDT,
		AccountNetwork: "TRC20", AccountNo: "T" + "A1B2C3D4E5F6G7H8J9K1L2M3N4P5Q6R7S", IdempotencyKey: "ot-2"})
	require.NoError(t, err)
	assert.Equal(t, model.ReferralWithdrawalStatusPending, withdrawal.Status)
}

func TestForcedCloseDoesNotVoidOrClawBack(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "clawback")
	customer := siteCustomer(t, db, site, "clawback-customer")
	sitePrice(t, db, site.Id, 0, "", "1.25")
	saved := adtySiteEarningDayFn
	t.Cleanup(func() { adtySiteEarningDayFn = saved })
	adtySiteEarningDayFn = func() string { return "2026-09-01" }
	consumeOnSite(t, customer, perUnit("100")) // +$20 settled immediately
	adtySiteEarningDayFn = func() string { return "2026-10-01" }
	consumeOnSite(t, customer, perUnit("50")) // +$10 settled immediately
	_, err := ScheduleAdtySiteClose(site.Id, 1, AdtySiteClosePlan{ClawBack: true, Immediate: true})
	require.NoError(t, err)
	affiliate, err := Referral.EnsureAffiliate(owner.Id)
	require.NoError(t, err)
	assert.True(t, siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(decimal.RequireFromString("30")),
		"已结算收益必须留在站点收益钱包里")
	assert.Zero(t, account(t, db, affiliate.Id).AvailableAmount,
		"站点收益不得再记进 referral 佣金账户（ISS-R4-0111）")
	assert.InDelta(t, 30, siteWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).InexactFloat64(), 1e-9,
		"forced close must not deduct settled earnings")
	var rows []model.AdtySiteEarningDaily
	require.NoError(t, db.Where("site_id = ? AND kind <> ?", site.Id, model.AdtySiteEarningTrial).Order("id").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.Equal(t, model.AdtySiteEarningAvailable, rows[0].Status)
	assert.True(t, rows[0].Earning.Equal(decimal.RequireFromString("20")))
	assert.Equal(t, model.AdtySiteEarningAvailable, rows[1].Status)
	assert.True(t, rows[1].Earning.Equal(decimal.RequireFromString("10")))
	var voided, clawLedgers int64
	require.NoError(t, db.Model(&model.AdtySiteEarningDaily{}).Where("site_id = ? AND status = ?", site.Id, model.AdtySiteEarningVoid).Count(&voided).Error)
	assert.Zero(t, voided)
	require.NoError(t, db.Model(&model.ReferralCommissionLedger{}).Where("affiliate_id = ? AND type = ?", affiliate.Id, "site_earning_clawback").Count(&clawLedgers).Error)
	assert.Zero(t, clawLedgers)
	closed, err := GetAdtySite(site.Id)
	require.NoError(t, err)
	assert.Equal(t, model.AdtySiteStatusClosed, closed.Status)
	assert.False(t, closed.CloseClawBack, "ClawBack on the close plan must be ignored")

	// 已结算 100、未结算加赠 -150、可提现仍是 100：关站不得当场结算负数，也不得扣回已结算佣金。
	owner2, site2 := openActiveSite(t, db, "claw-debt")
	customer2 := siteCustomer(t, db, site2, "claw-debt-customer")
	sitePrice(t, db, site2.Id, 0, "", "1.25")
	adtySiteEarningDayFn = func() string { return "2026-09-01" }
	consumeOnSite(t, customer2, perUnit("500")) // k 1.25，d 0.8 → +$100
	adtySiteEarningDayFn = func() string { return "2026-10-02" }
	siteTopUp(t, customer2, site2, "EPU-claw-debt", 1500, "10") // bonus −$150，还在 pending
	affiliate2, err := Referral.EnsureAffiliate(owner2.Id)
	require.NoError(t, err)
	assert.True(t, siteWalletBalance(t, db, site2.Id, model.AdtySiteWalletEarnings).Equal(decimal.RequireFromString("100")))
	assert.Zero(t, account(t, db, affiliate2.Id).AvailableAmount,
		"站点收益不得再记进 referral 佣金账户（ISS-R4-0111）")
	_, err = ScheduleAdtySiteClose(site2.Id, 1, AdtySiteClosePlan{ClawBack: true, Immediate: true})
	require.NoError(t, err)
	assert.True(t, siteWalletBalance(t, db, site2.Id, model.AdtySiteWalletEarnings).Equal(decimal.RequireFromString("100")),
		"forced close must not deduct settled earnings")
	var bonus, usage model.AdtySiteEarningDaily
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site2.Id, model.AdtySiteEarningBonus).First(&bonus).Error)
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site2.Id, model.AdtySiteEarningUsage).First(&usage).Error)
	assert.Equal(t, model.AdtySiteEarningPending, bonus.Status, "unsettled bonus must remain pending under the original source")
	assert.True(t, bonus.Earning.Equal(decimal.RequireFromString("-150")))
	assert.Equal(t, model.AdtySiteEarningAvailable, usage.Status)
	assert.True(t, usage.Earning.Equal(decimal.RequireFromString("100")))
	require.NoError(t, db.Model(&model.AdtySiteEarningDaily{}).Where("site_id = ? AND status = ?", site2.Id, model.AdtySiteEarningVoid).Count(&voided).Error)
	assert.Zero(t, voided)
	require.NoError(t, db.Model(&model.ReferralCommissionLedger{}).Where("affiliate_id = ? AND type = ?", affiliate2.Id, "site_earning_clawback").Count(&clawLedgers).Error)
	assert.Zero(t, clawLedgers)
}

func TestEarningHookConcurrentUpsert(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "concurrent")
	customer := siteCustomer(t, db, site, "concurrent-customer")
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			AdtySiteEarningHook(site.Id, customer.Id, perUnit("1"), 1.25, 0.8, model.AdtySiteEarningUsage)
		})
	}
	wg.Wait()
	var row model.AdtySiteEarningDaily
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteEarningUsage).First(&row).Error)
	assert.EqualValues(t, perUnit("50"), row.Quota)
	assert.True(t, row.Earning.Equal(usd("10")), "50 × $0.2, got %s", row.Earning)
	assert.True(t, row.Revenue.Equal(usd("50")))
	var rows int64
	require.NoError(t, db.Model(&model.AdtySiteEarningDaily{}).Where("site_id = ?", site.Id).Count(&rows).Error)
	assert.EqualValues(t, 1, rows)
}

// 上游的任务退款测试在 SQLite 上跑：带分站快照的任务走到记账钩子时，SQLite 也要能写。
func TestEarningHookOnSQLite(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(model.AdtySiteTables()...))
	seedUser(t, 1, 0)
	require.NoError(t, model.DB.Create(&model.AdtySite{Id: 3, OwnerUserId: 1, Subdomain: "sqlite-earning", Status: model.AdtySiteStatusActive}).Error)
	require.NoError(t, model.DB.Create(&model.User{Id: 4, Username: "sqlite-customer", SiteId: 3}).Error)
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM adty_site_earning_daily")
		model.DB.Exec("DELETE FROM adty_site_customers")
		model.DB.Exec("DELETE FROM adty_site_wallets WHERE site_id = 3")
		model.DB.Exec("DELETE FROM adty_sites WHERE id = 3")
	})
	for range 2 {
		AdtySiteEarningHook(3, 4, perUnit("1"), 1.25, 0.8, model.AdtySiteEarningUsage)
	}
	var row model.AdtySiteEarningDaily
	require.NoError(t, model.DB.Where("site_id = 3").First(&row).Error)
	assert.Equal(t, 1, row.SiteOwnerUserId)
	assert.EqualValues(t, perUnit("2"), row.Quota)
	assert.True(t, row.Earning.Equal(usd("0.4")))
}

func TestReviewedSkippedDoesNotHoldSettlement(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "review-hold")
	recordFingerprint(t, db, owner.Id, "203.0.113.20", "owner-device-hold")
	customer := registerSiteCustomer(t, db, site, "review-hold-customer", "203.0.113.20", "other")
	saved := adtySiteEarningDayFn
	t.Cleanup(func() { adtySiteEarningDayFn = saved })
	adtySiteEarningDayFn = func() string { return "2026-09-01" }
	consumeOnSite(t, customer, perUnit("10"))
	require.NoError(t, ReviewAdtySiteCustomer(customer.Id, 1, false))

	_, err := RunAdtySiteSettlementOnce(time.Date(2026, 10, 20, 4, 0, 0, 0, time.Local))
	require.NoError(t, err)
	var stored model.AdtySite
	require.NoError(t, db.First(&stored, site.Id).Error)
	assert.Empty(t, stored.SettleHoldReason)
	var usage model.AdtySiteEarningDaily
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteEarningUsage).First(&usage).Error)
	assert.Equal(t, model.AdtySiteEarningAvailable, usage.Status)
}

func TestSettlementHoldsWhenUsageDoesNotMatchLogs(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "mismatch")
	customer := siteCustomer(t, db, site, "mismatch-customer")
	consumeOnSite(t, customer, perUnit("10"))
	require.NoError(t, db.Model(&model.AdtySiteEarningDaily{}).Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteEarningUsage).
		Update("earning", usd("99")).Error)

	due := time.Now().AddDate(0, 0, 10)
	settled, err := RunAdtySiteSettlementOnce(due)
	require.NoError(t, err)
	assert.Zero(t, settled)
	var stored model.AdtySite
	require.NoError(t, db.First(&stored, site.Id).Error)
	assert.Contains(t, stored.SettleHoldReason, "do not match consume logs")

	resumed, err := ResumeAdtySiteSettlement(site.Id, 1, due)
	require.NoError(t, err)
	assert.Contains(t, resumed.SettleHoldReason, "do not match consume logs", "resume checks the numbers again")
}

func TestEarningHookFailureIncrementsCounter(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "hook-fail")
	customer := siteCustomer(t, db, site, "hook-fail-customer")
	AdtySiteEarningHook(site.Id, customer.Id, perUnit("1"), 1.25, 0.8, strings.Repeat("x", 40))
	var stored model.AdtySite
	require.NoError(t, db.First(&stored, site.Id).Error)
	assert.EqualValues(t, 1, stored.EarningHookFailures)
}

func TestEarningHookBooksWhenKBelowOneOrDAboveOne(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "k-d-bounds")
	customer := siteCustomer(t, db, site, "k-d-bounds-customer")

	// k=0.8：售价低于供货成本，价差为负。
	AdtySiteEarningHook(site.Id, customer.Id, perUnit("80"), 0.8, 0.5, model.AdtySiteEarningUsage)
	assert.True(t, earningTotals(t, db, site.Id)[model.AdtySiteEarningUsage].Equal(usd("-20")))

	// d=1.5 以前会被直接跳过；d 已含在实扣 q 里，按 k=1.25 记 $10。
	AdtySiteEarningHook(site.Id, customer.Id, perUnit("50"), 1.25, 1.5, model.AdtySiteEarningUsage)
	assert.True(t, earningTotals(t, db, site.Id)[model.AdtySiteEarningUsage].Equal(usd("-10")))

	var before int64
	require.NoError(t, db.Model(&model.AdtySiteEarningDaily{}).Where("site_id = ?", site.Id).Count(&before).Error)
	AdtySiteEarningHook(site.Id, customer.Id, perUnit("50"), 0, 1.5, model.AdtySiteEarningUsage)
	AdtySiteEarningHook(site.Id, customer.Id, perUnit("50"), 1.25, 0, model.AdtySiteEarningUsage)
	var after int64
	require.NoError(t, db.Model(&model.AdtySiteEarningDaily{}).Where("site_id = ?", site.Id).Count(&after).Error)
	assert.Equal(t, before, after, "non-positive k or d still does not book")
}

func TestBillingSettlementBooksEarningsWhenKBelowOneOrDAboveOne(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "bill-k-d-bounds")
	customer := siteCustomer(t, db, site, "bill-k-d-bounds-customer")
	require.NoError(t, db.Model(customer).Update("quota", perUnit("400")).Error)

	// k=0.8 was previously blocked by SiteMarkup>=1; d=1.5 was blocked by SiteCostFactor<=1.
	cases := []struct {
		name, key string
		k, d      float64
		charge    string
	}{
		{name: "k below one", key: "direct:k-below-one", k: 0.8, d: 0.5, charge: "80"},
		{name: "d above one", key: "direct:d-above-one", k: 1.25, d: 1.5, charge: "50"},
		{name: "k below one and d above one", key: "direct:k-and-d-out-of-old-range", k: 0.8, d: 1.5, charge: "80"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, model.QueueDirectBillingDelta(model.BillingOperation{
				OperationKey: tc.key, UserId: customer.Id, SiteId: site.Id,
				SiteMarkup: tc.k, SiteCostFactor: tc.d,
			}, int(perUnit(tc.charge))))
			_, _, err := model.ApplyBillingSettlement(tc.key)
			require.NoError(t, err)
			var event model.AdtySiteEarningEvent
			require.NoError(t, db.Where("operation_key = ?", tc.key).First(&event).Error)
			assert.Equal(t, site.Id, event.SiteId)
			assert.Equal(t, customer.Id, event.UserId)
			assert.InDelta(t, tc.k, event.Markup, 1e-9)
			assert.InDelta(t, tc.d, event.CostFactor, 1e-9)
			assert.EqualValues(t, perUnit(tc.charge), event.Quota)
		})
	}

	var before int64
	require.NoError(t, db.Model(&model.AdtySiteEarningEvent{}).Where("site_id = ?", site.Id).Count(&before).Error)
	require.NoError(t, model.QueueDirectBillingDelta(model.BillingOperation{
		OperationKey: "direct:k-zero", UserId: customer.Id, SiteId: site.Id,
		SiteMarkup: 0, SiteCostFactor: 1.5,
	}, int(perUnit("10"))))
	_, _, err := model.ApplyBillingSettlement("direct:k-zero")
	require.NoError(t, err)
	require.NoError(t, model.QueueDirectBillingDelta(model.BillingOperation{
		OperationKey: "direct:d-zero", UserId: customer.Id, SiteId: site.Id,
		SiteMarkup: 1.25, SiteCostFactor: 0,
	}, int(perUnit("10"))))
	_, _, err = model.ApplyBillingSettlement("direct:d-zero")
	require.NoError(t, err)
	var after int64
	require.NoError(t, db.Model(&model.AdtySiteEarningEvent{}).Where("site_id = ?", site.Id).Count(&after).Error)
	assert.Equal(t, before, after, "non-positive k or d still must not book an earning event")
}

func TestMidjourneyRefundKeepsSnapshotWhenKBelowOneOrDAboveOne(t *testing.T) {
	db := prepareSiteDB(t)
	require.NoError(t, db.AutoMigrate(&model.Midjourney{}, &model.Channel{}))
	_, site := openActiveSite(t, db, "mj-k-d-snap")
	customer := siteCustomer(t, db, site, "mj-k-d-snap-customer")
	task := &model.Midjourney{
		UserId: customer.Id, MjId: "mj-k-d-snap", Action: "IMAGINE", Status: "FAILURE", SubmitTime: time.Now().Unix(),
		Quota: int(perUnit("80")), BillingSiteId: site.Id, BillingSiteMarkup: 0.8, BillingSiteCostFactor: 1.5,
	}
	require.NoError(t, db.Create(task).Error)
	require.NoError(t, ReviewAdtySiteCustomer(customer.Id, 1, true))
	require.True(t, RefundMidjourneyQuota(context.Background(), task, "upstream failed"))

	var event model.AdtySiteEarningEvent
	require.NoError(t, db.Where("operation_key = ?", fmt.Sprintf("mj:%d:refund", task.Id)).First(&event).Error)
	assert.InDelta(t, 0.8, event.Markup, 1e-9)
	assert.InDelta(t, 1.5, event.CostFactor, 1e-9)
	assert.EqualValues(t, -perUnit("80"), event.Quota)

	var log model.Log
	require.NoError(t, db.Where("user_id = ? AND type = ?", customer.Id, model.LogTypeRefund).First(&log).Error)
	assert.Contains(t, log.Other, `"site_markup":0.8`, "refund log must keep a k<1 submit snapshot")
	assert.Contains(t, log.Other, `"site_cost_factor":1.5`, "refund log must keep a d>1 submit snapshot")
}

// TestTaskEarningOwnerSnapshotFailureIsCounted：任务差额结算解析收益归属失败时，日志要带站点与用户，
// 并且和 AdtySiteEarningHook 里同一失败语义一样计入 earning_hook_failures——否则这笔漏记的收益
// 既查不到也进不了日结算对账（adty_site_settlement_task 只捞 earning_hook_failures > 0 的站）。
func TestTaskEarningOwnerSnapshotFailureIsCounted(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "task-owner-failure")
	customer := siteCustomer(t, db, site, "task-owner-failure-customer")
	// 归属解析读的是 adty_site_ownerships；把这张表拿掉，site 行本身还在，计数才有落点。
	require.NoError(t, db.Migrator().DropTable(&model.AdtySiteOwnership{}))
	logs := logsinktest.Capture(t)
	other := model.NewLogOther()
	other.SetAdmin("site_id", site.Id)
	other.SetAdmin("site_markup", 1.25)
	other.SetAdmin("site_cost_factor", 0.8)
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{UserId: customer.Id, LogType: model.LogTypeConsume, Quota: int(perUnit("125")), Other: other})
	var stored model.AdtySite
	require.NoError(t, db.First(&stored, site.Id).Error)
	assert.EqualValues(t, 1, stored.EarningHookFailures, "解析失败要计数，让这个站进对账")
	text := logs.String()
	assert.Contains(t, text, "task earning owner snapshot failed")
	assert.Contains(t, text, fmt.Sprintf("site=%d", site.Id))
	assert.Contains(t, text, fmt.Sprintf("user=%d", customer.Id))
	assert.Contains(t, text, "kind="+model.AdtySiteEarningUsage)
	assert.Empty(t, earningTotals(t, db, site.Id), "解析失败不能凭空写收益行")
}

func TestDurableRequestAndTaskRecordModelGroupEarningLinesWithoutChannelDisclosure(t *testing.T) {
	db := prepareSiteDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.Channel{}, &model.Task{}, &model.Midjourney{}, &model.AdtySiteEarningLine{}))
	_, site := openActiveSite(t, db, "durable-lines")
	customer := siteCustomer(t, db, site, "durable-lines-customer")
	sitePrice(t, db, site.Id, 0, "", "1.25")
	require.NoError(t, db.Model(customer).Update("quota", perUnit("500")).Error)

	// 1. Durable request settlement + consume log records exactly one model/group detail line.
	c := adtyWalletRelayContext()
	c.Set(common.RequestIdKey, "req-durable-line-1")
	AdtyApplySiteMarkup(c, customer.Id, "vip", 1, "gpt-4o")
	info := adtyWalletRelayInfo(customer.Id, 0, "")
	info.IsPlayground = true
	info.OriginModelName = "gpt-4o"
	info.RequestId = "req-durable-line-1"
	info.PriceData.ModelRatio = 1
	info.UsingGroup = "vip"
	info.UserGroup = "vip"
	require.Nil(t, PreConsumeBilling(c, int(perUnit("125")), info))
	require.NoError(t, info.Billing.Settle(int(perUnit("125"))))
	model.RecordConsumeLog(c, customer.Id, model.RecordConsumeLogParams{
		ChannelId: 77, ModelName: "gpt-4o", Quota: int(perUnit("125")), Group: "vip",
	})

	// 2. Durable task settlement + task refund also record model/group detail lines.
	cTask := adtyWalletRelayContext()
	AdtyApplySiteMarkup(cTask, customer.Id, "pro", 1, "sora-video")
	taskInfo := adtyWalletRelayInfo(customer.Id, 0, "")
	taskInfo.IsPlayground = true
	taskInfo.OriginModelName = "sora-video"
	taskInfo.PriceData.ModelRatio = 1
	taskInfo.UsingGroup = "pro"
	require.Nil(t, PreConsumeBilling(cTask, int(perUnit("50")), taskInfo))
	task := &model.Task{
		TaskID: model.GenerateTaskID(), UserId: customer.Id, ChannelId: 88, Group: "pro",
		Quota: int(perUnit("50")), Status: model.TaskStatusInProgress,
		Properties: model.Properties{OriginModelName: "sora-video"},
	}
	task.PrivateData.BillingContext = &model.TaskBillingContext{
		OriginModelName: "sora-video",
		SiteId:          site.Id, SiteMarkup: 1.25, SiteCostFactor: 0.8,
	}
	AttachTaskBillingReservation(taskInfo, task)
	require.NoError(t, task.Insert())
	require.NoError(t, taskInfo.Billing.Settle(int(perUnit("50"))))
	require.True(t, RefundTaskQuota(context.Background(), task, "upstream failed"))

	lines, err := ListAdtyOwnerEarningLines(site.Id, 0, 20)
	require.NoError(t, err)
	require.Len(t, lines, 3, "expected 1 request line, 1 task settle line, and 1 task refund line")

	var reqLine *model.AdtySiteEarningLine
	for i := range lines {
		if lines[i].RequestId == "req-durable-line-1" {
			reqLine = &lines[i]
		}
	}
	require.NotNil(t, reqLine)
	assert.Equal(t, "gpt-4o", reqLine.ModelName)
	assert.Equal(t, "vip", reqLine.Group)
	assert.EqualValues(t, perUnit("125"), reqLine.Quota)
	assert.True(t, reqLine.Revenue.Equal(usd("125")))
	assert.True(t, reqLine.Platform.Equal(usd("100")))
	assert.True(t, reqLine.Earning.Equal(usd("25")))

	rawJSON, err := common.Marshal(lines)
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(rawJSON)), "channel", "earning detail lines must not disclose upstream channel fields")
}

func TestSiteEarningBalancesIgnorePersonalReferralPG(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "wallet-report")
	_, other := openActiveSite(t, db, "wallet-report-other")
	affiliate, err := Referral.EnsureAffiliate(owner.Id)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.ReferralCommissionAccount{}).Where("affiliate_id = ?", affiliate.Id).Updates(map[string]any{
		"available_amount": 999, "frozen_amount": 888, "withdrawn_amount": 777,
	}).Error)
	require.NoError(t, db.Model(&model.AdtySiteWallet{}).Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).
		Updates(map[string]any{"balance": 50, "frozen_usd": 12}).Error)
	for i, row := range []model.AdtySiteWithdrawal{
		{SiteId: site.Id, AmountUSD: usd("20"), Status: model.AdtySiteWithdrawPaid},
		{SiteId: site.Id, AmountUSD: usd("12"), Status: model.AdtySiteWithdrawPending},
		{SiteId: site.Id, AmountUSD: usd("9"), Status: model.AdtySiteWithdrawRejected},
		{SiteId: other.Id, AmountUSD: usd("999"), Status: model.AdtySiteWithdrawPaid},
	} {
		row.IdempotencyKey = fmt.Sprintf("wallet-report-%d", i)
		require.NoError(t, db.Create(&row).Error)
	}
	result, err := GetAdtySiteBalances(site)
	require.NoError(t, err)
	require.Equal(t, "38", result.AvailableUSD.String())
	require.Equal(t, "12", result.FrozenUSD.String())
	require.Equal(t, "20", result.WithdrawnUSD.String())
	require.NoError(t, db.Where("user_id = ?", owner.Id).Delete(&model.ReferralCommissionAccount{}).Error)
	require.NoError(t, db.Model(&model.AdtySiteWallet{}).Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).
		Updates(map[string]any{"balance": -5, "frozen_usd": 2}).Error)
	result, err = GetAdtySiteBalances(site)
	require.NoError(t, err)
	require.Equal(t, "-7", result.AvailableUSD.String())
	require.Equal(t, "2", result.FrozenUSD.String())
	require.Equal(t, "20", result.WithdrawnUSD.String())
}

func TestDurableEarningLinesPreserveFrozenIdentityAndAtomicityPG(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "durable-line-facts")
	user := siteCustomer(t, db, site, "durable-line-facts-user")
	snapshot, err := model.EncodeRequestPriceSnapshot(&model.RequestPriceSnapshot{
		SchemaVersion: model.RequestPriceSnapshotVersion, ModelName: "frozen-model", ModelBasePrice: "1", BillingGroup: "frozen-group",
		GroupRatio: "1", SiteCostFactor: "1", SiteMarkupRatio: "0.5", BilledGroupRatio: "1.2",
		TeamApplied: true, TeamPayRatio: "0.8", SupplyPriceFloor: "1", FinalPrice: "1.2",
		Currency: model.RequestPriceCurrencyUSD, BillingUnit: model.BillingUnitPerCall, AcceptedAt: 1_794_787_200,
		RuleVersion: model.RequestPriceSnapshotVersion, SupplyCost: "1", SellPrice: "1.5",
	})
	require.NoError(t, err)
	require.NotNil(t, model.DecodeRequestPriceSnapshot(snapshot))
	for _, op := range []struct {
		key   string
		quota int
		kind  string
	}{
		{"request:frozen-line", 60_000_000, "request"}, {"direct:frozen-refund", -30_000_000, "direct"},
	} {
		require.NoError(t, db.Create(&model.BillingOperation{OperationKey: op.key, UserId: user.Id, SiteId: site.Id, Kind: op.kind, State: model.BillingSettled, PriceSnapshot: snapshot}).Error)
		require.NoError(t, db.Create(&model.AdtySiteEarningEvent{OperationKey: op.key, SiteId: site.Id, SiteOwnerUserId: site.OwnerUserId,
			UserId: user.Id, Day: "2026-11-19", Quota: op.quota, Markup: 1.5, CostFactor: 1, CreatedAt: 1_794_787_200}).Error)
		for range 2 {
			require.NoError(t, projectAdtySiteEarningEvent(context.Background(), op.key))
		}
	}
	lines, err := ListAdtyOwnerEarningLines(site.Id, 0, 20)
	require.NoError(t, err)
	require.Len(t, lines, 2)
	for _, line := range lines {
		require.Equal(t, "frozen-model", line.ModelName)
		require.Equal(t, "frozen-group", line.Group)
		require.EqualValues(t, 1_794_787_200, line.CreatedAt)
		if line.Quota > 0 {
			require.Equal(t, "120", line.Revenue.String())
			require.Equal(t, "100", line.Platform.String())
			require.Equal(t, "20", line.Earning.String())
		} else {
			require.Equal(t, "-60", line.Revenue.String())
			require.Equal(t, "-50", line.Platform.String())
			require.Equal(t, "-10", line.Earning.String())
		}
	}
	raw, err := common.Marshal(lines)
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(string(raw)), "channel")
	// A detail projection failure must also roll back the daily and wallet writes.
	key := "request:line-rollback"
	require.NoError(t, db.Create(&model.BillingOperation{OperationKey: key, UserId: user.Id, SiteId: site.Id, Kind: "request", PriceSnapshot: snapshot}).Error)
	require.NoError(t, db.Create(&model.AdtySiteEarningEvent{OperationKey: key, SiteId: site.Id, UserId: user.Id, Day: "2026-11-19", Quota: 60_000_000, Markup: 1.5, CostFactor: 1}).Error)
	before := snapshotSiteWallets(t, db, site.Id)
	const callback = "test:durable-line-rollback"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "adty_site_earning_lines" {
			tx.AddError(fmt.Errorf("detail unavailable"))
		}
	}))
	t.Cleanup(func() { db.Callback().Create().Remove(callback) })
	require.Error(t, projectAdtySiteEarningEvent(context.Background(), key))
	require.Equal(t, before, snapshotSiteWallets(t, db, site.Id))
	var pending model.AdtySiteEarningEvent
	require.NoError(t, db.Where("operation_key = ?", key).First(&pending).Error)
	require.False(t, pending.Projected)
	require.NoError(t, db.Callback().Create().Remove(callback))
	errors := make(chan error, 6)
	var workers sync.WaitGroup
	for range 6 {
		workers.Go(func() { errors <- projectAdtySiteEarningEvent(context.Background(), key) })
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	lines, err = ListAdtyOwnerEarningLines(site.Id, 0, 20)
	require.NoError(t, err)
	require.Len(t, lines, 3)
	// An intentionally unnamed accepted group must not become the task's later group.
	require.NoError(t, db.AutoMigrate(&model.Task{}))
	task := &model.Task{TaskID: model.GenerateTaskID(), UserId: user.Id, Group: "later-group"}
	require.NoError(t, db.Create(task).Error)
	unnamed := model.DecodeRequestPriceSnapshot(snapshot)
	unnamed.BillingGroup = ""
	emptyGroupSnapshot, err := model.EncodeRequestPriceSnapshot(unnamed)
	require.NoError(t, err)
	key = "task:unnamed-frozen-line"
	require.NoError(t, db.Create(&model.BillingOperation{OperationKey: key, UserId: user.Id, SiteId: site.Id, Kind: "task", ReferenceId: task.ID, PriceSnapshot: emptyGroupSnapshot}).Error)
	require.NoError(t, db.Create(&model.AdtySiteEarningEvent{OperationKey: key, SiteId: site.Id, UserId: user.Id, Day: "2026-11-19", Quota: 60_000_000, Markup: 1.5, CostFactor: 1}).Error)
	require.NoError(t, projectAdtySiteEarningEvent(context.Background(), key))
	lines, err = ListAdtyOwnerEarningLines(site.Id, 0, 20)
	require.NoError(t, err)
	require.Len(t, lines, 4)
	require.Empty(t, lines[0].Group)
	require.Equal(t, "frozen-model", lines[0].ModelName)
}

func TestDurableEarningLinesRealtimeAndMidjourneyRecoveryPG(t *testing.T) {
	db := prepareSiteDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.Channel{}, &model.Task{}, &model.Midjourney{}))
	_, site := openActiveSite(t, db, "line-stream-mj")
	user := siteCustomer(t, db, site, "line-stream-mj-user")
	require.NoError(t, db.Model(user).Update("quota", 200_000_000).Error)
	sitePrice(t, db, site.Id, 0, "", "1.25")
	seedToken(t, 9997, user.Id, "line-stream", 200_000_000)
	savedLogs := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = savedLogs })
	c := adtyWalletRelayContext()
	AdtyApplySiteMarkup(c, user.Id, "default", 1, "gpt-4o-realtime-preview")
	info := adtyWalletRelayInfo(user.Id, 9997, "sk-line-stream")
	info.OriginModelName, info.UsingGroup, info.UserGroup = "gpt-4o-realtime-preview", "default", "default"
	info.PriceData.ModelRatio = 1
	require.Nil(t, PreConsumeBilling(c, 0, info))
	usage := &dto.RealtimeUsage{InputTokens: 200, OutputTokens: 100, TotalTokens: 300}
	usage.InputTokenDetails.TextTokens, usage.OutputTokenDetails.TextTokens = 200, 100
	require.NoError(t, PreWssConsumeQuota(c, info, usage))
	require.Positive(t, info.RealtimeAlreadyCharged)
	require.NoError(t, SettleBilling(c, info, 0))
	require.NoError(t, RecoverBillingOperations(context.Background(), 100))
	lines, err := ListAdtyOwnerEarningLines(site.Id, 0, 50)
	require.NoError(t, err)
	require.Len(t, lines, 2, "stream charge and final refund each retain one fact")
	var netQuota int64
	netRevenue, netPlatform, netEarning := decimal.Zero, decimal.Zero, decimal.Zero
	for _, line := range lines {
		require.Equal(t, "gpt-4o-realtime-preview", line.ModelName)
		require.Equal(t, "default", line.Group)
		netQuota += line.Quota
		netRevenue = netRevenue.Add(line.Revenue)
		netPlatform = netPlatform.Add(line.Platform)
		netEarning = netEarning.Add(line.Earning)
	}
	require.Zero(t, netQuota)
	require.True(t, netRevenue.IsZero())
	require.True(t, netPlatform.IsZero())
	require.True(t, netEarning.IsZero())
	var original model.BillingOperation
	require.NoError(t, db.Where("operation_key = ?", "request:"+info.RequestId).First(&original).Error)
	require.NotNil(t, original.RequestPriceSnapshot())
	mjSnapshot := original.RequestPriceSnapshot()
	mjSnapshot.ModelName, mjSnapshot.BillingGroup = "frozen-mj-model", "frozen-mj-group"
	encoded, err := model.EncodeRequestPriceSnapshot(mjSnapshot)
	require.NoError(t, err)
	task := &model.Midjourney{UserId: user.Id, MjId: "line-mj", Action: "IMAGINE", Quota: 5_000_000,
		BillingSiteId: site.Id, BillingSiteOwnerUserId: site.OwnerUserId, BillingSiteMarkup: 1.25, BillingSiteCostFactor: .8,
		BillingPriceSnapshot: encoded, BillingChargePending: true, BillingOperationKey: "mj:line-earning-recovery"}
	require.NoError(t, db.Create(task).Error)
	for _, refund := range []bool{false, true} {
		key, err := model.QueueMidjourneyBillingOperation(task, refund)
		require.NoError(t, err)
		op, applied, err := model.ApplyBillingSettlement(key)
		require.NoError(t, err)
		require.True(t, applied)
		if refund {
			require.Equal(t, model.BillingRefunded, op.State)
			require.Zero(t, op.AppliedQuota)
		} else {
			require.Equal(t, model.BillingSettled, op.State)
			require.Equal(t, 5_000_000, op.AppliedQuota)
		}
		for range 2 {
			require.NoError(t, projectAdtySiteEarningEvent(context.Background(), key))
		}
	}
	lines, err = ListAdtyOwnerEarningLines(site.Id, 0, 50)
	require.NoError(t, err)
	require.Len(t, lines, 4)
	for _, line := range lines[:2] {
		require.Equal(t, "frozen-mj-model", line.ModelName)
		require.Equal(t, "frozen-mj-group", line.Group)
	}
	require.Zero(t, lines[0].Quota+lines[1].Quota)
	require.True(t, lines[0].Revenue.Add(lines[1].Revenue).IsZero())
	require.True(t, lines[0].Platform.Add(lines[1].Platform).IsZero())
	require.True(t, lines[0].Earning.Add(lines[1].Earning).IsZero())
	var logs int64
	require.NoError(t, db.Model(&model.Log{}).Where("user_id = ?", user.Id).Count(&logs).Error)
	require.Zero(t, logs)
}
