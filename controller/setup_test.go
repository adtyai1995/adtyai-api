package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func doPostSetupRequest(username, password, confirm string, selfUse, demoSite bool) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	body := fmt.Sprintf(
		`{"username":%q,"password":%q,"confirmPassword":%q,"SelfUseModeEnabled":%t,"DemoSiteEnabled":%t}`,
		username, password, confirm, selfUse, demoSite,
	)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	PostSetup(ctx)
	return recorder
}

func doGetSetupRequest(t *testing.T) Setup {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/setup", nil)
	GetSetup(ctx)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var payload struct {
		Success bool  `json:"success"`
		Data    Setup `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload), recorder.Body.String())
	require.True(t, payload.Success)
	return payload.Data
}

// ISS-R1-0180: Root创建后两项option分别失败、setup行写失败、进程在各边界中断时，
// 失败恢复必须允许原合法Root请求补齐后段，不得重置已成立Root密码或开放匿名接管，
// 且恢复后唯一Root、配置持久值与内存Setup一致。
func TestSetupAtomicCompletionRecoveryAcrossFailureBoundaries(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		for _, failStage := range []string{"self_use_option", "demo_site_option", "setup_row", "crash_with_checksetup"} {
			t.Run(dialect+"_"+failStage, func(t *testing.T) {
				db := setupRootSetupDB(t, dialect)
				require.NoError(t, model.EnsureOneRootUserIndex(db))
				require.NoError(t, model.EnsureUsernameUniqueIndex(db))
				withSetupDisabled(t)
				prevSelf, prevDemo := operation_setting.SelfUseModeEnabled, operation_setting.DemoSiteEnabled
				operation_setting.SelfUseModeEnabled, operation_setting.DemoSiteEnabled = false, false
				t.Cleanup(func() {
					operation_setting.SelfUseModeEnabled, operation_setting.DemoSiteEnabled = prevSelf, prevDemo
				})

				injectFailure := true
				callbackName := "test:setup_fail_" + dialect + "_" + failStage
				shouldFailOption := func(tx *gorm.DB, targetKey string) bool {
					if !injectFailure || tx.Statement.Table != "options" {
						return false
					}
					switch dest := tx.Statement.Dest.(type) {
					case *model.Option:
						return dest != nil && dest.Key == targetKey
					case model.Option:
						return dest.Key == targetKey
					}
					return false
				}
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callbackName+"_create", func(tx *gorm.DB) {
					if failStage == "self_use_option" && shouldFailOption(tx, "SelfUseModeEnabled") {
						tx.AddError(assert.AnError)
					}
					if failStage == "demo_site_option" && shouldFailOption(tx, "DemoSiteEnabled") {
						tx.AddError(assert.AnError)
					}
					if failStage == "setup_row" && injectFailure && tx.Statement.Table == "setups" {
						tx.AddError(assert.AnError)
					}
				}))
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName+"_update", func(tx *gorm.DB) {
					if failStage == "self_use_option" && shouldFailOption(tx, "SelfUseModeEnabled") {
						tx.AddError(assert.AnError)
					}
					if failStage == "demo_site_option" && shouldFailOption(tx, "DemoSiteEnabled") {
						tx.AddError(assert.AnError)
					}
				}))
				t.Cleanup(func() {
					_ = db.Callback().Create().Remove(callbackName + "_create")
					_ = db.Callback().Update().Remove(callbackName + "_update")
				})

				if failStage == "crash_with_checksetup" {
					// Simulate crash right after Root creation followed by restart CheckSetup()
					hashed, err := common.HashAccountPassword("password-123")
					require.NoError(t, err)
					require.NoError(t, model.CreateRootUserIfAbsent(&model.User{
						Username: "root-admin",
						Password: hashed,
						Role:     common.RoleRootUser,
						Status:   common.UserStatusEnabled,
					}))
					model.CheckSetup()
				} else {
					first := doPostSetupRequest("root-admin", "password-123", "password-123", true, true)
					require.Contains(t, first.Body.String(), `"success":false`, first.Body.String())
					assert.False(t, constant.Setup, "in-memory Setup must remain false when a trailing persistence step fails")
					assert.False(t, operation_setting.SelfUseModeEnabled, "failed setup must not leave uncommitted SelfUseModeEnabled in memory")
					assert.False(t, operation_setting.DemoSiteEnabled, "failed setup must not leave uncommitted DemoSiteEnabled in memory")
				}

				// Root exists in DB, and its password hash is established.
				var rootBefore model.User
				require.NoError(t, db.Where("role = ?", common.RoleRootUser).First(&rootBefore).Error)
				require.Equal(t, "root-admin", rootBefore.Username)
				require.True(t, common.ValidatePasswordAndHash("password-123", rootBefore.Password))

				// GetSetup reflects half-initialized state: Status=false, RootInit=true.
				statusMid := doGetSetupRequest(t)
				assert.False(t, statusMid.Status)
				assert.True(t, statusMid.RootInit)

				// Anonymous takeover or password-reset attempts with wrong username/password must be rejected.
				injectFailure = false
				for _, attack := range []struct {
					name, user, pass string
				}{
					{"wrong_username", "attacker-root", "password-123"},
					{"wrong_password", "root-admin", "wrong-password-999"},
					{"blank_credentials", "", ""},
				} {
					resp := doPostSetupRequest(attack.user, attack.pass, attack.pass, false, false)
					require.Contains(t, resp.Body.String(), `"success":false`, attack.name+": "+resp.Body.String())
					var currentRoot model.User
					require.NoError(t, db.First(&currentRoot, rootBefore.Id).Error)
					assert.Equal(t, rootBefore.Password, currentRoot.Password, "failed recovery attempt %s must never reset established Root password", attack.name)
				}

				// Legitimate retry with the same Root credentials completes the remaining setup steps.
				retry := doPostSetupRequest("root-admin", "password-123", "password-123", true, true)
				require.Contains(t, retry.Body.String(), `"success":true`, retry.Body.String())
				assert.True(t, constant.Setup)
				assert.True(t, operation_setting.SelfUseModeEnabled)
				assert.True(t, operation_setting.DemoSiteEnabled)

				// Only one Root user and one Setup row exist, and Root password hash was preserved unchanged.
				var rootCount, setupCount int64
				require.NoError(t, db.Model(&model.User{}).Where("role = ?", common.RoleRootUser).Count(&rootCount).Error)
				require.NoError(t, db.Model(&model.Setup{}).Count(&setupCount).Error)
				assert.EqualValues(t, 1, rootCount)
				assert.EqualValues(t, 1, setupCount)

				var rootAfter model.User
				require.NoError(t, db.First(&rootAfter, rootBefore.Id).Error)
				assert.Equal(t, rootBefore.Password, rootAfter.Password, "Root password hash must not be regenerated on recovery")

				var selfOpt, demoOpt model.Option
				require.NoError(t, db.Where("key = ?", "SelfUseModeEnabled").First(&selfOpt).Error)
				require.NoError(t, db.Where("key = ?", "DemoSiteEnabled").First(&demoOpt).Error)
				assert.Equal(t, "true", selfOpt.Value)
				assert.Equal(t, "true", demoOpt.Value)

				// Simulate another instance or process restart with cold in-memory state.
				constant.Setup = false
				operation_setting.SelfUseModeEnabled = false
				operation_setting.DemoSiteEnabled = false
				statusAfter := doGetSetupRequest(t)
				assert.True(t, statusAfter.Status)
				assert.True(t, constant.Setup)
				assert.True(t, operation_setting.SelfUseModeEnabled)
				assert.True(t, operation_setting.DemoSiteEnabled)

				// Subsequent PostSetup even with valid Root credentials is now rejected as already initialized.
				again := doPostSetupRequest("root-admin", "password-123", "password-123", false, false)
				require.Contains(t, again.Body.String(), `"success":false`, again.Body.String())
				assert.True(t, operation_setting.SelfUseModeEnabled)
				assert.True(t, operation_setting.DemoSiteEnabled)
			})
		}
	}
}

func TestSetupMultiInstanceRaceKeepsWinnerOptionsAndSingleSetupRow(t *testing.T) {
	db := setupRootSetupDB(t, "postgres")
	require.NoError(t, model.EnsureOneRootUserIndex(db))
	require.NoError(t, model.EnsureUsernameUniqueIndex(db))
	withSetupDisabled(t)

	var wg sync.WaitGroup
	results := make([]string, 2)
	configs := []struct {
		user    string
		selfUse bool
		demo    bool
	}{
		{"root-node-a", true, false},
		{"root-node-b", false, true},
	}
	for i, cfg := range configs {
		wg.Add(1)
		go func(idx int, user string, selfUse, demo bool) {
			defer wg.Done()
			results[idx] = doPostSetupRequest(user, "password-123", "password-123", selfUse, demo).Body.String()
		}(i, cfg.user, cfg.selfUse, cfg.demo)
	}
	wg.Wait()

	var roots []model.User
	require.NoError(t, db.Where("role = ?", common.RoleRootUser).Find(&roots).Error)
	require.Len(t, roots, 1)

	var setups int64
	require.NoError(t, db.Model(&model.Setup{}).Count(&setups).Error)
	assert.EqualValues(t, 1, setups)

	winner := roots[0].Username
	wantSelfUse := winner == "root-node-a"
	wantDemo := winner == "root-node-b"
	assert.Equal(t, wantSelfUse, operation_setting.SelfUseModeEnabled)
	assert.Equal(t, wantDemo, operation_setting.DemoSiteEnabled)
}
