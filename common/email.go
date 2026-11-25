package common

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var outboundURLCredentialsRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s@]+@`)

// SanitizeOutboundError unwraps any *url.Error chain (dropping full request URLs
// and query secrets), redacts embedded URL/proxy userinfo credentials, and strips
// any caller-supplied secret values before surfacing or logging an outbound error (R10-023/R10-039).
func SanitizeOutboundError(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	for {
		var urlErr *url.Error
		if !errors.As(err, &urlErr) || urlErr.Err == nil {
			break
		}
		err = urlErr.Err
	}
	msg := outboundURLCredentialsRe.ReplaceAllString(err.Error(), "${1}[REDACTED]@")
	for _, secret := range secrets {
		trimmed := strings.TrimSpace(secret)
		if trimmed == "" {
			continue
		}
		candidates := []string{secret}
		if trimmed != secret {
			candidates = append(candidates, trimmed)
		}
		for _, s := range candidates {
			msg = strings.ReplaceAll(msg, s, "[REDACTED]")
			msg = strings.ReplaceAll(msg, url.QueryEscape(s), "[REDACTED]")
			msg = strings.ReplaceAll(msg, url.PathEscape(s), "[REDACTED]")
			if len(s) >= 4 {
				msg = strings.ReplaceAll(msg, base64.StdEncoding.EncodeToString([]byte(s)), "[REDACTED]")
				msg = strings.ReplaceAll(msg, base64.RawStdEncoding.EncodeToString([]byte(s)), "[REDACTED]")
			}
		}
	}
	return errors.New(msg)
}

// MailErrorKind classifies outbound mail failures without retaining raw
// provider or transport errors that could embed credentials.
type MailErrorKind string

const (
	MailErrUnknown  MailErrorKind = "unknown"
	MailErrAuth     MailErrorKind = "auth"
	MailErrTLS      MailErrorKind = "tls"
	MailErrTimeout  MailErrorKind = "timeout"
	MailErrConnect  MailErrorKind = "connect"
	MailErrRejected MailErrorKind = "rejected"
)

// SanitizedMailError holds a credential-scrubbed message plus a structured
// failure kind so callers can map user-facing errors without unwrapping raw
// transport or textproto errors.
type SanitizedMailError struct {
	Kind            MailErrorKind
	Msg             string
	DeliveryUnknown bool
}

func (e *SanitizedMailError) Error() string {
	if e == nil {
		return ""
	}
	return e.Msg
}

func (e *SanitizedMailError) Is(target error) bool {
	return e != nil && e.DeliveryUnknown && target == ErrSMTPDeliveryUnknown
}

func classifySMTPErrorKind(err error) MailErrorKind {
	if err == nil {
		return MailErrUnknown
	}
	var tpErr *textproto.Error
	if errors.As(err, &tpErr) {
		switch tpErr.Code {
		case 530, 534, 535, 538:
			return MailErrAuth
		default:
			return MailErrRejected
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return MailErrTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return MailErrTimeout
	}
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var certInvalid x509.CertificateInvalidError
	if errors.As(err, &unknownAuth) || errors.As(err, &hostErr) || errors.As(err, &certInvalid) {
		return MailErrTLS
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "tls:") || strings.Contains(lower, "x509:") || strings.Contains(lower, "starttls"):
		return MailErrTLS
	case strings.Contains(lower, "535") || strings.Contains(lower, "authentication") || strings.Contains(lower, "unauthorized"):
		return MailErrAuth
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out") || strings.Contains(lower, "deadline exceeded"):
		return MailErrTimeout
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "no such host") || strings.Contains(lower, "unreachable") || strings.Contains(lower, "eof"):
		return MailErrConnect
	default:
		return MailErrUnknown
	}
}

func sanitizeSMTPError(err error, account, token string, deliveryUnknown bool) error {
	if err == nil {
		return nil
	}
	var existing *SanitizedMailError
	if errors.As(err, &existing) {
		return err
	}
	kind := classifySMTPErrorKind(err)
	secrets := []string{token}
	if token != "" {
		secrets = append(secrets, "\x00"+account+"\x00"+token)
	}
	sanitized := SanitizeOutboundError(err, secrets...)
	return &SanitizedMailError{
		Kind:            kind,
		Msg:             sanitized.Error(),
		DeliveryUnknown: deliveryUnknown || errors.Is(err, ErrSMTPDeliveryUnknown),
	}
}

// SanitizeSMTPError classifies and scrubs an SMTP transport/auth error before
// logging or returning it across package boundaries.
func SanitizeSMTPError(err error, account, token string) error {
	return sanitizeSMTPError(err, account, token, false)
}

// ErrSMTPDeliveryUnknown marks a lost acknowledgement after DATA was transmitted.
var ErrSMTPDeliveryUnknown = errors.New("SMTP delivery outcome unknown")

func generateMessageID(from string) (string, error) {
	split := strings.Split(from, "@")
	if len(split) < 2 || split[1] == "" {
		return "", fmt.Errorf("invalid SMTP account")
	}
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), GetRandomString(12), split[1]), nil
}

// SMTPTransportConfig specifies explicit SMTP transport parameters for sending
// an email without reading global platform SMTP options.
type SMTPTransportConfig struct {
	Server             string
	Port               int
	Account            string
	Token              string
	From               string
	DisplayName        string
	SSLEnabled         bool
	StartTLSEnabled    bool
	InsecureSkipVerify bool
	ForceAuthLogin     bool
	DialContext        func(ctx context.Context, network, addr string) (net.Conn, error)
}

type smtpConfigSnapshot struct {
	systemName         string
	server             string
	port               int
	account            string
	from               string
	token              string
	sslEnabled         bool
	startTLSEnabled    bool
	forceAuthLogin     bool
	insecureSkipVerify bool
	dialContext        func(ctx context.Context, network, addr string) (net.Conn, error)
}

func snapshotSMTPConfig() smtpConfigSnapshot {
	OptionMapRWMutex.RLock()
	snap := smtpConfigSnapshot{
		systemName:         SystemName,
		server:             SMTPServer,
		port:               SMTPPort,
		account:            SMTPAccount,
		from:               SMTPFrom,
		token:              SMTPToken,
		sslEnabled:         SMTPSSLEnabled,
		startTLSEnabled:    SMTPStartTLSEnabled,
		forceAuthLogin:     SMTPForceAuthLogin,
		insecureSkipVerify: SMTPInsecureSkipVerify,
	}
	OptionMapRWMutex.RUnlock()
	return snap
}

func shouldUseSMTPLoginAuthWithConfig(cfg smtpConfigSnapshot) bool {
	if cfg.forceAuthLogin {
		return true
	}
	return isOutlookServer(cfg.account) || slices.Contains(EmailLoginAuthServerList, cfg.server)
}

func shouldUseSMTPLoginAuth() bool {
	return shouldUseSMTPLoginAuthWithConfig(snapshotSMTPConfig())
}

func getSMTPAuth() smtp.Auth {
	return autoSMTPAuthWithConfig(snapshotSMTPConfig())
}

func shouldAuthenticateSMTP() bool {
	cfg := snapshotSMTPConfig()
	return cfg.account != "" && cfg.token != ""
}

func smtpTLSConfig(cfg smtpConfigSnapshot) *tls.Config {
	return &tls.Config{
		ServerName:         cfg.server,
		InsecureSkipVerify: cfg.insecureSkipVerify, // #nosec G402 -- admin-controlled SMTP compatibility option.
	}
}

const (
	smtpDialTimeout = 15 * time.Second
	smtpIODeadline  = 30 * time.Second
)

func newSMTPClient(addr string, cfg smtpConfigSnapshot) (*smtp.Client, error) {
	dialContext := cfg.dialContext
	if dialContext == nil {
		dialer := &net.Dialer{Timeout: smtpDialTimeout}
		dialContext = dialer.DialContext
	}
	ctx, cancel := context.WithTimeout(context.Background(), smtpDialTimeout)
	defer cancel()

	if cfg.sslEnabled || (cfg.port == 465 && !cfg.startTLSEnabled) {
		rawConn, err := dialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		_ = rawConn.SetDeadline(time.Now().Add(smtpIODeadline))
		tlsConn := tls.Client(rawConn, smtpTLSConfig(cfg))
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = rawConn.Close()
			return nil, err
		}
		client, err := smtp.NewClient(tlsConn, cfg.server)
		if err != nil {
			_ = tlsConn.Close()
			return nil, err
		}
		return client, nil
	}

	conn, err := dialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(smtpIODeadline))
	client, err := smtp.NewClient(conn, cfg.server)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	if cfg.startTLSEnabled {
		startTLSSupported, _ := client.Extension("STARTTLS")
		if !startTLSSupported {
			_ = client.Close()
			return nil, fmt.Errorf("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(smtpTLSConfig(cfg)); err != nil {
			_ = client.Close()
			return nil, err
		}
	}

	return client, nil
}

func resolveSMTPFrom(rawFrom, fallbackAccount, defaultDisplayName, overrideFromHeader string) (envelopeAddr, fromHeader string, err error) {
	candidate := strings.TrimSpace(rawFrom)
	if candidate == "" {
		candidate = strings.TrimSpace(fallbackAccount)
	}
	if strings.ContainsAny(candidate, "\r\n\x00") {
		return "", "", fmt.Errorf("invalid SMTP account")
	}
	envelopeAddr = candidate
	parsedName := ""
	if parsed, parseErr := mail.ParseAddress(candidate); parseErr == nil && parsed != nil {
		envelopeAddr = strings.TrimSpace(parsed.Address)
		parsedName = strings.TrimSpace(parsed.Name)
	}
	if strings.ContainsAny(envelopeAddr, "\r\n\x00<>") {
		return "", "", fmt.Errorf("invalid SMTP account")
	}

	displayName := strings.TrimSpace(defaultDisplayName)
	if displayName == "" {
		displayName = parsedName
	}
	if override := strings.TrimSpace(overrideFromHeader); override != "" {
		if parsedHdr, parseErr := mail.ParseAddress(override); parseErr == nil && parsedHdr != nil && strings.TrimSpace(parsedHdr.Name) != "" {
			displayName = strings.TrimSpace(parsedHdr.Name)
		}
	}
	displayName = strings.NewReplacer("\r", "", "\n", "", "\x00", "").Replace(strings.TrimSpace(displayName))
	fromHeader = (&mail.Address{Name: displayName, Address: envelopeAddr}).String()
	return envelopeAddr, fromHeader, nil
}

func SendEmail(subject string, receiver string, content string) error {
	return SendEmailFrom("", subject, receiver, content)
}

// SendEmailFrom sends with a different sender (adty task 11.3: reseller site
// name and sender domain). An empty from uses the global sender, as
// SendEmail did. Resend uses the whole address; SMTP can only send from the
// global account, so it only takes the display name.
func SendEmailFrom(from string, subject string, receiver string, content string) error {
	OptionMapRWMutex.RLock()
	provider := OptionMap["EmailProvider"]
	resendKey := OptionMap["ResendApiKey"]
	resendFrom := OptionMap["ResendFrom"]
	OptionMapRWMutex.RUnlock()
	if from != "" {
		resendFrom = from
	}
	// One selector controls delivery. Never fall back to a second provider:
	// a failed response does not prove that the first provider did not send.
	switch provider {
	case "resend":
		return sendResendEmail(resendKey, resendFrom, subject, receiver, content)
	case "", "smtp": // Preserve existing installations without a selector.
	default:
		return NewMessage("Email sending method must be SMTP or Resend")
	}
	return SendSMTPEmailFrom(from, subject, receiver, content)
}

// SendSMTPEmailWithConfig sends an email using explicit SMTP transport settings.
// It extracts the pure email address for the SMTP envelope (MAIL FROM) and
// Message-ID even when cfg.From includes a display name, and uses cfg.DialContext
// when provided.
func SendSMTPEmailWithConfig(cfg SMTPTransportConfig, fromHeader, subject, receiver, content string) error {
	snap := smtpConfigSnapshot{
		systemName:         cfg.DisplayName,
		server:             strings.TrimSpace(cfg.Server),
		port:               cfg.Port,
		account:            strings.TrimSpace(cfg.Account),
		from:               strings.TrimSpace(cfg.From),
		token:              cfg.Token,
		sslEnabled:         cfg.SSLEnabled,
		startTLSEnabled:    cfg.StartTLSEnabled,
		forceAuthLogin:     cfg.ForceAuthLogin,
		insecureSkipVerify: cfg.InsecureSkipVerify,
		dialContext:        cfg.DialContext,
	}
	if snap.server == "" && snap.account == "" {
		return NewMessage("SMTP server is not configured")
	}
	if snap.server == "" {
		return NewMessage("SMTP server is not configured")
	}
	if strings.ContainsAny(receiver, "\r\n\x00") {
		return NewMessage("Invalid email address")
	}
	envelopeFrom, formattedFrom, err := resolveSMTPFrom(snap.from, snap.account, cfg.DisplayName, fromHeader)
	if err != nil {
		return err
	}
	id, err2 := generateMessageID(envelopeFrom)
	if err2 != nil {
		return err2
	}
	rawRecipients := strings.Split(receiver, ";")
	to := make([]string, 0, len(rawRecipients))
	for _, item := range rawRecipients {
		addr := strings.TrimSpace(item)
		if addr == "" {
			continue
		}
		to = append(to, addr)
	}
	if len(to) == 0 {
		return NewMessage("Invalid email address")
	}

	encodedSubject := fmt.Sprintf("=?UTF-8?B?%s?=", base64.StdEncoding.EncodeToString([]byte(subject)))
	message := []byte(fmt.Sprintf("To: %s\r\n"+
		"From: %s\r\n"+
		"Subject: %s\r\n"+
		"Date: %s\r\n"+
		"Message-ID: %s\r\n"+ // 添加 Message-ID 头
		"Content-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n",
		strings.Join(to, ", "), formattedFrom, encodedSubject, time.Now().Format(time.RFC1123Z), id, content))
	auth := autoSMTPAuthWithConfig(snap)
	addr := net.JoinHostPort(snap.server, strconv.Itoa(snap.port))
	client, err := newSMTPClient(addr, snap)
	if err != nil {
		return sanitizeSMTPError(err, snap.account, snap.token, false)
	}
	defer client.Close()
	if snap.account != "" && snap.token != "" {
		if err = client.Auth(auth); err != nil {
			return sanitizeSMTPError(err, snap.account, snap.token, false)
		}
	}
	if err = client.Mail(envelopeFrom); err != nil {
		return sanitizeSMTPError(err, snap.account, snap.token, false)
	}
	for _, rcpt := range to {
		if err = client.Rcpt(rcpt); err != nil {
			return sanitizeSMTPError(err, snap.account, snap.token, false)
		}
	}
	w, err := client.Data()
	if err != nil {
		return sanitizeSMTPError(err, snap.account, snap.token, false)
	}
	_, err = w.Write(message)
	if err != nil {
		return sanitizeSMTPError(err, snap.account, snap.token, false)
	}
	err = w.Close()
	if err != nil {
		var rejected *textproto.Error
		if errors.As(err, &rejected) {
			return sanitizeSMTPError(err, snap.account, snap.token, false)
		}
		return sanitizeSMTPError(errors.Join(ErrSMTPDeliveryUnknown, err), snap.account, snap.token, true)
	}
	err = client.Quit()
	if err != nil {
		SysError(LogText("SMTP QUIT failed after delivery acknowledgement: %v", sanitizeSMTPError(err, snap.account, snap.token, false)))
	}
	return nil
}

// SendSMTPEmailFrom uses the transport frozen on a claimed delivery attempt.
func SendSMTPEmailFrom(from, subject, receiver, content string) error {
	snap := snapshotSMTPConfig()
	displayName := snap.systemName
	if parsed, err := mail.ParseAddress(strings.TrimSpace(snap.from)); err == nil && parsed != nil && strings.TrimSpace(parsed.Name) != "" {
		displayName = strings.TrimSpace(parsed.Name)
	}
	return SendSMTPEmailWithConfig(SMTPTransportConfig{
		Server:             snap.server,
		Port:               snap.port,
		Account:            snap.account,
		Token:              snap.token,
		From:               snap.from,
		DisplayName:        displayName,
		SSLEnabled:         snap.sslEnabled,
		StartTLSEnabled:    snap.startTLSEnabled,
		InsecureSkipVerify: snap.insecureSkipVerify,
		ForceAuthLogin:     snap.forceAuthLogin,
	}, from, subject, receiver, content)
}
