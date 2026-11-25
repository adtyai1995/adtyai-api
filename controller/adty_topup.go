package controller

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
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
	ownerSiteID := 0
	if isOwnerBiz {
		ownerSiteID = service.ResolveAdtyOwnerTopupSiteID(c.GetInt("id"), service.AdtyRequestSiteID(c))
	}
	if business != model.AdtyBusinessTeamTopup {
		if view, err := model.GetUserPendingAdtyTopUpForBusiness(c.GetInt("id"), time.Now().Unix(), ownerSiteID, business); err == nil {
			pending = view
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
			if !errors.Is(cancelErr, service.ErrAdtyPaymentDetected) {
				providerCancelFailed = true
			}
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

var listUserAdtyTopUpsFn = model.ListUserAdtyTopUps

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

func (f adtyTopUpSelfFilter) Active() bool {
	return f.Business != "" || f.Status != "" ||
		f.PaymentMethod != "" ||
		f.PaymentScene != "" ||
		f.TradeNo != "" ||
		f.Keyword != "" ||
		f.StartTimestamp > 0 ||
		f.EndTimestamp > 0
}

type adtyTopUpFilterRow struct {
	UserID          int    `json:"user_id"`
	TradeNo         string `json:"trade_no"`
	PaymentMethod   string `json:"payment_method"`
	PaymentProvider string `json:"payment_provider"`
	Status          string `json:"status"`
	CreateTime      int64  `json:"create_time"`
	Ext             *struct {
		Method           string `json:"method"`
		MethodLabel      string `json:"method_label"`
		PaymentScene     string `json:"payment_scene"`
		PaymentSceneName string `json:"payment_scene_name"`
		GatewayTradeID   string `json:"gateway_trade_id"`
	} `json:"ext"`
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
		Business:       strings.TrimSpace(c.Query("business")),
		Status:         status,
		PaymentMethod:  paymentMethod,
		PaymentScene:   paymentScene,
		TradeNo:        tradeNo,
		Keyword:        keyword,
		StartTimestamp: startTS,
		EndTimestamp:   endTS,
	}, nil
}

func matchAdtyTopUpSelfFilter(userID int, item model.AdtyTopUpView, filter adtyTopUpSelfFilter) bool {
	if filter.OwnerSiteID != 0 && (item.Ext == nil || item.Ext.SiteId != filter.OwnerSiteID) {
		return false
	}
	if filter.Business != "" {
		effectiveBusiness := ""
		if item.Ext != nil {
			effectiveBusiness = item.Ext.Business
			if item.Ext.Provider == model.AdtyVoucherProvider && item.Ext.Method == model.AdtyVoucherPurposeSiteMargin {
				effectiveBusiness = model.AdtyBusinessOwnerMarginTopup
			}
		}
		if effectiveBusiness != filter.Business {
			return false
		}
	}
	raw, err := common.Marshal(item)
	if err != nil {
		return false
	}
	var row adtyTopUpFilterRow
	if err := common.Unmarshal(raw, &row); err != nil {
		return false
	}
	if row.UserID != 0 && row.UserID != userID {
		return false
	}
	if filter.Status != "" && normalizeAdtyOrderStatus(row.Status) != normalizeAdtyOrderStatus(filter.Status) {
		return false
	}
	if filter.PaymentMethod != "" {
		wantMethod := strings.ToLower(filter.PaymentMethod)
		gotMethod := strings.ToLower(row.PaymentMethod)
		extMethod := ""
		extLabel := ""
		if row.Ext != nil {
			extMethod = strings.ToLower(row.Ext.Method)
			extLabel = strings.ToLower(row.Ext.MethodLabel)
		}
		methodMatched := gotMethod == wantMethod ||
			extMethod == wantMethod ||
			(extLabel != "" && extLabel == wantMethod) ||
			(wantMethod == "usdt" && (strings.HasPrefix(gotMethod, "usdt.") || strings.HasPrefix(extMethod, "usdt.") || strings.EqualFold(row.PaymentProvider, service.AdtyProviderBEpusdt)))
		if !methodMatched {
			return false
		}
	}
	if filter.PaymentScene != "" {
		if row.Ext == nil {
			return false
		}
		if !strings.EqualFold(row.Ext.PaymentScene, filter.PaymentScene) &&
			!strings.EqualFold(row.Ext.PaymentSceneName, filter.PaymentScene) {
			return false
		}
	}
	if filter.TradeNo != "" && !strings.Contains(strings.ToLower(row.TradeNo), strings.ToLower(filter.TradeNo)) {
		return false
	}
	if filter.Keyword != "" {
		kw := strings.ToLower(filter.Keyword)
		candidates := []string{
			row.TradeNo,
			row.PaymentMethod,
			row.PaymentProvider,
			row.Status,
		}
		if row.Ext != nil {
			candidates = append(
				candidates,
				row.Ext.Method,
				row.Ext.MethodLabel,
				row.Ext.PaymentScene,
				row.Ext.PaymentSceneName,
				row.Ext.GatewayTradeID,
			)
		}
		matchedKeyword := false
		for _, candidate := range candidates {
			if candidate != "" && strings.Contains(strings.ToLower(candidate), kw) {
				matchedKeyword = true
				break
			}
		}
		if !matchedKeyword {
			return false
		}
	}
	if filter.StartTimestamp > 0 && row.CreateTime < filter.StartTimestamp {
		return false
	}
	if filter.EndTimestamp > 0 && row.CreateTime > filter.EndTimestamp {
		return false
	}
	return true
}

func newAdtyScanPageInfo(page, pageSize int) *common.PageInfo {
	var info common.PageInfo
	raw, _ := common.Marshal(map[string]int{
		"page":      page,
		"page_size": pageSize,
	})
	_ = common.Unmarshal(raw, &info)
	return &info
}

func collectUserAdtyTopUpsForFilter(userID int) ([]model.AdtyTopUpView, error) {
	const batchSize = 500
	firstPage := newAdtyScanPageInfo(1, batchSize)
	items, total, err := listUserAdtyTopUpsFn(userID, firstPage)
	if err != nil {
		return nil, err
	}
	if int(total) <= len(items) || len(items) == 0 {
		return items, nil
	}
	all := make([]model.AdtyTopUpView, 0, int(total))
	all = append(all, items...)
	for page := 2; len(all) < int(total); page++ {
		nextPage := newAdtyScanPageInfo(page, batchSize)
		batch, _, err := listUserAdtyTopUpsFn(userID, nextPage)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		all = append(all, batch...)
	}
	return all, nil
}

// GetAdtyTopUpSelf 分页列出本人的充值记录，支持按状态、支付方式、订单号、关键字与时间范围筛选。
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
	if !filter.Active() {
		items, total, err := listUserAdtyTopUpsFn(userID, pageInfo)
		if err != nil {
			writeAdtyTopUpError(c, err)
			return
		}
		pageInfo.SetTotal(int(total))
		pageInfo.SetItems(items)
		common.ApiSuccess(c, pageInfo)
		return
	}
	allItems, err := collectUserAdtyTopUpsForFilter(userID)
	if err != nil {
		writeAdtyTopUpError(c, err)
		return
	}
	matched := make([]model.AdtyTopUpView, 0, len(allItems))
	for _, item := range allItems {
		if matchAdtyTopUpSelfFilter(userID, item, filter) {
			matched = append(matched, item)
		}
	}
	start := pageInfo.GetStartIdx()
	pageSize := pageInfo.GetPageSize()
	var pageItems []model.AdtyTopUpView
	if start >= 0 && start < len(matched) {
		end := start + min(pageSize, len(matched)-start)
		pageItems = matched[start:end]
	} else {
		pageItems = make([]model.AdtyTopUpView, 0)
	}
	pageInfo.SetTotal(len(matched))
	pageInfo.SetItems(pageItems)
	common.ApiSuccess(c, pageInfo)
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

// replaceOtherPendingAdtyOrders freezes the user's other payable orders before
// a new one is created, and asks the gateway to cancel when it can.
func replaceOtherPendingAdtyOrders(c *gin.Context, quote ...*service.AdtyQuote) error {
	userID := c.GetInt("id")
	ownerSiteID := 0
	business := ""
	if len(quote) > 0 && quote[0] != nil && (quote[0].Business == model.AdtyBusinessOwnerSelfUseTopup || quote[0].Business == model.AdtyBusinessOwnerMarginTopup) {
		ownerSiteID = quote[0].SiteID
		business = quote[0].Business
	}
	tradeNos, err := model.ListUserPendingAdtyTradeNosForBusiness(userID, ownerSiteID, business)
	if err != nil {
		return err
	}
	for _, tradeNo := range tradeNos {
		_, err := model.ReplaceAdtyTopUpAfterLock(tradeNo, userID, func(locked *model.AdtyTopupOrder) error {
			provider := service.AdtyPaymentProviderByName(locked.Provider)
			if provider == nil {
				return nil
			}
			cancelErr := provider.Cancel(locked)
			if cancelErr != nil {
				return cancelErr
			}
			return nil
		})
		if errors.Is(err, model.ErrAdtyTopUpNotCancel) {
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
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
		Business:        quote.Business,
		TradeNo:         tradeNo,
		UserId:          userID,
		Provider:        draft.provider,
		Method:          draft.method,
		BonusMethod:     service.AdtyBonusMethod(draft.method),
		BonusExpiresAt:  0,
		FaceUSD:         quote.FaceUSD,
		BonusRate:       quote.BonusRate,
		BonusUSD:        quote.BonusUSD,
		CreditQuota:     quote.CreditQuota,
		PayAmount:       quote.PayAmount,
		PayCurrency:     quote.PayCurrency,
		PriceSnapshot:   quote.PriceCNYPerUSD,
		TopupGroupRatio: quote.TopupGroupRatio,
		ExpireAt:        draft.expireAt,
		ChannelId:       draft.channelID,
		MethodLabel:     draft.methodLabel,
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
	default:
		common.SysError(common.LogText("adty topup: %s", err.Error()))
		common.ApiErrorT(c, "Database error, please contact the administrator")
	}
}

// adtyCallerIP 是写日志用的来源 IP。
func adtyCallerIP(c *gin.Context) string {
	return strings.TrimSpace(c.ClientIP())
}
