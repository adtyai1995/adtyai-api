package service

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 计佣、结算、退款作废和扣回（任务 6.3）。

// adtyReferralSnapshot 是下单时的返佣快照：用户有绑定、推广员有效、绑定没有自邀标记、比例已设时，
// 返回推广员和当时的比例。之后改比例不影响已经下的单。
// 付款人或推广员开过分站（SiteOwnerSince > 0）、或者下单的是分站客户，就不计佣；开站前下的单照常。
func adtyReferralSnapshotWithDB(db *gorm.DB, userId int, lockUser bool) (int, decimal.Decimal, error) {
	if !Referral.IsEnabled() {
		return 0, decimal.Zero, nil
	}
	// Bindings are immutable after registration. Discover both identities before
	// locking users, then acquire their rows in a stable order shared by orders.
	var bindings []model.ReferralBinding
	if err := db.Where("invitee_user_id = ?", userId).Limit(1).Find(&bindings).Error; err != nil {
		return 0, decimal.Zero, err
	}
	if len(bindings) == 0 || bindings[0].RiskFlag == model.ReferralRiskSelfInvite {
		return 0, decimal.Zero, nil
	}
	binding := bindings[0]
	var affiliate model.ReferralAffiliate
	if err := db.Where("id = ?", binding.AffiliateId).First(&affiliate).Error; err != nil {
		return 0, decimal.Zero, err
	}
	if userId == affiliate.UserId {
		return 0, decimal.Zero, nil
	}
	userIDs := []int{userId, affiliate.UserId}
	slices.Sort(userIDs)
	for _, id := range userIDs {
		var user model.User
		userQuery := db.Select("id", "site_id", "status", "role").Where("id = ?", id)
		if lockUser {
			userQuery = model.AdtyLockForUpdate(userQuery)
		}
		if err := userQuery.First(&user).Error; err != nil {
			if id == affiliate.UserId && errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, decimal.Zero, nil
			}
			return 0, decimal.Zero, err
		}
		if user.SiteId > 0 || user.Role >= common.RoleAdminUser {
			return 0, decimal.Zero, nil
		}
		if id == affiliate.UserId && user.Status != common.UserStatusEnabled {
			return 0, decimal.Zero, nil
		}
	}
	// 分站客户不计 P6 佣金（执行层决定 27）。读库不读缓存。
	// 站长仍是主站用户（site_id = 0）；开站时写入的历史标记也不能在关站后恢复计佣。
	var siteOwners int64
	if err := db.Model(&model.ReferralAffiliate{}).Where("user_id = ? AND site_owner_since > 0", userId).Count(&siteOwners).Error; err != nil {
		return 0, decimal.Zero, err
	}
	if siteOwners > 0 {
		return 0, decimal.Zero, nil
	}
	// Read again after both user locks: a site opening may have committed while
	// the order waited. Order creation uses READ COMMITTED on MySQL.
	if err := db.Where("id = ?", binding.AffiliateId).First(&affiliate).Error; err != nil {
		return 0, decimal.Zero, err
	}
	if affiliate.Status != model.ReferralAffiliateStatusApproved || affiliate.SiteOwnerSince > 0 {
		return 0, decimal.Zero, nil
	}
	rate, ok := AffiliateRate(&affiliate)
	if !ok {
		return 0, decimal.Zero, nil
	}
	return affiliate.Id, rate, nil
}

func adtyReferralSnapshot(userId int) (int, decimal.Decimal) {
	affiliateID, rate, err := adtyReferralSnapshotWithDB(model.DB, userId, false)
	if err != nil {
		common.SysError(common.LogText("referral snapshot failed user=%d: %s", userId, err.Error()))
		return 0, decimal.Zero
	}
	return affiliateID, rate
}

func prepareAdtyReferralSnapshotTx(tx *gorm.DB, topUp *model.TopUp, ext *model.AdtyTopupOrder) error {
	// R1-095: Owner business and team top-ups do not generate main-site referral commissions.
	if ext.IsOwnerBusiness() || ext.TeamId > 0 || ext.Business == model.AdtyBusinessTeamTopup {
		ext.ReferralAffiliateId, ext.ReferralRate = 0, decimal.Zero
		return nil
	}
	// Subsite orders never accrue main-site referral commissions; skipping
	// adtyReferralSnapshotWithDB avoids locking User before AdtySite (R12-014).
	if ext.SiteId > 0 {
		ext.ReferralAffiliateId, ext.ReferralRate = 0, decimal.Zero
		return PrepareAdtySiteInviteCommissionSnapshotTx(tx, topUp, ext)
	}
	affiliateID, rate, err := adtyReferralSnapshotWithDB(tx, topUp.UserId, true)
	if err != nil {
		return err
	}
	ext.ReferralAffiliateId = affiliateID
	ext.ReferralRate = rate
	return PrepareAdtySiteInviteCommissionSnapshotTx(tx, topUp, ext)
}

// adtyReferralAfterCreditTx 在入账的同一个事务里插一条计佣任务。只用 OnConflict DoNothing，
// 不做可能报错的查询：PostgreSQL 下钩子一出错，整个入账事务都作废（审阅重点 10）。
func adtyReferralAfterCreditTx(tx *gorm.DB, topUp *model.TopUp, ext *model.AdtyTopupOrder) error {
	if ext == nil || ext.IsOwnerBusiness() || ext.TeamId > 0 || ext.Business == model.AdtyBusinessTeamTopup || ext.ReferralAffiliateId == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.ReferralCommissionJob{
		SourceType:    model.ReferralSourceTopup,
		SourceTradeNo: topUp.TradeNo,
		AffiliateId:   ext.ReferralAffiliateId,
		Status:        model.ReferralCommissionJobStatusPending,
	}).Error
}

// ProcessAdtyTopUpCommission 处理一个充值的计佣任务：佣金 = 面额（不含加赠）× 快照比例 ÷ 100，
// 状态 pending，SettleAt = 现在 + 冻结天数。由结算任务每轮扫描调用。
func ProcessAdtyTopUpCommission(tradeNo string) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		// R6-017: Lock top_ups -> adty_topup_orders BEFORE referral_commission_jobs,
		// matching CompleteAdtyTopUpManualRefund -> adtyReferralAfterRefundTx lock order.
		var topUps []model.TopUp
		if err := model.AdtyLockForUpdate(tx).Where("trade_no = ?", tradeNo).Limit(1).Find(&topUps).Error; err != nil {
			return err
		}
		var extRows []model.AdtyTopupOrder
		if err := model.AdtyLockForUpdate(tx).Where("trade_no = ?", tradeNo).Limit(1).Find(&extRows).Error; err != nil {
			return err
		}
		var job model.ReferralCommissionJob
		if err := model.AdtyLockForUpdate(tx).Where("source_type = ? AND source_trade_no = ?", model.ReferralSourceTopup, tradeNo).First(&job).Error; err != nil {
			return err
		}
		if job.Status == model.ReferralCommissionJobStatusSucceeded || job.Status == model.ReferralCommissionJobStatusSkipped {
			return nil
		}
		skip := func(reason string) error {
			return tx.Model(&model.ReferralCommissionJob{}).Where("id = ?", job.Id).Updates(map[string]any{
				"status": model.ReferralCommissionJobStatusSkipped, "last_error": reason, "attempt_count": job.AttemptCount + 1,
			}).Error
		}
		if len(topUps) == 0 {
			return skip("order not found")
		}
		status := topUps[0].Status
		if status != common.TopUpStatusSuccess && status != model.TopUpStatusRefunded {
			return skip("order is " + status)
		}
		var ext *model.AdtyTopupOrder
		if len(extRows) > 0 {
			ext = &extRows[0]
		}
		if ext == nil || ext.ReferralAffiliateId == 0 || ext.TeamId > 0 || ext.Business == model.AdtyBusinessTeamTopup {
			return skip("no referral snapshot")
		}
		// 退款早于计佣：退掉的本金不计佣，没退的（用户花掉的）照算。全额退本金时跳过。
		refundRatio := ext.RefundRatio()
		if refundRatio.GreaterThanOrEqual(decimal.NewFromInt(1)) {
			return skip("order refunded before commission")
		}
		var bindings []model.ReferralBinding
		if err := tx.Where("invitee_user_id = ?", topUps[0].UserId).Limit(1).Find(&bindings).Error; err != nil {
			return err
		}
		if len(bindings) > 0 && bindings[0].RiskFlag == model.ReferralRiskSelfInvite {
			return skip("self invite: no commission")
		}
		var affiliate model.ReferralAffiliate
		if err := tx.Where("id = ?", ext.ReferralAffiliateId).First(&affiliate).Error; err != nil {
			return err
		}
		amount := ext.FaceUSD.Mul(ext.ReferralRate).Div(decimal.NewFromInt(100)).Round(8)
		amount = amount.Sub(referralRefundClawback(amount, refundRatio))
		if !amount.IsPositive() {
			return skip("zero commission")
		}
		account, err := lockReferralAccountTx(tx, affiliate.Id)
		if err != nil {
			return err
		}
		now := time.Now().Unix()
		freeze := int64(max(referralSetting().FreezeDays, 0)) * 86400
		commission := &model.ReferralCommission{
			AffiliateId: affiliate.Id, AffiliateUserId: affiliate.UserId, InviteeUserId: topUps[0].UserId,
			SourceType: model.ReferralSourceTopup, SourceOrderId: topUps[0].Id, SourceTradeNo: tradeNo, OrderType: model.ReferralSourceTopup,
			BaseAmount: moneyF(ext.FaceUSD), PaidAmount: moneyF(ext.PaidAmount), PaidCurrency: ext.PaidCurrency,
			SettlementCurrency: "USD", SettlementFxRate: 1, SettlementBaseAmount: moneyF(ext.FaceUSD),
			Rate: moneyF(ext.ReferralRate), CommissionAmount: moneyF(amount),
			Status: model.ReferralCommissionStatusPending, SettleAt: now + freeze,
		}
		if err := tx.Create(commission).Error; err != nil {
			return err
		}
		if err := AdjustAccountTx(tx, account, amount, decimal.Zero, decimal.Zero, decimal.Zero, model.ReferralCommissionLedger{
			CommissionId: commission.Id, Type: "commission_accrue", RefType: model.ReferralSourceTopup, RefId: tradeNo,
			ExternalRefId: "accrue:" + tradeNo, Remark: "commission accrued",
		}); err != nil {
			return err
		}
		return tx.Model(&model.ReferralCommissionJob{}).Where("id = ?", job.Id).Updates(map[string]any{
			"status": model.ReferralCommissionJobStatusSucceeded, "succeeded_at": now, "attempt_count": job.AttemptCount + 1, "last_error": "",
		}).Error
	})
}

// markReferralJobFailed 把出错的计佣任务记成 failed，下一轮结算再重试。
func markReferralJobFailed(tradeNo string, cause error) {
	_ = model.DB.Model(&model.ReferralCommissionJob{}).
		Where("source_type = ? AND source_trade_no = ? AND status IN ?", model.ReferralSourceTopup, tradeNo,
			[]string{model.ReferralCommissionJobStatusPending, model.ReferralCommissionJobStatusFailed}).
		Updates(map[string]any{"status": model.ReferralCommissionJobStatusFailed, "failed_at": time.Now().Unix(), "last_error": truncateText(cause.Error(), 1000), "attempt_count": gorm.Expr("attempt_count + 1")}).Error
}

// RunReferralSettlementOnce 跑一轮结算：先重试 pending、failed 的计佣任务，再把到期的 pending 佣金转成可提现
// （停结算的推广员跳过）。返回转成可提现的笔数。
func RunReferralSettlementOnce(now int64, limit int) (int, error) {
	return RunReferralSettlementOnceForSite(now, limit, nil)
}

// RunReferralSettlementOnceForSite runs commission accrual + pending settlement, optionally restricted to one user site.
func RunReferralSettlementOnceForSite(now int64, limit int, siteID *int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	jobQuery := model.DB.Where("source_type = ? AND status IN ?", model.ReferralSourceTopup,
		[]string{model.ReferralCommissionJobStatusPending, model.ReferralCommissionJobStatusFailed})
	jobQuery = ScopeAdtyReferralAffiliateIDsQuery(jobQuery, "affiliate_id", siteID)
	var jobs []model.ReferralCommissionJob
	if err := jobQuery.Order("id").Limit(limit).Find(&jobs).Error; err != nil {
		return 0, err
	}
	for _, job := range jobs {
		if err := ProcessAdtyTopUpCommission(job.SourceTradeNo); err != nil {
			markReferralJobFailed(job.SourceTradeNo, err)
		}
	}
	commQuery := model.DB.Table("referral_commissions AS c").Select("c.*").
		Joins("JOIN referral_affiliates a ON a.id = c.affiliate_id").
		Where("c.status = ? AND c.settle_at <= ? AND a.settlement_enabled = ?", model.ReferralCommissionStatusPending, now, true)
	if siteID != nil {
		commQuery = commQuery.Where("c.affiliate_user_id IN (?)", model.DB.Model(&model.User{}).Select("id").Where("site_id = ?", *siteID))
	}
	var due []model.ReferralCommission
	if err := commQuery.Order("c.settle_at, c.id").Limit(limit).Scan(&due).Error; err != nil {
		return 0, err
	}
	settled := 0
	for _, item := range due {
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			// Canonical referral lock order: Affiliate -> Commission -> Account (R12-026).
			var affiliate model.ReferralAffiliate
			if err := model.AdtyLockForUpdate(tx).Where("id = ?", item.AffiliateId).First(&affiliate).Error; err != nil {
				return err
			}
			if !affiliate.SettlementEnabled {
				return nil
			}
			var commission model.ReferralCommission
			if err := model.AdtyLockForUpdate(tx).Where("id = ?", item.Id).First(&commission).Error; err != nil {
				return err
			}
			if commission.Status != model.ReferralCommissionStatusPending || commission.AffiliateId != affiliate.Id {
				return nil
			}
			account, err := lockReferralAccountTx(tx, commission.AffiliateId)
			if err != nil {
				return err
			}
			if err := tx.Model(&model.ReferralCommission{}).Where("id = ?", commission.Id).Updates(map[string]any{
				"status": model.ReferralCommissionStatusAvailable, "available_at": now,
			}).Error; err != nil {
				return err
			}
			amount := moneyDec(commission.CommissionAmount)
			if err := AdjustAccountTx(tx, account, amount.Neg(), amount, decimal.Zero, decimal.Zero, model.ReferralCommissionLedger{
				CommissionId: commission.Id, Type: "commission_settle", RefType: model.ReferralSourceTopup, RefId: commission.SourceTradeNo,
				ExternalRefId: fmt.Sprintf("settle:%d", commission.Id), Remark: "commission settled",
			}); err != nil {
				return err
			}
			settled++
			return nil
		})
		if err != nil {
			common.SysError(common.LogText("referral settle commission %d: %s", item.Id, err.Error()))
		}
	}
	return settled, nil
}

// referralRefundClawback 是退款要扣回的佣金：佣金 × 退掉的本金比例，8 位小数向下取整。计佣任务和退款钩子共用。
func referralRefundClawback(commission, ratio decimal.Decimal) decimal.Decimal {
	if !ratio.IsPositive() || !commission.IsPositive() {
		return decimal.Zero
	}
	return commission.Mul(ratio).RoundFloor(8)
}

// referralCommissionRefundBalanceTx reconstructs the entitlement still held as cash or paid allocation.
// The accrual ledger preserves the initial net credit even after pending refunds reduce CommissionAmount.
func referralCommissionRefundBalanceTx(tx *gorm.DB, commission *model.ReferralCommission) (remaining, recovered decimal.Decimal, err error) {
	var entries []model.ReferralCommissionLedger
	if err = tx.Where("commission_id = ? AND type IN ?", commission.Id,
		[]string{"commission_accrue", "commission_clawback_partial", "commission_clawback", "commission_void"}).Order("id").Find(&entries).Error; err != nil {
		return
	}
	remaining = moneyDec(commission.CommissionAmount)
	for _, entry := range entries {
		if entry.Type == "commission_accrue" {
			remaining = moneyDec(entry.DeltaPending)
			break
		}
	}
	for _, entry := range entries {
		if entry.Type != "commission_accrue" {
			remaining = remaining.Add(moneyDec(entry.DeltaPending)).Add(moneyDec(entry.DeltaAvailable)).Add(moneyDec(entry.DeltaFrozen))
		}
	}
	extRecovered, cvtRecovered, err := referralCommissionRecoveriesByKindTx(tx, commission.Id)
	if err != nil {
		return
	}
	cvtClawed, err := referralCommissionConvertedClawedTx(tx, commission.Id)
	if err != nil {
		return
	}
	recovered = extRecovered.Add(cvtRecovered).Add(cvtClawed)
	remaining = decimal.Max(remaining.Sub(recovered), decimal.Zero)
	return
}

func referralCommissionRecoveriesByKindTx(tx *gorm.DB, commissionID int) (externalRecovered, convertedRecovered decimal.Decimal, err error) {
	var rows []model.AdtyCommissionRecovery
	if err = tx.Where("commission_id = ? AND kind IN ?", commissionID, []string{adtyReferralCommissionRecoveryKind, adtyReferralConvertedRecoveryKind}).Find(&rows).Error; err != nil {
		return
	}
	for _, row := range rows {
		switch row.Kind {
		case adtyReferralConvertedRecoveryKind:
			convertedRecovered = convertedRecovered.Add(row.AmountUSD)
		default:
			externalRecovered = externalRecovered.Add(row.AmountUSD)
		}
	}
	return
}

func referralCommissionConvertedClawedTx(tx *gorm.DB, commissionID int) (decimal.Decimal, error) {
	if !tx.Migrator().HasTable(&model.AdtyBalanceLedger{}) {
		return decimal.Zero, nil
	}
	var sumQuota int64
	err := tx.Model(&model.AdtyBalanceLedger{}).
		Select("COALESCE(SUM(-delta_quota), 0)").
		Where("type = ? AND ref_type = ? AND ref_id = ? AND delta_quota < 0",
			model.AdtyLedgerTypeCommissionTransfer, "referral_commission_clawback", strconv.Itoa(commissionID)).
		Scan(&sumQuota).Error
	if err != nil || sumQuota <= 0 {
		return decimal.Zero, err
	}
	return decimal.NewFromInt(sumQuota).Div(common.QuotaPerUnitDecimal()), nil
}

// adtyReferralAfterRefundTx 在退款事务里按累计退掉的本金比例 ratio = P ÷ F 处理佣金（赠金不退，用户花掉的部分照样计佣）。
//   - ratio = 1：整笔处理。pending 作废并扣掉待结算；已经可提现或仍冻结的按实有余额冲回，不为负；
//     已经提现的记 AdtyCommissionRecovery 追偿责任，不伪造可提现扣回。还没计佣时，计佣任务标 skipped。
//   - 0 < ratio < 1：用原始面额和比例快照算累计应扣，只收回尚未扣过的差额，记 commission_clawback_partial。
//     pending 的佣金额随扣回减少，结算时只转剩下的；可提现或冻结按实有余额冲回；已提现记追偿。
//     还没计佣时任务不动，计佣任务按 (1 − ratio) 计佣。
//   - ratio = 0：什么都不做。
//
// 先锁计佣任务行：计佣任务处理时也先锁它，两边串行，不会漏扣或多计。
func adtyReferralAfterRefundTx(tx *gorm.DB, topUp *model.TopUp, ext *model.AdtyTopupOrder) error {
	ratio := ext.RefundRatio()
	if !ratio.IsPositive() {
		return nil
	}
	var jobs []model.ReferralCommissionJob
	if err := model.AdtyLockForUpdate(tx).Where("source_type = ? AND source_trade_no = ?", model.ReferralSourceTopup, topUp.TradeNo).Find(&jobs).Error; err != nil {
		return err
	}
	full := ratio.GreaterThanOrEqual(decimal.NewFromInt(1))
	if full {
		if err := tx.Model(&model.ReferralCommissionJob{}).
			Where("source_type = ? AND source_trade_no = ? AND status IN ?", model.ReferralSourceTopup, topUp.TradeNo,
				[]string{model.ReferralCommissionJobStatusPending, model.ReferralCommissionJobStatusFailed}).
			Updates(map[string]any{"status": model.ReferralCommissionJobStatusSkipped, "last_error": "order refunded before commission"}).Error; err != nil {
			return err
		}
	}
	// R5-006: Lock ALL ReferralCommission rows that may be touched by this refund OR by
	// auto-canceling multi-commission withdrawals (same affiliate's available/frozen commissions)
	// in ascending id order BEFORE locking any ReferralWithdrawal row.
	var affiliateIDs []int
	if err := tx.Model(&model.ReferralCommission{}).
		Where("source_type = ? AND source_trade_no = ? AND affiliate_id > 0", model.ReferralSourceTopup, topUp.TradeNo).
		Distinct("affiliate_id").Pluck("affiliate_id", &affiliateIDs).Error; err != nil {
		return err
	}
	var allLockedCommissions []model.ReferralCommission
	lockQuery := model.AdtyLockForUpdate(tx).Where("source_type = ? AND source_trade_no = ?", model.ReferralSourceTopup, topUp.TradeNo)
	if len(affiliateIDs) > 0 {
		lockQuery = model.AdtyLockForUpdate(tx).Where(
			"(source_type = ? AND source_trade_no = ?) OR (affiliate_id IN ? AND status IN ?)",
			model.ReferralSourceTopup, topUp.TradeNo, affiliateIDs,
			[]string{model.ReferralCommissionStatusAvailable, model.ReferralCommissionStatusFrozen},
		)
	}
	if err := lockQuery.Order("id ASC").Find(&allLockedCommissions).Error; err != nil {
		return err
	}
	commissions := make([]model.ReferralCommission, 0, len(allLockedCommissions))
	commissionIDs := make([]int, 0, len(allLockedCommissions))
	for _, commission := range allLockedCommissions {
		if commission.SourceType == model.ReferralSourceTopup && commission.SourceTradeNo == topUp.TradeNo {
			commissions = append(commissions, commission)
			commissionIDs = append(commissionIDs, commission.Id)
		}
	}
	if err := lockFrozenWithdrawalsForCommissionsTx(tx, commissionIDs); err != nil {
		return err
	}
	for _, commission := range commissions {
		remaining, _, err := referralCommissionRefundBalanceTx(tx, &commission)
		if err != nil {
			return err
		}
		gross := ext.FaceUSD.Mul(ext.ReferralRate).Div(decimal.NewFromInt(100)).Round(8)
		kept := gross.Sub(referralRefundClawback(gross, ratio))
		amount := decimal.Max(remaining.Sub(kept), decimal.Zero)
		account, err := lockReferralAccountTx(tx, commission.AffiliateId)
		if err != nil {
			return err
		}
		if !full {
			if err := clawBackCommissionPartTx(tx, account, &commission, amount, kept, topUp.TradeNo); err != nil {
				return err
			}
			continue
		}
		switch commission.Status {
		case model.ReferralCommissionStatusPending:
			if err := tx.Model(&model.ReferralCommission{}).Where("id = ?", commission.Id).Update("status", model.ReferralCommissionStatusVoid).Error; err != nil {
				return err
			}
			if err := AdjustAccountTx(tx, account, amount.Neg(), decimal.Zero, decimal.Zero, decimal.Zero, model.ReferralCommissionLedger{
				CommissionId: commission.Id, Type: "commission_void", RefType: "refund", RefId: topUp.TradeNo,
				ExternalRefId: fmt.Sprintf("void:%d", commission.Id), Remark: "order refunded",
			}); err != nil {
				return err
			}
		case model.ReferralCommissionStatusAvailable, model.ReferralCommissionStatusFrozen, model.ReferralCommissionStatusPaid:
			if err := tx.Model(&model.ReferralCommission{}).Where("id = ?", commission.Id).Update("status", model.ReferralCommissionStatusClawedBack).Error; err != nil {
				return err
			}
			if err := clawBackSettledCommissionTx(tx, account, &commission, amount, topUp.TradeNo, model.ReferralCommissionLedger{
				CommissionId: commission.Id, Type: "commission_clawback", RefType: "refund", RefId: topUp.TradeNo,
				ExternalRefId: fmt.Sprintf("clawback:%d", commission.Id), Remark: "order refunded",
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// clawBackCommissionPartTx 部分扣回一笔佣金（退款只退了一部分本金）：状态不改。pending 的把佣金额和待结算一起减掉；
// 可提现或冻结的按实有余额冲回（不为负）；已提现记追偿责任。void、clawed_back 不动。后续退款引用包含累计应留金额。
func clawBackCommissionPartTx(tx *gorm.DB, account *model.ReferralCommissionAccount, commission *model.ReferralCommission, part, kept decimal.Decimal, tradeNo string) error {
	if !part.IsPositive() {
		return nil
	}
	entry := model.ReferralCommissionLedger{
		CommissionId: commission.Id, Type: "commission_clawback_partial", RefType: "refund", RefId: tradeNo,
		ExternalRefId: fmt.Sprintf("clawback_partial:%d", commission.Id), Remark: "order partially refunded",
	}
	if exists, err := referralLedgerExistsTx(tx, entry.ExternalRefId); err != nil {
		return err
	} else if exists {
		entry.ExternalRefId += ":" + kept.StringFixed(8)
	}
	switch commission.Status {
	case model.ReferralCommissionStatusPending:
		if exists, err := referralLedgerExistsTx(tx, entry.ExternalRefId); err != nil {
			return err
		} else if exists {
			return nil
		}
		kept := moneyDec(commission.CommissionAmount).Sub(part)
		if err := tx.Model(&model.ReferralCommission{}).Where("id = ?", commission.Id).Update("commission_amount", moneyF(kept)).Error; err != nil {
			return err
		}
		return AdjustAccountTx(tx, account, part.Neg(), decimal.Zero, decimal.Zero, decimal.Zero, entry)
	case model.ReferralCommissionStatusAvailable, model.ReferralCommissionStatusFrozen, model.ReferralCommissionStatusPaid:
		return clawBackSettledCommissionTx(tx, account, commission, part, tradeNo, entry)
	}
	return nil
}

// adtyReferralCommissionRecoveryKind 把主站已提现邀请佣金的追偿和分站 kind=withdrawn 分开，避免 commission_id 撞号。
const (
	adtyReferralCommissionRecoveryKind = "ref_withdrawn"
	adtyReferralConvertedRecoveryKind  = "ref_converted"
)

// clawBackSettledCommissionTx 冲回已入账佣金：还在可提现或冻结里的按实有余额扣，不为负；
// 转入站内余额的优先从原提现批次回收未消费余额，仅已消费差额登记 ref_converted 追偿；
// 外部已提现的记 ref_withdrawn 追偿，不扣可提现。
// 从待处理或已审核提现里扣回的冻结占用，会同步缩减或解除对应提现单，避免后续打款或驳回再按原冻结额搬账户。
func clawBackSettledCommissionTx(tx *gorm.DB, account *model.ReferralCommissionAccount, commission *model.ReferralCommission, part decimal.Decimal, tradeNo string, entry model.ReferralCommissionLedger) error {
	if !part.IsPositive() {
		return nil
	}
	if account != nil {
		entry.AffiliateId = account.AffiliateId
		entry.UserId = account.UserId
	}
	if exists, err := referralLedgerExistsTx(tx, entry.ExternalRefId); err != nil {
		return err
	} else if exists {
		return nil
	}
	remaining, recovered, err := referralCommissionRefundBalanceTx(tx, commission)
	if err != nil {
		return err
	}
	frozenAlloc, convertedAlloc, externalAlloc, balanceItems, err := commissionAllocatedByChannelTx(tx, commission.Id)
	if err != nil {
		return err
	}
	extRecovered, cvtRecovered, err := referralCommissionRecoveriesByKindTx(tx, commission.Id)
	if err != nil {
		return err
	}
	cvtClawed, err := referralCommissionConvertedClawedTx(tx, commission.Id)
	if err != nil {
		return err
	}
	convertedLeft := decimal.Max(convertedAlloc.Sub(cvtClawed).Sub(cvtRecovered), decimal.Zero)
	externalLeft := decimal.Max(externalAlloc.Sub(extRecovered), decimal.Zero)
	if commission.Status == model.ReferralCommissionStatusPaid {
		// Legacy paid commissions without withdrawal_items fallback to external recovery.
		if !convertedLeft.IsPositive() && !externalLeft.IsPositive() {
			externalLeft = part
		}
		paidParts := splitDecimalByWeights(part, []decimal.Decimal{convertedLeft, externalLeft})
		if paidParts[0].IsPositive() {
			shortfall, reclaimErr := reclaimConvertedReferralCommissionTx(tx, commission, balanceItems, paidParts[0], entry.ExternalRefId)
			if reclaimErr != nil {
				return reclaimErr
			}
			if shortfall.IsPositive() {
				if err := recordReferralCommissionRecoveryByKindTx(tx, commission, adtyReferralConvertedRecoveryKind, cvtRecovered.Add(shortfall), tradeNo); err != nil {
					return err
				}
			}
		}
		if paidParts[1].IsPositive() {
			if err := recordReferralCommissionRecoveryByKindTx(tx, commission, adtyReferralCommissionRecoveryKind, extRecovered.Add(paidParts[1]), tradeNo); err != nil {
				return err
			}
		}
		return addReferralLedgerTx(tx, &entry)
	}
	frozenAlloc = decimal.Max(frozenAlloc, decimal.Zero)
	free := decimal.Max(remaining.Sub(frozenAlloc).Sub(convertedLeft).Sub(externalLeft), decimal.Zero)
	parts := splitDecimalByWeights(part, []decimal.Decimal{convertedLeft, externalLeft, frozenAlloc, free})
	if parts[0].IsPositive() {
		shortfall, reclaimErr := reclaimConvertedReferralCommissionTx(tx, commission, balanceItems, parts[0], entry.ExternalRefId)
		if reclaimErr != nil {
			return reclaimErr
		}
		if shortfall.IsPositive() {
			if err := recordReferralCommissionRecoveryByKindTx(tx, commission, adtyReferralConvertedRecoveryKind, cvtRecovered.Add(shortfall), tradeNo); err != nil {
				return err
			}
		}
	}
	if parts[1].IsPositive() {
		if err := recordReferralCommissionRecoveryByKindTx(tx, commission, adtyReferralCommissionRecoveryKind, extRecovered.Add(parts[1]), tradeNo); err != nil {
			return err
		}
	}
	deductFrozen := clawbackLimitedToBalance(account.FrozenAmount, parts[2])
	deductAvailable := clawbackLimitedToBalance(account.AvailableAmount, parts[3])
	// Keep paid allocations in the book value; remove only cash already clawed back.
	// Withdrawal allocation uses CommissionAmount minus the original paid and frozen items.
	booked := remaining.Add(recovered).Sub(deductFrozen).Sub(deductAvailable)
	if err := tx.Model(&model.ReferralCommission{}).Where("id = ?", commission.Id).Update("commission_amount", moneyF(booked)).Error; err != nil {
		return err
	}
	if deductFrozen.IsPositive() {
		if err := shrinkFrozenWithdrawalOccupancyTx(tx, commission.Id, deductFrozen); err != nil {
			return err
		}
	}
	if !deductFrozen.IsPositive() && !deductAvailable.IsPositive() {
		return addReferralLedgerTx(tx, &entry)
	}
	return AdjustAccountTx(tx, account, decimal.Zero, deductAvailable.Neg(), deductFrozen.Neg(), decimal.Zero, entry)
}

func reclaimConvertedReferralCommissionTx(tx *gorm.DB, commission *model.ReferralCommission, balanceItems []referralConvertedWithdrawalItem, amount decimal.Decimal, idemSuffix string) (decimal.Decimal, error) {
	if !amount.IsPositive() || commission == nil || commission.AffiliateUserId <= 0 || len(balanceItems) == 0 {
		return amount, nil
	}
	exact := amount.Mul(common.QuotaPerUnitDecimal())
	wanted, err := common.WalletQuotaFromDecimalStrict(exact.Floor())
	if err != nil {
		return decimal.Zero, err
	}
	if wanted <= 0 {
		return amount, nil
	}
	var user model.User
	if err := model.AdtyLockForUpdate(tx).First(&user, commission.AffiliateUserId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return amount, nil
		}
		return decimal.Zero, err
	}
	var reclaimed int64
	left := int64(wanted)
	if tx.Migrator().HasTable(&model.AdtyFundBatch{}) {
		for _, item := range balanceItems {
			if left <= 0 {
				break
			}
			// R3-09: Prefer the per-commission fund batch created for this commission.
			perCommSource := fmt.Sprintf("referral-withdrawal-%d-c-%d", item.WithdrawalId, commission.Id)
			var perLot model.AdtyFundBatch
			perRes := tx.Where("user_id = ? AND source = ? AND kind = ?", commission.AffiliateUserId, perCommSource, model.AdtyFundBatchKindPrincipal).Limit(1).Find(&perLot)
			if perRes.Error != nil {
				return decimal.Zero, perRes.Error
			}
			if perRes.RowsAffected > 0 {
				if perLot.Remaining <= 0 {
					continue
				}
				got, err := model.ReclaimAdtyFundBatchesBySourceTx(tx, commission.AffiliateUserId, perCommSource, min(perLot.Remaining, left), 0)
				if err != nil {
					return decimal.Zero, err
				}
				reclaimed += got
				left -= got
				continue
			}
			// Legacy combined balance transfer lot: subtract amounts already clawed back
			// for sibling commissions on the same withdrawal so the 2nd..Nth zero-spend
			// refunds are not masked behind the original combined lot size.
			source := fmt.Sprintf("referral-withdrawal-%d", item.WithdrawalId)
			var lot model.AdtyFundBatch
			res := tx.Where("user_id = ? AND source = ? AND kind = ?", commission.AffiliateUserId, source, model.AdtyFundBatchKindPrincipal).Limit(1).Find(&lot)
			if res.Error != nil {
				return decimal.Zero, res.Error
			}
			if res.RowsAffected == 0 || lot.Remaining <= 0 {
				continue
			}
			own, err := common.WalletQuotaFromDecimalStrict(item.AllocatedAmount.Mul(common.QuotaPerUnitDecimal()).Floor())
			if err != nil {
				return decimal.Zero, err
			}
			var siblingItems []model.ReferralWithdrawalItem
			if err := tx.Where("withdrawal_id = ? AND commission_id > 0 AND commission_id <> ?", item.WithdrawalId, commission.Id).
				Find(&siblingItems).Error; err != nil {
				return decimal.Zero, err
			}
			var siblingClawed int64
			if len(siblingItems) > 0 && tx.Migrator().HasTable(&model.AdtyBalanceLedger{}) {
				allocThisByComm := make(map[int]int64, len(siblingItems))
				siblingCommIDs := make([]int, 0, len(siblingItems))
				for _, sib := range siblingItems {
					q, qErr := common.WalletQuotaFromDecimalStrict(moneyDec(sib.AllocatedAmount).Mul(common.QuotaPerUnitDecimal()).Floor())
					if qErr != nil {
						return decimal.Zero, qErr
					}
					if _, exists := allocThisByComm[sib.CommissionId]; !exists {
						siblingCommIDs = append(siblingCommIDs, sib.CommissionId)
					}
					allocThisByComm[sib.CommissionId] += int64(q)
				}
				allocOthersByComm := make(map[int]int64, len(siblingCommIDs))
				var otherItems []model.ReferralWithdrawalItem
				if err := tx.Where("commission_id IN ? AND withdrawal_id <> ? AND status = ?",
					siblingCommIDs, item.WithdrawalId, model.ReferralWithdrawalItemStatusWithdrawn).
					Find(&otherItems).Error; err != nil {
					return decimal.Zero, err
				}
				if len(otherItems) > 0 {
					otherWIDs := make([]int, 0, len(otherItems))
					seenWID := make(map[int]struct{}, len(otherItems))
					for _, oi := range otherItems {
						if _, ok := seenWID[oi.WithdrawalId]; !ok {
							seenWID[oi.WithdrawalId] = struct{}{}
							otherWIDs = append(otherWIDs, oi.WithdrawalId)
						}
					}
					var balanceWIDs []int
					if err := tx.Model(&model.ReferralWithdrawal{}).
						Where("id IN ? AND account_type = ?", otherWIDs, model.ReferralWithdrawToBalance).
						Pluck("id", &balanceWIDs).Error; err != nil {
						return decimal.Zero, err
					}
					isBalanceWID := make(map[int]struct{}, len(balanceWIDs))
					for _, wid := range balanceWIDs {
						isBalanceWID[wid] = struct{}{}
					}
					for _, oi := range otherItems {
						if _, ok := isBalanceWID[oi.WithdrawalId]; !ok {
							continue
						}
						q, qErr := common.WalletQuotaFromDecimalStrict(moneyDec(oi.AllocatedAmount).Mul(common.QuotaPerUnitDecimal()).Floor())
						if qErr != nil {
							return decimal.Zero, qErr
						}
						allocOthersByComm[oi.CommissionId] += int64(q)
					}
				}
				refIDs := make([]string, 0, len(siblingCommIDs))
				for _, cid := range siblingCommIDs {
					refIDs = append(refIDs, strconv.Itoa(cid))
				}
				type clawSumRow struct {
					RefId string
					Total int64
				}
				var clawRows []clawSumRow
				if err := tx.Model(&model.AdtyBalanceLedger{}).
					Select("ref_id, COALESCE(SUM(-delta_quota), 0) AS total").
					Where("user_id = ? AND type = ? AND ref_type = ? AND ref_id IN ? AND delta_quota < 0",
						commission.AffiliateUserId, model.AdtyLedgerTypeCommissionTransfer, "referral_commission_clawback", refIDs).
					Group("ref_id").
					Scan(&clawRows).Error; err != nil {
					return decimal.Zero, err
				}
				totalClawedByComm := make(map[int]int64, len(clawRows))
				for _, cr := range clawRows {
					if cid, convErr := strconv.Atoi(cr.RefId); convErr == nil {
						totalClawedByComm[cid] = cr.Total
					}
				}
				// R4-12: Sum only the mathematically provable lower bound of clawbacks
				// attributable to this specific merged withdrawal batch:
				// min(max(totalClawed - allocInOthers, 0), allocInThis).
				for _, cid := range siblingCommIDs {
					provableFromThis := min(max(totalClawedByComm[cid]-allocOthersByComm[cid], 0), allocThisByComm[cid])
					siblingClawed += provableFromThis
				}
			}
			otherProtected := max(lot.Original-int64(own)-siblingClawed, 0)
			provable := lot.Remaining - otherProtected
			if provable <= 0 {
				continue
			}
			got, err := model.ReclaimAdtyFundBatchesBySourceTx(tx, commission.AffiliateUserId, source, min(provable, left), 0)
			if err != nil {
				return decimal.Zero, err
			}
			reclaimed += got
			left -= got
		}
	}
	if reclaimed <= 0 {
		return amount, nil
	}
	before, debited, err := model.AdtyDebitUserQuotaTx(tx, commission.AffiliateUserId, reclaimed)
	if err != nil {
		return decimal.Zero, err
	}
	if debited != reclaimed {
		return decimal.Zero, model.ErrAdtyFundBatchShortfall
	}
	if debited > 0 {
		key := fmt.Sprintf("ref-cvt-claw:%d:%s", commission.Id, idemSuffix)
		if len(key) > 64 {
			key = fmt.Sprintf("ref-cvt-claw:%d:%s", commission.Id, common.Sha1([]byte(idemSuffix)))
		}
		if err := model.InsertAdtyLedgerTx(tx, &model.AdtyBalanceLedger{
			UserId:         commission.AffiliateUserId,
			DeltaQuota:     -debited,
			BalanceBefore:  before,
			BalanceAfter:   before - debited,
			Type:           model.AdtyLedgerTypeCommissionTransfer,
			RefType:        "referral_commission_clawback",
			RefId:          strconv.Itoa(commission.Id),
			Reason:         "referral commission balance clawback",
			IdempotencyKey: &key,
		}); err != nil {
			return decimal.Zero, err
		}
	}
	debitedUSD := decimal.NewFromInt(debited).Div(common.QuotaPerUnitDecimal())
	shortfall := amount.Sub(debitedUSD)
	if shortfall.IsNegative() {
		return decimal.Zero, nil
	}
	return shortfall, nil
}

func clawbackLimitedToBalance(have float64, want decimal.Decimal) decimal.Decimal {
	if !want.IsPositive() {
		return decimal.Zero
	}
	balance := moneyDec(have)
	if !balance.IsPositive() {
		return decimal.Zero
	}
	if want.GreaterThan(balance) {
		return balance
	}
	return want
}

func recordReferralCommissionRecoveryTx(tx *gorm.DB, commission *model.ReferralCommission, target decimal.Decimal, tradeNo string) error {
	return recordReferralCommissionRecoveryByKindTx(tx, commission, adtyReferralCommissionRecoveryKind, target, tradeNo)
}

func recordReferralCommissionRecoveryByKindTx(tx *gorm.DB, commission *model.ReferralCommission, kind string, target decimal.Decimal, tradeNo string) error {
	if !target.IsPositive() {
		return nil
	}
	var rows []model.AdtyCommissionRecovery
	if err := tx.Where("commission_id = ? AND kind = ?", commission.Id, kind).Find(&rows).Error; err != nil {
		return err
	}
	already := decimal.Zero
	for _, row := range rows {
		already = already.Add(row.AmountUSD)
	}
	part := target.Sub(already)
	if !part.IsPositive() {
		return nil
	}
	prefix := "referral-recovery"
	if kind == adtyReferralConvertedRecoveryKind {
		prefix = "referral-cvt-recovery"
	}
	key := fmt.Sprintf("%s:%d:%s", prefix, commission.Id, target.String())
	if len(key) > 128 {
		key = fmt.Sprintf("%s:%d:%s", prefix, commission.Id, part.String())
		if len(key) > 128 {
			return fmt.Errorf("referral commission recovery key too long")
		}
	}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "idempotency_key"}}, DoNothing: true}).Create(&model.AdtyCommissionRecovery{
		SiteId: 0, CommissionId: int64(commission.Id), TradeNo: tradeNo, AmountUSD: part,
		Kind: kind, Status: "open", IdempotencyKey: key,
	}).Error
}

func referralLedgerExistsTx(tx *gorm.DB, externalRefId string) (bool, error) {
	if externalRefId == "" {
		return false, nil
	}
	var count int64
	if err := tx.Model(&model.ReferralCommissionLedger{}).Where("external_ref_id = ?", externalRefId).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func init() {
	model.AdtyBeforeTopUpCreateTx = prepareAdtyReferralSnapshotTx
	// 入账和退款的事务钩子只有一个，P6 和分站（11.5 的加赠扣减）一起挂上。
	model.AdtyAfterTopUpCreditedTx = func(tx *gorm.DB, topUp *model.TopUp, ext *model.AdtyTopupOrder) error {
		if err := adtyReferralAfterCreditTx(tx, topUp, ext); err != nil {
			return err
		}
		if err := adtySiteBonusAfterCreditTx(tx, topUp, ext); err != nil {
			return err
		}
		// R4-006: Record site invite commission (locks earnings -> margin) BEFORE
		// crediting operating principal (locks operating) to preserve canonical wallet lock order.
		if err := adtyRecordSiteInviteCommissionTx(tx, ext); err != nil {
			return err
		}
		return adtyCreditOperatingPrincipalTx(tx, topUp, ext)
	}
	model.AdtyAfterTopUpRefundedTx = func(tx *gorm.DB, topUp *model.TopUp, ext *model.AdtyTopupOrder) error {
		if err := adtyReferralAfterRefundTx(tx, topUp, ext); err != nil {
			return err
		}
		// R6-018: Process site invite commission (locks AdtySiteInviteCommission ->
		// AdtySiteInviteWithdrawal -> earnings -> margin) BEFORE site gift guarantee
		// release (locks earnings -> margin) to preserve canonical lock order.
		if err := adtySiteInviteCommissionAfterRefundTx(tx, ext); err != nil {
			return err
		}
		return adtySiteBonusAfterRefundTx(tx, topUp, ext)
	}
	model.AdtyAfterTopUpRefundCommitted = func(_ *model.TopUp, ext *model.AdtyTopupOrder) {
		if ext == nil || strings.TrimSpace(ext.TradeNo) == "" || model.DB == nil {
			return
		}
		var row model.AdtySiteInviteCommission
		if err := model.DB.Select("inviter_id").Where("trade_no = ?", ext.TradeNo).Limit(1).Find(&row).Error; err == nil && row.InviterId > 0 {
			model.AdtySyncUserQuotaCache(row.InviterId, "site invite commission clawback")
		}
		var refRows []model.ReferralCommission
		if err := model.DB.Select("affiliate_user_id").Where("source_type = ? AND source_trade_no = ?", model.ReferralSourceTopup, ext.TradeNo).Find(&refRows).Error; err == nil {
			seen := map[int]struct{}{}
			for _, refRow := range refRows {
				if refRow.AffiliateUserId > 0 {
					if _, dup := seen[refRow.AffiliateUserId]; !dup {
						seen[refRow.AffiliateUserId] = struct{}{}
						model.AdtySyncUserQuotaCache(refRow.AffiliateUserId, "referral commission balance clawback")
					}
				}
			}
		}
	}
	adtyMasterTasks = append(adtyMasterTasks, startReferralSettlementTask)
}

// startReferralSettlementTask 在主节点启动结算任务：间隔读 REFERRAL_SETTLEMENT_INTERVAL_SECONDS，默认 60，最少 10。
func startReferralSettlementTask() {
	interval := max(common.GetEnvOrDefault("REFERRAL_SETTLEMENT_INTERVAL_SECONDS", 60), 10)
	go func() {
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := RunReferralSettlementOnce(time.Now().Unix(), 200); err != nil {
				common.SysError(common.LogText("referral settlement: %s", err.Error()))
			}
			// R1-052: Release covered subsite invite commissions whose observation window has elapsed.
			if err := ReleaseAdtySiteInviteCommissions(time.Now()); err != nil {
				common.SysError(common.LogText("site invite commission release: %s", err.Error()))
			}
		}
	}()
}
