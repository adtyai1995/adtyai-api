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

func adtyAbortRelayOnWebHost(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"error": gin.H{
			"message": "Model API requests are not allowed on the website domain; use the API domain instead",
			"type":    "invalid_request_error",
			"code":    "api_domain_required",
		},
	})
}

// AdtySiteContext 按请求的 Host 识别当前主站/分站（任务 11.2），放进 c.Set("adty_site", site)；主站或查不到时不放。
// 只认站点表（带缓存），不信任 X-Forwarded-Host 之类的头（执行层决定 28）。分站关闭时仅主站 Host 可用。
// 主站与分站统一双域名规则：
//   - 网站/首页域名：承接网页与控制台，禁止调用 AI 模型中转 API（403）；
//   - API 调用域名：承接全部 AI 模型中转 API，同时也允许打开本站首页并完整登录操作控制台；
//   - active、paused、closing 的分站照常识别；pending 的站还没开，当作查不到；
//   - closed 的分站：网站页面请求 302 到主站首页，接口与 API 域名请求拒绝；
//   - 查不到的 Host：所有请求返回 404，不回退到主站。
func AdtySiteContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := adty_setting.GetResellerSetting()
		host := service.AdtyNormalizeHost(c.Request.Host)
		path := c.Request.URL.Path
		if service.AdtyIsMainHost(host) {
			if adtyIsRelayPath(path) && !service.AdtyHostAllowsRelay(host) {
				adtyAbortRelayOnWebHost(c)
				return
			}
			c.Next()
			return
		}
		if !cfg.Enabled {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
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
		if adtyIsRelayPath(path) && service.AdtySiteForHost(host) != nil && !service.AdtyHostAllowsRelay(host) {
			adtyAbortRelayOnWebHost(c)
			return
		}
		if service.AdtyIsCustomDomainHost(host) || service.AdtyIsApiOnlyHost(host) {
			adtyHandleCustomDomain(c, host)
			return
		}
		if adtyIsSessionAPI(path) {
			if status := service.AdtyRejectSessionAPIStatus(host); status != 0 {
				c.AbortWithStatus(status)
				return
			}
		}
		site := service.AdtySiteForHost(host)
		api := adtyIsAPIPath(path)
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
	isAPIHost := service.AdtyIsApiOnlyHost(host)
	isCustom := service.AdtyIsCustomDomainHost(host)
	if !service.AdtySiteServesCustomers(site) {
		// 已关停的站点域名仅保留页面 302 跳转主站；已关停的 API 域名一律 403 拒绝。
		if !isAPIHost && site.Status == model.AdtySiteStatusClosed && !adtyIsAPIPath(path) && (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && c.GetHeader("Upgrade") == "" {
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
	// 自定义域名（网站域名或自定义 API 域名）以及平台 API 域名均拒绝平台级管理接口，
	// 但承接完整的本站客户端体验（首页、登录、注册、充值、用量、令牌等）；
	// API 域名同时放行全部模型中转 API 与插件路由。
	if (isCustom && adtyCustomDomainAdminAPI(path)) || (!isCustom && adtyPlatformSubdomainAdminAPI(path)) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if adtyIsSessionAPI(path) {
		if status := service.AdtyRejectSessionAPIStatus(host); status != 0 {
			c.AbortWithStatus(status)
			return
		}
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

func adtyPlatformSubdomainAdminAPI(path string) bool {
	prefixes := []string{
		"/api/adty/admin", "/api/channel", "/api/option", "/api/deployments",
		"/api/adty/marketing/admin",
	}
	for _, prefix := range prefixes {
		if path == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// adtyCustomDomainAdminAPI 是自定义域名上仍然拒绝的平台级管理接口。
// 其余 /api 路径都按客户端体验放行（登录、注册、充值、用量、令牌等）。
//
// /api/oauth 下现在只剩邮箱绑定（第三方登录已删除），它在自定义域名上仍按原规则拒绝。
func adtyCustomDomainAdminAPI(path string) bool {
	if adtyPlatformSubdomainAdminAPI(path) {
		return true
	}
	return path == "/api/oauth" || strings.HasPrefix(path, "/api/oauth/")
}

func adtyIsRelayPath(path string) bool {
	_, rest, hasMode := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if hasMode && strings.HasPrefix(rest, "mj/") {
		return true
	}
	return strings.HasPrefix(path, "/v1/") || path == "/v1" ||
		strings.HasPrefix(path, "/v1beta/") || path == "/v1beta" ||
		strings.HasPrefix(path, "/mj/") || strings.HasPrefix(path, "/pg/") ||
		strings.HasPrefix(path, "/dashboard/billing/")
}
