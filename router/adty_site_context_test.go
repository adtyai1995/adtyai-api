package router

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 按 Host 识别分站、分站注册和登录、/api/status 覆盖（任务 11.2）。

const siteLoginFailure = "Username or password is incorrect, or user has been banned"

type siteFixture struct {
	db      *gorm.DB
	engine  *gin.Engine
	site    *model.AdtySite
	closed  *model.AdtySite
	pending *model.AdtySite
}

func newSiteFixture(t *testing.T) *siteFixture {
	t.Helper()
	require.NoError(t, i18n.Init())
	disableAdtyRedis(t)
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.Token{}, &model.AuthFlow{}, &model.TwoFA{},
		&model.TwoFABackupCode{}, &model.PasskeyCredential{}, &model.AuditLog{}, &model.Log{}, &model.Option{}, &model.TopUp{},
		&model.UserAccessToken{}, &model.Channel{}, &model.Ability{}))
	require.NoError(t, model.MigrateAdtyTables())
	cfg := withResellerSetting(t)
	cfg.Enabled, cfg.RootDomains = true, []string{"sites.example.com"}
	cfg.CostFactor, cfg.MinCostFactor, cfg.MaxMarkup = new(0.8), new(0.5), new(3.0)
	cfg.MaxSiteBonusExtra, cfg.TrialGrantMaxUSD, cfg.TrialGrantDailyUSD = new(0.0), new(0.0), new(0.0)
	oldAddress, oldGenerate := system_setting.ServerAddress, constant.GenerateDefaultToken
	oldLegal := *adty_setting.GetLegalSetting()
	system_setting.ServerAddress, constant.GenerateDefaultToken = "https://main.example.com", false
	t.Cleanup(func() {
		system_setting.ServerAddress, constant.GenerateDefaultToken = oldAddress, oldGenerate
		*adty_setting.GetLegalSetting() = oldLegal
	})

	owner := model.User{Username: "site-owner", Password: "x", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
	require.NoError(t, db.Create(&owner).Error)
	site, err := service.OpenAdtySite(owner.Id, "shop", model.AdtySiteOpenedByAdmin, service.AdtySitePreset{})
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.AdtySite{}).Where("id = ?", site.Id).Update("brand", `{"name":"Shop AI","logo_url":"/uploads/site-assets/shop.png"}`).Error)

	other := model.User{Username: "closed-owner", Password: "x", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
	require.NoError(t, db.Create(&other).Error)
	closed, err := service.OpenAdtySite(other.Id, "gone", model.AdtySiteOpenedByAdmin, service.AdtySitePreset{})
	require.NoError(t, err)
	_, err = service.ScheduleAdtySiteClose(closed.Id, 1, service.AdtySiteClosePlan{Immediate: true})
	require.NoError(t, err)

	applicant := model.User{Username: "pending-owner", Password: "x", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
	require.NoError(t, db.Create(&applicant).Error)
	adty_setting.GetLegalSetting().SiteAgreementVersion = 1
	pending := &model.AdtySite{
		OwnerUserId: applicant.Id, Subdomain: "later", RootDomain: "sites.example.com",
		Status: model.AdtySiteStatusPending, OpenedBy: model.AdtySiteOpenedByApply,
		AgreementVer: "1", AgreementAt: 1,
	}
	require.NoError(t, db.Create(pending).Error)
	require.NoError(t, service.EnsureAdtySiteWallets(pending.Id))
	service.InvalidateAdtySiteCache()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.AdtySiteContext())
	SetApiRouter(engine)
	SetRelayRouter(engine)
	engine.NoRoute(func(c *gin.Context) { c.String(http.StatusOK, "index") })
	site, _ = service.GetAdtySite(site.Id)
	closed, _ = service.GetAdtySite(closed.Id)
	return &siteFixture{db: db, engine: engine, site: site, closed: closed, pending: pending}
}

func (f *siteFixture) do(t *testing.T, method, host, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Host = host
	recorder := httptest.NewRecorder()
	f.engine.ServeHTTP(recorder, req)
	return recorder
}

func (f *siteFixture) user(t *testing.T, name string, siteId int) model.User {
	t.Helper()
	hash, err := common.HashAccountPassword("correct-horse-1")
	require.NoError(t, err)
	user := model.User{Username: name, Password: hash, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, SiteId: siteId}
	require.NoError(t, f.db.Create(&user).Error)
	return user
}

func siteOf(t *testing.T, engine *gin.Engine, host string) *model.AdtySite {
	t.Helper()
	var captured *model.AdtySite
	probe := gin.New()
	probe.Use(middleware.AdtySiteContext())
	probe.GET("/", func(c *gin.Context) { captured = service.AdtySiteForRequest(c) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = host
	probe.ServeHTTP(httptest.NewRecorder(), req)
	return captured
}

func TestSiteContextBySubdomain(t *testing.T) {
	f := newSiteFixture(t)
	pgtest.RecordServerVersion(t, f.db)
	for _, tc := range []struct {
		host string
		want int
	}{
		{"shop.sites.example.com", f.site.Id},
		{"SHOP.Sites.Example.com:443", f.site.Id},
		{"shop.sites.example.com.", f.site.Id},
		{"main.example.com", 0},
		{"later.sites.example.com", 0}, // pending: not open yet
		{"nobody.sites.example.com", 0},
		{"shop.other.example.com", 0},
	} {
		got := siteOf(t, f.engine, tc.host)
		if tc.want == 0 {
			assert.Nil(t, got, tc.host)
			continue
		}
		require.NotNil(t, got, tc.host)
		assert.Equal(t, tc.want, got.Id, tc.host)
	}
	// 遗留 paused 行仍应识别，但不能再通过已退役的暂停写路径写入。
	require.NoError(t, f.db.Model(&model.AdtySite{}).Where("id = ?", f.site.Id).Update("status", model.AdtySiteStatusPaused).Error)
	service.InvalidateAdtySiteCache()
	require.NotNil(t, siteOf(t, f.engine, "shop.sites.example.com"))
	// 分站功能关了就不识别。
	adty_setting.GetResellerSetting().Enabled = false
	assert.Nil(t, siteOf(t, f.engine, "shop.sites.example.com"))
}

func TestSiteContextByActiveCustomDomainOnly(t *testing.T) {
	f := newSiteFixture(t)
	domains := map[string]int{}
	saved := service.AdtyLoadCustomDomains
	service.AdtyLoadCustomDomains = func() (map[string]int, error) { return domains, nil }
	t.Cleanup(func() {
		service.AdtyLoadCustomDomains = saved
		service.InvalidateAdtySiteCache()
	})
	service.InvalidateAdtySiteCache()
	assert.Nil(t, siteOf(t, f.engine, "api.customer-brand.com"), "a pending custom domain is not in the active list")

	domains["api.customer-brand.com"] = f.site.Id
	service.InvalidateAdtySiteCache()
	got := siteOf(t, f.engine, "API.customer-brand.com")
	require.NotNil(t, got)
	assert.Equal(t, f.site.Id, got.Id)
	assert.Equal(t, "https://shop.sites.example.com", service.AdtySiteBaseURL(got), "links stay on the platform subdomain")
}

func TestCustomDomainServesVerificationAndReset(t *testing.T) {
	f := newSiteFixture(t)
	saved := service.AdtyLoadCustomDomains
	service.AdtyLoadCustomDomains = func() (map[string]int, error) {
		return map[string]int{"api.customer-brand.com": f.site.Id}, nil
	}
	t.Cleanup(func() {
		service.AdtyLoadCustomDomains = saved
		service.InvalidateAdtySiteCache()
	})
	// Verification mail is dispatched in the background and reads SMTP globals.
	// Wait before the next request, and again before this test returns, so those
	// sends cannot overlap a sender lookup or a later SMTP config change.
	t.Cleanup(service.WaitAdtySystemEmailAsync)
	service.InvalidateAdtySiteCache()
	do := func(method, host, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		response := f.do(t, method, host, path, body)
		service.WaitAdtySystemEmailAsync()
		return response
	}
	checks := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/verification?email=a@example.com"},
		{http.MethodGet, "/api/reset_password?email=a@example.com"},
		{http.MethodPost, "/api/user/reset"},
	}
	for _, check := range checks {
		// 自定义域名承接完整客户端体验：验证码、找回密码都不再被中间件拒绝。
		custom := do(check.method, "api.customer-brand.com", check.path, `{"email":"a@example.com","token":"x","password":"correct-horse-1"}`)
		assert.NotEqual(t, http.StatusForbidden, custom.Code, check.path)
		assert.Empty(t, custom.Header().Get("Location"), check.path)
		assert.Equal(t, http.StatusNotFound, do(check.method, "stranger.example.net", check.path, `{}`).Code, check.path)
		assert.Equal(t, http.StatusForbidden, do(check.method, "gone.sites.example.com", check.path, `{}`).Code, check.path)
		assert.NotEqual(t, http.StatusForbidden, do(check.method, "shop.sites.example.com", check.path, `{}`).Code, check.path)
		assert.NotEqual(t, http.StatusNotFound, do(check.method, "main.example.com", check.path, `{}`).Code, check.path)
	}
}

func TestCustomDomainServesAccountAPI(t *testing.T) {
	f := newSiteFixture(t)
	saved := service.AdtyLoadCustomDomains
	service.AdtyLoadCustomDomains = func() (map[string]int, error) {
		return map[string]int{"api.customer-brand.com": f.site.Id}, nil
	}
	t.Cleanup(func() {
		service.AdtyLoadCustomDomains = saved
		service.InvalidateAdtySiteCache()
	})
	service.InvalidateAdtySiteCache()

	// 控制台页面不再跳回平台子域名。
	for _, path := range []string{"/sign-in", "/wallet"} {
		page := f.do(t, http.MethodGet, "api.customer-brand.com", path, "")
		assert.NotEqual(t, http.StatusFound, page.Code, path)
		assert.Empty(t, page.Header().Get("Location"), path)
		assert.NotEqual(t, http.StatusForbidden, page.Code, path)
	}
	assert.Equal(t, http.StatusOK, f.do(t, http.MethodGet, "api.customer-brand.com", "/", "").Code)

	// 登录、充值、营销偏好等客户端接口都放行。
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/user/self", ""},
		{http.MethodPost, "/api/adty/epay/pay", `{}`},
		{http.MethodPut, "/api/adty/marketing/self", `{}`},
	} {
		response := f.do(t, request.method, "api.customer-brand.com", request.path, request.body)
		assert.NotEqual(t, http.StatusForbidden, response.Code, request.path+": "+response.Body.String())
		assert.NotEqual(t, http.StatusFound, response.Code, request.path)
		assert.Empty(t, response.Header().Get("Location"), request.path)
	}

	// 分站客户可以直接在自定义域名上登录，主站账号仍然进不来。
	customer := f.user(t, "custom-domain-customer", f.site.Id)
	login := func(username string) string {
		body := fmt.Sprintf(`{"username":%q,"password":"correct-horse-1"}`, username)
		return f.do(t, http.MethodPost, "api.customer-brand.com", "/api/user/login", body).Body.String()
	}
	require.Contains(t, login(customer.Username), `"success":true`)
	outsider := f.user(t, "custom-domain-outsider", 0)
	assert.Contains(t, login(outsider.Username), `"success":false`, "a main-site account cannot sign in on a custom domain")

	// 平台级管理接口仍然拒绝。
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/api/adty/admin/sites"},
		{http.MethodGet, "/api/channel/"},
		{http.MethodGet, "/api/option/"},
		{http.MethodGet, "/api/deployments"},
		{http.MethodGet, "/api/adty/marketing/admin/campaigns"},
	} {
		assert.Equal(t, http.StatusForbidden, f.do(t, request.method, "api.customer-brand.com", request.path, "").Code, request.path)
	}
	assert.NotEqual(t, http.StatusForbidden, f.do(t, http.MethodGet, "shop.sites.example.com", "/api/user/self", "").Code)
}

func TestCustomDomainRejectsOAuthPath(t *testing.T) {
	f := newSiteFixture(t)
	saved := service.AdtyLoadCustomDomains
	service.AdtyLoadCustomDomains = func() (map[string]int, error) {
		return map[string]int{"api.customer-brand.com": f.site.Id}, nil
	}
	t.Cleanup(func() {
		service.AdtyLoadCustomDomains = saved
		service.InvalidateAdtySiteCache()
	})
	service.InvalidateAdtySiteCache()

	// /api/oauth now only holds email binding; the host rules for the prefix are unchanged.
	const path = "/api/oauth/email/bind/start"
	body := `{"email":"someone@example.com"}`
	custom := f.do(t, http.MethodPost, "api.customer-brand.com", path, body)
	assert.Equal(t, http.StatusForbidden, custom.Code, custom.Body.String())
	assert.Equal(t, http.StatusNotFound, f.do(t, http.MethodPost, "stranger.example.net", path, body).Code)
	assert.Equal(t, http.StatusForbidden, f.do(t, http.MethodPost, "gone.sites.example.com", path, body).Code)

	platform := f.do(t, http.MethodPost, "shop.sites.example.com", path, body)
	assert.NotEqual(t, http.StatusForbidden, platform.Code, platform.Body.String())
	assert.NotEqual(t, http.StatusNotFound, platform.Code, platform.Body.String())
}

func TestUnknownHostModes(t *testing.T) {
	f := newSiteFixture(t)
	cfg := adty_setting.GetResellerSetting()
	assert.Equal(t, http.StatusNotFound, f.do(t, http.MethodGet, "stranger.example.net", "/pricing", "").Code,
		"an unrecognized host cannot be routed to the main site")

	cfg.UnknownHostMode = adty_setting.ResellerUnknownHostReject
	assert.Equal(t, http.StatusNotFound, f.do(t, http.MethodGet, "stranger.example.net", "/pricing", "").Code)
	assert.Equal(t, http.StatusNotFound, f.do(t, http.MethodGet, "stranger.example.net", "/api/status", "").Code,
		"unknown-host APIs cannot silently run in the main-site scope")
	assert.Equal(t, "index", f.do(t, http.MethodGet, "main.example.com", "/pricing", "").Body.String(), "the main host is never rejected")
	assert.Equal(t, "index", f.do(t, http.MethodGet, "shop.sites.example.com", "/pricing", "").Body.String())
}

func TestAPIKeyMustMatchRequestSite(t *testing.T) {
	f := newSiteFixture(t)
	mainUser := f.user(t, "main-api-key-user", 0)
	siteUser := f.user(t, "site-api-key-user", f.site.Id)
	mainKey := "mainSiteKey"
	siteKey := "siteSiteKey"
	for _, token := range []*model.Token{
		{UserId: mainUser.Id, Key: mainKey, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true},
		{UserId: siteUser.Id, Key: siteKey, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true},
	} {
		require.NoError(t, f.db.Create(token).Error)
	}
	request := func(host, key string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		req.Host = host
		req.Header.Set("Authorization", "Bearer "+key)
		response := httptest.NewRecorder()
		f.engine.ServeHTTP(response, req)
		return response
	}

	// Relay is served on the site API host, not the customer web host.
	// A key for this site must succeed there and fail everywhere else.
	service.InvalidateAdtySiteCache()
	siteAPI := fmt.Sprintf("api-s%d.sites.example.com", f.site.Id)
	own := request(siteAPI, siteKey)
	assert.Equal(t, http.StatusOK, own.Code, own.Body.String())
	assert.Equal(t, http.StatusForbidden, request("shop.sites.example.com", siteKey).Code, "customer web hosts do not serve relay")
	assert.Equal(t, http.StatusForbidden, request("main.example.com", siteKey).Code)
	main := request("main.example.com", mainKey)
	assert.Equal(t, http.StatusOK, main.Code, main.Body.String())
	assert.Equal(t, http.StatusForbidden, request(siteAPI, mainKey).Code, main.Body.String())
	assert.Equal(t, http.StatusForbidden, request("shop.sites.example.com", mainKey).Code)
	assert.Equal(t, http.StatusNotFound, request("stranger.example.net", mainKey).Code)
}

func TestClosedSiteRevokesMigratedCustomerKeys(t *testing.T) {
	for _, immediate := range []bool{false, true} {
		t.Run(fmt.Sprintf("immediate=%t", immediate), func(t *testing.T) {
			f := newSiteFixture(t)
			targetHost := "main.example.com"
			customer := f.user(t, "migrated-key-customer", f.site.Id)
			require.NoError(t, f.db.Create(&model.AdtySiteCustomer{UserId: customer.Id, SiteId: f.site.Id}).Error)
			old := model.Token{UserId: customer.Id, Key: "migrationOldSecret", Name: "old key", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, UsedQuota: 40, RemainQuota: 60}
			require.NoError(t, f.db.Create(&old).Error)
			otherUser := f.user(t, "untouched-key-customer", 0)
			other := model.Token{UserId: otherUser.Id, Key: "untouchedSecret", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
			require.NoError(t, f.db.Create(&other).Error)
			fresh := model.Token{UserId: customer.Id, Key: "migrationFreshSecret", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
			history := model.Log{UserId: customer.Id, TokenId: old.Id, TokenName: old.Name, Type: model.LogTypeConsume, Quota: 40}
			require.NoError(t, f.db.Create(&history).Error)
			if address := os.Getenv("ADTY_TEST_REDIS_ADDR"); address != "" {
				previousEnabled, previousClient := common.RedisEnabled, common.RDB
				client := redis.NewClient(&redis.Options{Addr: address, DB: 8})
				require.NoError(t, client.Ping(t.Context()).Err())
				var cacheKeys []string
				for _, token := range []model.Token{old, other, fresh} {
					cacheKey := "token:" + common.GenerateHMAC(token.Key)
					cacheKeys = append(cacheKeys, cacheKey, "token:generation:{"+cacheKey+"}")
				}
				for _, id := range []int{customer.Id, otherUser.Id} {
					userKey := fmt.Sprintf("user:%d", id)
					cacheKeys = append(cacheKeys, userKey, "auth:user:version:{"+userKey+"}", "auth:user:fence:{"+userKey+"}")
				}
				t.Cleanup(func() {
					assert.NoError(t, client.Del(context.Background(), cacheKeys...).Err())
					common.RedisEnabled, common.RDB = previousEnabled, previousClient
					assert.NoError(t, client.Close())
				})
				// Each PostgreSQL schema starts IDs again; remove only this
				// fixture's keys so earlier subtests cannot supply user metadata.
				require.NoError(t, client.Del(t.Context(), cacheKeys...).Err())
				common.RedisEnabled, common.RDB = true, client
			}
			request := func(host, path, key string) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Host = host
				req.Header.Set("Authorization", "Bearer "+key)
				response := httptest.NewRecorder()
				f.engine.ServeHTTP(response, req)
				return response
			}
			service.InvalidateAdtySiteCache()
			siteWeb := service.AdtySiteDisplayHost(f.site)
			siteAPI := fmt.Sprintf("api-s%d.%s", f.site.Id, strings.ToLower(f.site.RootDomain))
			for _, path := range []string{"/v1/models", "/api/usage/token/", "/api/log/token"} {
				host := siteWeb
				if path == "/v1/models" {
					host = siteAPI
				}
				response := request(host, path, old.Key)
				require.Equal(t, http.StatusOK, response.Code, path+" on "+host+": "+response.Body.String())
			}
			staleCache := map[string]string{}
			if common.RedisEnabled {
				oldCacheKey := "token:" + common.GenerateHMAC(old.Key)
				for _, key := range []string{oldCacheKey, "token:generation:{" + oldCacheKey + "}", fmt.Sprintf("user:%d", customer.Id)} {
					data, err := common.RDB.Dump(t.Context(), key).Result()
					if err == redis.Nil {
						continue
					}
					require.NoError(t, err)
					staleCache[key] = data
				}
				require.NotEmpty(t, staleCache)
			}
			scheduled, err := service.ScheduleAdtySiteClose(f.site.Id, 1, service.AdtySiteClosePlan{Immediate: immediate})
			require.NoError(t, err)
			if !immediate {
				closing := request(siteAPI, "/v1/models", old.Key)
				assert.Equal(t, http.StatusOK, closing.Code, closing.Body.String())
				require.NoError(t, service.RunAdtySiteCloseOnce(scheduled.CloseExecuteAt))
			}
			for _, path := range []string{"/v1/models", "/api/usage/token/", "/api/log/token"} {
				response := request(targetHost, path, old.Key)
				assert.Equal(t, http.StatusUnauthorized, response.Code, path+": "+response.Body.String())
			}
			if common.RedisEnabled {
				for key, data := range staleCache {
					require.NoError(t, common.RDB.RestoreReplace(t.Context(), key, time.Minute, data).Err())
				}
				for _, path := range []string{"/v1/models", "/api/usage/token/", "/api/log/token"} {
					assert.Equal(t, http.StatusUnauthorized, request(targetHost, path, old.Key).Code, "restored token/user caches must not restore credentials")
				}
			}
			assert.Equal(t, http.StatusOK, request("main.example.com", "/v1/models", other.Key).Code)
			require.NoError(t, fresh.InsertWithLimit(100))
			assert.Equal(t, http.StatusOK, request(targetHost, "/v1/models", fresh.Key).Code)
			var retired model.Token
			require.NoError(t, f.db.Unscoped().First(&retired, old.Id).Error)
			assert.True(t, retired.DeletedAt.Valid, "old credentials must not be re-enabled by normal token editing")
			assert.Equal(t, 40, retired.UsedQuota)
			assert.Equal(t, 60, retired.RemainQuota)
			var preserved model.Log
			require.NoError(t, f.db.First(&preserved, history.Id).Error)
			assert.Equal(t, history, preserved)
		})
	}
}

func TestAPIKeyReadOnlyAndCrossSiteHostIsolation(t *testing.T) {
	f := newSiteFixture(t)
	otherOwner := f.user(t, "other-api-owner", 0)
	other, err := service.OpenAdtySite(otherOwner.Id, "other-api-site", model.AdtySiteOpenedByAdmin, service.AdtySitePreset{})
	require.NoError(t, err)
	customer := f.user(t, "read-only-key-user", f.site.Id)
	key := "readonlySiteKey"
	require.NoError(t, f.db.Create(&model.Token{UserId: customer.Id, Key: key, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}).Error)
	var quotaBefore int
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", customer.Id).Select("quota").Scan(&quotaBefore).Error)
	ownWeb := service.AdtySiteDisplayHost(f.site)
	otherWeb := service.AdtySiteDisplayHost(other)
	request := func(host, path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = host
		req.Header.Set("Authorization", "Bearer "+key)
		// A spoofed forwarded host must not move the key onto another site.
		req.Header.Set("X-Forwarded-Host", ownWeb)
		response := httptest.NewRecorder()
		f.engine.ServeHTTP(response, req)
		return response
	}
	for _, path := range []string{"/api/usage/token/", "/api/log/token"} {
		assert.Equal(t, http.StatusOK, request(ownWeb, path).Code, path+" on own web host")
		assert.Equal(t, http.StatusForbidden, request(otherWeb, path).Code, path+" on other web host")
		assert.Equal(t, http.StatusForbidden, request("main.example.com", path).Code, path+" on main host")
		assert.Equal(t, http.StatusNotFound, request("stranger.example.net", path).Code, path+" on stranger host")
	}
	// Customer web hosts do not serve relay. /v1/models is 403 there even for the key's own site.
	for _, host := range []string{ownWeb, otherWeb} {
		assert.Equal(t, http.StatusForbidden, request(host, "/v1/models").Code, "/v1/models on "+host)
	}
	assert.Equal(t, http.StatusForbidden, request("main.example.com", "/v1/models").Code, "site key must not list models on the main host")
	assert.Equal(t, http.StatusNotFound, request("stranger.example.net", "/v1/models").Code, "unknown host stays 404")

	service.InvalidateAdtySiteCache()
	ownAPI := fmt.Sprintf("api-s%d.sites.example.com", f.site.Id)
	otherAPI := fmt.Sprintf("api-s%d.sites.example.com", other.Id)
	ownModels := request(ownAPI, "/v1/models")
	assert.Equal(t, http.StatusOK, ownModels.Code, ownModels.Body.String())
	assert.Contains(t, ownModels.Body.String(), `"data"`)
	assert.NotEqual(t, "index", ownModels.Body.String())
	assert.Equal(t, http.StatusForbidden, request(otherAPI, "/v1/models").Code, "site key must not relay on another site API host")
	var quotaAfter int
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", customer.Id).Select("quota").Scan(&quotaAfter).Error)
	assert.Equal(t, quotaBefore, quotaAfter, "read-only key checks must not change the caller quota")
}

func TestUnknownHostRejectedWhenResellerDisabled(t *testing.T) {
	f := newSiteFixture(t)
	adty_setting.GetResellerSetting().Enabled = false
	mainUser := f.user(t, "disabled-main-key", 0)
	require.NoError(t, f.db.Create(&model.Token{UserId: mainUser.Id, Key: "disabledResellerKey", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}).Error)
	for _, path := range []string{"/pricing", "/api/status", "/v1/models"} {
		for _, host := range []string{"stranger.example.net", "shop.sites.example.com"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Host = host
			req.Header.Set("Authorization", "Bearer disabledResellerKey")
			response := httptest.NewRecorder()
			f.engine.ServeHTTP(response, req)
			assert.Equal(t, http.StatusNotFound, response.Code, path+" on "+host)
		}
	}
}

func TestClosedSubdomainRedirects(t *testing.T) {
	f := newSiteFixture(t)
	resp := f.do(t, http.MethodGet, "gone.sites.example.com", "/wallet?x=1", "")
	assert.Equal(t, http.StatusFound, resp.Code)
	assert.Equal(t, "https://main.example.com/", resp.Header().Get("Location"), "closed pages do not forward sensitive query parameters")
	assert.Equal(t, http.StatusForbidden, f.do(t, http.MethodPost, "gone.sites.example.com", "/wallet", "").Code)
	assert.Equal(t, http.StatusForbidden, f.do(t, http.MethodGet, "gone.sites.example.com", "/api/status", "").Code, "closed sites cannot run API requests as the main site")

	// Historical customer migration targets do not change the retained redirect.
	require.NoError(t, f.db.Model(&model.AdtySite{}).Where("id = ?", f.closed.Id).Update("close_transfer_to", f.site.Id).Error)
	service.InvalidateAdtySiteCache()
	resp = f.do(t, http.MethodGet, "gone.sites.example.com", "/", "")
	assert.Equal(t, "https://main.example.com/", resp.Header().Get("Location"), "closed entry points redirect only to the platform")
}

func TestRegisterOnSiteSetsSiteId(t *testing.T) {
	f := newSiteFixture(t)
	register := func(host, name string) *httptest.ResponseRecorder {
		return f.do(t, http.MethodPost, host, "/api/user/register", fmt.Sprintf(`{"username":%q,"password":"correct-horse-1"}`, name))
	}
	require.Contains(t, register("shop.sites.example.com", "on-site").Body.String(), `"success":true`)
	require.Contains(t, register("main.example.com", "on-main").Body.String(), `"success":true`)
	var onSite, onMain model.User
	require.NoError(t, f.db.Where("username = ?", "on-site").First(&onSite).Error)
	require.NoError(t, f.db.Where("username = ?", "on-main").First(&onMain).Error)
	assert.Equal(t, f.site.Id, onSite.SiteId)
	assert.Zero(t, onMain.SiteId)

	var customer model.AdtySiteCustomer
	require.NoError(t, f.db.First(&customer, onSite.Id).Error)
	assert.Equal(t, f.site.Id, customer.SiteId)
	var affiliates int64
	require.NoError(t, f.db.Model(&model.ReferralAffiliate{}).Where("user_id = ?", onSite.Id).Count(&affiliates).Error)
	assert.Zero(t, affiliates, "site customers do not join the P6 program")
	require.NoError(t, f.db.Model(&model.ReferralAffiliate{}).Where("user_id = ?", onMain.Id).Count(&affiliates).Error)
	assert.EqualValues(t, 1, affiliates)

	_, err := service.ScheduleAdtySiteClose(f.site.Id, 1, service.AdtySiteClosePlan{})
	require.NoError(t, err)
	service.InvalidateAdtySiteCache()
	assert.Contains(t, register("shop.sites.example.com", "too-late").Body.String(), `"success":false`, "a closing site takes no new customers")
}

func TestMainLegalConsentIsRequiredAndRecorded(t *testing.T) {
	f := newSiteFixture(t)
	legal := system_setting.GetLegalSettings()
	oldAgreement, oldPrivacy := legal.UserAgreement, legal.PrivacyPolicy
	t.Cleanup(func() {
		legal.UserAgreement, legal.PrivacyPolicy = oldAgreement, oldPrivacy
	})
	legal.UserAgreement = "current terms"
	legal.PrivacyPolicy = "current privacy"

	missing := f.do(t, http.MethodPost, "main.example.com", "/api/user/register", `{"username":"no-legal","password":"correct-horse-1"}`)
	assert.Contains(t, missing.Body.String(), "Accept the current terms first")
	var missingUser int64
	require.NoError(t, f.db.Model(&model.User{}).Where("username = ?", "no-legal").Count(&missingUser).Error)
	assert.Zero(t, missingUser)

	accepted := f.do(t, http.MethodPost, "main.example.com", "/api/user/register", `{"username":"yes-legal","password":"correct-horse-1","agreement_accepted":true}`)
	require.Contains(t, accepted.Body.String(), `"success":true`, accepted.Body.String())
	var user model.User
	require.NoError(t, f.db.Where("username = ?", "yes-legal").First(&user).Error)
	var logs []model.Log
	require.NoError(t, f.db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeSystem).Find(&logs).Error)
	found := false
	for _, row := range logs {
		if strings.Contains(row.Content, "Accepted the current user agreement and privacy policy") {
			found = true
		}
	}
	assert.True(t, found, "accepting the published terms must leave a system log")
}

func TestSiteTermsRequiredAtRegister(t *testing.T) {
	f := newSiteFixture(t)
	legal := adty_setting.GetLegalSetting()
	old := legal.SiteCustomerTermsVersion
	legal.SiteCustomerTermsVersion = old + 4
	t.Cleanup(func() { legal.SiteCustomerTermsVersion = old })
	current := service.AdtyCurrentSiteCustomerTermsVersion()

	missing := f.do(t, http.MethodPost, "shop.sites.example.com", "/api/user/register", `{"username":"no-terms","password":"correct-horse-1"}`)
	assert.Contains(t, missing.Body.String(), `"success":false`)
	assert.Contains(t, missing.Body.String(), "Accept the current site customer terms first")
	var missingUser int64
	require.NoError(t, f.db.Model(&model.User{}).Where("username = ?", "no-terms").Count(&missingUser).Error)
	assert.Zero(t, missingUser)

	wrong := f.do(t, http.MethodPost, "shop.sites.example.com", "/api/user/register?site_terms_version=0", `{"username":"old-terms","password":"correct-horse-1"}`)
	assert.Contains(t, wrong.Body.String(), `"success":false`)

	main := f.do(t, http.MethodPost, "main.example.com", "/api/user/register", `{"username":"main-no-terms","password":"correct-horse-1"}`)
	assert.Contains(t, main.Body.String(), `"success":true`)

	accepted := f.do(t, http.MethodPost, "shop.sites.example.com", "/api/user/register?site_terms_version="+current, `{"username":"with-terms","password":"correct-horse-1"}`)
	require.Contains(t, accepted.Body.String(), `"success":true`, accepted.Body.String())
	var user model.User
	require.NoError(t, f.db.Where("username = ?", "with-terms").First(&user).Error)
	var row model.AdtySiteCustomer
	require.NoError(t, f.db.Where("user_id = ?", user.Id).First(&row).Error)
	assert.Equal(t, current, row.TermsVersion)
	assert.NotZero(t, row.TermsAt)
	assert.Equal(t, f.site.Id, row.SiteId)
}

func TestSiteCustomerLoginOnlyOnOwnSite(t *testing.T) {
	f := newSiteFixture(t)
	customer := f.user(t, "shop-customer", f.site.Id)
	mainUser := f.user(t, "main-customer", 0)
	login := func(host, username, password string) (bool, string) {
		t.Helper()
		resp := f.do(t, http.MethodPost, host, "/api/user/login", fmt.Sprintf(`{"username":%q,"password":%q}`, username, password))
		var envelope apiEnvelope
		require.NoError(t, common.Unmarshal(resp.Body.Bytes(), &envelope), resp.Body.String())
		return envelope.Success, envelope.Message
	}
	ok, _ := login("shop.sites.example.com", customer.Username, "correct-horse-1")
	assert.True(t, ok)
	ok, _ = login("main.example.com", mainUser.Username, "correct-horse-1")
	assert.True(t, ok)

	_, wrongPassword := login("main.example.com", mainUser.Username, "wrong-password")
	_, unknownUser := login("main.example.com", "nobody-at-all", "correct-horse-1")
	ok, wrongSite := login("main.example.com", customer.Username, "correct-horse-1")
	assert.False(t, ok)
	assert.Equal(t, siteLoginFailure, wrongPassword)
	assert.Equal(t, wrongPassword, wrongSite, "same message as a wrong password: no account enumeration")
	assert.Equal(t, wrongPassword, unknownUser)
	ok, onOtherSite := login("shop.sites.example.com", mainUser.Username, "correct-horse-1")
	assert.False(t, ok, "main-site users cannot sign in on a reseller site")
	assert.Equal(t, siteLoginFailure, onOtherSite)

	// 二次验证：挑战不能在别的站上发出来，也不能拿到别的站去完成。
	factor := &model.TwoFA{UserId: customer.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}
	require.NoError(t, f.db.Create(factor).Error)
	ok, message := login("main.example.com", customer.Username, "correct-horse-1")
	assert.False(t, ok)
	assert.Equal(t, siteLoginFailure, message, "no 2FA challenge is issued on the wrong site")
	resp := f.do(t, http.MethodPost, "shop.sites.example.com", "/api/user/login", fmt.Sprintf(`{"username":%q,"password":"correct-horse-1"}`, customer.Username))
	var challenge struct {
		Success bool `json:"success"`
		Data    struct {
			FlowToken string `json:"flow_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(resp.Body.Bytes(), &challenge))
	require.True(t, challenge.Success, resp.Body.String())
	require.NotEmpty(t, challenge.Data.FlowToken)
	code, err := totp.GenerateCode(factor.Secret, time.Now())
	require.NoError(t, err)
	verify := f.do(t, http.MethodPost, "main.example.com", "/api/user/login/verify", fmt.Sprintf(`{"flow_token":%q,"code":%q}`, challenge.Data.FlowToken, code))
	assert.Contains(t, verify.Body.String(), siteLoginFailure)
	assert.NotContains(t, verify.Body.String(), "access_token")
	var active int64
	require.NoError(t, f.db.Model(&model.UserSession{}).Where("user_id = ? AND status = ?", customer.Id, model.UserSessionStatusActive).Count(&active).Error)
	assert.EqualValues(t, 1, active, "only the first password login on the shop site holds a session")

	adty_setting.GetResellerSetting().SiteLoginOnly = false
	ok, _ = login("main.example.com", mainUser.Username, "correct-horse-1")
	assert.True(t, ok)
	require.NoError(t, f.db.Where("user_id = ?", customer.Id).Delete(&model.TwoFA{}).Error)
	ok, _ = login("main.example.com", customer.Username, "correct-horse-1")
	assert.True(t, ok, "the switch turns the restriction off")
}

func TestAPIKeySiteBindingIgnoresForwardedHostAndAllowsOwnCustomDomain(t *testing.T) {
	f := newSiteFixture(t)
	customer := f.user(t, "token-customer", f.site.Id)
	require.NoError(t, f.db.Create(&model.Token{UserId: customer.Id, Key: "sitetokenkey", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}).Error)
	saved := service.AdtyLoadCustomDomains
	savedRoles := service.AdtyLoadApiOnlyDomains
	service.AdtyLoadApiOnlyDomains = func() (map[string]bool, error) { return map[string]bool{"api.customer-brand.com": true}, nil }
	t.Cleanup(func() { service.AdtyLoadApiOnlyDomains = savedRoles; service.InvalidateAdtySiteCache() })
	service.AdtyLoadCustomDomains = func() (map[string]int, error) {
		return map[string]int{"api.customer-brand.com": f.site.Id}, nil
	}
	t.Cleanup(func() { service.AdtyLoadCustomDomains = saved; service.InvalidateAdtySiteCache() })
	service.InvalidateAdtySiteCache()
	for _, tc := range []struct {
		host   string
		status int
	}{
		{"main.example.com", http.StatusForbidden},
		{"shop.sites.example.com", http.StatusForbidden},
		{fmt.Sprintf("api-s%d.sites.example.com", f.site.Id), http.StatusOK},
		{"api.customer-brand.com", http.StatusOK},
		{"stranger.example.net", http.StatusNotFound},
		{"gone.sites.example.com", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		req.Host = tc.host
		req.Header.Set("X-Forwarded-Host", "shop.sites.example.com")
		req.Header.Set("Authorization", "Bearer sk-sitetokenkey")
		recorder := httptest.NewRecorder()
		f.engine.ServeHTTP(recorder, req)
		assert.Equal(t, tc.status, recorder.Code, tc.host+": "+recorder.Body.String())
	}
}

func TestStatusReturnsSiteBrand(t *testing.T) {
	f := newSiteFixture(t)
	oldName, oldFooter, oldQR, oldTurnstile := common.SystemName, common.Footer, common.WeChatAccountQRCodeImageURL, common.TurnstileSiteKey
	common.SystemName = "ADTY AI"
	common.Footer = "<p>main-footer</p>"
	common.WeChatAccountQRCodeImageURL = "https://cdn.example/qr.png"
	common.TurnstileSiteKey = "global-turnstile"
	cfg := adty_setting.GetResellerSetting()
	oldWidgets, oldSecrets := cfg.TurnstileWidgets, cfg.TurnstileWidgetSecret
	cfg.TurnstileWidgets = []adty_setting.ResellerTurnstileWidget{{Id: "widget-1", SiteKey: "site-widget-key"}}
	cfg.TurnstileWidgetSecret = map[string]string{"widget-1": "site-widget-secret"}
	t.Cleanup(func() {
		common.SystemName, common.Footer, common.WeChatAccountQRCodeImageURL, common.TurnstileSiteKey = oldName, oldFooter, oldQR, oldTurnstile
		cfg.TurnstileWidgets, cfg.TurnstileWidgetSecret = oldWidgets, oldSecrets
	})
	status := func(host string) map[string]any {
		t.Helper()
		var envelope apiEnvelope
		require.NoError(t, common.Unmarshal(f.do(t, http.MethodGet, host, "/api/status", "").Body.Bytes(), &envelope))
		return envelope.Data
	}
	main := status("main.example.com")
	assert.Equal(t, "ADTY AI", main["system_name"])
	assert.Equal(t, "https://main.example.com", main["server_address"])
	assert.Nil(t, main["adty_site"])
	assert.Equal(t, "<p>main-footer</p>", main["footer_html"])
	assert.Equal(t, "global-turnstile", main["turnstile_site_key"])
	assert.Equal(t, "main.example.com", main["passkey_rp_id"])

	site := status("shop.sites.example.com")
	assert.Equal(t, "Shop AI", site["system_name"])
	assert.Equal(t, "/uploads/site-assets/shop.png", site["logo"])
	assert.Equal(t, fmt.Sprintf("https://api-s%d.sites.example.com", f.site.Id), site["server_address"])
	assert.Equal(t, false, site["github_oauth"])
	assert.Equal(t, false, site["oidc_enabled"])
	assert.Equal(t, false, site["passkey_login"])
	assert.Nil(t, site["custom_oauth_providers"])
	assert.Equal(t, "", site["footer_html"])
	assert.Equal(t, "", site["wechat_qrcode"])
	assert.Equal(t, "", site["docs_link"])
	assert.Equal(t, "", site["passkey_rp_id"])
	assert.Empty(t, site["chats"])
	assert.Empty(t, site["api_info"])
	assert.Empty(t, site["announcements"])
	assert.Empty(t, site["faq"])
	assert.Empty(t, site["passkey_rp_ids"])
	assert.Equal(t, "site-widget-key", site["turnstile_site_key"])
}

func TestStatusDoesNotQuerySitesWhileHoldingOptionLock(t *testing.T) {
	f := newSiteFixture(t)
	cfg := adty_setting.GetResellerSetting()
	cfg.TurnstileWidgets = []adty_setting.ResellerTurnstileWidget{{Id: "widget-1", SiteKey: "site-widget-key"}}
	service.InvalidateAdtySiteCache()
	var underLock, outside int
	require.NoError(t, f.db.Callback().Query().After("gorm:query").Register("status_site_query_lock", func(tx *gorm.DB) {
		table := tx.Statement.Table
		if table != "adty_sites" && table != "adty_site_domains" {
			return
		}
		if common.OptionMapRWMutex.TryLock() {
			common.OptionMapRWMutex.Unlock()
			outside++
			return
		}
		underLock++
	}))
	t.Cleanup(func() {
		_ = f.db.Callback().Query().Remove("status_site_query_lock")
	})
	response := f.do(t, http.MethodGet, "shop.sites.example.com", "/api/status", "")
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Greater(t, outside, 0, "a cold site cache still loads the site table before the status handler")
	assert.Zero(t, underLock, "status must not query site tables while holding the option map lock")
}

func TestSiteNoticeAndHomePageContentAreEmpty(t *testing.T) {
	f := newSiteFixture(t)
	common.OptionMapRWMutex.Lock()
	oldNotice, oldHome := common.OptionMap["Notice"], common.OptionMap["HomePageContent"]
	common.OptionMap["Notice"] = "main-notice"
	common.OptionMap["HomePageContent"] = "<p>main-home</p>"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap["Notice"], common.OptionMap["HomePageContent"] = oldNotice, oldHome
		common.OptionMapRWMutex.Unlock()
	})
	body := func(host, path string) string {
		t.Helper()
		var envelope struct {
			Success bool   `json:"success"`
			Data    string `json:"data"`
		}
		response := f.do(t, http.MethodGet, host, path, "")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &envelope))
		require.True(t, envelope.Success, response.Body.String())
		return envelope.Data
	}
	assert.Equal(t, "main-notice", body("main.example.com", "/api/notice"))
	assert.Equal(t, "<p>main-home</p>", body("main.example.com", "/api/home_page_content"))
	assert.Empty(t, body("shop.sites.example.com", "/api/notice"))
	assert.Empty(t, body("shop.sites.example.com", "/api/home_page_content"))
}

// API 地址域名（role=base_url）只放行 relay 和探活接口：页面一律 404，账号接口一律 403。
// 站长给客户的「API 地址」打开就该只是个 API，不是站长的网站。
func TestCustomDomainApiRoleServesOnlyRelay(t *testing.T) {
	f := newSiteFixture(t)
	savedCustom, savedApi := service.AdtyLoadCustomDomains, service.AdtyLoadApiOnlyDomains
	service.AdtyLoadCustomDomains = func() (map[string]int, error) {
		return map[string]int{"shop.customer-brand.com": f.site.Id, "api.customer-brand.com": f.site.Id}, nil
	}
	service.AdtyLoadApiOnlyDomains = func() (map[string]bool, error) {
		return map[string]bool{"api.customer-brand.com": true}, nil
	}
	t.Cleanup(func() {
		service.AdtyLoadCustomDomains, service.AdtyLoadApiOnlyDomains = savedCustom, savedApi
		service.InvalidateAdtySiteCache()
	})
	service.InvalidateAdtySiteCache()

	// 站点域名照常出页面。
	assert.Equal(t, http.StatusOK, f.do(t, http.MethodGet, "shop.customer-brand.com", "/", "").Code)
	defaultAPI := fmt.Sprintf("api-s%d.sites.example.com", f.site.Id)
	for _, web := range []string{"shop.customer-brand.com", "shop.sites.example.com"} {
		for _, path := range []string{"/v1/models", "/v1/chat/completions", "/fast/mj/submit/imagine", "/v1/tasks"} {
			assert.Equal(t, http.StatusForbidden, f.do(t, http.MethodPost, web, path, "{}").Code, web+path)
		}
	}
	assert.Equal(t, http.StatusNotFound, f.do(t, http.MethodGet, defaultAPI, "/", "").Code)
	assert.Equal(t, http.StatusForbidden, f.do(t, http.MethodGet, defaultAPI, "/api/user/self", "").Code)
	assert.Equal(t, http.StatusUnauthorized, f.do(t, http.MethodGet, defaultAPI, "/v1/models", "").Code)

	// API 地址域名：页面 404。
	for _, path := range []string{"/", "/pricing", "/docs", "/sign-in", "/wallet"} {
		assert.Equal(t, http.StatusNotFound, f.do(t, http.MethodGet, "api.customer-brand.com", path, "").Code, path)
	}

	// API 地址域名：账号接口 403。
	for _, path := range []string{"/api/user/self", "/api/user/login", "/api/adty/epay/pay", "/api/adty/site/domains"} {
		assert.Equal(t, http.StatusForbidden, f.do(t, http.MethodGet, "api.customer-brand.com", path, "").Code, path)
	}

	// API 地址域名：探活接口放行（客户端调 /v1 前会看服务在不在）。
	assert.NotEqual(t, http.StatusForbidden, f.do(t, http.MethodGet, "api.customer-brand.com", "/api/status", "").Code)
	assert.NotEqual(t, http.StatusNotFound, f.do(t, http.MethodGet, "api.customer-brand.com", "/api/status", "").Code)

	// API 地址域名：relay 必须命中真实路由（GET /v1/models 与 POST /v1/chat/completions），
	// 无 Key 时按认证契约返回 401，不能靠 SPA NoRoute 的 200 "index" 充当放行证据。
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/v1/models", ""},
		{http.MethodPost, "/v1/chat/completions", "{}"},
	} {
		resp := f.do(t, tc.method, "api.customer-brand.com", tc.path, tc.body)
		assert.Equal(t, http.StatusUnauthorized, resp.Code, "%s %s: %s", tc.method, tc.path, resp.Body.String())
		assert.NotEqual(t, "index", resp.Body.String(), "%s %s must not fall through to SPA NoRoute", tc.method, tc.path)
	}

	customer := f.user(t, "api-only-relay-user", f.site.Id)
	require.NoError(t, f.db.Create(&model.Token{
		UserId: customer.Id, Key: "apionlymodelskey", Status: common.TokenStatusEnabled,
		ExpiredTime: -1, UnlimitedQuota: true,
	}).Error)
	modelsReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	modelsReq.Host = "api.customer-brand.com"
	modelsReq.Header.Set("Authorization", "Bearer sk-apionlymodelskey")
	modelsResp := httptest.NewRecorder()
	f.engine.ServeHTTP(modelsResp, modelsReq)
	assert.Equal(t, http.StatusOK, modelsResp.Code, modelsResp.Body.String())
	assert.NotEqual(t, "index", modelsResp.Body.String())
	assert.Contains(t, modelsResp.Body.String(), `"data"`)

	// API 地址域名仍然属于同一个站（用 relay 路径探测：页面路径在这里本来就会被 Abort）。
	var captured *model.AdtySite
	probe := gin.New()
	probe.Use(middleware.AdtySiteContext())
	probe.POST("/v1/probe", func(c *gin.Context) { captured = service.AdtySiteForRequest(c) })
	req := httptest.NewRequest(http.MethodPost, "/v1/probe", strings.NewReader("{}"))
	req.Host = "api.customer-brand.com"
	probe.ServeHTTP(httptest.NewRecorder(), req)
	require.NotNil(t, captured)
	assert.Equal(t, f.site.Id, captured.Id)
}

// /api/status 的 server_address 在分站要跟着品牌走：配了 API 地址域名时到处都显示那个
// （客户拿去调接口，跟从哪个页面复制的无关）；没配时才用访客当前所在的站点域名，
// 最后才退回平台子域名。
func TestStatusServerAddressFollowsCustomDomain(t *testing.T) {
	f := newSiteFixture(t)
	savedCustom, savedApi := service.AdtyLoadCustomDomains, service.AdtyLoadApiOnlyDomains
	t.Cleanup(func() {
		service.AdtyLoadCustomDomains, service.AdtyLoadApiOnlyDomains = savedCustom, savedApi
		service.InvalidateAdtySiteCache()
	})

	// 没有自定义域名：平台子域名照旧。
	service.InvalidateAdtySiteCache()
	platform := f.do(t, http.MethodGet, "shop.sites.example.com", "/api/status", "")
	require.Equal(t, http.StatusOK, platform.Code, platform.Body.String())
	dedicatedAddress := fmt.Sprintf(`"server_address":"https://api-s%d.sites.example.com"`, f.site.Id)
	assert.Contains(t, platform.Body.String(), dedicatedAddress)

	// 只绑了站点域名：显示访客当前这个域名，不是平台子域名。
	service.AdtyLoadCustomDomains = func() (map[string]int, error) {
		return map[string]int{"shop.customer-brand.com": f.site.Id}, nil
	}
	service.AdtyLoadApiOnlyDomains = func() (map[string]bool, error) { return map[string]bool{}, nil }
	service.InvalidateAdtySiteCache()
	custom := f.do(t, http.MethodGet, "shop.customer-brand.com", "/api/status", "")
	require.Equal(t, http.StatusOK, custom.Code, custom.Body.String())
	assert.Contains(t, custom.Body.String(), dedicatedAddress)
	assert.NotContains(t, custom.Body.String(), "shop.sites.example.com")

	// 配了 API 地址域名：不管从哪个入口打开，显示的都是它。
	service.AdtyLoadCustomDomains = func() (map[string]int, error) {
		return map[string]int{
			"shop.customer-brand.com": f.site.Id,
			"api.customer-brand.com":  f.site.Id,
		}, nil
	}
	service.AdtyLoadApiOnlyDomains = func() (map[string]bool, error) {
		return map[string]bool{"api.customer-brand.com": true}, nil
	}
	service.InvalidateAdtySiteCache()
	for _, host := range []string{"shop.sites.example.com", "shop.customer-brand.com"} {
		body := f.do(t, http.MethodGet, host, "/api/status", "").Body.String()
		assert.Contains(t, body, `"server_address":"https://api.customer-brand.com"`, host)
	}
}

func TestNewSiteFixtureRestoresLegalSiteAgreementVersion(t *testing.T) {
	legal := adty_setting.GetLegalSetting()
	original := *legal
	t.Cleanup(func() { *legal = original })

	for _, initial := range []int{0, 5} {
		legal.SiteAgreementVersion = initial
		t.Run(fmt.Sprintf("initial_%d", initial), func(t *testing.T) {
			f := newSiteFixture(t)
			require.NotNil(t, f)
			assert.Equal(t, 1, adty_setting.GetLegalSetting().SiteAgreementVersion)
		})
		assert.Equal(t, initial, adty_setting.GetLegalSetting().SiteAgreementVersion, "newSiteFixture cleanup must restore SiteAgreementVersion")
	}
}

func TestSubsiteFirstDocumentAndSupport(t *testing.T) {
	f := newSiteFixture(t)
	page, err := os.ReadFile("../web/index.html")
	require.NoError(t, err)
	probe := gin.New()
	probe.Use(middleware.AdtySiteContext())
	SetWebRouter(probe, WebAssets{IndexPage: page}, func(c *gin.Context) { c.Next() })
	for _, path := range []string{"/", "/sign-in", "/index.html", "/team-register/demo", "/wallet", "/404"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "shop.sites.example.com"
		out := httptest.NewRecorder()
		probe.ServeHTTP(out, req)
		assert.Equal(t, 200, out.Code, path)
		assert.Contains(t, out.Body.String(), "Shop AI", path)
		assert.Contains(t, out.Body.String(), "data-adty-site-id=", path)
		assert.NotContains(t, out.Body.String(), "<title>ADTY AI", path)
		assert.NotContains(t, out.Body.String(), `href="/adty-apple-touch-icon.png"`, path)
		assert.Contains(t, out.Header().Get("Cache-Control"), "no-store", path)
	}
	req := httptest.NewRequest("GET", "/site.webmanifest", nil)
	req.Host = "shop.sites.example.com"
	manifest := httptest.NewRecorder()
	probe.ServeHTTP(manifest, req)
	assert.Equal(t, http.StatusNotFound, manifest.Code)
	req = httptest.NewRequest("GET", "/mj/missing", nil)
	req.Host = fmt.Sprintf("api-s%d.sites.example.com", f.site.Id)
	unknown := httptest.NewRecorder()
	probe.ServeHTTP(unknown, req)
	assert.Equal(t, http.StatusNotFound, unknown.Code)
	assert.NotContains(t, unknown.Body.String(), "<html")
	cfg := adty_setting.GetSiteSetting()
	old := *cfg
	t.Cleanup(func() { *cfg = old })
	cfg.SupportEmail, cfg.TawkPropertyID = "root@example.test", "main-only-widget"
	require.NoError(t, f.db.Model(f.site).Update("brand", `{"support_contact":"WeChat: shop-support"}`).Error)
	service.InvalidateAdtySiteCache()
	public := f.do(t, "GET", "shop.sites.example.com", "/api/adty/site", "").Body.String()
	assert.Contains(t, public, `"support_contact":"WeChat: shop-support"`)
	assert.NotContains(t, f.do(t, "GET", "main.example.com", "/api/adty/site", "").Body.String(), "shop-support")
	assert.NotContains(t, public, "root@example.test")
	assert.NotContains(t, public, "main-only-widget")
}
