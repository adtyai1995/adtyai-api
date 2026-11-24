package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// GetAdtySidebarBadges returns confirmed, role-scoped sidebar badge counts.
// Opening this endpoint does not clear any todo. A query failure is an
// error; it is never a successful empty or stale payload.
func GetAdtySidebarBadges(c *gin.Context) {
	userID := c.GetInt("id")
	if userID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	counts, err := service.CollectAdtySidebarBadges(userID, service.AdtySiteForRequest(c), referralBadgeTokenScopes(c)...)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"counts": counts})
}

// PostAdtySidebarBadgesSeen marks one or more sidebar badge keys as seen so
// opening the corresponding menu immediately clears its badge until newer
// items arrive (R-32 #6).
func PostAdtySidebarBadgesSeen(c *gin.Context) {
	userID := c.GetInt("id")
	if userID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		Keys []string `json:"keys"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil || len(body.Keys) == 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.MarkAdtySidebarBadgesSeen(userID, body.Keys); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"seen": true})
}

func referralBadgeTokenScopes(c *gin.Context) [][]string {
	if legacy, token := c.Get("access_token_legacy"); token {
		if legacy == true {
			return [][]string{nil}
		}
		return [][]string{c.GetStringSlice("access_token_scopes")}
	}
	return nil
}
