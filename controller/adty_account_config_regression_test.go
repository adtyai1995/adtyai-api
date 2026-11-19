package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCredentialCommitReportsRecoveryPG(t *testing.T) {
	for _, failure := range []string{"cache", "session"} {
		t.Run(failure, func(t *testing.T) {
			db := adtyTestDB(t)
			previousRedis, previousClient, previousSecret := common.RedisEnabled, common.RDB, common.SessionSecret
			common.RedisEnabled, common.SessionSecret = false, "credential-outcome-regression"
			t.Cleanup(func() {
				common.RedisEnabled, common.RDB, common.SessionSecret = previousRedis, previousClient, previousSecret
			})
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.AuthFlow{}, &model.UserAccessToken{}, &model.TwoFA{}, &model.PasskeyCredential{}, &model.AuditIntent{}, &model.AuditLog{}, &model.Log{}))
			hash, err := common.HashAccountPassword("Before-change-413")
			require.NoError(t, err)
			user := model.User{Username: "credential-" + failure, Password: hash, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
			require.NoError(t, db.Create(&user).Error)
			bundle, err := service.CreateLoginSessionAtAuthVersion(user.Id, 1, "password", "127.0.0.1", "regression")
			require.NoError(t, err)
			identity, err := service.ParseAccessToken(bundle.AccessToken)
			require.NoError(t, err)
			binding, err := service.BindVerificationOperation(service.VerificationOperation{Scope: service.VerificationScopePasswordChange})
			require.NoError(t, err)
			proof, _, err := service.IssueSecurityProof(identity, service.VerificationMethodPassword, binding)
			require.NoError(t, err)
			client := redis.NewClient(&redis.Options{Network: "unix", Addr: t.TempDir() + "/unavailable.sock", MaxRetries: -1, DialTimeout: 10 * time.Millisecond})
			t.Cleanup(func() { _ = client.Close() })
			const hook = "adty:credential-outcome-fault"
			require.NoError(t, db.Callback().Update().After("gorm:update").Register(hook, func(tx *gorm.DB) {
				if tx.Statement.Table != "users" {
					return
				}
				changes, ok := tx.Statement.Dest.(map[string]any)
				if !ok || changes["password"] == nil {
					return
				}
				if failure == "cache" {
					common.RDB, common.RedisEnabled = client, true
					return
				}
				// A session revoked concurrently must not make a committed password look rolled back.
				if err := tx.Session(&gorm.Session{NewDB: true}).Exec("DELETE FROM user_sessions WHERE sid = ?", identity.SessionID).Error; err != nil {
					tx.AddError(err)
				}
			}))
			t.Cleanup(func() { _ = db.Callback().Update().Remove(hook) })
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPut, "/api/user/self", strings.NewReader(`{"original_password":"Before-change-413","password":"After-change-419"}`))
			c.Request.Header.Set("X-Security-Proof", proof)
			c.Set("id", user.Id)
			c.Set("role", user.Role)
			c.Set("username", user.Username)
			c.Set("session_id", identity.SessionID)
			c.Set("session_version", identity.SessionVersion)
			c.Set("auth_version", identity.UserAuthVersion)
			UpdateSelf(c)
			common.RedisEnabled = false
			require.NoError(t, db.Callback().Update().Remove(hook))
			var current model.User
			require.NoError(t, db.First(&current, user.Id).Error)
			require.True(t, common.ValidatePasswordAndHash("After-change-419", current.Password), rec.Body.String())
			require.False(t, common.ValidatePasswordAndHash("Before-change-413", current.Password))
			require.Greater(t, current.AuthVersion, user.AuthVersion)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var response struct {
				Success bool `json:"success"`
				Data    struct {
					Committed bool `json:"committed"`
					Relogin   bool `json:"relogin_required"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &response))
			require.True(t, response.Success, rec.Body.String())
			require.True(t, response.Data.Committed)
			require.True(t, response.Data.Relogin)
			require.Contains(t, rec.Header().Get("Cache-Control"), "no-store")
			require.NotContains(t, rec.Body.String(), "After-change-419")
			require.Error(t, model.ValidateAuthSessionWithTx(db, identity), "old authorization must be unusable even if Redis prevents a deny-fence write")
			var audit model.AuditLog
			require.NoError(t, db.Where("user_id = ? AND action = ?", user.Id, "user.password_change").First(&audit).Error)
			require.True(t, audit.Success)
		})
	}
}

func TestOptionAuditFreezesSQLAndRedactsSecretsPG(t *testing.T) {
	db := adtyTestDB(t)
	previousName, previousSMTP := common.SystemName, common.SMTPToken
	t.Cleanup(func() { common.SystemName, common.SMTPToken = previousName, previousSMTP })
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.AuditIntent{}, &model.AuditLog{}, &model.Log{}))
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = map[string]string{"SystemName": "stale-memory"}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() { common.OptionMapRWMutex.Lock(); common.OptionMap = previous; common.OptionMapRWMutex.Unlock() })
	require.NoError(t, db.Create(&model.Option{Key: "SystemName", Value: "before-sql"}).Error)
	call := func(key, value string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(fmt.Sprintf(`{"key":%q,"value":%q}`, key, value)))
		c.Set("id", 1)
		c.Set("role", common.RoleRootUser)
		c.Set("username", "root")
		UpdateOption(c)
		return rec
	}
	require.Contains(t, call("SystemName", "after-sql").Body.String(), `"success":true`)
	var audit model.AuditLog
	require.NoError(t, db.Where("action = ? AND object_id = ?", "option.update", "SystemName").First(&audit).Error)
	require.Equal(t, "before-sql", audit.BeforeValue)
	require.Equal(t, "after-sql", audit.AfterValue)
	require.Contains(t, call("SMTPToken", "private-token-951").Body.String(), `"success":true`)
	audit = model.AuditLog{}
	require.NoError(t, db.Where("action = ? AND object_id = ?", "option.update", "SMTPToken").First(&audit).Error)
	require.Contains(t, audit.AfterValue, `"configured":true`)
	encoded, err := common.Marshal(audit)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-token-951")
	require.Contains(t, call("AuditJSONFixture", `{"label":"visible","nested":{"client_secret":"nested-secret-967"},"tokens":[{"api_key":"nested-key-971","key":"nested-key-977"}]}`).Body.String(), `"success":true`)
	audit = model.AuditLog{}
	require.NoError(t, db.Where("action = ? AND object_id = ?", "option.update", "AuditJSONFixture").First(&audit).Error)
	encoded, err = common.Marshal(audit)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "nested-secret-967")
	require.NotContains(t, string(encoded), "nested-key-971")
	require.NotContains(t, string(encoded), "nested-key-977")
	require.Contains(t, audit.AfterValue, "visible")
	// Audit persistence failure must leave both SQL and runtime business values unchanged.
	require.NoError(t, db.Migrator().DropTable(&model.AuditIntent{}))
	require.Contains(t, call("SystemName", "must-rollback").Body.String(), `"success":false`)
	var stored model.Option
	require.NoError(t, db.Where("key = ?", "SystemName").First(&stored).Error)
	require.Equal(t, "after-sql", stored.Value)
}

func TestEpayTimeoutUsesAuthoritativePairPG(t *testing.T) {
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.AuditIntent{}, &model.AuditLog{}, &model.Log{}))
	common.OptionMapRWMutex.Lock()
	previousMap := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousMap
		common.OptionMapRWMutex.Unlock()
	})
	cfg := adty_setting.GetEpaySetting()
	previous := *cfg
	t.Cleanup(func() { *cfg = previous })
	cfg.ChannelTimeoutSeconds, cfg.TotalTimeoutSeconds = 5, 20
	require.NoError(t, db.Create(&[]model.Option{{Key: adty_setting.EpaySettingKeyChannelTimeout, Value: "5"}, {Key: adty_setting.EpaySettingKeyTotalTimeout, Value: "8"}}).Error)
	call := func(key, value string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(fmt.Sprintf(`{"key":%q,"value":%q}`, key, value)))
		c.Set("id", 1)
		c.Set("role", common.RoleRootUser)
		UpdateOption(c)
		return rec
	}
	require.Contains(t, call(adty_setting.EpaySettingKeyChannelTimeout, "10").Body.String(), `"success":false`)
	require.Contains(t, call(adty_setting.EpaySettingKeyChannelTimeout, "7").Body.String(), `"success":true`)
	var options []model.Option
	require.NoError(t, db.Order("key").Find(&options).Error)
	require.Len(t, options, 2)
	require.Equal(t, "7", options[0].Value)
	require.Equal(t, "8", options[1].Value)
	perChannel, total, err := adtyEpayTimeouts()
	require.NoError(t, err)
	require.Equal(t, 7*time.Second, perChannel)
	require.Equal(t, 8*time.Second, total)
	// Both requests would pass against the initial pair; only one can commit.
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", adty_setting.EpaySettingKeyChannelTimeout).Update("value", "5").Error)
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", adty_setting.EpaySettingKeyTotalTimeout).Update("value", "12").Error)
	var wg sync.WaitGroup
	results := make(chan string, 2)
	start := make(chan struct{})
	for _, update := range [][2]string{{adty_setting.EpaySettingKeyChannelTimeout, "10"}, {adty_setting.EpaySettingKeyTotalTimeout, "8"}} {
		wg.Add(1)
		go func(key, value string) { defer wg.Done(); <-start; results <- call(key, value).Body.String() }(update[0], update[1])
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for result := range results {
		if strings.Contains(result, `"success":true`) {
			successes++
		}
	}
	require.Equal(t, 1, successes)
	require.NoError(t, db.Order("key").Find(&options).Error)
	require.True(t, (options[0].Value == "10" && options[1].Value == "12") || (options[0].Value == "5" && options[1].Value == "8"), options)
	// Invalid persistent pairs and query failure cannot start a payment deadline.
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", adty_setting.EpaySettingKeyTotalTimeout).Update("value", "31").Error)
	_, _, err = adtyEpayTimeouts()
	require.Error(t, err)
	require.NoError(t, db.Migrator().DropTable(&model.Option{}))
	_, _, err = adtyEpayTimeouts()
	require.Error(t, err)
}
