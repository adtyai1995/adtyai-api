package model

import (
	"context"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AdtyBillingHoldOpen     = "open"
	AdtyBillingHoldResolved = "resolved"
)

// ErrAdtyBillingHoldTableMissing is returned when shortfall holds cannot be
// recorded or checked because the hold table is absent.
var ErrAdtyBillingHoldTableMissing = errors.New("adty billing hold table is missing")

// ErrAdtyUserPaidPaused is retained for compatibility; per DEC-003,
// shortfall holds are recorded for finance review and do not pause new paid
// requests on personal, owner, or team payer domains.
var ErrAdtyUserPaidPaused = errors.New("new paid requests are paused while a billing shortfall hold is open")

// AdtyBillingHold records a same-account billing shortfall for finance review.
// Per DEC-003, wallets never go negative; uncollected shortfall is recorded
// here without pausing subsequent paid requests (each request is still subject
// to its own pre-consume balance sufficiency check).
type AdtyBillingHold struct {
	Id             int64  `json:"id"`
	UserId         int    `json:"user_id" gorm:"index"`
	RequestId      string `json:"request_id" gorm:"uniqueIndex;type:varchar(64)"`
	ShortfallQuota int    `json:"shortfall_quota"`
	Status         string `json:"status" gorm:"index;type:varchar(16)"`
	CreatedAt      int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtyBillingHold) TableName() string { return "adty_billing_holds" }

// RecordAdtyBillingHold stores one shortfall. Replays of the same request do not add another hold.
func RecordAdtyBillingHold(userID int, requestID string, shortfall int) error {
	if userID <= 0 || shortfall <= 0 {
		return nil
	}
	if err := adtyRequireBillingHoldTable(); err != nil {
		return err
	}
	return RecordAdtyBillingHoldTx(DB, userID, requestID, shortfall)
}

// RecordAdtyBillingHoldTx 在调用方的事务里写一条短差额挂账：差额与扣款必须同生共死，
// 分开写会出现「款扣了、挂账没落」这种对账再也修不回来的状态。
// 表不在就返回错误由调用方决定，这里不吞。
// 同一 request_id 出现累计更高短差额时（如 Realtime WSS 多分片 + 终结欠费）单调更新为最新总额（R2-016）。
func RecordAdtyBillingHoldTx(tx *gorm.DB, userID int, requestID string, shortfall int) error {
	if userID <= 0 || shortfall <= 0 {
		return nil
	}
	if tx == nil || !tx.Migrator().HasTable(&AdtyBillingHold{}) {
		return ErrAdtyBillingHoldTableMissing
	}
	if requestID == "" {
		requestID = common.NewRequestId()
	}
	row := AdtyBillingHold{UserId: userID, RequestId: requestID, ShortfallQuota: shortfall, Status: AdtyBillingHoldOpen}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&row).Error; err != nil {
		return err
	}
	var existing AdtyBillingHold
	if err := lockForUpdate(tx).Where("request_id = ?", requestID).First(&existing).Error; err != nil {
		return err
	}
	if shortfall > existing.ShortfallQuota {
		return tx.Model(&existing).Updates(map[string]any{
			"shortfall_quota": shortfall,
			"status":          AdtyBillingHoldOpen,
		}).Error
	}
	return nil
}

// UpsertAdtyBillingHoldForOperationTx is kept as an alias for SyncAdtyBillingHoldForOperationTx.
func UpsertAdtyBillingHoldForOperationTx(tx *gorm.DB, op *BillingOperation) error {
	return syncAdtyBillingHoldForOperationTx(tx, op, false)
}

// SyncAdtyBillingHoldForOperationTx computes the authoritative shortfall for a
// request's billing operations and atomically creates, adjusts (up or down), or
// resolves its AdtyBillingHold row inside the settlement transaction (R2-016 / R3-006).
// Once the root request:<reqID> operation reaches BillingSettled or BillingRefunded,
// its shortfall (plus any non-direct child adjustments) replaces Realtime direct:*
// chunk shortfalls so chunks are never double-counted or resurrected.
func SyncAdtyBillingHoldForOperationTx(tx *gorm.DB, op *BillingOperation) error {
	return syncAdtyBillingHoldForOperationTx(tx, op, false)
}

func syncAdtyBillingHoldForOperationTx(tx *gorm.DB, op *BillingOperation, fromBackfill bool) error {
	if tx == nil || op == nil || op.UserId <= 0 {
		return nil
	}
	if !tx.Migrator().HasTable(&AdtyBillingHold{}) {
		return ErrAdtyBillingHoldTableMissing
	}
	reqID := billingHoldRequestIDFromOperationKey(op.OperationKey)
	if reqID == "" {
		reqID = op.OperationKey
	}
	if reqID == "" {
		return nil
	}
	totalShortfall := max(0, op.Shortfall)
	rootFinalized := false
	if tx.Migrator().HasTable(&BillingOperation{}) {
		rootKey := "request:" + reqID
		directPrefix := "direct:" + reqID + ":%"
		var rootOps []BillingOperation
		if err := tx.Select("operation_key", "state", "shortfall", "stream_wallet_quota").
			Where("user_id = ? AND operation_key IN ? AND state IN ?",
				op.UserId, []string{rootKey, reqID}, []string{BillingSettled, BillingRefunded}).
			Find(&rootOps).Error; err != nil {
			return err
		}
		if len(rootOps) > 0 {
			rootFinalized = true
			// R6-009: Once the root request:<reqID> op is finalized, direct:* chunks
			// are unconditionally folded into the root op even when every chunk had
			// AppliedQuota == 0 (StreamWalletQuota == 0) and Shortfall > 0.
			var sum int64
			q := tx.Model(&BillingOperation{}).
				Select("COALESCE(SUM(shortfall), 0)").
				Where("user_id = ? AND state = ? AND shortfall > 0 AND (operation_key = ? OR operation_key = ? OR (earning_root_key IN ? AND operation_key NOT LIKE ?))",
					op.UserId, BillingSettled, reqID, rootKey, []string{reqID, rootKey}, "direct:%")
			if err := q.Scan(&sum).Error; err != nil {
				return err
			}
			totalShortfall = int(max(0, sum))
		} else {
			var sum int64
			if err := tx.Model(&BillingOperation{}).
				Select("COALESCE(SUM(shortfall), 0)").
				Where("user_id = ? AND state = ? AND shortfall > 0 AND (operation_key = ? OR operation_key = ? OR operation_key LIKE ? OR earning_root_key IN ?)",
					op.UserId, BillingSettled, reqID, rootKey, directPrefix, []string{reqID, rootKey}).
				Scan(&sum).Error; err != nil {
				return err
			}
			totalShortfall = int(max(0, sum))
		}
	}
	var existing []AdtyBillingHold
	if err := lockForUpdate(tx).Where("request_id = ?", reqID).Limit(1).Find(&existing).Error; err != nil {
		return err
	}
	if totalShortfall <= 0 {
		if len(existing) > 0 && existing[0].Status == AdtyBillingHoldOpen {
			return tx.Model(&existing[0]).Updates(map[string]any{
				"shortfall_quota": 0,
				"status":          AdtyBillingHoldResolved,
			}).Error
		}
		return nil
	}
	if len(existing) == 0 {
		row := AdtyBillingHold{
			UserId:         op.UserId,
			RequestId:      reqID,
			ShortfallQuota: totalShortfall,
			Status:         AdtyBillingHoldOpen,
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("request_id = ?", reqID).Limit(1).Find(&existing).Error; err != nil {
			return err
		}
		if len(existing) == 0 {
			return nil
		}
	}
	if fromBackfill && existing[0].Status == AdtyBillingHoldResolved {
		return nil
	}
	if rootFinalized {
		if existing[0].ShortfallQuota != totalShortfall || existing[0].Status != AdtyBillingHoldOpen {
			return tx.Model(&existing[0]).Updates(map[string]any{
				"shortfall_quota": totalShortfall,
				"status":          AdtyBillingHoldOpen,
			}).Error
		}
		return nil
	}
	if totalShortfall > existing[0].ShortfallQuota {
		return tx.Model(&existing[0]).Updates(map[string]any{
			"shortfall_quota": totalShortfall,
			"status":          AdtyBillingHoldOpen,
		}).Error
	}
	return nil
}

func billingHoldRequestIDFromOperationKey(opKey string) string {
	opKey = strings.TrimSpace(opKey)
	if strings.HasPrefix(opKey, "request:") {
		return strings.TrimPrefix(opKey, "request:")
	}
	if strings.HasPrefix(opKey, "direct:") {
		rest := strings.TrimPrefix(opKey, "direct:")
		if idx := strings.LastIndexByte(rest, ':'); idx > 0 {
			return rest[:idx]
		}
		return rest
	}
	if strings.HasPrefix(opKey, "violation:") {
		rest := strings.TrimPrefix(opKey, "violation:")
		if idx := strings.LastIndexByte(rest, ':'); idx > 0 {
			return rest[:idx]
		}
		return rest
	}
	return opKey
}

// CheckAdtyBillingHoldForPayerTx previously paused new paid requests when an
// open shortfall hold existed. Per DEC-003 (R1-024), shortfall holds are audit
// and finance-review records only and must never block new paid requests.
func CheckAdtyBillingHoldForPayerTx(tx *gorm.DB, userID int, payerKind string, payerTeamID int64) error {
	return nil
}

// HasOpenAdtyBillingHoldTx reports whether userID has an unresolved personal
// shortfall hold or unprojected settled operation shortfall (used for finance
// inspection/reporting, never to pause paid requests).
func HasOpenAdtyBillingHoldTx(tx *gorm.DB, userID int) (bool, error) {
	if tx == nil || userID <= 0 {
		return false, nil
	}
	hasHoldTable := tx.Migrator().HasTable(&AdtyBillingHold{})
	hasOpTable := tx.Migrator().HasTable(&BillingOperation{})
	if hasHoldTable {
		var openHolds []AdtyBillingHold
		if err := tx.Where("user_id = ? AND status = ?", userID, AdtyBillingHoldOpen).Find(&openHolds).Error; err != nil {
			return false, err
		}
		for _, h := range openHolds {
			if hasOpTable && h.RequestId != "" {
				var teamOps int64
				if err := tx.Model(&BillingOperation{}).
					Where("operation_key IN ? AND payer_kind = ?", []string{"request:" + h.RequestId, h.RequestId}, BillingPayerTeam).
					Count(&teamOps).Error; err != nil {
					return false, err
				}
				if teamOps > 0 {
					continue
				}
			}
			return true, nil
		}
	}
	if hasOpTable {
		var ops []BillingOperation
		if err := tx.Select("operation_key", "user_id", "shortfall").
			Where("user_id = ? AND (payer_kind = '' OR payer_kind = ? OR payer_kind IS NULL) AND state = ? AND shortfall > 0",
				userID, BillingPayerPersonal, BillingSettled).
			Find(&ops).Error; err != nil {
			return false, err
		}
		for _, op := range ops {
			reqID := billingHoldRequestIDFromOperationKey(op.OperationKey)
			if strings.HasPrefix(op.OperationKey, "direct:") && reqID != "" {
				var finalizedStreamRoot int64
				if err := tx.Model(&BillingOperation{}).
					Where("user_id = ? AND operation_key IN ? AND state IN ?",
						userID, []string{"request:" + reqID, reqID}, []string{BillingSettled, BillingRefunded}).
					Count(&finalizedStreamRoot).Error; err != nil {
					return false, err
				}
				if finalizedStreamRoot > 0 {
					continue
				}
			}
			if !hasHoldTable {
				return true, nil
			}
			var resolved int64
			if err := tx.Model(&AdtyBillingHold{}).
				Where("user_id = ? AND request_id IN ? AND status = ?",
					userID, []string{reqID, op.OperationKey}, AdtyBillingHoldResolved).
				Count(&resolved).Error; err != nil {
				return false, err
			}
			if resolved == 0 {
				return true, nil
			}
		}
	}
	return false, nil
}

// BackfillMissingAdtyBillingHolds repairs missing AdtyBillingHold rows for
// settled operations that recorded a positive shortfall while the hold table
// was unavailable or after an interrupted post-settle note.
func BackfillMissingAdtyBillingHolds(ctx context.Context, limit int) error {
	if DB == nil || !DB.Migrator().HasTable(&AdtyBillingHold{}) || !DB.Migrator().HasTable(&BillingOperation{}) {
		return nil
	}
	if limit <= 0 {
		limit = 100
	}
	var ops []BillingOperation
	if err := DB.WithContext(ctx).
		Where("state = ? AND shortfall > 0", BillingSettled).
		Order("updated_at, operation_key").
		Limit(limit).
		Find(&ops).Error; err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, op := range ops {
		reqID := billingHoldRequestIDFromOperationKey(op.OperationKey)
		if reqID == "" {
			reqID = op.OperationKey
		}
		if _, dup := seen[reqID]; dup {
			continue
		}
		seen[reqID] = struct{}{}
		if err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return syncAdtyBillingHoldForOperationTx(tx, &op, true)
		}); err != nil {
			return err
		}
	}
	return nil
}

func adtyRequireBillingHoldTable() error {
	if DB == nil || !DB.Migrator().HasTable(&AdtyBillingHold{}) {
		return ErrAdtyBillingHoldTableMissing
	}
	return nil
}
