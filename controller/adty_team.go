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

type adtyTeamKeyResponse struct {
	service.AdtyTeamKeyView
	Username string `json:"username,omitempty"`
}

type adtyTeamAdminDetailResponse struct {
	service.AdtyTeamAdminRow
	Members []service.AdtyTeamMemberView `json:"members"`
	Keys    []adtyTeamKeyResponse        `json:"keys"`
}

type adtyTeamMineResponse struct {
	TeamId       int64                        `json:"team_id"`
	Name         string                       `json:"name"`
	SiteId       int                          `json:"site_id"`
	SourceSiteId int                          `json:"source_site_id"`
	Role         string                       `json:"role"`
	Discount     *decimal.Decimal             `json:"discount,omitempty"`
	SharedQuota  int64                        `json:"shared_quota"`
	Status       string                       `json:"status"`
	ClosedAt     int64                        `json:"closed_at,omitempty"`
	Members      []service.AdtyTeamMemberView `json:"members"`
	Keys         []adtyTeamKeyResponse        `json:"keys"`
}

type adtyTeamLedgerResponse struct {
	model.AdtyTeamLedger
	ActorUsername string `json:"actor_username,omitempty"`
}

type adtyTeamTopupResponse struct {
	model.AdtyTopupOrder
	Username string `json:"username,omitempty"`
}

type adtyTeamInviteResponse struct {
	service.AdtyTeamInviteView
	CreatedByUsername string `json:"created_by_username,omitempty"`
	UsedByUsername    string `json:"used_by_username,omitempty"`
	RevokedByUsername string `json:"revoked_by_username,omitempty"`
}

type adtyTeamRegistrationResponse struct {
	service.AdtyTeamRegistrationView
	CreatedByUsername string `json:"created_by_username,omitempty"`
	UsedByUsername    string `json:"used_by_username,omitempty"`
	RevokedByUsername string `json:"revoked_by_username,omitempty"`
}

func enrichAdtyTeamKeys(keys []service.AdtyTeamKeyView) []adtyTeamKeyResponse {
	out := make([]adtyTeamKeyResponse, len(keys))
	ids := make([]int, 0, len(keys))
	for _, k := range keys {
		if k.UserId > 0 {
			ids = append(ids, k.UserId)
		}
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	for i, k := range keys {
		out[i] = adtyTeamKeyResponse{
			AdtyTeamKeyView: k,
			Username:        names[k.UserId],
		}
	}
	return out
}

func enrichAdtyTeamAdminDetail(detail *service.AdtyTeamAdminDetail) *adtyTeamAdminDetailResponse {
	if detail == nil {
		return nil
	}
	return &adtyTeamAdminDetailResponse{
		AdtyTeamAdminRow: detail.AdtyTeamAdminRow,
		Members:          detail.Members,
		Keys:             enrichAdtyTeamKeys(detail.Keys),
	}
}

func enrichAdtyTeamMine(mine *service.AdtyTeamMine) *adtyTeamMineResponse {
	if mine == nil {
		return nil
	}
	var discount *decimal.Decimal
	if mine.SiteId == 0 && mine.SourceSiteId == 0 {
		d := mine.Discount
		discount = &d
	}
	return &adtyTeamMineResponse{
		TeamId:       mine.TeamId,
		Name:         mine.Name,
		SiteId:       mine.SiteId,
		SourceSiteId: mine.SourceSiteId,
		Role:         mine.Role,
		Discount:     discount,
		SharedQuota:  mine.SharedQuota,
		Status:       mine.Status,
		ClosedAt:     mine.ClosedAt,
		Members:      mine.Members,
		Keys:         enrichAdtyTeamKeys(mine.Keys),
	}
}

func enrichAdtyTeamLedgers(rows []model.AdtyTeamLedger) []adtyTeamLedgerResponse {
	out := make([]adtyTeamLedgerResponse, len(rows))
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		if row.ActorUserId > 0 {
			ids = append(ids, row.ActorUserId)
		}
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	for i, row := range rows {
		out[i] = adtyTeamLedgerResponse{
			AdtyTeamLedger: row,
			ActorUsername:  names[row.ActorUserId],
		}
	}
	return out
}

func enrichAdtyTeamTopups(rows []model.AdtyTopupOrder) []adtyTeamTopupResponse {
	out := make([]adtyTeamTopupResponse, len(rows))
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		if row.UserId > 0 {
			ids = append(ids, row.UserId)
		}
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	for i, row := range rows {
		out[i] = adtyTeamTopupResponse{
			AdtyTopupOrder: row,
			Username:       names[row.UserId],
		}
	}
	return out
}

func enrichAdtyTeamInvites(rows []service.AdtyTeamInviteView) []adtyTeamInviteResponse {
	out := make([]adtyTeamInviteResponse, len(rows))
	ids := make([]int, 0, len(rows)*3)
	for _, row := range rows {
		if row.CreatedBy > 0 {
			ids = append(ids, row.CreatedBy)
		}
		if row.UsedBy > 0 {
			ids = append(ids, row.UsedBy)
		}
		if row.RevokedBy > 0 {
			ids = append(ids, row.RevokedBy)
		}
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	for i, row := range rows {
		out[i] = adtyTeamInviteResponse{
			AdtyTeamInviteView: row,
			CreatedByUsername:  names[row.CreatedBy],
			UsedByUsername:     names[row.UsedBy],
			RevokedByUsername:  names[row.RevokedBy],
		}
	}
	return out
}

func enrichAdtyTeamRegistrations(rows []service.AdtyTeamRegistrationView) []adtyTeamRegistrationResponse {
	out := make([]adtyTeamRegistrationResponse, len(rows))
	ids := make([]int, 0, len(rows)*3)
	for _, row := range rows {
		if row.CreatedBy > 0 {
			ids = append(ids, row.CreatedBy)
		}
		if row.UsedBy > 0 {
			ids = append(ids, row.UsedBy)
		}
		if row.RevokedBy > 0 {
			ids = append(ids, row.RevokedBy)
		}
	}
	names := model.LookupUsernamesByIDs(model.DB, ids)
	for i, row := range rows {
		out[i] = adtyTeamRegistrationResponse{
			AdtyTeamRegistrationView: row,
			CreatedByUsername:        names[row.CreatedBy],
			UsedByUsername:           names[row.UsedBy],
			RevokedByUsername:        names[row.RevokedBy],
		}
	}
	return out
}

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
	common.ApiSuccess(c, enrichAdtyTeamAdminDetail(detail))
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
	common.ApiSuccess(c, gin.H{"total": total, "items": enrichAdtyTeamLedgers(rows)})
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
	common.ApiSuccess(c, enrichAdtyTeamAdminDetail(detail))
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
	common.ApiSuccess(c, gin.H{"total": total, "items": enrichAdtyTeamLedgers(rows)})
}

// PostAdtyOwnerSiteTeamBalance is disabled (R-35 #11).
func PostAdtyOwnerSiteTeamBalance(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Manual team balance top-up is disabled."))
}

// PutAdtyMainTeamAgreementPrice lets Root customize or re-customize the pricing
// multiplier for any active team (main-site: 0 < d <= 10; subsite: 1 <= d <= 10).
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
	amount, err := common.ParseBoundedDecimalString(body.AmountUSD, 9, 0)
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
	common.ApiSuccess(c, gin.H{"items": enrichAdtyTeamTopups(rows)})
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
	common.ApiSuccess(c, gin.H{"team": enrichAdtyTeamMine(mine)})
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
	common.ApiSuccess(c, gin.H{"items": enrichAdtyTeamInvites(rows)})
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
	common.ApiSuccess(c, gin.H{"items": enrichAdtyTeamRegistrations(rows)})
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
	common.ApiSuccess(c, gin.H{"items": enrichAdtyTeamRegistrations(rows)})
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
		if errors.Is(err, model.ErrSiteRegistrationUnavailable) || err.Error() == "Site is not open for registration" {
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Site is not open for registration"))
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, created)
}
