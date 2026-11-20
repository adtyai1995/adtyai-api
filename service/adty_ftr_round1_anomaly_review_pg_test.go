package service

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY2110ReviewAnomalyDispositionPG(t *testing.T) {
	db := prepareSiteDB(t)
	pgtest.RecordServerVersion(t, db)
	owner, site := openActiveSite(t, db, "ftr2110")
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", owner.Id).Update("quota", 500000).Error)
	require.NoError(t, db.AutoMigrate(&model.AdtyNotificationPublication{}))

	require.NoError(t, recordAdtyAnomalyDisposition("balance_ledger_gap", strconv.Itoa(site.Id), "open", "detected", 0))
	rows, err := ListAdtyAnomalyDispositions(site.Id)
	require.NoError(t, err)
	require.Len(t, rows, 1)

	require.Error(t, ReviewAdtyAnomalyDisposition(rows[0].Id, owner.Id, "closed", ""))
	require.NoError(t, ReviewAdtyAnomalyDisposition(rows[0].Id, owner.Id, "reviewing", "looking"))
	require.NoError(t, ReviewAdtyAnomalyDisposition(rows[0].Id, owner.Id, "closed", "checked"))
	require.Error(t, ReviewAdtyAnomalyDisposition(rows[0].Id, owner.Id, "closed", "again"))

	stored, err := ListAdtyAnomalyDispositions(site.Id)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	require.Equal(t, "closed", stored[0].Status)
	require.Equal(t, "checked", stored[0].Note)
	require.Equal(t, owner.Id, stored[0].OperatorId)

	var notices []model.AdtyNotificationPublication
	require.NoError(t, db.Where("business = ?", "fund_anomaly").Find(&notices).Error)
	require.NotEmpty(t, notices)
	var ownerRow model.User
	require.NoError(t, db.Select("site_id").First(&ownerRow, owner.Id).Error)
	for _, notice := range notices {
		require.Equal(t, "user", notice.Audience)
		require.Equal(t, ownerRow.SiteId, notice.TargetSiteId)
		require.Contains(t, notice.Content, "Fund anomaly review recorded.")
		require.Contains(t, notice.TargetSiteName, "user:"+strconv.Itoa(owner.Id))
	}

	var wallet model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletOperating).First(&wallet).Error)
	require.True(t, wallet.Balance.IsZero())
	var user model.User
	require.NoError(t, db.Select("quota").First(&user, owner.Id).Error)
	require.Equal(t, 500000, user.Quota)
}
