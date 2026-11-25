package service

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

const (
	// AdtyGroupProbeIdentity 是主动可用性探针的独立身份标识，不关联任何客户、团队或分站钱包。
	AdtyGroupProbeIdentity = "adty-group-probe"
	// AdtyGroupProbeUserID 固定为 0，确保探针绝不触发客户扣费、团队记账、分站抽成或邀请返佣。
	AdtyGroupProbeUserID = 0
	// AdtyGroupProbeHeader 标记由平台独立探针发起的上游探测请求。
	AdtyGroupProbeHeader = "X-Adty-Probe-Identity"

	adtyProbeProtocolGemini = "gemini"
)

// R-32 (#5) / R-33 (#2) / R6-29: Legacy full-model group-health background probes,
// per-request observers, alert reconcilers, and view/config helpers have been retired
// in favor of the built-in single-model-per-group probe engine in adty_model_probe.go.
// Only the probe identity, outcome classification, and channel resolution helpers
// reused by adty_model_probe.go remain here.

// ValidateAdtyGroupProbeIdentity 校验主动探针的身份：必须使用独立标识 AdtyGroupProbeIdentity、
// user_id=0 且绝不能使用任何客户 Token。
func ValidateAdtyGroupProbeIdentity(identity string, userID int, token string) error {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		identity = AdtyGroupProbeIdentity
	}
	if identity != AdtyGroupProbeIdentity || userID != AdtyGroupProbeUserID {
		return ErrAdtyTranslationInvalid
	}
	if strings.TrimSpace(token) != "" {
		if err := rejectAdtyCustomerToken(token); err != nil {
			return err
		}
	}
	return nil
}

// AdtyGroupProbeResult 描述一次主动探针调用的响应状态与错误分类信息。
type AdtyGroupProbeResult struct {
	StatusCode int
	ErrorCode  string
	ErrorType  string
	Err        error
}

// ClassifyAdtyGroupProbeOutcome 将主动探针结果分为：
// - AdtyGroupHealthSuccess (true): 上游正常返回
// - AdtyGroupHealthCustomer (false): 客户端参数/内容拒绝，不计入上游失败
// - AdtyGroupHealthUpstream (true): 上游鉴权、限流、超额、5xx 或网络错误
func ClassifyAdtyGroupProbeOutcome(res AdtyGroupProbeResult) (string, bool) {
	code := strings.ToLower(strings.TrimSpace(res.ErrorCode))
	errType := strings.ToLower(strings.TrimSpace(res.ErrorType))
	if res.Err == nil && res.StatusCode >= http.StatusOK && res.StatusCode < http.StatusMultipleChoices && code == "" && errType == "" {
		return model.AdtyGroupHealthSuccess, true
	}
	for _, val := range []string{code, errType} {
		switch val {
		case "invalid_api_key", "api_key_invalid", "api_key_expired", "api_key_service_blocked", "invalid_authentication",
			"authentication_error", "unauthenticated", "permission_denied", "permission_error", "access_denied",
			"insufficient_quota", "quota_exceeded", "resource_exhausted", "rate_limit_exceeded", "rate_limit_error",
			"overloaded_error", "server_error", "internal_error", "service_unavailable", "model_not_found":
			return model.AdtyGroupHealthUpstream, true
		case "context_length_exceeded", "invalid_request", "invalid_request_error", "invalid_argument",
			"sensitive_words_detected", "prompt_blocked", "content_filter", "content_policy_violation", "safety",
			"bad_request_body":
			return model.AdtyGroupHealthCustomer, false
		}
		if strings.HasPrefix(val, "violation_fee.") {
			return model.AdtyGroupHealthCustomer, false
		}
	}
	switch res.StatusCode {
	case http.StatusBadRequest, http.StatusMethodNotAllowed, http.StatusConflict,
		http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return model.AdtyGroupHealthCustomer, false
	}
	return model.AdtyGroupHealthUpstream, true
}

// adtyProbeChannelProtocol 返回内置探活引擎支持的上游协议，与系统翻译的
// adtyChannelProtocol 解耦，放行探活侧已适配的 Gemini / VolcEngine / ZhipuV4 /
// Ali / Perplexity 及全部 OpenAI / Anthropic 兼容渠道。
func adtyProbeChannelProtocol(channelType int) (string, error) {
	switch channelType {
	case constant.ChannelTypeGemini:
		return adtyProbeProtocolGemini, nil
	case constant.ChannelTypeVolcEngine,
		constant.ChannelTypeZhipu_v4,
		constant.ChannelTypeAli,
		constant.ChannelTypePerplexity:
		return adtyTranslationProtocolOpenAI, nil
	default:
		return adtyChannelProtocol(channelType)
	}
}

func normalizeAdtyProbeBaseURL(channelType int, baseURL, protocol string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(constant.GetChannelBaseURL(channelType))
	}
	if special, ok := constant.ChannelSpecialBases[baseURL]; ok {
		if protocol == adtyTranslationProtocolAnthropic && special.ClaudeBaseURL != "" {
			baseURL = special.ClaudeBaseURL
		} else if special.OpenAIBaseURL != "" {
			baseURL = special.OpenAIBaseURL
		}
	}
	if channelType == constant.ChannelTypeOpenRouter && strings.TrimSuffix(baseURL, "/") == "https://openrouter.ai/api" {
		return "https://openrouter.ai/api/v1"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return baseURL
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	if protocol == adtyTranslationProtocolAnthropic {
		if path != "" && !strings.HasSuffix(path, "/v1") && !strings.HasSuffix(path, "/messages") {
			parsed.Path = path + "/v1"
			return parsed.String()
		}
		return baseURL
	}
	switch channelType {
	case constant.ChannelTypeVolcEngine:
		if !strings.HasSuffix(path, "/api/v3") &&
			!strings.HasSuffix(path, "/v3") &&
			!strings.HasSuffix(path, "/v1") &&
			!strings.HasSuffix(path, "/chat/completions") &&
			!strings.HasSuffix(path, "/images/generations") {
			parsed.Path = path + "/api/v3"
			return parsed.String()
		}
	case constant.ChannelTypeZhipu_v4:
		if !strings.HasSuffix(path, "/api/paas/v4") &&
			!strings.HasSuffix(path, "/paas/v4") &&
			!strings.HasSuffix(path, "/v4") &&
			!strings.HasSuffix(path, "/v1") &&
			!strings.HasSuffix(path, "/chat/completions") &&
			!strings.HasSuffix(path, "/images/generations") {
			parsed.Path = path + "/api/paas/v4"
			return parsed.String()
		}
	case constant.ChannelTypeAli:
		if !strings.HasSuffix(path, "/compatible-mode/v1") &&
			!strings.HasSuffix(path, "/v1") &&
			!strings.HasSuffix(path, "/chat/completions") &&
			!strings.HasSuffix(path, "/images/generations") {
			parsed.Path = path + "/compatible-mode/v1"
			return parsed.String()
		}
	case constant.ChannelTypePerplexity:
		if path == "" {
			parsed.Path = "/chat/completions"
			return parsed.String()
		}
	}
	return baseURL
}

func resolveAdtyProbeCandidate(ch *model.Channel, modelName string) (upstreamModel, protocol, endpoint, key string, err error) {
	if ch == nil || ch.Status != common.ChannelStatusEnabled {
		return "", "", "", "", ErrAdtyTranslationInvalid
	}
	protocol, err = adtyProbeChannelProtocol(ch.Type)
	if err != nil {
		return "", "", "", "", err
	}
	upstreamModel, _ = adtyChannelSupportsModel(ch, modelName)
	if upstreamModel == "" {
		upstreamModel = modelName
	}
	baseURL := normalizeAdtyProbeBaseURL(ch.Type, ch.GetBaseURL(), protocol)
	endpoint, err = adtyTranslationEndpoint(baseURL, protocol)
	if err != nil {
		return "", "", "", "", err
	}
	rawKey, _, keyErr := ch.GetNextEnabledKey()
	if keyErr != nil {
		return "", "", "", "", ErrAdtyTranslationInvalid
	}
	key = strings.TrimSpace(rawKey)
	if key == "" {
		return "", "", "", "", ErrAdtyTranslationInvalid
	}
	if err := rejectAdtyCustomerToken(key); err != nil {
		return "", "", "", "", err
	}
	return upstreamModel, protocol, endpoint, key, nil
}

func resolveAdtyGroupProbeChannel(group, modelName string) (*model.Channel, string, string, string, string, error) {
	if model.DB == nil || !model.DB.Migrator().HasTable(&model.Channel{}) {
		return nil, "", "", "", "", ErrAdtyTranslationInvalid
	}
	tried := make(map[int]struct{})
	sawBlocked := false

	if model.DB.Migrator().HasTable(&model.Ability{}) {
		var channelIDs []int
		err := model.DB.Table("abilities").
			Select("abilities.channel_id").
			Joins("JOIN channels ON channels.id = abilities.channel_id").
			Where("abilities.enabled = ? AND abilities."+model.AdtyGroupColumn()+" = ? AND abilities.model = ? AND channels.status = ?",
				true, group, modelName, common.ChannelStatusEnabled).
			Order("COALESCE(abilities.priority, 0) DESC, abilities.weight DESC, channels.id DESC").
			Pluck("abilities.channel_id", &channelIDs).Error
		if err == nil {
			for _, id := range channelIDs {
				if _, seen := tried[id]; seen {
					continue
				}
				tried[id] = struct{}{}
				var ch model.Channel
				if err := model.DB.First(&ch, id).Error; err != nil || ch.Status != common.ChannelStatusEnabled {
					continue
				}
				upstreamModel, protocol, endpoint, key, candErr := resolveAdtyProbeCandidate(&ch, modelName)
				if candErr == nil {
					return &ch, upstreamModel, protocol, endpoint, key, nil
				}
				if errors.Is(candErr, ErrAdtyTranslationSelf) {
					sawBlocked = true
				}
			}
		}
	}

	var channels []model.Channel
	if err := model.DB.Where("status = ?", common.ChannelStatusEnabled).
		Order("COALESCE(priority, 0) DESC, COALESCE(weight, 0) DESC, id DESC").
		Find(&channels).Error; err != nil {
		return nil, "", "", "", "", err
	}
	for i := range channels {
		ch := &channels[i]
		if _, seen := tried[ch.Id]; seen {
			continue
		}
		tried[ch.Id] = struct{}{}
		if !slices.Contains(ch.GetGroups(), group) {
			continue
		}
		if _, ok := adtyChannelSupportsModel(ch, modelName); !ok {
			continue
		}
		upstreamModel, protocol, endpoint, key, candErr := resolveAdtyProbeCandidate(ch, modelName)
		if candErr == nil {
			return ch, upstreamModel, protocol, endpoint, key, nil
		}
		if errors.Is(candErr, ErrAdtyTranslationSelf) {
			sawBlocked = true
		}
	}

	if sawBlocked {
		return nil, "", "", "", "", ErrAdtyTranslationSelf
	}
	return nil, "", "", "", "", ErrAdtyTranslationInvalid
}
