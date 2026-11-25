package service

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// 三种提现（任务 6.4）：转入余额（立即完成）、USDT（人工审核）、支付宝（人工审核，汇率提交时查库锁定）。

var (
	ErrReferralRateChanged   = errors.New("rate_changed")
	ErrReferralRateUnset     = errors.New("withdrawal exchange rate is not set")
	tronAddressPattern       = regexp.MustCompile(`^T[1-9A-HJ-NP-Za-km-z]{33}$`)
	evmAddressPattern        = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	referralWithdrawNetworks = map[string]*regexp.Regexp{
		"TRC20": tronAddressPattern, "BEP20": evmAddressPattern,
		"POLYGON": evmAddressPattern, "XLAYER": evmAddressPattern,
	}
)

// ReferralWithdrawalCreateInput 是一笔提现申请。
type ReferralWithdrawalCreateInput struct {
	UserId          int
	Amount          decimal.Decimal
	AccountType     string // balance | usdt | alipay
	AccountNetwork  string // TRC20 | BEP20 | POLYGON | XLAYER（只有 usdt 用）
	AccountNo       string // USDT 地址或支付宝账号
	AccountName     string // 收款人真实姓名（支付宝必填）
	QRImageURL      string // 支付宝收款码（选填）
	ExpectedCNYRate decimal.Decimal
	IdempotencyKey  string
}

// ReferralRateChange 是 rate_changed 时返回给前端的新汇率。
type ReferralRateChange struct {
	Rate      string `json:"rate"`
	CNYAmount string `json:"cny_amount"`
	UpdatedAt int64  `json:"updated_at"`
}

// RateChangedError 带上新的汇率和人民币金额。
type RateChangedError struct{ Change ReferralRateChange }

func (e *RateChangedError) Error() string { return ErrReferralRateChanged.Error() }
func (e *RateChangedError) Unwrap() error { return ErrReferralRateChanged }

// CurrentWithdrawCNYRate 直接查库读提现汇率（只读无锁）：行不存在、解析不了或不大于 0 都算未设置。
func CurrentWithdrawCNYRate() (rate decimal.Decimal, updatedAt int64, err error) {
	return currentWithdrawCNYRateTx(model.DB, false)
}

// currentWithdrawCNYRateTx reads the withdrawal CNY rate, optionally locking
// the option rows inside a write transaction so an accepted application has
// one durable rate snapshot without acquiring FOR UPDATE locks on GET paths.
func currentWithdrawCNYRateTx(tx *gorm.DB, lock bool) (decimal.Decimal, int64, error) {
	var (
		raw string
		ok  bool
		err error
	)
	if lock {
		raw, ok, err = model.ReadOptionForUpdate(tx, adty_setting.ReferralKeyWithdrawCNYRate)
	} else {
		raw, ok, err = model.ReadOptionInTx(tx, adty_setting.ReferralKeyWithdrawCNYRate)
	}
	if err != nil {
		return decimal.Zero, 0, err
	}
	if !ok {
		return decimal.Zero, 0, ErrReferralRateUnset
	}
	rate, err := decimal.NewFromString(strings.TrimSpace(raw))
	if err != nil || !rate.IsPositive() {
		return decimal.Zero, 0, ErrReferralRateUnset
	}
	var updatedRaw string
	if lock {
		updatedRaw, _, err = model.ReadOptionForUpdate(tx, adty_setting.ReferralKeyWithdrawCNYRateUpdatedAt)
	} else {
		updatedRaw, _, err = model.ReadOptionInTx(tx, adty_setting.ReferralKeyWithdrawCNYRateUpdatedAt)
	}
	if err != nil {
		return decimal.Zero, 0, err
	}
	updated, _ := decimal.NewFromString(strings.TrimSpace(updatedRaw))
	return rate, updated.IntPart(), nil
}

// ValidateUSDTAddress 按链校验地址：TRC20 是 T 开头的 base58；BEP20、Polygon 和 X Layer 是 0x 开头的 40 位十六进制。
func ValidateUSDTAddress(network, address string) bool {
	pattern, ok := referralWithdrawNetworks[strings.ToUpper(network)]
	return ok && pattern.MatchString(strings.TrimSpace(address))
}

// validReferralQR checks format only; DB ownership and purpose are enforced by IsOwnedReferralAssetTx inside the transaction.
func validReferralQR(raw string) bool {
	_, ok := extractReferralAssetName(raw)
	return ok
}

// CreateWithdrawal 建提现单。所有去向都在事务里锁佣金账户行；防超提靠条件更新 available_amount >= ?；
// 幂等键先查后写（审阅重点 3、10）。
func (s *ReferralService) CreateWithdrawal(input ReferralWithdrawalCreateInput) (*model.ReferralWithdrawal, error) {
	cfg := referralSetting()
	scale := int32(2)
	if input.AccountType == model.ReferralWithdrawToUSDT {
		scale = 6
	}
	if !input.Amount.Equal(input.Amount.Truncate(scale)) {
		return nil, common.NewMessage("Invalid parameters")
	}
	amount := input.Amount.Truncate(scale)
	if !amount.IsPositive() {
		return nil, common.NewMessage("Enter a withdrawal amount")
	}
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" || len(key) > 128 {
		return nil, common.NewMessage("Invalid idempotency key")
	}
	withdrawal := &model.ReferralWithdrawal{
		UserId: input.UserId, SettlementCurrency: "USD", RequestedAmount: moneyF(amount), Amount: moneyF(amount), NetAmount: moneyF(amount),
		AccountType: input.AccountType, SubmittedAt: time.Now().Unix(), Status: model.ReferralWithdrawalStatusPending,
	}
	switch input.AccountType {
	case model.ReferralWithdrawToBalance, model.ReferralWithdrawToUSDT, model.ReferralWithdrawToAlipay:
	default:
		return nil, common.NewMessage("Unsupported withdrawal method")
	}
	normalizedNetwork := strings.ToUpper(strings.TrimSpace(input.AccountNetwork))
	trimmedAccountNo := strings.TrimSpace(input.AccountNo)
	trimmedAccountName := strings.TrimSpace(input.AccountName)
	normalizedQR := strings.TrimSpace(input.QRImageURL)
	if name, ok := extractReferralAssetName(normalizedQR); ok {
		normalizedQR = "/api/referral-assets/" + name
	}

	var result *model.ReferralWithdrawal
	var creditQuota int64
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		// R3-08: Acquire users(UserId) BEFORE referral affiliate/commission/account locks
		// to match canonical global User -> Affiliate -> Commission -> Withdrawal -> Account order.
		var lockedOwner model.User
		if err := model.AdtyLockForUpdate(tx).Select("id", "site_id").First(&lockedOwner, input.UserId).Error; err != nil {
			return err
		}
		affiliate, err := s.lockAffiliateByUserTx(tx, input.UserId)
		if err != nil {
			return err
		}
		var lockedCommissions []model.ReferralCommission
		if err := model.AdtyLockForUpdate(tx).Where("affiliate_id = ? AND status IN ?", affiliate.Id,
			[]string{model.ReferralCommissionStatusAvailable, model.ReferralCommissionStatusFrozen}).
			Order("id").Find(&lockedCommissions).Error; err != nil {
			return err
		}
		account, err := lockReferralAccountTx(tx, affiliate.Id)
		if err != nil {
			return err
		}
		// R4-044 / R7-29: Scope idempotency keys per user so one caller cannot
		// block or probe another user's withdrawal keys, while still matching
		// historical raw keys owned by this user.
		scopedKey := adtyReferralWithdrawScopedKey(input.UserId, key)
		var existing []model.ReferralWithdrawal
		if err := tx.Where("user_id = ? AND (idempotency_key = ? OR idempotency_key = ?)", input.UserId, scopedKey, key).Limit(1).Find(&existing).Error; err != nil {
			return err
		}
		if len(existing) > 0 {
			existingQR := strings.TrimSpace(existing[0].QRImageURL)
			if name, ok := extractReferralAssetName(existingQR); ok {
				existingQR = "/api/referral-assets/" + name
			}
			requestedAmount := moneyDec(existing[0].RequestedAmount)
			if !requestedAmount.IsPositive() {
				requestedAmount = moneyDec(existing[0].Amount)
			}
			sameDest := existing[0].UserId == input.UserId &&
				existing[0].AccountType == input.AccountType &&
				requestedAmount.Equal(amount) &&
				strings.ToUpper(strings.TrimSpace(existing[0].AccountNetwork)) == normalizedNetwork &&
				strings.TrimSpace(existing[0].AccountNo) == trimmedAccountNo &&
				strings.TrimSpace(existing[0].AccountName) == trimmedAccountName &&
				existingQR == normalizedQR
			if sameDest && input.AccountType == model.ReferralWithdrawToAlipay {
				existingRate, err := common.ParseBoundedDecimalString(existing[0].CNYRate, 18, 8)
				if err != nil || !existingRate.Equal(input.ExpectedCNYRate) {
					sameDest = false
				}
			}
			if !sameDest {
				return model.ErrAdtyIdempotencyConflict
			}
			result = &existing[0]
			return nil
		}
		if !affiliate.WithdrawalEnabled || affiliate.Status != model.ReferralAffiliateStatusApproved {
			return ErrReferralWithdrawBlocked
		}
		if err := model.RejectRestrictedAdtyFundWrite(tx, model.AdtyFundWrite{SiteID: 0, AccountID: input.UserId}); err != nil {
			return err
		}
		switch input.AccountType {
		case model.ReferralWithdrawToBalance:
			withdrawal.Status = model.ReferralWithdrawalStatusPaid
			withdrawal.PaidAt = withdrawal.SubmittedAt
		case model.ReferralWithdrawToUSDT:
			usableMethods, usdtNetworks := cfg.UsableWithdrawConfig(false)
			if cfg.MinWithdrawUSD == nil || cfg.USDTWithdrawFee == nil || !slices.Contains(usableMethods, adty_setting.ReferralWithdrawMethodUSDT) {
				return ErrReferralWithdrawUnavailable
			}
			if !slices.Contains(usdtNetworks, normalizedNetwork) {
				return common.NewMessage("This USDT network is not enabled")
			}
			if amount.LessThan(decimal.NewFromFloat(*cfg.MinWithdrawUSD)) {
				return common.NewMessage("The minimum withdrawal is ${{amount}}", map[string]any{"amount": *cfg.MinWithdrawUSD})
			}
			if !ValidateUSDTAddress(normalizedNetwork, trimmedAccountNo) {
				return common.NewMessage("The address does not match the {{network}} network", map[string]any{"network": normalizedNetwork})
			}
			fee := decimal.NewFromFloat(*cfg.USDTWithdrawFee).Round(6)
			net := amount.Sub(fee).Round(6)
			if !net.IsPositive() {
				return common.NewMessage("The amount must be larger than the network fee")
			}
			withdrawal.AccountNetwork = normalizedNetwork
			withdrawal.AccountNo = trimmedAccountNo
			withdrawal.FeeAmount = moneyF(fee)
			withdrawal.NetAmount = moneyF(net)
		case model.ReferralWithdrawToAlipay:
			if cfg.MinWithdrawUSD == nil {
				return ErrReferralWithdrawUnavailable
			}
			effectiveMethods, _ := cfg.EffectiveWithdrawConfig(true)
			if !slices.Contains(effectiveMethods, adty_setting.ReferralWithdrawMethodAlipay) {
				return ErrReferralWithdrawUnavailable
			}
			if amount.LessThan(decimal.NewFromFloat(*cfg.MinWithdrawUSD)) {
				return common.NewMessage("The minimum withdrawal is ${{amount}}", map[string]any{"amount": *cfg.MinWithdrawUSD})
			}
			if trimmedAccountName == "" || trimmedAccountNo == "" {
				return common.NewMessage("Enter the payee real name and Alipay account")
			}
			if utf8.RuneCountInString(trimmedAccountName) > 64 || utf8.RuneCountInString(trimmedAccountNo) > 128 {
				return common.NewMessage("Invalid parameters")
			}
			withdrawal.AccountName = trimmedAccountName
			withdrawal.AccountNo = trimmedAccountNo
			if trimmedQR := strings.TrimSpace(input.QRImageURL); trimmedQR != "" {
				if !validReferralQR(trimmedQR) {
					return common.NewMessage("Invalid payment QR code")
				}
				canonicalQR, ok := IsOwnedReferralAssetTx(tx, input.UserId, model.ReferralAssetPurposeWithdrawalQR, trimmedQR)
				if !ok {
					return common.NewMessage("Invalid payment QR code")
				}
				withdrawal.QRImageURL = canonicalQR
			}
		}
		if input.AccountType == model.ReferralWithdrawToAlipay {
			rate, updatedAt, err := currentWithdrawCNYRateTx(tx, true)
			if err != nil {
				return err
			}
			cny := amount.Mul(rate).Round(2)
			if !rate.Equal(input.ExpectedCNYRate) {
				return &RateChangedError{Change: ReferralRateChange{Rate: rate.String(), CNYAmount: cny.StringFixed(2), UpdatedAt: updatedAt}}
			}
			if !cny.IsPositive() {
				return common.NewMessage("Withdrawal payout amount must be positive")
			}
			withdrawal.CNYRate = rate.String()
			withdrawal.CNYAmount = cny.StringFixed(2)
		}
		var closing int64
		if err := tx.Model(&model.AdtySite{}).Where("owner_user_id = ? AND status = ?", input.UserId, model.AdtySiteStatusClosing).Count(&closing).Error; err != nil {
			return err
		}
		if closing > 0 {
			return common.NewMessage("Withdrawals are paused while the site is closing")
		}
		update := tx.Model(&model.ReferralCommissionAccount{}).Where("id = ? AND available_amount >= ?", account.Id, moneyF(amount))
		if withdrawal.AccountType == model.ReferralWithdrawToBalance {
			update = update.Updates(map[string]any{
				"available_amount": gorm.Expr("available_amount - ?", moneyF(amount)),
				"withdrawn_amount": gorm.Expr("withdrawn_amount + ?", moneyF(amount)),
			})
		} else {
			update = update.Updates(map[string]any{
				"available_amount": gorm.Expr("available_amount - ?", moneyF(amount)),
				"frozen_amount":    gorm.Expr("frozen_amount + ?", moneyF(amount)),
			})
		}
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected == 0 {
			return ErrReferralInsufficient
		}
		withdrawal.AffiliateId = affiliate.Id
		withdrawal.IdempotencyKey = &scopedKey
		if err := tx.Create(withdrawal).Error; err != nil {
			return err
		}
		if err := s.allocateWithdrawalItemsTx(tx, affiliate.Id, withdrawal.Id, amount); err != nil {
			return err
		}
		ledgerType, deltaFrozen, deltaWithdrawn := "withdraw_freeze", amount, decimal.Zero
		if withdrawal.AccountType == model.ReferralWithdrawToBalance {
			ledgerType, deltaFrozen, deltaWithdrawn = "withdraw_to_balance", decimal.Zero, amount
			if err := s.markWithdrawalItemsPaidTx(tx, withdrawal.Id); err != nil {
				return err
			}
			creditQuotaInt, convErr := common.WalletQuotaFromDecimalStrict(amount.Mul(common.QuotaPerUnitDecimal()).Floor())
			if convErr != nil {
				return convErr
			}
			if creditQuotaInt <= 0 {
				return common.NewMessage("Invalid withdrawal amount")
			}
			creditQuota = int64(creditQuotaInt)
			before, err := model.AdtyCreditUserQuotaTx(tx, input.UserId, creditQuota)
			if err != nil {
				return err
			}
			ref := fmt.Sprintf("referral-withdrawal-%d", withdrawal.Id)
			if err := model.InsertAdtyLedgerTx(tx, &model.AdtyBalanceLedger{
				UserId: input.UserId, DeltaQuota: creditQuota, BalanceBefore: before, BalanceAfter: before + creditQuota,
				Type: model.AdtyLedgerTypeCommissionTransfer, RefType: "referral_withdrawal", RefId: ref,
				Reason: "commission transferred to balance", IdempotencyKey: &ref,
			}); err != nil {
				return err
			}
			// 转余额也要留下批次背书（对偶：adty_site_invite_withdraw.go 的转余额路径）：
			// 只加余额不写批次，下一次扣费会把这条真实入账补建成 legacy 批次，
			// 从而被记为 fund_batch_gap，对账口径从此不可用。source 用 ledger 的幂等键。
			//
			// 站点必须一起写（ISS-R4-0047）：这条批次背书的是**用户的**本金，归属跟着
			// 用户当前所属站走，和用户调额的批次写入同一条口径。
			//
			// 两处修复各自都对，合起来才不一致：0086（w6）给这条路加了批次写入时
			// 还没有 site 列；0047（w23）给对偶路径加了 site，但他没回来改这里。
			// 结果同一个业务的两个实现又分叉了——这次差的是站点。
			//
			// 落 NULL 不只是「少一个字段」：按 adty_fund_batch.go 的定义，NULL 表示
			// 「当时就证不出站点」。这里站点是已知的，写 NULL 等于把漏填伪装成历史遗留，
			// 按站算本金负债时这部分会掉进「站点未知」，两边都不平。
			var batchOwner model.User
			if err := tx.Select("id", "site_id").First(&batchOwner, input.UserId).Error; err != nil {
				return err
			}
			// R3-09: Split balance conversion into per-commission fund batches so
			// multi-commission balance transfers can claw back each refunded commission
			// independently without masking the 2nd..Nth refunds behind a shared Original.
			var allocatedItems []model.ReferralWithdrawalItem
			if err := tx.Where("withdrawal_id = ?", withdrawal.Id).Order("id ASC").Find(&allocatedItems).Error; err != nil {
				return err
			}
			type batchSlice struct {
				source string
				quota  int64
			}
			slicesToCreate := make([]batchSlice, 0, len(allocatedItems))
			var allocatedSum int64
			for _, item := range allocatedItems {
				itemAmt := moneyDec(item.AllocatedAmount)
				if !itemAmt.IsPositive() {
					continue
				}
				qInt, qErr := common.WalletQuotaFromDecimalStrict(itemAmt.Mul(common.QuotaPerUnitDecimal()).Floor())
				if qErr != nil {
					return qErr
				}
				src := ref
				if item.CommissionId > 0 {
					src = fmt.Sprintf("referral-withdrawal-%d-c-%d", withdrawal.Id, item.CommissionId)
				}
				slicesToCreate = append(slicesToCreate, batchSlice{source: src, quota: int64(qInt)})
				allocatedSum += int64(qInt)
			}
			if len(slicesToCreate) == 0 {
				slicesToCreate = append(slicesToCreate, batchSlice{source: ref, quota: creditQuota})
			} else if diff := creditQuota - allocatedSum; diff != 0 {
				slicesToCreate[len(slicesToCreate)-1].quota += diff
			}
			sitePtr := model.AdtyFundBatchSiteID(batchOwner.SiteId)
			for _, part := range slicesToCreate {
				if part.quota <= 0 {
					continue
				}
				if err := model.CreateAdtyFundBatchesWithSiteTx(tx, input.UserId,
					sitePtr, part.source, part.quota, 0); err != nil {
					return err
				}
			}
		}
		if err := addReferralLedgerTx(tx, &model.ReferralCommissionLedger{
			AffiliateId: affiliate.Id, UserId: input.UserId, WithdrawalId: withdrawal.Id, Type: ledgerType,
			RefType: "withdrawal", RefId: fmt.Sprint(withdrawal.Id), ExternalRefId: fmt.Sprintf("withdraw:%d", withdrawal.Id),
			DeltaAvailable: moneyF(amount.Neg()), DeltaFrozen: moneyF(deltaFrozen), DeltaWithdrawn: moneyF(deltaWithdrawn),
			Remark: input.AccountType,
		}); err != nil {
			return err
		}
		result = withdrawal
		return nil
	})
	if err != nil {
		return nil, err
	}
	if creditQuota > 0 && result == withdrawal {
		model.AdtySyncCreditUserQuotaCache(input.UserId, creditQuota, "referral commission to balance")
		model.AdtyRecordUserLog(input.UserId, common.NewMessage("Commission transferred to balance: ${{amount}}", map[string]any{"amount": amount.StringFixed(2)}), "")
	}
	return result, nil
}

func (s *ReferralService) lockAffiliateByUserTx(tx *gorm.DB, userId int) (*model.ReferralAffiliate, error) {
	var affiliate model.ReferralAffiliate
	if err := model.AdtyLockForUpdate(tx).Where("user_id = ?", userId).First(&affiliate).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrReferralNotAffiliate
		}
		return nil, err
	}
	return &affiliate, nil
}

// CancelWithdrawal 在提交后 cancel_window_minutes 内撤回待审核的提现，撤回即解冻。
func (s *ReferralService) CancelWithdrawal(withdrawalId, userId int, now int64) error {
	window := int64(max(referralSetting().CancelWindowMinutes, 0)) * 60
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var peek model.ReferralWithdrawal
		if err := tx.Where("id = ? AND user_id = ?", withdrawalId, userId).First(&peek).Error; err != nil {
			return err
		}
		if err := lockWithdrawalCommissionRowsTx(tx, peek.Id); err != nil {
			return err
		}
		var withdrawal model.ReferralWithdrawal
		if err := model.AdtyLockForUpdate(tx).Where("id = ? AND user_id = ?", withdrawalId, userId).First(&withdrawal).Error; err != nil {
			return err
		}
		if withdrawal.Status != model.ReferralWithdrawalStatusPending {
			return common.NewMessage("Only pending withdrawals can be canceled")
		}
		if now-withdrawal.SubmittedAt > window {
			return common.NewMessage("The cancel window has passed")
		}
		return s.releaseWithdrawalTx(tx, &withdrawal, model.ReferralWithdrawalStatusCanceled, userId, "user canceled", now)
	})
}

// lockFrozenWithdrawalsForCommissionsTx 按提现 id 锁住仍占用这些佣金的待处理或已审核提现。
// 退款在锁账户之前调用，和打款/驳回一样走「先佣金、再提现、再账户」。
func lockFrozenWithdrawalsForCommissionsTx(tx *gorm.DB, commissionIDs []int) error {
	if tx == nil || len(commissionIDs) == 0 {
		return nil
	}
	var ids []int
	if err := tx.Model(&model.ReferralWithdrawalItem{}).
		Where("commission_id IN ? AND status = ?", commissionIDs, model.ReferralWithdrawalItemStatusFrozen).
		Order("withdrawal_id").
		Pluck("withdrawal_id", &ids).Error; err != nil {
		return err
	}
	seen := map[int]struct{}{}
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		var row model.ReferralWithdrawal
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", id).First(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

// shrinkFrozenWithdrawalOccupancyTx 按这次实际从冻结余额冲回的金额，缩减待审核提现明细与提现单占用；
// 已审核待打款（approved）提现保留原金额与冻结额，只登记待核验冲回额，在打款或驳回时结算（R1-11）。
// 返回本次已直接从冻结余额缩减的金额，供调用方在重锁账户后扣减。
func shrinkFrozenWithdrawalOccupancyTx(tx *gorm.DB, commissionId int, amount decimal.Decimal) (decimal.Decimal, error) {
	if tx == nil || commissionId <= 0 || !amount.IsPositive() {
		return decimal.Zero, nil
	}
	var items []model.ReferralWithdrawalItem
	if err := model.AdtyLockForUpdate(tx).
		Where("commission_id = ? AND status = ?", commissionId, model.ReferralWithdrawalItemStatusFrozen).
		Order("id").Find(&items).Error; err != nil {
		return decimal.Zero, err
	}
	withdrawalStatus := map[int]string{}
	for _, item := range items {
		if _, seen := withdrawalStatus[item.WithdrawalId]; seen {
			continue
		}
		var w model.ReferralWithdrawal
		if err := tx.Select("id", "status").Where("id = ?", item.WithdrawalId).First(&w).Error; err != nil {
			return decimal.Zero, err
		}
		withdrawalStatus[item.WithdrawalId] = w.Status
	}
	var pendingItems, approvedItems []model.ReferralWithdrawalItem
	for _, item := range items {
		switch withdrawalStatus[item.WithdrawalId] {
		case model.ReferralWithdrawalStatusPending:
			pendingItems = append(pendingItems, item)
		case model.ReferralWithdrawalStatusApproved:
			approvedItems = append(approvedItems, item)
		}
	}
	remaining := amount
	deductedNow := decimal.Zero
	reducedBy := map[int]decimal.Decimal{}
	// Phase 1: shrink or cancel pending withdrawals first before deferring clawback onto approved withdrawals.
	for i := range pendingItems {
		if !remaining.IsPositive() {
			break
		}
		have := moneyDec(pendingItems[i].AllocatedAmount)
		if !have.IsPositive() {
			continue
		}
		take := decimal.Min(have, remaining)
		left := have.Sub(take)
		updates := map[string]any{"allocated_amount": moneyF(left)}
		if !left.IsPositive() {
			updates["status"] = model.ReferralWithdrawalItemStatusReleased
			updates["allocated_amount"] = 0
			updates["pending_clawback_amount"] = 0
		}
		if err := tx.Model(&model.ReferralWithdrawalItem{}).Where("id = ?", pendingItems[i].Id).Updates(updates).Error; err != nil {
			return decimal.Zero, err
		}
		if pendingItems[i].CommissionId > 0 && !left.IsPositive() {
			if err := syncCommissionStatusTx(tx, pendingItems[i].CommissionId); err != nil {
				return decimal.Zero, err
			}
		}
		reducedBy[pendingItems[i].WithdrawalId] = reducedBy[pendingItems[i].WithdrawalId].Add(take)
		deductedNow = deductedNow.Add(take)
		remaining = remaining.Sub(take)
	}
	// Phase 2: record any remaining shortfall on approved withdrawals awaiting payout.
	for i := range approvedItems {
		if !remaining.IsPositive() {
			break
		}
		have := moneyDec(approvedItems[i].AllocatedAmount)
		alreadyPending := moneyDec(approvedItems[i].PendingClawbackAmount)
		availToMark := have.Sub(alreadyPending)
		if !availToMark.IsPositive() {
			continue
		}
		mark := decimal.Min(availToMark, remaining)
		if err := tx.Model(&model.ReferralWithdrawalItem{}).Where("id = ?", approvedItems[i].Id).
			Update("pending_clawback_amount", moneyF(alreadyPending.Add(mark))).Error; err != nil {
			return decimal.Zero, err
		}
		remaining = remaining.Sub(mark)
	}
	for withdrawalId, reduced := range reducedBy {
		if err := reduceOrReleaseWithdrawalAfterClawbackTx(tx, withdrawalId, reduced); err != nil {
			return decimal.Zero, err
		}
	}
	return deductedNow, nil
}

func reduceOrReleaseWithdrawalAfterClawbackTx(tx *gorm.DB, withdrawalId int, reduced decimal.Decimal) error {
	if tx == nil || withdrawalId <= 0 || !reduced.IsPositive() {
		return nil
	}
	var withdrawal model.ReferralWithdrawal
	if err := model.AdtyLockForUpdate(tx).Where("id = ?", withdrawalId).First(&withdrawal).Error; err != nil {
		return err
	}
	if withdrawal.Status != model.ReferralWithdrawalStatusPending {
		return nil
	}
	occupied, err := withdrawalFrozenItemTotalTx(tx, withdrawalId)
	if err != nil {
		return err
	}
	amount := moneyDec(withdrawal.Amount).Sub(reduced)
	if amount.IsNegative() {
		amount = decimal.Zero
	}
	if occupied.LessThan(amount) {
		amount = occupied
	}
	updates := withdrawalAmountUpdates(&withdrawal, amount)
	netAfter := amount.Sub(moneyDec(withdrawal.FeeAmount))
	payoutZero := false
	if withdrawal.AccountType == model.ReferralWithdrawToAlipay {
		cnyStr, _ := updates["cny_amount"].(string)
		if cnyStr == "" {
			cnyStr = withdrawal.CNYAmount
		}
		cnyDec, parseErr := decimal.NewFromString(strings.TrimSpace(cnyStr))
		payoutZero = parseErr != nil || !cnyDec.IsPositive()
	} else if withdrawal.AccountType == model.ReferralWithdrawToUSDT {
		payoutZero = !netAfter.Round(6).IsPositive()
	}
	shouldCancel := !amount.IsPositive() ||
		(withdrawal.AccountType != model.ReferralWithdrawToBalance &&
			(!netAfter.IsPositive() || payoutZero))
	if shouldCancel {
		now := time.Now().Unix()
		updates["status"] = model.ReferralWithdrawalStatusCanceled
		updates["canceled_at"] = now
		updates["reject_reason"] = "commission refunded"
		if err := tx.Model(&model.ReferralWithdrawal{}).Where("id = ?", withdrawal.Id).Updates(updates).Error; err != nil {
			return err
		}
		if err := (&ReferralService{}).releaseWithdrawalItemsTx(tx, withdrawal.Id); err != nil {
			return err
		}
		if amount.IsPositive() {
			account, err := lockReferralAccountTx(tx, withdrawal.AffiliateId)
			if err != nil {
				return err
			}
			releaseAmt := decimal.Min(amount, moneyDec(account.FrozenAmount))
			if releaseAmt.IsPositive() {
				if err := AdjustAccountTx(tx, account, decimal.Zero, releaseAmt, releaseAmt.Neg(), decimal.Zero, model.ReferralCommissionLedger{
					WithdrawalId: withdrawal.Id, Type: "withdraw_release", RefType: "withdrawal", RefId: fmt.Sprint(withdrawal.Id),
					ExternalRefId: fmt.Sprintf("release:%d", withdrawal.Id), Remark: "commission refunded",
				}); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return tx.Model(&model.ReferralWithdrawal{}).Where("id = ?", withdrawal.Id).Updates(updates).Error
}

func referralWithdrawalHasPositivePayout(w *model.ReferralWithdrawal) bool {
	if w == nil || !moneyDec(w.NetAmount).IsPositive() {
		return false
	}
	switch w.AccountType {
	case model.ReferralWithdrawToAlipay:
		cny, err := decimal.NewFromString(strings.TrimSpace(w.CNYAmount))
		return err == nil && cny.IsPositive()
	case model.ReferralWithdrawToUSDT:
		return moneyDec(w.NetAmount).Round(6).IsPositive()
	default:
		return true
	}
}

func withdrawalAmountUpdates(withdrawal *model.ReferralWithdrawal, amount decimal.Decimal) map[string]any {
	if amount.IsNegative() {
		amount = decimal.Zero
	}
	net := amount.Sub(moneyDec(withdrawal.FeeAmount))
	if net.IsNegative() {
		net = decimal.Zero
	}
	updates := map[string]any{"amount": moneyF(amount), "net_amount": moneyF(net)}
	if rate, err := decimal.NewFromString(strings.TrimSpace(withdrawal.CNYRate)); err == nil && rate.IsPositive() {
		updates["cny_amount"] = amount.Mul(rate).Round(2).StringFixed(2)
	}
	return updates
}

func withdrawalFrozenItemTotalTx(tx *gorm.DB, withdrawalId int) (decimal.Decimal, error) {
	var items []model.ReferralWithdrawalItem
	if err := tx.Where("withdrawal_id = ? AND status = ?", withdrawalId, model.ReferralWithdrawalItemStatusFrozen).Find(&items).Error; err != nil {
		return decimal.Zero, err
	}
	total := decimal.Zero
	for _, item := range items {
		total = total.Add(moneyDec(item.AllocatedAmount))
	}
	if total.IsNegative() {
		return decimal.Zero, nil
	}
	return total, nil
}

// withdrawalMovableFrozenTx 是打款或驳回时还可以从这笔提现搬动的冻结额：
// 取当前冻结明细、提现单金额和账户实有冻结里的最小正数。
func withdrawalMovableFrozenTx(tx *gorm.DB, withdrawal *model.ReferralWithdrawal, account *model.ReferralCommissionAccount) (decimal.Decimal, error) {
	if withdrawal == nil {
		return decimal.Zero, nil
	}
	occupied, err := withdrawalFrozenItemTotalTx(tx, withdrawal.Id)
	if err != nil {
		return decimal.Zero, err
	}
	amount := moneyDec(withdrawal.Amount)
	if !occupied.IsPositive() {
		amount = decimal.Zero
	} else if occupied.LessThan(amount) {
		amount = occupied
	}
	if account != nil {
		frozen := moneyDec(account.FrozenAmount)
		if !frozen.IsPositive() {
			amount = decimal.Zero
		} else if frozen.LessThan(amount) {
			amount = frozen
		}
	}
	if amount.IsNegative() {
		return decimal.Zero, nil
	}
	return amount, nil
}

// lockWithdrawalCommissionRowsTx 先按佣金 id 锁住这笔提现占用的佣金。
// 退款扣回是先锁佣金，再锁相关提现，再锁账户；打款或驳回保持同一顺序，避免互相锁死。
func lockWithdrawalCommissionRowsTx(tx *gorm.DB, withdrawalId int) error {
	var ids []int
	if err := tx.Model(&model.ReferralWithdrawalItem{}).
		Where("withdrawal_id = ? AND commission_id > 0", withdrawalId).
		Order("commission_id").
		Pluck("commission_id", &ids).Error; err != nil {
		return err
	}
	seen := map[int]struct{}{}
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		var row model.ReferralCommission
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", id).First(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

// releaseWithdrawalTx 驳回或撤回：仍冻结在这笔提现里的金额回到可提现，明细释放。
// 账户搬动按当前冻结占用和账户实有冻结取小，不按已经冲回过的原申请额再搬一次。
func (s *ReferralService) releaseWithdrawalTx(tx *gorm.DB, withdrawal *model.ReferralWithdrawal, status string, actor int, reason string, now int64) error {
	if err := lockWithdrawalCommissionRowsTx(tx, withdrawal.Id); err != nil {
		return err
	}
	if err := model.AdtyLockForUpdate(tx).Where("id = ?", withdrawal.Id).First(withdrawal).Error; err != nil {
		return err
	}
	account, err := lockReferralAccountTx(tx, withdrawal.AffiliateId)
	if err != nil {
		return err
	}
	amount, err := withdrawalMovableFrozenTx(tx, withdrawal, account)
	if err != nil {
		return err
	}
	var frozenItems []model.ReferralWithdrawalItem
	if err := tx.Where("withdrawal_id = ? AND status = ?", withdrawal.Id, model.ReferralWithdrawalItemStatusFrozen).Order("id ASC").Find(&frozenItems).Error; err != nil {
		return err
	}
	var pendingClawItems []model.ReferralWithdrawalItem
	var pendingClawWeights []decimal.Decimal
	pendingClawbackTotal := decimal.Zero
	for _, item := range frozenItems {
		pendingClaw := moneyDec(item.PendingClawbackAmount)
		if !pendingClaw.IsPositive() {
			continue
		}
		pendingClawItems = append(pendingClawItems, item)
		pendingClawWeights = append(pendingClawWeights, pendingClaw)
		pendingClawbackTotal = pendingClawbackTotal.Add(pendingClaw)
	}
	updates := map[string]any{"status": status, "reject_reason": truncateText(reason, 1000)}
	if status == model.ReferralWithdrawalStatusCanceled {
		updates["canceled_at"], updates["canceled_by"] = now, actor
	} else {
		updates["rejected_at"], updates["rejected_by"] = now, actor
	}
	if err := tx.Model(&model.ReferralWithdrawal{}).Where("id = ?", withdrawal.Id).Updates(updates).Error; err != nil {
		return err
	}
	if err := s.releaseWithdrawalItemsTx(tx, withdrawal.Id); err != nil {
		return err
	}
	if !amount.IsPositive() {
		return nil
	}
	clawAmt := decimal.Min(amount, pendingClawbackTotal)
	releaseAmt := amount.Sub(clawAmt)
	if releaseAmt.IsPositive() {
		if err := AdjustAccountTx(tx, account, decimal.Zero, releaseAmt, releaseAmt.Neg(), decimal.Zero, model.ReferralCommissionLedger{
			WithdrawalId: withdrawal.Id, Type: "withdraw_release", RefType: "withdrawal", RefId: fmt.Sprint(withdrawal.Id),
			ExternalRefId: fmt.Sprintf("release:%d", withdrawal.Id), Remark: status,
		}); err != nil {
			return err
		}
	}
	if clawAmt.IsPositive() {
		freshAccount, lockErr := lockReferralAccountTx(tx, withdrawal.AffiliateId)
		if lockErr != nil {
			return lockErr
		}
		deduct := decimal.Min(clawAmt, moneyDec(freshAccount.FrozenAmount))
		if deduct.IsPositive() && len(pendingClawItems) > 0 {
			shares := splitDecimalByWeights(deduct, pendingClawWeights)
			for idx, item := range pendingClawItems {
				share := shares[idx]
				if !share.IsPositive() {
					continue
				}
				if item.CommissionId > 0 {
					var comm model.ReferralCommission
					if err := tx.Where("id = ?", item.CommissionId).First(&comm).Error; err != nil {
						return err
					}
					nextBook := decimal.Max(moneyDec(comm.CommissionAmount).Sub(share), decimal.Zero)
					if err := tx.Model(&model.ReferralCommission{}).Where("id = ?", comm.Id).Update("commission_amount", moneyF(nextBook)).Error; err != nil {
						return err
					}
					if err := syncCommissionStatusTx(tx, comm.Id); err != nil {
						return err
					}
				}
				freshAccount, lockErr = lockReferralAccountTx(tx, withdrawal.AffiliateId)
				if lockErr != nil {
					return lockErr
				}
				if err := AdjustAccountTx(tx, freshAccount, decimal.Zero, decimal.Zero, share.Neg(), decimal.Zero, model.ReferralCommissionLedger{
					CommissionId: item.CommissionId, WithdrawalId: withdrawal.Id, Type: "withdraw_reject_clawback", RefType: "withdrawal", RefId: fmt.Sprint(withdrawal.Id),
					ExternalRefId: fmt.Sprintf("withdraw-reject-claw:%d:%d", withdrawal.Id, item.Id), Remark: "pending refund clawback settled on withdrawal release",
				}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ReviewWithdrawal 审核通过或驳回（驳回会解冻并发邮件）。
func (s *ReferralService) ReviewWithdrawal(withdrawalId, adminId int, approve bool, reason, ip, ua string) error {
	return s.ReviewWithdrawalForActor(withdrawalId, adminId, 0, false, approve, reason, ip, ua)
}

func (s *ReferralService) ReviewWithdrawalForActor(withdrawalId, adminId, actorRole int, denyAdminTarget, approve bool, reason, ip, ua string) error {
	now := time.Now().Unix()
	var notifyUser int
	var withdrawalCopy model.ReferralWithdrawal
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var peek model.ReferralWithdrawal
		if err := tx.Where("id = ?", withdrawalId).First(&peek).Error; err != nil {
			return err
		}
		// R3-08: Lock target user BEFORE referral commission/withdrawal/account rows.
		if err := CheckReferralAdminTargetUserTx(tx, peek.UserId, adminId, actorRole, denyAdminTarget); err != nil {
			return err
		}
		if err := lockWithdrawalCommissionRowsTx(tx, peek.Id); err != nil {
			return err
		}
		var withdrawal model.ReferralWithdrawal
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", withdrawalId).First(&withdrawal).Error; err != nil {
			return err
		}
		if withdrawal.UserId != peek.UserId {
			return common.NewMessage("Withdrawal state changed concurrently, please retry")
		}
		if approve {
			if err := model.RejectRestrictedAdtyFundWrite(tx, model.AdtyFundWrite{SiteID: 0, AccountID: withdrawal.UserId}); err != nil {
				return err
			}
			if withdrawal.Status != model.ReferralWithdrawalStatusPending {
				return common.NewMessage("Only pending withdrawals can be reviewed")
			}
			if !referralWithdrawalHasPositivePayout(&withdrawal) {
				return common.NewMessage("Withdrawal net amount is no longer positive; reject or cancel instead")
			}
			if err := tx.Model(&model.ReferralWithdrawal{}).Where("id = ?", withdrawal.Id).Updates(map[string]any{
				"status": model.ReferralWithdrawalStatusApproved, "approved_at": now, "reviewed_by": adminId,
			}).Error; err != nil {
				return err
			}
			return recordReferralAuditTx(tx, "withdrawal_approve", withdrawal.UserId, withdrawal.AffiliateId, adminId, reason, ip, ua, nil, map[string]any{"id": withdrawal.Id})
		}
		if withdrawal.Status != model.ReferralWithdrawalStatusPending && withdrawal.Status != model.ReferralWithdrawalStatusApproved {
			return common.NewMessage("Only pending or approved withdrawals can be rejected")
		}
		if err := s.releaseWithdrawalTx(tx, &withdrawal, model.ReferralWithdrawalStatusRejected, adminId, reason, now); err != nil {
			return err
		}
		notifyUser = withdrawal.UserId
		withdrawalCopy = withdrawal
		return recordReferralAuditTx(tx, "withdrawal_reject", withdrawal.UserId, withdrawal.AffiliateId, adminId, reason, ip, ua, nil, map[string]any{"id": withdrawal.Id})
	})
	if err == nil && notifyUser > 0 {
		sendReferralWithdrawalEmail(notifyUser, &withdrawalCopy, false, reason)
	}
	return err
}

// MarkWithdrawalPaid 标记已打款：冻结转已提现，明细标 withdrawn，佣金推到 paid；USDT 要填交易哈希。
func (s *ReferralService) MarkWithdrawalPaid(withdrawalId, adminId int, txnNo, proofURL, note, ip, ua string) error {
	return s.MarkWithdrawalPaidForActor(withdrawalId, adminId, 0, false, txnNo, proofURL, note, ip, ua)
}

func (s *ReferralService) MarkWithdrawalPaidForActor(withdrawalId, adminId, actorRole int, denyAdminTarget bool, txnNo, proofURL, note, ip, ua string) error {
	now := time.Now().Unix()
	var withdrawalCopy model.ReferralWithdrawal
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var peek model.ReferralWithdrawal
		if err := tx.Where("id = ?", withdrawalId).First(&peek).Error; err != nil {
			return err
		}
		cleanTxn := strings.TrimSpace(txnNo)
		if peek.AccountType == model.ReferralWithdrawToUSDT && cleanTxn == "" {
			return common.NewMessage("Enter the transaction hash")
		}
		cleanProof := strings.TrimSpace(proofURL)
		if cleanProof != "" {
			if !strings.HasPrefix(cleanProof, "/api/referral-assets/") {
				return common.NewMessage("Invalid parameters")
			}
			canonicalProof, ok := IsOwnedReferralAssetTx(tx, 0, model.ReferralAssetPurposePaymentProof, cleanProof)
			if !ok {
				return common.NewMessage("Invalid parameters")
			}
			cleanProof = canonicalProof
		}
		if peek.AccountType == model.ReferralWithdrawToAlipay && cleanTxn == "" && cleanProof == "" {
			return common.NewMessage("Enter the transfer number or upload payment proof")
		}
		// R3-08: Lock target user BEFORE referral commission/withdrawal/account rows.
		if err := CheckReferralAdminTargetUserTx(tx, peek.UserId, adminId, actorRole, denyAdminTarget); err != nil {
			return err
		}
		if err := lockWithdrawalCommissionRowsTx(tx, peek.Id); err != nil {
			return err
		}
		var withdrawal model.ReferralWithdrawal
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", withdrawalId).First(&withdrawal).Error; err != nil {
			return err
		}
		if withdrawal.UserId != peek.UserId {
			return common.NewMessage("Withdrawal state changed concurrently, please retry")
		}
		if withdrawal.Status != model.ReferralWithdrawalStatusApproved {
			return common.NewMessage("This withdrawal cannot be marked as paid")
		}
		if !referralWithdrawalHasPositivePayout(&withdrawal) {
			return common.NewMessage("Withdrawal net amount is no longer positive; reject or cancel instead")
		}
		account, err := lockReferralAccountTx(tx, withdrawal.AffiliateId)
		if err != nil {
			return err
		}
		if err := model.RejectRestrictedAdtyFundWrite(tx, model.AdtyFundWrite{SiteID: 0, AccountID: withdrawal.UserId}); err != nil {
			return err
		}
		var receiptKey *string
		if withdrawal.AccountType == model.ReferralWithdrawToUSDT || withdrawal.AccountType == model.ReferralWithdrawToAlipay {
			reference := cleanTxn
			if reference == "" {
				reference = cleanProof
			}
			if reference != "" {
				rk := fmt.Sprintf("%x", sha256.Sum256([]byte(withdrawal.AccountType+"|"+withdrawal.AccountNetwork+"|"+reference)))
				var dupCount int64
				if err := tx.Model(&model.ReferralWithdrawal{}).Where("receipt_key = ? AND id <> ?", rk, withdrawal.Id).Count(&dupCount).Error; err != nil {
					return err
				}
				if dupCount > 0 {
					return common.NewMessage("This payout reference was already used for another withdrawal")
				}
				receiptKey = &rk
			}
		}
		var frozenItems []model.ReferralWithdrawalItem
		if err := tx.Where("withdrawal_id = ? AND status = ?", withdrawal.Id, model.ReferralWithdrawalItemStatusFrozen).Find(&frozenItems).Error; err != nil {
			return err
		}
		for _, item := range frozenItems {
			pendingClaw := moneyDec(item.PendingClawbackAmount)
			if !pendingClaw.IsPositive() || item.CommissionId <= 0 {
				continue
			}
			var comm model.ReferralCommission
			if err := tx.Where("id = ?", item.CommissionId).First(&comm).Error; err != nil {
				return err
			}
			extRecovered, _, err := referralCommissionRecoveriesByKindTx(tx, comm.Id)
			if err != nil {
				return err
			}
			if err := recordReferralCommissionRecoveryByKindTx(tx, &comm, adtyReferralCommissionRecoveryKind, extRecovered.Add(pendingClaw), comm.SourceTradeNo); err != nil {
				return err
			}
		}
		amount, err := withdrawalMovableFrozenTx(tx, &withdrawal, account)
		if err != nil {
			return err
		}
		updates := map[string]any{
			"status": model.ReferralWithdrawalStatusPaid, "paid_at": now, "payment_txn_no": cleanTxn,
			"payment_proof_url": cleanProof, "admin_note": truncateText(note, 1000), "paid_by": adminId,
		}
		if receiptKey != nil {
			updates["receipt_key"] = receiptKey
		}
		if err := tx.Model(&model.ReferralWithdrawal{}).Where("id = ?", withdrawal.Id).Updates(updates).Error; err != nil {
			if isAdtyUniqueConflictError(err) {
				return common.NewMessage("This payout reference was already used for another withdrawal")
			}
			return err
		}
		if err := s.markWithdrawalItemsPaidTx(tx, withdrawal.Id); err != nil {
			return err
		}
		if amount.IsPositive() {
			if err := AdjustAccountTx(tx, account, decimal.Zero, decimal.Zero, amount.Neg(), amount, model.ReferralCommissionLedger{
				WithdrawalId: withdrawal.Id, Type: "withdraw_paid", RefType: "withdrawal", RefId: fmt.Sprint(withdrawal.Id),
				ExternalRefId: fmt.Sprintf("paid:%d", withdrawal.Id), Remark: cleanTxn, Operator: fmt.Sprint(adminId),
			}); err != nil {
				return err
			}
		}
		withdrawalCopy = withdrawal
		withdrawalCopy.PaymentTxnNo = cleanTxn
		withdrawalCopy.PaymentProofURL = cleanProof
		return recordReferralAuditTx(tx, "withdrawal_pay", withdrawal.UserId, withdrawal.AffiliateId, adminId, note, ip, ua, nil, map[string]any{"id": withdrawal.Id, "txn": cleanTxn, "proof_url": cleanProof})
	})
	if err == nil {
		sendReferralWithdrawalEmail(withdrawalCopy.UserId, &withdrawalCopy, true, "")
	}
	return err
}

func renderReferralWithdrawalMail(site *model.AdtySite, withdrawal *model.ReferralWithdrawal, paid bool, reason string) (subject, body string) {
	scale := int32(2)
	if withdrawal != nil && withdrawal.AccountType == model.ReferralWithdrawToUSDT {
		scale = 6
	}
	amount := "$" + strings.TrimRight(strings.TrimRight(moneyDec(withdrawal.Amount).StringFixed(scale), "0"), ".")
	if withdrawal == nil || withdrawal.AccountType != model.ReferralWithdrawToUSDT {
		amount = "$" + moneyDec(withdrawal.Amount).StringFixed(2)
	}
	var message AdtyMailMessage
	if paid {
		message.Title = "Your withdrawal of " + amount + " has been paid"
		message.Intro = "Your commission withdrawal of " + amount + " has been paid."
		message.Paragraphs = []string{"Reference: " + withdrawal.PaymentTxnNo}
	} else {
		message.Title = "Your withdrawal of " + amount + " was rejected"
		message.Intro = "Your commission withdrawal of " + amount + " was rejected. The amount is available again."
		message.Paragraphs = []string{"Reason: " + reason}
	}
	return message.Title, AdtyRenderMail(site, message)
}

// sendReferralWithdrawalEmail 给用户发打款或驳回邮件。失败只记日志。
var sendReferralWithdrawalEmail = func(userId int, withdrawal *model.ReferralWithdrawal, paid bool, reason string) {
	user, err := model.GetUserById(userId, false)
	if err != nil || strings.TrimSpace(user.Email) == "" {
		return
	}
	go func() {
		// 二开：发件人按用户所属站点（任务 11.3）
		site := AdtySiteById(user.SiteId)
		subject, body := renderReferralWithdrawalMail(site, withdrawal, paid, reason)
		if err := AdtySendSiteEmail(site, subject, user.Email, body); err != nil {
			common.SysError(common.LogText("referral withdrawal email failed user=%d: %s", userId, err.Error()))
		}
	}()
}

// allocateWithdrawalItemsTx 按可提现的佣金顺序冻结明细；后台调整出来的余额没有对应佣金，记一条 commission_id=0 的明细。
func (s *ReferralService) allocateWithdrawalItemsTx(tx *gorm.DB, affiliateId, withdrawalId int, amount decimal.Decimal) error {
	remaining := amount
	var commissions []model.ReferralCommission
	if err := model.AdtyLockForUpdate(tx).Where("affiliate_id = ? AND status IN ?", affiliateId,
		[]string{model.ReferralCommissionStatusAvailable, model.ReferralCommissionStatusFrozen}).
		Order("available_at, id").Find(&commissions).Error; err != nil {
		return err
	}
	for _, commission := range commissions {
		if !remaining.IsPositive() {
			break
		}
		frozen, withdrawn, err := commissionAllocatedTx(tx, commission.Id)
		if err != nil {
			return err
		}
		free := moneyDec(commission.CommissionAmount).Sub(frozen).Sub(withdrawn)
		if !free.IsPositive() {
			continue
		}
		use := decimal.Min(free, remaining)
		if err := tx.Create(&model.ReferralWithdrawalItem{WithdrawalId: withdrawalId, CommissionId: commission.Id, AllocatedAmount: moneyF(use), Status: model.ReferralWithdrawalItemStatusFrozen}).Error; err != nil {
			return err
		}
		if err := syncCommissionStatusTx(tx, commission.Id); err != nil {
			return err
		}
		remaining = remaining.Sub(use)
	}
	if remaining.IsPositive() {
		return tx.Create(&model.ReferralWithdrawalItem{WithdrawalId: withdrawalId, AllocatedAmount: moneyF(remaining), Status: model.ReferralWithdrawalItemStatusFrozen}).Error
	}
	return nil
}

func (s *ReferralService) releaseWithdrawalItemsTx(tx *gorm.DB, withdrawalId int) error {
	return s.updateWithdrawalItemsTx(tx, withdrawalId, model.ReferralWithdrawalItemStatusReleased)
}

func (s *ReferralService) markWithdrawalItemsPaidTx(tx *gorm.DB, withdrawalId int) error {
	return s.updateWithdrawalItemsTx(tx, withdrawalId, model.ReferralWithdrawalItemStatusWithdrawn)
}

func (s *ReferralService) updateWithdrawalItemsTx(tx *gorm.DB, withdrawalId int, status string) error {
	var items []model.ReferralWithdrawalItem
	if err := tx.Where("withdrawal_id = ? AND status = ?", withdrawalId, model.ReferralWithdrawalItemStatusFrozen).Find(&items).Error; err != nil {
		return err
	}
	for _, item := range items {
		updates := map[string]any{"status": status, "pending_clawback_amount": 0}
		if status == model.ReferralWithdrawalItemStatusReleased {
			updates["allocated_amount"] = 0
		}
		if err := tx.Model(&model.ReferralWithdrawalItem{}).Where("id = ?", item.Id).Updates(updates).Error; err != nil {
			return err
		}
		if item.CommissionId > 0 {
			if err := syncCommissionStatusTx(tx, item.CommissionId); err != nil {
				return err
			}
		}
	}
	return nil
}

type referralConvertedWithdrawalItem struct {
	WithdrawalId    int
	AllocatedAmount decimal.Decimal
}

func commissionAllocatedByChannelTx(tx *gorm.DB, commissionId int) (frozen, converted, externalWithdrawn decimal.Decimal, balanceItems []referralConvertedWithdrawalItem, err error) {
	var items []struct {
		WithdrawalId          int
		AllocatedAmount       float64
		PendingClawbackAmount float64
		Status                string
		AccountType           string
	}
	err = tx.Table("referral_withdrawal_items AS i").
		Select("i.withdrawal_id, i.allocated_amount, i.pending_clawback_amount, i.status, COALESCE(w.account_type, '') AS account_type").
		Joins("LEFT JOIN referral_withdrawals AS w ON w.id = i.withdrawal_id").
		Where("i.commission_id = ?", commissionId).
		Order("i.id ASC").
		Scan(&items).Error
	if err != nil {
		return
	}
	for _, item := range items {
		amt := moneyDec(item.AllocatedAmount)
		switch item.Status {
		case model.ReferralWithdrawalItemStatusFrozen:
			frozen = frozen.Add(decimal.Max(amt.Sub(moneyDec(item.PendingClawbackAmount)), decimal.Zero))
		case model.ReferralWithdrawalItemStatusWithdrawn:
			if item.AccountType == model.ReferralWithdrawToBalance {
				converted = converted.Add(amt)
				if amt.IsPositive() {
					balanceItems = append(balanceItems, referralConvertedWithdrawalItem{
						WithdrawalId:    item.WithdrawalId,
						AllocatedAmount: amt,
					})
				}
			} else {
				externalWithdrawn = externalWithdrawn.Add(amt)
			}
		}
	}
	return
}

func commissionAllocatedTx(tx *gorm.DB, commissionId int) (frozen, withdrawn decimal.Decimal, err error) {
	var items []model.ReferralWithdrawalItem
	if err = tx.Where("commission_id = ?", commissionId).Find(&items).Error; err != nil {
		return
	}
	for _, item := range items {
		switch item.Status {
		case model.ReferralWithdrawalItemStatusFrozen:
			frozen = frozen.Add(moneyDec(item.AllocatedAmount))
		case model.ReferralWithdrawalItemStatusWithdrawn:
			withdrawn = withdrawn.Add(moneyDec(item.AllocatedAmount))
		}
	}
	return
}

// syncCommissionStatusTx 按提现明细推佣金状态。pending、void、clawed_back 不动，否则提现释放或打款时会把它们改回去。
func syncCommissionStatusTx(tx *gorm.DB, commissionId int) error {
	var commission model.ReferralCommission
	if err := tx.Where("id = ?", commissionId).First(&commission).Error; err != nil {
		return err
	}
	switch commission.Status {
	case model.ReferralCommissionStatusPending, model.ReferralCommissionStatusVoid, model.ReferralCommissionStatusClawedBack:
		return nil
	}
	frozen, withdrawn, err := commissionAllocatedTx(tx, commissionId)
	if err != nil {
		return err
	}
	status := model.ReferralCommissionStatusAvailable
	switch {
	case frozen.IsPositive():
		status = model.ReferralCommissionStatusFrozen
	case withdrawn.GreaterThanOrEqual(moneyDec(commission.CommissionAmount)):
		status = model.ReferralCommissionStatusPaid
	}
	frozenAt := commission.FrozenAt
	if status == model.ReferralCommissionStatusFrozen && frozenAt == 0 {
		frozenAt = time.Now().Unix()
	}
	if status != model.ReferralCommissionStatusFrozen {
		frozenAt = 0
	}
	return tx.Model(&model.ReferralCommission{}).Where("id = ?", commissionId).Updates(map[string]any{"status": status, "frozen_at": frozenAt}).Error
}

// referralProgramCanStayOn 用数据库里的当前值加上这次要写的字段，判断开着的时候是否还有默认比例。
func referralProgramCanStayOn(tx *gorm.DB, values map[string]string) error {
	rate, _, err := model.ReadOptionForUpdate(tx, "adty_referral_setting.default_rate")
	if err != nil {
		return err
	}
	enabled, _, err := model.ReadOptionForUpdate(tx, "adty_referral_setting.enabled")
	if err != nil {
		return err
	}
	if incoming, ok := values["adty_referral_setting.default_rate"]; ok {
		rate = incoming
	}
	if incoming, ok := values["adty_referral_setting.enabled"]; ok {
		enabled = incoming
	}
	rate = strings.TrimSpace(rate)
	if enabled == "true" && (rate == "" || rate == "null") {
		return common.NewMessage("Set the default commission rate before turning the referral program on")
	}
	return nil
}

// classifyWithdrawCNYRate 只检查汇率，不写库。空串和 0 表示这次不改汇率。
func classifyWithdrawCNYRate(raw string) (rate decimal.Decimal, write bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return decimal.Zero, false, nil
	}
	rate, err = common.ParseBoundedDecimalString(raw, 6, 6)
	if err != nil {
		return decimal.Zero, false, common.NewMessage("The exchange rate must be a number")
	}
	if rate.IsZero() {
		return decimal.Zero, false, nil
	}
	if !rate.IsPositive() {
		return decimal.Zero, false, common.NewMessage("The exchange rate must be greater than 0")
	}
	return rate, true, nil
}

// persistWithdrawCNYRateTx 在调用方事务里写汇率和审计。和库里相同则什么都不写。
func (s *ReferralService) persistWithdrawCNYRateTx(tx *gorm.DB, rate decimal.Decimal, adminId int, ip, ua string) (map[string]string, error) {
	old, _, err := model.ReadOptionForUpdate(tx, adty_setting.ReferralKeyWithdrawCNYRate)
	if err != nil {
		return nil, err
	}
	if oldRate, parseErr := decimal.NewFromString(strings.TrimSpace(old)); parseErr == nil && oldRate.Equal(rate) {
		return nil, nil
	}
	now := time.Now().Unix()
	values := map[string]string{
		adty_setting.ReferralKeyWithdrawCNYRate:          rate.String(),
		adty_setting.ReferralKeyWithdrawCNYRateUpdatedAt: fmt.Sprint(now),
	}
	if err := model.SaveOptionsTx(tx, values); err != nil {
		return nil, err
	}
	if err := tx.Create(&model.ReferralAdminAuditLog{
		Action: "withdraw_cny_rate", AdminUserId: adminId, Ip: ip, UserAgent: truncateText(ua, 512),
		OldValue: old, NewValue: rate.String(),
	}).Error; err != nil {
		return nil, err
	}
	return values, nil
}

// UpdateWithdrawCNYRate 改提现汇率。汇率和审计同一个事务，提交后才改内存。为空、为 0 或和库里相同都不写。
func (s *ReferralService) UpdateWithdrawCNYRate(raw string, adminId int, ip, ua string) error {
	rate, write, err := classifyWithdrawCNYRate(raw)
	if err != nil || !write {
		return err
	}
	var written map[string]string
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var txErr error
		written, txErr = s.persistWithdrawCNYRateTx(tx, rate, adminId, ip, ua)
		return txErr
	})
	if err != nil {
		return err
	}
	return model.ApplyOptionValues(written)
}

// SaveReferralSettings 一次提交推广设置和提现汇率。汇率不合法或后半段写库失败时，设置也不落库、内存也不改。
func (s *ReferralService) SaveReferralSettings(values map[string]string, rawRate string, adminId int, ip, ua string) error {
	rate, writeRate, err := classifyWithdrawCNYRate(rawRate)
	if err != nil {
		return err
	}
	written := map[string]string{}
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		// Pre-lock all referral setting keys touched or read in sorted order at
		// transaction start so concurrent setting writes cannot deadlock.
		preLockSet := map[string]struct{}{
			"adty_referral_setting.enabled":              {},
			"adty_referral_setting.default_rate":         {},
			"adty_referral_setting.min_withdraw_usd":     {},
			"adty_referral_setting.usdt_withdraw_fee":    {},
			adty_setting.ReferralKeyWithdrawCNYRate:          {},
			adty_setting.ReferralKeyWithdrawCNYRateUpdatedAt: {},
			adty_setting.ReferralKeyWithdrawMethods:      {},
			adty_setting.ReferralKeyWithdrawUSDTNetworks: {},
		}
		for key := range values {
			preLockSet[key] = struct{}{}
		}
		allLockKeys := make([]string, 0, len(preLockSet))
		for key := range preLockSet {
			allLockKeys = append(allLockKeys, key)
		}
		slices.Sort(allLockKeys)
		oldValues := map[string]string{}
		for _, key := range allLockKeys {
			raw, ok, readErr := model.ReadOptionForUpdate(tx, key)
			if readErr != nil {
				return readErr
			}
			if _, inPatch := values[key]; inPatch && ok {
				oldValues[key] = raw
			}
		}
		if err := referralProgramCanStayOn(tx, values); err != nil {
			return err
		}
		if err := validateReferralWithdrawSettingsTx(tx, values, writeRate); err != nil {
			return err
		}
		if err := model.SaveOptionsTx(tx, values); err != nil {
			return err
		}
		for key, value := range values {
			written[key] = value
		}
		if len(values) > 0 {
			oldJSON, _ := common.Marshal(oldValues)
			newJSON, _ := common.Marshal(values)
			if err := tx.Create(&model.ReferralAdminAuditLog{
				Action:      "settings_update",
				AdminUserId: adminId,
				Ip:          ip,
				UserAgent:   truncateText(ua, 512),
				OldValue:    string(oldJSON),
				NewValue:    string(newJSON),
			}).Error; err != nil {
				return err
			}
		}
		if !writeRate {
			return nil
		}
		extra, err := s.persistWithdrawCNYRateTx(tx, rate, adminId, ip, ua)
		if err != nil {
			return err
		}
		for key, value := range extra {
			written[key] = value
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := model.ApplyOptionValues(written); err != nil {
		return err
	}
	return refreshReferralGateOptions()
}

// validateReferralWithdrawSettingsTx 校验主站外部提现方式及对应参数的完整性（USDT 与支付宝互相解耦）。
func validateReferralWithdrawSettingsTx(tx *gorm.DB, values map[string]string, writeRate bool) error {
	const (
		minKey = "adty_referral_setting.min_withdraw_usd"
		feeKey = "adty_referral_setting.usdt_withdraw_fee"
	)
	touched := writeRate
	for _, key := range []string{
		minKey,
		feeKey,
		adty_setting.ReferralKeyWithdrawCNYRate,
		adty_setting.ReferralKeyWithdrawMethods,
		adty_setting.ReferralKeyWithdrawUSDTNetworks,
	} {
		if _, ok := values[key]; ok {
			touched = true
			break
		}
	}
	if !touched {
		return nil
	}
	readOpt := func(key string) (string, bool, error) {
		if raw, ok := values[key]; ok {
			return raw, true, nil
		}
		return model.ReadOptionForUpdate(tx, key)
	}
	if rawNets, ok := values[adty_setting.ReferralKeyWithdrawUSDTNetworks]; ok {
		trimmed := strings.TrimSpace(rawNets)
		if trimmed != "" && !strings.EqualFold(trimmed, "null") {
			var parsed []string
			if err := common.UnmarshalJsonStr(trimmed, &parsed); err != nil {
				return common.NewMessage("Invalid parameters")
			}
			norm, valid := adty_setting.NormalizeReferralWithdrawUSDTNetworks(parsed)
			if !valid {
				return common.NewMessage("Invalid parameters")
			}
			encoded, err := common.Marshal(norm)
			if err != nil {
				return err
			}
			values[adty_setting.ReferralKeyWithdrawUSDTNetworks] = string(encoded)
		}
	}
	rawMethods, hasMethodsOpt, err := readOpt(adty_setting.ReferralKeyWithdrawMethods)
	if err != nil {
		return err
	}
	trimmedMethods := strings.TrimSpace(rawMethods)
	if !hasMethodsOpt || trimmedMethods == "" || strings.EqualFold(trimmedMethods, "null") {
		return nil
	}
	var parsedMethods []string
	if err := common.UnmarshalJsonStr(trimmedMethods, &parsedMethods); err != nil {
		return common.NewMessage("Unsupported withdrawal method")
	}
	methods, valid := adty_setting.NormalizeReferralWithdrawMethods(parsedMethods)
	if !valid {
		return common.NewMessage("Unsupported withdrawal method")
	}
	if _, ok := values[adty_setting.ReferralKeyWithdrawMethods]; ok {
		encoded, err := common.Marshal(methods)
		if err != nil {
			return err
		}
		values[adty_setting.ReferralKeyWithdrawMethods] = string(encoded)
	}
	hasSetDecimal := func(key string) (bool, error) {
		raw, ok, err := readOpt(key)
		if err != nil {
			return false, err
		}
		trimmed := strings.TrimSpace(raw)
		if !ok || trimmed == "" || strings.EqualFold(trimmed, "null") {
			return false, nil
		}
		val, err := common.ParseBoundedDecimalString(trimmed, 12, 8)
		if err != nil || val.IsNegative() {
			return false, common.NewMessage("Invalid parameters")
		}
		return true, nil
	}
	minSet, err := hasSetDecimal(minKey)
	if err != nil {
		return err
	}
	if slices.Contains(methods, adty_setting.ReferralWithdrawMethodUSDT) {
		feeSet, err := hasSetDecimal(feeKey)
		if err != nil {
			return err
		}
		if !minSet || !feeSet {
			return common.NewMessage("Set the minimum withdrawal and USDT fee before enabling USDT withdrawals")
		}
		rawNets, hasNetsOpt, err := readOpt(adty_setting.ReferralKeyWithdrawUSDTNetworks)
		if err != nil {
			return err
		}
		trimmedNets := strings.TrimSpace(rawNets)
		if !hasNetsOpt || trimmedNets == "" || strings.EqualFold(trimmedNets, "null") {
			return common.NewMessage("Select at least one USDT network")
		}
		var parsedNets []string
		if err := common.UnmarshalJsonStr(trimmedNets, &parsedNets); err != nil {
			return common.NewMessage("Invalid parameters")
		}
		nets, valid := adty_setting.NormalizeReferralWithdrawUSDTNetworks(parsedNets)
		if !valid || len(nets) == 0 {
			return common.NewMessage("Select at least one USDT network")
		}
	}
	if slices.Contains(methods, adty_setting.ReferralWithdrawMethodAlipay) {
		rateSet := writeRate
		if !rateSet {
			rawRateOpt, ok, err := readOpt(adty_setting.ReferralKeyWithdrawCNYRate)
			if err != nil {
				return err
			}
			if ok {
				if parsedRate, parseErr := decimal.NewFromString(strings.TrimSpace(rawRateOpt)); parseErr == nil && parsedRate.IsPositive() {
					rateSet = true
				}
			}
		}
		if !minSet || !rateSet {
			return common.NewMessage("Set the minimum withdrawal and CNY exchange rate before enabling Alipay withdrawals")
		}
	}
	return nil
}

// refreshReferralGateOptions 把库里的默认比例和开关写回本进程，避免别的进程刚改过、这里还是旧值。
func refreshReferralGateOptions() error {
	values := map[string]string{}
	for _, key := range []string{"adty_referral_setting.default_rate", "adty_referral_setting.enabled"} {
		value, ok, err := model.ReadOptionFromDB(key)
		if err != nil {
			return err
		}
		if ok {
			values[key] = value
		}
	}
	return model.ApplyOptionValues(values)
}
