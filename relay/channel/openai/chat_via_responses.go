package openai

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

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

func countResponsesOutputTools(info *relaycommon.RelayInfo, resp *dto.OpenAIResponsesResponse, rawBody []byte) {
	if info == nil || resp == nil {
		return
	}
	for _, output := range resp.Output {
		switch output.Type {
		case dto.BuildInCallWebSearchCall:
			info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
		case dto.BuildInCallFileSearchCall:
			info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
		case dto.BuildInCallFunctionCall:
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, output.Name)
		}
	}
	info.ApplyVendorToolUsage(rawBody)

	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	for i := range resp.Output {
		imageCounter.Observe(&resp.Output[i], &i)
	}
	imageCounter.Commit(info)
}

func OaiResponsesToChatHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	defer service.CloseResponseBodyGracefully(resp)

	var responsesResp dto.OpenAIResponsesResponse
	body, apiErr := helper.ReadResponseBody(c, resp, types.ErrorCodeReadResponseBodyFailed)
	if apiErr != nil {
		return nil, apiErr
	}

	if err := common.Unmarshal(body, &responsesResp); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if oaiError := responsesResp.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	info.ObserveResponseModel(responsesResp.Model)
	countResponsesOutputTools(info, &responsesResp, body)
	responseValue, usage, err := convertResponsesResponseForClient(c, info, &responsesResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	responseBody, err := common.Marshal(responseValue)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)
	return usage, nil
}

func OaiResponsesToChatBufferedStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.RequireTerminal()
	accumulator := relayconvert.NewResponsesBufferedAccumulator()
	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	var finalResponse *dto.OpenAIResponsesResponse
	var streamErr *types.NewAPIError
	var terminalRaw []byte
	receivedValidEvent := false
	countedItemTools := false

	scanner := helper.NewStreamScanner(resp.Body)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 6 || line[:5] != "data:" {
			continue
		}
		data := line[5:]
		data = strings.TrimSpace(data)
		if data == "" || data == "[DONE]" {
			if data == "[DONE]" {
				break
			}
			continue
		}

		var streamResp dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResp); err != nil {
			logger.LogError(c, common.LogText("failed to unmarshal buffered responses stream event: %s", err.Error()))
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			break
		}
		receivedValidEvent = true
		if streamResp.Response != nil {
			info.ObserveResponseModel(streamResp.Response.Model)
		}
		service.ObserveResponsesOutcome(info, &streamResp)
		accumulator.ProcessEvent(&streamResp)
		switch streamResp.Type {
		case dto.ResponsesOutputTypeItemDone:
			if streamResp.Item != nil {
				switch streamResp.Item.Type {
				case dto.BuildInCallWebSearchCall, dto.BuildInCallFileSearchCall, dto.BuildInCallFunctionCall:
					info.CountBillableToolCall(streamResp.Item.Type, streamResp.Item.Name)
					countedItemTools = true
				case dto.ResponsesOutputTypeImageGenerationCall:
					imageCounter.Observe(streamResp.Item, streamResp.OutputIndex)
				}
			}
		case "response.completed", "response.done", "response.incomplete":
			terminalRaw = append([]byte(nil), data...)
			finalResponse = streamResp.Response
			if streamResp.Type == "response.incomplete" {
				if finalResponse == nil {
					finalResponse = &dto.OpenAIResponsesResponse{}
				}
				if len(finalResponse.Status) == 0 {
					finalResponse.Status = []byte(`"incomplete"`)
				}
			}
		case "response.failed", "response.error":
			if streamResp.Response != nil {
				if oaiErr := streamResp.Response.GetOpenAIError(); oaiErr != nil && oaiErr.Type != "" {
					streamErr = types.WithOpenAIError(*oaiErr, http.StatusInternalServerError)
					break
				}
			}
			streamErr = types.NewOpenAIError(fmt.Errorf("responses stream error: %s", streamResp.Type), types.ErrorCodeBadResponse, http.StatusInternalServerError)
		}
		if streamErr != nil || finalResponse != nil {
			break
		}
	}
	if streamErr != nil {
		return nil, streamErr
	}
	if err := scanner.Err(); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	if !receivedValidEvent {
		return nil, types.NewOpenAIError(errors.New("empty upstream responses stream"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
	}
	synthesizedFinal := false
	if finalResponse == nil {
		synthesizedFinal = true
		finalResponse = &dto.OpenAIResponsesResponse{
			ID:        helper.GetResponseID(c),
			CreatedAt: dto.IntValue(time.Now().Unix()),
			Model:     info.UpstreamModelName,
			Status:    []byte(`"completed"`),
		}
	}
	accumulator.SupplementResponseOutput(finalResponse)
	if synthesizedFinal && len(finalResponse.Output) == 0 {
		return nil, types.NewOpenAIError(errors.New("empty upstream responses stream"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
	}

	if !countedItemTools {
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
	if len(terminalRaw) > 0 {
		info.ApplyVendorToolUsage(terminalRaw)
	}
	for i := range finalResponse.Output {
		imageCounter.Observe(&finalResponse.Output[i], &i)
	}
	imageCounter.Commit(info)

	responseValue, usage, err := convertResponsesResponseForClient(c, info, finalResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	responseBody, err := common.Marshal(responseValue)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)
	return usage, nil
}

func convertResponsesResponseForClient(c *gin.Context, info *relaycommon.RelayInfo, response *dto.OpenAIResponsesResponse) (any, *dto.Usage, error) {
	if responseID := helper.GetResponseID(c); responseID != "" {
		response.ID = responseID
	}

	usage := relayconvert.UsageFromResponsesUsage(response.Usage)
	if usage == nil || usage.TotalTokens == 0 {
		text := service.ExtractOutputTextFromResponses(response)
		usage = service.ResponseText2Usage(c, text, info.UpstreamModelName, info.GetEstimatePromptTokens())
		response.Usage = relayconvert.UsageFromChatUsage(usage)
	}

	result, err := service.ConvertResponse(c, info, info.RelayFormat, response)
	if err != nil {
		return nil, nil, err
	}
	if result.Usage != nil && result.Usage.TotalTokens != 0 {
		usage = result.Usage
	}
	return result.Value, usage, nil
}

func OaiResponsesToChatStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	defer service.CloseResponseBodyGracefully(resp)

	responseId := helper.GetResponseID(c)
	createAt := time.Now().Unix()
	state, err := relayconvert.NewResponseStreamState(types.RelayFormatOpenAIResponses, info.RelayFormat, relayconvert.ResponseStreamOptions{
		ID:      responseId,
		Model:   info.UpstreamModelName,
		Created: createAt,
	})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	streamErr := (*types.NewAPIError)(nil)
	// failedUsage records the usage reported by a response.failed / response.error event, if any.
	var failedUsage *dto.Usage
	wroteToClient := false
	clientGone := false
	receivedEvent := false
	countedItemTools := false
	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	imageCommitted := false

	if info.RelayFormat == types.RelayFormatClaude && info.ClaudeConvertInfo == nil {
		info.ClaudeConvertInfo = &relaycommon.ClaudeConvertInfo{LastMessagesType: relaycommon.LastMessageTypeNone}
	}

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

	// writeFailed stops the stream after any failed client write (including
	// non-context errors such as i/o timeout). Downstream write errors must not
	// become streamErr so already-delivered output settles normally.
	writeFailed := func(err error) bool {
		clientGone = true
		if info != nil && info.StreamStatus != nil {
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
		}
		logger.LogWarn(c, common.LogText("responses-to-chat client disconnected during stream: %v", err))
		return false
	}

	markWritten := func() {
		wroteToClient = true
		if info != nil {
			info.SendResponseCount++
		}
	}

	sendGeminiResponse := func(geminiResponse *dto.GeminiChatResponse) bool {
		if geminiResponse == nil {
			return true
		}
		geminiResponseStr, err := common.Marshal(geminiResponse)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
			return false
		}
		c.Render(-1, common.CustomEvent{Data: "data: " + string(geminiResponseStr)})
		if err := helper.FlushWriter(c); err != nil {
			return writeFailed(err)
		}
		helper.MarkStreamPayloadWritten(c)
		markWritten()
		return true
	}

	sendStreamResult := func(result relayconvert.ResponseResult) bool {
		switch value := result.Value.(type) {
		case dto.ChatCompletionsStreamResponse:
			if len(value.Choices) == 0 && value.Usage == nil {
				return true
			}
			if err := helper.ObjectData(c, &value); err != nil {
				return writeFailed(err)
			}
			markWritten()
			return true
		case *dto.ChatCompletionsStreamResponse:
			if value == nil || (len(value.Choices) == 0 && value.Usage == nil) {
				return true
			}
			if err := helper.ObjectData(c, value); err != nil {
				return writeFailed(err)
			}
			markWritten()
			return true
		case dto.ClaudeResponse:
			if err := helper.ClaudeData(c, value); err != nil {
				return writeFailed(err)
			}
			if value.Type != "ping" {
				markWritten()
			}
			return true
		case *dto.ClaudeResponse:
			if value == nil {
				return true
			}
			if err := helper.ClaudeData(c, *value); err != nil {
				return writeFailed(err)
			}
			if value.Type != "ping" {
				markWritten()
			}
			return true
		case dto.GeminiChatResponse:
			return sendGeminiResponse(&value)
		case *dto.GeminiChatResponse:
			return sendGeminiResponse(value)
		default:
			streamErr = types.NewOpenAIError(fmt.Errorf("unsupported converted stream response type %T", result.Value), types.ErrorCodeBadResponse, http.StatusInternalServerError)
			return false
		}
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if streamErr != nil {
			stopStream(sr, streamErr)
			return
		}

		var streamResp dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResp); err != nil {
			logger.LogError(c, common.LogText("failed to unmarshal responses stream event: %s", err.Error()))
			sr.Error(err)
			return
		}

		receivedEvent = true
		if streamResp.Response != nil {
			info.ObserveResponseModel(streamResp.Response.Model)
		}
		service.ObserveResponsesOutcome(info, &streamResp)
		switch streamResp.Type {
		case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
			if streamResp.Response != nil && !countedItemTools {
				for _, output := range streamResp.Response.Output {
					switch output.Type {
					case dto.BuildInCallWebSearchCall:
						info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
					case dto.BuildInCallFileSearchCall:
						info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
					case dto.BuildInCallFunctionCall:
						info.CountBillableToolCall(dto.BuildInCallFunctionCall, output.Name)
					}
				}
				countedItemTools = true
			}
			info.ApplyVendorToolUsage(common.StringToByteSlice(data))
			if !imageCommitted {
				if streamResp.Response != nil {
					for i := range streamResp.Response.Output {
						imageCounter.Observe(&streamResp.Response.Output[i], &i)
					}
				}
				imageCounter.Commit(info)
				imageCommitted = true
			}
		case dto.ResponsesOutputTypeItemDone:
			if streamResp.Item != nil {
				switch streamResp.Item.Type {
				case dto.BuildInCallWebSearchCall, dto.BuildInCallFileSearchCall, dto.BuildInCallFunctionCall:
					info.CountBillableToolCall(streamResp.Item.Type, streamResp.Item.Name)
					countedItemTools = true
				case dto.ResponsesOutputTypeImageGenerationCall:
					if !imageCommitted {
						imageCounter.Observe(streamResp.Item, streamResp.OutputIndex)
					}
				}
			}
		}

		if streamResp.Type == "response.error" || streamResp.Type == "response.failed" {
			if streamResp.Response != nil {
				failedUsage = streamResp.Response.Usage
				if oaiErr := streamResp.Response.GetOpenAIError(); oaiErr != nil && oaiErr.Type != "" {
					streamErr = types.WithOpenAIError(*oaiErr, http.StatusInternalServerError)
					stopStream(sr, streamErr)
					return
				}
			}
			streamErr = types.NewOpenAIError(fmt.Errorf("responses stream error: %s", streamResp.Type), types.ErrorCodeBadResponse, http.StatusInternalServerError)
			stopStream(sr, streamErr)
			return
		}

		results, err := service.ConvertStreamResponseChunk(c, info, state, &streamResp)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			stopStream(sr, streamErr)
			return
		}
		for _, result := range results {
			if !sendStreamResult(result) {
				stopStream(sr, streamErr)
				return
			}
		}
	})

	info.StreamStatus.RequireTerminal()

	isClientGone := clientGone || (info != nil && info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone)

	hasBillableToolOrImage := func() bool {
		if imageCounter.Count() > 0 || countedItemTools {
			return true
		}
		if info == nil || info.ResponsesUsageInfo == nil {
			return false
		}
		for _, tool := range info.ResponsesUsageInfo.BuiltInTools {
			if tool != nil && tool.CallCount > 0 {
				return true
			}
		}
		return false
	}

	if streamErr != nil {
		hasDeliveredBillableOutput := state.UsageText() != "" ||
			failedUsage != nil ||
			service.ValidUsage(state.Usage()) ||
			hasBillableToolOrImage()
		if !hasWritten() || !hasDeliveredBillableOutput {
			if isClientGone && hasWritten() {
				if !imageCommitted {
					imageCounter.Commit(info)
					imageCommitted = true
				}
				usage := state.Usage()
				if usage == nil || usage.TotalTokens == 0 {
					usage = service.ResponseText2Usage(c, state.UsageText(), info.UpstreamModelName, info.GetEstimatePromptTokens())
					state.SetUsage(usage)
				}
				return usage, nil
			}
			return nil, streamErr
		}
		if !imageCommitted {
			imageCounter.Commit(info)
			imageCommitted = true
		}
		// The stream failed after billable output reached the client: bill the
		// delivered output (upstream usage when reported, otherwise the
		// estimate) and terminate the already-open SSE stream with a protocol
		// error frame instead of writing raw JSON via c.JSON.
		usage := relayconvert.UsageFromResponsesUsage(failedUsage)
		if usage == nil || usage.TotalTokens == 0 {
			usage = state.Usage()
		}
		if usage == nil || usage.TotalTokens == 0 {
			usage = service.ResponseText2Usage(c, state.UsageText(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		} else if usage.CompletionTokens == 0 || usage.PromptTokens == 0 {
			// As in the native Responses accumulator, reported usage that
			// omits the output or the prompt is completed from the estimate of
			// the delivered output and the prompt.
			estimate := service.ResponseText2Usage(c, state.UsageText(), info.UpstreamModelName, info.GetEstimatePromptTokens())
			if usage.CompletionTokens == 0 {
				usage.CompletionTokens = estimate.CompletionTokens
			}
			if usage.PromptTokens == 0 {
				usage.PromptTokens = estimate.PromptTokens
			}
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			if usage.BillingUsage != nil {
				usage.BillingUsage = dto.CloneBillingUsageWithEstimatedCompletion(usage.BillingUsage, usage.CompletionTokens)
			}
		}
		if info.StreamStatus != nil {
			info.StreamStatus.MarkFailed(string(streamErr.GetErrorCode()), string(streamErr.GetErrorType()), streamErr.StatusCode)
		}
		logger.LogError(c, common.LogText("responses stream failed after output was delivered, billing the delivered output: %s", streamErr.Error()))
		if !isClientGone {
			streamErr.SetMessage(common.MessageWithRequestId(streamErr.Error(), c.GetString(common.RequestIdKey)))
			upstreamURLs := common.UpstreamAddresses(c)
			if info.RelayFormat == types.RelayFormatClaude {
				_ = helper.ClaudeData(c, dto.ClaudeResponse{
					Type:  "error",
					Error: streamErr.ToClaudeError(upstreamURLs...),
				})
			} else {
				_ = helper.ObjectData(c, gin.H{"error": streamErr.ToOpenAIError(upstreamURLs...)})
				helper.Done(c)
			}
		}
		return usage, nil
	}

	if !hasWritten() {
		if isClientGone || helper.ClientRequestCanceled(c) {
			return nil, types.NewError(clientCancelError(), types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry())
		}
		if !receivedEvent {
			return nil, types.NewOpenAIError(errors.New("empty upstream responses stream"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
		}
	}

	if !imageCommitted {
		imageCounter.Commit(info)
		imageCommitted = true
	}

	usage := state.Usage()
	if usage == nil || usage.TotalTokens == 0 {
		usage = service.ResponseText2Usage(c, state.UsageText(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		state.SetUsage(usage)
	}
	if isClientGone {
		return usage, nil
	}

	if info.RelayFormat == types.RelayFormatClaude && info.ClaudeConvertInfo != nil {
		info.ClaudeConvertInfo.Usage = usage
	}
	finalResults, err := service.FinalizeStreamResponse(c, info, state)
	if err != nil {
		if hasWritten() {
			logger.LogError(c, common.LogText("responses-to-chat finalize stream failed after output was delivered, billing the delivered output: %s", err.Error()))
			if info != nil && info.StreamStatus != nil {
				info.StreamStatus.MarkFailed(string(types.ErrorCodeBadResponse), "server_error", http.StatusInternalServerError)
			}
			return usage, nil
		}
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	for _, result := range finalResults {
		if !sendStreamResult(result) {
			if hasWritten() {
				return usage, nil
			}
			if clientGone {
				return nil, types.NewError(clientCancelError(), types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry())
			}
			if streamErr != nil {
				return nil, streamErr
			}
			return usage, nil
		}
	}
	if info.RelayFormat == types.RelayFormatOpenAI && info.ShouldIncludeUsage && usage != nil {
		if err := helper.ObjectData(c, helper.GenerateFinalUsageResponse(responseId, createAt, info.UpstreamModelName, *usage)); err != nil {
			writeFailed(err)
			if hasWritten() {
				return usage, nil
			}
			return nil, types.NewError(clientCancelError(), types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry())
		}
	}

	if info.RelayFormat == types.RelayFormatOpenAI {
		helper.Done(c)
	}
	return usage, nil
}
