package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ftrRound1Titles(inbox AdtyNotificationInbox) []string {
	titles := make([]string, 0, len(inbox.Announcements))
	for _, item := range inbox.Announcements {
		title, _ := item["title"].(string)
		titles = append(titles, title)
	}
	return titles
}

// ADTY-18-07: Root publishes site, team, and user notices. Each inbox keeps only its own scope.
func TestFTRADTY1807ScopedNoticePG(t *testing.T) {
	db := ftrRound1PG(t)
	root := ftrRound1User(t, db, "ftr1807root", common.RoleRootUser)
	reader := ftrRound1User(t, db, "ftr1807reader", common.RoleCommonUser)
	other := ftrRound1User(t, db, "ftr1807other", common.RoleCommonUser)
	member := ftrRound1User(t, db, "ftr1807member", common.RoleCommonUser)
	teammate := ftrRound1User(t, db, "ftr1807team", common.RoleCommonUser)
	require.Equal(t, common.RoleRootUser, root.Role)
	require.Equal(t, common.RoleCommonUser, reader.Role)

	site := model.AdtySite{
		OwnerUserId:  root.Id,
		Subdomain:    "ftr1807",
		Status:       model.AdtySiteStatusActive,
		AgreementVer: AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", member.Id).Update("site_id", site.Id).Error)

	team := model.AdtyTeam{
		SiteId:       0,
		LeaderUserId: teammate.Id,
		Name:         "ftr1807-team",
		Discount:     decimal.NewFromInt(1),
		Status:       model.AdtyTeamStatusActive,
	}
	require.NoError(t, db.Create(&team).Error)
	require.NoError(t, db.Create(&model.AdtyTeamMember{
		TeamId: team.Id,
		UserId: teammate.Id,
		Role:   model.AdtyTeamRoleMember,
		Status: model.AdtyTeamMemberActive,
	}).Error)

	for _, userID := range []int{root.Id, reader.Id, other.Id, member.Id, teammate.Id} {
		ftrRound1PinQuota(t, db, userID)
	}

	userNotice, err := SaveAdtyNotificationPublication(root.Id, nil, 0, AdtyNotificationInput{
		Title:        "FTR1807 user notice",
		Content:      "Only the named main-site user can read this.",
		Kind:         "system",
		Audience:     "user",
		TargetUserId: reader.Id,
	}, false)
	require.NoError(t, err)
	assert.Equal(t, "user", userNotice.Audience)
	assert.Equal(t, 0, userNotice.TargetSiteId)
	assert.Equal(t, "user:"+itoa(reader.Id), userNotice.TargetSiteName)
	assert.Equal(t, "published", userNotice.Status)

	siteNotice, err := SaveAdtyNotificationPublication(root.Id, nil, 0, AdtyNotificationInput{
		Title:        "FTR1807 site notice",
		Content:      "Only members of this site can read this.",
		Kind:         "system",
		Audience:     "site",
		TargetSiteId: site.Id,
	}, false)
	require.NoError(t, err)
	assert.Equal(t, "site", siteNotice.Audience)
	assert.Equal(t, site.Id, siteNotice.TargetSiteId)

	teamNotice, err := SaveAdtyNotificationPublication(root.Id, nil, 0, AdtyNotificationInput{
		Title:        "FTR1807 team notice",
		Content:      "Only active members of this team can read this.",
		Kind:         "system",
		Audience:     "team",
		TargetTeamId: team.Id,
	}, false)
	require.NoError(t, err)
	assert.Equal(t, "team", teamNotice.Audience)
	assert.Equal(t, "team:"+itoa64(team.Id), teamNotice.TargetSiteName)

	readerInbox, err := GetAdtyNotificationInbox(reader.Id, nil, 0)
	require.NoError(t, err)
	assert.Contains(t, ftrRound1Titles(readerInbox), "FTR1807 user notice")
	assert.NotContains(t, ftrRound1Titles(readerInbox), "FTR1807 site notice")
	assert.NotContains(t, ftrRound1Titles(readerInbox), "FTR1807 team notice")

	otherInbox, err := GetAdtyNotificationInbox(other.Id, nil, 0)
	require.NoError(t, err)
	assert.NotContains(t, ftrRound1Titles(otherInbox), "FTR1807 user notice")
	assert.NotContains(t, ftrRound1Titles(otherInbox), "FTR1807 site notice")
	assert.NotContains(t, ftrRound1Titles(otherInbox), "FTR1807 team notice")

	memberInbox, err := GetAdtyNotificationInbox(member.Id, &site, 0)
	require.NoError(t, err)
	assert.Contains(t, ftrRound1Titles(memberInbox), "FTR1807 site notice")
	assert.NotContains(t, ftrRound1Titles(memberInbox), "FTR1807 user notice")
	assert.NotContains(t, ftrRound1Titles(memberInbox), "FTR1807 team notice")

	teamInbox, err := GetAdtyNotificationInbox(teammate.Id, nil, 0)
	require.NoError(t, err)
	assert.Contains(t, ftrRound1Titles(teamInbox), "FTR1807 team notice")
	assert.NotContains(t, ftrRound1Titles(teamInbox), "FTR1807 user notice")
	assert.NotContains(t, ftrRound1Titles(teamInbox), "FTR1807 site notice")

	for _, userID := range []int{root.Id, reader.Id, other.Id, member.Id, teammate.Id} {
		ftrRound1AssertNoMoney(t, db, userID)
	}
}

func itoa(id int) string {
	return common.Interface2String(id)
}

func itoa64(id int64) string {
	return common.Interface2String(id)
}
