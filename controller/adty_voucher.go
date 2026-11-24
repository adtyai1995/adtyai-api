package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

func PostAdtyVoucherRedeem(c *gin.Context) {
	var body struct {
		Code string `json:"code"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	result, err := service.RedeemAdtyVoucher(c.GetInt("id"), body.Code)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func PostAdtySiteVoucher(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	var body struct {
		Purpose         string `json:"purpose"`
		AmountUSD       string `json:"amount_usd"`
		Count           int    `json:"count"`
		Source          string `json:"source"`
		Reason          string `json:"reason"`
		IntentKey       string `json:"intent_key"`
		IdempotencyKey  string `json:"idempotency_key"`
		ClientRequestId string `json:"client_request_id"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	amount, err := decimal.NewFromString(body.AmountUSD)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	intentKey, ok := resolveAdtyVoucherIntentKey(c, body.IntentKey, body.IdempotencyKey, body.ClientRequestId)
	if !ok {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if body.Purpose != model.AdtyVoucherPurposeSiteCustomer {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	if !adtyRequireOwnerProof(c, service.AdtySiteOwnerActionVoucherIssue, site.Id) {
		return
	}
	issued, err := service.IssueAdtyVoucherBatch(service.AdtyVoucherIssueInput{
		IssuerId: c.GetInt("id"), Purpose: body.Purpose, SiteId: site.Id, AmountUSD: amount,
		Source: body.Source, Reason: body.Reason, IntentKey: intentKey, Count: body.Count,
	})
	if err != nil {
		if errors.Is(err, service.ErrAdtyVoucherIntentConflict) || errors.Is(err, model.ErrAdtyIdempotencyConflict) {
			common.ApiErrorStatus(c, http.StatusConflict, err)
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, issued)
}

func PostAdtyAdminVoucher(c *gin.Context) {
	var body struct {
		Purpose           string `json:"purpose"`
		SiteId            int    `json:"site_id"`
		DesignatedOwnerId int    `json:"designated_owner_id"`
		OwnerUserId       int    `json:"owner_user_id"`
		OwnerId           int    `json:"owner_id"`
		AmountUSD         string `json:"amount_usd"`
		Count             int    `json:"count"`
		Reason            string `json:"reason"`
		IntentKey         string `json:"intent_key"`
		IdempotencyKey    string `json:"idempotency_key"`
		ClientRequestId   string `json:"client_request_id"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	amount, err := decimal.NewFromString(body.AmountUSD)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	designatedOwnerID, ok := resolveAdtyVoucherDesignatedOwnerID(body.DesignatedOwnerId, body.OwnerUserId, body.OwnerId)
	if !ok {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	intentKey, ok := resolveAdtyVoucherIntentKey(c, body.IntentKey, body.IdempotencyKey, body.ClientRequestId)
	if !ok {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if body.Purpose != model.AdtyVoucherPurposeMainUser && body.Purpose != model.AdtyVoucherPurposeSiteMargin {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	issued, err := service.IssueAdtyVoucherBatch(service.AdtyVoucherIssueInput{
		IssuerId: c.GetInt("id"), Purpose: body.Purpose, SiteId: body.SiteId,
		DesignatedOwnerId: designatedOwnerID, AmountUSD: amount, Reason: body.Reason, IntentKey: intentKey, Count: body.Count,
	})
	if err != nil {
		if errors.Is(err, service.ErrAdtyVoucherIntentConflict) || errors.Is(err, model.ErrAdtyIdempotencyConflict) {
			common.ApiErrorStatus(c, http.StatusConflict, err)
			return
		}
		common.ApiError(c, err)
		return
	}
	if issued.Replayed {
		markIdempotentReplayNoAudit(c)
	} else if issued.AuditEventID != "" {
		rememberPreparedAuditEvent(c, issued.AuditEventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, issued)
}

func resolveAdtyVoucherDesignatedOwnerID(values ...int) (int, bool) {
	resolved := 0
	for _, v := range values {
		if v < 0 {
			return 0, false
		}
		if v == 0 {
			continue
		}
		if resolved != 0 && resolved != v {
			return 0, false
		}
		resolved = v
	}
	return resolved, true
}

func resolveAdtyVoucherIntentKey(c *gin.Context, bodyValues ...string) (string, bool) {
	candidates := append([]string{}, bodyValues...)
	if c != nil {
		candidates = append(candidates,
			c.GetHeader("X-Intent-Key"),
			c.GetHeader("X-Idempotency-Key"),
			c.GetHeader("Idempotency-Key"),
		)
	}
	resolved := ""
	for _, raw := range candidates {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if len(v) > 128 {
			return "", false
		}
		if resolved != "" && resolved != v {
			return "", false
		}
		resolved = v
	}
	return resolved, true
}

func writeAdtyVoucherError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrAdtyVoucherForbidden) {
		common.ApiErrorStatus(c, http.StatusForbidden, err)
		return
	}
	common.ApiError(c, err)
}

// PostAdtyAdminVoucherFreeze lets Root freeze any pending code under RootAuth + option:write (R10-007).
func PostAdtyAdminVoucherFreeze(c *gin.Context) {
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	eventID, err := service.FreezeAdtyVoucherWithAudit(c.GetInt("id"), int64(id))
	if err != nil {
		writeAdtyVoucherError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, gin.H{"id": id, "status": model.AdtyVoucherStatusFrozen})
}

func PostAdtyVoucherFreeze(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	if !adtyRequireOwnerProof(c, service.AdtySiteOwnerActionVoucherFreeze, id) {
		return
	}
	eventID, err := service.FreezeAdtySiteVoucherWithAudit(c.GetInt("id"), site.Id, int64(id))
	if err != nil {
		writeAdtyVoucherError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, gin.H{"id": id, "status": model.AdtyVoucherStatusFrozen})
}

func PostAdtyVoucherCancel(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	if !adtyRequireOwnerProof(c, service.AdtySiteOwnerActionVoucherCancel, id) {
		return
	}
	eventID, err := service.CancelAdtySiteVoucherWithAudit(c.GetInt("id"), site.Id, int64(id))
	if err != nil {
		writeAdtyVoucherError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, gin.H{"id": id, "status": model.AdtyVoucherStatusCancelled})
}

func PutAdtyVoucherIssuanceLimit(c *gin.Context) {
	var body struct {
		LimitUSD string `json:"limit_usd"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	limit, err := decimal.NewFromString(body.LimitUSD)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.SetAdtyVoucherIssuanceLimit(c.GetInt("id"), limit); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"limit_usd": limit.String()})
}

// GetAdtyAdminVouchers lists every code for Root, optionally by status, purpose, target site, and batch_no.
func GetAdtyAdminVouchers(c *gin.Context) {
	page := common.GetPageQuery(c)
	targetSiteID := 0
	if rawSiteID := strings.TrimSpace(c.Query("site_id")); rawSiteID != "" {
		parsed, err := strconv.Atoi(rawSiteID)
		if err != nil || parsed < 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		targetSiteID = parsed
	}
	items, total, err := service.ListAdtyVouchers(service.AdtyVoucherListFilter{
		Status:       strings.TrimSpace(c.Query("status")),
		Purpose:      strings.TrimSpace(c.Query("purpose")),
		TargetSiteId: targetSiteID,
		BatchNo:      strings.TrimSpace(c.Query("batch_no")),
	}, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"total": total, "items": items, "page": page.GetPage(), "page_size": page.GetPageSize()})
}

// GetAdtySiteVouchers lists the site_customer codes this owner issued from this site's wallets.
func GetAdtySiteVouchers(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	page := common.GetPageQuery(c)
	items, total, err := service.ListAdtyVouchers(service.AdtyVoucherListFilter{
		Status:   strings.TrimSpace(c.Query("status")),
		Purpose:  model.AdtyVoucherPurposeSiteCustomer,
		IssuerId: c.GetInt("id"),
		SiteId:   site.Id,
		BatchNo:  strings.TrimSpace(c.Query("batch_no")),
	}, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"total": total, "items": items, "page": page.GetPage(), "page_size": page.GetPageSize(),
	})
}

func GetAdtyVoucherIssuanceLimit(c *gin.Context) {
	view, err := service.LoadAdtyVoucherIssuance()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

// PostAdtyAdminVoucherCancel lets Root cancel any unredeemed code and release
// its source. The route is Root-only, so no owner step-up applies.
func PostAdtyAdminVoucherCancel(c *gin.Context) {
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	eventID, err := service.CancelAdtyVoucherWithAudit(c.GetInt("id"), int64(id))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, gin.H{"id": id, "status": model.AdtyVoucherStatusCancelled})
}
