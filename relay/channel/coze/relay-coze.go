package coze

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
)

func convertCozeChatRequest(c *gin.Context, request dto.GeneralOpenAIRequest) *CozeChatRequest {
	var messages []CozeEnterMessage
	// 将 request的messages的role为user的content转换为CozeMessage
	for _, message := range request.Messages {
		if message.Role == "user" {
			messages = append(messages, CozeEnterMessage{
				Role:    "user",
				Content: message.Content,
				// TODO: support more content type
				ContentType: "text",
			})
		}
	}
	user := request.User
	if len(user) == 0 {
		user = json.RawMessage(helper.GetResponseID(c))
	}
	cozeRequest := &CozeChatRequest{
		BotId:              c.GetString("bot_id"),
		UserId:             user,
		AdditionalMessages: messages,
		Stream:             lo.FromPtrOr(request.Stream, false),
	}
	return cozeRequest
}

func cozeChatHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp != nil {
		defer service.CloseResponseBodyGracefully(resp)
	}
	responseBody, readErr := helper.ReadResponseBody(c, resp, types.ErrorCodeBadResponseBody)
	if readErr != nil {
		return nil, readErr
	}
	var err error
	// convert coze response to openai response
	var response dto.TextResponse
	var cozeResponse CozeChatDetailResponse
	response.Model = info.UpstreamModelName
	err = common.Unmarshal(responseBody, &cozeResponse)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if cozeResponse.Code != 0 {
		return nil, types.NewError(errors.New(cozeResponse.Msg), types.ErrorCodeBadResponseBody)
	}
	// 从上下文获取 usage
	var usage dto.Usage
	usage.PromptTokens = max(c.GetInt("coze_input_count"), 0)
	usage.CompletionTokens = max(c.GetInt("coze_output_count"), 0)
	usage.TotalTokens = max(c.GetInt("coze_token_count"), 0)
	if usage.TotalTokens <= 0 && (usage.PromptTokens > 0 || usage.CompletionTokens > 0) {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	response.Usage = usage
	response.Id = helper.GetResponseID(c)

	var responseContent json.RawMessage
	for _, data := range cozeResponse.Data {
		if data.Type == "answer" {
			responseContent = data.Content
			response.Created = data.CreatedAt
		}
	}
	if usage.PromptTokens <= 0 && usage.CompletionTokens <= 0 {
		if usage.TotalTokens > 0 {
			usage.PromptTokens = usage.TotalTokens
			response.Usage = usage
		} else if len(responseContent) > 0 {
			promptTokens := usage.PromptTokens
			if promptTokens <= 0 {
				promptTokens = info.GetEstimatePromptTokens()
			}
			if est := service.ResponseText2Usage(c, string(responseContent), info.UpstreamModelName, promptTokens); est != nil {
				usage = *est
				response.Usage = usage
			}
		}
	}
	// 添加 response.Choices
	response.Choices = []dto.OpenAITextResponseChoice{
		{
			Index:        0,
			Message:      dto.Message{Role: "assistant", Content: responseContent},
			FinishReason: "stop",
		},
	}
	jsonResponse, err := common.Marshal(response)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = c.Writer.Write(jsonResponse)

	return &usage, nil
}

func cozeChatStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)
	helper.StopStreamKeepAlive(c)
	scanner := helper.NewStreamScanner(resp.Body)
	scanner.Split(bufio.ScanLines)
	helper.SetEventStreamHeaders(c)
	id := helper.GetResponseID(c)
	var responseText string

	var currentEvent string
	var currentData string
	var usage = &dto.Usage{}
	// streamErr 是上游写进流里的失败帧。它既不能补 [DONE]，也不能当成功结算。
	var streamErr error
	var lastParseErr error

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			if currentEvent != "" && currentData != "" {
				// handle last event
				if err := handleCozeEvent(c, currentEvent, currentData, &responseText, usage, id, info, &lastParseErr); err != nil {
					streamErr = err
					break
				}
				currentEvent = ""
				currentData = ""
			}
			continue
		}

		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(line[6:])
			continue
		}

		if strings.HasPrefix(line, "data:") {
			currentData = strings.TrimSpace(line[5:])
			continue
		}
	}

	// Last event
	if streamErr == nil && currentEvent != "" && currentData != "" {
		streamErr = handleCozeEvent(c, currentEvent, currentData, &responseText, usage, id, info, &lastParseErr)
	}

	scanErr := scanner.Err()
	hadMidStreamErr := streamErr != nil || scanErr != nil
	if !helper.HasWrittenStreamPayload(c) && (info == nil || info.SendResponseCount == 0) {
		if helper.ClientRequestCanceled(c) {
			return nil, types.NewError(c.Request.Context().Err(), types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry())
		}
		if streamErr != nil {
			return nil, types.NewOpenAIError(streamErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if scanErr != nil {
			return nil, types.NewOpenAIError(scanErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if lastParseErr != nil {
			return nil, types.NewOpenAIError(lastParseErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		return nil, types.NewOpenAIError(errors.New("empty upstream stream response"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
	}
	if !hadMidStreamErr {
		helper.Done(c)
	}

	usage.PromptTokens = max(usage.PromptTokens, 0)
	usage.CompletionTokens = max(usage.CompletionTokens, 0)
	usage.TotalTokens = max(usage.TotalTokens, 0)
	if usage.PromptTokens <= 0 && usage.CompletionTokens <= 0 {
		if usage.TotalTokens > 0 {
			usage.PromptTokens = usage.TotalTokens
		} else {
			// 流式路径不经过 checkIfChatComplete，coze_input_count 没被设置过，取到的是 0；
			// 拿 0 当 prompt 等于把输入白送出去。退回所有 adaptor 共用的估算口径。
			promptTokens := c.GetInt("coze_input_count")
			if promptTokens <= 0 {
				promptTokens = info.GetEstimatePromptTokens()
			}
			usage = service.ResponseText2Usage(c, responseText, info.UpstreamModelName, promptTokens)
		}
	} else if usage.TotalTokens <= 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	return usage, nil
}

func handleCozeEvent(c *gin.Context, event string, data string, responseText *string, usage *dto.Usage, id string, info *relaycommon.RelayInfo, lastParseErr *error) error {
	switch event {
	case "conversation.chat.completed":
		// 将 data 解析为 CozeChatResponseData
		var chatData CozeChatResponseData
		err := common.Unmarshal([]byte(data), &chatData)
		if err != nil {
			common.SysLog(common.LogText("error_unmarshalling_stream_response: %s", err.Error()))
			if lastParseErr != nil {
				*lastParseErr = err
			}
			return nil
		}

		usage.PromptTokens = max(chatData.Usage.InputCount, 0)
		usage.CompletionTokens = max(chatData.Usage.OutputCount, 0)
		usage.TotalTokens = max(chatData.Usage.TokenCount, 0)
		if usage.TotalTokens <= 0 && (usage.PromptTokens > 0 || usage.CompletionTokens > 0) {
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}

		finishReason := "stop"
		stopResponse := helper.GenerateStopResponse(id, common.GetTimestamp(), info.UpstreamModelName, finishReason)
		if err := helper.ObjectData(c, stopResponse); err == nil && info != nil {
			info.SendResponseCount++
		}

	case "conversation.message.delta":
		// 将 data 解析为 CozeChatV3MessageDetail
		var messageData CozeChatV3MessageDetail
		err := common.Unmarshal([]byte(data), &messageData)
		if err != nil {
			common.SysLog(common.LogText("error_unmarshalling_stream_response: %s", err.Error()))
			if lastParseErr != nil {
				*lastParseErr = err
			}
			return nil
		}

		var content string
		err = common.Unmarshal(messageData.Content, &content)
		if err != nil {
			common.SysLog(common.LogText("error_unmarshalling_stream_response: %s", err.Error()))
			if lastParseErr != nil {
				*lastParseErr = err
			}
			return nil
		}

		openaiResponse := dto.ChatCompletionsStreamResponse{
			Id:      id,
			Object:  "chat.completion.chunk",
			Created: common.GetTimestamp(),
			Model:   info.UpstreamModelName,
		}

		choice := dto.ChatCompletionsStreamResponseChoice{
			Index: 0,
		}
		choice.Delta.SetContentString(content)
		openaiResponse.Choices = append(openaiResponse.Choices, choice)

		if err := helper.ObjectData(c, openaiResponse); err == nil {
			*responseText += content
			if info != nil {
				info.SendResponseCount++
			}
		}

	case "error":
		var errorData CozeError
		err := common.Unmarshal([]byte(data), &errorData)
		if err != nil {
			common.SysLog(common.LogText("error_unmarshalling_stream_response: %s", err.Error()))
			return fmt.Errorf("coze stream error: %w", err)
		}

		common.SysLog(common.LogText("stream event error: %v %v", errorData.Code, errorData.Message))
		return fmt.Errorf("coze stream error: code=%d message=%s", errorData.Code, errorData.Message)
	}
	return nil
}

func checkIfChatComplete(ctx context.Context, a *Adaptor, c *gin.Context, info *relaycommon.RelayInfo) (error, bool) {
	requestURL := fmt.Sprintf("%s/v3/chat/retrieve", info.ChannelBaseUrl)

	requestURL = requestURL + "?conversation_id=" + c.GetString("coze_conversation_id") + "&chat_id=" + c.GetString("coze_chat_id")
	// 将 conversationId和chatId作为参数发送get请求
	req, err := http.NewRequestWithContext(ctx, "GET", requestURL, nil)
	if err != nil {
		return common.SanitizeOutboundError(err, info.ApiKey, info.ChannelSetting.Proxy), false
	}
	err = a.SetupRequestHeader(c, &req.Header, info)
	if err != nil {
		return err, false
	}
	if err = channel.ApplyHeaderOverride(info, c, req); err != nil {
		return err, false
	}

	resp, err := channel.DoRequest(c, req, info)
	if err != nil {
		var apiErr *types.NewAPIError
		if errors.As(err, &apiErr) {
			return err, false
		}
		return common.SanitizeOutboundError(err, info.ApiKey, info.ChannelSetting.Proxy), false
	}
	if resp == nil { // 确保在 doRequest 失败时 resp 不为 nil 导致 panic
		return fmt.Errorf("resp is nil"), false
	}
	defer resp.Body.Close() // 确保响应体被关闭

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("coze chat retrieve failed with status %d", resp.StatusCode), false
	}

	// 解析 resp 到 CozeChatResponse
	var cozeResponse CozeChatResponse
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read response body failed: %w", err), false
	}
	err = common.Unmarshal(responseBody, &cozeResponse)
	if err != nil {
		return fmt.Errorf("unmarshal response body failed: %w", err), false
	}
	if cozeResponse.Code != 0 {
		return fmt.Errorf("coze chat retrieve error code %d", cozeResponse.Code), false
	}
	if cozeResponse.Data.Status == "completed" {
		// 在上下文设置 usage
		c.Set("coze_token_count", cozeResponse.Data.Usage.TokenCount)
		c.Set("coze_output_count", cozeResponse.Data.Usage.OutputCount)
		c.Set("coze_input_count", cozeResponse.Data.Usage.InputCount)
		return nil, true
	} else if cozeResponse.Data.Status == "failed" || cozeResponse.Data.Status == "canceled" || cozeResponse.Data.Status == "requires_action" {
		return fmt.Errorf("chat status: %s", cozeResponse.Data.Status), false
	} else {
		return nil, false
	}
}

func getChatDetail(ctx context.Context, a *Adaptor, c *gin.Context, info *relaycommon.RelayInfo) (*http.Response, error) {
	requestURL := fmt.Sprintf("%s/v3/chat/message/list", info.ChannelBaseUrl)

	requestURL = requestURL + "?conversation_id=" + c.GetString("coze_conversation_id") + "&chat_id=" + c.GetString("coze_chat_id")
	req, err := http.NewRequestWithContext(ctx, "GET", requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("new request failed: %w", common.SanitizeOutboundError(err, info.ApiKey, info.ChannelSetting.Proxy))
	}
	err = a.SetupRequestHeader(c, &req.Header, info)
	if err != nil {
		return nil, fmt.Errorf("setup request header failed: %w", err)
	}
	if err = channel.ApplyHeaderOverride(info, c, req); err != nil {
		return nil, err
	}
	resp, err := channel.DoRequest(c, req, info)
	if err != nil {
		var apiErr *types.NewAPIError
		if errors.As(err, &apiErr) {
			return nil, fmt.Errorf("do request failed: %w", err)
		}
		return nil, fmt.Errorf("do request failed: %w", common.SanitizeOutboundError(err, info.ApiKey, info.ChannelSetting.Proxy))
	}
	return resp, nil
}
