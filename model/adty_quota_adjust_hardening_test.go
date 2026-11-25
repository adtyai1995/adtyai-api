package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAdjustUserQuotaRejectsZeroDeltaOverride(t *testing.T) {
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&User{},
		&TopUp{},
		&AdtyTopupOrder{},
		&AdtyFinancialAdjustment{},
		&AdtyBalanceLedger{},
		&AdtyAnomalyDisposition{},
		&AuditIntent{},
		&AuditLog{},
	))

	oldDB := DB
	oldLogDB := LOG_DB
	oldRedis := common.RedisEnabled
	oldMemCache := common.MemoryCacheEnabled
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	DB = db
	LOG_DB = db
	t.Cleanup(func() {
		DB = oldDB
		LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		_ = sqlDB.Close()
	})

	root := User{Id: 1, Username: "qa_root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	target := User{Id: 2, Username: "qa_target", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 5_000_000}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&target).Error)
	require.NoError(t, db.Create(&TopUp{
		UserId: target.Id, TradeNo: "ORD_QA_1", Amount: 10, Money: 10, Status: common.TopUpStatusSuccess,
	}).Error)
	require.NoError(t, db.Create(&AdtyTopupOrder{
		TradeNo: "ORD_QA_1", UserId: target.Id, Business: AdtyBusinessWalletTopup, FaceUSD: decimal.NewFromInt(10), CreditQuota: 5_000_000,
	}).Error)

	_, err = AdjustUserQuotaWithLedgerForActor(
		target.Id, root.Id, common.RoleRootUser, false,
		"override", target.Quota, "no-op override", "idem-zero-1",
		AdtyQuotaAdjustProvenance{OrderRef: "ORD_QA_1"},
	)
	require.ErrorIs(t, err, ErrAdtyQuotaAdjustNoChange)

	var adjCount, ledgerCount, auditCount int64
	require.NoError(t, db.Model(&AdtyFinancialAdjustment{}).Count(&adjCount).Error)
	require.NoError(t, db.Model(&AdtyBalanceLedger{}).Count(&ledgerCount).Error)
	require.NoError(t, db.Model(&AuditLog{}).Count(&auditCount).Error)
	assert.Equal(t, int64(0), adjCount, "zero-delta override must not write adjustment rows")
	assert.Equal(t, int64(0), ledgerCount, "zero-delta override must not write ledger rows")
	assert.Equal(t, int64(0), auditCount, "zero-delta override must not write audit rows")
}
