package openai

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

func OaiResponsesCompactionHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, apiErr := helper.ReadResponseBody(c, resp, types.ErrorCodeReadResponseBodyFailed)
	if apiErr != nil {
		return nil, apiErr
	}

	var compactResp dto.OpenAIResponsesCompactionResponse
	if err := common.Unmarshal(responseBody, &compactResp); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := compactResp.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)

	usage := dto.Usage{}
	if compactResp.Usage != nil {
		usage.PromptTokens = max(compactResp.Usage.InputTokens, 0)
		usage.CompletionTokens = max(compactResp.Usage.OutputTokens, 0)
		usage.TotalTokens = max(compactResp.Usage.TotalTokens, 0)
		if compactResp.Usage.InputTokensDetails != nil {
			usage.PromptTokensDetails.CachedTokens = max(compactResp.Usage.InputTokensDetails.CachedTokens, 0)
			usage.PromptTokensDetails.CacheWriteTokens = max(compactResp.Usage.InputTokensDetails.CacheWriteTokens, 0)
			usage.PromptTokensDetails.CacheWriteTokensDetails = compactResp.Usage.InputTokensDetails.CacheWriteTokensDetails
		}
	}
	clampNonNegativeUsage(&usage)

	// Upstream may omit usage, send it as null, or report negative tokens: the compacted
	// body has already been delivered, so settle the input instead of a zero bill (R10-015).
	if usage.PromptTokens <= 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}
	if usage.TotalTokens < usage.PromptTokens+usage.CompletionTokens {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	return &usage, nil
}
