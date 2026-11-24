package service

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 站长收益记账（任务 11.5）。
// ISS-R1-0009 之后实扣额度 q = P × d × k（供货价 × (1+加价比例)）。
// A.10：客户付的钱 = q ÷ QuotaPerUnit；平台供货成本 = P × d = q ÷ k；
// 站长收益（价差）= 客户付 − 供货成本 = P × d × (k − 1)。
// 三项都用 decimal 算；站长收益用「客户付 − 平台收」得出，两项相加正好等于客户付的钱。
// 记账只累加当天仍待结算的槽位，钩子失败只记日志，不影响请求。

// adtySiteEarningDayFn 是记账用的日期（优先按平台业务时区），测试可以替换。
var adtySiteEarningDayFn = func() string {
	if model.DB != nil && model.DB.Migrator().HasTable(&model.Option{}) {
		if loc, _, err := loadAdtyBusinessLocation(); err == nil && loc != nil {
			return time.Now().In(loc).Format("2006-01-02")
		}
	}
	return time.Now().In(time.FixedZone("CST", 8*60*60)).Format("2006-01-02")
}

var adtyImmediateEarningSeq atomic.Uint64

func init() {
	adtyImmediateEarningSeq.Store(uint64(time.Now().UnixNano()))
}

// adtyEarningFactorsUsable reports whether a stored k/d snapshot can be used
// to book A.10 books. Markup k may be below 1 and the cost factor d may be
// above 1; only non-positive or non-finite values are rejected.
func adtyEarningFactorsUsable(k, d float64) bool {
	return model.BillingEarningFactorsUsable(k, d)
}

// AdtySiteEarningSplit 按 A.10 拆一笔已扣费额度（美元，8 位小数）。
// 实扣 q = P×d×k，供货成本 = q/k，站长收益 = q − q/k。供货倍率 d 已含在 q 里，不再参与拆分。
// k 须为正；k<1 时收益可为负（售价低于供货成本）。
func AdtySiteEarningSplit(quota int64, k, d decimal.Decimal) (revenue, platform, earning decimal.Decimal) {
	return OfficialAdtySupplySplit(quota, k)
}

// OfficialAdtySupplySplit is the A.10 settlement: charged q = P×d×k, supply
// cost = P×d = q/k, site earning = P×d×(k-1) = q − q/k. The live hook uses
// the same formula via AdtySiteEarningSplit.
func OfficialAdtySupplySplit(quota int64, k decimal.Decimal) (revenue, platform, earning decimal.Decimal) {
	perUnit := common.QuotaPerUnitDecimal()
	revenue = decimal.NewFromInt(quota).Div(perUnit).Round(8)
	if !k.IsPositive() {
		return revenue, decimal.Zero, revenue
	}
	platform = decimal.NewFromInt(quota).Div(k).Div(perUnit).Round(8)
	earning = revenue.Sub(platform)
	return revenue, platform, earning
}

// adtySiteEarningEntry 是一笔要累加的收益。
type adtySiteEarningEntry struct {
	SiteId, UserId  int
	SiteOwnerUserId int
	OperationKey    string
	Kind, Reason    string
	Quota           int64
	Revenue         decimal.Decimal
	Platform        decimal.Decimal
	Earning         decimal.Decimal
	OccurredAt      int64
}

func adtyImmediateEarningIdemKey(siteID, ownerID int, eventOpKey string) string {
	eventOpKey = strings.TrimSpace(eventOpKey)
	if eventOpKey != "" {
		sum := sha256.Sum256([]byte(eventOpKey))
		return fmt.Sprintf("site-earning-op:%d:%x", siteID, sum[:16])
	}
	return fmt.Sprintf("site-earning-imm:%d:%d:%s", siteID, ownerID, common.NewRequestId())
}

// upsertAdtySiteEarningTx records confirmed site earnings and credits the site
// earnings wallet immediately for confirmed usage/refund spread.
func upsertAdtySiteEarningTx(tx *gorm.DB, day string, entry adtySiteEarningEntry) error {
	return tx.Transaction(func(tx *gorm.DB) error {
		ownerID, err := model.ResolveAdtySiteFinancialOwnerTx(tx, entry.SiteId, entry.SiteOwnerUserId)
		if err != nil {
			return err
		}
		entry.SiteOwnerUserId = ownerID
		now := time.Now().Unix()
		immediateCredit := entry.Kind == model.AdtySiteEarningUsage || entry.Kind == model.AdtySiteEarningRefund
		rowStatus := model.AdtySiteEarningPending
		settledAt := int64(0)
		if immediateCredit {
			rowStatus = model.AdtySiteEarningAvailable
			settledAt = now
			// R7-004: Pre-lock earnings wallet BEFORE AdtySiteEarningDaily so every
			// writer obeys canonical AdtySiteWallet(earnings) -> AdtySiteEarningDaily order.
			if !entry.Earning.IsZero() {
				if err := preLockAdtySiteWalletsTx(tx, entry.SiteId, model.AdtySiteWalletEarnings); err != nil {
					return err
				}
			}
		}
		targetSlot := ""
		for attempt := range 8 {
			slot := day
			if attempt > 0 {
				slot = fmt.Sprintf("%s+%d", day, attempt)
			}
			row := model.AdtySiteEarningDaily{
				SiteId: entry.SiteId, SiteOwnerUserId: ownerID, UserId: entry.UserId, Day: slot, Kind: entry.Kind, Reason: entry.Reason,
				Revenue: decimal.Zero, Platform: decimal.Zero, Earning: decimal.Zero,
				Status: rowStatus, SettledAt: settledAt, UpdatedAt: now,
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
			var before []model.AdtySiteEarningDaily
			if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND site_owner_user_id = ? AND user_id = ? AND day = ? AND kind = ? AND status = ?",
				entry.SiteId, ownerID, entry.UserId, slot, entry.Kind, rowStatus).Limit(1).Find(&before).Error; err != nil {
				return err
			}
			if len(before) == 0 {
				continue
			}
			if immediateCredit && !strings.Contains(day, "+op") {
				var count int64
				if err := tx.Model(&model.AdtySiteEarningFact{}).Where("daily_id = ?", before[0].Id).Count(&count).Error; err != nil {
					return err
				}
				if count == 0 && (!before[0].Revenue.IsZero() || !before[0].Platform.IsZero() || !before[0].Earning.IsZero()) {
					if err := tx.Create(&model.AdtySiteEarningFact{SiteId: entry.SiteId, DailyId: before[0].Id, OccurredAt: before[0].UpdatedAt,
						Revenue: before[0].Revenue, Platform: before[0].Platform, Earning: before[0].Earning}).Error; err != nil {
						return err
					}
				}
				occurredAt := entry.OccurredAt
				if occurredAt == 0 {
					occurredAt = now
				}
				if err := tx.Create(&model.AdtySiteEarningFact{SiteId: entry.SiteId, DailyId: before[0].Id, OccurredAt: occurredAt,
					Revenue: entry.Revenue, Platform: entry.Platform, Earning: entry.Earning}).Error; err != nil {
					return err
				}
			}
			updates := map[string]any{
				"quota":      gorm.Expr("quota + ?", entry.Quota),
				"revenue":    gorm.Expr("revenue + ?", entry.Revenue),
				"platform":   gorm.Expr("platform + ?", entry.Platform),
				"earning":    gorm.Expr("earning + ?", entry.Earning),
				"updated_at": now,
			}
			if settledAt > 0 {
				updates["settled_at"] = settledAt
			}
			if entry.Reason != "" {
				updates["reason"] = entry.Reason
			}
			result := tx.Model(&model.AdtySiteEarningDaily{}).
				Where("site_id = ? AND site_owner_user_id = ? AND user_id = ? AND day = ? AND kind = ? AND status = ?",
					entry.SiteId, ownerID, entry.UserId, slot, entry.Kind, rowStatus).
				Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected > 0 {
				targetSlot = slot
				break
			}
			var matched int64
			if err := tx.Model(&model.AdtySiteEarningDaily{}).
				Where("site_id = ? AND site_owner_user_id = ? AND user_id = ? AND day = ? AND kind = ? AND status = ?",
					entry.SiteId, ownerID, entry.UserId, slot, entry.Kind, rowStatus).
				Count(&matched).Error; err != nil {
				return err
			}
			if matched > 0 {
				targetSlot = slot
				break
			}
		}
		if targetSlot == "" {
			return errors.New("no open earning slot for today")
		}
		if immediateCredit {
			if !entry.Earning.IsZero() {
				idemKey := adtyImmediateEarningIdemKey(entry.SiteId, ownerID, entry.OperationKey)
				if _, err := applyAdtySiteWalletChangeTx(tx, entry.SiteId, model.AdtySiteWalletEarnings, entry.Earning, AdtySiteWalletChange{
					Type: "site_earning_settlement", RefType: "site_earning", RefID: adtyEarningDayOf(day),
					Reason: fmt.Sprintf("site earnings up to %s", adtyEarningDayOf(day)), IdempotencyKey: idemKey, AllowOverdraft: true,
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func upsertAdtySiteEarningLineTx(tx *gorm.DB, siteID, ownerID, userID int, modelName, group, requestID string, quota int64, k, d float64) error {
	if tx == nil || siteID <= 0 || userID <= 0 || quota == 0 || !adtyEarningFactorsUsable(k, d) {
		return nil
	}
	if !tx.Migrator().HasTable(&model.AdtySiteEarningLine{}) {
		return nil
	}
	modelName = strings.TrimSpace(modelName)
	group = strings.TrimSpace(group)
	requestID = strings.TrimSpace(requestID)
	if len(requestID) > 64 {
		sum := sha256.Sum256([]byte(requestID))
		requestID = fmt.Sprintf("op:%x", sum[:24])
	}
	if requestID != "" {
		var existing model.AdtySiteEarningLine
		res := tx.Where("site_id = ? AND site_owner_user_id = ? AND user_id = ? AND request_id = ?", siteID, ownerID, userID, requestID).Limit(1).Find(&existing)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			updates := map[string]any{}
			if modelName != "" && (existing.ModelName == "" || existing.ModelName == "unknown") {
				updates["model_name"] = modelName
			}
			if group != "" && (existing.Group == "" || existing.Group == "default") {
				updates["group"] = group
			}
			if len(updates) > 0 {
				return tx.Model(&existing).Updates(updates).Error
			}
			return nil
		}
	}
	if modelName == "" {
		modelName = "unknown"
	}
	if group == "" {
		group = "default"
	}
	decK := decimal.NewFromFloat(k).Round(6)
	decD := decimal.NewFromFloat(d).Round(6)
	revenue, platform, earning := AdtySiteEarningSplit(quota, decK, decD)
	line := model.AdtySiteEarningLine{SiteOwnerUserId: ownerID,
		SiteId:     siteID,
		UserId:     userID,
		ModelName:  modelName,
		Group:      group,
		RequestId:  requestID,
		Quota:      quota,
		Markup:     decK,
		CostFactor: decD,
		Revenue:    revenue,
		Platform:   platform,
		Earning:    earning,
		CreatedAt:  time.Now().Unix(),
	}
	return tx.Create(&line).Error
}

func recordDurableEventEarningLineTx(tx *gorm.DB, event *model.AdtySiteEarningEvent, entry adtySiteEarningEntry) error {
	if tx == nil || event == nil || strings.TrimSpace(event.OperationKey) == "" || event.Quota == 0 {
		return nil
	}
	if !tx.Migrator().HasTable(&model.AdtySiteEarningLine{}) {
		return nil
	}
	opKey := strings.TrimSpace(event.OperationKey)
	requestID := opKey
	var modelName, group string
	if tx.Migrator().HasTable(&model.BillingOperation{}) {
		var op model.BillingOperation
		if err := tx.Where("operation_key = ?", opKey).Limit(1).Find(&op).Error; err != nil {
			return err
		}
		if op.OperationKey != "" {
			if op.Kind == "request" || strings.HasPrefix(opKey, "request:") {
				subj := strings.TrimPrefix(opKey, "request:")
				if subj != "" {
					requestID = subj
				}
			}
			for _, rawJSON := range []string{op.PriceSnapshot, op.BillingContext} {
				if strings.TrimSpace(rawJSON) == "" {
					continue
				}
				var snap map[string]any
				if err := common.UnmarshalJsonStr(rawJSON, &snap); err == nil {
					if modelName == "" {
						for _, k := range []string{"model_name", "origin_model_name", "OriginModelName", "ModelName"} {
							if m, ok := snap[k].(string); ok && strings.TrimSpace(m) != "" {
								modelName = strings.TrimSpace(m)
								break
							}
						}
					}
					if group == "" {
						for _, k := range []string{"group", "using_group", "user_group", "Group", "UsingGroup"} {
							if g, ok := snap[k].(string); ok && strings.TrimSpace(g) != "" {
								group = strings.TrimSpace(g)
								break
							}
						}
					}
				}
			}
			if (op.Kind == "task" || strings.HasPrefix(opKey, "task:") || strings.HasPrefix(op.EarningRootKey, "task:")) && (modelName == "" || group == "") && tx.Migrator().HasTable(&model.Task{}) {
				var tasks []model.Task
				if err := tx.Where("user_id = ?", entry.UserId).Order("id DESC").Limit(20).Find(&tasks).Error; err == nil && len(tasks) > 0 {
					chosen := &tasks[0]
					for i := range tasks {
						bKey := strings.TrimSpace(tasks[i].PrivateData.BillingOperationKey)
						if bKey != "" && (bKey == opKey || bKey == op.EarningRootKey) {
							chosen = &tasks[i]
							break
						}
						if op.ReferenceId > 0 && tasks[i].ID == op.ReferenceId {
							chosen = &tasks[i]
							break
						}
					}
					if group == "" && strings.TrimSpace(chosen.Group) != "" {
						group = strings.TrimSpace(chosen.Group)
					}
					if modelName == "" {
						if chosen.PrivateData.BillingContext != nil && strings.TrimSpace(chosen.PrivateData.BillingContext.OriginModelName) != "" {
							modelName = strings.TrimSpace(chosen.PrivateData.BillingContext.OriginModelName)
						}
						if modelName == "" && strings.TrimSpace(chosen.Properties.OriginModelName) != "" {
							modelName = strings.TrimSpace(chosen.Properties.OriginModelName)
						}
					}
				}
			}
			if (op.Kind == "mj_charge" || op.Kind == "mj_refund" || strings.HasPrefix(opKey, "mj:")) && modelName == "" {
				modelName = "midjourney"
			}
		}
	}
	return upsertAdtySiteEarningLineTx(tx, entry.SiteId, event.SiteOwnerUserId, entry.UserId, modelName, group, requestID, int64(event.Quota), event.Markup, event.CostFactor)
}

// adtySiteCustomerRisk 取客户的风控状态（仅作审计与管理侧标记，不再自动取消站长消费价差）。
func adtySiteCustomerRisk(userId int) (flag string) {
	var rows []model.AdtySiteCustomer
	if err := model.AdtyLiveSiteCustomer(model.DB).Select("user_id", "risk_flag", "reviewed").Where("user_id = ?", userId).Limit(1).Find(&rows).Error; err != nil || len(rows) == 0 {
		return ""
	}
	if rows[0].Reviewed {
		return ""
	}
	return rows[0].RiskFlag
}

// AdtySiteEarningHook 给一笔消费或退款记站长收益：quota 是客户的扣费额度（退款传正数，kind 用 refund），
// k、d 是扣费当时的快照。客户不是分站客户或没有 k 时直接返回。
func AdtySiteEarningHook(siteId, userId int, quota int64, k, d float64, kind string, frozenOwnerIDs ...int) {
	if siteId <= 0 || userId <= 0 || quota == 0 || !adtyEarningFactorsUsable(k, d) {
		return
	}
	revenue, platform, earning := AdtySiteEarningSplit(quota, decimal.NewFromFloat(k), decimal.NewFromFloat(d))
	entry := adtySiteEarningEntry{SiteId: siteId, UserId: userId, Kind: kind, Quota: quota, Revenue: revenue, Platform: platform, Earning: earning}
	if kind == model.AdtySiteEarningRefund {
		entry.Quota, entry.Revenue, entry.Platform, entry.Earning = -quota, revenue.Neg(), platform.Neg(), earning.Neg()
	}
	if flag := adtySiteCustomerRisk(userId); flag != "" {
		entry.Reason = flag
	}
	var err error
	if len(frozenOwnerIDs) > 0 {
		entry.SiteOwnerUserId = frozenOwnerIDs[0]
		if entry.SiteOwnerUserId <= 0 {
			err = errors.New("site financial owner snapshot unavailable")
		}
	} else if kind == model.AdtySiteEarningUsage {
		entry.SiteOwnerUserId, err = model.CurrentAdtySiteFinancialOwnerTx(model.DB, siteId)
	}
	if err == nil {
		err = upsertAdtySiteEarningTx(model.DB, adtySiteEarningDayFn(), entry)
	}
	if err != nil {
		common.SysError(common.LogText("record site earning failed site=%d user=%d: %s", siteId, userId, err.Error()))
		if incErr := model.DB.Model(&model.AdtySite{}).Where("id = ?", siteId).
			UpdateColumn("earning_hook_failures", gorm.Expr("earning_hook_failures + 1")).Error; incErr != nil {
			common.SysError(common.LogText("count site earning failure site=%d: %s", siteId, incErr.Error()))
		}
	}
}

// adtyRecordConsumeEarning 是 RecordConsumeLog 的钩子：从 gin 上下文取 11.4 写进去的 k、d。
// 渠道测试（controller/channel-test.go）不经过 HandleGroupRatio 的乘 k，上下文里没有 k，所以不记。
func adtyRecordConsumeEarning(c *gin.Context, userId int, params *model.RecordConsumeLogParams) {
	if c != nil {
		if _, self := c.Get(AdtyOwnerSelfUseContextKey); self {
			return
		}
	}
	k, d, ok := AdtySiteMarkupFromContext(c)
	if !ok {
		return
	}
	// 余额下限：收益按实扣记，日志额度减去平台承担的差额。
	charged := int64(params.Quota) - adtyLoggedShortfall(params.Other)
	if charged <= 0 {
		return
	}
	siteID := c.GetInt(AdtySiteIdContextKey)
	reqID := strings.TrimSpace(c.GetString(common.RequestIdKey))
	if reqID == "" {
		reqID = strings.TrimPrefix(strings.TrimSpace(c.GetString(adtyBillingOperationContextKey)), "request:")
	}
	if c.GetBool(adtyDurableBillingContextKey) {
		if lineErr := upsertAdtySiteEarningLineTx(model.DB, siteID, c.GetInt(AdtySiteOwnerContextKey), userId, params.ModelName, params.Group, reqID, charged, k, d); lineErr != nil {
			common.SysError(common.LogText("record site earning line failed site=%d user=%d: %s", siteID, userId, lineErr.Error()))
		}
		return
	}
	AdtySiteEarningHook(siteID, userId, charged, k, d, model.AdtySiteEarningUsage, c.GetInt(AdtySiteOwnerContextKey))
	if lineErr := upsertAdtySiteEarningLineTx(model.DB, siteID, c.GetInt(AdtySiteOwnerContextKey), userId, params.ModelName, params.Group, reqID, charged, k, d); lineErr != nil {
		common.SysError(common.LogText("record site earning line failed site=%d user=%d: %s", siteID, userId, lineErr.Error()))
	}
}

// adtyAppendTaskSiteAdminInfo 把任务快照里的 k、d、站点写进日志的 admin_info（taskBillingOther 调用），
// RecordTaskBillingLog 的钩子从那里读。普通用户的日志投影看不到 admin_info。
func adtyAppendTaskSiteAdminInfo(bc *model.TaskBillingContext, other *model.LogOther) {
	if bc == nil || other == nil || !adtyEarningFactorsUsable(bc.SiteMarkup, bc.SiteCostFactor) {
		return
	}
	other.SetAdmin("site_markup", bc.SiteMarkup)
	other.SetAdmin("site_cost_factor", bc.SiteCostFactor)
	other.SetAdmin("site_id", bc.SiteId)
	other.SetAdmin("site_owner_user_id", bc.SiteOwnerUserId)
}

// adtyRecordTaskBillingEarning 是 RecordTaskBillingLog 的钩子：任务差额结算（consume）记 usage，
// 退款和差额为负（refund）按快照的 k、d 记负收益。
// MJ 退款把提交时写入任务的 k、d 放进 admin_info。没有这份快照的旧任务才回退到当前倍率。
func adtyRecordTaskBillingEarning(params *model.RecordTaskBillingLogParams) {
	if params == nil || params.Quota <= 0 {
		return
	}
	kind := model.AdtySiteEarningUsage
	switch params.LogType {
	case model.LogTypeConsume:
	case model.LogTypeRefund:
		kind = model.AdtySiteEarningRefund
	default:
		return
	}
	snapshot := params.Other.Snapshot()
	adminInfo, _ := snapshot["admin_info"].(map[string]any)
	if key, ok := adminInfo["billing_operation_key"].(string); ok && key != "" {
		op, err := model.GetBillingOperation(key)
		if err != nil {
			common.SysError(common.LogText("task earning payer snapshot failed: %s", err.Error()))
			return
		}
		if op.UserId == params.UserId && op.TokenId == params.TokenId {
			stampRequestUsageOwner(params.Other, op)
		}
		if op.PayerKind == model.BillingPayerOwner {
			return
		}
		adminInfo["site_owner_user_id"] = op.SiteOwnerUserId
	}
	k, _ := adminInfo["site_markup"].(float64)
	d, _ := adminInfo["site_cost_factor"].(float64)
	siteId, _ := adminInfo["site_id"].(int)
	if !adtyEarningFactorsUsable(k, d) {
		if _, isTask := snapshot["is_task"]; isTask {
			return // 主站客户的任务
		}
		site := AdtyBillingSiteForUser(params.UserId)
		if site == nil {
			return
		}
		cost, ok := AdtySiteCostFactor(site)
		if !ok {
			return
		}
		siteId, d = site.Id, cost.InexactFloat64()
		k = adtyMarkupFloat(AdtySiteMarkup(params.UserId, params.Group, params.ModelName), params.UserId, params.Group)
	}
	// 余额下限：差额结算的收益按实扣记（只有补扣会有差额，退款日志里没有这个键）。
	quota := int64(params.Quota) - adtyLoggedShortfall(params.Other)
	if quota <= 0 {
		return
	}
	lineQuota := quota
	if kind == model.AdtySiteEarningRefund {
		lineQuota = -quota
	}
	if adminInfo["billing_operation_key"] != nil {
		ownerID := adtyAnyInt(adminInfo["site_owner_user_id"])
		opKey := strings.TrimSpace(fmt.Sprint(adminInfo["billing_operation_key"]))
		if lineErr := upsertAdtySiteEarningLineTx(model.DB, siteId, ownerID, params.UserId, params.ModelName, params.Group, opKey, lineQuota, k, d); lineErr != nil {
			common.SysError(common.LogText("record site earning line failed site=%d user=%d: %s", siteId, params.UserId, lineErr.Error()))
		}
		return
	}
	ownerID, err := model.ResolveAdtySiteFinancialOwnerTx(model.DB, siteId, adtyAnyInt(adminInfo["site_owner_user_id"]))
	if err != nil {
		// 与下面 AdtySiteEarningHook 里同一失败语义对齐：日志要能定位到站和人，
		// 计数要让这个站进对账（adty_site_settlement_task 会捞 earning_hook_failures > 0 的站）。
		common.SysError(common.LogText("task earning owner snapshot failed site=%d user=%d kind=%s: %s",
			siteId, params.UserId, kind, err.Error()))
		if incErr := model.DB.Model(&model.AdtySite{}).Where("id = ?", siteId).
			UpdateColumn("earning_hook_failures", gorm.Expr("earning_hook_failures + 1")).Error; incErr != nil {
			common.SysError(common.LogText("count site earning failure site=%d: %s", siteId, incErr.Error()))
		}
		return
	}
	AdtySiteEarningHook(siteId, params.UserId, quota, k, d, kind, ownerID)
	if lineErr := upsertAdtySiteEarningLineTx(model.DB, siteId, ownerID, params.UserId, params.ModelName, params.Group, "", lineQuota, k, d); lineErr != nil {
		common.SysError(common.LogText("record site earning line failed site=%d user=%d: %s", siteId, params.UserId, lineErr.Error()))
	}
}

// adtyBonusUSDForSite 是一笔订单要由站长出的加赠（美元），也就是分站订单的支付方式加赠；不是分站订单返回 0。
func adtyBonusUSDForSite(ext *model.AdtyTopupOrder) decimal.Decimal {
	if ext == nil || ext.SiteId <= 0 {
		return decimal.Zero
	}
	return decimal.Max(ext.BonusUSD, decimal.Zero)
}

// adtySiteBonusAfterCreditTx 在入账事务里记下站长出资的加赠（负数日汇总），供报表使用。
// 分站赠金保障在下单时已冻结，付款后转为保障冻结，实际支出仍为 0；不得在这里再扣钱包。
// 只用 OnConflict DoUpdate 写，不做可能失败的查询（审阅重点 10）。
func adtySiteBonusAfterCreditTx(tx *gorm.DB, topUp *model.TopUp, ext *model.AdtyTopupOrder) error {
	bonus := adtyBonusUSDForSite(ext)
	if bonus.IsZero() {
		return nil
	}
	// R5-053: Only book site bonus expense when the gift is actually credited now.
	// Deferred gifts (pending/pending_review) book when fulfilled; canceled gifts never book.
	switch ext.GiftFulfillmentStatus {
	case model.AdtyGiftFulfillmentPending, model.AdtyGiftFulfillmentPendingReview, model.AdtyGiftFulfillmentCanceled:
		return nil
	}
	return upsertAdtySiteEarningTx(tx, adtySiteEarningDayFn(), adtySiteEarningEntry{
		SiteId: ext.SiteId, SiteOwnerUserId: ext.SiteOwnerUserId, UserId: topUp.UserId, Kind: model.AdtySiteEarningBonus, Earning: bonus.Neg(),
	})
}

// adtySiteBonusAfterRefundTx：这笔充值退款时赠金不退给客户（只退没花掉的本金）。按「先花本金」，收回的赠金是
// min(G, R)，客户已经花掉的赠金收不回来，所以站长出的加赠只按收回的比例还给站长：
// 站长出资的加赠 × min(G, R) ÷ G，记一笔 bonus_reversal（正数）。平台优惠码出的赠金收回了不还给任何人
// （adtyBonusUSDForSite 只算站长出的部分）。站长的消费分成不扣：充值本身不产生站长收益，收益来自消费，
// 客户已经花掉的额度是真实消费。
func adtySiteBonusAfterRefundTx(tx *gorm.DB, topUp *model.TopUp, ext *model.AdtyTopupOrder) error {
	bonus, totalBonusQuota, err := effectiveSiteGiftBasisTx(tx, ext)
	if err != nil {
		return err
	}
	if bonus.IsZero() {
		// R4-015: Pass full order bonus quota as denominator so main-site (SiteId==0)
		// partial refunds shrink pending_review gifts proportionally instead of canceling 100%.
		return cancelPendingGiftFulfillmentOnFullRefundTx(tx, ext, ext.RefundBonusQuota, topUpBonusQuota(ext))
	}
	hasRows, prevReclaimed, cumReclaimed, err := adtyCompletedManualRefundBonusStatsTx(tx, ext)
	if err != nil {
		return err
	}
	var returned decimal.Decimal
	if hasRows {
		if cumReclaimed <= 0 && ext.RefundBonusQuota <= 0 {
			return cancelPendingGiftFulfillmentOnFullRefundTx(tx, ext, 0, totalBonusQuota)
		}
		denom := totalBonusQuota
		if denom <= 0 {
			denom = cumReclaimed
		}
		var cumUSD decimal.Decimal
		if denom > 0 && cumReclaimed >= denom {
			cumUSD = bonus
		} else {
			cumUSD = adtyProportionalGiftUSD(bonus, cumReclaimed, denom)
		}
		prevUSD := adtyProportionalGiftUSD(bonus, prevReclaimed, denom)
		returned = cumUSD.Sub(prevUSD)
	} else {
		if ext.RefundBonusQuota <= 0 {
			return cancelPendingGiftFulfillmentOnFullRefundTx(tx, ext, 0, totalBonusQuota)
		}
		denom := totalBonusQuota
		if denom <= 0 {
			denom = ext.RefundBonusQuota
		}
		reclaimed := min(denom, min(ext.RefundBonusQuota, max(ext.RefundDeductedQuota, 0)))
		if denom > 0 && reclaimed >= denom {
			returned = bonus
		} else {
			returned = adtyProportionalGiftUSD(bonus, reclaimed, denom)
		}
	}
	if returned.IsPositive() {
		if err := upsertAdtySiteEarningTx(tx, adtySiteEarningDayFn(), adtySiteEarningEntry{
			SiteId: ext.SiteId, SiteOwnerUserId: ext.SiteOwnerUserId, UserId: topUp.UserId, Kind: model.AdtySiteEarningBonusReversal, Earning: returned,
		}); err != nil {
			return err
		}
	}
	return releaseUnusedSiteGiftGuaranteeTx(tx, ext)
}

// ErrAdtyAmountTierRetired is returned when a caller tries to save the retired amount-tier gift.
var ErrAdtyAmountTierRetired = common.NewMessage("Amount-tier bonuses are retired")

// SaveAdtySiteBonusRates rejects new amount-tier gift tables. Payment-method rates are the live rule.
func SaveAdtySiteBonusRates(siteId int, rates map[int]float64) error {
	return ErrAdtyAmountTierRetired
}

func adtyUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "23505") || strings.Contains(text, "duplicate key")
}

// ReviewAdtySiteCustomer 管理员复核风控客户（分站·审核）：认可后之后的消费正常计收益；
// convertSkipped 时把还没结算的 skipped 行改成 usage。
func ReviewAdtySiteCustomer(userId, adminId int, convertSkipped bool) error {
	_, err := ReviewAdtySiteCustomerWithAudit(userId, adminId, convertSkipped)
	return err
}

func ReviewAdtySiteCustomerWithAudit(userId, adminId int, convertSkipped bool) (string, error) {
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var hint model.AdtySiteCustomer
		if err := tx.Select("user_id", "site_id").Where("user_id = ?", userId).First(&hint).Error; err != nil {
			return err
		}
		// R7-004: Pre-lock site earnings wallet BEFORE AdtySiteCustomer when converting
		// skipped rows so lock order matches ApplyBillingSettlement
		// (AdtySiteWallet(earnings) -> AdtySiteCustomer -> AdtySiteEarningDaily).
		if convertSkipped && hint.SiteId > 0 {
			if err := preLockAdtySiteWalletsTx(tx, hint.SiteId, model.AdtySiteWalletEarnings); err != nil {
				return err
			}
		}
		var customer model.AdtySiteCustomer
		if err := model.AdtyLockForUpdate(tx).Where("user_id = ?", userId).First(&customer).Error; err != nil {
			return err
		}
		if convertSkipped && customer.SiteId != hint.SiteId {
			return model.ErrBillingOperationConflict
		}
		if err := tx.Model(&model.AdtySiteCustomer{}).Where("user_id = ?", userId).Update("reviewed", true).Error; err != nil {
			return err
		}
		if convertSkipped {
			if err := tx.Model(&model.AdtySiteCustomer{}).Where("user_id = ?", userId).Update("earning_risk_released", true).Error; err != nil {
				return err
			}
			var events []model.AdtySiteEarningEvent
			if err := model.AdtyLockForUpdate(tx).Where("user_id = ? AND site_id = ? AND risk_flag <> ? AND (risk_released = ? OR risk_released IS NULL) AND (review_status = ? OR review_status IS NULL)",
				userId, customer.SiteId, "", false, "").Order("operation_key ASC").Find(&events).Error; err != nil {
				return err
			}
			for _, event := range events {
				if err := tx.Model(&event).Update("risk_released", true).Error; err != nil {
					return err
				}
			}
			var rows []model.AdtySiteEarningDaily
			if err := tx.Where("user_id = ? AND site_id = ? AND kind = ? AND status = ?", userId, customer.SiteId,
				model.AdtySiteEarningSkipped, model.AdtySiteEarningPending).Order("id ASC").Find(&rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				// Old operation aggregates can contain both heuristic flags and
				// explicit finance exclusions. Preserve the latter in this row.
				retained := adtySiteEarningEntry{}
				if strings.Contains(row.Day, "+op") {
					var aggregates int64
					if err := tx.Model(&model.AdtySiteEarningDaily{}).Where("site_id = ? AND user_id = ? AND site_owner_user_id = ? AND kind = ? AND status = ? AND (day = ? OR day LIKE ?)",
						row.SiteId, row.UserId, row.SiteOwnerUserId, model.AdtySiteEarningSkipped, model.AdtySiteEarningPending, adtyEarningDayOf(row.Day)+"+op", adtyEarningDayOf(row.Day)+"+op+%").Count(&aggregates).Error; err != nil {
						return err
					}
					if aggregates != 1 {
						return model.ErrBillingOperationConflict
					}
					var excluded []model.AdtySiteEarningEvent
					if err := tx.Where("site_id = ? AND user_id = ? AND site_owner_user_id IN ? AND day = ? AND projected = ? AND review_status = ?",
						row.SiteId, row.UserId, []int{0, row.SiteOwnerUserId}, adtyEarningDayOf(row.Day), true, model.AdtyEarningOriginalSkipped).Find(&excluded).Error; err != nil {
						return err
					}
					for _, event := range excluded {
						revenue, platform, earning, err := adtyDurableEarningSplitTx(tx, &event)
						if err != nil {
							return err
						}
						retained.Quota += int64(event.Quota)
						retained.Revenue = retained.Revenue.Add(revenue)
						retained.Platform = retained.Platform.Add(platform)
						retained.Earning = retained.Earning.Add(earning)
					}
				}
				quota, revenue, platform, earning := row.Quota-retained.Quota, row.Revenue.Sub(retained.Revenue), row.Platform.Sub(retained.Platform), row.Earning.Sub(retained.Earning)
				if quota == 0 && revenue.IsZero() && platform.IsZero() && earning.IsZero() {
					continue
				}
				kind := model.AdtySiteEarningUsage
				if quota < 0 || (quota == 0 && earning.IsNegative()) {
					kind = model.AdtySiteEarningRefund
				}
				if err := upsertAdtySiteEarningTx(tx, row.Day, adtySiteEarningEntry{
					SiteId: row.SiteId, SiteOwnerUserId: row.SiteOwnerUserId, UserId: row.UserId, Kind: kind,
					OperationKey: fmt.Sprintf("customer-risk-review:%d", row.Id),
					Reason:       row.Reason, Quota: quota, Revenue: revenue, Platform: platform, Earning: earning,
				}); err != nil {
					return err
				}
				if retained.Quota != 0 || !retained.Revenue.IsZero() || !retained.Platform.IsZero() || !retained.Earning.IsZero() {
					if err := tx.Model(&model.AdtySiteEarningDaily{}).Where("id = ?", row.Id).Updates(map[string]any{
						"quota": retained.Quota, "revenue": retained.Revenue, "platform": retained.Platform, "earning": retained.Earning,
					}).Error; err != nil {
						return err
					}
					continue
				}
				if err := tx.Where("id = ?", row.Id).Delete(&model.AdtySiteEarningDaily{}).Error; err != nil {
					return err
				}
			}
		}
		var auditErr error
		auditEvent, auditErr = recordSiteAuditWithIntentTx(tx, customer.SiteId, "customer_review", adminId, map[string]any{"user_id": userId, "risk_flag": customer.RiskFlag},
			map[string]any{"reviewed": true, "convert_skipped": convertSkipped}, "")
		return auditErr
	})
	if err != nil {
		return "", err
	}
	if auditEvent != nil {
		deliverAdtyCommittedAudit(*auditEvent)
		return auditEvent.EventId, nil
	}
	return "", nil
}

func init() {
	AdtyRecordConsumeEarning = adtyRecordConsumeEarning
	model.AdtyOnTaskBillingLog = adtyRecordTaskBillingEarning
}
