package controller

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const ftrAccountQuota = 500000

func ftrAccountDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, i18n.Init())
	gin.SetMode(gin.TestMode)
	previousRedis, previousSecret := common.RedisEnabled, common.SessionSecret
	previousEncryption := common.PasswordLoginEncryptionEnabled
	previousDomain := common.EmailDomainRestrictionEnabled
	previousAlias := common.EmailAliasRestrictionEnabled
	common.RedisEnabled = false
	common.SessionSecret = "ftr-account-pg-secret"
	common.PasswordLoginEncryptionEnabled = false
	common.EmailDomainRestrictionEnabled = false
	common.EmailAliasRestrictionEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled, common.SessionSecret = previousRedis, previousSecret
		common.PasswordLoginEncryptionEnabled = previousEncryption
		common.EmailDomainRestrictionEnabled = previousDomain
		common.EmailAliasRestrictionEnabled = previousAlias
	})
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.UserSession{}, &model.AuthFlow{}, &model.AuditIntent{}, &model.AuditLog{},
		&model.AdtyBalanceLedger{}, &model.Option{}, &model.TwoFA{}, &model.TwoFABackupCode{},
		&model.PasskeyCredential{}, &model.UserAccessToken{},
		&model.Token{}, &model.TopUp{}, &model.Model{}, &model.AdtyGroup{},
	))
	require.NoError(t, model.MigrateAdtyTables())
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	return db
}

func ftrAssertQuotaAndNoLedger(t *testing.T, db *gorm.DB, userID, quota int) {
	t.Helper()
	var stored model.User
	require.NoError(t, db.Unscoped().First(&stored, userID).Error)
	assert.Equal(t, quota, stored.Quota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Where("user_id = ?", userID).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

func TestFTRADTY0208EmailBindPG(t *testing.T) {
	db := ftrAccountDB(t)
	password, err := common.Password2Hash("enrollment-password")
	require.NoError(t, err)
	user := &model.User{
		Username: "ftr0208", Password: password, Email: "old-0208@example.com",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default",
		AuthVersion: 1, Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(user).Error)
	require.NoError(t, model.PublishUserAuthCache(user.Id))
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "ftr-0208")
	require.NoError(t, err)
	identity, err := service.ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)

	mailbox := newSecurityMailbox(t)
	const nextEmail = "new-0208@example.com"
	flow := startSecurityEmailBinding(t, identity, nextEmail, service.VerificationMethodPassword)
	require.True(t, flow.OldEmailRequired, "a password change of an existing email must confirm the old mailbox")
	request, err := common.Marshal(map[string]string{
		"flow_token": flow.FlowToken,
		"new_code":   mailbox.code(t, nextEmail),
		"old_code":   mailbox.code(t, "old-0208@example.com"),
	})
	require.NoError(t, err)
	response := securityEnrollmentRequest(http.MethodPost, "/api/oauth/email/bind", string(request), "", identity, EmailBind)
	require.Contains(t, response.Body.String(), `"success":true`, response.Body.String())

	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, nextEmail, stored.Email)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	assert.Equal(t, user.Id, stored.Id)
	ftrAssertQuotaAndNoLedger(t, db, user.Id, ftrAccountQuota)
}

func TestFTRADTY0209DeleteSelfPG(t *testing.T) {
	user, identity := setupAccountDeletionPrecheck(t)
	pgtest.RecordServerVersion(t, model.DB)
	require.NoError(t, model.DB.Model(user).Update("quota", ftrAccountQuota).Error)
	require.NoError(t, model.DB.Create(&model.TopUp{
		UserId: user.Id, TradeNo: "ftr0209-historical", Status: common.TopUpStatusSuccess, Amount: 3,
	}).Error)
	require.NoError(t, model.DB.Create(&model.AdtyBalanceLedger{
		UserId: user.Id, DeltaQuota: 1, BalanceBefore: 0, BalanceAfter: 1,
		Type: model.AdtyLedgerTypeTopUp, RefId: "ftr0209-historical",
	}).Error)

	operation := service.VerificationOperation{Scope: service.VerificationScopeAccountDelete}
	body := accountDeletionBody(t, user)
	proof := issueSecurityEnrollmentProof(t, identity, operation, service.VerificationMethodPassword)
	response := securityEnrollmentRequest(http.MethodDelete, "/api/user/self", body, proof, identity, DeleteSelf)
	require.Contains(t, response.Body.String(), `"success":true`, response.Body.String())

	_, err := model.GetUserById(user.Id, false)
	require.Error(t, err)
	var stored model.User
	require.NoError(t, model.DB.Unscoped().First(&stored, user.Id).Error)
	assert.True(t, stored.DeletedAt.Valid)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	assert.Equal(t, ftrAccountQuota, stored.Quota)
	var historical model.TopUp
	require.NoError(t, model.DB.Where("trade_no = ?", "ftr0209-historical").First(&historical).Error)
	assert.Equal(t, common.TopUpStatusSuccess, historical.Status)
	var ledger model.AdtyBalanceLedger
	require.NoError(t, model.DB.Where("ref_id = ?", "ftr0209-historical").First(&ledger).Error)
	assert.EqualValues(t, 1, ledger.DeltaQuota)
	var extra int64
	require.NoError(t, model.DB.Model(&model.AdtyBalanceLedger{}).Where("user_id = ? AND ref_id <> ?", user.Id, "ftr0209-historical").Count(&extra).Error)
	assert.Zero(t, extra)
}

func TestFTRADTY0210DisableAccountPG(t *testing.T) {
	db := ftrAccountDB(t)
	user := model.User{
		Username: "ftr0210", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&user).Error)
	identity, proof := manageUserProof(t, db, service.VerificationOperation{
		Scope:   service.VerificationScopeAdminUserManage,
		Context: []byte(fmt.Sprintf(`{"user_id":%d,"action":"disable"}`, user.Id)),
	})
	response := performVerifiedManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"disable"}`, user.Id), identity, proof)
	require.Contains(t, response.Body.String(), `"success":true`, response.Body.String())

	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, common.UserStatusDisabled, stored.Status)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	var operator model.User
	require.NoError(t, db.First(&operator, identity.UserID).Error)
	assert.Equal(t, common.RoleRootUser, operator.Role)
	ftrAssertQuotaAndNoLedger(t, db, user.Id, ftrAccountQuota)
}

func TestFTRADTY0501CreateModelPG(t *testing.T) {
	db := ftrAccountDB(t)
	admin := model.User{
		Username: "ftr0501", Password: "hashed-password", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&admin).Error)
	ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/models/", map[string]any{
		"model_name": "ftr-0501-model", "status": 1, "name_rule": 0, "context_length": 4096,
	}, admin.Id)
	ctx.Set("role", admin.Role)
	ctx.Set("username", admin.Username)
	CreateModelMeta(ctx)
	require.Contains(t, rec.Body.String(), `"success":true`, rec.Body.String())

	var stored model.Model
	require.NoError(t, db.Where("model_name = ?", "ftr-0501-model").First(&stored).Error)
	assert.Equal(t, 4096, stored.ContextLength)
	assert.Equal(t, 1, stored.Status)
	assert.Equal(t, common.RoleAdminUser, admin.Role)
	ftrAssertQuotaAndNoLedger(t, db, admin.Id, ftrAccountQuota)
}

func TestFTRADTY0506CreateGroupPG(t *testing.T) {
	db := ftrAccountDB(t)
	admin := model.User{
		Username: "ftr0506", Password: "hashed-password", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&admin).Error)
	ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/adty/groups", map[string]any{
		"name": "FTR Group", "desc": "isolated group",
	}, admin.Id)
	ctx.Set("role", admin.Role)
	ctx.Set("username", admin.Username)
	PostAdtyGroup(ctx)
	require.Contains(t, rec.Body.String(), `"success":true`, rec.Body.String())

	var groups []model.AdtyGroup
	require.NoError(t, db.Find(&groups).Error)
	var stored *model.AdtyGroup
	for i := range groups {
		if groups[i].Names["en"] == "FTR Group" {
			stored = &groups[i]
			break
		}
	}
	require.NotNil(t, stored)
	assert.Equal(t, "FTR Group", stored.Names["zh-CN"])
	assert.NotEmpty(t, stored.ID)
	assert.Equal(t, common.RoleAdminUser, admin.Role)
	ftrAssertQuotaAndNoLedger(t, db, admin.Id, ftrAccountQuota)
}
