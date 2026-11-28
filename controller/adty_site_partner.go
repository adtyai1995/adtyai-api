package controller

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// Partner referral (R-49) public, site-owner, and Root admin HTTP handlers.

func parseOptionalFlexibleDecimal(raw common.RawMessage) (*decimal.Decimal, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var str string
		if err := common.Unmarshal(raw, &str); err != nil {
			return nil, err
		}
		str = strings.TrimSpace(str)
		if str == "" {
			return nil, nil
		}
		val, err := common.ParseBoundedDecimalString(str, 6, 6)
		if err != nil {
			return nil, err
		}
		return &val, nil
	}
	val, err := common.ParseBoundedDecimalString(trimmed, 6, 6)
	if err != nil {
		return nil, err
	}
	return &val, nil
}

// GetAdtyPartnerPublic exposes main-site-only public config for the /partner landing page.
func GetAdtyPartnerPublic(c *gin.Context) {
	if service.AdtySiteForRequest(c) != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Not found"})
		return
	}
	cfg := adty_setting.GetResellerSetting()
	common.OptionMapRWMutex.RLock()
	turnstileCheck := common.TurnstileCheckEnabled
	turnstileSiteKey := common.TurnstileSiteKey
	common.OptionMapRWMutex.RUnlock()

	hasRefCookie := false
	if code := service.ReadAdtyPartnerCookieCode(c); code != "" {
		if site, err := service.ValidateActivePartnerCode(code); err == nil && site != nil {
			hasRefCookie = true
		}
	}
	common.ApiSuccess(c, gin.H{
		"enabled":              cfg.Ready(),
		"default_rate_percent": service.AdtyPartnerDefaultRateFloat(),
		"turnstile_check":      turnstileCheck,
		"turnstile_site_key":   turnstileSiteKey,
		"has_ref_cookie":       hasRefCookie,
	})
}

// PostAdtyPartnerClick validates an active partner code on the main site and sets
// the signed HttpOnly adty_partner_ref cookie.
func PostAdtyPartnerClick(c *gin.Context) {
	if service.AdtySiteForRequest(c) != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Not found"})
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	site, err := service.ValidateActivePartnerCode(body.Code)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	code := strings.ToUpper(strings.TrimSpace(body.Code))
	if site.PartnerCode != nil && strings.TrimSpace(*site.PartnerCode) != "" {
		code = strings.TrimSpace(*site.PartnerCode)
	}
	if err := service.SetAdtyPartnerCookie(c, code); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"ok": true})
}

// PostAdtyPartnerApply submits a main-site partner inquiry lead and clears the
// signed referral cookie upon success.
func PostAdtyPartnerApply(c *gin.Context) {
	if service.AdtySiteForRequest(c) != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Not found"})
		return
	}
	var body service.AdtyPartnerLeadInput
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	cookieCode := service.ReadAdtyPartnerCookieCode(c)
	lead, contactTelegram, err := service.SubmitAdtyPartnerLead(body, c.ClientIP(), cookieCode)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	service.ClearAdtyPartnerCookie(c)
	common.ApiSuccess(c, gin.H{
		"id":               lead.Id,
		"contact_telegram": contactTelegram,
	})
}

// GetAdtyOwnerPartnerOverview returns partner referral summary for the current site owner.
func GetAdtyOwnerPartnerOverview(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	overview, err := service.GetAdtyOwnerPartnerOverview(site)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, overview)
}

// GetAdtyOwnerPartnerSites returns anonymized referred subsites for the current site owner.
func GetAdtyOwnerPartnerSites(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	pageInfo := common.GetPageQuery(c)
	items, total, err := service.ListAdtyOwnerPartnerSites(site.Id, pageInfo.GetPage(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// GetAdtyOwnerPartnerEarnings returns anonymized hourly partner referral earnings for the current site owner.
func GetAdtyOwnerPartnerEarnings(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	pageInfo := common.GetPageQuery(c)
	items, total, err := service.ListAdtyOwnerPartnerEarnings(site.Id, pageInfo.GetPage(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// GetAdtyAdminPartnerLeads lists partner leads for Root review.
func GetAdtyAdminPartnerLeads(c *gin.Context) {
	if c.GetInt("role") < common.RoleRootUser {
		denyAdtyAdminAction(c)
		return
	}
	pageInfo := common.GetPageQuery(c)
	items, total, err := service.ListAdtyPartnerLeads(strings.TrimSpace(c.Query("status")), pageInfo.GetPage(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// PostAdtyAdminPartnerLeadReject rejects a pending partner lead (Root only).
func PostAdtyAdminPartnerLeadReject(c *gin.Context) {
	if c.GetInt("role") < common.RoleRootUser {
		denyAdtyAdminAction(c)
		return
	}
	leadID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || leadID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		ReviewNote string `json:"review_note"`
		Note       string `json:"note"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil && !errors.Is(err, io.EOF) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	reviewNote := body.ReviewNote
	if strings.TrimSpace(reviewNote) == "" {
		reviewNote = body.Note
	}
	lead, eventID, err := service.RejectAdtyPartnerLeadWithAudit(leadID, c.GetInt("id"), reviewNote)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, lead)
}

// PutAdtyAdminSitePartnerReferral manages late-bind, rate adjustment, or stop
// of a site's partner referral relation (Root only).
func PutAdtyAdminSitePartnerReferral(c *gin.Context) {
	if c.GetInt("role") < common.RoleRootUser {
		denyAdtyAdminAction(c)
		return
	}
	id, ok := siteIdParam(c)
	if !ok {
		return
	}
	var body struct {
		BindReferrerSiteId int               `json:"bind_referrer_site_id"`
		BindReferrerCode   string            `json:"bind_referrer_code"`
		RatePercent        common.RawMessage `json:"rate_percent"`
		Stop               bool              `json:"stop"`
		Reason             string            `json:"reason"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	ratePercent, err := parseOptionalFlexibleDecimal(body.RatePercent)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	site, eventID, err := service.ManageAdtySitePartnerReferralWithAudit(id, c.GetInt("id"), service.AdtySitePartnerManageInput{
		BindReferrerSiteId: body.BindReferrerSiteId,
		BindReferrerCode:   body.BindReferrerCode,
		RatePercent:        ratePercent,
		Stop:               body.Stop,
		Reason:             body.Reason,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	views, err := buildSiteAdminViews([]model.AdtySite{*site})
	if err != nil || len(views) == 0 {
		common.ApiSuccess(c, site)
		return
	}
	common.ApiSuccess(c, views[0])
}

// PostAdtyAdminHourlySettlementRun triggers an immediate hourly settlement run (Root only).
func PostAdtyAdminHourlySettlementRun(c *gin.Context) {
	if c.GetInt("role") < common.RoleRootUser {
		denyAdtyAdminAction(c)
		return
	}
	summary, err := service.RunAdtyHourlySettlement(time.Now())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "adty.hourly_settlement.run", map[string]any{"triggered_by": c.GetInt("id")})
	common.ApiSuccess(c, summary)
}
