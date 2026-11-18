package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func init() {
	model.AdtyAfterPrincipalSpentTx = adtyDebitOperatingPrincipalTx
	model.AdtyAfterPrincipalRefundedTx = adtyRefundOperatingPrincipalTx
}

func adtyCreditOperatingPrincipalTx(tx *gorm.DB, topUp *model.TopUp, ext *model.AdtyTopupOrder) error {
	if tx == nil || ext == nil || ext.SiteId <= 0 || ext.TeamId > 0 {
		return nil
	}
	return adtyMoveOperatingPrincipalTx(tx, ext.SiteId, ext.FaceQuota(), "principal_credit", "topup", ext.TradeNo, "operating:topup:"+ext.TradeNo, false)
}

// adtyDebitOperatingPrincipalTx 是 `AdtyAfterPrincipalSpentTx` 的实现：把这次
// 消费里**由站点本金垫付**的那部分从站点运营钱包扣掉。
//
// 幂等键必须由 operation key 派生，不能每次现编一个 UUID：结算路径
// (`ApplyBillingSettlement`) 在事务被重试或进程恢复后被重放，现编 UUID 会让
// 同一笔消费把运营钱包扣两遍。没有 operation 身份的直接扣费（每次调用都是
// 一笔真实消费）才退化成随机键。
func adtyResolveOperatingPrincipalSiteIDTx(tx *gorm.DB, userID int, operationKey string) (int, error) {
	var rootKey string
	if operationKey != "" {
		var ops []model.BillingOperation
		if err := tx.Select("operation_key", "site_id", "earning_root_key").Where("operation_key = ?", operationKey).Limit(1).Find(&ops).Error; err != nil {
			return 0, err
		}
		if len(ops) > 0 {
			if ops[0].SiteId > 0 {
				return ops[0].SiteId, nil
			}
			rootKey = ops[0].EarningRootKey
		}
		if rootKey != "" && rootKey != operationKey {
			var rootOps []model.BillingOperation
			if err := tx.Select("operation_key", "site_id").Where("operation_key = ?", rootKey).Limit(1).Find(&rootOps).Error; err != nil {
				return 0, err
			}
			if len(rootOps) > 0 && rootOps[0].SiteId > 0 {
				return rootOps[0].SiteId, nil
			}
		}
		lookupKeys := []string{"operating:consume:" + operationKey}
		if rootKey != "" && rootKey != operationKey {
			lookupKeys = append(lookupKeys, "operating:consume:"+rootKey)
		}
		var ledgers []model.AdtySiteWalletLedger
		if err := tx.Select("site_id").Where("idempotency_key IN ? AND site_id > 0", lookupKeys).Order("id DESC").Limit(1).Find(&ledgers).Error; err != nil {
			return 0, err
		}
		if len(ledgers) > 0 && ledgers[0].SiteId > 0 {
			return ledgers[0].SiteId, nil
		}
		allocKeys := []string{operationKey}
		if rootKey != "" && rootKey != operationKey {
			allocKeys = append(allocKeys, rootKey)
		}
		var allocs []model.AdtyFundAllocation
		if err := tx.Select("batch_id").Where("user_id = ? AND source IN ?", userID, allocKeys).Order("id ASC").Find(&allocs).Error; err != nil {
			return 0, err
		}
		for _, alloc := range allocs {
			if alloc.BatchId <= 0 {
				continue
			}
			var batches []model.AdtyFundBatch
			if err := tx.Select("id", "site_id", "source").Where("id = ?", alloc.BatchId).Limit(1).Find(&batches).Error; err != nil {
				return 0, err
			}
			if len(batches) == 0 {
				continue
			}
			if batches[0].SiteId != nil && *batches[0].SiteId > 0 {
				return *batches[0].SiteId, nil
			}
			if batches[0].Source == "" {
				continue
			}
			var orders []model.AdtyTopupOrder
			if err := tx.Select("site_id").Where("trade_no = ? AND site_id > 0", batches[0].Source).Limit(1).Find(&orders).Error; err != nil {
				return 0, err
			}
			if len(orders) > 0 && orders[0].SiteId > 0 {
				return orders[0].SiteId, nil
			}
		}
	}
	var user model.User
	if err := tx.Select("id", "site_id", "inherited_from_site_id").First(&user, userID).Error; err != nil {
		return 0, err
	}
	if user.SiteId > 0 {
		return user.SiteId, nil
	}
	if user.InheritedFromSiteId > 0 {
		return user.InheritedFromSiteId, nil
	}
	return 0, nil
}

func adtyDebitOperatingPrincipalTx(tx *gorm.DB, userID int, operationKey string, principalQuota int64) error {
	if tx == nil || userID <= 0 || principalQuota <= 0 {
		return nil
	}
	siteID, err := adtyResolveOperatingPrincipalSiteIDTx(tx, userID, operationKey)
	if err != nil {
		return err
	}
	if siteID <= 0 {
		return nil
	}
	key := "operating:consume:" + common.GetUUID()
	refID := key
	if operationKey != "" {
		// 一条 operation 只结算一次，所以「一条消费流水」正好对上一个 operation key。
		key = "operating:consume:" + operationKey
		refID = operationKey
	}
	// Insufficient cover leaves settlement pending for an authorized adjustment.
	return adtyMoveOperatingPrincipalTx(tx, siteID, -principalQuota, "principal_consume", "consume", refID, key, false)
}

func adtyRefundOperatingPrincipalTx(tx *gorm.DB, userID int, operationKey string, principalQuota int64) error {
	if tx == nil || userID <= 0 || principalQuota <= 0 {
		return nil
	}
	siteID, err := adtyResolveOperatingPrincipalSiteIDTx(tx, userID, operationKey)
	if err != nil {
		return err
	}
	if siteID <= 0 {
		return nil
	}
	key := "operating:refund:" + common.GetUUID()
	refID := key
	if operationKey != "" {
		key = "operating:refund:" + operationKey
		refID = operationKey
	}
	return adtyMoveOperatingPrincipalTx(tx, siteID, principalQuota, "principal_refund", "refund", refID, key, false)
}

func adtyMoveOperatingPrincipalTx(tx *gorm.DB, siteID int, quota int64, changeType, refType, refID, key string, allowOverdraft bool) error {
	if siteID <= 0 || quota == 0 {
		return nil
	}
	usd := decimal.NewFromInt(quota).Div(common.QuotaPerUnitDecimal())
	if usd.IsZero() {
		return nil
	}
	_, err := applyAdtySiteWalletChangeTx(tx, siteID, model.AdtySiteWalletOperating, usd, AdtySiteWalletChange{
		Type: changeType, RefType: refType, RefID: refID, IdempotencyKey: key, AllowOverdraft: allowOverdraft,
	})
	return err
}
