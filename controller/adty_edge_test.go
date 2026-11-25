package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAdtyEdgeManifestEndpointAuthETagAndSiteContextBypass(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CRYPTO_SECRET", "0123456789abcdef0123456789abcdef")

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.AdtySite{},
		&model.AdtySiteDomain{},
		&model.AdtyEdgeCert{},
		&model.AdtyEdgeACMEAccount{},
		&model.AdtyEdgeState{},
	))
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })

	rsPtr, ok := config.GlobalConfig.Get(adty_setting.ResellerSettingModule).(*adty_setting.ResellerSetting)
	require.True(t, ok && rsPtr != nil)
	common.OptionMapRWMutex.Lock()
	oldRS := *rsPtr
	nextRS := adty_setting.DefaultResellerSetting()
	nextRS.Enabled = true
	nextRS.EdgeMainlandEnabled = true
	*rsPtr = nextRS
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		*rsPtr = oldRS
		common.OptionMapRWMutex.Unlock()
	})

	engine := gin.New()
	engine.Use(middleware.AdtySiteContext())
	engine.GET("/api/adty/internal/edge/manifest", GetAdtyEdgeManifest)

	// 1. Missing ADTY_EDGE_TOKEN -> 403 even on fallback origin host.
	t.Setenv("ADTY_EDGE_TOKEN", "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/adty/internal/edge/manifest", nil)
	req.Host = "origin.adtyaicf.top"
	engine.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// 2. Wrong token -> 403.
	t.Setenv("ADTY_EDGE_TOKEN", "secret-edge-token-123456")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/adty/internal/edge/manifest", nil)
	req.Host = "origin.adtyaicf.top"
	req.Header.Set("X-Adty-Edge-Token", "wrong-token")
	engine.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// 3. Valid token -> 200 with ETag, Cache-Control, and recorded pull state.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/adty/internal/edge/manifest", nil)
	req.Host = "origin.adtyaicf.top"
	req.Header.Set("X-Adty-Edge-Token", "secret-edge-token-123456")
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	etag := rec.Header().Get("ETag")
	require.NotEmpty(t, etag)

	var manifest service.AdtyEdgeManifest
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &manifest))
	assert.True(t, manifest.EdgeMainlandEnabled)
	assert.Equal(t, `"`+manifest.Version+`"`, etag)

	pull, err := service.GetAdtyEdgeManifestPullState()
	require.NoError(t, err)
	require.NotNil(t, pull)
	assert.Equal(t, manifest.Version, pull.Version)
	assert.Greater(t, pull.PulledAt, int64(0))

	// 4. Matching If-None-Match from a second edge node IP -> 304 Not Modified and per-node pull map tracks both IPs.
	rec304 := httptest.NewRecorder()
	req304 := httptest.NewRequest(http.MethodGet, "/api/adty/internal/edge/manifest", nil)
	req304.Host = "origin.adtyaicf.top"
	req304.RemoteAddr = "47.242.10.21:41234"
	req304.Header.Set("X-Adty-Edge-Token", "secret-edge-token-123456")
	req304.Header.Set("If-None-Match", etag)
	engine.ServeHTTP(rec304, req304)
	assert.Equal(t, http.StatusNotModified, rec304.Code)
	assert.Empty(t, rec304.Body.Bytes())

	pullAfter, err := service.GetAdtyEdgeManifestPullState()
	require.NoError(t, err)
	require.NotNil(t, pullAfter)
	assert.Equal(t, "47.242.10.21", pullAfter.ClientIP)
	assert.Contains(t, pullAfter.Nodes, "47.242.10.21")
	assert.GreaterOrEqual(t, len(pullAfter.Nodes), 2)
}
