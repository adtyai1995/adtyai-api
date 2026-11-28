package router

import (
	"bytes"
	"embed"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestR49SubsitePartnerPagesAndAPIReturn404WhileMainSiteServes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupAdtyDocsBrandDB(t)

	prevServerAddress := system_setting.GetServerAddress()
	prevReseller := *adty_setting.GetResellerSetting()

	system_setting.ServerAddress = "https://www.adtyai.test"
	require.NoError(t, config.UpdateConfigFromMap(config.GlobalConfig.Get("adty_reseller_setting"), map[string]string{
		"enabled":      "true",
		"root_domains": `["sub.adtyai.test"]`,
	}))
	partnerCode := "PTNRISO01"
	require.NoError(t, db.Create(&model.AdtySite{
		OwnerUserId: 101,
		Subdomain:   "alpha",
		RootDomain:  "sub.adtyai.test",
		Status:      model.AdtySiteStatusActive,
		PartnerCode: &partnerCode,
	}).Error)
	service.InvalidateAdtySiteCache()
	t.Cleanup(func() {
		system_setting.ServerAddress = prevServerAddress
		resellerEnabled := "false"
		if prevReseller.Enabled {
			resellerEnabled = "true"
		}
		_ = config.UpdateConfigFromMap(config.GlobalConfig.Get("adty_reseller_setting"), map[string]string{
			"enabled": resellerEnabled,
		})
		service.InvalidateAdtySiteCache()
	})

	indexHTML := []byte(`<!doctype html><html lang="en"><head><!--adty-head--><title>Default</title><!--/adty-head--></head><body><div id="root"></div></body></html>`)
	engine := gin.New()
	engine.Use(middleware.AdtySiteContext())
	engine.GET("/api/adty/partner/public", controller.GetAdtyPartnerPublic)
	engine.POST("/api/adty/partner/click", controller.PostAdtyPartnerClick)
	engine.POST("/api/adty/partner/apply", controller.PostAdtyPartnerApply)
	SetWebRouter(engine, WebAssets{BuildFS: embed.FS{}, IndexPage: indexHTML}, func(c *gin.Context) {})

	doReq := func(method, host, path string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "https://"+host+path, bytes.NewBufferString(body))
		req.Host = host
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	// Subsite MUST return 404 for /partner, /partner/*, and all /api/adty/partner/* endpoints.
	assert.Equal(t, http.StatusNotFound, doReq(http.MethodGet, "alpha.sub.adtyai.test", "/partner", "").Code)
	assert.Equal(t, http.StatusNotFound, doReq(http.MethodGet, "alpha.sub.adtyai.test", "/partner/apply", "").Code)
	assert.Equal(t, http.StatusNotFound, doReq(http.MethodGet, "alpha.sub.adtyai.test", "/api/adty/partner/public", "").Code)
	assert.Equal(t, http.StatusNotFound, doReq(http.MethodPost, "alpha.sub.adtyai.test", "/api/adty/partner/click", `{"code":"PTNRISO01"}`).Code)
	assert.Equal(t, http.StatusNotFound, doReq(http.MethodPost, "alpha.sub.adtyai.test", "/api/adty/partner/apply", `{"contact_name":"Bob","contact_channel":"telegram","contact_value":"@bob"}`).Code)

	// Main site MUST serve /partner SPA shell and /api/adty/partner/public cleanly.
	assert.Equal(t, http.StatusOK, doReq(http.MethodGet, "www.adtyai.test", "/partner", "").Code)
	pub := doReq(http.MethodGet, "www.adtyai.test", "/api/adty/partner/public", "")
	assert.Equal(t, http.StatusOK, pub.Code)
	assert.Contains(t, pub.Body.String(), `"default_rate_percent"`)
}
