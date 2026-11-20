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

func TestFTRADTY1709OwnerCustomerExportPG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(&model.AdtySite{}, &model.AdtySiteCustomer{}, &model.AdtySiteEarningDaily{}))
	owner := model.User{
		Username: "ftr1709-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&owner).Error)
	require.Equal(t, common.RoleCommonUser, owner.Role)
	site := model.AdtySite{
		OwnerUserId: owner.Id, Subdomain: "ftr1709", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	customer := model.User{
		Username: "ftr1709-customer", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", SiteId: site.Id, Email: "ftr1709-customer@example.test",
		Quota: ftrAccountQuota,
	}
	other := model.AdtySite{
		OwnerUserId: owner.Id + 1000, Subdomain: "ftr1709b", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&other).Error)
	foreign := model.User{
		Username: "ftr1709-foreign", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", SiteId: other.Id, Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&customer).Error)
	require.NoError(t, db.Create(&foreign).Error)

	ctx, rec := newAuthenticatedContext(t, http.MethodGet, "/api/adty/site/export?kind=customers", nil, owner.Id)
	ctx.Set("role", owner.Role)
	GetAdtyOwnerExport(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "customers.csv")
	assert.Contains(t, body, "user_id,username,email,created_at,revenue_usd,earning_usd")
	assert.Contains(t, body, "ftr1709-customer")
	assert.NotContains(t, body, "ftr1709-foreign")
	assert.NotContains(t, body, "ftr1709-owner")
	assert.Contains(t, body, "0.00")
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrAssertQuotaAndNoLedger(t, db, owner.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, customer.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
