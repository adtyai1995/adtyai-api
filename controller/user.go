package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	passkeysvc "github.com/QuantumNous/new-api/service/passkey"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type LoginRequest struct {
	Username          string `json:"username"`
	Password          string `json:"password"`
	PasswordEncrypted string `json:"password_encrypted"`
	EncryptionKeyID   string `json:"encryption_key_id"`
}

func GetPasswordEncryptionKey(c *gin.Context) {
	if !common.PasswordLoginEncryptionEnabled {
		common.ApiSuccess(c, gin.H{"enabled": false})
		return
	}
	keyID, publicKey := common.PasswordEncryptionPublicKey()
	if keyID == "" || publicKey == "" {
		common.ApiErrorT(c, "Database error, please contact the administrator")
		return
	}
	common.ApiSuccess(c, gin.H{
		"enabled":    true,
		"kid":        keyID,
		"public_key": publicKey,
	})
}

func Login(c *gin.Context) {
	if !common.GetPasswordLoginEnabled() {
		common.ApiErrorT(c, "Password login has been disabled by administrator")
		return
	}
	var loginRequest LoginRequest
	err := common.DecodeJson(c.Request.Body, &loginRequest)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	username := loginRequest.Username
	password := loginRequest.Password
	if common.PasswordLoginEncryptionEnabled {
		if loginRequest.PasswordEncrypted == "" || loginRequest.EncryptionKeyID == "" {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		password, err = common.DecryptPassword(loginRequest.PasswordEncrypted, loginRequest.EncryptionKeyID)
		if err != nil {
			common.ApiErrorT(c, "Username or password is incorrect, or user has been banned")
			return
		}
	}
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	siteID := service.AdtyRequestSiteID(c)
	candID, hasCand := model.LookupLoginCandidateUserID(username, siteID)
	if adtyLoginLocked(username) || (hasCand && adtyLoginLockedUser(candID)) {
		common.ApiError(c, adtyLoginFailureResponse())
		return
	}
	user := model.User{
		Username: username,
		Password: password,
		SiteId:   siteID,
	}
	err = user.ValidateAndFill()
	if err != nil {
		switch {
		case errors.Is(err, model.ErrDatabase):
			common.SysLog(common.LogText("Login database error for user %s: %v", username, err))
			common.ApiErrorT(c, "Database error, please contact the administrator")
		case errors.Is(err, model.ErrUserEmptyCredentials):
			common.ApiErrorT(c, "Invalid parameters")
		case errors.Is(err, model.ErrEmailAmbiguous):
			common.ApiErrorStatus(c, http.StatusOK, common.NewMessage(model.EmailAmbiguousPrompt), gin.H{"code": model.EmailAmbiguousCode})
		default:
			lockUnavailable := false
			if recErr := adtyRecordLoginFailure(username); recErr != nil {
				common.SysError(common.LogText("login failure lock unavailable: %s", recErr.Error()))
				lockUnavailable = true
			}
			if hasCand {
				if recErr := adtyRecordLoginFailureUser(candID); recErr != nil {
					common.SysError(common.LogText("login failure lock unavailable: %s", recErr.Error()))
					lockUnavailable = true
				}
			}
			if lockUnavailable {
				common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Please try again later"))
				return
			}
			common.ApiError(c, adtyLoginFailureResponse())
		}
		return
	}
	adtyClearLoginFailures(username)
	adtyClearLoginFailuresUser(user.Id)
	if user.Username != "" {
		adtyClearLoginFailures(user.Username)
	}

	setupLogin(&user, c)
}

// loginMethodFromContext 根据请求路径推导登录方式，用于登录审计日志。
func loginMethodFromContext(c *gin.Context) string {
	if method := c.GetString("login_method"); method != "" {
		return method
	}
	switch c.FullPath() {
	case "/api/user/login":
		return "password"
	case "/api/user/login/2fa":
		return "2fa"
	case "/api/user/passkey/login/finish":
		return "passkey"
	case "/api/oauth/telegram/login":
		return "telegram"
	case "/api/oauth/:provider":
		if provider := c.Param("provider"); provider != "" {
			return "oauth:" + provider
		}
		return "oauth"
	default:
		return "unknown"
	}
}

// recordLoginAudit 记录登录成功审计日志（对所有用户启用，仅记录成功，不记录失败）。
func recordLoginAudit(user *model.User, c *gin.Context) {
	method := loginMethodFromContext(c)
	ip := c.ClientIP()
	extra := model.AuditOther{
		LoginMethod: method,
		UserAgent:   c.Request.UserAgent(),
	}
	content := fmt.Sprintf("Logged in successfully via %s", method)
	params := map[string]any{
		"method": method,
	}
	if verifiedMethod := c.GetString("login_verification_method"); verifiedMethod != "" {
		params["verification_method"] = verifiedMethod
	}
	model.RecordLoginLog(user.Id, user.Role, user.Username, content, ip, "login", params, extra, c)
}

// setupLogin evaluates the shared login policy after primary authentication.
// Only a completed Passkey ceremony may go directly to session issuance.
func setupLogin(user *model.User, c *gin.Context) {
	// 二开：在发二次验证之前检查，不让别站的账号拿到 2FA 挑战（任务 11.2）
	if user != nil && user.Id > 0 && !service.AdtySiteLoginAllowed(c, user.Id) {
		common.ApiErrorT(c, "Username or password is incorrect, or user has been banned")
		return
	}
	siteID := service.AdtyRequestSiteID(c)
	passkeyAvailable := true
	passkeyReason := ""
	if siteID == 0 && user != nil && user.Id > 0 {
		credRPID := ""
		if cred, credErr := model.GetPasskeyByUserID(user.Id); credErr == nil && cred != nil && cred.RPID != nil {
			credRPID = *cred.RPID
		}
		if !passkeysvc.IsAvailableForRequest(c.Request, credRPID) {
			passkeyAvailable = false
			passkeyReason = system_setting.ErrPasskeyRPIDUnavailable.Error()
		}
	}
	challenge, err := service.StartLoginVerificationForRequest(user, loginMethodFromContext(c), siteID, passkeyAvailable, passkeyReason)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if challenge != nil {
		setAuthNoStore(c)
		common.ApiSuccess(c, challenge)
		return
	}
	setupLoginAtAuthVersion(user, user.AuthVersion, c)
}

func setupLoginAtAuthVersion(user *model.User, expectedAuthVersion int64, c *gin.Context) {
	if user == nil || user.Id <= 0 || user.Status != common.UserStatusEnabled {
		common.ApiErrorT(c, "User has been banned")
		return
	}
	// 二开：分站客户只能在自己的分站登录（任务 11.2）
	if !service.AdtySiteLoginAllowed(c, user.Id) {
		common.ApiErrorT(c, "Username or password is incorrect, or user has been banned")
		return
	}
	currentUser, err := model.GetSelfUserById(user.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	bundle, err := service.CreateLoginSessionForRequest(
		c,
		user.Id,
		expectedAuthVersion,
		loginMethodFromContext(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)
	if err != nil {
		writeAuthSessionError(c, err)
		return
	}
	writeLoginResponse(c, currentUser, bundle)
}

func writeLoginResponse(c *gin.Context, user *model.User, bundle *service.AuthBundle) {
	c.Set("login_method", bundle.Session.LoginMethod)
	model.UpdateUserLastLoginAt(user.Id)
	service.WriteRefreshCookie(c, bundle.RefreshToken)
	setAuthNoStore(c)
	recordLoginAudit(user, c)
	// 二开：登录指纹（任务 6.5）
	service.AdtyRecordLoginFingerprint(c, user.Id)
	c.JSON(http.StatusOK, gin.H{
		"message": "",
		"success": true,
		"data": gin.H{
			"access_token":      bundle.AccessToken,
			"token_type":        bundle.TokenType,
			"access_expires_at": bundle.AccessExpiresAt,
			"session":           bundle.Session,
			"user":              buildSelfUserData(user),
		},
	})
}

func Register(c *gin.Context) {
	if !common.GetRegisterEnabled() {
		common.ApiErrorT(c, "New user registration has been disabled by administrator")
		return
	}
	if !common.GetPasswordRegisterEnabled() {
		common.ApiErrorT(c, "Password registration has been disabled by administrator")
		return
	}
	var payload struct {
		model.User
		AgreementAccepted bool    `json:"agreement_accepted"`
		SiteInviteCode    *string `json:"site_invite_code"`
	}
	err := common.DecodeJson(c.Request.Body, &payload)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	user := payload.User
	if service.AdtyLegalConsentRequiredForRequest(c) && !payload.AgreementAccepted {
		common.ApiErrorT(c, "Accept the current terms first")
		return
	}
	user.Username = strings.TrimSpace(user.Username)
	user.Email = model.NormalizeEmail(user.Email)
	if user.Username == "" || !model.ValidateUsernameNoControl(user.Username) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := common.Validate.Struct(&user); err != nil {
		common.ApiErrorT(c, "Invalid input {{error}}", map[string]any{"error": err.Error()})
		return
	}
	// 二开：在分站注册就归这个站（任务 11.2）；关站中的分站不再接受注册
	siteId, registrable := service.AdtyRegisterSiteId(c)
	if !registrable {
		common.ApiErrorT(c, "New user registration has been disabled by administrator")
		return
	}
	if common.GetEmailVerificationEnabled() {
		if user.Email == "" || user.VerificationCode == "" {
			common.ApiErrorT(c, "Email verification is enabled, please enter email address and verification code")
			return
		}
		// G.14.5: consume on a successful check. If a later register write
		// fails, the client must request a new code; do not restore this one.
		_, status := common.ConsumeVerificationCodeWithKeyStatus(model.SiteEmailCodeKey(user.Email, siteId), user.VerificationCode, common.EmailVerificationPurpose)
		if status != common.VerificationOK {
			if status == common.VerificationLocked {
				common.ApiErrorT(c, service.ErrVerificationCodeLocked.Error())
				return
			}
			common.ApiErrorT(c, "Verification code is incorrect or has expired")
			return
		}
		if err := model.EnsureEmailAvailable(user.Email, siteId, 0); err != nil {
			if errors.Is(err, model.ErrEmailAlreadyTaken) {
				// 二开：和验证码错误同一句话，不暴露邮箱已注册（任务 7.3）
				common.ApiErrorT(c, "Verification code is incorrect or has expired")
				return
			}
			common.ApiErrorT(c, "Database error, please contact the administrator")
			return
		}
	}
	emailForExistCheck := ""
	if common.GetEmailVerificationEnabled() {
		emailForExistCheck = user.Email
	}
	exist, err := model.CheckUserExistOrDeleted(user.Username, emailForExistCheck, siteId)
	if err != nil {
		common.ApiErrorT(c, "Database error, please contact the administrator")
		common.SysLog(common.LogText("CheckUserExistOrDeleted error: %v", err))
		return
	}
	if exist {
		common.ApiErrorT(c, "Username already exists or has been deleted")
		return
	}
	cleanUser := model.User{
		Username:    user.Username,
		Password:    user.Password,
		DisplayName: user.Username,
		Role:        common.RoleCommonUser, // 明确设置角色为普通用户
		Group:       "",
		SiteId:      siteId,
	}
	if common.GetEmailVerificationEnabled() {
		cleanUser.Email = user.Email
	}
	if !service.AdtyRegisterTermsAccepted(c, siteId) {
		common.ApiErrorT(c, "Accept the current site customer terms first")
		return
	}
	if err := cleanUser.Insert(0); err != nil {
		if errors.Is(err, model.ErrSiteRegistrationUnavailable) {
			common.ApiErrorT(c, "New user registration has been disabled by administrator")
			return
		}
		if errors.Is(err, model.ErrEmailAlreadyTaken) {
			common.ApiErrorT(c, "Verification code is incorrect or has expired") // 二开：任务 7.3
			return
		}
		if errors.Is(err, model.ErrUsernameAlreadyTaken) {
			common.ApiErrorT(c, "Username already exists or has been deleted")
			return
		}
		common.ApiError(c, err)
		return
	}

	// 获取插入后的用户ID
	var insertedUser model.User
	if cleanUser.Id > 0 {
		if err := model.DB.First(&insertedUser, cleanUser.Id).Error; err != nil {
			common.ApiErrorT(c, "User registration failed or user ID retrieval failed")
			return
		}
	} else if err := model.DB.Where("LOWER(username) = ?", model.NormalizeUsername(cleanUser.Username)).First(&insertedUser).Error; err != nil {
		common.ApiErrorT(c, "User registration failed or user ID retrieval failed")
		return
	}
	// 二开：注册即推广员、按推广 cookie 绑定、记指纹（任务 6.2、6.5）
	service.AdtyAfterRegister(c, &insertedUser)
	if insertedUser.SiteId > 0 {
		nowUnix := common.GetTimestamp()
		cookieRaw, cookieErr := c.Cookie(service.AdtySiteInviteCookie)
		hasCookie := cookieErr == nil && strings.TrimSpace(cookieRaw) != ""
		siteInviteCode := ""
		fromCookie := false
		var cookieCapturedAt int64
		if payload.SiteInviteCode != nil {
			// Explicit user input (including explicit empty string "" when the user clears the input) overrides cookie attribution.
			siteInviteCode = strings.TrimSpace(*payload.SiteInviteCode)
		} else if explicitQueryCode := strings.TrimSpace(c.Query("site_invite")); explicitQueryCode != "" {
			siteInviteCode = explicitQueryCode
		} else if hasCookie {
			if parsedCode, capturedAt, ok := service.ParseAdtySiteInviteCookie(insertedUser.SiteId, cookieRaw, nowUnix); ok {
				siteInviteCode = parsedCode
				cookieCapturedAt = capturedAt
				fromCookie = true
			}
		}
		if siteInviteCode != "" {
			if fromCookie && service.AdtySiteInviteCookieExpired(insertedUser.SiteId, cookieCapturedAt, nowUnix) {
				common.SysLog(common.LogText("site invite bind on register skipped user_id=%d site_id=%d: invite cookie expired", insertedUser.Id, insertedUser.SiteId))
			} else if bindErr := service.BindSiteInviteByCodeFromIP(insertedUser.SiteId, siteInviteCode, insertedUser.Id, service.HashReferralRiskValue(c.ClientIP())); bindErr != nil {
				common.SysLog(common.LogText("site invite bind on register skipped user_id=%d site_id=%d: %s", insertedUser.Id, insertedUser.SiteId, bindErr.Error()))
			}
		}
		if hasCookie {
			service.ClearAdtySiteInviteCookie(c)
		}
	}
	if payload.AgreementAccepted {
		model.RecordLog(insertedUser.Id, model.LogTypeSystem, common.NewMessage("Accepted the current user agreement and privacy policy"))
	}
	// 生成默认令牌（R2-46：仅在存在可用非 auto 分组时为该分组创建默认 Key，绝不创建空分组 Key）
	if constant.GenerateDefaultToken {
		usableGroups := service.GetUserUsableGroups(insertedUser.Group)
		defaultGroup := ""
		groupNames := make([]string, 0, len(usableGroups))
		for g := range usableGroups {
			trimmed := strings.TrimSpace(g)
			if trimmed == "" || trimmed == "auto" || !service.SiteGroupAllowed(insertedUser.SiteId, trimmed) {
				continue
			}
			groupNames = append(groupNames, trimmed)
		}
		slices.Sort(groupNames)
		if len(groupNames) > 0 {
			defaultGroup = groupNames[0]
		}
		if defaultGroup != "" {
			key, err := common.GenerateKey()
			if err != nil {
				common.ApiErrorT(c, "Failed to generate default token")
				common.SysLog(common.LogText("failed to generate token key: %s", err.Error()))
				return
			}
			token := model.Token{
				UserId:             insertedUser.Id, // 使用插入后的用户ID
				Name:               i18n.T(c, i18n.MsgUserDefaultTokenName, map[string]any{"Username": cleanUser.Username}),
				Key:                key,
				Group:              defaultGroup,
				CreatedTime:        common.GetTimestamp(),
				AccessedTime:       common.GetTimestamp(),
				ExpiredTime:        -1,     // 永不过期
				RemainQuota:        500000, // 示例额度
				UnlimitedQuota:     true,
				ModelLimitsEnabled: false,
			}
			token.AllowUserQuotaQuery = true
			if err := token.InsertWithLimit(operation_setting.GetMaxUserTokens()); err != nil && !errors.Is(err, model.ErrTokenLimit) {
				common.ApiErrorT(c, "Failed to create default token")
				return
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

type adminUserResponse struct {
	Id                  int                        `json:"id"`
	Username            string                     `json:"username"`
	DisplayName         string                     `json:"display_name"`
	Role                int                        `json:"role"`
	Status              int                        `json:"status"`
	Email               string                     `json:"email"`
	GitHubId            string                     `json:"github_id"`
	DiscordId           string                     `json:"discord_id"`
	OidcId              string                     `json:"oidc_id"`
	WeChatId            string                     `json:"wechat_id"`
	TelegramId          string                     `json:"telegram_id"`
	Quota               int                        `json:"quota"`
	LiveQuota           int                        `json:"live_quota"`
	UsedQuota           int                        `json:"used_quota"`
	RequestCount        int                        `json:"request_count"`
	Group               string                     `json:"group"`
	DeletedAt           gorm.DeletedAt             `json:"DeletedAt"`
	LinuxDOId           string                     `json:"linux_do_id"`
	Setting             string                     `json:"setting"`
	Remark              string                     `json:"remark,omitempty"`
	StripeCustomer      string                     `json:"stripe_customer"`
	CreatedAt           int64                      `json:"created_at"`
	LastLoginAt         int64                      `json:"last_login_at"`
	SiteId              int                        `json:"site_id"`
	SiteName            string                     `json:"site_name"`
	InheritedFromSiteId int                        `json:"inherited_from_site_id"`
	StaffSiteId         int                        `json:"staff_site_id"`
	IsTeamEmployee      bool                       `json:"is_team_employee"`
	AdtySiteOwner       bool                       `json:"adty_site_owner"`
	AdminPermissions    map[string]map[string]bool `json:"admin_permissions,omitempty"`
}

func sanitizeAdminUserSetting(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var parsed dto.UserSetting
	if err := common.UnmarshalJsonStr(trimmed, &parsed); err != nil {
		return ""
	}
	parsed.WebhookSecret = ""
	parsed.WebhookUrl = ""
	parsed.GotifyToken = ""
	parsed.GotifyUrl = ""
	parsed.BarkUrl = ""
	encoded, err := common.Marshal(parsed)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func sanitizeSelfUserSetting(raw string) (string, bool, bool, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false, false, false
	}
	var parsed map[string]any
	if err := common.UnmarshalJsonStr(trimmed, &parsed); err != nil || parsed == nil {
		return "", false, false, false
	}
	webhookSecret, _ := parsed["webhook_secret"].(string)
	barkUrl, _ := parsed["bark_url"].(string)
	gotifyToken, _ := parsed["gotify_token"].(string)
	hasWebhookSecret := strings.TrimSpace(webhookSecret) != ""
	hasBarkUrl := strings.TrimSpace(barkUrl) != ""
	hasGotifyToken := strings.TrimSpace(gotifyToken) != ""
	delete(parsed, "webhook_secret")
	delete(parsed, "bark_url")
	delete(parsed, "gotify_token")
	parsed["has_webhook_secret"] = hasWebhookSecret
	parsed["has_bark_url"] = hasBarkUrl
	parsed["has_gotify_token"] = hasGotifyToken
	encoded, err := common.Marshal(parsed)
	if err != nil {
		return "", hasWebhookSecret, hasBarkUrl, hasGotifyToken
	}
	return string(encoded), hasWebhookSecret, hasBarkUrl, hasGotifyToken
}

func buildAdminUserResponses(users []*model.User) ([]adminUserResponse, error) {
	siteIDs := make([]int, len(users))
	userIDs := make([]int, len(users))
	for i, user := range users {
		siteIDs[i] = user.SiteId
		userIDs[i] = user.Id
	}
	names, err := service.AdtySiteNames(siteIDs)
	if err != nil {
		return nil, err
	}
	teamEmployees, err := service.AdtyIndependentTeamEmployeeSet(userIDs)
	if err != nil {
		return nil, err
	}
	siteOwners := map[int]bool{}
	if len(userIDs) > 0 && model.DB != nil && model.DB.Migrator().HasTable(&model.AdtySite{}) {
		var ownerIDs []int
		if err := model.DB.Model(&model.AdtySite{}).
			Where("owner_user_id IN ? AND status NOT IN ?", userIDs, []string{model.AdtySiteStatusClosed, "deleted", "rejected"}).
			Distinct("owner_user_id").
			Pluck("owner_user_id", &ownerIDs).Error; err != nil {
			return nil, err
		}
		for _, oid := range ownerIDs {
			siteOwners[oid] = true
		}
	}
	items := make([]adminUserResponse, len(users))
	for i, user := range users {
		items[i] = adminUserResponse{
			Id:                  user.Id,
			Username:            user.Username,
			DisplayName:         user.DisplayName,
			Role:                user.Role,
			Status:              user.Status,
			Email:               user.Email,
			GitHubId:            user.GitHubId,
			DiscordId:           user.DiscordId,
			OidcId:              user.OidcId,
			WeChatId:            user.WeChatId,
			TelegramId:          user.TelegramId,
			Quota:               user.Quota,
			LiveQuota:           user.ToBaseUser().Quota,
			UsedQuota:           user.UsedQuota,
			RequestCount:        user.RequestCount,
			Group:               user.Group,
			DeletedAt:           user.DeletedAt,
			LinuxDOId:           user.LinuxDOId,
			Setting:             sanitizeAdminUserSetting(user.Setting),
			Remark:              user.Remark,
			StripeCustomer:      user.StripeCustomer,
			CreatedAt:           user.CreatedAt,
			LastLoginAt:         user.LastLoginAt,
			SiteId:              user.SiteId,
			SiteName:            names[user.SiteId],
			InheritedFromSiteId: user.InheritedFromSiteId,
			StaffSiteId:         user.StaffSiteId,
			IsTeamEmployee:      teamEmployees[user.Id],
			AdtySiteOwner:       siteOwners[user.Id],
			AdminPermissions:    user.AdminPermissions,
		}
	}
	return items, nil
}

// A present site_id must be a nonnegative integer; omission means every visible site.
func userSiteFilter(c *gin.Context) (*int, error) {
	value, present := c.GetQueryArray("site_id")
	if !present {
		return nil, nil
	}
	if len(value) != 1 || value[0] == "" || strings.IndexFunc(value[0], func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return nil, common.NewMessage("Invalid parameters")
	}
	siteID, err := strconv.Atoi(value[0])
	if err != nil {
		return nil, common.NewMessage("Invalid parameters")
	}
	return &siteID, nil
}

func GetUserSites(c *gin.Context) {
	belowRole := visibleUserRoleCeiling(c.GetInt("role"))
	openOnly := c.Query("open_only") == "1" || strings.EqualFold(c.Query("open_only"), "true")
	var (
		siteIDs []int
		err     error
	)
	if openOnly {
		siteIDs, err = model.GetOpenUserSiteIDs(belowRole)
	} else {
		siteIDs, err = model.GetUserSiteIDs(belowRole)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	names, err := service.AdtySiteNames(siteIDs)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	type siteChoice struct {
		SiteID   int    `json:"site_id"`
		SiteName string `json:"site_name"`
		IsMain   bool   `json:"is_main"`
	}
	items := make([]siteChoice, len(siteIDs))
	for i, id := range siteIDs {
		if id == 0 {
			items[i] = siteChoice{SiteID: 0, SiteName: "Main site", IsMain: true}
			continue
		}
		items[i] = siteChoice{SiteID: id, SiteName: names[id], IsMain: false}
	}
	common.ApiSuccess(c, items)
}

func GetAllUsers(c *gin.Context) {
	siteID, err := userSiteFilter(c)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, err)
		return
	}
	pageInfo := common.GetPageQuery(c)
	sortOptions := model.NewUserSortOptions(c.Query("sort_by"), c.Query("sort_order"))
	users, total, err := model.GetAllUsers(pageInfo, visibleUserRoleCeiling(c.GetInt("role")), siteID, sortOptions)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	items, err := buildAdminUserResponses(users)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)

	common.ApiSuccess(c, pageInfo)
	return
}

func SearchUsers(c *gin.Context) {
	siteID, err := userSiteFilter(c)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, err)
		return
	}
	keyword := c.Query("keyword")
	group := c.Query("group")
	var role *int
	if roleStr := c.Query("role"); roleStr != "" {
		if parsed, err := strconv.Atoi(roleStr); err == nil {
			role = &parsed
		}
	}
	var status *int
	if statusStr := c.Query("status"); statusStr != "" {
		if parsed, err := strconv.Atoi(statusStr); err == nil {
			status = &parsed
		}
	}
	pageInfo := common.GetPageQuery(c)
	sortOptions := model.NewUserSortOptions(c.Query("sort_by"), c.Query("sort_order"))
	users, total, err := model.SearchUsers(keyword, group, role, status, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), visibleUserRoleCeiling(c.GetInt("role")), siteID, sortOptions)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	items, err := buildAdminUserResponses(users)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
	return
}

func canManageTargetRole(myRole int, targetRole int) bool {
	return myRole == common.RoleRootUser || myRole > targetRole
}

// visibleUserRoleCeiling 是列表和搜索能看到的最高角色。根用户看全部，其它人看不到同级或更高角色。
func visibleUserRoleCeiling(myRole int) int {
	if myRole == common.RoleRootUser {
		return 0
	}
	return myRole
}

func GetUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid user ID")
		return
	}
	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	myRole := c.GetInt("role")
	if !canManageTargetRole(myRole, user.Role) {
		common.ApiErrorT(c, "No permission to access users of same or higher level")
		return
	}
	user.AdminPermissions = authz.Capabilities(user.Id, user.Role)
	items, err := buildAdminUserResponses([]*model.User{user})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    items[0],
	})
	return
}

func GetSelf(c *gin.Context) {
	id := c.GetInt("id")
	userRole := c.GetInt("role")
	user, err := model.GetSelfUserById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	responseData := buildSelfUserData(user)
	// The authenticated role is loaded from GetUserCache. It should equal the
	// row role, but use it for capabilities so GetSelf and login/refresh remain
	// consistent with the authorization decision made for this request.
	permissions := calculateUserPermissions(userRole)
	permissions["admin_permissions"] = authz.Capabilities(id, userRole)
	responseData["permissions"] = permissions

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    responseData,
	})
	return
}

// buildSelfUserData is the single safe dashboard-user DTO used by GetSelf,
// login and refresh. It intentionally excludes password, management PAT and
// administrator-only remarks.
func buildSelfUserData(user *model.User) map[string]any {
	userSetting := user.GetSetting()
	sanitizedSetting, hasWebhookSecret, hasBarkUrl, hasGotifyToken := sanitizeSelfUserSetting(user.Setting)
	permissions := calculateUserPermissions(user.Role)
	permissions["admin_permissions"] = authz.Capabilities(user.Id, user.Role)
	required, version := service.AdtySiteTermsStatus(user.Id)
	if user.StaffSiteId > 0 {
		required, version = false, "" // Staff use the dedicated workspace's site agreement gate.
	}
	data := map[string]any{
		"id":                       user.Id,
		"username":                 user.Username,
		"display_name":             user.DisplayName,
		"has_password":             user.HasPassword,
		"role":                     user.Role,
		"status":                   user.Status,
		"email":                    user.Email,
		"github_id":                user.GitHubId,
		"discord_id":               user.DiscordId,
		"oidc_id":                  user.OidcId,
		"wechat_id":                user.WeChatId,
		"telegram_id":              user.TelegramId,
		"group":                    user.Group,
		"quota":                    user.ToBaseUser().Quota,
		"used_quota":               user.UsedQuota,
		"request_count":            user.RequestCount,
		"linux_do_id":              user.LinuxDOId,
		"setting":                  sanitizedSetting,
		"has_webhook_secret":       hasWebhookSecret,
		"has_bark_url":             hasBarkUrl,
		"has_gotify_token":         hasGotifyToken,
		"stripe_customer":          user.StripeCustomer,
		"sidebar_modules":          userSetting.SidebarModules, // 正确提取sidebar_modules字段
		"permissions":              permissions,
		"adty_archived_site_owner":        service.AdtyHasArchivedSite(user.Id),
		"adty_site_owner":                 service.AdtyIsSiteOwner(user.Id), // 二开：侧边栏只对站长显示站长中心（任务 11.6）
		"adty_has_team":                   service.AdtyUserHasTeam(user.Id),
		"adty_referral_enabled":           service.IsAdtyReferralEnabledForSite(user.SiteId),
		"adty_has_legacy_site_commissions": service.AdtyUserHasLegacySiteCommissions(user.Id),
		"site_id":                  user.SiteId,
		"staff_site_id":            user.StaffSiteId,
		"site_terms_required":      required,
		"site_terms_version":       version,
	}
	attachWalletLotProjection(data, user)
	return data
}

// attachWalletLotProjection adds remaining 本金 and 永久赠金 from fund lots.
// The existing quota field stays the available-balance projection; this is not a second wallet.
func attachWalletLotProjection(data map[string]any, user *model.User) {
	proj, ok, err := service.ProjectWalletLotsForUser(user)
	if err != nil {
		common.SysLog(common.LogText("wallet lot projection: %s", err.Error()))
		return
	}
	if ok {
		data["wallet_lot_projection"] = proj
	}
}

// 计算用户权限的辅助函数
func calculateUserPermissions(userRole int) map[string]any {
	permissions := map[string]any{}

	// 根据用户角色计算权限
	if userRole == common.RoleRootUser {
		// 超级管理员不需要边栏设置功能
		permissions["sidebar_settings"] = false
		permissions["sidebar_modules"] = map[string]any{}
	} else if userRole == common.RoleAdminUser {
		// 管理员可以设置边栏，但不包含系统设置功能
		permissions["sidebar_settings"] = true
		permissions["sidebar_modules"] = map[string]any{
			"admin": map[string]any{
				"setting": false, // 管理员不能访问系统设置
			},
		}
	} else {
		// 普通用户只能设置个人功能，不包含管理员区域
		permissions["sidebar_settings"] = true
		permissions["sidebar_modules"] = map[string]any{
			"admin": false, // 普通用户不能访问管理员区域
		}
	}

	return permissions
}

func GetUserModels(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		id = c.GetInt("id")
	}
	user, err := model.GetUserCache(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	groups := service.GetUserUsableGroups(user.Group)
	group := c.Query("group")
	var groupsToQuery []string
	switch {
	case group == "":
		for g := range groups {
			if g != "auto" {
				groupsToQuery = append(groupsToQuery, g)
			}
		}
	case group == "auto":
		common.ApiErrorT(c, "Auto group has been retired; select a specific group")
		return
	default:
		if _, ok := groups[group]; ok {
			groupsToQuery = []string{group}
		}
	}
	siteID := service.AdtyRequestSiteID(c)
	if siteID == 0 {
		siteID = user.SiteId
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    service.GetSiteAllowedGroupModels(siteID, groupsToQuery),
	})
}

func UpdateUser(c *gin.Context) {
	var updatedUser model.User
	err := common.DecodeJson(c.Request.Body, &updatedUser)
	if err != nil || updatedUser.Id == 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	updatedUser.Username = strings.TrimSpace(updatedUser.Username)
	if updatedUser.Username == "" || !model.ValidateUsernameNoControl(updatedUser.Username) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := common.Validate.StructExcept(&updatedUser, "Password"); err != nil {
		common.ApiErrorT(c, "Invalid input {{error}}", map[string]any{"error": err.Error()})
		return
	}
	originUser, err := model.GetUserById(updatedUser.Id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if updatedUser.Role != common.RoleGuestUser && updatedUser.Role != originUser.Role {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	updatedUser.Role = originUser.Role
	myRole := c.GetInt("role")
	if !canManageTargetRole(myRole, originUser.Role) {
		common.ApiErrorT(c, "No permission to update users of same or higher permission level")
		return
	}
	if isAccessTokenRequest(c) && (originUser.Role >= common.RoleAdminUser || updatedUser.AdminPermissions != nil) {
		denyAdtyAdminAction(c)
		return
	}
	groupProvided := strings.TrimSpace(updatedUser.Group) != ""
	if !groupProvided {
		updatedUser.Group = originUser.Group
	} else {
		updatedUser.Group = strings.TrimSpace(updatedUser.Group)
	}
	// 二开：改分组要用户·改分组，目标分组要在分组表里（任务 2.6）
	if !adtyCheckUserUpdate(c, originUser, &updatedUser) {
		return
	}
	updatePassword := updatedUser.Password != ""
	auditParams := map[string]any{
		"username": originUser.Username,
		"id":       updatedUser.Id,
	}
	// Resetting a password or rewriting the admin permission matrix changes what
	// the managed account can do or who can sign in to it.
	if updatePassword || updatedUser.AdminPermissions != nil {
		authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserUpdate, service.AdminUserContext{UserID: updatedUser.Id})
		if authorization == nil {
			return
		}
		auditParams["verification_method"] = authorization.Method
		auditParams["password_reset"] = updatePassword
		auditParams["admin_permissions_updated"] = updatedUser.AdminPermissions != nil
	}
	authzTouched := false
	auditEvents := make([]model.AuditLog, 0, 2)
	var ticketAudits []model.AuditLog
	previousAuthVersion := originUser.AuthVersion
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		// Maintain canonical lock order adty_groups -> users only when group is explicitly provided (R6-05 / R7-18).
		// Allow locking an existing soft-deleted group row so callers echoing the user's
		// unchanged legacy group are not rejected before comparing against current.Group.
		targetGroupDeleted := false
		if groupProvided && updatedUser.Group != "" {
			var err error
			targetGroupDeleted, err = model.PreLockAdtyGroupRowAllowDeletedTx(tx, updatedUser.Group)
			if err != nil {
				return err
			}
		}
		if err := model.PreLockScopedEmployeeRowsTx(tx, updatedUser.Id); err != nil {
			return err
		}
		var current model.User
		if err := model.AdtyLockForUpdate(tx).First(&current, updatedUser.Id).Error; err != nil {
			return err
		}
		if !groupProvided {
			updatedUser.Group = current.Group
		}
		if !canManageTargetRole(myRole, current.Role) {
			return common.NewMessage("No permission to update users of same or higher permission level")
		}
		if isAccessTokenRequest(c) && (current.Role >= common.RoleAdminUser || updatedUser.AdminPermissions != nil) {
			return errAdtyAdminActionDenied
		}
		if groupProvided && current.Group != updatedUser.Group {
			if targetGroupDeleted {
				return model.ErrAdtyGroupNotFound
			}
			if err := adtyAuthorizeAdminActions(c, authz.AdtyUserGroup); err != nil {
				return err
			}
		}
		previousAuthVersion = current.AuthVersion
		auditParams["username"], auditParams["target_role"] = current.Username, current.Role
		auditEvents = append(auditEvents, prepareManageAudit(c, updatedUser.Id, "user.update", auditParams))
		if err := updatedUser.EditWithOptionsTx(tx, updatePassword, groupProvided); err != nil {
			return err
		}
		var before authz.PermissionsMap
		if updatedUser.AdminPermissions != nil && current.Role >= common.RoleAdminUser {
			var err error
			before, err = authz.CapabilitiesInTx(tx, updatedUser.Id, current.Role)
			if err != nil {
				return err
			}
		}
		touched, releasedAudits, err := updateAdminPermissionsForUserInTx(c, tx, updatedUser.Id, current.Role, updatedUser.AdminPermissions)
		authzTouched = touched
		if err != nil {
			return err
		}
		ticketAudits = append(ticketAudits, releasedAudits...)
		if before != nil {
			event, err := appendAdtyPermissionAuditTx(c, tx, updatedUser.Id, current.Role, before, updatedUser.AdminPermissions)
			if err != nil {
				return err
			}
			auditEvents = append(auditEvents, event)
		}
		return model.AppendAuditIntentTx(tx, auditEvents[0])
	}); err != nil {
		if errors.Is(err, errAdtyAdminActionDenied) {
			denyAdtyAdminAction(c)
			return
		}
		common.ApiError(c, err)
		return
	}
	deliverCommittedAudits(c, auditEvents)
	service.DeliverAdtyCommittedAudits(ticketAudits)
	var postCommitWarnings []string
	if authzTouched {
		if err := authz.ReloadPolicy(); err != nil {
			postCommitWarnings = append(postCommitWarnings, "policy_reload_failed")
		} else {
			authz.NotifyPolicyChanged()
		}
	}
	if updatedUser.AuthVersion > previousAuthVersion {
		if _, err := model.RevokeAllUserSessions(updatedUser.Id, "admin_user_update"); err != nil {
			postCommitWarnings = append(postCommitWarnings, "session_revoke_failed")
		}
	}
	if err := model.PublishUserAuthCache(updatedUser.Id); err != nil {
		postCommitWarnings = append(postCommitWarnings, "auth_cache_publish_failed")
	}
	if len(postCommitWarnings) > 0 {
		c.JSON(http.StatusOK, gin.H{
			"success":              true,
			"committed":            true,
			"recovery_required":    true,
			"post_commit_warnings": postCommitWarnings,
			"message":              "",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"committed": true,
		"message":   "",
	})
	return
}

// AdminResetUserPassword allows an administrator or root user to set a new
// manual password or generate a random alphanumeric password for a managed user.
// Existing sessions, PATs, and legacy access tokens are revoked immediately.
func AdminResetUserPassword(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid user ID")
		return
	}
	var body struct {
		Password string `json:"password"`
		Random   bool   `json:"random"`
		Length   int    `json:"length"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if body.Random {
		if body.Length != 0 && (body.Length < common.MinRandomAccountPasswordLength || body.Length > common.MaxRandomAccountPasswordLength) {
			common.ApiError(c, common.ErrRandomAccountPasswordLength)
			return
		}
	} else if err := common.ValidateNewAccountPassword(body.Password); err != nil {
		common.ApiError(c, err)
		return
	}
	originUser, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	myRole := c.GetInt("role")
	if !canManageTargetRole(myRole, originUser.Role) {
		common.ApiErrorT(c, "No permission to update users of same or higher permission level")
		return
	}
	if isAccessTokenRequest(c) && originUser.Role >= common.RoleAdminUser {
		denyAdtyAdminAction(c)
		return
	}
	authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserUpdate, service.AdminUserContext{UserID: originUser.Id})
	if authorization == nil {
		return
	}
	var newPassword, generatedPassword string
	if body.Random {
		newPassword, err = common.GenerateRandomAccountPassword(body.Length)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		generatedPassword = newPassword
	} else {
		newPassword = body.Password
	}
	hashedPassword, err := common.HashAccountPassword(newPassword)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var targetUsername string
	var auditEvent model.AuditLog
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.PreLockScopedEmployeeRowsTx(tx, id); err != nil {
			return err
		}
		var current model.User
		if err := model.AdtyLockForUpdate(tx).First(&current, id).Error; err != nil {
			return err
		}
		if !canManageTargetRole(myRole, current.Role) {
			return common.NewMessage("No permission to update users of same or higher permission level")
		}
		if isAccessTokenRequest(c) && current.Role >= common.RoleAdminUser {
			return errAdtyAdminActionDenied
		}
		nextAuthVersion, err := model.IncrementUserAuthVersionWithTx(tx, current.Id)
		if err != nil {
			return err
		}
		if err := model.SyncStaffAndTeamDisabledAuthVersionTx(tx, current.Id, current.AuthVersion, nextAuthVersion); err != nil {
			return err
		}
		if _, err := model.DeleteUserAccessTokensWithTx(tx, current.Id); err != nil {
			return err
		}
		if err := tx.Model(&model.User{}).Where("id = ?", current.Id).Updates(map[string]any{
			"password":                hashedPassword,
			"access_token":            nil,
			"access_token_created_at": nil,
		}).Error; err != nil {
			return err
		}
		targetUsername = current.Username
		auditParams := map[string]any{
			"id":                  current.Id,
			"username":            current.Username,
			"target_role":         current.Role,
			"random":              body.Random,
			"verification_method": authorization.Method,
		}
		auditEvent = prepareManageAudit(c, current.Id, "admin.user.password_reset", auditParams)
		return model.AppendAuditIntentTx(tx, auditEvent)
	}); err != nil {
		if errors.Is(err, errAdtyAdminActionDenied) {
			denyAdtyAdminAction(c)
			return
		}
		common.ApiError(c, err)
		return
	}
	deliverCommittedAudits(c, []model.AuditLog{auditEvent})
	if err := model.PublishUserAuthCache(id); err != nil {
		common.SysError(common.LogText("failed to publish auth cache after admin password reset: user=%d err=%v", id, err))
	}
	if _, err := model.RevokeAllUserSessions(id, "admin_user_password_reset"); err != nil {
		common.SysError(common.LogText("failed to revoke sessions after admin password reset: user=%d err=%v", id, err))
	}
	setAuthNoStore(c)
	common.ApiSuccess(c, gin.H{
		"user_id":            id,
		"username":           targetUsername,
		"generated_password": generatedPassword,
	})
}

func AdminClearUserBinding(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	bindingType := strings.ToLower(strings.TrimSpace(c.Param("binding_type")))
	if bindingType == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	myRole := c.GetInt("role")
	if !canManageTargetRole(myRole, user.Role) {
		common.ApiErrorT(c, "No permission to access users of same or higher level")
		return
	}
	if isAccessTokenRequest(c) && user.Role >= common.RoleAdminUser {
		denyAdtyAdminAction(c)
		return
	}
	authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserBindingClear, service.AdminUserBindingContext{UserID: user.Id, BindingType: bindingType})
	if authorization == nil {
		return
	}

	if err := user.ClearBindingForActor(bindingType, myRole, isAccessTokenRequest(c)); err != nil {
		if errors.Is(err, model.ErrAdminTargetSessionRequired) {
			denyAdtyAdminAction(c)
			return
		}
		if errors.Is(err, model.ErrUserQuotaPermission) {
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to access users of same or higher level"))
			return
		}
		common.ApiError(c, err)
		return
	}
	if bindingType == "telegram" {
		if err := service.RevokeAllAdtyTelegramBindings(user.Id); err != nil {
			common.ApiError(c, err)
			return
		}
	}

	recordManageAuditFor(c, user.Id, "user.binding_clear", map[string]any{
		"bindingType":         bindingType,
		"username":            user.Username,
		"verification_method": authorization.Method,
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "success",
	})
}

func UpdateSelf(c *gin.Context) {
	var requestData map[string]any
	if err := common.DecodeJson(c.Request.Body, &requestData); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	// R9-10: Reject uppercase or non-ASCII keys (e.g. "Password", "pa\u017f\u017fword")
	// so encoding/json case/Unicode folding cannot bypass password_change security audits.
	for k := range requestData {
		if !isCanonicalASCIILowerKey(k) {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}

	passwordRequested := false
	if value, exists := requestData["password"]; exists && value != nil {
		password, isString := value.(string)
		passwordRequested = !isString || password != ""
	}
	succeeded, notificationFailed := false, false
	reloginRequired, cacheSyncFailed, sessionRotationFailed := false, false, false
	auditRegistered := false
	if passwordRequested {
		auditRegistered = true
		defer func() {
			params := map[string]any{
				"success":             succeeded,
				"committed":           succeeded,
				"notification_failed": notificationFailed,
			}
			if succeeded {
				params["relogin_required"] = reloginRequired
				params["cache_sync_failed"] = cacheSyncFailed
				params["session_rotation_failed"] = sessionRotationFailed
			}
			recordUserSecurityAudit(c, c.GetInt("id"), "user.password_change", params)
		}()
	}
	// 检查是否是用户设置更新请求 (sidebar_modules 或 language)
	if sidebarModules, sidebarExists := requestData["sidebar_modules"]; sidebarExists && !passwordRequested {
		userId := c.GetInt("id")
		user, err := model.GetUserById(userId, false)
		if err != nil {
			common.ApiError(c, err)
			return
		}

		// 获取当前用户设置
		currentSetting := user.GetSetting()

		// 更新sidebar_modules字段（R8-19: 限制大小与 JSON 结构，防止热缓存与 DB 膨胀）
		if sidebarModulesStr, ok := sidebarModules.(string); ok {
			if len(sidebarModulesStr) > 16*1024 {
				common.ApiErrorT(c, "Invalid parameters")
				return
			}
			if trimmed := strings.TrimSpace(sidebarModulesStr); trimmed != "" {
				var parsed map[string]any
				if err := common.Unmarshal([]byte(trimmed), &parsed); err != nil {
					common.ApiErrorT(c, "Invalid parameters")
					return
				}
			}
			currentSetting.SidebarModules = sidebarModulesStr
		} else if sidebarModules != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}

		if err := model.UpdateUserSetting(user.Id, currentSetting); err != nil {
			common.ApiErrorT(c, "Update failed")
			return
		}

		common.ApiSuccessT(c, "Update successful", nil)
		return
	}

	// 检查是否是语言偏好更新请求
	if language, langExists := requestData["language"]; langExists && !passwordRequested {
		userId := c.GetInt("id")
		user, err := model.GetUserById(userId, false)
		if err != nil {
			common.ApiError(c, err)
			return
		}

		// 获取当前用户设置
		currentSetting := user.GetSetting()

		// 更新language字段。非正式语言被规范到官方语言，不会再存一份新的非正式取值。
		if langStr, ok := language.(string); ok {
			currentSetting.Language = i18n.OfficialPreferenceLanguage(langStr)
		}

		if err := model.UpdateUserSetting(user.Id, currentSetting); err != nil {
			common.ApiErrorT(c, "Update failed")
			return
		}

		common.ApiSuccessT(c, "Update successful", nil)
		return
	}

	// 原有的用户信息更新逻辑
	var user model.User
	requestDataBytes, err := common.Marshal(requestData)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err = common.Unmarshal(requestDataBytes, &user); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	user.Username = strings.TrimSpace(user.Username)
	if !model.ValidateUsernameNoControl(user.Username) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := common.Validate.StructExcept(&user, "Password"); err != nil {
		common.ApiErrorT(c, "Invalid input")
		return
	}

	cleanUser := model.User{
		Id:          c.GetInt("id"),
		Username:    user.Username,
		Password:    user.Password,
		DisplayName: user.DisplayName,
	}
	if user.Password != "" {
		if !auditRegistered {
			auditRegistered = true
			defer func() {
				params := map[string]any{
					"success":             succeeded,
					"committed":           succeeded,
					"notification_failed": notificationFailed,
				}
				if succeeded {
					params["relogin_required"] = reloginRequired
					params["cache_sync_failed"] = cacheSyncFailed
					params["session_rotation_failed"] = sessionRotationFailed
				}
				recordUserSecurityAudit(c, c.GetInt("id"), "user.password_change", params)
			}()
		}
		identity, ok := middleware.GetStepUpIdentity(c)
		if !ok {
			writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
			return
		}
		current, err := model.GetUserById(identity.UserID, true)
		if err != nil {
			writeSecurityOperationError(c, err)
			return
		}
		firstPassword := current.Password == ""
		scope := service.VerificationScopePasswordChange
		if firstPassword {
			scope = service.VerificationScopePasswordSet
		}
		if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: scope}) == nil {
			return
		}
		cleanUser.OriginalPassword = user.OriginalPassword
		if err := model.ChangeUserPassword(identity, &cleanUser, firstPassword); err != nil {
			writeSecurityOperationError(c, err)
			return
		}
		succeeded = true
		notificationFailed = service.NotifyAccountSecurityChange(service.AccountMailLanguage(c, current.Id), current.Email, current.Id, i18n.MsgSecurityEventPasswordUpdated) != nil
		cacheErr := model.PublishUserAuthCache(cleanUser.Id)
		bundle, sessionErr := service.AdvanceCurrentSessionToUserVersion(identity, "password_changed")
		if cacheErr != nil || sessionErr != nil {
			cacheSyncFailed = cacheErr != nil
			sessionRotationFailed = sessionErr != nil
			reloginRequired = true
			_, _ = model.RevokeAllUserSessions(cleanUser.Id, "password_changed")
			service.ClearRefreshCookie(c)
			setAuthNoStore(c)
			data := gin.H{
				"committed":               true,
				"has_password":            true,
				"notification_warning":    notificationFailed,
				"relogin_required":        true,
				"recovery_required":       true,
				"outcome":                 "committed_relogin_required",
				"cache_sync_failed":       cacheSyncFailed,
				"session_rotation_failed": sessionRotationFailed,
			}
			c.JSON(http.StatusOK, gin.H{
				"success":          true,
				"committed":        true,
				"relogin_required": true,
				"code":             "CREDENTIAL_COMMITTED_RELOGIN_REQUIRED",
				"message":          "Password updated. Sign in again with your new password.",
				"message_key":      "Password updated. Sign in again with your new password.",
				"data":             data,
			})
			return
		}
		data := authRotationData(bundle)
		data["committed"] = true
		data["has_password"] = true
		data["notification_warning"] = notificationFailed
		data["relogin_required"] = false
		c.JSON(http.StatusOK, gin.H{
			"success":   true,
			"committed": true,
			"message":   "",
			"data":      data,
		})
		return
	}
	if err := cleanUser.Update(false); err != nil {
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
	return
}

func writeUserDeleteBlockError(c *gin.Context, err error) bool {
	switch {
	case errors.Is(err, model.ErrCannotDeleteRootUser):
		common.ApiErrorT(c, "Cannot delete super administrator account")
	case errors.Is(err, model.ErrSiteOwnerCannotDelete):
		common.ApiErrorT(c, "Close the site before deleting this account")
	case errors.Is(err, model.ErrUserDeleteUnfinishedPayment):
		common.ApiErrorT(c, model.ErrUserDeleteUnfinishedPayment.Error())
	case errors.Is(err, model.ErrUserDeleteInFlightBilling):
		common.ApiErrorT(c, model.ErrUserDeleteInFlightBilling.Error())
	case errors.Is(err, model.ErrUserDeleteUnfinishedRefund):
		common.ApiErrorT(c, model.ErrUserDeleteUnfinishedRefund.Error())
	case errors.Is(err, model.ErrUserDeleteUnfinishedWithdraw):
		common.ApiErrorT(c, model.ErrUserDeleteUnfinishedWithdraw.Error())
	case errors.Is(err, model.ErrUserDeletePendingBalanceReq):
		common.ApiErrorT(c, model.ErrUserDeletePendingBalanceReq.Error())
	case errors.Is(err, model.ErrUserDeleteActiveStaff):
		common.ApiErrorT(c, model.ErrUserDeleteActiveStaff.Error())
	case errors.Is(err, model.ErrUserDeleteUnfinishedTeamFinance):
		common.ApiErrorT(c, model.ErrUserDeleteUnfinishedTeamFinance.Error())
	case errors.Is(err, model.ErrUserDeleteUnfinishedVoucher):
		common.ApiErrorT(c, model.ErrUserDeleteUnfinishedVoucher.Error())
	case errors.Is(err, model.ErrUserDeleteUnsettledSiteEarning):
		common.ApiErrorT(c, model.ErrUserDeleteUnsettledSiteEarning.Error())
	default:
		return false
	}
	return true
}

func DeleteUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid user ID")
		return
	}
	originUser, err := model.GetUserByIdIncludingDeleted(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	myRole := c.GetInt("role")
	if myRole <= originUser.Role {
		common.ApiErrorT(c, "No permission to update users of same or higher permission level")
		return
	}
	if isAccessTokenRequest(c) && originUser.Role >= common.RoleAdminUser {
		denyAdtyAdminAction(c)
		return
	}
	authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserDelete, service.AdminUserContext{UserID: originUser.Id})
	if authorization == nil {
		return
	}
	revokedAccessTokens, err := model.HardDeleteUserByIdForActorWithID(id, c.GetInt("id"), myRole, isAccessTokenRequest(c))
	if err != nil {
		if errors.Is(err, model.ErrAdminTargetSessionRequired) {
			denyAdtyAdminAction(c)
			return
		}
		if errors.Is(err, model.ErrUserQuotaPermission) {
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to update users of same or higher permission level"))
			return
		}
		if writeUserDeleteBlockError(c, err) {
			return
		}
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, originUser.Id, "user.delete", map[string]any{
		"username":              originUser.Username,
		"id":                    originUser.Id,
		"verification_method":   authorization.Method,
		"revoked_access_tokens": revokedAccessTokens,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func DeleteSelf(c *gin.Context) {
	setAuthNoStore(c)
	succeeded := false
	var revokedAccessTokens int64
	defer func() {
		params := map[string]any{"success": succeeded}
		if succeeded {
			params["revoked_access_tokens"] = revokedAccessTokens
		}
		recordUserSecurityAudit(c, c.GetInt("id"), "user.account_delete", params)
	}()
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
		return
	}
	account, err := model.GetUserById(identity.UserID, false)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeAccountDelete}) == nil {
		return
	}
	if _, _, ok := consumeAccountDeletionCode(c, account.Id, account.Email); !ok {
		return
	}
	if revokedAccessTokens, err = model.DeleteUserForSession(identity); err != nil {
		if writeUserDeleteBlockError(c, err) {
			return
		}
		writeSecurityOperationError(c, err)
		return
	}
	succeeded = true
	service.ClearRefreshCookie(c)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{},
	})
}

func CreateUser(c *gin.Context) {
	var user model.User
	err := common.DecodeJson(c.Request.Body, &user)
	user.Username = strings.TrimSpace(user.Username)
	if err != nil || user.Username == "" || !model.ValidateUsernameNoControl(user.Username) || user.Password == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := common.Validate.Struct(&user); err != nil {
		common.ApiErrorT(c, "Invalid input {{error}}", map[string]any{"error": err.Error()})
		return
	}
	if !common.IsValidateRole(user.Role) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if user.DisplayName == "" {
		user.DisplayName = user.Username
	}
	myRole := c.GetInt("role")
	if user.Role >= myRole {
		common.ApiErrorT(c, "Cannot create users with permission level equal to or higher than yourself")
		return
	}
	if isAccessTokenRequest(c) && (user.Role >= common.RoleAdminUser || user.AdminPermissions != nil) {
		denyAdtyAdminAction(c)
		return
	}
	auditParams := map[string]any{"role": user.Role}
	// Creating an administrator grants privilege just like promoting one.
	if user.Role >= common.RoleAdminUser {
		authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserCreate, service.AdminUserCreateContext{Role: user.Role})
		if authorization == nil {
			return
		}
		auditParams["verification_method"] = authorization.Method
	}
	group := strings.TrimSpace(user.Group)
	if group != "" {
		if group != model.AdtyDefaultGroupID {
			if err := adtyAuthorizeAdminActions(c, authz.AdtyUserGroup); err != nil {
				denyAdtyAdminAction(c)
				return
			}
		}
		if service.AdtyAdminPermissionsEnabled() {
			active, err := model.ActiveAdtyGroupIDs()
			if err != nil {
				common.ApiError(c, err)
				return
			}
			if _, ok := active[group]; !ok {
				common.ApiErrorT(c, "Group not found")
				return
			}
		}
	}
	// Even for admin users, we cannot fully trust them!
	cleanUser := model.User{
		Username:    user.Username,
		Password:    user.Password,
		DisplayName: user.DisplayName,
		Role:        user.Role, // 保持管理员设置的角色
		Group:       group,
	}
	authzTouched := false
	auditEvents := make([]model.AuditLog, 0, 2)
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if group != "" {
			if err := model.PreLockAdtyGroupsTx(tx, group); err != nil {
				return err
			}
		}
		if err := cleanUser.InsertWithTx(tx, 0); err != nil {
			return err
		}
		// Do not force the legacy column default "default" when caller omits/clears group.
		if group == "" {
			if err := tx.Model(&model.User{}).Where("id = ?", cleanUser.Id).Update("group", "").Error; err != nil {
				return err
			}
			cleanUser.Group = ""
		}
		touched, _, err := updateAdminPermissionsForUserInTx(c, tx, cleanUser.Id, cleanUser.Role, user.AdminPermissions)
		authzTouched = touched
		if err != nil {
			return err
		}
		if touched && cleanUser.Role >= common.RoleAdminUser && user.AdminPermissions != nil {
			event, err := appendAdtyPermissionAuditTx(c, tx, cleanUser.Id, cleanUser.Role, authz.PermissionsMap{}, user.AdminPermissions)
			if err != nil {
				return err
			}
			auditEvents = append(auditEvents, event)
			auditParams["admin_permissions_updated"] = true
		}
		auditParams["username"] = cleanUser.Username
		createEvent := prepareManageAudit(c, cleanUser.Id, "user.create", auditParams)
		if err := model.AppendAuditIntentTx(tx, createEvent); err != nil {
			return err
		}
		auditEvents = append(auditEvents, createEvent)
		return nil
	}); err != nil {
		if errors.Is(err, errAdtyAdminActionDenied) {
			denyAdtyAdminAction(c)
			return
		}
		if errors.Is(err, model.ErrUsernameAlreadyTaken) {
			common.ApiErrorT(c, "Username already exists or has been deleted")
			return
		}
		common.ApiError(c, err)
		return
	}
	deliverCommittedAudits(c, auditEvents)
	var postCommitWarnings []string
	if authzTouched {
		if err := authz.ReloadPolicy(); err != nil {
			postCommitWarnings = append(postCommitWarnings, "policy_reload_failed")
		} else {
			authz.NotifyPolicyChanged()
		}
	}
	cleanUser.FinishInsert(0)
	if len(postCommitWarnings) > 0 {
		c.JSON(http.StatusOK, gin.H{
			"success":              true,
			"committed":            true,
			"recovery_required":    true,
			"post_commit_warnings": postCommitWarnings,
			"message":              "",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func updateAdminPermissionsForUserInTx(c *gin.Context, tx *gorm.DB, userID int, userRole int, permissions map[string]map[string]bool) (bool, []model.AuditLog, error) {
	if isAccessTokenRequest(c) && (permissions != nil || userRole >= common.RoleAdminUser) {
		return false, nil, errAdtyAdminActionDenied
	}
	if permissions == nil {
		if userRole < common.RoleAdminUser && c.GetInt("role") == common.RoleRootUser {
			if err := authz.ClearUserAuthorizationInTx(tx, userID); err != nil {
				return true, nil, err
			}
			return true, nil, nil
		}
		return false, nil, nil
	}
	if c.GetInt("role") != common.RoleRootUser {
		return false, nil, common.NewMessage("Only super administrators can update admin permissions")
	}
	if userRole < common.RoleAdminUser {
		if err := authz.ClearUserAuthorizationInTx(tx, userID); err != nil {
			return true, nil, err
		}
		return true, nil, nil
	}
	var beforeCaps authz.PermissionsMap
	if userRole < common.RoleRootUser {
		var err error
		beforeCaps, err = authz.CapabilitiesInTx(tx, userID, userRole)
		if err != nil {
			return false, nil, err
		}
	}
	if err := authz.SetUserPermissionsInTx(tx, userID, permissions); err != nil {
		return true, nil, err
	}
	var ticketAudits []model.AuditLog
	if userRole < common.RoleRootUser {
		afterCaps, err := authz.CapabilitiesInTx(tx, userID, userRole)
		if err != nil {
			return true, nil, err
		}
		beforeMain := beforeCaps[authz.ResourceAdtyTicket]["manage"]
		beforeAll := beforeCaps[authz.ResourceAdtyTicket]["manage_all"]
		afterMain := afterCaps[authz.ResourceAdtyTicket]["manage"]
		afterAll := afterCaps[authz.ResourceAdtyTicket]["manage_all"]
		if (beforeMain || beforeAll) && !afterMain && !afterAll {
			_, audits, err := service.ReleaseAssigneeOpenTicketsWithAuditsTx(tx, userID, c.GetInt("id"), "ticket_permission_revoked")
			if err != nil {
				return true, nil, err
			}
			ticketAudits = append(ticketAudits, audits...)
		} else if beforeAll && !afterAll {
			_, audits, err := service.ReleaseAssigneeSubsiteOpenTicketsWithAuditsTx(tx, userID, c.GetInt("id"), "ticket_subsite_permission_revoked")
			if err != nil {
				return true, nil, err
			}
			ticketAudits = append(ticketAudits, audits...)
		}
	}
	return true, ticketAudits, nil
}

type ManageRequest struct {
	Id     int    `json:"id"`
	Action string `json:"action"`
	Value  int    `json:"value"`
	Mode   string `json:"mode"`
	// 二开：人工调额的原因（必填）和幂等键（任务 7.2）
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
	model.AdtyQuotaAdjustProvenance
}

// ManageUser Only admin user can do this
func ManageUser(c *gin.Context) {
	var req ManageRequest
	err := common.DecodeJson(c.Request.Body, &req)

	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	// 二开：按动作检查权限，要在 add_quota 提前返回之前（任务 2.6）
	if !adtyCheckManageAction(c, req) {
		return
	}
	if req.Action == "add_quota" {
		manageUserQuota(c, req)
		return
	}
	if req.Id <= 0 {
		common.ApiErrorT(c, "User does not exist")
		return
	}
	var user model.User
	if err := model.DB.Unscoped().First(&user, req.Id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorT(c, "User does not exist")
			return
		}
		common.ApiError(c, err)
		return
	}
	myRole := c.GetInt("role")
	if !canManageTargetRole(myRole, user.Role) {
		common.ApiErrorT(c, "No permission to update users of same or higher permission level")
		return
	}
	if isAccessTokenRequest(c) && user.Role >= common.RoleAdminUser {
		denyAdtyAdminAction(c)
		return
	}
	delta := model.User{Id: user.Id}
	switch req.Action {
	case "disable":
		delta.Status = common.UserStatusDisabled
		user.Status = common.UserStatusDisabled
		if user.Role == common.RoleRootUser {
			common.ApiErrorT(c, "Cannot disable super administrator user")
			return
		}
	case "enable":
		delta.Status = common.UserStatusEnabled
		user.Status = common.UserStatusEnabled
	case "delete":
		if user.Role == common.RoleRootUser {
			common.ApiErrorT(c, "Cannot delete super administrator account")
			return
		}
		authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserDelete, service.AdminUserContext{UserID: user.Id})
		if authorization == nil {
			return
		}
		revokedAccessTokens, err := user.DeleteForActorWithID(c.GetInt("id"), myRole, isAccessTokenRequest(c))
		if err != nil {
			if errors.Is(err, model.ErrAdminTargetSessionRequired) {
				denyAdtyAdminAction(c)
				return
			}
			if errors.Is(err, model.ErrUserQuotaPermission) {
				common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to update users of same or higher permission level"))
				return
			}
			if writeUserDeleteBlockError(c, err) {
				return
			}
			common.ApiError(c, err)
			return
		}
		// 删除用户后，强制清理 Redis 中所有该用户令牌的缓存，
		// 避免已缓存的令牌在 TTL 过期前仍能通过 TokenAuth 校验。
		if err := model.InvalidateUserTokensCache(user.Id); err != nil {
			common.SysLog(common.LogText("failed to invalidate tokens cache for user %d: %s", user.Id, err.Error()))
		}
		recordManageAuditFor(c, user.Id, "user.manage", map[string]any{
			"action":                req.Action,
			"username":              user.Username,
			"id":                    user.Id,
			"verification_method":   authorization.Method,
			"revoked_access_tokens": revokedAccessTokens,
		})
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
		})
		return
	case "promote":
		if myRole != common.RoleRootUser {
			common.ApiErrorT(c, "Regular administrators cannot promote other users to administrator")
			return
		}
		if user.Role >= common.RoleAdminUser {
			common.ApiErrorT(c, "This user is already an administrator")
			return
		}
		if user.SiteId > 0 || user.StaffSiteId > 0 {
			common.ApiErrorStatus(c, http.StatusForbidden, model.ErrCannotPromoteScopedUser)
			return
		}
		isSiteOwner, ownerErr := service.AdtyIsSiteOwnerStrict(user.Id)
		if ownerErr != nil {
			common.ApiError(c, ownerErr)
			return
		}
		if isSiteOwner {
			common.ApiErrorStatus(c, http.StatusForbidden, model.ErrCannotPromoteScopedUser)
			return
		}
		isTeamEmployee, empErr := service.AdtyIsIndependentTeamEmployee(user.Id)
		if empErr != nil {
			common.ApiError(c, empErr)
			return
		}
		if isTeamEmployee {
			common.ApiErrorStatus(c, http.StatusForbidden, model.ErrCannotPromoteScopedUser)
			return
		}
		delta.Role = common.RoleAdminUser
		user.Role = common.RoleAdminUser
	case "demote":
		if user.Role == common.RoleRootUser {
			common.ApiErrorT(c, "Cannot demote super administrator user")
			return
		}
		if user.Role == common.RoleCommonUser {
			common.ApiErrorT(c, "This user is already a common user")
			return
		}
		delta.Role = common.RoleCommonUser
		user.Role = common.RoleCommonUser
	default:
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserManage, service.AdminUserManageContext{UserID: user.Id, Action: req.Action})
	if authorization == nil {
		return
	}
	changed := false
	var ticketAudits []model.AuditLog
	var postCommitWarnings []string
	if req.Action == "demote" {
		if err := model.DB.Transaction(func(tx *gorm.DB) error {
			var txErr error
			changed, txErr = model.ApplyManagedUserAccessForActorTx(tx, &delta, myRole, isAccessTokenRequest(c))
			if txErr != nil {
				return txErr
			}
			if err := authz.ClearUserAuthorizationInTx(tx, user.Id); err != nil {
				return err
			}
			_, audits, err := service.ReleaseAssigneeOpenTicketsWithAuditsTx(tx, user.Id, c.GetInt("id"), "admin_demote")
			if err != nil {
				return err
			}
			ticketAudits = append(ticketAudits, audits...)
			return nil
		}); err != nil {
			if errors.Is(err, model.ErrAdminTargetSessionRequired) {
				denyAdtyAdminAction(c)
				return
			}
			if errors.Is(err, model.ErrUserQuotaPermission) {
				common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to update users of same or higher permission level"))
				return
			}
			common.ApiError(c, err)
			return
		}
		service.DeliverAdtyCommittedAudits(ticketAudits)
		if err := authz.ReloadPolicy(); err != nil {
			postCommitWarnings = append(postCommitWarnings, "policy_reload_failed")
		} else {
			authz.NotifyPolicyChanged()
		}
	} else if err := model.DB.Transaction(func(tx *gorm.DB) error {
		var staffUserIDs []int
		if req.Action == "disable" {
			var lockErr error
			staffUserIDs, lockErr = service.PreLockSiteOwnerStaffForDisableTx(tx, user.Id)
			if lockErr != nil {
				return lockErr
			}
		}
		var txErr error
		changed, txErr = model.ApplyManagedUserAccessForActorTx(tx, &delta, myRole, isAccessTokenRequest(c))
		if txErr != nil {
			return txErr
		}
		if req.Action == "disable" {
			var audits []model.AuditLog
			audits, txErr = service.ReleaseAssigneesOpenTicketsWithAuditsTx(tx, user.Id, staffUserIDs, c.GetInt("id"), "admin_disable", "site_owner_disabled")
			if txErr == nil {
				ticketAudits = append(ticketAudits, audits...)
			}
		}
		return txErr
	}); err != nil {
		if errors.Is(err, model.ErrAdminTargetSessionRequired) {
			denyAdtyAdminAction(c)
			return
		}
		if errors.Is(err, model.ErrUserQuotaPermission) {
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to update users of same or higher permission level"))
			return
		}
		common.ApiError(c, err)
		return
	} else {
		service.DeliverAdtyCommittedAudits(ticketAudits)
	}
	if delta.Status != 0 {
		user.Status = delta.Status
	}
	if changed {
		if err := model.PublishUserAuthCache(user.Id); err != nil {
			postCommitWarnings = append(postCommitWarnings, "auth_cache_publish_failed")
		}
		reason := "user_security_changed"
		if req.Action == "demote" {
			reason = "admin_demote"
		}
		if _, err := model.RevokeAllUserSessions(user.Id, reason); err != nil {
			postCommitWarnings = append(postCommitWarnings, "session_revoke_failed")
		}
	}
	// Role and status were written by themselves. Publish and revoke already
	// ran when that write changed access. Only PAT/relay token caches still
	// need an explicit invalidation; deleting the user hash here would discard
	// the freshly published auth-version floor.
	if err := model.InvalidateUserTokensCache(user.Id); err != nil {
		common.SysLog(common.LogText("failed to invalidate tokens cache for user %d: %s", user.Id, err.Error()))
	}
	recordManageAuditFor(c, user.Id, "user.manage", map[string]any{
		"action":              req.Action,
		"username":            user.Username,
		"id":                  user.Id,
		"verification_method": authorization.Method,
	})
	clearUser := model.User{
		Role:   user.Role,
		Status: user.Status,
	}
	resp := gin.H{
		"success":   true,
		"committed": true,
		"message":   "",
		"data":      clearUser,
	}
	if len(postCommitWarnings) > 0 {
		resp["recovery_required"] = true
		resp["post_commit_warnings"] = postCommitWarnings
	}
	c.JSON(http.StatusOK, resp)
	return
}

type UpdateUserSettingRequest struct {
	QuotaWarningType                 string  `json:"notify_type"`
	QuotaWarningThreshold            float64 `json:"quota_warning_threshold"`
	QuotaWarningEnabled              *bool   `json:"quota_warning_enabled,omitempty"`
	WebhookUrl                       string  `json:"webhook_url,omitempty"`
	WebhookSecret                    string  `json:"webhook_secret,omitempty"`
	ClearWebhookSecret               bool    `json:"clear_webhook_secret,omitempty"`
	NotificationEmail                string  `json:"notification_email,omitempty"`
	BarkUrl                          string  `json:"bark_url,omitempty"`
	ClearBarkUrl                     bool    `json:"clear_bark_url,omitempty"`
	GotifyUrl                        string  `json:"gotify_url,omitempty"`
	GotifyToken                      string  `json:"gotify_token,omitempty"`
	ClearGotifyToken                 bool    `json:"clear_gotify_token,omitempty"`
	GotifyPriority                   int     `json:"gotify_priority,omitempty"`
	UpstreamModelUpdateNotifyEnabled *bool   `json:"upstream_model_update_notify_enabled,omitempty"`
	ChannelNotifyEnabled             *bool   `json:"channel_notify_enabled,omitempty"`
	AcceptUnsetModelRatioModel       bool    `json:"accept_unset_model_ratio_model"`
	RecordIpLog                      bool    `json:"record_ip_log"`
}

func UpdateUserSetting(c *gin.Context) {
	var req UpdateUserSettingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	// R8-19: Bound hot-cached user setting string fields before validation/persistence.
	if len(req.WebhookUrl) > 2048 || len(req.BarkUrl) > 2048 || len(req.GotifyUrl) > 2048 ||
		len(req.WebhookSecret) > 512 || len(req.GotifyToken) > 512 || len(req.NotificationEmail) > 320 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	// 验证预警类型
	if req.QuotaWarningType != dto.NotifyTypeEmail && req.QuotaWarningType != dto.NotifyTypeWebhook && req.QuotaWarningType != dto.NotifyTypeBark && req.QuotaWarningType != dto.NotifyTypeGotify {
		common.ApiErrorT(c, "Invalid warning type")
		return
	}

	// 验证预警阈值，0 表示使用系统默认阈值
	if req.QuotaWarningThreshold < 0 {
		common.ApiErrorT(c, "Reminder threshold cannot be negative")
		return
	}

	// 如果是webhook类型,验证webhook地址
	if req.QuotaWarningType == dto.NotifyTypeWebhook {
		if req.WebhookUrl == "" {
			common.ApiErrorT(c, "Webhook URL cannot be empty")
			return
		}
		// 验证URL格式
		parsedWebhook, err := url.ParseRequestURI(req.WebhookUrl)
		if err != nil || (parsedWebhook.Scheme != "http" && parsedWebhook.Scheme != "https") || strings.TrimSpace(parsedWebhook.Host) == "" {
			common.ApiErrorT(c, "Invalid Webhook URL")
			return
		}
	}

	// 如果是邮件类型，验证邮箱地址（R4-049: 严格校验单邮箱，禁止多收件人列表）
	if req.QuotaWarningType == dto.NotifyTypeEmail && strings.TrimSpace(req.NotificationEmail) != "" {
		validatedEmail, err := service.ValidateSingleNotificationEmail(req.NotificationEmail)
		if err != nil {
			common.ApiErrorT(c, "Invalid email address")
			return
		}
		req.NotificationEmail = validatedEmail
	}

	// 如果是Bark类型，验证Bark URL格式（留空时允许沿用已保存密文）
	if req.QuotaWarningType == dto.NotifyTypeBark && strings.TrimSpace(req.BarkUrl) != "" {
		// 验证URL格式
		parsedBark, err := url.ParseRequestURI(strings.TrimSpace(req.BarkUrl))
		if err != nil {
			common.ApiErrorT(c, "Invalid Bark push URL")
			return
		}
		// 检查是否是HTTP或HTTPS且包含有效主机
		if (parsedBark.Scheme != "http" && parsedBark.Scheme != "https") || strings.TrimSpace(parsedBark.Host) == "" {
			common.ApiErrorT(c, "URL must start with http:// or https://")
			return
		}
	}

	// 如果是Gotify类型，验证Gotify URL格式（Token 留空时允许沿用已保存密文）
	if req.QuotaWarningType == dto.NotifyTypeGotify {
		if req.GotifyUrl == "" {
			common.ApiErrorT(c, "Gotify server URL cannot be empty")
			return
		}
		// 验证URL格式
		parsedGotify, err := url.ParseRequestURI(req.GotifyUrl)
		if err != nil {
			common.ApiErrorT(c, "Invalid Gotify server URL")
			return
		}
		// 检查是否是HTTP或HTTPS且包含有效主机
		if (parsedGotify.Scheme != "http" && parsedGotify.Scheme != "https") || strings.TrimSpace(parsedGotify.Host) == "" {
			common.ApiErrorT(c, "URL must start with http:// or https://")
			return
		}
	}

	userId := c.GetInt("id")
	user, err := model.GetUserById(userId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 从已保存的设置出发，语言、侧边栏、扣费策略等不由此表单管理的字段保持不变；
	// 请求未携带的开关也保持原值
	settings := user.GetSetting()
	prevWebhookSecret := settings.WebhookSecret
	prevBarkUrl := settings.BarkUrl
	prevGotifyToken := settings.GotifyToken
	settings.NotifyType = req.QuotaWarningType
	settings.QuotaWarningThreshold = req.QuotaWarningThreshold
	settings.AcceptUnsetRatioModel = req.AcceptUnsetModelRatioModel
	settings.RecordIpLog = req.RecordIpLog
	if req.QuotaWarningEnabled != nil {
		settings.QuotaWarningEnabled = req.QuotaWarningEnabled
	}
	if user.Role >= common.RoleAdminUser {
		if req.UpstreamModelUpdateNotifyEnabled != nil {
			settings.UpstreamModelUpdateNotifyEnabled = *req.UpstreamModelUpdateNotifyEnabled
		}
		if req.ChannelNotifyEnabled != nil {
			settings.ChannelNotifyEnabled = req.ChannelNotifyEnabled
		}
	}

	// 只保留所选通知方式的配置，其他方式的配置清空
	settings.WebhookUrl, settings.WebhookSecret = "", ""
	settings.NotificationEmail = ""
	settings.BarkUrl = ""
	settings.GotifyUrl, settings.GotifyToken, settings.GotifyPriority = "", "", 0
	switch req.QuotaWarningType {
	case dto.NotifyTypeWebhook:
		settings.WebhookUrl = req.WebhookUrl
		trimmedSecret := strings.TrimSpace(req.WebhookSecret)
		if req.ClearWebhookSecret {
			settings.WebhookSecret = ""
		} else if trimmedSecret != "" {
			sealed, sealErr := model.SealAdtyPurposeSecret(fmt.Sprintf("user_notify:%d:webhook_secret", user.Id), trimmedSecret)
			if sealErr != nil {
				common.ApiError(c, sealErr)
				return
			}
			settings.WebhookSecret = sealed
		} else {
			settings.WebhookSecret = prevWebhookSecret
		}
	case dto.NotifyTypeEmail:
		settings.NotificationEmail = req.NotificationEmail
	case dto.NotifyTypeBark:
		trimmedBark := strings.TrimSpace(req.BarkUrl)
		if req.ClearBarkUrl {
			common.ApiErrorT(c, "Bark push URL cannot be empty")
			return
		}
		if trimmedBark != "" {
			sealed, sealErr := model.SealAdtyPurposeSecret(fmt.Sprintf("user_notify:%d:bark_url", user.Id), trimmedBark)
			if sealErr != nil {
				common.ApiError(c, sealErr)
				return
			}
			settings.BarkUrl = sealed
		} else if strings.TrimSpace(prevBarkUrl) != "" {
			settings.BarkUrl = prevBarkUrl
		} else {
			common.ApiErrorT(c, "Bark push URL cannot be empty")
			return
		}
	case dto.NotifyTypeGotify:
		settings.GotifyUrl = req.GotifyUrl
		trimmedToken := strings.TrimSpace(req.GotifyToken)
		if req.ClearGotifyToken {
			common.ApiErrorT(c, "Gotify token cannot be empty")
			return
		}
		if trimmedToken != "" {
			sealed, sealErr := model.SealAdtyPurposeSecret(fmt.Sprintf("user_notify:%d:gotify_token", user.Id), trimmedToken)
			if sealErr != nil {
				common.ApiError(c, sealErr)
				return
			}
			settings.GotifyToken = sealed
		} else if strings.TrimSpace(prevGotifyToken) != "" {
			settings.GotifyToken = prevGotifyToken
		} else {
			common.ApiErrorT(c, "Gotify token cannot be empty")
			return
		}
		// Gotify优先级范围0-10，超出范围则使用默认值5
		if req.GotifyPriority < 0 || req.GotifyPriority > 10 {
			settings.GotifyPriority = 5
		} else {
			settings.GotifyPriority = req.GotifyPriority
		}
	}

	// 更新用户设置
	if err := model.UpdateUserSetting(user.Id, settings); err != nil {
		var msg *common.Message
		if errors.As(err, &msg) {
			common.ApiError(c, msg)
			return
		}
		common.ApiErrorT(c, "Update failed")
		return
	}

	common.ApiSuccessT(c, "Settings updated", nil)
}
