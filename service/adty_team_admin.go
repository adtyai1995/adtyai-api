package service

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// AdtyTeamListFilter narrows the team management list. SiteID nil means every
// site and is only accepted from Root; a site owner always passes its own site.
type AdtyTeamListFilter struct {
	SiteID *int
	Status string
	Query  string
}

// AdtyTeamAdminRow is one row of the Root or site-owner team list.
type AdtyTeamAdminRow struct {
	model.AdtyTeam
	SiteName       string `json:"site_name"`
	SourceSiteName string `json:"source_site_name,omitempty"`
	LeaderUsername string `json:"leader_username"`
	ActiveMembers  int64  `json:"active_members"`
	ActiveKeys     int64  `json:"active_keys"`
}

// AdtyTeamAdminDetail is the read-only team page for Root or the site owner.
// Members and keys stay managed by the team leader.
type AdtyTeamAdminDetail struct {
	AdtyTeamAdminRow
	Members []AdtyTeamMemberView `json:"members"`
	Keys    []AdtyTeamKeyView    `json:"keys"`
}

func adtyTeamListQuery(filter AdtyTeamListFilter) *gorm.DB {
	q := model.DB.Model(&model.AdtyTeam{})
	if filter.SiteID != nil {
		q = q.Where("site_id = ?", *filter.SiteID)
	}
	switch filter.Status {
	case model.AdtyTeamStatusActive, model.AdtyTeamStatusClosed:
		q = q.Where("status = ?", filter.Status)
	}
	if search := strings.TrimSpace(filter.Query); search != "" {
		like := "%" + strings.ToLower(search) + "%"
		leaders := model.DB.Model(&model.User{}).Select("id").Where("LOWER(username) LIKE ?", like)
		if id, err := strconv.ParseInt(strings.TrimPrefix(search, "#"), 10, 64); err == nil && id > 0 {
			q = q.Where("(id = ? OR leader_user_id = ? OR LOWER(name) LIKE ? OR leader_user_id IN (?))", id, id, like, leaders)
		} else {
			q = q.Where("(LOWER(name) LIKE ? OR leader_user_id IN (?))", like, leaders)
		}
	}
	return q
}

// ListAdtyTeamsForAdmin returns one page of teams with site, leader and
// active member and key counts.
func ListAdtyTeamsForAdmin(filter AdtyTeamListFilter, offset, limit int) ([]AdtyTeamAdminRow, int64, error) {
	q := adtyTeamListQuery(filter)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var teams []model.AdtyTeam
	if err := adtyTeamListQuery(filter).Order("id DESC").Offset(offset).Limit(limit).Find(&teams).Error; err != nil {
		return nil, 0, err
	}
	rows, err := adtyTeamAdminRows(teams)
	return rows, total, err
}

func adtyTeamAdminRows(teams []model.AdtyTeam) ([]AdtyTeamAdminRow, error) {
	rows := make([]AdtyTeamAdminRow, 0, len(teams))
	if len(teams) == 0 {
		return rows, nil
	}
	teamIDs := make([]int64, 0, len(teams))
	siteIDs := make([]int, 0, len(teams)*2)
	leaderIDs := make([]int, 0, len(teams))
	for _, team := range teams {
		teamIDs = append(teamIDs, team.Id)
		siteIDs = append(siteIDs, team.SiteId, team.SourceSiteId)
		leaderIDs = append(leaderIDs, team.LeaderUserId)
	}
	siteNames, err := AdtySiteNames(siteIDs)
	if err != nil {
		return nil, err
	}
	leaderNames := map[int]string{}
	var leaders []model.User
	if err := model.DB.Unscoped().Select("id", "username").Where("id IN ?", leaderIDs).Find(&leaders).Error; err != nil {
		return nil, err
	}
	for _, user := range leaders {
		leaderNames[user.Id] = user.Username
	}
	type countRow struct {
		TeamId int64
		Total  int64
	}
	members := map[int64]int64{}
	var memberCounts []countRow
	if err := model.DB.Model(&model.AdtyTeamMember{}).Select("team_id, COUNT(*) AS total").
		Where("team_id IN ? AND status = ?", teamIDs, model.AdtyTeamMemberActive).
		Group("team_id").Scan(&memberCounts).Error; err != nil {
		return nil, err
	}
	for _, row := range memberCounts {
		members[row.TeamId] = row.Total
	}
	keys := map[int64]int64{}
	var activeBindings []model.AdtyTeamKey
	if err := model.DB.Where("team_id IN ? AND status = ?", teamIDs, model.AdtyTeamMemberActive).Find(&activeBindings).Error; err != nil {
		return nil, err
	}
	if len(activeBindings) > 0 {
		tokenIDs := make([]int, 0, len(activeBindings))
		for _, binding := range activeBindings {
			tokenIDs = append(tokenIDs, binding.TokenId)
		}
		credentials := map[int]model.Token{}
		var tokens []model.Token
		if err := model.DB.Unscoped().
			Select("id", "status", "deleted_at", "expired_time", "remain_quota", "unlimited_quota", "quota_pending", "used_quota").
			Where("id IN ?", tokenIDs).Find(&tokens).Error; err != nil {
			return nil, err
		}
		for _, token := range tokens {
			credentials[token.Id] = token
		}
		for _, binding := range activeBindings {
			if teamKeyStatus(binding, credentials[binding.TokenId]) == model.AdtyTeamMemberActive {
				keys[binding.TeamId]++
			}
		}
	}
	for _, team := range teams {
		row := AdtyTeamAdminRow{
			AdtyTeam:       team,
			SiteName:       siteNames[team.SiteId],
			LeaderUsername: leaderNames[team.LeaderUserId],
			ActiveMembers:  members[team.Id],
			ActiveKeys:     keys[team.Id],
		}
		if team.SourceSiteId > 0 {
			row.SourceSiteName = siteNames[team.SourceSiteId]
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// loadAdtyTeamInScope reads a team. A non-nil siteScope limits the caller to
// teams currently on that site; any other team reads as not found.
func loadAdtyTeamInScope(teamID int64, siteScope *int) (*model.AdtyTeam, error) {
	var team model.AdtyTeam
	q := model.DB.Where("id = ?", teamID)
	if siteScope != nil {
		q = q.Where("site_id = ?", *siteScope)
	}
	lookup := q.Limit(1).Find(&team)
	if lookup.Error != nil {
		return nil, lookup.Error
	}
	if lookup.RowsAffected == 0 {
		return nil, common.NewMessage("Team not found")
	}
	return &team, nil
}

// GetAdtyTeamAdminDetail returns a team with every member and key binding.
func GetAdtyTeamAdminDetail(teamID int64, siteScope *int) (*AdtyTeamAdminDetail, error) {
	team, err := loadAdtyTeamInScope(teamID, siteScope)
	if err != nil {
		return nil, err
	}
	rows, err := adtyTeamAdminRows([]model.AdtyTeam{*team})
	if err != nil {
		return nil, err
	}
	page := &AdtyTeamMine{TeamId: team.Id, Members: []AdtyTeamMemberView{}, Keys: []AdtyTeamKeyView{}}
	// The leader view lists every member and key of the team.
	if err := fillAdtyTeamPage(page, model.AdtyTeamMember{UserId: team.LeaderUserId, Role: model.AdtyTeamRoleLeader}); err != nil {
		return nil, err
	}
	return &AdtyTeamAdminDetail{AdtyTeamAdminRow: rows[0], Members: page.Members, Keys: page.Keys}, nil
}

// ListAdtyTeamLedgerForAdmin returns one page of the team's shared-balance ledger, newest first.
func ListAdtyTeamLedgerForAdmin(teamID int64, siteScope *int, offset, limit int) ([]model.AdtyTeamLedger, int64, error) {
	if _, err := loadAdtyTeamInScope(teamID, siteScope); err != nil {
		return nil, 0, err
	}
	q := model.DB.Model(&model.AdtyTeamLedger{}).Where("team_id = ?", teamID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.AdtyTeamLedger
	err := model.DB.Where("team_id = ?", teamID).Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}
