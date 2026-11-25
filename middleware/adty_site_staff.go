package middleware

import (
	"net/http"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Dedicated employee accounts have an explicit dashboard allowlist. New owner,
// wallet, API-key, admin and PAT endpoints remain inaccessible by default.
func adtyStaffDashboardAllowed(c *gin.Context, user *model.UserBase, identity service.AuthIdentity) bool {
	if user.StaffSiteId == 0 {
		return true
	}
	if identity.SessionID == "" {
		common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtyStaffDenied)
		c.Abort()
		return false
	}
	staff, site, err := service.AdtyStaffAccess(user.Id, identity.UserAuthVersion, "")
	if err != nil {
		common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtyStaffDenied)
		c.Abort()
		return false
	}
	// Compare numeric site ids so a sub-site staff session is also rejected
	// on the main host (AdtySiteForRequest is nil, AdtyRequestSiteID = 0).
	if service.AdtyRequestSiteID(c) != site.Id {
		common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtyStaffDenied)
		c.Abort()
		return false
	}
	allowed := false
	switch c.Request.Method + " " + c.FullPath() {
	case "GET /api/adty/tickets", "GET /api/adty/tickets/:id", "GET /api/adty/tickets/:id/images/:imageId",
		"POST /api/adty/tickets/:id/status", "POST /api/adty/tickets/:id/replies", "POST /api/adty/tickets/:id/notes", "POST /api/adty/tickets/:id/images",
		"DELETE /api/adty/tickets/:id/images/:imageId":
		allowed = c.Query("scope") == "staff" && slices.Contains(service.AdtyStaffViewOf(*staff).Permissions, service.AdtyStaffTicketManage)
	case "GET /api/adty/telegram/me", "DELETE /api/adty/telegram/me",
		"POST /api/adty/telegram/bind/start", "POST /api/adty/telegram/bind/confirm",
		"POST /api/adty/telegram/unbind", "POST /api/adty/telegram/bind/unbind":
		allowed = slices.Contains(service.AdtyStaffViewOf(*staff).Permissions, service.AdtyStaffTicketManage)
	case "GET /api/adty/badges", "POST /api/adty/badges/seen", "GET /api/adty/staff/self", "GET /api/adty/staff/customers", "GET /api/adty/staff/customers/:id", "GET /api/adty/staff/customers/:id/topups", "GET /api/adty/staff/customers/:id/logs",
		"GET /api/adty/staff/customers/:id/orders", "GET /api/adty/staff/customers/:id/orders/:trade_no",
		"POST /api/adty/staff/customers/:id/password-reset", "POST /api/adty/staff/customers/:id/disable",
		"GET /api/user/self", "GET /api/verify/methods", "POST /api/verify", "GET /api/user/sessions", "DELETE /api/user/sessions/:sid", "POST /api/user/sessions/revoke-others",
		"GET /api/adty/notifications", "POST /api/adty/notifications/read", "GET /api/adty/notifications/announcements/:key", "POST /api/adty/notifications/hide",
		"GET /api/adty/notifications/todos", "POST /api/adty/notifications/todo/complete", "POST /api/adty/notifications/todo/in-progress":
		allowed = true
	}
	if !allowed {
		common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtyStaffDenied)
		c.Abort()
	}
	return allowed
}
