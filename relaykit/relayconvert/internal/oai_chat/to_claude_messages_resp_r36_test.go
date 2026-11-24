package oaichat

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseOpenAI2ClaudeToolUseInputIsObject(t *testing.T) {
	tests := []struct {
		name string
		args string
		want string
	}{
		{name: "large integer", args: `{"id":9007199254740993}`, want: `{"id":9007199254740993}`},
		{name: "nested integer", args: `{"ids":[18446744073709551615]}`, want: `{"ids":[18446744073709551615]}`},
		{name: "precise decimal", args: `{"value":0.123456789012345678901}`, want: `{"value":0.123456789012345678901}`},
		{name: "object", args: `{"q":"x"}`, want: `{"q":"x"}`},
		{name: "empty", args: "", want: `{}`},
		{name: "invalid", args: "{", want: `{}`},
		{name: "null", args: "null", want: `{}`},
		{name: "array", args: `["x"]`, want: `{}`},
		{name: "string", args: `"x"`, want: `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := dto.Message{Role: "assistant"}
			msg.SetToolCalls([]dto.ToolCallResponse{{
				ID:   "call_1",
				Type: "function",
				Function: dto.FunctionResponse{
					Name:      "search",
					Arguments: tt.args,
				},
			}})
			resp := ResponseOpenAI2Claude(&dto.OpenAITextResponse{
				Id:    "chatcmpl_1",
				Model: "claude-test",
				Choices: []dto.OpenAITextResponseChoice{{
					Message:      msg,
					FinishReason: "tool_calls",
				}},
			}, &convmeta.Values{})

			require.Len(t, resp.Content, 1)
			assert.Equal(t, "tool_use", resp.Content[0].Type)
			encoded, err := kitutil.Marshal(resp.Content[0].Input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(encoded))
		})
	}
}
