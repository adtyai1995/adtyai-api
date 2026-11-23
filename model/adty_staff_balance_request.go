package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	AdtyStaffBalanceIncrease = "increase"
	AdtyStaffBalanceDecrease = "decrease"
	AdtyStaffBalancePending  = "pending"
	AdtyStaffBalanceApproved = "approved"
	AdtyStaffBalanceRejected = "rejected"

	AdtyStaffFundingSourceAuto     = "auto"
	AdtyStaffFundingSourceEarnings = "site_earnings"
	AdtyStaffFundingSourceMargin   = "site_margin"
	AdtyStaffFundingSourcePlatform = "platform"

	AdtyStaffRecoverySourcePrincipalBatch = "principal_batch"
	AdtyStaffRecoverySourceBonusBatch     = "bonus_batch"
	AdtyStaffRecoverySourceFIFO           = "fifo_batches"
)

var (
	ErrAdtyStaffBalanceBusinessRefRequired   = errors.New("staff balance request business reference is required")
	ErrAdtyStaffBalanceFundingSourceRequired = errors.New("staff balance request funding or recovery source is required")
	ErrAdtyStaffBalanceInvalidState          = errors.New("staff balance request is not pending")
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
	IdempotencyKey *string         `json:"-" gorm:"type:varchar(64);uniqueIndex"`
	Status         string          `json:"status" gorm:"type:varchar(16);index"`
	ReviewerId     int             `json:"reviewer_id"`
	ReviewerRole   string          `json:"reviewer_role" gorm:"type:varchar(16)"`
	CreatedAt      int64           `json:"created_at" gorm:"autoCreateTime"`
	ReviewedAt     int64           `json:"reviewed_at"`
}

type AdtyStaffBalanceProvenance struct {
	Reason        string `json:"reason"`
	OrderRef      string `json:"order_ref"`
	FundingSource string `json:"funding_source"`
	BatchKind     string `json:"batch_kind"`
	BatchSource   string `json:"batch_source"`
}

func (AdtyStaffBalanceRequest) TableName() string { return "adty_staff_balance_requests" }

// NormalizeProvenance canonicalizes whitespace and keeps BusinessRef/OrderRef
// and FundingSource/RecoverySource synchronized.
func (r *AdtyStaffBalanceRequest) NormalizeProvenance() {
	if r == nil {
		return
	}
	r.Direction = strings.TrimSpace(r.Direction)
	r.Reason = strings.TrimSpace(r.Reason)
	r.BusinessRef = strings.TrimSpace(r.BusinessRef)
	r.OrderRef = strings.TrimSpace(r.OrderRef)
	r.FundingSource = strings.TrimSpace(r.FundingSource)
	r.RecoverySource = strings.TrimSpace(r.RecoverySource)
	r.BatchKind = strings.TrimSpace(r.BatchKind)
	r.BatchSource = strings.TrimSpace(r.BatchSource)
	r.ReviewNote = strings.TrimSpace(r.ReviewNote)
	if r.BusinessRef == "" && r.OrderRef != "" {
		r.BusinessRef = r.OrderRef
	}
	if r.OrderRef == "" && r.BusinessRef != "" {
		r.OrderRef = r.BusinessRef
	}
	if r.Direction == AdtyStaffBalanceIncrease && r.FundingSource == "" {
		r.FundingSource = AdtyStaffFundingSourceAuto
	}
	if r.Direction == AdtyStaffBalanceDecrease {
		if r.FundingSource == "" && r.RecoverySource != "" {
			r.FundingSource = r.RecoverySource
		}
		if r.RecoverySource == "" && r.FundingSource != "" {
			r.RecoverySource = r.FundingSource
		}
	}
}

// ValidateProvenance enforces that a staff balance request carries a concrete
// reason, an associated business/order reference, and a funding/recovery source (A.12.4).
func (r *AdtyStaffBalanceRequest) ValidateProvenance() error {
	if r == nil {
		return ErrInvalidUserQuotaAdjustment
	}
	r.NormalizeProvenance()
	if r.Reason == "" {
		return ErrAdtyAdjustReasonRequired
	}
	if r.BusinessRef == "" {
		return ErrAdtyStaffBalanceBusinessRefRequired
	}
	if r.FundingSource == "" {
		return ErrAdtyStaffBalanceFundingSourceRequired
	}
	return nil
}

// LedgerReason returns the concrete audit reason recorded on AdtyBalanceLedger
// when this staff balance request is approved.
func (r AdtyStaffBalanceRequest) LedgerReason() string {
	r.NormalizeProvenance()
	if r.Reason == "" && r.BusinessRef == "" && r.FundingSource == "" {
		return "staff balance request"
	}
	parts := make([]string, 0, 4)
	if r.Reason != "" {
		parts = append(parts, r.Reason)
	}
	if r.BusinessRef != "" {
		parts = append(parts, "ref="+r.BusinessRef)
	}
	if r.FundingSource != "" {
		parts = append(parts, "source="+r.FundingSource)
	}
	if r.ReviewNote != "" {
		parts = append(parts, "review="+r.ReviewNote)
	}
	return strings.Join(parts, " | ")
}

// LedgerRefID returns the structured reference ID recorded on AdtyBalanceLedger.
func (r AdtyStaffBalanceRequest) LedgerRefID() string {
	r.NormalizeProvenance()
	idStr := strconv.FormatInt(r.Id, 10)
	if r.BusinessRef == "" && r.FundingSource == "" {
		return idStr
	}
	ref := fmt.Sprintf("req:%s|ref:%s|src:%s", idStr, r.BusinessRef, r.FundingSource)
	if len(ref) > 128 {
		ref = ref[:128]
	}
	return ref
}

// CreateAdtyStaffBalanceRequestWithProvenance validates and persists a staff
// balance request with full A.12.4 provenance.
func CreateAdtyStaffBalanceRequestWithProvenance(req *AdtyStaffBalanceRequest) (*AdtyStaffBalanceRequest, error) {
	if DB == nil {
		return nil, errors.New("database is not initialized")
	}
	var out *AdtyStaffBalanceRequest
	err := DB.Transaction(func(tx *gorm.DB) error {
		var txErr error
		out, txErr = CreateAdtyStaffBalanceRequestWithProvenanceTx(tx, req)
		return txErr
	})
	return out, err
}

// CreateAdtyStaffBalanceRequestWithProvenanceTx validates and persists a staff
// balance request inside tx, enforcing idempotency on IdempotencyKey.
func CreateAdtyStaffBalanceRequestWithProvenanceTx(tx *gorm.DB, req *AdtyStaffBalanceRequest) (*AdtyStaffBalanceRequest, error) {
	if tx == nil || req == nil {
		return nil, ErrInvalidUserQuotaAdjustment
	}
	req.NormalizeProvenance()
	if req.SiteId < 0 || req.StaffUserId <= 0 || req.CustomerUserId <= 0 {
		return nil, ErrInvalidUserQuotaAdjustment
	}
	if req.Direction != AdtyStaffBalanceIncrease && req.Direction != AdtyStaffBalanceDecrease {
		return nil, ErrInvalidUserQuotaAdjustment
	}
	amount := req.AmountUSD.Round(8)
	if !amount.IsPositive() {
		return nil, ErrInvalidUserQuotaAdjustment
	}
	if err := req.ValidateProvenance(); err != nil {
		return nil, err
	}
	if req.IdempotencyKey == nil || strings.TrimSpace(*req.IdempotencyKey) == "" {
		return nil, ErrAdtyAdjustKeyRequired
	}
	key := strings.TrimSpace(*req.IdempotencyKey)
	req.IdempotencyKey = &key
	req.AmountUSD = amount
	req.Status = AdtyStaffBalancePending

	var existing AdtyStaffBalanceRequest
	err := tx.Where("idempotency_key = ?", key).First(&existing).Error
	if err == nil {
		existing.NormalizeProvenance()
		if existing.SiteId != req.SiteId || existing.StaffUserId != req.StaffUserId ||
			existing.CustomerUserId != req.CustomerUserId || existing.Direction != req.Direction ||
			!existing.AmountUSD.Equal(amount) || existing.Reason != req.Reason ||
			existing.BusinessRef != req.BusinessRef || existing.FundingSource != req.FundingSource {
			return nil, ErrAdtyIdempotencyConflict
		}
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err := tx.Create(req).Error; err != nil {
		return nil, err
	}
	return req, nil
}

// ApproveAdtyStaffBalanceRequestWithProvenanceTx approves a pending staff
// balance request inside tx, enforcing A.12.4 provenance, updating customer
// fund batches and balance, and writing a provenance-rich AdtyBalanceLedger row.
func ApproveAdtyStaffBalanceRequestWithProvenanceTx(tx *gorm.DB, requestID int64, reviewerID int, reviewerRole, fundingSource, reviewNote string) (*AdtyStaffBalanceRequest, *AdtyBalanceLedger, error) {
	if tx == nil || requestID <= 0 || reviewerID <= 0 {
		return nil, nil, ErrInvalidUserQuotaAdjustment
	}
	var req AdtyStaffBalanceRequest
	if err := lockForUpdate(tx).First(&req, requestID).Error; err != nil {
		return nil, nil, err
	}
	if req.Status != AdtyStaffBalancePending {
		return nil, nil, ErrAdtyStaffBalanceInvalidState
	}
	if strings.TrimSpace(fundingSource) != "" {
		req.FundingSource = strings.TrimSpace(fundingSource)
	}
	if strings.TrimSpace(reviewNote) != "" {
		req.ReviewNote = strings.TrimSpace(reviewNote)
	}
	if err := req.ValidateProvenance(); err != nil {
		return nil, nil, err
	}
	convertedQuota, err := common.WalletQuotaFromDecimalStrict(req.AmountUSD.Mul(common.QuotaPerUnitDecimal()).Floor())
	if err != nil || convertedQuota <= 0 {
		return nil, nil, ErrInvalidUserQuotaAdjustment
	}
	quota := int64(convertedQuota)
	before, _, _, err := loadLockedUserLogicalQuotaTx(tx, req.CustomerUserId)
	if err != nil {
		return nil, nil, err
	}
	delta := quota
	if req.Direction == AdtyStaffBalanceDecrease {
		delta = -quota
	}
	after := before + delta
	if after < 0 {
		return nil, nil, ErrAdtyQuotaFloor
	}
	if delta > 0 {
		batchSource := fmt.Sprintf("staff_req:%d:%s", req.Id, req.BusinessRef)
		if len(batchSource) > 64 {
			batchSource = batchSource[:64]
		}
		var pQuota, bQuota int64
		if req.BatchKind == AdtyFundBatchKindBonus {
			bQuota = delta
		} else {
			pQuota = delta
		}
		if err := CreateAdtyFundBatchesWithSiteTx(tx, req.CustomerUserId, AdtyFundBatchSiteID(req.SiteId), batchSource, pQuota, bQuota); err != nil {
			return nil, nil, err
		}
		if _, err := AdtyCreditUserQuotaTx(tx, req.CustomerUserId, delta); err != nil {
			return nil, nil, err
		}
	} else {
		need := -delta
		reclaimed := int64(0)
		if req.BatchSource != "" {
			var pWant, bWant int64
			if req.BatchKind == AdtyFundBatchKindBonus || req.RecoverySource == AdtyStaffRecoverySourceBonusBatch {
				bWant = need
			} else {
				pWant = need
			}
			taken, err := ReclaimAdtyFundBatchesBySourceTx(tx, req.CustomerUserId, req.BatchSource, pWant, bWant)
			if err != nil {
				return nil, nil, err
			}
			reclaimed = taken
		}
		if leftover := need - reclaimed; leftover > 0 {
			if err := ConsumeAdtyFundBatchesQuietTx(tx, req.CustomerUserId, leftover, before-reclaimed); err != nil {
				return nil, nil, err
			}
		}
		if _, _, err := AdtyDebitUserQuotaTx(tx, req.CustomerUserId, need); err != nil {
			return nil, nil, err
		}
	}

	idemKey := "staff-balance:" + strconv.FormatInt(req.Id, 10)
	ledger := &AdtyBalanceLedger{
		UserId:         req.CustomerUserId,
		DeltaQuota:     delta,
		BalanceBefore:  before,
		BalanceAfter:   after,
		Type:           AdtyLedgerTypeManualAdjust,
		RefType:        "staff_balance",
		RefId:          req.LedgerRefID(),
		Reason:         req.LedgerReason(),
		OperatorId:     reviewerID,
		IdempotencyKey: &idemKey,
	}
	if err := RecordAdtyBalanceLedgerTx(tx, ledger); err != nil {
		return nil, nil, err
	}
	now := common.GetTimestamp()
	req.Status = AdtyStaffBalanceApproved
	req.ReviewerId = reviewerID
	req.ReviewerRole = strings.TrimSpace(reviewerRole)
	req.ReviewedAt = now
	req.BeforeQuota = before
	req.AfterQuota = after
	req.LedgerId = ledger.Id
	if err := tx.Model(&req).Updates(map[string]any{
		"reason":          req.Reason,
		"business_ref":    req.BusinessRef,
		"order_ref":       req.OrderRef,
		"funding_source":  req.FundingSource,
		"recovery_source": req.RecoverySource,
		"review_note":     req.ReviewNote,
		"before_quota":    req.BeforeQuota,
		"after_quota":     req.AfterQuota,
		"ledger_id":       req.LedgerId,
		"status":          req.Status,
		"reviewer_id":     req.ReviewerId,
		"reviewer_role":   req.ReviewerRole,
		"reviewed_at":     req.ReviewedAt,
	}).Error; err != nil {
		return nil, nil, err
	}
	return &req, ledger, nil
}

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
	SiteId        int             `json:"site_id" gorm:"primaryKey"`
	Rate          decimal.Decimal `json:"rate" gorm:"type:decimal(10,6);not null"`
	Enabled       bool            `json:"enabled"`
	LegacyEnabled *bool           `json:"-"`
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
	TeamId     int64  `json:"team_id,omitempty" gorm:"not null;default:0"`
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
