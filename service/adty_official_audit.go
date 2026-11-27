package service

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Delivery never changes the outcome of an already committed business transaction.
func deliverAdtyCommittedAudit(event model.AuditLog) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = model.DeliverAuditIntent(ctx, event.EventId)
}

func appendAdtyOfficialAuditTx(tx *gorm.DB, actorId, siteId int, objectType, objectID, action, reason, approvalRef string, before, after any, result string) (*model.AuditLog, error) {
	return appendAdtyOfficialAuditWithOpParamsTx(tx, actorId, siteId, objectType, objectID, action, reason, approvalRef, nil, before, after, result)
}

func appendAdtyOfficialAuditWithOpParamsTx(tx *gorm.DB, actorId, siteId int, objectType, objectID, action, reason, approvalRef string, opParams map[string]any, before, after any, result string) (*model.AuditLog, error) {
	objectType = strings.TrimSpace(objectType)
	objectID = strings.TrimSpace(objectID)
	action = strings.TrimSpace(action)
	if objectType == "" || objectID == "" || action == "" || strings.TrimSpace(result) == "" {
		return nil, nil
	}
	if tx == nil {
		return nil, nil
	}
	hasAuditIntentTable, err := model.AdtyTableExists(tx, &model.AuditIntent{})
	if err != nil {
		return nil, err
	}
	if !hasAuditIntentTable {
		return nil, nil
	}
	rec := model.OfficialAuditWrite{
		UserId:      actorId,
		SiteID:      model.OfficialAuditSiteIDPointer(siteId),
		ObjectType:  objectType,
		ObjectID:    objectID,
		Action:      action,
		Category:    model.AuditCategoryOperation,
		Content:     action,
		Before:      before,
		After:       after,
		Reason:      reason,
		ApprovalRef: approvalRef,
		Result:      result,
		Success:     result == model.AuditResultSuccess,
	}
	if len(opParams) > 0 {
		rec.Other.Op = &model.AuditOperation{
			Action: action,
			Params: model.AuditFields(opParams),
		}
	}
	if actorId > 0 {
		var u model.User
		if err := tx.Select("id", "username", "role").Where("id = ?", actorId).Limit(1).Find(&u).Error; err == nil && u.Id > 0 {
			rec.Username = u.Username
			rec.ActorRole = u.Role
		}
	}
	entry, err := model.PrepareOfficialAuditLog(nil, rec)
	if err != nil {
		return nil, err
	}
	if err := model.AppendAuditIntentTx(tx, entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

func writeAdtyOfficialAudit(actorId, siteId int, objectType, objectID, action, reason, approvalRef string, before, after any, result string) {
	objectType = strings.TrimSpace(objectType)
	objectID = strings.TrimSpace(objectID)
	action = strings.TrimSpace(action)
	if objectType == "" || objectID == "" || action == "" || strings.TrimSpace(result) == "" {
		return
	}
	rec := model.OfficialAuditWrite{
		UserId:      actorId,
		SiteID:      model.OfficialAuditSiteIDPointer(siteId),
		ObjectType:  objectType,
		ObjectID:    objectID,
		Action:      action,
		Category:    model.AuditCategoryOperation,
		Content:     action,
		Before:      before,
		After:       after,
		Reason:      reason,
		ApprovalRef: approvalRef,
		Result:      result,
		Success:     result == model.AuditResultSuccess,
	}
	if actorId > 0 {
		if user, err := model.GetUserById(actorId, false); err == nil && user != nil {
			rec.Username = user.Username
			rec.ActorRole = user.Role
		}
	}
	if err := model.WriteDurableOfficialAuditLog(nil, rec); err != nil {
		common.SysLog(common.LogText("official %s audit write failed: %s", action, err.Error()))
	}
}

func adtyWalletIdempotencyExists(tx *gorm.DB, keys ...string) (bool, error) {
	if tx == nil {
		return false, nil
	}
	filtered := make([]string, 0, len(keys))
	for _, key := range keys {
		if strings.TrimSpace(key) != "" {
			filtered = append(filtered, key)
		}
	}
	if len(filtered) == 0 {
		return false, nil
	}
	var n int64
	if err := tx.Model(&model.AdtySiteWalletLedger{}).Where("idempotency_key IN ?", filtered).Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

func appendAdtyGiftOfficialAuditTx(tx *gorm.DB, actorId, siteId int, tradeNo, action, reason string, before, after any) (*model.AuditLog, error) {
	return appendAdtyOfficialAuditTx(tx, actorId, siteId, "gift", tradeNo, action, reason, "", before, after, model.AuditResultSuccess)
}

func appendAdtyGiftOfficialAuditWithParamsTx(tx *gorm.DB, actorId, siteId int, tradeNo, action, reason string, opParams map[string]any, before, after any) (*model.AuditLog, error) {
	return appendAdtyOfficialAuditWithOpParamsTx(tx, actorId, siteId, "gift", tradeNo, action, reason, "", opParams, before, after, model.AuditResultSuccess)
}

func appendAdtyMarginWalletOfficialAuditTx(tx *gorm.DB, ledger *model.AdtySiteWalletLedger, change AdtySiteWalletChange, frozenBefore, frozenAfter, cycleUsed, cycleTier decimal.Decimal) (*model.AuditLog, error) {
	if ledger == nil || ledger.WalletKind != model.AdtySiteWalletMargin {
		return nil, nil
	}
	switch change.Type {
	case "gift_reserve", "gift_release", "gift_spend", "voucher_margin", "voucher_freeze",
		"voucher_release", "voucher_spend", "margin_root_credit",
		"owner_customer_balance", "owner_team_balance", "staff_balance", "financial_adjustment":
		// Parent gift, voucher, margin deposit, or manual balance operations write the official record.
		return nil, nil
	}
	return appendAdtyOfficialAuditTx(tx, change.OperatorID, ledger.SiteId, "margin", strconv.Itoa(ledger.SiteId), "adty.margin."+change.Type, change.Reason, "",
		map[string]any{
			"balance": ledger.BalanceBefore.String(),
			"frozen":  frozenBefore.String(),
		},
		map[string]any{
			"balance":    ledger.BalanceAfter.String(),
			"frozen":     frozenAfter.String(),
			"hold_delta": ledger.HoldDelta.String(),
			"delta":      ledger.Delta.String(),
			"type":       change.Type,
			"ref_type":   change.RefType,
			"ref_id":     change.RefID,
			"cycle_used": cycleUsed.String(),
			"cycle_tier": cycleTier.String(),
		},
		model.AuditResultSuccess)
}
