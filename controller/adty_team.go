package controller

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// GetAdtyMainTeams is Root's team list across every site. site_id narrows it
// to one site (0 is the main site); status is active, closed or empty for all.
func GetAdtyMainTeams(c *gin.Context) {
	page := common.GetPageQuery(c)
	filter := service.AdtyTeamListFilter{Status: c.Query("status"), Query: c.Query("q")}
	if raw := strings.TrimSpace(c.Query("site_id")); raw != "" {
		siteID, err := strconv.Atoi(raw)
		if err != nil || siteID < 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		filter.SiteID = &siteID
	}
	rows, total, err := service.ListAdtyTeamsForAdmin(filter, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"total": total, "items": rows})
}

// GetAdtyAdminTeam is Root's read-only team detail.
func GetAdtyAdminTeam(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	detail, err := service.GetAdtyTeamAdminDetail(id, nil)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, detail)
}

// GetAdtyAdminTeamLedger is Root's page of one team's shared-balance ledger.
func GetAdtyAdminTeamLedger(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	page := common.GetPageQuery(c)
	rows, total, err := service.ListAdtyTeamLedgerForAdmin(id, nil, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"total": total, "items": rows})
}

// GetAdtyOwnerSiteTeam is the owner's read-only detail of a team on its site.
func GetAdtyOwnerSiteTeam(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	detail, err := service.GetAdtyTeamAdminDetail(id, &site.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, detail)
}

// GetAdtyOwnerSiteTeamLedger is the owner's page of a site team's shared-balance ledger.
func GetAdtyOwnerSiteTeamLedger(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	page := common.GetPageQuery(c)
	rows, total, err := service.ListAdtyTeamLedgerForAdmin(id, &site.Id, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"total": total, "items": rows})
}

// PostAdtyOwnerSiteTeamBalance lets an active site owner directly add shared
// balance to an active team on their site (earnings first, then excess margin
// above the site level's required deposit floor). Requires step-up verification.
func PostAdtyOwnerSiteTeamBalance(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	id, ok := adtyInt64Param(c, "id")
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
	if !adtyRequireOwnerProof(c, service.AdtySiteOwnerActionTeamBalance, int(id), service.AdtyOwnerBalanceCreditDigestInput{
		AmountUSD:      amount.String(),
		Reason:         body.Reason,
		IdempotencyKey: idemKey,
	}) {
		return
	}
	res, err := service.CreditAdtySiteTeamBalanceByOwner(site.Id, c.GetInt("id"), id, amount, body.Reason, idemKey)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrAdtyIdempotencyConflict):
			common.ApiErrorStatus(c, http.StatusConflict, err)
		default:
			common.ApiError(c, err)
		}
		return
	}
	// R6-037: Official audit intent is already persisted atomically inside CreditAdtySiteTeamBalanceByOwner.
	common.ApiSuccess(c, res)
}

func PutAdtyMainTeamAgreementPrice(c *gin.Context) {
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
		Discount string `json:"discount"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	eventID, err := service.SaveAdtyMainTeamAgreementPriceWithAudit(id, body.Discount, c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, gin.H{"updated": true, "discount": decimal.RequireFromString(strings.TrimSpace(body.Discount)).String()})
}

func PostAdtyTeamTopup(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		PaymentScene string `json:"payment_scene"`
		AmountUSD    string `json:"amount_usd"`
		Method       string `json:"method"`
		ChannelId    int    `json:"channel_id"` // 易支付：负责人选的通道，0 表示自动挑
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
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	order, err := service.StartAdtyTeamTopupPayment(c.GetInt("id"), id, amount, body.Method, body.ChannelId, key, c.ClientIP(), body.PaymentScene)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, order)
}

func ResumeAdtyTeamTopup(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	order, err := service.ResumeAdtyTeamTopupPayment(c.GetInt("id"), id, c.Param("trade_no"), c.ClientIP())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, order)
}

func GetAdtyTeamTopups(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	rows, err := service.ListAdtyTeamTopups(c.GetInt("id"), id, c.Query("status") == "pending")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": rows})
}

func PostAdtyTeam(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtyTeamForbidden)
}

func PostAdtyMainTeam(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	var body struct {
		Name     string `json:"name"`
		LeaderId int    `json:"leader_id"`
		SiteId   int    `json:"site_id"`
		Discount string `json:"discount"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	created, eventID, replayed, err := service.CreateAdtySiteTeamByRootWithAudit(c.GetInt("id"), body.SiteId, body.LeaderId, body.Name, body.Discount, c.GetHeader("Idempotency-Key"))
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
	common.ApiSuccess(c, created)
}

func PostAdtyOwnerSiteTeam(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtyTeamForbidden)
}

func GetAdtyTeamMine(c *gin.Context) {
	mine, err := service.GetAdtyTeamMine(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"team": mine})
}

func parseAdtyTeamInviteTTL(c *gin.Context) (time.Duration, bool) {
	ttl := service.AdtyTeamInviteDefaultTTL
	if c.Request.Body == nil {
		return ttl, true
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 4096))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return 0, false
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ttl, true
	}
	var body struct {
		TTLSeconds *int64 `json:"ttl_seconds"`
	}
	if err := common.Unmarshal(raw, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return 0, false
	}
	if body.TTLSeconds == nil {
		return ttl, true
	}
	if *body.TTLSeconds <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return 0, false
	}
	return time.Duration(*body.TTLSeconds) * time.Second, true
}

func PostAdtyTeamInvite(c *gin.Context) {
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	ttl, ok := parseAdtyTeamInviteTTL(c)
	if !ok {
		return
	}
	issued, err := service.CreateAdtyTeamInvite(c.GetInt("id"), int64(id), ttl)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, issued)
}

func GetAdtyTeamInvites(c *gin.Context) {
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	rows, err := service.ListAdtyTeamInvites(c.GetInt("id"), int64(id))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": rows})
}

func PostAdtyTeamRevokeInvite(c *gin.Context) {
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	inviteId, err := strconv.ParseInt(c.Param("inviteId"), 10, 64)
	if err != nil || inviteId <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	view, err := service.RevokeAdtyTeamInvite(c.GetInt("id"), int64(id), inviteId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func PostAdtyTeamAccept(c *gin.Context) {
	common.ApiError(c, service.ErrAdtyTeamForbidden)
}

func PostAdtyTeamEmployeeRegistration(c *gin.Context) {
	var body struct {
		Code        string `json:"code"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	siteID := 0
	if site := service.AdtySiteForRequest(c); site != nil {
		if site.Status != model.AdtySiteStatusActive {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		siteID = site.Id
	}
	member, err := service.RegisterAdtyTeamEmployee(body.Code, body.Username, body.Password, body.DisplayName, siteID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, member)
}

func PostAdtyTeamEmployee(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	var body struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	member, err := service.CreateAdtyTeamEmployee(c.GetInt("id"), id, body.Username, body.Password, body.DisplayName)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, member)
}

func PostAdtyTeamEnableMember(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	memberID, err := strconv.Atoi(c.Param("userId"))
	if err != nil || memberID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.EnableAdtyTeamMember(c.GetInt("id"), id, memberID); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"enabled": true})
}

func PostAdtyTeamDeleteMember(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	memberID, err := strconv.Atoi(c.Param("userId"))
	if err != nil || memberID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.DeleteAdtyTeamMember(c.GetInt("id"), id, memberID); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"deleted": true})
}

func PostAdtyTeamEmployeePassword(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	memberID, err := strconv.Atoi(c.Param("userId"))
	if err != nil || memberID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.ResetAdtyTeamEmployeePassword(c.GetInt("id"), id, memberID, body.Password); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"updated": true})
}

func PostAdtyMainTeamRegistration(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	ttl, ok := parseAdtyTeamInviteTTL(c)
	if !ok {
		return
	}
	issued, eventID, err := service.CreateAdtyMainTeamRegistrationWithAudit(c.GetInt("id"), ttl)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, issued)
}

func GetAdtyMainTeamRegistrations(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	rows, err := service.ListAdtyMainTeamRegistrations(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": rows})
}

func PostAdtyMainTeamRegistrationRevoke(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	view, eventID, err := service.RevokeAdtyMainTeamRegistrationWithAudit(c.GetInt("id"), int64(id))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, view)
}

func PostAdtyOwnerSiteTeamRegistration(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtyTeamForbidden)
}

func GetAdtyOwnerSiteTeamRegistrations(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	rows, err := service.ListAdtyOwnerSiteTeamRegistrations(c.GetInt("id"), site.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": rows})
}

func PostAdtyOwnerSiteTeamRegistrationRevoke(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	view, err := service.RevokeAdtyOwnerSiteTeamRegistration(c.GetInt("id"), site.Id, int64(id))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func PostAdtyTeamRegistrationComplete(c *gin.Context) {
	var body struct {
		Code     string `json:"code"`
		Username string `json:"username"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	// R1-021: Never trust a client-supplied site_id; derive from trusted request Host.
	trustedSiteID := 0
	if site := service.AdtySiteForRequest(c); site != nil {
		if site.Status != model.AdtySiteStatusActive {
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Site is not open for registration"))
			return
		}
		trustedSiteID = site.Id
	}
	created, err := service.CompleteAdtyTeamRegistration(body.Code, body.Username, body.Password, body.Name, &trustedSiteID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, created)
}
