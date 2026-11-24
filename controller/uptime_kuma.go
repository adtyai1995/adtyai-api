package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

const (
	uptimeCacheTTL          = 10 * time.Second
	uptimeWindowSeconds     = 24 * 3600
	manualProbeTimeout      = 120 * time.Second
	maxRecentHeartbeats     = 24
	UptimeStatusUnknown     = -1
	UptimeStatusDown        = 0
	UptimeStatusUp          = 1
	UptimeStatusDegraded    = 2
	UptimeStatusPending     = 2
	UptimeStatusMaintenance = 3
)

type UptimeHeartbeat struct {
	Status int      `json:"status"`
	Time   string   `json:"time"`
	Ping   *float64 `json:"ping"`
}

type Monitor struct {
	Name       string            `json:"name"`
	Uptime     float64           `json:"uptime"`
	Status     int               `json:"status"`
	Group      string            `json:"group,omitempty"`
	Heartbeats []UptimeHeartbeat `json:"heartbeats,omitempty"`
}

type UptimeModelStatus struct {
	Model         string            `json:"model"`
	MonitorName   string            `json:"monitorName,omitempty"`
	Status        int               `json:"status"`
	StatusLabel   string            `json:"statusLabel"`
	Uptime        *float64          `json:"uptime"`
	AvgPingMs     *float64          `json:"avgPingMs,omitempty"`
	LastCheckedAt int64             `json:"lastCheckedAt,omitempty"`
	LastMessage   string            `json:"lastMessage,omitempty"`
	Heartbeats    []UptimeHeartbeat `json:"heartbeats"`
}

type UptimeGroupResult struct {
	GroupID       string              `json:"groupId,omitempty"`
	CategoryName  string              `json:"categoryName"`
	Description   string              `json:"description,omitempty"`
	Platform      string              `json:"platform,omitempty"`
	ProbeType     string              `json:"probeType,omitempty"`
	ProbeModel    string              `json:"probeModel,omitempty"`
	GroupRatio    *float64            `json:"groupRatio,omitempty"`
	Status        int                 `json:"status"`
	StatusLabel   string              `json:"statusLabel"`
	Uptime        *float64            `json:"uptime"`
	AvgPingMs     *float64            `json:"avgPingMs,omitempty"`
	CacheHitRate  *float64            `json:"cacheHitRate"`
	LastCheckedAt string              `json:"lastCheckedAt,omitempty"`
	Heartbeats    []UptimeHeartbeat   `json:"heartbeats,omitempty"`
	Monitors      []Monitor           `json:"monitors"`
	Models        []UptimeModelStatus `json:"models,omitempty"`
}

type uptimeStatusCache struct {
	sync.Mutex
	generation    uint64
	configuration string
	expiresAt     time.Time
	results       []UptimeGroupResult
	refresh       singleflight.Group
}

var publicUptimeStatusCache uptimeStatusCache

func (cache *uptimeStatusCache) get(configuration string) ([]UptimeGroupResult, uint64, bool) {
	cache.Lock()
	defer cache.Unlock()
	gen := cache.generation
	if cache.configuration != configuration || !time.Now().Before(cache.expiresAt) {
		return nil, gen, false
	}
	return cache.results, gen, true
}

// InvalidateUptimeStatusCache clears the short-lived public status cache so option
// changes and manual probe runs become visible immediately.
func InvalidateUptimeStatusCache() {
	publicUptimeStatusCache.Lock()
	publicUptimeStatusCache.generation++
	publicUptimeStatusCache.configuration = ""
	publicUptimeStatusCache.expiresAt = time.Time{}
	publicUptimeStatusCache.results = nil
	publicUptimeStatusCache.Unlock()
}

func uptimeStatusLabel(status int) string {
	switch status {
	case UptimeStatusUp:
		return "up"
	case UptimeStatusDown:
		return "down"
	case UptimeStatusDegraded:
		return "degraded"
	case UptimeStatusMaintenance:
		return "maintenance"
	default:
		return "unknown"
	}
}

func deriveUptimeStatus(hasProbes, latestUp bool, uptime float64) (int, string) {
	if !hasProbes {
		return UptimeStatusUnknown, "unknown"
	}
	if latestUp {
		if uptime >= 0.95 {
			return UptimeStatusUp, "up"
		}
		return UptimeStatusDegraded, "degraded"
	}
	if uptime >= 0.50 {
		return UptimeStatusDegraded, "degraded"
	}
	return UptimeStatusDown, "down"
}

type uptimeProbeSummary struct {
	status           int
	statusLabel      string
	uptime           *float64
	avgPingMs        *float64
	lastCheckedUnix  int64
	lastCheckedAtRFC string
	lastMessage      string
	heartbeats       []UptimeHeartbeat
}

func summarizeUptimeProbeRows(rows []model.AdtyModelProbe) uptimeProbeSummary {
	if len(rows) == 0 {
		status, label := deriveUptimeStatus(false, false, 0)
		return uptimeProbeSummary{
			status:      status,
			statusLabel: label,
			heartbeats:  []UptimeHeartbeat{},
		}
	}

	upCount := 0
	pingSum := 0.0
	pingCount := 0
	for _, row := range rows {
		if row.Status == model.AdtyModelProbeStatusUp {
			upCount++
			pingSum += row.PingMs
			pingCount++
		}
	}

	uptimeVal := float64(upCount) / float64(len(rows))
	var avgPingPtr *float64
	if pingCount > 0 {
		avg := math.Round((pingSum/float64(pingCount))*100) / 100
		avgPingPtr = &avg
	}

	latest := rows[len(rows)-1]
	latestUp := latest.Status == model.AdtyModelProbeStatusUp
	status, label := deriveUptimeStatus(true, latestUp, uptimeVal)

	startIdx := max(0, len(rows)-maxRecentHeartbeats)
	recentRows := rows[startIdx:]
	heartbeats := make([]UptimeHeartbeat, 0, len(recentRows))
	for _, row := range recentRows {
		ping := row.PingMs
		hbStatus := UptimeStatusDown
		if row.Status == model.AdtyModelProbeStatusUp {
			hbStatus = UptimeStatusUp
		}
		heartbeats = append(heartbeats, UptimeHeartbeat{
			Status: hbStatus,
			Time:   time.Unix(row.CheckedAt, 0).UTC().Format(time.RFC3339),
			Ping:   &ping,
		})
	}

	var lastCheckedAtRFC string
	if latest.CheckedAt > 0 {
		lastCheckedAtRFC = time.Unix(latest.CheckedAt, 0).UTC().Format(time.RFC3339)
	}

	return uptimeProbeSummary{
		status:           status,
		statusLabel:      label,
		uptime:           &uptimeVal,
		avgPingMs:        avgPingPtr,
		lastCheckedUnix:  latest.CheckedAt,
		lastCheckedAtRFC: lastCheckedAtRFC,
		lastMessage:      latest.Message,
		heartbeats:       heartbeats,
	}
}

func resolveUptimeGroupDisplayName(group console_setting.UptimeKumaGroup) string {
	if name := strings.TrimSpace(group.CategoryName); name != "" {
		return name
	}
	groupID := strings.TrimSpace(group.GroupID)
	if groupID == "" {
		return ""
	}
	if adtyGroup, err := model.GetAdtyGroup(groupID); err == nil && adtyGroup != nil {
		if name := strings.TrimSpace(adtyGroup.Names["en"]); name != "" {
			return name
		}
		if name := strings.TrimSpace(adtyGroup.Names["zh-CN"]); name != "" {
			return name
		}
		for _, lang := range model.AdtyContentLangs {
			if name := strings.TrimSpace(adtyGroup.Names[lang]); name != "" {
				return name
			}
		}
	}
	return groupID
}

func buildBuiltInUptimeResults(groups []console_setting.UptimeKumaGroup, probes []model.AdtyModelProbe, cacheHitRates map[string]float64) []UptimeGroupResult {
	type probeKey struct {
		groupID   string
		modelName string
	}
	grouped := make(map[probeKey][]model.AdtyModelProbe)
	for _, p := range probes {
		key := probeKey{
			groupID:   strings.TrimSpace(p.GroupId),
			modelName: strings.TrimSpace(p.ModelName),
		}
		grouped[key] = append(grouped[key], p)
	}

	results := make([]UptimeGroupResult, 0, len(groups))
	for _, group := range groups {
		if !group.IsEnabled() {
			continue
		}
		groupKey := strings.TrimSpace(group.GroupID)
		if groupKey == "" {
			groupKey = strings.TrimSpace(group.CategoryName)
		}
		displayName := resolveUptimeGroupDisplayName(group)
		probeModel := group.EffectiveProbeModel()

		probeType := "text"
		if service.IsAdtyImageProbe(group, probeModel) {
			probeType = "image"
		}
		platform := service.ResolveAdtyGroupPlatform(group, probeModel)

		// Build backwards-compatible model list (ensure primary probeModel is included).
		modelList := make([]string, 0, len(group.Models)+1)
		seenModels := make(map[string]struct{}, len(group.Models)+1)
		if probeModel != "" {
			seenModels[probeModel] = struct{}{}
			modelList = append(modelList, probeModel)
		}
		for _, m := range group.Models {
			m = strings.TrimSpace(m)
			if m == "" {
				continue
			}
			if _, dup := seenModels[m]; dup {
				continue
			}
			seenModels[m] = struct{}{}
			modelList = append(modelList, m)
		}

		models := make([]UptimeModelStatus, 0, len(modelList))
		monitors := make([]Monitor, 0, len(modelList))
		for _, modelName := range modelList {
			summary := summarizeUptimeProbeRows(grouped[probeKey{groupID: groupKey, modelName: modelName}])
			uptimeNum := 0.0
			if summary.uptime != nil {
				uptimeNum = *summary.uptime
			}
			models = append(models, UptimeModelStatus{
				Model:         modelName,
				MonitorName:   modelName,
				Status:        summary.status,
				StatusLabel:   summary.statusLabel,
				Uptime:        summary.uptime,
				AvgPingMs:     summary.avgPingMs,
				LastCheckedAt: summary.lastCheckedUnix,
				LastMessage:   summary.lastMessage,
				Heartbeats:    summary.heartbeats,
			})
			monitors = append(monitors, Monitor{
				Name:       modelName,
				Uptime:     uptimeNum,
				Status:     summary.status,
				Heartbeats: summary.heartbeats,
			})
		}

		var primarySummary uptimeProbeSummary
		if probeModel != "" {
			primarySummary = summarizeUptimeProbeRows(grouped[probeKey{groupID: groupKey, modelName: probeModel}])
		} else {
			primarySummary = summarizeUptimeProbeRows(nil)
		}

		var cacheHitRatePtr *float64
		if rate, ok := cacheHitRates[groupKey]; ok {
			r := rate
			cacheHitRatePtr = &r
		}

		results = append(results, UptimeGroupResult{
			GroupID:       group.GroupID,
			CategoryName:  displayName,
			Description:   group.Description,
			Platform:      platform,
			ProbeType:     probeType,
			ProbeModel:    probeModel,
			Status:        primarySummary.status,
			StatusLabel:   primarySummary.statusLabel,
			Uptime:        primarySummary.uptime,
			AvgPingMs:     primarySummary.avgPingMs,
			CacheHitRate:  cacheHitRatePtr,
			LastCheckedAt: primarySummary.lastCheckedAtRFC,
			Heartbeats:    primarySummary.heartbeats,
			Monitors:      monitors,
			Models:        models,
		})
	}
	return results
}

func resolveUptimeSiteID(c *gin.Context) int {
	siteID := service.AdtyRequestSiteID(c)
	if siteID == 0 {
		if userID := c.GetInt("id"); userID > 0 {
			if user, err := model.GetUserCache(userID); err == nil && user != nil && user.SiteId > 0 {
				siteID = user.SiteId
			}
		}
	}
	return siteID
}

func resolveUptimeGroupRatios(c *gin.Context, siteID int, items []UptimeGroupResult) map[string]float64 {
	if len(items) == 0 {
		return nil
	}
	userID := c.GetInt("id")
	var userGroup string
	if userID > 0 {
		if user, err := model.GetUserCache(userID); err == nil && user != nil {
			userGroup = user.Group
		}
	}

	baseRatios := ratio_setting.GetGroupRatioCopy()
	groupRatio := make(map[string]float64, len(items))
	for _, item := range items {
		gID := strings.TrimSpace(item.GroupID)
		if gID == "" || gID == "auto" {
			continue
		}
		if _, exists := groupRatio[gID]; exists {
			continue
		}
		if r, ok := baseRatios[gID]; ok {
			groupRatio[gID] = r
		} else if model.DB != nil {
			if adtyGroup, err := model.GetAdtyGroup(gID); err == nil && adtyGroup != nil && !adtyGroup.Deleted() {
				groupRatio[gID] = 1
			}
		}
	}
	if len(groupRatio) == 0 {
		return nil
	}

	if userID > 0 && userGroup != "" {
		for g := range groupRatio {
			if ratio, ok := ratio_setting.GetGroupGroupRatio(userGroup, g); ok {
				groupRatio[g] = ratio
			}
		}
	}

	rawGroupRatio := maps.Clone(groupRatio)
	if _, err := service.AdtySitePriceList(c, groupRatio, "", nil); err != nil {
		common.SysError(common.LogText("uptime status site pricing failed: %s", err.Error()))
		return nil
	}
	applyPricingTeamMultiplier(c, userID, siteID, rawGroupRatio, groupRatio, nil)
	return groupRatio
}

func prepareUptimeResultsForRequest(c *gin.Context, results []UptimeGroupResult, siteID int) []UptimeGroupResult {
	if len(results) == 0 {
		return []UptimeGroupResult{}
	}
	filtered := make([]UptimeGroupResult, 0, len(results))
	for _, item := range results {
		if siteID > 0 && item.GroupID != "" && !service.SiteGroupAllowed(siteID, item.GroupID) {
			continue
		}
		// Copy struct so per-request GroupRatio never mutates the shared site-agnostic cache.
		filtered = append(filtered, item)
	}
	if len(filtered) == 0 {
		return filtered
	}
	ratios := resolveUptimeGroupRatios(c, siteID, filtered)
	if len(ratios) > 0 {
		for i := range filtered {
			if gID := strings.TrimSpace(filtered[i].GroupID); gID != "" {
				if r, ok := ratios[gID]; ok {
					val := r
					filtered[i].GroupRatio = &val
				}
			}
		}
	}
	return filtered
}

func GetUptimeKumaStatus(c *gin.Context) {
	settings := console_setting.GetConsoleSetting()
	if !settings.UptimeKumaEnabled {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": []UptimeGroupResult{}})
		return
	}
	configuration := settings.UptimeKumaGroups
	groups := console_setting.ParseUptimeKumaGroups(configuration)
	if len(groups) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": []UptimeGroupResult{}})
		return
	}

	siteID := resolveUptimeSiteID(c)

	if results, _, ok := publicUptimeStatusCache.get(configuration); ok {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": prepareUptimeResultsForRequest(c, results, siteID)})
		return
	}

	publicUptimeStatusCache.Lock()
	startGen := publicUptimeStatusCache.generation
	publicUptimeStatusCache.Unlock()

	flightKey := fmt.Sprintf("%d\x00%s", startGen, configuration)
	refresh := publicUptimeStatusCache.refresh.DoChan(flightKey, func() (any, error) {
		if results, gen, ok := publicUptimeStatusCache.get(configuration); ok && gen == startGen {
			return results, nil
		}
		cacheHitRates := service.GetAdtyGroupCacheHitRates24h()
		since := time.Now().Unix() - uptimeWindowSeconds
		probes, err := model.ListAdtyModelProbesSince(since)
		if err != nil {
			common.SysError(common.LogText("list adty model probes: %s", err.Error()))
			return buildBuiltInUptimeResults(groups, nil, cacheHitRates), nil
		}
		results := buildBuiltInUptimeResults(groups, probes, cacheHitRates)

		publicUptimeStatusCache.Lock()
		if publicUptimeStatusCache.generation == startGen {
			publicUptimeStatusCache.configuration = configuration
			publicUptimeStatusCache.results = results
			publicUptimeStatusCache.expiresAt = time.Now().Add(uptimeCacheTTL)
		}
		publicUptimeStatusCache.Unlock()
		return results, nil
	})
	select {
	case <-c.Request.Context().Done():
		return
	case result := <-refresh:
		prepared := prepareUptimeResultsForRequest(c, result.Val.([]UptimeGroupResult), siteID)
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": prepared})
	}
}

// PostAdtyModelProbeRun manually triggers a built-in model connectivity probe batch
// (optionally scoped to one group_id) and invalidates the cached public status.
func PostAdtyModelProbeRun(c *gin.Context) {
	var body struct {
		GroupID string `json:"group_id"`
	}
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if err := common.DecodeJson(c.Request.Body, &body); err != nil && !errors.Is(err, io.EOF) {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), manualProbeTimeout)
	defer cancel()

	summary, err := service.RunAdtyModelProbesOnce(ctx, strings.TrimSpace(body.GroupID))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	InvalidateUptimeStatusCache()
	common.ApiSuccess(c, summary)
}
