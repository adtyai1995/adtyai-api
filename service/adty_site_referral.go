package service

import (
	"crypto/rand"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

const adtySiteCustomerInviteAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

type AdtySiteCustomerReferralInvitee struct {
	InviteeId       int    `json:"invitee_id"`
	InviteeUsername string `json:"invitee_username"`
	Rate            string `json:"rate"`
	RatePercent     string `json:"rate_percent"`
	CreatedAt       int64  `json:"created_at"`
}

type AdtySiteCustomerReferralProfile struct {
	SiteId             int                               `json:"site_id"`
	SiteName           string                            `json:"site_name"`
	SiteStatus         string                            `json:"site_status"`
	Enabled            bool                              `json:"enabled"`
	RateConfigured     bool                              `json:"rate_configured"`
	Rate               string                            `json:"rate,omitempty"`
	RatePercent        string                            `json:"rate_percent,omitempty"`
	ObservationDays    *int64                            `json:"observation_days,omitempty"`
	PendingCoverageCap *decimal.Decimal                  `json:"pending_coverage_cap,omitempty"`
	InviteCode         string                            `json:"invite_code"`
	InviteURL          string                            `json:"invite_url"`
	InvitedCount       int64                             `json:"invited_count"`
	Invites            []AdtySiteCustomerReferralInvitee `json:"invites"`
	HasInviter         bool                              `json:"has_inviter"`
	InviterUsername    string                            `json:"inviter_username,omitempty"`
	CanBind            bool                              `json:"can_bind"`
}

func generateAdtySiteCustomerInviteCode() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, len(buf))
	for i, b := range buf {
		out[i] = adtySiteCustomerInviteAlphabet[int(b)%len(adtySiteCustomerInviteAlphabet)]
	}
	return string(out), nil
}

func ensureAdtySiteCustomerInviteCode(userID, siteID int) (string, error) {
	for range 8 {
		var code string
		retry := false
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.AdtySiteCustomer{
				UserId:   userID,
				SiteId:   siteID,
				Status:   model.AdtySiteCustomerActive,
				JoinedAt: common.GetTimestamp(),
			}).Error; err != nil {
				return err
			}
			var cust model.AdtySiteCustomer
			if err := model.AdtyLockForUpdate(tx).Where("user_id = ?", userID).First(&cust).Error; err != nil {
				return err
			}
			if cust.InviteCode != nil && strings.TrimSpace(*cust.InviteCode) != "" {
				code = strings.TrimSpace(*cust.InviteCode)
				return nil
			}
			candidate, err := generateAdtySiteCustomerInviteCode()
			if err != nil {
				return err
			}
			var dup int64
			if err := tx.Model(&model.AdtySiteCustomer{}).Where("invite_code = ?", candidate).Count(&dup).Error; err != nil {
				return err
			}
			if dup > 0 {
				retry = true
				return nil
			}
			if err := tx.Model(&model.AdtySiteCustomer{}).Where("user_id = ?", userID).Update("invite_code", candidate).Error; err != nil {
				return err
			}
			code = candidate
			return nil
		})
		if err != nil {
			continue
		}
		if !retry && code != "" {
			return code, nil
		}
	}
	return "", common.NewMessage("Failed to generate site invite code")
}

// GetAdtySiteCustomerReferralProfile returns the subsite customer's level-1 invite
// code, registration link, site referral policy, invited users, and bind eligibility.
func GetAdtySiteCustomerReferralProfile(userID int) (*AdtySiteCustomerReferralProfile, error) {
	if userID <= 0 {
		return nil, common.NewMessage("No permission to perform this action")
	}
	var user model.User
	if err := model.DB.Select("id", "site_id", "staff_site_id", "status").First(&user, userID).Error; err != nil {
		return nil, err
	}
	if user.SiteId <= 0 || user.StaffSiteId > 0 || user.Status != common.UserStatusEnabled {
		return nil, common.NewMessage("Only sub-site customers can use sub-site invitations")
	}
	var site model.AdtySite
	if err := model.DB.First(&site, user.SiteId).Error; err != nil {
		return nil, err
	}
	if site.Status == model.AdtySiteStatusClosed {
		return nil, common.NewMessage("Site is not open for registration")
	}
	code, err := ensureAdtySiteCustomerInviteCode(user.Id, site.Id)
	if err != nil {
		return nil, err
	}
	var rateRow model.AdtySiteReferralRate
	rateLookup := model.DB.Where("site_id = ?", site.Id).Limit(1).Find(&rateRow)
	if rateLookup.Error != nil {
		return nil, rateLookup.Error
	}
	var invitedCount int64
	if err := model.DB.Model(&model.AdtySiteInvite{}).Where("site_id = ? AND inviter_id = ?", site.Id, user.Id).Count(&invitedCount).Error; err != nil {
		return nil, err
	}
	var inviteRows []model.AdtySiteInvite
	if err := model.DB.Where("site_id = ? AND inviter_id = ?", site.Id, user.Id).Order("id desc").Limit(50).Find(&inviteRows).Error; err != nil {
		return nil, err
	}
	inviteeIDs := make([]int, 0, len(inviteRows))
	for _, row := range inviteRows {
		inviteeIDs = append(inviteeIDs, row.InviteeId)
	}
	userNames := map[int]string{}
	if len(inviteeIDs) > 0 {
		var users []model.User
		if err := model.DB.Select("id", "username").Where("id IN ?", inviteeIDs).Find(&users).Error; err != nil {
			return nil, err
		}
		for _, u := range users {
			userNames[u.Id] = u.Username
		}
	}
	invites := make([]AdtySiteCustomerReferralInvitee, 0, len(inviteRows))
	for _, row := range inviteRows {
		invites = append(invites, AdtySiteCustomerReferralInvitee{
			InviteeId:       row.InviteeId,
			InviteeUsername: userNames[row.InviteeId],
			Rate:            row.Rate.String(),
			RatePercent:     row.Rate.Mul(decimal.NewFromInt(100)).String(),
			CreatedAt:       row.CreatedAt,
		})
	}
	var bound model.AdtySiteInvite
	boundLookup := model.DB.Where("invitee_id = ?", user.Id).Limit(1).Find(&bound)
	if boundLookup.Error != nil {
		return nil, boundLookup.Error
	}
	hasInviter := boundLookup.RowsAffected > 0
	inviterUsername := ""
	if hasInviter {
		var inviter model.User
		if err := model.DB.Select("id", "username").First(&inviter, bound.InviterId).Error; err == nil {
			inviterUsername = inviter.Username
		}
	}
	var paid int64
	if err := model.DB.Model(&model.TopUp{}).Where("user_id = ? AND status = ?", user.Id, common.TopUpStatusSuccess).Count(&paid).Error; err != nil {
		return nil, err
	}
	siteName := strings.TrimSpace(AdtyBrandFor(&site).Name)
	if siteName == "" {
		siteName = site.Subdomain
	}
	baseURL := strings.TrimRight(AdtySitePublicBaseURL(&site), "/")
	inviteURL := "/register?site_invite=" + url.QueryEscape(code)
	if baseURL != "" {
		inviteURL = baseURL + inviteURL
	}
	rateConfigured := rateLookup.RowsAffected > 0
	enabled := rateConfigured && rateRow.Enabled && rateRow.Rate.IsPositive()
	siteOpen := site.Status != model.AdtySiteStatusClosed && site.Status != model.AdtySiteStatusClosing
	profile := &AdtySiteCustomerReferralProfile{
		SiteId:             site.Id,
		SiteName:           siteName,
		SiteStatus:         site.Status,
		Enabled:            enabled,
		RateConfigured:     rateConfigured,
		ObservationDays:    site.MarketingPolicy.ObservationDays,
		PendingCoverageCap: site.MarketingPolicy.PendingCoverageCap,
		InviteCode:         code,
		InviteURL:          inviteURL,
		InvitedCount:       invitedCount,
		Invites:            invites,
		HasInviter:         hasInviter,
		InviterUsername:    inviterUsername,
		CanBind:            !hasInviter && paid == 0 && enabled && siteOpen,
	}
	if rateConfigured {
		profile.Rate = rateRow.Rate.String()
		profile.RatePercent = rateRow.Rate.Mul(decimal.NewFromInt(100)).String()
	}
	return profile, nil
}

// BindSiteInviteByCodeFromIP resolves a subsite customer invite code within siteID
// and delegates binding to BindSiteInviteFromIP (same site, enabled referral, before first paid order).
func BindSiteInviteByCodeFromIP(siteID int, code string, inviteeID int, ipHash string) error {
	code = strings.ToUpper(strings.TrimSpace(code))
	if siteID <= 0 || inviteeID <= 0 || code == "" || len(code) > 24 {
		return common.NewMessage("Invalid site invite code")
	}
	var rate model.AdtySiteReferralRate
	if err := model.DB.Where("site_id = ?", siteID).First(&rate).Error; err != nil || !rate.Enabled || !rate.Rate.IsPositive() {
		return common.NewMessage("Site referral is not enabled")
	}
	var cust model.AdtySiteCustomer
	lookup := model.AdtyLiveSiteCustomer(model.DB).Where("site_id = ? AND invite_code = ?", siteID, code).Limit(1).Find(&cust)
	if lookup.Error != nil {
		return lookup.Error
	}
	if lookup.RowsAffected == 0 || cust.UserId <= 0 {
		return common.NewMessage("Invalid site invite code")
	}
	if cust.UserId == inviteeID {
		return common.NewMessage("You cannot invite yourself")
	}
	var inviter model.User
	if err := model.DB.Select("id", "site_id", "staff_site_id", "status").First(&inviter, cust.UserId).Error; err != nil {
		return common.NewMessage("Invalid site invite code")
	}
	if inviter.SiteId != siteID || inviter.StaffSiteId > 0 || inviter.Status != common.UserStatusEnabled {
		return common.NewMessage("Invalid site invite code")
	}
	var invitee model.User
	if err := model.DB.Select("id", "site_id", "staff_site_id", "status").First(&invitee, inviteeID).Error; err != nil {
		return err
	}
	if invitee.SiteId != siteID || invitee.StaffSiteId > 0 || invitee.Status != common.UserStatusEnabled {
		return common.NewMessage("Only sub-site customers can use sub-site invitations")
	}
	return BindSiteInviteFromIP(siteID, cust.UserId, inviteeID, ipHash)
}

// BindAdtySiteInviteByCodeForUser lets a logged-in subsite customer bind an inviter
// code manually before their first paid order.
func BindAdtySiteInviteByCodeForUser(userID int, code, ipHash string) error {
	if userID <= 0 {
		return common.NewMessage("No permission to perform this action")
	}
	var user model.User
	if err := model.DB.Select("id", "site_id", "staff_site_id", "status").First(&user, userID).Error; err != nil {
		return err
	}
	if user.SiteId <= 0 || user.StaffSiteId > 0 || user.Status != common.UserStatusEnabled {
		return common.NewMessage("Only sub-site customers can use sub-site invitations")
	}
	return BindSiteInviteByCodeFromIP(user.SiteId, code, user.Id, ipHash)
}
