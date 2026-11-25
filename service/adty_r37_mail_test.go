package service

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupR37MailTest(t *testing.T) *gorm.DB {
	t.Helper()
	t.Setenv("CRYPTO_SECRET", strings.Repeat("a", 32))

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.AdtySite{},
		&model.AdtySiteDomain{},
		&model.AdtySiteMailConfig{},
		&model.AdtySiteAudit{},
		&model.AdtySiteSenderDomain{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	oldDB := model.DB
	oldLookup := adtyMailLookupIP
	oldSMTP := adtySendSubsiteSMTP
	oldResend := adtySendSubsiteResend
	oldPlatform := adtySendSubsitePlatform
	oldDeliver := AdtyDeliverSiteEmail
	oldSystemSender := AdtySystemEmailSender

	model.DB = db
	InvalidateAdtySiteCache()

	t.Cleanup(func() {
		model.DB = oldDB
		adtyMailLookupIP = oldLookup
		adtySendSubsiteSMTP = oldSMTP
		adtySendSubsiteResend = oldResend
		adtySendSubsitePlatform = oldPlatform
		AdtyDeliverSiteEmail = oldDeliver
		AdtySystemEmailSender = oldSystemSender
		InvalidateAdtySiteCache()
	})

	return db
}

func createR37MailTestSite(t *testing.T, db *gorm.DB, ownerID int, subdomain, brandName string) model.AdtySite {
	t.Helper()
	brandJSON := ""
	if brandName != "" {
		raw, err := common.Marshal(AdtySiteBrandInput{Name: brandName})
		if err != nil {
			t.Fatalf("marshal brand: %v", err)
		}
		brandJSON = string(raw)
	}
	site := model.AdtySite{
		OwnerUserId: ownerID,
		Subdomain:   subdomain,
		RootDomain:  "platform.test",
		Status:      model.AdtySiteStatusActive,
		Brand:       brandJSON,
	}
	if err := db.Create(&site).Error; err != nil {
		t.Fatalf("create site: %v", err)
	}
	InvalidateAdtySiteCache()
	return site
}

func TestR37MailConfigCRUDAndSecretEncryption(t *testing.T) {
	db := setupR37MailTest(t)
	site := createR37MailTestSite(t, db, 201, "alpha", "Alpha AI")
	ctx := context.Background()

	adtyMailLookupIP = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if host == "smtp.public.example" {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}

	// 1. Default view before any row exists.
	defView, err := GetAdtySiteMailConfig(201)
	if err != nil {
		t.Fatalf("default get failed: %v", err)
	}
	if defView.Provider != model.AdtySiteMailProviderPlatform || defView.SMTPPort != 587 {
		t.Fatalf("unexpected default view: %+v", defView)
	}
	if defView.SMTPTokenConfigured || defView.ResendApiKeyConfigured {
		t.Fatalf("expected no configured secrets by default: %+v", defView)
	}

	// 2. Save platform sender name.
	platView, err := SaveAdtySiteMailConfig(ctx, 201, AdtySiteMailConfigUpdate{
		Provider:   model.AdtySiteMailProviderPlatform,
		SenderName: "Alpha Notifications",
	})
	if err != nil {
		t.Fatalf("save platform config: %v", err)
	}
	if platView.Provider != model.AdtySiteMailProviderPlatform || platView.SenderName != "Alpha Notifications" {
		t.Fatalf("unexpected platform view: %+v", platView)
	}

	// 3. Save SMTP config with secret token -> sealed in DB, never returned in JSON view.
	smtpSecret := "smtp-super-secret-token-999"
	smtpView, err := SaveAdtySiteMailConfig(ctx, 201, AdtySiteMailConfigUpdate{
		Provider:            model.AdtySiteMailProviderSMTP,
		SMTPServer:          "smtp.public.example",
		SMTPPort:            587,
		SMTPAccount:         "auth@alpha.example",
		SMTPFrom:            "noreply@alpha.example",
		SMTPSenderName:      "Alpha Mailer",
		SMTPToken:           smtpSecret,
		SMTPStartTLSEnabled: true,
	})
	if err != nil {
		t.Fatalf("save smtp config: %v", err)
	}
	if !smtpView.SMTPTokenConfigured {
		t.Fatalf("expected smtp_token_configured=true")
	}
	viewBytes, err := common.Marshal(smtpView)
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	if strings.Contains(string(viewBytes), smtpSecret) {
		t.Fatalf("plaintext SMTP secret leaked in view JSON: %s", string(viewBytes))
	}

	var stored model.AdtySiteMailConfig
	if err := db.Where("site_id = ?", site.Id).First(&stored).Error; err != nil {
		t.Fatalf("load stored config: %v", err)
	}
	if !strings.HasPrefix(stored.SMTPTokenCipher, "adty:secret:v1:") {
		t.Fatalf("expected sealed SMTP token cipher, got %q", stored.SMTPTokenCipher)
	}
	if strings.Contains(stored.SMTPTokenCipher, smtpSecret) {
		t.Fatalf("ciphertext must not contain plaintext token")
	}
	openedSMTP, err := model.OpenAdtyPurposeSecret(adtySiteMailSMTPPurpose(site.Id), stored.SMTPTokenCipher)
	if err != nil || openedSMTP != smtpSecret {
		t.Fatalf("open SMTP secret: got %q err=%v", openedSMTP, err)
	}

	// 4. Updating SMTP without passing SMTPToken preserves existing sealed secret.
	oldCipher := stored.SMTPTokenCipher
	smtpView2, err := SaveAdtySiteMailConfig(ctx, 201, AdtySiteMailConfigUpdate{
		Provider:            model.AdtySiteMailProviderSMTP,
		SMTPServer:          "smtp.public.example",
		SMTPPort:            465,
		SMTPAccount:         "auth@alpha.example",
		SMTPFrom:            "noreply@alpha.example",
		SMTPSenderName:      "Alpha Mailer v2",
		SMTPSSLEnabled:      true,
		SMTPStartTLSEnabled: false,
	})
	if err != nil {
		t.Fatalf("update smtp preserving secret: %v", err)
	}
	if !smtpView2.SMTPTokenConfigured || smtpView2.SMTPPort != 465 || !smtpView2.SMTPSSLEnabled {
		t.Fatalf("unexpected updated smtp view: %+v", smtpView2)
	}
	if err := db.Where("site_id = ?", site.Id).First(&stored).Error; err != nil {
		t.Fatalf("reload stored config: %v", err)
	}
	if stored.SMTPTokenCipher != oldCipher {
		t.Fatalf("expected existing SMTP cipher preserved when token omitted")
	}

	// 5. Clearing SMTP token clears DB cipher and flips configured flag to false.
	smtpCleared, err := SaveAdtySiteMailConfig(ctx, 201, AdtySiteMailConfigUpdate{
		Provider:       model.AdtySiteMailProviderSMTP,
		SMTPServer:     "smtp.public.example",
		SMTPPort:       587,
		SMTPFrom:       "noreply@alpha.example",
		ClearSMTPToken: true,
	})
	if err != nil {
		t.Fatalf("clear smtp token: %v", err)
	}
	if smtpCleared.SMTPTokenConfigured {
		t.Fatalf("expected smtp_token_configured=false after clear")
	}
	if err := db.Where("site_id = ?", site.Id).First(&stored).Error; err != nil {
		t.Fatalf("reload after clear: %v", err)
	}
	if stored.SMTPTokenCipher != "" {
		t.Fatalf("expected empty SMTPTokenCipher after clear, got %q", stored.SMTPTokenCipher)
	}

	// 6. Save Resend config: fails without key, succeeds with key, preserves on omit, rejects clear while resend active.
	if _, err := SaveAdtySiteMailConfig(ctx, 201, AdtySiteMailConfigUpdate{
		Provider:   model.AdtySiteMailProviderResend,
		ResendFrom: "mail@alpha.example",
	}); err == nil {
		t.Fatalf("expected error enabling resend without API key")
	}

	resendSecret := "re_live_subsite_secret_12345"
	resendView, err := SaveAdtySiteMailConfig(ctx, 201, AdtySiteMailConfigUpdate{
		Provider:         model.AdtySiteMailProviderResend,
		ResendFrom:       "mail@alpha.example",
		ResendSenderName: "Alpha Resend",
		ResendApiKey:     resendSecret,
	})
	if err != nil {
		t.Fatalf("save resend config: %v", err)
	}
	if !resendView.ResendApiKeyConfigured {
		t.Fatalf("expected resend_api_key_configured=true")
	}
	resendJSON, _ := common.Marshal(resendView)
	if strings.Contains(string(resendJSON), resendSecret) {
		t.Fatalf("plaintext Resend API key leaked in view JSON: %s", string(resendJSON))
	}

	if err := db.Where("site_id = ?", site.Id).First(&stored).Error; err != nil {
		t.Fatalf("reload resend stored config: %v", err)
	}
	if !strings.HasPrefix(stored.ResendApiKeyCipher, "adty:secret:v1:") {
		t.Fatalf("expected sealed Resend API key cipher, got %q", stored.ResendApiKeyCipher)
	}
	openedResend, err := model.OpenAdtyPurposeSecret(adtySiteMailResendPurpose(site.Id), stored.ResendApiKeyCipher)
	if err != nil || openedResend != resendSecret {
		t.Fatalf("open Resend secret: got %q err=%v", openedResend, err)
	}

	// Omitting ResendApiKey preserves existing key.
	resendView2, err := SaveAdtySiteMailConfig(ctx, 201, AdtySiteMailConfigUpdate{
		Provider:         model.AdtySiteMailProviderResend,
		ResendFrom:       "hello@alpha.example",
		ResendSenderName: "Alpha Hello",
	})
	if err != nil {
		t.Fatalf("update resend preserving key: %v", err)
	}
	if !resendView2.ResendApiKeyConfigured || resendView2.ResendFrom != "hello@alpha.example" {
		t.Fatalf("unexpected updated resend view: %+v", resendView2)
	}

	// Clearing ResendApiKey while switching back to platform works.
	clearedResendView, err := SaveAdtySiteMailConfig(ctx, 201, AdtySiteMailConfigUpdate{
		Provider:          model.AdtySiteMailProviderPlatform,
		SenderName:        "Alpha Back",
		ClearResendApiKey: true,
	})
	if err != nil {
		t.Fatalf("clear resend key on platform switch: %v", err)
	}
	if clearedResendView.ResendApiKeyConfigured {
		t.Fatalf("expected resend_api_key_configured=false after clear")
	}

	// Audit records must never contain secrets.
	var audits []model.AdtySiteAudit
	if err := db.Where("site_id = ? AND action = ?", site.Id, "mail_config_update").Find(&audits).Error; err != nil {
		t.Fatalf("load audits: %v", err)
	}
	if len(audits) == 0 {
		t.Fatalf("expected mail_config_update audit entries")
	}
	for _, a := range audits {
		if strings.Contains(a.NewValue, smtpSecret) || strings.Contains(a.NewValue, resendSecret) ||
			strings.Contains(a.OldValue, smtpSecret) || strings.Contains(a.OldValue, resendSecret) {
			t.Fatalf("secret leaked into site audit: %+v", a)
		}
	}
}

func TestR37MailSSRFProtection(t *testing.T) {
	_ = setupR37MailTest(t)
	ctx := context.Background()

	dnsRecords := map[string][]net.IPAddr{
		"public.example.com":     {{IP: net.ParseIP("93.184.216.34")}},
		"mixed.example.com":      {{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("10.0.0.5")}},
		"rebind.example.com":     {{IP: net.ParseIP("169.254.169.254")}},
		"ipv6public.example.com": {{IP: net.ParseIP("2606:4700:4700::1111")}},
	}
	adtyMailLookupIP = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if ips, ok := dnsRecords[host]; ok {
			return ips, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}

	blockedHosts := []string{
		"",
		"   ",
		"localhost",
		"sub.localhost",
		"printer.local",
		"metadata.internal",
		"smtp.example.com:587",
		"https://smtp.example.com",
		"smtp.example.com/path",
		"127.0.0.1",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.1.1",
		"169.254.169.254",
		"0.0.0.0",
		"100.64.0.1",
		"192.0.0.1",
		"198.18.0.1",
		"240.0.0.1",
		"::1",
		"::",
		"fc00::1",
		"fe80::1",
		"::ffff:127.0.0.1",
		"::ffff:169.254.169.254",
		"mixed.example.com",
		"rebind.example.com",
	}
	for _, host := range blockedHosts {
		if ips, err := ValidateSubsiteSMTPHost(ctx, host); err == nil {
			t.Errorf("expected host %q to be blocked, but got IPs %v", host, ips)
		}
	}

	allowedHosts := []string{
		"1.1.1.1",
		"93.184.216.34",
		"public.example.com",
		"ipv6public.example.com",
	}
	for _, host := range allowedHosts {
		ips, err := ValidateSubsiteSMTPHost(ctx, host)
		if err != nil || len(ips) == 0 {
			t.Errorf("expected host %q to be allowed, got ips=%v err=%v", host, ips, err)
		}
	}
}

func TestSendSMTPWithConfigExtractsEnvelopeAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock smtp: %v", err)
	}
	defer ln.Close()

	var (
		mu       sync.Mutex
		mailFrom string
		rcptTo   []string
		dataBody string
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
		writeLine := func(line string) {
			_, _ = rw.WriteString(line + "\r\n")
			_ = rw.Flush()
		}
		writeLine("220 mock.smtp.test ESMTP")
		inData := false
		var bodyLines []string
		for {
			line, readErr := rw.ReadString('\n')
			if readErr != nil {
				return
			}
			trimmed := strings.TrimRight(line, "\r\n")
			if inData {
				if trimmed == "." {
					inData = false
					mu.Lock()
					dataBody = strings.Join(bodyLines, "\r\n")
					mu.Unlock()
					writeLine("250 2.0.0 OK")
					continue
				}
				bodyLines = append(bodyLines, trimmed)
				continue
			}
			upper := strings.ToUpper(trimmed)
			switch {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				writeLine("250-mock.smtp.test")
				writeLine("250 OK")
			case strings.HasPrefix(upper, "MAIL FROM:"):
				mu.Lock()
				mailFrom = strings.TrimSpace(trimmed[len("MAIL FROM:"):])
				mu.Unlock()
				writeLine("250 OK")
			case strings.HasPrefix(upper, "RCPT TO:"):
				mu.Lock()
				rcptTo = append(rcptTo, strings.TrimSpace(trimmed[len("RCPT TO:"):]))
				mu.Unlock()
				writeLine("250 OK")
			case upper == "DATA":
				inData = true
				writeLine("354 Start mail input")
			case upper == "QUIT":
				writeLine("221 Bye")
				return
			default:
				writeLine("250 OK")
			}
		}
	}()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	dialed := false
	err = common.SendSMTPEmailWithConfig(common.SMTPTransportConfig{
		Server: host,
		Port:   port,
		From:   "My Brand <noreply@example.com>",
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialed = true
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}, "", "Hello Subject", "dest@example.org", "<p>Body</p>")
	if err != nil {
		t.Fatalf("SendSMTPEmailWithConfig failed: %v", err)
	}
	<-done

	if !dialed {
		t.Fatalf("expected custom DialContext to be used")
	}
	mu.Lock()
	defer mu.Unlock()
	if mailFrom != "<noreply@example.com>" {
		t.Fatalf("expected envelope MAIL FROM <noreply@example.com>, got %q", mailFrom)
	}
	if len(rcptTo) != 1 || rcptTo[0] != "<dest@example.org>" {
		t.Fatalf("unexpected RCPT TO: %v", rcptTo)
	}
	if !strings.Contains(dataBody, `From: "My Brand" <noreply@example.com>`) {
		t.Fatalf("expected RFC 5322 From header with display name, got:\n%s", dataBody)
	}
	if !strings.Contains(dataBody, "@example.com>") {
		t.Fatalf("expected Message-ID with pure domain @example.com>, got:\n%s", dataBody)
	}
}

func TestR37MailSubsiteDeliveryRouting(t *testing.T) {
	db := setupR37MailTest(t)
	site := createR37MailTestSite(t, db, 301, "bravo", "Bravo Brand")
	ctx := context.Background()

	common.OptionMapRWMutex.Lock()
	oldOptionMapNil := common.OptionMap == nil
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	oldSystemName := common.SystemName
	oldSMTPFrom := common.SMTPFrom
	oldProvider, hadProvider := common.OptionMap["EmailProvider"]
	common.SystemName = "MAIN PLATFORM NAME"
	common.SMTPFrom = "platform@main.test"
	common.OptionMap["EmailProvider"] = "smtp"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.SystemName = oldSystemName
		common.SMTPFrom = oldSMTPFrom
		if oldOptionMapNil {
			common.OptionMap = nil
		} else if hadProvider {
			common.OptionMap["EmailProvider"] = oldProvider
		} else {
			delete(common.OptionMap, "EmailProvider")
		}
		common.OptionMapRWMutex.Unlock()
	})

	adtyMailLookupIP = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}

	var gotPlatformFrom, gotSMTPServer, gotSMTPDisplayName, gotSMTPToken string
	var gotResendKey, gotResendFrom, gotResendDisplayName string

	adtySendSubsitePlatform = func(from, subject, receiver, content string) error {
		gotPlatformFrom = from
		return nil
	}
	adtySendSubsiteSMTP = func(cfg common.SMTPTransportConfig, fromHeader, subject, receiver, content string) error {
		gotSMTPServer = cfg.Server
		gotSMTPDisplayName = cfg.DisplayName
		gotSMTPToken = cfg.Token
		if cfg.DialContext == nil {
			return fmt.Errorf("expected SSRF-safe DialContext on subsite SMTP config")
		}
		return nil
	}
	adtySendSubsiteResend = func(apiKey, fromAddr, displayName, subject, receiver, content string) error {
		gotResendKey = apiKey
		gotResendFrom = fromAddr
		gotResendDisplayName = displayName
		return nil
	}

	// 1. Default (no config row): platform transport uses site brand, NEVER main site name.
	if err := AdtySendSiteEmail(&site, "Test", "user@example.com", "<p>hi</p>"); err != nil {
		t.Fatalf("platform default send: %v", err)
	}
	if !strings.Contains(gotPlatformFrom, "Bravo Brand") || strings.Contains(gotPlatformFrom, "MAIN PLATFORM NAME") {
		t.Fatalf("expected site brand in platform sender, got %q", gotPlatformFrom)
	}

	// 2. Platform config with custom SenderName overrides brand name.
	if _, err := SaveAdtySiteMailConfig(ctx, 301, AdtySiteMailConfigUpdate{
		Provider:   model.AdtySiteMailProviderPlatform,
		SenderName: "Bravo Support Team",
	}); err != nil {
		t.Fatalf("save platform sender name: %v", err)
	}
	gotPlatformFrom = ""
	if err := AdtySendSiteEmail(&site, "Test", "user@example.com", "<p>hi</p>"); err != nil {
		t.Fatalf("platform custom sender send: %v", err)
	}
	if !strings.Contains(gotPlatformFrom, "Bravo Support Team") {
		t.Fatalf("expected custom platform SenderName, got %q", gotPlatformFrom)
	}

	// 3. SMTP config dispatches via subsite SMTP with decrypted token and site brand fallback.
	if _, err := SaveAdtySiteMailConfig(ctx, 301, AdtySiteMailConfigUpdate{
		Provider:            model.AdtySiteMailProviderSMTP,
		SMTPServer:          "smtp.bravo.example",
		SMTPPort:            587,
		SMTPAccount:         "user@bravo.example",
		SMTPFrom:            "noreply@bravo.example",
		SMTPToken:           "bravo-smtp-pass",
		SMTPStartTLSEnabled: true,
	}); err != nil {
		t.Fatalf("save smtp config: %v", err)
	}
	if err := AdtySendSiteEmail(&site, "Test", "user@example.com", "<p>hi</p>"); err != nil {
		t.Fatalf("subsite smtp send: %v", err)
	}
	if gotSMTPServer != "smtp.bravo.example" || gotSMTPToken != "bravo-smtp-pass" || gotSMTPDisplayName != "Bravo Brand" {
		t.Fatalf("unexpected smtp dispatch: server=%q token=%q display=%q", gotSMTPServer, gotSMTPToken, gotSMTPDisplayName)
	}

	// 4. Resend config dispatches via subsite Resend with decrypted API key and custom display name.
	if _, err := SaveAdtySiteMailConfig(ctx, 301, AdtySiteMailConfigUpdate{
		Provider:         model.AdtySiteMailProviderResend,
		ResendFrom:       "alerts@bravo.example",
		ResendSenderName: "Bravo Alerts",
		ResendApiKey:     "re_bravo_secret_key",
	}); err != nil {
		t.Fatalf("save resend config: %v", err)
	}
	if err := AdtySendSiteEmail(&site, "Test", "user@example.com", "<p>hi</p>"); err != nil {
		t.Fatalf("subsite resend send: %v", err)
	}
	if gotResendKey != "re_bravo_secret_key" || gotResendFrom != "alerts@bravo.example" || gotResendDisplayName != "Bravo Alerts" {
		t.Fatalf("unexpected resend dispatch: key=%q from=%q display=%q", gotResendKey, gotResendFrom, gotResendDisplayName)
	}

	// 5. System email outbox row with SiteId > 0 routes through AdtySendSiteEmail (Resend here).
	gotResendKey = ""
	if err := db.AutoMigrate(&AdtySystemEmailDelivery{}); err != nil {
		t.Fatalf("migrate outbox: %v", err)
	}
	row, err := DeliverAdtySystemEmail(AdtySystemEmailRequest{
		Category:  AdtySystemEmailCategoryOrderNotice,
		SiteID:    site.Id,
		Recipient: "customer@example.com",
		Subject:   "Order Notice",
		Content:   "<p>order</p>",
	})
	if err != nil {
		t.Fatalf("DeliverAdtySystemEmail failed: %v", err)
	}
	if row.Status != AdtySystemEmailStatusSent || gotResendKey != "re_bravo_secret_key" {
		t.Fatalf("expected outbox delivery via subsite resend, status=%q key=%q", row.Status, gotResendKey)
	}

	// 6. TestAdtySiteMailConfig validates recipient and sends through saved config.
	if err := TestAdtySiteMailConfig(ctx, 301, "bad;user@example.com"); err == nil {
		t.Fatalf("expected error for multi-recipient test email")
	}
	gotResendKey = ""
	if err := TestAdtySiteMailConfig(ctx, 301, "owner@example.com"); err != nil {
		t.Fatalf("TestAdtySiteMailConfig failed: %v", err)
	}
	if gotResendKey != "re_bravo_secret_key" {
		t.Fatalf("expected TestAdtySiteMailConfig to dispatch via saved resend config, got key=%q", gotResendKey)
	}

	// 7. Official audit redaction scrubs smtp_token and resend_api_key.
	entry, err := model.PrepareOfficialAuditLog(nil, model.OfficialAuditWrite{
		SiteID:     model.OfficialAuditSiteIDPointer(site.Id),
		ObjectType: "site_mail_config",
		ObjectID:   fmt.Sprintf("%d", site.Id),
		Action:     "site.mail_config.update",
		Result:     model.AuditResultSuccess,
		After: map[string]any{
			"provider":       "smtp",
			"smtp_token":     "top-secret-smtp",
			"resend_api_key": "re_top_secret_key",
		},
	})
	if err != nil {
		t.Fatalf("PrepareOfficialAuditLog failed: %v", err)
	}
	if strings.Contains(entry.AfterValue, "top-secret-smtp") || strings.Contains(entry.AfterValue, "re_top_secret_key") {
		t.Fatalf("expected smtp_token and resend_api_key to be redacted, got %s", entry.AfterValue)
	}
}

func TestR37PlatformSMTPFromStripsMainSiteDisplayName(t *testing.T) {
	db := setupR37MailTest(t)
	site := createR37MailTestSite(t, db, 401, "subalpha", "SubSite")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	var (
		mu       sync.Mutex
		mailFrom string
		dataBody string
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
		writeLine := func(line string) {
			_, _ = rw.WriteString(line + "\r\n")
			_ = rw.Flush()
		}
		writeLine("220 mock.smtp.test ESMTP")
		inData := false
		var bodyLines []string
		for {
			line, readErr := rw.ReadString('\n')
			if readErr != nil {
				return
			}
			trimmed := strings.TrimRight(line, "\r\n")
			if inData {
				if trimmed == "." {
					inData = false
					mu.Lock()
					dataBody = strings.Join(bodyLines, "\r\n")
					mu.Unlock()
					writeLine("250 2.0.0 OK")
					continue
				}
				bodyLines = append(bodyLines, trimmed)
				continue
			}
			upper := strings.ToUpper(trimmed)
			switch {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				writeLine("250-mock.smtp.test")
				writeLine("250 OK")
			case strings.HasPrefix(upper, "MAIL FROM:"):
				mu.Lock()
				mailFrom = strings.TrimSpace(trimmed[len("MAIL FROM:"):])
				mu.Unlock()
				writeLine("250 OK")
			case upper == "DATA":
				inData = true
				writeLine("354 Start mail input")
			case upper == "QUIT":
				writeLine("221 Bye")
				return
			default:
				writeLine("250 OK")
			}
		}
	}()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscanf(portStr, "%d", &port)
	require.NoError(t, err)

	common.OptionMapRWMutex.Lock()
	oldOptionMapNil := common.OptionMap == nil
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	oldProvider, hadProvider := common.OptionMap["EmailProvider"]
	oldSystemName := common.SystemName
	oldSMTPServer := common.SMTPServer
	oldSMTPPort := common.SMTPPort
	oldSMTPAccount := common.SMTPAccount
	oldSMTPFrom := common.SMTPFrom
	oldSMTPToken := common.SMTPToken
	oldSMTPSSL := common.SMTPSSLEnabled
	oldSMTPStartTLS := common.SMTPStartTLSEnabled
	oldSMTPForceLogin := common.SMTPForceAuthLogin
	oldSMTPInsecure := common.SMTPInsecureSkipVerify

	common.OptionMap["EmailProvider"] = "smtp"
	common.SystemName = "MainSite System"
	common.SMTPServer = host
	common.SMTPPort = port
	common.SMTPAccount = ""
	common.SMTPFrom = "MainSite <noreply@main.com>"
	common.SMTPToken = ""
	common.SMTPSSLEnabled = false
	common.SMTPStartTLSEnabled = false
	common.SMTPForceAuthLogin = false
	common.SMTPInsecureSkipVerify = false
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.SystemName = oldSystemName
		common.SMTPServer = oldSMTPServer
		common.SMTPPort = oldSMTPPort
		common.SMTPAccount = oldSMTPAccount
		common.SMTPFrom = oldSMTPFrom
		common.SMTPToken = oldSMTPToken
		common.SMTPSSLEnabled = oldSMTPSSL
		common.SMTPStartTLSEnabled = oldSMTPStartTLS
		common.SMTPForceAuthLogin = oldSMTPForceLogin
		common.SMTPInsecureSkipVerify = oldSMTPInsecure
		if oldOptionMapNil {
			common.OptionMap = nil
		} else if hadProvider {
			common.OptionMap["EmailProvider"] = oldProvider
		} else {
			delete(common.OptionMap, "EmailProvider")
		}
		common.OptionMapRWMutex.Unlock()
	})

	// 1. AdtySenderFor strips the main-site display name from common.SMTPFrom.
	sender := AdtySenderFor(&site)
	assert.Equal(t, "SubSite <noreply@main.com>", sender)
	assert.NotContains(t, sender, "MainSite")

	// 2. End-to-end SMTP delivery via resolveSMTPFrom yields the subsite display name, never main-site.
	require.NoError(t, common.SendSMTPEmailFrom(sender, "Verify Email", "customer@example.org", "<p>hello</p>"))
	<-done

	mu.Lock()
	gotMailFrom := mailFrom
	gotBody := dataBody
	mu.Unlock()
	assert.Equal(t, "<noreply@main.com>", gotMailFrom)
	assert.Contains(t, gotBody, `From: "SubSite" <noreply@main.com>`)
	assert.NotContains(t, gotBody, "MainSite")

	// 3. Fallback to common.SMTPAccount also strips any display name cleanly.
	common.OptionMapRWMutex.Lock()
	common.SMTPFrom = "   "
	common.SMTPAccount = "MainAcct <acct@main.com>"
	common.OptionMapRWMutex.Unlock()

	assert.Equal(t, "SubSite <acct@main.com>", AdtySenderFor(&site))
}
