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
	if err := service.SaveAdtyOwnerSiteMarketing(site.Id, c.GetInt("id"), body); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"saved": true})
}

func GetAdtySiteCommissions(c *gin.Context) {
	rows, err := service.ListAdtySiteInviteCommissions(c.GetInt("id"), time.Now())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items, err := model.PresentSiteInviteCommissions(model.DB, rows)
	if err != nil {
		common.ApiError(c, err)
		return
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
	common.ApiSuccess(c, presentAdtySiteInviteWithdrawals([]model.AdtySiteInviteWithdrawal{*row}, nil, true)[0])
}

func GetAdtySiteInviteWithdrawals(c *gin.Context) {
	rows, err := service.ListAdtySiteInviteWithdrawals(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": presentAdtySiteInviteWithdrawals(rows, nil, true)})
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
	ids := make([]int, 0, len(rows))
	seen := map[int]struct{}{}
	for _, row := range rows {
		if _, ok := seen[row.UserId]; ok {
			continue
		}
		seen[row.UserId] = struct{}{}
		ids = append(ids, row.UserId)
	}
	names, err := adtyUsernamesByID(ids)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	page.SetTotal(int(total))
	page.SetItems(presentAdtySiteInviteWithdrawals(rows, names, redactOperators))
	common.ApiSuccess(c, page)
}

func PostAdtyOwnerSiteInviteWithdrawalReview(c *gin.Context) {
	if _, ok := adtyOwnerSiteOr403(c); !ok {
		return
	}
	processSiteWithdrawalReview(c, service.ReviewAdtySiteInviteWithdrawal)
}
func PostClosedSiteInviteWithdrawalReview(c *gin.Context) {
	processClosedSiteWithdrawalReview(c, service.ReviewClosedAdtySiteInviteWithdrawalWithAudit)
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

func processClosedSiteWithdrawalReview(c *gin.Context, process func(int, int64, bool, string) (string, bool, error)) {
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
	eventID, replayed, err := process(c.GetInt("id"), id, body.Approve, body.Reason)
	if err != nil {
		common.ApiError(c, err)
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
	processSiteWithdrawalPaid(c, service.MarkAdtySiteInviteWithdrawalPaid)
}
func PostClosedSiteInviteWithdrawalFailed(c *gin.Context) {
	processClosedSiteWithdrawalReview(c, service.ConfirmClosedSiteInvitePayoutFailureWithAudit)
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
	eventID, replayed, err := service.MarkClosedAdtySiteInviteWithdrawalPaidWithAudit(c.GetInt("id"), id, body.TxID, body.Proof)
	if err != nil {
		common.ApiError(c, err)
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
	CanCancel    bool   `json:"can_cancel"`
	CancelReason string `json:"cancel_reason"`
	Username     string `json:"username,omitempty"`
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
		}
		out[i] = item
	}
	return out
}

func adtyUsernamesByID(ids []int) (map[int]string, error) {
	names := map[int]string{}
	if len(ids) == 0 {
		return names, nil
	}
	var users []model.User
	if err := model.DB.Select("id", "username").Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	for _, user := range users {
		names[user.Id] = user.Username
	}
	return names, nil
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
	if err := service.SaveAdtyOwnerSiteMarketing(site.Id, c.GetInt("id"), service.AdtySiteMarketingUpdate{Enabled: body.Enabled}); err != nil {
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
	if err := service.ResumeAdtySiteInviteCoverage(site.Id, time.Now()); err != nil {
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
	common.ApiSuccess(c, gin.H{"items": rows})
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
	if err := service.BindAdtyOwnerSiteInviteFromIP(site.Id, c.GetInt("id"), body.InviterID, body.InviteeID, service.HashReferralRiskValue(c.ClientIP())); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"bound": true})
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
