package model

import (
	"math"
	"strings"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func backfillAdtySiteMarketingPolicies(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		const needsPolicy = "marketing_policy_initialized IS NULL OR marketing_policy_initialized = ?"
		var count int64
		if err := tx.Model(&AdtySite{}).Where(needsPolicy, false).Count(&count).Error; err != nil || count == 0 {
			return err
		}
		fields := map[string]string{
			"adty_site_referral.pending_coverage_cap":     "pending_coverage_cap",
			"adty_site_referral.observation_days":         "observation_days",
			"adty_site_invite_withdraw.min_usd":           "invite_withdraw_min",
			"adty_site_invite_withdraw.fee_usd":           "invite_withdraw_fee",
			"adty_site_invite_withdraw.cny_rate":          "invite_withdraw_cny_rate",
			"adty_reseller_setting.trial_grant_max_usd":   "trial_grant_max_usd",
			"adty_reseller_setting.trial_grant_daily_usd": "trial_grant_daily_usd",
		}
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		updates := map[string]any{"marketing_policy_initialized": true}
		var options []Option
		if tx.Migrator().HasTable(&Option{}) {
			if err := tx.Where("key IN ?", keys).Find(&options).Error; err != nil {
				return err
			}
		}
		for _, op := range options {
			value, err := decimal.NewFromString(strings.TrimSpace(op.Value))
			if err != nil || value.IsNegative() {
				continue
			}
			column := fields[op.Key]
			if column == "observation_days" {
				if value.IsInteger() && value.IsPositive() && value.LessThanOrEqual(decimal.NewFromInt(math.MaxInt32)) {
					updates[column] = value.IntPart()
				}
				continue
			}
			if !value.LessThan(decimal.New(1, 12)) {
				continue
			}
			if value.IsZero() && (column == "pending_coverage_cap" || column == "invite_withdraw_min" || column == "invite_withdraw_cny_rate") {
				continue
			}
			updates[column] = value
		}
		// Older orders without a referral snapshot used the enabled flag and bind-time rate.
		if tx.Migrator().HasTable(&AdtySiteReferralRate{}) {
			legacySites := tx.Model(&AdtySite{}).Select("id").Where(needsPolicy, false)
			rateUpdates := map[string]any{"legacy_enabled": gorm.Expr("enabled")}
			if updates["pending_coverage_cap"] == nil || updates["observation_days"] == nil {
				rateUpdates["enabled"] = false
			}
			if err := tx.Model(&AdtySiteReferralRate{}).Where("site_id IN (?) AND legacy_enabled IS NULL", legacySites).UpdateColumns(rateUpdates).Error; err != nil {
				return err
			}
		}
		return tx.Model(&AdtySite{}).Where(needsPolicy, false).UpdateColumns(updates).Error
	})
}
