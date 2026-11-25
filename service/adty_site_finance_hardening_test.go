package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupSiteFinanceMarginOverdraftTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.AdtySite{},
		&model.AdtySiteOwnership{},
		&model.AdtySiteTier{},
		&model.AdtySiteWallet{},
		&model.AdtySiteWalletLedger{},
		&model.AdtyAnomalyDisposition{},
		&model.AuditLog{},
	))

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedis := common.RedisEnabled
	oldMemCache := common.MemoryCacheEnabled
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	model.DB = db
	model.LOG_DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		_ = sqlDB.Close()
	})
	return db
}

func TestMarginExcessOnlySettleAndCancelSucceedAfterAllowOverdraft(t *testing.T) {
	db := setupSiteFinanceMarginOverdraftTestDB(t)

	owner := model.User{Id: 11, Username: "margin_owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner).Error)
	site := model.AdtySite{Id: 11, OwnerUserId: owner.Id, Status: model.AdtySiteStatusActive, Subdomain: "margin11"}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	require.NoError(t, db.Model(&model.AdtySiteWallet{}).
		Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).
		Update("required_balance", decimal.NewFromInt(100)).Error)

	// Fund margin to $150 ($50 excess above $100 floor).
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletMargin, decimal.NewFromInt(150), AdtySiteWalletChange{
		Type: "margin_topup", RefType: "test", RefID: "m1", IdempotencyKey: "margin:seed:1",
	})
	require.NoError(t, err)

	applyTx := func(delta decimal.Decimal, change AdtySiteWalletChange) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := applyAdtySiteWalletChangeTx(tx, site.Id, model.AdtySiteWalletMargin, delta, change)
			return err
		})
	}

	// Freeze two $20 MarginExcessOnly vouchers (total ExcessFrozenUSD = $40).
	require.NoError(t, applyTx(decimal.Zero, AdtySiteWalletChange{
		Type: "voucher_freeze", RefType: "voucher", RefID: "v1", IdempotencyKey: "vf:1",
		HoldDelta: decimal.NewFromInt(20), MarginExcessOnly: true,
	}))
	require.NoError(t, applyTx(decimal.Zero, AdtySiteWalletChange{
		Type: "voucher_freeze", RefType: "voucher", RefID: "v2", IdempotencyKey: "vf:2",
		HoldDelta: decimal.NewFromInt(20), MarginExcessOnly: true,
	}))

	// Simulate an AllowOverdraft debit that drops Balance below TotalFrozen ($150 -> $10 < $40).
	require.NoError(t, applyTx(decimal.NewFromInt(-140), AdtySiteWalletChange{
		Type: "forced_debit", RefType: "test", RefID: "od1", IdempotencyKey: "od:1",
		AllowOverdraft: true,
	}))

	// New MarginExcessOnly freeze (netNewUse > 0) must still fail with ErrAdtySiteWalletInsufficient.
	err = applyTx(decimal.Zero, AdtySiteWalletChange{
		Type: "voucher_freeze", RefType: "voucher", RefID: "v3", IdempotencyKey: "vf:3",
		HoldDelta: decimal.NewFromInt(5), MarginExcessOnly: true,
	})
	require.ErrorIs(t, err, ErrAdtySiteWalletInsufficient)

	// Settling already-frozen v1 (delta = -20, HoldDelta = -20 -> netNewUse = 0) MUST succeed even though Balance goes to -10.
	require.NoError(t, applyTx(decimal.NewFromInt(-20), AdtySiteWalletChange{
		Type: "voucher_spend", RefType: "voucher", RefID: "v1", IdempotencyKey: "vs:1",
		HoldDelta: decimal.NewFromInt(-20), MarginExcessOnly: true,
	}))

	// Canceling already-frozen v2 (delta = 0, HoldDelta = -20 -> netNewUse = -20) MUST succeed even while Balance is -10.
	require.NoError(t, applyTx(decimal.Zero, AdtySiteWalletChange{
		Type: "voucher_release", RefType: "voucher", RefID: "v2", IdempotencyKey: "vr:2",
		HoldDelta: decimal.NewFromInt(-20), MarginExcessOnly: true,
	}))

	var margin model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&margin).Error)
	assert.True(t, margin.Balance.Equal(decimal.NewFromInt(-10)), "expected balance -10, got %s", margin.Balance)
	assert.True(t, margin.ExcessFrozenUSD.IsZero(), "expected excess_frozen_usd 0, got %s", margin.ExcessFrozenUSD)
}
