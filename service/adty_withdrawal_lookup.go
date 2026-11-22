package service

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

func withdrawalReceiptID(q *gorm.DB, key string) (int64, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 128 {
		return 0, common.NewMessage("Invalid idempotency key")
	}
	var id int64
	err := q.Where("idempotency_key = ?", key).Limit(1).Pluck("id", &id).Error
	return id, err
}

func LookupSiteInviteWithdrawal(userID, siteID int, key string) (int64, error) {
	if userID <= 0 || siteID <= 0 {
		return 0, common.NewMessage("Invalid parameters")
	}
	return withdrawalReceiptID(model.DB.Model(&model.AdtySiteInviteWithdrawal{}).Where("user_id = ? AND site_id = ?", userID, siteID), key)
}

func LookupSiteOwnerWithdrawal(userID, siteID int, key string) (int64, error) {
	return withdrawalReceiptID(model.DB.Model(&model.AdtySiteWithdrawal{}).Scopes(model.AdtyWithdrawalOwnerScope(siteID, userID)), key)
}

func siteCommissionConversionKey(commissionID int64, key string) string {
	return "site-comm-cvt:" + strconv.FormatInt(commissionID, 10) + ":" + key
}

func LookupSiteCommissionConversion(userID, siteID int, commissionID int64, key string) (int64, error) {
	key = strings.TrimSpace(key)
	if userID <= 0 || siteID <= 0 || commissionID <= 0 || key == "" || len(key) > 40 {
		return 0, common.NewMessage("Invalid parameters")
	}
	commission := model.DB.Model(&model.AdtySiteInviteCommission{}).Select("1").Where("id = ? AND site_id = ? AND inviter_id = ?", commissionID, siteID, userID)
	return withdrawalReceiptID(model.DB.Model(&model.AdtyBalanceLedger{}).Where("user_id = ? AND type = ? AND EXISTS (?)", userID, model.AdtyLedgerTypeCommissionTransfer, commission), siteCommissionConversionKey(commissionID, key))
}
