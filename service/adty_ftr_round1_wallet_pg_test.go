package service

import (
	"testing"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY0302WalletLotSplitPG(t *testing.T) {
	db := prepareReferralDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(&model.AdtyFundBatch{}, &model.AdtyBalanceLedger{}))

	user := referralUser(t, db, "ftr0302")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", 500000).Error)
	empty := referralUser(t, db, "ftr0302-empty")
	expires := int64(1_800_000_302)
	rows := []model.AdtyFundBatch{
		{UserId: user.Id, Kind: model.AdtyFundBatchKindPrincipal, Source: "ftr-0302-principal", Original: 500000, Remaining: 500000, Status: model.AdtyFundBatchStatusActive},
		{UserId: user.Id, Kind: model.AdtyFundBatchKindBonus, GiftCategory: model.AdtyGiftCategoryPermanent, Source: "ftr-0302-bonus", Original: 250000, Remaining: 250000, Status: model.AdtyFundBatchStatusActive},
		{UserId: user.Id, Kind: model.AdtyFundBatchKindExpiringBonus, GiftCategory: model.AdtyGiftCategoryExpiring, Source: "ftr-0302-expiring", Original: 100000, Remaining: 100000, ExpiresAt: &expires, Status: model.AdtyFundBatchStatusActive},
	}
	require.NoError(t, db.Create(&rows).Error)

	proj, ok, err := ProjectWalletLotsForUser(user)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, WalletLotProjectionSourceBatches, proj.Source)
	require.EqualValues(t, 500000, proj.PrincipalQuota)
	require.EqualValues(t, 250000, proj.BonusQuota)
	require.EqualValues(t, 100000, proj.ExpiringBonusQuota)
	require.Equal(t, expires, proj.NextExpiresAt)
	require.True(t, proj.PrincipalUSD.Equal(decimal.RequireFromString("1")))
	require.True(t, proj.BonusUSD.Equal(decimal.RequireFromString("0.5")))
	require.True(t, proj.ExpiringBonusUSD.Equal(decimal.RequireFromString("0.2")))
	require.True(t, proj.TotalUSD.Equal(decimal.RequireFromString("1.7")))

	_, emptyOK, err := ProjectWalletLotsForUser(empty)
	require.NoError(t, err)
	require.False(t, emptyOK)

	var stored model.User
	require.NoError(t, db.Select("quota").First(&stored, user.Id).Error)
	require.Equal(t, 500000, stored.Quota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	require.Zero(t, ledgers)
}
