package model

const (
	AdtySiteMailProviderPlatform = "platform"
	AdtySiteMailProviderSMTP     = "smtp"
	AdtySiteMailProviderResend   = "resend"
)

// AdtySiteMailConfig stores a subsite's standalone outbound email configuration
// aligned with the main site's Email & Notifications settings (Platform / SMTP / Resend).
// Secrets are stored encrypted via SealAdtyPurposeSecret and never serialized in JSON.
type AdtySiteMailConfig struct {
	SiteId                 int    `json:"site_id" gorm:"primaryKey;autoIncrement:false"`
	Provider               string `json:"provider" gorm:"type:varchar(16);not null;default:platform"`
	SenderName             string `json:"sender_name" gorm:"type:varchar(128)"`
	SMTPServer             string `json:"smtp_server" gorm:"type:varchar(255)"`
	SMTPPort               int    `json:"smtp_port" gorm:"not null;default:587"`
	SMTPAccount            string `json:"smtp_account" gorm:"type:varchar(255)"`
	SMTPFrom               string `json:"smtp_from" gorm:"type:varchar(255)"`
	SMTPSenderName         string `json:"smtp_sender_name" gorm:"type:varchar(128)"`
	SMTPTokenCipher        string `json:"-" gorm:"type:text"`
	SMTPSSLEnabled         bool   `json:"smtp_ssl_enabled" gorm:"not null;default:false"`
	SMTPStartTLSEnabled    bool   `json:"smtp_starttls_enabled" gorm:"not null;default:false"`
	SMTPInsecureSkipVerify bool   `json:"smtp_insecure_skip_verify" gorm:"not null;default:false"`
	SMTPForceAuthLogin     bool   `json:"smtp_force_auth_login" gorm:"not null;default:false"`
	ResendFrom             string `json:"resend_from" gorm:"type:varchar(255)"`
	ResendSenderName       string `json:"resend_sender_name" gorm:"type:varchar(128)"`
	ResendApiKeyCipher     string `json:"-" gorm:"type:text"`
	UpdatedAt              int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (AdtySiteMailConfig) TableName() string { return "adty_site_mail_configs" }
