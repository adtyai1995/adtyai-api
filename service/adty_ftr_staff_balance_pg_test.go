package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ftrStaffBalanceDB(t *testing.T, tag string) (*gorm.DB, *model.User, *model.User, *model.User, model.AdtySite, string) {
	t.Helper()
	db := announcementTestDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(&model.AuditLog{}, &model.AuditIntent{}, &model.AdtySiteAudit{}, &model.TopUp{}))
	owner := ticketTestUser(t, db, "ftr"+tag+"-owner", common.RoleCommonUser)
	staff := ticketTestUser(t, db, "ftr"+tag+"-staff", common.RoleCommonUser)
	customer := ticketTestUser(t, db, "ftr"+tag+"-customer", common.RoleCommonUser)
	site := model.AdtySite{
		OwnerUserId: owner.Id, Subdomain: "ftr" + tag, Status: model.AdtySiteStatusActive,
		AgreementVer: AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(customer).Update("site_id", site.Id).Error)
	customer.SiteId = site.Id
	require.NoError(t, db.Create(&model.AdtySiteStaff{
		SiteId: site.Id, OwnerUserId: owner.Id, UserId: staff.Id, Status: "enabled", Role: "support",
		Permissions: `["customers.read","customers.balance_request"]`,
	}).Error)
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	trade := financialOriginalForTest(t, db, customer.Id)
	return db, owner, staff, customer, site, trade
}

func ftrStaffQuota(t *testing.T, db *gorm.DB, userID int) int {
	t.Helper()
	var stored model.User
	require.NoError(t, db.Select("id", "quota", "role").First(&stored, userID).Error)
	return stored.Quota
}

func ftrStaffSeedEarnings(t *testing.T, siteID int, amount decimal.Decimal, key string) {
	t.Helper()
	_, err := ApplyAdtySiteWalletChange(siteID, model.AdtySiteWalletEarnings, amount, AdtySiteWalletChange{
		Type: "ftr_seed", Reason: "FTR staff earnings", IdempotencyKey: key,
	})
	require.NoError(t, err)
}

// ADTY-14-06: an enabled staff member submits a customer increase funded by site earnings.
func TestFTRADTY1406StaffSubmitsIncreasePG(t *testing.T) {
	db, _, staff, customer, site, trade := ftrStaffBalanceDB(t, "1406")
	require.Equal(t, common.RoleCommonUser, staff.Role)
	beforeQuota := ftrStaffQuota(t, db, customer.Id)
	var beforeLedgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&beforeLedgers).Error)

	row, err := SubmitAdtyStaffBalanceRequest(staff.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.RequireFromString("2.00"), "ftr1406", model.AdtyStaffBalanceProvenance{
		Reason: "FTR 1406 increase", OrderRef: trade, FundingSource: model.AdtyStaffFundingSourceEarnings,
	})
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.Equal(t, model.AdtyStaffBalancePending, row.Status)
	assert.Equal(t, model.AdtyStaffFundingSourceEarnings, row.FundingSource)
	assert.Equal(t, site.Id, row.SiteId)
	assert.Equal(t, beforeQuota, ftrStaffQuota(t, db, customer.Id))
	assert.True(t, ftrWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).IsZero())
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Equal(t, beforeLedgers, ledgers)
	var audits int64
	require.NoError(t, db.Model(&model.AdtySiteAudit{}).Where("site_id = ? AND action = ?", site.Id, "staff.balance_request").Count(&audits).Error)
	assert.Equal(t, int64(1), audits)
}

// ADTY-14-07: an enabled staff member submits a decrease against a named principal batch.
func TestFTRADTY1407StaffSubmitsDecreasePG(t *testing.T) {
	db, _, staff, customer, site, trade := ftrStaffBalanceDB(t, "1407")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.CreateAdtyFundBatchesWithSiteTx(tx, customer.Id, model.AdtyFundBatchSiteID(site.Id), "ftr1407-batch", 500000, 0)
	}))
	beforeQuota := ftrStaffQuota(t, db, customer.Id)
	var beforeLedgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&beforeLedgers).Error)

	row, err := SubmitAdtyStaffBalanceRequest(staff.Id, customer.Id, model.AdtyStaffBalanceDecrease, decimal.NewFromInt(1), "ftr1407", model.AdtyStaffBalanceProvenance{
		Reason: "FTR 1407 decrease", OrderRef: trade, FundingSource: "specified_batch",
		BatchKind: model.AdtyFundBatchKindPrincipal, BatchSource: "ftr1407-batch",
	})
	require.NoError(t, err)
	assert.Equal(t, model.AdtyStaffBalancePending, row.Status)
	assert.Equal(t, "specified_batch", row.FundingSource)
	assert.Equal(t, model.AdtyFundBatchKindPrincipal, row.BatchKind)
	assert.Equal(t, "ftr1407-batch", row.BatchSource)
	assert.Equal(t, beforeQuota, ftrStaffQuota(t, db, customer.Id))
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Equal(t, beforeLedgers, ledgers)
	assert.Equal(t, common.RoleCommonUser, staff.Role)
}

// ADTY-14-08: the site owner approves an increase and the earnings wallet funds it.
func TestFTRADTY1408OwnerApprovesIncreasePG(t *testing.T) {
	db, owner, staff, customer, site, trade := ftrStaffBalanceDB(t, "1408")
	require.Equal(t, common.RoleCommonUser, owner.Role)
	ftrStaffSeedEarnings(t, site.Id, decimal.NewFromInt(8), "ftr1408-seed")
	row, err := SubmitAdtyStaffBalanceRequest(staff.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.NewFromInt(2), "ftr1408", model.AdtyStaffBalanceProvenance{
		Reason: "FTR 1408 approve", OrderRef: trade, FundingSource: model.AdtyStaffFundingSourceEarnings,
	})
	require.NoError(t, err)
	beforeQuota := ftrStaffQuota(t, db, customer.Id)
	// The fixture top-up credits operating principal and the refund does not reverse it.
	beforeOperating := ftrWalletBalance(t, db, site.Id, model.AdtySiteWalletOperating)

	require.NoError(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", row.Id, true))

	var reviewed model.AdtyStaffBalanceRequest
	require.NoError(t, db.First(&reviewed, row.Id).Error)
	assert.Equal(t, model.AdtyStaffBalanceApproved, reviewed.Status)
	assert.Equal(t, "owner", reviewed.ReviewerRole)
	assert.Equal(t, owner.Id, reviewed.ReviewerId)
	assert.Equal(t, beforeQuota+2*500000, ftrStaffQuota(t, db, customer.Id))
	assert.True(t, ftrWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(decimal.NewFromInt(6)))
	assert.True(t, ftrWalletBalance(t, db, site.Id, model.AdtySiteWalletOperating).Equal(beforeOperating.Add(decimal.NewFromInt(2))))
	var credit model.AdtySiteWalletLedger
	require.NoError(t, db.Where("site_id = ? AND wallet_kind = ? AND type = ? AND ref_type = ?", site.Id, model.AdtySiteWalletOperating, "principal_credit", "staff_balance").First(&credit).Error)
	assert.True(t, credit.Delta.Equal(decimal.NewFromInt(2)))
}

// ADTY-14-09: Root approves a pending staff increase for any active site.
func TestFTRADTY1409RootApprovesIncreasePG(t *testing.T) {
	db, _, staff, customer, site, trade := ftrStaffBalanceDB(t, "1409")
	root := ticketTestUser(t, db, "ftr1409-root", common.RoleRootUser)
	require.Equal(t, common.RoleRootUser, root.Role)
	ftrStaffSeedEarnings(t, site.Id, decimal.RequireFromString("10.00"), "ftr1409-seed")
	row, err := SubmitAdtyStaffBalanceRequest(staff.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.RequireFromString("1.50"), "ftr1409", model.AdtyStaffBalanceProvenance{
		Reason: "FTR 1409 root", OrderRef: trade, FundingSource: model.AdtyStaffFundingSourceEarnings,
	})
	require.NoError(t, err)
	beforeOperating := ftrWalletBalance(t, db, site.Id, model.AdtySiteWalletOperating)

	require.NoError(t, ReviewAdtyStaffBalanceRequest(root.Id, "root", row.Id, true))

	var reviewed model.AdtyStaffBalanceRequest
	require.NoError(t, db.First(&reviewed, row.Id).Error)
	assert.Equal(t, model.AdtyStaffBalanceApproved, reviewed.Status)
	assert.Equal(t, "root", reviewed.ReviewerRole)
	assert.Equal(t, root.Id, reviewed.ReviewerId)
	assert.Equal(t, 750000, ftrStaffQuota(t, db, customer.Id))
	assert.True(t, ftrWalletBalance(t, db, site.Id, model.AdtySiteWalletEarnings).Equal(decimal.RequireFromString("8.50")))
	assert.True(t, ftrWalletBalance(t, db, site.Id, model.AdtySiteWalletOperating).Equal(beforeOperating.Add(decimal.RequireFromString("1.50"))))
	var credit model.AdtySiteWalletLedger
	require.NoError(t, db.Where("site_id = ? AND wallet_kind = ? AND type = ? AND ref_type = ?", site.Id, model.AdtySiteWalletOperating, "principal_credit", "staff_balance").First(&credit).Error)
	assert.True(t, credit.Delta.Equal(decimal.RequireFromString("1.50")))
}

// ADTY-14-10: approval stores the quota ledger and both site and official audit rows.
func TestFTRADTY1410ApprovalWritesLedgerAndAuditPG(t *testing.T) {
	db, owner, staff, customer, site, trade := ftrStaffBalanceDB(t, "1410")
	ftrStaffSeedEarnings(t, site.Id, decimal.NewFromInt(10), "ftr1410-seed")
	row, err := SubmitAdtyStaffBalanceRequest(staff.Id, customer.Id, model.AdtyStaffBalanceIncrease, decimal.NewFromInt(3), "ftr1410", model.AdtyStaffBalanceProvenance{
		Reason: "FTR 1410 audit", OrderRef: trade, FundingSource: model.AdtyStaffFundingSourceEarnings,
	})
	require.NoError(t, err)
	beforeQuota := ftrStaffQuota(t, db, customer.Id)

	require.NoError(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", row.Id, true))

	var reviewed model.AdtyStaffBalanceRequest
	require.NoError(t, db.First(&reviewed, row.Id).Error)
	assert.Equal(t, model.AdtyStaffBalanceApproved, reviewed.Status)
	assert.Equal(t, int64(beforeQuota), reviewed.BeforeQuota)
	assert.Equal(t, int64(beforeQuota+3*500000), reviewed.AfterQuota)
	require.Positive(t, reviewed.LedgerId)
	var ledger model.AdtyBalanceLedger
	require.NoError(t, db.First(&ledger, reviewed.LedgerId).Error)
	assert.Equal(t, model.AdtyLedgerTypeManualAdjust, ledger.Type)
	assert.Equal(t, int64(3*500000), ledger.DeltaQuota)
	assert.Equal(t, int64(beforeQuota), ledger.BalanceBefore)
	assert.Equal(t, int64(beforeQuota+3*500000), ledger.BalanceAfter)
	assert.Equal(t, customer.Id, ledger.UserId)
	var siteAudits int64
	require.NoError(t, db.Model(&model.AdtySiteAudit{}).Where("site_id = ? AND action = ?", site.Id, "staff.balance_review").Count(&siteAudits).Error)
	assert.Equal(t, int64(1), siteAudits)
	var official int64
	require.NoError(t, db.Model(&model.AuditLog{}).Where("action = ?", "adty.staff.balance_approve").Count(&official).Error)
	assert.Equal(t, int64(1), official)
	assert.Equal(t, common.RoleCommonUser, owner.Role)
}
