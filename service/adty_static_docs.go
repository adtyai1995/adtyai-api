package service

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	adtyStaticDocsEnvKey   = "ADTY_DOCS_DIR"
	defaultAdtyStaticDocs  = "/data/docs"
	adtyStaticDocsCacheTTL = 2 * time.Second
)

var adtyStaticDocsState struct {
	mu        sync.Mutex
	dir       string
	mounted   bool
	checkedAt time.Time
}

// AdtyStaticDocsDir returns the configured directory for externally built
// same-domain white-label static docs (e.g. VitePress built with base="/docs/").
// Setting ADTY_DOCS_DIR=off/none/false/0 explicitly disables static docs serving.
// When unset, it checks /data/docs so mounting ./data:/data works out of the box.
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

func isAdtyStaticDocsReady(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil && !info.IsDir()
}

// AdtyStaticDocsMounted reports whether external static docs are currently
// available on disk. Results are briefly cached by directory path to keep
// /api/status free of per-request filesystem stats.
func AdtyStaticDocsMounted() bool {
	dir := AdtyStaticDocsDir()
	if dir == "" {
		return false
	}
	now := time.Now()
	adtyStaticDocsState.mu.Lock()
	if adtyStaticDocsState.dir == dir && now.Sub(adtyStaticDocsState.checkedAt) < adtyStaticDocsCacheTTL {
		mounted := adtyStaticDocsState.mounted
		adtyStaticDocsState.mu.Unlock()
		return mounted
	}
	adtyStaticDocsState.mu.Unlock()

	mounted := isAdtyStaticDocsReady(dir)

	adtyStaticDocsState.mu.Lock()
	adtyStaticDocsState.dir = dir
	adtyStaticDocsState.mounted = mounted
	adtyStaticDocsState.checkedAt = now
	adtyStaticDocsState.mu.Unlock()
	return mounted
}

// AdtyStaticDocsRootDir returns the live root directory when mounted static
// docs are ready to serve.
func AdtyStaticDocsRootDir() (string, bool) {
	dir := AdtyStaticDocsDir()
	if !isAdtyStaticDocsReady(dir) {
		return "", false
	}
	return dir, true
}
