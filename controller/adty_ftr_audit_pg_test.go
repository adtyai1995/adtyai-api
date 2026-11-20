package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ftrAuditAdmin(t *testing.T, f ftrOrderFixture) model.User {
	t.Helper()
	admin := model.User{
		Username: "ftr-audit-admin", Password: "hashed-password", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Quota: ftrOrderQuota,
	}
	require.NoError(t, f.db.Create(&admin).Error)
	require.Equal(t, common.RoleAdminUser, admin.Role)
	return admin
}

func TestFTRADTY1703GlobalOrderListPG(t *testing.T) {
	f := newFTROrderFixture(t, "ftr-1703")
	admin := ftrAuditAdmin(t, f)
	require.NoError(t, f.db.Create(&model.TopUp{
		UserId: f.owner.Id, TradeNo: "ftr-1703-own", Amount: ftrOrderQuota,
		Status: common.TopUpStatusSuccess, PaymentProvider: model.PaymentProviderAdtyEpay,
	}).Error)
	require.NoError(t, f.db.Create(&model.BillingOperation{
		OperationKey: "ftr-1703-other-op", UserId: f.other.Id, Kind: "request", State: "settled",
		AppliedQuota: ftrOrderQuota, ReferenceId: 1703, PayerTeamId: 0, SiteId: 0,
	}).Error)

	ctx, rec := f.call(t, admin, http.MethodGet, "/api/user/admin/finance/recharge-audit?p=1&page_size=10")
	GetRechargeAudit(ctx)
	body := rec.Body.String()
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr-1703-own")
	assert.Contains(t, body, "ftr-1703-other-op")
	assert.Equal(t, common.RoleAdminUser, admin.Role)
	f.assertWalletsUnchanged(t)
	var ledgers int64
	require.NoError(t, f.db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

func TestFTRADTY1706OrderTypeFilterPG(t *testing.T) {
	f := newFTROrderFixture(t, "ftr-1706")
	admin := ftrAuditAdmin(t, f)
	require.NoError(t, f.db.Create(&model.TopUp{
		UserId: f.owner.Id, TradeNo: "ftr-1706-topup", Amount: ftrOrderQuota,
		Status: common.TopUpStatusSuccess, PaymentProvider: model.PaymentProviderAdtyEpay,
	}).Error)
	require.NoError(t, f.db.Create(&model.BillingOperation{
		OperationKey: "ftr-1706-use", UserId: f.owner.Id, Kind: "request", State: "settled",
		AppliedQuota: 250000, ReferenceId: 1706, PayerTeamId: 0, SiteId: 0,
	}).Error)

	topupCtx, topupRec := f.call(t, admin, http.MethodGet, "/api/user/admin/finance/recharge-audit?order_type=topup&p=1&page_size=10")
	GetRechargeAudit(topupCtx)
	topupBody := topupRec.Body.String()
	require.Contains(t, topupBody, `"success":true`, topupBody)
	assert.Contains(t, topupBody, "ftr-1706-topup")
	assert.NotContains(t, topupBody, "ftr-1706-use")

	useCtx, useRec := f.call(t, admin, http.MethodGet, "/api/user/admin/finance/recharge-audit?order_type=consumption&p=1&page_size=10")
	GetRechargeAudit(useCtx)
	useBody := useRec.Body.String()
	require.Contains(t, useBody, `"success":true`, useBody)
	assert.Contains(t, useBody, "ftr-1706-use")
	assert.NotContains(t, useBody, "ftr-1706-topup")
	assert.Equal(t, common.RoleAdminUser, admin.Role)
	f.assertWalletsUnchanged(t)
	var ledgers int64
	require.NoError(t, f.db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

func TestFTRADTY1707OrderStatusPagePG(t *testing.T) {
	f := newFTROrderFixture(t, "ftr-1707")
	admin := ftrAuditAdmin(t, f)
	require.NoError(t, f.db.Create(&model.TopUp{
		UserId: f.owner.Id, TradeNo: "ftr-1707-ok-a", Amount: ftrOrderQuota,
		Status: common.TopUpStatusSuccess, PaymentProvider: model.PaymentProviderAdtyEpay, CreateTime: 20,
	}).Error)
	require.NoError(t, f.db.Create(&model.TopUp{
		UserId: f.other.Id, TradeNo: "ftr-1707-ok-b", Amount: ftrOrderQuota,
		Status: common.TopUpStatusSuccess, PaymentProvider: model.PaymentProviderAdtyEpay, CreateTime: 10,
	}).Error)
	require.NoError(t, f.db.Create(&model.TopUp{
		UserId: f.owner.Id, TradeNo: "ftr-1707-pending", Amount: ftrOrderQuota,
		Status: common.TopUpStatusPending, PaymentProvider: model.PaymentProviderAdtyEpay, CreateTime: 30,
	}).Error)

	ctx, rec := f.call(t, admin, http.MethodGet, "/api/user/admin/finance/recharge-audit?status=success&p=1&page_size=1")
	GetRechargeAudit(ctx)
	body := rec.Body.String()
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr-1707-ok-a")
	assert.NotContains(t, body, "ftr-1707-ok-b")
	assert.NotContains(t, body, "ftr-1707-pending")
	assert.Contains(t, body, `"total":2`)
	assert.Equal(t, common.RoleAdminUser, admin.Role)
	f.assertWalletsUnchanged(t)
	var ledgers int64
	require.NoError(t, f.db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
