package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY1006AdminOpenKeepsOwnerAndRejectsHandoverPG(t *testing.T) {
	db := prepareSiteDB(t)
	pgtest.RecordServerVersion(t, db)
	const quota = 500000
	owner := referralUser(t, db, "ftr-1006-owner")
	root := referralUser(t, db, "ftr-1006-root")
	next := referralUser(t, db, "ftr-1006-next")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", root.Id).Update("role", common.RoleRootUser).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id IN ?", []int{owner.Id, root.Id, next.Id}).Update("quota", quota).Error)

	_, err := OpenAdtySite(owner.Id, "ftr1006self", model.AdtySiteOpenedByApply, AdtySitePreset{})
	assert.ErrorIs(t, err, ErrAdtySiteSelfServeRetired)

	site, err := OpenAdtySite(owner.Id, "ftr1006", model.AdtySiteOpenedByAdmin, AdtySitePreset{})
	require.NoError(t, err)
	assert.Equal(t, owner.Id, site.OwnerUserId)
	assert.Equal(t, model.AdtySiteStatusActive, site.Status)
	assert.Equal(t, model.AdtySiteOpenedByAdmin, site.OpenedBy)

	var stored model.AdtySite
	require.NoError(t, db.First(&stored, site.Id).Error)
	assert.Equal(t, owner.Id, stored.OwnerUserId)

	version := stored.OwnershipVersion
	_, err = HandoverAdtySiteOwner(AdtySiteOwnerHandoverInput{
		SiteId: site.Id, OperatorId: root.Id,
		ExpectedOwnerUserId: owner.Id, ExpectedOwnershipVersion: &version,
		NewOwnerUserId: next.Id, Reason: "must not replace the opening owner",
	})
	assert.ErrorIs(t, err, ErrAdtySiteOwnerImmutable)
	require.NoError(t, db.First(&stored, site.Id).Error)
	assert.Equal(t, owner.Id, stored.OwnerUserId, "a refused handover must leave the opening owner in place")

	for _, id := range []int{owner.Id, root.Id, next.Id} {
		var storedQuota int
		require.NoError(t, db.Model(&model.User{}).Where("id = ?", id).Select("quota").Scan(&storedQuota).Error)
		assert.Equal(t, quota, storedQuota)
	}
	var wallets []model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ?", site.Id).Find(&wallets).Error)
	for _, wallet := range wallets {
		assert.True(t, wallet.Balance.IsZero(), wallet.Kind)
	}
}
