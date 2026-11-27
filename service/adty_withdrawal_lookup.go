package service

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

func withdrawalReceiptIDAny(q *gorm.DB, key string, keys ...string) (int64, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 128 {
		return 0, common.NewMessage("Invalid idempotency key")
	}
	candidates := make([]string, 0, len(keys)+1)
	seen := map[string]struct{}{}
	for _, k := range append(keys, key) {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		candidates = append(candidates, k)
	}
	var id int64
	err := q.Where("idempotency_key IN ?", candidates).Limit(1).Pluck("id", &id).Error
	return id, err
}

func adtyReferralWithdrawScopedKey(userID int, rawKey string) string {
	return "u" + strconv.Itoa(userID) + ":" + common.Sha1([]byte(rawKey))
}

func adtySiteInviteWithdrawScopedKey(siteID, userID int, rawKey string) string {
	return "s" + strconv.Itoa(siteID) + ":u" + strconv.Itoa(userID) + ":" + common.Sha1([]byte(rawKey))
}

func LookupSiteInviteWithdrawal(userID, siteID int, key string) (int64, error) {
	if userID <= 0 || siteID <= 0 {
		return 0, common.NewMessage("Invalid parameters")
	}
	if _, err := RequireAdtySiteCommissionCustomerTx(model.DB, userID); err != nil {
		return 0, err
	}
	trimmed := strings.TrimSpace(key)
	return withdrawalReceiptIDAny(
		model.DB.Model(&model.AdtySiteInviteWithdrawal{}).Where("user_id = ? AND site_id = ?", userID, siteID),
		trimmed,
		adtySiteInviteWithdrawScopedKey(siteID, userID, trimmed),
	)
}

func LookupSiteOwnerWithdrawal(userID, siteID int, key string) (int64, error) {
	if userID <= 0 || siteID <= 0 {
		return 0, common.NewMessage("Invalid parameters")
	}
	trimmed := strings.TrimSpace(key)
	return withdrawalReceiptIDAny(
		model.DB.Model(&model.AdtySiteWithdrawal{}).Scopes(model.AdtyWithdrawalOwnerScope(siteID, userID)),
		trimmed,
		adtySiteWithdrawScopedKey(siteID, trimmed),
	)
}

func siteCommissionConversionKey(commissionID int64, key string) string {
	return "site-comm-cvt:" + strconv.FormatInt(commissionID, 10) + ":" + key
}

func LookupSiteCommissionConversion(userID, siteID int, commissionID int64, key string) (int64, error) {
	key = strings.TrimSpace(key)
	if userID <= 0 || siteID <= 0 || commissionID <= 0 || key == "" || len(key) > 40 {
		return 0, common.NewMessage("Invalid parameters")
	}
	if _, err := RequireAdtySiteCommissionCustomerTx(model.DB, userID); err != nil {
		return 0, err
	}
	commission := model.DB.Model(&model.AdtySiteInviteCommission{}).Select("1").Where("id = ? AND site_id = ? AND inviter_id = ?", commissionID, siteID, userID)
	return withdrawalReceiptIDAny(model.DB.Model(&model.AdtyBalanceLedger{}).Where("user_id = ? AND type = ? AND EXISTS (?)", userID, model.AdtyLedgerTypeCommissionTransfer, commission), siteCommissionConversionKey(commissionID, key))
}
