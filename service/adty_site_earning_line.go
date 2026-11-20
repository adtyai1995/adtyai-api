package service

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// RecordAdtySiteEarningLine stores one earning fact with model and billing group.
// It does not accept or store an upstream channel name.
func RecordAdtySiteEarningLine(siteID, userID int, modelName, group, requestID string, quota int64, k, d float64) error {
	modelName, group, requestID = strings.TrimSpace(modelName), strings.TrimSpace(group), strings.TrimSpace(requestID)
	if siteID <= 0 || userID <= 0 || quota == 0 || modelName == "" || group == "" || !adtyEarningFactorsUsable(k, d) {
		return common.NewMessage("Invalid parameters")
	}
	revenue, platform, earning := AdtySiteEarningSplit(quota, decimal.NewFromFloat(k), decimal.NewFromFloat(d))
	row := model.AdtySiteEarningLine{
		SiteId: siteID, UserId: userID, ModelName: modelName, Group: group, RequestId: requestID,
		Quota: quota, Markup: decimal.NewFromFloat(k), CostFactor: decimal.NewFromFloat(d),
		Revenue: revenue, Platform: platform, Earning: earning,
	}
	return model.DB.Create(&row).Error
}

// ListAdtyOwnerEarningLines returns recent earning facts for one site.
func ListAdtyOwnerEarningLines(siteID, offset, limit int) ([]model.AdtySiteEarningLine, error) {
	if siteID <= 0 {
		return nil, common.NewMessage("Invalid parameters")
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var rows []model.AdtySiteEarningLine
	err := model.DB.Where("site_id = ?", siteID).Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, err
}

func recordDurableAdtySiteEarningLineTx(tx *gorm.DB, event *model.AdtySiteEarningEvent, revenue, platform, earning decimal.Decimal) error {
	if event.Quota == 0 || event.ReviewStatus == model.AdtyEarningOriginalSkipped || event.ReviewStatus == model.AdtyEarningNeedsReview {
		return nil
	}
	var ops []model.BillingOperation
	if err := tx.Where("operation_key = ? AND site_id = ? AND user_id = ?", event.OperationKey, event.SiteId, event.UserId).Limit(1).Find(&ops).Error; err != nil {
		return err
	}
	if len(ops) == 0 {
		return nil
	} // Historical events may predate the immutable request metadata.
	op := &ops[0]
	var modelName, group string
	snap := op.RequestPriceSnapshot()
	if snap == nil && op.EarningRootKey != "" && op.EarningRootKey != op.OperationKey {
		var roots []model.BillingOperation
		if err := tx.Where("operation_key = ? AND site_id = ? AND user_id = ?", op.EarningRootKey, event.SiteId, event.UserId).Limit(1).Find(&roots).Error; err != nil {
			return err
		}
		if len(roots) == 1 {
			snap = roots[0].RequestPriceSnapshot()
		}
	}
	if snap != nil {
		modelName, group = snap.ModelName, snap.BillingGroup
	}
	if snap == nil && modelName == "" && op.BillingContext != "" {
		var bc model.TaskBillingContext
		if err := common.UnmarshalJsonStr(op.BillingContext, &bc); err != nil {
			return err
		}
		modelName = bc.OriginModelName
		if bc.PriceSnapshot != nil {
			if modelName == "" {
				modelName = bc.PriceSnapshot.ModelName
			}
			group = bc.PriceSnapshot.BillingGroup
		}
	}
	if snap == nil && op.ReferenceId > 0 && (op.Kind == "task" || op.Kind == "request") && (modelName == "" || group == "") {
		var tasks []model.Task
		if err := tx.Where("id = ? AND user_id = ?", op.ReferenceId, event.UserId).Limit(1).Find(&tasks).Error; err != nil {
			return err
		}
		if len(tasks) == 1 {
			if modelName == "" {
				modelName = tasks[0].Properties.OriginModelName
			}
			if group == "" {
				group = tasks[0].Group
			}
		}
	}
	if modelName == "" && (op.Kind == "mj_charge" || op.Kind == "mj_refund") {
		modelName = "midjourney"
	}
	if strings.TrimSpace(modelName) == "" {
		return nil
	}
	requestID := strings.TrimPrefix(event.OperationKey, "request:")
	if len(requestID) > 64 {
		sum := sha256.Sum256([]byte(event.OperationKey))
		requestID = fmt.Sprintf("op:%x", sum[:24])
	}
	// The caller holds this event's row lock and commits Projected atomically.
	// Request IDs alone are not unique across independent billing operations.
	return tx.Create(&model.AdtySiteEarningLine{SiteId: event.SiteId, UserId: event.UserId,
		ModelName: strings.TrimSpace(modelName), Group: group, RequestId: requestID, Quota: int64(event.Quota),
		Markup: decimal.NewFromFloat(event.Markup), CostFactor: decimal.NewFromFloat(event.CostFactor),
		Revenue: revenue, Platform: platform, Earning: earning, CreatedAt: event.CreatedAt}).Error
}
