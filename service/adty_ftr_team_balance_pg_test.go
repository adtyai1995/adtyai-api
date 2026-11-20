package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-13-06: the leader credits team shared quota and a member spend debits that quota.
// GET /api/adty/teams/mine reads the same shared balance through GetAdtyTeamMine.
// Personal quota and user ledgers stay unchanged.
func TestFTRADTY1306LeaderCreditsAndSpendsSharedQuotaPG(t *testing.T) {
	db := ftrRound1PG(t)
	require.NoError(t, db.AutoMigrate(&model.AdtyTeamLedger{}, &model.AdtyTeamKey{}))
	leader := ftrRound1User(t, db, "ftr1306-leader", common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, leader.Role)
	require.Zero(t, leader.SiteId)
	require.Zero(t, leader.StaffSiteId)
	ftrRound1PinQuota(t, db, leader.Id)

	created, err := CreateAdtyTeam(leader.Id, 0, "ftr1306", decimal.NewFromInt(1))
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, leader.Id, created.Team.LeaderUserId)
	assert.Equal(t, model.AdtyTeamStatusActive, created.Team.Status)
	assert.Equal(t, model.AdtyTeamRoleLeader, created.Member.Role)
	assert.True(t, created.Team.Discount.Equal(decimal.NewFromInt(1)))
	assert.Zero(t, created.Team.SharedQuota)

	require.NoError(t, CreditAdtyTeam(int64(leader.Id), created.Team.Id, 1_000_000, "ftr1306-credit"))
	require.Error(t, CreditAdtyTeam(int64(leader.Id), created.Team.Id, 1_000_000, "ftr1306-credit"))

	mine, err := GetAdtyTeamMine(leader.Id)
	require.NoError(t, err)
	require.NotNil(t, mine)
	assert.Equal(t, created.Team.Id, mine.TeamId)
	assert.Equal(t, model.AdtyTeamRoleLeader, mine.Role)
	assert.Zero(t, mine.SiteId)
	assert.EqualValues(t, 1_000_000, mine.SharedQuota)

	require.NoError(t, SpendAdtyTeam(leader.Id, 250_000, "ftr1306-spend"))
	require.Error(t, SpendAdtyTeam(leader.Id, 250_000, "ftr1306-spend"))

	var stored model.AdtyTeam
	require.NoError(t, db.First(&stored, created.Team.Id).Error)
	assert.EqualValues(t, 750_000, stored.SharedQuota)
	var ledgers []model.AdtyTeamLedger
	require.NoError(t, db.Where("team_id = ?", created.Team.Id).Order("id ASC").Find(&ledgers).Error)
	require.Len(t, ledgers, 2)
	assert.Equal(t, "credit", ledgers[0].Type)
	assert.EqualValues(t, 1_000_000, ledgers[0].DeltaQuota)
	assert.EqualValues(t, 0, ledgers[0].BalanceBefore)
	assert.EqualValues(t, 1_000_000, ledgers[0].BalanceAfter)
	assert.Equal(t, leader.Id, ledgers[0].ActorUserId)
	assert.Equal(t, "spend", ledgers[1].Type)
	assert.EqualValues(t, -250_000, ledgers[1].DeltaQuota)
	assert.EqualValues(t, 1_000_000, ledgers[1].BalanceBefore)
	assert.EqualValues(t, 750_000, ledgers[1].BalanceAfter)
	assert.Equal(t, leader.Id, ledgers[1].ActorUserId)
	ftrRound1AssertNoMoney(t, db, leader.Id)
}
