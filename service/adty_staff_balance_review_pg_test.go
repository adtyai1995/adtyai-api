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

// 审核扣减必须用**逻辑余额**去扣批次，不是 raw quota。
//
// 这条走的是审核入口（`ReviewAdtyStaffBalanceRequest` → `applyApprovedStaffBalanceTx`），
// 不是 `AdjustUserQuota` 那条路。`TestAdjustUsesLogicalBoundsForTransientRawRows`
// 只覆盖后者，证明不了这一处——两条路各写一遍「在哪拿 before」。
//
// 缺陷形态：客户余额 quota=10 / pending=100（逻辑 110），审核扣 50。
// 准入判据用逻辑余额、扣批次用 raw quota=10 → 「批次不够」→ 整笔回滚，
// 管理员报 `fund batches cannot cover the debit: uncovered 40`，且没有绕法。
func TestStaffBalanceReviewDebitsBatchesFromLogicalBalancePG(t *testing.T) {
	db := announcementTestDB(t)
	pgtest.RecordServerVersion(t, db)

	const (
		rawQuota = 10
		pending  = 100
		deduct   = 50
	)
	owner := ticketTestUser(t, db, "review-owner", 1)
	site := model.AdtySite{Subdomain: "review-site", RootDomain: "example.com", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(&model.AdtySite{}).Where("id = ?", site.Id).Update("owner_user_id", owner.Id).Error)

	customer := ticketTestUser(t, db, "review-customer", 1)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", customer.Id).Updates(map[string]any{
		"quota": rawQuota, "quota_pending": pending, "site_id": site.Id,
	}).Error)

	// 新规则要求显式来源批次，仍保留 raw=10、pending=100 的活余额边界。
	trade := financialOriginalForTest(t, db, customer.Id)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.CreateAdtyFundBatchesWithSiteTx(tx, customer.Id, model.AdtyFundBatchSiteID(site.Id), "review-legacy", 110, 0)
	}))

	// AmountUSD 换回 quota：delta 必须正好是 50。
	amount := decimal.NewFromInt(int64(deduct)).Div(common.QuotaPerUnitDecimal())
	request := model.AdtyStaffBalanceRequest{
		SiteId: site.Id, StaffUserId: owner.Id, CustomerUserId: customer.Id,
		Direction: model.AdtyStaffBalanceDecrease, AmountUSD: amount,
		Status: model.AdtyStaffBalancePending,
		Reason: "correct original payment", OrderRef: trade, FundingSource: "specified_batch", BatchKind: model.AdtyFundBatchKindPrincipal, BatchSource: "review-legacy",
	}
	require.NoError(t, db.Create(&request).Error)

	require.NoError(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", request.Id, true),
		"钱够就必须能扣；这里报 fund batches cannot cover the debit 就是本单的缺陷")

	var after model.User
	require.NoError(t, db.First(&after, customer.Id).Error)
	assert.EqualValues(t, rawQuota-deduct, after.Quota, "raw quota 被扣到 -40")
	assert.EqualValues(t, pending, after.QuotaPending, "pending 不动")

	logical, err := model.GetUserQuota(customer.Id, false)
	require.NoError(t, err)
	assert.EqualValues(t, rawQuota+pending-deduct, logical, "逻辑余额 60")

	var lots []model.AdtyFundBatch
	require.NoError(t, db.Where("user_id = ? AND source = ?", customer.Id, "review-legacy").Find(&lots).Error)
	require.Len(t, lots, 1, "仅扣指定 legacy 批次")
	assert.EqualValues(t, 110, lots[0].Original, "指定批次原额110，原单批次不变")
	assert.EqualValues(t, 60, lots[0].Remaining, "110 - 50")

	var reviewed model.AdtyStaffBalanceRequest
	require.NoError(t, db.First(&reviewed, request.Id).Error)
	assert.Equal(t, model.AdtyStaffBalanceApproved, reviewed.Status)
}

// 反向：raw quota 不够但逻辑余额够的账户，**不能**因为「raw 是 0」被拒。
// 这是同一条缺陷的另一副面孔：raw=0 时 AdtyDebitUserQuotaTx 会返回 before>0、
// debited 正常，但传错 before 会错误拒绝显式指定、涵盖 pending 的来源批次。
func TestStaffBalanceReviewWorksWhenRawQuotaIsExhaustedPG(t *testing.T) {
	db := announcementTestDB(t)
	pgtest.RecordServerVersion(t, db)

	owner := ticketTestUser(t, db, "review2-owner", 1)
	site := model.AdtySite{Subdomain: "review2-site", RootDomain: "example.com", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(&model.AdtySite{}).Where("id = ?", site.Id).Update("owner_user_id", owner.Id).Error)

	customer := ticketTestUser(t, db, "review2-customer", 1)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", customer.Id).Updates(map[string]any{
		"quota": 0, "quota_pending": 80, "site_id": site.Id,
	}).Error)
	// 显式来源批次涵盖 pending，不能因 raw=0 拒绝合法扣减。
	trade := financialOriginalForTest(t, db, customer.Id)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.CreateAdtyFundBatchesWithSiteTx(tx, customer.Id, model.AdtyFundBatchSiteID(site.Id), "review-legacy", 80, 0)
	}))
	amount := decimal.NewFromInt(30).Div(common.QuotaPerUnitDecimal())
	request := model.AdtyStaffBalanceRequest{
		SiteId: site.Id, StaffUserId: owner.Id, CustomerUserId: customer.Id,
		Direction: model.AdtyStaffBalanceDecrease, AmountUSD: amount,
		Status: model.AdtyStaffBalancePending,
		Reason: "correct original payment", OrderRef: trade, FundingSource: "specified_batch", BatchKind: model.AdtyFundBatchKindPrincipal, BatchSource: "review-legacy",
	}
	require.NoError(t, db.Create(&request).Error)

	require.NoError(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", request.Id, true))

	logical, err := model.GetUserQuota(customer.Id, false)
	require.NoError(t, err)
	assert.EqualValues(t, 50, logical, "80 - 30")

}
