package model

import (
	"errors"
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// Available excludes both consumed and reserved quota. Top-up lots retain their
// original trade number even after a team moves to another site.
type AdtyTeamFundLot struct {
	Id          int64
	TeamId      int64  `gorm:"uniqueIndex:ux_team_lot_source,priority:1"`
	Kind        string `gorm:"type:varchar(16);uniqueIndex:ux_team_lot_source,priority:2"`
	Source      string `gorm:"type:varchar(191);uniqueIndex:ux_team_lot_source,priority:3"`
	Original    int64
	Available   int64
	EffectiveAt int64 `gorm:"not null;default:0"`
	Reclaimed   int64
}

type AdtyTeamFundAllocation struct {
	Id           int64
	TeamId       int64  `gorm:"index:idx_team_alloc_root,priority:1"`
	LotId        int64  `gorm:"index"` // zero identifies pre-lot funds of unknown origin
	OperationKey string `gorm:"type:varchar(191);index"`
	RootKey      string `gorm:"type:varchar(191);index:idx_team_alloc_root,priority:2"`
	Quota        int64
	Returned     int64
	Reserved     bool
}

// Callers hold the team row lock. All lot changes commit with the balance and ledger.
func ApplyAdtyTeamFundMovementTx(tx *gorm.DB, team *AdtyTeam, kind, ref string, delta int64, effectiveAt ...int64) error {
	if kind == "topup" || kind == "credit" {
		at := common.GetTimestamp()
		if len(effectiveAt) > 0 && effectiveAt[0] > 0 {
			at = effectiveAt[0]
		}
		return tx.Create(&AdtyTeamFundLot{TeamId: team.Id, Kind: kind, Source: ref, Original: delta, Available: delta, EffectiveAt: at}).Error
	}
	root := ref
	var op BillingOperation
	if kind != "spend" {
		if err := tx.Select("earning_root_key", "kind", "stream_wallet_quota").Where("operation_key = ?", ref).Limit(1).Find(&op).Error; err != nil {
			return err
		}
		if op.EarningRootKey != "" {
			root = op.EarningRootKey
		}
	}
	if kind == "refund" {
		// Task/MJ adjustments refund the original charge; a regular request only
		// releases its own allocations, even when other chunks share its root.
		q := tx.Where("team_id = ? AND quota > returned", team.Id)
		if op.Kind == "task" || op.Kind == "mj_refund" {
			q = q.Where("root_key = ?", root)
		} else if op.Kind == "request" && op.StreamWalletQuota > 0 {
			// The request settles its own reservation and the direct stream
			// charges already included in its cumulative wallet amount.
			chunks := tx.Model(&BillingOperation{}).Select("operation_key").Where("kind = ? AND earning_root_key = ? AND payer_team_id = ?", "direct", root, team.Id)
			q = q.Where("(operation_key = ? OR operation_key IN (?))", ref, chunks)
		} else {
			q = q.Where("operation_key = ?", ref)
		}
		var shares []AdtyTeamFundAllocation
		if err := q.Order("id desc").Find(&shares).Error; err != nil {
			return err
		}
		left := delta
		for _, share := range shares {
			if left == 0 {
				break
			}
			amount := min(left, share.Quota-share.Returned)
			if share.LotId > 0 {
				if err := tx.Model(&AdtyTeamFundLot{}).Where("team_id = ? AND id = ?", team.Id, share.LotId).Update("available", gorm.Expr("available + ?", amount)).Error; err != nil {
					return err
				}
			}
			if err := tx.Model(&share).Update("returned", share.Returned+amount).Error; err != nil {
				return err
			}
			left -= amount
		}
		// Older operations have no source evidence. The residual credit remains
		// unassigned and cannot make a particular top-up refundable.
		return nil
	}
	var tracked int64
	if err := tx.Model(&AdtyTeamFundLot{}).Where("team_id = ?", team.Id).Select("COALESCE(SUM(available),0)").Scan(&tracked).Error; err != nil {
		return err
	}
	if tracked > team.SharedQuota || -delta > team.SharedQuota {
		return ErrBillingOperationConflict
	}
	left := -delta
	record := func(lotID, amount int64) error {
		return tx.Create(&AdtyTeamFundAllocation{TeamId: team.Id, LotId: lotID, OperationKey: ref, RootKey: root, Quota: amount, Reserved: kind == "reserve"}).Error
	}
	if legacy := min(left, team.SharedQuota-tracked); legacy > 0 {
		if err := record(0, legacy); err != nil {
			return err
		}
		left -= legacy
	}
	for left > 0 {
		var lots []AdtyTeamFundLot
		if err := tx.Where("team_id = ? AND available > 0", team.Id).Order("effective_at, source, id").Limit(64).Find(&lots).Error; err != nil {
			return err
		}
		if len(lots) == 0 {
			return ErrBillingOperationConflict
		}
		for _, lot := range lots {
			amount := min(left, lot.Available)
			if err := tx.Model(&lot).Update("available", lot.Available-amount).Error; err != nil {
				return err
			}
			if err := record(lot.Id, amount); err != nil {
				return err
			}
			left -= amount
			if left == 0 {
				break
			}
		}
	}
	return nil
}

func adtyTeamLotReserved(tx *gorm.DB, lotID int64) (int64, error) {
	var held int64
	err := tx.Model(&AdtyTeamFundAllocation{}).Where("lot_id = ? AND reserved = ?", lotID, true).Select("COALESCE(SUM(quota-returned),0)").Scan(&held).Error
	return held, err
}

func reclaimAdtyTeamLotTx(tx *gorm.DB, team *AdtyTeam, tradeNo string, requested int64) (int64, error) {
	var lot AdtyTeamFundLot
	q := tx.Where("team_id = ? AND kind = ? AND source = ?", team.Id, "topup", tradeNo).Limit(1).Find(&lot)
	if q.Error != nil {
		return 0, q.Error
	}
	if q.RowsAffected == 0 {
		return 0, common.NewMessage("This team recharge requires historical principal review")
	}
	if lot.Available > team.SharedQuota {
		return 0, ErrBillingOperationConflict
	}
	if requested > lot.Available {
		held, err := adtyTeamLotReserved(tx, lot.Id)
		if err != nil {
			return 0, err
		}
		if held > 0 {
			return 0, common.NewMessage("This order has reserved principal awaiting settlement")
		}
	}
	available := lot.Available
	amount := min(requested, available)
	if amount > 0 {
		if err := tx.Model(&lot).Updates(map[string]any{"available": lot.Available - amount, "reclaimed": lot.Reclaimed + amount}).Error; err != nil {
			return 0, err
		}
	}
	return available, nil
}

// The caller holds the original order and refund locks. Release exactly the
// amount the reclaim step actually deducted, including for a closed team.
func releaseAdtyTeamRefundTx(tx *gorm.DB, ext *AdtyTopupOrder, row *AdtyManualRefund, operatorID int) error {
	var team AdtyTeam
	if err := AdtyLockForUpdate(tx).First(&team, ext.TeamId).Error; err != nil {
		return err
	}
	amount := row.DeductedPrincipalQuota
	var debits []AdtyTeamLedger
	if err := tx.Where("type = ? AND ref_id = ?", "manual_refund", row.RefundId).Limit(2).Find(&debits).Error; err != nil {
		return err
	}
	if amount <= 0 || len(debits) != 1 || debits[0].TeamId != team.Id || debits[0].DeltaQuota != -amount || debits[0].BalanceBefore-debits[0].BalanceAfter != amount || debits[0].IdempotencyKey != "manual_refund:"+row.ReclaimIdemKey {
		return ErrBillingOperationConflict
	}
	key := "refund_release:" + row.RefundId
	var releases []AdtyTeamLedger
	result := tx.Where("idempotency_key = ? OR (type = ? AND ref_id = ?)", key, "refund_release", row.RefundId).Limit(2).Find(&releases)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		prior := releases[0]
		if len(releases) != 1 || prior.Type != "refund_release" || prior.BalanceAfter-prior.BalanceBefore != amount || prior.TeamId != team.Id || prior.DeltaQuota != amount || prior.RefId != row.RefundId {
			return ErrBillingOperationConflict
		}
		var lotCount int64
		if err := tx.Model(&AdtyTeamFundLot{}).Where("team_id=? AND kind='topup' AND source=?", team.Id, ext.TradeNo).Count(&lotCount).Error; err != nil {
			return err
		}
		if lotCount != 1 {
			return ErrBillingOperationConflict
		}
		return nil
	}
	if team.SharedQuota > common.MaxWalletQuota-amount {
		return ErrBillingOperationConflict
	}
	if err := RejectRestrictedAdtyFundWrite(tx, AdtyFundWrite{SiteID: ext.SiteId, AccountID: ext.UserId, OrderRef: ext.TradeNo}); err != nil {
		return err
	}
	lot := tx.Model(&AdtyTeamFundLot{}).Where("team_id = ? AND kind = 'topup' AND source = ? AND reclaimed >= ?", team.Id, ext.TradeNo, amount).
		Updates(map[string]any{"available": gorm.Expr("available + ?", amount), "reclaimed": gorm.Expr("reclaimed - ?", amount)})
	if lot.Error != nil {
		return lot.Error
	}
	if lot.RowsAffected != 1 {
		return ErrBillingOperationConflict
	}
	after := team.SharedQuota + amount
	if err := tx.Model(&team).UpdateColumn("shared_quota", after).Error; err != nil {
		return err
	}
	return tx.Create(&AdtyTeamLedger{TeamId: team.Id, RefId: row.RefundId, Type: "refund_release",
		ActorUserId: operatorID, IdempotencyKey: key,
		DeltaQuota: amount, BalanceBefore: after - amount, BalanceAfter: after}).Error
}

// Scan by primary key so inconclusive cases do not block later repairs.
func backfillAdtyTerminatedTeamRefunds(db *gorm.DB) error {
	var cursor int64
	for {
		var rows []AdtyManualRefund
		if err := db.Where("id > ? AND status = ?", cursor, AdtyManualRefundStatusTerminated).
			Where("EXISTS (SELECT 1 FROM adty_topup_orders o WHERE o.trade_no=adty_manual_refunds.trade_no AND o.team_id>0)").
			Order("id").Limit(100).Find(&rows).Error; err != nil {
			return err
		}
		for _, candidate := range rows {
			err := db.Transaction(func(tx *gorm.DB) error {
				var ext AdtyTopupOrder
				if err := AdtyLockForUpdate(tx).Where("trade_no = ?", candidate.TradeNo).First(&ext).Error; err != nil {
					return err
				}
				var row AdtyManualRefund
				if err := AdtyLockForUpdate(tx).First(&row, candidate.Id).Error; err != nil {
					return err
				}
				if row.Status != AdtyManualRefundStatusTerminated || ext.TeamId == 0 {
					return nil
				}
				var reviewed int64
				if err := tx.Model(&AdtyAnomalyDisposition{}).Where("kind=? AND ref=?", "team_refund_release_review", row.RefundId).Count(&reviewed).Error; err != nil {
					return err
				}
				if reviewed > 0 {
					return nil
				}
				if row.DeductedPrincipalQuota == 0 {
					var ledgerCount int64
					if err := tx.Model(&AdtyTeamLedger{}).Where("type IN ? AND ref_id=?", []string{"manual_refund", "refund_release"}, row.RefundId).Count(&ledgerCount).Error; err != nil {
						return err
					}
					if ledgerCount == 0 && (row.ReclaimedAt == 0 || row.ShortfallQuota == row.RequestQuota) {
						return nil
					}
				} else {
					err := releaseAdtyTeamRefundTx(tx, &ext, &row, row.TerminateOperatorId)
					if err == nil {
						return nil
					}
					if !errors.Is(err, ErrBillingOperationConflict) && !errors.Is(err, gorm.ErrRecordNotFound) {
						return err
					}
				}
				return tx.Create(&AdtyAnomalyDisposition{Kind: "team_refund_release_review", Ref: row.RefundId, OrderRef: ext.TradeNo, SiteId: ext.SiteId, TeamId: ext.TeamId, AccountId: ext.UserId, Status: "open", Note: "Verify original team refund debit, lot and release before returning principal"}).Error
			})
			if err != nil {
				return err
			}
			cursor = candidate.Id
		}
		if len(rows) < 100 {
			return nil
		}
	}
}
