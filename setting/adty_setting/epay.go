package adty_setting

import (
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// EpaySetting 是易支付多通道的运行设置（任务 5.3），键名 adty_epay_setting.<json 标签>。
type EpaySetting struct {
	UnpaidThreshold      int  `json:"unpaid_threshold"`       // 同一通道连续未付多少笔转维护
	ProbeEnabled         bool `json:"probe_enabled"`          // 探活默认关
	ProbeIntervalMinutes int  `json:"probe_interval_minutes"` // 探活间隔
	QueryAfterSeconds    int  `json:"query_after_seconds"`    // 下单多久还没回调就补查
	ExpiredQuerySeconds  int  `json:"expired_query_seconds"`  // 过期后还继续补查多久，0 表示过期后不再查
	// 换通道的时限（任务 3.6）：单个通道等多久，一次下单最多等多久。保存时校验 单通道 ≤ 总时长 ≤ 30。
	ChannelTimeoutSeconds int `json:"channel_timeout_seconds"`
	TotalTimeoutSeconds   int `json:"total_timeout_seconds"`
}

// 换通道时限的配置键，保存前要校验（controller/option.go）。
const (
	EpaySettingKeyChannelTimeout     = "adty_epay_setting.channel_timeout_seconds"
	EpaySettingKeyTotalTimeout       = "adty_epay_setting.total_timeout_seconds"
	DefaultEpayChannelTimeoutSeconds = 5
	DefaultEpayTotalTimeoutSeconds   = 12
	epayMaxTotalTimeoutSeconds       = 30
)

var (
	epayTimeoutReaderMu sync.RWMutex
	epayTimeoutReader   func() (map[string]string, error)
)

var epaySetting = EpaySetting{
	UnpaidThreshold:       10,
	ProbeEnabled:          false,
	ProbeIntervalMinutes:  5,
	QueryAfterSeconds:     120,
	ExpiredQuerySeconds:   24 * 60 * 60,
	ChannelTimeoutSeconds: DefaultEpayChannelTimeoutSeconds,
	TotalTimeoutSeconds:   DefaultEpayTotalTimeoutSeconds,
}

func init() {
	config.GlobalConfig.Register("adty_epay_setting", &epaySetting)
}

// GetEpaySetting 返回当前易支付设置的只读快照副本。
func GetEpaySetting() *EpaySetting {
	common.OptionMapRWMutex.RLock()
	snap := epaySetting
	common.OptionMapRWMutex.RUnlock()
	return &snap
}

// IsEpayTimeoutKey 判断配置键是否属于易支付换通道时限。
func IsEpayTimeoutKey(key string) bool {
	return key == EpaySettingKeyChannelTimeout || key == EpaySettingKeyTotalTimeout
}

// SetAuthoritativeEpayTimeoutReader 注册用于读取当前权威 SQL 时限配置的函数。
func SetAuthoritativeEpayTimeoutReader(reader func() (map[string]string, error)) {
	epayTimeoutReaderMu.Lock()
	defer epayTimeoutReaderMu.Unlock()
	epayTimeoutReader = reader
}

// ValidateEpayTimeoutPair 校验单通道与总时长的配对关系：1 ≤ 单通道 ≤ 总时长 ≤ 30。
func ValidateEpayTimeoutPair(channelSeconds, totalSeconds int) error {
	if channelSeconds < 1 || channelSeconds > totalSeconds || totalSeconds > epayMaxTotalTimeoutSeconds {
		return common.NewMessage("The per-channel timeout must be at least 1 second and not above the total, and the total at most 30 seconds")
	}
	return nil
}

// ValidateEpayTimeoutWithCurrent 基于给定的当前权威键值映射校验单个时限键的写入，并返回校验通过后的 (channel, total)。
func ValidateEpayTimeoutWithCurrent(key, value string, current map[string]string) (int, int, error) {
	if !IsEpayTimeoutKey(key) {
		return 0, 0, common.NewMessage("Invalid parameters")
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, 0, common.NewMessage("Timeouts must be whole seconds")
	}
	channel, total := DefaultEpayChannelTimeoutSeconds, DefaultEpayTotalTimeoutSeconds
	if channel < 1 {
		channel = DefaultEpayChannelTimeoutSeconds
	}
	if total < 1 {
		total = DefaultEpayTotalTimeoutSeconds
	}
	if current != nil {
		if rawChannel, ok := current[EpaySettingKeyChannelTimeout]; ok && strings.TrimSpace(rawChannel) != "" {
			parsedChannel, parseErr := strconv.Atoi(strings.TrimSpace(rawChannel))
			if parseErr != nil {
				return 0, 0, common.NewMessage("Timeouts must be whole seconds")
			}
			channel = parsedChannel
		}
		if rawTotal, ok := current[EpaySettingKeyTotalTimeout]; ok && strings.TrimSpace(rawTotal) != "" {
			parsedTotal, parseErr := strconv.Atoi(strings.TrimSpace(rawTotal))
			if parseErr != nil {
				return 0, 0, common.NewMessage("Timeouts must be whole seconds")
			}
			total = parsedTotal
		}
	}
	if key == EpaySettingKeyChannelTimeout {
		channel = seconds
	} else {
		total = seconds
	}
	if err := ValidateEpayTimeoutPair(channel, total); err != nil {
		return 0, 0, err
	}
	return channel, total, nil
}

// SyncEpayTimeouts 将已通过校验的权威单通道与总时长同步到进程内存及 OptionMap。
func SyncEpayTimeouts(channelSeconds, totalSeconds int) {
	if ValidateEpayTimeoutPair(channelSeconds, totalSeconds) != nil {
		return
	}
	common.OptionMapRWMutex.Lock()
	epaySetting.ChannelTimeoutSeconds = channelSeconds
	epaySetting.TotalTimeoutSeconds = totalSeconds
	if common.OptionMap != nil {
		common.OptionMap[EpaySettingKeyChannelTimeout] = strconv.Itoa(channelSeconds)
		common.OptionMap[EpaySettingKeyTotalTimeout] = strconv.Itoa(totalSeconds)
	}
	common.OptionMapRWMutex.Unlock()
}

// ValidateEpayTimeout 校验换通道时限的一个键：优先使用权威读取器获取另一个键的当前值，满足 1 ≤ 单通道 ≤ 总时长 ≤ 30。
func ValidateEpayTimeout(key, value string) error {
	epayTimeoutReaderMu.RLock()
	reader := epayTimeoutReader
	epayTimeoutReaderMu.RUnlock()
	var current map[string]string
	if reader != nil {
		loaded, err := reader()
		if err != nil {
			return err
		}
		current = loaded
	}
	_, _, err := ValidateEpayTimeoutWithCurrent(key, value, current)
	return err
}
