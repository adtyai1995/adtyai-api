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
	var tokenScopes [][]string
	if _, token := c.Get("access_token_legacy"); token {
		tokenScopes = append(tokenScopes, c.GetStringSlice("access_token_scopes"))
	}
	counts, err := service.CollectAdtySidebarBadges(userID, service.AdtySiteForRequest(c), tokenScopes...)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"counts": counts})
}
