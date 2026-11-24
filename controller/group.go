package controller

import (
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func GetGroups(c *gin.Context) {
	siteID := service.AdtyRequestSiteID(c)
	seen := make(map[string]struct{})
	for groupName := range ratio_setting.GetGroupRatioCopy() {
		groupName = strings.TrimSpace(groupName)
		if groupName != "" && groupName != "auto" {
			seen[groupName] = struct{}{}
		}
	}
	if model.DB != nil {
		if adtyGroups, err := model.ListAdtyGroups(false); err == nil {
			for _, g := range adtyGroups {
				id := strings.TrimSpace(g.ID)
				if id != "" && id != "auto" {
					seen[id] = struct{}{}
				}
			}
		} else {
			common.SysError(common.LogText("list active adty groups failed: %s", err.Error()))
		}
	}
	groupNames := make([]string, 0, len(seen))
	for groupName := range seen {
		if !service.SiteGroupAllowed(siteID, groupName) {
			continue
		}
		groupNames = append(groupNames, groupName)
	}
	slices.Sort(groupNames)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    groupNames,
	})
}

func GetUserGroups(c *gin.Context) {
	siteID := service.AdtyRequestSiteID(c)
	usableGroups := make(map[string]map[string]any)
	userGroup := ""
	userId := c.GetInt("id")
	if userId > 0 {
		userGroup, _ = model.GetUserGroup(userId, false)
		if siteID == 0 {
			if user, err := model.GetUserCache(userId); err == nil && user != nil && user.SiteId > 0 {
				siteID = user.SiteId
			}
		}
	}
	groupRatios := make(map[string]float64)
	for groupName := range ratio_setting.GetGroupRatioCopy() {
		groupName = strings.TrimSpace(groupName)
		if groupName == "" || groupName == "auto" {
			continue
		}
		if !service.SiteGroupAllowed(siteID, groupName) {
			continue
		}
		groupRatios[groupName] = service.GetUserGroupRatio(userGroup, groupName)
	}
	if err := service.AdtyApplySitePricing(c, groupRatios); err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, err)
		return
	}
	for groupName, ratio := range groupRatios {
		usableGroups[groupName] = map[string]any{
			"ratio": ratio,
			"desc":  setting.GetUsableGroupDescription(groupName),
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    usableGroups,
	})
}
