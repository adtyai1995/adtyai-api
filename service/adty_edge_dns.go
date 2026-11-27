package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"gorm.io/gorm"
)

// AdtyEdgeDNSSyncState records the latest Cloudflare DNS A-record synchronization
// for the mainland edge hostname (e.g. cn.adtyaicf.top).
type AdtyEdgeDNSSyncState struct {
	SyncedAt  int64    `json:"synced_at"`
	Target    string   `json:"target"`
	IP        string   `json:"ip"`
	IPs       []string `json:"ips,omitempty"`
	Proxied   bool     `json:"proxied"`
	AutoMode  bool     `json:"auto_mode,omitempty"`
	TTL       int      `json:"ttl"`
	RecordID  string   `json:"record_id"`
	RecordIDs []string `json:"record_ids,omitempty"`
	LastError string   `json:"last_error"`
}

// GetAdtyEdgeDNSSyncState returns the latest recorded DNS sync state, or nil if never synced.
func GetAdtyEdgeDNSSyncState() (*AdtyEdgeDNSSyncState, error) {
	var state AdtyEdgeDNSSyncState
	ok, err := LoadAdtyEdgeState(model.AdtyEdgeStateKeyDNSSync, &state)
	if err != nil || !ok {
		return nil, err
	}
	if len(state.IPs) == 0 && strings.TrimSpace(state.IP) != "" {
		state.IPs = []string{strings.TrimSpace(state.IP)}
	}
	if len(state.RecordIDs) == 0 && strings.TrimSpace(state.RecordID) != "" {
		state.RecordIDs = []string{strings.TrimSpace(state.RecordID)}
	}
	return &state, nil
}

func validateAdtyEdgeMainlandDNSTarget(cfg *adty_setting.ResellerSetting) (string, []string, error) {
	if cfg == nil || !cfg.CloudflareConfigured() {
		return "", nil, common.NewMessage("Configure Cloudflare credentials before syncing mainland edge DNS")
	}
	target := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.DomainMainlandTarget)), ".")
	if target == "" {
		return "", nil, common.NewMessage("Configure the mainland CNAME target before syncing edge DNS")
	}
	cnameTarget := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.CnameTarget)), ".")
	fallbackOrigin := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.FallbackOrigin)), ".")
	if target == cnameTarget || target == fallbackOrigin {
		return "", nil, common.NewMessage("Mainland CNAME target cannot be the same as the Cloudflare CNAME target or fallback origin")
	}
	ips := cfg.EffectiveEdgeNodeIPs()
	if len(ips) == 0 {
		return "", nil, common.NewMessage("Configure at least one valid public IPv4 edge node address")
	}
	for _, ip := range ips {
		if !adty_setting.ValidPublicEdgeIPv4(ip) {
			return "", nil, common.NewMessage("Edge node IP {{ip}} must be a valid public IPv4 address", map[string]any{"ip": ip})
		}
	}
	return target, ips, nil
}

// syncAdtyEdgeMainlandDNSWithIPs reconciles the mainland target A record(s) in Cloudflare DNS
// and records the resulting sync state. Caller must hold adtyEdgeProbeMu.
func syncAdtyEdgeMainlandDNSWithIPs(ctx context.Context, cfg *adty_setting.ResellerSetting, ips []string, proxied bool, autoMode bool) (*AdtyEdgeDNSSyncState, error) {
	target, configuredIPs, err := validateAdtyEdgeMainlandDNSTarget(cfg)
	if err != nil {
		return nil, err
	}
	targetIPs := adty_setting.NormalizeEdgeNodeIPs(ips)
	if len(targetIPs) == 0 {
		targetIPs = configuredIPs
	}
	for _, ip := range targetIPs {
		if !adty_setting.ValidPublicEdgeIPv4(ip) {
			return nil, common.NewMessage("Edge node IP {{ip}} must be a valid public IPv4 address", map[string]any{"ip": ip})
		}
	}
	now := time.Now()
	prev, _ := GetAdtyEdgeDNSSyncState()

	recs, err := AdtyCloudflare.upsertARecords(ctx, cfg.CloudflareZoneId, target, targetIPs, proxied)
	if err != nil {
		userErr := err
		var msg *common.Message
		if !errors.As(err, &msg) {
			userErr = adtyCloudflareUserError(err)
		}
		errState := AdtyEdgeDNSSyncState{
			Target:    target,
			IP:        targetIPs[0],
			IPs:       targetIPs,
			Proxied:   proxied,
			AutoMode:  autoMode,
			LastError: userErr.Error(),
		}
		if prev != nil {
			errState.SyncedAt = prev.SyncedAt
			errState.TTL = prev.TTL
			errState.RecordID = prev.RecordID
			errState.RecordIDs = prev.RecordIDs
			errState.IP = prev.IP
			errState.IPs = prev.IPs
			errState.Proxied = prev.Proxied
			errState.AutoMode = prev.AutoMode
		}
		_ = SaveAdtyEdgeState(model.AdtyEdgeStateKeyDNSSync, errState, now)
		return nil, userErr
	}

	syncedIPs := make([]string, 0, len(recs))
	recordIDs := make([]string, 0, len(recs))
	firstIP := targetIPs[0]
	firstID := ""
	ttl := 60
	if proxied {
		ttl = 1
	}
	for i, rec := range recs {
		ip := strings.TrimSpace(rec.Content)
		if ip == "" && i < len(targetIPs) {
			ip = targetIPs[i]
		}
		syncedIPs = append(syncedIPs, ip)
		if rec.Id != "" {
			recordIDs = append(recordIDs, rec.Id)
		}
		if i == 0 {
			firstIP = ip
			firstID = rec.Id
			if rec.TTL > 0 {
				ttl = rec.TTL
			}
		}
	}
	actualProxied := proxied
	if len(recs) > 0 {
		actualProxied = recs[0].Proxied
	}
	state := AdtyEdgeDNSSyncState{
		SyncedAt:  now.Unix(),
		Target:    target,
		IP:        firstIP,
		IPs:       syncedIPs,
		Proxied:   actualProxied,
		AutoMode:  autoMode,
		TTL:       ttl,
		RecordID:  firstID,
		RecordIDs: recordIDs,
		LastError: "",
	}
	if err := SaveAdtyEdgeState(model.AdtyEdgeStateKeyDNSSync, state, now); err != nil {
		return &state, err
	}
	return &state, nil
}

// SyncAdtyEdgeMainlandDNS synchronizes the configured mainland target A record(s)
// in Cloudflare DNS according to the current edge_node_ips and edge_failover_to_cf settings.
// Manual sync resets any auto-ejection flags and restores all configured nodes unless manual
// Cloudflare failover is enabled.
func SyncAdtyEdgeMainlandDNS(ctx context.Context) (*AdtyEdgeDNSSyncState, error) {
	adtyEdgeProbeMu.Lock()
	defer adtyEdgeProbeMu.Unlock()

	cfg := resellerSetting()
	_, ips, err := validateAdtyEdgeMainlandDNSTarget(cfg)
	if err != nil {
		return nil, err
	}
	state, err := syncAdtyEdgeMainlandDNSWithIPs(ctx, cfg, ips, cfg.EdgeFailoverToCF, false)
	if state != nil {
		adtyEdgeControlEpoch++
	}
	if err != nil {
		return nil, err
	}
	resetAdtyEdgeAutoFailoverStateLocked(ips, cfg.EdgeFailoverToCF, time.Now())
	return state, nil
}

// SetAdtyEdgeFailover switches the mainland target between direct HK edge (grey cloud, TTL 60)
// and Cloudflare failover proxy (orange cloud, auto TTL), updating Cloudflare DNS first so the
// stored switch always reflects the live DNS state.
func SetAdtyEdgeFailover(ctx context.Context, failoverToCF bool) (*AdtyEdgeDNSSyncState, error) {
	// Enforce global lock order: model.ResellerWriteMu -> adtyEdgeProbeMu.
	// UpdateAdtyResellerSettings holds ResellerWriteMu while calling SyncAdtyEdgeMainlandDNS
	// (which acquires adtyEdgeProbeMu), so acquiring ResellerWriteMu first prevents AB-BA
	// deadlocks and serializes manual failover against concurrent settings writes.
	model.ResellerWriteMu.Lock()
	defer model.ResellerWriteMu.Unlock()
	adtyEdgeProbeMu.Lock()
	defer adtyEdgeProbeMu.Unlock()

	cfg := resellerSetting()
	_, ips, err := validateAdtyEdgeMainlandDNSTarget(cfg)
	if err != nil {
		return nil, err
	}
	state, err := syncAdtyEdgeMainlandDNSWithIPs(ctx, cfg, ips, failoverToCF, false)
	if state != nil {
		adtyEdgeControlEpoch++
	}
	if err != nil {
		return nil, err
	}
	key := adty_setting.ResellerSettingModule + ".edge_failover_to_cf"
	options := map[string]string{key: strconv.FormatBool(failoverToCF)}

	if err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := model.LockResellerOptionsTx(tx); err != nil {
			return err
		}
		return model.SaveOptionsTx(tx, options)
	}); err != nil {
		return state, err
	}
	if err := model.ApplyOptionValues(options); err != nil {
		return state, err
	}
	InvalidateAdtySiteCache()
	resetAdtyEdgeAutoFailoverStateLocked(ips, failoverToCF, time.Now())
	return state, nil
}
