package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

// SetAdtyRouter 挂分组路由。令牌规则在注册处理函数前写入，覆盖测试才能看到。
func SetAdtyRouter(apiRouter *gin.RouterGroup) {
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/groups/display", "any", "")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/groups", "scope", "option:read")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/groups", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("PUT", "/api/adty/admin/groups/:id", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("DELETE", "/api/adty/admin/groups/:id", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/groups/:id/migrate", "scope", "option:write")

	apiRouter.GET("/adty/groups/display", middleware.TryUserAuth(), controller.GetAdtyGroupDisplay)
	apiRouter.GET("/adty/admin/groups", middleware.RootAuth(), controller.GetAdtyGroups)
	apiRouter.POST("/adty/admin/groups", middleware.RootAuth(), controller.PostAdtyGroup)
	apiRouter.PUT("/adty/admin/groups/:id", middleware.RootAuth(), controller.UpdateAdtyGroup)
	apiRouter.DELETE("/adty/admin/groups/:id", middleware.RootAuth(), controller.DeleteAdtyGroup)
	apiRouter.POST("/adty/admin/groups/:id/migrate", middleware.RootAuth(), controller.MigrateAdtyGroup)

	registerAdtyTopUpRoutes(apiRouter)
	registerAdtyFinanceRoutes(apiRouter)
	registerAdtyBEpusdtRoutes(apiRouter)
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/internal/flush-batch", "session", "")
	apiRouter.POST("/adty/internal/flush-batch", controller.FlushAdtyBatchUpdates)
	registerAdtyEpayRoutes(apiRouter)
	registerAdtyReferralRoutes(apiRouter)
	registerAdtySiteRoutes(apiRouter)
	registerAdtyLegalRoutes(apiRouter)
	registerAdtyResellerRoutes(apiRouter)
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/notifications", "scope", "profile:read")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/notifications/read", "scope", "profile:write")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/notifications/hide", "scope", "profile:write")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/notifications/todo/complete", "scope", "profile:write")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/notifications/todo/in-progress", "scope", "profile:write")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/notifications/todos", "scope", "profile:read")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/notifications/announcements/:key", "scope", "profile:read")
	notifications := apiRouter.Group("/adty/notifications", middleware.UserAuth())
	notifications.GET("/announcements/:key", controller.GetAdtyAnnouncement)
	notifications.GET("", controller.GetAdtyNotifications)
	notifications.POST("/read", controller.ReadAdtyNotifications)
	notifications.POST("/hide", controller.PostAdtyNotificationHide)
	notifications.POST("/todo/complete", controller.PostAdtyNotificationTodoComplete)
	notifications.POST("/todo/in-progress", controller.PostAdtyNotificationTodoProgress)
	notifications.GET("/todos", controller.GetAdtyNotificationTodos)
	for _, route := range []struct{ method, path, scope string }{
		{http.MethodGet, "", "read"}, {http.MethodPost, "", "write"},
		{http.MethodPut, "/:id", "write"}, {http.MethodDelete, "/:id", "write"},
	} {
		rootPath, ownerPath := "/adty/admin/notifications"+route.path, "/adty/site/notifications"+route.path
		middleware.DeclareAdtyAccessTokenRoute(route.method, "/api"+rootPath, "scope", "option:"+route.scope)
		middleware.DeclareAdtyAccessTokenRoute(route.method, "/api"+ownerPath, "scope", service.AccessTokenResourceSiteOwnerSetting+":"+route.scope)
		handler := controller.WriteAdtyManagedNotification
		if route.method == http.MethodGet {
			handler = controller.GetAdtyManagedNotifications
		}
		apiRouter.Handle(route.method, rootPath, middleware.RootAuth(), func(c *gin.Context) { c.Set("notification_root", true) }, handler)
		if route.method == http.MethodGet {
			apiRouter.Handle(route.method, ownerPath, middleware.UserAuth(), handler)
		} else {
			apiRouter.Handle(route.method, ownerPath, middleware.UserAuth(), middleware.UserCriticalRateLimit("CT:site-notification"), handler)
		}
	}

	// 余额流水（任务 7.2）
	users := apiRouter.Group("/adty/admin/users", middleware.AdminAuth())
	handlePermissionRoute(users, http.MethodGet, "/:id/ledger", authz.AdtyUserRead, controller.GetAdtyUserLedger)
	// 按用户的请求限制（任务 7.4）
	handlePermissionRoute(users, http.MethodGet, "/:id/limits", authz.AdtyUserRead, controller.GetAdtyUserLimits)
	handlePermissionRoute(users, http.MethodPut, "/:id/limits", authz.AdtyUserWrite, controller.PutAdtyUserLimits)
	registerAdtyWorkspaceRoutes(apiRouter)
}

func registerAdtyWorkspaceRoutes(apiRouter *gin.RouterGroup) {
	for _, route := range []struct{ method, path, scope string }{
		// R1-016: High-risk team administration routes require browser session, never personal wallet:write PATs.
		{http.MethodPost, "/api/adty/teams/:id/close", "session"},
		{http.MethodPost, "/api/adty/teams/:id/profile", "session"},
		{http.MethodPost, "/api/adty/teams/:id/topups", "wallet:write"},
		{http.MethodPost, "/api/adty/teams/:id/topups/:trade_no/resume", "wallet:write"},
		{http.MethodGet, "/api/adty/teams/:id/topups", "wallet:read"},
		{http.MethodPost, "/api/adty/teams/:id/leave", "session"},
		{http.MethodPost, "/api/adty/teams/:id/employees", "session"},
		{http.MethodPost, "/api/adty/teams/:id/members/:userId/enable", "session"},
		{http.MethodPost, "/api/adty/teams/:id/members/:userId/disable", "session"},
		{http.MethodPost, "/api/adty/teams/:id/members/:userId/delete", "session"},
		{http.MethodPost, "/api/adty/teams/:id/members/:userId/password", "session"},
		{http.MethodPost, "/api/adty/teams/:id/keys", "session"},
		{http.MethodPost, "/api/adty/teams/:id/keys/:tokenId/revoke", "session"},
		{http.MethodPost, "/api/adty/teams/:id/transfer", "session"},
		{http.MethodGet, "/api/adty/telegram/me", "profile:read"},
		{http.MethodDelete, "/api/adty/telegram/me", "profile:write"},
		{http.MethodPost, "/api/adty/telegram/bind/start", "profile:write"},
		{http.MethodPost, "/api/adty/telegram/bind/confirm", "profile:write"},
		{http.MethodPost, "/api/adty/telegram/unbind", "profile:write"},
		{http.MethodPost, "/api/adty/telegram/bind/unbind", "profile:write"},
		{http.MethodGet, "/api/adty/site/telegram", service.AccessTokenResourceSiteOwnerSetting + ":read"},
		{http.MethodDelete, "/api/adty/site/telegram", service.AccessTokenResourceSiteOwnerSetting + ":write"},
		{http.MethodPost, "/api/adty/site/telegram/bind/start", service.AccessTokenResourceSiteOwnerSetting + ":write"},
		{http.MethodPost, "/api/adty/site/telegram/bind/confirm", service.AccessTokenResourceSiteOwnerSetting + ":write"},
		{http.MethodPost, "/api/adty/site/telegram/unbind", service.AccessTokenResourceSiteOwnerSetting + ":write"},
		{http.MethodPost, "/api/adty/site/telegram/bind/unbind", service.AccessTokenResourceSiteOwnerSetting + ":write"},
		{http.MethodGet, "/api/adty/admin/telegram/bots", "option:read"},
		{http.MethodPut, "/api/adty/admin/telegram/bots/:purpose", "option:write"},
		{http.MethodPost, "/api/adty/admin/telegram/bots/:purpose/test", "option:write"},
		{http.MethodPost, "/api/adty/telegram/webhook/:purpose/:secret", "session"},
		{http.MethodGet, "/api/adty/badges", "profile:read"},
		{http.MethodPost, "/api/adty/badges/seen", "profile:write"},
		{http.MethodGet, "/api/adty/tickets", "profile:read"},
		{http.MethodGet, "/api/adty/tickets/request-audience", "profile:read"},
		{http.MethodPost, "/api/adty/tickets", "profile:write"},
		{http.MethodGet, "/api/adty/tickets/:id", "profile:read"},
		{http.MethodPost, "/api/adty/tickets/:id/replies", "profile:write"},
		{http.MethodPost, "/api/adty/tickets/:id/notes", "profile:write"},
		{http.MethodPost, "/api/adty/tickets/:id/status", "profile:write"},
		{http.MethodPost, "/api/adty/tickets/:id/images", "profile:write"},
		{http.MethodGet, "/api/adty/tickets/:id/images/:imageId", "profile:read"},
		{http.MethodDelete, "/api/adty/tickets/:id/images/:imageId", "profile:write"},
		{http.MethodPut, "/api/adty/admin/ticket-image-policy", "option:write"},
		{http.MethodGet, "/api/adty/admin/translation", "option:read"},
		{http.MethodPut, "/api/adty/admin/translation", "option:write"},
		{http.MethodPost, "/api/adty/admin/translation/test", "option:write"},
		{http.MethodPost, "/api/adty/admin/translation/run", "option:write"},
		{http.MethodGet, "/api/adty/admin/translation/usage", "option:read"},
		{http.MethodGet, "/api/adty/admin/translation/tasks", "option:read"},
		{http.MethodPost, "/api/adty/admin/translation/retry", "option:write"},
		{http.MethodPost, "/api/adty/admin/translation/complete", "option:write"},
		{http.MethodPut, "/api/adty/admin/translation/edit", "option:write"},
		{http.MethodGet, "/api/adty/admin/translation/glossary", "option:read"},
		{http.MethodPost, "/api/adty/admin/translation/glossary", "option:write"},
		{http.MethodPut, "/api/adty/admin/translation/glossary/:id", "option:write"},
		{http.MethodDelete, "/api/adty/admin/translation/glossary/:id", "option:write"},
		{http.MethodGet, "/api/adty/groups/health", "api_key:read"},
		{http.MethodGet, "/api/adty/admin/group-health", "option:read"},
		{http.MethodPut, "/api/adty/admin/group-health", "option:write"},
		{http.MethodPost, "/api/adty/admin/group-health/probe", "option:write"},
	} {
		kind := "scope"
		scope := route.scope
		if route.scope == "session" {
			kind, scope = "session", ""
		}
		middleware.DeclareAdtyAccessTokenRoute(route.method, route.path, kind, scope)
	}
	apiRouter.POST("/adty/teams/:id/close", middleware.UserAuth(), controller.PostAdtyTeamClose)
	apiRouter.POST("/adty/teams/:id/profile", middleware.UserAuth(), controller.PostAdtyTeamProfile)
	// 团队充值下单同样是建单入口：每张单都会真实打一次支付网关，且 trade_no 由客户端的
	// Idempotency-Key 决定，换一个 key 就是一张新单。必须和 epay/pay 一样按用户限速
	// （ISS-R4-0127）。
	apiRouter.POST("/adty/teams/:id/topups", middleware.UserAuth(), middleware.UserCriticalRateLimit("CT:pay-team"), controller.PostAdtyTeamTopup)
	apiRouter.POST("/adty/teams/:id/topups/:trade_no/resume", middleware.UserAuth(), middleware.UserCriticalRateLimit("CT:pay-team"), controller.ResumeAdtyTeamTopup)
	apiRouter.GET("/adty/teams/:id/topups", middleware.UserAuth(), controller.GetAdtyTeamTopups)
	apiRouter.POST("/adty/teams/:id/leave", middleware.UserAuth(), controller.PostAdtyTeamLeave)
	apiRouter.POST("/adty/teams/:id/employees", middleware.UserAuth(), middleware.UserCriticalRateLimit("CT:team-employee"), middleware.AnonymousRequestBodyLimit(), controller.PostAdtyTeamEmployee)
	apiRouter.POST("/adty/teams/:id/members/:userId/enable", middleware.UserAuth(), controller.PostAdtyTeamEnableMember)
	apiRouter.POST("/adty/teams/:id/members/:userId/disable", middleware.UserAuth(), controller.PostAdtyTeamDisableMember)
	apiRouter.POST("/adty/teams/:id/members/:userId/delete", middleware.UserAuth(), controller.PostAdtyTeamDeleteMember)
	apiRouter.POST("/adty/teams/:id/members/:userId/password", middleware.UserAuth(), middleware.UserCriticalRateLimit("CT:team-employee"), middleware.AnonymousRequestBodyLimit(), controller.PostAdtyTeamEmployeePassword)
	apiRouter.POST("/adty/teams/:id/keys", middleware.UserAuth(), controller.PostAdtyTeamBindKey)
	apiRouter.POST("/adty/teams/:id/keys/:tokenId/revoke", middleware.UserAuth(), controller.PostAdtyTeamRevokeKey)
	apiRouter.POST("/adty/teams/:id/transfer", middleware.UserAuth(), controller.PostAdtyTeamTransfer)
	apiRouter.GET("/adty/telegram/me", middleware.UserAuth(), controller.GetAdtyTelegramMe)
	apiRouter.DELETE("/adty/telegram/me", middleware.UserAuth(), controller.DeleteAdtyTelegramBinding)
	apiRouter.POST("/adty/telegram/unbind", middleware.UserAuth(), controller.DeleteAdtyTelegramBinding)
	apiRouter.POST("/adty/telegram/bind/unbind", middleware.UserAuth(), controller.DeleteAdtyTelegramBinding)
	apiRouter.POST("/adty/telegram/bind/start", middleware.UserAuth(), controller.PostAdtyTelegramBindStart)
	apiRouter.POST("/adty/telegram/bind/confirm", middleware.UserAuth(), controller.PostAdtyTelegramBindConfirm)
	apiRouter.GET("/adty/site/telegram", middleware.UserAuth(), controller.GetAdtyTelegramMe)
	apiRouter.DELETE("/adty/site/telegram", middleware.UserAuth(), controller.DeleteAdtyTelegramBinding)
	apiRouter.POST("/adty/site/telegram/unbind", middleware.UserAuth(), controller.DeleteAdtyTelegramBinding)
	apiRouter.POST("/adty/site/telegram/bind/unbind", middleware.UserAuth(), controller.DeleteAdtyTelegramBinding)
	apiRouter.POST("/adty/site/telegram/bind/start", middleware.UserAuth(), controller.PostAdtyTelegramBindStart)
	apiRouter.POST("/adty/site/telegram/bind/confirm", middleware.UserAuth(), controller.PostAdtyTelegramBindConfirm)
	apiRouter.POST("/adty/telegram/webhook/:purpose/:secret", middleware.AnonymousRequestBodyLimit(), middleware.CriticalRateLimit(), controller.PostAdtyTelegramWebhook)
	root := apiRouter.Group("/adty/admin", middleware.RootAuth())
	root.PUT("/ticket-image-policy", controller.PutAdtyTicketImagePolicy)
	root.GET("/notice-templates", controller.GetAdtyNoticeTemplates)
	root.PUT("/notice-templates/:key", controller.PutAdtyNoticeTemplate)
	root.GET("/telegram/bots", controller.GetAdtyAdminTelegram)
	root.PUT("/telegram/bots/:purpose", controller.PutAdtyAdminTelegram)
	root.POST("/telegram/bots/:purpose/test", controller.PostAdtyAdminTelegramTest)
	root.GET("/translation", controller.GetAdtyTranslation)
	root.PUT("/translation", controller.PutAdtyTranslation)
	root.POST("/translation/test", controller.PostAdtyTranslationTest)
	root.POST("/translation/run", controller.PostAdtyTranslationRun)
	root.GET("/translation/usage", controller.GetAdtyTranslationUsage)
	root.GET("/translation/tasks", controller.GetAdtyTranslationTasks)
	root.POST("/translation/retry", controller.PostAdtyTranslationRetry)
	root.POST("/translation/complete", controller.PostAdtyTranslationComplete)
	root.PUT("/translation/edit", controller.PutAdtyTranslationEdit)
	root.GET("/translation/glossary", controller.GetAdtyTranslationGlossary)
	root.POST("/translation/glossary", controller.PostAdtyTranslationGlossary)
	root.PUT("/translation/glossary/:id", controller.PutAdtyTranslationGlossary)
	root.DELETE("/translation/glossary/:id", controller.DeleteAdtyTranslationGlossary)
	root.GET("/group-health", controller.GetAdtyAdminGroupHealth)
	root.PUT("/group-health", controller.PutAdtyGroupHealth)
	root.POST("/group-health/probe", controller.PostAdtyGroupProbe)
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/notice-templates", "scope", "option:read")
	middleware.DeclareAdtyAccessTokenRoute("PUT", "/api/adty/admin/notice-templates/:key", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/data-retention", "scope", "option:read")
	middleware.DeclareAdtyAccessTokenRoute("PUT", "/api/adty/admin/data-retention", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/data-retention/runs", "scope", "option:read")
	root.GET("/data-retention", controller.GetAdtyDataRetention)
	root.PUT("/data-retention", controller.PutAdtyDataRetention)
	root.GET("/data-retention/runs", controller.GetAdtyDataRetentionRuns)
	apiRouter.GET("/adty/badges", middleware.UserAuth(), controller.GetAdtySidebarBadges)
	apiRouter.POST("/adty/badges/seen", middleware.UserAuth(), controller.PostAdtySidebarBadgesSeen)
	apiRouter.GET("/adty/tickets", middleware.UserAuth(), controller.GetAdtyTickets)
	apiRouter.GET("/adty/tickets/request-audience", middleware.UserAuth(), controller.GetAdtyTicketRequestAudience)
	apiRouter.POST("/adty/tickets", middleware.UserAuth(), middleware.UserCriticalRateLimit("CT:ticket-create"), controller.PostAdtyTicket)
	apiRouter.GET("/adty/tickets/:id", middleware.UserAuth(), controller.GetAdtyTicket)
	apiRouter.POST("/adty/tickets/:id/replies", middleware.UserAuth(), middleware.UserCriticalRateLimit("CT:ticket-reply"), controller.PostAdtyTicketReply)
	apiRouter.POST("/adty/tickets/:id/notes", middleware.UserAuth(), middleware.UserCriticalRateLimit("CT:ticket-note"), controller.PostAdtyTicketNote)
	apiRouter.POST("/adty/tickets/:id/status", middleware.UserAuth(), controller.PostAdtyTicketStatus)
	apiRouter.POST("/adty/tickets/:id/images", middleware.UserAuth(), middleware.UserCriticalRateLimit("CT:ticket-image"), controller.PostAdtyTicketImage)
	apiRouter.GET("/adty/tickets/:id/images/:imageId", middleware.UserAuth(), controller.GetAdtyTicketImage)
	apiRouter.DELETE("/adty/tickets/:id/images/:imageId", middleware.UserAuth(), controller.DeleteAdtyTicketImage)
	apiRouter.GET("/adty/groups/health", middleware.UserAuth(), controller.GetAdtyGroupHealth)
}

// registerAdtyTopUpRoutes 挂钱包的充值接口（任务 3.3），令牌规则照上游钱包接口。
func registerAdtyTopUpRoutes(apiRouter *gin.RouterGroup) {
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/payment-scenes", "scope", "option:read")
	middleware.DeclareAdtyAccessTokenRoute("PUT", "/api/adty/admin/payment-scenes", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/fund-anomalies", "scope", "option:read")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/fund-anomalies/dispositions", "scope", "option:read")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/fund-anomalies/dispositions/:id/review", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/gift-fulfillments", "scope", "adty_finance:read")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/gift-fulfillments/:id/review", "scope", "adty_finance:manual_credit")
	apiRouter.GET("/adty/admin/fund-anomalies/dispositions", middleware.RootAuth(), controller.GetAdtyAnomalyDispositions)
	apiRouter.POST("/adty/admin/fund-anomalies/dispositions/:id/review", middleware.RootAuth(), controller.PostAdtyAnomalyDispositionReview)
	apiRouter.GET("/adty/admin/fund-anomalies", middleware.RootAuth(), controller.GetAdtyFundAnomalies)
	apiRouter.GET("/adty/admin/gift-fulfillments", middleware.RootAuth(), controller.GetAdtyGiftFulfillments)
	apiRouter.POST("/adty/admin/gift-fulfillments/:id/review", middleware.RootAuth(), controller.PostAdtyGiftFulfillmentReview)
	middleware.DeclareAdtyAccessTokenRoute("PUT", "/api/adty/admin/site-withdrawals/rules", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/site-withdrawals/:id/paying", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/site-withdrawals/:id/recall", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/site-withdrawals/:id/reject", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/site-withdrawals/:id/paid", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/sites/:id/commission-margin", "scope", "option:read")
	middleware.DeclareAdtyAccessTokenRoute("PUT", "/api/adty/admin/sites/:id/commission-margin", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/teams", "scope", "option:read")
	apiRouter.GET("/adty/admin/teams", middleware.RootAuth(), controller.GetAdtyMainTeams)
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/teams/:id", "scope", "option:read")
	apiRouter.GET("/adty/admin/teams/:id", middleware.RootAuth(), controller.GetAdtyAdminTeam)
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/teams/:id/ledgers", "scope", "option:read")
	apiRouter.GET("/adty/admin/teams/:id/ledgers", middleware.RootAuth(), controller.GetAdtyAdminTeamLedger)
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/teams", "scope", "option:write")
	apiRouter.POST("/adty/admin/teams", middleware.RootAuth(), controller.PostAdtyMainTeam)
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/team-registrations", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/team-registrations", "scope", "option:read")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/team-registrations/:id/revoke", "scope", "option:write")
	apiRouter.POST("/adty/admin/team-registrations", middleware.RootAuth(), controller.PostAdtyMainTeamRegistration)
	apiRouter.GET("/adty/admin/team-registrations", middleware.RootAuth(), controller.GetAdtyMainTeamRegistrations)
	apiRouter.POST("/adty/admin/team-registrations/:id/revoke", middleware.RootAuth(), controller.PostAdtyMainTeamRegistrationRevoke)
	middleware.DeclareAdtyAccessTokenRoute("PUT", "/api/adty/admin/teams/:id/agreement-price", "scope", "option:write")
	apiRouter.PUT("/adty/admin/teams/:id/agreement-price", middleware.RootAuth(), controller.PutAdtyMainTeamAgreementPrice)
	for _, path := range []string{"/adty/admin/site-referral/cap", "/adty/admin/site-referral/commission-policy", "/adty/admin/site-invite-withdraw/rules"} {
		middleware.DeclareAdtyAccessTokenRoute("PUT", "/api"+path, "scope", "option:write")
		apiRouter.PUT(path, middleware.RootAuth(), controller.PutAdtySiteReferralCap)
	}
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/site-referral/batch-policy", "scope", "option:read")
	middleware.DeclareAdtyAccessTokenRoute("PUT", "/api/adty/admin/site-referral/batch-policy", "scope", "option:write")
	apiRouter.GET("/adty/admin/site-referral/batch-policy", middleware.RootAuth(), controller.GetAdtySiteInviteBatchPolicy)
	apiRouter.PUT("/adty/admin/site-referral/batch-policy", middleware.RootAuth(), controller.PutAdtySiteInviteBatchPolicy)
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/business-timezone", "scope", "option:read")
	middleware.DeclareAdtyAccessTokenRoute("PUT", "/api/adty/admin/business-timezone", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/settlement-sheets", "scope", "option:read")
	apiRouter.GET("/adty/admin/business-timezone", middleware.RootAuth(), controller.GetAdtyBusinessTimezone)
	apiRouter.PUT("/adty/admin/business-timezone", middleware.RootAuth(), controller.PutAdtyBusinessTimezone)
	apiRouter.GET("/adty/admin/settlement-sheets", middleware.RootAuth(), controller.GetAdtyAdminSettlementSheets)
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/site-withdrawals", "scope", "option:read")
	apiRouter.GET("/adty/admin/site-withdrawals", middleware.RootAuth(), controller.GetAdtyAdminSiteWithdrawals)
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/site-withdrawals/rules", "scope", "option:read")
	apiRouter.GET("/adty/admin/site-withdrawals/rules", middleware.RootAuth(), controller.GetAdtyAdminWithdrawRules)
	apiRouter.PUT("/adty/admin/site-withdrawals/rules", middleware.RootAuth(), controller.PutAdtySiteWithdrawRules)
	apiRouter.POST("/adty/admin/site-withdrawals/:id/paying", middleware.RootAuth(), controller.PostAdtySiteWithdrawalPaying)
	apiRouter.POST("/adty/admin/site-withdrawals/:id/recall", middleware.RootAuth(), controller.PostAdtySiteWithdrawalRecall)
	apiRouter.POST("/adty/admin/site-withdrawals/:id/reject", middleware.RootAuth(), controller.PostAdtySiteWithdrawalReject)
	apiRouter.POST("/adty/admin/site-withdrawals/:id/paid", middleware.RootAuth(), controller.PostAdtySiteWithdrawalPaid)
	apiRouter.GET("/adty/admin/payment-scenes", middleware.RootAuth(), controller.GetAdtyPaymentScenes)
	apiRouter.PUT("/adty/admin/payment-scenes", middleware.RootAuth(), controller.PutAdtyPaymentScenes)
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/topup/info", "scope", "wallet:read")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/topup/quote", "scope", "wallet:read")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/topup/orders/:trade_no", "scope", "wallet:read")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/topup/orders/:trade_no/cancel", "scope", "wallet:write")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/topup/pending", "scope", "wallet:read")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/topup/self", "scope", "wallet:read")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/vouchers/redeem", "scope", "wallet:write")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/teams/mine", "scope", "wallet:read")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/teams", "session", "")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/teams/:id/invites", "session", "")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/teams/:id/invites", "session", "")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/teams/:id/invites/:inviteId/revoke", "session", "")

	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/orders", "scope", "wallet:read")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/orders/:trade_no", "scope", "wallet:read")
	apiRouter.GET("/adty/orders", middleware.UserAuth(), middleware.AdtyRequireCurrentSiteTerms(), controller.GetAdtySelfOrders)
	apiRouter.GET("/adty/orders/:trade_no", middleware.UserAuth(), middleware.AdtyRequireCurrentSiteTerms(), controller.GetAdtySelfOrder)
	topup := apiRouter.Group("/adty/topup", middleware.UserAuth(), middleware.AdtyRequireCurrentSiteTerms())
	topup.GET("/info", controller.GetAdtyTopUpInfo)
	topup.POST("/quote", controller.PostAdtyTopUpQuote)
	topup.GET("/orders/:trade_no", controller.GetAdtyTopUpOrder)
	topup.POST("/orders/:trade_no/cancel", middleware.CriticalRateLimit(), controller.CancelAdtyTopUpOrder)
	topup.GET("/pending", controller.GetAdtyTopUpPending)
	topup.GET("/self", controller.GetAdtyTopUpSelf)
	// R1-062: Enforce site customer terms gate on voucher redemption and site-commission writes.
	apiRouter.POST("/adty/vouchers/redeem", middleware.UserAuth(), middleware.AdtyRequireCurrentSiteTerms(), middleware.UserCriticalRateLimit("CT:voucher-redeem"), controller.PostAdtyVoucherRedeem)
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/site-referral/click", "session", "")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/site-referral/profile", "scope", "wallet:read")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/site-referral/bind", "scope", "wallet:write")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/site-commissions", "scope", "wallet:read")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/site-commissions/withdrawals/rules", "scope", "wallet:read")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/site-commissions/withdrawals/quote", "scope", "wallet:read")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/site-commissions/withdrawals", "scope", "wallet:read")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/site-commissions/withdrawals", "scope", "wallet:write")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/site-commissions/withdrawals/:id/cancel", "scope", "wallet:write")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/site-commissions/withdrawals/by-key", "scope", "wallet:read")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/site-commissions/:id/convert/by-key", "scope", "wallet:read")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/site-commissions/:id/convert", "scope", "wallet:write")
	apiRouter.POST("/adty/site-referral/click", middleware.CriticalRateLimit(), middleware.AnonymousRequestBodyLimit(), middleware.ReferralClickCSRFGuard(), controller.PostAdtySiteReferralClick)
	apiRouter.GET("/adty/site-referral/profile", middleware.UserAuth(), controller.GetAdtySiteCustomerReferralProfile)
	apiRouter.POST("/adty/site-referral/bind", middleware.UserAuth(), middleware.AdtyRequireCurrentSiteTerms(), middleware.UserCriticalRateLimit("CT:site-invite-bind"), controller.PostAdtySiteCustomerReferralBind)
	apiRouter.GET("/adty/site-commissions", middleware.UserAuth(), controller.GetAdtySiteCommissions)
	apiRouter.GET("/adty/site-commissions/withdrawals/rules", middleware.UserAuth(), controller.GetAdtySiteInviteWithdrawRules)
	apiRouter.GET("/adty/site-commissions/withdrawals/quote", middleware.UserAuth(), controller.GetAdtySiteInviteWithdrawalQuote)
	apiRouter.GET("/adty/site-commissions/withdrawals", middleware.UserAuth(), controller.GetAdtySiteInviteWithdrawals)
	apiRouter.POST("/adty/site-commissions/withdrawals", middleware.UserAuth(), middleware.AdtyRequireCurrentSiteTerms(), middleware.UserCriticalRateLimit("CT:site-comm-withdraw"), controller.PostAdtySiteInviteWithdrawal)
	apiRouter.POST("/adty/site-commissions/withdrawals/:id/cancel", middleware.UserAuth(), middleware.AdtyRequireCurrentSiteTerms(), controller.PostAdtySiteInviteWithdrawalCancel)
	apiRouter.GET("/adty/site-commissions/withdrawals/by-key", middleware.UserAuth(), controller.GetAdtySiteInviteWithdrawalByKey)
	apiRouter.GET("/adty/site-commissions/:id/convert/by-key", middleware.UserAuth(), controller.GetAdtySiteCommissionConversionByKey)
	apiRouter.POST("/adty/site-commissions/:id/convert", middleware.UserAuth(), middleware.AdtyRequireCurrentSiteTerms(), middleware.UserCriticalRateLimit("CT:site-comm-convert"), controller.PostAdtySiteCommissionConvert)
	apiRouter.GET("/adty/teams/mine", middleware.UserAuth(), controller.GetAdtyTeamMine)
	apiRouter.POST("/adty/teams", middleware.UserAuth(), controller.PostAdtyTeam)
	apiRouter.POST("/adty/teams/:id/invites", middleware.UserAuth(), controller.PostAdtyTeamInvite)
	apiRouter.GET("/adty/teams/:id/invites", middleware.UserAuth(), controller.GetAdtyTeamInvites)
	apiRouter.POST("/adty/teams/:id/invites/:inviteId/revoke", middleware.UserAuth(), controller.PostAdtyTeamRevokeInvite)
	apiRouter.POST("/adty/team-employees/register", middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.AnonymousRequestBodyLimit(), controller.PostAdtyTeamEmployeeRegistration)
	apiRouter.POST("/adty/team-registrations/complete", middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.AnonymousRequestBodyLimit(), controller.PostAdtyTeamRegistrationComplete)
}

// registerAdtyFinanceRoutes 挂充值对账、补单和退款（任务 3.5），按后台权限注册。
func registerAdtyFinanceRoutes(apiRouter *gin.RouterGroup) {
	finance := apiRouter.Group("/user/admin/finance", middleware.AdminAuth())
	handlePermissionRoute(finance, http.MethodGet, "/recharge-audit", authz.AdtyFinanceRead, controller.GetRechargeAudit)
	handlePermissionRoute(finance, http.MethodGet, "/recharge-audit/summary", authz.AdtyFinanceRead, controller.GetRechargeAuditSummary)
	handlePermissionRoute(finance, http.MethodGet, "/recharge-audit/:trade_no", authz.AdtyFinanceRead, controller.GetRechargeAuditOrder)
	handlePermissionRoute(finance, http.MethodGet, "/payment-orphans", authz.AdtyFinanceRead, controller.GetPaymentOrphans)
	handlePermissionRoute(finance, http.MethodPost, "/payment-orphans/:id/credit", authz.AdtyFinanceManualCredit, controller.CreditPaymentOrphan)
	handlePermissionRoute(finance, http.MethodPost, "/payment-orphans/:id/resolve", authz.AdtyFinanceManualCredit, controller.ResolvePaymentOrphan)
	handlePermissionRoute(finance, http.MethodGet, "/site-earning-reviews", authz.AdtyFinanceRead, controller.GetAdtySiteEarningReviews)
	handlePermissionRoute(finance, http.MethodPost, "/site-earning-reviews/resolve", authz.AdtyFinanceManualCredit, controller.ReviewAdtySiteEarning)

	topup := apiRouter.Group("/adty/admin/topup", middleware.AdminAuth())
	handlePermissionRoute(topup, http.MethodPost, "/:trade_no/query", authz.AdtyFinanceManualCredit, controller.QueryAdtyTopUp)
	handlePermissionRoute(topup, http.MethodPost, "/:trade_no/complete", authz.AdtyFinanceManualCredit, controller.ManualCompleteAdtyTopUp)
	handlePermissionRoute(topup, http.MethodGet, "/:trade_no/refund-preview", authz.AdtyFinanceRefund, controller.PreviewAdtyTopUpRefund)
	handlePermissionRoute(topup, http.MethodGet, "/:trade_no/refunds", authz.AdtyFinanceRefund, controller.ListAdtyTopUpManualRefunds)
	handlePermissionRoute(topup, http.MethodGet, "/:trade_no/refunds/:refund_id", authz.AdtyFinanceRefund, controller.GetAdtyTopUpManualRefund)
	handlePermissionRoute(topup, http.MethodPost, "/:trade_no/refund", authz.AdtyFinanceRefund, controller.RefundAdtyTopUp)
	handlePermissionRoute(topup, http.MethodPost, "/:trade_no/refunds/:refund_id/approve", authz.AdtyFinanceRefund, controller.ApproveAdtyTopUpManualRefund)
	handlePermissionRoute(topup, http.MethodPost, "/:trade_no/refunds/:refund_id/reclaim", authz.AdtyFinanceRefund, controller.ReclaimAdtyTopUpManualRefund)
	handlePermissionRoute(topup, http.MethodPost, "/:trade_no/refunds/:refund_id/bonus", authz.AdtyFinanceRefund, controller.PlanAdtyTopUpManualRefundBonus)
	handlePermissionRoute(topup, http.MethodPost, "/:trade_no/refunds/:refund_id/commission", authz.AdtyFinanceRefund, controller.PlanAdtyTopUpManualRefundCommission)
	handlePermissionRoute(topup, http.MethodPost, "/:trade_no/refunds/:refund_id/payout", authz.AdtyFinanceRefund, controller.RecordAdtyTopUpManualRefundPayout)
	handlePermissionRoute(topup, http.MethodPost, "/:trade_no/refunds/:refund_id/complete", authz.AdtyFinanceRefund, controller.CompleteAdtyTopUpManualRefund)
	handlePermissionRoute(topup, http.MethodPost, "/:trade_no/refunds/:refund_id/terminate", authz.AdtyFinanceRefund, controller.TerminateAdtyTopUpManualRefund)
}

// registerAdtyBEpusdtRoutes 挂 USDT 下单和回调（任务 4.2）。BEpusdt 只发 POST JSON 回调，不注册 GET。
func registerAdtyBEpusdtRoutes(apiRouter *gin.RouterGroup) {
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/user/bepusdt/pay", "scope", "wallet:write")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/user/bepusdt/notify", "session", "")
	apiRouter.POST("/user/bepusdt/pay", middleware.UserAuth(), middleware.AdtyRequireCurrentSiteTerms(), middleware.UserCriticalRateLimit("CT:pay-bepusdt"), controller.RequestBEpusdtPay)
	apiRouter.POST("/user/bepusdt/notify", middleware.PaymentNotifyRateLimit(), middleware.AnonymousRequestBodyLimit(), controller.BEpusdtTopUpNotify)
}

// registerAdtyEpayRoutes 挂易支付多通道（任务 5.2、5.4）。静态的 /api/user/epay/notify 是上游的，和这里不冲突。
func registerAdtyEpayRoutes(apiRouter *gin.RouterGroup) {
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/epay/pay", "scope", "wallet:write")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/user/epay/notify/:channelId", "session", "")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/user/epay/notify/:channelId", "session", "")
	apiRouter.POST("/adty/epay/pay", middleware.UserAuth(), middleware.AdtyRequireCurrentSiteTerms(), middleware.UserCriticalRateLimit("CT:pay-epay"), controller.RequestAdtyEpayPay)
	apiRouter.GET("/user/epay/notify/:channelId", middleware.PaymentNotifyRateLimit(), controller.AdtyEpayNotify)
	apiRouter.POST("/user/epay/notify/:channelId", middleware.PaymentNotifyRateLimit(), middleware.AnonymousRequestBodyLimit(), controller.AdtyEpayNotify)

	admin := apiRouter.Group("/adty/admin/epay", middleware.AdminAuth())
	handlePermissionRoute(admin, http.MethodGet, "/channels", authz.AdtyPaymentRead, controller.GetAdtyEpayChannels)
	handlePermissionRoute(admin, http.MethodPost, "/channels/:id/status", authz.AdtyPaymentOperate, controller.SetAdtyEpayChannelStatus)
	handlePermissionRoute(admin, http.MethodPost, "/channels/:id/test", authz.AdtyPaymentOperate, controller.TestAdtyEpayChannel)

	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/epay/channels", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("PUT", "/api/adty/admin/epay/channels/:id", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("DELETE", "/api/adty/admin/epay/channels/:id", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/epay/channels/:id/revoke", "scope", "option:write")
	root := apiRouter.Group("/adty/admin/epay", middleware.RootAuth())
	root.POST("/channels", controller.CreateAdtyEpayChannel)
	root.PUT("/channels/:id", controller.UpdateAdtyEpayChannel)
	root.DELETE("/channels/:id", controller.DeleteAdtyEpayChannel)
	root.POST("/channels/:id/revoke", controller.RevokeAdtyEpayChannel)
}

// registerAdtyReferralRoutes 挂推广返佣（P6）。用户侧接口读 wallet:read、写 wallet:write；后台按权限注册。
func registerAdtyReferralRoutes(apiRouter *gin.RouterGroup) {
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/referral/click", "session", "")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/referral/public-settings", "any", "")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/referral-assets/*path", "session", "")
	apiRouter.POST("/adty/referral/click", middleware.CriticalRateLimit(), middleware.AnonymousRequestBodyLimit(), middleware.ReferralClickCSRFGuard(), controller.PostAdtyReferralClick)
	apiRouter.GET("/adty/referral/public-settings", controller.GetAdtyReferralPublicSettings)
	apiRouter.GET("/referral-assets/*path", controller.GetReferralAsset)

	user := apiRouter.Group("/user/referral", middleware.UserAuth(), controller.RejectAdminUserReferral)
	for _, route := range []struct {
		method, path, scope string
		handlers            []gin.HandlerFunc
	}{
		{http.MethodGet, "/profile", "wallet:read", []gin.HandlerFunc{controller.GetReferralProfile}},
		{http.MethodPost, "/apply", "wallet:write", []gin.HandlerFunc{controller.ApplyReferralPartner}},
		{http.MethodGet, "/commissions", "wallet:read", []gin.HandlerFunc{controller.GetReferralCommissions}},
		{http.MethodGet, "/withdrawals", "wallet:read", []gin.HandlerFunc{controller.GetReferralWithdrawals}},
		{http.MethodPost, "/withdrawals", "wallet:write", []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:referral-withdraw"), controller.CreateReferralWithdrawal}},
		{http.MethodPost, "/withdrawals/:id/cancel", "wallet:write", []gin.HandlerFunc{controller.CancelReferralWithdrawal}},
		{http.MethodPost, "/upload", "wallet:write", []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:referral-upload"), controller.UploadReferralAsset}},
	} {
		middleware.DeclareAdtyAccessTokenRoute(route.method, "/api/user/referral"+route.path, "scope", route.scope)
		user.Handle(route.method, route.path, route.handlers...)
	}

	admin := apiRouter.Group("/user/admin/referral", middleware.AdminAuth())
	for _, route := range []struct {
		method, path string
		permission   authz.Permission
		handlers     []gin.HandlerFunc
	}{
		{http.MethodGet, "/overview", authz.AdtyReferralRead, []gin.HandlerFunc{controller.GetReferralOverview}},
		{http.MethodGet, "/badges", authz.AdtyReferralRead, []gin.HandlerFunc{controller.GetReferralAdminBadges}},
		{http.MethodGet, "/affiliates", authz.AdtyReferralRead, []gin.HandlerFunc{controller.GetReferralAffiliates}},
		{http.MethodGet, "/affiliates/:user_id/bindings", authz.AdtyReferralRead, []gin.HandlerFunc{controller.GetReferralBindings}},
		{http.MethodGet, "/commissions", authz.AdtyReferralRead, []gin.HandlerFunc{controller.GetAdminReferralCommissions}},
		{http.MethodGet, "/commission-jobs", authz.AdtyReferralRead, []gin.HandlerFunc{controller.GetReferralCommissionJobs}},
		{http.MethodGet, "/ledgers", authz.AdtyReferralRead, []gin.HandlerFunc{controller.GetReferralLedgers}},
		{http.MethodGet, "/audit-logs", authz.AdtyReferralRead, []gin.HandlerFunc{controller.GetReferralAdminAuditLogs}},
		{http.MethodGet, "/withdrawals", authz.AdtyReferralRead, []gin.HandlerFunc{controller.GetAdminReferralWithdrawals}},
		{http.MethodPost, "/affiliates/:user_id/partner", authz.AdtyReferralReview, []gin.HandlerFunc{controller.ReviewReferralPartner}},
		{http.MethodGet, "/closed-site-withdrawals", authz.AdtyReferralRead, []gin.HandlerFunc{controller.GetClosedSiteInviteWithdrawals}},
		{http.MethodPost, "/closed-site-withdrawals/:id/review", authz.AdtyReferralReview, []gin.HandlerFunc{controller.PostClosedSiteInviteWithdrawalReview}},
		{http.MethodPost, "/closed-site-withdrawals/:id/failed", authz.AdtyReferralPayout, []gin.HandlerFunc{controller.PostClosedSiteInviteWithdrawalFailed}},
		{http.MethodPost, "/closed-site-withdrawals/:id/paid", authz.AdtyReferralPayout, []gin.HandlerFunc{controller.PostClosedSiteInviteWithdrawalPaid}},
		{http.MethodPost, "/withdrawals/:id/review", authz.AdtyReferralReview, []gin.HandlerFunc{controller.ReviewReferralWithdrawal}},
		{http.MethodPost, "/withdrawals/:id/pay", authz.AdtyReferralPayout, []gin.HandlerFunc{controller.PayReferralWithdrawal}},
		{http.MethodPost, "/upload", authz.AdtyReferralPayout, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:referral-upload"), controller.UploadReferralAsset}},
		{http.MethodPost, "/affiliates/:user_id/update", authz.AdtyReferralAdjust, []gin.HandlerFunc{controller.UpdateReferralAffiliate}},
		{http.MethodPost, "/commission-jobs/retry", authz.AdtyReferralAdjust, []gin.HandlerFunc{controller.RetryReferralCommissionJob}},
		{http.MethodPost, "/settlements/run", authz.AdtyReferralAdjust, []gin.HandlerFunc{controller.RunReferralSettlement}},
		{http.MethodGet, "/settings", authz.AdtyReferralSettingsRead, []gin.HandlerFunc{controller.GetReferralSettings}},
		{http.MethodPut, "/settings", authz.AdtyReferralSettings, []gin.HandlerFunc{controller.UpdateReferralSettings}},
	} {
		handlePermissionRoute(admin, route.method, route.path, route.permission, route.handlers...)
	}
}

// registerAdtySiteRoutes 挂站点公开配置（任务 8.3）。
func registerAdtySiteRoutes(apiRouter *gin.RouterGroup) {
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/site", "any", "")
	apiRouter.GET("/adty/site", controller.GetAdtySite)
}

// registerAdtyLegalRoutes 挂多语言条款（任务 8.6），和上游 /api/user-agreement 一样公开。
func registerAdtyLegalRoutes(apiRouter *gin.RouterGroup) {
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/legal/:doc", "any", "")
	apiRouter.GET("/adty/legal/:doc", controller.GetAdtyLegal)
}

// registerAdtyResellerRoutes 挂站长分站（P11）。后台接口按「分站」权限注册（任务 11.9）；
// 站长侧接口按专属 site_owner_* scope 隔离，严禁与个人 wallet:* 混用（R1-016）。
func registerAdtyResellerRoutes(apiRouter *gin.RouterGroup) {
	const (
		ownerCustRead  = service.AccessTokenResourceSiteOwnerCustomer + ":read"
		ownerCustWrite = service.AccessTokenResourceSiteOwnerCustomer + ":write"
		ownerOrdRead   = service.AccessTokenResourceSiteOwnerOrder + ":read"
		ownerOrdWrite  = service.AccessTokenResourceSiteOwnerOrder + ":write"
		ownerLogRead   = service.AccessTokenResourceSiteOwnerLog + ":read"
		ownerSetRead   = service.AccessTokenResourceSiteOwnerSetting + ":read"
		ownerSetWrite  = service.AccessTokenResourceSiteOwnerSetting + ":write"
	)
	owner := apiRouter.Group("/adty/site", middleware.UserAuth())
	for _, route := range []struct {
		method, path, scope string
		handlers            []gin.HandlerFunc
	}{
		{http.MethodPost, "/apply", "wallet:write", []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-apply"), controller.ApplyAdtySite}},
		{http.MethodPost, "/close", ownerSetWrite, []gin.HandlerFunc{controller.RequestAdtySiteClose}},
		// 改价、改加赠、删域名等敏感操作需二次验证（任务 11.6）
		{http.MethodPut, "/pricing", ownerSetWrite, []gin.HandlerFunc{controller.PutAdtySitePricingWithProof}},
		{http.MethodGet, "/pricing/history", ownerSetRead, []gin.HandlerFunc{controller.GetAdtyOwnerPricingHistory}},
		{http.MethodGet, "/customers/:id/pricing/history", ownerCustRead, []gin.HandlerFunc{controller.GetAdtyOwnerPricingHistory}},
		{http.MethodPut, "/customers/:id/pricing", ownerCustWrite, []gin.HandlerFunc{controller.PutAdtySiteCustomerPricingWithProof}},
		// 自定义域名（任务 11.8）
		{http.MethodGet, "/domains", ownerSetRead, []gin.HandlerFunc{controller.GetAdtySiteDomains}},
		{http.MethodPost, "/domains", ownerSetWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-domain"), controller.AddAdtySiteDomain}},
		{http.MethodPost, "/domains/pair", ownerSetWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-domain"), controller.AddAdtySiteDomainPair}},
		{http.MethodDelete, "/domains/:id", ownerSetWrite, []gin.HandlerFunc{controller.RemoveAdtySiteDomainWithProof}},
		// 站长中心（任务 11.6、11.7）。接受协议不走站长中心的协议门。
		{http.MethodPost, "/terms/accept", "profile:write", []gin.HandlerFunc{controller.AcceptAdtySiteTerms}},
		{http.MethodGet, "/self", ownerSetRead, []gin.HandlerFunc{controller.GetAdtyOwnerSite}},
		{http.MethodGet, "/archived-sites/:site_id/domains", ownerSetRead, []gin.HandlerFunc{controller.GetAdtyArchivedWebsiteDomains}},
		{http.MethodDelete, "/archived-sites/:site_id/domains/:id", ownerSetWrite, []gin.HandlerFunc{controller.DeleteAdtyArchivedWebsiteDomain}},
		{http.MethodGet, "/archived-sites", ownerSetRead, []gin.HandlerFunc{controller.GetAdtyArchivedOwnerSites}},
		{http.MethodPut, "/self", ownerSetWrite, []gin.HandlerFunc{controller.PutAdtyOwnerSite}},
		{http.MethodGet, "/stats", ownerOrdRead, []gin.HandlerFunc{controller.GetAdtyOwnerStats}},
		{http.MethodGet, "/earnings", ownerOrdRead, []gin.HandlerFunc{controller.GetAdtyOwnerEarnings}},
		{http.MethodGet, "/settlement-sheets", ownerOrdRead, []gin.HandlerFunc{controller.GetAdtyOwnerSettlementSheets}},
		{http.MethodGet, "/earnings/lines", ownerOrdRead, []gin.HandlerFunc{controller.GetAdtyOwnerEarningLines}},
		{http.MethodGet, "/vouchers", ownerOrdRead, []gin.HandlerFunc{controller.GetAdtySiteVouchers}},
		{http.MethodPost, "/vouchers", ownerOrdWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-voucher"), controller.PostAdtySiteVoucher}},
		{http.MethodPost, "/vouchers/:id/cancel", ownerOrdWrite, []gin.HandlerFunc{controller.PostAdtyVoucherCancel}},
		{http.MethodPost, "/vouchers/:id/freeze", ownerOrdWrite, []gin.HandlerFunc{controller.PostAdtyVoucherFreeze}},
		{http.MethodPut, "/bonus-methods", ownerOrdWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-bonus-method"), controller.PutAdtySiteMethodBonus}},
		{http.MethodGet, "/wallets", ownerOrdRead, []gin.HandlerFunc{controller.GetAdtyOwnerWallets}},
		{http.MethodPost, "/wallets/earning-to-margin", ownerOrdWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-earning-margin"), controller.PostAdtyEarningToMargin}},
		{http.MethodPost, "/wallets/earning-to-personal", ownerOrdWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-earning-personal"), controller.PostOwnerPersonalTransfer}},
		{http.MethodGet, "/wallets/self-use", ownerOrdRead, []gin.HandlerFunc{controller.GetOwnerSelfUseBalance}},
		{http.MethodGet, "/self-use/usage", ownerOrdRead, []gin.HandlerFunc{controller.GetOwnerSelfUseUsage}},
		{http.MethodGet, "/self-use/transfers", ownerOrdRead, []gin.HandlerFunc{controller.GetOwnerPrincipalTransfers}},
		{http.MethodPost, "/self-use/keys", ownerOrdWrite, []gin.HandlerFunc{middleware.TokenOperationAudit(), middleware.UserCriticalRateLimit("CT:owner-key"), controller.AddOwnerSelfUseToken}},
		{http.MethodGet, "/withdrawals/rules", ownerOrdRead, []gin.HandlerFunc{controller.GetAdtySiteWithdrawRules}},
		{http.MethodGet, "/withdrawals/by-key", ownerOrdRead, []gin.HandlerFunc{controller.GetAdtySiteWithdrawalByKey}},
		{http.MethodGet, "/withdrawals", ownerOrdRead, []gin.HandlerFunc{controller.GetAdtySiteWithdrawals}},
		{http.MethodPost, "/withdrawals", ownerOrdWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-withdraw"), controller.PostAdtySiteWithdrawal}},
		{http.MethodPost, "/withdrawals/qr", ownerOrdWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-withdraw-qr"), controller.PostAdtySiteWithdrawalQR}},
		{http.MethodPost, "/withdrawals/:id/cancel", ownerOrdWrite, []gin.HandlerFunc{controller.PostAdtySiteWithdrawalCancel}},
		{http.MethodGet, "/wallet-ledgers", ownerOrdRead, []gin.HandlerFunc{controller.GetAdtyOwnerWalletLedger}},
		{http.MethodGet, "/orders", ownerOrdRead, []gin.HandlerFunc{controller.GetAdtyOwnerOrders}},
		{http.MethodGet, "/orders/summary", ownerOrdRead, []gin.HandlerFunc{controller.GetAdtyOwnerOrderSummary}},
		{http.MethodGet, "/orders/:trade_no", ownerOrdRead, []gin.HandlerFunc{controller.GetAdtyOwnerOrder}},
		{http.MethodGet, "/orders/:trade_no/refund-preview", ownerOrdRead, []gin.HandlerFunc{controller.PreviewAdtyOwnerOrderRefund}},
		{http.MethodGet, "/orders/:trade_no/refunds", ownerOrdRead, []gin.HandlerFunc{controller.ListAdtyOwnerOrderManualRefunds}},
		{http.MethodGet, "/orders/:trade_no/refunds/:refund_id", ownerOrdRead, []gin.HandlerFunc{controller.GetAdtyOwnerOrderManualRefund}},
		{http.MethodPost, "/orders/:trade_no/refund", ownerOrdWrite, []gin.HandlerFunc{controller.RefundAdtyOwnerOrder}},
		{http.MethodPost, "/orders/:trade_no/refunds/:refund_id/approve", ownerOrdWrite, []gin.HandlerFunc{controller.ApproveAdtyOwnerOrderManualRefund}},
		{http.MethodPost, "/orders/:trade_no/refunds/:refund_id/reclaim", ownerOrdWrite, []gin.HandlerFunc{controller.ReclaimAdtyOwnerOrderManualRefund}},
		{http.MethodPost, "/orders/:trade_no/refunds/:refund_id/bonus", ownerOrdWrite, []gin.HandlerFunc{controller.PlanAdtyOwnerOrderManualRefundBonus}},
		{http.MethodPost, "/orders/:trade_no/refunds/:refund_id/commission", ownerOrdWrite, []gin.HandlerFunc{controller.PlanAdtyOwnerOrderManualRefundCommission}},
		{http.MethodPost, "/orders/:trade_no/refunds/:refund_id/payout", ownerOrdWrite, []gin.HandlerFunc{controller.RecordAdtyOwnerOrderManualRefundPayout}},
		{http.MethodPost, "/orders/:trade_no/refunds/:refund_id/complete", ownerOrdWrite, []gin.HandlerFunc{controller.CompleteAdtyOwnerOrderManualRefund}},
		{http.MethodPost, "/orders/:trade_no/refunds/:refund_id/terminate", ownerOrdWrite, []gin.HandlerFunc{controller.TerminateAdtyOwnerOrderManualRefund}},
		{http.MethodGet, "/teams", ownerSetRead, []gin.HandlerFunc{controller.GetAdtyOwnerSiteTeams}},
		{http.MethodGet, "/teams/:id", ownerSetRead, []gin.HandlerFunc{controller.GetAdtyOwnerSiteTeam}},
		{http.MethodGet, "/teams/:id/ledgers", ownerSetRead, []gin.HandlerFunc{controller.GetAdtyOwnerSiteTeamLedger}},
		{http.MethodPost, "/teams/:id/balance", ownerSetWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-team-balance"), controller.PostAdtyOwnerSiteTeamBalance}},
		{http.MethodPost, "/teams", ownerSetWrite, []gin.HandlerFunc{controller.PostAdtyOwnerSiteTeam}},
		{http.MethodPost, "/team-registrations", ownerSetWrite, []gin.HandlerFunc{controller.PostAdtyOwnerSiteTeamRegistration}},
		{http.MethodGet, "/team-registrations", ownerSetRead, []gin.HandlerFunc{controller.GetAdtyOwnerSiteTeamRegistrations}},
		{http.MethodPost, "/team-registrations/:id/revoke", ownerSetWrite, []gin.HandlerFunc{controller.PostAdtyOwnerSiteTeamRegistrationRevoke}},
		{http.MethodPut, "/teams/:id/discount", ownerSetWrite, []gin.HandlerFunc{controller.PutAdtyOwnerSiteTeamDiscount}},
		{http.MethodGet, "/referral", ownerSetRead, []gin.HandlerFunc{controller.GetAdtyOwnerSiteReferral}},
		{http.MethodPut, "/referral", ownerSetWrite, []gin.HandlerFunc{controller.PutAdtyOwnerSiteReferral}},
		{http.MethodPut, "/referral/enabled", ownerSetWrite, []gin.HandlerFunc{controller.PutAdtyOwnerSiteReferralEnabled}},
		{http.MethodGet, "/referral/overview", ownerSetRead, []gin.HandlerFunc{controller.GetAdtyOwnerSiteReferralOverview}},
		{http.MethodGet, "/referral/affiliates", ownerSetRead, []gin.HandlerFunc{controller.GetAdtyOwnerSiteReferralAffiliates}},
		{http.MethodPost, "/referral/affiliates/:user_id/update", ownerSetWrite, []gin.HandlerFunc{controller.PostAdtyOwnerSiteReferralAffiliateUpdate}},
		{http.MethodGet, "/referral/commissions", ownerSetRead, []gin.HandlerFunc{controller.GetAdtyOwnerSiteReferralCommissions}},
		{http.MethodGet, "/referral/audit-logs", ownerSetRead, []gin.HandlerFunc{controller.GetAdtyOwnerSiteReferralAuditLogs}},
		{http.MethodGet, "/referral/invites", ownerSetRead, []gin.HandlerFunc{controller.GetAdtyOwnerSiteInvites}},
		{http.MethodPost, "/referral/invites", ownerSetWrite, []gin.HandlerFunc{controller.PostAdtyOwnerSiteInvite}},
		{http.MethodPost, "/referral/cover-pending", ownerSetWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-referral-cover"), controller.PostAdtyOwnerSiteCoverageResume}},
		{http.MethodGet, "/commission-withdrawals", ownerOrdRead, []gin.HandlerFunc{controller.GetAdtyOwnerSiteInviteWithdrawals}},
		{http.MethodPost, "/commission-withdrawals/:id/review", ownerOrdWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-comm-wd-review"), controller.PostAdtyOwnerSiteInviteWithdrawalReview}},
		{http.MethodPost, "/commission-withdrawals/:id/paid", ownerOrdWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-comm-wd-paid"), controller.PostAdtyOwnerSiteInviteWithdrawalPaid}},
		{http.MethodGet, "/customers", ownerCustRead, []gin.HandlerFunc{controller.GetAdtyOwnerCustomers}},
		{http.MethodGet, "/customers/lookup", ownerCustRead, []gin.HandlerFunc{controller.LookupAdtyOwnerCustomer}},
		{http.MethodGet, "/customers/:id", ownerCustRead, []gin.HandlerFunc{controller.GetAdtyOwnerCustomer}},
		{http.MethodGet, "/customers/:id/logs", ownerCustRead, []gin.HandlerFunc{controller.GetAdtyOwnerCustomerLogs}},
		{http.MethodGet, "/customers/:id/topups", ownerCustRead, []gin.HandlerFunc{controller.GetAdtyOwnerCustomerTopUps}},
		{http.MethodPost, "/customers/:id/balance", ownerCustWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-customer-balance"), controller.PostAdtyOwnerCustomerBalance}},
		{http.MethodPost, "/customers/:id/password", ownerCustWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-customer-password"), middleware.DisableCache(), controller.PostAdtyOwnerCustomerPassword}},
		{http.MethodPost, "/report", ownerCustWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-report"), controller.PostAdtyOwnerReport}},
		{http.MethodGet, "/export", ownerLogRead, []gin.HandlerFunc{middleware.DisableCache(), controller.GetAdtyOwnerExport}},
		{http.MethodPost, "/logo", ownerSetWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-logo"), controller.UploadAdtySiteLogo}},
		// 站长发件域名（任务 11.3）与独立邮件配置（R-37）
		{http.MethodGet, "/sender-domain", ownerSetRead, []gin.HandlerFunc{controller.GetAdtySiteSenderDomain}},
		{http.MethodPost, "/sender-domain", ownerSetWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-sender"), controller.AddAdtySiteSenderDomain}},
		{http.MethodDelete, "/sender-domain", ownerSetWrite, []gin.HandlerFunc{controller.RemoveAdtySiteSenderDomainWithProof}},
		{http.MethodGet, "/mail-config", ownerSetRead, []gin.HandlerFunc{controller.GetAdtySiteMailConfig}},
		{http.MethodPut, "/mail-config", ownerSetWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-mail-config"), controller.PutAdtySiteMailConfig}},
		{http.MethodPost, "/mail-config/test", ownerSetWrite, []gin.HandlerFunc{middleware.UserCriticalRateLimit("CT:site-mail-test"), controller.PostAdtySiteMailConfigTest}},
	} {
		middleware.DeclareAdtyAccessTokenRoute(route.method, "/api/adty/site"+route.path, "scope", route.scope)
		owner.Handle(route.method, route.path, route.handlers...)
	}

	// Employee administration and workspace require live browser sessions.
	for _, route := range []struct {
		method, path string
		handler      gin.HandlerFunc
	}{
		{http.MethodGet, "/adty/site/staff", controller.GetAdtyOwnerStaff},
		{http.MethodGet, "/adty/site/staff/audits", controller.GetAdtyOwnerStaffAudits},
		{http.MethodPost, "/adty/site/staff", controller.PostAdtyOwnerStaff},
		{http.MethodPut, "/adty/site/staff/:id", controller.PutAdtyOwnerStaff},
		{http.MethodPut, "/adty/site/staff/:id/password", controller.PutAdtyOwnerStaffPassword},
		{http.MethodDelete, "/adty/site/staff/:id", controller.DeleteAdtyOwnerStaff},
		{http.MethodGet, "/adty/staff/self", controller.GetAdtyStaffSelf},
		{http.MethodGet, "/adty/staff/customers", controller.GetAdtyStaffCustomers},
		{http.MethodGet, "/adty/staff/customers/:id", controller.GetAdtyStaffCustomer},
		{http.MethodGet, "/adty/staff/customers/:id/topups", controller.GetAdtyStaffCustomerTopUps},
		{http.MethodGet, "/adty/staff/customers/:id/orders", controller.GetAdtyStaffCustomerOrders},
		{http.MethodGet, "/adty/staff/customers/:id/orders/:trade_no", controller.GetAdtyStaffCustomerOrders},
		{http.MethodGet, "/adty/staff/customers/:id/logs", controller.GetAdtyStaffCustomerLogs},
		{http.MethodPost, "/adty/staff/customers/:id/password-reset", controller.PostAdtyStaffCustomerPasswordReset},
		{http.MethodPost, "/adty/staff/customers/:id/disable", controller.PostAdtyStaffCustomerDisable},
		{http.MethodGet, "/adty/staff/balance-requests", controller.GetAdtyStaffBalanceRequests},
		{http.MethodPost, "/adty/staff/balance-requests", controller.PostAdtyStaffBalanceRequest},
		{http.MethodGet, "/adty/site/balance-requests", controller.GetAdtyOwnerBalanceRequests},
		{http.MethodPost, "/adty/site/balance-requests/:id/review", controller.PostAdtyOwnerBalanceReview},
	} {
		middleware.DeclareAdtyAccessTokenRoute(route.method, "/api"+route.path, "session", "")
		handlers := []gin.HandlerFunc{middleware.UserAuth(), middleware.DisableCache()}
		if route.method == http.MethodPost && (route.path == "/adty/staff/customers/:id/password-reset" || route.path == "/adty/staff/customers/:id/disable") {
			handlers = append(handlers, middleware.UserCriticalRateLimit("CT:staff-customer-security"))
		} else if (route.method == http.MethodPost && route.path == "/adty/site/staff") || (route.method == http.MethodPut && route.path == "/adty/site/staff/:id/password") {
			handlers = append(handlers, middleware.UserCriticalRateLimit("CT:site-staff"))
		}
		if route.method == http.MethodPost && route.path == "/adty/site/balance-requests/:id/review" {
			handlers = append(handlers, middleware.UserCriticalRateLimit("CT:site-balance-review"))
		}
		apiRouter.Handle(route.method, route.path, append(handlers, route.handler)...)
	}
	// R7-035: Mount Root balance-request endpoints under RootAuth so failed Root
	// review attempts also flow through beginAdminAudit/finishAdminAudit.
	middleware.DeclareAdtyAccessTokenRoute(http.MethodGet, "/api/adty/admin/balance-requests", "session", "")
	middleware.DeclareAdtyAccessTokenRoute(http.MethodPost, "/api/adty/admin/balance-requests/:id/review", "session", "")
	apiRouter.GET("/adty/admin/balance-requests", middleware.RootAuth(), middleware.DisableCache(), controller.GetAdtyAdminBalanceRequests)
	apiRouter.POST("/adty/admin/balance-requests/:id/review", middleware.RootAuth(), middleware.DisableCache(), controller.PostAdtyAdminBalanceReview)

	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/reseller/tiers", "scope", "option:read")
	middleware.DeclareAdtyAccessTokenRoute("PUT", "/api/adty/admin/reseller/tiers", "scope", "option:write")
	root := apiRouter.Group("/adty/admin/reseller", middleware.RootAuth())
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/reseller/tiers", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("PATCH", "/api/adty/admin/reseller/tiers", "scope", "option:write")
	root.PATCH("/tiers", controller.PutAdtySiteTiers)
	root.POST("/tiers", controller.PutAdtySiteTiers)
	root.GET("/tiers", controller.GetAdtySiteTiers)
	root.PUT("/tiers", controller.PutAdtySiteTiers)
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/bonus-methods", "scope", "option:read")
	middleware.DeclareAdtyAccessTokenRoute("PUT", "/api/adty/admin/bonus-methods", "scope", "option:write")
	apiRouter.GET("/adty/admin/bonus-methods", middleware.RootAuth(), controller.GetAdtyMainMethodBonus)
	apiRouter.PUT("/adty/admin/bonus-methods", middleware.RootAuth(), controller.PutAdtyMainMethodBonus)
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/users/:id/expiring-gifts", "scope", service.AccessTokenScopeOf(authz.AdtyFinanceManualCredit))
	apiRouter.POST("/adty/admin/users/:id/expiring-gifts", middleware.RootAuth(), controller.PostAdtyRootExpiringGift)
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/sites/:id/wallets", "scope", "option:read")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/sites/:id/wallet-ledgers", "scope", "option:read")
	rootWallets := apiRouter.Group("/adty/admin/sites", middleware.RootAuth())
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/sites/paused-repairs", "scope", "option:read")
	rootWallets.GET("/paused-repairs", controller.ListAdtyPausedSiteRepairs)
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/sites/:id/paused-repair", "scope", "option:write")
	rootWallets.POST("/:id/paused-repair", controller.RepairAdtyPausedSite)
	middleware.DeclareAdtyAccessTokenRoute("PUT", "/api/adty/admin/sites/:id/bonus-methods", "scope", "option:write")
	rootWallets.PUT("/:id/bonus-methods", controller.PutAdtySiteBonusMethodAuth)
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/sites/:id/bonus-margin-fill", "scope", "option:read")
	middleware.DeclareAdtyAccessTokenRoute("PUT", "/api/adty/admin/sites/:id/bonus-margin-fill", "scope", "option:write")
	rootWallets.GET("/:id/bonus-margin-fill", controller.GetAdtySiteBonusMarginFill)
	rootWallets.PUT("/:id/bonus-margin-fill", controller.PutAdtySiteBonusMarginFill)
	rootWallets.GET("/:id/commission-margin", controller.GetAdtySiteCommissionMargin)
	rootWallets.PUT("/:id/commission-margin", controller.PutAdtySiteCommissionMargin)
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/sites/:id/close-mail", "scope", "option:read")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/sites/:id/close-mail", "scope", "option:write")
	rootWallets.GET("/:id/close-mail", controller.GetAdtyAdminCloseMail)
	rootWallets.POST("/:id/close-mail", controller.ResolveAdtyAdminCloseMail)
	rootWallets.GET("/:id/wallets", controller.GetAdtyAdminSiteWallets)
	rootWallets.GET("/:id/wallet-ledgers", controller.GetAdtyAdminSiteWalletLedger)
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/sites/:id/margin", "scope", "option:write")
	rootWallets.POST("/:id/margin", controller.PostAdtyAdminSiteMargin)
	middleware.DeclareAdtyAccessTokenRoute("PUT", "/api/adty/admin/sites/:id/tier", "scope", "option:write")
	rootWallets.PUT("/:id/tier", controller.PutAdtySiteTierAssignment)
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/sites/:id/tier-principal", "scope", "option:read")
	rootWallets.GET("/:id/tier-principal", controller.GetAdtyAdminSiteTierPrincipal)
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/sites/:id/tier-principal", "scope", "option:write")
	rootWallets.POST("/:id/tier-principal", controller.PostAdtyAdminSiteTierPrincipal)
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/sites", "scope", "option:write")
	rootWallets.POST("", controller.CreateAdtyAdminSite)
	middleware.DeclareAdtyAccessTokenRoute("DELETE", "/api/adty/admin/sites/:id", "scope", "option:write")
	rootWallets.DELETE("/:id", controller.DeleteAdtyAdminSite)
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/sites/:id/review", "scope", "option:write")
	rootWallets.POST("/:id/review", controller.ReviewAdtyAdminSite)
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/sites/:id/owner", "scope", "option:write")
	rootWallets.POST("/:id/owner", controller.HandoverAdtyAdminSiteOwner)
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/vouchers", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("PUT", "/api/adty/admin/vouchers/limit", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/vouchers/:id/freeze", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("POST", "/api/adty/admin/vouchers/:id/cancel", "scope", "option:write")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/vouchers", "scope", "option:read")
	middleware.DeclareAdtyAccessTokenRoute("GET", "/api/adty/admin/vouchers/limit", "scope", "option:read")
	rootVouchers := apiRouter.Group("/adty/admin/vouchers", middleware.RootAuth())
	rootVouchers.GET("", controller.GetAdtyAdminVouchers)
	rootVouchers.POST("", controller.PostAdtyAdminVoucher)
	rootVouchers.GET("/limit", controller.GetAdtyVoucherIssuanceLimit)
	rootVouchers.PUT("/limit", controller.PutAdtyVoucherIssuanceLimit)
	rootVouchers.POST("/:id/freeze", controller.PostAdtyAdminVoucherFreeze)
	rootVouchers.POST("/:id/cancel", controller.PostAdtyAdminVoucherCancel)

	admin := apiRouter.Group("/adty/admin", middleware.AdminAuth())
	for _, route := range []struct {
		method, path string
		permission   authz.Permission
		handler      gin.HandlerFunc
	}{
		{http.MethodGet, "/sites", authz.AdtySiteRead, controller.GetAdtyAdminSites},
		// 静态路径写在 /sites/:id 前面，gin 静态段优先。
		{http.MethodGet, "/sites/overview", authz.AdtySiteRead, controller.GetAdtyAdminSitesOverview},
		{http.MethodGet, "/sites/flagged-customers", authz.AdtySiteRead, controller.GetAdtyAdminFlaggedCustomers},
		{http.MethodGet, "/sites/reports", authz.AdtySiteRead, controller.GetAdtyAdminSiteReports},
		{http.MethodPost, "/sites/reports/:id/handle", authz.AdtySiteReview, controller.HandleAdtyAdminSiteReport},
		{http.MethodGet, "/sites/:id/stats", authz.AdtySiteRead, controller.GetAdtyAdminSiteStats},
		{http.MethodGet, "/sites/:id", authz.AdtySiteRead, controller.GetAdtyAdminSite},
		{http.MethodGet, "/sites/:id/customers", authz.AdtySiteRead, controller.GetAdtyAdminSiteCustomers},
		{http.MethodPost, "/sites/customers/:id/review", authz.AdtySiteReview, controller.ReviewAdtySiteCustomer},
		{http.MethodPut, "/sites/:id", authz.AdtySiteAdjust, controller.AdjustAdtyAdminSite},
		{http.MethodPut, "/sites/:id/status", authz.AdtySiteAdjust, controller.SetAdtyAdminSiteStatus},
		{http.MethodPost, "/sites/:id/settlement/resume", authz.AdtySiteAdjust, controller.ResumeAdtySiteSettlement},
		{http.MethodDelete, "/sites/:id/sender-domain", authz.AdtySiteDelete, controller.DisableAdtyAdminSiteSenderDomain},
		{http.MethodDelete, "/sites/domains/:id", authz.AdtySiteDelete, controller.DisableAdtyAdminSiteDomain},
		{http.MethodDelete, "/sites/:id/notice", authz.AdtySiteDelete, controller.ClearAdtyAdminSiteNotice},
		{http.MethodGet, "/reseller/usage", authz.AdtySiteRead, controller.GetAdtyResellerUsage},
		{http.MethodGet, "/reseller/settings", authz.AdtySiteSettingsRead, controller.GetAdtyResellerSettings},
		{http.MethodPut, "/reseller/settings", authz.AdtySiteSettings, controller.UpdateAdtyResellerSettings},
		{http.MethodPost, "/reseller/domains/check/:provider", authz.AdtySiteSettings, controller.CheckAdtyDomainProvider},
		{http.MethodPost, "/sites/domains/:id/retry", authz.AdtySiteSettings, controller.RetryAdtyDomainAutomation},
	} {
		handlePermissionRoute(admin, route.method, route.path, route.permission, route.handler)
	}
}

// SetAdtyWebRoutes 挂 /api 以外的二开页面（任务 11.3）：易支付付完跳回的 /pay/result/:token、按站点返回的 /robots.txt
// 和站长 Logo。
// 在 SetWebRouter 的 NoRoute 之前注册，所以它们不经过前端静态文件。
func SetAdtyWebRoutes(router *gin.Engine) {
	router.GET("/pay/result", controller.AdtyPayResult) // 不带签名的一律跳主站钱包
	router.GET("/pay/result/:token", controller.AdtyPayResult)
	router.GET("/robots.txt", AdtyRobotsTxt())
	router.GET("/sitemap.xml", AdtySitemap())
	// 站长上传的 Logo（任务 11.3）：公开读取，文件名是随机的。
	router.GET("/uploads/site-assets/:name", controller.GetAdtySiteAsset)
}
