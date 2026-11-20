package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-07-10: a team key pre-charge debits shared quota and leaves personal quota unchanged.
// POST /v1/chat/completions reaches PreConsumeBilling, which calls the team reserve hook.
func TestFTRADTY0710TeamKeyPreConsumeDebitsSharedQuotaPG(t *testing.T) {
	db := ftrPreconsumeDB(t)
	require.NoError(t, db.AutoMigrate(&model.AdtyTeamKey{}, &model.AdtyTeamLedger{}))
	leader := ftrRound1User(t, db, "ftr0710-leader", common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, leader.Role)
	require.Zero(t, leader.SiteId)
	ftrRound1PinQuota(t, db, leader.Id)

	created, err := CreateAdtyTeam(leader.Id, 0, "ftr0710", decimal.NewFromInt(1))
	require.NoError(t, err)
	require.NotNil(t, created)
	require.NoError(t, CreditAdtyTeam(int64(leader.Id), created.Team.Id, 100000, "ftr0710-credit"))
	token := model.Token{
		UserId: leader.Id, Key: "ftr0710-key", Name: "ftr0710", Status: common.TokenStatusEnabled,
		RemainQuota: 100000, UsedQuota: 0, UnlimitedQuota: false, ExpiredTime: -1, OwnerSelfUseSiteId: 0,
	}
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, BindAdtyTeamKey(leader.Id, token.Id, created.Team.Id))

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		UserId: leader.Id, TokenId: token.Id, TokenKey: token.Key, TokenUnlimited: false,
		ForcePreConsume: true, RequestId: "ftr0710-reserve", UserGroup: "default", UsingGroup: "default",
	}
	require.Nil(t, PreConsumeBilling(ctx, 10000, info))

	var storedUser model.User
	require.NoError(t, db.Select("id", "quota", "role").First(&storedUser, leader.Id).Error)
	assert.Equal(t, common.RoleCommonUser, storedUser.Role)
	assert.Equal(t, 500000, storedUser.Quota)
	var team model.AdtyTeam
	require.NoError(t, db.First(&team, created.Team.Id).Error)
	assert.EqualValues(t, 90000, team.SharedQuota)
	var ledgers []model.AdtyTeamLedger
	require.NoError(t, db.Where("team_id = ?", created.Team.Id).Order("id ASC").Find(&ledgers).Error)
	require.Len(t, ledgers, 2)
	assert.Equal(t, "reserve", ledgers[1].Type)
	assert.EqualValues(t, -10000, ledgers[1].DeltaQuota)
	assert.EqualValues(t, 100000, ledgers[1].BalanceBefore)
	assert.EqualValues(t, 90000, ledgers[1].BalanceAfter)
	assert.Equal(t, leader.Id, ledgers[1].ActorUserId)
	op, err := model.GetBillingOperation("request:ftr0710-reserve")
	require.NoError(t, err)
	require.NotNil(t, op)
	assert.Equal(t, model.BillingPayerTeam, op.PayerKind)
	assert.Equal(t, created.Team.Id, op.PayerTeamId)
	assert.Equal(t, 10000, op.ReservedQuota)
	var balanceLedgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&balanceLedgers).Error)
	assert.Zero(t, balanceLedgers)
}
