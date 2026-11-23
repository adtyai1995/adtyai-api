package service

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFinanceBoundaries(t *testing.T) {
	dsn := os.Getenv("ADTY_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL 18 via ADTY_TEST_PG_DSN")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	pool, err := admin.DB()
	require.NoError(t, err)
	defer pool.Close()
	var version int
	require.NoError(t, admin.Raw("SHOW server_version_num").Scan(&version).Error)
	require.GreaterOrEqual(t, version, 180000)
	require.Less(t, version, 190000)
	t.Logf("server_version_num=%d", version)
	schema := fmt.Sprintf("finance_boundaries_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	defer admin.Exec("DROP SCHEMA " + schema + " CASCADE")
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		require.NoError(t, err)
		q := u.Query()
		q.Set("search_path", schema)
		q.Set("client_encoding", "UTF8")
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " client_encoding=UTF8 search_path=" + schema
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	pg, err := db.DB()
	require.NoError(t, err)
	defer pg.Close()

	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	defer func() { common.RedisEnabled = previousRedis }()
	previousDB, previousType := model.DB, common.MainDatabaseType()
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	defer func() { model.DB = previousDB; common.SetMainDatabaseType(previousType) }()
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.TopUp{}, &model.Token{}))
	require.NoError(t, model.MigrateAdtyTablesOn(db))
	site := model.AdtySite{OwnerUserId: 20, Subdomain: "finance", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)
	usd := decimal.NewFromInt
	t.Run("historical owner wallet reconciliation and payout", func(t *testing.T) {
		const oldOwner, currentOwner = 701, 702
		s := model.AdtySite{OwnerUserId: currentOwner, Subdomain: "mixed-archive", Status: model.AdtySiteStatusClosed}
		require.NoError(t, db.Create(&s).Error)
		require.NoError(t, db.Create(&model.AdtySiteOwnership{SiteId: s.Id, OldOwnerUserId: oldOwner, NewOwnerUserId: currentOwner, Version: 1}).Error)
		require.True(t, AdtyHasArchivedSite(oldOwner))
		// Late business settles on the original site after the former owner opens another.
		require.NoError(t, db.Create(&model.AdtySite{OwnerUserId: oldOwner, Subdomain: "reopened-site", Status: model.AdtySiteStatusActive}).Error)
		for _, fact := range []struct {
			owner, user int
			earn        int64
		}{{0, 5, 12}, {oldOwner, 6, 8}, {currentOwner, 7, 11}} {
			require.NoError(t, upsertAdtySiteEarningTx(db, "2026-11-12", adtySiteEarningEntry{SiteId: s.Id, SiteOwnerUserId: fact.owner, UserId: fact.user, Kind: model.AdtySiteEarningUsage, Earning: usd(fact.earn)}))
		}
		require.NoError(t, db.Model(&model.AdtySiteEarningDaily{}).Where("site_id = ? AND user_id = ?", s.Id, 5).Update("site_owner_user_id", 0).Error)
		for _, owner := range []int{oldOwner, currentOwner} {
			balances, err := GetAdtyOwnerBalances(&s, owner)
			require.NoError(t, err)
			require.False(t, balances.RequiresReview)
			require.Equal(t, map[int]string{oldOwner: "20", currentOwner: "11"}[owner], balances.AvailableUSD.String())
		}
		for key, value := range map[string]string{adtySiteWithdrawMinKey: "1", adtySiteWithdrawFeeKey: "1", adtySiteWithdrawAlipayKey: "7", adtySiteWithdrawUSDTKey: "1"} {
			require.NoError(t, db.Save(&model.Option{Key: key, Value: value}).Error)
		}
		withdraw := func(owner int, amount int64, key string) (*model.AdtySiteWithdrawal, error) {
			return CreateAdtySiteWithdrawal(AdtySiteWithdrawInput{SiteID: s.Id, OwnerID: owner, Method: "alipay", AccountNo: "123", AccountName: "Payee", AmountUSD: usd(amount), ExpectedRate: usd(7), IdempotencyKey: key})
		}
		_, err := withdraw(currentOwner, 12, "new-owner-overdraw")
		require.ErrorIs(t, err, ErrAdtySiteWalletInsufficient)
		old, err := withdraw(oldOwner, 15, "old-owner")
		require.NoError(t, err)
		require.NoError(t, db.Model(old).Update("owner_user_id", 0).Error)
		require.Error(t, CancelAdtySiteWithdrawal(s.Id, currentOwner, old.Id))
		require.NoError(t, MarkAdtySiteWithdrawalPaying(old.Id, 1))
		require.NoError(t, MarkAdtySiteWithdrawalPaid(old.Id, 1, "payout", "receipt", 1))
		for _, owner := range []int{oldOwner, currentOwner} {
			balances, err := GetAdtyOwnerBalances(&s, owner)
			require.NoError(t, err)
			require.False(t, balances.RequiresReview)
			require.Equal(t, map[int]string{oldOwner: "5", currentOwner: "11"}[owner], balances.AvailableUSD.String())
			require.Equal(t, map[int]string{oldOwner: "15", currentOwner: "0"}[owner], balances.WithdrawnUSD.String())
			list, err := ListAdtySiteWithdrawals(s.Id, owner)
			require.NoError(t, err)
			require.Len(t, list, map[int]int{oldOwner: 1, currentOwner: 0}[owner])
		}
		_, err = withdraw(currentOwner, 11, "current-owner")
		require.NoError(t, err)
		_, err = withdraw(999, 2, "other-user")
		require.Error(t, err)
		_, err = ApplyAdtySiteWalletChange(s.Id, model.AdtySiteWalletEarnings, usd(91), AdtySiteWalletChange{Type: "legacy_adjustment", IdempotencyKey: "unverified"})
		require.NoError(t, err)
		balances, err := GetAdtyOwnerBalances(&s, oldOwner)
		require.NoError(t, err)
		require.True(t, balances.RequiresReview)
		require.Equal(t, "0", balances.AvailableUSD.String())
		_, err = withdraw(oldOwner, 2, "must-reconcile")
		require.ErrorIs(t, err, ErrAdtyOwnerWalletReview)
	})
	t.Run("R23 personal commission funding and immutable payout", func(t *testing.T) {
		s := model.AdtySite{OwnerUserId: 502, Subdomain: "r23-history", Status: model.AdtySiteStatusClosed}
		require.NoError(t, db.Create(&s).Error)
		require.NoError(t, db.Create(&model.AdtySiteOwnership{SiteId: s.Id, OldOwnerUserId: 501, NewOwnerUserId: 502, Version: 1}).Error)
		require.NoError(t, upsertAdtySiteEarningTx(db, "2026-11-22", adtySiteEarningEntry{SiteId: s.Id, SiteOwnerUserId: 501, UserId: 10, Kind: model.AdtySiteEarningUsage, Earning: usd(100)}))
		require.NoError(t, db.Create(&model.AdtyTopupOrder{TradeNo: "r23-new-owner", SiteId: s.Id, SiteOwnerUserId: 502}).Error)
		funded, err := freezeSiteCommissionSourceTx(db, s.Id, model.AdtySiteWalletEarnings, usd(10), "r23-new-owner")
		require.NoError(t, err)
		require.True(t, funded.IsZero())
		for key, value := range map[string]string{adtySiteWithdrawMinKey: "1", adtySiteWithdrawFeeKey: "1", adtySiteWithdrawAlipayKey: "7.1234", adtySiteWithdrawUSDTKey: "1"} {
			require.NoError(t, db.Save(&model.Option{Key: key, Value: value}).Error)
		}
		q, err := QuoteSiteWithdrawal(usd(11), "alipay")
		require.NoError(t, err)
		require.Equal(t, "71.23", q.String())
		request := AdtySiteWithdrawInput{SiteID: s.Id, OwnerID: 501, AmountUSD: usd(11), ExpectedRate: decimal.RequireFromString("7.1234"), ExpectedPayout: &q, Method: "alipay", AccountName: "Tester", AccountNo: "123", IdempotencyKey: "r23-old-owner"}
		first, err := CreateAdtySiteWithdrawal(request)
		require.NoError(t, err)
		require.NoError(t, db.Save(&model.Option{Key: adtySiteWithdrawMinKey, Value: "20"}).Error)
		replay, err := CreateAdtySiteWithdrawal(request)
		require.NoError(t, err)
		require.Equal(t, first.Id, replay.Id)
		for i := 0; i < 21; i++ {
			require.NoError(t, db.Create(&model.AdtySiteWithdrawal{SiteId: s.Id, OwnerUserId: 501, Method: "alipay", Status: model.AdtySiteWithdrawRejected, IdempotencyKey: fmt.Sprintf("r23-list-%d", i)}).Error)
		}
		rows, total, err := PageAdtySiteWithdrawals(s.Id, 501, "", "", 20, 20)
		require.NoError(t, err)
		require.EqualValues(t, 22, total)
		require.Len(t, rows, 2)
		require.Equal(t, first.Id, rows[1].Id)
		rows, total, err = PageAdtySiteWithdrawals(s.Id, 502, "pending", fmt.Sprint(first.Id), 0, 20)
		require.NoError(t, err)
		require.Zero(t, total)
		require.Empty(t, rows)
	})
	t.Run("payout result unknown keeps earnings frozen", func(t *testing.T) {
		w := model.AdtySiteWithdrawal{SiteId: site.Id, OwnerUserId: 20, AmountUSD: usd(10), NetUSD: usd(10), FeeUSD: usd(0), IdempotencyKey: "F04", Method: "alipay", Status: model.AdtySiteWithdrawPending}
		require.NoError(t, db.Create(&w).Error)
		require.NoError(t, MarkAdtySiteWithdrawalPaying(w.Id, 1))
		require.NoError(t, RecallAdtySiteWithdrawalToPending(w.Id, 1, "no receipt yet", 1))
		require.Error(t, MarkAdtySiteWithdrawalPaying(w.Id, 1, 1))
		require.Error(t, RejectAdtySiteWithdrawal(w.Id, 1, "no receipt yet"))
		require.Error(t, CancelAdtySiteWithdrawal(site.Id, 20, w.Id))
		wallet := model.AdtySiteWallet{SiteId: site.Id, Kind: model.AdtySiteWalletEarnings, Currency: "USD", Balance: usd(40), FrozenUSD: usd(40)}
		require.NoError(t, db.Create(&wallet).Error)
		require.NoError(t, MarkAdtySiteWithdrawalPaid(w.Id, 1, "late-transfer", "receipt", 1))
		require.NoError(t, MarkAdtySiteWithdrawalPaid(w.Id, 1, "late-transfer", "receipt", 1))
		require.NoError(t, db.First(&wallet, wallet.Id).Error)
		require.Equal(t, "30", wallet.Balance.String())
		require.Equal(t, "30", wallet.FrozenUSD.String())
		require.Error(t, ConfirmAdtySiteWithdrawalFailed(w.Id, 1, 1, "failed", "late-failure"))
		require.Error(t, MarkAdtySiteWithdrawalPaying(w.Id, 1, 1))
		var attempts []model.AdtySitePayoutAttempt
		require.NoError(t, db.Where("withdrawal_id = ?", w.Id).Find(&attempts).Error)
		require.Len(t, attempts, 1)
		require.Equal(t, "late-transfer", attempts[0].Reference)
	})
	t.Run("spent commission cannot take later principal", func(t *testing.T) {
		u := model.User{Username: "later-topup", SiteId: site.Id, Quota: 10_000_000}
		require.NoError(t, db.Create(&u).Error)
		lot := model.AdtyFundBatch{UserId: u.Id, Source: "later-online", Kind: model.AdtyFundBatchKindPrincipal, Remaining: 10_000_000, Original: 10_000_000}
		require.NoError(t, db.Create(&lot).Error)
		row := model.AdtySiteInviteCommission{SiteId: site.Id, TradeNo: "converted-spent", InviterId: u.Id, AmountUSD: usd(4), CoveredUSD: usd(4), ConvertedUSD: usd(4), Status: model.AdtySiteCommissionAvailable}
		require.NoError(t, db.Create(&row).Error)
		ext := model.AdtyTopupOrder{SiteId: site.Id, TradeNo: row.TradeNo, FaceUSD: usd(4), CreditQuota: 2_000_000, RefundQuota: 2_000_000}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return adtySiteInviteCommissionAfterRefundTx(tx, &ext) }))
		require.NoError(t, db.First(&u, u.Id).Error)
		require.Equal(t, 10_000_000, u.Quota)
		require.NoError(t, db.First(&lot, lot.Id).Error)
		require.Equal(t, int64(10_000_000), lot.Remaining)
		var recovery model.AdtyCommissionRecovery
		require.NoError(t, db.Where("commission_id = ? AND kind = ?", row.Id, "converted").First(&recovery).Error)
		require.Equal(t, "4", recovery.AmountUSD.String())
	})
	for _, state := range []string{model.AdtySiteInviteWithdrawPending, model.AdtySiteInviteWithdrawApproved} {
		t.Run("partial refund "+state, func(t *testing.T) {

			w := model.AdtySiteInviteWithdrawal{SiteId: site.Id, UserId: 42, AmountUSD: usd(10), NetUSD: usd(10), Method: "alipay", Rate: "7", PayoutAmount: "70.00", Status: state, IdempotencyKey: "F05" + state}
			require.NoError(t, db.Create(&w).Error)
			var rows []model.AdtySiteInviteCommission
			for i, value := range []int64{4, 6} {
				row := model.AdtySiteInviteCommission{SiteId: site.Id, TradeNo: fmt.Sprintf("%s-%d", state, i), InviterId: 42, AmountUSD: usd(value), CoveredUSD: usd(value), EarningsHoldUSD: usd(value), FrozenUSD: usd(value), Status: model.AdtySiteCommissionAvailable}
				require.NoError(t, db.Create(&row).Error)
				require.NoError(t, db.Create(&model.AdtySiteInviteWithdrawalItem{WithdrawalId: w.Id, CommissionId: row.Id, AmountUSD: usd(value), Status: model.AdtySiteInviteWithdrawItemFrozen}).Error)
				rows = append(rows, row)
			}
			ext := model.AdtyTopupOrder{SiteId: site.Id, TradeNo: rows[0].TradeNo, FaceUSD: usd(8), CreditQuota: 4_000_000, RefundQuota: 2_000_000}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return adtySiteInviteCommissionAfterRefundTx(tx, &ext) }))
			require.NoError(t, db.First(&w, w.Id).Error)
			require.NoError(t, db.First(&rows[1], rows[1].Id).Error)
			require.Equal(t, "6", rows[1].FrozenUSD.String())
			if state == model.AdtySiteInviteWithdrawPending {
				require.Equal(t, "8", w.AmountUSD.String())
				require.Equal(t, "56.00", w.PayoutAmount)
				require.NoError(t, ReviewAdtySiteInviteWithdrawal(20, w.Id, true, ""))
			} else {
				require.Equal(t, "10", w.AmountUSD.String())
				require.Equal(t, "70.00", w.PayoutAmount)
			}
			require.NoError(t, MarkAdtySiteInviteWithdrawalPaid(20, w.Id, "transfer-"+state, ""))
			require.NoError(t, MarkAdtySiteInviteWithdrawalPaid(20, w.Id, "transfer-"+state, ""))
			if state == model.AdtySiteInviteWithdrawApproved {
				var r model.AdtyCommissionRecovery
				require.NoError(t, db.Where("commission_id = ? AND kind = ?", rows[0].Id, "withdrawn").First(&r).Error)
				require.Equal(t, "2", r.AmountUSD.String())
			}
		})
	}
	t.Run("R23 tiers use the last complete Shanghai month", func(t *testing.T) {
		cfg := resellerSetting()
		prior := cfg.CostFactorTiers
		defer func() { cfg.CostFactorTiers = prior }()
		cfg.CostFactorTiers = []adty_setting.ResellerCostTier{{MinMonthlyUSD: 0, CostFactor: 0.9}}
		for _, check := range []struct{ day, want string }{{"2024-03-31T03:00:00Z", "2024-02"}, {"2026-03-29T03:00:00Z", "2026-02"}, {"2026-03-31T03:00:00Z", "2026-02"}, {"2026-03-31T16:00:00Z", "2026-03"}} {
			at, err := time.Parse(time.RFC3339, check.day)
			require.NoError(t, err)
			require.NoError(t, RunAdtySiteCostTierAssignment(at))
			var stored model.AdtySite
			require.NoError(t, db.First(&stored, site.Id).Error)
			require.Empty(t, stored.TierMonth)
		}
	})
	t.Run("business month uses completed payment", func(t *testing.T) {
		require.NoError(t, db.Save(&model.Option{Key: adtyBusinessTimezoneKey, Value: "Asia/Shanghai"}).Error)
		paid := time.Date(2026, 10, 31, 16, 1, 0, 0, time.UTC)
		order := model.AdtyTopupOrder{SiteId: site.Id, TradeNo: "month-edge", UserId: 56, FaceUSD: usd(3), CreditQuota: 1_500_000, CreatedAt: paid.Add(-time.Hour).Unix(), PaidCurrency: "USD", PaidAmount: usd(3)}
		require.NoError(t, db.Create(&order).Error)
		require.NoError(t, db.Create(&model.TopUp{TradeNo: order.TradeNo, UserId: 56, CompleteTime: paid.Unix(), Status: common.TopUpStatusSuccess}).Error)
		require.NoError(t, db.Create(&model.AdtySiteCustomer{SiteId: site.Id, UserId: 56, JoinedAt: paid.Unix()}).Error)
		stats, err := GetAdtySiteStats(site.Id, "2026-11-01", "2026-11-01")
		require.NoError(t, err)
		require.Len(t, stats.Series, 1)
		require.Equal(t, int64(1), stats.Total.NewCustomers)
		require.Equal(t, "3", stats.Total.TopUpUSD.String())
		months, err := AdtySitesMonthTotals([]int{site.Id}, paid)
		require.NoError(t, err)
		require.Equal(t, "3", months[site.Id].TopUpUSD.String())
		overview, err := GetAdtySitesOverview("2026-11-01", "2026-11-01")
		require.NoError(t, err)
		require.Equal(t, "3", overview.SiteTopUpUSD.String())
		sheet, err := summarizeAdtySiteSettlementTx(db, site.Id, "2026-11-01", paid.Add(-time.Minute).Unix(), paid.Add(23*time.Hour).Unix())
		require.NoError(t, err)
		require.Equal(t, "3", sheet.PaidUSD.String())
	})
	t.Run("next day coverage preserves earlier pending amount", func(t *testing.T) {
		end := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC).Unix()
		row := model.AdtySiteInviteCommission{SiteId: site.Id, TradeNo: "coverage-history", AmountUSD: usd(10), Status: model.AdtySiteCommissionPendingCoverage, CreatedAt: end - 10}
		require.NoError(t, db.Create(&row).Error)
		require.NoError(t, db.Model(&row).Updates(map[string]any{"covered_usd": usd(10), "covered_at": end + 10, "status": model.AdtySiteCommissionAvailable}).Error)
		for range 2 {
			sheet, err := summarizeAdtySiteSettlementTx(db, site.Id, "2026-11-19", end-86400, end)
			require.NoError(t, err)
			require.Equal(t, "10", sheet.PendingCoverageUSD.String())
		}
	})
	t.Run("request identifies the accepted retail revision", func(t *testing.T) {
		cfg := resellerSetting()
		oldEnabled := cfg.Enabled
		cfg.Enabled = true
		defer func() { cfg.Enabled = oldEnabled; InvalidateAdtySiteCache() }()
		cost := usd(1)
		require.NoError(t, db.Model(&site).Update("cost_factor", cost).Error)
		InvalidateAdtySiteCache()
		user := model.User{Username: "retail-clock", SiteId: site.Id}
		require.NoError(t, db.Create(&user).Error)
		capture := func(ctx *gin.Context, customerID int) *model.RequestPriceSnapshot {
			billed, _ := AdtyApplySiteMarkupDecimal(ctx, customerID, "basic", usd(1), "meter")
			info := &relaycommon.RelayInfo{UsingGroup: "basic", OriginModelName: "meter"}
			info.PriceData.ModelRatio = 1
			info.PriceData.GroupRatioInfo.SetBilledGroupRatio(billed)
			AdtyFreezeRequestPriceSnapshot(ctx, info)
			return AdtyFrozenPriceSnapshot(ctx)
		}
		fresh := func(customerID int) *model.RequestPriceSnapshot {
			ctx, _ := gin.CreateTestContext(nil)
			return capture(ctx, customerID)
		}
		unconfigured := fresh(user.Id)
		first, second := usd(2), usd(3)
		initial, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, DefaultMarkup: &first})
		require.NoError(t, err)
		before, _ := gin.CreateTestContext(nil)
		snap := capture(before, user.Id)
		require.NotNil(t, snap)
		require.Equal(t, "2", snap.FinalPrice)
		changed, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, ExpectedVersion: initial.Version, DefaultMarkup: &second})
		require.NoError(t, err)
		after, _ := gin.CreateTestContext(nil)
		next := capture(after, user.Id)
		require.NotNil(t, next)
		require.Equal(t, "3", next.FinalPrice)
		require.NotEqual(t, snap.RuleVersion, next.RuleVersion)
		require.Equal(t, snap.SchemaVersion, next.SchemaVersion)
		require.Equal(t, snap.RuleVersion, capture(before, user.Id).RuleVersion)

		t.Run("customer deletion restores site price with a new revision", func(t *testing.T) {
			override, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, UserId: user.Id, DefaultMarkup: &first})
			require.NoError(t, err)
			require.Equal(t, "2", fresh(user.Id).FinalPrice)
			_, err = SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, UserId: user.Id, ExpectedVersion: override.Version, ClearDefault: true})
			require.NoError(t, err)
			inherited := fresh(user.Id)
			require.Equal(t, next.FinalPrice, inherited.FinalPrice)
			require.NotEqual(t, next.RuleVersion, inherited.RuleVersion)
			other := model.User{Username: "retail-clock-other", SiteId: site.Id}
			require.NoError(t, db.Create(&other).Error)
			require.Equal(t, next.RuleVersion, fresh(other.Id).RuleVersion)
			otherOverride, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, UserId: other.Id, DefaultMarkup: &first})
			require.NoError(t, err)
			_, err = SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, UserId: other.Id, ExpectedVersion: otherOverride.Version, ClearDefault: true})
			require.NoError(t, err)
			otherInherited := fresh(other.Id)
			require.Equal(t, inherited.FinalPrice, otherInherited.FinalPrice)
			require.NotEqual(t, inherited.RuleVersion, otherInherited.RuleVersion)
			require.Equal(t, inherited.RuleVersion, fresh(user.Id).RuleVersion)
		})
		t.Run("successive site deletions have distinct built-in price revisions", func(t *testing.T) {
			cleared, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, ExpectedVersion: changed.Version, ClearDefault: true})
			require.NoError(t, err)
			fallback := fresh(user.Id)
			require.Equal(t, unconfigured.FinalPrice, fallback.FinalPrice)
			require.NotEqual(t, unconfigured.RuleVersion, fallback.RuleVersion)
			res, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, ExpectedVersion: cleared.Version, DefaultMarkup: &first})
			require.NoError(t, err)
			_, err = SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, ExpectedVersion: res.Version, ClearDefault: true})
			require.NoError(t, err)
			require.NotEqual(t, fallback.RuleVersion, fresh(user.Id).RuleVersion)
			require.Equal(t, snap.RuleVersion, capture(before, user.Id).RuleVersion)
		})
	})
	t.Run("Alipay requires proof", func(t *testing.T) {
		w := model.AdtySiteInviteWithdrawal{SiteId: site.Id, UserId: 42, AmountUSD: usd(2), NetUSD: usd(2), IdempotencyKey: "F07", Method: "alipay", Status: model.AdtySiteInviteWithdrawApproved}
		require.NoError(t, db.Create(&w).Error)
		require.Error(t, MarkAdtySiteInviteWithdrawalPaid(20, w.Id, "", "   "))
	})
	t.Run("R23 closed site cash remains payable after customer migration", func(t *testing.T) {
		one, zero, rate := usd(1), usd(0), usd(7)
		s := model.AdtySite{OwnerUserId: 901, Subdomain: "r23-closed-cash", Status: model.AdtySiteStatusClosed, MarketingPolicy: model.AdtySiteMarketingPolicy{MinUSD: &one, FeeUSD: &zero, CNYRate: &rate}}
		require.NoError(t, db.Create(&s).Error)
		u := model.User{Username: "r23-migrated", SiteId: site.Id}
		require.NoError(t, db.Create(&u).Error)
		c := model.AdtySiteInviteCommission{SiteId: s.Id, TradeNo: "r23-cash-origin", AvailableAt: time.Now().Unix() - 10, InviterId: u.Id, AmountUSD: usd(8), CoveredUSD: usd(8), EarningsHoldUSD: usd(8), Status: model.AdtySiteCommissionAvailable}
		require.NoError(t, db.Create(&c).Error)
		require.NoError(t, db.Create(&model.AdtySiteWallet{SiteId: s.Id, Kind: model.AdtySiteWalletEarnings, Balance: usd(8), FrozenUSD: usd(8)}).Error)
		in := AdtySiteInviteWithdrawalInput{SiteID: s.Id, UserID: u.Id, Amount: usd(4), Method: "usdt", Network: "BEP20", AccountNo: "0x1111111111111111111111111111111111111111", IdempotencyKey: "r23-customer-replay"}
		w, err := CreateAdtySiteInviteWithdrawal(in)
		require.NoError(t, err)
		in.SiteID = 0
		retry, err := CreateAdtySiteInviteWithdrawal(in)
		require.NoError(t, err)
		require.Equal(t, w.Id, retry.Id)
		in.AccountNo = "0x2222222222222222222222222222222222222222"
		_, err = CreateAdtySiteInviteWithdrawal(in)
		require.ErrorIs(t, err, model.ErrAdtyIdempotencyConflict)
		require.Error(t, ReviewAdtySiteInviteWithdrawal(901, w.Id, true, ""))
		require.NoError(t, ReviewClosedAdtySiteInviteWithdrawal(1, w.Id, true, ""))
		require.Error(t, ReviewClosedAdtySiteInviteWithdrawal(1, w.Id, false, "already approved"))
		require.NoError(t, MarkClosedAdtySiteInviteWithdrawalPaid(1, w.Id, "wire-001", "confirmed"))
		require.NoError(t, db.First(&w, w.Id).Error)
		require.Equal(t, model.AdtySiteInviteWithdrawPaid, w.Status)
		require.Equal(t, s.Id, w.SiteId)
	})
	t.Run("R23 late settlement creates one immutable revision", func(t *testing.T) {
		now := time.Now().Unix()
		day := "2026-11-20"
		const start int64 = 1795132800
		s := model.AdtySite{OwnerUserId: 801, Subdomain: "r23-revision", Status: model.AdtySiteStatusActive}
		require.NoError(t, db.Create(&s).Error)
		require.NoError(t, writeAdtySiteSettlementSheet(s.Id, day, start, start+86400, "UTC", now))
		grant := model.AdtySiteTrialGrant{SiteId: s.Id, UserId: 10, AmountUSD: usd(5), CreatedAt: start + 600}
		require.NoError(t, db.Create(&grant).Error)
		for i := 1; i <= 2; i++ {
			require.NoError(t, writeAdtySiteSettlementSheet(s.Id, day, start, start+86400, "UTC", now+int64(i)))
		}
		rows, err := ListAdtySiteSettlementSheets(s.Id)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.True(t, rows[0].TrialExpenseUSD.IsZero())
		require.Len(t, rows[0].Revisions, 1)
		revision := rows[0].Revisions[0]
		var after model.AdtySiteSettlementSheet
		require.NoError(t, common.Unmarshal(revision.After, &after))
		require.Equal(t, "5", after.TrialExpenseUSD.String())
		require.Contains(t, revision.Sources, fmt.Sprintf("trial_grant/%d", grant.Id))
	})
	t.Run("settlement boundary follows previous end", func(t *testing.T) {
		setZone := func(name string) {
			require.NoError(t, db.Save(&model.Option{Key: adtyBusinessTimezoneKey, Value: name}).Error)
		}
		setZone("UTC")
		day := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
		// The event falls in both nominal windows after UTC switches to Shanghai.
		boundaryPayment := time.Date(2026, 11, 22, 20, 0, 0, 0, time.UTC)
		require.NoError(t, db.Create(&model.TopUp{TradeNo: "boundary-payment", CompleteTime: boundaryPayment.Unix(), Status: common.TopUpStatusSuccess}).Error)
		require.NoError(t, db.Create(&model.AdtyTopupOrder{SiteId: site.Id, TradeNo: "boundary-payment", FaceUSD: usd(11), PaidCurrency: "USD", PaidAmount: usd(11)}).Error)
		_, err := GenerateAdtySiteSettlementSheets(day)
		require.NoError(t, err)
		setZone("Asia/Shanghai")
		_, err = CatchUpAdtySiteSettlementSheets(day.AddDate(0, 0, 3))
		require.NoError(t, err)
		setZone("UTC")
		_, err = CatchUpAdtySiteSettlementSheets(day.AddDate(0, 0, 4))
		require.NoError(t, err)
		var sheets []model.AdtySiteSettlementSheet
		require.NoError(t, db.Where("site_id = ?", site.Id).Order("business_day").Find(&sheets).Error)
		require.GreaterOrEqual(t, len(sheets), 5)
		total := decimal.Zero
		for _, sheet := range sheets {
			if sheet.BusinessDay >= "2026-11-22" {
				total = total.Add(sheet.PaidUSD)
			}
		}
		require.Equal(t, "11", total.String())
		for i := 1; i < len(sheets); i++ {
			require.Equal(t, sheets[i-1].DayEnd, sheets[i].DayStart)
		}
	})
	legacy := model.AdtySiteInviteCommission{SiteId: site.Id, TradeNo: "old-undated-claw", AmountUSD: usd(8), ClawedUSD: usd(3), Status: model.AdtySiteCommissionPendingCoverage, CreatedAt: 100}
	require.NoError(t, db.Create(&legacy).Error)
	require.NoError(t, model.MigrateAdtyTablesOn(db))
	var event model.AdtyCommissionCoverageCancellation
	require.NoError(t, db.Where("commission_id = ?", legacy.Id).First(&event).Error)
	require.True(t, event.Undated)
	require.Equal(t, "3", event.AmountUSD.String())
	historical, err := summarizeAdtySiteSettlementTx(db, site.Id, "1970-01-01", 0, 86400)
	require.NoError(t, err)
	require.True(t, historical.PendingCoverageUnverified)
	require.NoError(t, model.MigrateAdtyTablesOn(db))
	var attempts int64
	require.NoError(t, db.Model(&model.AdtySitePayoutAttempt{}).Count(&attempts).Error)
	require.Positive(t, attempts)

}
