package router

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAdtyDocsBrandDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AdtySite{}, &model.AdtySiteDomain{}))
	oldDB := model.DB
	model.DB = db
	service.InvalidateAdtySiteCache()
	t.Cleanup(func() {
		model.DB = oldDB
		service.InvalidateAdtySiteCache()
	})
	return db
}

func TestAdtySiteBrandDocsURLAndStatusOverride(t *testing.T) {
	db := setupAdtyDocsBrandDB(t)

	// 1. Validation: accepts empty and http(s) URLs; rejects unsafe schemes, whitespace, and >500 chars.
	validCases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{" https://docs.sub.example/guide?a=1 ", "https://docs.sub.example/guide?a=1"},
		{"http://help.sub.example", "http://help.sub.example"},
	}
	for _, tc := range validCases {
		got, err := service.NormalizeAdtySiteBrand(service.AdtySiteBrandInput{DocsURL: tc.in})
		require.NoError(t, err, "input=%q", tc.in)
		assert.Equal(t, tc.want, got.DocsURL)
	}

	invalidCases := []string{
		"javascript:alert(1)",
		"ftp://docs.sub.example",
		"https://",
		"docs.sub.example",
		"https://docs.sub.example/has space",
		"https://docs.sub.example/" + strings.Repeat("a", 500),
	}
	for _, raw := range invalidCases {
		_, err := service.NormalizeAdtySiteBrand(service.AdtySiteBrandInput{DocsURL: raw})
		require.Error(t, err, "expected rejection for %q", raw)
	}

	// 2. SaveAdtyBrandPatch persists and clears docs_url on a site.
	site := model.AdtySite{
		OwnerUserId: 901,
		Subdomain:   "s1",
		RootDomain:  "adtyai-fz.top",
		Status:      model.AdtySiteStatusActive,
	}
	require.NoError(t, db.Create(&site).Error)

	require.NoError(t, service.SaveAdtyBrandPatch(
		site.Id,
		site.OwnerUserId,
		map[string]string{"docs_url": "https://help.s1.example/"},
		map[string]string{"docs_url": ""},
		nil,
	))
	var reloaded model.AdtySite
	require.NoError(t, db.First(&reloaded, site.Id).Error)
	assert.Equal(t, "https://help.s1.example/", service.AdtyBrandFor(&reloaded).DocsURL)

	// 3. AdtyOverrideStatus exposes subsite docs_url, never leaks main site docs_link, and preserves docs_static_mounted.
	gin.SetMode(gin.TestMode)
	ctxWithCustom, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctxWithCustom.Set(service.AdtySiteContextKey, &reloaded)
	statusCustom := map[string]any{
		"docs_link":           "https://docs.adtyai.com",
		"docs_static_mounted": true,
	}
	service.AdtyOverrideStatus(ctxWithCustom, statusCustom)
	assert.Equal(t, "https://help.s1.example/", statusCustom["docs_link"])
	assert.Equal(t, true, statusCustom["docs_static_mounted"])

	// Clearing docs_url reverts subsite status.docs_link to "" (not main site's docs_link).
	require.NoError(t, service.SaveAdtyBrandPatch(
		site.Id,
		site.OwnerUserId,
		map[string]string{"docs_url": ""},
		map[string]string{"docs_url": "https://help.s1.example/"},
		nil,
	))
	require.NoError(t, db.First(&reloaded, site.Id).Error)
	ctxEmpty, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctxEmpty.Set(service.AdtySiteContextKey, &reloaded)
	statusEmpty := map[string]any{
		"docs_link":           "https://docs.adtyai.com",
		"docs_static_mounted": true,
	}
	service.AdtyOverrideStatus(ctxEmpty, statusEmpty)
	assert.Equal(t, "", statusEmpty["docs_link"])
	assert.Equal(t, true, statusEmpty["docs_static_mounted"])
}

func TestAdtyStaticDocsServingAndSubsiteOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = setupAdtyDocsBrandDB(t)

	parentDir := t.TempDir()
	docsDir := filepath.Join(parentDir, "docs")
	require.NoError(t, os.MkdirAll(filepath.Join(docsDir, "guide", "nested"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(docsDir, "assets"), 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(parentDir, "secret.txt"), []byte("TOP_SECRET"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(docsDir, "index.html"), []byte("<html><title>Docs Root</title></html>"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(docsDir, "404.html"), []byte("<html>Custom 404</html>"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(docsDir, "guide", "quickstart.html"), []byte("<html>Quickstart</html>"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(docsDir, "guide", "nested", "index.html"), []byte("<html>Nested</html>"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(docsDir, "assets", "app.hash.js"), []byte("console.log('ok')"), 0o644))
	// Best-effort symlink outside docsDir to verify os.OpenRoot containment.
	_ = os.Symlink(filepath.Join(parentDir, "secret.txt"), filepath.Join(docsDir, "escape.txt"))

	t.Setenv("ADTY_DOCS_DIR", docsDir)
	assert.True(t, service.AdtyStaticDocsMounted())

	buildRouter := func(site *model.AdtySite) *gin.Engine {
		r := gin.New()
		r.NoRoute(func(c *gin.Context) {
			if site != nil {
				c.Set(service.AdtySiteContextKey, site)
			}
			c.Next()
		}, AdtyStaticDocs(), func(c *gin.Context) {
			c.String(http.StatusOK, "SPA_FALLBACK")
		})
		return r
	}

	// Main site requests.
	mainRouter := buildRouter(nil)

	// /docs -> 301 /docs/ preserving query string.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://www.adtyai.com/docs?lang=zh", nil)
	mainRouter.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/docs/?lang=zh", rec.Header().Get("Location"))

	// /docs/ -> 200 index.html with must-revalidate.
	rec = httptest.NewRecorder()
	mainRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://www.adtyai.com/docs/", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Docs Root")
	assert.Equal(t, "public, max-age=0, must-revalidate", rec.Header().Get("Cache-Control"))

	// HEAD /docs/ -> 200 empty body.
	rec = httptest.NewRecorder()
	mainRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "https://www.adtyai.com/docs/", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Body.String())

	// VitePress cleanUrls: /docs/guide/quickstart -> guide/quickstart.html.
	rec = httptest.NewRecorder()
	mainRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://www.adtyai.com/docs/guide/quickstart", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Quickstart")

	// Directory without trailing slash redirects to trailing slash; with trailing slash serves index.html.
	rec = httptest.NewRecorder()
	mainRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://www.adtyai.com/docs/guide/nested", nil))
	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/docs/guide/nested/", rec.Header().Get("Location"))

	rec = httptest.NewRecorder()
	mainRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://www.adtyai.com/docs/guide/nested/", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Nested")

	// Hashed asset under /docs/assets/ gets immutable long-term cache.
	rec = httptest.NewRecorder()
	mainRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://www.adtyai.com/docs/assets/app.hash.js", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "public, max-age=2592000, immutable", rec.Header().Get("Cache-Control"))
	assert.Contains(t, rec.Body.String(), "console.log('ok')")

	// Missing path returns 404 with custom 404.html.
	rec = httptest.NewRecorder()
	mainRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://www.adtyai.com/docs/not-found", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "Custom 404")

	// Path traversal and symlink escape are rejected and never return secret.txt.
	for _, attackPath := range []string{"/docs/../secret.txt", "/docs/guide/../../secret.txt", "/docs/escape.txt"} {
		rec = httptest.NewRecorder()
		mainRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://www.adtyai.com"+attackPath, nil))
		assert.Equal(t, http.StatusNotFound, rec.Code, "path=%s", attackPath)
		assert.NotContains(t, rec.Body.String(), "TOP_SECRET", "path=%s", attackPath)
	}

	// Subsite without custom docs_url and seo_index=false gets static docs + X-Robots-Tag: noindex.
	subNoSEO := &model.AdtySite{Id: 7, Subdomain: "s7", RootDomain: "adtyai-fz.top", Status: model.AdtySiteStatusActive, SeoIndex: false}
	rec = httptest.NewRecorder()
	buildRouter(subNoSEO).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://s7.adtyai-fz.top/docs/", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "noindex", rec.Header().Get("X-Robots-Tag"))

	// Subsite with custom external docs_url redirects /docs and /docs/ to the external URL.
	rawCustomBrand, err := common.Marshal(service.AdtySiteBrandInput{Name: "Sub7", DocsURL: "https://help.sub7.example/start"})
	require.NoError(t, err)
	subCustom := &model.AdtySite{Id: 8, Subdomain: "s8", RootDomain: "adtyai-fz.top", Status: model.AdtySiteStatusActive, Brand: string(rawCustomBrand)}
	for _, p := range []string{"/docs", "/docs/"} {
		rec = httptest.NewRecorder()
		buildRouter(subCustom).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://s8.adtyai-fz.top"+p, nil))
		assert.Equal(t, http.StatusFound, rec.Code, "path=%s", p)
		assert.Equal(t, "https://help.sub7.example/start", rec.Header().Get("Location"))
		assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	}

	// Self-redirect loop guard: subsite pointing docs_url at its own /docs/ still renders static docs cleanly.
	rawLoopBrand, err := common.Marshal(service.AdtySiteBrandInput{Name: "LoopSite", DocsURL: "https://s9.adtyai-fz.top/docs/"})
	require.NoError(t, err)
	subLoop := &model.AdtySite{Id: 9, Subdomain: "s9", RootDomain: "adtyai-fz.top", Status: model.AdtySiteStatusActive, Brand: string(rawLoopBrand)}
	rec = httptest.NewRecorder()
	buildRouter(subLoop).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://s9.adtyai-fz.top/docs/", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Docs Root")

	// Disabling ADTY_DOCS_DIR falls through cleanly to the built-in React SPA.
	t.Setenv("ADTY_DOCS_DIR", "off")
	assert.False(t, service.AdtyStaticDocsMounted())
	rec = httptest.NewRecorder()
	mainRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://www.adtyai.com/docs", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "SPA_FALLBACK", rec.Body.String())
}

func TestAdtyStaticDocsBundledFallbackAndDisable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = setupAdtyDocsBrandDB(t)

	bundledDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bundledDir, "index.html"), []byte("<html>Bundled VitePress</html>"), 0o644))
	cleanupBundled := service.SetAdtyBundledStaticDocsDirForTest(bundledDir)
	defer cleanupBundled()

	buildRouter := func() *gin.Engine {
		r := gin.New()
		r.NoRoute(AdtyStaticDocs(), func(c *gin.Context) {
			c.String(http.StatusOK, "SPA_FALLBACK")
		})
		return r
	}

	// Default unset ADTY_DOCS_DIR falls back to bundled /app/docs when /data/docs is absent.
	t.Setenv("ADTY_DOCS_DIR", "")
	assert.True(t, service.AdtyStaticDocsMounted())
	rec := httptest.NewRecorder()
	buildRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://www.adtyai.com/docs/", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Bundled VitePress")

	// Explicit default /data/docs also falls back to bundled docs when /data/docs has no index.html.
	t.Setenv("ADTY_DOCS_DIR", "/data/docs")
	assert.True(t, service.AdtyStaticDocsMounted())
	dir, ok := service.AdtyStaticDocsRootDir()
	assert.True(t, ok)
	assert.Equal(t, bundledDir, dir)

	// Explicit non-default custom dir overrides bundled docs when ready.
	overrideDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(overrideDir, "index.html"), []byte("<html>Hot Override</html>"), 0o644))
	t.Setenv("ADTY_DOCS_DIR", overrideDir)
	assert.True(t, service.AdtyStaticDocsMounted())
	rec = httptest.NewRecorder()
	buildRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://www.adtyai.com/docs/", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Hot Override")

	// ADTY_DOCS_DIR=off disables BOTH external override and bundled fallback.
	t.Setenv("ADTY_DOCS_DIR", "off")
	assert.False(t, service.AdtyStaticDocsMounted())
	rec = httptest.NewRecorder()
	buildRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://www.adtyai.com/docs", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "SPA_FALLBACK", rec.Body.String())
}
