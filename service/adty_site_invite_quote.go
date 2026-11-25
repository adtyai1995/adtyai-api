package service

import (
	"crypto/sha256"
	"fmt"
	"math"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
)

type SiteInviteWithdrawalQuote struct {
	QuoteID        string          `json:"quote_id"`
	FeeUSD         decimal.Decimal `json:"fee_usd"`
	Rate           decimal.Decimal `json:"rate"`
	PayoutAmount   string          `json:"payout_amount"`
	PayoutCurrency string          `json:"payout_currency"`
}
type SiteInviteQuoteChanged struct{ Quote SiteInviteWithdrawalQuote }

func (*SiteInviteQuoteChanged) Error() string {
	return "Withdrawal quote changed. Confirm the updated amount."
}

func siteInvitePayable(gross, fee, rate decimal.Decimal, method string) string {
	net := decimal.Max(gross.Sub(fee), decimal.Zero)
	payout := siteWithdrawalPayout(net, rate, method)
	if method == "usdt" {
		return payout.StringFixed(6)
	}
	return payout.StringFixed(2)
}

func payableSiteInviteWithdrawal(row *model.AdtySiteInviteWithdrawal) bool {
	if !row.NetUSD.IsPositive() {
		return false
	}
	if row.Method != "alipay" && row.Method != "usdt" {
		return true
	}
	amount, err := decimal.NewFromString(row.PayoutAmount)
	return err == nil && amount.IsPositive()
}

func siteInviteQuote(site *model.AdtySite, amount decimal.Decimal, method string) (q SiteInviteWithdrawalQuote, err error) {
	p := site.MarketingPolicy
	scale := int32(2)
	if method == "usdt" {
		scale = 6
	}
	if !amount.IsPositive() || !amount.Equal(amount.Truncate(scale)) {
		return q, common.NewMessage("Invalid parameters")
	}
	methods, _ := usableSiteInviteWithdrawConfig(p)
	switch method {
	case "usdt":
		if !slices.Contains(methods, adty_setting.ReferralWithdrawMethodUSDT) || p.MinUSD == nil || p.FeeUSD == nil {
			return q, common.NewMessage("This withdrawal method is not available")
		}
		if amount.LessThan(*p.MinUSD) {
			return q, common.NewMessage("The minimum withdrawal is ${{amount}}", map[string]any{"amount": p.MinUSD.String()})
		}
		q.Rate, q.FeeUSD, q.PayoutCurrency = decimal.NewFromInt(1), *p.FeeUSD, "USDT"
		if !amount.GreaterThan(q.FeeUSD) {
			return q, common.NewMessage("The amount must be larger than the network fee")
		}
	case "alipay":
		if !slices.Contains(methods, adty_setting.ReferralWithdrawMethodAlipay) || p.MinUSD == nil || p.CNYRate == nil {
			return q, common.NewMessage("This withdrawal method is not available")
		}
		if amount.LessThan(*p.MinUSD) {
			return q, common.NewMessage("The minimum withdrawal is ${{amount}}", map[string]any{"amount": p.MinUSD.String()})
		}
		q.Rate, q.PayoutCurrency = *p.CNYRate, "CNY"
	default:
		return q, common.NewMessage("Unsupported withdrawal method")
	}
	q.PayoutAmount = siteInvitePayable(amount, q.FeeUSD, q.Rate, method)
	if decimal.RequireFromString(q.PayoutAmount).IsZero() {
		return q, common.NewMessage("The payout amount is too small")
	}
	facts := fmt.Sprintf("%d|%s|%s|%s|%s|%s", site.Id, method, amount, q.FeeUSD, q.Rate, q.PayoutAmount)
	q.QuoteID = fmt.Sprintf("%x", sha256.Sum256([]byte(facts)))
	return q, nil
}

func QuoteAdtySiteInviteWithdrawal(userID, siteID int, amount decimal.Decimal, method string) (q SiteInviteWithdrawalQuote, err error) {
	var user model.User
	if err = model.DB.Select("site_id").First(&user, userID).Error; err != nil {
		return
	}
	if siteID == 0 {
		siteID = user.SiteId
	}
	if err = authorizeCommissionSource(model.DB, userID, user.SiteId, siteID); err != nil {
		return
	}
	var site model.AdtySite
	if err = model.DB.First(&site, siteID).Error; err != nil {
		return
	}
	return siteInviteQuote(&site, amount, method)
}

func siteInviteCancelDeadline(created int64, override *int64) int64 {
	minutes := int64(adty_setting.GetReferralSetting().CancelWindowMinutes)
	if override != nil {
		minutes = *override
	}
	if minutes > (math.MaxInt64-created)/60 {
		return math.MaxInt64
	}
	return created + max(minutes, 0)*60
}

type SiteInviteCancelState struct {
	CanCancel      bool   `json:"can_cancel"`
	CancelReason   string `json:"cancel_reason"`
	CancelDeadline int64  `json:"cancel_deadline"`
}

func SiteInviteCancellation(row *model.AdtySiteInviteWithdrawal, now int64) SiteInviteCancelState {
	state := SiteInviteCancelState{CancelDeadline: row.CancelDeadline}
	switch {
	case row.Status != model.AdtySiteInviteWithdrawPending || row.Method == "balance":
		state.CancelReason = "Only pending withdrawals can be canceled"
	case now >= row.CancelDeadline:
		state.CancelReason = "The cancel window has passed"
	default:
		state.CanCancel = true
	}
	return state
}
