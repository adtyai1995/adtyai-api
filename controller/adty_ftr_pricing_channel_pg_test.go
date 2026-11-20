package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY0103PricingPagePG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Vendor{}))
	oldUsable := setting.UserUsableGroups2JSONString()
	oldRatio := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldUsable))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatio))
		model.InvalidatePricingCache()
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default group"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))

	viewer := model.User{
		Username: "ftr0103-viewer", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&viewer).Error)
	require.Equal(t, common.RoleCommonUser, viewer.Role)
	channel := model.Channel{
		Name: "ftr-0103-channel", Type: 1, Key: "sk-ftr-0103", Models: "ftr-0103-public",
		Group: "default", Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: "ftr-0103-public", ChannelId: channel.Id, Enabled: true,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "ftr-hidden", Model: "ftr-0103-hidden", ChannelId: channel.Id, Enabled: true,
	}).Error)
	model.InvalidatePricingCache()

	ctx, rec := newAuthenticatedContext(t, http.MethodGet, "/api/pricing", nil, viewer.Id)
	ctx.Set("role", viewer.Role)
	GetPricing(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr-0103-public")
	assert.NotContains(t, body, "ftr-0103-hidden")
	assert.Contains(t, body, `"usable_group"`)
	assert.Equal(t, common.RoleCommonUser, viewer.Role)
	ftrAssertQuotaAndNoLedger(t, db, viewer.Id, ftrAccountQuota)
}

func TestFTRADTY0509AddChannelPG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	admin := model.User{
		Username: "ftr0509-admin", Password: "hashed-password", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&admin).Error)
	require.Equal(t, common.RoleAdminUser, admin.Role)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/channel/", strings.NewReader(`{"mode":"single","channel":{"type":1,"name":"ftr-0509-channel","key":"sk-ftr-0509","models":"ftr-0509-model","group":"default","status":1}}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", admin.Id)
	ctx.Set("role", admin.Role)
	ctx.Set("username", admin.Username)
	AddChannel(ctx)
	response := rec.Body.String()
	require.Contains(t, response, `"success":true`, response)
	assert.NotContains(t, response, "sk-ftr-0509")

	var stored model.Channel
	require.NoError(t, db.Where("name = ?", "ftr-0509-channel").First(&stored).Error)
	assert.Equal(t, 1, stored.Type)
	assert.Equal(t, "ftr-0509-model", stored.Models)
	assert.Equal(t, "default", stored.Group)
	assert.Equal(t, "sk-ftr-0509", stored.Key)
	var abilities int64
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ? AND model = ? AND enabled = ?", stored.Id, "ftr-0509-model", true).Count(&abilities).Error)
	assert.Equal(t, int64(1), abilities)
	assert.Equal(t, common.RoleAdminUser, admin.Role)
	ftrAssertQuotaAndNoLedger(t, db, admin.Id, ftrAccountQuota)
}

func TestFTRADTY1701GlobalUserListPG(t *testing.T) {
	db := ftrAccountDB(t)
	admin := model.User{
		Username: "ftr1701-admin", Password: "hashed-password", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	customer := model.User{
		Username: "ftr1701-customer", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	root := model.User{
		Username: "ftr1701-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&admin).Error)
	require.NoError(t, db.Create(&customer).Error)
	require.NoError(t, db.Create(&root).Error)
	require.Equal(t, common.RoleAdminUser, admin.Role)

	ctx, rec := newAuthenticatedContext(t, http.MethodGet, "/api/user/?p=1&page_size=10", nil, admin.Id)
	ctx.Set("role", admin.Role)
	GetAllUsers(ctx)
	body := rec.Body.String()
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr1701-customer")
	assert.NotContains(t, body, "ftr1701-root")
	assert.Contains(t, body, `"total":1`)
	assert.Equal(t, common.RoleAdminUser, admin.Role)
	ftrAssertQuotaAndNoLedger(t, db, admin.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, customer.Id, ftrAccountQuota)
}

func TestFTRADTY2308ReadyAndStatusPG(t *testing.T) {
	db := ftrAccountDB(t)
	bystander := model.User{
		Username: "ftr2308-user", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&bystander).Error)

	readyCtx, readyRec := newAuthenticatedContext(t, http.MethodGet, "/api/ready", nil, 0)
	GetReady(readyCtx)
	readyBody := readyRec.Body.String()
	require.Equal(t, http.StatusOK, readyRec.Code, readyBody)
	require.Contains(t, readyBody, `"success":true`, readyBody)

	statusCtx, statusRec := newAuthenticatedContext(t, http.MethodGet, "/api/status", nil, 0)
	GetStatus(statusCtx)
	statusBody := statusRec.Body.String()
	require.Equal(t, http.StatusOK, statusRec.Code, statusBody)
	require.Contains(t, statusBody, `"success":true`, statusBody)
	assert.Contains(t, statusBody, `"system_name"`)
	ftrAssertQuotaAndNoLedger(t, db, bystander.Id, ftrAccountQuota)
}
