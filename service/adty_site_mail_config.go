package service

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/textproto"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// AdtySiteMailConfigView is returned to site owners by GET/PUT /api/adty/site/mail-config.
// Plaintext secrets are never serialized here.
type AdtySiteMailConfigView struct {
	Provider               string `json:"provider"`
	SenderName             string `json:"sender_name"`
	SMTPServer             string `json:"smtp_server"`
	SMTPPort               int    `json:"smtp_port"`
	SMTPAccount            string `json:"smtp_account"`
	SMTPFrom               string `json:"smtp_from"`
	SMTPSenderName         string `json:"smtp_sender_name"`
	SMTPTokenConfigured    bool   `json:"smtp_token_configured"`
	SMTPSSLEnabled         bool   `json:"smtp_ssl_enabled"`
	SMTPStartTLSEnabled    bool   `json:"smtp_starttls_enabled"`
	SMTPInsecureSkipVerify bool   `json:"smtp_insecure_skip_verify"`
	SMTPForceAuthLogin     bool   `json:"smtp_force_auth_login"`
	ResendFrom             string `json:"resend_from"`
	ResendSenderName       string `json:"resend_sender_name"`
	ResendApiKeyConfigured bool   `json:"resend_api_key_configured"`
	UpdatedAt              int64  `json:"updated_at,omitempty"`
}

// AdtySiteMailConfigUpdate is the owner-submitted payload for PUT /api/adty/site/mail-config.
type AdtySiteMailConfigUpdate struct {
	Provider               string `json:"provider"`
	SenderName             string `json:"sender_name"`
	SMTPServer             string `json:"smtp_server"`
	SMTPPort               int    `json:"smtp_port"`
	SMTPAccount            string `json:"smtp_account"`
	SMTPFrom               string `json:"smtp_from"`
	SMTPSenderName         string `json:"smtp_sender_name"`
	SMTPToken              string `json:"smtp_token"`
	ClearSMTPToken         bool   `json:"clear_smtp_token"`
	SMTPSSLEnabled         bool   `json:"smtp_ssl_enabled"`
	SMTPStartTLSEnabled    bool   `json:"smtp_starttls_enabled"`
	SMTPInsecureSkipVerify bool   `json:"smtp_insecure_skip_verify"`
	SMTPForceAuthLogin     bool   `json:"smtp_force_auth_login"`
	ResendFrom             string `json:"resend_from"`
	ResendSenderName       string `json:"resend_sender_name"`
	ResendApiKey           string `json:"resend_api_key"`
	ClearResendApiKey      bool   `json:"clear_resend_api_key"`
}

var (
	// ErrAdtySiteMailNotConfigured is returned when a subsite tries to send
	// operational mail without configuring its own SMTP or Resend transport (R-41 #8).
	ErrAdtySiteMailNotConfigured = common.NewMessage("Site email sending is not configured. Configure SMTP or Resend first.")

	// adtyMailLookupIP resolves hostnames for subsite SMTP SSRF validation.
	// Overridable in tests.
	adtyMailLookupIP = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return net.DefaultResolver.LookupIPAddr(ctx, host)
	}

	// Transport hooks used by sendSubsiteConfiguredEmail; overridable in tests.
	adtySendSubsiteSMTP   = common.SendSMTPEmailWithConfig
	adtySendSubsiteResend = common.SendResendEmailWithConfig

	subsiteBlockedIPPrefixes = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),      // RFC 1122 "this network"
		netip.MustParsePrefix("100.64.0.0/10"),  // RFC 6598 CGNAT
		netip.MustParsePrefix("192.0.0.0/24"),   // IETF protocol assignments
		netip.MustParsePrefix("192.0.2.0/24"),   // TEST-NET-1
		netip.MustParsePrefix("198.18.0.0/15"),  // RFC 2544 benchmarking
		netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
		netip.MustParsePrefix("203.0.113.0/24"), // TEST-NET-3
		netip.MustParsePrefix("240.0.0.0/4"),    // Reserved / limited broadcast
		netip.MustParsePrefix("::/96"),          // Deprecated IPv4-compatible IPv6
		netip.MustParsePrefix("::ffff:0:0:0/96"), // RFC 2765 SIIT IPv4-translated
		netip.MustParsePrefix("64:ff9b::/96"),   // RFC 6052 NAT64 well-known
		netip.MustParsePrefix("64:ff9b:1::/48"), // RFC 8215 NAT64 local-use
		netip.MustParsePrefix("100::/64"),       // RFC 6666 discard-only
		netip.MustParsePrefix("2001::/23"),      // RFC 2928 / Teredo / IETF protocol assignments
		netip.MustParsePrefix("2001:db8::/32"),  // RFC 3849 documentation
		netip.MustParsePrefix("2002::/16"),      // RFC 3056 6to4
		netip.MustParsePrefix("fec0::/10"),      // Deprecated IPv6 site-local
	}
)

func adtySiteMailSMTPPurpose(siteID int) string {
	return fmt.Sprintf("site-mail-smtp:%d", siteID)
}

func adtySiteMailResendPurpose(siteID int) string {
	return fmt.Sprintf("site-mail-resend:%d", siteID)
}

func isAllowedSubsiteMailAddr(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	addr = addr.Unmap()
	if addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsInterfaceLocalMulticast() ||
		addr.IsUnspecified() {
		return false
	}
	for _, prefix := range subsiteBlockedIPPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func isValidSubsiteHostname(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			ch := label[i]
			if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
				return false
			}
		}
	}
	return true
}

// ValidateSubsiteSMTPHost validates an SMTP host against SSRF rules and
// resolves it to a list of verified public IP addresses.
func ValidateSubsiteSMTPHost(ctx context.Context, host string) ([]net.IP, error) {
	if host == "" {
		return nil, common.NewMessage("SMTP host is invalid or points to a restricted address")
	}
	for _, r := range host {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return nil, common.NewMessage("SMTP host is invalid or points to a restricted address")
		}
	}
	if strings.ContainsAny(host, "/\\?#@[]%") || strings.Contains(host, "://") {
		return nil, common.NewMessage("SMTP host is invalid or points to a restricted address")
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return nil, common.NewMessage("SMTP host is invalid or points to a restricted address")
	}

	normalized := strings.TrimSuffix(strings.ToLower(host), ".")
	if normalized == "" || len(normalized) > 253 {
		return nil, common.NewMessage("SMTP host is invalid or points to a restricted address")
	}
	if normalized == "localhost" ||
		strings.HasSuffix(normalized, ".localhost") ||
		strings.HasSuffix(normalized, ".local") ||
		strings.HasSuffix(normalized, ".internal") {
		return nil, common.NewMessage("SMTP host is invalid or points to a restricted address")
	}

	if addr, err := netip.ParseAddr(normalized); err == nil {
		if !isAllowedSubsiteMailAddr(addr) {
			return nil, common.NewMessage("SMTP host is invalid or points to a restricted address")
		}
		unmapped := addr.Unmap()
		return []net.IP{net.IP(unmapped.AsSlice())}, nil
	}

	if strings.Contains(normalized, ":") || !isValidSubsiteHostname(normalized) {
		return nil, common.NewMessage("SMTP host is invalid or points to a restricted address")
	}

	if ctx == nil {
		ctx = context.Background()
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resolved, err := adtyMailLookupIP(lookupCtx, normalized)
	if err != nil || len(resolved) == 0 {
		return nil, common.NewMessage("SMTP server host could not be resolved")
	}

	ips := make([]net.IP, 0, len(resolved))
	for _, entry := range resolved {
		if entry.Zone != "" {
			return nil, common.NewMessage("SMTP host is invalid or points to a restricted address")
		}
		addr, ok := netip.AddrFromSlice(entry.IP)
		if !ok || !isAllowedSubsiteMailAddr(addr) {
			return nil, common.NewMessage("SMTP host is invalid or points to a restricted address")
		}
		unmapped := addr.Unmap()
		ips = append(ips, net.IP(unmapped.AsSlice()))
	}
	return ips, nil
}

// subsiteSMTPDialContext resolves and validates IPs at dial time and dials the
// validated IP literal directly to prevent DNS rebinding TOCTOU.
func subsiteSMTPDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, common.NewMessage("SMTP host is invalid or points to a restricted address")
	}
	ips, err := ValidateSubsiteSMTPHost(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var lastErr error
	for _, ip := range ips {
		target := net.JoinHostPort(ip.String(), port)
		conn, dialErr := dialer.DialContext(ctx, network, target)
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, common.NewMessage("SMTP server host could not be resolved")
}

func adtyMailHasControlChars(values ...string) bool {
	for _, v := range values {
		if strings.ContainsAny(v, "\r\n\x00") {
			return true
		}
	}
	return false
}

func validateSubsiteMailAddress(raw string) (*mail.Address, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || adtyMailHasControlChars(raw) {
		return nil, false
	}
	parsed, err := mail.ParseAddress(raw)
	if err != nil || parsed == nil {
		return nil, false
	}
	addr := strings.TrimSpace(parsed.Address)
	local, domain, ok := strings.Cut(addr, "@")
	if !ok || local == "" || domain == "" || strings.ContainsAny(addr, " \t\r\n\x00<>") {
		return nil, false
	}
	return parsed, true
}

func loadAdtySiteMailConfig(siteID int) (*model.AdtySiteMailConfig, bool, error) {
	if siteID <= 0 || model.DB == nil {
		return nil, false, nil
	}
	if !model.DB.Migrator().HasTable(&model.AdtySiteMailConfig{}) {
		return nil, false, nil
	}
	var row model.AdtySiteMailConfig
	res := model.DB.Where("site_id = ?", siteID).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, false, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, false, nil
	}
	return &row, true, nil
}

func adtySiteMailConfigToView(row *model.AdtySiteMailConfig) *AdtySiteMailConfigView {
	if row == nil {
		return &AdtySiteMailConfigView{
			Provider: "",
			SMTPPort: 587,
		}
	}
	provider := strings.TrimSpace(row.Provider)
	if provider == model.AdtySiteMailProviderPlatform {
		provider = ""
	}
	port := row.SMTPPort
	if port <= 0 {
		port = 587
	}
	return &AdtySiteMailConfigView{
		Provider:               provider,
		SenderName:             row.SenderName,
		SMTPServer:             row.SMTPServer,
		SMTPPort:               port,
		SMTPAccount:            row.SMTPAccount,
		SMTPFrom:               row.SMTPFrom,
		SMTPSenderName:         row.SMTPSenderName,
		SMTPTokenConfigured:    strings.TrimSpace(row.SMTPTokenCipher) != "",
		SMTPSSLEnabled:         row.SMTPSSLEnabled,
		SMTPStartTLSEnabled:    row.SMTPStartTLSEnabled,
		SMTPInsecureSkipVerify: row.SMTPInsecureSkipVerify,
		SMTPForceAuthLogin:     row.SMTPForceAuthLogin,
		ResendFrom:             row.ResendFrom,
		ResendSenderName:       row.ResendSenderName,
		ResendApiKeyConfigured: strings.TrimSpace(row.ResendApiKeyCipher) != "",
		UpdatedAt:              row.UpdatedAt,
	}
}

// GetAdtySiteMailConfig returns the site owner's mail config view.
func GetAdtySiteMailConfig(ownerUserID int) (*AdtySiteMailConfigView, error) {
	site, err := AdtyOwnerSite(ownerUserID)
	if err != nil {
		return nil, err
	}
	row, found, err := loadAdtySiteMailConfig(site.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return adtySiteMailConfigToView(nil), nil
	}
	return adtySiteMailConfigToView(row), nil
}

// SaveAdtySiteMailConfig validates, encrypts secrets, upserts the site's mail
// configuration, and writes a secret-free audit entry.
func SaveAdtySiteMailConfig(ctx context.Context, ownerUserID int, input AdtySiteMailConfigUpdate) (*AdtySiteMailConfigView, error) {
	site, err := AdtyOwnerSite(ownerUserID)
	if err != nil {
		return nil, err
	}
	if site.Status != model.AdtySiteStatusActive && site.Status != model.AdtySiteStatusPaused {
		return nil, ErrAdtySiteClosing
	}
	if adtyMailHasControlChars(
		input.Provider,
		input.SenderName,
		input.SMTPServer,
		input.SMTPAccount,
		input.SMTPFrom,
		input.SMTPSenderName,
		input.SMTPToken,
		input.ResendFrom,
		input.ResendSenderName,
		input.ResendApiKey,
	) {
		return nil, common.NewMessage("Email configuration fields must not contain line breaks or control characters")
	}

	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	switch provider {
	case model.AdtySiteMailProviderSMTP, model.AdtySiteMailProviderResend:
	default:
		return nil, common.NewMessage("Email provider must be SMTP or Resend")
	}

	senderName := strings.TrimSpace(input.SenderName)
	smtpServer := strings.TrimSpace(input.SMTPServer)
	smtpAccount := strings.TrimSpace(input.SMTPAccount)
	smtpFrom := strings.TrimSpace(input.SMTPFrom)
	smtpSenderName := strings.TrimSpace(input.SMTPSenderName)
	smtpToken := strings.TrimSpace(input.SMTPToken)
	resendFrom := strings.TrimSpace(input.ResendFrom)
	resendSenderName := strings.TrimSpace(input.ResendSenderName)
	resendApiKey := strings.TrimSpace(input.ResendApiKey)

	if utf8.RuneCountInString(senderName) > 128 ||
		utf8.RuneCountInString(smtpSenderName) > 128 ||
		utf8.RuneCountInString(resendSenderName) > 128 {
		return nil, common.NewMessage("Sender display name must be at most 128 characters")
	}
	if len(smtpServer) > 255 || len(smtpAccount) > 255 || len(smtpFrom) > 255 || len(resendFrom) > 255 {
		return nil, common.NewMessage("Email configuration field is too long")
	}

	switch provider {
	case model.AdtySiteMailProviderSMTP:
		if _, err := ValidateSubsiteSMTPHost(ctx, smtpServer); err != nil {
			return nil, err
		}
		if input.SMTPPort < 1 || input.SMTPPort > 65535 {
			return nil, common.NewMessage("SMTP port must be between 1 and 65535")
		}
		if input.SMTPSSLEnabled && input.SMTPStartTLSEnabled {
			return nil, common.NewMessage("SSL/TLS and STARTTLS cannot be enabled at the same time")
		}
		effectiveFrom := smtpFrom
		if effectiveFrom == "" {
			effectiveFrom = smtpAccount
		}
		if _, ok := validateSubsiteMailAddress(effectiveFrom); !ok {
			return nil, common.NewMessage("Enter a valid SMTP sender email address")
		}
	case model.AdtySiteMailProviderResend:
		if _, ok := validateSubsiteMailAddress(resendFrom); !ok {
			return nil, common.NewMessage("Resend sender address is not configured or invalid")
		}
	}

	var sealedSMTPToken string
	if smtpToken != "" {
		sealed, err := model.SealAdtyPurposeSecret(adtySiteMailSMTPPurpose(site.Id), smtpToken)
		if err != nil {
			return nil, err
		}
		sealedSMTPToken = sealed
	}
	var sealedResendKey string
	if resendApiKey != "" {
		sealed, err := model.SealAdtyPurposeSecret(adtySiteMailResendPurpose(site.Id), resendApiKey)
		if err != nil {
			return nil, err
		}
		sealedResendKey = sealed
	}

	var saved model.AdtySiteMailConfig
	var auditEvent *model.AuditLog
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var locked model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Where("id = ? AND owner_user_id = ?", site.Id, ownerUserID).First(&locked).Error; err != nil {
			return err
		}
		if locked.Status != model.AdtySiteStatusActive && locked.Status != model.AdtySiteStatusPaused {
			return ErrAdtySiteClosing
		}

		var row model.AdtySiteMailConfig
		lookup := model.AdtyLockForUpdate(tx).Where("site_id = ?", site.Id).Limit(1).Find(&row)
		if lookup.Error != nil {
			return lookup.Error
		}
		existed := lookup.RowsAffected > 0
		oldProvider := ""
		if existed && strings.TrimSpace(row.Provider) != "" && row.Provider != model.AdtySiteMailProviderPlatform {
			oldProvider = row.Provider
		} else if !existed {
			row.SiteId = site.Id
			row.SMTPPort = 587
		}

		row.Provider = provider
		row.SenderName = ""
		switch provider {
		case model.AdtySiteMailProviderSMTP:
			row.SMTPServer = smtpServer
			row.SMTPPort = input.SMTPPort
			row.SMTPAccount = smtpAccount
			row.SMTPFrom = smtpFrom
			row.SMTPSenderName = smtpSenderName
			row.SMTPSSLEnabled = input.SMTPSSLEnabled
			row.SMTPStartTLSEnabled = input.SMTPStartTLSEnabled
			row.SMTPInsecureSkipVerify = input.SMTPInsecureSkipVerify
			row.SMTPForceAuthLogin = input.SMTPForceAuthLogin
		case model.AdtySiteMailProviderResend:
			row.ResendFrom = resendFrom
			row.ResendSenderName = resendSenderName
		}

		if sealedSMTPToken != "" {
			row.SMTPTokenCipher = sealedSMTPToken
		} else if input.ClearSMTPToken {
			row.SMTPTokenCipher = ""
		}

		if sealedResendKey != "" {
			row.ResendApiKeyCipher = sealedResendKey
		} else if input.ClearResendApiKey {
			row.ResendApiKeyCipher = ""
		}

		if provider == model.AdtySiteMailProviderResend && strings.TrimSpace(row.ResendApiKeyCipher) == "" {
			return common.NewMessage("Configure a Resend API key before enabling Resend")
		}
		if row.SMTPPort <= 0 {
			row.SMTPPort = 587
		}
		row.UpdatedAt = time.Now().Unix()

		if !existed {
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
		}

		if tx.Migrator().HasTable(&model.AdtySiteAudit{}) {
			if err := tx.Create(&model.AdtySiteAudit{
				SiteId:     site.Id,
				Action:     "mail_config_update",
				OperatorId: ownerUserID,
				OldValue:   oldProvider,
				NewValue:   row.Provider,
				CreatedAt:  row.UpdatedAt,
			}).Error; err != nil {
				return err
			}
		}
		var auditErr error
		auditEvent, auditErr = appendAdtyOfficialAuditTx(
			tx,
			ownerUserID,
			site.Id,
			"site_mail_config",
			strconv.Itoa(site.Id),
			"adty.site.mail_config_update",
			"",
			"",
			map[string]any{"provider": oldProvider},
			map[string]any{
				"provider":           row.Provider,
				"smtp_server":        row.SMTPServer,
				"smtp_port":          row.SMTPPort,
				"smtp_from":          row.SMTPFrom,
				"resend_from":        row.ResendFrom,
				"resend_sender_name": row.ResendSenderName,
			},
			model.AuditResultSuccess,
		)
		if auditErr != nil {
			return auditErr
		}

		saved = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	if auditEvent != nil {
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return adtySiteMailConfigToView(&saved), nil
}

func adtySubsiteFallbackDisplayName(site *model.AdtySite) string {
	if site == nil {
		return ""
	}
	if name := strings.TrimSpace(AdtyBrandFor(site).Name); name != "" {
		return name
	}
	if host := strings.TrimSpace(AdtySiteDisplayHost(site)); host != "" {
		return host
	}
	return strings.TrimSpace(site.Subdomain)
}

// EnsureAdtySiteMailConfigured enforces the R-41 #8A policy before enqueuing
// asynchronous subsite emails or accepting subsite verification flows.
func EnsureAdtySiteMailConfigured(site *model.AdtySite) error {
	if site == nil || site.Id <= 0 {
		return nil
	}
	cfg, found, err := loadAdtySiteMailConfig(site.Id)
	if err != nil {
		return err
	}
	if !found || cfg == nil {
		return ErrAdtySiteMailNotConfigured
	}
	switch cfg.Provider {
	case model.AdtySiteMailProviderSMTP:
		effectiveFrom := strings.TrimSpace(cfg.SMTPFrom)
		if effectiveFrom == "" {
			effectiveFrom = strings.TrimSpace(cfg.SMTPAccount)
		}
		if strings.TrimSpace(cfg.SMTPServer) == "" || effectiveFrom == "" {
			return ErrAdtySiteMailNotConfigured
		}
		return nil
	case model.AdtySiteMailProviderResend:
		if strings.TrimSpace(cfg.ResendApiKeyCipher) == "" || strings.TrimSpace(cfg.ResendFrom) == "" {
			return ErrAdtySiteMailNotConfigured
		}
		return nil
	default:
		return ErrAdtySiteMailNotConfigured
	}
}

// sendSubsiteConfiguredEmail delivers a subsite email using its configured
// transport (custom SMTP or own Resend API key). Unconfigured subsites return
// ErrAdtySiteMailNotConfigured (R-41 #8A); official site-closure migration
// emails and platform-to-owner notices use platform transport separately.
func sendSubsiteConfiguredEmail(site *model.AdtySite, subject, receiver, content string) error {
	if site == nil {
		return common.SendEmail(subject, receiver, content)
	}
	cfg, found, err := loadAdtySiteMailConfig(site.Id)
	if err != nil {
		return err
	}
	if !found || cfg == nil {
		return ErrAdtySiteMailNotConfigured
	}
	switch cfg.Provider {
	case model.AdtySiteMailProviderSMTP:
		token := ""
		if strings.TrimSpace(cfg.SMTPTokenCipher) != "" {
			opened, err := model.OpenAdtyPurposeSecret(adtySiteMailSMTPPurpose(site.Id), cfg.SMTPTokenCipher)
			if err != nil {
				return err
			}
			token = opened
		}
		displayName := strings.TrimSpace(cfg.SMTPSenderName)
		if displayName == "" {
			if parsed, ok := validateSubsiteMailAddress(cfg.SMTPFrom); ok && strings.TrimSpace(parsed.Name) != "" {
				displayName = strings.TrimSpace(parsed.Name)
			}
		}
		if displayName == "" {
			displayName = adtySubsiteFallbackDisplayName(site)
		}
		port := cfg.SMTPPort
		if port <= 0 {
			port = 587
		}
		sendErr := adtySendSubsiteSMTP(common.SMTPTransportConfig{
			Server:             cfg.SMTPServer,
			Port:               port,
			Account:            cfg.SMTPAccount,
			Token:              token,
			From:               cfg.SMTPFrom,
			DisplayName:        displayName,
			SSLEnabled:         cfg.SMTPSSLEnabled,
			StartTLSEnabled:    cfg.SMTPStartTLSEnabled,
			InsecureSkipVerify: cfg.SMTPInsecureSkipVerify,
			ForceAuthLogin:     cfg.SMTPForceAuthLogin,
			DialContext:        subsiteSMTPDialContext,
		}, "", subject, receiver, content)
		if sendErr != nil {
			var msg *common.Message
			if errors.As(sendErr, &msg) {
				return sendErr
			}
			return common.SanitizeSMTPError(sendErr, cfg.SMTPAccount, token)
		}
		return nil

	case model.AdtySiteMailProviderResend:
		if strings.TrimSpace(cfg.ResendApiKeyCipher) == "" {
			return common.NewMessage("Resend API key is not configured or invalid")
		}
		apiKey, err := model.OpenAdtyPurposeSecret(adtySiteMailResendPurpose(site.Id), cfg.ResendApiKeyCipher)
		if err != nil {
			return err
		}
		displayName := strings.TrimSpace(cfg.ResendSenderName)
		if displayName == "" {
			if parsed, ok := validateSubsiteMailAddress(cfg.ResendFrom); ok && strings.TrimSpace(parsed.Name) != "" {
				displayName = strings.TrimSpace(parsed.Name)
			}
		}
		if displayName == "" {
			displayName = adtySubsiteFallbackDisplayName(site)
		}
		if sendErr := adtySendSubsiteResend(apiKey, cfg.ResendFrom, displayName, subject, receiver, content); sendErr != nil {
			var msg *common.Message
			if errors.As(sendErr, &msg) {
				return sendErr
			}
			return common.SanitizeOutboundError(sendErr, apiKey)
		}
		return nil

	default:
		return ErrAdtySiteMailNotConfigured
	}
}

func classifySubsiteTestMailError(siteID int, sendErr error) error {
	if sendErr == nil {
		return nil
	}
	var msg *common.Message
	if errors.As(sendErr, &msg) {
		return sendErr
	}
	common.SysError(common.LogText("subsite %d test email delivery failed: %s", siteID, sendErr.Error()))
	var mailErr *common.SanitizedMailError
	if errors.As(sendErr, &mailErr) {
		switch mailErr.Kind {
		case common.MailErrAuth:
			return common.NewMessage("Email authentication failed")
		case common.MailErrTLS:
			return common.NewMessage("TLS handshake with mail server failed")
		case common.MailErrTimeout:
			return common.NewMessage("Connection to mail server timed out")
		case common.MailErrConnect:
			return common.NewMessage("Could not connect to mail server")
		case common.MailErrRejected:
			return common.NewMessage("Mail server rejected the test message")
		}
	}
	var tpErr *textproto.Error
	if errors.As(sendErr, &tpErr) {
		switch tpErr.Code {
		case 530, 534, 535, 538:
			return common.NewMessage("Email authentication failed")
		default:
			return common.NewMessage("Mail server rejected the test message")
		}
	}
	if errors.Is(sendErr, context.DeadlineExceeded) {
		return common.NewMessage("Connection to mail server timed out")
	}
	var netErr net.Error
	if errors.As(sendErr, &netErr) && netErr.Timeout() {
		return common.NewMessage("Connection to mail server timed out")
	}
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var certInvalid x509.CertificateInvalidError
	if errors.As(sendErr, &unknownAuth) || errors.As(sendErr, &hostErr) || errors.As(sendErr, &certInvalid) {
		return common.NewMessage("TLS handshake with mail server failed")
	}
	lower := strings.ToLower(sendErr.Error())
	switch {
	case strings.Contains(lower, "tls:") || strings.Contains(lower, "x509:") || strings.Contains(lower, "starttls"):
		return common.NewMessage("TLS handshake with mail server failed")
	case strings.Contains(lower, "535") || strings.Contains(lower, "authentication") || strings.Contains(lower, "invalid api key") || strings.Contains(lower, "unauthorized"):
		return common.NewMessage("Email authentication failed")
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out") || strings.Contains(lower, "deadline exceeded"):
		return common.NewMessage("Connection to mail server timed out")
	case strings.Contains(lower, "resend"):
		return common.NewMessage("Resend API request failed")
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "no such host") || strings.Contains(lower, "unreachable") || strings.Contains(lower, "eof"):
		return common.NewMessage("Could not connect to mail server")
	default:
		return common.NewMessage("Failed to send test email")
	}
}

// TestAdtySiteMailConfig sends a test email to a single validated recipient
// using the site's currently saved email configuration.
func TestAdtySiteMailConfig(ctx context.Context, ownerUserID int, toEmail string) error {
	_ = ctx
	site, err := AdtyOwnerSite(ownerUserID)
	if err != nil {
		return err
	}
	if site.Status != model.AdtySiteStatusActive && site.Status != model.AdtySiteStatusPaused {
		return ErrAdtySiteClosing
	}
	trimmed := strings.TrimSpace(toEmail)
	if trimmed == "" || adtyMailHasControlChars(trimmed) || strings.ContainsAny(trimmed, ";,<>") {
		return common.NewMessage("Invalid email address")
	}
	parsed, ok := validateSubsiteMailAddress(trimmed)
	if !ok || !strings.EqualFold(parsed.Address, trimmed) {
		return common.NewMessage("Invalid email address")
	}

	brandName := adtySubsiteFallbackDisplayName(site)
	subject := brandName + " - Test Email"
	content := AdtyRenderMail(site, AdtyMailMessage{
		Title: "Test Email",
		Intro: "This is a test email from " + brandName + ".",
		Paragraphs: []string{
			"Your site email configuration is working properly.",
		},
	})
	return classifySubsiteTestMailError(site.Id, AdtySendSiteEmail(site, subject, parsed.Address, content))
}
