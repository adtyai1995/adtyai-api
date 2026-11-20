package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordManageAuditUsesDurableAuditIntentAndProvesPersistence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.AuditLog{}, &model.AuditIntent{}, &model.Log{}))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/adty/admin/telegram/bots/user", nil)
	c.Set("id", 1)
	c.Set("username", "root")
	c.Set("role", common.RoleRootUser)

	recordManageAudit(c, "adty.telegram.save", map[string]any{
		"purpose":       "user",
		"enabled":       true,
		"token_changed": true,
	})

	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyAuditLogged))

	var intents []model.AuditIntent
	require.NoError(t, db.Where("event_id <> ''").Find(&intents).Error)
	require.Len(t, intents, 1)
	assert.NotZero(t, intents[0].DeliveredAt)

	var logs []model.AuditLog
	require.NoError(t, db.Where("action = ?", "adty.telegram.save").Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, intents[0].EventID, logs[0].EventId)

	// Simulate temporary log store outage: durable AuditIntent must still persist in primary DB
	// and recover into AuditLog when DeliverPendingAuditIntents runs.
	oldLogDB := model.LOG_DB
	model.LOG_DB = nil
	defer func() {
		model.LOG_DB = oldLogDB
	}()

	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = httptest.NewRequest(http.MethodPut, "/api/adty/admin/translation", nil)
	c2.Set("id", 1)
	c2.Set("username", "root")
	c2.Set("role", common.RoleRootUser)

	recordManageAudit(c2, "adty.translation.save", map[string]any{
		"mode":  "auto",
		"model": "gpt-4o-mini",
	})
	assert.True(t, common.GetContextKeyBool(c2, constant.ContextKeyAuditLogged))

	var pendingIntent model.AuditIntent
	require.NoError(t, db.Where("delivered_at = 0").First(&pendingIntent).Error)
	assert.NotEmpty(t, pendingIntent.EventID)

	model.LOG_DB = oldLogDB
	require.NoError(t, model.DeliverAuditIntent(context.Background(), pendingIntent.EventID))

	var recoveredLog model.AuditLog
	require.NoError(t, db.Where("event_id = ?", pendingIntent.EventID).First(&recoveredLog).Error)
	assert.Equal(t, "adty.translation.save", recoveredLog.Action)
}

func TestMarkAuditLoggedDoesNotSuppressFallbackWithoutDurablePersistence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = adtyTestDB(t)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/option/", nil)

	markAuditLogged(c)
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyAuditLogged), "markAuditLogged must not suppress fallback when no durable AuditIntent was persisted")

	deliverCommittedAudits(c, []model.AuditLog{{}})
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyAuditLogged), "deliverCommittedAudits must not suppress fallback for unpersisted empty event")
}

func TestRecordUserSecurityAuditAndPasskeyDomainAuditPersistDurableIntent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.AuditLog{}, &model.AuditIntent{}, &model.Log{}))

	user := model.User{
		Username: "sec-audit-user",
		Password: "hashed-password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&user).Error)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/user/self", nil)
	c.Set("id", user.Id)
	c.Set("username", user.Username)
	c.Set("role", user.Role)

	recordUserSecurityAudit(c, user.Id, "user.password_change", map[string]any{
		"success":             true,
		"notification_failed": false,
	})

	recDomain := httptest.NewRecorder()
	cDomain, _ := gin.CreateTestContext(recDomain)
	cDomain.Request = httptest.NewRequest(http.MethodPut, "/api/option/passkey/domains", nil)
	cDomain.Set("id", 1)
	cDomain.Set("username", "root")
	cDomain.Set("role", common.RoleRootUser)

	recordPasskeyDomainAudit(cDomain, &model.PasskeyDomainChange{
		PreviousRPID:        "old.example.com",
		EffectiveRPID:       "new.example.com",
		RemovedRPIDs:        []string{"old.example.com"},
		AffectedCredentials: 2,
	}, true, nil)

	var intents []model.AuditIntent
	require.NoError(t, db.Find(&intents).Error)
	require.Len(t, intents, 2)

	var secLog model.AuditLog
	require.NoError(t, db.Where("action = ?", "user.password_change").First(&secLog).Error)
	assert.Equal(t, model.AuditCategorySecurity, secLog.Category)
	assert.True(t, secLog.Success)

	var domainLog model.AuditLog
	require.NoError(t, db.Where("action = ?", "option.passkey_domains_confirmed").First(&domainLog).Error)
	assert.Equal(t, "old.example.com", domainLog.BeforeValue)
	assert.Equal(t, "new.example.com", domainLog.AfterValue)
}
