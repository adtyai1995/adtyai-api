package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func AdtyEpayExpiry(created int64) int64 {
	minutes := adty_setting.GetTopupSetting().EpayOrderMinutes
	if minutes <= 0 {
		minutes = 10
	}
	return created + int64(minutes)*60
}

func adtyUSDTOrderTimeoutSeconds() int64 {
	minutes := adty_setting.GetTopupSetting().USDTOrderMinutes
	if minutes <= 0 {
		return bepusdtTimeoutSeconds
	}
	return int64(minutes) * 60
}

func adtyTeamQuote(userGroup string, face int64, method string) (*AdtyQuote, error) {
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		return nil, common.NewMessage("Top-up is unavailable while quota is displayed as tokens")
	}
	method = strings.TrimSpace(method)
	if !IsAdtyUSDTMethod(method) && !IsAdtyEpayMethod(method) {
		return nil, common.NewMessage("Unsupported payment method")
	}
	minTopUp := operation_setting.GetMinTopUp()
	if face < int64(minTopUp) || face <= 0 {
		return nil, common.NewMessage("Top-up amount cannot be less than {{amount}}", map[string]any{"amount": minTopUp})
	}
	faceUSD := decimal.NewFromInt(face)
	creditQuota := faceUSD.Mul(common.QuotaPerUnitDecimal()).Floor()
	if _, err := common.WalletQuotaFromDecimalStrict(creditQuota); err != nil || !creditQuota.IsPositive() {
		return nil, common.NewMessage("Top-up quota exceeds the range the system can represent")
	}
	quote := &AdtyQuote{
		FaceUSD:         faceUSD,
		BonusRate:       decimal.Zero,
		BonusUSD:        decimal.Zero,
		CreditQuota:     creditQuota.IntPart(),
		TopupGroupRatio: decimal.NewFromInt(1),
	}
	if IsAdtyUSDTMethod(method) {
		quote.PayCurrency = AdtyCurrencyUSDT
		quote.PayAmount = quote.FaceUSD.Mul(quote.TopupGroupRatio).Round(8)
		return quote, nil
	}
	price := decimal.NewFromFloat(operation_setting.GetPrice())
	if !price.IsPositive() {
		return nil, common.NewMessage("The top-up price is not configured")
	}
	quote.PayCurrency = AdtyCurrencyCNY
	quote.PriceCNYPerUSD = price
	quote.PayAmount = quote.FaceUSD.Mul(price).Mul(quote.TopupGroupRatio).Round(2)
	return quote, nil
}

// AdtyQuoteForTeamPayment quotes a team top-up for an active team leader.
// Team orders keep Business=team_topup and receive zero recharge bonus;
// scene authorization routes subsite teams (team.SiteId > 0) through wallet_topup
// and main-site teams (team.SiteId == 0) through team_topup.
func AdtyQuoteForTeamPayment(leaderID, requestSiteID int, teamID int64, userGroup string, face int64, method string) (*AdtyQuote, error) {
	if leaderID <= 0 || teamID <= 0 {
		return nil, ErrAdtyTeamForbidden
	}
	var team model.AdtyTeam
	if err := model.DB.First(&team, teamID).Error; err != nil {
		return nil, ErrAdtyTeamForbidden
	}
	if team.Status != model.AdtyTeamStatusActive || team.LeaderUserId != leaderID {
		return nil, ErrAdtyTeamForbidden
	}
	var leader model.User
	if err := model.DB.First(&leader, leaderID).Error; err != nil {
		return nil, ErrAdtyTeamForbidden
	}
	if leader.Status != common.UserStatusEnabled || leader.StaffSiteId != 0 || leader.SiteId != team.SiteId {
		return nil, ErrAdtyTeamForbidden
	}
	if requestSiteID > 0 && requestSiteID != team.SiteId {
		return nil, ErrAdtyTeamForbidden
	}
	group := userGroup
	if strings.TrimSpace(group) == "" {
		group = leader.Group
	}
	quote, err := adtyTeamQuote(group, face, method)
	if err != nil {
		return nil, err
	}
	quote.TeamID = teamID
	quote.Business = model.AdtyBusinessTeamTopup
	quote.SiteID = team.SiteId
	return quote, nil
}

// StartAdtyTeamTopupPayment opens an epay or USDT order for a team.
// The payable amount uses the configured top-up price. The order has no gift.
// A positive channelID pins the Epay order to that channel: if it cannot take
// the order, the leader is asked to choose another payment method.
func StartAdtyTeamTopupPayment(leaderID int, teamID int64, amountUSD decimal.Decimal, method string, channelID int, key, clientIP string, paymentScene ...string) (*model.AdtyTopupOrder, error) {
	method, key = strings.TrimSpace(method), strings.TrimSpace(key)
	if leaderID <= 0 || teamID <= 0 || channelID < 0 || key == "" || !amountUSD.IsPositive() {
		return nil, ErrAdtyTeamInvalid
	}
	// R5-05: Reject extreme exponents before IntPart()/rescale allocates giant big.Int.
	if exp := amountUSD.Exponent(); exp < -4 || exp > 9 {
		return nil, ErrAdtyTeamInvalid
	}
	if !amountUSD.Equal(decimal.NewFromInt(amountUSD.IntPart())) {
		return nil, ErrAdtyTeamInvalid
	}
	if len(key) > adtyTeamTopupKeyMaxLen {
		return nil, ErrAdtyTeamIdempotencyKey
	}
	sceneKey := ""
	if len(paymentScene) > 0 {
		sceneKey = strings.TrimSpace(paymentScene[0])
	}
	unlock := lockAdtyTeamTopupKey(teamID, key)
	defer unlock()
	if IsAdtyUSDTMethod(method) {
		if channelID > 0 {
			return nil, ErrAdtyTeamInvalid
		}
		return startAdtyTeamBEpusdtTopup(leaderID, teamID, amountUSD, method, key, sceneKey)
	}
	if !IsAdtyEpayMethod(method) {
		return nil, ErrAdtyTeamInvalid
	}
	tradeNo := adtyTeamTopupTradeNo("TTEP", teamID, key)
	existing, conflict, err := adtyClaimTeamTopupReplay(tradeNo, adtyTeamTopupLegacyTradeNo("TTEP", key), teamID, func(o *model.AdtyTopupOrder) bool {
		return o.TeamId == teamID && o.UserId == leaderID && o.FaceUSD.Equal(amountUSD) && (sceneKey == "" || o.PaymentScene == sceneKey || (o.PaymentScene == "" && sceneKey == model.AdtyBusinessWalletTopup)) &&
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
		return ResumeAdtyTeamTopupPayment(leaderID, teamID, existing.TradeNo, clientIP)
	}
	var team model.AdtyTeam
	if err := model.DB.First(&team, teamID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAdtyTeamForbidden
		}
		return nil, err
	}
	if team.Status != model.AdtyTeamStatusActive || team.LeaderUserId != leaderID {
		return nil, ErrAdtyTeamForbidden
	}
	var leader model.User
	if err := model.DB.First(&leader, leaderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAdtyTeamForbidden
		}
		return nil, err
	}
	if leader.SiteId != team.SiteId {
		return nil, ErrAdtyTeamForbidden
	}
	quote, err := adtyTeamQuote(leader.Group, amountUSD.IntPart(), method)
	if err != nil {
		return nil, err
	}
	quote.TeamID, quote.Business, quote.SiteID = teamID, model.AdtyBusinessTeamTopup, team.SiteId
	scene, err := AuthorizeAdtyPaymentScene(leaderID, sceneKey, model.PaymentProviderAdtyEpay, method, quote)
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
		if errors.Is(err, ErrAdtyEpayNoChannel) {
			return nil, common.NewMessage("This payment method is not available")
		}
		if errors.Is(err, model.ErrAdtyEpayDailyLimit) {
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
		Business: model.AdtyBusinessTeamTopup, TradeNo: tradeNo, UserId: leaderID, TeamId: teamID, SiteId: team.SiteId,
		ExpireAt: AdtyEpayExpiry(topUp.CreateTime), CheckoutStage: "local", Provider: model.PaymentProviderAdtyEpay, Method: method,
		FaceUSD: quote.FaceUSD, BonusRate: decimal.Zero, BonusUSD: decimal.Zero,
		CreditQuota: quote.CreditQuota, PayAmount: quote.PayAmount,
		PayCurrency: quote.PayCurrency, PriceSnapshot: quote.PriceCNYPerUSD, TopupGroupRatio: quote.TopupGroupRatio,
		ChannelId: channel.Id, MethodLabel: channelMethod.DisplayName,
	}
	if err := CreateAdtyPaymentOrder(topUp, ext, sceneKey, channel); err != nil {
		if errors.Is(err, model.ErrAdtyIdempotencyConflict) {
			return nil, ErrAdtyTeamInvalid
		}
		if errors.Is(err, model.ErrAdtyEpayDailyLimit) {
			return nil, ErrAdtyEpayChosenChannelUnavailable
		}
		return nil, err
	}
	return ResumeAdtyTeamTopupPayment(leaderID, teamID, tradeNo, clientIP)
}

func startAdtyTeamBEpusdtTopup(leaderID int, teamID int64, amountUSD decimal.Decimal, method, key, sceneKey string) (*model.AdtyTopupOrder, error) {
	tradeNo := adtyTeamTopupTradeNo("TTU", teamID, key)
	existing, conflict, err := adtyClaimTeamTopupReplay(tradeNo, adtyTeamTopupLegacyTradeNo("TTU", key), teamID, func(o *model.AdtyTopupOrder) bool {
		return o.TeamId == teamID && o.UserId == leaderID && o.FaceUSD.Equal(amountUSD) && (sceneKey == "" || o.PaymentScene == sceneKey || (o.PaymentScene == "" && sceneKey == model.AdtyBusinessWalletTopup)) &&
			o.Provider == model.PaymentProviderAdtyBEpusdt && o.Method == method
	})
	if err != nil {
		return nil, err
	}
	if conflict {
		return nil, ErrAdtyTeamInvalid
	}
	if existing != nil {
		return ResumeAdtyTeamTopupPayment(leaderID, teamID, existing.TradeNo, "")
	}
	if !setting.BEpusdtConfigured() || !setting.BEpusdtChainEnabled(method) {
		return nil, common.NewMessage("This payment method is not available")
	}
	var team model.AdtyTeam
	if err := model.DB.First(&team, teamID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAdtyTeamForbidden
		}
		return nil, err
	}
	if team.Status != model.AdtyTeamStatusActive || team.LeaderUserId != leaderID {
		return nil, ErrAdtyTeamForbidden
	}
	var leader model.User
	if err := model.DB.First(&leader, leaderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAdtyTeamForbidden
		}
		return nil, err
	}
	if leader.SiteId != team.SiteId {
		return nil, ErrAdtyTeamForbidden
	}
	quote, err := adtyTeamQuote(leader.Group, amountUSD.IntPart(), method)
	if err != nil {
		return nil, err
	}
	quote.TeamID, quote.Business, quote.SiteID = teamID, model.AdtyBusinessTeamTopup, team.SiteId
	if _, err := AuthorizeAdtyPaymentScene(leaderID, sceneKey, model.PaymentProviderAdtyBEpusdt, method, quote); err != nil {
		return nil, err
	}
	payMoney, _ := quote.PayAmount.Float64()
	topUp := &model.TopUp{
		UserId: leaderID, Amount: quote.FaceUSD.IntPart(), Money: payMoney, TradeNo: tradeNo,
		PaymentMethod: method, PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		CreateTime: time.Now().Unix(), Status: common.TopUpStatusPending,
	}
	ext := &model.AdtyTopupOrder{
		Business: model.AdtyBusinessTeamTopup, TradeNo: tradeNo, UserId: leaderID, TeamId: teamID, SiteId: team.SiteId,
		ExpireAt: time.Now().Unix() + adtyUSDTOrderTimeoutSeconds(), CheckoutStage: "local", CheckoutGateway: strings.TrimRight(setting.GetBEpusdtSetting().BaseURL, "/"), Provider: model.PaymentProviderAdtyBEpusdt, Method: method,
		FaceUSD: quote.FaceUSD, BonusRate: decimal.Zero, BonusUSD: decimal.Zero,
		CreditQuota: quote.CreditQuota, PayAmount: quote.PayAmount,
		PayCurrency: quote.PayCurrency, PriceSnapshot: quote.PriceCNYPerUSD, TopupGroupRatio: quote.TopupGroupRatio,
	}
	if err := CreateAdtyPaymentOrder(topUp, ext, sceneKey); err != nil {
		if errors.Is(err, model.ErrAdtyIdempotencyConflict) {
			return nil, ErrAdtyTeamInvalid
		}
		return nil, err
	}
	return ResumeAdtyTeamTopupPayment(leaderID, teamID, tradeNo, "")
}

// Resume the original merchant order. The committed submitting marker survives
// loss of the gateway response. The order lock serializes gateway calls with
// other workers, cancellation, and final credit.
func ResumeAdtyTeamTopupPayment(leaderID int, teamID int64, tradeNo, clientIP string) (*model.AdtyTopupOrder, error) {
	view, err := model.GetUserAdtyTopUp(tradeNo, leaderID)
	if err != nil {
		return nil, err
	}
	ext := view.Ext
	if teamID <= 0 || ext.TeamId != teamID || ext.UserId != leaderID {
		return nil, ErrAdtyTeamForbidden
	}
	if view.Status == common.TopUpStatusPending && ext.Provider == model.PaymentProviderAdtyEpay {
		deadline := ext.ExpireAt
		if deadline == 0 {
			deadline = AdtyEpayExpiry(ext.CreatedAt)
		}
		if deadline <= time.Now().Unix() {
			return nil, common.NewMessage("Order expired")
		}
	}
	if view.Status != common.TopUpStatusPending || teamCheckoutReady(ext) {
		return ext, nil
	}
	if !IsAdtyUSDTMethod(ext.Method) && (!IsAdtyEpayMethod(ext.Method) || ext.ChannelId <= 0) {
		return nil, ErrAdtyTeamInvalid
	}
	unlockResume := lockAdtyTeamTopupKey(teamID, "resume:"+tradeNo)
	defer unlockResume()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var (
		alreadyDone bool
		isReplay    bool
		extSnapshot model.AdtyTopupOrder
		epayChannel *model.AdtyEpayChannel
	)
	// Tx 1: Validate state, atomically claim checkout_stage=submitting, and
	// capture immutable checkout inputs before calling external gateways
	// (never hold FOR UPDATE row locks across HTTP I/O).
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var top model.TopUp
		if err := model.AdtyLockForUpdate(tx).Where("trade_no = ? AND user_id = ?", tradeNo, leaderID).First(&top).Error; err != nil {
			return err
		}
		if err := model.AdtyLockForUpdate(tx).First(ext, ext.Id).Error; err != nil {
			return err
		}
		if ext.Provider == model.PaymentProviderAdtyEpay && ext.ExpireAt == 0 && top.Status == common.TopUpStatusPending {
			ext.ExpireAt = AdtyEpayExpiry(ext.CreatedAt)
			if err := tx.Model(ext).UpdateColumn("expire_at", ext.ExpireAt).Error; err != nil {
				return err
			}
		}
		if top.Status != common.TopUpStatusPending || teamCheckoutReady(ext) {
			alreadyDone = true
			return nil
		}
		var team model.AdtyTeam
		if err := tx.First(&team, teamID).Error; err != nil {
			return err
		}
		if team.LeaderUserId != leaderID || team.Status != model.AdtyTeamStatusActive {
			return ErrAdtyTeamForbidden
		}
		if ext.Provider == model.PaymentProviderAdtyEpay && IsAdtyEpayMethod(ext.Method) {
			ch := &model.AdtyEpayChannel{}
			if err := tx.Preload("Methods").First(ch, ext.ChannelId).Error; err != nil {
				return err
			}
			if ch.MethodFor(ext.Method) == nil || ch.Status != model.AdtyEpayStatusEnabled || !ch.HasCredentials() {
				return ErrAdtyEpayChosenChannelUnavailable
			}
			epayChannel = ch
		}
		if ext.CheckoutStage == "local" {
			if err := tx.Model(ext).UpdateColumn("checkout_stage", "submitting").Error; err != nil {
				return err
			}
			ext.CheckoutStage = "submitting"
			isReplay = false
		} else {
			isReplay = true
		}
		extSnapshot = *ext
		return nil
	})
	if err != nil {
		return nil, err
	}
	if alreadyDone {
		return model.GetAdtyTopupOrder(tradeNo)
	}
	fields, recoverErr := recoverTeamCheckout(ctx, &extSnapshot, epayChannel, clientIP, isReplay)
	if errors.Is(recoverErr, ErrAdtyPaymentDetected) {
		if err := RecoverAdtyHostedPayment(ctx, &extSnapshot); err != nil {
			return nil, err
		}
		return model.GetAdtyTopupOrder(tradeNo)
	}
	if recoverErr != nil {
		return nil, recoverErr
	}
	// Tx 2: Re-lock and persist checkout fields only if still pending, unready,
	// and the team remains active under the same leader.
	var (
		discarded         bool
		teamForbidden     bool
		curGatewayTradeID string
	)
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var top model.TopUp
		if err := model.AdtyLockForUpdate(tx).Where("trade_no = ? AND user_id = ?", tradeNo, leaderID).First(&top).Error; err != nil {
			return err
		}
		var cur model.AdtyTopupOrder
		if err := model.AdtyLockForUpdate(tx).First(&cur, extSnapshot.Id).Error; err != nil {
			return err
		}
		curGatewayTradeID = cur.GatewayTradeId
		if top.Status != common.TopUpStatusPending || teamCheckoutReady(&cur) {
			discarded = true
			return nil
		}
		var team model.AdtyTeam
		if err := tx.First(&team, teamID).Error; err != nil {
			discarded = true
			teamForbidden = true
			return nil
		}
		if team.LeaderUserId != leaderID || team.Status != model.AdtyTeamStatusActive {
			discarded = true
			teamForbidden = true
			return nil
		}
		fields["checkout_stage"] = "ready"
		return tx.Model(&cur).Updates(fields).Error
	})
	if err != nil || discarded {
		if extSnapshot.Provider == model.PaymentProviderAdtyBEpusdt {
			if gwID, _ := fields["gateway_trade_id"].(string); gwID != "" && gwID != curGatewayTradeID {
				if cancelErr := CancelBEpusdtOrder(gwID); cancelErr != nil {
					common.SysError(common.LogText("failed to cancel discarded team BEpusdt order %s: %s", gwID, cancelErr.Error()))
				}
			}
		}
		if err != nil {
			return nil, err
		}
		if teamForbidden {
			return nil, ErrAdtyTeamForbidden
		}
	}
	return model.GetAdtyTopupOrder(tradeNo)
}

func teamCheckoutReady(ext *model.AdtyTopupOrder) bool {
	if ext.Provider == model.PaymentProviderAdtyBEpusdt {
		return ext.PayAddress != "" && ext.GatewayTradeId != ""
	}
	return ext.PayURL != "" || ext.QRCode != "" || ext.URLScheme != ""
}

func recoverTeamCheckout(ctx context.Context, ext *model.AdtyTopupOrder, channel *model.AdtyEpayChannel, clientIP string, replay bool) (map[string]any, error) {
	trade := ext.TradeNo
	callback := strings.TrimRight(GetCallbackAddress(), "/") + "/api/user/"
	if ext.Provider == model.PaymentProviderAdtyBEpusdt && IsAdtyUSDTMethod(ext.Method) {
		if ext.CheckoutGateway != "" && strings.TrimRight(setting.GetBEpusdtSetting().BaseURL, "/") != ext.CheckoutGateway {
			return nil, model.ErrPaymentMethodMismatch
		}
		deadline := ext.ExpireAt
		if deadline == 0 {
			deadline = ext.CreatedAt + adtyUSDTOrderTimeoutSeconds()
		}
		remaining := deadline - time.Now().Unix()
		if remaining < 120 {
			return nil, common.NewMessage("Order expired")
		}
		order, err := CreateBEpusdtOrder(BEpusdtCreateRequest{
			OrderID: trade, AmountUSD: ext.PayAmount, TradeType: ext.Method, VerifyOrder: true,
			NotifyURL: callback + "bepusdt/notify", RedirectURL: AdtyPayResultURL(trade), Name: "Team top-up " + trade, Timeout: int(remaining),
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"pay_address": order.Address, "pay_amount": order.ActualAmount,
			"pay_url": order.PaymentURL, "expire_at": min(deadline, order.ExpireAt), "gateway_trade_id": order.TradeId}, nil
	}
	if ext.Provider != model.PaymentProviderAdtyEpay || !IsAdtyEpayMethod(ext.Method) {
		return nil, ErrAdtyTeamInvalid
	}
	if channel == nil {
		return nil, ErrAdtyEpayChosenChannelUnavailable
	}
	method := channel.MethodFor(ext.Method)
	if method == nil || channel.Status != model.AdtyEpayStatusEnabled || !channel.HasCredentials() {
		return nil, ErrAdtyEpayChosenChannelUnavailable
	}
	var gatewayTradeNo string
	if replay && channel.OrderMode != model.AdtyEpayOrderModeSubmit {
		if !strings.HasPrefix(strings.ToLower(channel.GatewayURL), "https://") {
			return nil, ErrAdtyEpayChosenChannelUnavailable
		}
		result, err := QueryAdtyEpayOrder(ctx, channel, trade)
		if err != nil && !errors.Is(err, errEpayOrderAbsent) {
			return nil, ErrAdtyEpayChosenChannelUnavailable
		}
		if err == nil {
			expectedType := method.TypeCode
			if expectedType == "" {
				expectedType = method.Method
			}
			if result.OutTradeNo != trade || result.MerchantId != channel.MerchantId || result.TypeCode != expectedType || !result.MoneyCNY.Equal(ext.PayAmount) {
				return nil, model.ErrPaymentMethodMismatch
			}
			if result.Paid {
				return nil, ErrAdtyPaymentDetected
			}
			// A pending mapi order may reject a duplicate create. Epay's signed
			// checkout uses the original merchant, method, amount and order ID.
			channel.OrderMode = model.AdtyEpayOrderModeSubmit
			gatewayTradeNo = result.GatewayTradeNo
		}
	}
	result, err := CreateAdtyEpayOrder(ctx, channel, method, AdtyEpayCreateRequest{
		TradeNo: trade, Name: "Team top-up " + trade, ClientIP: clientIP, Device: "pc", MoneyCNY: ext.PayAmount,
		NotifyURL: callback + "epay/notify/" + strconv.Itoa(channel.Id), ReturnURL: AdtyPayResultURL(trade),
	})
	if err != nil {
		return nil, ErrAdtyEpayChosenChannelUnavailable
	}
	if result.GatewayTradeNo == "" {
		result.GatewayTradeNo = gatewayTradeNo
	}
	return map[string]any{"qr_code": result.QRCode, "pay_url": result.PayURL, "url_scheme": result.URLScheme, "gateway_trade_id": result.GatewayTradeNo}, nil
}
