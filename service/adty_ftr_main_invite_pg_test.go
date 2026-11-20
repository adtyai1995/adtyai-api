package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-16-01: a main-site user gets an affiliate and binds one invitee.
// GET /api/user/referral/profile calls EnsureAffiliate; the binding is what that profile counts as a signup.
// The bind does not pay commission.
func TestFTRADTY1601MainSiteInviteBindPG(t *testing.T) {
	db := ftrRound1PG(t)
	require.NoError(t, db.AutoMigrate(model.ReferralTables()...))
	inviter := ftrRound1User(t, db, "ftr1601-inviter", common.RoleCommonUser)
	invitee := ftrRound1User(t, db, "ftr1601-invitee", common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, inviter.Role)
	require.Equal(t, common.RoleCommonUser, invitee.Role)
	require.Zero(t, inviter.SiteId)
	require.Zero(t, invitee.SiteId)
	ftrRound1PinQuota(t, db, inviter.Id)
	ftrRound1PinQuota(t, db, invitee.Id)

	affiliate, err := Referral.EnsureAffiliate(inviter.Id)
	require.NoError(t, err)
	require.NotNil(t, affiliate)
	assert.Equal(t, inviter.Id, affiliate.UserId)
	assert.Equal(t, model.ReferralAffiliateStatusApproved, affiliate.Status)
	assert.Len(t, affiliate.InviteCode, 8)
	assert.Zero(t, affiliate.SiteOwnerSince)
	again, err := Referral.EnsureAffiliate(inviter.Id)
	require.NoError(t, err)
	assert.Equal(t, affiliate.Id, again.Id)
	assert.Equal(t, affiliate.InviteCode, again.InviteCode)

	binding, err := Referral.BindInvitee(invitee.Id, affiliate.InviteCode, "ftr1601-ip")
	require.NoError(t, err)
	require.NotNil(t, binding)
	assert.Equal(t, invitee.Id, binding.InviteeUserId)
	assert.Equal(t, inviter.Id, binding.InviterUserId)
	assert.Equal(t, affiliate.Id, binding.AffiliateId)
	assert.Equal(t, affiliate.InviteCode, binding.BindCode)
	_, err = Referral.BindInvitee(invitee.Id, affiliate.InviteCode, "ftr1601-ip")
	require.ErrorIs(t, err, ErrReferralAlreadyBound)

	var stored model.ReferralBinding
	require.NoError(t, db.Where("invitee_user_id = ?", invitee.Id).First(&stored).Error)
	assert.Equal(t, inviter.Id, stored.InviterUserId)
	var bindings int64
	require.NoError(t, db.Model(&model.ReferralBinding{}).Count(&bindings).Error)
	assert.EqualValues(t, 1, bindings)
	var account model.ReferralCommissionAccount
	require.NoError(t, db.Where("affiliate_id = ?", affiliate.Id).First(&account).Error)
	assert.Equal(t, "USD", account.SettlementCurrency)
	assert.Zero(t, account.PendingAmount)
	assert.Zero(t, account.AvailableAmount)
	assert.Zero(t, account.FrozenAmount)
	assert.Zero(t, account.WithdrawnAmount)
	var commissions int64
	require.NoError(t, db.Model(&model.ReferralCommission{}).Count(&commissions).Error)
	assert.Zero(t, commissions)
	ftrRound1AssertNoMoney(t, db, inviter.Id)
	ftrRound1AssertNoMoney(t, db, invitee.Id)
}
