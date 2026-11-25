package common_handler

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel/xinference"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func RerankHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp != nil {
		defer service.CloseResponseBodyGracefully(resp)
	}
	responseBody, readErr := helper.ReadResponseBody(c, resp, types.ErrorCodeReadResponseBodyFailed)
	if readErr != nil {
		return nil, readErr
	}
	var err error
	logger.LogDebug(c, "reranker response body: %s", responseBody)
	var jinaResp dto.RerankResponse
	if info.ChannelType == constant.ChannelTypeXinference {
		var xinRerankResponse xinference.XinRerankResponse
		err = common.Unmarshal(responseBody, &xinRerankResponse)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		jinaRespResults := make([]dto.RerankResponseResult, len(xinRerankResponse.Results))
		for i, result := range xinRerankResponse.Results {
			respResult := dto.RerankResponseResult{
				Index:          result.Index,
				RelevanceScore: result.RelevanceScore,
			}
			if info.ReturnDocuments {
				var document any
				if result.Document != nil {
					if doc, ok := result.Document.(string); ok {
						if doc == "" {
							if result.Index >= 0 && result.Index < len(info.Documents) {
								document = info.Documents[result.Index]
							}
						} else {
							document = doc
						}
					} else {
						document = result.Document
					}
				}
				respResult.Document = document
			}
			jinaRespResults[i] = respResult
		}
		jinaResp = dto.RerankResponse{
			Results: jinaRespResults,
			Usage: dto.Usage{
				PromptTokens: info.GetEstimatePromptTokens(),
				TotalTokens:  info.GetEstimatePromptTokens(),
			},
		}
	} else {
		err = common.Unmarshal(responseBody, &jinaResp)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		jinaResp.Usage.PromptTokens = max(jinaResp.Usage.PromptTokens, 0)
		jinaResp.Usage.CompletionTokens = max(jinaResp.Usage.CompletionTokens, 0)
		jinaResp.Usage.TotalTokens = max(jinaResp.Usage.TotalTokens, 0)
		if jinaResp.Usage.TotalTokens <= 0 && jinaResp.Usage.PromptTokens <= 0 {
			est := info.GetEstimatePromptTokens()
			jinaResp.Usage.PromptTokens = est
			jinaResp.Usage.TotalTokens = est
		} else if jinaResp.Usage.TotalTokens > 0 {
			jinaResp.Usage.PromptTokens = jinaResp.Usage.TotalTokens
		} else {
			jinaResp.Usage.TotalTokens = jinaResp.Usage.PromptTokens
		}
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	c.JSON(http.StatusOK, jinaResp)
	return &jinaResp.Usage, nil
}
