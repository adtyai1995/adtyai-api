package service

import (
	"crypto/rand"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrAdtySiteReferralUnset     = errors.New("site commission policy is not configured")
	ErrAdtySiteReferralRateLimit = common.NewMessage("Subsite referral commission rate cannot exceed 30%")
	adtySiteMaxReferralRate      = decimal.RequireFromString("0.30")
)

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
	if input.TrialGrantMaxUSD != nil || input.TrialGrantDailyUSD != nil {
		return common.NewMessage("Trial credit budgets have been retired")
	}
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
			if err != nil || value.IsNegative() || !value.Equal(value.Round(6)) {
				return common.NewMessage("Enter an invite rate between 0 and 0.3 with up to six decimal places")
			}
			if value.GreaterThan(adtySiteMaxReferralRate) {
				return ErrAdtySiteReferralRateLimit
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

// SaveAdtyOwnerSiteTeamDiscount updates one site team's supply-price multiplier.
// Subsite teams use team_price = max(supply_price, supply_price * discount) with 1 <= discount <= 10.
// Shared quota is not changed.
func SaveAdtyOwnerSiteTeamDiscount(siteID, ownerID int, teamID int64, raw string) error {
	if _, err := lockOwnerSite(siteID, ownerID); err != nil {
		return err
	}
	value, err := decimal.NewFromString(strings.TrimSpace(raw))
	if teamID <= 0 || err != nil {
		return common.NewMessage("Invalid parameters")
	}
	if err := validateAdtyTeamDiscountForSite(siteID, value); err != nil {
		return err
	}
	var event model.AuditLog
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var team model.AdtyTeam
		if err := model.AdtyLockForUpdate(tx).First(&team, teamID).Error; err != nil {
			return err
		}
		if team.SiteId != siteID || team.Status != model.AdtyTeamStatusActive {
			return common.NewMessage("No permission to perform this action")
		}
		before := team.Discount.String()
		if err := tx.Model(&team).Update("discount", value).Error; err != nil {
			return err
		}
		event, err = appendAdtyTeamAuditTx(tx, ownerID, siteID, "team", strconv.FormatInt(teamID, 10), "adty.team.discount.update",
			map[string]any{"team_id": teamID, "before": before},
			map[string]any{"team_id": teamID, "before": before, "after": value.String()})
		return err
	})
	if err != nil {
		return err
	}
	deliverAdtyCommittedAudit(event)
	return nil
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
	if isEmp, empErr := AdtyIsIndependentTeamEmployee(userID); empErr != nil {
		return nil, empErr
	} else if isEmp {
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
	if err := model.DB.Model(&model.TopUp{}).Where("user_id = ? AND status IN ?", user.Id, []string{common.TopUpStatusSuccess, model.TopUpStatusRefunded}).Count(&paid).Error; err != nil {
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
	if isEmp, empErr := AdtyIsIndependentTeamEmployee(userID); empErr != nil {
		return empErr
	} else if isEmp {
		return common.NewMessage("Only sub-site customers can use sub-site invitations")
	}
	return BindSiteInviteByCodeFromIP(user.SiteId, code, user.Id, ipHash)
}

// AdtyOwnerSiteReferralOverview summarizes subsite referral activity for the site owner.
type AdtyOwnerSiteReferralOverview struct {
	Affiliates           int64  `json:"affiliates"`
	Bindings             int64  `json:"bindings"`
	Pending              string `json:"pending"`
	Available            string `json:"available"`
	Frozen               string `json:"frozen"`
	Withdrawn            string `json:"withdrawn"`
	PendingCoverageCount int64  `json:"pending_coverage_count"`
	PendingWithdrawals   int64  `json:"pending_withdrawals"`
}

// AdtySiteAffiliateRow is one inviter summary row for the subsite owner's referral console.
type AdtySiteAffiliateRow struct {
	UserId      int    `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name,omitempty"`
	InviteCount int64  `json:"invite_count"`
	Pending     string `json:"pending"`
	Available   string `json:"available"`
	Frozen      string `json:"frozen"`
	Withdrawn   string `json:"withdrawn"`
	TotalEarned string `json:"total_earned"`
}

// AdtyOwnerSiteCommissionRow is one subsite invite commission row enriched for the site owner.
type AdtyOwnerSiteCommissionRow struct {
	Id              int64                                 `json:"id"`
	SiteId          int                                   `json:"site_id"`
	OrderId         int64                                 `json:"order_id"`
	TradeNo         string                                `json:"trade_no"`
	InviterId       int                                   `json:"inviter_id"`
	InviterUsername string                                `json:"inviter_username"`
	InviteeId       int                                   `json:"invitee_id"`
	InviteeUsername string                                `json:"invitee_username"`
	OrderAmountUSD  string                                `json:"order_amount_usd"`
	FaceUSD         string                                `json:"face_usd"`
	Rate            string                                `json:"rate"`
	AmountUSD       string                                `json:"amount_usd"`
	CoveredUSD      string                                `json:"covered_usd"`
	RemainingUSD    string                                `json:"remaining_usd"`
	ConvertedUSD    string                                `json:"converted_usd"`
	WithdrawnUSD    string                                `json:"withdrawn_usd"`
	FrozenUSD       string                                `json:"frozen_usd"`
	ClawedUSD       string                                `json:"clawed_usd"`
	Status          string                                `json:"status"`
	CoverageStatus  string                                `json:"coverage_status"`
	ObservationDays int64                                 `json:"observation_days"`
	CoveredAt       int64                                 `json:"covered_at"`
	AvailableAt     int64                                 `json:"available_at"`
	ObservableAt    int64                                 `json:"observable_at"`
	SettleAt        int64                                 `json:"settle_at"`
	CreatedAt       int64                                 `json:"created_at"`
	OfficialStates  []model.AdtySiteCommissionStateAmount `json:"official_states,omitempty"`
}

type adtySiteReferralUserSummary struct {
	Username    string
	DisplayName string
}

func classifySiteInviteCommissionRow(row *model.AdtySiteInviteCommission, now time.Time) (pending, available, netEarned decimal.Decimal) {
	if row == nil {
		return decimal.Zero, decimal.Zero, decimal.Zero
	}
	netEarned = row.AmountUSD.Sub(row.ClawedUSD)
	if netEarned.IsNegative() {
		netEarned = decimal.Zero
	}
	switch row.Status {
	case model.AdtySiteCommissionPendingCoverage:
		pending = netEarned
	case model.AdtySiteCommissionCovered:
		pending = siteInviteCommissionFree(row)
	case model.AdtySiteCommissionAvailable:
		free := siteInviteCommissionFree(row)
		if SiteInviteCommissionWithdrawable(row, now) {
			available = free
		} else {
			pending = free
		}
	case model.AdtySiteCommissionClawedBack:
		// Fully clawed back rows contribute neither pending nor available funds.
	}
	return pending, available, netEarned
}

func siteInviteCommissionCoverageStatus(row *model.AdtySiteInviteCommission) string {
	if row == nil {
		return ""
	}
	switch row.Status {
	case model.AdtySiteCommissionPendingCoverage:
		if row.CoveredUSD.IsPositive() {
			return "partial"
		}
		return model.AdtySiteCommissionPendingCoverage
	case model.AdtySiteCommissionClawedBack:
		return model.AdtySiteCommissionClawedBack
	default:
		return model.AdtySiteCommissionCovered
	}
}

func loadSiteReferralUserSummaries(ids []int) (map[int]adtySiteReferralUserSummary, error) {
	out := make(map[int]adtySiteReferralUserSummary, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	unique := make([]int, 0, len(ids))
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	const chunkSize = 500
	for start := 0; start < len(unique); start += chunkSize {
		end := start + chunkSize
		if end > len(unique) {
			end = len(unique)
		}
		var users []model.User
		if err := model.DB.Unscoped().Select("id", "username", "display_name").Where("id IN ?", unique[start:end]).Find(&users).Error; err != nil {
			return nil, err
		}
		for _, u := range users {
			out[u.Id] = adtySiteReferralUserSummary{
				Username:    u.Username,
				DisplayName: u.DisplayName,
			}
		}
	}
	return out, nil
}

// LoadAdtyOwnerSiteReferralOverview aggregates referral metrics strictly scoped to siteID.
func LoadAdtyOwnerSiteReferralOverview(siteID int, now time.Time) (AdtyOwnerSiteReferralOverview, error) {
	var out AdtyOwnerSiteReferralOverview
	if siteID <= 0 {
		return out, common.NewMessage("Invalid parameters")
	}
	if now.IsZero() {
		now = time.Now()
	}
	if err := model.DB.Model(&model.AdtySiteInvite{}).Where("site_id = ?", siteID).Count(&out.Bindings).Error; err != nil {
		return out, err
	}
	var inviteInviters []int
	if err := model.DB.Model(&model.AdtySiteInvite{}).
		Where("site_id = ? AND inviter_id > 0", siteID).
		Distinct("inviter_id").
		Pluck("inviter_id", &inviteInviters).Error; err != nil {
		return out, err
	}
	affiliatesSet := make(map[int]struct{}, len(inviteInviters))
	for _, uid := range inviteInviters {
		affiliatesSet[uid] = struct{}{}
	}

	pendingTotal := decimal.Zero
	availableTotal := decimal.Zero
	commConverted := decimal.Zero
	commWithdrawn := decimal.Zero
	var commBatch []model.AdtySiteInviteCommission
	if err := model.DB.Select(
		"id", "inviter_id", "status", "amount_usd", "clawed_usd",
		"earnings_hold_usd", "margin_hold_usd", "frozen_usd",
		"converted_usd", "withdrawn_usd", "available_at",
	).Where("site_id = ?", siteID).FindInBatches(&commBatch, 1000, func(_ *gorm.DB, _ int) error {
		for i := range commBatch {
			row := &commBatch[i]
			if row.InviterId > 0 {
				affiliatesSet[row.InviterId] = struct{}{}
			}
			if row.Status == model.AdtySiteCommissionPendingCoverage {
				out.PendingCoverageCount++
			}
			pending, available, _ := classifySiteInviteCommissionRow(row, now)
			pendingTotal = pendingTotal.Add(pending)
			availableTotal = availableTotal.Add(available)
			commConverted = commConverted.Add(row.ConvertedUSD)
			commWithdrawn = commWithdrawn.Add(row.WithdrawnUSD)
		}
		return nil
	}).Error; err != nil {
		return out, err
	}

	frozenTotal := decimal.Zero
	cashPaid := decimal.Zero
	balancePaid := decimal.Zero
	var wdBatch []model.AdtySiteInviteWithdrawal
	if err := model.DB.Select("id", "status", "method", "amount_usd").
		Where("site_id = ? AND status IN ?", siteID, []string{
			model.AdtySiteInviteWithdrawPending,
			model.AdtySiteInviteWithdrawApproved,
			model.AdtySiteInviteWithdrawPaid,
		}).
		FindInBatches(&wdBatch, 1000, func(_ *gorm.DB, _ int) error {
			for i := range wdBatch {
				w := &wdBatch[i]
				switch w.Status {
				case model.AdtySiteInviteWithdrawPending, model.AdtySiteInviteWithdrawApproved:
					out.PendingWithdrawals++
					frozenTotal = frozenTotal.Add(w.AmountUSD)
				case model.AdtySiteInviteWithdrawPaid:
					if w.Method == "balance" {
						balancePaid = balancePaid.Add(w.AmountUSD)
					} else {
						cashPaid = cashPaid.Add(w.AmountUSD)
					}
				}
			}
			return nil
		}).Error; err != nil {
		return out, err
	}

	// Avoid double-counting balance withdrawals (which update both withdrawals and commissions.converted_usd)
	// while still counting direct ConvertAdtySiteInviteCommission conversions.
	withdrawnTotal := decimal.Max(cashPaid, commWithdrawn).Add(decimal.Max(balancePaid, commConverted))

	out.Affiliates = int64(len(affiliatesSet))
	out.Pending = pendingTotal.StringFixed(2)
	out.Available = availableTotal.StringFixed(2)
	out.Frozen = frozenTotal.StringFixed(2)
	out.Withdrawn = withdrawnTotal.StringFixed(2)
	return out, nil
}

// ListAdtyOwnerSiteReferralAffiliates returns paginated subsite affiliates with per-user commission totals.
func ListAdtyOwnerSiteReferralAffiliates(siteID int, q string, offset, limit int, now time.Time) ([]AdtySiteAffiliateRow, int64, error) {
	if siteID <= 0 {
		return nil, 0, common.NewMessage("Invalid parameters")
	}
	if now.IsZero() {
		now = time.Now()
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	type inviteCountRow struct {
		InviterId   int
		InviteCount int64
	}
	var invCountRows []inviteCountRow
	if err := model.DB.Model(&model.AdtySiteInvite{}).
		Select("inviter_id, COUNT(*) AS invite_count").
		Where("site_id = ? AND inviter_id > 0", siteID).
		Group("inviter_id").
		Scan(&invCountRows).Error; err != nil {
		return nil, 0, err
	}
	inviteCounts := make(map[int]int64, len(invCountRows))
	affiliateSet := make(map[int]struct{}, len(invCountRows))
	for _, r := range invCountRows {
		inviteCounts[r.InviterId] = r.InviteCount
		affiliateSet[r.InviterId] = struct{}{}
	}

	var commInviters []int
	if err := model.DB.Model(&model.AdtySiteInviteCommission{}).
		Where("site_id = ? AND inviter_id > 0", siteID).
		Distinct("inviter_id").
		Pluck("inviter_id", &commInviters).Error; err != nil {
		return nil, 0, err
	}
	for _, uid := range commInviters {
		affiliateSet[uid] = struct{}{}
	}
	if len(affiliateSet) == 0 {
		return []AdtySiteAffiliateRow{}, 0, nil
	}

	filteredIDs := make([]int, 0, len(affiliateSet))
	for uid := range affiliateSet {
		filteredIDs = append(filteredIDs, uid)
	}

	var userSummaries map[int]adtySiteReferralUserSummary
	if q = strings.TrimSpace(q); q != "" {
		var err error
		userSummaries, err = loadSiteReferralUserSummaries(filteredIDs)
		if err != nil {
			return nil, 0, err
		}
		lowerQ := strings.ToLower(q)
		parsedID, idErr := strconv.Atoi(strings.TrimPrefix(q, "#"))
		hasID := idErr == nil && parsedID > 0
		matched := make([]int, 0, len(filteredIDs))
		for _, uid := range filteredIDs {
			u := userSummaries[uid]
			if (hasID && uid == parsedID) ||
				strings.Contains(strings.ToLower(u.Username), lowerQ) ||
				strings.Contains(strings.ToLower(u.DisplayName), lowerQ) {
				matched = append(matched, uid)
			}
		}
		filteredIDs = matched
	}

	total := int64(len(filteredIDs))
	if total == 0 || offset >= len(filteredIDs) {
		return []AdtySiteAffiliateRow{}, total, nil
	}
	sort.Slice(filteredIDs, func(i, j int) bool {
		ci, cj := inviteCounts[filteredIDs[i]], inviteCounts[filteredIDs[j]]
		if ci != cj {
			return ci > cj
		}
		return filteredIDs[i] > filteredIDs[j]
	})
	end := offset + limit
	if end > len(filteredIDs) {
		end = len(filteredIDs)
	}
	pageIDs := filteredIDs[offset:end]

	if userSummaries == nil {
		var err error
		userSummaries, err = loadSiteReferralUserSummaries(pageIDs)
		if err != nil {
			return nil, 0, err
		}
	}

	type affiliateTotals struct {
		pending       decimal.Decimal
		available     decimal.Decimal
		totalEarned   decimal.Decimal
		commConverted decimal.Decimal
		commWithdrawn decimal.Decimal
		frozen        decimal.Decimal
		cashPaid      decimal.Decimal
		balancePaid   decimal.Decimal
	}
	totalsByUser := make(map[int]*affiliateTotals, len(pageIDs))
	for _, uid := range pageIDs {
		totalsByUser[uid] = &affiliateTotals{}
	}

	var commBatch []model.AdtySiteInviteCommission
	if err := model.DB.Select(
		"id", "inviter_id", "status", "amount_usd", "clawed_usd",
		"earnings_hold_usd", "margin_hold_usd", "frozen_usd",
		"converted_usd", "withdrawn_usd", "available_at",
	).Where("site_id = ? AND inviter_id IN ?", siteID, pageIDs).
		FindInBatches(&commBatch, 1000, func(_ *gorm.DB, _ int) error {
			for i := range commBatch {
				row := &commBatch[i]
				agg := totalsByUser[row.InviterId]
				if agg == nil {
					continue
				}
				pending, available, netEarned := classifySiteInviteCommissionRow(row, now)
				agg.pending = agg.pending.Add(pending)
				agg.available = agg.available.Add(available)
				agg.totalEarned = agg.totalEarned.Add(netEarned)
				agg.commConverted = agg.commConverted.Add(row.ConvertedUSD)
				agg.commWithdrawn = agg.commWithdrawn.Add(row.WithdrawnUSD)
			}
			return nil
		}).Error; err != nil {
		return nil, 0, err
	}

	var wdBatch []model.AdtySiteInviteWithdrawal
	if err := model.DB.Select("id", "user_id", "status", "method", "amount_usd").
		Where("site_id = ? AND user_id IN ? AND status IN ?", siteID, pageIDs, []string{
			model.AdtySiteInviteWithdrawPending,
			model.AdtySiteInviteWithdrawApproved,
			model.AdtySiteInviteWithdrawPaid,
		}).
		FindInBatches(&wdBatch, 1000, func(_ *gorm.DB, _ int) error {
			for i := range wdBatch {
				w := &wdBatch[i]
				agg := totalsByUser[w.UserId]
				if agg == nil {
					continue
				}
				switch w.Status {
				case model.AdtySiteInviteWithdrawPending, model.AdtySiteInviteWithdrawApproved:
					agg.frozen = agg.frozen.Add(w.AmountUSD)
				case model.AdtySiteInviteWithdrawPaid:
					if w.Method == "balance" {
						agg.balancePaid = agg.balancePaid.Add(w.AmountUSD)
					} else {
						agg.cashPaid = agg.cashPaid.Add(w.AmountUSD)
					}
				}
			}
			return nil
		}).Error; err != nil {
		return nil, 0, err
	}

	items := make([]AdtySiteAffiliateRow, 0, len(pageIDs))
	for _, uid := range pageIDs {
		u := userSummaries[uid]
		agg := totalsByUser[uid]
		withdrawn := decimal.Max(agg.cashPaid, agg.commWithdrawn).Add(decimal.Max(agg.balancePaid, agg.commConverted))
		items = append(items, AdtySiteAffiliateRow{
			UserId:      uid,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			InviteCount: inviteCounts[uid],
			Pending:     agg.pending.StringFixed(2),
			Available:   agg.available.StringFixed(2),
			Frozen:      agg.frozen.StringFixed(2),
			Withdrawn:   withdrawn.StringFixed(2),
			TotalEarned: agg.totalEarned.StringFixed(2),
		})
	}
	return items, total, nil
}

// ListAdtyOwnerSiteReferralCommissions returns paginated subsite invite commissions for siteID.
func ListAdtyOwnerSiteReferralCommissions(siteID int, status, q string, offset, limit int, now time.Time, inviterUserID ...int) ([]AdtyOwnerSiteCommissionRow, int64, error) {
	if siteID <= 0 {
		return nil, 0, common.NewMessage("Invalid parameters")
	}
	if now.IsZero() {
		now = time.Now()
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	query := model.DB.Model(&model.AdtySiteInviteCommission{}).Where("site_id = ?", siteID)
	if len(inviterUserID) > 0 && inviterUserID[0] > 0 {
		query = query.Where("inviter_id = ?", inviterUserID[0])
	}
	switch trimmedStatus := strings.TrimSpace(status); trimmedStatus {
	case "", "all":
	case "pending":
		query = query.Where("status IN ?", []string{model.AdtySiteCommissionPendingCoverage, model.AdtySiteCommissionCovered})
	case model.AdtySiteCommissionPendingCoverage, model.AdtySiteCommissionCovered, model.AdtySiteCommissionAvailable, model.AdtySiteCommissionClawedBack:
		query = query.Where("status = ?", trimmedStatus)
	default:
		return nil, 0, common.NewMessage("Invalid parameters")
	}

	if q = strings.TrimSpace(q); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		userMatch := func() *gorm.DB {
			return model.DB.Unscoped().Model(&model.User{}).Select("id").Where("LOWER(username) LIKE ? OR LOWER(display_name) LIKE ?", like, like)
		}
		if id, err := strconv.Atoi(strings.TrimPrefix(q, "#")); err == nil && id > 0 {
			query = query.Where(
				"(LOWER(trade_no) LIKE ? OR inviter_id = ? OR invitee_id = ? OR inviter_id IN (?) OR invitee_id IN (?))",
				like, id, id, userMatch(), userMatch(),
			)
		} else {
			query = query.Where(
				"(LOWER(trade_no) LIKE ? OR inviter_id IN (?) OR invitee_id IN (?))",
				like, userMatch(), userMatch(),
			)
		}
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []AdtyOwnerSiteCommissionRow{}, 0, nil
	}

	var rows []model.AdtySiteInviteCommission
	if err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	if len(rows) == 0 {
		return []AdtyOwnerSiteCommissionRow{}, total, nil
	}

	userIDs := make([]int, 0, len(rows)*2)
	tradeNos := make([]string, 0, len(rows))
	tradeSeen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		userIDs = append(userIDs, row.InviterId, row.InviteeId)
		if row.TradeNo != "" {
			if _, ok := tradeSeen[row.TradeNo]; !ok {
				tradeSeen[row.TradeNo] = struct{}{}
				tradeNos = append(tradeNos, row.TradeNo)
			}
		}
	}
	users, err := loadSiteReferralUserSummaries(userIDs)
	if err != nil {
		return nil, 0, err
	}

	orderIDByTrade := make(map[string]int64, len(tradeNos))
	if len(tradeNos) > 0 {
		var orders []model.AdtyTopupOrder
		if err := model.DB.Select("id", "trade_no").Where("site_id = ? AND trade_no IN ?", siteID, tradeNos).Find(&orders).Error; err != nil {
			return nil, 0, err
		}
		for _, ord := range orders {
			orderIDByTrade[ord.TradeNo] = ord.Id
		}
	}

	presented, err := model.PresentSiteInviteCommissions(model.DB, rows)
	if err != nil {
		return nil, 0, err
	}

	items := make([]AdtyOwnerSiteCommissionRow, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		var states []model.AdtySiteCommissionStateAmount
		if i < len(presented) {
			states = presented[i].OfficialStates
		}
		items = append(items, AdtyOwnerSiteCommissionRow{
			Id:              row.Id,
			SiteId:          row.SiteId,
			OrderId:         orderIDByTrade[row.TradeNo],
			TradeNo:         row.TradeNo,
			InviterId:       row.InviterId,
			InviterUsername: users[row.InviterId].Username,
			InviteeId:       row.InviteeId,
			InviteeUsername: users[row.InviteeId].Username,
			OrderAmountUSD:  row.FaceUSD.String(),
			FaceUSD:         row.FaceUSD.String(),
			Rate:            row.Rate.String(),
			AmountUSD:       row.AmountUSD.String(),
			CoveredUSD:      row.CoveredUSD.String(),
			RemainingUSD:    siteInviteCommissionFree(row).String(),
			ConvertedUSD:    row.ConvertedUSD.String(),
			WithdrawnUSD:    row.WithdrawnUSD.String(),
			FrozenUSD:       row.FrozenUSD.String(),
			ClawedUSD:       row.ClawedUSD.String(),
			Status:          row.Status,
			CoverageStatus:  siteInviteCommissionCoverageStatus(row),
			ObservationDays: row.ObservationDays,
			CoveredAt:       row.CoveredAt,
			AvailableAt:     row.AvailableAt,
			ObservableAt:    row.AvailableAt,
			SettleAt:        row.AvailableAt,
			CreatedAt:       row.CreatedAt,
			OfficialStates:  states,
		})
	}
	return items, total, nil
}
