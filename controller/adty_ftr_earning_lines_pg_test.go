package controller

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-11-10: the owner lists earning lines that show model and group and omit channel.
func TestFTRADTY1110OwnerListsEarningLinesPG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(&model.AdtySite{}, &model.AdtySiteEarningLine{}))
	owner := model.User{
		Username: "ftr1110-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	customer := model.User{
		Username: "ftr1110-customer", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&customer).Error)
	require.Equal(t, common.RoleCommonUser, owner.Role)
	require.Equal(t, common.RoleCommonUser, customer.Role)
	site := model.AdtySite{
		OwnerUserId: owner.Id, Subdomain: "ftr1110", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(&customer).Update("site_id", site.Id).Error)
	line := model.AdtySiteEarningLine{
		SiteId: site.Id, UserId: customer.Id, ModelName: "ftr1110-model", Group: "default",
		RequestId: "ftr1110-req", Quota: 1000,
		Markup: decimal.RequireFromString("1.2000"), CostFactor: decimal.RequireFromString("1.000000"),
		Revenue: decimal.RequireFromString("0.00200000"), Platform: decimal.RequireFromString("0.00200000"),
		Earning: decimal.Zero,
	}
	require.NoError(t, db.Create(&line).Error)

	ctx, rec := newAuthenticatedContext(t, http.MethodGet, "/api/adty/site/earnings/lines", nil, owner.Id)
	ctx.Set("role", owner.Role)
	ctx.Set("username", owner.Username)
	GetAdtyOwnerEarningLines(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, `"model_name":"ftr1110-model"`)
	assert.Contains(t, body, `"group":"default"`)
	assert.NotContains(t, body, `"channel"`)
	assert.NotContains(t, body, `"upstream"`)

	var stored model.AdtySiteEarningLine
	require.NoError(t, db.First(&stored, line.Id).Error)
	assert.Equal(t, "ftr1110-model", stored.ModelName)
	assert.Equal(t, "default", stored.Group)
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "channel")
	ftrAssertQuotaAndNoLedger(t, db, owner.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, customer.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
	var walletRows int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ?", site.Id).Count(&walletRows).Error)
	assert.Zero(t, walletRows)
}
