package service

import (
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTeamProfileAndTopupDoNotAddBonus(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	leader := referralUser(t, db, "gap-leader")
	created, err := CreateAdtyTeam(leader.Id, 0, "Ops", decimal.NewFromInt(1))
	require.NoError(t, err)
	require.NoError(t, UpdateAdtyTeamProfile(leader.Id, created.Team.Id, "Ops Renamed"))
	var team model.AdtyTeam
	require.NoError(t, db.First(&team, created.Team.Id).Error)
	require.Equal(t, "Ops Renamed", team.Name)
	member := referralUser(t, db, "gap-member")
	_, err = JoinAdtyTeam(member.Id, created.Team.Id)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	employee, err := CreateAdtyTeamEmployee(leader.Id, created.Team.Id, "gap-emp", "StrongPassword123!", "Gap Emp")
	require.NoError(t, err)
	member.Id = employee.UserId
	require.ErrorIs(t, UpdateAdtyTeamProfile(member.Id, created.Team.Id, "Stolen"), ErrAdtyTeamForbidden)
	require.NoError(t, db.First(&team, created.Team.Id).Error)
	require.Equal(t, "Ops Renamed", team.Name)
	require.NoError(t, DisableAdtyTeamMember(leader.Id, created.Team.Id, member.Id))
	require.Error(t, SpendAdtyTeam(member.Id, 1, "left-member"))
}

func TestStaffBalanceRequestNeedsApproval(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	owner := referralUser(t, db, "gap-owner")
	staffUser := referralUser(t, db, "gap-staff")
	customer := referralUser(t, db, "gap-customer")
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "gap-site", Status: model.AdtySiteStatusActive, AgreementVer: AdtyCurrentSiteAgreementVersion()}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(customer).Update("site_id", site.Id).Error)
	require.NoError(t, db.Create(&model.AdtySiteStaff{SiteId: site.Id, OwnerUserId: owner.Id, UserId: staffUser.Id, Status: "enabled", Role: "support"}).Error)
	fundStaffBalanceSite(t, db, site.Id, "gap")
	request, err := SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.RequireFromString("2"), "staff-bal-inc-1", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceIncrease))
	require.NoError(t, err)
	require.Equal(t, model.AdtyStaffBalancePending, request.Status)
	var before model.User
	require.NoError(t, db.First(&before, customer.Id).Error)
	require.Error(t, ReviewAdtyStaffBalanceRequest(staffUser.Id, "member", request.Id, true))
	require.NoError(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", request.Id, true))
	var after model.User
	require.NoError(t, db.First(&after, customer.Id).Error)
	require.EqualValues(t, before.Quota+1_000_000, after.Quota)
	require.Error(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", request.Id, true))
	root := referralUser(t, db, "gap-root")
	second, err := SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceDecrease, decimal.RequireFromString("1"), "staff-bal-dec-1", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceDecrease))
	require.NoError(t, err)
	require.NoError(t, ReviewAdtyStaffBalanceRequest(root.Id, "root", second.Id, true))
}

func TestFundChecksWindowsGlossaryAndEarningView(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	require.True(t, ReconcileOperatingWallet(decimal.RequireFromString("10"), decimal.RequireFromString("10")).IsZero())
	require.Equal(t, []string{"balance_ledger_gap", "open_shortfall_hold", "margin_anomaly"}, AdtyFundAnomalies(2, 1, decimal.RequireFromString("0.5")))
	require.Len(t, AdtyHealthWindowStarts(time.Date(2026, 11, 13, 12, 7, 0, 0, time.UTC)), 96)
	require.NoError(t, SaveAdtyGlossaryTerm("充值", "en", "top-up"))
	var terms int64
	require.NoError(t, db.Model(&model.AdtyGlossaryTerm{}).Count(&terms).Error)
	require.EqualValues(t, 1, terms)
	require.NoError(t, HideAdtyNotificationForUser(4, "notice-hide"))
	filtered, err := hideAdtyNotifications(4, AdtyNotificationInbox{Announcements: []map[string]any{{"read_key": "notice-hide"}, {"read_key": "keep"}}})
	require.NoError(t, err)
	require.Len(t, filtered.Announcements, 1)
	require.Equal(t, "keep", filtered.Announcements[0]["read_key"])
}

func TestSiteReferralTeamFloorAndDuplicateRefund(t *testing.T) {
	prepareReferralDialectDB(t, "sqlite")
	require.NoError(t, SetSiteReferralRate(4, decimal.RequireFromString("0.10")))
	require.True(t, SiteInviteCommissionUSD(decimal.NewFromInt(80), decimal.RequireFromString("0.10")).Equal(decimal.RequireFromString("8")))
	require.NoError(t, recordAdtyAnomalyDisposition("duplicate_refund", "T-1", "reviewing", "second ledger", 1))
}

func TestStaffDeleteRevokesSession(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	owner := referralUser(t, db, "staff-del-owner")
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "staff-del", Status: model.AdtySiteStatusActive, AgreementVer: AdtyCurrentSiteAgreementVersion()}
	require.NoError(t, db.Create(&site).Error)
	created, err := CreateAdtyStaff(owner.Id, site.Id, AdtyStaffInput{Username: "staff-del", DisplayName: "Support", Password: "employee-password-42", Role: "support"})
	require.NoError(t, err)
	updated, err := MutateAdtyStaff(owner.Id, site.Id, created.Id, AdtySiteOwnerActionStaffUpdate, AdtyStaffInput{DisplayName: "Finance", Role: "finance", Status: "enabled", ExpectedVersion: created.Version})
	require.NoError(t, err)
	require.Equal(t, "finance", updated.Role)
	var before model.User
	require.NoError(t, db.First(&before, created.UserId).Error)
	_, err = MutateAdtyStaff(owner.Id, site.Id, created.Id, AdtySiteOwnerActionStaffDelete, AdtyStaffInput{ExpectedVersion: updated.Version})
	require.NoError(t, err)
	var staff model.AdtySiteStaff
	require.NoError(t, db.First(&staff, created.Id).Error)
	require.Equal(t, "deleted", staff.Status)
	var after model.User
	require.NoError(t, db.First(&after, created.UserId).Error)
	require.Greater(t, after.AuthVersion, before.AuthVersion)
	require.Equal(t, common.UserStatusDisabled, after.Status)
}

func TestPricingWalletsReferralAndReplayGuards(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	require.True(t, QuotaToUSD(500000).Equal(decimal.NewFromInt(1)))
	owner := referralUser(t, db, "price-owner")
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "price-site", Status: model.AdtySiteStatusActive, CostFactor: decimalPtr("0.80")}
	require.NoError(t, db.Create(&site).Error)
	factor := decimal.RequireFromString("0.75")
	_, err := AdjustAdtySite(site.Id, owner.Id, AdtySiteAdjust{CostFactor: &factor, Reason: "supply"})
	require.NoError(t, err)
	require.NoError(t, SetSiteReferralRate(site.Id, decimal.RequireFromString("0.08")))
	inviter := referralUser(t, db, "site-inviter")
	invitee := referralUser(t, db, "site-invitee")
	require.NoError(t, db.Model(inviter).Update("site_id", site.Id).Error)
	require.NoError(t, db.Model(invitee).Update("site_id", site.Id).Error)
	require.NoError(t, BindSiteInviteFromIP(site.Id, inviter.Id, invitee.Id, ""))
	require.Error(t, BindSiteInviteFromIP(site.Id, owner.Id, invitee.Id, ""))
	_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(5), AdtySiteWalletChange{Type: "earn", IdempotencyKey: "earn-ledger"})
	require.NoError(t, err)
	_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletMargin, decimal.NewFromInt(7), AdtySiteWalletChange{Type: "margin", IdempotencyKey: "margin-ledger"})
	require.NoError(t, err)
	earnings, _, err := ListAdtySiteWalletLedger(site.Id, model.AdtySiteWalletEarnings, 0, 10)
	require.NoError(t, err)
	require.NotEmpty(t, earnings)
	margin, _, err := ListAdtySiteWalletLedger(site.Id, model.AdtySiteWalletMargin, 0, 10)
	require.NoError(t, err)
	require.NotEmpty(t, margin)
}

func TestPaymentCallbackReplayDoesNotCreditTwice(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	owner := referralUser(t, db, "replay-pay-owner")
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "replay-pay", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)
	user := siteCustomer(t, db, &site, "replay-pay-user")
	siteTopUp(t, user, &site, "EPU-replay-pay", 20, "0")
	var before model.User
	require.NoError(t, db.First(&before, user.Id).Error)
	already, err := model.CompleteAdtyTopUp("EPU-replay-pay", model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(20), FiatAmount: decimal.NewFromInt(20),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)
	require.True(t, already)
	var after model.User
	require.NoError(t, db.First(&after, user.Id).Error)
	require.Equal(t, before.Quota, after.Quota)
}

func TestRemainingPriceCommissionAndIsolation(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	owner := referralUser(t, db, "remain-owner")
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "remain-site", Status: model.AdtySiteStatusActive, MaxMarkup: decimalPtr("2")}
	require.NoError(t, db.Create(&site).Error)
	markup := decimal.RequireFromString("1.2")
	_, err := SaveAdtySitePricing(AdtySitePricingInput{SiteId: site.Id, DefaultMarkup: &markup, Now: 1})
	require.NoError(t, err)
	leader := referralUser(t, db, "remain-leader")
	require.NoError(t, db.Model(leader).Update("site_id", site.Id).Error)
	team, err := CreateAdtyTeam(leader.Id, site.Id, "Remain", decimal.RequireFromString("0.90"))
	require.NoError(t, err)
	require.True(t, team.Team.Discount.Equal(decimal.RequireFromString("0.90")))
	main, err := CreateAdtyTeam(leader.Id, 0, "Main", decimal.NewFromInt(1))
	require.Error(t, err)
	_ = main
	other := referralUser(t, db, "remain-main-leader")
	_, err = CreateAdtyTeam(other.Id, 0, "MainAgree", decimal.NewFromInt(1))
	require.NoError(t, err)
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	require.NoError(t, CompleteAdtyTodo(owner.Id, "severe-1"))
}

func TestSiteRegisterDocsAndRollback(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	cfg := adty_setting.GetResellerSetting()
	saved := *cfg
	t.Cleanup(func() { *cfg = saved })
	*cfg = adty_setting.DefaultResellerSetting()
	cfg.Enabled, cfg.RootDomains = true, []string{"sites.example.com"}
	cfg.CostFactor, cfg.MinCostFactor, cfg.MaxMarkup = new(0.8), new(0.5), new(3.0)
	cfg.MaxSiteBonusExtra, cfg.TrialGrantMaxUSD, cfg.TrialGrantDailyUSD = new(0.0), new(5.0), new(10.0)
	owner := referralUser(t, db, "open-owner")
	_, err := Referral.EnsureAffiliate(owner.Id)
	require.NoError(t, err)
	site, err := OpenAdtySite(owner.Id, "north", model.AdtySiteOpenedByAdmin, AdtySitePreset{})
	require.NoError(t, err)
	require.Equal(t, owner.Id, site.OwnerUserId)
	// Official site registration is POST /api/user/register with a password.
	// A passwordless RegisterOnSite factory is not a landed product API.
	created := &model.User{
		Username: "north-user", Password: "correct-horse-1", Email: "north-user@example.com",
		SiteId: site.Id, Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(created).Error)
	require.Equal(t, site.Id, created.SiteId)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&model.User{Username: "rollback-user", Email: "rollback@example.com"}).Error; err != nil {
			return err
		}
		return common.NewMessage("rollback")
	}))
	var count int64
	require.NoError(t, db.Model(&model.User{}).Where("username = ?", "rollback-user").Count(&count).Error)
	require.Zero(t, count)
	for _, path := range []string{
		"../web/src/features/adty-docs/content/zh-CN/roles.md",
		"../web/src/features/adty-docs/content/en/roles.md",
		"../web/src/features/adty-docs/content/en/openai-sdk.md",
		"../web/src/features/adty-docs/content/en/claude-code.md",
		"../web/src/features/adty-docs/content/zh-CN/models.md",
	} {
		body, err := os.ReadFile(path)
		require.NoError(t, err, path)
		require.NotEmpty(t, body, path)
	}
	zh, err := os.ReadFile("../web/src/features/adty-docs/content/zh-CN/roles.md")
	require.NoError(t, err)
	for _, heading := range []string{"普通用户", "团队负责人", "团队成员", "分站站长", "分站员工", "主站管理员"} {
		require.Contains(t, string(zh), heading)
	}
	item, ok := AnnouncementDetail([]map[string]any{{"read_key": "notice-1", "title": "维护"}}, "notice-1")
	require.True(t, ok)
	require.Equal(t, "维护", item["title"])
}

func TestModelsGroupsOrdersAndLimits(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	owner := referralUser(t, db, "orders-owner")
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "orders-site", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Create(&model.AdtyStaffBalanceRequest{SiteId: site.Id, StaffUserId: owner.Id, CustomerUserId: owner.Id, Direction: model.AdtyStaffBalanceIncrease, AmountUSD: decimal.NewFromInt(1), Status: model.AdtyStaffBalancePending}).Error)
	requests, err := ListStaffBalanceRequests(site.Id)
	require.NoError(t, err)
	require.Len(t, requests, 1)
}

func TestAccountOrdersTranslationAndHealth(t *testing.T) {
	prepareReferralDialectDB(t, "sqlite")
	parts := WalletUSDPartsFromQuota(500000, 250000)
	require.True(t, parts.Principal.Equal(decimal.NewFromInt(1)))
	require.True(t, parts.Bonus.Equal(decimal.RequireFromString("0.5")))
	require.Equal(t, "no_sample", HealthSampleStatus(0, 0, 100, 60))
	require.Equal(t, "stale", HealthSampleStatus(3, 10, 100, 60))
	require.Equal(t, "ok", HealthSampleStatus(3, 90, 100, 60))
}

func TestPriceRevisionAndBalanceGap(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	require.NoError(t, HideAdtyNotificationForUser(9, "notice-1"))
	require.NoError(t, HideAdtyNotificationForUser(9, "notice-1"))
	var hides int64
	require.NoError(t, db.Model(&model.AdtyNotificationHide{}).Where("user_id = ?", 9).Count(&hides).Error)
	require.EqualValues(t, 1, hides)
	var hideAudits int64
	require.NoError(t, model.LOG_DB.Model(&model.AuditLog{}).Where("action = ? AND object_id = ?", "adty.notification.hide", "notice-1").Count(&hideAudits).Error)
	require.EqualValues(t, 1, hideAudits)
}

func TestHideAdtyNotificationWritesOfficialAuditOnce(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	require.NoError(t, model.MigrateAuditLogs())
	user := referralUser(t, db, "notice-hide-actor")
	site := model.AdtySite{OwnerUserId: user.Id, Subdomain: "notice-hide-site", Status: model.AdtySiteStatusActive, AgreementVer: AdtyCurrentSiteAgreementVersion()}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(user).Update("site_id", site.Id).Error)
	user.SiteId = site.Id
	publication := model.AdtyNotificationPublication{
		IssuerSiteId: 0, PublisherId: 1, PublisherName: "Root",
		Audience: "all", TargetSiteId: site.Id, TargetSiteName: site.Subdomain,
		Kind: "notice", Title: "Maintenance", Content: "window",
		Business: "ops", Status: "published", Revision: 1,
		ReadKey: "notice-audit-1", PublishedAt: 1, UpdatedAt: 1, UpdatedBy: 1,
	}
	require.NoError(t, db.Create(&publication).Error)

	require.Error(t, HideAdtyNotificationForUser(0, publication.ReadKey))
	require.Error(t, HideAdtyNotificationForUser(user.Id, "  "))
	var beforeAudits int64
	require.NoError(t, model.LOG_DB.Model(&model.AuditLog{}).Where("action = ?", "adty.notification.hide").Count(&beforeAudits).Error)
	require.Zero(t, beforeAudits)

	require.NoError(t, HideAdtyNotificationForUser(user.Id, publication.ReadKey))
	require.NoError(t, HideAdtyNotificationForUser(user.Id, publication.ReadKey))

	var hides int64
	require.NoError(t, db.Model(&model.AdtyNotificationHide{}).Where("user_id = ? AND key = ?", user.Id, publication.ReadKey).Count(&hides).Error)
	require.EqualValues(t, 1, hides)

	var audits []model.AuditLog
	require.NoError(t, model.LOG_DB.Where("action = ? AND object_id = ?", "adty.notification.hide", publication.ReadKey).Find(&audits).Error)
	require.Len(t, audits, 1)
	row := audits[0]
	require.NotNil(t, row.SiteId)
	require.Equal(t, site.Id, *row.SiteId)
	require.Equal(t, "notification", row.ObjectType)
	require.Equal(t, publication.ReadKey, row.ObjectId)
	require.Equal(t, "adty.notification.hide", row.Action)
	require.Equal(t, model.AuditResultSuccess, row.Result)
	require.True(t, row.Success)
	require.Equal(t, user.Id, row.UserId)
	require.Equal(t, "notice-hide-actor", row.Username)
	require.Equal(t, common.RoleCommonUser, row.ActorRole)

	var still model.AdtyNotificationPublication
	require.NoError(t, db.First(&still, publication.Id).Error)
	require.Equal(t, "published", still.Status)
	require.Equal(t, publication.ReadKey, still.ReadKey)

	requestSite := model.AdtySite{Id: site.Id + 100}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set(AdtySiteContextKey, &requestSite)
	require.NoError(t, HideAdtyNotificationForUserFromRequest(ctx, user.Id, "notice-audit-ctx"))
	var fromRequest model.AuditLog
	require.NoError(t, model.LOG_DB.Where("action = ? AND object_id = ?", "adty.notification.hide", "notice-audit-ctx").Take(&fromRequest).Error)
	require.NotNil(t, fromRequest.SiteId)
	require.Equal(t, requestSite.Id, *fromRequest.SiteId)
}

func staffBalanceBatchRemaining(t *testing.T, db *gorm.DB, userID int) int64 {
	t.Helper()
	var rows []model.AdtyFundBatch
	require.NoError(t, db.Where("user_id = ?", userID).Find(&rows).Error)
	var total int64
	for _, row := range rows {
		total += row.Remaining
	}
	return total
}

func staffBalanceLedgers(t *testing.T, db *gorm.DB, userID int) []model.AdtyBalanceLedger {
	t.Helper()
	var rows []model.AdtyBalanceLedger
	require.NoError(t, db.Where("user_id = ? AND ref_type = ?", userID, "staff_balance").Order("id").Find(&rows).Error)
	return rows
}

func seedStaffBalanceActors(t *testing.T, db *gorm.DB, prefix string) (owner, staffUser, customer *model.User, site model.AdtySite) {
	t.Helper()
	owner = referralUser(t, db, prefix+"-owner")
	staffUser = referralUser(t, db, prefix+"-staff")
	customer = referralUser(t, db, prefix+"-customer")
	site = model.AdtySite{OwnerUserId: owner.Id, Subdomain: prefix + "-site", Status: model.AdtySiteStatusActive, AgreementVer: AdtyCurrentSiteAgreementVersion()}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(customer).Update("site_id", site.Id).Error)
	require.NoError(t, db.Create(&model.AdtySiteStaff{SiteId: site.Id, OwnerUserId: owner.Id, UserId: staffUser.Id, Status: "enabled", Role: "support"}).Error)
	fundStaffBalanceSite(t, db, site.Id, prefix)
	return owner, staffUser, customer, site
}

// fundStaffBalanceSite 给站点的运营钱包放进足够的钱。
//
// 员工给客户加款必须有出资（`ISS-R4-0075`）。放进钱让既有用例继续只测
// 「加款账务本身」；「钱包为空必须被拒」由
// `adty_staff_balance_funding_pg_test.go` 单独覆盖。
func fundStaffBalanceSite(t *testing.T, db *gorm.DB, siteID int, tag string) {
	t.Helper()
	require.NoError(t, EnsureAdtySiteWallets(siteID))
	_, err := ApplyAdtySiteWalletChange(siteID, model.AdtySiteWalletEarnings, decimal.NewFromInt(100), AdtySiteWalletChange{
		Type: "seed", RefType: "test", RefID: tag, Reason: "seed earnings balance for staff balance tests",
		IdempotencyKey: "seed-earnings-" + tag,
	})
	require.NoError(t, err)
}

func requireWalletMatchesStaffBatches(t *testing.T, db *gorm.DB, userID int) {
	t.Helper()
	var user model.User
	require.NoError(t, db.First(&user, userID).Error)
	require.EqualValues(t, user.Quota, staffBalanceBatchRemaining(t, db, userID))
}

func TestStaffBalanceApprovalWritesLedgerAndBatches(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := prepareReferralDialectDB(t, dialect)
			owner, staffUser, customer, _ := seedStaffBalanceActors(t, db, "staff-book-"+dialect)
			request, err := SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.RequireFromString("2"), "staff-book-inc", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceIncrease))
			require.NoError(t, err)
			require.NoError(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", request.Id, true))

			var after model.User
			require.NoError(t, db.First(&after, customer.Id).Error)
			require.EqualValues(t, 1_000_000, after.Quota)
			ledgers := staffBalanceLedgers(t, db, customer.Id)
			require.Len(t, ledgers, 1)
			require.Equal(t, model.AdtyLedgerTypeManualAdjust, ledgers[0].Type)
			require.Equal(t, "staff_balance", ledgers[0].RefType)
			require.Equal(t, strconv.FormatInt(request.Id, 10), ledgers[0].RefId)
			require.EqualValues(t, 1_000_000, ledgers[0].DeltaQuota)
			require.EqualValues(t, 0, ledgers[0].BalanceBefore)
			require.EqualValues(t, 1_000_000, ledgers[0].BalanceAfter)
			require.Equal(t, owner.Id, ledgers[0].OperatorId)
			var lots []model.AdtyFundBatch
			require.NoError(t, db.Where("user_id = ? AND source = ?", customer.Id, adtyStaffBalanceLedgerKey(*request)).Find(&lots).Error)
			require.Len(t, lots, 1)
			require.Equal(t, "principal", lots[0].Kind)
			require.EqualValues(t, 1_000_000, lots[0].Original)
			require.EqualValues(t, 1_000_000, lots[0].Remaining)
			requireWalletMatchesStaffBatches(t, db, customer.Id)
		})
	}
}

func TestStaffBalanceDeductRefusesInFlightHolds(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := prepareReferralDialectDB(t, dialect)
			owner, staffUser, customer, _ := seedStaffBalanceActors(t, db, "staff-hold-"+dialect)
			credit, err := SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.RequireFromString("2"), "staff-hold-inc", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceIncrease))
			require.NoError(t, err)
			require.NoError(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", credit.Id, true))
			require.NoError(t, db.Model(customer).Update("quota_pending", -600_000).Error)

			debit, err := SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceDecrease, decimal.RequireFromString("1"), "staff-hold-dec", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceDecrease))
			require.NoError(t, err)
			err = ReviewAdtyStaffBalanceRequest(owner.Id, "owner", debit.Id, true)
			require.Error(t, err)
			require.Equal(t, "Customer balance is not enough", err.Error())

			var after model.User
			require.NoError(t, db.First(&after, customer.Id).Error)
			require.EqualValues(t, 1_000_000, after.Quota)
			require.EqualValues(t, -600_000, after.QuotaPending)
			require.Len(t, staffBalanceLedgers(t, db, customer.Id), 1)
			require.EqualValues(t, 1_000_000, staffBalanceBatchRemaining(t, db, customer.Id))
			var pending model.AdtyStaffBalanceRequest
			require.NoError(t, db.First(&pending, debit.Id).Error)
			require.Equal(t, model.AdtyStaffBalancePending, pending.Status)
		})
	}
}

func TestStaffBalanceReviewReplayDoesNotDoubleBook(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := prepareReferralDialectDB(t, dialect)
			owner, staffUser, customer, _ := seedStaffBalanceActors(t, db, "staff-replay-"+dialect)
			first, err := SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.RequireFromString("2"), "staff-replay-inc", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceIncrease))
			require.NoError(t, err)
			again, err := SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.RequireFromString("2"), "staff-replay-inc", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceIncrease))
			require.NoError(t, err)
			require.Equal(t, first.Id, again.Id)
			_, err = SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.RequireFromString("3"), "staff-replay-inc", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceIncrease))
			require.ErrorIs(t, err, model.ErrAdtyIdempotencyConflict)
			_, err = SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.RequireFromString("2"), "", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceIncrease))
			require.Error(t, err)

			require.NoError(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", first.Id, true))
			require.Error(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", first.Id, true))
			replayed, err := SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.RequireFromString("2"), "staff-replay-inc", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceIncrease))
			require.NoError(t, err)
			require.Equal(t, first.Id, replayed.Id)
			require.Equal(t, model.AdtyStaffBalanceApproved, replayed.Status)

			var after model.User
			require.NoError(t, db.First(&after, customer.Id).Error)
			require.EqualValues(t, 1_000_000, after.Quota)
			require.Len(t, staffBalanceLedgers(t, db, customer.Id), 1)
			requireWalletMatchesStaffBatches(t, db, customer.Id)
		})
	}
}

func TestStaffBalanceDecreaseConsumesBatches(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := prepareReferralDialectDB(t, dialect)
			owner, staffUser, customer, _ := seedStaffBalanceActors(t, db, "staff-dec-"+dialect)
			credit, err := SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.RequireFromString("2"), "staff-dec-inc", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceIncrease))
			require.NoError(t, err)
			require.NoError(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", credit.Id, true))
			debit, err := SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceDecrease, decimal.RequireFromString("1"), "staff-dec-dec", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceDecrease))
			require.NoError(t, err)
			require.NoError(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", debit.Id, true))

			var after model.User
			require.NoError(t, db.First(&after, customer.Id).Error)
			require.EqualValues(t, 500_000, after.Quota)
			ledgers := staffBalanceLedgers(t, db, customer.Id)
			require.Len(t, ledgers, 2)
			require.EqualValues(t, -500_000, ledgers[1].DeltaQuota)
			require.EqualValues(t, 1_000_000, ledgers[1].BalanceBefore)
			require.EqualValues(t, 500_000, ledgers[1].BalanceAfter)
			requireWalletMatchesStaffBatches(t, db, customer.Id)
		})
	}
}

func TestBindSiteInviteFromIPSerializesWithFirstPaymentPG(t *testing.T) {
	db := announcementTestDB(t)
	pgtest.RecordServerVersion(t, db)

	owner := ticketTestUser(t, db, "inv-race-owner", 1)
	site := model.AdtySite{
		OwnerUserId: owner.Id, Subdomain: "inv-race-site", RootDomain: "example.com",
		Status: model.AdtySiteStatusActive, AgreementVer: AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	inviter := ticketTestUser(t, db, "inv-race-inviter", 1)
	customer := ticketTestUser(t, db, "inv-race-customer", 1)
	require.NoError(t, db.Model(&model.User{}).Where("id IN ?", []int{inviter.Id, customer.Id}).Update("site_id", site.Id).Error)
	require.NoError(t, SetSiteReferralRate(site.Id, decimal.RequireFromString("0.1")))

	tradeNo := "INV-RACE-" + strconv.Itoa(customer.Id)
	require.NoError(t, model.CreateAdtyTopUp(
		&model.TopUp{
			UserId: customer.Id, Amount: 10, TradeNo: tradeNo,
			PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt,
			Status: common.TopUpStatusPending,
		},
		&model.AdtyTopupOrder{
			TradeNo: tradeNo, SiteId: site.Id, UserId: customer.Id,
			Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
			FaceUSD: decimal.NewFromInt(10), CreditQuota: 5_000_000,
			PayAmount: decimal.NewFromInt(10), PayCurrency: "USDT",
			TopupGroupRatio: decimal.NewFromInt(1), ExpireAt: time.Now().Unix() + 900,
		},
	))

	topUpUpdated := make(chan struct{})
	bindStarted := make(chan struct{})
	var hookOnce sync.Once
	const hookName = "test_bind_invite_pause_after_topup_update"
	require.NoError(t, db.Callback().Update().After("gorm:update").Register(hookName, func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Table != "top_ups" {
			return
		}
		hookOnce.Do(func() {
			close(topUpUpdated)
			<-bindStarted
			time.Sleep(120 * time.Millisecond)
		})
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(hookName) })

	var payErr error
	var bindErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _, _, _, payErr = func() (bool, int, *model.TopUp, *model.AdtyTopupOrder, error) {
			var done bool
			var credited int
			var tu *model.TopUp
			var ext *model.AdtyTopupOrder
			err := model.DB.Transaction(func(tx *gorm.DB) error {
				var err error
				done, credited, tu, ext, err = model.CompleteAdtyTopUpTx(tx, tradeNo, model.AdtyPaymentFacts{
					Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(10),
					FiatAmount: decimal.NewFromInt(10), PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
				})
				return err
			})
			return done, credited, tu, ext, err
		}()
	}()
	go func() {
		defer wg.Done()
		<-topUpUpdated
		close(bindStarted)
		bindErr = BindSiteInviteFromIP(site.Id, inviter.Id, customer.Id, "iphash-1")
	}()
	wg.Wait()

	require.NoError(t, payErr)
	var paidTopUp model.TopUp
	require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&paidTopUp).Error)
	require.Equal(t, common.TopUpStatusSuccess, paidTopUp.Status)

	var inviteCount int64
	require.NoError(t, db.Model(&model.AdtySiteInvite{}).Where("invitee_id = ?", customer.Id).Count(&inviteCount).Error)
	require.Error(t, bindErr, "concurrent invite bind after payment has already entered TopUp success transition must be refused")
	require.Zero(t, inviteCount, "customer with committed first paid order must never receive a post-payment invite binding")
}

func TestStaffBalanceReviewChecksLiveSiteAndCustomerScope(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := prepareReferralDialectDB(t, dialect)
			owner, staffUser, customer, site := seedStaffBalanceActors(t, db, "scope-"+dialect)
			otherOwner := referralUser(t, db, "scope-other-owner-"+dialect)
			root := referralUser(t, db, "scope-root-"+dialect)
			require.NoError(t, db.Model(&model.AdtySiteWallet{}).Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).Update("balance", decimal.NewFromInt(100)).Error)

			inc0, err := SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.NewFromInt(2), "scope-inc-init", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceIncrease))
			require.NoError(t, err)
			require.NoError(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", inc0.Id, true))

			// Case 1: customer migrated to main site -> pending decrease & increase must be refused even for owner/root.
			decMigrated, err := SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceDecrease, decimal.NewFromInt(1), "scope-dec-migrated", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceDecrease))
			require.NoError(t, err)
			incMigrated, err := SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.NewFromInt(1), "scope-inc-migrated", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceIncrease))
			require.NoError(t, err)
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", customer.Id).Update("site_id", 0).Error)

			require.Error(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", decMigrated.Id, true), "migrated customer must not be debited by old site owner")
			require.Error(t, ReviewAdtyStaffBalanceRequest(root.Id, "root", decMigrated.Id, true), "migrated customer must not be debited via old site request")
			require.Error(t, ReviewAdtyStaffBalanceRequest(root.Id, "root", incMigrated.Id, true), "migrated customer must not be credited via old site request")

			// Restore customer membership to test closing/closed & ownership transfer.
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", customer.Id).Update("site_id", site.Id).Error)
			require.NoError(t, db.Model(&model.AdtySite{}).Where("id = ?", site.Id).Update("status", model.AdtySiteStatusClosing).Error)
			require.Error(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", decMigrated.Id, true), "closing site must reject approval")
			require.Error(t, ReviewAdtyStaffBalanceRequest(root.Id, "root", decMigrated.Id, true), "closing site must reject root approval")

			require.NoError(t, db.Model(&model.AdtySite{}).Where("id = ?", site.Id).Update("status", model.AdtySiteStatusClosed).Error)
			require.Error(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", incMigrated.Id, true), "closed site must reject approval")
			require.Error(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", incMigrated.Id, false), "closed site must reject owner review")

			// Reactivate with transferred ownership -> old owner must fail, new owner succeeds.
			require.NoError(t, db.Model(&model.AdtySite{}).Where("id = ?", site.Id).Updates(map[string]any{
				"status": model.AdtySiteStatusActive, "owner_user_id": otherOwner.Id,
			}).Error)
			require.Error(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", decMigrated.Id, true), "former owner must be rejected after transfer")
			require.NoError(t, ReviewAdtyStaffBalanceRequest(otherOwner.Id, "owner", decMigrated.Id, true), "live new owner succeeds")
		})
	}
}

func TestStaffBalanceWorkflowWritesSiteAndOfficialAudit(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := prepareReferralDialectDB(t, dialect)
			owner, staffUser, customer, site := seedStaffBalanceActors(t, db, "audit-"+dialect)
			require.NoError(t, db.Model(&model.AdtySiteWallet{}).Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).Update("balance", decimal.NewFromInt(100)).Error)

			req, err := SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.NewFromInt(2), "audit-inc-1", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceIncrease))
			require.NoError(t, err)
			// Idempotent replay must not emit a duplicate submit audit.
			_, err = SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.NewFromInt(2), "audit-inc-1", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceIncrease))
			require.NoError(t, err)

			var siteAudits []model.AdtySiteAudit
			require.NoError(t, db.Where("site_id = ? AND action = ?", site.Id, "staff.balance_request").Find(&siteAudits).Error)
			require.Len(t, siteAudits, 1)
			require.Contains(t, siteAudits[0].NewValue, strconv.FormatInt(req.Id, 10))

			var officialAudits []model.AuditLog
			require.NoError(t, model.LOG_DB.Where("object_type = ? AND object_id = ?", "staff_balance_request", strconv.FormatInt(req.Id, 10)).Order("id").Find(&officialAudits).Error)
			require.Len(t, officialAudits, 1)
			require.Equal(t, "adty.staff.balance_request", officialAudits[0].Action)
			require.Equal(t, staffUser.Id, officialAudits[0].UserId)

			// Approve -> appends review audit linked to request and ledger with before/after quota.
			require.NoError(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", req.Id, true))
			require.NoError(t, db.Where("site_id = ? AND action = ?", site.Id, "staff.balance_review").Find(&siteAudits).Error)
			require.Len(t, siteAudits, 1)
			require.Contains(t, siteAudits[0].NewValue, `"ledger_id"`)

			require.NoError(t, model.LOG_DB.Where("object_type = ? AND object_id = ?", "staff_balance_request", strconv.FormatInt(req.Id, 10)).Order("id").Find(&officialAudits).Error)
			require.Len(t, officialAudits, 2)
			require.Equal(t, "adty.staff.balance_approve", officialAudits[1].Action)
			require.Equal(t, owner.Id, officialAudits[1].UserId)
			require.Contains(t, officialAudits[1].AfterValue, "before_quota")
			require.Contains(t, officialAudits[1].AfterValue, "after_quota")
			require.Contains(t, officialAudits[1].ApprovalRef, strconv.FormatInt(req.Id, 10))

			// Reject also audits even though no financial ledger entry is written.
			root := referralUser(t, db, "audit-root-"+dialect)
			req2, err := SubmitAdtyStaffBalanceRequest(staffUser.Id, customer.Id, model.AdtyStaffBalanceDecrease, decimal.NewFromInt(1), "audit-dec-2", staffProvenanceForTest(t, customer.Id, model.AdtyStaffBalanceDecrease))
			require.NoError(t, err)
			require.NoError(t, ReviewAdtyStaffBalanceRequest(root.Id, "root", req2.Id, false))
			require.NoError(t, model.LOG_DB.Where("object_type = ? AND object_id = ? AND action = ?", "staff_balance_request", strconv.FormatInt(req2.Id, 10), "adty.staff.balance_reject").Find(&officialAudits).Error)
			require.Len(t, officialAudits, 1)
			require.Equal(t, root.Id, officialAudits[0].UserId)
		})
	}
}

func TestTodoStateMachinePersistsAndPreservesHistory(t *testing.T) {
	db := prepareReferralDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, model.MigrateAuditLogs())
	user := referralUser(t, db, "todo-state-user")

	pub := model.AdtyNotificationPublication{
		IssuerSiteId:  0,
		PublisherId:   1,
		PublisherName: "Root",
		Audience:      "all_users",
		Kind:          "system",
		Title:         "Margin anomaly requires action",
		Content:       "Please replenish margin within 24 hours.",
		Business:      "margin_anomaly",
		Status:        "published",
		Revision:      1,
		ReadKey:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		PublishedAt:   100,
		UpdatedAt:     100,
		UpdatedBy:     1,
	}
	require.NoError(t, db.Create(&pub).Error)
	require.NoError(t, db.Create(&model.AdtyAnomalyDisposition{
		Kind:   "margin_anomaly",
		Ref:    pub.ReadKey,
		Status: "open",
		Note:   "margin below floor",
	}).Error)

	produced, err := UpsertAdtySevereTodo(user.Id, 0, pub.ReadKey, "", "", "", pub.Id)
	require.NoError(t, err)
	require.Equal(t, AdtyTodoStatusPending, produced.Status)
	require.Equal(t, pub.Id, produced.PublicationId)

	inProgress, err := TransitionAdtyTodo(nil, user.Id, pub.ReadKey, AdtyTodoStatusInProgress, "investigating")
	require.NoError(t, err)
	require.Equal(t, AdtyTodoStatusInProgress, inProgress.Status)

	require.NoError(t, CompleteAdtyTodo(user.Id, "todo:"+pub.ReadKey))
	// Idempotent replay does not duplicate history or error.
	require.NoError(t, CompleteAdtyTodo(user.Id, pub.ReadKey))
	// Completed todo cannot transition backward.
	_, err = TransitionAdtyTodo(nil, user.Id, pub.ReadKey, AdtyTodoStatusPending, "reopen")
	require.Error(t, err)

	openOnly, err := ListAdtyTodosForUser(user.Id, false)
	require.NoError(t, err)
	require.Empty(t, openOnly)

	allTodos, err := ListAdtyTodosForUser(user.Id, true)
	require.NoError(t, err)
	require.Len(t, allTodos, 1)
	require.Equal(t, AdtyTodoStatusCompleted, allTodos[0].Status)
	require.Positive(t, allTodos[0].CompletedAt)

	history, err := GetAdtyTodoHistory(user.Id, pub.ReadKey)
	require.NoError(t, err)
	require.Len(t, history, 3)
	require.Equal(t, AdtyTodoStatusPending, history[0].ToStatus)
	require.Equal(t, AdtyTodoStatusInProgress, history[1].ToStatus)
	require.Equal(t, AdtyTodoStatusCompleted, history[2].ToStatus)

	// Completing a severe todo preserves inbox history (never hides the notification).
	var hideCount int64
	require.NoError(t, db.Model(&model.AdtyNotificationHide{}).Where("user_id = ?", user.Id).Count(&hideCount).Error)
	require.Zero(t, hideCount)

	inbox, err := GetAdtyNotificationInbox(user.Id, nil, 0)
	require.NoError(t, err)
	require.NotEmpty(t, inbox.Announcements)
	detail, ok := AnnouncementDetail(inbox.Announcements, pub.ReadKey)
	require.True(t, ok)
	require.Equal(t, AdtyTodoStatusCompleted, detail["todo_status"])
	require.Equal(t, pub.ReadKey, detail["todo_key"])

	var disp model.AdtyAnomalyDisposition
	require.NoError(t, db.Where("ref = ?", pub.ReadKey).First(&disp).Error)
	require.Equal(t, "resolved", disp.Status)

	var todoAudits []model.AuditLog
	require.NoError(t, model.LOG_DB.Where("action = ? AND object_id = ?", "adty.todo.complete", pub.ReadKey).Find(&todoAudits).Error)
	require.Len(t, todoAudits, 1)
}

func TestSevereTodoIsProducedBeforeOwnedTransitionAndRejectsForeignKeys(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	publisher := referralUser(t, db, "todo-publisher")
	owner := referralUser(t, db, "todo-owner")
	reader := referralUser(t, db, "todo-reader")
	stranger := referralUser(t, db, "todo-stranger")
	siteOwner := referralUser(t, db, "todo-site-owner")
	site := model.AdtySite{OwnerUserId: siteOwner.Id, Subdomain: "todo-site", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)

	userNotice, err := SaveAdtyNotificationPublication(publisher.Id, nil, 0, AdtyNotificationInput{
		Title:        "User margin",
		Content:      "Replenish the user margin.",
		Kind:         "system",
		Audience:     "user",
		TargetUserId: owner.Id,
		Business:     "margin_anomaly",
	}, false)
	require.NoError(t, err)
	owned, err := ListAdtyTodosForUser(owner.Id, true)
	require.NoError(t, err)
	require.Len(t, owned, 1)
	require.Equal(t, AdtyTodoStatusPending, owned[0].Status)
	require.Equal(t, userNotice.ReadKey, owned[0].Key)

	siteNotice, err := SaveAdtyNotificationPublication(publisher.Id, nil, 0, AdtyNotificationInput{
		Title:        "Site margin",
		Content:      "Replenish the site margin.",
		Kind:         "system",
		Audience:     "site",
		TargetSiteId: site.Id,
		Business:     "severe_todo",
	}, false)
	require.NoError(t, err)
	siteTodos, err := ListAdtyTodosForUser(siteOwner.Id, false)
	require.NoError(t, err)
	require.Len(t, siteTodos, 1)
	require.Equal(t, siteNotice.ReadKey, siteTodos[0].Key)

	platform, err := SaveAdtyNotificationPublication(publisher.Id, nil, 0, AdtyNotificationInput{
		Title:    "Platform margin",
		Content:  "Visible after the reader opens the inbox.",
		Kind:     "system",
		Audience: "all_users",
		Business: "margin_anomaly",
	}, false)
	require.NoError(t, err)
	beforeInbox, err := ListAdtyTodosForUser(reader.Id, true)
	require.NoError(t, err)
	require.Empty(t, beforeInbox)
	_, err = GetAdtyNotificationInbox(reader.Id, nil, 0)
	require.NoError(t, err)
	afterInbox, err := ListAdtyTodosForUser(reader.Id, true)
	require.NoError(t, err)
	require.Len(t, afterInbox, 1)
	require.Equal(t, AdtyTodoStatusPending, afterInbox[0].Status)
	require.Equal(t, platform.ReadKey, afterInbox[0].Key)

	inProgress, err := MarkAdtyTodoInProgress(owner.Id, userNotice.ReadKey, "investigating")
	require.NoError(t, err)
	require.Equal(t, AdtyTodoStatusInProgress, inProgress.Status)
	_, err = MarkAdtyTodoInProgress(owner.Id, "missing-todo", "")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	require.Error(t, CompleteAdtyTodo(owner.Id, "   "))
	require.ErrorIs(t, CompleteAdtyTodo(stranger.Id, "missing-todo"), gorm.ErrRecordNotFound)
	require.EqualError(t, CompleteAdtyTodo(stranger.Id, userNotice.ReadKey), "Todo item belongs to another account")
	var strangerCount int64
	require.NoError(t, db.Model(&AdtyTodoItem{}).Where("user_id = ?", stranger.Id).Count(&strangerCount).Error)
	require.Zero(t, strangerCount)
	var missingCount int64
	require.NoError(t, db.Model(&AdtyTodoItem{}).Where("key = ?", "missing-todo").Count(&missingCount).Error)
	require.Zero(t, missingCount)

	require.NoError(t, CompleteAdtyTodo(owner.Id, userNotice.ReadKey))
	history, err := GetAdtyTodoHistory(owner.Id, userNotice.ReadKey)
	require.NoError(t, err)
	require.Len(t, history, 3)
	require.Equal(t, AdtyTodoStatusPending, history[0].ToStatus)
	require.Equal(t, AdtyTodoStatusInProgress, history[1].ToStatus)
	require.Equal(t, AdtyTodoStatusCompleted, history[2].ToStatus)
}

func TestBilingualNoticeTemplateManagementAndRendering(t *testing.T) {
	db := prepareReferralDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, model.MigrateAuditLogs())
	root := referralUser(t, db, "tpl-root")
	admin := referralUser(t, db, "tpl-admin")

	// Non-Root cannot save fixed system templates.
	_, err := SaveAdtyNoticeTemplate(admin.Id, common.RoleAdminUser, "channel_disabled", "标题", "内容 {{1}}", "Title", "Body {{1}}")
	require.Error(t, err)

	// Placeholder mismatch between Chinese and English is rejected.
	_, err = SaveAdtyNoticeTemplate(root.Id, common.RoleRootUser, "channel_disabled", "通道 {{1}} 告警", "原因 {{2}}", "Channel {{1}} alert", "Reason without second placeholder")
	require.Error(t, err)

	// Root saves valid bilingual template.
	saved, err := SaveAdtyNoticeTemplate(
		root.Id,
		common.RoleRootUser,
		"channel_disabled",
		"[自定义] 通道「{{1}}」（#{{2}}）已停用",
		"通道 {{1}} (#{{2}}) 因 {{3}} 自动停用",
		`[Custom] Channel "{{1}}" (#{{2}}) disabled`,
		`Channel {{1}} (#{{2}}) was disabled due to {{3}}`,
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, saved.Revision)

	zhTitle, zhBody, err := RenderAdtyNoticeTemplate("channel_disabled", "zh-CN", nil, "kling-pro", 12, "timeout")
	require.NoError(t, err)
	require.Equal(t, "[自定义] 通道「kling-pro」（#12）已停用", zhTitle)
	require.Equal(t, "通道 kling-pro (#12) 因 timeout 自动停用", zhBody)

	enTitle, enBody, err := RenderAdtyNoticeTemplate("channel_disabled", "en", nil, "kling-pro", 12, "timeout")
	require.NoError(t, err)
	require.Equal(t, `[Custom] Channel "kling-pro" (#12) disabled`, enTitle)
	require.Equal(t, `Channel kling-pro (#12) was disabled due to timeout`, enBody)

	biTitle, biBody, err := RenderAdtyBilingualNotice("channel_disabled", nil, "kling-pro", 12, "timeout")
	require.NoError(t, err)
	require.Contains(t, biTitle, zhTitle)
	require.Contains(t, biTitle, enTitle)
	require.Contains(t, biBody, zhBody)
	require.Contains(t, biBody, enBody)

	allTemplates, err := ListAdtyNoticeTemplates()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(allTemplates), 6)
}
