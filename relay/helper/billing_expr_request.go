package helper

import (
	"bytes"
	"encoding/json"
	"maps"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

func ResolveIncomingBillingExprRequestInput(c *gin.Context, info *relaycommon.RelayInfo) (billingexpr.RequestInput, error) {
	if info != nil && info.BillingRequestInput != nil {
		input := cloneRequestInput(*info.BillingRequestInput)
		merged := cloneStringMap(info.RequestHeaders)
		maps.Copy(merged, input.Headers)
		input.Headers = merged
		return input, nil
	}

	input := billingexpr.RequestInput{}
	if info != nil {
		input.Headers = cloneStringMap(info.RequestHeaders)
	}

	bodyBytes, err := readIncomingBillingExprBody(c)
	if err != nil {
		return billingexpr.RequestInput{}, err
	}
	if info != nil && len(bodyBytes) > 0 && bytes.Contains(bodyBytes, []byte("_")) {
		_, isGeminiReq := info.Request.(*dto.GeminiChatRequest)
		if isGeminiReq || info.RelayMode == relayconstant.RelayModeGemini {
			if normalized, ok := normalizeGeminiBillingBody(bodyBytes); ok {
				bodyBytes = normalized
			}
		}
	}
	input.Body = bodyBytes
	return input, nil
}

func normalizeGeminiBillingBody(raw []byte) ([]byte, bool) {
	var root map[string]any
	if err := common.Unmarshal(raw, &root); err != nil || root == nil {
		return raw, false
	}
	changed := false
	if _, hasCamel := root["generationConfig"]; !hasCamel {
		if snakeGen, ok := root["generation_config"]; ok {
			root["generationConfig"] = snakeGen
			changed = true
		}
	}
	if gen, ok := root["generationConfig"].(map[string]any); ok && gen != nil {
		for _, pair := range [][2]string{
			{"thinkingConfig", "thinking_config"},
			{"imageConfig", "image_config"},
			{"maxOutputTokens", "max_output_tokens"},
			{"candidateCount", "candidate_count"},
			{"responseModalities", "response_modalities"},
			{"mediaResolution", "media_resolution"},
		} {
			if _, hasCamel := gen[pair[0]]; !hasCamel {
				if v, ok := gen[pair[1]]; ok {
					gen[pair[0]] = v
					changed = true
				}
			}
		}
		if tc, ok := gen["thinkingConfig"].(map[string]any); ok && tc != nil {
			for _, pair := range [][2]string{
				{"thinkingBudget", "thinking_budget"},
				{"thinkingLevel", "thinking_level"},
				{"includeThoughts", "include_thoughts"},
			} {
				if _, hasCamel := tc[pair[0]]; !hasCamel {
					if v, ok := tc[pair[1]]; ok {
						tc[pair[0]] = v
						changed = true
					}
				}
			}
		}
		if ic, ok := gen["imageConfig"].(map[string]any); ok && ic != nil {
			for _, pair := range [][2]string{
				{"imageSize", "image_size"},
				{"aspectRatio", "aspect_ratio"},
			} {
				if _, hasCamel := ic[pair[0]]; !hasCamel {
					if v, ok := ic[pair[1]]; ok {
						ic[pair[0]] = v
						changed = true
					}
				}
			}
		}
	}
	if !changed {
		return raw, false
	}
	encoded, err := common.Marshal(root)
	if err != nil {
		return raw, false
	}
	return encoded, true
}

// ResolveImageBillingRequestInput freezes only the validated scalar image
// parameters needed by pricing. Image files, prompts and base64 payloads are
// deliberately excluded, including for multipart edits.
func ResolveImageBillingRequestInput(c *gin.Context, info *relaycommon.RelayInfo, input billingexpr.RequestInput) (billingexpr.RequestInput, error) {
	request, ok := info.Request.(*dto.ImageRequest)
	if !ok {
		return input, nil
	}
	count, err := request.ImageCount(false)
	if err != nil {
		return input, err
	}
	body := map[string]any{"model": request.Model, "n": count, "size": request.Size, "quality": request.Quality}
	if request.ResponseFormat != "" {
		body["response_format"] = request.ResponseFormat
	}
	if request.Watermark != nil {
		body["watermark"] = *request.Watermark
	}
	addScalarRaw := func(key string, raw json.RawMessage) {
		if len(raw) == 0 {
			return
		}
		if _, exists := body[key]; exists {
			return
		}
		var value any
		if err := common.Unmarshal(raw, &value); err != nil {
			return
		}
		switch value.(type) {
		case string, float64, bool:
			body[key] = value
		}
	}
	addScalarRaw("style", request.Style)
	addScalarRaw("background", request.Background)
	addScalarRaw("moderation", request.Moderation)
	addScalarRaw("output_format", request.OutputFormat)
	addScalarRaw("output_compression", request.OutputCompression)
	addScalarRaw("partial_images", request.PartialImages)
	addScalarRaw("input_fidelity", request.InputFidelity)
	addScalarRaw("complexity", request.Complexity)
	addScalarRaw("frame_count", request.FrameCount)
	addScalarRaw("frame_duration_ms", request.FrameDurationMs)
	addScalarRaw("watermark_enabled", request.WatermarkEnabled)
	// Freeze provider-specific scalar params (e.g. xAI grok-imagine's
	// resolution and aspect_ratio) so pricing expressions can branch on them.
	// Non-scalar extras are excluded to keep large payloads out of the
	// frozen body; known fields above are never overwritten.
	for key, raw := range request.Extra {
		if key == "" {
			continue
		}
		addScalarRaw(key, raw)
	}
	if request.BillingParameters != nil {
		body["parameters"] = request.BillingParameters
	}
	encoded, err := common.Marshal(body)
	if err != nil {
		return input, err
	}
	input.Body = encoded
	input.ImageCount = &count
	return input, nil
}

func BuildBillingExprRequestInputFromRequest(request dto.Request, headers map[string]string) (billingexpr.RequestInput, error) {
	input := billingexpr.RequestInput{
		Headers: cloneStringMap(headers),
	}
	if request == nil {
		return input, nil
	}

	bodyBytes, err := common.Marshal(request)
	if err != nil {
		return billingexpr.RequestInput{}, err
	}
	input.Body = bodyBytes
	return input, nil
}

func readIncomingBillingExprBody(c *gin.Context) ([]byte, error) {
	if c == nil || c.Request == nil || !isJSONContentType(c.Request.Header.Get("Content-Type")) {
		return nil, nil
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	return storage.Bytes()
}

func cloneRequestInput(src billingexpr.RequestInput) billingexpr.RequestInput {
	input := src
	input.Headers = cloneStringMap(src.Headers)
	input.Usage = maps.Clone(src.Usage)
	if src.ImageCount != nil {
		count := *src.ImageCount
		input.ImageCount = &count
	}
	if len(src.Body) > 0 {
		input.Body = append([]byte(nil), src.Body...)
	}
	return input
}

func isJSONContentType(contentType string) bool {
	return common.IsJSONMediaType(common.NormalizedContentMediaType(contentType))
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		if strings.TrimSpace(key) == "" {
			continue
		}
		dst[key] = value
	}
	return dst
}
