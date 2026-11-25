package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/console_setting"

	"golang.org/x/sync/errgroup"
)

const (
	adtyModelProbeConcurrency         = 4
	adtyModelProbeRetentionSeconds    = 25 * 3600
	adtyModelProbeStartupDelay        = 20 * time.Second
	adtyModelProbeSchedulerTick       = 1 * time.Minute
	adtyModelProbeScheduledRunTimeout = 5 * time.Minute
	adtyModelProbeMaxErrorBodyBytes   = 64 << 10
	adtyImageProbeMinTimeout          = 45 * time.Second
	adtyImageProbeMaxTimeout          = 90 * time.Second
	adtyImageProbeMaxBodyBytes        = 16 << 20
)

var (
	adtyModelProbeOnce        sync.Once
	adtyModelProbeRunning     atomic.Bool
	adtyModelProbeLastRunUnix atomic.Int64

	adtyModelProbeGroupLastRunMu     sync.Mutex
	adtyModelProbeGroupLastRun       = make(map[string]int64)
	adtyModelProbeGroupLastRunSeeded bool

	adtyMarkdownImageURLRegex = regexp.MustCompile(`(?i)!\[[^\]]*\]\(https?://[^)\s]+`)

	// AdtyModelProbeDispatchFunc performs one lightweight connectivity probe against
	// an upstream channel using independent probe identity (zero customer billing).
	AdtyModelProbeDispatchFunc = dispatchAdtyModelProbeCtx

	// AdtyImageProbeDispatchFunc performs one real image generation connectivity probe
	// against an upstream channel and verifies an actual image payload is returned.
	AdtyImageProbeDispatchFunc = dispatchAdtyImageProbeCtx
)

func init() {
	adtyMasterTasks = append(adtyMasterTasks, StartAdtyModelProbes)
}

// ResetAdtyModelProbeGroup removes a deleted group's cached last-run timestamps
// so re-creating or inspecting probes never sees stale scheduler state.
func ResetAdtyModelProbeGroup(groupID string) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return
	}
	prefix := groupID + "\x00"
	adtyModelProbeGroupLastRunMu.Lock()
	defer adtyModelProbeGroupLastRunMu.Unlock()
	for k := range adtyModelProbeGroupLastRun {
		if k == groupID || strings.HasPrefix(k, prefix) {
			delete(adtyModelProbeGroupLastRun, k)
		}
	}
}

// AdtyModelProbeRunSummary summarizes a completed probe batch.
type AdtyModelProbeRunSummary struct {
	Probed    int   `json:"probed"`
	Up        int   `json:"up"`
	Down      int   `json:"down"`
	CheckedAt int64 `json:"checked_at"`
}

func isAdtyEmbeddingModel(modelName string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	return strings.Contains(lower, "embedding") || strings.Contains(lower, "embed-") || strings.HasPrefix(lower, "embed")
}

func isAdtyImageModel(modelName string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "gemini-") && strings.Contains(lower, "image") {
		return true
	}
	for _, kw := range []string{
		"dall-e", "gpt-image", "gpt-4o-image", "imagen", "flux", "ideogram",
		"midjourney", "mj_", "mj-", "stable-diffusion", "sdxl", "kolors",
		"recraft", "seedream", "jimeng", "cogview", "qwen-image", "wanx",
		"nano-banana", "sora_image", "-image",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func isAdtyChatStyleImageModel(modelName string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "gemini-") && strings.Contains(lower, "image") {
		return true
	}
	for _, kw := range []string{"gpt-4o-image", "nano-banana", "sora_image"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// IsAdtyImageProbe determines whether the group+model pair should use the real
// image generation prober (and image probe schedule interval).
func IsAdtyImageProbe(group console_setting.UptimeKumaGroup, modelName string) bool {
	switch strings.ToLower(strings.TrimSpace(group.ProbeType)) {
	case "image":
		return true
	case "text":
		return false
	default:
		if strings.TrimSpace(modelName) == "" {
			modelName = group.EffectiveProbeModel()
		}
		return isAdtyImageModel(modelName)
	}
}

func isAdtyNonChatModel(modelName string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	for _, kw := range []string{
		"dall-e", "gpt-image", "imagen", "flux", "ideogram", "midjourney", "mj_", "mj-",
		"stable-diffusion", "sdxl", "kolors", "recraft", "seedream",
		"whisper", "tts", "audio", "speech", "transcribe", "suno",
		"rerank", "moderation", "sora", "kling", "vidu", "veo", "runway", "luma", "hailuo", "jimeng", "video",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func isAdtyModelMissingMessage(errCode, errType, errMsg string) bool {
	combined := strings.ToLower(strings.TrimSpace(errCode + " " + errType + " " + errMsg))
	for _, needle := range []string{
		"model_not_found",
		"does not exist",
		"not found",
		"unknown model",
		"invalid model",
		"no such model",
		"no available channel",
		"model is not supported",
	} {
		if strings.Contains(combined, needle) {
			return true
		}
	}
	return false
}

func isAdtyMaxTokensParamMismatch(errMsg string) bool {
	lower := strings.ToLower(strings.TrimSpace(errMsg))
	return strings.Contains(lower, "max_completion_tokens") ||
		(strings.Contains(lower, "max_tokens") && strings.Contains(lower, "unsupported"))
}

func deriveAdtyGeminiEndpoint(endpoint, upstreamModel, action string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" {
		return "", false
	}
	modelID := strings.TrimPrefix(strings.TrimSpace(upstreamModel), "models/")
	if modelID == "" {
		return "", false
	}
	basePath := strings.TrimSuffix(parsed.Path, "/")
	basePath = strings.TrimSuffix(basePath, "/chat/completions")
	basePath = strings.TrimSuffix(basePath, "/messages")
	basePath = strings.TrimSuffix(basePath, "/images/generations")
	basePath = strings.TrimSuffix(basePath, "/openai")
	basePath = strings.TrimSuffix(basePath, "/v1")
	basePath = strings.TrimSuffix(basePath, "/v1beta")
	parsed.Path = basePath + "/v1beta/models/" + url.PathEscape(modelID) + action
	parsed.RawQuery = ""
	return parsed.String(), true
}

func deriveAdtyVolcEngineImageEndpoint(endpoint string) string {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" {
		return endpoint
	}
	basePath := strings.TrimSuffix(parsed.Path, "/")
	basePath = strings.TrimSuffix(basePath, "/chat/completions")
	basePath = strings.TrimSuffix(basePath, "/messages")
	basePath = strings.TrimSuffix(basePath, "/images/generations")
	basePath = strings.TrimSuffix(basePath, "/api/v3")
	basePath = strings.TrimSuffix(basePath, "/v1")
	parsed.Path = basePath + "/api/v3/images/generations"
	parsed.RawQuery = ""
	return parsed.String()
}

func deriveAdtyOpenAIImageEndpoint(endpoint string) string {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" {
		if strings.HasSuffix(endpoint, "/chat/completions") {
			return strings.TrimSuffix(endpoint, "/chat/completions") + "/images/generations"
		}
		return endpoint
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/images/generations"):
		// already images endpoint
	case strings.HasSuffix(path, "/chat/completions"):
		path = strings.TrimSuffix(path, "/chat/completions") + "/images/generations"
	case strings.HasSuffix(path, "/messages"):
		path = strings.TrimSuffix(path, "/messages") + "/images/generations"
	case path == "":
		path = "/v1/images/generations"
	default:
		path += "/images/generations"
	}
	parsed.Path = path
	parsed.RawQuery = ""
	return parsed.String()
}

func deriveAdtyOpenAIChatEndpoint(endpoint string) string {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" {
		return endpoint
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		// already chat completions endpoint
	case strings.HasSuffix(path, "/images/generations"):
		path = strings.TrimSuffix(path, "/images/generations") + "/chat/completions"
	case strings.HasSuffix(path, "/messages"):
		path = strings.TrimSuffix(path, "/messages") + "/chat/completions"
	case path == "":
		path = "/v1/chat/completions"
	default:
		path += "/chat/completions"
	}
	parsed.Path = path
	parsed.RawQuery = ""
	return parsed.String()
}

func deriveAdtyModelProbeTarget(channel *model.Channel, upstreamModel, protocol, endpoint string) (probeProtocol, probeEndpoint string, payload map[string]any) {
	embedding := isAdtyEmbeddingModel(upstreamModel)

	if (channel != nil && channel.Type == constant.ChannelTypeGemini) || protocol == adtyProbeProtocolGemini {
		action := ":generateContent"
		if embedding {
			action = ":embedContent"
			payload = map[string]any{
				"content": map[string]any{
					"parts": []map[string]string{{"text": "hi"}},
				},
			}
		} else {
			payload = map[string]any{
				"contents": []map[string]any{
					{
						"role":  "user",
						"parts": []map[string]string{{"text": "hi"}},
					},
				},
				"generationConfig": map[string]any{
					"maxOutputTokens": 1,
				},
			}
		}
		if geminiURL, ok := deriveAdtyGeminiEndpoint(endpoint, upstreamModel, action); ok {
			return adtyProbeProtocolGemini, geminiURL, payload
		}
	}

	if protocol == adtyTranslationProtocolAnthropic {
		return adtyTranslationProtocolAnthropic, endpoint, map[string]any{
			"model":      upstreamModel,
			"max_tokens": 1,
			"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		}
	}

	if embedding {
		embEndpoint := endpoint
		if strings.HasSuffix(embEndpoint, "/chat/completions") {
			embEndpoint = strings.TrimSuffix(embEndpoint, "/chat/completions") + "/embeddings"
		}
		return adtyTranslationProtocolOpenAI, embEndpoint, map[string]any{
			"model": upstreamModel,
			"input": "hi",
		}
	}

	return adtyTranslationProtocolOpenAI, endpoint, map[string]any{
		"model":      upstreamModel,
		"max_tokens": 1,
		"stream":     false,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	}
}

func formatAdtyProbeHTTPMessage(statusCode int, errCode, errType string) string {
	detail := strings.TrimSpace(errCode)
	if detail == "" {
		detail = strings.TrimSpace(errType)
	}
	if detail != "" {
		if len(detail) > 80 {
			detail = detail[:80]
		}
		return fmt.Sprintf("HTTP %d: %s", statusCode, detail)
	}
	return fmt.Sprintf("HTTP %d", statusCode)
}

func parseAdtyProbeErrorPayload(raw []byte) (errCode, errType, errMsg string) {
	var parsedErr struct {
		Error struct {
			Code    any    `json:"code"`
			Type    string `json:"type"`
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = common.Unmarshal(raw, &parsedErr)

	errCode = strings.TrimSpace(common.Interface2String(parsedErr.Error.Code))
	errType = strings.TrimSpace(parsedErr.Error.Type)
	if errType == "" {
		errType = strings.TrimSpace(parsedErr.Error.Status)
	}
	errMsg = strings.TrimSpace(parsedErr.Error.Message)
	return errCode, errType, errMsg
}

func formatAdtyImageProbeError(statusCode int, raw []byte) string {
	errCode, errType, errMsg := parseAdtyProbeErrorPayload(raw)
	if isAdtyModelMissingMessage(errCode, errType, errMsg) {
		return fmt.Sprintf("HTTP %d: model not found", statusCode)
	}
	return formatAdtyProbeHTTPMessage(statusCode, errCode, errType)
}

func dispatchAdtyModelProbe(channel *model.Channel, upstreamModel, protocol, endpoint, key string, timeout time.Duration) (status int, pingMs float64, message string) {
	return dispatchAdtyModelProbeCtx(context.Background(), channel, upstreamModel, protocol, endpoint, key, timeout)
}

func dispatchAdtyModelProbeCtx(ctx context.Context, channel *model.Channel, upstreamModel, protocol, endpoint, key string, timeout time.Duration) (status int, pingMs float64, message string) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = time.Duration(console_setting.DefaultModelProbeTimeoutSeconds) * time.Second
	}

	probeProtocol, probeEndpoint, payload := deriveAdtyModelProbeTarget(channel, upstreamModel, protocol, endpoint)
	body, err := common.Marshal(payload)
	if err != nil {
		return model.AdtyModelProbeStatusDown, 0, "failed to encode probe payload"
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, probeEndpoint, bytes.NewReader(body))
	if err != nil {
		return model.AdtyModelProbeStatusDown, 0, "failed to build probe request"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(AdtyGroupProbeHeader, AdtyGroupProbeIdentity)
	switch probeProtocol {
	case adtyTranslationProtocolAnthropic:
		req.Header.Set("anthropic-version", adtyTranslationAnthropicVer)
		if key != "" {
			req.Header.Set("x-api-key", key)
		}
	case "gemini":
		if key != "" {
			req.Header.Set("x-goog-api-key", key)
		}
	default:
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}

	start := time.Now()
	resp, err := adtyTranslationHTTPClient(timeout).Do(req)
	elapsedMs := float64(time.Since(start).Microseconds()) / 1000.0
	if err != nil {
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(reqCtx.Err(), context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			return model.AdtyModelProbeStatusDown, elapsedMs, "timeout"
		}
		return model.AdtyModelProbeStatusDown, elapsedMs, classifyAdtyProbeNetworkError(err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, adtyModelProbeMaxErrorBodyBytes))
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return model.AdtyModelProbeStatusUp, elapsedMs, ""
	}

	errCode, errType, errMsg := parseAdtyProbeErrorPayload(raw)

	probeRes := AdtyGroupProbeResult{
		StatusCode: resp.StatusCode,
		ErrorCode:  errCode,
		ErrorType:  errType,
		Err:        fmt.Errorf("probe HTTP %d", resp.StatusCode),
	}
	outcome, _ := ClassifyAdtyGroupProbeOutcome(probeRes)
	switch outcome {
	case model.AdtyGroupHealthSuccess:
		return model.AdtyModelProbeStatusUp, elapsedMs, ""
	case model.AdtyGroupHealthCustomer:
		if isAdtyModelMissingMessage(errCode, errType, errMsg) {
			return model.AdtyModelProbeStatusDown, elapsedMs, fmt.Sprintf("HTTP %d: model not found", resp.StatusCode)
		}
		if isAdtyNonChatModel(upstreamModel) || isAdtyMaxTokensParamMismatch(errMsg) {
			return model.AdtyModelProbeStatusUp, elapsedMs, ""
		}
		return model.AdtyModelProbeStatusDown, elapsedMs, formatAdtyProbeHTTPMessage(resp.StatusCode, errCode, errType)
	default:
		return model.AdtyModelProbeStatusDown, elapsedMs, formatAdtyProbeHTTPMessage(resp.StatusCode, errCode, errType)
	}
}

func classifyAdtyProbeNetworkError(err error) string {
	if err == nil {
		return "network error"
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "x509") || strings.Contains(lower, "tls") || strings.Contains(lower, "certificate"):
		return "tls error"
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "lookup "):
		return "dns error"
	case strings.Contains(lower, "connection refused"):
		return "connection refused"
	default:
		return "network error"
	}
}

func effectiveAdtyImageProbeTimeout(timeout time.Duration) time.Duration {
	if timeout < adtyImageProbeMinTimeout {
		timeout = adtyImageProbeMinTimeout
	}
	if timeout > adtyImageProbeMaxTimeout {
		timeout = adtyImageProbeMaxTimeout
	}
	return timeout
}

func buildAdtyOpenAIImagePayload(upstreamModel string) map[string]any {
	lower := strings.ToLower(strings.TrimSpace(upstreamModel))
	switch {
	case strings.Contains(lower, "gpt-image"):
		return map[string]any{
			"model":   upstreamModel,
			"prompt":  "a single white dot",
			"n":       1,
			"size":    "1024x1024",
			"quality": "low",
		}
	case strings.Contains(lower, "dall-e-3"):
		return map[string]any{
			"model":   upstreamModel,
			"prompt":  "a single white dot",
			"n":       1,
			"size":    "1024x1024",
			"quality": "standard",
		}
	case strings.Contains(lower, "dall-e-2"):
		return map[string]any{
			"model":  upstreamModel,
			"prompt": "a single white dot",
			"n":      1,
			"size":   "256x256",
		}
	default:
		return map[string]any{
			"model":  upstreamModel,
			"prompt": "a single white dot",
			"n":      1,
		}
	}
}

func adtyTextContainsImage(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	if strings.Contains(strings.ToLower(trimmed), "data:image/") {
		return true
	}
	return adtyMarkdownImageURLRegex.MatchString(trimmed)
}

func adtyImageAnyEntryHasImage(item any) bool {
	switch v := item.(type) {
	case string:
		return strings.TrimSpace(v) != ""
	case map[string]any:
		for _, field := range []string{"url", "b64_json", "b64", "data"} {
			if s, ok := v[field].(string); ok && strings.TrimSpace(s) != "" {
				return true
			}
		}
		if rawImgURL, exists := v["image_url"]; exists && rawImgURL != nil {
			switch iv := rawImgURL.(type) {
			case string:
				if strings.TrimSpace(iv) != "" {
					return true
				}
			case map[string]any:
				if s, ok := iv["url"].(string); ok && strings.TrimSpace(s) != "" {
					return true
				}
			}
		}
		for _, inlineKey := range []string{"inlineData", "inline_data"} {
			if m, ok := v[inlineKey].(map[string]any); ok {
				if s, ok := m["data"].(string); ok && strings.TrimSpace(s) != "" {
					return true
				}
			}
		}
		for _, fileKey := range []string{"fileData", "file_data"} {
			if m, ok := v[fileKey].(map[string]any); ok {
				if s, ok := m["fileUri"].(string); ok && strings.TrimSpace(s) != "" {
					return true
				}
				if s, ok := m["file_uri"].(string); ok && strings.TrimSpace(s) != "" {
					return true
				}
			}
		}
	}
	return false
}

func verifyAdtyOpenAIImagePayload(raw []byte) bool {
	var decoded struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
		Images []any `json:"images"`
		Output struct {
			Results []struct {
				URL     string `json:"url"`
				B64JSON string `json:"b64_json"`
			} `json:"results"`
		} `json:"output"`
	}
	if err := common.Unmarshal(raw, &decoded); err != nil {
		return false
	}
	for _, item := range decoded.Data {
		if strings.TrimSpace(item.URL) != "" || strings.TrimSpace(item.B64JSON) != "" {
			return true
		}
	}
	for _, item := range decoded.Output.Results {
		if strings.TrimSpace(item.URL) != "" || strings.TrimSpace(item.B64JSON) != "" {
			return true
		}
	}
	for _, item := range decoded.Images {
		if adtyImageAnyEntryHasImage(item) {
			return true
		}
	}
	return false
}

// verifyAdtyImageResponsePayload strictly checks that an HTTP 2xx image probe response
// actually contains generated image data rather than an empty/text-only payload.
func verifyAdtyImageResponsePayload(kind string, raw []byte) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		return false
	}
	switch kind {
	case "gemini-imagen":
		var decoded struct {
			Predictions []struct {
				BytesBase64Encoded string `json:"bytesBase64Encoded"`
				RaiFilteredReason  string `json:"raiFilteredReason"`
			} `json:"predictions"`
		}
		if err := common.Unmarshal(raw, &decoded); err != nil {
			return false
		}
		for _, p := range decoded.Predictions {
			if strings.TrimSpace(p.BytesBase64Encoded) != "" && strings.TrimSpace(p.RaiFilteredReason) == "" {
				return true
			}
		}
		return false

	case "gemini-native":
		var decoded struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						InlineData *struct {
							Data string `json:"data"`
						} `json:"inlineData"`
						InlineDataSnake *struct {
							Data string `json:"data"`
						} `json:"inline_data"`
						FileData *struct {
							FileUri string `json:"fileUri"`
						} `json:"fileData"`
						FileDataSnake *struct {
							FileUri string `json:"file_uri"`
						} `json:"file_data"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if err := common.Unmarshal(raw, &decoded); err != nil {
			return false
		}
		for _, cand := range decoded.Candidates {
			for _, part := range cand.Content.Parts {
				if part.InlineData != nil && strings.TrimSpace(part.InlineData.Data) != "" {
					return true
				}
				if part.InlineDataSnake != nil && strings.TrimSpace(part.InlineDataSnake.Data) != "" {
					return true
				}
				if part.FileData != nil && strings.TrimSpace(part.FileData.FileUri) != "" {
					return true
				}
				if part.FileDataSnake != nil && strings.TrimSpace(part.FileDataSnake.FileUri) != "" {
					return true
				}
			}
		}
		return false

	case "chat":
		if verifyAdtyOpenAIImagePayload(raw) {
			return true
		}
		var decoded struct {
			Choices []struct {
				Message struct {
					Content any    `json:"content"`
					Images  []any  `json:"images"`
					URL     string `json:"url"`
					B64JSON string `json:"b64_json"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := common.Unmarshal(raw, &decoded); err != nil {
			return false
		}
		for _, ch := range decoded.Choices {
			if strings.TrimSpace(ch.Message.URL) != "" || strings.TrimSpace(ch.Message.B64JSON) != "" {
				return true
			}
			for _, img := range ch.Message.Images {
				if adtyImageAnyEntryHasImage(img) {
					return true
				}
			}
			switch content := ch.Message.Content.(type) {
			case string:
				if adtyTextContainsImage(content) {
					return true
				}
			case []any:
				for _, part := range content {
					if adtyImageAnyEntryHasImage(part) {
						return true
					}
					if m, ok := part.(map[string]any); ok {
						if txt, ok := m["text"].(string); ok && adtyTextContainsImage(txt) {
							return true
						}
					}
				}
			}
		}
		return false

	default:
		return verifyAdtyOpenAIImagePayload(raw)
	}
}

func doAdtyImageProbeHTTP(reqCtx context.Context, authKind, targetURL, key string, payload map[string]any, clientTimeout time.Duration) (statusCode int, raw []byte, elapsedMs float64, errMsg string) {
	body, err := common.Marshal(payload)
	if err != nil {
		return 0, nil, 0, "failed to encode probe payload"
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return 0, nil, 0, "failed to build probe request"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(AdtyGroupProbeHeader, AdtyGroupProbeIdentity)
	if authKind == "gemini" {
		if key != "" {
			req.Header.Set("x-goog-api-key", key)
		}
	} else if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	start := time.Now()
	resp, err := adtyTranslationHTTPClient(clientTimeout).Do(req)
	elapsedMs = float64(time.Since(start).Microseconds()) / 1000.0
	if err != nil {
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(reqCtx.Err(), context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			return 0, nil, elapsedMs, "timeout"
		}
		return 0, nil, elapsedMs, classifyAdtyProbeNetworkError(err)
	}
	defer resp.Body.Close()

	raw, _ = io.ReadAll(io.LimitReader(resp.Body, adtyImageProbeMaxBodyBytes))
	return resp.StatusCode, raw, elapsedMs, ""
}

func dispatchAdtyImageProbeCtx(ctx context.Context, channel *model.Channel, upstreamModel, protocol, endpoint, key string, timeout time.Duration) (status int, pingMs float64, message string) {
	if ctx == nil {
		ctx = context.Background()
	}
	imgTimeout := effectiveAdtyImageProbeTimeout(timeout)
	reqCtx, cancel := context.WithTimeout(ctx, imgTimeout)
	defer cancel()

	lowerModel := strings.ToLower(strings.TrimSpace(upstreamModel))
	isGemini := (channel != nil && channel.Type == constant.ChannelTypeGemini) || protocol == adtyProbeProtocolGemini

	// 1. Gemini Imagen (:predict)
	if isGemini && strings.Contains(lowerModel, "imagen") {
		targetURL, ok := deriveAdtyGeminiEndpoint(endpoint, upstreamModel, ":predict")
		if !ok {
			return model.AdtyModelProbeStatusDown, 0, "invalid gemini endpoint"
		}
		payload := map[string]any{
			"instances": []map[string]any{
				{"prompt": "a white dot"},
			},
			"parameters": map[string]any{
				"sampleCount": 1,
			},
		}
		statusCode, raw, elapsedMs, errMsg := doAdtyImageProbeHTTP(reqCtx, adtyProbeProtocolGemini, targetURL, key, payload, imgTimeout)
		if errMsg != "" {
			return model.AdtyModelProbeStatusDown, elapsedMs, errMsg
		}
		if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
			if verifyAdtyImageResponsePayload("gemini-imagen", raw) {
				return model.AdtyModelProbeStatusUp, elapsedMs, ""
			}
			return model.AdtyModelProbeStatusDown, elapsedMs, fmt.Sprintf("HTTP %d: no image returned", statusCode)
		}
		return model.AdtyModelProbeStatusDown, elapsedMs, formatAdtyImageProbeError(statusCode, raw)
	}

	// 2. Gemini Native Image (:generateContent with IMAGE modality)
	if isGemini {
		targetURL, ok := deriveAdtyGeminiEndpoint(endpoint, upstreamModel, ":generateContent")
		if !ok {
			return model.AdtyModelProbeStatusDown, 0, "invalid gemini endpoint"
		}
		payload := map[string]any{
			"contents": []map[string]any{
				{
					"role":  "user",
					"parts": []map[string]string{{"text": "Generate a tiny plain white square image."}},
				},
			},
			"generationConfig": map[string]any{
				"responseModalities": []string{"IMAGE", "TEXT"},
			},
		}
		statusCode, raw, elapsedMs, errMsg := doAdtyImageProbeHTTP(reqCtx, adtyProbeProtocolGemini, targetURL, key, payload, imgTimeout)
		if errMsg != "" {
			return model.AdtyModelProbeStatusDown, elapsedMs, errMsg
		}
		if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
			if verifyAdtyImageResponsePayload("gemini-native", raw) {
				return model.AdtyModelProbeStatusUp, elapsedMs, ""
			}
			return model.AdtyModelProbeStatusDown, elapsedMs, fmt.Sprintf("HTTP %d: no image returned", statusCode)
		}
		return model.AdtyModelProbeStatusDown, elapsedMs, formatAdtyImageProbeError(statusCode, raw)
	}

	// 3. Volcengine / Ark (/api/v3/images/generations)
	if channel != nil && channel.Type == constant.ChannelTypeVolcEngine {
		targetURL := deriveAdtyVolcEngineImageEndpoint(endpoint)
		payload := map[string]any{
			"model":  upstreamModel,
			"prompt": "a single white dot",
			"n":      1,
		}
		statusCode, raw, elapsedMs, errMsg := doAdtyImageProbeHTTP(reqCtx, "openai", targetURL, key, payload, imgTimeout)
		if errMsg != "" {
			return model.AdtyModelProbeStatusDown, elapsedMs, errMsg
		}
		if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
			if verifyAdtyImageResponsePayload("openai", raw) {
				return model.AdtyModelProbeStatusUp, elapsedMs, ""
			}
			return model.AdtyModelProbeStatusDown, elapsedMs, fmt.Sprintf("HTTP %d: no image returned", statusCode)
		}
		return model.AdtyModelProbeStatusDown, elapsedMs, formatAdtyImageProbeError(statusCode, raw)
	}

	// 4. OpenAI-compatible Image API (+ chat-completions fallback for chat-style image models)
	imagesURL := deriveAdtyOpenAIImageEndpoint(endpoint)
	payload := buildAdtyOpenAIImagePayload(upstreamModel)
	statusCode, raw, elapsedMs, errMsg := doAdtyImageProbeHTTP(reqCtx, "openai", imagesURL, key, payload, imgTimeout)
	if errMsg != "" {
		return model.AdtyModelProbeStatusDown, elapsedMs, errMsg
	}

	chatStyle := isAdtyChatStyleImageModel(upstreamModel)
	if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
		if verifyAdtyImageResponsePayload("openai", raw) {
			return model.AdtyModelProbeStatusUp, elapsedMs, ""
		}
		if !chatStyle {
			return model.AdtyModelProbeStatusDown, elapsedMs, fmt.Sprintf("HTTP %d: no image returned", statusCode)
		}
	} else if !chatStyle || (statusCode != http.StatusBadRequest && statusCode != http.StatusNotFound && statusCode != http.StatusMethodNotAllowed && statusCode != http.StatusUnprocessableEntity) {
		return model.AdtyModelProbeStatusDown, elapsedMs, formatAdtyImageProbeError(statusCode, raw)
	}

	// Chat-style image model fallback via /chat/completions
	chatURL := deriveAdtyOpenAIChatEndpoint(endpoint)
	chatPayload := map[string]any{
		"model":    upstreamModel,
		"stream":   false,
		"messages": []map[string]string{{"role": "user", "content": "Generate a tiny white square image."}},
	}
	fbStatusCode, fbRaw, fbElapsedMs, fbErrMsg := doAdtyImageProbeHTTP(reqCtx, "openai", chatURL, key, chatPayload, imgTimeout)
	if fbErrMsg != "" {
		return model.AdtyModelProbeStatusDown, fbElapsedMs, fbErrMsg
	}
	if fbStatusCode >= http.StatusOK && fbStatusCode < http.StatusMultipleChoices {
		if verifyAdtyImageResponsePayload("chat", fbRaw) {
			return model.AdtyModelProbeStatusUp, fbElapsedMs, ""
		}
		return model.AdtyModelProbeStatusDown, fbElapsedMs, fmt.Sprintf("HTTP %d: no image returned", fbStatusCode)
	}
	return model.AdtyModelProbeStatusDown, fbElapsedMs, formatAdtyImageProbeError(fbStatusCode, fbRaw)
}

func effectiveProbeGroupKey(g console_setting.UptimeKumaGroup) string {
	if id := strings.TrimSpace(g.GroupID); id != "" {
		return id
	}
	return strings.TrimSpace(g.CategoryName)
}

func adtyProbeTargetRunKey(groupKey, modelName string) string {
	return strings.TrimSpace(groupKey) + "\x00" + strings.TrimSpace(modelName)
}

func adtyProbeIntervalSecondsForGroup(g console_setting.UptimeKumaGroup, modelName string) int64 {
	if IsAdtyImageProbe(g, modelName) {
		return int64(console_setting.GetImageProbeIntervalMinutes()) * 60
	}
	return int64(console_setting.GetModelProbeIntervalMinutes()) * 60
}

func seedAdtyModelProbeLastRunsFromDB(now int64) {
	adtyModelProbeGroupLastRunMu.Lock()
	if adtyModelProbeGroupLastRunSeeded {
		adtyModelProbeGroupLastRunMu.Unlock()
		return
	}
	adtyModelProbeGroupLastRunSeeded = true
	adtyModelProbeGroupLastRunMu.Unlock()

	maxWindowSec := int64(console_setting.MaxImageProbeIntervalMinutes) * 60
	rows, err := model.ListAdtyModelProbesSince(now - maxWindowSec)
	if err != nil || len(rows) == 0 {
		return
	}

	adtyModelProbeGroupLastRunMu.Lock()
	defer adtyModelProbeGroupLastRunMu.Unlock()
	for _, row := range rows {
		k := adtyProbeTargetRunKey(strings.TrimSpace(row.GroupId), strings.TrimSpace(row.ModelName))
		if row.CheckedAt > adtyModelProbeGroupLastRun[k] {
			adtyModelProbeGroupLastRun[k] = row.CheckedAt
		}
	}
}

func getAdtyModelProbeGroupLastRun(groupKey, modelName string) int64 {
	adtyModelProbeGroupLastRunMu.Lock()
	defer adtyModelProbeGroupLastRunMu.Unlock()
	return adtyModelProbeGroupLastRun[adtyProbeTargetRunKey(groupKey, modelName)]
}

func setAdtyModelProbeGroupLastRun(groupKey, modelName string, checkedAt int64) {
	adtyModelProbeGroupLastRunMu.Lock()
	defer adtyModelProbeGroupLastRunMu.Unlock()
	k := adtyProbeTargetRunKey(groupKey, modelName)
	if checkedAt > adtyModelProbeGroupLastRun[k] {
		adtyModelProbeGroupLastRun[k] = checkedAt
	}
}

func hasConfiguredAdtyModelProbes() bool {
	settings := console_setting.GetConsoleSetting()
	if !settings.UptimeKumaEnabled {
		return false
	}
	for _, g := range console_setting.ParseUptimeKumaGroups(settings.UptimeKumaGroups) {
		if !g.IsEnabled() || g.EffectiveProbeModel() == "" {
			continue
		}
		if effectiveProbeGroupKey(g) != "" {
			return true
		}
	}
	return false
}

func hasDueAdtyModelProbes(now int64) bool {
	settings := console_setting.GetConsoleSetting()
	if !settings.UptimeKumaEnabled {
		return false
	}
	seedAdtyModelProbeLastRunsFromDB(now)
	for _, g := range console_setting.ParseUptimeKumaGroups(settings.UptimeKumaGroups) {
		if !g.IsEnabled() {
			continue
		}
		groupKey := effectiveProbeGroupKey(g)
		modelName := g.EffectiveProbeModel()
		if groupKey == "" || modelName == "" {
			continue
		}
		intervalSec := adtyProbeIntervalSecondsForGroup(g, modelName)
		lastRun := getAdtyModelProbeGroupLastRun(groupKey, modelName)
		if lastRun == 0 || now-lastRun >= intervalSec {
			return true
		}
	}
	return false
}

// RunAdtyModelProbesOnce executes connectivity probes for all enabled configured
// groups (each using its single primary probe model, optionally filtered to one
// filterGroupID), persists rows into adty_model_probes, and prunes records older
// than 25 hours. Manual triggers probe all matching groups regardless of interval.
func RunAdtyModelProbesOnce(ctx context.Context, filterGroupID string) (AdtyModelProbeRunSummary, error) {
	return runAdtyModelProbes(ctx, filterGroupID, false)
}

func runAdtyModelProbes(ctx context.Context, filterGroupID string, scheduled bool) (AdtyModelProbeRunSummary, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now().Unix()
	summary := AdtyModelProbeRunSummary{CheckedAt: now}

	if err := ValidateAdtyGroupProbeIdentity(AdtyGroupProbeIdentity, AdtyGroupProbeUserID, ""); err != nil {
		return summary, err
	}
	if model.DB == nil || !model.DB.Migrator().HasTable(&model.AdtyModelProbe{}) {
		return summary, nil
	}
	if scheduled {
		seedAdtyModelProbeLastRunsFromDB(now)
	}

	settings := console_setting.GetConsoleSetting()
	groups := console_setting.ParseUptimeKumaGroups(settings.UptimeKumaGroups)
	timeout := time.Duration(console_setting.GetModelProbeTimeoutSeconds()) * time.Second
	filterGroupID = strings.TrimSpace(filterGroupID)

	type probeTarget struct {
		groupID   string
		modelName string
		isImage   bool
	}
	activeGroups, activeErr := model.ActiveAdtyGroupIDs()
	if activeErr != nil {
		activeGroups = nil
	}
	targets := make([]probeTarget, 0, len(groups))
	seen := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		if !g.IsEnabled() {
			continue
		}
		groupKey := effectiveProbeGroupKey(g)
		modelName := g.EffectiveProbeModel()
		if groupKey == "" || modelName == "" {
			continue
		}
		if len(activeGroups) > 0 && strings.TrimSpace(g.GroupID) != "" {
			if _, ok := activeGroups[strings.TrimSpace(g.GroupID)]; !ok {
				continue
			}
		}
		if filterGroupID != "" && groupKey != filterGroupID {
			continue
		}
		if _, dup := seen[groupKey]; dup {
			continue
		}
		seen[groupKey] = struct{}{}

		if scheduled {
			intervalSec := adtyProbeIntervalSecondsForGroup(g, modelName)
			lastRun := getAdtyModelProbeGroupLastRun(groupKey, modelName)
			if lastRun > 0 && now-lastRun < intervalSec {
				continue
			}
		}

		targets = append(targets, probeTarget{
			groupID:   groupKey,
			modelName: modelName,
			isImage:   IsAdtyImageProbe(g, modelName),
		})
	}

	if len(targets) == 0 {
		_ = model.PruneAdtyModelProbesBefore(now - adtyModelProbeRetentionSeconds)
		if filterGroupID == "" && !scheduled {
			adtyModelProbeLastRunUnix.Store(now)
		}
		return summary, nil
	}

	var (
		mu     sync.Mutex
		eg     errgroup.Group
		probed int
		up     int
		down   int
	)
	eg.SetLimit(adtyModelProbeConcurrency)

	for _, target := range targets {
		eg.Go(func() error {
			if ctx.Err() != nil {
				return nil
			}
			var (
				status  int
				pingMs  float64
				message string
			)
			channel, upstreamModel, protocol, endpoint, key, err := resolveAdtyGroupProbeChannel(target.groupID, target.modelName)
			if err != nil {
				status = model.AdtyModelProbeStatusDown
				pingMs = 0
				if errors.Is(err, ErrAdtyTranslationSelf) {
					message = "upstream endpoint blocked"
				} else {
					message = "no enabled channel for group/model"
				}
			} else if idErr := ValidateAdtyGroupProbeIdentity(AdtyGroupProbeIdentity, AdtyGroupProbeUserID, key); idErr != nil {
				status = model.AdtyModelProbeStatusDown
				pingMs = 0
				message = "probe identity rejected"
			} else if target.isImage {
				status, pingMs, message = AdtyImageProbeDispatchFunc(ctx, channel, upstreamModel, protocol, endpoint, key, timeout)
			} else {
				status, pingMs, message = AdtyModelProbeDispatchFunc(ctx, channel, upstreamModel, protocol, endpoint, key, timeout)
			}

			checkedAt := time.Now().Unix()
			setAdtyModelProbeGroupLastRun(target.groupID, target.modelName, checkedAt)
			row := &model.AdtyModelProbe{
				GroupId:   target.groupID,
				ModelName: target.modelName,
				Status:    status,
				PingMs:    math.Round(pingMs*100) / 100,
				Message:   message,
				CheckedAt: checkedAt,
			}
			if insertErr := model.InsertAdtyModelProbe(row); insertErr != nil {
				common.SysError(common.LogText("adty model probe insert (%s/%s): %s", target.groupID, target.modelName, insertErr.Error()))
			}

			mu.Lock()
			probed++
			if status == model.AdtyModelProbeStatusUp {
				up++
			} else {
				down++
			}
			mu.Unlock()
			return nil
		})
	}
	_ = eg.Wait()

	finishedAt := time.Now().Unix()
	_ = model.PruneAdtyModelProbesBefore(finishedAt - adtyModelProbeRetentionSeconds)
	if filterGroupID == "" {
		adtyModelProbeLastRunUnix.Store(finishedAt)
	}

	summary.Probed = probed
	summary.Up = up
	summary.Down = down
	summary.CheckedAt = finishedAt
	return summary, nil
}

func runScheduledAdtyModelProbes() {
	if !adtyModelProbeRunning.CompareAndSwap(false, true) {
		return
	}
	defer adtyModelProbeRunning.Store(false)

	ctx, cancel := context.WithTimeout(context.Background(), adtyModelProbeScheduledRunTimeout)
	defer cancel()
	if _, err := runAdtyModelProbes(ctx, "", true); err != nil {
		common.SysError(common.LogText("adty model probe scheduled run failed: %s", err.Error()))
	}
}

// StartAdtyModelProbes starts the master-node periodic model connectivity probe loop.
func StartAdtyModelProbes() {
	if !common.IsMasterNode {
		return
	}
	adtyModelProbeOnce.Do(func() {
		go func() {
			startupTimer := time.NewTimer(adtyModelProbeStartupDelay)
			<-startupTimer.C

			now := time.Now().Unix()
			if hasConfiguredAdtyModelProbes() {
				seedAdtyModelProbeLastRunsFromDB(now)
				if latest, err := model.LatestAdtyModelProbeCheckedAt(); err == nil && latest > 0 {
					adtyModelProbeLastRunUnix.Store(latest)
				}
				if hasDueAdtyModelProbes(now) {
					runScheduledAdtyModelProbes()
				}
			}

			ticker := time.NewTicker(adtyModelProbeSchedulerTick)
			defer ticker.Stop()
			for range ticker.C {
				if !hasConfiguredAdtyModelProbes() {
					continue
				}
				if hasDueAdtyModelProbes(time.Now().Unix()) {
					runScheduledAdtyModelProbes()
				}
			}
		}()
	})
}

func isOpenAIReasoningModelName(lower string) bool {
	if idx := strings.LastIndex(lower, "/"); idx >= 0 {
		lower = lower[idx+1:]
	}
	for _, prefix := range []string{"o1", "o3", "o4"} {
		if lower == prefix || strings.HasPrefix(lower, prefix+"-") || strings.HasPrefix(lower, prefix+".") {
			return true
		}
	}
	return false
}

func inferAdtyPlatformFromModel(modelName string) string {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	if lower == "" {
		return ""
	}
	switch {
	case strings.Contains(lower, "claude"):
		return "anthropic"
	case strings.Contains(lower, "gemini"),
		strings.Contains(lower, "imagen"),
		strings.Contains(lower, "veo"),
		strings.Contains(lower, "gemma"),
		strings.Contains(lower, "nano-banana"):
		return "gemini"
	case strings.Contains(lower, "deepseek"):
		return "deepseek"
	case strings.Contains(lower, "glm"),
		strings.Contains(lower, "cogview"),
		strings.Contains(lower, "cogvideo"),
		strings.Contains(lower, "zhipu"):
		return "zhipu"
	case strings.Contains(lower, "moonshot"),
		strings.Contains(lower, "kimi"):
		return "moonshot"
	case strings.Contains(lower, "grok"):
		return "xai"
	case strings.Contains(lower, "azure"):
		return "azure"
	case strings.Contains(lower, "gpt-"),
		strings.Contains(lower, "gpt4"),
		strings.Contains(lower, "chatgpt"),
		strings.Contains(lower, "dall-e"),
		strings.Contains(lower, "text-embedding"),
		strings.Contains(lower, "whisper"),
		strings.Contains(lower, "tts-"),
		strings.Contains(lower, "sora"),
		strings.Contains(lower, "codex"),
		strings.Contains(lower, "openai"),
		isOpenAIReasoningModelName(lower):
		return "openai"
	default:
		return ""
	}
}

func inferAdtyPlatformFromChannelType(channelType int) string {
	switch channelType {
	case constant.ChannelTypeOpenAI, constant.ChannelTypeOpenAIMax, constant.ChannelTypeCodex, constant.ChannelTypeSora:
		return "openai"
	case constant.ChannelTypeAnthropic:
		return "anthropic"
	case constant.ChannelTypeGemini, constant.ChannelTypeVertexAi, constant.ChannelTypePaLM:
		return "gemini"
	case constant.ChannelTypeZhipu, constant.ChannelTypeZhipu_v4:
		return "zhipu"
	case constant.ChannelTypeDeepSeek:
		return "deepseek"
	case constant.ChannelTypeMoonshot:
		return "moonshot"
	case constant.ChannelTypeXai:
		return "xai"
	case constant.ChannelTypeAzure:
		return "azure"
	default:
		return ""
	}
}

func lookupAdtyGroupChannelType(groupKey, modelName string) int {
	if model.DB == nil || groupKey == "" || !model.DB.Migrator().HasTable(&model.Channel{}) {
		return 0
	}
	groupCol := model.AdtyGroupColumn()
	if groupCol == "" {
		groupCol = `"group"`
	}
	if model.DB.Migrator().HasTable(&model.Ability{}) {
		var channelType int
		if modelName != "" {
			res := model.DB.Table("abilities").
				Select("channels.type").
				Joins("JOIN channels ON channels.id = abilities.channel_id").
				Where("abilities.enabled = ? AND abilities."+groupCol+" = ? AND abilities.model = ? AND channels.status = ?",
					true, groupKey, modelName, common.ChannelStatusEnabled).
				Order("COALESCE(abilities.priority, 0) DESC, abilities.weight DESC, channels.id DESC").
				Limit(1).
				Scan(&channelType)
			if res.Error == nil && res.RowsAffected > 0 && channelType > 0 {
				return channelType
			}
		}
		res := model.DB.Table("abilities").
			Select("channels.type").
			Joins("JOIN channels ON channels.id = abilities.channel_id").
			Where("abilities.enabled = ? AND abilities."+groupCol+" = ? AND channels.status = ?",
				true, groupKey, common.ChannelStatusEnabled).
			Order("COALESCE(abilities.priority, 0) DESC, abilities.weight DESC, channels.id DESC").
			Limit(1).
			Scan(&channelType)
		if res.Error == nil && res.RowsAffected > 0 && channelType > 0 {
			return channelType
		}
	}
	return 0
}

// ResolveAdtyGroupPlatform resolves a group's public platform family from explicit
// configuration, probe model name, or channel type without exposing internal channels.
func ResolveAdtyGroupPlatform(group console_setting.UptimeKumaGroup, probeModel string) string {
	if p := strings.ToLower(strings.TrimSpace(group.Platform)); p != "" {
		return p
	}
	if strings.TrimSpace(probeModel) == "" {
		probeModel = group.EffectiveProbeModel()
	}
	if p := inferAdtyPlatformFromModel(probeModel); p != "" {
		return p
	}
	if chType := lookupAdtyGroupChannelType(effectiveProbeGroupKey(group), probeModel); chType > 0 {
		if p := inferAdtyPlatformFromChannelType(chType); p != "" {
			return p
		}
	}
	return "other"
}
