package dto

import (
	"testing"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 契约:gpt-image-2.6 新增的请求字段是已知字段,不能落进 Extra 被序列化丢掉。
func TestImageRequestPreserveGPTImage26Fields(t *testing.T) {
	raw := []byte(`{"model":"gpt-image-2.6-flare","prompt":"p","complexity":"low","markup":{"image_url":"https://x/o.png"},"svg":{"model":"m"},"frame_count":2,"frame_duration_ms":0}`)

	var req ImageRequest
	require.NoError(t, kitutil.Unmarshal(raw, &req))

	encoded, err := kitutil.Marshal(req)
	require.NoError(t, err)
	assert.JSONEq(t, string(raw), string(encoded))
}

func TestGetOpenAIChatCapabilitiesGPT6Models(t *testing.T) {
	tests := []struct {
		name             string
		model            string
		effort           string
		wantMaxComp      bool
		wantDevRole      bool
		wantSupportsSamp bool
	}{
		{name: "gpt-6.1-sol default", model: "gpt-6.1-sol", wantMaxComp: true, wantDevRole: true, wantSupportsSamp: false},
		{name: "gpt-6.1-sol none", model: "gpt-6.1-sol", effort: "none", wantMaxComp: true, wantDevRole: true, wantSupportsSamp: false},
		{name: "gpt-6.1-terra default", model: "gpt-6.1-terra", wantMaxComp: true, wantDevRole: true, wantSupportsSamp: false},
		{name: "gpt-6.1-terra none", model: "gpt-6.1-terra", effort: "none", wantMaxComp: true, wantDevRole: true, wantSupportsSamp: true},
		{name: "gpt-6.1-terra high", model: "gpt-6.1-terra", effort: "high", wantMaxComp: true, wantDevRole: true, wantSupportsSamp: false},
		{name: "gpt-6.2-astra default", model: "gpt-6.2-astra", wantMaxComp: true, wantDevRole: true, wantSupportsSamp: false},
		{name: "gpt-6.2-sol default", model: "gpt-6.2-sol", wantMaxComp: true, wantDevRole: true, wantSupportsSamp: false},
		{name: "gpt-6.2-sol none", model: "gpt-6.2-sol", effort: "none", wantMaxComp: true, wantDevRole: true, wantSupportsSamp: false},
		{name: "gpt-6.2-astra snapshot", model: "gpt-6.2-astra-2026-04-01", wantMaxComp: true, wantDevRole: true, wantSupportsSamp: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			caps := GetOpenAIChatCapabilities(tc.model, tc.effort)
			assert.Equal(t, tc.wantMaxComp, caps.UseMaxCompletionTokens)
			assert.Equal(t, tc.wantDevRole, caps.UseDeveloperRole)
			assert.Equal(t, tc.wantSupportsSamp, caps.SupportsTemperature)
			assert.Equal(t, tc.wantSupportsSamp, caps.SupportsTopP)
			assert.Equal(t, tc.wantSupportsSamp, caps.SupportsLogProbs)
		})
	}
}

func TestGeminiArgonFieldsUnmarshalSnakeAndCamelCase(t *testing.T) {
	raw := []byte(`{
		"continuation_token":"tok_1",
		"contents":[{"role":"user","parts":[{"text":"hi","media_processing":{"mode":"fast"}}]}],
		"generationConfig":{"media_processing":{"mode":"auto"}}
	}`)

	var req GeminiChatRequest
	require.NoError(t, kitutil.Unmarshal(raw, &req))
	assert.JSONEq(t, `"tok_1"`, string(req.ContinuationToken))
	require.Len(t, req.Contents, 1)
	require.Len(t, req.Contents[0].Parts, 1)
	assert.JSONEq(t, `{"mode":"fast"}`, string(req.Contents[0].Parts[0].MediaProcessing))
	assert.JSONEq(t, `{"mode":"auto"}`, string(req.GenerationConfig.MediaProcessing))
}
