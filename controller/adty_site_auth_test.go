package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupSubsiteBasicAuthTestDB(t *testing.T) (*gorm.DB, *model.AdtySite) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("CRYPTO_SECRET", "0123456789abcdef0123456789abcdef")

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&model.Option{},
		&model.User{},
		&model.AdtySite{},
		&model.AdtySiteDomain{},
		&model.AdtySiteMailConfig{},
		&model.AdtySiteAudit{},
		&model.AuditLog{},
		&model.AuditIntent{},
		&model.AdtySiteStaff{},
		&model.AdtyBalanceLedger{},
		&model.Log{},
	))

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedis := common.RedisEnabled
	oldMem := common.MemoryCacheEnabled
	oldEmailVerif := common.EmailVerificationEnabled
	oldReg := common.RegisterEnabled
	oldPwdLogin := common.PasswordLoginEnabled
	oldPwdReg := common.PasswordRegisterEnabled
	oldDomainRestrict := common.EmailDomainRestrictionEnabled
	oldAliasRestrict := common.EmailAliasRestrictionEnabled
	oldWhitelist := append([]string(nil), common.EmailDomainWhitelist...)
	oldSMTPServer := common.SMTPServer
	oldSMTPFrom := common.SMTPFrom
	oldSMTPAccount := common.SMTPAccount
	oldOptionMap := map[string]string{}
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	for k, v := range common.OptionMap {
		oldOptionMap[k] = v
	}
	common.OptionMapRWMutex.Unlock()
	oldAsyncSend := service.AdtySendEmailAsync
	oldSiteAsyncSend := service.AdtySendSiteEmailAsync

	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	model.DB = db
	model.LOG_DB = db
	service.InvalidateAdtySiteCache()

	owner := &model.User{
		Id:          100,
		Username:    "siteowner1",
		DisplayName: "Site Owner",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(owner).Error)

	site := &model.AdtySite{
		Id:          7,
		OwnerUserId: owner.Id,
		Subdomain:   "alpha",
		RootDomain:  "sub.test",
		Status:      model.AdtySiteStatusActive,
	}
	require.NoError(t, db.Create(site).Error)
	service.InvalidateAdtySiteCache()

	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMem
		common.EmailVerificationEnabled = oldEmailVerif
		common.RegisterEnabled = oldReg
		common.PasswordLoginEnabled = oldPwdLogin
		common.PasswordRegisterEnabled = oldPwdReg
		common.EmailDomainRestrictionEnabled = oldDomainRestrict
		common.EmailAliasRestrictionEnabled = oldAliasRestrict
		common.EmailDomainWhitelist = oldWhitelist
		common.SMTPServer = oldSMTPServer
		common.SMTPFrom = oldSMTPFrom
		common.SMTPAccount = oldSMTPAccount
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
		service.AdtySendEmailAsync = oldAsyncSend
		service.AdtySendSiteEmailAsync = oldSiteAsyncSend
		service.InvalidateAdtySiteCache()
		_ = sqlDB.Close()
	})

	return db, site
}

func TestSubsiteBasicAuthIsolationAndMailGuards(t *testing.T) {
	db, site := setupSubsiteBasicAuthTestDB(t)

	// Main site turns on email verification and restricts domains to mainonly.com,
	// while leaving mail transport unconfigured to test unconfigured guards too.
	func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		common.EmailVerificationEnabled = true
		common.RegisterEnabled = true
		common.PasswordLoginEnabled = true
		common.PasswordRegisterEnabled = true
		common.EmailDomainRestrictionEnabled = true
		common.EmailAliasRestrictionEnabled = true
		common.EmailDomainWhitelist = []string{"mainonly.com"}
		common.SMTPServer = ""
		common.SMTPFrom = ""
		common.SMTPAccount = ""
		common.OptionMap["EmailProvider"] = ""
		common.OptionMap["ResendApiKey"] = ""
		common.OptionMap["ResendFrom"] = ""
	}()

	t.Run("subsite status defaults email_verification to false even when main site enables it", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
		c.Set("adty_site", site)
		c.Set("adty_site_id", site.Id)
		GetStatus(c)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp struct {
			Success bool           `json:"success"`
			Data    map[string]any `json:"data"`
		}
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
		require.True(t, resp.Success)
		assert.Equal(t, false, resp.Data["email_verification"])
		assert.Equal(t, true, resp.Data["register_enabled"])
		assert.Equal(t, true, resp.Data["password_login_enabled"])
		assert.Equal(t, true, resp.Data["password_register_enabled"])
	})

	t.Run("unconfigured subsite and unconfigured main site both reject SendEmailVerification", func(t *testing.T) {
		// Unconfigured subsite rejects instead of faking success.
		recSub := httptest.NewRecorder()
		cSub, _ := gin.CreateTestContext(recSub)
		cSub.Request = httptest.NewRequest(http.MethodGet, "/api/verification?email=alice@example.com", nil)
		cSub.Set("adty_site", site)
		cSub.Set("adty_site_id", site.Id)
		SendEmailVerification(cSub)
		assert.Contains(t, recSub.Body.String(), `"success":false`)

		// Unconfigured main site also rejects instead of faking success.
		recMain := httptest.NewRecorder()
		cMain, _ := gin.CreateTestContext(recMain)
		cMain.Request = httptest.NewRequest(http.MethodGet, "/api/verification?email=alice@mainonly.com", nil)
		SendEmailVerification(cMain)
		assert.Contains(t, recMain.Body.String(), `"success":false`)
	})

	t.Run("subsite cannot enable email verification before configuring mail or domain restriction with empty whitelist", func(t *testing.T) {
		_, err := service.SaveAdtySiteAuthConfig(site.OwnerUserId, model.AdtySiteAuthConfig{
			PasswordLoginEnabled:     true,
			RegisterEnabled:          true,
			PasswordRegisterEnabled:  true,
			EmailVerificationEnabled: true,
		})
		require.Error(t, err)

		_, err = service.SaveAdtySiteAuthConfig(site.OwnerUserId, model.AdtySiteAuthConfig{
			PasswordLoginEnabled:          true,
			RegisterEnabled:               true,
			PasswordRegisterEnabled:       true,
			EmailDomainRestrictionEnabled: true,
			EmailDomainWhitelist:          []string{},
		})
		require.Error(t, err)
	})

	t.Run("subsite basic auth settings take effect independently of main site", func(t *testing.T) {
		// Configure subsite mail transport in DB so email verification can be enabled.
		require.NoError(t, db.Create(&model.AdtySiteMailConfig{
			SiteId:     site.Id,
			Provider:   model.AdtySiteMailProviderSMTP,
			SMTPServer: "smtp.sub.example",
			SMTPPort:   587,
			SMTPFrom:   "noreply@sub.example",
		}).Error)

		saved, err := service.SaveAdtySiteAuthConfig(site.OwnerUserId, model.AdtySiteAuthConfig{
			PasswordLoginEnabled:          true,
			RegisterEnabled:               true,
			PasswordRegisterEnabled:       true,
			EmailVerificationEnabled:      true,
			EmailDomainRestrictionEnabled: true,
			EmailAliasRestrictionEnabled:  true,
			EmailDomainWhitelist:          []string{"SubSite.Example", "corp.example"},
		})
		require.NoError(t, err)
		assert.True(t, saved.MailConfigured)
		assert.True(t, saved.EmailVerificationEnabled)
		assert.Equal(t, []string{"subsite.example", "corp.example"}, saved.EmailDomainWhitelist)

		// Reload site from DB as middleware would after cache invalidation.
		var refreshedSite model.AdtySite
		require.NoError(t, db.First(&refreshedSite, site.Id).Error)

		// Status reflects subsite email_verification = true.
		recStatus := httptest.NewRecorder()
		cStatus, _ := gin.CreateTestContext(recStatus)
		cStatus.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
		cStatus.Set("adty_site", &refreshedSite)
		cStatus.Set("adty_site_id", refreshedSite.Id)
		GetStatus(cStatus)
		var statusResp struct {
			Success bool           `json:"success"`
			Data    map[string]any `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recStatus.Body.Bytes(), &statusResp))
		assert.Equal(t, true, statusResp.Data["email_verification"])

		// Mock async delivery to capture valid subsite verification emails.
		sentTo := ""
		service.AdtySendSiteEmailAsync = func(_ *model.AdtySite, _, to, _ string) {
			sentTo = to
		}

		// Subsite rejects mainonly.com (not in subsite whitelist) and alias user+1@subsite.example.
		for _, badEmail := range []string{"user@mainonly.com", "user+alias@subsite.example"} {
			recBad := httptest.NewRecorder()
			cBad, _ := gin.CreateTestContext(recBad)
			cBad.Request = httptest.NewRequest(http.MethodGet, "/api/verification?email="+badEmail, nil)
			cBad.Set("adty_site", &refreshedSite)
			cBad.Set("adty_site_id", refreshedSite.Id)
			SendEmailVerification(cBad)
			assert.Contains(t, recBad.Body.String(), `"success":false`, "expected rejection for %s", badEmail)
		}

		// Subsite accepts allowed domain without alias.
		recOK := httptest.NewRecorder()
		cOK, _ := gin.CreateTestContext(recOK)
		cOK.Request = httptest.NewRequest(http.MethodGet, "/api/verification?email=alice@subsite.example", nil)
		cOK.Set("adty_site", &refreshedSite)
		cOK.Set("adty_site_id", refreshedSite.Id)
		SendEmailVerification(cOK)
		assert.Contains(t, recOK.Body.String(), `"success":true`, recOK.Body.String())
		assert.Equal(t, "alice@subsite.example", sentTo)

		// Disable password_login_enabled and register_enabled on subsite only.
		_, err = service.SaveAdtySiteAuthConfig(site.OwnerUserId, model.AdtySiteAuthConfig{
			PasswordLoginEnabled:    false,
			RegisterEnabled:         false,
			PasswordRegisterEnabled: true,
		})
		require.NoError(t, err)
		require.NoError(t, db.First(&refreshedSite, site.Id).Error)

		// Subsite Login blocked.
		recLogin := httptest.NewRecorder()
		cLogin, _ := gin.CreateTestContext(recLogin)
		cLogin.Request = httptest.NewRequest(http.MethodPost, "/api/user/login", bytes.NewBufferString(`{"username":"x","password":"y"}`))
		cLogin.Set("adty_site", &refreshedSite)
		cLogin.Set("adty_site_id", refreshedSite.Id)
		Login(cLogin)
		assert.Contains(t, recLogin.Body.String(), `"success":false`)
		assert.Contains(t, recLogin.Body.String(), "Password login has been disabled")

		// Subsite Register blocked.
		recReg := httptest.NewRecorder()
		cReg, _ := gin.CreateTestContext(recReg)
		cReg.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", bytes.NewBufferString(`{"username":"newu","password":"Password123"}`))
		cReg.Set("adty_site", &refreshedSite)
		cReg.Set("adty_site_id", refreshedSite.Id)
		Register(cReg)
		assert.Contains(t, recReg.Body.String(), `"success":false`)
		assert.Contains(t, recReg.Body.String(), "New user registration has been disabled")

		// Main site Login is still allowed past the toggle check (fails only on invalid credentials, not disabled toggle).
		recMainLogin := httptest.NewRecorder()
		cMainLogin, _ := gin.CreateTestContext(recMainLogin)
		cMainLogin.Request = httptest.NewRequest(http.MethodPost, "/api/user/login", bytes.NewBufferString(`{"username":"nonexistent","password":"wrong"}`))
		Login(cMainLogin)
		assert.NotContains(t, recMainLogin.Body.String(), "Password login has been disabled")
	})
}

func TestAdtyEpayAndSiteAuthPostgreSQL18Acceptance(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("ADTY_TEST_PG_DSN"))
	if dsn == "" {
		t.Skip("ADTY_TEST_PG_DSN not set")
	}
	t.Setenv("CRYPTO_SECRET", "0123456789abcdef0123456789abcdef")

	baseDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlBase, err := baseDB.DB()
	require.NoError(t, err)
	defer func() { _ = sqlBase.Close() }()

	var versionNum int
	require.NoError(t, baseDB.Raw("SHOW server_version_num").Scan(&versionNum).Error)
	t.Logf("PostgreSQL server_version_num=%d", versionNum)
	require.GreaterOrEqual(t, versionNum, 180000, "PostgreSQL 18 required for acceptance")
	require.Less(t, versionNum, 190000, "PostgreSQL 18 required for acceptance")

	schema := fmt.Sprintf("adty_test_epay_auth_%d", time.Now().UnixNano())
	require.NoError(t, baseDB.Exec("CREATE SCHEMA "+schema).Error)
	defer func() {
		_ = baseDB.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE").Error
	}()

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	schemaDSN := dsn + sep + "search_path=" + schema
	pgDB, err := gorm.Open(postgres.Open(schemaDSN), &gorm.Config{})
	require.NoError(t, err)
	sqlPG, err := pgDB.DB()
	require.NoError(t, err)
	defer func() { _ = sqlPG.Close() }()

	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB = pgDB
	model.LOG_DB = pgDB
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.SetDatabaseTypes(oldMainType, oldLogType)
		service.InvalidateAdtySiteCache()
	})

	require.NoError(t, pgDB.AutoMigrate(
		&model.Option{},
		&model.User{},
		&model.TopUp{},
		&model.AuditLog{},
		&model.AuditIntent{},
	))
	// Run MigrateAdtyTablesOn twice to prove fresh + idempotent repeated migration on PostgreSQL 18.
	require.NoError(t, model.MigrateAdtyTablesOn(pgDB))
	require.NoError(t, model.MigrateAdtyTablesOn(pgDB))

	// 1. Epay channel full edit, replace, delete with orders and scenes on PG18.
	ch := &model.AdtyEpayChannel{
		Name:        "PGChannel1",
		GatewayURL:  "https://pg1.epay.example",
		MerchantId:  "5001",
		MerchantKey: "pg-secret-1",
		SignType:    model.AdtyEpaySignTypeMD5,
		OrderMode:   model.AdtyEpayOrderModeAPI,
		Status:      model.AdtyEpayStatusEnabled,
		Methods:     []model.AdtyEpayChannelMethod{{Method: "alipay", TypeCode: "alipay", Enabled: true}},
	}
	require.NoError(t, model.SaveAdtyEpayChannel(ch, false))
	require.NoError(t, pgDB.Create(&model.AdtyTopupOrder{
		TradeNo:   "PG_EPY_1",
		UserId:    1,
		Provider:  model.PaymentProviderAdtyEpay,
		Method:    "alipay",
		ChannelId: ch.Id,
	}).Error)

	// Re-run migration to confirm PostgreSQL channel_name backfill works cleanly.
	require.NoError(t, model.MigrateAdtyTablesOn(pgDB))
	var order model.AdtyTopupOrder
	require.NoError(t, pgDB.Where("trade_no = ?", "PG_EPY_1").First(&order).Error)
	assert.Equal(t, "PGChannel1", order.ChannelName)

	// Edit gateway + merchant + key on ordered channel.
	ch.Name = "PGChannel1Edited"
	ch.GatewayURL = "https://pg1-new.epay.example"
	ch.MerchantId = "5002"
	ch.MerchantKey = "pg-secret-2"
	require.NoError(t, model.SaveAdtyEpayChannel(ch, false))
	reloadedCh, err := model.GetAdtyEpayChannel(ch.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(1), reloadedCh.ConfigVersion)

	// Replace with new channel and delete old on PG18.
	rep, err := model.ReplaceAdtyEpayChannel(ch.Id, model.AdtyEpayChannelReplaceOptions{
		NewChannel: &model.AdtyEpayChannel{
			Name:        "PGChannel2",
			GatewayURL:  "https://pg2.epay.example",
			MerchantId:  "6001",
			MerchantKey: "pg-secret-new",
			SignType:    model.AdtyEpaySignTypeMD5,
			OrderMode:   model.AdtyEpayOrderModeAPI,
			Methods:     []model.AdtyEpayChannelMethod{{Method: "alipay", TypeCode: "alipay", Enabled: true}},
		},
		EnableTarget: true,
		DeleteOld:    true,
		ActorID:      1,
	})
	require.NoError(t, err)
	assert.True(t, rep.DeletedOld)
	_, err = model.GetAdtyEpayChannel(ch.Id)
	assert.ErrorIs(t, err, model.ErrAdtyEpayChannelNotFound)

	delRes, err := model.DeleteAdtyEpayChannelWithActor(rep.NewID, 1)
	require.NoError(t, err)
	assert.Equal(t, "PGChannel2", delRes.Name)

	// 2. Subsite basic auth config persistence on PG18.
	owner := &model.User{Username: "pgowner", DisplayName: "PG Owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, pgDB.Create(owner).Error)
	pgSite := &model.AdtySite{OwnerUserId: owner.Id, Subdomain: "pgsub", RootDomain: "example.test", Status: model.AdtySiteStatusActive}
	require.NoError(t, pgDB.Create(pgSite).Error)
	service.InvalidateAdtySiteCache()

	// Unconfigured mail blocks enabling email_verification_enabled.
	_, err = service.SaveAdtySiteAuthConfig(owner.Id, model.AdtySiteAuthConfig{
		PasswordLoginEnabled:     true,
		RegisterEnabled:          true,
		PasswordRegisterEnabled:  true,
		EmailVerificationEnabled: true,
	})
	require.Error(t, err)

	// Configure subsite SMTP via DB, then save auth config and read back.
	require.NoError(t, pgDB.Create(&model.AdtySiteMailConfig{
		SiteId:     pgSite.Id,
		Provider:   model.AdtySiteMailProviderSMTP,
		SMTPServer: "smtp.pgsub.example",
		SMTPPort:   587,
		SMTPFrom:   "noreply@pgsub.example",
	}).Error)

	view, err := service.SaveAdtySiteAuthConfig(owner.Id, model.AdtySiteAuthConfig{
		PasswordLoginEnabled:          true,
		RegisterEnabled:               true,
		PasswordRegisterEnabled:       false,
		EmailVerificationEnabled:      true,
		EmailDomainRestrictionEnabled: true,
		EmailAliasRestrictionEnabled:  true,
		EmailDomainWhitelist:          []string{"pgsub.example"},
	})
	require.NoError(t, err)
	assert.True(t, view.MailConfigured)
	assert.True(t, view.EmailVerificationEnabled)
	assert.False(t, view.PasswordRegisterEnabled)

	loaded, err := service.GetAdtySiteAuthConfig(owner.Id)
	require.NoError(t, err)
	assert.Equal(t, view.AdtySiteAuthConfig, loaded.AdtySiteAuthConfig)
}
