package controller

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type adtySiteEarningReviewItem struct {
	model.AdtySiteEarningEvent
	Username          string `json:"username"`
	SiteOwnerUsername string `json:"site_owner_username,omitempty"`
}

// GetAdtySiteEarningReviews exposes unresolved historical task adjustments to
// finance. The original site and stable task root remain visible after transfer.
func GetAdtySiteEarningReviews(c *gin.Context) {
	page := common.GetPageQuery(c)
	query := model.DB.Model(&model.AdtySiteEarningEvent{}).Where("review_status = ?", model.AdtyEarningNeedsReview)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	var events []model.AdtySiteEarningEvent
	if err := query.Order("created_at, operation_key").Limit(page.GetPageSize()).Offset(page.GetStartIdx()).Find(&events).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	ids := make([]int, 0, len(events)*2)
	for _, ev := range events {
		ids = append(ids, ev.UserId, ev.SiteOwnerUserId)
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	items := make([]adtySiteEarningReviewItem, len(events))
	for i, ev := range events {
		items[i] = adtySiteEarningReviewItem{
			AdtySiteEarningEvent: ev,
			Username:             names[ev.UserId],
			SiteOwnerUsername:    names[ev.SiteOwnerUserId],
		}
	}
	common.ApiSuccess(c, gin.H{"total": total, "items": items})
}

// ReviewAdtySiteEarning resolves an original charge's disposition, rather than
// treating each refund or adjustment as an independent finance decision.
func ReviewAdtySiteEarning(c *gin.Context) {
	var body struct {
		OperationKey   string `json:"operation_key"`
		OriginalCharge string `json:"original_charge"`
		Reason         string `json:"reason"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil || strings.TrimSpace(body.OperationKey) == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if middleware.RequireSecurityProof(c, service.AdtySiteEarningReviewOperation(body.OperationKey, body.OriginalCharge, body.Reason)) == nil {
		return
	}
	eventID, replayed, err := service.ReviewAdtySiteEarningRootWithAuditForActor(body.OperationKey, c.GetInt("id"), c.GetInt("role"), isAccessTokenRequest(c), body.OriginalCharge, body.Reason)
	if err != nil {
		writeAdtyFinanceError(c, err)
		return
	}
	if replayed {
		markIdempotentReplayNoAudit(c)
	} else if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, gin.H{"operation_key": body.OperationKey, "original_charge": body.OriginalCharge})
}
