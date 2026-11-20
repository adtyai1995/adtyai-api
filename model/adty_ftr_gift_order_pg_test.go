package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ftrGiftWindows uses the next two CST midnights so Root can grant gifts that
// have not expired yet. The consume and sweep numbers match the opened case:
// principal 1000, permanent bonus 500, later gift 600, earlier gift 800.
func ftrGiftWindows(t *testing.T) (earlier, later, beforeSweep, afterSweep time.Time) {
	t.Helper()
	loc := time.FixedZone("CST", 8*3600)
	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	earlier = start.AddDate(0, 0, 1)
	later = start.AddDate(0, 0, 2)
	beforeSweep = earlier.Add(-time.Second)
	afterSweep = earlier.Add(5 * time.Minute)
	require.Greater(t, earlier.Unix(), common.GetTimestamp())
	return earlier, later, beforeSweep, afterSweep
}

func ftrGiftPG(t *testing.T) *gorm.DB {
	t.Helper()
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(&User{}, &AdtyFundBatch{}, &AdtyFundAllocation{}, &AdtyBalanceLedger{}))
	return db
}

func ftrGiftCustomer(t *testing.T, db *gorm.DB, name string) User {
	t.Helper()
	user := User{
		Username: name,
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    1500,
		SiteId:   0,
	}
	require.NoError(t, db.Create(&user).Error)
	require.Equal(t, common.RoleCommonUser, user.Role)
	return user
}

// ftrConsumeGiftLots grants two future gifts, then spends 1500: principal 1000
// first, then 500 from the earlier gift. Permanent bonus 500 stays.
func ftrConsumeGiftLots(t *testing.T, db *gorm.DB, user User, earlier, later time.Time) *AdtyFundBatch {
	t.Helper()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return CreateAdtyFundBatchesWithSiteTx(tx, user.Id, AdtyFundBatchSiteID(user.SiteId), "topup-perm-"+user.Username, 1000, 500)
	}))
	laterBatch, err := GrantAdtyRootExpiringGift(AdtyRootExpiringGiftGrantInput{
		UserID: user.Id, OperatorID: 1, OperatorRole: common.RoleRootUser,
		Quota: 600, ExpiresAt: later.Unix(), Reason: "trial-later", IdempotencyKey: "grant-later-" + user.Username,
	})
	require.NoError(t, err)
	require.EqualValues(t, 600, laterBatch.Original)
	require.True(t, laterBatch.IsExpiringGift())
	earlierBatch, err := GrantAdtyRootExpiringGift(AdtyRootExpiringGiftGrantInput{
		UserID: user.Id, OperatorID: 1, OperatorRole: common.RoleRootUser,
		Quota: 800, ExpiresAt: earlier.Unix(), Reason: "trial-earlier", IdempotencyKey: "grant-earlier-" + user.Username,
	})
	require.NoError(t, err)
	require.EqualValues(t, 800, earlierBatch.Original)
	require.True(t, earlierBatch.IsExpiringGift())

	var granted User
	require.NoError(t, db.First(&granted, user.Id).Error)
	require.EqualValues(t, 2900, granted.Quota)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		before, debited, err := AdtyDebitUserQuotaTx(tx, user.Id, 1500)
		if err != nil {
			return err
		}
		if before != 2900 || debited != 1500 {
			return ErrAdtyFundBatchShortfall
		}
		return ConsumeAdtyFundBatchesTx(tx, user.Id, 1500, before)
	}))
	return earlierBatch
}

// ADTY-09-09: a charge spends principal first, then the earlier expiring gift.
// Permanent recharge bonus stays untouched.
func TestFTRADTY0909ConsumePrincipalThenExpiringGiftPG(t *testing.T) {
	db := ftrGiftPG(t)
	earlier, later, _, _ := ftrGiftWindows(t)
	user := ftrGiftCustomer(t, db, "ftr0909")
	ftrConsumeGiftLots(t, db, user, earlier, later)

	sum, err := SumAdtyFundBatchRemainingByKind(user.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 0, sum.Principal)
	assert.EqualValues(t, 500, sum.Bonus)
	assert.EqualValues(t, 900, sum.ExpiringBonus)
	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.EqualValues(t, 1400, stored.Quota)
	var expireRows int64
	require.NoError(t, db.Model(&AdtyBalanceLedger{}).Where("user_id = ? AND type = ?", user.Id, AdtyLedgerTypeGiftExpire).Count(&expireRows).Error)
	assert.Zero(t, expireRows)
	var grants int64
	require.NoError(t, db.Model(&AdtyBalanceLedger{}).Where("user_id = ? AND type = ? AND operator_id = ?", user.Id, AdtyLedgerTypeRootExpiringGift, 1).Count(&grants).Error)
	assert.EqualValues(t, 2, grants)
}

// ADTY-09-08: the daily sweep clears only the unconsumed earlier gift after its midnight.
func TestFTRADTY0908ExpireRemainingGiftAfterMidnightPG(t *testing.T) {
	db := ftrGiftPG(t)
	earlier, later, beforeSweep, afterSweep := ftrGiftWindows(t)
	user := ftrGiftCustomer(t, db, "ftr0908")
	earlierBatch := ftrConsumeGiftLots(t, db, user, earlier, later)

	expiredBefore, err := ExpireAdtyGiftBatches(beforeSweep)
	require.NoError(t, err)
	assert.Empty(t, expiredBefore)

	expired, err := ExpireAdtyGiftBatches(afterSweep)
	require.NoError(t, err)
	require.Len(t, expired, 1)
	assert.Equal(t, earlierBatch.Id, expired[0].BatchID)
	assert.EqualValues(t, 800, expired[0].Original)
	assert.EqualValues(t, 500, expired[0].Consumed)
	assert.EqualValues(t, 300, expired[0].ExpiredQuota)
	require.Positive(t, expired[0].LedgerID)

	again, err := ExpireAdtyGiftBatches(afterSweep)
	require.NoError(t, err)
	assert.Empty(t, again)

	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.EqualValues(t, 1100, stored.Quota)
	var ledger AdtyBalanceLedger
	require.NoError(t, db.First(&ledger, expired[0].LedgerID).Error)
	assert.Equal(t, AdtyLedgerTypeGiftExpire, ledger.Type)
	assert.EqualValues(t, -300, ledger.DeltaQuota)
	sum, err := SumAdtyFundBatchRemainingByKind(user.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 0, sum.Principal)
	assert.EqualValues(t, 500, sum.Bonus)
	assert.EqualValues(t, 600, sum.ExpiringBonus)
}
