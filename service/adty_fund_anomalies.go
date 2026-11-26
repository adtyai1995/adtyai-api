package service

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	AdtyAnomalyBalanceLedgerGap      = "balance_ledger_gap"
	AdtyAnomalyOpenShortfallHold     = "open_shortfall_hold"
	AdtyAnomalyMargin                = "margin_anomaly"
	AdtyAnomalyDuplicateCredit       = "duplicate_credit"
	AdtyAnomalyDuplicateRefund       = "duplicate_refund"
	AdtyAnomalyOrderSuccessUnbooked  = "order_success_unbooked"
	AdtyAnomalyGiftGuaranteeShort    = "gift_guarantee_short"
	AdtyAnomalyEarningSupplyMismatch = "earning_supply_mismatch"
	AdtyAnomalyRefundAmountMismatch  = "refund_amount_mismatch"
)

type adtyFundAnomalyItem struct {
	Kind, Ref, OrderRef, Difference, Evidence string
	SiteID, AccountID                         int
}

func detectAdtyFundAnomalies(siteID int) ([]adtyFundAnomalyItem, error) {
	if siteID < 0 {
		return nil, ErrAdtySiteWalletInvalidOperation
	}
	if siteID == 0 {
		return detectMainSiteFundAnomalies()
	}
	items, err := detectExistingAdtyFundAnomalies(siteID)
	if err != nil {
		return nil, err
	}
	unbooked, err := detectUnbookedSuccessfulOrders(siteID)
	if err != nil {
		return nil, err
	}
	items = append(items, unbooked...)
	gifts, err := detectInsufficientGiftGuarantees(siteID)
	if err != nil {
		return nil, err
	}
	items = append(items, gifts...)
	earnings, err := detectEarningSupplyMismatches(siteID)
	if err != nil {
		return nil, err
	}
	items = append(items, earnings...)
	refunds, err := detectRefundAmountMismatches(siteID)
	if err != nil {
		return nil, err
	}
	items = append(items, refunds...)
	return items, nil
}

func detectMainSiteFundAnomalies() ([]adtyFundAnomalyItem, error) {
	var items []adtyFundAnomalyItem
	principalQuota, giftQuota, lotGap, err := adtySiteCustomerPrincipalAndGiftQuota(0)
	if err != nil {
		return nil, err
	}
	if lotGap != 0 {
		items = append(items, adtyFundAnomalyItem{
			Kind:       AdtyAnomalyBalanceLedgerGap,
			Ref:        "main:balance_ledger_gap",
			SiteID:     0,
			Difference: decimal.NewFromInt(lotGap).Div(common.QuotaPerUnitDecimal()).String(),
			Evidence: mustAnomalyEvidence(map[string]any{
				"customer_principal":   decimal.NewFromInt(principalQuota).Div(common.QuotaPerUnitDecimal()).String(),
				"customer_gift":        decimal.NewFromInt(giftQuota).Div(common.QuotaPerUnitDecimal()).String(),
				"fund_batch_gap_quota": lotGap,
			}),
		})
	}
	var userIDs []int
	if err := model.DB.Model(&model.User{}).Where("site_id = 0").Pluck("id", &userIDs).Error; err != nil {
		return nil, err
	}
	var holds int64
	if len(userIDs) > 0 {
		if err := model.DB.Model(&model.AdtyBillingHold{}).Where("user_id IN ? AND status = ?", userIDs, model.AdtyBillingHoldOpen).Count(&holds).Error; err != nil {
			return nil, err
		}
	}
	if holds > 0 {
		items = append(items, adtyFundAnomalyItem{
			Kind:       AdtyAnomalyOpenShortfallHold,
			Ref:        "main:open_shortfall_hold",
			SiteID:     0,
			Difference: strconv.FormatInt(holds, 10),
			Evidence:   mustAnomalyEvidence(map[string]any{"open_holds": holds}),
		})
	}
	dupCredit, err := adtyDuplicateEventTradeNos(0, "credited", "")
	if err != nil {
		return nil, err
	}
	if len(dupCredit) > 0 {
		items = append(items, adtyFundAnomalyItem{
			Kind:       AdtyAnomalyDuplicateCredit,
			Ref:        "main:duplicate_credit",
			SiteID:     0,
			Difference: strconv.Itoa(len(dupCredit)),
			Evidence:   mustAnomalyEvidence(map[string]any{"trade_nos": dupCredit, "reason": "repeated credited payment events"}),
		})
	}
	dupRefund, err := adtyDuplicateEventTradeNos(0, "", "refund.succeeded")
	if err != nil {
		return nil, err
	}
	if len(dupRefund) > 0 {
		items = append(items, adtyFundAnomalyItem{
			Kind:       AdtyAnomalyDuplicateRefund,
			Ref:        "main:duplicate_refund",
			SiteID:     0,
			Difference: strconv.Itoa(len(dupRefund)),
			Evidence:   mustAnomalyEvidence(map[string]any{"trade_nos": dupRefund, "reason": "repeated refund.succeeded payment events"}),
		})
	}
	unbooked, err := detectUnbookedSuccessfulOrders(0)
	if err != nil {
		return nil, err
	}
	items = append(items, unbooked...)
	refunds, err := detectRefundAmountMismatches(0)
	if err != nil {
		return nil, err
	}
	items = append(items, refunds...)
	return items, nil
}

func detectExistingAdtyFundAnomalies(siteID int) ([]adtyFundAnomalyItem, error) {
	var wallets []model.AdtySiteWallet
	if err := model.DB.Where("site_id = ?", siteID).Find(&wallets).Error; err != nil {
		return nil, err
	}
	balanceGap := int64(0)
	marginGap := decimal.Zero
	var operating decimal.Decimal
	var walletMismatches []map[string]any
	for _, wallet := range wallets {
		var deltas []decimal.Decimal
		if err := model.DB.Model(&model.AdtySiteWalletLedger{}).Where("wallet_id = ?", wallet.Id).Pluck("delta", &deltas).Error; err != nil {
			return nil, err
		}
		sum := decimal.Zero
		for _, delta := range deltas {
			sum = sum.Add(delta)
		}
		if !sum.Equal(wallet.Balance) {
			balanceGap = 1
			walletMismatches = append(walletMismatches, map[string]any{
				"wallet_id": wallet.Id, "kind": wallet.Kind,
				"balance": wallet.Balance.String(), "ledger_sum": sum.String(),
				"difference": wallet.Balance.Sub(sum).String(),
			})
		}
		if wallet.Kind == model.AdtySiteWalletOperating {
			operating = wallet.Balance
		}
		if wallet.Kind == model.AdtySiteWalletMargin && wallet.CycleTierUSD.IsPositive() {
			over := wallet.CycleUsedUSD.Sub(AdtyMarginCycleLimit(wallet.CycleTierUSD))
			if over.IsPositive() {
				marginGap = over
			}
		}
	}
	// Stored principal lots remain operating liabilities until settlement. Gifts
	// keep their original backing and are reconciled separately (ISS-R1-0075).
	principalQuota, giftQuota, lotGap, err := adtySiteCustomerPrincipalAndGiftQuota(siteID)
	if err != nil {
		return nil, err
	}
	principal := decimal.NewFromInt(principalQuota).Div(common.QuotaPerUnitDecimal())
	giftUSD := decimal.NewFromInt(giftQuota).Div(common.QuotaPerUnitDecimal())
	// Both API and approved refund reservations remain a liability until
	// settlement/reclaim reduces the original batch and operating balance.
	var ownerQuota int64
	if err := model.DB.Table("adty_fund_batches b").
		Joins("JOIN adty_sites s ON s.id = b.site_id AND s.owner_user_id = b.user_id").
		Joins("JOIN users u ON u.id = b.user_id").
		Where("b.site_id = ? AND b.kind = ? AND u.site_id <> ?", siteID, model.AdtyFundBatchKindPrincipal, siteID).
		Select("COALESCE(SUM(b.remaining),0)").Scan(&ownerQuota).Error; err != nil {
		return nil, err
	}
	ownerPrincipal := decimal.NewFromInt(ownerQuota).Div(common.QuotaPerUnitDecimal())
	operatingGap := ReconcileOperatingWallet(principal.Add(ownerPrincipal), operating)
	if !operatingGap.IsZero() || lotGap != 0 {
		balanceGap = 1
	}
	var userIDs []int
	if err := model.DB.Model(&model.User{}).Where("site_id = ?", siteID).Pluck("id", &userIDs).Error; err != nil {
		return nil, err
	}
	var holds int64
	if len(userIDs) > 0 {
		if err := model.DB.Model(&model.AdtyBillingHold{}).Where("user_id IN ? AND status = ?", userIDs, model.AdtyBillingHoldOpen).Count(&holds).Error; err != nil {
			return nil, err
		}
	}
	kinds := AdtyFundAnomalies(balanceGap, int(holds), marginGap)
	dupCredit, err := adtyDuplicateEventTradeNos(siteID, "credited", "")
	if err != nil {
		return nil, err
	}
	dupRefund, err := adtyDuplicateEventTradeNos(siteID, "", "refund.succeeded")
	if err != nil {
		return nil, err
	}
	if len(dupCredit) > 0 {
		kinds = append(kinds, AdtyAnomalyDuplicateCredit)
	}
	if len(dupRefund) > 0 {
		kinds = append(kinds, AdtyAnomalyDuplicateRefund)
	}

	ref := strconv.Itoa(siteID)
	var items []adtyFundAnomalyItem
	for _, kind := range kinds {
		item := adtyFundAnomalyItem{Kind: kind, Ref: ref, SiteID: siteID}
		switch kind {
		case AdtyAnomalyBalanceLedgerGap:
			item.Difference = operatingGap.String()
			if operatingGap.IsZero() && lotGap != 0 {
				item.Difference = decimal.NewFromInt(lotGap).Div(common.QuotaPerUnitDecimal()).String()
			}
			item.Evidence = mustAnomalyEvidence(map[string]any{
				"operating_wallet":     operating.String(),
				"customer_principal":   principal.String(),
				"owner_principal":      ownerPrincipal.String(),
				"customer_gift":        giftUSD.String(),
				"fund_batch_gap_quota": lotGap,
				"operating_gap":        operatingGap.String(),
				"wallet_mismatches":    walletMismatches,
			})
		case AdtyAnomalyOpenShortfallHold:
			item.Difference = strconv.FormatInt(holds, 10)
			item.Evidence = mustAnomalyEvidence(map[string]any{"open_holds": holds})
		case AdtyAnomalyMargin:
			item.Difference = marginGap.String()
			item.Evidence = mustAnomalyEvidence(map[string]any{"cycle_overage_usd": marginGap.String()})
		case AdtyAnomalyDuplicateCredit:
			item.Difference = strconv.Itoa(len(dupCredit))
			item.Evidence = mustAnomalyEvidence(map[string]any{"trade_nos": dupCredit, "reason": "repeated credited payment events"})
		case AdtyAnomalyDuplicateRefund:
			item.Difference = strconv.Itoa(len(dupRefund))
			item.Evidence = mustAnomalyEvidence(map[string]any{"trade_nos": dupRefund, "reason": "repeated refund.succeeded payment events"})
		}
		items = append(items, item)
	}
	return items, nil
}

// adtySiteCustomerPrincipalAndGiftQuota separates unconsumed customer principal
// liability from unconsumed gift/bonus quota for siteID (G.3 / A.11 / A.12).
// Lot remaining includes reservations until settlement. Compare it separately
// with the logical wallet plus outstanding shares so a gap cannot become principal.
func adtySiteCustomerPrincipalAndGiftQuota(siteID int) (principalQuota int64, giftQuota int64, lotGap int64, err error) {
	type userQuotaRow struct {
		Id           int `gorm:"column:id"`
		Quota        int `gorm:"column:quota"`
		QuotaPending int `gorm:"column:quota_pending"`
	}
	var users []userQuotaRow
	if err := model.DB.Model(&model.User{}).
		Select("id, quota, quota_pending").
		Where("site_id = ?", siteID).
		Find(&users).Error; err != nil {
		return 0, 0, 0, err
	}
	if len(users) == 0 {
		return 0, 0, 0, nil
	}
	userIDs := make([]int, 0, len(users))
	for _, u := range users {
		userIDs = append(userIDs, u.Id)
	}

	type batchSplit struct {
		sitePrincipal int64
		siteGift      int64
		allPrincipal  int64
		allGift       int64
		hasAnyLots    bool
	}
	byUser := make(map[int]batchSplit, len(users))
	if model.DB.Migrator().HasTable(&model.AdtyFundBatch{}) {
		var batches []model.AdtyFundBatch
		if err := model.DB.Where("user_id IN ?", userIDs).
			Find(&batches).Error; err != nil {
			return 0, 0, 0, err
		}
		for _, b := range batches {
			split := byUser[b.UserId]
			split.hasAnyLots = true
			inTargetSite := b.SiteId == nil || *b.SiteId == siteID
			switch b.Kind {
			case model.AdtyFundBatchKindPrincipal:
				split.allPrincipal += b.Remaining
				if inTargetSite {
					split.sitePrincipal += b.Remaining
				}
			case model.AdtyFundBatchKindBonus, model.AdtyFundBatchKindExpiringBonus:
				split.allGift += b.Remaining
				if inTargetSite {
					split.siteGift += b.Remaining
				}
			}
			byUser[b.UserId] = split
		}
	}

	heldByUser := map[int]int64{}
	if model.DB.Migrator().HasTable(&model.AdtyFundAllocation{}) && model.DB.Migrator().HasTable(&model.AdtyFundBatch{}) {
		var held []struct {
			UserId int
			Quota  int64
		}
		if err := model.DB.Table("adty_fund_allocations AS a").
			Joins("JOIN adty_fund_batches b ON b.id = a.batch_id").
			Select("a.user_id, COALESCE(SUM(a.held - a.settled), 0) AS quota").
			Where("a.user_id IN ? AND a.held > a.settled AND a.operation_id NOT LIKE ?", userIDs, "owner-refund:%").
			Group("a.user_id").Scan(&held).Error; err != nil {
			return 0, 0, 0, err
		}
		for _, row := range held {
			heldByUser[row.UserId] = row.Quota
		}
	}

	for _, u := range users {
		logical := int64(u.Quota) + int64(u.QuotaPending)
		split := byUser[u.Id]
		if !split.hasAnyLots {
			// Historical accounts without any lots retain the logical-balance fallback.
			principalQuota += logical
			continue
		}
		// Return only target-site lots for this site's operating balance, while
		// reconciling the user's global wallet against all of their lots so valid
		// cross-site lots (e.g. legacy owner self-use lots) never trigger false gaps.
		principalQuota += split.sitePrincipal
		giftQuota += split.siteGift
		gap := logical + heldByUser[u.Id] - split.allPrincipal - split.allGift
		if gap < 0 {
			gap = -gap
		}
		lotGap += gap
	}
	return principalQuota, giftQuota, lotGap, nil
}

func adtyDuplicateEventTradeNos(siteID int, outcome, eventType string) ([]string, error) {
	query := model.DB.Model(&model.AdtyOrderPaymentEvent{}).Where("site_id = ?", siteID)
	if outcome != "" {
		query = query.Where("outcome = ?", outcome)
	}
	if eventType != "" {
		query = query.Where("event_type = ?", eventType)
	}
	var trades []string
	if err := query.Group("trade_no").Having("COUNT(*) > 1").Pluck("trade_no", &trades).Error; err != nil {
		return nil, err
	}
	return trades, nil
}

type adtySuccessOrderRow struct {
	TradeNo               string
	UserId                int
	SiteId                int
	Status                string
	Business              string
	TeamId                int64
	GiftFulfillmentStatus string
	BonusUSD              decimal.Decimal
	CreditQuota           int64
	FaceUSD               decimal.Decimal
	PaymentProvider       string
	PaymentMethod         string
	HasExt                bool
}

func detectUnbookedSuccessfulOrders(siteID int) ([]adtyFundAnomalyItem, error) {
	var rows []adtySuccessOrderRow
	err := model.DB.Table("top_ups AS t").
		Select(`t.trade_no AS trade_no, t.user_id AS user_id, t.status AS status,
			COALESCE(a.site_id, u.site_id) AS site_id,
			COALESCE(a.business, '') AS business,
			COALESCE(a.team_id, 0) AS team_id,
			COALESCE(a.gift_fulfillment_status, '') AS gift_fulfillment_status,
			COALESCE(a.bonus_usd, 0) AS bonus_usd,
			COALESCE(a.credit_quota, 0) AS credit_quota,
			COALESCE(a.face_usd, 0) AS face_usd,
			COALESCE(t.payment_provider, '') AS payment_provider,
			COALESCE(t.payment_method, '') AS payment_method,
			(a.trade_no IS NOT NULL) AS has_ext`).
		Joins("JOIN users u ON u.id = t.user_id").
		Joins("LEFT JOIN adty_topup_orders a ON a.trade_no = t.trade_no").
		Where("t.status IN ? AND (a.site_id = ? OR (a.trade_no IS NULL AND u.site_id = ?))",
			[]string{common.TopUpStatusSuccess, model.TopUpStatusRefunded}, siteID, siteID).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	var items []adtyFundAnomalyItem
	for _, row := range rows {
		// 1) Owner margin top-ups & site_margin vouchers credit the site's margin wallet, not AdtyBalanceLedger.
		isMarginVoucher := row.PaymentProvider == model.AdtyVoucherProvider && row.PaymentMethod == model.AdtyVoucherPurposeSiteMargin
		if row.Business == model.AdtyBusinessOwnerMarginTopup || isMarginVoucher {
			if !row.FaceUSD.IsPositive() {
				continue
			}
			refType := "topup"
			reason := "successful owner margin top-up was not booked to margin wallet"
			if isMarginVoucher {
				refType = "voucher"
				reason = "redeemed site_margin voucher was not booked to margin wallet"
			}
			var marginBooked decimal.Decimal
			if err := model.DB.Model(&model.AdtySiteWalletLedger{}).
				Where("site_id = ? AND wallet_kind = ? AND ref_type = ? AND ref_id = ? AND delta > 0",
					siteID, model.AdtySiteWalletMargin, refType, row.TradeNo).
				Select("COALESCE(SUM(delta), 0)").Scan(&marginBooked).Error; err != nil {
				return nil, err
			}
			if !marginBooked.LessThan(row.FaceUSD) {
				continue
			}
			gapUSD := row.FaceUSD.Sub(marginBooked)
			items = append(items, adtyFundAnomalyItem{
				Kind:       AdtyAnomalyOrderSuccessUnbooked,
				Ref:        row.TradeNo,
				OrderRef:   row.TradeNo,
				SiteID:     siteID,
				AccountID:  row.UserId,
				Difference: gapUSD.String(),
				Evidence: mustAnomalyEvidence(map[string]any{
					"status": row.Status, "business": row.Business, "payment_method": row.PaymentMethod,
					"expected_margin_usd": row.FaceUSD.String(), "booked_margin_usd": marginBooked.String(),
					"reason": reason,
				}),
			})
			continue
		}

		expected := row.CreditQuota
		if expected <= 0 && row.FaceUSD.IsPositive() {
			expected = row.FaceUSD.Mul(common.QuotaPerUnitDecimal()).IntPart()
		}
		if expected <= 0 {
			continue
		}

		// 2) Team top-ups credit AdtyTeamLedger, not personal AdtyBalanceLedger.
		if row.Business == model.AdtyBusinessTeamTopup || row.TeamId > 0 {
			var teamBooked int64
			if err := model.DB.Model(&model.AdtyTeamLedger{}).
				Where("team_id = ? AND ref_id = ? AND delta_quota > 0", row.TeamId, row.TradeNo).
				Select("COALESCE(SUM(delta_quota), 0)").Scan(&teamBooked).Error; err != nil {
				return nil, err
			}
			if teamBooked >= expected {
				continue
			}
			gap := expected - teamBooked
			items = append(items, adtyFundAnomalyItem{
				Kind:       AdtyAnomalyOrderSuccessUnbooked,
				Ref:        row.TradeNo,
				OrderRef:   row.TradeNo,
				SiteID:     siteID,
				AccountID:  row.UserId,
				Difference: decimal.NewFromInt(gap).Div(common.QuotaPerUnitDecimal()).String(),
				Evidence: mustAnomalyEvidence(map[string]any{
					"status": row.Status, "business": row.Business, "team_id": row.TeamId,
					"expected_credit_quota": expected, "booked_quota": teamBooked,
					"reason": "successful team top-up was not booked to team ledger",
				}),
			})
			continue
		}

		// 3) Pending / under-review / canceled / shrunk-then-fulfilled gift fulfillment:
		// adjust expected booked quota to principal + actually fulfilled bonus (R5-002).
		if row.GiftFulfillmentStatus == model.AdtyGiftFulfillmentPending ||
			row.GiftFulfillmentStatus == model.AdtyGiftFulfillmentPendingReview ||
			row.GiftFulfillmentStatus == model.AdtyGiftFulfillmentCanceled {
			faceQuota := row.FaceUSD.Mul(common.QuotaPerUnitDecimal()).Floor().IntPart()
			if faceQuota > 0 && faceQuota < expected {
				expected = faceQuota
			}
		} else if row.GiftFulfillmentStatus == model.AdtyGiftFulfillmentFulfilled && model.DB.Migrator().HasTable(&model.AdtyGiftFulfillment{}) {
			var fulfill model.AdtyGiftFulfillment
			if err := model.DB.Select("bonus_quota").Where("trade_no = ?", row.TradeNo).Limit(1).Find(&fulfill).Error; err != nil {
				return nil, err
			}
			if fulfill.BonusQuota > 0 {
				faceQuota := row.FaceUSD.Mul(common.QuotaPerUnitDecimal()).Floor().IntPart()
				fulfilledExpected := faceQuota + fulfill.BonusQuota
				if fulfilledExpected > 0 && fulfilledExpected < expected {
					expected = fulfilledExpected
				}
			}
		}

		// 4) Personal wallet top-ups & voucher redemptions (include voucher and gift_fulfillment ledger types).
		var booked int64
		if err := model.DB.Model(&model.AdtyBalanceLedger{}).
			Where("user_id = ? AND ref_id = ? AND delta_quota > 0 AND ((type = ? AND ref_type = ?) OR (type = ? AND ref_type = ?) OR (type = ? AND ref_type = ?))",
				row.UserId, row.TradeNo,
				model.AdtyLedgerTypeTopUp, "topup",
				model.AdtyLedgerTypeVoucher, "voucher",
				model.AdtyLedgerTypeGiftFulfillment, "topup").
			Select("COALESCE(SUM(delta_quota), 0)").Scan(&booked).Error; err != nil {
			return nil, err
		}
		if booked >= expected {
			continue
		}
		gap := expected - booked
		items = append(items, adtyFundAnomalyItem{
			Kind:       AdtyAnomalyOrderSuccessUnbooked,
			Ref:        row.TradeNo,
			OrderRef:   row.TradeNo,
			SiteID:     siteID,
			AccountID:  row.UserId,
			Difference: decimal.NewFromInt(gap).Div(common.QuotaPerUnitDecimal()).String(),
			Evidence: mustAnomalyEvidence(map[string]any{
				"status": row.Status, "has_ext": row.HasExt, "business": row.Business,
				"expected_credit_quota": expected, "booked_quota": booked,
				"face_usd": row.FaceUSD.String(),
				"reason":   "successful top-up principal or official credit was not booked",
			}),
		})
	}
	return items, nil
}

func detectInsufficientGiftGuarantees(siteID int) ([]adtyFundAnomalyItem, error) {
	var orders []model.AdtyTopupOrder
	if err := model.DB.Where("site_id = ? AND bonus_usd > 0", siteID).Find(&orders).Error; err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return nil, nil
	}
	tradeNos := make([]string, 0, len(orders))
	for _, ext := range orders {
		tradeNos = append(tradeNos, ext.TradeNo)
	}
	statusByTrade, err := loadTopUpStatuses(tradeNos)
	if err != nil {
		return nil, err
	}
	holdByTrade, ledgerCountByTrade, err := loadGiftHoldByTrade(siteID, tradeNos)
	if err != nil {
		return nil, err
	}
	bonusBatchByTrade, err := loadGiftBatchRemainingByTrade(siteID, tradeNos)
	if err != nil {
		return nil, err
	}
	var items []adtyFundAnomalyItem
	for _, ext := range orders {
		status := strings.TrimSpace(statusByTrade[ext.TradeNo])
		if !giftGuaranteeShouldBeHeld(status) {
			// Failed / expired / canceled / replaced orders must not retain any frozen site gift guarantee (R1-003).
			if ledgerCountByTrade[ext.TradeNo] > 0 && holdByTrade[ext.TradeNo].IsPositive() {
				held := holdByTrade[ext.TradeNo]
				items = append(items, adtyFundAnomalyItem{
					Kind:       AdtyAnomalyGiftGuaranteeShort,
					Ref:        ext.TradeNo,
					OrderRef:   ext.TradeNo,
					SiteID:     siteID,
					AccountID:  ext.UserId,
					Difference: held.String(),
					Evidence: mustAnomalyEvidence(map[string]any{
						"status":           status,
						"required_usd":     "0",
						"reserved_earning": ext.GiftReserveEarning.String(),
						"reserved_margin":  ext.GiftReserveMargin.String(),
						"held_usd":         held.String(),
						"bonus_usd":        ext.BonusUSD.String(),
						"reason":           "non-active order still holds unreleased site gift guarantee",
					}),
				})
			}
			continue
		}
		batchInfo, hasBatch := bonusBatchByTrade[ext.TradeNo]
		required, err := currentRequiredGiftGuaranteeUSD(&ext, status, batchInfo, hasBatch)
		if err != nil {
			return nil, err
		}
		if !required.IsPositive() {
			continue
		}
		snapshot := ext.GiftReserveEarning.Add(ext.GiftReserveMargin)
		held := snapshot
		if ledgerCountByTrade[ext.TradeNo] > 0 {
			held = holdByTrade[ext.TradeNo]
		}
		if !held.LessThan(required) {
			continue
		}
		items = append(items, adtyFundAnomalyItem{
			Kind:       AdtyAnomalyGiftGuaranteeShort,
			Ref:        ext.TradeNo,
			OrderRef:   ext.TradeNo,
			SiteID:     siteID,
			AccountID:  ext.UserId,
			Difference: required.Sub(held).String(),
			Evidence: mustAnomalyEvidence(map[string]any{
				"status":           status,
				"required_usd":     required.String(),
				"reserved_earning": ext.GiftReserveEarning.String(),
				"reserved_margin":  ext.GiftReserveMargin.String(),
				"held_usd":         held.String(),
				"bonus_usd":        ext.BonusUSD.String(),
				"reason":           "site-funded gift guarantee is less than the official reserve",
			}),
		})
	}
	return items, nil
}

type giftBatchRemainingInfo struct {
	Remaining int64
	Original  int64
}

func loadGiftBatchRemainingByTrade(siteID int, tradeNos []string) (map[string]giftBatchRemainingInfo, error) {
	out := map[string]giftBatchRemainingInfo{}
	if len(tradeNos) == 0 || !model.DB.Migrator().HasTable(&model.AdtyFundBatch{}) {
		return out, nil
	}
	var batches []model.AdtyFundBatch
	if err := model.DB.Where("source IN ? AND (site_id = ? OR site_id = 0 OR site_id IS NULL) AND kind IN ?", tradeNos, siteID, []string{
		model.AdtyFundBatchKindBonus,
		model.AdtyFundBatchKindExpiringBonus,
	}).Find(&batches).Error; err != nil {
		return nil, err
	}
	for _, b := range batches {
		info := out[b.Source]
		info.Remaining += b.Remaining
		info.Original += b.Original
		out[b.Source] = info
	}
	return out, nil
}

// currentRequiredGiftGuaranteeUSD computes the current remaining site-funded
// gift guarantee liability for ext (ISS-R1-0076 / G.9.4 / A.8 / A.12).
// Already consumed or legitimately reclaimed gift quota no longer requires a
// frozen guarantee; a fully refunded order with zero remaining gift requires 0.
func currentRequiredGiftGuaranteeUSD(ext *model.AdtyTopupOrder, status string, batchInfo giftBatchRemainingInfo, hasBatch bool) (decimal.Decimal, error) {
	if ext == nil {
		return decimal.Zero, nil
	}
	siteBonus := adtyBonusUSDForSite(ext)
	if !siteBonus.IsPositive() {
		return decimal.Zero, nil
	}
	if status == common.TopUpStatusPending || status == "" {
		return siteBonus, nil
	}
	switch ext.GiftFulfillmentStatus {
	case model.AdtyGiftFulfillmentPending, model.AdtyGiftFulfillmentPendingReview, model.AdtyGiftFulfillmentCanceled:
		return decimal.Zero, nil
	}
	totalBonusQuota, err := siteGiftBonusQuota(ext)
	if err != nil {
		return decimal.Zero, err
	}
	if totalBonusQuota <= 0 {
		return decimal.Zero, nil
	}
	faceQuota := ext.FaceQuota()
	if status == model.TopUpStatusRefunded && (ext.RefundQuota == 0 || (faceQuota > 0 && ext.RefundQuota >= faceQuota)) {
		if !hasBatch || batchInfo.Remaining <= 0 {
			return decimal.Zero, nil
		}
	}
	if hasBatch {
		rem := min(batchInfo.Remaining, totalBonusQuota)
		if rem <= 0 {
			return decimal.Zero, nil
		}
		return adtyProportionalGiftUSD(siteBonus, rem, totalBonusQuota), nil
	}
	reclaimedQuota := ext.RefundBonusQuota
	if model.DB.Migrator().HasTable(&model.AdtyManualRefund{}) {
		hasRows, _, cumReclaimed, err := adtyCompletedManualRefundBonusStatsTx(model.DB, ext)
		if err != nil {
			return decimal.Zero, err
		}
		if hasRows && cumReclaimed > reclaimedQuota {
			reclaimedQuota = cumReclaimed
		}
	}
	if reclaimedQuota <= 0 && ext.RefundQuota > 0 && faceQuota > 0 {
		whole, _ := decimal.NewFromInt(totalBonusQuota).Mul(decimal.NewFromInt(min(ext.RefundQuota, faceQuota))).QuoRem(decimal.NewFromInt(faceQuota), 0)
		reclaimedQuota = whole.IntPart()
	}
	remainingBonusQuota := totalBonusQuota - reclaimedQuota
	if remainingBonusQuota <= 0 {
		return decimal.Zero, nil
	}
	required := adtyProportionalGiftUSD(siteBonus, remainingBonusQuota, totalBonusQuota)
	var spent decimal.Decimal
	if err := model.DB.Model(&model.AdtySiteWalletLedger{}).
		Where("site_id = ? AND ref_type = ? AND ref_id = ? AND type = ?", ext.SiteId, "topup", ext.TradeNo, "gift_spend").
		Select("COALESCE(SUM(-delta), 0)").Scan(&spent).Error; err != nil {
		return decimal.Zero, err
	}
	var adjustReleased decimal.Decimal
	if err := model.DB.Model(&model.AdtySiteWalletLedger{}).
		Where("site_id = ? AND ref_type = ? AND ref_id = ? AND type = ? AND idempotency_key LIKE ?", ext.SiteId, "topup", ext.TradeNo, "gift_release", "%:adjust-release:%").
		Select("COALESCE(SUM(-hold_delta), 0)").Scan(&adjustReleased).Error; err != nil {
		return decimal.Zero, err
	}
	return decimal.Max(decimal.Zero, required.Sub(spent).Sub(adjustReleased)), nil
}

func giftGuaranteeShouldBeHeld(status string) bool {
	switch status {
	case common.TopUpStatusPending, common.TopUpStatusSuccess, model.TopUpStatusRefunded, "":
		return true
	default:
		return false
	}
}

func loadTopUpStatuses(tradeNos []string) (map[string]string, error) {
	out := map[string]string{}
	if len(tradeNos) == 0 {
		return out, nil
	}
	var rows []model.TopUp
	if err := model.DB.Select("trade_no", "status").Where("trade_no IN ?", tradeNos).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.TradeNo] = row.Status
	}
	return out, nil
}

func loadGiftHoldByTrade(siteID int, tradeNos []string) (map[string]decimal.Decimal, map[string]int64, error) {
	holds := map[string]decimal.Decimal{}
	counts := map[string]int64{}
	if len(tradeNos) == 0 {
		return holds, counts, nil
	}
	type holdRow struct {
		RefId   string
		Held    decimal.Decimal
		Ledgers int64
	}
	var rows []holdRow
	err := model.DB.Model(&model.AdtySiteWalletLedger{}).
		Select("ref_id, COALESCE(SUM(hold_delta), 0) AS held, COUNT(*) AS ledgers").
		Where("site_id = ? AND ref_type = ? AND ref_id IN ? AND type IN ?", siteID, "topup", tradeNos, adtyGiftNetHoldTypes()).
		Group("ref_id").Scan(&rows).Error
	if err != nil {
		return nil, nil, err
	}
	for _, row := range rows {
		holds[row.RefId] = row.Held
		counts[row.RefId] = row.Ledgers
	}
	return holds, counts, nil
}

func normalizeAdtyEarningDay(day string) string {
	day = strings.TrimSpace(day)
	if base, _, ok := strings.Cut(day, "+"); ok {
		return strings.TrimSpace(base)
	}
	return day
}

func adtyEarningEventKind(event model.AdtySiteEarningEvent) (string, bool) {
	if event.ReviewStatus == model.AdtyEarningNeedsReview {
		return "", false
	}
	kind := model.AdtySiteEarningUsage
	if event.Quota < 0 {
		kind = model.AdtySiteEarningRefund
	}
	if event.ReviewStatus == model.AdtyEarningOriginalSkipped {
		kind = model.AdtySiteEarningSkipped
	}
	return kind, true
}

func detectEarningSupplyMismatches(siteID int) ([]adtyFundAnomalyItem, error) {
	var events []model.AdtySiteEarningEvent
	if err := model.DB.Where("site_id = ?", siteID).Find(&events).Error; err != nil {
		return nil, err
	}
	var items []adtyFundAnomalyItem
	seen := map[string]struct{}{}

	var dailies []model.AdtySiteEarningDaily
	if err := model.DB.Where("site_id = ? AND kind IN ?", siteID,
		[]string{model.AdtySiteEarningUsage, model.AdtySiteEarningRefund, model.AdtySiteEarningSkipped}).
		Find(&dailies).Error; err != nil {
		return nil, err
	}
	type eventSum struct {
		UserId                            int
		BaseDay                           string
		Kind                              string
		Quota                             int64
		ProjectedCount                    int
		OfficialPlatform, OfficialEarning decimal.Decimal
	}
	sumsByKind := map[string]eventSum{}
	eventDays := map[string]bool{}
	kindOrder := make([]string, 0, len(events))
	for _, event := range events {
		k := decimal.NewFromFloat(event.Markup)
		if event.Quota == 0 || !k.IsPositive() {
			continue
		}
		kind, ok := adtyEarningEventKind(event)
		if !ok {
			continue
		}
		baseDay := normalizeAdtyEarningDay(event.Day)
		if baseDay == "" {
			continue
		}
		_, officialPlatform, officialEarning := OfficialAdtySupplySplit(int64(event.Quota), k)
		var operations []model.BillingOperation
		if err := model.DB.Where("operation_key = ? AND user_id = ? AND site_id = ?", event.OperationKey, event.UserId, event.SiteId).Limit(1).Find(&operations).Error; err != nil {
			return nil, err
		}
		if len(operations) > 0 {
			if snapshot := operations[0].RequestPriceSnapshot(); snapshot != nil {
				group, gOK := model.ParseSnapshotDecimal(snapshot.GroupRatio)
				cost, cOK := model.ParseSnapshotDecimal(snapshot.SiteCostFactor)
				billed, bOK := snapshot.BilledGroupRatioDecimal()
				if !gOK || !cOK || !bOK || !group.IsPositive() || !cost.IsPositive() || !billed.IsPositive() {
					return nil, model.ErrBillingOperationConflict
				}
				revenue := decimal.NewFromInt(int64(event.Quota)).Div(common.QuotaPerUnitDecimal()).Round(8)
				officialPlatform = decimal.NewFromInt(int64(event.Quota)).Mul(group).Mul(cost).DivRound(billed.Mul(common.QuotaPerUnitDecimal()), 8)
				officialEarning = revenue.Sub(officialPlatform)
			}
		}
		kindKey := strconv.Itoa(event.UserId) + "|" + baseDay + "|" + kind
		row, exists := sumsByKind[kindKey]
		if !exists {
			row = eventSum{UserId: event.UserId, BaseDay: baseDay, Kind: kind}
			kindOrder = append(kindOrder, kindKey)
		}
		row.Quota += int64(event.Quota)
		if event.Projected {
			row.ProjectedCount++
		}
		row.OfficialPlatform = row.OfficialPlatform.Add(officialPlatform)
		row.OfficialEarning = row.OfficialEarning.Add(officialEarning)
		sumsByKind[kindKey] = row
		eventDays[strconv.Itoa(event.UserId)+"|"+baseDay] = true
	}

	type dailyAggregate struct {
		UserId    int
		BaseDay   string
		Kind      string
		Quota     int64
		Revenue   decimal.Decimal
		Platform  decimal.Decimal
		Earning   decimal.Decimal
		HasOpSlot bool
		RawDays   []string
	}
	dailyGroups := map[string]*dailyAggregate{}
	dailyKeys := make([]string, 0, len(dailies))
	for _, daily := range dailies {
		if daily.Quota == 0 && daily.Platform.IsZero() && daily.Earning.IsZero() {
			continue
		}
		baseDay := normalizeAdtyEarningDay(daily.Day)
		if baseDay == "" {
			continue
		}
		key := strconv.Itoa(daily.UserId) + "|" + baseDay + "|" + daily.Kind
		grp, exists := dailyGroups[key]
		if !exists {
			grp = &dailyAggregate{
				UserId:  daily.UserId,
				BaseDay: baseDay,
				Kind:    daily.Kind,
			}
			dailyGroups[key] = grp
			dailyKeys = append(dailyKeys, key)
		}
		grp.Quota += daily.Quota
		grp.Revenue = grp.Revenue.Add(daily.Revenue)
		grp.Platform = grp.Platform.Add(daily.Platform)
		grp.Earning = grp.Earning.Add(daily.Earning)
		if strings.Contains(strings.TrimSpace(daily.Day), "+op") {
			grp.HasOpSlot = true
		}
		grp.RawDays = append(grp.RawDays, daily.Day)
	}

	matchedKindKeys := map[string]bool{}
	perUnit := common.QuotaPerUnitDecimal()

	for _, key := range dailyKeys {
		grp := dailyGroups[key]
		dayKey := strconv.Itoa(grp.UserId) + "|" + grp.BaseDay
		sum, ok := sumsByKind[key]
		if ok {
			matchedKindKeys[key] = true
		}
		if !ok {
			expectedRev := decimal.NewFromInt(grp.Quota).Div(perUnit).Round(8)
			expectedEarn := grp.Revenue.Sub(grp.Platform)
			if !eventDays[dayKey] && !grp.HasOpSlot && grp.Revenue.Equal(expectedRev) && grp.Earning.Equal(expectedEarn) {
				continue
			}
			ref := "earning-daily:" + grp.BaseDay + ":" + grp.Kind + ":" + strconv.Itoa(grp.UserId)
			if _, exists := seen[ref]; exists {
				continue
			}
			seen[ref] = struct{}{}
			items = append(items, adtyFundAnomalyItem{
				Kind:       AdtyAnomalyEarningSupplyMismatch,
				Ref:        ref,
				OrderRef:   ref,
				SiteID:     siteID,
				AccountID:  grp.UserId,
				Difference: grp.Earning.Abs().String(),
				Evidence: mustAnomalyEvidence(map[string]any{
					"day":               grp.BaseDay,
					"raw_days":          grp.RawDays,
					"kind":              grp.Kind,
					"quota":             grp.Quota,
					"booked_platform":   grp.Platform.String(),
					"booked_earning":    grp.Earning.String(),
					"official_platform": "0",
					"official_earning":  "0",
					"reason":            "daily earning/supply totals have no matching settled earning events",
				}),
			})
			continue
		}

		if grp.Quota == sum.Quota && grp.Platform.Equal(sum.OfficialPlatform) && grp.Earning.Equal(sum.OfficialEarning) {
			continue
		}
		ref := "earning-daily:" + grp.BaseDay + ":" + grp.Kind + ":" + strconv.Itoa(grp.UserId)
		if _, exists := seen[ref]; exists {
			continue
		}
		seen[ref] = struct{}{}
		diff := grp.Earning.Sub(sum.OfficialEarning).Abs()
		if diff.IsZero() && !grp.Platform.Equal(sum.OfficialPlatform) {
			diff = grp.Platform.Sub(sum.OfficialPlatform).Abs()
		}
		if diff.IsZero() && grp.Quota != sum.Quota {
			diff = decimal.NewFromInt(grp.Quota - sum.Quota).Abs().Div(perUnit).Round(8)
		}
		items = append(items, adtyFundAnomalyItem{
			Kind:       AdtyAnomalyEarningSupplyMismatch,
			Ref:        ref,
			OrderRef:   ref,
			SiteID:     siteID,
			AccountID:  grp.UserId,
			Difference: diff.String(),
			Evidence: mustAnomalyEvidence(map[string]any{
				"day":               grp.BaseDay,
				"raw_days":          grp.RawDays,
				"kind":              grp.Kind,
				"quota":             grp.Quota,
				"event_quota":       sum.Quota,
				"booked_platform":   grp.Platform.String(),
				"booked_earning":    grp.Earning.String(),
				"official_platform": sum.OfficialPlatform.String(),
				"official_earning":  sum.OfficialEarning.String(),
				"reason":            "daily earning/supply totals do not match official A.10 books",
			}),
		})
	}

	// Projected settled events that have no corresponding daily summary row are
	// also reconciled and flagged as earning/supply mismatches (ISS-R1-0077).
	for _, kindKey := range kindOrder {
		sum := sumsByKind[kindKey]
		if matchedKindKeys[kindKey] || sum.ProjectedCount == 0 {
			continue
		}
		ref := "earning-daily:" + sum.BaseDay + ":" + sum.Kind + ":" + strconv.Itoa(sum.UserId)
		if _, exists := seen[ref]; exists {
			continue
		}
		seen[ref] = struct{}{}
		diff := sum.OfficialEarning.Abs()
		if diff.IsZero() {
			diff = sum.OfficialPlatform.Abs()
		}
		items = append(items, adtyFundAnomalyItem{
			Kind:       AdtyAnomalyEarningSupplyMismatch,
			Ref:        ref,
			OrderRef:   ref,
			SiteID:     siteID,
			AccountID:  sum.UserId,
			Difference: diff.String(),
			Evidence: mustAnomalyEvidence(map[string]any{
				"day":               sum.BaseDay,
				"kind":              sum.Kind,
				"quota":             0,
				"event_quota":       sum.Quota,
				"booked_platform":   "0",
				"booked_earning":    "0",
				"official_platform": sum.OfficialPlatform.String(),
				"official_earning":  sum.OfficialEarning.String(),
				"reason":            "projected earning events are missing from daily earning/supply summary",
			}),
		})
	}
	return items, nil
}

func detectRefundAmountMismatches(siteID int) ([]adtyFundAnomalyItem, error) {
	var refunds []model.AdtyManualRefund
	if err := model.DB.Where("site_id = ?", siteID).Order("id").Find(&refunds).Error; err != nil {
		return nil, err
	}
	var orders []model.AdtyTopupOrder
	if err := model.DB.Where("site_id = ? AND refund_quota > 0", siteID).Find(&orders).Error; err != nil {
		return nil, err
	}
	refundsByTrade := map[string][]model.AdtyManualRefund{}
	tradeSet := map[string]struct{}{}
	for _, row := range refunds {
		refundsByTrade[row.TradeNo] = append(refundsByTrade[row.TradeNo], row)
		tradeSet[row.TradeNo] = struct{}{}
	}
	ordersByTrade := map[string]model.AdtyTopupOrder{}
	for _, ext := range orders {
		ordersByTrade[ext.TradeNo] = ext
		tradeSet[ext.TradeNo] = struct{}{}
	}
	missing := make([]string, 0)
	for trade := range tradeSet {
		if _, ok := ordersByTrade[trade]; !ok {
			missing = append(missing, trade)
		}
	}
	if len(missing) > 0 {
		var extra []model.AdtyTopupOrder
		if err := model.DB.Where("site_id = ? AND trade_no IN ?", siteID, missing).Find(&extra).Error; err != nil {
			return nil, err
		}
		for _, ext := range extra {
			ordersByTrade[ext.TradeNo] = ext
		}
	}
	var items []adtyFundAnomalyItem
	for trade := range tradeSet {
		ext, ok := ordersByTrade[trade]
		if !ok {
			continue
		}
		rows := refundsByTrade[trade]
		if item, ok := refundOccupancyMismatch(siteID, &ext, rows); ok {
			items = append(items, item)
			continue
		}
		if item, ok := refundRowAmountMismatch(siteID, &ext, rows); ok {
			items = append(items, item)
		}
	}
	return items, nil
}

func refundOccupancyMismatch(siteID int, ext *model.AdtyTopupOrder, rows []model.AdtyManualRefund) (adtyFundAnomalyItem, bool) {
	completed := ext.RefundQuota
	if completed < 0 {
		completed = 0
	}
	var held, unknown int64
	for _, row := range rows {
		switch row.Status {
		case model.AdtyManualRefundStatusCompleted, model.AdtyManualRefundStatusTerminated:
			continue
		default:
			if row.PayoutStatus == model.AdtyManualRefundPayoutUnknown {
				unknown += row.RequestQuota
			} else {
				held += row.RequestQuota
			}
		}
	}
	extOffset, _ := model.AdtyExternalRefundOffsetForRowsTx(model.DB, ext, rows)
	if extOffset < 0 {
		extOffset = 0
	}
	occupied := completed + held + unknown + extOffset
	face := ext.FaceQuota()
	if occupied <= face {
		return adtyFundAnomalyItem{}, false
	}
	gap := occupied - face
	return adtyFundAnomalyItem{
		Kind:       AdtyAnomalyRefundAmountMismatch,
		Ref:        ext.TradeNo,
		OrderRef:   ext.TradeNo,
		SiteID:     siteID,
		AccountID:  ext.UserId,
		Difference: decimal.NewFromInt(gap).Div(common.QuotaPerUnitDecimal()).String(),
		Evidence: mustAnomalyEvidence(map[string]any{
			"face_quota": face, "completed_s": completed, "in_progress_h": held,
			"unknown_u": unknown, "external_offset_x": extOffset, "occupied": occupied,
			"reason": "refunded, occupied, unknown, or external offset amounts exceed the authorized remaining principal",
		}),
	}, true
}

func refundRowAmountMismatch(siteID int, ext *model.AdtyTopupOrder, rows []model.AdtyManualRefund) (adtyFundAnomalyItem, bool) {
	for _, row := range rows {
		if row.ReclaimedAt > 0 {
			sum := row.DeductedPrincipalQuota + row.ShortfallQuota
			if sum != row.RequestQuota {
				gap := row.RequestQuota - sum
				if gap < 0 {
					gap = -gap
				}
				return adtyFundAnomalyItem{
					Kind:       AdtyAnomalyRefundAmountMismatch,
					Ref:        ext.TradeNo,
					OrderRef:   ext.TradeNo,
					SiteID:     siteID,
					AccountID:  ext.UserId,
					Difference: decimal.NewFromInt(gap).Div(common.QuotaPerUnitDecimal()).String(),
					Evidence: mustAnomalyEvidence(map[string]any{
						"refund_id": row.RefundId, "request_quota": row.RequestQuota,
						"deducted_principal_quota": row.DeductedPrincipalQuota,
						"shortfall_quota":          row.ShortfallQuota,
						"reason":                   "deducted plus shortfall does not equal the authorized refund quota",
					}),
				}, true
			}
		}
		if row.PayoutStatus == model.AdtyManualRefundPayoutSuccess {
			payoutCur := strings.ToUpper(strings.TrimSpace(row.PayoutCurrency))
			orderCur := strings.ToUpper(strings.TrimSpace(ext.PaidCurrency))
			if orderCur == "" {
				orderCur = strings.ToUpper(strings.TrimSpace(ext.PayCurrency))
			}
			var expected decimal.Decimal
			sameCurrency := false
			switch {
			case row.ForeignReference.IsPositive() && (payoutCur == "" || orderCur == "" || payoutCur == orderCur):
				expected = row.ForeignReference
				sameCurrency = true
			case payoutCur == "" || payoutCur == "USD":
				expected = row.RequestUSD
				sameCurrency = true
			}
			if sameCurrency && expected.IsPositive() && !row.PayoutAmount.Equal(expected) {
				return adtyFundAnomalyItem{
					Kind:       AdtyAnomalyRefundAmountMismatch,
					Ref:        ext.TradeNo,
					OrderRef:   ext.TradeNo,
					SiteID:     siteID,
					AccountID:  ext.UserId,
					Difference: row.PayoutAmount.Sub(expected).Abs().String(),
					Evidence: mustAnomalyEvidence(map[string]any{
						"refund_id": row.RefundId, "payout_amount": row.PayoutAmount.String(),
						"payout_currency":   payoutCur,
						"order_currency":    orderCur,
						"foreign_reference": row.ForeignReference.String(),
						"request_usd":       row.RequestUSD.String(),
						"payout_status":     row.PayoutStatus,
						"reason":            "paid-out amount does not match the authorized refund",
					}),
				}, true
			}
			if !sameCurrency && (!row.PayoutAmount.IsPositive() || strings.TrimSpace(row.PayoutVoucher) == "") {
				return adtyFundAnomalyItem{
					Kind:       AdtyAnomalyRefundAmountMismatch,
					Ref:        ext.TradeNo,
					OrderRef:   ext.TradeNo,
					SiteID:     siteID,
					AccountID:  ext.UserId,
					Difference: row.RequestUSD.String(),
					Evidence: mustAnomalyEvidence(map[string]any{
						"refund_id": row.RefundId, "payout_amount": row.PayoutAmount.String(),
						"payout_currency": payoutCur, "order_currency": orderCur,
						"payout_voucher": row.PayoutVoucher, "payout_status": row.PayoutStatus,
						"reason": "cross-currency refund payout is missing a positive amount or voucher proof",
					}),
				}, true
			}
		}
	}
	return adtyFundAnomalyItem{}, false
}

// adtyAnomalyDispositionOpen 是仍在等待复核的处置行状态。
const adtyAnomalyDispositionOpen = "open"

// upsertOpenAdtyAnomalyDisposition 记录一次检出，并保证同一异常只有一条 open 处置行。
//
// 查和写放在同一事务里，并按异常身份串行：手动刷新和定时扫描（或两个节点）同时
// 发现同一条异常时，两边都会读到「没有 open 行」再各插一条。这一点没法交给唯一
// 约束兜底——(kind, ref) 在本表里本来就可以重复（`batch_register` 按 note 区分
// 发起人，同一条异常也会被重新检出），唯一索引会把它们一起挡住。
func upsertOpenAdtyAnomalyDisposition(item adtyFundAnomalyItem) error {
	kind, ref := strings.TrimSpace(item.Kind), strings.TrimSpace(item.Ref)
	if kind == "" || ref == "" || item.SiteID < 0 {
		return common.NewMessage("Invalid parameters")
	}
	updates := map[string]any{
		"site_id": item.SiteID, "account_id": item.AccountID,
		"order_ref":  strings.TrimSpace(item.OrderRef),
		"difference": item.Difference, "evidence": item.Evidence,
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAdtyOpenAnomalyDisposition(tx, kind, ref); err != nil {
			return err
		}
		var existing []model.AdtyAnomalyDisposition
		lookup := model.AdtyLockForUpdate(tx).
			Where("kind = ? AND ref = ? AND status IN ?", kind, ref, []string{adtyAnomalyDispositionOpen, "reviewing"}).
			Limit(1).Find(&existing)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 {
			return tx.Create(&model.AdtyAnomalyDisposition{
				Kind: kind, Ref: ref, Status: adtyAnomalyDispositionOpen, Note: "detected",
				SiteId: item.SiteID, AccountId: item.AccountID, OrderRef: strings.TrimSpace(item.OrderRef),
				Difference: item.Difference, Evidence: item.Evidence,
			}).Error
		}
		return tx.Model(&existing[0]).Updates(updates).Error
	})
}

// lockAdtyOpenAnomalyDisposition 让同一条异常的「查-插」互斥。生产库只有
// PostgreSQL（CheckAdtyPostgreSQL），advisory lock 随事务结束释放，跨节点同样
// 生效；SQLite 没有这把锁，但它单写，第二个写者会被挡住而不是提交出重复行。
func lockAdtyOpenAnomalyDisposition(tx *gorm.DB, kind, ref string) error {
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return nil
	}
	key := "adty-anomaly-disposition\x1f" + kind + "\x1f" + ref
	return tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", key).Error
}

func mustAnomalyEvidence(fields map[string]any) string {
	raw, err := common.Marshal(fields)
	if err != nil {
		return ""
	}
	return string(raw)
}
