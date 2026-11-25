package controller

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type adtySiteTierInput struct {
	Id                int64                  `json:"id"`
	Code              string                 `json:"code"`
	Name              string                 `json:"name"`
	MinMonthlyUSD     common.BoundedDecimal  `json:"min_monthly_usd"`
	MinEffectiveUsers int64                  `json:"min_effective_users"`
	MinPrincipalUSD   common.BoundedDecimal  `json:"min_principal_usd"`
	MinConsumptionUSD common.BoundedDecimal  `json:"min_consumption_usd"`
	CostFactor        *common.BoundedDecimal `json:"cost_factor"`
	DepositUSD        common.BoundedDecimal  `json:"deposit_usd"`
	Enabled           bool                   `json:"enabled"`
	SortOrder         int                    `json:"sort_order"`
}

func (in adtySiteTierInput) toModel() model.AdtySiteTier {
	out := model.AdtySiteTier{
		Id:                in.Id,
		Code:              in.Code,
		Name:              in.Name,
		MinMonthlyUSD:     in.MinMonthlyUSD.Decimal,
		MinEffectiveUsers: in.MinEffectiveUsers,
		MinPrincipalUSD:   in.MinPrincipalUSD.Decimal,
		MinConsumptionUSD: in.MinConsumptionUSD.Decimal,
		DepositUSD:        in.DepositUSD.Decimal,
		Enabled:           in.Enabled,
		SortOrder:         in.SortOrder,
	}
	if in.CostFactor != nil {
		val := in.CostFactor.Decimal
		out.CostFactor = &val
	}
	return out
}

type adtySiteTierBatchRequest struct {
	Tiers *[]adtySiteTierInput `json:"tiers"`
}

func GetAdtySiteTiers(c *gin.Context) {
	tiers, err := service.ListAdtySiteTiers()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, tiers)
}

func PutAdtySiteTiers(c *gin.Context) {
	var body adtySiteTierBatchRequest
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if body.Tiers == nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	tiers := make([]model.AdtySiteTier, 0, len(*body.Tiers))
	for _, item := range *body.Tiers {
		tiers = append(tiers, item.toModel())
	}
	save := service.SaveAdtySiteTiers
	if c.Request.Method == http.MethodPatch {
		save = service.UpdateAdtySiteTiers
	}
	if c.Request.Method == http.MethodPost {
		save = service.CreateAdtySiteTiers
	}
	if err := save(tiers); err != nil {
		if errors.Is(err, service.ErrAdtySiteTierNotFound) {
			common.ApiErrorT(c, "This level does not exist or is disabled")
			return
		}
		if errors.Is(err, service.ErrAdtySiteTierConflict) {
			common.ApiErrorStatus(c, http.StatusConflict, err)
			return
		}
		if errors.Is(err, service.ErrAdtySiteTierInvalid) {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "adty.site.tiers", map[string]any{"count": len(*body.Tiers)})
	GetAdtySiteTiers(c)
}

// PutAdtySiteTierAssignment lets Root pin one site to an enabled level
// (mode manual) or hand it back to the hourly evaluation (mode auto). The
// margin requirement, cost factor and markup limit follow the level at once.
func PutAdtySiteTierAssignment(c *gin.Context) {
	siteID, ok := siteIdParam(c)
	if !ok {
		return
	}
	var body struct {
		Code string `json:"code"`
		Mode string `json:"mode"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	site, err := service.SetAdtySiteTier(siteID, body.Code, body.Mode, time.Now().Unix())
	switch {
	case errors.Is(err, service.ErrAdtySiteNotFound):
		common.ApiErrorStatus(c, http.StatusNotFound, err)
	case errors.Is(err, service.ErrAdtySiteTierNotFound):
		common.ApiErrorT(c, "This level does not exist or is disabled")
	case errors.Is(err, service.ErrAdtySiteTierMode), errors.Is(err, service.ErrAdtySiteTierInvalid):
		common.ApiErrorT(c, "Invalid parameters")
	case err != nil:
		common.ApiError(c, err)
	default:
		recordManageAudit(c, "adty.site.tier", map[string]any{"site_id": siteID, "code": site.TierCode, "mode": site.TierMode})
		common.ApiSuccess(c, site)
	}
}

type adtySiteTierPrincipalAdjustmentItem struct {
	model.AdtySiteTierPrincipalAdjustment
	OperatorUsername string `json:"operator_username,omitempty"`
}

// GetAdtyAdminSiteTierPrincipal returns the tier-evaluation customer recharge
// breakdown (online customer net recharge + Root manual adjustment = effective
// total) and paged adjustment history for one site.
func GetAdtyAdminSiteTierPrincipal(c *gin.Context) {
	siteID, ok := siteIdParam(c)
	if !ok {
		return
	}
	site, err := service.GetAdtySite(siteID)
	if err != nil {
		if errors.Is(err, service.ErrAdtySiteNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, err)
			return
		}
		common.ApiError(c, err)
		return
	}
	stats, err := service.ComputeAdtySiteTierStats(siteID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo, offset, limit := adtyPage(c)
	rows, total, err := service.ListAdtySiteTierPrincipalAdjustments(siteID, offset, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	opIDs := make([]int, 0, len(rows))
	for _, row := range rows {
		opIDs = append(opIDs, row.OperatorId)
	}
	names := model.LookupUsernamesByIDs(model.DB, opIDs)
	items := make([]adtySiteTierPrincipalAdjustmentItem, len(rows))
	for i, row := range rows {
		items[i] = adtySiteTierPrincipalAdjustmentItem{
			AdtySiteTierPrincipalAdjustment: row,
			OperatorUsername:                names[row.OperatorId],
		}
	}
	common.ApiSuccess(c, gin.H{
		"site_id":                 siteID,
		"tier_code":               site.TierCode,
		"tier_mode":               site.TierMode,
		"online_principal_usd":    stats.OnlinePrincipalUSD.String(),
		"adjustment_usd":          stats.AdjustmentUSD.String(),
		"effective_principal_usd": stats.PrincipalUSD.String(),
		"items":                   items,
		"total":                   total,
		"page":                    pageInfo.GetPage(),
		"page_size":               pageInfo.GetPageSize(),
	})
}

// PostAdtyAdminSiteTierPrincipal lets Root adjust a site's tier-evaluation
// cumulative customer recharge total (delta or set_total) with reason and
// idempotency key, without changing any wallet or customer balance.
func PostAdtyAdminSiteTierPrincipal(c *gin.Context) {
	siteID, ok := siteIdParam(c)
	if !ok {
		return
	}
	var body struct {
		Mode           string `json:"mode"`
		AmountUSD      string `json:"amount_usd"`
		Reason         string `json:"reason"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	amount, err := common.ParseBoundedDecimalString(body.AmountUSD, 12, 8)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	idemKey := strings.TrimSpace(body.IdempotencyKey)
	if hdr := strings.TrimSpace(c.GetHeader("Idempotency-Key")); hdr != "" {
		if idemKey != "" && idemKey != hdr {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		idemKey = hdr
	}
	res, err := service.AdjustAdtySiteTierPrincipal(siteID, c.GetInt("id"), body.Mode, amount, body.Reason, idemKey)
	switch {
	case errors.Is(err, service.ErrAdtySiteNotFound):
		common.ApiErrorStatus(c, http.StatusNotFound, err)
	case errors.Is(err, model.ErrAdtyIdempotencyConflict):
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("This request was already used for another operation"))
	case errors.Is(err, service.ErrAdtySiteTierInvalid):
		common.ApiErrorT(c, "Invalid parameters")
	case err != nil:
		common.ApiError(c, err)
	default:
		if res.Replayed {
			markIdempotentReplayNoAudit(c)
		} else if res.AuditEventID != "" {
			rememberPreparedAuditEvent(c, res.AuditEventID)
			markAuditLogged(c)
		}
		common.ApiSuccess(c, res)
	}
}
