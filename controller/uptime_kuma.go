package controller

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/console_setting"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

const (
	requestTimeout      = 30 * time.Second
	httpTimeout         = 10 * time.Second
	uptimeCacheTTL      = 15 * time.Second
	uptimeKeySuffix     = "_24"
	maxRecentHeartbeats = 24
	apiStatusPath       = "/api/status-page/"
	apiHeartbeatPath    = "/api/status-page/heartbeat/"

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
	Model       string            `json:"model"`
	MonitorName string            `json:"monitorName,omitempty"`
	Status      int               `json:"status"`
	StatusLabel string            `json:"statusLabel"`
	Uptime      *float64          `json:"uptime"`
	Heartbeats  []UptimeHeartbeat `json:"heartbeats"`
}

type UptimeGroupResult struct {
	GroupID      string              `json:"groupId,omitempty"`
	CategoryName string              `json:"categoryName"`
	Description  string              `json:"description,omitempty"`
	Monitors     []Monitor           `json:"monitors"`
	Models       []UptimeModelStatus `json:"models,omitempty"`
}

type uptimeStatusPageResponse struct {
	PublicGroupList []struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		MonitorList []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"monitorList"`
	} `json:"publicGroupList"`
}

type uptimeKumaStatusCache struct {
	sync.Mutex
	configuration string
	expiresAt     time.Time
	results       []UptimeGroupResult
	refresh       singleflight.Group
}

var publicUptimeStatusCache uptimeKumaStatusCache

func (cache *uptimeKumaStatusCache) get(configuration string) ([]UptimeGroupResult, bool) {
	cache.Lock()
	defer cache.Unlock()
	if cache.configuration != configuration || !time.Now().Before(cache.expiresAt) {
		return nil, false
	}
	return cache.results, true
}

func getAndDecode(ctx context.Context, client *http.Client, url string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.New("non-200 status")
	}

	return common.DecodeJson(io.LimitReader(resp.Body, 2<<20), dest)
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

func parseHeartbeatTime(value string) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, trimmed); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func extractRecentHeartbeats(raw []UptimeHeartbeat) (int, []UptimeHeartbeat) {
	if len(raw) == 0 {
		return UptimeStatusUnknown, nil
	}
	reverseOrder := false
	if len(raw) >= 2 {
		if first, ok1 := parseHeartbeatTime(raw[0].Time); ok1 {
			if last, ok2 := parseHeartbeatTime(raw[len(raw)-1].Time); ok2 && first.After(last) {
				reverseOrder = true
			}
		}
	}
	count := min(len(raw), maxRecentHeartbeats)
	recent := make([]UptimeHeartbeat, count)
	if reverseOrder {
		for i := range count {
			recent[count-1-i] = raw[i]
		}
	} else {
		copy(recent, raw[len(raw)-count:])
	}
	return recent[len(recent)-1].Status, recent
}

func buildUptimeModelStatuses(group console_setting.UptimeKumaGroup, monitors []Monitor, hasUptime []bool) []UptimeModelStatus {
	if len(group.Models) == 0 {
		return nil
	}

	byExactName := make(map[string]int, len(monitors))
	byLowerName := make(map[string]int, len(monitors))
	for i, m := range monitors {
		if _, exists := byExactName[m.Name]; !exists {
			byExactName[m.Name] = i
		}
		lower := strings.ToLower(strings.TrimSpace(m.Name))
		if lower != "" {
			if _, exists := byLowerName[lower]; !exists {
				byLowerName[lower] = i
			}
		}
	}

	statuses := make([]UptimeModelStatus, 0, len(group.Models))
	for _, modelName := range group.Models {
		matchedIdx := -1
		if mappedName := strings.TrimSpace(group.MonitorMap[modelName]); mappedName != "" {
			if idx, ok := byExactName[mappedName]; ok {
				matchedIdx = idx
			} else if idx, ok := byLowerName[strings.ToLower(mappedName)]; ok {
				matchedIdx = idx
			}
		}
		if matchedIdx < 0 {
			if idx, ok := byExactName[modelName]; ok {
				matchedIdx = idx
			} else if idx, ok := byLowerName[strings.ToLower(modelName)]; ok {
				matchedIdx = idx
			}
		}

		if matchedIdx < 0 {
			statuses = append(statuses, UptimeModelStatus{
				Model:       modelName,
				Status:      UptimeStatusUnknown,
				StatusLabel: uptimeStatusLabel(UptimeStatusUnknown),
				Uptime:      nil,
				Heartbeats:  []UptimeHeartbeat{},
			})
			continue
		}

		m := monitors[matchedIdx]
		var uptimePtr *float64
		if matchedIdx < len(hasUptime) && hasUptime[matchedIdx] {
			val := m.Uptime
			uptimePtr = &val
		}
		heartbeats := m.Heartbeats
		if heartbeats == nil {
			heartbeats = []UptimeHeartbeat{}
		}
		statuses = append(statuses, UptimeModelStatus{
			Model:       modelName,
			MonitorName: m.Name,
			Status:      m.Status,
			StatusLabel: uptimeStatusLabel(m.Status),
			Uptime:      uptimePtr,
			Heartbeats:  heartbeats,
		})
	}
	return statuses
}

func fetchGroupData(ctx context.Context, client *http.Client, group console_setting.UptimeKumaGroup, globalBaseURL string) UptimeGroupResult {
	result := UptimeGroupResult{
		GroupID:      group.GroupID,
		CategoryName: group.CategoryName,
		Description:  group.Description,
		Monitors:     []Monitor{},
	}

	effectiveURL := strings.TrimSpace(group.URL)
	if effectiveURL == "" {
		effectiveURL = strings.TrimSpace(globalBaseURL)
	}
	slug := strings.TrimSpace(group.Slug)
	if effectiveURL == "" || slug == "" || console_setting.ValidateUptimeKumaBaseURL(effectiveURL) != nil || console_setting.ValidateUptimeKumaSlug(slug) != nil {
		result.Models = buildUptimeModelStatuses(group, nil, nil)
		return result
	}

	baseURL := strings.TrimRight(effectiveURL, "/")

	var statusData uptimeStatusPageResponse
	var heartbeatData struct {
		HeartbeatList map[string][]UptimeHeartbeat `json:"heartbeatList"`
		UptimeList    map[string]*float64          `json:"uptimeList"`
	}

	// The status and heartbeat endpoints are independent; one failure must not
	// cancel the other request after it has already been dispatched.
	var g errgroup.Group
	g.Go(func() error {
		return getAndDecode(ctx, client, baseURL+apiStatusPath+slug, &statusData)
	})
	g.Go(func() error {
		return getAndDecode(ctx, client, baseURL+apiHeartbeatPath+slug, &heartbeatData)
	})

	if g.Wait() != nil {
		result.Models = buildUptimeModelStatuses(group, nil, nil)
		return result
	}

	hasUptime := make([]bool, 0)
	for _, pg := range statusData.PublicGroupList {
		if len(pg.MonitorList) == 0 {
			continue
		}

		for _, m := range pg.MonitorList {
			// Group names often identify channels. The public page does not
			// show them, and this unauthenticated response must not either.
			monitor := Monitor{
				Name:   m.Name,
				Status: UptimeStatusUnknown,
			}

			monitorID := strconv.Itoa(m.ID)
			if heartbeats, exists := heartbeatData.HeartbeatList[monitorID]; exists && len(heartbeats) > 0 {
				monitor.Status, monitor.Heartbeats = extractRecentHeartbeats(heartbeats)
			}

			uptimeFound := false
			if uptimePtr, exists := heartbeatData.UptimeList[monitorID+uptimeKeySuffix]; exists && uptimePtr != nil && !math.IsNaN(*uptimePtr) && !math.IsInf(*uptimePtr, 0) {
				if len(monitor.Heartbeats) > 0 || *uptimePtr > 0 {
					monitor.Uptime = *uptimePtr
					uptimeFound = true
				}
			}

			result.Monitors = append(result.Monitors, monitor)
			hasUptime = append(hasUptime, uptimeFound)
		}
	}

	result.Models = buildUptimeModelStatuses(group, result.Monitors, hasUptime)
	return result
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
	globalBaseURL := strings.TrimSpace(settings.UptimeKumaBaseURL)
	configuration := globalBaseURL + "\x00" + settings.UptimeKumaGroups
	groups := console_setting.ParseUptimeKumaGroups(settings.UptimeKumaGroups)
	if len(groups) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": []UptimeGroupResult{}})
		return
	}

	siteID := resolveUptimeSiteID(c)

	if results, ok := publicUptimeStatusCache.get(configuration); ok {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": filterUptimeResultsForSite(results, siteID)})
		return
	}

	refresh := publicUptimeStatusCache.refresh.DoChan(configuration, func() (any, error) {
		if results, ok := publicUptimeStatusCache.get(configuration); ok {
			return results, nil
		}
		// A disconnected visitor must not cancel work shared by other callers.
		// The independent deadline still bounds every refresh, including failures.
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		client := service.GetVerifiedNoRedirectSSRFProtectedHTTPClient(httpTimeout)
		results := make([]UptimeGroupResult, len(groups))
		g, gCtx := errgroup.WithContext(ctx)
		g.SetLimit(4)
		for i, group := range groups {
			g.Go(func() error {
				results[i] = fetchGroupData(gCtx, client, group, globalBaseURL)
				return nil
			})
		}
		_ = g.Wait()
		publicUptimeStatusCache.Lock()
		publicUptimeStatusCache.configuration = configuration
		publicUptimeStatusCache.results = results
		publicUptimeStatusCache.expiresAt = time.Now().Add(uptimeCacheTTL)
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

func GetUptimeKumaMonitors(c *gin.Context) {
	rawURL := strings.TrimSpace(c.Query("url"))
	if rawURL == "" {
		rawURL = strings.TrimSpace(console_setting.GetConsoleSetting().UptimeKumaBaseURL)
	}
	if rawURL == "" {
		common.ApiErrorT(c, "Uptime Kuma URL is required")
		return
	}
	if err := console_setting.ValidateUptimeKumaBaseURL(rawURL); err != nil {
		common.ApiError(c, err)
		return
	}

	slug := strings.TrimSpace(c.Query("slug"))
	if err := console_setting.ValidateUptimeKumaSlug(slug); err != nil {
		common.ApiError(c, err)
		return
	}

	baseURL := strings.TrimRight(rawURL, "/")
	ctx, cancel := context.WithTimeout(c.Request.Context(), httpTimeout)
	defer cancel()

	client := service.GetVerifiedNoRedirectSSRFProtectedHTTPClient(httpTimeout)
	var statusData uptimeStatusPageResponse
	if err := getAndDecode(ctx, client, baseURL+apiStatusPath+slug, &statusData); err != nil {
		common.ApiErrorT(c, "Failed to fetch Uptime Kuma monitors: {{error}}", map[string]any{"error": err.Error()})
		return
	}

	names := make([]string, 0)
	seen := make(map[string]struct{})
	for _, pg := range statusData.PublicGroupList {
		for _, m := range pg.MonitorList {
			name := strings.TrimSpace(m.Name)
			if name == "" {
				continue
			}
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}

	common.ApiSuccess(c, names)
}
