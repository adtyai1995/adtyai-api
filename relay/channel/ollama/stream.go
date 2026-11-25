package ollama

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

type ollamaChatStreamChunk struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	Error     string `json:"error"`
	// chat
	Message *struct {
		Role      string           `json:"role"`
		Content   string           `json:"content"`
		Thinking  json.RawMessage  `json:"thinking"`
		ToolCalls []OllamaToolCall `json:"tool_calls"`
	} `json:"message"`
	// generate
	Response           string `json:"response"`
	Done               bool   `json:"done"`
	DoneReason         string `json:"done_reason"`
	TotalDuration      int64  `json:"total_duration"`
	LoadDuration       int64  `json:"load_duration"`
	PromptEvalCount    int    `json:"prompt_eval_count"`
	EvalCount          int    `json:"eval_count"`
	PromptEvalDuration int64  `json:"prompt_eval_duration"`
	EvalDuration       int64  `json:"eval_duration"`
}

func ollamaToolCallsToOpenAI(toolCalls []OllamaToolCall, startIndex int, includeIndex bool) ([]dto.ToolCallResponse, int) {
	if len(toolCalls) == 0 {
		return nil, startIndex
	}
	result := make([]dto.ToolCallResponse, 0, len(toolCalls))
	for _, tc := range toolCalls {
		var argBytes []byte
		var err error
		if tc.Function.Arguments == nil {
			argBytes = []byte("{}")
		} else {
			argBytes, err = common.Marshal(tc.Function.Arguments)
			if err != nil || len(argBytes) == 0 {
				argBytes = []byte("{}")
			}
		}
		toolCallID := tc.ID
		if toolCallID == "" {
			toolCallID = fmt.Sprintf("call_%d", startIndex)
		}
		tr := dto.ToolCallResponse{
			ID:   toolCallID,
			Type: "function",
			Function: dto.FunctionResponse{
				Name:      tc.Function.Name,
				Arguments: string(argBytes),
			},
		}
		if includeIndex {
			tr.SetIndex(startIndex)
		}
		startIndex++
		result = append(result, tr)
	}
	return result, startIndex
}

func buildOllamaStreamDelta(chunk *ollamaChatStreamChunk, responseID string, created int64, model string, toolCallIndex *int) (dto.ChatCompletionsStreamResponse, bool) {
	delta := dto.ChatCompletionsStreamResponse{
		Id:      responseID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Index: 0,
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Role: "assistant"},
		}},
	}

	var content string
	if chunk.Message != nil {
		content = chunk.Message.Content
	} else {
		content = chunk.Response
	}
	if content != "" {
		delta.Choices[0].Delta.SetContentString(content)
	}

	hasPayload := content != ""
	if chunk.Message != nil && len(chunk.Message.Thinking) > 0 {
		raw := strings.TrimSpace(string(chunk.Message.Thinking))
		if raw != "" && raw != "null" {
			// Unmarshal the JSON string to get the actual content without quotes
			var thinkingContent string
			if err := common.Unmarshal(chunk.Message.Thinking, &thinkingContent); err == nil {
				delta.Choices[0].Delta.SetReasoningContent(thinkingContent)
			} else {
				// Fallback to raw string if it's not a JSON string
				delta.Choices[0].Delta.SetReasoningContent(raw)
			}
			hasPayload = true
		}
	}
	if chunk.Message != nil && len(chunk.Message.ToolCalls) > 0 {
		delta.Choices[0].Delta.ToolCalls, *toolCallIndex = ollamaToolCallsToOpenAI(chunk.Message.ToolCalls, *toolCallIndex, true)
		hasPayload = true
	}

	return delta, hasPayload
}

func toUnix(ts string) int64 {
	if ts == "" {
		return time.Now().Unix()
	}
	// try time.RFC3339 or with nanoseconds
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t2, err2 := time.Parse(time.RFC3339, ts)
		if err2 == nil {
			return t2.Unix()
		}
		return time.Now().Unix()
	}
	return t.Unix()
}

func ollamaStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("empty response"), types.ErrorCodeBadResponse, http.StatusBadRequest)
	}
	defer service.CloseResponseBodyGracefully(resp)
	helper.StopStreamKeepAlive(c)

	scanner := helper.NewStreamScanner(resp.Body)
	usage := &dto.Usage{}
	var model = info.UpstreamModelName
	var responseId = common.GetUUID()
	var created = time.Now().Unix()
	var toolCallIndex int
	var started bool
	var emittedOutput bool
	var responseText strings.Builder
	ensureStarted := func() error {
		if started {
			return nil
		}
		helper.SetEventStreamHeaders(c)
		start := helper.GenerateStartEmptyResponse(responseId, created, model, nil)
		if err := helper.ObjectData(c, start); err != nil {
			return err
		}
		started = true
		if info != nil {
			info.SendResponseCount++
		}
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var chunk ollamaChatStreamChunk
		if err := common.Unmarshal([]byte(line), &chunk); err != nil {
			logger.LogError(c, common.LogText("ollama stream json decode error: %s line=%s", err.Error(), line))
			if emittedOutput {
				// R4-023: Output has already been delivered to the client; settle
				// billed usage instead of returning an error that refunds 100%.
				break
			}
			if started || helper.HasWrittenStreamPayload(c) || (info != nil && info.SendResponseCount > 0) {
				return usage, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
			}
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if chunk.Error != "" {
			if helper.HasWrittenStreamPayload(c) || (info != nil && info.SendResponseCount > 0) {
				logger.LogError(c, common.LogText("ollama stream error after output written, billing delivered usage: %s", chunk.Error))
				break
			}
			return nil, types.NewOpenAIError(errors.New(chunk.Error), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		created = toUnix(chunk.CreatedAt)
		if err := ensureStarted(); err != nil {
			break
		}

		if !chunk.Done {
			delta, hasPayload := buildOllamaStreamDelta(&chunk, responseId, created, model, &toolCallIndex)
			if err := helper.ObjectData(c, delta); err != nil {
				break
			}
			if info != nil {
				info.SendResponseCount++
			}
			if hasPayload {
				emittedOutput = true
				if len(delta.Choices) > 0 {
					if content := delta.Choices[0].Delta.GetContentString(); content != "" {
						responseText.WriteString(content)
					}
					if reasoning := delta.Choices[0].Delta.GetReasoningContent(); reasoning != "" {
						responseText.WriteString(reasoning)
					}
				}
			}
			continue
		}
		// done frame
		if delta, hasPayload := buildOllamaStreamDelta(&chunk, responseId, created, model, &toolCallIndex); hasPayload {
			if err := helper.ObjectData(c, delta); err != nil {
				break
			}
			if info != nil {
				info.SendResponseCount++
			}
			emittedOutput = true
			if len(delta.Choices) > 0 {
				if content := delta.Choices[0].Delta.GetContentString(); content != "" {
					responseText.WriteString(content)
				}
				if reasoning := delta.Choices[0].Delta.GetReasoningContent(); reasoning != "" {
					responseText.WriteString(reasoning)
				}
			}
		}
		// finalize once and break loop
		usage.PromptTokens = max(chunk.PromptEvalCount, 0)
		usage.CompletionTokens = max(chunk.EvalCount, 0)
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		finishReason := chunk.DoneReason
		if finishReason == "" {
			finishReason = "stop"
		}
		if toolCallIndex > 0 {
			finishReason = constant.FinishReasonToolCalls
		}
		// emit stop delta
		if stop := helper.GenerateStopResponse(responseId, created, model, finishReason); stop != nil {
			if err := helper.ObjectData(c, stop); err == nil && info != nil {
				info.SendResponseCount++
			}
		}
		// emit usage frame
		if final := helper.GenerateFinalUsageResponse(responseId, created, model, *usage); final != nil {
			if err := helper.ObjectData(c, final); err == nil && info != nil {
				info.SendResponseCount++
			}
		}
		// send [DONE]
		helper.Done(c)
		break
	}
	scanErr := scanner.Err()
	if scanErr != nil && scanErr != io.EOF {
		logger.LogError(c, common.LogText("ollama stream scan error: %s", scanErr.Error()))
	}
	if !helper.HasWrittenStreamPayload(c) && (info == nil || info.SendResponseCount == 0) {
		if helper.ClientRequestCanceled(c) {
			return nil, types.NewError(c.Request.Context().Err(), types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry())
		}
		if scanErr != nil && scanErr != io.EOF {
			return nil, types.NewOpenAIError(scanErr, types.ErrorCodeReadResponseBodyFailed, http.StatusBadGateway)
		}
		return nil, types.NewOpenAIError(errors.New("empty upstream stream response"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
	}
	if usage.TotalTokens <= 0 && (emittedOutput || responseText.Len() > 0) {
		usage = service.ResponseText2Usage(c, responseText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
	}
	return usage, nil
}

// non-stream handler for chat/generate
func ollamaChatHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("empty response"), types.ErrorCodeBadResponse, http.StatusBadRequest)
	}
	defer service.CloseResponseBodyGracefully(resp)
	body, readErr := helper.ReadResponseBody(c, resp, types.ErrorCodeReadResponseBodyFailed)
	if readErr != nil {
		return nil, readErr
	}
	raw := string(body)
	if common.DebugEnabled {
		println("ollama non-stream raw resp:", raw)
	}

	lines := strings.Split(raw, "\n")
	var (
		aggContent       strings.Builder
		reasoningBuilder strings.Builder
		lastChunk        ollamaChatStreamChunk
		parsedAny        bool
		toolCallIndex    int
		toolCalls        []dto.ToolCallResponse
	)
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var ck ollamaChatStreamChunk
		if err := common.Unmarshal([]byte(ln), &ck); err != nil {
			if len(lines) == 1 {
				return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			}
			continue
		}
		parsedAny = true
		lastChunk = ck
		if ck.Message != nil && len(ck.Message.Thinking) > 0 {
			raw := strings.TrimSpace(string(ck.Message.Thinking))
			if raw != "" && raw != "null" {
				// Unmarshal the JSON string to get the actual content without quotes
				var thinkingContent string
				if err := common.Unmarshal(ck.Message.Thinking, &thinkingContent); err == nil {
					reasoningBuilder.WriteString(thinkingContent)
				} else {
					// Fallback to raw string if it's not a JSON string
					reasoningBuilder.WriteString(raw)
				}
			}
		}
		if ck.Message != nil && ck.Message.Content != "" {
			aggContent.WriteString(ck.Message.Content)
		} else if ck.Response != "" {
			aggContent.WriteString(ck.Response)
		}
		if ck.Message != nil && len(ck.Message.ToolCalls) > 0 {
			var converted []dto.ToolCallResponse
			converted, toolCallIndex = ollamaToolCallsToOpenAI(ck.Message.ToolCalls, toolCallIndex, false)
			toolCalls = append(toolCalls, converted...)
		}
	}

	if !parsedAny {
		var single ollamaChatStreamChunk
		if err := common.Unmarshal(body, &single); err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		lastChunk = single
		if single.Message != nil {
			if len(single.Message.Thinking) > 0 {
				raw := strings.TrimSpace(string(single.Message.Thinking))
				if raw != "" && raw != "null" {
					// Unmarshal the JSON string to get the actual content without quotes
					var thinkingContent string
					if err := common.Unmarshal(single.Message.Thinking, &thinkingContent); err == nil {
						reasoningBuilder.WriteString(thinkingContent)
					} else {
						// Fallback to raw string if it's not a JSON string
						reasoningBuilder.WriteString(raw)
					}
				}
			}
			aggContent.WriteString(single.Message.Content)
			if len(single.Message.ToolCalls) > 0 {
				var converted []dto.ToolCallResponse
				converted, toolCallIndex = ollamaToolCallsToOpenAI(single.Message.ToolCalls, toolCallIndex, false)
				toolCalls = append(toolCalls, converted...)
			}
		} else {
			aggContent.WriteString(single.Response)
		}
	}

	model := lastChunk.Model
	if model == "" {
		model = info.UpstreamModelName
	}
	created := toUnix(lastChunk.CreatedAt)
	promptTokens := max(lastChunk.PromptEvalCount, 0)
	completionTokens := max(lastChunk.EvalCount, 0)
	usage := &dto.Usage{PromptTokens: promptTokens, CompletionTokens: completionTokens, TotalTokens: promptTokens + completionTokens}
	content := aggContent.String()
	if usage.TotalTokens <= 0 && (content != "" || reasoningBuilder.Len() > 0 || len(toolCalls) > 0) {
		usage = service.ResponseText2Usage(c, content+reasoningBuilder.String(), model, info.GetEstimatePromptTokens())
	}
	finishReason := lastChunk.DoneReason
	if finishReason == "" {
		finishReason = "stop"
	}
	if len(toolCalls) > 0 {
		finishReason = constant.FinishReasonToolCalls
	}

	msg := dto.Message{Role: "assistant", Content: contentPtr(content)}
	if len(toolCalls) > 0 {
		if rawToolCalls, err := common.Marshal(toolCalls); err == nil {
			msg.ToolCalls = rawToolCalls
		}
	}
	if rc := reasoningBuilder.String(); rc != "" {
		msg.ReasoningContent = &rc
	}
	full := dto.OpenAITextResponse{
		Id:      common.GetUUID(),
		Model:   model,
		Object:  "chat.completion",
		Created: created,
		Choices: []dto.OpenAITextResponseChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: finishReason,
		}},
		Usage: *usage,
	}
	out, _ := common.Marshal(full)
	service.IOCopyBytesGracefully(c, resp, out)
	return usage, nil
}

func contentPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
