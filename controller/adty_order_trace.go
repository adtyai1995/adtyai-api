package controller

import (
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Only explicitly linked records are exposed. A customer's other wallet
// operations and a site's aggregated daily earnings do not belong to an order.
type adtyOrderTraceEntry struct {
	Id             int64            `json:"id"`
	Kind           string           `json:"kind"`
	Source         string           `json:"source"`
	Status         string           `json:"status"`
	CreatedAt      int64            `json:"created_at"`
	Reference      string           `json:"reference"`
	Amount         *decimal.Decimal `json:"amount,omitempty"`
	Currency       string           `json:"currency,omitempty"`
	DeltaQuota     *int64           `json:"delta_quota,omitempty"`
	BalanceBefore  *decimal.Decimal `json:"balance_before,omitempty"`
	BalanceAfter   *decimal.Decimal `json:"balance_after,omitempty"`
	PendingDelta   *decimal.Decimal `json:"pending_delta,omitempty"`
	AvailableDelta *decimal.Decimal `json:"available_delta,omitempty"`
	FrozenDelta    *decimal.Decimal `json:"frozen_delta,omitempty"`
	WithdrawnDelta *decimal.Decimal `json:"withdrawn_delta,omitempty"`
	OperatorId     int              `json:"operator_id,omitempty"`
	Note           string           `json:"note,omitempty"`
	Proof          string           `json:"proof,omitempty"`
}

func GetRechargeAuditOrder(c *gin.Context) { getAdtyUnifiedOrderTrace(c, nil, nil) }

func GetAdtyOwnerOrder(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if ok {
		getAdtyUnifiedOrderTrace(c, &site.Id, nil)
	}
}

func GetAdtySelfOrder(c *gin.Context) {
	userID := c.GetInt("id")
	getAdtyUnifiedOrderTrace(c, nil, &userID)
}

func getAdtyUnifiedOrderTrace(c *gin.Context, ownerSiteID *int, selfUserID *int) {
	query, ok := adtyUnifiedOrdersQuery(c, ownerSiteID, selfUserID)
	if !ok {
		return
	}
	tradeNo := c.Param("trade_no")
	if tradeNo == "" || len(tradeNo) > 255 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var orders []rechargeAuditOrder
	if err := query.Where("o.trade_no = ?", tradeNo).Select(adtyUnifiedOrderSelect).Limit(8).Scan(&orders).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	// A refunded top-up is also projected as a legacy refund with the same trade number.
	// The trace opens the original top-up, which carries payments, wallet rows, and refund facts.
	if len(orders) > 1 {
		var topups []rechargeAuditOrder
		for _, item := range orders {
			if item.SourceType == "topup" {
				topups = append(topups, item)
			}
		}
		if len(topups) == 1 {
			orders = topups
		}
	}
	if len(orders) != 1 {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Order not found"))
		return
	}
	order := orders[0]
	if order.SourceType == "topup" {
		// Personal readers use the same restricted projection as owners. The
		// original query already verified user_id before entering top-up trace.
		if selfUserID != nil {
			getAdtyOrderTrace(c, nil, *selfUserID)
		} else {
			getAdtyOrderTrace(c, ownerSiteID)
		}
		return
	}
	if order.SiteId != nil {
		names, err := service.AdtySiteNames([]int{*order.SiteId})
		if err != nil {
			common.ApiError(c, err)
			return
		}
		order.SiteName = names[*order.SiteId]
	}
	sections := []string{"business"}
	switch order.SourceType {
	case "billing", "manual_refund", "staff_adjustment":
		sections = append(sections, "wallet", "funds")
	case "legacy_refund", "balance_ledger":
		sections = append(sections, "wallet")
	case "site_withdrawal":
		if ownerSiteID == nil && selfUserID == nil {
			sections = append(sections, "site_wallet")
		}
	case "site_invite_withdrawal", "site_commission":
		sections = append(sections, "commissions", "wallet")
	case "main_withdrawal", "main_commission":
		sections = append(sections, "commission_movements")
	}
	section := c.DefaultQuery("section", "business")
	if section == "payments" {
		section = "business"
	}
	page := common.GetPageQuery(c)
	entries := []adtyOrderTraceEntry{}
	var total int64
	if section == "business" {
		total = 1
		if page.GetPage() == 1 {
			entries = append(entries, adtyOrderTraceEntry{Id: int64(order.Id), Kind: order.OrderType, Source: order.SourceType,
				Status: order.Status, Reference: order.Reference, CreatedAt: order.CreateTime, Amount: &order.FaceUSD, Currency: "USD"})
		}
	} else {
		allowed := false
		for _, name := range sections {
			allowed = allowed || section == name
		}
		trace, projection, valid := adtyUnifiedOrderTraceQuery(section, order)
		if !allowed || !valid {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		if err := trace.Session(&gorm.Session{}).Count(&total).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		if err := trace.Select(projection).Order("created_at DESC, reference DESC").Limit(page.GetPageSize()).Offset(page.GetStartIdx()).Scan(&entries).Error; err != nil {
			common.ApiError(c, err)
			return
		}
	}
	if ownerSiteID != nil || selfUserID != nil {
		for i := range entries {
			entries[i].Note, entries[i].Proof, entries[i].OperatorId = "", "", 0
			entries[i].BalanceBefore, entries[i].BalanceAfter = nil, nil
		}
	}
	var cancellation *service.SiteInviteCancelState
	if order.SourceType == "site_invite_withdrawal" {
		var row model.AdtySiteInviteWithdrawal
		if err := model.DB.Select("status", "method", "cancel_deadline").Where("id = ? AND user_id = ? AND site_id = ?", order.Reference, order.UserId, order.SiteId).First(&row).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		state := service.SiteInviteCancellation(&row, time.Now().Unix())
		cancellation = &state
	}
	common.ApiSuccess(c, gin.H{"order": order, "cancellation": cancellation, "refund": gin.H{"refunded_at": 0, "refund_quota": 0, "refund_amount": "0", "currency": ""},
		"section": section, "sections": sections, "items": entries, "total": total, "page": page.GetPage(), "page_size": page.GetPageSize()})
}

// Associations are taken from the selected business row, never from the user's
// other activity. Refund step keys stay in subqueries and are not returned.
func adtyUnifiedOrderTraceQuery(section string, order rechargeAuditOrder) (*gorm.DB, string, bool) {
	walletProjection := "id, 'Wallet movement' AS kind, type AS status, ref_id AS reference, delta_quota, balance_before, balance_after, operator_id, reason AS note, created_at"
	commissionProjection := "id, 'Commission wallet movement' AS kind, type AS status, ref_id AS reference, settlement_currency AS currency, delta_pending AS pending_delta, delta_available AS available_delta, delta_frozen AS frozen_delta, delta_withdrawn AS withdrawn_delta, remark AS note, created_at"
	switch order.SourceType {
	case "billing":
		if section == "wallet" {
			return model.DB.Model(&model.BillingOperationMovement{}).Where("operation_key = ? AND user_id = ?", order.Reference, order.UserId),
				"0 AS id, 'Wallet movement' AS kind, stage AS status, movement_key AS reference, wallet_delta AS delta_quota, created_at", true
		}
		if section == "funds" {
			return model.DB.Model(&model.AdtyFundAllocation{}).Where("operation_id = ? AND user_id = ?", order.Reference, order.UserId),
				"id, kind, source, source AS reference, settled AS delta_quota, created_at", true
		}
	case "manual_refund":
		if section == "wallet" {
			row := model.DB.Model(&model.AdtyManualRefund{}).Select("NULLIF(reclaim_idem_key, '')").Where("refund_id = ? AND user_id = ? AND trade_no = ?", order.Reference, order.UserId, order.OriginalTradeNo)
			completed := model.DB.Model(&model.AdtyManualRefund{}).Select("NULLIF(complete_idem_key, '')").Where("refund_id = ? AND user_id = ? AND trade_no = ?", order.Reference, order.UserId, order.OriginalTradeNo)
			return model.DB.Model(&model.AdtyBalanceLedger{}).Where("user_id = ? AND ref_type = 'topup' AND ref_id = ? AND (idempotency_key IN (?) OR idempotency_key IN (?))", order.UserId, order.OriginalTradeNo, row, completed), walletProjection, true
		}
		if section == "funds" {
			return model.DB.Model(&model.AdtyFundBatch{}).Where("user_id = ? AND source = ?", order.UserId, order.OriginalTradeNo),
				"id, kind, status, source AS reference, remaining AS delta_quota, created_at", true
		}
	case "legacy_refund":
		if section == "wallet" {
			return model.DB.Model(&model.AdtyBalanceLedger{}).Where("user_id = ? AND type = 'refund' AND ref_type = 'topup' AND ref_id = ?", order.UserId, order.Reference), walletProjection, true
		}
	case "staff_adjustment":
		if section == "wallet" {
			return model.DB.Model(&model.AdtyBalanceLedger{}).Where("user_id = ? AND ref_type = 'staff_balance' AND ref_id = ?", order.UserId, order.Reference), walletProjection, true
		}
		if section == "funds" {
			keys := model.DB.Model(&model.AdtyBalanceLedger{}).Select("idempotency_key").Where("user_id = ? AND ref_type = 'staff_balance' AND ref_id = ?", order.UserId, order.Reference)
			return model.DB.Model(&model.AdtyFundBatch{}).Where("user_id = ? AND source IN (?)", order.UserId, keys),
				"id, kind, status, source AS reference, remaining AS delta_quota, created_at", true
		}
	case "balance_ledger":
		if section == "wallet" {
			return model.DB.Model(&model.AdtyBalanceLedger{}).Where("id = ? AND user_id = ?", order.Reference, order.UserId), walletProjection, true
		}
	case "site_withdrawal":
		if section == "site_wallet" {
			key := model.DB.Model(&model.AdtySiteWithdrawal{}).Select("idempotency_key").Where("id = ? AND owner_user_id = ? AND site_id = ?", order.Reference, order.UserId, order.SiteId)
			return model.DB.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ? AND ref_type = 'site_withdraw' AND ref_id IN (?)", order.SiteId, key),
				"id, 'Site wallet movement' AS kind, wallet_kind AS source, type AS status, '' AS reference, delta AS amount, balance_before, balance_after, created_at", true
		}
	case "site_invite_withdrawal", "site_commission":
		commissions := model.DB.Model(&model.AdtySiteInviteCommission{}).Where("id = ? AND inviter_id = ? AND site_id = ?", order.Reference, order.UserId, order.SiteId)
		if order.SourceType == "site_invite_withdrawal" {
			ids := model.DB.Model(&model.AdtySiteInviteWithdrawalItem{}).Select("commission_id").Where("withdrawal_id = ?", order.Reference)
			commissions = model.DB.Model(&model.AdtySiteInviteCommission{}).Where("id IN (?) AND inviter_id = ? AND site_id = ?", ids, order.UserId, order.SiteId)
		}
		if section == "commissions" {
			return commissions, "id, 'Commission' AS kind, status, trade_no AS reference, amount_usd AS amount, 'USD' AS currency, created_at", true
		}
		if section == "wallet" {
			if order.SourceType == "site_invite_withdrawal" {
				return model.DB.Model(&model.AdtyBalanceLedger{}).Where("user_id = ? AND ref_type = 'site_invite_withdrawal' AND ref_id = ?", order.UserId, order.Reference), walletProjection, true
			}
			return model.DB.Model(&model.AdtyBalanceLedger{}).Where("user_id = ? AND ref_type = 'site_invite' AND ref_id IN (?)", order.UserId, commissions.Select("trade_no")), walletProjection, true
		}
	case "main_withdrawal", "main_commission":
		if section == "commission_movements" {
			column := "commission_id"
			if order.SourceType == "main_withdrawal" {
				column = "withdrawal_id"
			}
			return model.DB.Model(&model.ReferralCommissionLedger{}).Where(column+" = ? AND user_id = ?", order.Reference, order.UserId), commissionProjection, true
		}
	}
	return nil, "", false
}

func getAdtyOrderTrace(c *gin.Context, ownerSiteID *int, selfUserID ...int) {
	tradeNo := c.Param("trade_no")
	if tradeNo == "" || len(tradeNo) > 255 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	query := model.DB.Table("top_ups AS t").
		Joins("LEFT JOIN adty_topup_orders a ON a.trade_no = t.trade_no").
		Joins("LEFT JOIN users u ON u.id = t.user_id").Where("t.trade_no = ?", tradeNo)
	if ownerSiteID != nil {
		query = query.Where("a.site_id = ?", *ownerSiteID)
	}
	if len(selfUserID) > 0 {
		query = query.Where("t.user_id = ?", selfUserID[0])
	}
	var orders []rechargeAuditOrder
	if err := query.Select(rechargeAuditSelect).Limit(1).Scan(&orders).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if len(orders) == 0 {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Order not found"))
		return
	}
	order := orders[0]
	order.OrderType = "topup"
	order.SourceType, order.Reference, order.OriginalTradeNo = "topup", order.TradeNo, order.TradeNo
	if order.PaymentProvider == model.AdtyVoucherProvider {
		order.OrderType = "voucher"
	}
	order.Anomaly = rechargeAuditAnomaly(order)
	if order.SiteId != nil {
		names, err := service.AdtySiteNames([]int{*order.SiteId})
		if err != nil {
			common.ApiError(c, err)
			return
		}
		order.SiteName = names[*order.SiteId]
	}
	isSelf := len(selfUserID) > 0
	isAdmin := ownerSiteID == nil && !isSelf
	if isAdmin {
		order.ChannelName = adtyEpayChannelNames()[order.ChannelId]
	} else {
		order.ChannelId = 0
		order.ChannelName = ""
	}
	var ext model.AdtyTopupOrder
	err := model.DB.Where("trade_no = ?", tradeNo).First(&ext).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiError(c, err)
		return
	}
	sections := []string{"payments", "wallet", "funds", "refunds", "exceptions", "reviews"}
	if isSelf {
		sections = []string{"payments", "wallet", "funds", "refunds"}
	} else if isAdmin {
		sections = []string{"payments", "wallet", "funds", "refunds", "site_wallet", "exceptions", "reviews", "commissions", "commission_movements"}
	}
	section := c.DefaultQuery("section", "payments")
	if isSelf {
		// Reuse the restricted trace projection after selecting this user's
		// exact order. Unknown legacy sites remain unknown, never guessed.
		hiddenSite := -1
		ownerSiteID = &hiddenSite
		if order.SiteId != nil {
			ownerSiteID = order.SiteId
		}
	}
	traceQuery, projection, ok := adtyOrderTraceQuery(section, order, ownerSiteID, isSelf)
	if !ok {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	page := common.GetPageQuery(c)
	var total int64
	if err := traceQuery.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	entries := []adtyOrderTraceEntry{}
	if err := traceQuery.Select(projection).Order("created_at DESC, id DESC").Limit(page.GetPageSize()).Offset(page.GetStartIdx()).Scan(&entries).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	refundCurrency := ext.PaidCurrency
	if refundCurrency == "" {
		refundCurrency = ext.PayCurrency
	}
	refundUSD := decimal.NewFromInt(ext.RefundQuota).Div(common.QuotaPerUnitDecimal()).String()
	refund := gin.H{"refunded_at": ext.RefundedAt, "refund_quota": ext.RefundQuota, "refund_amount": ext.RefundAmount, "refund_usd": refundUSD, "currency": refundCurrency}
	if !isSelf {
		preview, previewErr := model.LoadSiteInviteCommissionRefundPreview(model.DB, tradeNo, ext.RefundQuota, ext.FaceQuota())
		if previewErr != nil {
			common.ApiError(c, previewErr)
			return
		}
		if preview != nil {
			refund["commission"] = preview
			if preview.ClawedUSD != "0" {
				refund["commission_clawed_usd"] = preview.ClawedUSD
				refund["commission_recovered_usd"] = preview.RecoveredUSD
			}
			if preview.WithdrawnUSD != "0" {
				refund["commission_withdrawn_usd"] = preview.WithdrawnUSD
			}
		}
		var recoveries []model.AdtyCommissionRecovery
		if err := model.DB.Where("trade_no = ?", tradeNo).Find(&recoveries).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		recovery := decimal.Zero
		for _, row := range recoveries {
			recovery = recovery.Add(row.AmountUSD)
		}
		if recovery.IsPositive() {
			refund["commission_recovery_usd"] = recovery.String()
		}
	}
	if ownerSiteID == nil {
		refund["reason"] = ext.RefundReason
	}
	if ownerSiteID != nil {
		if order.PaidSource == model.AdtyPaymentSourceManual {
			order.PaidTxHash = ""
		}
		for i := range entries {
			entries[i].Note, entries[i].Proof, entries[i].OperatorId = "", "", 0
			// These balances include the buyer's unrelated activity at other sites.
			if section == "wallet" {
				entries[i].BalanceBefore, entries[i].BalanceAfter = nil, nil
			}
		}
	}
	common.ApiSuccess(c, gin.H{"order": order, "refund": refund,
		"section": section, "sections": sections, "items": entries, "total": total, "page": page.GetPage(), "page_size": page.GetPageSize()})
}

// Each section has its own total and pagination. Static projections omit raw
// provider payload/error fields and administrative credentials for every role.
func adtyOrderTraceQuery(section string, order rechargeAuditOrder, ownerSiteID *int, isSelf bool) (*gorm.DB, string, bool) {
	switch section {
	case "payments":
		q := model.DB.Model(&model.AdtyOrderPaymentEvent{}).Where("trade_no = ? AND user_id = ? AND provider = ?", order.TradeNo, order.UserId, order.PaymentProvider)
		if ownerSiteID != nil {
			q = q.Where("site_id = ?", *ownerSiteID)
		}
		return q, "id, 'Payment' AS kind, source, outcome AS status, event_type AS reference, CASE WHEN outcome IN ('credited', 'replayed') THEN paid_amount ELSE NULL END AS amount, paid_currency AS currency, created_at, operator_id", true
	case "wallet":
		return model.DB.Model(&model.AdtyBalanceLedger{}).Where("ref_type IN ? AND ref_id = ? AND user_id = ?", []string{"topup", "voucher"}, order.TradeNo, order.UserId),
			"id, 'Wallet movement' AS kind, type AS status, ref_id AS reference, delta_quota, balance_before, balance_after, operator_id, reason AS note, created_at", true
	case "funds":
		return model.DB.Model(&model.AdtyFundBatch{}).Where("user_id = ? AND source = ?", order.UserId, order.TradeNo),
			"id, kind, status, source AS reference, remaining AS delta_quota, created_at", true
	case "refunds":
		return model.DB.Model(&model.AdtyManualRefund{}).Where("user_id = ? AND trade_no = ?", order.UserId, order.TradeNo),
			"id, 'Manual refund' AS kind, status, refund_id AS reference, request_usd AS amount, 'USD' AS currency, reason AS note, payout_voucher AS proof, created_at", true
	case "site_wallet":
		if ownerSiteID != nil {
			return nil, "", false
		}
		q := model.DB.Model(&model.AdtySiteWalletLedger{}).Where("ref_id = ? AND ref_type IN ?", order.TradeNo, []string{"topup", "site_invite", "voucher"})
		if order.SiteId == nil {
			q = q.Where("1 = 0")
		} else {
			q = q.Where("site_id = ?", *order.SiteId)
		}
		return q, "id, 'Site wallet movement' AS kind, wallet_kind AS source, type AS status, ref_id AS reference, delta AS amount, balance_before, balance_after, reason AS note, operator_id, created_at", true
	case "exceptions":
		if isSelf {
			return nil, "", false
		}
		return model.DB.Model(&model.PaymentOrphanEvent{}).Where("reference_id = ? AND provider = ?", order.TradeNo, order.PaymentProvider),
			"id, 'Reconciliation event' AS kind, event_type AS source, status, event_id AS reference, create_time AS created_at", true
	case "reviews":
		if isSelf {
			return nil, "", false
		}
		return model.DB.Model(&model.PaymentOrphanReview{}).Where("trade_no = ? AND provider = ?", order.TradeNo, order.PaymentProvider),
			"id, 'Financial review' AS kind, status, external_amount AS amount, external_currency AS currency, refund_ledger_id AS reference, operator_id, note, created_at", true
	case "commissions":
		if ownerSiteID != nil {
			return nil, "", false
		}
		return model.DB.Model(&model.ReferralCommission{}).Where("source_type = ? AND source_trade_no = ? AND source_order_id = ? AND invitee_user_id = ?", model.ReferralSourceTopup, order.TradeNo, order.Id, order.UserId),
			"id, 'Commission' AS kind, status, commission_amount AS amount, settlement_currency AS currency, source_trade_no AS reference, created_at", true
	case "commission_movements":
		if ownerSiteID != nil {
			return nil, "", false
		}
		ids := model.DB.Model(&model.ReferralCommission{}).Select("id").Where("source_type = ? AND source_trade_no = ? AND source_order_id = ? AND invitee_user_id = ?", model.ReferralSourceTopup, order.TradeNo, order.Id, order.UserId)
		return model.DB.Model(&model.ReferralCommissionLedger{}).Where("commission_id IN (?)", ids),
			"id, 'Commission wallet movement' AS kind, type AS status, ref_id AS reference, settlement_currency AS currency, delta_pending AS pending_delta, delta_available AS available_delta, delta_frozen AS frozen_delta, delta_withdrawn AS withdrawn_delta, remark AS note, created_at", true
	}
	return nil, "", false
}
