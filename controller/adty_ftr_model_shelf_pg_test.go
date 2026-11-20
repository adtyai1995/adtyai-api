package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ftrCreateModel(t *testing.T, admin model.User, name string, contextLength int) model.Model {
	t.Helper()
	ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/models/", map[string]any{
		"model_name": name, "status": 1, "name_rule": 0, "context_length": contextLength,
	}, admin.Id)
	ctx.Set("role", admin.Role)
	ctx.Set("username", admin.Username)
	CreateModelMeta(ctx)
	require.Contains(t, rec.Body.String(), `"success":true`, rec.Body.String())
	var stored model.Model
	require.NoError(t, model.DB.Where("model_name = ?", name).First(&stored).Error)
	return stored
}

func TestFTRADTY0502ShelfModelPG(t *testing.T) {
	db := ftrAccountDB(t)
	admin := model.User{
		Username: "ftr0502-admin", Password: "hashed-password", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&admin).Error)
	require.Equal(t, common.RoleAdminUser, admin.Role)

	stored := ftrCreateModel(t, admin, "ftr-0502-model", 4096)
	assert.True(t, model.CatalogModelCallable("ftr-0502-model"))

	offCtx, offRec := newAuthenticatedContext(t, http.MethodPut, "/api/models/?status_only=true", map[string]any{
		"id": stored.Id, "status": 0,
	}, admin.Id)
	offCtx.Set("role", admin.Role)
	offCtx.Set("username", admin.Username)
	UpdateModelMeta(offCtx)
	offBody := offRec.Body.String()
	require.Contains(t, offBody, `"success":true`, offBody)
	var shelved model.Model
	require.NoError(t, db.First(&shelved, stored.Id).Error)
	assert.Equal(t, 0, shelved.Status)
	assert.Equal(t, "ftr-0502-model", shelved.ModelName)
	assert.Equal(t, 4096, shelved.ContextLength)
	assert.False(t, model.CatalogModelCallable("ftr-0502-model"))

	onCtx, onRec := newAuthenticatedContext(t, http.MethodPut, "/api/models/?status_only=true", map[string]any{
		"id": stored.Id, "status": 1,
	}, admin.Id)
	onCtx.Set("role", admin.Role)
	onCtx.Set("username", admin.Username)
	UpdateModelMeta(onCtx)
	require.Contains(t, onRec.Body.String(), `"success":true`, onRec.Body.String())
	var restored model.Model
	require.NoError(t, db.First(&restored, stored.Id).Error)
	assert.Equal(t, 1, restored.Status)
	assert.True(t, model.CatalogModelCallable("ftr-0502-model"))
	assert.Equal(t, common.RoleAdminUser, admin.Role)
	ftrAssertQuotaAndNoLedger(t, db, admin.Id, ftrAccountQuota)
}

func TestFTRADTY0505ModelContextPG(t *testing.T) {
	db := ftrAccountDB(t)
	admin := model.User{
		Username: "ftr0505-admin", Password: "hashed-password", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&admin).Error)
	require.Equal(t, common.RoleAdminUser, admin.Role)

	stored := ftrCreateModel(t, admin, "ftr-0505-model", 4096)
	ctx, rec := newAuthenticatedContext(t, http.MethodPut, "/api/models/", map[string]any{
		"id": stored.Id, "model_name": stored.ModelName, "status": 1, "name_rule": 0, "context_length": 8192,
	}, admin.Id)
	ctx.Set("role", admin.Role)
	ctx.Set("username", admin.Username)
	UpdateModelMeta(ctx)
	body := rec.Body.String()
	require.Contains(t, body, `"success":true`, body)

	var updated model.Model
	require.NoError(t, db.First(&updated, stored.Id).Error)
	assert.Equal(t, 8192, updated.ContextLength)
	assert.Equal(t, 1, updated.Status)
	assert.Equal(t, "ftr-0505-model", updated.ModelName)
	assert.True(t, model.CatalogModelCallable(updated.ModelName))
	assert.Equal(t, common.RoleAdminUser, admin.Role)
	ftrAssertQuotaAndNoLedger(t, db, admin.Id, ftrAccountQuota)
}
