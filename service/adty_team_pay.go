package service

import (
	"context"
	"errors"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/shopspring/decimal"
)

// StartAdtyTeamTopupPayment opens an epay or USDT order for a team.
// The payable amount uses the configured top-up price. The order has no gift.
// A positive channelID pins the Epay order to that channel: if it cannot take
// the order, the leader is asked to choose another payment method.
func StartAdtyTeamTopupPayment(leaderID int, teamID int64, amountUSD decimal.Decimal, method string, channelID int, key, clientIP string) (*model.AdtyTopupOrder, error) {
	method, key = strings.TrimSpace(method), strings.TrimSpace(key)
	if leaderID <= 0 || teamID <= 0 || !amountUSD.IsPositive() || !amountUSD.Equal(decimal.NewFromInt(amountUSD.IntPart())) || key == "" {
		return nil, ErrAdtyTeamInvalid
	}
	if len(key) > adtyTeamTopupKeyMaxLen {
		return nil, ErrAdtyTeamIdempotencyKey
	}
	unlock := lockAdtyTeamTopupKey(teamID, key)
	defer unlock()
	if IsAdtyUSDTMethod(method) {
		if channelID > 0 {
			return nil, ErrAdtyTeamInvalid
		}
		return startAdtyTeamBEpusdtTopup(leaderID, teamID, amountUSD, method, key)
	}
	if !IsAdtyEpayMethod(method) {
		return nil, ErrAdtyTeamInvalid
	}
	tradeNo := adtyTeamTopupTradeNo("TTEP", teamID, key)
	existing, conflict, err := adtyClaimTeamTopupReplay(tradeNo, adtyTeamTopupLegacyTradeNo("TTEP", key), teamID, func(o *model.AdtyTopupOrder) bool {
		return o.TeamId == teamID && o.UserId == leaderID && o.FaceUSD.Equal(amountUSD) &&
			o.Provider == model.PaymentProviderAdtyEpay && o.Method == method &&
			(channelID <= 0 || o.ChannelId == channelID)
	})
	if err != nil {
		return nil, err
	}
	if conflict {
		return nil, ErrAdtyTeamInvalid
	}
	if existing != nil {
		return existing, nil
	}
	var team model.AdtyTeam
	if err := model.DB.First(&team, teamID).Error; err != nil {
		return nil, err
	}
	if team.Status != model.AdtyTeamStatusActive || team.LeaderUserId != leaderID {
		return nil, ErrAdtyTeamForbidden
	}
	var leader model.User
	if err := model.DB.First(&leader, leaderID).Error; err != nil {
		return nil, err
	}
	if leader.SiteId != team.SiteId {
		return nil, ErrAdtyTeamForbidden
	}
	quote, err := adtyTeamQuote(leader.Group, amountUSD.IntPart(), method)
	if err != nil {
		return nil, err
	}
	scene, err := AuthorizeAdtyPaymentScene(leaderID, "wallet_topup", model.PaymentProviderAdtyEpay, method, quote)
	if err != nil {
		return nil, err
	}
	allowed, ok := AdtyEpayChannelAllowed(scene, channelID)
	if !ok {
		return nil, ErrAdtyEpayChosenChannelUnavailable
	}
	channel, channelMethod, err := PickAdtyEpayChannel(method, quote.PayAmount, "pc", nil, time.Now().Unix(), allowed)
	if err != nil {
		if channelID > 0 {
			return nil, ErrAdtyEpayChosenChannelUnavailable
		}
		return nil, err
	}
	payMoney, _ := quote.PayAmount.Float64()
	topUp := &model.TopUp{
		UserId: leaderID, Amount: quote.FaceUSD.IntPart(), Money: payMoney, TradeNo: tradeNo,
		PaymentMethod: method, PaymentProvider: model.PaymentProviderAdtyEpay,
		CreateTime: time.Now().Unix(), Status: common.TopUpStatusPending,
	}
	ext := &model.AdtyTopupOrder{
		TradeNo: tradeNo, UserId: leaderID, TeamId: teamID, SiteId: team.SiteId,
		Provider: model.PaymentProviderAdtyEpay, Method: method,
		FaceUSD: quote.FaceUSD, BonusRate: decimal.Zero, BonusUSD: decimal.Zero,
		CreditQuota: quote.CreditQuota, PayAmount: quote.PayAmount,
		PayCurrency: quote.PayCurrency, PriceSnapshot: quote.PriceCNYPerUSD, TopupGroupRatio: quote.TopupGroupRatio,
		ChannelId: channel.Id, MethodLabel: channelMethod.DisplayName,
	}
	if err := CreateAdtyPaymentOrder(topUp, ext, "wallet_topup", channel); err != nil {
		if errors.Is(err, model.ErrAdtyIdempotencyConflict) {
			return nil, ErrAdtyTeamInvalid
		}
		return nil, err
	}
	result, err := CreateAdtyEpayOrder(context.Background(), channel, channelMethod, AdtyEpayCreateRequest{
		TradeNo: tradeNo, Name: "Team top-up " + tradeNo, ClientIP: clientIP, Device: "pc",
		MoneyCNY: quote.PayAmount, NotifyURL: strings.TrimRight(GetCallbackAddress(), "/") + "/api/user/epay/notify/" + strconv.Itoa(channel.Id),
		ReturnURL: AdtyPayResultURL(tradeNo),
	})
	if err != nil {
		_ = model.UpdatePendingTopUpStatus(tradeNo, model.PaymentProviderAdtyEpay, common.TopUpStatusFailed)
		if channelID > 0 {
			return nil, ErrAdtyEpayChosenChannelUnavailable
		}
		return nil, err
	}
	if err := model.UpdateAdtyTopupOrderFields(tradeNo, map[string]any{
		"qr_code": result.QRCode, "pay_url": result.PayURL, "url_scheme": result.URLScheme, "gateway_trade_id": result.GatewayTradeNo,
	}); err != nil {
		return nil, err
	}
	return model.GetAdtyTopupOrder(tradeNo)
}

func startAdtyTeamBEpusdtTopup(leaderID int, teamID int64, amountUSD decimal.Decimal, method, key string) (*model.AdtyTopupOrder, error) {
	tradeNo := adtyTeamTopupTradeNo("TTU", teamID, key)
	existing, conflict, err := adtyClaimTeamTopupReplay(tradeNo, adtyTeamTopupLegacyTradeNo("TTU", key), teamID, func(o *model.AdtyTopupOrder) bool {
		return o.TeamId == teamID && o.UserId == leaderID && o.FaceUSD.Equal(amountUSD) &&
			o.Provider == model.PaymentProviderAdtyBEpusdt && o.Method == method
	})
	if err != nil {
		return nil, err
	}
	if conflict {
		return nil, ErrAdtyTeamInvalid
	}
	if existing != nil {
		return existing, nil
	}
	if !setting.BEpusdtConfigured() || !setting.BEpusdtChainEnabled(method) {
		return nil, common.NewMessage("This payment method is not available")
	}
	var team model.AdtyTeam
	if err := model.DB.First(&team, teamID).Error; err != nil {
		return nil, err
	}
	if team.Status != model.AdtyTeamStatusActive || team.LeaderUserId != leaderID {
		return nil, ErrAdtyTeamForbidden
	}
	var leader model.User
	if err := model.DB.First(&leader, leaderID).Error; err != nil {
		return nil, err
	}
	if leader.SiteId != team.SiteId {
		return nil, ErrAdtyTeamForbidden
	}
	quote, err := adtyTeamQuote(leader.Group, amountUSD.IntPart(), method)
	if err != nil {
		return nil, err
	}
	if _, err := AuthorizeAdtyPaymentScene(leaderID, "wallet_topup", model.PaymentProviderAdtyBEpusdt, method, quote); err != nil {
		return nil, err
	}
	payMoney, _ := quote.PayAmount.Float64()
	topUp := &model.TopUp{
		UserId: leaderID, Amount: quote.FaceUSD.IntPart(), Money: payMoney, TradeNo: tradeNo,
		PaymentMethod: method, PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		CreateTime: time.Now().Unix(), Status: common.TopUpStatusPending,
	}
	ext := &model.AdtyTopupOrder{
		TradeNo: tradeNo, UserId: leaderID, TeamId: teamID, SiteId: team.SiteId,
		Provider: model.PaymentProviderAdtyBEpusdt, Method: method,
		FaceUSD: quote.FaceUSD, BonusRate: decimal.Zero, BonusUSD: decimal.Zero,
		CreditQuota: quote.CreditQuota, PayAmount: quote.PayAmount,
		PayCurrency: quote.PayCurrency, PriceSnapshot: quote.PriceCNYPerUSD, TopupGroupRatio: quote.TopupGroupRatio,
	}
	if err := CreateAdtyPaymentOrder(topUp, ext, "wallet_topup"); err != nil {
		if errors.Is(err, model.ErrAdtyIdempotencyConflict) {
			return nil, ErrAdtyTeamInvalid
		}
		return nil, err
	}
	minutes := 15
	order, err := CreateBEpusdtOrder(BEpusdtCreateRequest{
		OrderID: tradeNo, AmountUSD: quote.PayAmount, TradeType: method,
		NotifyURL:   strings.TrimRight(GetCallbackAddress(), "/") + "/api/user/bepusdt/notify",
		RedirectURL: AdtyPayResultURL(tradeNo), Name: "Team top-up " + tradeNo, Timeout: minutes * 60,
	})
	if err != nil {
		_ = model.UpdatePendingTopUpStatus(tradeNo, model.PaymentProviderAdtyBEpusdt, common.TopUpStatusFailed)
		return nil, err
	}
	if err := model.UpdateAdtyTopupOrderFields(tradeNo, map[string]any{
		"pay_address": order.Address, "pay_amount": order.ActualAmount, "pay_url": order.PaymentURL,
		"expire_at": order.ExpireAt, "gateway_trade_id": order.TradeId,
	}); err != nil {
		return nil, err
	}
	return model.GetAdtyTopupOrder(tradeNo)
}

func adtyTeamQuote(userGroup string, face int64, method string) (*AdtyQuote, error) {
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
	faceUSD := decimal.NewFromInt(face)
	creditQuota := faceUSD.Mul(common.QuotaPerUnitDecimal()).Floor()
	if _, err := common.WalletQuotaFromDecimalStrict(creditQuota); err != nil || !creditQuota.IsPositive() {
		return nil, common.NewMessage("Top-up quota exceeds the range the system can represent")
	}
	groupRatio := decimal.NewFromFloat(common.GetTopupGroupRatio(userGroup))
	if !groupRatio.IsPositive() {
		groupRatio = decimal.NewFromInt(1)
	}
	quote := &AdtyQuote{
		FaceUSD:         faceUSD,
		BonusRate:       decimal.Zero,
		BonusUSD:        decimal.Zero,
		CreditQuota:     creditQuota.IntPart(),
		TopupGroupRatio: groupRatio,
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
