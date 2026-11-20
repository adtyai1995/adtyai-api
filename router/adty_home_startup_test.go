package router

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestHomeSetupHintRequiresPersistedCompletionPG(t *testing.T) {
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Setup{}, &model.Option{}))
	previous := constant.Setup
	constant.Setup = true // A stale memory flag must not bypass recovery.
	t.Cleanup(func() { constant.Setup = previous })
	check := func(want bool) {
		t.Helper()
		response := newSEOFixture().serve(t, "adtyai.com", "/")
		require.Equal(t, want, strings.Contains(response.Body.String(), `<meta name="adty-setup-complete" content="true">`))
	}
	check(false)
	require.NoError(t, db.Create(&model.User{Username: "root", Role: common.RoleRootUser}).Error)
	require.NoError(t, db.Create(&model.Setup{Version: "test"}).Error)
	check(false)
	require.NoError(t, db.Create(&model.Option{Key: "SelfUseModeEnabled", Value: "false"}).Error)
	check(false)
	require.NoError(t, db.Create(&model.Option{Key: "DemoSiteEnabled", Value: "false"}).Error)
	check(true)
	require.NoError(t, db.Where("role = ?", common.RoleRootUser).Delete(&model.User{}).Error)
	check(false)
}

func TestSubsiteHomePrerenderPG(t *testing.T) {
	oldAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://main.example.com"
	t.Cleanup(func() { system_setting.ServerAddress = oldAddress })
	db := adtyTestDB(t)
	var version string
	require.NoError(t, db.Raw("SHOW server_version_num").Scan(&version).Error)
	t.Logf("PostgreSQL server_version_num=%s", version)
	require.NoError(t, db.AutoMigrate(&model.AdtySite{}, &model.AdtySiteDomain{}, &model.User{}))
	service.InvalidateAdtySiteCache()
	t.Cleanup(service.InvalidateAdtySiteCache)
	sites := []model.AdtySite{
		{Subdomain: "alpha", RootDomain: "sites.example.com", Brand: `{"name":"Alpha <script> & __ADTY_SITE_API__","logo_url":"/uploads/site-assets/a.png?x=1&y=2"}`},
		{Subdomain: "beta", RootDomain: "sites.example.com", Brand: `{"name":"Beta"}`},
	}
	for i := range sites {
		require.NoError(t, db.Create(&sites[i]).Error)
	}
	files := fstest.MapFS{
		"web/dist/adty-home.json":      {Data: []byte(seoPrerender)},
		"web/dist/adty-site-home.json": {Data: []byte(`{"en":"<header>__ADTY_SITE_BRAND__</header><h1>EN hero</h1><code>__ADTY_SITE_API__</code>","zh-CN":"<header>__ADTY_SITE_BRAND__</header><h1>中文首屏</h1><code>__ADTY_SITE_API__</code>"}`)},
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		if c.Request.Host == "alpha.sites.example.com" {
			c.Set(service.AdtySiteContextKey, &sites[0])
		} else {
			c.Set(service.AdtySiteContextKey, &sites[1])
		}
	})
	engine.NoRoute(AdtySEOHomePages([]byte(seoIndexPage), files))
	for _, site := range sites {
		for _, path := range []string{"/", "/zh-cn"} {
			t.Run(site.Subdomain+path, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Host = site.Subdomain + ".sites.example.com"
				rec := httptest.NewRecorder()
				engine.ServeHTTP(rec, req)
				body := rec.Body.String()
				require.Equal(t, http.StatusOK, rec.Code)
				require.Contains(t, body, "<header>")
				hero := "EN hero"
				if path == "/zh-cn" {
					hero = "中文首屏"
				}
				require.Contains(t, body, "<h1>"+hero+"</h1>")
				require.Contains(t, body, html.EscapeString(service.AdtyBrandFor(&site).Name))
				require.Contains(t, body, fmt.Sprintf("<code>https://api-s%d.sites.example.com</code>", site.Id))
				require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
				require.Equal(t, "noindex", rec.Header().Get("X-Robots-Tag"))
				require.NotContains(t, body, "<script>")
				require.NotContains(t, body, "__ADTY_SITE_BRAND__")
				require.NotContains(t, body, "ADTY AI")
				if site.Subdomain == "alpha" {
					require.Contains(t, body, `src="/uploads/site-assets/a.png?x=1&amp;y=2"`)
					require.Contains(t, body, "__ADTY_SITE_API__", "replacement values must not be recursively expanded")
				} else {
					require.NotContains(t, body, "Alpha")
					require.NotContains(t, body, "<img")
				}
			})
		}
	}
	// Older builds without a site template retain the escaped-name fallback.
	delete(files, "web/dist/adty-site-home.json")
	engine = gin.New()
	engine.Use(func(c *gin.Context) { c.Set(service.AdtySiteContextKey, &sites[0]) })
	engine.NoRoute(AdtySEOHomePages([]byte(seoIndexPage), files))
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Contains(t, rec.Body.String(), "<main><h1>"+html.EscapeString(service.AdtyBrandFor(&sites[0]).Name)+"</h1></main>")
}
