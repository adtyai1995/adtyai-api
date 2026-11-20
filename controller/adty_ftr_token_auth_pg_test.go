package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-06-01: an enabled personal key authenticates POST /v1/chat/completions.
// TokenAuth binds the caller and token and does not move quota.
func TestFTRADTY0601TokenAuthAcceptsEnabledKeyPG(t *testing.T) {
	ftrRelayDB(t)
	buyer := model.User{
		Username: "ftr0601", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", SiteId: 0, StaffSiteId: 0, Quota: 1000000,
	}
	require.NoError(t, model.DB.Create(&buyer).Error)
	require.Equal(t, common.RoleCommonUser, buyer.Role)
	require.Zero(t, buyer.SiteId)
	require.Zero(t, buyer.StaffSiteId)
	token := model.Token{
		UserId: buyer.Id, Key: "ftr0601key", Name: "ftr0601", Status: common.TokenStatusEnabled,
		RemainQuota: 1000000, UsedQuota: 0, UnlimitedQuota: false, ExpiredTime: -1, OwnerSelfUseSiteId: 0,
	}
	require.NoError(t, model.DB.Create(&token).Error)
	require.Positive(t, token.Id)

	engine := gin.New()
	engine.POST("/v1/chat/completions", middleware.TokenAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"user_id":     c.GetInt("id"),
			"token_id":    c.GetInt("token_id"),
			"using_group": c.GetString("group"),
		})
	})
	req := httptest.NewRequest(http.MethodPost, "http://localhost:3000/v1/chat/completions", nil)
	req.Host = "localhost:3000"
	req.Header.Set("Authorization", "Bearer sk-ftr0601key")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"user_id":`)
	require.Contains(t, rec.Body.String(), `"using_group":"default"`)
	assert.Contains(t, rec.Body.String(), `"token_id":`)

	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, buyer.Id).Error)
	assert.Equal(t, common.RoleCommonUser, storedUser.Role)
	assert.Equal(t, 1000000, storedUser.Quota)
	var storedToken model.Token
	require.NoError(t, model.DB.First(&storedToken, token.Id).Error)
	assert.Equal(t, common.TokenStatusEnabled, storedToken.Status)
	assert.Equal(t, 1000000, storedToken.RemainQuota)
	assert.Zero(t, storedToken.UsedQuota)
	var ops int64
	require.NoError(t, model.DB.Model(&model.BillingOperation{}).Count(&ops).Error)
	assert.Zero(t, ops)
	var ledgers int64
	require.NoError(t, model.DB.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
