package helper

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
)

func IsGeminiSingleEmbeddingPath(path string) bool {
	trimmed := strings.TrimRight(path, "/")
	return strings.HasSuffix(trimmed, ":embedContent") || strings.HasSuffix(trimmed, "/embeddings")
}

func IsGeminiBatchEmbeddingPath(path string) bool {
	trimmed := strings.TrimRight(path, "/")
	return strings.HasSuffix(trimmed, ":batchEmbedContents")
}

func IsGeminiEmbeddingPath(path string) bool {
	return IsGeminiSingleEmbeddingPath(path) || IsGeminiBatchEmbeddingPath(path)
}

func GetAndValidateRequest(c *gin.Context, format types.RelayFormat) (request dto.Request, err error) {
	relayMode := relayconstant.EditsRelayMode(c.Request.URL.Path, c.GetInt("relay_mode"))

	switch format {
	case types.RelayFormatOpenAI:
		request, err = GetAndValidateTextRequest(c, relayMode)
	case types.RelayFormatGemini:
		if IsGeminiSingleEmbeddingPath(c.Request.URL.Path) {
			request, err = GetAndValidateGeminiEmbeddingRequest(c)
		} else if IsGeminiBatchEmbeddingPath(c.Request.URL.Path) {
			request, err = GetAndValidateGeminiBatchEmbeddingRequest(c)
		} else {
			request, err = GetAndValidateGeminiRequest(c)
		}
	case types.RelayFormatClaude:
		request, err = GetAndValidateClaudeRequest(c)
	case types.RelayFormatOpenAIResponses:
		request, err = GetAndValidateResponsesRequest(c)
	case types.RelayFormatOpenAIResponsesCompaction:
		request, err = GetAndValidateResponsesCompactionRequest(c)
	case types.RelayFormatOpenAIAlphaSearch:
		request, err = GetAndValidateAlphaSearchRequest(c)
	case types.RelayFormatDeepSeekTools:
		request, err = GetAndValidateDeepSeekToolsRequest(c)

	case types.RelayFormatOpenAIImage:
		request, err = GetAndValidOpenAIImageRequest(c, relayMode)
	case types.RelayFormatEmbedding:
		request, err = GetAndValidateEmbeddingRequest(c, relayMode)
	case types.RelayFormatRerank:
		request, err = GetAndValidateRerankRequest(c)
	case types.RelayFormatOpenAIAudio:
		request, err = GetAndValidAudioRequest(c, relayMode)
	case types.RelayFormatOpenAIRealtime:
		request = &dto.BaseRequest{}
	default:
		return nil, fmt.Errorf("unsupported relay format: %s", format)
	}
	return request, err
}

func requireJSONMediaType(c *gin.Context) error {
	if !common.IsJSONMediaType(common.NormalizedContentMediaType(c.Request.Header.Get("Content-Type"))) {
		return errors.New("Content-Type must be application/json")
	}
	return nil
}

func GetAndValidAudioRequest(c *gin.Context, relayMode int) (*dto.AudioRequest, error) {
	mediaType := common.NormalizedContentMediaType(c.Request.Header.Get("Content-Type"))
	if relayMode == relayconstant.RelayModeAudioSpeech {
		if !common.IsJSONMediaType(mediaType) {
			return nil, errors.New("Content-Type must be application/json")
		}
	} else {
		if mediaType != gin.MIMEMultipartPOSTForm {
			return nil, errors.New("Content-Type must be multipart/form-data")
		}
		form, err := common.ParseMultipartFormReusable(c)
		if err != nil {
			return nil, fmt.Errorf("failed to parse audio form request: %w", err)
		}
		defer form.RemoveAll()
		keys := make([]string, 0, len(form.Value))
		for key, values := range form.Value {
			keys = append(keys, key)
			if !strings.HasSuffix(key, "[]") && len(values) > 1 {
				return nil, fmt.Errorf("duplicate form field %q is not allowed", key)
			}
		}
		if err := common.ValidateFormFieldKeys(keys); err != nil {
			return nil, err
		}
	}
	audioRequest := &dto.AudioRequest{}
	err := common.UnmarshalBodyReusable(c, audioRequest)
	if err != nil {
		return nil, err
	}
	switch relayMode {
	case relayconstant.RelayModeAudioSpeech:
		if audioRequest.Model == "" {
			return nil, errors.New("model is required")
		}
	default:
		if audioRequest.Model == "" {
			return nil, errors.New("model is required")
		}
		if audioRequest.ResponseFormat == "" {
			audioRequest.ResponseFormat = "json"
		}
	}
	return audioRequest, nil
}

func GetAndValidateRerankRequest(c *gin.Context) (*dto.RerankRequest, error) {
	if err := requireJSONMediaType(c); err != nil {
		return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	rerankRequest := &dto.RerankRequest{}
	err := common.UnmarshalBodyReusable(c, rerankRequest)
	if err != nil {
		logger.LogError(c, common.LogText("getAndValidateTextRequest failed: %s", err.Error()))
		return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	if rerankRequest.Query == "" {
		return nil, types.NewError(fmt.Errorf("query is empty"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if len(rerankRequest.Documents) == 0 {
		return nil, types.NewError(fmt.Errorf("documents is empty"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	return rerankRequest, nil
}

func GetAndValidateEmbeddingRequest(c *gin.Context, relayMode int) (*dto.EmbeddingRequest, error) {
	if err := requireJSONMediaType(c); err != nil {
		return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	embeddingRequest := &dto.EmbeddingRequest{}
	err := common.UnmarshalBodyReusable(c, embeddingRequest)
	if err != nil {
		logger.LogError(c, common.LogText("getAndValidateTextRequest failed: %s", err.Error()))
		return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	if embeddingRequest.Input == nil {
		return nil, fmt.Errorf("input is empty")
	}
	if relayMode == relayconstant.RelayModeModerations && embeddingRequest.Model == "" {
		embeddingRequest.Model = "omni-moderation-latest"
	}
	if relayMode == relayconstant.RelayModeEmbeddings && embeddingRequest.Model == "" {
		embeddingRequest.Model = c.Param("model")
	}
	return embeddingRequest, nil
}

// maxTokensLimit bounds user-supplied max token fields. These values feed
// pre-consume quota math (preConsumedTokens * ratio); an unbounded value can
// overflow the conversion and corrupt billing.
const maxTokensLimit = math.MaxInt32 / 2

// ExceedsMaxTokensLimit checks token limits before they reach billing arithmetic.
func ExceedsMaxTokensLimit(values ...*uint) bool {
	for _, v := range values {
		if lo.FromPtrOr(v, uint(0)) > maxTokensLimit {
			return true
		}
	}
	return false
}

func GetAndValidateResponsesRequest(c *gin.Context) (*dto.OpenAIResponsesRequest, error) {
	if err := requireJSONMediaType(c); err != nil {
		return nil, err
	}
	request := &dto.OpenAIResponsesRequest{}
	err := common.UnmarshalBodyReusable(c, request)
	if err != nil {
		return nil, err
	}
	if request.Model == "" {
		return nil, errors.New("model is required")
	}
	if request.Input == nil {
		return nil, errors.New("input is required")
	}
	if ExceedsMaxTokensLimit(request.MaxOutputTokens) {
		return nil, errors.New("max_output_tokens is invalid")
	}
	return request, nil
}

func GetAndValidateAlphaSearchRequest(c *gin.Context) (*dto.AlphaSearchRequest, error) {
	if err := requireJSONMediaType(c); err != nil {
		return nil, err
	}
	request := &dto.AlphaSearchRequest{}
	if err := common.UnmarshalBodyReusable(c, request); err != nil {
		return nil, err
	}
	if request.Model == "" {
		return nil, errors.New("model is required")
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	rawBody, err := storage.Bytes()
	if err != nil {
		return nil, err
	}
	request.RawBody = rawBody
	return request, nil
}

// GetAndValidateDeepSeekToolsRequest keeps the raw body for forwarding. The
// upstream validates query/url itself, and no field changes the per-call price.
func GetAndValidateDeepSeekToolsRequest(c *gin.Context) (*dto.DeepSeekToolsRequest, error) {
	if err := requireJSONMediaType(c); err != nil {
		return nil, err
	}
	request := &dto.DeepSeekToolsRequest{}
	if err := common.UnmarshalBodyReusable(c, request); err != nil {
		return nil, err
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	rawBody, err := storage.Bytes()
	if err != nil {
		return nil, err
	}
	if len(rawBody) == 0 {
		return nil, errors.New("request body is empty")
	}
	var bodyObj map[string]any
	if err := common.Unmarshal(rawBody, &bodyObj); err != nil || bodyObj == nil {
		return nil, errors.New("request body must be a JSON object")
	}
	request.RawBody = rawBody
	return request, nil
}

func GetAndValidateResponsesCompactionRequest(c *gin.Context) (*dto.OpenAIResponsesCompactionRequest, error) {
	if err := requireJSONMediaType(c); err != nil {
		return nil, err
	}
	request := &dto.OpenAIResponsesCompactionRequest{}
	if err := common.UnmarshalBodyReusable(c, request); err != nil {
		return nil, err
	}
	if request.Model == "" {
		return nil, errors.New("model is required")
	}
	return request, nil
}

func GetAndValidOpenAIImageRequest(c *gin.Context, relayMode int) (*dto.ImageRequest, error) {
	imageRequest := &dto.ImageRequest{}

	switch relayMode {
	case relayconstant.RelayModeImagesEdits:
		if common.NormalizedContentMediaType(c.Request.Header.Get("Content-Type")) == gin.MIMEMultipartPOSTForm {
			form, err := common.ParseMultipartFormReusable(c)
			if err != nil {
				return nil, fmt.Errorf("failed to parse image edit form request: %w", err)
			}
			// R6-013 & R10-03: Reject duplicate scalar multipart fields and
			// case/Unicode-folded field collisions so upstream last-value-wins
			// or encoding/json SimpleFold cannot disagree with billing.
			keys := make([]string, 0, len(form.Value))
			for key, values := range form.Value {
				keys = append(keys, key)
				if !strings.HasSuffix(key, "[]") && !strings.HasPrefix(key, "image[") && len(values) > 1 {
					_ = form.RemoveAll()
					return nil, fmt.Errorf("duplicate form field %q is not allowed", key)
				}
			}
			if err := common.ValidateFormFieldKeys(keys); err != nil {
				_ = form.RemoveAll()
				return nil, err
			}
			formData := url.Values(form.Value)
			c.Request.MultipartForm = form
			c.Request.PostForm = formData
			imageRequest.Prompt = formData.Get("prompt")
			imageRequest.Model = formData.Get("model")
			if nValue := strings.TrimSpace(formData.Get("n")); nValue != "" {
				n, err := strconv.Atoi(nValue)
				if err != nil || n < 0 || n > dto.MaxImageN {
					return nil, fmt.Errorf("n must be an integer between 1 and %d", dto.MaxImageN)
				}
				imageRequest.N = common.GetPointer(uint(n))
			}
			imageRequest.Quality = formData.Get("quality")
			imageRequest.Size = formData.Get("size")
			if parameters := formData.Get("parameters"); parameters != "" {
				imageRequest.Extra = map[string]json.RawMessage{"parameters": json.RawMessage(parameters)}
			}
			if streamValue := strings.TrimSpace(formData.Get("stream")); streamValue != "" {
				stream, err := strconv.ParseBool(streamValue)
				if err != nil {
					return nil, fmt.Errorf("invalid stream value: %w", err)
				}
				imageRequest.Stream = common.GetPointer(stream)
			}
			if imageValue := formData.Get("image"); imageValue != "" {
				imageRequest.Image, _ = common.Marshal(imageValue)
			}

			if imageRequest.Model == "gpt-image-1" {
				if imageRequest.Quality == "" {
					imageRequest.Quality = "standard"
				}
			}
			if imageRequest.N == nil || *imageRequest.N == 0 {
				imageRequest.N = common.GetPointer(uint(1))
			}

			hasWatermark := formData.Has("watermark")
			if hasWatermark {
				watermark := formData.Get("watermark") == "true"
				imageRequest.Watermark = &watermark
			}
			// Preserve additional scalar multipart fields (e.g. resolution,
			// aspect_ratio, input_fidelity, background, output_format) in Extra
			// so ResolveImageBillingRequestInput freezes the same parameters
			// forwarded upstream by multipart adaptors.
			for key, values := range form.Value {
				if key == "" || len(values) != 1 || strings.HasSuffix(key, "[]") || strings.HasPrefix(key, "image[") {
					continue
				}
				switch key {
				case "prompt", "model", "n", "quality", "size", "parameters", "stream", "image", "mask", "watermark":
					continue
				}
				val := values[0]
				if len(val) > 4096 {
					continue
				}
				if encoded, mErr := common.Marshal(val); mErr == nil {
					if imageRequest.Extra == nil {
						imageRequest.Extra = make(map[string]json.RawMessage)
					}
					imageRequest.Extra[key] = json.RawMessage(encoded)
				}
			}
			break
		}
		fallthrough
	default:
		if err := requireJSONMediaType(c); err != nil {
			return nil, err
		}
		err := common.UnmarshalBodyReusable(c, imageRequest)
		if err != nil {
			return nil, err
		}

		if imageRequest.Model == "" {
			return nil, errors.New("model is required")
		}

		if strings.Contains(imageRequest.Size, "×") {
			return nil, errors.New("size an unexpected error occurred in the parameter, please use 'x' instead of the multiplication sign '×'")
		}

		if imageRequest.N != nil && *imageRequest.N > dto.MaxImageN {
			return nil, fmt.Errorf("n must be an integer between 1 and %d", dto.MaxImageN)
		}

		if err := imageRequest.NormalizeLegacyDalleImageRequest(); err != nil {
			return nil, err
		}
		if imageRequest.Model == "gpt-image-1" {
			if imageRequest.Quality == "" {
				imageRequest.Quality = "auto"
			}
		}

		//if imageRequest.Prompt == "" {
		//	return nil, errors.New("prompt is required")
		//}

		if imageRequest.N == nil || *imageRequest.N == 0 {
			imageRequest.N = common.GetPointer(uint(1))
		}
	}

	// Provider parameters can override the top-level count. Validate before
	// pricing so malformed multipliers return a client error, not a pricing
	// failure after reservation has started.
	if raw, exists := imageRequest.Extra["parameters"]; exists {
		parameters := &dto.ImageBillingParameters{}
		if err := common.Unmarshal(raw, parameters); err != nil {
			return nil, fmt.Errorf("invalid image parameters: %w", err)
		}
		imageRequest.BillingParameters = parameters
	}
	if _, err := imageRequest.ImageCount(false); err != nil {
		return nil, err
	}
	return imageRequest, nil
}

func GetAndValidateClaudeRequest(c *gin.Context) (textRequest *dto.ClaudeRequest, err error) {
	if err := requireJSONMediaType(c); err != nil {
		return nil, err
	}
	textRequest = &dto.ClaudeRequest{}
	err = common.UnmarshalBodyReusable(c, textRequest)
	if err != nil {
		return nil, err
	}
	if textRequest.Messages == nil || len(textRequest.Messages) == 0 {
		return nil, errors.New("field messages is required")
	}
	if textRequest.Model == "" {
		return nil, errors.New("field model is required")
	}
	if ExceedsMaxTokensLimit(textRequest.MaxTokens, textRequest.MaxTokensToSample) {
		return nil, errors.New("max_tokens is invalid")
	}

	//if textRequest.Stream {
	//	relayInfo.IsStream = true
	//}

	return textRequest, nil
}

func GetAndValidateTextRequest(c *gin.Context, relayMode int) (*dto.GeneralOpenAIRequest, error) {
	if err := requireJSONMediaType(c); err != nil {
		return nil, err
	}
	textRequest := &dto.GeneralOpenAIRequest{}
	err := common.UnmarshalBodyReusable(c, textRequest)
	if err != nil {
		return nil, err
	}

	if relayMode == relayconstant.RelayModeModerations && textRequest.Model == "" {
		textRequest.Model = "text-moderation-latest"
	}
	if relayMode == relayconstant.RelayModeEmbeddings && textRequest.Model == "" {
		textRequest.Model = c.Param("model")
	}

	if ExceedsMaxTokensLimit(textRequest.MaxTokens, textRequest.MaxCompletionTokens) {
		return nil, errors.New("max_tokens is invalid")
	}
	if ExceedsMaxTokensLimit(textRequest.MinTokens) {
		return nil, errors.New("min_tokens is invalid")
	}
	if textRequest.Model == "" {
		return nil, errors.New("model is required")
	}
	if textRequest.WebSearchOptions != nil {
		if textRequest.WebSearchOptions.SearchContextSize != "" {
			validSizes := map[string]bool{
				"high":   true,
				"medium": true,
				"low":    true,
			}
			if !validSizes[textRequest.WebSearchOptions.SearchContextSize] {
				return nil, errors.New("invalid search_context_size, must be one of: high, medium, low")
			}
		} else {
			textRequest.WebSearchOptions.SearchContextSize = "medium"
		}
	}
	switch relayMode {
	case relayconstant.RelayModeCompletions:
		if textRequest.Prompt == "" {
			return nil, errors.New("field prompt is required")
		}
	case relayconstant.RelayModeChatCompletions:
		// For FIM (Fill-in-the-middle) requests with prefix/suffix, messages is optional
		// It will be filled by provider-specific adaptors if needed (e.g., SiliconFlow)。Or it is allowed by model vendor(s) (e.g., DeepSeek)
		if len(textRequest.Messages) == 0 && textRequest.Prefix == nil && textRequest.Suffix == nil {
			return nil, errors.New("field messages is required")
		}
	case relayconstant.RelayModeEmbeddings:
	case relayconstant.RelayModeModerations:
		if textRequest.Input == nil || textRequest.Input == "" {
			return nil, errors.New("field input is required")
		}
	case relayconstant.RelayModeEdits:
		if textRequest.Instruction == "" {
			return nil, errors.New("field instruction is required")
		}
	}
	return textRequest, nil
}

func GetAndValidateGeminiRequest(c *gin.Context) (*dto.GeminiChatRequest, error) {
	if err := requireJSONMediaType(c); err != nil {
		return nil, err
	}
	request := &dto.GeminiChatRequest{}
	err := common.UnmarshalBodyReusable(c, request)
	if err != nil {
		return nil, err
	}
	if len(request.Contents) == 0 && len(request.Requests) == 0 {
		return nil, errors.New("contents is required")
	}
	if ExceedsMaxTokensLimit(request.GenerationConfig.MaxOutputTokens) {
		return nil, errors.New("maxOutputTokens is invalid")
	}

	//if c.Query("alt") == "sse" {
	//	relayInfo.IsStream = true
	//}

	return request, nil
}

func GetAndValidateGeminiEmbeddingRequest(c *gin.Context) (*dto.GeminiEmbeddingRequest, error) {
	if err := requireJSONMediaType(c); err != nil {
		return nil, err
	}
	request := &dto.GeminiEmbeddingRequest{}
	err := common.UnmarshalBodyReusable(c, request)
	if err != nil {
		return nil, err
	}
	return request, nil
}

func GetAndValidateGeminiBatchEmbeddingRequest(c *gin.Context) (*dto.GeminiBatchEmbeddingRequest, error) {
	if err := requireJSONMediaType(c); err != nil {
		return nil, err
	}
	request := &dto.GeminiBatchEmbeddingRequest{}
	err := common.UnmarshalBodyReusable(c, request)
	if err != nil {
		return nil, err
	}
	if len(request.Requests) == 0 {
		return nil, errors.New("requests is required")
	}
	for i, item := range request.Requests {
		if item == nil {
			return nil, fmt.Errorf("requests[%d] must not be null", i)
		}
	}
	return request, nil
}
