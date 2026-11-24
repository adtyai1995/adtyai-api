package model

import (
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AdtyBadgeSeen records the timestamp when a user last opened a sidebar menu
// (R-32: opening the menu clears its badge until newer items arrive).
type AdtyBadgeSeen struct {
	UserId  int    `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	MenuKey string `json:"menu_key" gorm:"primaryKey;type:varchar(64)"`
	SeenAt  int64  `json:"seen_at" gorm:"not null;default:0"`
}

func (AdtyBadgeSeen) TableName() string { return "adty_badge_seens" }

func GetAdtyBadgeSeenMap(userID int) (map[string]int64, error) {
	result := map[string]int64{}
	if userID <= 0 || DB == nil || !DB.Migrator().HasTable(&AdtyBadgeSeen{}) {
		return result, nil
	}
	var rows []AdtyBadgeSeen
	if err := DB.Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.MenuKey] = row.SeenAt
	}
	return result, nil
}

func MarkAdtyBadgeSeen(userID int, keys []string, seenAt int64) error {
	if userID <= 0 || len(keys) == 0 || DB == nil {
		return nil
	}
	rows := make([]AdtyBadgeSeen, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, raw := range keys {
		key := strings.TrimSpace(raw)
		if key == "" || len(key) > 64 {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		rows = append(rows, AdtyBadgeSeen{
			UserId:  userID,
			MenuKey: key,
			SeenAt:  seenAt,
		})
	}
	if len(rows) == 0 {
		return nil
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "menu_key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"seen_at": gorm.Expr("CASE WHEN adty_badge_seens.seen_at < EXCLUDED.seen_at THEN EXCLUDED.seen_at ELSE adty_badge_seens.seen_at END"),
		}),
	}).Create(&rows).Error
}
