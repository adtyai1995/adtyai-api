package service

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const adtyDurableBillingContextKey = "adty_durable_billing"
const adtyBillingOperationContextKey = "adty_billing_operation_key"

var activeBillingReservations sync.Map
var billingRecoveryOnce sync.Once
var billingProcessLeaseOwner = common.NewRequestId()

// AttachTaskBillingReservation connects the pre-upstream reservation to the
// durable task barrier. InsertWithContext commits the task and settlement
// intent together, so death before SettleBilling cannot strand the charge.
func AttachTaskBillingReservation(info *relaycommon.RelayInfo, task *model.Task) {
	if info == nil || task == nil {
		return
	}
	if session, ok := info.Billing.(*BillingSession); ok && session.operationKey != "" {
		task.PrivateData.BillingOperationKey = session.operationKey
		quota := session.tokenConsumed
		task.PrivateData.TokenConsumedQuota = &quota
	}
}

func projectBillingEarnings(key string) {
	if err := projectAdtySiteEarningEvent(context.Background(), key); err != nil {
		common.SysError(common.LogText("site earning projection remains pending: %s", err.Error()))
	}
}

func projectAdtySiteEarningEvent(ctx context.Context, key string) error {
	return model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []model.AdtySiteEarningEvent
		if err := model.AdtyLockForUpdate(tx).Where("operation_key = ?", key).Limit(1).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 || rows[0].Projected || rows[0].ReviewStatus == model.AdtyEarningNeedsReview {
			return nil
		}
		event := rows[0]
		return projectAdtySiteEarningEventTx(tx, &event)
	})
}

func projectAdtySiteEarningEventTx(tx *gorm.DB, event *model.AdtySiteEarningEvent) error {
	if event.Projected || event.ReviewStatus == model.AdtyEarningNeedsReview {
		return nil
	}
	revenue, platform, earning, err := adtyDurableEarningSplitTx(tx, event)
	if err != nil {
		return err
	}
	kind := model.AdtySiteEarningUsage
	if event.Quota < 0 {
		kind = model.AdtySiteEarningRefund
	}
	// Only an explicit finance disposition can exclude earnings. IP/device
	// heuristics remain metadata and cannot cancel a confirmed charge's spread.
	if event.ReviewStatus == model.AdtyEarningOriginalSkipped {
		kind = model.AdtySiteEarningSkipped
	}
	if err := upsertAdtySiteEarningTx(tx, event.Day+"+op", adtySiteEarningEntry{
		SiteId: event.SiteId, SiteOwnerUserId: event.SiteOwnerUserId, UserId: event.UserId, OperationKey: event.OperationKey,
		Kind: kind, Reason: event.RiskFlag, Quota: int64(event.Quota), Revenue: revenue, Platform: platform, Earning: earning,
	}); err != nil {
		return err
	}
	if err := recordDurableAdtySiteEarningLineTx(tx, event, revenue, platform, earning); err != nil {
		return err
	}
	return tx.Model(&model.AdtySiteEarningEvent{}).Where("operation_key = ?", event.OperationKey).Update("projected", true).Error
}

func adtyDurableEarningSplitTx(tx *gorm.DB, event *model.AdtySiteEarningEvent) (revenue, platform, earning decimal.Decimal, err error) {
	var operations []model.BillingOperation
	if err = tx.Where("operation_key = ? AND user_id = ? AND site_id = ?", event.OperationKey, event.UserId, event.SiteId).Limit(1).Find(&operations).Error; err != nil {
		return
	}
	if len(operations) > 0 {
		if snap := operations[0].RequestPriceSnapshot(); snap != nil {
			group, groupOK := model.ParseSnapshotDecimal(snap.GroupRatio)
			cost, costOK := model.ParseSnapshotDecimal(snap.SiteCostFactor)
			billed, billedOK := snap.BilledGroupRatioDecimal()
			if !groupOK || !costOK || !billedOK || !group.IsPositive() || !cost.IsPositive() || !billed.IsPositive() {
				err = model.ErrBillingOperationConflict
				return
			}
			revenue = decimal.NewFromInt(int64(event.Quota)).Div(common.QuotaPerUnitDecimal()).Round(8)
			platform = decimal.NewFromInt(int64(event.Quota)).Mul(group).Mul(cost).DivRound(billed.Mul(common.QuotaPerUnitDecimal()), 8)
			earning = revenue.Sub(platform)
			return
		}
	}
	revenue, platform, earning = AdtySiteEarningSplit(int64(event.Quota), decimal.NewFromFloat(event.Markup), decimal.NewFromFloat(event.CostFactor))
	return
}

func init() { model.AdtyApplySiteEarningTx = projectAdtySiteEarningEventTx }

// RecoverBillingOperations retries durable decisions and expires only unbound
// reservations whose heartbeat has stopped.
func RecoverBillingOperations(ctx context.Context, limit int) error {
	limit = min(max(limit, 1), 1000)
	cutoff := time.Now().Add(-2 * time.Minute).Unix()
	var abandoned []model.BillingOperation
	if err := model.DB.WithContext(ctx).Where("state = ? AND reference_id = 0 AND updated_at < ?", model.BillingReserved, cutoff).
		Order("updated_at").Limit(limit).Find(&abandoned).Error; err != nil {
		return err
	}
	for _, op := range abandoned {
		if err := model.QueueExpiredBillingRefund(op.OperationKey, cutoff); err != nil {
			return err
		}
	}
	// 只有到点的 op 才进队列：next_retry_at = 0 是存量行与新建行（立即可以试），
	// 失败过的按退避阶梯排到将来。排序也必须换到 next_retry_at 上——失败会整笔
	// 回滚，updated_at 不前进，只按它排的话永久失败的 op 永远占据队头。
	retryNow := time.Now().Unix()
	var pending []model.BillingOperation
	if err := model.DB.WithContext(ctx).Where("state IN ? AND (next_retry_at IS NULL OR next_retry_at <= ?)",
		[]string{model.BillingSettlePending, model.BillingRefundPending}, retryNow).
		Order("next_retry_at, updated_at, operation_key").Limit(limit).Find(&pending).Error; err != nil {
		return err
	}
	var first error
	for _, op := range pending {
		if _, _, err := model.ApplyBillingSettlement(op.OperationKey); err != nil {
			if first == nil {
				first = err
			}
			// 失败必须回执：不记 attempts 的话，同一条 op 每 15 秒重新排到队头，
			// 攒够 limit 条就把「其他所有待结算 op」全部饿死（ISS-R4-0094）。
			dead, retryErr := model.RecordBillingOperationRetryFailure(op.OperationKey, err, retryNow)
			if retryErr != nil {
				common.SysError(common.LogText("billing recovery: failed to record the retry budget for %s: %s", op.OperationKey, retryErr.Error()))
			}
			if dead {
				common.SysError(common.LogText("billing recovery: operation %s moved to the dead letter after %d attempts and needs manual settlement, last error: %s",
					op.OperationKey, model.BillingOperationMaxAttempts, err.Error()))
				// R6-11: Settle pending task/MJ flags when their billing op enters dead letter
				// so dead tasks do not permanently fill LIMIT 100 and starve healthy tasks.
				if op.Kind == "task" && op.ReferenceId > 0 {
					var deadTask model.Task
					if findErr := model.DB.WithContext(ctx).First(&deadTask, op.ReferenceId).Error; findErr == nil {
						if strings.TrimSpace(deadTask.PrivateData.BillingReviewReason) == "" {
							deadTask.PrivateData.BillingReviewReason = "billing operation moved to dead letter: " + op.OperationKey
						}
						_ = model.DB.WithContext(ctx).Model(&model.Task{}).Where("id = ?", deadTask.ID).
							Updates(map[string]any{"billing_pending": false, "private_data": deadTask.PrivateData}).Error
					}
				} else if op.Kind == "mj_refund" && op.ReferenceId > 0 {
					_ = model.DB.WithContext(ctx).Model(&model.Midjourney{}).Where("id = ?", op.ReferenceId).
						Update("billing_pending", false).Error
				} else if op.Kind == "mj_charge" && op.ReferenceId > 0 {
					// R7-08: Keep billing_charge_pending = true when mj_charge enters dead
					// letter so polling and automatic mj_refund remain blocked pending
					// manual review; queue starvation is already prevented by NOT EXISTS.
					_ = model.DB.WithContext(ctx).Model(&model.Midjourney{}).Where("id = ?", op.ReferenceId).
						Update("billing_pending", false).Error
				}
			}
		}
	}
	var tasks []model.Task
	if err := model.DB.WithContext(ctx).Where("billing_pending = ? AND billing_initial_pending = ?", true, false).
		Where("status IN ?", []string{model.TaskStatusSuccess, model.TaskStatusFailure}).
		Where("NOT EXISTS (SELECT 1 FROM billing_operations bo WHERE bo.state IN (?, ?, ?) AND bo.kind = ? AND bo.reference_id = tasks.id)",
			model.BillingDead, model.BillingSettlePending, model.BillingRefundPending, "task").
		Order("id").Limit(limit).Find(&tasks).Error; err != nil {
		return err
	}
	for i := range tasks {
		task := &tasks[i]
		if task.Status == model.TaskStatusFailure {
			RefundTaskQuota(ctx, task, task.FailReason)
			continue
		}
		if GetTaskAdaptorFunc == nil && GetTaskAdaptorForTaskFunc == nil {
			flagTaskBillingManualReview(ctx, task, "no task adaptor factory is registered for platform "+string(task.Platform))
			continue
		}
		adaptor := ResolveTaskPollingAdaptor(task, task.Platform)
		if adaptor == nil {
			flagTaskBillingManualReview(ctx, task, "no task adaptor for platform "+string(task.Platform))
			continue
		}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelId: task.ChannelId,
			},
		}
		if ch, chErr := model.CacheGetChannel(task.ChannelId); chErr == nil && ch != nil {
			baseURL := ch.GetBaseURL()
			if baseURL == "" {
				baseURL = constant.GetChannelBaseURL(ch.Type)
			}
			info.ChannelMeta.ChannelType = ch.Type
			info.ChannelMeta.ChannelBaseUrl = baseURL
			info.ChannelMeta.ChannelSetting = ch.GetSetting()
			info.ChannelMeta.HeadersOverride = ch.GetHeaderOverride()
			info.ApiKey = ch.Key
		}
		if task.PrivateData.Key != "" {
			info.ApiKey = task.PrivateData.Key
		}
		adaptor.Init(info)
		result := task.PrivateData.CompletionBilling
		if result == nil {
			result = &relaycommon.TaskInfo{Status: string(task.Status)}
		}
		finalizeTerminalTask(ctx, adaptor, task, result)
	}
	var mjs []model.Midjourney
	if err := model.DB.WithContext(ctx).Where("(billing_pending = ? OR billing_charge_pending = ?)", true, true).
		Where("(fail_reason IS NULL OR fail_reason NOT LIKE ?)", "billing_conflict:%").
		Where("NOT EXISTS (SELECT 1 FROM billing_operations bo WHERE bo.state IN (?, ?, ?) AND bo.kind IN (?, ?) AND bo.reference_id = midjourneys.id)",
			model.BillingDead, model.BillingSettlePending, model.BillingRefundPending, "mj_charge", "mj_refund").
		Order("id").Limit(limit).Find(&mjs).Error; err != nil {
		return err
	}
	for i := range mjs {
		task := &mjs[i]
		if task.Status == "FAILURE" {
			RefundMidjourneyQuota(ctx, task, task.FailReason)
		} else if task.BillingChargePending {
			_, err := settleDurableMidjourney(task)
			if err != nil && first == nil {
				first = err
			}
		} else {
			if err := model.DB.Model(task).Update("billing_pending", false).Error; err != nil && first == nil {
				first = err
			}
		}
	}
	var events []model.AdtySiteEarningEvent
	if err := model.DB.WithContext(ctx).Where("projected = ? AND (review_status <> ? OR review_status IS NULL)", false, model.AdtyEarningNeedsReview).Order("created_at, operation_key").Limit(limit).Find(&events).Error; err != nil {
		return err
	}
	for _, event := range events {
		if err := projectAdtySiteEarningEvent(ctx, event.OperationKey); err != nil && first == nil {
			first = err
		}
	}
	if err := model.BackfillMissingAdtyBillingHolds(ctx, limit); err != nil && first == nil {
		first = err
	}
	return first
}

// flagTaskBillingManualReview 收口「恢复链救不了」的任务：一条 SysError 留痕，
// 行上写复核原因并把 billing_pending 关掉。
//
// 为什么不能只是 continue：这两个分支既不推进也不写日志，任务每 15 秒被同一个
// 地方跳过（`billing_pending = true AND billing_initial_pending = false`），预扣
// 额度永久停在中间态且运营完全看不到（ISS-R4-0095）。
//
// 为什么不顺手退或补扣：没有 adaptor 就重算不出实际用量，退多少、补多少都是猜。
// 预扣额度保持原样，由 BillingReviewReason 交给人工判断——恢复链在这里只负责
// 「让这行停下来并被看见」，不替人做财务决定。
func flagTaskBillingManualReview(ctx context.Context, task *model.Task, reason string) {
	if task == nil {
		return
	}
	common.SysError(common.LogText("task billing recovery: %s, task %s platform %s user %d keeps its reserved charge and is flagged for manual review",
		reason, task.TaskID, task.Platform, task.UserId))
	task.PrivateData.BillingReviewReason = reason
	err := model.DB.WithContext(ctx).Model(&model.Task{}).Where("id = ? AND billing_pending = ?", task.ID, true).
		Updates(map[string]any{"billing_pending": false, "private_data": task.PrivateData}).Error
	if err != nil {
		// 写不进去就还是原来的死循环，所以这一条必须报出来，不能只返回。
		common.SysError(common.LogText("task billing recovery: failed to flag task %s for manual review: %s", task.TaskID, err.Error()))
		return
	}
	task.BillingPending = false
}

// StartBillingRecovery runs on every serving node, including non-master nodes,
// so long requests renew their reservations. Process death stops heartbeats;
// any node can then atomically recover the expired reservation.
func StartBillingRecovery() {
	billingRecoveryOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				keys := []string{}
				activeBillingReservations.Range(func(key, _ any) bool { keys = append(keys, key.(string)); return true })
				if len(keys) > 0 {
					if err := model.DB.Model(&model.BillingOperation{}).Where("operation_key IN ? AND state = ? AND lease_owner = ?", keys, model.BillingReserved, billingProcessLeaseOwner).
						UpdateColumn("updated_at", time.Now().Unix()).Error; err != nil {
						common.SysError(common.LogText("billing reservation heartbeat failed: %s", err.Error()))
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if err := RecoverBillingOperations(ctx, 100); err != nil {
					common.SysError(common.LogText("billing recovery remains pending: %s", err.Error()))
				}
				cancel()
				<-ticker.C
			}
		}()
	})
}
