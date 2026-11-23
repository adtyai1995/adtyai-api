package controller

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	OrderTypeTopup           = "topup"
	OrderTypeConsumption     = "consumption"
	OrderTypeVoucher         = "voucher"
	OrderTypeRefund          = "refund"
	OrderTypeWithdrawal      = "withdrawal"
	OrderTypeCommission      = "commission"
	OrderTypeStaffAdjustment = "staff_adjustment"
)

var unifiedOrderTypeOptions = []string{
	OrderTypeTopup,
	OrderTypeConsumption,
	OrderTypeVoucher,
	OrderTypeRefund,
	OrderTypeWithdrawal,
	OrderTypeCommission,
	OrderTypeStaffAdjustment,
	"other",
}

func normalizeUnifiedOrderType(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all":
		return "", true
	case OrderTypeTopup, "recharge":
		return OrderTypeTopup, true
	case OrderTypeConsumption, "api_consumption", "usage":
		return OrderTypeConsumption, true
	case OrderTypeVoucher:
		return OrderTypeVoucher, true
	case OrderTypeRefund:
		return OrderTypeRefund, true
	case OrderTypeWithdrawal:
		return OrderTypeWithdrawal, true
	case OrderTypeCommission:
		return OrderTypeCommission, true
	case OrderTypeStaffAdjustment, "adjustment", "staff_balance":
		return OrderTypeStaffAdjustment, true
	case "other":
		return "other", true
	default:
		return "", false
	}
}

// Each source projects committed financial facts. UNION ALL stays inside SQL,
// so filters, exact totals, detail lookups, and pagination share one scope.
type adtyUnifiedSource struct {
	from, where string
	columns     map[string]string
}

func adtyUnifiedOrdersQuery(c *gin.Context, ownerSiteID *int, selfUserID *int) (*gorm.DB, bool) {
	kind, valid := normalizeUnifiedOrderType(c.Query("order_type"))
	if !valid {
		common.ApiErrorT(c, "Invalid parameters")
		return nil, false
	}
	columns := []string{"id", "user_id", "site_id", "order_type", "source_type", "reference", "original_trade_no", "team_id", "trade_no", "payment_method", "payment_provider", "status", "create_time", "complete_time", "amount", "face_usd", "credit_quota", "paid_amount", "paid_currency", "channel_id"}
	sources := []adtyUnifiedSource{
		{from: "top_ups x LEFT JOIN adty_topup_orders e ON e.trade_no = x.trade_no", columns: map[string]string{
			"id": "x.id", "user_id": "x.user_id", "site_id": "e.site_id", "order_type": "CASE WHEN x.payment_provider = '" + model.AdtyVoucherProvider + "' THEN 'voucher' ELSE 'topup' END",
			"source_type": "'topup'", "reference": "x.trade_no", "trade_no": "x.trade_no", "original_trade_no": "x.trade_no",
			"payment_method": "x.payment_method", "payment_provider": "x.payment_provider", "status": "x.status", "create_time": "x.create_time", "complete_time": "x.complete_time",
			"amount": "x.amount", "face_usd": "COALESCE(e.face_usd, x.amount)", "credit_quota": "COALESCE(e.credit_quota, 0)", "paid_amount": "COALESCE(e.paid_amount, 0)", "paid_currency": "COALESCE(e.paid_currency, '')", "channel_id": "COALESCE(e.channel_id, 0)",
		}},
		{from: "billing_operations x", where: "x.state = 'settled' AND x.applied_quota > 0", columns: map[string]string{
			"id": "x.reference_id", "user_id": "x.user_id", "site_id": "x.site_id", "order_type": "'consumption'", "source_type": "'billing'",
			"reference": "x.operation_key", "trade_no": "x.operation_key", "team_id": "x.payer_team_id", "payment_method": "'api'", "payment_provider": "'wallet'", "status": "x.state", "create_time": "x.created_at", "complete_time": "x.updated_at",
			"amount": "x.applied_quota", "face_usd": "x.applied_quota / 500000.0", "credit_quota": "x.applied_quota", "paid_amount": "x.applied_quota / 500000.0", "paid_currency": "'USD'",
		}},
		{from: "adty_manual_refunds x", columns: map[string]string{
			"id": "x.id", "user_id": "x.user_id", "site_id": "x.site_id", "order_type": "'refund'", "source_type": "'manual_refund'", "reference": "x.refund_id", "trade_no": "x.refund_id", "original_trade_no": "x.trade_no",
			"payment_method": "'manual'", "payment_provider": "'manual_refund'", "status": "x.status", "create_time": "x.created_at", "complete_time": "x.completed_at", "amount": "x.request_quota", "face_usd": "x.request_usd", "credit_quota": "x.request_quota", "paid_amount": "x.payout_amount", "paid_currency": "x.payout_currency",
		}},
		{from: "adty_topup_orders x", where: "x.refunded_at > 0 AND NOT EXISTS (SELECT 1 FROM adty_manual_refunds r WHERE r.trade_no = x.trade_no)", columns: map[string]string{
			"id": "x.id", "user_id": "x.user_id", "site_id": "x.site_id", "order_type": "'refund'", "source_type": "'legacy_refund'", "reference": "x.trade_no", "trade_no": "x.trade_no", "original_trade_no": "x.trade_no",
			"payment_method": "'manual'", "payment_provider": "'manual_refund'", "status": "'completed'", "create_time": "x.refunded_at", "complete_time": "x.refunded_at", "amount": "x.refund_quota", "face_usd": "x.refund_quota / 500000.0", "credit_quota": "x.refund_quota", "paid_amount": "x.refund_amount", "paid_currency": "COALESCE(NULLIF(x.paid_currency, ''), x.pay_currency)",
		}},
		{from: "adty_site_withdrawals x", columns: map[string]string{
			"id": "x.id", "user_id": "x.owner_user_id", "site_id": "x.site_id", "order_type": "'withdrawal'", "source_type": "'site_withdrawal'", "reference": "CAST(x.id AS TEXT)", "trade_no": "'WD-SITE-' || CAST(x.id AS TEXT)",
			"payment_method": "x.method", "payment_provider": "'withdrawal'", "status": "x.status", "create_time": "x.created_at", "complete_time": "x.paid_at", "face_usd": "x.amount_usd", "paid_amount": "CASE WHEN x.method = 'usdt' THEN x.net_usd ELSE 0 END", "paid_currency": "CASE WHEN x.method = 'usdt' THEN 'USDT' ELSE '' END",
		}},
		{from: "adty_site_invite_withdrawals x", columns: map[string]string{
			"id": "x.id", "user_id": "x.user_id", "site_id": "x.site_id", "order_type": "'withdrawal'", "source_type": "'site_invite_withdrawal'", "reference": "CAST(x.id AS TEXT)", "trade_no": "'WD-INV-' || CAST(x.id AS TEXT)",
			"payment_method": "x.method", "payment_provider": "'withdrawal'", "status": "x.status", "create_time": "x.created_at", "complete_time": "x.paid_at", "face_usd": "x.amount_usd", "paid_amount": "CAST(COALESCE(NULLIF(x.payout_amount, ''), '0') AS NUMERIC)", "paid_currency": "x.payout_currency",
		}},
		{from: "referral_withdrawals x", columns: map[string]string{
			"id": "x.id", "user_id": "x.user_id", "site_id": "0", "order_type": "'withdrawal'", "source_type": "'main_withdrawal'", "reference": "CAST(x.id AS TEXT)", "trade_no": "'WD-MAIN-' || CAST(x.id AS TEXT)",
			"payment_method": "x.account_type", "payment_provider": "'withdrawal'", "status": "x.status", "create_time": "x.submitted_at", "complete_time": "x.paid_at", "face_usd": "x.amount", "paid_amount": "CASE WHEN x.account_type = 'usdt' THEN x.net_amount ELSE 0 END", "paid_currency": "CASE WHEN x.account_type = 'usdt' THEN 'USDT' ELSE '' END",
		}},
		{from: "adty_site_invite_commissions x", columns: map[string]string{
			"id": "x.id", "user_id": "x.inviter_id", "site_id": "x.site_id", "order_type": "'commission'", "source_type": "'site_commission'", "reference": "CAST(x.id AS TEXT)", "trade_no": "'COM-SITE-' || CAST(x.id AS TEXT)", "original_trade_no": "x.trade_no",
			"payment_method": "'site_invite'", "payment_provider": "'commission'", "status": "x.status", "create_time": "x.created_at", "complete_time": "x.available_at", "face_usd": "x.amount_usd", "paid_amount": "x.covered_usd", "paid_currency": "'USD'",
		}},
		{from: "referral_commissions x", columns: map[string]string{
			"id": "x.id", "user_id": "x.affiliate_user_id", "site_id": "0", "order_type": "'commission'", "source_type": "'main_commission'", "reference": "CAST(x.id AS TEXT)", "trade_no": "'COM-MAIN-' || CAST(x.id AS TEXT)", "original_trade_no": "x.source_trade_no",
			"payment_method": "x.source_type", "payment_provider": "'commission'", "status": "x.status", "create_time": "x.created_at", "complete_time": "x.available_at", "face_usd": "x.commission_amount", "paid_amount": "x.commission_amount", "paid_currency": "x.settlement_currency",
		}},
		{from: "adty_staff_balance_requests x", columns: map[string]string{
			"id": "x.id", "user_id": "x.customer_user_id", "site_id": "x.site_id", "order_type": "'staff_adjustment'", "source_type": "'staff_adjustment'", "reference": "CAST(x.id AS TEXT)", "trade_no": "'STAFF-ADJ-' || CAST(x.id AS TEXT)",
			"payment_method": "x.direction", "payment_provider": "'staff_balance'", "status": "x.status", "create_time": "x.created_at", "complete_time": "x.reviewed_at", "face_usd": "x.amount_usd", "paid_currency": "'USD'",
		}},
		{from: "adty_balance_ledgers x", where: "x.type NOT IN ('topup', 'refund') AND COALESCE(x.ref_type, '') NOT IN ('staff_balance', 'staff_balance_request')", columns: map[string]string{
			"id": "x.id", "user_id": "x.user_id", "site_id": "NULL", "order_type": "'other'", "source_type": "'balance_ledger'", "reference": "CAST(x.id AS TEXT)", "trade_no": "'LEDGER-' || CAST(x.id AS TEXT)",
			"payment_method": "x.type", "payment_provider": "'wallet'", "status": "'completed'", "create_time": "x.created_at", "complete_time": "x.created_at", "amount": "x.delta_quota", "face_usd": "x.delta_quota / 500000.0", "credit_quota": "x.delta_quota", "paid_currency": "'USD'",
		}},
	}
	var parts []string
	for _, source := range sources {
		projection := make([]string, 0, len(columns))
		for _, column := range columns {
			expression, exists := source.columns[column]
			if !exists {
				switch column {
				case "order_type", "source_type", "reference", "original_trade_no", "trade_no", "payment_method", "payment_provider", "status", "paid_currency":
					expression = "''"
				default:
					expression = "0"
				}
			}
			projection = append(projection, expression+" AS "+column)
		}
		part := "SELECT " + strings.Join(projection, ", ") + " FROM " + source.from
		if source.where != "" {
			part += " WHERE " + source.where
		}
		parts = append(parts, part)
	}
	query := model.DB.Table("(" + strings.Join(parts, " UNION ALL ") + ") AS o").
		Joins("LEFT JOIN users u ON u.id = o.user_id").
		Joins("LEFT JOIN top_ups t ON o.source_type = 'topup' AND t.trade_no = o.reference").
		Joins("LEFT JOIN adty_topup_orders a ON o.source_type = 'topup' AND a.trade_no = o.reference")
	if ownerSiteID != nil {
		ownWithdrawals := model.DB.Model(&model.AdtySiteWithdrawal{}).Select("id").Scopes(model.AdtyWithdrawalOwnerScope(*ownerSiteID, c.GetInt("id")))
		query = query.Where("o.site_id = ?", *ownerSiteID).
			Where("(o.source_type <> 'site_withdrawal' OR o.id IN (?))", ownWithdrawals).
			Where("(o.source_type <> 'topup' OR COALESCE(a.business,'') NOT IN ? OR o.user_id = ?)", []string{model.AdtyBusinessOwnerSelfUseTopup, model.AdtyBusinessOwnerMarginTopup}, c.GetInt("id"))
	}
	if selfUserID != nil {
		query = query.Where("o.user_id = ?", *selfUserID)
	}
	if kind != "" {
		query = query.Where("o.order_type = ?", kind)
	}
	if sourceType := c.Query("source_type"); sourceType != "" {
		query = query.Where("o.source_type = ?", sourceType)
	}
	for parameter, column := range map[string]string{"site_id": "o.site_id", "user_id": "o.user_id", "channel_id": "o.channel_id", "team_id": "o.team_id"} {
		if raw := strings.TrimSpace(c.Query(parameter)); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value < 0 {
				common.ApiErrorT(c, "Invalid parameters")
				return nil, false
			}
			query = query.Where(column+" = ?", value)
		}
	}
	for parameter, column := range map[string]string{"status": "o.status", "provider": "o.payment_provider", "method": "o.payment_method"} {
		if value := strings.TrimSpace(c.Query(parameter)); value != "" && value != "all" {
			query = query.Where(column+" = ?", value)
		}
	}
	for parameter, comparison := range map[string]string{"start_timestamp": " >= ?", "end_timestamp": " <= ?"} {
		if raw := c.Query(parameter); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value < 0 {
				common.ApiErrorT(c, "Invalid parameters")
				return nil, false
			}
			if value > 0 {
				query = query.Where("o.create_time"+comparison, value)
			}
		}
	}
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		like := "%" + keyword + "%"
		where := "o.trade_no LIKE ? OR COALESCE(u.username, '') LIKE ? OR o.original_trade_no LIKE ?"
		args := []any{like, like, like}
		if ownerSiteID == nil && selfUserID == nil {
			where += " OR a.paid_tx_hash LIKE ? OR a.gateway_trade_id LIKE ?"
			args = append(args, like, like)
		}
		query = query.Where("("+where+")", args...)
	}
	return query, true
}

const adtyUnifiedOrderSelect = `o.*, COALESCE(u.username, '') AS username,
	COALESCE(a.business, 'wallet_topup') AS business,
	COALESCE(a.payment_scene, '') AS payment_scene, COALESCE(a.payment_scene_name, '') AS payment_scene_name,
	COALESCE(a.payment_scene_version, 0) AS payment_scene_version, (a.id IS NOT NULL) AS has_ext,
	COALESCE(a.bonus_usd, 0) AS bonus_usd, COALESCE(a.pay_amount, 0) AS pay_amount, COALESCE(a.pay_currency, '') AS pay_currency,
	COALESCE(t.money, o.face_usd) AS money, COALESCE(a.paid_tx_hash, '') AS paid_tx_hash,
	COALESCE(a.paid_source, '') AS paid_source, COALESCE(a.method_label, '') AS method_label`

func getAdtyUnifiedOrders(c *gin.Context, ownerSiteID *int, selfUserID *int) {
	query, ok := adtyUnifiedOrdersQuery(c, ownerSiteID, selfUserID)
	if !ok {
		return
	}
	page := common.GetPageQuery(c)
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	orders := []rechargeAuditOrder{}
	if err := query.Select(adtyUnifiedOrderSelect).Order("o.create_time DESC, o.source_type DESC, o.reference DESC").
		Limit(page.GetPageSize()).Offset(page.GetStartIdx()).Scan(&orders).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	var ids []int
	for _, order := range orders {
		if order.SiteId != nil {
			ids = append(ids, *order.SiteId)
		}
	}
	names, err := service.AdtySiteNames(ids)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	for i := range orders {
		order := &orders[i]
		if order.SiteId != nil {
			order.SiteName = names[*order.SiteId]
		}
		if order.SourceType == "topup" {
			order.Anomaly = rechargeAuditAnomaly(*order)
			order.ChannelName = adtyEpayChannelNames()[order.ChannelId]
			if selfUserID == nil && order.HasExt {
				for _, capability := range service.AdtyPaymentCapabilities() {
					if capability.Provider != order.PaymentProvider {
						continue
					}
					switch order.Status {
					case common.TopUpStatusSuccess:
						order.Actions.Refund = capability.Refund && order.Business != model.AdtyBusinessOwnerMarginTopup && (ownerSiteID == nil || order.Business != model.AdtyBusinessOwnerSelfUseTopup)
					case common.TopUpStatusPending, common.TopUpStatusExpired, common.TopUpStatusFailed, model.TopUpStatusCanceled:
						if ownerSiteID == nil {
							order.Actions.Complete, order.Actions.Query = capability.Complete, capability.Query
						}
					}
				}
			}
		}
		if (ownerSiteID != nil || selfUserID != nil) && order.PaidSource == model.AdtyPaymentSourceManual {
			order.PaidTxHash = ""
		}
	}
	page.SetTotal(int(total))
	page.SetItems(orders)
	common.ApiSuccess(c, page)
}

func GetAdtySelfOrders(c *gin.Context) {
	userID := c.GetInt("id")
	getAdtyUnifiedOrders(c, nil, &userID)
}

// 充值对账（任务 3.5）：从旧项目改写，只看 top_ups LEFT JOIN adty_topup_orders，金额一律美元，
// 去掉了订阅订单的 UNION、人民币折算和 Creem/Stripe 的特判。

type rechargeAuditOrder struct {
	Business            string               `json:"business"`
	Id                  int                  `json:"id"`
	UserId              int                  `json:"user_id"`
	Username            string               `json:"username"`
	SiteId              *int                 `json:"site_id"`
	SiteName            string               `json:"site_name" gorm:"-"`
	OrderType           string               `json:"order_type"`
	SourceType          string               `json:"source_type"`
	Reference           string               `json:"reference"`
	OriginalTradeNo     string               `json:"original_trade_no"`
	TeamId              int64                `json:"team_id"`
	TradeNo             string               `json:"trade_no"`
	PaymentMethod       string               `json:"payment_method"`
	PaymentProvider     string               `json:"payment_provider"`
	PaymentScene        string               `json:"payment_scene"`
	PaymentSceneName    string               `json:"payment_scene_name"`
	PaymentSceneVersion int64                `json:"payment_scene_version"`
	Status              string               `json:"status"`
	CreateTime          int64                `json:"create_time"`
	CompleteTime        int64                `json:"complete_time"`
	Amount              int64                `json:"amount"`
	Money               float64              `json:"money"`
	HasExt              bool                 `json:"has_ext"`
	FaceUSD             decimal.Decimal      `json:"face_usd"`
	BonusUSD            decimal.Decimal      `json:"bonus_usd"`
	CreditQuota         int64                `json:"credit_quota"`
	PayAmount           decimal.Decimal      `json:"pay_amount"`
	PayCurrency         string               `json:"pay_currency"`
	PaidAmount          decimal.Decimal      `json:"paid_amount"`
	PaidCurrency        string               `json:"paid_currency"`
	PaidTxHash          string               `json:"paid_tx_hash"`
	PaidSource          string               `json:"paid_source"`
	ChannelId           int                  `json:"channel_id"`
	MethodLabel         string               `json:"method_label"`
	ChannelName         string               `json:"channel_name"`
	Anomaly             string               `json:"anomaly"`
	Actions             rechargeAuditActions `json:"actions" gorm:"-"`
}

type rechargeAuditActions struct {
	Complete bool `json:"complete"`
	Refund   bool `json:"refund"`
	Query    bool `json:"query"`
}

var rechargeAuditStatuses = []string{
	common.TopUpStatusPending, common.TopUpStatusSuccess, common.TopUpStatusExpired,
	common.TopUpStatusFailed, model.TopUpStatusCanceled, model.TopUpStatusRefunded,
}

// rechargeAuditQuery 建对账查询，筛选条件都来自查询参数。
func rechargeAuditQuery(c *gin.Context, ownerSiteID *int) (*gorm.DB, bool) {
	orderType, validType := normalizeUnifiedOrderType(c.Query("order_type"))
	if !validType {
		common.ApiErrorT(c, "Invalid parameters")
		return nil, false
	}
	query := model.DB.Table("top_ups AS t").
		Joins("LEFT JOIN adty_topup_orders a ON a.trade_no = t.trade_no").
		Joins("LEFT JOIN users u ON u.id = t.user_id")
	if ownerSiteID != nil {
		query = query.Where("a.site_id = ?", *ownerSiteID)
	}
	if raw := strings.TrimSpace(c.Query("site_id")); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id < 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return nil, false
		}
		query = query.Where("a.site_id = ?", id)
	}
	if orderType == OrderTypeTopup {
		query = query.Where("COALESCE(t.payment_provider, '') <> ?", model.AdtyVoucherProvider)
	} else if orderType != "" {
		if orderType == OrderTypeVoucher {
			query = query.Where("t.payment_provider = ?", model.AdtyVoucherProvider)
		} else {
			query = query.Where("1 = 0")
		}
	}
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		like := "%" + keyword + "%"
		// 只筛「这个视图真的会返回」的列：站长视图里 manual 来源的 paid_tx_hash 会被清空
		// （见 getRechargeAudit），gateway_trade_id 从不返回。这两列若能筛，空/非空就是一个
		// 逐字符的盲注 oracle，绕过同一文件里已经写下的遮蔽。
		conds := "t.trade_no LIKE ? OR u.username LIKE ?"
		args := []any{like, like}
		if ownerSiteID == nil {
			conds += " OR a.paid_tx_hash LIKE ? OR a.gateway_trade_id LIKE ?"
			args = append(args, like, like)
		}
		query = query.Where("("+conds+")", args...)
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" && status != "all" {
		query = query.Where("t.status = ?", status)
	}
	if provider := strings.TrimSpace(c.Query("provider")); provider != "" {
		query = query.Where("t.payment_provider = ?", provider)
	}
	if method := strings.TrimSpace(c.Query("method")); method != "" {
		query = query.Where("t.payment_method = ?", method)
	}
	if raw := strings.TrimSpace(c.Query("channel_id")); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return nil, false
		}
		query = query.Where("a.channel_id = ?", id)
	}
	if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return nil, false
		}
		query = query.Where("t.user_id = ?", id)
	}
	if start, err := strconv.ParseInt(c.Query("start_timestamp"), 10, 64); err == nil && start > 0 {
		query = query.Where("t.create_time >= ?", start)
	}
	if end, err := strconv.ParseInt(c.Query("end_timestamp"), 10, 64); err == nil && end > 0 {
		query = query.Where("t.create_time <= ?", end)
	}
	return query, true
}

const rechargeAuditSelect = `t.id, t.user_id, COALESCE(u.username, '') AS username, a.site_id, t.trade_no, t.payment_method, t.payment_provider,
	COALESCE(a.business, 'wallet_topup') AS business,
	COALESCE(a.payment_scene, '') AS payment_scene, COALESCE(a.payment_scene_name, '') AS payment_scene_name, COALESCE(a.payment_scene_version, 0) AS payment_scene_version,
	t.status, t.create_time, t.complete_time, t.amount, t.money,
	(a.id IS NOT NULL) AS has_ext,
	COALESCE(a.face_usd, 0) AS face_usd, COALESCE(a.bonus_usd, 0) AS bonus_usd, COALESCE(a.credit_quota, 0) AS credit_quota,
	COALESCE(a.pay_amount, 0) AS pay_amount, COALESCE(a.pay_currency, '') AS pay_currency,
	COALESCE(a.paid_amount, 0) AS paid_amount, COALESCE(a.paid_currency, '') AS paid_currency,
	COALESCE(a.paid_tx_hash, '') AS paid_tx_hash, COALESCE(a.paid_source, '') AS paid_source,
	COALESCE(a.channel_id, 0) AS channel_id, COALESCE(a.method_label, '') AS method_label`

// GetRechargeAudit 分页列出统一财务订单（含充值、API消费、兑换码、退款、提现、佣金及员工调额）。
func GetRechargeAudit(c *gin.Context) {
	getRechargeAudit(c, nil)
}

// GetAdtyOwnerOrders uses the order's frozen site, even after a customer moves.
func GetAdtyOwnerOrders(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if ok {
		getRechargeAudit(c, &site.Id)
	}
}

func enrichTopupAuditOrders(orders []rechargeAuditOrder, ownerSiteID *int, siteNames map[int]string) {
	channelNames := adtyEpayChannelNames()
	capabilities := make(map[string]service.AdtyPaymentCapability)
	for _, capability := range service.AdtyPaymentCapabilities() {
		capabilities[capability.Provider] = capability
	}
	for i := range orders {
		if ownerSiteID != nil && orders[i].PaidSource == model.AdtyPaymentSourceManual {
			orders[i].PaidTxHash = ""
		}
		if orders[i].OrderType == "" {
			if orders[i].PaymentProvider == model.AdtyVoucherProvider {
				orders[i].OrderType = OrderTypeVoucher
			} else {
				orders[i].OrderType = OrderTypeTopup
			}
		}
		if orders[i].SiteId != nil {
			orders[i].SiteName = siteNames[*orders[i].SiteId]
		}
		orders[i].Anomaly = rechargeAuditAnomaly(orders[i])
		orders[i].ChannelName = channelNames[orders[i].ChannelId]
		capability, supported := capabilities[orders[i].PaymentProvider]
		if !supported || !orders[i].HasExt {
			continue
		}
		if ownerSiteID != nil {
			// ISS-R1-0081: An authorized site owner can initiate/manage manual
			// refunds on original orders belonging to their site.
			if orders[i].Business != model.AdtyBusinessOwnerSelfUseTopup && orders[i].Business != model.AdtyBusinessOwnerMarginTopup && orders[i].SiteId != nil && *orders[i].SiteId == *ownerSiteID && orders[i].Status == common.TopUpStatusSuccess {
				orders[i].Actions.Refund = capability.Refund
			}
			continue
		}
		switch orders[i].Status {
		case common.TopUpStatusPending, common.TopUpStatusExpired, common.TopUpStatusFailed, model.TopUpStatusCanceled:
			orders[i].Actions.Complete = capability.Complete
			orders[i].Actions.Query = capability.Query
		case common.TopUpStatusSuccess:
			orders[i].Actions.Refund = capability.Refund && orders[i].Business != model.AdtyBusinessOwnerMarginTopup
		}
	}
}

func getRechargeAudit(c *gin.Context, ownerSiteID *int) {
	getAdtyUnifiedOrders(c, ownerSiteID, nil)
}

func collectNonTopupUnifiedOrders(c *gin.Context, ownerSiteID *int, orderType string) ([]rechargeAuditOrder, error) {
	var siteFilter *int
	if ownerSiteID != nil {
		siteFilter = ownerSiteID
	} else if raw := strings.TrimSpace(c.Query("site_id")); raw != "" {
		id, err := strconv.Atoi(raw)
		if err == nil && id >= 0 {
			siteFilter = &id
		}
	}
	var userFilter *int
	if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
		if id, err := strconv.Atoi(raw); err == nil {
			userFilter = &id
		}
	}
	statusFilter := strings.TrimSpace(c.Query("status"))
	if statusFilter == "all" {
		statusFilter = ""
	}
	providerFilter := strings.TrimSpace(c.Query("provider"))
	methodFilter := strings.TrimSpace(c.Query("method"))
	keywordFilter := strings.ToLower(strings.TrimSpace(c.Query("keyword")))
	startTS, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTS, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)

	want := func(cat string) bool {
		return orderType == "" || orderType == cat
	}
	matchesCommon := func(item rechargeAuditOrder) bool {
		if siteFilter != nil {
			if item.SiteId == nil || *item.SiteId != *siteFilter {
				return false
			}
		}
		if userFilter != nil && item.UserId != *userFilter {
			return false
		}
		if statusFilter != "" && !strings.EqualFold(item.Status, statusFilter) {
			return false
		}
		if providerFilter != "" && !strings.EqualFold(item.PaymentProvider, providerFilter) {
			return false
		}
		if methodFilter != "" && !strings.EqualFold(item.PaymentMethod, methodFilter) {
			return false
		}
		if startTS > 0 && item.CreateTime < startTS {
			return false
		}
		if endTS > 0 && item.CreateTime > endTS {
			return false
		}
		if keywordFilter != "" {
			if !strings.Contains(strings.ToLower(item.TradeNo), keywordFilter) &&
				!strings.Contains(strings.ToLower(item.Username), keywordFilter) {
				return false
			}
		}
		return true
	}

	userMap := map[int]string{}
	loadUsernames := func(ids []int) {
		missing := make([]int, 0, len(ids))
		for _, id := range ids {
			if id > 0 {
				if _, ok := userMap[id]; !ok {
					missing = append(missing, id)
				}
			}
		}
		if len(missing) == 0 || !model.DB.Migrator().HasTable(&model.User{}) {
			return
		}
		var rows []struct {
			Id       int    `gorm:"column:id"`
			Username string `gorm:"column:username"`
		}
		if err := model.DB.Model(&model.User{}).Select("id, username").Where("id IN ?", missing).Find(&rows).Error; err == nil {
			for _, r := range rows {
				userMap[r.Id] = r.Username
			}
		}
	}

	var out []rechargeAuditOrder
	perUnit := common.QuotaPerUnitDecimal()

	// 1. API Consumption (BillingOperation)
	if want(OrderTypeConsumption) && model.DB.Migrator().HasTable(&model.BillingOperation{}) {
		q := model.DB.Model(&model.BillingOperation{}).Where("state = ? AND applied_quota > 0", model.BillingSettled)
		if siteFilter != nil {
			q = q.Where("site_id = ?", *siteFilter)
		}
		if userFilter != nil {
			q = q.Where("user_id = ?", *userFilter)
		}
		var ops []model.BillingOperation
		if err := q.Order("created_at DESC, operation_key DESC").Limit(500).Find(&ops).Error; err != nil {
			return nil, err
		}
		uIDs := make([]int, 0, len(ops))
		for _, op := range ops {
			uIDs = append(uIDs, op.UserId)
		}
		loadUsernames(uIDs)
		for _, op := range ops {
			sid := op.SiteId
			appliedQuota := int64(op.AppliedQuota)
			usd := decimal.NewFromInt(appliedQuota).Div(perUnit).Round(6)
			f, _ := usd.Float64()
			item := rechargeAuditOrder{
				Id:              int(op.ReferenceId),
				UserId:          op.UserId,
				Username:        userMap[op.UserId],
				SiteId:          &sid,
				OrderType:       OrderTypeConsumption,
				TradeNo:         op.OperationKey,
				PaymentMethod:   "api",
				PaymentProvider: "wallet",
				Status:          common.TopUpStatusSuccess,
				CreateTime:      op.CreatedAt,
				CompleteTime:    op.UpdatedAt,
				Amount:          appliedQuota,
				Money:           f,
				FaceUSD:         usd,
				CreditQuota:     appliedQuota,
				PaidAmount:      usd,
				PaidCurrency:    "USD",
			}
			if statusFilter != "" && strings.EqualFold(statusFilter, op.State) {
				item.Status = op.State
			}
			if matchesCommon(item) {
				out = append(out, item)
			}
		}
	}

	// 2. Voucher (AdtyVoucher)
	if want(OrderTypeVoucher) && model.DB.Migrator().HasTable(&model.AdtyVoucher{}) {
		q := model.DB.Model(&model.AdtyVoucher{})
		if siteFilter != nil {
			q = q.Where("(source_site_id = ? OR target_site_id = ?)", *siteFilter, *siteFilter)
		}
		var vouchers []model.AdtyVoucher
		if err := q.Order("id DESC").Limit(500).Find(&vouchers).Error; err != nil {
			return nil, err
		}
		uIDs := make([]int, 0, len(vouchers)*2)
		for _, v := range vouchers {
			uIDs = append(uIDs, v.RedeemerId, v.IssuerId)
		}
		loadUsernames(uIDs)
		for _, v := range vouchers {
			uid := v.RedeemerId
			if uid <= 0 {
				uid = v.IssuerId
			}
			sid := v.SourceSiteId
			if v.TargetSiteId > 0 && sid == 0 {
				sid = v.TargetSiteId
			}
			tradeNo := strings.TrimSpace(v.TradeNo)
			if tradeNo == "" {
				tradeNo = fmt.Sprintf("VOUCHER-%d", v.Id)
			}
			f, _ := v.AmountUSD.Float64()
			item := rechargeAuditOrder{
				Id:              int(v.Id),
				UserId:          uid,
				Username:        userMap[uid],
				SiteId:          &sid,
				OrderType:       OrderTypeVoucher,
				TradeNo:         tradeNo,
				PaymentMethod:   v.Purpose,
				PaymentProvider: model.AdtyVoucherProvider,
				Status:          v.Status,
				CreateTime:      v.CreatedAt,
				CompleteTime:    v.RedeemedAt,
				Amount:          v.Quota,
				Money:           f,
				FaceUSD:         v.AmountUSD,
				CreditQuota:     v.Quota,
				PaidAmount:      v.AmountUSD,
				PaidCurrency:    "USD",
			}
			if matchesCommon(item) {
				out = append(out, item)
			}
		}
	}

	// 3. Manual Refund (AdtyManualRefund)
	if want(OrderTypeRefund) && model.DB.Migrator().HasTable(&model.AdtyManualRefund{}) {
		q := model.DB.Model(&model.AdtyManualRefund{})
		if siteFilter != nil {
			q = q.Where("site_id = ?", *siteFilter)
		}
		if userFilter != nil {
			q = q.Where("user_id = ?", *userFilter)
		}
		var refunds []model.AdtyManualRefund
		if err := q.Order("id DESC").Limit(500).Find(&refunds).Error; err != nil {
			return nil, err
		}
		uIDs := make([]int, 0, len(refunds))
		for _, r := range refunds {
			uIDs = append(uIDs, r.UserId)
		}
		loadUsernames(uIDs)
		for _, r := range refunds {
			sid := r.SiteId
			f, _ := r.RequestUSD.Float64()
			item := rechargeAuditOrder{
				Id:              int(r.Id),
				UserId:          r.UserId,
				Username:        userMap[r.UserId],
				SiteId:          &sid,
				OrderType:       OrderTypeRefund,
				TradeNo:         r.RefundId,
				PaymentMethod:   r.TradeNo,
				PaymentProvider: "manual_refund",
				Status:          r.Status,
				CreateTime:      r.CreatedAt,
				CompleteTime:    r.CompletedAt,
				Amount:          r.RequestQuota,
				Money:           f,
				FaceUSD:         r.RequestUSD,
				CreditQuota:     r.RequestQuota,
				PaidAmount:      r.PayoutAmount,
				PaidCurrency:    r.PayoutCurrency,
			}
			if matchesCommon(item) {
				out = append(out, item)
			}
		}
	}

	// 4. Withdrawal (AdtySiteWithdrawal & AdtySiteInviteWithdrawal)
	if want(OrderTypeWithdrawal) {
		if model.DB.Migrator().HasTable(&model.AdtySiteWithdrawal{}) {
			q := model.DB.Model(&model.AdtySiteWithdrawal{})
			if siteFilter != nil {
				q = q.Where("site_id = ?", *siteFilter)
			}
			if userFilter != nil {
				q = q.Where("owner_user_id = ?", *userFilter)
			}
			var withdrawals []model.AdtySiteWithdrawal
			if err := q.Order("id DESC").Limit(500).Find(&withdrawals).Error; err != nil {
				return nil, err
			}
			uIDs := make([]int, 0, len(withdrawals))
			for _, w := range withdrawals {
				uIDs = append(uIDs, w.OwnerUserId)
			}
			loadUsernames(uIDs)
			for _, w := range withdrawals {
				sid := w.SiteId
				f, _ := w.AmountUSD.Float64()
				item := rechargeAuditOrder{
					Id:              int(w.Id),
					UserId:          w.OwnerUserId,
					Username:        userMap[w.OwnerUserId],
					SiteId:          &sid,
					OrderType:       OrderTypeWithdrawal,
					TradeNo:         fmt.Sprintf("WD-SITE-%d", w.Id),
					PaymentMethod:   w.Method,
					PaymentProvider: "withdrawal",
					Status:          w.Status,
					CreateTime:      w.CreatedAt,
					CompleteTime:    w.ReviewedAt,
					Money:           f,
					FaceUSD:         w.AmountUSD,
					PaidAmount:      w.NetUSD,
					PaidCurrency:    "USDT",
				}
				if matchesCommon(item) {
					out = append(out, item)
				}
			}
		}
		if model.DB.Migrator().HasTable(&model.AdtySiteInviteWithdrawal{}) {
			q := model.DB.Model(&model.AdtySiteInviteWithdrawal{})
			if siteFilter != nil {
				q = q.Where("site_id = ?", *siteFilter)
			}
			if userFilter != nil {
				q = q.Where("user_id = ?", *userFilter)
			}
			var invWithdrawals []model.AdtySiteInviteWithdrawal
			if err := q.Order("id DESC").Limit(500).Find(&invWithdrawals).Error; err != nil {
				return nil, err
			}
			uIDs := make([]int, 0, len(invWithdrawals))
			for _, w := range invWithdrawals {
				uIDs = append(uIDs, w.UserId)
			}
			loadUsernames(uIDs)
			for _, w := range invWithdrawals {
				sid := w.SiteId
				f, _ := w.AmountUSD.Float64()
				paidAmount, _ := decimal.NewFromString(w.PayoutAmount)
				item := rechargeAuditOrder{
					Id:              int(w.Id),
					UserId:          w.UserId,
					Username:        userMap[w.UserId],
					SiteId:          &sid,
					OrderType:       OrderTypeWithdrawal,
					TradeNo:         fmt.Sprintf("WD-INV-%d", w.Id),
					PaymentMethod:   w.Method,
					PaymentProvider: "withdrawal",
					Status:          w.Status,
					CreateTime:      w.CreatedAt,
					CompleteTime:    w.PaidAt,
					Money:           f,
					FaceUSD:         w.AmountUSD,
					PaidAmount:      paidAmount,
					PaidCurrency:    w.PayoutCurrency,
				}
				if matchesCommon(item) {
					out = append(out, item)
				}
			}
		}
	}

	// 5. Commission (AdtySiteInviteCommission & ReferralCommission for main-site admin)
	if want(OrderTypeCommission) {
		if model.DB.Migrator().HasTable(&model.AdtySiteInviteCommission{}) {
			q := model.DB.Model(&model.AdtySiteInviteCommission{})
			if siteFilter != nil {
				q = q.Where("site_id = ?", *siteFilter)
			}
			if userFilter != nil {
				q = q.Where("inviter_id = ?", *userFilter)
			}
			var comms []model.AdtySiteInviteCommission
			if err := q.Order("id DESC").Limit(500).Find(&comms).Error; err != nil {
				return nil, err
			}
			uIDs := make([]int, 0, len(comms))
			for _, cm := range comms {
				uIDs = append(uIDs, cm.InviterId)
			}
			loadUsernames(uIDs)
			for _, cm := range comms {
				sid := cm.SiteId
				f, _ := cm.AmountUSD.Float64()
				item := rechargeAuditOrder{
					Id:              int(cm.Id),
					UserId:          cm.InviterId,
					Username:        userMap[cm.InviterId],
					SiteId:          &sid,
					OrderType:       OrderTypeCommission,
					TradeNo:         fmt.Sprintf("COM-SITE-%d:%s", cm.Id, cm.TradeNo),
					PaymentMethod:   "site_invite",
					PaymentProvider: "commission",
					Status:          cm.Status,
					CreateTime:      cm.CreatedAt,
					CompleteTime:    cm.AvailableAt,
					Money:           f,
					FaceUSD:         cm.AmountUSD,
					PaidAmount:      cm.CoveredUSD,
					PaidCurrency:    "USD",
				}
				if matchesCommon(item) {
					out = append(out, item)
				}
			}
		}
		if ownerSiteID == nil && (siteFilter == nil || *siteFilter == 0) && model.DB.Migrator().HasTable(&model.ReferralCommission{}) {
			q := model.DB.Model(&model.ReferralCommission{})
			if userFilter != nil {
				q = q.Where("affiliate_user_id = ?", *userFilter)
			}
			var mainComms []model.ReferralCommission
			if err := q.Order("id DESC").Limit(500).Find(&mainComms).Error; err != nil {
				return nil, err
			}
			uIDs := make([]int, 0, len(mainComms))
			for _, cm := range mainComms {
				uIDs = append(uIDs, cm.AffiliateUserId)
			}
			loadUsernames(uIDs)
			for _, cm := range mainComms {
				mainSite := 0
				amt := decimal.NewFromFloat(cm.CommissionAmount).Round(6)
				item := rechargeAuditOrder{
					Id:              cm.Id,
					UserId:          cm.AffiliateUserId,
					Username:        userMap[cm.AffiliateUserId],
					SiteId:          &mainSite,
					OrderType:       OrderTypeCommission,
					TradeNo:         fmt.Sprintf("COM-MAIN-%d:%s", cm.Id, cm.SourceTradeNo),
					PaymentMethod:   cm.SourceType,
					PaymentProvider: "commission",
					Status:          cm.Status,
					CreateTime:      cm.CreatedAt,
					CompleteTime:    cm.AvailableAt,
					Money:           cm.CommissionAmount,
					FaceUSD:         amt,
					PaidAmount:      amt,
					PaidCurrency:    cm.SettlementCurrency,
				}
				if matchesCommon(item) {
					out = append(out, item)
				}
			}
		}
	}

	// 6. Staff Adjustment (AdtyStaffBalanceRequest)
	if want(OrderTypeStaffAdjustment) && model.DB.Migrator().HasTable(&model.AdtyStaffBalanceRequest{}) {
		q := model.DB.Model(&model.AdtyStaffBalanceRequest{})
		if siteFilter != nil {
			q = q.Where("site_id = ?", *siteFilter)
		}
		if userFilter != nil {
			q = q.Where("customer_user_id = ?", *userFilter)
		}
		var reqs []model.AdtyStaffBalanceRequest
		if err := q.Order("id DESC").Limit(500).Find(&reqs).Error; err != nil {
			return nil, err
		}
		uIDs := make([]int, 0, len(reqs))
		for _, r := range reqs {
			uIDs = append(uIDs, r.CustomerUserId)
		}
		loadUsernames(uIDs)
		for _, r := range reqs {
			sid := r.SiteId
			f, _ := r.AmountUSD.Float64()
			provider := strings.TrimSpace(r.FundingSource)
			if provider == "" {
				provider = "staff_balance"
			}
			tradeRef := fmt.Sprintf("STAFF-ADJ-%d", r.Id)
			if strings.TrimSpace(r.BusinessRef) != "" {
				tradeRef = fmt.Sprintf("STAFF-ADJ-%d:%s", r.Id, strings.TrimSpace(r.BusinessRef))
			}
			item := rechargeAuditOrder{
				Id:              int(r.Id),
				UserId:          r.CustomerUserId,
				Username:        userMap[r.CustomerUserId],
				SiteId:          &sid,
				OrderType:       OrderTypeStaffAdjustment,
				TradeNo:         tradeRef,
				PaymentMethod:   r.Direction,
				PaymentProvider: provider,
				Status:          r.Status,
				CreateTime:      r.CreatedAt,
				CompleteTime:    r.ReviewedAt,
				Money:           f,
				FaceUSD:         r.AmountUSD,
				PaidAmount:      r.AmountUSD,
				PaidCurrency:    "USD",
			}
			if matchesCommon(item) {
				out = append(out, item)
			}
		}
	}

	return out, nil
}

// adtyEpayChannelNames 由 P5 换成通道表的名字；没有通道时是空表。
var adtyEpayChannelNames = func() map[int]string { return map[int]string{} }

// rechargeAuditAnomaly 标出两类异常：成功订单缺扩展记录，实付和应付对不上。
func rechargeAuditAnomaly(order rechargeAuditOrder) string {
	ours := model.IsAdtyPaymentProvider(order.PaymentProvider)
	if !ours {
		return ""
	}
	if !order.HasExt {
		return "missing_ext"
	}
	if order.Status != common.TopUpStatusSuccess && order.Status != model.TopUpStatusRefunded {
		return ""
	}
	if order.PaidSource == model.AdtyPaymentSourceManual {
		return ""
	}
	if order.PaymentProvider == model.PaymentProviderAdtyEpay && !order.PaidAmount.Round(2).Equal(order.PayAmount.Round(2)) {
		return "paid_mismatch"
	}
	if order.PaymentProvider == model.PaymentProviderAdtyBEpusdt && order.PaidAmount.LessThan(order.PayAmount) {
		return "paid_mismatch"
	}
	return ""
}

type rechargeAuditSummaryRow struct {
	Status   string          `json:"status"`
	Provider string          `json:"provider"`
	Count    int64           `json:"count"`
	FaceUSD  decimal.Decimal `json:"face_usd"`
	BonusUSD decimal.Decimal `json:"bonus_usd"`
}

// GetRechargeAuditSummary 按状态和支付商汇总，金额都是美元。
func GetRechargeAuditSummary(c *gin.Context) {
	getRechargeAuditSummary(c, nil)
}

func GetAdtyOwnerOrderSummary(c *gin.Context) {
	if _, ok := adtyOwnerSiteOr403(c); !ok {
		return
	}
	common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Site owners cannot view recharge summaries"))
}

func getRechargeAuditSummary(c *gin.Context, ownerSiteID *int) {
	query, ok := rechargeAuditQuery(c, ownerSiteID)
	if !ok {
		return
	}
	var rows []rechargeAuditSummaryRow
	if err := query.Select(`t.status AS status, t.payment_provider AS provider, COUNT(*) AS count,
		COALESCE(SUM(COALESCE(a.face_usd, t.amount)), 0) AS face_usd, COALESCE(SUM(COALESCE(a.bonus_usd, 0)), 0) AS bonus_usd`).
		Group("t.status, t.payment_provider").Scan(&rows).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	credited := decimal.Zero
	bonus := decimal.Zero
	var orders int64
	for _, row := range rows {
		orders += row.Count
		if row.Status == common.TopUpStatusSuccess {
			credited = credited.Add(row.FaceUSD)
			bonus = bonus.Add(row.BonusUSD)
		}
	}
	capabilities := service.AdtyPaymentCapabilities()
	providers := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		providers = append(providers, capability.Provider)
	}
	// Keep choices independent of active filters and within the caller's scope.
	sitesQuery := model.DB.Table("top_ups AS t").Joins("JOIN adty_topup_orders a ON a.trade_no = t.trade_no")
	if ownerSiteID != nil {
		sitesQuery = sitesQuery.Where("a.site_id = ?", *ownerSiteID)
	}
	var siteIDs []int
	if err := sitesQuery.Distinct("a.site_id").Order("a.site_id").Pluck("a.site_id", &siteIDs).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	siteNames, err := service.AdtySiteNames(siteIDs)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	type siteOption struct {
		SiteId   int    `json:"site_id"`
		SiteName string `json:"site_name"`
	}
	siteOptions := make([]siteOption, 0, len(siteIDs))
	for _, id := range siteIDs {
		siteOptions = append(siteOptions, siteOption{SiteId: id, SiteName: siteNames[id]})
	}
	common.ApiSuccess(c, gin.H{
		"rows":                  rows,
		"orders":                orders,
		"credited_usd":          credited,
		"bonus_usd":             bonus,
		"currency":              "USD",
		"order_type_options":    unifiedOrderTypeOptions,
		"status_options":        rechargeAuditStatuses,
		"provider_option":       providers,
		"provider_capabilities": capabilities,
		"site_options":          siteOptions,
	})
}

// adtyOwnerOrderForRefundOrError verifies that the caller is the authenticated
// owner of an active site and that trade_no is an original order frozen under
// that exact site (A.8 / G.9 / ISS-R1-0081).
func adtyOwnerOrderForRefundOrError(c *gin.Context) (*model.AdtySite, string, *model.AdtyTopupOrder, bool) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return nil, "", nil, false
	}
	tradeNo := strings.TrimSpace(c.Param("trade_no"))
	if tradeNo == "" || len(tradeNo) > 255 {
		common.ApiErrorT(c, "Invalid parameters")
		return nil, "", nil, false
	}
	ext, err := model.GetAdtyTopupOrder(tradeNo)
	if err != nil {
		writeAdtyFinanceError(c, err)
		return nil, "", nil, false
	}
	if ext == nil || ext.SiteId != site.Id {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Order not found"))
		return nil, "", nil, false
	}
	if ext.IsOwnerBusiness() {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("This operation requires main-site finance permission"))
		return nil, "", nil, false
	}
	return site, tradeNo, ext, true
}

// PreviewAdtyOwnerOrderRefund lets an authorized site owner preview a partial
// or full manual refund for an order belonging to their site (ISS-R1-0081).
func PreviewAdtyOwnerOrderRefund(c *gin.Context) {
	_, tradeNo, _, ok := adtyOwnerOrderForRefundOrError(c)
	if !ok {
		return
	}
	result, err := model.PreviewAdtyTopUpRefund(tradeNo)
	if err != nil {
		writeAdtyFinanceError(c, err)
		return
	}
	if raw := strings.TrimSpace(c.Query("refund_usd")); raw != "" {
		amount, parseErr := decimal.NewFromString(raw)
		if parseErr != nil || !amount.IsPositive() {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		result.Facts, err = model.LoadAdtyRefundPageFacts(tradeNo, amount)
		if err != nil {
			writeAdtyFinanceError(c, err)
			return
		}
	}
	result.Ledger = nil
	adtyOwnerRefundFacts(result.Facts, c.GetInt("id"))
	common.ApiSuccess(c, result)
}

// Preserve original-order amounts and state, exposing private handling evidence
// only to the owner who recorded it. Global customer balances are never returned.
func adtyOwnerRefundRow(row *model.AdtyManualRefund, ownerID int) {
	if row == nil {
		return
	}
	if row.ApplyOperatorId != ownerID {
		row.Reason = ""
	}
	if row.ApproveOperatorId != ownerID {
		row.ApproveNote = ""
	}
	if row.TerminateOperatorId != ownerID {
		row.TerminateNote = ""
	}
	if row.PayoutOperatorId != ownerID {
		row.PayoutVoucher = ""
	}
	row.ApplyOperatorId, row.ApproveOperatorId, row.ReclaimOperatorId = 0, 0, 0
	row.BonusOperatorId, row.CommissionOperatorId, row.PayoutOperatorId = 0, 0, 0
	row.CompleteOperatorId, row.TerminateOperatorId = 0, 0
	row.ApplyIdemKey, row.ApproveIdemKey, row.ReclaimIdemKey = "", "", ""
	row.BonusIdemKey, row.CommissionIdemKey, row.PayoutIdemKey = "", "", ""
	row.CompleteIdemKey, row.TerminateIdemKey, row.CommissionSnapshot = "", "", ""
}

func adtyOwnerRefundFacts(facts *model.AdtyRefundPageFacts, ownerID int) {
	if facts == nil {
		return
	}
	facts.RefundProof = ""
	for i := range facts.ManualRefunds {
		adtyOwnerRefundRow(&facts.ManualRefunds[i], ownerID)
	}
}

func adtyOwnerRefundResult(result *model.AdtyManualRefundResult, ownerID int) {
	if result == nil {
		return
	}
	result.Ledger = nil
	adtyOwnerRefundRow(result.Refund, ownerID)
	adtyOwnerRefundFacts(result.Facts, ownerID)
}

// ListAdtyOwnerOrderManualRefunds lists manual refund records for an order
// belonging to the caller's site.
func ListAdtyOwnerOrderManualRefunds(c *gin.Context) {
	if _, tradeNo, _, ok := adtyOwnerOrderForRefundOrError(c); !ok {
		return
	} else {
		result, err := model.ListAdtyTopUpManualRefunds(tradeNo)
		if err != nil {
			writeAdtyFinanceError(c, err)
			return
		}
		adtyOwnerRefundResult(result, c.GetInt("id"))
		common.ApiSuccess(c, result)
	}
}

// GetAdtyOwnerOrderManualRefund returns one manual refund record for an order
// belonging to the caller's site.
func GetAdtyOwnerOrderManualRefund(c *gin.Context) {
	if _, tradeNo, _, ok := adtyOwnerOrderForRefundOrError(c); !ok {
		return
	} else {
		result, err := model.GetAdtyTopUpManualRefund(tradeNo, c.Param("refund_id"))
		if err != nil {
			writeAdtyFinanceError(c, err)
			return
		}
		adtyOwnerRefundResult(result, c.GetInt("id"))
		common.ApiSuccess(c, result)
	}
}

// RefundAdtyOwnerOrder lets an authorized site owner submit a manual partial
// or full refund for an original order belonging to their site under required
// server scope and single-use security proof (A.8:1184 / G.9 / ISS-R1-0081).
func RefundAdtyOwnerOrder(c *gin.Context) {
	site, tradeNo, _, ok := adtyOwnerOrderForRefundOrError(c)
	if !ok {
		return
	}
	var body struct {
		Reason         string `json:"reason"`
		IdempotencyKey string `json:"idempotency_key"`
		RefundQuota    *int64 `json:"refund_quota"`
		RefundUSD      string `json:"refund_usd"`
		Proof          string `json:"proof"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	refundUSD := strings.TrimSpace(body.RefundUSD)
	var refundQuota *int64
	if refundUSD != "" {
		if _, parseErr := decimal.NewFromString(refundUSD); parseErr != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	} else if body.RefundQuota != nil {
		refundQuota = body.RefundQuota
	} else {
		common.ApiErrorT(c, "Enter the refund amount in USD")
		return
	}
	primaryOp := service.AdtyOwnerOrderRefundOperation(tradeNo, body.Reason, refundUSD, refundQuota)
	if middleware.RequireSecurityProof(c, primaryOp) == nil {
		return
	}
	var amount decimal.Decimal
	var parseErr error
	if refundUSD != "" {
		amount, parseErr = decimal.NewFromString(refundUSD)
		if parseErr != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	} else {
		amount, parseErr = model.RefundQuotaToUSD(*refundQuota)
		if parseErr != nil {
			writeAdtyFinanceError(c, parseErr)
			return
		}
	}
	applied, applyErr := model.ApplyAdtyTopUpManualRefund(tradeNo, body.Reason, c.GetInt("id"), body.IdempotencyKey, amount, site.Id)
	if applyErr != nil {
		writeAdtyFinanceError(c, applyErr)
		return
	}
	recordManageAuditFor(c, applied.Refund.UserId, "adty.site.order.refund.apply", map[string]any{
		"site_id":       site.Id,
		"trade_no":      tradeNo,
		"refund_id":     applied.Refund.RefundId,
		"reason":        strings.TrimSpace(body.Reason),
		"request_usd":   applied.Refund.RequestUSD.String(),
		"request_quota": applied.Refund.RequestQuota,
	})
	adtyOwnerRefundResult(applied, c.GetInt("id"))
	common.ApiSuccess(c, applied)
}

// PostAdtyOwnerOrderRefund aliases RefundAdtyOwnerOrder for site-owner routing.
func PostAdtyOwnerOrderRefund(c *gin.Context) {
	RefundAdtyOwnerOrder(c)
}

func writeOwnerManualRefundStep(c *gin.Context, step string, run func(tradeNo, refundID string, operatorID int, idemKey string, ownerSiteID ...int) (*model.AdtyManualRefundResult, error)) {
	site, tradeNo, _, ok := adtyOwnerOrderForRefundOrError(c)
	if !ok {
		return
	}
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		Note           string `json:"note"`
		Status         string `json:"status"`
		Currency       string `json:"currency"`
		Amount         string `json:"amount"`
		PaidAt         int64  `json:"paid_at"`
		Voucher        string `json:"voucher"`
		Proof          string `json:"proof"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil && !errors.Is(err, io.EOF) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	refundID := strings.TrimSpace(c.Param("refund_id"))
	payout := model.AdtyManualRefundPayout{
		Status:   body.Status,
		Currency: body.Currency,
		PaidAt:   body.PaidAt,
		Voucher:  strings.TrimSpace(body.Voucher),
	}
	if payout.Voucher == "" {
		payout.Voucher = strings.TrimSpace(body.Proof)
	}
	if strings.TrimSpace(body.Amount) != "" {
		amount, err := decimal.NewFromString(strings.TrimSpace(body.Amount))
		if err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		payout.Amount = amount
	}
	amount := ""
	if strings.TrimSpace(body.Amount) != "" {
		amount = payout.Amount.String()
	}
	primaryOp := service.AdtyOwnerOrderRefundStepOperation(tradeNo, refundID, step, body.Note, service.AdtyManualRefundStepPayout{
		Status: payout.Status, Currency: payout.Currency, Amount: amount,
		Voucher: payout.Voucher, PaidAt: payout.PaidAt,
	})
	if middleware.RequireSecurityProof(c, primaryOp) == nil {
		return
	}
	var result *model.AdtyManualRefundResult
	var err error
	switch step {
	case model.AdtyManualRefundStepPayout:
		result, err = model.RecordAdtyTopUpManualRefundPayout(tradeNo, refundID, c.GetInt("id"), body.IdempotencyKey, payout, site.Id)
	case model.AdtyManualRefundStepApprove:
		result, err = model.ApproveAdtyTopUpManualRefund(tradeNo, refundID, c.GetInt("id"), body.IdempotencyKey, body.Note, site.Id)
	case model.AdtyManualRefundStepTerminate:
		result, err = model.TerminateAdtyTopUpManualRefund(tradeNo, refundID, c.GetInt("id"), body.IdempotencyKey, body.Note, site.Id)
	default:
		result, err = run(tradeNo, refundID, c.GetInt("id"), body.IdempotencyKey, site.Id)
	}
	if err != nil {
		writeAdtyFinanceError(c, err)
		return
	}
	audit := map[string]any{"site_id": site.Id, "trade_no": tradeNo, "refund_id": refundID, "step": step}
	if result.Refund != nil {
		audit["status"] = result.Refund.Status
		audit["shortfall_quota"] = result.Refund.ShortfallQuota
		audit["payout_status"] = result.Refund.PayoutStatus
	}
	recordManageAuditFor(c, result.Refund.UserId, "adty.site.order.refund."+step, audit)
	adtyOwnerRefundResult(result, c.GetInt("id"))
	common.ApiSuccess(c, result)
}

func ApproveAdtyOwnerOrderManualRefund(c *gin.Context) {
	writeOwnerManualRefundStep(c, model.AdtyManualRefundStepApprove, nil)
}

func ReclaimAdtyOwnerOrderManualRefund(c *gin.Context) {
	writeOwnerManualRefundStep(c, model.AdtyManualRefundStepReclaim, model.ReclaimAdtyTopUpManualRefund)
}

func PlanAdtyOwnerOrderManualRefundBonus(c *gin.Context) {
	writeOwnerManualRefundStep(c, model.AdtyManualRefundStepBonus, model.PlanAdtyTopUpManualRefundBonus)
}

func PlanAdtyOwnerOrderManualRefundCommission(c *gin.Context) {
	writeOwnerManualRefundStep(c, model.AdtyManualRefundStepCommission, model.PlanAdtyTopUpManualRefundCommission)
}

func RecordAdtyOwnerOrderManualRefundPayout(c *gin.Context) {
	writeOwnerManualRefundStep(c, model.AdtyManualRefundStepPayout, nil)
}

func CompleteAdtyOwnerOrderManualRefund(c *gin.Context) {
	writeOwnerManualRefundStep(c, model.AdtyManualRefundStepComplete, model.CompleteAdtyTopUpManualRefund)
}

func TerminateAdtyOwnerOrderManualRefund(c *gin.Context) {
	writeOwnerManualRefundStep(c, model.AdtyManualRefundStepTerminate, nil)
}
