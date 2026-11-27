package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	passkeysvc "github.com/QuantumNous/new-api/service/passkey"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

func TestStatus(c *gin.Context) {
	err := model.PingDB()
	if err != nil {
		writeAccountStatusError(c, http.StatusServiceUnavailable, "Database connection failed")
		return
	}
	// 获取HTTP统计信息
	httpStats := middleware.GetStats()
	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"message":    "Server is running",
		"http_stats": httpStats,
	})
	return
}

// GetReady is the process readiness probe. It does not return configuration,
// database DSNs, or node inventory. A cached status response is not readiness.
func GetReady(c *gin.Context) {
	if err := model.PingDBImmediate(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "not ready",
		})
		return
	}
	if common.RedisEnabled {
		if err := common.PingRedis(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"message": "not ready",
			})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func GetStatus(c *gin.Context) {
	// 支付方式可能查通道表与场景表，必须在配置锁之外算。
	requestSiteID := service.AdtyRequestSiteID(c)
	paymentMethods := service.AdtyPublicWalletPaymentMethodIDsForSite(requestSiteID)
	// 分站 widget 要在拿配置锁之前查，避免锁里再查站点表。
	common.OptionMapRWMutex.RLock()
	turnstileSiteKey := common.TurnstileSiteKey
	common.OptionMapRWMutex.RUnlock()
	if choice := service.AdtyTurnstileFor(service.AdtySiteForRequest(c), c.Request.Host); choice.SiteKey != "" {
		turnstileSiteKey = choice.SiteKey
	}

	// 公告是一次真实的 DB 查询，同样必须在配置锁之外算（规则见上面 :69-72）：
	// `/api/status` 是匿名热路径，把数据库往返放在全局读锁里，会让保存设置的后台
	// 请求排在每个匿名页面加载后面。开关取自进程内快照，GetConsoleSetting 不拿锁，
	// 所以可以先看开关再决定要不要查。
	cs := console_setting.GetConsoleSetting()
	var publicAnnouncements []service.AdtyPublicAnnouncement
	if cs.AnnouncementsEnabled {
		publicAnnouncements = service.AdtyPublicAnnouncementsForRequest(c)
	}
	passkeySetting := system_setting.PasskeySettingsSnapshot()
	passkeyLoginAvailable := passkeySetting.Enabled && passkeysvc.IsAvailableForRequest(c.Request, "")
	userAgreementEnabled := service.AdtyMainLegalDocEnabled("user-agreement")
	privacyPolicyEnabled := service.AdtyMainLegalDocEnabled("privacy-policy")
	generalSetting := operation_setting.GetGeneralSetting()
	isCurrencyDisplay := operation_setting.IsCurrencyDisplay()
	quotaDisplayType := operation_setting.GetQuotaDisplayType()
	logSetting := operation_setting.GetLogSetting()
	serverAddress := service.AdtySiteAPIBaseURL(nil)
	referralPublic := service.AdtyPublicReferralInfoForSite(requestSiteID)
	referralEnabled := referralPublic.Enabled
	docsStaticMounted := service.AdtyStaticDocsMounted()
	// 锁只包住读 OptionMap / 全局变量的这段，读完立刻放（下面都是拼响应，不碰配置）。
	// 禁止在持锁期间调用任何内部加锁 getter（避免 Go RWMutex 递归 RLock 死锁）。
	common.OptionMapRWMutex.RLock()

	data := gin.H{
		"version":             common.Version,
		"start_time":          common.StartTime,
		"email_verification":  common.EmailVerificationEnabled,
		"telegram_bot_name":   common.TelegramBotName,
		"theme":               "default",
		"system_name":         common.SystemName,
		"logo":                common.Logo,
		"footer_html":         common.Footer,
		"server_address":      serverAddress,
		"turnstile_check":     common.TurnstileCheckEnabled,
		"turnstile_site_key":  turnstileSiteKey,
		"docs_link":           generalSetting.DocsLink,
		"docs_static_mounted": docsStaticMounted,
		"quota_per_unit":      common.QuotaPerUnit,
		// 兼容旧前端：保留 display_in_currency，同时提供新的 quota_display_type
		"display_in_currency":           isCurrencyDisplay,
		"quota_display_type":            quotaDisplayType,
		"custom_currency_symbol":        generalSetting.CustomCurrencySymbol,
		"custom_currency_exchange_rate": generalSetting.CustomCurrencyExchangeRate,
		"enable_batch_update":           common.BatchUpdateEnabled,
		"enable_drawing":                common.DrawingEnabled,
		"enable_task":                   common.TaskEnabled,
		"enable_data_export":            common.DataExportEnabled,
		"data_export_default_time":      common.DataExportDefaultTime,
		"default_collapse_sidebar":      common.DefaultCollapseSidebar,
		"mj_notify_enabled":             setting.MjNotifyEnabled,
		"demo_site_enabled":             operation_setting.GetDemoSiteEnabled(),
		"self_use_mode_enabled":         operation_setting.GetSelfUseModeEnabled(),
		"register_enabled":              common.RegisterEnabled,
		"password_login_enabled":        common.PasswordLoginEnabled,
		"password_register_enabled":     common.PasswordRegisterEnabled,

		"password_login_encryption_enabled": common.PasswordLoginEncryptionEnabled,

		"usd_exchange_rate": operation_setting.GetUSDExchangeRate(),
		"price":             operation_setting.GetPrice(),

		// 面板启用开关
		"uptime_kuma_enabled":   cs.UptimeKumaEnabled,
		"announcements_enabled": cs.AnnouncementsEnabled,
		"faq_enabled":           cs.FAQEnabled,

		// 模块管理配置
		"HeaderNavModules":    common.OptionMap["HeaderNavModules"],
		"SidebarModulesAdmin": common.OptionMap["SidebarModulesAdmin"],

		"passkey_login":             passkeyLoginAvailable,
		"passkey_display_name":      passkeySetting.RPDisplayName,
		"passkey_rp_id":             passkeySetting.EffectiveRPID(),
		"passkey_rp_ids":            passkeySetting.RelyingPartyIDs(),
		"passkey_allow_insecure":    passkeySetting.AllowInsecureOrigin,
		"passkey_user_verification": passkeySetting.UserVerification,
		"passkey_attachment":        passkeySetting.AttachmentPreference,
		"setup":                     constant.Setup,
		"user_agreement_enabled":    userAgreementEnabled,
		"privacy_policy_enabled":    privacyPolicyEnabled,
		"log_export_enabled":        logSetting.UserExportEnabled,
		"log_max_range_days":        logSetting.MaxRangeDays,
		"quota_remind_enabled":      common.QuotaRemindEnabled,
		"quota_remind_threshold":    common.QuotaRemindThreshold,
		"payment_methods":           paymentMethods,
		"adty_referral_enabled":     referralEnabled,
		"adty_referral_public":      referralPublic,
	}
	common.OptionMapRWMutex.RUnlock()

	// 根据启用状态注入可选内容
	if cs.AnnouncementsEnabled {
		// 从这里起数据源是 `adty_notification_publications`，不再是
		// `console_setting.announcements`（那个 option 只作为回滚源保留）。
		// 形状保持不变：这条路由没有认证，公开面板靠它给未登录访客看公告，
		// 换成需要登录的新接口会让匿名访客看不到公告——那是行为退化。
		// 按 Host 判断站点可见性，所以分站只会读到自己的和平台级公告。
		data["announcements"] = publicAnnouncements
	}
	if cs.FAQEnabled {
		data["faq"] = console_setting.GetFAQ()
	}

	service.AdtyOverrideStatus(c, data) // 二开：分站的站名、Logo、地址，关掉第三方登录（任务 11.2）

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    data,
	})
	return
}

func GetNotice(c *gin.Context) {
	if site := service.AdtySiteForRequest(c); site != nil {
		serveRevalidatedJSON(c, strings.TrimSpace(service.AdtyBrandFor(site).Notice))
		return
	}
	common.OptionMapRWMutex.RLock()
	notice := common.OptionMap["Notice"]
	common.OptionMapRWMutex.RUnlock()
	serveRevalidatedJSON(c, notice)
}

func GetAbout(c *gin.Context) {
	if service.AdtySiteForRequest(c) != nil {
		serveRevalidatedJSON(c, "")
		return
	}
	common.OptionMapRWMutex.RLock()
	about := common.OptionMap["About"]
	common.OptionMapRWMutex.RUnlock()
	serveRevalidatedJSON(c, about)
}

func GetUserAgreement(c *gin.Context) {
	lang := service.AdtyRequestLegalLang(c)
	if site := service.AdtySiteForRequest(c); site != nil {
		legal, err := service.AdtySiteLegalForWithError(site)
		if err != nil {
			common.SysError(common.LogText("failed to load legal docs for site %d: %s", site.Id, err.Error()))
			common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Site legal documents are temporarily unavailable"))
			return
		}
		serveRevalidatedJSON(c, service.AdtyResolveLegalText(legal.UserAgreement, lang))
		return
	}
	if text := service.AdtyResolveLegalText(adty_setting.GetLegalSetting().UserAgreement, lang); text != "" {
		serveRevalidatedJSON(c, text)
		return
	}
	serveRevalidatedJSON(c, system_setting.GetLegalSettings().UserAgreement)
}

func GetPrivacyPolicy(c *gin.Context) {
	lang := service.AdtyRequestLegalLang(c)
	if site := service.AdtySiteForRequest(c); site != nil {
		legal, err := service.AdtySiteLegalForWithError(site)
		if err != nil {
			common.SysError(common.LogText("failed to load legal docs for site %d: %s", site.Id, err.Error()))
			common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Site legal documents are temporarily unavailable"))
			return
		}
		serveRevalidatedJSON(c, service.AdtyResolveLegalText(legal.PrivacyPolicy, lang))
		return
	}
	if text := service.AdtyResolveLegalText(adty_setting.GetLegalSetting().PrivacyPolicy, lang); text != "" {
		serveRevalidatedJSON(c, text)
		return
	}
	serveRevalidatedJSON(c, system_setting.GetLegalSettings().PrivacyPolicy)
}

func GetMidjourney(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    common.OptionMap["Midjourney"],
	})
	return
}

func GetHomePageContent(c *gin.Context) {
	if site := service.AdtySiteForRequest(c); site != nil {
		serveRevalidatedJSON(c, site.HomePageContent)
		return
	}
	common.OptionMapRWMutex.RLock()
	homePageContent := common.OptionMap["HomePageContent"]
	common.OptionMapRWMutex.RUnlock()
	serveRevalidatedJSON(c, homePageContent)
}

func SendEmailVerification(c *gin.Context) {
	email, err := service.ValidateAccountEmail(c.Query("email"))
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	if err := common.VerificationStoreReady(); err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Please try again later"))
		return
	}
	reqSite := service.AdtySiteForRequest(c)
	if err := service.EnsureAdtySiteMailConfigured(reqSite); err != nil {
		common.ApiError(c, err)
		return
	}
	// 二开：已注册的邮箱不发验证码，改发一封提醒，响应和正常情况完全一样（任务 7.3）
	siteID := service.AdtyRequestSiteID(c)
	if model.IsEmailAlreadyTaken(email, siteID) {
		service.AdtySendRegisteredReminder(c, email)
		c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
		return
	}
	code := common.GenerateVerificationCode(6)
	if err := common.RegisterVerificationCodeWithKey(model.SiteEmailCodeKey(email, siteID), code, common.EmailVerificationPurpose); err != nil {
		// Same empty success as a registered address. A 503 here would show that the address is new.
		c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
		return
	}
	// Registration happens before an account exists; use the request site.
	site := reqSite
	subject := service.AdtyBrandFor(site).Name + " email verification"
	content := service.AdtyRenderMail(site, service.AdtyMailMessage{
		Title:    "Verify your email address",
		Intro:    "Enter this code to verify your email address and finish signing up.",
		Code:     code,
		Footnote: fmt.Sprintf("This code expires in %d minutes. If you did not request it, ignore this email.", common.VerificationValidMinutes),
	})
	// 二开：异步发信，响应时间不暴露邮箱是否存在（任务 7.3）；发件人按站点（任务 11.3）
	service.AdtySendSiteEmailAsync(site, subject, email, content)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func SendPasswordResetEmail(c *gin.Context) {
	email := model.NormalizeEmail(c.Query("email"))
	if err := common.Validate.Var(email, "required,email"); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := common.VerificationStoreReady(); err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Please try again later"))
		return
	}
	if err := service.EnsureAdtySiteMailConfigured(service.AdtySiteForRequest(c)); err != nil {
		common.ApiError(c, err)
		return
	}
	username := strings.TrimSpace(c.Query("username"))
	user, err := model.ResolveUserForEmailChallenge(email, username, service.AdtyRequestSiteID(c))
	switch {
	case err == nil:
		if sendErr := sendPasswordResetInstructions(c, user); errors.Is(sendErr, service.ErrAdtySiteMailNotConfigured) {
			common.ApiError(c, sendErr)
			return
		}
	case errors.Is(err, model.ErrEmailAmbiguous):
		common.ApiErrorStatus(c, http.StatusOK, common.NewMessage(model.EmailAmbiguousPrompt), gin.H{"code": model.EmailAmbiguousCode})
		return
	case errors.Is(err, model.ErrEmailNotFound):
		// Preserve the same success response for unknown addresses.
	default:
		logger.LogWarn(c.Request.Context(), common.LogText("skip password reset email for %s: %s", email, err.Error()))
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

// Self-service recovery issues a one-time reset link. No caller, including
// staff, receives the reset token or chooses the customer's new password.
func sendPasswordResetInstructions(c *gin.Context, user *model.User) error {
	current, err := model.GetUserById(user.Id, false)
	if err != nil || current == nil || current.Id != user.Id || current.Status != common.UserStatusEnabled {
		return common.NewMessage("Invalid parameters")
	}
	email := model.NormalizeEmail(current.Email)
	site := service.AdtySiteById(current.SiteId)
	if current.SiteId == 0 {
		if reqSite := service.AdtySiteForRequest(c); reqSite != nil && reqSite.OwnerUserId == current.Id &&
			reqSite.Status != model.AdtySiteStatusClosed && reqSite.Status != model.AdtySiteStatusDeleting && reqSite.Status != model.AdtySiteStatusPending {
			site = reqSite
		}
	}
	if current.SiteId > 0 && !service.AdtySiteServesCustomers(site) {
		return common.NewMessage("Site not found")
	}
	if err := service.EnsureAdtySiteMailConfigured(site); err != nil {
		return err
	}
	code := common.GenerateVerificationCode(0)
	if err := common.RegisterVerificationCodeWithKey(model.PasswordResetCodeKey(current), code, common.PasswordResetPurpose); err != nil {
		return err
	}
	// Derive branding and URL from SQL membership, never from the request Host.
	link := fmt.Sprintf("%s/user/reset#email=%s&token=%s&username=%s", service.AdtySitePublicBaseURL(site), url.QueryEscape(email), url.QueryEscape(code), url.QueryEscape(current.Username))
	subject := service.AdtyBrandFor(site).Name + " password reset"
	content := service.AdtyRenderMail(site, service.AdtyMailMessage{
		Title:       "Reset your password",
		Intro:       "Use the link below to choose a new password for your account.",
		ActionLabel: "Reset password",
		ActionURL:   link,
		Footnote:    fmt.Sprintf("This link expires in %d minutes. If you did not request a password reset, ignore this email.", common.VerificationValidMinutes),
	})
	service.AdtySendSiteEmailAsync(site, subject, email, content)
	writePasswordResetChallengeAudit(c, current)
	return nil
}

type PasswordResetRequest struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Token    string `json:"token"`
	Password string `json:"password"`
}

func ResetPassword(c *gin.Context) {
	var req PasswordResetRequest
	err := common.DecodeJson(c.Request.Body, &req)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	req.Email = model.NormalizeEmail(req.Email)
	req.Username = strings.TrimSpace(req.Username)
	if (req.Email == "" && req.Username == "") || req.Token == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := common.Validate.StructPartial(&model.User{Password: req.Password}, "Password"); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	siteID := service.AdtyRequestSiteID(c)
	user, err := model.ResolveUserForEmailChallenge(req.Email, req.Username, siteID)
	if err != nil {
		common.ApiErrorT(c, "Password reset link is invalid or has expired")
		return
	}
	codeKey := model.PasswordResetCodeKey(user)
	issued, ok := common.ConsumeVerificationCodeWithKey(codeKey, req.Token, common.PasswordResetPurpose)
	if !ok {
		if _, uniqueErr := model.GetUniqueUserByEmail(user.Email, siteID); uniqueErr == nil {
			legacyKey := model.SiteEmailCodeKey(user.Email, siteID)
			issued, ok = common.ConsumeVerificationCodeWithKey(legacyKey, req.Token, common.PasswordResetPurpose)
			if ok {
				codeKey = legacyKey
			}
		}
	}
	if !ok {
		common.ApiErrorT(c, "Password reset link is invalid or has expired")
		return
	}
	outcome, err := model.ResetUserPasswordByIdentity(user.Email, user.Username, req.Password, siteID)
	if outcome.UserID > 0 {
		committed, notificationFailed := outcome.State == model.PasswordResetCommitted, false
		if committed {
			notificationFailed = service.NotifyAccountSecurityChange(i18n.LangEn, outcome.Email, outcome.UserID, i18n.MsgSecurityEventPasswordUpdated) != nil
		}
		c.Set("role", outcome.Role)
		defer recordUserSecurityAudit(c, outcome.UserID, "user.password_change", map[string]any{
			"password_reset": true, "success": committed, "notification_failed": notificationFailed,
		})
	}
	if err != nil {
		if errors.Is(err, model.ErrEmailNotFound) || errors.Is(err, model.ErrEmailAmbiguous) {
			common.ApiErrorT(c, "Password reset link is invalid or has expired")
			return
		}
		if outcome.State == model.PasswordResetNotCommitted {
			common.RestoreVerificationCodeWithKey(codeKey, req.Token, common.PasswordResetPurpose, issued)
		}
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func writePasswordResetChallengeAudit(c *gin.Context, user *model.User) {
	if user == nil {
		return
	}
	rec := model.OfficialAuditWrite{
		UserId: user.Id, Username: user.Username, ActorRole: user.Role,
		SiteID:     model.OfficialAuditSiteIDPointer(user.SiteId),
		ObjectType: "user", ObjectID: strconv.Itoa(user.Id),
		Action: "adty.user.password_reset.challenge", Category: model.AuditCategoryOperation,
		Content: "adty.user.password_reset.challenge",
		After: map[string]any{
			"user_id": user.Id, "username": user.Username,
			"site_id": user.SiteId, "inherited_from_site_id": user.InheritedFromSiteId,
			"purpose": common.PasswordResetPurpose, "result": model.AuditResultSuccess,
		},
		Result: model.AuditResultSuccess, Success: true,
	}
	if err := model.WriteDurableOfficialAuditLog(c, rec); err != nil {
		common.SysLog(common.LogText("password reset challenge audit write failed: %s", err.Error()))
	}
}
