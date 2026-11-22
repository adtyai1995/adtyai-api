package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/stretchr/testify/require"
)

func TestR24UsageFollowsRequestSiteAndPayerPG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.BillingOperation{}, &model.AdtySiteSettlementSheet{}))
	// The log database has no user or billing tables.
	logs := adtyTestDB(t)
	model.DB = db
	pgtest.RecordServerVersion(t, logs)
	require.NoError(t, logs.AutoMigrate(&model.Log{}))
	user := badgeTestUser(t, db, "migrated", common.RoleCommonUser, 22)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).Unix()
	for _, op := range []model.BillingOperation{
		{OperationKey: "request:before", SiteId: 11, UserId: user.Id, PayerKind: model.BillingPayerTeam, PayerTeamId: 8},
		{OperationKey: "request:after", SiteId: 22, UserId: user.Id, PayerKind: model.BillingPayerPersonal},
		{OperationKey: "request:blank", SiteId: 11, UserId: user.Id},
	} {
		require.NoError(t, db.Create(&op).Error)
	}
	ownerSite := 33
	other := model.NewLogOther()
	stampRequestUsageOwner(other, &model.BillingOperation{OperationKey: "request:archived", UserId: user.Id, SiteId: ownerSite, PayerKind: model.BillingPayerOwner})
	require.NoError(t, db.Create(&model.AdtySiteSettlementSheet{SiteId: ownerSite, BusinessDay: "2026-10-31", DayStart: start - 3600*23, DayEnd: start + 3600}).Error)
	require.NoError(t, logs.Create(&[]model.Log{
		{UserId: user.Id, CreatedAt: start, Type: model.LogTypeConsume, ModelName: "self-use", Group: "owner", Quota: 83, Other: other.JSONString()},
		{UserId: user.Id, CreatedAt: start, Type: model.LogTypeConsume, RequestId: "before", ModelName: "old-model", Group: "old-group", Quota: 120},
		{UserId: user.Id, CreatedAt: start + 86400, Type: model.LogTypeConsume, ModelName: "new-model", Group: "new-group", Quota: 30, Other: `{"admin_info":{"billing_operation_key":"request:after"}}`},
		{UserId: user.Id, CreatedAt: start, Type: model.LogTypeConsume, RequestId: "blank", Quota: 7},
		{UserId: user.Id, CreatedAt: start, Type: model.LogTypeConsume, Quota: 9, Other: "legacy"},
	}).Error) // LOG_DB only
	old, err := GetAdtySiteUsageBreakdown(11, "2026-11-01", "2026-11-02")
	require.NoError(t, err)
	require.Len(t, old.ByModel, 1)
	require.Equal(t, "old-model", old.ByModel[0].Key)
	require.EqualValues(t, 120, old.ByModel[0].Quota)
	require.Len(t, old.ByGroup, 1)
	require.Equal(t, "old-group", old.ByGroup[0].Key)
	require.Equal(t, AdtySiteUsageTotals{Requests: 1, Quota: 7}, old.Unverified)
	require.Equal(t, AdtySiteUsageTotals{Requests: 1, Quota: 9}, old.UnknownSite)
	require.Len(t, old.ByPayer, 1)
	require.Equal(t, model.BillingPayerTeam, old.ByPayer[0].Key)
	archived, err := GetAdtySiteUsageBreakdown(ownerSite, "2026-10-31", "2026-10-31")
	require.NoError(t, err)
	require.Len(t, archived.ByPayer, 1)
	require.Equal(t, model.BillingPayerOwner, archived.ByPayer[0].Key)
	require.EqualValues(t, 83, archived.ByPayer[0].Quota)
	nextDay, err := GetAdtySiteUsageBreakdown(ownerSite, "2026-11-01", "2026-11-01")
	require.NoError(t, err)
	require.Empty(t, nextDay.ByPayer)
	after, err := GetAdtySiteUsageBreakdown(22, "2026-11-02", "2026-11-02")
	require.NoError(t, err)
	require.Len(t, after.ByModel, 1)
	require.Equal(t, "new-model", after.ByModel[0].Key)
	require.Equal(t, model.BillingPayerPersonal, after.ByPayer[0].Key)
	require.Equal(t, "new-group", after.ByGroup[0].Key)
	require.EqualValues(t, 30, after.ByGroup[0].Quota)
	earlier, err := GetAdtySiteUsageBreakdown(22, "2026-11-01", "2026-11-01")
	require.NoError(t, err)
	require.Empty(t, earlier.ByGroup)
}

func TestR24ClosedReferralWorkByPermissionPG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.CasbinRule{}, &model.AuthzRole{}, &model.AuthzActionCatalogState{}))
	require.NoError(t, db.AutoMigrate(model.ReferralTables()...))
	require.NoError(t, model.MigrateAdtyTables())
	wasMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = wasMaster })
	authz.AdtyRegisterResources()
	require.NoError(t, authz.Init(db))
	previous := AdtyAdminPermissionsEnabled()
	SetAdtyAdminPermissionsEnabled(true)
	t.Cleanup(func() { SetAdtyAdminPermissionsEnabled(previous) })
	for _, site := range []model.AdtySite{{Id: 11, OwnerUserId: 101, Subdomain: "closed", Status: model.AdtySiteStatusClosed}, {Id: 22, OwnerUserId: 102, Subdomain: "active", Status: model.AdtySiteStatusActive}} {
		require.NoError(t, db.Create(&site).Error)
		for _, status := range []string{"pending", "approved", "paid"} {
			require.NoError(t, db.Create(&model.AdtySiteInviteWithdrawal{SiteId: site.Id, UserId: site.OwnerUserId, Method: "usdt", Status: status, IdempotencyKey: site.Subdomain + status}).Error)
		}
	}
	for _, tc := range []struct {
		name              string
		review, pay       bool
		pending, approved int64
	}{{"review", true, false, 1, 0}, {"payout", false, true, 0, 1}, {"both", true, true, 1, 1}, {"reader", false, false, 0, 0}} {
		actor := badgeTestUser(t, db, tc.name, common.RoleAdminUser, 0)
		require.NoError(t, authz.SetUserPermissions(actor.Id, authz.PermissionsMap{authz.ResourceAdtyReferral: {authz.ActionRead: true, authz.ActionReview: tc.review, authz.ActionPayout: tc.pay}}))
		badges, err := CollectAdtyReferralAdminBadges(actor.Id)
		require.NoError(t, err)
		require.Equal(t, tc.pending, badges.PendingWithdrawals, tc.name)
		require.Equal(t, tc.approved, badges.ApprovedWithdrawals, tc.name)
		sidebar, err := CollectAdtySidebarBadges(actor.Id, nil)
		require.NoError(t, err)
		require.Equal(t, tc.pending, sidebar[AdtyBadgeReferralWithdrawalsPending])
		require.Equal(t, tc.approved, sidebar[AdtyBadgeReferralWithdrawalsApproved])
		if !tc.review {
			require.NotContains(t, sidebar, AdtyBadgeReferralWithdrawalsPending)
		}
		if !tc.pay {
			require.NotContains(t, sidebar, AdtyBadgeReferralWithdrawalsApproved)
		}
		if tc.review && tc.pay {
			scoped, err := CollectAdtyReferralAdminBadges(actor.Id, []string{"adty_referral:read", "adty_referral:payout"})
			require.NoError(t, err)
			require.Zero(t, scoped.PendingWithdrawals)
			require.EqualValues(t, 1, scoped.ApprovedWithdrawals)
		}
	}
}
