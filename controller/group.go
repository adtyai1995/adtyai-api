package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func GetGroups(c *gin.Context) {
	siteID := service.AdtyRequestSiteID(c)
	groupNames := make([]string, 0)
	for groupName := range ratio_setting.GetGroupRatioCopy() {
		if !service.SiteGroupAllowed(siteID, groupName) {
			continue
		}
		groupNames = append(groupNames, groupName)
	}
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
	userUsableGroups := service.GetUserUsableGroups(userGroup)
	for groupName := range ratio_setting.GetGroupRatioCopy() {
		if !service.SiteGroupAllowed(siteID, groupName) {
			continue
		}
		// UserUsableGroups contains the groups that the user can use
		if desc, ok := userUsableGroups[groupName]; ok {
			usableGroups[groupName] = map[string]any{
				"ratio": service.AdtyUserGroupRatio(userId, userGroup, groupName), // 二开：分站价格（任务 11.4）
				"desc":  desc,
			}
		}
	}
	if _, ok := userUsableGroups["auto"]; ok && (siteID == 0 || len(usableGroups) > 0) {
		usableGroups["auto"] = map[string]any{
			"ratio": "auto",
			"desc":  setting.GetUsableGroupDescription("auto"),
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    usableGroups,
	})
}
