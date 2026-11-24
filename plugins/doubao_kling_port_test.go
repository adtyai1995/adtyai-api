package plugins_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/plugins"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadEmbeddedPlugin(t *testing.T, key string) *jsplugin.LoadedPlugin {
	t.Helper()
	source, err := plugins.Source(key)
	require.NoError(t, err)
	loaded, err := jsplugin.NewRegistry().Register(source, jsplugin.Options{Key: key})
	require.NoError(t, err)
	return loaded
}

func TestDoubaoSeedream55AndDefaultProfile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	plugin := loadEmbeddedPlugin(t, "doubao")
	const ref = "https://cdn.example/ref.png"

	submit := func(t *testing.T, model string, body map[string]any) map[string]any {
		t.Helper()
		payload := make(map[string]any, len(body)+1)
		for k, v := range body {
			payload[k] = v
		}
		payload["model"] = model
		value, err := plugin.Engine.CallPath(t.Context(), "native", []string{"createImage"}, map[string]any{
			"body": map[string]any{"kind": "json", "value": payload},
		})
		require.NoError(t, err)
		intent, ok := value.(map[string]any)
		require.True(t, ok)

		action, _ := intent["action"].(string)
		adaptor := taskplugin.New(plugin)
		info := &relaycommon.RelayInfo{
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: "https://ark.cn-beijing.volces.com", ApiKey: "k", UpstreamModelName: model},
			TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_1", Action: action},
			OriginModelName: model,
		}
		adaptor.Init(info)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/doubao/api/v3/images/generations", nil)
		c.Set("task_action", action)
		c.Set("task_request", intent["requestBody"])
		require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
		facts, err := adaptor.ExtractUsageFactsValidated(c, info)
		require.NoError(t, err)
		return facts
	}

	t.Run("5.5 native transparency and layer bounds", func(t *testing.T) {
		facts := submit(t, "doubao-seedream-5-5-261028", map[string]any{"prompt": "a glass cat", "background": "transparent", "size": "1K"})
		assert.EqualValues(t, 1, facts["images_up_to_1_5k"])

		facts = submit(t, "doubao-seedream-5-5-261028", map[string]any{"image": ref, "layer_decomposition": true})
		assert.EqualValues(t, 30, facts["images_above_1_5k"])
		assert.Equal(t, true, facts["layer_decomposition"])

		facts = submit(t, "doubao-seedream-5-5-261028", map[string]any{
			"image": ref, "layer_decomposition": true, "size": "1K",
			"layer_decomposition_options": map[string]any{"max_images": 100},
		})
		assert.EqualValues(t, 100, facts["images_up_to_1_5k"])

		_, err := plugin.Engine.CallPath(t.Context(), "native", []string{"createImage"}, map[string]any{
			"body": map[string]any{"kind": "json", "value": map[string]any{
				"model": "doubao-seedream-5-5-261028", "image": ref, "layer_decomposition": true,
				"layer_decomposition_options": map[string]any{"max_images": 101},
			}},
		})
		require.ErrorContains(t, err, "max_images must be an integer between 2 and 100")
	})

	t.Run("endpoint id default profile supports rgba and 100 layers", func(t *testing.T) {
		facts := submit(t, "ep-20261028-seedream55", map[string]any{"prompt": "a glass cat", "background": "auto", "size": "1K"})
		assert.EqualValues(t, 1, facts["images_up_to_1_5k"])

		facts = submit(t, "ep-20261028-seedream55", map[string]any{
			"image": ref, "layer_decomposition": true, "size": "1K",
			"layer_decomposition_options": map[string]any{"max_images": 100},
		})
		assert.EqualValues(t, 100, facts["images_up_to_1_5k"])
	})
}

func TestKling4AspectRatiosDurationAndADTYBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	plugin := loadEmbeddedPlugin(t, "kling")

	submitOpenAI := func(t *testing.T, body map[string]any) map[string]any {
		t.Helper()
		value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
			"model": "kling-4.0",
			"body":  map[string]any{"kind": "json", "value": body},
		})
		require.NoError(t, err)
		intent, ok := value.(map[string]any)
		require.True(t, ok)

		adaptor := taskplugin.New(plugin)
		info := &relaycommon.RelayInfo{
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api-singapore.klingai.com", ApiKey: "sk-test", UpstreamModelName: "kling-4.0"},
			TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
			OriginModelName: "kling-4.0",
		}
		adaptor.Init(info)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
		c.Set("task_request", intent["requestBody"])
		require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
		reader, err := adaptor.BuildRequestBody(c, info)
		require.NoError(t, err)
		raw, err := io.ReadAll(reader)
		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, common.Unmarshal(raw, &decoded))
		return decoded
	}

	t.Run("kling 4.0 aspect ratios include 4:3 and 3:4", func(t *testing.T) {
		for _, tc := range []struct {
			size     string
			settings map[string]any
		}{
			{"720x720", map[string]any{"resolution": "720p", "aspect_ratio": "1:1", "duration": float64(5)}},
			{"1792x1024", map[string]any{"resolution": "1080p", "aspect_ratio": "16:9", "duration": float64(5)}},
			{"1024x1792", map[string]any{"resolution": "1080p", "aspect_ratio": "9:16", "duration": float64(5)}},
			{"2560x1080", map[string]any{"resolution": "1080p", "aspect_ratio": "21:9", "duration": float64(5)}},
			{"1440x1080", map[string]any{"resolution": "1080p", "aspect_ratio": "4:3", "duration": float64(5)}},
			{"1080x1440", map[string]any{"resolution": "1080p", "aspect_ratio": "3:4", "duration": float64(5)}},
		} {
			sent := submitOpenAI(t, map[string]any{"model": "kling-4.0", "prompt": "a cat", "size": tc.size, "seconds": 5})
			assert.Equal(t, tc.settings, sent["settings"], tc.size)
		}
	})

	t.Run("kling 4.0 duration bounds 3..30", func(t *testing.T) {
		for _, duration := range []any{2, 31, "5.5"} {
			_, err := plugin.Engine.CallPath(t.Context(), "native", []string{"kling4TextToVideo"}, map[string]any{
				"body": map[string]any{"kind": "json", "value": map[string]any{"prompt": "a cat", "settings": map[string]any{"duration": duration}}},
			})
			require.ErrorContains(t, err, "duration must be an integer from 3 to 30")
		}
	})

	t.Run("ADTY keeps cash list_price out of credit units settlement", func(t *testing.T) {
		body := map[string]any{
			"code": 0,
			"data": []any{map[string]any{
				"id":      "kling_4",
				"status":  "succeeded",
				"outputs": []any{map[string]any{"type": "video", "url": "https://cdn.example/out.mp4", "duration": "10"}},
				"billing": []any{map[string]any{"charge_type": "cash", "cash_type": "balance", "amount": "2.55", "list_price": "3", "currency": "CNY"}},
			}},
		}
		usage, err := plugin.Engine.Call(t.Context(), "extractUsageOnComplete", map[string]any{
			"taskId": "kling_4",
			"state":  map[string]any{"resolution": "720p"},
		}, map[string]any{}, body)
		require.NoError(t, err)
		facts, ok := usage.(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 10.0, facts["units"], 1e-9, "cash billing must fall back to reported duration units instead of currency list_price")
	})
}
