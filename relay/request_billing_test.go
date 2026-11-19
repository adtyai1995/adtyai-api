package relay

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// A free model can still incur a violation fee after the upstream response.
// That fee must retain the payer accepted before the token binding changes.
func TestFreeRequestViolationFeeKeepsAcceptedPayerPG(t *testing.T) {
	oldPrices := ratio_setting.ModelPrice2JSONString()
	oldGroups := ratio_setting.GroupRatio2JSONString()
	oldGrok := *model_setting.GetGrokSettings()
	oldQuota := *operation_setting.GetQuotaSetting()
	oldRedis, oldBatch, oldCount := common.RedisEnabled, common.BatchUpdateEnabled, constant.CountToken
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"free-payer-model":0}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	model_setting.GetGrokSettings().ViolationDeductionEnabled = true
	model_setting.GetGrokSettings().ViolationDeductionAmount = 0.04
	operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = false
	common.RedisEnabled, common.BatchUpdateEnabled, constant.CountToken = false, false, false
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroups))
		*model_setting.GetGrokSettings() = oldGrok
		*operation_setting.GetQuotaSetting() = oldQuota
		common.RedisEnabled, common.BatchUpdateEnabled, constant.CountToken = oldRedis, oldBatch, oldCount
	})
	for _, tc := range []struct {
		name         string
		teamPaid     bool
		feeEnabled   bool
		openingQuota int
	}{
		{name: "personal_bind", feeEnabled: true, openingQuota: 500_000},
		{name: "team_rebind", teamPaid: true, feeEnabled: true, openingQuota: 500_000},
		{name: "free_zero_balance", openingQuota: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model_setting.GetGrokSettings().ViolationDeductionEnabled = tc.feeEnabled
			opened, err := model.OpenAdtyTestDatabase()
			require.NoError(t, err, "requires isolated PostgreSQL")
			t.Cleanup(func() { require.NoError(t, opened.Close()) })
			db := opened.DB
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Log{}, &model.Channel{},
				&model.BillingOperation{}, &model.BillingOperationMovement{}, &model.AuditLog{}, &model.AuditIntent{},
				&model.AdtySiteEarningEvent{}, &model.AdtyFundBatch{}, &model.AdtyFundAllocation{}, &model.AdtyBillingHold{}))
			require.NoError(t, model.MigrateAdtyTables())
			users := []model.User{
				{Username: "free-payer-member", Group: "default", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: tc.openingQuota},
				{Username: "free-payer-leader-a", Group: "default", Status: common.UserStatusEnabled, Role: common.RoleCommonUser},
				{Username: "free-payer-leader-b", Group: "default", Status: common.UserStatusEnabled, Role: common.RoleCommonUser},
			}
			require.NoError(t, db.Create(&users).Error)
			member := &users[0]
			a, err := service.CreateAdtyTeam(users[1].Id, 0, "Original", decimal.NewFromInt(1))
			require.NoError(t, err)
			b, err := service.CreateAdtyTeam(users[2].Id, 0, "Later", decimal.NewFromInt(1))
			require.NoError(t, err)
			_, err = service.JoinAdtyTeam(member.Id, a.Team.Id)
			require.ErrorIs(t, err, service.ErrAdtyTeamForbidden)
			employee, err := service.CreateAdtyTeamEmployee(users[1].Id, a.Team.Id, "fee-emp", "StrongPassword123!", "Fee Emp")
			require.NoError(t, err)
			member.Id = employee.UserId
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", member.Id).Update("quota", tc.openingQuota).Error)
			require.NoError(t, service.CreditAdtyTeam(int64(users[1].Id), a.Team.Id, 100_000, "free-original"))
			require.NoError(t, service.CreditAdtyTeam(int64(users[2].Id), b.Team.Id, 200_000, "free-later"))
			token := &model.Token{UserId: member.Id, Key: "free-payer-key", Name: "free-payer", Status: common.TokenStatusEnabled,
				RemainQuota: 1_000_000, UnlimitedQuota: true, ExpiredTime: -1}
			require.NoError(t, token.Insert())
			if tc.teamPaid {
				require.NoError(t, service.BindAdtyTeamKey(member.Id, token.Id))
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{UserId: member.Id, TokenId: token.Id, TokenKey: token.Key,
				TokenUnlimited: true, RequestId: "free-payer-" + tc.name, OriginModelName: "free-payer-model",
				UserGroup: "default", UsingGroup: "default", StartTime: time.Now(), ChannelMeta: &relaycommon.ChannelMeta{}}
			require.Nil(t, PrepareRequestBilling(ctx, info))
			require.True(t, info.PriceData.FreeModel)
			require.NoError(t, db.First(member, member.Id).Error)
			require.Equal(t, tc.openingQuota, member.Quota, "free acceptance must not reserve money")
			if tc.teamPaid {
				require.NoError(t, db.Model(&model.AdtyTeamKey{}).Where("token_id = ?", token.Id).Update("team_id", b.Team.Id).Error)
			} else {
				require.NoError(t, service.BindAdtyTeamKey(member.Id, token.Id))
			}
			apiErr := types.NewError(errors.New("blocked"), types.ErrorCodeViolationFeeGrokCSAM)
			require.NotNil(t, RefundFailedRequestBilling(ctx, info, apiErr))
			require.NoError(t, db.First(member, member.Id).Error)
			require.NoError(t, db.First(&a.Team, a.Team.Id).Error)
			require.NoError(t, db.First(&b.Team, b.Team.Id).Error)
			wantPersonal, wantTeam := tc.openingQuota, int64(100_000)
			if tc.feeEnabled {
				if tc.teamPaid {
					wantTeam -= 20_000
				} else {
					wantPersonal -= 20_000
				}
			}
			require.Equal(t, wantPersonal, member.Quota, "only the accepted personal payer may pay the violation fee")
			require.Equal(t, wantTeam, a.Team.SharedQuota, "only the accepted team may pay the violation fee")
			require.EqualValues(t, 200_000, b.Team.SharedQuota, "a later team never inherits the fee")
			root, err := model.GetBillingOperation("request:" + info.RequestId)
			require.NoError(t, err)
			require.Zero(t, root.ReservedQuota)
			wantKind, wantTeamID := model.BillingPayerPersonal, int64(0)
			if tc.teamPaid {
				wantKind, wantTeamID = model.BillingPayerTeam, a.Team.Id
			}
			require.Equal(t, wantKind, root.PayerKind)
			require.Equal(t, wantTeamID, root.PayerTeamId)
		})
	}
}
