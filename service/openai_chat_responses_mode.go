package service

import (
	"regexp"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
)

// Chat→Responses upgrade policy is host routing logic (it decides *whether*
// to convert, reading host settings), so it lives here, not in relayconvert.

var chatResponsesRegexCache sync.Map // map[string]*regexp.Regexp

// SupportsOpenAIResponsesUpstreamChannelType reports whether channelType's
// adaptor speaks OpenAI Responses API wire format when RelayModeResponses is
// selected. Adaptors that translate Responses into another upstream protocol
// (e.g. Anthropic Messages, Gemini generateContent) or do not implement
// Responses must return false so Chat→Responses upgrades never feed foreign
// upstream responses into OaiResponsesToChat*Handler.
func SupportsOpenAIResponsesUpstreamChannelType(channelType int) bool {
	if channelType == constant.ChannelTypeUnknown || channelType == constant.ChannelTypeCustom {
		return false
	}
	apiType, ok := common.ChannelType2APIType(channelType)
	if !ok {
		switch channelType {
		case constant.ChannelTypeAzure,
			constant.ChannelTypeOpenAIMax,
			constant.ChannelTypeOhMyGPT,
			constant.ChannelTypeAILS,
			constant.ChannelTypeAIProxy,
			constant.ChannelTypeAPI2GPT,
			constant.ChannelTypeAIGC2D,
			constant.ChannelType360,
			constant.ChannelTypeFastGPT,
			constant.ChannelTypeLingYiWanWu:
			return true
		default:
			return false
		}
	}
	switch apiType {
	case constant.APITypeOpenAI,
		constant.APITypeOpenRouter,
		constant.APITypeXinference,
		constant.APITypeAli,
		constant.APITypeCloudflare,
		constant.APITypeCodex,
		constant.APITypeDeepSeek,
		constant.APITypeMoonshot,
		constant.APITypeNewAPI,
		constant.APITypeOllama,
		constant.APITypeSub2API,
		constant.APITypeVolcEngine,
		constant.APITypeXai,
		constant.APITypeZhipuV4,
		constant.APITypeAdvancedCustom:
		return true
	default:
		return false
	}
}

func matchAnyModelPattern(patterns []string, model string) bool {
	if len(patterns) == 0 || model == "" {
		return false
	}
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		re, ok := chatResponsesRegexCache.Load(pattern)
		if !ok {
			compiled, err := regexp.Compile(pattern)
			if err != nil {
				// Treat invalid patterns as non-matching to avoid breaking runtime traffic.
				continue
			}
			re = compiled
			chatResponsesRegexCache.Store(pattern, re)
		}
		if re.(*regexp.Regexp).MatchString(model) {
			return true
		}
	}
	return false
}

func ShouldChatCompletionsUseResponsesPolicy(policy model_setting.ChatCompletionsToResponsesPolicy, channelID int, channelType int, model string) bool {
	if !SupportsOpenAIResponsesUpstreamChannelType(channelType) {
		return false
	}
	if !policy.IsChannelEnabled(channelID, channelType) {
		return false
	}
	return matchAnyModelPattern(policy.ModelPatterns, model)
}

func ShouldChatCompletionsUseResponsesGlobal(channelID int, channelType int, model string) bool {
	return ShouldChatCompletionsUseResponsesPolicy(
		model_setting.GetGlobalSettings().ChatCompletionsToResponsesPolicy,
		channelID,
		channelType,
		model,
	)
}

func ShouldUseOpenAIResponsesModeWithPolicy(policy model_setting.ChatCompletionsToResponsesPolicy, info *relaycommon.RelayInfo) bool {
	if info == nil {
		return false
	}
	if !ShouldChatCompletionsUseResponsesPolicy(policy, info.ChannelId, info.ChannelType, info.OriginModelName) {
		return false
	}
	if constant.IsAdvancedCustomChannel(info.ChannelType) || info.ApiType == constant.APITypeAdvancedCustom {
		config := info.ChannelOtherSettings.AdvancedCustom
		if config == nil || config.Validate() != nil {
			return false
		}
		route, ok := config.MatchPathForModel("/v1/responses", info.OriginModelName)
		if !ok || !route.IsNative() {
			return false
		}
	}
	return true
}

func ShouldUseOpenAIResponsesMode(info *relaycommon.RelayInfo) bool {
	return ShouldUseOpenAIResponsesModeWithPolicy(
		model_setting.GetGlobalSettings().ChatCompletionsToResponsesPolicy,
		info,
	)
}
