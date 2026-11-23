package controller

import (
	"net/http"

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
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}
