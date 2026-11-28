package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupR49TestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

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
		&model.AdtyGiftFulfillment{},
		&model.AdtyTeam{},
		&model.AdtyTeamMember{},
		&model.AdtyTelegramBinding{},
		&model.AdtyTelegramDelivery{},
		&model.AdtySiteReferralRate{},
		&model.AdtySiteReferralAffiliate{},
		&model.AdtySiteReferralAuditLog{},
		&model.AdtySiteInvite{},
		&model.AdtySiteInviteCommission{},
		&model.AdtySiteInviteWithdrawal{},
		&model.AdtySiteInviteWithdrawalItem{},
		&model.BillingOperation{},
		&model.AdtySiteEarningEvent{},
		&model.AdtySiteEarningDaily{},
		&model.AdtySiteEarningLine{},
		&model.AdtySiteSettlementSheet{},
	}
	tables = append(tables, model.ReferralTables()...)
	tables = append(tables, model.AdtySiteTables()...)
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
	common.CryptoSecret = "r49-test-secret-key"
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, model.UpdateOption("adty_referral_setting.default_rate", "10"))
	require.NoError(t, model.UpdateOption("adty_referral_setting.freeze_days", "0"))
	require.NoError(t, model.UpdateOption("adty_referral_setting.enabled", "true"))
	InvalidateAdtySiteCache()

	t.Cleanup(func() {
		_ = model.UpdateOption("adty_referral_setting.enabled", "false")
		_ = model.UpdateOption("adty_referral_setting.default_rate", "null")
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.CryptoSecret = oldCrypto
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
		InvalidateAdtySiteCache()
		_ = sqlDB.Close()
	})
	return db
}

func TestR49PartnerCookieAndActiveCodeValidation(t *testing.T) {
	db := setupR49TestDB(t)
	now := time.Now().Truncate(time.Second)

	// Signed partner cookie round-trip and strict isolation from user referral cookie.
	token, err := BuildAdtyPartnerCookie("P9X2K7M4", now)
	require.NoError(t, err)
	code, err := ParseAdtyPartnerCookie(token, now.Add(24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, "P9X2K7M4", code)

	// Expired (> 30 days) or tampered cookie must fail.
	_, err = ParseAdtyPartnerCookie(token, now.Add(31*24*time.Hour))
	assert.Error(t, err)
	_, err = ParseAdtyPartnerCookie(token+"x", now)
	assert.Error(t, err)

	// User referral cookie must NOT parse as partner cookie (distinct "partner:" prefix).
	userRefCookie, err := BuildReferralCookie("P9X2K7M4", now)
	require.NoError(t, err)
	_, err = ParseAdtyPartnerCookie(userRefCookie, now)
	assert.Error(t, err)

	// ValidateActivePartnerCode accepts active site and immediately rejects closing site.
	activeCode := "PTNRACTIVE"
	closingCode := "PTNRCLOSE1"
	require.NoError(t, db.Create(&model.AdtySite{
		OwnerUserId: 101, Subdomain: "site-active", RootDomain: "example.com",
		Status: model.AdtySiteStatusActive, PartnerCode: &activeCode,
	}).Error)
	require.NoError(t, db.Create(&model.AdtySite{
		OwnerUserId: 102, Subdomain: "site-closing", RootDomain: "example.com",
		Status: model.AdtySiteStatusClosing, PartnerCode: &closingCode,
	}).Error)

	found, err := ValidateActivePartnerCode("ptnractive")
	require.NoError(t, err)
	assert.Equal(t, "site-active", found.Subdomain)

	_, err = ValidateActivePartnerCode("PTNRCLOSE1")
	assert.Error(t, err)
}

func TestR49PartnerLeadLateBindRateAndStopLifecycle(t *testing.T) {
	db := setupR49TestDB(t)
	now := time.Now().Unix()

	root := model.User{Id: 1, Username: "root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&root).Error)

	codeA := "PARTNER001"
	codeB := "PARTNER002"
	siteA := model.AdtySite{
		OwnerUserId: 201, Subdomain: "alpha", RootDomain: "example.com",
		Status: model.AdtySiteStatusActive, PartnerCode: &codeA, CreatedAt: now - 3600,
	}
	siteB := model.AdtySite{
		OwnerUserId: 202, Subdomain: "beta", RootDomain: "example.com",
		Status: model.AdtySiteStatusActive, PartnerCode: &codeB, CreatedAt: now - 3600,
	}
	require.NoError(t, db.Create(&siteA).Error)
	require.NoError(t, db.Create(&siteB).Error)

	// 1. Submit partner lead via signed cookie code.
	cfg := adty_setting.DefaultResellerSetting()
	cfg.PartnerContactTelegram = "@adty_partner_official"
	lead, tg, err := SubmitAdtyPartnerLead(AdtyPartnerLeadInput{
		ContactName:     "Bob Reseller",
		ContactChannel:  "telegram",
		ContactValue:    "@bob_api",
		EstimatedVolume: "$10k/mo",
		Note:            "Ready to launch",
	}, "198.51.100.7", codeA)
	require.NoError(t, err)
	assert.Equal(t, siteA.Id, lead.ReferrerSiteId)
	assert.Equal(t, codeA, lead.ReferralCode)
	assert.Equal(t, model.AdtyPartnerLeadStatusPending, lead.Status)
	assert.Equal(t, "", tg) // options map has empty override until saved

	// Invalid explicit referral code is rejected.
	_, _, err = SubmitAdtyPartnerLead(AdtyPartnerLeadInput{
		ContactName:    "Invalid",
		ContactChannel: "email",
		ContactValue:   "bad@example.com",
		ReferralCode:   "NOPE9999",
	}, "198.51.100.8", "")
	assert.Error(t, err)

	// Reject lead works once and blocks re-processing.
	rejected, err := RejectAdtyPartnerLead(lead.Id, root.Id, "duplicate inquiry")
	require.NoError(t, err)
	assert.Equal(t, model.AdtyPartnerLeadStatusRejected, rejected.Status)
	_, err = RejectAdtyPartnerLead(lead.Id, root.Id, "again")
	assert.Error(t, err)

	// 2. Late-bind siteB -> siteA within 7 days succeeds once.
	rateFive := decimal.NewFromInt(5)
	updated, err := ManageAdtySitePartnerReferral(siteB.Id, root.Id, AdtySitePartnerManageInput{
		BindReferrerCode: codeA,
		RatePercent:      &rateFive,
		Reason:           "confirmed partner attribution",
	})
	require.NoError(t, err)
	assert.Equal(t, siteA.Id, updated.ReferrerSiteId)
	assert.True(t, updated.ReferrerLateBound)
	require.NotNil(t, updated.ReferrerRate)
	assert.True(t, updated.ReferrerRate.Equal(decimal.NewFromInt(5)))

	// Second late-bind attempt fails.
	_, err = ManageAdtySitePartnerReferral(siteB.Id, root.Id, AdtySitePartnerManageInput{
		BindReferrerCode: codeA,
	})
	assert.Error(t, err)

	// Rate edit works.
	rateSeven := decimal.RequireFromString("7.5")
	updated, err = ManageAdtySitePartnerReferral(siteB.Id, root.Id, AdtySitePartnerManageInput{
		RatePercent: &rateSeven,
		Reason:      "top partner incentive",
	})
	require.NoError(t, err)
	require.NotNil(t, updated.ReferrerRate)
	assert.True(t, updated.ReferrerRate.Equal(decimal.RequireFromString("7.5")))

	// Stop requires non-empty reason, then marks stopped.
	_, err = ManageAdtySitePartnerReferral(siteB.Id, root.Id, AdtySitePartnerManageInput{Stop: true})
	assert.Error(t, err)
	updated, err = ManageAdtySitePartnerReferral(siteB.Id, root.Id, AdtySitePartnerManageInput{
		Stop:   true,
		Reason: "policy violation",
	})
	require.NoError(t, err)
	assert.Greater(t, updated.ReferrerStoppedAt, int64(0))
	assert.Equal(t, "policy violation", updated.ReferrerStopReason)
}

func TestR49PrincipalConsumeFilterOnlyRealPaidPersonalTopups(t *testing.T) {
	db := setupR49TestDB(t)
	now := time.Now().Unix()

	// Seed orders covering all AdtyFundBatch.Source kinds.
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: "EPU_REAL_1", UserId: 10, SiteId: 0, Provider: model.PaymentProviderAdtyBEpusdt,
		Business: model.AdtyBusinessWalletTopup, TeamId: 0, FaceUSD: decimal.NewFromInt(100),
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: "EPY_TEAM_1", UserId: 10, SiteId: 0, Provider: model.PaymentProviderAdtyEpay,
		Business: model.AdtyBusinessTeamTopup, TeamId: 5, FaceUSD: decimal.NewFromInt(100),
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: "VCH_CODE_1", UserId: 10, SiteId: 0, Provider: "adty_voucher",
		Business: model.AdtyBusinessWalletTopup, TeamId: 0, FaceUSD: decimal.NewFromInt(100),
	}).Error)

	oneUSD := common.QuotaPerUnitDecimal().IntPart()
	deltas := []model.AdtyPrincipalBatchDelta{
		{BatchId: 1, SiteId: 0, Source: "EPU_REAL_1", Quota: 20 * oneUSD},               // INCLUDED ($20)
		{BatchId: 2, SiteId: 0, Source: "EPY_TEAM_1", Quota: 30 * oneUSD},               // EXCLUDED (team)
		{BatchId: 3, SiteId: 0, Source: "VCH_CODE_1", Quota: 40 * oneUSD},               // EXCLUDED (voucher)
		{BatchId: 4, SiteId: 0, Source: "adjust:abc123", Quota: 50 * oneUSD},            // EXCLUDED (manual adjust)
		{BatchId: 5, SiteId: 0, Source: "site-comm-cvt:1:k1", Quota: 10 * oneUSD},       // EXCLUDED (comm convert)
		{BatchId: 6, SiteId: 0, Source: "referral-withdrawal-9", Quota: 15 * oneUSD},    // EXCLUDED (referral convert)
		{BatchId: 7, SiteId: 0, Source: "legacy", Quota: 25 * oneUSD},                   // EXCLUDED (legacy)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return adtyRecordPrincipalConsumeEventsTx(tx, 10, "op-filter-1", deltas)
	}))

	var events []model.AdtyPrincipalConsumeEvent
	require.NoError(t, db.Where("operation_key = ?", "op-filter-1").Find(&events).Error)
	require.Len(t, events, 1)
	assert.Equal(t, 0, events[0].SiteId)
	assert.Equal(t, 10, events[0].UserId)
	assert.Equal(t, 20*oneUSD, events[0].Quota)
	assert.True(t, events[0].AmountUSD.Equal(decimal.NewFromInt(20)), "got %s", events[0].AmountUSD.String())
	_ = now
}

func TestR49UnifiedHourlySettlementOrderOffsetRiskHeldAndCloseFlush(t *testing.T) {
	db := setupR49TestDB(t)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	// Use a deterministic hour boundary: 2026-11-28 10:05 Asia/Shanghai settles 09:00..10:00.
	h1Start := time.Date(2026, 11, 28, 9, 0, 0, 0, loc)
	h1EventTime := h1Start.Add(15 * time.Minute).Unix()
	h1SettleNow := h1Start.Add(65 * time.Minute) // 10:05

	h2Start := h1Start.Add(time.Hour)
	h2EventTime := h2Start.Add(15 * time.Minute).Unix()
	h2SettleNow := h2Start.Add(65 * time.Minute) // 11:05

	h3Start := h2Start.Add(time.Hour)
	h3EventTime := h3Start.Add(15 * time.Minute).Unix()
	h3SettleNow := h3Start.Add(65 * time.Minute) // 12:05

	oneUSD := common.QuotaPerUnitDecimal().IntPart()

	// Create root, ownerA, ownerB, main users (mAffNormal, mAffFrozen, mInv1, mInv2), subsite users (sAffNormal, sAffFrozen, sInv1, sInv2).
	users := []model.User{
		{Id: 1, Username: "root", Role: common.RoleRootUser, Status: common.UserStatusEnabled},
		{Id: 10, Username: "owner_a", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
		{Id: 11, Username: "owner_b", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
		{Id: 20, Username: "main_aff_ok", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
		{Id: 21, Username: "main_aff_frozen", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
		{Id: 22, Username: "main_inv_1", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
		{Id: 23, Username: "main_inv_2", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
		{Id: 30, Username: "sub_aff_ok", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
		{Id: 31, Username: "sub_aff_frozen", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
		{Id: 32, Username: "sub_inv_1", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
		{Id: 33, Username: "sub_inv_2", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
	}
	for i := range users {
		require.NoError(t, db.Create(&users[i]).Error)
	}

	// Sites: A refers B at 5% permanent supply commission.
	codeA := "PARTNERA01"
	codeB := "PARTNERB01"
	rateFive := decimal.NewFromInt(5)
	siteA := model.AdtySite{
		OwnerUserId: 10, Subdomain: "site-a", RootDomain: "example.com",
		Status: model.AdtySiteStatusActive, PartnerCode: &codeA, CreatedAt: h1Start.Unix() - 7200,
	}
	require.NoError(t, db.Create(&siteA).Error)
	siteB := model.AdtySite{
		OwnerUserId: 11, Subdomain: "site-b", RootDomain: "example.com",
		Status: model.AdtySiteStatusActive, PartnerCode: &codeB,
		ReferrerSiteId: siteA.Id, ReferrerRate: &rateFive, ReferrerBoundAt: h1Start.Unix() - 3600,
		CreatedAt: h1Start.Unix() - 3600,
	}
	require.NoError(t, db.Create(&siteB).Error)

	// Mark subsite customers and enable site B referral (10%, cap $500, 0 observation days).
	for _, uid := range []int{30, 31, 32, 33} {
		require.NoError(t, db.Model(&model.User{}).Where("id = ?", uid).Update("site_id", siteB.Id).Error)
		require.NoError(t, db.Create(&model.AdtySiteCustomer{SiteId: siteB.Id, UserId: uid}).Error)
	}
	require.NoError(t, SaveAdtyOwnerSiteMarketingWithAudit(siteB.Id, 11, AdtySiteMarketingUpdate{
		Rate:               strPtr("0.10"),
		PendingCoverageCap: strPtr("500"),
		Enabled:            boolPtr(true),
	}, "127.0.0.1", "test"))

	// Seed site B operating wallet with $500 so hourly operating debit succeeds.
	require.NoError(t, db.Create(&model.AdtySiteWallet{
		SiteId: siteB.Id, Kind: model.AdtySiteWalletOperating, Balance: decimal.NewFromInt(500),
	}).Error)

	// Bind main-site referrals (mAffNormal -> mInv1, mAffFrozen -> mInv2).
	mAff1 := model.ReferralAffiliate{UserId: 20, InviteCode: "MAFFOK01", Status: model.ReferralAffiliateStatusApproved, AcquisitionEnabled: true, SettlementEnabled: true, WithdrawalEnabled: true}
	mAff2 := model.ReferralAffiliate{UserId: 21, InviteCode: "MAFFFR02", Status: model.ReferralAffiliateStatusApproved, AcquisitionEnabled: true, SettlementEnabled: false, WithdrawalEnabled: true}
	require.NoError(t, db.Create(&mAff1).Error)
	require.NoError(t, db.Create(&mAff2).Error)
	require.NoError(t, db.Create(&model.ReferralCommissionAccount{AffiliateId: mAff1.Id, UserId: 20}).Error)
	require.NoError(t, db.Create(&model.ReferralCommissionAccount{AffiliateId: mAff2.Id, UserId: 21}).Error)
	require.NoError(t, db.Create(&model.ReferralBinding{AffiliateId: mAff1.Id, InviterUserId: 20, InviteeUserId: 22, BindCode: mAff1.InviteCode}).Error)
	require.NoError(t, db.Create(&model.ReferralBinding{AffiliateId: mAff2.Id, InviterUserId: 21, InviteeUserId: 23, BindCode: mAff2.InviteCode}).Error)

	// Bind subsite B referrals (sub_aff_ok -> sub_inv_1, sub_aff_frozen -> sub_inv_2).
	require.NoError(t, BindSiteInviteFromIP(siteB.Id, 30, 32, "ip-s1"))
	require.NoError(t, BindSiteInviteFromIP(siteB.Id, 31, 33, "ip-s2"))
	require.NoError(t, UpdateAdtyOwnerSiteReferralAffiliate(siteB.Id, 11, 31, AdtySiteAffiliateUpdateInput{
		Action: "freeze_settlement",
		Reason: "risk check",
	}, "127.0.0.1", "test"))

	// === HOUR 1 EVENTS ===
	// Subsite B usage event: Customer charged $120 at k=1.2 (supply $100, spread $20), of which $24 charged came from adjust:* free credit (20% excluded -> partner supply eligible = $80!).
	require.NoError(t, db.Create(&model.AdtySiteEarningEvent{
		OperationKey:         "op-b-h1-1",
		RootOperationKey:     "op-b-h1-1",
		SiteId:               siteB.Id,
		SiteOwnerUserId:      11,
		UserId:               32,
		Day:                  "2026-11-28",
		Quota:                int(120 * oneUSD),
		PartnerExcludedQuota: 24 * oneUSD, // 20% of 120 -> excludes $20 of $100 supply, leaving $80 partner-eligible supply!
		Markup:               1.2,
		CostFactor:           0.7,
		CreatedAt:            h1EventTime,
	}).Error)
	// Subsite B operating principal spend event: -$100.
	require.NoError(t, db.Create(&model.AdtyOperatingPrincipalEvent{
		OperationKey: "op-b-h1-1", Direction: "spend", SiteId: siteB.Id, UserId: 32,
		Quota: -100 * oneUSD, CreatedAt: h1EventTime,
	}).Error)
	// Main site real paid consumption: mInv1 spends $100, mInv2 spends $50.
	require.NoError(t, db.Create(&model.AdtyPrincipalConsumeEvent{
		OperationKey: "op-m-h1-1", Direction: "spend", SiteId: 0, UserId: 22,
		Quota: 100 * oneUSD, AmountUSD: decimal.NewFromInt(100), CreatedAt: h1EventTime,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyPrincipalConsumeEvent{
		OperationKey: "op-m-h1-2", Direction: "spend", SiteId: 0, UserId: 23,
		Quota: 50 * oneUSD, AmountUSD: decimal.NewFromInt(50), CreatedAt: h1EventTime,
	}).Error)
	// Subsite B real paid consumption: sInv1 spends $80 (-> $8 commission covered from B's $20 hourly spread!), sInv2 spends $40 (-> $4 risk_held!).
	require.NoError(t, db.Create(&model.AdtyPrincipalConsumeEvent{
		OperationKey: "op-sb-h1-1", Direction: "spend", SiteId: siteB.Id, UserId: 32,
		Quota: 80 * oneUSD, AmountUSD: decimal.NewFromInt(80), CreatedAt: h1EventTime,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyPrincipalConsumeEvent{
		OperationKey: "op-sb-h1-2", Direction: "spend", SiteId: siteB.Id, UserId: 33,
		Quota: 40 * oneUSD, AmountUSD: decimal.NewFromInt(40), CreatedAt: h1EventTime,
	}).Error)

	// Before H1 settles: check real-time pending-hour estimates!
	estB, err := GetAdtySiteCurrentHourEstimate(siteB.Id)
	require.NoError(t, err)
	assert.True(t, estB.PendingEarningUSD.Equal(decimal.NewFromInt(20)))
	partnerPendTotal, partnerPendMap, err := GetAdtySitePartnerPendingHourUSD(siteA.Id)
	require.NoError(t, err)
	assert.True(t, partnerPendTotal.Equal(decimal.NewFromInt(4)), "5%% of $80 eligible supply = $4, got %s", partnerPendTotal)
	assert.True(t, partnerPendMap[siteB.Id].Equal(decimal.NewFromInt(4)))
	mainPend, err := GetAdtyMainReferralPendingHourUSD(mAff1.Id)
	require.NoError(t, err)
	assert.True(t, mainPend.Equal(decimal.NewFromInt(10)), "10%% of $100 = $10, got %s", mainPend)

	// Run Hourly Settlement for H1!
	summary1, err := RunAdtyHourlySettlement(h1SettleNow)
	require.NoError(t, err)
	assert.Equal(t, 1, summary1.SitesSettled)
	assert.Equal(t, 1, summary1.PartnerSettled)
	assert.Equal(t, 2, summary1.MainRefSettled)
	assert.Equal(t, 2, summary1.SiteRefSettled)

	// Verify Step 1: Site B operating wallet debited $100 ($500 -> $400), earnings wallet credited $20 spread (and $8 frozen for sInv1 in Step 4!).
	var opWalletB, earnWalletB, earnWalletA model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", siteB.Id, model.AdtySiteWalletOperating).First(&opWalletB).Error)
	require.NoError(t, db.Where("site_id = ? AND kind = ?", siteB.Id, model.AdtySiteWalletEarnings).First(&earnWalletB).Error)
	require.NoError(t, db.Where("site_id = ? AND kind = ?", siteA.Id, model.AdtySiteWalletEarnings).First(&earnWalletA).Error)
	assert.True(t, opWalletB.Balance.Equal(decimal.NewFromInt(400)), "operating balance=%s", opWalletB.Balance)
	assert.True(t, earnWalletB.Balance.Equal(decimal.NewFromInt(20)), "B earnings balance=%s", earnWalletB.Balance)
	assert.True(t, earnWalletB.FrozenUSD.Equal(decimal.NewFromInt(8)), "B earnings frozen for sInv1=%s", earnWalletB.FrozenUSD)

	// Verify Step 2: Site A earnings wallet credited $4 (5% of $80 eligible supply, excluding $20 adjust:* supply!).
	assert.True(t, earnWalletA.Balance.Equal(decimal.NewFromInt(4)), "A partner earnings=%s", earnWalletA.Balance)

	// Verify Step 3: Main referral mAff1 got $10 available immediately (0 freeze), mAff2 got $5 risk_held in PendingAmount.
	var acc1, acc2 model.ReferralCommissionAccount
	require.NoError(t, db.Where("affiliate_id = ?", mAff1.Id).First(&acc1).Error)
	require.NoError(t, db.Where("affiliate_id = ?", mAff2.Id).First(&acc2).Error)
	assert.InDelta(t, 10.0, acc1.AvailableAmount, 1e-6, "mAff1 avail=%v", acc1.AvailableAmount)
	assert.InDelta(t, 0.0, acc2.AvailableAmount, 1e-6)
	assert.InDelta(t, 5.0, acc2.PendingAmount, 1e-6, "mAff2 pending=%v", acc2.PendingAmount)

	var mCommHeld model.ReferralCommission
	require.NoError(t, db.Where("affiliate_id = ?", mAff2.Id).First(&mCommHeld).Error)
	assert.Equal(t, model.ReferralCommissionStatusRiskHeld, mCommHeld.Status)

	// Release main risk_held commission -> moves to available!
	require.NoError(t, ReleaseMainReferralRiskHeldCommission(mCommHeld.Id, 1, "verified clean", "127.0.0.1", "test"))
	require.NoError(t, db.Where("affiliate_id = ?", mAff2.Id).First(&acc2).Error)
	assert.InDelta(t, 5.0, acc2.AvailableAmount, 1e-6)
	assert.InDelta(t, 0.0, acc2.PendingAmount, 1e-6)

	// Verify Step 4: Subsite sInv1 commission is available ($8), sInv2 is risk_held ($4, 0 covered).
	var sComm1, sComm2 model.AdtySiteInviteCommission
	require.NoError(t, db.Where("site_id = ? AND invitee_id = ?", siteB.Id, 32).First(&sComm1).Error)
	require.NoError(t, db.Where("site_id = ? AND invitee_id = ?", siteB.Id, 33).First(&sComm2).Error)
	assert.Equal(t, model.AdtySiteCommissionAvailable, sComm1.Status)
	assert.True(t, sComm1.CoveredUSD.Equal(decimal.NewFromInt(8)))
	assert.Equal(t, model.AdtySiteCommissionRiskHeld, sComm2.Status)
	assert.True(t, sComm2.CoveredUSD.IsZero())

	// Void subsite risk_held commission -> clawed_back, no wallet touched.
	require.NoError(t, VoidSiteInviteRiskHeldCommission(siteB.Id, 11, sComm2.Id, "confirmed abuse", "127.0.0.1", "test"))
	require.NoError(t, db.First(&sComm2, sComm2.Id).Error)
	assert.Equal(t, model.AdtySiteCommissionClawedBack, sComm2.Status)

	// Re-running H1 is 100% idempotent (0 new settlements).
	summaryDup, err := RunAdtyHourlySettlement(h1SettleNow)
	require.NoError(t, err)
	assert.Zero(t, summaryDup.SitesSettled)
	assert.Zero(t, summaryDup.PartnerSettled)
	assert.Zero(t, summaryDup.MainRefSettled)
	assert.Zero(t, summaryDup.SiteRefSettled)

	// === HOUR 2 (NEGATIVE REFUND) & HOUR 3 (CARRY-FORWARD OFFSET) ===
	// Main site mInv1 gets a -$30 task refund in H2 -> -$3 negative commission carried forward in AdtyCommissionOffset.
	require.NoError(t, db.Create(&model.AdtyPrincipalConsumeEvent{
		OperationKey: "op-m-h2-refund", Direction: "refund", SiteId: 0, UserId: 22,
		Quota: -30 * oneUSD, AmountUSD: decimal.NewFromInt(-30), CreatedAt: h2EventTime,
	}).Error)
	_, err = RunAdtyHourlySettlement(h2SettleNow)
	require.NoError(t, err)
	require.NoError(t, db.Where("affiliate_id = ?", mAff1.Id).First(&acc1).Error)
	assert.InDelta(t, 10.0, acc1.AvailableAmount, 1e-6, "refund must not reduce below prior available")

	// In H3, mInv1 spends $50 -> gross $5 minus $3 carried offset = $2 net credited ($10 + $2 = $12)!
	require.NoError(t, db.Create(&model.AdtyPrincipalConsumeEvent{
		OperationKey: "op-m-h3-spend", Direction: "spend", SiteId: 0, UserId: 22,
		Quota: 50 * oneUSD, AmountUSD: decimal.NewFromInt(50), CreatedAt: h3EventTime,
	}).Error)
	_, err = RunAdtyHourlySettlement(h3SettleNow)
	require.NoError(t, err)
	require.NoError(t, db.Where("affiliate_id = ?", mAff1.Id).First(&acc1).Error)
	assert.InDelta(t, 12.0, acc1.AvailableAmount, 1e-6, "expected $12 after -$3 offset, got %v", acc1.AvailableAmount)

	// === ANONYMIZED OWNER PARTNER VIEW ===
	overviewA, err := GetAdtyOwnerPartnerOverview(&siteA)
	require.NoError(t, err)
	assert.Equal(t, codeA, overviewA.PartnerCode)
	assert.True(t, overviewA.LinkActive)
	assert.Equal(t, int64(1), overviewA.ReferredCount)
	assert.True(t, overviewA.TotalEarnedUSD.Equal(decimal.NewFromInt(4)))

	refSites, totalSites, err := ListAdtyOwnerPartnerSites(siteA.Id, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), totalSites)
	require.Len(t, refSites, 1)
	assert.Equal(t, fmt.Sprintf("Site #%d", siteB.Id), refSites[0].MaskedName)
	assert.NotContains(t, refSites[0].MaskedName, "site-b")

	// === CLOSE FLUSH ===
	// Add current-hour unsettled event on site B right before site B closes -> FlushAdtySiteBeforeClose settles it immediately!
	closeNow := h3SettleNow.Add(10 * time.Minute).Unix()
	require.NoError(t, db.Create(&model.AdtySiteEarningEvent{
		OperationKey:     "op-b-close-flush",
		RootOperationKey: "op-b-close-flush",
		SiteId:           siteB.Id,
		SiteOwnerUserId:  11,
		UserId:           32,
		Day:              "2026-11-28",
		Quota:            int(60 * oneUSD),
		Markup:           1.2,
		CostFactor:       0.7,
		CreatedAt:        closeNow - 30,
	}).Error)
	require.NoError(t, FlushAdtySiteBeforeClose(siteB.Id, closeNow))
	require.NoError(t, db.Where("site_id = ? AND kind = ?", siteA.Id, model.AdtySiteWalletEarnings).First(&earnWalletA).Error)
	// Prior $4 + 5% of $50 ($2.50) = $6.50!
	assert.True(t, earnWalletA.Balance.Equal(decimal.RequireFromString("6.5")), "expected $6.50 after close flush, got %s", earnWalletA.Balance)

	// StopAdtySitePartnerReferralsOnCloseTx stops siteA's links without modifying siteB's active status.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return StopAdtySitePartnerReferralsOnCloseTx(tx, siteA.Id, closeNow)
	}))
	var reloadedB model.AdtySite
	require.NoError(t, db.First(&reloadedB, siteB.Id).Error)
	assert.Equal(t, model.AdtySiteStatusActive, reloadedB.Status)
	assert.Equal(t, closeNow, reloadedB.ReferrerStoppedAt)
	assert.Equal(t, "referrer_site_closed", reloadedB.ReferrerStopReason)
}
