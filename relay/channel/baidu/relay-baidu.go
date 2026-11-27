package baidu

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
	"golang.org/x/sync/singleflight"
)

// https://cloud.baidu.com/doc/WENXINWORKSHOP/s/flfmc9do2

var (
	baiduTokenStore        sync.Map
	baiduTokenRefreshGroup singleflight.Group
	baiduTokenRefreshing   sync.Map
)

func requestOpenAI2Baidu(request dto.GeneralOpenAIRequest) *BaiduChatRequest {
	baiduRequest := BaiduChatRequest{
		Temperature:    request.Temperature,
		TopP:           lo.FromPtrOr(request.TopP, 0),
		PenaltyScore:   lo.FromPtrOr(request.FrequencyPenalty, 0),
		Stream:         lo.FromPtrOr(request.Stream, false),
		DisableSearch:  false,
		EnableCitation: false,
		UserId:         request.User,
	}
	if request.GetMaxTokens() != 0 {
		maxTokens := int(request.GetMaxTokens())
		if request.GetMaxTokens() == 1 {
			maxTokens = 2
		}
		baiduRequest.MaxOutputTokens = &maxTokens
	}
	for _, message := range request.Messages {
		if message.Role == "system" {
			baiduRequest.System = message.StringContent()
		} else {
			baiduRequest.Messages = append(baiduRequest.Messages, BaiduMessage{
				Role:    message.Role,
				Content: message.StringContent(),
			})
		}
	}
	return &baiduRequest
}

func responseBaidu2OpenAI(response *BaiduChatResponse) *dto.OpenAITextResponse {
	choice := dto.OpenAITextResponseChoice{
		Index: 0,
		Message: dto.Message{
			Role:    "assistant",
			Content: response.Result,
		},
		FinishReason: "stop",
	}
	fullTextResponse := dto.OpenAITextResponse{
		Id:      response.Id,
		Object:  "chat.completion",
		Created: response.Created,
		Choices: []dto.OpenAITextResponseChoice{choice},
		Usage:   response.Usage,
	}
	return &fullTextResponse
}

func streamResponseBaidu2OpenAI(baiduResponse *BaiduChatStreamResponse) *dto.ChatCompletionsStreamResponse {
	var choice dto.ChatCompletionsStreamResponseChoice
	choice.Delta.SetContentString(baiduResponse.Result)
	if baiduResponse.IsEnd {
		choice.FinishReason = &constant.FinishReasonStop
	}
	response := dto.ChatCompletionsStreamResponse{
		Id:      baiduResponse.Id,
		Object:  "chat.completion.chunk",
		Created: baiduResponse.Created,
		Model:   "ernie-bot",
		Choices: []dto.ChatCompletionsStreamResponseChoice{choice},
	}
	return &response
}

func embeddingRequestOpenAI2Baidu(request dto.EmbeddingRequest) *BaiduEmbeddingRequest {
	return &BaiduEmbeddingRequest{
		Input: request.ParseInput(),
	}
}

func embeddingResponseBaidu2OpenAI(response *BaiduEmbeddingResponse) *dto.OpenAIEmbeddingResponse {
	openAIEmbeddingResponse := dto.OpenAIEmbeddingResponse{
		Object: "list",
		Data:   make([]dto.OpenAIEmbeddingResponseItem, 0, len(response.Data)),
		Model:  "baidu-embedding",
		Usage:  response.Usage,
	}
	for _, item := range response.Data {
		openAIEmbeddingResponse.Data = append(openAIEmbeddingResponse.Data, dto.OpenAIEmbeddingResponseItem{
			Object:    item.Object,
			Index:     item.Index,
			Embedding: item.Embedding,
		})
	}
	return &openAIEmbeddingResponse
}

func baiduStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*types.NewAPIError, *dto.Usage) {
	defer service.CloseResponseBodyGracefully(resp)
	usage := &dto.Usage{}
	var responseText strings.Builder
	var lastParseErr error
	var streamErr *types.NewAPIError
	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var baiduResponse BaiduChatStreamResponse
		if err := common.Unmarshal([]byte(data), &baiduResponse); err != nil {
			common.SysLog(common.LogText("error unmarshalling stream response: %s", err.Error()))
			lastParseErr = err
			sr.Error(err)
			return
		}
		if baiduResponse.ErrorCode != 0 || baiduResponse.ErrorMsg != "" {
			errMsg := baiduResponse.ErrorMsg
			if errMsg == "" {
				errMsg = fmt.Sprintf("baidu upstream error code: %d", baiduResponse.ErrorCode)
			}
			if !helper.HasWrittenStreamPayload(c) && (info == nil || info.SendResponseCount == 0) {
				streamErr = types.NewOpenAIError(errors.New(errMsg), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
				sr.Stop(streamErr)
				return
			}
			common.SysLog(common.LogText("baidu stream error after output written, billing delivered usage: %s", errMsg))
			sr.Stop(errors.New(errMsg))
			return
		}
		if baiduResponse.Usage.TotalTokens > 0 || baiduResponse.Usage.PromptTokens > 0 || baiduResponse.Usage.CompletionTokens > 0 {
			prompt := max(baiduResponse.Usage.PromptTokens, 0)
			total := max(baiduResponse.Usage.TotalTokens, prompt)
			completion := max(baiduResponse.Usage.CompletionTokens, total-prompt, 0)
			usage.PromptTokens = prompt
			usage.CompletionTokens = completion
			usage.TotalTokens = max(total, prompt+completion)
		}
		response := streamResponseBaidu2OpenAI(&baiduResponse)
		if err := helper.ObjectData(c, response); err != nil {
			common.SysLog(common.LogText("error sending stream response: %s", err.Error()))
			sr.Stop(err)
			return
		}
		if baiduResponse.Result != "" {
			responseText.WriteString(baiduResponse.Result)
		}
		if info != nil {
			info.SendResponseCount++
		}
	})
	if !helper.HasWrittenStreamPayload(c) && (info == nil || info.SendResponseCount == 0) {
		if helper.ClientRequestCanceled(c) {
			return types.NewError(c.Request.Context().Err(), types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry()), nil
		}
		if streamErr != nil {
			return streamErr, nil
		}
		if info != nil && info.StreamStatus != nil && info.StreamStatus.EndError != nil {
			return types.NewOpenAIError(info.StreamStatus.EndError, types.ErrorCodeBadResponseBody, http.StatusInternalServerError), nil
		}
		if lastParseErr != nil {
			return types.NewOpenAIError(lastParseErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError), nil
		}
		return types.NewOpenAIError(errors.New("empty upstream stream response"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError), nil
	}
	if usage.TotalTokens <= 0 {
		usage = service.ResponseText2Usage(c, responseText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
	}
	return nil, usage
}

func baiduHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*types.NewAPIError, *dto.Usage) {
	defer service.CloseResponseBodyGracefully(resp)
	var baiduResponse BaiduChatResponse
	responseBody, readErr := helper.ReadResponseBody(c, resp, types.ErrorCodeBadResponseBody)
	if readErr != nil {
		return readErr, nil
	}
	var err error
	err = common.Unmarshal(responseBody, &baiduResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	if baiduResponse.ErrorMsg != "" {
		return types.NewError(fmt.Errorf("%s", baiduResponse.ErrorMsg), types.ErrorCodeBadResponseBody), nil
	}
	fullTextResponse := responseBaidu2OpenAI(&baiduResponse)
	fullTextResponse.Usage.PromptTokens = max(fullTextResponse.Usage.PromptTokens, 0)
	fullTextResponse.Usage.CompletionTokens = max(fullTextResponse.Usage.CompletionTokens, 0)
	fullTextResponse.Usage.TotalTokens = max(fullTextResponse.Usage.TotalTokens, 0)
	if fullTextResponse.Usage.PromptTokens <= 0 && fullTextResponse.Usage.CompletionTokens <= 0 {
		if fullTextResponse.Usage.TotalTokens > 0 {
			fullTextResponse.Usage.PromptTokens = fullTextResponse.Usage.TotalTokens
		} else if est := service.ResponseText2Usage(c, baiduResponse.Result, info.UpstreamModelName, info.GetEstimatePromptTokens()); est != nil {
			fullTextResponse.Usage = *est
		}
	} else if fullTextResponse.Usage.TotalTokens <= 0 {
		fullTextResponse.Usage.TotalTokens = fullTextResponse.Usage.PromptTokens + fullTextResponse.Usage.CompletionTokens
	}
	jsonResponse, err := common.Marshal(fullTextResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = c.Writer.Write(jsonResponse)
	return nil, &fullTextResponse.Usage
}

func baiduEmbeddingHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*types.NewAPIError, *dto.Usage) {
	defer service.CloseResponseBodyGracefully(resp)
	var baiduResponse BaiduEmbeddingResponse
	responseBody, readErr := helper.ReadResponseBody(c, resp, types.ErrorCodeBadResponseBody)
	if readErr != nil {
		return readErr, nil
	}
	var err error
	err = common.Unmarshal(responseBody, &baiduResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	if baiduResponse.ErrorMsg != "" {
		return types.NewError(fmt.Errorf("%s", baiduResponse.ErrorMsg), types.ErrorCodeBadResponseBody), nil
	}
	fullTextResponse := embeddingResponseBaidu2OpenAI(&baiduResponse)
	fullTextResponse.Usage.PromptTokens = max(fullTextResponse.Usage.PromptTokens, 0)
	fullTextResponse.Usage.CompletionTokens = max(fullTextResponse.Usage.CompletionTokens, 0)
	fullTextResponse.Usage.TotalTokens = max(fullTextResponse.Usage.TotalTokens, 0)
	if fullTextResponse.Usage.PromptTokens <= 0 {
		if fullTextResponse.Usage.TotalTokens > 0 {
			fullTextResponse.Usage.PromptTokens = fullTextResponse.Usage.TotalTokens
		} else if len(fullTextResponse.Data) > 0 {
			est := info.GetEstimatePromptTokens()
			fullTextResponse.Usage.PromptTokens = est
			fullTextResponse.Usage.TotalTokens = est
		}
	} else if fullTextResponse.Usage.TotalTokens <= 0 {
		fullTextResponse.Usage.TotalTokens = fullTextResponse.Usage.PromptTokens
	}
	jsonResponse, err := common.Marshal(fullTextResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = c.Writer.Write(jsonResponse)
	return nil, &fullTextResponse.Usage
}

func getBaiduAccessToken(apiKey string, settings dto.ChannelSettings) (string, error) {
	now := time.Now()
	if val, ok := baiduTokenStore.Load(apiKey); ok {
		if accessToken, ok := val.(BaiduAccessToken); ok && accessToken.AccessToken != "" && now.Before(accessToken.ExpiresAt) {
			// soon this will expire; deduplicate background refresh across concurrent callers
			if now.Add(time.Hour).After(accessToken.ExpiresAt) {
				if _, alreadyInFlight := baiduTokenRefreshing.LoadOrStore(apiKey, struct{}{}); !alreadyInFlight {
					go func() {
						defer baiduTokenRefreshing.Delete(apiKey)
						_, _, _ = baiduTokenRefreshGroup.Do(apiKey, func() (any, error) {
							return getBaiduAccessTokenHelper(apiKey, settings)
						})
					}()
				}
			}
			return accessToken.AccessToken, nil
		}
	}
	val, err, _ := baiduTokenRefreshGroup.Do(apiKey, func() (any, error) {
		if cached, ok := baiduTokenStore.Load(apiKey); ok {
			if token, ok := cached.(BaiduAccessToken); ok && token.AccessToken != "" && time.Now().Before(token.ExpiresAt) {
				return &token, nil
			}
		}
		return getBaiduAccessTokenHelper(apiKey, settings)
	})
	if err != nil {
		return "", err
	}
	accessToken, ok := val.(*BaiduAccessToken)
	if !ok || accessToken == nil || accessToken.AccessToken == "" {
		return "", errors.New("getBaiduAccessToken return a nil token")
	}
	return accessToken.AccessToken, nil
}

func getBaiduAccessTokenHelper(apiKey string, settings dto.ChannelSettings) (*BaiduAccessToken, error) {
	parts := strings.Split(apiKey, "|")
	if len(parts) != 2 {
		return nil, errors.New("invalid baidu apikey")
	}
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", parts[0])
	form.Set("client_secret", parts[1])
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://aip.baidubce.com/oauth/2.0/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.New("failed to create baidu token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	client, err := service.GetHttpClientWithProxySettings(settings.Proxy, settings)
	if err != nil {
		return nil, fmt.Errorf("failed to build baidu token http client: %w", err)
	}
	noRedirectClient := *client
	noRedirectClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	res, err := noRedirectClient.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Err != nil {
			return nil, fmt.Errorf("baidu token request failed: %w", urlErr.Err)
		}
		return nil, fmt.Errorf("baidu token request failed: %w", err)
	}
	defer res.Body.Close()

	var accessToken BaiduAccessToken
	err = common.DecodeJson(io.LimitReader(res.Body, 1<<20), &accessToken)
	if err != nil {
		return nil, err
	}
	if accessToken.Error != "" {
		return nil, errors.New(accessToken.Error + ": " + accessToken.ErrorDescription)
	}
	if accessToken.AccessToken == "" {
		return nil, errors.New("getBaiduAccessTokenHelper get empty access token")
	}
	accessToken.ExpiresAt = time.Now().Add(time.Duration(accessToken.ExpiresIn) * time.Second)
	baiduTokenStore.Store(apiKey, accessToken)
	return &accessToken, nil
}
