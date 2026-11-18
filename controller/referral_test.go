package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func prepareReferralControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	driver := sqlite.Open(filepath.Join(t.TempDir(), "referral_controller.db") + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	db, err := gorm.Open(driver, &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)

	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldSecret := common.RedisEnabled, common.SessionSecret
	oldMap := common.OptionMap
	savedCfg := *adty_setting.GetReferralSetting()

	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseType("sqlite"), common.DatabaseType("sqlite"))
	common.RedisEnabled = false
	common.SessionSecret = "referral-controller-test-secret"
	common.OptionMap = map[string]string{}
	model.InitColumnNamesForTests()

	t.Cleanup(func() {
		assert.NoError(t, sqlDB.Close())
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.SessionSecret = oldRedis, oldSecret
		common.OptionMap = oldMap
		*adty_setting.GetReferralSetting() = savedCfg
		model.InitColumnNamesForTests()
	})

	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Option{},
		&model.ReferralAffiliate{},
		&model.ReferralCommissionAccount{},
		&model.ReferralClick{},
		&model.ReferralBinding{},
		&model.ReferralCommission{},
		&model.ReferralWithdrawal{},
		&model.ReferralAsset{},
	))
	cfg := adty_setting.GetReferralSetting()
	rate := 10.0
	cfg.Enabled = true
	cfg.DefaultRate = &rate
	return db
}

func callReferralControllerHandler(t *testing.T, handler gin.HandlerFunc, userID int, target string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	c.Set("id", userID)
	handler(c)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return rec.Code, body
}

func TestGetReferralProfileQueryErrorPropagation(t *testing.T) {
	db := prepareReferralControllerTestDB(t)
	user := &model.User{Username: "ref-profile-user", Password: "hashed-password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
	require.NoError(t, db.Create(user).Error)
	affiliate, err := service.Referral.EnsureAffiliate(user.Id)
	require.NoError(t, err)

	// 1. Unset CNY rate (ErrReferralRateUnset) succeeds with empty cny_rate.
	_, body := callReferralControllerHandler(t, GetReferralProfile, user.Id, "/api/user/referral/profile")
	require.Equal(t, true, body["success"])
	data, ok := body["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "", data["cny_rate"])

	// 2. Database error on options (CNY rate lookup) must fail instead of masquerading as unset.
	for _, failTable := range []string{"options", "referral_clicks", "referral_bindings", "referral_commissions"} {
		t.Run("fail_"+failTable, func(t *testing.T) {
			cbName := "fail_profile_" + failTable
			injected := errors.New("synthetic query failure on " + failTable)
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register(cbName, func(tx *gorm.DB) {
				if tx.Statement != nil && tx.Statement.Table == failTable {
					tx.AddError(injected)
				}
			}))
			defer func() { _ = db.Callback().Query().Remove(cbName) }()

			_, errBody := callReferralControllerHandler(t, GetReferralProfile, user.Id, "/api/user/referral/profile")
			assert.Equal(t, false, errBody["success"], "table %s failure must not return success", failTable)
		})
	}
	_ = affiliate
}

func TestGetReferralCommissionsAndWithdrawalsCountErrorPropagation(t *testing.T) {
	db := prepareReferralControllerTestDB(t)
	user := &model.User{Username: "ref-list-user", Password: "hashed-password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
	require.NoError(t, db.Create(user).Error)
	_, err := service.Referral.EnsureAffiliate(user.Id)
	require.NoError(t, err)

	t.Run("commissions count failure", func(t *testing.T) {
		const cbName = "fail_commissions_count"
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register(cbName, func(tx *gorm.DB) {
			if tx.Statement != nil && tx.Statement.Table == "referral_commissions" {
				tx.AddError(errors.New("synthetic commissions count error"))
			}
		}))
		defer func() { _ = db.Callback().Query().Remove(cbName) }()

		_, body := callReferralControllerHandler(t, GetReferralCommissions, user.Id, "/api/user/referral/commissions?p=1&page_size=10")
		assert.Equal(t, false, body["success"])
	})

	t.Run("withdrawals count failure", func(t *testing.T) {
		const cbName = "fail_withdrawals_count"
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register(cbName, func(tx *gorm.DB) {
			if tx.Statement != nil && tx.Statement.Table == "referral_withdrawals" {
				tx.AddError(errors.New("synthetic withdrawals count error"))
			}
		}))
		defer func() { _ = db.Callback().Query().Remove(cbName) }()

		_, body := callReferralControllerHandler(t, GetReferralWithdrawals, user.Id, "/api/user/referral/withdrawals?p=1&page_size=10")
		assert.Equal(t, false, body["success"])
	})
}
