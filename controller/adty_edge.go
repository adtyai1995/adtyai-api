package controller

import (
	"context"
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// GetAdtyEdgeManifest serves the signed-token internal manifest for mainland edge nodes.
// Edge nodes authenticate with X-Adty-Edge-Token matching ADTY_EDGE_TOKEN and use ETag/304.
func GetAdtyEdgeManifest(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	expected := strings.TrimSpace(os.Getenv("ADTY_EDGE_TOKEN"))
	provided := strings.TrimSpace(c.GetHeader("X-Adty-Edge-Token"))
	if expected == "" || len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	now := time.Now()
	manifest, err := service.BuildAdtyEdgeManifest(c.Request.Context(), now)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	etag := `"` + manifest.Version + `"`
	c.Header("ETag", etag)
	service.RecordAdtyEdgeManifestPull(now, c.ClientIP(), manifest.Version)

	if matchesETag(c.GetHeader("If-None-Match"), manifest.Version) {
		c.Status(http.StatusNotModified)
		return
	}
	c.JSON(http.StatusOK, manifest)
}

func matchesETag(header, version string) bool {
	header = strings.TrimSpace(header)
	if header == "" || version == "" {
		return false
	}
	for _, part := range strings.Split(header, ",") {
		tag := strings.TrimSpace(part)
		tag = strings.TrimPrefix(tag, "W/")
		tag = strings.Trim(tag, `"`)
		if tag == version {
			return true
		}
	}
	return false
}

// GetAdtyAdminEdgeStatus returns edge acceleration status, wildcard cert state, DNS sync, and probe state.
func GetAdtyAdminEdgeStatus(c *gin.Context) {
	status, err := service.GetAdtyEdgeStatus()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, status)
}

// PostAdtyAdminEdgeFailover toggles one-click failover between the HK edge node and Cloudflare proxy.
func PostAdtyAdminEdgeFailover(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		denyAdtyAdminAction(c)
		return
	}
	var body struct {
		FailoverToCF bool `json:"failover_to_cf"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
	defer cancel()
	syncState, err := service.SetAdtyEdgeFailover(ctx, body.FailoverToCF)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "adty.site.edge_failover", map[string]any{"failover_to_cf": body.FailoverToCF})
	common.ApiSuccess(c, gin.H{"failover_to_cf": body.FailoverToCF, "dns_sync": syncState})
}

// PostAdtyAdminEdgeSyncDNS forces an immediate Cloudflare DNS sync for the mainland target A record.
func PostAdtyAdminEdgeSyncDNS(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		denyAdtyAdminAction(c)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
	defer cancel()
	syncState, err := service.SyncAdtyEdgeMainlandDNS(ctx)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "adty.site.edge_sync_dns", map[string]any{"target": syncState.Target, "ip": syncState.IP, "ips": strings.Join(syncState.IPs, ","), "proxied": syncState.Proxied})
	common.ApiSuccess(c, syncState)
}

// PostAdtyAdminEdgeProbe triggers an immediate origin-to-edge HTTPS health probe.
func PostAdtyAdminEdgeProbe(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		denyAdtyAdminAction(c)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	probeState, err := service.RunAdtyEdgeProbeOnce(ctx, time.Now(), true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, probeState)
}

// PostAdtyAdminEdgeCertRetry resets backoff on an edge certificate (defaulting to the primary wildcard cert).
func PostAdtyAdminEdgeCertRetry(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		denyAdtyAdminAction(c)
		return
	}
	var body struct {
		CertName string `json:"cert_name"`
	}
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if err := common.DecodeJson(c.Request.Body, &body); err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	if err := service.RetryAdtyEdgeCert(body.CertName); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "adty.site.edge_cert_retry", map[string]any{"cert_name": strings.TrimSpace(body.CertName)})
	common.ApiSuccess(c, gin.H{"queued": true})
}
