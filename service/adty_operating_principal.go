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
func adtyDebitOperatingPrincipalTx(tx *gorm.DB, userID int, operationKey string, principalQuota int64) error {
	if tx == nil || userID <= 0 || principalQuota <= 0 {
		return nil
	}
	var user model.User
	if err := tx.Select("id", "site_id").First(&user, userID).Error; err != nil {
		return err
	}
	if user.SiteId <= 0 {
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
	return adtyMoveOperatingPrincipalTx(tx, user.SiteId, -principalQuota, "principal_consume", "consume", refID, key, false)
}

func adtyRefundOperatingPrincipalTx(tx *gorm.DB, userID int, operationKey string, principalQuota int64) error {
	if tx == nil || userID <= 0 || principalQuota <= 0 {
		return nil
	}
	var user model.User
	if err := tx.Select("id", "site_id").First(&user, userID).Error; err != nil {
		return err
	}
	if user.SiteId <= 0 {
		return nil
	}
	key := "operating:refund:" + common.GetUUID()
	refID := key
	if operationKey != "" {
		key = "operating:refund:" + operationKey
		refID = operationKey
	}
	return adtyMoveOperatingPrincipalTx(tx, user.SiteId, principalQuota, "principal_refund", "refund", refID, key, false)
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
