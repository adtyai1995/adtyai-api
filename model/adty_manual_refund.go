package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Manual refund overall statuses. Occupancy uses the official S/H/U buckets:
// completed rows are counted in S (also stored on the order's RefundQuota);
// submitted/approved in-progress rows occupy H; unknown external payout occupies U;
// terminated rows release their occupancy.
const (
	AdtyManualRefundStatusSubmitted  = "submitted"
	AdtyManualRefundStatusApproved   = "approved"
	AdtyManualRefundStatusTerminated = "terminated"
	AdtyManualRefundStatusCompleted  = "completed"
)

const (
	AdtyManualRefundPayoutNone    = ""
	AdtyManualRefundPayoutUnknown = "unknown"
	AdtyManualRefundPayoutSuccess = "success"
	AdtyManualRefundPayoutFailed  = "failed"
)

const (
	AdtyManualRefundStepApply      = "apply"
	AdtyManualRefundStepApprove    = "approve"
	AdtyManualRefundStepReclaim    = "reclaim"
	AdtyManualRefundStepBonus      = "bonus"
	AdtyManualRefundStepCommission = "commission"
	AdtyManualRefundStepPayout     = "payout"
	AdtyManualRefundStepComplete   = "complete"
	AdtyManualRefundStepTerminate  = "terminate"
)

// ErrAdtyManualRefundTableMissing is returned when manual-refund occupancy
// cannot be read or written because the table is absent.
var ErrAdtyManualRefundTableMissing = errors.New("adty manual refund table is missing")

// AdtyManualRefund is one authorized manual top-up refund. Sub-steps are
// recorded independently; overall completion requires every required step.
type AdtyManualRefund struct {
	Id               int64           `json:"id"`
	RefundId         string          `json:"refund_id" gorm:"uniqueIndex;type:varchar(64);not null"`
	TradeNo          string          `json:"trade_no" gorm:"index;type:varchar(64);not null"`
	UserId           int             `json:"user_id" gorm:"index"`
	SiteId           int             `json:"site_id"`
	Status           string          `json:"status" gorm:"type:varchar(32);index;not null"`
	RequestUSD       decimal.Decimal `json:"request_usd" gorm:"type:decimal(20,8);not null"`
	RequestQuota     int64           `json:"request_quota" gorm:"not null"`
	Reason            string          `json:"reason" gorm:"type:text"`
	ForeignReference  decimal.Decimal `json:"foreign_reference" gorm:"type:decimal(20,8);not null;default:0"`
	ReconcileOrphanId int64           `json:"reconcile_orphan_id" gorm:"index;not null;default:0"`

	ApplyOperatorId int    `json:"apply_operator_id"`
	AppliedAt       int64  `json:"applied_at"`
	ApplyIdemKey    string `json:"apply_idem_key" gorm:"type:varchar(64)"`

	ApproveOperatorId int    `json:"approve_operator_id"`
	ApprovedAt        int64  `json:"approved_at"`
	ApproveIdemKey    string `json:"approve_idem_key" gorm:"type:varchar(64)"`
	ApproveNote       string `json:"approve_note" gorm:"type:text"`

	ReclaimOperatorId         int             `json:"reclaim_operator_id"`
	ReclaimedAt               int64           `json:"reclaimed_at"`
	ReclaimIdemKey            string          `json:"reclaim_idem_key" gorm:"type:varchar(64)"`
	ReclaimablePrincipalQuota int64           `json:"reclaimable_principal_quota"`
	DeductedPrincipalQuota    int64           `json:"deducted_principal_quota"`
	ConsumedPrincipalQuota    int64           `json:"consumed_principal_quota"`
	ShortfallQuota            int64           `json:"shortfall_quota"`
	ShortfallUSD              decimal.Decimal `json:"shortfall_usd" gorm:"type:decimal(20,8);not null;default:0"`

	BonusOperatorId     int    `json:"bonus_operator_id"`
	BonusAt             int64  `json:"bonus_at"`
	BonusIdemKey        string `json:"bonus_idem_key" gorm:"type:varchar(64)"`
	BonusPlannedQuota   int64  `json:"bonus_planned_quota"`
	BonusDeductedQuota  int64  `json:"bonus_deducted_quota"`
	BonusShortfallQuota int64  `json:"bonus_shortfall_quota"`

	CommissionOperatorId int    `json:"commission_operator_id"`
	CommissionAt         int64  `json:"commission_at"`
	CommissionIdemKey    string `json:"commission_idem_key" gorm:"type:varchar(64)"`
	CommissionSnapshot   string `json:"commission_snapshot" gorm:"type:text"`

	PayoutStatus     string          `json:"payout_status" gorm:"type:varchar(16)"`
	PayoutCurrency   string          `json:"payout_currency" gorm:"type:varchar(16)"`
	PayoutAmount     decimal.Decimal `json:"payout_amount" gorm:"type:decimal(20,8);not null;default:0"`
	PayoutAt         int64           `json:"payout_at"`
	PayoutRecordedAt int64           `json:"payout_recorded_at"`
	PayoutVoucher    string          `json:"payout_voucher" gorm:"type:varchar(255)"`
	PayoutOperatorId int             `json:"payout_operator_id"`
	PayoutIdemKey    string          `json:"payout_idem_key" gorm:"type:varchar(64)"`

	CompleteOperatorId int    `json:"complete_operator_id"`
	CompletedAt        int64  `json:"completed_at"`
	CompleteIdemKey    string `json:"complete_idem_key" gorm:"type:varchar(64)"`

	TerminateOperatorId int    `json:"terminate_operator_id"`
	TerminatedAt        int64  `json:"terminated_at"`
	TerminateIdemKey    string `json:"terminate_idem_key" gorm:"type:varchar(64)"`
	TerminateNote       string `json:"terminate_note" gorm:"type:text"`

	CreatedAt int64 `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt int64 `json:"updated_at" gorm:"autoUpdateTime"`
}

func (AdtyManualRefund) TableName() string { return "adty_manual_refunds" }

// AdtyManualRefundOccupancy is the official cap breakdown for one original order.
// L is the paid USD principal in quota units. S is completed refund occupancy
// (historical one-shot RefundQuota plus completed machine rows, stored on the
// order). H and U come from in-progress machine rows and are not added to
// RefundQuota or refund ratios until the refund is completed.
type AdtyManualRefundOccupancy struct {
	FaceQuota           int64  `json:"face_quota"`
	CompletedQuota      int64  `json:"completed_quota"`
	InProgressQuota     int64  `json:"in_progress_quota"`
	UnknownQuota        int64  `json:"unknown_quota"`
	ExternalOffsetQuota int64  `json:"external_offset_quota"`
	OccupiedQuota       int64  `json:"occupied_quota"`
	RemainingQuota      int64  `json:"remaining_quota"`
	FaceUSD             string `json:"face_usd"`
	CompletedUSD        string `json:"completed_usd"`
	InProgressUSD       string `json:"in_progress_usd"`
	UnknownUSD          string `json:"unknown_usd"`
	ExternalOffsetUSD   string `json:"external_offset_usd"`
	OccupiedUSD         string `json:"occupied_usd"`
	RemainingUSD        string `json:"remaining_usd"`
}

// AdtyManualRefundResult is one state-machine step result.
type AdtyManualRefundResult struct {
	Refund     *AdtyManualRefund                `json:"refund"`
	Occupancy  *AdtyManualRefundOccupancy       `json:"occupancy"`
	Ledger     *AdtyBalanceLedger               `json:"ledger,omitempty"`
	Facts      *AdtyRefundPageFacts             `json:"facts,omitempty"`
	Commission *AdtySiteCommissionRefundPreview `json:"commission,omitempty"`
}

func adtyRequireManualRefundTable(tx *gorm.DB) error {
	if tx == nil {
		return ErrAdtyManualRefundTableMissing
	}
	hasTable, err := AdtyTableExists(tx, &AdtyManualRefund{})
	if err != nil {
		return err
	}
	if !hasTable {
		return ErrAdtyManualRefundTableMissing
	}
	return nil
}

func adtyManualRefundOccupancyBucket(row AdtyManualRefund) string {
	if row.Status == AdtyManualRefundStatusCompleted {
		return ""
	}
	if row.PayoutStatus == AdtyManualRefundPayoutUnknown {
		return "U"
	}
	if row.Status == AdtyManualRefundStatusTerminated {
		return ""
	}
	return "H"
}

func adtyQuotaUSDString(quota int64) string {
	return decimal.NewFromInt(quota).Div(common.QuotaPerUnitDecimal()).String()
}

func adtyConvertExternalOffsetToQuota(ext *AdtyTopupOrder, amount decimal.Decimal, currency string) int64 {
	if ext == nil || !amount.IsPositive() {
		return 0
	}
	face := ext.FaceQuota()
	if face <= 0 {
		return 0
	}
	cur := strings.ToUpper(strings.TrimSpace(currency))
	var q int64
	switch {
	case cur == "" || cur == "USD":
		q = amount.Mul(common.QuotaPerUnitDecimal()).Ceil().IntPart()
	case ext.PaidAmount.IsPositive() && strings.EqualFold(cur, ext.PaidCurrency):
		q = decimal.NewFromInt(face).Mul(amount).Div(ext.PaidAmount).Ceil().IntPart()
	case ext.PayAmount.IsPositive() && strings.EqualFold(cur, ext.PayCurrency):
		q = decimal.NewFromInt(face).Mul(amount).Div(ext.PayAmount).Ceil().IntPart()
	default:
		// Unknown currency mismatch: conservatively occupy full order principal until resolved.
		q = face
	}
	if q < 0 {
		return 0
	}
	if q > face {
		return face
	}
	return q
}

func isExternalReversalEventType(eventID, eventType string) bool {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(eventID)), "reversal:") {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(eventType))
	return strings.Contains(lower, "refund") ||
		strings.Contains(lower, "chargeback") ||
		strings.Contains(lower, "dispute") ||
		strings.Contains(lower, "reversal")
}

// adtyExternalRefundOffsetTx computes X: net verified or pending-review external
// refund/chargeback/dispute occupancy beyond already-completed internal refund S
// (R1-004 / G.9 / 1.3.3). Must be called inside the original order lock.
func adtyExternalRefundOffsetTx(tx *gorm.DB, ext *AdtyTopupOrder) (int64, error) {
	return adtyExternalRefundOffsetForRowsTx(tx, ext, nil, 0, 0)
}

func adtyOrphanExternalQuota(ext *AdtyTopupOrder, ev PaymentOrphanEvent, reviewByID map[int64]PaymentOrphanReview) int64 {
	if ext == nil {
		return 0
	}
	face := ext.FaceQuota()
	if face <= 0 {
		return 0
	}
	if ev.ReviewID > 0 {
		if rev, ok := reviewByID[ev.ReviewID]; ok {
			if rev.Status == PaymentOrphanStatusDismissed || rev.Status == PaymentOrphanStatusNeverCredited {
				return 0
			}
			if rev.ExternalAmount.IsPositive() {
				q := adtyConvertExternalOffsetToQuota(ext, rev.ExternalAmount, rev.ExternalCurrency)
				if q > face {
					return face
				}
				return max(q, int64(0))
			}
		}
	}
	if ev.Status == PaymentOrphanStatusRefunded || isExternalReversalEventType(ev.EventID, ev.EventType) {
		return face
	}
	return 0
}

// adtyOrphanAuthorizedReconcileQuota returns the verified external reversal
// quota that may authorize payout-free manual refund reconciliation. Unlike
// adtyOrphanExternalQuota (which conservatively returns full face for unreviewed
// or zero-amount blocking X), an explicit $0 review authorizes 0 (R10-018).
func adtyOrphanAuthorizedReconcileQuota(ext *AdtyTopupOrder, ev PaymentOrphanEvent, reviewByID map[int64]PaymentOrphanReview) int64 {
	if ext == nil {
		return 0
	}
	face := ext.FaceQuota()
	if face <= 0 {
		return 0
	}
	if ev.ReviewID > 0 {
		if rev, ok := reviewByID[ev.ReviewID]; ok {
			if rev.Status == PaymentOrphanStatusDismissed || rev.Status == PaymentOrphanStatusNeverCredited || !rev.ExternalAmount.IsPositive() {
				return 0
			}
			q := adtyConvertExternalOffsetToQuota(ext, rev.ExternalAmount, rev.ExternalCurrency)
			if q > face {
				return face
			}
			return max(q, int64(0))
		}
	}
	if ev.Status == PaymentOrphanStatusRefunded || isExternalReversalEventType(ev.EventID, ev.EventType) {
		return face
	}
	return 0
}

// adtyExternalRefundOffsetForRowsTx computes X (unreconciled external reversal
// exposure beyond completed S and in-flight manual refunds explicitly reconciling
// pending_review external reversal orphans on this order). Each bound orphan's
// waived share is strictly capped at both that orphan's verified external
// reversal quota and the order-wide cumulative maxExternalQuota (R10-018).
func adtyExternalRefundOffsetForRowsTx(tx *gorm.DB, ext *AdtyTopupOrder, rows []AdtyManualRefund, extraReconcileOrphanID, extraReconcileQuota int64) (int64, error) {
	if tx == nil || ext == nil || strings.TrimSpace(ext.TradeNo) == "" {
		return 0, nil
	}
	face := ext.FaceQuota()
	if face <= 0 {
		return 0, nil
	}
	hasOrphan, err := AdtyTableExists(tx, &PaymentOrphanEvent{})
	if err != nil {
		return 0, err
	}
	hasReview, err := AdtyTableExists(tx, &PaymentOrphanReview{})
	if err != nil {
		return 0, err
	}
	if !hasOrphan && !hasReview {
		return 0, nil
	}
	var maxExternalQuota int64
	reviewByID := map[int64]PaymentOrphanReview{}
	if hasReview {
		var reviews []PaymentOrphanReview
		if err := tx.Where("trade_no = ? AND status IN ?", ext.TradeNo, []string{PaymentOrphanStatusPendingReview, PaymentOrphanStatusRefunded}).
			Order("id ASC").Find(&reviews).Error; err != nil {
			return 0, err
		}
		for _, r := range reviews {
			reviewByID[r.ID] = r
		}
	}
	pendingOrphanCap := map[int64]int64{}
	if hasOrphan {
		var events []PaymentOrphanEvent
		if err := tx.Where("reference_id = ? AND status IN ?", ext.TradeNo, []string{PaymentOrphanStatusPendingReview, PaymentOrphanStatusRefunded}).
			Order("id ASC").Find(&events).Error; err != nil {
			return 0, err
		}
		for _, ev := range events {
			q := adtyOrphanExternalQuota(ext, ev, reviewByID)
			if q > maxExternalQuota {
				maxExternalQuota = q
			}
			authCap := adtyOrphanAuthorizedReconcileQuota(ext, ev, reviewByID)
			if ev.Status == PaymentOrphanStatusPendingReview && isExternalReversalEventType(ev.EventID, ev.EventType) && authCap > 0 {
				pendingOrphanCap[ev.ID] = authCap
			}
		}
	} else if hasReview {
		for _, rev := range reviewByID {
			if rev.ExternalAmount.IsPositive() {
				q := adtyConvertExternalOffsetToQuota(ext, rev.ExternalAmount, rev.ExternalCurrency)
				if q > face {
					q = face
				}
				if q > maxExternalQuota {
					maxExternalQuota = q
				}
			}
		}
	}
	activeReconcileByOrphan := map[int64]int64{}
	for _, row := range rows {
		if row.ReconcileOrphanId <= 0 || row.Status == AdtyManualRefundStatusCompleted || row.Status == AdtyManualRefundStatusTerminated {
			continue
		}
		if pendingOrphanCap[row.ReconcileOrphanId] > 0 && row.RequestQuota > 0 {
			activeReconcileByOrphan[row.ReconcileOrphanId] += row.RequestQuota
		}
	}
	if extraReconcileOrphanID > 0 && extraReconcileQuota > 0 && pendingOrphanCap[extraReconcileOrphanID] > 0 {
		activeReconcileByOrphan[extraReconcileOrphanID] += extraReconcileQuota
	}
	var activeReconcileQuota int64
	for oid, bound := range activeReconcileByOrphan {
		capQuota := pendingOrphanCap[oid]
		if bound > capQuota {
			bound = capQuota
		}
		activeReconcileQuota += bound
	}
	if activeReconcileQuota > maxExternalQuota {
		activeReconcileQuota = maxExternalQuota
	}
	accounted := ext.RefundQuota + activeReconcileQuota
	if maxExternalQuota <= accounted {
		return 0, nil
	}
	return maxExternalQuota - accounted, nil
}

func adtyBuildManualRefundOccupancy(ext *AdtyTopupOrder, held, unknown, externalOffset int64) *AdtyManualRefundOccupancy {
	face := ext.FaceQuota()
	completed := ext.RefundQuota
	if completed < 0 {
		completed = 0
	}
	if held < 0 {
		held = 0
	}
	if unknown < 0 {
		unknown = 0
	}
	if externalOffset < 0 {
		externalOffset = 0
	}
	occupied := completed + held + unknown + externalOffset
	remaining := face - occupied
	if remaining < 0 {
		remaining = 0
	}
	return &AdtyManualRefundOccupancy{
		FaceQuota:           face,
		CompletedQuota:      completed,
		InProgressQuota:     held,
		UnknownQuota:        unknown,
		ExternalOffsetQuota: externalOffset,
		OccupiedQuota:       occupied,
		RemainingQuota:      remaining,
		FaceUSD:             ext.FaceUSD.String(),
		CompletedUSD:        adtyQuotaUSDString(completed),
		InProgressUSD:       adtyQuotaUSDString(held),
		UnknownUSD:          adtyQuotaUSDString(unknown),
		ExternalOffsetUSD:   adtyQuotaUSDString(externalOffset),
		OccupiedUSD:         adtyQuotaUSDString(occupied),
		RemainingUSD:        adtyQuotaUSDString(remaining),
	}
}

func adtySumManualRefundOccupancy(rows []AdtyManualRefund) (held, unknown int64) {
	for _, row := range rows {
		switch adtyManualRefundOccupancyBucket(row) {
		case "H":
			held += row.RequestQuota
		case "U":
			unknown += row.RequestQuota
		}
	}
	return held, unknown
}

func lockAdtyManualRefundsTx(tx *gorm.DB, tradeNo string) ([]AdtyManualRefund, error) {
	if err := adtyRequireManualRefundTable(tx); err != nil {
		return nil, err
	}
	var rows []AdtyManualRefund
	if err := lockForUpdate(tx).Where("trade_no = ?", tradeNo).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func lockAdtyManualRefundByIDTx(tx *gorm.DB, tradeNo, refundID string) (*AdtyManualRefund, []AdtyManualRefund, error) {
	rows, err := lockAdtyManualRefundsTx(tx, tradeNo)
	if err != nil {
		return nil, nil, err
	}
	refundID = strings.TrimSpace(refundID)
	for i := range rows {
		if rows[i].RefundId == refundID {
			return &rows[i], rows, nil
		}
	}
	return nil, rows, common.NewMessage("Refund not found")
}

func adtyFindManualRefundByIDTx(tx *gorm.DB, refundID string) (*AdtyManualRefund, error) {
	if err := adtyRequireManualRefundTable(tx); err != nil {
		return nil, err
	}
	var rows []AdtyManualRefund
	if err := tx.Where("refund_id = ?", refundID).Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}
