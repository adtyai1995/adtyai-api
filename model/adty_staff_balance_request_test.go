package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func openStaffBalanceRequestTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &AdtyStaffBalanceRequest{}, &AdtyBalanceLedger{}, &AdtyFundBatch{}, &AdtyFundAllocation{}))
	prev := DB
	DB = db
	t.Cleanup(func() { DB = prev })
	return db
}

// ISS-R1-0088：A.4 / A.12.4 员工余额调整申请和站长/主站审批必须保留具体原因、
// 关联业务/订单引用以及出资或扣回来源，不能只写固定字符串 "staff balance request"。
func TestStaffBalanceRequestAndApprovalRetainConcreteProvenance(t *testing.T) {
	db := openStaffBalanceRequestTestDB(t)

	customer := User{Id: 501, Username: "staff_adj_buyer", SiteId: 11, Quota: 2_000_000}
	require.NoError(t, db.Create(&customer).Error)
	require.NoError(t, db.Create(&AdtyFundBatch{
		UserId: customer.Id, SiteId: AdtyFundBatchSiteID(11),
		Kind: AdtyFundBatchKindPrincipal, Source: "ORD-STAFF-1",
		Original: 2_000_000, Remaining: 2_000_000, Status: AdtyFundBatchStatusActive,
	}).Error)

	// 1. Missing reason, business ref, or funding source must be rejected.
	keyMissing := "staff-req-missing"
	_, err := CreateAdtyStaffBalanceRequestWithProvenance(&AdtyStaffBalanceRequest{
		SiteId: 11, StaffUserId: 21, CustomerUserId: customer.Id,
		Direction: AdtyStaffBalanceIncrease, AmountUSD: decimal.NewFromInt(2),
		IdempotencyKey: &keyMissing,
	})
	require.ErrorIs(t, err, ErrAdtyAdjustReasonRequired)

	_, err = CreateAdtyStaffBalanceRequestWithProvenance(&AdtyStaffBalanceRequest{
		SiteId: 11, StaffUserId: 21, CustomerUserId: customer.Id,
		Direction: AdtyStaffBalanceIncrease, AmountUSD: decimal.NewFromInt(2),
		Reason:         "SLA outage compensation",
		IdempotencyKey: &keyMissing,
	})
	require.ErrorIs(t, err, ErrAdtyStaffBalanceBusinessRefRequired)

	_, err = CreateAdtyStaffBalanceRequestWithProvenance(&AdtyStaffBalanceRequest{
		SiteId: 11, StaffUserId: 21, CustomerUserId: customer.Id,
		Direction: AdtyStaffBalanceIncrease, AmountUSD: decimal.NewFromInt(2),
		Reason:         "SLA outage compensation",
		BusinessRef:    "TICKET-9001",
		IdempotencyKey: &keyMissing,
	})
	require.ErrorIs(t, err, ErrAdtyStaffBalanceFundingSourceRequired)

	// 2. Create a valid increase request with concrete reason, business ref, and funding source.
	incKey := "staff-req-inc-1"
	incReq, err := CreateAdtyStaffBalanceRequestWithProvenance(&AdtyStaffBalanceRequest{
		SiteId:         11,
		StaffUserId:    21,
		CustomerUserId: customer.Id,
		Direction:      AdtyStaffBalanceIncrease,
		AmountUSD:      decimal.NewFromInt(3), // 1,500,000 quota
		Reason:         "SLA outage compensation for ticket TICKET-9001",
		BusinessRef:    "TICKET-9001",
		FundingSource:  AdtyStaffFundingSourceEarnings,
		IdempotencyKey: &incKey,
	})
	require.NoError(t, err)
	require.Equal(t, "TICKET-9001", incReq.OrderRef)
	require.Equal(t, AdtyStaffFundingSourceEarnings, incReq.FundingSource)

	// Idempotent replay with conflicting provenance is rejected.
	_, err = CreateAdtyStaffBalanceRequestWithProvenance(&AdtyStaffBalanceRequest{
		SiteId:         11,
		StaffUserId:    21,
		CustomerUserId: customer.Id,
		Direction:      AdtyStaffBalanceIncrease,
		AmountUSD:      decimal.NewFromInt(3),
		Reason:         "SLA outage compensation for ticket TICKET-9001",
		BusinessRef:    "TICKET-OTHER",
		FundingSource:  AdtyStaffFundingSourceEarnings,
		IdempotencyKey: &incKey,
	})
	require.ErrorIs(t, err, ErrAdtyIdempotencyConflict)

	// 3. Site owner approves the increase request; request and ledger retain full provenance.
	var approvedInc *AdtyStaffBalanceRequest
	var incLedger *AdtyBalanceLedger
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var txErr error
		approvedInc, incLedger, txErr = ApproveAdtyStaffBalanceRequestWithProvenanceTx(
			tx, incReq.Id, 99, "owner", AdtyStaffFundingSourceEarnings, "verified ticket logs",
		)
		return txErr
	}))
	require.Equal(t, AdtyStaffBalanceApproved, approvedInc.Status)
	require.EqualValues(t, 2_000_000, approvedInc.BeforeQuota)
	require.EqualValues(t, 3_500_000, approvedInc.AfterQuota)
	require.Equal(t, incLedger.Id, approvedInc.LedgerId)
	require.Contains(t, incLedger.Reason, "SLA outage compensation for ticket TICKET-9001")
	require.Contains(t, incLedger.Reason, "ref=TICKET-9001")
	require.Contains(t, incLedger.Reason, "source=site_earnings")
	require.Contains(t, incLedger.Reason, "review=verified ticket logs")
	require.Contains(t, incLedger.RefId, "TICKET-9001")

	// 4. Create and approve a decrease request with recovery source targeting ORD-STAFF-1.
	decKey := "staff-req-dec-2"
	decReq, err := CreateAdtyStaffBalanceRequestWithProvenance(&AdtyStaffBalanceRequest{
		SiteId:         11,
		StaffUserId:    21,
		CustomerUserId: customer.Id,
		Direction:      AdtyStaffBalanceDecrease,
		AmountUSD:      decimal.NewFromInt(1), // 500,000 quota
		Reason:         "reclaim duplicate credit on ORD-STAFF-1",
		OrderRef:       "ORD-STAFF-1",
		RecoverySource: AdtyStaffRecoverySourcePrincipalBatch,
		BatchKind:      AdtyFundBatchKindPrincipal,
		BatchSource:    "ORD-STAFF-1",
		IdempotencyKey: &decKey,
	})
	require.NoError(t, err)
	require.Equal(t, "ORD-STAFF-1", decReq.BusinessRef)
	require.Equal(t, AdtyStaffRecoverySourcePrincipalBatch, decReq.FundingSource)

	var approvedDec *AdtyStaffBalanceRequest
	var decLedger *AdtyBalanceLedger
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var txErr error
		approvedDec, decLedger, txErr = ApproveAdtyStaffBalanceRequestWithProvenanceTx(
			tx, decReq.Id, 99, "owner", "", "approved duplicate reclaim",
		)
		return txErr
	}))
	require.Equal(t, AdtyStaffBalanceApproved, approvedDec.Status)
	require.EqualValues(t, 3_500_000, approvedDec.BeforeQuota)
	require.EqualValues(t, 3_000_000, approvedDec.AfterQuota)
	require.Contains(t, decLedger.Reason, "reclaim duplicate credit on ORD-STAFF-1")
	require.Contains(t, decLedger.Reason, "source=principal_batch")

	var origBatch AdtyFundBatch
	require.NoError(t, db.Where("user_id = ? AND source = ?", customer.Id, "ORD-STAFF-1").First(&origBatch).Error)
	require.EqualValues(t, 1_500_000, origBatch.Remaining, "targeted principal batch must be reclaimed by 500,000")
}
