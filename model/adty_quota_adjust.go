package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	ErrAdtyAdjustReasonRequired = errors.New("adjustment reason required")
	ErrAdtyAdjustReasonTooLong  = errors.New("adjustment reason too long")
	ErrAdtyAdjustKeyTooLong     = errors.New("idempotency key too long")
	ErrAdtyAdjustKeyRequired    = errors.New("idempotency key required")
	ErrAdtyAdjustOrderTooLong   = errors.New("order reference too long")
	ErrAdtyQuotaAdjustNoChange  = common.NewMessage("Quota is unchanged")
)

const (
	adtyAdjustReasonMaxChars    = 200
	adtyAdjustKeyMaxLen         = 64
	AdtyAdjustDirectionIncrease = "increase"
	AdtyAdjustDirectionDecrease = "decrease"
	AdtyAdjustDirectionOverride = "override"
	AdtyAdjustSourcePlatform    = "platform_correction"
	AdtyAdjustSourceBatch       = "specified_batch"
)

type AdtyQuotaAdjustProvenance struct {
	OrderRef      string `json:"order_ref"`
	FundingSource string `json:"funding_source"`
	BatchKind     string `json:"batch_kind"`
	BatchSource   string `json:"batch_source"`
	// Retained for internal callers; authority is always the authenticated actor.
	ApprovalRef string `json:"-"`
	ApproverID  int    `json:"-"`
	ExecutorID  int    `json:"-"`
	SiteID      *int   `json:"-"`
}

type AdtyQuotaAdjustInput struct {
	UserID, OperatorID, OperatorRole int
	Mode                             string
	Value                            int
	Reason, IdempotencyKey           string
	Provenance                       AdtyQuotaAdjustProvenance
}

// A correction is a new financial fact, never an edit to the original order.
type AdtyFinancialAdjustment struct {
	Id             int64  `json:"id"`
	UserId         int    `json:"user_id" gorm:"index;not null"`
	SiteId         int    `json:"site_id" gorm:"index"`
	Mode           string `json:"mode" gorm:"type:varchar(16);not null"`
	Direction      string `json:"direction" gorm:"type:varchar(16);not null"`
	RequestedValue int    `json:"requested_value"`
	DeltaQuota     int64  `json:"delta_quota"`
	BalanceBefore  int64  `json:"balance_before"`
	BalanceAfter   int64  `json:"balance_after"`
	RawBefore      int    `json:"raw_before"`
	RawAfter       int    `json:"raw_after"`
	Reason         string `json:"reason" gorm:"type:text"`
	OrderRef       string `json:"order_ref" gorm:"type:varchar(64);index"`
	FundingSource  string `json:"funding_source" gorm:"type:varchar(64)"`
	BatchKind      string `json:"batch_kind" gorm:"type:varchar(32)"`
	BatchSource    string `json:"batch_source" gorm:"type:varchar(64)"`
	ApprovalRef    string `json:"approval_ref" gorm:"type:varchar(128);index"`
	ApproverId     int    `json:"approver_id" gorm:"index"`
	ExecutorId     int    `json:"executor_id" gorm:"index"`
	OperatorRole   int    `json:"operator_role"`
	LedgerId       int64  `json:"ledger_id" gorm:"index"`
	IdempotencyKey string `json:"idempotency_key" gorm:"type:varchar(64);uniqueIndex"`
	CreatedAt      int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtyFinancialAdjustment) TableName() string { return "adty_financial_adjustments" }

// The service hook uses the existing site-wallet transaction and fails closed.
var AdtyFundFinancialAdjustmentTx func(*gorm.DB, AdtyFinancialAdjustment) error

type AdtyQuotaAdjustResult struct {
	UserQuotaAdjustment
	Replayed      bool   `json:"replayed"`
	Direction     string `json:"direction"`
	DeltaQuota    int64  `json:"delta_quota"`
	BalanceBefore int64  `json:"balance_before"`
	BalanceAfter  int64  `json:"balance_after"`
	Reason        string `json:"reason"`
	OrderRef      string `json:"order_ref"`
	ApprovalRef   string `json:"approval_ref"`
	ApproverID    int    `json:"approver_id"`
	ExecutorID    int    `json:"executor_id"`
	SiteID        int    `json:"site_id"`
	LedgerID      int64  `json:"ledger_id"`
	AdjustmentID  int64  `json:"adjustment_id"`
}

func adtyAdjustFundSource(idemKey string) string { return "adjust:" + common.Sha1([]byte(idemKey)) }

// The caller holds the user lock. Only a real successful order can be corrected.
func ValidateAdtyAdjustmentOrderTx(tx *gorm.DB, userID int, orderRef string, siteID *int) (*AdtyTopupOrder, error) {
	orderRef = strings.TrimSpace(orderRef)
	if orderRef == "" || len(orderRef) > 64 {
		return nil, common.NewMessage("A valid original order is required")
	}
	var order AdtyTopupOrder
	if err := tx.Where("trade_no = ? AND user_id = ?", orderRef, userID).First(&order).Error; err != nil {
		return nil, err
	}
	if order.TeamId != 0 || order.IsOwnerBusiness() {
		return nil, common.NewMessage("No permission to perform this action")
	}
	if siteID != nil && order.SiteId != *siteID {
		return nil, common.NewMessage("No permission to perform this action")
	}
	var topup TopUp
	if err := tx.Where("trade_no = ? AND user_id = ? AND status IN ?", orderRef, userID, []string{common.TopUpStatusSuccess, TopUpStatusRefunded}).First(&topup).Error; err != nil {
		return nil, err
	}
	return &order, nil
}

func resolveAdtyAdjustDirection(mode string, delta int64) string {
	if delta > 0 {
		return AdtyAdjustDirectionIncrease
	}
	if delta < 0 {
		return AdtyAdjustDirectionDecrease
	}
	return AdtyAdjustDirectionOverride
}

func AdjustUserQuotaWithProvenance(input AdtyQuotaAdjustInput) (*AdtyQuotaAdjustResult, error) {
	return AdjustUserQuotaWithLedger(input.UserID, input.OperatorID, input.OperatorRole, input.Mode, input.Value, input.Reason, input.IdempotencyKey, input.Provenance)
}

// User, batch, ledger, approval and durable audit are committed together.
// A repeated key must match the full original request and never changes money.
func AdjustUserQuotaWithLedger(userID, operatorID, operatorRole int, mode string, value int, reason, idemKey string, provenance ...AdtyQuotaAdjustProvenance) (*AdtyQuotaAdjustResult, error) {
	return AdjustUserQuotaWithLedgerForActor(userID, operatorID, operatorRole, false, mode, value, reason, idemKey, provenance...)
}

func AdjustUserQuotaWithLedgerForActor(userID, operatorID, operatorRole int, denyAdminTarget bool, mode string, value int, reason, idemKey string, provenance ...AdtyQuotaAdjustProvenance) (*AdtyQuotaAdjustResult, error) {
	reason, idemKey = strings.TrimSpace(reason), strings.TrimSpace(idemKey)
	if reason == "" {
		return nil, ErrAdtyAdjustReasonRequired
	}
	if utf8.RuneCountInString(reason) > adtyAdjustReasonMaxChars {
		return nil, ErrAdtyAdjustReasonTooLong
	}
	if idemKey == "" {
		return nil, ErrAdtyAdjustKeyRequired
	}
	if len(idemKey) > adtyAdjustKeyMaxLen {
		return nil, ErrAdtyAdjustKeyTooLong
	}
	if userID <= 0 || operatorID <= 0 || (mode != "add" && mode != "subtract" && mode != "override") || (mode != "override" && value <= 0) {
		return nil, ErrInvalidUserQuotaAdjustment
	}
	if value > common.MaxWalletQuota || value < -common.MaxWalletQuota {
		return nil, ErrWalletQuotaLimitExceeded
	}
	if len(provenance) != 1 {
		return nil, common.NewMessage("A valid original order is required")
	}
	prov := provenance[0]
	prov.OrderRef, prov.FundingSource = strings.TrimSpace(prov.OrderRef), strings.TrimSpace(prov.FundingSource)
	prov.BatchKind, prov.BatchSource = strings.TrimSpace(prov.BatchKind), strings.TrimSpace(prov.BatchSource)
	if prov.OrderRef == "" || len(prov.OrderRef) > 64 || len(prov.BatchSource) > 64 {
		return nil, common.NewMessage("A valid original order is required")
	}
	if (prov.ApproverID != 0 && prov.ApproverID != operatorID) || (prov.ExecutorID != 0 && prov.ExecutorID != operatorID) || prov.SiteID != nil || prov.ApprovalRef != "" {
		return nil, ErrUserQuotaPermission
	}
	var result AdtyQuotaAdjustResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
			return err
		}
		var actor User
		if err := tx.First(&actor, operatorID).Error; err != nil {
			return err
		}
		if actor.Role != operatorRole || actor.Role < common.RoleAdminUser || actor.StaffSiteId != 0 || actor.Status != common.UserStatusEnabled || (actor.Role != common.RoleRootUser && actor.Role <= user.Role) {
			return ErrUserQuotaPermission
		}
		if denyAdminTarget && user.Role >= common.RoleAdminUser {
			return ErrAdminTargetSessionRequired
		}
		var row AdtyFinancialAdjustment
		lookup := tx.Where("idempotency_key = ?", idemKey).Limit(1).Find(&row)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected != 0 {
			if row.UserId != userID || row.ExecutorId != operatorID || row.Mode != mode || row.RequestedValue != value || row.Reason != reason || row.OrderRef != prov.OrderRef || row.FundingSource != prov.FundingSource || row.BatchKind != prov.BatchKind || row.BatchSource != prov.BatchSource {
				return ErrAdtyIdempotencyConflict
			}
			var entry AdtyBalanceLedger
			if err := tx.First(&entry, row.LedgerId).Error; err != nil {
				return err
			}
			if entry.UserId != userID || entry.RefType != "financial_adjustment" || entry.RefId != strconv.FormatInt(row.Id, 10) || entry.DeltaQuota != row.DeltaQuota {
				return ErrAdtyIdempotencyConflict
			}
			result = adtyFinancialAdjustmentResult(user, row, true)
			return nil
		}
		order, err := ValidateAdtyAdjustmentOrderTx(tx, userID, prov.OrderRef, nil)
		if err != nil {
			return err
		}
		if order.TeamId != 0 || order.IsOwnerBusiness() {
			return common.NewMessage("No permission to perform this action")
		}
		if err := RejectRestrictedAdtyFundWrite(tx, AdtyFundWrite{AccountID: userID, SiteID: order.SiteId, OrderRef: prov.OrderRef}); err != nil {
			return err
		}
		if bs := strings.TrimSpace(prov.BatchSource); bs != "" && bs != strings.TrimSpace(prov.OrderRef) {
			if err := RejectRestrictedAdtyFundWrite(tx, AdtyFundWrite{AccountID: userID, SiteID: order.SiteId, OrderRef: bs}); err != nil {
				return err
			}
		}
		after, err := quotaAfterManualAdjust(user.Quota, value, mode)
		if err != nil {
			return err
		}
		liveBefore, err := userWalletBalance(user.Quota, user.QuotaPending)
		if err != nil {
			return err
		}
		liveAfter, err := userWalletBalance(after, user.QuotaPending)
		if err != nil {
			return err
		}
		if mode != "add" && liveAfter < 0 {
			return ErrAdtyQuotaFloor
		}
		delta := int64(after) - int64(user.Quota)
		if delta == 0 {
			return ErrAdtyQuotaAdjustNoChange
		}
		batchSiteID := order.SiteId
		if delta < 0 {
			if prov.FundingSource != AdtyAdjustSourceBatch {
				return common.NewMessage("A specific available fund batch is required")
			}
			// R10-033: When a customer's original subsite has closed and migrated the
			// user and their batches to main site (site_id = 0), target site_id = 0
			// and do not debit the closed site's drained operating wallet.
			if batchSiteID > 0 && user.SiteId == 0 {
				batchSiteID = 0
			} else if batchSiteID > 0 {
				hasSiteTable, err := AdtyTableExists(tx, &AdtySite{})
				if err != nil {
					return err
				}
				if hasSiteTable {
					var siteStatus string
					if err := tx.Model(&AdtySite{}).Select("status").Where("id = ?", batchSiteID).Limit(1).Scan(&siteStatus).Error; err != nil {
						return err
					}
					if siteStatus == AdtySiteStatusClosed {
						batchSiteID = 0
					}
				}
			}
			if err := ReduceAdtyAdjustmentBatchTx(tx, userID, batchSiteID, prov.BatchSource, prov.BatchKind, -delta); err != nil {
				return err
			}
		} else if delta > 0 {
			if prov.BatchKind != "" || prov.BatchSource != "" {
				return common.NewMessage("Select an authorized funding source")
			}
			if user.SiteId != order.SiteId {
				return common.NewMessage("No permission to perform this action")
			}
			if order.SiteId == 0 && prov.FundingSource != AdtyAdjustSourcePlatform {
				return common.NewMessage("Select an authorized funding source")
			}
			if order.SiteId > 0 && prov.FundingSource != AdtyStaffFundingSourceEarnings && prov.FundingSource != AdtyStaffFundingSourceMargin {
				return common.NewMessage("Select an authorized funding source")
			}
		}
		row = AdtyFinancialAdjustment{UserId: userID, SiteId: order.SiteId, Mode: mode, Direction: resolveAdtyAdjustDirection(mode, delta), RequestedValue: value, DeltaQuota: delta, BalanceBefore: int64(liveBefore), BalanceAfter: int64(liveAfter), RawBefore: user.Quota, RawAfter: after, Reason: reason, OrderRef: prov.OrderRef, FundingSource: prov.FundingSource, BatchKind: prov.BatchKind, BatchSource: prov.BatchSource, ApproverId: operatorID, ExecutorId: operatorID, OperatorRole: actor.Role, IdempotencyKey: idemKey}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		row.ApprovalRef = fmt.Sprintf("financial_adjustment:%d:approved:%d", row.Id, operatorID)
		if delta < 0 && batchSiteID > 0 && prov.BatchKind == AdtyFundBatchKindPrincipal {
			// Reducing a subsite customer's principal lot must symmetrically debit
			// the subsite operating wallet to preserve 1:1 principal backing (R1-007 / R2-034 fail-closed).
			if AdtyAfterPrincipalReducedTx == nil {
				return common.NewMessage("Site operating wallet principal reduction hook is unavailable")
			}
			if err := AdtyAfterPrincipalReducedTx(tx, batchSiteID, -delta, "financial_adjustment", fmt.Sprintf("%d", row.Id), fmt.Sprintf("financial-adjustment:%d:operating_debit", row.Id)); err != nil {
				return err
			}
		}
		if delta > 0 {
			if order.SiteId > 0 {
				if AdtyFundFinancialAdjustmentTx == nil {
					return common.NewMessage("Select an authorized funding source")
				}
				if err := AdtyFundFinancialAdjustmentTx(tx, row); err != nil {
					return err
				}
			}
			if err := CreateAdtyFundBatchesWithSiteTx(tx, userID, AdtyFundBatchSiteID(order.SiteId), adtyAdjustFundSource(idemKey), delta, 0); err != nil {
				return err
			}
		}
		if after != user.Quota {
			updated := tx.Model(&User{}).Where("id = ?", userID).Update("quota", after)
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
		}
		if err := clampCheckinQuotaDB(tx, userID); err != nil {
			return err
		}
		entry := &AdtyBalanceLedger{UserId: userID, DeltaQuota: delta, BalanceBefore: int64(liveBefore), BalanceAfter: int64(liveAfter), Type: AdtyLedgerTypeManualAdjust, RefType: "financial_adjustment", RefId: strconv.FormatInt(row.Id, 10), Reason: reason, OperatorId: operatorID, IdempotencyKey: &idemKey}
		if err := InsertAdtyLedgerTx(tx, entry); err != nil {
			return err
		}
		row.LedgerId = entry.Id
		if err := tx.Model(&row).Updates(map[string]any{"ledger_id": entry.Id, "approval_ref": row.ApprovalRef}).Error; err != nil {
			return err
		}
		audit, err := PrepareOfficialAuditLog(nil, OfficialAuditWrite{UserId: actor.Id, Username: actor.Username, ActorRole: actor.Role, SiteID: OfficialAuditSiteIDPointer(row.SiteId), ObjectType: "financial_adjustment", ObjectID: strconv.FormatInt(row.Id, 10), Action: "adty.user.quota_adjust", Category: AuditCategoryOperation, Content: "Manual quota correction", Before: map[string]any{"balance": row.BalanceBefore}, After: row, Reason: reason, ApprovalRef: row.ApprovalRef, Result: AuditResultSuccess, Success: true})
		if err != nil {
			return err
		}
		if err := AppendAuditIntentTx(tx, audit); err != nil {
			return err
		}
		result = adtyFinancialAdjustmentResult(user, row, false)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !result.Replayed {
		if err := invalidateUserCache(userID); err != nil {
			common.SysError(common.LogText("failed to invalidate manual quota projection for user %d: %s", userID, err))
		}
	}
	return &result, nil
}

func adtyFinancialAdjustmentResult(user User, row AdtyFinancialAdjustment, replayed bool) AdtyQuotaAdjustResult {
	return AdtyQuotaAdjustResult{UserQuotaAdjustment: UserQuotaAdjustment{UserID: user.Id, Username: user.Username, Before: row.RawBefore, After: row.RawAfter}, Replayed: replayed, Direction: row.Direction, DeltaQuota: row.DeltaQuota, BalanceBefore: row.BalanceBefore, BalanceAfter: row.BalanceAfter, Reason: row.Reason, OrderRef: row.OrderRef, ApprovalRef: row.ApprovalRef, ApproverID: row.ApproverId, ExecutorID: row.ExecutorId, SiteID: row.SiteId, LedgerID: row.LedgerId, AdjustmentID: row.Id}
}
