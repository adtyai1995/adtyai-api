package relay

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMidjourneyProgressImageUrlPreservesSignedQueryOnRandAppend(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prevForward := setting.MjForwardUrlEnabled
	prevServer := system_setting.ServerAddress
	prevSecret := common.CryptoSecret
	setting.MjForwardUrlEnabled = true
	system_setting.ServerAddress = "https://api.example.com"
	common.CryptoSecret = "test-crypto-secret-for-mj-image-0087"
	t.Cleanup(func() {
		setting.MjForwardUrlEnabled = prevForward
		system_setting.ServerAddress = prevServer
		common.CryptoSecret = prevSecret
	})

	now := time.Unix(1775000000, 0)
	imagePath, err := service.MidjourneyImagePath("mj-progress-1", now)
	require.NoError(t, err)

	fullURL := strings.TrimRight(system_setting.ServerAddress, "/") + imagePath
	sep := "?"
	if strings.Contains(fullURL, "?") {
		sep = "&"
	}
	fullURL += sep + "rand=" + strconv.FormatInt(now.UnixNano(), 10)

	req := httptest.NewRequest(http.MethodGet, fullURL, nil)
	q := req.URL.Query()
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	require.NoError(t, err)
	sig := q.Get("sig")
	require.NotEmpty(t, sig)
	assert.False(t, strings.Contains(sig, "?rand="), "sig must not be corrupted by ?rand=")
	assert.True(t, service.VerifyMidjourneyImageAccess("mj-progress-1", sig, exp, now.Unix()))
}

func TestModelPriceHelperPerCallRetryRestoresUnmutatedBasePriceData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"video-retry-model": 1.0}`))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	info := &relaycommon.RelayInfo{
		OriginModelName: "video-retry-model",
		UsingGroup:      "default",
		UserGroup:       "default",
		StartTime:       time.Now(),
	}

	first, err := helper.ModelPriceHelperPerCall(c, info)
	require.NoError(t, err)
	baseQuota := first.Quota
	require.Positive(t, baseQuota)

	// Simulate first attempt in RelayTaskSubmit multiplying Quota by duration/resolution OtherRatios.
	info.PriceData.AddOtherRatio("duration", 3.0)
	info.PriceData.Quota = int(info.PriceData.ApplyOtherRatiosToFloat(float64(info.PriceData.Quota)))
	require.Equal(t, baseQuota*3, info.PriceData.Quota)

	// Simulate second attempt retry calling ModelPriceHelperPerCall again on the same context.
	second, err := helper.ModelPriceHelperPerCall(c, info)
	require.NoError(t, err)
	assert.Equal(t, baseQuota, second.Quota, "retry must start from unmutated base quota")
	assert.Empty(t, second.OtherRatios(), "retry must not retain previous attempt OtherRatios")
}

func TestIsAmbiguousTaskSubmitTransportErrorClassification(t *testing.T) {
	t.Parallel()

	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	assert.False(t, IsAmbiguousTaskSubmitTransportError(dialErr), "pre-send dial errors are safe to retry")
	assert.False(t, IsAmbiguousTaskSubmitTransportError(context.Canceled), "caller cancellation is handled separately")

	readTimeoutErr := &net.OpError{Op: "read", Net: "tcp", Err: context.DeadlineExceeded}
	assert.True(t, IsAmbiguousTaskSubmitTransportError(readTimeoutErr), "post-send read timeout is ambiguous")
	assert.True(t, IsAmbiguousTaskSubmitTransportError(context.DeadlineExceeded))
	assert.True(t, IsAmbiguousTaskSubmitTransportError(errors.New("net/http: timeout awaiting response headers")))
}

func TestResolveTaskPluginForTaskPinsSubmittedVersionAcrossHotReload(t *testing.T) {
	srcV1 := `
export const meta = {apiVersion:1,key:"version-pinned-plugin",name:"Pinned V1",version:"1.0.0",author:{name:"Test"},models:["pinned-model"],fetchMode:"per_task",routes:[{method:"POST",path:"/v1/pinned",type:"submit",decode:"decode",render:"created"}]};
export const native = {decode:function(ctx){return {kind:"submit",model:"pinned-model",requestBody:ctx.body.value};},created:function(ctx,task){return {data:{task_id:task.task_id}};}};
export function buildSubmitRequest(){return {}} export function parseSubmitResponse(){return {taskId:"t1"}} export function buildQueryRequest(){return {}} export function parseTaskResult(){return {status:"SUCCESS"}}
`
	srcV2 := strings.ReplaceAll(strings.ReplaceAll(srcV1, `"1.0.0"`, `"2.0.0"`), `"Pinned V1"`, `"Pinned V2"`)

	p1, err := jsplugin.DefaultRegistry.Register(srcV1, jsplugin.Options{Key: "version-pinned-plugin", Version: "1.0.0"})
	require.NoError(t, err)
	require.Equal(t, "1.0.0", p1.Meta.Version)

	p2, err := jsplugin.DefaultRegistry.Register(srcV2, jsplugin.Options{Key: "version-pinned-plugin", Version: "2.0.0"})
	require.NoError(t, err)
	require.Equal(t, "2.0.0", p2.Meta.Version)
	t.Cleanup(func() {
		_ = jsplugin.DefaultRegistry.Unregister("version-pinned-plugin")
	})

	inFlightTask := &model.Task{
		Platform: constant.TaskPlatform("version-pinned-plugin"),
		PrivateData: model.TaskPrivateData{
			Execution: &model.TaskExecutionSnapshot{
				TaskPlugin: &model.TaskPluginSnapshot{
					Key:     "version-pinned-plugin",
					Version: "1.0.0",
				},
			},
		},
	}

	resolved, ok := ResolveTaskPluginForTask(inFlightTask, inFlightTask.Platform)
	require.True(t, ok)
	require.NotNil(t, resolved)
	assert.Equal(t, "1.0.0", resolved.Meta.Version, "in-flight task must resolve its pinned v1.0.0 plugin after v2.0.0 activation")
	assert.Equal(t, "Pinned V1", resolved.Meta.Name)

	pollingAdaptor := service.ResolveTaskPollingAdaptor(inFlightTask, inFlightTask.Platform)
	require.NotNil(t, pollingAdaptor)
}
