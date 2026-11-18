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

// ErrAdtyUserPaidPaused is returned when an unresolved shortfall hold pauses
// new paid requests on the same payer domain (DEC-034 / G3.6).
var ErrAdtyUserPaidPaused = errors.New("new paid requests are paused while a billing shortfall hold is open")

// AdtyBillingHold is the same-account shortfall waiting for finance review.
// Per DEC-034 (G3.6), an open shortfall hold pauses NEW paid requests on the
// same payer domain until resolved, without blocking free requests, channel
// retries on already-reserved sessions, or in-flight settlements/refunds.
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
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&row).Error
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
	return opKey
}

// CheckAdtyBillingHoldForPayerTx checks whether the given payer domain has an
// unresolved shortfall hold or an unprojected settled operation shortfall.
func CheckAdtyBillingHoldForPayerTx(tx *gorm.DB, userID int, payerKind string, payerTeamID int64) error {
	if tx == nil || userID <= 0 {
		return nil
	}
	hasHoldTable := tx.Migrator().HasTable(&AdtyBillingHold{})
	hasOpTable := tx.Migrator().HasTable(&BillingOperation{})

	if payerKind == BillingPayerTeam && payerTeamID > 0 {
		if hasOpTable {
			var ops []BillingOperation
			if err := tx.Select("operation_key", "user_id", "shortfall").
				Where("payer_kind = ? AND payer_team_id = ? AND state = ? AND shortfall > 0", BillingPayerTeam, payerTeamID, BillingSettled).
				Find(&ops).Error; err != nil {
				return err
			}
			for _, op := range ops {
				if !hasHoldTable {
					return ErrAdtyUserPaidPaused
				}
				reqID := billingHoldRequestIDFromOperationKey(op.OperationKey)
				var resolved int64
				if err := tx.Model(&AdtyBillingHold{}).
					Where("request_id IN ? AND status = ?", []string{reqID, op.OperationKey}, AdtyBillingHoldResolved).
					Count(&resolved).Error; err != nil {
					return err
				}
				if resolved == 0 {
					return ErrAdtyUserPaidPaused
				}
			}
		}
		return nil
	}

	if hasHoldTable {
		var openHolds []AdtyBillingHold
		if err := tx.Where("user_id = ? AND status = ?", userID, AdtyBillingHoldOpen).Find(&openHolds).Error; err != nil {
			return err
		}
		for _, h := range openHolds {
			if hasOpTable && h.RequestId != "" {
				var teamOps int64
				if err := tx.Model(&BillingOperation{}).
					Where("operation_key IN ? AND payer_kind = ?", []string{"request:" + h.RequestId, h.RequestId}, BillingPayerTeam).
					Count(&teamOps).Error; err != nil {
					return err
				}
				if teamOps > 0 {
					continue
				}
			}
			return ErrAdtyUserPaidPaused
		}
	}

	if hasOpTable {
		var ops []BillingOperation
		if err := tx.Select("operation_key", "user_id", "shortfall").
			Where("user_id = ? AND (payer_kind = '' OR payer_kind = ? OR payer_kind IS NULL) AND state = ? AND shortfall > 0",
				userID, BillingPayerPersonal, BillingSettled).
			Find(&ops).Error; err != nil {
			return err
		}
		for _, op := range ops {
			if !hasHoldTable {
				return ErrAdtyUserPaidPaused
			}
			reqID := billingHoldRequestIDFromOperationKey(op.OperationKey)
			var resolved int64
			if err := tx.Model(&AdtyBillingHold{}).
				Where("user_id = ? AND (request_id IN ? OR status = ?) AND status = ?",
					userID, []string{reqID, op.OperationKey}, AdtyBillingHoldResolved, AdtyBillingHoldResolved).
				Count(&resolved).Error; err != nil {
				return err
			}
			if resolved == 0 {
				return ErrAdtyUserPaidPaused
			}
		}
	}
	return nil
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
	for _, op := range ops {
		reqID := billingHoldRequestIDFromOperationKey(op.OperationKey)
		if reqID == "" {
			reqID = op.OperationKey
		}
		var count int64
		if err := DB.WithContext(ctx).Model(&AdtyBillingHold{}).
			Where("request_id IN ?", []string{reqID, op.OperationKey}).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if err := RecordAdtyBillingHoldTx(DB.WithContext(ctx), op.UserId, reqID, op.Shortfall); err != nil {
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
