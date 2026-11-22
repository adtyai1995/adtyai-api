package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-13-07: the leader creates a pending team top-up order.
// POST /api/adty/teams/:id/topups creates a payable order on the selected channel.
// The order records the face and credit quota and does not credit the shared balance.
func TestFTRADTY1307LeaderCreatesTeamTopupOrderPG(t *testing.T) {
	db := ftrRound1PG(t)
	channel := seedTeamCheckoutChannel(t, db)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.AdtyTopupOrder{}, &model.AdtyTeamLedger{}))
	require.True(t, common.QuotaPerUnitDecimal().Equal(decimal.NewFromInt(500000)))
	leader := ftrRound1User(t, db, "ftr1307-leader", common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, leader.Role)
	require.Zero(t, leader.SiteId)
	ftrRound1PinQuota(t, db, leader.Id)

	createdTeam, err := CreateAdtyTeam(leader.Id, 0, "ftr1307", decimal.NewFromInt(1))
	require.NoError(t, err)
	face := decimal.RequireFromString("20")
	order, err := StartAdtyTeamTopupPayment(leader.Id, createdTeam.Team.Id, face, "alipay", channel.Id, "ftr1307-key", "127.0.0.1")
	require.NoError(t, err)
	require.NotNil(t, order)
	replay, err := StartAdtyTeamTopupPayment(leader.Id, createdTeam.Team.Id, face, "alipay", channel.Id, "ftr1307-key", "127.0.0.1")
	require.NoError(t, err)
	require.NotNil(t, replay)
	assert.Equal(t, order.TradeNo, replay.TradeNo)

	stored, err := model.GetAdtyTopupOrder(order.TradeNo)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, createdTeam.Team.Id, stored.TeamId)
	assert.Equal(t, leader.Id, stored.UserId)
	assert.Zero(t, stored.SiteId)
	assert.Equal(t, "alipay", stored.Method)
	assert.Equal(t, channel.Id, stored.ChannelId)
	assert.NotEmpty(t, stored.PayURL)
	assert.Equal(t, model.PaymentProviderAdtyEpay, stored.Provider)
	assert.True(t, stored.FaceUSD.Equal(face))
	assert.True(t, stored.BonusUSD.IsZero())
	assert.True(t, stored.BonusRate.IsZero())
	assert.EqualValues(t, 10_000_000, stored.CreditQuota)
	assert.Equal(t, "CNY", stored.PayCurrency)
	assert.True(t, stored.PayAmount.Equal(face.Mul(stored.PriceSnapshot)))

	var topUp model.TopUp
	require.NoError(t, db.Where("trade_no = ?", order.TradeNo).First(&topUp).Error)
	assert.Equal(t, common.TopUpStatusPending, topUp.Status)
	assert.Equal(t, "alipay", topUp.PaymentMethod)
	assert.Zero(t, topUp.CompleteTime)
	var orders int64
	require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Count(&orders).Error)
	assert.EqualValues(t, 1, orders)

	listed, err := ListAdtyTeamTopups(leader.Id, createdTeam.Team.Id)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, order.TradeNo, listed[0].TradeNo)

	var team model.AdtyTeam
	require.NoError(t, db.First(&team, createdTeam.Team.Id).Error)
	assert.Zero(t, team.SharedQuota)
	var teamLedgers int64
	require.NoError(t, db.Model(&model.AdtyTeamLedger{}).Count(&teamLedgers).Error)
	assert.Zero(t, teamLedgers)
	ftrRound1AssertNoMoney(t, db, leader.Id)
}
