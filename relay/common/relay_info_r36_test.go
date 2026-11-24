package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
)

func TestRelayInfoConvOptionsResponsesPromptCaching(t *testing.T) {
	nativeURL := constant.GetChannelBaseURL(constant.ChannelTypeAnthropic)

	native := &RelayInfo{
		ChannelMeta: &ChannelMeta{
			ChannelType:    constant.ChannelTypeAnthropic,
			ChannelBaseUrl: nativeURL + "/",
		},
	}
	assert.True(t, native.ConvOptions().Claude.ResponsesPromptCaching)

	custom := &RelayInfo{
		ChannelMeta: &ChannelMeta{
			ChannelType:    constant.ChannelTypeAnthropic,
			ChannelBaseUrl: "https://proxy.example.com",
		},
	}
	assert.False(t, custom.ConvOptions().Claude.ResponsesPromptCaching)

	other := &RelayInfo{
		ChannelMeta: &ChannelMeta{
			ChannelType:    constant.ChannelTypeOpenAI,
			ChannelBaseUrl: nativeURL,
		},
	}
	assert.False(t, other.ConvOptions().Claude.ResponsesPromptCaching)
}
