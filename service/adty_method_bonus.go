package service

import (
	"errors"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var adtyCanonicalBonusMethods = []string{AdtyMethodAlipay, AdtyMethodWxpay, "usdt"}

// AdtyBonusMethod is the canonical payment-method key for gift rules.
// USDT chains share one rule. Amount-tier rates are not a bonus authority.
func AdtyBonusMethod(method string) string {
	method = strings.ToLower(strings.TrimSpace(method))
	if strings.HasPrefix(method, "usdt") {
		return "usdt"
	}
	switch method {
	case AdtyMethodAlipay, AdtyMethodWxpay:
		return method
	default:
		return ""
	}
}

// AdtyPaymentMethodBonus returns the snapshot rate and USD gift for one order.
// Rate is a decimal fraction: 0.025 means 2.5% of the USD principal.
// Amount-tier tables do not affect this result.
func AdtyPaymentMethodBonus(site *model.AdtySite, method string, faceUSD decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
	key := AdtyBonusMethod(method)
	if key == "" || !faceUSD.IsPositive() {
		return decimal.Zero, decimal.Zero, nil
	}
	rule, ok := adtyMethodBonusRule(site, key)
	if !ok || !rule.Enabled {
		return decimal.Zero, decimal.Zero, nil
	}
	rate, err := common.ParseBoundedDecimalString(rule.Rate, 4, 8)
	if err != nil || rate.IsNegative() || rate.GreaterThan(decimal.NewFromInt(1)) {
		return decimal.Zero, decimal.Zero, err
	}
	return rate, faceUSD.Mul(rate).Round(8), nil
}

func adtyMethodBonusRule(site *model.AdtySite, key string) (adty_setting.MethodBonusRule, bool) {
	if site != nil {
		return adty_setting.SiteMethodBonus(site.Id, key)
	}
	return adty_setting.MainMethodBonus(key)
}

// EnabledMainSiteBonusMethods returns the canonical bonus method keys
// ("alipay", "wxpay", "usdt") currently enabled on the main site across active
// wallet_topup payment scenes and configured providers/channels, in fixed order.
func EnabledMainSiteBonusMethods() []string {
	enabled := map[string]bool{}
	usedScenes := false
	if model.DB != nil {
		if cfg, err := GetAdtyPaymentScenes(); err == nil {
			usedScenes = true
			for _, scene := range cfg.Scenes {
				if !scene.Enabled || scene.Business != model.AdtyBusinessWalletTopup {
					continue
				}
				for _, provider := range AdtyPaymentProviders() {
					for _, method := range provider.Methods() {
						if !method.Available || !adtySceneMethod(scene, provider.Name(), method.Id, method.Currency) {
							continue
						}
						if method.Kind == "epay" {
							channelMethods, chErr := adtyEpaySceneChannelMethods(scene, method)
							if chErr != nil || len(channelMethods) == 0 {
								continue
							}
						}
						if key := AdtyBonusMethod(method.Id); key != "" {
							enabled[key] = true
						}
					}
				}
			}
		}
	}
	if !usedScenes {
		for _, id := range AdtyAvailablePaymentMethodIDs() {
			if key := AdtyBonusMethod(id); key != "" {
				enabled[key] = true
			}
		}
	}
	out := make([]string, 0, len(adtyCanonicalBonusMethods))
	for _, method := range adtyCanonicalBonusMethods {
		if enabled[method] {
			out = append(out, method)
		}
	}
	return out
}

// IsMainSiteBonusMethodEnabled reports whether method (raw or canonical) is
// currently enabled on the main site.
func IsMainSiteBonusMethodEnabled(method string) bool {
	key := AdtyBonusMethod(method)
	if key == "" {
		return false
	}
	return slices.Contains(EnabledMainSiteBonusMethods(), key)
}

// AdtyActiveMethodBonusRates returns authoritative active bonus rates as decimal
// fraction strings in (0, 1] (e.g. "0.05" for 5%) keyed by canonical method
// ("alipay", "wxpay", "usdt"). Only enabled rules with positive rates are included.
// Team and site-owner businesses do not receive recharge method bonuses.
func AdtyActiveMethodBonusRates(userID int, requestSiteID int, business string) map[string]string {
	out := map[string]string{}
	biz := strings.TrimSpace(business)
	if biz != "" && biz != model.AdtyBusinessWalletTopup {
		return out
	}
	siteID := requestSiteID
	if userID > 0 && model.DB != nil {
		var user model.User
		if err := model.DB.Select("id, site_id").Where("id = ?", userID).Limit(1).Find(&user).Error; err == nil && user.Id > 0 {
			siteID = user.SiteId
		}
	}
	if siteID > 0 && model.DB != nil {
		var site model.AdtySite
		if err := model.DB.Select("id, status").Where("id = ?", siteID).Limit(1).Find(&site).Error; err != nil || site.Id == 0 || site.Status != model.AdtySiteStatusActive {
			return out
		}
	}
	for _, method := range adtyCanonicalBonusMethods {
		var (
			rule adty_setting.MethodBonusRule
			ok   bool
		)
		if siteID > 0 {
			rule, ok = adty_setting.SiteMethodBonus(siteID, method)
		} else {
			rule, ok = adty_setting.MainMethodBonus(method)
		}
		if !ok || !rule.Enabled {
			continue
		}
		rateDec, err := common.ParseBoundedDecimalString(rule.Rate, 4, 8)
		if err != nil || !rateDec.IsPositive() || rateDec.GreaterThan(decimal.NewFromInt(1)) {
			continue
		}
		out[method] = rateDec.String()
	}
	return out
}

// AdtySiteBonusMethod is one main-site-enabled payment method available for a
// subsite's gift configuration, with the owner's current rule. Rate is a decimal
// fraction in [0, 1], not a percent.
type AdtySiteBonusMethod struct {
	Method        string `json:"method"`
	Enabled       bool   `json:"enabled"`
	Rate          string `json:"rate"`
	Authorized    bool   `json:"authorized"`
	EnabledByMain bool   `json:"enabled_by_main"`
}

// AdtySiteBonusMethods lists all payment methods currently enabled on the main
// site in fixed order (alipay, wxpay, usdt) with the subsite owner's saved rules.
func AdtySiteBonusMethods(site *model.AdtySite) []AdtySiteBonusMethod {
	if site == nil {
		return nil
	}
	mainMethods := EnabledMainSiteBonusMethods()
	methods := make([]AdtySiteBonusMethod, 0, len(mainMethods))
	for _, method := range mainMethods {
		rule, configured := adty_setting.SiteMethodBonus(site.Id, method)
		rate := "0"
		if configured {
			rate = rule.Rate
		}
		methods = append(methods, AdtySiteBonusMethod{
			Method:        method,
			Enabled:       configured && rule.Enabled,
			Rate:          rate,
			Authorized:    true,
			EnabledByMain: true,
		})
	}
	return methods
}

// SiteBonusEarningCovers refuses a promised site gift unless earning cash covers it,
// or Root authorized margin fill and the open margin cycle can cover the remainder.
func SiteBonusEarningCovers(siteID int, bonus decimal.Decimal) error {
	if siteID <= 0 || !bonus.IsPositive() || model.DB == nil {
		return nil
	}
	var earning model.AdtySiteWallet
	if err := model.DB.Where("site_id = ? AND kind = ?", siteID, model.AdtySiteWalletEarnings).Limit(1).Find(&earning).Error; err != nil {
		return err
	}
	earningAvailable := earning.Balance.Sub(earning.FrozenUSD)
	pooledErr := ReviewAdtyPooledSiteFinance(model.DB, siteID)
	pooledSite := errors.Is(pooledErr, ErrAdtyOwnerWalletReview)
	if pooledErr != nil && !pooledSite {
		return pooledErr
	}
	if pooledSite && earning.Id != 0 {
		owner, ownerErr := model.CurrentAdtySiteFinancialOwnerTx(model.DB, siteID)
		if ownerErr != nil {
			return ownerErr
		}
		ownerAvail, _, availErr := ownerEarningsWalletTx(model.DB, siteID, owner, &earning)
		if errors.Is(availErr, ErrAdtyOwnerWalletReview) {
			earningAvailable = decimal.Zero
		} else if availErr != nil {
			return availErr
		} else {
			earningAvailable = decimal.Min(earningAvailable, ownerAvail)
		}
	}
	if earningAvailable.IsNegative() {
		earningAvailable = decimal.Zero
	}
	if earning.Id != 0 && earningAvailable.GreaterThanOrEqual(bonus) {
		return nil
	}
	short := bonus
	if earning.Id != 0 {
		short = bonus.Sub(earningAvailable)
	}
	if !adty_setting.SiteBonusMarginFillAllowed(siteID) {
		return common.NewMessage("Site gift reserve is not covered by the earning wallet")
	}
	if pooledSite {
		return common.NewMessage("Site gift reserve is not covered by the earning wallet or authorized margin")
	}
	var margin model.AdtySiteWallet
	if err := model.DB.Where("site_id = ? AND kind = ?", siteID, model.AdtySiteWalletMargin).Limit(1).Find(&margin).Error; err != nil {
		return err
	}
	allowed := AdtyMarginNewDraw(AdtyMarginDrawInput{
		TierUSD: margin.CycleTierUSD, Balance: margin.Balance, Used: margin.CycleUsedUSD, Frozen: margin.FrozenUSD, ExcessFrozen: margin.ExcessFrozenUSD,
	})
	if margin.Id == 0 || short.GreaterThan(allowed) {
		return common.NewMessage("Site gift reserve is not covered by the earning wallet or authorized margin")
	}
	return nil
}

// ReserveAdtySiteGiftTx freezes the site-funded gift from the earning wallet,
// then from an authorized margin cycle. The reserved amount is the same
// site-funded total as adtyBonusUSDForSite: the payment-method gift.
func ReserveAdtySiteGiftTx(tx *gorm.DB, ext *model.AdtyTopupOrder, marginAuthorization ...bool) error {
	if tx == nil || ext == nil || ext.SiteId <= 0 {
		return nil
	}
	bonus := adtyBonusUSDForSite(ext)
	if !bonus.IsPositive() {
		return nil
	}
	if err := ensureAdtySiteWalletsTx(tx, ext.SiteId); err != nil {
		return err
	}
	var earning model.AdtySiteWallet
	if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND kind = ?", ext.SiteId, model.AdtySiteWalletEarnings).First(&earning).Error; err != nil {
		return err
	}
	fromEarning := earning.Balance.Sub(earning.FrozenUSD)
	pooledErr := ReviewAdtyPooledSiteFinance(tx, ext.SiteId)
	pooledSite := errors.Is(pooledErr, ErrAdtyOwnerWalletReview)
	if pooledErr != nil && !pooledSite {
		return pooledErr
	}
	if pooledSite {
		owner := ext.SiteOwnerUserId
		if owner <= 0 {
			var ownerErr error
			owner, ownerErr = model.CurrentAdtySiteFinancialOwnerTx(tx, ext.SiteId)
			if ownerErr != nil {
				return ownerErr
			}
		}
		ownerAvail, _, availErr := ownerEarningsWalletTx(tx, ext.SiteId, owner, &earning)
		if errors.Is(availErr, ErrAdtyOwnerWalletReview) {
			fromEarning = decimal.Zero
		} else if availErr != nil {
			return availErr
		} else {
			fromEarning = decimal.Min(fromEarning, ownerAvail)
		}
	}
	if fromEarning.GreaterThan(bonus) {
		fromEarning = bonus
	}
	if fromEarning.IsNegative() {
		fromEarning = decimal.Zero
	}
	rest := bonus.Sub(fromEarning)
	fromMargin := decimal.Zero
	if rest.IsPositive() {
		marginAllowed := adty_setting.SiteBonusMarginFillAllowed(ext.SiteId)
		if len(marginAuthorization) > 0 {
			marginAllowed = marginAuthorization[0]
		}
		if !marginAllowed {
			return common.NewMessage("Site gift reserve is not covered by the earning wallet")
		}
		if pooledSite {
			return common.NewMessage("Site gift reserve is not covered by the earning wallet or authorized margin")
		}
		var margin model.AdtySiteWallet
		if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND kind = ?", ext.SiteId, model.AdtySiteWalletMargin).First(&margin).Error; err != nil {
			return err
		}
		allowed := AdtyMarginNewDraw(AdtyMarginDrawInput{TierUSD: margin.CycleTierUSD, Balance: margin.Balance, Used: margin.CycleUsedUSD, Frozen: margin.FrozenUSD, ExcessFrozen: margin.ExcessFrozenUSD})
		if rest.GreaterThan(allowed) {
			return common.NewMessage("Site gift reserve is not covered by the earning wallet or authorized margin")
		}
		fromMargin = rest
	}
	key := "gift:" + ext.TradeNo
	already, err := adtyWalletIdempotencyExists(tx, key+":earn", key+":margin")
	if err != nil {
		return err
	}
	if fromEarning.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, ext.SiteId, model.AdtySiteWalletEarnings, decimal.Zero, AdtySiteWalletChange{
			Type: "gift_reserve", RefType: "topup", RefID: ext.TradeNo, IdempotencyKey: key + ":earn",
			HoldDelta: fromEarning,
		}); err != nil {
			return err
		}
	}
	if fromMargin.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, ext.SiteId, model.AdtySiteWalletMargin, decimal.Zero, AdtySiteWalletChange{
			Type: "gift_reserve", RefType: "topup", RefID: ext.TradeNo, IdempotencyKey: key + ":margin",
			HoldDelta: fromMargin,
		}); err != nil {
			return err
		}
	}
	ext.GiftReserveEarning = fromEarning
	ext.GiftReserveMargin = fromMargin
	switch {
	case fromEarning.IsPositive() && fromMargin.IsPositive():
		ext.GiftReserveSource = "earning+margin"
	case fromMargin.IsPositive():
		ext.GiftReserveSource = "margin"
	default:
		ext.GiftReserveSource = "earning"
	}
	if !already && (fromEarning.IsPositive() || fromMargin.IsPositive()) {
		if _, err := appendAdtyGiftOfficialAuditTx(tx, ext.UserId, ext.SiteId, ext.TradeNo, "adty.gift.reserve", "",
			map[string]any{"earning": "0", "margin": "0"},
			map[string]any{
				"trade_no": ext.TradeNo, "site_id": ext.SiteId, "bonus_usd": ext.BonusUSD.String(),
				"source":  ext.GiftReserveSource,
				"earning": fromEarning.String(), "margin": fromMargin.String(),
				"method": ext.Method, "bonus_method": ext.BonusMethod,
			}); err != nil {
			return err
		}
	}
	return nil
}

// ReleaseAdtySiteGiftTx returns a reserved site gift to its original earning
// and/or margin source. Cancel, expiry, and replace call it in the same
// transaction as the status change. Replays of the same order are idempotent.
func ReleaseAdtySiteGiftTx(tx *gorm.DB, ext *model.AdtyTopupOrder) error {
	if tx == nil || ext == nil || ext.SiteId <= 0 {
		return nil
	}
	key := "gift:" + ext.TradeNo
	already, err := adtyWalletIdempotencyExists(tx, key+":earn:release", key+":margin:release")
	if err != nil {
		return err
	}
	if ext.GiftReserveMargin.IsPositive() && !ext.GiftReserveEarning.IsPositive() {
		if err := preLockAdtySiteWalletsTx(tx, ext.SiteId, model.AdtySiteWalletEarnings); err != nil {
			return err
		}
	}
	if ext.GiftReserveEarning.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, ext.SiteId, model.AdtySiteWalletEarnings, decimal.Zero, AdtySiteWalletChange{
			Type: "gift_release", RefType: "topup", RefID: ext.TradeNo, IdempotencyKey: key + ":earn:release",
			HoldDelta: ext.GiftReserveEarning.Neg(),
		}); err != nil {
			return err
		}
	}
	if ext.GiftReserveMargin.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, ext.SiteId, model.AdtySiteWalletMargin, decimal.Zero, AdtySiteWalletChange{
			Type: "gift_release", RefType: "topup", RefID: ext.TradeNo, IdempotencyKey: key + ":margin:release",
			HoldDelta: ext.GiftReserveMargin.Neg(),
		}); err != nil {
			return err
		}
	}
	if !already && (ext.GiftReserveEarning.IsPositive() || ext.GiftReserveMargin.IsPositive()) {
		if _, err := appendAdtyGiftOfficialAuditTx(tx, ext.UserId, ext.SiteId, ext.TradeNo, "adty.gift.release", "",
			map[string]any{
				"trade_no": ext.TradeNo, "source": ext.GiftReserveSource,
				"earning": ext.GiftReserveEarning.String(), "margin": ext.GiftReserveMargin.String(),
			},
			map[string]any{"trade_no": ext.TradeNo, "earning": "0", "margin": "0", "source": ext.GiftReserveSource}); err != nil {
			return err
		}
	}
	return nil
}

func cloneMethodBonus(in map[string]adty_setting.MethodBonusRule) map[string]adty_setting.MethodBonusRule {
	if in == nil {
		return nil
	}
	out := make(map[string]adty_setting.MethodBonusRule, len(in))
	for key, rule := range in {
		out[key] = rule
	}
	return out
}

func init() {
	model.AdtyReleaseTopUpReserveTx = ReleaseAdtySiteGiftTx
	initGiftFulfillmentHooks()
}
