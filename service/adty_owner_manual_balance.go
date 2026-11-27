package service

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var (
	ErrAdtySiteCustomerNotFound      = common.NewMessage("Customer not found")
	ErrAdtyOwnerAutoFundingShortfall = common.NewMessage("Insufficient available earnings and excess margin above the level floor")
)

// AdtySiteTierPrincipalAdjustResult is returned after Root adjusts a site's
// tier-evaluation cumulative customer recharge total.
type AdtySiteTierPrincipalAdjustResult struct {
	Adjustment   *model.AdtySiteTierPrincipalAdjustment `json:"adjustment"`
	Site         *model.AdtySite                        `json:"site"`
	Stats        AdtySiteTierStats                      `json:"stats"`
	Replayed     bool                                   `json:"replayed,omitempty"`
	AuditEventID string                                 `json:"-"`
}

func adtyMarginRequiredFloorTx(tx *gorm.DB, siteID int, margin *model.AdtySiteWallet) (decimal.Decimal, error) {
	if margin != nil && margin.RequiredBalance.IsPositive() {
		return margin.RequiredBalance, nil
	}
	var site model.AdtySite
	if err := tx.Select("id", "tier_code").Where("id = ?", siteID).Limit(1).Find(&site).Error; err != nil {
		return decimal.Zero, err
	}
	code := strings.TrimSpace(site.TierCode)
	if code == "" {
		return decimal.Zero, nil
	}
	var tier model.AdtySiteTier
	lookup := tx.Select("deposit_usd").Where("code = ? AND enabled = ?", code, true).Limit(1).Find(&tier)
	if lookup.Error != nil {
		return decimal.Zero, lookup.Error
	}
	if lookup.RowsAffected == 0 || !tier.DepositUSD.IsPositive() {
		return decimal.Zero, nil
	}
	return tier.DepositUSD, nil
}

// adtyResolveOwnerFundingSplitTx locks earnings -> margin in canonical order
// and computes the automatic split: available earnings first, then excess
// margin strictly above the site level's required deposit floor.
func adtyResolveOwnerFundingSplitTx(tx *gorm.DB, siteID, operatorID int, amountUSD decimal.Decimal) (AdtyOwnerFundingSplit, error) {
	if siteID <= 0 || !amountUSD.IsPositive() || !validAdtySiteWalletAmount(amountUSD) {
		return AdtyOwnerFundingSplit{}, ErrAdtySiteWalletInvalidOperation
	}
	if err := ensureAdtySiteWalletsTx(tx, siteID); err != nil {
		return AdtyOwnerFundingSplit{}, err
	}
	var earnWallet model.AdtySiteWallet
	if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND kind = ?", siteID, model.AdtySiteWalletEarnings).First(&earnWallet).Error; err != nil {
		return AdtyOwnerFundingSplit{}, err
	}
	var marginWallet model.AdtySiteWallet
	if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND kind = ?", siteID, model.AdtySiteWalletMargin).First(&marginWallet).Error; err != nil {
		return AdtyOwnerFundingSplit{}, err
	}
	ownerID := operatorID
	if ownerID <= 0 {
		var err error
		ownerID, err = model.CurrentAdtySiteFinancialOwnerTx(tx, siteID)
		if err != nil {
			return AdtyOwnerFundingSplit{}, err
		}
	}
	earnAvail, _, err := ownerEarningsWalletTx(tx, siteID, ownerID, &earnWallet)
	if err != nil {
		return AdtyOwnerFundingSplit{}, err
	}
	if earnAvail.IsNegative() {
		earnAvail = decimal.Zero
	}
	floor, err := adtyMarginRequiredFloorTx(tx, siteID, &marginWallet)
	if err != nil {
		return AdtyOwnerFundingSplit{}, err
	}
	marginExcess := marginWallet.Balance.Sub(marginWallet.FrozenUSD).Sub(marginWallet.ExcessFrozenUSD).Sub(floor)
	if pooledErr := ReviewAdtyPooledSiteFinance(tx, siteID); errors.Is(pooledErr, ErrAdtyOwnerWalletReview) {
		// R9-14: Never auto-draw pooled excess margin on a historical-handover site.
		marginExcess = decimal.Zero
	} else if pooledErr != nil {
		return AdtyOwnerFundingSplit{}, pooledErr
	}
	if marginExcess.IsNegative() {
		marginExcess = decimal.Zero
	}
	fromEarnings := decimal.Min(earnAvail, amountUSD)
	fromMargin := amountUSD.Sub(fromEarnings)
	if fromMargin.GreaterThan(marginExcess) {
		return AdtyOwnerFundingSplit{}, ErrAdtySiteWalletInsufficient
	}
	return AdtyOwnerFundingSplit{
		EarningsUSD: fromEarnings,
		MarginUSD:   fromMargin,
		TotalUSD:    amountUSD,
	}, nil
}

// adtyFreezeOwnerAutoFundingTx freezes earnings first and excess margin above
// the level floor second within tx (used by site-owner voucher issuance).
func adtyFreezeOwnerAutoFundingTx(tx *gorm.DB, siteID, operatorID int, amountUSD decimal.Decimal, refID, reason, earnKey, marginKey string) (AdtyOwnerFundingSplit, error) {
	ownerID := operatorID
	if ownerID <= 0 {
		var err error
		ownerID, err = model.CurrentAdtySiteFinancialOwnerTx(tx, siteID)
		if err != nil {
			return AdtyOwnerFundingSplit{}, err
		}
	}
	if err := model.RejectRestrictedAdtyFundWrite(tx, model.AdtyFundWrite{SiteID: siteID, AccountID: ownerID}); err != nil {
		return AdtyOwnerFundingSplit{}, err
	}
	split, err := adtyResolveOwnerFundingSplitTx(tx, siteID, ownerID, amountUSD)
	if err != nil {
		return AdtyOwnerFundingSplit{}, err
	}
	if split.EarningsUSD.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, siteID, model.AdtySiteWalletEarnings, decimal.Zero, AdtySiteWalletChange{
			Type:           "voucher_freeze",
			RefType:        "voucher",
			RefID:          refID,
			Reason:         reason,
			OperatorID:     ownerID,
			IdempotencyKey: earnKey,
			HoldDelta:      split.EarningsUSD,
		}); err != nil {
			return AdtyOwnerFundingSplit{}, err
		}
	}
	if split.MarginUSD.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, siteID, model.AdtySiteWalletMargin, decimal.Zero, AdtySiteWalletChange{
			Type:             "voucher_freeze",
			RefType:          "voucher",
			RefID:            refID,
			Reason:           reason,
			OperatorID:       ownerID,
			IdempotencyKey:   marginKey,
			HoldDelta:        split.MarginUSD,
			MarginExcessOnly: true,
		}); err != nil {
			return AdtyOwnerFundingSplit{}, err
		}
	}
	return split, nil
}

// AdjustAdtySiteTierPrincipal lets Root adjust a site's tier-evaluation
// cumulative customer recharge total (delta or set_total) to reflect offline
// large transfers without changing any wallet balance or user quota. When the
// site is in automatic tier mode, its qualifying tier and supply discount are
// recalculated immediately in the same transaction.
func AdjustAdtySiteTierPrincipal(siteID, operatorID int, mode string, amountUSD decimal.Decimal, reason, idempotencyKey string) (*AdtySiteTierPrincipalAdjustResult, error) {
	mode = strings.TrimSpace(mode)
	reason = strings.TrimSpace(reason)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if mode == "" {
		mode = model.AdtyTierPrincipalAdjustModeDelta
	}
	if siteID <= 0 || operatorID <= 0 || reason == "" || utf8.RuneCountInString(reason) > 500 || idempotencyKey == "" || len(idempotencyKey) > 64 {
		return nil, ErrAdtySiteTierInvalid
	}
	if !amountUSD.Equal(amountUSD.Truncate(8)) || amountUSD.Abs().GreaterThanOrEqual(decimal.New(1, 12)) {
		return nil, ErrAdtySiteTierInvalid
	}
	switch mode {
	case model.AdtyTierPrincipalAdjustModeDelta:
		if amountUSD.IsZero() {
			return nil, ErrAdtySiteTierInvalid
		}
	case model.AdtyTierPrincipalAdjustModeSetTotal:
		if amountUSD.IsNegative() {
			return nil, ErrAdtySiteTierInvalid
		}
	default:
		return nil, ErrAdtySiteTierInvalid
	}
	scopedKey := "site:" + strconv.Itoa(siteID) + ":tier-principal:" + idempotencyKey
	if len(scopedKey) > 128 {
		return nil, ErrAdtySiteTierInvalid
	}
	var out *AdtySiteTierPrincipalAdjustResult
	var auditLog *model.AuditLog
	var hasIntent bool
	var costFactorChanged bool
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteID).First(&site).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		if site.Status == model.AdtySiteStatusClosed || site.Status == model.AdtySiteStatusDeleting {
			return common.NewMessage("Site is closed")
		}
		var existing model.AdtySiteTierPrincipalAdjustment
		lookup := tx.Where("idempotency_key = ?", scopedKey).Limit(1).Find(&existing)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected > 0 {
			same := existing.SiteId == siteID && existing.Mode == mode && existing.Reason == reason
			if same && mode == model.AdtyTierPrincipalAdjustModeDelta {
				same = existing.DeltaUSD.Equal(amountUSD)
			} else if same && mode == model.AdtyTierPrincipalAdjustModeSetTotal {
				same = existing.EffectiveAfterUSD.Equal(amountUSD)
			}
			if !same {
				return model.ErrAdtyIdempotencyConflict
			}
			stats, err := loadAdtySiteTierStatsTx(tx, siteID)
			if err != nil {
				return err
			}
			out = &AdtySiteTierPrincipalAdjustResult{
				Adjustment: &existing,
				Site:       &site,
				Stats:      stats,
				Replayed:   true,
			}
			return nil
		}
		statsBefore, err := loadAdtySiteTierStatsTx(tx, siteID)
		if err != nil {
			return err
		}
		onlinePrincipal := statsBefore.OnlinePrincipalUSD.Round(8)
		adjustBefore := site.TierPrincipalAdjustmentUSD.Round(8)
		var adjustAfter, delta decimal.Decimal
		if mode == model.AdtyTierPrincipalAdjustModeDelta {
			delta = amountUSD
			adjustAfter = adjustBefore.Add(delta)
		} else {
			adjustAfter = amountUSD.Sub(onlinePrincipal).Round(8)
			delta = adjustAfter.Sub(adjustBefore)
			if delta.IsZero() {
				return common.NewMessage("Effective total already matches the target amount")
			}
		}
		if !adjustAfter.Equal(adjustAfter.Truncate(8)) || adjustAfter.Abs().GreaterThanOrEqual(decimal.New(1, 12)) {
			return ErrAdtySiteTierInvalid
		}
		effectiveAfter := onlinePrincipal.Add(adjustAfter).Round(8)
		if effectiveAfter.IsNegative() {
			effectiveAfter = decimal.Zero
		}
		if err := tx.Model(&model.AdtySite{}).Where("id = ?", siteID).Update("tier_principal_adjustment_usd", adjustAfter).Error; err != nil {
			return err
		}
		beforeEff, beforeOK := AdtySiteCostFactor(&site)
		tierBefore := site.TierCode
		tierAfter := site.TierCode
		if site.TierMode != AdtySiteTierModeManual {
			if err := ensureAdtySiteWalletsTx(tx, siteID); err != nil {
				return err
			}
			var tiers []model.AdtySiteTier
			if err := tx.Where("enabled = ?", true).Find(&tiers).Error; err != nil {
				return err
			}
			nextStats := statsBefore
			nextStats.AdjustmentUSD = adjustAfter
			nextStats.PrincipalUSD = effectiveAfter
			selected := SelectAdtySiteTier(tiers, nextStats)
			if selected == nil {
				selected = lowestEnabledAdtySiteTier(tiers)
			}
			if selected != nil && selected.CostFactor != nil && selected.CostFactor.IsPositive() {
				tierAfter = selected.Code
				if tierAfter != tierBefore {
					if err := applyAdtySiteTierAssignmentTx(tx, siteID, selected, time.Now().Unix()); err != nil {
						return err
					}
				}
			}
		}
		adj := &model.AdtySiteTierPrincipalAdjustment{
			SiteId:            siteID,
			Mode:              mode,
			DeltaUSD:          delta,
			OnlineBeforeUSD:   statsBefore.OnlinePrincipalUSD,
			AdjustBeforeUSD:   adjustBefore,
			AdjustAfterUSD:    adjustAfter,
			EffectiveAfterUSD: effectiveAfter,
			TierBefore:        tierBefore,
			TierAfter:         tierAfter,
			Reason:            reason,
			OperatorId:        operatorID,
			IdempotencyKey:    scopedKey,
		}
		if err := tx.Create(adj).Error; err != nil {
			return err
		}
		beforeSnap := map[string]any{
			"online_principal_usd": statsBefore.OnlinePrincipalUSD.String(),
			"adjustment_usd":       adjustBefore.String(),
			"effective_total_usd":  statsBefore.PrincipalUSD.String(),
			"tier_code":            tierBefore,
		}
		afterSnap := map[string]any{
			"mode":                 mode,
			"delta_usd":            delta.String(),
			"online_principal_usd": statsBefore.OnlinePrincipalUSD.String(),
			"adjustment_usd":       adjustAfter.String(),
			"effective_total_usd":  effectiveAfter.String(),
			"tier_code":            tierAfter,
			"adjustment_id":        adj.Id,
			"reason":               reason,
		}
		if err := recordSiteCompatibilityAuditTx(tx, siteID, "tier.principal_adjust", operatorID, beforeSnap, afterSnap, reason); err != nil {
			return err
		}
		var operator model.User
		_ = tx.Select("id", "username", "role").First(&operator, operatorID).Error
		prepared, err := model.PrepareOfficialAuditLog(nil, model.OfficialAuditWrite{
			UserId:      operatorID,
			Username:    operator.Username,
			ActorRole:   operator.Role,
			SiteID:      model.OfficialAuditSiteIDPointer(siteID),
			ObjectType:  "site_tier_principal",
			ObjectID:    strconv.FormatInt(adj.Id, 10),
			Action:      "adty.site.tier_principal_adjust",
			Category:    model.AuditCategoryOperation,
			Content:     "adty.site.tier_principal_adjust",
			Before:      beforeSnap,
			After:       afterSnap,
			Reason:      reason,
			ApprovalRef: "tier_principal:" + strconv.FormatInt(adj.Id, 10),
			Result:      model.AuditResultSuccess,
			Success:     true,
		})
		if err != nil {
			return err
		}
		hasAuditIntentTable, err := model.AdtyTableExists(tx, &model.AuditIntent{})
		if err != nil {
			return err
		}
		if hasAuditIntentTable {
			if err := model.AppendAuditIntentTx(tx, prepared); err != nil {
				return err
			}
			hasIntent = true
		}
		auditLog = &prepared
		var refreshed model.AdtySite
		if err := tx.Where("id = ?", siteID).First(&refreshed).Error; err != nil {
			return err
		}
		afterEff, afterOK := AdtySiteCostFactor(&refreshed)
		costFactorChanged = beforeOK != afterOK || !beforeEff.Equal(afterEff)
		statsAfter := statsBefore
		statsAfter.AdjustmentUSD = adjustAfter
		statsAfter.PrincipalUSD = effectiveAfter
		out = &AdtySiteTierPrincipalAdjustResult{
			Adjustment:   adj,
			Site:         &refreshed,
			Stats:        statsAfter,
			AuditEventID: prepared.EventId,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out != nil && !out.Replayed {
		InvalidateAdtySiteCache()
		deliverStaffBalanceOfficialAudit(auditLog, hasIntent)
		if costFactorChanged && out.Site != nil && AdtyOnSiteCostFactorChanged != nil {
			AdtyOnSiteCostFactorChanged(out.Site)
		}
	}
	return out, nil
}

// ListAdtySiteTierPrincipalAdjustments returns paged Root adjustments for one site.
func ListAdtySiteTierPrincipalAdjustments(siteID, offset, limit int) ([]model.AdtySiteTierPrincipalAdjustment, int64, error) {
	if siteID <= 0 || offset < 0 || limit <= 0 {
		return nil, 0, ErrAdtySiteTierInvalid
	}
	if _, err := GetAdtySite(siteID); err != nil {
		return nil, 0, err
	}
	query := model.DB.Model(&model.AdtySiteTierPrincipalAdjustment{}).Where("site_id = ?", siteID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]model.AdtySiteTierPrincipalAdjustment, 0)
	err := query.Order("id DESC").Offset(offset).Limit(min(limit, 100)).Find(&items).Error
	return items, total, err
}
