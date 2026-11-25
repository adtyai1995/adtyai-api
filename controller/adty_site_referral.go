package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Retired global site marketing writes return the addressable owner policy error.
func PutAdtySiteReferralCap(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusGone, service.ErrSiteMarketingOwnerOnly)
}

// PostAdtySiteReferralClick validates a subsite invite code on the current
// subsite host and issues a server-signed HttpOnly cookie using the site's
// configured CookieTTLDays (R2-51).
func PostAdtySiteReferralClick(c *gin.Context) {
	siteID := service.AdtyRequestSiteID(c)
	if siteID <= 0 {
		common.ApiErrorT(c, "Site referral is not enabled")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	canonicalCode, err := service.ValidateAdtySiteInviteCode(siteID, body.Code)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	ttlDays, err := service.SetAdtySiteInviteCookie(c, siteID, canonicalCode)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"code": canonicalCode, "ttl_days": ttlDays})
}

func GetAdtySiteInviteBatchPolicy(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	view, err := service.LoadAdtySiteInviteBatchPolicy()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func PutAdtySiteInviteBatchPolicy(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	var body struct {
		Threshold   string `json:"threshold"`
		WindowHours string `json:"window_hours"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.SaveAdtySiteInviteBatchPolicy(body.Threshold, body.WindowHours); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"saved": true})
}

func GetAdtySiteCommissionMargin(c *gin.Context) {
	siteID, ok := adtyCommissionMarginSite(c)
	if !ok {
		return
	}
	allowed, err := service.SiteCommissionMarginAllowed(siteID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"site_id": siteID, "allowed": allowed})
}

func PutAdtySiteCommissionMargin(c *gin.Context) {
	siteID, ok := adtyCommissionMarginSite(c)
	if !ok {
		return
	}
	var body struct {
		Allowed *bool `json:"allowed"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil || body.Allowed == nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.AuthorizeAdtySiteCommissionMargin(siteID, *body.Allowed); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"site_id": siteID, "allowed": *body.Allowed})
}

func adtyCommissionMarginSite(c *gin.Context) (int, bool) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return 0, false
	}
	siteID, err := strconv.Atoi(c.Param("id"))
	if err != nil || siteID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return 0, false
	}
	var count int64
	if err := model.DB.Model(&model.AdtySite{}).Where("id = ?", siteID).Count(&count).Error; err != nil {
		common.ApiError(c, err)
		return 0, false
	}
	if count != 1 {
		common.ApiErrorT(c, "Invalid parameters")
		return 0, false
	}
	return siteID, true
}

func GetAdtyOwnerSiteReferral(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	view, err := service.LoadAdtyOwnerSiteReferral(site.Id, c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func PutAdtyOwnerSiteReferral(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	var body service.AdtySiteMarketingUpdate
	if err := common.DecodeJson(c.Request.Body, &body); err != nil || body == (service.AdtySiteMarketingUpdate{}) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.SaveAdtyOwnerSiteMarketingWithAudit(site.Id, c.GetInt("id"), body, c.ClientIP(), c.Request.UserAgent()); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"saved": true})
}

type adtySiteCommissionCustomerItem struct {
	model.AdtySiteInviteCommissionView
	InviterUsername string `json:"inviter_username"`
	InviteeUsername string `json:"invitee_username"`
}

func GetAdtySiteCommissions(c *gin.Context) {
	rows, err := service.ListAdtySiteInviteCommissions(c.GetInt("id"), time.Now())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	views, err := model.PresentSiteInviteCommissions(model.DB, rows)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	ids := make([]int, 0, len(views)*2)
	for _, view := range views {
		ids = append(ids, view.InviterId, view.InviteeId)
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	items := make([]adtySiteCommissionCustomerItem, len(views))
	for i, view := range views {
		items[i] = adtySiteCommissionCustomerItem{
			AdtySiteInviteCommissionView: view,
			InviterUsername:              names[view.InviterId],
			InviteeUsername:              names[view.InviteeId],
		}
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

func PostAdtySiteCommissionConvert(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		AmountUSD string `json:"amount_usd"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	amount, err := decimal.NewFromString(strings.TrimSpace(body.AmountUSD))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.ConvertAdtySiteInviteCommission(c.GetInt("id"), id, amount, strings.TrimSpace(c.GetHeader("Idempotency-Key"))); err != nil {
		if errors.Is(err, model.ErrAdtyIdempotencyConflict) {
			common.ApiErrorStatus(c, http.StatusConflict, err)
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"converted": true})
}

func GetAdtySiteInviteWithdrawRules(c *gin.Context) {
	source, _ := strconv.Atoi(c.Query("site_id"))
	view, err := service.LoadAdtyCustomerSiteInviteWithdrawRules(c.GetInt("id"), source)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func GetAdtySiteInviteWithdrawalQuote(c *gin.Context) {
	source, err := strconv.Atoi(c.DefaultQuery("site_id", "0"))
	amount, parseErr := decimal.NewFromString(c.Query("amount_usd"))
	if err != nil || source < 0 || parseErr != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	quote, err := service.QuoteAdtySiteInviteWithdrawal(c.GetInt("id"), source, amount, c.Query("method"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, quote)
}

func PostAdtySiteInviteWithdrawal(c *gin.Context) {
	var body struct {
		SiteID          int    `json:"site_id"`
		AmountUSD       string `json:"amount_usd"`
		Method          string `json:"method"`
		Network         string `json:"network"`
		AccountNo       string `json:"account_no"`
		AccountName     string `json:"account_name"`
		QRImageURL      string `json:"qr_image_url"`
		ExpectedQuoteID string `json:"expected_quote_id"`
		ExpectedCNYRate string `json:"expected_cny_rate"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	amount, err := decimal.NewFromString(strings.TrimSpace(body.AmountUSD))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	expected := decimal.Zero
	if strings.TrimSpace(body.ExpectedCNYRate) != "" {
		expected, err = decimal.NewFromString(strings.TrimSpace(body.ExpectedCNYRate))
		if err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	row, err := service.CreateAdtySiteInviteWithdrawal(service.AdtySiteInviteWithdrawalInput{
		UserID: c.GetInt("id"), SiteID: body.SiteID, Amount: amount, Method: body.Method,
		Network: body.Network, AccountNo: body.AccountNo, AccountName: body.AccountName,
		QRImageURL: body.QRImageURL, ExpectedCNYRate: expected, ExpectedQuoteID: body.ExpectedQuoteID, RequireQuote: true,
		IdempotencyKey: strings.TrimSpace(c.GetHeader("Idempotency-Key")),
	})
	if err != nil {
		var quote *service.SiteInviteQuoteChanged
		if errors.As(err, &quote) {
			common.ApiErrorStatus(c, http.StatusOK, common.NewMessage(quote.Error()), gin.H{"code": "quote_changed", "data": quote.Quote})
			return
		}
		var changed *service.RateChangedError
		if errors.As(err, &changed) {
			common.ApiErrorStatus(c, http.StatusOK, common.NewMessage("The exchange rate has changed. Confirm the new amount"), gin.H{"code": "rate_changed", "data": changed.Change})
			return
		}
		if errors.Is(err, model.ErrAdtyIdempotencyConflict) {
			common.ApiErrorStatus(c, http.StatusConflict, err)
			return
		}
		common.ApiError(c, err)
		return
	}
	names := model.LookupUsernamesByIDs(model.DB, []int{row.UserId})
	common.ApiSuccess(c, presentAdtySiteInviteWithdrawals([]model.AdtySiteInviteWithdrawal{*row}, names, true)[0])
}

func GetAdtySiteInviteWithdrawals(c *gin.Context) {
	userID := c.GetInt("id")
	rows, err := service.ListAdtySiteInviteWithdrawals(userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	names := model.LookupUsernamesByIDs(model.DB, []int{userID})
	common.ApiSuccess(c, gin.H{"items": presentAdtySiteInviteWithdrawals(rows, names, true)})
}

func PostAdtySiteInviteWithdrawalCancel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.CancelAdtySiteInviteWithdrawal(c.GetInt("id"), id, time.Now().Unix()); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"canceled": true})
}

func GetAdtyOwnerSiteInviteWithdrawals(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	page := common.GetPageQuery(c)
	rows, total, err := service.ListAdtyOwnerSiteInviteWithdrawals(site.Id, c.Query("status"), page)
	presentCommissionPage(c, page, rows, total, err, true)
}
func GetClosedSiteInviteWithdrawals(c *gin.Context) {
	page := common.GetPageQuery(c)
	rows, total, err := service.ListClosedSiteInviteWithdrawals(c.Query("status"), page)
	presentCommissionPage(c, page, rows, total, err, false)
}
func presentCommissionPage(c *gin.Context, page *common.PageInfo, rows []model.AdtySiteInviteWithdrawal, total int64, err error, redactOperators bool) {
	if err != nil {
		common.ApiError(c, err)
		return
	}
	ids := make([]int, 0, len(rows)*5)
	for _, row := range rows {
		ids = append(ids, row.UserId)
		if !redactOperators {
			ids = append(ids, row.ReviewedBy, row.PaidBy, row.FailureBy, row.LegacyDecisionBy)
		}
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	page.SetTotal(int(total))
	page.SetItems(presentAdtySiteInviteWithdrawals(rows, names, redactOperators))
	common.ApiSuccess(c, page)
}

func PostAdtyOwnerSiteInviteWithdrawalReview(c *gin.Context) {
	if _, ok := adtyOwnerSiteOr403(c); !ok {
		return
	}
	processSiteWithdrawalReview(c, func(ownerID int, id int64, approve bool, reason string) error {
		return service.ReviewAdtySiteInviteWithdrawalWithClientMeta(ownerID, id, approve, reason, c.ClientIP(), c.Request.UserAgent())
	})
}
func PostClosedSiteInviteWithdrawalReview(c *gin.Context) {
	processClosedSiteWithdrawalReview(c, service.ReviewClosedAdtySiteInviteWithdrawalWithAuditForActor)
}
func processSiteWithdrawalReview(c *gin.Context, process func(int, int64, bool, string) error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		Approve bool   `json:"approve"`
		Reason  string `json:"reason"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := process(c.GetInt("id"), id, body.Approve, body.Reason); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"reviewed": true})
}

func processClosedSiteWithdrawalReview(c *gin.Context, process func(int, int, bool, int64, bool, string) (string, bool, error)) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		Approve bool   `json:"approve"`
		Reason  string `json:"reason"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	eventID, replayed, err := process(c.GetInt("id"), c.GetInt("role"), isAccessTokenRequest(c), id, body.Approve, body.Reason)
	if err != nil {
		writeReferralError(c, err)
		return
	}
	if replayed {
		markIdempotentReplayNoAudit(c)
	} else if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, gin.H{"reviewed": true})
}

func PostAdtyOwnerSiteInviteWithdrawalPaid(c *gin.Context) {
	if _, ok := adtyOwnerSiteOr403(c); !ok {
		return
	}
	processSiteWithdrawalPaid(c, func(ownerID int, id int64, txID, proof string) error {
		return service.MarkAdtySiteInviteWithdrawalPaidWithClientMeta(ownerID, id, txID, proof, c.ClientIP(), c.Request.UserAgent())
	})
}
func PostClosedSiteInviteWithdrawalFailed(c *gin.Context) {
	processClosedSiteWithdrawalReview(c, service.ConfirmClosedSiteInvitePayoutFailureWithAuditForActor)
}
func PostClosedSiteInviteWithdrawalPaid(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		TxID  string `json:"tx_id"`
		Proof string `json:"proof"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	eventID, replayed, err := service.MarkClosedAdtySiteInviteWithdrawalPaidWithAuditForActor(c.GetInt("id"), c.GetInt("role"), isAccessTokenRequest(c), id, body.TxID, body.Proof)
	if err != nil {
		writeReferralError(c, err)
		return
	}
	if replayed {
		markIdempotentReplayNoAudit(c)
	} else if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, gin.H{"paid": true})
}
func processSiteWithdrawalPaid(c *gin.Context, process func(int, int64, string, string) error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		TxID  string `json:"tx_id"`
		Proof string `json:"proof"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := process(c.GetInt("id"), id, body.TxID, body.Proof); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"paid": true})
}

type adtySiteInviteWithdrawalItem struct {
	model.AdtySiteInviteWithdrawal
	CanCancel                bool   `json:"can_cancel"`
	CancelReason             string `json:"cancel_reason"`
	Username                 string `json:"username"`
	ReviewedByUsername       string `json:"reviewed_by_username,omitempty"`
	PaidByUsername           string `json:"paid_by_username,omitempty"`
	FailureByUsername        string `json:"failure_by_username,omitempty"`
	LegacyDecisionByUsername string `json:"legacy_decision_by_username,omitempty"`
}

func presentAdtySiteInviteWithdrawals(rows []model.AdtySiteInviteWithdrawal, names map[int]string, redactOperators bool) []adtySiteInviteWithdrawalItem {
	now := time.Now()
	out := make([]adtySiteInviteWithdrawalItem, len(rows))
	for i, row := range rows {
		row.QRImageURL = service.ResignReferralAssetURLForWithdrawal(row.QRImageURL, row.UserId, model.ReferralAssetPurposeWithdrawalQR, now)
		row.ProofNote = service.ResignReferralAssetURLForWithdrawal(row.ProofNote, 0, model.ReferralAssetPurposePaymentProof, now)
		if redactOperators {
			row.ReviewedBy = 0
			row.PaidBy = 0
			row.FailureBy = 0
			row.LegacyDecisionBy = 0
		}
		state := service.SiteInviteCancellation(&row, now.Unix())
		item := adtySiteInviteWithdrawalItem{AdtySiteInviteWithdrawal: row, CanCancel: state.CanCancel, CancelReason: state.CancelReason}
		if names != nil {
			item.Username = names[row.UserId]
			if !redactOperators {
				item.ReviewedByUsername = names[row.ReviewedBy]
				item.PaidByUsername = names[row.PaidBy]
				item.FailureByUsername = names[row.FailureBy]
				item.LegacyDecisionByUsername = names[row.LegacyDecisionBy]
			}
		}
		out[i] = item
	}
	return out
}

func PutAdtyOwnerSiteReferralEnabled(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil || body.Enabled == nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.SaveAdtyOwnerSiteMarketingWithAudit(site.Id, c.GetInt("id"), service.AdtySiteMarketingUpdate{Enabled: body.Enabled}, c.ClientIP(), c.Request.UserAgent()); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"enabled": *body.Enabled})
}

func PostAdtyOwnerSiteCoverageResume(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	if err := service.ResumeAdtySiteInviteCoverageWithAudit(site.Id, c.GetInt("id"), time.Now(), c.ClientIP(), c.Request.UserAgent()); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"resumed": true})
}

func GetAdtyOwnerSiteTeams(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	page := common.GetPageQuery(c)
	// Without status the list keeps its historical meaning: active teams only.
	status := c.Query("status")
	switch status {
	case "":
		status = model.AdtyTeamStatusActive
	case "all":
		status = ""
	}
	rows, total, err := service.ListAdtyTeamsForAdmin(service.AdtyTeamListFilter{SiteID: &site.Id, Status: status, Query: c.Query("q")}, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": rows, "total": total})
}

func PutAdtyOwnerSiteTeamDiscount(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		Discount string `json:"discount"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.SaveAdtyOwnerSiteTeamDiscount(site.Id, c.GetInt("id"), id, body.Discount); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"updated": true, "discount": decimal.RequireFromString(strings.TrimSpace(body.Discount)).String()})
}

func adtySiteReferralPageQuery(c *gin.Context) *common.PageInfo {
	page := common.GetPageQuery(c)
	if strings.TrimSpace(c.Query("p")) == "" {
		if raw := strings.TrimSpace(c.Query("page")); raw != "" {
			if p, err := strconv.Atoi(raw); err == nil && p >= 1 {
				page.Page = p
			}
		}
	}
	return page
}

func GetAdtyOwnerSiteReferralOverview(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	overview, err := service.LoadAdtyOwnerSiteReferralOverview(site.Id, time.Now())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, overview)
}

func GetAdtyOwnerSiteReferralAffiliates(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	page := adtySiteReferralPageQuery(c)
	items, total, err := service.ListAdtyOwnerSiteReferralAffiliates(site.Id, c.Query("q"), page.GetStartIdx(), page.GetPageSize(), time.Now())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"items":     items,
		"total":     total,
		"page":      page.GetPage(),
		"page_size": page.GetPageSize(),
	})
}

func GetAdtyOwnerSiteReferralCommissions(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	inviterUserID := 0
	rawInviter := strings.TrimSpace(c.Query("inviter_user_id"))
	if rawInviter == "" {
		rawInviter = strings.TrimSpace(c.Query("inviter_id"))
	}
	if rawInviter != "" {
		uid, err := strconv.Atoi(rawInviter)
		if err != nil || uid <= 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		inviterUserID = uid
	}
	page := adtySiteReferralPageQuery(c)
	items, total, err := service.ListAdtyOwnerSiteReferralCommissions(site.Id, c.Query("status"), c.Query("q"), page.GetStartIdx(), page.GetPageSize(), time.Now(), inviterUserID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"items":     items,
		"total":     total,
		"page":      page.GetPage(),
		"page_size": page.GetPageSize(),
	})
}

type adtyOwnerSiteInviteItem struct {
	model.AdtySiteInvite
	InviterUsername string `json:"inviter_username"`
	InviteeUsername string `json:"invitee_username"`
}

func GetAdtyOwnerSiteInvites(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	rows, err := service.ListAdtySiteInvites(site.Id, c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	ids := make([]int, 0, len(rows)*2)
	for _, row := range rows {
		ids = append(ids, row.InviterId, row.InviteeId)
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	items := make([]adtyOwnerSiteInviteItem, len(rows))
	for i, row := range rows {
		items[i] = adtyOwnerSiteInviteItem{
			AdtySiteInvite:  row,
			InviterUsername: names[row.InviterId],
			InviteeUsername: names[row.InviteeId],
		}
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

func PostAdtyOwnerSiteInvite(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	var body struct {
		InviterID int `json:"inviter_id"`
		InviteeID int `json:"invitee_id"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.BindAdtyOwnerSiteInviteFromIPWithAudit(site.Id, c.GetInt("id"), body.InviterID, body.InviteeID, "", c.ClientIP(), c.Request.UserAgent()); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"bound": true})
}

func PostAdtyOwnerSiteReferralAffiliateUpdate(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	userID, err := strconv.Atoi(c.Param("user_id"))
	if err != nil || userID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body service.AdtySiteAffiliateUpdateInput
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.UpdateAdtyOwnerSiteReferralAffiliate(site.Id, c.GetInt("id"), userID, body, c.ClientIP(), c.Request.UserAgent()); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"updated": true})
}

func GetAdtyOwnerSiteReferralAuditLogs(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	page := adtySiteReferralPageQuery(c)
	items, total, err := service.ListAdtyOwnerSiteReferralAuditLogs(site.Id, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"items":     items,
		"total":     total,
		"page":      page.GetPage(),
		"page_size": page.GetPageSize(),
	})
}

func GetAdtySiteCustomerReferralProfile(c *gin.Context) {
	profile, err := service.GetAdtySiteCustomerReferralProfile(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, profile)
}

func PostAdtySiteCustomerReferralBind(c *gin.Context) {
	var body struct {
		InviteCode string `json:"invite_code"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.BindAdtySiteInviteByCodeForUser(c.GetInt("id"), body.InviteCode, service.HashReferralRiskValue(c.ClientIP())); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"bound": true})
}
