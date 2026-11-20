package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ftrWalletSite(t *testing.T, db *gorm.DB, ownerID int, subdomain string) model.AdtySite {
	t.Helper()
	site := model.AdtySite{
		OwnerUserId: ownerID, Subdomain: subdomain, Status: model.AdtySiteStatusActive,
		AgreementVer: AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	return site
}

func ftrWalletBalance(t *testing.T, db *gorm.DB, siteID int, kind string) decimal.Decimal {
	t.Helper()
	var wallet model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", siteID, kind).First(&wallet).Error)
	return wallet.Balance
}

// ADTY-12-01: an operating-wallet credit writes an internal ledger row and does not change user quota.
func TestFTRADTY1201OperatingWalletLedgerPG(t *testing.T) {
	db := ftrRound1PG(t)
	owner := ftrRound1User(t, db, "ftr1201owner", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)
	site := ftrWalletSite(t, db, owner.Id, "ftr1201")

	entry, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletOperating, decimal.RequireFromString("8.00"), AdtySiteWalletChange{
		Type: "ftr_seed", Reason: "FTR 1201 operating", IdempotencyKey: "ftr1201-operating",
	})
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, model.AdtySiteWalletOperating, entry.WalletKind)
	assert.True(t, entry.Delta.Equal(decimal.RequireFromString("8.00")))
	assert.True(t, ftrWalletBalance(t, db, site.Id, model.AdtySiteWalletOperating).Equal(decimal.RequireFromString("8.00")))
	var rows int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ? AND wallet_kind = ?", site.Id, model.AdtySiteWalletOperating).Count(&rows).Error)
	assert.Equal(t, int64(1), rows)
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrRound1AssertNoMoney(t, db, owner.Id)
}

// ADTY-12-04: reconciliation is operating balance minus customer principal.
func TestFTRADTY1204ReconcileOperatingWalletPG(t *testing.T) {
	db := ftrRound1PG(t)
	owner := ftrRound1User(t, db, "ftr1204owner", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)
	principal := decimal.RequireFromString("12.50")
	operating := decimal.RequireFromString("30.00")

	gap := ReconcileOperatingWallet(principal, operating)
	assert.True(t, gap.Equal(decimal.RequireFromString("17.50")))
	assert.True(t, ReconcileOperatingWallet(operating, operating).IsZero())
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrRound1AssertNoMoney(t, db, owner.Id)
}

// ADTY-12-05: an earnings-wallet credit is stored with its ledger.
func TestFTRADTY1205EarningsWalletLedgerPG(t *testing.T) {
	db := ftrRound1PG(t)
	owner := ftrRound1User(t, db, "ftr1205owner", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)
	site := ftrWalletSite(t, db, owner.Id, "ftr1205")

	entry, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.RequireFromString("12.50"), AdtySiteWalletChange{
		Type: "ftr_seed", Reason: "FTR 1205 earnings", IdempotencyKey: "ftr1205-earnings",
	})
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, model.AdtySiteWalletEarnings, entry.WalletKind)
	assert.True(t, ftrWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(decimal.RequireFromString("12.50")))
	listed, total, err := ListAdtyOwnerSiteWalletLedger(site.Id, model.AdtySiteWalletEarnings, 0, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, listed, 1)
	assert.Equal(t, "FTR 1205 earnings", listed[0].Reason)
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrRound1AssertNoMoney(t, db, owner.Id)
}

// ADTY-12-06: a margin-wallet credit is stored with its ledger.
func TestFTRADTY1206MarginWalletLedgerPG(t *testing.T) {
	db := ftrRound1PG(t)
	owner := ftrRound1User(t, db, "ftr1206owner", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)
	site := ftrWalletSite(t, db, owner.Id, "ftr1206")

	entry, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletMargin, decimal.RequireFromString("40.00"), AdtySiteWalletChange{
		Type: "ftr_seed", Reason: "FTR 1206 margin", IdempotencyKey: "ftr1206-margin",
	})
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, model.AdtySiteWalletMargin, entry.WalletKind)
	assert.True(t, ftrWalletBalance(t, db, site.Id, model.AdtySiteWalletMargin).Equal(decimal.RequireFromString("40.00")))
	var rows int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ? AND wallet_kind = ?", site.Id, model.AdtySiteWalletMargin).Count(&rows).Error)
	assert.Equal(t, int64(1), rows)
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrRound1AssertNoMoney(t, db, owner.Id)
}

// ADTY-12-07: the usable margin draw is 90 percent of the frozen tier.
func TestFTRADTY1207MarginCycleLimitPG(t *testing.T) {
	db := ftrRound1PG(t)
	owner := ftrRound1User(t, db, "ftr1207owner", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)

	limit := AdtyMarginCycleLimit(decimal.RequireFromString("100"))
	assert.True(t, limit.Equal(decimal.RequireFromString("90")))
	assert.True(t, AdtyMarginCycleLimit(decimal.Zero).IsZero())
	assert.True(t, AdtyMarginCycleLimit(decimal.RequireFromString("-5")).IsZero())
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrRound1AssertNoMoney(t, db, owner.Id)
}

// ADTY-19-09: a failed translation is stored as failed and not ready.
func TestFTRADTY1909TranslationFallbackPG(t *testing.T) {
	db := ftrRound1PG(t)
	require.NoError(t, db.AutoMigrate(&model.AdtyTranslationHead{}, &model.AdtyTranslationJob{}))
	owner := ftrRound1User(t, db, "ftr1909owner", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)

	result, err := fallbackAdtyTranslation("ftr1909-notice", "原文未译", 0, 1, "ftr1909hash", "ftr-model")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, model.AdtyTranslationFailed, result.Status)
	assert.Equal(t, AdtyTranslationNotReady, result.Notice)
	assert.Equal(t, "原文未译", result.Source)

	var head model.AdtyTranslationHead
	require.NoError(t, db.Where("object_key = ?", "ftr1909-notice").First(&head).Error)
	assert.Equal(t, model.AdtyTranslationFailed, head.Status)
	assert.Equal(t, AdtyTranslationNotReady, head.Notice)
	var jobs int64
	require.NoError(t, db.Model(&model.AdtyTranslationJob{}).Where("object_key = ? AND status = ?", "ftr1909-notice", model.AdtyTranslationFailed).Count(&jobs).Error)
	assert.Equal(t, int64(1), jobs)
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrRound1AssertNoMoney(t, db, owner.Id)
}

// ADTY-19-10: an English rendering keeps the amount, percent, and currency from the source.
func TestFTRADTY1910TranslationKeepsFactsPG(t *testing.T) {
	db := ftrRound1PG(t)
	require.NoError(t, db.AutoMigrate(&model.AdtyGlossaryTerm{}))
	owner := ftrRound1User(t, db, "ftr1910owner", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)
	source := "折扣 10%，金额 12.50 USD"
	english := "Discount 10%, Amount 12.50 USD"

	assert.True(t, adtyTranslationKeepsFactsWithDB(db, source, english))
	assert.False(t, adtyTranslationKeepsFactsWithDB(db, source, "Discount 10%, Amount 112.50 USD"))
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrRound1AssertNoMoney(t, db, owner.Id)
}
