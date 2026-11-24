package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/console_setting"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

const (
	uptimeCacheTTL          = 10 * time.Second
	uptimeWindowSeconds     = 24 * 3600
	manualProbeTimeout      = 60 * time.Second
	maxRecentHeartbeats     = 24
	UptimeStatusUnknown     = -1
	UptimeStatusDown        = 0
	UptimeStatusUp          = 1
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
	GroupID      string              `json:"groupId,omitempty"`
	CategoryName string              `json:"categoryName"`
	Description  string              `json:"description,omitempty"`
	Monitors     []Monitor           `json:"monitors"`
	Models       []UptimeModelStatus `json:"models,omitempty"`
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
	case UptimeStatusPending:
		return "pending"
	case UptimeStatusMaintenance:
		return "maintenance"
	default:
		return "unknown"
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

func buildBuiltInUptimeResults(groups []console_setting.UptimeKumaGroup, probes []model.AdtyModelProbe) []UptimeGroupResult {
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

		models := make([]UptimeModelStatus, 0, len(group.Models))
		monitors := make([]Monitor, 0, len(group.Models))

		for _, modelName := range group.Models {
			rows := grouped[probeKey{groupID: groupKey, modelName: modelName}]
			if len(rows) == 0 {
				models = append(models, UptimeModelStatus{
					Model:       modelName,
					MonitorName: modelName,
					Status:      UptimeStatusUnknown,
					StatusLabel: uptimeStatusLabel(UptimeStatusUnknown),
					Uptime:      nil,
					Heartbeats:  []UptimeHeartbeat{},
				})
				monitors = append(monitors, Monitor{
					Name:       modelName,
					Uptime:     0,
					Status:     UptimeStatusUnknown,
					Heartbeats: []UptimeHeartbeat{},
				})
				continue
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
			status := UptimeStatusDown
			if latest.Status == model.AdtyModelProbeStatusUp {
				status = UptimeStatusUp
			}

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

			models = append(models, UptimeModelStatus{
				Model:         modelName,
				MonitorName:   modelName,
				Status:        status,
				StatusLabel:   uptimeStatusLabel(status),
				Uptime:        &uptimeVal,
				AvgPingMs:     avgPingPtr,
				LastCheckedAt: latest.CheckedAt,
				LastMessage:   latest.Message,
				Heartbeats:    heartbeats,
			})
			monitors = append(monitors, Monitor{
				Name:       modelName,
				Uptime:     uptimeVal,
				Status:     status,
				Heartbeats: heartbeats,
			})
		}

		results = append(results, UptimeGroupResult{
			GroupID:      group.GroupID,
			CategoryName: displayName,
			Description:  group.Description,
			Monitors:     monitors,
			Models:       models,
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

func filterUptimeResultsForSite(results []UptimeGroupResult, siteID int) []UptimeGroupResult {
	if siteID <= 0 || len(results) == 0 {
		return results
	}
	filtered := make([]UptimeGroupResult, 0, len(results))
	for _, item := range results {
		if item.GroupID != "" && !service.SiteGroupAllowed(siteID, item.GroupID) {
			continue
		}
		filtered = append(filtered, item)
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
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": filterUptimeResultsForSite(results, siteID)})
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
		since := time.Now().Unix() - uptimeWindowSeconds
		probes, err := model.ListAdtyModelProbesSince(since)
		if err != nil {
			common.SysError(common.LogText("list adty model probes: %s", err.Error()))
			return buildBuiltInUptimeResults(groups, nil), nil
		}
		results := buildBuiltInUptimeResults(groups, probes)

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
		filtered := filterUptimeResultsForSite(result.Val.([]UptimeGroupResult), siteID)
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": filtered})
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
