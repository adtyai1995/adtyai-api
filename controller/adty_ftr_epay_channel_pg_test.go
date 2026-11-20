package controller

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-08-07: root creates an epay channel, enables it, and selection returns that channel.
// Routes: POST /api/adty/admin/epay/channels and POST /api/adty/admin/epay/channels/:id/status.
func TestFTRADTY0807RootConfiguresAndPicksEpayChannelPG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(&model.AdtyEpayChannel{}, &model.AdtyEpayChannelMethod{}))
	root := model.User{
		Username: "ftr0807-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&root).Error)
	require.Equal(t, common.RoleRootUser, root.Role)

	createBody := map[string]any{
		"name": "ftr0807", "gateway_url": "https://pay.example.test", "merchant_id": "ftr0807-pid",
		"merchant_key": "ftr0807-secret", "priority": 4, "weight": 1, "order_mode": "api",
		"methods": []map[string]any{{
			"method": "alipay", "display_name": "支付宝1", "type_code": "alipay",
			"min_cny": "1.00", "max_cny": "1000.00", "daily_limit_cny": "0.00",
			"enabled": true, "return_type": "qrcode",
		}},
	}
	ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/adty/admin/epay/channels", createBody, root.Id)
	ctx.Set("role", root.Role)
	ctx.Set("username", root.Username)
	CreateAdtyEpayChannel(ctx)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"success":true`)
	var created struct {
		Success bool `json:"success"`
		Data    struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.True(t, created.Success)
	require.Positive(t, created.Data.ID)

	idleBody := map[string]any{
		"name": "ftr0807-idle", "gateway_url": "https://idle.example.test", "merchant_id": "ftr0807-idle",
		"merchant_key": "idle-secret", "priority": 9, "weight": 1, "order_mode": "api",
		"methods": []map[string]any{{
			"method": "alipay", "type_code": "alipay", "enabled": true, "return_type": "qrcode",
		}},
	}
	ctx, rec = newAuthenticatedContext(t, http.MethodPost, "/api/adty/admin/epay/channels", idleBody, root.Id)
	ctx.Set("role", root.Role)
	CreateAdtyEpayChannel(ctx)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	ctx, rec = newAuthenticatedContext(t, http.MethodPost, "/api/adty/admin/epay/channels/"+strconv.Itoa(created.Data.ID)+"/status", map[string]string{"status": "enabled"}, root.Id)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(created.Data.ID)}}
	ctx.Set("role", root.Role)
	SetAdtyEpayChannelStatus(ctx)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"success":true`)

	picked, method, err := service.PickAdtyEpayChannel("alipay", decimal.RequireFromString("730.00"), "pc", nil, 1_700_000_000)
	require.NoError(t, err)
	require.NotNil(t, picked)
	require.NotNil(t, method)
	assert.Equal(t, created.Data.ID, picked.Id)
	assert.Equal(t, "alipay", method.Method)
	assert.Equal(t, "qrcode", method.ReturnType)
	assert.True(t, method.MinCNY.Equal(decimal.RequireFromString("1.00")))
	assert.True(t, method.MaxCNY.Equal(decimal.RequireFromString("1000.00")))

	var stored model.AdtyEpayChannel
	require.NoError(t, db.Preload("Methods").First(&stored, created.Data.ID).Error)
	assert.Equal(t, model.AdtyEpayStatusEnabled, stored.Status)
	assert.Equal(t, "ftr0807-secret", stored.MerchantKey)
	require.Len(t, stored.Methods, 1)
	ctx, rec = newAuthenticatedContext(t, http.MethodGet, "/api/adty/admin/epay/channels", nil, root.Id)
	ctx.Set("role", root.Role)
	GetAdtyEpayChannels(ctx)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "ftr0807-secret")
	assert.Contains(t, rec.Body.String(), `"name":"ftr0807"`)

	var topups int64
	require.NoError(t, db.Model(&model.TopUp{}).Count(&topups).Error)
	assert.Zero(t, topups)
	ftrAssertQuotaAndNoLedger(t, db, root.Id, ftrAccountQuota)
}
