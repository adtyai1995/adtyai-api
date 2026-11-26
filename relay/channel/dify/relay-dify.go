package dify

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
)

func uploadDifyFile(c *gin.Context, info *relaycommon.RelayInfo, user string, media dto.MediaContent) (*DifyFile, error) {
	if info == nil {
		return nil, errors.New("relay info is nil")
	}
	uploadUrl := fmt.Sprintf("%s/v1/files/upload", info.ChannelBaseUrl)
	switch media.Type {
	case dto.ContentTypeImageURL:
		// Decode base64 data
		imageMedia := media.GetImageMedia()
		base64Data := imageMedia.Url
		// Remove base64 prefix if exists (e.g., "data:image/jpeg;base64,")
		if idx := strings.Index(base64Data, ","); idx != -1 {
			base64Data = base64Data[idx+1:]
		}

		// Decode base64 string
		decodedData, err := base64.StdEncoding.DecodeString(base64Data)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64: %w", err)
		}

		// Create multipart form directly from in-memory decodedData
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)

		// Add user field
		if err := writer.WriteField("user", user); err != nil {
			_ = writer.Close()
			return nil, fmt.Errorf("failed to add user field: %w", err)
		}

		// Create form file with proper mime type
		mimeType := imageMedia.MimeType
		if mimeType == "" {
			mimeType = "image/jpeg" // default mime type
		}

		// Create form file
		part, err := writer.CreateFormFile("file", fmt.Sprintf("image.%s", strings.TrimPrefix(mimeType, "image/")))
		if err != nil {
			_ = writer.Close()
			return nil, fmt.Errorf("failed to create form file: %w", err)
		}

		// Copy file content to form
		if _, err = io.Copy(part, bytes.NewReader(decodedData)); err != nil {
			_ = writer.Close()
			return nil, fmt.Errorf("failed to copy file content: %w", err)
		}
		if err := writer.Close(); err != nil {
			return nil, fmt.Errorf("failed to finalize multipart form: %w", err)
		}

		parentCtx := context.Background()
		if c != nil && c.Request != nil && c.Request.Context() != nil {
			parentCtx = c.Request.Context()
		}
		if err := parentCtx.Err(); err != nil {
			return nil, err
		}
		uploadTimeout := 60 * time.Second
		if common.RelayTimeout > 0 {
			uploadTimeout = time.Duration(common.RelayTimeout) * time.Second
		}
		ctx, cancel := context.WithTimeout(parentCtx, uploadTimeout)
		defer cancel()

		// Create HTTP request
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadUrl, body)
		if err != nil {
			return nil, fmt.Errorf("failed to create upload request: %w", common.SanitizeOutboundError(err, info.ApiKey, info.ChannelSetting.Proxy))
		}

		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", info.ApiKey))
		if err := channel.ApplyHeaderOverride(info, c, req); err != nil {
			return nil, err
		}

		// Send request
		client, err := service.GetHttpClientWithProxySettings(info.ChannelSetting.Proxy, info.ChannelSetting)
		if err != nil {
			return nil, fmt.Errorf("failed to configure http client: %w", common.SanitizeOutboundError(err, info.ApiKey, info.ChannelSetting.Proxy))
		}
		noRedirectClient := *client
		noRedirectClient.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
		resp, err := noRedirectClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to send upload request: %w", common.SanitizeOutboundError(err, info.ApiKey, info.ChannelSetting.Proxy))
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			return nil, fmt.Errorf("failed to read upload response: %w", common.SanitizeOutboundError(err, info.ApiKey, info.ChannelSetting.Proxy))
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("dify file upload failed with status %d", resp.StatusCode)
		}

		// Parse response
		var result struct {
			Id string `json:"id"`
		}
		if err := common.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("failed to decode upload response: %w", err)
		}
		if strings.TrimSpace(result.Id) == "" {
			return nil, errors.New("dify file upload response missing id")
		}

		return &DifyFile{
			UploadFileId: result.Id,
			Type:         "image",
			TransferMode: "local_file",
		}, nil
	}
	return nil, nil
}

func requestOpenAI2Dify(c *gin.Context, info *relaycommon.RelayInfo, request dto.GeneralOpenAIRequest) *DifyChatRequest {
	difyReq := DifyChatRequest{
		Inputs:           make(map[string]interface{}),
		AutoGenerateName: false,
	}

	user := request.User
	if len(user) == 0 {
		user = json.RawMessage(helper.GetResponseID(c))
	}
	var stringUser string
	err := common.Unmarshal(user, &stringUser)
	if err != nil {
		common.SysLog(common.LogText("failed to unmarshal user: %s", err.Error()))
		stringUser = helper.GetResponseID(c)
	}
	difyReq.User = stringUser

	files := make([]DifyFile, 0)
	var content strings.Builder
	for _, message := range request.Messages {
		if message.Role == "system" {
			content.WriteString("SYSTEM: \n" + message.StringContent() + "\n")
		} else if message.Role == "assistant" {
			content.WriteString("ASSISTANT: \n" + message.StringContent() + "\n")
		} else {
			parseContent := message.ParseContent()
			for _, mediaContent := range parseContent {
				switch mediaContent.Type {
				case dto.ContentTypeText:
					content.WriteString("USER: \n" + mediaContent.Text + "\n")
				case dto.ContentTypeImageURL:
					media := mediaContent.GetImageMedia()
					var file *DifyFile
					if media.IsRemoteImage() {
						// 修复 #2083: 远程图片分支此前未初始化 file，
						// 导致 file.Type = ... 触发 nil pointer dereference
						// 而 panic（500: "invalid memory address or nil pointer dereference"）。
						file = &DifyFile{
							Type:         media.MimeType,
							TransferMode: "remote_url",
							URL:          media.Url,
						}
					} else {
						var uploadErr error
						file, uploadErr = uploadDifyFile(c, info, difyReq.User, mediaContent)
						if uploadErr != nil {
							common.SysLog(common.LogText("failed to upload dify file: %s", uploadErr.Error()))
						}
					}
					if file != nil {
						files = append(files, *file)
					}
				}
			}
		}
	}
	difyReq.Query = content.String()
	difyReq.Files = files
	mode := "blocking"
	if lo.FromPtrOr(request.Stream, false) {
		mode = "streaming"
	}
	difyReq.ResponseMode = mode
	return &difyReq
}

func streamResponseDify2OpenAI(difyResponse DifyChunkChatCompletionResponse) *dto.ChatCompletionsStreamResponse {
	response := dto.ChatCompletionsStreamResponse{
		Object:  "chat.completion.chunk",
		Created: common.GetTimestamp(),
		Model:   "dify",
	}
	var choice dto.ChatCompletionsStreamResponseChoice
	if strings.HasPrefix(difyResponse.Event, "workflow_") {
		if constant.DifyDebug {
			text := "Workflow: " + difyResponse.Data.WorkflowId
			if difyResponse.Event == "workflow_finished" {
				text += " " + difyResponse.Data.Status
			}
			choice.Delta.SetReasoningContent(text + "\n")
		}
	} else if strings.HasPrefix(difyResponse.Event, "node_") {
		if constant.DifyDebug {
			text := "Node: " + difyResponse.Data.NodeType
			if difyResponse.Event == "node_finished" {
				text += " " + difyResponse.Data.Status
			}
			choice.Delta.SetReasoningContent(text + "\n")
		}
	} else if difyResponse.Event == "message" || difyResponse.Event == "agent_message" {
		if difyResponse.Answer == "<details style=\"color:gray;background-color: #f8f8f8;padding: 8px;border-radius: 4px;\" open> <summary> Thinking... </summary>\n" {
			difyResponse.Answer = "<think>"
		} else if difyResponse.Answer == "</details>" {
			difyResponse.Answer = "</think>"
		}

		choice.Delta.SetContentString(difyResponse.Answer)
	}
	response.Choices = append(response.Choices, choice)
	return &response
}

func difyStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp != nil {
		defer service.CloseResponseBodyGracefully(resp)
	}
	var responseText string
	usage := &dto.Usage{}
	var nodeToken int
	var streamErr error
	var lastParseErr error
	helper.SetEventStreamHeaders(c)
	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var difyResponse DifyChunkChatCompletionResponse
		if err := common.Unmarshal([]byte(data), &difyResponse); err != nil {
			common.SysLog(common.LogText("error unmarshalling stream response: %s", err.Error()))
			lastParseErr = err
			sr.Error(err)
			return
		}
		if difyResponse.Event == "message_end" {
			u := difyResponse.MetaData.Usage
			u.PromptTokens = max(u.PromptTokens, 0)
			u.CompletionTokens = max(u.CompletionTokens, 0)
			u.TotalTokens = max(u.TotalTokens, 0)
			usage = &u
			sr.Done()
			return
		} else if difyResponse.Event == "error" {
			streamErr = fmt.Errorf("dify error event")
			sr.Stop(streamErr)
			return
		}
		openaiResponse := *streamResponseDify2OpenAI(difyResponse)
		if err := helper.ObjectData(c, openaiResponse); err != nil {
			common.SysLog(err.Error())
			sr.Stop(err)
			return
		}
		if len(openaiResponse.Choices) != 0 {
			content := openaiResponse.Choices[0].Delta.GetContentString()
			responseText += content
			if openaiResponse.Choices[0].Delta.ReasoningContent != nil {
				nodeToken += 1
			}
		}
		if info != nil {
			info.SendResponseCount++
		}
	})
	if !helper.HasWrittenStreamPayload(c) && (info == nil || info.SendResponseCount == 0) {
		if helper.ClientRequestCanceled(c) {
			return nil, types.NewError(c.Request.Context().Err(), types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry())
		}
		if streamErr != nil {
			return nil, types.NewOpenAIError(streamErr, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
		}
		if info != nil && info.StreamStatus != nil && info.StreamStatus.EndError != nil {
			return nil, types.NewOpenAIError(info.StreamStatus.EndError, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
		}
		if lastParseErr != nil {
			return nil, types.NewOpenAIError(lastParseErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		return nil, types.NewOpenAIError(errors.New("empty upstream stream response"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
	}
	if streamErr == nil && (info == nil || info.StreamStatus == nil || info.StreamStatus.EndError == nil) {
		helper.Done(c)
	}
	usage.PromptTokens = max(usage.PromptTokens, 0)
	usage.CompletionTokens = max(usage.CompletionTokens, 0)
	usage.TotalTokens = max(usage.TotalTokens, 0)
	if usage.PromptTokens <= 0 && usage.CompletionTokens <= 0 {
		if usage.TotalTokens > 0 {
			usage.PromptTokens = usage.TotalTokens
		} else {
			usage = service.ResponseText2Usage(c, responseText, info.UpstreamModelName, info.GetEstimatePromptTokens())
		}
	} else if usage.TotalTokens <= 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	usage.CompletionTokens += nodeToken
	usage.TotalTokens = max(usage.TotalTokens, usage.PromptTokens+usage.CompletionTokens)
	return usage, nil
}

func difyHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp != nil {
		defer service.CloseResponseBodyGracefully(resp)
	}
	var difyResponse DifyChatCompletionResponse
	responseBody, readErr := helper.ReadResponseBody(c, resp, types.ErrorCodeBadResponseBody)
	if readErr != nil {
		return nil, readErr
	}
	var err error
	err = common.Unmarshal(responseBody, &difyResponse)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	usage := difyResponse.MetaData.Usage
	usage.PromptTokens = max(usage.PromptTokens, 0)
	usage.CompletionTokens = max(usage.CompletionTokens, 0)
	usage.TotalTokens = max(usage.TotalTokens, 0)
	if usage.PromptTokens <= 0 && usage.CompletionTokens <= 0 {
		if usage.TotalTokens > 0 {
			usage.PromptTokens = usage.TotalTokens
		} else if strings.TrimSpace(difyResponse.Answer) != "" {
			if est := service.ResponseText2Usage(c, difyResponse.Answer, info.UpstreamModelName, info.GetEstimatePromptTokens()); est != nil {
				usage = *est
			}
		}
	} else if usage.TotalTokens <= 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	fullTextResponse := dto.OpenAITextResponse{
		Id:      difyResponse.ConversationId,
		Object:  "chat.completion",
		Created: common.GetTimestamp(),
		Usage:   usage,
	}
	choice := dto.OpenAITextResponseChoice{
		Index: 0,
		Message: dto.Message{
			Role:    "assistant",
			Content: difyResponse.Answer,
		},
		FinishReason: "stop",
	}
	fullTextResponse.Choices = append(fullTextResponse.Choices, choice)
	jsonResponse, err := common.Marshal(fullTextResponse)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	c.Writer.Write(jsonResponse)
	return &usage, nil
}
