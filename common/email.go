package common

import (
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
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

func shouldUseSMTPLoginAuth() bool {
	if SMTPForceAuthLogin {
		return true
	}
	return isOutlookServer(SMTPAccount) || slices.Contains(EmailLoginAuthServerList, SMTPServer)
}

func getSMTPAuth() smtp.Auth {
	return AutoSMTPAuth(SMTPAccount, SMTPToken)
}

func shouldAuthenticateSMTP() bool {
	return SMTPAccount != "" && SMTPToken != ""
}

func smtpTLSConfig() *tls.Config {
	return &tls.Config{
		ServerName:         SMTPServer,
		InsecureSkipVerify: SMTPInsecureSkipVerify, // #nosec G402 -- admin-controlled SMTP compatibility option.
	}
}

func newSMTPClient(addr string) (*smtp.Client, error) {
	if SMTPSSLEnabled || (SMTPPort == 465 && !SMTPStartTLSEnabled) {
		conn, err := tls.Dial("tcp", addr, smtpTLSConfig())
		if err != nil {
			return nil, err
		}
		client, err := smtp.NewClient(conn, SMTPServer)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return client, nil
	}

	client, err := smtp.Dial(addr)
	if err != nil {
		return nil, err
	}

	if SMTPStartTLSEnabled {
		startTLSSupported, _ := client.Extension("STARTTLS")
		if !startTLSSupported {
			_ = client.Close()
			return nil, fmt.Errorf("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(smtpTLSConfig()); err != nil {
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
	displayName := SystemName
	if from != "" {
		resendFrom = from
		if parsed, err := mail.ParseAddress(from); err == nil && parsed.Name != "" {
			displayName = parsed.Name
		}
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
	// Compatibility fallback stays local. Publishing it onto SMTPFrom races with
	// concurrent readers such as adtyPlatformSender while a background send runs.
	smtpFrom := SMTPFrom
	if smtpFrom == "" {
		smtpFrom = SMTPAccount
	}
	id, err2 := generateMessageID(smtpFrom)
	if err2 != nil {
		return err2
	}
	if SMTPServer == "" && SMTPAccount == "" {
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
	auth := getSMTPAuth()
	addr := fmt.Sprintf("%s:%d", SMTPServer, SMTPPort)
	to := strings.Split(receiver, ";")
	var err error
	client, err := newSMTPClient(addr)
	if err != nil {
		return err
	}
	defer client.Close()
	if shouldAuthenticateSMTP() {
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
