package model

import "gorm.io/gorm"

// BackfillAdtyCloseMailDeadline preserves an unknown deadline when neither
// the first reschedule nor an unrevised site supplies the original value.
func BackfillAdtyCloseMailDeadline(db *gorm.DB, siteID int) error {
	q := db.Model(&AdtySiteCloseMailBatch{}).Where("revision = 1 AND COALESCE(execute_at,0) = 0")
	if siteID > 0 {
		q = q.Where("site_id = ?", siteID)
	}
	return q.UpdateColumn("execute_at", gorm.Expr(`COALESCE(
 (SELECT NULLIF(b.previous_execute_at,0) FROM adty_site_close_mail_batches b WHERE b.site_id = adty_site_close_mail_batches.site_id AND b.revision = 2),
 (SELECT s.close_execute_at FROM adty_sites s WHERE s.id = adty_site_close_mail_batches.site_id AND s.status IN ('closing','closed') AND NOT EXISTS (SELECT 1 FROM adty_site_close_mail_batches r WHERE r.site_id = s.id AND r.revision > 1)), 0)`)).Error
}

// Official close-migration batch mail inventory (A.16.2).
// One campaign is recorded per closing site. Recipient rows are the failure
// list and retryable state; a later retry must not create a second campaign
// or re-send already successful mail.
const (
	AdtySiteCloseMailPending        = "pending"
	AdtySiteCloseMailSuccess        = "success"
	AdtySiteCloseMailFailed         = "failed"
	AdtySiteCloseMailRetryNone      = "none"
	AdtySiteCloseMailRetryRetryable = "retryable"
	AdtySiteCloseMailRetryComplete  = "complete"
)

// AdtySiteCloseMailBatch records one official close-migration mail campaign.
// It stores the target site, recipient count, success/fail counts, send time,
// and retry status required by A.16.2.
type AdtySiteCloseMailBatch struct {
	Id                int64  `json:"id"`
	SiteId            int    `json:"site_id" gorm:"uniqueIndex:ux_close_mail_schedule;index:idx_close_mail_site"`
	Revision          int    `json:"revision" gorm:"not null;default:1;uniqueIndex:ux_close_mail_schedule"`
	PreviousExecuteAt int64  `json:"previous_execute_at"`
	ExecuteAt         int64  `json:"execute_at"`
	RecipientCount    int    `json:"recipient_count"`
	SuccessCount      int    `json:"success_count"`
	FailCount         int    `json:"fail_count"`
	SentAt            int64  `json:"sent_at"`
	RetryStatus       string `json:"retry_status" gorm:"type:varchar(16);not null;default:none"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
}

func (AdtySiteCloseMailBatch) TableName() string { return "adty_site_close_mail_batches" }

// AdtySiteCloseMailRecipient is one recipient in a close-migration batch.
// Failed rows remain retryable. The stored email is for the operator failure
// list only and must never be placed in another recipient's message.
type AdtySiteCloseMailRecipient struct {
	Attempts       []AdtySiteCloseMailAttempt `json:"attempts,omitempty" gorm:"-"`
	Id             int64                      `json:"id"`
	BatchId        int64                      `json:"batch_id" gorm:"uniqueIndex:ux_adty_site_close_mail_recipient;index"`
	SiteId         int                        `json:"site_id" gorm:"index"`
	UserId         int                        `json:"user_id" gorm:"uniqueIndex:ux_adty_site_close_mail_recipient;index"`
	Username       string                     `json:"username" gorm:"type:varchar(64)"`
	Email          string                     `json:"email" gorm:"type:varchar(255)"`
	Status         string                     `json:"status" gorm:"type:varchar(16);index;not null;default:pending"`
	RetryAllowance int                        `json:"-" gorm:"default:0"`
	Provider       string                     `json:"provider" gorm:"type:varchar(16)"`
	LeaseUntil     int64                      `json:"lease_until" gorm:"default:0"`
	FirstAttemptAt int64                      `json:"first_attempt_at" gorm:"default:0"`
	LastError      string                     `json:"last_error" gorm:"type:text"`
	AttemptCount   int                        `json:"attempt_count"`
	LastAttemptAt  int64                      `json:"last_attempt_at"`
	CreatedAt      int64                      `json:"created_at"`
	UpdatedAt      int64                      `json:"updated_at"`
}

func (AdtySiteCloseMailRecipient) TableName() string { return "adty_site_close_mail_recipients" }

// One row per provider call. A process loss leaves an identifiable sending attempt.
type AdtySiteCloseMailAttempt struct {
	Id                int64  `json:"-"`
	RecipientID       int64  `json:"-" gorm:"uniqueIndex:ux_close_mail_attempt"`
	Number            int    `json:"number" gorm:"uniqueIndex:ux_close_mail_attempt"`
	Provider          string `json:"provider" gorm:"type:varchar(16)"`
	Outcome           string `json:"outcome" gorm:"type:varchar(16)"`
	ProviderMessageID string `json:"provider_message_id,omitempty" gorm:"type:varchar(128)"`
	StartedAt         int64  `json:"started_at"`
	FinishedAt        int64  `json:"finished_at"`
}
