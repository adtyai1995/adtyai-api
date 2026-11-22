package middleware

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
)

// adtyAPIPathPrefixes identifies API requests for closed-site and custom-domain handling.
// "/pg/" is official keep-list classification of the retired Playground
// namespace, not a registered user feature. See internal/adtygov.
var adtyAPIPathPrefixes = []string{"/api/", "/v1/", "/v1beta/", "/mj/", "/pg/"}

// Dynamic plugin routes use their own Gin tree. A miss must never serve the SPA.
var AdtyTryAPIPlugin func(*gin.Context) bool

func adtyIsSessionAPI(path string) bool {
	switch path {
	case "/api/verification", "/api/reset_password", "/api/user/reset",
		"/api/user/register", "/api/user/auth/refresh", "/api/user/auth/logout":
		return true
	}
	return strings.HasPrefix(path, "/api/user/login") ||
		strings.HasPrefix(path, "/api/user/passkey/login") ||
		strings.HasPrefix(path, "/api/oauth/")
}

func adtyIsAPIPath(path string) bool {
	if generation := jsplugin.DefaultRegistry.Generation(); generation != nil {
		if _, found := generation.LookupDeclaredRoute(http.MethodGet, path); found {
			return true
		}
	}
	if path == "/api" || path == "/v1" || path == "/v1beta" {
		return true
	}
	for _, prefix := range adtyAPIPathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return strings.Contains(path, "/mj/")
}

// AdtySiteContext 按请求的 Host 识别当前分站（任务 11.2），放进 c.Set("adty_site", site)；主站或查不到时不放。
// 只认站点表（带缓存），不信任 X-Forwarded-Host 之类的头（执行层决定 28）。分站关闭时仅主站 Host 可用。
//   - active、paused、closing 的站照常识别；pending 的站还没开，当作查不到。
//   - closed 的站：页面请求 302 到客户的新归属，接口请求拒绝。
//   - 查不到的 Host：所有请求返回 404，不回退到主站。
func AdtySiteContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := adty_setting.GetResellerSetting()
		host := service.AdtyNormalizeHost(c.Request.Host)
		if service.AdtyIsMainHost(host) {
			c.Next()
			return
		}
		if !cfg.Enabled {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		path := c.Request.URL.Path
		if c.Request.Method == http.MethodGet && path == service.AdtyDomainProbePath {
			c.Header("Cache-Control", "no-store")
			identity, err := service.AdtyDomainProbeForHost(host)
			if err != nil {
				c.AbortWithStatus(http.StatusNotFound)
				return
			}
			c.AbortWithStatusJSON(http.StatusOK, identity)
			return
		}
		if adtyIsAPIPath(path) {
			c.Header("Cache-Control", "private, no-store")
		}
		if adtyIsRelayPath(path) && service.AdtySiteForHost(host) != nil && !service.AdtyIsApiOnlyHost(host) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		if service.AdtyIsCustomDomainHost(host) || service.AdtyIsApiOnlyHost(host) {
			adtyHandleCustomDomain(c, host)
			return
		}
		if adtyIsSessionAPI(c.Request.URL.Path) {
			if status := service.AdtyRejectSessionAPIStatus(host); status != 0 {
				c.AbortWithStatus(status)
				return
			}
		}
		site := service.AdtySiteForHost(host)
		api := adtyIsAPIPath(c.Request.URL.Path)
		switch {
		case service.AdtySiteServesCustomers(site):
			c.Set(service.AdtySiteContextKey, site)
		case site != nil && site.Status == model.AdtySiteStatusClosed:
			// Closed websites retain only a redirect to the main-site root.
			target := service.AdtySiteBaseURL(nil)
			if !api && target != "" && (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && c.GetHeader("Upgrade") == "" && !service.AdtyIsApiOnlyHost(host) {
				c.Header("Cache-Control", "no-store")
				c.Redirect(http.StatusFound, strings.TrimRight(target, "/")+"/")
				c.Abort()
				return
			}
			c.AbortWithStatus(http.StatusForbidden)
			return
		default:
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}

func adtyHandleCustomDomain(c *gin.Context, host string) {
	site := service.AdtySiteForHost(host)
	if site == nil {
		// 域名表里还留着这个 Host，但站点已经查不到：按未知 Host 处理，不回退到主站。
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.Set(service.AdtySiteContextKey, site)
	path := c.Request.URL.Path
	// API 地址域名（role=base_url）：只放行 relay（/v1、/v1beta、/mj、/pg）和几个探活接口，
	// 页面一律 404、账号接口一律 403。站长给客户的「API 地址」打开就只是个 API，
	// 看不到网站，也拿不到登录页。
	if service.AdtyIsApiOnlyHost(host) {
		if !service.AdtySiteServesCustomers(site) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		if adtyIsRelayPath(path) || adtyApiOnlyProbePath(path) {
			c.Next()
			return
		}
		if c.FullPath() == "" && !adtyIsAPIPath(path) && AdtyTryAPIPlugin != nil {
			c.Header("Cache-Control", "private, no-store")
			if AdtyTryAPIPlugin(c) {
				return
			}
		}
		if adtyIsAPIPath(path) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	// 站点域名（role=site）承接完整的客户端体验（登录、注册、充值、用量、令牌），
	// 只有平台级管理接口仍被拒绝。
	if adtyCustomDomainAdminAPI(path) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if !service.AdtySiteServesCustomers(site) {
		// 站点已关停、只是缓存里还留着域名行：页面跳到客户的新归属，接口一律拒绝，
		// 和平台子域名的关站处理保持一致。
		if site.Status == model.AdtySiteStatusClosed && !adtyIsAPIPath(path) && (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && c.GetHeader("Upgrade") == "" {
			if target := service.AdtySiteBaseURL(nil); target != "" {
				c.Header("Cache-Control", "no-store")
				c.Redirect(http.StatusFound, strings.TrimRight(target, "/")+"/")
				c.Abort()
				return
			}
		}
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	c.Next()
}

// AdtyRequireCurrentSiteTerms blocks account and relay use until a site customer
// accepts the current terms. Logout, self, and the accept endpoint stay open.
func AdtyRequireCurrentSiteTerms() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if path == "/api/user/self" || path == "/api/user/auth/logout" || path == "/api/adty/site/terms/accept" || path == "/api/status" {
			c.Next()
			return
		}
		userID := c.GetInt("id")
		if userID <= 0 {
			c.Next()
			return
		}
		required, _ := service.AdtySiteTermsStatus(userID)
		if required {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "Accept the current site customer terms first",
			})
			return
		}
		c.Next()
	}
}

// adtyCustomDomainAdminAPI 是自定义域名上仍然拒绝的平台级管理接口。
// 其余 /api 路径都按客户端体验放行（登录、注册、充值、用量、令牌等）。
//
// /api/oauth 下现在只剩邮箱绑定（第三方登录已删除），它在自定义域名上仍按原规则拒绝。
func adtyCustomDomainAdminAPI(path string) bool {
	prefixes := []string{
		"/api/adty/admin", "/api/channel", "/api/option", "/api/deployments",
		"/api/adty/marketing/admin", "/api/oauth",
	}
	for _, prefix := range prefixes {
		if path == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// adtyApiOnlyProbePath 是 API 地址域名上仍然放行的探活/元信息接口：客户端调 /v1 之前
// 会先看服务是否在线、有哪些模型分组，这些不带账号状态，放行没有风险。
func adtyApiOnlyProbePath(path string) bool {
	switch path {
	case "/api/status", "/api/ready", "/api/health", "/api/uptime/status", "/api/ratio_config",
		"/api/adty/groups/display", "/api/adty/site":
		return true
	}
	return strings.HasPrefix(path, "/api/adty/pricing")
}

func adtyIsRelayPath(path string) bool {
	_, rest, hasMode := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if hasMode && strings.HasPrefix(rest, "mj/") {
		return true
	}
	return strings.HasPrefix(path, "/v1/") || path == "/v1" ||
		strings.HasPrefix(path, "/v1beta/") || path == "/v1beta" ||
		strings.HasPrefix(path, "/mj/") || strings.HasPrefix(path, "/pg/")
}
