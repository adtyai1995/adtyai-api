package router

import (
	"embed"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

// WebAssets holds the embedded dashboard frontend assets.
type WebAssets struct {
	BuildFS   embed.FS
	IndexPage []byte
}

func SetWebRouter(router *gin.Engine, assets WebAssets, pluginDispatcher gin.HandlerFunc) {
	setWebRouter(router, assets, pluginDispatcher, nil)
}

func setWebRouter(router *gin.Engine, assets WebAssets, pluginDispatcher, external gin.HandlerFunc) {
	frontendFS := common.EmbedFolder(assets.BuildFS, "web/dist")
	compress := gzip.Gzip(gzip.DefaultCompression)

	handlers := gin.HandlersChain{
		rejectRetiredFeaturePath,
		pluginDispatcher,
		func(c *gin.Context) {
			if service.AdtySiteForRequest(c) == nil {
				return
			}
			if c.Request.URL.Path == "/site.webmanifest" {
				c.Header("Cache-Control", "private, no-store")
				c.AbortWithStatus(http.StatusNotFound)
			}
		},
		middleware.RouteTag("web"),
		middleware.AccessTokenAudit(),
	}
	if external != nil {
		handlers = append(handlers, external)
	}
	router.NoRoute(append(handlers,
		func(c *gin.Context) {
			// ServeFrontendFiles sends build files already compressed.
			if !frontendFS.Exists("/", c.Request.URL.Path) {
				compress(c)
			}
		},
		middleware.GlobalWebRateLimit(frontendFS),
		middleware.Cache(),
		// Serve externally mounted white-label /docs/ static site and subsite docs_url redirects.
		AdtyStaticDocs(),
		// Serve SEO metadata and prerendered content for the two official homes.
		AdtySEOHomePages(assets.IndexPage, assets.BuildFS),
		func(c *gin.Context) {
			if site := service.AdtySiteForRequest(c); site != nil && c.Request.URL.Path == "/index.html" {
				c.Header("Cache-Control", "private, no-store")
				c.Data(http.StatusOK, "text/html; charset=utf-8", adtyBrandDocument(assets.IndexPage, site, true))
				c.Abort()
			}
		},
		middleware.ServeFrontendFiles(frontendFS),
		func(c *gin.Context) {
			if strings.HasPrefix(c.Request.RequestURI, "/v1") || strings.HasPrefix(c.Request.RequestURI, "/api") || strings.HasPrefix(c.Request.RequestURI, "/assets") {
				controller.RelayNotFound(c)
				return
			}
			if site := service.AdtySiteForRequest(c); site != nil {
				c.Header("Cache-Control", "private, no-store")
				c.Data(http.StatusOK, "text/html; charset=utf-8", adtyBrandDocument(assets.IndexPage, site, true))
				return
			}
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusOK, "text/html; charset=utf-8", assets.IndexPage)
		},
	)...)
}

// rejectRetiredFeaturePath returns JSON 404 for retired namespaces, including
// official Playground /pg paths and the retired marketing module entry.
// /marketing has no page and neither plugins nor an external frontend may take
// it over. This is not a live relay route. See internal/adtygov.
func rejectRetiredFeaturePath(c *gin.Context) {
	switch strings.TrimRight(c.Request.URL.Path, "/") {
	case "/zh-tw", "/fr", "/ja", "/ko", "/ru", "/vi":
		// DEC-028 retires these exact home paths before the SPA fallback.
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	for _, prefix := range []string{"/pg", "/marketing", "/api/adty/marketing", "/api/adty/admin/marketing", "/api/deployments"} {
		if c.Request.URL.Path == prefix || strings.HasPrefix(c.Request.URL.Path, prefix+"/") {
			controller.RelayNotFound(c)
			c.Abort()
			return
		}
	}
}
