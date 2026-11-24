package dto

import (
	"testing"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInputTokenDetailsCacheCreationInputTokens(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		raw   string
		total int
	}{
		{
			name:  "scalar only",
			raw:   `{"cached_tokens":1200,"cache_creation_input_tokens":300}`,
			total: 300,
		},
		{
			name:  "scalar larger than cache write",
			raw:   `{"cache_write_tokens":250,"cache_creation_input_tokens":400}`,
			total: 400,
		},
		{
			name:  "cache write larger than scalar",
			raw:   `{"cache_write_tokens":250,"cache_creation_input_tokens":100}`,
			total: 250,
		},
		{
			name:  "scalar larger than cached creation",
			raw:   `{"cached_creation_tokens":300,"cache_creation_input_tokens":400}`,
			total: 400,
		},
		{
			name:  "negative scalar ignored beside cache write",
			raw:   `{"cache_write_tokens":80,"cache_creation_input_tokens":-1}`,
			total: 80,
		},
		{
			name:  "only negative scalar",
			raw:   `{"cache_creation_input_tokens":-5}`,
			total: 0,
		},
		{
			name:  "undocumented creation object ignored",
			raw:   `{"cached_tokens":1200,"cache_creation":{"ephemeral_5m_input_tokens":300}}`,
			total: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var details InputTokenDetails
			require.NoError(t, kitutil.Unmarshal([]byte(tc.raw), &details))
			assert.Equal(t, tc.total, details.CacheCreationTokensTotal())
		})
	}

	var withCreation Usage
	require.NoError(t, kitutil.Unmarshal([]byte(`{"prompt_tokens":1510,"completion_tokens":20,"total_tokens":1530,"prompt_tokens_details":{"cached_tokens":1200,"cache_creation_input_tokens":300,"cache_creation":{"ephemeral_5m_input_tokens":300}}}`), &withCreation))
	assert.Equal(t, 1510, withCreation.PromptTokens)
	assert.Equal(t, 1200, withCreation.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 300, withCreation.PromptTokensDetails.CacheCreationInputTokens)
	assert.Equal(t, 300, withCreation.PromptTokensDetails.CacheCreationTokensTotal())
	assert.Zero(t, withCreation.PromptTokensDetails.CachedCreationTokens)
	assert.Zero(t, withCreation.PromptTokensDetails.CacheWriteTokens)
	assert.Zero(t, withCreation.ClaudeCacheCreation5mTokens)
	assert.Zero(t, withCreation.ClaudeCacheCreation1hTokens)

	encoded, err := kitutil.Marshal(withCreation)
	require.NoError(t, err)
	body := string(encoded)
	assert.Contains(t, body, `"cache_creation_input_tokens":300`)
	assert.NotContains(t, body, `"cache_creation":`)
	assert.NotContains(t, body, "ephemeral_5m_input_tokens")
	assert.NotContains(t, body, "cache_write_tokens")
	assert.NotContains(t, body, "cached_creation_tokens")
	assert.Contains(t, body, `"cached_tokens":1200`)
	assert.Contains(t, body, `"prompt_tokens":1510`)

	var withoutCreation Usage
	require.NoError(t, kitutil.Unmarshal([]byte(`{"prompt_tokens":1510,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":1200}}`), &withoutCreation))
	encoded, err = kitutil.Marshal(withoutCreation)
	require.NoError(t, err)
	absent := string(encoded)
	assert.NotContains(t, absent, "cache_creation_input_tokens")
	assert.NotContains(t, absent, `"cache_creation":`)
	assert.NotContains(t, absent, "ephemeral_5m_input_tokens")
	assert.NotContains(t, absent, "cache_write_tokens")
	assert.NotContains(t, absent, "cached_creation_tokens")
}

func TestCacheCreationInputTokensRoundTrip(t *testing.T) {
	t.Parallel()

	var usage Usage
	require.NoError(t, kitutil.Unmarshal([]byte(`{"prompt_tokens":1510,"prompt_tokens_details":{"cached_tokens":1200,"cache_creation_input_tokens":300}}`), &usage))
	encoded, err := kitutil.Marshal(usage)
	require.NoError(t, err)
	var again Usage
	require.NoError(t, kitutil.Unmarshal(encoded, &again))
	assert.Equal(t, 300, again.PromptTokensDetails.CacheCreationInputTokens)
	assert.Equal(t, 300, again.PromptTokensDetails.CacheCreationTokensTotal())
	assert.Zero(t, again.ClaudeCacheCreation5mTokens)
	assert.Zero(t, again.ClaudeCacheCreation1hTokens)
}

func TestMergeUsageNonZeroCacheCreationInputTokensReplacesWithoutAdding(t *testing.T) {
	t.Parallel()

	first := &Usage{}
	require.NoError(t, kitutil.Unmarshal([]byte(`{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":10,"cache_creation_input_tokens":100}}`), first))
	second := &Usage{}
	require.NoError(t, kitutil.Unmarshal([]byte(`{"prompt_tokens":200,"prompt_tokens_details":{"cached_tokens":20,"cache_creation_input_tokens":300}}`), second))
	merged := MergeUsageNonZero(first, second)
	require.NotNil(t, merged)
	assert.Equal(t, 300, merged.PromptTokensDetails.CacheCreationTokensTotal())
	assert.Equal(t, 20, merged.PromptTokensDetails.CachedTokens)
	assert.Zero(t, merged.ClaudeCacheCreation5mTokens)
	assert.Zero(t, merged.ClaudeCacheCreation1hTokens)

	kept := MergeUsageNonZero(merged, &Usage{})
	assert.Equal(t, 300, kept.PromptTokensDetails.CacheCreationTokensTotal())

	write := &Usage{}
	require.NoError(t, kitutil.Unmarshal([]byte(`{"prompt_tokens_details":{"cache_write_tokens":250}}`), write))
	scalar := &Usage{}
	require.NoError(t, kitutil.Unmarshal([]byte(`{"prompt_tokens_details":{"cache_creation_input_tokens":100}}`), scalar))
	both := MergeUsageNonZero(write, scalar)
	assert.Equal(t, 250, both.PromptTokensDetails.CacheWriteTokens)
	assert.Equal(t, 250, both.PromptTokensDetails.CacheCreationTokensTotal())

	responses := &Usage{}
	require.NoError(t, kitutil.Unmarshal([]byte(`{"input_tokens_details":{"cache_creation_input_tokens":300}}`), responses))
	overlaid := MergeUsageNonZero(&Usage{}, responses)
	require.NotNil(t, overlaid.InputTokensDetails)
	assert.Equal(t, 300, overlaid.InputTokensDetails.CacheCreationTokensTotal())
	assert.Zero(t, overlaid.PromptTokensDetails.CacheCreationTokensTotal())
}
