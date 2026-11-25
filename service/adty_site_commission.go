package service

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	adtySiteCommissionCapKey  = "adty_site_referral.pending_coverage_cap"
	adtySiteCommissionDaysKey = "adty_site_referral.observation_days"
	adtySiteBatchThresholdKey = "adty_site_referral.batch_register_threshold"
	adtySiteBatchWindowKey    = "adty_site_referral.batch_register_window_hours"
)

type adtySiteCommissionPolicy struct {
	Cap  decimal.Decimal
	Days int64
}

type adtySiteCommissionOrderSnapshot struct {
	Captured           bool             `json:"captured"`
	Eligible           bool             `json:"eligible"`
	Enabled            bool             `json:"enabled"`
	SiteId             int              `json:"site_id"`
	PayerTeamId        int64            `json:"payer_team_id"`
	InviterId          int              `json:"inviter_id"`
	InviteeId          int              `json:"invitee_id"`
	FaceUSD            decimal.Decimal  `json:"face_usd"`
	Rate               decimal.Decimal  `json:"rate"`
	AmountUSD          decimal.Decimal  `json:"amount_usd"`
	PendingCoverageCap *decimal.Decimal `json:"pending_coverage_cap,omitempty"`
	ObservationDays    int64            `json:"observation_days"`
	CreatedAt          int64            `json:"created_at"`
}

func loadLegacySiteCommissionPolicyTx(tx *gorm.DB) (adtySiteCommissionPolicy, error) {
	var rows []model.Option
	if err := tx.Where("key IN ?", []string{adtySiteCommissionCapKey, adtySiteCommissionDaysKey}).Find(&rows).Error; err != nil {
		return adtySiteCommissionPolicy{}, err
	}
	values := make(map[string]string, len(rows))
	for _, r := range rows {
		values[r.Key] = strings.TrimSpace(r.Value)
	}
	capRaw, capOK := values[adtySiteCommissionCapKey]
	daysRaw, daysOK := values[adtySiteCommissionDaysKey]
	if !capOK || !daysOK || capRaw == "" || daysRaw == "" {
		return adtySiteCommissionPolicy{}, ErrAdtySiteReferralUnset
	}
	capVal, err := decimal.NewFromString(capRaw)
	if err != nil || !capVal.IsPositive() {
		return adtySiteCommissionPolicy{}, ErrAdtySiteReferralUnset
	}
	days, err := decimal.NewFromString(daysRaw)
	if err != nil || !days.IsInteger() || !days.IsPositive() || days.GreaterThan(decimal.NewFromInt(math.MaxInt32)) {
		return adtySiteCommissionPolicy{}, ErrAdtySiteReferralUnset
	}
	return adtySiteCommissionPolicy{Cap: capVal, Days: days.IntPart()}, nil
}

// PrepareAdtySiteInviteCommissionSnapshotTx freezes site invite commission eligibility,
// rate, amount, and observation period at order creation time inside the order transaction.
func PrepareAdtySiteInviteCommissionSnapshotTx(tx *gorm.DB, topUp *model.TopUp, ext *model.AdtyTopupOrder) error {
	if ext.IsOwnerBusiness() {
		return nil
	}
	if tx == nil || ext == nil || strings.TrimSpace(ext.TradeNo) == "" {
		return nil
	}
	siteID := ext.SiteId
	userID := ext.UserId
	if userID <= 0 && topUp != nil {
		userID = topUp.UserId
	}
	if siteID <= 0 {
		return nil
	}
	createdAt := ext.CreatedAt
	if createdAt <= 0 {
		createdAt = time.Now().Unix()
	}
	snap := adtySiteCommissionOrderSnapshot{
		Captured:    true,
		SiteId:      siteID,
		PayerTeamId: ext.TeamId,
		InviteeId:   userID,
		FaceUSD:     ext.FaceUSD,
		CreatedAt:   createdAt,
	}
	if ext.TeamId == 0 && userID > 0 && ext.FaceUSD.IsPositive() {
		// Canonical lock order: AdtySite -> User -> AdtySiteInvite.
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).First(&site, siteID).Error; err != nil {
			return err
		}
		var payer model.User
		if err := model.AdtyLockForUpdate(tx).Select("id", "site_id").First(&payer, userID).Error; err != nil {
			return err
		}
		if payer.SiteId != siteID {
			return common.NewMessage("No permission to perform this action")
		}
		var invite model.AdtySiteInvite
		lookup := model.AdtyLockForUpdate(tx).Where("site_id = ? AND invitee_id = ?", siteID, userID).Limit(1).Find(&invite)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected > 0 && invite.InviterId > 0 && invite.InviterId != userID && invite.RiskFlag != model.ReferralRiskSelfInvite {
			if ext.CreatedAt <= 0 || invite.CreatedAt <= 0 || invite.CreatedAt <= ext.CreatedAt {
				snap.InviterId = invite.InviterId
			}
		}
		var siteRate model.AdtySiteReferralRate
		rateLookup := tx.Where("site_id = ?", siteID).Limit(1).Find(&siteRate)
		if rateLookup.Error != nil {
			return rateLookup.Error
		}
		snap.Enabled = rateLookup.RowsAffected > 0 && siteRate.Enabled
		snap.Rate = siteRate.Rate
		if snap.InviterId > 0 {
			aff, affFound, affErr := loadSiteReferralAffiliateTx(tx, siteID, snap.InviterId)
			if affErr != nil {
				return affErr
			}
			if affFound {
				if aff.Status == model.AdtySiteAffiliateStatusDisabled {
					snap.InviterId = 0
				} else if aff.RateOverride != nil {
					override := *aff.RateOverride
					if override.IsNegative() {
						override = decimal.Zero
					} else if override.GreaterThan(adtySiteMaxReferralRate) {
						override = adtySiteMaxReferralRate
					}
					snap.Rate = override
				}
			}
		}
		snap.PendingCoverageCap = site.MarketingPolicy.PendingCoverageCap
		if site.MarketingPolicy.ObservationDays != nil {
			snap.ObservationDays = *site.MarketingPolicy.ObservationDays
		}
		if snap.Enabled && snap.PendingCoverageCap != nil && site.MarketingPolicy.ObservationDays != nil && snap.InviterId > 0 && snap.Rate.IsPositive() {
			amount := SiteInviteCommissionUSD(ext.FaceUSD, snap.Rate)
			if amount.IsPositive() {
				snap.AmountUSD = amount
				snap.Eligible = true
			}
		}
	}
	encoded, err := common.Marshal(snap)
	if err != nil {
		return err
	}
	ext.SiteReferralSnapshot = string(encoded)
	return nil
}

func loadAdtySiteCommissionOrderSnapshotTx(ext *model.AdtyTopupOrder) (adtySiteCommissionOrderSnapshot, bool, error) {
	if ext == nil || strings.TrimSpace(ext.SiteReferralSnapshot) == "" {
		return adtySiteCommissionOrderSnapshot{}, false, nil
	}
	var snap adtySiteCommissionOrderSnapshot
	if err := common.UnmarshalJsonStr(ext.SiteReferralSnapshot, &snap); err != nil {
		return adtySiteCommissionOrderSnapshot{}, false, err
	}
	return snap, snap.Captured, nil
}

// SaveAdtySiteInviteBatchPolicy stores a separate Root threshold for site invite binds.
// Both values must be positive integers. Empty values clear the policy. No default is written.
func SaveAdtySiteInviteBatchPolicy(thresholdRaw, hoursRaw string) error {
	thresholdRaw = strings.TrimSpace(thresholdRaw)
	hoursRaw = strings.TrimSpace(hoursRaw)
	if thresholdRaw == "" && hoursRaw == "" {
		return model.UpdateOptionsBulk(map[string]string{
			adtySiteBatchThresholdKey: "",
			adtySiteBatchWindowKey:    "",
		})
	}
	threshold, err := strconv.ParseInt(thresholdRaw, 10, 64)
	if err != nil || threshold <= 0 {
		return common.NewMessage("Invalid parameters")
	}
	hours, err := strconv.ParseInt(hoursRaw, 10, 64)
	if err != nil || hours <= 0 {
		return common.NewMessage("Invalid parameters")
	}
	return model.UpdateOptionsBulk(map[string]string{
		adtySiteBatchThresholdKey: strconv.FormatInt(threshold, 10),
		adtySiteBatchWindowKey:    strconv.FormatInt(hours, 10),
	})
}

// AdtySiteInviteBatchPolicyView is blank until Root saves both values.
type AdtySiteInviteBatchPolicyView struct {
	Configured  bool   `json:"configured"`
	Threshold   string `json:"threshold,omitempty"`
	WindowHours string `json:"window_hours,omitempty"`
}

// LoadAdtySiteInviteBatchPolicy reports the saved site-invite batch threshold.
func LoadAdtySiteInviteBatchPolicy() (AdtySiteInviteBatchPolicyView, error) {
	thresholdRaw, thresholdOK, err := model.ReadOptionFromDB(adtySiteBatchThresholdKey)
	if err != nil {
		return AdtySiteInviteBatchPolicyView{}, err
	}
	windowRaw, windowOK, err := model.ReadOptionFromDB(adtySiteBatchWindowKey)
	if err != nil {
		return AdtySiteInviteBatchPolicyView{}, err
	}
	threshold, err := strconv.ParseInt(strings.TrimSpace(thresholdRaw), 10, 64)
	hours, err2 := strconv.ParseInt(strings.TrimSpace(windowRaw), 10, 64)
	if !thresholdOK || !windowOK || err != nil || err2 != nil || threshold <= 0 || hours <= 0 {
		return AdtySiteInviteBatchPolicyView{}, nil
	}
	return AdtySiteInviteBatchPolicyView{
		Configured: true, Threshold: strconv.FormatInt(threshold, 10), WindowHours: strconv.FormatInt(hours, 10),
	}, nil
}

func commissionMarginOptionKey(siteID int) string {
	return fmt.Sprintf("adty_site_referral.commission_margin.%d", siteID)
}

// AuthorizeAdtySiteCommissionMargin records Root's permission for this site to
// cover an invite-commission shortfall from its own margin cycle.
// It does not authorize gift margin fill and does not set an amount.
func AuthorizeAdtySiteCommissionMargin(siteID int, allowed bool) error {
	if siteID <= 0 {
		return common.NewMessage("Invalid parameters")
	}
	var count int64
	if err := model.DB.Model(&model.AdtySite{}).Where("id = ?", siteID).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return common.NewMessage("Invalid parameters")
	}
	value := "0"
	if allowed {
		value = "1"
	}
	return model.UpdateOption(commissionMarginOptionKey(siteID), value)
}

// SiteCommissionMarginAllowed reports the saved commission-margin authorization.
func SiteCommissionMarginAllowed(siteID int) (bool, error) {
	raw, ok, err := model.ReadOptionFromDB(commissionMarginOptionKey(siteID))
	if err != nil || !ok {
		return false, err
	}
	return strings.TrimSpace(raw) == "1", nil
}

// SiteInviteCommissionWithdrawable is true only after coverage and observation.
func SiteInviteCommissionWithdrawable(row *model.AdtySiteInviteCommission, now time.Time) bool {
	if row == nil || row.Status != model.AdtySiteCommissionAvailable || row.AvailableAt <= 0 || now.Before(time.Unix(row.AvailableAt, 0)) {
		return false
	}
	return siteInviteCommissionFree(row).IsPositive()
}

// RecordAdtySiteInviteCommissionTx snapshots one site invite commission.
// It does not spend a wallet. Team top-ups and missing invites record nothing.
func RecordAdtySiteInviteCommissionTx(tx *gorm.DB, ext *model.AdtyTopupOrder, now time.Time) error {
	if ext.IsOwnerBusiness() {
		return nil
	}
	if tx == nil || ext == nil || strings.TrimSpace(ext.TradeNo) == "" {
		return nil
	}
	if snap, ok, err := loadAdtySiteCommissionOrderSnapshotTx(ext); err != nil {
		return err
	} else if ok {
		if !snap.Eligible || !snap.Enabled || snap.SiteId <= 0 || snap.PayerTeamId > 0 || snap.InviterId <= 0 || snap.InviteeId <= 0 || snap.InviterId == snap.InviteeId || !snap.Rate.IsPositive() || !snap.AmountUSD.IsPositive() {
			return nil
		}
		row := model.AdtySiteInviteCommission{
			SiteId: snap.SiteId, InviterId: snap.InviterId, InviteeId: snap.InviteeId, TradeNo: ext.TradeNo,
			FaceUSD: snap.FaceUSD, Rate: snap.Rate, AmountUSD: snap.AmountUSD, Status: model.AdtySiteCommissionPendingCoverage,
			PendingCoverageCap: snap.PendingCoverageCap, ObservationDays: snap.ObservationDays, CreatedAt: now.Unix(),
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
	}
	if ext.SiteId <= 0 || ext.TeamId > 0 || ext.UserId <= 0 || !ext.FaceUSD.IsPositive() {
		return nil
	}
	var invite model.AdtySiteInvite
	lookup := tx.Where("site_id = ? AND invitee_id = ?", ext.SiteId, ext.UserId).Limit(1).Find(&invite)
	if lookup.Error != nil {
		return lookup.Error
	}
	if lookup.RowsAffected == 0 || invite.InviterId == ext.UserId || !invite.Rate.IsPositive() {
		return nil
	}
	if ext.CreatedAt > 0 && invite.CreatedAt > ext.CreatedAt {
		return nil
	}
	var siteRate model.AdtySiteReferralRate
	rateLookup := tx.Where("site_id = ?", ext.SiteId).Limit(1).Find(&siteRate)
	if rateLookup.Error != nil {
		return rateLookup.Error
	}
	if rateLookup.RowsAffected == 0 || siteRate.LegacyEnabled == nil || !*siteRate.LegacyEnabled {
		return nil
	}
	amount := SiteInviteCommissionUSD(ext.FaceUSD, invite.Rate)
	if !amount.IsPositive() {
		return nil
	}
	var observationDays int64
	var pendingCap *decimal.Decimal
	if policy, err := loadLegacySiteCommissionPolicyTx(tx); err == nil {
		pendingCap = &policy.Cap
		observationDays = policy.Days
	} else if !errors.Is(err, ErrAdtySiteReferralUnset) {
		return err
	}
	row := model.AdtySiteInviteCommission{
		SiteId: ext.SiteId, InviterId: invite.InviterId, InviteeId: ext.UserId, TradeNo: ext.TradeNo,
		FaceUSD: ext.FaceUSD, Rate: invite.Rate, AmountUSD: amount, Status: model.AdtySiteCommissionPendingCoverage,
		PendingCoverageCap: pendingCap, ObservationDays: observationDays, CreatedAt: now.Unix(),
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

func siteCommissionNetTargetUSD(row *model.AdtySiteInviteCommission) decimal.Decimal {
	if row == nil {
		return decimal.Zero
	}
	target := row.AmountUSD.Sub(row.ClawedUSD).Add(row.RecoveredUSD)
	if target.IsNegative() {
		return decimal.Zero
	}
	return target
}

// CoverAdtySiteInviteCommissionTx freezes site earnings, then authorized margin.
// It does not deduct a wallet. Observation starts when the freeze is complete.
func CoverAdtySiteInviteCommissionTx(tx *gorm.DB, id int64, now time.Time) error {
	return coverAdtySiteInviteCommissionTx(tx, id, now, false)
}

// ResumeAdtySiteInviteCoverage funds the site's original pending rows in
// creation order. It does not create a commission and does not invent an amount.
func ResumeAdtySiteInviteCoverage(siteID int, now time.Time) error {
	if siteID <= 0 || model.DB == nil {
		return common.NewMessage("Invalid parameters")
	}
	var ids []int64
	if err := model.DB.Model(&model.AdtySiteInviteCommission{}).
		Where("site_id = ? AND status = ?", siteID, model.AdtySiteCommissionPendingCoverage).
		Order("created_at, id").Pluck("id", &ids).Error; err != nil {
		return err
	}
	for _, id := range ids {
		if err := model.DB.Transaction(func(tx *gorm.DB) error {
			return coverAdtySiteInviteCommissionTx(tx, id, now, true)
		}); err != nil {
			return err
		}
	}
	return nil
}

// ResumeAdtySiteInviteCoverageWithAudit resumes pending coverage and writes a
// site referral audit entry for manual owner triggers.
func ResumeAdtySiteInviteCoverageWithAudit(siteID, operatorID int, now time.Time, ip, ua string) error {
	if siteID <= 0 || model.DB == nil {
		return common.NewMessage("Invalid parameters")
	}
	var ids []int64
	if err := model.DB.Model(&model.AdtySiteInviteCommission{}).
		Where("site_id = ? AND status = ?", siteID, model.AdtySiteCommissionPendingCoverage).
		Order("created_at, id").Pluck("id", &ids).Error; err != nil {
		return err
	}
	beforePending := int64(len(ids))
	processed := 0
	var runErr error
	for _, id := range ids {
		if err := model.DB.Transaction(func(tx *gorm.DB) error {
			return coverAdtySiteInviteCommissionTx(tx, id, now, true)
		}); err != nil {
			runErr = err
			break
		}
		processed++
	}
	if runErr != nil && processed == 0 {
		return runErr
	}
	var afterPending int64
	_ = model.DB.Model(&model.AdtySiteInviteCommission{}).
		Where("site_id = ? AND status = ?", siteID, model.AdtySiteCommissionPendingCoverage).
		Count(&afterPending).Error
	newSnap := map[string]any{
		"pending_coverage_count": afterPending,
		"processed_count":        processed,
		"partial":                runErr != nil,
	}
	auditErr := recordSiteReferralAuditTx(model.DB, siteID, "coverage_resume", 0, operatorID, "", ip, ua,
		map[string]any{"pending_coverage_count": beforePending},
		newSnap)
	if runErr != nil {
		return runErr
	}
	return auditErr
}

func coverAdtySiteInviteCommissionTx(tx *gorm.DB, id int64, now time.Time, resume bool) error {
	// Read site_id unlocked first so we preserve canonical lock order:
	// AdtySite -> AdtySiteInviteCommission -> AdtySiteWallet.
	var stub struct {
		SiteId int
	}
	if err := tx.Model(&model.AdtySiteInviteCommission{}).Select("site_id").First(&stub, id).Error; err != nil {
		return err
	}
	// Serialize the site-wide backlog check with concurrent coverage attempts.
	var site model.AdtySite
	if err := model.AdtyLockForUpdate(tx).First(&site, stub.SiteId).Error; err != nil {
		return err
	}
	var row model.AdtySiteInviteCommission
	if err := model.AdtyLockForUpdate(tx).First(&row, id).Error; err != nil {
		return err
	}
	if row.Status != model.AdtySiteCommissionPendingCoverage {
		return nil
	}
	cap := row.PendingCoverageCap
	days := row.ObservationDays
	if cap == nil {
		// Pre-upgrade snapshots did not record a threshold. Preserve the legacy
		// terms where available. An originally unconfigured hold can be funded
		// once the owner supplies the missing conditions.
		legacy, err := loadLegacySiteCommissionPolicyTx(tx)
		if err == nil {
			cap = &legacy.Cap
			if days == 0 {
				days = legacy.Days
			}
		} else if errors.Is(err, ErrAdtySiteReferralUnset) {
			cap = site.MarketingPolicy.PendingCoverageCap
			if cap == nil || (days == 0 && site.MarketingPolicy.ObservationDays == nil) {
				return nil
			}
			if days == 0 {
				days = *site.MarketingPolicy.ObservationDays
			}
		} else {
			return err
		}
		if err := tx.Model(&row).Updates(map[string]any{"pending_coverage_cap": cap, "observation_days": days}).Error; err != nil {
			return err
		}
	}
	if !resume {
		var pending decimal.Decimal
		if err := tx.Model(&model.AdtySiteInviteCommission{}).
			Where("site_id = ? AND status = ? AND id <> ?", row.SiteId, model.AdtySiteCommissionPendingCoverage, row.Id).
			Select("COALESCE(SUM(amount_usd - clawed_usd - covered_usd + recovered_usd), 0)").Scan(&pending).Error; err != nil {
			return err
		}
		if !pending.LessThan(*cap) {
			return nil
		}
	}
	if !row.AmountUSD.Sub(row.ClawedUSD).IsPositive() {
		row.Status = model.AdtySiteCommissionClawedBack
		return tx.Model(&row).Update("status", row.Status).Error
	}
	target := siteCommissionNetTargetUSD(&row)
	need := target.Sub(row.CoveredUSD)
	if !need.IsPositive() {
		return markSiteCommissionCovered(tx, &row, now, days)
	}
	taken, err := freezeSiteCommissionSourceTx(tx, row.SiteId, model.AdtySiteWalletEarnings, need, row.TradeNo)
	if err != nil {
		return err
	}
	row.CoveredUSD = row.CoveredUSD.Add(taken)
	row.EarningsHoldUSD = row.EarningsHoldUSD.Add(taken)
	if row.CoveredUSD.LessThan(target) {
		marginTaken, err := freezeSiteCommissionMarginTx(tx, row.SiteId, target.Sub(row.CoveredUSD), row.TradeNo)
		if err != nil {
			return err
		}
		row.CoveredUSD = row.CoveredUSD.Add(marginTaken)
		row.MarginHoldUSD = row.MarginHoldUSD.Add(marginTaken)
	}
	if row.CoveredUSD.LessThan(target) {
		return tx.Model(&row).Updates(map[string]any{
			"covered_usd": row.CoveredUSD, "earnings_hold_usd": row.EarningsHoldUSD, "margin_hold_usd": row.MarginHoldUSD,
		}).Error
	}
	return markSiteCommissionCovered(tx, &row, now, days)
}

func freezeSiteCommissionMarginTx(tx *gorm.DB, siteID int, need decimal.Decimal, tradeNo string) (decimal.Decimal, error) {
	if !need.IsPositive() {
		return decimal.Zero, nil
	}
	if err := ReviewAdtyPooledSiteFinance(tx, siteID); errors.Is(err, ErrAdtyOwnerWalletReview) {
		return decimal.Zero, nil
	} else if err != nil {
		return decimal.Zero, err
	}
	var authorization model.Option
	lookup := tx.Where("key = ?", commissionMarginOptionKey(siteID)).Limit(1).Find(&authorization)
	if lookup.Error != nil || strings.TrimSpace(authorization.Value) != "1" {
		return decimal.Zero, lookup.Error
	}
	var wallet model.AdtySiteWallet
	if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND kind = ?", siteID, model.AdtySiteWalletMargin).First(&wallet).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return decimal.Zero, nil
		}
		return decimal.Zero, err
	}
	room := AdtyMarginNewDraw(AdtyMarginDrawInput{
		TierUSD: wallet.CycleTierUSD, Balance: wallet.Balance, Used: wallet.CycleUsedUSD, Frozen: wallet.FrozenUSD, ExcessFrozen: wallet.ExcessFrozenUSD,
	})
	take := decimal.Min(need, room)
	already, err := siteCommissionFrozenHoldTx(tx, siteID, model.AdtySiteWalletMargin, tradeNo)
	if err != nil {
		return decimal.Zero, err
	}
	return applySiteCommissionFreezeTx(tx, siteID, model.AdtySiteWalletMargin, tradeNo, take, already)
}

func freezeSiteCommissionSourceTx(tx *gorm.DB, siteID int, kind string, need decimal.Decimal, tradeNo string) (decimal.Decimal, error) {
	var wallet model.AdtySiteWallet
	if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND kind = ?", siteID, kind).First(&wallet).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return decimal.Zero, nil
		}
		return decimal.Zero, err
	}
	take := wallet.Balance.Sub(wallet.FrozenUSD)
	if err := ReviewAdtyPooledSiteFinance(tx, siteID); errors.Is(err, ErrAdtyOwnerWalletReview) {
		var order model.AdtyTopupOrder
		result := tx.Where("site_id = ? AND trade_no = ?", siteID, tradeNo).Limit(1).Find(&order)
		if result.Error != nil {
			return decimal.Zero, result.Error
		}
		if result.RowsAffected == 0 {
			return decimal.Zero, nil
		}
		owner, err := model.ResolveAdtySiteFinancialOwnerTx(tx, siteID, order.SiteOwnerUserId)
		if err != nil {
			return decimal.Zero, err
		}
		available, _, err := ownerEarningsWalletTx(tx, siteID, owner, &wallet)
		if errors.Is(err, ErrAdtyOwnerWalletReview) {
			return decimal.Zero, nil
		}
		if err != nil {
			return decimal.Zero, err
		}
		take = decimal.Min(take, available)
	} else if err != nil {
		return decimal.Zero, err
	}
	if take.GreaterThan(need) {
		take = need
	}
	already, err := siteCommissionFrozenHoldTx(tx, siteID, kind, tradeNo)
	if err != nil {
		return decimal.Zero, err
	}
	return applySiteCommissionFreezeTx(tx, siteID, kind, tradeNo, take, already)
}

// siteCommissionFrozenHoldTx sums commission_freeze holds already booked for
// this order and wallet. Resume uses that total so a later increment can
// take a new idempotency key instead of treating a partial freeze as done.
func siteCommissionFrozenHoldTx(tx *gorm.DB, siteID int, kind, tradeNo string) (decimal.Decimal, error) {
	var entries []model.AdtySiteWalletLedger
	if err := tx.Where("site_id = ? AND wallet_kind = ? AND type = ? AND ref_type = ? AND ref_id = ?",
		siteID, kind, "commission_freeze", "site_invite", tradeNo).Find(&entries).Error; err != nil {
		return decimal.Zero, err
	}
	held := decimal.Zero
	for _, entry := range entries {
		held = held.Add(entry.HoldDelta)
	}
	return held, nil
}

// applySiteCommissionFreezeTx books one freeze increment. The first increment
// keeps commission:<tradeNo>:<kind> so a completed one-shot freeze stays
// uniquely keyed. Later increments append the cumulative hold so a partial
// freeze can be resumed after a top-up. A replay of the same increment is a
// no-op and must not be added to CoveredUSD again.
func applySiteCommissionFreezeTx(tx *gorm.DB, siteID int, kind, tradeNo string, take, already decimal.Decimal) (decimal.Decimal, error) {
	if !take.IsPositive() {
		return decimal.Zero, nil
	}
	key := "commission:" + tradeNo + ":" + kind
	if already.IsPositive() {
		key += ":" + already.Add(take).String()
	}
	if len(key) > 128 {
		return decimal.Zero, common.NewMessage("Invalid parameters")
	}
	var existing model.AdtySiteWalletLedger
	lookup := tx.Where("idempotency_key = ?", key).Limit(1).Find(&existing)
	if lookup.Error != nil {
		return decimal.Zero, lookup.Error
	}
	if lookup.RowsAffected > 0 {
		return decimal.Zero, nil
	}
	if _, err := applyAdtySiteWalletChangeTx(tx, siteID, kind, decimal.Zero, AdtySiteWalletChange{
		Type: "commission_freeze", RefType: "site_invite", RefID: tradeNo, IdempotencyKey: key, HoldDelta: take,
	}); err != nil {
		if errors.Is(err, ErrAdtySiteWalletInsufficient) {
			return decimal.Zero, nil
		}
		return decimal.Zero, err
	}
	return take, nil
}

func markSiteCommissionCovered(tx *gorm.DB, row *model.AdtySiteInviteCommission, now time.Time, days int64) error {
	row.Status = model.AdtySiteCommissionCovered
	if days == 0 {
		aff, found, err := loadSiteReferralAffiliateTx(tx, row.SiteId, row.InviterId)
		if err != nil {
			return err
		}
		if !found || aff.SettlementEnabled {
			row.Status = model.AdtySiteCommissionAvailable
		}
	}
	row.CoveredAt = now.Unix()
	row.AvailableAt = now.Unix() + days*86400
	row.CoveredUSD = siteCommissionNetTargetUSD(row)
	return tx.Model(row).Updates(map[string]any{
		"status": row.Status, "covered_at": row.CoveredAt, "available_at": row.AvailableAt, "covered_usd": row.CoveredUSD,
		"earnings_hold_usd": row.EarningsHoldUSD, "margin_hold_usd": row.MarginHoldUSD,
	}).Error
}

func adtySiteInviteCommissionAfterRefundTx(tx *gorm.DB, ext *model.AdtyTopupOrder) error {
	if tx == nil || ext == nil || ext.SiteId <= 0 || strings.TrimSpace(ext.TradeNo) == "" {
		return nil
	}
	if ext.RefundRatio().IsZero() && ext.FaceQuota() > 0 && ext.RefundQuota <= 0 {
		return nil
	}
	// R1-045 global lock order: lock inviter User before AdtySiteInviteCommission
	// to match ConvertAdtySiteInviteCommission and avoid AB-BA deadlocks.
	var stub model.AdtySiteInviteCommission
	if err := tx.Select("id", "inviter_id").Where("trade_no = ?", ext.TradeNo).Limit(1).Find(&stub).Error; err != nil {
		return err
	}
	if stub.Id == 0 {
		return nil
	}
	if stub.InviterId > 0 && stub.InviterId != ext.UserId {
		var inviter model.User
		if err := model.AdtyLockForUpdate(tx).Select("id").First(&inviter, stub.InviterId).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	var row model.AdtySiteInviteCommission
	lookup := model.AdtyLockForUpdate(tx).Where("trade_no = ?", ext.TradeNo).Limit(1).Find(&row)
	if lookup.Error != nil || lookup.RowsAffected == 0 || row.Status == model.AdtySiteCommissionClawedBack {
		return lookup.Error
	}
	thisClaw := model.SiteInviteCommissionThisClaw(row.AmountUSD, row.ClawedUSD, ext.RefundQuota, ext.FaceQuota())
	if !thisClaw.IsPositive() {
		return nil
	}
	withdrawnLive, err := model.SiteInviteCommissionWithdrawnLive(tx, row.Id, row.WithdrawnUSD)
	if err != nil {
		return err
	}
	releaseAmt, convertedAmt, withdrawnAmt, pendingAmt, _ := model.SiteInviteCommissionRefundParts(row, withdrawnLive, ext.RefundQuota, ext.FaceQuota())
	parts := []decimal.Decimal{releaseAmt, convertedAmt, withdrawnAmt, pendingAmt}
	target := row.ClawedUSD.Add(parts[0]).Add(parts[1]).Add(parts[2]).Add(parts[3])
	deferred, err := releaseSiteInviteCommissionFreezeTx(tx, &row, releaseAmt)
	if err != nil {
		return err
	}
	row.PendingPayoutClawUSD = row.PendingPayoutClawUSD.Add(deferred)
	parts[0] = parts[0].Sub(deferred)
	if pendingAmt.IsPositive() {
		if err := tx.Create(&model.AdtyCommissionCoverageCancellation{SiteId: row.SiteId, CommissionId: row.Id, ClawedAfter: target, AmountUSD: pendingAmt}).Error; err != nil {
			return err
		}
	}
	// R7-006: Pre-lock earnings and margin in canonical order BEFORE any
	// operating-wallet debit in clawConvertedSiteCommissionTx or subsequent
	// gift-guarantee release in adtySiteBonusAfterRefundTx.
	if row.SiteId > 0 && (parts[0].IsPositive() || parts[1].IsPositive()) {
		if err := preLockAdtySiteWalletsTx(tx, row.SiteId, model.AdtySiteWalletEarnings, model.AdtySiteWalletMargin); err != nil {
			return err
		}
	}
	if parts[0].IsPositive() {
		if err := consumeSiteCommissionHoldTx(tx, &row, parts[0], true, "refund:"+target.String()); err != nil {
			return err
		}
		row.RecoveredUSD = row.RecoveredUSD.Add(parts[0])
	}
	shortfall := decimal.Zero
	if parts[1].IsPositive() {
		unpaid, err := clawConvertedSiteCommissionTx(tx, &row, parts[1], target)
		if err != nil {
			return err
		}
		shortfall = unpaid
		row.ConvertedClawedUSD = row.ConvertedClawedUSD.Add(parts[1])
	}
	if parts[2].IsPositive() {
		key := "commission-recovery:" + row.TradeNo + ":" + target.String()
		if err := tx.Create(&model.AdtyCommissionRecovery{
			SiteId: row.SiteId, CommissionId: row.Id, TradeNo: row.TradeNo, AmountUSD: parts[2],
			Kind: "withdrawn", Status: "open", IdempotencyKey: key,
		}).Error; err != nil {
			return err
		}
	}
	if shortfall.IsPositive() {
		key := "commission-recovery-converted:" + row.TradeNo + ":" + target.String()
		if err := tx.Create(&model.AdtyCommissionRecovery{
			SiteId: row.SiteId, CommissionId: row.Id, TradeNo: row.TradeNo, AmountUSD: shortfall,
			Kind: "converted", Status: "open", IdempotencyKey: key,
		}).Error; err != nil {
			return err
		}
	}
	row.ClawedUSD = target
	// R1-093: Both withdrawn external recoveries (parts[2]) and unrecoverable
	// converted shortfalls contribute to recovery_usd.
	row.RecoveryUSD = row.RecoveryUSD.Add(shortfall).Add(parts[2])
	updates := map[string]any{
		"pending_payout_claw_usd": row.PendingPayoutClawUSD, "clawed_usd": row.ClawedUSD, "recovered_usd": row.RecoveredUSD, "recovery_usd": row.RecoveryUSD,
		"earnings_hold_usd": row.EarningsHoldUSD, "margin_hold_usd": row.MarginHoldUSD,
		"converted_clawed_usd": row.ConvertedClawedUSD,
	}
	if !row.ClawedUSD.LessThan(row.AmountUSD) {
		row.Status = model.AdtySiteCommissionClawedBack
		updates["status"] = row.Status
	}
	return tx.Model(&row).Updates(updates).Error
}

func splitDecimalByWeights(total decimal.Decimal, weights []decimal.Decimal) []decimal.Decimal {
	return model.SplitDecimalByWeights(total, weights)
}

func clawConvertedSiteCommissionTx(tx *gorm.DB, row *model.AdtySiteInviteCommission, amount, clawedAfter decimal.Decimal) (decimal.Decimal, error) {
	exact := amount.Mul(common.QuotaPerUnitDecimal())
	quota, err := common.WalletQuotaFromDecimalStrict(exact.Floor())
	if err != nil {
		return decimal.Zero, err
	}
	if quota == 0 {
		return amount, nil
	}
	var user model.User
	if err := model.AdtyLockForUpdate(tx).First(&user, row.InviterId).Error; err != nil {
		return decimal.Zero, err
	}
	found, siteFound, err := reclaimConvertedCommissionFundBatchesTx(tx, row, int64(quota))
	if err != nil {
		return decimal.Zero, err
	}
	if found == 0 {
		return amount, nil
	}
	before, debited, err := model.AdtyDebitUserQuotaTx(tx, row.InviterId, found)
	if err != nil {
		return decimal.Zero, err
	}
	if debited != found {
		return decimal.Zero, model.ErrAdtyFundBatchShortfall
	}
	if debited > 0 {
		key := "sic-claw:" + strconv.FormatInt(row.Id, 10) + ":" + clawedAfter.String()
		if len(key) > 64 {
			return decimal.Zero, common.NewMessage("Invalid parameters")
		}
		if err := model.InsertAdtyLedgerTx(tx, &model.AdtyBalanceLedger{
			UserId: row.InviterId, DeltaQuota: -debited, BalanceBefore: before, BalanceAfter: before - debited,
			Type: model.AdtyLedgerTypeCommissionTransfer, RefType: "site_invite", RefId: row.TradeNo,
			Reason: "site invite commission clawback", IdempotencyKey: &key,
		}); err != nil {
			return decimal.Zero, err
		}
		// R2-022 & R2-024: Return recovered funds to funding wallets (earnings/margin)
		// BEFORE debiting operating (canonical wallet lock order), and only debit
		// operating for batches actually belonging to row.SiteId (never site_id=0
		// batches converted after site close).
		if row.SiteId > 0 {
			debitedUSD := decimal.NewFromInt(debited).Div(common.QuotaPerUnitDecimal())
			if err := refundClawedSiteCommissionToFundingWalletsTx(tx, row, debitedUSD, clawedAfter); err != nil {
				return decimal.Zero, err
			}
			if siteFound > 0 {
				if err := adtyMoveOperatingPrincipalTx(tx, row.SiteId, -siteFound, "principal_clawback", "site_invite", row.TradeNo, "operating:"+key, false); err != nil {
					return decimal.Zero, err
				}
			}
		}
	}
	debitedUSD := decimal.NewFromInt(debited).Div(common.QuotaPerUnitDecimal())
	shortfall := amount.Sub(debitedUSD)
	if shortfall.IsNegative() {
		return decimal.Zero, nil
	}
	return shortfall, nil
}

func refundClawedSiteCommissionToFundingWalletsTx(tx *gorm.DB, row *model.AdtySiteInviteCommission, amount, clawedAfter decimal.Decimal) error {
	if tx == nil || row == nil || row.SiteId <= 0 || !amount.IsPositive() {
		return nil
	}
	var deductRows []struct {
		WalletKind string
		Spent      decimal.Decimal
	}
	if err := tx.Model(&model.AdtySiteWalletLedger{}).
		Select("wallet_kind, COALESCE(SUM(-delta), 0) AS spent").
		Where("site_id = ? AND type = ? AND ref_type = ? AND ref_id = ? AND delta < 0",
			row.SiteId, "commission_deduct", "site_invite", row.TradeNo).
		Group("wallet_kind").
		Scan(&deductRows).Error; err != nil {
		return err
	}
	earningsSpent := decimal.Zero
	marginSpent := decimal.Zero
	for _, r := range deductRows {
		switch r.WalletKind {
		case model.AdtySiteWalletEarnings:
			earningsSpent = earningsSpent.Add(r.Spent)
		case model.AdtySiteWalletMargin:
			marginSpent = marginSpent.Add(r.Spent)
		}
	}
	weights := []decimal.Decimal{earningsSpent, marginSpent}
	if !earningsSpent.IsPositive() && !marginSpent.IsPositive() {
		weights = []decimal.Decimal{amount, decimal.Zero}
	}
	parts := splitDecimalByWeights(amount, weights)
	if parts[1].IsPositive() && !parts[0].IsPositive() && row.SiteId > 0 {
		if err := preLockAdtySiteWalletsTx(tx, row.SiteId, model.AdtySiteWalletEarnings); err != nil {
			return err
		}
	}
	kinds := []string{model.AdtySiteWalletEarnings, model.AdtySiteWalletMargin}
	codes := []string{"e", "m"}
	for i, part := range parts {
		if !part.IsPositive() {
			continue
		}
		idemKey := fmt.Sprintf("ccr:%d:%s:%s", row.Id, codes[i], clawedAfter.String())
		if len(idemKey) > 128 {
			idemKey = fmt.Sprintf("ccr:%d:%s:%s", row.Id, codes[i], common.Sha1([]byte(clawedAfter.String())))
		}
		if _, err := applyAdtySiteWalletChangeTx(tx, row.SiteId, kinds[i], part, AdtySiteWalletChange{
			Type:           "commission_clawback_return",
			RefType:        "site_invite",
			RefID:          row.TradeNo,
			Reason:         "site invite commission clawback return",
			IdempotencyKey: idemKey,
		}); err != nil {
			return err
		}
	}
	return nil
}

func reclaimConvertedCommissionFundBatchesTx(tx *gorm.DB, row *model.AdtySiteInviteCommission, wanted int64) (total int64, siteTotal int64, err error) {
	var batches []model.AdtyFundBatch
	if err := tx.Select("id", "source", "site_id").
		Where("user_id = ? AND (source LIKE ? OR source LIKE ?)", row.InviterId, fmt.Sprintf("site-comm-cvt:%d:%%", row.Id), fmt.Sprintf("site-comm-wd:%d:%%", row.Id)).
		Order("id").Find(&batches).Error; err != nil {
		return 0, 0, err
	}
	seen := map[string]struct{}{}
	left := wanted
	for _, b := range batches {
		if _, ok := seen[b.Source]; ok {
			continue
		}
		seen[b.Source] = struct{}{}
		got, err := model.ReclaimAdtyFundBatchesBySourceTx(tx, row.InviterId, b.Source, left, 0)
		if err != nil {
			return 0, 0, err
		}
		left -= got
		total += got
		if row.SiteId > 0 && b.SiteId != nil && *b.SiteId == row.SiteId {
			siteTotal += got
		}
		if left == 0 {
			return total, siteTotal, nil
		}
	}
	// Older balance transfers shared a lot across several commissions. Preserve
	// any remaining quota that could belong to another commission.
	var items []model.AdtySiteInviteWithdrawalItem
	if err := tx.Where("commission_id = ? AND status = ?", row.Id, model.AdtySiteInviteWithdrawItemPaid).Find(&items).Error; err != nil {
		return 0, 0, err
	}
	for _, item := range items {
		source := fmt.Sprintf("site-invite-wd:%d", item.WithdrawalId)
		var lot model.AdtyFundBatch
		res := tx.Where("user_id = ? AND source = ? AND kind = ?", row.InviterId, source, model.AdtyFundBatchKindPrincipal).Limit(1).Find(&lot)
		if res.Error != nil {
			return 0, 0, res.Error
		}
		if res.RowsAffected == 0 {
			continue
		}
		own, err := common.WalletQuotaFromDecimalStrict(item.AmountUSD.Mul(common.QuotaPerUnitDecimal()).Floor())
		if err != nil {
			return 0, 0, err
		}
		provable := lot.Remaining - max(lot.Original-int64(own), 0)
		if provable <= 0 {
			continue
		}
		got, err := model.ReclaimAdtyFundBatchesBySourceTx(tx, row.InviterId, source, min(provable, left), 0)
		if err != nil {
			return 0, 0, err
		}
		left -= got
		total += got
		if row.SiteId > 0 && lot.SiteId != nil && *lot.SiteId == row.SiteId {
			siteTotal += got
		}
		if left == 0 {
			break
		}
	}
	return total, siteTotal, nil
}

func adtySiteIsClosedTx(tx *gorm.DB, siteID int) (bool, error) {
	if tx == nil || siteID <= 0 {
		return true, nil
	}
	var site model.AdtySite
	res := tx.Select("id", "status").Where("id = ?", siteID).Limit(1).Find(&site)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return true, nil
	}
	return site.Status == model.AdtySiteStatusClosed, nil
}

// consumeSiteCommissionHoldTx settles a frozen hold. release returns it to the
// original wallet. A deduction removes it from that wallet.
func consumeSiteCommissionHoldTx(tx *gorm.DB, row *model.AdtySiteInviteCommission, amount decimal.Decimal, release bool, idemBase string) error {
	if !amount.IsPositive() {
		return nil
	}
	holds := []decimal.Decimal{row.EarningsHoldUSD, row.MarginHoldUSD}
	parts := splitDecimalByWeights(amount, holds)
	settled := decimal.Zero
	for _, part := range parts {
		settled = settled.Add(part)
	}
	if settled.LessThan(amount) {
		return common.NewMessage("Not enough withdrawable commission")
	}
	if parts[1].IsPositive() && !parts[0].IsPositive() && row.SiteId > 0 {
		if err := preLockAdtySiteWalletsTx(tx, row.SiteId, model.AdtySiteWalletEarnings); err != nil {
			return err
		}
	}
	kinds := []string{model.AdtySiteWalletEarnings, model.AdtySiteWalletMargin}
	codes := []string{"e", "m"}
	prefix := "cd"
	if release {
		prefix = "cr"
	}
	changed := false
	for i, part := range parts {
		if !part.IsPositive() {
			continue
		}
		key := prefix + ":" + strconv.FormatInt(row.Id, 10) + ":" + codes[i] + ":" + idemBase
		if len(key) > 128 {
			return common.NewMessage("Invalid parameters")
		}
		var existing model.AdtySiteWalletLedger
		lookup := tx.Where("idempotency_key = ?", key).Limit(1).Find(&existing)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected > 0 {
			continue
		}
		change := AdtySiteWalletChange{
			Type: "commission_release", RefType: "site_invite", RefID: row.TradeNo, IdempotencyKey: key, HoldDelta: part.Neg(),
		}
		delta := decimal.Zero
		if !release {
			change.Type = "commission_deduct"
			delta = part.Neg()
		}
		if _, err := applyAdtySiteWalletChangeTx(tx, row.SiteId, kinds[i], delta, change); err != nil {
			return err
		}
		if i == 0 {
			row.EarningsHoldUSD = row.EarningsHoldUSD.Sub(part)
		} else {
			row.MarginHoldUSD = row.MarginHoldUSD.Sub(part)
		}
		changed = true
	}
	if !changed {
		return nil
	}
	return tx.Model(row).Updates(map[string]any{
		"earnings_hold_usd": row.EarningsHoldUSD, "margin_hold_usd": row.MarginHoldUSD,
	}).Error
}

// ListAdtySiteInviteCommissions returns the inviter's commission rows and their status.
// It does not mark a row withdrawable.
func ListAdtySiteInviteCommissions(inviterID int, now time.Time) ([]model.AdtySiteInviteCommission, error) {
	if inviterID <= 0 {
		return nil, common.NewMessage("Invalid parameters")
	}
	var rows []model.AdtySiteInviteCommission
	err := model.DB.Where("inviter_id = ?", inviterID).Order("id").Find(&rows).Error
	return rows, err
}

// ConvertAdtySiteInviteCommission moves an available remainder into the inviter's
// consumption quota and deducts the matching site hold in the same transaction.
func ConvertAdtySiteInviteCommission(inviterID int, commissionID int64, amount decimal.Decimal, key string) error {
	key = strings.TrimSpace(key)
	if inviterID <= 0 || commissionID <= 0 || !amount.IsPositive() || !amount.Equal(amount.Round(8)) || key == "" || len(key) > 40 {
		return common.NewMessage("Invalid parameters")
	}
	exact := amount.Mul(common.QuotaPerUnitDecimal())
	if !exact.Equal(exact.Floor()) {
		return common.NewMessage("Invalid parameters")
	}
	quota, err := common.WalletQuotaFromDecimalStrict(exact)
	if err != nil || quota <= 0 {
		return common.NewMessage("Invalid parameters")
	}
	ref := siteCommissionConversionKey(commissionID, key)
	if len(ref) > 64 {
		return common.NewMessage("Invalid parameters")
	}
	credited := false
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		// R1-045 global lock order: lock User before AdtySiteInviteCommission and AdtySiteWallet.
		var inviter model.User
		if err := model.AdtyLockForUpdate(tx).Select("id").First(&inviter, inviterID).Error; err != nil {
			return err
		}
		var row model.AdtySiteInviteCommission
		if err := model.AdtyLockForUpdate(tx).Where("inviter_id = ?", inviterID).First(&row, commissionID).Error; err != nil {
			return err
		}
		existing, err := model.FindAdtyLedgerByIdemKeyTx(tx, ref)
		if err != nil {
			return err
		}
		if existing != nil {
			if existing.UserId != inviterID || existing.DeltaQuota != int64(quota) || existing.Type != model.AdtyLedgerTypeCommissionTransfer {
				return model.ErrAdtyIdempotencyConflict
			}
			return nil
		}
		if !SiteInviteCommissionWithdrawable(&row, time.Now()) {
			return common.NewMessage("No permission to perform this action")
		}
		if err := model.RejectRestrictedAdtyFundWrite(tx, model.AdtyFundWrite{SiteID: row.SiteId, AccountID: inviterID}); err != nil {
			return err
		}
		if blocked, blockErr := isSiteReferralAffiliateWithdrawalBlockedTx(tx, row.SiteId, inviterID); blockErr != nil {
			return blockErr
		} else if blocked {
			return common.NewMessage("Withdrawals are paused for this account")
		}
		remaining := siteInviteCommissionFree(&row)
		if amount.GreaterThan(remaining) {
			return common.NewMessage("Invalid parameters")
		}
		if err := consumeSiteCommissionHoldTx(tx, &row, amount, false, key); err != nil {
			return err
		}
		before, err := model.AdtyCreditUserQuotaTx(tx, inviterID, int64(quota))
		if err != nil {
			return err
		}
		if err := model.InsertAdtyLedgerTx(tx, &model.AdtyBalanceLedger{
			UserId: inviterID, DeltaQuota: int64(quota), BalanceBefore: before, BalanceAfter: before + int64(quota),
			Type: model.AdtyLedgerTypeCommissionTransfer, RefType: "site_invite", RefId: row.TradeNo,
			Reason: "site invite commission to balance", IdempotencyKey: &ref,
		}); err != nil {
			return err
		}
		// R2-024: Closed sites migrated their customers to site_id=0; tag converted batches
		// to site_id=0 and never credit a closed site's operating wallet.
		siteClosed, err := adtySiteIsClosedTx(tx, row.SiteId)
		if err != nil {
			return err
		}
		batchSiteID := row.SiteId
		if siteClosed {
			batchSiteID = 0
		}
		if err := model.CreateAdtyFundBatchesWithSiteTx(tx, inviterID, model.AdtyFundBatchSiteID(batchSiteID), ref, int64(quota), 0); err != nil {
			return err
		}
		if batchSiteID > 0 {
			if err := adtyMoveOperatingPrincipalTx(tx, batchSiteID, int64(quota), "principal_credit", "site_invite", row.TradeNo, "operating:"+ref, false); err != nil {
				return err
			}
		}
		credited = true
		return tx.Model(&row).Updates(map[string]any{
			"converted_usd":     row.ConvertedUSD.Add(amount),
			"earnings_hold_usd": row.EarningsHoldUSD, "margin_hold_usd": row.MarginHoldUSD,
		}).Error
	})
	if err != nil {
		return err
	}
	if credited {
		model.AdtySyncCreditUserQuotaCache(inviterID, int64(quota), "site invite commission")
	}
	return nil
}

// AdtyUserHasLegacySiteCommissions reports whether a main-site user (including
// customers migrated from a closed sub-site) has any closed-site invite
// commissions or non-terminal closed-site invite withdrawals to manage.
func AdtyUserHasLegacySiteCommissions(userID int) bool {
	if userID <= 0 || model.DB == nil {
		return false
	}
	if !model.DB.Migrator().HasTable(&model.AdtySite{}) {
		return false
	}
	if model.DB.Migrator().HasTable(&model.AdtySiteInviteCommission{}) {
		var count int64
		if err := model.DB.Table("adty_site_invite_commissions AS c").
			Joins("JOIN adty_sites s ON s.id = c.site_id").
			Where("c.inviter_id = ? AND s.status = ?", userID, model.AdtySiteStatusClosed).
			Limit(1).Count(&count).Error; err == nil && count > 0 {
			return true
		}
	}
	if model.DB.Migrator().HasTable(&model.AdtySiteInviteWithdrawal{}) {
		var count int64
		if err := model.DB.Table("adty_site_invite_withdrawals AS w").
			Joins("JOIN adty_sites s ON s.id = w.site_id").
			Where("w.user_id = ? AND s.status = ?", userID, model.AdtySiteStatusClosed).
			Limit(1).Count(&count).Error; err == nil && count > 0 {
			return true
		}
	}
	return false
}

func adtyRecordSiteInviteCommissionTx(tx *gorm.DB, ext *model.AdtyTopupOrder) error {
	now := time.Now()
	if err := RecordAdtySiteInviteCommissionTx(tx, ext, now); err != nil {
		return err
	}
	if ext == nil || strings.TrimSpace(ext.TradeNo) == "" {
		return nil
	}
	var row model.AdtySiteInviteCommission
	lookup := tx.Where("trade_no = ?", ext.TradeNo).Limit(1).Find(&row)
	if lookup.Error != nil || lookup.RowsAffected == 0 {
		return lookup.Error
	}
	return CoverAdtySiteInviteCommissionTx(tx, row.Id, now)
}

// ReleaseAdtySiteInviteCommissions marks covered rows available after observation.
// Affiliates with settlement frozen on their site are skipped until restored.
// It does not credit personal quota or a second commission account.
func ReleaseAdtySiteInviteCommissions(now time.Time) error {
	query := model.DB.Model(&model.AdtySiteInviteCommission{}).
		Where("status = ? AND available_at > 0 AND available_at <= ?", model.AdtySiteCommissionCovered, now.Unix())
	if model.DB.Migrator().HasTable(&model.AdtySiteReferralAffiliate{}) {
		query = query.Where(
			"NOT EXISTS (SELECT 1 FROM adty_site_referral_affiliates a WHERE a.site_id = adty_site_invite_commissions.site_id AND a.user_id = adty_site_invite_commissions.inviter_id AND a.settlement_enabled = ?)",
			false,
		)
	}
	return query.Update("status", model.AdtySiteCommissionAvailable).Error
}
