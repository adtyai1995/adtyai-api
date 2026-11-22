package model

import (
	"fmt"

	"gorm.io/gorm"
)

const (
	BillingPayerPersonal = "personal"
	BillingPayerOwner    = "owner_self_use"
	BillingPayerTeam     = "team"
)

// CaptureBillingPayer records the acceptance-time identity for a task that is
// persisted before its billing operation. The caller must persist both fields.
func CaptureBillingPayer(userId, tokenId int) (*BillingOperation, error) {
	row := BillingOperation{UserId: userId, TokenId: tokenId}
	err := DB.Transaction(func(tx *gorm.DB) error {
		return captureBillingPayerTx(tx, &row)
	})
	return &row, err
}

// A blank kind denotes an upgraded operation whose payer has not been proved.
// It never means "use this token's present binding".
func freezeBillingPayerTx(tx *gorm.DB, row *BillingOperation, newAcceptance bool) error {
	if row.PayerKind != "" {
		if row.PayerKind == BillingPayerPersonal && row.PayerTeamId == 0 || row.PayerKind == BillingPayerTeam && row.PayerTeamId > 0 || row.PayerKind == BillingPayerOwner && row.SiteId > 0 && row.PayerTeamId == 0 {
			return nil
		}
		return ErrBillingOperationConflict
	}
	if row.EarningRootKey != "" && row.EarningRootKey != row.OperationKey {
		var roots []BillingOperation
		if err := tx.Where("operation_key = ?", row.EarningRootKey).Limit(1).Find(&roots).Error; err != nil {
			return err
		}
		if len(roots) > 0 {
			root := &roots[0]
			if root.UserId != row.UserId || root.TokenId != row.TokenId {
				return ErrBillingOperationConflict
			}
			if root.PayerKind == "" {
				known, err := recoverBillingPayerTx(tx, root)
				if err != nil {
					return err
				}
				if !known {
					return fmt.Errorf("%w: original payer requires review", ErrBillingOperationConflict)
				}
				if err := tx.Model(root).Updates(map[string]any{"payer_kind": root.PayerKind, "payer_team_id": root.PayerTeamId}).Error; err != nil {
					return err
				}
			}
			row.PayerKind, row.PayerTeamId = root.PayerKind, root.PayerTeamId
			if row.PayerKind == BillingPayerOwner {
				row.SiteId = root.SiteId
			}
			return freezeBillingPayerTx(tx, row, false)
		}
		// Legacy direct chunks may have no request row. Reuse the first
		// accepted sibling, rather than selecting a new payer for each chunk.
		if row.Kind == "direct" {
			var siblings []BillingOperation
			if err := tx.Where("earning_root_key = ? AND user_id = ? AND token_id = ? AND operation_key <> ?", row.EarningRootKey, row.UserId, row.TokenId, row.OperationKey).
				Order("created_at, operation_key").Limit(1).Find(&siblings).Error; err != nil {
				return err
			}
			if len(siblings) > 0 {
				if siblings[0].PayerKind == "" {
					known, err := recoverBillingPayerTx(tx, &siblings[0])
					if err != nil {
						return err
					}
					if !known {
						return fmt.Errorf("%w: original sibling payer requires review", ErrBillingOperationConflict)
					}
					if err := tx.Model(&siblings[0]).Updates(map[string]any{
						"payer_kind": siblings[0].PayerKind, "payer_team_id": siblings[0].PayerTeamId,
					}).Error; err != nil {
						return err
					}
				}
				row.PayerKind, row.PayerTeamId = siblings[0].PayerKind, siblings[0].PayerTeamId
				if row.PayerKind == BillingPayerOwner {
					row.SiteId = siblings[0].SiteId
				}
				return freezeBillingPayerTx(tx, row, false)
			}
		}
	}
	if !newAcceptance {
		known, err := recoverBillingPayerTx(tx, row)
		if err != nil {
			return err
		}
		if !known {
			return fmt.Errorf("%w: payer snapshot missing; review original movements", ErrBillingOperationConflict)
		}
		return nil
	}
	return captureBillingPayerTx(tx, row)
}

func captureBillingPayerTx(tx *gorm.DB, row *BillingOperation) error {
	var user User
	if err := lockForUpdate(tx).First(&user, row.UserId).Error; err != nil {
		return err
	}
	row.PayerKind, row.PayerTeamId = BillingPayerPersonal, 0
	if row.TokenId <= 0 {
		return validateUnboundTeamEmployeeTx(tx, row.UserId)
	}
	var before AdtyTeamKey
	hasBindings := tx.Migrator().HasTable(&AdtyTeamKey{})
	if hasBindings {
		if err := tx.Where("token_id = ?", row.TokenId).Limit(1).Find(&before).Error; err != nil {
			return err
		}
		if before.Id != 0 {
			// Team lifecycle mutations also lock team before token. Do not
			// invert that order while freezing an accepted team request.
			var team AdtyTeam
			if err := lockForUpdate(tx).First(&team, before.TeamId).Error; err != nil {
				return err
			}
			if team.Status != AdtyTeamStatusActive || before.Status != AdtyTeamMemberActive || before.UserId != row.UserId {
				return ErrBillingOperationConflict
			}
		}
	}
	var token Token
	if err := lockForUpdate(tx).First(&token, row.TokenId).Error; err != nil {
		return err
	}
	if token.UserId != row.UserId {
		return ErrBillingOperationConflict
	}
	if err := ValidateAdtyTeamTokenTx(tx, &token); err != nil {
		return err
	}
	if token.OwnerSelfUseSiteId > 0 {
		var site AdtySite
		if err := tx.Select("id", "owner_user_id", "status").First(&site, token.OwnerSelfUseSiteId).Error; err != nil {
			return err
		}
		if before.Id != 0 || site.OwnerUserId != row.UserId || site.Status != AdtySiteStatusActive || row.SiteId != 0 && row.SiteId != site.Id {
			return ErrBillingOperationConflict
		}
		row.PayerKind, row.SiteId = BillingPayerOwner, site.Id
	}
	if hasBindings {
		var current AdtyTeamKey
		if err := tx.Where("token_id = ?", row.TokenId).Limit(1).Find(&current).Error; err != nil {
			return err
		}
		// Bind holds the token lock. A changed binding must be retried as a
		// new acceptance, not silently charged against the optimistic read.
		if current.Id != before.Id || current.TeamId != before.TeamId || current.Status != before.Status || current.UserId != before.UserId {
			return ErrBillingOperationConflict
		}
		if current.Id != 0 {
			row.PayerKind, row.PayerTeamId = BillingPayerTeam, current.TeamId
		}
	}
	return nil
}

// Historical movements identify the account that actually paid. Mutable key,
// membership and user/site state cannot establish a legacy financial owner.
func recoverBillingPayerTx(tx *gorm.DB, row *BillingOperation) (bool, error) {
	teamId := int64(0)
	if tx.Migrator().HasTable(&AdtyTeamLedger{}) {
		var entries []AdtyTeamLedger
		if err := tx.Where("ref_id = ? AND type IN ?", row.OperationKey, []string{"reserve", "settle", "refund"}).Find(&entries).Error; err != nil {
			return false, err
		}
		for _, entry := range entries {
			if entry.TeamId <= 0 || entry.ActorUserId != row.UserId || teamId != 0 && teamId != entry.TeamId {
				return false, ErrBillingOperationConflict
			}
			teamId = entry.TeamId
		}
	}
	var personal int64
	if err := tx.Model(&BillingOperationMovement{}).Where("operation_key = ? AND user_id = ? AND wallet_delta <> 0", row.OperationKey, row.UserId).Count(&personal).Error; err != nil {
		return false, err
	}
	if teamId > 0 && personal > 0 {
		return false, ErrBillingOperationConflict
	}
	if teamId > 0 {
		row.PayerKind, row.PayerTeamId = BillingPayerTeam, teamId
		return true, nil
	}
	if personal > 0 || row.TokenId <= 0 {
		row.PayerKind, row.PayerTeamId = BillingPayerPersonal, 0
		return true, nil
	}
	return false, nil
}
