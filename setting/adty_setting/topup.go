package adty_setting

import (
	"maps"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// TopupSetting 是充值加赠和订单有效期（任务 3.1）。键名是 adty_topup_setting.<json 标签>。
type TopupSetting struct {
	BonusRates          map[int]float64                    `json:"bonus_rates"`            // 已退役的金额档位，不参与新订单赠金
	MethodBonus         map[string]MethodBonusRule         `json:"method_bonus"`           // 主站按支付方式的赠金开关和比例
	SiteMethodAuth      map[int][]string                   `json:"site_method_auth"`       // Root 授权分站可配置赠金的支付方式
	SiteMethodBonus     map[int]map[string]MethodBonusRule `json:"site_method_bonus"`      // 站长在授权范围内的本站规则
	SiteBonusMarginFill map[int]bool                       `json:"site_bonus_margin_fill"` // Root 授权用保证金补位分站赠金
	USDTOrderMinutes    int                                `json:"usdt_order_minutes"`     // USDT 订单有效期
	EpayOrderMinutes    int                                `json:"epay_order_minutes"`     // 易支付订单有效期
}

// TopupSettingKeyBonusRates 是已退役的金额档位配置键，保留用于写入拒绝（controller/option.go）。
const TopupSettingKeyBonusRates = "adty_topup_setting.bonus_rates"

// 加赠没有内置档位（约定 I）：后台没设时不加赠，档位金额沿用上游的 payment_setting.amount_options。
var topupSetting = TopupSetting{
	BonusRates:       map[int]float64{},
	USDTOrderMinutes: 15,
	EpayOrderMinutes: 10,
}

func init() {
	config.GlobalConfig.Register("adty_topup_setting", &topupSetting)
}

// GetTopupSetting 返回当前充值设置的深拷贝快照，防止与并发配置更新产生 map 读写竞争。
func GetTopupSetting() *TopupSetting {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	snap := TopupSetting{
		USDTOrderMinutes: topupSetting.USDTOrderMinutes,
		EpayOrderMinutes: topupSetting.EpayOrderMinutes,
	}
	if topupSetting.BonusRates != nil {
		snap.BonusRates = maps.Clone(topupSetting.BonusRates)
	}
	if topupSetting.MethodBonus != nil {
		snap.MethodBonus = maps.Clone(topupSetting.MethodBonus)
	}
	if topupSetting.SiteMethodAuth != nil {
		snap.SiteMethodAuth = make(map[int][]string, len(topupSetting.SiteMethodAuth))
		for k, v := range topupSetting.SiteMethodAuth {
			snap.SiteMethodAuth[k] = slices.Clone(v)
		}
	}
	if topupSetting.SiteMethodBonus != nil {
		snap.SiteMethodBonus = make(map[int]map[string]MethodBonusRule, len(topupSetting.SiteMethodBonus))
		for k, v := range topupSetting.SiteMethodBonus {
			snap.SiteMethodBonus[k] = maps.Clone(v)
		}
	}
	if topupSetting.SiteBonusMarginFill != nil {
		snap.SiteBonusMarginFill = maps.Clone(topupSetting.SiteBonusMarginFill)
	}
	return &snap
}
