package xai

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// normalizeXAIUsage 用上游的 prompt_tokens / total_tokens 推出 completion_tokens，
// 并保证这个差值不会变成负数（ISS-R4-0071）。
//
// 上游这两个字段自相矛盾时（total < prompt；极端情形 total = 0），`total - prompt`
// 是负数。负的 completion 会先冲抵已经完成的那部分输出的费用；随后
// `service/text_quota.go` 又用 `prompt + completion` 重算 total，负数正好把 total
// 抹成 0，`hasBillableUsage()` 判成「无可计费用量」，于是整单免费。
//
// 矛盾时以 prompt 为准：total 抬到 prompt、completion 记 0，并留一条错误日志——
// 静默记 0 会让「上游回包自相矛盾」和「真的没有输出」在账面上长得一模一样。
func normalizeXAIUsage(usage *dto.Usage, where string) {
	if usage == nil {
		return
	}
	usage.PromptTokens = max(usage.PromptTokens, 0)
	usage.CompletionTokens = max(usage.CompletionTokens, 0)
	usage.TotalTokens = max(usage.TotalTokens, 0)
	usage.PromptTokensDetails.CachedTokens = max(usage.PromptTokensDetails.CachedTokens, 0)
	usage.CompletionTokenDetails.ReasoningTokens = max(usage.CompletionTokenDetails.ReasoningTokens, 0)
	if usage.TotalTokens < usage.PromptTokens {
		common.SysError(common.LogText(
			"%s upstream usage is self-contradictory: total_tokens=%d < prompt_tokens=%d, billing prompt tokens only",
			where, usage.TotalTokens, usage.PromptTokens))
		usage.TotalTokens = usage.PromptTokens
	}
	usage.CompletionTokens = max(usage.TotalTokens-usage.PromptTokens, 0)
}

func streamResponseXAI2OpenAI(xAIResp *dto.ChatCompletionsStreamResponse, usage *dto.Usage) *dto.ChatCompletionsStreamResponse {
	if xAIResp == nil {
		return nil
	}
	if xAIResp.Usage != nil {
		xAIResp.Usage.CompletionTokens = usage.CompletionTokens
	}
	openAIResp := &dto.ChatCompletionsStreamResponse{
		Id:      xAIResp.Id,
		Object:  xAIResp.Object,
		Created: xAIResp.Created,
		Model:   xAIResp.Model,
		Choices: xAIResp.Choices,
		Usage:   xAIResp.Usage,
	}

	return openAIResp
}

func xAIStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)
	usage := &dto.Usage{}
	var responseTextBuilder strings.Builder
	var toolCount int
	var containStreamUsage bool
	var lastParseErr error
	var streamErr *types.NewAPIError
	var embeddedErrStopped bool

	helper.SetEventStreamHeaders(c)

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if len(data) > 0 {
			if oaiErr, statusCode := openai.ExtractEmbeddedOpenAIError(common.StringToByteSlice(data), http.StatusBadGateway); oaiErr != nil {
				if !helper.HasWrittenStreamPayload(c) && (info == nil || info.SendResponseCount == 0) {
					streamErr = types.WithOpenAIError(*oaiErr, statusCode)
					sr.Stop(streamErr)
					return
				}
				embeddedErr := types.WithOpenAIError(*oaiErr, statusCode)
				logger.LogError(c, common.LogText("xai stream returned embedded error after payload written: %s", embeddedErr.Error()))
				sr.Error(embeddedErr)
				embeddedErrStopped = true
				sr.Stop(nil)
				return
			}
		}

		var xAIResp dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &xAIResp); err != nil {
			common.SysLog(common.LogText("error unmarshalling stream response: %s", err.Error()))
			lastParseErr = err
			sr.Error(err)
			return
		}

		// 把 xAI 的usage转换为 OpenAI 的usage
		if xAIResp.Usage != nil && (xAIResp.Usage.TotalTokens > 0 || xAIResp.Usage.PromptTokens > 0 || xAIResp.Usage.CompletionTokens > 0) {
			containStreamUsage = true
			usage.PromptTokens = max(xAIResp.Usage.PromptTokens, 0)
			usage.CompletionTokens = max(xAIResp.Usage.CompletionTokens, 0)
			usage.TotalTokens = max(xAIResp.Usage.TotalTokens, 0)
			if usage.TotalTokens == 0 && (usage.PromptTokens > 0 || usage.CompletionTokens > 0) {
				usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			}
			usage.PromptTokensDetails = xAIResp.Usage.PromptTokensDetails
			usage.CompletionTokenDetails = xAIResp.Usage.CompletionTokenDetails
			normalizeXAIUsage(usage, "xai stream")
		}

		openaiResponse := streamResponseXAI2OpenAI(&xAIResp, usage)
		if err := helper.ObjectData(c, openaiResponse); err != nil {
			common.SysLog(err.Error())
			if info != nil && info.StreamStatus != nil {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
			}
			sr.Stop(err)
			return
		}
		_ = openai.ProcessStreamResponse(*openaiResponse, &responseTextBuilder, &toolCount)
		if info != nil {
			info.SendResponseCount++
		}
	})

	if streamErr != nil {
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
				cancelErr = errors.New("client closed request")
			}
			return nil, types.NewError(cancelErr, types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry())
		}
		if info != nil && info.StreamStatus != nil && info.StreamStatus.EndError != nil {
			return nil, types.NewOpenAIError(info.StreamStatus.EndError, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if lastParseErr != nil {
			return nil, types.NewOpenAIError(lastParseErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		return nil, types.NewOpenAIError(errors.New("empty upstream stream response"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
	}

	if !containStreamUsage || usage.TotalTokens <= 0 {
		usage = service.ResponseText2Usage(c, responseTextBuilder.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		usage.CompletionTokens += toolCount * 7
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	if !embeddedErrStopped && (info == nil || info.StreamStatus == nil || info.StreamStatus.EndError == nil) {
		helper.Done(c)
	}
	return usage, nil
}

func xAIHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, apiErr := helper.ReadResponseBody(c, resp, types.ErrorCodeBadResponseBody)
	if apiErr != nil {
		return nil, apiErr
	}
	fallbackStatus := http.StatusBadGateway
	if resp != nil && resp.StatusCode >= 400 && resp.StatusCode <= 599 {
		fallbackStatus = resp.StatusCode
	}
	if oaiErr, statusCode := openai.ExtractEmbeddedOpenAIError(responseBody, fallbackStatus); oaiErr != nil {
		return nil, types.WithOpenAIError(*oaiErr, statusCode)
	}
	var xaiResponse ChatCompletionResponse
	err := common.Unmarshal(responseBody, &xaiResponse)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if xaiResponse.Usage != nil {
		xaiResponse.Usage.PromptTokens = max(xaiResponse.Usage.PromptTokens, 0)
		xaiResponse.Usage.CompletionTokens = max(xaiResponse.Usage.CompletionTokens, 0)
		xaiResponse.Usage.TotalTokens = max(xaiResponse.Usage.TotalTokens, 0)
		if xaiResponse.Usage.TotalTokens == 0 && (xaiResponse.Usage.PromptTokens > 0 || xaiResponse.Usage.CompletionTokens > 0) {
			xaiResponse.Usage.TotalTokens = xaiResponse.Usage.PromptTokens + xaiResponse.Usage.CompletionTokens
		}
		normalizeXAIUsage(xaiResponse.Usage, "xai non-stream")
		xaiResponse.Usage.CompletionTokenDetails.ReasoningTokens = max(xaiResponse.Usage.CompletionTokenDetails.ReasoningTokens, 0)
		xaiResponse.Usage.CompletionTokenDetails.TextTokens = max(xaiResponse.Usage.CompletionTokens-xaiResponse.Usage.CompletionTokenDetails.ReasoningTokens, 0)
	}
	if xaiResponse.Usage == nil || (xaiResponse.Usage.TotalTokens <= 0 && xaiResponse.Usage.PromptTokens <= 0 && xaiResponse.Usage.CompletionTokens <= 0) {
		var textBuilder strings.Builder
		for _, choice := range xaiResponse.Choices {
			textBuilder.WriteString(choice.Message.StringContent())
		}
		xaiResponse.Usage = service.ResponseText2Usage(c, textBuilder.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
	}

	// new body
	encodeJson, err := common.Marshal(xaiResponse)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}

	service.IOCopyBytesGracefully(c, resp, encodeJson)

	return xaiResponse.Usage, nil
}
