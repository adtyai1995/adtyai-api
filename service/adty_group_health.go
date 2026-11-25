package service

import (
	"net/http"
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

func resolveAdtyGroupProbeChannel(group, modelName string) (*model.Channel, string, string, string, string, error) {
	if model.DB == nil || !model.DB.Migrator().HasTable(&model.Channel{}) {
		return nil, "", "", "", "", ErrAdtyTranslationInvalid
	}
	var selected *model.Channel
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
				var ch model.Channel
				if err := model.DB.First(&ch, id).Error; err == nil && ch.Status == common.ChannelStatusEnabled {
					selected = &ch
					break
				}
			}
		}
	}
	if selected == nil {
		var channels []model.Channel
		if err := model.DB.Where("status = ?", common.ChannelStatusEnabled).
			Order("COALESCE(priority, 0) DESC, COALESCE(weight, 0) DESC, id DESC").
			Find(&channels).Error; err != nil {
			return nil, "", "", "", "", err
		}
		for i := range channels {
			if !slices.Contains(channels[i].GetGroups(), group) {
				continue
			}
			if _, ok := adtyChannelSupportsModel(&channels[i], modelName); ok {
				selected = &channels[i]
				break
			}
		}
	}
	if selected == nil {
		return nil, "", "", "", "", ErrAdtyTranslationInvalid
	}
	upstreamModel, _ := adtyChannelSupportsModel(selected, modelName)
	if upstreamModel == "" {
		upstreamModel = modelName
	}
	protocol, err := adtyChannelProtocol(selected.Type)
	if err != nil {
		return nil, "", "", "", "", err
	}
	baseURL := strings.TrimSpace(selected.GetBaseURL())
	if baseURL == "" {
		baseURL = strings.TrimSpace(constant.GetChannelBaseURL(selected.Type))
	}
	endpoint, err := adtyTranslationEndpoint(baseURL, protocol)
	if err != nil {
		return nil, "", "", "", "", err
	}
	key, _, keyErr := selected.GetNextEnabledKey()
	if keyErr != nil {
		return nil, "", "", "", "", ErrAdtyTranslationInvalid
	}
	key = strings.TrimSpace(key)
	if err := rejectAdtyCustomerToken(key); err != nil {
		return nil, "", "", "", "", err
	}
	return selected, upstreamModel, protocol, endpoint, key, nil
}
