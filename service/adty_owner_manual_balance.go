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

const adtyOwnerManualBalanceMaxUSD = 10000000

var (
	ErrAdtySiteCustomerNotFound      = common.NewMessage("Customer not found")
	ErrAdtyOwnerAutoFundingShortfall = common.NewMessage("Insufficient available earnings and excess margin above the level floor")
)

// AdtyOwnerCustomerBalanceResult is returned after a site owner directly
// credits a site customer's balance.
type AdtyOwnerCustomerBalanceResult struct {
	CustomerID    int             `json:"customer_id"`
	AmountUSD     decimal.Decimal `json:"amount_usd"`
	Quota         int64           `json:"quota"`
	BalanceBefore int64           `json:"balance_before"`
	BalanceAfter  int64           `json:"balance_after"`
	EarningsUSD   decimal.Decimal `json:"earnings_usd"`
	MarginUSD     decimal.Decimal `json:"margin_usd"`
	LedgerID      int64           `json:"ledger_id"`
	Replayed      bool            `json:"replayed,omitempty"`
}

// AdtyOwnerTeamBalanceResult is returned after a site owner directly credits a
// site team's shared balance.
type AdtyOwnerTeamBalanceResult struct {
	TeamID            int64           `json:"team_id"`
	AmountUSD         decimal.Decimal `json:"amount_usd"`
	Quota             int64           `json:"quota"`
	SharedQuotaBefore int64           `json:"shared_quota_before"`
	SharedQuotaAfter  int64           `json:"shared_quota_after"`
	EarningsUSD       decimal.Decimal `json:"earnings_usd"`
	MarginUSD         decimal.Decimal `json:"margin_usd"`
	Replayed          bool            `json:"replayed,omitempty"`
}

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

// adtyDebitOwnerAutoFundingTx immediately debits earnings first and excess
// margin above the level floor second within tx.
func adtyDebitOwnerAutoFundingTx(tx *gorm.DB, siteID, operatorID int, amountUSD decimal.Decimal, changeType, refType, refID, reason, baseKey string) (AdtyOwnerFundingSplit, error) {
	ownerID := operatorID
	if ownerID <= 0 {
		var err error
		ownerID, err = model.CurrentAdtySiteFinancialOwnerTx(tx, siteID)
		if err != nil {
			return AdtyOwnerFundingSplit{}, err
		}
	}
	split, err := adtyResolveOwnerFundingSplitTx(tx, siteID, ownerID, amountUSD)
	if err != nil {
		return AdtyOwnerFundingSplit{}, err
	}
	if split.EarningsUSD.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, siteID, model.AdtySiteWalletEarnings, split.EarningsUSD.Neg(), AdtySiteWalletChange{
			Type:           changeType,
			RefType:        refType,
			RefID:          refID,
			Reason:         reason,
			OperatorID:     ownerID,
			IdempotencyKey: baseKey + ":earnings",
		}); err != nil {
			return AdtyOwnerFundingSplit{}, err
		}
	}
	if split.MarginUSD.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, siteID, model.AdtySiteWalletMargin, split.MarginUSD.Neg(), AdtySiteWalletChange{
			Type:             changeType,
			RefType:          refType,
			RefID:            refID,
			Reason:           reason,
			OperatorID:       ownerID,
			IdempotencyKey:   baseKey + ":margin",
			MarginExcessOnly: true,
		}); err != nil {
			return AdtyOwnerFundingSplit{}, err
		}
	}
	return split, nil
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

func adtyOwnerManualBalanceQuota(amount decimal.Decimal) (int64, error) {
	if !amount.IsPositive() || amount.Exponent() < -2 || amount.GreaterThan(decimal.NewFromInt(adtyOwnerManualBalanceMaxUSD)) {
		return 0, common.NewMessage("Invalid parameters")
	}
	computed := amount.Mul(common.QuotaPerUnitDecimal())
	if !computed.Equal(computed.Truncate(0)) {
		return 0, common.NewMessage("Invalid parameters")
	}
	quota, err := common.WalletQuotaFromDecimalStrict(computed)
	if err != nil || quota <= 0 {
		return 0, common.NewMessage("Invalid parameters")
	}
	return int64(quota), nil
}

func lookupOwnerFundingSplitFromLedgersTx(tx *gorm.DB, siteID int, baseKey string) (decimal.Decimal, decimal.Decimal) {
	earn, margin, _ := lookupOwnerFundingSplitAndReasonFromLedgersTx(tx, siteID, baseKey)
	return earn, margin
}

func lookupOwnerFundingSplitAndReasonFromLedgersTx(tx *gorm.DB, siteID int, baseKey string) (decimal.Decimal, decimal.Decimal, string) {
	var rows []model.AdtySiteWalletLedger
	if err := tx.Where("site_id = ? AND idempotency_key IN ?", siteID, []string{baseKey + ":earnings", baseKey + ":margin"}).Find(&rows).Error; err != nil {
		return decimal.Zero, decimal.Zero, ""
	}
	earn, margin := decimal.Zero, decimal.Zero
	reason := ""
	for _, row := range rows {
		if reason == "" {
			reason = row.Reason
		}
		if row.WalletKind == model.AdtySiteWalletEarnings {
			earn = row.Delta.Abs()
		} else if row.WalletKind == model.AdtySiteWalletMargin {
			margin = row.Delta.Abs()
		}
	}
	return earn, margin, reason
}

// CreditAdtySiteCustomerBalanceByOwner lets an active site owner directly add
// principal balance to a customer belonging to that site. Funds are debited
// automatically from earnings first and excess margin above the tier deposit
// floor second, backed 1:1 in the operating wallet, and never auto-counted in
// the site's tier cumulative customer recharge total.
func CreditAdtySiteCustomerBalanceByOwner(siteID, ownerID, customerID int, amountUSD decimal.Decimal, reason, idempotencyKey string) (*AdtyOwnerCustomerBalanceResult, error) {
	reason = strings.TrimSpace(reason)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if siteID <= 0 || ownerID <= 0 || customerID <= 0 || reason == "" || utf8.RuneCountInString(reason) > 200 || idempotencyKey == "" || len(idempotencyKey) > 64 {
		return nil, common.NewMessage("Invalid parameters")
	}
	deltaQuota, err := adtyOwnerManualBalanceQuota(amountUSD)
	if err != nil {
		return nil, err
	}
	ledgerKey := "owner-cust-bal:" + strconv.Itoa(siteID) + ":" + common.Sha1([]byte(idempotencyKey))
	var out *AdtyOwnerCustomerBalanceResult
	var auditLog *model.AuditLog
	var hasIntent bool
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).First(&site, siteID).Error; err != nil {
			return err
		}
		if site.OwnerUserId != ownerID || site.Status != model.AdtySiteStatusActive {
			return common.NewMessage("No permission to perform this action")
		}
		var customer model.User
		if err := model.AdtyLockForUpdate(tx).First(&customer, customerID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteCustomerNotFound
			}
			return err
		}
		if customer.SiteId != siteID || customer.StaffSiteId != 0 || customer.Status != common.UserStatusEnabled {
			return common.NewMessage("No permission to perform this action")
		}
		if isEmp, empErr := adtyIsIndependentTeamEmployeeTx(tx, customerID); empErr != nil {
			return empErr
		} else if isEmp {
			return common.NewMessage("No permission to perform this action")
		}
		existing, err := model.FindAdtyLedgerByIdemKeyTx(tx, ledgerKey)
		if err != nil {
			return err
		}
		if existing != nil {
			if existing.UserId != customerID || existing.DeltaQuota != deltaQuota || existing.RefType != "owner_balance" || existing.Reason != reason || existing.OperatorId != ownerID {
				return model.ErrAdtyIdempotencyConflict
			}
			earn, margin := lookupOwnerFundingSplitFromLedgersTx(tx, siteID, ledgerKey)
			out = &AdtyOwnerCustomerBalanceResult{
				CustomerID:    customerID,
				AmountUSD:     amountUSD,
				Quota:         deltaQuota,
				BalanceBefore: existing.BalanceBefore,
				BalanceAfter:  existing.BalanceAfter,
				EarningsUSD:   earn,
				MarginUSD:     margin,
				LedgerID:      existing.Id,
				Replayed:      true,
			}
			return nil
		}
		refID := strconv.Itoa(customerID)
		split, err := adtyDebitOwnerAutoFundingTx(tx, siteID, ownerID, amountUSD, "owner_customer_balance", "owner_customer_balance", refID, reason, ledgerKey)
		if err != nil {
			if errors.Is(err, ErrAdtySiteWalletInsufficient) {
				return ErrAdtyOwnerAutoFundingShortfall
			}
			return err
		}
		if err := adtyMoveOperatingPrincipalTx(tx, siteID, deltaQuota, "principal_credit", "owner_customer_balance", refID, ledgerKey+":operating", false); err != nil {
			return err
		}
		before, err := model.AdtyCreditUserQuotaTx(tx, customerID, deltaQuota)
		if err != nil {
			return err
		}
		if err := model.CreateAdtyFundBatchesWithSiteTx(tx, customerID, model.AdtyFundBatchSiteID(siteID), ledgerKey, deltaQuota, 0); err != nil {
			return err
		}
		entry := &model.AdtyBalanceLedger{
			UserId:         customerID,
			DeltaQuota:     deltaQuota,
			BalanceBefore:  before,
			BalanceAfter:   before + deltaQuota,
			Type:           model.AdtyLedgerTypeManualAdjust,
			RefType:        "owner_balance",
			RefId:          refID,
			Reason:         reason,
			OperatorId:     ownerID,
			IdempotencyKey: &ledgerKey,
		}
		if err := model.InsertAdtyLedgerTx(tx, entry); err != nil {
			return err
		}
		beforeSnap := map[string]any{"customer_id": customerID, "balance_quota": before}
		afterSnap := map[string]any{
			"customer_id":   customerID,
			"amount_usd":    amountUSD.String(),
			"quota":         deltaQuota,
			"balance_after": before + deltaQuota,
			"earnings_usd":  split.EarningsUSD.String(),
			"margin_usd":    split.MarginUSD.String(),
			"ledger_id":     entry.Id,
			"reason":        reason,
		}
		if err := recordSiteAuditTx(tx, siteID, "customer.balance_credit", ownerID, beforeSnap, afterSnap, reason); err != nil {
			return err
		}
		var owner model.User
		_ = tx.Select("id", "username", "role").First(&owner, ownerID).Error
		prepared, err := model.PrepareOfficialAuditLog(nil, model.OfficialAuditWrite{
			UserId:      ownerID,
			Username:    owner.Username,
			ActorRole:   owner.Role,
			SiteID:      model.OfficialAuditSiteIDPointer(siteID),
			ObjectType:  "site_customer_balance",
			ObjectID:    strconv.FormatInt(entry.Id, 10),
			Action:      "adty.site.customer_balance_credit",
			Category:    model.AuditCategoryOperation,
			Content:     "adty.site.customer_balance_credit",
			Before:      beforeSnap,
			After:       afterSnap,
			Reason:      reason,
			ApprovalRef: "owner_balance:" + strconv.FormatInt(entry.Id, 10),
			Result:      model.AuditResultSuccess,
			Success:     true,
		})
		if err != nil {
			return err
		}
		if tx.Migrator().HasTable(&model.AuditIntent{}) {
			if err := model.AppendAuditIntentTx(tx, prepared); err != nil {
				return err
			}
			hasIntent = true
		}
		auditLog = &prepared
		out = &AdtyOwnerCustomerBalanceResult{
			CustomerID:    customerID,
			AmountUSD:     amountUSD,
			Quota:         deltaQuota,
			BalanceBefore: before,
			BalanceAfter:  before + deltaQuota,
			EarningsUSD:   split.EarningsUSD,
			MarginUSD:     split.MarginUSD,
			LedgerID:      entry.Id,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out != nil && !out.Replayed {
		model.AdtySyncCreditUserQuotaCache(customerID, deltaQuota, "owner-customer-balance")
		deliverStaffBalanceOfficialAudit(auditLog, hasIntent)
	}
	return out, nil
}

// CreditAdtySiteTeamBalanceByOwner lets an active site owner directly add
// shared balance to an active team belonging to that site. Funds are debited
// automatically from earnings first and excess margin above the tier deposit
// floor second. Matching online team top-ups, team shared balance is tracked in
// adty_teams / adty_team_fund_lots / adty_team_ledgers (not operating wallet)
// and is excluded from tier cumulative customer recharge.
func CreditAdtySiteTeamBalanceByOwner(siteID, ownerID int, teamID int64, amountUSD decimal.Decimal, reason, idempotencyKey string) (*AdtyOwnerTeamBalanceResult, error) {
	reason = strings.TrimSpace(reason)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if siteID <= 0 || ownerID <= 0 || teamID <= 0 || reason == "" || utf8.RuneCountInString(reason) > 200 || idempotencyKey == "" || len(idempotencyKey) > 64 {
		return nil, common.NewMessage("Invalid parameters")
	}
	deltaQuota, err := adtyOwnerManualBalanceQuota(amountUSD)
	if err != nil {
		return nil, err
	}
	hash := common.Sha1([]byte("site:" + strconv.Itoa(siteID) + ":" + idempotencyKey))
	ref := "owner-team-" + hash[:24]
	baseKey := "owner-team-bal:" + strconv.Itoa(siteID) + ":" + hash
	ledgerKey := adtyTeamLedgerKey("credit", ref, 0)
	var out *AdtyOwnerTeamBalanceResult
	var auditLog *model.AuditLog
	var hasIntent bool
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).First(&site, siteID).Error; err != nil {
			return err
		}
		if site.OwnerUserId != ownerID || site.Status != model.AdtySiteStatusActive {
			return common.NewMessage("No permission to perform this action")
		}
		var team model.AdtyTeam
		if err := model.AdtyLockForUpdate(tx).First(&team, teamID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtyTeamForbidden
			}
			return err
		}
		if team.SiteId != siteID || team.Status != model.AdtyTeamStatusActive {
			return ErrAdtyTeamForbidden
		}
		var existing model.AdtyTeamLedger
		lookup := tx.Where("idempotency_key = ?", ledgerKey).Limit(1).Find(&existing)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected > 0 {
			earn, margin, storedReason := lookupOwnerFundingSplitAndReasonFromLedgersTx(tx, siteID, baseKey)
			if existing.TeamId != teamID || existing.DeltaQuota != deltaQuota || existing.ActorUserId != ownerID || (storedReason != "" && storedReason != reason) {
				return model.ErrAdtyIdempotencyConflict
			}
			out = &AdtyOwnerTeamBalanceResult{
				TeamID:            teamID,
				AmountUSD:         amountUSD,
				Quota:             deltaQuota,
				SharedQuotaBefore: existing.BalanceBefore,
				SharedQuotaAfter:  existing.BalanceAfter,
				EarningsUSD:       earn,
				MarginUSD:         margin,
				Replayed:          true,
			}
			return nil
		}
		split, err := adtyDebitOwnerAutoFundingTx(tx, siteID, ownerID, amountUSD, "owner_team_balance", "owner_team_balance", strconv.FormatInt(teamID, 10), reason, baseKey)
		if err != nil {
			if errors.Is(err, ErrAdtySiteWalletInsufficient) {
				return ErrAdtyOwnerAutoFundingShortfall
			}
			return err
		}
		before := team.SharedQuota
		if err := applyAdtyTeamQuotaTx(tx, &team, deltaQuota, "credit", ref, ownerID, time.Now().Unix()); err != nil {
			return err
		}
		beforeSnap := map[string]any{"team_id": teamID, "shared_quota": before}
		afterSnap := map[string]any{
			"team_id":      teamID,
			"amount_usd":   amountUSD.String(),
			"quota":        deltaQuota,
			"shared_quota": team.SharedQuota,
			"earnings_usd": split.EarningsUSD.String(),
			"margin_usd":   split.MarginUSD.String(),
			"ref":          ref,
			"reason":       reason,
		}
		if err := recordSiteAuditTx(tx, siteID, "team.balance_credit", ownerID, beforeSnap, afterSnap, reason); err != nil {
			return err
		}
		var owner model.User
		_ = tx.Select("id", "username", "role").First(&owner, ownerID).Error
		prepared, err := model.PrepareOfficialAuditLog(nil, model.OfficialAuditWrite{
			UserId:      ownerID,
			Username:    owner.Username,
			ActorRole:   owner.Role,
			SiteID:      model.OfficialAuditSiteIDPointer(siteID),
			ObjectType:  "site_team_balance",
			ObjectID:    strconv.FormatInt(teamID, 10),
			Action:      "adty.site.team_balance_credit",
			Category:    model.AuditCategoryOperation,
			Content:     "adty.site.team_balance_credit",
			Before:      beforeSnap,
			After:       afterSnap,
			Reason:      reason,
			ApprovalRef: "owner_team_balance:" + ref,
			Result:      model.AuditResultSuccess,
			Success:     true,
		})
		if err != nil {
			return err
		}
		if tx.Migrator().HasTable(&model.AuditIntent{}) {
			if err := model.AppendAuditIntentTx(tx, prepared); err != nil {
				return err
			}
			hasIntent = true
		}
		auditLog = &prepared
		out = &AdtyOwnerTeamBalanceResult{
			TeamID:            teamID,
			AmountUSD:         amountUSD,
			Quota:             deltaQuota,
			SharedQuotaBefore: before,
			SharedQuotaAfter:  team.SharedQuota,
			EarningsUSD:       split.EarningsUSD,
			MarginUSD:         split.MarginUSD,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out != nil && !out.Replayed {
		deliverStaffBalanceOfficialAudit(auditLog, hasIntent)
	}
	return out, nil
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
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteID).First(&site).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		if site.Status == model.AdtySiteStatusClosed {
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
			if selected != nil {
				tierAfter = selected.Code
			} else {
				tierAfter = ""
			}
			if tierAfter != tierBefore {
				if err := applyAdtySiteTierAssignmentTx(tx, siteID, selected, time.Now().Unix()); err != nil {
					return err
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
		if err := recordSiteAuditTx(tx, siteID, "tier.principal_adjust", operatorID, beforeSnap, afterSnap, reason); err != nil {
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
		if tx.Migrator().HasTable(&model.AuditIntent{}) {
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
