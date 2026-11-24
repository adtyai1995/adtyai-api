package controller

import (
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// DeleteAdtyTelegramBinding serves all user and site-owner Telegram unbind
// endpoints through the single service.RevokeAdtyTelegramBind ledger path.
// It must not live at the retired controller/telegram.go path and must never
// silently downgrade an explicit admin unbind request into a user unbind.
func DeleteAdtyTelegramBinding(c *gin.Context) {
	userID := c.GetInt("id")
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, common.NewMessage("Unauthorized"))
		return
	}

	var body struct {
		Purpose string `json:"purpose"`
	}
	if c.Request != nil && c.Request.Body != nil {
		if err := common.DecodeJson(c.Request.Body, &body); err != nil && err != io.EOF {
			if !strings.Contains(strings.ToLower(err.Error()), "eof") {
				common.ApiErrorT(c, "Invalid parameters")
				return
			}
		}
	}

	purpose := strings.TrimSpace(body.Purpose)
	if purpose == "" {
		purpose = strings.TrimSpace(c.Query("purpose"))
	}
	path := c.FullPath()
	if path == "" && c.Request != nil && c.Request.URL != nil {
		path = c.Request.URL.Path
	}
	if strings.Contains(path, "/site/") {
		if _, ok := adtyOwnerSiteOr403(c); !ok {
			return
		}
		purpose = model.AdtyTelegramPurposeAdmin
	}
	if purpose == "" {
		purpose = model.AdtyTelegramPurposeUser
	}
	if purpose != model.AdtyTelegramPurposeUser && purpose != model.AdtyTelegramPurposeAdmin {
		common.ApiError(c, service.ErrAdtyTelegramInvalid)
		return
	}
	if !strings.Contains(path, "/site/") && purpose == model.AdtyTelegramPurposeAdmin {
		if !adtyRequireTelegramAdminAccess(c, true) {
			return
		}
	}

	audit := prepareManageAudit(c, userID, "telegram.unbind", map[string]any{"purpose": purpose})
	if err := service.RevokeAdtyTelegramBind(userID, purpose, &audit); err != nil {
		common.ApiError(c, err)
		return
	}
	deliverCommittedAudits(c, []model.AuditLog{audit})
	common.ApiSuccess(c, gin.H{
		"bound":            false,
		"purpose":          purpose,
		"status":           "revoked",
		"telegram_user_id": int64(0),
		"telegram_id":      "",
	})
}
