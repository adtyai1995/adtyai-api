package service

import (
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// RevokeAdtyTelegramBind serializes with confirmation on the same user row.
func RevokeAdtyTelegramBind(userID int, purpose string, audit *model.AuditLog) error {
	if userID <= 0 || !adtyValidTelegramPurpose(purpose) || audit == nil {
		return ErrAdtyTelegramInvalid
	}
	var (
		wasActive          bool
		prevTelegramUserID int64
		prevBotIdentity    string
		prevSiteID         int
	)
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := model.AdtyLockForUpdate(tx).First(&user, userID).Error; err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled {
			return ErrAdtyTelegramForbidden
		}
		if user.StaffSiteId > 0 && purpose != model.AdtyTelegramPurposeAdmin {
			return ErrAdtyTelegramForbidden
		}
		prevSiteID = user.SiteId
		if user.StaffSiteId > 0 {
			prevSiteID = user.StaffSiteId
		}
		var row model.AdtyTelegramBinding
		before := map[string]any{"purpose": purpose, "status": "unbound"}
		if tx.Migrator().HasTable(&model.AdtyTelegramBinding{}) {
			found := model.AdtyLockForUpdate(tx).Where("user_id = ? AND purpose = ?", userID, purpose).Limit(1).Find(&row)
			if found.Error != nil {
				return found.Error
			}
			if found.RowsAffected > 0 {
				before["status"] = row.Status
				if row.Status == model.AdtyTelegramBindActive && row.TelegramUserId > 0 {
					wasActive = true
					prevTelegramUserID = row.TelegramUserId
					prevBotIdentity = row.BotIdentity
				}
				if err := tx.Model(&row).Update("status", "revoked").Error; err != nil {
					return err
				}
			}
		}
		if tx.Migrator().HasTable(&model.AdtyTelegramChallenge{}) {
			if err := tx.Model(&model.AdtyTelegramChallenge{}).Where("user_id = ? AND purpose = ? AND used_at = 0", userID, purpose).Update("used_at", time.Now().Unix()).Error; err != nil {
				return err
			}
		}
		if tx.Migrator().HasTable(&model.AdtyTelegramDelivery{}) {
			if err := tx.Model(&model.AdtyTelegramDelivery{}).Where("user_id = ? AND purpose = ? AND public = ? AND status IN ?", userID, purpose, false, []string{model.AdtyTelegramDeliveryWait, model.AdtyTelegramDeliveryFail}).Updates(map[string]any{"status": model.AdtyTelegramDeliverySkip, "last_error": "binding revoked"}).Error; err != nil {
				return err
			}
		}
		if purpose == model.AdtyTelegramPurposeUser && strings.TrimSpace(user.TelegramId) != "" {
			if before["status"] == "unbound" {
				before["status"] = model.AdtyTelegramBindActive
			}
			if !wasActive {
				if parsed, parseErr := strconv.ParseInt(strings.TrimSpace(user.TelegramId), 10, 64); parseErr == nil && parsed > 0 {
					wasActive = true
					prevTelegramUserID = parsed
				}
			}
			if err := tx.Model(&model.User{}).Where("id = ?", userID).Update("telegram_id", "").Error; err != nil {
				return err
			}
		}
		audit.SiteId = model.OfficialAuditSiteIDPointer(row.SiteId)
		audit.ObjectType, audit.ObjectId = "telegram_binding", strconv.FormatInt(row.Id, 10)
		audit.BeforeValue = common.GetJsonString(before)
		audit.AfterValue = common.GetJsonString(map[string]any{"purpose": purpose, "status": "revoked"})
		return model.AppendAuditIntentTx(tx, *audit)
	})
	if err == nil && wasActive && prevTelegramUserID > 0 && AdtyTelegramSender != nil && model.DB != nil && model.DB.Migrator().HasTable(&model.AdtyTelegramBot{}) {
		var bot model.AdtyTelegramBot
		if model.DB.Where("purpose = ? AND enabled = ?", purpose, true).Limit(1).Find(&bot).RowsAffected > 0 &&
			(prevBotIdentity == "" || bot.IdentityHash == prevBotIdentity) {
			if token := mustOpenTelegramToken(bot); token != "" {
				siteName := adtyTelegramSiteDisplayName(prevSiteID)
				if _, body, renderErr := RenderAdtyBilingualNotice("telegram_unbind_confirm", nil, siteName); renderErr == nil && strings.TrimSpace(body) != "" {
					sender := AdtyTelegramSender
					chat := strconv.FormatInt(prevTelegramUserID, 10)
					go func() {
						_ = sender(token, chat, body)
					}()
				}
			}
		}
	}
	return err
}

// RevokeAllAdtyTelegramBindings revokes all Telegram bindings for a user when
// an administrator clears the user's Telegram binding.
func RevokeAllAdtyTelegramBindings(userID int) error {
	if model.DB == nil || userID <= 0 {
		return nil
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := model.AdtyLockForUpdate(tx).First(&user, userID).Error; err != nil {
			return err
		}
		if tx.Migrator().HasTable(&model.AdtyTelegramBinding{}) {
			if err := tx.Model(&model.AdtyTelegramBinding{}).
				Where("user_id = ? AND status <> ?", userID, "revoked").
				Update("status", "revoked").Error; err != nil {
				return err
			}
		}
		if tx.Migrator().HasTable(&model.AdtyTelegramChallenge{}) {
			if err := tx.Model(&model.AdtyTelegramChallenge{}).
				Where("user_id = ? AND used_at = 0", userID).
				Update("used_at", time.Now().Unix()).Error; err != nil {
				return err
			}
		}
		if tx.Migrator().HasTable(&model.AdtyTelegramDelivery{}) {
			if err := tx.Model(&model.AdtyTelegramDelivery{}).
				Where("user_id = ? AND public = ? AND status IN ?", userID, false, []string{model.AdtyTelegramDeliveryWait, model.AdtyTelegramDeliveryFail}).
				Updates(map[string]any{"status": model.AdtyTelegramDeliverySkip, "last_error": "binding revoked"}).Error; err != nil {
				return err
			}
		}
		if strings.TrimSpace(user.TelegramId) != "" {
			if err := tx.Model(&model.User{}).Where("id = ?", userID).Update("telegram_id", "").Error; err != nil {
				return err
			}
		}
		return nil
	})
}
