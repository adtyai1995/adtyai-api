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
	"gorm.io/gorm"
)

func GetAdtyNotifications(c *gin.Context) {
	before := 0
	if raw := c.Query("before_id"); raw != "" {
		var err error
		before, err = strconv.Atoi(raw)
		if err != nil || before < -1 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	ticketBefore, err := strconv.ParseInt(c.DefaultQuery("ticket_before", "0"), 10, 64)
	if err != nil || ticketBefore < -1 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if before > 0 && c.Query("ticket_before") == "" {
		ticketBefore = -1
	}
	var tokenScopes [][]string
	if _, isToken := c.Get("access_token_legacy"); isToken {
		tokenScopes = [][]string{c.GetStringSlice("access_token_scopes")}
	}
	inbox, err := service.GetAdtyNotificationInboxWithScopes(c.GetInt("id"), service.AdtySiteForRequest(c), before, ticketBefore, tokenScopes...)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, inbox)
}

func GetAdtyAnnouncement(c *gin.Context) {
	var tokenScopes [][]string
	if _, isToken := c.Get("access_token_legacy"); isToken {
		tokenScopes = [][]string{c.GetStringSlice("access_token_scopes")}
	}
	item, ok, err := service.GetAdtyNotificationDetail(c.GetInt("id"), service.AdtySiteForRequest(c), c.Param("key"), tokenScopes...)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !ok {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Announcement not found"))
		return
	}
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, item)
}

func PostAdtyNotificationHide(c *gin.Context) {
	var body struct {
		Key string `json:"key"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.HideAdtyNotificationForUserFromRequest(c, c.GetInt("id"), body.Key); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Announcement not found"))
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"hidden": true})
}

func PostAdtyNotificationTodoComplete(c *gin.Context) {
	var body struct {
		Key string `json:"key"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if _, err := service.CompleteAdtyTodoFromRequest(c, c.GetInt("id"), body.Key); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Todo item not found"))
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"completed": true})
}

func GetAdtyNotificationTodos(c *gin.Context) {
	includeCompleted := c.Query("include_completed") == "1" || c.Query("include_completed") == "true"
	var tokenScopes [][]string
	if _, isToken := c.Get("access_token_legacy"); isToken {
		tokenScopes = [][]string{c.GetStringSlice("access_token_scopes")}
	}
	items, err := service.ListAdtyTodosForUserOnSite(c.GetInt("id"), service.AdtySiteForRequest(c), includeCompleted, tokenScopes...)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if items == nil {
		items = []service.AdtyTodoItem{}
	}
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, items)
}

func PostAdtyNotificationTodoProgress(c *gin.Context) {
	var body struct {
		Key  string `json:"key"`
		Note string `json:"note"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	item, err := service.MarkAdtyTodoInProgressFromRequest(c, c.GetInt("id"), body.Key, body.Note)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Todo item not found"))
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"status": item.Status, "key": item.Key})
}

// Root and owner routes select the publishing domain on the server. Request
// fields cannot impersonate a publisher or choose another owner's domain.
func adtyNotificationIssuer(c *gin.Context) (*model.AdtySite, bool) {
	if c.GetBool("notification_root") {
		return nil, true
	}
	return adtyOwnerSiteOr403(c)
}

type adtyManagedNotificationDTO struct {
	Id              int      `json:"id"`
	IssuerSiteId    int      `json:"issuer_site_id"`
	PublisherId     int      `json:"publisher_id"`
	PublisherName   string   `json:"publisher_name"`
	Audience        string   `json:"audience"`
	TargetAudiences []string `json:"target_audiences"`
	TargetSiteIDs   []int    `json:"target_site_ids"`
	TargetSiteId    int      `json:"target_site_id"`
	TargetSiteName  string   `json:"target_site_name"`
	Kind            string   `json:"kind"`
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	ContentFormat   string   `json:"content_format"`
	Business        string   `json:"business"`
	Status          string   `json:"status"`
	Revision        int64    `json:"revision"`
	PublishedAt     int64    `json:"published_at"`
	UpdatedAt       int64    `json:"updated_at"`
	UpdatedBy       int      `json:"updated_by"`
	TelegramChannel string   `json:"telegram_channel"`
	TelegramAt      int64    `json:"telegram_at"`
}

func toAdtyManagedNotificationDTO(row model.AdtyNotificationPublication, normalizedContent string) adtyManagedNotificationDTO {
	targetAudiences := row.ParsedTargetAudiences()
	if targetAudiences == nil {
		targetAudiences = []string{}
	}
	targetSiteIDs := row.ParsedTargetSiteIDs()
	if targetSiteIDs == nil {
		targetSiteIDs = []int{}
	}
	contentFormat := strings.ToLower(strings.TrimSpace(row.ContentFormat))
	if contentFormat == "" {
		contentFormat = "markdown"
	}
	return adtyManagedNotificationDTO{
		Id:              row.Id,
		IssuerSiteId:    row.IssuerSiteId,
		PublisherId:     row.PublisherId,
		PublisherName:   row.PublisherName,
		Audience:        row.Audience,
		TargetAudiences: targetAudiences,
		TargetSiteIDs:   targetSiteIDs,
		TargetSiteId:    row.TargetSiteId,
		TargetSiteName:  row.TargetSiteName,
		Kind:            row.Kind,
		Title:           row.Title,
		Content:         normalizedContent,
		ContentFormat:   contentFormat,
		Business:        row.Business,
		Status:          row.Status,
		Revision:        row.Revision,
		PublishedAt:     row.PublishedAt,
		UpdatedAt:       row.UpdatedAt,
		UpdatedBy:       row.UpdatedBy,
		TelegramChannel: row.TelegramChannel,
		TelegramAt:      row.TelegramAt,
	}
}

func GetAdtyManagedNotifications(c *gin.Context) {
	issuer, ok := adtyNotificationIssuer(c)
	if !ok {
		return
	}
	issuerID := 0
	if issuer != nil {
		issuerID = issuer.Id
	}
	page, start, limit := adtyPage(c)
	items, total, err := service.ListAdtyManagedNotifications(issuerID, start, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	normalized := service.NormalizeAdtyNotificationSourceContents(items)
	dtos := make([]adtyManagedNotificationDTO, 0, len(items))
	for _, row := range items {
		dtos = append(dtos, toAdtyManagedNotificationDTO(row, normalized[row.Id]))
	}
	c.Header("Cache-Control", "no-store")
	page.SetTotal(int(total))
	page.SetItems(dtos)
	common.ApiSuccess(c, page)
}

func WriteAdtyManagedNotification(c *gin.Context) {
	issuer, ok := adtyNotificationIssuer(c)
	if !ok {
		return
	}
	id := 0
	if c.Request.Method != http.MethodPost {
		id, ok = adtyPathId(c)
		if !ok {
			return
		}
	}
	var input service.AdtyNotificationInput
	if err := common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 98304), &input); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	row, eventID, err := service.SaveAdtyNotificationPublicationWithAudit(c.GetInt("id"), issuer, id, input, c.Request.Method == http.MethodDelete)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Not found"))
		return
	}
	if errors.Is(err, model.ErrAdtyNotificationConflict) {
		common.ApiErrorStatus(c, http.StatusConflict, err)
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	if row == nil {
		common.ApiSuccess(c, nil)
		return
	}
	dto := toAdtyManagedNotificationDTO(*row, service.NormalizeAdtyNotificationSourceContent(row.Id, row.Content))
	common.ApiSuccess(c, dto)
}

func ReadAdtyNotifications(c *gin.Context) {
	var body struct {
		Keys []string `json:"keys"`
	}
	if err := common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 8192), &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var tokenScopes [][]string
	if _, isToken := c.Get("access_token_legacy"); isToken {
		tokenScopes = [][]string{c.GetStringSlice("access_token_scopes")}
	}
	if err := service.MarkAdtyNotificationInboxRead(c.GetInt("id"), service.AdtySiteForRequest(c), body.Keys, tokenScopes...); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func GetAdtyNoticeTemplates(c *gin.Context) {
	rows, err := service.ListAdtyNoticeTemplates()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

func PutAdtyNoticeTemplate(c *gin.Context) {
	var body struct {
		TitleZh   string `json:"title_zh"`
		ContentZh string `json:"content_zh"`
		TitleEn   string `json:"title_en"`
		ContentEn string `json:"content_en"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	saved, eventID, err := service.SaveAdtyNoticeTemplateWithAudit(c.GetInt("id"), common.RoleRootUser, c.Param("key"), body.TitleZh, body.ContentZh, body.TitleEn, body.ContentEn)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if eventID != "" {
		rememberPreparedAuditEvent(c, eventID)
		markAuditLogged(c)
	}
	common.ApiSuccess(c, saved)
}
