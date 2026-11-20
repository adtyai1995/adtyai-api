package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	ftrRelayStartQuota = 1000000
	// 0.002 USD * 500000 quota per USD. Group ratio is pinned to 1.
	ftrRelayChargeQuota = 1000
	// 0.04 USD * 500000 quota per USD for one grok-imagine-image-2.0 image.
	ftrImageChargeQuota = 20000
)

func ftrRelayDB(t *testing.T) {
	t.Helper()
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
	oldRedis := common.RedisEnabled
	oldBatch := common.BatchUpdateEnabled
	oldTimeout := constant.StreamingTimeout
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	// Production init uses STREAMING_TIMEOUT, default 300. An unset 0 panics in NewTicker.
	constant.StreamingTimeout = 300
	metrics := config.GlobalConfig.Get("perf_metrics_setting").(*perf_metrics_setting.PerfMetricsSetting)
	oldMetrics := metrics.Enabled
	// Record() publishes a goroutine that reads RedisEnabled. Keep it off so test cleanup does not race that read.
	metrics.Enabled = false
	t.Cleanup(func() {
		common.RedisEnabled = oldRedis
		common.BatchUpdateEnabled = oldBatch
		constant.StreamingTimeout = oldTimeout
		metrics.Enabled = oldMetrics
	})
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.Log{},
		&model.Channel{},
		&model.BillingOperation{},
		&model.BillingOperationMovement{},
		&model.AdtySiteEarningEvent{},
		&model.AdtyGroupHealthSample{},
		&model.AdtyGroupHealthState{},
		&model.AdtyFundBatch{},
		&model.AdtyFundAllocation{},
		&model.AdtyBillingHold{},
		&model.PerfMetric{},
		&model.AdtyBalanceLedger{},
	))
}

func ftrRelayBuyer(t *testing.T, name string) (model.User, model.User, model.Token) {
	t.Helper()
	buyer := model.User{
		Username: name, Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", SiteId: 0, Quota: ftrRelayStartQuota,
	}
	other := model.User{
		Username: name + "-other", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", SiteId: 0, Quota: ftrRelayStartQuota,
	}
	require.NoError(t, model.DB.Create(&buyer).Error)
	require.NoError(t, model.DB.Create(&other).Error)
	token := model.Token{
		UserId: buyer.Id, Key: "sk-" + name, Name: name, Status: common.TokenStatusEnabled,
		RemainQuota: ftrRelayStartQuota, ExpiredTime: -1, Group: "default",
	}
	require.NoError(t, model.DB.Create(&token).Error)
	return buyer, other, token
}

func ftrPinRelayPrice(t *testing.T, priceJSON string) {
	t.Helper()
	oldPrice := ratio_setting.ModelPrice2JSONString()
	oldRatio := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrice))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatio))
	})
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(priceJSON))
}

func ftrRelayContext(t *testing.T, method, path, body string, buyer model.User, token model.Token, baseURL, modelName string, channelType int) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("Accept", "application/json")
	common.SetContextKey(ctx, constant.ContextKeyUserId, buyer.Id)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenId, token.Id)
	common.SetContextKey(ctx, constant.ContextKeyTokenKey, token.Key)
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenUnlimited, false)
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, modelName)
	common.SetContextKey(ctx, constant.ContextKeyChannelId, 1)
	common.SetContextKey(ctx, constant.ContextKeyChannelName, "ftr-relay")
	common.SetContextKey(ctx, constant.ContextKeyChannelType, channelType)
	common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, baseURL)
	common.SetContextKey(ctx, constant.ContextKeyChannelKey, "sk-upstream")
	common.SetContextKey(ctx, constant.ContextKeyChannelIsMultiKey, false)
	ctx.Set("role", buyer.Role)
	ctx.Set("username", buyer.Username)
	return ctx, recorder
}

func ftrQuota(t *testing.T, userID int) int {
	t.Helper()
	var quota int
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Select("quota").Scan(&quota).Error)
	return quota
}

func TestFTRADTY0604ProtocolConversionPG(t *testing.T) {
	ftrRelayDB(t)
	buyer, other, _ := ftrRelayBuyer(t, "ftr-0604")
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeAnthropic,
			ApiType:           constant.APITypeAnthropic,
			UpstreamModelName: "claude-sonnet-4-6",
		},
		OriginModelName: "claude-sonnet-4-6",
		RelayMode:       relayconstant.RelayModeChatCompletions,
		RelayFormat:     types.RelayFormatOpenAI,
		UserId:          buyer.Id,
	}
	converted, err := (&claude.Adaptor{}).ConvertOpenAIRequest(ctx, info, &dto.GeneralOpenAIRequest{
		Model: "claude-sonnet-4-6",
		Messages: []dto.Message{
			{Role: "system", Content: "answer in English"},
			{Role: "user", Content: "convert this sentence"},
		},
	})
	require.NoError(t, err)
	claudeReq, ok := converted.(*dto.ClaudeRequest)
	require.True(t, ok, "OpenAI chat must convert to a Claude messages request")
	assert.Equal(t, "claude-sonnet-4-6", claudeReq.Model)
	raw, err := common.Marshal(claudeReq)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "convert this sentence")
	assert.Contains(t, string(raw), "answer in English")
	assert.NotContains(t, string(raw), `"object":"chat.completion"`)
	assert.Equal(t, ftrRelayStartQuota, ftrQuota(t, buyer.Id), "protocol conversion must leave the 1000000 quota wallet unchanged")
	assert.Equal(t, ftrRelayStartQuota, ftrQuota(t, other.Id))
	var ledgers int64
	require.NoError(t, model.DB.Model(&model.AdtyBalanceLedger{}).Where("user_id = ?", buyer.Id).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

func TestFTRADTY0605NonStreamForwardPG(t *testing.T) {
	ftrRelayDB(t)
	ftrPinRelayPrice(t, `{"ftr-relay-flat":0.002}`)
	buyer, other, token := ftrRelayBuyer(t, "ftr-0605")
	var seenPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		assert.Contains(t, string(body), "ftr-relay-flat")
		assert.Contains(t, string(body), "say pong")
		assert.NotContains(t, string(body), `"stream":true`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-ftr","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}`)
	}))
	t.Cleanup(upstream.Close)

	ctx, recorder := ftrRelayContext(t, http.MethodPost, "/v1/chat/completions",
		`{"model":"ftr-relay-flat","messages":[{"role":"user","content":"say pong"}]}`,
		buyer, token, upstream.URL, "ftr-relay-flat", constant.ChannelTypeOpenAI)
	Relay(ctx, types.RelayFormatOpenAI)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), "pong")
	assert.Equal(t, "/v1/chat/completions", seenPath)
	assert.Equal(t, ftrRelayStartQuota-ftrRelayChargeQuota, ftrQuota(t, buyer.Id), "0.002 USD settles as 1000 quota")
	assert.Equal(t, ftrRelayStartQuota, ftrQuota(t, other.Id))
	var used int
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", token.Id).Select("used_quota").Scan(&used).Error)
	assert.Equal(t, ftrRelayChargeQuota, used)
}

func TestFTRADTY0606StreamForwardPG(t *testing.T) {
	ftrRelayDB(t)
	ftrPinRelayPrice(t, `{"ftr-relay-flat":0.002}`)
	buyer, other, token := ftrRelayBuyer(t, "ftr-0606")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		assert.Contains(t, string(body), `"stream":true`)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-ftr\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"pong\"},\"finish_reason\":null}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-ftr\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":1,\"total_tokens\":5}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(upstream.Close)

	ctx, recorder := ftrRelayContext(t, http.MethodPost, "/v1/chat/completions",
		`{"model":"ftr-relay-flat","stream":true,"messages":[{"role":"user","content":"say pong"}]}`,
		buyer, token, upstream.URL, "ftr-relay-flat", constant.ChannelTypeOpenAI)
	Relay(ctx, types.RelayFormatOpenAI)

	require.Contains(t, recorder.Body.String(), "pong", recorder.Body.String())
	assert.Equal(t, ftrRelayStartQuota-ftrRelayChargeQuota, ftrQuota(t, buyer.Id), "stream settlement still charges 0.002 USD as 1000 quota")
	assert.Equal(t, ftrRelayStartQuota, ftrQuota(t, other.Id))
}

func TestFTRADTY0607ImageForwardPG(t *testing.T) {
	ftrRelayDB(t)
	// Published image price is 0.04 USD. The live price map starts empty until an administrator sets it.
	ftrPinRelayPrice(t, `{"grok-imagine-image-2.0":0.04}`)
	buyer, other, token := ftrRelayBuyer(t, "ftr-0607")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/images/generations", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		assert.Contains(t, string(body), "grok-imagine-image-2.0")
		assert.Contains(t, string(body), "a red square")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"created":1710000000,"data":[{"b64_json":"image"}]}`)
	}))
	t.Cleanup(upstream.Close)

	ctx, recorder := ftrRelayContext(t, http.MethodPost, "/v1/images/generations",
		`{"model":"grok-imagine-image-2.0","prompt":"a red square","n":1,"size":"1024x1024"}`,
		buyer, token, upstream.URL, "grok-imagine-image-2.0", constant.ChannelTypeOpenAI)
	Relay(ctx, types.RelayFormatOpenAIImage)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), "image")
	assert.Equal(t, ftrRelayStartQuota-ftrImageChargeQuota, ftrQuota(t, buyer.Id), "one 0.04 USD image settles as 20000 quota")
	assert.Equal(t, ftrRelayStartQuota, ftrQuota(t, other.Id))
}
