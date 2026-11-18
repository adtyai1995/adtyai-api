package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetReferralOverviewUsesSameSiteScopeAsBadges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.AdtySite{}, &model.AdtySiteStaff{}, &model.AdtyTeam{}, &model.AdtyTeamMember{}))
	require.NoError(t, db.AutoMigrate(model.ReferralTables()...))

	root := overviewTestUser(t, db, "ctrl-overview-root", common.RoleRootUser, 0)
	admin := overviewTestUser(t, db, "ctrl-overview-admin", common.RoleAdminUser, 0)
	owner := overviewTestUser(t, db, "ctrl-overview-owner", common.RoleCommonUser, 0)
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "ctrl-overview-site", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(owner).Update("site_id", site.Id).Error)

	mainUser := overviewTestUser(t, db, "ctrl-overview-main", common.RoleCommonUser, 0)
	siteUser := overviewTestUser(t, db, "ctrl-overview-site-user", common.RoleCommonUser, site.Id)
	mainInvitee := overviewTestUser(t, db, "ctrl-overview-main-invitee", common.RoleCommonUser, 0)
	siteInvitee := overviewTestUser(t, db, "ctrl-overview-site-invitee", common.RoleCommonUser, site.Id)

	mainAffiliate := model.ReferralAffiliate{UserId: mainUser.Id, InviteCode: "CTRLOVWM", Status: model.ReferralAffiliateStatusApproved}
	siteAffiliate := model.ReferralAffiliate{UserId: siteUser.Id, InviteCode: "CTRLOVWS", Status: model.ReferralAffiliateStatusApproved}
	require.NoError(t, db.Create(&mainAffiliate).Error)
	require.NoError(t, db.Create(&siteAffiliate).Error)
	require.NoError(t, db.Create(&model.ReferralBinding{InviteeUserId: mainInvitee.Id, InviterUserId: mainUser.Id, AffiliateId: mainAffiliate.Id}).Error)
	require.NoError(t, db.Create(&model.ReferralBinding{InviteeUserId: siteInvitee.Id, InviterUserId: siteUser.Id, AffiliateId: siteAffiliate.Id}).Error)
	require.NoError(t, db.Create(&model.ReferralCommissionAccount{
		AffiliateId: mainAffiliate.Id, UserId: mainUser.Id, SettlementCurrency: "USD",
		PendingAmount: 1.25, AvailableAmount: 2.5, FrozenAmount: 0.75, WithdrawnAmount: 4,
	}).Error)
	require.NoError(t, db.Create(&model.ReferralCommissionAccount{
		AffiliateId: siteAffiliate.Id, UserId: siteUser.Id, SettlementCurrency: "USD",
		PendingAmount: 8, AvailableAmount: 16, FrozenAmount: 24, WithdrawnAmount: 32,
	}).Error)

	rootBody := callReferralOverview(t, root.Id)
	assert.EqualValues(t, 2, rootBody["affiliates"])
	assert.EqualValues(t, 2, rootBody["bindings"])
	assert.Equal(t, "9.25", rootBody["pending"])
	assert.Equal(t, "18.50", rootBody["available"])
	assert.Equal(t, "24.75", rootBody["frozen"])
	assert.Equal(t, "36.00", rootBody["withdrawn"])

	adminBody := callReferralOverview(t, admin.Id)
	assert.EqualValues(t, 1, adminBody["affiliates"], "non-root admin with referral-read standing only sees main-site users")
	assert.EqualValues(t, 1, adminBody["bindings"])
	assert.Equal(t, "1.25", adminBody["pending"])
	assert.Equal(t, "2.50", adminBody["available"])
	assert.Equal(t, "0.75", adminBody["frozen"])
	assert.Equal(t, "4.00", adminBody["withdrawn"])
	assert.NotEqual(t, rootBody["affiliates"], adminBody["affiliates"])
	assert.NotEqual(t, rootBody["bindings"], adminBody["bindings"])
	assert.NotEqual(t, rootBody["pending"], adminBody["pending"])
	assert.NotEqual(t, rootBody["available"], adminBody["available"])
	assert.NotEqual(t, rootBody["frozen"], adminBody["frozen"])
	assert.NotEqual(t, rootBody["withdrawn"], adminBody["withdrawn"])

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/referral/admin/overview", nil)
	ctx.Set("id", owner.Id)
	GetReferralOverview(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"success":false`)
	require.NotContains(t, recorder.Body.String(), `"affiliates"`)
	require.NotContains(t, recorder.Body.String(), `"bindings"`)
	require.NotContains(t, recorder.Body.String(), "9.25")
	require.NotContains(t, recorder.Body.String(), "8.00")
}

func callReferralOverview(t *testing.T, userID int) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/referral/admin/overview", nil)
	ctx.Set("id", userID)
	GetReferralOverview(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)

	var payload struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.NotNil(t, payload.Data)
	return payload.Data
}

func overviewTestUser(t *testing.T, db *gorm.DB, name string, role, siteID int) *model.User {
	t.Helper()
	user := &model.User{Username: name, Password: "hashed-password", Role: role, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, Email: name + "@example.com", SiteId: siteID}
	require.NoError(t, db.Create(user).Error)
	return user
}

func TestReferralAdminHandlersEnforceMainSiteScopeAndAssetResignAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.AdtyTopupOrder{}, &model.AdtySite{}, &model.AdtySiteStaff{}, &model.AdtyTeam{}, &model.AdtyTeamMember{}))
	require.NoError(t, db.AutoMigrate(model.ReferralTables()...))

	root := overviewTestUser(t, db, "scope-root", common.RoleRootUser, 0)
	admin := overviewTestUser(t, db, "scope-admin", common.RoleAdminUser, 0)
	owner := overviewTestUser(t, db, "scope-owner", common.RoleCommonUser, 0)
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "scope-site", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)

	mainUser := overviewTestUser(t, db, "scope-main-aff", common.RoleCommonUser, 0)
	subUser := overviewTestUser(t, db, "scope-sub-aff", common.RoleCommonUser, site.Id)
	mainInvitee := overviewTestUser(t, db, "scope-main-inv", common.RoleCommonUser, 0)
	subInvitee := overviewTestUser(t, db, "scope-sub-inv", common.RoleCommonUser, site.Id)
	stranger := overviewTestUser(t, db, "scope-stranger", common.RoleCommonUser, 0)

	mainAff := model.ReferralAffiliate{UserId: mainUser.Id, InviteCode: "MAINSCOP1", Status: model.ReferralAffiliateStatusApproved, PartnerStatus: model.ReferralPartnerStatusPending}
	subAff := model.ReferralAffiliate{UserId: subUser.Id, InviteCode: "SUBSCOP1", Status: model.ReferralAffiliateStatusApproved, PartnerStatus: model.ReferralPartnerStatusPending}
	require.NoError(t, db.Create(&mainAff).Error)
	require.NoError(t, db.Create(&subAff).Error)
	require.NoError(t, db.Create(&model.ReferralCommissionAccount{AffiliateId: mainAff.Id, UserId: mainUser.Id, AvailableAmount: 50, FrozenAmount: 10}).Error)
	require.NoError(t, db.Create(&model.ReferralCommissionAccount{AffiliateId: subAff.Id, UserId: subUser.Id, AvailableAmount: 50, FrozenAmount: 20}).Error)
	require.NoError(t, db.Create(&model.ReferralBinding{InviteeUserId: mainInvitee.Id, InviterUserId: mainUser.Id, AffiliateId: mainAff.Id}).Error)
	require.NoError(t, db.Create(&model.ReferralBinding{InviteeUserId: subInvitee.Id, InviterUserId: subUser.Id, AffiliateId: subAff.Id}).Error)

	mainComm := model.ReferralCommission{AffiliateId: mainAff.Id, AffiliateUserId: mainUser.Id, InviteeUserId: mainInvitee.Id, SourceType: model.ReferralSourceTopup, SourceTradeNo: "MAIN-TRD-1", CommissionAmount: 5, Status: model.ReferralCommissionStatusFrozen}
	subComm := model.ReferralCommission{AffiliateId: subAff.Id, AffiliateUserId: subUser.Id, InviteeUserId: subInvitee.Id, SourceType: model.ReferralSourceTopup, SourceTradeNo: "SUB-TRD-1", CommissionAmount: 6, Status: model.ReferralCommissionStatusFrozen}
	require.NoError(t, db.Create(&mainComm).Error)
	require.NoError(t, db.Create(&subComm).Error)
	require.NoError(t, db.Create(&model.ReferralCommissionJob{SourceType: model.ReferralSourceTopup, SourceTradeNo: "MAIN-TRD-1", AffiliateId: mainAff.Id, Status: model.ReferralCommissionJobStatusFailed}).Error)
	require.NoError(t, db.Create(&model.ReferralCommissionJob{SourceType: model.ReferralSourceTopup, SourceTradeNo: "SUB-TRD-1", AffiliateId: subAff.Id, Status: model.ReferralCommissionJobStatusFailed}).Error)
	require.NoError(t, db.Create(&model.ReferralCommissionLedger{AffiliateId: mainAff.Id, UserId: mainUser.Id, Type: "accrue", ExternalRefId: "main-leg-1"}).Error)
	require.NoError(t, db.Create(&model.ReferralCommissionLedger{AffiliateId: subAff.Id, UserId: subUser.Id, Type: "accrue", ExternalRefId: "sub-leg-1"}).Error)
	require.NoError(t, db.Create(&model.ReferralAdminAuditLog{Action: "rate", TargetUserId: mainUser.Id, AffiliateId: mainAff.Id, AdminUserId: root.Id}).Error)
	require.NoError(t, db.Create(&model.ReferralAdminAuditLog{Action: "rate", TargetUserId: subUser.Id, AffiliateId: subAff.Id, AdminUserId: root.Id}).Error)

	// Assets: mainUser's valid QR, stranger's QR illegally referenced on mainWd2 for resign check, subUser's QR.
	require.NoError(t, db.Create(&model.ReferralAsset{OwnerUserId: mainUser.Id, Purpose: model.ReferralAssetPurposeWithdrawalQR, StoragePath: "main-own.png", ContentType: "image/png", CreatedBy: "user"}).Error)
	require.NoError(t, db.Create(&model.ReferralAsset{OwnerUserId: stranger.Id, Purpose: model.ReferralAssetPurposeWithdrawalQR, StoragePath: "stranger-stolen.png", ContentType: "image/png", CreatedBy: "user"}).Error)
	require.NoError(t, db.Create(&model.ReferralAsset{OwnerUserId: subUser.Id, Purpose: model.ReferralAssetPurposeWithdrawalQR, StoragePath: "sub-own.png", ContentType: "image/png", CreatedBy: "user"}).Error)

	mainWd := model.ReferralWithdrawal{AffiliateId: mainAff.Id, UserId: mainUser.Id, Amount: 10, AccountType: model.ReferralWithdrawToAlipay, QRImageURL: "/api/referral-assets/main-own.png", Status: model.ReferralWithdrawalStatusPending}
	mainStolenWd := model.ReferralWithdrawal{AffiliateId: mainAff.Id, UserId: mainUser.Id, Amount: 10, AccountType: model.ReferralWithdrawToAlipay, QRImageURL: "/api/referral-assets/stranger-stolen.png", Status: model.ReferralWithdrawalStatusCanceled}
	subWd := model.ReferralWithdrawal{AffiliateId: subAff.Id, UserId: subUser.Id, Amount: 20, AccountType: model.ReferralWithdrawToAlipay, QRImageURL: "/api/referral-assets/sub-own.png", Status: model.ReferralWithdrawalStatusPending}
	require.NoError(t, db.Create(&mainWd).Error)
	require.NoError(t, db.Create(&mainStolenWd).Error)
	require.NoError(t, db.Create(&subWd).Error)

	callJSON := func(method, path string, userID int, body any, params gin.Params, handler gin.HandlerFunc) (int, map[string]any, string) {
		var raw []byte
		if body != nil {
			var err error
			raw, err = json.Marshal(body)
			require.NoError(t, err)
		}
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(method, path, bytes.NewReader(raw))
		ctx.Set("id", userID)
		ctx.Params = params
		handler(ctx)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out, rec.Body.String()
	}
	itemCount := func(resp map[string]any) int {
		data, _ := resp["data"].(map[string]any)
		if data == nil {
			return 0
		}
		items, _ := data["items"].([]any)
		return len(items)
	}

	// Non-root admin reads MUST only return main-site rows (whereas Root sees both).
	_, adminAffResp, _ := callJSON(http.MethodGet, "/api/user/admin/referral/affiliates", admin.Id, nil, nil, GetReferralAffiliates)
	_, rootAffResp, _ := callJSON(http.MethodGet, "/api/user/admin/referral/affiliates", root.Id, nil, nil, GetReferralAffiliates)
	assert.Equal(t, 1, itemCount(adminAffResp), "admin only sees main-site affiliates")
	assert.Equal(t, 2, itemCount(rootAffResp), "root sees all affiliates")

	_, adminCommResp, _ := callJSON(http.MethodGet, "/api/user/admin/referral/commissions", admin.Id, nil, nil, GetAdminReferralCommissions)
	_, rootCommResp, _ := callJSON(http.MethodGet, "/api/user/admin/referral/commissions", root.Id, nil, nil, GetAdminReferralCommissions)
	assert.Equal(t, 1, itemCount(adminCommResp), "admin only sees main-site commissions")
	assert.Equal(t, 2, itemCount(rootCommResp))

	_, adminJobsResp, _ := callJSON(http.MethodGet, "/api/user/admin/referral/commission-jobs", admin.Id, nil, nil, GetReferralCommissionJobs)
	_, rootJobsResp, _ := callJSON(http.MethodGet, "/api/user/admin/referral/commission-jobs", root.Id, nil, nil, GetReferralCommissionJobs)
	assert.Equal(t, 1, itemCount(adminJobsResp), "admin only sees main-site commission jobs")
	assert.Equal(t, 2, itemCount(rootJobsResp))

	_, adminLedgersResp, _ := callJSON(http.MethodGet, "/api/user/admin/referral/ledgers", admin.Id, nil, nil, GetReferralLedgers)
	_, rootLedgersResp, _ := callJSON(http.MethodGet, "/api/user/admin/referral/ledgers", root.Id, nil, nil, GetReferralLedgers)
	assert.Equal(t, 1, itemCount(adminLedgersResp), "admin only sees main-site ledgers")
	assert.Equal(t, 2, itemCount(rootLedgersResp))

	_, adminAuditResp, _ := callJSON(http.MethodGet, "/api/user/admin/referral/audit-logs", admin.Id, nil, nil, GetReferralAdminAuditLogs)
	_, rootAuditResp, _ := callJSON(http.MethodGet, "/api/user/admin/referral/audit-logs", root.Id, nil, nil, GetReferralAdminAuditLogs)
	assert.Equal(t, 1, itemCount(adminAuditResp), "admin only sees main-site audit logs")
	assert.Equal(t, 2, itemCount(rootAuditResp))

	_, adminWdResp, adminWdBody := callJSON(http.MethodGet, "/api/user/admin/referral/withdrawals", admin.Id, nil, nil, GetAdminReferralWithdrawals)
	_, rootWdResp, _ := callJSON(http.MethodGet, "/api/user/admin/referral/withdrawals", root.Id, nil, nil, GetAdminReferralWithdrawals)
	assert.Equal(t, 2, itemCount(adminWdResp), "admin sees only main-site withdrawals, never sub-site withdrawal")
	assert.Equal(t, 3, itemCount(rootWdResp))
	assert.NotContains(t, adminWdBody, "sub-own.png")
	assert.NotContains(t, adminWdBody, "stranger-stolen.png", "forged QR referencing stranger's asset must NOT be re-signed on list response")

	// Non-root admin MUST be blocked from inspecting or mutating sub-site targets.
	_, adminSubBindResp, _ := callJSON(http.MethodGet, "/api/user/admin/referral/affiliates/bindings", admin.Id, nil, gin.Params{{Key: "user_id", Value: strconv.Itoa(subUser.Id)}}, GetReferralBindings)
	assert.Equal(t, false, adminSubBindResp["success"], "admin must not read sub-site affiliate bindings")

	_, adminPartnerResp, _ := callJSON(http.MethodPost, "/api/user/admin/referral/affiliates/partner", admin.Id, map[string]any{"approve": true, "rate": decimal.NewFromInt(15)}, gin.Params{{Key: "user_id", Value: strconv.Itoa(subUser.Id)}}, ReviewReferralPartner)
	assert.Equal(t, false, adminPartnerResp["success"], "admin must not review sub-site partner")

	_, adminAffUpdResp, _ := callJSON(http.MethodPost, "/api/user/admin/referral/affiliates/update", admin.Id, map[string]any{"action": "disable"}, gin.Params{{Key: "user_id", Value: strconv.Itoa(subUser.Id)}}, UpdateReferralAffiliate)
	assert.Equal(t, false, adminAffUpdResp["success"], "admin must not update sub-site affiliate")

	_, adminJobRetryResp, _ := callJSON(http.MethodPost, "/api/user/admin/referral/commission-jobs/retry", admin.Id, map[string]any{"trade_no": "SUB-TRD-1"}, nil, RetryReferralCommissionJob)
	assert.Equal(t, false, adminJobRetryResp["success"], "admin must not retry sub-site commission job")

	_, adminWdReviewResp, _ := callJSON(http.MethodPost, "/api/user/admin/referral/withdrawals/review", admin.Id, map[string]any{"approve": false, "reason": "no"}, gin.Params{{Key: "id", Value: strconv.Itoa(subWd.Id)}}, ReviewReferralWithdrawal)
	assert.Equal(t, false, adminWdReviewResp["success"], "admin must not review sub-site withdrawal")

	_, adminWdPayResp, _ := callJSON(http.MethodPost, "/api/user/admin/referral/withdrawals/pay", admin.Id, map[string]any{"txn_no": "tx1"}, gin.Params{{Key: "id", Value: strconv.Itoa(subWd.Id)}}, PayReferralWithdrawal)
	assert.Equal(t, false, adminWdPayResp["success"], "admin must not pay sub-site withdrawal")
}
