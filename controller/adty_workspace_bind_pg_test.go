package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ISS-R1-0118 on isolated PostgreSQL 18: the requested team id must be the
// caller's authorized team. A foreign id, including 999999, must not bind.
func TestPostAdtyTeamBindKeyVerifiesRequestedTeamTargetPG(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.AuditIntent{}, &model.AuditLog{}))
	require.NoError(t, model.MigrateAdtyTablesOn(db))

	leaderA := model.User{Username: "team-bind-pg-leader-a", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	leaderB := model.User{Username: "team-bind-pg-leader-b", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&leaderA).Error)
	require.NoError(t, db.Create(&leaderB).Error)

	teamA, err := service.CreateAdtyTeam(leaderA.Id, 0, "Team A PG", decimal.NewFromInt(1))
	require.NoError(t, err)
	teamB, err := service.CreateAdtyTeam(leaderB.Id, 0, "Team B PG", decimal.NewFromInt(1))
	require.NoError(t, err)
	employee, err := service.CreateAdtyTeamEmployee(leaderA.Id, teamA.Team.Id, "bind-emp-pg", "StrongPassword123!", "Bind Emp PG")
	require.NoError(t, err)

	var quotaBefore int
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", employee.UserId).Select("quota").Scan(&quotaBefore).Error)

	token := model.Token{
		UserId:         employee.UserId,
		Key:            "team-bind-pg-token-key",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}
	require.NoError(t, db.Create(&token).Error)

	callBind := func(userID int, targetTeamID int64, tokenID int) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		body := fmt.Sprintf(`{"token_id":%d}`, tokenID)
		c.Request = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/adty/teams/%d/keys", targetTeamID), strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", targetTeamID)}}
		c.Set("id", userID)
		PostAdtyTeamBindKey(c)
		var resp map[string]any
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &resp), w.Body.String())
		return resp
	}

	mismatchResp := callBind(employee.UserId, teamB.Team.Id, token.Id)
	assert.Equal(t, false, mismatchResp["success"], mismatchResp)
	var boundCount int64
	require.NoError(t, db.Model(&model.AdtyTeamKey{}).Where("token_id = ?", token.Id).Count(&boundCount).Error)
	assert.Zero(t, boundCount)

	missingResp := callBind(employee.UserId, 999999, token.Id)
	assert.Equal(t, false, missingResp["success"], missingResp)
	require.NoError(t, db.Model(&model.AdtyTeamKey{}).Where("token_id = ?", token.Id).Count(&boundCount).Error)
	assert.Zero(t, boundCount)

	var quotaAfter int
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", employee.UserId).Select("quota").Scan(&quotaAfter).Error)
	assert.Equal(t, quotaBefore, quotaAfter, "rejected team bind must not change the caller quota")

	okResp := callBind(employee.UserId, teamA.Team.Id, token.Id)
	require.Equal(t, true, okResp["success"], okResp)
	var teamKey model.AdtyTeamKey
	require.NoError(t, db.Where("token_id = ?", token.Id).First(&teamKey).Error)
	assert.Equal(t, teamA.Team.Id, teamKey.TeamId)
	assert.Equal(t, employee.UserId, teamKey.UserId)
	assert.Equal(t, model.AdtyTeamMemberActive, teamKey.Status)

	replayResp := callBind(employee.UserId, teamA.Team.Id, token.Id)
	assert.Equal(t, false, replayResp["success"], replayResp)
	require.NoError(t, db.Model(&model.AdtyTeamKey{}).Where("token_id = ?", token.Id).Count(&boundCount).Error)
	assert.EqualValues(t, 1, boundCount)
}
