package controller

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This black-box fixture describes the durable storage contract independently
// of the producer: successful mutations must leave their frozen event here.
type durableAuditIntentRow struct {
	ID                int64  `gorm:"primaryKey"`
	EventID           string `gorm:"type:varchar(64);uniqueIndex"`
	Version           int
	Payload           string `gorm:"type:text"`
	CreatedAt         int64
	Attempts          int
	NextAttemptAt     int64
	DeliveredAt       int64
	LastErrorCategory string `gorm:"type:varchar(64)"`
}

func (durableAuditIntentRow) TableName() string { return "audit_intents" }

func TestDurablePermissionAuditSurvivesLogOutage(t *testing.T) {
	db, target := setupAdtyPermissionAudit(t)
	require.NoError(t, db.AutoMigrate(&durableAuditIntentRow{}))
	previous := model.LOG_DB
	model.LOG_DB = nil
	t.Cleanup(func() { model.LOG_DB = previous })

	response := updateUserPermissionsAsRoot(t, db, target, `{"adty_user":{"read":true}}`)
	require.Contains(t, response.Body.String(), `"success":true`)
	assert.True(t, authz.Can(target.Id, common.RoleAdminUser, authz.AdtyUserRead))
	var intents []durableAuditIntentRow
	require.NoError(t, db.Find(&intents).Error)
	require.Len(t, intents, 2, "both the committed user mutation and its permission diff must survive the independent log outage")
	actions := make([]string, 0, len(intents))
	for _, intent := range intents {
		var event model.AuditLog
		require.NoError(t, common.UnmarshalJsonStr(intent.Payload, &event))
		assert.NotEmpty(t, event.EventId)
		assert.Equal(t, event.EventId, intent.EventID)
		assert.True(t, event.Success)
		assert.Equal(t, common.RoleRootUser, event.ActorRole)
		assert.Zero(t, intent.DeliveredAt)
		actions = append(actions, event.Action)
	}
	assert.ElementsMatch(t, []string{"user.update", "adty.admin_permissions.update"}, actions)
}

func TestDurablePermissionAuditFailureRollsBackMutation(t *testing.T) {
	db, target := setupAdtyPermissionAudit(t)
	require.NoError(t, db.AutoMigrate(&durableAuditIntentRow{}))
	const callback = "test:durable-permission-intent-failure"
	intentAttempted := false
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_intents" {
			intentAttempted = true
			tx.AddError(assert.AnError)
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove(callback)) })
	response := updateUserPermissionsAsRoot(t, db, target, `{"adty_user":{"read":true}}`)
	assert.True(t, intentAttempted)
	assert.Contains(t, response.Body.String(), `"success":false`)
	assert.False(t, authz.Can(target.Id, common.RoleAdminUser, authz.AdtyUserRead))
	var rules int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ? AND v1 = ? AND v2 = ?", authz.UserSubject(target.Id), "adty_user", "read").Count(&rules).Error)
	assert.Zero(t, rules)
	var logs int64
	require.NoError(t, db.Model(&model.AuditLog{}).Where("action IN ?", []string{"user.update", "adty.admin_permissions.update"}).Count(&logs).Error)
	assert.Zero(t, logs)
}

func TestDurablePermissionAuditUsesTransactionSnapshot(t *testing.T) {
	db, target := setupAdtyPermissionAudit(t)
	// Another committed writer has changed policy before this node reloads it.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return authz.SetUserPermissionsInTx(tx, target.Id, authz.PermissionsMap{"adty_user": {"read": true}})
	}))
	require.False(t, authz.Can(target.Id, common.RoleAdminUser, authz.AdtyUserRead))
	response := updateUserPermissionsAsRoot(t, db, target, `{"adty_user":{"read":false}}`)
	require.Contains(t, response.Body.String(), `"success":true`)
	var event model.AuditLog
	require.NoError(t, db.Where("action = ?", "adty.admin_permissions.update").Take(&event).Error)
	removed, err := common.Marshal(event.Other.Op.Params["removed"])
	require.NoError(t, err)
	assert.JSONEq(t, `["adty_user:read"]`, string(removed), "the diff must use committed DB policy, not this node's stale enforcer")
}

func TestDurablePermissionAuditSurvivesPolicyReloadFailure(t *testing.T) {
	db, target := setupAdtyPermissionAudit(t)
	const createCallback, queryCallback = "test:audit-intent-committed", "test:audit-policy-reload-failure"
	intentWritten, injected := false, false
	require.NoError(t, db.Callback().Create().After("gorm:create").Register(createCallback, func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_intents" && tx.Error == nil {
			intentWritten = true
		}
	}))
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(queryCallback, func(tx *gorm.DB) {
		if intentWritten && !injected && tx.Statement.Table == "casbin_rule" {
			injected = true
			tx.AddError(assert.AnError)
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Create().Remove(createCallback))
		require.NoError(t, db.Callback().Query().Remove(queryCallback))
	})
	response := updateUserPermissionsAsRoot(t, db, target, `{"adty_user":{"read":true}}`)
	require.True(t, injected)
	assert.Contains(t, response.Body.String(), `"success":false`, "the HTTP attempt can fail after the business commit")
	var rules int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?", authz.UserSubject(target.Id), "adty_user", "read", authz.EffectAllow).Count(&rules).Error)
	assert.EqualValues(t, 1, rules)
	var intents []model.AuditIntent
	require.NoError(t, db.Find(&intents).Error)
	require.Len(t, intents, 2)
	for _, intent := range intents {
		var event model.AuditLog
		require.NoError(t, common.UnmarshalJsonStr(intent.Payload, &event))
		assert.True(t, event.Success, "a committed mutation is distinct from failed HTTP finalization")
		assert.Equal(t, common.RoleRootUser, event.ActorRole)
	}
	require.NoError(t, authz.ReloadPolicy())
	assert.True(t, authz.Can(target.Id, common.RoleAdminUser, authz.AdtyUserRead))
}

func TestDurablePermissionAuditRechecksTransactionRole(t *testing.T) {
	for _, test := range []struct {
		name  string
		role  int
		rules int64
	}{
		{"promoted target audit uses root capabilities", common.RoleRootUser, 1},
		{"demoted target cannot retain admin overrides", common.RoleCommonUser, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, target := setupAdtyPermissionAudit(t)
			const callback = "test:role-changed-after-origin-read"
			changed := false
			require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
				user, ok := tx.Statement.Dest.(*model.User)
				if !changed && ok && user.Id == target.Id && tx.Error == nil {
					changed = true
					require.NoError(t, db.Model(&model.User{}).Where("id = ?", target.Id).Update("role", test.role).Error)
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(callback)) })
			response := updateUserPermissionsAsRoot(t, db, target, `{"adty_user":{"read":true}}`)
			require.True(t, changed)
			assert.Contains(t, response.Body.String(), `"success":true`)
			var rules int64
			require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ?", authz.UserSubject(target.Id)).Count(&rules).Error)
			assert.Equal(t, test.rules, rules, "the stale initial role cannot authorize persisted admin grants")
			var event model.AuditLog
			require.NoError(t, db.Where("action = ?", "user.update").Take(&event).Error)
			role, err := common.Marshal(event.Other.Op.Params["target_role"])
			require.NoError(t, err)
			assert.Equal(t, fmt.Sprint(test.role), string(role))
			if test.role == common.RoleRootUser {
				event = model.AuditLog{}
				require.NoError(t, db.Where("action = ?", "adty.admin_permissions.update").Take(&event).Error)
				added, err := common.Marshal(event.Other.Op.Params["added"])
				require.NoError(t, err)
				var permissions []string
				require.NoError(t, common.Unmarshal(added, &permissions))
				assert.Empty(t, permissions, "a root target already has the permission independently of admin overrides")
			}
		})
	}
}

func TestDurableUserUpdateRechecksTargetAuthorizationInTransaction(t *testing.T) {
	db, target := setupAdtyPermissionAudit(t)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", target.Id).Update("role", common.RoleCommonUser).Error)
	const callback = "test:target-promoted-before-admin-update"
	changed := false
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		user, ok := tx.Statement.Dest.(*model.User)
		if !changed && ok && user.Id == target.Id && tx.Error == nil {
			changed = true
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", target.Id).Update("role", common.RoleAdminUser).Error)
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(callback)) })
	body := fmt.Sprintf(`{"id":%d,"username":%q,"group":"default","display_name":"must-not-commit"}`, target.Id, target.Username)
	response := adminUserRequest(http.MethodPut, "/api/user/", body, "", service.AuthIdentity{UserID: 9999}, common.RoleAdminUser, nil, UpdateUser)
	require.True(t, changed)
	assert.Contains(t, response.Body.String(), `"success":false`)
	var stored model.User
	require.NoError(t, db.First(&stored, target.Id).Error)
	assert.Empty(t, stored.DisplayName)
	var count int64
	require.NoError(t, db.Model(&model.AuditIntent{}).Count(&count).Error)
	assert.Zero(t, count)
}

func durableOptionRequest() *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(`{"key":"SystemName","value":"durable-brand"}`))
	c.Set("id", 9999)
	c.Set("role", common.RoleRootUser)
	c.Set("username", "root-operator")
	UpdateOption(c)
	return response
}

func prepareDurableOption(t *testing.T) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	previous, previousName := common.OptionMap, common.SystemName
	common.OptionMap = maps.Clone(previous)
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap, common.SystemName = previous, previousName
		common.OptionMapRWMutex.Unlock()
	})
}

func TestDurableOptionAuditSurvivesLogOutage(t *testing.T) {
	db := setupManageUserTestDB(t)
	prepareDurableOption(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &durableAuditIntentRow{}))
	previous := model.LOG_DB
	model.LOG_DB = nil
	t.Cleanup(func() { model.LOG_DB = previous })
	response := durableOptionRequest()
	require.Contains(t, response.Body.String(), `"success":true`)
	var intent durableAuditIntentRow
	require.NoError(t, db.Take(&intent).Error)
	var event model.AuditLog
	require.NoError(t, common.UnmarshalJsonStr(intent.Payload, &event))
	assert.Equal(t, "option.update", event.Action)
	assert.Contains(t, intent.Payload, "SystemName")
	assert.NotContains(t, intent.Payload, "durable-brand", "configuration values must not enter audit storage")
}

func TestDurableOptionAuditFailureRollsBackMutation(t *testing.T) {
	db := setupManageUserTestDB(t)
	prepareDurableOption(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &durableAuditIntentRow{}))
	require.NoError(t, db.Create(&model.Option{Key: "SystemName", Value: "old-brand"}).Error)
	const callback = "test:durable-option-intent-failure"
	intentAttempted := false
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_intents" {
			intentAttempted = true
			tx.AddError(assert.AnError)
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove(callback)) })
	response := durableOptionRequest()
	assert.True(t, intentAttempted)
	assert.Contains(t, response.Body.String(), `"success":false`)
	var option model.Option
	require.NoError(t, db.Where("key = ?", "SystemName").Take(&option).Error)
	assert.Equal(t, "old-brand", option.Value)
}

func TestWorkspaceConfigurationAuditIsAtomic(t *testing.T) {
	for _, kind := range []string{"telegram", "translation", "health"} {
		for _, failure := range []string{"intent", "log_store"} {
			t.Run(kind+"/"+failure, func(t *testing.T) {
				t.Setenv("TEST_SECURITY_DIALECT", "postgres")
				user, identity := setupSecurityEnrollmentTest(t)
				db := model.DB
				require.NoError(t, db.Model(user).Update("role", common.RoleRootUser).Error)
				require.NoError(t, db.AutoMigrate(&model.AdtyTelegramBot{}, &model.AdtyTelegramBinding{}, &model.AdtyTelegramDelivery{}, &model.AdtyTranslationConfig{}, &model.AdtyGroupHealthState{}))
				require.NoError(t, db.Create(&model.AdtyTelegramBot{Purpose: model.AdtyTelegramPurposeUser, Enabled: false}).Error)
				require.NoError(t, db.Create(&model.AdtyTranslationConfig{Id: 1, Mode: model.AdtyTranslationModeSet, ModelName: "old-model"}).Error)
				require.NoError(t, db.Create(&model.AdtyGroupHealthState{Id: 1, SuccessMin: "0.90", StaleSeconds: 120}).Error)
				logs := model.LOG_DB
				if failure == "intent" {
					const callback = "test:workspace-audit-failure"
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
						if tx.Statement.Table == "audit_intents" {
							tx.AddError(assert.AnError)
						}
					}))
					t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove(callback)) })
				} else {
					model.LOG_DB = nil
				}
				handler, path, body, proof := gin.HandlerFunc(PutAdtyGroupHealth), "/api/adty/admin/group-health", `{"success_min":"0.96","stale_seconds":180}`, ""
				if kind == "telegram" {
					handler, path, body = PutAdtyAdminTelegram, "/api/adty/admin/telegram/bots/user", `{"enabled":true}`
					proof = issueSecurityEnrollmentProof(t, identity, service.AdtyNoticeOperation(service.AdtyNoticeActionTelegramSave, model.AdtyTelegramPurposeUser), service.VerificationMethodRootExempt)
				} else if kind == "translation" {
					handler, path, body = PutAdtyTranslation, "/api/adty/admin/translation", `{"mode":"platform","model_name":"new-model"}`
					proof = issueSecurityEnrollmentProof(t, identity, service.AdtyNoticeOperation(service.AdtyNoticeActionTranslation, model.AdtyTranslationModeSet), service.VerificationMethodRootExempt)
				}
				response := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(response)
				c.Request = httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Request.Header.Set("X-Security-Proof", proof)
				c.Set("id", user.Id)
				c.Set("username", user.Username)
				c.Set("role", common.RoleRootUser)
				c.Set("session_id", identity.SessionID)
				c.Set("auth_version", identity.UserAuthVersion)
				c.Set("session_version", identity.SessionVersion)
				c.Params = gin.Params{{Key: "purpose", Value: model.AdtyTelegramPurposeUser}}
				if kind == "telegram" {
					PutAdtyAdminTelegram(c)
				} else {
					handler(c)
				}
				var result struct {
					Success bool `json:"success"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				assert.Equal(t, failure == "log_store", result.Success, response.Body.String())
				if failure == "intent" {
					var bot model.AdtyTelegramBot
					require.NoError(t, db.First(&bot).Error)
					assert.False(t, bot.Enabled)
					var cfg model.AdtyTranslationConfig
					require.NoError(t, db.First(&cfg).Error)
					assert.Equal(t, "old-model", cfg.ModelName)
					var health model.AdtyGroupHealthState
					require.NoError(t, db.First(&health).Error)
					assert.Equal(t, "0.90", health.SuccessMin)
				} else {
					var intents []model.AuditIntent
					require.NoError(t, db.Find(&intents).Error)
					require.Len(t, intents, 1)
					model.LOG_DB = logs
					require.NoError(t, model.DeliverAuditIntent(t.Context(), intents[0].EventID))
					require.NoError(t, model.DeliverAuditIntent(t.Context(), intents[0].EventID))
					var count int64
					require.NoError(t, logs.Model(&model.AuditLog{}).Where("event_id = ?", intents[0].EventID).Count(&count).Error)
					assert.EqualValues(t, 1, count)
				}
			})
		}
	}
}

func TestAPIKeyReadRequiresDurableAuditBeforeResponse(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, failure := range []string{"intent", "log_store"} {
			t.Run(fmt.Sprintf("batch_%t/%s", batch, failure), func(t *testing.T) {
				t.Setenv("TEST_SECURITY_DIALECT", "postgres")
				user, identity := setupSecurityEnrollmentTest(t)
				db, logs := model.DB, model.LOG_DB
				model.InitColumnNamesForTests()
				t.Cleanup(model.InitColumnNamesForTests)
				require.NoError(t, db.AutoMigrate(&model.Token{}))
				token := model.Token{UserId: user.Id, Name: "audit-key", Key: "audit-secret-never-record", Status: common.TokenStatusEnabled, ExpiredTime: -1}
				require.NoError(t, db.Create(&token).Error)
				if failure == "intent" {
					const callback = "test:key-audit-failure"
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
						if tx.Statement.Table == "audit_intents" {
							tx.AddError(assert.AnError)
						}
					}))
					t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove(callback)) })
				} else {
					model.LOG_DB = nil
				}
				proof := issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopeAPIKeyRead}, service.VerificationMethodPassword)
				path, body, handler := fmt.Sprintf("/api/token/%d/key", token.Id), "", gin.HandlerFunc(GetTokenKey)
				if batch {
					path, body, handler = "/api/token/batch/keys", fmt.Sprintf(`{"ids":[%d]}`, token.Id), GetTokenKeysBatch
				}
				response := securityEnrollmentRequest(http.MethodPost, path, body, proof, identity, func(c *gin.Context) { c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(token.Id)}}; handler(c) })
				if failure == "intent" {
					assert.NotContains(t, response.Body.String(), token.Key)
					assert.Contains(t, response.Body.String(), `"success":false`)
				} else {
					require.Contains(t, response.Body.String(), token.Key)
					var intents []model.AuditIntent
					require.NoError(t, db.Find(&intents).Error)
					require.Len(t, intents, 1)
					assert.NotContains(t, intents[0].Payload, token.Key)
					model.LOG_DB = logs
					require.NoError(t, model.DeliverAuditIntent(t.Context(), intents[0].EventID))
				}
			})
		}
	}
}

func TestSensitiveMutationsRequireTransactionAudit(t *testing.T) {
	for _, action := range []string{"key_create", "key_update", "key_delete", "key_batch_delete", "notification_hide", "voucher_cancel", "voucher_freeze", "snapshot_backfill"} {
		t.Run(action, func(t *testing.T) {
			previousRedis := common.RedisEnabled
			common.RedisEnabled = false
			t.Cleanup(func() { common.RedisEnabled = previousRedis })
			db := adtyTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.AuditLog{}, &model.AuditIntent{}, &model.Task{}, &model.BillingOperation{}))
			require.NoError(t, model.MigrateAdtyTables())
			user := model.User{Username: "atomic-audit-owner", Password: "unused", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default"}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Name: "before", Key: "retained-secret", Status: common.TokenStatusEnabled, ExpiredTime: -1, Group: "default", UnlimitedQuota: true}
			require.NoError(t, db.Create(&token).Error)
			voucher := model.AdtyVoucher{IssuerId: user.Id, CodeHash: "voucher-test-hash", Purpose: model.AdtyVoucherPurposeSiteOwner, TargetSiteId: 7, Status: model.AdtyVoucherStatusPending, SourceKind: model.AdtyVoucherSourcePlatform, AmountUSD: decimal.NewFromInt(1)}
			require.NoError(t, db.Create(&voucher).Error)
			require.NoError(t, db.Create(&model.AdtyVoucherLiability{Scope: "main", LimitUSD: decimal.NewFromInt(10), FrozenUSD: decimal.NewFromInt(1)}).Error)
			task := model.Task{TaskID: "audit-historical-task", UserId: user.Id, Group: "default", Quota: 2400, SubmitTime: 1700000000}
			task.PrivateData.BillingContext = &model.TaskBillingContext{OriginModelName: "audit-model", ModelRatio: 2, GroupRatio: 1.2, SiteMarkup: 1.25, SiteCostFactor: 0.8}
			require.NoError(t, db.Create(&task).Error)
			const callback = "test:all-sensitive-audit-failure"
			attempted := false
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "audit_intents" {
					attempted = true
					tx.AddError(assert.AnError)
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove(callback)) })
			var operationErr error
			switch action {
			case "notification_hide":
				operationErr = service.HideAdtyNotificationForUser(user.Id, "atomic-hide")
			case "voucher_cancel":
				operationErr = service.CancelAdtyVoucher(user.Id, voucher.Id)
			case "voucher_freeze":
				operationErr = service.FreezeAdtyVoucher(user.Id, voucher.Id)
			case "snapshot_backfill":
				_, operationErr = service.ApplyPriceSnapshotBackfill(user.Id, 10)
			default:
				method, path, body, handler := http.MethodPost, "/api/token/", `{"name":"new-key","unlimited_quota":true,"group":"default"}`, gin.HandlerFunc(AddToken)
				if action == "key_update" {
					method, body, handler = http.MethodPut, fmt.Sprintf(`{"id":%d,"name":"after","unlimited_quota":true,"expired_time":-1,"group":"default"}`, token.Id), UpdateToken
				}
				if action == "key_delete" {
					method, path, body, handler = http.MethodDelete, fmt.Sprintf("/api/token/%d", token.Id), "", DeleteToken
				}
				if action == "key_batch_delete" {
					path, body, handler = "/api/token/batch", fmt.Sprintf(`{"ids":[%d]}`, token.Id), DeleteTokenBatch
				}
				response := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(response)
				c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Set("id", user.Id)
				c.Set("role", user.Role)
				c.Set("username", user.Username)
				c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(token.Id)}}
				handler(c)
				assert.Contains(t, response.Body.String(), `"success":false`, response.Body.String())
			}
			assert.True(t, attempted, "mandatory audit must use the primary transaction")
			if action == "notification_hide" || action == "voucher_cancel" || action == "voucher_freeze" || action == "snapshot_backfill" {
				assert.Error(t, operationErr)
			}
			var saved model.Token
			require.NoError(t, db.First(&saved, token.Id).Error)
			assert.Equal(t, "before", saved.Name)
			var count int64
			require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", user.Id).Count(&count).Error)
			assert.EqualValues(t, 1, count)
			require.NoError(t, db.Model(&model.AdtyNotificationHide{}).Count(&count).Error)
			assert.Zero(t, count)
			var savedVoucher model.AdtyVoucher
			require.NoError(t, db.First(&savedVoucher, voucher.Id).Error)
			assert.Equal(t, model.AdtyVoucherStatusPending, savedVoucher.Status)
			require.NoError(t, db.First(&task, task.ID).Error)
			assert.Nil(t, task.PrivateData.BillingContext.PriceSnapshot)
		})
	}
}

func TestSensitiveAuditRecoveryAndOuterRollback(t *testing.T) {
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.AuditLog{}, &model.AuditIntent{}, &model.Task{}, &model.BillingOperation{}))
	require.NoError(t, model.MigrateAdtyTables())
	user := model.User{Username: "audit-recovery-root", Password: "unused", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "recovery-key-never-in-audit", Name: "before", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, Group: "default"}
	require.NoError(t, db.Create(&token).Error)
	logs := model.LOG_DB
	model.LOG_DB = nil
	t.Cleanup(func() { model.LOG_DB = logs })
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("id", user.Id)
		c.Set("username", user.Username)
		c.Set("role", user.Role)
		c.Next()
	}, middleware.TokenOperationAudit())
	router.POST("/api/token/", AddToken)
	router.PUT("/api/token/", UpdateToken)
	router.DELETE("/api/token/:id", DeleteToken)
	router.POST("/api/token/batch", DeleteTokenBatch)
	for _, request := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/token/", `{"name":"created","group":"default","unlimited_quota":true}`},
		{http.MethodPut, "/api/token/", fmt.Sprintf(`{"id":%d,"name":"updated","group":"default","unlimited_quota":true,"expired_time":-1}`, token.Id)},
		{http.MethodDelete, fmt.Sprintf("/api/token/%d", token.Id), ""},
		{http.MethodPost, "/api/token/batch", `{"ids":[99999]}`},
	} {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(request.method, request.path, strings.NewReader(request.body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, req)
		require.Contains(t, response.Body.String(), `"success":true`, response.Body.String())
	}
	// Rejected requests also retain a recoverable event.
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/token/", strings.NewReader("invalid-json")))
	require.Contains(t, response.Body.String(), `"success":false`)
	require.NoError(t, service.HideAdtyNotificationForUser(user.Id, "recover-hide"))
	require.NoError(t, service.HideAdtyNotificationForUser(user.Id, "recover-hide"))
	voucher := model.AdtyVoucher{IssuerId: user.Id, CodeHash: "recovery-voucher", Purpose: model.AdtyVoucherPurposeSiteOwner, SourceKind: model.AdtyVoucherSourcePlatform, TargetSiteId: 7, Status: model.AdtyVoucherStatusPending, AmountUSD: decimal.NewFromInt(1)}
	require.NoError(t, db.Create(&voucher).Error)
	require.NoError(t, db.Create(&model.AdtyVoucherLiability{Scope: "main", LimitUSD: decimal.NewFromInt(10), FrozenUSD: decimal.NewFromInt(1)}).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		if err := service.InheritAdtyVouchersOnCloseTx(tx, 7, nil); err != nil {
			return err
		}
		return assert.AnError
	}), assert.AnError)
	var phantom int64
	require.NoError(t, db.Model(&model.AuditIntent{}).Where("payload LIKE ?", "%unused owner or margin voucher revoked on site close%").Count(&phantom).Error)
	assert.Zero(t, phantom, "outer rollback must remove both status mutation and success audit")
	require.NoError(t, service.FreezeAdtyVoucher(user.Id, voucher.Id))
	require.NoError(t, service.CancelAdtyVoucher(user.Id, voucher.Id))
	_, err := service.PreviewPriceSnapshotBackfill(user.Id, 10)
	require.NoError(t, err)
	_, err = service.ApplyPriceSnapshotBackfill(user.Id, 10)
	require.NoError(t, err)
	var intents []model.AuditIntent
	require.NoError(t, db.Find(&intents).Error)
	require.Len(t, intents, 9, "four Key writes, one rejection, one idempotent hide, freeze, cancel, and one apply")
	model.LOG_DB = logs
	for _, intent := range intents {
		assert.Zero(t, intent.DeliveredAt)
		assert.NotContains(t, intent.Payload, token.Key)
		require.NoError(t, model.DeliverAuditIntent(t.Context(), intent.EventID))
		require.NoError(t, model.DeliverAuditIntent(t.Context(), intent.EventID))
	}
	var count int64
	require.NoError(t, logs.Model(&model.AuditLog{}).Count(&count).Error)
	assert.EqualValues(t, 9, count)
}

func TestAuditSuppressionRequiresPersistedIntent(t *testing.T) {
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AuditIntent{}, &model.AuditLog{}))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", nil)
	c.Set("id", 1)
	c.Set("role", common.RoleRootUser)
	markAuditLogged(c)
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyAuditLogged))
	event := prepareManageAudit(c, 0, "adty.telegram.save", map[string]any{"purpose": "user"})
	deliverCommittedAudits(c, []model.AuditLog{event})
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyAuditLogged))
	var count int64
	require.NoError(t, db.Model(&model.AuditIntent{}).Count(&count).Error)
	assert.Zero(t, count, "delivery must never manufacture evidence after a transaction")
	require.NoError(t, model.AppendAuditIntentTx(db, event))
	deliverCommittedAudits(c, []model.AuditLog{event})
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyAuditLogged))
}
