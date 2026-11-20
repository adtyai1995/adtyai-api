package controller

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ftrSeedTeam(t *testing.T, db *gorm.DB, leader model.User, name string) model.AdtyTeam {
	t.Helper()
	team := model.AdtyTeam{
		SiteId: 0, LeaderUserId: leader.Id, Name: name,
		Discount: decimal.NewFromInt(1), Status: model.AdtyTeamStatusActive,
	}
	require.NoError(t, db.Create(&team).Error)
	require.NoError(t, db.Create(&model.AdtyTeamMember{
		TeamId: team.Id, UserId: leader.Id, Role: model.AdtyTeamRoleLeader, Status: model.AdtyTeamMemberActive,
	}).Error)
	return team
}

// ADTY-13-01: Root creates a main-site team through POST /api/adty/admin/teams.
func TestFTRADTY1301RootCreatesTeamPG(t *testing.T) {
	db := ftrAccountDB(t)
	root := model.User{
		Username: "ftr1301-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	leader := model.User{
		Username: "ftr1301-lead", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&leader).Error)
	require.Equal(t, common.RoleRootUser, root.Role)

	ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/adty/admin/teams", map[string]any{
		"name": "FTR 1301 Team", "leader_id": leader.Id, "site_id": 0, "discount": "1",
	}, root.Id)
	ctx.Set("role", root.Role)
	ctx.Set("username", root.Username)
	PostAdtyMainTeam(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "FTR 1301 Team")

	var stored model.AdtyTeam
	require.NoError(t, db.Where("name = ?", "FTR 1301 Team").First(&stored).Error)
	assert.Equal(t, leader.Id, stored.LeaderUserId)
	assert.Equal(t, 0, stored.SiteId)
	assert.Equal(t, model.AdtyTeamStatusActive, stored.Status)
	assert.True(t, stored.Discount.Equal(decimal.NewFromInt(1)))
	assert.Zero(t, stored.SharedQuota)
	var member model.AdtyTeamMember
	require.NoError(t, db.Where("team_id = ? AND user_id = ?", stored.Id, leader.Id).First(&member).Error)
	assert.Equal(t, model.AdtyTeamRoleLeader, member.Role)
	assert.Equal(t, model.AdtyTeamMemberActive, member.Status)
	ftrAssertQuotaAndNoLedger(t, db, root.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, leader.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-13-02: the team leader renames the team through POST /api/adty/teams/:id/profile.
func TestFTRADTY1302LeaderRenamesTeamPG(t *testing.T) {
	db := ftrAccountDB(t)
	leader := model.User{
		Username: "ftr1302-lead", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&leader).Error)
	team := ftrSeedTeam(t, db, leader, "ftr1302-old")

	ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/adty/teams/"+strconv.FormatInt(team.Id, 10)+"/profile", map[string]any{
		"name": "FTR 1302 Renamed",
	}, leader.Id)
	ctx.Set("role", leader.Role)
	ctx.Set("username", leader.Username)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(team.Id, 10)}}
	PostAdtyTeamProfile(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)

	var stored model.AdtyTeam
	require.NoError(t, db.First(&stored, team.Id).Error)
	assert.Equal(t, "FTR 1302 Renamed", stored.Name)
	assert.Equal(t, leader.Id, stored.LeaderUserId)
	assert.True(t, stored.Discount.Equal(decimal.NewFromInt(1)))
	assert.Equal(t, model.AdtyTeamStatusActive, stored.Status)
	assert.Equal(t, common.RoleCommonUser, leader.Role)
	ftrAssertQuotaAndNoLedger(t, db, leader.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-13-03: the leader creates an independent team employee.
func TestFTRADTY1303LeaderCreatesEmployeePG(t *testing.T) {
	db := ftrAccountDB(t)
	leader := model.User{
		Username: "ftr1303-lead", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&leader).Error)
	team := ftrSeedTeam(t, db, leader, "ftr1303")

	ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/adty/teams/"+strconv.FormatInt(team.Id, 10)+"/employees", map[string]any{
		"username": "ftr1303emp", "password": "StrongPassword123!", "display_name": "FTR Member",
	}, leader.Id)
	ctx.Set("role", leader.Role)
	ctx.Set("username", leader.Username)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(team.Id, 10)}}
	PostAdtyTeamEmployee(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr1303emp")

	var user model.User
	require.NoError(t, db.Where("username = ?", "ftr1303emp").First(&user).Error)
	assert.Equal(t, common.RoleCommonUser, user.Role)
	assert.Equal(t, common.UserStatusEnabled, user.Status)
	assert.Equal(t, 0, user.SiteId)
	var member model.AdtyTeamMember
	require.NoError(t, db.Where("team_id = ? AND user_id = ?", team.Id, user.Id).First(&member).Error)
	assert.Equal(t, model.AdtyTeamRoleMember, member.Role)
	assert.Equal(t, model.AdtyTeamMemberActive, member.Status)
	assert.True(t, member.IndependentEmployee)
	ftrAssertQuotaAndNoLedger(t, db, leader.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-13-04: the leader deletes an independent employee and the login is disabled.
func TestFTRADTY1304LeaderDeletesEmployeePG(t *testing.T) {
	db := ftrAccountDB(t)
	leader := model.User{
		Username: "ftr1304-lead", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	employee := model.User{
		Username: "ftr1304emp", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota, AuthVersion: 1,
	}
	require.NoError(t, db.Create(&leader).Error)
	require.NoError(t, db.Create(&employee).Error)
	team := ftrSeedTeam(t, db, leader, "ftr1304")
	require.NoError(t, db.Create(&model.AdtyTeamMember{
		TeamId: team.Id, UserId: employee.Id, Role: model.AdtyTeamRoleMember,
		Status: model.AdtyTeamMemberActive, IndependentEmployee: true,
	}).Error)

	ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/adty/teams/"+strconv.FormatInt(team.Id, 10)+"/members/"+strconv.Itoa(employee.Id)+"/delete", nil, leader.Id)
	ctx.Set("role", leader.Role)
	ctx.Set("username", leader.Username)
	ctx.Params = gin.Params{
		{Key: "id", Value: strconv.FormatInt(team.Id, 10)},
		{Key: "userId", Value: strconv.Itoa(employee.Id)},
	}
	PostAdtyTeamDeleteMember(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)

	var member model.AdtyTeamMember
	require.NoError(t, db.Where("team_id = ? AND user_id = ?", team.Id, employee.Id).First(&member).Error)
	assert.Equal(t, model.AdtyTeamMemberDeleted, member.Status)
	assert.NotZero(t, member.DeletedAt)
	var stored model.User
	require.NoError(t, db.First(&stored, employee.Id).Error)
	assert.Equal(t, common.UserStatusDisabled, stored.Status)
	assert.Greater(t, stored.AuthVersion, int64(1))
	assert.Equal(t, common.RoleCommonUser, leader.Role)
	ftrAssertQuotaAndNoLedger(t, db, leader.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-13-10: the leader leaving closes the team.
func TestFTRADTY1310LeaderLeaveClosesTeamPG(t *testing.T) {
	db := ftrAccountDB(t)
	leader := model.User{
		Username: "ftr1310-lead", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&leader).Error)
	team := ftrSeedTeam(t, db, leader, "ftr1310")

	ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/adty/teams/"+strconv.FormatInt(team.Id, 10)+"/leave", nil, leader.Id)
	ctx.Set("role", leader.Role)
	ctx.Set("username", leader.Username)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(team.Id, 10)}}
	PostAdtyTeamLeave(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)

	var stored model.AdtyTeam
	require.NoError(t, db.First(&stored, team.Id).Error)
	assert.Equal(t, model.AdtyTeamStatusClosed, stored.Status)
	assert.NotZero(t, stored.ClosedAt)
	var member model.AdtyTeamMember
	require.NoError(t, db.Where("team_id = ? AND user_id = ?", team.Id, leader.Id).First(&member).Error)
	assert.Equal(t, model.AdtyTeamMemberLeft, member.Status)
	assert.Equal(t, common.RoleCommonUser, leader.Role)
	ftrAssertQuotaAndNoLedger(t, db, leader.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
