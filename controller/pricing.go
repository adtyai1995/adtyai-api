package controller

import (
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func filterPricingByUsableGroups(pricing []model.Pricing, usableGroup map[string]string, siteID int) []model.Pricing {
	if len(pricing) == 0 {
		return pricing
	}
	if len(usableGroup) == 0 {
		return []model.Pricing{}
	}

	concreteUsable := make([]string, 0, len(usableGroup))
	for group := range usableGroup {
		if group == "" || group == "auto" || group == "all" {
			continue
		}
		if !service.SiteGroupAllowed(siteID, group) {
			continue
		}
		concreteUsable = append(concreteUsable, group)
	}
	slices.Sort(concreteUsable)
	if len(concreteUsable) == 0 {
		return []model.Pricing{}
	}

	filtered := make([]model.Pricing, 0, len(pricing))
	for _, item := range pricing {
		var candidates []string
		if common.StringsContains(item.EnableGroup, "all") {
			candidates = concreteUsable
		} else {
			seen := make(map[string]struct{}, len(item.EnableGroup))
			candidates = make([]string, 0, len(item.EnableGroup))
			for _, group := range item.EnableGroup {
				if group == "" || group == "auto" || group == "all" {
					continue
				}
				if _, ok := usableGroup[group]; !ok {
					continue
				}
				if !service.SiteGroupAllowed(siteID, group) {
					continue
				}
				if _, dup := seen[group]; dup {
					continue
				}
				seen[group] = struct{}{}
				candidates = append(candidates, group)
			}
		}

		allowedGroups := make([]string, 0, len(candidates))
		for _, group := range candidates {
			if service.GroupModelAllowed(group, item.ModelName) {
				allowedGroups = append(allowedGroups, group)
			}
		}
		if len(allowedGroups) == 0 {
			continue
		}
		copied := item
		copied.EnableGroup = allowedGroups
		filtered = append(filtered, copied)
	}
	return filtered
}

func GetPricing(c *gin.Context) {
	pricing, vendors, supportedEndpoints := model.GetPricingBundle()
	siteID := service.AdtyRequestSiteID(c)
	userId, exists := c.Get("id")
	usableGroup := map[string]string{}
	groupRatio := map[string]float64{}
	maps.Copy(groupRatio, ratio_setting.GetGroupRatioCopy())
	var group string
	if exists {
		user, err := model.GetUserCache(userId.(int))
		if err == nil {
			group = user.Group
			if siteID == 0 && user.SiteId > 0 {
				siteID = user.SiteId
			}
			for g := range groupRatio {
				ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
				if ok {
					groupRatio[g] = ratio
				}
			}
		}
	}
	rawUsable := service.GetUserUsableGroups(group)
	for g, desc := range rawUsable {
		if g == "auto" {
			continue
		}
		if !service.SiteGroupAllowed(siteID, g) {
			continue
		}
		usableGroup[g] = desc
	}
	if desc, ok := rawUsable["auto"]; ok && (siteID == 0 || len(usableGroup) > 0) {
		usableGroup["auto"] = desc
	}

	requested := strings.TrimSpace(c.Query("group"))
	if requested != "" && requested != "all" {
		if requested == "auto" {
			if _, ok := usableGroup["auto"]; !ok {
				common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
				return
			}
		} else if !service.SiteGroupAllowed(siteID, requested) {
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
			return
		}
	}

	pricing = filterPricingByUsableGroups(pricing, usableGroup, siteID)
	adjustGroup := group
	if adjustGroup != "" && adjustGroup != "auto" && !service.SiteGroupAllowed(siteID, adjustGroup) {
		adjustGroup = ""
	}
	if requested != "" && requested != "all" {
		if _, ok := usableGroup[requested]; ok {
			adjustGroup = requested
		}
	}
	for g := range groupRatio {
		if _, ok := usableGroup[g]; !ok || !service.SiteGroupAllowed(siteID, g) {
			delete(groupRatio, g)
		}
	}
	pricing, err := service.AdtySitePriceList(c, groupRatio, adjustGroup, pricing)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	rawAutoGroups := service.GetUserAutoGroup(group)
	autoGroups := make([]string, 0, len(rawAutoGroups))
	for _, g := range rawAutoGroups {
		if service.SiteGroupAllowed(siteID, g) {
			autoGroups = append(autoGroups, g)
		}
	}

	c.JSON(200, gin.H{
		"success":            true,
		"data":               pricing,
		"vendors":            vendors,
		"group_ratio":        groupRatio,
		"usable_group":       usableGroup,
		"supported_endpoint": supportedEndpoints,
		"auto_groups":        autoGroups,
		"pricing_version":    "a42d372ccf0b5dd13ecf71203521f9d2",
	})
}

func ResetModelRatio(c *gin.Context) {
	defaultStr := ratio_setting.DefaultModelRatio2JSONString()
	err := model.UpdateOption("ModelRatio", defaultStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccessT(c, "Model ratios reset", nil)
}
