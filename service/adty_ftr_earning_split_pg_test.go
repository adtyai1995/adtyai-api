package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-11-09: a frozen price snapshot splits a confirmed charge and credits site earnings once.
func TestFTRADTY1109DurableEarningSplitPG(t *testing.T) {
	db := ftrRound1PG(t)
	require.NoError(t, db.AutoMigrate(
		&model.BillingOperation{},
		&model.AdtySiteEarningEvent{},
		&model.AdtySiteEarningDaily{},
		&model.AdtySiteEarningLine{},
		&model.AdtySiteEarningFact{},
	))
	owner := ftrRound1User(t, db, "ftr1109-owner", common.RoleCommonUser)
	customer := ftrRound1User(t, db, "ftr1109-customer", common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, owner.Role)
	require.Equal(t, common.RoleCommonUser, customer.Role)
	site := ftrWalletSite(t, db, owner.Id, "ftr1109")
	require.NoError(t, db.Model(customer).Update("site_id", site.Id).Error)
	ftrRound1PinQuota(t, db, owner.Id)
	ftrRound1PinQuota(t, db, customer.Id)

	snapshot, err := model.EncodeRequestPriceSnapshot(&model.RequestPriceSnapshot{
		SchemaVersion: model.RequestPriceSnapshotVersion, ModelName: "ftr1109-model", ModelBasePrice: "1",
		BillingGroup: "default", GroupRatio: "1", SiteCostFactor: "1", SiteMarkupRatio: "0.2",
		BilledGroupRatio: "1.2", FinalPrice: "1.2", Currency: model.RequestPriceCurrencyUSD,
		BillingUnit: model.BillingUnitPerCall, AcceptedAt: 1_794_787_200,
		RuleVersion: model.RequestPriceSnapshotVersion, SupplyCost: "1", SellPrice: "1.2",
	})
	require.NoError(t, err)
	require.NotNil(t, model.DecodeRequestPriceSnapshot(snapshot))
	const key = "request:ftr1109"
	require.NoError(t, db.Create(&model.BillingOperation{
		OperationKey: key, UserId: customer.Id, SiteId: site.Id, SiteOwnerUserId: owner.Id,
		Kind: "request", State: model.BillingSettled, PriceSnapshot: snapshot,
	}).Error)
	require.NoError(t, db.Create(&model.AdtySiteEarningEvent{
		OperationKey: key, SiteId: site.Id, SiteOwnerUserId: owner.Id, UserId: customer.Id,
		Day: "2026-11-19", Quota: 60_000_000, Markup: 1.2, CostFactor: 1, CreatedAt: 1_794_787_200,
	}).Error)

	require.NoError(t, projectAdtySiteEarningEvent(context.Background(), key))
	require.NoError(t, projectAdtySiteEarningEvent(context.Background(), key))

	var event model.AdtySiteEarningEvent
	require.NoError(t, db.Where("operation_key = ?", key).First(&event).Error)
	assert.True(t, event.Projected)
	assert.True(t, ftrWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(decimal.NewFromInt(20)))
	var credit model.AdtySiteWalletLedger
	require.NoError(t, db.Where("site_id = ? AND wallet_kind = ? AND type = ?", site.Id, model.AdtySiteWalletEarnings, "site_earning_settlement").First(&credit).Error)
	assert.True(t, credit.Delta.Equal(decimal.NewFromInt(20)))
	var credits int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ? AND wallet_kind = ? AND type = ?", site.Id, model.AdtySiteWalletEarnings, "site_earning_settlement").Count(&credits).Error)
	assert.Equal(t, int64(1), credits)
	ftrRound1AssertNoMoney(t, db, owner.Id)
	ftrRound1AssertNoMoney(t, db, customer.Id)
}
