package relayconvert

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConvertRequestClaudeStructuredOutput verifies schema constraints and property
// order on both Claude conversion paths, with streaming and reasoning effort.
func TestConvertRequestClaudeStructuredOutput(t *testing.T) {
	const schema = `{"type":"object","properties":{"zeta":{"type":"string"},"alpha":{"type":"integer"}},"required":["zeta","alpha"],"additionalProperties":false}`
	for _, from := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses} {
		for _, stream := range []bool{false, true} {
			for _, effort := range []string{"", "low"} {
				t.Run(string(from)+"/stream="+lo.Ternary(stream, "true", "false")+"/effort="+effort, func(t *testing.T) {
					format := `{"type":"json_schema","name":"result","description":"Extract the result","strict":true,"schema":` + schema + `}`
					var request any
					if from == types.RelayFormatOpenAI {
						request = &dto.GeneralOpenAIRequest{
							Model: "claude-opus-4-6", Messages: []dto.Message{{Role: "user", Content: "Extract a result."}},
							MaxTokens: lo.ToPtr(uint(4096)), Stream: &stream, ReasoningEffort: effort,
							ResponseFormat: &dto.ResponseFormat{Type: "json_schema", JsonSchema: json.RawMessage(format)},
						}
					} else {
						responses := &dto.OpenAIResponsesRequest{
							Model: "claude-opus-4-6", Input: json.RawMessage(`"Extract a result."`),
							MaxOutputTokens: lo.ToPtr(uint(4096)), Stream: &stream,
							Text: json.RawMessage(`{"format":` + format + `}`),
						}
						if effort != "" {
							responses.Reasoning = &dto.Reasoning{Effort: effort}
						}
						request = responses
					}

					result, err := ConvertRequest(context.Background(), nil, types.RelayFormatClaude, request)
					require.NoError(t, err)
					converted, ok := result.Value.(*dto.ClaudeRequest)
					require.True(t, ok)
					body, err := kitutil.Marshal(converted)
					require.NoError(t, err)
					outputConfig := kitutil.ObjectMember(body, "output_config")
					require.NotEmpty(t, outputConfig)
					outputFormat := kitutil.ObjectMember(outputConfig, "format")
					assert.JSONEq(t, `{"type":"json_schema","schema":`+schema+`}`, string(outputFormat))
					assert.Equal(t, schema, string(kitutil.ObjectMember(outputFormat, "schema")), "schema property order must survive serialization")
					if effort != "" {
						assert.JSONEq(t, `"low"`, string(kitutil.ObjectMember(outputConfig, "effort")))
					}
					assert.Empty(t, converted.OutputFormat, "use the current output_config.format API")
					assert.Empty(t, result.Diagnostics)
				})
			}
		}
	}
}

// TestConvertRequestClaudeResponseFormatValidation checks that absent and text
// formats stay optional while malformed output constraints fail conversion.
func TestConvertRequestClaudeResponseFormatValidation(t *testing.T) {
	tests := []struct {
		name      string
		format    string
		wantError string
	}{
		{name: "omitted format"},
		{name: "null format", format: `null`},
		{name: "empty format", format: `{}`, wantError: "response format type is required"},
		{name: "missing type with schema", format: `{"schema":{"type":"object","properties":{}}}`, wantError: "response format type is required"},
		{name: "empty type with schema", format: `{"type":"","schema":{"type":"object","properties":{}}}`, wantError: "response format type is required"},
		{name: "text", format: `{"type":"text"}`},
		{name: "json object", format: `{"type":"json_object"}`, wantError: "json_object"},
		{name: "unknown format", format: `{"type":"unknown"}`, wantError: "unknown"},
		{name: "missing schema", format: `{"type":"json_schema","name":"result"}`, wantError: "schema must be a JSON object"},
		{name: "null schema", format: `{"type":"json_schema","schema":null}`, wantError: "schema must be a JSON object"},
		{name: "array schema", format: `{"type":"json_schema","schema":[]}`, wantError: "schema must be a JSON object"},
		{name: "boolean schema", format: `{"type":"json_schema","schema":true}`, wantError: "schema must be a JSON object"},
	}
	for _, from := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses} {
		for _, tt := range tests {
			t.Run(string(from)+"/"+tt.name, func(t *testing.T) {
				var request any
				if from == types.RelayFormatOpenAI {
					chat := &dto.GeneralOpenAIRequest{
						Model: "claude-test", Messages: []dto.Message{{Role: "user", Content: "hello"}}, MaxTokens: lo.ToPtr(uint(1024)),
					}
					if tt.format != "" && tt.format != "null" {
						var format struct {
							Type string `json:"type"`
						}
						require.NoError(t, kitutil.Unmarshal([]byte(tt.format), &format))
						chat.ResponseFormat = &dto.ResponseFormat{Type: format.Type, JsonSchema: json.RawMessage(tt.format)}
					}
					request = chat
				} else {
					responses := &dto.OpenAIResponsesRequest{
						Model: "claude-test", Input: json.RawMessage(`"hello"`), MaxOutputTokens: lo.ToPtr(uint(1024)),
					}
					if tt.format != "" {
						responses.Text = json.RawMessage(`{"format":` + tt.format + `}`)
					}
					request = responses
				}
				result, err := ConvertRequest(context.Background(), nil, types.RelayFormatClaude, request)
				if tt.wantError != "" {
					require.ErrorContains(t, err, tt.wantError)
					assert.True(t, reasoning.IsClientError(err), "invalid response formats must be classified as client errors")
					return
				}
				require.NoError(t, err)
				converted, ok := result.Value.(*dto.ClaudeRequest)
				require.True(t, ok)
				assert.Empty(t, converted.OutputConfig)
			})
		}
	}
	t.Run("openai_responses/invalid text config", func(t *testing.T) {
		request := &dto.OpenAIResponsesRequest{
			Model: "claude-test", Input: json.RawMessage(`"hello"`), MaxOutputTokens: lo.ToPtr(uint(1024)),
			Text: json.RawMessage(`[]`),
		}
		_, err := ConvertRequest(context.Background(), nil, types.RelayFormatClaude, request)
		require.ErrorContains(t, err, "invalid text config")
		assert.True(t, reasoning.IsClientError(err))
	})
}

func TestGeminiToOpenAIChatPreservesTextAlongsideToolCalls(t *testing.T) {
	const text = "I will check the weather."
	call := dto.GeminiPart{FunctionCall: &dto.FunctionCall{
		ID: "call_weather", FunctionName: "get_weather", Arguments: map[string]any{"city": "Paris"},
	}}
	for _, tt := range []struct {
		name      string
		parts     []dto.GeminiPart
		wantText  []string
		wantCalls int
	}{
		{name: "text and call", parts: []dto.GeminiPart{{Text: text}, call}, wantText: []string{text}, wantCalls: 1},
		{name: "text around call", parts: []dto.GeminiPart{{Text: text}, call, {Text: "Please wait."}}, wantText: []string{text, "Please wait."}, wantCalls: 1},
		{name: "call only", parts: []dto.GeminiPart{call}, wantCalls: 1},
		{name: "text only", parts: []dto.GeminiPart{{Text: text}}, wantText: []string{text}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := &dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Role: "model", Parts: tt.parts}}}
			result, err := ConvertRequest(context.Background(), &convmeta.Values{
				UpstreamModelName: "gpt-4o", ChannelMetaAttached: true,
			}, types.RelayFormatOpenAI, request)
			require.NoError(t, err)
			converted, ok := result.Value.(*dto.GeneralOpenAIRequest)
			require.True(t, ok)
			// Verify the JSON sent upstream, including custom DTO serialization.
			body, err := kitutil.Marshal(converted)
			require.NoError(t, err)
			var wire dto.GeneralOpenAIRequest
			require.NoError(t, kitutil.Unmarshal(body, &wire))
			require.Len(t, wire.Messages, 1)
			assistant := wire.Messages[0]
			assert.Equal(t, "assistant", assistant.Role)
			content := assistant.ParseContent()
			require.Len(t, content, len(tt.wantText))
			for i, want := range tt.wantText {
				assert.Equal(t, "text", content[i].Type)
				assert.Equal(t, want, content[i].Text)
			}
			calls := assistant.ParseToolCalls()
			require.Len(t, calls, tt.wantCalls)
			if tt.wantCalls > 0 {
				assert.Equal(t, "call_weather", calls[0].ID)
				assert.Equal(t, "get_weather", calls[0].Function.Name)
				assert.JSONEq(t, `{"city":"Paris"}`, calls[0].Function.Arguments)
			}
		})
	}
}

func TestConvertResponsesToClaudePromptCaching(t *testing.T) {
	enabledInfo := &convmeta.Values{
		Options: &convmeta.Options{
			Claude: convmeta.ClaudeOptions{ResponsesPromptCaching: true},
		},
	}

	t.Run("disabled option omits cache_control", func(t *testing.T) {
		req := &dto.OpenAIResponsesRequest{
			Model:           "claude-sonnet-4-6",
			MaxOutputTokens: lo.ToPtr(uint(1024)),
			PromptCacheKey:  json.RawMessage(`"k1"`),
			Input:           json.RawMessage(`[{"role":"user","content":[{"type":"input_text","text":"cached","prompt_cache_breakpoint":{"mode":"explicit"}}]}]`),
		}
		res, err := ConvertRequest(context.Background(), nil, types.RelayFormatClaude, req)
		require.NoError(t, err)
		out := res.Value.(*dto.ClaudeRequest)
		assert.Empty(t, out.CacheControl)
		blocks := out.Messages[0].Content.([]dto.ClaudeMediaMessage)
		assert.Empty(t, blocks[0].CacheControl)
	})

	t.Run("one-shot without hints does not write cache", func(t *testing.T) {
		req := &dto.OpenAIResponsesRequest{
			Model:           "claude-sonnet-4-6",
			MaxOutputTokens: lo.ToPtr(uint(1024)),
			Input:           json.RawMessage(`"hello"`),
		}
		res, err := ConvertRequest(context.Background(), enabledInfo, types.RelayFormatClaude, req)
		require.NoError(t, err)
		out := res.Value.(*dto.ClaudeRequest)
		assert.Empty(t, out.CacheControl)
	})

	t.Run("cache key enables top-level ephemeral cache", func(t *testing.T) {
		req := &dto.OpenAIResponsesRequest{
			Model:           "claude-sonnet-4-6",
			MaxOutputTokens: lo.ToPtr(uint(1024)),
			PromptCacheKey:  json.RawMessage(`"session-1"`),
			Input:           json.RawMessage(`"hello"`),
		}
		res, err := ConvertRequest(context.Background(), enabledInfo, types.RelayFormatClaude, req)
		require.NoError(t, err)
		out := res.Value.(*dto.ClaudeRequest)
		assert.JSONEq(t, `{"type":"ephemeral"}`, string(out.CacheControl))
	})

	t.Run("explicit breakpoints capped at 4 with warning diagnostic", func(t *testing.T) {
		req := &dto.OpenAIResponsesRequest{
			Model:           "claude-sonnet-4-6",
			MaxOutputTokens: lo.ToPtr(uint(1024)),
			Input: json.RawMessage(`[{"role":"user","content":[
				{"type":"input_text","text":"a","prompt_cache_breakpoint":{"mode":"explicit"}},
				{"type":"input_text","text":"b","prompt_cache_breakpoint":{"mode":"explicit"}},
				{"type":"input_text","text":"c","prompt_cache_breakpoint":{"mode":"explicit"}},
				{"type":"input_text","text":"d","prompt_cache_breakpoint":{"mode":"explicit"}},
				{"type":"input_text","text":"e","prompt_cache_breakpoint":{"mode":"explicit"}}
			]}]`),
		}
		res, err := ConvertRequest(context.Background(), enabledInfo, types.RelayFormatClaude, req)
		require.NoError(t, err)
		out := res.Value.(*dto.ClaudeRequest)
		assert.Empty(t, out.CacheControl)
		blocks := out.Messages[0].Content.([]dto.ClaudeMediaMessage)
		require.Len(t, blocks, 5)
		for i := 0; i < 4; i++ {
			assert.JSONEq(t, `{"type":"ephemeral"}`, string(blocks[i].CacheControl))
		}
		assert.Empty(t, blocks[4].CacheControl)
		require.Len(t, res.Diagnostics, 1)
		assert.Equal(t, "cache_breakpoint_limit", res.Diagnostics[0].Code)
	})
}
