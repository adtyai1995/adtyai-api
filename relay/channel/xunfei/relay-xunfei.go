package xunfei

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// https://console.xfyun.cn/services/cbm
// https://www.xfyun.cn/doc/spark/Web.html

func requestOpenAI2Xunfei(request dto.GeneralOpenAIRequest, xunfeiAppId string, domain string) *XunfeiChatRequest {
	messages := make([]XunfeiMessage, 0, len(request.Messages))
	shouldCovertSystemMessage := !strings.HasSuffix(request.Model, "3.5")
	for _, message := range request.Messages {
		if message.Role == "system" && shouldCovertSystemMessage {
			messages = append(messages, XunfeiMessage{
				Role:    "user",
				Content: message.StringContent(),
			})
			messages = append(messages, XunfeiMessage{
				Role:    "assistant",
				Content: "Okay",
			})
		} else {
			messages = append(messages, XunfeiMessage{
				Role:    message.Role,
				Content: message.StringContent(),
			})
		}
	}
	xunfeiRequest := XunfeiChatRequest{}
	xunfeiRequest.Header.AppId = xunfeiAppId
	xunfeiRequest.Parameter.Chat.Domain = domain
	xunfeiRequest.Parameter.Chat.Temperature = request.Temperature
	xunfeiRequest.Parameter.Chat.TopK = lo.FromPtrOr(request.N, 0)
	xunfeiRequest.Parameter.Chat.MaxTokens = request.GetMaxTokens()
	xunfeiRequest.Payload.Message.Text = messages
	return &xunfeiRequest
}

func responseXunfei2OpenAI(response *XunfeiChatResponse) *dto.OpenAITextResponse {
	if len(response.Payload.Choices.Text) == 0 {
		response.Payload.Choices.Text = []XunfeiChatResponseTextItem{
			{
				Content: "",
			},
		}
	}
	choice := dto.OpenAITextResponseChoice{
		Index: 0,
		Message: dto.Message{
			Role:    "assistant",
			Content: response.Payload.Choices.Text[0].Content,
		},
		FinishReason: constant.FinishReasonStop,
	}
	fullTextResponse := dto.OpenAITextResponse{
		Object:  "chat.completion",
		Created: common.GetTimestamp(),
		Choices: []dto.OpenAITextResponseChoice{choice},
		Usage:   response.Payload.Usage.Text,
	}
	return &fullTextResponse
}

func streamResponseXunfei2OpenAI(xunfeiResponse *XunfeiChatResponse) *dto.ChatCompletionsStreamResponse {
	if len(xunfeiResponse.Payload.Choices.Text) == 0 {
		xunfeiResponse.Payload.Choices.Text = []XunfeiChatResponseTextItem{
			{
				Content: "",
			},
		}
	}
	var choice dto.ChatCompletionsStreamResponseChoice
	choice.Delta.SetContentString(xunfeiResponse.Payload.Choices.Text[0].Content)
	if xunfeiResponse.Payload.Choices.Status == 2 {
		choice.FinishReason = &constant.FinishReasonStop
	}
	response := dto.ChatCompletionsStreamResponse{
		Object:  "chat.completion.chunk",
		Created: common.GetTimestamp(),
		Model:   "SparkDesk",
		Choices: []dto.ChatCompletionsStreamResponseChoice{choice},
	}
	return &response
}

func buildXunfeiAuthUrl(hostUrl string, apiKey, apiSecret string) string {
	HmacWithShaToBase64 := func(algorithm, data, key string) string {
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(data))
		encodeData := mac.Sum(nil)
		return base64.StdEncoding.EncodeToString(encodeData)
	}
	ul, err := url.Parse(hostUrl)
	if err != nil {
		fmt.Println(err)
	}
	date := time.Now().UTC().Format(time.RFC1123)
	signString := []string{"host: " + ul.Host, "date: " + date, "GET " + ul.Path + " HTTP/1.1"}
	sign := strings.Join(signString, "\n")
	sha := HmacWithShaToBase64("hmac-sha256", sign, apiSecret)
	authUrl := fmt.Sprintf("hmac username=\"%s\", algorithm=\"%s\", headers=\"%s\", signature=\"%s\"", apiKey,
		"hmac-sha256", "host date request-line", sha)
	authorization := base64.StdEncoding.EncodeToString([]byte(authUrl))
	v := url.Values{}
	v.Add("host", ul.Host)
	v.Add("date", date)
	v.Add("authorization", authorization)
	callUrl := hostUrl + "?" + v.Encode()
	return callUrl
}

func xunfeiStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, textRequest dto.GeneralOpenAIRequest, appId string, apiSecret string, apiKey string) (*dto.Usage, *types.NewAPIError) {
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	domain, authUrl := getXunfeiAuthUrl(c, apiKey, apiSecret, textRequest.Model)
	dataChan, stopChan, err := xunfeiMakeRequest(ctx, info, textRequest, domain, authUrl, appId)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeDoRequestFailed)
	}
	helper.SetEventStreamHeaders(c)
	var usage dto.Usage
	var responseText strings.Builder
	hasOutput := false
	finalizeUsage := func() *dto.Usage {
		usage.PromptTokens = max(usage.PromptTokens, 0)
		usage.CompletionTokens = max(usage.CompletionTokens, 0)
		usage.TotalTokens = max(usage.TotalTokens, 0)
		if usage.TotalTokens <= 0 && usage.PromptTokens <= 0 && usage.CompletionTokens <= 0 && (hasOutput || responseText.Len() > 0) {
			modelName := textRequest.Model
			promptTokens := 0
			if info != nil {
				if info.UpstreamModelName != "" {
					modelName = info.UpstreamModelName
				}
				promptTokens = info.GetEstimatePromptTokens()
			}
			return service.ResponseText2Usage(c, responseText.String(), modelName, promptTokens)
		}
		if usage.TotalTokens <= 0 {
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
		return &usage
	}
	for {
		select {
		case <-ctx.Done():
			if hasOutput {
				return finalizeUsage(), nil
			}
			return &usage, types.NewError(ctx.Err(), types.ErrorCodeDoRequestFailed)
		case xunfeiResponse := <-dataChan:
			usage.PromptTokens += max(xunfeiResponse.Payload.Usage.Text.PromptTokens, 0)
			usage.CompletionTokens += max(xunfeiResponse.Payload.Usage.Text.CompletionTokens, 0)
			usage.TotalTokens += max(xunfeiResponse.Payload.Usage.Text.TotalTokens, 0)
			if len(xunfeiResponse.Payload.Choices.Text) > 0 {
				responseText.WriteString(xunfeiResponse.Payload.Choices.Text[0].Content)
			}
			response := streamResponseXunfei2OpenAI(&xunfeiResponse)
			jsonResponse, err := common.Marshal(response)
			if err != nil {
				if hasOutput {
					return finalizeUsage(), nil
				}
				return &usage, types.NewError(err, types.ErrorCodeBadResponseBody)
			}
			c.Render(-1, common.CustomEvent{Data: "data: " + string(jsonResponse)})
			c.Writer.Flush()
			hasOutput = true
		case err := <-stopChan:
			if err != nil {
				if hasOutput {
					return finalizeUsage(), nil
				}
				return &usage, types.NewError(err, types.ErrorCodeBadResponseBody)
			}
			c.Render(-1, common.CustomEvent{Data: "data: [DONE]"})
			c.Writer.Flush()
			return finalizeUsage(), nil
		}
	}
}

func xunfeiHandler(c *gin.Context, info *relaycommon.RelayInfo, textRequest dto.GeneralOpenAIRequest, appId string, apiSecret string, apiKey string) (*dto.Usage, *types.NewAPIError) {
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	domain, authUrl := getXunfeiAuthUrl(c, apiKey, apiSecret, textRequest.Model)
	dataChan, stopChan, err := xunfeiMakeRequest(ctx, info, textRequest, domain, authUrl, appId)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeDoRequestFailed)
	}
	var usage dto.Usage
	var content string
	var xunfeiResponse XunfeiChatResponse
	stop := false
	for !stop {
		select {
		case <-ctx.Done():
			return &usage, types.NewError(ctx.Err(), types.ErrorCodeDoRequestFailed)
		case xunfeiResponse = <-dataChan:
			if len(xunfeiResponse.Payload.Choices.Text) > 0 {
				content += xunfeiResponse.Payload.Choices.Text[0].Content
			}
			usage.PromptTokens += max(xunfeiResponse.Payload.Usage.Text.PromptTokens, 0)
			usage.CompletionTokens += max(xunfeiResponse.Payload.Usage.Text.CompletionTokens, 0)
			usage.TotalTokens += max(xunfeiResponse.Payload.Usage.Text.TotalTokens, 0)
		case err := <-stopChan:
			if err != nil {
				return &usage, types.NewError(err, types.ErrorCodeBadResponseBody)
			}
			stop = true
		}
	}
	if len(xunfeiResponse.Payload.Choices.Text) == 0 {
		xunfeiResponse.Payload.Choices.Text = []XunfeiChatResponseTextItem{
			{
				Content: "",
			},
		}
	}
	if usage.TotalTokens <= 0 && usage.PromptTokens <= 0 && usage.CompletionTokens <= 0 {
		modelName := textRequest.Model
		promptTokens := 0
		if info != nil {
			if info.UpstreamModelName != "" {
				modelName = info.UpstreamModelName
			}
			promptTokens = info.GetEstimatePromptTokens()
		}
		if est := service.ResponseText2Usage(c, content, modelName, promptTokens); est != nil {
			usage = *est
		}
	} else if usage.TotalTokens <= 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	xunfeiResponse.Payload.Choices.Text[0].Content = content
	xunfeiResponse.Payload.Usage.Text = usage

	response := responseXunfei2OpenAI(&xunfeiResponse)
	jsonResponse, err := common.Marshal(response)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	_, _ = c.Writer.Write(jsonResponse)
	return &usage, nil
}

func xunfeiMakeRequest(ctx context.Context, info *relaycommon.RelayInfo, textRequest dto.GeneralOpenAIRequest, domain, authUrl, appId string) (<-chan XunfeiChatResponse, <-chan error, error) {
	handshakeTimeout := 10 * time.Second
	if common.RelayTimeout > 0 {
		if configured := time.Duration(common.RelayTimeout) * time.Second; configured < handshakeTimeout {
			handshakeTimeout = configured
		}
	}
	d := websocket.Dialer{
		HandshakeTimeout: handshakeTimeout,
	}
	if info != nil {
		tr, err := service.NewRelayTransportWithProxySettings(info.ChannelSetting.Proxy, info.ChannelSetting)
		if err != nil {
			return nil, nil, fmt.Errorf("xunfei transport setup failed: %w", err)
		}
		d.Proxy = tr.Proxy
		d.NetDialContext = tr.DialContext
		d.TLSClientConfig = tr.TLSClientConfig
	}
	conn, resp, err := d.DialContext(ctx, authUrl, nil)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr != nil {
			sanitized := *urlErr
			sanitized.URL = relaycommon.SanitizeURLForLog(sanitized.URL)
			return nil, nil, &sanitized
		}
		return nil, nil, fmt.Errorf("xunfei dial failed (%s): %w", relaycommon.SanitizeURLForLog(authUrl), err)
	}
	stopCancellation := context.AfterFunc(ctx, func() { _ = conn.Close() })

	data := requestOpenAI2Xunfei(textRequest, appId, domain)
	err = conn.WriteJSON(data)
	if err != nil {
		stopCancellation()
		_ = conn.Close()
		return nil, nil, err
	}

	readTimeout := 60 * time.Second
	if common.RelayTimeout > 0 {
		readTimeout = time.Duration(common.RelayTimeout) * time.Second
	}
	dataChan := make(chan XunfeiChatResponse)
	stopChan := make(chan error, 1)
	go func() {
		var responseErr error
		defer func() {
			stopCancellation()
			_ = conn.Close()
			stopChan <- responseErr
		}()
		for {
			_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
			_, msg, err := conn.ReadMessage()
			if err != nil {
				responseErr = fmt.Errorf("xunfei response ended before its final frame: %w", err)
				return
			}
			var response XunfeiChatResponse
			err = common.Unmarshal(msg, &response)
			if err != nil {
				responseErr = fmt.Errorf("invalid xunfei response: %w", err)
				return
			}
			if response.Header.Code != 0 {
				responseErr = fmt.Errorf("xunfei upstream error %d: %s", response.Header.Code, response.Header.Message)
				return
			}
			select {
			case dataChan <- response:
			case <-ctx.Done():
				responseErr = ctx.Err()
				return
			}
			if response.Payload.Choices.Status == 2 {
				return
			}
		}
	}()

	return dataChan, stopChan, nil
}

func apiVersion2domain(apiVersion string) string {
	switch apiVersion {
	case "v1.1":
		return "lite"
	case "v2.1":
		return "generalv2"
	case "v3.1":
		return "generalv3"
	case "v3.5":
		return "generalv3.5"
	case "v4.0":
		return "4.0Ultra"
	}
	return "general" + apiVersion
}

func getXunfeiAuthUrl(c *gin.Context, apiKey string, apiSecret string, modelName string) (string, string) {
	apiVersion := getAPIVersion(c, modelName)
	domain := apiVersion2domain(apiVersion)
	baseURL := strings.TrimRight(common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl), "/")
	if baseURL == "" {
		baseURL = "wss://spark-api.xf-yun.com"
	} else if strings.HasPrefix(baseURL, "https://") {
		baseURL = "wss://" + strings.TrimPrefix(baseURL, "https://")
	} else if strings.HasPrefix(baseURL, "http://") {
		baseURL = "ws://" + strings.TrimPrefix(baseURL, "http://")
	}
	authUrl := buildXunfeiAuthUrl(fmt.Sprintf("%s/%s/chat", baseURL, apiVersion), apiKey, apiSecret)
	return domain, authUrl
}

func isAllowedXunfeiAPIVersion(v string) bool {
	switch v {
	case "v1.1", "v2.1", "v3.1", "v3.5", "v4.0":
		return true
	default:
		return false
	}
}

func getAPIVersion(c *gin.Context, modelName string) string {
	// R6-012: Never trust client query `?api-version=` to override upstream domain/path.
	parts := strings.Split(modelName, "-")
	if len(parts) == 2 && isAllowedXunfeiAPIVersion(parts[1]) {
		return parts[1]
	}
	apiVersion := strings.TrimSpace(c.GetString("api_version"))
	if isAllowedXunfeiAPIVersion(apiVersion) {
		return apiVersion
	}
	apiVersion = "v1.1"
	common.SysLog(common.LogText("api_version not found, using default: %s", apiVersion))
	return apiVersion
}
