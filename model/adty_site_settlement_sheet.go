package model

import (
	"encoding/json"
	"errors"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ErrAdtySettlementSheetImmutable rejects a direct rewrite or delete.
var ErrAdtySettlementSheetImmutable = errors.New("settlement sheet identity is immutable")

// AdtySiteSettlementSheet is a previous-business-day check sheet.
// It stores sums of recorded facts and does not credit a wallet.
type AdtySiteSettlementSheet struct {
	Id                        int64                              `json:"id"`
	SiteId                    int                                `json:"site_id" gorm:"uniqueIndex:idx_adty_site_settlement"`
	BusinessDay               string                             `json:"business_day" gorm:"uniqueIndex:idx_adty_site_settlement;type:varchar(10)"`
	Timezone                  string                             `json:"timezone" gorm:"type:varchar(64)"`
	DayStart                  int64                              `json:"day_start"`
	DayEnd                    int64                              `json:"day_end"`
	EventWatermark            int64                              `json:"event_watermark"`
	IncludedCount             int64                              `json:"included_count"`
	BatchId                   string                             `json:"batch_id" gorm:"type:varchar(64);uniqueIndex"`
	TopupFaceUSD              decimal.Decimal                    `json:"topup_face_usd" gorm:"type:decimal(20,8);not null;default:0"`
	PaidUSD                   decimal.Decimal                    `json:"paid_usd" gorm:"type:decimal(20,8);not null;default:0"`
	PaidByCurrency            string                             `json:"paid_by_currency" gorm:"type:text"`
	ConsumptionUSD            decimal.Decimal                    `json:"consumption_usd" gorm:"type:decimal(20,8);not null;default:0"`
	OwnerTransferUSD          decimal.Decimal                    `json:"owner_transfer_usd" gorm:"type:decimal(20,8);not null;default:0"`
	OwnerSelfUseTopupUSD      decimal.Decimal                    `json:"owner_self_use_topup_usd" gorm:"type:decimal(20,8);not null;default:0"`
	OwnerSelfUseVoucherUSD    decimal.Decimal                    `json:"owner_self_use_voucher_usd" gorm:"type:decimal(20,8);not null;default:0"`
	OwnerSelfUseRefundUSD     decimal.Decimal                    `json:"owner_self_use_refund_usd" gorm:"type:decimal(20,8);not null;default:0"`
	OwnerSelfUseUSD           decimal.Decimal                    `json:"owner_self_use_usd" gorm:"type:decimal(20,8);not null;default:0"`
	OwnerSelfUseCostUSD       decimal.Decimal                    `json:"owner_self_use_cost_usd" gorm:"type:decimal(20,8);not null;default:0"`
	SupplyCostUSD             decimal.Decimal                    `json:"supply_cost_usd" gorm:"type:decimal(20,8);not null;default:0"`
	SpreadUSD                 decimal.Decimal                    `json:"spread_usd" gorm:"type:decimal(20,8);not null;default:0"`
	GiftReserveUSD              decimal.Decimal                    `json:"gift_reserve_usd" gorm:"type:decimal(20,8);not null;default:0"`
	GiftFundingUSD              decimal.Decimal                    `json:"gift_funding_usd" gorm:"type:decimal(20,8);not null;default:0"`
	GiftReleaseUSD              decimal.Decimal                    `json:"gift_release_usd" gorm:"type:decimal(20,8);not null;default:0"`
	VoucherFundingUSD           decimal.Decimal                    `json:"voucher_funding_usd" gorm:"type:decimal(20,8);not null;default:0"`
	EarningToMarginUSD          decimal.Decimal                    `json:"earning_to_margin_usd" gorm:"type:decimal(20,8);not null;default:0"`
	MarginTopupUSD              decimal.Decimal                    `json:"margin_topup_usd" gorm:"type:decimal(20,8);not null;default:0"`
	CommissionPaidUSD           decimal.Decimal                    `json:"commission_paid_usd" gorm:"type:decimal(20,8);not null;default:0"`
	CommissionClawbackReturnUSD decimal.Decimal                    `json:"commission_clawback_return_usd" gorm:"type:decimal(20,8);not null;default:0"`
	TrialExpenseUSD             decimal.Decimal                    `json:"trial_expense_usd" gorm:"type:decimal(20,8);not null;default:0"`
	CommissionUSD             decimal.Decimal                    `json:"commission_usd" gorm:"type:decimal(20,8);not null;default:0"`
	PendingCoverageUnverified bool                               `json:"pending_coverage_unverified" gorm:"not null;default:false"`
	PendingCoverageUSD        decimal.Decimal                    `json:"pending_coverage_usd" gorm:"type:decimal(20,8);not null;default:0"`
	MarginDeltaUSD            decimal.Decimal                    `json:"margin_delta_usd" gorm:"type:decimal(20,8);not null;default:0"`
	EarningsDeltaUSD          decimal.Decimal                    `json:"earnings_delta_usd" gorm:"type:decimal(20,8);not null;default:0"`
	EarningsAvailableDeltaUSD decimal.Decimal                    `json:"earnings_available_delta_usd" gorm:"type:decimal(20,8);not null;default:0"`
	EarningsFrozenDeltaUSD    decimal.Decimal                    `json:"earnings_frozen_delta_usd" gorm:"type:decimal(20,8);not null;default:0"`
	WithdrawalUSD             decimal.Decimal                    `json:"withdrawal_usd" gorm:"type:decimal(20,8);not null;default:0"`
	WithdrawalFeeUSD          decimal.Decimal                    `json:"withdrawal_fee_usd" gorm:"type:decimal(20,8);not null;default:0"`
	AdjustmentUSD             decimal.Decimal                    `json:"adjustment_usd" gorm:"type:decimal(20,8);not null;default:0"`
	AnomalyCount              int64                              `json:"anomaly_count"`
	LegacyCoverage            []AdtySettlementCoverageAdjustment `json:"legacy_coverage,omitempty" gorm:"foreignKey:SheetId"`
	Revisions                 []AdtySettlementRevision           `json:"revisions,omitempty" gorm:"foreignKey:SheetId"`
	GeneratedAt               int64                              `json:"generated_at"`
}

func (AdtySiteSettlementSheet) TableName() string { return "adty_site_settlement_sheets" }

func (AdtySiteSettlementSheet) BeforeUpdate(*gorm.DB) error { return ErrAdtySettlementSheetImmutable }

func (AdtySiteSettlementSheet) BeforeDelete(*gorm.DB) error {
	return ErrAdtySettlementSheetImmutable
}

type AdtySettlementCoverageAdjustment struct {
	Id             int64           `json:"id"`
	SheetId        int64           `json:"sheet_id" gorm:"index"`
	BeforeUSD      decimal.Decimal `json:"before_usd" gorm:"type:decimal(20,8);not null"`
	AfterUSD       decimal.Decimal `json:"after_usd" gorm:"type:decimal(20,8);not null"`
	EventWatermark int64           `json:"event_watermark"`
	CreatedAt      int64           `json:"created_at"`
}

// Original sheets remain immutable. A revision describes the recorded business
// interval, its prior figures and all figures recalculated from that interval.
type AdtySettlementRevision struct {
	Id        int64           `json:"id"`
	SheetId   int64           `json:"sheet_id" gorm:"index"`
	Before    json.RawMessage `json:"before" gorm:"type:text;serializer:json"`
	After     json.RawMessage `json:"after" gorm:"type:text;serializer:json"`
	Sources   []string        `json:"sources" gorm:"type:text;serializer:json"`
	CreatedAt int64           `json:"created_at"`
}

func (AdtySettlementRevision) BeforeUpdate(*gorm.DB) error { return ErrAdtySettlementSheetImmutable }
func (AdtySettlementRevision) BeforeDelete(*gorm.DB) error { return ErrAdtySettlementSheetImmutable }

type AdtySettlementScan struct {
	SiteId      int    `gorm:"primaryKey;autoIncrement:false"`
	NextDay     string `gorm:"size:10"`
	LastError   string
	LastSheetId int64
}
