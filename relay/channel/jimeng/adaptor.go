package jimeng

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(*gin.Context, *relaycommon.RelayInfo, *dto.ClaudeRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s/?Action=CVProcess&Version=2022-08-31", info.ChannelBaseUrl), nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	return errors.New("not implemented")
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	return request, nil
}

type LogoInfo struct {
	AddLogo         bool    `json:"add_logo,omitempty"`
	Position        int     `json:"position,omitempty"`
	Language        int     `json:"language,omitempty"`
	Opacity         float64 `json:"opacity,omitempty"`
	LogoTextContent string  `json:"logo_text_content,omitempty"`
}

type imageRequestPayload struct {
	ReqKey     string   `json:"req_key"`                      // Service identifier, fixed value: jimeng_high_aes_general_v21_L
	Prompt     string   `json:"prompt"`                       // Prompt for image generation, supports both Chinese and English
	Seed       int64    `json:"seed,omitempty"`               // Random seed, default -1 (random)
	Width      int      `json:"width,omitempty"`              // Image width, default 512, range [256, 768]
	Height     int      `json:"height,omitempty"`             // Image height, default 512, range [256, 768]
	UsePreLLM  bool     `json:"use_pre_llm,omitempty"`        // Enable text expansion, default true
	UseSR      bool     `json:"use_sr,omitempty"`             // Enable super resolution, default true
	ReturnURL  bool     `json:"return_url,omitempty"`         // Whether to return image URL (valid for 24 hours)
	LogoInfo   LogoInfo `json:"logo_info,omitempty"`          // Watermark information
	ImageUrls  []string `json:"image_urls,omitempty"`         // Image URLs for input
	BinaryData []string `json:"binary_data_base64,omitempty"` // Base64 encoded binary data
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	payload := imageRequestPayload{
		ReqKey: request.Model,
		Prompt: request.Prompt,
	}
	if request.ResponseFormat == "" || request.ResponseFormat == "url" {
		payload.ReturnURL = true // Default to returning image URLs
	}

	if len(request.ExtraFields) > 0 {
		if err := common.Unmarshal(request.ExtraFields, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal extra fields: %w", err)
		}
		// R5-014: Restore billing-critical model and prompt after ExtraFields merge.
		payload.ReqKey = request.Model
		payload.Prompt = request.Prompt
	}

	return payload, nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return nil, errors.New("not implemented")
}

type jimengCancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *jimengCancelBody) Close() error {
	err := b.ReadCloser.Close()
	if b.cancel != nil {
		b.cancel()
	}
	return err
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if c == nil || c.Request == nil {
		return nil, errors.New("client request is missing")
	}
	fullRequestURL, err := a.GetRequestURL(info)
	if err != nil {
		return nil, fmt.Errorf("get request url failed: %w", err)
	}
	upstreamCtx, cancel, cancelErr := channel.DetachedNonStreamContext(c, info, 0)
	if cancelErr != nil {
		return nil, cancelErr
	}
	req, err := http.NewRequestWithContext(upstreamCtx, c.Request.Method, fullRequestURL, requestBody)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, fmt.Errorf("new request failed: %w", err)
	}
	channel.ApplyUpstreamBodyMetadata(req, requestBody)
	headerOverride, err := channel.ResolveHeaderOverride(info, c)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	for key, value := range headerOverride {
		// Sign computes Volcengine HMAC-SHA256 against req.URL.Host and sets
		// Host, X-Date, X-Content-Sha256, and Authorization itself. Apply overrides
		// before Sign (skipping Host so req.Host cannot diverge from the signed host)
		// so custom headers including Content-Type take effect without breaking signing.
		if strings.EqualFold(key, "Host") {
			continue
		}
		req.Header.Set(key, value)
	}
	err = Sign(c, req, info.ApiKey)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, fmt.Errorf("setup request header failed: %w", err)
	}
	resp, err := channel.DoRequest(c, req, info)
	if err != nil {
		doErr := fmt.Errorf("do request failed: %w", err)
		if cancel != nil {
			cancel()
		}
		return nil, doErr
	}
	if cancel != nil {
		if resp != nil && resp.Body != nil {
			resp.Body = &jimengCancelBody{ReadCloser: resp.Body, cancel: cancel}
		} else {
			cancel()
		}
	}
	return resp, nil
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if info.RelayMode == relayconstant.RelayModeImagesGenerations {
		usage, err = jimengImageHandler(c, resp, info)
	} else if info.IsStream {
		usage, err = openai.OaiStreamHandler(c, info, resp)
	} else {
		usage, err = openai.OpenaiHandler(c, info, resp)
	}
	return
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
