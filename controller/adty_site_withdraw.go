package controller

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

func GetAdtySiteWithdrawRules(c *gin.Context) {
	if _, ok := adtyOwnerAccountingSite(c); !ok {
		return
	}
	if raw, ok := c.GetQuery("amount_usd"); ok {
		amount, err := decimal.NewFromString(raw)
		if err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		payout, err := service.QuoteSiteWithdrawal(amount, c.Query("method"))
		if err != nil {
			common.ApiError(c, err)
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
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func GetAdtySiteWithdrawals(c *gin.Context) {
	site, ok := adtyOwnerAccountingSite(c)
	if !ok {
		return
	}
	page, offset, limit := adtyPage(c)
	rows, total, err := service.PageAdtySiteWithdrawals(site.Id, c.GetInt("id"), c.Query("status"), c.Query("number"), offset, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	page.SetTotal(int(total))
	page.SetItems(rows)
	common.ApiSuccess(c, page)
}

func PostAdtySiteWithdrawal(c *gin.Context) {
	site, ok := adtyOwnerAccountingSite(c)
	if !ok {
		return
	}
	var body struct {
		AmountUSD      string           `json:"amount_usd"`
		Method         string           `json:"method"`
		Network        string           `json:"network"`
		AccountNo      string           `json:"account_no"`
		AccountName    string           `json:"account_name"`
		ExpectedRate   string           `json:"expected_rate"`
		ExpectedPayout *decimal.Decimal `json:"expected_payout"`
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
	expected, err := decimal.NewFromString(strings.TrimSpace(body.ExpectedRate))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	row, err := service.CreateAdtySiteWithdrawal(service.AdtySiteWithdrawInput{
		SiteID: site.Id, OwnerID: c.GetInt("id"), AmountUSD: amount, Method: body.Method, Network: body.Network,
		AccountNo: body.AccountNo, AccountName: body.AccountName, ExpectedRate: expected, ExpectedPayout: body.ExpectedPayout,
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
	})
	if err != nil {
		common.ApiError(c, err)
		return
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
		common.ApiError(c, err)
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
	if err := service.SaveAdtySiteWithdrawRules(body, c.GetInt("id")); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"saved": true})
}

// GetAdtyAdminSiteWithdrawals is Root's review list across all sites.
func GetAdtyAdminSiteWithdrawals(c *gin.Context) {
	page := common.GetPageQuery(c)
	items, total, err := service.ListAdtySiteWithdrawalsForAdmin(strings.TrimSpace(c.Query("status")), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
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
	var err error
	switch action {
	case "paying":
		err = service.MarkAdtySiteWithdrawalPaying(id, c.GetInt("id"), body.Attempt)
	case "recall":
		reason := strings.TrimSpace(body.Reason)
		if reason == "" {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Recall reason is required"))
			return
		}
		if body.TxID != "" {
			err = service.ConfirmAdtySiteWithdrawalFailed(id, c.GetInt("id"), body.Attempt, reason, body.TxID)
		} else {
			err = service.RecallAdtySiteWithdrawalToPending(id, c.GetInt("id"), reason, body.Attempt)
		}
	case "reject":
		reason := strings.TrimSpace(body.Reason)
		if reason == "" {
			common.ApiErrorT(c, "Reject reason is required")
			return
		}
		err = service.RejectAdtySiteWithdrawal(id, c.GetInt("id"), reason)
	default:
		err = service.MarkAdtySiteWithdrawalPaid(id, c.GetInt("id"), body.TxID, body.Proof, body.Attempt)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"updated": true})
}
