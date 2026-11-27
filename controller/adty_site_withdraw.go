package controller

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func writeAdtySiteWithdrawError(c *gin.Context, err error, forReview bool) {
	var changed *service.RateChangedError
	if errors.As(err, &changed) {
		common.ApiErrorStatus(c, http.StatusOK, common.NewMessage("The exchange rate has changed. Confirm the new amount"), gin.H{"code": "rate_changed", "data": changed.Change})
		return
	}
	switch {
	case errors.Is(err, model.ErrAdtyIdempotencyConflict), errors.Is(err, service.ErrAdtySiteWalletIdempotencyConflict):
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("This request was already used for another operation"))
	case errors.Is(err, service.ErrAdtySiteWalletInsufficient):
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Insufficient available funds"))
	case errors.Is(err, service.ErrAdtySiteWithdrawUnset):
		common.ApiErrorT(c, "Withdrawal rules are not set")
	case errors.Is(err, service.ErrAdtySiteWithdrawLocked):
		if forReview {
			common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("Withdrawal status has changed. Refresh and try again."))
		} else {
			common.ApiErrorT(c, "Only pending withdrawals can be canceled")
		}
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Withdrawal request not found"))
	case errors.Is(err, service.ErrAdtySiteWalletInvalidKind),
		errors.Is(err, service.ErrAdtySiteWalletInvalidOperation),
		errors.Is(err, service.ErrAdtySiteWalletIdempotencyKey):
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
	default:
		common.ApiError(c, err)
	}
}

func GetAdtySiteWithdrawRules(c *gin.Context) {
	if _, ok := adtyOwnerAccountingSite(c); !ok {
		return
	}
	if raw, ok := c.GetQuery("amount_usd"); ok {
		amount, err := common.ParseBoundedDecimalString(raw, 12, 8)
		if err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		method := strings.ToLower(strings.TrimSpace(c.Query("method")))
		if method == "" {
			method = "usdt"
		}
		if method != "usdt" {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Only USDT withdrawal is supported"))
			return
		}
		payout, err := service.QuoteSiteWithdrawal(amount, method)
		if err != nil {
			writeAdtySiteWithdrawError(c, err, false)
			return
		}
		common.ApiSuccess(c, gin.H{"payout_amount": payout.String()})
		return
	}
	GetAdtyAdminWithdrawRules(c)
}

func GetAdtyAdminWithdrawRules(c *gin.Context) {
	view, err := service.LoadAdtySiteWithdrawRules()
	if err != nil {
		writeAdtySiteWithdrawError(c, err, true)
		return
	}
	common.ApiSuccess(c, view)
}

func redactSiteWithdrawalForOwner(row *model.AdtySiteWithdrawal) {
	if row == nil {
		return
	}
	row.ReviewerId = 0
	if row.Status != model.AdtySiteWithdrawPaid && row.Status != model.AdtySiteWithdrawRejected {
		row.ProofNote = ""
	}
}

type adtyOwnerSiteWithdrawalItem struct {
	model.AdtySiteWithdrawal
	OwnerUsername string `json:"owner_username"`
}

func GetAdtySiteWithdrawals(c *gin.Context) {
	site, ok := adtyOwnerAccountingSite(c)
	if !ok {
		return
	}
	page, offset, limit := adtyPage(c)
	rows, total, err := service.PageAdtySiteWithdrawals(site.Id, c.GetInt("id"), c.Query("status"), c.Query("number"), offset, limit)
	if err != nil {
		writeAdtySiteWithdrawError(c, err, true)
		return
	}
	ownerIDs := make([]int, 0, len(rows))
	// R5-041/R6-036: Redact Root reviewer_id and non-terminal internal recall/failure notes before returning to site owner.
	for i := range rows {
		redactSiteWithdrawalForOwner(&rows[i])
		ownerIDs = append(ownerIDs, rows[i].OwnerUserId)
	}
	names := model.LookupUsernamesByIDs(model.DB, ownerIDs)
	items := make([]adtyOwnerSiteWithdrawalItem, len(rows))
	for i, row := range rows {
		items[i] = adtyOwnerSiteWithdrawalItem{
			AdtySiteWithdrawal: row,
			OwnerUsername:      names[row.OwnerUserId],
		}
	}
	page.SetTotal(int(total))
	page.SetItems(items)
	common.ApiSuccess(c, page)
}

// PostAdtySiteWithdrawalQR retires owner Alipay withdrawal QR uploads (R-34 #4,
// returning 410 Gone for default/withdrawal_qr purpose) while retaining
// purpose=payment_proof for active site owners uploading customer
// invite-commission payout proof screenshots.
func PostAdtySiteWithdrawalQR(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	if site.OwnerUserId != c.GetInt("id") || site.Status == model.AdtySiteStatusClosed || site.Status == model.AdtySiteStatusDeleting {
		common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtySiteNotOwner)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 5<<20+64<<10)
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.ContentType())), "multipart/form-data") {
		if _, err := c.MultipartForm(); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				common.ApiErrorT(c, "The image must be at most 5 MB")
				return
			}
			common.ApiErrorT(c, "Choose an image to upload")
			return
		}
	}
	purpose := strings.TrimSpace(c.PostForm("purpose"))
	if purpose == "" || purpose == model.ReferralAssetPurposeWithdrawalQR {
		common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Owner Alipay withdrawal QR upload has been disabled"))
		return
	}
	if purpose != model.ReferralAssetPurposePaymentProof {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	header, err := c.FormFile("file")
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			common.ApiErrorT(c, "The image must be at most 5 MB")
			return
		}
		common.ApiErrorT(c, "Choose an image to upload")
		return
	}
	file, err := header.Open()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 5<<20+1))
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			common.ApiErrorT(c, "The image must be at most 5 MB")
			return
		}
		common.ApiError(c, err)
		return
	}
	if len(data) > 5<<20 {
		common.ApiErrorT(c, "The image must be at most 5 MB")
		return
	}
	contentType := http.DetectContentType(data)
	switch contentType {
	case "image/png", "image/jpeg", "image/webp":
	default:
		common.ApiErrorT(c, "Only PNG, JPEG or WebP images are accepted")
		return
	}
	link, err := service.SaveReferralAsset(data, contentType, c.GetInt("id"), purpose, "site_owner")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"url": link})
}

func PostAdtySiteWithdrawal(c *gin.Context) {
	site, ok := adtyOwnerAccountingSite(c)
	if !ok {
		return
	}
	var body struct {
		SiteID         int                    `json:"site_id"`
		AmountUSD      string                 `json:"amount_usd"`
		Method         string                 `json:"method"`
		Network        string                 `json:"network"`
		AccountNo      string                 `json:"account_no"`
		AccountName    string                 `json:"account_name"`
		QRImageURL     string                 `json:"qr_image_url"`
		ExpectedRate   string                 `json:"expected_rate"`
		ExpectedPayout *common.BoundedDecimal `json:"expected_payout"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if body.SiteID != 0 && body.SiteID != site.Id {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	method := strings.ToLower(strings.TrimSpace(body.Method))
	if method == "" {
		method = "usdt"
	}
	if method != "usdt" {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Only USDT withdrawal is supported"))
		return
	}
	amount, err := common.ParseBoundedDecimalString(body.AmountUSD, 12, 8)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	expected, err := common.ParseBoundedDecimalString(body.ExpectedRate, 12, 8)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var expectedPayout *decimal.Decimal
	if body.ExpectedPayout != nil {
		d := body.ExpectedPayout.Decimal
		expectedPayout = &d
	}
	network := strings.ToUpper(strings.TrimSpace(body.Network))
	accountNo := strings.TrimSpace(body.AccountNo)
	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if !adtyRequireOwnerProof(c, service.AdtySiteOwnerActionWithdrawalCreate, site.Id, map[string]any{
		"amount_usd":      amount.String(),
		"method":          method,
		"network":         network,
		"account_no":      accountNo,
		"account_name":    "",
		"qr_image_url":    "",
		"expected_rate":   expected.String(),
		"idempotency_key": idempotencyKey,
	}) {
		return
	}
	row, err := service.CreateAdtySiteWithdrawal(service.AdtySiteWithdrawInput{
		SiteID: site.Id, OwnerID: c.GetInt("id"), AmountUSD: amount, Method: method, Network: network,
		AccountNo: accountNo, AccountName: "", QRImageURL: "", ExpectedRate: expected, ExpectedPayout: expectedPayout,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		writeAdtySiteWithdrawError(c, err, false)
		return
	}
	redactSiteWithdrawalForOwner(row)
	if row != nil && row.QRImageURL != "" {
		row.QRImageURL = service.ResignReferralAssetURLForWithdrawal(row.QRImageURL, row.OwnerUserId, model.ReferralAssetPurposeWithdrawalQR, time.Now())
	}
	common.ApiSuccess(c, row)
}

func PostAdtySiteWithdrawalCancel(c *gin.Context) {
	site, ok := adtyOwnerAccountingSite(c)
	if !ok {
		return
	}
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	if err := service.CancelAdtySiteWithdrawal(site.Id, c.GetInt("id"), id); err != nil {
		writeAdtySiteWithdrawError(c, err, false)
		return
	}
	common.ApiSuccess(c, gin.H{"cancelled": true})
}

func PutAdtySiteWithdrawRules(c *gin.Context) {
	if c.GetInt("role") < common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	var body service.AdtySiteWithdrawRules
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	eventID, err := service.SaveAdtySiteWithdrawRulesWithAudit(body, c.GetInt("id"))
	if err != nil {
		writeAdtySiteWithdrawError(c, err, true)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, gin.H{"saved": true})
}

type adtyAdminSiteWithdrawalItem struct {
	service.AdtySiteWithdrawalAdminRow
	OwnerUsername    string `json:"owner_username"`
	ReviewerUsername string `json:"reviewer_username,omitempty"`
}

// GetAdtyAdminSiteWithdrawals is Root's review list across all sites.
func GetAdtyAdminSiteWithdrawals(c *gin.Context) {
	page := common.GetPageQuery(c)
	rows, total, err := service.ListAdtySiteWithdrawalsForAdmin(strings.TrimSpace(c.Query("status")), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		writeAdtySiteWithdrawError(c, err, true)
		return
	}
	ids := make([]int, 0, len(rows)*2)
	for _, row := range rows {
		ids = append(ids, row.OwnerUserId, row.ReviewerId)
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	items := make([]adtyAdminSiteWithdrawalItem, len(rows))
	for i, row := range rows {
		items[i] = adtyAdminSiteWithdrawalItem{
			AdtySiteWithdrawalAdminRow: row,
			OwnerUsername:              names[row.OwnerUserId],
			ReviewerUsername:           names[row.ReviewerId],
		}
	}
	common.ApiSuccess(c, gin.H{"total": total, "items": items, "page": page.GetPage(), "page_size": page.GetPageSize()})
}

func PostAdtySiteWithdrawalPaying(c *gin.Context) {
	reviewAdtySiteWithdrawal(c, "paying")
}

func PostAdtySiteWithdrawalRecall(c *gin.Context) {
	reviewAdtySiteWithdrawal(c, "recall")
}

func PostAdtySiteWithdrawalReject(c *gin.Context) {
	reviewAdtySiteWithdrawal(c, "reject")
}

func PostAdtySiteWithdrawalPaid(c *gin.Context) {
	reviewAdtySiteWithdrawal(c, "paid")
}

func reviewAdtySiteWithdrawal(c *gin.Context, action string) {
	if c.GetInt("role") < common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	var body struct {
		Attempt int    `json:"attempt_number"`
		Reason  string `json:"reason"`
		TxID    string `json:"tx_id"`
		Proof   string `json:"proof"`
	}
	// A malformed body must not fall through with zero values: reject can release
	// frozen earnings, so an unreadable request is refused instead of executed
	// with an empty reason. An empty body stays allowed because "paying" takes
	// no parameters.
	if err := common.DecodeJson(c.Request.Body, &body); err != nil && !errors.Is(err, io.EOF) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var eventID string
	var replayed bool
	var err error
	switch action {
	case "paying":
		eventID, replayed, err = service.MarkAdtySiteWithdrawalPayingWithAudit(id, c.GetInt("id"), body.Attempt)
	case "recall":
		reason := strings.TrimSpace(body.Reason)
		if reason == "" {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Recall reason is required"))
			return
		}
		if body.TxID != "" {
			eventID, err = service.ConfirmAdtySiteWithdrawalFailedWithAudit(id, c.GetInt("id"), body.Attempt, reason, body.TxID)
		} else {
			eventID, replayed, err = service.RecallAdtySiteWithdrawalToPendingWithAudit(id, c.GetInt("id"), reason, body.Attempt)
		}
	case "reject":
		reason := strings.TrimSpace(body.Reason)
		if reason == "" {
			common.ApiErrorT(c, "Reject reason is required")
			return
		}
		eventID, err = service.RejectAdtySiteWithdrawalWithAudit(id, c.GetInt("id"), reason)
	default:
		eventID, replayed, err = service.MarkAdtySiteWithdrawalPaidWithAudit(id, c.GetInt("id"), body.TxID, body.Proof, body.Attempt)
	}
	if err != nil {
		writeAdtySiteWithdrawError(c, err, true)
		return
	}
	if replayed {
		markIdempotentReplayNoAudit(c)
	} else if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, gin.H{"updated": true})
}
