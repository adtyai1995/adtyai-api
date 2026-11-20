package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ftrProfileQuota = 500000

func TestFTRADTY0206ProfileViewAndEditPG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	gin.SetMode(gin.TestMode)
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}))
	require.NoError(t, model.MigrateAdtyTables())

	user := model.User{
		Username: "ftr0206user", Password: "hashed-password", DisplayName: "Old Name",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default",
		Quota: ftrProfileQuota,
	}
	require.NoError(t, db.Create(&user).Error)

	view := httptest.NewRecorder()
	viewCtx, _ := gin.CreateTestContext(view)
	viewCtx.Request = httptest.NewRequest(http.MethodGet, "/api/user/self", nil)
	viewCtx.Set("id", user.Id)
	viewCtx.Set("role", user.Role)
	GetSelf(viewCtx)
	require.Contains(t, view.Body.String(), `"success":true`, view.Body.String())
	require.Contains(t, view.Body.String(), `"display_name":"Old Name"`)
	require.Contains(t, view.Body.String(), `"role":1`)

	edit := httptest.NewRecorder()
	editCtx, _ := gin.CreateTestContext(edit)
	editCtx.Request = httptest.NewRequest(http.MethodPut, "/api/user/self", strings.NewReader(`{"display_name":"FTR Name"}`))
	editCtx.Request.Header.Set("Content-Type", "application/json")
	editCtx.Set("id", user.Id)
	editCtx.Set("role", user.Role)
	UpdateSelf(editCtx)
	require.Contains(t, edit.Body.String(), `"success":true`, edit.Body.String())

	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, "FTR Name", stored.DisplayName)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	assert.Equal(t, ftrProfileQuota, stored.Quota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Where("user_id = ?", user.Id).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

func TestFTRADTY0207PasswordChangePG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	gin.SetMode(gin.TestMode)
	oldRedis, oldSecret := common.RedisEnabled, common.SessionSecret
	common.RedisEnabled = false
	common.SessionSecret = "adty-ftr-0207-session-secret"
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
	require.NoError(t, model.MigrateAdtyTables())

	hashed, err := common.Password2Hash("OldPass#12345")
	require.NoError(t, err)
	user := model.User{
		Username: "ftr0207user", Password: hashed, Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, Quota: ftrProfileQuota,
	}
	require.NoError(t, db.Create(&user).Error)
	bundle, err := service.CreateLoginSessionAtAuthVersion(user.Id, user.AuthVersion, "password", "127.0.0.1", "ftr-0207")
	require.NoError(t, err)
	identity, err := service.ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)
	binding, err := service.BindVerificationOperation(service.VerificationOperation{Scope: service.VerificationScopePasswordChange})
	require.NoError(t, err)
	proof, _, err := service.IssueSecurityProof(identity, service.VerificationMethodPassword, binding)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/user/self", strings.NewReader(`{"original_password":"OldPass#12345","password":"NewPass#67890"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("X-Security-Proof", proof)
	ctx.Set("id", user.Id)
	ctx.Set("username", user.Username)
	ctx.Set("role", user.Role)
	ctx.Set("session_id", identity.SessionID)
	ctx.Set("session_version", identity.SessionVersion)
	ctx.Set("auth_version", identity.UserAuthVersion)
	ctx.Set("login_method", bundle.Session.LoginMethod)
	UpdateSelf(ctx)
	require.Contains(t, rec.Body.String(), `"success":true`, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"committed":true`)

	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.True(t, common.ValidatePasswordAndHash("NewPass#67890", stored.Password))
	assert.False(t, common.ValidatePasswordAndHash("OldPass#12345", stored.Password))
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	assert.Equal(t, ftrProfileQuota, stored.Quota)
	assert.Greater(t, stored.AuthVersion, user.AuthVersion)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Where("user_id = ?", user.Id).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
