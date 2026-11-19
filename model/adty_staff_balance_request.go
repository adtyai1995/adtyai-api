package model

import (
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	AdtyStaffFundingSourceEarnings = "site_earnings"
	AdtyStaffFundingSourceMargin   = "site_margin"
	AdtyStaffBalanceIncrease       = "increase"
	AdtyStaffBalanceDecrease       = "decrease"
	AdtyStaffBalancePending        = "pending"
	AdtyStaffBalanceApproved       = "approved"
	AdtyStaffBalanceRejected       = "rejected"
)

// AdtyStaffBalanceRequest is an employee request to change a customer balance.
// It does not move money until a site owner or main-site admin approves it.
// Per A.4 (line 852) and A.12.4 (lines 1600-1602), both request and approval
// retain a concrete reason, associated business/order reference, and funding or
// recovery source (ISS-R1-0088).
type AdtyStaffBalanceRequest struct {
	Id             int64           `json:"id"`
	SiteId         int             `json:"site_id" gorm:"index"`
	StaffUserId    int             `json:"staff_user_id" gorm:"index"`
	CustomerUserId int             `json:"customer_user_id" gorm:"index"`
	Direction      string          `json:"direction" gorm:"type:varchar(16)"`
	AmountUSD      decimal.Decimal `json:"amount_usd" gorm:"type:decimal(20,8);not null"`
	Reason         string          `json:"reason" gorm:"type:text"`
	BusinessRef    string          `json:"business_ref" gorm:"type:varchar(128);index"`
	OrderRef       string          `json:"order_ref,omitempty" gorm:"type:varchar(128);index"`
	FundingSource  string          `json:"funding_source" gorm:"type:varchar(64)"`
	RecoverySource string          `json:"recovery_source,omitempty" gorm:"type:varchar(64)"`
	BatchKind      string          `json:"batch_kind,omitempty" gorm:"type:varchar(16)"`
	BatchSource    string          `json:"batch_source,omitempty" gorm:"type:varchar(64)"`
	ReviewNote     string          `json:"review_note,omitempty" gorm:"type:text"`
	BeforeQuota    int64           `json:"before_quota"`
	AfterQuota     int64           `json:"after_quota"`
	LedgerId       int64           `json:"ledger_id"`
	IdempotencyKey *string         `json:"idempotency_key,omitempty" gorm:"type:varchar(64);uniqueIndex"`
	Status         string          `json:"status" gorm:"type:varchar(16);index"`
	ReviewerId     int             `json:"reviewer_id"`
	ReviewerRole   string          `json:"reviewer_role" gorm:"type:varchar(16)"`
	CreatedAt      int64           `json:"created_at" gorm:"autoCreateTime"`
	ReviewedAt     int64           `json:"reviewed_at"`
}

func (AdtyStaffBalanceRequest) TableName() string { return "adty_staff_balance_requests" }

// AdtyPriceRevision records one price change. It is append-only history.
type AdtyPriceRevision struct {
	UserId      int    `json:"user_id" gorm:"index"`
	Group       string `json:"group" gorm:"type:varchar(64)"`
	Model       string `json:"model" gorm:"type:varchar(191)"`
	Action      string `json:"action" gorm:"type:varchar(32)"`
	Version     int64  `json:"version"`
	EffectiveAt int64  `json:"effective_at"`
	BeforeState string `json:"before_state" gorm:"type:text"`
	AfterState  string `json:"after_state" gorm:"type:text"`
	Id          int64  `json:"id"`
	SiteId      int    `json:"site_id" gorm:"index"`
	ActorId     int    `json:"actor_id"`
	Kind        string `json:"kind" gorm:"type:varchar(32);index"`
	Before      string `json:"before" gorm:"type:varchar(64)"`
	After       string `json:"after" gorm:"type:varchar(64)"`
	CreatedAt   int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtyPriceRevision) TableName() string { return "adty_price_revisions" }

func (AdtyPriceRevision) BeforeUpdate(*gorm.DB) error { return ErrAdtySiteWalletLedgerImmutable }

func (AdtyPriceRevision) BeforeDelete(*gorm.DB) error { return ErrAdtySiteWalletLedgerImmutable }

// AdtyNotificationHide is a user's manual removal of one inbox item.
type AdtyNotificationHide struct {
	Id        int64  `json:"id"`
	UserId    int    `json:"user_id" gorm:"uniqueIndex:idx_adty_notice_hide,priority:1"`
	Key       string `json:"key" gorm:"uniqueIndex:idx_adty_notice_hide,priority:2;type:varchar(128)"`
	CreatedAt int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtyNotificationHide) TableName() string { return "adty_notification_hides" }

// AdtyGlossaryTerm is one protected translation pair. Placeholders and amounts stay unchanged.
type AdtyGlossaryTerm struct {
	Id        int64  `json:"id"`
	Source    string `json:"source" gorm:"uniqueIndex:idx_adty_glossary,priority:1;type:varchar(128)"`
	Locale    string `json:"locale" gorm:"uniqueIndex:idx_adty_glossary,priority:2;type:varchar(16)"`
	Target    string `json:"target" gorm:"type:varchar(128)"`
	CreatedAt int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtyGlossaryTerm) TableName() string { return "adty_glossary_terms" }

// AdtySiteReferralRate is the one-level invite rate for a site. It is a decimal fraction.
type AdtySiteReferralRate struct {
	SiteId  int             `json:"site_id" gorm:"primaryKey"`
	Rate    decimal.Decimal `json:"rate" gorm:"type:decimal(10,6);not null"`
	Enabled bool            `json:"enabled"`
}

func (AdtySiteReferralRate) TableName() string { return "adty_site_referral_rates" }

// AdtySiteInvite is one direct site invitation. Invitees cannot be bound twice.
type AdtySiteInvite struct {
	Id        int64           `json:"id"`
	SiteId    int             `json:"site_id" gorm:"index"`
	InviterId int             `json:"inviter_id" gorm:"index"`
	InviteeId int             `json:"invitee_id" gorm:"uniqueIndex"`
	Rate      decimal.Decimal `json:"rate" gorm:"type:decimal(10,6);not null"`
	IpHash    string          `json:"-" gorm:"type:varchar(64);index"`
	CreatedAt int64           `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtySiteInvite) TableName() string { return "adty_site_invites" }

// AdtyAnomalyDisposition records a review of a fund anomaly.
// G.13.5 fields (difference, account, order, site, evidence) sit beside the
// original review columns. CreatedAt is the discovery time; OperatorId and
// Status/Note are the handler and disposition result after review.
type AdtyAnomalyDisposition struct {
	Id         int64  `json:"id"`
	Kind       string `json:"kind" gorm:"type:varchar(32);index"`
	Ref        string `json:"ref" gorm:"type:varchar(128);index"`
	Status     string `json:"status" gorm:"type:varchar(16)"`
	Note       string `json:"note" gorm:"type:varchar(255)"`
	OperatorId int    `json:"operator_id"`
	SiteId     int    `json:"site_id" gorm:"index"`
	AccountId  int    `json:"account_id"`
	OrderRef   string `json:"order_ref" gorm:"type:varchar(128)"`
	Difference string `json:"difference" gorm:"type:varchar(64)"`
	Evidence   string `json:"evidence" gorm:"type:text"`
	CreatedAt  int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtyAnomalyDisposition) TableName() string { return "adty_anomaly_dispositions" }

// AdtyEmailChange is a pending login-email change. It is not applied until confirmed.
type AdtyEmailChange struct {
	UserId   int    `json:"user_id" gorm:"primaryKey"`
	NewEmail string `json:"new_email" gorm:"type:varchar(128)"`
	Status   string `json:"status" gorm:"type:varchar(16)"`
}

func (AdtyEmailChange) TableName() string { return "adty_email_changes" }

// AdtyAccountClosure is a closure request waiting for email confirmation.
type AdtyAccountClosure struct {
	UserId int    `json:"user_id" gorm:"primaryKey"`
	Status string `json:"status" gorm:"type:varchar(24)"`
}

func (AdtyAccountClosure) TableName() string { return "adty_account_closures" }

// AdtyNoticeTemplate is a fixed bilingual system notice.
type AdtyNoticeTemplate struct {
	Key string `json:"key" gorm:"column:notice_key;primaryKey;type:varchar(64)"`
	Zh  string `json:"zh" gorm:"type:varchar(255)"`
	En  string `json:"en" gorm:"type:varchar(255)"`
}

func (AdtyNoticeTemplate) TableName() string { return "adty_notice_templates" }

// AdtyKeyLimit is the per-minute cap for one API key.
type AdtyKeyLimit struct {
	TokenId   int `json:"token_id" gorm:"primaryKey"`
	PerMinute int `json:"per_minute"`
}

func (AdtyKeyLimit) TableName() string { return "adty_key_limits" }

// AdtyModelAlias maps a public name to an upstream model and context window.
type AdtyModelAlias struct {
	Alias   string `json:"alias" gorm:"primaryKey;type:varchar(80)"`
	Target  string `json:"target" gorm:"type:varchar(80)"`
	Context int    `json:"context"`
	Enabled bool   `json:"enabled"`
}

func (AdtyModelAlias) TableName() string { return "adty_model_aliases" }

// AdtyGroupConfig is one token group, its ratio, and the models it may call.
type AdtyGroupConfig struct {
	Name   string          `json:"name" gorm:"primaryKey;type:varchar(64)"`
	SiteId int             `json:"site_id" gorm:"primaryKey"`
	Ratio  decimal.Decimal `json:"ratio" gorm:"type:decimal(10,4);not null"`
	Models string          `json:"models" gorm:"type:text"`
}

func (AdtyGroupConfig) TableName() string { return "adty_group_configs" }

type AdtyStaffBalanceProvenance struct {
	Reason        string `json:"reason"`
	OrderRef      string `json:"order_ref"`
	FundingSource string `json:"funding_source"`
	BatchKind     string `json:"batch_kind"`
	BatchSource   string `json:"batch_source"`
}
