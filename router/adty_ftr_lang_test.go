package router

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFTRADTY0108OfficialLanguagePages covers the committed page switch:
// English / and Simplified Chinese /zh-cn are the official homes. Leftover
// aliases stay reachable and canonicalize onto those two homes. They are not
// advertised as extra languages, and they are not turned into 404s.
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

	for _, path := range []string{"/zh-tw", "/fr", "/ja", "/ko", "/ru", "/vi"} {
		rec := newSEOFixture().serve(t, "adtyai.com", path)
		require.Equal(t, 200, rec.Code, path)
		body := rec.Body.String()
		assert.NotContains(t, body, "hreflang=\""+strings.TrimPrefix(path, "/")+"\"", path)
		assert.NotContains(t, body, "https://adtyai.com"+path+"\"", path)
		if path == "/zh-tw" {
			assert.Contains(t, body, `<link rel="canonical" href="https://adtyai.com/zh-cn" />`)
			assert.Contains(t, body, `<html lang="zh-Hans"`)
		} else {
			assert.Contains(t, body, `<link rel="canonical" href="https://adtyai.com/" />`)
			assert.Contains(t, body, `<html lang="en"`)
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
