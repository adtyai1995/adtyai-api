package controller

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCoerceTestUsage(t *testing.T) {
	var nilUsage *dto.Usage
	validUsage := &dto.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}
	for _, testCase := range []struct {
		name    string
		usage   any
		stream  bool
		want    *dto.Usage
		wantErr string
	}{
		{name: "nil non-stream", wantErr: "usage is nil"},
		{name: "typed nil non-stream", usage: nilUsage, wantErr: "usage is nil"},
		{name: "nil stream", stream: true, want: &dto.Usage{PromptTokens: 23, TotalTokens: 23}},
		{name: "typed nil stream", usage: nilUsage, stream: true, want: &dto.Usage{PromptTokens: 23, TotalTokens: 23}},
		{name: "pointer non-stream", usage: validUsage, want: validUsage},
		{name: "pointer stream", usage: validUsage, stream: true, want: validUsage},
		{name: "value non-stream", usage: *validUsage, want: validUsage},
		{name: "value stream", usage: *validUsage, stream: true, want: validUsage},
		{name: "invalid non-stream", usage: "invalid", wantErr: "invalid usage type: string"},
		{name: "invalid stream", usage: "invalid", stream: true, want: &dto.Usage{PromptTokens: 23, TotalTokens: 23}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			usage, err := coerceTestUsage(testCase.usage, testCase.stream, 23)
			if testCase.wantErr != "" {
				require.EqualError(t, err, testCase.wantErr)
				assert.Nil(t, usage)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, usage)
			assert.Equal(t, testCase.want, usage)
		})
	}
}

func TestRunChannelTestWorkersContinuesAfterPanic(t *testing.T) {
	originalInterval := common.RequestInterval
	common.RequestInterval = 0
	t.Cleanup(func() { common.RequestInterval = originalInterval })

	channels := []*model.Channel{
		{Id: 1, Status: common.ChannelStatusEnabled},
		{Id: 2, Status: common.ChannelStatusAutoDisabled},
		{Id: 3, Status: common.ChannelStatusEnabled},
	}
	var visited []int
	var progress []int
	summary := runChannelTestWorkers(
		context.Background(),
		channels,
		1,
		func(_ context.Context, channel *model.Channel) channelTestSummary {
			visited = append(visited, channel.Id)
			if channel.Id == 2 {
				var usage *dto.Usage
				return channelTestSummary{Tested: usage.PromptTokens}
			}
			return channelTestSummary{Tested: 1, Succeeded: 1}
		},
		func(processed, total int) {
			assert.Equal(t, len(channels), total)
			progress = append(progress, processed)
		},
	)

	assert.Equal(t, []int{1, 2, 3}, visited)
	assert.Equal(t, []int{0, 1, 2, 3}, progress)
	assert.Equal(t, channelTestSummary{Tested: 3, Succeeded: 2, Failed: 1}, summary)
	assert.Equal(t, common.ChannelStatusAutoDisabled, channels[1].Status)
}
