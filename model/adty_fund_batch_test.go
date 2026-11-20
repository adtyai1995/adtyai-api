package model

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestConsumeFundBatchesSpendsPrincipalBeforeBonus(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&AdtyFundBatch{}))
	require.NoError(t, db.Create([]AdtyFundBatch{
		{UserId: 7, Kind: "bonus", Source: "b", Original: 40, Remaining: 40},
		{UserId: 7, Kind: "principal", Source: "late", Original: 30, Remaining: 30},
		{UserId: 7, Kind: "principal", Source: "early", Original: 20, Remaining: 20},
	}).Error)
	require.NoError(t, db.Model(&AdtyFundBatch{}).Where("source = ?", "early").Update("created_at", 1).Error)
	require.NoError(t, db.Model(&AdtyFundBatch{}).Where("source = ?", "late").Update("created_at", 2).Error)
	require.NoError(t, db.Model(&AdtyFundBatch{}).Where("source = ?", "b").Update("created_at", 1).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return ConsumeAdtyFundBatchesTx(tx, 7, 45, 0)
	}))
	var rows []AdtyFundBatch
	require.NoError(t, db.Where("user_id = ?", 7).Order("id").Find(&rows).Error)
	remaining := map[string]int64{}
	for _, row := range rows {
		remaining[row.Source] = row.Remaining
	}
	require.EqualValues(t, 0, remaining["early"])
	require.EqualValues(t, 5, remaining["late"])
	require.EqualValues(t, 40, remaining["b"])
}

func TestLegacyBalanceBecomesPrincipalBatch(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&AdtyFundBatch{}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return ConsumeAdtyFundBatchesTx(tx, 8, 40, 100)
	}))
	var row AdtyFundBatch
	require.NoError(t, db.Where("user_id = ?", 8).First(&row).Error)
	require.Equal(t, "principal", row.Kind)
	require.Equal(t, "legacy", row.Source)
	require.EqualValues(t, 60, row.Remaining)
}

func TestReclaimFundBatchesBySourceCapsEachKind(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&AdtyFundBatch{}))
	require.NoError(t, db.Create([]AdtyFundBatch{
		{UserId: 9, Kind: "principal", Source: "order-a", Original: 80, Remaining: 80},
		{UserId: 9, Kind: "bonus", Source: "order-a", Original: 20, Remaining: 20},
		{UserId: 9, Kind: "principal", Source: "order-b", Original: 50, Remaining: 50},
	}).Error)
	var taken int64
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		taken, err = ReclaimAdtyFundBatchesBySourceTx(tx, 9, "order-a", 100, 5)
		return err
	}))
	require.EqualValues(t, 85, taken)
	var rows []AdtyFundBatch
	require.NoError(t, db.Where("user_id = ?", 9).Order("id").Find(&rows).Error)
	got := map[string]int64{}
	for _, row := range rows {
		got[row.Source+":"+row.Kind] = row.Remaining
	}
	require.EqualValues(t, 0, got["order-a:principal"])
	require.EqualValues(t, 15, got["order-a:bonus"])
	require.EqualValues(t, 50, got["order-b:principal"])
}

func openMemoryFundBatchDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&AdtyFundBatch{}))
	return db
}

func requireFundLots(t *testing.T, db *gorm.DB, userID int, source string, principal, bonus int64, lotCount int) {
	t.Helper()
	var rows []AdtyFundBatch
	require.NoError(t, db.Where("user_id = ? AND source = ?", userID, source).Order("kind").Find(&rows).Error)
	require.Len(t, rows, lotCount)
	got := map[string]int64{}
	for _, row := range rows {
		got[row.Kind] += row.Remaining
	}
	require.EqualValues(t, principal, got["principal"])
	require.EqualValues(t, bonus, got["bonus"])
}

func TestCreateAdtyFundBatchesTxReplayDoesNotDuplicate(t *testing.T) {
	db := openMemoryFundBatchDB(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return CreateAdtyFundBatchesTx(tx, 11, "order-1", 80, 20)
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return CreateAdtyFundBatchesTx(tx, 11, "order-1", 80, 20)
	}))
	requireFundLots(t, db, 11, "order-1", 80, 20, 2)
}

func TestCreateAdtyFundBatchesTxAllowsPrincipalAndBonusForSameSource(t *testing.T) {
	db := openMemoryFundBatchDB(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return CreateAdtyFundBatchesTx(tx, 12, "order-2", 70, 15)
	}))
	requireFundLots(t, db, 12, "order-2", 70, 15, 2)
}

func TestCreateAdtyFundBatchesTxDifferentSourceStillInserts(t *testing.T) {
	db := openMemoryFundBatchDB(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := CreateAdtyFundBatchesTx(tx, 13, "order-a", 50, 10); err != nil {
			return err
		}
		return CreateAdtyFundBatchesTx(tx, 13, "order-b", 30, 0)
	}))
	requireFundLots(t, db, 13, "order-a", 50, 10, 2)
	requireFundLots(t, db, 13, "order-b", 30, 0, 1)
}

func TestAdtyFundBatchUniqueOnUserSourceKind(t *testing.T) {
	db := openMemoryFundBatchDB(t)
	require.NoError(t, db.Create(&AdtyFundBatch{UserId: 14, Kind: "principal", Source: "order-u", Original: 10, Remaining: 10}).Error)
	err := db.Create(&AdtyFundBatch{UserId: 14, Kind: "principal", Source: "order-u", Original: 10, Remaining: 10}).Error
	require.Error(t, err)
	require.True(t, isAdtyUniqueViolation(err), err)
	require.NoError(t, db.Create(&AdtyFundBatch{UserId: 14, Kind: "bonus", Source: "order-u", Original: 4, Remaining: 4}).Error)
	require.NoError(t, db.Create(&AdtyFundBatch{UserId: 15, Kind: "principal", Source: "order-u", Original: 10, Remaining: 10}).Error)
}

// ISS-R4-0025 的形状：先只传 principal，之后同一个 source 再补 bonus。
// 旧判据只数 (user_id, source)，第二次调用撞 existing > 0 就 return nil，bonus 永不落库。
func TestCreateAdtyFundBatchesTxAddsKindMissingUnderSameSource(t *testing.T) {
	db := openMemoryFundBatchDB(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return CreateAdtyFundBatchesTx(tx, 31, "trade-31", 60, 0)
	}))
	requireFundLots(t, db, 31, "trade-31", 60, 0, 1)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return CreateAdtyFundBatchesTx(tx, 31, "trade-31", 60, 15)
	}))
	requireFundLots(t, db, 31, "trade-31", 60, 15, 2)
}

// 并发下两个事务对同一个 source 各写一种 kind：要么两行都在，要么失败的一方
// 真的返回错误。绝不允许「返回 nil 但行不在」——那是旧代码的假成功。
func TestCreateAdtyFundBatchesTxConcurrentKindsNeverFakeSuccess(t *testing.T) {
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&AdtyFundBatch{}))

	const userID = 32
	const source = "concurrent-kinds"
	credits := []struct {
		kind      string
		principal int64
		bonus     int64
	}{
		{kind: "principal", principal: 100},
		{kind: "bonus", bonus: 25},
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, len(credits))
	for i := range credits {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = db.Transaction(func(tx *gorm.DB) error {
				return CreateAdtyFundBatchesTx(tx, userID, source, credits[i].principal, credits[i].bonus)
			})
		}(i)
	}
	close(start)
	wg.Wait()

	if errs[0] == nil && errs[1] == nil {
		requireFundLots(t, db, userID, source, 100, 25, 2)
		return
	}
	var rows []AdtyFundBatch
	require.NoError(t, db.Where("user_id = ? AND source = ?", userID, source).Find(&rows).Error)
	for i, credit := range credits {
		if errs[i] != nil {
			continue
		}
		found := false
		for _, row := range rows {
			if row.Kind == credit.kind {
				found = true
			}
		}
		require.True(t, found, "worker %d 返回 nil 但 %s 行不在库里（假成功）", i, credit.kind)
	}
}

func TestCreateAdtyFundBatchesTxConcurrentReplayIsIdempotent(t *testing.T) {
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&AdtyFundBatch{}))

	const userID = 22
	const source = "concurrent-source"
	const workers = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = db.Transaction(func(tx *gorm.DB) error {
				if err := CreateAdtyFundBatchesTx(tx, userID, source, 100, 25); err != nil {
					return err
				}
				// The caller's transaction must still be usable after a unique conflict.
				var count int64
				if err := tx.Model(&AdtyFundBatch{}).Where("user_id = ? AND source = ?", userID, source).Count(&count).Error; err != nil {
					return err
				}
				if count == 0 {
					return errors.New("expected lots after idempotent create")
				}
				return nil
			})
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "worker %d", i)
	}
	requireFundLots(t, db, userID, source, 100, 25, 2)
}

func remainingBySource(t *testing.T, db *gorm.DB, userID int) map[string]int64 {
	t.Helper()
	var rows []AdtyFundBatch
	require.NoError(t, db.Where("user_id = ?", userID).Order("id").Find(&rows).Error)
	got := map[string]int64{}
	for _, row := range rows {
		got[row.Source+":"+row.Kind] = row.Remaining
	}
	return got
}

func TestConsumeFundBatchesFullyCoveredDecreasesRemaining(t *testing.T) {
	db := openMemoryFundBatchDB(t)
	require.NoError(t, db.Create([]AdtyFundBatch{
		{UserId: 16, Kind: "principal", Source: "p", Original: 30, Remaining: 30},
		{UserId: 16, Kind: "bonus", Source: "b", Original: 20, Remaining: 20},
	}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return ConsumeAdtyFundBatchesTx(tx, 16, 35, 50)
	}))
	got := remainingBySource(t, db, 16)
	require.EqualValues(t, 0, got["p:principal"])
	require.EqualValues(t, 15, got["b:bonus"])
}

// 「批次少于余额」是账本漂移的形状，不是扣费失败的理由。
//
// 这条用例我原来写的是「库里余额改成 0，却传 balanceBefore=50，然后要求报错」。
// 那个输入自相矛盾：它把 balanceBefore 传成一个自己刚在库里否认掉的值。旧代码
// 不看 balanceBefore（只在无批次时用它建批次），所以这个谎没影响；新代码信任它，
// 于是凭空补了 30 —— 测试红的不是行为，是输入。
//
// 改成前后一致的输入：库里有 50，批次只有 20。结论应当是**能扣、扣完、
// 而且补建出来的那 30 被记成漂移**（由结算层写进 fund_batch_gap）。
func TestConsumeFundBatchesShortfallDoesNotDropRemainder(t *testing.T) {
	db := openMemoryFundBatchDB(t)
	require.NoError(t, db.AutoMigrate(&User{}))
	require.NoError(t, db.Create(&User{Id: 17, Username: "short-17", Password: "password1", Quota: 50}).Error)
	require.NoError(t, db.Create(&AdtyFundBatch{
		UserId: 17, Kind: "principal", Source: "thin", Original: 20, Remaining: 20,
	}).Error)

	var backed int64
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).Where("id = ?", 17).Update("quota", 0).Error; err != nil {
			return err
		}
		got, err := consumeAdtyFundBatchesTx(tx, 17, 50, 50, true, "")
		backed = got
		return err
	})
	require.NoError(t, err, "余额够就该能扣，哪怕批次不全")
	assert.EqualValues(t, 20, backed, "backed 只算补建前真有的额度，凭空补的 30 不算")

	var user User
	require.NoError(t, db.First(&user, 17).Error)
	require.EqualValues(t, 0, user.Quota)
	got := remainingBySource(t, db, 17)
	require.EqualValues(t, 0, got["thin:principal"], "真实批次被扣光")
	require.EqualValues(t, 0, got["legacy:principal"], "补建的 30 也正好扣完")
}

func TestConsumeFundBatchesShortfallWithNoLotsErrors(t *testing.T) {
	db := openMemoryFundBatchDB(t)
	err := db.Transaction(func(tx *gorm.DB) error {
		return ConsumeAdtyFundBatchesTx(tx, 18, 40, 0)
	})
	require.ErrorIs(t, err, ErrAdtyFundBatchShortfall)
	var count int64
	require.NoError(t, db.Model(&AdtyFundBatch{}).Where("user_id = ?", 18).Count(&count).Error)
	require.Zero(t, count)
}

func TestConsumeFundBatchesLegacyLotStillShortErrors(t *testing.T) {
	db := openMemoryFundBatchDB(t)
	err := db.Transaction(func(tx *gorm.DB) error {
		return ConsumeAdtyFundBatchesTx(tx, 19, 40, 25)
	})
	require.ErrorIs(t, err, ErrAdtyFundBatchShortfall)
	var count int64
	require.NoError(t, db.Model(&AdtyFundBatch{}).Where("user_id = ?", 19).Count(&count).Error)
	require.Zero(t, count)
}

func TestConsumeFundBatchesQuietTxShortfallErrors(t *testing.T) {
	db := openMemoryFundBatchDB(t)
	require.NoError(t, db.Create(&AdtyFundBatch{
		UserId: 20, Kind: "principal", Source: "q", Original: 10, Remaining: 10,
	}).Error)
	err := db.Transaction(func(tx *gorm.DB) error {
		return ConsumeAdtyFundBatchesQuietTx(tx, 20, 15, 10)
	})
	require.ErrorIs(t, err, ErrAdtyFundBatchShortfall)
	require.EqualValues(t, 10, remainingBySource(t, db, 20)["q:principal"])
}

func TestApplyWalletDebitToBatchesShortfallAfterReclaimErrors(t *testing.T) {
	db := openMemoryFundBatchDB(t)
	require.NoError(t, db.Create([]AdtyFundBatch{
		{UserId: 21, Kind: "principal", Source: "order-a", Original: 50, Remaining: 50},
		{UserId: 21, Kind: "bonus", Source: "order-a", Original: 10, Remaining: 10},
		{UserId: 21, Kind: "principal", Source: "order-b", Original: 20, Remaining: 20},
	}).Error)
	err := db.Transaction(func(tx *gorm.DB) error {
		return applyAdtyWalletDebitToBatchesTx(tx, 21, "order-a", 50, 10, 100, 80)
	})
	require.ErrorIs(t, err, ErrAdtyFundBatchShortfall)
	got := remainingBySource(t, db, 21)
	require.EqualValues(t, 50, got["order-a:principal"])
	require.EqualValues(t, 10, got["order-a:bonus"])
	require.EqualValues(t, 20, got["order-b:principal"])
}

func TestApplyWalletDebitToBatchesReclaimThenFIFOCovers(t *testing.T) {
	db := openMemoryFundBatchDB(t)
	require.NoError(t, db.Create([]AdtyFundBatch{
		{UserId: 23, Kind: "principal", Source: "order-a", Original: 50, Remaining: 50},
		{UserId: 23, Kind: "bonus", Source: "order-a", Original: 10, Remaining: 10},
		{UserId: 23, Kind: "principal", Source: "order-b", Original: 20, Remaining: 20},
	}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return applyAdtyWalletDebitToBatchesTx(tx, 23, "order-a", 50, 10, 70, 80)
	}))
	got := remainingBySource(t, db, 23)
	require.EqualValues(t, 0, got["order-a:principal"])
	require.EqualValues(t, 0, got["order-a:bonus"])
	require.EqualValues(t, 10, got["order-b:principal"])
}

func openMemoryDBWithoutFundBatchTable(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.False(t, db.Migrator().HasTable(&AdtyFundBatch{}))
	return db
}

func TestCreateAdtyFundBatchesTxMissingTableErrors(t *testing.T) {
	db := openMemoryDBWithoutFundBatchTable(t)
	err := db.Transaction(func(tx *gorm.DB) error {
		return CreateAdtyFundBatchesTx(tx, 41, "order-missing", 80, 20)
	})
	require.ErrorIs(t, err, ErrAdtyFundBatchTableMissing)
}

func TestConsumeFundBatchesMissingTableErrors(t *testing.T) {
	db := openMemoryDBWithoutFundBatchTable(t)
	err := db.Transaction(func(tx *gorm.DB) error {
		return ConsumeAdtyFundBatchesTx(tx, 42, 40, 100)
	})
	require.ErrorIs(t, err, ErrAdtyFundBatchTableMissing)
}

func TestConsumeFundBatchesQuietTxMissingTableErrors(t *testing.T) {
	db := openMemoryDBWithoutFundBatchTable(t)
	err := db.Transaction(func(tx *gorm.DB) error {
		return ConsumeAdtyFundBatchesQuietTx(tx, 43, 15, 50)
	})
	require.ErrorIs(t, err, ErrAdtyFundBatchTableMissing)
}

func TestReclaimFundBatchesMissingTableErrors(t *testing.T) {
	db := openMemoryDBWithoutFundBatchTable(t)
	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := ReclaimAdtyFundBatchesBySourceTx(tx, 44, "order-a", 10, 5)
		return err
	})
	require.ErrorIs(t, err, ErrAdtyFundBatchTableMissing)
}

func TestApplyWalletDebitToBatchesMissingTableErrors(t *testing.T) {
	db := openMemoryDBWithoutFundBatchTable(t)
	err := db.Transaction(func(tx *gorm.DB) error {
		return applyAdtyWalletDebitToBatchesTx(tx, 45, "order-a", 50, 10, 70, 80)
	})
	require.ErrorIs(t, err, ErrAdtyFundBatchTableMissing)
}

// 同一件事：真的扣不出去的时候（余额也不够），钩子不许触发。
//
// 这条原来传的是 balanceBefore=50 而库里根本没有这个用户，同样是自相矛盾的输入；
// 它被改成了「余额确实只有 20，却要扣 50」——这才是钩子不该触发的场景。
func TestConsumeFundBatchesShortfallDoesNotFireHook(t *testing.T) {
	db := openMemoryFundBatchDB(t)
	require.NoError(t, db.Create(&AdtyFundBatch{
		UserId: 46, Kind: "principal", Source: "thin", Original: 20, Remaining: 20,
	}).Error)
	fired := 0
	prev := AdtyAfterPrincipalSpentTx
	AdtyAfterPrincipalSpentTx = func(tx *gorm.DB, userID int, operationKey string, principalQuota int64) error {
		fired++
		return nil
	}
	t.Cleanup(func() { AdtyAfterPrincipalSpentTx = prev })

	err := db.Transaction(func(tx *gorm.DB) error {
		return ConsumeAdtyFundBatchesTx(tx, 46, 50, 20)
	})
	require.ErrorIs(t, err, ErrAdtyFundBatchShortfall)
	require.Zero(t, fired, "扣不出去就不能报「本金已花掉」")
	// 整笔回滚：剩余额度原样留着，不能因为一半扣成功就少掉一块。
	require.EqualValues(t, 20, remainingBySource(t, db, 46)["thin:principal"])
}

func TestConsumeFundBatchesQuietTxFiresBonusHookNotPrincipalHook(t *testing.T) {
	db := openMemoryFundBatchDB(t)
	require.NoError(t, db.Create([]AdtyFundBatch{
		{UserId: 47, Kind: "principal", Source: "topup-p", Original: 50, Remaining: 50},
		{UserId: 47, Kind: "bonus", Source: "topup-b", Original: 20, Remaining: 20},
	}).Error)

	prevPrincipal := AdtyAfterPrincipalSpentTx
	prevBonus := AdtyAfterBonusSpentTx
	principalCalled := 0
	var bonusSpends []AdtyBonusSpend
	AdtyAfterPrincipalSpentTx = func(tx *gorm.DB, userID int, operationKey string, principalQuota int64) error {
		principalCalled++
		t.Errorf("QuietTx fired principal-spent hook user=%d quota=%d", userID, principalQuota)
		return nil
	}
	AdtyAfterBonusSpentTx = func(tx *gorm.DB, userID int, spends []AdtyBonusSpend) error {
		require.Equal(t, 47, userID)
		bonusSpends = append([]AdtyBonusSpend(nil), spends...)
		return nil
	}
	t.Cleanup(func() {
		AdtyAfterPrincipalSpentTx = prevPrincipal
		AdtyAfterBonusSpentTx = prevBonus
	})

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return ConsumeAdtyFundBatchesQuietTx(tx, 47, 60, 70)
	}))
	require.Equal(t, 0, principalCalled)
	require.Equal(t, []AdtyBonusSpend{{Source: "topup-b", Quota: 10}}, bonusSpends)
	require.EqualValues(t, 0, remainingBySource(t, db, 47)["topup-p:principal"])
	require.EqualValues(t, 10, remainingBySource(t, db, 47)["topup-b:bonus"])
}

func TestSumFundBatchRemainingByKind(t *testing.T) {
	db := openMemoryFundBatchDB(t)
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
	require.NoError(t, db.Create([]AdtyFundBatch{
		{UserId: 11, Kind: "principal", Source: "a", Original: 80, Remaining: 50},
		{UserId: 11, Kind: "principal", Source: "b", Original: 20, Remaining: 20},
		{UserId: 11, Kind: "bonus", Source: "a", Original: 40, Remaining: 15},
		{UserId: 12, Kind: "bonus", Source: "c", Original: 9, Remaining: 9},
	}).Error)
	sum, err := SumAdtyFundBatchRemainingByKind(11)
	require.NoError(t, err)
	require.True(t, sum.HasLots)
	require.EqualValues(t, 70, sum.Principal)
	require.EqualValues(t, 15, sum.Bonus)
	other, err := SumAdtyFundBatchRemainingByKind(12)
	require.NoError(t, err)
	require.True(t, other.HasLots)
	require.EqualValues(t, 0, other.Principal)
	require.EqualValues(t, 9, other.Bonus)
	empty, err := SumAdtyFundBatchRemainingByKind(13)
	require.NoError(t, err)
	require.False(t, empty.HasLots)
	require.EqualValues(t, 0, empty.Principal)
	require.EqualValues(t, 0, empty.Bonus)
}

func TestExpiringRootGiftLifecycleOrderDistinctionAndPostMidnightSweep(t *testing.T) {
	db := openMemoryFundBatchDB(t)
	require.NoError(t, db.AutoMigrate(&User{}, &AdtyBalanceLedger{}, &AdtyFundAllocation{}))
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })

	user := User{Id: 901, Username: "gift_user_901", SiteId: 5, Quota: 1500}
	require.NoError(t, db.Create(&user).Error)

	// Recharge principal (1000) + permanent recharge bonus (500).
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return CreateAdtyFundBatchesWithSiteTx(tx, user.Id, AdtyFundBatchSiteID(5), "topup-perm-1", 1000, 500)
	}))

	loc := time.FixedZone("CST", 8*3600)
	// Grant rejects an expiry that is already past. Sweep calls pass their own
	// clock, so these midnights only need to stay in the future and in order.
	now := time.Now().In(loc)
	day1 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).Add(48 * time.Hour)
	day1Midnight := day1.Unix()
	day2Midnight := day1.Add(24 * time.Hour).Unix()

	// Non-Root cannot issue expiring gift.
	_, err := GrantAdtyRootExpiringGift(AdtyRootExpiringGiftGrantInput{
		UserID:         user.Id,
		OperatorID:     2,
		OperatorRole:   common.RoleAdminUser,
		Quota:          800,
		ExpiresAt:      day1Midnight,
		Reason:         "trial",
		IdempotencyKey: "grant-non-root",
	})
	require.Error(t, err)

	// Root issues two expiring gifts: later expiry (600) created first, earlier expiry (800) created second.
	laterBatch, err := GrantAdtyRootExpiringGift(AdtyRootExpiringGiftGrantInput{
		UserID:         user.Id,
		OperatorID:     1,
		OperatorRole:   common.RoleRootUser,
		AmountUSD:      decimal.RequireFromString("0.0012"), // 600 quota
		ExpiresAt:      day2Midnight,
		Reason:         "trial-day2",
		IdempotencyKey: "grant-day2",
	})
	require.NoError(t, err)
	require.EqualValues(t, 600, laterBatch.Original)
	require.True(t, laterBatch.IsExpiringGift())

	earlierBatch, err := GrantAdtyRootExpiringGift(AdtyRootExpiringGiftGrantInput{
		UserID:         user.Id,
		OperatorID:     1,
		OperatorRole:   common.RoleRootUser,
		Quota:          800,
		ExpiresAt:      day1Midnight,
		Reason:         "trial-day1",
		IdempotencyKey: "grant-day1",
	})
	require.NoError(t, err)
	require.EqualValues(t, 800, earlierBatch.Original)

	// Idempotent replay returns same batch without double-crediting; conflicting parameters reject.
	replayed, err := GrantAdtyRootExpiringGift(AdtyRootExpiringGiftGrantInput{
		UserID:         user.Id,
		OperatorID:     1,
		OperatorRole:   common.RoleRootUser,
		Quota:          800,
		ExpiresAt:      day1Midnight,
		Reason:         "trial-day1",
		IdempotencyKey: "grant-day1",
	})
	require.NoError(t, err)
	require.Equal(t, earlierBatch.Id, replayed.Id)

	_, err = GrantAdtyRootExpiringGift(AdtyRootExpiringGiftGrantInput{
		UserID:         user.Id,
		OperatorID:     1,
		OperatorRole:   common.RoleRootUser,
		Quota:          900,
		ExpiresAt:      day1Midnight,
		Reason:         "trial-day1",
		IdempotencyKey: "grant-day1",
	})
	require.ErrorIs(t, err, ErrAdtyIdempotencyConflict)

	// Balance distinction separates Principal (1000), Permanent Bonus (500), and Expiring Bonus (1400).
	sum, err := SumAdtyFundBatchRemainingByKind(user.Id)
	require.NoError(t, err)
	require.EqualValues(t, 1000, sum.Principal)
	require.EqualValues(t, 500, sum.Bonus)
	require.EqualValues(t, 1400, sum.ExpiringBonus)
	require.EqualValues(t, 1900, sum.TotalBonus())
	require.EqualValues(t, day1Midnight, sum.NextExpiresAt)

	// Consume 1500 quota: must spend 1000 principal first, then 500 from earlier-expiring gift (day1Midnight),
	// leaving 300 on earlier-expiring gift, 600 on later-expiring gift, and 500 on permanent recharge bonus.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if _, _, err := AdtyDebitUserQuotaTx(tx, user.Id, 1500); err != nil {
			return err
		}
		_, err := ConsumeAdtyFundBatchesInOrderTx(tx, user.Id, 1500, 2900)
		return err
	}))

	sumAfterSpend, err := SumAdtyFundBatchRemainingByKind(user.Id)
	require.NoError(t, err)
	require.EqualValues(t, 0, sumAfterSpend.Principal)
	require.EqualValues(t, 500, sumAfterSpend.Bonus, "permanent recharge bonus must remain untouched while expiring gifts remain")
	require.EqualValues(t, 900, sumAfterSpend.ExpiringBonus, "300 on day1 + 600 on day2")

	// One second before day1 midnight, the daily sweep clears nothing.
	beforeMidnight := day1.Add(-time.Second)
	expiredBefore, err := ExpireAdtyGiftBatches(beforeMidnight)
	require.NoError(t, err)
	require.Empty(t, expiredBefore)

	// Five minutes after day1 midnight: sweep clears only the unconsumed 300 on earlierBatch.
	// The 500 already consumed is not cleared again. laterBatch (600) and permanent bonus (500) remain.
	afterDay1Midnight := day1.Add(5 * time.Minute)
	expiredDay1, err := ExpireAdtyGiftBatches(afterDay1Midnight)
	require.NoError(t, err)
	require.Len(t, expiredDay1, 1)
	require.Equal(t, earlierBatch.Id, expiredDay1[0].BatchID)
	require.EqualValues(t, 800, expiredDay1[0].Original)
	require.EqualValues(t, 500, expiredDay1[0].Consumed)
	require.EqualValues(t, 300, expiredDay1[0].ExpiredQuota)
	require.Positive(t, expiredDay1[0].LedgerID)

	// Re-running the sweep at the same timestamp is idempotent.
	reSweep, err := ExpireAdtyGiftBatches(afterDay1Midnight)
	require.NoError(t, err)
	require.Empty(t, reSweep)

	var refreshedUser User
	require.NoError(t, db.First(&refreshedUser, user.Id).Error)
	require.EqualValues(t, 1100, refreshedUser.Quota, "600 later expiring gift + 500 permanent bonus")

	var expireLedger AdtyBalanceLedger
	require.NoError(t, db.First(&expireLedger, expiredDay1[0].LedgerID).Error)
	require.Equal(t, AdtyLedgerTypeGiftExpire, expireLedger.Type)
	require.EqualValues(t, -300, expireLedger.DeltaQuota)
	require.Contains(t, expireLedger.RefId, earlierBatch.Source)

	sumAfterDay1, err := SumAdtyFundBatchRemainingByKind(user.Id)
	require.NoError(t, err)
	require.EqualValues(t, 0, sumAfterDay1.Principal)
	require.EqualValues(t, 500, sumAfterDay1.Bonus)
	require.EqualValues(t, 600, sumAfterDay1.ExpiringBonus)
	require.EqualValues(t, day2Midnight, sumAfterDay1.NextExpiresAt)
}

func expiringGiftPostgres(t *testing.T) (*gorm.DB, User, int64) {
	t.Helper()
	opened, err := OpenAdtyTestDatabase()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, opened.Close()) })
	db := opened.DB
	require.NoError(t, db.AutoMigrate(&User{}, &AdtyFundBatch{}, &AdtyFundAllocation{}, &AdtyBalanceLedger{}))
	var version int
	require.NoError(t, db.Raw("SHOW server_version_num").Scan(&version).Error)
	require.GreaterOrEqual(t, version, 180000)
	t.Logf("server_version_num=%d", version)
	user := User{Username: "expiring-gift-customer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&user).Error)
	return db, user, time.Now().AddDate(0, 0, 2).Truncate(24 * time.Hour).Unix()
}

func TestExpiringRootGiftRejectsMissingRootIdentity(t *testing.T) {
	db, user, expires := expiringGiftPostgres(t)
	for _, role := range []int{0, common.RoleCommonUser, common.RoleAdminUser} {
		_, err := GrantAdtyRootExpiringGift(AdtyRootExpiringGiftGrantInput{UserID: user.Id, OperatorID: 1, OperatorRole: role, Quota: 1000, ExpiresAt: expires, IdempotencyKey: "denied"})
		require.Error(t, err, "role %d must not issue gifts", role)
	}
	_, err := GrantAdtyRootExpiringGift(AdtyRootExpiringGiftGrantInput{UserID: user.Id, OperatorRole: common.RoleRootUser, Quota: 1000, ExpiresAt: expires, IdempotencyKey: "missing-operator"})
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Model(&AdtyFundBatch{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestExpiringRootGiftRejectsAdministrativeOrWrongSiteTarget(t *testing.T) {
	db, user, expires := expiringGiftPostgres(t)
	input := AdtyRootExpiringGiftGrantInput{UserID: user.Id, OperatorID: 1, OperatorRole: common.RoleRootUser, Quota: 1000, ExpiresAt: expires, IdempotencyKey: "target-check"}
	input.SiteID = AdtyFundBatchSiteID(99)
	_, err := GrantAdtyRootExpiringGift(input)
	require.Error(t, err)
	input.SiteID = nil
	for _, role := range []int{common.RoleAdminUser, common.RoleRootUser} {
		require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("role", role).Error)
		_, err := GrantAdtyRootExpiringGift(input)
		require.Error(t, err)
	}
	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Zero(t, stored.Quota)
	var count int64
	require.NoError(t, db.Model(&AdtyFundBatch{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestExpiringRootGiftConcurrentSweepAndSettlementPreserveConservation(t *testing.T) {
	for _, action := range []string{"settle", "release", "refund"} {
		t.Run(action, func(t *testing.T) {
			db, user, expires := expiringGiftPostgres(t)
			batch, err := GrantAdtyRootExpiringGift(AdtyRootExpiringGiftGrantInput{UserID: user.Id, OperatorID: 1, OperatorRole: common.RoleRootUser, Quota: 1000, ExpiresAt: expires, IdempotencyKey: "concurrent-sweep"})
			require.NoError(t, err)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				if _, _, err := AdtyDebitUserQuotaTx(tx, user.Id, 600); err != nil {
					return err
				}
				if err := AllocateAdtyFundAllocationsTx(tx, user.Id, "concurrent-use", 600, 1000); err != nil {
					return err
				}
				if action == "refund" {
					_, err := SettleAdtyFundAllocationsTx(tx, user.Id, "concurrent-use", 600, 400)
					return err
				}
				return nil
			}))
			start := make(chan struct{})
			errs := make(chan error, 2)
			var wg sync.WaitGroup
			wg.Go(func() { <-start; _, err := ExpireAdtyGiftBatches(time.Unix(expires, 0)); errs <- err })
			wg.Go(func() {
				<-start
				errs <- db.Transaction(func(tx *gorm.DB) error {
					credit := int64(600)
					if action == "settle" {
						credit = 350
					}
					if _, err := AdtyCreditUserQuotaTx(tx, user.Id, credit); err != nil {
						return err
					}
					switch action {
					case "settle":
						_, err := SettleAdtyFundAllocationsTx(tx, user.Id, "concurrent-use", 250, 400)
						return err
					case "release":
						return ReleaseAdtyFundAllocationsTx(tx, user.Id, "concurrent-use")
					default:
						return ReverseSettledAdtyFundAllocationsTx(tx, user.Id, "concurrent-use", "concurrent-refund", 600)
					}
				})
			})
			close(start)
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			_, err = ExpireAdtyGiftBatches(time.Unix(expires, 0))
			require.NoError(t, err)
			require.NoError(t, db.First(batch, batch.Id).Error)
			wantExpired := int64(1000)
			if action == "settle" {
				wantExpired = 750
			}
			require.Equal(t, wantExpired, batch.ExpiredQuota)
			require.Zero(t, batch.Remaining)
			var stored User
			require.NoError(t, db.First(&stored, user.Id).Error)
			require.Zero(t, stored.Quota)
			var cleared int64
			require.NoError(t, db.Model(&AdtyBalanceLedger{}).Select("coalesce(sum(delta_quota),0)").Where("user_id = ? AND type = ?", user.Id, AdtyLedgerTypeGiftExpire).Scan(&cleared).Error)
			require.Equal(t, -wantExpired, cleared)
		})
	}
}

func TestExpiringRootGiftConcurrentReplayMaintainsConservation(t *testing.T) {
	db, user, expires := expiringGiftPostgres(t)
	input := AdtyRootExpiringGiftGrantInput{UserID: user.Id, OperatorID: 1, OperatorRole: common.RoleRootUser, Quota: 1000, ExpiresAt: expires, IdempotencyKey: "concurrent"}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := GrantAdtyRootExpiringGift(input); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 1000, stored.Quota)
	var rows int64
	require.NoError(t, db.Model(&AdtyBalanceLedger{}).Count(&rows).Error)
	require.EqualValues(t, 1, rows)
	input.IdempotencyKey = strings.Repeat("prefix", 12) + "-one"
	_, err := GrantAdtyRootExpiringGift(input)
	require.NoError(t, err)
	input.IdempotencyKey = strings.Repeat("prefix", 12) + "-two"
	_, err = GrantAdtyRootExpiringGift(input)
	require.NoError(t, err)
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 3000, stored.Quota, "different full business keys must never collide")
}

func TestExpiringRootGiftSweepClearsReleasedReservations(t *testing.T) {
	db, user, expires := expiringGiftPostgres(t)
	batch, err := GrantAdtyRootExpiringGift(AdtyRootExpiringGiftGrantInput{UserID: user.Id, OperatorID: 1, OperatorRole: common.RoleRootUser, Quota: 1000, ExpiresAt: expires, IdempotencyKey: "held"})
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if _, _, err := AdtyDebitUserQuotaTx(tx, user.Id, 600); err != nil {
			return err
		}
		return AllocateAdtyFundAllocationsTx(tx, user.Id, "pending-gift", 600, 1000)
	}))
	now := time.Unix(expires, 0).Add(5 * time.Minute)
	items, err := ExpireAdtyGiftBatches(now)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.EqualValues(t, 400, items[0].ExpiredQuota)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if _, err := AdtyCreditUserQuotaTx(tx, user.Id, 600); err != nil {
			return err
		}
		return ReleaseAdtyFundAllocationsTx(tx, user.Id, "pending-gift")
	}))
	items, err = ExpireAdtyGiftBatches(now)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.EqualValues(t, 600, items[0].ExpiredQuota)
	require.NoError(t, db.First(batch, batch.Id).Error)
	require.Zero(t, batch.Remaining)
	require.EqualValues(t, 1000, batch.ExpiredQuota)
	items, err = ExpireAdtyGiftBatches(now)
	require.NoError(t, err)
	require.Empty(t, items)
	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Zero(t, stored.Quota)
}

func TestExpiringRootGiftSweepUsesExpiryCalendarDayAndPreservesMismatch(t *testing.T) {
	db, user, expires := expiringGiftPostgres(t)
	loc := time.FixedZone("CST", 8*3600)
	day := time.Unix(expires, 0).In(loc)
	midnight := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	batch, err := GrantAdtyRootExpiringGift(AdtyRootExpiringGiftGrantInput{UserID: user.Id, OperatorID: 1, OperatorRole: common.RoleRootUser, Quota: 1000, ExpiresAt: midnight.Add(12 * time.Hour).Unix(), IdempotencyKey: "calendar"})
	require.NoError(t, err)
	items, err := ExpireAdtyGiftBatches(midnight.Add(-time.Second))
	require.NoError(t, err)
	require.Empty(t, items)
	// Deliberately inconsistent fixture: the sweep must roll back instead of
	// silently destroying more lot quota than it can debit from the wallet.
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 500).Error)
	_, err = ExpireAdtyGiftBatches(midnight.Add(time.Minute))
	require.Error(t, err)
	require.NoError(t, db.First(batch, batch.Id).Error)
	require.EqualValues(t, 1000, batch.Remaining)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 1000).Error)
	items, err = ExpireAdtyGiftBatches(midnight.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.EqualValues(t, 1000, items[0].ExpiredQuota)
}

func TestExpiringRootGiftUsesWalletScaleAndSkipsPaymentGuarantee(t *testing.T) {
	db, user, expires := expiringGiftPostgres(t)
	var paymentSpends []AdtyBonusSpend
	oldHook := AdtyAfterBonusSpentTx
	AdtyAfterBonusSpentTx = func(_ *gorm.DB, _ int, spends []AdtyBonusSpend) error {
		paymentSpends = append(paymentSpends, spends...)
		return nil
	}
	t.Cleanup(func() { AdtyAfterBonusSpentTx = oldHook })
	batch, err := GrantAdtyRootExpiringGift(AdtyRootExpiringGiftGrantInput{
		UserID: user.Id, OperatorID: 1, OperatorRole: common.RoleRootUser,
		AmountUSD: decimal.NewFromInt(2), ExpiresAt: expires, Reason: "trial", IdempotencyKey: "usd-trial",
	})
	require.NoError(t, err)
	var wallet User
	require.NoError(t, db.First(&wallet, user.Id).Error)
	assert.Equal(t, 1_000_000, wallet.Quota)
	assert.EqualValues(t, 1_000_000, batch.Original)

	// Reserve half, settle half of that reservation, then clear the unused lot.
	const reserved, actual int64 = 500_000, 250_000
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if _, _, err := AdtyDebitUserQuotaTx(tx, user.Id, reserved); err != nil {
			return err
		}
		return AllocateAdtyFundAllocationsTx(tx, user.Id, "trial-usage", reserved, 1_000_000)
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if _, err := AdtyCreditUserQuotaTx(tx, user.Id, reserved-actual); err != nil {
			return err
		}
		backed, err := SettleAdtyFundAllocationsTx(tx, user.Id, "trial-usage", actual, 500_000)
		assert.Equal(t, actual, backed)
		return err
	}))
	require.Empty(t, paymentSpends)
	items, err := ExpireAdtyGiftBatches(time.Unix(expires, 0))
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.EqualValues(t, 250_000, items[0].Consumed)
	require.EqualValues(t, 750_000, items[0].ExpiredQuota)
	require.NoError(t, db.First(&wallet, user.Id).Error)
	require.Zero(t, wallet.Quota)
}

func TestExpiringRootGiftAllocatesInOrderAndRefundsOriginalLot(t *testing.T) {
	db, user, expires := expiringGiftPostgres(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if _, err := AdtyCreditUserQuotaTx(tx, user.Id, 1_500); err != nil {
			return err
		}
		return CreateAdtyFundBatchesTx(tx, user.Id, "gift-order", 1_000, 500)
	}))
	grant := func(key string, at int64) *AdtyFundBatch {
		batch, err := GrantAdtyRootExpiringGift(AdtyRootExpiringGiftGrantInput{
			UserID: user.Id, OperatorID: 1, OperatorRole: common.RoleRootUser, Quota: 600,
			ExpiresAt: at, Reason: "trial", IdempotencyKey: key,
		})
		require.NoError(t, err)
		return batch
	}
	later := grant("later-first", expires+86_400)
	first := grant("same-expiry-first", expires)
	second := grant("same-expiry-second", expires)
	var paidLots []AdtyFundBatch
	require.NoError(t, db.Where("user_id = ? AND source = ?", user.Id, "gift-order").Order("id").Find(&paidLots).Error)
	require.Len(t, paidLots, 2)
	require.Nil(t, paidLots[1].ExpiresAt)
	const used int64 = 2_100
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if _, _, err := AdtyDebitUserQuotaTx(tx, user.Id, used); err != nil {
			return err
		}
		if err := AllocateAdtyFundAllocationsTx(tx, user.Id, "ordered-gift-use", used, 3_300); err != nil {
			return err
		}
		backed, err := SettleAdtyFundAllocationsTx(tx, user.Id, "ordered-gift-use", used, 1_200)
		assert.Equal(t, used, backed)
		return err
	}))
	var shares []AdtyFundAllocation
	require.NoError(t, db.Where("operation_id = ?", "ordered-gift-use").Order("seq").Find(&shares).Error)
	require.Len(t, shares, 3)
	require.Equal(t, []int64{paidLots[0].Id, first.Id, second.Id}, []int64{shares[0].BatchId, shares[1].BatchId, shares[2].BatchId})
	require.EqualValues(t, 500, shares[2].Settled)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if _, err := AdtyCreditUserQuotaTx(tx, user.Id, 300); err != nil {
			return err
		}
		return ReverseSettledAdtyFundAllocationsTx(tx, user.Id, "ordered-gift-use", "gift-usage-refund", 300)
	}))
	items, err := ExpireAdtyGiftBatches(time.Unix(expires, 0))
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, second.Id, items[0].BatchID)
	require.EqualValues(t, 200, items[0].Consumed)
	require.EqualValues(t, 400, items[0].ExpiredQuota)
	require.NoError(t, db.First(later, later.Id).Error)
	require.NoError(t, db.First(&paidLots[1], paidLots[1].Id).Error)
	require.EqualValues(t, 600, later.Remaining)
	require.EqualValues(t, 500, paidLots[1].Remaining)
}
