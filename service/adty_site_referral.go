package service

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var ErrAdtySiteReferralUnset = errors.New("site commission policy is not configured")

type AdtySiteReferralView struct {
	model.AdtySiteMarketingPolicy
	RateConfigured bool   `json:"rate_configured"`
	Rate           string `json:"rate,omitempty"`
	Enabled        bool   `json:"enabled"`
}

type AdtySiteMarketingUpdate struct {
	Rate                *string `json:"rate"`
	Enabled             *bool   `json:"enabled"`
	PendingCoverageCap  *string `json:"pending_coverage_cap"`
	CancelWindowMinutes *string `json:"cancel_window_minutes"`
	ObservationDays     *string `json:"observation_days"`
	MinUSD              *string `json:"min_usd"`
	FeeUSD              *string `json:"fee_usd"`
	CNYRate             *string `json:"cny_rate"`
	TrialGrantMaxUSD    *string `json:"trial_grant_max_usd"`
	TrialGrantDailyUSD  *string `json:"trial_grant_daily_usd"`
}

var siteMarketingDecimal = regexp.MustCompile(`^(?:[0-9]{1,12}(?:\.[0-9]{0,8})?|\.[0-9]{1,8})$`)

func parseSiteMarketingDecimal(raw string) (decimal.Decimal, error) {
	raw = strings.TrimSpace(raw)
	if !siteMarketingDecimal.MatchString(raw) {
		return decimal.Zero, common.NewMessage("Invalid parameters")
	}
	return decimal.NewFromString(raw)
}

func lockOwnerSite(siteID, ownerID int) (*model.AdtySite, error) {
	var site model.AdtySite
	if err := model.DB.First(&site, siteID).Error; err != nil {
		return nil, err
	}
	if site.OwnerUserId != ownerID {
		return nil, common.NewMessage("No permission to perform this action")
	}
	if site.Status != model.AdtySiteStatusActive && site.Status != model.AdtySiteStatusPaused {
		return nil, common.NewMessage("Site is not open for registration")
	}
	return &site, nil
}

// SaveAdtyOwnerSiteMarketing updates only the fields submitted by the owner.
func SaveAdtyOwnerSiteMarketing(siteID, ownerID int, input AdtySiteMarketingUpdate) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).First(&site, siteID).Error; err != nil {
			return err
		}
		if site.OwnerUserId != ownerID || (site.Status != model.AdtySiteStatusActive && site.Status != model.AdtySiteStatusPaused) {
			return common.NewMessage("No permission to perform this action")
		}
		policy := site.MarketingPolicy
		updates := map[string]any{}
		for _, field := range []struct {
			input     *string
			dest      **decimal.Decimal
			column    string
			allowZero bool
		}{
			{input.PendingCoverageCap, &policy.PendingCoverageCap, "pending_coverage_cap", false},
			{input.MinUSD, &policy.MinUSD, "invite_withdraw_min", false},
			{input.FeeUSD, &policy.FeeUSD, "invite_withdraw_fee", true},
			{input.CNYRate, &policy.CNYRate, "invite_withdraw_cny_rate", false},
			{input.TrialGrantMaxUSD, &policy.TrialGrantMaxUSD, "trial_grant_max_usd", true},
			{input.TrialGrantDailyUSD, &policy.TrialGrantDailyUSD, "trial_grant_daily_usd", true},
		} {
			if field.input == nil {
				continue
			}
			if strings.TrimSpace(*field.input) == "" {
				*field.dest = nil
				updates[field.column] = nil
				continue
			}
			value, err := parseSiteMarketingDecimal(*field.input)
			if err != nil || (!field.allowZero && value.IsZero()) {
				return common.NewMessage("Invalid parameters")
			}
			*field.dest = &value
			updates[field.column] = value
		}
		if input.CancelWindowMinutes != nil {
			raw := strings.TrimSpace(*input.CancelWindowMinutes)
			if raw == "" {
				updates["invite_cancel_window_minutes"] = nil
			} else {
				minutes, err := strconv.ParseUint(raw, 10, 31)
				if err != nil {
					return common.NewMessage("Invalid parameters")
				}
				updates["invite_cancel_window_minutes"] = minutes
			}
		}
		if input.ObservationDays != nil {
			raw := strings.TrimSpace(*input.ObservationDays)
			policy.ObservationDays = nil
			if raw != "" {
				days, err := strconv.ParseInt(raw, 10, 32)
				if err != nil || days < 0 {
					return common.NewMessage("Invalid parameters")
				}
				policy.ObservationDays = &days
			}
			updates["observation_days"] = policy.ObservationDays
		}
		if input.MinUSD != nil || input.FeeUSD != nil || input.CNYRate != nil {
			if (policy.MinUSD == nil) != (policy.FeeUSD == nil) || (policy.MinUSD == nil) != (policy.CNYRate == nil) {
				return common.NewMessage("Set the minimum, fee and exchange rate together to enable cash withdrawals")
			}
		}
		var rate model.AdtySiteReferralRate
		lookup := tx.Where("site_id = ?", siteID).Limit(1).Find(&rate)
		if lookup.Error != nil {
			return lookup.Error
		}
		if input.Rate != nil {
			value, err := parseSiteMarketingDecimal(*input.Rate)
			if err != nil || value.GreaterThan(decimal.NewFromInt(1)) || !value.Equal(value.Round(6)) {
				return common.NewMessage("Enter an invite rate between 0 and 1 with up to six decimal places")
			}
			rate.Rate = value
		}
		if input.Enabled != nil {
			rate.Enabled = *input.Enabled
		}
		if (input.Enabled != nil || input.Rate != nil || input.PendingCoverageCap != nil || input.ObservationDays != nil) && rate.Enabled &&
			((lookup.RowsAffected == 0 && input.Rate == nil) || policy.PendingCoverageCap == nil || policy.ObservationDays == nil) {
			return common.NewMessage("Set an invite rate, pending coverage cap and observation days before enabling commissions")
		}
		if len(updates) > 0 {
			if err := tx.Model(&site).Updates(updates).Error; err != nil {
				return err
			}
		}
		if input.Rate != nil || input.Enabled != nil {
			if lookup.RowsAffected == 0 && input.Rate == nil {
				return nil
			}
			rate.SiteId = siteID
			return tx.Save(&rate).Error
		}
		return nil
	})
}

// Legacy callers receive the same migration message as the retired HTTP endpoints.
var ErrSiteMarketingOwnerOnly = common.NewMessage("Site marketing rules are managed by the site owner")

func SaveAdtySiteReferralCap(string) error                         { return ErrSiteMarketingOwnerOnly }
func SaveAdtySiteCommissionPolicy(string, string) error            { return ErrSiteMarketingOwnerOnly }
func SaveAdtySiteInviteWithdrawRules(string, string, string) error { return ErrSiteMarketingOwnerOnly }

func SaveAdtyOwnerSiteReferralRate(siteID, ownerID int, rate string) error {
	return SaveAdtyOwnerSiteMarketing(siteID, ownerID, AdtySiteMarketingUpdate{Rate: &rate})
}
func SetAdtyOwnerSiteReferralEnabled(siteID, ownerID int, enabled bool) error {
	return SaveAdtyOwnerSiteMarketing(siteID, ownerID, AdtySiteMarketingUpdate{Enabled: &enabled})
}

func LoadAdtyOwnerSiteReferral(siteID, ownerID int) (AdtySiteReferralView, error) {
	site, err := lockOwnerSite(siteID, ownerID)
	if err != nil {
		return AdtySiteReferralView{}, err
	}
	view := AdtySiteReferralView{AdtySiteMarketingPolicy: site.MarketingPolicy}
	var row model.AdtySiteReferralRate
	lookup := model.DB.Where("site_id = ?", siteID).Limit(1).Find(&row)
	if lookup.Error != nil {
		return view, lookup.Error
	}
	if lookup.RowsAffected > 0 {
		view.RateConfigured = true
		view.Rate = row.Rate.String()
		view.Enabled = row.Enabled
	}
	return view, nil
}

// BindAdtyOwnerSiteInvite records one direct invite on the owner's site.
// It does not pay commission. A customer who already has a successful top-up cannot be bound.
func BindAdtyOwnerSiteInvite(siteID, ownerID, inviterID, inviteeID int) error {
	if _, err := lockOwnerSite(siteID, ownerID); err != nil {
		return err
	}
	return BindSiteInviteFromIP(siteID, inviterID, inviteeID, "")
}

// BindAdtyOwnerSiteInviteFromIP is the owner bind with the visitor IP hash.
func BindAdtyOwnerSiteInviteFromIP(siteID, ownerID, inviterID, inviteeID int, ipHash string) error {
	if _, err := lockOwnerSite(siteID, ownerID); err != nil {
		return err
	}
	return BindSiteInviteFromIP(siteID, inviterID, inviteeID, ipHash)
}

// SaveAdtyOwnerSiteTeamDiscount updates one site team's payment ratio.
// Validity follows G.2: final team price = max(site supply price, site regular
// sale price × payment ratio). Billing already applies that floor, so a ratio
// below the site cost factor is not rejected. Shared quota is not changed.
func SaveAdtyOwnerSiteTeamDiscount(siteID, ownerID int, teamID int64, raw string) error {
	if _, err := lockOwnerSite(siteID, ownerID); err != nil {
		return err
	}
	value, err := decimal.NewFromString(strings.TrimSpace(raw))
	if teamID <= 0 || err != nil || !adtySiteTeamPayRatioConfigValid(value) {
		return common.NewMessage("Invalid parameters")
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var team model.AdtyTeam
		if err := model.AdtyLockForUpdate(tx).First(&team, teamID).Error; err != nil {
			return err
		}
		if team.SiteId != siteID || team.Status != model.AdtyTeamStatusActive {
			return common.NewMessage("No permission to perform this action")
		}
		return tx.Model(&team).Update("discount", value).Error
	})
}

// ListAdtyOwnerSiteTeams returns active teams on the owner's site.
func ListAdtyOwnerSiteTeams(siteID, ownerID, offset, limit int, search string) ([]model.AdtyTeam, int64, error) {
	if _, err := lockOwnerSite(siteID, ownerID); err != nil {
		return nil, 0, err
	}
	q := model.DB.Model(&model.AdtyTeam{}).Where("site_id = ? AND status = ?", siteID, model.AdtyTeamStatusActive)
	if search = strings.TrimSpace(search); search != "" {
		name := "%" + strings.ToLower(search) + "%"
		if id, err := strconv.ParseInt(strings.TrimPrefix(search, "#"), 10, 64); err == nil {
			q = q.Where("(id = ? OR LOWER(name) LIKE ?)", id, name)
		} else {
			q = q.Where("LOWER(name) LIKE ?", name)
		}
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.AdtyTeam
	err := q.Order("id desc").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

// ListAdtySiteInvites returns the owner's direct site invites. It does not create any.
func ListAdtySiteInvites(siteID, ownerID int) ([]model.AdtySiteInvite, error) {
	if _, err := lockOwnerSite(siteID, ownerID); err != nil {
		return nil, err
	}
	var rows []model.AdtySiteInvite
	err := model.DB.Where("site_id = ?", siteID).Order("id desc").Limit(50).Find(&rows).Error
	return rows, err
}
