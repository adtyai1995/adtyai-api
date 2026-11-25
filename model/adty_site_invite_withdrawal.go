package model

import (
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"math"
	"strconv"
)

const (
	AdtySiteInviteWithdrawPending  = "pending"
	AdtySiteInviteWithdrawApproved = "approved"
	AdtySiteInviteWithdrawRejected = "rejected"
	AdtySiteInviteWithdrawCanceled = "canceled"
	AdtySiteInviteWithdrawPaid     = "paid"

	AdtySiteInviteWithdrawItemFrozen   = "frozen"
	AdtySiteInviteWithdrawItemReleased = "released"
	AdtySiteInviteWithdrawItemPaid     = "paid"
)

// AdtySiteInviteWithdrawal is one site-customer invite-commission withdrawal.
// It is not a site-owner earnings withdrawal and not a main-site referral withdrawal.
type AdtySiteInviteWithdrawal struct {
	Id               int64           `json:"id"`
	SiteId           int             `json:"site_id" gorm:"index"`
	UserId           int             `json:"user_id" gorm:"index"`
	RequestedUSD     decimal.Decimal `json:"requested_usd" gorm:"type:decimal(20,8);not null;default:0"`
	AmountUSD        decimal.Decimal `json:"amount_usd" gorm:"type:decimal(20,8);not null"`
	FeeUSD           decimal.Decimal `json:"fee_usd" gorm:"type:decimal(20,8);not null;default:0"`
	NetUSD           decimal.Decimal `json:"net_usd" gorm:"type:decimal(20,8);not null"`
	Method           string          `json:"method" gorm:"type:varchar(16)"`
	Network          string          `json:"network" gorm:"type:varchar(16)"`
	AccountNo        string          `json:"account_no" gorm:"type:varchar(128)"`
	AccountName      string          `json:"account_name" gorm:"type:varchar(128)"`
	QRImageURL       string          `json:"qr_image_url" gorm:"type:text"`
	Rate             string          `json:"rate" gorm:"type:varchar(32)"`
	PayoutAmount     string          `json:"payout_amount" gorm:"type:varchar(32)"`
	PayoutCurrency   string          `json:"payout_currency" gorm:"type:varchar(8)"`
	Status           string          `json:"status" gorm:"type:varchar(16);index"`
	IdempotencyKey   string          `json:"-" gorm:"uniqueIndex;type:varchar(128)"`
	CancelDeadline   int64           `json:"cancel_deadline" gorm:"not null;default:0"`
	QuoteID          string          `json:"-" gorm:"size:64"`
	PaidBy           int             `json:"paid_by" gorm:"not null;default:0"`
	FailureBy        int             `json:"failure_by" gorm:"not null;default:0"`
	FailureAt        int64           `json:"failure_at" gorm:"not null;default:0"`
	LegacyDecisionBy int             `json:"legacy_decision_by,omitempty" gorm:"not null;default:0"`
	LegacyDecisionAt int64           `json:"legacy_decision_at,omitempty" gorm:"not null;default:0"`
	ReviewedBy       int             `json:"reviewed_by"`
	ReviewedAt       int64           `json:"reviewed_at"`
	PaidAt           int64           `json:"paid_at"`
	ReceiptKey       *string         `json:"-" gorm:"size:64;uniqueIndex"`
	TxID             string          `json:"tx_id" gorm:"type:varchar(128)"`
	ProofNote        string          `json:"proof_note" gorm:"type:varchar(255)"`
	RejectReason     string          `json:"reject_reason" gorm:"type:varchar(255)"`
	CreatedAt        int64           `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtySiteInviteWithdrawal) TableName() string { return "adty_site_invite_withdrawals" }

// AdtySiteInviteWithdrawalItem ties a withdrawal to the commission rows it holds.
type AdtySiteInviteWithdrawalItem struct {
	Id           int64           `json:"id"`
	ReleasedUSD  decimal.Decimal `json:"released_usd" gorm:"type:decimal(20,8);not null;default:0"`
	RefundUSD    decimal.Decimal `json:"refund_usd" gorm:"type:decimal(20,8);not null;default:0"`
	WithdrawalId int64           `json:"withdrawal_id" gorm:"index"`
	CommissionId int64           `json:"commission_id" gorm:"index"`
	AmountUSD    decimal.Decimal `json:"amount_usd" gorm:"type:decimal(20,8);not null"`
	Status       string          `json:"status" gorm:"type:varchar(16)"`
}

func (AdtySiteInviteWithdrawalItem) TableName() string { return "adty_site_invite_withdrawal_items" }

// Site-only upgrades can encounter either no customer withdrawals or the older
// table. Use the previous platform window once for requests already submitted.
func backfillSiteInviteWithdrawals(db *gorm.DB) error {
	if !db.Migrator().HasTable(&AdtySiteInviteWithdrawal{}) {
		return nil
	}
	if err := db.AutoMigrate(&AdtySiteInviteWithdrawal{}); err != nil {
		return err
	}
	minutes := int64(adty_setting.GetReferralSetting().CancelWindowMinutes)
	if db.Migrator().HasTable(&Option{}) {
		var opt Option
		result := db.Where(&Option{Key: "adty_referral_setting.cancel_window_minutes"}).Limit(1).Find(&opt)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			parsed, err := strconv.ParseInt(opt.Value, 10, 64)
			if err != nil {
				return err
			}
			minutes = parsed
		}
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var rows []AdtySiteInviteWithdrawal
		return tx.Where("cancel_deadline = 0 AND created_at > 0").FindInBatches(&rows, 250, func(batch *gorm.DB, _ int) error {
			for _, row := range rows {
				deadline := int64(math.MaxInt64)
				if minutes <= (math.MaxInt64-row.CreatedAt)/60 {
					deadline = row.CreatedAt + max(minutes, 0)*60
				}
				changes := map[string]any{"cancel_deadline": deadline}
				// The legacy paid transition itself stored the registering actor in reviewed_by.
				if row.Status == AdtySiteInviteWithdrawPaid && row.Method != "balance" && row.PaidBy == 0 && row.ReviewedBy > 0 {
					if row.PaidAt > 0 && (row.TxID != "" || row.ProofNote != "") {
						changes["paid_by"] = row.ReviewedBy
					} else {
						changes["legacy_decision_by"] = row.ReviewedBy
					}
					changes["reviewed_by"] = 0
				}
				// Old rejection and failed-payment transitions wrote identical fields.
				if row.Status == AdtySiteInviteWithdrawRejected && row.ReviewedBy > 0 && row.FailureAt == 0 {
					changes["legacy_decision_by"], changes["legacy_decision_at"] = row.ReviewedBy, row.ReviewedAt
					changes["reviewed_by"], changes["reviewed_at"] = 0, 0
				}
				if row.Method == "usdt" && row.PayoutAmount == "" && row.NetUSD.IsPositive() {
					if row.Status != AdtySiteInviteWithdrawPaid || row.NetUSD.Equal(row.NetUSD.Round(6)) {
						changes["payout_amount"], changes["payout_currency"] = row.NetUSD.StringFixed(6), "USDT"
					}
				}
				if err := batch.Model(&AdtySiteInviteWithdrawal{}).Where("id = ? AND cancel_deadline = 0", row.Id).Updates(changes).Error; err != nil {
					return err
				}
			}
			return nil
		}).Error
	})
}
