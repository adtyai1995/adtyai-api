package claudemessages

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeThinkingTokensMapToOpenAIReasoningTokens(t *testing.T) {
	t.Run("non-stream api usage", func(t *testing.T) {
		usage := UsageFromClaudeAPIUsage(&dto.ClaudeUsage{
			InputTokens:         4,
			OutputTokens:        120,
			OutputTokensDetails: &dto.ClaudeOutputTokensDetails{ThinkingTokens: 40},
		})
		assert.Equal(t, 120, usage.CompletionTokens)
		assert.Equal(t, 40, usage.CompletionTokenDetails.ReasoningTokens)
		require.NotNil(t, usage.BillingUsage)
		require.NotNil(t, usage.BillingUsage.ClaudeUsage)
		require.NotNil(t, usage.BillingUsage.ClaudeUsage.OutputTokensDetails)
		assert.Equal(t, 40, usage.BillingUsage.ClaudeUsage.OutputTokensDetails.ThinkingTokens)
	})

	t.Run("stream message_start and message_delta", func(t *testing.T) {
		claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}
		require.True(t, FormatClaudeResponseInfo(&dto.ClaudeResponse{
			Type: "message_start",
			Message: &dto.ClaudeMediaMessage{
				Id:    "msg_1",
				Model: "claude-opus-5-5",
				Usage: &dto.ClaudeUsage{
					InputTokens:         4,
					OutputTokens:        10,
					OutputTokensDetails: &dto.ClaudeOutputTokensDetails{ThinkingTokens: 6},
				},
			},
		}, nil, claudeInfo))
		assert.Equal(t, 6, claudeInfo.Usage.CompletionTokenDetails.ReasoningTokens)

		require.True(t, FormatClaudeResponseInfo(&dto.ClaudeResponse{
			Type: "message_delta",
			Usage: &dto.ClaudeUsage{
				OutputTokens:        300,
				OutputTokensDetails: &dto.ClaudeOutputTokensDetails{ThinkingTokens: 75},
			},
		}, nil, claudeInfo))
		usage := buildOpenAIStyleUsageFromClaudeUsage(claudeInfo.Usage)
		assert.Equal(t, 300, usage.CompletionTokens)
		assert.Equal(t, 75, usage.CompletionTokenDetails.ReasoningTokens)
	})

	t.Run("stream message_delta reported zero replaces, absent keeps", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			details *dto.ClaudeOutputTokensDetails
			want    int
		}{
			{name: "explicit zero", details: &dto.ClaudeOutputTokensDetails{ThinkingTokens: 0}, want: 0},
			{name: "absent", details: nil, want: 40},
		} {
			claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{PromptTokens: 4, CompletionTokens: 50}}
			claudeInfo.Usage.CompletionTokenDetails.ReasoningTokens = 40
			require.True(t, FormatClaudeResponseInfo(&dto.ClaudeResponse{
				Type:  "message_delta",
				Usage: &dto.ClaudeUsage{OutputTokens: 120, OutputTokensDetails: tc.details},
			}, nil, claudeInfo))
			assert.Equal(t, tc.want, claudeInfo.Usage.CompletionTokenDetails.ReasoningTokens, tc.name)
		}
	})

	t.Run("bounded by output_tokens", func(t *testing.T) {
		for _, tc := range []struct{ thinking, output, want int }{
			{thinking: 500, output: 100, want: 100},
			{thinking: -5, output: 100, want: 0},
		} {
			usage := UsageFromClaudeAPIUsage(&dto.ClaudeUsage{
				InputTokens:         1,
				OutputTokens:        tc.output,
				OutputTokensDetails: &dto.ClaudeOutputTokensDetails{ThinkingTokens: tc.thinking},
			})
			assert.Equal(t, tc.want, usage.CompletionTokenDetails.ReasoningTokens)
		}
	})
}
