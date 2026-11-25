package service

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// AdtySiteInviteWithdrawalInput is one site-customer commission withdrawal request.
type AdtySiteInviteWithdrawalInput struct {
	UserID          int
	SiteID          int
	Amount          decimal.Decimal
	Method          string
	Network         string
	AccountNo       string
	AccountName     string
	QRImageURL      string
	ExpectedQuoteID string
	RequireQuote    bool
	ExpectedCNYRate decimal.Decimal
	IdempotencyKey  string
}

// CreateAdtySiteInviteWithdrawal freezes available site invite commission.
// Balance completes immediately into quota. External payout methods are Alipay or USDT
// only (DEC-025); cash methods stay pending for that site's owner.
func CreateAdtySiteInviteWithdrawal(input AdtySiteInviteWithdrawalInput) (*model.AdtySiteInviteWithdrawal, error) {
	method := strings.TrimSpace(input.Method)
	scale := int32(2)
	switch method {
	case "usdt":
		scale = 6
	case "balance":
		scale = 6
	}
	if !input.Amount.Equal(input.Amount.Truncate(scale)) {
		return nil, common.NewMessage("Invalid parameters")
	}
	amount := input.Amount.Truncate(scale)
	key := strings.TrimSpace(input.IdempotencyKey)
	if input.UserID <= 0 || input.SiteID < 0 || !amount.IsPositive() || key == "" || len(key) > 128 {
		return nil, common.NewMessage("Invalid parameters")
	}
	if isAdtySiteInviteWeChatMethod(method) {
		return nil, common.NewMessage("Unsupported withdrawal method")
	}
	row := &model.AdtySiteInviteWithdrawal{
		SiteId: input.SiteID, UserId: input.UserID, RequestedUSD: amount, AmountUSD: amount, NetUSD: amount,
		Method: method, Status: model.AdtySiteInviteWithdrawPending, IdempotencyKey: key,
	}
	var credit int
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := model.AdtyLockForUpdate(tx).Select("id", "site_id").First(&user, input.UserID).Error; err != nil {
			return err
		}
		// R7-29: Scope idempotency keys per (site, user) so callers cannot collide
		// with or probe another user's withdrawal keys, while keeping compatibility
		// with legacy raw keys belonging to the same user.
		if input.SiteID == 0 {
			input.SiteID = user.SiteId
		}
		var existing model.AdtySiteInviteWithdrawal
		var lookup *gorm.DB
		if input.SiteID > 0 {
			scopedKey := adtySiteInviteWithdrawScopedKey(input.SiteID, input.UserID, key)
			lookup = tx.Where("user_id = ? AND site_id = ? AND (idempotency_key = ? OR idempotency_key = ?)", input.UserID, input.SiteID, scopedKey, key).Limit(1).Find(&existing)
			row.IdempotencyKey = scopedKey
		} else {
			lookup = tx.Where("user_id = ? AND idempotency_key = ?", input.UserID, key).Limit(1).Find(&existing)
		}
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected > 0 {
			if input.SiteID == 0 && existing.UserId == input.UserID {
				input.SiteID = existing.SiteId
			}
			requested := existing.RequestedUSD
			if !requested.IsPositive() {
				requested = existing.AmountUSD
			}
			if existing.SiteId != input.SiteID || existing.UserId != input.UserID || existing.Method != method || !requested.Equal(amount) || (input.ExpectedQuoteID != "" && existing.QuoteID != input.ExpectedQuoteID) || !sameSiteInviteDestination(tx, &existing, input) {
				return model.ErrAdtyIdempotencyConflict
			}
			row = &existing
			return nil
		}
		row.SiteId = input.SiteID
		if input.SiteID > 0 {
			row.IdempotencyKey = adtySiteInviteWithdrawScopedKey(input.SiteID, input.UserID, key)
		}
		if err := authorizeCommissionSource(tx, input.UserID, user.SiteId, input.SiteID); err != nil {
			return err
		}
		if blocked, blockErr := isSiteReferralAffiliateWithdrawalBlockedTx(tx, input.SiteID, input.UserID); blockErr != nil {
			return blockErr
		} else if blocked {
			return common.NewMessage("Withdrawals are paused for this account")
		}
		if err := model.RejectRestrictedAdtyFundWrite(tx, model.AdtyFundWrite{SiteID: input.SiteID, AccountID: input.UserID}); err != nil {
			return err
		}
		var site model.AdtySite
		if err := tx.First(&site, input.SiteID).Error; err != nil {
			return err
		}
		row.CreatedAt = time.Now().Unix()
		row.CancelDeadline = siteInviteCancelDeadline(row.CreatedAt, site.MarketingPolicy.CancelWindowMinutes)
		switch method {
		case "balance":
			exact := amount.Mul(common.QuotaPerUnitDecimal())
			if !exact.Equal(exact.Floor()) {
				return common.NewMessage("Invalid parameters")
			}
			quota, err := common.WalletQuotaFromDecimalStrict(exact)
			if err != nil || quota <= 0 {
				return common.NewMessage("Invalid parameters")
			}
			row.Status = model.AdtySiteInviteWithdrawPaid
			row.PaidAt = time.Now().Unix()
		case "usdt", "alipay":
			quote, err := siteInviteQuote(&site, amount, method)
			if err != nil {
				return err
			}
			if (input.RequireQuote || input.ExpectedQuoteID != "") && input.ExpectedQuoteID != quote.QuoteID {
				return &SiteInviteQuoteChanged{Quote: quote}
			}
			row.FeeUSD, row.NetUSD = quote.FeeUSD, amount.Sub(quote.FeeUSD)
			row.QuoteID, row.Rate = quote.QuoteID, quote.Rate.String()
			row.PayoutAmount, row.PayoutCurrency = quote.PayoutAmount, quote.PayoutCurrency
			if method == "usdt" {
				network := strings.ToUpper(strings.TrimSpace(input.Network))
				_, usdtNetworks := usableSiteInviteWithdrawConfig(site.MarketingPolicy)
				if !slices.Contains(usdtNetworks, network) {
					return common.NewMessage("This USDT network is not enabled")
				}
				if !ValidateUSDTAddress(network, input.AccountNo) {
					return common.NewMessage("The address does not match the {{network}} network", map[string]any{"network": network})
				}
				row.Network, row.AccountNo = network, strings.TrimSpace(input.AccountNo)
			} else {
				trimmedName := strings.TrimSpace(input.AccountName)
				trimmedNo := strings.TrimSpace(input.AccountNo)
				if trimmedName == "" || trimmedNo == "" {
					return common.NewMessage("Enter the payee real name and Alipay account")
				}
				if utf8.RuneCountInString(trimmedName) > 64 || utf8.RuneCountInString(trimmedNo) > 128 {
					return common.NewMessage("Invalid parameters")
				}
				if input.ExpectedQuoteID == "" && !quote.Rate.Equal(input.ExpectedCNYRate) {
					return &RateChangedError{Change: ReferralRateChange{Rate: quote.Rate.String(), CNYAmount: quote.PayoutAmount}}
				}
				row.AccountName = trimmedName
				row.AccountNo = trimmedNo
				if trimmedQR := strings.TrimSpace(input.QRImageURL); trimmedQR != "" {
					if !validReferralQR(trimmedQR) {
						return common.NewMessage("Invalid payment QR code")
					}
					canonicalQR, ok := IsOwnedReferralAssetTx(tx, input.UserID, model.ReferralAssetPurposeWithdrawalQR, trimmedQR)
					if !ok {
						return common.NewMessage("Invalid payment QR code")
					}
					row.QRImageURL = canonicalQR
				}
			}
		default:
			return common.NewMessage("Unsupported withdrawal method")
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		// R1-045 global lock order: lock User before AdtySiteInviteCommission when crediting balance.
		if method == "balance" {
			var lockUser model.User
			if err := model.AdtyLockForUpdate(tx).Select("id").First(&lockUser, input.UserID).Error; err != nil {
				return err
			}
		}
		if err := allocateSiteInviteWithdrawalTx(tx, row, method == "balance"); err != nil {
			return err
		}
		if method == "balance" {
			exact := amount.Mul(common.QuotaPerUnitDecimal())
			quota, err := common.WalletQuotaFromDecimalStrict(exact)
			if err != nil {
				return err
			}
			credit = quota
			ref := "site-invite-wd:" + strconv.FormatInt(row.Id, 10)
			before, err := model.AdtyCreditUserQuotaTx(tx, input.UserID, int64(quota))
			if err != nil {
				return err
			}
			if err := model.InsertAdtyLedgerTx(tx, &model.AdtyBalanceLedger{
				UserId: input.UserID, DeltaQuota: int64(quota), BalanceBefore: before, BalanceAfter: before + int64(quota),
				Type: model.AdtyLedgerTypeCommissionTransfer, RefType: "site_invite_withdrawal", RefId: strconv.FormatInt(row.Id, 10),
				Reason: "site invite commission to balance", IdempotencyKey: &ref,
			}); err != nil {
				return err
			}
			// R2-024: Only credit operating wallet if the site is not closed.
			siteClosed, err := adtySiteIsClosedTx(tx, row.SiteId)
			if err != nil {
				return err
			}
			if row.SiteId > 0 && !siteClosed {
				if err := adtyMoveOperatingPrincipalTx(tx, row.SiteId, int64(quota), "principal_credit", "site_invite_withdrawal", strconv.FormatInt(row.Id, 10), "operating:"+ref, false); err != nil {
					return err
				}
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if credit > 0 {
		model.AdtySyncCreditUserQuotaCache(input.UserID, int64(credit), "site invite commission to balance")
	}
	return row, nil
}

func authorizeCommissionSource(tx *gorm.DB, userID, currentSite, source int) error {
	if source > 0 && currentSite == source {
		return nil
	}
	var count int64
	err := tx.Model(&model.AdtySiteInviteCommission{}).Where("inviter_id = ? AND site_id = ? AND site_id IN (?)", userID, source, tx.Model(&model.AdtySite{}).Select("id").Where("status = ?", model.AdtySiteStatusClosed)).Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return common.NewMessage("No permission to perform this action")
	}
	return nil
}
func sameSiteInviteDestination(tx *gorm.DB, row *model.AdtySiteInviteWithdrawal, input AdtySiteInviteWithdrawalInput) bool {
	switch row.Method {
	case "usdt":
		return row.Network == strings.ToUpper(strings.TrimSpace(input.Network)) && row.AccountNo == strings.TrimSpace(input.AccountNo)
	case "alipay":
		qr := ""
		if trimmedQR := strings.TrimSpace(input.QRImageURL); trimmedQR != "" {
			var ok bool
			qr, ok = IsOwnedReferralAssetTx(tx, input.UserID, model.ReferralAssetPurposeWithdrawalQR, trimmedQR)
			if !ok {
				return false
			}
		}
		rate, err := decimal.NewFromString(row.Rate)
		return row.QRImageURL == qr &&
			row.AccountName == strings.TrimSpace(input.AccountName) &&
			row.AccountNo == strings.TrimSpace(input.AccountNo) &&
			err == nil &&
			(input.ExpectedQuoteID != "" || rate.Equal(input.ExpectedCNYRate))
	default:
		return row.Method == "balance"
	}
}

func allocateSiteInviteWithdrawalTx(tx *gorm.DB, withdrawal *model.AdtySiteInviteWithdrawal, toBalance bool) error {
	siteClosed := false
	if toBalance {
		var err error
		siteClosed, err = adtySiteIsClosedTx(tx, withdrawal.SiteId)
		if err != nil {
			return err
		}
	}
	var rows []model.AdtySiteInviteCommission
	if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND inviter_id = ?", withdrawal.SiteId, withdrawal.UserId).Order("id").Find(&rows).Error; err != nil {
		return err
	}
	left := withdrawal.AmountUSD
	now := time.Now()
	for i := range rows {
		if !left.IsPositive() {
			break
		}
		if !SiteInviteCommissionWithdrawable(&rows[i], now) {
			continue
		}
		free := siteInviteCommissionFree(&rows[i])
		take := free
		if take.GreaterThan(left) {
			take = left
		}
		if !take.IsPositive() {
			continue
		}
		status := model.AdtySiteInviteWithdrawItemFrozen
		field := "frozen_usd"
		if toBalance {
			status = model.AdtySiteInviteWithdrawItemPaid
			field = "converted_usd"
		}
		if err := tx.Create(&model.AdtySiteInviteWithdrawalItem{
			WithdrawalId: withdrawal.Id, CommissionId: rows[i].Id, AmountUSD: take, Status: status,
		}).Error; err != nil {
			return err
		}
		if toBalance {
			// Cumulative integer allocation preserves the exact transferred quota.
			before := withdrawal.AmountUSD.Sub(left).Mul(common.QuotaPerUnitDecimal()).Floor()
			after := withdrawal.AmountUSD.Sub(left).Add(take).Mul(common.QuotaPerUnitDecimal()).Floor()
			quota, err := common.WalletQuotaFromDecimalStrict(after.Sub(before))
			if err != nil {
				return err
			}
			if quota > 0 {
				source := fmt.Sprintf("site-comm-wd:%d:%d", rows[i].Id, withdrawal.Id)
				batchSiteID := withdrawal.SiteId
				if siteClosed {
					batchSiteID = 0
				}
				if err := model.CreateAdtyFundBatchesWithSiteTx(tx, withdrawal.UserId, model.AdtyFundBatchSiteID(batchSiteID), source, int64(quota), 0); err != nil {
					return err
				}
			}
			if err := consumeSiteCommissionHoldTx(tx, &rows[i], take, false, "wd:"+strconv.FormatInt(withdrawal.Id, 10)); err != nil {
				return err
			}
		}
		if err := tx.Model(&rows[i]).Update(field, gorm.Expr(field+" + ?", take)).Error; err != nil {
			return err
		}
		left = left.Sub(take)
	}
	if left.IsPositive() {
		return common.NewMessage("Not enough withdrawable commission")
	}
	return nil
}

func siteInviteCommissionFree(row *model.AdtySiteInviteCommission) decimal.Decimal {
	if row == nil {
		return decimal.Zero
	}
	// The site hold is what can still be assigned. Allocation freeze is only a
	// pending cash request. Clawed converted and withdrawn amounts stay in those
	// fields, so subtracting them again would hide the remaining hold.
	free := row.EarningsHoldUSD.Add(row.MarginHoldUSD).Sub(row.FrozenUSD)
	if free.IsNegative() {
		return decimal.Zero
	}
	return free
}

// ReviewAdtySiteInviteWithdrawal approves or rejects a pending cash withdrawal.
// Only the owner of that withdrawal's site may review it.
func ReviewAdtySiteInviteWithdrawal(ownerID int, withdrawalID int64, approve bool, reason string) error {
	return ReviewAdtySiteInviteWithdrawalWithClientMeta(ownerID, withdrawalID, approve, reason, "", "")
}
func ReviewAdtySiteInviteWithdrawalWithClientMeta(ownerID int, withdrawalID int64, approve bool, reason, ip, ua string) error {
	_, _, err := reviewSiteInviteWithdrawalWithAudit(ownerID, 0, false, withdrawalID, approve, reason, false, "", ip, ua)
	return err
}
func ReviewClosedAdtySiteInviteWithdrawal(ownerID int, withdrawalID int64, approve bool, reason string) error {
	_, _, err := reviewSiteInviteWithdrawalWithAudit(ownerID, 0, false, withdrawalID, approve, reason, true, model.AdtySiteInviteWithdrawPending, "", "")
	return err
}
func ReviewClosedAdtySiteInviteWithdrawalWithAudit(ownerID int, withdrawalID int64, approve bool, reason string) (string, bool, error) {
	return ReviewClosedAdtySiteInviteWithdrawalWithAuditForActor(ownerID, 0, false, withdrawalID, approve, reason)
}
func ReviewClosedAdtySiteInviteWithdrawalWithAuditForActor(ownerID, actorRole int, denyAdminTarget bool, withdrawalID int64, approve bool, reason string) (string, bool, error) {
	return reviewSiteInviteWithdrawalWithAudit(ownerID, actorRole, denyAdminTarget, withdrawalID, approve, reason, true, model.AdtySiteInviteWithdrawPending, "", "")
}
func ConfirmClosedSiteInvitePayoutFailure(actor int, id int64, approve bool, reason string) error {
	_, _, err := ConfirmClosedSiteInvitePayoutFailureWithAudit(actor, id, approve, reason)
	return err
}
func ConfirmClosedSiteInvitePayoutFailureWithAudit(actor int, id int64, approve bool, reason string) (string, bool, error) {
	return ConfirmClosedSiteInvitePayoutFailureWithAuditForActor(actor, 0, false, id, approve, reason)
}
func ConfirmClosedSiteInvitePayoutFailureWithAuditForActor(actor, actorRole int, denyAdminTarget bool, id int64, approve bool, reason string) (string, bool, error) {
	if approve {
		return "", false, common.NewMessage("Invalid parameters")
	}
	return reviewSiteInviteWithdrawalWithAudit(actor, actorRole, denyAdminTarget, id, false, reason, true, model.AdtySiteInviteWithdrawApproved, "", "")
}
// lockSiteInviteWithdrawalCanonicalTx acquires locks in canonical global order
// (AdtySite -> User -> AdtySiteInviteCommission(id ASC) -> AdtySiteInviteWithdrawal)
// after a non-locking pre-read to eliminate AB-BA deadlocks with refund clawbacks (R2-022, R12-003).
func lockSiteInviteWithdrawalCanonicalTx(tx *gorm.DB, withdrawalID int64, ownerID int, platform bool, checkUserID int) (*model.AdtySiteInviteWithdrawal, error) {
	return lockSiteInviteWithdrawalCanonicalForActorTx(tx, withdrawalID, ownerID, 0, false, platform, checkUserID)
}
func lockSiteInviteWithdrawalCanonicalForActorTx(tx *gorm.DB, withdrawalID int64, ownerID, actorRole int, denyAdminTarget, platform bool, checkUserID int) (*model.AdtySiteInviteWithdrawal, error) {
	var stub model.AdtySiteInviteWithdrawal
	if err := tx.Select("id", "site_id", "user_id").First(&stub, withdrawalID).Error; err != nil {
		return nil, err
	}
	if checkUserID > 0 && stub.UserId != checkUserID {
		return nil, gorm.ErrRecordNotFound
	}
	var commIDs []int64
	if err := tx.Model(&model.AdtySiteInviteWithdrawalItem{}).
		Where("withdrawal_id = ?", withdrawalID).
		Distinct("commission_id").
		Order("commission_id ASC").
		Pluck("commission_id", &commIDs).Error; err != nil {
		return nil, err
	}
	if ownerID > 0 {
		if err := requireSiteInviteProcessor(tx, ownerID, stub.SiteId, platform); err != nil {
			return nil, err
		}
	}
	if stub.UserId > 0 {
		// R4-10: Always lock users(stub.UserId) BEFORE AdtySiteInviteCommission to
		// preserve canonical AdtySite -> User -> Commission -> Withdrawal lock order.
		var target model.User
		if err := model.AdtyLockForUpdate(tx).Select("id", "role").First(&target, stub.UserId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if platform && (actorRole > 0 || denyAdminTarget) {
					return nil, err
				}
			} else {
				return nil, err
			}
		} else if platform && (actorRole > 0 || denyAdminTarget) {
			if ownerID > 0 && ownerID == target.Id {
				return nil, model.ErrUserQuotaPermission
			}
			if actorRole > 0 && actorRole != common.RoleRootUser && actorRole <= target.Role {
				return nil, model.ErrUserQuotaPermission
			}
			if denyAdminTarget && target.Role >= common.RoleAdminUser {
				return nil, model.ErrAdminTargetSessionRequired
			}
		}
	}
	if len(commIDs) > 0 {
		var lockedComms []model.AdtySiteInviteCommission
		if err := model.AdtyLockForUpdate(tx).Where("id IN ?", commIDs).Order("id ASC").Find(&lockedComms).Error; err != nil {
			return nil, err
		}
	}
	var row model.AdtySiteInviteWithdrawal
	q := model.AdtyLockForUpdate(tx)
	if checkUserID > 0 {
		q = q.Where("id = ? AND user_id = ?", withdrawalID, checkUserID)
		if err := q.First(&row).Error; err != nil {
			return nil, err
		}
	} else {
		if err := q.First(&row, withdrawalID).Error; err != nil {
			return nil, err
		}
	}
	return &row, nil
}

func reviewSiteInviteWithdrawalWithAudit(ownerID, actorRole int, denyAdminTarget bool, withdrawalID int64, approve bool, reason string, platform bool, requiredStatus, ip, ua string) (string, bool, error) {
	var auditEvent *model.AuditLog
	replayed := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		lockedRow, err := lockSiteInviteWithdrawalCanonicalForActorTx(tx, withdrawalID, ownerID, actorRole, denyAdminTarget, platform, 0)
		if err != nil {
			return err
		}
		row := *lockedRow
		if !approve && row.Status == model.AdtySiteInviteWithdrawRejected && row.FailureAt > 0 && row.FailureBy == ownerID && row.RejectReason == strings.TrimSpace(reason) && requiredStatus != model.AdtySiteInviteWithdrawPending {
			replayed = true
			return nil
		}
		if requiredStatus != "" && row.Status != requiredStatus {
			return common.NewMessage("Only pending withdrawals can be reviewed")
		}
		if row.Method == "balance" || (approve && row.Status != model.AdtySiteInviteWithdrawPending) || (!approve && row.Status != model.AdtySiteInviteWithdrawPending && row.Status != model.AdtySiteInviteWithdrawApproved) {
			return common.NewMessage("Only pending withdrawals can be reviewed")
		}
		if approve && !payableSiteInviteWithdrawal(&row) {
			return common.NewMessage("The amount must be larger than the network fee")
		}
		if !approve && row.Status == model.AdtySiteInviteWithdrawApproved && strings.TrimSpace(reason) == "" {
			return common.NewMessage("Enter the failed payout reference")
		}
		beforeSnap := map[string]any{"id": row.Id, "site_id": row.SiteId, "status": row.Status, "amount_usd": row.AmountUSD.String()}
		if approve {
			if err := model.RejectRestrictedAdtyFundWrite(tx, model.AdtyFundWrite{SiteID: row.SiteId, AccountID: row.UserId}); err != nil {
				return err
			}
			if err := tx.Model(&row).Updates(map[string]any{
				"status": model.AdtySiteInviteWithdrawApproved, "reviewed_by": ownerID, "reviewed_at": time.Now().Unix(),
			}).Error; err != nil {
				return err
			}
			var auditErr error
			auditEvent, auditErr = appendAdtyOfficialAuditTx(tx, ownerID, row.SiteId, "site_invite_withdrawal", strconv.FormatInt(row.Id, 10), "adty.site_invite_withdrawal.approve", strings.TrimSpace(reason), "",
				beforeSnap, map[string]any{"id": row.Id, "site_id": row.SiteId, "status": model.AdtySiteInviteWithdrawApproved, "platform": platform}, model.AuditResultSuccess)
			if auditErr != nil {
				return auditErr
			}
			if !platform {
				return recordSiteReferralAuditTx(tx, row.SiteId, "withdrawal_approve", row.UserId, ownerID, strings.TrimSpace(reason), ip, ua, beforeSnap, map[string]any{"id": row.Id, "status": model.AdtySiteInviteWithdrawApproved})
			}
			return nil
		}
		if err := releaseSiteInviteWithdrawalTx(tx, &row); err != nil {
			return err
		}
		changes := map[string]any{"status": model.AdtySiteInviteWithdrawRejected, "reject_reason": strings.TrimSpace(reason)}
		action := "adty.site_invite_withdrawal.reject"
		if row.Status == model.AdtySiteInviteWithdrawApproved {
			changes["failure_by"], changes["failure_at"] = ownerID, time.Now().Unix()
			action = "adty.site_invite_withdrawal.failed"
		} else {
			changes["reviewed_by"], changes["reviewed_at"] = ownerID, time.Now().Unix()
		}
		if err := tx.Model(&row).Updates(changes).Error; err != nil {
			return err
		}
		var auditErr error
		auditEvent, auditErr = appendAdtyOfficialAuditTx(tx, ownerID, row.SiteId, "site_invite_withdrawal", strconv.FormatInt(row.Id, 10), action, strings.TrimSpace(reason), "",
			beforeSnap, map[string]any{"id": row.Id, "site_id": row.SiteId, "status": model.AdtySiteInviteWithdrawRejected, "platform": platform}, model.AuditResultSuccess)
		if auditErr != nil {
			return auditErr
		}
		if !platform {
			return recordSiteReferralAuditTx(tx, row.SiteId, "withdrawal_reject", row.UserId, ownerID, strings.TrimSpace(reason), ip, ua, beforeSnap, map[string]any{"id": row.Id, "status": model.AdtySiteInviteWithdrawRejected})
		}
		return nil
	})
	if err != nil {
		return "", false, err
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return eventID, replayed, nil
}

// MarkAdtySiteInviteWithdrawalPaid records the owner's offline payment.
// It moves the freeze to withdrawn commission and does not credit quota.
func MarkAdtySiteInviteWithdrawalPaid(ownerID int, withdrawalID int64, txID, proof string) error {
	return MarkAdtySiteInviteWithdrawalPaidWithClientMeta(ownerID, withdrawalID, txID, proof, "", "")
}
func MarkAdtySiteInviteWithdrawalPaidWithClientMeta(ownerID int, withdrawalID int64, txID, proof, ip, ua string) error {
	_, _, err := paySiteInviteWithdrawalWithAudit(ownerID, 0, false, withdrawalID, txID, proof, false, ip, ua)
	return err
}
func MarkClosedAdtySiteInviteWithdrawalPaid(ownerID int, withdrawalID int64, txID, proof string) error {
	_, _, err := paySiteInviteWithdrawalWithAudit(ownerID, 0, false, withdrawalID, txID, proof, true, "", "")
	return err
}
func MarkClosedAdtySiteInviteWithdrawalPaidWithAudit(ownerID int, withdrawalID int64, txID, proof string) (string, bool, error) {
	return MarkClosedAdtySiteInviteWithdrawalPaidWithAuditForActor(ownerID, 0, false, withdrawalID, txID, proof)
}
func MarkClosedAdtySiteInviteWithdrawalPaidWithAuditForActor(ownerID, actorRole int, denyAdminTarget bool, withdrawalID int64, txID, proof string) (string, bool, error) {
	return paySiteInviteWithdrawalWithAudit(ownerID, actorRole, denyAdminTarget, withdrawalID, txID, proof, true, "", "")
}
func paySiteInviteWithdrawalWithAudit(ownerID, actorRole int, denyAdminTarget bool, withdrawalID int64, txID, proof string, platform bool, ip, ua string) (string, bool, error) {
	txID = strings.TrimSpace(txID)
	var auditEvent *model.AuditLog
	replayed := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		lockedRow, err := lockSiteInviteWithdrawalCanonicalForActorTx(tx, withdrawalID, ownerID, actorRole, denyAdminTarget, platform, 0)
		if err != nil {
			return err
		}
		row := *lockedRow
		cleanProof := strings.TrimSpace(proof)
		if strings.HasPrefix(cleanProof, "/api/referral-assets/") {
			canonicalProof, ok := IsOwnedReferralAssetTx(tx, ownerID, model.ReferralAssetPurposePaymentProof, cleanProof)
			if !ok {
				return common.NewMessage("Invalid parameters")
			}
			cleanProof = canonicalProof
		}
		if row.Status == model.AdtySiteInviteWithdrawPaid && row.Method != "balance" && row.TxID == txID && row.ProofNote == cleanProof {
			replayed = true
			return nil
		}
		if row.Status != model.AdtySiteInviteWithdrawApproved || !payableSiteInviteWithdrawal(&row) {
			return common.NewMessage("This withdrawal cannot be marked as paid")
		}
		if err := model.RejectRestrictedAdtyFundWrite(tx, model.AdtyFundWrite{SiteID: row.SiteId, AccountID: row.UserId}); err != nil {
			return err
		}
		if row.Method == "usdt" && txID == "" {
			return common.NewMessage("Enter the transaction hash")
		}
		if row.Method == "alipay" && txID == "" && !strings.HasPrefix(cleanProof, "/api/referral-assets/") {
			return common.NewMessage("Enter the payout reference")
		}
		reference := txID
		if reference == "" {
			reference = cleanProof
		}
		receiptKey := fmt.Sprintf("%x", sha256.Sum256([]byte(row.Method+"|"+row.Network+"|"+reference)))
		var items []model.AdtySiteInviteWithdrawalItem
		if err := tx.Where("withdrawal_id = ? AND status = ?", row.Id, model.AdtySiteInviteWithdrawItemFrozen).Find(&items).Error; err != nil {
			return err
		}
		allocated := decimal.Zero
		for _, item := range items {
			allocated = allocated.Add(item.AmountUSD)
		}
		if !allocated.IsPositive() || !allocated.Equal(row.AmountUSD) {
			return common.NewMessage("This withdrawal cannot be marked as paid")
		}
		for _, item := range items {
			var commission model.AdtySiteInviteCommission
			if err := model.AdtyLockForUpdate(tx).First(&commission, item.CommissionId).Error; err != nil {
				return err
			}
			if err := consumeSiteCommissionHoldTx(tx, &commission, item.AmountUSD, !platform, "paid:"+strconv.FormatInt(row.Id, 10)+":"+strconv.FormatInt(item.Id, 10)); err != nil {
				return err
			}
			if item.RefundUSD.IsPositive() {
				if err := tx.Create(&model.AdtyCommissionRecovery{SiteId: row.SiteId, CommissionId: commission.Id, TradeNo: commission.TradeNo, AmountUSD: item.RefundUSD, Kind: "withdrawn", Status: "open", IdempotencyKey: fmt.Sprintf("payout-refund:%d", item.Id)}).Error; err != nil {
					return err
				}
			}
			updates := map[string]any{
				"frozen_usd":              gorm.Expr("frozen_usd - ?", item.AmountUSD),
				"withdrawn_usd":           gorm.Expr("withdrawn_usd + ?", item.AmountUSD),
				"pending_payout_claw_usd": gorm.Expr("pending_payout_claw_usd - ?", item.RefundUSD),
			}
			if item.RefundUSD.IsPositive() {
				// R1-093: Reflect newly created withdrawn recovery in commission.recovery_usd.
				updates["recovery_usd"] = gorm.Expr("recovery_usd + ?", item.RefundUSD)
			}
			if err := tx.Model(&commission).Updates(updates).Error; err != nil {
				return err
			}
			if err := tx.Model(&item).Update("status", model.AdtySiteInviteWithdrawItemPaid).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&row).Updates(map[string]any{
			"status": model.AdtySiteInviteWithdrawPaid, "paid_at": time.Now().Unix(), "paid_by": ownerID,
			"tx_id": txID, "proof_note": cleanProof, "receipt_key": receiptKey,
		}).Error; err != nil {
			return err
		}
		var auditErr error
		auditEvent, auditErr = appendAdtyOfficialAuditTx(tx, ownerID, row.SiteId, "site_invite_withdrawal", strconv.FormatInt(row.Id, 10), "adty.site_invite_withdrawal.paid", "", "",
			map[string]any{"id": row.Id, "site_id": row.SiteId, "status": row.Status, "amount_usd": row.AmountUSD.String()},
			map[string]any{"id": row.Id, "site_id": row.SiteId, "status": model.AdtySiteInviteWithdrawPaid, "tx_id": txID, "platform": platform}, model.AuditResultSuccess)
		if auditErr != nil {
			return auditErr
		}
		if !platform {
			return recordSiteReferralAuditTx(tx, row.SiteId, "withdrawal_pay", row.UserId, ownerID, cleanProof, ip, ua,
				map[string]any{"id": row.Id, "status": row.Status, "amount_usd": row.AmountUSD.String()},
				map[string]any{"id": row.Id, "status": model.AdtySiteInviteWithdrawPaid, "tx_id": txID})
		}
		return nil
	})
	if err != nil {
		return "", false, err
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return eventID, replayed, nil
}

func requireSiteInviteProcessor(tx *gorm.DB, ownerID, siteID int, platform bool) error {
	var site model.AdtySite
	if err := model.AdtyLockForUpdate(tx).First(&site, siteID).Error; err != nil {
		return err
	}
	if platform {
		if site.Status == model.AdtySiteStatusClosed && ownerID > 0 {
			return nil
		}
		return common.NewMessage("No permission to perform this action")
	}
	if site.OwnerUserId != ownerID || (site.Status != model.AdtySiteStatusActive && site.Status != model.AdtySiteStatusPaused && site.Status != model.AdtySiteStatusClosing) {
		return common.NewMessage("No permission to perform this action")
	}
	return nil
}

// Only the affected commission item can be resized. Approved amounts keep their
// original payable value until the owner reconciles the external result.
// Refund clawbacks consume unfrozen free hold first, then shrink pending
// withdrawals, and only mark approved withdrawals as deferred clawbacks as a last resort.
func releaseSiteInviteCommissionFreezeTx(tx *gorm.DB, commission *model.AdtySiteInviteCommission, amount decimal.Decimal) (decimal.Decimal, error) {
	unfrozenHold := decimal.Max(commission.EarningsHoldUSD.Add(commission.MarginHoldUSD).Sub(commission.FrozenUSD), decimal.Zero)
	needFromFreeze := decimal.Max(amount.Sub(unfrozenHold), decimal.Zero)
	left := decimal.Min(needFromFreeze, decimal.Max(commission.FrozenUSD.Sub(commission.PendingPayoutClawUSD), decimal.Zero))
	deferred := decimal.Zero
	if !left.IsPositive() {
		return deferred, nil
	}
	var items []model.AdtySiteInviteWithdrawalItem
	if err := tx.Where("commission_id = ? AND status = ?", commission.Id, model.AdtySiteInviteWithdrawItemFrozen).Order("id").Find(&items).Error; err != nil {
		return deferred, err
	}
	type lockedPair struct {
		item       model.AdtySiteInviteWithdrawalItem
		withdrawal model.AdtySiteInviteWithdrawal
	}
	pendingPairs := make([]lockedPair, 0, len(items))
	approvedPairs := make([]lockedPair, 0, len(items))
	for _, item := range items {
		var withdrawal model.AdtySiteInviteWithdrawal
		if err := model.AdtyLockForUpdate(tx).First(&withdrawal, item.WithdrawalId).Error; err != nil {
			return deferred, err
		}
		switch withdrawal.Status {
		case model.AdtySiteInviteWithdrawPending:
			pendingPairs = append(pendingPairs, lockedPair{item: item, withdrawal: withdrawal})
		case model.AdtySiteInviteWithdrawApproved:
			approvedPairs = append(approvedPairs, lockedPair{item: item, withdrawal: withdrawal})
		}
	}
	// Phase 1: shrink or cancel pending withdrawals first so funds return immediately.
	for _, pair := range pendingPairs {
		if !left.IsPositive() {
			break
		}
		item := pair.item
		withdrawal := pair.withdrawal
		take := decimal.Min(left, item.AmountUSD.Sub(item.RefundUSD))
		if !take.IsPositive() {
			continue
		}
		remaining := item.AmountUSD.Sub(take)
		changes := map[string]any{"amount_usd": remaining, "released_usd": gorm.Expr("released_usd + ?", take)}
		if remaining.IsZero() {
			changes["status"] = model.AdtySiteInviteWithdrawItemReleased
		}
		if err := tx.Model(&item).Updates(changes).Error; err != nil {
			return deferred, err
		}
		gross := withdrawal.AmountUSD.Sub(take)
		netUSD := decimal.Max(gross.Sub(withdrawal.FeeUSD), decimal.Zero)
		changes = map[string]any{"amount_usd": gross, "net_usd": netUSD}
		if !withdrawal.RequestedUSD.IsPositive() {
			changes["requested_usd"] = withdrawal.AmountUSD
		}
		payoutZero := false
		if withdrawal.Method == "alipay" || withdrawal.Method == "usdt" {
			rate, err := decimal.NewFromString(withdrawal.Rate)
			if withdrawal.Method == "usdt" {
				rate, err = decimal.NewFromInt(1), nil
			}
			if err != nil {
				return deferred, err
			}
			payable := siteInvitePayable(gross, withdrawal.FeeUSD, rate, withdrawal.Method)
			changes["payout_amount"] = payable
			payableDec, parseErr := decimal.NewFromString(payable)
			payoutZero = parseErr != nil || !payableDec.IsPositive()
		}
		shouldCancel := gross.IsZero() || ((withdrawal.Method == "alipay" || withdrawal.Method == "usdt") && (!netUSD.IsPositive() || payoutZero))
		if shouldCancel {
			changes["status"] = model.AdtySiteInviteWithdrawCanceled
		}
		if err := tx.Model(&withdrawal).Updates(changes).Error; err != nil {
			return deferred, err
		}
		if err := tx.Model(commission).Update("frozen_usd", gorm.Expr("frozen_usd - ?", take)).Error; err != nil {
			return deferred, err
		}
		commission.FrozenUSD = decimal.Max(commission.FrozenUSD.Sub(take), decimal.Zero)
		if shouldCancel {
			// R3-32: Release any remaining frozen items on this auto-canceled withdrawal
			// back to their respective commissions so zero-payout pending rows never stall.
			if err := releaseSiteInviteWithdrawalTx(tx, &withdrawal); err != nil {
				return deferred, err
			}
			if remaining.IsPositive() {
				commission.FrozenUSD = decimal.Max(commission.FrozenUSD.Sub(remaining), decimal.Zero)
			}
		}
		left = left.Sub(take)
	}
	// Phase 2: only if still short, mark approved withdrawals for deferred reconciliation.
	for _, pair := range approvedPairs {
		if !left.IsPositive() {
			break
		}
		item := pair.item
		take := decimal.Min(left, item.AmountUSD.Sub(item.RefundUSD))
		if !take.IsPositive() {
			continue
		}
		if err := tx.Model(&item).Update("refund_usd", item.RefundUSD.Add(take)).Error; err != nil {
			return deferred, err
		}
		deferred = deferred.Add(take)
		left = left.Sub(take)
	}
	return deferred, nil
}

func releaseSiteInviteWithdrawalTx(tx *gorm.DB, row *model.AdtySiteInviteWithdrawal) error {
	var items []model.AdtySiteInviteWithdrawalItem
	if err := tx.Where("withdrawal_id = ? AND status = ?", row.Id, model.AdtySiteInviteWithdrawItemFrozen).Find(&items).Error; err != nil {
		return err
	}
	for _, item := range items {
		if item.RefundUSD.IsPositive() {
			var commission model.AdtySiteInviteCommission
			if err := model.AdtyLockForUpdate(tx).First(&commission, item.CommissionId).Error; err != nil {
				return err
			}
			if err := consumeSiteCommissionHoldTx(tx, &commission, item.RefundUSD, true, fmt.Sprintf("failed-payout:%d", item.Id)); err != nil {
				return err
			}
			if err := tx.Model(&commission).Updates(map[string]any{"pending_payout_claw_usd": gorm.Expr("pending_payout_claw_usd - ?", item.RefundUSD), "recovered_usd": gorm.Expr("recovered_usd + ?", item.RefundUSD)}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&model.AdtySiteInviteCommission{}).Where("id = ?", item.CommissionId).
			Update("frozen_usd", gorm.Expr("frozen_usd - ?", item.AmountUSD)).Error; err != nil {
			return err
		}
		if err := tx.Model(&item).Update("status", model.AdtySiteInviteWithdrawItemReleased).Error; err != nil {
			return err
		}
	}
	return nil
}

// AdtySiteInviteWithdrawRulesView reports the site-invite withdrawal methods and parameters enabled by the owner.
type AdtySiteInviteWithdrawRulesView struct {
	Configured   bool     `json:"configured"`
	MinUSD       string   `json:"min_usd,omitempty"`
	FeeUSD       string   `json:"fee_usd,omitempty"`
	CNYRate      string   `json:"cny_rate,omitempty"`
	Methods      []string `json:"methods"`
	USDTNetworks []string `json:"usdt_networks"`
}

// LoadAdtySiteInviteWithdrawRules reports the separate site-invite withdrawal rules.
func LoadAdtySiteInviteWithdrawRules() (AdtySiteInviteWithdrawRulesView, error) {
	return AdtySiteInviteWithdrawRulesView{}, ErrSiteMarketingOwnerOnly
}

func LoadAdtyCustomerSiteInviteWithdrawRules(userID int, requested ...int) (AdtySiteInviteWithdrawRulesView, error) {
	view := AdtySiteInviteWithdrawRulesView{
		Methods:      adtySiteInviteWithdrawMethods(false),
		USDTNetworks: []string{},
	}
	var user model.User
	if err := model.DB.Select("site_id").First(&user, userID).Error; err != nil {
		return view, err
	}
	if len(requested) > 0 && requested[0] > 0 {
		if err := authorizeCommissionSource(model.DB, userID, user.SiteId, requested[0]); err != nil {
			return view, err
		}
		user.SiteId = requested[0]
	}
	if user.SiteId <= 0 {
		return view, nil
	}
	var site model.AdtySite
	if err := model.DB.First(&site, user.SiteId).Error; err != nil {
		return view, err
	}
	policy := site.MarketingPolicy
	externalMethods, usdtNetworks := usableSiteInviteWithdrawConfig(policy)
	if len(externalMethods) > 0 {
		view.Configured = true
		if policy.MinUSD != nil {
			view.MinUSD = policy.MinUSD.String()
		}
		if slices.Contains(externalMethods, adty_setting.ReferralWithdrawMethodUSDT) && policy.FeeUSD != nil {
			view.FeeUSD = policy.FeeUSD.String()
			view.USDTNetworks = usdtNetworks
		}
		if slices.Contains(externalMethods, adty_setting.ReferralWithdrawMethodAlipay) && policy.CNYRate != nil {
			view.CNYRate = policy.CNYRate.String()
		}
		view.Methods = append([]string{"balance"}, externalMethods...)
	}
	return view, nil
}

// ListAdtySiteInviteWithdrawals returns the applicant's own requests.
func ListAdtySiteInviteWithdrawals(userID int) ([]model.AdtySiteInviteWithdrawal, error) {
	var rows []model.AdtySiteInviteWithdrawal
	err := model.DB.Where("user_id = ?", userID).Order("id DESC").Find(&rows).Error
	return rows, err
}

// ListAdtyOwnerSiteInviteWithdrawals returns withdrawals for the owner's site.
func ListAdtyOwnerSiteInviteWithdrawals(siteID int, status string, page *common.PageInfo) ([]model.AdtySiteInviteWithdrawal, int64, error) {
	return pageCommissionWithdrawals(model.DB.Model(&model.AdtySiteInviteWithdrawal{}).Where("site_id = ?", siteID), status, page)
}
func ListClosedSiteInviteWithdrawals(status string, page *common.PageInfo) ([]model.AdtySiteInviteWithdrawal, int64, error) {
	return pageCommissionWithdrawals(closedSiteInviteWithdrawalQuery(status), status, page)
}
func closedSiteInviteWithdrawalQuery(status string) *gorm.DB {
	query := model.DB.Model(&model.AdtySiteInviteWithdrawal{}).Where("site_id IN (?)", model.DB.Model(&model.AdtySite{}).Select("id").Where("status = ?", model.AdtySiteStatusClosed))
	if status == "actionable" || status == model.AdtySiteInviteWithdrawPending || status == model.AdtySiteInviteWithdrawApproved {
		return query.Where("method <> ?", "balance")
	}
	return query
}
func pageCommissionWithdrawals(query *gorm.DB, status string, page *common.PageInfo) ([]model.AdtySiteInviteWithdrawal, int64, error) {
	if page == nil || page.PageSize < 1 || page.PageSize > 100 || page.Page < 1 || page.Page > math.MaxInt/page.PageSize {
		return nil, 0, common.NewMessage("Invalid parameters")
	}
	switch status {
	case "":
	case "actionable":
		query = query.Where("status IN ?", []string{model.AdtySiteInviteWithdrawPending, model.AdtySiteInviteWithdrawApproved})
	case model.AdtySiteInviteWithdrawPending, model.AdtySiteInviteWithdrawApproved, model.AdtySiteInviteWithdrawPaid, model.AdtySiteInviteWithdrawCanceled, model.AdtySiteInviteWithdrawRejected:
		query = query.Where("status = ?", status)
	default:
		return nil, 0, common.NewMessage("Invalid parameters")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.AdtySiteInviteWithdrawal
	err := query.Order("id DESC").Offset(page.GetStartIdx()).Limit(page.PageSize).Find(&rows).Error
	return rows, total, err
}

// CancelAdtySiteInviteWithdrawal lets the applicant cancel a pending request inside the main-site cancel window.
func CancelAdtySiteInviteWithdrawal(userID int, withdrawalID int64, now int64) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		lockedRow, err := lockSiteInviteWithdrawalCanonicalTx(tx, withdrawalID, 0, false, userID)
		if err != nil {
			return err
		}
		row := *lockedRow
		if state := SiteInviteCancellation(&row, now); !state.CanCancel {
			return common.NewMessage(state.CancelReason)
		}
		if err := releaseSiteInviteWithdrawalTx(tx, &row); err != nil {
			return err
		}
		return tx.Model(&row).Update("status", model.AdtySiteInviteWithdrawCanceled).Error
	})
}

// adtySiteInviteWithdrawMethods is the official catalog for this path.
// Transfer-to-balance stays available even before the site owner saves cash rules.
// External payout is only Alipay or USDT (DEC-025); WeChat is not offered.
func adtySiteInviteWithdrawMethods(configured bool) []string {
	if !configured {
		return []string{"balance"}
	}
	return []string{"balance", "usdt", "alipay"}
}

func isAdtySiteInviteWeChatMethod(method string) bool {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "wechat", "weixin", "wei_xin", "wei-xin", "wx", "wxpay", "wechatpay", "weixinpay", "wechat_pay", "wechat-pay", "微信", "微信支付":
		return true
	default:
		return false
	}
}
