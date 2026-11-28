package model

import (
	"crypto/rand"
	"math/big"
	"strings"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Partner referral, hourly settlement, principal consumption events, and negative carry-forward offsets (R-49).

const (
	AdtyPartnerLeadStatusPending  = "pending"
	AdtyPartnerLeadStatusApproved = "approved"
	AdtyPartnerLeadStatusRejected = "rejected"

	AdtyCommissionOffsetScopeSiteEarning  = "site_earning"
	AdtyCommissionOffsetScopeSitePartner  = "site_partner"
	AdtyCommissionOffsetScopeMainReferral = "main_referral"
	AdtyCommissionOffsetScopeSiteInvite   = "site_invite"
)

// AdtyPartnerLead stores a prospective subsite partner inquiry submitted on the main site (/partner).
type AdtyPartnerLead struct {
	Id              int64  `json:"id" gorm:"primaryKey"`
	ReferrerSiteId  int    `json:"referrer_site_id" gorm:"index;not null;default:0"`
	ReferralCode    string `json:"referral_code" gorm:"type:varchar(32)"`
	ContactName     string `json:"contact_name" gorm:"type:varchar(100);not null"`
	ContactChannel  string `json:"contact_channel" gorm:"type:varchar(32);not null"`
	ContactValue    string `json:"contact_value" gorm:"type:varchar(200);not null"`
	EstimatedVolume string `json:"estimated_volume" gorm:"type:varchar(128)"`
	Note            string `json:"note" gorm:"type:text"`
	Status          string `json:"status" gorm:"type:varchar(24);index;not null;default:'pending'"`
	OpenedSiteId    int    `json:"opened_site_id" gorm:"index;not null;default:0"`
	ReviewedBy      int    `json:"reviewed_by" gorm:"index;not null;default:0"`
	ReviewedAt      int64  `json:"reviewed_at" gorm:"not null;default:0"`
	ReviewNote      string `json:"review_note" gorm:"type:text"`
	IpHash          string `json:"-" gorm:"type:varchar(64);index"`
	CreatedAt       int64  `json:"created_at" gorm:"autoCreateTime;index"`
	UpdatedAt       int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (AdtyPartnerLead) TableName() string { return "adty_partner_leads" }

// AdtySitePartnerEarning records one hourly settled partner referral payout from main site to referrer site A
// based on referred site B's net platform supply consumption.
type AdtySitePartnerEarning struct {
	Id                 int64           `json:"id" gorm:"primaryKey"`
	HourSlot           string          `json:"hour_slot" gorm:"type:varchar(48);uniqueIndex:ux_adty_partner_earning_slot,priority:1;index;not null"`
	ReferrerSiteId     int             `json:"referrer_site_id" gorm:"uniqueIndex:ux_adty_partner_earning_slot,priority:2;index;not null"`
	ReferredSiteId     int             `json:"referred_site_id" gorm:"uniqueIndex:ux_adty_partner_earning_slot,priority:3;index;not null"`
	SupplyNetUSD       decimal.Decimal `json:"supply_net_usd" gorm:"type:decimal(20,8);not null;default:0"`
	RatePercent        decimal.Decimal `json:"rate_percent" gorm:"type:decimal(10,6);not null;default:0"`
	GrossCommissionUSD decimal.Decimal `json:"gross_commission_usd" gorm:"type:decimal(20,8);not null;default:0"`
	OffsetAppliedUSD   decimal.Decimal `json:"offset_applied_usd" gorm:"type:decimal(20,8);not null;default:0"`
	CreditedUSD        decimal.Decimal `json:"credited_usd" gorm:"type:decimal(20,8);not null;default:0"`
	SettledAt          int64           `json:"settled_at" gorm:"index;not null;default:0"`
	CreatedAt          int64           `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtySitePartnerEarning) TableName() string { return "adty_site_partner_earnings" }

// AdtyOperatingPrincipalEvent records an immutable subsite operating principal spend or refund delta
// settled in batch every hour (and flushed immediately on site close) to avoid per-request wallet row locks.
type AdtyOperatingPrincipalEvent struct {
	Id           int64  `json:"id" gorm:"primaryKey"`
	OperationKey string `json:"operation_key" gorm:"type:varchar(191);uniqueIndex:ux_adty_op_principal_evt,priority:1;not null"`
	Direction    string `json:"direction" gorm:"type:varchar(16);uniqueIndex:ux_adty_op_principal_evt,priority:2;not null"` // spend | refund
	SiteId       int    `json:"site_id" gorm:"uniqueIndex:ux_adty_op_principal_evt,priority:3;index;not null"`
	UserId       int    `json:"user_id" gorm:"index;not null"`
	Quota        int64  `json:"quota" gorm:"not null"` // negative for spend, positive for refund (wallet delta direction)
	Settled      bool   `json:"settled" gorm:"index;not null;default:false"`
	HourSlot     string `json:"hour_slot" gorm:"type:varchar(48);index"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime;index"`
}

func (AdtyOperatingPrincipalEvent) TableName() string { return "adty_operating_principal_events" }

// AdtyPrincipalConsumeEvent records immutable consumption/refund deltas of real paid personal top-up principal
// (strictly excluding bonus, vouchers, commission transfers, manual quota adjustments, and team balances).
type AdtyPrincipalConsumeEvent struct {
	Id           int64           `json:"id" gorm:"primaryKey"`
	OperationKey string          `json:"operation_key" gorm:"type:varchar(191);uniqueIndex:ux_adty_prin_consume_evt,priority:1;not null"`
	Direction    string          `json:"direction" gorm:"type:varchar(16);uniqueIndex:ux_adty_prin_consume_evt,priority:2;not null"` // spend | refund
	SiteId       int             `json:"site_id" gorm:"uniqueIndex:ux_adty_prin_consume_evt,priority:3;index;not null"`
	UserId       int             `json:"user_id" gorm:"index;not null"`
	Quota        int64           `json:"quota" gorm:"not null"` // positive for spend, negative for refund
	AmountUSD    decimal.Decimal `json:"amount_usd" gorm:"type:decimal(20,8);not null;default:0"`
	Settled      bool            `json:"settled" gorm:"index;not null;default:false"`
	HourSlot     string          `json:"hour_slot" gorm:"type:varchar(48);index"`
	CreatedAt    int64           `json:"created_at" gorm:"autoCreateTime;index"`
}

func (AdtyPrincipalConsumeEvent) TableName() string { return "adty_principal_consume_events" }

// AdtyCommissionOffset tracks pending negative carry-forward balances caused by cross-hour refunds
// so future positive hourly settlements automatically offset the deficit without driving wallets negative.
type AdtyCommissionOffset struct {
	Id                 int64           `json:"id" gorm:"primaryKey"`
	Scope              string          `json:"scope" gorm:"type:varchar(32);uniqueIndex:ux_adty_comm_offset,priority:1;not null"`
	OwnerKey           string          `json:"owner_key" gorm:"type:varchar(64);uniqueIndex:ux_adty_comm_offset,priority:2;not null"`
	PendingNegativeUSD decimal.Decimal `json:"pending_negative_usd" gorm:"type:decimal(20,8);not null;default:0"`
	UpdatedAt          int64           `json:"updated_at" gorm:"autoUpdateTime"`
}

func (AdtyCommissionOffset) TableName() string { return "adty_commission_offsets" }

// AdtyHourlySettlementRun tracks completed hourly settlement slots for idempotency and restart catch-up.
type AdtyHourlySettlementRun struct {
	HourSlot     string `json:"hour_slot" gorm:"primaryKey;type:varchar(48)"`
	WindowStart  int64  `json:"window_start"`
	WindowEnd    int64  `json:"window_end"`
	Status       string `json:"status" gorm:"type:varchar(16);index"`
	SiteCount    int    `json:"site_count"`
	PartnerCount int    `json:"partner_count"`
	MainRefCount int    `json:"main_ref_count"`
	SiteRefCount int    `json:"site_ref_count"`
	FinishedAt   int64  `json:"finished_at"`
}

func (AdtyHourlySettlementRun) TableName() string { return "adty_hourly_settlement_runs" }

// AdtyPrincipalBatchDelta carries exact batch-level principal consumption (+quota) or refund (-quota).
type AdtyPrincipalBatchDelta struct {
	BatchId int64
	SiteId  int
	Source  string
	Quota   int64
}

// AdtyAfterPrincipalDeltaTx runs inside the billing/refund transaction for all non-legacy principal
// batch movements (both main-site site_id=0 and subsite site_id>0).
var AdtyAfterPrincipalDeltaTx func(tx *gorm.DB, userID int, operationKey string, deltas []AdtyPrincipalBatchDelta) error

func adtyNotifyPrincipalDeltaTx(tx *gorm.DB, userID int, operationKey string, deltas []AdtyPrincipalBatchDelta) error {
	if len(deltas) == 0 || AdtyAfterPrincipalDeltaTx == nil {
		return nil
	}
	return AdtyAfterPrincipalDeltaTx(tx, userID, operationKey, deltas)
}

const adtyPartnerCodeAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

// GenerateAdtyPartnerCode returns a 10-character opaque random code containing no site ID, name, or domain.
func GenerateAdtyPartnerCode() (string, error) {
	var b strings.Builder
	b.Grow(10)
	maxIdx := big.NewInt(int64(len(adtyPartnerCodeAlphabet)))
	for range 10 {
		n, err := rand.Int(rand.Reader, maxIdx)
		if err != nil {
			return "", err
		}
		b.WriteByte(adtyPartnerCodeAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// AllocateAdtySitePartnerCodeTx generates a unique opaque partner referral code on adty_sites.
func AllocateAdtySitePartnerCodeTx(tx *gorm.DB) (string, error) {
	for range 24 {
		code, err := GenerateAdtyPartnerCode()
		if err != nil {
			return "", err
		}
		var count int64
		if err := tx.Model(&AdtySite{}).Where("partner_code = ?", code).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return code, nil
		}
	}
	return GenerateAdtyPartnerCode()
}

// backfillAdtySitePartnerCodes ensures every existing subsite has a unique opaque partner code.
func backfillAdtySitePartnerCodes(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&AdtySite{}) {
		return nil
	}
	var siteIDs []int
	if err := db.Model(&AdtySite{}).Where("partner_code IS NULL OR partner_code = ''").Pluck("id", &siteIDs).Error; err != nil {
		return err
	}
	for _, id := range siteIDs {
		code, err := AllocateAdtySitePartnerCodeTx(db)
		if err != nil {
			return err
		}
		if err := db.Model(&AdtySite{}).Where("id = ? AND (partner_code IS NULL OR partner_code = '')", id).Update("partner_code", code).Error; err != nil {
			return err
		}
	}
	return nil
}

// adtyPartnerExcludedQuotaTx calculates how much of a subsite operation's earningQuota was funded by
// platform-free Root compensation/trial credits (manual adjustments "adjust:*" or expiring_bonus),
// which are excluded from the referred site's supply base for partner 5% commission (R-49).
// Subsite customer online top-ups, site-owner-funded site_customer vouchers, site USDT bonuses,
// and team balances are all backed by real supply revenue and remain included.
func adtyPartnerExcludedQuotaTx(tx *gorm.DB, userID int, operationKey string, earningQuota int, teamPaid bool) (int64, error) {
	if tx == nil || teamPaid || userID <= 0 || operationKey == "" || earningQuota == 0 {
		return 0, nil
	}
	hasAlloc, err := AdtyTableExists(tx, &AdtyFundAllocation{})
	if err != nil || !hasAlloc {
		return 0, err
	}
	hasBatch, err := AdtyTableExists(tx, &AdtyFundBatch{})
	if err != nil || !hasBatch {
		return 0, err
	}
	var allocs []AdtyFundAllocation
	if err := tx.Select("batch_id", "kind", "source", "settled").
		Where("user_id = ? AND operation_id = ? AND settled > 0", userID, operationKey).
		Find(&allocs).Error; err != nil {
		return 0, err
	}
	if len(allocs) == 0 {
		return 0, nil
	}
	var excluded int64
	for _, alloc := range allocs {
		if strings.HasPrefix(alloc.Source, "adjust:") {
			excluded += alloc.Settled
			continue
		}
		if alloc.Kind == AdtyFundBatchKindExpiringBonus {
			excluded += alloc.Settled
			continue
		}
		if isAdtyBonusBatchKind(alloc.Kind) && alloc.BatchId > 0 {
			var batch AdtyFundBatch
			if err := tx.Select("id", "kind", "gift_category", "expires_at").Where("id = ?", alloc.BatchId).Limit(1).Find(&batch).Error; err != nil {
				return 0, err
			}
			if batch.Id > 0 && batch.IsExpiringGift() {
				excluded += alloc.Settled
			}
		}
	}
	if excluded == 0 {
		return 0, nil
	}
	absQuota := int64(earningQuota)
	if absQuota < 0 {
		absQuota = -absQuota
	}
	if excluded > absQuota {
		excluded = absQuota
	}
	if earningQuota < 0 {
		return -excluded, nil
	}
	return excluded, nil
}
