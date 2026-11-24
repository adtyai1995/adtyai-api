package oaichat

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIChatRequestToClaudeMessagesStopSequences(t *testing.T) {
	tests := []struct {
		name string
		stop any
		want []string
	}{
		{name: "string", stop: "END", want: []string{"END"}},
		{name: "empty string", stop: "", want: []string{""}},
		{name: "string slice", stop: []string{"END", ""}, want: []string{"END", ""}},
		{name: "mixed slice", stop: []any{"END", 1, nil, "", "STOP"}, want: []string{"END", "", "STOP"}},
		{name: "non-string slice", stop: []any{1, nil}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			maxTokens := uint(16)
			got, err := OpenAIChatRequestToClaudeMessages(context.Background(), nil, dto.GeneralOpenAIRequest{
				Model:     "claude-test",
				MaxTokens: &maxTokens,
				Messages:  []dto.Message{{Role: "user", Content: "hello"}},
				Stop:      tt.stop,
			})
			require.NoError(t, err)
			require.NotNil(t, got)
			if len(tt.want) == 0 {
				assert.Empty(t, got.StopSequences)
				return
			}
			assert.Equal(t, tt.want, got.StopSequences)
		})
	}
}
