package controller

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

var (
	queryPerfMetricsSummary = perfmetrics.QuerySummaryAll
	queryPerfMetrics        = perfmetrics.Query
)

const perfMetricsLoadFailed = "Failed to load performance metrics"

func reportPerfMetricsQueryError(c *gin.Context, err error, what string) {
	common.SysError(common.LogText("query performance metrics %s: %s", what, err.Error()))
	common.ApiErrorStatus(c, http.StatusInternalServerError, common.NewMessage(perfMetricsLoadFailed))
}

func perfMetricsScope(c *gin.Context) (int, []string, []string) {
	siteID := service.AdtyRequestSiteID(c)
	userGroup := ""
	if userId, exists := c.Get("id"); exists {
		if uid, ok := userId.(int); ok && uid > 0 {
			if user, err := model.GetUserCache(uid); err == nil && user != nil {
				userGroup = user.Group
				if siteID == 0 && user.SiteId > 0 {
					siteID = user.SiteId
				}
			}
		}
	}
	usable := service.GetUserUsableGroups(userGroup)
	concreteGroups := make([]string, 0, len(usable))
	for g := range ratio_setting.GetGroupRatioCopy() {
		if g == "" || g == "auto" || g == "all" {
			continue
		}
		if _, ok := usable[g]; !ok {
			continue
		}
		if !service.SiteGroupAllowed(siteID, g) {
			continue
		}
		concreteGroups = append(concreteGroups, g)
	}
	slices.Sort(concreteGroups)
	allowedGroups := append([]string{}, concreteGroups...)
	if siteID == 0 {
		if _, ok := usable["auto"]; ok {
			allowedGroups = append(allowedGroups, "auto")
		}
	}
	return siteID, concreteGroups, allowedGroups
}

func perfMetricsModelVisible(modelName string, concreteGroups []string) bool {
	if strings.TrimSpace(modelName) == "" || len(concreteGroups) == 0 {
		return false
	}
	if !model.CatalogModelCallable(modelName) {
		return false
	}
	for _, g := range concreteGroups {
		if service.GroupModelAllowed(g, modelName) {
			return true
		}
	}
	return false
}

func GetPerfMetricsSummary(c *gin.Context) {
	hours := 24
	if rawHours := c.Query("hours"); rawHours != "" {
		if parsed, err := strconv.Atoi(rawHours); err == nil {
			hours = parsed
		}
	}

	recentModels := c.QueryArray("recent_model")
	if len(recentModels) > perfmetrics.MaxRecentModels {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	_, concreteGroups, activeGroups := perfMetricsScope(c)
	if len(activeGroups) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": perfmetrics.SummaryAllResult{
				Models: []perfmetrics.ModelSummary{},
			},
		})
		return
	}
	visibleRecent := make([]string, 0, len(recentModels))
	for _, name := range recentModels {
		if perfMetricsModelVisible(name, concreteGroups) {
			visibleRecent = append(visibleRecent, name)
		}
	}
	result, err := queryPerfMetricsSummary(hours, activeGroups, visibleRecent...)
	if err != nil {
		reportPerfMetricsQueryError(c, err, "summary")
		return
	}
	filteredModels := make([]perfmetrics.ModelSummary, 0, len(result.Models))
	for _, item := range result.Models {
		if perfMetricsModelVisible(item.ModelName, concreteGroups) {
			filteredModels = append(filteredModels, item)
		}
	}
	result.Models = filteredModels
	for name := range result.RecentSuccessSeries {
		if !perfMetricsModelVisible(name, concreteGroups) {
			delete(result.RecentSuccessSeries, name)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

func GetPerfMetrics(c *gin.Context) {
	modelName := c.Query("model")
	if modelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "model is required",
		})
		return
	}

	hours := 24
	if rawHours := c.Query("hours"); rawHours != "" {
		if parsed, err := strconv.Atoi(rawHours); err == nil {
			hours = parsed
		}
	}

	_, concreteGroups, allowedGroups := perfMetricsScope(c)
	if !perfMetricsModelVisible(modelName, concreteGroups) {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": perfmetrics.QueryResult{
				ModelName: modelName,
				Series:    []perfmetrics.BucketPoint{},
				Groups:    []perfmetrics.GroupResult{},
			},
		})
		return
	}
	modelGroups := make([]string, 0, len(allowedGroups))
	for _, g := range allowedGroups {
		if g == "auto" || service.GroupModelAllowed(g, modelName) {
			modelGroups = append(modelGroups, g)
		}
	}
	requestedGroup := strings.TrimSpace(c.Query("group"))
	if requestedGroup != "" && requestedGroup != "all" && !slices.Contains(modelGroups, requestedGroup) {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": perfmetrics.QueryResult{
				ModelName: modelName,
				Series:    []perfmetrics.BucketPoint{},
				Groups:    []perfmetrics.GroupResult{},
			},
		})
		return
	}

	result, err := queryPerfMetrics(perfmetrics.QueryParams{
		Model:         modelName,
		Group:         requestedGroup,
		Hours:         hours,
		AllowedGroups: modelGroups,
	})
	if err != nil {
		reportPerfMetricsQueryError(c, err, "model")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}
