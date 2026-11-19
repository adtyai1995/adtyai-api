package service

import (
	"strconv"
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
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := model.AdtyLockForUpdate(tx).First(&user, userID).Error; err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled || (purpose == model.AdtyTelegramPurposeAdmin && !adtyCanUseAdminTelegram(tx, &user)) {
			return ErrAdtyTelegramForbidden
		}
		var row model.AdtyTelegramBinding
		found := model.AdtyLockForUpdate(tx).Where("user_id = ? AND purpose = ?", userID, purpose).Limit(1).Find(&row)
		if found.Error != nil {
			return found.Error
		}
		before := map[string]any{"purpose": purpose, "status": "unbound"}
		if found.RowsAffected > 0 {
			before["status"] = row.Status
			if err := tx.Model(&row).Update("status", "revoked").Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&model.AdtyTelegramChallenge{}).Where("user_id = ? AND purpose = ? AND used_at = 0", userID, purpose).Update("used_at", time.Now().Unix()).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.AdtyTelegramDelivery{}).Where("user_id = ? AND purpose = ? AND public = ? AND status IN ?", userID, purpose, false, []string{model.AdtyTelegramDeliveryWait, model.AdtyTelegramDeliveryFail}).Updates(map[string]any{"status": model.AdtyTelegramDeliverySkip, "last_error": "binding revoked"}).Error; err != nil {
			return err
		}
		audit.SiteId = model.OfficialAuditSiteIDPointer(row.SiteId)
		audit.ObjectType, audit.ObjectId = "telegram_binding", strconv.FormatInt(row.Id, 10)
		audit.BeforeValue = common.GetJsonString(before)
		audit.AfterValue = common.GetJsonString(map[string]any{"purpose": purpose, "status": "revoked"})
		return model.AppendAuditIntentTx(tx, *audit)
	})
}
