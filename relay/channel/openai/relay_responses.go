package openai

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func isResponsesEventStreamContentType(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/event-stream")
}

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 && isResponsesEventStreamContentType(resp.Header.Get("Content-Type")) {
		return OaiResponsesBufferedStreamHandler(c, info, resp)
	}
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, apiErr := helper.ReadResponseBody(c, resp, types.ErrorCodeReadResponseBodyFailed)
	if apiErr != nil {
		return nil, apiErr
	}
	if oaiErr, statusCode := ExtractEmbeddedOpenAIError(responseBody, upstreamErrorFallbackStatus(resp)); oaiErr != nil {
		return nil, types.WithOpenAIError(*oaiErr, statusCode)
	}
	err := common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	info.ObserveResponseModel(responsesResponse.Model)
	responseBody = rewriteSGLangResponsesCreatedAt(info, responseBody, "created_at", responsesResponse.CreatedAt)
	responseBody = ensureResponsesUsageReasoningDetails(responseBody, "usage")

	// 写入新的 response body；若上游在非流式分离上下文下已完成而客户端已断开，仍需结算已产生的用量。
	if !helper.ClientRequestCanceled(c) {
		service.IOCopyBytesGracefully(c, resp, responseBody)
	}

	// compute usage
	usage := &dto.Usage{}
	service.ApplyResponsesUsage(usage, responsesResponse.Usage)

	// Upstream may omit usage, or report only input tokens. The body has already
	// been delivered, so estimate the missing counts from it instead of settling
	// a zero bill, the same way ResponsesUsageAccumulator.Finish does for the
	// stream.
	if usage.CompletionTokens == 0 || usage.PromptTokens == 0 {
		text := service.ExtractOutputTextFromResponses(&responsesResponse)
		if text != "" || usage.PromptTokens == 0 {
			fallback := service.ResponseText2Usage(c, text, info.GetUpstreamModelName(), info.GetEstimatePromptTokens())
			if usage.PromptTokens == 0 {
				usage.PromptTokens = fallback.PromptTokens
			}
			if usage.CompletionTokens == 0 {
				usage.CompletionTokens = fallback.CompletionTokens
			}
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			if usage.BillingUsage != nil {
				usage.BillingUsage = dto.CloneBillingUsageWithEstimatedCompletion(usage.BillingUsage, usage.CompletionTokens)
			}
		}
	}

	// Count actual tool invocations from Output (not tool declarations).
	for _, output := range responsesResponse.Output {
		switch output.Type {
		case dto.BuildInCallWebSearchCall:
			info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
		case dto.BuildInCallFileSearchCall:
			info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
		case dto.BuildInCallFunctionCall:
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, output.Name)
		}
	}
	info.ApplyVendorToolUsage(responseBody)

	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	for i := range responsesResponse.Output {
		imageCounter.Observe(&responsesResponse.Output[i], &i)
	}
	imageCounter.Commit(info)

	return usage, nil
}

// OaiResponsesBufferedStreamHandler drains an upstream Responses SSE stream for
// a non-streaming /v1/responses request, collects the terminal OpenAI Responses
// object and its billing usage, and writes a valid JSON response to the client.
// Because the scanner reads resp.Body directly without binding to
// c.Request.Context().Done(), it preserves detached non-stream draining when a
// client disconnects mid-flight.
func OaiResponsesBufferedStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	if info != nil {
		info.StreamStatus = relaycommon.NewStreamStatus()
		info.StreamStatus.RequireTerminal()
	}
	accumulator := relayconvert.NewResponsesBufferedAccumulator()
	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	var finalResponse *dto.OpenAIResponsesResponse
	var streamErr *types.NewAPIError
	var terminalRaw []byte
	var terminalEventType string
	receivedValidEvent := false
	countedItemTools := false

	scanner := helper.NewStreamScanner(resp.Body)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		line := scanner.Text()
		data, hasDataPrefix := strings.CutPrefix(line, "data:")
		data = strings.TrimSpace(data)
		if !hasDataPrefix && data != "[DONE]" {
			continue
		}
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			if info != nil && info.StreamStatus != nil {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
			}
			break
		}

		raw := common.StringToByteSlice(data)
		if oaiErr, statusCode := extractEmbeddedOpenAIErrorRaw(raw); oaiErr != nil {
			if statusCode < 400 || statusCode > 599 {
				statusCode = responsesErrorTypeHTTPStatus(oaiErr.Type)
			}
			streamErr = types.WithOpenAIError(*oaiErr, statusCode)
			break
		}

		var streamResp dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResp); err != nil {
			logger.LogError(c, common.LogText("failed to unmarshal buffered responses stream event: %s", err.Error()))
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			break
		}
		receivedValidEvent = true
		if info != nil {
			info.SetFirstResponseTime()
			info.ReceivedResponseCount++
		}
		if streamResp.Response != nil && info != nil {
			info.ObserveResponseModel(streamResp.Response.Model)
		}
		if info != nil {
			service.ObserveResponsesOutcome(info, &streamResp)
		}
		accumulator.ProcessEvent(&streamResp)
		switch streamResp.Type {
		case dto.ResponsesOutputTypeItemDone:
			if streamResp.Item != nil {
				switch streamResp.Item.Type {
				case dto.BuildInCallWebSearchCall, dto.BuildInCallFileSearchCall, dto.BuildInCallFunctionCall:
					if info != nil {
						info.CountBillableToolCall(streamResp.Item.Type, streamResp.Item.Name)
					}
					countedItemTools = true
				case dto.ResponsesOutputTypeImageGenerationCall:
					imageCounter.Observe(streamResp.Item, streamResp.OutputIndex)
				}
			}
		case "response.completed", "response.done", "response.incomplete", "response.cancelled", "response.canceled":
			terminalEventType = streamResp.Type
			terminalRaw = append([]byte(nil), data...)
			finalResponse = streamResp.Response
			if streamResp.Type == "response.incomplete" || streamResp.Type == "response.cancelled" || streamResp.Type == "response.canceled" {
				if finalResponse == nil {
					finalResponse = &dto.OpenAIResponsesResponse{
						ID:        helper.GetResponseID(c),
						Object:    "response",
						CreatedAt: dto.IntValue(time.Now().Unix()),
						Model:     info.GetUpstreamModelName(),
					}
				}
				if len(finalResponse.Status) == 0 {
					if streamResp.Type == "response.incomplete" {
						finalResponse.Status = []byte(`"incomplete"`)
					} else {
						finalResponse.Status = []byte(`"cancelled"`)
					}
				}
			}
		case "error", "response.failed", "response.error":
			streamErr = extractResponsesStreamError(&streamResp, raw)
		}
		if streamErr != nil || finalResponse != nil {
			if finalResponse != nil && info != nil && info.StreamStatus != nil {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
			}
			break
		}
	}
	if streamErr != nil {
		return nil, streamErr
	}
	if err := scanner.Err(); err != nil {
		if helper.ClientRequestCanceled(c) || errors.Is(err, context.Canceled) {
			return nil, types.NewError(err, types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry(), types.ErrOptionWithHideErrMsg("client closed request"))
		}
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	if !receivedValidEvent {
		return nil, types.NewOpenAIError(errors.New("empty upstream responses stream"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
	}

	synthesizedFinal := false
	if finalResponse == nil {
		synthesizedFinal = true
		finalResponse = &dto.OpenAIResponsesResponse{
			ID:        helper.GetResponseID(c),
			Object:    "response",
			CreatedAt: dto.IntValue(time.Now().Unix()),
			Model:     info.GetUpstreamModelName(),
			Status:    []byte(`"completed"`),
		}
	}
	hadOutput := len(finalResponse.Output) > 0
	accumulator.SupplementResponseOutput(finalResponse)
	if synthesizedFinal && len(finalResponse.Output) == 0 {
		return nil, types.NewOpenAIError(errors.New("empty upstream responses stream"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
	}

	usage := &dto.Usage{}
	service.ApplyResponsesUsage(usage, finalResponse.Usage)
	if usage.CompletionTokens == 0 || usage.PromptTokens == 0 {
		text := service.ExtractOutputTextFromResponses(finalResponse)
		if text != "" || usage.PromptTokens == 0 {
			fallback := service.ResponseText2Usage(c, text, info.GetUpstreamModelName(), info.GetEstimatePromptTokens())
			if usage.PromptTokens == 0 {
				usage.PromptTokens = fallback.PromptTokens
			}
			if usage.CompletionTokens == 0 {
				usage.CompletionTokens = fallback.CompletionTokens
			}
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			if usage.BillingUsage != nil {
				usage.BillingUsage = dto.CloneBillingUsageWithEstimatedCompletion(usage.BillingUsage, usage.CompletionTokens)
			}
		}
	}

	if synthesizedFinal && finalResponse.Usage == nil {
		finalResponse.Usage = relayconvert.UsageFromChatUsage(usage)
	}

	var responseBody []byte
	if !synthesizedFinal && len(terminalRaw) > 0 {
		if rawResp := gjson.GetBytes(terminalRaw, "response"); rawResp.IsObject() {
			responseBody = []byte(rawResp.Raw)
			if terminalEventType == "response.incomplete" && !gjson.GetBytes(responseBody, "status").Exists() {
				if patched, err := sjson.SetBytes(responseBody, "status", "incomplete"); err == nil {
					responseBody = patched
				}
			} else if (terminalEventType == "response.cancelled" || terminalEventType == "response.canceled") && !gjson.GetBytes(responseBody, "status").Exists() {
				if patched, err := sjson.SetBytes(responseBody, "status", "cancelled"); err == nil {
					responseBody = patched
				}
			}
			if !hadOutput && len(finalResponse.Output) > 0 {
				if outputJSON, err := common.Marshal(finalResponse.Output); err == nil {
					if patched, err := sjson.SetRawBytes(responseBody, "output", outputJSON); err == nil {
						responseBody = patched
					} else {
						responseBody = nil
					}
				} else {
					responseBody = nil
				}
			}
		}
	}
	if len(responseBody) == 0 {
		var err error
		responseBody, err = common.Marshal(finalResponse)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
		}
	}

	responseBody = rewriteSGLangResponsesCreatedAt(info, responseBody, "created_at", finalResponse.CreatedAt)
	responseBody = ensureResponsesUsageReasoningDetails(responseBody, "usage")

	if !countedItemTools && info != nil {
		for _, output := range finalResponse.Output {
			switch output.Type {
			case dto.BuildInCallWebSearchCall:
				info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
			case dto.BuildInCallFileSearchCall:
				info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
			case dto.BuildInCallFunctionCall:
				info.CountBillableToolCall(dto.BuildInCallFunctionCall, output.Name)
			}
		}
	}
	if info != nil {
		if len(terminalRaw) > 0 {
			info.ApplyVendorToolUsage(terminalRaw)
		} else {
			info.ApplyVendorToolUsage(responseBody)
		}
		for i := range finalResponse.Output {
			imageCounter.Observe(&finalResponse.Output[i], &i)
		}
		imageCounter.Commit(info)
	}

	if !helper.ClientRequestCanceled(c) {
		jsonResp := *resp
		if resp.Header != nil {
			jsonResp.Header = resp.Header.Clone()
		} else {
			jsonResp.Header = make(http.Header)
		}
		jsonResp.Header.Set("Content-Type", "application/json")
		jsonResp.Header.Del("Transfer-Encoding")
		jsonResp.Header.Del("Content-Length")
		service.IOCopyBytesGracefully(c, &jsonResp, responseBody)
	}

	return usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, common.LogText("invalid response or response body"))
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	accumulator := service.NewResponsesUsageAccumulator(info)
	var lastParseErr error
	var streamErr *types.NewAPIError

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		raw := common.StringToByteSlice(data)
		if oaiErr, statusCode := extractEmbeddedOpenAIErrorRaw(raw); oaiErr != nil {
			if statusCode < 400 || statusCode > 599 {
				statusCode = responsesErrorTypeHTTPStatus(oaiErr.Type)
			}
			errObj := types.WithOpenAIError(*oaiErr, statusCode)
			if info != nil && info.StreamStatus != nil {
				code := ""
				if oaiErr.Code != nil {
					code = fmt.Sprint(oaiErr.Code)
				}
				info.StreamStatus.MarkFailed(code, oaiErr.Type, errObj.StatusCode)
			}
			if !helper.HasWrittenStreamPayload(c) && (info == nil || info.SendResponseCount == 0) {
				streamErr = errObj
				sr.Stop(streamErr)
				return
			}
			logger.LogError(c, common.LogText("upstream responses stream returned embedded error after payload written: %s", errObj.Error()))
			sr.Error(errObj)
			sr.Stop(nil)
			return
		}

		// 检查当前数据是否包含 completed 状态和 usage 信息
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, common.LogText("failed to unmarshal stream response: %s", err.Error()))
			lastParseErr = err
			sr.Error(err)
			return
		}
		if (streamResponse.Type == "error" || streamResponse.Type == "response.failed" || streamResponse.Type == "response.error") &&
			!helper.HasWrittenStreamPayload(c) && (info == nil || info.SendResponseCount == 0) {
			streamErr = extractResponsesStreamError(&streamResponse, raw)
			if info != nil && info.StreamStatus != nil {
				oaiErr := streamErr.ToOpenAIError()
				code := ""
				if oaiErr.Code != nil {
					code = fmt.Sprint(oaiErr.Code)
				}
				info.StreamStatus.MarkFailed(code, oaiErr.Type, streamErr.StatusCode)
			}
			sr.Stop(streamErr)
			return
		}
		if streamResponse.Response != nil {
			data = string(rewriteSGLangResponsesCreatedAt(info, []byte(data), "response.created_at", streamResponse.Response.CreatedAt))
			data = string(ensureResponsesUsageReasoningDetails([]byte(data), "response.usage"))
		}
		accumulator.Observe(&streamResponse, common.StringToByteSlice(data))
		if err := helper.ResponseChunkData(c, streamResponse, data); err != nil {
			if info != nil && info.StreamStatus != nil {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
			}
			sr.Stop(err)
			return
		}
		if info != nil {
			info.SendResponseCount++
		}
		if (streamResponse.Type == "response.completed" || streamResponse.Type == "response.done") &&
			streamResponse.Response != nil && info.StreamStatus.ResponseOutcome() == string(relaycommon.ResponseOutcomeCompleted) {
			sr.Done()
		}
	})

	common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, info.StreamStatus)
	info.StreamStatus.RequireTerminal()
	if streamErr != nil && !helper.HasWrittenStreamPayload(c) && (info == nil || info.SendResponseCount == 0) {
		return nil, streamErr
	}
	if !helper.HasWrittenStreamPayload(c) && (info == nil || info.SendResponseCount == 0) {
		if helper.ClientRequestCanceled(c) || (info != nil && info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone) {
			var cancelErr error
			if info != nil && info.StreamStatus != nil && info.StreamStatus.EndError != nil {
				cancelErr = info.StreamStatus.EndError
			} else if c != nil && c.Request != nil && c.Request.Context().Err() != nil {
				cancelErr = c.Request.Context().Err()
			} else {
				cancelErr = fmt.Errorf("client closed request")
			}
			return nil, types.NewError(cancelErr, types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry())
		}
		if info != nil && info.StreamStatus != nil && info.StreamStatus.EndError != nil {
			return nil, types.NewOpenAIError(info.StreamStatus.EndError, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if lastParseErr != nil {
			return nil, types.NewOpenAIError(lastParseErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		return nil, types.NewOpenAIError(fmt.Errorf("empty upstream stream response"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
	}
	return accumulator.Finish(), nil
}

func responsesErrorTypeHTTPStatus(errType string) int {
	switch errType {
	case "invalid_request_error":
		return http.StatusBadRequest
	case "rate_limit_error":
		return http.StatusTooManyRequests
	case "authentication_error":
		return http.StatusUnauthorized
	default:
		return http.StatusBadGateway
	}
}

func extractResponsesStreamError(event *dto.ResponsesStreamResponse, raw []byte) *types.NewAPIError {
	var envelope struct {
		Status     any `json:"status"`
		StatusCode any `json:"status_code"`
		Code       any `json:"code"`
	}
	_ = common.Unmarshal(raw, &envelope)

	var oaiErr types.OpenAIError
	var statusCode int
	switch {
	case event != nil && event.Response != nil && event.Response.GetOpenAIError() != nil:
		oaiErr = *event.Response.GetOpenAIError()
		statusCode = parseEmbeddedHTTPStatus(oaiErr.Code, envelope.StatusCode, envelope.Status, envelope.Code)
	default:
		if embeddedErr, embeddedStatus := extractEmbeddedOpenAIErrorRaw(raw); embeddedErr != nil {
			oaiErr = *embeddedErr
			statusCode = embeddedStatus
		} else if event != nil {
			oaiErr = types.OpenAIError{
				Message: event.Message,
				Type:    "server_error",
				Param:   event.Param,
				Code:    event.Code,
			}
			statusCode = parseEmbeddedHTTPStatus(event.Code, envelope.StatusCode, envelope.Status, envelope.Code)
		}
	}
	if strings.TrimSpace(oaiErr.Message) == "" {
		oaiErr.Message = "upstream responses stream failed"
	}
	if statusCode < 400 || statusCode > 599 {
		statusCode = responsesErrorTypeHTTPStatus(oaiErr.Type)
	}
	return types.WithOpenAIError(oaiErr, statusCode)
}

// ensureResponsesUsageReasoningDetails copies a top-level usage.reasoning_tokens
// value into usage.output_tokens_details without re-serializing the body.
func ensureResponsesUsageReasoningDetails(payload []byte, usagePath string) []byte {
	usage := gjson.GetBytes(payload, usagePath)
	if !usage.Exists() {
		return payload
	}
	rt := usage.Get("reasoning_tokens").Int()
	if rt == 0 {
		rt = usage.Get("output_tokens_details.reasoning_tokens").Int()
	}
	if rt == 0 {
		return payload
	}
	detailsPath := usagePath + ".output_tokens_details.reasoning_tokens"
	if gjson.GetBytes(payload, detailsPath).Int() != 0 {
		return payload
	}
	patched, err := sjson.SetBytes(payload, detailsPath, rt)
	if err != nil {
		return payload
	}
	return patched
}

func rewriteSGLangResponsesCreatedAt(info *relaycommon.RelayInfo, payload []byte, path string, createdAt dto.IntValue) []byte {
	if info.GetChannelType() != constant.ChannelTypeSGLang {
		return payload
	}
	if !gjson.GetBytes(payload, path).Exists() {
		return payload
	}
	patched, err := sjson.SetBytes(payload, path, int(createdAt))
	if err != nil {
		return payload
	}
	return patched
}
