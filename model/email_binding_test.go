package model

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupEmailBindingTestDB(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	var db *gorm.DB
	if dialect == "postgres" {
		db = adtyTestDB(t)
	} else {
		path := filepath.Join(t.TempDir(), "email-binding.db")
		opened, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := opened.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		prevDB, prevLog := DB, LOG_DB
		prevMain, prevLogType := common.MainDatabaseType(), common.LogDatabaseType()
		DB, LOG_DB = opened, opened
		common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
		InitColumnNamesForTests()
		t.Cleanup(func() {
			DB, LOG_DB = prevDB, prevLog
			common.SetDatabaseTypes(prevMain, prevLogType)
			InitColumnNamesForTests()
			_ = sqlDB.Close()
		})
		db = opened
	}
	require.NoError(t, db.AutoMigrate(&User{}, &UserSession{}, &AuthFlow{}, &AdtySite{}, &AdtySiteDomain{}, &Option{}))
	return db
}

func newEmailBindingIdentity(t *testing.T, db *gorm.DB, username, email string, siteID int) (*User, AuthSessionIdentity) {
	t.Helper()
	user := &User{
		Username: username, Password: "hashed-password", Email: email,
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		AuthVersion: 1, SiteId: siteID,
	}
	require.NoError(t, db.Create(user).Error)
	now := time.Now().Unix()
	sid := fmt.Sprintf("sid-bind-%d-%s", user.Id, username)
	session := UserSession{
		SID: sid, UserID: user.Id, Version: 1, UserAuthVersion: user.AuthVersion,
		Status:      UserSessionStatusActive,
		RefreshHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		LoginMethod: "password", CreatedAt: now, LastActiveAt: now, ExpiresAt: now + 3600,
	}
	session.BindIssuedSite(siteID)
	require.NoError(t, db.Create(&session).Error)
	identity := AuthSessionIdentity{
		UserID: user.Id, SessionID: sid, UserAuthVersion: user.AuthVersion, SessionVersion: 1,
	}
	identity.BindIssuedSite(siteID)
	return user, identity
}

func newEmailBindingState(identity AuthSessionIdentity, currentEmail, targetEmail, newCodeHash, oldCodeHash string) EmailBindingState {
	return EmailBindingState{
		Authorization: &AuthFlowAuthorization{
			AuthSessionIdentity: identity,
			ProofID:             101,
			Scope:               "account.binding.bind",
			ContextHash:         "ctx-hash-bind",
			Method:              "password",
		},
		CurrentEmail: currentEmail,
		Email:        targetEmail,
		NewCodeHash:  newCodeHash,
		OldCodeHash:  oldCodeHash,
		ResendAt:     time.Now().Add(-time.Second).Unix(),
	}
}

// ISS-R1-0128: A15:1760 要求邮箱绑定回调绑定 site_id、精确发起回调域名和一次性 state。
// 覆盖：同站别名 A 发起/A 完成、同站别名 A 发起/B 拒绝、跨站拒绝、一次性 state、过期、撤权、域名解绑与重绑、无主域或宽 Cookie 扩权。
func TestEmailBindingEnforcesExactOriginHostSiteIsolationAndDomainLifecycle(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := setupEmailBindingTestDB(t, dialect)
			common.OptionMapRWMutex.Lock()
			prevOptions := common.OptionMap
			common.OptionMap = map[string]string{"ServerAddress": "https://main.example.com"}
			common.OptionMapRWMutex.Unlock()
			t.Cleanup(func() {
				common.OptionMapRWMutex.Lock()
				common.OptionMap = prevOptions
				common.OptionMapRWMutex.Unlock()
			})

			siteA := AdtySite{OwnerUserId: 9001, Subdomain: "shop-a", RootDomain: "sites.example.com", Status: AdtySiteStatusActive}
			siteB := AdtySite{OwnerUserId: 9002, Subdomain: "shop-b", RootDomain: "sites.example.com", Status: AdtySiteStatusActive}
			require.NoError(t, db.Create(&siteA).Error)
			require.NoError(t, db.Create(&siteB).Error)

			aliasA := AdtySiteDomain{SiteId: siteA.Id, Hostname: "alias-a.brand.example", Role: AdtySiteDomainRoleSite, Status: AdtySiteDomainActive, SSLStatus: "active"}
			aliasB := AdtySiteDomain{SiteId: siteA.Id, Hostname: "alias-b.brand.example", Role: AdtySiteDomainRoleSite, Status: AdtySiteDomainActive, SSLStatus: "active"}
			apiOnlyAlias := AdtySiteDomain{SiteId: siteA.Id, Hostname: "api.brand.example", Role: AdtySiteDomainRoleBaseURL, Status: AdtySiteDomainActive, SSLStatus: "active"}
			siteBDomain := AdtySiteDomain{SiteId: siteB.Id, Hostname: "portal.other-brand.example", Role: AdtySiteDomainRoleSite, Status: AdtySiteDomainActive, SSLStatus: "active"}
			for _, d := range []*AdtySiteDomain{&aliasA, &aliasB, &apiOnlyAlias, &siteBDomain} {
				require.NoError(t, db.Create(d).Error)
			}

			userA, identityA := newEmailBindingIdentity(t, db, "site-a-user", "old@example.com", siteA.Id)
			newHash, err := common.Password2Hash("123456")
			require.NoError(t, err)
			resendHash, err := common.Password2Hash("654321")
			require.NoError(t, err)

			// 1. 拒绝主域名、裸分站根域、宽 Cookie / 通配域名、API-only 域名与跨站域名发起。
			for _, badHost := range []string{
				"",
				"main.example.com",
				"sites.example.com",
				".brand.example",
				"*.brand.example",
				"api.brand.example",
				"shop-b.sites.example.com",
				"portal.other-brand.example",
			} {
				_, _, err := CreateEmailBinding(identityA, newEmailBindingState(identityA, "old@example.com", "target@example.com", newHash, ""), badHost)
				assert.ErrorIs(t, err, ErrAuthFlowInvalid, "host %q must be rejected at start", badHost)
			}

			// 2. 同站别名 A 发起 -> 别名 B 完成/重发/读取均被拒绝，且不消耗别名 A 的一次性 flow 或错误次数；随后在别名 A 完成成功且仅能消费一次。
			token, flow, err := CreateEmailBinding(identityA, newEmailBindingState(identityA, "old@example.com", "bound-a@example.com", newHash, ""), "ALIAS-A.Brand.Example:443")
			require.NoError(t, err)
			require.NotEmpty(t, token)
			assert.Equal(t, "alias-a.brand.example", flow.Provider)

			// 同站另一个已验证别名 B 拒绝读取、重发和完成
			_, _, err = GetEmailBinding(identityA, token, "alias-b.brand.example")
			assert.ErrorIs(t, err, ErrAuthFlowInvalid)
			_, _, err = GetEmailBinding(identityA, token, "shop-a.sites.example.com")
			assert.ErrorIs(t, err, ErrAuthFlowInvalid)
			_, _, err = GetEmailBinding(identityA, token)
			assert.ErrorIs(t, err, ErrAuthFlowInvalid)
			_, _, err = ResendEmailBinding(identityA, token, resendHash, "", "alias-b.brand.example")
			assert.ErrorIs(t, err, ErrAuthFlowInvalid)
			_, err = CompleteEmailBinding(identityA, token, "bound-a@example.com", "123456", "", "alias-b.brand.example")
			assert.ErrorIs(t, err, ErrAuthFlowInvalid)
			_, err = CompleteEmailBinding(identityA, token, "bound-a@example.com", "123456", "", "shop-a.sites.example.com")
			assert.ErrorIs(t, err, ErrAuthFlowInvalid)
			_, err = CompleteEmailBinding(identityA, token, "bound-a@example.com", "123456", "")
			assert.ErrorIs(t, err, ErrAuthFlowInvalid)

			// 确认别名 B 的拒绝未污染别名 A 的尝试计数，也未提前修改邮箱
			_, stateOnA, err := GetEmailBinding(identityA, token, "alias-a.brand.example")
			require.NoError(t, err)
			assert.Equal(t, siteA.Id, stateOnA.SiteID)
			assert.Equal(t, "alias-a.brand.example", stateOnA.OriginHost)
			assert.Zero(t, stateOnA.FailedAttempts)
			var unchanged User
			require.NoError(t, db.First(&unchanged, userA.Id).Error)
			assert.Equal(t, "old@example.com", unchanged.Email)

			// 在发起域名 alias-a 上重发并完成成功
			_, resentState, err := ResendEmailBinding(identityA, token, resendHash, "", "alias-a.brand.example")
			require.NoError(t, err)
			assert.Equal(t, "alias-a.brand.example", resentState.OriginHost)
			completed, err := CompleteEmailBinding(identityA, token, "bound-a@example.com", "654321", "", "alias-a.brand.example")
			require.NoError(t, err)
			assert.Equal(t, "bound-a@example.com", completed.Email)
			var updated User
			require.NoError(t, db.First(&updated, userA.Id).Error)
			assert.Equal(t, "bound-a@example.com", updated.Email)

			// 一次性 state：再次在 alias-a 上完成返回已消费
			_, err = CompleteEmailBinding(identityA, token, "bound-a@example.com", "654321", "", "alias-a.brand.example")
			assert.ErrorIs(t, err, ErrAuthFlowConsumed)

			// 3. 域名解绑（removed）或重绑到其他站点（siteB）后，已发起未完成的绑定挑战立即失效
			tokenUnbind, _, err := CreateEmailBinding(identityA, newEmailBindingState(identityA, "bound-a@example.com", "after-unbind@example.com", newHash, ""), "alias-a.brand.example")
			require.NoError(t, err)
			require.NoError(t, db.Model(&AdtySiteDomain{}).Where("id = ?", aliasA.Id).Update("status", AdtySiteDomainRemoved).Error)
			_, err = CompleteEmailBinding(identityA, tokenUnbind, "after-unbind@example.com", "123456", "", "alias-a.brand.example")
			assert.ErrorIs(t, err, ErrAuthFlowInvalid, "unbound domain must not complete pending email binding")

			// 域名重绑到 siteB，即使 status 重新变为 active，原 siteA 的未完成挑战仍须拒绝，且 siteB 会话也不能复用该 state
			require.NoError(t, db.Model(&AdtySiteDomain{}).Where("id = ?", aliasA.Id).Updates(map[string]any{
				"site_id": siteB.Id,
				"status":  AdtySiteDomainActive,
			}).Error)
			_, err = CompleteEmailBinding(identityA, tokenUnbind, "after-unbind@example.com", "123456", "", "alias-a.brand.example")
			assert.ErrorIs(t, err, ErrAuthFlowInvalid, "rebound domain must not inherit old site authorization")
			_, identityB := newEmailBindingIdentity(t, db, "site-b-user", "b-old@example.com", siteB.Id)
			_, err = CompleteEmailBinding(identityB, tokenUnbind, "after-unbind@example.com", "123456", "", "alias-a.brand.example")
			assert.ErrorIs(t, err, ErrAuthFlowInvalid, "new site owner of rebound domain cannot consume old flow")

			// 4. 过期与会话撤权
			tokenExp, flowExp, err := CreateEmailBinding(identityA, newEmailBindingState(identityA, "bound-a@example.com", "exp@example.com", newHash, ""), "alias-b.brand.example")
			require.NoError(t, err)
			require.NoError(t, db.Model(flowExp).Update("expires_at", time.Now().Add(-time.Minute)).Error)
			_, err = CompleteEmailBinding(identityA, tokenExp, "exp@example.com", "123456", "", "alias-b.brand.example")
			assert.ErrorIs(t, err, ErrAuthFlowExpired)

			tokenRevoke, _, err := CreateEmailBinding(identityA, newEmailBindingState(identityA, "bound-a@example.com", "revoked@example.com", newHash, ""), "alias-b.brand.example")
			require.NoError(t, err)
			require.NoError(t, db.Model(&UserSession{}).Where("sid = ?", identityA.SessionID).Update("status", "revoked").Error)
			_, err = CompleteEmailBinding(identityA, tokenRevoke, "revoked@example.com", "123456", "", "alias-b.brand.example")
			assert.ErrorIs(t, err, ErrUserSessionInactive)
		})
	}
}
