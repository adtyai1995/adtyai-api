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

func PostAdtyEarningToMargin(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
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
	if err != nil || !amount.IsPositive() {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.TransferAdtyEarningToMargin(site.Id, c.GetInt("id"), amount, "earn-margin:"+key); err != nil {
		if errors.Is(err, model.ErrAdtyIdempotencyConflict) || errors.Is(err, service.ErrAdtySiteWalletIdempotencyConflict) {
			common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("This request was already used for another operation"))
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"transferred": true})
}

func PostAdtyAdminSiteMargin(c *gin.Context) {
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	var body struct {
		AmountUSD      string `json:"amount_usd"`
		Reason         string `json:"reason"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	amount, err := decimal.NewFromString(strings.TrimSpace(body.AmountUSD))
	if err != nil || !amount.IsPositive() {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key == "" {
		key = strings.TrimSpace(body.IdempotencyKey)
	}
	if key == "" || strings.TrimSpace(body.Reason) == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	ledger, eventID, replayed, err := service.CreditAdtySiteMarginByRootWithAudit(id, c.GetInt("id"), amount, body.Reason, key)
	if err != nil {
		if errors.Is(err, model.ErrAdtyIdempotencyConflict) {
			common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("This request was already used for another operation"))
			return
		}
		common.ApiError(c, err)
		return
	}
	if replayed {
		markIdempotentReplayNoAudit(c)
	} else if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, gin.H{
		"credited":       true,
		"ledger_id":      ledger.Id,
		"balance":        ledger.BalanceAfter.String(),
		"balance_before": ledger.BalanceBefore.String(),
		"balance_after":  ledger.BalanceAfter.String(),
	})
}

func GetAdtyOwnerWallets(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	adtySiteWalletResponse(c, site.Id, false, true)
}

func PostOwnerPersonalTransfer(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Owner self-use features have been disabled"))
}

func GetOwnerSelfUseBalance(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Owner self-use features have been disabled"))
}

func GetOwnerSelfUseUsage(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Owner self-use features have been disabled"))
}

func GetOwnerPrincipalTransfers(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Owner self-use features have been disabled"))
}

func GetAdtyOwnerWalletLedger(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	adtySiteWalletResponse(c, site.Id, true, true)
}

type adtyAnomalyDispositionAdminItem struct {
	model.AdtyAnomalyDisposition
	AccountUsername  string `json:"account_username,omitempty"`
	OperatorUsername string `json:"operator_username,omitempty"`
}

func GetAdtyAnomalyDispositions(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	siteID, err := strconv.Atoi(c.Query("site_id"))
	if err != nil || siteID < 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	page := common.GetPageQuery(c)
	rows, total, err := service.PageAdtyAnomalyDispositions(siteID, c.Query("status"), c.Query("order_ref"), c.Query("kind"), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	ids := make([]int, 0, len(rows)*2)
	for _, row := range rows {
		ids = append(ids, row.AccountId, row.OperatorId)
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	items := make([]adtyAnomalyDispositionAdminItem, len(rows))
	for i, row := range rows {
		items[i] = adtyAnomalyDispositionAdminItem{
			AdtyAnomalyDisposition: row,
			AccountUsername:        names[row.AccountId],
			OperatorUsername:       names[row.OperatorId],
		}
	}
	common.ApiSuccess(c, gin.H{"items": items, "total": total, "page": page.GetPage(), "page_size": page.GetPageSize()})
}

func PostAdtyAnomalyDispositionReview(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	eventID, err := service.ReviewAdtyAnomalyDispositionWithAudit(id, c.GetInt("id"), body.Status, body.Note)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, gin.H{"reviewed": true})
}

func GetAdtyFundAnomalies(c *gin.Context) {
	siteID, err := strconv.Atoi(c.Query("site_id"))
	if err != nil || siteID < 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	kinds, err := service.CollectAdtyFundAnomalies(siteID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"kinds": kinds})
}

func GetAdtyAdminSiteWallets(c *gin.Context) {
	siteID, ok := siteIdParam(c)
	if !ok {
		return
	}
	adtySiteWalletResponse(c, siteID, false, false)
}

func GetAdtyAdminSiteWalletLedger(c *gin.Context) {
	siteID, ok := siteIdParam(c)
	if !ok {
		return
	}
	adtySiteWalletResponse(c, siteID, true, false)
}

type adtySiteWalletLedgerItem struct {
	model.AdtySiteWalletLedger
	OperatorUsername string `json:"operator_username,omitempty"`
}

func adtySiteWalletResponse(c *gin.Context, siteID int, ledger bool, hideOperating bool) {
	var data any
	var err error
	if ledger {
		page := common.GetPageQuery(c)
		var items []model.AdtySiteWalletLedger
		var total int64
		var queryErr error
		if hideOperating {
			items, total, queryErr = service.ListAdtyOwnerSiteWalletLedger(siteID, c.Query("kind"), page.GetStartIdx(), page.GetPageSize())
			if queryErr == nil {
				callerID := c.GetInt("id")
				// R4-042: Redact Root operator IDs and internal platform remarks on owner wallet ledger views.
				for i := range items {
					if items[i].OperatorId > 0 && items[i].OperatorId != callerID {
						items[i].OperatorId = 0
						items[i].Reason = ""
					}
				}
			}
		} else {
			items, total, queryErr = service.ListAdtySiteWalletLedger(siteID, c.Query("kind"), page.GetStartIdx(), page.GetPageSize())
		}
		enriched := make([]adtySiteWalletLedgerItem, len(items))
		if queryErr == nil {
			opIDs := make([]int, 0, len(items))
			for _, item := range items {
				if item.OperatorId > 0 {
					opIDs = append(opIDs, item.OperatorId)
				}
			}
			names := model.LookupUsernamesByIDs(model.DB, opIDs)
			for i, item := range items {
				enriched[i] = adtySiteWalletLedgerItem{
					AdtySiteWalletLedger: item,
					OperatorUsername:     names[item.OperatorId],
				}
			}
		}
		data, err = gin.H{"total": total, "items": enriched, "page": page.GetPage(), "page_size": page.GetPageSize()}, queryErr
	} else {
		if hideOperating {
			if err := service.ReviewAdtyPooledSiteFinance(model.DB, siteID); err != nil {
				common.ApiError(c, err)
				return
			}
		}
		wallets, queryErr := service.ListAdtySiteWallets(siteID)
		if queryErr == nil && hideOperating {
			visible := make([]model.AdtySiteWallet, 0, len(wallets))
			for _, wallet := range wallets {
				if wallet.Kind != model.AdtySiteWalletOperating {
					visible = append(visible, wallet)
				}
			}
			wallets = visible
		}
		data, err = wallets, queryErr
	}
	switch {
	case errors.Is(err, service.ErrAdtyOperatingWalletHidden):
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Operating wallet is not available to the site owner"))
		return
	case errors.Is(err, service.ErrAdtySiteNotFound):
		common.ApiErrorStatus(c, http.StatusNotFound, err)
	case errors.Is(err, service.ErrAdtySiteWalletInvalidKind), errors.Is(err, service.ErrAdtySiteWalletInvalidOperation):
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
	case err != nil:
		common.ApiError(c, err)
	default:
		common.ApiSuccess(c, data)
	}
}
