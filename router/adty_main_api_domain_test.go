package router

import (
	"embed"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdtyMainAPIDomainServesHomeAndRelayWhileWebDomainRejectsRelay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = setupAdtyDocsBrandDB(t)

	prevServerAddress := system_setting.GetServerAddress()
	prevSiteSetting := *adty_setting.GetSiteSetting()
	prevReseller := *adty_setting.GetResellerSetting()

	system_setting.ServerAddress = "https://www.adtyai.test"
	require.NoError(t, config.UpdateConfigFromMap(config.GlobalConfig.Get("adty_site_setting"), map[string]string{
		"api_base_url": "https://api.adtyai.test",
	}))
	require.NoError(t, config.UpdateConfigFromMap(config.GlobalConfig.Get("adty_reseller_setting"), map[string]string{
		"enabled": "true",
	}))
	service.InvalidateAdtySiteCache()
	t.Cleanup(func() {
		system_setting.ServerAddress = prevServerAddress
		_ = config.UpdateConfigFromMap(config.GlobalConfig.Get("adty_site_setting"), map[string]string{
			"api_base_url": prevSiteSetting.APIBaseURL,
		})
		resellerEnabled := "false"
		if prevReseller.Enabled {
			resellerEnabled = "true"
		}
		_ = config.UpdateConfigFromMap(config.GlobalConfig.Get("adty_reseller_setting"), map[string]string{
			"enabled": resellerEnabled,
		})
		service.InvalidateAdtySiteCache()
	})

	docsDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(docsDir, "index.html"), []byte("<!doctype html><html><head><title>Docs</title></head><body><h1>ADTY Docs</h1></body></html>"), 0o644))
	t.Setenv("ADTY_DOCS_DIR", docsDir)

	indexHTML := []byte(`<!doctype html><html lang="en"><head><!--adty-head--><title>Default</title><!--/adty-head--></head><body><div id="root"></div></body></html>`)
	engine := gin.New()
	engine.Use(middleware.AdtySiteContext())
	engine.GET("/v1/models", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	engine.GET("/api/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	SetWebRouter(engine, WebAssets{BuildFS: embed.FS{}, IndexPage: indexHTML}, func(c *gin.Context) {})

	doReq := func(host, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "https://"+host+path, nil)
		req.Host = host
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	// 1. Main API domain serves homepage (/ and /zh-cn), /docs/, /api/status, AND /v1/models.
	apiHome := doReq("api.adtyai.test", "/")
	assert.Equal(t, http.StatusOK, apiHome.Code)
	assert.Contains(t, apiHome.Body.String(), `<div id="root"></div>`)
	assert.Equal(t, "noindex", apiHome.Header().Get("X-Robots-Tag"))

	apiZhHome := doReq("api.adtyai.test", "/zh-cn")
	assert.Equal(t, http.StatusOK, apiZhHome.Code)
	assert.Equal(t, "noindex", apiZhHome.Header().Get("X-Robots-Tag"))

	apiDocs := doReq("api.adtyai.test", "/docs/")
	assert.Equal(t, http.StatusOK, apiDocs.Code)
	assert.Contains(t, apiDocs.Body.String(), "ADTY Docs")
	assert.Equal(t, "noindex", apiDocs.Header().Get("X-Robots-Tag"))

	apiStatus := doReq("api.adtyai.test", "/api/status")
	assert.Equal(t, http.StatusOK, apiStatus.Code)

	apiRelay := doReq("api.adtyai.test", "/v1/models")
	assert.Equal(t, http.StatusOK, apiRelay.Code)

	// 2. Main website domain serves homepage and docs, and rejects /v1/models with 403 api_domain_required.
	webHome := doReq("www.adtyai.test", "/")
	assert.Equal(t, http.StatusOK, webHome.Code)
	assert.Equal(t, "index, follow", webHome.Header().Get("X-Robots-Tag"))

	webDocs := doReq("www.adtyai.test", "/docs/")
	assert.Equal(t, http.StatusOK, webDocs.Code)

	webRelay := doReq("www.adtyai.test", "/v1/models")
	assert.Equal(t, http.StatusForbidden, webRelay.Code)
	assert.Contains(t, webRelay.Body.String(), "api_domain_required")

	// 3. Unknown host is still rejected with 404.
	unknown := doReq("unknown.adtyai.test", "/")
	assert.Equal(t, http.StatusNotFound, unknown.Code)
}
