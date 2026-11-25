package openai

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, apiErr := helper.ReadResponseBody(c, resp, types.ErrorCodeReadResponseBodyFailed)
	if apiErr != nil {
		return nil, apiErr
	}
	err := common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	info.ObserveResponseModel(responsesResponse.Model)
	responseBody = rewriteSGLangResponsesCreatedAt(info, responseBody, "created_at", responsesResponse.CreatedAt)
	responseBody = ensureResponsesUsageReasoningDetails(responseBody, "usage")

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

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

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, common.LogText("invalid response or response body"))
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	accumulator := service.NewResponsesUsageAccumulator(info)
	receivedValidEvent := false
	var lastParseErr error
	var streamErr *types.NewAPIError

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {

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
			streamErr = extractResponsesStreamError(&streamResponse, common.StringToByteSlice(data))
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
		receivedValidEvent = true
		if streamResponse.Response != nil {
			data = string(rewriteSGLangResponsesCreatedAt(info, []byte(data), "response.created_at", streamResponse.Response.CreatedAt))
			data = string(ensureResponsesUsageReasoningDetails([]byte(data), "response.usage"))
		}
		accumulator.Observe(&streamResponse, common.StringToByteSlice(data))
		if err := helper.ResponseChunkData(c, streamResponse, data); err != nil {
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
	if !receivedValidEvent && !helper.HasWrittenStreamPayload(c) && (info == nil || info.SendResponseCount == 0) {
		if helper.ClientRequestCanceled(c) {
			return nil, types.NewError(c.Request.Context().Err(), types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry())
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

func extractResponsesStreamError(event *dto.ResponsesStreamResponse, raw []byte) *types.NewAPIError {
	var envelope struct {
		Status int                `json:"status"`
		Error  *types.OpenAIError `json:"error"`
	}
	_ = common.Unmarshal(raw, &envelope)

	var oaiErr types.OpenAIError
	switch {
	case event != nil && event.Response != nil && event.Response.GetOpenAIError() != nil:
		oaiErr = *event.Response.GetOpenAIError()
	case envelope.Error != nil && (strings.TrimSpace(envelope.Error.Message) != "" || strings.TrimSpace(envelope.Error.Type) != "" || envelope.Error.Code != nil):
		oaiErr = *envelope.Error
	case event != nil:
		oaiErr = types.OpenAIError{
			Message: event.Message,
			Type:    "server_error",
			Param:   event.Param,
			Code:    event.Code,
		}
	}
	if strings.TrimSpace(oaiErr.Message) == "" {
		oaiErr.Message = "upstream responses stream failed"
	}
	statusCode := envelope.Status
	if statusCode < 400 || statusCode > 599 {
		switch oaiErr.Type {
		case "invalid_request_error":
			statusCode = http.StatusBadRequest
		case "rate_limit_error":
			statusCode = http.StatusTooManyRequests
		case "authentication_error":
			statusCode = http.StatusUnauthorized
		default:
			statusCode = http.StatusBadGateway
		}
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
