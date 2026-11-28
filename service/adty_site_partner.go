package service

import (
	"crypto/hmac"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Partner referral (R-49): signed click attribution cookie, public partner lead
// intake, Root lead/binding management, hourly partner settlement, close hooks,
// and anonymized owner views.

const (
	AdtyPartnerRefCookie             = "adty_partner_ref"
	adtyPartnerCookieTTLDays         = 30
	adtyPartnerCookieDomainPrefix    = "partner:"
	adtyNotifyTypePartnerLead        = "adty_partner_lead"
	AdtyPartnerLateBindWindowSeconds = 7 * 86400
	AdtyPartnerDefaultRatePercent    = 5.0
)

var (
	ErrAdtyPartnerCodeInvalid = common.NewMessage("Invalid or inactive partner referral code")
	ErrAdtyPartnerLeadNotFound = common.NewMessage("Partner lead not found")
)

var adtyPartnerValidChannels = map[string]struct{}{
	"telegram": {},
	"wechat":   {},
	"email":    {},
	"other":    {},
}

// AdtyPartnerDefaultRateFloat returns the configured global partner referral
// default commission rate in percent (defaults to 5.0%).
func AdtyPartnerDefaultRateFloat() float64 {
	cfg := resellerSetting()
	if cfg != nil && cfg.PartnerReferralDefaultRate != nil {
		return *cfg.PartnerReferralDefaultRate
	}
	return AdtyPartnerDefaultRatePercent
}

func adtyPartnerDefaultRatePercent() decimal.Decimal {
	return decimal.NewFromFloat(AdtyPartnerDefaultRateFloat()).Round(6)
}

func validateAdtyPartnerRate(rate decimal.Decimal) error {
	if rate.IsNegative() || rate.GreaterThan(decimal.NewFromInt(50)) || !rate.Equal(rate.Truncate(6)) {
		return common.NewMessage("Partner referral rate must be between 0% and 50%")
	}
	return nil
}

// BuildAdtyPartnerCookie creates an HMAC-SHA256 signed partner referral cookie
// payload with a distinct "partner:" domain prefix so user referral cookies and
// partner referral cookies cannot cross-talk.
func BuildAdtyPartnerCookie(code string, issuedAt time.Time) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	secret := referralSecret()
	if code == "" || strings.ContainsAny(code, ".: \t\r\n") || secret == "" {
		return "", common.NewMessage("Unable to issue the partner referral cookie right now")
	}
	payload := fmt.Sprintf("%s%s.%d", adtyPartnerCookieDomainPrefix, code, issuedAt.Unix())
	sig := common.HmacSha256(payload, secret)
	return base64.StdEncoding.EncodeToString([]byte(payload + "." + sig)), nil
}

// ParseAdtyPartnerCookie verifies the HMAC signature, domain prefix, and 30-day
// TTL of a signed partner referral cookie and returns the normalized partner code.
func ParseAdtyPartnerCookie(raw string, now time.Time) (string, error) {
	secret := referralSecret()
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil || secret == "" {
		return "", ErrAdtyPartnerCodeInvalid
	}
	parts := strings.Split(string(decoded), ".")
	if len(parts) != 3 {
		return "", ErrAdtyPartnerCodeInvalid
	}
	if !strings.HasPrefix(parts[0], adtyPartnerCookieDomainPrefix) {
		return "", ErrAdtyPartnerCodeInvalid
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(common.HmacSha256(payload, secret)), []byte(parts[2])) {
		return "", ErrAdtyPartnerCodeInvalid
	}
	issued, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", ErrAdtyPartnerCodeInvalid
	}
	issuedAt := time.Unix(issued, 0)
	if issuedAt.After(now.Add(5*time.Minute)) || issuedAt.Add(time.Duration(adtyPartnerCookieTTLDays)*24*time.Hour).Before(now) {
		return "", ErrAdtyPartnerCodeInvalid
	}
	code := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(parts[0], adtyPartnerCookieDomainPrefix)))
	if code == "" || strings.ContainsAny(code, ".: \t\r\n") {
		return "", ErrAdtyPartnerCodeInvalid
	}
	return code, nil
}

// SetAdtyPartnerCookie sets the signed HttpOnly adty_partner_ref cookie (30d, SameSite=Lax).
func SetAdtyPartnerCookie(c *gin.Context, code string) error {
	value, err := BuildAdtyPartnerCookie(code, time.Now())
	if err != nil {
		return err
	}
	maxAge := adtyPartnerCookieTTLDays * 86400
	secure := adtyReferralCookieSecure(c)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(AdtyPartnerRefCookie, value, maxAge, "/", "", secure, true)
	return nil
}

// ClearAdtyPartnerCookie expires the adty_partner_ref cookie.
func ClearAdtyPartnerCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(AdtyPartnerRefCookie, "", -1, "/", "", adtyReferralCookieSecure(c), true)
}

// ReadAdtyPartnerCookieCode returns the verified partner code from the request cookie, or empty string.
func ReadAdtyPartnerCookieCode(c *gin.Context) string {
	if c == nil {
		return ""
	}
	raw, err := c.Cookie(AdtyPartnerRefCookie)
	if err != nil || strings.TrimSpace(raw) == "" {
		return ""
	}
	code, err := ParseAdtyPartnerCookie(raw, time.Now())
	if err != nil {
		return ""
	}
	return code
}

func validateActivePartnerCodeTx(tx *gorm.DB, rawCode string) (*model.AdtySite, error) {
	if tx == nil {
		tx = model.DB
	}
	normalized := strings.ToUpper(strings.TrimSpace(rawCode))
	if normalized == "" || len(normalized) > 24 {
		return nil, ErrAdtyPartnerCodeInvalid
	}
	var site model.AdtySite
	res := tx.Where("partner_code = ?", normalized).Limit(1).Find(&site)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		res = tx.Where("UPPER(partner_code) = ?", normalized).Limit(1).Find(&site)
		if res.Error != nil {
			return nil, res.Error
		}
	}
	if res.RowsAffected == 0 || site.Id == 0 || site.Status != model.AdtySiteStatusActive {
		return nil, ErrAdtyPartnerCodeInvalid
	}
	return &site, nil
}

// ValidateActivePartnerCode resolves a partner referral code only when the owning
// site is currently active. Entering closing immediately invalidates the link.
func ValidateActivePartnerCode(code string) (*model.AdtySite, error) {
	return validateActivePartnerCodeTx(model.DB, code)
}

type AdtyPartnerLeadInput struct {
	ContactName     string `json:"contact_name"`
	ContactChannel  string `json:"contact_channel"`
	ContactValue    string `json:"contact_value"`
	EstimatedVolume string `json:"estimated_volume"`
	Note            string `json:"note"`
	ReferralCode    string `json:"referral_code"`
}

// SubmitAdtyPartnerLead validates and stores a prospective subsite partner inquiry,
// resolves optional referral attribution, and asynchronously alerts Root admins.
func SubmitAdtyPartnerLead(input AdtyPartnerLeadInput, ip, cookieCode string) (*model.AdtyPartnerLead, string, error) {
	contactName := strings.TrimSpace(input.ContactName)
	if n := utf8.RuneCountInString(contactName); n < 1 || n > 100 {
		return nil, "", common.NewMessage("Contact name must be between 1 and 100 characters")
	}
	contactChannel := strings.ToLower(strings.TrimSpace(input.ContactChannel))
	if _, ok := adtyPartnerValidChannels[contactChannel]; !ok {
		return nil, "", common.NewMessage("Invalid contact channel")
	}
	contactValue := strings.TrimSpace(input.ContactValue)
	if n := utf8.RuneCountInString(contactValue); n < 1 || n > 200 {
		return nil, "", common.NewMessage("Contact value must be between 1 and 200 characters")
	}
	estimatedVolume := strings.TrimSpace(input.EstimatedVolume)
	if utf8.RuneCountInString(estimatedVolume) > 128 {
		return nil, "", common.NewMessage("Estimated volume must be at most 128 characters")
	}
	note := strings.TrimSpace(input.Note)
	if utf8.RuneCountInString(note) > 1000 {
		return nil, "", common.NewMessage("Note must be at most 1000 characters")
	}

	var referrerSite *model.AdtySite
	resolvedCode := ""
	explicitCode := strings.ToUpper(strings.TrimSpace(input.ReferralCode))
	if explicitCode != "" {
		site, err := ValidateActivePartnerCode(explicitCode)
		if err != nil {
			if errors.Is(err, ErrAdtyPartnerCodeInvalid) {
				return nil, "", ErrAdtyPartnerCodeInvalid
			}
			return nil, "", err
		}
		referrerSite = site
		resolvedCode = explicitCode
		if site.PartnerCode != nil && strings.TrimSpace(*site.PartnerCode) != "" {
			resolvedCode = strings.TrimSpace(*site.PartnerCode)
		}
	} else if trimmedCookie := strings.ToUpper(strings.TrimSpace(cookieCode)); trimmedCookie != "" {
		site, err := ValidateActivePartnerCode(trimmedCookie)
		if err == nil && site != nil {
			referrerSite = site
			resolvedCode = trimmedCookie
			if site.PartnerCode != nil && strings.TrimSpace(*site.PartnerCode) != "" {
				resolvedCode = strings.TrimSpace(*site.PartnerCode)
			}
		} else if err != nil && !errors.Is(err, ErrAdtyPartnerCodeInvalid) {
			return nil, "", err
		}
	}

	referrerSiteID := 0
	if referrerSite != nil {
		referrerSiteID = referrerSite.Id
	}
	lead := &model.AdtyPartnerLead{
		ReferrerSiteId:  referrerSiteID,
		ReferralCode:    resolvedCode,
		ContactName:     contactName,
		ContactChannel:  contactChannel,
		ContactValue:    contactValue,
		EstimatedVolume: estimatedVolume,
		Note:            note,
		Status:          model.AdtyPartnerLeadStatusPending,
		IpHash:          HashReferralRiskValue(ip),
	}
	if err := model.DB.Create(lead).Error; err != nil {
		return nil, "", err
	}

	leadCopy := *lead
	var refCopy *model.AdtySite
	if referrerSite != nil {
		cp := *referrerSite
		refCopy = &cp
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				common.SysError(common.LogText("partner lead notify panic lead=%d: %v", leadCopy.Id, r))
			}
		}()
		notifyRootNewPartnerLead(&leadCopy, refCopy)
		queueRootPartnerLeadTelegramNotices(&leadCopy, refCopy)
	}()

	return lead, strings.TrimSpace(resellerSetting().PartnerContactTelegram), nil
}

func formatPartnerLeadSource(lead *model.AdtyPartnerLead, referrerSite *model.AdtySite) (zh, en string) {
	if referrerSite != nil && referrerSite.Id > 0 {
		code := strings.TrimSpace(lead.ReferralCode)
		if code == "" && referrerSite.PartnerCode != nil {
			code = strings.TrimSpace(*referrerSite.PartnerCode)
		}
		zh = fmt.Sprintf("站长推荐：分站 #%d (%s) [%s]", referrerSite.Id, referrerSite.Subdomain, code)
		en = fmt.Sprintf("Partner referral: Site #%d (%s) [%s]", referrerSite.Id, referrerSite.Subdomain, code)
		return zh, en
	}
	if lead.ReferrerSiteId > 0 {
		zh = fmt.Sprintf("站长推荐：分站 #%d [%s]", lead.ReferrerSiteId, strings.TrimSpace(lead.ReferralCode))
		en = fmt.Sprintf("Partner referral: Site #%d [%s]", lead.ReferrerSiteId, strings.TrimSpace(lead.ReferralCode))
		return zh, en
	}
	return "官网直客申请", "Direct main-site inquiry"
}

func notifyRootNewPartnerLead(lead *model.AdtyPartnerLead, referrerSite *model.AdtySite) {
	if lead == nil || lead.Id <= 0 {
		return
	}
	sourceZh, sourceEn := formatPartnerLeadSource(lead, referrerSite)
	safeName := redactAdtySecrets(lead.ContactName)
	safeValue := redactAdtySecrets(lead.ContactValue)
	safeVol := redactAdtySecrets(lead.EstimatedVolume)
	safeNote := redactAdtySecrets(lead.Note)

	eventID := fmt.Sprintf("partner_lead:%d", lead.Id)
	notifyAdminsWithEventID(eventID, adtyNotifyTypePartnerLead, func(_ int, role int, _ dto.UserSetting) bool {
		return role == common.RoleRootUser
	}, func(lang string) (string, string) {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "zh") {
			var b strings.Builder
			fmt.Fprintf(&b, "收到新的分站合作意向申请 #%d。\n", lead.Id)
			fmt.Fprintf(&b, "联系人：%s\n", safeName)
			fmt.Fprintf(&b, "联系方式：%s (%s)\n", lead.ContactChannel, safeValue)
			fmt.Fprintf(&b, "来源：%s", sourceZh)
			if safeVol != "" {
				fmt.Fprintf(&b, "\n预计规模：%s", safeVol)
			}
			if safeNote != "" {
				fmt.Fprintf(&b, "\n备注：%s", safeNote)
			}
			return fmt.Sprintf("新的分站合作意向 #%d", lead.Id), b.String()
		}
		var b strings.Builder
		fmt.Fprintf(&b, "A new reseller partner lead #%d has been submitted.\n", lead.Id)
		fmt.Fprintf(&b, "Contact: %s\n", safeName)
		fmt.Fprintf(&b, "Channel: %s (%s)\n", lead.ContactChannel, safeValue)
		fmt.Fprintf(&b, "Source: %s", sourceEn)
		if safeVol != "" {
			fmt.Fprintf(&b, "\nEstimated volume: %s", safeVol)
		}
		if safeNote != "" {
			fmt.Fprintf(&b, "\nNote: %s", safeNote)
		}
		return fmt.Sprintf("New reseller partner lead #%d", lead.Id), b.String()
	})
}

func queueRootPartnerLeadTelegramNotices(lead *model.AdtyPartnerLead, referrerSite *model.AdtySite) {
	if lead == nil || lead.Id <= 0 || model.DB == nil {
		return
	}
	var rootUsers []model.User
	if err := model.DB.Select("id").
		Where("site_id = 0 AND staff_site_id = 0 AND status = ? AND role >= ?", common.UserStatusEnabled, common.RoleRootUser).
		Order("id ASC").
		Find(&rootUsers).Error; err != nil {
		common.SysError(common.LogText("query root users for partner lead %d telegram notice failed: %s", lead.Id, err.Error()))
		return
	}
	if len(rootUsers) == 0 {
		return
	}
	sourceZh, sourceEn := formatPartnerLeadSource(lead, referrerSite)
	safeName := redactAdtySecrets(lead.ContactName)
	safeValue := redactAdtySecrets(lead.ContactValue)
	safeVol := redactAdtySecrets(lead.EstimatedVolume)
	safeNote := redactAdtySecrets(lead.Note)

	var zhBuf, enBuf strings.Builder
	fmt.Fprintf(&zhBuf, "[分站合作意向 #%d]\n联系人：%s\n联系方式：%s: %s\n来源：%s",
		lead.Id, safeName, lead.ContactChannel, safeValue, sourceZh)
	if safeVol != "" {
		fmt.Fprintf(&zhBuf, "\n预计规模：%s", safeVol)
	}
	if safeNote != "" {
		fmt.Fprintf(&zhBuf, "\n备注：%s", safeNote)
	}

	fmt.Fprintf(&enBuf, "[Reseller Partner Lead #%d]\nContact: %s\nChannel: %s: %s\nSource: %s",
		lead.Id, safeName, lead.ContactChannel, safeValue, sourceEn)
	if safeVol != "" {
		fmt.Fprintf(&enBuf, "\nEstimated volume: %s", safeVol)
	}
	if safeNote != "" {
		fmt.Fprintf(&enBuf, "\nNote: %s", safeNote)
	}

	bilingualMsg := zhBuf.String() + "\n\n" + enBuf.String()
	for _, rootUser := range rootUsers {
		eventKey := fmt.Sprintf("partner-lead:%d:u%d", lead.Id, rootUser.Id)
		if err := QueueAdtyTelegramNotice(rootUser.Id, model.AdtyTelegramPurposeAdmin, bilingualMsg, eventKey); err != nil {
			common.SysLog(common.LogText("queue partner lead telegram notice lead=%d user=%d failed: %s", lead.Id, rootUser.Id, err.Error()))
		}
	}
}

type AdtyPartnerLeadAdminItem struct {
	model.AdtyPartnerLead
	ReferrerSiteSubdomain string `json:"referrer_site_subdomain,omitempty"`
	ReferrerSiteName      string `json:"referrer_site_name,omitempty"`
	OpenedSiteSubdomain   string `json:"opened_site_subdomain,omitempty"`
	ReviewedByUsername    string `json:"reviewed_by_username,omitempty"`
}

// ListAdtyPartnerLeads returns paginated partner leads enriched with site metadata for Root review.
func ListAdtyPartnerLeads(status string, page, pageSize int) ([]AdtyPartnerLeadAdminItem, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	status = strings.TrimSpace(status)
	query := model.DB.Model(&model.AdtyPartnerLead{})
	if status != "" && status != "all" {
		switch status {
		case model.AdtyPartnerLeadStatusPending, model.AdtyPartnerLeadStatusApproved, model.AdtyPartnerLeadStatusRejected:
			query = query.Where("status = ?", status)
		default:
			return nil, 0, common.NewMessage("Invalid parameters")
		}
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []AdtyPartnerLeadAdminItem{}, 0, nil
	}
	var rows []model.AdtyPartnerLead
	if err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	siteIDSet := map[int]struct{}{}
	reviewerIDs := make([]int, 0, len(rows))
	for _, r := range rows {
		if r.ReferrerSiteId > 0 {
			siteIDSet[r.ReferrerSiteId] = struct{}{}
		}
		if r.OpenedSiteId > 0 {
			siteIDSet[r.OpenedSiteId] = struct{}{}
		}
		if r.ReviewedBy > 0 {
			reviewerIDs = append(reviewerIDs, r.ReviewedBy)
		}
	}
	sitesByID := map[int]model.AdtySite{}
	if len(siteIDSet) > 0 {
		siteIDs := make([]int, 0, len(siteIDSet))
		for id := range siteIDSet {
			siteIDs = append(siteIDs, id)
		}
		var sites []model.AdtySite
		if err := model.DB.Select("id", "subdomain", "root_domain", "brand").Where("id IN ?", siteIDs).Find(&sites).Error; err != nil {
			return nil, 0, err
		}
		for _, s := range sites {
			sitesByID[s.Id] = s
		}
	}
	reviewerNames := model.LookupUsernamesByIDs(model.DB, reviewerIDs)
	items := make([]AdtyPartnerLeadAdminItem, len(rows))
	for i, r := range rows {
		item := AdtyPartnerLeadAdminItem{
			AdtyPartnerLead:    r,
			ReviewedByUsername: reviewerNames[r.ReviewedBy],
		}
		if ref, ok := sitesByID[r.ReferrerSiteId]; ok {
			item.ReferrerSiteSubdomain = ref.Subdomain
			item.ReferrerSiteName = AdtyBrandFor(&ref).Name
		}
		if opened, ok := sitesByID[r.OpenedSiteId]; ok {
			item.OpenedSiteSubdomain = opened.Subdomain
		}
		items[i] = item
	}
	return items, total, nil
}

// RejectAdtyPartnerLead rejects a pending partner lead and records an admin audit entry.
func RejectAdtyPartnerLead(leadID int64, operatorID int, reviewNote string) (*model.AdtyPartnerLead, error) {
	lead, _, err := RejectAdtyPartnerLeadWithAudit(leadID, operatorID, reviewNote)
	return lead, err
}

func RejectAdtyPartnerLeadWithAudit(leadID int64, operatorID int, reviewNote string) (*model.AdtyPartnerLead, string, error) {
	if leadID <= 0 || operatorID <= 0 {
		return nil, "", common.NewMessage("Invalid parameters")
	}
	note := truncateText(strings.TrimSpace(reviewNote), 1000)
	now := time.Now().Unix()
	var lead model.AdtyPartnerLead
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", leadID).First(&lead).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtyPartnerLeadNotFound
			}
			return err
		}
		if lead.Status != model.AdtyPartnerLeadStatusPending {
			return common.NewMessage("Partner lead has already been processed")
		}
		oldStatus := lead.Status
		updates := map[string]any{
			"status":      model.AdtyPartnerLeadStatusRejected,
			"reviewed_by": operatorID,
			"reviewed_at": now,
			"review_note": note,
		}
		if err := tx.Model(&model.AdtyPartnerLead{}).Where("id = ?", lead.Id).Updates(updates).Error; err != nil {
			return err
		}
		lead.Status = model.AdtyPartnerLeadStatusRejected
		lead.ReviewedBy = operatorID
		lead.ReviewedAt = now
		lead.ReviewNote = note
		var auditErr error
		auditEvent, auditErr = appendAdtyOfficialAuditTx(
			tx, operatorID, 0, "partner_lead", strconv.FormatInt(lead.Id, 10),
			"adty.partner_lead.reject", note, "",
			map[string]any{"status": oldStatus},
			map[string]any{"status": lead.Status, "review_note": note},
			model.AuditResultSuccess,
		)
		return auditErr
	})
	if err != nil {
		return nil, "", err
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return &lead, eventID, nil
}

type AdtySitePartnerManageInput struct {
	BindReferrerSiteId int              `json:"bind_referrer_site_id"`
	BindReferrerCode   string           `json:"bind_referrer_code"`
	RatePercent        *decimal.Decimal `json:"rate_percent"`
	Stop               bool             `json:"stop"`
	Reason             string           `json:"reason"`
}

// ManageAdtySitePartnerReferral executes Root-only partner referral operations
// on an existing site: 7-day late bind, rate adjustment, or stop referral.
func ManageAdtySitePartnerReferral(siteID, operatorID int, input AdtySitePartnerManageInput) (*model.AdtySite, error) {
	site, _, err := ManageAdtySitePartnerReferralWithAudit(siteID, operatorID, input)
	return site, err
}

func ManageAdtySitePartnerReferralWithAudit(siteID, operatorID int, input AdtySitePartnerManageInput) (*model.AdtySite, string, error) {
	if siteID <= 0 || operatorID <= 0 {
		return nil, "", common.NewMessage("Invalid parameters")
	}
	bindCode := strings.ToUpper(strings.TrimSpace(input.BindReferrerCode))
	hasBind := input.BindReferrerSiteId > 0 || bindCode != ""
	hasRate := input.RatePercent != nil
	hasStop := input.Stop
	if !hasBind && !hasRate && !hasStop {
		return nil, "", common.NewMessage("Invalid parameters")
	}
	if hasStop && (hasBind || hasRate) {
		return nil, "", common.NewMessage("Invalid parameters")
	}
	if hasRate {
		if err := validateAdtyPartnerRate(*input.RatePercent); err != nil {
			return nil, "", err
		}
	}
	reason := truncateText(strings.TrimSpace(input.Reason), 1000)
	if hasStop && reason == "" {
		return nil, "", common.NewMessage("A reason is required to stop partner referral")
	}

	now := time.Now().Unix()
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if hasBind {
			targetRefID := input.BindReferrerSiteId
			if bindCode != "" {
				foundRef, refErr := validateActivePartnerCodeTx(tx, bindCode)
				if refErr != nil {
					return refErr
				}
				if targetRefID > 0 && targetRefID != foundRef.Id {
					return common.NewMessage("Referrer site ID and partner code do not match")
				}
				targetRefID = foundRef.Id
			}
			if targetRefID <= 0 {
				return ErrAdtyPartnerCodeInvalid
			}
			if targetRefID == siteID {
				return common.NewMessage("A site cannot refer itself")
			}
			lockIDs := []int{siteID, targetRefID}
			slices.Sort(lockIDs)
			var site, refSite *model.AdtySite
			for _, id := range lockIDs {
				var row model.AdtySite
				res := model.AdtyLockForUpdate(tx).Where("id = ?", id).Limit(1).Find(&row)
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected == 0 {
					continue
				}
				cp := row
				if id == siteID {
					site = &cp
				}
				if id == targetRefID {
					refSite = &cp
				}
			}
			if site == nil {
				return ErrAdtySiteNotFound
			}
			if refSite == nil || refSite.Status != model.AdtySiteStatusActive {
				return common.NewMessage("Referrer site must be active")
			}
			if bindCode != "" && (refSite.PartnerCode == nil || !strings.EqualFold(strings.TrimSpace(*refSite.PartnerCode), bindCode)) {
				return ErrAdtyPartnerCodeInvalid
			}
			if site.Status != model.AdtySiteStatusActive {
				return common.NewMessage("Only active sites can be late-bound to a partner referrer")
			}
			if site.ReferrerSiteId > 0 || site.ReferrerLateBound {
				return common.NewMessage("Site already has a partner referrer or has already been late-bound")
			}
			if now-site.CreatedAt > AdtyPartnerLateBindWindowSeconds {
				return common.NewMessage("Late binding is only allowed within 7 days of site creation")
			}
			rate := adtyPartnerDefaultRatePercent()
			if input.RatePercent != nil {
				rate = *input.RatePercent
			}
			oldSnap := map[string]any{
				"referrer_site_id":    site.ReferrerSiteId,
				"referrer_rate":       site.ReferrerRate,
				"referrer_bound_at":   site.ReferrerBoundAt,
				"referrer_late_bound": site.ReferrerLateBound,
			}
			updates := map[string]any{
				"referrer_site_id":    refSite.Id,
				"referrer_rate":       rate,
				"referrer_bound_at":   now,
				"referrer_late_bound": true,
			}
			if err := tx.Model(&model.AdtySite{}).Where("id = ?", site.Id).Updates(updates).Error; err != nil {
				return err
			}
			var auditErr error
			auditEvent, auditErr = recordSiteAuditWithIntentTx(tx, site.Id, "partner_bind", operatorID, oldSnap, updates, reason)
			return auditErr
		}

		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteID).First(&site).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		if site.ReferrerSiteId <= 0 {
			return common.NewMessage("Site has no partner referrer")
		}
		if hasStop {
			if site.ReferrerStoppedAt > 0 {
				return common.NewMessage("Partner referral is already stopped")
			}
			oldSnap := map[string]any{
				"referrer_site_id":     site.ReferrerSiteId,
				"referrer_stopped_at":  site.ReferrerStoppedAt,
				"referrer_stop_reason": site.ReferrerStopReason,
			}
			updates := map[string]any{
				"referrer_stopped_at":  now,
				"referrer_stop_reason": reason,
			}
			if err := tx.Model(&model.AdtySite{}).Where("id = ?", site.Id).Updates(updates).Error; err != nil {
				return err
			}
			var auditErr error
			auditEvent, auditErr = recordSiteAuditWithIntentTx(tx, site.Id, "partner_stop", operatorID, oldSnap, updates, reason)
			return auditErr
		}

		// Update rate
		if site.ReferrerStoppedAt > 0 {
			return common.NewMessage("Partner referral is already stopped")
		}
		if site.Status == model.AdtySiteStatusClosed || site.Status == model.AdtySiteStatusDeleting {
			return ErrAdtySiteStatus
		}
		oldSnap := map[string]any{
			"referrer_site_id": site.ReferrerSiteId,
			"referrer_rate":    site.ReferrerRate,
		}
		updates := map[string]any{
			"referrer_rate": *input.RatePercent,
		}
		if err := tx.Model(&model.AdtySite{}).Where("id = ?", site.Id).Updates(updates).Error; err != nil {
			return err
		}
		var auditErr error
		auditEvent, auditErr = recordSiteAuditWithIntentTx(tx, site.Id, "partner_rate", operatorID, oldSnap, updates, reason)
		return auditErr
	})
	if err != nil {
		return nil, "", err
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	InvalidateAdtySiteCache()
	updated, err := GetAdtySite(siteID)
	return updated, eventID, err
}

// settleSitePartnerHourForSite settles one hourly partner referral payout for
// (referrerSiteID, referredSiteID, hourSlot). Locks adty_sites in ascending ID
// order to preserve canonical lock ordering.
func settleSitePartnerHourForSite(referrerSiteID, referredSiteID int, hourSlot string, supplyNetUSD decimal.Decimal, now int64) (bool, error) {
	hourSlot = strings.TrimSpace(hourSlot)
	if referrerSiteID <= 0 || referredSiteID <= 0 || referrerSiteID == referredSiteID || hourSlot == "" || supplyNetUSD.IsZero() {
		return false, nil
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	settled := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		firstID, secondID := referrerSiteID, referredSiteID
		if firstID > secondID {
			firstID, secondID = secondID, firstID
		}
		var referrerSite, referredSite *model.AdtySite
		for _, id := range []int{firstID, secondID} {
			var row model.AdtySite
			res := model.AdtyLockForUpdate(tx).Where("id = ?", id).Limit(1).Find(&row)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				continue
			}
			cp := row
			if id == referrerSiteID {
				referrerSite = &cp
			}
			if id == referredSiteID {
				referredSite = &cp
			}
		}
		if referrerSite == nil || referredSite == nil {
			return nil
		}
		if referredSite.ReferrerSiteId != referrerSiteID || referredSite.ReferrerStoppedAt > 0 {
			return nil
		}
		if referrerSite.Status == model.AdtySiteStatusClosed || referrerSite.Status == model.AdtySiteStatusDeleting {
			return nil
		}

		var existing model.AdtySitePartnerEarning
		res := tx.Where("hour_slot = ? AND referrer_site_id = ? AND referred_site_id = ?", hourSlot, referrerSiteID, referredSiteID).Limit(1).Find(&existing)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			return nil
		}

		ratePercent := adtyPartnerDefaultRatePercent()
		if referredSite.ReferrerRate != nil {
			ratePercent = *referredSite.ReferrerRate
		}
		if !ratePercent.IsPositive() {
			return nil
		}
		grossUSD := supplyNetUSD.Mul(ratePercent).Div(decimal.NewFromInt(100)).Round(8)
		if grossUSD.IsZero() {
			return nil
		}

		ownerKey := fmt.Sprintf("partner:%d:%d", referrerSiteID, referredSiteID)
		effective, offsetApplied, err := applyAdtyCommissionOffsetTx(tx, model.AdtyCommissionOffsetScopeSitePartner, ownerKey, grossUSD)
		if err != nil {
			return err
		}
		effective = effective.Round(8)
		offsetApplied = offsetApplied.Round(8)

		credited := decimal.Zero
		if effective.IsPositive() {
			if _, err := applyAdtySiteWalletChangeTx(tx, referrerSiteID, model.AdtySiteWalletEarnings, effective, AdtySiteWalletChange{
				Type:           "site_referral_earning",
				RefType:        "site_partner",
				RefID:          fmt.Sprintf("%d:%s", referredSiteID, hourSlot),
				Reason:         fmt.Sprintf("Partner referral commission (%s)", hourSlot),
				IdempotencyKey: fmt.Sprintf("site-partner:%d:%d:%s", referrerSiteID, referredSiteID, hourSlot),
				AllowOverdraft: true,
			}); err != nil {
				return err
			}
			credited = effective
		}

		earning := model.AdtySitePartnerEarning{
			HourSlot:           hourSlot,
			ReferrerSiteId:     referrerSiteID,
			ReferredSiteId:     referredSiteID,
			SupplyNetUSD:       supplyNetUSD.Round(8),
			RatePercent:        ratePercent,
			GrossCommissionUSD: grossUSD,
			OffsetAppliedUSD:   offsetApplied,
			CreditedUSD:        credited,
			SettledAt:          now,
		}
		if err := tx.Create(&earning).Error; err != nil {
			return err
		}
		settled = true
		return nil
	})
	return settled, err
}

// StopAdtySitePartnerReferralsOnCloseTx stops partner referral payouts involving
// closedSiteID when it completes closure, without disrupting any referred child sites.
func StopAdtySitePartnerReferralsOnCloseTx(tx *gorm.DB, closedSiteID int, now int64) error {
	if tx == nil || closedSiteID <= 0 {
		return nil
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	if err := tx.Model(&model.AdtySite{}).
		Where("id = ? AND referrer_site_id > 0 AND referrer_stopped_at = 0", closedSiteID).
		Updates(map[string]any{
			"referrer_stopped_at":  now,
			"referrer_stop_reason": "referred_site_closed",
		}).Error; err != nil {
		return err
	}
	return tx.Model(&model.AdtySite{}).
		Where("referrer_site_id = ? AND referrer_stopped_at = 0", closedSiteID).
		Updates(map[string]any{
			"referrer_stopped_at":  now,
			"referrer_stop_reason": "referrer_site_closed",
		}).Error
}

type AdtyOwnerPartnerOverview struct {
	PartnerCode        string          `json:"partner_code"`
	ShareURL           string          `json:"share_url"`
	SiteStatus         string          `json:"site_status"`
	LinkActive         bool            `json:"link_active"`
	DefaultRatePercent float64         `json:"default_rate_percent"`
	ReferredCount      int64           `json:"referred_count"`
	TotalEarnedUSD     decimal.Decimal `json:"total_earned_usd"`
	PendingHourUSD     decimal.Decimal `json:"pending_hour_usd"`
}

// GetAdtyOwnerPartnerOverview returns partner referral summary for a site owner
// without disclosing any sensitive information about referred subsites.
func GetAdtyOwnerPartnerOverview(site *model.AdtySite) (*AdtyOwnerPartnerOverview, error) {
	if site == nil || site.Id <= 0 {
		return nil, ErrAdtySiteNotFound
	}
	partnerCode := ""
	if site.PartnerCode != nil {
		partnerCode = strings.TrimSpace(*site.PartnerCode)
	}
	shareURL := ""
	if partnerCode != "" {
		base := strings.TrimRight(strings.TrimSpace(system_setting.GetServerAddress()), "/")
		if base != "" {
			shareURL = base + "/partner?ref=" + partnerCode
		} else {
			shareURL = "/partner?ref=" + partnerCode
		}
	}
	var referredCount int64
	if err := model.DB.Model(&model.AdtySite{}).Where("referrer_site_id = ?", site.Id).Count(&referredCount).Error; err != nil {
		return nil, err
	}
	totalEarnedUSD := decimal.Zero
	hasEarningTable, err := model.AdtyTableExists(model.DB, &model.AdtySitePartnerEarning{})
	if err != nil {
		return nil, err
	}
	if hasEarningTable {
		if err := model.DB.Model(&model.AdtySitePartnerEarning{}).
			Select("COALESCE(SUM(credited_usd), 0)").
			Where("referrer_site_id = ?", site.Id).
			Scan(&totalEarnedUSD).Error; err != nil {
			return nil, err
		}
	}
	pendingHourUSD, _, err := GetAdtySitePartnerPendingHourUSD(site.Id)
	if err != nil {
		return nil, err
	}
	return &AdtyOwnerPartnerOverview{
		PartnerCode:        partnerCode,
		ShareURL:           shareURL,
		SiteStatus:         site.Status,
		LinkActive:         site.Status == model.AdtySiteStatusActive && partnerCode != "",
		DefaultRatePercent: AdtyPartnerDefaultRateFloat(),
		ReferredCount:      referredCount,
		TotalEarnedUSD:     totalEarnedUSD,
		PendingHourUSD:     pendingHourUSD,
	}, nil
}

type AdtyOwnerPartnerSiteItem struct {
	Id             int             `json:"id"`
	MaskedName     string          `json:"masked_name"`
	Status         string          `json:"status"`
	RatePercent    decimal.Decimal `json:"rate_percent"`
	OpenedAt       int64           `json:"opened_at"`
	StoppedAt      int64           `json:"stopped_at"`
	PendingHourUSD decimal.Decimal `json:"pending_hour_usd"`
	TotalEarnedUSD decimal.Decimal `json:"total_earned_usd"`
}

// ListAdtyOwnerPartnerSites returns a strictly anonymized paginated list of
// sites referred by referrerSiteID (never leaking domains, customers, markup, or orders).
func ListAdtyOwnerPartnerSites(referrerSiteID int, page, pageSize int) ([]AdtyOwnerPartnerSiteItem, int64, error) {
	if referrerSiteID <= 0 {
		return []AdtyOwnerPartnerSiteItem{}, 0, nil
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	query := model.DB.Model(&model.AdtySite{}).Where("referrer_site_id = ?", referrerSiteID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []AdtyOwnerPartnerSiteItem{}, 0, nil
	}
	var rows []struct {
		Id                int
		Status            string
		ReferrerRate      *decimal.Decimal
		CreatedAt         int64
		ReferrerStoppedAt int64
	}
	if err := query.Select("id", "status", "referrer_rate", "created_at", "referrer_stopped_at").
		Order("id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	referredIDs := make([]int, len(rows))
	for i, r := range rows {
		referredIDs[i] = r.Id
	}
	earnedBySite := map[int]decimal.Decimal{}
	hasEarningTable, err := model.AdtyTableExists(model.DB, &model.AdtySitePartnerEarning{})
	if err != nil {
		return nil, 0, err
	}
	if hasEarningTable && len(referredIDs) > 0 {
		var sums []struct {
			ReferredSiteId int
			Total          decimal.Decimal
		}
		if err := model.DB.Model(&model.AdtySitePartnerEarning{}).
			Select("referred_site_id, COALESCE(SUM(credited_usd), 0) AS total").
			Where("referrer_site_id = ? AND referred_site_id IN ?", referrerSiteID, referredIDs).
			Group("referred_site_id").
			Scan(&sums).Error; err != nil {
			return nil, 0, err
		}
		for _, s := range sums {
			earnedBySite[s.ReferredSiteId] = s.Total
		}
	}
	_, pendingBySite, err := GetAdtySitePartnerPendingHourUSD(referrerSiteID)
	if err != nil {
		return nil, 0, err
	}
	defaultRate := adtyPartnerDefaultRatePercent()
	items := make([]AdtyOwnerPartnerSiteItem, len(rows))
	for i, r := range rows {
		rate := defaultRate
		if r.ReferrerRate != nil {
			rate = *r.ReferrerRate
		}
		items[i] = AdtyOwnerPartnerSiteItem{
			Id:             r.Id,
			MaskedName:     fmt.Sprintf("Site #%d", r.Id),
			Status:         r.Status,
			RatePercent:    rate,
			OpenedAt:       r.CreatedAt,
			StoppedAt:      r.ReferrerStoppedAt,
			PendingHourUSD: pendingBySite[r.Id],
			TotalEarnedUSD: earnedBySite[r.Id],
		}
	}
	return items, total, nil
}

type AdtyOwnerPartnerEarningItem struct {
	model.AdtySitePartnerEarning
	MaskedName string `json:"masked_name"`
}

// ListAdtyOwnerPartnerEarnings returns paginated anonymized hourly partner
// referral settlement rows for referrerSiteID.
func ListAdtyOwnerPartnerEarnings(referrerSiteID int, page, pageSize int) ([]AdtyOwnerPartnerEarningItem, int64, error) {
	if referrerSiteID <= 0 {
		return []AdtyOwnerPartnerEarningItem{}, 0, nil
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	hasEarningTable, err := model.AdtyTableExists(model.DB, &model.AdtySitePartnerEarning{})
	if err != nil {
		return nil, 0, err
	}
	if !hasEarningTable {
		return []AdtyOwnerPartnerEarningItem{}, 0, nil
	}
	query := model.DB.Model(&model.AdtySitePartnerEarning{}).Where("referrer_site_id = ?", referrerSiteID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []AdtyOwnerPartnerEarningItem{}, 0, nil
	}
	var rows []model.AdtySitePartnerEarning
	if err := query.Order("settled_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]AdtyOwnerPartnerEarningItem, len(rows))
	for i, r := range rows {
		items[i] = AdtyOwnerPartnerEarningItem{
			AdtySitePartnerEarning: r,
			MaskedName:             fmt.Sprintf("Site #%d", r.ReferredSiteId),
		}
	}
	return items, total, nil
}
