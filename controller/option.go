package controller

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var optionUpdateCrossKeyMu sync.Mutex

func init() {
	adty_setting.SetAuthoritativeEpayTimeoutReader(readAuthoritativeEpayTimeouts)
}

func readAuthoritativeEpayTimeouts() (map[string]string, error) {
	if model.DB == nil || !model.DB.Migrator().HasTable(&model.Option{}) {
		return nil, common.NewMessage("Operation failed")
	}
	var rows []model.Option
	if err := model.DB.Where("key IN ?", []string{
		adty_setting.EpaySettingKeyChannelTimeout,
		adty_setting.EpaySettingKeyTotalTimeout,
	}).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}

func isSensitiveOptionKey(k string) bool {
	trimmed := strings.TrimSpace(k)
	lower := strings.ToLower(trimmed)
	if strings.HasSuffix(lower, "aliyun_access_key_id") {
		return true
	}
	return lower == "key" || lower == "authorization" || strings.HasSuffix(trimmed, "Token") ||
		strings.HasSuffix(trimmed, "Secret") ||
		strings.HasSuffix(trimmed, "Key") ||
		strings.HasSuffix(trimmed, "Password") ||
		strings.HasSuffix(lower, "token") ||
		strings.HasSuffix(lower, "secret") ||
		strings.HasSuffix(lower, "api_key") ||
		strings.HasSuffix(lower, "apikey") ||
		strings.HasSuffix(lower, "private_key") ||
		strings.HasSuffix(lower, "client_secret") ||
		strings.HasSuffix(lower, "password") ||
		strings.HasSuffix(lower, "passwd")
}

func formatOptionAuditValue(key, raw string) any {
	if !isSensitiveOptionKey(key) {
		var value any
		if common.Unmarshal([]byte(raw), &value) == nil {
			return redactOptionAuditJSON(value)
		}
		return raw
	}
	configured := strings.TrimSpace(raw) != ""
	masked := "unset"
	status := "empty"
	if configured {
		masked = "***"
		status = "configured"
	}
	return map[string]any{
		"configured": configured,
		"redacted":   true,
		"status":     status,
		"masked":     masked,
	}
}

func redactOptionAuditJSON(value any) any {
	switch object := value.(type) {
	case map[string]any:
		for key, field := range object {
			if isSensitiveOptionKey(key) {
				object[key] = map[string]any{"redacted": true, "configured": field != nil && fmt.Sprint(field) != ""}
			} else {
				object[key] = redactOptionAuditJSON(field)
			}
		}
	case []any:
		for index, field := range object {
			object[index] = redactOptionAuditJSON(field)
		}
	}
	return value
}

func buildOptionAuditParams(key, beforeRaw, afterRaw string) map[string]any {
	beforeVal := formatOptionAuditValue(key, beforeRaw)
	afterVal := formatOptionAuditValue(key, afterRaw)
	if isSensitiveOptionKey(key) {
		return map[string]any{
			"key":        key,
			"sensitive":  true,
			"configured": strings.TrimSpace(afterRaw) != "",
			"changed":    beforeRaw != afterRaw,
			"before":     beforeVal,
			"after":      afterVal,
		}
	}
	return map[string]any{
		"key":    key,
		"before": beforeVal,
		"after":  afterVal,
	}
}

func readOptionFallbackFromMap(key string) string {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	if common.OptionMap == nil {
		return ""
	}
	return common.OptionMap[key]
}

func readCurrentOptionRawValueLocked(tx *gorm.DB, key string) (string, error) {
	if tx == nil {
		return readOptionFallbackFromMap(key), nil
	}
	var row model.Option
	lookup := model.AdtyLockForUpdate(tx).Where(&model.Option{Key: key}).Limit(1).Find(&row)
	if lookup.Error != nil {
		return "", lookup.Error
	}
	if lookup.RowsAffected == 0 {
		return readOptionFallbackFromMap(key), nil
	}
	return row.Value, nil
}

func readCurrentOptionRawValue(db *gorm.DB, key string) string {
	if db != nil && db.Migrator().HasTable(&model.Option{}) {
		var row model.Option
		lookup := db.Where(&model.Option{Key: key}).Limit(1).Find(&row)
		if lookup.Error == nil && lookup.RowsAffected > 0 {
			return row.Value
		}
	}
	return readOptionFallbackFromMap(key)
}

func isGroupConfigOptionKey(key string) bool {
	switch key {
	case "GroupRatio", "UserUsableGroups", "TopupGroupRatio", "GroupGroupRatio", "AutoGroups",
		"group_ratio_setting.group_special_usable_group", "ModelRequestRateLimitGroup", model.AdtyGroupModelAllowOption:
		return true
	default:
		return false
	}
}

var completionRatioMetaOptionKeys = []string{
	"ModelPrice",
	"ModelRatio",
	"CompletionRatio",
	"CacheRatio",
	"CreateCacheRatio",
	"ImageRatio",
	"AudioRatio",
	"AudioCompletionRatio",
}

func collectModelNamesFromOptionValue(raw string, modelNames map[string]struct{}) {
	if strings.TrimSpace(raw) == "" {
		return
	}

	var parsed map[string]any
	if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
		return
	}

	for modelName := range parsed {
		modelNames[modelName] = struct{}{}
	}
}

func buildCompletionRatioMetaValue(optionValues map[string]string) string {
	modelNames := make(map[string]struct{})
	for _, key := range completionRatioMetaOptionKeys {
		collectModelNamesFromOptionValue(optionValues[key], modelNames)
	}

	meta := make(map[string]ratio_setting.CompletionRatioInfo, len(modelNames))
	for modelName := range modelNames {
		meta[modelName] = ratio_setting.GetCompletionRatioInfo(modelName)
	}

	jsonBytes, err := common.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(jsonBytes)
}

func GetOptions(c *gin.Context) {
	var options []*model.Option
	optionValues := make(map[string]string)
	common.OptionMapRWMutex.Lock()
	for k, v := range common.OptionMap {
		if strings.HasPrefix(k, "model_deployment.") || k == "theme.frontend" || k == "billing_setting.billing_mode" || k == "billing_setting.billing_expr" || k == "ResendKeyConfigured" {
			continue
		}
		value := common.Interface2String(v)
		if isSensitiveOptionKey(k) {
			continue
		}
		options = append(options, &model.Option{
			Key:   k,
			Value: value,
		})
		if slices.Contains(completionRatioMetaOptionKeys, k) {
			optionValues[k] = value
		}
	}
	options = append(options, &model.Option{
		Key: "ResendKeyConfigured", Value: strconv.FormatBool(strings.TrimSpace(common.OptionMap["ResendApiKey"]) != ""),
	})
	common.OptionMapRWMutex.Unlock()
	// Display the same effective expressions used by pricing and settlement,
	// including built-in defaults absent from persisted administrator options.
	for key, values := range map[string]map[string]string{
		"billing_setting.billing_mode": billing_setting.GetBillingModeCopy(),
		"billing_setting.billing_expr": billing_setting.GetBillingExprCopy(),
	} {
		encoded, err := common.Marshal(values)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
		options = append(options, &model.Option{Key: key, Value: string(encoded)})
	}
	options = append(options, &model.Option{
		Key:   "CompletionRatioMeta",
		Value: buildCompletionRatioMetaValue(optionValues),
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    options,
	})
}

type OptionUpdateRequest struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

func UpdatePasskeyDomains(c *gin.Context) {
	var request struct {
		RPID                *string `json:"rp_id"`
		LegacyRPIDs         *string `json:"legacy_rp_ids"`
		Origins             *string `json:"origins"`
		Preview             bool    `json:"preview"`
		RemovalConfirmation string  `json:"removal_confirmation"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.RPID == nil || request.LegacyRPIDs == nil || request.Origins == nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	change, err := model.UpdatePasskeyDomainOptions(map[string]string{
		"passkey.rp_id": *request.RPID, "passkey.legacy_rp_ids": *request.LegacyRPIDs, "passkey.origins": *request.Origins,
	}, request.Preview, request.RemovalConfirmation)
	if err != nil {
		writePasskeyDomainSettingsError(c, err)
		if !request.Preview {
			recordPasskeyDomainAudit(c, change, request.RemovalConfirmation != "", err)
		}
		return
	}
	if !request.Preview {
		recordPasskeyDomainAudit(c, change, request.RemovalConfirmation != "", nil)
	}
	common.ApiSuccess(c, change)
}

func writePasskeyDomainSettingsError(c *gin.Context, err error) {
	var removal *model.PasskeyDomainRemovalError
	if errors.As(err, &removal) {
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("Review the affected Passkeys and confirm the domain removal. If the settings or impact have changed, confirmation is required again."), gin.H{
			"code": "PASSKEY_RP_ID_REMOVAL_CONFIRMATION_REQUIRED",
			"data": removal.Change,
		})
		return
	}
	if errors.Is(err, system_setting.ErrPasskeyRPIDInvalid) {
		writeSecurityOperationError(c, err)
		return
	}
	common.ApiError(c, err)
}

func UpdateOption(c *gin.Context) {
	var option OptionUpdateRequest
	err := common.DecodeJson(c.Request.Body, &option)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	switch option.Value.(type) {
	case bool:
		option.Value = common.Interface2String(option.Value.(bool))
	case float64:
		option.Value = common.Interface2String(option.Value.(float64))
	case int:
		option.Value = common.Interface2String(option.Value.(int))
	default:
		option.Value = fmt.Sprintf("%v", option.Value)
	}
	switch option.Key {
	// Deleted with invite rewards, redemption codes and subscriptions
	// (docs/adty/deleted-features.md); nothing reads them any more.
	case "QuotaForInviter", "QuotaForInvitee", "TopUpLink", "WaffoSubscriptionReturnUrl",
		// Telegram 只是通知通道（约定 I）；OAuth 登录链已退役，开关和客户端凭据
		// 都不再接受写入，历史明文凭据由 model.InitOptionMap 清理。
		"TelegramOAuthEnabled", "telegram.client_id", "telegram.client_secret":
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	case adty_setting.TopupSettingKeyBonusRates:
		common.ApiError(c, service.ErrAdtyAmountTierRetired)
		return
	case adty_setting.EpaySettingKeyChannelTimeout, adty_setting.EpaySettingKeyTotalTimeout:
		// Cross-key validation runs below after locking both SQL rows.
	default:
		if strings.HasPrefix(option.Key, "model_deployment.") {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		// 二开：分站设置只能走分站管理的设置接口，那里会校验金额参数和依赖项（P11）
		if strings.HasPrefix(option.Key, adty_setting.ResellerSettingModule+".") {
			common.ApiErrorT(c, "Reseller settings can only be changed on the reseller settings page")
			return
		}
		if strings.HasPrefix(option.Key, adty_setting.ReferralKeyPrefix) {
			common.ApiErrorT(c, "Referral settings can only be changed on the referral settings page")
			return
		}
		if strings.HasPrefix(option.Key, "adty_marketing_setting.") || strings.HasPrefix(option.Key, "adty_site_referral.") || strings.HasPrefix(option.Key, "adty_site_invite_withdraw.") {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	if option.Key == "TaskPublicAddress" && option.Value.(string) != "" {
		if err := service.ValidateTaskArtifactBaseURL(option.Value.(string)); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	// 二开：分组类配置里的 ID 必须在分组表里，GroupRatio 不能漏掉还在的分组（任务 2.2）
	if err := service.ValidateAdtyGroupOption(option.Key, option.Value.(string)); err != nil {
		common.ApiError(c, err)
		return
	}
	switch option.Key {
	case "EmailDomainRestrictionEnabled":
		if option.Value == "true" && len(common.EmailDomainWhitelist) == 0 {
			common.ApiErrorT(c, "Cannot enable the email domain restriction. Enter the allowed email domains first.")
			return
		}
	case "TurnstileCheckEnabled":
		if option.Value == "true" && common.TurnstileSiteKey == "" {
			common.ApiErrorT(c, "Cannot enable Turnstile verification. Enter the Turnstile settings first.")
			return
		}
	case "theme.frontend":
		if option.Value != "default" {
			common.ApiErrorT(c, "The Classic frontend has been removed. The theme can only be set to default")
			return
		}
	case "GroupRatio":
		err = ratio_setting.CheckGroupRatio(option.Value.(string))
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "gemini.safety_settings":
		err = model_setting.ValidateGeminiSafetySettings(option.Value.(string))
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "claude.default_max_tokens":
		err = model_setting.ValidateClaudeDefaultMaxTokens(option.Value.(string))
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case operation_setting.ToolPriceOptionKey:
		err = operation_setting.ValidateToolPricesJSON(option.Value.(string))
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "ImageRatio", "AudioRatio", "AudioCompletionRatio", "CreateCacheRatio":
		parsed := map[string]float64{}
		if err = common.UnmarshalJsonStr(option.Value.(string), &parsed); err != nil || parsed == nil {
			common.ApiErrorT(c, "Failed to set the ratio: {{error}}", map[string]any{"error": "must be a JSON object"})
			return
		}
	case "ModelRequestRateLimitGroup":
		err = setting.CheckModelRequestRateLimitGroup(option.Value.(string))
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "AutomaticDisableStatusCodes":
		_, err = operation_setting.ParseHTTPStatusCodeRanges(option.Value.(string))
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "AutomaticRetryStatusCodes":
		_, err = operation_setting.ParseHTTPStatusCodeRanges(option.Value.(string))
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "billing_setting.billing_expr":
		expressions := make(map[string]string)
		if err = common.UnmarshalJsonStr(option.Value.(string), &expressions); err != nil {
			common.ApiErrorT(c, "Billing expressions must be a JSON object mapping models to expressions: {{error}}", map[string]any{"error": err.Error()})
			return
		}
		models := make([]string, 0, len(expressions))
		for modelName := range expressions {
			models = append(models, modelName)
		}
		sort.Strings(models)
		storedVariants := billing_setting.GetPluginBillingExprCopy()
		for _, modelName := range models {
			variants := make(map[string]any)
			for key, expression := range storedVariants {
				if plugin, name, ok := billing_setting.SplitPluginBillingExprKey(key); ok && name == modelName {
					variants[plugin] = expression
				}
			}
			err = model.ValidateModelPricing(modelName, model.PricingValues{
				"billing_setting.billing_expr":          expressions[modelName],
				billing_setting.PluginBillingExprOption: variants,
			})
			if err != nil {
				common.ApiErrorT(c, "Invalid billing expression for model {{model}}: {{error}}", map[string]any{"model": modelName, "error": err.Error()})
				return
			}
		}
	case billing_setting.PluginBillingExprOption:
		var expressions map[string]string
		if err = common.UnmarshalJsonStr(option.Value.(string), &expressions); err != nil || expressions == nil {
			common.ApiErrorMsg(c, "plugin billing expressions must be a JSON object")
			return
		}
		for key, expression := range expressions {
			plugin, name, valid := billing_setting.SplitPluginBillingExprKey(key)
			if !valid {
				common.ApiErrorMsg(c, "invalid plugin billing expression key: "+key)
				return
			}
			if err = model.ValidateModelPricing(name, model.PricingValues{
				billing_setting.PluginBillingExprOption: map[string]any{plugin: expression},
			}); err != nil {
				common.ApiError(c, err)
				return
			}
		}
	case "console_setting.api_info":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "ApiInfo")
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "console_setting.announcements":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "Announcements")
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "console_setting.faq":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "FAQ")
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "ServerAddress":
		if err = service.ValidateMainSiteDomainSeparation(option.Value.(string), adty_setting.GetSiteSetting().APIBaseURL); err != nil {
			common.ApiError(c, err)
			return
		}
	case "adty_site_setting.api_base_url":
		if err = service.ValidateMainSiteDomainSeparation(system_setting.ServerAddress, option.Value.(string)); err != nil {
			common.ApiError(c, err)
			return
		}
	case "adty_site_setting.tawk_property_id", "adty_site_setting.tawk_widget_id":
		if err = service.ValidateAdtyTawkID(option.Value.(string)); err != nil {
			common.ApiError(c, err)
			return
		}
	case "console_setting.uptime_kuma_groups":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "UptimeKumaGroups")
		if err != nil {
			common.ApiError(c, err)
			return
		}
	}
	if model.IsPasskeyDomainOption(option.Key) {
		if option.Key == "ServerAddress" {
			optionUpdateCrossKeyMu.Lock()
			defer optionUpdateCrossKeyMu.Unlock()
			if err := service.ValidateMainSiteDomainSeparation(option.Value.(string), adty_setting.GetSiteSetting().APIBaseURL); err != nil {
				common.ApiError(c, err)
				return
			}
		}
		change, updateErr := model.UpdatePasskeyDomainOptions(map[string]string{option.Key: option.Value.(string)}, false, "")
		if updateErr != nil {
			writePasskeyDomainSettingsError(c, updateErr)
			recordPasskeyDomainAudit(c, change, false, updateErr)
			return
		}
		if option.Key == "ServerAddress" {
			service.InvalidateAdtySiteCache()
		}
		recordPasskeyDomainAudit(c, change, false, nil)
		common.ApiSuccess(c, change)
		return
	}
	valueStr := option.Value.(string)
	var auditEvent model.AuditLog
	if adty_setting.IsEpayTimeoutKey(option.Key) {
		var channelSec, totalSec int
		var values map[string]string
		err = model.DB.Transaction(func(tx *gorm.DB) error {
			defaultCh := adty_setting.DefaultEpayChannelTimeoutSeconds
			defaultTot := adty_setting.DefaultEpayTotalTimeoutSeconds
			for _, seed := range []model.Option{
				{Key: adty_setting.EpaySettingKeyChannelTimeout, Value: strconv.Itoa(defaultCh)},
				{Key: adty_setting.EpaySettingKeyTotalTimeout, Value: strconv.Itoa(defaultTot)},
			} {
				if seedErr := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoNothing: true}).Create(&seed).Error; seedErr != nil {
					return seedErr
				}
			}
			lockedChannel, lockErr := model.LockOptionValueTx(tx, adty_setting.EpaySettingKeyChannelTimeout)
			if lockErr != nil {
				return lockErr
			}
			lockedTotal, lockErr := model.LockOptionValueTx(tx, adty_setting.EpaySettingKeyTotalTimeout)
			if lockErr != nil {
				return lockErr
			}
			currentLocked := map[string]string{
				adty_setting.EpaySettingKeyChannelTimeout: lockedChannel,
				adty_setting.EpaySettingKeyTotalTimeout:   lockedTotal,
			}
			var valErr error
			channelSec, totalSec, valErr = adty_setting.ValidateEpayTimeoutWithCurrent(option.Key, valueStr, currentLocked)
			if valErr != nil {
				return valErr
			}
			beforeRaw := currentLocked[option.Key]
			auditBefore := formatOptionAuditValue(option.Key, beforeRaw)
			auditAfter := formatOptionAuditValue(option.Key, valueStr)
			auditParams := buildOptionAuditParams(option.Key, beforeRaw, valueStr)
			auditEvent = prepareManageAuditChange(c, c.GetInt("id"), "option.update", auditParams, auditBefore, auditAfter, "", "")
			values = map[string]string{
				adty_setting.EpaySettingKeyChannelTimeout: strconv.Itoa(channelSec),
				adty_setting.EpaySettingKeyTotalTimeout:   strconv.Itoa(totalSec),
			}
			if saveErr := model.SaveOptionsTx(tx, values); saveErr != nil {
				return saveErr
			}
			return model.AppendAuditIntentTx(tx, auditEvent)
		})
		if err == nil {
			err = model.ApplyOptionValues(values)
		}
	} else {
		optionUpdateCrossKeyMu.Lock()
		defer optionUpdateCrossKeyMu.Unlock()
		if option.Key == "adty_site_setting.api_base_url" {
			if err = service.ValidateMainSiteDomainSeparation(system_setting.ServerAddress, valueStr); err != nil {
				common.ApiError(c, err)
				return
			}
		}
		err = model.UpdateOptionWithAudit(option.Key, valueStr, auditEvent, func(beforeRaw string) model.AuditLog {
			auditBefore := formatOptionAuditValue(option.Key, beforeRaw)
			auditAfter := formatOptionAuditValue(option.Key, valueStr)
			auditParams := buildOptionAuditParams(option.Key, beforeRaw, valueStr)
			auditEvent = prepareManageAuditChange(c, c.GetInt("id"), "option.update", auditParams, auditBefore, auditAfter, "", "")
			return auditEvent
		})
	}
	if err != nil {
		if errors.Is(err, system_setting.ErrPasskeyRPIDInvalid) {
			writeSecurityOperationError(c, err)
		} else {
			common.ApiError(c, err)
		}
		return
	}
	if option.Key == "adty_site_setting.api_base_url" {
		service.InvalidateAdtySiteCache()
	}
	deliverCommittedAudits(c, []model.AuditLog{auditEvent})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}
