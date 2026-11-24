package common_test

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
)

// /v1/models reports the DeepSeek Search and Fetch routing names with their
// own endpoint on the channels that relay them; chat models report every
// protocol DeepSeek serves.
func TestDeepSeekToolRoutingNamesReportTheirEndpoint(t *testing.T) {
	for _, channelType := range []int{constant.ChannelTypeDeepSeek, constant.ChannelTypeNewAPI} {
		assert.Equal(t, []constant.EndpointType{constant.EndpointTypeDeepSeekToolsSearch}, common.GetEndpointTypesByChannelType(channelType, constant.DeepSeekSearchModel))
		assert.Equal(t, []constant.EndpointType{constant.EndpointTypeDeepSeekToolsFetch}, common.GetEndpointTypesByChannelType(channelType, constant.DeepSeekFetchModel))
	}
	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
		constant.EndpointTypeAnthropic,
	}, common.GetEndpointTypesByChannelType(constant.ChannelTypeDeepSeek, "deepseek-flash"))
}
