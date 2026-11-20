package relay

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY0701EstimateBeforePreConsumePG(t *testing.T) {
	db := ftr0708DB(t)

	t.Run("negative_preconsume_rejected", func(t *testing.T) {
		user, _, ctx, info := ftr0708Caller(t, db, "ftr0701-neg", "ftr0701-neg")
		require.NotNil(t, service.PreConsumeBilling(ctx, -1, info))
		require.Nil(t, info.Billing)
		var after model.User
		require.NoError(t, db.Select("quota").First(&after, user.Id).Error)
		require.Equal(t, 1_000_000, after.Quota)
		var ops int64
		require.NoError(t, db.Model(&model.BillingOperation{}).Where("operation_key = ?", "request:ftr0701-neg").Count(&ops).Error)
		require.Zero(t, ops)
	})

	t.Run("free_model_freezes_zero", func(t *testing.T) {
		oldPrices := ratio_setting.ModelPrice2JSONString()
		oldGroups := ratio_setting.GroupRatio2JSONString()
		oldQuota := *operation_setting.GetQuotaSetting()
		oldCount := constant.CountToken
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"ftr-0701-free":0}`))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
		operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = false
		constant.CountToken = false
		t.Cleanup(func() {
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices))
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroups))
			*operation_setting.GetQuotaSetting() = oldQuota
			constant.CountToken = oldCount
		})

		user, _, ctx, info := ftr0708Caller(t, db, "ftr0701-free", "ftr0701-free")
		info.OriginModelName = "ftr-0701-free"
		require.Nil(t, PrepareRequestBilling(ctx, info))
		require.True(t, info.PriceData.FreeModel)
		require.Zero(t, info.PriceData.QuotaToPreConsume)
		var after model.User
		require.NoError(t, db.Select("quota").First(&after, user.Id).Error)
		require.Equal(t, 1_000_000, after.Quota)
		op, err := model.GetBillingOperation("request:ftr0701-free")
		require.NoError(t, err)
		require.NotNil(t, op)
		require.Zero(t, op.ReservedQuota)
	})

	t.Run("unset_ratio_errors_before_preconsume", func(t *testing.T) {
		oldPrices := ratio_setting.ModelPrice2JSONString()
		oldRatios := ratio_setting.ModelRatio2JSONString()
		oldGroups := ratio_setting.GroupRatio2JSONString()
		oldSelf := operation_setting.SelfUseModeEnabled
		oldCount := constant.CountToken
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{}`))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
		operation_setting.SelfUseModeEnabled = false
		constant.CountToken = false
		t.Cleanup(func() {
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices))
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatios))
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroups))
			operation_setting.SelfUseModeEnabled = oldSelf
			constant.CountToken = oldCount
		})

		user, _, ctx, info := ftr0708Caller(t, db, "ftr0701-unset", "ftr0701-unset")
		info.OriginModelName = "ftr-0701-unset"
		info.UserSetting.AcceptUnsetRatioModel = false
		meta := &types.TokenCountMeta{}
		_, priceErr := helper.ModelPriceHelper(ctx, info, 0, meta)
		require.Error(t, priceErr)
		apiErr := PrepareRequestBilling(ctx, info)
		require.NotNil(t, apiErr)
		require.Nil(t, info.Billing)
		var after model.User
		require.NoError(t, db.Select("quota").First(&after, user.Id).Error)
		require.Equal(t, 1_000_000, after.Quota)
		var ops int64
		require.NoError(t, db.Model(&model.BillingOperation{}).Where("operation_key = ?", "request:ftr0701-unset").Count(&ops).Error)
		require.Zero(t, ops)
		require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	})
}
