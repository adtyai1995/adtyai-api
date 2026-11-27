package siliconflow

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
	openaiAdaptor openai.Adaptor
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, req *dto.ClaudeRequest) (any, error) {
	return a.openaiAdaptor.ConvertClaudeRequest(c, info, req)
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	if info != nil && info.RelayMode == constant.RelayModeAudioSpeech &&
		(info.IsStream || strings.EqualFold(strings.TrimSpace(request.StreamFormat), "sse")) {
		return nil, types.NewErrorWithStatusCode(errors.New("streaming is not supported for siliconflow tts"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	return a.openaiAdaptor.ConvertAudioRequest(c, info, request)
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	if (info != nil && info.IsStream) || (request.Stream != nil && *request.Stream) {
		return nil, types.NewErrorWithStatusCode(errors.New("streaming image generation is not supported for this channel"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	// 解析extra到SFImageRequest里，以填入SiliconFlow特殊字段。若失败重建一个空的。
	sfRequest := &SFImageRequest{}
	extra, err := common.Marshal(request.Extra)
	if err == nil {
		err = common.Unmarshal(extra, sfRequest)
		if err != nil {
			sfRequest = &SFImageRequest{}
		}
	}

	sfRequest.Model = request.Model
	sfRequest.Prompt = request.Prompt
	// R5-014: Always derive billing-relevant size and batch count from billed request.Size/N
	// so request.Extra cannot silently multiply generated images or upscale resolution.
	sfRequest.ImageSize = request.Size
	if request.N != nil && *request.N > 0 {
		sfRequest.BatchSize = *request.N
	} else {
		sfRequest.BatchSize = 1
	}

	return sfRequest, nil
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	a.openaiAdaptor.Init(info)
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.RelayMode == constant.RelayModeRerank {
		return fmt.Sprintf("%s/v1/rerank", info.ChannelBaseUrl), nil
	}
	if info.RelayFormat == types.RelayFormatClaude &&
		!info.ChannelSetting.PassThroughBodyEnabled &&
		!model_setting.GetGlobalSettings().PassThroughRequestEnabled &&
		info.RelayMode != constant.RelayModeResponses &&
		info.RelayMode != constant.RelayModeResponsesCompact {
		return fmt.Sprintf("%s/v1/chat/completions", info.ChannelBaseUrl), nil
	}
	return relaycommon.GetFullRequestURL(info.ChannelBaseUrl, info.RequestURLPath, info.ChannelType), nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	if info != nil && info.RelayMode == constant.RelayModeImagesEdits && !info.ChannelSetting.PassThroughBodyEnabled && !model_setting.GetGlobalSettings().PassThroughRequestEnabled {
		req.Set("Content-Type", gin.MIMEJSON)
	}
	req.Set("Authorization", fmt.Sprintf("Bearer %s", info.ApiKey))
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	// SiliconFlow requires messages array for FIM requests, even if client doesn't send it
	if (request.Prefix != nil || request.Suffix != nil) && len(request.Messages) == 0 {
		// Add an empty user message to satisfy SiliconFlow's requirement
		request.Messages = []dto.Message{
			{
				Role:    "user",
				Content: "",
			},
		}
	}
	return request, nil
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	// TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if info != nil && info.IsStream {
		if info.RelayMode == constant.RelayModeAudioSpeech {
			return nil, types.NewErrorWithStatusCode(errors.New("streaming is not supported for siliconflow tts"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		if info.RelayMode == constant.RelayModeImagesGenerations || info.RelayMode == constant.RelayModeImagesEdits {
			return nil, types.NewErrorWithStatusCode(errors.New("streaming image generation is not supported for this channel"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
	}
	if _, ok := requestBody.(channel.ContentTypeBody); ok ||
		(info != nil && (info.RelayMode == constant.RelayModeAudioTranscription ||
			info.RelayMode == constant.RelayModeAudioTranslation ||
			(info.RelayMode == constant.RelayModeImagesEdits &&
				(info.ChannelSetting.PassThroughBodyEnabled || model_setting.GetGlobalSettings().PassThroughRequestEnabled) &&
				(c == nil || c.Request == nil || !strings.Contains(strings.ToLower(c.Request.Header.Get("Content-Type")), "application/json"))))) {
		return channel.DoFormRequest(a, c, info, requestBody)
	}
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	switch info.RelayMode {
	case constant.RelayModeRerank:
		usage, err = siliconflowRerankHandler(c, info, resp)
	default:
		usage, err = a.openaiAdaptor.DoResponse(c, resp, info)
	}
	return
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
