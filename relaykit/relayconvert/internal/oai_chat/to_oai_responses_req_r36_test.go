package oaichat

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestChatCompletionsRequestToResponsesRequestCacheControls(t *testing.T) {
	for _, tc := range []struct {
		name, options, retention, wantOptions, wantError string
	}{
		{name: "shared options", options: `{"mode":"implicit","ttl":"30m"}`, wantOptions: `{"mode":"implicit","ttl":"30m"}`},
		{name: "legacy retention stays independent", retention: `"24h"`},
		{name: "absent"},
		{name: "null", options: `null`},
		{name: "empty", options: `{}`},
		{name: "explicit without breakpoints", options: `{"mode":"explicit"}`, wantOptions: `{"mode":"explicit"}`},
		{name: "Responses-only TTL", options: `{"ttl":"12h"}`, wantError: "prompt_cache_options.ttl"},
		{name: "Responses-only prewarm", options: `{"prewarm":true}`, wantError: "unsupported field \"prewarm\""},
		{name: "malformed mode", options: `{"mode":true}`, wantError: "invalid prompt_cache_options.mode"},
		{name: "unknown option", options: `{"extra":true}`, wantError: "unsupported field \"extra\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := &dto.GeneralOpenAIRequest{
				Model:                "gpt-test",
				Messages:             []dto.Message{{Role: "user", Content: "stable prompt"}},
				PromptCacheOptions:   json.RawMessage(tc.options),
				PromptCacheRetention: json.RawMessage(tc.retention),
			}
			converted, err := ChatCompletionsRequestToResponsesRequest(context.Background(), request)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				assert.Nil(t, converted)
				return
			}
			require.NoError(t, err)
			encoded, err := kitutil.Marshal(converted)
			require.NoError(t, err)
			for name, expected := range map[string]string{"prompt_cache_options": tc.wantOptions, "prompt_cache_retention": tc.retention} {
				if expected == "" {
					assert.False(t, gjson.GetBytes(encoded, name).Exists(), name)
				} else {
					assert.JSONEq(t, expected, gjson.GetBytes(encoded, name).Raw, name)
				}
			}
		})
	}
}

func TestChatCompletionsRequestToResponsesRequestCacheBreakpoints(t *testing.T) {
	for _, tc := range []struct {
		name, request, wantPath string
	}{
		{
			name:     "implicit mode by omission",
			request:  `{"messages":[{"role":"user","content":[{"type":"text","text":"prefix","prompt_cache_breakpoint":{}}]}]}`,
			wantPath: "messages[0].content[0].prompt_cache_breakpoint",
		},
		{
			name:     "implicit mode with developer instructions",
			request:  `{"prompt_cache_options":{"mode":"implicit"},"messages":[{"role":"developer","content":[{"type":"text","text":"prefix","prompt_cache_breakpoint":{"mode":"explicit"}}]}]}`,
			wantPath: "messages[0].content[0].prompt_cache_breakpoint",
		},
		{
			name:     "explicit mode with a tool message",
			request:  `{"prompt_cache_options":{"mode":"explicit"},"messages":[{"role":"tool","tool_call_id":"call_1","content":[{"type":"text","text":"prefix","prompt_cache_breakpoint":{"ttl":"30m"}}]}]}`,
			wantPath: "messages[0].content[0].prompt_cache_breakpoint",
		},
		{
			name:    "text and function parameter names",
			request: `{"messages":[{"role":"user","content":[{"type":"text","text":"{\"prompt_cache_breakpoint\":{}}"}]}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"prompt_cache_breakpoint":{"type":"string"}}}}}]}`,
		},
		{
			name:     "prediction content array",
			request:  `{"messages":[{"role":"user","content":"hello"}],"prediction":{"type":"content","content":[{"type":"text","text":"prefix","prompt_cache_breakpoint":{}}]}}`,
			wantPath: "prediction.content[0].prompt_cache_breakpoint",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var req dto.GeneralOpenAIRequest
			require.NoError(t, kitutil.Unmarshal([]byte(tc.request), &req))
			req.Model = "gpt-test"
			converted, err := ChatCompletionsRequestToResponsesRequest(context.Background(), &req)
			if tc.wantPath != "" {
				require.ErrorContains(t, err, tc.wantPath)
				assert.Nil(t, converted)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, string(converted.Input), "prompt_cache_breakpoint")
			assert.Contains(t, string(converted.Tools), "prompt_cache_breakpoint")
		})
	}
}
