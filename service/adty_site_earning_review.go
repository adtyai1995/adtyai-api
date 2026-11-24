package service

import (
	"context"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// ReviewAdtySiteEarningRoot records a finance operator's evidence-based
// classification of the original charge. It resolves the whole historical
// task, so later positive adjustments and refunds cannot receive new decisions.
func ReviewAdtySiteEarningRoot(operationKey string, adminId int, decision, reason string) error {
	_, _, err := ReviewAdtySiteEarningRootWithAudit(operationKey, adminId, decision, reason)
	return err
}

func ReviewAdtySiteEarningRootWithAudit(operationKey string, adminId int, decision, reason string) (string, bool, error) {
	return ReviewAdtySiteEarningRootWithAuditForActor(operationKey, adminId, 0, false, decision, reason)
}

func ReviewAdtySiteEarningRootWithAuditForActor(operationKey string, adminId, actorRole int, denyAdminTarget bool, decision, reason string) (string, bool, error) {
	if adminId <= 0 || strings.TrimSpace(reason) == "" ||
		(decision != model.AdtyEarningOriginalReleased && decision != model.AdtyEarningOriginalSkipped) {
		return "", false, common.NewMessage("Invalid parameters")
	}
	var event model.AdtySiteEarningEvent
	if err := model.DB.Where("operation_key = ?", operationKey).First(&event).Error; err != nil {
		return "", false, err
	}
	var keys []string
	var auditEvent *model.AuditLog
	replayed := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if actorRole > 0 || denyAdminTarget {
			var site model.AdtySite
			if err := model.AdtyLockForUpdate(tx).Select("id", "owner_user_id").First(&site, event.SiteId).Error; err != nil {
				return err
			}
			if site.OwnerUserId > 0 {
				var owner model.User
				if err := model.AdtyLockForUpdate(tx).Select("id", "role").First(&owner, site.OwnerUserId).Error; err != nil {
					return err
				}
				if adminId > 0 && adminId == owner.Id {
					return model.ErrUserQuotaPermission
				}
				if actorRole > 0 && actorRole != common.RoleRootUser && actorRole <= owner.Role {
					return model.ErrUserQuotaPermission
				}
				if denyAdminTarget && owner.Role >= common.RoleAdminUser {
					return model.ErrAdminTargetSessionRequired
				}
			}
		}
		var customers []model.AdtySiteCustomer
		if err := model.AdtyLockForUpdate(tx).Where("user_id = ?", event.UserId).Limit(1).Find(&customers).Error; err != nil {
			return err
		}
		var root model.BillingOperation
		if err := model.AdtyLockForUpdate(tx).Where("operation_key = ? AND kind = ? AND user_id = ? AND site_id = ?",
			event.RootOperationKey, "earning_root", event.UserId, event.SiteId).First(&root).Error; err != nil {
			return err
		}
		if root.EarningReviewStatus != model.AdtyEarningNeedsReview && root.EarningReviewStatus != decision {
			return model.ErrBillingOperationConflict
		}
		if root.EarningReviewStatus == model.AdtyEarningNeedsReview {
			if err := tx.Model(&root).Update("earning_review_status", decision).Error; err != nil {
				return err
			}
			var auditErr error
			auditEvent, auditErr = recordSiteAuditWithIntentTx(tx, event.SiteId, "earning_review", adminId,
				map[string]any{"root_operation_key": root.OperationKey, "status": model.AdtyEarningNeedsReview},
				map[string]any{"operation_key": operationKey, "root_operation_key": root.OperationKey, "original_charge": decision}, strings.TrimSpace(reason))
			if auditErr != nil {
				return auditErr
			}
		} else {
			replayed = true
		}
		if err := tx.Model(&model.AdtySiteEarningEvent{}).Where("root_operation_key = ? AND review_status = ?", root.OperationKey, model.AdtyEarningNeedsReview).
			Updates(map[string]any{"review_status": decision, "risk_released": decision == model.AdtyEarningOriginalReleased}).Error; err != nil {
			return err
		}
		return tx.Model(&model.AdtySiteEarningEvent{}).Where("root_operation_key = ? AND projected = ?", root.OperationKey, false).Pluck("operation_key", &keys).Error
	})
	if err != nil {
		return "", false, err
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	// Classification and audit are already durable. A projection failure can be
	// retried through the same decision or the normal recovery worker.
	for _, key := range keys {
		if err := projectAdtySiteEarningEvent(context.Background(), key); err != nil {
			return eventID, replayed, err
		}
	}
	return eventID, replayed, clearAdtyEarningReviewHold(event.SiteId)
}

func clearAdtyEarningReviewHold(siteId int) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteId).First(&site).Error; err != nil {
			return err
		}
		var unresolved int64
		if err := tx.Model(&model.AdtySiteEarningEvent{}).Where("site_id = ? AND (review_status = ? OR projected = ?)", siteId, model.AdtyEarningNeedsReview, false).Count(&unresolved).Error; err != nil {
			return err
		}
		if unresolved == 0 && site.SettleHoldReason == model.AdtyEarningReviewHoldReason {
			return tx.Model(&site).Update("settle_hold_reason", "").Error
		}
		return nil
	})
}
