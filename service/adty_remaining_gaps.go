package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Exported functions in this file are landed production helpers with
// non-test callers. Unexported helpers are remaining-gap sketches or
// same-package internals; they are not landed HTTP or product APIs.

// AnnouncementDetail returns one published notice by key.
func AnnouncementDetail(items []map[string]any, key string) (map[string]any, bool) {
	key = strings.TrimSpace(key)
	for _, item := range items {
		if item["read_key"] == key || item["id"] == key {
			return item, true
		}
	}
	return nil, false
}

// ListStaffBalanceRequests returns adjustment requests for one site.
func ListStaffBalanceRequests(siteID int) ([]model.AdtyStaffBalanceRequest, error) {
	var rows []model.AdtyStaffBalanceRequest
	err := model.DB.Where("site_id = ?", siteID).Order("id").Find(&rows).Error
	return rows, err
}

func PageStaffBalanceRequests(siteID int, status string, offset, limit int) ([]model.AdtyStaffBalanceRequest, int64, error) {
	var rows []model.AdtyStaffBalanceRequest
	var total int64
	query := model.DB.Model(&model.AdtyStaffBalanceRequest{}).Where("site_id = ?", siteID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

// PageStaffOwnBalanceRequests returns paginated adjustment requests submitted by one staff member on one site (R5-044).
func PageStaffOwnBalanceRequests(siteID, staffUserID int, status string, offset, limit int) ([]model.AdtyStaffBalanceRequest, int64, error) {
	var rows []model.AdtyStaffBalanceRequest
	var total int64
	query := model.DB.Model(&model.AdtyStaffBalanceRequest{}).Where("site_id = ? AND staff_user_id = ?", siteID, staffUserID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

// WalletUSDParts splits a wallet into principal and permanent bonus for display.
type WalletUSDParts struct {
	Principal decimal.Decimal
	Bonus     decimal.Decimal
	Total     decimal.Decimal
}

func WalletUSDPartsFromQuota(principalQuota, bonusQuota int64) WalletUSDParts {
	principal := QuotaToUSD(principalQuota)
	bonus := QuotaToUSD(bonusQuota)
	return WalletUSDParts{Principal: principal, Bonus: bonus, Total: principal.Add(bonus)}
}

// HealthSampleStatus is no_sample, stale, or ok.
func HealthSampleStatus(samples int, updatedAt, now, staleSeconds int64) string {
	if samples <= 0 || updatedAt <= 0 {
		return "no_sample"
	}
	if staleSeconds > 0 && now-updatedAt > staleSeconds {
		return "stale"
	}
	return "ok"
}

// UpdateAdtyTeamProfile lets the leader rename the team. Discount changes stay on the create path.
func UpdateAdtyTeamProfile(leaderId int, teamId int64, name string) error {
	name = strings.TrimSpace(name)
	if err := validateAdtyTeamName(name); err != nil {
		return err
	}
	if teamId <= 0 {
		return ErrAdtyTeamInvalid
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		team, err := lockActiveTeam(tx, teamId)
		if err != nil {
			return err
		}
		if team.LeaderUserId != leaderId {
			return ErrAdtyTeamForbidden
		}
		return tx.Model(team).Update("name", name).Error
	})
}

// SubmitAdtyStaffBalanceRequest rejects retired staff balance requests.
func SubmitAdtyStaffBalanceRequest(staffUserID, customerUserID int, direction string, amount decimal.Decimal, idempotencyKey string, provenance ...model.AdtyStaffBalanceProvenance) (*model.AdtyStaffBalanceRequest, error) {
	_, _, _, _, _, _ = staffUserID, customerUserID, direction, amount, idempotencyKey, provenance
	return nil, common.NewMessage("Staff balance requests have been disabled.")
}

func appendStaffBalanceOfficialAuditTx(tx *gorm.DB, actorID int, username string, actorRole, siteID int, objectID, action, approvalRef string, before, after any, reason string) (*model.AuditLog, bool, error) {
	entry, err := model.PrepareOfficialAuditLog(nil, model.OfficialAuditWrite{
		UserId:      actorID,
		Username:    username,
		ActorRole:   actorRole,
		SiteID:      model.OfficialAuditSiteIDPointer(siteID),
		ObjectType:  "staff_balance_request",
		ObjectID:    objectID,
		Action:      action,
		Category:    model.AuditCategoryOperation,
		Content:     action,
		Before:      before,
		After:       after,
		Reason:      reason,
		ApprovalRef: approvalRef,
		Result:      model.AuditResultSuccess,
		Success:     true,
	})
	if err != nil {
		return nil, false, err
	}
	if tx.Migrator().HasTable(&model.AuditIntent{}) {
		if err := model.AppendAuditIntentTx(tx, entry); err != nil {
			return nil, false, err
		}
		return &entry, true, nil
	}
	return &entry, false, nil
}

func deliverStaffBalanceOfficialAudit(entry *model.AuditLog, hasIntent bool) {
	if entry == nil {
		return
	}
	if hasIntent {
		if err := model.DeliverAuditIntent(context.Background(), entry.EventId); err != nil {
			common.SysLog(common.LogText("official staff balance audit delivery deferred action=%s id=%s: %s", entry.Action, entry.ObjectId, err.Error()))
		}
		return
	}
	if err := model.WriteOfficialAuditLog(nil, model.OfficialAuditWrite{
		UserId:      entry.UserId,
		Username:    entry.Username,
		ActorRole:   entry.ActorRole,
		SiteID:      entry.SiteId,
		ObjectType:  entry.ObjectType,
		ObjectID:    entry.ObjectId,
		Action:      entry.Action,
		Category:    entry.Category,
		Content:     entry.Content,
		Before:      jsonRawString(entry.BeforeValue),
		After:       jsonRawString(entry.AfterValue),
		Reason:      entry.Reason,
		ApprovalRef: entry.ApprovalRef,
		Result:      entry.Result,
		Success:     entry.Success,
	}); err != nil {
		common.SysLog(common.LogText("official staff balance audit write failed action=%s id=%s: %s", entry.Action, entry.ObjectId, err.Error()))
	}
}

type jsonRawString string

func (s jsonRawString) MarshalJSON() ([]byte, error) {
	if strings.TrimSpace(string(s)) == "" {
		return []byte("null"), nil
	}
	return []byte(s), nil
}

// adtyScopedStaffBalanceIdemKey isolates staff balance request idempotency keys per
// (site_id, staff_user_id) while staying within the existing varchar(64) column limit (R5-044).
func adtyScopedStaffBalanceIdemKey(siteID, staffUserID int, key string) string {
	return fmt.Sprintf("s%d:u%d:%s", siteID, staffUserID, common.Sha1([]byte(strings.TrimSpace(key))))
}

func findAdtyStaffBalanceRequestByIdemKey(db *gorm.DB, siteID, staffUserID int, rawKey string) (*model.AdtyStaffBalanceRequest, error) {
	scopedKey := adtyScopedStaffBalanceIdemKey(siteID, staffUserID, rawKey)
	var rows []model.AdtyStaffBalanceRequest
	if err := db.Where("idempotency_key = ? OR (site_id = ? AND staff_user_id = ? AND idempotency_key = ?)",
		scopedKey, siteID, staffUserID, strings.TrimSpace(rawKey)).Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

func sameAdtyStaffBalanceRequest(row *model.AdtyStaffBalanceRequest, siteID, staffUserID, customerUserID int, direction string, amount decimal.Decimal) bool {
	return row != nil && row.SiteId == siteID && row.StaffUserId == staffUserID &&
		row.CustomerUserId == customerUserID && row.Direction == direction && row.AmountUSD.Equal(amount)
}

func adtyStaffBalanceLedgerKey(row model.AdtyStaffBalanceRequest) string {
	if row.IdempotencyKey != nil {
		if key := strings.TrimSpace(*row.IdempotencyKey); key != "" {
			return "staff-balance:" + common.Sha1([]byte(key))
		}
	}
	return "staff-balance:" + strconv.FormatInt(row.Id, 10)
}

func sameStaffBalanceLedger(entry *model.AdtyBalanceLedger, row model.AdtyStaffBalanceRequest, delta int64) bool {
	if entry == nil {
		return false
	}
	expected := delta
	if row.Direction == model.AdtyStaffBalanceDecrease {
		expected = -delta
	}
	return entry.Type == model.AdtyLedgerTypeManualAdjust && entry.RefType == "staff_balance" &&
		entry.RefId == strconv.FormatInt(row.Id, 10) && entry.UserId == row.CustomerUserId &&
		entry.DeltaQuota == expected
}

// applyApprovedStaffBalanceTx credits or debits through the official wallet
// helpers so the same transaction writes the ledger and principal fund batches.
// A decrease is refused unless available balance (quota minus in-flight holds)
// covers the full amount.
type staffBalanceApplyResult struct {
	LedgerID      int64
	BeforeQuota   int64
	AfterQuota    int64
	FundingSource string
	EarningsUSD   decimal.Decimal
	MarginUSD     decimal.Decimal
}

func applyApprovedStaffBalanceTx(tx *gorm.DB, row model.AdtyStaffBalanceRequest, reviewerID int) (staffBalanceApplyResult, error) {
	if row.Reason == "" || row.OrderRef == "" {
		return staffBalanceApplyResult{}, common.NewMessage("Adjustment reason, original order and funding source are required")
	}
	if _, err := model.ValidateAdtyAdjustmentOrderTx(tx, row.CustomerUserId, row.OrderRef, &row.SiteId); err != nil {
		return staffBalanceApplyResult{}, err
	}
	if err := model.RejectRestrictedAdtyFundWrite(tx, model.AdtyFundWrite{AccountID: row.CustomerUserId, SiteID: row.SiteId, OrderRef: row.OrderRef}); err != nil {
		return staffBalanceApplyResult{}, err
	}
	if bs := strings.TrimSpace(row.BatchSource); bs != "" && bs != strings.TrimSpace(row.OrderRef) {
		if err := model.RejectRestrictedAdtyFundWrite(tx, model.AdtyFundWrite{AccountID: row.CustomerUserId, SiteID: row.SiteId, OrderRef: bs}); err != nil {
			return staffBalanceApplyResult{}, err
		}
	}
	quota, err := common.WalletQuotaFromDecimalStrict(row.AmountUSD.Mul(common.QuotaPerUnitDecimal()).Floor())
	if err != nil {
		return staffBalanceApplyResult{}, err
	}
	delta := int64(quota)
	if delta <= 0 {
		return staffBalanceApplyResult{}, common.NewMessage("Invalid parameters")
	}
	var user model.User
	if err := model.AdtyLockForUpdate(tx).First(&user, row.CustomerUserId).Error; err != nil {
		return staffBalanceApplyResult{}, err
	}
	// Customer must still belong to this site (and not be a staff member) for both increase and decrease.
	if user.SiteId != row.SiteId || user.StaffSiteId != 0 || user.Status != common.UserStatusEnabled {
		return staffBalanceApplyResult{}, common.NewMessage("No permission to perform this action")
	}
	key := adtyStaffBalanceLedgerKey(row)
	existing, err := model.FindAdtyLedgerByIdemKeyTx(tx, key)
	if err != nil {
		return staffBalanceApplyResult{}, err
	}
	if existing != nil {
		if !sameStaffBalanceLedger(existing, row, delta) {
			return staffBalanceApplyResult{}, model.ErrAdtyIdempotencyConflict
		}
		return staffBalanceApplyResult{
			LedgerID:    existing.Id,
			BeforeQuota: existing.BalanceBefore,
			AfterQuota:  existing.BalanceAfter,
		}, nil
	}
	var before, applied int64
	var fundingSource string
	var split AdtyOwnerFundingSplit
	if row.Direction == model.AdtyStaffBalanceDecrease {
		var debited int64
		before, debited, err = model.AdtyDebitUserQuotaTx(tx, row.CustomerUserId, delta)
		if err != nil {
			return staffBalanceApplyResult{}, err
		}
		if debited != delta {
			return staffBalanceApplyResult{}, common.NewMessage("Customer balance is not enough")
		}
		applied = -debited
		if row.FundingSource != "specified_batch" {
			return staffBalanceApplyResult{}, common.NewMessage("A specific available fund batch is required")
		}
		if err := model.ReduceAdtyAdjustmentBatchTx(tx, row.CustomerUserId, row.SiteId, row.BatchSource, row.BatchKind, debited); err != nil {
			return staffBalanceApplyResult{}, err
		}
		if row.BatchKind == model.AdtyFundBatchKindPrincipal && row.SiteId > 0 {
			// Symmetrically debit subsite operating wallet on principal reduction (R1-007).
			if err := adtyMoveOperatingPrincipalTx(tx, row.SiteId, -debited, "principal_adjust_debit", "staff_balance",
				strconv.FormatInt(row.Id, 10), key+":operating_debit", false); err != nil {
				return staffBalanceApplyResult{}, err
			}
		}
		fundingSource = row.FundingSource
	} else {
		split, err = adtyFundStaffBalanceIncreaseTx(tx, row, delta)
		if err != nil {
			return staffBalanceApplyResult{}, err
		}
		fundingSource = split.SourceKind()
		before, err = model.AdtyCreditUserQuotaTx(tx, row.CustomerUserId, delta)
		if err != nil {
			return staffBalanceApplyResult{}, err
		}
		applied = delta
		// 员工调额批次记在申请单所在站（row.SiteId），与该单的出资钱包同站。
		if err := model.CreateAdtyFundBatchesWithSiteTx(tx, row.CustomerUserId, model.AdtyFundBatchSiteID(row.SiteId), adtyStaffBalanceLedgerKey(row), delta, 0); err != nil {
			return staffBalanceApplyResult{}, err
		}
	}
	entry := &model.AdtyBalanceLedger{
		UserId: row.CustomerUserId, DeltaQuota: applied,
		BalanceBefore: before, BalanceAfter: before + applied,
		Type: model.AdtyLedgerTypeManualAdjust, RefType: "staff_balance",
		RefId: strconv.FormatInt(row.Id, 10), Reason: row.Reason,
		OperatorId: reviewerID, IdempotencyKey: &key,
	}
	if err := model.InsertAdtyLedgerTx(tx, entry); err != nil {
		return staffBalanceApplyResult{}, err
	}
	return staffBalanceApplyResult{
		LedgerID:      entry.Id,
		BeforeQuota:   before,
		AfterQuota:    before + applied,
		FundingSource: fundingSource,
		EarningsUSD:   split.EarningsUSD,
		MarginUSD:     split.MarginUSD,
	}, nil
}

// ReviewAdtyStaffBalanceRequest approves or rejects a pending request.
// Site owners review their own site. Root reviews any site. Approval changes the customer quota once
// through the official credit or debit path, writing the ledger and fund batches.
// adtyStaffBalanceFundingOrder 是「员工加款从哪儿出钱」的顺序：
// 优先扣站点可支配收益，不足部分自动从超出当前站点等级所需保证金底线的溢余保证金中补足；
// 运营钱包是现有客户本金的 1:1 托管准备金，绝不可当成站长可支配盈余花费。
var adtyStaffBalanceFundingOrder = []string{
	model.AdtySiteWalletEarnings,
	model.AdtySiteWalletMargin,
}

// adtyFundStaffBalanceIncreaseTx 按「收益优先 + 超额保证金自动补足」扣出站方出资，并同步向运营钱包入账等额本金背书。
func adtyFundStaffBalanceIncreaseTx(tx *gorm.DB, row model.AdtyStaffBalanceRequest, delta int64) (AdtyOwnerFundingSplit, error) {
	if delta <= 0 {
		return AdtyOwnerFundingSplit{}, common.NewMessage("Invalid parameters")
	}
	if row.SiteId <= 0 {
		return AdtyOwnerFundingSplit{}, common.NewMessage("This request has no site to fund it")
	}
	var customer model.User
	if err := tx.Select("id", "site_id", "staff_site_id").First(&customer, row.CustomerUserId).Error; err != nil {
		return AdtyOwnerFundingSplit{}, err
	}
	// 客户必须还在这个站点下：站点换了之后，旧站点不该继续为他的加款出钱。
	if customer.SiteId != row.SiteId || customer.StaffSiteId != 0 {
		return AdtyOwnerFundingSplit{}, common.NewMessage("No permission to perform this action")
	}
	switch row.FundingSource {
	case "", model.AdtyStaffFundingSourceAuto, model.AdtyStaffFundingSourceEarnings, model.AdtyStaffFundingSourceMargin:
	default:
		return AdtyOwnerFundingSplit{}, common.NewMessage("Select an authorized funding source")
	}
	amountUSD := decimal.NewFromInt(delta).Div(common.QuotaPerUnitDecimal())
	key := adtyStaffBalanceLedgerKey(row)
	// Pass operatorID=0 so earnings attribution always uses the site's current
	// financial owner even when Root approves the request.
	split, err := adtyDebitOwnerAutoFundingTx(tx, row.SiteId, 0, amountUSD, "staff_balance", "staff_balance", strconv.FormatInt(row.Id, 10), row.Reason, key+":fund")
	if err != nil {
		if errors.Is(err, ErrAdtySiteWalletInsufficient) {
			return AdtyOwnerFundingSplit{}, ErrAdtyOwnerAutoFundingShortfall
		}
		return AdtyOwnerFundingSplit{}, err
	}
	if err := adtyMoveOperatingPrincipalTx(tx, row.SiteId, delta, "principal_credit", "staff_balance",
		strconv.FormatInt(row.Id, 10), key+":operating", false); err != nil {
		return AdtyOwnerFundingSplit{}, err
	}
	return split, nil
}

func fundAdtyFinancialAdjustmentTx(tx *gorm.DB, row model.AdtyFinancialAdjustment) error {
	kind := ""
	marginExcessOnly := false
	operatorID := 0
	switch row.FundingSource {
	case model.AdtyStaffFundingSourceEarnings:
		kind = model.AdtySiteWalletEarnings
		var site model.AdtySite
		if err := tx.Select("id", "owner_user_id").First(&site, row.SiteId).Error; err != nil {
			return err
		}
		operatorID = site.OwnerUserId
	case model.AdtyStaffFundingSourceMargin:
		kind = model.AdtySiteWalletMargin
		marginExcessOnly = true
	default:
		return common.NewMessage("Select an authorized funding source")
	}
	key := fmt.Sprintf("financial-adjustment:%d", row.Id)
	ref := strconv.FormatInt(row.Id, 10)
	amount := decimal.NewFromInt(row.DeltaQuota).Div(common.QuotaPerUnitDecimal())
	if _, err := applyAdtySiteWalletChangeTx(tx, row.SiteId, kind, amount.Neg(), AdtySiteWalletChange{
		Type: "financial_adjustment", RefType: "financial_adjustment", RefID: ref,
		OperatorID: operatorID, Reason: row.Reason, IdempotencyKey: key + ":fund",
		MarginExcessOnly: marginExcessOnly,
	}); err != nil {
		return err
	}
	return adtyMoveOperatingPrincipalTx(tx, row.SiteId, row.DeltaQuota, "principal_credit", "financial_adjustment", ref, key+":operating", false)
}

func init() {
	model.AdtyFundFinancialAdjustmentTx = fundAdtyFinancialAdjustmentTx
}

// RecordAdtyStaffReadAudit persists both the site audit row and durable official
// AuditIntent atomically before returning customer data, then delivers best-effort.
func RecordAdtyStaffReadAudit(c *gin.Context, staff *model.AdtySiteStaff, action string, target int) error {
	if staff == nil || model.DB == nil {
		return nil
	}
	after := map[string]any{"staff_id": staff.Id, "customer_id": target}
	username := ""
	actorRole := 0
	if c != nil {
		username = c.GetString("username")
		actorRole = c.GetInt("role")
	}
	var auditEntry *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&model.AdtySiteAudit{
			SiteId:     staff.SiteId,
			OperatorId: staff.UserId,
			Action:     action,
			NewValue:   common.GetJsonString(after),
		}).Error; err != nil {
			return err
		}
		entry, auditErr := model.PrepareOfficialAuditLog(c, model.OfficialAuditWrite{
			UserId: staff.UserId, Username: username, ActorRole: actorRole,
			SiteID: model.OfficialAuditSiteIDPointer(staff.SiteId), ObjectType: "site.staff",
			ObjectID: strconv.Itoa(staff.Id), Action: "adty." + action, Category: model.AuditCategoryOperation,
			Content: action, After: after, Result: model.AuditResultSuccess, Success: true,
		})
		if auditErr != nil {
			return auditErr
		}
		if err := model.AppendAuditIntentTx(tx, entry); err != nil {
			return err
		}
		auditEntry = &entry
		return nil
	})
	if err != nil {
		return err
	}
	if auditEntry != nil {
		deliverAdtyCommittedAudit(*auditEntry)
	}
	return nil
}

func ReviewAdtyStaffBalanceRequest(reviewerID int, reviewerRole string, requestID int64, approve bool, reviewReason ...string) error {
	_, err := ReviewAdtyStaffBalanceRequestWithAudit(reviewerID, reviewerRole, requestID, approve, reviewReason...)
	return err
}

func ReviewAdtyStaffBalanceRequestWithAudit(reviewerID int, reviewerRole string, requestID int64, approve bool, reviewReason ...string) (string, error) {
	var changedUserID int
	var officialEntry *model.AuditLog
	var deliverIntent bool
	note := ""
	if len(reviewReason) > 0 {
		note = strings.TrimSpace(reviewReason[0])
	}
	if len(note) > 200 {
		return "", common.NewMessage("Review reason must be at most 200 characters")
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var row model.AdtyStaffBalanceRequest
		if err := model.AdtyLockForUpdate(tx).First(&row, requestID).Error; err != nil {
			return err
		}
		if row.Status != model.AdtyStaffBalancePending {
			return common.NewMessage("This request is already reviewed")
		}
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).First(&site, row.SiteId).Error; err != nil {
			return err
		}
		if reviewerRole == "owner" {
			if site.OwnerUserId != reviewerID || site.Status != model.AdtySiteStatusActive {
				return common.NewMessage("No permission to perform this action")
			}
		} else if reviewerRole != "root" {
			return common.NewMessage("No permission to perform this action")
		} else if approve && site.Status != model.AdtySiteStatusActive {
			return common.NewMessage("No permission to perform this action")
		}
		var customerBefore model.User
		if err := model.AdtyLockForUpdate(tx).First(&customerBefore, row.CustomerUserId).Error; err != nil {
			return err
		}
		if reviewerRole == "owner" && (customerBefore.SiteId != row.SiteId || customerBefore.StaffSiteId != 0) {
			return common.NewMessage("No permission to perform this action")
		}
		status := model.AdtyStaffBalanceRejected
		var applied staffBalanceApplyResult
		if approve {
			status = model.AdtyStaffBalanceApproved
			var applyErr error
			applied, applyErr = applyApprovedStaffBalanceTx(tx, row, reviewerID)
			if applyErr != nil {
				return applyErr
			}
			changedUserID = row.CustomerUserId
		} else {
			applied.BeforeQuota = int64(customerBefore.Quota)
			applied.AfterQuota = int64(customerBefore.Quota)
		}
		reviewedAt := common.GetTimestamp()
		if err := tx.Model(&row).Updates(map[string]any{
			"status": status, "reviewer_id": reviewerID, "reviewer_role": reviewerRole, "reviewed_at": reviewedAt,
			"before_quota": applied.BeforeQuota, "after_quota": applied.AfterQuota, "ledger_id": applied.LedgerID,
		}).Error; err != nil {
			return err
		}
		var reviewer model.User
		if err := tx.Select("id", "username", "role").First(&reviewer, reviewerID).Error; err != nil {
			return err
		}
		beforeSnapshot := map[string]any{
			"request_id":   row.Id,
			"staff_id":     row.StaffUserId,
			"customer_id":  row.CustomerUserId,
			"site_id":      row.SiteId,
			"direction":    row.Direction,
			"amount_usd":   row.AmountUSD.String(),
			"status":       model.AdtyStaffBalancePending,
			"before_quota": applied.BeforeQuota,
		}
		afterSnapshot := map[string]any{
			"id":             row.Id,
			"request_id":     row.Id,
			"staff_id":       row.StaffUserId,
			"customer_id":    row.CustomerUserId,
			"reviewer_id":    reviewerID,
			"reviewer_role":  reviewerRole,
			"site_id":        row.SiteId,
			"direction":      row.Direction,
			"amount_usd":     row.AmountUSD.String(),
			"status":         status,
			"before_quota":   applied.BeforeQuota,
			"after_quota":    applied.AfterQuota,
			"ledger_id":      applied.LedgerID,
			"funding_source": applied.FundingSource,
			"earnings_usd":   applied.EarningsUSD.String(),
			"margin_usd":     applied.MarginUSD.String(),
			"reviewed_at":    reviewedAt,
			"review_reason":  note,
			"reason":         row.Reason, "order_ref": row.OrderRef, "batch_kind": row.BatchKind, "batch_source": row.BatchSource,
		}
		auditReason := "staff balance " + status
		if note != "" {
			auditReason += ": " + note
		}
		if err := tx.Create(&model.AdtySiteAudit{
			SiteId: row.SiteId, Action: "staff.balance_review", OperatorId: reviewerID,
			OldValue: common.GetJsonString(beforeSnapshot),
			NewValue: common.GetJsonString(afterSnapshot),
			Reason:   auditReason,
		}).Error; err != nil {
			return err
		}
		officialAction := "adty.staff.balance_reject"
		approvalRef := fmt.Sprintf("staff_balance:%d", row.Id)
		if approve {
			officialAction = "adty.staff.balance_approve"
			approvalRef = fmt.Sprintf("staff_balance:%d:ledger:%d", row.Id, applied.LedgerID)
		}
		entry, ok, err := appendStaffBalanceOfficialAuditTx(tx, reviewer.Id, reviewer.Username, reviewer.Role, row.SiteId,
			strconv.FormatInt(row.Id, 10), officialAction, approvalRef, beforeSnapshot, afterSnapshot, auditReason)
		if err != nil {
			return err
		}
		officialEntry = entry
		deliverIntent = ok
		return nil
	})
	if err != nil {
		return "", err
	}
	eventID := ""
	if officialEntry != nil {
		eventID = officialEntry.EventId
		deliverStaffBalanceOfficialAudit(officialEntry, deliverIntent)
	}
	if changedUserID > 0 {
		model.AdtySyncUserQuotaCache(changedUserID, "staff-balance")
	}
	return eventID, nil
}

// ReconcileOperatingWallet returns operating balance minus customer principal.
// A non-zero result is a fund gap and must not be hidden.
func ReconcileOperatingWallet(customerPrincipal, operatingBalance decimal.Decimal) decimal.Decimal {
	return operatingBalance.Sub(customerPrincipal)
}

// AdtyFundAnomalies lists open fund exceptions. Empty means this check found none.
func AdtyFundAnomalies(balanceGap int64, openHolds int, marginGap decimal.Decimal) []string {
	var out []string
	if balanceGap != 0 {
		out = append(out, "balance_ledger_gap")
	}
	if openHolds > 0 {
		out = append(out, "open_shortfall_hold")
	}
	if !marginGap.IsZero() {
		out = append(out, "margin_anomaly")
	}
	return out
}

// AdtyHealthWindowStarts returns the 96 fifteen-minute window starts ending at now.
func AdtyHealthWindowStarts(now time.Time) []int64 {
	end := now.UTC().Truncate(15 * time.Minute)
	out := make([]int64, 96)
	for i := 0; i < 96; i++ {
		out[i] = end.Add(-time.Duration(95-i) * 15 * time.Minute).Unix()
	}
	return out
}

// SaveAdtyGlossaryTerm stores one source/target pair for a locale.
func SaveAdtyGlossaryTerm(source, locale, target string) error {
	source, locale, target = strings.TrimSpace(source), strings.TrimSpace(locale), strings.TrimSpace(target)
	if locale == "" || validateAdtyGlossaryPair(source, target) != nil {
		return common.NewMessage("Invalid parameters")
	}
	row := model.AdtyGlossaryTerm{Source: source, Locale: locale, Target: target}
	return model.DB.Where(model.AdtyGlossaryTerm{Source: source, Locale: locale}).Assign(row).FirstOrCreate(&row).Error
}

const (
	AdtyTodoStatusPending    = "pending"
	AdtyTodoStatusInProgress = "in_progress"
	AdtyTodoStatusCompleted  = "completed"
)

// AdtyTodoItem persists the authoritative state of a severe todo item for a user,
// keeping the full lifecycle and completion history instead of hiding the notice.
type AdtyTodoItem struct {
	Id             int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId         int    `json:"user_id" gorm:"not null;uniqueIndex:idx_adty_todo_user_key,priority:1;index"`
	SiteId         int    `json:"site_id" gorm:"not null;default:0;index"`
	Key            string `json:"key" gorm:"type:varchar(128);not null;uniqueIndex:idx_adty_todo_user_key,priority:2;index"`
	PublicationId  int    `json:"publication_id" gorm:"not null;default:0;index"`
	Title          string `json:"title" gorm:"type:varchar(200);not null;default:''"`
	Content        string `json:"content" gorm:"type:text"`
	Business       string `json:"business" gorm:"type:varchar(200);not null;default:''"`
	Severity       string `json:"severity" gorm:"type:varchar(32);not null;default:'severe'"`
	Status         string `json:"status" gorm:"type:varchar(32);not null;default:'pending';index"`
	Revision       int64  `json:"revision" gorm:"not null;default:1"`
	ResolutionNote string `json:"resolution_note" gorm:"type:varchar(500);not null;default:''"`
	CreatedAt      int64  `json:"created_at" gorm:"not null;default:0"`
	UpdatedAt      int64  `json:"updated_at" gorm:"not null;default:0"`
	CompletedAt    int64  `json:"completed_at" gorm:"not null;default:0"`
}

func (AdtyTodoItem) TableName() string {
	return "adty_todo_items"
}

// AdtyTodoStateHistory records every state transition on a severe todo item.
type AdtyTodoStateHistory struct {
	Id         int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	TodoId     int64  `json:"todo_id" gorm:"not null;index"`
	UserId     int    `json:"user_id" gorm:"not null;index"`
	Key        string `json:"key" gorm:"type:varchar(128);not null;index"`
	FromStatus string `json:"from_status" gorm:"type:varchar(32);not null;default:''"`
	ToStatus   string `json:"to_status" gorm:"type:varchar(32);not null"`
	ActorId    int    `json:"actor_id" gorm:"not null;default:0"`
	Note       string `json:"note" gorm:"type:varchar(500);not null;default:''"`
	CreatedAt  int64  `json:"created_at" gorm:"not null;default:0"`
}

func (AdtyTodoStateHistory) TableName() string {
	return "adty_todo_state_histories"
}

func ensureAdtyTodoTables(db *gorm.DB) error {
	if db == nil {
		return common.NewMessage("Database is not initialized")
	}
	if !db.Migrator().HasTable(&AdtyTodoItem{}) || !db.Migrator().HasTable(&AdtyTodoStateHistory{}) {
		if err := db.AutoMigrate(&AdtyTodoItem{}, &AdtyTodoStateHistory{}); err != nil {
			return err
		}
	}
	return nil
}

func normalizeAdtyTodoKey(key string) string {
	key = strings.TrimSpace(key)
	key = strings.TrimPrefix(key, "todo:")
	return strings.TrimSpace(key)
}

func truncateAdtyRunes(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes])
}

func adtyTodoItemVisibleForUserOnSite(db *gorm.DB, userID int, site *model.AdtySite, item *AdtyTodoItem, pub *model.AdtyNotificationPublication, tokenScopes ...[]string) (bool, error) {
	if db == nil || userID <= 0 || item == nil || item.UserId != userID {
		return false, nil
	}
	hasPubTable := db.Migrator().HasTable(&model.AdtyNotificationPublication{})
	if pub == nil && hasPubTable {
		pub = lookupAdtyPublicationForTodoTx(db, item.Key, item.PublicationId)
	}
	if item.PublicationId > 0 || pub != nil {
		if !hasPubTable {
			return false, nil
		}
		if pub == nil || pub.Status != "published" || !adtyPublicationIsSevere(db, pub) {
			return false, nil
		}
		q, err := adtyVisibleNotificationPublicationsWithDB(db, userID, site)
		if err != nil {
			return false, err
		}
		var visibleCount int64
		if err := q.Where("id = ?", pub.Id).Count(&visibleCount).Error; err != nil {
			return false, err
		}
		if visibleCount > 0 {
			return true, nil
		}
		// Site owners managing their subsite from the main host can still see and
		// transition active site-targeted severe todos while they remain owner.
		if site == nil && pub.Audience == "site" && pub.TargetSiteId > 0 && db.Migrator().HasTable(&model.AdtySite{}) {
			var ownedCount int64
			if err := db.Model(&model.AdtySite{}).
				Where("id = ? AND owner_user_id = ? AND status <> ?", pub.TargetSiteId, userID, model.AdtySiteStatusClosed).
				Count(&ownedCount).Error; err != nil {
				return false, err
			}
			return ownedCount > 0, nil
		}
		return false, nil
	}
	if item.Key != "" && db.Migrator().HasTable(&AdtyChannelWatcherNotice{}) {
		var notice AdtyChannelWatcherNotice
		res := db.Select("id", "notify_type").Where("user_id = ? AND read_key = ?", userID, item.Key).Limit(1).Find(&notice)
		if res.Error != nil {
			return false, res.Error
		}
		if res.RowsAffected > 0 {
			canChannel, canEpay := adtyWatcherNoticeAccess(userID, site, tokenScopes...)
			return adtyWatcherNoticePermitted(notice.NotifyType, canChannel, canEpay), nil
		}
	}
	if site != nil && item.SiteId > 0 && item.SiteId != site.Id {
		return false, nil
	}
	return true, nil
}

func lookupAdtyPublicationForTodoTx(tx *gorm.DB, key string, publicationID int) *model.AdtyNotificationPublication {
	if tx == nil || !tx.Migrator().HasTable(&model.AdtyNotificationPublication{}) {
		return nil
	}
	var pub model.AdtyNotificationPublication
	if publicationID > 0 {
		if tx.Where("id = ?", publicationID).Limit(1).Find(&pub).RowsAffected > 0 {
			return &pub
		}
	}
	if rawID, ok := strings.CutPrefix(key, "publication:"); ok {
		if id, err := strconv.Atoi(strings.TrimSpace(rawID)); err == nil && id > 0 {
			if tx.Where("id = ?", id).Limit(1).Find(&pub).RowsAffected > 0 {
				return &pub
			}
		}
	}
	if tx.Where("read_key = ?", key).Order("id DESC").Limit(1).Find(&pub).RowsAffected > 0 {
		return &pub
	}
	return nil
}

// UpsertAdtySevereTodo produces or updates a persistent severe todo item for userID.
func UpsertAdtySevereTodo(userID, siteID int, key, title, content, business string, publicationID int) (*AdtyTodoItem, error) {
	key = normalizeAdtyTodoKey(key)
	if userID <= 0 || key == "" {
		return nil, common.NewMessage("Invalid parameters")
	}
	if err := ensureAdtyTodoTables(model.DB); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	var out *AdtyTodoItem
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		// R10-036: Serialize per-user severe todo upserts so concurrent edits cannot
		// create duplicate rows, and normalize key against the live publication ReadKey.
		if tx.Migrator().HasTable(&model.User{}) {
			var lockedUser model.User
			if err := model.AdtyLockForUpdate(tx).Select("id").Where("id = ?", userID).Limit(1).Find(&lockedUser).Error; err != nil {
				return err
			}
		}
		pub := lookupAdtyPublicationForTodoTx(tx, key, publicationID)
		if pub == nil && publicationID > 0 && tx.Migrator().HasTable(&model.AdtyNotificationPublication{}) {
			return nil
		}
		if pub != nil {
			if !adtyPublicationIsSevere(tx, pub) {
				return nil
			}
			if publicationID <= 0 {
				publicationID = pub.Id
			}
			if siteID == 0 && pub.TargetSiteId > 0 {
				siteID = pub.TargetSiteId
			}
			if tx.Migrator().HasTable(&model.User{}) {
				var targetSite *model.AdtySite
				if siteID > 0 && tx.Migrator().HasTable(&model.AdtySite{}) {
					var siteRow model.AdtySite
					if tx.Where("id = ?", siteID).Limit(1).Find(&siteRow).RowsAffected == 0 {
						return nil
					}
					targetSite = &siteRow
				}
				visible, visErr := adtyTodoItemVisibleForUserOnSite(tx, userID, targetSite, &AdtyTodoItem{
					UserId:        userID,
					SiteId:        siteID,
					Key:           key,
					PublicationId: pub.Id,
				}, pub)
				if visErr != nil {
					if errors.Is(visErr, gorm.ErrRecordNotFound) {
						return nil
					}
					return visErr
				}
				if !visible {
					return nil
				}
			}
			if liveKey := normalizeAdtyTodoKey(pub.ReadKey); liveKey != "" {
				key = liveKey
			}
			if strings.TrimSpace(pub.Title) != "" {
				title = pub.Title
			}
			if strings.TrimSpace(pub.Content) != "" {
				content = pub.Content
			}
			if strings.TrimSpace(pub.Business) != "" {
				business = pub.Business
			}
		}
		var item AdtyTodoItem
		// R5-058: When publicationID > 0, update any existing todo for (user_id, publication_id)
		// in place (updating its key to the new ReadKey) so editing a published severe announcement
		// never spawns duplicate orphaned pending todos.
		var found *gorm.DB
		if publicationID > 0 {
			found = tx.Where("user_id = ? AND publication_id = ?", userID, publicationID).Order("id DESC").Limit(1).Find(&item)
			if found.Error != nil {
				return found.Error
			}
		}
		if found == nil || found.RowsAffected == 0 {
			found = tx.Where("user_id = ? AND key = ?", userID, key).Limit(1).Find(&item)
			if found.Error != nil {
				return found.Error
			}
		}
		if found.RowsAffected == 0 {
			item = AdtyTodoItem{
				UserId:        userID,
				SiteId:        siteID,
				Key:           key,
				PublicationId: publicationID,
				Title:         truncateAdtyRunes(title, 200),
				Content:       strings.TrimSpace(content),
				Business:      truncateAdtyRunes(business, 200),
				Severity:      "severe",
				Status:        AdtyTodoStatusPending,
				Revision:      1,
				CreatedAt:     now,
				UpdatedAt:     now,
			}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
			if err := tx.Create(&AdtyTodoStateHistory{
				TodoId:     item.Id,
				UserId:     userID,
				Key:        key,
				FromStatus: "",
				ToStatus:   AdtyTodoStatusPending,
				ActorId:    userID,
				Note:       "created",
				CreatedAt:  now,
			}).Error; err != nil {
				return err
			}
			out = &item
			return nil
		}
		updates := map[string]any{"updated_at": now}
		if key != "" && item.Key != key {
			updates["key"] = key
			item.Key = key
		}
		if publicationID > 0 && item.PublicationId == 0 {
			updates["publication_id"] = publicationID
			item.PublicationId = publicationID
		}
		if trimmedTitle := truncateAdtyRunes(title, 200); trimmedTitle != "" {
			updates["title"] = trimmedTitle
			item.Title = trimmedTitle
		}
		if strings.TrimSpace(content) != "" {
			updates["content"] = strings.TrimSpace(content)
			item.Content = strings.TrimSpace(content)
		}
		if trimmedBusiness := truncateAdtyRunes(business, 200); trimmedBusiness != "" {
			updates["business"] = trimmedBusiness
			item.Business = trimmedBusiness
		}
		if err := tx.Model(&item).Updates(updates).Error; err != nil {
			return err
		}
		out = &item
		return nil
	})
	return out, err
}

// TransitionAdtyTodo advances one severe todo item through the state machine
// (pending -> in_progress -> completed), persisting the severe item state and
// preserving its full history without hiding the underlying notification.
func TransitionAdtyTodo(c *gin.Context, userID int, key, targetStatus, note string) (*AdtyTodoItem, error) {
	key = normalizeAdtyTodoKey(key)
	targetStatus = strings.TrimSpace(targetStatus)
	note = strings.TrimSpace(note)
	if userID <= 0 || key == "" || utf8.RuneCountInString(note) > 500 {
		return nil, common.NewMessage("Invalid parameters")
	}
	if targetStatus != AdtyTodoStatusPending && targetStatus != AdtyTodoStatusInProgress && targetStatus != AdtyTodoStatusCompleted {
		return nil, common.NewMessage("Invalid parameters")
	}
	if err := ensureAdtyTodoTables(model.DB); err != nil {
		return nil, err
	}
	var reqSite *model.AdtySite
	var tokenScopes [][]string
	if c != nil {
		reqSite = AdtySiteForRequest(c)
		if _, isToken := c.Get("access_token_legacy"); isToken {
			tokenScopes = [][]string{c.GetStringSlice("access_token_scopes")}
		}
	}
	now := time.Now().Unix()
	var result *AdtyTodoItem
	var prevStatus string
	var auditEntry *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var user model.User
		hasUser := tx.Select("id", "username", "role", "site_id").Where("id = ?", userID).Limit(1).Find(&user).RowsAffected > 0
		var item AdtyTodoItem
		found := model.AdtyLockForUpdate(tx).Where("user_id = ? AND key = ?", userID, key).Limit(1).Find(&item)
		if found.Error != nil {
			return found.Error
		}
		if found.RowsAffected == 0 {
			// 完成和进行中只改已有待办。缺行、外键、任意公告键都不得在这里物化，
			// 且统一返回 404 避免暴露其他账号是否存在该待办键（R13-019）。
			return gorm.ErrRecordNotFound
		}
		// R7-042: Resolve publication using item.PublicationId after loading item so
		// post-edit ReadKey rotations still link the publication and record AdtyNotificationRead.
		pub := lookupAdtyPublicationForTodoTx(tx, item.Key, item.PublicationId)
		if pub != nil && item.PublicationId == 0 {
			item.PublicationId = pub.Id
		}
		// R6-004 / R13-004: Enforce live visibility (withdrawn/retargeted publication, revoked watcher RBAC, PAT scopes, host isolation).
		visible, err := adtyTodoItemVisibleForUserOnSite(tx, userID, reqSite, &item, pub, tokenScopes...)
		if err != nil {
			return err
		}
		if !visible {
			return gorm.ErrRecordNotFound
		}

		prevStatus = item.Status
		if prevStatus == AdtyTodoStatusCompleted && targetStatus != AdtyTodoStatusCompleted {
			return common.NewMessage("Completed todo item cannot transition backward")
		}
		if prevStatus == targetStatus {
			result = &item
			return nil
		}

		completedAt := item.CompletedAt
		if targetStatus == AdtyTodoStatusCompleted {
			completedAt = now
		}
		nextRev := item.Revision + 1
		updates := map[string]any{
			"status":          targetStatus,
			"revision":        nextRev,
			"updated_at":      now,
			"completed_at":    completedAt,
			"publication_id":  item.PublicationId,
			"resolution_note": note,
		}
		res := tx.Model(&AdtyTodoItem{}).Where("id = ? AND revision = ?", item.Id, item.Revision).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return common.NewMessage("Todo item was modified concurrently, please retry")
		}
		item.Status = targetStatus
		item.Revision = nextRev
		item.UpdatedAt = now
		item.CompletedAt = completedAt
		item.ResolutionNote = note

		if err := tx.Create(&AdtyTodoStateHistory{
			TodoId:     item.Id,
			UserId:     userID,
			Key:        key,
			FromStatus: prevStatus,
			ToStatus:   targetStatus,
			ActorId:    userID,
			Note:       note,
			CreatedAt:  now,
		}).Error; err != nil {
			return err
		}

		if targetStatus == AdtyTodoStatusCompleted && tx.Migrator().HasTable(&model.AdtyNotificationRead{}) {
			// R1-055: Completing a personal todo must NEVER mutate global AdtyAnomalyDisposition rows;
			// anomaly dispositions are exclusively closed by authorized Root review handlers.
			if pub != nil && pub.ReadKey != "" {
				readSiteID := 0
				if pub.Audience == "site" && pub.TargetSiteId > 0 {
					readSiteID = pub.TargetSiteId
				} else if reqSite != nil && reqSite.Id > 0 {
					readSiteID = reqSite.Id
				}
				readRow := model.AdtyNotificationRead{
					UserId:  userID,
					SiteId:  readSiteID,
					ReadKey: pub.ReadKey,
					ReadAt:  now,
				}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&readRow).Error; err != nil {
					return err
				}
			} else if pub == nil && item.Key != "" && tx.Migrator().HasTable(&AdtyChannelWatcherNotice{}) {
				var watcherCount int64
				if err := tx.Model(&AdtyChannelWatcherNotice{}).Where("user_id = ? AND read_key = ?", userID, item.Key).Count(&watcherCount).Error; err != nil {
					return err
				}
				if watcherCount > 0 {
					readRow := model.AdtyNotificationRead{
						UserId:  userID,
						SiteId:  0,
						ReadKey: item.Key,
						ReadAt:  now,
					}
					if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&readRow).Error; err != nil {
						return err
					}
				}
			}
		}
		action := "adty.todo.transition"
		if item.Status == AdtyTodoStatusCompleted {
			action = "adty.todo.complete"
		}
		auditSiteID := item.SiteId
		if auditSiteID == 0 {
			var actorPtr *model.User
			if hasUser {
				actorPtr = &user
			}
			auditSiteID = officialNotificationHideSiteID(c, actorPtr)
		}
		entry, auditErr := model.PrepareOfficialAuditLog(c, model.OfficialAuditWrite{
			UserId:     userID,
			Username:   user.Username,
			ActorRole:  user.Role,
			SiteID:     model.OfficialAuditSiteIDPointer(auditSiteID),
			ObjectType: "todo",
			ObjectID:   item.Key,
			Action:     action,
			Category:   model.AuditCategoryOperation,
			Content:    action,
			Before:     map[string]any{"key": item.Key, "status": prevStatus},
			After:      map[string]any{"key": item.Key, "status": item.Status, "completed_at": item.CompletedAt, "revision": item.Revision},
			Result:     model.AuditResultSuccess,
			Success:    true,
		})
		if auditErr != nil {
			return auditErr
		}
		if err := model.AppendAuditIntentTx(tx, entry); err != nil {
			return err
		}
		auditEntry = &entry
		result = &item
		return nil
	})
	if err != nil {
		return nil, err
	}
	if auditEntry != nil {
		deliverAdtyCommittedAudit(*auditEntry)
	}
	return result, nil
}

func writeAdtyTodoAudit(c *gin.Context, userID int, item *AdtyTodoItem, fromStatus string) {
	if item == nil {
		return
	}
	action := "adty.todo.transition"
	if item.Status == AdtyTodoStatusCompleted {
		action = "adty.todo.complete"
	}
	rec := model.OfficialAuditWrite{
		UserId:     userID,
		SiteID:     model.OfficialAuditSiteIDPointer(item.SiteId),
		ObjectType: "todo",
		ObjectID:   item.Key,
		Action:     action,
		Category:   model.AuditCategoryOperation,
		Content:    action,
		Before:     map[string]any{"key": item.Key, "status": fromStatus},
		After:      map[string]any{"key": item.Key, "status": item.Status, "completed_at": item.CompletedAt, "revision": item.Revision},
		Result:     model.AuditResultSuccess,
		Success:    true,
	}
	var actor *model.User
	if userID > 0 {
		if user, err := model.GetUserById(userID, false); err == nil && user != nil {
			actor = user
			rec.Username = user.Username
			rec.ActorRole = user.Role
		}
	}
	if item.SiteId == 0 {
		rec.SiteID = model.OfficialAuditSiteIDPointer(officialNotificationHideSiteID(c, actor))
	}
	if err := model.WriteOfficialAuditLog(c, rec); err != nil {
		common.SysLog(common.LogText("official todo audit write failed: %s", err.Error()))
	}
}

// CompleteAdtyTodo marks one severe inbox item done for that user, persisting
// the state transition and preserving history rather than hiding the notice.
func CompleteAdtyTodo(userID int, key string) error {
	_, err := TransitionAdtyTodo(nil, userID, key, AdtyTodoStatusCompleted, "manual complete")
	return err
}

// CompleteAdtyTodoFromRequest transitions a severe todo item to completed with request context.
func CompleteAdtyTodoFromRequest(c *gin.Context, userID int, key string) (*AdtyTodoItem, error) {
	return TransitionAdtyTodo(c, userID, key, AdtyTodoStatusCompleted, "manual complete")
}

// MarkAdtyTodoInProgress is the service entry for pending -> in_progress.
// It updates an owned todo only. A missing or foreign key is rejected.
func MarkAdtyTodoInProgress(userID int, key, note string) (*AdtyTodoItem, error) {
	if strings.TrimSpace(note) == "" {
		note = "in progress"
	}
	return TransitionAdtyTodo(nil, userID, key, AdtyTodoStatusInProgress, note)
}

// MarkAdtyTodoInProgressFromRequest is the request-scoped in_progress entry.
func MarkAdtyTodoInProgressFromRequest(c *gin.Context, userID int, key, note string) (*AdtyTodoItem, error) {
	if strings.TrimSpace(note) == "" {
		note = "in progress"
	}
	return TransitionAdtyTodo(c, userID, key, AdtyTodoStatusInProgress, note)
}

func adtyPublicationIsSevere(db *gorm.DB, pub *model.AdtyNotificationPublication) bool {
	if pub == nil || pub.Status != "published" {
		return false
	}
	business := strings.TrimSpace(pub.Business)
	readKey := strings.TrimSpace(pub.ReadKey)
	if strings.HasSuffix(business, "_anomaly") || business == "severe_todo" || strings.HasPrefix(business, "severe:") || strings.HasPrefix(readKey, "severe:") {
		return true
	}
	if db == nil || !db.Migrator().HasTable(&model.AdtyAnomalyDisposition{}) {
		return false
	}
	refs := make([]string, 0, 2)
	if readKey != "" {
		refs = append(refs, readKey)
	}
	if business != "" && business != readKey {
		refs = append(refs, business)
	}
	if len(refs) == 0 {
		return false
	}
	var count int64
	if err := db.Model(&model.AdtyAnomalyDisposition{}).
		Where("ref IN ? AND status IN ?", refs, []string{"open", "reviewing"}).
		Count(&count).Error; err != nil {
		return false
	}
	return count > 0
}

// EnsureAdtySevereTodoForVisiblePublication creates the reader's pending severe
// todo when a visible publication is severe. It does not change an existing status.
func EnsureAdtySevereTodoForVisiblePublication(userID, siteID int, pub *model.AdtyNotificationPublication) (*AdtyTodoItem, error) {
	if userID <= 0 || pub == nil || strings.TrimSpace(pub.ReadKey) == "" || !adtyPublicationIsSevere(model.DB, pub) {
		return nil, nil
	}
	return UpsertAdtySevereTodo(userID, siteID, pub.ReadKey, pub.Title, pub.Content, pub.Business, pub.Id)
}

// produceAdtySevereTodosForPublished creates todos for a concrete published target.
// all_users and all_sites are not fanned out here; the reader path creates those.
func produceAdtySevereTodosForPublished(row *model.AdtyNotificationPublication) {
	if row == nil || model.DB == nil || !adtyPublicationIsSevere(model.DB, row) {
		return
	}
	if row.Id > 0 && !adtyNotificationRevisionIsCurrent(row.Id, row.Revision) {
		return
	}
	switch row.Audience {
	case "user":
		raw, ok := strings.CutPrefix(row.TargetSiteName, "user:")
		if !ok {
			return
		}
		userID, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || userID <= 0 {
			return
		}
		siteID := row.TargetSiteId
		if siteID == 0 {
			var user model.User
			if model.DB.Select("id", "site_id").Where("id = ?", userID).Limit(1).Find(&user).RowsAffected > 0 {
				siteID = user.SiteId
			}
		}
		if _, err := UpsertAdtySevereTodo(userID, siteID, row.ReadKey, row.Title, row.Content, row.Business, row.Id); err != nil {
			common.SysLog(common.LogText("severe todo was not created for published user notice %s: %s", row.ReadKey, err.Error()))
		}
	case "site":
		if row.TargetSiteId <= 0 {
			return
		}
		var site model.AdtySite
		if err := model.DB.Select("id", "owner_user_id").Where("id = ?", row.TargetSiteId).Limit(1).Find(&site).Error; err != nil || site.OwnerUserId <= 0 {
			return
		}
		if _, err := UpsertAdtySevereTodo(site.OwnerUserId, site.Id, row.ReadKey, row.Title, row.Content, row.Business, row.Id); err != nil {
			common.SysLog(common.LogText("severe todo was not created for published site notice %s: %s", row.ReadKey, err.Error()))
		}
	}
}

// MigrateAdtyOpsClusterTables migrates the ops-cluster tables that are not yet
// in model.MigrateAdtyTables. Callers in model must not be added by this change.
func MigrateAdtyOpsClusterTables(db *gorm.DB) error {
	if db == nil {
		return common.NewMessage("Database is not initialized")
	}
	return db.AutoMigrate(&AdtyTodoItem{}, &AdtyTodoStateHistory{}, &AdtyBilingualNoticeTemplate{}, &AdtyChannelWatcherNotice{}, &AdtyChannelWatcherOutbox{})
}

// ListAdtyTodosForUser returns severe todo items for userID, optionally including completed items.
func ListAdtyTodosForUser(userID int, includeCompleted bool) ([]AdtyTodoItem, error) {
	return ListAdtyTodosForUserOnSite(userID, nil, includeCompleted)
}

// ListAdtyTodosForUserOnSite returns severe todo items for userID filtered against
// withdrawn/retargeted announcements (R5-058, R6-004) and revoked/subsite watcher permissions (R5-025).
func ListAdtyTodosForUserOnSite(userID int, site *model.AdtySite, includeCompleted bool, tokenScopes ...[]string) ([]AdtyTodoItem, error) {
	if userID <= 0 {
		return nil, common.NewMessage("Invalid parameters")
	}
	if err := ensureAdtyTodoTables(model.DB); err != nil {
		return nil, err
	}
	q := model.DB.Where("user_id = ?", userID)
	if !includeCompleted {
		q = q.Where("status <> ?", AdtyTodoStatusCompleted)
	}
	var items []AdtyTodoItem
	if err := q.Order("id DESC").Find(&items).Error; err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return items, nil
	}
	filtered := make([]AdtyTodoItem, 0, len(items))
	for i := range items {
		visible, err := adtyTodoItemVisibleForUserOnSite(model.DB, userID, site, &items[i], nil, tokenScopes...)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		filtered = append(filtered, items[i])
	}
	return filtered, nil
}

// GetAdtyTodoHistory returns the ordered state transition history for one user's todo key.
func GetAdtyTodoHistory(userID int, key string) ([]AdtyTodoStateHistory, error) {
	key = normalizeAdtyTodoKey(key)
	if userID <= 0 || key == "" {
		return nil, common.NewMessage("Invalid parameters")
	}
	if err := ensureAdtyTodoTables(model.DB); err != nil {
		return nil, err
	}
	var item AdtyTodoItem
	found := model.DB.Where("user_id = ? AND key = ?", userID, key).Limit(1).Find(&item)
	if found.Error != nil {
		return nil, found.Error
	}
	if found.RowsAffected == 0 {
		if pub := lookupAdtyPublicationForTodoTx(model.DB, key, 0); pub != nil && pub.Id > 0 {
			if err := model.DB.Where("user_id = ? AND publication_id = ?", userID, pub.Id).Limit(1).Find(&item).Error; err != nil {
				return nil, err
			}
		}
	}
	var history []AdtyTodoStateHistory
	if item.Id > 0 {
		err := model.DB.Where("user_id = ? AND (todo_id = ? OR key = ?)", userID, item.Id, key).Order("id ASC").Find(&history).Error
		return history, err
	}
	err := model.DB.Where("user_id = ? AND key = ?", userID, key).Order("id ASC").Find(&history).Error
	return history, err
}

// RenderAdtyTodoInboxItems enriches inbox announcement items with persisted todo state
// so severe todo items remain in history with their live status and completion timestamp.
func RenderAdtyTodoInboxItems(userID int, announcements []map[string]any) []map[string]any {
	if userID <= 0 || len(announcements) == 0 || model.DB == nil || !model.DB.Migrator().HasTable(&AdtyTodoItem{}) {
		return announcements
	}
	var todos []AdtyTodoItem
	if err := model.DB.Where("user_id = ?", userID).Find(&todos).Error; err != nil || len(todos) == 0 {
		return announcements
	}
	hasPubTable := model.DB.Migrator().HasTable(&model.AdtyNotificationPublication{})
	severePubs := make(map[int]bool)
	if hasPubTable {
		pubIDSet := make(map[int]struct{})
		for _, td := range todos {
			if td.PublicationId > 0 {
				pubIDSet[td.PublicationId] = struct{}{}
			}
		}
		for _, item := range announcements {
			if idStr, _ := item["id"].(string); strings.HasPrefix(idStr, "publication:") {
				if pubID, err := strconv.Atoi(strings.TrimPrefix(idStr, "publication:")); err == nil && pubID > 0 {
					pubIDSet[pubID] = struct{}{}
				}
			}
		}
		if len(pubIDSet) > 0 {
			pubIDs := make([]int, 0, len(pubIDSet))
			for id := range pubIDSet {
				pubIDs = append(pubIDs, id)
			}
			var pubs []model.AdtyNotificationPublication
			if err := model.DB.Where("id IN ?", pubIDs).Find(&pubs).Error; err == nil {
				for i := range pubs {
					if pubs[i].Status == "published" && adtyPublicationIsSevere(model.DB, &pubs[i]) {
						severePubs[pubs[i].Id] = true
					}
				}
			}
		}
	}
	byKey := make(map[string]AdtyTodoItem, len(todos)*2)
	for _, td := range todos {
		if td.PublicationId > 0 && (!hasPubTable || !severePubs[td.PublicationId]) {
			continue
		}
		byKey[td.Key] = td
		if td.PublicationId > 0 {
			byKey[fmt.Sprintf("publication:%d", td.PublicationId)] = td
		}
	}
	for _, item := range announcements {
		idStr, _ := item["id"].(string)
		if strings.HasPrefix(idStr, "publication:") {
			pubID, err := strconv.Atoi(strings.TrimPrefix(idStr, "publication:"))
			if err == nil && pubID > 0 && (!hasPubTable || !severePubs[pubID]) {
				continue
			}
		}
		readKey, _ := item["read_key"].(string)
		if td, ok := byKey[readKey]; ok {
			item["todo_key"] = td.Key
			item["todo_status"] = td.Status
			item["todo_severity"] = td.Severity
			item["todo_completed_at"] = td.CompletedAt
			item["todo_revision"] = td.Revision
		} else if td, ok := byKey[idStr]; ok {
			item["todo_key"] = td.Key
			item["todo_status"] = td.Status
			item["todo_severity"] = td.Severity
			item["todo_completed_at"] = td.CompletedAt
			item["todo_revision"] = td.Revision
		}
	}
	return announcements
}

// AdtyNotificationAudienceVisibleOnSite reports whether a notification publication audience
// is visible on viewerSiteID (0 for main site, >0 for subsite). Root-published all_users
// and all_sites notifications are platform-wide across main and subsites (A.18.2 / features.md:16).
func AdtyNotificationAudienceVisibleOnSite(audience string, targetSiteID, viewerSiteID int) bool {
	switch strings.TrimSpace(audience) {
	case "all_users", "all_sites", "all":
		return viewerSiteID >= 0
	case "site":
		return targetSiteID > 0 && targetSiteID == viewerSiteID
	default:
		return false
	}
}

// QuotaToUSD converts internal quota to USD at the fixed integer scale.
func QuotaToUSD(quota int64) decimal.Decimal {
	return decimal.NewFromInt(quota).Div(common.QuotaPerUnitDecimal())
}

// errSiteInviteNeedsReview refuses the bind. The anomaly row is written after
// this transaction rolls back, so the review fact is not discarded with the invite.
var errSiteInviteNeedsReview = common.NewMessage("This invite needs review")

// BindSiteInviteFromIP records one direct site invite and keeps the client IP hash.
// It serializes on locked inviter/invitee user rows with first-payment completion
// so a concurrent paid order cannot leave a post-payment binding.
func BindSiteInviteFromIP(siteID, inviterID, inviteeID int, ipHash string) error {
	if siteID <= 0 || inviterID <= 0 || inviteeID <= 0 || inviterID == inviteeID {
		return common.NewMessage("Invalid parameters")
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		return bindSiteInviteFromIPTx(tx, siteID, inviterID, inviteeID, ipHash)
	})
	if err == errSiteInviteNeedsReview {
		if writeErr := recordSiteInviteBatchAnomaly(siteID, inviterID); writeErr != nil {
			return writeErr
		}
	}
	return err
}

func bindSiteInviteFromIPTx(tx *gorm.DB, siteID, inviterID, inviteeID int, ipHash string) error {
	if siteID <= 0 || inviterID <= 0 || inviteeID <= 0 || inviterID == inviteeID {
		return common.NewMessage("Invalid parameters")
	}
	firstID, secondID := inviterID, inviteeID
	if secondID < firstID {
		firstID, secondID = secondID, firstID
	}
	var firstUser, secondUser model.User
	if err := model.AdtyLockForUpdate(tx).First(&firstUser, firstID).Error; err != nil {
		return err
	}
	if err := model.AdtyLockForUpdate(tx).First(&secondUser, secondID).Error; err != nil {
		return err
	}
	inviter, invitee := firstUser, secondUser
	if inviterID != firstID {
		inviter, invitee = secondUser, firstUser
	}
	if inviter.SiteId != siteID || invitee.SiteId != siteID ||
		inviter.Status != common.UserStatusEnabled || invitee.Status != common.UserStatusEnabled ||
		inviter.StaffSiteId != 0 || invitee.StaffSiteId != 0 ||
		inviter.Role != common.RoleCommonUser || invitee.Role != common.RoleCommonUser {
		return common.NewMessage("No permission to perform this action")
	}
	if isEmp, empErr := adtyIsIndependentTeamEmployeeTx(tx, inviterID); empErr != nil {
		return empErr
	} else if isEmp {
		return common.NewMessage("No permission to perform this action")
	}
	if isEmp, empErr := adtyIsIndependentTeamEmployeeTx(tx, inviteeID); empErr != nil {
		return empErr
	} else if isEmp {
		return common.NewMessage("No permission to perform this action")
	}
	var site model.AdtySite
	if err := tx.First(&site, siteID).Error; err != nil {
		return err
	}
	if site.Status == model.AdtySiteStatusClosed || site.Status == model.AdtySiteStatusClosing {
		return common.NewMessage("Site is not open for registration")
	}
	var rate model.AdtySiteReferralRate
	if err := tx.Where("site_id = ?", siteID).First(&rate).Error; err != nil {
		return common.NewMessage("Site invite rate is not configured")
	}
	if !rate.Enabled {
		return common.NewMessage("Site referral is disabled")
	}
	effectiveRate := rate.Rate
	if aff, affFound, affErr := loadSiteReferralAffiliateTx(tx, siteID, inviterID); affErr != nil {
		return affErr
	} else if affFound {
		if aff.Status == model.AdtySiteAffiliateStatusDisabled {
			return common.NewMessage("This affiliate is disabled")
		}
		if aff.RateOverride != nil {
			effectiveRate = *aff.RateOverride
		}
	}
	if !effectiveRate.IsPositive() {
		return common.NewMessage("Site invite rate is not configured")
	}
	var paid int64
	if err := tx.Model(&model.TopUp{}).Where("user_id = ? AND status IN ?", inviteeID, []string{common.TopUpStatusSuccess, model.TopUpStatusRefunded}).Count(&paid).Error; err != nil {
		return err
	}
	if paid > 0 {
		return common.NewMessage("This customer already has a paid order")
	}
	var existing model.AdtySiteInvite
	lookup := tx.Where("invitee_id = ?", inviteeID).Limit(1).Find(&existing)
	if lookup.Error != nil {
		return lookup.Error
	}
	if lookup.RowsAffected > 0 {
		if existing.InviterId != inviterID || existing.SiteId != siteID {
			return common.NewMessage("Invitee already has an inviter")
		}
		return nil
	}
	// R14-025: Prevent mutual or cyclic subsite invite chains (matching main-site referral rules).
	ancestorID := inviterID
	visited := map[int]struct{}{inviteeID: {}}
	for hops := 0; hops < 64 && ancestorID > 0; hops++ {
		if _, seen := visited[ancestorID]; seen {
			return common.NewMessage("Cannot create a circular invite relationship")
		}
		visited[ancestorID] = struct{}{}
		var parent model.AdtySiteInvite
		parentLookup := tx.Select("inviter_id").Where("site_id = ? AND invitee_id = ?", siteID, ancestorID).Limit(1).Find(&parent)
		if parentLookup.Error != nil {
			return parentLookup.Error
		}
		if parentLookup.RowsAffected == 0 || parent.InviterId <= 0 {
			break
		}
		ancestorID = parent.InviterId
	}
	if site.MarketingPolicy.BatchRegisterThreshold == nil {
		if err := refuseSiteInviteBatchTx(tx, siteID, inviterID, ipHash); err != nil {
			return err
		}
	}
	trimmedIP := strings.TrimSpace(ipHash)
	riskFlag := ""
	selfInviteDays := int64(30)
	if site.MarketingPolicy.SelfInviteWindowDays != nil {
		selfInviteDays = *site.MarketingPolicy.SelfInviteWindowDays
	}
	if selfInviteDays > 0 && tx.Migrator().HasTable(&model.AdtyUserFingerprint{}) {
		since := time.Now().Unix() - selfInviteDays*86400
		ipSet := map[string]struct{}{}
		deviceSet := map[string]struct{}{}
		if trimmedIP != "" {
			ipSet[trimmedIP] = struct{}{}
		}
		var inviteeFPs []model.AdtyUserFingerprint
		if err := tx.Where("user_id = ? AND created_at >= ?", inviteeID, since).Order("id DESC").Find(&inviteeFPs).Error; err == nil {
			for _, fp := range inviteeFPs {
				if v := strings.TrimSpace(fp.IpHash); v != "" {
					ipSet[v] = struct{}{}
					if trimmedIP == "" {
						trimmedIP = v
					}
				}
				if v := strings.TrimSpace(fp.DeviceHash); v != "" {
					deviceSet[v] = struct{}{}
				}
			}
		}
		ips := make([]string, 0, len(ipSet))
		for v := range ipSet {
			ips = append(ips, v)
		}
		devices := make([]string, 0, len(deviceSet))
		for v := range deviceSet {
			devices = append(devices, v)
		}
		if len(ips) > 0 || len(devices) > 0 {
			fpQ := tx.Model(&model.AdtyUserFingerprint{}).Where("user_id = ? AND created_at >= ?", inviterID, since)
			switch {
			case len(ips) > 0 && len(devices) > 0:
				fpQ = fpQ.Where("(ip_hash IN ? OR device_hash IN ?)", ips, devices)
			case len(ips) > 0:
				fpQ = fpQ.Where("ip_hash IN ?", ips)
			default:
				fpQ = fpQ.Where("device_hash IN ?", devices)
			}
			var fpMatches int64
			if err := fpQ.Count(&fpMatches).Error; err == nil && fpMatches > 0 {
				riskFlag = model.ReferralRiskSelfInvite
				note := "self invite: invitee shares IP or device with the inviter"
				if err := upsertSiteReferralAffiliateRiskTx(tx, siteID, inviterID, false, model.ReferralRiskSelfInvite, note); err != nil {
					return err
				}
				if err := upsertSiteReferralAffiliateRiskTx(tx, siteID, inviteeID, false, model.ReferralRiskSelfInvite, note); err != nil {
					return err
				}
			}
		}
	}
	if err := tx.Create(&model.AdtySiteInvite{
		SiteId: siteID, InviterId: inviterID, InviteeId: inviteeID, Rate: effectiveRate, IpHash: trimmedIP, RiskFlag: riskFlag,
	}).Error; err != nil {
		return err
	}
	if site.MarketingPolicy.BatchRegisterThreshold != nil && *site.MarketingPolicy.BatchRegisterThreshold > 0 && trimmedIP != "" {
		hours := int64(24)
		if site.MarketingPolicy.BatchRegisterWindowHours != nil && *site.MarketingPolicy.BatchRegisterWindowHours > 0 {
			hours = *site.MarketingPolicy.BatchRegisterWindowHours
		}
		since := time.Now().Unix() - hours*3600
		var recentCount int64
		if err := tx.Model(&model.AdtySiteInvite{}).
			Where("site_id = ? AND inviter_id = ? AND ip_hash = ? AND created_at >= ?", siteID, inviterID, trimmedIP, since).
			Count(&recentCount).Error; err == nil && recentCount >= *site.MarketingPolicy.BatchRegisterThreshold {
			if err := upsertSiteReferralAffiliateRiskTx(tx, siteID, inviterID, true, model.ReferralRiskBatchRegister, "settlement frozen: several sign-ups from one IP"); err != nil {
				return err
			}
		}
	}
	return nil
}

func refuseSiteInviteBatchTx(tx *gorm.DB, siteID, inviterID int, ipHash string) error {
	ipHash = strings.TrimSpace(ipHash)
	if ipHash == "" {
		return nil
	}
	thresholdRaw, thresholdOK, err := model.ReadOptionFromDB(adtySiteBatchThresholdKey)
	if err != nil || !thresholdOK {
		return err
	}
	windowRaw, windowOK, err := model.ReadOptionFromDB(adtySiteBatchWindowKey)
	if err != nil || !windowOK {
		return err
	}
	threshold, err := strconv.ParseInt(strings.TrimSpace(thresholdRaw), 10, 64)
	if err != nil || threshold <= 0 {
		return nil
	}
	hours, err := strconv.ParseInt(strings.TrimSpace(windowRaw), 10, 64)
	if err != nil || hours <= 0 {
		return nil
	}
	since := time.Now().Unix() - hours*3600
	var count int64
	if err := tx.Model(&model.AdtySiteInvite{}).
		Where("site_id = ? AND inviter_id = ? AND ip_hash = ? AND created_at >= ?", siteID, inviterID, ipHash, since).
		Count(&count).Error; err != nil {
		return err
	}
	if count+1 < threshold {
		return nil
	}
	return errSiteInviteNeedsReview
}

// recordSiteInviteBatchAnomaly commits the review fact after the refused bind rolls back.
func recordSiteInviteBatchAnomaly(siteID, inviterID int) error {
	ref := strconv.Itoa(siteID)
	note := "inviter " + strconv.Itoa(inviterID)
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var existing int64
		if err := tx.Model(&model.AdtyAnomalyDisposition{}).
			Where("kind = ? AND ref = ? AND status = ? AND note = ?", "batch_register", ref, "open", note).
			Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return nil
		}
		return tx.Create(&model.AdtyAnomalyDisposition{Kind: "batch_register", Ref: ref, Status: "open", Note: note}).Error
	})
}

// SetSiteReferralRate stores the site's one-level invite rate. Rate is a fraction in [0, 1].
func SetSiteReferralRate(siteID int, rate decimal.Decimal) error {
	if siteID <= 0 || rate.IsNegative() || rate.GreaterThan(decimal.NewFromInt(1)) {
		return common.NewMessage("Invalid parameters")
	}
	row := model.AdtySiteReferralRate{SiteId: siteID, Rate: rate}
	return model.DB.Where(model.AdtySiteReferralRate{SiteId: siteID}).Assign(map[string]any{"rate": rate}).FirstOrCreate(&row).Error
}

// SiteInviteCommissionUSD is face times the site rate. It does not add a payment-method gift.
func SiteInviteCommissionUSD(face, rate decimal.Decimal) decimal.Decimal {
	if !face.IsPositive() || !rate.IsPositive() {
		return decimal.Zero
	}
	return face.Mul(rate).Round(8)
}

// recordAdtyAnomalyDisposition stores a review note for one detected anomaly.
func recordAdtyAnomalyDisposition(kind, ref, status, note string, operatorID int) error {
	kind, ref, status = strings.TrimSpace(kind), strings.TrimSpace(ref), strings.TrimSpace(status)
	if kind == "" || ref == "" || status == "" {
		return common.NewMessage("Invalid parameters")
	}
	return model.DB.Create(&model.AdtyAnomalyDisposition{Kind: kind, Ref: ref, Status: status, Note: note, OperatorId: operatorID}).Error
}

// HideAdtyNotificationForUser removes one inbox item for that user only.
func HideAdtyNotificationForUser(userID int, key string) error {
	return hideAdtyNotificationForUser(nil, userID, key)
}

// HideAdtyNotificationForUserFromRequest is the request-scoped hide path so
// the official audit can use the site already resolved for this request.
func HideAdtyNotificationForUserFromRequest(c *gin.Context, userID int, key string) error {
	key = strings.TrimSpace(key)
	if userID <= 0 || key == "" {
		return common.NewMessage("Invalid parameters")
	}
	if adtyIsNotificationKeyHidden(userID, key) {
		return nil
	}
	var tokenScopes [][]string
	if c != nil {
		if _, isToken := c.Get("access_token_legacy"); isToken {
			tokenScopes = [][]string{c.GetStringSlice("access_token_scopes")}
		}
	}
	canonicalKey, found, err := resolveAdtyNotificationCanonicalKey(userID, AdtySiteForRequest(c), key, tokenScopes...)
	if err != nil {
		return err
	}
	if !found || strings.TrimSpace(canonicalKey) == "" {
		return gorm.ErrRecordNotFound
	}
	canonicalKey = strings.TrimSpace(canonicalKey)
	if adtyIsNotificationKeyHidden(userID, canonicalKey) {
		return nil
	}
	return hideAdtyNotificationForUser(c, userID, canonicalKey)
}

func hideAdtyNotificationForUser(c *gin.Context, userID int, key string) error {
	key = strings.TrimSpace(key)
	if userID <= 0 || key == "" {
		return common.NewMessage("Invalid parameters")
	}
	var event model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		row := model.AdtyNotificationHide{UserId: userID, Key: key}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		var actor model.User
		if err := tx.First(&actor, userID).Error; err != nil {
			return err
		}
		var err error
		event, err = model.PrepareOfficialAuditLog(c, model.OfficialAuditWrite{
			UserId: userID, Username: actor.Username, ActorRole: actor.Role,
			SiteID:     model.OfficialAuditSiteIDPointer(officialNotificationHideSiteID(c, &actor)),
			ObjectType: "notification", ObjectID: key, Action: "adty.notification.hide",
			Content: "adty.notification.hide", Result: model.AuditResultSuccess, Success: true,
			Before: map[string]any{"hidden": false}, After: map[string]any{"hidden": true},
		})
		if err != nil {
			return err
		}
		return model.AppendAuditIntentTx(tx, event)
	})
	if err == nil && event.EventId != "" {
		deliverAdtyCommittedAudit(event)
	}
	return err
}

func officialNotificationHideSiteID(c *gin.Context, user *model.User) int {
	if site := AdtySiteForRequest(c); site != nil {
		return site.Id
	}
	if user != nil {
		return user.SiteId
	}
	return 0
}

// AdtyBilingualNoticeTemplate stores fixed Chinese/English system notice templates
// maintained by Root (G.11.4 / A.18.4 / ISS-R1-0151).
type AdtyBilingualNoticeTemplate struct {
	Id        int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	Key       string `json:"key" gorm:"type:varchar(128);not null;uniqueIndex"`
	Category  string `json:"category" gorm:"type:varchar(64);not null;default:'system'"`
	TitleZh   string `json:"title_zh" gorm:"type:varchar(255);not null;default:''"`
	ContentZh string `json:"content_zh" gorm:"type:text"`
	TitleEn   string `json:"title_en" gorm:"type:varchar(255);not null;default:''"`
	ContentEn string `json:"content_en" gorm:"type:text"`
	Revision  int64  `json:"revision" gorm:"not null;default:1"`
	UpdatedBy int    `json:"updated_by" gorm:"not null;default:0"`
	UpdatedAt int64  `json:"updated_at" gorm:"not null;default:0"`
}

func (AdtyBilingualNoticeTemplate) TableName() string {
	return "adty_bilingual_notice_templates"
}

var defaultAdtyBilingualNoticeTemplates = map[string]AdtyBilingualNoticeTemplate{
	"channel_disabled": {
		Key:       "channel_disabled",
		Category:  "channel",
		TitleZh:   "通道「{{1}}」（#{{2}}）已被禁用",
		ContentZh: "通道「{{1}}」（#{{2}}）已被禁用，原因：{{3}}",
		TitleEn:   `Channel "{{1}}" (#{{2}}) has been disabled`,
		ContentEn: `Channel "{{1}}" (#{{2}}) has been disabled, reason: {{3}}`,
		Revision:  1,
	},
	"channel_enabled": {
		Key:       "channel_enabled",
		Category:  "channel",
		TitleZh:   "通道「{{1}}」（#{{2}}）已被启用",
		ContentZh: "通道「{{1}}」（#{{2}}）已被恢复启用",
		TitleEn:   `Channel "{{1}}" (#{{2}}) has been enabled`,
		ContentEn: `Channel "{{1}}" (#{{2}}) has been enabled`,
		Revision:  1,
	},
	"channel_test": {
		Key:       "channel_test",
		Category:  "channel",
		TitleZh:   "通道测试完成",
		ContentZh: "全部通道可用性测试已完成",
		TitleEn:   "Channel test completed",
		ContentEn: "All channel availability tests have completed",
		Revision:  1,
	},
	"channel_update": {
		Key:       "channel_update",
		Category:  "channel",
		TitleZh:   "上游模型变更通知",
		ContentZh: "检测到上游渠道模型列表变更：{{1}}",
		TitleEn:   "Upstream model update notice",
		ContentEn: "Detected upstream channel model changes: {{1}}",
		Revision:  1,
	},
	"quota_exceed": {
		Key:       "quota_exceed",
		Category:  "wallet",
		TitleZh:   "{{SystemName}} 余额不足提醒",
		ContentZh: "您的账户当前余额为 {{1}}，已低于预警阈值 {{2}}，请及时充值。",
		TitleEn:   "{{SystemName}} Low balance reminder",
		ContentEn: "Your account balance is {{1}}, which is below the threshold {{2}}. Please top up soon.",
		Revision:  1,
	},
	"telegram_bind_confirm": {
		Key:       "telegram_bind_confirm",
		Category:  "telegram",
		TitleZh:   "Telegram 通知绑定成功",
		ContentZh: "您的账号已成功绑定 Telegram 通知（站点：{{1}}）。",
		TitleEn:   "Telegram notification bound",
		ContentEn: "Your account is now bound to Telegram notifications (site: {{1}}).",
		Revision:  1,
	},
	"telegram_unbind_confirm": {
		Key:       "telegram_unbind_confirm",
		Category:  "telegram",
		TitleZh:   "Telegram 通知已解绑",
		ContentZh: "您的账号已解除 Telegram 通知绑定（站点：{{1}}）。",
		TitleEn:   "Telegram notification unbound",
		ContentEn: "Your account has been unbound from Telegram notifications (site: {{1}}).",
		Revision:  1,
	},
	"site_close_migration": {
		Key:       "site_close_migration",
		Category:  "site",
		TitleZh:   "分站关闭与主站承接通知",
		ContentZh: "您所在的分站 {{1}} 已关闭，账户及剩余可用余额已由主站承接。",
		TitleEn:   "Subsite closure and main-site takeover notice",
		ContentEn: "Your subsite {{1}} has closed; your account and remaining balance are now served by the main site.",
		Revision:  1,
	},
	"telegram_bot_reply": {
		Key:       "telegram_bot_reply",
		Category:  "telegram",
		TitleZh:   "此机器人仅用于通知",
		ContentZh: "此机器人仅用于通知，请前往网站操作",
		TitleEn:   "Notifications only",
		ContentEn: "This bot only sends notifications. Please use the website.",
		Revision:  1,
	},
	"telegram_bind_staged": {
		Key:       "telegram_bind_staged",
		Category:  "telegram",
		TitleZh:   "绑定请求已收到",
		ContentZh: "已收到绑定请求，请回到网站确认。",
		TitleEn:   "Binding request received",
		ContentEn: "Binding request received. Confirm it on the website.",
		Revision:  1,
	},
	"telegram_test_notice": {
		Key:       "telegram_test_notice",
		Category:  "telegram",
		TitleZh:   "测试通知",
		ContentZh: "测试通知",
		TitleEn:   "Test notice",
		ContentEn: "Test notice",
		Revision:  1,
	},
}

var adtyNoticePlaceholderPattern = regexp.MustCompile(`\{\{[A-Za-z0-9_.]+\}\}|\{[A-Za-z0-9_.]+\}`)

func extractAdtyNoticePlaceholders(texts ...string) []string {
	seen := map[string]struct{}{}
	for _, text := range texts {
		for _, match := range adtyNoticePlaceholderPattern.FindAllString(text, -1) {
			seen[match] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ValidateAdtyNoticeTemplatePlaceholders verifies that Chinese and English templates
// preserve the exact same placeholder tokens (ADTY-19-10 / G.11.4).
func ValidateAdtyNoticeTemplatePlaceholders(titleZh, contentZh, titleEn, contentEn string) error {
	zhTokens := extractAdtyNoticePlaceholders(titleZh, contentZh)
	enTokens := extractAdtyNoticePlaceholders(titleEn, contentEn)
	if len(zhTokens) != len(enTokens) {
		return common.NewMessage("Chinese and English template placeholders do not match")
	}
	for i := range zhTokens {
		if zhTokens[i] != enTokens[i] {
			return common.NewMessage("Chinese and English template placeholders do not match")
		}
	}
	return nil
}

func ensureAdtyNoticeTemplateTable(db *gorm.DB) error {
	if db == nil {
		return common.NewMessage("Database is not initialized")
	}
	if !db.Migrator().HasTable(&AdtyBilingualNoticeTemplate{}) {
		if err := db.AutoMigrate(&AdtyBilingualNoticeTemplate{}); err != nil {
			return err
		}
	}
	return nil
}

func syncLegacyAdtyNoticeTemplatesTable(tx *gorm.DB, tpl AdtyBilingualNoticeTemplate) {
	if tx == nil || !tx.Migrator().HasTable("adty_notice_templates") {
		return
	}
	cols, err := tx.Migrator().ColumnTypes("adty_notice_templates")
	if err != nil || len(cols) == 0 {
		return
	}
	colSet := make(map[string]bool, len(cols))
	for _, c := range cols {
		colSet[strings.ToLower(c.Name())] = true
	}
	keyCol := ""
	for _, candidate := range []string{"key", "template_key", "code", "name"} {
		if colSet[candidate] {
			keyCol = candidate
			break
		}
	}
	if keyCol == "" {
		return
	}
	if colSet["locale"] {
		for _, pair := range []struct {
			locale  string
			title   string
			content string
		}{
			{"zh-CN", tpl.TitleZh, tpl.ContentZh},
			{"en", tpl.TitleEn, tpl.ContentEn},
		} {
			row := map[string]any{keyCol: tpl.Key, "locale": pair.locale}
			if colSet["title"] {
				row["title"] = pair.title
			}
			if colSet["content"] {
				row["content"] = pair.content
			}
			if colSet["body"] {
				row["body"] = pair.content
			}
			if colSet["updated_at"] {
				row["updated_at"] = tpl.UpdatedAt
			}
			var count int64
			_ = tx.Table("adty_notice_templates").Where(keyCol+" = ? AND locale = ?", tpl.Key, pair.locale).Count(&count).Error
			if count > 0 {
				_ = tx.Table("adty_notice_templates").Where(keyCol+" = ? AND locale = ?", tpl.Key, pair.locale).Updates(row).Error
			} else {
				_ = tx.Table("adty_notice_templates").Create(row).Error
			}
		}
		return
	}
	row := map[string]any{keyCol: tpl.Key}
	for _, k := range []string{"zh_cn", "zh", "content_zh", "body_zh"} {
		if colSet[k] {
			row[k] = tpl.ContentZh
		}
	}
	for _, k := range []string{"en_us", "en", "content_en", "body_en"} {
		if colSet[k] {
			row[k] = tpl.ContentEn
		}
	}
	if colSet["title_zh"] {
		row["title_zh"] = tpl.TitleZh
	}
	if colSet["title_en"] {
		row["title_en"] = tpl.TitleEn
	}
	if colSet["title"] && !colSet["title_zh"] {
		row["title"] = tpl.TitleZh
	}
	if colSet["content"] && !colSet["content_zh"] {
		row["content"] = tpl.ContentZh + "\n\n" + tpl.ContentEn
	}
	if colSet["updated_at"] {
		row["updated_at"] = tpl.UpdatedAt
	}
	var count int64
	_ = tx.Table("adty_notice_templates").Where(keyCol+" = ?", tpl.Key).Count(&count).Error
	if count > 0 {
		_ = tx.Table("adty_notice_templates").Where(keyCol+" = ?", tpl.Key).Updates(row).Error
	} else {
		_ = tx.Table("adty_notice_templates").Create(row).Error
	}
}

// SaveAdtyNoticeTemplate allows Root to create or update a fixed bilingual system notice template.
func SaveAdtyNoticeTemplate(actorID, actorRole int, key, titleZh, contentZh, titleEn, contentEn string) (*AdtyBilingualNoticeTemplate, error) {
	saved, _, err := SaveAdtyNoticeTemplateWithAudit(actorID, actorRole, key, titleZh, contentZh, titleEn, contentEn)
	return saved, err
}

func SaveAdtyNoticeTemplateWithAudit(actorID, actorRole int, key, titleZh, contentZh, titleEn, contentEn string) (*AdtyBilingualNoticeTemplate, string, error) {
	if actorID <= 0 || actorRole != common.RoleRootUser {
		return nil, "", common.NewMessage("No permission to perform this action")
	}
	key = strings.TrimSpace(key)
	titleZh, contentZh = strings.TrimSpace(titleZh), strings.TrimSpace(contentZh)
	titleEn, contentEn = strings.TrimSpace(titleEn), strings.TrimSpace(contentEn)
	if key == "" || len(key) > 128 || titleZh == "" || contentZh == "" || titleEn == "" || contentEn == "" ||
		utf8.RuneCountInString(titleZh) > 255 || utf8.RuneCountInString(titleEn) > 255 {
		return nil, "", common.NewMessage("Invalid parameters")
	}
	if err := ValidateAdtyNoticeTemplatePlaceholders(titleZh, contentZh, titleEn, contentEn); err != nil {
		return nil, "", err
	}
	if err := ensureAdtyNoticeTemplateTable(model.DB); err != nil {
		return nil, "", err
	}
	now := time.Now().Unix()
	var saved *AdtyBilingualNoticeTemplate
	var beforeSnap any
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var existing AdtyBilingualNoticeTemplate
		found := tx.Where("key = ?", key).Limit(1).Find(&existing)
		if found.Error != nil {
			return found.Error
		}
		category := "system"
		if def, ok := defaultAdtyBilingualNoticeTemplates[key]; ok && def.Category != "" {
			category = def.Category
		}
		if found.RowsAffected == 0 {
			row := AdtyBilingualNoticeTemplate{
				Key:       key,
				Category:  category,
				TitleZh:   titleZh,
				ContentZh: contentZh,
				TitleEn:   titleEn,
				ContentEn: contentEn,
				Revision:  1,
				UpdatedBy: actorID,
				UpdatedAt: now,
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			syncLegacyAdtyNoticeTemplatesTable(tx, row)
			saved = &row
		} else {
			beforeSnap = existing
			existing.TitleZh = titleZh
			existing.ContentZh = contentZh
			existing.TitleEn = titleEn
			existing.ContentEn = contentEn
			existing.Revision++
			existing.UpdatedBy = actorID
			existing.UpdatedAt = now
			if err := tx.Save(&existing).Error; err != nil {
				return err
			}
			syncLegacyAdtyNoticeTemplatesTable(tx, existing)
			saved = &existing
		}
		var auditErr error
		auditEvent, auditErr = appendAdtyOfficialAuditTx(tx, actorID, 0, "notice_template", saved.Key, "adty.notice_template.save", "", "", beforeSnap, saved, model.AuditResultSuccess)
		return auditErr
	})
	if err != nil {
		return nil, "", err
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return saved, eventID, nil
}

// GetAdtyNoticeTemplate returns a customized or default bilingual system notice template by key.
// The second return value is true when a Root-customized row exists in the database.
func GetAdtyNoticeTemplate(key string) (*AdtyBilingualNoticeTemplate, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, false, common.NewMessage("Invalid parameters")
	}
	if model.DB != nil && model.DB.Migrator().HasTable(&AdtyBilingualNoticeTemplate{}) {
		var row AdtyBilingualNoticeTemplate
		res := model.DB.Where("key = ?", key).Limit(1).Find(&row)
		if res.Error != nil {
			return nil, false, res.Error
		}
		if res.RowsAffected > 0 {
			return &row, true, nil
		}
	}
	if def, ok := defaultAdtyBilingualNoticeTemplates[key]; ok {
		copyDef := def
		return &copyDef, false, nil
	}
	return nil, false, nil
}

// ListAdtyNoticeTemplates lists all fixed bilingual system templates with any Root overrides merged.
func ListAdtyNoticeTemplates() ([]AdtyBilingualNoticeTemplate, error) {
	merged := make(map[string]AdtyBilingualNoticeTemplate, len(defaultAdtyBilingualNoticeTemplates))
	for k, v := range defaultAdtyBilingualNoticeTemplates {
		merged[k] = v
	}
	if model.DB != nil && model.DB.Migrator().HasTable(&AdtyBilingualNoticeTemplate{}) {
		var rows []AdtyBilingualNoticeTemplate
		if err := model.DB.Order("key ASC").Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			merged[r.Key] = r
		}
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]AdtyBilingualNoticeTemplate, 0, len(keys))
	for _, k := range keys {
		out = append(out, merged[k])
	}
	return out, nil
}

func applyAdtyNoticeVars(text string, vars map[string]string, args ...any) string {
	out := text
	if strings.Contains(out, "{{SystemName}}") {
		if sysName := common.GetSystemName(); sysName != "" {
			out = strings.ReplaceAll(out, "{{SystemName}}", sysName)
		}
	}
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	for i, arg := range args {
		val := fmt.Sprintf("%v", arg)
		idx := strconv.Itoa(i + 1)
		out = strings.ReplaceAll(out, "{{"+idx+"}}", val)
		out = strings.ReplaceAll(out, "{"+idx+"}", val)
		out = strings.Replace(out, "{{value}}", val, 1)
	}
	return out
}

func isChineseNoticeLang(lang string) bool {
	norm := strings.ToLower(strings.TrimSpace(lang))
	return strings.HasPrefix(norm, "zh")
}

// RenderAdtyNoticeTemplate renders one system notice template in the requested language ("zh-CN" or "en").
func RenderAdtyNoticeTemplate(key, lang string, vars map[string]string, args ...any) (string, string, error) {
	tpl, _, err := GetAdtyNoticeTemplate(key)
	if err != nil {
		return "", "", err
	}
	if tpl == nil {
		return "", "", common.NewMessage("Notice template not found")
	}
	if isChineseNoticeLang(lang) {
		return applyAdtyNoticeVars(tpl.TitleZh, vars, args...), applyAdtyNoticeVars(tpl.ContentZh, vars, args...), nil
	}
	return applyAdtyNoticeVars(tpl.TitleEn, vars, args...), applyAdtyNoticeVars(tpl.ContentEn, vars, args...), nil
}

// RenderAdtyBilingualNotice renders a combined Chinese + English notice from the template.
func RenderAdtyBilingualNotice(key string, vars map[string]string, args ...any) (string, string, error) {
	zhTitle, zhBody, err := RenderAdtyNoticeTemplate(key, "zh-CN", vars, args...)
	if err != nil {
		return "", "", err
	}
	enTitle, enBody, err := RenderAdtyNoticeTemplate(key, "en", vars, args...)
	if err != nil {
		return "", "", err
	}
	title := zhTitle
	if enTitle != "" && enTitle != zhTitle {
		title = zhTitle + " / " + enTitle
	}
	body := zhBody
	if enBody != "" && enBody != zhBody {
		body = zhBody + "\n\n" + enBody
	}
	return title, body, nil
}

func normalizeNoticeTemplateKeyForNotifyType(notifyType string) string {
	notifyType = strings.TrimSpace(notifyType)
	if strings.HasPrefix(notifyType, "channel_status:") {
		parts := strings.Split(notifyType, ":")
		if len(parts) >= 3 && parts[2] == strconv.Itoa(common.ChannelStatusEnabled) {
			return "channel_enabled"
		}
		return "channel_disabled"
	}
	// formatNotifyType writes channel_update_{id}_{status}. A saved channel_disabled
	// or channel_enabled row is the production template for that committed transition.
	if strings.HasPrefix(notifyType, "channel_update_") {
		parts := strings.Split(notifyType, "_")
		if len(parts) >= 4 {
			if parts[len(parts)-1] == strconv.Itoa(common.ChannelStatusEnabled) {
				return "channel_enabled"
			}
			return "channel_disabled"
		}
	}
	return notifyType
}

// SavedAdtyBilingualNoticeText returns the saved Chinese and English lines for key.
// In-memory defaults are not production copy. An empty or missing saved row returns false.
func SavedAdtyBilingualNoticeText(key string) (string, bool) {
	tpl, customized, err := GetAdtyNoticeTemplate(key)
	if err != nil || !customized || tpl == nil {
		return "", false
	}
	zh := strings.TrimSpace(tpl.ContentZh)
	if zh == "" {
		zh = strings.TrimSpace(tpl.TitleZh)
	}
	en := strings.TrimSpace(tpl.ContentEn)
	if en == "" {
		en = strings.TrimSpace(tpl.TitleEn)
	}
	switch {
	case zh != "" && en != "":
		return zh + "\n\n" + en, true
	case zh != "":
		return zh, true
	case en != "":
		return en, true
	default:
		return "", false
	}
}

func hasUnfilledNoticePlaceholder(rawTexts []string, valueCount int) bool {
	for _, raw := range rawTexts {
		valueUses := 0
		for _, token := range adtyNoticePlaceholderPattern.FindAllString(raw, -1) {
			if token == "{{SystemName}}" {
				if common.GetSystemName() != "" {
					continue
				}
				return true
			}
			if token == "{{value}}" {
				valueUses++
				if valueUses <= valueCount {
					continue
				}
				return true
			}
			name := strings.TrimSuffix(strings.TrimPrefix(token, "{{"), "}}")
			if name == token {
				name = strings.TrimSuffix(strings.TrimPrefix(token, "{"), "}")
			}
			if idx, err := strconv.Atoi(name); err == nil && idx >= 1 && idx <= valueCount {
				continue
			}
			return true
		}
	}
	return false
}

// ResolveAdtyNoticeTemplateForNotify checks if Root has configured a custom bilingual
// template for notifyType in the database, and if so renders it in lang; otherwise
// returns defaultSubject and defaultContent unchanged.
func ResolveAdtyNoticeTemplateForNotify(notifyType, lang, defaultSubject, defaultContent string, values []any) (string, string) {
	if model.DB == nil || !model.DB.Migrator().HasTable(&AdtyBilingualNoticeTemplate{}) {
		return defaultSubject, defaultContent
	}
	for _, candidate := range []string{strings.TrimSpace(notifyType), normalizeNoticeTemplateKeyForNotifyType(notifyType)} {
		if candidate == "" {
			continue
		}
		tpl, customized, err := GetAdtyNoticeTemplate(candidate)
		if err != nil || !customized || tpl == nil {
			continue
		}
		rawTitle, rawContent := tpl.TitleEn, tpl.ContentEn
		if isChineseNoticeLang(lang) {
			rawTitle, rawContent = tpl.TitleZh, tpl.ContentZh
		}
		// A saved row with unfilled placeholders must not replace already rendered copy (R4-048).
		if hasUnfilledNoticePlaceholder([]string{rawTitle, rawContent}, len(values)) {
			continue
		}
		subject, content := applyAdtyNoticeVars(rawTitle, nil, values...), applyAdtyNoticeVars(rawContent, nil, values...)
		return subject, content
	}
	return defaultSubject, defaultContent
}
