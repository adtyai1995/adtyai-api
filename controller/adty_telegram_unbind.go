package controller

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func revokeUserTelegramBindingsTx(tx *gorm.DB, userID int, purpose string) (map[string]any, map[string]any, string, error) {
	var user model.User
	if err := model.AdtyLockForUpdate(tx).First(&user, userID).Error; err != nil {
		return nil, nil, "", err
	}
	targetPurpose := strings.TrimSpace(purpose)
	explicitPurpose := targetPurpose != ""
	if targetPurpose == "" {
		targetPurpose = model.AdtyTelegramPurposeUser
	}

	var prevTelegramUserID int64
	prevStatus := "unbound"
	if tx.Migrator().HasTable(&model.AdtyTelegramBinding{}) {
		var bindings []model.AdtyTelegramBinding
		query := model.AdtyLockForUpdate(tx).Where("user_id = ?", userID)
		if explicitPurpose {
			query = query.Where("purpose = ?", targetPurpose)
		}
		if err := query.Order("id ASC").Find(&bindings).Error; err != nil {
			return nil, nil, "", err
		}
		if !explicitPurpose && len(bindings) > 1 {
			var filtered []model.AdtyTelegramBinding
			for _, b := range bindings {
				if b.Purpose == model.AdtyTelegramPurposeUser {
					filtered = append(filtered, b)
				}
			}
			if len(filtered) > 0 {
				bindings = filtered
			}
		}
		var bindingIDs []int64
		for _, b := range bindings {
			bindingIDs = append(bindingIDs, b.Id)
			if prevTelegramUserID == 0 && b.TelegramUserId != 0 {
				prevTelegramUserID = b.TelegramUserId
			}
			if b.Status != "" {
				prevStatus = b.Status
			}
			if !explicitPurpose && b.Purpose != "" {
				targetPurpose = b.Purpose
			}
		}
		if len(bindingIDs) > 0 {
			if tx.Migrator().HasTable(&model.AdtyTelegramDelivery{}) {
				if err := tx.Model(&model.AdtyTelegramDelivery{}).
					Where("binding_id IN ? AND status IN ?", bindingIDs, []string{model.AdtyTelegramDeliveryWait, model.AdtyTelegramDeliveryFail}).
					Updates(map[string]any{
						"status":     model.AdtyTelegramDeliverySkip,
						"last_error": "binding revoked",
					}).Error; err != nil {
					return nil, nil, "", err
				}
			}
			if err := tx.Where("id IN ?", bindingIDs).Delete(&model.AdtyTelegramBinding{}).Error; err != nil {
				return nil, nil, "", err
			}
		}
	}

	if tx.Migrator().HasTable(&model.AdtyTelegramChallenge{}) {
		chalQuery := tx.Model(&model.AdtyTelegramChallenge{}).Where("user_id = ? AND used_at = 0", userID)
		if explicitPurpose {
			chalQuery = chalQuery.Where("purpose = ?", targetPurpose)
		}
		if err := chalQuery.Update("used_at", time.Now().Unix()).Error; err != nil {
			return nil, nil, "", err
		}
	}

	prevUserTelegramID := strings.TrimSpace(user.TelegramId)
	if (!explicitPurpose || targetPurpose == model.AdtyTelegramPurposeUser) && prevUserTelegramID != "" {
		if prevStatus == "unbound" {
			prevStatus = model.AdtyTelegramBindActive
		}
		if err := tx.Model(&model.User{}).Where("id = ?", userID).Update("telegram_id", "").Error; err != nil {
			return nil, nil, "", err
		}
	}

	beforeSnapshot := map[string]any{
		"bound":            prevStatus != "unbound" || prevTelegramUserID > 0 || prevUserTelegramID != "",
		"status":           prevStatus,
		"purpose":          targetPurpose,
		"telegram_user_id": prevTelegramUserID,
		"telegram_id":      prevUserTelegramID,
	}
	afterSnapshot := map[string]any{
		"bound":            false,
		"status":           "unbound",
		"purpose":          targetPurpose,
		"telegram_user_id": int64(0),
		"telegram_id":      "",
	}
	return beforeSnapshot, afterSnapshot, targetPurpose, nil
}

func revokeUserTelegramBindings(userID int, purpose string) error {
	if model.DB == nil || userID <= 0 {
		return nil
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		_, _, _, err := revokeUserTelegramBindingsTx(tx, userID, purpose)
		return err
	})
}

// UnbindTelegram revokes the authenticated user's or site owner's Telegram notification binding.
func UnbindTelegram(c *gin.Context) {
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
	if purpose != "" && purpose != model.AdtyTelegramPurposeUser && purpose != model.AdtyTelegramPurposeAdmin {
		common.ApiError(c, service.ErrAdtyTelegramInvalid)
		return
	}

	var resolvedPurpose string
	var auditEvent model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		beforeSnap, afterSnap, targetPurpose, txErr := revokeUserTelegramBindingsTx(tx, userID, purpose)
		if txErr != nil {
			return txErr
		}
		resolvedPurpose = targetPurpose
		params := map[string]any{
			"bindingType": "telegram",
			"purpose":     targetPurpose,
			"success":     true,
			"before":      beforeSnap,
			"after":       afterSnap,
		}
		username := c.GetString("username")
		if username == "" {
			var u model.User
			if tx.Select("username").First(&u, userID).Error == nil {
				username = u.Username
			}
		}
		role := c.GetInt("role")
		if role == 0 {
			role = common.RoleCommonUser
		}
		siteID := service.AdtyRequestSiteID(c)
		if inferred := model.InferOfficialAuditSiteID(c, params); inferred != 0 || params["site_id"] != nil {
			siteID = inferred
		}
		entry, prepErr := model.PrepareOfficialAuditLog(c, model.OfficialAuditWrite{
			UserId:     userID,
			Username:   username,
			ActorRole:  role,
			SiteID:     model.OfficialAuditSiteIDPointer(siteID),
			ObjectType: "user",
			ObjectID:   strconv.Itoa(userID),
			Action:     "user.binding_unbind",
			Category:   model.AuditCategorySecurity,
			Content:    auditContentEN("user.binding_unbind", params),
			Before:     beforeSnap,
			After:      afterSnap,
			Result:     model.AuditResultSuccess,
			Success:    true,
			Status:     http.StatusOK,
			Ip:         c.ClientIP(),
			Other:      model.AuditOther{Op: &model.AuditOperation{Action: "user.binding_unbind", Params: params}},
		})
		if prepErr != nil {
			return prepErr
		}
		auditEvent = entry
		rememberPreparedAuditEvent(c, auditEvent.EventId)
		return model.AppendAuditIntentTx(tx, auditEvent)
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}

	deliverCommittedAudits(c, []model.AuditLog{auditEvent})
	common.ApiSuccess(c, gin.H{
		"bound":            false,
		"status":           "unbound",
		"purpose":          resolvedPurpose,
		"telegram_user_id": int64(0),
		"telegram_id":      "",
		"telegram_chat_id": "",
		"chat_id":          "",
		"username":         "",
	})
}

func PostAdtyTelegramBindUnbind(c *gin.Context) {
	UnbindTelegram(c)
}

func PostAdtyTelegramUnbind(c *gin.Context) {
	UnbindTelegram(c)
}

func DeleteAdtyTelegramMe(c *gin.Context) {
	UnbindTelegram(c)
}

func UnbindAdtyTelegram(c *gin.Context) {
	UnbindTelegram(c)
}

func RevokeAdtyTelegramBinding(c *gin.Context) {
	UnbindTelegram(c)
}

func DeleteTelegramBinding(c *gin.Context) {
	UnbindTelegram(c)
}

// DeleteAdtyTelegramBinding serves DELETE /api/adty/telegram/me and
// DELETE /api/adty/site/telegram. It revokes through the service ledger path
// and must not live at the retired controller/telegram.go path.
func DeleteAdtyTelegramBinding(c *gin.Context) {
	purpose := c.DefaultQuery("purpose", model.AdtyTelegramPurposeUser)
	if strings.Contains(c.FullPath(), "/site/") {
		if _, ok := adtyOwnerSiteOr403(c); !ok {
			return
		}
		purpose = model.AdtyTelegramPurposeAdmin
	}
	audit := prepareManageAudit(c, c.GetInt("id"), "telegram.unbind", map[string]any{"purpose": purpose})
	if err := service.RevokeAdtyTelegramBind(c.GetInt("id"), purpose, &audit); err != nil {
		common.ApiError(c, err)
		return
	}
	deliverCommittedAudits(c, []model.AuditLog{audit})
	common.ApiSuccess(c, gin.H{"purpose": purpose, "status": "revoked", "telegram_user_id": 0})
}
