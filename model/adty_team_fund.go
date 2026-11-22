package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// Available excludes both consumed and reserved quota. Top-up lots retain their
// original trade number even after a team moves to another site.
type AdtyTeamFundLot struct {
	Id        int64
	TeamId    int64  `gorm:"uniqueIndex:ux_team_lot_source,priority:1"`
	Kind      string `gorm:"type:varchar(16);uniqueIndex:ux_team_lot_source,priority:2"`
	Source    string `gorm:"type:varchar(191);uniqueIndex:ux_team_lot_source,priority:3"`
	Original  int64
	Available int64
	Reclaimed int64
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
func ApplyAdtyTeamFundMovementTx(tx *gorm.DB, team *AdtyTeam, kind, ref string, delta int64) error {
	if kind == "topup" || kind == "credit" {
		return tx.Create(&AdtyTeamFundLot{TeamId: team.Id, Kind: kind, Source: ref, Original: delta, Available: delta}).Error
	}
	root := ref
	var op BillingOperation
	if kind != "spend" {
		if err := tx.Select("earning_root_key", "kind").Where("operation_key = ?", ref).Limit(1).Find(&op).Error; err != nil {
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
		if err := tx.Where("team_id = ? AND available > 0", team.Id).Order("id").Limit(64).Find(&lots).Error; err != nil {
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
