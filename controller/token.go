package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type tokenAutoGroupsInput struct {
	Set    bool
	Groups []string
}

func (input *tokenAutoGroupsInput) UnmarshalJSON(data []byte) error {
	input.Set = true
	if strings.TrimSpace(string(data)) == "null" {
		input.Groups = nil
		return nil
	}
	return common.Unmarshal(data, &input.Groups)
}

type tokenRequest struct {
	model.Token
	AutoGroups tokenAutoGroupsInput `json:"auto_groups"`
	// 指针区分“未传”和显式 false：新建时未传即开启，更新时未传则保留原值。
	AllowUserQuotaQuery  *bool `json:"allow_user_quota_query"`
	RateLimitPerMinute   *int  `json:"rate_limit_per_minute"`
	RateLimitTPM         *int  `json:"rate_limit_tpm"`
	RateLimitConcurrency *int  `json:"rate_limit_concurrency"`
	remainQuotaSet       bool  `json:"-"`
}

func unmarshalFirstOptionalInt(fields map[string]json.RawMessage, keys ...string) (*int, error) {
	for _, key := range keys {
		raw, ok := fields[key]
		if !ok || strings.TrimSpace(string(raw)) == "null" {
			continue
		}
		var value int
		if err := common.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return &value, nil
	}
	return nil, nil
}

func (r *tokenRequest) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := common.Unmarshal(data, &fields); err != nil {
		return err
	}
	type tokenRequestBody tokenRequest
	var body tokenRequestBody
	if err := common.Unmarshal(data, &body); err != nil {
		return err
	}
	*r = tokenRequest(body)
	if r.RateLimitPerMinute == nil {
		val, err := unmarshalFirstOptionalInt(fields, "rpm_limit", "rpm", "per_minute")
		if err != nil {
			return err
		}
		r.RateLimitPerMinute = val
	}
	if r.RateLimitTPM == nil {
		val, err := unmarshalFirstOptionalInt(fields, "tpm_limit", "tokens_per_minute", "tpm")
		if err != nil {
			return err
		}
		r.RateLimitTPM = val
	}
	if r.RateLimitConcurrency == nil {
		val, err := unmarshalFirstOptionalInt(fields, "concurrency_limit", "max_concurrency", "concurrency")
		if err != nil {
			return err
		}
		r.RateLimitConcurrency = val
	}
	raw, ok := fields["remain_quota"]
	if !ok || string(raw) == "null" {
		return nil
	}
	var remain int
	if err := common.Unmarshal(raw, &remain); err != nil {
		return err
	}
	r.RemainQuota = remain
	r.remainQuotaSet = true
	return nil
}

func (r tokenRequest) keyLimitPatch() model.AdtyKeyLimitPatch {
	return model.AdtyKeyLimitPatch{
		PerMinute:   r.RateLimitPerMinute,
		TPM:         r.RateLimitTPM,
		Concurrency: r.RateLimitConcurrency,
	}
}

type tokenResponse struct {
	*model.Token
	AutoGroups           []string `json:"auto_groups"`
	RateLimitPerMinute   int      `json:"rate_limit_per_minute"`
	RateLimitTPM         int      `json:"rate_limit_tpm"`
	RateLimitConcurrency int      `json:"rate_limit_concurrency"`
	TPMLimit             int      `json:"tpm_limit"`
	ConcurrencyLimit     int      `json:"concurrency_limit"`
}

func maxTokenQuota() int {
	quota, err := common.WalletQuotaFromDecimalStrict(
		decimal.NewFromInt(1_000_000_000).Mul(decimal.NewFromFloat(common.QuotaPerUnit)),
	)
	if err != nil {
		return common.MaxWalletQuota
	}
	return quota
}

func buildMaskedTokenResponse(token *model.Token) *tokenResponse {
	if token == nil {
		return nil
	}
	maskedToken := *token
	maskedToken.Key = token.GetMaskedKey()
	maskedToken.Status = token.EffectiveStatus(common.GetTimestamp())
	autoGroups, err := token.GetAutoGroups()
	if err != nil {
		common.SysError(common.LogText("failed to parse auto groups for token %d: %v", token.Id, err))
		autoGroups = nil
	}
	if len(autoGroups) == 0 {
		autoGroups = nil
	}
	response := &tokenResponse{Token: &maskedToken, AutoGroups: autoGroups}
	if limits, err := model.GetAdtyKeyLimits(token.Id); err == nil {
		response.RateLimitPerMinute = limits.PerMinute
		response.RateLimitTPM = limits.TPM
		response.RateLimitConcurrency = limits.Concurrency
		response.TPMLimit = limits.TPM
		response.ConcurrencyLimit = limits.Concurrency
	}
	return response
}

func saveTokenRateLimit(c *gin.Context, tokenID int, perMinute *int) bool {
	if perMinute == nil {
		return true
	}
	var err error
	if *perMinute <= 0 {
		err = model.ClearAdtyKeyLimit(tokenID)
	} else {
		err = model.SaveAdtyKeyLimit(tokenID, *perMinute)
	}
	if err != nil {
		common.ApiError(c, err)
		return false
	}
	return true
}

func buildMaskedTokenResponses(tokens []*model.Token) []*tokenResponse {
	maskedTokens := make([]*tokenResponse, 0, len(tokens))
	for _, token := range tokens {
		maskedTokens = append(maskedTokens, buildMaskedTokenResponse(token))
	}
	return maskedTokens
}

func getTokenRequestUserGroup(c *gin.Context) (string, error) {
	if userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup); userGroup != "" {
		return userGroup, nil
	}
	if userGroup := c.GetString("group"); userGroup != "" {
		return userGroup, nil
	}
	return model.GetUserGroup(c.GetInt("id"), false)
}

func setTokenAutoGroups(c *gin.Context, token *model.Token, groups []string) bool {
	if len(groups) == 0 {
		if err := token.SetAutoGroups(nil); err != nil {
			common.ApiError(c, err)
			return false
		}
		return true
	}

	maxCount := setting.GetMaxTokenAutoGroups()
	if len(groups) > maxCount {
		common.ApiErrorT(c, "A token can select at most {{max}} Auto groups", map[string]any{"max": maxCount})
		return false
	}

	userGroup, err := getTokenRequestUserGroup(c)
	if err != nil {
		common.ApiError(c, err)
		return false
	}
	seen := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		if _, ok := seen[group]; ok {
			common.ApiErrorT(c, "Auto group {{group}} is duplicated", map[string]any{"group": group})
			return false
		}
		seen[group] = struct{}{}
		if !service.IsUserSelectableGroup(userGroup, group) {
			common.ApiErrorT(c, "Auto group {{group}} is unavailable or unauthorized", map[string]any{"group": group})
			return false
		}
	}

	if err := token.SetAutoGroups(groups); err != nil {
		common.ApiError(c, err)
		return false
	}
	return true
}

func GetAllTokens(c *gin.Context) {
	userId := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)
	tokens, err := model.GetAllUserTokens(userId, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	total, _ := model.CountUserTokens(userId)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(buildMaskedTokenResponses(tokens))
	common.ApiSuccess(c, pageInfo)
}

func SearchTokens(c *gin.Context) {
	userId := c.GetInt("id")
	keyword := c.Query("keyword")
	token := c.Query("token")

	pageInfo := common.GetPageQuery(c)

	tokens, total, err := model.SearchUserTokens(userId, keyword, token, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(buildMaskedTokenResponses(tokens))
	common.ApiSuccess(c, pageInfo)
}

func GetToken(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token, err := model.GetTokenByIds(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, buildMaskedTokenResponse(token))
}

func GetTokenAutoGroups(c *gin.Context) {
	userGroup, err := getTokenRequestUserGroup(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"groups":    service.GetUserAutoGroup(userGroup),
		"max_count": setting.GetMaxTokenAutoGroups(),
	})
}

func GetTokenKey(c *gin.Context) {
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeAPIKeyRead}) == nil {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token, err := model.GetTokenByIds(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params := tokenAuditParams(c)
	params["id"], params["name"] = token.Id, token.Name
	if !persistTokenReadAudit(c, "token.key_view", params) {
		return
	}
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	common.ApiSuccess(c, gin.H{
		"key": token.GetFullKey(),
	})
}

func GetTokenStatus(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	userId := c.GetInt("id")
	token, err := model.GetTokenByIds(tokenId, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}
	c.JSON(http.StatusOK, gin.H{
		"object":          "credit_summary",
		"total_granted":   token.RemainQuota,
		"total_used":      0, // not supported currently
		"total_available": token.RemainQuota,
		"expires_at":      expiredAt * 1000,
	})
}

func GetTokenUsage(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "No Authorization header",
		})
		return
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid Bearer token",
		})
		return
	}
	tokenKey := strings.Split(strings.TrimPrefix(parts[1], "sk-"), "-")[0]

	token, err := model.GetTokenByKey(tokenKey, false)
	if err != nil {
		common.SysError(common.LogText("failed to get token by key: %s", err.Error()))
		common.ApiErrorI18n(c, i18n.MsgTokenGetInfoFailed)
		return
	}

	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}

	data := gin.H{
		"object":               "token_usage",
		"name":                 token.Name,
		"total_granted":        token.RemainQuota + token.UsedQuota,
		"total_used":           token.UsedQuota,
		"total_available":      token.RemainQuota,
		"unlimited_quota":      token.UnlimitedQuota,
		"model_limits":         token.GetModelLimitsMap(),
		"model_limits_enabled": token.ModelLimitsEnabled,
		"expires_at":           expiredAt,
	}
	// 用户额度只在令牌允许时返回；IP 白名单已由 TokenAuthReadOnly 检查。
	if token.AllowUserQuotaQuery {
		userQuota, err := model.GetUserQuota(token.UserId, false)
		var userUsedQuota int
		if err == nil {
			userUsedQuota, err = model.GetUserUsedQuota(token.UserId)
		}
		if err != nil {
			common.SysError(common.LogText("failed to get user quota: %s", err.Error()))
			common.ApiErrorI18n(c, i18n.MsgTokenGetInfoFailed)
			return
		}
		data["user_quota"] = gin.H{
			"total_available": userQuota,
			"total_used":      userUsedQuota,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    true,
		"message": "ok",
		"data":    data,
	})
}

func AddToken(c *gin.Context) {
	addPersonalToken(c, 0)
}

func AddOwnerSelfUseToken(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	if site.Status != model.AdtySiteStatusActive || service.AdtyRequestSiteID(c) != site.Id {
		common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtySiteNotOwner)
		return
	}
	addPersonalToken(c, site.Id)
}

func addPersonalToken(c *gin.Context, ownerSite int) {
	request := tokenRequest{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token := request.Token
	if len(token.Name) > 50 {
		common.ApiErrorT(c, "Token name is too long")
		return
	}
	params := tokenAuditParams(c)
	params["name"] = token.Name
	// 非无限额度时，检查额度值是否超出有效范围
	if !token.UnlimitedQuota {
		if token.RemainQuota < 0 {
			common.ApiErrorT(c, "Quota value cannot be negative")
			return
		}
		maxQuotaValue := maxTokenQuota()
		if token.RemainQuota > maxQuotaValue {
			common.ApiErrorT(c, "Quota value exceeds valid range, maximum is {{max}}", map[string]any{"max": logger.FormatQuota(maxQuotaValue)})
			return
		}
	}
	// 检查用户令牌数量是否已达上限
	maxTokens := operation_setting.GetMaxUserTokens()
	if token.Group == "auto" {
		if !setTokenAutoGroups(c, &token, request.AutoGroups.Groups) {
			return
		}
	} else {
		token.CrossGroupRetry = false
		_ = token.SetAutoGroups(nil)
	}
	key, err := common.GenerateKey()
	if err != nil {
		common.ApiErrorT(c, "Failed to generate token")
		common.SysLog(common.LogText("failed to generate token key: %s", err.Error()))
		return
	}
	cleanToken := model.Token{
		OwnerSelfUseSiteId: ownerSite,
		UserId:             c.GetInt("id"),
		Name:               token.Name,
		Key:                key,
		CreatedTime:        common.GetTimestamp(),
		AccessedTime:       common.GetTimestamp(),
		ExpiredTime:        token.ExpiredTime,
		RemainQuota:        token.RemainQuota,
		UnlimitedQuota:     token.UnlimitedQuota,
		ModelLimitsEnabled: token.ModelLimitsEnabled,
		ModelLimits:        token.ModelLimits,
		AllowIps:           token.AllowIps,
		Group:              token.Group,
		CrossGroupRetry:    token.CrossGroupRetry,
		AutoGroups:         token.AutoGroups,
	}
	cleanToken.AllowUserQuotaQuery = request.AllowUserQuotaQuery == nil || *request.AllowUserQuotaQuery
	if ownerSite > 0 {
		cleanToken.AllowUserQuotaQuery = false
		params["site_id"] = ownerSite
	}
	event := prepareTokenMutationAudit(c, "token.create", params)
	err = cleanToken.InsertWithLimitAndKeyLimit(maxTokens, request.keyLimitPatch(), event)
	if err != nil {
		if errors.Is(err, model.ErrTokenLimit) {
			common.ApiErrorT(c, "Maximum number of tokens reached ({{max}})", map[string]any{"max": maxTokens})
			return
		}
		common.ApiError(c, err)
		return
	}
	params["id"] = cleanToken.Id
	deliverCommittedAudits(c, []model.AuditLog{event})
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	if ownerSite > 0 {
		c.Header("Cache-Control", "no-store")
		common.ApiSuccess(c, gin.H{"key": "sk-" + key, "id": cleanToken.Id})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func DeleteToken(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	token, err := model.GetTokenByIds(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params := tokenAuditParams(c)
	params["id"], params["name"] = token.Id, token.Name
	event := prepareTokenMutationAudit(c, "token.delete", params)
	err = token.Delete(event)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	deliverCommittedAudits(c, []model.AuditLog{event})
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func UpdateToken(c *gin.Context) {
	userId := c.GetInt("id")
	statusOnly := c.Query("status_only")
	request := tokenRequest{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token := request.Token
	params := tokenAuditParams(c)
	if token.Id > 0 {
		params["id"] = token.Id
	}
	if len(token.Name) > 50 {
		common.ApiErrorT(c, "Token name is too long")
		return
	}
	if !token.UnlimitedQuota {
		if token.RemainQuota < 0 {
			common.ApiErrorT(c, "Quota value cannot be negative")
			return
		}
		maxQuotaValue := maxTokenQuota()
		if token.RemainQuota > maxQuotaValue {
			common.ApiErrorT(c, "Quota value exceeds valid range, maximum is {{max}}", map[string]any{"max": logger.FormatQuota(maxQuotaValue)})
			return
		}
	}
	cleanToken, err := model.GetTokenByIds(token.Id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params["name"] = cleanToken.Name
	previous := *cleanToken
	if token.Status == common.TokenStatusEnabled {
		if cleanToken.Status == common.TokenStatusExpired && cleanToken.ExpiredTime <= common.GetTimestamp() && cleanToken.ExpiredTime != -1 {
			common.ApiErrorT(c, "Token has expired and cannot be enabled. Please modify the expiration time or set it to never expire")
			return
		}
		if cleanToken.Status == common.TokenStatusExhausted && cleanToken.RemainQuota <= 0 && !cleanToken.UnlimitedQuota {
			common.ApiErrorT(c, "Token quota is exhausted and cannot be enabled. Please modify the remaining quota or set it to unlimited")
			return
		}
	}
	replaceRemainQuota := false
	keyLimitPatch := model.AdtyKeyLimitPatch{}
	if statusOnly != "" {
		cleanToken.Status = token.Status
	} else {
		// If you add more fields, please also update token.Update()
		cleanToken.Name = token.Name
		cleanToken.ExpiredTime = token.ExpiredTime
		// 没提交 remain_quota，或和库里刚读到的一样，就不写回。表单打开后扣掉的额度不能被旧快照加回去。
		if request.remainQuotaSet && token.RemainQuota != previous.RemainQuota {
			cleanToken.RemainQuota = token.RemainQuota
			replaceRemainQuota = true
		}
		cleanToken.UnlimitedQuota = token.UnlimitedQuota
		cleanToken.ModelLimitsEnabled = token.ModelLimitsEnabled
		cleanToken.ModelLimits = token.ModelLimits
		cleanToken.AllowIps = token.AllowIps
		if request.AllowUserQuotaQuery != nil {
			cleanToken.AllowUserQuotaQuery = *request.AllowUserQuotaQuery
		}
		cleanToken.Group = token.Group
		cleanToken.CrossGroupRetry = token.CrossGroupRetry
		if token.Group != "auto" {
			cleanToken.CrossGroupRetry = false
			_ = cleanToken.SetAutoGroups(nil)
		} else if request.AutoGroups.Set {
			if !setTokenAutoGroups(c, cleanToken, request.AutoGroups.Groups) {
				return
			}
		}
		keyLimitPatch = request.keyLimitPatch()
	}
	action := "token.update"
	if statusOnly != "" {
		action = "token.status_update"
	}
	event := prepareTokenMutationAudit(c, action, params)
	previousLimit, updatedLimit, err := cleanToken.UpdateWithKeyLimit(replaceRemainQuota, keyLimitPatch, event)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params["name"] = cleanToken.Name
	deliverCommittedAudits(c, []model.AuditLog{event})
	if statusOnly != "" {
		params["from"], params["to"] = previous.Status, cleanToken.Status
	} else {
		changedFields := []string{}
		rateChanged := request.RateLimitPerMinute != nil && updatedLimit.PerMinute != previousLimit.PerMinute
		tpmChanged := request.RateLimitTPM != nil && updatedLimit.TPM != previousLimit.TPM
		concurrencyChanged := request.RateLimitConcurrency != nil && updatedLimit.Concurrency != previousLimit.Concurrency
		for _, field := range []struct {
			name    string
			changed bool
		}{
			{"name", previous.Name != cleanToken.Name},
			{"expired_time", previous.ExpiredTime != cleanToken.ExpiredTime},
			{"remain_quota", replaceRemainQuota},
			{"unlimited_quota", previous.UnlimitedQuota != cleanToken.UnlimitedQuota},
			{"model_limits_enabled", previous.ModelLimitsEnabled != cleanToken.ModelLimitsEnabled},
			{"model_limits", previous.ModelLimits != cleanToken.ModelLimits},
			{"allow_ips", (previous.AllowIps == nil) != (cleanToken.AllowIps == nil) ||
				(previous.AllowIps != nil && cleanToken.AllowIps != nil && *previous.AllowIps != *cleanToken.AllowIps)},
			{"allow_user_quota_query", previous.AllowUserQuotaQuery != cleanToken.AllowUserQuotaQuery},
			{"group", previous.Group != cleanToken.Group},
			{"cross_group_retry", previous.CrossGroupRetry != cleanToken.CrossGroupRetry},
			{"auto_groups", previous.AutoGroups != cleanToken.AutoGroups},
			{"rate_limit_per_minute", rateChanged},
			{"rate_limit_tpm", tpmChanged},
			{"rate_limit_concurrency", concurrencyChanged},
		} {
			if field.changed {
				changedFields = append(changedFields, field.name)
			}
		}
		params["changed_fields"] = changedFields
	}
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	// Read a fresh logical snapshot for the response without attributing any
	// concurrent consumption to the metadata edit in changed_fields above.
	if latest, readErr := model.GetTokenByIds(cleanToken.Id, userId); readErr == nil {
		cleanToken = latest
	} else {
		common.SysError(common.LogText("failed to read committed token projection: token=%d error=%s", cleanToken.Id, readErr.Error()))
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    buildMaskedTokenResponse(cleanToken),
	})
}

const maxTokenBatchSize = 100

type TokenBatch struct {
	Ids []int `json:"ids"`
}

func DeleteTokenBatch(c *gin.Context) {
	tokenBatch := TokenBatch{}
	if err := c.ShouldBindJSON(&tokenBatch); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	params := tokenBatchAuditParams(c, tokenBatch.Ids)
	if len(tokenBatch.Ids) == 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if len(tokenBatch.Ids) > maxTokenBatchSize {
		common.ApiErrorT(c, "Too many items in batch request, maximum is {{max}}", map[string]any{"max": maxTokenBatchSize})
		return
	}
	userId := c.GetInt("id")
	event := prepareTokenMutationAudit(c, "token.delete_batch", params)
	count, err := model.BatchDeleteTokens(tokenBatch.Ids, userId, event)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params["count"] = count
	deliverCommittedAudits(c, []model.AuditLog{event})
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    count,
	})
}

func GetTokenKeysBatch(c *gin.Context) {
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeAPIKeyRead}) == nil {
		return
	}
	tokenBatch := TokenBatch{}
	if err := c.ShouldBindJSON(&tokenBatch); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	params := tokenBatchAuditParams(c, tokenBatch.Ids)
	if len(tokenBatch.Ids) == 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if len(tokenBatch.Ids) > maxTokenBatchSize {
		common.ApiErrorT(c, "Too many items in batch request, maximum is {{max}}", map[string]any{"max": maxTokenBatchSize})
		return
	}
	userId := c.GetInt("id")
	tokens, err := model.GetTokenKeysByIds(tokenBatch.Ids, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	keysMap := make(map[int]string)
	returnedIDs := make([]int, 0, len(tokens))
	for _, t := range tokens {
		keysMap[t.Id] = t.GetFullKey()
		returnedIDs = append(returnedIDs, t.Id)
	}
	params["count"] = len(tokens)
	params["returned_ids"] = returnedIDs
	if !persistTokenReadAudit(c, "token.key_view_batch", params) {
		return
	}
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	common.ApiSuccess(c, gin.H{"keys": keysMap})
}

// Credential access requires durable evidence before the response can expose it.
func persistTokenReadAudit(c *gin.Context, action string, params model.AuditFields) bool {
	event := prepareManageAudit(c, c.GetInt("id"), action, map[string]any(params))
	event.Category = model.AuditCategorySecurity
	event.Other.AdminInfo = nil
	if err := model.AppendAuditIntentTx(model.DB, event); err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Service temporarily unavailable"))
		return false
	}
	deliverCommittedAudits(c, []model.AuditLog{event})
	return true
}

func prepareTokenMutationAudit(c *gin.Context, action string, params model.AuditFields) model.AuditLog {
	event := prepareManageAudit(c, c.GetInt("id"), action, map[string]any(params))
	event.Category = model.AuditCategorySecurity
	event.Other.AdminInfo = nil
	return event
}
