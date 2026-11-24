package relayconvert

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func claudeStreamChunk(raw string) *dto.ClaudeResponse {
	var resp dto.ClaudeResponse
	if err := kitutil.Unmarshal([]byte(raw), &resp); err != nil {
		panic(err)
	}
	return &resp
}

func chatStreamChunk(raw string) *dto.ChatCompletionsStreamResponse {
	var resp dto.ChatCompletionsStreamResponse
	if err := kitutil.Unmarshal([]byte(raw), &resp); err != nil {
		panic(err)
	}
	return &resp
}

// Gemini promptTokenCount includes cache reads; cachedContentTokenCount must
// report them on every route that builds Gemini usage from chat usage.
func TestConvertResponseToGeminiReportsCachedContentTokens(t *testing.T) {
	const claudeUsage = `{"input_tokens":2054,"cache_creation_input_tokens":0,"cache_read_input_tokens":12981,"output_tokens":42}`
	const chatUsage = `{"prompt_tokens":1000,"completion_tokens":5,"total_tokens":1005,"prompt_tokens_details":{"cached_tokens":800}}`
	tests := []struct {
		name       string
		from       types.RelayFormat
		response   any
		stream     []any
		stateless  any
		wantPrompt int
		wantCached int
	}{
		{
			name:     "claude",
			from:     types.RelayFormatClaude,
			response: claudeStreamChunk(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":` + claudeUsage + `}`),
			stream: []any{
				claudeStreamChunk(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":2054,"cache_creation_input_tokens":0,"cache_read_input_tokens":12981,"output_tokens":1}}}`),
				claudeStreamChunk(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
				claudeStreamChunk(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`),
				claudeStreamChunk(`{"type":"content_block_stop","index":0}`),
				claudeStreamChunk(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":` + claudeUsage + `}`),
				claudeStreamChunk(`{"type":"message_stop"}`),
			},
			wantPrompt: 15035,
			wantCached: 12981,
		},
		{
			name: "chat",
			from: types.RelayFormatOpenAI,
			response: &dto.OpenAITextResponse{
				Choices: []dto.OpenAITextResponseChoice{{Message: dto.Message{Role: "assistant", Content: "hi"}, FinishReason: "stop"}},
				Usage: dto.Usage{
					PromptTokens:        1000,
					CompletionTokens:    5,
					TotalTokens:         1005,
					PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 800},
				},
			},
			stream: []any{
				chatStreamChunk(`{"id":"c1","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`),
				chatStreamChunk(`{"id":"c1","choices":[],"usage":` + chatUsage + `}`),
			},
			stateless:  chatStreamChunk(`{"id":"c1","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}],"usage":` + chatUsage + `}`),
			wantPrompt: 1000,
			wantCached: 800,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &convmeta.Values{}
			result, err := ConvertResponse(nil, info, types.RelayFormatGemini, tt.response)
			require.NoError(t, err)
			gemini, ok := result.Value.(*dto.GeminiChatResponse)
			require.True(t, ok)
			assert.Equal(t, tt.wantPrompt, gemini.UsageMetadata.PromptTokenCount)
			assert.Equal(t, tt.wantCached, gemini.UsageMetadata.CachedContentTokenCount)

			state, err := NewResponseStreamState(tt.from, types.RelayFormatGemini, ResponseStreamOptions{})
			require.NoError(t, err)
			var results []ResponseResult
			for _, chunk := range tt.stream {
				chunkResults, err := ConvertStreamResponseChunk(nil, info, state, chunk)
				require.NoError(t, err)
				results = append(results, chunkResults...)
			}
			finals, err := FinalizeStreamResponse(nil, info, state)
			require.NoError(t, err)
			results = append(results, finals...)
			require.NotEmpty(t, results)
			// Gemini clients read usage from the last chunk.
			last, ok := results[len(results)-1].Value.(*dto.GeminiChatResponse)
			require.True(t, ok)
			assert.Equal(t, tt.wantPrompt, last.UsageMetadata.PromptTokenCount)
			assert.Equal(t, tt.wantCached, last.UsageMetadata.CachedContentTokenCount)

			if tt.stateless == nil {
				return
			}
			result, err = ConvertStreamResponse(nil, info, types.RelayFormatGemini, tt.stateless)
			require.NoError(t, err)
			chunk, ok := result.Value.(*dto.GeminiChatResponse)
			require.True(t, ok)
			assert.Equal(t, tt.wantPrompt, chunk.UsageMetadata.PromptTokenCount)
			assert.Equal(t, tt.wantCached, chunk.UsageMetadata.CachedContentTokenCount)
		})
	}
}
