package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
)

const (
	// AdtyEdgeAutoEjectFailures is the number of consecutive probe failures before
	// automatically removing an unhealthy edge node IP from Cloudflare DNS.
	AdtyEdgeAutoEjectFailures = 3
	// AdtyEdgeAutoRecoverSuccess is the number of consecutive probe successes before
	// automatically adding an ejected edge node IP back into Cloudflare DNS.
	AdtyEdgeAutoRecoverSuccess = 2
)

// AdtyEdgeNodeProbeState records the latest health probe and auto-ejection state for one HK edge IP.
type AdtyEdgeNodeProbeState struct {
	IP                   string `json:"ip"`
	CheckedAt            int64  `json:"checked_at"`
	Healthy              bool   `json:"healthy"`
	HTTPStatus           int    `json:"http_status"`
	LatencyMs            int64  `json:"latency_ms"`
	CertNotAfter         int64  `json:"cert_not_after"`
	ConsecutiveFailures  int    `json:"consecutive_failures"`
	ConsecutiveSuccesses int    `json:"consecutive_successes"`
	Ejected              bool   `json:"ejected"`
	EjectedAt            int64  `json:"ejected_at,omitempty"`
	LastError            string `json:"last_error"`
}

// AdtyEdgeProbeState records the latest origin-to-edge HTTPS health probe result across all configured nodes.
type AdtyEdgeProbeState struct {
	CheckedAt            int64                    `json:"checked_at"`
	Healthy              bool                     `json:"healthy"`
	HealthyCount         int                      `json:"healthy_count"`
	TotalCount           int                      `json:"total_count"`
	ProbeHost            string                   `json:"probe_host"`
	HTTPStatus           int                      `json:"http_status"`
	LatencyMs            int64                    `json:"latency_ms"`
	CertNotAfter         int64                    `json:"cert_not_after"`
	ConsecutiveFailures  int                      `json:"consecutive_failures"`
	ConsecutiveSuccesses int                      `json:"consecutive_successes"`
	AutoFailoverActive   bool                     `json:"auto_failover_active"`
	ActiveDNSIPs         []string                 `json:"active_dns_ips,omitempty"`
	LastAutoAction       string                   `json:"last_auto_action,omitempty"`
	LastAutoActionAt     int64                    `json:"last_auto_action_at,omitempty"`
	LastError            string                   `json:"last_error"`
	Nodes                []AdtyEdgeNodeProbeState `json:"nodes,omitempty"`
}

// AdtyEdgeWildcardCertView describes the primary platform wildcard certificate for admin UI.
type AdtyEdgeWildcardCertView struct {
	CertName      string `json:"cert_name"`
	ChallengeHost string `json:"challenge_host"`
	AliasFQDN     string `json:"alias_fqdn"`
	Environment   string `json:"environment"`
	Status        string `json:"status"`
	NotAfter      int64  `json:"not_after"`
	LastError     string `json:"last_error"`
	LastIssuedAt  int64  `json:"last_issued_at"`
	Attempts      int    `json:"attempts"`
}

// AdtyEdgeCertCounts summarizes custom website domain edge certificate statuses.
type AdtyEdgeCertCounts struct {
	Total      int `json:"total"`
	Ready      int `json:"ready"`
	WaitingDNS int `json:"waiting_dns"`
	Issuing    int `json:"issuing"`
	Failed     int `json:"failed"`
}

// AdtyEdgeStatusSummary is returned to admin settings for mainland edge observability and control.
type AdtyEdgeStatusSummary struct {
	EdgeMainlandEnabled   bool                       `json:"edge_mainland_enabled"`
	EdgeACMEStaging       bool                       `json:"edge_acme_staging"`
	EdgeNodeIP            string                     `json:"edge_node_ip"`
	EdgeNodeIPs           []string                   `json:"edge_node_ips"`
	EdgeFailoverToCF      bool                       `json:"edge_failover_to_cf"`
	EdgeProbeEnabled      bool                       `json:"edge_probe_enabled"`
	EdgeAutoFailover      bool                       `json:"edge_auto_failover"`
	EdgeMainSiteEnabled   bool                       `json:"edge_main_site_enabled"`
	EdgeMainSiteHost      string                     `json:"edge_main_site_host"`
	EdgeMainSiteOriginSNI string                     `json:"edge_main_site_origin_sni"`
	EdgeTokenConfigured   bool                       `json:"edge_token_configured"`
	DomainMainlandTarget  string                     `json:"domain_mainland_target"`
	EdgeACMEAliasSuffix   string                     `json:"edge_acme_alias_suffix"`
	WildcardCert          *AdtyEdgeWildcardCertView  `json:"wildcard_cert"`
	MainSiteCert          *AdtyEdgeWildcardCertView  `json:"main_site_cert"`
	CustomCertCounts      AdtyEdgeCertCounts         `json:"custom_cert_counts"`
	ManifestPull          *AdtyEdgeManifestPullState `json:"manifest_pull"`
	DNSSync               *AdtyEdgeDNSSyncState      `json:"dns_sync"`
	Probe                 *AdtyEdgeProbeState        `json:"probe"`
}

var (
	adtyEdgeProbeMu       sync.Mutex
	adtyEdgeProbeDialAddr = func(nodeIP string) string {
		return net.JoinHostPort(nodeIP, "443")
	}
	adtyEdgeProbeRootCAs *x509.CertPool
)

// GetAdtyEdgeProbeState returns the latest recorded edge probe state, or nil if never probed.
func GetAdtyEdgeProbeState() (*AdtyEdgeProbeState, error) {
	var state AdtyEdgeProbeState
	ok, err := LoadAdtyEdgeState(model.AdtyEdgeStateKeyProbe, &state)
	if err != nil || !ok {
		return nil, err
	}
	if state.TotalCount == 0 && len(state.Nodes) > 0 {
		state.TotalCount = len(state.Nodes)
		for _, n := range state.Nodes {
			if n.Healthy {
				state.HealthyCount++
			}
		}
	}
	return &state, nil
}

func resetAdtyEdgeAutoFailoverState(ips []string, manualFailover bool, now time.Time) {
	adtyEdgeProbeMu.Lock()
	defer adtyEdgeProbeMu.Unlock()

	prev, err := GetAdtyEdgeProbeState()
	if err != nil || prev == nil {
		return
	}
	prev.AutoFailoverActive = false
	if manualFailover {
		prev.ActiveDNSIPs = nil
	} else {
		prev.ActiveDNSIPs = slices.Clone(ips)
	}
	for i := range prev.Nodes {
		prev.Nodes[i].Ejected = false
		prev.Nodes[i].EjectedAt = 0
	}
	_ = SaveAdtyEdgeState(model.AdtyEdgeStateKeyProbe, prev, now)
}

func adtySelectEdgeProbeTarget(ctx context.Context, cfg *adty_setting.ResellerSetting) (host string, expectedSiteID int, expectedRole string, err error) {
	return adtySelectEdgeProbeTargetAt(ctx, cfg, time.Now())
}

func adtySelectEdgeProbeTargetAt(ctx context.Context, cfg *adty_setting.ResellerSetting, now time.Time) (host string, expectedSiteID int, expectedRole string, err error) {
	if now.IsZero() {
		now = time.Now()
	}
	env := adtyEdgeACMEEnvironment(cfg)
	nowUnix := now.Unix()
	roots := adtyEdgeRootDomains(cfg)

	certReady := func(kind, certName string) (bool, error) {
		var count int64
		err := model.DB.WithContext(ctx).Model(&model.AdtyEdgeCert{}).
			Where("kind = ? AND cert_name = ? AND status = ? AND environment = ? AND not_after > ? AND cert_pem <> '' AND key_encrypted <> ''",
				kind, certName, model.AdtyEdgeCertStatusReady, env, nowUnix).
			Count(&count).Error
		return count > 0, err
	}

	rootSet := make(map[string]bool, len(roots))
	for _, r := range roots {
		if nr := strings.ToLower(strings.TrimSpace(r)); nr != "" {
			rootSet[nr] = true
		}
	}

	type probeSiteDomain struct {
		Hostname   string
		SiteId     int
		Role       string
		RootDomain string
	}
	var doms []probeSiteDomain
	if err := model.DB.WithContext(ctx).
		Table("adty_site_domains").
		Select("adty_site_domains.hostname, adty_site_domains.site_id, adty_site_domains.role, adty_sites.root_domain").
		Joins("JOIN adty_sites ON adty_sites.id = adty_site_domains.site_id").
		Where("adty_site_domains.managed_by = ? AND adty_site_domains.role = ? AND adty_site_domains.status = ? AND adty_site_domains.desired_state = ? AND adty_sites.status IN ?",
			"platform", model.AdtySiteDomainRoleSite, model.AdtySiteDomainActive, "serve", []string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused, model.AdtySiteStatusClosing}).
		Order("adty_site_domains.id ASC").Find(&doms).Error; err != nil {
		return "", 0, "", err
	}

	resolveActiveRoot := func(dom probeSiteDomain, host string) string {
		root := strings.ToLower(strings.TrimSpace(dom.RootDomain))
		if root != "" {
			if rootSet[root] {
				return root
			}
			return ""
		}
		for _, candidate := range roots {
			if strings.HasSuffix(host, "."+candidate) {
				return candidate
			}
		}
		return ""
	}

	wildcardReadyCache := make(map[string]bool, len(roots))
	isWildcardReady := func(root string) (bool, error) {
		r := strings.ToLower(strings.TrimSpace(root))
		if r == "" || !rootSet[r] {
			return false, nil
		}
		if ready, ok := wildcardReadyCache[r]; ok {
			return ready, nil
		}
		ready, err := certReady(model.AdtyEdgeCertKindWildcard, "*."+r)
		if err != nil {
			return false, err
		}
		wildcardReadyCache[r] = ready
		return ready, nil
	}

	for _, dom := range doms {
		h := strings.ToLower(strings.TrimSpace(dom.Hostname))
		if h == "" {
			continue
		}
		root := resolveActiveRoot(dom, h)
		if root == "" {
			continue
		}
		ready, err := isWildcardReady(root)
		if err != nil {
			return "", 0, "", err
		}
		if ready {
			return h, dom.SiteId, dom.Role, nil
		}
	}

	mainHost := adtyEdgeMainSiteHost(cfg)
	if mainHost != "" {
		mainReady, err := certReady(model.AdtyEdgeCertKindMain, mainHost)
		if err != nil {
			return "", 0, "", err
		}
		if mainReady {
			return mainHost, 0, "", nil
		}
	}

	for _, root := range roots {
		ready, err := isWildcardReady(root)
		if err != nil {
			return "", 0, "", err
		}
		if ready {
			return "adty-edge-probe." + root, 0, "", nil
		}
	}

	// Fallback before any certificate is ready: preserve prior deterministic target selection
	// across currently configured root domains only.
	for _, dom := range doms {
		h := strings.ToLower(strings.TrimSpace(dom.Hostname))
		if h == "" || resolveActiveRoot(dom, h) == "" {
			continue
		}
		return h, dom.SiteId, dom.Role, nil
	}
	if mainHost != "" {
		return mainHost, 0, "", nil
	}
	if len(roots) == 0 {
		return "", 0, "", common.NewMessage("Configure at least one root domain before probing the edge node")
	}
	return "adty-edge-probe." + roots[0], 0, "", nil
}

func probeSingleAdtyEdgeNode(
	ctx context.Context,
	nodeIP, probeHost string,
	expectedSiteID int,
	expectedRole string,
	staging bool,
	now time.Time,
) AdtyEdgeNodeProbeState {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: probeHost,
		RootCAs:    adtyEdgeProbeRootCAs,
	}
	if staging {
		tlsCfg.InsecureSkipVerify = true
		tlsCfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("edge node presented no TLS certificate")
			}
			leaf := cs.PeerCertificates[0]
			if err := leaf.VerifyHostname(probeHost); err != nil {
				return err
			}
			if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
				return errors.New("edge TLS certificate is outside its validity period")
			}
			return nil
		}
	}

	dialAddr := adtyEdgeProbeDialAddr(nodeIP)
	transport := &http.Transport{
		Proxy:           nil,
		TLSClientConfig: tlsCfg,
		DialContext: func(dialCtx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(dialCtx, network, dialAddr)
		},
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	result := AdtyEdgeNodeProbeState{
		IP:        nodeIP,
		CheckedAt: now.Unix(),
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+probeHost+AdtyDomainProbePath, nil)
	if err != nil {
		result.LastError = err.Error()
		return result
	}

	started := time.Now()
	resp, reqErr := client.Do(req)
	result.LatencyMs = time.Since(started).Milliseconds()

	if reqErr != nil {
		result.Healthy = false
		msg := strings.TrimSpace(reqErr.Error())
		if len(msg) > 220 {
			msg = msg[:220]
		}
		result.LastError = msg
		return result
	}
	defer resp.Body.Close()
	result.HTTPStatus = resp.StatusCode
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		result.CertNotAfter = resp.TLS.PeerCertificates[0].NotAfter.Unix()
	}
	if expectedSiteID > 0 {
		if resp.StatusCode != http.StatusOK {
			result.Healthy = false
			result.LastError = fmt.Sprintf("Unexpected HTTP status %d from edge probe", resp.StatusCode)
		} else {
			var identity AdtyDomainProbeIdentity
			if err := common.DecodeJson(io.LimitReader(resp.Body, 2048), &identity); err != nil ||
				identity.SiteID != expectedSiteID || identity.Role != expectedRole || !strings.EqualFold(identity.Hostname, probeHost) {
				result.Healthy = false
				result.LastError = "Edge probe response did not match expected site identity"
			} else {
				result.Healthy = true
			}
		}
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			result.Healthy = true
		} else {
			result.Healthy = false
			result.LastError = fmt.Sprintf("Unexpected HTTP status %d from edge probe", resp.StatusCode)
		}
	}
	return result
}

// RunAdtyEdgeProbeOnce dials every configured edge node IP directly over HTTPS with SNI
// to verify TLS termination, certificate validity, and edge-to-origin reachability.
// When edge_auto_failover is enabled (and manual Cloudflare failover is off), nodes reaching
// AdtyEdgeAutoEjectFailures consecutive failures are automatically removed from Cloudflare DNS,
// restored after AdtyEdgeAutoRecoverSuccess consecutive successes, and if all nodes are ejected
// the mainland target automatically fails over to Cloudflare orange-cloud proxy until any node recovers.
func RunAdtyEdgeProbeOnce(ctx context.Context, now time.Time, force bool) (*AdtyEdgeProbeState, error) {
	adtyEdgeProbeMu.Lock()
	defer adtyEdgeProbeMu.Unlock()

	cfg := resellerSetting()
	ips := cfg.EffectiveEdgeNodeIPs()
	if !force && (!cfg.EdgeMainlandEnabled || !cfg.EdgeProbeEnabled || len(ips) == 0) {
		return nil, nil
	}
	if len(ips) == 0 {
		return nil, common.NewMessage("Configure at least one valid public IPv4 edge node address")
	}
	for _, ip := range ips {
		if !adty_setting.ValidPublicEdgeIPv4(ip) {
			return nil, common.NewMessage("Edge node IP {{ip}} must be a valid public IPv4 address", map[string]any{"ip": ip})
		}
	}
	probeHost, expectedSiteID, expectedRole, err := adtySelectEdgeProbeTargetAt(ctx, cfg, now)
	if err != nil {
		return nil, err
	}

	prev, _ := GetAdtyEdgeProbeState()
	prevByIP := make(map[string]AdtyEdgeNodeProbeState, len(ips))
	if prev != nil {
		for _, node := range prev.Nodes {
			if node.IP != "" {
				prevByIP[node.IP] = node
			}
		}
		// Migrate legacy single-node counters when upgrading from a pre-multi-node state blob.
		if len(prevByIP) == 0 && len(ips) == 1 && prev.CheckedAt > 0 {
			prevByIP[ips[0]] = AdtyEdgeNodeProbeState{
				IP:                   ips[0],
				CheckedAt:            prev.CheckedAt,
				Healthy:              prev.Healthy,
				HTTPStatus:           prev.HTTPStatus,
				LatencyMs:            prev.LatencyMs,
				CertNotAfter:         prev.CertNotAfter,
				ConsecutiveFailures:  prev.ConsecutiveFailures,
				ConsecutiveSuccesses: prev.ConsecutiveSuccesses,
				LastError:            prev.LastError,
			}
		}
	}

	nodes := make([]AdtyEdgeNodeProbeState, len(ips))
	var wg sync.WaitGroup
	for i, ip := range ips {
		wg.Add(1)
		go func(idx int, nodeIP string) {
			defer wg.Done()
			nodes[idx] = probeSingleAdtyEdgeNode(ctx, nodeIP, probeHost, expectedSiteID, expectedRole, cfg.EdgeACMEStaging, now)
		}(i, ip)
	}
	wg.Wait()

	healthyCount := 0
	var maxHealthyLatency int64
	var earliestCertNotAfter int64
	firstFailureIdx := -1
	activeIPs := make([]string, 0, len(ips))

	for i := range nodes {
		prevNode, hasPrev := prevByIP[nodes[i].IP]
		if nodes[i].Healthy {
			healthyCount++
			nodes[i].ConsecutiveFailures = 0
			if hasPrev {
				nodes[i].ConsecutiveSuccesses = prevNode.ConsecutiveSuccesses + 1
			} else {
				nodes[i].ConsecutiveSuccesses = 1
			}
			if nodes[i].LatencyMs > maxHealthyLatency {
				maxHealthyLatency = nodes[i].LatencyMs
			}
		} else {
			if firstFailureIdx == -1 {
				firstFailureIdx = i
			}
			nodes[i].ConsecutiveSuccesses = 0
			if hasPrev {
				nodes[i].ConsecutiveFailures = prevNode.ConsecutiveFailures + 1
			} else {
				nodes[i].ConsecutiveFailures = 1
			}
		}
		if nodes[i].CertNotAfter > 0 && (earliestCertNotAfter == 0 || nodes[i].CertNotAfter < earliestCertNotAfter) {
			earliestCertNotAfter = nodes[i].CertNotAfter
		}

		if !cfg.EdgeAutoFailover {
			nodes[i].Ejected = false
			nodes[i].EjectedAt = 0
		} else if hasPrev && prevNode.Ejected {
			if nodes[i].ConsecutiveSuccesses >= AdtyEdgeAutoRecoverSuccess {
				nodes[i].Ejected = false
				nodes[i].EjectedAt = 0
			} else {
				nodes[i].Ejected = true
				nodes[i].EjectedAt = prevNode.EjectedAt
			}
		} else if nodes[i].ConsecutiveFailures >= AdtyEdgeAutoEjectFailures {
			nodes[i].Ejected = true
			nodes[i].EjectedAt = now.Unix()
		}

		if !nodes[i].Ejected {
			activeIPs = append(activeIPs, nodes[i].IP)
		}
	}

	allHealthy := healthyCount == len(nodes) && len(nodes) > 0
	rep := nodes[0]
	if firstFailureIdx >= 0 {
		rep = nodes[firstFailureIdx]
	}
	summaryLatency := maxHealthyLatency
	if healthyCount == 0 {
		summaryLatency = rep.LatencyMs
	}

	state := AdtyEdgeProbeState{
		CheckedAt:    now.Unix(),
		Healthy:      allHealthy,
		HealthyCount: healthyCount,
		TotalCount:   len(nodes),
		ProbeHost:    probeHost,
		HTTPStatus:   rep.HTTPStatus,
		LatencyMs:    summaryLatency,
		CertNotAfter: earliestCertNotAfter,
		LastError:    rep.LastError,
		ActiveDNSIPs: activeIPs,
		Nodes:        nodes,
	}
	if prev != nil {
		state.LastAutoAction = prev.LastAutoAction
		state.LastAutoActionAt = prev.LastAutoActionAt
	}
	if allHealthy {
		state.ConsecutiveFailures = 0
		if prev != nil {
			state.ConsecutiveSuccesses = prev.ConsecutiveSuccesses + 1
		} else {
			state.ConsecutiveSuccesses = 1
		}
	} else {
		state.ConsecutiveSuccesses = 0
		if prev != nil {
			state.ConsecutiveFailures = prev.ConsecutiveFailures + 1
		} else {
			state.ConsecutiveFailures = 1
		}
	}

	prevAutoFailover := prev != nil && prev.AutoFailoverActive
	if cfg.EdgeFailoverToCF {
		state.AutoFailoverActive = false
		state.ActiveDNSIPs = nil
	} else if cfg.EdgeAutoFailover && cfg.EdgeMainlandEnabled && cfg.CloudflareConfigured() && strings.TrimSpace(cfg.DomainMainlandTarget) != "" {
		dnsState, _ := GetAdtyEdgeDNSSyncState()
		if len(activeIPs) > 0 {
			needSync := dnsState == nil || dnsState.Proxied || !slices.Equal(dnsState.IPs, activeIPs) || prevAutoFailover
			if needSync {
				if _, syncErr := syncAdtyEdgeMainlandDNSWithIPs(ctx, cfg, activeIPs, false, true); syncErr != nil {
					common.SysError(common.LogText("adty edge auto-reconcile DNS failed: %s", syncErr.Error()))
					state.AutoFailoverActive = prevAutoFailover
					if dnsState != nil && !dnsState.Proxied {
						state.ActiveDNSIPs = slices.Clone(dnsState.IPs)
					} else if prevAutoFailover {
						state.ActiveDNSIPs = nil
					}
				} else {
					state.AutoFailoverActive = false
					state.ActiveDNSIPs = slices.Clone(activeIPs)
					if len(activeIPs) < len(ips) {
						state.LastAutoAction = "ejected_unhealthy"
						state.LastAutoActionAt = now.Unix()
					} else if prevAutoFailover || (dnsState != nil && (dnsState.Proxied || len(dnsState.IPs) < len(ips))) {
						state.LastAutoAction = "recovered_all"
						state.LastAutoActionAt = now.Unix()
					}
				}
			} else {
				state.AutoFailoverActive = false
				state.ActiveDNSIPs = slices.Clone(activeIPs)
			}
		} else {
			needCF := !prevAutoFailover || dnsState == nil || !dnsState.Proxied
			if needCF {
				if _, syncErr := syncAdtyEdgeMainlandDNSWithIPs(ctx, cfg, []string{ips[0]}, true, true); syncErr != nil {
					common.SysError(common.LogText("adty edge auto-failover to Cloudflare failed: %s", syncErr.Error()))
					state.AutoFailoverActive = prevAutoFailover
					if dnsState != nil && !dnsState.Proxied {
						state.ActiveDNSIPs = slices.Clone(dnsState.IPs)
					}
				} else {
					state.AutoFailoverActive = true
					state.ActiveDNSIPs = nil
					state.LastAutoAction = "auto_failover_cf"
					state.LastAutoActionAt = now.Unix()
				}
			} else {
				state.AutoFailoverActive = true
				state.ActiveDNSIPs = nil
			}
		}
	} else {
		state.AutoFailoverActive = false
	}

	if err := SaveAdtyEdgeState(model.AdtyEdgeStateKeyProbe, state, now); err != nil {
		return &state, err
	}
	return &state, nil
}

// GetAdtyEdgeStatus aggregates edge configuration, certificate status, manifest pull, DNS sync, and probe results.
func GetAdtyEdgeStatus() (*AdtyEdgeStatusSummary, error) {
	cfg := resellerSetting()
	ips := cfg.EffectiveEdgeNodeIPs()
	if ips == nil {
		ips = []string{}
	}
	firstIP := ""
	if len(ips) > 0 {
		firstIP = ips[0]
	}
	summary := &AdtyEdgeStatusSummary{
		EdgeMainlandEnabled:   cfg.EdgeMainlandEnabled,
		EdgeACMEStaging:       cfg.EdgeACMEStaging,
		EdgeNodeIP:            firstIP,
		EdgeNodeIPs:           ips,
		EdgeFailoverToCF:      cfg.EdgeFailoverToCF,
		EdgeProbeEnabled:      cfg.EdgeProbeEnabled,
		EdgeAutoFailover:      cfg.EdgeAutoFailover,
		EdgeMainSiteEnabled:   cfg.EdgeMainSiteEnabled,
		EdgeMainSiteHost:      strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.EdgeMainSiteHost)), "."),
		EdgeMainSiteOriginSNI: strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.EdgeMainSiteOriginSNI)), "."),
		EdgeTokenConfigured:   strings.TrimSpace(os.Getenv("ADTY_EDGE_TOKEN")) != "",
		DomainMainlandTarget:  strings.TrimSpace(cfg.DomainMainlandTarget),
		EdgeACMEAliasSuffix:   strings.TrimSpace(cfg.EdgeACMEAliasSuffix),
	}

	roots := adtyEdgeRootDomains(cfg)
	if len(roots) > 0 {
		certName := "*." + roots[0]
		suffix := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.EdgeACMEAliasSuffix)), ".")
		aliasFQDN := ""
		if suffix != "" {
			aliasFQDN = adtyEdgeWildcardAlias(roots[0], true, suffix)
		}
		wc := &AdtyEdgeWildcardCertView{
			CertName:      certName,
			ChallengeHost: adtyEdgeChallengeHost(certName),
			AliasFQDN:     aliasFQDN,
			Environment:   adtyEdgeACMEEnvironment(cfg),
			Status:        model.AdtyEdgeCertStatusWaitingDNS,
		}
		if row, err := GetAdtyEdgeWildcardCert(cfg); err != nil {
			return nil, err
		} else if row != nil {
			wc.AliasFQDN = row.AliasFQDN
			wc.Environment = row.Environment
			wc.Status = row.Status
			wc.NotAfter = row.NotAfter
			wc.LastError = row.LastError
			wc.LastIssuedAt = row.LastIssuedAt
			wc.Attempts = row.Attempts
		}
		summary.WildcardCert = wc
	}

	if mainHost := adtyEdgeMainSiteHost(cfg); mainHost != "" {
		mc := &AdtyEdgeWildcardCertView{
			CertName:      mainHost,
			ChallengeHost: adtyEdgeChallengeHost(mainHost),
			AliasFQDN:     adtyEdgeMainSiteAlias(cfg.EdgeACMEAliasSuffix),
			Environment:   adtyEdgeACMEEnvironment(cfg),
			Status:        model.AdtyEdgeCertStatusWaitingDNS,
		}
		if row, err := GetAdtyEdgeMainSiteCert(cfg); err != nil {
			return nil, err
		} else if row != nil {
			mc.AliasFQDN = row.AliasFQDN
			mc.Environment = row.Environment
			mc.Status = row.Status
			mc.NotAfter = row.NotAfter
			mc.LastError = row.LastError
			mc.LastIssuedAt = row.LastIssuedAt
			mc.Attempts = row.Attempts
		}
		summary.MainSiteCert = mc
	}

	var customStatusRows []struct {
		EdgeCertStatus string
		Total          int
	}
	if err := model.DB.Model(&model.AdtySiteDomain{}).
		Select("edge_cert_status, COUNT(*) AS total").
		Where("managed_by <> ? AND role = ? AND status = ? AND desired_state IN ?",
			"platform", model.AdtySiteDomainRoleSite, model.AdtySiteDomainActive, []string{"", "serve", "redirect_only"}).
		Group("edge_cert_status").Scan(&customStatusRows).Error; err != nil {
		return nil, err
	}
	for _, r := range customStatusRows {
		summary.CustomCertCounts.Total += r.Total
		switch r.EdgeCertStatus {
		case model.AdtyEdgeCertStatusReady:
			summary.CustomCertCounts.Ready += r.Total
		case model.AdtyEdgeCertStatusIssuing:
			summary.CustomCertCounts.Issuing += r.Total
		case model.AdtyEdgeCertStatusFailed:
			summary.CustomCertCounts.Failed += r.Total
		default:
			summary.CustomCertCounts.WaitingDNS += r.Total
		}
	}

	var err error
	if summary.ManifestPull, err = GetAdtyEdgeManifestPullState(); err != nil {
		return nil, err
	}
	if summary.DNSSync, err = GetAdtyEdgeDNSSyncState(); err != nil {
		return nil, err
	}
	if summary.Probe, err = GetAdtyEdgeProbeState(); err != nil {
		return nil, err
	}
	return summary, nil
}

func startAdtyEdgeProbeTask() {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if _, err := RunAdtyEdgeProbeOnce(ctx, time.Now(), false); err != nil {
				common.SysError(common.LogText("adty edge probe task: %s", err.Error()))
			}
			cancel()
		}
	}()
}

func init() {
	adtyMasterTasks = append(adtyMasterTasks, startAdtyEdgeProbeTask)
}
