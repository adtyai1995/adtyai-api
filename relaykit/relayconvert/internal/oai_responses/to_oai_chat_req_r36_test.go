package oairesponses

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func mustRawMessage(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := kitutil.Marshal(v)
	require.NoError(t, err)
	return raw
}

func TestResponsesRequestToChatCompletionsRequestInstructionsAndScalarInput(t *testing.T) {
	parallelToolCalls := true
	got, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
		Model:                "gpt-test",
		Instructions:         mustRawMessage(t, "be helpful"),
		Input:                mustRawMessage(t, "hello"),
		TopP:                 lo.ToPtr(0.9),
		User:                 mustRawMessage(t, "user-1"),
		Store:                mustRawMessage(t, false),
		Metadata:             mustRawMessage(t, map[string]any{"trace": "abc"}),
		ParallelToolCalls:    mustRawMessage(t, parallelToolCalls),
		PromptCacheKey:       mustRawMessage(t, "cache-key"),
		PromptCacheOptions:   mustRawMessage(t, map[string]any{"mode": "implicit", "ttl": "30m", "prewarm": false}),
		PromptCacheRetention: mustRawMessage(t, "24h"),
		Reasoning:            &dto.Reasoning{Effort: "medium"},
	})
	require.NoError(t, err)
	require.Len(t, got.Messages, 2)
	assert.Equal(t, "system", got.Messages[0].Role)
	assert.Equal(t, "be helpful", got.Messages[0].StringContent())
	assert.Equal(t, "user", got.Messages[1].Role)
	assert.Equal(t, "hello", got.Messages[1].StringContent())
	assert.Equal(t, 0.9, lo.FromPtr(got.TopP))
	assert.True(t, lo.FromPtr(got.ParallelTooCalls))
	assert.Equal(t, "cache-key", got.PromptCacheKey)
	encoded, err := kitutil.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, `{"mode":"implicit","ttl":"30m"}`, gjson.GetBytes(encoded, "prompt_cache_options").Raw)
	assert.JSONEq(t, `"24h"`, string(got.PromptCacheRetention))
	assert.Equal(t, "medium", got.ReasoningEffort)
	assert.Equal(t, `"user-1"`, string(got.User))
	assert.Equal(t, `false`, string(got.Store))
	assert.Equal(t, "abc", gjson.GetBytes(got.Metadata, "trace").String())
}

func TestResponsesRequestToChatCompletionsRequestCacheControls(t *testing.T) {
	for _, tc := range []struct {
		name, options, wantOptions, wantError string
	}{
		{name: "absent"},
		{name: "no-op Responses fields", options: `{"prewarm":false,"comparison_response_id":null}`},
		{name: "prewarm cannot become generation", options: `{"prewarm":true}`, wantError: "does not support prompt_cache_options.prewarm"},
		{name: "comparison needs Responses diagnostics", options: `{"comparison_response_id":"resp_123"}`, wantError: "does not support prompt_cache_options.comparison_response_id"},
		{name: "TTL cannot be shortened", options: `{"ttl":"12h"}`, wantError: "does not support prompt_cache_options.ttl=12h"},
		{name: "explicit without breakpoints", options: `{"mode":"explicit"}`, wantOptions: `{"mode":"explicit"}`},
		{name: "malformed prewarm", options: `{"prewarm":"false"}`, wantError: "invalid prompt_cache_options.prewarm"},
		{name: "non-object", options: `[]`, wantError: "invalid prompt_cache_options"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			converted, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
				Model:              "gpt-test",
				Input:              json.RawMessage(`"hello"`),
				PromptCacheOptions: json.RawMessage(tc.options),
			})
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				assert.Nil(t, converted)
				return
			}
			require.NoError(t, err)
			encoded, err := kitutil.Marshal(converted)
			require.NoError(t, err)
			options := gjson.GetBytes(encoded, "prompt_cache_options")
			if tc.wantOptions == "" {
				assert.False(t, options.Exists())
			} else {
				assert.JSONEq(t, tc.wantOptions, options.Raw)
			}
		})
	}
}

func TestResponsesRequestToChatCompletionsRequestCacheBreakpoints(t *testing.T) {
	for _, tc := range []struct {
		name, request, wantPath string
	}{
		{
			name:     "implicit mode by omission",
			request:  `{"input":[{"role":"user","content":[{"type":"input_text","text":"prefix","prompt_cache_breakpoint":{}}]}]}`,
			wantPath: "input[0].content[0].prompt_cache_breakpoint",
		},
		{
			name:     "implicit mode with instruction blocks",
			request:  `{"prompt_cache_options":{"mode":"implicit"},"input":"hello","instructions":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"prefix","prompt_cache_breakpoint":{}}]}]}`,
			wantPath: "instructions[0].content[0].prompt_cache_breakpoint",
		},
		{
			name:     "explicit mode with a function output block",
			request:  `{"prompt_cache_options":{"mode":"explicit"},"input":[{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_text","text":"prefix","prompt_cache_breakpoint":{"mode":"explicit"}}]}]}`,
			wantPath: "input[0].output[0].prompt_cache_breakpoint",
		},
		{
			name:     "custom tool output image block",
			request:  `{"input":[{"type":"custom_tool_call_output","call_id":"call_1","output":[{"type":"input_image","image_url":"https://example.test/a.png","prompt_cache_breakpoint":{"ttl":"30m"}}]}]}`,
			wantPath: "input[0].output[0].prompt_cache_breakpoint",
		},
		{
			name:    "text and function parameter names",
			request: `{"instructions":"{\"prompt_cache_breakpoint\":{}}","input":"prompt_cache_breakpoint","tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"prompt_cache_breakpoint":{"type":"string"}}}}]}`,
		},
		{
			name:    "arbitrary JSON tool output",
			request: `{"input":[{"type":"function_call_output","call_id":"call_1","output":[{"type":"record","prompt_cache_breakpoint":{}}]}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var req dto.OpenAIResponsesRequest
			require.NoError(t, kitutil.Unmarshal([]byte(tc.request), &req))
			req.Model = "gpt-test"
			converted, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &req)
			if tc.wantPath != "" {
				require.ErrorContains(t, err, tc.wantPath)
				assert.Nil(t, converted)
				return
			}
			require.NoError(t, err)
			require.NotEmpty(t, converted.Messages)
			assert.Contains(t, converted.Messages[0].StringContent(), "prompt_cache_breakpoint")
			if len(req.Tools) > 0 {
				tools, err := kitutil.Marshal(converted.Tools)
				require.NoError(t, err)
				assert.Contains(t, string(tools), "prompt_cache_breakpoint")
			}
		})
	}
}
