package service

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func dropBillingHoldTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Migrator().DropTable(&model.AdtyBillingHold{}))
	require.False(t, db.Migrator().HasTable(&model.AdtyBillingHold{}))
}

// DEC034（G3.6）确认规则：同一付款域出现尚未解除的短差额后，即使老在途退款或充值
// 让余额重新变为正数，仍须暂停该付款域新的付费请求，直到核查/解除；同时免费请求与
// 老在途请求的结算/退还不得被新付费暂停错误阻断，也不得回滚已成功的结算扣款。
func TestShortfallHoldBlocksNextPaidRequestPerDEC034(t *testing.T) {
	db := prepareFloorDB(t)
	require.NoError(t, db.AutoMigrate(&model.AdtyBillingHold{}))
	site := floorSite(t, db, "hold")
	user, token := floorCustomer(t, db, site, "hold", 10_000)

	// In-flight request B reserves 2,000 before A produces a shortfall.
	ctxInflight, infoInflight, _ := floorRelay(t, user, token)
	infoInflight.RequestId = "hold-req-inflight"
	require.Nil(t, PreConsumeBilling(ctxInflight, 2_000, infoInflight))

	// Request A reserves 4,000 then settles for more than remaining balance -> open shortfall hold.
	ctx, info, quota := floorRelay(t, user, token)
	info.RequestId = "hold-req-1"
	require.Nil(t, PreConsumeBilling(ctx, 4_000, info))
	require.NoError(t, SettleBilling(ctx, info, quota))
	require.Positive(t, info.BillingShortfall, "这一笔确实产生了差额")

	var holds int64
	require.NoError(t, db.Model(&model.AdtyBillingHold{}).Where("user_id = ? AND status = ?",
		user.Id, model.AdtyBillingHoldOpen).Count(&holds).Error)
	require.EqualValues(t, 1, holds, "缺口必须被记下来")

	// Existing in-flight request B can still refund/settle cleanly even while A's hold is open.
	require.NoError(t, SettleBilling(ctxInflight, infoInflight, 0))
	require.Positive(t, floorBalance(t, user.Id), "in-flight refund restores positive balance")

	// Even with positive balance after in-flight refund or manual topup, DEC034 pauses NEW paid requests.
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).
		Update("quota", 50_000).Error)
	ctxPaid, infoPaid, _ := floorRelay(t, user, token)
	infoPaid.RequestId = "hold-req-blocked"
	blockedErr := PreConsumeBilling(ctxPaid, 1_000, infoPaid)
	require.NotNil(t, blockedErr, "open shortfall hold must pause new paid requests even when balance > 0")
	require.Equal(t, http.StatusForbidden, blockedErr.StatusCode)
	require.Equal(t, 50_000, floorBalance(t, user.Id), "blocked request must not debit wallet")

	// Free requests must still be admitted cleanly during shortfall pause.
	ctxFree, infoFree, _ := floorRelay(t, user, token)
	infoFree.RequestId = "hold-req-free"
	require.Nil(t, FreezeRequestBillingPayer(ctxFree, infoFree))

	// Once the hold is resolved, new paid requests succeed again.
	require.NoError(t, db.Model(&model.AdtyBillingHold{}).Where("user_id = ?", user.Id).Update("status", "resolved").Error)
	ctxAfter, infoAfter, _ := floorRelay(t, user, token)
	infoAfter.RequestId = "hold-req-after-resolve"
	require.Nil(t, PreConsumeBilling(ctxAfter, 1_000, infoAfter))
}

func TestSettledShortfallBackfillsMissingHoldOnRecoveryAndBlocksPaidRequests(t *testing.T) {
	db := prepareFloorDB(t)
	require.NoError(t, db.AutoMigrate(&model.AdtyBillingHold{}))
	user, token := floorCustomer(t, db, nil, "hold-backfill", 10)
	ctx, info, _ := floorRelay(t, user, token)
	info.RequestId = "hold-backfill-1"
	require.Nil(t, PreConsumeBilling(ctx, 10, info))
	dropBillingHoldTable(t, db)

	err := SettleBilling(ctx, info, 225)
	require.ErrorIs(t, err, model.ErrAdtyBillingHoldTableMissing)

	// Re-topup user before hold table is restored: settled operation shortfall must still pause new paid requests.
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", 50_000).Error)
	ctxBlocked, infoBlocked, _ := floorRelay(t, user, token)
	infoBlocked.RequestId = "hold-backfill-blocked"
	blocked := PreConsumeBilling(ctxBlocked, 1_000, infoBlocked)
	require.NotNil(t, blocked, "settled shortfall must pause new paid requests even if hold row projection initially failed")
	require.Equal(t, http.StatusForbidden, blocked.StatusCode)

	// Restore table and run recovery -> missing open hold projection is repaired automatically.
	require.NoError(t, db.AutoMigrate(&model.AdtyBillingHold{}))
	require.NoError(t, RecoverBillingOperations(context.Background(), 100))
	var holds []model.AdtyBillingHold
	require.NoError(t, db.Where("user_id = ? AND request_id = ? AND status = ?",
		user.Id, "hold-backfill-1", model.AdtyBillingHoldOpen).Find(&holds).Error)
	require.Len(t, holds, 1)
	require.EqualValues(t, 215, holds[0].ShortfallQuota)
}

func TestAdtyNoteBillingShortfallReturnsHoldWriteError(t *testing.T) {
	db := prepareFloorDB(t)
	dropBillingHoldTable(t, db)
	user, token := floorCustomer(t, db, nil, "hold-note-fail", 10)
	ctx, info, _ := floorRelay(t, user, token)
	info.RequestId = "hold-note-fail"
	info.BillingShortfall = 15

	err := adtyNoteBillingShortfall(ctx, info, 0)
	require.ErrorIs(t, err, model.ErrAdtyBillingHoldTableMissing)
	require.Equal(t, 15, ctx.GetInt(AdtyBillingShortfallContextKey))
}

func TestSettleBillingReturnsHoldWriteErrorAfterDebit(t *testing.T) {
	db := prepareFloorDB(t)
	user, token := floorCustomer(t, db, nil, "hold-settle-fail", 10)
	ctx, info, _ := floorRelay(t, user, token)
	info.RequestId = "hold-settle-fail"
	require.Nil(t, PreConsumeBilling(ctx, 10, info))
	dropBillingHoldTable(t, db)

	err := SettleBilling(ctx, info, 225)
	require.ErrorIs(t, err, model.ErrAdtyBillingHoldTableMissing)
	require.Equal(t, 0, floorBalance(t, user.Id), "wallet debit already succeeded and must not be rolled back")
	require.Equal(t, 215, info.BillingShortfall)
	require.False(t, unsettledBilling(info), "settlement stays applied after a hold-write failure")
}

func TestChargeViolationFeeIfNeededReturnsHoldWriteErrorAfterDebit(t *testing.T) {
	db := prepareFloorDB(t)
	savedGrok := *model_setting.GetGrokSettings()
	t.Cleanup(func() { *model_setting.GetGrokSettings() = savedGrok })
	model_setting.GetGrokSettings().ViolationDeductionEnabled = true
	model_setting.GetGrokSettings().ViolationDeductionAmount = 0.04

	user, token := floorCustomer(t, db, nil, "hold-violation-fail", 1_000)
	ctx, info, _ := floorRelay(t, user, token)
	info.RequestId = "hold-violation-fail"
	dropBillingHoldTable(t, db)

	charged, err := ChargeViolationFeeIfNeeded(ctx, info, types.NewError(fmt.Errorf("blocked"), types.ErrorCodeViolationFeeGrokCSAM))
	require.True(t, charged, "the fee debit already succeeded")
	require.ErrorIs(t, err, model.ErrAdtyBillingHoldTableMissing)
	require.Equal(t, 0, floorBalance(t, user.Id), "wallet debit already succeeded and must not be rolled back")
	require.Equal(t, 19_000, info.BillingShortfall)
}
