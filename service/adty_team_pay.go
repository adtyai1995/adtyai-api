package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	maxActivePendingAdtyTeamTopUps  = model.MaxActivePendingAdtyTopUps
	reusableTeamPendingMinRemaining = int64(60)
)

// ErrAdtyPaymentGatewayUnavailable wraps upstream payment gateway failures so
// controllers can log the raw cause and return a sanitized user-facing error.
var ErrAdtyPaymentGatewayUnavailable = errors.New("payment gateway unavailable")

var (
	adtyTeamCheckoutLocks     sync.Map
	adtyTeamCheckoutLockGuard sync.Mutex
)

func lockAdtyTeamCheckout(teamID int64) func() {
	adtyTeamCheckoutLockGuard.Lock()
	var entry *adtyTeamTopupRefLock
	if v, ok := adtyTeamCheckoutLocks.Load(teamID); ok {
		entry = v.(*adtyTeamTopupRefLock)
	} else {
		entry = &adtyTeamTopupRefLock{}
		adtyTeamCheckoutLocks.Store(teamID, entry)
	}
	entry.refs++
	adtyTeamCheckoutLockGuard.Unlock()
	entry.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			entry.mu.Unlock()
			adtyTeamCheckoutLockGuard.Lock()
			entry.refs--
			if entry.refs == 0 {
				adtyTeamCheckoutLocks.Delete(teamID)
			}
			adtyTeamCheckoutLockGuard.Unlock()
		})
	}
}

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
	seconds := int64(minutes) * 60
	if seconds < 180 {
		seconds = 180
	}
	if seconds > 3600 {
		seconds = 3600
	}
	return seconds
}

func normalizeAdtyTeamDevice(raw string) string {
	if strings.TrimSpace(raw) == "mobile" {
		return "mobile"
	}
	return "pc"
}

func adtyTeamQuote(team *model.AdtyTeam, userGroup string, face int64, method string) (*AdtyQuote, error) {
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
	if team != nil {
		shared := max(team.SharedQuota, int64(0))
		if creditQuota.IntPart() > int64(common.MaxWalletQuota)-shared {
			return nil, model.ErrTopUpQuotaLimitExceeded
		}
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
	quote, err := adtyTeamQuote(&team, group, face, method)
	if err != nil {
		return nil, err
	}
	quote.TeamID = teamID
	quote.Business = model.AdtyBusinessTeamTopup
	quote.SiteID = team.SiteId
	return quote, nil
}

// AdtyTeamTopupStartOptions carries all parameters for starting a team checkout.
type AdtyTeamTopupStartOptions struct {
	LeaderID      int
	TeamID        int64
	RequestSiteID int
	AmountUSD     decimal.Decimal
	Method        string
	ChannelID     int
	Key           string
	ClientIP      string
	PaymentScene  string
	Device        string
}

// StartAdtyTeamTopupPayment opens an epay or USDT order for a team.
// The payable amount uses the configured top-up price. The order has no gift.
// A positive channelID pins the Epay order to that channel: if it cannot take
// the order, the leader is asked to choose another payment method.
func StartAdtyTeamTopupPayment(leaderID int, teamID int64, amountUSD decimal.Decimal, method string, channelID int, key, clientIP string, paymentScene ...string) (*model.AdtyTopupOrder, error) {
	sceneKey := ""
	if len(paymentScene) > 0 {
		sceneKey = strings.TrimSpace(paymentScene[0])
	}
	return StartAdtyTeamTopupPaymentWithOptions(AdtyTeamTopupStartOptions{
		LeaderID:     leaderID,
		TeamID:       teamID,
		AmountUSD:    amountUSD,
		Method:       method,
		ChannelID:    channelID,
		Key:          key,
		ClientIP:     clientIP,
		PaymentScene: sceneKey,
		Device:       "pc",
	})
}

// StartAdtyTeamTopupPaymentWithOptions opens an Epay or BEpusdt checkout for a team,
// verifying request site scope, enforcing the per-team active pending order cap,
// reusing matching ready pending orders, and forwarding device preference.
func StartAdtyTeamTopupPaymentWithOptions(opts AdtyTeamTopupStartOptions) (*model.AdtyTopupOrder, error) {
	leaderID := opts.LeaderID
	teamID := opts.TeamID
	amountUSD := opts.AmountUSD
	method := strings.TrimSpace(opts.Method)
	channelID := opts.ChannelID
	key := strings.TrimSpace(opts.Key)
	clientIP := opts.ClientIP
	sceneKey := strings.TrimSpace(opts.PaymentScene)
	device := normalizeAdtyTeamDevice(opts.Device)

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
	unlock := lockAdtyTeamTopupKey(teamID, key)
	defer unlock()
	if IsAdtyUSDTMethod(method) {
		if channelID > 0 {
			return nil, ErrAdtyTeamInvalid
		}
		return startAdtyTeamBEpusdtTopup(leaderID, teamID, opts.RequestSiteID, amountUSD, method, key, sceneKey)
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
		if opts.RequestSiteID > 0 && opts.RequestSiteID != existing.SiteId {
			return nil, ErrAdtyTeamForbidden
		}
		return ResumeAdtyTeamTopupPayment(leaderID, teamID, existing.TradeNo, clientIP, device)
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
	if opts.RequestSiteID > 0 && opts.RequestSiteID != team.SiteId {
		return nil, ErrAdtyTeamForbidden
	}
	var leader model.User
	if err := model.DB.First(&leader, leaderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAdtyTeamForbidden
		}
		return nil, err
	}
	if leader.Status != common.UserStatusEnabled || leader.StaffSiteId != 0 || leader.SiteId != team.SiteId {
		return nil, ErrAdtyTeamForbidden
	}
	quote, err := adtyTeamQuote(&team, leader.Group, amountUSD.IntPart(), method)
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

	unlockCheckout := lockAdtyTeamCheckout(teamID)
	defer unlockCheckout()

	now := time.Now().Unix()
	pendingList, err := model.ListTeamActivePendingAdtyTopUps(teamID, now)
	if err != nil {
		return nil, err
	}
	if reusable := findReusableTeamPendingOrder(pendingList, leaderID, teamID, model.PaymentProviderAdtyEpay, method, scene.Key, quote, channelID, allowed, now); reusable != nil {
		unlockCheckout()
		return ResumeAdtyTeamTopupPayment(leaderID, teamID, reusable.TradeNo, clientIP, device)
	}
	if len(pendingList) >= maxActivePendingAdtyTeamTopUps {
		return nil, model.ErrAdtyTooManyPendingOrders
	}

	maxAttempts := 1
	if channelID == 0 {
		maxAttempts = 5
	}
	exclude := map[int]bool{}
	var createErr error
	created := false
	for attempt := 0; attempt < maxAttempts; attempt++ {
		channel, channelMethod, pickErr := PickAdtyEpayChannel(method, quote.PayAmount, device, exclude, time.Now().Unix(), allowed)
		if pickErr != nil {
			if attempt > 0 && errors.Is(createErr, model.ErrAdtyEpayDailyLimit) {
				return nil, ErrAdtyEpayChosenChannelUnavailable
			}
			if channelID > 0 {
				return nil, ErrAdtyEpayChosenChannelUnavailable
			}
			if errors.Is(pickErr, ErrAdtyEpayNoChannel) {
				return nil, common.NewMessage("This payment method is not available")
			}
			if errors.Is(pickErr, model.ErrAdtyEpayDailyLimit) {
				return nil, ErrAdtyEpayChosenChannelUnavailable
			}
			return nil, pickErr
		}
		exclude[channel.Id] = true
		payMoney, _ := quote.PayAmount.Float64()
		createTime := time.Now().Unix()
		topUp := &model.TopUp{
			UserId: leaderID, Amount: quote.FaceUSD.IntPart(), Money: payMoney, TradeNo: tradeNo,
			PaymentMethod: method, PaymentProvider: model.PaymentProviderAdtyEpay,
			CreateTime: createTime, Status: common.TopUpStatusPending,
		}
		ext := &model.AdtyTopupOrder{
			Business: model.AdtyBusinessTeamTopup, TradeNo: tradeNo, UserId: leaderID, TeamId: teamID, SiteId: team.SiteId,
			ExpireAt: AdtyEpayExpiry(createTime), CheckoutStage: "local", Provider: model.PaymentProviderAdtyEpay, Method: method,
			FaceUSD: quote.FaceUSD, BonusRate: decimal.Zero, BonusUSD: decimal.Zero,
			CreditQuota: quote.CreditQuota, PayAmount: quote.PayAmount,
			PayCurrency: quote.PayCurrency, PriceSnapshot: quote.PriceCNYPerUSD, TopupGroupRatio: quote.TopupGroupRatio,
			ChannelId: channel.Id, MethodLabel: channelMethod.DisplayName,
			MaxActivePending: maxActivePendingAdtyTeamTopUps,
		}
		createErr = CreateAdtyPaymentOrder(topUp, ext, sceneKey, channel)
		if errors.Is(createErr, model.ErrAdtyEpayDailyLimit) {
			if channelID > 0 {
				return nil, ErrAdtyEpayChosenChannelUnavailable
			}
			continue
		}
		if createErr != nil {
			if errors.Is(createErr, model.ErrAdtyIdempotencyConflict) {
				return nil, ErrAdtyTeamInvalid
			}
			return nil, createErr
		}
		created = true
		break
	}
	unlockCheckout()
	if !created {
		return nil, ErrAdtyEpayChosenChannelUnavailable
	}
	return ResumeAdtyTeamTopupPayment(leaderID, teamID, tradeNo, clientIP, device)
}

func startAdtyTeamBEpusdtTopup(leaderID int, teamID int64, requestSiteID int, amountUSD decimal.Decimal, method, key, sceneKey string) (*model.AdtyTopupOrder, error) {
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
		if requestSiteID > 0 && requestSiteID != existing.SiteId {
			return nil, ErrAdtyTeamForbidden
		}
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
	if requestSiteID > 0 && requestSiteID != team.SiteId {
		return nil, ErrAdtyTeamForbidden
	}
	var leader model.User
	if err := model.DB.First(&leader, leaderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAdtyTeamForbidden
		}
		return nil, err
	}
	if leader.Status != common.UserStatusEnabled || leader.StaffSiteId != 0 || leader.SiteId != team.SiteId {
		return nil, ErrAdtyTeamForbidden
	}
	quote, err := adtyTeamQuote(&team, leader.Group, amountUSD.IntPart(), method)
	if err != nil {
		return nil, err
	}
	quote.TeamID, quote.Business, quote.SiteID = teamID, model.AdtyBusinessTeamTopup, team.SiteId
	scene, err := AuthorizeAdtyPaymentScene(leaderID, sceneKey, model.PaymentProviderAdtyBEpusdt, method, quote)
	if err != nil {
		return nil, err
	}

	unlockCheckout := lockAdtyTeamCheckout(teamID)
	defer unlockCheckout()

	now := time.Now().Unix()
	pendingList, err := model.ListTeamActivePendingAdtyTopUps(teamID, now)
	if err != nil {
		return nil, err
	}
	if reusable := findReusableTeamPendingOrder(pendingList, leaderID, teamID, model.PaymentProviderAdtyBEpusdt, method, scene.Key, quote, 0, nil, now); reusable != nil {
		unlockCheckout()
		return ResumeAdtyTeamTopupPayment(leaderID, teamID, reusable.TradeNo, "")
	}
	if len(pendingList) >= maxActivePendingAdtyTeamTopUps {
		return nil, model.ErrAdtyTooManyPendingOrders
	}

	payMoney, _ := quote.PayAmount.Float64()
	createTime := time.Now().Unix()
	topUp := &model.TopUp{
		UserId: leaderID, Amount: quote.FaceUSD.IntPart(), Money: payMoney, TradeNo: tradeNo,
		PaymentMethod: method, PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		CreateTime: createTime, Status: common.TopUpStatusPending,
	}
	ext := &model.AdtyTopupOrder{
		Business: model.AdtyBusinessTeamTopup, TradeNo: tradeNo, UserId: leaderID, TeamId: teamID, SiteId: team.SiteId,
		ExpireAt: createTime + adtyUSDTOrderTimeoutSeconds(), CheckoutStage: "local",
		CheckoutGateway: strings.TrimRight(strings.TrimSpace(setting.GetBEpusdtSetting().BaseURL), "/"),
		Provider:        model.PaymentProviderAdtyBEpusdt, Method: method,
		FaceUSD: quote.FaceUSD, BonusRate: decimal.Zero, BonusUSD: decimal.Zero,
		CreditQuota: quote.CreditQuota, PayAmount: quote.PayAmount,
		PayCurrency: quote.PayCurrency, PriceSnapshot: quote.PriceCNYPerUSD, TopupGroupRatio: quote.TopupGroupRatio,
		MaxActivePending: maxActivePendingAdtyTeamTopUps,
	}
	if err := CreateAdtyPaymentOrder(topUp, ext, sceneKey); err != nil {
		if errors.Is(err, model.ErrAdtyIdempotencyConflict) {
			return nil, ErrAdtyTeamInvalid
		}
		return nil, err
	}
	unlockCheckout()
	return ResumeAdtyTeamTopupPayment(leaderID, teamID, tradeNo, "")
}

func findReusableTeamPendingOrder(pendingList []model.AdtyTopupOrder, leaderID int, teamID int64, provider, method, sceneKey string, quote *AdtyQuote, requestedChannel int, allowedChannels []int, now int64) *model.AdtyTopupOrder {
	if quote == nil {
		return nil
	}
	currentBEpusdtGateway := strings.TrimRight(strings.TrimSpace(setting.GetBEpusdtSetting().BaseURL), "/")
	for i := range pendingList {
		ext := &pendingList[i]
		if ext.TeamId != teamID || ext.UserId != leaderID || ext.SiteId != quote.SiteID || ext.Provider != provider || ext.Method != method {
			continue
		}
		if ext.PaymentScene != sceneKey && !(ext.PaymentScene == "" && sceneKey == model.AdtyBusinessWalletTopup) {
			continue
		}
		if !ext.FaceUSD.Equal(quote.FaceUSD) || ext.CreditQuota != quote.CreditQuota || ext.PayCurrency != quote.PayCurrency {
			continue
		}
		if ext.ExpireAt > 0 && ext.ExpireAt <= now+reusableTeamPendingMinRemaining {
			continue
		}
		if !teamCheckoutReady(ext) {
			continue
		}
		switch provider {
		case model.PaymentProviderAdtyBEpusdt:
			if !setting.BEpusdtConfigured() || !setting.BEpusdtChainEnabled(method) {
				continue
			}
			storedGW := strings.TrimRight(strings.TrimSpace(ext.CheckoutGateway), "/")
			if storedGW != "" && storedGW != currentBEpusdtGateway {
				continue
			}
			return ext
		case model.PaymentProviderAdtyEpay:
			if !ext.PayAmount.Equal(quote.PayAmount) || ext.ChannelId <= 0 {
				continue
			}
			if requestedChannel > 0 && ext.ChannelId != requestedChannel {
				continue
			}
			if len(allowedChannels) > 0 && !slices.Contains(allowedChannels, ext.ChannelId) {
				continue
			}
			channel, err := model.GetAdtyEpayChannel(ext.ChannelId)
			if err != nil || channel == nil || channel.Status != model.AdtyEpayStatusEnabled || !channel.HasCredentials() || !adtyEpayMethodAllowsAmount(channel.MethodFor(method), ext.PayAmount) {
				continue
			}
			return ext
		}
	}
	return nil
}

func adtyEpayMethodAllowsAmount(method *model.AdtyEpayChannelMethod, amount decimal.Decimal) bool {
	if method == nil || !method.Enabled {
		return false
	}
	if method.MinCNY.IsPositive() && amount.LessThan(method.MinCNY) {
		return false
	}
	if method.MaxCNY.IsPositive() && amount.GreaterThan(method.MaxCNY) {
		return false
	}
	return true
}

func validateReadyTeamTopupCheckout(ext *model.AdtyTopupOrder) error {
	if ext == nil || ext.Provider != model.PaymentProviderAdtyBEpusdt {
		return nil
	}
	if !setting.BEpusdtConfigured() || !setting.BEpusdtChainEnabled(ext.Method) {
		return model.ErrPaymentMethodMismatch
	}
	currentGateway := strings.TrimRight(strings.TrimSpace(setting.GetBEpusdtSetting().BaseURL), "/")
	storedGateway := strings.TrimRight(strings.TrimSpace(ext.CheckoutGateway), "/")
	if storedGateway != "" && storedGateway != currentGateway {
		return model.ErrPaymentMethodMismatch
	}
	return nil
}

func adtyTeamTopupTerminalStatusError(status string) error {
	switch status {
	case common.TopUpStatusExpired:
		return common.NewMessage("Order expired")
	case model.TopUpStatusCanceled:
		return common.NewMessage("Order has been canceled")
	case common.TopUpStatusFailed:
		return common.NewMessage("Order checkout failed. Please create a new order")
	case model.TopUpStatusRefunded:
		return common.NewMessage("Order has been refunded")
	case common.TopUpStatusSuccess:
		return common.NewMessage("Order has already been paid")
	default:
		return common.NewMessage("Order is no longer payable")
	}
}

func adtyTeamOrderExpired(ext *model.AdtyTopupOrder, now int64) bool {
	if ext == nil {
		return false
	}
	deadline := ext.ExpireAt
	if deadline == 0 {
		switch ext.Provider {
		case model.PaymentProviderAdtyEpay:
			if ext.CreatedAt > 0 {
				deadline = AdtyEpayExpiry(ext.CreatedAt)
			}
		case model.PaymentProviderAdtyBEpusdt:
			if ext.CreatedAt > 0 {
				deadline = ext.CreatedAt + adtyUSDTOrderTimeoutSeconds()
			}
		}
	}
	return deadline > 0 && deadline <= now
}

func refreshTeamEpaySubmitURLIfNeeded(ext *model.AdtyTopupOrder, clientIP, device string) error {
	if ext == nil || ext.Provider != model.PaymentProviderAdtyEpay || ext.ChannelId <= 0 {
		return nil
	}
	channel, err := model.GetAdtyEpayChannel(ext.ChannelId)
	if err != nil {
		if errors.Is(err, model.ErrAdtyEpayChannelNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAdtyEpayChosenChannelUnavailable
		}
		return err
	}
	if channel == nil {
		return ErrAdtyEpayChosenChannelUnavailable
	}
	method := channel.MethodFor(ext.Method)
	if channel.Status != model.AdtyEpayStatusEnabled || !channel.HasCredentials() || !adtyEpayMethodAllowsAmount(method, ext.PayAmount) {
		return ErrAdtyEpayChosenChannelUnavailable
	}
	if !channel.IsRSA() || channel.OrderMode != model.AdtyEpayOrderModeSubmit {
		return nil
	}
	callback := strings.TrimRight(GetCallbackAddress(), "/") + "/api/user/"
	newURL, refreshed, err := RefreshAdtyEpaySubmitURL(channel, method, AdtyEpayCreateRequest{
		TradeNo:   ext.TradeNo,
		Name:      "Team top-up " + ext.TradeNo,
		ClientIP:  clientIP,
		Device:    normalizeAdtyTeamDevice(device),
		MoneyCNY:  ext.PayAmount,
		NotifyURL: callback + "epay/notify/" + strconv.Itoa(channel.Id),
		ReturnURL: AdtyPayResultURL(ext.TradeNo),
	}, ext.PayURL)
	if err != nil {
		return ErrAdtyEpayChosenChannelUnavailable
	}
	if refreshed && newURL != "" {
		if err := model.UpdateAdtyTopupOrderFields(ext.TradeNo, map[string]any{"pay_url": newURL}); err != nil {
			return err
		}
		ext.PayURL = newURL
	}
	return nil
}

// Resume the original merchant order. The committed submitting marker survives
// loss of the gateway response. The order lock serializes gateway calls with
// other workers, cancellation, and final credit.
func ResumeAdtyTeamTopupPayment(leaderID int, teamID int64, tradeNo, clientIP string, device ...string) (*model.AdtyTopupOrder, error) {
	dev := "pc"
	if len(device) > 0 {
		dev = normalizeAdtyTeamDevice(device[0])
	}
	view, err := model.GetUserAdtyTopUp(tradeNo, leaderID)
	if err != nil {
		return nil, err
	}
	ext := view.Ext
	if teamID <= 0 || ext.TeamId != teamID || ext.UserId != leaderID {
		return nil, ErrAdtyTeamForbidden
	}
	if view.Status != common.TopUpStatusPending {
		return nil, adtyTeamTopupTerminalStatusError(view.Status)
	}
	if adtyTeamOrderExpired(ext, time.Now().Unix()) {
		return nil, common.NewMessage("Order expired")
	}
	var precheckTeam model.AdtyTeam
	if err := model.DB.First(&precheckTeam, teamID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAdtyTeamForbidden
		}
		return nil, err
	}
	if precheckTeam.LeaderUserId != leaderID || precheckTeam.Status != model.AdtyTeamStatusActive || ext.SiteId != precheckTeam.SiteId {
		return nil, ErrAdtyTeamForbidden
	}
	if teamCheckoutReady(ext) {
		if err := validateReadyTeamTopupCheckout(ext); err != nil {
			return nil, err
		}
		if err := refreshTeamEpaySubmitURLIfNeeded(ext, clientIP, dev); err != nil {
			return nil, err
		}
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
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return model.ErrTopUpNotFound
			}
			return err
		}
		if err := model.AdtyLockForUpdate(tx).First(ext, ext.Id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return model.ErrTopUpNotFound
			}
			return err
		}
		if top.Status != common.TopUpStatusPending {
			return adtyTeamTopupTerminalStatusError(top.Status)
		}
		if ext.Provider == model.PaymentProviderAdtyEpay && ext.ExpireAt == 0 {
			ext.ExpireAt = AdtyEpayExpiry(ext.CreatedAt)
			if err := tx.Model(ext).UpdateColumn("expire_at", ext.ExpireAt).Error; err != nil {
				return err
			}
		}
		if adtyTeamOrderExpired(ext, time.Now().Unix()) {
			return common.NewMessage("Order expired")
		}
		var team model.AdtyTeam
		if err := tx.First(&team, teamID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtyTeamForbidden
			}
			return err
		}
		if team.LeaderUserId != leaderID || team.Status != model.AdtyTeamStatusActive || ext.SiteId != team.SiteId {
			return ErrAdtyTeamForbidden
		}
		if teamCheckoutReady(ext) {
			alreadyDone = true
			return nil
		}
		if ext.Provider == model.PaymentProviderAdtyBEpusdt {
			if err := validateReadyTeamTopupCheckout(ext); err != nil {
				return err
			}
		}
		if ext.Provider == model.PaymentProviderAdtyEpay && IsAdtyEpayMethod(ext.Method) {
			ch := &model.AdtyEpayChannel{}
			if err := tx.Preload("Methods").First(ch, ext.ChannelId).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAdtyEpayChosenChannelUnavailable
				}
				return err
			}
			if ch.Status != model.AdtyEpayStatusEnabled || !ch.HasCredentials() || !adtyEpayMethodAllowsAmount(ch.MethodFor(ext.Method), ext.PayAmount) {
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
		latest, err := model.GetAdtyTopupOrder(tradeNo)
		if err != nil || latest == nil {
			return latest, err
		}
		if err := validateReadyTeamTopupCheckout(latest); err != nil {
			return nil, err
		}
		if err := refreshTeamEpaySubmitURLIfNeeded(latest, clientIP, dev); err != nil {
			return nil, err
		}
		return latest, nil
	}
	fields, recoverErr := recoverTeamCheckout(ctx, &extSnapshot, epayChannel, clientIP, dev, isReplay)
	if errors.Is(recoverErr, ErrAdtyPaymentDetected) {
		if err := RecoverAdtyHostedPayment(ctx, &extSnapshot); err != nil {
			return nil, err
		}
		return model.GetAdtyTopupOrder(tradeNo)
	}
	if recoverErr != nil {
		if !isReplay {
			if failErr := model.FailPendingAdtyTopUp(tradeNo, extSnapshot.Provider); failErr != nil {
				common.SysError(common.LogText("team topup fail pending order %s failed: %s", tradeNo, failErr.Error()))
			}
		}
		return nil, recoverErr
	}
	// Tx 2: Re-lock and persist checkout fields only if still pending, unready,
	// and the team remains active under the same leader.
	var (
		discarded         bool
		teamForbidden     bool
		terminalStatus    string
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
		if top.Status != common.TopUpStatusPending {
			discarded = true
			terminalStatus = top.Status
			return nil
		}
		if teamCheckoutReady(&cur) {
			discarded = true
			return nil
		}
		var team model.AdtyTeam
		if err := tx.First(&team, teamID).Error; err != nil {
			discarded = true
			teamForbidden = true
			return nil
		}
		if team.LeaderUserId != leaderID || team.Status != model.AdtyTeamStatusActive || cur.SiteId != team.SiteId {
			discarded = true
			teamForbidden = true
			return nil
		}
		if extSnapshot.Provider == model.PaymentProviderAdtyBEpusdt {
			if payAmount, ok := fields["pay_amount"].(decimal.Decimal); ok {
				money, _ := payAmount.Float64()
				if err := tx.Model(&model.TopUp{}).Where("id = ?", top.Id).Update("money", money).Error; err != nil {
					return err
				}
			}
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
			if !isReplay {
				if failErr := model.FailPendingAdtyTopUp(tradeNo, extSnapshot.Provider); failErr != nil {
					common.SysError(common.LogText("team topup fail pending order %s after writeback error failed: %s", tradeNo, failErr.Error()))
				}
			}
			return nil, err
		}
		if teamForbidden {
			if !isReplay {
				_ = model.FailPendingAdtyTopUp(tradeNo, extSnapshot.Provider)
			}
			return nil, ErrAdtyTeamForbidden
		}
		if terminalStatus != "" {
			return nil, adtyTeamTopupTerminalStatusError(terminalStatus)
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

func recoverTeamCheckout(ctx context.Context, ext *model.AdtyTopupOrder, channel *model.AdtyEpayChannel, clientIP, device string, replay bool) (map[string]any, error) {
	trade := ext.TradeNo
	device = normalizeAdtyTeamDevice(device)
	callback := strings.TrimRight(GetCallbackAddress(), "/") + "/api/user/"
	if ext.Provider == model.PaymentProviderAdtyBEpusdt && IsAdtyUSDTMethod(ext.Method) {
		if err := validateReadyTeamTopupCheckout(ext); err != nil {
			return nil, err
		}
		deadline := ext.ExpireAt
		if deadline == 0 {
			deadline = ext.CreatedAt + adtyUSDTOrderTimeoutSeconds()
		}
		remaining := deadline - time.Now().Unix()
		if !replay && remaining < 180 && remaining > 0 {
			remaining = 180
		}
		if remaining < 180 {
			return nil, common.NewMessage("Order expired")
		}
		if remaining > 3600 {
			remaining = 3600
		}
		order, err := CreateBEpusdtOrderContext(ctx, BEpusdtCreateRequest{
			OrderID: trade, AmountUSD: ext.PayAmount, TradeType: ext.Method, VerifyOrder: true,
			NotifyURL: callback + "bepusdt/notify", RedirectURL: AdtyPayResultURL(trade), Name: "Team top-up " + trade, Timeout: int(remaining),
		})
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrAdtyPaymentGatewayUnavailable, err)
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
	if channel.Status != model.AdtyEpayStatusEnabled || !channel.HasCredentials() || !adtyEpayMethodAllowsAmount(method, ext.PayAmount) {
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
			merchantID := strings.TrimSpace(result.MerchantId)
			if result.OutTradeNo != trade || (merchantID != "" && merchantID != strings.TrimSpace(channel.MerchantId)) || result.TypeCode != expectedType || !result.MoneyCNY.Equal(ext.PayAmount) {
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
		TradeNo: trade, Name: "Team top-up " + trade, ClientIP: clientIP, Device: device, MoneyCNY: ext.PayAmount,
		NotifyURL: callback + "epay/notify/" + strconv.Itoa(channel.Id), ReturnURL: AdtyPayResultURL(trade),
	})
	if err != nil {
		return nil, ErrAdtyEpayChosenChannelUnavailable
	}
	if result.Mode != "submit" && result.Mode != method.ReturnType {
		_ = model.UpdateAdtyEpayMethodReturnType(method.Id, result.Mode)
	}
	if result.GatewayTradeNo == "" {
		result.GatewayTradeNo = gatewayTradeNo
	}
	return map[string]any{"qr_code": result.QRCode, "pay_url": result.PayURL, "url_scheme": result.URLScheme, "gateway_trade_id": result.GatewayTradeNo}, nil
}
