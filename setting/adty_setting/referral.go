package adty_setting

import (
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// ReferralSetting 是推广返佣的设置（任务 6.1），键名 adty_referral_setting.<json 标签>。
// 比例是百分数，10 表示 10%（执行层决定 18）。提现汇率用字符串存：空串表示未设置，
// 提交提现时直接查库，不读这里（任务 6.4）。
// 和钱有关的三项没有内置值（约定 I）：nil 表示未设置，存成 "null"。0% 和没设是两回事。
type ReferralSetting struct {
	Enabled                  bool     `json:"enabled"`
	DefaultRate              *float64 `json:"default_rate"` // 没设时不计佣，也不能打开 enabled
	CookieTTLDays            int      `json:"cookie_ttl_days"`
	FreezeDays               int      `json:"freeze_days"`
	MinWithdrawUSD           *float64 `json:"min_withdraw_usd"`  // 没设时不能提现到 USDT 和支付宝
	USDTWithdrawFee          *float64 `json:"usdt_withdraw_fee"` // 没设时不能提现到 USDT
	CancelWindowMinutes      int      `json:"cancel_window_minutes"`
	WithdrawCNYRate          string   `json:"withdraw_cny_rate"`
	WithdrawCNYRateUpdatedAt int64    `json:"withdraw_cny_rate_updated_at"`
	WithdrawMethods          []string `json:"withdraw_methods"`       // nil 表示历史未显式设置；[] 表示显式关闭所有外部提现方式
	WithdrawUSDTNetworks     []string `json:"withdraw_usdt_networks"` // nil 表示历史未显式设置；非 nil 为已启用的 USDT 链
	SelfInviteWindowDays     int      `json:"self_invite_window_days"`
	BatchRegisterThreshold   int      `json:"batch_register_threshold"`
	BatchRegisterWindowHours int      `json:"batch_register_window_hours"`
}

// 推广设置在 options 表里的键及外部提现方式常量。
const (
	ReferralKeyPrefix                   = "adty_referral_setting."
	ReferralKeyWithdrawCNYRate          = "adty_referral_setting.withdraw_cny_rate"
	ReferralKeyWithdrawCNYRateUpdatedAt = "adty_referral_setting.withdraw_cny_rate_updated_at"
	ReferralKeyWithdrawMethods          = "adty_referral_setting.withdraw_methods"
	ReferralKeyWithdrawUSDTNetworks     = "adty_referral_setting.withdraw_usdt_networks"

	ReferralWithdrawMethodUSDT   = "usdt"
	ReferralWithdrawMethodAlipay = "alipay"
)

var (
	canonicalReferralWithdrawMethods      = []string{ReferralWithdrawMethodUSDT, ReferralWithdrawMethodAlipay}
	canonicalReferralWithdrawUSDTNetworks = []string{"TRC20", "BEP20", "POLYGON", "XLAYER"}
)

// AllReferralWithdrawUSDTNetworks 返回全部 4 条支持的 USDT 提现链（提现固定 4 链；BEpusdt 充值另支持 ERC20 共 5 链）。
func AllReferralWithdrawUSDTNetworks() []string {
	return slices.Clone(canonicalReferralWithdrawUSDTNetworks)
}

// NormalizeReferralWithdrawMethods 校验并按规范顺序去重外部提现方式（仅允许 usdt、alipay）。
func NormalizeReferralWithdrawMethods(in []string) ([]string, bool) {
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		item := strings.ToLower(strings.TrimSpace(raw))
		if item != ReferralWithdrawMethodUSDT && item != ReferralWithdrawMethodAlipay {
			return nil, false
		}
		seen[item] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for _, method := range canonicalReferralWithdrawMethods {
		if _, ok := seen[method]; ok {
			out = append(out, method)
		}
	}
	return out, true
}

// NormalizeReferralWithdrawUSDTNetworks 校验并按规范顺序去重 USDT 提现链。
func NormalizeReferralWithdrawUSDTNetworks(in []string) ([]string, bool) {
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		item := strings.ToUpper(strings.TrimSpace(raw))
		if !slices.Contains(canonicalReferralWithdrawUSDTNetworks, item) {
			return nil, false
		}
		seen[item] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for _, network := range canonicalReferralWithdrawUSDTNetworks {
		if _, ok := seen[network]; ok {
			out = append(out, network)
		}
	}
	return out, true
}

func normalizeValidReferralWithdrawMethods(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		item := strings.ToLower(strings.TrimSpace(raw))
		if item == ReferralWithdrawMethodUSDT || item == ReferralWithdrawMethodAlipay {
			seen[item] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for _, method := range canonicalReferralWithdrawMethods {
		if _, ok := seen[method]; ok {
			out = append(out, method)
		}
	}
	return out
}

func normalizeValidReferralWithdrawUSDTNetworks(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		item := strings.ToUpper(strings.TrimSpace(raw))
		if slices.Contains(canonicalReferralWithdrawUSDTNetworks, item) {
			seen[item] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for _, network := range canonicalReferralWithdrawUSDTNetworks {
		if _, ok := seen[network]; ok {
			out = append(out, network)
		}
	}
	return out
}

// EffectiveWithdrawConfig 返回生效的外部提现方式与 USDT 链配置（始终返回非 nil 切片）。
// 当 WithdrawMethods 为 nil（历史未显式配置）时，按已有金额与汇率配置推导默认值。
func (s *ReferralSetting) EffectiveWithdrawConfig(hasPositiveCNYRate bool) (methods []string, usdtNetworks []string) {
	if s == nil {
		return []string{}, []string{}
	}
	if s.WithdrawMethods != nil {
		methods = normalizeValidReferralWithdrawMethods(s.WithdrawMethods)
		if s.WithdrawUSDTNetworks != nil {
			usdtNetworks = normalizeValidReferralWithdrawUSDTNetworks(s.WithdrawUSDTNetworks)
		} else {
			usdtNetworks = []string{}
		}
		return methods, usdtNetworks
	}
	methods = []string{}
	usdtNetworks = []string{}
	if s.MinWithdrawUSD != nil && s.USDTWithdrawFee != nil {
		methods = append(methods, ReferralWithdrawMethodUSDT)
		if s.WithdrawUSDTNetworks != nil {
			usdtNetworks = normalizeValidReferralWithdrawUSDTNetworks(s.WithdrawUSDTNetworks)
		} else {
			usdtNetworks = AllReferralWithdrawUSDTNetworks()
		}
	}
	if s.MinWithdrawUSD != nil && hasPositiveCNYRate {
		methods = append(methods, ReferralWithdrawMethodAlipay)
	}
	return methods, usdtNetworks
}

// UsableWithdrawConfig 返回实际可供推广员提交提现的外部方式和 USDT 链（同时校验对应参数已齐备）。
func (s *ReferralSetting) UsableWithdrawConfig(hasPositiveCNYRate bool) (methods []string, usdtNetworks []string) {
	if s == nil {
		return []string{}, []string{}
	}
	rawMethods, rawNetworks := s.EffectiveWithdrawConfig(hasPositiveCNYRate)
	methods = []string{}
	usdtNetworks = []string{}
	if slices.Contains(rawMethods, ReferralWithdrawMethodUSDT) && s.MinWithdrawUSD != nil && s.USDTWithdrawFee != nil && len(rawNetworks) > 0 {
		methods = append(methods, ReferralWithdrawMethodUSDT)
		usdtNetworks = rawNetworks
	}
	if slices.Contains(rawMethods, ReferralWithdrawMethodAlipay) && s.MinWithdrawUSD != nil && hasPositiveCNYRate {
		methods = append(methods, ReferralWithdrawMethodAlipay)
	}
	return methods, usdtNetworks
}

var referralSetting = ReferralSetting{
	CookieTTLDays:            30,
	FreezeDays:               0,
	CancelWindowMinutes:      30,
	SelfInviteWindowDays:     30,
	BatchRegisterThreshold:   3,
	BatchRegisterWindowHours: 24,
}

func init() {
	config.GlobalConfig.Register("adty_referral_setting", &referralSetting)
}

// GetReferralSetting 返回当前的推广设置快照。
func GetReferralSetting() *ReferralSetting {
	common.OptionMapRWMutex.RLock()
	snap := referralSetting
	snap.DefaultRate = cloneFloat64Ptr(referralSetting.DefaultRate)
	snap.MinWithdrawUSD = cloneFloat64Ptr(referralSetting.MinWithdrawUSD)
	snap.USDTWithdrawFee = cloneFloat64Ptr(referralSetting.USDTWithdrawFee)
	snap.WithdrawMethods = slices.Clone(referralSetting.WithdrawMethods)
	snap.WithdrawUSDTNetworks = slices.Clone(referralSetting.WithdrawUSDTNetworks)
	common.OptionMapRWMutex.RUnlock()
	return &snap
}
