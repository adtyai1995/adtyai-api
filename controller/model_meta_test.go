package controller

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelMetaContextLengthCreateUpdateDetailAndValidation(t *testing.T) {
	db := modelManagementDB(t, "sqlite", "")
	require.NoError(t, db.AutoMigrate(&model.AdtyModelAlias{}))

	// 1. Reject invalid negative context_length on create.
	var badCreate struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	modelManagementRequest(t, CreateModelMeta, http.MethodPost, "/api/models/", map[string]any{
		"model_name":     "ctx-invalid-create",
		"context_length": -100,
		"status":         1,
	}, &badCreate)
	assert.False(t, badCreate.Success)

	// 2. Create with valid context_length, tags, and endpoints.
	var created struct {
		Success bool        `json:"success"`
		Data    model.Model `json:"data"`
	}
	modelManagementRequest(t, CreateModelMeta, http.MethodPost, "/api/models/", map[string]any{
		"model_name":     "ctx-roundtrip-model",
		"description":    "Model with context window",
		"tags":           "Chat,Reasoning",
		"endpoints":      `{"openai":"/v1/chat/completions"}`,
		"context_length": 128000,
		"status":         1,
		"sync_official":  1,
	}, &created)
	require.True(t, created.Success)
	assert.Equal(t, 128000, created.Data.ContextLength)
	assert.Equal(t, "Chat,Reasoning", created.Data.Tags)
	assert.Equal(t, `{"openai":"/v1/chat/completions"}`, created.Data.Endpoints)

	// 3. Reject invalid context_length on update and preserve previous persisted value.
	var badUpdate struct {
		Success bool `json:"success"`
	}
	modelManagementRequest(t, UpdateModelMeta, http.MethodPut, "/api/models/", map[string]any{
		"id":             created.Data.Id,
		"model_name":     "ctx-roundtrip-model",
		"context_length": -1,
		"status":         1,
	}, &badUpdate)
	assert.False(t, badUpdate.Success)

	var detail struct {
		Success bool        `json:"success"`
		Data    model.Model `json:"data"`
	}
	idStr := strconv.Itoa(created.Data.Id)
	modelManagementRequest(t, func(c *gin.Context) {
		c.Params = gin.Params{{Key: "id", Value: idStr}}
		GetModelMeta(c)
	}, http.MethodGet, "/api/models/"+idStr, nil, &detail)
	require.True(t, detail.Success)
	assert.Equal(t, 128000, detail.Data.ContextLength)

	// 4. Update context_length to a new value and verify detail & list endpoints return it.
	var updated struct {
		Success bool        `json:"success"`
		Data    model.Model `json:"data"`
	}
	modelManagementRequest(t, UpdateModelMeta, http.MethodPut, "/api/models/", map[string]any{
		"id":             created.Data.Id,
		"model_name":     "ctx-roundtrip-model",
		"description":    "Updated context window",
		"tags":           "Chat,Reasoning",
		"endpoints":      `{"openai":"/v1/chat/completions"}`,
		"context_length": 256000,
		"status":         1,
		"sync_official":  1,
	}, &updated)
	require.True(t, updated.Success)
	assert.Equal(t, 256000, updated.Data.ContextLength)

	var list struct {
		Success bool `json:"success"`
		Data    struct {
			Items []model.Model `json:"items"`
		} `json:"data"`
	}
	modelManagementRequest(t, SearchModelsMeta, http.MethodGet, "/api/models/search?keyword=ctx-roundtrip-model", nil, &list)
	require.True(t, list.Success)
	require.Len(t, list.Data.Items, 1)
	assert.Equal(t, 256000, list.Data.Items[0].ContextLength)
	assert.Equal(t, "Chat,Reasoning", list.Data.Items[0].Tags)
	assert.Equal(t, `{"openai":"/v1/chat/completions"}`, list.Data.Items[0].Endpoints)
	require.ErrorIs(t, model.CheckModelContextLimit("ctx-roundtrip-model", 256001), model.ErrModelContextExceeded)
}
