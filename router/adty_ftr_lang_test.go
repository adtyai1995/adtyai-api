package router

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFTRADTY0108OfficialLanguagePages covers the two official home URLs.
func TestFTRADTY0108OfficialLanguagePages(t *testing.T) {
	en := newSEOFixture().serve(t, "adtyai.com", "/")
	require.Equal(t, 200, en.Code)
	assert.Contains(t, en.Body.String(), `<html lang="en" data-adty-seo="1">`)
	assert.Contains(t, en.Body.String(), `<link rel="canonical" href="https://adtyai.com/" />`)
	assert.Contains(t, en.Body.String(), `<link rel="alternate" hreflang="en" href="https://adtyai.com/" />`)
	assert.Contains(t, en.Body.String(), `<link rel="alternate" hreflang="zh-Hans" href="https://adtyai.com/zh-cn" />`)
	assert.NotContains(t, en.Body.String(), `hreflang="zh-Hant"`)
	assert.NotContains(t, en.Body.String(), `hreflang="ko"`)
	assert.NotContains(t, en.Body.String(), `hreflang="fr"`)

	zh := newSEOFixture().serve(t, "adtyai.com", "/zh-cn")
	require.Equal(t, 200, zh.Code)
	assert.Contains(t, zh.Body.String(), `<html lang="zh-Hans" data-adty-seo="1">`)
	assert.Contains(t, zh.Body.String(), `<link rel="canonical" href="https://adtyai.com/zh-cn" />`)
	assert.Contains(t, zh.Body.String(), "<h1>中文首屏</h1>")

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetWebRouter(engine, WebAssets{IndexPage: []byte(seoIndexPage)}, func(c *gin.Context) { c.Next() })
	for _, path := range []string{"/zh-tw", "/fr", "/ja", "/ko", "/ru", "/vi"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, suffix := range []string{"", "/?ref=test"} {
				rec := httptest.NewRecorder()
				engine.ServeHTTP(rec, httptest.NewRequest(method, path+suffix, nil))
				assert.Equal(t, http.StatusNotFound, rec.Code, "%s %s%s", method, path, suffix)
				assert.Empty(t, rec.Header().Get("Location"))
				assert.NotContains(t, rec.Body.String(), `id="root"`)
			}
		}
	}
}

func TestFTRADTY0201SEOCopyNamesTwoOfficialHomes(t *testing.T) {
	en := readRepoFile(t, "web/src/i18n/locales/en.json")
	zh := readRepoFile(t, "web/src/i18n/locales/zh.json")
	help := "Written into the source of the two home page URLs (/ for English and /zh-cn for Simplified Chinese)"
	require.Contains(t, en, help)
	require.Contains(t, zh, help)
	require.Contains(t, zh, "写进首页 2 个语言网址（英文 / 与简体中文 /zh-cn）")
	for _, banned := range []string{"eight home", "zh-TW", "/fr", "/ja", "/ko", "/ru", "/vi"} {
		assert.NotContains(t, helpLine(en, help), banned)
		assert.NotContains(t, helpLine(zh, "写进首页 2 个语言网址"), banned)
	}
	section := readRepoFile(t, "web/src/features/system-settings/site/adty-seo-section.tsx")
	assert.Contains(t, section, "two home page URLs (/ and")
	assert.Contains(t, section, "/zh-cn")
	assert.NotContains(t, section, "eight")
	comment := readRepoFile(t, "setting/adty_setting/seo.go")
	assert.Contains(t, comment, "两个正式首页")
	assert.NotContains(t, comment, "8 个语言")
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	path := filepath.Join("..", filepath.FromSlash(rel))
	body, err := os.ReadFile(path)
	require.NoError(t, err, path)
	return string(body)
}

func helpLine(file, marker string) string {
	for _, line := range strings.Split(file, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	return ""
}
