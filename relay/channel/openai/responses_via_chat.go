package openai

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func OaiChatToResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	body, apiErr := helper.ReadResponseBody(c, resp, types.ErrorCodeReadResponseBodyFailed)
	if apiErr != nil {
		return nil, apiErr
	}

	if oaiErr, statusCode := ExtractEmbeddedOpenAIError(body, upstreamErrorFallbackStatus(resp)); oaiErr != nil {
		return nil, types.WithOpenAIError(*oaiErr, statusCode)
	}

	var chatResp dto.OpenAITextResponse
	if err := common.Unmarshal(body, &chatResp); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	info.ObserveResponseModel(chatResp.Model)
	if responseID := helper.GetResponseID(c); responseID != "" {
		chatResp.Id = responseID
	}
	convertResult, err := service.ConvertResponse(c, info, types.RelayFormatOpenAIResponses, &chatResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	responsesResp, ok := convertResult.Value.(*dto.OpenAIResponsesResponse)
	if !ok {
		return nil, types.NewOpenAIError(fmt.Errorf("expected OpenAI responses response, got %T", convertResult.Value), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	for _, choice := range chatResp.Choices {
		for _, tc := range choice.Message.ParseToolCalls() {
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, tc.Function.Name)
		}
	}
	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	for i := range responsesResp.Output {
		switch responsesResp.Output[i].Type {
		case dto.BuildInCallWebSearchCall:
			info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
		case dto.BuildInCallFileSearchCall:
			info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
		case dto.ResponsesOutputTypeImageGenerationCall:
			imageCounter.Observe(&responsesResp.Output[i], &i)
		}
	}
	info.ApplyVendorToolUsage(body)
	imageCounter.Commit(info)

	usage := convertResult.Usage
	if usage == nil || usage.TotalTokens == 0 {
		text := service.ExtractOutputTextFromResponses(responsesResp)
		usage = service.ResponseText2Usage(c, text, info.UpstreamModelName, info.GetEstimatePromptTokens())
		responsesResp.Usage = relayconvert.UsageFromChatUsage(usage)
	}

	responseBody, err := common.Marshal(responsesResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)
	return usage, nil
}

func OaiChatToResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	responseID := helper.GetResponseID(c)
	state, err := relayconvert.NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, relayconvert.ResponseStreamOptions{
		ID:                 responseID,
		Model:              info.UpstreamModelName,
		EmitSequenceNumber: true,
	})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	streamErr := (*types.NewAPIError)(nil)
	// upstreamFailure is the upstream or conversion failure the client was
	// told about with response.failed.
	var upstreamFailure *types.NewAPIError
	wroteToClient := false
	clientGone := false
	seenStreamToolCalls := make(map[string]struct{})
	var streamFunctionCallNames []string
	var lastStreamData string
	var secondLastStreamData string
	var vendorUsageFrame string
	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	toolsCommitted := false

	hasWritten := func() bool {
		return wroteToClient || (info != nil && info.SendResponseCount > 0) || helper.HasWrittenStreamPayload(c)
	}

	clientCancelError := func() error {
		if info != nil && info.StreamStatus != nil && info.StreamStatus.EndError != nil {
			return info.StreamStatus.EndError
		}
		if c != nil && c.Request != nil && c.Request.Context().Err() != nil {
			return c.Request.Context().Err()
		}
		return errors.New("client closed request")
	}

	observeConvertedEvent := func(event relayconvert.ChatToResponsesStreamEvent) {
		switch event.Type {
		case dto.ResponsesOutputTypeItemDone:
			if event.Payload.Item == nil {
				return
			}
			switch event.Payload.Item.Type {
			case dto.BuildInCallWebSearchCall, dto.BuildInCallFileSearchCall:
				info.CountBillableToolCall(event.Payload.Item.Type, event.Payload.Item.Name)
			case dto.ResponsesOutputTypeImageGenerationCall:
				imageCounter.Observe(event.Payload.Item, event.Payload.OutputIndex)
			}
		case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
			if event.Payload.Response != nil {
				for i := range event.Payload.Response.Output {
					imageCounter.Observe(&event.Payload.Response.Output[i], &i)
				}
			}
		}
	}

	commitStreamTools := func() {
		if toolsCommitted {
			return
		}
		toolsCommitted = true
		for _, name := range streamFunctionCallNames {
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, name)
		}
		rawUsage := vendorUsageFrame
		if rawUsage == "" {
			rawUsage = lastStreamData
		}
		if rawUsage == "" {
			rawUsage = secondLastStreamData
		}
		if rawUsage != "" {
			info.ApplyVendorToolUsage(common.StringToByteSlice(rawUsage))
		}
		imageCounter.Commit(info)
	}

	sendEvent := func(event relayconvert.ChatToResponsesStreamEvent) bool {
		data, err := common.Marshal(event.Payload)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
			return false
		}
		if err := helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: event.Type}, string(data)); err != nil {
			clientGone = true
			if info != nil && info.StreamStatus != nil {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
			}
			logger.LogWarn(c, common.LogText("chat-to-responses client disconnected during stream: %v", err))
			return false
		}
		wroteToClient = true
		if info != nil {
			info.SendResponseCount++
		}
		return true
	}
	failResponsesStream := func(err error) bool {
		if !hasWritten() {
			return false
		}
		failureResults, handled := state.FailResponsesStream("server_error", err.Error(), "")
		if !handled {
			return false
		}
		for _, result := range failureResults {
			event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
			if !ok {
				streamErr = types.NewOpenAIError(fmt.Errorf("expected OAI responses stream event, got %T", result.Value), types.ErrorCodeBadResponse, http.StatusInternalServerError)
				return true
			}
			if !sendEvent(event) {
				return true
			}
		}
		return true
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if streamErr != nil {
			stopStream(sr, streamErr)
			return
		}

		if oaiError, statusCode := ExtractEmbeddedOpenAIError(common.StringToByteSlice(data), upstreamErrorFallbackStatus(resp)); oaiError != nil {
			errMsg := strings.TrimSpace(oaiError.Message)
			if errMsg == "" {
				errMsg = fmt.Sprintf("upstream error: %v", oaiError.Code)
			}
			if failResponsesStream(fmt.Errorf("%s", errMsg)) {
				// The client got response.failed, so the request is never
				// retried; the recorded error keeps the stream failed.
				upstreamFailure = types.WithOpenAIError(*oaiError, statusCode, types.ErrOptionWithSkipRetry())
				sr.Stop(upstreamFailure)
				return
			}
			streamErr = types.WithOpenAIError(*oaiError, statusCode)
			stopStream(sr, streamErr)
			return
		}

		var chunk dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &chunk); err != nil {
			logger.LogError(c, common.LogText("failed to unmarshal chat stream response: %s", err.Error()))
			if failResponsesStream(err) {
				upstreamFailure = types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
				sr.Stop(upstreamFailure)
				return
			}
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			stopStream(sr, streamErr)
			return
		}

		if lastStreamData != "" {
			secondLastStreamData = lastStreamData
		}
		lastStreamData = data
		if chunk.Usage != nil {
			vendorUsageFrame = data
		}
		info.ObserveResponseModel(chunk.Model)
		observeStreamChoices(info, data, seenStreamToolCalls, &streamFunctionCallNames)
		results, err := service.ConvertStreamResponseChunk(c, info, state, &chunk)
		if err != nil {
			if failResponsesStream(err) {
				upstreamFailure = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
				sr.Stop(upstreamFailure)
				return
			}
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			stopStream(sr, streamErr)
			return
		}
		for _, result := range results {
			event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
			if !ok {
				streamErr = types.NewOpenAIError(fmt.Errorf("expected OAI responses stream event, got %T", result.Value), types.ErrorCodeBadResponse, http.StatusInternalServerError)
				stopStream(sr, streamErr)
				return
			}
			observeConvertedEvent(event)
			if !sendEvent(event) {
				stopStream(sr, streamErr)
				return
			}
		}
	})

	isClientGone := clientGone || (info != nil && info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone)

	resolveDeliveredUsage := func() *dto.Usage {
		usage := state.Usage()
		if usage == nil || usage.TotalTokens == 0 {
			usage = service.ResponseText2Usage(c, state.UsageText(), info.UpstreamModelName, info.GetEstimatePromptTokens())
			state.SetUsage(usage)
		}
		return usage
	}

	if streamErr != nil {
		if hasWritten() {
			usage := resolveDeliveredUsage()
			commitStreamTools()
			return usage, nil
		}
		return nil, streamErr
	}
	if info != nil && info.StreamStatus != nil && !info.StreamStatus.IsNormalEnd() {
		if info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone {
			if !hasWritten() {
				return nil, types.NewError(clientCancelError(), types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry())
			}
			usage := resolveDeliveredUsage()
			commitStreamTools()
			return usage, nil
		}
		err := fmt.Errorf("chat-to-responses stream ended unexpectedly: %s", info.StreamStatus.Summary())
		if !hasWritten() {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusBadGateway)
		}
		info.StreamStatus.MarkFailed(string(types.ErrorCodeBadResponse), "server_error", http.StatusBadGateway)
		failResponsesStream(err)
		usage := resolveDeliveredUsage()
		commitStreamTools()
		return usage, nil
	}
	if !hasWritten() {
		if isClientGone || helper.ClientRequestCanceled(c) {
			return nil, types.NewError(clientCancelError(), types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry())
		}
		if lastStreamData == "" {
			return nil, types.NewOpenAIError(fmt.Errorf("empty upstream stream response"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
		}
	}

	usage := state.Usage()
	noUpstreamUsage := usage == nil || usage.TotalTokens == 0
	if upstreamFailure != nil {
		if noUpstreamUsage && state.UsageText() == "" {
			// The stream failed before any output reached the client, which
			// already received response.failed: the request is not charged.
			return nil, upstreamFailure
		}
		if noUpstreamUsage {
			usage = service.ResponseText2Usage(c, state.UsageText(), info.UpstreamModelName, info.GetEstimatePromptTokens())
			state.SetUsage(usage)
		}
		commitStreamTools()
		return usage, nil
	}
	if noUpstreamUsage {
		usage = service.ResponseText2Usage(c, state.UsageText(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		state.SetUsage(usage)
	}
	if isClientGone {
		commitStreamTools()
		return usage, nil
	}

	finalResults, err := service.FinalizeStreamResponse(c, info, state)
	if err != nil {
		if failResponsesStream(err) {
			commitStreamTools()
			return usage, nil
		}
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	for _, result := range finalResults {
		event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
		if !ok {
			if hasWritten() {
				commitStreamTools()
				return usage, nil
			}
			return nil, types.NewOpenAIError(fmt.Errorf("expected OAI responses stream event, got %T", result.Value), types.ErrorCodeBadResponse, http.StatusInternalServerError)
		}
		observeConvertedEvent(event)
		if !sendEvent(event) {
			if hasWritten() {
				commitStreamTools()
				return usage, nil
			}
			if clientGone {
				return nil, types.NewError(clientCancelError(), types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry())
			}
			if streamErr != nil {
				return nil, streamErr
			}
			commitStreamTools()
			return usage, nil
		}
	}

	commitStreamTools()
	return usage, nil
}

// stopStream ends a converted stream from the scanner callback. A nil
// streamErr (the stream stops because the client went away) must reach
// StreamResult.Stop as an untyped nil; a typed nil *NewAPIError is a non-nil
// error that StreamResult would record as a failure and dereference.
func stopStream(sr *helper.StreamResult, streamErr *types.NewAPIError) {
	if streamErr == nil {
		sr.Stop(nil)
		return
	}
	sr.Stop(streamErr)
}
