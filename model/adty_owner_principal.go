package model

import (
	"context"

	"gorm.io/gorm"
)

type ownerLotScopeKey struct{}

// Attach the immutable payer scope to all batch queries in this transaction.
func withOwnerFundScope(tx *gorm.DB, op *BillingOperation) *gorm.DB {
	if op.PayerKind != BillingPayerOwner && !tx.Migrator().HasTable(&AdtySite{}) {
		return tx
	}
	id := 0
	if op.PayerKind == BillingPayerOwner {
		id = op.SiteId
	}
	return tx.WithContext(context.WithValue(tx.Statement.Context, ownerLotScopeKey{}, id))
}

// Reverse only settled shares belonging to this original self-use request.
func reverseOwnerPrincipalTx(tx *gorm.DB, op *BillingOperation, amount int64) error {
	root := op.EarningRootKey
	if root == "" {
		root = op.OperationKey
	}
	var sources []BillingOperation
	if err := tx.Where("user_id = ? AND site_id = ? AND payer_kind = ? AND (operation_key = ? OR earning_root_key = ?) AND operation_key <> ?", op.UserId, op.SiteId, BillingPayerOwner, root, root, op.OperationKey).Order("created_at DESC, operation_key DESC").Find(&sources).Error; err != nil {
		return err
	}
	keys := make([]string, 0, len(sources))
	for _, source := range sources {
		keys = append(keys, source.OperationKey)
	}
	var settled int64
	if err := tx.Model(&AdtyFundAllocation{}).Where("user_id = ? AND operation_id IN ?", op.UserId, keys).Select("COALESCE(SUM(settled),0)").Scan(&settled).Error; err != nil {
		return err
	}
	if settled < amount {
		return ErrBillingOperationConflict
	}
	return reverseSettledAdtyFundSourcesTx(tx, op.UserId, keys, op.OperationKey, amount)
}

// reversePersonalStreamAllocationsTx reverses already-settled shares from a personal
// request's Realtime WSS child stream operations (direct:*) and root operation when
// final settlement returns quota below StreamWalletQuota (R2-002).
func reversePersonalStreamAllocationsTx(tx *gorm.DB, op *BillingOperation, amount int64) error {
	if tx == nil || op == nil || amount <= 0 {
		return nil
	}
	root := op.EarningRootKey
	if root == "" {
		root = op.OperationKey
	}
	var sources []BillingOperation
	if err := tx.Select("operation_key").
		Where("user_id = ? AND (operation_key = ? OR earning_root_key = ?) AND operation_key <> ?", op.UserId, root, root, op.OperationKey).
		Order("created_at DESC, operation_key DESC").
		Find(&sources).Error; err != nil {
		return err
	}
	keys := make([]string, 0, len(sources)+1)
	seen := map[string]struct{}{}
	for _, s := range sources {
		if s.OperationKey != "" {
			if _, ok := seen[s.OperationKey]; !ok {
				seen[s.OperationKey] = struct{}{}
				keys = append(keys, s.OperationKey)
			}
		}
	}
	if _, ok := seen[root]; !ok && root != "" {
		keys = append(keys, root)
	}
	if len(keys) == 0 {
		return nil
	}
	return reverseSettledAdtyFundSourcesTx(tx, op.UserId, keys, op.OperationKey, amount)
}

func eligibleFundLots(tx *gorm.DB, userID int) *gorm.DB {
	q := tx.Where("user_id = ?", userID)
	id, ok := tx.Statement.Context.Value(ownerLotScopeKey{}).(int)
	if !ok {
		return q
	}
	if id > 0 {
		return q.Where("site_id = ? AND kind = ?", id, "principal")
	}
	return q.Where("(kind <> 'principal' OR site_id IS NULL OR site_id NOT IN (?))", tx.Model(&AdtySite{}).Select("id").Where("owner_user_id = ?", userID))
}

// Available site principal excludes shares still reserved by another request.
func AdtyOwnerPrincipalAvailable(tx *gorm.DB, userID, siteID int) (int64, error) {
	q := tx.Model(&AdtyFundBatch{}).Where("user_id = ? AND kind = ? AND remaining > 0", userID, "principal")
	if siteID > 0 {
		q = q.Where("site_id = ?", siteID)
	} else {
		q = q.Where("site_id IN (?)", tx.Model(&AdtySite{}).Select("id").Where("owner_user_id = ?", userID))
	}
	const sum = "COALESCE(SUM(remaining - COALESCE((SELECT SUM(a.held-a.settled) FROM adty_fund_allocations a WHERE a.batch_id=adty_fund_batches.id AND a.held>a.settled),0)),0)"
	var available int64
	err := q.Select(sum).Scan(&available).Error
	return available, err
}

// AdtyOwnerScopedWallet exposes the owner self-use vs ordinary wallet isolation
// calculation for non-transactional pre-checks such as Realtime WSS streaming.
func AdtyOwnerScopedWallet(tx *gorm.DB, op *BillingOperation, balance int) (int, error) {
	if tx == nil {
		tx = DB
	}
	if op == nil {
		return balance, nil
	}
	return ownerScopedWallet(tx, op, balance)
}

func ownerScopedWallet(tx *gorm.DB, op *BillingOperation, balance int) (int, error) {
	if op.PayerKind != BillingPayerOwner && !tx.Migrator().HasTable(&AdtySite{}) {
		return balance, nil
	}
	siteID := 0
	if op.PayerKind == BillingPayerOwner {
		siteID = op.SiteId
	}
	available, err := AdtyOwnerPrincipalAvailable(tx, op.UserId, siteID)
	if err != nil {
		return 0, err
	}
	if siteID > 0 {
		return int(min(int64(balance), available)), nil
	}
	// API reservations already debit the aggregate wallet. Refund reservations
	// only hold a batch until reclaim, so that amount must remain excluded from
	// the ordinary wallet as well as from the owner's available principal.
	var refundHeld int64
	if err := tx.Model(&AdtyFundAllocation{}).
		Where("user_id = ? AND operation_id LIKE ? AND held > settled", op.UserId, ownerRefundOperationPrefix+"%").
		Select("COALESCE(SUM(held-settled),0)").Scan(&refundHeld).Error; err != nil {
		return 0, err
	}
	return int(max(0, int64(balance)-available-refundHeld)), nil
}
