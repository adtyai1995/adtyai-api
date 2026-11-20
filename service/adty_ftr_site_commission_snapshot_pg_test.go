package service

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-16-05: order creation freezes the site invite commission snapshot.
// CreateAdtyTopUp calls AdtyBeforeTopUpCreateTx, which is prepareAdtyReferralSnapshotTx.
// Changing the live invite rate afterwards must not rewrite the stored snapshot.
// The snapshot is not a commission payment.
func TestFTRADTY1605OrderFreezesInviteCommissionSnapshotPG(t *testing.T) {
	db := ftrRound1PG(t)
	require.NoError(t, db.AutoMigrate(
		&model.Option{},
		&model.TopUp{},
		&model.AdtyTopupOrder{},
		&model.AdtySiteInvite{},
		&model.AdtySiteReferralRate{},
		&model.AdtySiteWallet{},
		&model.AdtySiteWalletLedger{},
	))
	require.NoError(t, db.AutoMigrate(model.ReferralTables()...))
	require.NotNil(t, model.AdtyBeforeTopUpCreateTx)

	root := ftrRound1User(t, db, "ftr1605-root", common.RoleRootUser)
	owner := ftrRound1User(t, db, "ftr1605-owner", common.RoleCommonUser)
	inviter := ftrRound1User(t, db, "ftr1605-inviter", common.RoleCommonUser)
	invitee := ftrRound1User(t, db, "ftr1605-invitee", common.RoleCommonUser)
	require.Equal(t, common.RoleRootUser, root.Role)
	require.Equal(t, common.RoleCommonUser, owner.Role)
	require.Equal(t, common.RoleCommonUser, inviter.Role)
	require.Equal(t, common.RoleCommonUser, invitee.Role)
	site := model.AdtySite{
		OwnerUserId: owner.Id, Subdomain: "ftr1605", Status: model.AdtySiteStatusActive,
	}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id IN ?", []int{inviter.Id, invitee.Id}).Update("site_id", site.Id).Error)
	var storedInviter, storedInvitee model.User
	require.NoError(t, db.First(&storedInviter, inviter.Id).Error)
	require.NoError(t, db.First(&storedInvitee, invitee.Id).Error)
	assert.Equal(t, site.Id, storedInviter.SiteId)
	assert.Equal(t, site.Id, storedInvitee.SiteId)
	for _, id := range []int{root.Id, owner.Id, inviter.Id, invitee.Id} {
		ftrRound1PinQuota(t, db, id)
	}

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMapRWMutex.Unlock()
	require.NoError(t, SaveAdtySiteCommissionPolicy("100", "7"))
	require.NoError(t, db.Create(&model.AdtySiteReferralRate{
		SiteId: site.Id, Rate: decimal.RequireFromString("0.10"), Enabled: true,
	}).Error)
	invite := model.AdtySiteInvite{
		SiteId: site.Id, InviterId: inviter.Id, InviteeId: invitee.Id,
		Rate: decimal.RequireFromString("0.10"),
	}
	require.NoError(t, db.Create(&invite).Error)
	const createdAt int64 = 1_700_000_000
	require.NoError(t, db.Model(&model.AdtySiteInvite{}).Where("id = ?", invite.Id).Update("created_at", createdAt).Error)

	tradeNo := "ftr1605-trade"
	topUp := &model.TopUp{
		UserId: invitee.Id, TradeNo: tradeNo, PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		PaymentMethod: "usdt", Status: common.TopUpStatusPending, MoneyExact: decimal.RequireFromString("100"),
	}
	ext := &model.AdtyTopupOrder{
		Business: model.AdtyBusinessWalletTopup, TradeNo: tradeNo, UserId: invitee.Id,
		SiteId: site.Id, TeamId: 0, FaceUSD: decimal.RequireFromString("100"),
		Provider: model.PaymentProviderAdtyBEpusdt, CreatedAt: createdAt,
	}
	require.NoError(t, model.CreateAdtyTopUp(topUp, ext))

	require.NoError(t, db.Model(&model.AdtySiteInvite{}).Where("id = ?", invite.Id).Update("rate", decimal.RequireFromString("0.20")).Error)
	var live model.AdtySiteInvite
	require.NoError(t, db.First(&live, invite.Id).Error)
	assert.True(t, live.Rate.Equal(decimal.RequireFromString("0.20")))

	stored, err := model.GetAdtyTopupOrder(tradeNo)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, site.Id, stored.SiteId)
	assert.Equal(t, owner.Id, stored.SiteOwnerUserId)
	assert.Zero(t, stored.TeamId)
	assert.NotContains(t, stored.SiteReferralSnapshot, "0.20")

	var snap struct {
		Captured        bool            `json:"captured"`
		Eligible        bool            `json:"eligible"`
		Enabled         bool            `json:"enabled"`
		SiteId          int             `json:"site_id"`
		PayerTeamId     int64           `json:"payer_team_id"`
		InviterId       int             `json:"inviter_id"`
		InviteeId       int             `json:"invitee_id"`
		FaceUSD         decimal.Decimal `json:"face_usd"`
		Rate            decimal.Decimal `json:"rate"`
		AmountUSD       decimal.Decimal `json:"amount_usd"`
		ObservationDays int64           `json:"observation_days"`
		CreatedAt       int64           `json:"created_at"`
	}
	require.NoError(t, json.Unmarshal([]byte(stored.SiteReferralSnapshot), &snap))
	assert.True(t, snap.Captured)
	assert.True(t, snap.Eligible)
	assert.True(t, snap.Enabled)
	assert.Equal(t, site.Id, snap.SiteId)
	assert.Zero(t, snap.PayerTeamId)
	assert.Equal(t, inviter.Id, snap.InviterId)
	assert.Equal(t, invitee.Id, snap.InviteeId)
	assert.True(t, snap.FaceUSD.Equal(decimal.RequireFromString("100")))
	assert.True(t, snap.Rate.Equal(decimal.RequireFromString("0.10")))
	assert.True(t, snap.AmountUSD.Equal(decimal.RequireFromString("10")))
	assert.EqualValues(t, 7, snap.ObservationDays)
	assert.Equal(t, createdAt, snap.CreatedAt)

	var orders, topups, commissions, walletLedgers int64
	require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Count(&orders).Error)
	require.NoError(t, db.Model(&model.TopUp{}).Count(&topups).Error)
	require.NoError(t, db.Model(&model.ReferralCommission{}).Count(&commissions).Error)
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Count(&walletLedgers).Error)
	assert.EqualValues(t, 1, orders)
	assert.EqualValues(t, 1, topups)
	assert.Zero(t, commissions)
	assert.Zero(t, walletLedgers)
	var pending model.TopUp
	require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&pending).Error)
	assert.Equal(t, common.TopUpStatusPending, pending.Status)
	assert.Zero(t, pending.CompleteTime)
	for _, id := range []int{root.Id, owner.Id, inviter.Id, invitee.Id} {
		ftrRound1AssertNoMoney(t, db, id)
	}
}
