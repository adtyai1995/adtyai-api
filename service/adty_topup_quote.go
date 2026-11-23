package service

import (
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
)

// 支付商和支付方式（约定 C）。
const (
	AdtyProviderBEpusdt = "bepusdt"
	AdtyProviderEpay    = "adty_epay"

	AdtyMethodUSDTTRC20   = "usdt.trc20"
	AdtyMethodUSDTBEP20   = "usdt.bep20"
	AdtyMethodUSDTPolygon = "usdt.polygon"
	AdtyMethodUSDTXLayer  = "usdt.xlayer"
	AdtyMethodAlipay      = "alipay"
	AdtyMethodWxpay       = "wxpay"

	AdtyCurrencyUSDT = "USDT"
	AdtyCurrencyCNY  = "CNY"
)

// AdtyUSDTMethods 是支持的 USDT 链，值和 BEpusdt 的 trade_type 相同。
var AdtyUSDTMethods = []string{AdtyMethodUSDTTRC20, AdtyMethodUSDTBEP20, AdtyMethodUSDTPolygon, AdtyMethodUSDTXLayer}

// AdtyEpayMethods 是易支付的支付方式。
var AdtyEpayMethods = []string{AdtyMethodAlipay, AdtyMethodWxpay}

// IsAdtyUSDTMethod 说明这是不是一条 USDT 链。
func IsAdtyUSDTMethod(method string) bool {
	return slices.Contains(AdtyUSDTMethods, method)
}

// IsAdtyEpayMethod 说明这是不是易支付的方式。
func IsAdtyEpayMethod(method string) bool {
	return slices.Contains(AdtyEpayMethods, method)
}

// AdtyQuote 是一笔充值的报价：面额、加赠、入账额度和应付。
type AdtyQuote struct {
	TeamID          int64           `json:"-"`
	Business        string          `json:"business,omitempty"`
	SiteID          int             `json:"-"`
	FaceUSD         decimal.Decimal `json:"face_usd"`
	BonusRate       decimal.Decimal `json:"bonus_rate"` // 百分数，2.5 表示 2.5%
	BonusUSD        decimal.Decimal `json:"bonus_usd"`
	CreditQuota     int64           `json:"credit_quota"`
	PayAmount       decimal.Decimal `json:"pay_amount"`
	PayCurrency     string          `json:"pay_currency"`      // USDT | CNY
	PriceCNYPerUSD  decimal.Decimal `json:"price_cny_per_usd"` // 只有易支付用
	TopupGroupRatio decimal.Decimal `json:"topup_group_ratio"`
}

// AdtyQuoteFor 给一个分组的主站用户算一笔充值的报价。
func AdtyQuoteFor(userGroup string, face int64, method string) (*AdtyQuote, error) {
	return AdtyQuoteForSite(nil, userGroup, face, method)
}

// AdtyQuoteForSite 算一笔充值的报价；site 是客户所属的分站（主站传 nil），赠金取本站获授权的支付方式规则。
// TOKENS 显示模式会改变金额含义，这种模式下不报价。
func AdtyQuoteForSite(site *model.AdtySite, userGroup string, face int64, method string) (*AdtyQuote, error) {
	return adtyQuoteForSite(site, userGroup, face, method, false)
}

func adtyQuoteForSite(site *model.AdtySite, userGroup string, face int64, method string, owner bool) (*AdtyQuote, error) {
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		return nil, common.NewMessage("Top-up is unavailable while quota is displayed as tokens")
	}
	method = strings.TrimSpace(method)
	if !IsAdtyUSDTMethod(method) && !IsAdtyEpayMethod(method) {
		return nil, common.NewMessage("Unsupported payment method")
	}
	if face < int64(operation_setting.MinTopUp) || face <= 0 {
		return nil, common.NewMessage("Top-up amount cannot be less than {{amount}}", map[string]any{"amount": operation_setting.MinTopUp})
	}
	var quote *AdtyQuote
	var err error
	if owner {
		quote, err = adtyOwnerQuotaQuote(face)
	} else {
		quote, err = adtyQuotaQuoteForSite(site, userGroup, decimal.NewFromInt(face), method)
	}
	if err != nil {
		return nil, err
	}
	if IsAdtyUSDTMethod(method) {
		quote.PayCurrency = AdtyCurrencyUSDT
		quote.PayAmount = quote.FaceUSD.Mul(quote.TopupGroupRatio).Round(8)
		return quote, nil
	}
	price := decimal.NewFromFloat(operation_setting.Price)
	if !price.IsPositive() {
		return nil, common.NewMessage("The top-up price is not configured")
	}
	quote.PayCurrency = AdtyCurrencyCNY
	quote.PriceCNYPerUSD = price
	quote.PayAmount = quote.FaceUSD.Mul(price).Mul(quote.TopupGroupRatio).Round(2)
	return quote, nil
}

func adtyQuotaQuoteForSite(site *model.AdtySite, userGroup string, faceUSD decimal.Decimal, method string) (*AdtyQuote, error) {
	rate, bonus, err := AdtyPaymentMethodBonus(site, method, faceUSD)
	if err != nil {
		return nil, err
	}
	if site != nil && bonus.IsPositive() {
		if err := SiteBonusEarningCovers(site.Id, bonus); err != nil {
			return nil, err
		}
	}
	creditQuota := faceUSD.Add(bonus).Mul(common.QuotaPerUnitDecimal()).Floor()
	if _, err := common.WalletQuotaFromDecimalStrict(creditQuota); err != nil || !creditQuota.IsPositive() {
		return nil, common.NewMessage("Top-up quota exceeds the range the system can represent")
	}
	groupRatio := decimal.NewFromFloat(common.GetTopupGroupRatio(userGroup))
	if !groupRatio.IsPositive() {
		groupRatio = decimal.NewFromInt(1)
	}
	return &AdtyQuote{
		FaceUSD:         faceUSD,
		BonusRate:       rate,
		BonusUSD:        bonus,
		CreditQuota:     creditQuota.IntPart(),
		TopupGroupRatio: groupRatio,
	}, nil
}
