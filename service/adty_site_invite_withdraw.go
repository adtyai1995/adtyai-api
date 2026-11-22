package service

import (
	"math"
	"strconv"
	"strings"
	"time"

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
	ExpectedCNYRate decimal.Decimal
	IdempotencyKey  string
}

// CreateAdtySiteInviteWithdrawal freezes available site invite commission.
// Balance completes immediately into quota. External payout methods are Alipay or USDT
// only (DEC-025); cash methods stay pending for that site's owner.
func CreateAdtySiteInviteWithdrawal(input AdtySiteInviteWithdrawalInput) (*model.AdtySiteInviteWithdrawal, error) {
	if !input.Amount.Equal(input.Amount.Round(8)) {
		return nil, common.NewMessage("Invalid parameters")
	}
	amount := input.Amount.Round(8)
	key := strings.TrimSpace(input.IdempotencyKey)
	method := strings.TrimSpace(input.Method)
	if input.UserID <= 0 || input.SiteID <= 0 || !amount.IsPositive() || key == "" || len(key) > 128 {
		return nil, common.NewMessage("Invalid parameters")
	}
	if isAdtySiteInviteWeChatMethod(method) {
		return nil, common.NewMessage("Unsupported withdrawal method")
	}
	row := &model.AdtySiteInviteWithdrawal{
		SiteId: input.SiteID, UserId: input.UserID, AmountUSD: amount, NetUSD: amount,
		Method: method, Status: model.AdtySiteInviteWithdrawPending, IdempotencyKey: key,
	}
	var credit int
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := model.AdtyLockForUpdate(tx).Select("id", "site_id").First(&user, input.UserID).Error; err != nil {
			return err
		}
		if user.SiteId != input.SiteID {
			return common.NewMessage("No permission to perform this action")
		}
		var existing model.AdtySiteInviteWithdrawal
		lookup := tx.Where("idempotency_key = ?", key).Limit(1).Find(&existing)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected > 0 {
			if existing.SiteId != input.SiteID || existing.UserId != input.UserID || existing.Method != method || !existing.AmountUSD.Equal(amount) {
				return model.ErrAdtyIdempotencyConflict
			}
			row = &existing
			return nil
		}
		var site model.AdtySite
		if err := tx.First(&site, input.SiteID).Error; err != nil {
			return err
		}
		policy := site.MarketingPolicy
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
		case "usdt":
			if policy.MinUSD == nil || policy.FeeUSD == nil || policy.CNYRate == nil {
				return common.NewMessage("This withdrawal method is not available")
			}
			if amount.LessThan(*policy.MinUSD) {
				return common.NewMessage("The minimum withdrawal is ${{amount}}", map[string]any{"amount": policy.MinUSD.String()})
			}
			network := strings.ToUpper(strings.TrimSpace(input.Network))
			if !ValidateUSDTAddress(network, input.AccountNo) {
				return common.NewMessage("The address does not match the {{network}} network", map[string]any{"network": network})
			}
			net := amount.Sub(*policy.FeeUSD)
			if !net.IsPositive() {
				return common.NewMessage("The amount must be larger than the network fee")
			}
			row.Network, row.AccountNo, row.FeeUSD, row.NetUSD = network, strings.TrimSpace(input.AccountNo), *policy.FeeUSD, net
		case "alipay":
			if policy.MinUSD == nil || policy.FeeUSD == nil || policy.CNYRate == nil {
				return common.NewMessage("This withdrawal method is not available")
			}
			if amount.LessThan(*policy.MinUSD) {
				return common.NewMessage("The minimum withdrawal is ${{amount}}", map[string]any{"amount": policy.MinUSD.String()})
			}
			if !validReferralQR(input.QRImageURL) || strings.TrimSpace(input.AccountName) == "" {
				return common.NewMessage("Upload the payment QR code and enter the payee name")
			}
			if !policy.CNYRate.Equal(input.ExpectedCNYRate) {
				return &RateChangedError{Change: ReferralRateChange{Rate: policy.CNYRate.String(), CNYAmount: amount.Mul(*policy.CNYRate).Round(2).StringFixed(2)}}
			}
			row.AccountName = strings.TrimSpace(input.AccountName)
			row.QRImageURL = strings.TrimSpace(input.QRImageURL)
			row.Rate = policy.CNYRate.String()
			row.PayoutAmount = amount.Mul(*policy.CNYRate).Round(2).StringFixed(2)
			row.PayoutCurrency = "CNY"
		default:
			return common.NewMessage("Unsupported withdrawal method")
		}
		if method == "alipay" {
			canonicalQR, ok := IsOwnedReferralAssetTx(tx, input.UserID, model.ReferralAssetPurposeWithdrawalQR, input.QRImageURL)
			if !ok {
				return common.NewMessage("Upload the payment QR code and enter the payee name")
			}
			row.QRImageURL = canonicalQR
		}
		if err := tx.Create(row).Error; err != nil {
			return err
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
			// 邀请佣金转到余额的批次记在佣金所属站（row.SiteId）。
			return model.CreateAdtyFundBatchesWithSiteTx(tx, input.UserID, model.AdtyFundBatchSiteID(row.SiteId), ref, int64(quota), 0)
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

func allocateSiteInviteWithdrawalTx(tx *gorm.DB, withdrawal *model.AdtySiteInviteWithdrawal, toBalance bool) error {
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
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var row model.AdtySiteInviteWithdrawal
		if err := model.AdtyLockForUpdate(tx).First(&row, withdrawalID).Error; err != nil {
			return err
		}
		if err := requireSiteInviteWithdrawOwner(tx, ownerID, row.SiteId); err != nil {
			return err
		}
		if row.Method == "balance" || (approve && row.Status != model.AdtySiteInviteWithdrawPending) || (!approve && row.Status != model.AdtySiteInviteWithdrawPending && row.Status != model.AdtySiteInviteWithdrawApproved) {
			return common.NewMessage("Only pending withdrawals can be reviewed")
		}
		if approve {
			return tx.Model(&row).Updates(map[string]any{
				"status": model.AdtySiteInviteWithdrawApproved, "reviewed_by": ownerID, "reviewed_at": time.Now().Unix(),
			}).Error
		}
		if err := releaseSiteInviteWithdrawalTx(tx, &row); err != nil {
			return err
		}
		return tx.Model(&row).Updates(map[string]any{
			"status": model.AdtySiteInviteWithdrawRejected, "reviewed_by": ownerID, "reviewed_at": time.Now().Unix(),
			"reject_reason": strings.TrimSpace(reason),
		}).Error
	})
}

// MarkAdtySiteInviteWithdrawalPaid records the owner's offline payment.
// It moves the freeze to withdrawn commission and does not credit quota.
func MarkAdtySiteInviteWithdrawalPaid(ownerID int, withdrawalID int64, txID, proof string) error {
	txID = strings.TrimSpace(txID)
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var row model.AdtySiteInviteWithdrawal
		if err := model.AdtyLockForUpdate(tx).First(&row, withdrawalID).Error; err != nil {
			return err
		}
		if err := requireSiteInviteWithdrawOwner(tx, ownerID, row.SiteId); err != nil {
			return err
		}
		if row.Status != model.AdtySiteInviteWithdrawApproved {
			return common.NewMessage("This withdrawal cannot be marked as paid")
		}
		if row.Method == "usdt" && txID == "" {
			return common.NewMessage("Enter the transaction hash")
		}
		cleanProof := strings.TrimSpace(proof)
		if strings.HasPrefix(cleanProof, "/api/referral-assets/") {
			canonicalProof, ok := IsOwnedReferralAssetTx(tx, 0, model.ReferralAssetPurposePaymentProof, cleanProof)
			if !ok {
				return common.NewMessage("Invalid parameters")
			}
			cleanProof = canonicalProof
		}
		var items []model.AdtySiteInviteWithdrawalItem
		if err := tx.Where("withdrawal_id = ? AND status = ?", row.Id, model.AdtySiteInviteWithdrawItemFrozen).Find(&items).Error; err != nil {
			return err
		}
		for _, item := range items {
			var commission model.AdtySiteInviteCommission
			if err := model.AdtyLockForUpdate(tx).First(&commission, item.CommissionId).Error; err != nil {
				return err
			}
			if err := consumeSiteCommissionHoldTx(tx, &commission, item.AmountUSD, true, "paid:"+strconv.FormatInt(row.Id, 10)+":"+strconv.FormatInt(item.Id, 10)); err != nil {
				return err
			}
			if err := tx.Model(&commission).Updates(map[string]any{
				"frozen_usd":    gorm.Expr("frozen_usd - ?", item.AmountUSD),
				"withdrawn_usd": gorm.Expr("withdrawn_usd + ?", item.AmountUSD),
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&item).Update("status", model.AdtySiteInviteWithdrawItemPaid).Error; err != nil {
				return err
			}
		}
		return tx.Model(&row).Updates(map[string]any{
			"status": model.AdtySiteInviteWithdrawPaid, "paid_at": time.Now().Unix(), "reviewed_by": ownerID,
			"tx_id": txID, "proof_note": cleanProof,
		}).Error
	})
}

func requireSiteInviteWithdrawOwner(tx *gorm.DB, ownerID, siteID int) error {
	var site model.AdtySite
	if err := tx.First(&site, siteID).Error; err != nil {
		return err
	}
	if site.OwnerUserId != ownerID || (site.Status != model.AdtySiteStatusActive && site.Status != model.AdtySiteStatusPaused && site.Status != model.AdtySiteStatusClosing) {
		return common.NewMessage("No permission to perform this action")
	}
	return nil
}

func releaseSiteInviteCommissionFreezeTx(tx *gorm.DB, commission *model.AdtySiteInviteCommission, amount decimal.Decimal) error {
	if !amount.IsPositive() || !commission.FrozenUSD.IsPositive() {
		return nil
	}
	var items []model.AdtySiteInviteWithdrawalItem
	if err := tx.Where("commission_id = ? AND status = ?", commission.Id, model.AdtySiteInviteWithdrawItemFrozen).Order("id").Find(&items).Error; err != nil {
		return err
	}
	left := amount
	if left.GreaterThan(commission.FrozenUSD) {
		left = commission.FrozenUSD
	}
	for _, item := range items {
		if !left.IsPositive() {
			break
		}
		var withdrawal model.AdtySiteInviteWithdrawal
		if err := model.AdtyLockForUpdate(tx).First(&withdrawal, item.WithdrawalId).Error; err != nil {
			return err
		}
		if withdrawal.Status != model.AdtySiteInviteWithdrawPending && withdrawal.Status != model.AdtySiteInviteWithdrawApproved {
			continue
		}
		if err := releaseSiteInviteWithdrawalTx(tx, &withdrawal); err != nil {
			return err
		}
		if err := tx.Model(&withdrawal).Updates(map[string]any{
			"status": model.AdtySiteInviteWithdrawCanceled, "reject_reason": "refund released the freeze",
		}).Error; err != nil {
			return err
		}
		left = left.Sub(item.AmountUSD)
	}
	return nil
}

func releaseSiteInviteWithdrawalTx(tx *gorm.DB, row *model.AdtySiteInviteWithdrawal) error {
	var items []model.AdtySiteInviteWithdrawalItem
	if err := tx.Where("withdrawal_id = ? AND status = ?", row.Id, model.AdtySiteInviteWithdrawItemFrozen).Find(&items).Error; err != nil {
		return err
	}
	for _, item := range items {
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

// AdtySiteInviteWithdrawRulesView is blank for cash methods until the owner saves min, fee, and rate.
type AdtySiteInviteWithdrawRulesView struct {
	Configured bool     `json:"configured"`
	MinUSD     string   `json:"min_usd,omitempty"`
	FeeUSD     string   `json:"fee_usd,omitempty"`
	CNYRate    string   `json:"cny_rate,omitempty"`
	Methods    []string `json:"methods"`
}

// LoadAdtySiteInviteWithdrawRules reports the separate site-invite withdrawal rules.
func LoadAdtySiteInviteWithdrawRules() (AdtySiteInviteWithdrawRulesView, error) {
	return AdtySiteInviteWithdrawRulesView{}, ErrSiteMarketingOwnerOnly
}

func LoadAdtyCustomerSiteInviteWithdrawRules(userID int) (AdtySiteInviteWithdrawRulesView, error) {
	view := AdtySiteInviteWithdrawRulesView{Methods: adtySiteInviteWithdrawMethods(false)}
	var user model.User
	if err := model.DB.Select("site_id").First(&user, userID).Error; err != nil {
		return view, err
	}
	if user.SiteId <= 0 {
		return view, nil
	}
	var site model.AdtySite
	if err := model.DB.First(&site, user.SiteId).Error; err != nil {
		return view, err
	}
	policy := site.MarketingPolicy
	if policy.MinUSD != nil && policy.FeeUSD != nil && policy.CNYRate != nil {
		view.Configured = true
		view.MinUSD, view.FeeUSD, view.CNYRate = policy.MinUSD.String(), policy.FeeUSD.String(), policy.CNYRate.String()
		view.Methods = adtySiteInviteWithdrawMethods(true)
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
	if page == nil || page.PageSize < 1 || page.PageSize > 100 || page.Page < 1 || page.Page > math.MaxInt/page.PageSize {
		return nil, 0, common.NewMessage("Invalid parameters")
	}
	query := model.DB.Model(&model.AdtySiteInviteWithdrawal{}).Where("site_id = ?", siteID)
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
	window := int64(max(adty_setting.GetReferralSetting().CancelWindowMinutes, 0)) * 60
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var row model.AdtySiteInviteWithdrawal
		if err := model.AdtyLockForUpdate(tx).Where("id = ? AND user_id = ?", withdrawalID, userID).First(&row).Error; err != nil {
			return err
		}
		if row.Status != model.AdtySiteInviteWithdrawPending {
			return common.NewMessage("Only pending withdrawals can be canceled")
		}
		if now-row.CreatedAt > window {
			return common.NewMessage("The cancel window has passed")
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
