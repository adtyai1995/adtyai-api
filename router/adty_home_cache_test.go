package router

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdtyMainHomeCache(t *testing.T) {
	base, err := url.Parse("https://main.example")
	require.NoError(t, err)
	const public = "public, max-age=0, s-maxage=60, must-revalidate"
	const private = "private, no-store"
	for _, tc := range []struct {
		name, url, requestHeader        string
		setCookie, incomplete, disabled bool
		want                            string
	}{
		{name: "English", url: "https://main.example/", want: public},
		{name: "Chinese", url: "https://main.example/zh-cn", want: public},
		{name: "opt in required", url: "https://main.example/", disabled: true, want: "no-cache"},
		{name: "incomplete default home", url: "https://main.example/", incomplete: true, want: private},
		{name: "cookie header", url: "https://main.example/", requestHeader: "Cookie", want: private},
		{name: "authorization header", url: "https://main.example/", requestHeader: "Authorization", want: private},
		{name: "response sets a cookie", url: "https://main.example/", setCookie: true, want: private},
		{name: "custom authority", url: "https://main.example:8443/", want: private},
		{name: "alias host", url: "https://alias.example/", want: private},
		{name: "query", url: "https://main.example/?via=invite", want: private},
		{name: "empty query", url: "https://main.example/?", want: private},
		{name: "trailing slash", url: "https://main.example/zh-cn/", want: private},
		{name: "escaped path", url: "https://main.example/zh-%63n", want: private},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, tc.url, nil)
			if tc.requestHeader != "" {
				c.Request.Header.Set(tc.requestHeader, "") // Header presence alone must bypass lookup.
			}
			if tc.setCookie {
				w.Header().Add("Set-Cookie", "session=example; HttpOnly")
			}
			serveAdtyMainHome(c, base, !tc.disabled, !tc.incomplete, []byte("home"))
			assert.Equal(t, tc.want, w.Header().Get("Cache-Control"))
			assert.Equal(t, "home", w.Body.String())
			if tc.want == public {
				vary := strings.Join(w.Header().Values("Vary"), ", ")
				for _, header := range []string{"Accept-Encoding", "Authorization", "Cookie"} {
					assert.Contains(t, vary, header)
				}
			} else {
				assert.Empty(t, w.Header().Get("ETag"))
			}
		})
	}
}

func TestAdtyMainHomeRevalidation(t *testing.T) {
	base, err := url.Parse("https://main.example")
	require.NoError(t, err)
	engine := gin.New()
	engine.NoRoute(gzip.Gzip(gzip.DefaultCompression), func(c *gin.Context) {
		serveAdtyMainHome(c, base, true, true, []byte(c.GetHeader("X-Test-Content")))
		c.Abort()
	})
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)
	serve := func(method, ifNoneMatch, cookie, html, encoding string) (*http.Response, []byte) {
		req, err := http.NewRequest(method, server.URL+"/", nil)
		require.NoError(t, err)
		req.Host = "main.example"
		req.Header.Set("If-None-Match", ifNoneMatch)
		req.Header.Set("Accept-Encoding", encoding)
		req.Header.Set("X-Test-Content", html)
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		resp, err := server.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp, body
	}
	get, body := serve(http.MethodGet, "", "", "first", "identity")
	assert.Equal(t, "first", string(body))
	etag := get.Header.Get("ETag")
	require.NotEmpty(t, etag)
	for _, encoding := range []string{"identity", "gzip"} {
		head, body := serve(http.MethodHead, "", "", "first", encoding)
		assert.Equal(t, http.StatusOK, head.StatusCode)
		assert.Empty(t, body)
		assert.Equal(t, etag, head.Header.Get("ETag"))
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			same, body := serve(method, etag, "", "first", encoding)
			assert.Equal(t, http.StatusNotModified, same.StatusCode)
			assert.Empty(t, body)
			assert.Equal(t, get.Header.Get("Cache-Control"), same.Header.Get("Cache-Control"))
		}
	}
	updated, body := serve(http.MethodGet, etag, "", "updated", "identity")
	assert.Equal(t, http.StatusOK, updated.StatusCode)
	assert.Equal(t, "updated", string(body))
	assert.NotEqual(t, etag, updated.Header.Get("ETag"))
	signedIn, _ := serve(http.MethodGet, etag, "session=example", "first", "identity")
	assert.Equal(t, http.StatusOK, signedIn.StatusCode)
	assert.Equal(t, "private, no-store", signedIn.Header.Get("Cache-Control"))
}
