package model

import (
	"errors"
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

// migrateClosedOwnerBatchIfUnencumberedTx moves an owner self-use principal lot
// pinned to a closed subsite onto main site (site_id = 0) and debits its
// authoritative DB remaining balance from the closed site's operating wallet so
// operating(S) == ownerPrincipal(S) remains exact (R10-019/R11-001/R11-002/R11-032).
func migrateClosedOwnerBatchIfUnencumberedTx(tx *gorm.DB, siteID, ownerUserID int, batchID int64, excludeRefundID string, operatorID int, reason, idemKey string, ignoreRestricted bool) error {
	if tx == nil || siteID <= 0 || ownerUserID <= 0 || batchID <= 0 {
		return nil
	}
	siteClosed, err := isOwnerRefundSiteClosedTx(tx, siteID)
	if err != nil || !siteClosed {
		return err
	}
	var fresh AdtyFundBatch
	res := lockForUpdate(tx).
		Where("id = ? AND user_id = ? AND kind = ?", batchID, ownerUserID, AdtyFundBatchKindPrincipal).
		Limit(1).Find(&fresh)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 || fresh.SiteId == nil || *fresh.SiteId != siteID {
		return nil
	}
	if tx.Migrator().HasTable(&AdtyFundAllocation{}) {
		var activeAllocs int64
		if err := tx.Model(&AdtyFundAllocation{}).Where("batch_id = ? AND held > settled", fresh.Id).Count(&activeAllocs).Error; err != nil {
			return err
		}
		if activeAllocs != 0 {
			return nil
		}
	}
	if fresh.Source != "" && tx.Migrator().HasTable(&AdtyManualRefund{}) {
		q := tx.Model(&AdtyManualRefund{}).
			Where("trade_no = ? AND status NOT IN ?", fresh.Source, []string{AdtyManualRefundStatusCompleted, AdtyManualRefundStatusTerminated})
		if excludeRefundID != "" {
			q = q.Where("refund_id <> ?", excludeRefundID)
		}
		var otherActiveRefunds int64
		if err := q.Count(&otherActiveRefunds).Error; err != nil {
			return err
		}
		if otherActiveRefunds != 0 {
			return nil
		}
	}
	if err := RejectRestrictedAdtyFundWrite(tx, AdtyFundWrite{SiteID: siteID, AccountID: ownerUserID, OrderRef: fresh.Source}); err != nil {
		// Honor reviewing anomaly restrictions without mutating restricted funds
		// AND without wedging un-reclaimed Terminate or paid-out Complete;
		// both batch.SiteId and operating(S) stay unchanged and conserved until
		// the review closes and MigrateUnencumberedOwnerLotsOnClosedSites sweeps it (R12-029).
		if errors.Is(err, ErrAdtyFundWriteRestricted) {
			return nil
		}
		return err
	}
	if err := tx.Model(&AdtyFundBatch{}).Where("id = ?", fresh.Id).Update("site_id", 0).Error; err != nil {
		return err
	}
	if fresh.Remaining <= 0 || fresh.Source == adtyLegacyBatchSource || !tx.Migrator().HasTable(&AdtySiteWallet{}) {
		return nil
	}
	var wallet AdtySiteWallet
	wres := lockForUpdate(tx).Where("site_id = ? AND kind = ?", siteID, AdtySiteWalletOperating).Limit(1).Find(&wallet)
	if wres.Error != nil {
		return wres.Error
	}
	if wres.RowsAffected == 0 || !wallet.Balance.IsPositive() {
		return nil
	}
	debitUSD := decimal.NewFromInt(fresh.Remaining).Div(common.QuotaPerUnitDecimal())
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
		SiteId: siteID, WalletId: wallet.Id, WalletKind: wallet.Kind,
		Delta: debitUSD.Neg(), BalanceBefore: before, BalanceAfter: after,
		Type: "principal_site_close_migrate", RefType: "topup", RefId: fresh.Source,
		OperatorId: operatorID, Reason: reason,
		IdempotencyKey: idemKey,
	}).Error
}

func migrateClosedOwnerLotIfUnencumberedTx(tx *gorm.DB, ext *AdtyTopupOrder, lot *AdtyFundBatch, row *AdtyManualRefund, operatorID int, keySuffix string) error {
	if ext == nil || lot == nil || row == nil {
		return nil
	}
	return migrateClosedOwnerBatchIfUnencumberedTx(
		tx,
		ext.SiteId,
		ext.UserId,
		lot.Id,
		row.RefundId,
		operatorID,
		row.Reason,
		"owner_topup_close_migrate:"+keySuffix+":"+row.RefundId,
		false,
	)
}

// migrateTouchedClosedOwnerBatchesTx runs inside a billing settlement/refund tx
// (after User lock and operating principal callbacks) to migrate any touched
// owner principal batches on already-closed subsites once unencumbered (R11-002).
func migrateTouchedClosedOwnerBatchesTx(tx *gorm.DB, userID int, batchIDs []int64, keySuffix string) error {
	if tx == nil || userID <= 0 || len(batchIDs) == 0 || !tx.Migrator().HasTable(&AdtySite{}) || !tx.Migrator().HasTable(&AdtyFundBatch{}) {
		return nil
	}
	seen := make(map[int64]bool, len(batchIDs))
	uniq := make([]int64, 0, len(batchIDs))
	for _, id := range batchIDs {
		if id > 0 && !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	if len(uniq) == 0 {
		return nil
	}
	var candidates []struct {
		BatchId int64
		SiteId  int
	}
	if err := tx.Table("adty_fund_batches b").
		Joins("JOIN adty_sites s ON s.id = b.site_id AND s.owner_user_id = b.user_id").
		Where("b.id IN ? AND b.user_id = ? AND b.kind = ? AND b.site_id > 0 AND s.status = ?",
			uniq, userID, AdtyFundBatchKindPrincipal, AdtySiteStatusClosed).
		Order("b.id ASC").
		Select("b.id AS batch_id, b.site_id AS site_id").
		Scan(&candidates).Error; err != nil {
		return err
	}
	for _, c := range candidates {
		idem := "owner_topup_close_migrate:batch:" + strconv.FormatInt(c.BatchId, 10)
		if err := migrateClosedOwnerBatchIfUnencumberedTx(tx, c.SiteId, userID, c.BatchId, "", 0, "closed site owner lot cleanup", idem, true); err != nil {
			return err
		}
	}
	return nil
}

// MigrateUnencumberedOwnerLotsOnClosedSites periodically sweeps closed subsites
// for owner principal lots that stayed pinned due to in-flight holds/refunds
// during site close, migrating them in canonical AdtySite -> User -> AdtyFundBatch -> AdtySiteWallet order (R11-002).
func MigrateUnencumberedOwnerLotsOnClosedSites() error {
	if DB == nil || !DB.Migrator().HasTable(&AdtySite{}) || !DB.Migrator().HasTable(&AdtyFundBatch{}) {
		return nil
	}
	var targets []struct {
		SiteId      int
		OwnerUserId int
	}
	if err := DB.Table("adty_fund_batches b").
		Joins("JOIN adty_sites s ON s.id = b.site_id AND s.owner_user_id = b.user_id").
		Where("b.kind = ? AND b.site_id > 0 AND s.status = ?", AdtyFundBatchKindPrincipal, AdtySiteStatusClosed).
		Distinct("s.id AS site_id, s.owner_user_id AS owner_user_id").
		Order("s.id ASC").
		Scan(&targets).Error; err != nil {
		return err
	}
	var firstErr error
	for _, target := range targets {
		err := DB.Transaction(func(tx *gorm.DB) error {
			var site AdtySite
			if err := lockForUpdate(tx).First(&site, target.SiteId).Error; err != nil {
				return err
			}
			if site.Status != AdtySiteStatusClosed || site.OwnerUserId <= 0 {
				return nil
			}
			var owner User
			if err := lockForUpdate(tx).Select("id").Where("id = ?", site.OwnerUserId).First(&owner).Error; err != nil {
				return err
			}
			var batches []AdtyFundBatch
			if err := lockForUpdate(tx).
				Where("user_id = ? AND site_id = ? AND kind = ?", site.OwnerUserId, site.Id, AdtyFundBatchKindPrincipal).
				Order("id ASC").
				Find(&batches).Error; err != nil {
				return err
			}
			for i := range batches {
				idem := "owner_topup_close_migrate:sweep:" + strconv.Itoa(site.Id) + ":" + strconv.FormatInt(batches[i].Id, 10)
				if err := migrateClosedOwnerBatchIfUnencumberedTx(tx, site.Id, site.OwnerUserId, batches[i].Id, "", 0, "closed site owner lot sweep", idem, true); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
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
	return migrateClosedOwnerLotIfUnencumberedTx(tx, ext, lot, row, operatorID, "release")
}
