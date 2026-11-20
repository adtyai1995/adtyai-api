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

// ADTY-10-02: the site owner updates brand and SEO through PUT /api/adty/site/self.
func TestFTRADTY1002OwnerSiteProfilePG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(&model.AdtySite{}))
	owner := model.User{
		Username: "ftr1002-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&owner).Error)
	require.Equal(t, common.RoleCommonUser, owner.Role)
	site := model.AdtySite{
		OwnerUserId:  owner.Id,
		Subdomain:    "ftr1002",
		Status:       model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
		SeoIndex:     false,
	}
	require.NoError(t, db.Create(&site).Error)

	ctx, rec := newAuthenticatedContext(t, http.MethodPut, "/api/adty/site/self", map[string]any{
		"brand": map[string]any{
			"name":        "FTR 1002 Shop",
			"theme_color": "#336699",
			"tagline":     "Owner edited profile",
		},
		"seo_index": true,
	}, owner.Id)
	ctx.Set("role", owner.Role)
	ctx.Set("username", owner.Username)
	PutAdtyOwnerSite(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "FTR 1002 Shop")
	assert.Contains(t, body, "#336699")

	var stored model.AdtySite
	require.NoError(t, db.First(&stored, site.Id).Error)
	assert.True(t, stored.SeoIndex)
	assert.Contains(t, stored.Brand, "FTR 1002 Shop")
	assert.Equal(t, owner.Id, stored.OwnerUserId)
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrAssertQuotaAndNoLedger(t, db, owner.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
