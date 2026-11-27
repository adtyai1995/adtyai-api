package relay

import (
	"fmt"
	"net/http"

	openaichannel "github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func ResponsesHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	var responsesReq *dto.OpenAIResponsesRequest
	switch req := info.Request.(type) {
	case *dto.OpenAIResponsesRequest:
		responsesReq = req
	case *dto.OpenAIResponsesCompactionRequest:
		// Only fields documented for POST /v1/responses/compact are forwarded:
		// model, input, instructions, previous_response_id, prompt_cache_key,
		// prompt_cache_options, prompt_cache_retention, service_tier.
		// Undocumented Codex-parity fields (tools, reasoning, text) are parsed
		// for client compatibility but intentionally not sent upstream.
		responsesReq = &dto.OpenAIResponsesRequest{
			Model:                req.Model,
			Input:                req.Input,
			Instructions:         req.Instructions,
			PreviousResponseID:   req.PreviousResponseID,
			ParallelToolCalls:    req.ParallelToolCalls,
			ServiceTier:          req.ServiceTier,
			PromptCacheKey:       req.PromptCacheKey,
			PromptCacheOptions:   req.PromptCacheOptions,
			PromptCacheRetention: req.PromptCacheRetention,
		}
	default:
		return types.NewErrorWithStatusCode(
			fmt.Errorf("invalid request type, expected dto.OpenAIResponsesRequest or dto.OpenAIResponsesCompactionRequest, got %T", info.Request),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	adaptor, requestBody, closer, apiErr := PrepareResponsesRequest(c, info, responsesReq)
	if apiErr != nil {
		return apiErr
	}
	defer closer.Close()

	var httpResp *http.Response
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")

	if resp != nil {
		var ok bool
		httpResp, ok = resp.(*http.Response)
		if !ok {
			return types.NewOpenAIError(fmt.Errorf("unexpected response type %T", resp), types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
		}

		if httpResp != nil && httpResp.StatusCode != http.StatusOK {
			newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
			// reset status code 重置状态码
			service.ResetStatusCode(newAPIError, statusCodeMappingStr)
			return newAPIError
		}
	}

	var usage any
	if !info.IsStream &&
		info.RelayMode == relayconstant.RelayModeResponses &&
		httpResp != nil && httpResp.StatusCode >= 200 && httpResp.StatusCode < 300 &&
		isResponsesEventStreamContentType(httpResp.Header.Get("Content-Type")) &&
		isOpenAIResponsesUpstream(info) {
		usage, newAPIError = openaichannel.OaiResponsesBufferedStreamHandler(c, info, httpResp)
	} else {
		usage, newAPIError = adaptor.DoResponse(c, httpResp, info)
	}
	if newAPIError != nil {
		// reset status code 重置状态码
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}

	usageDto, ok := usage.(*dto.Usage)
	if !ok || usageDto == nil {
		return types.NewError(fmt.Errorf("unexpected usage type %T", usage), types.ErrorCodeBadResponseBody)
	}

	return ConsumeResponsesQuota(c, info, usageDto)
}

func isOpenAIResponsesUpstream(info *relaycommon.RelayInfo) bool {
	if info == nil || len(info.RequestConversionChain) == 0 {
		return false
	}
	return info.RequestConversionChain[len(info.RequestConversionChain)-1] == types.RelayFormatOpenAIResponses
}

func hasAudioTokenDetails(usage *dto.Usage) bool {
	if usage == nil {
		return false
	}
	if usage.PromptTokensDetails.AudioTokens > 0 ||
		usage.CompletionTokenDetails.AudioTokens > 0 ||
		(usage.InputTokensDetails != nil && usage.InputTokensDetails.AudioTokens > 0) ||
		(usage.OutputTokensDetails != nil && usage.OutputTokensDetails.AudioTokens > 0) {
		return true
	}
	if usage.BillingUsage != nil {
		if canonical, ok := usage.BillingUsage.CanonicalUsage(); ok && canonical != nil {
			return canonical.PromptTokensDetails.AudioTokens > 0 ||
				canonical.CompletionTokenDetails.AudioTokens > 0 ||
				(canonical.InputTokensDetails != nil && canonical.InputTokensDetails.AudioTokens > 0) ||
				(canonical.OutputTokensDetails != nil && canonical.OutputTokensDetails.AudioTokens > 0)
		}
	}
	return false
}

func shouldConsumeAudioQuota(info *relaycommon.RelayInfo, usage *dto.Usage) bool {
	if info == nil || !hasAudioTokenDetails(usage) {
		return false
	}
	billingModelName := info.GetBillingModelName()
	return ratio_setting.ContainsAudioRatio(billingModelName) || ratio_setting.ContainsAudioCompletionRatio(billingModelName)
}

// ConsumeResponsesQuota applies the same settlement dispatch to HTTP and
// WebSocket Responses usage (including Responses Compact, which reuses the
// pricing resolved before upstream dispatch).
// A non-nil error means the wallet adjustment did not commit and the pre-charge
// must be refunded by the caller.
func ConsumeResponsesQuota(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) *types.NewAPIError {
	var err error
	if shouldConsumeAudioQuota(info, usage) {
		err = service.PostAudioConsumeQuota(c, info, usage, nil)
	} else {
		err = service.PostTextConsumeQuota(c, info, usage, nil)
	}
	if err != nil {
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
	return nil
}
