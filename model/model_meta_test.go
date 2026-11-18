package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelContextLengthPersistenceAndConsumerRoundTrip(t *testing.T) {
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&Model{}, &Vendor{}, &Option{}, &AdtyModelAlias{}))

	// Invalid boundaries (< 0 or > MaxModelContextLength) are rejected before write.
	require.ErrorIs(t, ValidateModelContextLength(-1), ErrModelContextInvalid)
	require.ErrorIs(t, ValidateModelContextLength(MaxModelContextLength+1), ErrModelContextInvalid)
	require.NoError(t, ValidateModelContextLength(0))
	require.NoError(t, ValidateModelContextLength(128000))

	neg := &Model{ModelName: "neg-context", ContextLength: -10, Status: 1}
	require.ErrorIs(t, neg.Insert(), ErrModelContextInvalid)

	// Exact model insert & update round-trip preserves tags/endpoints alongside context_length.
	exact := &Model{
		ModelName:     "gpt-4o-ctx",
		Description:   "Context test",
		Tags:          "Chat,Vision",
		Endpoints:     `{"openai":"/v1/chat/completions"}`,
		ContextLength: 128000,
		Status:        1,
		SyncOfficial:  1,
	}
	require.NoError(t, exact.Insert())
	require.NotZero(t, exact.Id)

	var stored Model
	require.NoError(t, db.First(&stored, exact.Id).Error)
	assert.Equal(t, 128000, stored.ContextLength)
	assert.Equal(t, "Chat,Vision", stored.Tags)
	assert.Equal(t, `{"openai":"/v1/chat/completions"}`, stored.Endpoints)

	resolved, err := ResolveModelContextLength("gpt-4o-ctx")
	require.NoError(t, err)
	assert.Equal(t, 128000, resolved)
	assert.Equal(t, 128000, GetModelContextLength("gpt-4o-ctx"))
	require.NoError(t, CheckModelContextLimit("gpt-4o-ctx", 128000))
	require.ErrorIs(t, CheckModelContextLimit("gpt-4o-ctx", 128001), ErrModelContextExceeded)
	require.ErrorIs(t, CheckModelContextLimit("gpt-4o-ctx", -1), ErrModelContextInvalid)

	// Update context_length and verify invalid update does not overwrite stored record.
	exact.ContextLength = -5
	require.ErrorIs(t, exact.Update(), ErrModelContextInvalid)
	require.NoError(t, db.First(&stored, exact.Id).Error)
	assert.Equal(t, 128000, stored.ContextLength)

	exact.ContextLength = 200000
	exact.Tags = "Chat,Vision,Updated"
	require.NoError(t, exact.Update())
	require.NoError(t, db.First(&stored, exact.Id).Error)
	assert.Equal(t, 200000, stored.ContextLength)
	assert.Equal(t, "Chat,Vision,Updated", stored.Tags)
	assert.Equal(t, `{"openai":"/v1/chat/completions"}`, stored.Endpoints)

	// Prefix rule and AdtyModelAlias resolution.
	prefixRule := &Model{
		ModelName:     "claude-3-5-",
		NameRule:      NameRulePrefix,
		ContextLength: 200000,
		Status:        1,
	}
	require.NoError(t, prefixRule.Insert())
	assert.Equal(t, 200000, GetModelContextLength("claude-3-5-sonnet"))

	require.NoError(t, db.Create(&AdtyModelAlias{
		Alias:   "custom-alias-model",
		Target:  "unknown-target",
		Context: 64000,
		Enabled: true,
	}).Error)
	assert.Equal(t, 64000, GetModelContextLength("custom-alias-model"))
	require.ErrorIs(t, CheckModelContextLimit("custom-alias-model", 64001), ErrModelContextExceeded)
}
