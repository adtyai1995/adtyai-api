package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUpdateSelfPasswordCommittedPostCommitCacheOrSessionFailureOutcome(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := adtyTestDB(t)
	oldRedis, oldSecret := common.RedisEnabled, common.SessionSecret
	common.RedisEnabled = false
	common.SessionSecret = "adty-user-cred-postcommit-secret"
	t.Cleanup(func() {
		common.RedisEnabled, common.SessionSecret = oldRedis, oldSecret
	})

	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserSession{},
		&model.AuthFlow{},
		&model.AuditLog{},
		&model.AuditIntent{},
		&model.Log{},
		&model.UserAccessToken{},
		&model.TwoFA{},
		&model.PasskeyCredential{},
	))

	hashedOld, err := common.Password2Hash("OldPass#12345")
	require.NoError(t, err)

	user := model.User{
		Username:    "cred-commit-user",
		Password:    hashedOld,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
	}
	require.NoError(t, db.Create(&user).Error)

	bundle, err := service.CreateLoginSessionAtAuthVersion(user.Id, user.AuthVersion, "password", "127.0.0.1", "test-agent")
	require.NoError(t, err)

	identity, err := service.ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)
	binding, err := service.BindVerificationOperation(service.VerificationOperation{
		Scope: service.VerificationScopePasswordChange,
	})
	require.NoError(t, err)
	proof, _, err := service.IssueSecurityProof(identity, service.VerificationMethodPassword, binding)
	require.NoError(t, err)

	// Force post-commit PublishUserAuthCache failure by enabling Redis without an active Redis client
	// only after ChangeUserPassword commits, or by revoking the session row inside an after-update hook on users
	// when auth_version advances so AdvanceCurrentSessionToUserVersion fails after the password transaction commits.
	const hookName = "adty:post_commit_session_advance_fail"
	require.NoError(t, db.Callback().Update().After("gorm:update").Register(hookName, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "users" {
			// Enabling Redis with nil RDB causes PublishUserAuthCache (which runs right after ChangeUserPassword commits)
			// to return an error after the password transaction has already committed.
			common.RedisEnabled = true
		}
	}))
	t.Cleanup(func() {
		_ = db.Callback().Update().Remove(hookName)
		common.RedisEnabled = false
	})

	body := fmt.Sprintf(`{"original_password":%q,"password":%q}`, "OldPass#12345", "NewPass#67890")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/user/self", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Security-Proof", proof)
	c.Set("id", user.Id)
	c.Set("username", user.Username)
	c.Set("role", user.Role)
	c.Set("session_id", identity.SessionID)
	c.Set("session_version", identity.SessionVersion)
	c.Set("auth_version", identity.UserAuthVersion)
	c.Set("login_method", bundle.Session.LoginMethod)

	UpdateSelf(c)
	common.RedisEnabled = false

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"success":true`)
	assert.Contains(t, rec.Body.String(), `"committed":true`)
	assert.Contains(t, rec.Body.String(), `"relogin_required":true`)
	assert.Contains(t, rec.Body.String(), `"CREDENTIAL_COMMITTED_RELOGIN_REQUIRED"`)

	// Verify the new password was indeed committed in the database.
	var reloaded model.User
	require.NoError(t, db.First(&reloaded, user.Id).Error)
	assert.True(t, common.ValidatePasswordAndHash("NewPass#67890", reloaded.Password))
	assert.Greater(t, reloaded.AuthVersion, user.AuthVersion)

	var audit model.AuditLog
	require.NoError(t, db.Where("action = ? AND user_id = ?", "user.password_change", user.Id).First(&audit).Error)
	assert.True(t, audit.Success)
}

func TestUnbindTelegramRevokesBindingAndLogsDurableAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.AuditLog{},
		&model.AuditIntent{},
		&model.Log{},
		&model.AdtyTelegramBot{},
		&model.AdtyTelegramChallenge{},
		&model.AdtyTelegramBinding{},
		&model.AdtyTelegramDelivery{},
	))

	user := model.User{
		Username:   "tg-unbind-user",
		Password:   "hashed-password",
		Role:       common.RoleCommonUser,
		Status:     common.UserStatusEnabled,
		Group:      "default",
		TelegramId: "99887766",
	}
	require.NoError(t, db.Create(&user).Error)

	binding := model.AdtyTelegramBinding{
		UserId:         user.Id,
		Purpose:        model.AdtyTelegramPurposeUser,
		SiteId:         0,
		TelegramUserId: 99887766,
		BotIdentity:    "bot-ident-hash",
		Status:         model.AdtyTelegramBindActive,
	}
	require.NoError(t, db.Create(&binding).Error)

	delivery := model.AdtyTelegramDelivery{
		EventKey:  "pending-tg-event-1",
		UserId:    user.Id,
		Purpose:   model.AdtyTelegramPurposeUser,
		BindingId: binding.Id,
		Status:    model.AdtyTelegramDeliveryWait,
		Body:      "测试\n\nTest",
	}
	require.NoError(t, db.Create(&delivery).Error)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/adty/telegram/bind/unbind", strings.NewReader(`{"purpose":"user"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", user.Id)
	c.Set("username", user.Username)
	c.Set("role", user.Role)

	UnbindTelegram(c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"success":true`)
	assert.Contains(t, rec.Body.String(), `"status":"unbound"`)
	assert.Contains(t, rec.Body.String(), `"bound":false`)

	view, err := service.GetAdtyTelegramBind(user.Id, model.AdtyTelegramPurposeUser)
	require.NoError(t, err)
	assert.Equal(t, "unbound", view.Status)
	assert.Zero(t, view.TelegramUserId)

	var updatedUser model.User
	require.NoError(t, db.First(&updatedUser, user.Id).Error)
	assert.Empty(t, updatedUser.TelegramId)

	var updatedDelivery model.AdtyTelegramDelivery
	require.NoError(t, db.First(&updatedDelivery, delivery.Id).Error)
	assert.Equal(t, model.AdtyTelegramDeliverySkip, updatedDelivery.Status)

	var audit model.AuditLog
	require.NoError(t, db.Where("action = ? AND user_id = ?", "user.binding_unbind", user.Id).First(&audit).Error)
	assert.Contains(t, audit.BeforeValue, "99887766")
	assert.Contains(t, audit.AfterValue, `"unbound"`)
}
