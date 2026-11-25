package service

import (
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProjectWalletLotsForUsersExcludesOverdueExpiringAndHeldAllocations(t *testing.T) {
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.AdtyFundBatch{}, &model.AdtyFundAllocation{}))

	oldDB := model.DB
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		common.RedisEnabled = oldRedis
		_ = sqlDB.Close()
	})

	loc := model.AdtyBusinessLocation(db)
	todayUnix := model.AdtyGiftExpiryMidnightUnix(time.Now().In(loc).Unix(), loc)
	overdueExp := todayUnix + 60
	futureExp := time.Unix(todayUnix, 0).In(loc).AddDate(0, 0, 5).Unix()

	user := model.User{Id: 777}
	pLot := model.AdtyFundBatch{
		UserId: user.Id, Kind: model.AdtyFundBatchKindPrincipal, Source: "p1", Original: 5_000_000, Remaining: 5_000_000, Status: model.AdtyFundBatchStatusActive,
	}
	overdueLot := model.AdtyFundBatch{
		UserId: user.Id, Kind: model.AdtyFundBatchKindBonus, GiftCategory: model.AdtyGiftCategoryExpiring, Source: "g-overdue",
		Original: 2_000_000, Remaining: 2_000_000, ExpiresAt: &overdueExp, Status: model.AdtyFundBatchStatusActive,
	}
	activeLot := model.AdtyFundBatch{
		UserId: user.Id, Kind: model.AdtyFundBatchKindBonus, GiftCategory: model.AdtyGiftCategoryExpiring, Source: "g-live",
		Original: 1_500_000, Remaining: 1_500_000, ExpiresAt: &futureExp, Status: model.AdtyFundBatchStatusActive,
	}
	require.NoError(t, db.Create(&pLot).Error)
	require.NoError(t, db.Create(&overdueLot).Error)
	require.NoError(t, db.Create(&activeLot).Error)
	require.NoError(t, db.Create(&model.AdtyFundAllocation{
		UserId: user.Id, BatchId: pLot.Id, OperationId: "in-flight-1", Seq: 1, Kind: model.AdtyFundBatchKindPrincipal, Source: pLot.Source, Held: 1_000_000, Settled: 0,
	}).Error)

	projections, err := ProjectWalletLotsForUsers([]model.User{user})
	require.NoError(t, err)
	proj, ok := projections[user.Id]
	require.True(t, ok)
	assert.Equal(t, int64(4_000_000), proj.PrincipalQuota)
	assert.Equal(t, int64(1_500_000), proj.ExpiringBonusQuota)
	assert.Equal(t, int64(5_500_000), proj.TotalQuota)
	assert.Equal(t, futureExp, proj.NextExpiresAt)
}
