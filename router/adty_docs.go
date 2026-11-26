package router

import (
	"io"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"strings"

	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func isSelfDocsRedirect(rawTarget, requestHost string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawTarget))
	if err != nil || parsed.Host == "" {
		return true
	}
	if service.AdtyNormalizeHost(parsed.Host) != service.AdtyNormalizeHost(requestHost) {
		return false
	}
	return parsed.Path == "/docs" || strings.HasPrefix(parsed.Path, "/docs/")
}

func appendRawQuery(target, rawQuery string) string {
	if rawQuery == "" {
		return target
	}
	return target + "?" + rawQuery
}

func openRegularInRoot(root *os.Root, rel string) (*os.File, os.FileInfo, bool) {
	if rel == "" {
		return nil, nil, false
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, nil, false
	}
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		_ = f.Close()
		return nil, nil, false
	}
	return f, info, true
}

// AdtyStaticDocs serves externally built same-domain white-label static docs
// (e.g. VitePress with base="/docs/") mounted under ADTY_DOCS_DIR, and honors
// per-subsite external docs_url overrides on /docs and /docs/.
// When no static docs directory is mounted, non-redirected requests fall
// through cleanly to the built-in React /docs SPA.
func AdtyStaticDocs() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqPath := c.Request.URL.Path
		if reqPath != "/docs" && !strings.HasPrefix(reqPath, "/docs/") {
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			if _, ok := service.AdtyStaticDocsRootDir(); ok {
				c.AbortWithStatus(http.StatusMethodNotAllowed)
			}
			return
		}

		site := service.AdtySiteForRequest(c)
		if site != nil && (reqPath == "/docs" || reqPath == "/docs/") {
			if customDocs := service.AdtyBrandFor(site).DocsURL; customDocs != "" && !isSelfDocsRedirect(customDocs, c.Request.Host) {
				c.Header("Cache-Control", "private, no-store")
				c.Redirect(http.StatusFound, customDocs)
				c.Abort()
				return
			}
		}

		dir, ok := service.AdtyStaticDocsRootDir()
		if !ok {
			return
		}

		if reqPath == "/docs" {
			c.Header("Cache-Control", "public, max-age=0, must-revalidate")
			c.Redirect(http.StatusMovedPermanently, appendRawQuery("/docs/", c.Request.URL.RawQuery))
			c.Abort()
			return
		}

		rawRel := strings.TrimPrefix(reqPath, "/docs/")
		if strings.ContainsAny(rawRel, "\\\x00") {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		for seg := range strings.SplitSeq(rawRel, "/") {
			if seg == ".." {
				c.AbortWithStatus(http.StatusNotFound)
				return
			}
		}

		root, err := os.OpenRoot(dir)
		if err != nil {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		defer func() { _ = root.Close() }()

		noindex := false
		if site == nil {
			noindex = !service.AdtyIsMainSiteHost(c.Request.Host)
		} else {
			noindex = !site.SeoIndex || service.AdtyIsApiOnlyHost(c.Request.Host)
		}
		if noindex {
			c.Header("X-Robots-Tag", "noindex")
		} else {
			c.Header("X-Robots-Tag", "index, follow")
		}

		relClean := strings.TrimPrefix(pathpkg.Clean("/"+rawRel), "/")
		var (
			file      *os.File
			info      os.FileInfo
			serveName string
		)
		if rawRel == "" || strings.HasSuffix(reqPath, "/") {
			target := "index.html"
			if relClean != "" {
				target = pathpkg.Join(relClean, "index.html")
			}
			file, info, _ = openRegularInRoot(root, target)
			serveName = "index.html"
		} else {
			if f, fi, found := openRegularInRoot(root, relClean); found {
				file, info, serveName = f, fi, pathpkg.Base(relClean)
			} else if df, di, foundDirIndex := openRegularInRoot(root, pathpkg.Join(relClean, "index.html")); foundDirIndex {
				_ = df.Close()
				_ = di
				c.Header("Cache-Control", "public, max-age=0, must-revalidate")
				c.Redirect(http.StatusMovedPermanently, appendRawQuery("/docs/"+relClean+"/", c.Request.URL.RawQuery))
				c.Abort()
				return
			} else if hf, hi, foundClean := openRegularInRoot(root, relClean+".html"); foundClean {
				file, info, serveName = hf, hi, pathpkg.Base(relClean+".html")
			}
		}

		if file != nil {
			defer func() { _ = file.Close() }()
			if strings.HasPrefix(relClean, "assets/") {
				c.Header("Cache-Control", "public, max-age=2592000, immutable")
			} else {
				c.Header("Cache-Control", "public, max-age=0, s-maxage=300, must-revalidate")
			}
			http.ServeContent(c.Writer, c.Request, serveName, info.ModTime(), file)
			c.Abort()
			return
		}

		c.Header("Cache-Control", "no-cache")
		if nf, _, found404 := openRegularInRoot(root, "404.html"); found404 {
			defer func() { _ = nf.Close() }()
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.Status(http.StatusNotFound)
			if c.Request.Method != http.MethodHead {
				_, _ = io.Copy(c.Writer, nf)
			}
			c.Abort()
			return
		}
		c.AbortWithStatus(http.StatusNotFound)
	}
}
