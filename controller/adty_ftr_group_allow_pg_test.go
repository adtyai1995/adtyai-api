package controller

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-05-07: Root saves a closed group model list through PUT /api/option/.
func TestFTRADTY0507GroupModelAllowPG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	admin := model.User{
		Username: "ftr0507-admin", Password: "hashed-password", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	root := model.User{
		Username: "ftr0507-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&admin).Error)
	require.NoError(t, db.Create(&root).Error)
	require.Equal(t, common.RoleAdminUser, admin.Role)
	require.Equal(t, common.RoleRootUser, root.Role)

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	previous, had := common.OptionMap[model.AdtyGroupModelAllowOption]
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if common.OptionMap == nil {
			common.OptionMap = map[string]string{}
		}
		if had {
			common.OptionMap[model.AdtyGroupModelAllowOption] = previous
		} else {
			delete(common.OptionMap, model.AdtyGroupModelAllowOption)
		}
	})

	createCtx, createRec := newAuthenticatedContext(t, http.MethodPost, "/api/adty/groups", map[string]any{
		"name": "FTR 0507 Group", "desc": "model allow list",
	}, admin.Id)
	createCtx.Set("role", admin.Role)
	createCtx.Set("username", admin.Username)
	PostAdtyGroup(createCtx)
	require.Contains(t, createRec.Body.String(), `"success":true`, createRec.Body.String())
	var groups []model.AdtyGroup
	require.NoError(t, db.Find(&groups).Error)
	var groupID string
	for _, group := range groups {
		if group.Names["en"] == "FTR 0507 Group" {
			groupID = group.ID
		}
	}
	require.NotEmpty(t, groupID)

	allow := fmt.Sprintf("{\"%s\":[\"ftr-0507-model\"]}", groupID)
	ctx, rec := newAuthenticatedContext(t, http.MethodPut, "/api/option/", map[string]any{
		"key": model.AdtyGroupModelAllowOption, "value": allow,
	}, root.Id)
	ctx.Set("role", root.Role)
	ctx.Set("username", root.Username)
	UpdateOption(ctx)
	body := rec.Body.String()
	require.Contains(t, body, `"success":true`, body)

	var stored model.Option
	require.NoError(t, db.Where("key = ?", model.AdtyGroupModelAllowOption).First(&stored).Error)
	assert.Contains(t, stored.Value, groupID)
	assert.Contains(t, stored.Value, "ftr-0507-model")
	assert.True(t, service.GroupModelAllowed(groupID, "ftr-0507-model"))
	assert.False(t, service.GroupModelAllowed(groupID, "ftr-0507-other"))

	badCtx, badRec := newAuthenticatedContext(t, http.MethodPut, "/api/option/", map[string]any{
		"key": model.AdtyGroupModelAllowOption, "value": `{"missing-group":["ftr-0507-model"]}`,
	}, root.Id)
	badCtx.Set("role", root.Role)
	badCtx.Set("username", root.Username)
	UpdateOption(badCtx)
	assert.NotContains(t, badRec.Body.String(), `"success":true`)
	var unchanged model.Option
	require.NoError(t, db.Where("key = ?", model.AdtyGroupModelAllowOption).First(&unchanged).Error)
	assert.Equal(t, stored.Value, unchanged.Value)
	assert.True(t, service.GroupModelAllowed(groupID, "ftr-0507-model"))
	assert.False(t, service.GroupModelAllowed(groupID, "ftr-0507-other"))

	ftrAssertQuotaAndNoLedger(t, db, admin.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, root.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
