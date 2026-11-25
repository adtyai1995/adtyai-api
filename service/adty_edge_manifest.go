package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm/clause"
)

const adtyEdgeAPISubdomainRegex = `^api-s[0-9]+\.`

// AdtyEdgeManifestWildcardCert is one wildcard certificate delivered to edge nodes.
type AdtyEdgeManifestWildcardCert struct {
	RootDomain string `json:"root_domain"`
	CertName   string `json:"cert_name"`
	CertPEM    string `json:"cert_pem"`
	KeyPEM     string `json:"key_pem"`
	NotAfter   int64  `json:"not_after"`
}

// AdtyEdgeManifestCustomDomain describes how an edge node routes one custom website domain.
type AdtyEdgeManifestCustomDomain struct {
	Hostname string `json:"hostname"`
	Mode     string `json:"mode"`   // serve | redirect_only
	Action   string `json:"action"` // terminate | passthrough_cf
	CertPEM  string `json:"cert_pem,omitempty"`
	KeyPEM   string `json:"key_pem,omitempty"`
	NotAfter int64  `json:"not_after,omitempty"`
}

// AdtyEdgeManifest is the canonical state consumed by mainland edge nodes.
type AdtyEdgeManifest struct {
	Version               string                         `json:"version"`
	GeneratedAt           int64                          `json:"generated_at"`
	EdgeMainlandEnabled   bool                           `json:"edge_mainland_enabled"`
	PlatformRootDomains   []string                       `json:"platform_root_domains"`
	APISubdomainRegex     string                         `json:"api_subdomain_regex"`
	FallbackOrigin        string                         `json:"fallback_origin"`
	CloudflareCnameTarget string                         `json:"cloudflare_cname_target"`
	WildcardCerts         []AdtyEdgeManifestWildcardCert `json:"wildcard_certs"`
	CustomDomains         []AdtyEdgeManifestCustomDomain `json:"custom_domains"`
}

// AdtyEdgeNodePullState records the most recent manifest fetch from one edge node IP.
type AdtyEdgeNodePullState struct {
	PulledAt int64  `json:"pulled_at"`
	Version  string `json:"version"`
}

// AdtyEdgeManifestPullState records the most recent successful manifest fetch by edge nodes.
type AdtyEdgeManifestPullState struct {
	PulledAt int64                            `json:"pulled_at"`
	ClientIP string                           `json:"client_ip"`
	Version  string                           `json:"version"`
	Nodes    map[string]AdtyEdgeNodePullState `json:"nodes,omitempty"`
}

var adtyEdgeManifestPullMu sync.Mutex

// SaveAdtyEdgeState upserts one JSON-serialized operational state blob into adty_edge_states.
func SaveAdtyEdgeState(key string, payload any, now time.Time) error {
	if model.DB == nil {
		return nil
	}
	encoded, err := common.Marshal(payload)
	if err != nil {
		return err
	}
	row := model.AdtyEdgeState{
		StateKey:  strings.TrimSpace(key),
		Value:     string(encoded),
		UpdatedAt: now.Unix(),
	}
	return model.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "state_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&row).Error
}

// LoadAdtyEdgeState reads one JSON-serialized operational state blob from adty_edge_states.
func LoadAdtyEdgeState(key string, out any) (bool, error) {
	if model.DB == nil {
		return false, nil
	}
	var row model.AdtyEdgeState
	res := model.DB.Where("state_key = ?", strings.TrimSpace(key)).Limit(1).Find(&row)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 || strings.TrimSpace(row.Value) == "" {
		return false, nil
	}
	if err := common.Unmarshal([]byte(row.Value), out); err != nil {
		return false, err
	}
	return true, nil
}

// RecordAdtyEdgeManifestPull persists the last pull timestamp/IP/version and per-node pull map.
func RecordAdtyEdgeManifestPull(now time.Time, clientIP, version string) {
	adtyEdgeManifestPullMu.Lock()
	defer adtyEdgeManifestPullMu.Unlock()

	ip := strings.TrimSpace(clientIP)
	ver := strings.TrimSpace(version)
	var state AdtyEdgeManifestPullState
	if ok, err := LoadAdtyEdgeState(model.AdtyEdgeStateKeyManifestPull, &state); err != nil || !ok {
		state = AdtyEdgeManifestPullState{}
	}
	if state.Nodes == nil {
		state.Nodes = make(map[string]AdtyEdgeNodePullState)
	}
	if state.ClientIP != "" && state.PulledAt > 0 {
		if _, exists := state.Nodes[state.ClientIP]; !exists {
			state.Nodes[state.ClientIP] = AdtyEdgeNodePullState{
				PulledAt: state.PulledAt,
				Version:  state.Version,
			}
		}
	}
	nowUnix := now.Unix()
	state.PulledAt = nowUnix
	state.ClientIP = ip
	state.Version = ver
	if ip != "" {
		state.Nodes[ip] = AdtyEdgeNodePullState{
			PulledAt: nowUnix,
			Version:  ver,
		}
	}
	// Prune stale per-node entries older than 7 days and cap map size at 64.
	cutoff := now.Add(-7 * 24 * time.Hour).Unix()
	for nodeIP, info := range state.Nodes {
		if nodeIP != ip && info.PulledAt < cutoff {
			delete(state.Nodes, nodeIP)
		}
	}
	if len(state.Nodes) > 64 {
		type pair struct {
			ip       string
			pulledAt int64
		}
		items := make([]pair, 0, len(state.Nodes))
		for nodeIP, info := range state.Nodes {
			items = append(items, pair{ip: nodeIP, pulledAt: info.PulledAt})
		}
		slices.SortFunc(items, func(a, b pair) int {
			if a.pulledAt == b.pulledAt {
				return strings.Compare(a.ip, b.ip)
			}
			if a.pulledAt > b.pulledAt {
				return -1
			}
			return 1
		})
		trimmed := make(map[string]AdtyEdgeNodePullState, 64)
		for _, item := range items[:64] {
			trimmed[item.ip] = state.Nodes[item.ip]
		}
		state.Nodes = trimmed
	}
	if err := SaveAdtyEdgeState(model.AdtyEdgeStateKeyManifestPull, state, now); err != nil {
		common.SysError(common.LogText("record edge manifest pull failed: %s", err.Error()))
	}
}

// GetAdtyEdgeManifestPullState returns the latest recorded edge manifest pull, or nil if never pulled.
func GetAdtyEdgeManifestPullState() (*AdtyEdgeManifestPullState, error) {
	var state AdtyEdgeManifestPullState
	ok, err := LoadAdtyEdgeState(model.AdtyEdgeStateKeyManifestPull, &state)
	if err != nil || !ok {
		return nil, err
	}
	if state.Nodes == nil && state.ClientIP != "" && state.PulledAt > 0 {
		state.Nodes = map[string]AdtyEdgeNodePullState{
			state.ClientIP: {
				PulledAt: state.PulledAt,
				Version:  state.Version,
			},
		}
	}
	return &state, nil
}

// BuildAdtyEdgeManifest builds the deterministic manifest for edge nodes.
// The Version field is the SHA-256 digest of the canonical payload excluding generated_at.
func BuildAdtyEdgeManifest(ctx context.Context, now time.Time) (*AdtyEdgeManifest, error) {
	cfg := resellerSetting()
	roots := adtyEdgeRootDomains(cfg)
	slices.Sort(roots)

	manifest := &AdtyEdgeManifest{
		EdgeMainlandEnabled:   cfg.EdgeMainlandEnabled,
		PlatformRootDomains:   roots,
		APISubdomainRegex:     adtyEdgeAPISubdomainRegex,
		FallbackOrigin:        strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.FallbackOrigin)), "."),
		CloudflareCnameTarget: strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.CnameTarget)), "."),
		WildcardCerts:         []AdtyEdgeManifestWildcardCert{},
		CustomDomains:         []AdtyEdgeManifestCustomDomain{},
	}

	env := adtyEdgeACMEEnvironment(cfg)
	nowUnix := now.Unix()

	if len(roots) > 0 {
		wildcardNames := make([]string, 0, len(roots))
		for _, root := range roots {
			wildcardNames = append(wildcardNames, "*."+root)
		}
		var certs []model.AdtyEdgeCert
		if err := model.DB.WithContext(ctx).
			Where("kind = ? AND cert_name IN ? AND status = ? AND environment = ? AND not_after > ? AND cert_pem <> ? AND key_encrypted <> ?",
				model.AdtyEdgeCertKindWildcard, wildcardNames, model.AdtyEdgeCertStatusReady, env, nowUnix, "", "").
			Order("cert_name ASC").Find(&certs).Error; err != nil {
			return nil, err
		}
		for _, cert := range certs {
			keyPEM, err := model.OpenAdtyPurposeSecret(adtyEdgeCertKeyPurpose(cert.CertName), cert.KeyEncrypted)
			if err != nil || strings.TrimSpace(keyPEM) == "" {
				common.SysError(common.LogText("open edge wildcard cert key failed for %s", cert.CertName))
				continue
			}
			manifest.WildcardCerts = append(manifest.WildcardCerts, AdtyEdgeManifestWildcardCert{
				RootDomain: strings.TrimPrefix(cert.CertName, "*."),
				CertName:   cert.CertName,
				CertPEM:    cert.CertPEM,
				KeyPEM:     keyPEM,
				NotAfter:   cert.NotAfter,
			})
		}
	}

	type customDomainRow struct {
		Hostname     string
		DesiredState string
		SiteStatus   string
	}
	var domRows []customDomainRow
	if err := model.DB.WithContext(ctx).Table("adty_site_domains").
		Select("adty_site_domains.hostname, adty_site_domains.desired_state, adty_sites.status AS site_status").
		Joins("JOIN adty_sites ON adty_sites.id = adty_site_domains.site_id").
		Where("adty_site_domains.managed_by <> ? AND adty_site_domains.role = ? AND adty_site_domains.status = ? AND adty_site_domains.desired_state IN ?",
			"platform", model.AdtySiteDomainRoleSite, model.AdtySiteDomainActive, []string{"", "serve", "redirect_only"}).
		Where("adty_sites.status IN ?", []string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused, model.AdtySiteStatusClosing, model.AdtySiteStatusClosed}).
		Order("adty_site_domains.hostname ASC").Scan(&domRows).Error; err != nil {
		return nil, err
	}

	hostnames := make([]string, 0, len(domRows))
	for _, r := range domRows {
		h := strings.ToLower(strings.TrimSpace(r.Hostname))
		if h == "" {
			continue
		}
		if r.SiteStatus == model.AdtySiteStatusClosed && r.DesiredState != "redirect_only" {
			continue
		}
		hostnames = append(hostnames, h)
	}

	readyCerts := map[string]model.AdtyEdgeCert{}
	if len(hostnames) > 0 {
		var certs []model.AdtyEdgeCert
		if err := model.DB.WithContext(ctx).
			Where("kind = ? AND cert_name IN ? AND status = ? AND environment = ? AND not_after > ? AND cert_pem <> ? AND key_encrypted <> ?",
				model.AdtyEdgeCertKindCustom, hostnames, model.AdtyEdgeCertStatusReady, env, nowUnix, "", "").
			Find(&certs).Error; err != nil {
			return nil, err
		}
		for _, cert := range certs {
			readyCerts[strings.ToLower(strings.TrimSpace(cert.CertName))] = cert
		}
	}

	for _, r := range domRows {
		h := strings.ToLower(strings.TrimSpace(r.Hostname))
		if h == "" {
			continue
		}
		if r.SiteStatus == model.AdtySiteStatusClosed && r.DesiredState != "redirect_only" {
			continue
		}
		mode := "serve"
		if r.DesiredState == "redirect_only" || r.SiteStatus == model.AdtySiteStatusClosed {
			mode = "redirect_only"
		}
		entry := AdtyEdgeManifestCustomDomain{
			Hostname: h,
			Mode:     mode,
			Action:   "passthrough_cf",
		}
		if cert, ok := readyCerts[h]; ok {
			keyPEM, err := model.OpenAdtyPurposeSecret(adtyEdgeCertKeyPurpose(cert.CertName), cert.KeyEncrypted)
			if err == nil && strings.TrimSpace(keyPEM) != "" {
				entry.Action = "terminate"
				entry.CertPEM = cert.CertPEM
				entry.KeyPEM = keyPEM
				entry.NotAfter = cert.NotAfter
			} else {
				common.SysError(common.LogText("open edge custom cert key failed for %s", cert.CertName))
			}
		}
		manifest.CustomDomains = append(manifest.CustomDomains, entry)
	}

	canonical, err := common.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(canonical)
	manifest.Version = hex.EncodeToString(digest[:])
	manifest.GeneratedAt = nowUnix
	return manifest, nil
}
