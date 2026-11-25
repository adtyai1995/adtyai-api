package setting

import (
	"net/url"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

const (
	BEpusdtOptionKeyEnabled    = "adty_bepusdt.enabled"
	BEpusdtOptionKeyBaseURL    = "adty_bepusdt.base_url"
	BEpusdtOptionKeySecret     = "adty_bepusdt.secret"
	BEpusdtOptionKeyTradeTypes = "adty_bepusdt.trade_types"
)

// ValidBEpusdtTradeTypes lists the supported USDT trade_type values accepted by BEpusdt.
var ValidBEpusdtTradeTypes = []string{
	"usdt.trc20",
	"usdt.bep20",
	"usdt.polygon",
	"usdt.xlayer",
}

// BEpusdtSetting 是 USDT 网关的配置（任务 4.1），键名 adty_bepusdt.<json 标签>。
// secret 以 secret 结尾，GET /api/option/ 会把它过滤掉。
type BEpusdtSetting struct {
	Enabled    bool     `json:"enabled"`
	BaseURL    string   `json:"base_url"`
	Secret     string   `json:"secret"`
	TradeTypes []string `json:"trade_types"` // usdt.trc20、usdt.bep20、usdt.polygon、usdt.xlayer
}

var bepusdtSetting = BEpusdtSetting{
	TradeTypes: []string{"usdt.trc20"},
}

func init() {
	config.GlobalConfig.Register("adty_bepusdt", &bepusdtSetting)
}

// GetBEpusdtSetting 返回当前的 BEpusdt 配置快照。
func GetBEpusdtSetting() *BEpusdtSetting {
	common.OptionMapRWMutex.RLock()
	snap := bepusdtSetting
	snap.TradeTypes = slices.Clone(bepusdtSetting.TradeTypes)
	common.OptionMapRWMutex.RUnlock()
	return &snap
}

// ValidateBEpusdtBaseURL trims and validates adty_bepusdt.base_url.
// Empty values are allowed when BEpusdt is disabled; non-empty values must be
// valid http:// or https:// URLs with a host and no userinfo, query, or fragment.
func ValidateBEpusdtBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed == nil || strings.ContainsAny(trimmed, " \t\r\n") ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		strings.TrimSpace(parsed.Host) == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", common.NewMessage("BEpusdt gateway URL must be a valid http:// or https:// URL without credentials, query, or fragment")
	}
	return trimmed, nil
}

// ValidateBEpusdtTradeTypes validates adty_bepusdt.trade_types as a JSON string
// array containing only supported USDT trade types, and returns its canonical
// deduplicated JSON representation.
func ValidateBEpusdtTradeTypes(raw string) (string, error) {
	var parsed []string
	if err := common.UnmarshalJsonStr(strings.TrimSpace(raw), &parsed); err != nil || parsed == nil {
		return "", common.NewMessage("BEpusdt trade types must be a JSON array of supported USDT chains")
	}
	deduped := make([]string, 0, len(parsed))
	for _, entry := range parsed {
		if !slices.Contains(ValidBEpusdtTradeTypes, entry) {
			return "", common.NewMessage("Unsupported BEpusdt trade type: {{type}}", map[string]any{"type": entry})
		}
		if !slices.Contains(deduped, entry) {
			deduped = append(deduped, entry)
		}
	}
	encoded, err := common.Marshal(deduped)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// BEpusdtHasValidTradeType reports whether tradeTypes includes at least one supported USDT chain.
func BEpusdtHasValidTradeType(tradeTypes []string) bool {
	for _, entry := range tradeTypes {
		if slices.Contains(ValidBEpusdtTradeTypes, entry) {
			return true
		}
	}
	return false
}

// BEpusdtConfigured 说明网关地址和密钥都已填写并且启用。
func BEpusdtConfigured() bool {
	cfg := GetBEpusdtSetting()
	if !cfg.Enabled || strings.TrimSpace(cfg.Secret) == "" {
		return false
	}
	baseURL, err := ValidateBEpusdtBaseURL(cfg.BaseURL)
	return err == nil && baseURL != ""
}

// BEpusdtChainEnabled 说明这条链已启用。
func BEpusdtChainEnabled(tradeType string) bool {
	return slices.Contains(GetBEpusdtSetting().TradeTypes, tradeType)
}
