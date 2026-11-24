package common

import (
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"slices"
	"strings"
	"time"
)

// ErrSMTPDeliveryUnknown marks a lost acknowledgement after DATA was transmitted.
var ErrSMTPDeliveryUnknown = errors.New("SMTP delivery outcome unknown")

func generateMessageID(from string) (string, error) {
	split := strings.Split(from, "@")
	if len(split) < 2 || split[1] == "" {
		return "", fmt.Errorf("invalid SMTP account")
	}
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), GetRandomString(12), split[1]), nil
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
	dialer := &net.Dialer{Timeout: smtpDialTimeout}
	if cfg.sslEnabled || (cfg.port == 465 && !cfg.startTLSEnabled) {
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, smtpTLSConfig(cfg))
		if err != nil {
			return nil, err
		}
		_ = conn.SetDeadline(time.Now().Add(smtpIODeadline))
		client, err := smtp.NewClient(conn, cfg.server)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return client, nil
	}

	conn, err := dialer.Dial("tcp", addr)
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

// SendSMTPEmailFrom uses the transport frozen on a claimed delivery attempt.
func SendSMTPEmailFrom(from, subject, receiver, content string) error {
	cfg := snapshotSMTPConfig()
	displayName := cfg.systemName
	if parsed, err := mail.ParseAddress(from); err == nil && parsed.Name != "" {
		displayName = parsed.Name
	}
	// Compatibility fallback stays local. Publishing it onto SMTPFrom races with
	// concurrent readers such as adtyPlatformSender while a background send runs.
	smtpFrom := cfg.from
	if smtpFrom == "" {
		smtpFrom = cfg.account
	}
	id, err2 := generateMessageID(smtpFrom)
	if err2 != nil {
		return err2
	}
	if cfg.server == "" && cfg.account == "" {
		return NewMessage("SMTP server is not configured")
	}
	encodedSubject := fmt.Sprintf("=?UTF-8?B?%s?=", base64.StdEncoding.EncodeToString([]byte(subject)))
	mail := []byte(fmt.Sprintf("To: %s\r\n"+
		"From: %s <%s>\r\n"+
		"Subject: %s\r\n"+
		"Date: %s\r\n"+
		"Message-ID: %s\r\n"+ // 添加 Message-ID 头
		"Content-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n",
		receiver, mime.QEncoding.Encode("utf-8", displayName), smtpFrom, encodedSubject, time.Now().Format(time.RFC1123Z), id, content))
	auth := autoSMTPAuthWithConfig(cfg)
	addr := fmt.Sprintf("%s:%d", cfg.server, cfg.port)
	to := strings.Split(receiver, ";")
	var err error
	client, err := newSMTPClient(addr, cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	if cfg.account != "" && cfg.token != "" {
		if err = client.Auth(auth); err != nil {
			return err
		}
	}
	if err = client.Mail(smtpFrom); err != nil {
		return err
	}
	for _, receiver := range to {
		if err = client.Rcpt(receiver); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	_, err = w.Write(mail)
	if err != nil {
		return err
	}
	err = w.Close()
	if err != nil {
		var rejected *textproto.Error
		if errors.As(err, &rejected) {
			return err
		}
		return errors.Join(ErrSMTPDeliveryUnknown, err)
	}
	err = client.Quit()
	if err != nil {
		SysError(LogText("SMTP QUIT failed after delivery acknowledgement: %v", err))
	}
	return nil
}
