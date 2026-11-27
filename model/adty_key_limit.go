package model

import (
	"sync"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AdtyKeyLimits extends the adty_key_limits table schema with per-key TPM and
// concurrency caps while keeping AdtyKeyLimit backward-compatible.
type AdtyKeyLimits struct {
	TokenId     int   `json:"token_id" gorm:"primaryKey;autoIncrement:false"`
	PerMinute   int   `json:"per_minute" gorm:"not null;default:0"`
	TPM         int   `json:"tpm" gorm:"column:tpm;not null;default:0"`
	Concurrency int   `json:"concurrency" gorm:"column:concurrency;not null;default:0"`
	UpdatedAt   int64 `json:"updated_at" gorm:"bigint;not null;default:0"`
}

func (AdtyKeyLimits) TableName() string {
	return "adty_key_limits"
}

type AdtyKeyLimitPatch struct {
	PerMinute   *int
	TPM         *int
	Concurrency *int
}

func (p AdtyKeyLimitPatch) HasChanges() bool {
	return p.PerMinute != nil || p.TPM != nil || p.Concurrency != nil
}

var migratedKeyLimitDBs sync.Map

func EnsureAdtyKeyLimitSchema(db *gorm.DB) error {
	if db == nil {
		return common.NewMessage("Invalid database connection")
	}
	key := any(db)
	if sqlDB, err := db.DB(); err == nil && sqlDB != nil {
		key = sqlDB
	}
	if _, ok := migratedKeyLimitDBs.Load(key); ok {
		return nil
	}
	if err := db.AutoMigrate(&AdtyKeyLimits{}); err != nil {
		return err
	}
	migratedKeyLimitDBs.Store(key, struct{}{})
	return nil
}

func GetAdtyKeyLimitsTx(tx *gorm.DB, tokenID int) (AdtyKeyLimits, error) {
	var row AdtyKeyLimits
	if tokenID <= 0 {
		return row, common.NewMessage("Invalid parameters")
	}
	if tx == nil {
		return row, common.NewMessage("Invalid database connection")
	}
	err := tx.Where("token_id = ?", tokenID).Limit(1).Find(&row).Error
	return row, err
}

// GetAdtyKeyLimits returns all stored per-key rate limits (RPM, TPM, Concurrency).
// A missing row means no extra cap on that key.
func GetAdtyKeyLimits(tokenID int) (AdtyKeyLimits, error) {
	if DB != nil {
		var key any = DB
		if sqlDB, err := DB.DB(); err == nil && sqlDB != nil {
			key = sqlDB
		}
		if _, ok := migratedKeyLimitDBs.Load(key); !ok {
			hasKeyLimitsTable, err := AdtyTableExists(DB, &AdtyKeyLimits{})
			if err != nil {
				return AdtyKeyLimits{}, err
			}
			if hasKeyLimitsTable {
				if err := EnsureAdtyKeyLimitSchema(DB); err != nil {
					return AdtyKeyLimits{}, err
				}
			}
		}
	}
	return GetAdtyKeyLimitsTx(DB, tokenID)
}

// GetAdtyKeyLimit returns the stored per-minute cap. A missing row means no extra cap.
func GetAdtyKeyLimit(tokenID int) (AdtyKeyLimit, error) {
	limits, err := GetAdtyKeyLimits(tokenID)
	return AdtyKeyLimit{
		TokenId:   limits.TokenId,
		PerMinute: limits.PerMinute,
	}, err
}

// SaveAdtyKeyLimits stores positive per-key caps (RPM, TPM, Concurrency) for one key.
func SaveAdtyKeyLimits(tokenID, perMinute, tpm, concurrency int) error {
	if tokenID <= 0 || (perMinute <= 0 && tpm <= 0 && concurrency <= 0) {
		return common.NewMessage("Invalid parameters")
	}
	if err := EnsureAdtyKeyLimitSchema(DB); err != nil {
		return err
	}
	_, _, err := ApplyAdtyKeyLimitPatchTx(DB, tokenID, AdtyKeyLimitPatch{
		PerMinute:   &perMinute,
		TPM:         &tpm,
		Concurrency: &concurrency,
	})
	return err
}

// SaveAdtyKeyLimit stores a positive per-minute cap for one key.
func SaveAdtyKeyLimit(tokenID, perMinute int) error {
	if tokenID <= 0 || perMinute <= 0 {
		return common.NewMessage("Invalid parameters")
	}
	if err := EnsureAdtyKeyLimitSchema(DB); err != nil {
		return err
	}
	_, _, err := ApplyAdtyKeyLimitPatchTx(DB, tokenID, AdtyKeyLimitPatch{
		PerMinute: &perMinute,
	})
	return err
}

func ClearAdtyKeyLimitTx(tx *gorm.DB, tokenID int) error {
	if tokenID <= 0 {
		return common.NewMessage("Invalid parameters")
	}
	if tx == nil {
		return common.NewMessage("Invalid database connection")
	}
	return tx.Where("token_id = ?", tokenID).Delete(&AdtyKeyLimits{}).Error
}

// ClearAdtyKeyLimit removes a key cap. Later requests are not extra-limited.
func ClearAdtyKeyLimit(tokenID int) error {
	return ClearAdtyKeyLimitTx(DB, tokenID)
}

// ApplyAdtyKeyLimitPatchTx reads the current per-key limits inside tx, applies
// any non-nil fields from patch, and writes or deletes the row within tx so key
// metadata and rate-limit policies commit or roll back atomically.
func ApplyAdtyKeyLimitPatchTx(tx *gorm.DB, tokenID int, patch AdtyKeyLimitPatch) (previous AdtyKeyLimits, updated AdtyKeyLimits, err error) {
	if tokenID <= 0 {
		return previous, updated, common.NewMessage("Invalid parameters")
	}
	if tx == nil {
		return previous, updated, common.NewMessage("Invalid database connection")
	}
	if !patch.HasChanges() {
		return previous, updated, nil
	}
	if err := tx.Where("token_id = ?", tokenID).Limit(1).Find(&previous).Error; err != nil {
		return previous, updated, err
	}
	updated = previous
	updated.TokenId = tokenID
	if patch.PerMinute != nil {
		updated.PerMinute = max(*patch.PerMinute, 0)
	}
	if patch.TPM != nil {
		updated.TPM = max(*patch.TPM, 0)
	}
	if patch.Concurrency != nil {
		updated.Concurrency = max(*patch.Concurrency, 0)
	}
	if updated.PerMinute <= 0 && updated.TPM <= 0 && updated.Concurrency <= 0 {
		if previous.TokenId > 0 || previous.PerMinute > 0 || previous.TPM > 0 || previous.Concurrency > 0 {
			if err := tx.Where("token_id = ?", tokenID).Delete(&AdtyKeyLimits{}).Error; err != nil {
				return previous, updated, err
			}
		}
		return previous, AdtyKeyLimits{TokenId: tokenID}, nil
	}
	updated.UpdatedAt = common.GetTimestamp()
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "token_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"per_minute", "tpm", "concurrency", "updated_at"}),
	}).Create(&updated).Error; err != nil {
		return previous, updated, err
	}
	return previous, updated, nil
}
