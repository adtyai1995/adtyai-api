package controller

import (
	"errors"
	"net/http"
	"strconv"

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
		if err != nil || before <= 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	inbox, err := service.GetAdtyNotificationInbox(c.GetInt("id"), service.AdtySiteForRequest(c), before)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, inbox)
}

func GetAdtyAnnouncement(c *gin.Context) {
	item, ok, err := service.GetAdtyNotificationDetail(c.GetInt("id"), service.AdtySiteForRequest(c), c.Param("key"))
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
	items, err := service.ListAdtyTodosForUser(c.GetInt("id"), includeCompleted)
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
	c.Header("Cache-Control", "no-store")
	page.SetTotal(int(total))
	page.SetItems(items)
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
	if err := common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 65536), &input); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	row, err := service.SaveAdtyNotificationPublication(c.GetInt("id"), issuer, id, input, c.Request.Method == http.MethodDelete)
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
	common.ApiSuccess(c, row)
}

func ReadAdtyNotifications(c *gin.Context) {
	var body struct {
		Keys []string `json:"keys"`
	}
	if err := common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 8192), &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.MarkAdtyNotificationInboxRead(c.GetInt("id"), service.AdtySiteForRequest(c), body.Keys); err != nil {
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
	saved, err := service.SaveAdtyNoticeTemplate(c.GetInt("id"), common.RoleRootUser, c.Param("key"), body.TitleZh, body.ContentZh, body.TitleEn, body.ContentEn)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, saved)
}
