package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func adtyInt64Param(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return 0, false
	}
	return id, true
}

func PostAdtyTeamClose(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	if err := service.CloseAdtyTeam(c.GetInt("id"), id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"closed": true})
}

func PostAdtyTeamProfile(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.UpdateAdtyTeamProfile(c.GetInt("id"), id, body.Name); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"updated": true})
}

func PostAdtyTeamLeave(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	if err := service.LeaveAdtyTeam(c.GetInt("id"), id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"left": true})
}

func PostAdtyTeamDisableMember(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	memberID, err := strconv.Atoi(c.Param("userId"))
	if err != nil || memberID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.DisableAdtyTeamMember(c.GetInt("id"), id, memberID); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"disabled": true})
}

func PostAdtyTeamBindKey(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	var body struct {
		TokenId int `json:"token_id"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil || body.TokenId <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.BindAdtyTeamKey(c.GetInt("id"), body.TokenId, id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"bound": true})
}

func PostAdtyTeamRevokeKey(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	tokenID, err := strconv.Atoi(c.Param("tokenId"))
	if err != nil || tokenID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.RevokeAdtyTeamKey(c.GetInt("id"), id, tokenID); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"revoked": true})
}

func PostAdtyTeamTransfer(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	var body struct {
		UserId int `json:"user_id"`
	}
	_ = common.DecodeJson(c.Request.Body, &body)
	if err := service.TransferAdtyTeamLeader(c.GetInt("id"), id, body.UserId); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"transferred": false})
}

// adtyRequireTelegramAdminAccess enforces admin/site-owner eligibility and
// prevents personal profile:read/write PATs from accessing or mutating the
// Admin Operations Telegram Bot via purpose=admin on /api/adty/telegram/* (R10-005).
func adtyRequireTelegramAdminAccess(c *gin.Context, write bool) bool {
	if err := service.CheckAdtyTelegramAdminAccess(c.GetInt("id")); err != nil {
		common.ApiErrorStatus(c, http.StatusForbidden, err)
		return false
	}
	if !isAccessTokenRequest(c) {
		return true
	}
	if c.GetBool("access_token_legacy") {
		common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtyTelegramForbidden)
		return false
	}
	scopes := c.GetStringSlice("access_token_scopes")
	allowed := []string{"site_owner_setting:write", "adty_ticket:manage", "adty_ticket:manage_all", "option:write"}
	if !write {
		allowed = append(allowed, "site_owner_setting:read", "option:read")
	}
	for _, s := range scopes {
		for _, want := range allowed {
			if s == want {
				return true
			}
		}
	}
	common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtyTelegramForbidden)
	return false
}

func GetAdtyTelegramMe(c *gin.Context) {
	purpose := c.DefaultQuery("purpose", model.AdtyTelegramPurposeUser)
	if c.FullPath() != "" && strings.Contains(c.FullPath(), "/site/") {
		if _, ok := adtyOwnerSiteOr403(c); !ok {
			return
		}
		purpose = model.AdtyTelegramPurposeAdmin
	} else if purpose == model.AdtyTelegramPurposeAdmin {
		if !adtyRequireTelegramAdminAccess(c, false) {
			return
		}
	}
	view, err := service.GetAdtyTelegramBind(c.GetInt("id"), purpose)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func PostAdtyTelegramBindStart(c *gin.Context) {
	purpose := model.AdtyTelegramPurposeUser
	if strings.Contains(c.FullPath(), "/site/") {
		if _, ok := adtyOwnerSiteOr403(c); !ok {
			return
		}
		purpose = model.AdtyTelegramPurposeAdmin
	}
	if !strings.Contains(c.FullPath(), "/site/") && c.Query("purpose") == model.AdtyTelegramPurposeAdmin {
		if !adtyRequireTelegramAdminAccess(c, true) {
			return
		}
		purpose = model.AdtyTelegramPurposeAdmin
	}
	code, username, expires, err := service.StartAdtyTelegramBind(c.GetInt("id"), purpose)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"code": code, "bot_username": username, "expires_at": expires})
}

func PostAdtyTelegramBindConfirm(c *gin.Context) {
	var body struct {
		Purpose        string `json:"purpose"`
		Code           string `json:"code"`
		TelegramUserId int64  `json:"telegram_user_id"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if strings.Contains(c.FullPath(), "/site/") {
		if _, ok := adtyOwnerSiteOr403(c); !ok {
			return
		}
		body.Purpose = model.AdtyTelegramPurposeAdmin
	}
	if body.Purpose == "" {
		body.Purpose = model.AdtyTelegramPurposeUser
	}
	if !strings.Contains(c.FullPath(), "/site/") && body.Purpose == model.AdtyTelegramPurposeAdmin {
		if !adtyRequireTelegramAdminAccess(c, true) {
			return
		}
	}
	binding, err := service.ConfirmAdtyTelegramBind(c.GetInt("id"), body.Purpose, body.Code, body.TelegramUserId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"status": binding.Status, "telegram_user_id": binding.TelegramUserId})
}

func PostAdtyTelegramWebhook(c *gin.Context) {
	purpose := c.Param("purpose")
	secret := c.Param("secret")
	if err := service.VerifyAdtyTelegramWebhook(purpose, secret); err != nil {
		c.Status(http.StatusForbidden)
		return
	}
	var update service.AdtyTelegramUpdate
	if err := common.DecodeJson(io.LimitReader(c.Request.Body, 1<<20), &update); err != nil {
		c.Status(http.StatusForbidden)
		return
	}
	reply, err := service.HandleAdtyTelegramUpdate(purpose, secret, update)
	if err != nil {
		c.Status(http.StatusForbidden)
		return
	}
	chatID := int64(0)
	if update.Message != nil {
		chatID = update.Message.Chat.Id
	}
	if chatID == 0 || strings.TrimSpace(reply) == "" {
		c.JSON(http.StatusOK, gin.H{})
		return
	}
	c.JSON(http.StatusOK, gin.H{"method": "sendMessage", "chat_id": chatID, "text": reply})
}

func GetAdtyAdminTelegram(c *gin.Context) {
	bots, err := service.ListAdtyTelegramBots()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"bots": bots})
}

func PutAdtyAdminTelegram(c *gin.Context) {
	purpose := c.Param("purpose")
	var body struct {
		Token   string `json:"token"`
		Enabled bool   `json:"enabled"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if middleware.RequireSecurityProof(c, service.AdtyNoticeOperation(service.AdtyNoticeActionTelegramSave, purpose)) == nil {
		return
	}
	event := prepareManageAudit(c, c.GetInt("id"), "adty.telegram.save", map[string]any{"purpose": purpose, "enabled": body.Enabled, "token_changed": body.Token != ""})
	saved, err := service.SaveAdtyTelegramBot(purpose, body.Token, body.Enabled, event)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	deliverCommittedAudits(c, []model.AuditLog{event})
	common.ApiSuccess(c, saved)
}

func PostAdtyAdminTelegramTest(c *gin.Context) {
	purpose := c.Param("purpose")
	if middleware.RequireSecurityProof(c, service.AdtyNoticeOperation(service.AdtyNoticeActionTelegramTest, purpose)) == nil {
		return
	}
	if err := service.TestAdtyTelegramBot(purpose, c.GetInt("id")); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"tested": true})
}

// Browser requests use the selected workspace. Scoped dashboard tokens also
// need its management grant, in addition to the route's profile read/write scope.
func adtyRequestedTicketScope(c *gin.Context) string {
	scope := c.Query("scope")
	if _, token := c.Get("access_token_legacy"); token {
		return service.AdtyTicketTokenScope(scope, c.Request.Method, c.GetStringSlice("access_token_scopes"))
	}
	return scope
}

func GetAdtyTicketRequestAudience(c *gin.Context) {
	audience, err := service.AdtyTicketRequestAudience(c.GetInt("id"), c.Query("ref"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"audience": audience})
}

func GetAdtyTickets(c *gin.Context) {
	if kind := c.Query("related_kind"); kind != "" {
		tickets, err := service.RelatedOpenAdtyTickets(c.GetInt("id"), adtyRequestedTicketScope(c), kind, c.Query("related_ref"))
		if err != nil {
			common.ApiError(c, err)
			return
		}
		common.ApiSuccess(c, gin.H{"tickets": tickets})
		return
	}
	page := common.GetPageQuery(c)
	before, err := strconv.ParseInt(c.DefaultQuery("before_id", "0"), 10, 64)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	list, err := service.SearchAdtyTicketPage(c.GetInt("id"), c.Query("queue"), c.Query("q"), before, page.Page, page.PageSize, adtyRequestedTicketScope(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, list)
}

func PostAdtyTicket(c *gin.Context) {
	var body service.AdtyTicketInput
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	body.Idempotency = c.GetHeader("Idempotency-Key")
	ticket, err := service.CreateAdtyTicket(c.GetInt("id"), body)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, ticket)
}

const AdtyTicketImageGrantTTL = 5 * time.Minute

type adtyTicketDetailResponse struct {
	*service.AdtyTicketDetail
	ImageGrantsExp int64            `json:"image_grants_exp,omitempty"`
	ImageGrants    map[int64]string `json:"image_grants,omitempty"`
}

func adtyTicketImageGrantSecret() []byte {
	return []byte(strings.TrimSpace(common.CryptoSecret))
}

func adtyTicketImageGrantMAC(userId int, ticketId, imageId, exp int64) string {
	mac := hmac.New(sha256.New, adtyTicketImageGrantSecret())
	_, _ = fmt.Fprintf(mac, "adty-ticket-image\x00%d\x00%d\x00%d\x00%d", userId, ticketId, imageId, exp)
	return hex.EncodeToString(mac.Sum(nil))
}

func IssueAdtyTicketImageGrant(userId int, ticketId, imageId, exp int64) (string, error) {
	now := time.Now().Unix()
	if userId <= 0 || ticketId <= 0 || imageId <= 0 || exp <= now || exp-now > int64(AdtyTicketImageGrantTTL/time.Second) || len(adtyTicketImageGrantSecret()) == 0 {
		return "", service.ErrAdtyTicketForbidden
	}
	sig := adtyTicketImageGrantMAC(userId, ticketId, imageId, exp)
	return strconv.FormatInt(exp, 10) + "." + sig, nil
}

func VerifyAdtyTicketImageGrant(userId int, ticketId, imageId int64, grant string, now int64) bool {
	if userId <= 0 || ticketId <= 0 || imageId <= 0 || len(grant) > 100 || len(adtyTicketImageGrantSecret()) == 0 {
		return false
	}
	expStr, sigStr, ok := strings.Cut(grant, ".")
	if !ok || len(sigStr) != sha256.Size*2 {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || exp <= now || exp-now > int64(AdtyTicketImageGrantTTL/time.Second) {
		return false
	}
	expected := adtyTicketImageGrantMAC(userId, ticketId, imageId, exp)
	return hmac.Equal([]byte(sigStr), []byte(expected))
}

func GetAdtyTicket(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	userID := c.GetInt("id")
	detail, err := service.GetAdtyTicket(userID, id, adtyRequestedTicketScope(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	exp := time.Now().Add(AdtyTicketImageGrantTTL).Unix()
	var grants map[int64]string
	if len(detail.Images) > 0 {
		grants = make(map[int64]string, len(detail.Images))
		for _, img := range detail.Images {
			if grant, err := IssueAdtyTicketImageGrant(userID, id, img.Id, exp); err == nil {
				grants[img.Id] = grant
			}
		}
	}
	common.ApiSuccess(c, adtyTicketDetailResponse{
		AdtyTicketDetail: detail,
		ImageGrants:      grants,
		ImageGrantsExp:   exp,
	})
}

func PostAdtyTicketReply(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	var body struct {
		Body string `json:"body"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	message, err := service.ReplyAdtyTicket(int64(c.GetInt("id")), id, body.Body, c.GetHeader("Idempotency-Key"), adtyRequestedTicketScope(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, message)
}

func PostAdtyTicketNote(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	var body struct {
		Body string `json:"body"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	message, err := service.NoteAdtyTicket(c.GetInt("id"), id, body.Body, c.GetHeader("Idempotency-Key"), adtyRequestedTicketScope(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, message)
}

func PostAdtyTicketStatus(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	var body struct {
		Status         string `json:"status"`
		Priority       string `json:"priority"`
		ClosureEventId int64  `json:"closure_event_id"`
		AssigneeId     int    `json:"assignee_id"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.SetAdtyTicketStatusWithKey(c.GetInt("id"), id, body.Status, body.Priority, body.AssigneeId, c.GetHeader("Idempotency-Key"), body.ClosureEventId, adtyRequestedTicketScope(c)); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"updated": true})
}

func PostAdtyTicketImage(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	var body struct {
		Data      string `json:"data"`
		MessageID int64  `json:"message_id"`
		// Visibility has no default: an upload must say whether the image
		// belongs to a public reply or an internal note.
		Visibility string `json:"visibility"`
	}
	// Base64 expansion and the metadata envelope use the same byte policy
	// reported to the frontend. Limit the stream before reading the JSON.
	limit := base64.StdEncoding.EncodedLen(service.AdtyTicketImageMaxBytes()) + 1024
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, int64(limit))
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	raw, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		common.ApiErrorT(c, "Only images are accepted")
		return
	}
	userID := c.GetInt("id")
	image, err := service.AttachAdtyTicketImageWithKey(userID, id, body.MessageID, body.Visibility, raw, c.GetHeader("Idempotency-Key"), adtyRequestedTicketScope(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	exp := time.Now().Add(AdtyTicketImageGrantTTL).Unix()
	grant, err := IssueAdtyTicketImageGrant(userID, id, image.Id, exp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"id":        image.Id,
		"object_id": image.ObjectId,
		"mime":      image.Mime,
		"width":     image.Width,
		"height":    image.Height,
		"grant":     grant,
		"grant_exp": exp,
	})
}

func GetAdtyTicketImage(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	imageID, ok := adtyInt64Param(c, "imageId")
	if !ok {
		return
	}
	userID := c.GetInt("id")
	now := time.Now()
	if c.Query("action") == "grant" {
		if _, err := service.AuthorizedAdtyTicketImage(userID, id, imageID, adtyRequestedTicketScope(c)); err != nil {
			common.ApiError(c, err)
			return
		}
		exp := now.Add(AdtyTicketImageGrantTTL).Unix()
		grant, err := IssueAdtyTicketImageGrant(userID, id, imageID, exp)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		common.ApiSuccess(c, gin.H{
			"grant":      grant,
			"expires_at": exp,
		})
		return
	}

	if c.Query("action") != "" || !VerifyAdtyTicketImageGrant(userID, id, imageID, c.GetHeader("X-Ticket-Image-Grant"), now.Unix()) {
		common.ApiError(c, service.ErrAdtyTicketForbidden)
		return
	}
	path, mime, err := service.OpenAdtyTicketImage(userID, id, imageID, adtyRequestedTicketScope(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Type", mime)
	c.File(path)
}

func DeleteAdtyTicketImage(c *gin.Context) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	imageID, ok := adtyInt64Param(c, "imageId")
	if !ok {
		return
	}
	if err := service.DeleteAdtyTicketImage(c.GetInt("id"), id, imageID, adtyRequestedTicketScope(c)); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"deleted": true})
}

func GetAdtyTranslation(c *gin.Context) {
	view, err := service.GetAdtyTranslationConfig()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func PutAdtyTranslation(c *gin.Context) {
	var body service.AdtyTranslationInput
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if middleware.RequireSecurityProof(c, service.AdtyNoticeOperation(service.AdtyNoticeActionTranslation, body.Mode)) == nil {
		return
	}
	body.RequestHost = c.Request.Host
	event := prepareManageAudit(c, c.GetInt("id"), "adty.translation.save", map[string]any{"mode": body.Mode, "model": body.ModelName})
	view, err := service.SaveAdtyTranslationConfig(body, event)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	deliverCommittedAudits(c, []model.AuditLog{event})
	common.ApiSuccess(c, view)
}

func PostAdtyTranslationTest(c *gin.Context) {
	if middleware.RequireSecurityProof(c, service.AdtyNoticeOperation(service.AdtyNoticeActionTranslateTry, "translation")) == nil {
		return
	}
	view, err := service.TestAdtyTranslation(c.Request.Host)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func PostAdtyTranslationRun(c *gin.Context) {
	var body struct {
		ObjectKey string `json:"object_key"`
		Source    string `json:"source"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	result, err := service.TranslateAdtyDynamic(body.ObjectKey, body.Source, 0)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func GetAdtyTranslationUsage(c *gin.Context) {
	usage, err := service.AdtyTranslationUsageToday()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, usage)
}

func GetAdtyGroupHealth(c *gin.Context) {
	usable := service.GetUserUsableGroups(c.GetString("group"))
	allowed := make([]string, 0, len(usable))
	for group := range usable {
		allowed = append(allowed, group)
	}
	rows, err := service.ListAdtyGroupHealthForUserOnSite(c.GetInt("id"), service.AdtyRequestSiteID(c), c.GetString("group"), c.Query("model"), allowed, time.Now())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	scope := "platform"
	if len(rows) > 0 {
		scope = rows[0].Scope
	}
	common.ApiSuccess(c, gin.H{"scope": scope, "groups": rows})
}

func GetAdtyAdminGroupHealth(c *gin.Context) {
	view, err := service.GetAdtyGroupHealthConfig()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func PutAdtyGroupHealth(c *gin.Context) {
	var body struct {
		SuccessMin   string `json:"success_min"`
		StaleSeconds int    `json:"stale_seconds"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	event := prepareManageAudit(c, c.GetInt("id"), "adty.group_health.config", map[string]any{"success_min": body.SuccessMin, "stale_seconds": body.StaleSeconds})
	if err := service.PutAdtyGroupHealthConfig(body.SuccessMin, body.StaleSeconds, event); err != nil {
		common.ApiError(c, err)
		return
	}
	deliverCommittedAudits(c, []model.AuditLog{event})
	common.ApiSuccess(c, gin.H{"saved": true})
}

func PostAdtyGroupProbe(c *gin.Context) {
	var body struct {
		SiteId  int    `json:"site_id"`
		Group   string `json:"group"`
		Model   string `json:"model"`
		Ok      *bool  `json:"ok"`
		Execute bool   `json:"execute"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if body.Execute || body.Ok == nil {
		sample, err := service.ExecuteAdtyGroupProbe(body.SiteId, body.Group, body.Model, time.Now())
		if err != nil {
			common.ApiError(c, err)
			return
		}
		common.ApiSuccess(c, gin.H{"recorded": true, "sample": sample})
		return
	}
	if err := service.RecordAdtyGroupProbe(body.SiteId, body.Group, body.Model, *body.Ok, time.Now()); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"recorded": true})
}

func PutAdtyTicketImagePolicy(c *gin.Context) {
	var policy model.AdtyTicketPolicy
	if err := common.DecodeJson(c.Request.Body, &policy); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	audit := prepareManageAudit(c, c.GetInt("id"), "adty.ticket.image_policy.update", map[string]any{
		"max_bytes":  policy.MaxBytes,
		"max_count":  policy.MaxCount,
		"max_total":  policy.MaxTotal,
		"max_side":   policy.MaxSide,
		"max_pixels": policy.MaxPixels,
	})
	if err := service.SaveAdtyTicketImagePolicy(policy, audit); err != nil {
		common.ApiError(c, err)
		return
	}
	deliverCommittedAudits(c, []model.AuditLog{audit})
	common.ApiSuccess(c, gin.H{"updated": true})
}
