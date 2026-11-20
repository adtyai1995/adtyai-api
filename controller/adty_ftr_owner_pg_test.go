package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY1702OwnerCustomerListPG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(&model.AdtySite{}, &model.AdtySiteCustomer{}))
	owner := model.User{
		Username: "ftr1702-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&owner).Error)
	site := model.AdtySite{
		OwnerUserId: owner.Id, Subdomain: "ftr1702", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	require.NotZero(t, site.Id)
	customer := model.User{
		Username: "ftr1702-customer", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", SiteId: site.Id, Quota: ftrAccountQuota,
	}
	otherSite := model.AdtySite{
		OwnerUserId: owner.Id + 1000, Subdomain: "ftr1702b", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&otherSite).Error)
	foreign := model.User{
		Username: "ftr1702-foreign", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", SiteId: otherSite.Id, Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&customer).Error)
	require.NoError(t, db.Create(&foreign).Error)

	ctx, rec := newAuthenticatedContext(t, http.MethodGet, "/api/adty/site/customers?p=1&page_size=10", nil, owner.Id)
	ctx.Set("role", owner.Role)
	GetAdtyOwnerCustomers(ctx)
	body := rec.Body.String()
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr1702-customer")
	assert.NotContains(t, body, "ftr1702-foreign")
	assert.NotContains(t, body, "ftr1702-owner")
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrAssertQuotaAndNoLedger(t, db, owner.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, customer.Id, ftrAccountQuota)
}

func TestFTRADTY1704OwnerSiteOrdersPG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.AdtySite{}, &model.TopUp{}, &model.AdtyTopupOrder{}, &model.BillingOperation{},
		&model.AdtyManualRefund{}, &model.AdtySiteWithdrawal{}, &model.AdtySiteInviteWithdrawal{},
		&model.ReferralWithdrawal{}, &model.AdtySiteInviteCommission{}, &model.ReferralCommission{},
		&model.AdtyStaffBalanceRequest{},
	))
	owner := model.User{
		Username: "ftr1704-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	buyer := model.User{
		Username: "ftr1704-buyer", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&buyer).Error)
	site := model.AdtySite{
		OwnerUserId: owner.Id, Subdomain: "ftr1704", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	other := model.AdtySite{
		OwnerUserId: owner.Id + 1000, Subdomain: "ftr1704b", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Create(&other).Error)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: buyer.Id, TradeNo: "ftr-1704-own", Amount: ftrAccountQuota,
		Status: common.TopUpStatusSuccess, PaymentProvider: model.PaymentProviderAdtyEpay,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		UserId: buyer.Id, TradeNo: "ftr-1704-own", SiteId: site.Id, Provider: model.PaymentProviderAdtyEpay,
	}).Error)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: buyer.Id, TradeNo: "ftr-1704-other", Amount: ftrAccountQuota,
		Status: common.TopUpStatusSuccess, PaymentProvider: model.PaymentProviderAdtyEpay,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		UserId: buyer.Id, TradeNo: "ftr-1704-other", SiteId: other.Id, Provider: model.PaymentProviderAdtyEpay,
	}).Error)

	ctx, rec := newAuthenticatedContext(t, http.MethodGet, "/api/adty/site/orders?p=1&page_size=10", nil, owner.Id)
	ctx.Set("role", owner.Role)
	GetAdtyOwnerOrders(ctx)
	body := rec.Body.String()
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr-1704-own")
	assert.NotContains(t, body, "ftr-1704-other")
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrAssertQuotaAndNoLedger(t, db, owner.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, buyer.Id, ftrAccountQuota)
}
