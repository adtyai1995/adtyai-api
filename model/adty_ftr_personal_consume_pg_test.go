package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ADTY-07-09: a personal consume spends older principal before bonus.
// Spending 45 leaves the early principal at 0, the later principal at 5, and the bonus at 40.
func TestFTRADTY0709PersonalConsumeSpendsPrincipalBeforeBonusPG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(&User{}, &AdtyFundBatch{}, &AdtyBalanceLedger{}))
	user := User{
		Username: "ftr0709", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", SiteId: 0, StaffSiteId: 0, Quota: 500000,
	}
	require.NoError(t, db.Create(&user).Error)
	require.Equal(t, common.RoleCommonUser, user.Role)
	require.Zero(t, user.SiteId)
	require.NoError(t, db.Create([]AdtyFundBatch{
		{UserId: user.Id, Kind: "bonus", Source: "ftr0709-bonus", Original: 40, Remaining: 40},
		{UserId: user.Id, Kind: "principal", Source: "ftr0709-late", Original: 30, Remaining: 30},
		{UserId: user.Id, Kind: "principal", Source: "ftr0709-early", Original: 20, Remaining: 20},
	}).Error)
	require.NoError(t, db.Model(&AdtyFundBatch{}).Where("source = ?", "ftr0709-early").Update("created_at", 1).Error)
	require.NoError(t, db.Model(&AdtyFundBatch{}).Where("source = ?", "ftr0709-late").Update("created_at", 2).Error)
	require.NoError(t, db.Model(&AdtyFundBatch{}).Where("source = ?", "ftr0709-bonus").Update("created_at", 1).Error)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return ConsumeAdtyFundBatchesTx(tx, user.Id, 45, 0)
	}))

	var rows []AdtyFundBatch
	require.NoError(t, db.Where("user_id = ?", user.Id).Find(&rows).Error)
	require.Len(t, rows, 3)
	remaining := map[string]int64{}
	for _, row := range rows {
		remaining[row.Source] = row.Remaining
	}
	assert.EqualValues(t, 0, remaining["ftr0709-early"])
	assert.EqualValues(t, 5, remaining["ftr0709-late"])
	assert.EqualValues(t, 40, remaining["ftr0709-bonus"])
	var stored User
	require.NoError(t, db.Select("id", "quota", "role").First(&stored, user.Id).Error)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	assert.Equal(t, 500000, stored.Quota)
	var ledgers int64
	require.NoError(t, db.Model(&AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
