package model

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

// 调额必须挂真实订单。调用方不能自带审批号，也不能指定另一个审批人或执行人；
// 审批引用由产品生成，权限主体始终是已认证操作者。主站加额只用 platform_correction，
// 且批次字段必须为空；减额必须点名一笔已经存在的批次。
func TestAdjustUserQuotaWithProvenance(t *testing.T) {
	db := adtyTestDBWithSchema(t)
	customer := &User{
		Username: "adj-provenance-customer",
		Password: "x",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(customer).Error)

	rootID, prov := adjustmentTestRequest(t, db, customer.Id, "add", "")
	require.Positive(t, rootID)
	require.NotEqual(t, customer.Id, rootID)
	require.Empty(t, prov.ApprovalRef)
	require.Nil(t, prov.SiteID)
	require.Zero(t, prov.ApproverID)
	require.Zero(t, prov.ExecutorID)
	require.Equal(t, AdtyAdjustSourcePlatform, prov.FundingSource)
	require.Empty(t, prov.BatchKind)
	require.Empty(t, prov.BatchSource)

	var order AdtyTopupOrder
	require.NoError(t, db.Where("trade_no = ? AND user_id = ?", prov.OrderRef, customer.Id).First(&order).Error)
	require.Equal(t, 0, order.SiteId)
	require.EqualValues(t, 0, order.TeamId)
	require.False(t, order.IsOwnerSelfUse())
	var topup TopUp
	require.NoError(t, db.Where("trade_no = ? AND user_id = ?", prov.OrderRef, customer.Id).First(&topup).Error)
	require.True(t, topup.Status == common.TopUpStatusSuccess || topup.Status == TopUpStatusRefunded, "status=%s", topup.Status)

	// 夹具建单时会临时改钱包。这里装回本测试要核对的 raw/pending。
	require.NoError(t, db.Model(&User{}).Where("id = ?", customer.Id).Updates(map[string]any{
		"quota": 1000, "quota_pending": -200,
	}).Error)

	const (
		incKey    = "adj-prov-real-inc"
		incReason = "compensate under-credited original order"
		incValue  = 500
	)
	input := AdtyQuotaAdjustInput{
		UserID:         customer.Id,
		OperatorID:     rootID,
		OperatorRole:   common.RoleRootUser,
		Mode:           "add",
		Value:          incValue,
		Reason:         incReason,
		IdempotencyKey: incKey,
		Provenance:     prov,
	}
	res, err := AdjustUserQuotaWithProvenance(input)
	require.NoError(t, err)
	require.False(t, res.Replayed)
	require.Equal(t, customer.Username, res.Username)
	require.Equal(t, AdtyAdjustDirectionIncrease, res.Direction)
	require.EqualValues(t, incValue, res.DeltaQuota)
	require.Equal(t, 1000, res.Before)
	require.Equal(t, 1500, res.After)
	require.EqualValues(t, 800, res.BalanceBefore, "logical balance before = 1000 + -200")
	require.EqualValues(t, 1300, res.BalanceAfter, "logical balance after = 1500 + -200")
	require.Equal(t, prov.OrderRef, res.OrderRef)
	require.Equal(t, 0, res.SiteID)
	require.Equal(t, rootID, res.ApproverID)
	require.Equal(t, rootID, res.ExecutorID)
	wantApproval := fmt.Sprintf("financial_adjustment:%d:approved:%d", res.AdjustmentID, rootID)
	require.Equal(t, wantApproval, res.ApprovalRef)
	require.Positive(t, res.LedgerID)
	require.Positive(t, res.AdjustmentID)

	var persisted AdtyFinancialAdjustment
	require.NoError(t, db.First(&persisted, res.AdjustmentID).Error)
	require.Equal(t, customer.Id, persisted.UserId)
	require.Equal(t, 0, persisted.SiteId)
	require.Equal(t, "add", persisted.Mode)
	require.Equal(t, AdtyAdjustDirectionIncrease, persisted.Direction)
	require.Equal(t, incValue, persisted.RequestedValue)
	require.EqualValues(t, incValue, persisted.DeltaQuota)
	require.EqualValues(t, 800, persisted.BalanceBefore)
	require.EqualValues(t, 1300, persisted.BalanceAfter)
	require.Equal(t, 1000, persisted.RawBefore)
	require.Equal(t, 1500, persisted.RawAfter)
	require.Equal(t, incReason, persisted.Reason)
	require.Equal(t, prov.OrderRef, persisted.OrderRef)
	require.Equal(t, AdtyAdjustSourcePlatform, persisted.FundingSource)
	require.Empty(t, persisted.BatchKind)
	require.Empty(t, persisted.BatchSource)
	require.Equal(t, wantApproval, persisted.ApprovalRef)
	require.Equal(t, rootID, persisted.ApproverId)
	require.Equal(t, rootID, persisted.ExecutorId)
	require.Equal(t, common.RoleRootUser, persisted.OperatorRole)
	require.Equal(t, res.LedgerID, persisted.LedgerId)
	require.Equal(t, incKey, persisted.IdempotencyKey)

	var entry AdtyBalanceLedger
	require.NoError(t, db.First(&entry, res.LedgerID).Error)
	require.Equal(t, customer.Id, entry.UserId)
	require.EqualValues(t, incValue, entry.DeltaQuota)
	require.EqualValues(t, 800, entry.BalanceBefore)
	require.EqualValues(t, 1300, entry.BalanceAfter)
	require.Equal(t, AdtyLedgerTypeManualAdjust, entry.Type)
	require.Equal(t, "financial_adjustment", entry.RefType)
	require.Equal(t, strconv.FormatInt(res.AdjustmentID, 10), entry.RefId)
	require.Equal(t, rootID, entry.OperatorId)

	batchSource := adtyAdjustFundSource(incKey)
	var batch AdtyFundBatch
	require.NoError(t, db.Where("user_id = ? AND source = ? AND kind = ?", customer.Id, batchSource, AdtyFundBatchKindPrincipal).First(&batch).Error)
	require.NotNil(t, batch.SiteId)
	require.Equal(t, 0, *batch.SiteId)
	require.EqualValues(t, incValue, batch.Original)
	require.EqualValues(t, incValue, batch.Remaining)

	replay, err := AdjustUserQuotaWithProvenance(input)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, res.AdjustmentID, replay.AdjustmentID)
	require.Equal(t, res.LedgerID, replay.LedgerID)
	require.Equal(t, wantApproval, replay.ApprovalRef)
	require.Equal(t, rootID, replay.ApproverID)
	require.Equal(t, rootID, replay.ExecutorID)
	require.Equal(t, prov.OrderRef, replay.OrderRef)
	require.Equal(t, 1000, replay.Before)
	require.Equal(t, 1500, replay.After)
	require.EqualValues(t, 800, replay.BalanceBefore)
	require.EqualValues(t, 1300, replay.BalanceAfter)

	var stored User
	require.NoError(t, db.First(&stored, customer.Id).Error)
	require.Equal(t, 1500, stored.Quota)
	require.Equal(t, -200, stored.QuotaPending)
	require.NoError(t, db.Where("user_id = ? AND source = ? AND kind = ?", customer.Id, batchSource, AdtyFundBatchKindPrincipal).First(&batch).Error)
	require.EqualValues(t, incValue, batch.Remaining)
	var adjustments int64
	require.NoError(t, db.Model(&AdtyFinancialAdjustment{}).Where("idempotency_key = ?", incKey).Count(&adjustments).Error)
	require.EqualValues(t, 1, adjustments)

	conflict := input
	conflict.Provenance.OrderRef = prov.OrderRef + "-other"
	_, err = AdjustUserQuotaWithProvenance(conflict)
	require.ErrorIs(t, err, ErrAdtyIdempotencyConflict)
	require.NoError(t, db.Model(&AdtyFinancialAdjustment{}).Where("user_id = ?", customer.Id).Count(&adjustments).Error)
	require.EqualValues(t, 1, adjustments)
	require.NoError(t, db.First(&stored, customer.Id).Error)
	require.Equal(t, 1500, stored.Quota)

	rootDec, decProv := adjustmentTestRequest(t, db, customer.Id, "subtract", batchSource)
	require.Equal(t, rootID, rootDec)
	require.Equal(t, prov.OrderRef, decProv.OrderRef)
	require.Equal(t, AdtyAdjustSourceBatch, decProv.FundingSource)
	require.Equal(t, AdtyFundBatchKindPrincipal, decProv.BatchKind)
	require.Equal(t, batchSource, decProv.BatchSource)
	require.Empty(t, decProv.ApprovalRef)
	require.Nil(t, decProv.SiteID)
	require.Zero(t, decProv.ApproverID)
	require.Zero(t, decProv.ExecutorID)

	const (
		decKey    = "adj-prov-real-dec"
		decReason = "reclaim over-credited original order"
		decValue  = 300
	)
	decRes, err := AdjustUserQuotaWithLedger(customer.Id, rootID, common.RoleRootUser, "subtract", decValue, decReason, decKey, decProv)
	require.NoError(t, err)
	require.False(t, decRes.Replayed)
	require.Equal(t, AdtyAdjustDirectionDecrease, decRes.Direction)
	require.EqualValues(t, -decValue, decRes.DeltaQuota)
	require.Equal(t, 1500, decRes.Before)
	require.Equal(t, 1200, decRes.After)
	require.EqualValues(t, 1300, decRes.BalanceBefore)
	require.EqualValues(t, 1000, decRes.BalanceAfter)
	require.Equal(t, prov.OrderRef, decRes.OrderRef)
	require.Equal(t, 0, decRes.SiteID)
	require.Equal(t, rootID, decRes.ApproverID)
	require.Equal(t, rootID, decRes.ExecutorID)
	require.Equal(t, fmt.Sprintf("financial_adjustment:%d:approved:%d", decRes.AdjustmentID, rootID), decRes.ApprovalRef)
	require.NotEqual(t, res.AdjustmentID, decRes.AdjustmentID)

	var decreased AdtyFinancialAdjustment
	require.NoError(t, db.First(&decreased, decRes.AdjustmentID).Error)
	require.Equal(t, customer.Id, decreased.UserId)
	require.Equal(t, 0, decreased.SiteId)
	require.Equal(t, "subtract", decreased.Mode)
	require.Equal(t, AdtyAdjustDirectionDecrease, decreased.Direction)
	require.Equal(t, decValue, decreased.RequestedValue)
	require.EqualValues(t, -decValue, decreased.DeltaQuota)
	require.Equal(t, decReason, decreased.Reason)
	require.Equal(t, prov.OrderRef, decreased.OrderRef)
	require.Equal(t, AdtyAdjustSourceBatch, decreased.FundingSource)
	require.Equal(t, AdtyFundBatchKindPrincipal, decreased.BatchKind)
	require.Equal(t, batchSource, decreased.BatchSource)
	require.Equal(t, rootID, decreased.ApproverId)
	require.Equal(t, rootID, decreased.ExecutorId)
	require.Equal(t, decRes.ApprovalRef, decreased.ApprovalRef)
	require.Equal(t, decRes.LedgerID, decreased.LedgerId)

	require.NoError(t, db.Where("user_id = ? AND source = ? AND kind = ?", customer.Id, batchSource, AdtyFundBatchKindPrincipal).First(&batch).Error)
	require.EqualValues(t, incValue-decValue, batch.Remaining)
	require.NoError(t, db.First(&stored, customer.Id).Error)
	require.Equal(t, 1200, stored.Quota)
	require.Equal(t, -200, stored.QuotaPending)
	require.NoError(t, db.Model(&AdtyFinancialAdjustment{}).Where("user_id = ?", customer.Id).Count(&adjustments).Error)
	require.EqualValues(t, 2, adjustments)
}
