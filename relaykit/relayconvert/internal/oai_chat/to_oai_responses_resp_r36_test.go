package oaichat

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsageFromChatUsagePreservesCacheCreationInputTokensAlone(t *testing.T) {
	usage := dto.Usage{PromptTokens: 12, TotalTokens: 12}
	usage.PromptTokensDetails.CacheCreationInputTokens = 9

	converted := UsageFromChatUsage(&usage)

	require.NotNil(t, converted.InputTokensDetails)
	assert.Equal(t, 9, converted.InputTokensDetails.CacheCreationInputTokens)
}
