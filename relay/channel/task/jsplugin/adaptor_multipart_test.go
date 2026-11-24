package jsplugin

import (
	"bytes"
	"mime"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskAdaptorBuildsMultipartFromJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	source := `
export const meta = {apiVersion:1,key:"json-multipart",name:"JSON Multipart",version:"1.0.0",author:{name:"Test"},models:["m"],fetchMode:"per_task"};
export function buildSubmitRequest(ctx) {
  const parts = [{name:"model",value:"m"},{name:"prompt",value:ctx.requestBody.prompt},{name:"tag",value:"first"},{name:"tag",value:"second"},{name:"empty",value:""}];
  if (ctx.requestBody.reference) parts.push({name:"input_reference",fileRef:"request_file:input_reference"});
  return {url:ctx.baseUrl+"/submit",headers:{"Content-Type":ctx.requestHeaders["Content-Type"]},bodyType:"multipart",parts};
}
export function parseSubmitResponse(){return {taskId:"1"}} export function buildQueryRequest(){return {url:"https://example.com"}} export function parseTaskResult(){return {status:"SUCCESS"}}
`
	plugin, err := pluginruntime.NewRegistry().Register(source, pluginruntime.Options{})
	require.NoError(t, err)
	for _, testCase := range []struct {
		name      string
		reference bool
		wantError string
	}{
		{name: "text fields"},
		{name: "file reference requires an upload", reference: true, wantError: "multipart boundary not found"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			adaptor := New(plugin)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://provider.example"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
			adaptor.Init(info)
			request := map[string]any{"model": "m", "prompt": "paper plane", "reference": testCase.reference}
			encoded, err := common.Marshal(request)
			require.NoError(t, err)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(encoded))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set("task_request", request)

			body, err := adaptor.BuildRequestBody(c, info)
			if testCase.wantError != "" {
				require.EqualError(t, err, testCase.wantError)
				assert.Nil(t, body)
				return
			}
			require.NoError(t, err)
			upstreamRequest := httptest.NewRequest(http.MethodPost, "https://provider.example/submit", body)
			require.NoError(t, adaptor.BuildRequestHeader(c, upstreamRequest, info))
			assert.Equal(t, "application/json", c.GetHeader("Content-Type"))
			mediaType, params, err := mime.ParseMediaType(upstreamRequest.Header.Get("Content-Type"))
			require.NoError(t, err)
			assert.Equal(t, "multipart/form-data", mediaType)
			require.NotEmpty(t, params["boundary"])
			require.NoError(t, upstreamRequest.ParseMultipartForm(1024))
			t.Cleanup(func() { require.NoError(t, upstreamRequest.MultipartForm.RemoveAll()) })
			assert.Equal(t, map[string][]string{
				"model": {"m"}, "prompt": {"paper plane"}, "tag": {"first", "second"}, "empty": {""},
			}, upstreamRequest.MultipartForm.Value)
			assert.Empty(t, upstreamRequest.MultipartForm.File)
		})
	}
}
