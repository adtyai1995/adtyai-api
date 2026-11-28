package service

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AdtyHourlySettlementSummary reports the totals settled by RunAdtyHourlySettlement.
type AdtyHourlySettlementSummary struct {
	HourSlot       string `json:"hour_slot"`
	SitesSettled   int    `json:"sites_settled"`
	PartnerSettled int    `json:"partner_settled"`
	MainRefSettled int    `json:"main_ref_settled"`
	SiteRefSettled int    `json:"site_ref_settled"`
}

// AdtySiteHourEstimate reports unsettled current-hour revenue, supply cost, and site spread.
type AdtySiteHourEstimate struct {
	PendingRevenueUSD  decimal.Decimal `json:"revenue_usd"`
	PendingPlatformUSD decimal.Decimal `json:"platform_usd"`
	PendingEarningUSD  decimal.Decimal `json:"earning_usd"`
}

var adtyHourlySettlementMu sync.Mutex

func init() {
	adtyMasterTasks = append(adtyMasterTasks, startAdtyHourlySettlementTask)
}

// applyAdtyCommissionOffsetTx applies pending negative carry-forward balance for (scope, ownerKey)
// against rawDelta under a FOR UPDATE row lock. PendingNegativeUSD is always stored >= 0.
func applyAdtyCommissionOffsetTx(tx *gorm.DB, scope, ownerKey string, rawDelta decimal.Decimal) (effective decimal.Decimal, offsetApplied decimal.Decimal, err error) {
	scope = strings.TrimSpace(scope)
	ownerKey = strings.TrimSpace(ownerKey)
	rawDelta = rawDelta.Round(8)
	if tx == nil || scope == "" || ownerKey == "" {
		return decimal.Zero, decimal.Zero, common.NewMessage("Invalid parameters")
	}
	if rawDelta.IsZero() {
		return decimal.Zero, decimal.Zero, nil
	}
	seed := model.AdtyCommissionOffset{
		Scope:              scope,
		OwnerKey:           ownerKey,
		PendingNegativeUSD: decimal.Zero,
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "scope"}, {Name: "owner_key"}},
		DoNothing: true,
	}).Create(&seed).Error; err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	var row model.AdtyCommissionOffset
	if err := model.AdtyLockForUpdate(tx).
		Where("scope = ? AND owner_key = ?", scope, ownerKey).
		First(&row).Error; err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	pending := row.PendingNegativeUSD.Round(8)
	if pending.IsNegative() {
		pending = decimal.Zero
	}
	effective = rawDelta.Sub(pending).Round(8)
	if effective.IsPositive() {
		offsetApplied = pending
		if !row.PendingNegativeUSD.IsZero() {
			if err := tx.Model(&model.AdtyCommissionOffset{}).
				Where("id = ?", row.Id).
				Update("pending_negative_usd", decimal.Zero).Error; err != nil {
				return decimal.Zero, decimal.Zero, err
			}
		}
		return effective, offsetApplied, nil
	}
	if rawDelta.IsPositive() {
		offsetApplied = rawDelta
	} else {
		offsetApplied = decimal.Zero
	}
	newPending := effective.Neg().Round(8)
	if newPending.IsNegative() {
		newPending = decimal.Zero
	}
	if !newPending.Equal(row.PendingNegativeUSD) {
		if err := tx.Model(&model.AdtyCommissionOffset{}).
			Where("id = ?", row.Id).
			Update("pending_negative_usd", newPending).Error; err != nil {
			return decimal.Zero, decimal.Zero, err
		}
	}
	return decimal.Zero, offsetApplied, nil
}

func adtyPartnerEligibleSupplyUSD(event *model.AdtySiteEarningEvent, platform decimal.Decimal) decimal.Decimal {
	if event == nil || event.Quota == 0 || platform.IsZero() {
		return decimal.Zero
	}
	if event.PartnerExcludedQuota == 0 {
		return platform.Round(8)
	}
	absQuota := int64(event.Quota)
	if absQuota < 0 {
		absQuota = -absQuota
	}
	absExcl := event.PartnerExcludedQuota
	if absExcl < 0 {
		absExcl = -absExcl
	}
	if absExcl >= absQuota {
		return decimal.Zero
	}
	eligibleAbs := absQuota - absExcl
	return platform.Mul(decimal.NewFromInt(eligibleAbs)).Div(decimal.NewFromInt(absQuota)).Round(8)
}

type adtyHourlySlotSpec struct {
	HourSlot    string
	WindowStart int64
	WindowEnd   int64
}

func adtyDiscoverPendingHourSlots(now time.Time, loc *time.Location) (latestSlot string, slots []adtyHourlySlotSpec, err error) {
	localNow := now.In(loc)
	currentHourStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), localNow.Hour(), 0, 0, 0, loc)
	latestStart := currentHourStart.Add(-time.Hour)
	latestSlot = latestStart.Format("2006-01-02T15")
	cutoffUnix := currentHourStart.Unix()

	bySlot := map[string]adtyHourlySlotSpec{
		latestSlot: {
			HourSlot:    latestSlot,
			WindowStart: latestStart.Unix(),
			WindowEnd:   cutoffUnix,
		},
	}
	addTimestamp := func(ts int64) {
		if ts <= 0 || ts >= cutoffUnix {
			return
		}
		t := time.Unix(ts, 0).In(loc)
		hStart := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc)
		hEnd := hStart.Add(time.Hour)
		if hEnd.Unix() > cutoffUnix {
			hEnd = currentHourStart
		}
		slot := hStart.Format("2006-01-02T15")
		if existing, ok := bySlot[slot]; ok {
			if hEnd.Unix() > existing.WindowEnd {
				existing.WindowEnd = hEnd.Unix()
				bySlot[slot] = existing
			}
			return
		}
		bySlot[slot] = adtyHourlySlotSpec{
			HourSlot:    slot,
			WindowStart: hStart.Unix(),
			WindowEnd:   hEnd.Unix(),
		}
	}

	if ok, tblErr := model.AdtyTableExists(model.DB, &model.AdtyOperatingPrincipalEvent{}); tblErr != nil {
		return latestSlot, nil, tblErr
	} else if ok {
		var tsList []int64
		if err := model.DB.Model(&model.AdtyOperatingPrincipalEvent{}).
			Where("settled = ? AND created_at < ?", false, cutoffUnix).
			Pluck("created_at", &tsList).Error; err != nil {
			return latestSlot, nil, err
		}
		for _, ts := range tsList {
			addTimestamp(ts)
		}
	}
	if ok, tblErr := model.AdtyTableExists(model.DB, &model.AdtySiteEarningEvent{}); tblErr != nil {
		return latestSlot, nil, tblErr
	} else if ok {
		var tsList []int64
		if err := model.DB.Model(&model.AdtySiteEarningEvent{}).
			Where("projected = ? AND created_at < ? AND (review_status <> ? OR review_status IS NULL)",
				false, cutoffUnix, model.AdtyEarningNeedsReview).
			Pluck("created_at", &tsList).Error; err != nil {
			return latestSlot, nil, err
		}
		for _, ts := range tsList {
			addTimestamp(ts)
		}
	}
	if ok, tblErr := model.AdtyTableExists(model.DB, &model.AdtyPrincipalConsumeEvent{}); tblErr != nil {
		return latestSlot, nil, tblErr
	} else if ok {
		var tsList []int64
		if err := model.DB.Model(&model.AdtyPrincipalConsumeEvent{}).
			Where("settled = ? AND created_at < ?", false, cutoffUnix).
			Pluck("created_at", &tsList).Error; err != nil {
			return latestSlot, nil, err
		}
		for _, ts := range tsList {
			addTimestamp(ts)
		}
	}

	completed := map[string]bool{}
	hasRunTable, tblErr := model.AdtyTableExists(model.DB, &model.AdtyHourlySettlementRun{})
	if tblErr != nil {
		return latestSlot, nil, tblErr
	}
	if hasRunTable && len(bySlot) > 0 {
		slotNames := make([]string, 0, len(bySlot))
		for s := range bySlot {
			slotNames = append(slotNames, s)
		}
		var doneRuns []string
		if err := model.DB.Model(&model.AdtyHourlySettlementRun{}).
			Where("hour_slot IN ? AND status = ?", slotNames, "completed").
			Pluck("hour_slot", &doneRuns).Error; err != nil {
			return latestSlot, nil, err
		}
		for _, s := range doneRuns {
			completed[s] = true
		}
	}

	slots = make([]adtyHourlySlotSpec, 0, len(bySlot))
	for name, spec := range bySlot {
		if completed[name] {
			continue
		}
		slots = append(slots, spec)
	}
	sort.Slice(slots, func(i, j int) bool {
		if slots[i].WindowEnd != slots[j].WindowEnd {
			return slots[i].WindowEnd < slots[j].WindowEnd
		}
		return slots[i].HourSlot < slots[j].HourSlot
	})
	return latestSlot, slots, nil
}

type adtySiteHourStep1Result struct {
	Settled          bool
	ReferrerSiteID   int
	ReferrerStopped  bool
	PartnerSupplyUSD decimal.Decimal
}

func settleAdtySiteHourStep1(siteID int, hourSlot string, windowEnd int64, nowUnix int64) (adtySiteHourStep1Result, error) {
	var res adtySiteHourStep1Result
	if siteID <= 0 || model.DB == nil {
		return res, nil
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		// Canonical lock order:
		// AdtySite -> AdtySiteWallet(earnings -> operating) -> Events / Daily -> AdtyCommissionOffset.
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteID).First(&site).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		res.ReferrerSiteID = site.ReferrerSiteId
		res.ReferrerStopped = site.ReferrerStoppedAt > 0

		if err := preLockAdtySiteWalletsTx(tx, siteID, model.AdtySiteWalletEarnings, model.AdtySiteWalletOperating); err != nil {
			return err
		}

		hasOpTable, err := model.AdtyTableExists(tx, &model.AdtyOperatingPrincipalEvent{})
		if err != nil {
			return err
		}
		var opEvents []model.AdtyOperatingPrincipalEvent
		if hasOpTable {
			if err := model.AdtyLockForUpdate(tx).
				Where("site_id = ? AND settled = ? AND created_at < ?", siteID, false, windowEnd).
				Order("id ASC").
				Find(&opEvents).Error; err != nil {
				return err
			}
		}

		hasEarnTable, err := model.AdtyTableExists(tx, &model.AdtySiteEarningEvent{})
		if err != nil {
			return err
		}
		var earningEvents []model.AdtySiteEarningEvent
		if hasEarnTable {
			if err := model.AdtyLockForUpdate(tx).
				Where("site_id = ? AND projected = ? AND created_at < ? AND (review_status <> ? OR review_status IS NULL)",
					siteID, false, windowEnd, model.AdtyEarningNeedsReview).
				Order("created_at ASC, operation_key ASC").
				Find(&earningEvents).Error; err != nil {
				return err
			}
		}

		if len(opEvents) == 0 && len(earningEvents) == 0 {
			return nil
		}

		// 1a) Aggregate and settle operating principal movements for this site.
		if len(opEvents) > 0 {
			var netOpQuota int64
			opIDs := make([]int64, 0, len(opEvents))
			for _, ev := range opEvents {
				netOpQuota += ev.Quota
				opIDs = append(opIDs, ev.Id)
			}
			if netOpQuota != 0 {
				netOpUSD := decimal.NewFromInt(netOpQuota).Div(common.QuotaPerUnitDecimal()).Round(8)
				if !netOpUSD.IsZero() {
					changeType := "principal_refund"
					if netOpUSD.IsNegative() {
						changeType = "principal_consume"
					}
					postDelta := netOpUSD
					if postDelta.IsNegative() {
						var opWallet model.AdtySiteWallet
						if err := model.AdtyLockForUpdate(tx).
							Where("site_id = ? AND kind = ?", siteID, model.AdtySiteWalletOperating).
							First(&opWallet).Error; err != nil {
							return err
						}
						availOp := opWallet.Balance.Sub(opWallet.FrozenUSD).Sub(opWallet.ExcessFrozenUSD).Round(8)
						if availOp.IsNegative() {
							availOp = decimal.Zero
						}
						if postDelta.Abs().GreaterThan(availOp) {
							common.SysError(common.LogText(
								"hourly settlement operating drift capped site=%d slot=%s requested=%s available=%s",
								siteID, hourSlot, postDelta.String(), availOp.String(),
							))
							if availOp.IsPositive() {
								postDelta = availOp.Neg()
							} else {
								postDelta = decimal.Zero
							}
						}
					}
					if !postDelta.IsZero() {
						if _, err := applyAdtySiteWalletChangeTx(tx, siteID, model.AdtySiteWalletOperating, postDelta, AdtySiteWalletChange{
							Type:           changeType,
							RefType:        "hourly_settlement",
							RefID:          hourSlot,
							Reason:         fmt.Sprintf("hourly operating principal %s", hourSlot),
							IdempotencyKey: fmt.Sprintf("operating:hourly:%d:%s", siteID, hourSlot),
							AllowOverdraft: false,
						}); err != nil {
							return err
						}
					}
				}
			}
			if err := tx.Model(&model.AdtyOperatingPrincipalEvent{}).
				Where("id IN ?", opIDs).
				Updates(map[string]any{"settled": true, "hour_slot": hourSlot}).Error; err != nil {
				return err
			}
		}

		// 1b) Project eligible earning events and aggregate site spread + partner supply base.
		totalSpreadUSD := decimal.Zero
		totalPartnerSupplyUSD := decimal.Zero
		for i := range earningEvents {
			ev := &earningEvents[i]
			_, platform, earning, kind, err := projectAdtySiteEarningDailyAndLineTx(tx, ev, hourSlot, nowUnix)
			if err != nil {
				return err
			}
			if kind != model.AdtySiteEarningSkipped {
				totalSpreadUSD = totalSpreadUSD.Add(earning)
			}
			if site.ReferrerSiteId > 0 && site.ReferrerStoppedAt == 0 &&
				ev.ReviewStatus != model.AdtyEarningOriginalSkipped &&
				ev.CreatedAt >= site.ReferrerBoundAt {
				totalPartnerSupplyUSD = totalPartnerSupplyUSD.Add(adtyPartnerEligibleSupplyUSD(ev, platform))
			}
		}
		totalSpreadUSD = totalSpreadUSD.Round(8)
		totalPartnerSupplyUSD = totalPartnerSupplyUSD.Round(8)
		res.PartnerSupplyUSD = totalPartnerSupplyUSD

		// 1c) Apply site_earning negative carry-forward offset and post 1 aggregated earnings ledger.
		if len(earningEvents) > 0 && !totalSpreadUSD.IsZero() {
			ownerKey := fmt.Sprintf("site:%d", siteID)
			seed := model.AdtyCommissionOffset{
				Scope:              model.AdtyCommissionOffsetScopeSiteEarning,
				OwnerKey:           ownerKey,
				PendingNegativeUSD: decimal.Zero,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "scope"}, {Name: "owner_key"}},
				DoNothing: true,
			}).Create(&seed).Error; err != nil {
				return err
			}
			var offsetRow model.AdtyCommissionOffset
			if err := model.AdtyLockForUpdate(tx).
				Where("scope = ? AND owner_key = ?", model.AdtyCommissionOffsetScopeSiteEarning, ownerKey).
				First(&offsetRow).Error; err != nil {
				return err
			}
			pendingNeg := offsetRow.PendingNegativeUSD.Round(8)
			if pendingNeg.IsNegative() {
				pendingNeg = decimal.Zero
			}
			effective := totalSpreadUSD.Sub(pendingNeg).Round(8)
			idemKey := fmt.Sprintf("site-earning-hourly:%d:%s", siteID, hourSlot)
			var newPending decimal.Decimal
			switch {
			case effective.IsPositive():
				if _, err := applyAdtySiteWalletChangeTx(tx, siteID, model.AdtySiteWalletEarnings, effective, AdtySiteWalletChange{
					Type:           "site_earning_settlement",
					RefType:        "site_earning",
					RefID:          hourSlot,
					Reason:         fmt.Sprintf("hourly site earnings %s", hourSlot),
					IdempotencyKey: idemKey,
					AllowOverdraft: true,
				}); err != nil {
					return err
				}
				newPending = decimal.Zero
			case effective.IsZero():
				newPending = decimal.Zero
			default:
				deficit := effective.Neg().Round(8)
				var earnWallet model.AdtySiteWallet
				if err := model.AdtyLockForUpdate(tx).
					Where("site_id = ? AND kind = ?", siteID, model.AdtySiteWalletEarnings).
					First(&earnWallet).Error; err != nil {
					return err
				}
				avail := earnWallet.Balance.Sub(earnWallet.FrozenUSD).Sub(earnWallet.ExcessFrozenUSD).Round(8)
				if avail.IsNegative() {
					avail = decimal.Zero
				}
				deduct := decimal.Min(avail, deficit).Round(8)
				if deduct.IsPositive() {
					if _, err := applyAdtySiteWalletChangeTx(tx, siteID, model.AdtySiteWalletEarnings, deduct.Neg(), AdtySiteWalletChange{
						Type:           "site_earning_settlement",
						RefType:        "site_earning",
						RefID:          hourSlot,
						Reason:         fmt.Sprintf("hourly site earnings %s", hourSlot),
						IdempotencyKey: idemKey,
						AllowOverdraft: false,
					}); err != nil {
						return err
					}
				}
				newPending = deficit.Sub(deduct).Round(8)
				if newPending.IsNegative() {
					newPending = decimal.Zero
				}
			}
			if !newPending.Equal(offsetRow.PendingNegativeUSD) {
				if err := tx.Model(&model.AdtyCommissionOffset{}).
					Where("id = ?", offsetRow.Id).
					Update("pending_negative_usd", newPending).Error; err != nil {
					return err
				}
			}
		}

		res.Settled = true
		return nil
	})
	if err != nil {
		return res, err
	}
	if res.Settled {
		nowTime := time.Unix(nowUnix, 0)
		if _, resumeErr := ResumePendingAdtyGiftFulfillments(siteID); resumeErr != nil {
			common.SysError(common.LogText("hourly settlement resume gift fulfillments failed site=%d: %s", siteID, resumeErr.Error()))
		}
		if resumeErr := ResumeAdtySiteInviteCoverage(siteID, nowTime); resumeErr != nil {
			common.SysError(common.LogText("hourly settlement resume invite coverage failed site=%d: %s", siteID, resumeErr.Error()))
		}
	}
	return res, nil
}

func adtyListPendingStep1SiteIDs(windowEnd int64) ([]int, error) {
	seen := map[int]struct{}{}
	if ok, err := model.AdtyTableExists(model.DB, &model.AdtyOperatingPrincipalEvent{}); err != nil {
		return nil, err
	} else if ok {
		var ids []int
		if err := model.DB.Model(&model.AdtyOperatingPrincipalEvent{}).
			Distinct("site_id").
			Where("site_id > 0 AND settled = ? AND created_at < ?", false, windowEnd).
			Pluck("site_id", &ids).Error; err != nil {
			return nil, err
		}
		for _, id := range ids {
			if id > 0 {
				seen[id] = struct{}{}
			}
		}
	}
	if ok, err := model.AdtyTableExists(model.DB, &model.AdtySiteEarningEvent{}); err != nil {
		return nil, err
	} else if ok {
		var ids []int
		if err := model.DB.Model(&model.AdtySiteEarningEvent{}).
			Distinct("site_id").
			Where("site_id > 0 AND projected = ? AND created_at < ? AND (review_status <> ? OR review_status IS NULL)",
				false, windowEnd, model.AdtyEarningNeedsReview).
			Pluck("site_id", &ids).Error; err != nil {
			return nil, err
		}
		for _, id := range ids {
			if id > 0 {
				seen[id] = struct{}{}
			}
		}
	}
	out := make([]int, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Ints(out)
	return out, nil
}

// RunAdtyHourlySettlement settles all completed hours before the current hour in business timezone,
// executing Steps 1 -> 2 -> 3 -> 4 in strict order and recording AdtyHourlySettlementRun rows.
func RunAdtyHourlySettlement(now time.Time) (*AdtyHourlySettlementSummary, error) {
	if model.DB == nil {
		return &AdtyHourlySettlementSummary{}, nil
	}
	adtyHourlySettlementMu.Lock()
	defer adtyHourlySettlementMu.Unlock()

	loc, _, err := loadAdtyBusinessLocation()
	if err != nil {
		return nil, err
	}
	latestSlot, slots, err := adtyDiscoverPendingHourSlots(now, loc)
	if err != nil {
		return nil, err
	}
	summary := &AdtyHourlySettlementSummary{HourSlot: latestSlot}
	if len(slots) == 0 {
		return summary, nil
	}

	hasRunTable, err := model.AdtyTableExists(model.DB, &model.AdtyHourlySettlementRun{})
	if err != nil {
		return summary, err
	}

	nowUnix := now.Unix()
	var firstErr error

	for _, slot := range slots {
		if hasRunTable {
			runRow := model.AdtyHourlySettlementRun{
				HourSlot:    slot.HourSlot,
				WindowStart: slot.WindowStart,
				WindowEnd:   slot.WindowEnd,
				Status:      "running",
			}
			if err := model.DB.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "hour_slot"}},
				DoUpdates: clause.AssignmentColumns([]string{"window_start", "window_end", "status"}),
			}).Create(&runRow).Error; err != nil {
				common.SysError(common.LogText("hourly settlement run start failed slot=%s: %s", slot.HourSlot, err.Error()))
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
		}

		slotSiteCount := 0
		slotPartnerCount := 0
		slotMainRefCount := 0
		slotSiteRefCount := 0
		var slotErr error

		// Step 1 & Step 2: per subsite operating + earnings spread, then partner commission.
		siteIDs, listErr := adtyListPendingStep1SiteIDs(slot.WindowEnd)
		if listErr != nil {
			slotErr = listErr
			common.SysError(common.LogText("hourly settlement list sites failed slot=%s: %s", slot.HourSlot, listErr.Error()))
		} else {
			for _, siteID := range siteIDs {
				step1Res, step1Err := settleAdtySiteHourStep1(siteID, slot.HourSlot, slot.WindowEnd, nowUnix)
				if step1Err != nil {
					common.SysError(common.LogText("hourly settlement step1 failed site=%d slot=%s: %s", siteID, slot.HourSlot, step1Err.Error()))
					if slotErr == nil {
						slotErr = step1Err
					}
					continue
				}
				if step1Res.Settled {
					slotSiteCount++
				}
				// Step 2: partner referral commission from B's eligible supply to referrer site A.
				if step1Res.ReferrerSiteID > 0 && !step1Res.ReferrerStopped && !step1Res.PartnerSupplyUSD.IsZero() {
					partnerOK, partnerErr := settleSitePartnerHourForSite(
						step1Res.ReferrerSiteID, siteID, slot.HourSlot, step1Res.PartnerSupplyUSD, nowUnix,
					)
					if partnerErr != nil {
						common.SysError(common.LogText(
							"hourly settlement step2 partner failed referrer=%d referred=%d slot=%s: %s",
							step1Res.ReferrerSiteID, siteID, slot.HourSlot, partnerErr.Error(),
						))
						if slotErr == nil {
							slotErr = partnerErr
						}
					} else if partnerOK {
						slotPartnerCount++
					}
				}
			}
		}

		// Step 3: main-site referral consumption commissions.
		mainCount, mainErr := settleMainReferralConsumeHourTx(slot.HourSlot, slot.WindowEnd, nowUnix)
		if mainErr != nil {
			common.SysError(common.LogText("hourly settlement step3 main referral failed slot=%s: %s", slot.HourSlot, mainErr.Error()))
			if slotErr == nil {
				slotErr = mainErr
			}
		}
		slotMainRefCount = mainCount

		// Step 4: subsite customer invite consumption commissions.
		siteRefCount, siteRefErr := settleSiteInviteConsumeHourTx(slot.HourSlot, slot.WindowEnd, nowUnix, 0)
		if siteRefErr != nil {
			common.SysError(common.LogText("hourly settlement step4 site invite failed slot=%s: %s", slot.HourSlot, siteRefErr.Error()))
			if slotErr == nil {
				slotErr = siteRefErr
			}
		}
		slotSiteRefCount = siteRefCount

		summary.SitesSettled += slotSiteCount
		summary.PartnerSettled += slotPartnerCount
		summary.MainRefSettled += slotMainRefCount
		summary.SiteRefSettled += slotSiteRefCount

		if slotErr != nil && firstErr == nil {
			firstErr = slotErr
		}
		if hasRunTable {
			status := "completed"
			if slotErr != nil {
				status = "failed"
			}
			if err := model.DB.Model(&model.AdtyHourlySettlementRun{}).
				Where("hour_slot = ?", slot.HourSlot).
				Updates(map[string]any{
					"status":         status,
					"site_count":     gorm.Expr("site_count + ?", slotSiteCount),
					"partner_count":  gorm.Expr("partner_count + ?", slotPartnerCount),
					"main_ref_count": gorm.Expr("main_ref_count + ?", slotMainRefCount),
					"site_ref_count": gorm.Expr("site_ref_count + ?", slotSiteRefCount),
					"finished_at":    time.Now().Unix(),
				}).Error; err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}

	return summary, firstErr
}

// FlushAdtySiteBeforeClose immediately flushes all unsettled operating events, earning events,
// partner referral commissions (both as referred site B paying A and for all child sites B1..Bn
// paying A), and site invite commissions for siteID right before the site closes.
func FlushAdtySiteBeforeClose(siteID int, now int64) error {
	if siteID <= 0 || model.DB == nil {
		return nil
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	adtyHourlySettlementMu.Lock()
	defer adtyHourlySettlementMu.Unlock()

	slot := fmt.Sprintf("close-%d-%d", siteID, now)
	windowEnd := now + 1
	var firstErr error

	// 1. Flush siteID itself (operating + earning spread + partner commission paid to its referrer A).
	step1Res, err := settleAdtySiteHourStep1(siteID, slot, windowEnd, now)
	if err != nil {
		firstErr = err
	} else if step1Res.ReferrerSiteID > 0 && !step1Res.ReferrerStopped && !step1Res.PartnerSupplyUSD.IsZero() {
		if _, pErr := settleSitePartnerHourForSite(
			step1Res.ReferrerSiteID, siteID, slot, step1Res.PartnerSupplyUSD, now,
		); pErr != nil && firstErr == nil {
			firstErr = pErr
		}
	}

	// 2. Flush all child sites B1..Bn referred by siteID so siteID collects partner commission up to close.
	var childIDs []int
	if err := model.DB.Model(&model.AdtySite{}).
		Where("referrer_site_id = ? AND referrer_stopped_at = 0 AND id <> ?", siteID, siteID).
		Order("id ASC").
		Pluck("id", &childIDs).Error; err != nil {
		if firstErr == nil {
			firstErr = err
		}
	} else {
		for _, childID := range childIDs {
			childRes, cErr := settleAdtySiteHourStep1(childID, slot, windowEnd, now)
			if cErr != nil {
				common.SysError(common.LogText("close flush child site=%d for referrer=%d failed: %s", childID, siteID, cErr.Error()))
				if firstErr == nil {
					firstErr = cErr
				}
				continue
			}
			if childRes.ReferrerSiteID == siteID && !childRes.ReferrerStopped && !childRes.PartnerSupplyUSD.IsZero() {
				if _, pErr := settleSitePartnerHourForSite(
					siteID, childID, slot, childRes.PartnerSupplyUSD, now,
				); pErr != nil {
					common.SysError(common.LogText("close flush partner payout referrer=%d child=%d failed: %s", siteID, childID, pErr.Error()))
					if firstErr == nil {
						firstErr = pErr
					}
				}
			}
		}
	}

	// 3. Flush siteID's subsite customer invite commissions up to now.
	if _, invErr := settleSiteInviteConsumeHourTx(slot, windowEnd, now, siteID); invErr != nil {
		common.SysError(common.LogText("close flush site invite commissions failed site=%d: %s", siteID, invErr.Error()))
		if firstErr == nil {
			firstErr = invErr
		}
	}
	if resumeErr := ResumeAdtySiteInviteCoverage(siteID, time.Unix(now, 0)); resumeErr != nil {
		common.SysError(common.LogText("close flush resume invite coverage failed site=%d: %s", siteID, resumeErr.Error()))
	}

	return firstErr
}

// startAdtyHourlySettlementTask runs on the master node, ticking every minute and triggering
// RunAdtyHourlySettlement on startup (after 15s delay) and whenever the hour rolls over in bizLoc.
func startAdtyHourlySettlementTask() {
	go func() {
		time.Sleep(15 * time.Second)
		lastRunSlot := ""
		run := func(now time.Time) {
			loc, _, err := loadAdtyBusinessLocation()
			if err != nil {
				common.SysError(common.LogText("hourly settlement: %s", err.Error()))
				return
			}
			localNow := now.In(loc)
			currentHourStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), localNow.Hour(), 0, 0, 0, loc)
			targetSlot := currentHourStart.Add(-time.Hour).Format("2006-01-02T15")
			if lastRunSlot == targetSlot {
				return
			}
			if _, err := RunAdtyHourlySettlement(now); err != nil {
				common.SysError(common.LogText("hourly settlement slot=%s: %s", targetSlot, err.Error()))
				return
			}
			lastRunSlot = targetSlot
		}
		run(time.Now())
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for now := range ticker.C {
			run(now)
		}
	}()
}

// GetAdtySiteCurrentHourEstimate returns the real-time unsettled revenue, supply cost, and site earning spread.
func GetAdtySiteCurrentHourEstimate(siteID int) (AdtySiteHourEstimate, error) {
	return getAdtySiteCurrentHourEstimateScoped(siteID, 0)
}

func getAdtySiteCurrentHourEstimateScoped(siteID, ownerID int) (AdtySiteHourEstimate, error) {
	var est AdtySiteHourEstimate
	if siteID <= 0 || model.DB == nil {
		return est, nil
	}
	hasTable, err := model.AdtyTableExists(model.DB, &model.AdtySiteEarningEvent{})
	if err != nil || !hasTable {
		return est, err
	}
	q := model.DB.Where("site_id = ? AND projected = ? AND (review_status <> ? OR review_status IS NULL)",
		siteID, false, model.AdtyEarningNeedsReview)
	if ownerID > 0 {
		q = q.Scopes(model.AdtyFinancialOwnerScope(siteID, ownerID))
	}
	var events []model.AdtySiteEarningEvent
	if err := q.Find(&events).Error; err != nil {
		return est, err
	}
	for i := range events {
		ev := &events[i]
		revenue, platform, earning, err := adtyDurableEarningSplitTx(model.DB, ev)
		if err != nil {
			return est, err
		}
		est.PendingRevenueUSD = est.PendingRevenueUSD.Add(revenue)
		est.PendingPlatformUSD = est.PendingPlatformUSD.Add(platform)
		if ev.ReviewStatus != model.AdtyEarningOriginalSkipped {
			est.PendingEarningUSD = est.PendingEarningUSD.Add(earning)
		}
	}
	est.PendingRevenueUSD = est.PendingRevenueUSD.Round(8)
	est.PendingPlatformUSD = est.PendingPlatformUSD.Round(8)
	est.PendingEarningUSD = est.PendingEarningUSD.Round(8)
	return est, nil
}

func adtyReadCommissionOffsetPending(scope, ownerKey string) (decimal.Decimal, error) {
	if model.DB == nil || scope == "" || ownerKey == "" {
		return decimal.Zero, nil
	}
	hasTable, err := model.AdtyTableExists(model.DB, &model.AdtyCommissionOffset{})
	if err != nil || !hasTable {
		return decimal.Zero, err
	}
	var row model.AdtyCommissionOffset
	res := model.DB.Select("pending_negative_usd").
		Where("scope = ? AND owner_key = ?", scope, ownerKey).
		Limit(1).
		Find(&row)
	if res.Error != nil {
		return decimal.Zero, res.Error
	}
	if res.RowsAffected == 0 || !row.PendingNegativeUSD.IsPositive() {
		return decimal.Zero, nil
	}
	return row.PendingNegativeUSD.Round(8), nil
}

// GetAdtySitePartnerPendingHourUSD returns the estimated partner referral commission pending
// hourly settlement for referrerSiteID (total and per referred site).
func GetAdtySitePartnerPendingHourUSD(referrerSiteID int) (decimal.Decimal, map[int]decimal.Decimal, error) {
	bySite := make(map[int]decimal.Decimal)
	if referrerSiteID <= 0 || model.DB == nil {
		return decimal.Zero, bySite, nil
	}
	hasEventTable, err := model.AdtyTableExists(model.DB, &model.AdtySiteEarningEvent{})
	if err != nil || !hasEventTable {
		return decimal.Zero, bySite, err
	}
	var referrerSite model.AdtySite
	res := model.DB.Select("id", "status").Where("id = ?", referrerSiteID).Limit(1).Find(&referrerSite)
	if res.Error != nil {
		return decimal.Zero, bySite, res.Error
	}
	if res.RowsAffected == 0 || referrerSite.Status == model.AdtySiteStatusClosed || referrerSite.Status == model.AdtySiteStatusDeleting {
		return decimal.Zero, bySite, nil
	}

	var referredSites []model.AdtySite
	if err := model.DB.Select("id", "referrer_rate", "referrer_bound_at").
		Where("referrer_site_id = ? AND referrer_stopped_at = 0 AND id <> ?", referrerSiteID, referrerSiteID).
		Find(&referredSites).Error; err != nil {
		return decimal.Zero, bySite, err
	}
	if len(referredSites) == 0 {
		return decimal.Zero, bySite, nil
	}

	defaultRate := decimal.NewFromInt(5)
	if cfg := adty_setting.GetResellerSetting(); cfg != nil && cfg.PartnerReferralDefaultRate != nil {
		defaultRate = decimal.NewFromFloat(*cfg.PartnerReferralDefaultRate)
	}

	total := decimal.Zero
	for _, child := range referredSites {
		ratePercent := defaultRate
		if child.ReferrerRate != nil {
			ratePercent = *child.ReferrerRate
		}
		if !ratePercent.IsPositive() {
			continue
		}
		q := model.DB.Where("site_id = ? AND projected = ? AND (review_status IS NULL OR review_status NOT IN ?)",
			child.Id, false, []string{model.AdtyEarningNeedsReview, model.AdtyEarningOriginalSkipped})
		if child.ReferrerBoundAt > 0 {
			q = q.Where("created_at >= ?", child.ReferrerBoundAt)
		}
		var events []model.AdtySiteEarningEvent
		if err := q.Find(&events).Error; err != nil {
			return decimal.Zero, bySite, err
		}
		if len(events) == 0 {
			continue
		}
		supplyNet := decimal.Zero
		for i := range events {
			_, platform, _, err := adtyDurableEarningSplitTx(model.DB, &events[i])
			if err != nil {
				return decimal.Zero, bySite, err
			}
			supplyNet = supplyNet.Add(adtyPartnerEligibleSupplyUSD(&events[i], platform))
		}
		if !supplyNet.IsPositive() {
			continue
		}
		gross := supplyNet.Mul(ratePercent).Div(decimal.NewFromInt(100)).Round(8)
		pendingNeg, err := adtyReadCommissionOffsetPending(
			model.AdtyCommissionOffsetScopeSitePartner,
			fmt.Sprintf("partner:%d:%d", referrerSiteID, child.Id),
		)
		if err != nil {
			return decimal.Zero, bySite, err
		}
		eff := gross.Sub(pendingNeg).Round(8)
		if eff.IsPositive() {
			bySite[child.Id] = eff
			total = total.Add(eff)
		}
	}
	return total.Round(8), bySite, nil
}

// GetAdtyMainReferralPendingHourUSD returns the estimated main-site referral commission
// pending hourly settlement for affiliateID.
func GetAdtyMainReferralPendingHourUSD(affiliateID int) (decimal.Decimal, error) {
	if affiliateID <= 0 || model.DB == nil || !Referral.IsEnabled() {
		return decimal.Zero, nil
	}
	hasTable, err := model.AdtyTableExists(model.DB, &model.AdtyPrincipalConsumeEvent{})
	if err != nil || !hasTable {
		return decimal.Zero, err
	}
	var affiliate model.ReferralAffiliate
	if err := model.DB.Where("id = ?", affiliateID).Limit(1).Find(&affiliate).Error; err != nil {
		return decimal.Zero, err
	}
	if affiliate.Id == 0 || affiliate.Status != model.ReferralAffiliateStatusApproved || affiliate.SiteOwnerSince > 0 {
		return decimal.Zero, nil
	}
	var inviter model.User
	if err := model.DB.Select("id", "site_id", "status", "role").Where("id = ?", affiliate.UserId).Limit(1).Find(&inviter).Error; err != nil {
		return decimal.Zero, err
	}
	if inviter.Id == 0 || inviter.Status != common.UserStatusEnabled || inviter.SiteId != 0 || inviter.Role >= common.RoleAdminUser {
		return decimal.Zero, nil
	}
	rate, ok := AffiliateRate(&affiliate)
	if !ok || !rate.IsPositive() {
		return decimal.Zero, nil
	}
	var bindings []model.ReferralBinding
	if err := model.DB.Where("affiliate_id = ? AND (risk_flag IS NULL OR risk_flag <> ?)",
		affiliateID, model.ReferralRiskSelfInvite).Find(&bindings).Error; err != nil {
		return decimal.Zero, err
	}
	if len(bindings) == 0 {
		return decimal.Zero, nil
	}
	candidateIDs := make([]int, 0, len(bindings))
	for _, b := range bindings {
		if b.InviteeUserId > 0 && b.InviteeUserId != affiliate.UserId {
			candidateIDs = append(candidateIDs, b.InviteeUserId)
		}
	}
	if len(candidateIDs) == 0 {
		return decimal.Zero, nil
	}
	var eligibleUsers []model.User
	if err := model.DB.Select("id").
		Where("id IN ? AND site_id = 0 AND role < ?", candidateIDs, common.RoleAdminUser).
		Find(&eligibleUsers).Error; err != nil {
		return decimal.Zero, err
	}
	if len(eligibleUsers) == 0 {
		return decimal.Zero, nil
	}
	eligibleIDs := make([]int, 0, len(eligibleUsers))
	for _, u := range eligibleUsers {
		eligibleIDs = append(eligibleIDs, u.Id)
	}
	var siteOwnerUserIDs []int
	if err := model.DB.Model(&model.ReferralAffiliate{}).
		Where("user_id IN ? AND site_owner_since > 0", eligibleIDs).
		Pluck("user_id", &siteOwnerUserIDs).Error; err != nil {
		return decimal.Zero, err
	}
	siteOwners := make(map[int]struct{}, len(siteOwnerUserIDs))
	for _, uid := range siteOwnerUserIDs {
		siteOwners[uid] = struct{}{}
	}
	finalIDs := make([]int, 0, len(eligibleIDs))
	for _, uid := range eligibleIDs {
		if _, isOwner := siteOwners[uid]; !isOwner {
			finalIDs = append(finalIDs, uid)
		}
	}
	if len(finalIDs) == 0 {
		return decimal.Zero, nil
	}

	var sums []struct {
		UserId int             `gorm:"column:user_id"`
		NetUSD decimal.Decimal `gorm:"column:net_usd"`
	}
	if err := model.DB.Model(&model.AdtyPrincipalConsumeEvent{}).
		Select("user_id, COALESCE(SUM(amount_usd), 0) AS net_usd").
		Where("site_id = 0 AND settled = ? AND user_id IN ?", false, finalIDs).
		Group("user_id").
		Scan(&sums).Error; err != nil {
		return decimal.Zero, err
	}

	total := decimal.Zero
	for _, row := range sums {
		if !row.NetUSD.IsPositive() {
			continue
		}
		rawComm := row.NetUSD.Mul(rate).Div(decimal.NewFromInt(100)).Round(8)
		pendingNeg, err := adtyReadCommissionOffsetPending(
			model.AdtyCommissionOffsetScopeMainReferral,
			fmt.Sprintf("mref:%d:%d", affiliate.Id, row.UserId),
		)
		if err != nil {
			return decimal.Zero, err
		}
		eff := rawComm.Sub(pendingNeg).Round(8)
		if eff.IsPositive() {
			total = total.Add(eff)
		}
	}
	return total.Round(8), nil
}

// GetAdtySiteInvitePendingHourUSD returns the estimated subsite customer invite commission
// pending hourly settlement for (siteID, inviterID).
func GetAdtySiteInvitePendingHourUSD(siteID, inviterID int) (decimal.Decimal, error) {
	if siteID <= 0 || inviterID <= 0 || model.DB == nil {
		return decimal.Zero, nil
	}
	hasTable, err := model.AdtyTableExists(model.DB, &model.AdtyPrincipalConsumeEvent{})
	if err != nil || !hasTable {
		return decimal.Zero, err
	}
	var siteRate model.AdtySiteReferralRate
	rateLookup := model.DB.Where("site_id = ?", siteID).Limit(1).Find(&siteRate)
	if rateLookup.Error != nil {
		return decimal.Zero, rateLookup.Error
	}
	if rateLookup.RowsAffected == 0 || !siteRate.Enabled {
		return decimal.Zero, nil
	}
	activeReferrer, err := isActiveSiteReferrerUserTx(model.DB, siteID, inviterID)
	if err != nil || !activeReferrer {
		return decimal.Zero, err
	}
	aff, affFound, err := loadSiteReferralAffiliateTx(model.DB, siteID, inviterID)
	if err != nil {
		return decimal.Zero, err
	}
	if affFound && aff.Status == model.AdtySiteAffiliateStatusDisabled {
		return decimal.Zero, nil
	}
	rate := siteRate.Rate
	if affFound && aff.RateOverride != nil {
		override := *aff.RateOverride
		if override.IsNegative() {
			override = decimal.Zero
		} else if override.GreaterThan(adtySiteMaxReferralRate) {
			override = adtySiteMaxReferralRate
		}
		rate = override
	}
	if !rate.IsPositive() {
		return decimal.Zero, nil
	}
	var invites []model.AdtySiteInvite
	if err := model.DB.Select("invitee_id").
		Where("site_id = ? AND inviter_id = ? AND (risk_flag IS NULL OR risk_flag <> ?)",
			siteID, inviterID, model.ReferralRiskSelfInvite).
		Find(&invites).Error; err != nil {
		return decimal.Zero, err
	}
	if len(invites) == 0 {
		return decimal.Zero, nil
	}
	inviteeIDs := make([]int, 0, len(invites))
	for _, inv := range invites {
		if inv.InviteeId > 0 && inv.InviteeId != inviterID {
			inviteeIDs = append(inviteeIDs, inv.InviteeId)
		}
	}
	if len(inviteeIDs) == 0 {
		return decimal.Zero, nil
	}
	var sums []struct {
		UserId int             `gorm:"column:user_id"`
		NetUSD decimal.Decimal `gorm:"column:net_usd"`
	}
	if err := model.DB.Model(&model.AdtyPrincipalConsumeEvent{}).
		Select("user_id, COALESCE(SUM(amount_usd), 0) AS net_usd").
		Where("site_id = ? AND settled = ? AND user_id IN ?", siteID, false, inviteeIDs).
		Group("user_id").
		Scan(&sums).Error; err != nil {
		return decimal.Zero, err
	}

	total := decimal.Zero
	for _, row := range sums {
		if !row.NetUSD.IsPositive() {
			continue
		}
		rawComm := row.NetUSD.Mul(rate).Round(8)
		pendingNeg, err := adtyReadCommissionOffsetPending(
			model.AdtyCommissionOffsetScopeSiteInvite,
			fmt.Sprintf("sinv:%d:%d:%d", siteID, inviterID, row.UserId),
		)
		if err != nil {
			return decimal.Zero, err
		}
		eff := rawComm.Sub(pendingNeg).Round(8)
		if eff.IsPositive() {
			total = total.Add(eff)
		}
	}
	return total.Round(8), nil
}
