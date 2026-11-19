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
// unique (user, source, kind) key and the order's frozen site identify the lot.
func ownerRefundCapacity(tx *gorm.DB, ext *AdtyTopupOrder, spendable int64) (AdtyFundBatch, int64, error) {
	var lot AdtyFundBatch
	err := tx.Where("user_id = ? AND site_id = ? AND source = ? AND kind = ?", ext.UserId, ext.SiteId, ext.TradeNo, AdtyFundBatchKindPrincipal).Limit(1).Find(&lot).Error
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

// A share is bound to the original row ID as well as its site and source.
// Changing or replacing the batch cannot redirect a reclaim or a release.
func lockOwnerRefundShareTx(tx *gorm.DB, ext *AdtyTopupOrder, row *AdtyManualRefund) (*AdtyFundAllocation, error) {
	var share AdtyFundAllocation
	err := lockForUpdate(tx).Where("operation_id = ? AND user_id = ? AND source = ? AND kind = ? AND seq = 1", ownerRefundOperation(row), ext.UserId, ext.TradeNo, AdtyFundBatchKindPrincipal).First(&share).Error
	if err != nil {
		return nil, err
	}
	var lot AdtyFundBatch
	err = lockForUpdate(tx).Where("id = ? AND user_id = ? AND site_id = ? AND source = ? AND kind = ?", share.BatchId, ext.UserId, ext.SiteId, ext.TradeNo, share.Kind).First(&lot).Error
	if err != nil {
		return nil, err
	}
	if share.Held != row.RequestQuota || share.Held <= 0 {
		return nil, ownerRefundQuotaError()
	}
	if share.Settled == 0 {
		held, err := outstandingAdtyFundAllocationByBatchTx(tx, ext.UserId)
		if err != nil {
			return nil, err
		}
		if lot.Remaining < held[lot.Id] {
			return nil, ownerRefundQuotaError()
		}
	}
	return &share, nil
}

// Reclaim and its failed-payout reversal move the operating principal alongside
// the personal ledger. Both are dated when the money moves and refer to the
// frozen site/order. Completion separately timestamps the offline refund.
func moveOwnerRefundOperatingTx(tx *gorm.DB, ext *AdtyTopupOrder, row *AdtyManualRefund, operatorID int, release bool) error {
	if err := RejectRestrictedAdtyFundWrite(tx, AdtyFundWrite{SiteID: ext.SiteId, AccountID: ext.UserId, OrderRef: ext.TradeNo}); err != nil {
		return err
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
	share, err := lockOwnerRefundShareTx(tx, ext, row)
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
	return moveOwnerRefundOperatingTx(tx, ext, row, operatorID, false)
}

// Payout can be recorded before internal reclaim under the existing state
// machine. In that case the approved share must still cover it in full.
func confirmOwnerRefundBackingTx(tx *gorm.DB, ext *AdtyTopupOrder, row *AdtyManualRefund) error {
	before, checkin, err := lockUserQuotaForRefundTx(tx, ext.UserId)
	if err != nil {
		return err
	}
	share, err := lockOwnerRefundShareTx(tx, ext, row)
	if err != nil {
		return err
	}
	if row.ReclaimedAt == 0 {
		if share.Settled != 0 || adtySpendableQuota(before, checkin) < share.Held {
			return ownerRefundQuotaError()
		}
	} else if share.Settled != share.Held || row.DeductedPrincipalQuota != share.Held || row.ShortfallQuota != 0 {
		return ownerRefundQuotaError()
	}
	return nil
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
	share, err := lockOwnerRefundShareTx(tx, ext, row)
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
		if err := moveOwnerRefundOperatingTx(tx, ext, row, operatorID, true); err != nil {
			return err
		}
	} else if share.Settled != 0 {
		return ownerRefundQuotaError()
	}
	return tx.Model(share).Updates(map[string]any{"held": 0, "settled": 0}).Error
}
