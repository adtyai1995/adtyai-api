package middleware

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// SessionCookieOriginGuard protects cookie-authenticated refresh/logout
// endpoints when secure cookie mode is enabled. In insecure local development
// mode it preserves the legacy behavior and intentionally performs no Origin
// validation. It never adds CORS response headers and must not be installed on
// relay routes.
func SessionCookieOriginGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !common.SessionCookieSecure {
			c.Next()
			return
		}
		origin, ok := requestBrowserOrigin(c.Request)
		if !ok || !isAllowedSessionOrigin(c, origin) {
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Request origin is not allowed"), gin.H{"code": "AUTH_ORIGIN_FORBIDDEN"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func requestBrowserOrigin(request *http.Request) (string, bool) {
	originValues := request.Header.Values("Origin")
	if len(originValues) > 1 {
		return "", false
	}
	if len(originValues) == 1 {
		if strings.Contains(originValues[0], ",") {
			return "", false
		}
		origin, err := common.NormalizeOrigin(originValues[0])
		return origin, err == nil
	}
	refererValues := request.Header.Values("Referer")
	if len(refererValues) != 1 {
		return "", false
	}
	referer, err := url.Parse(strings.TrimSpace(refererValues[0]))
	if err != nil || referer.Scheme == "" || referer.Host == "" || referer.User != nil {
		return "", false
	}
	origin, err := common.NormalizeOrigin(referer.Scheme + "://" + referer.Host)
	return origin, err == nil
}

func isAllowedSessionOrigin(c *gin.Context, origin string) bool {
	request := c.Request
	requestScheme, ok := common.RequestScheme(request)
	if !ok {
		return false
	}
	requestOrigin, err := common.NormalizeOrigin(requestScheme + "://" + request.Host)
	if err == nil && subtle.ConstantTimeCompare([]byte(origin), []byte(requestOrigin)) == 1 {
		return true
	}
	for _, trustedOrigin := range common.SessionCookieTrustedURLs {
		if subtle.ConstantTimeCompare([]byte(origin), []byte(trustedOrigin)) == 1 {
			return true
		}
	}
	// R-32 (#26): Behind TLS-terminating reverse proxies (e.g. aaPanel Nginx)
	// where TRUSTED_PROXIES is unset or does not match the proxy IP, RequestScheme
	// falls back to "http" while the browser sends Origin: "https://<host>".
	// Main site falls back to SESSION_COOKIE_TRUSTED_URL, whereas dynamic subsite
	// subdomains and custom domains are not listed there and would 403 at the first
	// 15-minute access-token refresh. If the Host has already been authenticated as
	// an active serving subsite (or main host) via trusted site resolution, accept
	// exact-host https://<request.Host> as a same-origin session request.
	if host := strings.TrimSpace(request.Host); host != "" {
		normalizedHost := service.AdtyNormalizeHost(host)
		isKnownHost := service.AdtyIsMainHost(normalizedHost)
		if !isKnownHost {
			if site := service.AdtySiteForRequest(c); site != nil && service.AdtySiteServesCustomers(site) {
				isKnownHost = true
			}
		}
		if isKnownHost {
			httpsOrigin, normErr := common.NormalizeOrigin("https://" + host)
			if normErr == nil && subtle.ConstantTimeCompare([]byte(origin), []byte(httpsOrigin)) == 1 {
				return true
			}
		}
	}
	return false
}
