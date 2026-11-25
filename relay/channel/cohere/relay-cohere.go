package cohere

import (
	"encoding/base64"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func requestOpenAI2Cohere(textRequest dto.GeneralOpenAIRequest) *CohereRequest {
	cohereReq := CohereRequest{
		Model:       textRequest.Model,
		ChatHistory: []ChatHistory{},
		Message:     "",
		Stream:      lo.FromPtrOr(textRequest.Stream, false),
		MaxTokens:   textRequest.GetMaxTokens(),
	}
	if common.CohereSafetySetting != "NONE" {
		cohereReq.SafetyMode = common.CohereSafetySetting
	}
	if cohereReq.MaxTokens == 0 {
		cohereReq.MaxTokens = 4000
	}
	for _, msg := range textRequest.Messages {
		if msg.Role == "user" {
			cohereReq.Message = msg.StringContent()
		} else {
			var role string
			if msg.Role == "assistant" {
				role = "CHATBOT"
			} else if msg.Role == "system" {
				role = "SYSTEM"
			} else {
				role = "USER"
			}
			cohereReq.ChatHistory = append(cohereReq.ChatHistory, ChatHistory{
				Role:    role,
				Message: msg.StringContent(),
			})
		}
	}

	return &cohereReq
}

func requestConvertRerank2Cohere(rerankRequest dto.RerankRequest) *CohereRerankRequest {
	topN := lo.FromPtrOr(rerankRequest.TopN, 1)
	if topN <= 0 {
		topN = 1
	}
	cohereReq := CohereRerankRequest{
		Query:           rerankRequest.Query,
		Documents:       rerankRequest.Documents,
		Model:           rerankRequest.Model,
		TopN:            topN,
		ReturnDocuments: true,
	}
	return &cohereReq
}

func stopReasonCohere2OpenAI(reason string) string {
	switch reason {
	case "COMPLETE":
		return "stop"
	case "MAX_TOKENS":
		return "max_tokens"
	default:
		return reason
	}
}

func cohereStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	helper.StopStreamKeepAlive(c)
	responseId := helper.GetResponseID(c)
	createdTime := common.GetTimestamp()
	usage := &dto.Usage{}
	responseText := ""
	scanner := helper.NewStreamScanner(resp.Body)
	scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := strings.Index(string(data), "\n"); i >= 0 {
			return i + 1, data[0:i], nil
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	dataChan := make(chan string)
	stopChan := make(chan error, 1)
	done := make(chan struct{})
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for scanner.Scan() {
			data := scanner.Text()
			select {
			case dataChan <- data:
			case <-done:
				return
			}
		}
		err := scanner.Err()
		if err != nil {
			common.SysLog(common.LogText("error reading stream: %s", err.Error()))
		}
		select {
		case stopChan <- err:
		case <-done:
		}
	}()
	helper.SetEventStreamHeaders(c)
	isFirst := true
	var scanErr error
	var lastParseErr error
streamLoop:
	for {
		select {
		case <-c.Request.Context().Done():
			break streamLoop
		case data := <-dataChan:
			if isFirst {
				isFirst = false
				info.FirstResponseTime = time.Now()
			}
			data = strings.TrimSuffix(data, "\r")
			var cohereResp CohereResponse
			err := common.Unmarshal([]byte(data), &cohereResp)
			if err != nil {
				lastParseErr = err
				common.SysLog(common.LogText("error unmarshalling stream response: %s", err.Error()))
				continue
			}
			var openaiResp dto.ChatCompletionsStreamResponse
			openaiResp.Id = responseId
			openaiResp.Created = createdTime
			openaiResp.Object = "chat.completion.chunk"
			openaiResp.Model = info.UpstreamModelName
			if cohereResp.IsFinished {
				finishReason := stopReasonCohere2OpenAI(cohereResp.FinishReason)
				openaiResp.Choices = []dto.ChatCompletionsStreamResponseChoice{
					{
						Delta:        dto.ChatCompletionsStreamResponseChoiceDelta{},
						Index:        0,
						FinishReason: &finishReason,
					},
				}
			} else {
				openaiResp.Choices = []dto.ChatCompletionsStreamResponseChoice{
					{
						Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
							Role:    "assistant",
							Content: &cohereResp.Text,
						},
						Index: 0,
					},
				}
			}
			if err := helper.ObjectData(c, openaiResp); err != nil {
				common.SysLog(common.LogText("error sending stream response: %s", err.Error()))
				break streamLoop
			}
			if cohereResp.IsFinished {
				if cohereResp.Response != nil {
					usage.PromptTokens = max(cohereResp.Response.Meta.BilledUnits.InputTokens, 0)
					usage.CompletionTokens = max(cohereResp.Response.Meta.BilledUnits.OutputTokens, 0)
					usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
				}
			} else {
				responseText += cohereResp.Text
			}
			if info != nil {
				info.SendResponseCount++
			}
		case err := <-stopChan:
			scanErr = err
			break streamLoop
		}
	}
	close(done)
	service.CloseResponseBodyGracefully(resp)
	<-workerDone

	if !helper.HasWrittenStreamPayload(c) && (info == nil || info.SendResponseCount == 0) {
		if helper.ClientRequestCanceled(c) {
			return nil, types.NewError(c.Request.Context().Err(), types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry())
		}
		if scanErr != nil {
			return nil, types.NewOpenAIError(scanErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if lastParseErr != nil {
			return nil, types.NewOpenAIError(lastParseErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		return nil, types.NewOpenAIError(errors.New("empty upstream stream response"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
	}
	if scanErr == nil {
		helper.Done(c)
	}
	if usage.PromptTokens <= 0 && usage.CompletionTokens <= 0 && usage.TotalTokens <= 0 {
		usage = service.ResponseText2Usage(c, responseText, info.UpstreamModelName, info.GetEstimatePromptTokens())
	} else if usage.TotalTokens <= 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage, nil
}

func cohereHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp != nil {
		defer service.CloseResponseBodyGracefully(resp)
	}
	createdTime := common.GetTimestamp()
	responseBody, apiErr := helper.ReadResponseBody(c, resp, types.ErrorCodeBadResponseBody)
	if apiErr != nil {
		return nil, apiErr
	}
	var cohereResp CohereResponseResult
	err := common.Unmarshal(responseBody, &cohereResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	usage := dto.Usage{}
	usage.PromptTokens = max(cohereResp.Meta.BilledUnits.InputTokens, 0)
	usage.CompletionTokens = max(cohereResp.Meta.BilledUnits.OutputTokens, 0)
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	if usage.TotalTokens <= 0 && strings.TrimSpace(cohereResp.Text) != "" {
		if est := service.ResponseText2Usage(c, cohereResp.Text, info.UpstreamModelName, info.GetEstimatePromptTokens()); est != nil {
			usage = *est
		}
	}

	var openaiResp dto.TextResponse
	openaiResp.Id = cohereResp.ResponseId
	openaiResp.Created = createdTime
	openaiResp.Object = "chat.completion"
	openaiResp.Model = info.UpstreamModelName
	openaiResp.Usage = usage

	openaiResp.Choices = []dto.OpenAITextResponseChoice{
		{
			Index:        0,
			Message:      dto.Message{Content: cohereResp.Text, Role: "assistant"},
			FinishReason: stopReasonCohere2OpenAI(cohereResp.FinishReason),
		},
	}

	jsonResponse, err := common.Marshal(openaiResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = c.Writer.Write(jsonResponse)
	return &usage, nil
}

func cohereRerankHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	if resp != nil {
		defer service.CloseResponseBodyGracefully(resp)
	}
	responseBody, apiErr := helper.ReadResponseBody(c, resp, types.ErrorCodeBadResponseBody)
	if apiErr != nil {
		return nil, apiErr
	}
	var cohereResp CohereRerankResponseResult
	err := common.Unmarshal(responseBody, &cohereResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	usage := dto.Usage{}
	inTokens := max(cohereResp.Meta.BilledUnits.InputTokens, 0)
	outTokens := max(cohereResp.Meta.BilledUnits.OutputTokens, 0)
	if inTokens <= 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
		usage.CompletionTokens = 0
		usage.TotalTokens = info.GetEstimatePromptTokens()
	} else {
		usage.PromptTokens = inTokens
		usage.CompletionTokens = outTokens
		usage.TotalTokens = inTokens + outTokens
	}

	var rerankResp dto.RerankResponse
	rerankResp.Results = cohereResp.Results
	rerankResp.Usage = usage

	jsonResponse, err := common.Marshal(rerankResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = c.Writer.Write(jsonResponse)
	return &usage, nil
}

func cohereEmbeddingHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	if resp != nil {
		defer service.CloseResponseBodyGracefully(resp)
	}
	responseBody, apiErr := helper.ReadResponseBody(c, resp, types.ErrorCodeBadResponseBody)
	if apiErr != nil {
		return nil, apiErr
	}
	var response CohereEmbeddingResponse
	if err := common.Unmarshal(responseBody, &response); err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	data := make([]map[string]any, 0, len(response.Embeddings.Float)+len(response.Embeddings.Base64))
	for index, embedding := range response.Embeddings.Float {
		if len(embedding) == 0 {
			return nil, types.NewError(errors.New("Cohere response contains an empty embedding"), types.ErrorCodeBadResponseBody)
		}
		values := make([]float64, len(embedding))
		for i, value := range embedding {
			if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
				return nil, types.NewError(errors.New("Cohere response contains an invalid embedding value"), types.ErrorCodeBadResponseBody)
			}
			values[i] = *value
		}
		data = append(data, map[string]any{"object": "embedding", "index": index, "embedding": values})
	}
	for index, embedding := range response.Embeddings.Base64 {
		decoded, err := base64.StdEncoding.DecodeString(embedding)
		if err != nil || len(decoded) == 0 {
			return nil, types.NewError(errors.New("Cohere response contains an invalid base64 embedding"), types.ErrorCodeBadResponseBody)
		}
		data = append(data, map[string]any{"object": "embedding", "index": index, "embedding": embedding})
	}
	if len(data) == 0 {
		return nil, types.NewError(errors.New("Cohere response contains no embeddings"), types.ErrorCodeBadResponseBody)
	}
	for _, reported := range []*float64{response.Meta.BilledUnits.InputTokens, response.Meta.Tokens.InputTokens} {
		if reported != nil && (math.IsNaN(*reported) || math.IsInf(*reported, 0) || *reported < 0 || *reported > common.MaxQuota || math.Trunc(*reported) != *reported) {
			return nil, types.NewError(errors.New("Cohere response contains an invalid input token count"), types.ErrorCodeBadResponseBody)
		}
	}
	inputTokens := info.GetEstimatePromptTokens()
	var clamp *common.QuotaClamp
	if response.Meta.BilledUnits.InputTokens != nil && *response.Meta.BilledUnits.InputTokens > 0 {
		inputTokens, clamp = common.QuotaFromFloatChecked(*response.Meta.BilledUnits.InputTokens)
	} else if response.Meta.Tokens.InputTokens != nil && *response.Meta.Tokens.InputTokens > 0 {
		inputTokens, clamp = common.QuotaFromFloatChecked(*response.Meta.Tokens.InputTokens)
	}
	if clamp != nil && info.QuotaClamp == nil {
		info.QuotaClamp = clamp
	}
	if inputTokens < 0 || inputTokens > common.MaxQuota {
		return nil, types.NewError(errors.New("Cohere embedding token estimate is out of range"), types.ErrorCodeBadResponseBody)
	}
	usage := &dto.Usage{PromptTokens: inputTokens, TotalTokens: inputTokens}
	jsonResponse, err := common.Marshal(map[string]any{"object": "list", "model": info.UpstreamModelName, "data": data, "usage": usage})
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Data(resp.StatusCode, "application/json", jsonResponse)
	return usage, nil
}
