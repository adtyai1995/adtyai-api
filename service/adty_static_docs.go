package service

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	adtyStaticDocsEnvKey        = "ADTY_DOCS_DIR"
	defaultAdtyStaticDocs       = "/data/docs"
	defaultBundledAdtyStaticDir = "/app/docs"
	adtyStaticDocsCacheTTL      = 2 * time.Second
)

var (
	adtyBundledStaticDocsDir = defaultBundledAdtyStaticDir

	adtyStaticDocsState struct {
		mu        sync.Mutex
		key       string
		mounted   bool
		checkedAt time.Time
	}
)

// SetAdtyBundledStaticDocsDirForTest overrides the bundled fallback docs
// directory for unit tests and returns a cleanup callback.
func SetAdtyBundledStaticDocsDirForTest(dir string) func() {
	adtyStaticDocsState.mu.Lock()
	prev := adtyBundledStaticDocsDir
	adtyBundledStaticDocsDir = strings.TrimSpace(dir)
	adtyStaticDocsState.checkedAt = time.Time{}
	adtyStaticDocsState.mu.Unlock()
	return func() {
		adtyStaticDocsState.mu.Lock()
		adtyBundledStaticDocsDir = prev
		adtyStaticDocsState.checkedAt = time.Time{}
		adtyStaticDocsState.mu.Unlock()
	}
}

// AdtyStaticDocsDir returns the configured primary directory for same-domain
// white-label static docs (e.g. VitePress built with base="/docs/").
// Setting ADTY_DOCS_DIR=off/none/false/0 explicitly disables both mounted and
// image-bundled static docs serving.
// When unset, it checks /data/docs first (host hot-override) and falls back to
// image-bundled /app/docs.
func AdtyStaticDocsDir() string {
	raw := strings.TrimSpace(os.Getenv(adtyStaticDocsEnvKey))
	switch strings.ToLower(raw) {
	case "off", "none", "false", "0":
		return ""
	case "":
		return defaultAdtyStaticDocs
	default:
		return filepath.Clean(raw)
	}
}

func adtyStaticDocsCandidateDirs() []string {
	raw := strings.TrimSpace(os.Getenv(adtyStaticDocsEnvKey))
	switch strings.ToLower(raw) {
	case "off", "none", "false", "0":
		return nil
	}

	adtyStaticDocsState.mu.Lock()
	bundled := adtyBundledStaticDocsDir
	adtyStaticDocsState.mu.Unlock()

	candidates := make([]string, 0, 2)
	if raw == "" {
		candidates = append(candidates, defaultAdtyStaticDocs)
		if bundled != "" && bundled != defaultAdtyStaticDocs {
			candidates = append(candidates, bundled)
		}
		return candidates
	}
	clean := filepath.Clean(raw)
	candidates = append(candidates, clean)
	if clean == defaultAdtyStaticDocs && bundled != "" && bundled != clean {
		candidates = append(candidates, bundled)
	}
	return candidates
}

func isAdtyStaticDocsReady(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil && !info.IsDir()
}

func resolveReadyAdtyStaticDocsDir(candidates []string) (string, bool) {
	for _, dir := range candidates {
		if isAdtyStaticDocsReady(dir) {
			return dir, true
		}
	}
	return "", false
}

// AdtyStaticDocsMounted reports whether static docs (host hot-override or
// image-bundled fallback) are currently available on disk. Results are briefly
// cached to keep /api/status free of per-request filesystem stats.
func AdtyStaticDocsMounted() bool {
	candidates := adtyStaticDocsCandidateDirs()
	if len(candidates) == 0 {
		return false
	}
	key := strings.Join(candidates, "\x00")
	now := time.Now()
	adtyStaticDocsState.mu.Lock()
	if adtyStaticDocsState.key == key && now.Sub(adtyStaticDocsState.checkedAt) < adtyStaticDocsCacheTTL {
		mounted := adtyStaticDocsState.mounted
		adtyStaticDocsState.mu.Unlock()
		return mounted
	}
	adtyStaticDocsState.mu.Unlock()

	_, mounted := resolveReadyAdtyStaticDocsDir(candidates)

	adtyStaticDocsState.mu.Lock()
	adtyStaticDocsState.key = key
	adtyStaticDocsState.mounted = mounted
	adtyStaticDocsState.checkedAt = now
	adtyStaticDocsState.mu.Unlock()
	return mounted
}

// AdtyStaticDocsRootDir returns the live root directory when mounted or
// image-bundled static docs are ready to serve.
func AdtyStaticDocsRootDir() (string, bool) {
	return resolveReadyAdtyStaticDocsDir(adtyStaticDocsCandidateDirs())
}
