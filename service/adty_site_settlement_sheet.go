package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
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
	loc, name, err := loadAdtyBusinessLocation()
	if err != nil {
		return 0, err
	}
	day, start, end := previousBusinessWindow(now, loc)
	return writeAdtySettlementDay(day, start.Unix(), end.Unix(), now.Unix(), name)
}

// CatchUpAdtySiteSettlementSheets fills business days after the latest saved sheet
// through yesterday, then refreshes yesterday. The first run only writes yesterday.
// An existing sheet keeps its timezone and window. No wallet is credited.
func CatchUpAdtySiteSettlementSheets(now time.Time) (int, error) {
	loc, name, err := loadAdtyBusinessLocation()
	if err != nil {
		return 0, err
	}
	yesterday, yStart, _ := previousBusinessWindow(now, loc)
	var sites []model.AdtySite
	if err := model.DB.Select("id").Find(&sites).Error; err != nil {
		return 0, err
	}
	written := 0
	for _, site := range sites {
		var latest []model.AdtySiteSettlementSheet
		if err := model.DB.Where("site_id = ?", site.Id).Order("business_day DESC").Limit(1).Find(&latest).Error; err != nil {
			return written, err
		}
		if len(latest) == 0 {
			continue
		}
		parsed, err := time.ParseInLocation("2006-01-02", latest[0].BusinessDay, time.UTC)
		if err != nil {
			return written, err
		}
		for cursor := parsed.AddDate(0, 0, 1); cursor.Format("2006-01-02") < yesterday; cursor = cursor.AddDate(0, 0, 1) {
			day := cursor.Format("2006-01-02")
			start := time.Date(cursor.Year(), cursor.Month(), cursor.Day(), 0, 0, 0, 0, loc)
			if err := writeAdtySiteSettlementSheet(site.Id, day, start.Unix(), start.AddDate(0, 0, 1).Unix(), name, now.Unix()); err != nil {
				return written, err
			}
			written++
		}
	}
	refreshed, err := writeAdtySettlementDay(yesterday, yStart.Unix(), yStart.AddDate(0, 0, 1).Unix(), now.Unix(), name)
	return written + refreshed, err
}

func writeAdtySettlementDay(day string, start, end, generatedAt int64, timezone string) (int, error) {
	var sites []model.AdtySite
	if err := model.DB.Select("id").Find(&sites).Error; err != nil {
		return 0, err
	}
	written := 0
	for _, site := range sites {
		if err := writeAdtySiteSettlementSheet(site.Id, day, start, end, timezone, generatedAt); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

func writeAdtySiteSettlementSheet(siteID int, day string, start, end int64, timezone string, generatedAt int64) error {
	var existing []model.AdtySiteSettlementSheet
	if err := model.DB.Where("site_id = ? AND business_day = ?", siteID, day).Limit(1).Find(&existing).Error; err != nil {
		return err
	}
	windowStart, windowEnd, zone := start, end, timezone
	if len(existing) == 1 {
		windowStart, windowEnd, zone = existing[0].DayStart, existing[0].DayEnd, existing[0].Timezone
	}
	figures, err := summarizeAdtySiteSettlement(siteID, day, windowStart, windowEnd)
	if err != nil {
		return err
	}
	figures.GeneratedAt = generatedAt
	if len(existing) == 1 {
		return model.DB.WithContext(model.WithAdtySettlementRefresh(context.Background())).
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
	return model.DB.Create(&figures).Error
}

func summarizeAdtySiteSettlement(siteID int, day string, start, end int64) (model.AdtySiteSettlementSheet, error) {
	sheet := model.AdtySiteSettlementSheet{PaidByCurrency: "{}"}
	paidByCurrency := map[string]decimal.Decimal{}
	note := func(at int64) {
		if at > sheet.EventWatermark {
			sheet.EventWatermark = at
		}
	}

	var topups []model.TopUp
	if err := model.DB.Where("status IN ? AND complete_time >= ? AND complete_time < ?", []string{common.TopUpStatusSuccess, model.TopUpStatusRefunded}, start, end).Find(&topups).Error; err != nil {
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
		if err := model.DB.Where("site_id = ? AND trade_no IN ?", siteID, trades).Find(&orders).Error; err != nil {
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

	ownerTrades := model.DB.Model(&model.AdtyTopupOrder{}).Select("trade_no").Where("site_id = ? AND business = ?", siteID, model.AdtyBusinessOwnerSelfUseTopup)
	var refunds struct {
		Amount decimal.Decimal
		Count  int64
		Latest int64
	}
	if err := model.DB.Model(&model.AdtyManualRefund{}).Where("trade_no IN (?) AND status = ? AND completed_at >= ? AND completed_at < ?", ownerTrades, model.AdtyManualRefundStatusCompleted, start, end).
		Select("COALESCE(SUM(request_usd),0) AS amount, COUNT(*) AS count, COALESCE(MAX(completed_at),0) AS latest").Scan(&refunds).Error; err != nil {
		return sheet, err
	}
	sheet.OwnerSelfUseRefundUSD = refunds.Amount
	sheet.IncludedCount += refunds.Count
	note(refunds.Latest)

	var events []model.AdtySiteEarningEvent
	if err := model.DB.Where("site_id = ? AND projected = ? AND created_at >= ? AND created_at < ?", siteID, true, start, end).Find(&events).Error; err != nil {
		return sheet, err
	}
	for _, ev := range events {
		if ev.ReviewStatus == model.AdtyEarningOriginalSkipped || ev.ReviewStatus == model.AdtyEarningNeedsReview {
			continue
		}
		revenue, platform, earning, err := adtyDurableEarningSplitTx(model.DB, &ev)
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
	if err := model.DB.Where("site_id = ?", siteID).Find(&facts).Error; err != nil {
		return sheet, err
	}
	factsByDaily := map[int64][]model.AdtySiteEarningFact{}
	for _, fact := range facts {
		factsByDaily[fact.DailyId] = append(factsByDaily[fact.DailyId], fact)
	}

	var earnings []model.AdtySiteEarningDaily
	if err := model.DB.Where("site_id = ? AND status <> ?", siteID, model.AdtySiteEarningVoid).Find(&earnings).Error; err != nil {
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
	if err := model.DB.Where("site_id = ?", siteID).Find(&grants).Error; err != nil {
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
	if err := model.DB.Where("site_id = ? AND created_at >= ? AND created_at < ?", siteID, start, end).Find(&ledgers).Error; err != nil {
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
	if err := model.DB.Where("site_id = ? AND created_at > 0 AND created_at < ?", siteID, end).Find(&commissions).Error; err != nil {
		return sheet, err
	}
	for _, row := range commissions {
		if row.CreatedAt >= start {
			sheet.CommissionUSD = sheet.CommissionUSD.Add(row.AmountUSD)
			note(row.CreatedAt)
			sheet.IncludedCount++
		}
		if row.Status == model.AdtySiteCommissionPendingCoverage {
			remainder := row.AmountUSD.Sub(row.ClawedUSD).Sub(row.CoveredUSD).Add(row.RecoveredUSD)
			if remainder.IsPositive() {
				sheet.PendingCoverageUSD = sheet.PendingCoverageUSD.Add(remainder)
			}
		}
	}

	var withdrawals []model.AdtySiteWithdrawal
	if err := model.DB.Where("site_id = ? AND created_at >= ? AND created_at < ?", siteID, start, end).Find(&withdrawals).Error; err != nil {
		return sheet, err
	}
	for _, row := range withdrawals {
		sheet.WithdrawalUSD = sheet.WithdrawalUSD.Add(row.AmountUSD)
		sheet.WithdrawalFeeUSD = sheet.WithdrawalFeeUSD.Add(row.FeeUSD)
		note(row.CreatedAt)
		sheet.IncludedCount++
	}

	var trades []string
	if err := model.DB.Model(&model.AdtyTopupOrder{}).Where("site_id = ?", siteID).Pluck("trade_no", &trades).Error; err != nil {
		return sheet, err
	}
	tradeSet := make(map[string]struct{}, len(trades))
	for _, trade := range trades {
		tradeSet[trade] = struct{}{}
	}
	var anomalies []model.AdtyAnomalyDisposition
	if err := model.DB.Where("created_at >= ? AND created_at < ?", start, end).Find(&anomalies).Error; err != nil {
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
