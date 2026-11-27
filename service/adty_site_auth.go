package service

import (
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const adtySiteAuthWhitelistMaxDomains = 200

var (
	ErrAdtyMainSiteMailNotConfigured = common.NewMessage("Email sending is not configured. Configure SMTP or Resend first.")
	ErrAdtyCustomerMailNotConfigured = common.NewMessage("Email sending is not configured for this site. Please contact the site administrator.")
)

// AdtyCustomerMailError maps internal site/main mail-unconfigured sentinel
// errors to a neutral customer-facing message so end users on autonomous
// subsites never see subsite/admin transport details.
func AdtyCustomerMailError(err error) error {
	if errors.Is(err, ErrAdtySiteMailNotConfigured) || errors.Is(err, ErrAdtyMainSiteMailNotConfigured) {
		return ErrAdtyCustomerMailNotConfigured
	}
	return err
}

// AdtySiteAuthConfigView is returned to site owners when reading or updating
// per-subsite basic authentication settings.
type AdtySiteAuthConfigView struct {
	model.AdtySiteAuthConfig
	MailConfigured bool `json:"mail_configured"`
}

// DefaultAdtySiteAuthConfig returns the safe baseline basic authentication
// policy for subsites. Email verification defaults to off because subsites do
// not inherit the main site's mail transport (R-41 #8A).
func DefaultAdtySiteAuthConfig() model.AdtySiteAuthConfig {
	return model.AdtySiteAuthConfig{
		PasswordLoginEnabled:          true,
		RegisterEnabled:               true,
		PasswordRegisterEnabled:       true,
		EmailVerificationEnabled:      false,
		EmailDomainRestrictionEnabled: false,
		EmailAliasRestrictionEnabled:  false,
		EmailDomainWhitelist:          []string{},
	}
}

// AdtySiteAuthConfigFor parses a subsite's stored basic authentication config,
// falling back to safe defaults when unset or malformed. Lock-free and query-free
// so it can run inside AdtyOverrideStatus.
func AdtySiteAuthConfigFor(site *model.AdtySite) model.AdtySiteAuthConfig {
	cfg := DefaultAdtySiteAuthConfig()
	if site == nil || strings.TrimSpace(site.AuthConfig) == "" {
		return cfg
	}
	if err := common.UnmarshalJsonStr(site.AuthConfig, &cfg); err != nil {
		return DefaultAdtySiteAuthConfig()
	}
	cfg.EmailDomainWhitelist = cleanStoredEmailDomainWhitelist(cfg.EmailDomainWhitelist)
	return cfg
}

// AdtyEffectiveAuthSettingsForSite returns the subsite's auth settings when
// site != nil, or the main site's global auth settings when site == nil.
func AdtyEffectiveAuthSettingsForSite(site *model.AdtySite) model.AdtySiteAuthConfig {
	if site != nil {
		return AdtySiteAuthConfigFor(site)
	}
	common.OptionMapRWMutex.RLock()
	cfg := model.AdtySiteAuthConfig{
		PasswordLoginEnabled:          common.PasswordLoginEnabled,
		RegisterEnabled:               common.RegisterEnabled,
		PasswordRegisterEnabled:       common.PasswordRegisterEnabled,
		EmailVerificationEnabled:      common.EmailVerificationEnabled,
		EmailDomainRestrictionEnabled: common.EmailDomainRestrictionEnabled,
		EmailAliasRestrictionEnabled:  common.EmailAliasRestrictionEnabled,
		EmailDomainWhitelist:          append([]string{}, common.EmailDomainWhitelist...),
	}
	common.OptionMapRWMutex.RUnlock()
	if cfg.EmailDomainWhitelist == nil {
		cfg.EmailDomainWhitelist = []string{}
	}
	return cfg
}

// AdtyEffectiveAuthSettings resolves the active basic authentication settings
// for the current HTTP request (subsite-specific or main-site global).
func AdtyEffectiveAuthSettings(c *gin.Context) model.AdtySiteAuthConfig {
	return AdtyEffectiveAuthSettingsForSite(AdtySiteForRequest(c))
}

func cleanStoredEmailDomainWhitelist(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		for _, part := range strings.FieldsFunc(entry, func(r rune) bool {
			return r == ',' || r == '\n' || r == '\r'
		}) {
			domain := strings.ToLower(strings.TrimSpace(part))
			if domain != "" && !slices.Contains(out, domain) {
				out = append(out, domain)
			}
		}
	}
	return out
}

func normalizeAdtySiteEmailDomainWhitelist(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		for _, part := range strings.FieldsFunc(entry, func(r rune) bool {
			return r == ',' || r == '\n' || r == '\r'
		}) {
			domain := strings.ToLower(strings.TrimSpace(part))
			if domain == "" {
				continue
			}
			if !isValidEmailWhitelistDomain(domain) {
				return nil, common.NewMessage("Enter valid email domains in the whitelist, such as gmail.com")
			}
			if !slices.Contains(out, domain) {
				out = append(out, domain)
				if len(out) > adtySiteAuthWhitelistMaxDomains {
					return nil, common.NewMessage("At most 200 email domains are allowed")
				}
			}
		}
	}
	return out, nil
}

func isValidEmailWhitelistDomain(domain string) bool {
	if len(domain) == 0 || len(domain) > 253 || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || !strings.Contains(domain, ".") {
		return false
	}
	for _, r := range domain {
		if r > unicode.MaxASCII || unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
				return false
			}
		}
	}
	return true
}

// GetAdtySiteAuthConfig loads the owner's subsite basic authentication settings
// along with whether the subsite has outbound email sending configured.
func GetAdtySiteAuthConfig(ownerUserID int) (*AdtySiteAuthConfigView, error) {
	site, err := AdtyOwnerSite(ownerUserID)
	if err != nil {
		return nil, err
	}
	mailErr := EnsureAdtySiteMailConfigured(site)
	if mailErr != nil && !errors.Is(mailErr, ErrAdtySiteMailNotConfigured) {
		return nil, mailErr
	}
	return &AdtySiteAuthConfigView{
		AdtySiteAuthConfig: AdtySiteAuthConfigFor(site),
		MailConfigured:     mailErr == nil,
	}, nil
}

// SaveAdtySiteAuthConfig validates and persists a subsite's basic authentication
// configuration. Enabling email verification requires the subsite to have its own
// SMTP or Resend mail configuration saved first.
func SaveAdtySiteAuthConfig(ownerUserID int, input model.AdtySiteAuthConfig) (*AdtySiteAuthConfigView, error) {
	site, err := AdtyOwnerSite(ownerUserID)
	if err != nil {
		return nil, err
	}
	if site.Status != model.AdtySiteStatusActive && site.Status != model.AdtySiteStatusPaused {
		return nil, ErrAdtySiteClosing
	}
	whitelist, err := normalizeAdtySiteEmailDomainWhitelist(input.EmailDomainWhitelist)
	if err != nil {
		return nil, err
	}
	if input.EmailDomainRestrictionEnabled && len(whitelist) == 0 {
		return nil, common.NewMessage("Cannot enable the email domain restriction. Enter the allowed email domains first.")
	}
	nextCfg := model.AdtySiteAuthConfig{
		PasswordLoginEnabled:          input.PasswordLoginEnabled,
		RegisterEnabled:               input.RegisterEnabled,
		PasswordRegisterEnabled:       input.PasswordRegisterEnabled,
		EmailVerificationEnabled:      input.EmailVerificationEnabled,
		EmailDomainRestrictionEnabled: input.EmailDomainRestrictionEnabled,
		EmailAliasRestrictionEnabled:  input.EmailAliasRestrictionEnabled,
		EmailDomainWhitelist:          whitelist,
	}
	raw, err := common.Marshal(nextCfg)
	if err != nil {
		return nil, err
	}

	var mailConfigured bool
	var auditEvent *model.AuditLog
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var locked model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Where("id = ? AND owner_user_id = ?", site.Id, ownerUserID).First(&locked).Error; err != nil {
			return err
		}
		if locked.Status != model.AdtySiteStatusActive && locked.Status != model.AdtySiteStatusPaused {
			return ErrAdtySiteClosing
		}
		mailErr := EnsureAdtySiteMailConfiguredTx(tx, &locked)
		if mailErr != nil && !errors.Is(mailErr, ErrAdtySiteMailNotConfigured) {
			return mailErr
		}
		mailConfigured = mailErr == nil
		if nextCfg.EmailVerificationEnabled && !mailConfigured {
			return common.NewMessage("Configure your site's email sending (SMTP or Resend) before enabling email verification")
		}

		oldCfg := AdtySiteAuthConfigFor(&locked)
		oldRaw, _ := common.Marshal(oldCfg)
		if err := tx.Model(&locked).Update("auth_config", string(raw)).Error; err != nil {
			return err
		}
		now := time.Now().Unix()
		hasSiteAuditTable, err := model.AdtyTableExists(tx, &model.AdtySiteAudit{})
		if err != nil {
			return err
		}
		if hasSiteAuditTable {
			if err := tx.Create(&model.AdtySiteAudit{
				SiteId:     locked.Id,
				Action:     "auth_config_update",
				OperatorId: ownerUserID,
				OldValue:   string(oldRaw),
				NewValue:   string(raw),
				CreatedAt:  now,
			}).Error; err != nil {
				return err
			}
		}
		var auditErr error
		auditEvent, auditErr = appendAdtyOfficialAuditTx(
			tx,
			ownerUserID,
			locked.Id,
			"site_auth_config",
			strconv.Itoa(locked.Id),
			"adty.site.auth_config_update",
			"",
			"",
			oldCfg,
			nextCfg,
			model.AuditResultSuccess,
		)
		return auditErr
	})
	if err != nil {
		return nil, err
	}
	InvalidateAdtySiteCache()
	if auditEvent != nil {
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return &AdtySiteAuthConfigView{
		AdtySiteAuthConfig: nextCfg,
		MailConfigured:     mailConfigured,
	}, nil
}

func adtyMainSiteMailTransportOverridden() bool {
	if !adtyUsesDefaultDeliverSiteEmail() {
		return true
	}
	if !AdtySystemEmailAsyncDispatch {
		return true
	}
	if AdtySendEmailAsync != nil && reflect.ValueOf(AdtySendEmailAsync).Pointer() != reflect.ValueOf(defaultAdtySendEmailAsync).Pointer() {
		return true
	}
	if AdtySystemEmailSender != nil && reflect.ValueOf(AdtySystemEmailSender).Pointer() != reflect.ValueOf(defaultAdtySystemEmailSender).Pointer() {
		return true
	}
	return false
}

// EnsureMainSiteMailConfigured verifies that the main site has a real SMTP or
// Resend transport configured before acknowledging an async verification email.
func EnsureMainSiteMailConfigured() error {
	if adtyMainSiteMailTransportOverridden() {
		return nil
	}
	common.OptionMapRWMutex.RLock()
	provider := strings.ToLower(strings.TrimSpace(common.OptionMap["EmailProvider"]))
	resendKey := strings.TrimSpace(common.OptionMap["ResendApiKey"])
	resendFrom := strings.TrimSpace(common.OptionMap["ResendFrom"])
	smtpServer := strings.TrimSpace(common.SMTPServer)
	smtpFrom := strings.TrimSpace(common.SMTPFrom)
	smtpAccount := strings.TrimSpace(common.SMTPAccount)
	common.OptionMapRWMutex.RUnlock()

	switch provider {
	case "resend":
		if resendKey == "" || resendFrom == "" {
			return ErrAdtyMainSiteMailNotConfigured
		}
		return nil
	case "", "smtp":
		if smtpServer == "" || (smtpFrom == "" && smtpAccount == "") {
			return ErrAdtyMainSiteMailNotConfigured
		}
		return nil
	default:
		return common.NewMessage("Email sending method must be SMTP or Resend")
	}
}

// EnsureAdtyRequestMailConfigured guards email-sending endpoints so neither an
// unconfigured subsite nor an unconfigured main site can falsely claim an email
// was sent.
func EnsureAdtyRequestMailConfigured(site *model.AdtySite) error {
	if site != nil {
		return EnsureAdtySiteMailConfigured(site)
	}
	return EnsureMainSiteMailConfigured()
}
