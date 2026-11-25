package controller

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

type adtyTierView struct {
	Amount int     `json:"amount"`
	Rate   float64 `json:"rate"`
}

// GetAdtyTopUpInfo 返回钱包页要的数据：档位和加赠、最低金额、支付方式、价格、有效期、待付款订单。
func GetAdtyTopUpInfo(c *gin.Context) {
	setting := adty_setting.GetTopupSetting()
	payment := operation_setting.GetPaymentSetting()
	tiers := make([]adtyTierView, 0, len(payment.AmountOptions))
	for _, amount := range payment.AmountOptions {
		tiers = append(tiers, adtyTierView{Amount: amount, Rate: 0})
	}
	bonusRates := map[string]float64{}
	methods := make([]service.AdtyMethodInfo, 0)
	teamID, _ := strconv.ParseInt(c.Query("team_id"), 10, 64)
	scenes, err := service.AdtyPaymentScenesForUser(c.GetInt("id"), service.AdtyPaymentScope{TeamID: teamID, Business: c.Query("business"), SiteID: service.AdtyRequestSiteID(c)})
	if err != nil {
		writeAdtyTopUpError(c, err)
		return
	}
	business := c.Query("business")
	isOwnerBiz := business == model.AdtyBusinessOwnerSelfUseTopup || business == model.AdtyBusinessOwnerMarginTopup
	for _, scene := range scenes {
		if scene.Key == model.AdtyBusinessWalletTopup || isOwnerBiz || business == model.AdtyBusinessTeamTopup {
			methods = scene.Methods
			break
		}
	}
	if len(methods) == 0 && len(scenes) > 0 {
		methods = scenes[0].Methods
	}
	methodBonusRates := service.AdtyActiveMethodBonusRates(c.GetInt("id"), service.AdtyRequestSiteID(c), business)
	var pending *model.AdtyTopUpView
	pendingOrders := make([]model.AdtyTopUpView, 0)
	ownerSiteID := 0
	if isOwnerBiz {
		ownerSiteID = service.ResolveAdtyOwnerTopupSiteID(c.GetInt("id"), service.AdtyRequestSiteID(c))
	}
	if business != model.AdtyBusinessTeamTopup && (!isOwnerBiz || ownerSiteID > 0) {
		if views, err := model.ListUserActivePendingAdtyTopUpViewsForBusiness(c.GetInt("id"), time.Now().Unix(), ownerSiteID, business); err == nil {
			pendingOrders = views
			if len(views) > 0 {
				first := views[0]
				pending = &first
			}
		}
	}
	common.ApiSuccess(c, gin.H{
		"tiers":              tiers,
		"bonus_rates":        bonusRates,
		"method_bonus_rates": methodBonusRates,
		"min_topup":          operation_setting.GetMinTopUp(),
		"methods":            methods,
		"scenes":             scenes,
		"price_cny_per_usd":  operation_setting.GetPrice(),
		"usdt_order_minutes": setting.USDTOrderMinutes,
		"epay_order_minutes": setting.EpayOrderMinutes,
		"pending":            pending,
		"pending_orders":     pendingOrders,
	})
}

type adtyQuoteRequest struct {
	Business     string `json:"business"`
	PaymentScene string `json:"payment_scene"`
	Amount       int64  `json:"amount"`
	Method       string `json:"method"`
	ChannelId    int    `json:"channel_id"` // 易支付：客户选的通道，0 表示不指定
	TeamId       int64  `json:"team_id"`
}

// PostAdtyTopUpQuote 报价，同时返回这个金额下这种方式能不能用。
func PostAdtyTopUpQuote(c *gin.Context) {
	var req adtyQuoteRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	req.Method = strings.TrimSpace(req.Method)
	var (
		quote *service.AdtyQuote
		err   error
	)
	if req.Business == model.AdtyBusinessTeamTopup {
		quote, err = service.AdtyQuoteForTeamPayment(c.GetInt("id"), service.AdtyRequestSiteID(c), req.TeamId, c.GetString("group"), req.Amount, req.Method)
	} else {
		quote, err = service.AdtyQuoteForPayment(c.GetInt("id"), service.AdtyRequestSiteID(c), c.GetString("group"), req.Business, req.Amount, req.Method)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	available := false
	if provider := service.AdtyPaymentProviderForMethod(req.Method); provider != nil {
		scene, err := service.AuthorizeAdtyPaymentScene(c.GetInt("id"), req.PaymentScene, provider.Name(), req.Method, quote)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if provider.Name() == service.AdtyProviderEpay {
			if allowed, ok := service.AdtyEpayChannelAllowed(scene, req.ChannelId); ok {
				_, _, err := service.PickAdtyEpayChannel(req.Method, quote.PayAmount, "desktop", nil, time.Now().Unix(), allowed)
				available = err == nil
			}
		} else {
			available = provider.AvailableFor(req.Method, quote)
		}
	}
	common.ApiSuccess(c, gin.H{"quote": quote, "available": available})
}

// GetAdtyTopUpOrder 返回本人一笔订单的状态和支付信息；别人的订单当作不存在。
func GetAdtyTopUpOrder(c *gin.Context) {
	view, err := model.GetUserAdtyTopUp(c.Param("trade_no"), c.GetInt("id"))
	if err != nil {
		writeAdtyTopUpError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

// CancelAdtyTopUpOrder 先锁住本地订单，再问支付商。只有网关明确说已经有转账时才留下待支付。
func CancelAdtyTopUpOrder(c *gin.Context) {
	userID := c.GetInt("id")
	var providerCancelFailed bool
	ext, err := model.CancelAdtyTopUpAfterLock(c.Param("trade_no"), userID, func(locked *model.AdtyTopupOrder) error {
		provider := service.AdtyPaymentProviderByName(locked.Provider)
		if provider == nil {
			return nil
		}
		cancelErr := provider.Cancel(locked)
		if cancelErr != nil {
			common.SysLog(common.LogText("cancel adty order at provider failed trade_no=%s: %s", locked.TradeNo, cancelErr.Error()))
			if errors.Is(cancelErr, service.ErrAdtyPaymentDetected) {
				return cancelErr
			}
			// Epay orders with no reserved site gift hold zero site funds; PAY-06 still
			// auto-credits late callbacks on canceled orders, so a broken/unqueryable
			// Epay gateway must not trap users from canceling unpaid orders.
			if locked.Provider == model.PaymentProviderAdtyEpay &&
				!locked.GiftReserveEarning.IsPositive() &&
				!locked.GiftReserveMargin.IsPositive() {
				return nil
			}
			providerCancelFailed = true
			return cancelErr
		}
		return nil
	})
	if errors.Is(err, service.ErrAdtyPaymentDetected) {
		common.ApiErrorT(c, "A transfer has been detected. Please wait")
		return
	}
	if err != nil && providerCancelFailed {
		common.ApiErrorT(c, "Payment gateway is temporarily unavailable. Please try again later")
		return
	}
	if err != nil {
		writeAdtyTopUpError(c, err)
		return
	}
	if service.AdtyCanceledHook != nil {
		service.AdtyCanceledHook(ext)
	}
	common.ApiSuccess(c, nil)
}

// FlushAdtyBatchUpdates folds durable quota deltas and local statistics before
// pg_dump. It is not a user route: the shared token must match.
func FlushAdtyBatchUpdates(c *gin.Context) {
	expected := os.Getenv("ADTY_FLUSH_TOKEN")
	provided := c.GetHeader("X-Adty-Flush-Token")
	if expected == "" || len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if err := model.FlushBatchUpdates(); err != nil {
		common.SysError(common.LogText("batch flush failed: %s", err.Error()))
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Batch flush failed"})
		return
	}
	common.ApiSuccess(c, gin.H{"flushed": true})
}

// GetAdtyTopUpPending 返回本人最新一笔还没过期的 pending 订单，没有时 data 为 null。
func GetAdtyTopUpPending(c *gin.Context) {
	business := c.Query("business")
	ownerSiteID := 0
	if business == model.AdtyBusinessOwnerSelfUseTopup || business == model.AdtyBusinessOwnerMarginTopup {
		ownerSiteID = service.ResolveAdtyOwnerTopupSiteID(c.GetInt("id"), service.AdtyRequestSiteID(c))
		if ownerSiteID <= 0 {
			common.ApiSuccess(c, nil)
			return
		}
	}
	view, err := model.GetUserPendingAdtyTopUpForBusiness(c.GetInt("id"), time.Now().Unix(), ownerSiteID, business)
	if err != nil {
		writeAdtyTopUpError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

var listUserAdtyTopUpsFilteredFn = model.ListUserAdtyTopUpsFiltered

type adtyTopUpSelfFilter struct {
	OwnerSiteID    int
	Business       string
	Status         string
	PaymentMethod  string
	PaymentScene   string
	TradeNo        string
	Keyword        string
	StartTimestamp int64
	EndTimestamp   int64
}

func firstAdtyQuery(c *gin.Context, keys ...string) string {
	for _, key := range keys {
		if val := strings.TrimSpace(c.Query(key)); val != "" {
			return val
		}
	}
	return ""
}

func parseAdtyTopUpFilterTimestamp(raw string, isEnd bool) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if unixVal, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if unixVal < 0 {
			return 0, fmt.Errorf("negative timestamp")
		}
		if unixVal > 1_000_000_000_000 {
			unixVal /= 1000
		}
		return unixVal, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.Unix(), nil
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04",
	} {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return t.Unix(), nil
		}
	}
	if d, err := time.ParseInLocation("2006-01-02", raw, time.Local); err == nil {
		if isEnd {
			return d.Add(24*time.Hour - time.Second).Unix(), nil
		}
		return d.Unix(), nil
	}
	return 0, fmt.Errorf("invalid timestamp")
}

func normalizeAdtyOrderStatus(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "cancelled" {
		return "canceled"
	}
	return v
}

func parseAdtyTopUpSelfFilter(c *gin.Context) (adtyTopUpSelfFilter, error) {
	business := strings.TrimSpace(c.Query("business"))
	if business == "" {
		business = model.AdtyBusinessWalletTopup
	} else if strings.EqualFold(business, "all") {
		business = ""
	}
	status := strings.TrimSpace(c.Query("status"))
	if strings.EqualFold(status, "all") {
		status = ""
	}
	paymentMethod := firstAdtyQuery(c, "payment_method", "method")
	if strings.EqualFold(paymentMethod, "all") {
		paymentMethod = ""
	}
	paymentScene := firstAdtyQuery(c, "payment_scene", "scene")
	if strings.EqualFold(paymentScene, "all") {
		paymentScene = ""
	}
	tradeNo := strings.TrimSpace(c.Query("trade_no"))
	keyword := firstAdtyQuery(c, "keyword", "q")
	startTS, err := parseAdtyTopUpFilterTimestamp(firstAdtyQuery(c, "start_timestamp", "start_time", "from"), false)
	if err != nil {
		return adtyTopUpSelfFilter{}, err
	}
	endTS, err := parseAdtyTopUpFilterTimestamp(firstAdtyQuery(c, "end_timestamp", "end_time", "to"), true)
	if err != nil {
		return adtyTopUpSelfFilter{}, err
	}
	if startTS > 0 && endTS > 0 && startTS > endTS {
		return adtyTopUpSelfFilter{}, fmt.Errorf("start timestamp after end timestamp")
	}
	return adtyTopUpSelfFilter{
		Business:       business,
		Status:         normalizeAdtyOrderStatus(status),
		PaymentMethod:  paymentMethod,
		PaymentScene:   paymentScene,
		TradeNo:        tradeNo,
		Keyword:        keyword,
		StartTimestamp: startTS,
		EndTimestamp:   endTS,
	}, nil
}

// GetAdtyTopUpSelf 分页列出本人的充值记录，支持按业务域、状态、支付方式、订单号、关键字与时间范围在 SQL 层筛选。
func GetAdtyTopUpSelf(c *gin.Context) {
	userID := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)
	filter, err := parseAdtyTopUpSelfFilter(c)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if filter.Business == model.AdtyBusinessOwnerSelfUseTopup || filter.Business == model.AdtyBusinessOwnerMarginTopup {
		filter.OwnerSiteID = service.ResolveAdtyOwnerTopupSiteID(userID, service.AdtyRequestSiteID(c))
		if filter.OwnerSiteID <= 0 {
			filter.OwnerSiteID = -1
		}
	}
	items, total, err := listUserAdtyTopUpsFilteredFn(userID, model.AdtyUserTopUpListFilter{
		OwnerSiteID:    filter.OwnerSiteID,
		Business:       filter.Business,
		Status:         filter.Status,
		Method:         filter.PaymentMethod,
		PaymentScene:   filter.PaymentScene,
		TradeNo:        filter.TradeNo,
		Keyword:        filter.Keyword,
		StartTimestamp: filter.StartTimestamp,
		EndTimestamp:   filter.EndTimestamp,
		Offset:         pageInfo.GetStartIdx(),
		Limit:          pageInfo.GetPageSize(),
	})
	if err != nil {
		writeAdtyTopUpError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

var (
	adtyUserCheckoutLocks     sync.Map
	adtyUserCheckoutLockGuard sync.Mutex
)

// lockAdtyUserCheckout serializes per-user checkout reuse checks and order creation
// so concurrent clicks cannot race past pending reuse or the active pending cap.
func lockAdtyUserCheckout(userID int) func() {
	adtyUserCheckoutLockGuard.Lock()
	var rcm *refCountedMutex
	if v, ok := adtyUserCheckoutLocks.Load(userID); ok {
		rcm = v.(*refCountedMutex)
	} else {
		rcm = &refCountedMutex{}
		adtyUserCheckoutLocks.Store(userID, rcm)
	}
	rcm.refCount++
	adtyUserCheckoutLockGuard.Unlock()
	rcm.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			rcm.mu.Unlock()
			adtyUserCheckoutLockGuard.Lock()
			rcm.refCount--
			if rcm.refCount == 0 {
				adtyUserCheckoutLocks.Delete(userID)
			}
			adtyUserCheckoutLockGuard.Unlock()
		})
	}
}

// adtyOrderDraft 是下单时要的数据，P4 和 P5 共用 createAdtyOrder。
type adtyOrderDraft struct {
	paymentScene string
	prefix       string // EPU | EPY
	provider     string
	method       string
	quote        *service.AdtyQuote
	expireAt     int64
	channelID    int
	channel      *model.AdtyEpayChannel
	methodLabel  string // 易支付通道方式的显示名称，下单时记进订单
}

// newAdtyTradeNo 生成 <前缀><uid><6 位随机><unix>，易支付要求不超过 32 位。
func newAdtyTradeNo(prefix string, userID int) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%d%06d%d", prefix, userID, n.Int64(), time.Now().Unix()), nil
}

const (
	maxActivePendingAdtyTopUps  = model.MaxActivePendingAdtyTopUps
	reusablePendingMinRemaining = int64(60)
)

type adtyPendingReuseFilter struct {
	paymentScene     string
	provider         string
	method           string
	device           string
	quote            *service.AdtyQuote
	requestedChannel int
	allowedChannels  []int
	excludedChannels map[int]bool
}

func adtyPendingScopeFromQuote(quote *service.AdtyQuote) (ownerSiteID int, business string) {
	if quote != nil && (quote.Business == model.AdtyBusinessOwnerSelfUseTopup || quote.Business == model.AdtyBusinessOwnerMarginTopup) {
		return quote.SiteID, quote.Business
	}
	return 0, ""
}

// findReusablePendingOrCheckCap reuses an existing unexpired pending order matching the
// same payment method, scene, and quote snapshot when available; otherwise enforces the
// per-user active unexpired pending order cap (maxActivePendingAdtyTopUps = 10) without
// canceling or replacing orders on other payment methods.
func findReusablePendingOrCheckCap(c *gin.Context, filter adtyPendingReuseFilter) (*model.AdtyTopUpView, error) {
	userID := c.GetInt("id")
	now := time.Now().Unix()
	ownerSiteID, scopeBusiness := adtyPendingScopeFromQuote(filter.quote)
	pendingList, err := model.ListUserActivePendingAdtyTopUpsForBusiness(userID, now, ownerSiteID, scopeBusiness)
	if err != nil {
		return nil, err
	}
	expectedSiteID := 0
	if filter.quote != nil {
		if filter.quote.Business == model.AdtyBusinessOwnerSelfUseTopup || filter.quote.Business == model.AdtyBusinessOwnerMarginTopup {
			expectedSiteID = filter.quote.SiteID
		} else if site := service.AdtyBillingSiteForUser(userID); site != nil {
			expectedSiteID = site.Id
		}
	}
	device := filter.device
	if device != "mobile" {
		device = "pc"
	}
	for i := range pendingList {
		ext := &pendingList[i]
		if !adtyPendingOrderReusable(ext, filter, expectedSiteID, now) {
			continue
		}
		view, err := model.GetUserAdtyTopUp(ext.TradeNo, userID)
		if err != nil {
			if errors.Is(err, model.ErrTopUpNotFound) {
				continue
			}
			return nil, err
		}
		if view == nil || view.Status != common.TopUpStatusPending || view.Ext == nil || view.Ext.ExpireAt <= now+reusablePendingMinRemaining {
			continue
		}
		if view.Ext.Provider == model.PaymentProviderAdtyEpay && view.Ext.ChannelId > 0 {
			channel, chErr := model.GetAdtyEpayChannel(view.Ext.ChannelId)
			if chErr == nil && channel != nil && channel.IsRSA() && channel.OrderMode == model.AdtyEpayOrderModeSubmit {
				chMethod := channel.MethodFor(view.Ext.Method)
				if chMethod == nil {
					continue
				}
				callback := strings.TrimRight(service.GetCallbackAddress(), "/")
				newPayURL, refreshed, refreshErr := service.RefreshAdtyEpaySubmitURL(channel, chMethod, service.AdtyEpayCreateRequest{
					TradeNo:   view.Ext.TradeNo,
					Name:      "Top-up " + view.Ext.TradeNo,
					ClientIP:  c.ClientIP(),
					Device:    device,
					MoneyCNY:  view.Ext.PayAmount,
					NotifyURL: callback + "/api/user/epay/notify/" + strconv.Itoa(channel.Id),
					ReturnURL: service.AdtyPayResultURLForHost(view.Ext.TradeNo, view.Ext.Business == model.AdtyBusinessOwnerMarginTopup && service.AdtyRequestSiteID(c) == 0),
				}, view.Ext.PayURL)
				if refreshErr != nil {
					common.SysLog(common.LogText("refresh reusable epay submit url failed trade_no=%s: %s", view.Ext.TradeNo, refreshErr.Error()))
					continue
				}
				if refreshed && newPayURL != "" {
					if err := model.UpdateAdtyTopupOrderFields(view.Ext.TradeNo, map[string]any{"pay_url": newPayURL}); err != nil {
						return nil, err
					}
					view.Ext.PayURL = newPayURL
				}
			}
		}
		return view, nil
	}
	if len(pendingList) >= maxActivePendingAdtyTopUps {
		return nil, model.ErrAdtyTooManyPendingOrders
	}
	return nil, nil
}

func adtyPendingOrderReusable(ext *model.AdtyTopupOrder, filter adtyPendingReuseFilter, expectedSiteID int, now int64) bool {
	if ext == nil || filter.quote == nil {
		return false
	}
	if ext.ExpireAt <= now+reusablePendingMinRemaining {
		return false
	}
	if ext.Provider != filter.provider || ext.Method != filter.method || ext.PaymentScene != filter.paymentScene {
		return false
	}
	if ext.Business != filter.quote.Business || ext.SiteId != expectedSiteID {
		return false
	}
	if !ext.FaceUSD.Equal(filter.quote.FaceUSD) || !ext.BonusUSD.Equal(filter.quote.BonusUSD) ||
		ext.CreditQuota != filter.quote.CreditQuota || ext.PayCurrency != filter.quote.PayCurrency {
		return false
	}
	switch filter.provider {
	case model.PaymentProviderAdtyBEpusdt:
		// BEpusdt adjusts PayAmount by small unique decimals (e.g. 10.0001), so
		// FaceUSD + BonusUSD + CreditQuota + PayCurrency + non-empty address define match.
		return strings.TrimSpace(ext.PayAddress) != ""
	case model.PaymentProviderAdtyEpay:
		if !ext.PayAmount.Equal(filter.quote.PayAmount) {
			return false
		}
		if strings.TrimSpace(ext.PayURL) == "" && strings.TrimSpace(ext.QRCode) == "" && strings.TrimSpace(ext.URLScheme) == "" {
			return false
		}
		if ext.ChannelId <= 0 {
			return false
		}
		if filter.requestedChannel > 0 && ext.ChannelId != filter.requestedChannel {
			return false
		}
		if len(filter.allowedChannels) > 0 && !slices.Contains(filter.allowedChannels, ext.ChannelId) {
			return false
		}
		if filter.excludedChannels[ext.ChannelId] {
			return false
		}
		channel, err := model.GetAdtyEpayChannel(ext.ChannelId)
		if err != nil || channel == nil || channel.Status != model.AdtyEpayStatusEnabled || !channel.HasCredentials() {
			return false
		}
		methodEnabled := false
		for i := range channel.Methods {
			if channel.Methods[i].Method == filter.method && channel.Methods[i].Enabled {
				methodEnabled = true
				break
			}
		}
		return methodEnabled
	default:
		return false
	}
}

// createAdtyOrder 写 top_ups 和扩展行，带上返佣快照。
func createAdtyOrder(c *gin.Context, draft adtyOrderDraft) (*model.TopUp, *model.AdtyTopupOrder, error) {
	userID := c.GetInt("id")
	tradeNo, err := newAdtyTradeNo(draft.prefix, userID)
	if err != nil {
		return nil, nil, err
	}
	quote := draft.quote
	payMoney, _ := quote.PayAmount.Float64()
	topUp := &model.TopUp{
		UserId:          userID,
		Amount:          quote.FaceUSD.IntPart(),
		Money:           payMoney,
		TradeNo:         tradeNo,
		PaymentMethod:   draft.method,
		PaymentProvider: draft.provider,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	ext := &model.AdtyTopupOrder{
		Business:         quote.Business,
		TradeNo:          tradeNo,
		UserId:           userID,
		Provider:         draft.provider,
		Method:           draft.method,
		BonusMethod:      service.AdtyBonusMethod(draft.method),
		BonusExpiresAt:   0,
		FaceUSD:          quote.FaceUSD,
		BonusRate:        quote.BonusRate,
		BonusUSD:         quote.BonusUSD,
		CreditQuota:      quote.CreditQuota,
		PayAmount:        quote.PayAmount,
		PayCurrency:      quote.PayCurrency,
		PriceSnapshot:    quote.PriceCNYPerUSD,
		TopupGroupRatio:  quote.TopupGroupRatio,
		ExpireAt:         draft.expireAt,
		ChannelId:        draft.channelID,
		MethodLabel:      draft.methodLabel,
		MaxActivePending: maxActivePendingAdtyTopUps,
	}
	if quote.Business == model.AdtyBusinessOwnerSelfUseTopup || quote.Business == model.AdtyBusinessOwnerMarginTopup {
		ext.SiteId = quote.SiteID
	} else if site := service.AdtyBillingSiteForUser(userID); site != nil {
		ext.SiteId = site.Id // 分站客户的加赠由站长出（任务 11.5）
	}
	if err := service.CreateAdtyPaymentOrder(topUp, ext, draft.paymentScene, draft.channel); err != nil {
		return nil, nil, err
	}
	return topUp, ext, nil
}

func writeAdtyTopUpError(c *gin.Context, err error) {
	var message *common.Message
	switch {
	case errors.As(err, &message):
		common.ApiError(c, err)
	case errors.Is(err, model.ErrTopUpNotFound):
		common.ApiErrorStatus(c, 404, common.NewMessage("Order not found"))
	case errors.Is(err, model.ErrAdtyTopUpNotCancel):
		common.ApiErrorT(c, "Only orders waiting for payment can be canceled")
	case errors.Is(err, service.ErrAdtyPaymentDetected):
		common.ApiErrorT(c, "A transfer has been detected. Please wait")
	case errors.Is(err, model.ErrPaymentMethodMismatch):
		common.ApiErrorT(c, "This payment method is not available")
	default:
		common.SysError(common.LogText("adty topup: %s", err.Error()))
		common.ApiErrorT(c, "Database error, please contact the administrator")
	}
}

// adtyCallerIP 是写日志用的来源 IP。
func adtyCallerIP(c *gin.Context) string {
	return strings.TrimSpace(c.ClientIP())
}
