package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ADTY-21-06: WriteOfficialAuditLog and AppendAuditIntentTx persist the actor
// and action. They are not the staff-balance booking path.
func TestFTRADTY2106OfficialAuditPersistPG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(
		&User{},
		&AuditIntent{},
		&AdtySiteWallet{},
		&AdtySiteWalletLedger{},
		&AdtyBalanceLedger{},
		&AdtyFundBatch{},
		&AdtyFundAllocation{},
		&Log{},
	))
	require.NoError(t, MigrateAuditLogs())

	const quota = 500000
	admin := User{
		Username: "ftr2106-admin", Password: "hashed-password", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: quota,
	}
	staff := User{
		Username: "ftr2106-staff", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: quota, StaffSiteId: 4,
	}
	require.NoError(t, db.Create(&admin).Error)
	require.NoError(t, db.Create(&staff).Error)
	wallet := AdtySiteWallet{
		SiteId: staff.StaffSiteId, Kind: AdtySiteWalletEarnings, Currency: "USD",
		Balance: decimal.RequireFromString("12.50"),
	}
	require.NoError(t, db.Create(&wallet).Error)

	var usersBefore, walletsBefore, walletLedgersBefore, balanceLedgersBefore, batchesBefore, allocationsBefore, logsBefore int64
	require.NoError(t, db.Model(&User{}).Count(&usersBefore).Error)
	require.NoError(t, db.Model(&AdtySiteWallet{}).Count(&walletsBefore).Error)
	require.NoError(t, db.Model(&AdtySiteWalletLedger{}).Count(&walletLedgersBefore).Error)
	require.NoError(t, db.Model(&AdtyBalanceLedger{}).Count(&balanceLedgersBefore).Error)
	require.NoError(t, db.Model(&AdtyFundBatch{}).Count(&batchesBefore).Error)
	require.NoError(t, db.Model(&AdtyFundAllocation{}).Count(&allocationsBefore).Error)
	require.NoError(t, db.Model(&Log{}).Count(&logsBefore).Error)
	assert.Equal(t, int64(2), usersBefore)
	assert.Equal(t, int64(1), walletsBefore)
	assert.Zero(t, walletLedgersBefore)
	assert.Zero(t, balanceLedgersBefore)
	assert.Zero(t, batchesBefore)
	assert.Zero(t, allocationsBefore)
	assert.Zero(t, logsBefore)

	writes := []struct {
		user       User
		action     string
		objectType string
		objectID   string
		eventID    string
		siteID     int
		createdAt  int64
	}{
		{admin, "adty.admin_permissions.update", "user", "22", "ftr-2106-admin", 0, 1_763_000_210},
		{staff, "adty.staff.customers.read", "site.staff", "9", "ftr-2106-staff", staff.StaffSiteId, 1_763_000_211},
	}
	for _, write := range writes {
		rec := OfficialAuditWrite{
			UserId: write.user.Id, Username: write.user.Username, ActorRole: write.user.Role,
			SiteID: OfficialAuditSiteIDPointer(write.siteID), ObjectType: write.objectType, ObjectID: write.objectID,
			Action: write.action, Category: AuditCategoryOperation, Result: AuditResultSuccess, Success: true,
			RequestId: write.eventID, EventId: write.eventID, CreatedAt: write.createdAt,
		}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			entry, err := PrepareOfficialAuditLog(nil, rec)
			if err != nil {
				return err
			}
			return AppendAuditIntentTx(tx, entry)
		}))
		require.NoError(t, WriteOfficialAuditLog(nil, rec))

		var stored AuditLog
		require.NoError(t, LOG_DB.Where("event_id = ?", write.eventID).Take(&stored).Error)
		assert.Equal(t, write.user.Id, stored.UserId)
		assert.Equal(t, write.user.Username, stored.Username)
		assert.Equal(t, write.user.Role, stored.ActorRole)
		assert.Equal(t, write.action, stored.Action)

		var intent AuditIntent
		require.NoError(t, db.Where("event_id = ?", write.eventID).Take(&intent).Error)
		var payload AuditLog
		require.NoError(t, common.UnmarshalJsonStr(intent.Payload, &payload))
		assert.Equal(t, write.user.Id, payload.UserId)
		assert.Equal(t, write.user.Username, payload.Username)
		assert.Equal(t, write.user.Role, payload.ActorRole)
		assert.Equal(t, write.action, payload.Action)
	}

	for _, userID := range []int{admin.Id, staff.Id} {
		var stored User
		require.NoError(t, db.Select("quota", "quota_pending", "used_quota", "checkin_quota").First(&stored, userID).Error)
		assert.Equal(t, quota, stored.Quota)
		assert.Zero(t, stored.QuotaPending)
		assert.Zero(t, stored.UsedQuota)
		assert.Zero(t, stored.CheckinQuota)
	}
	var storedWallet AdtySiteWallet
	require.NoError(t, db.First(&storedWallet, wallet.Id).Error)
	assert.Equal(t, wallet.SiteId, storedWallet.SiteId)
	assert.Equal(t, AdtySiteWalletEarnings, storedWallet.Kind)
	assert.True(t, storedWallet.Balance.Equal(decimal.RequireFromString("12.50")))
	assert.True(t, storedWallet.FrozenUSD.Equal(decimal.Zero))

	var usersAfter, walletsAfter, walletLedgersAfter, balanceLedgersAfter, batchesAfter, allocationsAfter, logsAfter int64
	require.NoError(t, db.Model(&User{}).Count(&usersAfter).Error)
	require.NoError(t, db.Model(&AdtySiteWallet{}).Count(&walletsAfter).Error)
	require.NoError(t, db.Model(&AdtySiteWalletLedger{}).Count(&walletLedgersAfter).Error)
	require.NoError(t, db.Model(&AdtyBalanceLedger{}).Count(&balanceLedgersAfter).Error)
	require.NoError(t, db.Model(&AdtyFundBatch{}).Count(&batchesAfter).Error)
	require.NoError(t, db.Model(&AdtyFundAllocation{}).Count(&allocationsAfter).Error)
	require.NoError(t, db.Model(&Log{}).Count(&logsAfter).Error)
	assert.Equal(t, usersBefore, usersAfter)
	assert.Equal(t, walletsBefore, walletsAfter)
	assert.Equal(t, walletLedgersBefore, walletLedgersAfter)
	assert.Equal(t, balanceLedgersBefore, balanceLedgersAfter)
	assert.Equal(t, batchesBefore, batchesAfter)
	assert.Equal(t, allocationsBefore, allocationsAfter)
	assert.Equal(t, logsBefore, logsAfter)
	assert.Zero(t, walletLedgersAfter)
	assert.Zero(t, balanceLedgersAfter)
	assert.Zero(t, batchesAfter)
	assert.Zero(t, allocationsAfter)
	assert.Zero(t, logsAfter)
}
