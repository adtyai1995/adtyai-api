package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AdtyUserLimit 是单个用户的请求限制（任务 7.4）。-1 表示跟默认，0 表示不限。
// 列上不设 GORM default：有 default 时 GORM 插入会把 0（不限）当成没填，写成默认值。
type AdtyUserLimit struct {
	UserId      int   `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	RPM         int   `json:"rpm" gorm:"not null"`
	TPM         int   `json:"tpm" gorm:"not null"`
	Concurrency int   `json:"concurrency" gorm:"not null"`
	UpdatedAt   int64 `json:"updated_at" gorm:"autoUpdateTime"`
}

func (AdtyUserLimit) TableName() string { return "adty_user_limits" }

// AdtyOnConsumeTokens 由 adtylimit 注册，RecordConsumeLog 一开头调用它累计 TPM。
// 放在 model 包里，避免 model 反过来依赖限流包。
var AdtyOnConsumeTokens func(userId int, tokens int, withoutToken bool)

// AdtyOnConsumeKeyTokens records per-key TPM usage inside RecordConsumeLog
// even when LogConsumeEnabled is disabled (R3-023).
var AdtyOnConsumeKeyTokens func(tokenID int, tokens int)

// GetAdtyUserLimit 取一个用户的限制；没有设过时返回全部为 -1 的一行。
func GetAdtyUserLimit(userId int) (AdtyUserLimit, error) {
	if DB == nil {
		return AdtyUserLimit{}, gorm.ErrInvalidDB
	}
	var rows []AdtyUserLimit
	if err := DB.Where("user_id = ?", userId).Limit(1).Find(&rows).Error; err != nil {
		return AdtyUserLimit{}, err
	}
	if len(rows) == 0 {
		return AdtyUserLimit{UserId: userId, RPM: -1, TPM: -1, Concurrency: -1}, nil
	}
	return rows[0], nil
}

// SaveAdtyUserLimit 写一个用户的限制（有就覆盖）。
func SaveAdtyUserLimit(limit AdtyUserLimit) error {
	return DB.Session(&gorm.Session{}).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"rpm", "tpm", "concurrency", "updated_at"}),
	}).Create(&limit).Error
}

// SaveAdtyUserLimitForActor locks the target user row and enforces role hierarchy
// and PAT admin-target guards atomically alongside saving AdtyUserLimit (R10-006).
func SaveAdtyUserLimitForActor(limit AdtyUserLimit, actorRole int, denyAdminTarget bool) error {
	if DB == nil {
		return gorm.ErrInvalidDB
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var target User
		if err := lockForUpdate(tx).Select("id", "role").First(&target, limit.UserId).Error; err != nil {
			return err
		}
		if actorRole != common.RoleRootUser && (actorRole < common.RoleAdminUser || actorRole <= target.Role) {
			return ErrUserQuotaPermission
		}
		if denyAdminTarget && target.Role >= common.RoleAdminUser {
			return ErrAdminTargetSessionRequired
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"rpm", "tpm", "concurrency", "updated_at"}),
		}).Create(&limit).Error
	})
}
