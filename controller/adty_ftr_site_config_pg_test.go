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

// ADTY-10-05: the site owner saves brand page copy through PUT /api/adty/site/self.
func TestFTRADTY1005OwnerSavesBrandPagePG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(&model.AdtySite{}))
	owner := model.User{
		Username: "ftr1005-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&owner).Error)
	require.Equal(t, common.RoleCommonUser, owner.Role)
	site := model.AdtySite{
		OwnerUserId: owner.Id, Subdomain: "ftr1005", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(), SeoIndex: true,
	}
	require.NoError(t, db.Create(&site).Error)

	ctx, rec := newAuthenticatedContext(t, http.MethodPut, "/api/adty/site/self", map[string]any{
		"brand": map[string]any{
			"name":            "FTR 1005 Page",
			"title":           "FTR 1005 Title",
			"notice":          "Page notice for owners",
			"support_contact": "support@ftr1005.example",
		},
		"seo_index": false,
	}, owner.Id)
	ctx.Set("role", owner.Role)
	ctx.Set("username", owner.Username)
	PutAdtyOwnerSite(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "FTR 1005 Page")
	assert.Contains(t, body, "Page notice for owners")

	var stored model.AdtySite
	require.NoError(t, db.First(&stored, site.Id).Error)
	assert.False(t, stored.SeoIndex)
	assert.Contains(t, stored.Brand, "FTR 1005 Title")
	assert.Contains(t, stored.Brand, "support@ftr1005.example")
	assert.Equal(t, owner.Id, stored.OwnerUserId)
	ftrAssertQuotaAndNoLedger(t, db, owner.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-16-04: Root stores the site invite commission cap through PUT /api/adty/admin/site-referral/cap.
func TestFTRADTY1604RootSavesSiteReferralCapPG(t *testing.T) {
	db := ftrAccountDB(t)
	ftrPinProcessOptions(t)
	root := model.User{
		Username: "ftr1604-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&root).Error)
	require.Equal(t, common.RoleRootUser, root.Role)

	ctx, rec := newAuthenticatedContext(t, http.MethodPut, "/api/adty/admin/site-referral/cap", map[string]any{
		"max_rate": "0.15",
	}, root.Id)
	ctx.Set("role", root.Role)
	ctx.Set("username", root.Username)
	PutAdtySiteReferralCap(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, `"saved":true`)

	var stored model.Option
	require.NoError(t, db.Where("key = ?", "adty_site_referral.max_rate").First(&stored).Error)
	assert.Equal(t, "0.15", stored.Value)
	ftrAssertQuotaAndNoLedger(t, db, root.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
