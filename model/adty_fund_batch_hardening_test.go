package model

import (
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupFundBatchFixTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&Option{},
		&User{},
		&AdtySite{},
		&AdtyTeamMember{},
		&AdtyFundBatch{},
		&AdtyFundAllocation{},
		&AdtyBalanceLedger{},
	))

	oldDB := DB
	oldRedis := common.RedisEnabled
	oldMemCache := common.MemoryCacheEnabled
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	DB = db
	t.Cleanup(func() {
		DB = oldDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		_ = sqlDB.Close()
	})
	return db
}

func TestSumAdtyFundBatchRemainingExcludesExpiredOverdueAndInFlightHolds(t *testing.T) {
	db := setupFundBatchFixTestDB(t)
	loc := AdtyBusinessLocation(db)
	now := time.Now().In(loc)
	todayUnix := AdtyGiftExpiryMidnightUnix(now.Unix(), loc)
	overdueExp := todayUnix + 3600 // today's calendar day -> midnight cutoff <= now (overdue unswept!)
	futureExp1 := time.Unix(todayUnix, 0).In(loc).AddDate(0, 0, 3).Unix()
	futureExp2 := time.Unix(todayUnix, 0).In(loc).AddDate(0, 0, 7).Unix()

	uid := 501
	principalBatch := AdtyFundBatch{
		UserId: uid, Kind: AdtyFundBatchKindPrincipal, Source: "ord-p", Original: 1000, Remaining: 1000, Status: AdtyFundBatchStatusActive,
	}
	permBonusBatch := AdtyFundBatch{
		UserId: uid, Kind: AdtyFundBatchKindBonus, GiftCategory: AdtyGiftCategoryPermanent, Source: "ord-b", Original: 500, Remaining: 500, Status: AdtyFundBatchStatusActive,
	}
	expiredStatusBatch := AdtyFundBatch{
		UserId: uid, Kind: AdtyFundBatchKindBonus, GiftCategory: AdtyGiftCategoryExpiring, Source: "gift-expired-status",
		Original: 400, Remaining: 400, ExpiresAt: &futureExp1, Status: AdtyFundBatchStatusExpired,
	}
	overdueUnsweptBatch := AdtyFundBatch{
		UserId: uid, Kind: AdtyFundBatchKindBonus, GiftCategory: AdtyGiftCategoryExpiring, Source: "gift-overdue",
		Original: 600, Remaining: 600, ExpiresAt: &overdueExp, Status: AdtyFundBatchStatusActive,
	}
	fullyHeldExpiringBatch := AdtyFundBatch{
		UserId: uid, Kind: AdtyFundBatchKindBonus, GiftCategory: AdtyGiftCategoryExpiring, Source: "gift-held",
		Original: 300, Remaining: 300, ExpiresAt: &futureExp1, Status: AdtyFundBatchStatusActive,
	}
	activeExpiringBatch := AdtyFundBatch{
		UserId: uid, Kind: AdtyFundBatchKindBonus, GiftCategory: AdtyGiftCategoryExpiring, Source: "gift-active",
		Original: 700, Remaining: 700, ExpiresAt: &futureExp2, Status: AdtyFundBatchStatusActive,
	}
	require.NoError(t, db.Create(&principalBatch).Error)
	require.NoError(t, db.Create(&permBonusBatch).Error)
	require.NoError(t, db.Create(&expiredStatusBatch).Error)
	require.NoError(t, db.Create(&overdueUnsweptBatch).Error)
	require.NoError(t, db.Create(&fullyHeldExpiringBatch).Error)
	require.NoError(t, db.Create(&activeExpiringBatch).Error)

	// Active in-flight holds: 250 on principal, 100 on perm bonus, 300 (full) on futureExp1, 200 on futureExp2.
	require.NoError(t, db.Create(&AdtyFundAllocation{
		UserId: uid, BatchId: principalBatch.Id, OperationId: "op1", Seq: 1, Kind: AdtyFundBatchKindPrincipal, Source: principalBatch.Source, Held: 350, Settled: 100,
	}).Error)
	require.NoError(t, db.Create(&AdtyFundAllocation{
		UserId: uid, BatchId: permBonusBatch.Id, OperationId: "op2", Seq: 1, Kind: AdtyFundBatchKindBonus, Source: permBonusBatch.Source, Held: 100, Settled: 0,
	}).Error)
	require.NoError(t, db.Create(&AdtyFundAllocation{
		UserId: uid, BatchId: fullyHeldExpiringBatch.Id, OperationId: "op3", Seq: 1, Kind: AdtyFundBatchKindBonus, Source: fullyHeldExpiringBatch.Source, Held: 300, Settled: 0,
	}).Error)
	require.NoError(t, db.Create(&AdtyFundAllocation{
		UserId: uid, BatchId: activeExpiringBatch.Id, OperationId: "op4", Seq: 1, Kind: AdtyFundBatchKindBonus, Source: activeExpiringBatch.Source, Held: 200, Settled: 0,
	}).Error)

	got, err := SumAdtyFundBatchRemainingByKind(uid)
	require.NoError(t, err)
	assert.True(t, got.HasLots)
	assert.Equal(t, int64(750), got.Principal, "principal should subtract 250 active hold")
	assert.Equal(t, int64(400), got.Bonus, "permanent bonus should subtract 100 active hold")
	assert.Equal(t, int64(500), got.ExpiringBonus, "expiring bonus should exclude expired-status, overdue unswept, and held quotas")
	assert.Equal(t, futureExp2, got.NextExpiresAt, "next_expires_at should skip expired, overdue, and fully-held lots")
}

func TestGrantAdtyRootExpiringGiftRejectsDisabledDeletedAndSiteOwners(t *testing.T) {
	db := setupFundBatchFixTestDB(t)
	loc := AdtyBusinessLocation(db)
	todayUnix := AdtyGiftExpiryMidnightUnix(time.Now().In(loc).Unix(), loc)
	tomorrow := time.Unix(todayUnix, 0).In(loc).AddDate(0, 0, 2).Unix()

	enabledCustomer := User{Id: 601, Username: "gift_ok", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	disabledUser := User{Id: 602, Username: "gift_disabled", Role: common.RoleCommonUser, Status: common.UserStatusDisabled}
	deletedUser := User{Id: 603, Username: "gift_deleted", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	siteOwner := User{Id: 604, Username: "gift_owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&enabledCustomer).Error)
	require.NoError(t, db.Create(&disabledUser).Error)
	require.NoError(t, db.Create(&deletedUser).Error)
	require.NoError(t, db.Delete(&deletedUser).Error)
	require.NoError(t, db.Create(&siteOwner).Error)
	require.NoError(t, db.Create(&AdtySite{
		Id: 60, OwnerUserId: siteOwner.Id, Status: AdtySiteStatusActive, Subdomain: "gift60",
	}).Error)

	// Enabled regular customer succeeds.
	err := db.Transaction(func(tx *gorm.DB) error {
		batch, replayed, err := GrantAdtyRootExpiringGiftWithReplayTx(tx, AdtyRootExpiringGiftGrantInput{
			UserID: enabledCustomer.Id, OperatorID: 1, OperatorRole: common.RoleRootUser,
			Quota: 500_000, ExpiresAt: tomorrow, Reason: "welcome", IdempotencyKey: "gift-ok-1",
		})
		require.NoError(t, err)
		assert.False(t, replayed)
		assert.Equal(t, int64(500_000), batch.Remaining)
		return nil
	})
	require.NoError(t, err)

	// Disabled user rejected.
	err = db.Transaction(func(tx *gorm.DB) error {
		_, _, err := GrantAdtyRootExpiringGiftWithReplayTx(tx, AdtyRootExpiringGiftGrantInput{
			UserID: disabledUser.Id, OperatorID: 1, OperatorRole: common.RoleRootUser,
			Quota: 500_000, ExpiresAt: tomorrow, Reason: "test", IdempotencyKey: "gift-disabled-1",
		})
		return err
	})
	require.Error(t, err)

	// Deleted-status user rejected.
	err = db.Transaction(func(tx *gorm.DB) error {
		_, _, err := GrantAdtyRootExpiringGiftWithReplayTx(tx, AdtyRootExpiringGiftGrantInput{
			UserID: deletedUser.Id, OperatorID: 1, OperatorRole: common.RoleRootUser,
			Quota: 500_000, ExpiresAt: tomorrow, Reason: "test", IdempotencyKey: "gift-deleted-1",
		})
		return err
	})
	require.Error(t, err)

	// Pure site owner rejected.
	err = db.Transaction(func(tx *gorm.DB) error {
		_, _, err := GrantAdtyRootExpiringGiftWithReplayTx(tx, AdtyRootExpiringGiftGrantInput{
			UserID: siteOwner.Id, OperatorID: 1, OperatorRole: common.RoleRootUser,
			Quota: 500_000, ExpiresAt: tomorrow, Reason: "test", IdempotencyKey: "gift-owner-1",
		})
		return err
	})
	require.Error(t, err)
}
