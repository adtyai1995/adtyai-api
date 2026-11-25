package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTopUpVerificationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.TwoFA{},
		&model.PasskeyCredential{},
		&model.UserSession{},
		&model.AuthFlow{},
	))

	oldDB := model.DB
	oldRedis := common.RedisEnabled
	oldMemCache := common.MemoryCacheEnabled
	oldSecret := common.SessionSecret

	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	if common.SessionSecret == "" {
		common.SessionSecret = "test-session-secret-for-verification"
	}
	model.DB = db

	t.Cleanup(func() {
		model.DB = oldDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		common.SessionSecret = oldSecret
		_ = sqlDB.Close()
	})
	return db
}

func TestAdtyGiftFulfillmentReviewScopeBinding(t *testing.T) {
	oldSecret := common.SessionSecret
	if common.SessionSecret == "" {
		common.SessionSecret = "test-session-secret-for-binding"
	}
	t.Cleanup(func() { common.SessionSecret = oldSecret })

	opApprove := AdtyGiftFulfillmentReviewOperation(42, "approve", "")
	bindingFromBuilder, err := BindVerificationOperation(opApprove)
	require.NoError(t, err)
	assert.Equal(t, VerificationScopeAdtyGiftFulfillmentReview, bindingFromBuilder.Scope)
	assert.NotEmpty(t, bindingFromBuilder.ContextHash)

	bindingOmittedNote, err := BindVerificationOperation(VerificationOperation{
		Scope:   VerificationScopeAdtyGiftFulfillmentReview,
		Context: json.RawMessage(`{"id":42,"action":"approve"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, bindingFromBuilder.ContextHash, bindingOmittedNote.ContextHash)

	bindingNormalizedApprove, err := BindVerificationOperation(VerificationOperation{
		Scope:   VerificationScopeAdtyGiftFulfillmentReview,
		Context: json.RawMessage(`{"id":42,"action":" APPROVE ","note":"   "}`),
	})
	require.NoError(t, err)
	assert.Equal(t, bindingFromBuilder.ContextHash, bindingNormalizedApprove.ContextHash)

	opReject := AdtyGiftFulfillmentReviewOperation(42, "reject", "manual review failed")
	bindingRejectBuilder, err := BindVerificationOperation(opReject)
	require.NoError(t, err)

	bindingRejectRaw, err := BindVerificationOperation(VerificationOperation{
		Scope:   VerificationScopeAdtyGiftFulfillmentReview,
		Context: json.RawMessage(`{"id":42,"action":" ReJeCt ","note":"  manual review failed  "}`),
	})
	require.NoError(t, err)
	assert.Equal(t, bindingRejectBuilder.ContextHash, bindingRejectRaw.ContextHash)
	assert.NotEqual(t, bindingFromBuilder.ContextHash, bindingRejectBuilder.ContextHash)

	bindingDifferentID, err := BindVerificationOperation(AdtyGiftFulfillmentReviewOperation(43, "approve", ""))
	require.NoError(t, err)
	assert.NotEqual(t, bindingFromBuilder.ContextHash, bindingDifferentID.ContextHash)

	bindingDifferentNote, err := BindVerificationOperation(AdtyGiftFulfillmentReviewOperation(42, "reject", "other reason"))
	require.NoError(t, err)
	assert.NotEqual(t, bindingRejectBuilder.ContextHash, bindingDifferentNote.ContextHash)

	invalidContexts := []struct {
		name string
		raw  string
	}{
		{name: "empty_object", raw: `{}`},
		{name: "zero_id", raw: `{"id":0,"action":"approve"}`},
		{name: "negative_id", raw: `{"id":-1,"action":"approve"}`},
		{name: "missing_action", raw: `{"id":42}`},
		{name: "invalid_action", raw: `{"id":42,"action":"cancel"}`},
		{name: "extra_field", raw: `{"id":42,"action":"approve","extra":"nope"}`},
		{name: "note_too_long", raw: `{"id":42,"action":"reject","note":"` + strings.Repeat("a", adtyTopUpTextMaxLen+1) + `"}`},
	}
	for _, tc := range invalidContexts {
		t.Run(tc.name, func(t *testing.T) {
			_, bindErr := BindVerificationOperation(VerificationOperation{
				Scope:   VerificationScopeAdtyGiftFulfillmentReview,
				Context: json.RawMessage(tc.raw),
			})
			assert.ErrorIs(t, bindErr, ErrVerificationContextInvalid)
		})
	}
}

func TestAdtyGiftFulfillmentReviewRootOnlyAndAccessTokenScope(t *testing.T) {
	db := setupTopUpVerificationTestDB(t)

	scope, listed := AccessTokenVerificationScope(VerificationScopeAdtyGiftFulfillmentReview)
	assert.True(t, listed)
	assert.Equal(t, AccessTokenScopeOf(authz.AdtyFinanceManualCredit), scope)

	rootUser := model.User{Username: "gf_root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	adminUser := model.User{Username: "gf_admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	commonUser := model.User{Username: "gf_user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(&rootUser).Error)
	require.NoError(t, db.Create(&adminUser).Error)
	require.NoError(t, db.Create(&commonUser).Error)

	rootID := AuthIdentity{UserID: rootUser.Id, SessionID: "sess-root", UserAuthVersion: 1, SessionVersion: 1}
	adminID := AuthIdentity{UserID: adminUser.Id, SessionID: "sess-admin", UserAuthVersion: 1, SessionVersion: 1}
	userID := AuthIdentity{UserID: commonUser.Id, SessionID: "sess-user", UserAuthVersion: 1, SessionVersion: 1}

	assert.False(t, adtyTopUpVerificationForbidden(rootID, common.RoleRootUser, VerificationScopeAdtyGiftFulfillmentReview))
	assert.True(t, adtyTopUpVerificationForbidden(adminID, common.RoleAdminUser, VerificationScopeAdtyGiftFulfillmentReview))
	assert.True(t, adtyTopUpVerificationForbidden(userID, common.RoleCommonUser, VerificationScopeAdtyGiftFulfillmentReview))

	reqs, err := GetVerificationRequirements(rootID, VerificationScopeAdtyGiftFulfillmentReview)
	require.NoError(t, err)
	require.Len(t, reqs.Methods, 1)
	assert.Equal(t, VerificationMethodRootExempt, reqs.Methods[0].Method)
	assert.True(t, reqs.Methods[0].Available)

	_, err = GetVerificationRequirements(adminID, VerificationScopeAdtyGiftFulfillmentReview)
	assert.ErrorIs(t, err, ErrVerificationForbidden)

	_, err = GetVerificationRequirements(userID, VerificationScopeAdtyGiftFulfillmentReview)
	assert.ErrorIs(t, err, ErrVerificationForbidden)
}
