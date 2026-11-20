package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY0301SelfQuotaPayloadPG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.AdtySite{}, &model.AdtySiteCustomer{}))
	user := model.User{
		Username: "ftr0301", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: 500000,
	}
	require.NoError(t, db.Create(&user).Error)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/api/user/self", func(c *gin.Context) {
		c.Set("id", user.Id)
		c.Set("role", user.Role)
		GetSelf(c)
	})
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/user/self", nil))
	require.Equal(t, http.StatusOK, recorder.Code)

	var envelope struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)
	require.EqualValues(t, 500000, envelope.Data["quota"])
	require.NotContains(t, envelope.Data, "password")

	var stored model.User
	require.NoError(t, db.Select("quota").First(&stored, user.Id).Error)
	require.Equal(t, 500000, stored.Quota)
}
