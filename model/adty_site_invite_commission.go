package model

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AdtySiteCommissionPendingCoverage = "pending_coverage"
	AdtySiteCommissionCovered         = "covered"
	AdtySiteCommissionAvailable       = "available"
	AdtySiteCommissionClawedBack      = "clawed_back"

	// Official DEC-025 display states. A row can be split across several of
	// these at once. They are derived from amount fields and are not a
	// replacement for the recorded status column.
	AdtySiteCommissionStateOnHold              = "on_hold"
	AdtySiteCommissionStatePendingCoverage     = "pending_coverage"
	AdtySiteCommissionStateConverted           = "converted"
	AdtySiteCommissionStateWithdrawalRequested = "withdrawal_requested"
	AdtySiteCommissionStateWithdrawnReleased   = "withdrawn_released"
	AdtySiteCommissionStateNotIssued           = "not_issued"
)

// AdtySiteInviteCommission is one site invite commission for one top-up.
// The original order fixes its terms. Observation starts at full coverage.
type AdtySiteInviteCommission struct {
	Id                   int64            `json:"id"`
	SiteId               int              `json:"site_id" gorm:"index"`
	InviterId            int              `json:"inviter_id" gorm:"index"`
	InviteeId            int              `json:"invitee_id" gorm:"index"`
	TradeNo              string           `json:"trade_no" gorm:"type:varchar(64);uniqueIndex"`
	FaceUSD              decimal.Decimal  `json:"face_usd" gorm:"type:decimal(20,8);not null"`
	Rate                 decimal.Decimal  `json:"rate" gorm:"type:decimal(10,6);not null"`
	AmountUSD            decimal.Decimal  `json:"amount_usd" gorm:"type:decimal(20,8);not null"`
	PendingPayoutClawUSD decimal.Decimal  `json:"pending_payout_claw_usd" gorm:"type:decimal(20,8);not null;default:0"`
	CoveredUSD           decimal.Decimal  `json:"covered_usd" gorm:"type:decimal(20,8);not null;default:0"`
	EarningsHoldUSD      decimal.Decimal  `json:"earnings_hold_usd" gorm:"type:decimal(20,8);not null;default:0"`
	MarginHoldUSD        decimal.Decimal  `json:"margin_hold_usd" gorm:"type:decimal(20,8);not null;default:0"`
	ConvertedClawedUSD   decimal.Decimal  `json:"converted_clawed_usd" gorm:"type:decimal(20,8);not null;default:0"`
	ClawedUSD            decimal.Decimal  `json:"clawed_usd" gorm:"type:decimal(20,8);not null;default:0"`
	RecoveredUSD         decimal.Decimal  `json:"recovered_usd" gorm:"type:decimal(20,8);not null;default:0"`
	ConvertedUSD         decimal.Decimal  `json:"converted_usd" gorm:"type:decimal(20,8);not null;default:0"`
	WithdrawnUSD         decimal.Decimal  `json:"withdrawn_usd" gorm:"type:decimal(20,8);not null;default:0"`
	FrozenUSD            decimal.Decimal  `json:"frozen_usd" gorm:"type:decimal(20,8);not null;default:0"`
	RecoveryUSD          decimal.Decimal  `json:"recovery_usd" gorm:"type:decimal(20,8);not null;default:0"`
	Status               string           `json:"status" gorm:"type:varchar(24);index"`
	PendingCoverageCap   *decimal.Decimal `json:"pending_coverage_cap" gorm:"type:decimal(20,8)"`
	ObservationDays      int64            `json:"observation_days"`
	CoveredAt            int64            `json:"covered_at"`
	AvailableAt          int64            `json:"available_at"`
	CreatedAt            int64            `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtySiteInviteCommission) TableName() string { return "adty_site_invite_commissions" }

// SiteInviteCommissionThisClaw is the claw for this refund ratio, minus what was already clawed.
// The ratio is refunded principal over the original principal, capped at 1.
func SiteInviteCommissionThisClaw(amount, already decimal.Decimal, refundQuota, faceQuota int64) decimal.Decimal {
	ratio := decimal.NewFromInt(1)
	if faceQuota > 0 {
		ratio = decimal.NewFromInt(refundQuota).Div(decimal.NewFromInt(faceQuota))
		if ratio.IsNegative() {
			ratio = decimal.Zero
		}
		if ratio.GreaterThan(decimal.NewFromInt(1)) {
			ratio = decimal.NewFromInt(1)
		}
	}
	target := amount.Mul(ratio).Round(8)
	if !ratio.LessThan(decimal.NewFromInt(1)) || target.GreaterThan(amount) {
		target = amount
	}
	thisClaw := target.Sub(already)
	if thisClaw.IsNegative() {
		return decimal.Zero
	}
	return thisClaw
}

// SiteInviteCommissionRefundPreview reports issued commission, withdrawn commission,
// and the clawback this refund ratio would record. It does not move money.
func SiteInviteCommissionRefundPreview(amount, withdrawn, already decimal.Decimal, refundQuota, faceQuota int64) (issued, withdrawnOut, expected string) {
	return amount.String(), withdrawn.String(), SiteInviteCommissionThisClaw(amount, already, refundQuota, faceQuota).String()
}

// AdtySiteCommissionStateAmount is one official lifecycle state and its live amount.
type AdtySiteCommissionStateAmount struct {
	State     string `json:"state"`
	AmountUSD string `json:"amount_usd"`
}

// AdtySiteCommissionRefundPreview is the official commission view for a refund
// preview or refund page. Expected amounts follow the after-refund splitter:
// release of remaining hold, clawback of converted balance, recovery of
// withdrawn payout, and cancellation of pending-coverage remainder.
type AdtySiteCommissionRefundPreview struct {
	IssuedUSD                        string                          `json:"issued_usd"`
	States                           []AdtySiteCommissionStateAmount `json:"states"`
	AmountUSD                        string                          `json:"amount_usd"`
	EarningsHoldUSD                  string                          `json:"earnings_hold_usd,omitempty"`
	MarginHoldUSD                    string                          `json:"margin_hold_usd,omitempty"`
	FrozenUSD                        string                          `json:"frozen_usd,omitempty"`
	ConvertedUSD                     string                          `json:"converted_usd,omitempty"`
	ConvertedClawedUSD               string                          `json:"converted_clawed_usd,omitempty"`
	WithdrawnUSD                     string                          `json:"withdrawn_usd,omitempty"`
	ClawedUSD                        string                          `json:"clawed_usd,omitempty"`
	RecoveredUSD                     string                          `json:"recovered_usd,omitempty"`
	RecoveryUSD                      string                          `json:"recovery_usd,omitempty"`
	RecordedStatus                   string                          `json:"recorded_status,omitempty"`
	ExpectedReleaseUSD               string                          `json:"expected_release_usd"`
	ExpectedConvertedClawbackUSD     string                          `json:"expected_converted_clawback_usd"`
	ExpectedWithdrawnRecoveryUSD     string                          `json:"expected_withdrawn_recovery_usd"`
	ExpectedPendingCoverageCancelUSD string                          `json:"expected_pending_coverage_cancel_usd"`
	WithdrawnRecoveryLiable          string                          `json:"withdrawn_recovery_liable,omitempty"`
	ExpectedClawbackUSD              string                          `json:"expected_clawback_usd,omitempty"`
}

// AdtySiteInviteCommissionView is a commission row plus its official state breakdown.
type AdtySiteInviteCommissionView struct {
	AdtySiteInviteCommission
	OfficialStates []AdtySiteCommissionStateAmount `json:"official_states"`
}

// SplitDecimalByWeights assigns total across positive weights at 1e-8 unit
// precision using G.9.5 largest-remainder allocation: floor each bucket to 1e-8,
// then distribute remaining 1e-8 units by descending fractional remainder and
// ascending bucket index. Zero weights stay zero and no bucket exceeds its weight.
func SplitDecimalByWeights(total decimal.Decimal, weights []decimal.Decimal) []decimal.Decimal {
	out := make([]decimal.Decimal, len(weights))
	sum := decimal.Zero
	for _, weight := range weights {
		if weight.IsPositive() {
			sum = sum.Add(weight)
		}
	}
	if !total.IsPositive() || !sum.IsPositive() {
		return out
	}
	if total.GreaterThan(sum) {
		total = sum
	}
	scale := decimal.New(1, 8)
	totalUnits := total.Mul(scale).Floor().IntPart()
	if totalUnits <= 0 {
		return out
	}
	type bucket struct {
		idx      int
		units    int64
		capUnits int64
		rem      decimal.Decimal
	}
	buckets := make([]bucket, 0, len(weights))
	var assigned int64
	for i, weight := range weights {
		if !weight.IsPositive() {
			continue
		}
		capUnits := weight.Mul(scale).Floor().IntPart()
		raw := decimal.NewFromInt(totalUnits).Mul(weight).Div(sum)
		floored := raw.Floor().IntPart()
		if floored > capUnits {
			floored = capUnits
		}
		rem := raw.Sub(decimal.NewFromInt(floored))
		assigned += floored
		buckets = append(buckets, bucket{idx: i, units: floored, capUnits: capUnits, rem: rem})
	}
	leftover := totalUnits - assigned
	// Sort by descending fractional remainder, tie-breaking by ascending bucket index (G.9.5).
	for i := 1; i < len(buckets); i++ {
		for j := i; j > 0; j-- {
			cmp := buckets[j].rem.Cmp(buckets[j-1].rem)
			if cmp > 0 || (cmp == 0 && buckets[j].idx < buckets[j-1].idx) {
				buckets[j], buckets[j-1] = buckets[j-1], buckets[j]
			} else {
				break
			}
		}
	}
	for leftover > 0 {
		progressed := false
		for i := range buckets {
			if leftover <= 0 {
				break
			}
			if buckets[i].units < buckets[i].capUnits {
				buckets[i].units++
				leftover--
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	for _, b := range buckets {
		out[b.idx] = decimal.NewFromInt(b.units).Div(scale)
	}
	return out
}

func siteInviteCommissionConvertedLive(row AdtySiteInviteCommission) decimal.Decimal {
	live := row.ConvertedUSD.Sub(row.ConvertedClawedUSD)
	if live.IsNegative() {
		return decimal.Zero
	}
	return live
}

func siteInviteCommissionHoldUSD(row AdtySiteInviteCommission) decimal.Decimal {
	hold := row.EarningsHoldUSD.Add(row.MarginHoldUSD).Sub(row.PendingPayoutClawUSD)
	if hold.IsNegative() {
		return decimal.Zero
	}
	return hold
}

func siteInviteCommissionUncoveredUSD(row AdtySiteInviteCommission, withdrawnLive decimal.Decimal) decimal.Decimal {
	if withdrawnLive.IsNegative() {
		withdrawnLive = decimal.Zero
	}
	uncovered := row.AmountUSD.Sub(row.ClawedUSD).Sub(siteInviteCommissionHoldUSD(row)).Sub(siteInviteCommissionConvertedLive(row)).Sub(withdrawnLive)
	if uncovered.IsNegative() {
		return decimal.Zero
	}
	return uncovered
}

// SiteInviteCommissionRefundParts splits this refund's claw the same way
// adtySiteInviteCommissionAfterRefundTx does. It does not move money.
func SiteInviteCommissionRefundParts(row AdtySiteInviteCommission, withdrawnLive decimal.Decimal, refundQuota, faceQuota int64) (release, convertedClaw, withdrawnRecovery, pendingCancel, thisClaw decimal.Decimal) {
	thisClaw = SiteInviteCommissionThisClaw(row.AmountUSD, row.ClawedUSD, refundQuota, faceQuota)
	if withdrawnLive.IsNegative() {
		withdrawnLive = decimal.Zero
	}
	parts := SplitDecimalByWeights(thisClaw, []decimal.Decimal{
		siteInviteCommissionHoldUSD(row),
		siteInviteCommissionConvertedLive(row),
		withdrawnLive,
		siteInviteCommissionUncoveredUSD(row, withdrawnLive),
	})
	return parts[0], parts[1], parts[2], parts[3], thisClaw
}

// SiteInviteCommissionOfficialStates lists live amounts under official DEC-025
// names. It does not use covered, available, or clawed_back as product states.
func SiteInviteCommissionOfficialStates(row AdtySiteInviteCommission, withdrawnLive decimal.Decimal) []AdtySiteCommissionStateAmount {
	totalHold := row.EarningsHoldUSD.Add(row.MarginHoldUSD)
	if totalHold.IsNegative() {
		totalHold = decimal.Zero
	}
	frozen := row.FrozenUSD
	if frozen.IsNegative() {
		frozen = decimal.Zero
	}
	// PendingPayoutClawUSD is a subset of FrozenUSD awaiting approved-payout reconciliation.
	// Do not subtract it twice from on_hold.
	onHold := totalHold.Sub(frozen)
	if onHold.IsNegative() {
		onHold = decimal.Zero
	}
	pendingWithdrawal := frozen.Sub(row.PendingPayoutClawUSD)
	if pendingWithdrawal.IsNegative() {
		pendingWithdrawal = decimal.Zero
	}
	states := make([]AdtySiteCommissionStateAmount, 0, 5)
	appendPositive := func(state string, amount decimal.Decimal) {
		if amount.IsPositive() {
			states = append(states, AdtySiteCommissionStateAmount{State: state, AmountUSD: amount.String()})
		}
	}
	appendPositive(AdtySiteCommissionStateOnHold, onHold)
	appendPositive(AdtySiteCommissionStatePendingCoverage, siteInviteCommissionUncoveredUSD(row, withdrawnLive))
	appendPositive(AdtySiteCommissionStateConverted, siteInviteCommissionConvertedLive(row))
	appendPositive(AdtySiteCommissionStateWithdrawalRequested, pendingWithdrawal)
	appendPositive(AdtySiteCommissionStateWithdrawnReleased, row.WithdrawnUSD)
	return states
}

// SiteInviteCommissionWithdrawnLive is the withdrawn amount that has not yet
// been recorded as an open or historical withdrawn recovery.
func SiteInviteCommissionWithdrawnLive(db *gorm.DB, commissionID int64, withdrawn decimal.Decimal) (decimal.Decimal, error) {
	if db == nil || !withdrawn.IsPositive() {
		return decimal.Zero, nil
	}
	var rows []AdtyCommissionRecovery
	if err := db.Where("commission_id = ? AND kind = ?", commissionID, "withdrawn").Find(&rows).Error; err != nil {
		return decimal.Zero, err
	}
	recorded := decimal.Zero
	for _, row := range rows {
		recorded = recorded.Add(row.AmountUSD)
	}
	left := withdrawn.Sub(recorded)
	if left.IsNegative() {
		return decimal.Zero, nil
	}
	return left, nil
}

// BuildSiteInviteCommissionRefundPreview fills official states and the four
// expected refund amounts. It does not move money.
func BuildSiteInviteCommissionRefundPreview(row AdtySiteInviteCommission, withdrawnLive decimal.Decimal, refundQuota, faceQuota int64) *AdtySiteCommissionRefundPreview {
	release, convertedClaw, withdrawnRecovery, pendingCancel, thisClaw := SiteInviteCommissionRefundParts(row, withdrawnLive, refundQuota, faceQuota)
	view := &AdtySiteCommissionRefundPreview{
		IssuedUSD:                        row.AmountUSD.String(),
		States:                           SiteInviteCommissionOfficialStates(row, withdrawnLive),
		AmountUSD:                        row.AmountUSD.String(),
		EarningsHoldUSD:                  row.EarningsHoldUSD.String(),
		MarginHoldUSD:                    row.MarginHoldUSD.String(),
		FrozenUSD:                        row.FrozenUSD.String(),
		ConvertedUSD:                     row.ConvertedUSD.String(),
		ConvertedClawedUSD:               row.ConvertedClawedUSD.String(),
		WithdrawnUSD:                     row.WithdrawnUSD.String(),
		ClawedUSD:                        row.ClawedUSD.String(),
		RecoveredUSD:                     row.RecoveredUSD.String(),
		RecoveryUSD:                      row.RecoveryUSD.String(),
		RecordedStatus:                   row.Status,
		ExpectedReleaseUSD:               release.String(),
		ExpectedConvertedClawbackUSD:     convertedClaw.String(),
		ExpectedWithdrawnRecoveryUSD:     withdrawnRecovery.String(),
		ExpectedPendingCoverageCancelUSD: pendingCancel.String(),
		ExpectedClawbackUSD:              thisClaw.String(),
	}
	if withdrawnRecovery.IsPositive() {
		view.WithdrawnRecoveryLiable = "inviter"
	}
	return view
}

// LoadSiteInviteCommissionRefundPreview reads the commission for one top-up
// (checking subsite adty_site_invite_commissions first, then falling back to
// main-site referral_commissions) and builds the official refund preview.
// It returns nil when none was issued.
func LoadSiteInviteCommissionRefundPreview(db *gorm.DB, tradeNo string, refundQuota, faceQuota int64) (*AdtySiteCommissionRefundPreview, error) {
	if db == nil || strings.TrimSpace(tradeNo) == "" {
		return nil, nil
	}
	var row AdtySiteInviteCommission
	if err := db.Where("trade_no = ?", tradeNo).Limit(1).Find(&row).Error; err != nil {
		return nil, err
	}
	if row.Id > 0 && row.AmountUSD.IsPositive() {
		withdrawnLive, err := SiteInviteCommissionWithdrawnLive(db, row.Id, row.WithdrawnUSD)
		if err != nil {
			return nil, err
		}
		return BuildSiteInviteCommissionRefundPreview(row, withdrawnLive, refundQuota, faceQuota), nil
	}
	return loadMainSiteReferralCommissionRefundPreview(db, tradeNo, refundQuota, faceQuota)
}

func loadMainSiteReferralCommissionRefundPreview(db *gorm.DB, tradeNo string, refundQuota, faceQuota int64) (*AdtySiteCommissionRefundPreview, error) {
	hasRefTable, err := AdtyTableExists(db, &ReferralCommission{})
	if err != nil || !hasRefTable {
		return nil, err
	}
	var commissions []ReferralCommission
	if err := db.Where("source_type = ? AND source_trade_no = ?", ReferralSourceTopup, tradeNo).Order("id ASC").Find(&commissions).Error; err != nil {
		return nil, err
	}
	if len(commissions) == 0 {
		return nil, nil
	}
	var ext AdtyTopupOrder
	hasExt := false
	if ok, tblErr := AdtyTableExists(db, &AdtyTopupOrder{}); tblErr != nil {
		return nil, tblErr
	} else if ok {
		res := db.Where("trade_no = ?", tradeNo).Limit(1).Find(&ext)
		if res.Error != nil {
			return nil, res.Error
		}
		hasExt = res.RowsAffected > 0
	}
	ratio := decimal.NewFromInt(1)
	if faceQuota > 0 {
		ratio = decimal.NewFromInt(refundQuota).Div(decimal.NewFromInt(faceQuota))
		if ratio.IsNegative() {
			ratio = decimal.Zero
		}
		if ratio.GreaterThan(decimal.NewFromInt(1)) {
			ratio = decimal.NewFromInt(1)
		}
	}
	hasLedgerTable, err := AdtyTableExists(db, &ReferralCommissionLedger{})
	if err != nil {
		return nil, err
	}
	hasRecoveryTable, err := AdtyTableExists(db, &AdtyCommissionRecovery{})
	if err != nil {
		return nil, err
	}
	hasBalanceLedgerTable, err := AdtyTableExists(db, &AdtyBalanceLedger{})
	if err != nil {
		return nil, err
	}
	hasWithdrawItemTable, err := AdtyTableExists(db, &ReferralWithdrawalItem{})
	if err != nil {
		return nil, err
	}
	hasWithdrawTable, err := AdtyTableExists(db, &ReferralWithdrawal{})
	if err != nil {
		return nil, err
	}

	var (
		totalIssued            decimal.Decimal
		totalFrozen            decimal.Decimal
		totalConverted         decimal.Decimal
		totalConvertedClawed   decimal.Decimal
		totalConvertedLive     decimal.Decimal
		totalExternalWithdrawn decimal.Decimal
		totalOnHold            decimal.Decimal
		totalClawed            decimal.Decimal
		totalRecovery          decimal.Decimal
		totalExpectedRelease   decimal.Decimal
		totalExpectedConverted decimal.Decimal
		totalExpectedWithdrawn decimal.Decimal
		totalExpectedClawback  decimal.Decimal
		recordedStatus         string
	)
	for _, comm := range commissions {
		recordedStatus = comm.Status
		accrued := decimal.NewFromFloat(comm.CommissionAmount).Round(8)
		ledgerDelta := decimal.Zero
		if hasLedgerTable {
			var entries []ReferralCommissionLedger
			if err := db.Where("commission_id = ? AND type IN ?", comm.Id,
				[]string{"commission_accrue", "commission_clawback_partial", "commission_clawback", "commission_void", "withdraw_reject_clawback"}).
				Order("id ASC").Find(&entries).Error; err != nil {
				return nil, err
			}
			for _, entry := range entries {
				if entry.Type == "commission_accrue" {
					accrued = decimal.NewFromFloat(entry.DeltaPending).Round(8)
					break
				}
			}
			for _, entry := range entries {
				if entry.Type != "commission_accrue" {
					ledgerDelta = ledgerDelta.Add(decimal.NewFromFloat(entry.DeltaPending).Round(8)).
						Add(decimal.NewFromFloat(entry.DeltaAvailable).Round(8)).
						Add(decimal.NewFromFloat(entry.DeltaFrozen).Round(8))
				}
			}
		}
		if !accrued.IsPositive() {
			continue
		}
		extRecovered := decimal.Zero
		cvtRecovered := decimal.Zero
		if hasRecoveryTable {
			var recRows []AdtyCommissionRecovery
			if err := db.Where("commission_id = ? AND kind IN ?", comm.Id, []string{"ref_withdrawn", "ref_converted"}).Find(&recRows).Error; err != nil {
				return nil, err
			}
			for _, r := range recRows {
				if r.Kind == "ref_converted" {
					cvtRecovered = cvtRecovered.Add(r.AmountUSD)
				} else {
					extRecovered = extRecovered.Add(r.AmountUSD)
				}
			}
		}
		cvtClawed := decimal.Zero
		if hasBalanceLedgerTable {
			var sumQuota int64
			if err := db.Model(&AdtyBalanceLedger{}).
				Select("COALESCE(SUM(-delta_quota), 0)").
				Where("type = ? AND ref_type = ? AND ref_id = ? AND delta_quota < 0",
					AdtyLedgerTypeCommissionTransfer, "referral_commission_clawback", strconv.Itoa(comm.Id)).
				Scan(&sumQuota).Error; err != nil {
				return nil, err
			}
			if sumQuota > 0 {
				cvtClawed = decimal.NewFromInt(sumQuota).Div(common.QuotaPerUnitDecimal())
			}
		}
		frozenAlloc := decimal.Zero
		pendingClaw := decimal.Zero
		convertedAlloc := decimal.Zero
		externalAlloc := decimal.Zero
		if hasWithdrawItemTable {
			var items []struct {
				AllocatedAmount       float64
				PendingClawbackAmount float64
				Status                string
				AccountType           string
			}
			q := db.Table("referral_withdrawal_items AS i").Where("i.commission_id = ?", comm.Id).Order("i.id ASC")
			if hasWithdrawTable {
				q = q.Select("i.allocated_amount, i.pending_clawback_amount, i.status, COALESCE(w.account_type, '') AS account_type").
					Joins("LEFT JOIN referral_withdrawals AS w ON w.id = i.withdrawal_id")
			} else {
				q = q.Select("i.allocated_amount, i.pending_clawback_amount, i.status, '' AS account_type")
			}
			if err := q.Scan(&items).Error; err != nil {
				return nil, err
			}
			for _, item := range items {
				amt := decimal.NewFromFloat(item.AllocatedAmount).Round(8)
				pc := decimal.NewFromFloat(item.PendingClawbackAmount).Round(8)
				switch item.Status {
				case ReferralWithdrawalItemStatusFrozen:
					if pc.IsPositive() {
						pendingClaw = pendingClaw.Add(pc)
					}
					frozenAlloc = frozenAlloc.Add(decimal.Max(amt.Sub(pc), decimal.Zero))
				case ReferralWithdrawalItemStatusWithdrawn:
					if item.AccountType == ReferralWithdrawToBalance {
						convertedAlloc = convertedAlloc.Add(amt)
					} else {
						externalAlloc = externalAlloc.Add(amt)
					}
				}
			}
		}
		recovered := extRecovered.Add(cvtRecovered).Add(cvtClawed).Add(pendingClaw)
		remaining := decimal.Max(accrued.Add(ledgerDelta).Sub(recovered), decimal.Zero)
		alreadyClawed := decimal.Max(accrued.Sub(remaining), decimal.Zero)

		gross := accrued
		if hasExt && ext.FaceUSD.IsPositive() && ext.ReferralRate.IsPositive() {
			gross = ext.FaceUSD.Mul(ext.ReferralRate).Div(decimal.NewFromInt(100)).Round(8)
		}
		targetClaw := decimal.Zero
		if ratio.IsPositive() && gross.IsPositive() {
			targetClaw = gross.Mul(ratio).RoundFloor(8)
		}
		kept := gross.Sub(targetClaw)
		thisClaw := decimal.Max(remaining.Sub(kept), decimal.Zero)

		convertedLive := decimal.Max(convertedAlloc.Sub(cvtClawed).Sub(cvtRecovered), decimal.Zero)
		externalLive := decimal.Max(externalAlloc.Sub(extRecovered), decimal.Zero)

		var expectedRelease, expectedConverted, expectedWithdrawn, onHold decimal.Decimal
		switch comm.Status {
		case ReferralCommissionStatusPending:
			onHold = remaining
			expectedRelease = decimal.Min(thisClaw, remaining)
		case ReferralCommissionStatusPaid:
			if !convertedLive.IsPositive() && !externalLive.IsPositive() {
				externalLive = remaining
				if !externalAlloc.IsPositive() {
					externalAlloc = remaining
				}
			}
			paidParts := SplitDecimalByWeights(thisClaw, []decimal.Decimal{convertedLive, externalLive})
			expectedConverted = paidParts[0]
			expectedWithdrawn = paidParts[1]
		case ReferralCommissionStatusAvailable, ReferralCommissionStatusFrozen:
			free := decimal.Max(remaining.Sub(frozenAlloc).Sub(convertedLive).Sub(externalLive), decimal.Zero)
			onHold = free
			parts := SplitDecimalByWeights(thisClaw, []decimal.Decimal{convertedLive, externalLive, frozenAlloc, free})
			expectedConverted = parts[0]
			expectedWithdrawn = parts[1]
			expectedRelease = parts[2].Add(parts[3])
		default:
			thisClaw = decimal.Zero
		}

		totalIssued = totalIssued.Add(accrued)
		totalFrozen = totalFrozen.Add(frozenAlloc)
		totalConverted = totalConverted.Add(convertedAlloc)
		totalConvertedClawed = totalConvertedClawed.Add(cvtClawed)
		totalConvertedLive = totalConvertedLive.Add(convertedLive)
		totalExternalWithdrawn = totalExternalWithdrawn.Add(externalAlloc)
		totalOnHold = totalOnHold.Add(onHold)
		totalClawed = totalClawed.Add(alreadyClawed)
		totalRecovery = totalRecovery.Add(extRecovered).Add(cvtRecovered)
		totalExpectedRelease = totalExpectedRelease.Add(expectedRelease)
		totalExpectedConverted = totalExpectedConverted.Add(expectedConverted)
		totalExpectedWithdrawn = totalExpectedWithdrawn.Add(expectedWithdrawn)
		totalExpectedClawback = totalExpectedClawback.Add(thisClaw)
	}
	if !totalIssued.IsPositive() {
		return nil, nil
	}
	states := make([]AdtySiteCommissionStateAmount, 0, 4)
	appendPositive := func(state string, amount decimal.Decimal) {
		if amount.IsPositive() {
			states = append(states, AdtySiteCommissionStateAmount{State: state, AmountUSD: amount.String()})
		}
	}
	appendPositive(AdtySiteCommissionStateOnHold, totalOnHold)
	appendPositive(AdtySiteCommissionStateConverted, totalConvertedLive)
	appendPositive(AdtySiteCommissionStateWithdrawalRequested, totalFrozen)
	appendPositive(AdtySiteCommissionStateWithdrawnReleased, totalExternalWithdrawn)

	view := &AdtySiteCommissionRefundPreview{
		IssuedUSD:                        totalIssued.String(),
		States:                           states,
		AmountUSD:                        totalIssued.String(),
		FrozenUSD:                        totalFrozen.String(),
		ConvertedUSD:                     totalConverted.String(),
		ConvertedClawedUSD:               totalConvertedClawed.String(),
		WithdrawnUSD:                     totalExternalWithdrawn.String(),
		ClawedUSD:                        totalClawed.String(),
		RecoveryUSD:                      totalRecovery.String(),
		RecordedStatus:                   recordedStatus,
		ExpectedReleaseUSD:               totalExpectedRelease.String(),
		ExpectedConvertedClawbackUSD:     totalExpectedConverted.String(),
		ExpectedWithdrawnRecoveryUSD:     totalExpectedWithdrawn.String(),
		ExpectedPendingCoverageCancelUSD: "0",
		ExpectedClawbackUSD:              totalExpectedClawback.String(),
	}
	if totalExpectedWithdrawn.IsPositive() {
		view.WithdrawnRecoveryLiable = "inviter"
	}
	return view, nil
}

// PresentSiteInviteCommissions attaches official state amounts to each row.
func PresentSiteInviteCommissions(db *gorm.DB, rows []AdtySiteInviteCommission) ([]AdtySiteInviteCommissionView, error) {
	views := make([]AdtySiteInviteCommissionView, 0, len(rows))
	if len(rows) == 0 {
		return views, nil
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.Id)
	}
	recorded := map[int64]decimal.Decimal{}
	if db != nil {
		var recoveries []AdtyCommissionRecovery
		if err := db.Where("commission_id IN ? AND kind = ?", ids, "withdrawn").Find(&recoveries).Error; err != nil {
			return nil, err
		}
		for _, recovery := range recoveries {
			recorded[recovery.CommissionId] = recorded[recovery.CommissionId].Add(recovery.AmountUSD)
		}
	}
	for _, row := range rows {
		withdrawnLive := row.WithdrawnUSD.Sub(recorded[row.Id])
		if withdrawnLive.IsNegative() || !row.WithdrawnUSD.IsPositive() {
			withdrawnLive = decimal.Zero
		}
		views = append(views, AdtySiteInviteCommissionView{
			AdtySiteInviteCommission: row,
			OfficialStates:           SiteInviteCommissionOfficialStates(row, withdrawnLive),
		})
	}
	return views, nil
}

// AdtyCommissionRecovery is the liability for commission that was already withdrawn.
// It does not debit a customer balance.
type AdtyCommissionRecovery struct {
	Id             int64           `json:"id"`
	SiteId         int             `json:"site_id" gorm:"index"`
	CommissionId   int64           `json:"commission_id" gorm:"index"`
	TradeNo        string          `json:"trade_no" gorm:"type:varchar(64);index"`
	AmountUSD      decimal.Decimal `json:"amount_usd" gorm:"type:decimal(20,8);not null"`
	Kind           string          `json:"kind" gorm:"type:varchar(16)"`
	Status         string          `json:"status" gorm:"type:varchar(16)"`
	IdempotencyKey string          `json:"-" gorm:"uniqueIndex;type:varchar(128)"`
	CreatedAt      int64           `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtyCommissionRecovery) TableName() string { return "adty_commission_recoveries" }

// This part of a refund cancels uncovered commission and has no wallet movement.
type AdtyCommissionCoverageCancellation struct {
	Id           int64
	CommissionId int64           `gorm:"index;uniqueIndex:idx_commission_cancel_total"`
	SiteId       int             `gorm:"index"`
	AmountUSD    decimal.Decimal `gorm:"type:decimal(20,8);not null"`
	ClawedAfter  decimal.Decimal `gorm:"type:decimal(20,8);not null;uniqueIndex:idx_commission_cancel_total"`
	Undated      bool            `gorm:"not null;default:false"`
	CreatedAt    int64
}

// Old partial refunds recorded cumulative amounts without a dated event for the
// uncovered part. Carry the provable amount forward from migration and flag
// earlier reports that cannot be reconstructed from dated records.
func backfillCommissionCoverageCancellations(db *gorm.DB, now int64) error {
	hasCommTable, err := AdtyTableExists(db, &AdtySiteInviteCommission{})
	if err != nil {
		return err
	}
	if !hasCommTable {
		return nil
	}
	if err := db.AutoMigrate(&AdtySiteInviteCommission{}, &AdtyCommissionRecovery{}, &AdtyCommissionCoverageCancellation{}); err != nil {
		return err
	}
	var rows []AdtySiteInviteCommission
	if err := db.Where("clawed_usd > 0").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		withdrawn, err := SiteInviteCommissionWithdrawnLive(db, row.Id, row.WithdrawnUSD)
		if err != nil {
			return err
		}
		var known decimal.Decimal
		if err := db.Model(&AdtyCommissionCoverageCancellation{}).Where("commission_id = ?", row.Id).Select("COALESCE(SUM(amount_usd),0)").Scan(&known).Error; err != nil {
			return err
		}
		missing := row.ClawedUSD.Sub(row.RecoveredUSD).Sub(row.ConvertedClawedUSD).Sub(row.WithdrawnUSD.Sub(withdrawn)).Sub(row.PendingPayoutClawUSD).Sub(known)
		if !missing.IsPositive() {
			continue
		}
		event := AdtyCommissionCoverageCancellation{SiteId: row.SiteId, CommissionId: row.Id, ClawedAfter: row.ClawedUSD, AmountUSD: missing, Undated: true, CreatedAt: now}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&event).Error; err != nil {
			return err
		}
	}
	return nil
}
