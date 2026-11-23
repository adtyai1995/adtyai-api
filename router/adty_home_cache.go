package router

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// Called only for the main site's GET/HEAD home document, after rendering it.
func serveAdtyMainHome(c *gin.Context, base *url.URL, cacheEnabled, completeHome bool, out []byte) {
	req := c.Request
	uri := req.URL.RequestURI() // Preserves escapes, trailing slashes and a bare '?'.
	switch {
	case !cacheEnabled:
		c.Header("Cache-Control", "no-cache")
	case completeHome && base != nil && strings.EqualFold(req.Host, base.Host) &&
		(uri == "/" || uri == "/zh-cn") && len(req.Header.Values("Cookie")) == 0 &&
		len(req.Header.Values("Authorization")) == 0 && len(c.Writer.Header().Values("Set-Cookie")) == 0:
		c.Header("Cache-Control", "public, max-age=0, s-maxage=60, must-revalidate")
		c.Header("Vary", "Accept-Encoding, Cookie, Authorization")
		etag := common.ETagFor("adty-main-home", string(out))
		c.Header("ETag", etag)
		if common.ETagMatches(req.Header.Get("If-None-Match"), etag) {
			c.Status(http.StatusNotModified)
			return
		}
	default:
		c.Header("Cache-Control", "private, no-store")
	}
	// net/http suppresses the body on HEAD after the normal representation
	// headers (including those from the gzip middleware) have been generated.
	c.Data(http.StatusOK, "text/html; charset=utf-8", out)
}
