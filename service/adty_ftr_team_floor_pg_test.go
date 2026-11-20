package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ftrEnableResellerForPrice(t *testing.T) {
	t.Helper()
	saved := *adty_setting.GetResellerSetting()
	t.Cleanup(func() {
		*adty_setting.GetResellerSetting() = saved
		InvalidateAdtySiteCache()
	})
	cfg := adty_setting.GetResellerSetting()
	*cfg = adty_setting.DefaultResellerSetting()
	cfg.Enabled = true
	cfg.CostFactor = new(0.8)
	cfg.MinCostFactor = new(0.5)
	cfg.MaxMarkup = new(3.0)
	InvalidateAdtySiteCache()
}

func ftrTeamPriceContext(t *testing.T, tokenID int) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(ctx, constant.ContextKeyTokenId, tokenID)
	return ctx
}

// ADTY-11-04: a subsite team discount below the supply price is raised to that floor.
func TestFTRADTY1104SubsiteTeamPriceKeepsSupplyFloorPG(t *testing.T) {
	db := ftrRound1PG(t)
	require.NoError(t, db.AutoMigrate(&model.AdtyTeamKey{}, &model.Token{}))
	ftrEnableResellerForPrice(t)
	owner := ftrRound1User(t, db, "ftr1104-owner", common.RoleCommonUser)
	customer := ftrRound1User(t, db, "ftr1104-customer", common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, customer.Role)
	cost := decimal.RequireFromString("0.80")
	site := ftrWalletSite(t, db, owner.Id, "ftr1104")
	require.NoError(t, db.Model(&site).Update("cost_factor", cost).Error)
	require.NoError(t, db.Model(&customer).Update("site_id", site.Id).Error)
	InvalidateAdtySiteCache()
	team := model.AdtyTeam{
		SiteId: site.Id, LeaderUserId: customer.Id, Name: "ftr1104",
		Discount: decimal.RequireFromString("0.50"), Status: model.AdtyTeamStatusActive, SharedQuota: 0,
	}
	require.NoError(t, db.Create(&team).Error)
	require.Positive(t, team.SiteId)
	token := model.Token{UserId: customer.Id, Key: "ftr1104-key", Status: common.TokenStatusEnabled, ExpiredTime: -1}
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, db.Create(&model.AdtyTeamKey{
		TokenId: token.Id, TeamId: team.Id, UserId: customer.Id, Status: model.AdtyTeamMemberActive,
	}).Error)
	ftrRound1PinQuota(t, db, owner.Id)
	ftrRound1PinQuota(t, db, customer.Id)

	marked := decimal.RequireFromString("1.20")
	got, _ := adtyApplyTeamProtocolPriceDecimal(ftrTeamPriceContext(t, token.Id), customer.Id, decimal.NewFromInt(1), marked, marked)
	assert.True(t, got.Equal(decimal.RequireFromString("0.80")))
	var stored model.AdtyTeam
	require.NoError(t, db.First(&stored, team.Id).Error)
	assert.True(t, stored.Discount.Equal(decimal.RequireFromString("0.50")))
	assert.EqualValues(t, 0, stored.SharedQuota)
	ftrRound1AssertNoMoney(t, db, owner.Id)
	ftrRound1AssertNoMoney(t, db, customer.Id)
}

// ADTY-11-06: a main-site team discount is not raised to a supply floor.
func TestFTRADTY1106MainTeamPriceHasNoSupplyFloorPG(t *testing.T) {
	db := ftrRound1PG(t)
	require.NoError(t, db.AutoMigrate(&model.AdtyTeamKey{}, &model.Token{}))
	leader := ftrRound1User(t, db, "ftr1106-leader", common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, leader.Role)
	team := model.AdtyTeam{
		SiteId: 0, LeaderUserId: leader.Id, Name: "ftr1106",
		Discount: decimal.RequireFromString("0.50"), Status: model.AdtyTeamStatusActive, SharedQuota: 0,
	}
	require.NoError(t, db.Create(&team).Error)
	require.Equal(t, 0, team.SiteId)
	token := model.Token{UserId: leader.Id, Key: "ftr1106-key", Status: common.TokenStatusEnabled, ExpiredTime: -1}
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, db.Create(&model.AdtyTeamKey{
		TokenId: token.Id, TeamId: team.Id, UserId: leader.Id, Status: model.AdtyTeamMemberActive,
	}).Error)
	ftrRound1PinQuota(t, db, leader.Id)

	ctx := ftrTeamPriceContext(t, token.Id)
	marked := decimal.RequireFromString("1.20")
	got, _ := adtyApplyTeamProtocolPriceDecimal(ctx, leader.Id, decimal.NewFromInt(1), marked, marked)
	assert.True(t, got.Equal(decimal.RequireFromString("0.60")))
	assert.False(t, AdtyTeamPriceBlocked(ctx))
	assert.InDelta(t, 0.50, ctx.GetFloat64(AdtyTeamDiscountContextKey), 1e-12)
	var stored model.AdtyTeam
	require.NoError(t, db.First(&stored, team.Id).Error)
	assert.Equal(t, 0, stored.SiteId)
	assert.EqualValues(t, 0, stored.SharedQuota)
	ftrRound1AssertNoMoney(t, db, leader.Id)
}
