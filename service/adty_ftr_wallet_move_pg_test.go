package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ftrWalletLedger(t *testing.T, db *gorm.DB, siteID int, kind, changeType string) model.AdtySiteWalletLedger {
	t.Helper()
	var row model.AdtySiteWalletLedger
	require.NoError(t, db.Where("site_id = ? AND wallet_kind = ? AND type = ?", siteID, kind, changeType).First(&row).Error)
	return row
}

// ADTY-12-02: a site customer top-up credits operating principal; a team order does not.
func TestFTRADTY1202CreditOperatingPrincipalPG(t *testing.T) {
	db := ftrRound1PG(t)
	owner := ftrRound1User(t, db, "ftr1202owner", common.RoleCommonUser)
	customer := ftrRound1User(t, db, "ftr1202customer", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)
	ftrRound1PinQuota(t, db, customer.Id)
	site := ftrWalletSite(t, db, owner.Id, "ftr1202")
	require.NoError(t, db.Model(&customer).Update("site_id", site.Id).Error)

	order := model.AdtyTopupOrder{
		TradeNo: "ftr1202-trade", UserId: customer.Id, SiteId: site.Id, TeamId: 0,
		FaceUSD: decimal.NewFromInt(2), CreditQuota: 2 * 500000,
	}
	require.NoError(t, db.Create(&order).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return adtyCreditOperatingPrincipalTx(tx, &model.TopUp{TradeNo: order.TradeNo}, &order)
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return adtyCreditOperatingPrincipalTx(tx, &model.TopUp{TradeNo: "ftr1202-team"}, &model.AdtyTopupOrder{
			TradeNo: "ftr1202-team", UserId: customer.Id, SiteId: site.Id, TeamId: 9,
			FaceUSD: decimal.NewFromInt(3), CreditQuota: 3 * 500000,
		})
	}))

	assert.True(t, ftrWalletBalance(t, db, site.Id, model.AdtySiteWalletOperating).Equal(decimal.NewFromInt(2)))
	credit := ftrWalletLedger(t, db, site.Id, model.AdtySiteWalletOperating, "principal_credit")
	assert.Equal(t, "operating:topup:ftr1202-trade", credit.IdempotencyKey)
	assert.True(t, credit.Delta.Equal(decimal.NewFromInt(2)))
	var teamRows int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("idempotency_key = ?", "operating:topup:ftr1202-team").Count(&teamRows).Error)
	assert.Zero(t, teamRows)
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	assert.Equal(t, common.RoleCommonUser, customer.Role)
	ftrRound1AssertNoMoney(t, db, customer.Id)
}

// ADTY-12-03: spending customer principal debits only that quota from the operating wallet.
func TestFTRADTY1203DebitOperatingPrincipalPG(t *testing.T) {
	db := ftrRound1PG(t)
	owner := ftrRound1User(t, db, "ftr1203owner", common.RoleCommonUser)
	customer := ftrRound1User(t, db, "ftr1203customer", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)
	ftrRound1PinQuota(t, db, customer.Id)
	site := ftrWalletSite(t, db, owner.Id, "ftr1203")
	require.NoError(t, db.Model(&customer).Update("site_id", site.Id).Error)
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletOperating, decimal.NewFromInt(2), AdtySiteWalletChange{
		Type: "ftr_seed", Reason: "FTR 1203 seed", IdempotencyKey: "ftr1203-seed",
	})
	require.NoError(t, err)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return adtyDebitOperatingPrincipalTx(tx, customer.Id, "ftr1203-op", 500000)
	}))

	assert.True(t, ftrWalletBalance(t, db, site.Id, model.AdtySiteWalletOperating).Equal(decimal.NewFromInt(1)))
	debit := ftrWalletLedger(t, db, site.Id, model.AdtySiteWalletOperating, "principal_consume")
	assert.Equal(t, "operating:consume:ftr1203-op", debit.IdempotencyKey)
	assert.True(t, debit.Delta.Equal(decimal.NewFromInt(-1)))
	assert.False(t, debit.AllowOverdraft)
	assert.Equal(t, common.RoleCommonUser, customer.Role)
	ftrRound1AssertNoMoney(t, db, customer.Id)
}

// ADTY-12-08: a new margin supplement resets cycle usage and advances the cycle number.
func TestFTRADTY1208MarginSupplementResetsCyclePG(t *testing.T) {
	db := ftrRound1PG(t)
	owner := ftrRound1User(t, db, "ftr1208owner", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)
	site := ftrWalletSite(t, db, owner.Id, "ftr1208")

	require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.NewFromInt(100), "ftr1208-open"))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletMargin, decimal.NewFromInt(-20), AdtySiteWalletChange{
		Type: "margin_draw", RefType: "business", RefID: "ftr1208-draw", IdempotencyKey: "ftr1208-draw",
	})
	require.NoError(t, err)
	require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.NewFromInt(80), "ftr1208-reopen"))

	var margin model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&margin).Error)
	assert.True(t, margin.CycleUsedUSD.IsZero())
	assert.Equal(t, int64(2), margin.CycleNumber)
	assert.True(t, margin.CycleTierUSD.Equal(decimal.NewFromInt(80)))
	assert.True(t, margin.Balance.Equal(decimal.NewFromInt(160)))
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrRound1AssertNoMoney(t, db, owner.Id)
}

// ADTY-12-10: confirmed usage credits earnings immediately; the daily sheet does not credit again.
func TestFTRADTY1210EarningAndSettlementSheetPG(t *testing.T) {
	db := ftrRound1PG(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.TopUp{}, &model.AdtySiteEarningFact{}, &model.AdtySiteSettlementSheet{}))
	owner := ftrRound1User(t, db, "ftr1210owner", common.RoleCommonUser)
	customer := ftrRound1User(t, db, "ftr1210customer", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)
	ftrRound1PinQuota(t, db, customer.Id)
	site := ftrWalletSite(t, db, owner.Id, "ftr1210")
	require.NoError(t, db.Model(&customer).Update("site_id", site.Id).Error)
	entry := adtySiteEarningEntry{
		SiteId: site.Id, UserId: customer.Id, SiteOwnerUserId: owner.Id,
		OperationKey: "ftr1210-op", Kind: model.AdtySiteEarningUsage, Reason: "FTR 1210 usage",
		Quota: 625000, Revenue: decimal.RequireFromString("1.25"), Platform: decimal.Zero,
		Earning: decimal.RequireFromString("1.25"), OccurredAt: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC).Unix(),
	}
	require.NoError(t, upsertAdtySiteEarningTx(db, "2026-11-19", entry))
	require.NoError(t, upsertAdtySiteEarningTx(db, "2026-11-19", entry))

	assert.True(t, ftrWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(decimal.RequireFromString("1.25")))
	var earningRows int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ? AND type = ?", site.Id, "site_earning_settlement").Count(&earningRows).Error)
	assert.Equal(t, int64(1), earningRows)

	now := time.Date(2026, 11, 20, 0, 30, 0, 0, time.UTC)
	day, _, _ := previousBusinessWindow(now, time.UTC)
	assert.Equal(t, "2026-11-19", day)
	written, err := GenerateAdtySiteSettlementSheets(now)
	require.NoError(t, err)
	assert.Equal(t, 1, written)
	again, err := GenerateAdtySiteSettlementSheets(now)
	require.NoError(t, err)
	assert.Equal(t, 1, again)
	var sheets int64
	require.NoError(t, db.Model(&model.AdtySiteSettlementSheet{}).Where("site_id = ? AND business_day = ?", site.Id, day).Count(&sheets).Error)
	assert.Equal(t, int64(1), sheets)
	assert.True(t, ftrWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(decimal.RequireFromString("1.25")))
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrRound1AssertNoMoney(t, db, customer.Id)
}
