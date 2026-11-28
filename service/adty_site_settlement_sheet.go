package service

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

func loadAdtyBusinessLocationTx(tx *gorm.DB) (*time.Location, string, error) {
	if tx == nil {
		return loadAdtyBusinessLocation()
	}
	name := defaultBusinessTimezone
	hasOptionTable, err := model.AdtyTableExists(tx, &model.Option{})
	if err != nil {
		return nil, "", err
	}
	if hasOptionTable {
		var row model.Option
		res := tx.Where(&model.Option{Key: adtyBusinessTimezoneKey}).Limit(1).Find(&row)
		if res.Error != nil {
			return nil, "", res.Error
		}
		if res.RowsAffected > 0 {
			if trimmed := strings.TrimSpace(row.Value); trimmed != "" {
				name = trimmed
			}
		}
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
			hasOptionTable, err := model.AdtyTableExists(model.DB, &model.Option{})
			if err != nil {
				return "", err
			}
			if hasOptionTable {
				_, name, err := loadAdtyBusinessLocation()
				if err != nil {
					return "", err
				}
				zone = name
			}
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
// A rerun appends a revision when its recorded facts change.
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
	day, start, end := previousBusinessWindow(now, loc)
	var sites []model.AdtySite
	if err := model.DB.Select("id").
		Where("status NOT IN ?", []string{model.AdtySiteStatusDeleting, model.AdtySiteStatusPending}).
		Find(&sites).Error; err != nil {
		return 0, err
	}
	written := 0
	var failures []error
	for _, site := range sites {
		count := 0
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			var locked model.AdtySite
			if err := model.AdtyLockForUpdate(tx).Select("id", "status", "created_at").First(&locked, site.Id).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			if locked.Status == model.AdtySiteStatusDeleting || locked.Status == model.AdtySiteStatusPending {
				return nil
			}
			scan := model.AdtySettlementScan{SiteId: site.Id}
			if err := tx.Where("site_id = ?", site.Id).Limit(1).Find(&scan).Error; err != nil {
				return err
			}
			var cursor time.Time
			var err error
			if scan.NextDay == "" {
				var hasPriorFacts bool
				cursor, hasPriorFacts, err = firstAdtySettlementDay(tx, site.Id, start, loc)
				if err != nil {
					return err
				}
				// R4-04: Do not generate an immutable zero-row settlement sheet for the day
				// BEFORE a brand-new site even existed when it has no historical facts.
				if !hasPriorFacts && locked.CreatedAt >= end.Unix() {
					return nil
				}
			} else {
				cursor, err = time.ParseInLocation("2006-01-02", scan.NextDay, loc)
			}
			if err != nil {
				return err
			}
			if cursor.After(start) {
				cursor = start
			}
			const daysPerRun = 31
			lastInclusive := cursor.AddDate(0, 0, daysPerRun-1)
			if start.Before(lastInclusive) {
				lastInclusive = start
			}
			var sealedDays []string
			if err := tx.Model(&model.AdtySiteSettlementSheet{}).Where("site_id = ? AND business_day BETWEEN ? AND ?", site.Id, cursor.Format("2006-01-02"), lastInclusive.Format("2006-01-02")).Pluck("business_day", &sealedDays).Error; err != nil {
				return err
			}
			sealed := make(map[string]bool, len(sealedDays))
			for _, existing := range sealedDays {
				sealed[existing] = true
			}
			for !cursor.After(lastInclusive) {
				current := cursor.Format("2006-01-02")
				next := cursor.AddDate(0, 0, 1)
				if !sealed[current] || current == day {
					if err := writeAdtyCatchUpDay(tx, site.Id, cursor, zone, now.Unix()); err != nil {
						return err
					}
					count++
				}
				cursor = next
			}
			// Keep yesterday available while older gaps are filled across runs.
			if !cursor.After(start) {
				if err := writeAdtyCatchUpDay(tx, site.Id, start, zone, now.Unix()); err != nil {
					return err
				}
				count++
			}
			scan.NextDay = cursor.Format("2006-01-02")
			scan.LastError = ""
			if err := tx.Save(&scan).Error; err != nil {
				return err
			}
			return revisitAdtySettlement(tx, site.Id, day, now.Unix())
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("site %d: %w", site.Id, err))
			state := model.AdtySettlementScan{SiteId: site.Id, LastError: err.Error()}
			if writeErr := model.DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "site_id"}}, DoUpdates: clause.AssignmentColumns([]string{"last_error"})}).Create(&state).Error; writeErr != nil {
				failures = append(failures, writeErr)
			}
			continue
		}
		written += count
	}
	return written, errors.Join(failures...)
}

func writeAdtyCatchUpDay(tx *gorm.DB, siteID int, date time.Time, zone string, generatedAt int64) error {
	day := date.Format("2006-01-02")
	next := date.AddDate(0, 0, 1)
	begin, end := date.Unix(), next.Unix()
	var adjacent []model.AdtySiteSettlementSheet
	if err := tx.Select("business_day", "day_start", "day_end").Where("site_id=? AND business_day IN ?", siteID, []string{date.AddDate(0, 0, -1).Format("2006-01-02"), next.Format("2006-01-02")}).Find(&adjacent).Error; err != nil {
		return err
	}
	for _, sealed := range adjacent {
		if sealed.BusinessDay < day {
			begin = sealed.DayEnd
		} else {
			end = sealed.DayStart
		}
	}
	if begin >= end {
		return fmt.Errorf("invalid settlement interval for site %d day %s", siteID, day)
	}
	return writeAdtySiteSettlementSheetTx(tx, siteID, day, begin, end, zone, generatedAt)
}

func firstAdtySettlementDay(tx *gorm.DB, siteID int, yesterday time.Time, loc *time.Location) (time.Time, bool, error) {
	var opened model.AdtySite
	if err := tx.Select("created_at").First(&opened, siteID).Error; err != nil {
		return time.Time{}, false, err
	}
	first := yesterday
	if opened.CreatedAt > 0 {
		d := time.Unix(opened.CreatedAt, 0).In(loc)
		d = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
		if d.Before(first) {
			first = d
		}
	}
	// Imported sites can have business facts predating the local site row.
	var ts int64
	if err := tx.Raw(`SELECT COALESCE(MIN(stamp),0) FROM (
	 SELECT MIN(t.complete_time) AS stamp FROM top_ups t JOIN adty_topup_orders o ON o.trade_no=t.trade_no WHERE o.site_id=? AND t.complete_time>0
	 UNION ALL SELECT MIN(created_at) FROM adty_site_wallet_ledgers WHERE site_id=? AND created_at>0
	 UNION ALL SELECT MIN(created_at) FROM adty_site_earning_events WHERE site_id=? AND created_at>0
	 UNION ALL SELECT MIN(created_at) FROM adty_site_invite_commissions WHERE site_id=? AND created_at>0
	 UNION ALL SELECT MIN(created_at) FROM adty_site_withdrawals WHERE site_id=? AND created_at>0
	 UNION ALL SELECT MIN(occurred_at) FROM adty_site_earning_facts WHERE site_id=? AND occurred_at>0
 UNION ALL SELECT MIN(created_at) FROM adty_site_trial_grants WHERE site_id=? AND created_at>0
 UNION ALL SELECT MIN(r.completed_at) FROM adty_manual_refunds r JOIN adty_topup_orders o ON o.trade_no=r.trade_no WHERE o.site_id=? AND r.completed_at>0
 UNION ALL SELECT MIN(created_at) FROM adty_anomaly_dispositions WHERE site_id=? AND created_at>0
 ) AS stamps`, siteID, siteID, siteID, siteID, siteID, siteID, siteID, siteID, siteID).Scan(&ts).Error; err != nil {
		return first, false, err
	}
	cutoff := yesterday.AddDate(0, 0, 1)
	hasPriorFacts := ts > 0 && ts < cutoff.Unix()
	if ts > 0 && ts < first.Unix() {
		d := time.Unix(ts, 0).In(loc)
		first = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	}
	var recorded string
	if err := tx.Model(&model.AdtySiteEarningDaily{}).Where("site_id = ? AND day <> ''", siteID).Select("COALESCE(MIN(day),'')").Scan(&recorded).Error; err != nil {
		return first, hasPriorFacts, err
	}
	if d, err := time.ParseInLocation("2006-01-02", adtyEarningDayOf(recorded), loc); err == nil {
		if d.Before(cutoff) {
			hasPriorFacts = true
		}
		if d.Before(first) {
			first = d
		}
	}
	return first, hasPriorFacts, nil
}

func revisitAdtySettlement(tx *gorm.DB, siteID int, day string, now int64) error {
	cursor := model.AdtySettlementScan{SiteId: siteID}
	if err := tx.Where("site_id = ?", siteID).Limit(1).Find(&cursor).Error; err != nil {
		return err
	}
	var row model.AdtySiteSettlementSheet
	found := tx.Where("site_id = ? AND business_day < ? AND id > ?", siteID, day, cursor.LastSheetId).Order("id").Limit(1).Find(&row)
	if found.Error != nil {
		return found.Error
	}
	if found.RowsAffected == 0 && cursor.LastSheetId == 0 {
		return nil
	}
	cursor.LastSheetId = row.Id
	if row.Id != 0 {
		if err := writeAdtySiteSettlementSheetTx(tx, siteID, row.BusinessDay, row.DayStart, row.DayEnd, row.Timezone, now); err != nil {
			return err
		}
	}
	return tx.Save(&cursor).Error
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
	var sources []string
	figures, err := summarizeAdtySiteSettlementTx(tx, siteID, day, windowStart, windowEnd, &sources)
	if err != nil {
		return err
	}
	figures.GeneratedAt = generatedAt
	if len(existing) == 1 {
		base := existing[0]
		previous := base
		var last model.AdtySettlementRevision
		res := tx.Where("sheet_id = ?", base.Id).Order("id DESC").Limit(1).Find(&last)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			if err := common.Unmarshal(last.After, &previous); err != nil {
				return err
			}
		}
		if figures.PendingCoverageUnverified {
			figures.PendingCoverageUSD = previous.PendingCoverageUSD
		}
		figures.Id, figures.SiteId, figures.BatchId, figures.BusinessDay = base.Id, siteID, base.BatchId, day
		figures.DayStart, figures.DayEnd, figures.Timezone = windowStart, windowEnd, zone
		figures.GeneratedAt = previous.GeneratedAt
		before, err := common.Marshal(previous)
		if err != nil {
			return err
		}
		after, err := common.Marshal(figures)
		if err != nil {
			return err
		}
		if bytes.Equal(before, after) {
			return nil
		}
		sort.Strings(sources)
		return tx.Create(&model.AdtySettlementRevision{SheetId: base.Id, Before: before, After: after, Sources: sources, CreatedAt: generatedAt}).Error
	}
	figures.SiteId = siteID
	figures.BusinessDay = day
	figures.Timezone = zone
	figures.DayStart = windowStart
	figures.DayEnd = windowEnd
	figures.BatchId = fmt.Sprintf("site-settlement:%d:%s", siteID, day)
	return tx.Create(&figures).Error
}

func summarizeAdtySiteSettlementTx(tx *gorm.DB, siteID int, day string, start, end int64, sources ...*[]string) (model.AdtySiteSettlementSheet, error) {
	sheet := model.AdtySiteSettlementSheet{PaidByCurrency: "{}"}
	record := func(kind string, id any) {
		if len(sources) > 0 {
			*sources[0] = append(*sources[0], fmt.Sprintf("%s/%v", kind, id))
		}
	}
	paidByCurrency := map[string]decimal.Decimal{}
	note := func(at int64) {
		if at > sheet.EventWatermark {
			sheet.EventWatermark = at
		}
	}

	var topups []model.TopUp
	if err := tx.Where("trade_no IN (?)", tx.Model(&model.AdtyTopupOrder{}).Select("trade_no").Where("site_id=?", siteID)).Where("status IN ? AND complete_time >= ? AND complete_time < ?", []string{common.TopUpStatusSuccess, model.TopUpStatusRefunded}, start, end).Find(&topups).Error; err != nil {
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
			record("topup", order.TradeNo)
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
			if order.IsOwnerMargin() {
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
	var refunds []model.AdtyManualRefund
	if err := tx.Where("trade_no IN (?) AND status = ? AND completed_at >= ? AND completed_at < ?", ownerTrades, model.AdtyManualRefundStatusCompleted, start, end).Find(&refunds).Error; err != nil {
		return sheet, err
	}
	for _, refund := range refunds {
		sheet.OwnerSelfUseRefundUSD = sheet.OwnerSelfUseRefundUSD.Add(refund.RequestUSD)
		sheet.IncludedCount++
		note(refund.CompletedAt)
		record("refund", refund.RefundId)
	}
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
		record("earning_event", ev.OperationKey)
		sheet.IncludedCount++
	}
	dayMatch := "(day = ? OR day LIKE ?)"
	legacyWindow := "((updated_at>=? AND updated_at<?) OR (updated_at=0 AND " + dayMatch + "))"
	allFacts := tx.Model(&model.AdtySiteEarningFact{}).Select("daily_id").Where("site_id=?", siteID)
	datedFacts := tx.Model(&model.AdtySiteEarningFact{}).Select("daily_id").Where("site_id=? AND occurred_at>=? AND occurred_at<?", siteID, start, end)
	undatedFacts := tx.Model(&model.AdtySiteEarningFact{}).Select("daily_id").Where("site_id=? AND occurred_at=0", siteID)
	var earnings []model.AdtySiteEarningDaily
	if err := tx.Where("site_id=? AND status<>? AND kind IN ?", siteID, model.AdtySiteEarningVoid, []string{model.AdtySiteEarningUsage, model.AdtySiteEarningRefund}).Where("id IN (?) OR (id IN (?) AND "+dayMatch+") OR (id NOT IN (?) AND "+legacyWindow+")", datedFacts, undatedFacts, day, day+"+%", allFacts, start, end, day, day+"+%").Find(&earnings).Error; err != nil {
		return sheet, err
	}
	factsByDaily := map[int64][]model.AdtySiteEarningFact{}
	var facts []model.AdtySiteEarningFact
	if err := tx.Where("site_id=? AND ((occurred_at>=? AND occurred_at<?) OR (occurred_at=0 AND daily_id IN (?)))", siteID, start, end, tx.Model(&model.AdtySiteEarningDaily{}).Select("id").Where("site_id=? AND "+dayMatch, siteID, day, day+"+%")).Find(&facts).Error; err != nil {
		return sheet, err
	}
	for _, f := range facts {
		factsByDaily[f.DailyId] = append(factsByDaily[f.DailyId], f)
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
				record("earning_fact", fact.Id)
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
			record("daily_earning", row.Id)
			sheet.SupplyCostUSD = sheet.SupplyCostUSD.Add(row.Platform)
			sheet.SpreadUSD = sheet.SpreadUSD.Add(row.Earning)
			if hasTS {
				note(row.UpdatedAt)
			}
			sheet.IncludedCount++
		}
	}

	var grants []model.AdtySiteTrialGrant
	if err := tx.Where("site_id=? AND ((created_at>=? AND created_at<?) OR (created_at=0 AND "+dayMatch+"))", siteID, start, end, day, day+"+%").Find(&grants).Error; err != nil {
		return sheet, err
	}
	for _, row := range grants {
		matched, hasTS := inAdtySheetWindow(row.CreatedAt, row.Day, day, start, end)
		if !matched {
			continue
		}
		record("trial_grant", row.Id)
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
		if row.Type == "adjustment" || row.Type == model.AdtyLedgerTypeManualAdjust ||
			(row.WalletKind != model.AdtySiteWalletOperating &&
				(row.Type == "financial_adjustment" || row.Type == "staff_balance" ||
					row.Type == "owner_customer_balance" || row.Type == "owner_team_balance")) {
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
			if spent.IsZero() && !row.HoldDelta.IsZero() {
				spent = row.HoldDelta.Neg()
			}
			if !spent.IsZero() {
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
		// R5-017: Classify all cash-moving earnings/margin ledger types so daily sheet
		// breakdowns reconcile cleanly against EarningsDeltaUSD and MarginDeltaUSD.
		if row.WalletKind == model.AdtySiteWalletEarnings || row.WalletKind == model.AdtySiteWalletMargin {
			switch row.Type {
			case "site_referral_earning":
				if row.WalletKind == model.AdtySiteWalletEarnings && !row.Delta.IsZero() {
					sheet.PartnerReferralEarningUSD = sheet.PartnerReferralEarningUSD.Add(row.Delta)
				}
			case "voucher_spend":
				if row.Delta.IsNegative() {
					sheet.VoucherFundingUSD = sheet.VoucherFundingUSD.Add(row.Delta.Neg())
				}
			case "earning_to_margin":
				if row.WalletKind == model.AdtySiteWalletMargin && row.Delta.IsPositive() {
					sheet.EarningToMarginUSD = sheet.EarningToMarginUSD.Add(row.Delta)
				}
			case "commission_deduct", "commission_closed_payout_deduct":
				if row.Delta.IsNegative() {
					sheet.CommissionPaidUSD = sheet.CommissionPaidUSD.Add(row.Delta.Neg())
				}
			case "commission_clawback_return":
				if row.Delta.IsPositive() {
					sheet.CommissionClawbackReturnUSD = sheet.CommissionClawbackReturnUSD.Add(row.Delta)
				}
			case "margin_topup", "margin_root_credit", "margin_cycle_open", "voucher_margin":
				if row.WalletKind == model.AdtySiteWalletMargin && !row.Delta.IsZero() {
					sheet.MarginTopupUSD = sheet.MarginTopupUSD.Add(row.Delta)
				}
			}
		}
		record("wallet_entry", row.Id)
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
	if err := tx.Where("site_id = ? AND created_at > 0 AND created_at < ? AND (created_at >= ? OR covered_at = 0 OR covered_at >= ?)", siteID, end, start, end).Find(&commissions).Error; err != nil {
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
		record("coverage_cancellation", event.Id)
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
		record("commission", row.Id)
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
	if err := tx.Where("site_id = ? AND status = ? AND paid_at >= ? AND paid_at < ?", siteID, model.AdtySiteWithdrawPaid, start, end).Find(&withdrawals).Error; err != nil {
		return sheet, err
	}
	for _, row := range withdrawals {
		sheet.WithdrawalUSD = sheet.WithdrawalUSD.Add(row.AmountUSD)
		record("withdrawal", row.Id)
		sheet.WithdrawalFeeUSD = sheet.WithdrawalFeeUSD.Add(row.FeeUSD)
		note(row.PaidAt)
		sheet.IncludedCount++
	}

	var anomalies []model.AdtyAnomalyDisposition
	if err := tx.Where("created_at>=? AND created_at<?", start, end).Where("site_id=? OR (site_id=0 AND (ref=? OR ref IN (?)))", siteID, strconv.Itoa(siteID), tx.Model(&model.AdtyTopupOrder{}).Select("trade_no").Where("site_id=?", siteID)).Find(&anomalies).Error; err != nil {
		return sheet, err
	}
	for _, row := range anomalies {
		sheet.AnomalyCount++
		record("anomaly", row.Id)
		note(row.CreatedAt)
		sheet.IncludedCount++
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
	err := model.DB.Preload("Revisions", func(db *gorm.DB) *gorm.DB { return db.Order("id") }).Where("site_id = ?", siteID).Order("business_day DESC, id DESC").Find(&rows).Error
	return rows, err
}

// ListAllAdtySiteSettlementSheets returns every site's sheets for Root.
func ListAllAdtySiteSettlementSheets() ([]model.AdtySiteSettlementSheet, error) {
	var rows []model.AdtySiteSettlementSheet
	err := model.DB.Preload("Revisions", func(db *gorm.DB) *gorm.DB { return db.Order("id") }).Order("business_day DESC, id DESC").Find(&rows).Error
	return rows, err
}

type SettlementSheetSummary struct {
	Id          int64  `json:"id"`
	SiteId      int    `json:"site_id"`
	BusinessDay string `json:"business_day"`
}

func scopedSettlementSheets(siteID int) *gorm.DB {
	query := model.DB.Model(&model.AdtySiteSettlementSheet{})
	if siteID > 0 {
		return query.Where("site_id = ?", siteID)
	}
	return query
}

func PageSettlementSheets(siteID int, day string, page *common.PageInfo) error {
	if page == nil || page.PageSize < 1 || page.Page < 1 || page.Page > math.MaxInt/page.PageSize {
		return common.NewMessage("Invalid parameters")
	}
	query := scopedSettlementSheets(siteID)
	if day != "" {
		if _, err := time.Parse("2006-01-02", day); err != nil {
			return common.NewMessage("Invalid parameters")
		}
		query = query.Where("business_day = ?", day)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return err
	}
	var rows []SettlementSheetSummary
	err := query.Select("id", "site_id", "business_day").Order("business_day DESC,id DESC").Offset(page.GetStartIdx()).Limit(page.PageSize).Find(&rows).Error
	page.SetItems(rows)
	page.SetTotal(int(total))
	return err
}

func GetSettlementSheet(siteID int, id int64) (model.AdtySiteSettlementSheet, error) {
	var row model.AdtySiteSettlementSheet
	ordered := func(db *gorm.DB) *gorm.DB { return db.Order("id") }
	err := scopedSettlementSheets(siteID).Preload("Revisions", ordered).Preload("LegacyCoverage", ordered).First(&row, id).Error
	return row, err
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
