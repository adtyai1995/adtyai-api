package model

import (
	"strings"

	"gorm.io/gorm"
)

const (
	AdtyModelProbeStatusDown = 0
	AdtyModelProbeStatusUp   = 1

	adtyModelProbeMaxMessageBytes = 255
	adtyModelProbeMaxGroupBytes   = 64
	adtyModelProbeMaxModelBytes   = 128
)

// AdtyModelProbe records one lightweight built-in connectivity probe for a
// (group_id, model_name) target. It stores no channel IDs, URLs, or secrets.
type AdtyModelProbe struct {
	Id        int64   `json:"id" gorm:"primaryKey;autoIncrement"`
	GroupId   string  `json:"group_id" gorm:"type:varchar(64);index:idx_adty_model_probes_gm_t,priority:1;not null"`
	ModelName string  `json:"model_name" gorm:"type:varchar(128);index:idx_adty_model_probes_gm_t,priority:2;not null"`
	Status    int     `json:"status" gorm:"not null"`
	PingMs    float64 `json:"ping_ms" gorm:"not null;default:0"`
	Message   string  `json:"message" gorm:"type:varchar(255);not null;default:''"`
	CheckedAt int64   `json:"checked_at" gorm:"index:idx_adty_model_probes_gm_t,priority:3;index;not null"`
}

func (AdtyModelProbe) TableName() string {
	return "adty_model_probes"
}

func truncateUTF8Bytes(s string, maxBytes int) string {
	if len(s) <= maxBytes || maxBytes <= 0 {
		return s
	}
	cut := 0
	for i := range s {
		if i > maxBytes {
			break
		}
		cut = i
	}
	return s[:cut]
}

// InsertAdtyModelProbe persists one model connectivity probe result.
func InsertAdtyModelProbe(row *AdtyModelProbe) error {
	if DB == nil {
		return gorm.ErrInvalidDB
	}
	if row == nil {
		return nil
	}
	hasProbeTable, err := AdtyTableExists(DB, &AdtyModelProbe{})
	if err != nil {
		return err
	}
	if !hasProbeTable {
		return nil
	}
	entry := *row
	entry.GroupId = truncateUTF8Bytes(strings.TrimSpace(entry.GroupId), adtyModelProbeMaxGroupBytes)
	entry.ModelName = truncateUTF8Bytes(strings.TrimSpace(entry.ModelName), adtyModelProbeMaxModelBytes)
	entry.Message = truncateUTF8Bytes(strings.TrimSpace(entry.Message), adtyModelProbeMaxMessageBytes)
	if entry.PingMs < 0 {
		entry.PingMs = 0
	}
	if err := DB.Create(&entry).Error; err != nil {
		return err
	}
	row.Id = entry.Id
	return nil
}

// PruneAdtyModelProbesBefore deletes probe records strictly older than cutoffUnix.
func PruneAdtyModelProbesBefore(cutoffUnix int64) error {
	if DB == nil {
		return nil
	}
	hasProbeTable, err := AdtyTableExists(DB, &AdtyModelProbe{})
	if err != nil {
		return err
	}
	if !hasProbeTable {
		return nil
	}
	return DB.Where("checked_at < ?", cutoffUnix).Delete(&AdtyModelProbe{}).Error
}

// ListAdtyModelProbesSince returns all probe records with checked_at >= sinceUnix
// ordered chronologically (checked_at ASC, id ASC).
func ListAdtyModelProbesSince(sinceUnix int64) ([]AdtyModelProbe, error) {
	if DB == nil {
		return []AdtyModelProbe{}, nil
	}
	hasProbeTable, err := AdtyTableExists(DB, &AdtyModelProbe{})
	if err != nil {
		return nil, err
	}
	if !hasProbeTable {
		return []AdtyModelProbe{}, nil
	}
	var rows []AdtyModelProbe
	if err := DB.Where("checked_at >= ?", sinceUnix).
		Order("checked_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		return []AdtyModelProbe{}, nil
	}
	return rows, nil
}

// LatestAdtyModelProbeCheckedAt returns the most recent probe timestamp, or 0 if none exist.
func LatestAdtyModelProbeCheckedAt() (int64, error) {
	if DB == nil {
		return 0, nil
	}
	hasProbeTable, err := AdtyTableExists(DB, &AdtyModelProbe{})
	if err != nil {
		return 0, err
	}
	if !hasProbeTable {
		return 0, nil
	}
	var row AdtyModelProbe
	res := DB.Select("checked_at").Order("checked_at DESC, id DESC").Limit(1).Find(&row)
	if res.Error != nil {
		return 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, nil
	}
	return row.CheckedAt, nil
}
