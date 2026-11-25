package openai

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel/openrouter"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/tokenkit"

	"github.com/gin-gonic/gin"
)

func clampNonNegativeUsage(usage *dto.Usage) bool {
	if usage == nil {
		return false
	}
	changed := false
	clamp := func(v *int) {
		if *v < 0 {
			*v = 0
			changed = true
		}
	}
	clampInput := func(d *dto.InputTokenDetails) {
		if d == nil {
			return
		}
		clamp(&d.CachedTokens)
		clamp(&d.CachedCreationTokens)
		clamp(&d.CacheWriteTokens)
		clamp(&d.CacheCreationInputTokens)
		clamp(&d.TextTokens)
		clamp(&d.AudioTokens)
		clamp(&d.ImageTokens)
		if d.CacheWriteTokensDetails != nil {
			clamp(&d.CacheWriteTokensDetails.TTL30mTokens)
			clamp(&d.CacheWriteTokensDetails.TTL12hTokens)
		}
	}
	clampOutput := func(d *dto.OutputTokenDetails) {
		if d == nil {
			return
		}
		clamp(&d.TextTokens)
		clamp(&d.AudioTokens)
		clamp(&d.ImageTokens)
		clamp(&d.ReasoningTokens)
	}
	clamp(&usage.PromptTokens)
	clamp(&usage.CompletionTokens)
	clamp(&usage.TotalTokens)
	clamp(&usage.PromptCacheHitTokens)
	clamp(&usage.InputTokens)
	clamp(&usage.OutputTokens)
	clamp(&usage.ReasoningTokens)
	clamp(&usage.ClaudeCacheCreation5mTokens)
	clamp(&usage.ClaudeCacheCreation1hTokens)
	clampInput(&usage.PromptTokensDetails)
	clampOutput(&usage.CompletionTokenDetails)
	clampInput(usage.InputTokensDetails)
	clampOutput(usage.OutputTokensDetails)
	if usage.BillingUsage != nil {
		sanitized := dto.SanitizeBillingUsage(usage.BillingUsage)
		if sanitized == nil {
			changed = true
		}
		usage.BillingUsage = sanitized
	}
	return changed
}

func sendStreamData(c *gin.Context, info *relaycommon.RelayInfo, data string, forceFormat bool, thinkToContent bool) error {
	if data == "" {
		return nil
	}

	if !forceFormat && !thinkToContent {
		return wrapStreamWriteError(helper.StringData(c, data))
	}

	var lastStreamResponse dto.ChatCompletionsStreamResponse
	if err := common.UnmarshalJsonStr(data, &lastStreamResponse); err != nil {
		return err
	}

	if !thinkToContent {
		return wrapStreamWriteError(helper.ObjectData(c, lastStreamResponse))
	}

	hasThinkingContent := false
	hasContent := false
	var thinkingContent strings.Builder
	for _, choice := range lastStreamResponse.Choices {
		if len(choice.Delta.GetReasoningContent()) > 0 {
			hasThinkingContent = true
			thinkingContent.WriteString(choice.Delta.GetReasoningContent())
		}
		if len(choice.Delta.GetContentString()) > 0 {
			hasContent = true
		}
	}

	// Handle think to content conversion
	if info.ThinkingContentInfo.IsFirstThinkingContent {
		if hasThinkingContent {
			response := lastStreamResponse.Copy()
			for i := range response.Choices {
				// send `think` tag with thinking content
				response.Choices[i].Delta.SetContentString("<think>\n" + thinkingContent.String())
				response.Choices[i].Delta.ReasoningContent = nil
				response.Choices[i].Delta.Reasoning = nil
			}
			info.ThinkingContentInfo.IsFirstThinkingContent = false
			info.ThinkingContentInfo.HasSentThinkingContent = true
			return wrapStreamWriteError(helper.ObjectData(c, response))
		}
	}

	if lastStreamResponse.Choices == nil || len(lastStreamResponse.Choices) == 0 {
		return wrapStreamWriteError(helper.ObjectData(c, lastStreamResponse))
	}

	// Process each choice
	for i, choice := range lastStreamResponse.Choices {
		// Handle transition from thinking to content
		// only send `</think>` tag when previous thinking content has been sent
		if hasContent && !info.ThinkingContentInfo.SendLastThinkingContent && info.ThinkingContentInfo.HasSentThinkingContent {
			response := lastStreamResponse.Copy()
			for j := range response.Choices {
				response.Choices[j].Delta.SetContentString("\n</think>\n")
				response.Choices[j].Delta.ReasoningContent = nil
				response.Choices[j].Delta.Reasoning = nil
			}
			info.ThinkingContentInfo.SendLastThinkingContent = true
			if err := helper.ObjectData(c, response); err != nil {
				return wrapStreamWriteError(err)
			}
		}

		// Convert reasoning content to regular content if any
		if len(choice.Delta.GetReasoningContent()) > 0 {
			lastStreamResponse.Choices[i].Delta.SetContentString(choice.Delta.GetReasoningContent())
			lastStreamResponse.Choices[i].Delta.ReasoningContent = nil
			lastStreamResponse.Choices[i].Delta.Reasoning = nil
		} else if !hasThinkingContent && !hasContent {
			// flush thinking content
			lastStreamResponse.Choices[i].Delta.ReasoningContent = nil
			lastStreamResponse.Choices[i].Delta.Reasoning = nil
		}
	}

	return wrapStreamWriteError(helper.ObjectData(c, lastStreamResponse))
}

func parseEmbeddedHTTPStatus(candidates ...any) int {
	for _, candidate := range candidates {
		switch v := candidate.(type) {
		case int:
			if v >= 400 && v <= 599 {
				return v
			}
		case int64:
			if v >= 400 && v <= 599 {
				return int(v)
			}
		case float64:
			if !math.IsNaN(v) && !math.IsInf(v, 0) && math.Trunc(v) == v && v >= 400 && v <= 599 {
				return int(v)
			}
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 400 && n <= 599 {
				return n
			}
		}
	}
	return 0
}

func hasNonEmptyErrorCode(code any) bool {
	if code == nil {
		return false
	}
	if s, ok := code.(string); ok {
		return strings.TrimSpace(s) != ""
	}
	return true
}

// ExtractEmbeddedOpenAIError inspects a JSON payload (such as an HTTP 200 body
// or SSE data frame) for a non-empty embedded OpenAI "error" field and resolves
// the HTTP status code from any embedded 4xx/5xx status/code or fallbackStatus
// (defaulting to 502 Bad Gateway).
func ExtractEmbeddedOpenAIError(raw []byte, fallbackStatus int) (*types.OpenAIError, int) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' || !bytes.Contains(trimmed, []byte(`"error"`)) {
		return nil, 0
	}
	var payload struct {
		Status     any `json:"status"`
		StatusCode any `json:"status_code"`
		Code       any `json:"code"`
		Error      any `json:"error"`
	}
	if err := common.Unmarshal(trimmed, &payload); err != nil || payload.Error == nil {
		return nil, 0
	}
	if s, ok := payload.Error.(string); ok && strings.TrimSpace(s) == "" {
		return nil, 0
	}
	oaiErr := dto.GetOpenAIError(payload.Error)
	if oaiErr == nil || (strings.TrimSpace(oaiErr.Type) == "" && strings.TrimSpace(oaiErr.Message) == "" && !hasNonEmptyErrorCode(oaiErr.Code)) {
		return nil, 0
	}
	var statusCode int
	if errMap, ok := payload.Error.(map[string]any); ok {
		statusCode = parseEmbeddedHTTPStatus(errMap["status_code"], errMap["status"], errMap["code"], payload.StatusCode, payload.Status, payload.Code)
	} else {
		statusCode = parseEmbeddedHTTPStatus(oaiErr.Code, payload.StatusCode, payload.Status, payload.Code)
	}
	if statusCode == 0 {
		if fallbackStatus >= 400 && fallbackStatus <= 599 {
			statusCode = fallbackStatus
		} else {
			statusCode = http.StatusBadGateway
		}
	}
	return oaiErr, statusCode
}

func OaiStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, common.LogText("invalid response or response body"))
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	defer service.CloseResponseBodyGracefully(resp)

	model := info.UpstreamModelName
	var responseId string
	var createAt int64 = 0
	var systemFingerprint string
	var containStreamUsage bool
	var responseTextBuilder strings.Builder
	var toolCount int
	var usage = &dto.Usage{}
	var lastStreamData string
	var secondLastStreamData string // 保留倒数第二个stream data；部分兼容网关把完整usage放在倒数第二个事件
	var receivedValidFrame bool
	seenStreamToolCalls := make(map[string]struct{})
	var streamFunctionCallNames []string
	var streamErr *types.NewAPIError
	var embeddedErrStopped bool

	// OpenAI format forwards every frame the moment it is read. The one
	// exception is a usage-only chunk (usage present, choices empty) when the
	// client's own stream_options did not ask for usage: the gateway requested
	// that chunk itself (ForceStreamOption), so it is billed but not delivered.
	// Frames that carry choices are always delivered, including a terminal
	// finish_reason/tool_calls frame with piggybacked usage. Claude/Gemini
	// conversions keep the lag-by-one path because HandleFinalResponse needs
	// the unsent terminal frame for their closing events.
	directForward := info.RelayFormat == types.RelayFormatOpenAI

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if len(data) > 0 {
			if oaiErr, statusCode := ExtractEmbeddedOpenAIError(common.StringToByteSlice(data), http.StatusBadGateway); oaiErr != nil {
				if !helper.HasWrittenStreamPayload(c) && (info == nil || info.SendResponseCount == 0) {
					streamErr = types.WithOpenAIError(*oaiErr, statusCode)
					sr.Stop(streamErr)
					return
				}
				embeddedErr := types.WithOpenAIError(*oaiErr, statusCode)
				logger.LogError(c, common.LogText("upstream stream returned embedded error after payload written: %s", embeddedErr.Error()))
				sr.Error(embeddedErr)
				embeddedErrStopped = true
				sr.Stop(nil)
				return
			}
		}
		if !directForward && lastStreamData != "" {
			if err := HandleStreamFormat(c, info, lastStreamData, info.ChannelSetting.ForceFormat, info.ChannelSetting.ThinkingToContent); err != nil {
				common.SysLog(common.LogText("error handling stream format: %s", err.Error()))
				if isStreamWriteError(err) || helper.ClientRequestCanceled(c) {
					if info != nil && info.StreamStatus != nil {
						info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
					}
					sr.Stop(err)
					return
				}
				sr.Error(err)
			}
		}
		if len(data) == 0 {
			return
		}
		if lastStreamData != "" {
			secondLastStreamData = lastStreamData
		}

		lastStreamData = data
		observeStreamChoices(info, data, seenStreamToolCalls, &streamFunctionCallNames)
		if err := processTokenData(info, data, &responseTextBuilder, &toolCount); err != nil {
			logger.LogError(c, common.LogText("error processing stream token data: %s", err.Error()))
			sr.Error(err)
		} else {
			receivedValidFrame = true
		}
		if !directForward {
			return
		}
		if !info.ShouldIncludeUsage {
			var frame dto.ChatCompletionsStreamResponse
			if err := common.UnmarshalJsonStr(data, &frame); err == nil && frame.Usage != nil && len(frame.Choices) == 0 {
				return
			}
		}
		if err := HandleStreamFormat(c, info, data, info.ChannelSetting.ForceFormat, info.ChannelSetting.ThinkingToContent); err != nil {
			common.SysLog(common.LogText("error handling stream format: %s", err.Error()))
			if isStreamWriteError(err) || helper.ClientRequestCanceled(c) {
				if info != nil && info.StreamStatus != nil {
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
				}
				sr.Stop(err)
				return
			}
			sr.Error(err)
		}
	})

	if streamErr != nil {
		return nil, streamErr
	}

	info.StreamStatus.RequireTerminal()

	if !helper.HasWrittenStreamPayload(c) && (info == nil || info.SendResponseCount == 0) &&
		(helper.ClientRequestCanceled(c) || (info != nil && info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone)) {
		var cancelErr error
		if info != nil && info.StreamStatus != nil && info.StreamStatus.EndError != nil {
			cancelErr = info.StreamStatus.EndError
		} else if c != nil && c.Request != nil && c.Request.Context().Err() != nil {
			cancelErr = c.Request.Context().Err()
		} else {
			cancelErr = errors.New("client closed request")
		}
		return nil, types.NewError(cancelErr, types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry())
	}

	if lastStreamData == "" || (!receivedValidFrame && info.SendResponseCount == 0) {
		return nil, types.NewOpenAIError(errors.New("empty upstream stream response"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
	}

	// 处理最后的响应
	if err := handleLastResponse(lastStreamData, &responseId, &createAt, &systemFingerprint, &model, &usage,
		&containStreamUsage); err != nil {
		logger.LogError(c, common.LogText("error handling last response: %s, lastStreamData: [%s]", err.Error(), lastStreamData))
	}

	// 部分兼容网关把完整的累计usage附在倒数第二个事件上，随后发送一个空的最后事件。
	// 仅当最后一个事件没有有效usage时，回退到倒数第二个事件的完整快照。
	usageFrame := lastStreamData
	if !containStreamUsage && secondLastStreamData != "" {
		var streamResp struct {
			Usage *dto.Usage `json:"usage"`
		}
		err := common.Unmarshal([]byte(secondLastStreamData), &streamResp)
		if err == nil && streamResp.Usage != nil &&
			streamResp.Usage.PromptTokens > 0 &&
			(streamResp.Usage.CompletionTokens > 0 || streamResp.Usage.TotalTokens > 0) {
			usage = dto.MergeUsageNonZero(usage, streamResp.Usage)
			containStreamUsage = true
			usageFrame = secondLastStreamData

			if common.DebugEnabled {
				logger.LogDebug(c, "usage extracted from second last SSE: PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d, InputTokens=%d, OutputTokens=%d",
					usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens,
					usage.InputTokens, usage.OutputTokens)
			}
		}
	}

	clampNonNegativeUsage(usage)
	if !containStreamUsage || (usage.PromptTokens <= 0 && usage.CompletionTokens <= 0) {
		if containStreamUsage && usage.TotalTokens > 0 {
			usage.PromptTokens = usage.TotalTokens
		} else {
			usage = service.ResponseText2Usage(c, responseTextBuilder.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
			usage.CompletionTokens += toolCount * 7
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
	} else if usage.TotalTokens <= 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	applyUsagePostProcessing(info, usage, common.StringToByteSlice(usageFrame))
	clampNonNegativeUsage(usage)

	for _, name := range streamFunctionCallNames {
		info.CountBillableToolCall(dto.BuildInCallFunctionCall, name)
	}

	if !embeddedErrStopped {
		HandleFinalResponse(c, info, lastStreamData, responseId, createAt, model, systemFingerprint, usage, containStreamUsage)
	}

	return usage, nil
}

// observeStreamChoices collects billable function call names and records the
// finish reason facts used by health sampling from one parsed chunk.
func observeStreamChoices(info *relaycommon.RelayInfo, data string, seen map[string]struct{}, names *[]string) {
	var streamResponse dto.ChatCompletionsStreamResponse
	if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
		return
	}
	for _, choice := range streamResponse.Choices {
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			if *choice.FinishReason == constant.FinishReasonContentFilter {
				info.PerformanceBusinessRejection = true
			}
			info.StreamStatus.MarkCompleted()
		}
		for i, tc := range choice.Delta.ToolCalls {
			name := strings.TrimSpace(tc.Function.Name)
			if name == "" {
				continue
			}
			toolIdx := i
			if tc.Index != nil {
				toolIdx = *tc.Index
			}
			fallbackKey := fmt.Sprintf("index\x00%d\x00%d\x00%s", choice.Index, toolIdx, name)
			activeKey := fmt.Sprintf("active\x00%d\x00%d\x00%s", choice.Index, toolIdx, name)
			callID := strings.TrimSpace(tc.ID)
			if callID != "" {
				idKey := fmt.Sprintf("id\x00%d\x00%s", choice.Index, callID)
				if _, ok := seen[idKey]; ok {
					continue
				}
				seen[idKey] = struct{}{}
				seen[activeKey] = struct{}{}
				if _, delayedID := seen[fallbackKey]; delayedID {
					delete(seen, fallbackKey)
					continue
				}
			} else {
				if _, ok := seen[fallbackKey]; ok {
					continue
				}
				if _, ok := seen[activeKey]; ok {
					continue
				}
				seen[fallbackKey] = struct{}{}
				seen[activeKey] = struct{}{}
			}
			*names = append(*names, name)
		}
	}
}

func OpenaiHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	var simpleResponse dto.OpenAITextResponse
	responseBody, apiErr := helper.ReadResponseBody(c, resp, types.ErrorCodeReadResponseBodyFailed)
	if apiErr != nil {
		return nil, apiErr
	}
	var err error
	logger.LogDebug(c, "upstream response body: %s", responseBody)
	// Unmarshal to simpleResponse
	if info.ChannelType == constant.ChannelTypeOpenRouter && info.ChannelOtherSettings.IsOpenRouterEnterprise() {
		// 尝试解析为 openrouter enterprise
		var enterpriseResponse openrouter.OpenRouterEnterpriseResponse
		err = common.Unmarshal(responseBody, &enterpriseResponse)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if enterpriseResponse.Success {
			responseBody = enterpriseResponse.Data
		} else {
			logger.LogError(c, common.LogText("openrouter enterprise response success=false, data: %s", enterpriseResponse.Data))
			return nil, types.NewOpenAIError(fmt.Errorf("openrouter response success=false"), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
	}

	err = common.Unmarshal(responseBody, &simpleResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if oaiError := simpleResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	info.ObserveResponseModel(simpleResponse.Model)
	for _, choice := range simpleResponse.Choices {
		if choice.FinishReason == constant.FinishReasonContentFilter {
			info.PerformanceBusinessRejection = true
			common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "openai_finish_reason=content_filter")
			break
		}
	}

	for _, choice := range simpleResponse.Choices {
		for _, tc := range choice.Message.ParseToolCalls() {
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, tc.Function.Name)
		}
	}
	info.ApplyVendorToolUsage(responseBody)

	forceFormat := false
	if info.ChannelSetting.ForceFormat {
		forceFormat = true
	}

	usageModified := clampNonNegativeUsage(&simpleResponse.Usage)
	if simpleResponse.Usage.PromptTokens <= 0 {
		completionTokens := max(simpleResponse.Usage.CompletionTokens, 0)
		if completionTokens <= 0 {
			for _, choice := range simpleResponse.Choices {
				ctkm := tokenkit.Count(info.UpstreamModelName, choice.Message.StringContent()+choice.Message.GetReasoningContent())
				completionTokens += ctkm
			}
		}
		fallbackUsage := &dto.Usage{
			PromptTokens:     info.GetEstimatePromptTokens(),
			CompletionTokens: completionTokens,
			TotalTokens:      info.GetEstimatePromptTokens() + completionTokens,
		}
		simpleResponse.Usage = *fallbackUsage
		usageModified = true
	} else if simpleResponse.Usage.TotalTokens <= 0 {
		simpleResponse.Usage.TotalTokens = simpleResponse.Usage.PromptTokens + simpleResponse.Usage.CompletionTokens
		usageModified = true
	}

	applyUsagePostProcessing(info, &simpleResponse.Usage, responseBody)
	if clampNonNegativeUsage(&simpleResponse.Usage) {
		usageModified = true
	}

	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		if usageModified {
			var bodyMap map[string]any
			err = common.Unmarshal(responseBody, &bodyMap)
			if err != nil {
				return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			}
			if bodyMap == nil {
				return nil, types.NewOpenAIError(errors.New("upstream returned non-object json response"), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			}
			bodyMap["usage"] = simpleResponse.Usage
			responseBody, _ = common.Marshal(bodyMap)
		}
		if forceFormat {
			responseBody, err = common.Marshal(simpleResponse)
			if err != nil {
				return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
			}
		} else {
			break
		}
	case types.RelayFormatClaude:
		convertResult, err := service.ConvertResponse(c, info, types.RelayFormatClaude, &simpleResponse)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		claudeRespStr, err := common.Marshal(convertResult.Value)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		responseBody = claudeRespStr
	case types.RelayFormatGemini:
		convertResult, err := service.ConvertResponse(c, info, types.RelayFormatGemini, &simpleResponse)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		geminiRespStr, err := common.Marshal(convertResult.Value)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		responseBody = geminiRespStr
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)

	return &simpleResponse.Usage, nil
}
