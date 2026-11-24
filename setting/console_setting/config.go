package console_setting

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type ConsoleSetting struct {
	ApiInfo                   string `json:"api_info"`                     // 控制台 API 信息 (JSON 数组字符串)
	UptimeKumaBaseURL         string `json:"uptime_kuma_base_url"`         // 历史兼容字段（不再轮询外部 Uptime Kuma）
	UptimeKumaGroups          string `json:"uptime_kuma_groups"`           // 模型探活分组配置 (JSON 数组字符串)
	ModelProbeIntervalMinutes int    `json:"model_probe_interval_minutes"` // 内置模型探活间隔（分钟，1-60）
	ImageProbeIntervalMinutes int    `json:"image_probe_interval_minutes"` // 内置图像模型探活间隔（分钟，15-360）
	ModelProbeTimeoutSeconds  int    `json:"model_probe_timeout_seconds"`  // 内置模型探活单次超时（秒，5-60）
	Announcements             string `json:"announcements"`                // 系统公告 (JSON 数组字符串)
	FAQ                       string `json:"faq"`                          // 常见问题 (JSON 数组字符串)
	ApiInfoEnabled            bool   `json:"api_info_enabled"`             // 是否启用 API 信息面板
	UptimeKumaEnabled         bool   `json:"uptime_kuma_enabled"`          // 是否启用模型状态探活面板
	AnnouncementsEnabled      bool   `json:"announcements_enabled"`        // 是否启用系统公告面板
	FAQEnabled                bool   `json:"faq_enabled"`                  // 是否启用常见问答面板
}

// 默认配置
var defaultConsoleSetting = ConsoleSetting{
	ApiInfo:                   "",
	UptimeKumaBaseURL:         "",
	UptimeKumaGroups:          "",
	ModelProbeIntervalMinutes: 10,
	ImageProbeIntervalMinutes: 60,
	ModelProbeTimeoutSeconds:  15,
	Announcements:             "",
	FAQ:                       "",
	ApiInfoEnabled:            true,
	UptimeKumaEnabled:         true,
	AnnouncementsEnabled:      true,
	FAQEnabled:                true,
}

// 全局实例
var consoleSetting = defaultConsoleSetting

func init() {
	// 注册到全局配置管理器，键名为 console_setting
	config.GlobalConfig.Register("console_setting", &consoleSetting)
}

// GetConsoleSetting 获取 ConsoleSetting 配置快照
func GetConsoleSetting() *ConsoleSetting {
	common.OptionMapRWMutex.RLock()
	snap := consoleSetting
	common.OptionMapRWMutex.RUnlock()
	return &snap
}
