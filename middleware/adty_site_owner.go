package middleware

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// adtyOwnerDashboardAllowed enforces R-32 pure-admin isolation for subsite owner
// accounts: non-admin site owners cannot create/use personal API keys, access
// consumer referral flows, invoke retired self-use APIs, or access consumer
// usage logs. Margin top-up and site management APIs remain permitted.
func adtyOwnerDashboardAllowed(c *gin.Context, user *model.UserBase) bool {
	if user == nil || user.Role >= common.RoleAdminUser || user.StaffSiteId > 0 {
		return true
	}
	isOwner, err := service.AdtyIsSiteOwnerStrict(user.Id)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Service temporarily unavailable"))
		c.Abort()
		return false
	}
	if !isOwner {
		return true
	}
	fullPath := c.FullPath()
	rawPath := c.Request.URL.Path
	isBlocked := func(prefix string) bool {
		return rawPath == prefix || strings.HasPrefix(rawPath, prefix+"/") ||
			fullPath == prefix || strings.HasPrefix(fullPath, prefix+"/")
	}
	if isBlocked("/api/token") ||
		isBlocked("/api/user/referral") ||
		isBlocked("/api/adty/site-referral") ||
		isBlocked("/api/adty/site-commissions") ||
		isBlocked("/api/log/self") ||
		isBlocked("/api/mj/self") ||
		isBlocked("/api/task") ||
		isBlocked("/api/data/self") ||
		isBlocked("/api/adty/orders") ||
		rawPath == "/api/adty/site/wallets/earning-to-personal" ||
		rawPath == "/api/adty/site/wallets/self-use" ||
		isBlocked("/api/adty/site/self-use") {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Site owner accounts are management-only and cannot access consumer or API features"))
		c.Abort()
		return false
	}
	// Block non-margin personal top-up queries for site owners; margin top-up stays allowed.
	if (rawPath == "/api/adty/topup/info" || rawPath == "/api/adty/topup/pending" || rawPath == "/api/adty/topup/self") &&
		strings.TrimSpace(c.Query("business")) != model.AdtyBusinessOwnerMarginTopup {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Site owner accounts are management-only and cannot access consumer or API features"))
		c.Abort()
		return false
	}
	return true
}
