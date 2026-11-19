package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ftrPublicQuota = 500000

func TestFTRADTY0102ModelIntroductionPagePG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	gin.SetMode(gin.TestMode)
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}))

	oldUsable := setting.UserUsableGroups2JSONString()
	oldRatio := ratio_setting.GroupRatio2JSONString()
	oldPrice := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldUsable))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatio))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrice))
		model.InvalidatePricingCache()
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default group"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"ftr-intro-model":2}`))

	owner := model.User{
		Username: "ftr-0102-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrPublicQuota,
	}
	other := model.User{
		Username: "ftr-0102-other", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrPublicQuota,
	}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&other).Error)
	require.NoError(t, db.Create(&model.Model{
		ModelName: "ftr-intro-model", Description: "FTR model introduction", Status: 1,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: "ftr-intro-model", ChannelId: 1, Enabled: true,
	}).Error)
	model.InvalidatePricingCache()

	call := func(userID int) string {
		t.Helper()
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/pricing", nil)
		if userID > 0 {
			ctx.Set("id", userID)
			ctx.Set("role", common.RoleCommonUser)
		}
		GetPricing(ctx)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return rec.Body.String()
	}

	publicBody := call(0)
	require.Contains(t, publicBody, `"success":true`)
	assert.Contains(t, publicBody, "ftr-intro-model")
	assert.Contains(t, publicBody, "FTR model introduction")
	assert.Contains(t, publicBody, `"model_price":2`)

	ownerBody := call(owner.Id)
	assert.Contains(t, ownerBody, "FTR model introduction")
	assert.NotContains(t, ownerBody, "ftr-0102-other")

	for _, id := range []int{owner.Id, other.Id} {
		var quota int
		require.NoError(t, db.Model(&model.User{}).Where("id = ?", id).Select("quota").Scan(&quota).Error)
		assert.Equal(t, ftrPublicQuota, quota)
	}
}

func TestFTRADTY0108SavedLanguagePreferencePG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	gin.SetMode(gin.TestMode)
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	require.NoError(t, db.AutoMigrate(&model.User{}))

	user := model.User{
		Username: "ftr-0108-user", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Quota: ftrPublicQuota, Setting: `{"language":"en"}`,
	}
	require.NoError(t, db.Create(&user).Error)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/user/self", strings.NewReader(`{"language":"zh-TW"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", user.Id)
	ctx.Set("role", user.Role)
	UpdateSelf(ctx)
	require.Contains(t, rec.Body.String(), `"success":true`, rec.Body.String())

	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, ftrPublicQuota, stored.Quota)
	assert.Contains(t, stored.Setting, `"language":"zhCN"`, "unofficial zh-TW is stored as the official Simplified Chinese preference")
	assert.NotContains(t, stored.Setting, "zh-TW")
}
