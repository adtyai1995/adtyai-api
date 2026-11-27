package zhipu_4v

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	channelconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	result, err := service.ConvertRequest(c, info, types.RelayFormatOpenAI, request)
	if err != nil {
		return nil, err
	}
	openaiRequest, ok := result.Value.(*dto.GeneralOpenAIRequest)
	if !ok {
		return nil, fmt.Errorf("expected OpenAI chat completions request, got %T", result.Value)
	}
	return a.ConvertOpenAIRequest(c, info, openaiRequest)
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, req *dto.ClaudeRequest) (any, error) {
	claudeAdaptor := claude.Adaptor{}
	return claudeAdaptor.ConvertClaudeRequest(c, info, req)
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	if info.RelayMode != relayconstant.RelayModeAudioTranscription {
		return nil, errors.New("Zhipu audio mode is not supported")
	}
	if request.ResponseFormat != "" && request.ResponseFormat != "json" {
		return nil, errors.New("Zhipu transcription supports JSON responses")
	}
	form, err := common.ParseMultipartFormReusable(c)
	if err != nil {
		return nil, err
	}
	defer form.RemoveAll()
	for _, value := range form.Value["stream"] {
		if value != "false" && value != "0" {
			return nil, errors.New("streaming Zhipu transcription is not supported")
		}
	}
	return (&openai.Adaptor{}).ConvertAudioRequest(c, info, request)
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	if (info != nil && info.IsStream) || (request.Stream != nil && *request.Stream) {
		return nil, types.NewErrorWithStatusCode(errors.New("streaming image generation is not supported for this channel"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	return request, nil
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	info.ConvOptions().WebSearch = encodeWebSearch(info)
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	baseURL := info.ChannelBaseUrl
	if baseURL == "" {
		baseURL = channelconstant.GetChannelBaseURL(channelconstant.ChannelTypeZhipu_v4)
	}
	if specialPlan, hasSpecialPlan := channelconstant.ChannelSpecialBases[baseURL]; hasSpecialPlan {
		if info.RelayMode == relayconstant.RelayModeResponses || info.RelayFormat == types.RelayFormatOpenAIResponses {
			if specialPlan.OpenAIBaseURL != "" {
				return fmt.Sprintf("%s/responses", specialPlan.OpenAIBaseURL), nil
			}
			return "", fmt.Errorf("coding plan %q does not support relay mode %d", baseURL, info.RelayMode)
		}
		if info.RelayFormat == types.RelayFormatClaude {
			if specialPlan.ClaudeBaseURL != "" {
				return fmt.Sprintf("%s/v1/messages", specialPlan.ClaudeBaseURL), nil
			}
			return "", fmt.Errorf("coding plan %q does not support relay mode %d", baseURL, info.RelayMode)
		}
		if specialPlan.OpenAIBaseURL == "" {
			return "", fmt.Errorf("coding plan %q does not support relay mode %d", baseURL, info.RelayMode)
		}
		switch info.RelayMode {
		case relayconstant.RelayModeChatCompletions, relayconstant.RelayModeGemini:
			return fmt.Sprintf("%s/chat/completions", specialPlan.OpenAIBaseURL), nil
		case relayconstant.RelayModeEmbeddings:
			return fmt.Sprintf("%s/embeddings", specialPlan.OpenAIBaseURL), nil
		case relayconstant.RelayModeImagesGenerations:
			return fmt.Sprintf("%s/images/generations", specialPlan.OpenAIBaseURL), nil
		case relayconstant.RelayModeImagesEdits:
			return fmt.Sprintf("%s/images/edits", specialPlan.OpenAIBaseURL), nil
		default:
			return "", fmt.Errorf("coding plan %q does not support relay mode %d", baseURL, info.RelayMode)
		}
	}

	if info.RelayMode == relayconstant.RelayModeResponses || info.RelayFormat == types.RelayFormatOpenAIResponses {
		return fmt.Sprintf("%s/api/v1/responses", baseURL), nil
	}

	switch info.RelayFormat {
	case types.RelayFormatClaude:
		return fmt.Sprintf("%s/api/anthropic/v1/messages", baseURL), nil
	default:
		switch info.RelayMode {
		case relayconstant.RelayModeAudioTranscription:
			return fmt.Sprintf("%s/api/paas/v4/audio/transcriptions", baseURL), nil
		case relayconstant.RelayModeEmbeddings:
			return fmt.Sprintf("%s/api/paas/v4/embeddings", baseURL), nil
		case relayconstant.RelayModeImagesGenerations:
			return fmt.Sprintf("%s/api/paas/v4/images/generations", baseURL), nil
		case relayconstant.RelayModeImagesEdits:
			return fmt.Sprintf("%s/api/paas/v4/images/edits", baseURL), nil
		default:
			return fmt.Sprintf("%s/api/paas/v4/chat/completions", baseURL), nil
		}
	}
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	if info != nil && info.RelayMode == relayconstant.RelayModeImagesEdits && !info.ChannelSetting.PassThroughBodyEnabled && !model_setting.GetGlobalSettings().PassThroughRequestEnabled {
		req.Set("Content-Type", gin.MIMEJSON)
	}
	req.Set("Authorization", "Bearer "+info.ApiKey)
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if lo.FromPtrOr(request.TopP, 0) >= 1 {
		request.TopP = lo.ToPtr(0.99)
	}
	return requestOpenAI2Zhipu(*request), nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if info != nil && (info.RelayMode == relayconstant.RelayModeImagesGenerations || info.RelayMode == relayconstant.RelayModeImagesEdits) && info.IsStream {
		return nil, types.NewErrorWithStatusCode(errors.New("streaming image generation is not supported for this channel"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if info.RelayMode == relayconstant.RelayModeAudioTranscription {
		return channel.DoFormRequest(a, c, info, requestBody)
	}
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if info.RelayMode == relayconstant.RelayModeResponses || info.RelayFormat == types.RelayFormatOpenAIResponses {
		adaptor := openai.Adaptor{}
		return adaptor.DoResponse(c, resp, info)
	}
	switch info.RelayFormat {
	case types.RelayFormatClaude:
		adaptor := claude.Adaptor{}
		return adaptor.DoResponse(c, resp, info)
	default:
		if info.RelayMode == relayconstant.RelayModeImagesGenerations || info.RelayMode == relayconstant.RelayModeImagesEdits {
			return zhipu4vImageHandler(c, resp, info)
		}
		adaptor := openai.Adaptor{}
		return adaptor.DoResponse(c, resp, info)
	}
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
