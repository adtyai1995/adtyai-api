package adty_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateEpayTimeoutWithAuthoritativeReader(t *testing.T) {
	oldChannel := epaySetting.ChannelTimeoutSeconds
	oldTotal := epaySetting.TotalTimeoutSeconds
	defer func() {
		epaySetting.ChannelTimeoutSeconds = oldChannel
		epaySetting.TotalTimeoutSeconds = oldTotal
		SetAuthoritativeEpayTimeoutReader(nil)
	}()

	// Local in-memory cache has TotalTimeoutSeconds = 20, while authoritative SQL has TotalTimeoutSeconds = 8.
	epaySetting.ChannelTimeoutSeconds = 5
	epaySetting.TotalTimeoutSeconds = 20
	SetAuthoritativeEpayTimeoutReader(func() (map[string]string, error) {
		return map[string]string{
			EpaySettingKeyChannelTimeout: "5",
			EpaySettingKeyTotalTimeout:   "8",
		}, nil
	})

	// Updating channel_timeout_seconds to 10 must be rejected against authoritative total=8.
	require.Error(t, ValidateEpayTimeout(EpaySettingKeyChannelTimeout, "10"))
	require.NoError(t, ValidateEpayTimeout(EpaySettingKeyChannelTimeout, "8"))
}

func TestValidateEpayTimeoutWithCurrentCrossKeyBounds(t *testing.T) {
	oldChannel := epaySetting.ChannelTimeoutSeconds
	oldTotal := epaySetting.TotalTimeoutSeconds
	defer func() {
		epaySetting.ChannelTimeoutSeconds = oldChannel
		epaySetting.TotalTimeoutSeconds = oldTotal
	}()

	current := map[string]string{
		EpaySettingKeyChannelTimeout: "6",
		EpaySettingKeyTotalTimeout:   "12",
	}

	ch, tot, err := ValidateEpayTimeoutWithCurrent(EpaySettingKeyChannelTimeout, "10", current)
	require.NoError(t, err)
	assert.Equal(t, 10, ch)
	assert.Equal(t, 12, tot)

	_, _, err = ValidateEpayTimeoutWithCurrent(EpaySettingKeyTotalTimeout, "5", current)
	require.Error(t, err)

	_, _, err = ValidateEpayTimeoutWithCurrent(EpaySettingKeyTotalTimeout, "31", current)
	require.Error(t, err)

	_, _, err = ValidateEpayTimeoutWithCurrent(EpaySettingKeyChannelTimeout, "0", current)
	require.Error(t, err)

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()

	SyncEpayTimeouts(7, 15)
	assert.Equal(t, 7, GetEpaySetting().ChannelTimeoutSeconds)
	assert.Equal(t, 15, GetEpaySetting().TotalTimeoutSeconds)
}
