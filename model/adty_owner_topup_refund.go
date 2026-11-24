package model

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Refund shares use a separate operation namespace. Unlike API precharges,
// they leave users.quota untouched until the internal reclaim step.
const ownerRefundOperationPrefix = "owner-refund:"

func lockAdtyRefundOrderForOperatorTx(tx *gorm.DB, tradeNo string, operatorID int) (*TopUp, *AdtyTopupOrder, error) {
	top, ext, err := lockAdtyTopUpTx(tx, tradeNo)
	if err != nil || !ext.IsOwnerSelfUse() {
		return top, ext, err
	}
	if operatorID <= 0 || operatorID == ext.UserId || operatorID == ext.SiteOwnerUserId {
		return nil, nil, common.NewMessage("No permission to perform this action")
	}
	if ext.SiteId <= 0 || ext.UserId != top.UserId || ext.SiteOwnerUserId != ext.UserId || ext.TeamId != 0 {
		return nil, nil, ErrAdtyRefundNotCashBacked
	}
	return top, ext, nil
}

// Call inside the user's wallet lock for a binding decision. The batch's
// unique (user, source, kind) key and the order's frozen site (or site_id = 0
// once that subsite has closed and migrated the lot) identify the lot (R10-019).
func ownerRefundCapacity(tx *gorm.DB, ext *AdtyTopupOrder, spendable int64) (AdtyFundBatch, int64, error) {
	var lot AdtyFundBatch
	err := tx.Where("user_id = ? AND source = ? AND kind = ? AND (site_id = ? OR (site_id = 0 AND EXISTS (SELECT 1 FROM adty_sites WHERE id = ? AND status = ?)))",
		ext.UserId, ext.TradeNo, AdtyFundBatchKindPrincipal, ext.SiteId, ext.SiteId, AdtySiteStatusClosed).
		Limit(1).Find(&lot).Error
	if err != nil || lot.Id == 0 {
		return lot, 0, err
	}
	var claims struct{ OnLot, Refunds int64 }
	err = tx.Model(&AdtyFundAllocation{}).Where("user_id = ? AND held > settled", ext.UserId).
		Select("COALESCE(SUM(CASE WHEN batch_id = ? THEN held-settled ELSE 0 END),0) AS on_lot, COALESCE(SUM(CASE WHEN operation_id LIKE ? THEN held-settled ELSE 0 END),0) AS refunds", lot.Id, ownerRefundOperationPrefix+"%").Scan(&claims).Error
	return lot, max(0, min(lot.Remaining-claims.OnLot, spendable-claims.Refunds)), err
}

func ownerRefundApplicationLimit(available int64, occupancy *AdtyManualRefundOccupancy, rows []AdtyManualRefund) int64 {
	// Submitted applications occupy order capacity before approval takes a lot
	// hold. Approved and reclaimed rows are already reflected in available.
	for _, row := range rows {
		if row.Status == AdtyManualRefundStatusSubmitted {
			available -= row.RequestQuota
		}
	}
	return max(0, min(available, occupancy.RemainingQuota))
}

func ownerRefundQuotaError() error {
	return common.NewMessage("Refund amount exceeds the remaining principal")
}

func ownerRefundOperation(row *AdtyManualRefund) string {
	return ownerRefundOperationPrefix + strconv.FormatInt(row.Id, 10)
}

func reserveOwnerRefundTx(tx *gorm.DB, ext *AdtyTopupOrder, row *AdtyManualRefund) error {
	before, checkin, err := lockUserQuotaForRefundTx(tx, ext.UserId)
	if err != nil {
		return err
	}
	lot, available, err := ownerRefundCapacity(tx, ext, adtySpendableQuota(before, checkin))
	if err != nil {
		return err
	}
	if row.RequestQuota <= 0 || row.RequestQuota > available || !row.RequestUSD.Mul(common.QuotaPerUnitDecimal()).Equal(decimal.NewFromInt(row.RequestQuota)) {
		return ownerRefundQuotaError()
	}
	return tx.Create(&AdtyFundAllocation{UserId: ext.UserId, BatchId: lot.Id, Source: lot.Source, Kind: lot.Kind, OperationId: ownerRefundOperation(row), Seq: 1, Held: row.RequestQuota}).Error
}

// A share is bound to the original row ID as well as its site (or site_id = 0
// after closed-site migration) and source. Changing or replacing the batch
// cannot redirect a reclaim or a release (R10-019).
func lockOwnerRefundShareTx(tx *gorm.DB, ext *AdtyTopupOrder, row *AdtyManualRefund) (*AdtyFundAllocation, *AdtyFundBatch, error) {
	var share AdtyFundAllocation
	err := lockForUpdate(tx).Where("operation_id = ? AND user_id = ? AND source = ? AND kind = ? AND seq = 1", ownerRefundOperation(row), ext.UserId, ext.TradeNo, AdtyFundBatchKindPrincipal).First(&share).Error
	if err != nil {
		return nil, nil, err
	}
	var lot AdtyFundBatch
	err = lockForUpdate(tx).Where("id = ? AND user_id = ? AND source = ? AND kind = ? AND (site_id = ? OR (site_id = 0 AND EXISTS (SELECT 1 FROM adty_sites WHERE id = ? AND status = ?)))",
		share.BatchId, ext.UserId, ext.TradeNo, share.Kind, ext.SiteId, ext.SiteId, AdtySiteStatusClosed).First(&lot).Error
	if err != nil {
		return nil, nil, err
	}
	if share.Held != row.RequestQuota || share.Held <= 0 {
		return nil, nil, ownerRefundQuotaError()
	}
	if share.Settled == 0 {
		held, err := outstandingAdtyFundAllocationByBatchTx(tx, ext.UserId)
		if err != nil {
			return nil, nil, err
		}
		if lot.Remaining < held[lot.Id] {
			return nil, nil, ownerRefundQuotaError()
		}
	}
	return &share, &lot, nil
}

func isOwnerRefundSiteClosedTx(tx *gorm.DB, siteID int) (bool, error) {
	if tx == nil || siteID <= 0 || !tx.Migrator().HasTable(&AdtySite{}) {
		return false, nil
	}
	var status string
	if err := tx.Model(&AdtySite{}).Select("status").Where("id = ?", siteID).Limit(1).Scan(&status).Error; err != nil {
		return false, err
	}
	return status == AdtySiteStatusClosed, nil
}

// Reclaim and its failed-payout reversal move the operating principal alongside
// the personal ledger. Both are dated when the money moves and refer to the
// frozen site/order. Completion separately timestamps the offline refund.
// Once the lot has already migrated to site_id = 0 (its balance was debited
// from the closing site's operating wallet via movedPrincipal), skip touching
// the closed site's operating wallet. While lot.SiteId still points to
// ext.SiteId, its remaining balance is still backed by operating(ext.SiteId)
// and must stay synchronized with lot.Remaining (R10-019).
func moveOwnerRefundOperatingTx(tx *gorm.DB, ext *AdtyTopupOrder, lot *AdtyFundBatch, row *AdtyManualRefund, operatorID int, release bool) error {
	if err := RejectRestrictedAdtyFundWrite(tx, AdtyFundWrite{SiteID: ext.SiteId, AccountID: ext.UserId, OrderRef: ext.TradeNo}); err != nil {
		return err
	}
	if lot != nil && (lot.SiteId == nil || *lot.SiteId == 0) {
		return nil
	}
	var wallet AdtySiteWallet
	if err := lockForUpdate(tx).Where("site_id = ? AND kind = ?", ext.SiteId, AdtySiteWalletOperating).First(&wallet).Error; err != nil {
		return err
	}
	delta, kind := row.RequestUSD.Neg(), "owner_topup_refund"
	if release {
		delta, kind = row.RequestUSD, "owner_topup_refund_release"
	}
	before := wallet.Balance
	after := before.Add(delta)
	if !release && after.LessThan(wallet.FrozenUSD) {
		return ownerRefundQuotaError()
	}
	if err := tx.Model(&wallet).Update("balance", after).Error; err != nil {
		return err
	}
	return tx.Create(&AdtySiteWalletLedger{SiteId: ext.SiteId, WalletId: wallet.Id, WalletKind: wallet.Kind,
		Delta: delta, BalanceBefore: before, BalanceAfter: after, Type: kind, RefType: "topup", RefId: ext.TradeNo,
		OperatorId: operatorID, Reason: row.Reason, IdempotencyKey: kind + ":" + row.RefundId}).Error
}

func reclaimOwnerRefundTx(tx *gorm.DB, ext *AdtyTopupOrder, row *AdtyManualRefund, spendable int64, operatorID int) error {
	share, lot, err := lockOwnerRefundShareTx(tx, ext, row)
	if err != nil {
		return err
	}
	if share.Settled != 0 || spendable < share.Held {
		return ownerRefundQuotaError()
	}
	if err := deductAdtyFundBatchTx(tx, share.BatchId, share.Held); err != nil {
		return err
	}
	if err := tx.Model(share).Update("settled", share.Held).Error; err != nil {
		return err
	}
	return moveOwnerRefundOperatingTx(tx, ext, lot, row, operatorID, false)
}

// migrateClosedOwnerLotIfUnencumberedTx moves an owner self-use principal lot
// that remained pinned to a closed subsite (due to an active refund/hold at
// close time) onto main site (site_id = 0) and debits its remaining balance
// from the closed site's operating wallet so operating(S) == ownerPrincipal(S)
// remains exact (R10-019).
func migrateClosedOwnerLotIfUnencumberedTx(tx *gorm.DB, ext *AdtyTopupOrder, lot *AdtyFundBatch, remainingQuota int64, row *AdtyManualRefund, operatorID int, keySuffix string) error {
	if tx == nil || ext == nil || lot == nil || lot.SiteId == nil || *lot.SiteId != ext.SiteId || ext.SiteId <= 0 {
		return nil
	}
	siteClosed, err := isOwnerRefundSiteClosedTx(tx, ext.SiteId)
	if err != nil || !siteClosed {
		return err
	}
	var activeAllocs int64
	if err := tx.Model(&AdtyFundAllocation{}).Where("batch_id = ? AND held > settled", lot.Id).Count(&activeAllocs).Error; err != nil {
		return err
	}
	var otherActiveRefunds int64
	if err := tx.Model(&AdtyManualRefund{}).Where("trade_no = ? AND refund_id <> ? AND status NOT IN ?", ext.TradeNo, row.RefundId, []string{AdtyManualRefundStatusCompleted, AdtyManualRefundStatusTerminated}).Count(&otherActiveRefunds).Error; err != nil {
		return err
	}
	if activeAllocs != 0 || otherActiveRefunds != 0 {
		return nil
	}
	if err := tx.Model(&AdtyFundBatch{}).Where("id = ?", lot.Id).Update("site_id", 0).Error; err != nil {
		return err
	}
	if remainingQuota <= 0 || lot.Source == adtyLegacyBatchSource || !tx.Migrator().HasTable(&AdtySiteWallet{}) {
		return nil
	}
	var wallet AdtySiteWallet
	res := lockForUpdate(tx).Where("site_id = ? AND kind = ?", ext.SiteId, AdtySiteWalletOperating).Limit(1).Find(&wallet)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 || !wallet.Balance.IsPositive() {
		return nil
	}
	debitUSD := decimal.NewFromInt(remainingQuota).Div(common.QuotaPerUnitDecimal())
	if debitUSD.GreaterThan(wallet.Balance) {
		debitUSD = wallet.Balance
	}
	if !debitUSD.IsPositive() {
		return nil
	}
	before := wallet.Balance
	after := before.Sub(debitUSD)
	if err := tx.Model(&wallet).Update("balance", after).Error; err != nil {
		return err
	}
	return tx.Create(&AdtySiteWalletLedger{
		SiteId: ext.SiteId, WalletId: wallet.Id, WalletKind: wallet.Kind,
		Delta: debitUSD.Neg(), BalanceBefore: before, BalanceAfter: after,
		Type: "principal_site_close_migrate", RefType: "topup", RefId: ext.TradeNo,
		OperatorId: operatorID, Reason: row.Reason,
		IdempotencyKey: "owner_topup_close_migrate:" + keySuffix + ":" + row.RefundId,
	}).Error
}

// Payout can be recorded before internal reclaim under the existing state
// machine. In that case the approved share must still cover it in full.
func confirmOwnerRefundBackingTx(tx *gorm.DB, ext *AdtyTopupOrder, row *AdtyManualRefund) (*AdtyFundBatch, error) {
	before, checkin, err := lockUserQuotaForRefundTx(tx, ext.UserId)
	if err != nil {
		return nil, err
	}
	share, lot, err := lockOwnerRefundShareTx(tx, ext, row)
	if err != nil {
		return nil, err
	}
	if row.ReclaimedAt == 0 {
		if share.Settled != 0 || adtySpendableQuota(before, checkin) < share.Held {
			return nil, ownerRefundQuotaError()
		}
	} else if share.Settled != share.Held || row.DeductedPrincipalQuota != share.Held || row.ShortfallQuota != 0 {
		return nil, ownerRefundQuotaError()
	}
	return lot, nil
}

func releaseOwnerRefundTx(tx *gorm.DB, ext *AdtyTopupOrder, row *AdtyManualRefund, operatorID int, key string) error {
	var user User
	if err := lockForUpdate(tx).First(&user, ext.UserId).Error; err != nil {
		return err
	}
	before, err := userWalletBalance(user.Quota, user.QuotaPending)
	if err != nil {
		return err
	}
	share, lot, err := lockOwnerRefundShareTx(tx, ext, row)
	if err != nil {
		return err
	}
	if row.ReclaimedAt > 0 {
		if share.Settled != share.Held || row.DeductedPrincipalQuota != share.Settled {
			return ownerRefundQuotaError()
		}
		// Use the shared wallet mutation so both the logical wallet ceiling and
		// raw/pending integer bounds are checked before any restoration commits.
		if err := applyUserQuotaDeltaTx(tx, &user, int(share.Settled), false); err != nil {
			return err
		}
		r := tx.Model(&AdtyFundBatch{}).Where("id = ? AND remaining <= original - ?", share.BatchId, share.Settled).Update("remaining", gorm.Expr("remaining + ?", share.Settled))
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return ownerRefundQuotaError()
		}
		ledger := adtyManualRefundLedger(ext.UserId, -share.Settled, int64(before), ext.TradeNo, row.Reason+" [refund release: "+row.RefundId+"]", operatorID, key)
		if err := InsertAdtyLedgerTx(tx, ledger); err != nil {
			return err
		}
		if err := moveOwnerRefundOperatingTx(tx, ext, lot, row, operatorID, true); err != nil {
			return err
		}
	} else if share.Settled != 0 {
		return ownerRefundQuotaError()
	}
	if err := tx.Model(share).Updates(map[string]any{"held": 0, "settled": 0}).Error; err != nil {
		return err
	}
	restoredRemaining := lot.Remaining
	if row.ReclaimedAt > 0 {
		restoredRemaining += share.Settled
	}
	return migrateClosedOwnerLotIfUnencumberedTx(tx, ext, lot, restoredRemaining, row, operatorID, "release")
}
