package siliconflow

import (
	"net/http"
	"github.com/QuantumNous/new-api/common"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func siliconflowRerankHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp != nil {
		defer service.CloseResponseBodyGracefully(resp)
	}
	responseBody, readErr := helper.ReadResponseBody(c, resp, types.ErrorCodeReadResponseBodyFailed)
	if readErr != nil {
		return nil, readErr
	}
	var err error
	var siliconflowResp SFRerankResponse
	err = common.Unmarshal(responseBody, &siliconflowResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	inTokens := max(siliconflowResp.Meta.Tokens.InputTokens, 0)
	outTokens := max(siliconflowResp.Meta.Tokens.OutputTokens, 0)
	var usage *dto.Usage
	if inTokens <= 0 && outTokens <= 0 {
		est := info.GetEstimatePromptTokens()
		usage = &dto.Usage{
			PromptTokens:     est,
			CompletionTokens: 0,
			TotalTokens:      est,
		}
	} else {
		usage = &dto.Usage{
			PromptTokens:     inTokens,
			CompletionTokens: outTokens,
			TotalTokens:      inTokens + outTokens,
		}
	}
	rerankResp := &dto.RerankResponse{
		Results: siliconflowResp.Results,
		Usage:   *usage,
	}

	jsonResponse, err := common.Marshal(rerankResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	service.IOCopyBytesGracefully(c, resp, jsonResponse)
	return usage, nil
}
