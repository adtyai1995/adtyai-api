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

// ADTY-16-03: the owner binds one direct site invite and lists it. The bind does not pay commission.
func TestFTRADTY1603OwnerBindsDirectInvitePG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.AdtySite{}, &model.AdtySiteInvite{}, &model.AdtySiteReferralRate{},
		&model.AdtySiteWallet{}, &model.AdtySiteWalletLedger{}, &model.ReferralCommission{},
	))
	owner := model.User{
		Username: "ftr1603-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	inviter := model.User{
		Username: "ftr1603-inviter", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	invitee := model.User{
		Username: "ftr1603-invitee", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&inviter).Error)
	require.NoError(t, db.Create(&invitee).Error)
	require.Equal(t, common.RoleCommonUser, owner.Role)
	site := model.AdtySite{
		OwnerUserId: owner.Id, Subdomain: "ftr1603", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(&inviter).Update("site_id", site.Id).Error)
	require.NoError(t, db.Model(&invitee).Update("site_id", site.Id).Error)
	rate := decimal.RequireFromString("0.10")
	require.NoError(t, db.Create(&model.AdtySiteReferralRate{SiteId: site.Id, Rate: rate, Enabled: true}).Error)
	earnings := model.AdtySiteWallet{
		SiteId: site.Id, Kind: model.AdtySiteWalletEarnings, Currency: "USD",
		Balance: decimal.RequireFromString("12.50"),
	}
	require.NoError(t, db.Create(&earnings).Error)

	body := map[string]int{"inviter_id": inviter.Id, "invitee_id": invitee.Id}
	ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/adty/site/referral/invites", body, owner.Id)
	ctx.Set("role", owner.Role)
	ctx.Set("username", owner.Username)
	PostAdtyOwnerSiteInvite(ctx)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"success":true`)
	require.Contains(t, rec.Body.String(), `"bound":true`)

	ctx, rec = newAuthenticatedContext(t, http.MethodPost, "/api/adty/site/referral/invites", body, owner.Id)
	ctx.Set("role", owner.Role)
	PostAdtyOwnerSiteInvite(ctx)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"bound":true`)

	ctx, rec = newAuthenticatedContext(t, http.MethodGet, "/api/adty/site/referral/invites", nil, owner.Id)
	ctx.Set("role", owner.Role)
	GetAdtyOwnerSiteInvites(ctx)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var listed struct {
		Success bool `json:"success"`
		Data    struct {
			Items []struct {
				SiteId    int    `json:"site_id"`
				InviterId int    `json:"inviter_id"`
				InviteeId int    `json:"invitee_id"`
				Rate      string `json:"rate"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.True(t, listed.Success)
	require.Len(t, listed.Data.Items, 1)
	assert.Equal(t, site.Id, listed.Data.Items[0].SiteId)
	assert.Equal(t, inviter.Id, listed.Data.Items[0].InviterId)
	assert.Equal(t, invitee.Id, listed.Data.Items[0].InviteeId)
	assert.Equal(t, "0.1", listed.Data.Items[0].Rate)

	var stored model.AdtySiteInvite
	require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&stored).Error)
	assert.Equal(t, site.Id, stored.SiteId)
	assert.Equal(t, inviter.Id, stored.InviterId)
	assert.Equal(t, invitee.Id, stored.InviteeId)
	assert.True(t, stored.Rate.Equal(rate))
	var invites int64
	require.NoError(t, db.Model(&model.AdtySiteInvite{}).Count(&invites).Error)
	assert.EqualValues(t, 1, invites)
	var topups int64
	require.NoError(t, db.Model(&model.TopUp{}).Count(&topups).Error)
	assert.Zero(t, topups)
	var commissions int64
	require.NoError(t, db.Model(&model.ReferralCommission{}).Count(&commissions).Error)
	assert.Zero(t, commissions)
	var wallet model.AdtySiteWallet
	require.NoError(t, db.First(&wallet, earnings.Id).Error)
	assert.True(t, wallet.Balance.Equal(decimal.RequireFromString("12.50")))
	var walletRows int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ?", site.Id).Count(&walletRows).Error)
	assert.Zero(t, walletRows)
	ftrAssertQuotaAndNoLedger(t, db, owner.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, inviter.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, invitee.Id, ftrAccountQuota)
}
