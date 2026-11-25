package cloudflare

import (
	"bufio"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
)

func convertCf2CompletionsRequest(textRequest dto.GeneralOpenAIRequest) *CfRequest {
	p, _ := textRequest.Prompt.(string)
	return &CfRequest{
		Prompt:      p,
		MaxTokens:   textRequest.GetMaxTokens(),
		Stream:      lo.FromPtrOr(textRequest.Stream, false),
		Temperature: textRequest.Temperature,
	}
}

func cfStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*types.NewAPIError, *dto.Usage) {
	if resp != nil {
		defer service.CloseResponseBodyGracefully(resp)
	}
	helper.StopStreamKeepAlive(c)
	scanner := helper.NewStreamScanner(resp.Body)
	scanner.Split(bufio.ScanLines)

	helper.SetEventStreamHeaders(c)
	id := helper.GetResponseID(c)
	var responseText string
	isFirst := true
	var lastParseErr error

	for scanner.Scan() {
		data := scanner.Text()
		if len(data) < len("data: ") {
			continue
		}
		data = strings.TrimPrefix(data, "data: ")
		data = strings.TrimSuffix(data, "\r")

		if data == "[DONE]" {
			break
		}

		var response dto.ChatCompletionsStreamResponse
		err := common.Unmarshal([]byte(data), &response)
		if err != nil {
			lastParseErr = err
			logger.LogError(c, common.LogText("error_unmarshalling_stream_response: %s", err.Error()))
			continue
		}
		var chunkText strings.Builder
		for i := range response.Choices {
			response.Choices[i].Delta.Role = "assistant"
			chunkText.WriteString(response.Choices[i].Delta.GetContentString())
		}
		response.Id = id
		response.Model = info.UpstreamModelName
		err = helper.ObjectData(c, response)
		if err != nil {
			logger.LogError(c, common.LogText("error_rendering_stream_response: %s", err.Error()))
			break
		}
		if isFirst {
			isFirst = false
			info.FirstResponseTime = time.Now()
		}
		responseText += chunkText.String()
		if info != nil {
			info.SendResponseCount++
		}
	}

	scanErr := scanner.Err()
	if scanErr != nil {
		logger.LogError(c, common.LogText("error_scanning_stream_response: %s", scanErr.Error()))
	}
	if !helper.HasWrittenStreamPayload(c) && (info == nil || info.SendResponseCount == 0) {
		if helper.ClientRequestCanceled(c) {
			return types.NewError(c.Request.Context().Err(), types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry()), nil
		}
		if scanErr != nil {
			return types.NewOpenAIError(scanErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError), nil
		}
		if lastParseErr != nil {
			return types.NewOpenAIError(lastParseErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError), nil
		}
		return types.NewOpenAIError(errors.New("empty upstream stream response"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError), nil
	}
	usage := service.ResponseText2Usage(c, responseText, info.UpstreamModelName, info.GetEstimatePromptTokens())
	if scanErr == nil {
		if info.ShouldIncludeUsage {
			response := helper.GenerateFinalUsageResponse(id, info.StartTime.Unix(), info.UpstreamModelName, *usage)
			err := helper.ObjectData(c, response)
			if err != nil {
				logger.LogError(c, common.LogText("error_rendering_final_usage_response: %s", err.Error()))
			}
		}
		helper.Done(c)
	}

	return nil, usage
}

func cfHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*types.NewAPIError, *dto.Usage) {
	if resp != nil {
		defer service.CloseResponseBodyGracefully(resp)
	}
	responseBody, readErr := helper.ReadResponseBody(c, resp, types.ErrorCodeBadResponseBody)
	if readErr != nil {
		return readErr, nil
	}
	var err error
	var response dto.TextResponse
	err = common.Unmarshal(responseBody, &response)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	response.Model = info.UpstreamModelName
	var responseText string
	for _, choice := range response.Choices {
		responseText += choice.Message.StringContent()
	}
	usage := service.ResponseText2Usage(c, responseText, info.UpstreamModelName, info.GetEstimatePromptTokens())
	response.Usage = *usage
	response.Id = helper.GetResponseID(c)
	jsonResponse, err := common.Marshal(response)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = c.Writer.Write(jsonResponse)
	return nil, usage
}

func cfSTTHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*types.NewAPIError, *dto.Usage) {
	if resp != nil {
		defer service.CloseResponseBodyGracefully(resp)
	}
	var cfResp CfAudioResponse
	responseBody, readErr := helper.ReadResponseBody(c, resp, types.ErrorCodeBadResponseBody)
	if readErr != nil {
		return readErr, nil
	}
	var err error
	err = common.Unmarshal(responseBody, &cfResp)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}

	audioResp := &dto.AudioResponse{
		Text: cfResp.Result.Text,
	}

	jsonResponse, err := common.Marshal(audioResp)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = c.Writer.Write(jsonResponse)

	usage := service.ResponseText2Usage(c, cfResp.Result.Text, info.UpstreamModelName, info.GetEstimatePromptTokens())
	return nil, usage
}
