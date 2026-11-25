package controller

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetRankings(c *gin.Context) {
	siteID := service.AdtyRequestSiteID(c)
	if siteID == 0 {
		if userId, exists := c.Get("id"); exists {
			if uid, ok := userId.(int); ok && uid > 0 {
				if user, err := model.GetUserCache(uid); err == nil && user != nil && user.SiteId > 0 {
					siteID = user.SiteId
				}
			}
		}
	}
	result, err := service.GetRankingsSnapshotForSite(c.DefaultQuery("period", "week"), siteID)
	if err != nil {
		var msg *common.Message
		if errors.As(err, &msg) {
			common.ApiErrorStatus(c, http.StatusBadRequest, msg)
			return
		}
		if strings.Contains(strings.ToLower(err.Error()), "period") {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
			return
		}
		common.SysError(common.LogText("failed to load rankings: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, common.NewMessage("Operation failed"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}
