package controller

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-11-01: Root raises a site cost factor through PUT /api/adty/admin/sites/:id.
// A higher factor waits 24 hours. A lower factor writes immediately.
func TestFTRADTY1101RootAdjustsSiteCostFactorPG(t *testing.T) {
	db := ftrAccountDB(t)
	ftrEnableReseller(t)
	root := model.User{
		Username: "ftr1101-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	owner := model.User{
		Username: "ftr1101-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&owner).Error)
	require.Equal(t, common.RoleRootUser, root.Role)
	current := decimal.RequireFromString("0.60")
	site := model.AdtySite{
		OwnerUserId: owner.Id, Subdomain: "ftr1101", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(), CostFactor: &current,
	}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, service.EnsureAdtySiteWallets(site.Id))

	before := time.Now().Unix()
	raiseURL := "/api/adty/admin/sites/" + strconv.Itoa(site.Id)
	ctx, rec := newAuthenticatedContext(t, http.MethodPut, raiseURL, map[string]any{
		"cost_factor": "0.70", "reason": "FTR 1101 raise",
	}, root.Id)
	ctx.Set("role", root.Role)
	ctx.Set("username", root.Username)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(site.Id)}}
	AdjustAdtyAdminSite(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)

	var raised model.AdtySite
	require.NoError(t, db.First(&raised, site.Id).Error)
	require.NotNil(t, raised.CostFactor)
	assert.True(t, raised.CostFactor.Equal(decimal.RequireFromString("0.60")))
	require.NotNil(t, raised.PendingCostFactor)
	assert.True(t, raised.PendingCostFactor.Equal(decimal.RequireFromString("0.70")))
	assert.GreaterOrEqual(t, raised.PendingCostFactorAt, before+24*3600-2)
	assert.LessOrEqual(t, raised.PendingCostFactorAt, time.Now().Unix()+24*3600+5)

	lowerURL := "/api/adty/admin/sites/" + strconv.Itoa(site.Id)
	ctx, rec = newAuthenticatedContext(t, http.MethodPut, lowerURL, map[string]any{
		"cost_factor": "0.55", "reason": "FTR 1101 lower",
	}, root.Id)
	ctx.Set("role", root.Role)
	ctx.Set("username", root.Username)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(site.Id)}}
	AdjustAdtyAdminSite(ctx)
	body = rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)

	var lowered model.AdtySite
	require.NoError(t, db.First(&lowered, site.Id).Error)
	require.NotNil(t, lowered.CostFactor)
	assert.True(t, lowered.CostFactor.Equal(decimal.RequireFromString("0.55")))
	assert.Nil(t, lowered.PendingCostFactor)
	assert.Zero(t, lowered.PendingCostFactorAt)
	var audits int64
	require.NoError(t, db.Model(&model.AdtySiteAudit{}).Where("site_id = ? AND action = ? AND operator_id = ?", site.Id, "adjust", root.Id).Count(&audits).Error)
	assert.Equal(t, int64(2), audits)
	ftrAssertQuotaAndNoLedger(t, db, root.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, owner.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
	var walletRows int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ?", site.Id).Count(&walletRows).Error)
	assert.Zero(t, walletRows)
}

// ADTY-11-03: the site owner saves a team payment ratio below the cost factor.
func TestFTRADTY1103OwnerSavesTeamDiscountPG(t *testing.T) {
	db := ftrAccountDB(t)
	owner := model.User{
		Username: "ftr1103-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	leader := model.User{
		Username: "ftr1103-lead", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&leader).Error)
	require.Equal(t, common.RoleCommonUser, owner.Role)
	cost := decimal.RequireFromString("0.80")
	site := model.AdtySite{
		OwnerUserId: owner.Id, Subdomain: "ftr1103", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(), CostFactor: &cost,
	}
	require.NoError(t, db.Create(&site).Error)
	team := model.AdtyTeam{
		SiteId: site.Id, LeaderUserId: leader.Id, Name: "ftr1103",
		Discount: decimal.NewFromInt(1), Status: model.AdtyTeamStatusActive, SharedQuota: 0,
	}
	require.NoError(t, db.Create(&team).Error)

	url := "/api/adty/site/teams/" + strconv.FormatInt(team.Id, 10) + "/discount"
	ctx, rec := newAuthenticatedContext(t, http.MethodPut, url, map[string]any{"discount": "0.65"}, owner.Id)
	ctx.Set("role", owner.Role)
	ctx.Set("username", owner.Username)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(team.Id, 10)}}
	PutAdtyOwnerSiteTeamDiscount(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, `"updated":true`)

	var stored model.AdtyTeam
	require.NoError(t, db.First(&stored, team.Id).Error)
	assert.True(t, stored.Discount.Equal(decimal.RequireFromString("0.65")))
	assert.Equal(t, int64(0), stored.SharedQuota)
	assert.Equal(t, site.Id, stored.SiteId)
	assert.Equal(t, model.AdtyTeamStatusActive, stored.Status)
	var storedSite model.AdtySite
	require.NoError(t, db.First(&storedSite, site.Id).Error)
	require.NotNil(t, storedSite.CostFactor)
	assert.True(t, storedSite.CostFactor.Equal(decimal.RequireFromString("0.80")))
	ftrAssertQuotaAndNoLedger(t, db, owner.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, leader.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-11-05: Root saves a main-site team agreement price through PUT /api/adty/admin/teams/:id/agreement-price.
func TestFTRADTY1105RootSavesMainTeamAgreementPG(t *testing.T) {
	db := ftrAccountDB(t)
	root := model.User{
		Username: "ftr1105-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	leader := model.User{
		Username: "ftr1105-lead", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&leader).Error)
	require.Equal(t, common.RoleRootUser, root.Role)
	team := ftrSeedTeam(t, db, leader, "ftr1105")
	require.Equal(t, 0, team.SiteId)
	require.Equal(t, int64(0), team.SharedQuota)

	url := "/api/adty/admin/teams/" + strconv.FormatInt(team.Id, 10) + "/agreement-price"
	ctx, rec := newAuthenticatedContext(t, http.MethodPut, url, map[string]any{"discount": "0.70"}, root.Id)
	ctx.Set("role", root.Role)
	ctx.Set("username", root.Username)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(team.Id, 10)}}
	PutAdtyMainTeamAgreementPrice(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, `"updated":true`)

	var stored model.AdtyTeam
	require.NoError(t, db.First(&stored, team.Id).Error)
	assert.True(t, stored.Discount.Equal(decimal.RequireFromString("0.70")))
	assert.Equal(t, 0, stored.SiteId)
	assert.Equal(t, int64(0), stored.SharedQuota)
	assert.Equal(t, model.AdtyTeamStatusActive, stored.Status)
	ftrAssertQuotaAndNoLedger(t, db, root.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, leader.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
