package reasoning

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderGeminiMapsAndClampsGemini4(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		intent     Intent
		wantLevel  string
		wantEffort Effort
		wantCodes  []string
	}{
		{
			name:       "xhigh on gemini 4 hydrogen is kept",
			model:      "gemini-4-hydrogen",
			intent:     Intent{Mode: ModeEnabled, Effort: EffortXHigh},
			wantLevel:  "xhigh",
			wantEffort: EffortXHigh,
		},
		{
			name:       "minimal on gemini 4 hydrogen moves to low",
			model:      "gemini-4-hydrogen",
			intent:     Intent{Mode: ModeEnabled, Effort: EffortMinimal},
			wantLevel:  "low",
			wantEffort: EffortLow,
			wantCodes:  []string{"gemini_level_adjusted"},
		},
		{
			name:       "max on gemini 4 argon moves to high",
			model:      "gemini-4-argon",
			intent:     Intent{Mode: ModeEnabled, Effort: EffortMax},
			wantLevel:  "high",
			wantEffort: EffortHigh,
			wantCodes:  []string{"gemini_level_adjusted"},
		},
		{
			name:       "max on gemini 4 neon moves to xhigh",
			model:      "gemini-4-neon",
			intent:     Intent{Mode: ModeEnabled, Effort: EffortMax},
			wantLevel:  "xhigh",
			wantEffort: EffortXHigh,
			wantCodes:  []string{"gemini_level_adjusted"},
		},
		{
			name:       "xhigh on gemini 4 carbon moves to high",
			model:      "gemini-4-carbon",
			intent:     Intent{Mode: ModeEnabled, Effort: EffortXHigh},
			wantLevel:  "high",
			wantEffort: EffortHigh,
			wantCodes:  []string{"gemini_level_adjusted"},
		},
		{
			name:       "xhigh on gemini 4 argon moves to high",
			model:      "gemini-4-argon",
			intent:     Intent{Mode: ModeEnabled, Effort: EffortXHigh},
			wantLevel:  "high",
			wantEffort: EffortHigh,
			wantCodes:  []string{"gemini_level_adjusted"},
		},
		{
			name:       "enabled default on gemini 4 argon is medium",
			model:      "gemini-4-argon",
			intent:     Intent{Mode: ModeEnabled},
			wantEffort: EffortMedium,
		},
		{
			name:       "enabled default on gemini 4 argon cyber permissive is high",
			model:      "gemini-4-argon-cyber-permissive",
			intent:     Intent{Mode: ModeEnabled},
			wantEffort: EffortHigh,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			render, err := RenderGemini(tc.model, tc.intent, nil, 0)
			require.NoError(t, err)
			require.NotNil(t, render.Config)
			assert.Equal(t, tc.wantLevel, render.Config.ThinkingLevel)
			assert.Equal(t, tc.wantEffort, render.EffectiveEffort)
			var gotCodes []string
			for _, diag := range render.Diagnostics {
				gotCodes = append(gotCodes, diag.Code)
			}
			assert.Equal(t, tc.wantCodes, gotCodes)
		})
	}
}

func TestNormalizeGeminiThinkingConfigGemini4(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		config     dto.GeminiThinkingConfig
		wantLevel  string
		wantEffort Effort
		wantCodes  []string
	}{
		{
			name:       "max on gemini 4 argon moves to high",
			model:      "gemini-4-argon",
			config:     dto.GeminiThinkingConfig{ThinkingLevel: "max"},
			wantLevel:  "high",
			wantEffort: EffortHigh,
			wantCodes:  []string{"gemini_level_adjusted"},
		},
		{
			name:       "max on gemini 4 hydrogen moves to xhigh",
			model:      "gemini-4-hydrogen",
			config:     dto.GeminiThinkingConfig{ThinkingLevel: "max"},
			wantLevel:  "xhigh",
			wantEffort: EffortXHigh,
			wantCodes:  []string{"gemini_level_adjusted"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.config
			gen := &dto.GeminiChatGenerationConfig{ThinkingConfig: &cfg}
			effort, diags, err := NormalizeGeminiThinkingConfig(tc.model, gen)
			require.NoError(t, err)
			require.NotNil(t, gen.ThinkingConfig)
			assert.Equal(t, tc.wantLevel, gen.ThinkingConfig.ThinkingLevel)
			assert.Equal(t, tc.wantEffort, effort)
			var gotCodes []string
			for _, diag := range diags {
				gotCodes = append(gotCodes, diag.Code)
			}
			assert.Equal(t, tc.wantCodes, gotCodes)
		})
	}
}
