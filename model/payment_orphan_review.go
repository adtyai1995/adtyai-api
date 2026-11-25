package model

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var paymentReversalAmountPattern = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,8})?$`)

type PaymentOrphanReviewRequest struct {
	Status           string `json:"status"`
	TradeNo          string `json:"trade_no"`
	ExternalAmount   string `json:"external_amount"`
	ExternalCurrency string `json:"external_currency"`
	Proof            string `json:"proof"`
	Note             string `json:"note"`
	DismissalReason  string `json:"dismissal_reason"`
}

// PaymentOrphanReview is an append-only financial evidence snapshot. Amounts
// are cumulative for one payment, never increments to sum across notifications.
// A pending review records facts without authorizing a partial wallet clawback.
type PaymentOrphanReview struct {
	ID               int64           `json:"id"`
	OrphanID         int64           `json:"orphan_id" gorm:"index"`
	OperatorID       int             `json:"operator_id"`
	CreatedAt        int64           `json:"created_at" gorm:"autoCreateTime"`
	Status           string          `json:"status" gorm:"type:varchar(32)"`
	Provider         string          `json:"provider" gorm:"type:varchar(32)"`
	EventID          string          `json:"event_id" gorm:"type:varchar(128)"`
	EventType        string          `json:"event_type" gorm:"type:varchar(128)"`
	SessionID        string          `json:"session_id" gorm:"type:varchar(128)"`
	PayloadHash      string          `json:"payload_hash" gorm:"type:varchar(64)"`
	TradeNo          string          `json:"trade_no" gorm:"type:varchar(255);index"`
	ExternalAmount   decimal.Decimal `json:"external_amount" gorm:"type:decimal(20,8);not null;default:0"`
	ExternalCurrency string          `json:"external_currency" gorm:"type:varchar(16)"`
	Proof            string          `json:"proof" gorm:"type:varchar(512)"`
	Note             string          `json:"note" gorm:"type:text"`
	DismissalReason  string          `json:"dismissal_reason" gorm:"type:varchar(32)"`
	RefundLedgerID   int64           `json:"refund_ledger_id"`
	DeductedQuota    int64           `json:"deducted_quota"`
	RefundQuota      int64           `json:"refund_quota"`
	RefundAmount     decimal.Decimal `json:"refund_amount" gorm:"type:decimal(20,8);not null;default:0"`
	PaidAmount       decimal.Decimal `json:"paid_amount" gorm:"type:decimal(20,8);not null;default:0"`
	PaidCurrency     string          `json:"paid_currency" gorm:"type:varchar(16)"`
	ExposureAmount   decimal.Decimal `json:"exposure_amount" gorm:"type:decimal(20,8);not null;default:0"`
	Request          string          `json:"-" gorm:"type:text"`
}

func (PaymentOrphanReview) TableName() string { return "adty_payment_orphan_reviews" }

// ResolvePaymentOrphanReview never changes a wallet. A refunded disposition
// requires the existing authorized refund flow and its same-order ledger.
func ResolvePaymentOrphanReview(id int64, operatorID int, request PaymentOrphanReviewRequest) (*PaymentOrphanReview, error) {
	return ResolvePaymentOrphanReviewForActor(id, operatorID, 0, false, request)
}

func ResolvePaymentOrphanReviewForActor(id int64, operatorID, actorRole int, denyAdminTarget bool, request PaymentOrphanReviewRequest) (*PaymentOrphanReview, error) {
	request.Status = strings.TrimSpace(request.Status)
	request.TradeNo = strings.TrimSpace(request.TradeNo)
	request.ExternalCurrency = strings.ToUpper(strings.TrimSpace(request.ExternalCurrency))
	request.ExternalAmount = strings.TrimSpace(request.ExternalAmount)
	request.Proof, request.Note = strings.TrimSpace(request.Proof), strings.TrimSpace(request.Note)
	request.DismissalReason = strings.TrimSpace(request.DismissalReason)
	if operatorID <= 0 || request.TradeNo == "" || len(request.TradeNo) > 255 || request.Proof == "" || len(request.Proof) > 512 || request.Note == "" || len(request.Note) > 2000 {
		return nil, common.NewMessage("Order number, proof and note are required")
	}
	amount := decimal.Zero
	if request.Status == PaymentOrphanStatusDismissed {
		if request.DismissalReason != "not_this_order" && request.DismissalReason != "reversal_canceled" {
			return nil, common.NewMessage("Confirm why this event does not block payment")
		}
		request.ExternalAmount, request.ExternalCurrency = "0", ""
	} else {
		var err error
		if !paymentReversalAmountPattern.MatchString(request.ExternalAmount) {
			return nil, common.NewMessage("Invalid external reversal amount or currency")
		}
		amount, err = decimal.NewFromString(request.ExternalAmount)
		if err != nil || amount.IsNegative() || amount.GreaterThan(decimal.New(1, 12).Sub(decimal.New(1, -8))) || !amount.Equal(amount.Truncate(8)) || len(request.ExternalCurrency) < 3 || len(request.ExternalCurrency) > 16 || request.DismissalReason != "" {
			return nil, common.NewMessage("Invalid external reversal amount or currency")
		}
		for _, letter := range request.ExternalCurrency {
			if letter < 'A' || letter > 'Z' {
				return nil, common.NewMessage("Invalid external reversal amount or currency")
			}
		}
		request.ExternalAmount = amount.String()
	}
	switch request.Status {
	case PaymentOrphanStatusPendingReview, PaymentOrphanStatusDismissed:
	case PaymentOrphanStatusRefunded, PaymentOrphanStatusNeverCredited:
		if !amount.IsPositive() {
			return nil, common.NewMessage("A confirmed external reversal amount is required")
		}
	default:
		return nil, ErrPaymentOrphanNotCredit
	}
	canonical, err := common.Marshal(request)
	if err != nil {
		return nil, err
	}
	var result *PaymentOrphanReview
	err = DB.Transaction(func(tx *gorm.DB) error {
		// The same lock order as callbacks, queries and refunds prevents a
		// never-credited conclusion racing a successful payment.
		orphan, topUp, ext, err := LockPaymentOrphanOrderTx(tx, id)
		if err != nil {
			return err
		}
		if actorRole > 0 || denyAdminTarget {
			if topUp == nil {
				if actorRole > 0 && actorRole != common.RoleRootUser {
					return ErrUserQuotaPermission
				}
			} else {
				if actorRole > 0 && actorRole != common.RoleRootUser && (ext == nil || ext.SiteId != 0) {
					return ErrUserQuotaPermission
				}
				if ext != nil && ext.TeamId > 0 && tx.Migrator().HasTable(&AdtyTeam{}) {
					var team AdtyTeam
					if err := lockForUpdate(tx).Select("id").Where("id = ?", ext.TeamId).Limit(1).Find(&team).Error; err != nil {
						return err
					}
				}
				var target User
				if err := lockForUpdate(tx).Select("id", "role").First(&target, topUp.UserId).Error; err != nil {
					return err
				}
				if operatorID > 0 && operatorID == target.Id {
					return ErrUserQuotaPermission
				}
				if actorRole > 0 && actorRole != common.RoleRootUser && actorRole <= target.Role {
					return ErrUserQuotaPermission
				}
				if denyAdminTarget && target.Role >= common.RoleAdminUser {
					return ErrAdminTargetSessionRequired
				}
			}
		}
		if request.TradeNo != orphan.ReferenceID {
			return common.NewMessage("The review must reference the original order")
		}
		if orphan.ReviewID > 0 {
			var previous PaymentOrphanReview
			if err := tx.First(&previous, orphan.ReviewID).Error; err != nil {
				return err
			}
			if previous.Request == string(canonical) {
				result = &previous
				return nil
			}
		}
		if orphan.Status != PaymentOrphanStatusPendingReview {
			return ErrAdtyIdempotencyConflict
		}
		if topUp != nil && (topUp.PaymentProvider != orphan.Provider || (ext != nil && (ext.Provider != orphan.Provider || ext.UserId != topUp.UserId))) {
			return ErrPaymentMethodMismatch
		}
		if tx.Migrator().HasTable(&AdtyManualRefund{}) {
			if request.Status == PaymentOrphanStatusDismissed || request.Status == PaymentOrphanStatusNeverCredited {
				var activeBound int64
				if err := tx.Model(&AdtyManualRefund{}).
					Where("reconcile_orphan_id = ? AND status NOT IN ?", id, []string{AdtyManualRefundStatusCompleted, AdtyManualRefundStatusTerminated}).
					Count(&activeBound).Error; err != nil {
					return err
				}
				if activeBound > 0 {
					return common.NewMessage("Terminate active manual refunds bound to this external reversal before resolving")
				}
			}
			if request.Status == PaymentOrphanStatusPendingReview && ext != nil {
				var boundQuota int64
				if err := tx.Model(&AdtyManualRefund{}).
					Select("COALESCE(SUM(request_quota), 0)").
					Where("reconcile_orphan_id = ? AND status <> ?", id, AdtyManualRefundStatusTerminated).
					Scan(&boundQuota).Error; err != nil {
					return err
				}
				if boundQuota > 0 && (!amount.IsPositive() || adtyConvertExternalOffsetToQuota(ext, amount, request.ExternalCurrency) < boundQuota) {
					return common.NewMessage("External reversal amount cannot be less than manual refunds already bound to it")
				}
			}
		}
		review := &PaymentOrphanReview{
			OrphanID: id, OperatorID: operatorID, Status: request.Status, TradeNo: request.TradeNo,
			ExternalAmount: amount, ExternalCurrency: request.ExternalCurrency, Proof: request.Proof, Note: request.Note,
			DismissalReason: request.DismissalReason, Request: string(canonical),
			Provider: orphan.Provider, EventID: orphan.EventID, EventType: orphan.EventType, SessionID: orphan.SessionID,
		}
		payloadHash := sha256.Sum256([]byte(orphan.Payload))
		review.PayloadHash = hex.EncodeToString(payloadHash[:])
		if ext != nil {
			review.PaidAmount, review.PaidCurrency = ext.PaidAmount, ext.PaidCurrency
		}
		switch request.Status {
		case PaymentOrphanStatusRefunded:
			if topUp == nil || ext == nil || topUp.Status != TopUpStatusRefunded || !ext.PaidAmount.IsPositive() || !strings.EqualFold(ext.PaidCurrency, request.ExternalCurrency) || !amount.Equal(ext.PaidAmount) {
				return common.NewMessage("A full external reversal and the original wallet refund are required")
			}
			var completedRefunds []AdtyManualRefund
			if tx.Migrator().HasTable(&AdtyManualRefund{}) {
				if err := tx.Where("trade_no = ? AND status = ?", topUp.TradeNo, AdtyManualRefundStatusCompleted).Order("id asc").Find(&completedRefunds).Error; err != nil {
					return err
				}
			}
			var deducted int64
			var refundLedgerID int64
			if ext.TeamId > 0 {
				var teamLedgers []AdtyTeamLedger
				if tx.Migrator().HasTable(&AdtyTeamLedger{}) {
					refIDs := make([]string, 0, len(completedRefunds)+1)
					refIDs = append(refIDs, topUp.TradeNo)
					completedRefSet := map[string]struct{}{}
					for _, cr := range completedRefunds {
						refIDs = append(refIDs, cr.RefundId)
						completedRefSet[cr.RefundId] = struct{}{}
					}
					if err := tx.Where("team_id = ? AND type IN ? AND ref_id IN ?", ext.TeamId, []string{"manual_refund", "refund_release"}, refIDs).Order("id asc").Find(&teamLedgers).Error; err != nil {
						return err
					}
					for _, ledger := range teamLedgers {
						if ledger.DeltaQuota < -int64(common.MaxWalletQuota) || ledger.DeltaQuota > int64(common.MaxWalletQuota) {
							return common.NewMessage("The original wallet refund ledger is required")
						}
						if refundLedgerID == 0 && ledger.DeltaQuota <= 0 {
							if len(completedRefSet) == 0 {
								refundLedgerID = ledger.Id
							} else if _, ok := completedRefSet[ledger.RefId]; ok {
								refundLedgerID = ledger.Id
							}
						}
						deducted += -ledger.DeltaQuota
					}
				}
				if len(completedRefunds) == 0 && len(teamLedgers) == 0 {
					return common.NewMessage("The original wallet refund ledger is required")
				}
			} else {
				var ledgers []AdtyBalanceLedger
				if err := tx.Where("type = ? AND ref_type = ? AND ref_id = ? AND user_id = ?", AdtyLedgerTypeRefund, "topup", topUp.TradeNo, topUp.UserId).Order("id asc").Find(&ledgers).Error; err != nil {
					return err
				}
				if len(ledgers) == 0 {
					return common.NewMessage("The original wallet refund ledger is required")
				}
				completedIdemKeys := map[string]struct{}{}
				for _, cr := range completedRefunds {
					if cr.ReclaimIdemKey != "" {
						completedIdemKeys[cr.ReclaimIdemKey] = struct{}{}
					}
					if cr.CompleteIdemKey != "" {
						completedIdemKeys[cr.CompleteIdemKey] = struct{}{}
					}
				}
				var hasRefundLedger bool
				var fallbackLedgerID int64
				for _, ledger := range ledgers {
					if ledger.DeltaQuota < -int64(common.MaxWalletQuota) || ledger.DeltaQuota > int64(common.MaxWalletQuota) {
						return common.NewMessage("The original wallet refund ledger is required")
					}
					if ledger.DeltaQuota <= 0 {
						hasRefundLedger = true
						if fallbackLedgerID == 0 {
							fallbackLedgerID = ledger.Id
						}
						if refundLedgerID == 0 && ledger.IdempotencyKey != nil {
							if _, ok := completedIdemKeys[*ledger.IdempotencyKey]; ok {
								refundLedgerID = ledger.Id
							}
						}
					}
					deducted += -ledger.DeltaQuota
				}
				if refundLedgerID == 0 {
					refundLedgerID = fallbackLedgerID
				}
				if !hasRefundLedger && len(completedRefunds) == 0 {
					return common.NewMessage("The original wallet refund ledger is required")
				}
			}
			refundUSD := decimal.NewFromInt(ext.RefundQuota).Div(common.QuotaPerUnitDecimal())
			amountOK := ext.RefundAmount.Equal(adtyRefundAmount(ext, ext.RefundQuota, ext.FaceQuota())) ||
				ext.RefundAmount.Equal(refundUSD) ||
				(ext.RefundQuota == ext.FaceQuota() &&
					len(completedRefunds) > 0 &&
					!ext.RefundAmount.IsNegative() &&
					ext.RefundAmount.LessThanOrEqual(ext.PaidAmount))
			if deducted < 0 || deducted > ext.CreditQuota || ext.RefundQuota < 0 || ext.RefundQuota > ext.FaceQuota() || !amountOK {
				return common.NewMessage("The original wallet refund ledger is required")
			}
			review.RefundLedgerID, review.DeductedQuota = refundLedgerID, deducted
			review.RefundQuota, review.RefundAmount = ext.RefundQuota, ext.RefundAmount
			review.ExposureAmount = decimal.Max(amount.Sub(ext.RefundAmount), decimal.Zero)
		case PaymentOrphanStatusNeverCredited:
			if topUp == nil || ext == nil || topUp.CompleteTime != 0 || ext.PaidSource != "" || ext.PaidAmount.IsPositive() || topUp.Status == common.TopUpStatusSuccess || topUp.Status == TopUpStatusRefunded {
				return common.NewMessage("The original order must never have been credited")
			}
			switch topUp.Status {
			case common.TopUpStatusPending, common.TopUpStatusFailed, common.TopUpStatusExpired, TopUpStatusCanceled, TopUpStatusReplaced:
			default:
				return common.NewMessage("The original order must never have been credited")
			}
			var credits int64
			if err := tx.Model(&AdtyBalanceLedger{}).Where("ref_type = ? AND ref_id = ? AND type = ?", "topup", topUp.TradeNo, AdtyLedgerTypeTopUp).Count(&credits).Error; err != nil {
				return err
			}
			if credits != 0 {
				return common.NewMessage("The original order must never have been credited")
			}
			if !strings.EqualFold(ext.PayCurrency, request.ExternalCurrency) {
				return common.NewMessage("The external reversal currency must match the original order")
			}
		}
		if err := tx.Create(review).Error; err != nil {
			return err
		}
		now := common.GetTimestamp()
		resolvedAt := now
		if request.Status == PaymentOrphanStatusPendingReview {
			resolvedAt = 0
		}
		if err := tx.Model(&PaymentOrphanEvent{}).Where("id = ?", id).Updates(map[string]any{
			"status": request.Status, "resolved_by": operatorID, "resolved_at": resolvedAt,
			"resolution": request.Status, "resolution_note": request.Note, "review_id": review.ID, "update_time": now,
		}).Error; err != nil {
			return err
		}
		result = review
		return nil
	})
	return result, err
}
