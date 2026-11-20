package controller

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-13-09: the leader binds one enabled token to the team, then revokes it.
// POST /api/adty/teams/:id/keys and POST /api/adty/teams/:id/keys/:tokenId/revoke.
// Binding does not move personal quota or team shared quota.
func TestFTRADTY1309LeaderBindsAndRevokesTeamKeyPG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.AdtyTeam{}, &model.AdtyTeamMember{}, &model.AdtyTeamLedger{}, &model.AdtyTeamKey{}, &model.Token{},
	))
	leader := model.User{
		Username: "ftr1309-leader", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&leader).Error)
	require.Equal(t, common.RoleCommonUser, leader.Role)
	require.Zero(t, leader.SiteId)
	require.Zero(t, leader.StaffSiteId)

	created, err := service.CreateAdtyTeam(leader.Id, 0, "ftr1309", decimal.NewFromInt(1))
	require.NoError(t, err)
	require.NotNil(t, created)
	token := &model.Token{
		UserId: leader.Id, Key: "ftr1309-key", Name: "ftr1309", Status: common.TokenStatusEnabled,
		ExpiredTime: -1, RemainQuota: 1000, UnlimitedQuota: false, OwnerSelfUseSiteId: 0,
	}
	require.NoError(t, db.Create(token).Error)
	require.Positive(t, token.Id)

	teamID := strconv.FormatInt(created.Team.Id, 10)
	body := map[string]int{"token_id": token.Id}
	ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/adty/teams/"+teamID+"/keys", body, leader.Id)
	ctx.Params = gin.Params{{Key: "id", Value: teamID}}
	ctx.Set("role", leader.Role)
	ctx.Set("username", leader.Username)
	PostAdtyTeamBindKey(ctx)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"bound":true`)

	ctx, rec = newAuthenticatedContext(t, http.MethodPost, "/api/adty/teams/"+teamID+"/keys", body, leader.Id)
	ctx.Params = gin.Params{{Key: "id", Value: teamID}}
	ctx.Set("role", leader.Role)
	PostAdtyTeamBindKey(ctx)
	assert.NotContains(t, rec.Body.String(), `"bound":true`)

	var bound model.AdtyTeamKey
	require.NoError(t, db.Where("token_id = ?", token.Id).First(&bound).Error)
	assert.Equal(t, created.Team.Id, bound.TeamId)
	assert.Equal(t, leader.Id, bound.UserId)
	assert.Equal(t, model.AdtyTeamMemberActive, bound.Status)
	var keyRows int64
	require.NoError(t, db.Model(&model.AdtyTeamKey{}).Count(&keyRows).Error)
	assert.EqualValues(t, 1, keyRows)

	tokenID := strconv.Itoa(token.Id)
	ctx, rec = newAuthenticatedContext(t, http.MethodPost, "/api/adty/teams/"+teamID+"/keys/"+tokenID+"/revoke", nil, leader.Id)
	ctx.Params = gin.Params{{Key: "id", Value: teamID}, {Key: "tokenId", Value: tokenID}}
	ctx.Set("role", leader.Role)
	PostAdtyTeamRevokeKey(ctx)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"revoked":true`)

	require.NoError(t, db.Where("token_id = ?", token.Id).First(&bound).Error)
	assert.Equal(t, model.AdtyTeamMemberLeft, bound.Status)
	require.NoError(t, db.Model(&model.AdtyTeamKey{}).Count(&keyRows).Error)
	assert.EqualValues(t, 1, keyRows)
	var storedToken model.Token
	require.NoError(t, db.First(&storedToken, token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, storedToken.Status)
	assert.Equal(t, 1000, storedToken.RemainQuota)
	var team model.AdtyTeam
	require.NoError(t, db.First(&team, created.Team.Id).Error)
	assert.Zero(t, team.SharedQuota)
	var teamLedgers int64
	require.NoError(t, db.Model(&model.AdtyTeamLedger{}).Count(&teamLedgers).Error)
	assert.Zero(t, teamLedgers)
	ftrAssertQuotaAndNoLedger(t, db, leader.Id, ftrAccountQuota)
}
