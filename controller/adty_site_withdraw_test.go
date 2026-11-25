package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupSiteWithdrawControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&model.Option{},
		&model.User{},
		&model.AdtySite{},
		&model.AdtySiteOwnership{},
	))

	oldDB := model.DB
	oldRedis := common.RedisEnabled
	oldMemCache := common.MemoryCacheEnabled
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		_ = sqlDB.Close()
	})
	return db
}

func TestPostAdtySiteWithdrawalAccepts8DecimalExpectedRate(t *testing.T) {
	db := setupSiteWithdrawControllerTestDB(t)

	owner := model.User{Id: 31, Username: "wd_owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner).Error)
	site := model.AdtySite{Id: 31, OwnerUserId: owner.Id, Status: model.AdtySiteStatusActive, Subdomain: "wd31"}
	require.NoError(t, db.Create(&site).Error)

	postWithdrawal := func(rate string) (int, map[string]any) {
		payload := `{"amount_usd":"50","method":"usdt","network":"TRC20","account_no":"TAddr123","expected_rate":"` + rate + `"}`
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/adty/site/withdrawals", bytes.NewBufferString(payload))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("Idempotency-Key", "wd-key-"+rate)
		c.Set("id", owner.Id)
		c.Set("role", owner.Role)
		PostAdtySiteWithdrawal(c)
		var resp map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return w.Code, resp
	}

	// 8-decimal expected_rate passes decimal validation and proceeds to step-up security proof check (403/428/non-Invalid-parameters).
	_, resp8 := postWithdrawal("0.99876543")
	assert.Equal(t, false, resp8["success"])
	assert.NotEqual(t, "Invalid parameters", resp8["message"], "8-decimal expected_rate must pass ParseBoundedDecimalString(12, 8)")

	// 9-decimal expected_rate is rejected immediately as Invalid parameters.
	_, resp9 := postWithdrawal("0.998765432")
	assert.Equal(t, false, resp9["success"])
	assert.Equal(t, "Invalid parameters", resp9["message"], "9-decimal expected_rate must be rejected")
}
