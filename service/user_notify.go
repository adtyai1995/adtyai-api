package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"gorm.io/gorm/clause"
)

const adtyChannelWatcherRetryTaskType = "adty_channel_watcher_retry"

const (
	AdtyWatcherOutboxPending   = "pending"
	AdtyWatcherOutboxDelivered = "delivered"
	AdtyWatcherOutboxFailed    = "failed"
	AdtyWatcherOutboxRevoked   = "revoked"
)

// AdtyChannelWatcherNotice persists the authoritative in-site notification history
// for channel/model/group watcher events (A.18.1 / A.18.3 / ISS-R1-0179).
type AdtyChannelWatcherNotice struct {
	Id         int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	EventId    string `json:"event_id" gorm:"type:varchar(128);not null;uniqueIndex:idx_adty_watcher_notice_event_user,priority:1;index"`
	UserId     int    `json:"user_id" gorm:"not null;uniqueIndex:idx_adty_watcher_notice_event_user,priority:2;index"`
	NotifyType string `json:"notify_type" gorm:"type:varchar(128);not null;index"`
	Category   string `json:"category" gorm:"type:varchar(64);not null;default:'model_group_anomaly'"`
	Language   string `json:"language" gorm:"type:varchar(32);not null;default:''"`
	Title      string `json:"title" gorm:"type:varchar(500);not null"`
	Content    string `json:"content" gorm:"type:text"`
	ReadKey    string `json:"read_key" gorm:"type:varchar(128);not null;index"`
	CreatedAt  int64  `json:"created_at" gorm:"not null;default:0"`
}

func (AdtyChannelWatcherNotice) TableName() string {
	return "adty_channel_watcher_notices"
}

// AdtyChannelWatcherOutbox persists the per-recipient durable delivery state and
// failure queue for channel/model watcher events, keyed idempotently by (event_id, user_id).
type AdtyChannelWatcherOutbox struct {
	Id           int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	EventId      string `json:"event_id" gorm:"type:varchar(128);not null;uniqueIndex:idx_adty_watcher_outbox_event_user,priority:1;index"`
	UserId       int    `json:"user_id" gorm:"not null;uniqueIndex:idx_adty_watcher_outbox_event_user,priority:2;index"`
	NotifyType   string `json:"notify_type" gorm:"type:varchar(128);not null;index"`
	Category     string `json:"category" gorm:"type:varchar(64);not null;default:'model_group_anomaly'"`
	Language     string `json:"language" gorm:"type:varchar(32);not null;default:''"`
	Title        string `json:"title" gorm:"type:varchar(500);not null"`
	Content      string `json:"content" gorm:"type:text"`
	EmailTitle   string `json:"-" gorm:"type:text"`
	EmailContent string `json:"-" gorm:"type:text"`
	Status       string `json:"status" gorm:"type:varchar(32);not null;default:'pending';index"`
	AttemptCount int    `json:"attempt_count" gorm:"not null;default:0"`
	LastError    string `json:"last_error" gorm:"type:varchar(1000);not null;default:''"`
	NextRetryAt  int64  `json:"next_retry_at" gorm:"not null;default:0;index"`
	CreatedAt    int64  `json:"created_at" gorm:"not null;default:0"`
	UpdatedAt    int64  `json:"updated_at" gorm:"not null;default:0"`
	DeliveredAt  int64  `json:"delivered_at" gorm:"not null;default:0"`
}

func (AdtyChannelWatcherOutbox) TableName() string {
	return "adty_channel_watcher_outboxes"
}

func ensureAdtyChannelWatcherTables() {
	if model.DB == nil {
		return
	}
	if !model.DB.Migrator().HasTable(&AdtyChannelWatcherNotice{}) || !model.DB.Migrator().HasTable(&AdtyChannelWatcherOutbox{}) {
		_ = model.DB.AutoMigrate(&AdtyChannelWatcherNotice{}, &AdtyChannelWatcherOutbox{})
	}
}

func receivesChannelWatcherNotice(userID, role int, setting dto.UserSetting) bool {
	// 二开：没有渠道·查看的管理员不收渠道通知（任务 2.6）
	if !AdtyAdminRoleCan(userID, role, authz.ChannelRead) {
		return false
	}
	if setting.ChannelNotifyEnabled != nil {
		return *setting.ChannelNotifyEnabled
	}
	return role == common.RoleRootUser
}

func receivesUpstreamModelUpdateNotice(userID, role int, setting dto.UserSetting) bool {
	return setting.UpstreamModelUpdateNotifyEnabled && AdtyAdminRoleCan(userID, role, authz.ChannelRead)
}

func deriveWatcherEventID(notifyType string, render func(lang string) (string, string)) string {
	defSubj, defBody := "", ""
	if render != nil {
		defSubj, defBody = render("")
	}
	sum := sha256.Sum256([]byte(notifyType + "\x00" + defSubj + "\x00" + defBody))
	baseHash := hex.EncodeToString(sum[:12])
	if strings.HasPrefix(notifyType, "channel_status:") && model.DB != nil && model.DB.Migrator().HasTable(&AdtyChannelWatcherOutbox{}) {
		parts := strings.Split(notifyType, ":")
		if len(parts) >= 2 {
			prefix := "channel_status:" + parts[1] + ":"
			var latest AdtyChannelWatcherOutbox
			if model.DB.Where("notify_type LIKE ?", prefix+"%").Order("id DESC").Limit(1).Find(&latest).RowsAffected > 0 {
				if latest.NotifyType == notifyType && strings.HasPrefix(latest.EventId, "watcher:"+baseHash+":") {
					return latest.EventId
				}
				var total int64
				_ = model.DB.Model(&AdtyChannelWatcherOutbox{}).Where("notify_type LIKE ?", prefix+"%").Count(&total).Error
				return fmt.Sprintf("watcher:%s:%d", baseHash, total+1)
			}
		}
	}
	return "watcher:" + baseHash + ":1"
}

// NotifyChannelWatchers sends a channel notice (a channel disabled or enabled
// automatically, or a test of all channels finished) to every administrator
// who receives channel notices. Until they choose, the root user receives them
// and other administrators do not. render builds the subject and content in
// the recipient's saved language; an empty language means DEFAULT_LANGUAGE.
func NotifyChannelWatchers(notifyType string, render func(lang string) (subject string, content string)) {
	NotifyChannelWatchersWithEventID("", notifyType, render)
}

// NotifyChannelWatchersWithEventID persists authoritative in-site watcher notices
// and durable outbox entries keyed by eventID, deduplicating repeated events.
func NotifyChannelWatchersWithEventID(eventID, notifyType string, render func(lang string) (subject string, content string)) int {
	return notifyAdminsWithEventID(eventID, notifyType, receivesChannelWatcherNotice, render)
}

// NotifyUpstreamModelUpdateWatchers sends the upstream model update notice to
// every administrator who enabled it. render builds the subject and content in
// the recipient's saved language; an empty language means DEFAULT_LANGUAGE.
func NotifyUpstreamModelUpdateWatchers(render func(lang string) (subject string, content string)) {
	NotifyUpstreamModelUpdateWatchersWithEventID("", render)
}

// NotifyUpstreamModelUpdateWatchersWithEventID persists authoritative in-site
// upstream model update notices and durable outbox entries keyed by eventID.
func NotifyUpstreamModelUpdateWatchersWithEventID(eventID string, render func(lang string) (subject string, content string)) int {
	sentCount := notifyAdminsWithEventID(eventID, dto.NotifyTypeChannelUpdate, receivesUpstreamModelUpdateNotice, render)
	common.SysLog(common.LogText("upstream model update notifications sent: %d", sentCount))
	return sentCount
}

// notifyAdmins sends a notice to each enabled administrator for whom receives
// returns true, and returns how many notices it sent.
func notifyAdmins(notifyType string, receives func(userID, role int, setting dto.UserSetting) bool, render func(lang string) (subject string, content string)) int {
	return notifyAdminsWithEventID("", notifyType, receives, render)
}

func notifyAdminsWithEventID(eventID, notifyType string, receives func(userID, role int, setting dto.UserSetting) bool, render func(lang string) (subject string, content string)) int {
	if model.DB == nil {
		return 0
	}
	ensureAdtyChannelWatcherTables()
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		eventID = deriveWatcherEventID(notifyType, render)
	}
	var users []model.User
	if err := model.DB.
		Select("id", "email", "role", "status", "setting", "site_id").
		Where("status = ? AND role >= ?", common.UserStatusEnabled, common.RoleAdminUser).
		Find(&users).Error; err != nil {
		common.SysLog(common.LogText("failed to query administrators to notify: %s", err.Error()))
		return 0
	}

	now := time.Now().Unix()
	sentCount := 0
	for _, user := range users {
		userSetting := user.GetSetting()
		if !receives(user.Id, user.Role, userSetting) {
			continue
		}
		subject, content := "", ""
		if render != nil {
			subject, content = render(userSetting.Language)
		}
		subject, content = ResolveAdtyNoticeTemplateForNotify(notifyType, userSetting.Language, subject, content, nil)
		readSum := sha256.Sum256([]byte("watcher-notice:" + eventID + ":" + strconv.Itoa(user.Id)))
		readKey := hex.EncodeToString(readSum[:])

		notice := AdtyChannelWatcherNotice{
			EventId:    eventID,
			UserId:     user.Id,
			NotifyType: notifyType,
			Category:   "model_group_anomaly",
			Language:   userSetting.Language,
			Title:      subject,
			Content:    content,
			ReadKey:    readKey,
			CreatedAt:  now,
		}
		if err := model.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&notice).Error; err != nil {
			common.SysLog(common.LogText("failed to persist watcher notice %s for user %d: %s", eventID, user.Id, err.Error()))
		} else if _, err := UpsertAdtySevereTodo(user.Id, user.SiteId, readKey, subject, content, notice.Category, 0); err != nil {
			common.SysLog(common.LogText("severe todo was not created for watcher notice %s: %s", readKey, err.Error()))
		}

		var outbox AdtyChannelWatcherOutbox
		found := model.DB.Where("event_id = ? AND user_id = ?", eventID, user.Id).Limit(1).Find(&outbox)
		if found.Error != nil {
			continue
		}
		if found.RowsAffected > 0 {
			// Idempotent: already delivered or revoked for this event_id and recipient.
			if outbox.Status == AdtyWatcherOutboxDelivered || outbox.Status == AdtyWatcherOutboxRevoked {
				continue
			}
		} else {
			// Freeze a separate English copy: the in-site notice stays localized,
			// even when delivery is retried after a notification-channel change.
			emailTitle, emailContent := "", ""
			if render != nil {
				emailTitle, emailContent = render("en")
			}
			outbox = AdtyChannelWatcherOutbox{
				EventId:      eventID,
				UserId:       user.Id,
				NotifyType:   notifyType,
				Category:     "model_group_anomaly",
				Language:     userSetting.Language,
				Title:        subject,
				Content:      content,
				EmailTitle:   emailTitle,
				EmailContent: emailContent,
				Status:       AdtyWatcherOutboxPending,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			if err := model.DB.Create(&outbox).Error; err != nil {
				continue
			}
		}

		if deliverWatcherOutboxEntry(&outbox, receives) {
			sentCount++
		}
	}
	return sentCount
}

func deliverWatcherOutboxEntry(outbox *AdtyChannelWatcherOutbox, receives func(userID, role int, setting dto.UserSetting) bool) bool {
	if outbox == nil || model.DB == nil {
		return false
	}
	now := time.Now().Unix()
	var liveUser model.User
	if err := model.DB.Select("id", "email", "role", "status", "setting").Where("id = ?", outbox.UserId).Limit(1).Find(&liveUser).Error; err != nil {
		common.SysLog(common.LogText("failed to query recipient %d for watcher outbox %d: %s", outbox.UserId, outbox.Id, err.Error()))
		_ = model.DB.Model(outbox).Updates(map[string]any{
			"status":        AdtyWatcherOutboxFailed,
			"last_error":    "recipient lookup error: " + err.Error(),
			"next_retry_at": now + 60,
			"updated_at":    now,
		}).Error
		outbox.Status = AdtyWatcherOutboxFailed
		return false
	}
	if liveUser.Id == 0 {
		_ = model.DB.Model(outbox).Updates(map[string]any{
			"status":     AdtyWatcherOutboxRevoked,
			"last_error": "recipient not found",
			"updated_at": now,
		}).Error
		outbox.Status = AdtyWatcherOutboxRevoked
		return false
	}
	liveSetting := liveUser.GetSetting()
	if receives == nil {
		if outbox.NotifyType == dto.NotifyTypeChannelUpdate {
			receives = receivesUpstreamModelUpdateNotice
		} else {
			receives = receivesChannelWatcherNotice
		}
	}
	if liveUser.Status != common.UserStatusEnabled || liveUser.Role < common.RoleAdminUser || !receives(liveUser.Id, liveUser.Role, liveSetting) {
		_ = model.DB.Model(outbox).Updates(map[string]any{
			"status":     AdtyWatcherOutboxRevoked,
			"last_error": "recipient permission or notification switch revoked before delivery",
			"updated_at": now,
		}).Error
		outbox.Status = AdtyWatcherOutboxRevoked
		return false
	}

	lang := notifyDeliveryLanguage(liveSetting)
	baseTitle, baseContent := outbox.Title, outbox.Content
	if (liveSetting.NotifyType == "" || liveSetting.NotifyType == dto.NotifyTypeEmail) && outbox.EmailTitle != "" {
		baseTitle, baseContent = outbox.EmailTitle, outbox.EmailContent
	}
	subject, content := ResolveAdtyNoticeTemplateForNotify(outbox.NotifyType, lang, baseTitle, baseContent, nil)
	attempts := outbox.AttemptCount + 1
	if err := NotifyUser(liveUser.Id, liveUser.Email, liveSetting, dto.NewNotify(outbox.NotifyType, subject, content, nil)); err != nil {
		common.SysLog(common.LogText("failed to send notice %s to user %d: %s", outbox.NotifyType, liveUser.Id, err.Error()))
		_ = model.DB.Model(outbox).Updates(map[string]any{
			"status":        AdtyWatcherOutboxFailed,
			"attempt_count": attempts,
			"last_error":    err.Error(),
			"next_retry_at": now + 60,
			"updated_at":    now,
		}).Error
		outbox.Status = AdtyWatcherOutboxFailed
		outbox.AttemptCount = attempts
		outbox.LastError = err.Error()
		return false
	}
	_ = model.DB.Model(outbox).Updates(map[string]any{
		"status":        AdtyWatcherOutboxDelivered,
		"attempt_count": attempts,
		"last_error":    "",
		"next_retry_at": 0,
		"delivered_at":  now,
		"updated_at":    now,
	}).Error
	outbox.Status = AdtyWatcherOutboxDelivered
	outbox.AttemptCount = attempts
	outbox.LastError = ""
	outbox.DeliveredAt = now
	return true
}

// RetryFailedChannelWatcherNotices retries pending or failed watcher outbox entries,
// re-verifying each recipient's current role, ChannelRead permission, and switch before sending.
func RetryFailedChannelWatcherNotices() int {
	return retryChannelWatcherNotices(false, 0)
}

// RetryDueChannelWatcherNotices retries only rows whose next_retry_at is due.
func RetryDueChannelWatcherNotices(now int64) int {
	if now <= 0 {
		now = time.Now().Unix()
	}
	return retryChannelWatcherNotices(true, now)
}

func retryChannelWatcherNotices(dueOnly bool, now int64) int {
	if model.DB == nil || !model.DB.Migrator().HasTable(&AdtyChannelWatcherOutbox{}) {
		return 0
	}
	query := model.DB.Where("status IN ?", []string{AdtyWatcherOutboxPending, AdtyWatcherOutboxFailed})
	if dueOnly {
		query = query.Where("next_retry_at = 0 OR next_retry_at <= ?", now)
	}
	var rows []AdtyChannelWatcherOutbox
	if err := query.Order("id ASC").Find(&rows).Error; err != nil {
		return 0
	}
	delivered := 0
	for i := range rows {
		if deliverWatcherOutboxEntry(&rows[i], nil) {
			delivered++
		}
	}
	return delivered
}

type adtyChannelWatcherRetryHandler struct{}

func (adtyChannelWatcherRetryHandler) Type() string { return adtyChannelWatcherRetryTaskType }
func (adtyChannelWatcherRetryHandler) Interval() time.Duration {
	return 60 * time.Second
}
func (adtyChannelWatcherRetryHandler) NewPayload() any { return nil }
func (adtyChannelWatcherRetryHandler) Enabled() bool {
	if model.DB == nil || !model.DB.Migrator().HasTable(&AdtyChannelWatcherOutbox{}) {
		return false
	}
	now := time.Now().Unix()
	var count int64
	err := model.DB.Model(&AdtyChannelWatcherOutbox{}).
		Where("status IN ? AND (next_retry_at = 0 OR next_retry_at <= ?)", []string{AdtyWatcherOutboxPending, AdtyWatcherOutboxFailed}, now).
		Count(&count).Error
	return err == nil && count > 0
}

func (adtyChannelWatcherRetryHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	if task == nil {
		return
	}
	status, errorCategory := model.SystemTaskStatusSucceeded, ""
	delivered := 0
	if ctx != nil && ctx.Err() != nil {
		status, errorCategory = model.SystemTaskStatusFailed, "cancelled"
	} else {
		delivered = RetryDueChannelWatcherNotices(time.Now().Unix())
	}
	if err := model.FinishSystemTask(task.TaskID, runnerID, status, map[string]int{"delivered": delivered}, errorCategory); err != nil {
		common.SysLog(common.LogText("channel watcher retry task result could not be persisted: %s", err.Error()))
	}
}

func init() {
	RegisterSystemTaskHandler(adtyChannelWatcherRetryHandler{})
}

// channelStatusEventID identifies one committed channel status transition.
// status_time is read after UpdateChannelStatus returns, so a later transition
// with a new timestamp is a new event. Send failure does not roll that status back.
func channelStatusEventID(channelID, status int) string {
	statusTime := time.Now().Unix()
	if channelID > 0 && model.DB != nil {
		if channel, err := model.GetChannelById(channelID, false); err == nil && channel != nil {
			if ts, ok := channelOtherInfoUnix(channel.GetOtherInfo()["status_time"]); ok && ts > 0 {
				statusTime = ts
			}
		}
	}
	return fmt.Sprintf("channel_status:%d:%d:%d", channelID, status, statusTime)
}

func channelOtherInfoUnix(value any) (int64, bool) {
	switch v := value.(type) {
	case float64:
		return int64(v), true
	case float32:
		return int64(v), true
	case int:
		return int64(v), true
	case int64:
		return v, true
	case int32:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			f, ferr := v.Float64()
			if ferr != nil {
				return 0, false
			}
			return int64(f), true
		}
		return n, true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

// UpstreamModelUpdateEventID is stable for one scan that already passed the 24h window.
func UpstreamModelUpdateEventID(now int64, changedChannels, failedChannels int) string {
	if now <= 0 {
		now = time.Now().Unix()
	}
	return fmt.Sprintf("upstream_model_update:%d:%d:%d", now, changedChannels, failedChannels)
}

const adtyWatcherNoticeListLimit = 100

func adtyWatcherNoticeInboxItems(userID int) []map[string]any {
	if userID <= 0 || model.DB == nil || !model.DB.Migrator().HasTable(&AdtyChannelWatcherNotice{}) {
		return nil
	}
	q := model.DB.Where("user_id = ?", userID)
	if model.DB.Migrator().HasTable(&model.AdtyNotificationHide{}) {
		q = q.Where(
			"NOT EXISTS (SELECT 1 FROM adty_notification_hides h WHERE h.user_id = ? AND (h.key = adty_channel_watcher_notices.read_key OR h.key = 'watcher:' || adty_channel_watcher_notices.read_key))",
			userID,
		)
	}
	var notices []AdtyChannelWatcherNotice
	if err := q.Order("id DESC").Limit(adtyWatcherNoticeListLimit).Find(&notices).Error; err != nil || len(notices) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(notices))
	for _, notice := range notices {
		if strings.TrimSpace(notice.ReadKey) == "" {
			continue
		}
		items = append(items, formatAdtyWatcherNoticeItem(notice))
	}
	return items
}

func formatAdtyWatcherNoticeItem(notice AdtyChannelWatcherNotice) map[string]any {
	created := notice.CreatedAt
	if created <= 0 {
		created = time.Now().Unix()
	}
	return map[string]any{
		"id":               "watcher:" + notice.ReadKey,
		"read_key":         notice.ReadKey,
		"title":            notice.Title,
		"content":          notice.Content,
		"type":             "default",
		"publishDate":      time.Unix(created, 0).UTC().Format(time.RFC3339),
		"publisher_name":   "System",
		"publisher_id":     0,
		"issuer_site_id":   0,
		"audience":         "user",
		"target_site_id":   0,
		"target_site_name": fmt.Sprintf("user:%d", notice.UserId),
		"kind":             "system",
		"business":         notice.Category,
		"source":           "channel_watcher",
		"event_id":         notice.EventId,
		"notify_type":      notice.NotifyType,
	}
}

func adtyUnreadWatcherNoticeCount(userID, siteID int) int64 {
	if userID <= 0 || model.DB == nil || !model.DB.Migrator().HasTable(&AdtyChannelWatcherNotice{}) {
		return 0
	}
	q := model.DB.Model(&AdtyChannelWatcherNotice{}).Where("user_id = ? AND read_key <> ''", userID)
	if model.DB.Migrator().HasTable(&model.AdtyNotificationRead{}) {
		q = q.Where(
			"NOT EXISTS (SELECT 1 FROM adty_notification_reads r WHERE r.user_id = ? AND r.site_id = ? AND r.read_key = adty_channel_watcher_notices.read_key)",
			userID, siteID,
		)
	}
	if model.DB.Migrator().HasTable(&model.AdtyNotificationHide{}) {
		q = q.Where(
			"NOT EXISTS (SELECT 1 FROM adty_notification_hides h WHERE h.user_id = ? AND (h.key = adty_channel_watcher_notices.read_key OR h.key = 'watcher:' || adty_channel_watcher_notices.read_key))",
			userID,
		)
	}
	var unread int64
	if err := q.Count(&unread).Error; err != nil {
		return 0
	}
	return unread
}

func adtyOwnedWatcherReadKeys(userID int, keys []string) map[string]bool {
	owned := map[string]bool{}
	if userID <= 0 || len(keys) == 0 || model.DB == nil || !model.DB.Migrator().HasTable(&AdtyChannelWatcherNotice{}) {
		return owned
	}
	var found []string
	if err := model.DB.Model(&AdtyChannelWatcherNotice{}).Where("user_id = ? AND read_key IN ?", userID, keys).Pluck("read_key", &found).Error; err != nil {
		return owned
	}
	for _, key := range found {
		owned[key] = true
	}
	return owned
}

func adtyWatcherNoticeDetail(userID int, key string) (map[string]any, bool) {
	key = strings.TrimSpace(key)
	if userID <= 0 || key == "" || model.DB == nil || !model.DB.Migrator().HasTable(&AdtyChannelWatcherNotice{}) {
		return nil, false
	}
	var notice AdtyChannelWatcherNotice
	res := model.DB.Where("user_id = ? AND (read_key = ? OR event_id = ?)", userID, key, key).Order("id DESC").Limit(1).Find(&notice)
	if res.Error != nil || res.RowsAffected == 0 || notice.Id == 0 {
		return nil, false
	}
	if adtyIsNotificationKeyHidden(userID, notice.ReadKey) {
		return nil, false
	}
	if _, err := UpsertAdtySevereTodo(userID, adtyTodoSiteID(userID, nil), notice.ReadKey, notice.Title, notice.Content, notice.Category, 0); err != nil {
		common.SysLog(common.LogText("severe todo was not created for watcher notice %s: %s", notice.ReadKey, err.Error()))
	}
	item := formatAdtyWatcherNoticeItem(notice)
	RenderAdtyTodoInboxItems(userID, []map[string]any{item})
	return item, true
}

// ListAdtyChannelWatcherNoticesForUser returns the authoritative in-site watcher notifications for userID.
func ListAdtyChannelWatcherNoticesForUser(userID int) ([]AdtyChannelWatcherNotice, error) {
	if userID <= 0 || model.DB == nil {
		return nil, nil
	}
	ensureAdtyChannelWatcherTables()
	var rows []AdtyChannelWatcherNotice
	err := model.DB.Where("user_id = ?", userID).Order("id DESC").Limit(adtyWatcherNoticeListLimit).Find(&rows).Error
	return rows, err
}

func notifyDeliveryLanguage(setting dto.UserSetting) string {
	if setting.NotifyType == "" || setting.NotifyType == dto.NotifyTypeEmail {
		return "en"
	}
	return setting.Language
}

func NotifyUser(userId int, userEmail string, userSetting dto.UserSetting, data dto.Notify) error {
	notifyType := userSetting.NotifyType
	if notifyType == "" {
		notifyType = dto.NotifyTypeEmail
	}

	// Check notification limit
	canSend, err := CheckNotificationLimit(userId, data.Type)
	if err != nil {
		common.SysLog(common.LogText("failed to check notification limit: %s", err.Error()))
		return err
	}
	if !canSend {
		return fmt.Errorf("notification limit exceeded for user %d with type %s", userId, notifyType)
	}

	switch notifyType {
	case dto.NotifyTypeEmail:
		// 优先使用设置中的通知邮箱，如果为空则使用用户的默认邮箱
		emailToUse := userSetting.NotificationEmail
		if emailToUse == "" {
			emailToUse = userEmail
		}
		if emailToUse == "" {
			common.SysLog(common.LogText("user %d has no email, skip sending email", userId))
			return nil
		}
		return sendEmailNotify(AdtySiteForUser(userId), emailToUse, data)
	case dto.NotifyTypeWebhook:
		webhookURLStr := userSetting.WebhookUrl
		if webhookURLStr == "" {
			common.SysLog(common.LogText("user %d has no webhook url, skip sending webhook", userId))
			return nil
		}

		// 获取 webhook secret
		webhookSecret := userSetting.WebhookSecret
		return SendWebhookNotify(webhookURLStr, webhookSecret, data)
	case dto.NotifyTypeBark:
		barkURL := userSetting.BarkUrl
		if barkURL == "" {
			common.SysLog(common.LogText("user %d has no bark url, skip sending bark", userId))
			return nil
		}
		return sendBarkNotify(barkURL, data)
	case dto.NotifyTypeGotify:
		gotifyUrl := userSetting.GotifyUrl
		gotifyToken := userSetting.GotifyToken
		if gotifyUrl == "" || gotifyToken == "" {
			common.SysLog(common.LogText("user %d has no gotify url or token, skip sending gotify", userId))
			return nil
		}
		return sendGotifyNotify(gotifyUrl, gotifyToken, userSetting.GotifyPriority, data)
	}
	return nil
}

// sendEmailNotify 二开：发件人和站名按用户所属站点（任务 11.3）。通知正文里的 {{SystemName}} 由调用方填好；
// 这里只换发件人，标题里的平台站名换成分站站名。
func sendEmailNotify(site *model.AdtySite, userEmail string, data dto.Notify) error {
	// make email content
	content := data.Content
	// 处理占位符
	for _, value := range data.Values {
		content = strings.Replace(content, dto.ContentValueParam, fmt.Sprintf("%v", value), 1)
	}
	title := data.Title
	if site != nil {
		title = strings.ReplaceAll(title, common.SystemName, AdtyBrandFor(site).Name)
	}
	msg := AdtyMailMessage{Title: title, Paragraphs: strings.Split(content, "\n")}
	if data.Type == dto.NotifyTypeQuotaExceed && len(data.Values) >= 3 {
		msg.ActionLabel = "Top up your balance"
		msg.ActionURL, _ = data.Values[2].(string)
	}
	return AdtySendSiteEmail(site, title, userEmail, AdtyRenderMail(site, msg))
}

func sendBarkNotify(barkURL string, data dto.Notify) error {
	// 处理占位符
	content := data.Content
	for _, value := range data.Values {
		content = strings.Replace(content, dto.ContentValueParam, fmt.Sprintf("%v", value), 1)
	}

	// 替换模板变量
	finalURL := strings.ReplaceAll(barkURL, "{{title}}", url.QueryEscape(data.Title))
	finalURL = strings.ReplaceAll(finalURL, "{{content}}", url.QueryEscape(content))

	// 发送GET请求到Bark
	var req *http.Request
	var resp *http.Response
	var err error

	if system_setting.EnableWorker() {
		// 使用worker发送请求
		workerReq := &WorkerRequest{
			URL:    finalURL,
			Key:    system_setting.WorkerValidKey,
			Method: http.MethodGet,
			Headers: map[string]string{
				"User-Agent": "OneAPI-Bark-Notify/1.0",
			},
		}

		resp, err = DoWorkerRequest(workerReq)
		if err != nil {
			return fmt.Errorf("failed to send bark request through worker: %v", err)
		}
		defer resp.Body.Close()

		// 检查响应状态
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("bark request failed with status code: %d", resp.StatusCode)
		}
	} else {
		// SSRF防护：验证Bark URL（非Worker模式）
		if err := ValidateSSRFProtectedFetchURL(finalURL); err != nil {
			return fmt.Errorf("request reject: %v", err)
		}

		// 直接发送请求
		req, err = http.NewRequest(http.MethodGet, finalURL, nil)
		if err != nil {
			return fmt.Errorf("failed to create bark request: %v", err)
		}

		// 设置User-Agent
		req.Header.Set("User-Agent", "OneAPI-Bark-Notify/1.0")

		// 发送请求
		client := GetSSRFProtectedHTTPClient()
		resp, err = client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send bark request: %v", err)
		}
		defer resp.Body.Close()

		// 检查响应状态
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("bark request failed with status code: %d", resp.StatusCode)
		}
	}

	return nil
}

func sendGotifyNotify(gotifyUrl string, gotifyToken string, priority int, data dto.Notify) error {
	// 处理占位符
	content := data.Content
	for _, value := range data.Values {
		content = strings.Replace(content, dto.ContentValueParam, fmt.Sprintf("%v", value), 1)
	}

	// 构建完整的 Gotify API URL
	// 确保 URL 以 /message 结尾
	finalURL := strings.TrimSuffix(gotifyUrl, "/") + "/message?token=" + url.QueryEscape(gotifyToken)

	// Gotify优先级范围0-10，如果超出范围则使用默认值5
	if priority < 0 || priority > 10 {
		priority = 5
	}

	// 构建 JSON payload
	type GotifyMessage struct {
		Title    string `json:"title"`
		Message  string `json:"message"`
		Priority int    `json:"priority"`
	}

	payload := GotifyMessage{
		Title:    data.Title,
		Message:  content,
		Priority: priority,
	}

	// 序列化为 JSON
	payloadBytes, err := common.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal gotify payload: %v", err)
	}

	var req *http.Request
	var resp *http.Response

	if system_setting.EnableWorker() {
		// 使用worker发送请求
		workerReq := &WorkerRequest{
			URL:    finalURL,
			Key:    system_setting.WorkerValidKey,
			Method: http.MethodPost,
			Headers: map[string]string{
				"Content-Type": "application/json; charset=utf-8",
				"User-Agent":   "OneAPI-Gotify-Notify/1.0",
			},
			Body: payloadBytes,
		}

		resp, err = DoWorkerRequest(workerReq)
		if err != nil {
			return fmt.Errorf("failed to send gotify request through worker: %v", err)
		}
		defer resp.Body.Close()

		// 检查响应状态
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("gotify request failed with status code: %d", resp.StatusCode)
		}
	} else {
		// SSRF防护：验证Gotify URL（非Worker模式）
		if err := ValidateSSRFProtectedFetchURL(finalURL); err != nil {
			return fmt.Errorf("request reject: %v", err)
		}

		// 直接发送请求
		req, err = http.NewRequest(http.MethodPost, finalURL, bytes.NewBuffer(payloadBytes))
		if err != nil {
			return fmt.Errorf("failed to create gotify request: %v", err)
		}

		// 设置请求头
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("User-Agent", "NewAPI-Gotify-Notify/1.0")

		// 发送请求
		client := GetSSRFProtectedHTTPClient()
		resp, err = client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send gotify request: %v", err)
		}
		defer resp.Body.Close()

		// 检查响应状态
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("gotify request failed with status code: %d", resp.StatusCode)
		}
	}

	return nil
}
