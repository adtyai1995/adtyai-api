package model

import "gorm.io/gorm"

// LookupUsernamesByIDs batch-resolves user IDs to usernames in a single query.
// Uses Unscoped so soft-deleted historical references still resolve cleanly.
func LookupUsernamesByIDs(db *gorm.DB, ids []int) map[int]string {
	result := make(map[int]string)
	if db == nil || len(ids) == 0 {
		return result
	}
	seen := make(map[int]struct{}, len(ids))
	unique := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return result
	}
	type userRow struct {
		Id       int
		Username string
	}
	var rows []userRow
	if err := db.Unscoped().Table("users").Select("id, username").Where("id IN ?", unique).Find(&rows).Error; err != nil {
		return result
	}
	for _, row := range rows {
		result[row.Id] = row.Username
	}
	return result
}
