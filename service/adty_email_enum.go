package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 防邮箱枚举（任务 7.3）及系统事务邮件可靠投递与重试（A.22.2 / ISS-R1-0114）。

const (
	AdtySystemEmailStatusPending = "pending"
	AdtySystemEmailStatusSending = "sending"
	AdtySystemEmailStatusSent    = "sent"
	AdtySystemEmailStatusFailed  = "failed"
	AdtySystemEmailStatusExpired = "expired"
	AdtySystemEmailStatusDead    = "dead"

	AdtySystemEmailCategoryVerification       = "register_verification"
	AdtySystemEmailCategoryRegisteredReminder = "registered_reminder"
	AdtySystemEmailCategoryPasswordReset      = "password_reset"
	AdtySystemEmailCategoryEmailBinding       = "email_binding"
	AdtySystemEmailCategorySecurityNotice     = "security_notice"
	AdtySystemEmailCategoryOrderNotice        = "order_notice"

	AdtySystemEmailMaxAttempts  = 5
	adtySystemEmailCipherPrefix = "adty:mail:v1:"
)

var (
	ErrAdtySystemEmailMarketingRetired   = errors.New("retired marketing email category is not permitted")
	ErrAdtySystemEmailCategoryInvalid    = errors.New("system email category is invalid")
	ErrAdtySystemEmailRecipientInvalid   = errors.New("system email recipient is invalid")
	ErrAdtySystemEmailRecipientForbidden = errors.New("system email recipient is not permitted for this account or site")
)

// AdtySystemEmailDelivery persists one transactional system email in the outbox
// so delivery failures never roll back committed business state, retain failure
// status and retry records, and survive process restarts without losing pending tasks.
// Plaintext tokens/codes are never stored in ContentRedacted; BodyCipher is encrypted
// and wiped once the row reaches a terminal state (sent, expired, dead).
type AdtySystemEmailDelivery struct {
	Id              int64  `json:"id" gorm:"primaryKey"`
	EventKey        string `json:"event_key" gorm:"type:varchar(128);index"`
	Category        string `json:"category" gorm:"type:varchar(40);not null;index"`
	SiteId          int    `json:"site_id" gorm:"index"`
	UserId          int    `json:"user_id" gorm:"index"`
	Language        string `json:"language" gorm:"type:varchar(16)"`
	Sender          string `json:"sender" gorm:"type:varchar(255)"`
	Recipient       string `json:"recipient" gorm:"type:varchar(255);not null;index"`
	Subject         string `json:"subject" gorm:"type:varchar(255);not null"`
	ContentRedacted string `json:"content_redacted" gorm:"type:text"`
	BodyCipher      string `json:"-" gorm:"type:text"`
	Status          string `json:"status" gorm:"type:varchar(24);not null;index"`
	Attempts        int    `json:"attempts"`
	LastError       string `json:"last_error" gorm:"type:text"`
	CreatedAt       int64  `json:"created_at" gorm:"index"`
	ExpiresAt       int64  `json:"expires_at" gorm:"index"`
	NextRetryAt     int64  `json:"next_retry_at" gorm:"index"`
	SentAt          int64  `json:"sent_at"`
	UpdatedAt       int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (AdtySystemEmailDelivery) TableName() string {
	return "adty_system_email_deliveries"
}

type AdtySystemEmailRequest struct {
	EventKey       string
	Category       string
	SiteID         int
	UserID         int
	Language       string
	Sender         string
	Recipient      string
	AllowedAddress string
	Subject        string
	Content        string
	TTL            time.Duration
	Now            time.Time
}

var (
	adtySystemEmailRedactionPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(token=)` + `[^"&<\s]+`),
		regexp.MustCompile(`(?i)(code=)` + `[^"&<\s]+`),
		regexp.MustCompile(`(?i)(Bearer\s+)[A-Za-z0-9._-]+`),
	}
	adtySystemEmailKeyPattern  = regexp.MustCompile(`sk-[A-Za-z0-9_-]{6,}`)
	adtySystemEmailCodePattern = regexp.MustCompile(`\b\d{6}\b`)
	// Registration codes may contain hex letters and are rendered as standalone text.
	adtySystemEmailHexCodeText = regexp.MustCompile(`(?i)(>\s*)[0-9a-f]{6}(\s*<)`)

	defaultAdtySystemEmailSender = func(from, subject, to, content string) error {
		if strings.TrimSpace(from) == "" {
			return common.SendEmail(subject, to, content)
		}
		return common.SendEmailFrom(from, subject, to, content)
	}

	// AdtySystemEmailSender performs the underlying SMTP/Resend transport call.
	// Tests may replace it to inject provider failures or inspect deliveries.
	AdtySystemEmailSender = defaultAdtySystemEmailSender

	// AdtySystemEmailAsyncDispatch controls whether async enqueue immediately
	// schedules a background delivery attempt after persisting the outbox row.
	// Setting false in tests simulates a process exit right after enqueueing.
	AdtySystemEmailAsyncDispatch = true

	adtySystemEmailAsyncWG  sync.WaitGroup
	adtySystemEmailTableMu  sync.Mutex
	adtySystemEmailMigrated = map[*gorm.DB]bool{}
)

// RedactAdtySystemEmailContent strips verification codes, reset tokens, and API
// secrets so persisted evidence never retains usable credentials.
func RedactAdtySystemEmailContent(content string) string {
	redacted := content
	for _, pat := range adtySystemEmailRedactionPatterns {
		redacted = pat.ReplaceAllString(redacted, "${1}[REDACTED]")
	}
	redacted = adtySystemEmailKeyPattern.ReplaceAllString(redacted, "sk-[REDACTED]")
	redacted = adtySystemEmailHexCodeText.ReplaceAllString(redacted, "${1}[REDACTED_CODE]${2}")
	redacted = adtySystemEmailCodePattern.ReplaceAllString(redacted, "[REDACTED_CODE]")
	return redacted
}

func adtySystemEmailAEAD() (cipher.AEAD, error) {
	seed := "adty-system-email-v1:" + os.Getenv("CRYPTO_SECRET") + ":" + common.SessionSecret
	sum := sha256.Sum256([]byte(seed))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealAdtySystemEmailBody(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	aead, err := adtySystemEmailAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(plain), []byte("adty:system_email_body"))
	return adtySystemEmailCipherPrefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func openAdtySystemEmailBody(stored string) (string, error) {
	if stored == "" {
		return "", errors.New("empty email cipher")
	}
	encoded, ok := strings.CutPrefix(stored, adtySystemEmailCipherPrefix)
	if !ok {
		return "", errors.New("invalid email cipher prefix")
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	aead, err := adtySystemEmailAEAD()
	if err != nil {
		return "", err
	}
	if len(raw) < aead.NonceSize() {
		return "", errors.New("truncated email cipher")
	}
	nonce, ciphertext := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ciphertext, []byte("adty:system_email_body"))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func ensureAdtySystemEmailTable(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	adtySystemEmailTableMu.Lock()
	defer adtySystemEmailTableMu.Unlock()
	if adtySystemEmailMigrated[db] {
		return nil
	}
	if !db.Migrator().HasTable(&AdtySystemEmailDelivery{}) {
		if err := db.AutoMigrate(&AdtySystemEmailDelivery{}); err != nil {
			return err
		}
	}
	adtySystemEmailMigrated[db] = true
	return nil
}

func isRetiredMarketingEmail(category, subject, content string) bool {
	cat := strings.ToLower(strings.TrimSpace(category))
	switch cat {
	case "marketing", "campaign", "broadcast", "newsletter", "promo", "promotion":
		return true
	}
	lowerContent := strings.ToLower(content)
	lowerSubject := strings.ToLower(subject)
	return strings.Contains(lowerContent, "list-unsubscribe") || strings.Contains(lowerSubject, "[marketing]")
}

func isAllowedAdtySystemEmailCategory(category string) bool {
	switch category {
	case AdtySystemEmailCategoryVerification,
		AdtySystemEmailCategoryRegisteredReminder,
		AdtySystemEmailCategoryPasswordReset,
		AdtySystemEmailCategoryEmailBinding,
		AdtySystemEmailCategorySecurityNotice,
		AdtySystemEmailCategoryOrderNotice:
		return true
	}
	return false
}

func inferAdtySystemEmailCategory(subject, content string) string {
	lowerSub := strings.ToLower(subject)
	lowerBody := strings.ToLower(content)
	switch {
	case strings.Contains(lowerBody, "/user/reset") || strings.Contains(lowerSub, "reset") || strings.Contains(subject, "重置"):
		return AdtySystemEmailCategoryPasswordReset
	case strings.Contains(lowerBody, "sign in or reset your password") || strings.Contains(content, "直接登录或找回密码"):
		return AdtySystemEmailCategoryRegisteredReminder
	case strings.Contains(lowerSub, "binding") || strings.Contains(subject, "邮箱绑定"):
		return AdtySystemEmailCategoryEmailBinding
	case strings.Contains(lowerSub, "security") || strings.Contains(subject, "安全"):
		return AdtySystemEmailCategorySecurityNotice
	case strings.Contains(lowerSub, "verification") || strings.Contains(subject, "验证码"):
		return AdtySystemEmailCategoryVerification
	default:
		return AdtySystemEmailCategoryOrderNotice
	}
}

func inferAdtySystemEmailLanguage(explicitLang string, userID int, subject, content string) string {
	if strings.TrimSpace(explicitLang) != "" {
		return i18n.OfficialLanguage(explicitLang)
	}
	if userID > 0 && model.DB != nil && model.DB.Migrator().HasTable(&model.User{}) {
		if lang := model.GetUserLanguage(userID); lang != "" {
			return i18n.OfficialLanguage(lang)
		}
	}
	for _, r := range subject + content {
		if unicode.Is(unicode.Han, r) {
			return "zh-CN"
		}
	}
	return "en"
}

func defaultAdtySystemEmailTTL(category string) time.Duration {
	validMinutes := common.VerificationValidMinutes
	if validMinutes <= 0 {
		validMinutes = 10
	}
	switch category {
	case AdtySystemEmailCategoryVerification, AdtySystemEmailCategoryRegisteredReminder, AdtySystemEmailCategoryPasswordReset:
		return time.Duration(validMinutes) * time.Minute
	case AdtySystemEmailCategoryEmailBinding:
		return model.EmailBindingTTL
	default:
		return 24 * time.Hour
	}
}

func validateAdtySystemEmailRecipient(db *gorm.DB, req AdtySystemEmailRequest) (string, error) {
	raw := strings.TrimSpace(req.Recipient)
	if raw == "" || strings.ContainsAny(raw, "\r\n") {
		return "", ErrAdtySystemEmailRecipientInvalid
	}
	to := model.NormalizeEmail(raw)
	if !strings.Contains(to, "@") || strings.HasPrefix(to, "@") || strings.HasSuffix(to, "@") {
		return "", ErrAdtySystemEmailRecipientInvalid
	}
	if req.Category == AdtySystemEmailCategoryVerification {
		validated, err := ValidateAccountEmail(to)
		if err != nil {
			return "", ErrAdtySystemEmailRecipientInvalid
		}
		to = validated
		if db != nil && db.Migrator().HasTable(&model.User{}) && model.IsEmailAlreadyTaken(to, req.SiteID) {
			return "", ErrAdtySystemEmailRecipientForbidden
		}
	}
	if req.UserID > 0 && db != nil && db.Migrator().HasTable(&model.User{}) {
		var user model.User
		if err := db.Where("id = ?", req.UserID).First(&user).Error; err != nil {
			return "", ErrAdtySystemEmailRecipientForbidden
		}
		if user.Status != common.UserStatusEnabled || user.SiteId != req.SiteID {
			return "", ErrAdtySystemEmailRecipientForbidden
		}
		allowed := map[string]bool{}
		if primary := model.NormalizeEmail(user.Email); primary != "" {
			allowed[primary] = true
		}
		if req.Category == AdtySystemEmailCategoryOrderNotice {
			if notify := model.NormalizeEmail(user.GetSetting().NotificationEmail); notify != "" {
				allowed[notify] = true
			}
		}
		if req.Category == AdtySystemEmailCategoryEmailBinding || req.Category == AdtySystemEmailCategorySecurityNotice {
			if extra := model.NormalizeEmail(req.AllowedAddress); extra != "" {
				allowed[extra] = true
			}
		}
		if !allowed[to] {
			return "", ErrAdtySystemEmailRecipientForbidden
		}
	}
	return to, nil
}

// EnqueueAdtySystemEmail validates and persists a transactional system email in
// the outbox with status=pending before any network delivery attempt.
func EnqueueAdtySystemEmail(req AdtySystemEmailRequest) (*AdtySystemEmailDelivery, error) {
	return enqueueAdtySystemEmailOnDB(model.DB, req)
}

func enqueueAdtySystemEmailOnDB(db *gorm.DB, req AdtySystemEmailRequest) (*AdtySystemEmailDelivery, error) {
	if isRetiredMarketingEmail(req.Category, req.Subject, req.Content) {
		return nil, ErrAdtySystemEmailMarketingRetired
	}
	category := strings.TrimSpace(req.Category)
	if category == "" {
		category = inferAdtySystemEmailCategory(req.Subject, req.Content)
	}
	if !isAllowedAdtySystemEmailCategory(category) {
		return nil, ErrAdtySystemEmailCategoryInvalid
	}
	req.Category = category
	to, err := validateAdtySystemEmailRecipient(db, req)
	if err != nil {
		return nil, err
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = defaultAdtySystemEmailTTL(category)
	}
	lang := inferAdtySystemEmailLanguage(req.Language, req.UserID, req.Subject, req.Content)
	sender := strings.TrimSpace(req.Sender)
	if sender == "" && req.SiteID > 0 {
		if site := AdtySiteById(req.SiteID); site != nil {
			sender = AdtySenderFor(site)
		}
	}
	sealed, err := sealAdtySystemEmailBody(req.Content)
	if err != nil {
		return nil, err
	}
	row := &AdtySystemEmailDelivery{
		EventKey:        strings.TrimSpace(req.EventKey),
		Category:        category,
		SiteId:          req.SiteID,
		UserId:          req.UserID,
		Language:        lang,
		Sender:          sender,
		Recipient:       to,
		Subject:         req.Subject,
		ContentRedacted: RedactAdtySystemEmailContent(req.Content),
		BodyCipher:      sealed,
		Status:          AdtySystemEmailStatusPending,
		CreatedAt:       now.Unix(),
		ExpiresAt:       now.Add(ttl).Unix(),
		NextRetryAt:     now.Unix(),
	}
	if db != nil {
		if err := ensureAdtySystemEmailTable(db); err != nil {
			return nil, err
		}
		if row.EventKey != "" {
			var existing AdtySystemEmailDelivery
			if db.Where("event_key = ?", row.EventKey).Limit(1).Find(&existing).RowsAffected > 0 {
				return &existing, nil
			}
		}
		if err := db.Create(row).Error; err != nil {
			return nil, err
		}
	}
	return row, nil
}

// DeliverAdtySystemEmail enqueues a system email and immediately attempts one
// delivery. Delivery errors are recorded on the outbox row as retryable (failed)
// so callers can preserve committed business state while keeping a retry trail.
func DeliverAdtySystemEmail(req AdtySystemEmailRequest) (*AdtySystemEmailDelivery, error) {
	row, err := enqueueAdtySystemEmailOnDB(model.DB, req)
	if err != nil {
		return nil, err
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	sendErr := deliverAdtySystemEmailRow(model.DB, row, now)
	return row, sendErr
}

func adtyUsesDefaultSystemEmailSender() bool {
	if AdtySystemEmailSender == nil {
		return false
	}
	ptr := reflect.ValueOf(AdtySystemEmailSender).Pointer()
	return ptr == reflect.ValueOf(defaultAdtySystemEmailSender).Pointer() ||
		ptr == reflect.ValueOf(common.SendEmailFrom).Pointer()
}

func deliverAdtySystemEmailRow(db *gorm.DB, row *AdtySystemEmailDelivery, now time.Time) error {
	if row == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	if row.ExpiresAt > 0 && now.Unix() >= row.ExpiresAt {
		row.Status = AdtySystemEmailStatusExpired
		row.LastError = "expired before delivery"
		row.BodyCipher = ""
		if db != nil && row.Id > 0 {
			_ = db.Model(row).Updates(map[string]any{
				"status":      row.Status,
				"last_error":  row.LastError,
				"body_cipher": "",
			}).Error
		}
		return errors.New(row.LastError)
	}
	content, err := openAdtySystemEmailBody(row.BodyCipher)
	if err != nil {
		row.Status = AdtySystemEmailStatusDead
		row.LastError = "email body unavailable: " + err.Error()
		row.BodyCipher = ""
		if db != nil && row.Id > 0 {
			_ = db.Model(row).Updates(map[string]any{
				"status":      row.Status,
				"last_error":  row.LastError,
				"body_cipher": "",
			}).Error
		}
		return err
	}
	if db != nil && row.Id > 0 {
		claim := db.Model(&AdtySystemEmailDelivery{}).
			Where("id = ? AND status IN ?", row.Id, []string{AdtySystemEmailStatusPending, AdtySystemEmailStatusFailed}).
			Updates(map[string]any{
				"status":     AdtySystemEmailStatusSending,
				"updated_at": now.Unix(),
			})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			_ = db.First(row, row.Id).Error
			return nil
		}
	}
	attempts := row.Attempts + 1
	var sendErr error
	if row.SiteId > 0 {
		var site *model.AdtySite
		if db != nil && db.Migrator().HasTable(&model.AdtySite{}) {
			// R8-16: Read authoritative site status directly from DB instead of
			// the 60s local cache so closed/deleting sites are stopped immediately across replicas.
			var siteRow model.AdtySite
			if findErr := db.Where("id = ?", row.SiteId).First(&siteRow).Error; findErr == nil {
				site = &siteRow
			} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
				sendErr = findErr
			}
		} else {
			site = AdtySiteById(row.SiteId)
		}
		if sendErr == nil {
			if site == nil || site.Status == model.AdtySiteStatusDeleting || site.Status == model.AdtySiteStatusClosed || site.Status == model.AdtySiteStatusPending {
				// R7-27: Never fall back to the platform default sender when a subsite has
				// been hard-deleted, is deleting, or is closed.
				row.Status = AdtySystemEmailStatusDead
				row.LastError = "site unavailable"
				row.BodyCipher = ""
				if db != nil && row.Id > 0 {
					_ = db.Model(&AdtySystemEmailDelivery{}).
						Where("id = ? AND status = ?", row.Id, AdtySystemEmailStatusSending).
						Updates(map[string]any{
							"status":      row.Status,
							"last_error":  row.LastError,
							"body_cipher": "",
							"updated_at":  now.Unix(),
						}).Error
				}
				return nil
			}
			if adtyUsesDefaultSystemEmailSender() {
				sendErr = AdtySendSiteEmail(site, row.Subject, row.Recipient, content)
			} else {
				sendErr = AdtySystemEmailSender(row.Sender, row.Subject, row.Recipient, content)
			}
		}
	} else {
		sendErr = AdtySystemEmailSender(row.Sender, row.Subject, row.Recipient, content)
	}
	row.Attempts = attempts
	if sendErr == nil {
		row.Status = AdtySystemEmailStatusSent
		row.SentAt = now.Unix()
		row.LastError = ""
		row.BodyCipher = ""
		if db != nil && row.Id > 0 {
			_ = db.Model(&AdtySystemEmailDelivery{}).
				Where("id = ? AND status = ?", row.Id, AdtySystemEmailStatusSending).
				Updates(map[string]any{
					"status":      row.Status,
					"attempts":    row.Attempts,
					"sent_at":     row.SentAt,
					"last_error":  "",
					"body_cipher": "",
					"updated_at":  now.Unix(),
				}).Error
		}
		return nil
	}
	common.SysError(common.LogText("send email to %s failed: %s", row.Recipient, sendErr.Error()))
	row.LastError = sendErr.Error()
	row.NextRetryAt = now.Add(time.Duration(attempts) * time.Minute).Unix()
	if attempts >= AdtySystemEmailMaxAttempts {
		row.Status = AdtySystemEmailStatusDead
		row.BodyCipher = ""
	} else {
		row.Status = AdtySystemEmailStatusFailed
	}
	if db != nil && row.Id > 0 {
		_ = db.Model(&AdtySystemEmailDelivery{}).
			Where("id = ? AND status = ?", row.Id, AdtySystemEmailStatusSending).
			Updates(map[string]any{
				"status":        row.Status,
				"attempts":      row.Attempts,
				"last_error":    row.LastError,
				"next_retry_at": row.NextRetryAt,
				"body_cipher":   row.BodyCipher,
				"updated_at":    now.Unix(),
			}).Error
	}
	return sendErr
}

// RetryAdtySystemEmails scans pending, failed, and interrupted sending rows in
// the system email outbox, marks expired items as expired without sending, and
// retries eligible items up to AdtySystemEmailMaxAttempts.
func RetryAdtySystemEmails(now time.Time) (int, error) {
	db := model.DB
	if db == nil {
		return 0, nil
	}
	if err := ensureAdtySystemEmailTable(db); err != nil {
		return 0, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	// R8-16: Only recover stale "sending" rows whose in-flight lease timed out
	// (>= 120s) so concurrent workers cannot double-send active deliveries.
	_ = db.Model(&AdtySystemEmailDelivery{}).
		Where("status = ? AND updated_at <= ?", AdtySystemEmailStatusSending, now.Unix()-120).
		Updates(map[string]any{
			"status":     AdtySystemEmailStatusPending,
			"updated_at": now.Unix(),
		}).Error

	// R9-15: Filter eligible pending, due failed, and expired failed rows in SQL
	// so backed-off failures for one tenant cannot head-of-line block LIMIT 50.
	nowUnix := now.Unix()
	var rows []AdtySystemEmailDelivery
	if err := db.Where(
		"status = ? OR (status = ? AND (next_retry_at <= ? OR (expires_at > 0 AND expires_at <= ?)))",
		AdtySystemEmailStatusPending,
		AdtySystemEmailStatusFailed,
		nowUnix,
		nowUnix,
	).Order("id").Limit(50).Find(&rows).Error; err != nil {
		return 0, err
	}
	processed := 0
	for i := range rows {
		row := rows[i]
		if row.Status == AdtySystemEmailStatusFailed && row.NextRetryAt > now.Unix() && (row.ExpiresAt == 0 || now.Unix() < row.ExpiresAt) {
			continue
		}
		_ = deliverAdtySystemEmailRow(db, &row, now)
		processed++
	}
	return processed, nil
}

// WaitAdtySystemEmailAsync waits for in-flight async system email dispatches in tests.
func WaitAdtySystemEmailAsync() {
	adtySystemEmailAsyncWG.Wait()
}

func enqueueAndDispatchAdtySystemEmail(siteID int, from, subject, to, content string) {
	db := model.DB
	row, err := enqueueAdtySystemEmailOnDB(db, AdtySystemEmailRequest{
		SiteID:    siteID,
		Language:  i18n.LangEn,
		Sender:    from,
		Recipient: to,
		Subject:   subject,
		Content:   content,
	})
	if err != nil {
		common.SysError(common.LogText("enqueue email to %s failed: %s", to, err.Error()))
		return
	}
	if !AdtySystemEmailAsyncDispatch {
		return
	}
	adtySystemEmailAsyncWG.Add(1)
	gopool.Go(func() {
		defer adtySystemEmailAsyncWG.Done()
		_ = deliverAdtySystemEmailRow(db, row, time.Now())
	})
}

func startAdtySystemEmailDeliveryTask() {
	go func() {
		_, _ = RetryAdtySystemEmails(time.Now())
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for now := range ticker.C {
			if _, err := RetryAdtySystemEmails(now); err != nil {
				common.SysError(common.LogText("system email delivery task: %s", err.Error()))
			}
		}
	}()
}

func init() {
	adtyMasterTasks = append(adtyMasterTasks, startAdtySystemEmailDeliveryTask)
}

// AdtyReminderInterval 是同一个邮箱两封提醒之间的最短间隔；测试可以改短。
var AdtyReminderInterval = 10 * time.Minute

var adtyReminderSent = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

var defaultAdtySendEmailAsync = func(subject, to, content string) {
	enqueueAndDispatchAdtySystemEmail(0, "", subject, to, content)
}

var defaultAdtySendFromAsync = func(from, subject, to, content string) {
	enqueueAndDispatchAdtySystemEmail(0, from, subject, to, content)
}

// AdtySendEmailAsync 在后台发信，先持久化 outbox 记录，失败保留可重试状态。发信的耗时不再暴露邮箱是否存在。
// 测试把它换成同步记录的函数。
var AdtySendEmailAsync = defaultAdtySendEmailAsync

// AdtySendSiteEmailAsync 同 AdtySendEmailAsync，发件人按站点（任务 11.3）；site 为 nil 时就是 AdtySendEmailAsync。
// 测试把 AdtySendEmailAsync 换掉时这里也跟着走同一个记录函数，发件人另由 AdtySenderFor 测。
var AdtySendSiteEmailAsync = func(site *model.AdtySite, subject, to, content string) {
	if site == nil {
		AdtySendEmailAsync(subject, to, content)
		return
	}
	from := AdtySenderFor(site)
	if adtySendFromAsync != nil && reflect.ValueOf(adtySendFromAsync).Pointer() != reflect.ValueOf(defaultAdtySendFromAsync).Pointer() {
		adtySendFromAsync(from, subject, to, content)
		return
	}
	enqueueAndDispatchAdtySystemEmail(site.Id, from, subject, to, content)
}

// adtySendFromAsync 是带发件人的异步发信；测试里换成记录函数。
var adtySendFromAsync = defaultAdtySendFromAsync

// AdtySendRegisteredReminder 给已注册的邮箱发「如果你在本站有账号，请直接登录或找回密码」。
// 10 分钟内同一邮箱只发一封；返回是否真的发了（只给测试看，调用方的响应不能因此不同）。
func AdtySendRegisteredReminder(c *gin.Context, email string) bool {
	key := strings.ToLower(strings.TrimSpace(email))
	site := AdtySiteForRequest(c)
	siteID := 0
	if site != nil {
		siteID = site.Id
	}
	throttleKey := model.SiteEmailCodeKey(key, siteID)
	now := time.Now()
	adtyReminderSent.Lock()
	if last, ok := adtyReminderSent.at[throttleKey]; ok && now.Sub(last) < AdtyReminderInterval {
		adtyReminderSent.Unlock()
		return false
	}
	adtyReminderSent.at[throttleKey] = now
	for addr, at := range adtyReminderSent.at {
		if now.Sub(at) >= AdtyReminderInterval {
			delete(adtyReminderSent.at, addr)
		}
	}
	adtyReminderSent.Unlock()

	subject := "Sign-in help for " + AdtyBrandFor(site).Name
	content := AdtyRenderMail(site, AdtyMailMessage{
		Title:      "Sign-in help",
		Intro:      "Someone requested a sign-up code for this email address.",
		Paragraphs: []string{"If you already have an account, sign in or reset your password instead."},
		Footnote:   "If you did not request a code, ignore this email.",
	})
	AdtySendSiteEmailAsync(site, subject, key, content)
	return true
}

// ResetAdtyReminderThrottle 清空限频记录，只给测试用。
func ResetAdtyReminderThrottle() {
	adtyReminderSent.Lock()
	adtyReminderSent.at = map[string]time.Time{}
	adtyReminderSent.Unlock()
}
