package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const adtyBusinessTimezoneKey = "adty.business_timezone"
const defaultBusinessTimezone = "Asia/Shanghai"

// SaveAdtyBusinessTimezone stores the Root business-day timezone.
// An empty value clears it. An unset value uses the China business day. Invalid names are refused.
func SaveAdtyBusinessTimezone(raw string) error {
	name := strings.TrimSpace(raw)
	if name == "" {
		return model.UpdateOption(adtyBusinessTimezoneKey, "")
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return common.NewMessage("Invalid parameters")
	}
	return model.UpdateOption(adtyBusinessTimezoneKey, loc.String())
}

func loadAdtyBusinessLocation() (*time.Location, string, error) {
	raw, ok, err := model.ReadOptionFromDB(adtyBusinessTimezoneKey)
	if err != nil {
		return nil, "", err
	}
	name := strings.TrimSpace(raw)
	if !ok || name == "" {
		name = defaultBusinessTimezone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, "", common.NewMessage("Invalid parameters")
	}
	return loc, loc.String(), nil
}

// FreezeAdtyBillingClock records the business clock once when quoting an
// expression. Serialized attempt snapshots take precedence on a retry.
func FreezeAdtyBillingClock(input *billingexpr.RequestInput, snapshot *billingexpr.BillingSnapshot, start time.Time) (string, error) {
	if snapshot != nil && snapshot.AcceptedAt > 0 {
		input.AcceptedAt = time.Unix(snapshot.AcceptedAt, 0).UTC()
		input.BusinessTimezone = snapshot.BusinessTimezone
		if input.BusinessTimezone == "" {
			input.BusinessTimezone = "UTC"
		} // Legacy terms.
	}
	if input.AcceptedAt.IsZero() {
		switch {
		case input.AcceptedUnix != 0:
			input.AcceptedAt = time.Unix(input.AcceptedUnix, 0).UTC()
		case !start.IsZero():
			input.AcceptedAt = start.UTC()
		default:
			input.AcceptedAt = time.Now().UTC()
		}
	}
	input.AcceptedUnix = input.AcceptedAt.Unix()
	if input.BusinessTimezone == "" {
		zone := defaultBusinessTimezone
		if model.DB != nil {
			_, name, err := loadAdtyBusinessLocation()
			if err != nil {
				return "", err
			}
			zone = name
		}
		input.BusinessTimezone = zone
	}
	loc, err := time.LoadLocation(input.BusinessTimezone)
	if err != nil {
		return "", err
	}
	return input.AcceptedAt.In(loc).Format(time.RFC3339), nil
}

func previousBusinessWindow(now time.Time, loc *time.Location) (day string, start, end time.Time) {
	local := now.In(loc)
	todayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	start = todayStart.AddDate(0, 0, -1)
	end = todayStart
	day = start.Format("2006-01-02")
	return day, start, end
}

func inAdtySheetWindow(ts int64, rowDay, sheetDay string, start, end int64) (matched, hasWindowTS bool) {
	if ts > 0 {
		return ts >= start && ts < end, true
	}
	// Only timestamp-less historical facts retain their original recorded day.
	return adtyEarningDayOf(rowDay) == sheetDay, false
}

// GenerateAdtySiteSettlementSheets writes the previous business day's check sheet.
// A rerun updates that batch's figures and does not credit a wallet.
// A sheet keeps the timezone and window it was first saved with.
func GenerateAdtySiteSettlementSheets(now time.Time) (int, error) {
	return CatchUpAdtySiteSettlementSheets(now)
}

// CatchUpAdtySiteSettlementSheets joins each new window to the preceding
// sealed end. The first day after a zone change may be shorter or longer.
func CatchUpAdtySiteSettlementSheets(now time.Time) (int, error) {
	loc, zone, err := loadAdtyBusinessLocation()
	if err != nil {
		return 0, err
	}
	day, start, _ := previousBusinessWindow(now, loc)
	var sites []model.AdtySite
	if err := model.DB.Select("id").Find(&sites).Error; err != nil {
		return 0, err
	}
	written := 0
	for _, site := range sites {
		count := 0
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			var locked model.AdtySite
			if err := model.AdtyLockForUpdate(tx).Select("id").First(&locked, site.Id).Error; err != nil {
				return err
			}
			var latest model.AdtySiteSettlementSheet
			res := tx.Where("site_id = ?", site.Id).Order("business_day DESC").Limit(1).Find(&latest)
			if res.Error != nil {
				return res.Error
			}
			cursor, windowStart := start, start.Unix()
			if res.RowsAffected > 0 && latest.BusinessDay < day {
				prev, err := time.ParseInLocation("2006-01-02", latest.BusinessDay, loc)
				if err != nil {
					return err
				}
				cursor, windowStart = prev.AddDate(0, 0, 1), latest.DayEnd
			} else if res.RowsAffected > 0 && latest.BusinessDay > day {
				return nil
			}
			for !cursor.After(start) {
				next := cursor.AddDate(0, 0, 1)
				if next.Unix() > windowStart {
					if err := writeAdtySiteSettlementSheetTx(tx, site.Id, cursor.Format("2006-01-02"), windowStart, next.Unix(), zone, now.Unix()); err != nil {
						return err
					}
					count++
					windowStart = next.Unix()
				}
				cursor = next
			}
			return nil
		})
		if err != nil {
			return written, err
		}
		written += count
	}
	return written, nil
}

func writeAdtySiteSettlementSheet(siteID int, day string, start, end int64, timezone string, generatedAt int64) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		return writeAdtySiteSettlementSheetTx(tx, siteID, day, start, end, timezone, generatedAt)
	})
}

func summarizeAdtySiteSettlement(siteID int, day string, start, end int64) (model.AdtySiteSettlementSheet, error) {
	return summarizeAdtySiteSettlementTx(model.DB, siteID, day, start, end)
}

func writeAdtySiteSettlementSheetTx(tx *gorm.DB, siteID int, day string, start, end int64, timezone string, generatedAt int64) error {
	var existing []model.AdtySiteSettlementSheet
	if err := tx.Where("site_id = ? AND business_day = ?", siteID, day).Limit(1).Find(&existing).Error; err != nil {
		return err
	}
	windowStart, windowEnd, zone := start, end, timezone
	if len(existing) == 1 {
		windowStart, windowEnd, zone = existing[0].DayStart, existing[0].DayEnd, existing[0].Timezone
	}
	figures, err := summarizeAdtySiteSettlementTx(tx, siteID, day, windowStart, windowEnd)
	if err != nil {
		return err
	}
	figures.GeneratedAt = generatedAt
	if len(existing) == 1 {
		if figures.PendingCoverageUnverified {
			figures.PendingCoverageUSD = existing[0].PendingCoverageUSD
		}
		if !existing[0].PendingCoverageUSD.Equal(figures.PendingCoverageUSD) {
			change := model.AdtySettlementCoverageAdjustment{SheetId: existing[0].Id, BeforeUSD: existing[0].PendingCoverageUSD, AfterUSD: figures.PendingCoverageUSD, EventWatermark: figures.EventWatermark, CreatedAt: generatedAt}
			if err := tx.Create(&change).Error; err != nil {
				return err
			}
		}
		return tx.WithContext(model.WithAdtySettlementRefresh(context.Background())).
			Model(&model.AdtySiteSettlementSheet{}).Where("id = ?", existing[0].Id).Updates(map[string]any{
			"event_watermark":              figures.EventWatermark,
			"included_count":               figures.IncludedCount,
			"topup_face_usd":               figures.TopupFaceUSD,
			"paid_usd":                     figures.PaidUSD,
			"paid_by_currency":             figures.PaidByCurrency,
			"consumption_usd":              figures.ConsumptionUSD,
			"owner_transfer_usd":           figures.OwnerTransferUSD,
			"owner_self_use_topup_usd":     figures.OwnerSelfUseTopupUSD,
			"owner_self_use_voucher_usd":   figures.OwnerSelfUseVoucherUSD,
			"owner_self_use_refund_usd":    figures.OwnerSelfUseRefundUSD,
			"owner_self_use_usd":           figures.OwnerSelfUseUSD,
			"owner_self_use_cost_usd":      figures.OwnerSelfUseCostUSD,
			"supply_cost_usd":              figures.SupplyCostUSD,
			"spread_usd":                   figures.SpreadUSD,
			"gift_reserve_usd":             figures.GiftReserveUSD,
			"gift_funding_usd":             figures.GiftFundingUSD,
			"gift_release_usd":             figures.GiftReleaseUSD,
			"trial_expense_usd":            figures.TrialExpenseUSD,
			"commission_usd":               figures.CommissionUSD,
			"pending_coverage_usd":         figures.PendingCoverageUSD,
			"pending_coverage_unverified":  figures.PendingCoverageUnverified,
			"margin_delta_usd":             figures.MarginDeltaUSD,
			"earnings_delta_usd":           figures.EarningsDeltaUSD,
			"earnings_available_delta_usd": figures.EarningsAvailableDeltaUSD,
			"earnings_frozen_delta_usd":    figures.EarningsFrozenDeltaUSD,
			"withdrawal_usd":               figures.WithdrawalUSD,
			"withdrawal_fee_usd":           figures.WithdrawalFeeUSD,
			"adjustment_usd":               figures.AdjustmentUSD,
			"anomaly_count":                figures.AnomalyCount,
			"generated_at":                 figures.GeneratedAt,
		}).Error
	}
	figures.SiteId = siteID
	figures.BusinessDay = day
	figures.Timezone = zone
	figures.DayStart = windowStart
	figures.DayEnd = windowEnd
	figures.BatchId = fmt.Sprintf("site-settlement:%d:%s", siteID, day)
	return tx.Create(&figures).Error
}

func summarizeAdtySiteSettlementTx(tx *gorm.DB, siteID int, day string, start, end int64) (model.AdtySiteSettlementSheet, error) {
	sheet := model.AdtySiteSettlementSheet{PaidByCurrency: "{}"}
	paidByCurrency := map[string]decimal.Decimal{}
	note := func(at int64) {
		if at > sheet.EventWatermark {
			sheet.EventWatermark = at
		}
	}

	var topups []model.TopUp
	if err := tx.Where("status IN ? AND complete_time >= ? AND complete_time < ?", []string{common.TopUpStatusSuccess, model.TopUpStatusRefunded}, start, end).Find(&topups).Error; err != nil {
		return sheet, err
	}
	if len(topups) > 0 {
		trades := make([]string, 0, len(topups))
		byTrade := make(map[string]model.TopUp, len(topups))
		for _, row := range topups {
			trades = append(trades, row.TradeNo)
			byTrade[row.TradeNo] = row
		}
		var orders []model.AdtyTopupOrder
		if err := tx.Where("site_id = ? AND trade_no IN ?", siteID, trades).Find(&orders).Error; err != nil {
			return sheet, err
		}
		for _, order := range orders {
			paid := byTrade[order.TradeNo]
			if order.Provider == model.AdtyVoucherProvider {
				switch order.Method {
				case model.AdtyVoucherPurposeSiteOwner:
					sheet.OwnerSelfUseVoucherUSD = sheet.OwnerSelfUseVoucherUSD.Add(order.FaceUSD)
				case model.AdtyVoucherPurposeSiteCustomer:
					sheet.TopupFaceUSD = sheet.TopupFaceUSD.Add(order.FaceUSD)
				}
				note(paid.CompleteTime)
				sheet.IncludedCount++
				continue
			}
			if order.IsOwnerSelfUse() {
				sheet.OwnerSelfUseTopupUSD = sheet.OwnerSelfUseTopupUSD.Add(order.FaceUSD)
			} else {
				sheet.TopupFaceUSD = sheet.TopupFaceUSD.Add(order.FaceUSD)
			}
			currency := strings.TrimSpace(order.PaidCurrency)
			if strings.EqualFold(currency, "USD") {
				sheet.PaidUSD = sheet.PaidUSD.Add(order.PaidAmount)
			} else if !order.PaidAmount.IsZero() {
				paidByCurrency[currency] = paidByCurrency[currency].Add(order.PaidAmount)
			}
			note(paid.CompleteTime)
			sheet.IncludedCount++
		}
	}

	ownerTrades := tx.Model(&model.AdtyTopupOrder{}).Select("trade_no").Where("site_id = ? AND business = ?", siteID, model.AdtyBusinessOwnerSelfUseTopup)
	var refunds struct {
		Amount decimal.Decimal
		Count  int64
		Latest int64
	}
	if err := tx.Model(&model.AdtyManualRefund{}).Where("trade_no IN (?) AND status = ? AND completed_at >= ? AND completed_at < ?", ownerTrades, model.AdtyManualRefundStatusCompleted, start, end).
		Select("COALESCE(SUM(request_usd),0) AS amount, COUNT(*) AS count, COALESCE(MAX(completed_at),0) AS latest").Scan(&refunds).Error; err != nil {
		return sheet, err
	}
	sheet.OwnerSelfUseRefundUSD = refunds.Amount
	sheet.IncludedCount += refunds.Count
	note(refunds.Latest)

	var events []model.AdtySiteEarningEvent
	if err := tx.Where("site_id = ? AND projected = ? AND created_at >= ? AND created_at < ?", siteID, true, start, end).Find(&events).Error; err != nil {
		return sheet, err
	}
	for _, ev := range events {
		if ev.ReviewStatus == model.AdtyEarningOriginalSkipped || ev.ReviewStatus == model.AdtyEarningNeedsReview {
			continue
		}
		revenue, platform, earning, err := adtyDurableEarningSplitTx(tx, &ev)
		if err != nil {
			return sheet, err
		}
		sheet.ConsumptionUSD = sheet.ConsumptionUSD.Add(revenue)
		sheet.SupplyCostUSD = sheet.SupplyCostUSD.Add(platform)
		sheet.SpreadUSD = sheet.SpreadUSD.Add(earning)
		note(ev.CreatedAt)
		sheet.IncludedCount++
	}
	var facts []model.AdtySiteEarningFact
	if err := tx.Where("site_id = ?", siteID).Find(&facts).Error; err != nil {
		return sheet, err
	}
	factsByDaily := map[int64][]model.AdtySiteEarningFact{}
	for _, fact := range facts {
		factsByDaily[fact.DailyId] = append(factsByDaily[fact.DailyId], fact)
	}

	var earnings []model.AdtySiteEarningDaily
	if err := tx.Where("site_id = ? AND status <> ?", siteID, model.AdtySiteEarningVoid).Find(&earnings).Error; err != nil {
		return sheet, err
	}
	for _, row := range earnings {
		if strings.Contains(row.Day, "+op") {
			continue
		}
		if row.Kind != model.AdtySiteEarningUsage && row.Kind != model.AdtySiteEarningRefund {
			continue
		}
		if recorded, ok := factsByDaily[row.Id]; ok {
			for _, fact := range recorded {
				matched, hasTS := inAdtySheetWindow(fact.OccurredAt, row.Day, day, start, end)
				if !matched {
					continue
				}
				sheet.ConsumptionUSD = sheet.ConsumptionUSD.Add(fact.Revenue)
				sheet.SupplyCostUSD = sheet.SupplyCostUSD.Add(fact.Platform)
				sheet.SpreadUSD = sheet.SpreadUSD.Add(fact.Earning)
				if hasTS {
					note(fact.OccurredAt)
				}
				sheet.IncludedCount++
			}
			continue
		}
		matched, hasTS := inAdtySheetWindow(row.UpdatedAt, row.Day, day, start, end)
		if !matched {
			continue
		}
		switch row.Kind {
		case model.AdtySiteEarningUsage, model.AdtySiteEarningRefund:
			sheet.ConsumptionUSD = sheet.ConsumptionUSD.Add(row.Revenue)
			sheet.SupplyCostUSD = sheet.SupplyCostUSD.Add(row.Platform)
			sheet.SpreadUSD = sheet.SpreadUSD.Add(row.Earning)
			if hasTS {
				note(row.UpdatedAt)
			}
			sheet.IncludedCount++
		}
	}

	var grants []model.AdtySiteTrialGrant
	if err := tx.Where("site_id = ?", siteID).Find(&grants).Error; err != nil {
		return sheet, err
	}
	for _, row := range grants {
		matched, hasTS := inAdtySheetWindow(row.CreatedAt, row.Day, day, start, end)
		if !matched {
			continue
		}
		sheet.TrialExpenseUSD = sheet.TrialExpenseUSD.Add(row.AmountUSD)
		if hasTS {
			note(row.CreatedAt)
		}
		sheet.IncludedCount++
	}

	var ledgers []model.AdtySiteWalletLedger
	if err := tx.Where("site_id = ? AND created_at >= ? AND created_at < ?", siteID, start, end).Find(&ledgers).Error; err != nil {
		return sheet, err
	}
	for _, row := range ledgers {
		note(row.CreatedAt)
		sheet.IncludedCount++
		if row.WalletKind == model.AdtySiteWalletEarnings && row.Type == ownerTransfer {
			sheet.OwnerTransferUSD = sheet.OwnerTransferUSD.Sub(row.Delta)
		}
		if row.WalletKind == model.AdtySiteWalletOperating && row.RefType == model.BillingPayerOwner {
			sheet.OwnerSelfUseUSD = sheet.OwnerSelfUseUSD.Sub(row.Delta)
			sheet.OwnerSelfUseCostUSD = sheet.OwnerSelfUseUSD
		}
		if row.Type == "adjustment" || row.Type == model.AdtyLedgerTypeManualAdjust {
			sheet.AdjustmentUSD = sheet.AdjustmentUSD.Add(row.Delta)
		}
		if row.Type == "gift_reserve" {
			reserved := row.HoldDelta
			if reserved.IsZero() {
				reserved = row.Delta.Neg()
			}
			if reserved.IsPositive() {
				sheet.GiftReserveUSD = sheet.GiftReserveUSD.Add(reserved)
			}
		}
		if row.Type == "gift_spend" {
			spent := row.Delta.Neg()
			if !spent.IsPositive() && row.HoldDelta.IsNegative() {
				spent = row.HoldDelta.Neg()
			}
			if spent.IsPositive() {
				sheet.GiftFundingUSD = sheet.GiftFundingUSD.Add(spent)
			}
		}
		if row.Type == "gift_release" {
			released := row.HoldDelta.Neg()
			if !released.IsPositive() && row.Delta.IsPositive() {
				released = row.Delta
			}
			if released.IsPositive() {
				sheet.GiftReleaseUSD = sheet.GiftReleaseUSD.Add(released)
			}
		}
		switch row.WalletKind {
		case model.AdtySiteWalletEarnings:
			sheet.EarningsDeltaUSD = sheet.EarningsDeltaUSD.Add(row.Delta)
			if !row.HoldDelta.IsZero() {
				sheet.EarningsAvailableDeltaUSD = sheet.EarningsAvailableDeltaUSD.Add(row.Delta.Sub(row.HoldDelta))
				sheet.EarningsFrozenDeltaUSD = sheet.EarningsFrozenDeltaUSD.Add(row.HoldDelta)
			} else if row.Type == "earning_withdraw_freeze" || row.Type == "earning_withdraw_release" || row.Type == "gift_reserve" {
				sheet.EarningsAvailableDeltaUSD = sheet.EarningsAvailableDeltaUSD.Add(row.Delta)
				sheet.EarningsFrozenDeltaUSD = sheet.EarningsFrozenDeltaUSD.Add(row.Delta.Neg())
			} else {
				sheet.EarningsAvailableDeltaUSD = sheet.EarningsAvailableDeltaUSD.Add(row.Delta)
			}
		case model.AdtySiteWalletMargin:
			sheet.MarginDeltaUSD = sheet.MarginDeltaUSD.Add(row.Delta)
		}
	}

	var commissions []model.AdtySiteInviteCommission
	if err := tx.Where("site_id = ? AND created_at > 0 AND created_at < ?", siteID, end).Find(&commissions).Error; err != nil {
		return sheet, err
	}
	var freezes []struct {
		RefId  string
		Amount decimal.Decimal
	}
	if err := tx.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ? AND type = ? AND ref_type = ? AND created_at < ?", siteID, "commission_freeze", "site_invite", end).Select("ref_id, SUM(hold_delta) AS amount").Group("ref_id").Scan(&freezes).Error; err != nil {
		return sheet, err
	}
	covered := map[string]decimal.Decimal{}
	for _, freeze := range freezes {
		covered[freeze.RefId] = freeze.Amount
	}
	var cancellations []model.AdtyCommissionCoverageCancellation
	if err := tx.Where("site_id = ? AND (created_at < ? OR undated = ?)", siteID, end, true).Find(&cancellations).Error; err != nil {
		return sheet, err
	}
	canceled := map[int64]decimal.Decimal{}
	unknown := map[int64]bool{}
	for _, event := range cancellations {
		if event.Undated && event.CreatedAt >= end {
			unknown[event.CommissionId] = true
			continue
		}
		canceled[event.CommissionId] = canceled[event.CommissionId].Add(event.AmountUSD)
		if event.CreatedAt >= start {
			sheet.IncludedCount++
			note(event.CreatedAt)
		}
	}
	for _, row := range commissions {
		if row.CreatedAt >= start {
			sheet.CommissionUSD = sheet.CommissionUSD.Add(row.AmountUSD)
			note(row.CreatedAt)
			sheet.IncludedCount++
		}
		// A dated completion is also evidence for older one-shot coverage.
		if row.CoveredAt == 0 || row.CoveredAt >= end {
			if unknown[row.Id] {
				sheet.PendingCoverageUnverified = true
			}
			pending := row.AmountUSD.Sub(covered[row.TradeNo]).Sub(canceled[row.Id])
			if pending.IsPositive() {
				sheet.PendingCoverageUSD = sheet.PendingCoverageUSD.Add(pending)
			}
		}
	}

	var withdrawals []model.AdtySiteWithdrawal
	if err := tx.Where("site_id = ? AND created_at >= ? AND created_at < ?", siteID, start, end).Find(&withdrawals).Error; err != nil {
		return sheet, err
	}
	for _, row := range withdrawals {
		sheet.WithdrawalUSD = sheet.WithdrawalUSD.Add(row.AmountUSD)
		sheet.WithdrawalFeeUSD = sheet.WithdrawalFeeUSD.Add(row.FeeUSD)
		note(row.CreatedAt)
		sheet.IncludedCount++
	}

	var trades []string
	if err := tx.Model(&model.AdtyTopupOrder{}).Where("site_id = ?", siteID).Pluck("trade_no", &trades).Error; err != nil {
		return sheet, err
	}
	tradeSet := make(map[string]struct{}, len(trades))
	for _, trade := range trades {
		tradeSet[trade] = struct{}{}
	}
	var anomalies []model.AdtyAnomalyDisposition
	if err := tx.Where("created_at >= ? AND created_at < ?", start, end).Find(&anomalies).Error; err != nil {
		return sheet, err
	}
	siteRef := strconv.Itoa(siteID)
	for _, row := range anomalies {
		_, byTrade := tradeSet[row.Ref]
		if row.SiteId == siteID || (row.SiteId == 0 && (row.Ref == siteRef || byTrade)) {
			sheet.AnomalyCount++
			note(row.CreatedAt)
			sheet.IncludedCount++
		}
	}

	if len(paidByCurrency) > 0 {
		encoded := make(map[string]string, len(paidByCurrency))
		for currency, amount := range paidByCurrency {
			encoded[currency] = amount.String()
		}
		raw, err := common.Marshal(encoded)
		if err != nil {
			return sheet, err
		}
		sheet.PaidByCurrency = string(raw)
	}
	return sheet, nil
}

// AdtyBusinessTimezoneView uses Asia/Shanghai until Root saves a timezone.
type AdtyBusinessTimezoneView struct {
	Configured bool   `json:"configured"`
	Timezone   string `json:"timezone"`
}

// LoadAdtyBusinessTimezone reports the saved business-day timezone.
// An unset value uses Asia/Shanghai and is not presented as a saved zone.
func LoadAdtyBusinessTimezone() (AdtyBusinessTimezoneView, error) {
	raw, ok, err := model.ReadOptionFromDB(adtyBusinessTimezoneKey)
	if err != nil {
		return AdtyBusinessTimezoneView{}, err
	}
	name := strings.TrimSpace(raw)
	if !ok || name == "" {
		return AdtyBusinessTimezoneView{Timezone: defaultBusinessTimezone}, nil
	}
	return AdtyBusinessTimezoneView{Configured: true, Timezone: name}, nil
}

// ListAdtySiteSettlementSheets returns one site's sheets, newest day first.
func ListAdtySiteSettlementSheets(siteID int) ([]model.AdtySiteSettlementSheet, error) {
	if siteID <= 0 {
		return nil, common.NewMessage("Invalid parameters")
	}
	var rows []model.AdtySiteSettlementSheet
	err := model.DB.Where("site_id = ?", siteID).Order("business_day DESC, id DESC").Find(&rows).Error
	return rows, err
}

// ListAllAdtySiteSettlementSheets returns every site's sheets for Root.
func ListAllAdtySiteSettlementSheets() ([]model.AdtySiteSettlementSheet, error) {
	var rows []model.AdtySiteSettlementSheet
	err := model.DB.Order("business_day DESC, id DESC").Find(&rows).Error
	return rows, err
}

func init() {
	adtyMasterTasks = append(adtyMasterTasks, startAdtySiteSettlementSheetTask)
}

// startAdtySiteSettlementSheetTask refreshes yesterday's check sheet.
// It does not settle earnings or credit a wallet.
func startAdtySiteSettlementSheetTask() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for now := range ticker.C {
			if _, err := CatchUpAdtySiteSettlementSheets(now); err != nil {
				common.SysError(common.LogText("site settlement sheet: %s", err.Error()))
			}
		}
	}()
}
