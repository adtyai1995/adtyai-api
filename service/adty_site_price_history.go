package service

import (
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// LoadAdtySitePricing reads prices and their version under the same lock as saves and activation.
func LoadAdtySitePricing(siteID, userID int) ([]model.AdtySitePrice, int64, error) {
	prices := []model.AdtySitePrice{}
	var version int64
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Select("id").First(&site, siteID).Error; err != nil {
			return err
		}
		if err := tx.Where("site_id = ? AND user_id = ?", siteID, userID).Order("id").Find(&prices).Error; err != nil {
			return err
		}
		var err error
		version, err = adtySitePricingVersionTx(tx, siteID, userID)
		return err
	})
	return prices, version, err
}

func ListAdtySitePricingHistory(siteID, userID, offset, limit int) ([]model.AdtyPriceRevision, int64, error) {
	if siteID <= 0 || userID < 0 {
		return nil, 0, ErrAdtySiteNotFound
	}
	query := model.DB.Model(&model.AdtyPriceRevision{}).Where("site_id = ? AND user_id = ?", siteID, userID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := []model.AdtyPriceRevision{}
	err := query.Order("id DESC").Offset(max(offset, 0)).Limit(min(max(limit, 1), 100)).Find(&items).Error
	return items, total, err
}
