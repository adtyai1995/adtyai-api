package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-13-08: a team quote ignores an enabled payment-method bonus.
// A personal quote for the same face and method records the bonus.
// The team order stores bonus 0 and does not credit shared quota.
func TestFTRADTY1308TeamTopupSkipsMethodBonusPG(t *testing.T) {
	db := ftrRound1PG(t)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.AdtyTopupOrder{}, &model.AdtyTeamLedger{}))
	require.True(t, common.QuotaPerUnitDecimal().Equal(decimal.NewFromInt(500000)))
	require.Equal(t, 7.3, operation_setting.Price)
	leader := ftrRound1User(t, db, "ftr1308-leader", common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, leader.Role)
	require.Zero(t, leader.SiteId)
	ftrRound1PinQuota(t, db, leader.Id)

	raw, err := adty_setting.MergeMainMethodBonus("", "alipay", true, "0.10")
	require.NoError(t, err)
	require.NoError(t, adty_setting.ApplyMethodBonusOption(adty_setting.TopupSettingKeyMethodBonus, raw))

	personal, err := AdtyQuoteFor("default", 100, "alipay")
	require.NoError(t, err)
	require.NotNil(t, personal)
	assert.True(t, personal.BonusRate.Equal(decimal.RequireFromString("0.10")))
	assert.True(t, personal.BonusUSD.Equal(decimal.RequireFromString("10")))
	assert.EqualValues(t, 55_000_000, personal.CreditQuota)

	teamQuote, err := adtyTeamQuote("default", 100, "alipay")
	require.NoError(t, err)
	require.NotNil(t, teamQuote)
	assert.True(t, teamQuote.FaceUSD.Equal(decimal.NewFromInt(100)))
	assert.True(t, teamQuote.BonusRate.IsZero())
	assert.True(t, teamQuote.BonusUSD.IsZero())
	assert.EqualValues(t, 50_000_000, teamQuote.CreditQuota)
	assert.Equal(t, AdtyCurrencyCNY, teamQuote.PayCurrency)
	assert.True(t, teamQuote.PayAmount.Equal(decimal.RequireFromString("730.00")))

	createdTeam, err := CreateAdtyTeam(leader.Id, 0, "ftr1308", decimal.NewFromInt(1))
	require.NoError(t, err)
	order, err := CreateAdtyTeamTopupOrder(leader.Id, createdTeam.Team.Id, decimal.NewFromInt(100), "ftr1308-key")
	require.NoError(t, err)
	require.NotNil(t, order)
	stored, err := model.GetAdtyTopupOrder(order.TradeNo)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, createdTeam.Team.Id, stored.TeamId)
	assert.True(t, stored.BonusUSD.IsZero())
	assert.True(t, stored.BonusRate.IsZero())
	assert.EqualValues(t, 50_000_000, stored.CreditQuota)
	assert.True(t, stored.FaceUSD.Equal(decimal.NewFromInt(100)))

	var team model.AdtyTeam
	require.NoError(t, db.First(&team, createdTeam.Team.Id).Error)
	assert.Zero(t, team.SharedQuota)
	var teamLedgers int64
	require.NoError(t, db.Model(&model.AdtyTeamLedger{}).Count(&teamLedgers).Error)
	assert.Zero(t, teamLedgers)
	ftrRound1AssertNoMoney(t, db, leader.Id)
}
