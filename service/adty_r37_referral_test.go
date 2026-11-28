package service

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupR37TestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)

	tables := []any{
		&model.Option{},
		&model.User{},
		&model.Log{},
		&model.AuditLog{},
		&model.AuditIntent{},
		&model.TopUp{},
		&model.AdtyTopupOrder{},
		&model.AdtyBalanceLedger{},
		&model.AdtyFundBatch{},
		&model.AdtyFundAllocation{},
		&model.AdtyUserFingerprint{},
		&model.AdtyAnomalyDisposition{},
		&model.AdtySite{},
		&model.AdtySiteOwnership{},
		&model.AdtySiteCustomer{},
		&model.AdtySiteReferralRate{},
		&model.AdtySiteReferralAffiliate{},
		&model.AdtySiteReferralAuditLog{},
		&model.AdtySiteInvite{},
		&model.AdtySiteInviteCommission{},
		&model.AdtySiteInviteWithdrawal{},
		&model.AdtySiteInviteWithdrawalItem{},
		&model.AdtySiteWallet{},
		&model.AdtySiteWalletLedger{},
		&model.AdtyPrincipalConsumeEvent{},
		&model.AdtyCommissionOffset{},
	}
	tables = append(tables, model.ReferralTables()...)
	require.NoError(t, db.AutoMigrate(tables...))

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldCrypto := common.CryptoSecret
	oldOptionMap := common.OptionMap
	oldRedis := common.RedisEnabled
	oldMemCache := common.MemoryCacheEnabled

	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	common.CryptoSecret = "r37-test-secret"
	model.DB = db
	model.LOG_DB = db

	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.CryptoSecret = oldCrypto
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
		_ = sqlDB.Close()
	})
	return db
}

func boolPtr(b bool) *bool          { return &b }
func float64Ptr(v float64) *float64 { return &v }

func createR37SiteWithOwner(t *testing.T, db *gorm.DB, suffix string) (model.User, model.AdtySite) {
	t.Helper()
	owner := model.User{
		Username:    "owner_" + suffix,
		DisplayName: "Owner " + suffix,
		Status:      common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&owner).Error)

	site := model.AdtySite{
		OwnerUserId: owner.Id,
		Subdomain:   "sub-" + suffix,
		RootDomain:  "example.com",
		Status:      model.AdtySiteStatusActive,
	}
	require.NoError(t, db.Create(&site).Error)

	for _, w := range []model.AdtySiteWallet{
		{SiteId: site.Id, Kind: model.AdtySiteWalletEarnings, Currency: "USD", Balance: decimal.NewFromInt(500), FrozenUSD: decimal.Zero},
		{SiteId: site.Id, Kind: model.AdtySiteWalletMargin, Currency: "USD", Balance: decimal.Zero, FrozenUSD: decimal.Zero},
		{SiteId: site.Id, Kind: model.AdtySiteWalletOperating, Currency: "QUOTA", Balance: decimal.Zero, FrozenUSD: decimal.Zero},
	} {
		require.NoError(t, db.Create(&w).Error)
	}
	return owner, site
}

func createR37SiteCustomer(t *testing.T, db *gorm.DB, siteID int, username string) model.User {
	t.Helper()
	u := model.User{
		Username:    username,
		DisplayName: username,
		Status:      common.UserStatusEnabled,
		SiteId:      siteID,
	}
	require.NoError(t, db.Create(&u).Error)
	cust := model.AdtySiteCustomer{
		UserId:   u.Id,
		SiteId:   siteID,
		Status:   model.AdtySiteCustomerActive,
		JoinedAt: time.Now().Unix(),
	}
	require.NoError(t, db.Create(&cust).Error)
	return u
}

func TestR37SubsiteReferralRulesAndAuditLogs(t *testing.T) {
	db := setupR37TestDB(t)
	ownerA, siteA := createR37SiteWithOwner(t, db, "rules_a")
	ownerB, siteB := createR37SiteWithOwner(t, db, "rules_b")

	// 1. Save all 6 referral rules plus rate & coverage cap, with audit metadata.
	err := SaveAdtyOwnerSiteMarketingWithAudit(siteA.Id, ownerA.Id, AdtySiteMarketingUpdate{
		Rate:                     strPtr("0.15"),
		Enabled:                  boolPtr(true),
		PendingCoverageCap:       strPtr("500"),
		CookieTTLDays:            strPtr("14"),
		ObservationDays:          strPtr("7"),
		CancelWindowMinutes:      strPtr("30"),
		SelfInviteWindowDays:     strPtr("21"),
		BatchRegisterThreshold:   strPtr("5"),
		BatchRegisterWindowHours: strPtr("12"),
	}, "203.0.113.10", "r37-ua/1.0")
	require.NoError(t, err)

	view, err := LoadAdtyOwnerSiteReferral(siteA.Id, ownerA.Id)
	require.NoError(t, err)
	assert.True(t, view.Enabled)
	assert.Equal(t, "0.15", view.Rate)
	require.NotNil(t, view.CookieTTLDays)
	assert.Equal(t, int64(14), *view.CookieTTLDays)
	require.NotNil(t, view.ObservationDays)
	assert.Equal(t, int64(0), *view.ObservationDays)
	require.NotNil(t, view.CancelWindowMinutes)
	assert.Equal(t, int64(30), *view.CancelWindowMinutes)
	require.NotNil(t, view.SelfInviteWindowDays)
	assert.Equal(t, int64(21), *view.SelfInviteWindowDays)
	require.NotNil(t, view.BatchRegisterThreshold)
	assert.Equal(t, int64(5), *view.BatchRegisterThreshold)
	require.NotNil(t, view.BatchRegisterWindowHours)
	assert.Equal(t, int64(12), *view.BatchRegisterWindowHours)

	// 2. Reject rate > 0.30 and confirm no settings change or audit row is added.
	err = SaveAdtyOwnerSiteMarketingWithAudit(siteA.Id, ownerA.Id, AdtySiteMarketingUpdate{
		Rate: strPtr("0.31"),
	}, "203.0.113.10", "r37-ua/1.0")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAdtySiteReferralRateLimit), "got: %v", err)

	err = SaveAdtyOwnerSiteMarketingWithAudit(siteA.Id, ownerA.Id, AdtySiteMarketingUpdate{
		Rate: strPtr("0.300001"),
	}, "203.0.113.10", "r37-ua/1.0")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAdtySiteReferralRateLimit), "got: %v", err)

	// Reject out-of-range rule bounds.
	for _, bad := range []AdtySiteMarketingUpdate{
		{CookieTTLDays: strPtr("0")},
		{ObservationDays: strPtr("-1")},
		{CancelWindowMinutes: strPtr("-1")},
		{SelfInviteWindowDays: strPtr("0")},
		{BatchRegisterThreshold: strPtr("-1")},
		{BatchRegisterWindowHours: strPtr("0")},
	} {
		assert.Error(t, SaveAdtyOwnerSiteMarketingWithAudit(siteA.Id, ownerA.Id, bad, "", ""))
	}

	// 3. Perform a second valid save on site A (updating CookieTTLDays and clearing BatchRegisterThreshold)
	// and one save on site B to verify pagination and site isolation.
	require.NoError(t, SaveAdtyOwnerSiteMarketingWithAudit(siteA.Id, ownerA.Id, AdtySiteMarketingUpdate{
		CookieTTLDays:          strPtr("30"),
		BatchRegisterThreshold: strPtr(""),
	}, "203.0.113.11", "r37-ua/2.0"))

	viewAfterClear, err := LoadAdtyOwnerSiteReferral(siteA.Id, ownerA.Id)
	require.NoError(t, err)
	require.NotNil(t, viewAfterClear.CookieTTLDays)
	assert.Equal(t, int64(30), *viewAfterClear.CookieTTLDays)
	assert.Nil(t, viewAfterClear.BatchRegisterThreshold, "empty string must clear BatchRegisterThreshold to NULL")

	require.NoError(t, SaveAdtyOwnerSiteMarketingWithAudit(siteB.Id, ownerB.Id, AdtySiteMarketingUpdate{
		Rate:                     strPtr("0.20"),
		PendingCoverageCap:       strPtr("200"),
		ObservationDays:          strPtr("3"),
		Enabled:                  boolPtr(true),
		CookieTTLDays:            strPtr("10"),
		CancelWindowMinutes:      strPtr("15"),
		SelfInviteWindowDays:     strPtr("14"),
		BatchRegisterThreshold:   strPtr("4"),
		BatchRegisterWindowHours: strPtr("6"),
	}, "198.51.100.20", "site-b-agent"))

	// Paginate site A logs (page size 1 -> 2 total pages, newest first, strictly scoped to site A).
	page1, totalA, err := ListAdtyOwnerSiteReferralAuditLogs(siteA.Id, 0, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), totalA)
	require.Len(t, page1, 1)
	assert.Equal(t, siteA.Id, page1[0].SiteId)
	assert.Equal(t, "settings_update", page1[0].Action)
	assert.Equal(t, ownerA.Id, page1[0].OperatorId)
	assert.Equal(t, ownerA.Username, page1[0].OperatorUsername)
	assert.Equal(t, "203.0.113.11", page1[0].Ip)
	assert.NotEmpty(t, page1[0].OldValue)
	assert.NotEmpty(t, page1[0].NewValue)

	page2, totalA2, err := ListAdtyOwnerSiteReferralAuditLogs(siteA.Id, 1, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), totalA2)
	require.Len(t, page2, 1)
	assert.Equal(t, siteA.Id, page2[0].SiteId)
	assert.Greater(t, page1[0].Id, page2[0].Id)
	assert.Equal(t, "203.0.113.10", page2[0].Ip)

	// Site B logs contain only site B's record.
	logsB, totalB, err := ListAdtyOwnerSiteReferralAuditLogs(siteB.Id, 0, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), totalB)
	require.Len(t, logsB, 1)
	assert.Equal(t, siteB.Id, logsB[0].SiteId)
	assert.Equal(t, ownerB.Id, logsB[0].OperatorId)
	assert.Equal(t, ownerB.Username, logsB[0].OperatorUsername)
}

func TestR37SubsiteAffiliateRateOverrideAndAudit(t *testing.T) {
	db := setupR37TestDB(t)
	owner, site := createR37SiteWithOwner(t, db, "aff_rate")
	inviter := createR37SiteCustomer(t, db, site.Id, "inviter_rate")
	invitee := createR37SiteCustomer(t, db, site.Id, "invitee_rate")

	require.NoError(t, SaveAdtyOwnerSiteMarketingWithAudit(site.Id, owner.Id, AdtySiteMarketingUpdate{
		Rate:               strPtr("0.10"),
		PendingCoverageCap: strPtr("500"),
		ObservationDays:    strPtr("7"),
		Enabled:            boolPtr(true),
	}, "127.0.0.1", "test"))

	require.NoError(t, BindSiteInviteFromIP(site.Id, inviter.Id, invitee.Id, "ip_rate_1"))

	// 1. Set individual affiliate rate override (percentage 0..30 -> stored as decimal 0..0.30).
	require.NoError(t, UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, inviter.Id, AdtySiteAffiliateUpdateInput{
		Action: "rate",
		Rate:   float64Ptr(25),
		Reason: "vip affiliate",
	}, "198.51.100.1", "owner-ui"))

	var aff model.AdtySiteReferralAffiliate
	require.NoError(t, db.Where("site_id = ? AND user_id = ?", site.Id, inviter.Id).First(&aff).Error)
	require.NotNil(t, aff.RateOverride)
	assert.True(t, aff.RateOverride.Equal(decimal.RequireFromString("0.25")), "got %s", aff.RateOverride.String())

	// R-49: Hourly consumption settlement uses overridden 0.25 live rate instead of site default 0.10.
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.AdtyPrincipalConsumeEvent{
		OperationKey: "r37-rate-override-1",
		Direction:    "spend",
		SiteId:       site.Id,
		UserId:       invitee.Id,
		Quota:        100 * common.QuotaPerUnitDecimal().IntPart(),
		AmountUSD:    decimal.NewFromInt(100),
		CreatedAt:    now - 3600,
	}).Error)
	settledCount, err := settleSiteInviteConsumeHourTx("2026-11-28T01", now, now, site.Id)
	require.NoError(t, err)
	assert.Equal(t, 1, settledCount)
	var commOverride model.AdtySiteInviteCommission
	require.NoError(t, db.Where("trade_no = ?", "sconsume:2026-11-28T01:s1:u3").First(&commOverride).Error)
	assert.True(t, commOverride.Rate.Equal(decimal.RequireFromString("0.25")))
	assert.True(t, commOverride.AmountUSD.Equal(decimal.NewFromInt(25)))

	// Boundary 0 and 30 succeed.
	require.NoError(t, UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, inviter.Id, AdtySiteAffiliateUpdateInput{
		Action: "rate",
		Rate:   float64Ptr(0),
	}, "198.51.100.1", "owner-ui"))
	require.NoError(t, UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, inviter.Id, AdtySiteAffiliateUpdateInput{
		Action: "rate",
		Rate:   float64Ptr(30),
	}, "198.51.100.1", "owner-ui"))

	// Reject > 30 (30% cap), negative, NaN, Inf.
	for _, badRate := range []float64{30.01, 31, 50} {
		err := UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, inviter.Id, AdtySiteAffiliateUpdateInput{
			Action: "rate",
			Rate:   float64Ptr(badRate),
		}, "198.51.100.1", "owner-ui")
		require.Error(t, err, "rate %v must be rejected", badRate)
		assert.True(t, errors.Is(err, ErrAdtySiteReferralRateLimit), "rate %v expected rate limit error, got %v", badRate, err)
	}
	for _, invalidRate := range []float64{-0.5, math.NaN(), math.Inf(1), math.Inf(-1)} {
		err := UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, inviter.Id, AdtySiteAffiliateUpdateInput{
			Action: "rate",
			Rate:   float64Ptr(invalidRate),
		}, "198.51.100.1", "owner-ui")
		require.Error(t, err, "rate %v must be rejected", invalidRate)
	}

	// 2. Clear rate override (nil restores site default).
	require.NoError(t, UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, inviter.Id, AdtySiteAffiliateUpdateInput{
		Action: "rate",
		Rate:   nil,
		Reason: "reset to site default",
	}, "198.51.100.1", "owner-ui"))

	var cleared model.AdtySiteReferralAffiliate
	require.NoError(t, db.Where("site_id = ? AND user_id = ?", site.Id, inviter.Id).First(&cleared).Error)
	assert.Nil(t, cleared.RateOverride)

	var isNull int
	require.NoError(t, db.Raw("SELECT CASE WHEN rate_override IS NULL THEN 1 ELSE 0 END FROM adty_site_referral_affiliates WHERE id = ?", cleared.Id).Scan(&isNull).Error)
	assert.Equal(t, 1, isNull)

	// R-49: Hourly consumption settlement falls back to site default 0.10 after clearing override.
	require.NoError(t, db.Create(&model.AdtyPrincipalConsumeEvent{
		OperationKey: "r37-rate-default-2",
		Direction:    "spend",
		SiteId:       site.Id,
		UserId:       invitee.Id,
		Quota:        100 * common.QuotaPerUnitDecimal().IntPart(),
		AmountUSD:    decimal.NewFromInt(100),
		CreatedAt:    now - 1800,
	}).Error)
	settledCount, err = settleSiteInviteConsumeHourTx("2026-11-28T02", now, now, site.Id)
	require.NoError(t, err)
	assert.Equal(t, 1, settledCount)
	var commDefault model.AdtySiteInviteCommission
	require.NoError(t, db.Where("trade_no = ?", "sconsume:2026-11-28T02:s1:u3").First(&commDefault).Error)
	assert.True(t, commDefault.Rate.Equal(decimal.RequireFromString("0.10")))
	assert.True(t, commDefault.AmountUSD.Equal(decimal.NewFromInt(10)))

	// Verify audit logs recorded OldValue & NewValue for all 4 successful rate updates.
	var logs []model.AdtySiteReferralAuditLog
	require.NoError(t, db.Where("site_id = ? AND action = ?", site.Id, "rate").Order("id ASC").Find(&logs).Error)
	require.Len(t, logs, 4)
	for _, entry := range logs {
		assert.Equal(t, inviter.Id, entry.TargetUserId)
		assert.Equal(t, owner.Id, entry.OperatorId)
		assert.NotEmpty(t, strings.TrimSpace(entry.OldValue))
		assert.NotEmpty(t, strings.TrimSpace(entry.NewValue))
	}
}

func TestR37SubsiteAffiliateDisableAndRestoreEnforcement(t *testing.T) {
	db := setupR37TestDB(t)
	owner, site := createR37SiteWithOwner(t, db, "aff_disable")
	inviter := createR37SiteCustomer(t, db, site.Id, "inviter_dis")
	inviteeBound := createR37SiteCustomer(t, db, site.Id, "invitee_dis_1")
	inviteeNew := createR37SiteCustomer(t, db, site.Id, "invitee_dis_2")

	require.NoError(t, SaveAdtyOwnerSiteMarketingWithAudit(site.Id, owner.Id, AdtySiteMarketingUpdate{
		Rate:               strPtr("0.12"),
		PendingCoverageCap: strPtr("500"),
		ObservationDays:    strPtr("7"),
		Enabled:            boolPtr(true),
	}, "127.0.0.1", "test"))

	// Bind inviteeBound before disabling.
	require.NoError(t, BindSiteInviteFromIP(site.Id, inviter.Id, inviteeBound.Id, "ip_dis_1"))

	// 1. Disable affiliate.
	require.NoError(t, UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, inviter.Id, AdtySiteAffiliateUpdateInput{
		Action: "disable",
		Reason: "abusive traffic",
	}, "198.51.100.2", "owner-ui"))

	var aff model.AdtySiteReferralAffiliate
	require.NoError(t, db.Where("site_id = ? AND user_id = ?", site.Id, inviter.Id).First(&aff).Error)
	assert.Equal(t, model.AdtySiteAffiliateStatusDisabled, aff.Status)
	assert.Equal(t, owner.Id, aff.DisabledBy)
	assert.Greater(t, aff.DisabledAt, int64(0))

	// Disabled affiliate cannot bind new invitees.
	err := BindSiteInviteFromIP(site.Id, inviter.Id, inviteeNew.Id, "ip_dis_2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")

	// R-49: Disabled affiliate accrues zero hourly consumption commission for existing bound invitee.
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.AdtyPrincipalConsumeEvent{
		OperationKey: "r37-dis-consume-1",
		Direction:    "spend",
		SiteId:       site.Id,
		UserId:       inviteeBound.Id,
		Quota:        100 * common.QuotaPerUnitDecimal().IntPart(),
		AmountUSD:    decimal.NewFromInt(100),
		CreatedAt:    now - 3600,
	}).Error)
	settledCount, err := settleSiteInviteConsumeHourTx("2026-11-28T03", now, now, site.Id)
	require.NoError(t, err)
	assert.Zero(t, settledCount)

	var commCount int64
	require.NoError(t, db.Model(&model.AdtySiteInviteCommission{}).Where("site_id = ?", site.Id).Count(&commCount).Error)
	assert.Zero(t, commCount)

	// 2. Restore affiliate.
	require.NoError(t, UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, inviter.Id, AdtySiteAffiliateUpdateInput{
		Action: "restore",
		Reason: "reinstated",
	}, "198.51.100.2", "owner-ui"))

	require.NoError(t, db.Where("site_id = ? AND user_id = ?", site.Id, inviter.Id).First(&aff).Error)
	assert.Equal(t, model.AdtySiteAffiliateStatusApproved, aff.Status)
	assert.Zero(t, aff.DisabledBy)
	assert.Zero(t, aff.DisabledAt)

	// Restored affiliate can bind inviteeNew and accrue hourly consumption commission again.
	require.NoError(t, BindSiteInviteFromIP(site.Id, inviter.Id, inviteeNew.Id, "ip_dis_2"))

	require.NoError(t, db.Create(&model.AdtyPrincipalConsumeEvent{
		OperationKey: "r37-res-consume-2",
		Direction:    "spend",
		SiteId:       site.Id,
		UserId:       inviteeNew.Id,
		Quota:        100 * common.QuotaPerUnitDecimal().IntPart(),
		AmountUSD:    decimal.NewFromInt(100),
		CreatedAt:    now - 1800,
	}).Error)
	settledCount, err = settleSiteInviteConsumeHourTx("2026-11-28T04", now, now, site.Id)
	require.NoError(t, err)
	assert.Equal(t, 1, settledCount)
	var commRes model.AdtySiteInviteCommission
	require.NoError(t, db.Where("site_id = ? AND invitee_id = ?", site.Id, inviteeNew.Id).First(&commRes).Error)
	assert.Equal(t, inviter.Id, commRes.InviterId)
	assert.True(t, commRes.AmountUSD.Equal(decimal.NewFromInt(12)))

	require.NoError(t, db.Model(&model.AdtySiteInviteCommission{}).Where("site_id = ?", site.Id).Count(&commCount).Error)
	assert.Equal(t, int64(1), commCount)

	// Verify audit logs for disable + restore.
	var logs []model.AdtySiteReferralAuditLog
	require.NoError(t, db.Where("site_id = ? AND action IN ?", site.Id, []string{"disable", "restore"}).Order("id ASC").Find(&logs).Error)
	require.Len(t, logs, 2)
	assert.Equal(t, "disable", logs[0].Action)
	assert.Equal(t, "restore", logs[1].Action)
	for _, entry := range logs {
		assert.NotEmpty(t, strings.TrimSpace(entry.OldValue))
		assert.NotEmpty(t, strings.TrimSpace(entry.NewValue))
	}
}

func TestR37SubsiteAffiliateSettlementFreezeAndRestoreEnforcement(t *testing.T) {
	db := setupR37TestDB(t)
	owner, site := createR37SiteWithOwner(t, db, "aff_settle")
	inviterFrozen := createR37SiteCustomer(t, db, site.Id, "inviter_settle_1")
	inviterNormal := createR37SiteCustomer(t, db, site.Id, "inviter_settle_2")
	invitee1 := createR37SiteCustomer(t, db, site.Id, "invitee_settle_1")
	invitee2 := createR37SiteCustomer(t, db, site.Id, "invitee_settle_2")

	pastDue := time.Now().Add(-2 * time.Hour).Unix()
	commFrozen := model.AdtySiteInviteCommission{
		SiteId:          site.Id,
		TradeNo:         "R37-SETTLE-1",
		InviterId:       inviterFrozen.Id,
		InviteeId:       invitee1.Id,
		FaceUSD:         decimal.NewFromInt(100),
		Rate:            decimal.RequireFromString("0.10"),
		AmountUSD:       decimal.NewFromInt(10),
		CoveredUSD:      decimal.NewFromInt(10),
		EarningsHoldUSD: decimal.NewFromInt(10),
		Status:          model.AdtySiteCommissionCovered,
		CoveredAt:       pastDue - 86400,
		AvailableAt:     pastDue,
		CreatedAt:       pastDue - 86400,
	}
	commNormal := model.AdtySiteInviteCommission{
		SiteId:          site.Id,
		TradeNo:         "R37-SETTLE-2",
		InviterId:       inviterNormal.Id,
		InviteeId:       invitee2.Id,
		FaceUSD:         decimal.NewFromInt(100),
		Rate:            decimal.RequireFromString("0.10"),
		AmountUSD:       decimal.NewFromInt(10),
		CoveredUSD:      decimal.NewFromInt(10),
		EarningsHoldUSD: decimal.NewFromInt(10),
		Status:          model.AdtySiteCommissionCovered,
		CoveredAt:       pastDue - 86400,
		AvailableAt:     pastDue,
		CreatedAt:       pastDue - 86400,
	}
	require.NoError(t, db.Create(&commFrozen).Error)
	require.NoError(t, db.Create(&commNormal).Error)

	// 1. Freeze settlement for inviterFrozen and attach an existing risk_reason too.
	require.NoError(t, UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, inviterFrozen.Id, AdtySiteAffiliateUpdateInput{
		Action: "freeze_settlement",
		Reason: "manual settlement freeze",
	}, "198.51.100.3", "owner-ui"))
	require.NoError(t, db.Model(&model.AdtySiteReferralAffiliate{}).
		Where("site_id = ? AND user_id = ?", site.Id, inviterFrozen.Id).
		Update("risk_reason", model.ReferralRiskBatchRegister).Error)

	var aff model.AdtySiteReferralAffiliate
	require.NoError(t, db.Where("site_id = ? AND user_id = ?", site.Id, inviterFrozen.Id).First(&aff).Error)
	assert.False(t, aff.SettlementEnabled)
	assert.Equal(t, model.ReferralRiskBatchRegister, aff.RiskReason)
	assert.Equal(t, "manual settlement freeze", aff.RiskNote)

	// Run release job: frozen affiliate's commission stays covered; normal affiliate releases.
	require.NoError(t, ReleaseAdtySiteInviteCommissions(time.Now()))

	var reloadedFrozen, reloadedNormal model.AdtySiteInviteCommission
	require.NoError(t, db.First(&reloadedFrozen, commFrozen.Id).Error)
	require.NoError(t, db.First(&reloadedNormal, commNormal.Id).Error)
	assert.Equal(t, model.AdtySiteCommissionCovered, reloadedFrozen.Status, "frozen affiliate commission must not release")
	assert.Equal(t, model.AdtySiteCommissionAvailable, reloadedNormal.Status, "unfrozen affiliate commission must release")

	// 2. Restore settlement -> clears BOTH risk_reason and risk_note, then release succeeds.
	require.NoError(t, UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, inviterFrozen.Id, AdtySiteAffiliateUpdateInput{
		Action: "restore_settlement",
		Reason: "verified legitimate",
	}, "198.51.100.3", "owner-ui"))

	require.NoError(t, db.Where("site_id = ? AND user_id = ?", site.Id, inviterFrozen.Id).First(&aff).Error)
	assert.True(t, aff.SettlementEnabled)
	assert.Empty(t, aff.RiskReason, "restore_settlement must clear risk_reason")
	assert.Empty(t, aff.RiskNote, "restore_settlement must clear risk_note")

	require.NoError(t, ReleaseAdtySiteInviteCommissions(time.Now()))
	require.NoError(t, db.First(&reloadedFrozen, commFrozen.Id).Error)
	assert.Equal(t, model.AdtySiteCommissionAvailable, reloadedFrozen.Status, "restored affiliate commission must release")

	// Verify audit logs for freeze_settlement + restore_settlement.
	var logs []model.AdtySiteReferralAuditLog
	require.NoError(t, db.Where("site_id = ? AND action IN ?", site.Id, []string{"freeze_settlement", "restore_settlement"}).Order("id ASC").Find(&logs).Error)
	require.Len(t, logs, 2)
	assert.Equal(t, "freeze_settlement", logs[0].Action)
	assert.Equal(t, "restore_settlement", logs[1].Action)
	for _, entry := range logs {
		assert.NotEmpty(t, strings.TrimSpace(entry.OldValue))
		assert.NotEmpty(t, strings.TrimSpace(entry.NewValue))
	}
}

func TestR37SubsiteAffiliateWithdrawalFreezeAndRestoreEnforcement(t *testing.T) {
	db := setupR37TestDB(t)
	owner, site := createR37SiteWithOwner(t, db, "aff_wd")
	inviter := createR37SiteCustomer(t, db, site.Id, "inviter_wd")
	invitee := createR37SiteCustomer(t, db, site.Id, "invitee_wd")

	// Configure site withdrawal rules and earnings hold matching the available commission.
	require.NoError(t, SaveAdtyOwnerSiteMarketingWithAudit(site.Id, owner.Id, AdtySiteMarketingUpdate{
		MinUSD:               strPtr("10"),
		FeeUSD:               strPtr("1"),
		WithdrawMethods:      strSlicePtr([]string{"usdt"}),
		WithdrawUSDTNetworks: strSlicePtr([]string{"TRC20"}),
	}, "127.0.0.1", "test"))
	require.NoError(t, db.Model(&model.AdtySiteWallet{}).
		Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).
		Updates(map[string]any{"balance": decimal.NewFromInt(100), "frozen_usd": decimal.NewFromInt(100)}).Error)

	comm := model.AdtySiteInviteCommission{
		SiteId:          site.Id,
		TradeNo:         "R37-WD-COMM-1",
		InviterId:       inviter.Id,
		InviteeId:       invitee.Id,
		FaceUSD:         decimal.NewFromInt(1000),
		Rate:            decimal.RequireFromString("0.10"),
		AmountUSD:       decimal.NewFromInt(100),
		CoveredUSD:      decimal.NewFromInt(100),
		EarningsHoldUSD: decimal.NewFromInt(100),
		Status:          model.AdtySiteCommissionAvailable,
		AvailableAt:     time.Now().Add(-time.Hour).Unix(),
		CreatedAt:       time.Now().Add(-2 * time.Hour).Unix(),
	}
	require.NoError(t, db.Create(&comm).Error)

	// 1. Freeze withdrawal -> blocks both CreateAdtySiteInviteWithdrawal and ConvertAdtySiteInviteCommission.
	require.NoError(t, UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, inviter.Id, AdtySiteAffiliateUpdateInput{
		Action: "freeze_withdrawal",
		Reason: "payout hold",
	}, "198.51.100.4", "owner-ui"))

	var aff model.AdtySiteReferralAffiliate
	require.NoError(t, db.Where("site_id = ? AND user_id = ?", site.Id, inviter.Id).First(&aff).Error)
	assert.False(t, aff.WithdrawalEnabled)

	_, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID:         inviter.Id,
		SiteID:         site.Id,
		Amount:         decimal.NewFromInt(20),
		Method:         "balance",
		IdempotencyKey: "r37-wd-frozen-balance",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Withdrawals are paused")

	quote, err := QuoteAdtySiteInviteWithdrawal(inviter.Id, site.Id, decimal.NewFromInt(20), "usdt")
	require.NoError(t, err)
	_, err = CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID:          inviter.Id,
		SiteID:          site.Id,
		Amount:          decimal.NewFromInt(20),
		Method:          "usdt",
		Network:         "TRC20",
		AccountNo:       "TQn9Y2khEsLJW1ChVWFMSMeRDow5KcbLSE",
		ExpectedQuoteID: quote.QuoteID,
		RequireQuote:    true,
		IdempotencyKey:  "r37-wd-frozen-usdt",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Withdrawals are paused")

	err = ConvertAdtySiteInviteCommission(inviter.Id, comm.Id, decimal.NewFromInt(10), "r37-cvt-frozen")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Withdrawals are paused")

	// 2. Restore withdrawal -> both ConvertAdtySiteInviteCommission and CreateAdtySiteInviteWithdrawal succeed.
	require.NoError(t, UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, inviter.Id, AdtySiteAffiliateUpdateInput{
		Action: "restore_withdrawal",
		Reason: "hold lifted",
	}, "198.51.100.4", "owner-ui"))

	require.NoError(t, db.Where("site_id = ? AND user_id = ?", site.Id, inviter.Id).First(&aff).Error)
	assert.True(t, aff.WithdrawalEnabled)

	require.NoError(t, ConvertAdtySiteInviteCommission(inviter.Id, comm.Id, decimal.NewFromInt(10), "r37-cvt-ok"))

	wd, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID:         inviter.Id,
		SiteID:         site.Id,
		Amount:         decimal.NewFromInt(15),
		Method:         "balance",
		IdempotencyKey: "r37-wd-restored-ok",
	})
	require.NoError(t, err)
	assert.Equal(t, model.AdtySiteInviteWithdrawPaid, wd.Status)

	// Verify audit logs for freeze_withdrawal + restore_withdrawal.
	var logs []model.AdtySiteReferralAuditLog
	require.NoError(t, db.Where("site_id = ? AND action IN ?", site.Id, []string{"freeze_withdrawal", "restore_withdrawal"}).Order("id ASC").Find(&logs).Error)
	require.Len(t, logs, 2)
	assert.Equal(t, "freeze_withdrawal", logs[0].Action)
	assert.Equal(t, "restore_withdrawal", logs[1].Action)
	for _, entry := range logs {
		assert.NotEmpty(t, strings.TrimSpace(entry.OldValue))
		assert.NotEmpty(t, strings.TrimSpace(entry.NewValue))
	}
}

func TestR37SubsiteSelfInviteWindowEnforcement(t *testing.T) {
	db := setupR37TestDB(t)
	owner, site := createR37SiteWithOwner(t, db, "self_inv")
	inviterRecent := createR37SiteCustomer(t, db, site.Id, "inviter_self_recent")
	inviterOld := createR37SiteCustomer(t, db, site.Id, "inviter_self_old")
	inviteeSameIP := createR37SiteCustomer(t, db, site.Id, "invitee_same_ip")
	inviteeSameDev := createR37SiteCustomer(t, db, site.Id, "invitee_same_dev")
	inviteeExpiredWin := createR37SiteCustomer(t, db, site.Id, "invitee_expired_win")

	require.NoError(t, SaveAdtyOwnerSiteMarketingWithAudit(site.Id, owner.Id, AdtySiteMarketingUpdate{
		Rate:                 strPtr("0.10"),
		PendingCoverageCap:   strPtr("500"),
		ObservationDays:      strPtr("7"),
		SelfInviteWindowDays: strPtr("7"),
		Enabled:              boolPtr(true),
	}, "127.0.0.1", "test"))

	now := time.Now().Unix()
	// Fingerprint within the 7-day window.
	require.NoError(t, db.Create(&model.AdtyUserFingerprint{
		UserId:     inviterRecent.Id,
		IpHash:     "fp_ip_shared",
		DeviceHash: "fp_dev_shared",
		Kind:       "login",
		CreatedAt:  now - 2*86400,
	}).Error)
	// Invitee sharing device hash with inviterRecent (different IP).
	require.NoError(t, db.Create(&model.AdtyUserFingerprint{
		UserId:     inviteeSameDev.Id,
		IpHash:     "fp_ip_different",
		DeviceHash: "fp_dev_shared",
		Kind:       "register",
		CreatedAt:  now,
	}).Error)
	// Fingerprint outside the 7-day window (10 days ago).
	require.NoError(t, db.Create(&model.AdtyUserFingerprint{
		UserId:     inviterOld.Id,
		IpHash:     "fp_ip_old",
		DeviceHash: "fp_dev_old",
		Kind:       "login",
		CreatedAt:  now - 10*86400,
	}).Error)

	// 1. Same IP inside SelfInviteWindowDays -> flagged self_invite, snapshot skipped.
	require.NoError(t, BindSiteInviteFromIP(site.Id, inviterRecent.Id, inviteeSameIP.Id, "fp_ip_shared"))
	var invIP model.AdtySiteInvite
	require.NoError(t, db.Where("site_id = ? AND invitee_id = ?", site.Id, inviteeSameIP.Id).First(&invIP).Error)
	assert.Equal(t, model.ReferralRiskSelfInvite, invIP.RiskFlag)

	// R-49: self_invite flagged binding accrues zero hourly consumption commission.
	require.NoError(t, db.Create(&model.AdtyPrincipalConsumeEvent{
		OperationKey: "r37-self-flagged-1",
		Direction:    "spend",
		SiteId:       site.Id,
		UserId:       inviteeSameIP.Id,
		Quota:        100 * common.QuotaPerUnitDecimal().IntPart(),
		AmountUSD:    decimal.NewFromInt(100),
		CreatedAt:    now - 10,
	}).Error)
	settledCount, err := settleSiteInviteConsumeHourTx("2026-11-28T05", now+1, now, site.Id)
	require.NoError(t, err)
	assert.Zero(t, settledCount, "self_invite must skip hourly commission eligibility")

	var commCount int64
	require.NoError(t, db.Model(&model.AdtySiteInviteCommission{}).Where("site_id = ? AND invitee_id = ?", site.Id, inviteeSameIP.Id).Count(&commCount).Error)
	assert.Zero(t, commCount)

	// 2. Same device hash inside SelfInviteWindowDays -> also flagged self_invite.
	require.NoError(t, BindSiteInviteFromIP(site.Id, inviterRecent.Id, inviteeSameDev.Id, "fp_ip_different"))
	var invDev model.AdtySiteInvite
	require.NoError(t, db.Where("site_id = ? AND invitee_id = ?", site.Id, inviteeSameDev.Id).First(&invDev).Error)
	assert.Equal(t, model.ReferralRiskSelfInvite, invDev.RiskFlag)

	// 3. Same IP OUTSIDE SelfInviteWindowDays (10d > 7d) -> NOT flagged, eligible for hourly consumption commission.
	require.NoError(t, BindSiteInviteFromIP(site.Id, inviterOld.Id, inviteeExpiredWin.Id, "fp_ip_old"))
	var invOld model.AdtySiteInvite
	require.NoError(t, db.Where("site_id = ? AND invitee_id = ?", site.Id, inviteeExpiredWin.Id).First(&invOld).Error)
	assert.Empty(t, invOld.RiskFlag)

	require.NoError(t, db.Create(&model.AdtyPrincipalConsumeEvent{
		OperationKey: "r37-self-clean-2",
		Direction:    "spend",
		SiteId:       site.Id,
		UserId:       inviteeExpiredWin.Id,
		Quota:        100 * common.QuotaPerUnitDecimal().IntPart(),
		AmountUSD:    decimal.NewFromInt(100),
		CreatedAt:    now - 10,
	}).Error)
	settledCount, err = settleSiteInviteConsumeHourTx("2026-11-28T06", now+1, now, site.Id)
	require.NoError(t, err)
	assert.Equal(t, 1, settledCount)
	var commClean model.AdtySiteInviteCommission
	require.NoError(t, db.Where("site_id = ? AND invitee_id = ?", site.Id, inviteeExpiredWin.Id).First(&commClean).Error)
	assert.Equal(t, inviterOld.Id, commClean.InviterId)
}

func TestR37SubsiteBatchRegisterRuleEnforcement(t *testing.T) {
	db := setupR37TestDB(t)
	owner, site := createR37SiteWithOwner(t, db, "batch_reg")
	inviter := createR37SiteCustomer(t, db, site.Id, "inviter_batch")
	inviterOldWin := createR37SiteCustomer(t, db, site.Id, "inviter_batch_old")

	require.NoError(t, SaveAdtyOwnerSiteMarketingWithAudit(site.Id, owner.Id, AdtySiteMarketingUpdate{
		Rate:                     strPtr("0.10"),
		PendingCoverageCap:       strPtr("500"),
		ObservationDays:          strPtr("7"),
		BatchRegisterThreshold:   strPtr("3"),
		BatchRegisterWindowHours: strPtr("12"),
		Enabled:                  boolPtr(true),
	}, "127.0.0.1", "test"))

	// 1. First 2 invites from same IP stay below threshold=3 -> settlement not frozen.
	u1 := createR37SiteCustomer(t, db, site.Id, "batch_u1")
	u2 := createR37SiteCustomer(t, db, site.Id, "batch_u2")
	u3 := createR37SiteCustomer(t, db, site.Id, "batch_u3")

	require.NoError(t, BindSiteInviteFromIP(site.Id, inviter.Id, u1.Id, "same_batch_ip"))
	require.NoError(t, BindSiteInviteFromIP(site.Id, inviter.Id, u2.Id, "same_batch_ip"))

	var countBefore int64
	require.NoError(t, db.Model(&model.AdtySiteReferralAffiliate{}).
		Where("site_id = ? AND user_id = ? AND settlement_enabled = ?", site.Id, inviter.Id, false).
		Count(&countBefore).Error)
	assert.Zero(t, countBefore, "must not freeze before reaching BatchRegisterThreshold")

	// 2. 3rd invite within 12h window reaches threshold=3 -> freezes inviter settlement with batch_register risk.
	require.NoError(t, BindSiteInviteFromIP(site.Id, inviter.Id, u3.Id, "same_batch_ip"))

	var aff model.AdtySiteReferralAffiliate
	require.NoError(t, db.Where("site_id = ? AND user_id = ?", site.Id, inviter.Id).First(&aff).Error)
	assert.False(t, aff.SettlementEnabled, "exceeding BatchRegisterThreshold must freeze affiliate settlement")
	assert.Equal(t, model.ReferralRiskBatchRegister, aff.RiskReason)
	assert.NotEmpty(t, aff.RiskNote)

	// Confirm downstream settlement release respects the batch-register freeze.
	comm := model.AdtySiteInviteCommission{
		SiteId:          site.Id,
		TradeNo:         "R37-BATCH-COMM-1",
		InviterId:       inviter.Id,
		InviteeId:       u1.Id,
		FaceUSD:         decimal.NewFromInt(100),
		Rate:            decimal.RequireFromString("0.10"),
		AmountUSD:       decimal.NewFromInt(10),
		CoveredUSD:      decimal.NewFromInt(10),
		EarningsHoldUSD: decimal.NewFromInt(10),
		Status:          model.AdtySiteCommissionCovered,
		AvailableAt:     time.Now().Add(-time.Hour).Unix(),
		CreatedAt:       time.Now().Add(-2 * time.Hour).Unix(),
	}
	require.NoError(t, db.Create(&comm).Error)
	require.NoError(t, ReleaseAdtySiteInviteCommissions(time.Now()))
	var reloaded model.AdtySiteInviteCommission
	require.NoError(t, db.First(&reloaded, comm.Id).Error)
	assert.Equal(t, model.AdtySiteCommissionCovered, reloaded.Status, "batch-frozen affiliate commission must stay covered")

	// 3. Window check: invites older than BatchRegisterWindowHours (12h) do not count toward threshold.
	oldA := createR37SiteCustomer(t, db, site.Id, "batch_old_a")
	oldB := createR37SiteCustomer(t, db, site.Id, "batch_old_b")
	freshC := createR37SiteCustomer(t, db, site.Id, "batch_fresh_c")
	oldTime := time.Now().Add(-20 * time.Hour).Unix()
	require.NoError(t, db.Create(&model.AdtySiteInvite{
		SiteId: site.Id, InviterId: inviterOldWin.Id, InviteeId: oldA.Id,
		Rate: decimal.RequireFromString("0.10"), IpHash: "window_ip", CreatedAt: oldTime,
	}).Error)
	require.NoError(t, db.Create(&model.AdtySiteInvite{
		SiteId: site.Id, InviterId: inviterOldWin.Id, InviteeId: oldB.Id,
		Rate: decimal.RequireFromString("0.10"), IpHash: "window_ip", CreatedAt: oldTime,
	}).Error)
	require.NoError(t, BindSiteInviteFromIP(site.Id, inviterOldWin.Id, freshC.Id, "window_ip"))

	var frozenOldWin int64
	require.NoError(t, db.Model(&model.AdtySiteReferralAffiliate{}).
		Where("site_id = ? AND user_id = ? AND settlement_enabled = ?", site.Id, inviterOldWin.Id, false).
		Count(&frozenOldWin).Error)
	assert.Zero(t, frozenOldWin, "invites outside BatchRegisterWindowHours must not freeze settlement")
}

func TestR37ZeroDayObservationRespectsAffiliateSettlementFreeze(t *testing.T) {
	db := setupR37TestDB(t)
	owner, site := createR37SiteWithOwner(t, db, "zero_day_freeze")
	inviterFrozen := createR37SiteCustomer(t, db, site.Id, "inviter_0d_frozen")
	inviterNormal := createR37SiteCustomer(t, db, site.Id, "inviter_0d_normal")
	invitee1 := createR37SiteCustomer(t, db, site.Id, "invitee_0d_1")
	invitee2 := createR37SiteCustomer(t, db, site.Id, "invitee_0d_2")

	require.NoError(t, SaveAdtyOwnerSiteMarketingWithAudit(site.Id, owner.Id, AdtySiteMarketingUpdate{
		Rate:               strPtr("0.10"),
		PendingCoverageCap: strPtr("500"),
		ObservationDays:    strPtr("0"),
		Enabled:            boolPtr(true),
	}, "127.0.0.1", "test"))

	require.NoError(t, BindSiteInviteFromIP(site.Id, inviterFrozen.Id, invitee1.Id, "ip_0d_1"))
	require.NoError(t, BindSiteInviteFromIP(site.Id, inviterNormal.Id, invitee2.Id, "ip_0d_2"))

	// Freeze settlement for inviterFrozen before the order is covered.
	require.NoError(t, UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, inviterFrozen.Id, AdtySiteAffiliateUpdateInput{
		Action: "freeze_settlement",
		Reason: "pending risk review",
	}, "198.51.100.9", "owner-ui"))

	now := time.Now().Truncate(time.Second)
	// R-49: Hourly consumption settlement records risk_held (without freezing earnings) for frozen affiliate,
	// while normal affiliate becomes available immediately (0 observation days).
	require.NoError(t, db.Create(&model.AdtyPrincipalConsumeEvent{
		OperationKey: "r37-0day-frozen-1",
		Direction:    "spend",
		SiteId:       site.Id,
		UserId:       invitee1.Id,
		Quota:        100 * common.QuotaPerUnitDecimal().IntPart(),
		AmountUSD:    decimal.NewFromInt(100),
		CreatedAt:    now.Unix() - 10,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyPrincipalConsumeEvent{
		OperationKey: "r37-0day-normal-2",
		Direction:    "spend",
		SiteId:       site.Id,
		UserId:       invitee2.Id,
		Quota:        100 * common.QuotaPerUnitDecimal().IntPart(),
		AmountUSD:    decimal.NewFromInt(100),
		CreatedAt:    now.Unix() - 10,
	}).Error)
	settledCount, err := settleSiteInviteConsumeHourTx("2026-11-28T07", now.Unix()+1, now.Unix(), site.Id)
	require.NoError(t, err)
	assert.Equal(t, 2, settledCount)

	var frozenRow, normalRow model.AdtySiteInviteCommission
	require.NoError(t, db.Where("site_id = ? AND invitee_id = ?", site.Id, invitee1.Id).First(&frozenRow).Error)
	require.NoError(t, db.Where("site_id = ? AND invitee_id = ?", site.Id, invitee2.Id).First(&normalRow).Error)

	// Frozen affiliate gets risk_held (0 wallet freeze, not withdrawable); normal affiliate becomes available immediately.
	assert.Equal(t, model.AdtySiteCommissionRiskHeld, frozenRow.Status, "risk-frozen affiliate must be recorded as risk_held")
	assert.True(t, frozenRow.CoveredUSD.IsZero())
	assert.False(t, SiteInviteCommissionWithdrawable(&frozenRow, now))

	assert.Equal(t, model.AdtySiteCommissionAvailable, normalRow.Status)
	assert.True(t, SiteInviteCommissionWithdrawable(&normalRow, now))

	// Release job skips risk_held until owner explicitly releases it.
	require.NoError(t, ReleaseAdtySiteInviteCommissions(now.Add(time.Minute)))
	require.NoError(t, db.First(&frozenRow, frozenRow.Id).Error)
	assert.Equal(t, model.AdtySiteCommissionRiskHeld, frozenRow.Status)

	// Owner explicitly releases the risk_held commission -> covered & immediately available.
	require.NoError(t, ReleaseSiteInviteRiskHeldCommission(site.Id, owner.Id, frozenRow.Id, "cleared", "198.51.100.9", "owner-ui"))
	require.NoError(t, db.First(&frozenRow, frozenRow.Id).Error)
	assert.Equal(t, model.AdtySiteCommissionAvailable, frozenRow.Status)
	assert.True(t, SiteInviteCommissionWithdrawable(&frozenRow, now.Add(time.Minute)))
}

func TestR37IsAdtyReferralEnabledForSiteWithRateOverride(t *testing.T) {
	db := setupR37TestDB(t)
	owner, site := createR37SiteWithOwner(t, db, "zero_rate_override")
	vip := createR37SiteCustomer(t, db, site.Id, "vip_affiliate")

	// 1. Enabled with 0% site rate and no affiliate override -> disabled.
	require.NoError(t, SaveAdtyOwnerSiteMarketingWithAudit(site.Id, owner.Id, AdtySiteMarketingUpdate{
		Rate:               strPtr("0"),
		PendingCoverageCap: strPtr("500"),
		ObservationDays:    strPtr("7"),
		Enabled:            boolPtr(true),
	}, "127.0.0.1", "test"))
	assert.False(t, IsAdtyReferralEnabledForSite(site.Id))

	// 2. Active affiliate with 0% override -> still disabled.
	require.NoError(t, UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, vip.Id, AdtySiteAffiliateUpdateInput{
		Action: "rate",
		Rate:   float64Ptr(0),
	}, "127.0.0.1", "test"))
	assert.False(t, IsAdtyReferralEnabledForSite(site.Id))

	// 3. Active affiliate with positive override (15%) -> enabled.
	require.NoError(t, UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, vip.Id, AdtySiteAffiliateUpdateInput{
		Action: "rate",
		Rate:   float64Ptr(15),
	}, "127.0.0.1", "test"))
	assert.True(t, IsAdtyReferralEnabledForSite(site.Id))

	// 4. Disabling that affiliate -> disabled again.
	require.NoError(t, UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, vip.Id, AdtySiteAffiliateUpdateInput{
		Action: "disable",
		Reason: "paused",
	}, "127.0.0.1", "test"))
	assert.False(t, IsAdtyReferralEnabledForSite(site.Id))

	// 5. Restoring affiliate restores enabled, while turning site Enabled=false turns it off.
	require.NoError(t, UpdateAdtyOwnerSiteReferralAffiliate(site.Id, owner.Id, vip.Id, AdtySiteAffiliateUpdateInput{
		Action: "restore",
		Reason: "restored",
	}, "127.0.0.1", "test"))
	assert.True(t, IsAdtyReferralEnabledForSite(site.Id))

	require.NoError(t, SaveAdtyOwnerSiteMarketingWithAudit(site.Id, owner.Id, AdtySiteMarketingUpdate{
		Enabled: boolPtr(false),
	}, "127.0.0.1", "test"))
	assert.False(t, IsAdtyReferralEnabledForSite(site.Id))

	// 6. Nil model.DB must return false safely without panicking.
	savedDB := model.DB
	model.DB = nil
	assert.False(t, IsAdtyReferralEnabledForSite(site.Id))
	model.DB = savedDB
}
