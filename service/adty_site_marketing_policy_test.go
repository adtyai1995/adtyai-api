package service

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestSiteMarketingPolicyLifecycle(t *testing.T) {
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
	schema := fmt.Sprintf("site_policy_%d", time.Now().UnixNano())
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
	oldRedis, oldDay := common.RedisEnabled, adtySiteEarningDayFn
	common.RedisEnabled = false
	adtySiteEarningDayFn = func() string { return "2026-11-23" }
	defer func() { common.RedisEnabled = oldRedis; adtySiteEarningDayFn = oldDay }()
	previousDB, previousType := model.DB, common.MainDatabaseType()
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	defer func() { model.DB = previousDB; common.SetMainDatabaseType(previousType) }()
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.TopUp{}, &model.Token{}))
	require.NoError(t, model.MigrateAdtyTablesOn(db))
	// An upgraded site has SQL NULL in the new initialization flag. A subsequent
	// startup must preserve its owner's edits and leave a new site independent.
	site := model.AdtySite{OwnerUserId: 20, Subdomain: "first", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(&site).UpdateColumn("marketing_policy_initialized", false).Error)
	require.NoError(t, db.Create(&model.AdtySiteReferralRate{SiteId: site.Id, Rate: decimal.RequireFromString(".1"), Enabled: true}).Error)
	legacy := map[string]string{
		"adty_site_referral.max_rate": ".1", adtySiteCommissionCapKey: "2", adtySiteCommissionDaysKey: "4",
		"adty_site_invite_withdraw.min_usd": "4", "adty_site_invite_withdraw.fee_usd": ".2", "adty_site_invite_withdraw.cny_rate": "7",
		"adty_reseller_setting.trial_grant_max_usd": "1", "adty_reseller_setting.trial_grant_daily_usd": "2",
	}
	for key, val := range legacy {
		require.NoError(t, db.Create(&model.Option{Key: key, Value: val}).Error)
	}
	require.NoError(t, model.MigrateAdtyTablesOn(db))
	ownerView, err := LoadAdtyOwnerSiteReferral(site.Id, site.OwnerUserId)
	require.NoError(t, err)
	require.Equal(t, int64(4), *ownerView.ObservationDays)
	second := model.AdtySite{OwnerUserId: 21, Subdomain: "second", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&second).Error)
	secondView, err := LoadAdtyOwnerSiteReferral(second.Id, second.OwnerUserId)
	require.NoError(t, err)
	require.Nil(t, secondView.ObservationDays)
	require.Error(t, SaveAdtyOwnerSiteMarketing(second.Id, site.OwnerUserId, AdtySiteMarketingUpdate{Rate: new("1")}))
	require.Error(t, SaveAdtyOwnerSiteMarketing(second.Id, second.OwnerUserId, AdtySiteMarketingUpdate{Enabled: new(true)}))
	require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, site.OwnerUserId, AdtySiteMarketingUpdate{Rate: new("1"), PendingCoverageCap: new("1"), ObservationDays: new("0"), Enabled: new(true)}))
	require.NoError(t, model.MigrateAdtyTablesOn(db))
	buyer := model.User{Username: "buyer", SiteId: site.Id}
	require.NoError(t, db.Create(&buyer).Error)
	inviter := model.User{Username: "inviter", SiteId: site.Id}
	require.NoError(t, db.Create(&inviter).Error)
	require.NoError(t, db.Create(&model.AdtySiteInvite{SiteId: site.Id, InviteeId: buyer.Id, InviterId: inviter.Id, Rate: decimal.RequireFromString(".1")}).Error)
	order := model.AdtyTopupOrder{SiteId: site.Id, UserId: buyer.Id, TradeNo: "old-order", FaceUSD: decimal.NewFromInt(10)}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return PrepareAdtySiteInviteCommissionSnapshotTx(tx, nil, &order) }))
	require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, site.OwnerUserId, AdtySiteMarketingUpdate{Rate: new("0.2"), PendingCoverageCap: new("10"), ObservationDays: new("3")}))
	next := order
	next.TradeNo = "next"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return PrepareAdtySiteInviteCommissionSnapshotTx(tx, nil, &next) }))
	newTerms, ok, err := loadAdtySiteCommissionOrderSnapshotTx(&next)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "2", newTerms.AmountUSD.String())
	require.Equal(t, "10", newTerms.PendingCoverageCap.String())
	require.Equal(t, int64(3), newTerms.ObservationDays)
	require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, site.OwnerUserId, AdtySiteMarketingUpdate{Enabled: new(false)}))
	now := time.Now()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return RecordAdtySiteInviteCommissionTx(tx, &order, now) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return RecordAdtySiteInviteCommissionTx(tx, &order, now) }))
	var row model.AdtySiteInviteCommission
	require.NoError(t, db.Where("trade_no = ?", order.TradeNo).First(&row).Error)
	require.Equal(t, "10", row.AmountUSD.String())
	require.Equal(t, "1", row.PendingCoverageCap.String())
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return CoverAdtySiteInviteCommissionTx(tx, row.Id, now) }))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, model.AdtySiteCommissionPendingCoverage, row.Status)
	require.NoError(t, db.Model(&model.AdtySiteWallet{}).Where("site_id=? AND kind=?", site.Id, model.AdtySiteWalletEarnings).Update("balance", 20).Error)
	// The next order's threshold is exactly the current outstanding amount.
	// New funding does not allow this new row to jump the older obligations.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := RecordAdtySiteInviteCommissionTx(tx, &next, now); err != nil {
			return err
		}
		var newer model.AdtySiteInviteCommission
		if err := tx.Where("trade_no = ?", next.TradeNo).First(&newer).Error; err != nil {
			return err
		}
		return CoverAdtySiteInviteCommissionTx(tx, newer.Id, now)
	}))
	var queued model.AdtySiteInviteCommission
	require.NoError(t, db.Where("trade_no = ?", next.TradeNo).First(&queued).Error)
	require.Equal(t, model.AdtySiteCommissionPendingCoverage, queued.Status)
	require.True(t, queued.CoveredUSD.IsZero())
	require.NoError(t, ResumeAdtySiteInviteCoverage(site.Id, now))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, model.AdtySiteCommissionAvailable, row.Status)
	require.Equal(t, row.CoveredAt, row.AvailableAt)
	require.True(t, SiteInviteCommissionWithdrawable(&row, now))
	// Old orders without snapshots keep their bind-time rate and the enabled
	// status captured by the migration, even after the owner disables commissions.
	legacyOrder := order
	legacyOrder.TradeNo = "pre-snapshot"
	legacyOrder.SiteReferralSnapshot = ""
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return RecordAdtySiteInviteCommissionTx(tx, &legacyOrder, now) }))
	var legacyRow model.AdtySiteInviteCommission
	require.NoError(t, db.Where("trade_no = ?", legacyOrder.TradeNo).First(&legacyRow).Error)
	require.Equal(t, "1", legacyRow.AmountUSD.String())
	require.Equal(t, "2", legacyRow.PendingCoverageCap.String())
	require.Equal(t, int64(4), legacyRow.ObservationDays)
	require.NoError(t, db.First(&queued, queued.Id).Error)
	require.Equal(t, model.AdtySiteCommissionCovered, queued.Status)
	require.Equal(t, now.Add(72*time.Hour).Unix(), queued.AvailableAt)
	request := AdtySiteInviteWithdrawalInput{UserID: inviter.Id, SiteID: site.Id, Amount: decimal.NewFromInt(4), Method: "usdt", Network: "BEP20", AccountNo: "0x" + strings.Repeat("2", 40), IdempotencyKey: "withdraw-test"}
	paid, err := CreateAdtySiteInviteWithdrawal(request)
	require.NoError(t, err)
	require.Equal(t, "3.8", paid.NetUSD.String())
	require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, site.OwnerUserId, AdtySiteMarketingUpdate{MinUSD: new(""), CNYRate: new(""), FeeUSD: new("")}))
	replay, err := CreateAdtySiteInviteWithdrawal(request)
	require.NoError(t, err)
	require.Equal(t, paid.Id, replay.Id)
	request.IdempotencyKey = "new-after-disable"
	_, err = CreateAdtySiteInviteWithdrawal(request)
	require.Error(t, err)
	require.Error(t, ReviewAdtySiteInviteWithdrawal(second.OwnerUserId, paid.Id, true, ""))
	require.NoError(t, ReviewAdtySiteInviteWithdrawal(site.OwnerUserId, paid.Id, true, ""))
	require.NoError(t, MarkAdtySiteInviteWithdrawalPaid(site.OwnerUserId, paid.Id, "tx-reference", ""))
	for i := range 3 {
		require.NoError(t, db.Create(&model.AdtySiteInviteWithdrawal{SiteId: site.Id, UserId: inviter.Id, Method: "usdt", Status: "pending", IdempotencyKey: fmt.Sprint("page-", i)}).Error)
	}
	page := &common.PageInfo{Page: 1, PageSize: 2}
	first, total, err := ListAdtyOwnerSiteInviteWithdrawals(site.Id, "pending", page)
	require.NoError(t, err)
	require.Equal(t, int64(3), total)
	require.Len(t, first, 2)
	page.Page = 2
	last, total, err := ListAdtyOwnerSiteInviteWithdrawals(site.Id, "pending", page)
	require.NoError(t, err)
	require.Equal(t, int64(3), total)
	require.Len(t, last, 1)
	require.Less(t, last[0].Id, first[1].Id)
	_, total, err = ListAdtyOwnerSiteInviteWithdrawals(second.Id, "pending", page)
	require.NoError(t, err)
	require.Zero(t, total)
	affiliate := model.ReferralAffiliate{UserId: site.OwnerUserId, InviteCode: "owner"}
	require.NoError(t, db.Create(&affiliate).Error)
	require.NoError(t, db.Create(&model.ReferralCommissionAccount{AffiliateId: affiliate.Id, UserId: site.OwnerUserId, AvailableAmount: 10}).Error)
	_, err = GrantAdtySiteTrial(site.Id, buyer.Id, decimal.NewFromInt(2), "too-large")
	require.Error(t, err)
	require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, site.OwnerUserId, AdtySiteMarketingUpdate{TrialGrantDailyUSD: new("1")}))
	grant, err := GrantAdtySiteTrial(site.Id, buyer.Id, decimal.NewFromInt(1), "trial")
	require.NoError(t, err)
	_, err = GrantAdtySiteTrial(site.Id, buyer.Id, decimal.NewFromInt(1), "over-daily-limit")
	require.Error(t, err)
	require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, site.OwnerUserId, AdtySiteMarketingUpdate{TrialGrantMaxUSD: new(""), TrialGrantDailyUSD: new("")}))
	again, err := GrantAdtySiteTrial(site.Id, buyer.Id, decimal.NewFromInt(1), "trial")
	require.NoError(t, err)
	require.Equal(t, grant.Id, again.Id)
	_, err = GrantAdtySiteTrial(site.Id, buyer.Id, decimal.NewFromInt(1), "disabled")
	require.ErrorIs(t, err, ErrAdtySiteTrialUnavailable)
	var account model.ReferralCommissionAccount
	require.NoError(t, db.Where("user_id = ?", site.OwnerUserId).First(&account).Error)
	require.Equal(t, 9.0, account.AvailableAmount)
}
