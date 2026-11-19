package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

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

// PersonalOrder is one row a customer can search.
type PersonalOrder struct {
	TradeNo string
	Kind    string
	Status  string
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

// SiteTierRule is one assessment band.
type SiteTierRule struct {
	Code     string
	MinUsers int
	MinTopup decimal.Decimal
}

// UpdateAdtyTeamProfile lets the leader rename the team. Discount changes stay on the create path.
func UpdateAdtyTeamProfile(leaderId int, teamId int64, name string) error {
	name = strings.TrimSpace(name)
	if teamId <= 0 || name == "" || len(name) > 64 {
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

// SubmitAdtyStaffBalanceRequest records an employee request. It does not change the customer balance.
// The idempotency key is required so a retried submit cannot create a second booking.
func SubmitAdtyStaffBalanceRequest(staffUserID, customerUserID int, direction string, amount decimal.Decimal, idempotencyKey string) (*model.AdtyStaffBalanceRequest, error) {
	if staffUserID <= 0 || customerUserID <= 0 || !amount.IsPositive() {
		return nil, common.NewMessage("Invalid parameters")
	}
	if direction != model.AdtyStaffBalanceIncrease && direction != model.AdtyStaffBalanceDecrease {
		return nil, common.NewMessage("Invalid parameters")
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || len(idempotencyKey) > 64 {
		return nil, common.NewMessage("Idempotency key is required")
	}
	var result *model.AdtyStaffBalanceRequest
	var officialEntry *model.AuditLog
	var deliverIntent bool
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var staff model.AdtySiteStaff
		if err := tx.Where("user_id = ? AND status = ?", staffUserID, "enabled").First(&staff).Error; err != nil {
			return common.NewMessage("No permission to perform this action")
		}
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).First(&site, staff.SiteId).Error; err != nil {
			return err
		}
		if site.OwnerUserId != staff.OwnerUserId || site.Status != model.AdtySiteStatusActive {
			return common.NewMessage("No permission to perform this action")
		}
		var customer model.User
		if err := model.AdtyLockForUpdate(tx).First(&customer, customerUserID).Error; err != nil {
			return err
		}
		if customer.SiteId != staff.SiteId || customer.StaffSiteId != 0 || customer.Status != common.UserStatusEnabled {
			return common.NewMessage("No permission to perform this action")
		}
		if existing, err := findAdtyStaffBalanceRequestByIdemKey(tx, idempotencyKey); err != nil {
			return err
		} else if existing != nil {
			if !sameAdtyStaffBalanceRequest(existing, staff.SiteId, staffUserID, customerUserID, direction, amount) {
				return model.ErrAdtyIdempotencyConflict
			}
			result = existing
			return nil
		}
		row := &model.AdtyStaffBalanceRequest{
			SiteId: staff.SiteId, StaffUserId: staffUserID, CustomerUserId: customerUserID,
			Direction: direction, AmountUSD: amount, Status: model.AdtyStaffBalancePending,
			IdempotencyKey: &idempotencyKey,
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		var actor model.User
		if err := tx.Select("id", "username", "role").First(&actor, staffUserID).Error; err != nil {
			return err
		}
		snapshot := map[string]any{
			"id":              row.Id,
			"request_id":      row.Id,
			"staff_id":        staffUserID,
			"customer_id":     customerUserID,
			"site_id":         row.SiteId,
			"direction":       direction,
			"amount_usd":      amount.String(),
			"status":          row.Status,
			"idempotency_key": idempotencyKey,
			"customer_quota":  customer.Quota,
		}
		if err := tx.Create(&model.AdtySiteAudit{
			SiteId: row.SiteId, Action: "staff.balance_request", OperatorId: staffUserID,
			NewValue: common.GetJsonString(snapshot), Reason: "staff balance request",
		}).Error; err != nil {
			return err
		}
		entry, ok, err := appendStaffBalanceOfficialAuditTx(tx, actor.Id, actor.Username, actor.Role, row.SiteId,
			strconv.FormatInt(row.Id, 10), "adty.staff.balance_request",
			fmt.Sprintf("staff_balance:%d", row.Id), nil, snapshot, "staff balance request")
		if err != nil {
			return err
		}
		officialEntry = entry
		deliverIntent = ok
		result = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	if officialEntry != nil {
		deliverStaffBalanceOfficialAudit(officialEntry, deliverIntent)
	}
	return result, nil
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

func findAdtyStaffBalanceRequestByIdemKey(db *gorm.DB, key string) (*model.AdtyStaffBalanceRequest, error) {
	var rows []model.AdtyStaffBalanceRequest
	if err := db.Where("idempotency_key = ?", key).Limit(1).Find(&rows).Error; err != nil {
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
}

func applyApprovedStaffBalanceTx(tx *gorm.DB, row model.AdtyStaffBalanceRequest, reviewerID int) (staffBalanceApplyResult, error) {
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
		if err := model.ConsumeAdtyFundBatchesQuietTx(tx, row.CustomerUserId, debited, before); err != nil {
			return staffBalanceApplyResult{}, err
		}
	} else {
		fundingSource, err = adtyFundStaffBalanceIncreaseTx(tx, row, delta)
		if err != nil {
			return staffBalanceApplyResult{}, err
		}
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
		RefId: strconv.FormatInt(row.Id, 10), Reason: "staff balance request",
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
	}, nil
}

// ReviewAdtyStaffBalanceRequest approves or rejects a pending request.
// Site owners review their own site. Root reviews any site. Approval changes the customer quota once
// through the official credit or debit path, writing the ledger and fund batches.
// adtyStaffBalanceFundingOrder 是「员工加款从哪儿出钱」的顺序：
// 只允许从站点自有盈余（已结算收益）或可用保证金（受 10% 底线与 90% 周期上限约束）出资；
// 运营钱包是现有客户本金的 1:1 托管准备金，绝不可当成站长可支配盈余花费。
var adtyStaffBalanceFundingOrder = []string{
	model.AdtySiteWalletEarnings,
	model.AdtySiteWalletMargin,
}

// adtyFundStaffBalanceIncreaseTx 从收益或保证金扣出站方出资，并同步向运营钱包入账等额本金背书。
func adtyFundStaffBalanceIncreaseTx(tx *gorm.DB, row model.AdtyStaffBalanceRequest, delta int64) (string, error) {
	if delta <= 0 {
		return "", common.NewMessage("Invalid parameters")
	}
	if row.SiteId <= 0 {
		return "", common.NewMessage("This request has no site to fund it")
	}
	var customer model.User
	if err := tx.Select("id", "site_id", "staff_site_id").First(&customer, row.CustomerUserId).Error; err != nil {
		return "", err
	}
	// 客户必须还在这个站点下：站点换了之后，旧站点不该继续为他的加款出钱。
	if customer.SiteId != row.SiteId || customer.StaffSiteId != 0 {
		return "", common.NewMessage("No permission to perform this action")
	}
	amountUSD := decimal.NewFromInt(delta).Div(common.QuotaPerUnitDecimal())
	key := adtyStaffBalanceLedgerKey(row)
	var fundedKind string
	for _, kind := range adtyStaffBalanceFundingOrder {
		_, err := applyAdtySiteWalletChangeTx(tx, row.SiteId, kind, amountUSD.Neg(), AdtySiteWalletChange{
			Type: "staff_balance", RefType: "staff_balance", RefID: strconv.FormatInt(row.Id, 10),
			Reason:         "staff balance increase for customer " + strconv.Itoa(row.CustomerUserId),
			IdempotencyKey: fmt.Sprintf("%s:fund:%s", key, kind),
		})
		if err == nil {
			fundedKind = kind
			break
		}
		if errors.Is(err, ErrAdtySiteWalletInsufficient) {
			continue
		}
		return "", err
	}
	if fundedKind == "" {
		return "", ErrAdtySiteWalletInsufficient
	}
	if err := adtyMoveOperatingPrincipalTx(tx, row.SiteId, delta, "principal_credit", "staff_balance",
		strconv.FormatInt(row.Id, 10), key+":operating", false); err != nil {
		return "", err
	}
	return fundedKind, nil
}

func ReviewAdtyStaffBalanceRequest(reviewerID int, reviewerRole string, requestID int64, approve bool) error {
	var changedUserID int
	var officialEntry *model.AuditLog
	var deliverIntent bool
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
			"reviewed_at":    reviewedAt,
		}
		if err := tx.Create(&model.AdtySiteAudit{
			SiteId: row.SiteId, Action: "staff.balance_review", OperatorId: reviewerID,
			OldValue: common.GetJsonString(beforeSnapshot),
			NewValue: common.GetJsonString(afterSnapshot),
			Reason:   "staff balance " + status,
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
			strconv.FormatInt(row.Id, 10), officialAction, approvalRef, beforeSnapshot, afterSnapshot, "staff balance "+status)
		if err != nil {
			return err
		}
		officialEntry = entry
		deliverIntent = ok
		return nil
	})
	if err != nil {
		return err
	}
	if officialEntry != nil {
		deliverStaffBalanceOfficialAudit(officialEntry, deliverIntent)
	}
	if changedUserID > 0 {
		model.AdtySyncUserQuotaCache(changedUserID, "staff-balance")
	}
	return nil
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
	if source == "" || locale == "" || target == "" {
		return common.NewMessage("Invalid parameters")
	}
	row := model.AdtyGlossaryTerm{Source: source, Locale: locale, Target: target}
	return model.DB.Where(model.AdtyGlossaryTerm{Source: source, Locale: locale}).Assign(row).FirstOrCreate(&row).Error
}

// CompleteAdtyTodo marks one severe inbox item done for that user.
func CompleteAdtyTodo(userID int, key string) error {
	return HideAdtyNotificationForUser(userID, "todo:"+strings.TrimSpace(key))
}

// QuotaToUSD converts internal quota to USD at the fixed integer scale.
func QuotaToUSD(quota int64) decimal.Decimal {
	return decimal.NewFromInt(quota).Div(common.QuotaPerUnitDecimal())
}

// BindSiteInviteFromIP records one direct site invite and keeps the client IP hash.
// It serializes on locked inviter/invitee user rows with first-payment completion
// so a concurrent paid order cannot leave a post-payment binding.
func BindSiteInviteFromIP(siteID, inviterID, inviteeID int, ipHash string) error {
	if siteID <= 0 || inviterID <= 0 || inviteeID <= 0 || inviterID == inviteeID {
		return common.NewMessage("Invalid parameters")
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
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
		if inviter.SiteId != siteID || invitee.SiteId != siteID {
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
		var paid int64
		if err := tx.Model(&model.TopUp{}).Where("user_id = ? AND status = ?", inviteeID, common.TopUpStatusSuccess).Count(&paid).Error; err != nil {
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
		if err := refuseSiteInviteBatchTx(tx, siteID, inviterID, ipHash); err != nil {
			return err
		}
		return tx.Create(&model.AdtySiteInvite{
			SiteId: siteID, InviterId: inviterID, InviteeId: inviteeID, Rate: rate.Rate, IpHash: strings.TrimSpace(ipHash),
		}).Error
	})
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
		Where("inviter_id = ? AND ip_hash = ? AND created_at >= ?", inviterID, ipHash, since).
		Count(&count).Error; err != nil {
		return err
	}
	if count+1 < threshold {
		return nil
	}
	ref := strconv.Itoa(siteID)
	note := "inviter " + strconv.Itoa(inviterID)
	var existing int64
	if err := tx.Model(&model.AdtyAnomalyDisposition{}).
		Where("kind = ? AND ref = ? AND status = ? AND note = ?", "batch_register", ref, "open", note).
		Count(&existing).Error; err != nil {
		return err
	}
	if existing == 0 {
		if err := tx.Create(&model.AdtyAnomalyDisposition{Kind: "batch_register", Ref: ref, Status: "open", Note: note}).Error; err != nil {
			return err
		}
	}
	return common.NewMessage("This invite needs review")
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

// RequestPriceSnapshot freezes the accepted model and group price.
type RequestPriceSnapshot struct {
	Model    string
	Group    string
	UnitUSD  decimal.Decimal
	FrozenAt int64
}

// HideAdtyNotificationForUser removes one inbox item for that user only.
func HideAdtyNotificationForUser(userID int, key string) error {
	return hideAdtyNotificationForUser(nil, userID, key)
}

// HideAdtyNotificationForUserFromRequest is the request-scoped hide path so
// the official audit can use the site already resolved for this request.
func HideAdtyNotificationForUserFromRequest(c *gin.Context, userID int, key string) error {
	return hideAdtyNotificationForUser(c, userID, key)
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

func writeAdtyNotificationHideAudit(c *gin.Context, userID int, key string) {
	rec := model.OfficialAuditWrite{
		UserId:     userID,
		SiteID:     model.OfficialAuditSiteIDPointer(0),
		ObjectType: "notification",
		ObjectID:   key,
		Action:     "adty.notification.hide",
		Category:   model.AuditCategoryOperation,
		Content:    "adty.notification.hide",
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
	rec.SiteID = model.OfficialAuditSiteIDPointer(officialNotificationHideSiteID(c, actor))
	if err := model.WriteOfficialAuditLog(c, rec); err != nil {
		common.SysLog(common.LogText("official notification hide audit write failed: %s", err.Error()))
	}
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
