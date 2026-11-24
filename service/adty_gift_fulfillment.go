package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var errGiftGuaranteeShort = errors.New("site gift guarantee is not covered")

// adtyNotifyGiftFulfillmentPending notifies the site owner and Root that a
// verified payment left a site-funded gift in 待履约. Tests may replace it.
var adtyNotifyGiftFulfillmentPending = func(ext *model.AdtyTopupOrder) {
	if ext == nil {
		return
	}
	tradeNo := ext.TradeNo
	adtyNotifyRootAdmins(func(lang string) (string, string) {
		if stringsHasZH(lang) {
			return "赠金待履约", "订单 " + tradeNo + " 本金已入账，站长赠金尚未取得足额保障，已记为待履约，暂不可消费。"
		}
		return "Gift pending fulfillment", "Order " + tradeNo + " credited the principal, but the site gift is not yet fully guaranteed and cannot be spent."
	})
	site, err := GetAdtySite(ext.SiteId)
	if err != nil || site == nil || site.OwnerUserId <= 0 {
		return
	}
	var owner model.User
	if err := model.DB.Select("id", "email", "setting", "status").First(&owner, site.OwnerUserId).Error; err != nil {
		common.SysLog(common.LogText("gift fulfillment: load owner %d: %s", site.OwnerUserId, err.Error()))
		return
	}
	setting := owner.GetSetting()
	subject, content := "Gift pending fulfillment", "Order "+tradeNo+" credited the principal, but the site gift is not yet fully guaranteed and cannot be spent."
	if stringsHasZH(notifyDeliveryLanguage(setting)) {
		subject, content = "赠金待履约", "订单 "+tradeNo+" 本金已入账，站长赠金尚未取得足额保障，已记为待履约，暂不可消费。"
	}
	if err := NotifyUser(owner.Id, owner.Email, setting, dto.NewNotify(dto.NotifyTypeChannelUpdate, subject, content, nil)); err != nil {
		common.SysLog(common.LogText("gift fulfillment: notify owner %d: %s", owner.Id, err.Error()))
	}
}

// adtyNotifyGiftFulfillmentFulfilled tells the customer that a 待履约 gift was
// finally paid out after the site topped up its guarantee. Tests may replace it.
var adtyNotifyGiftFulfillmentFulfilled = func(row *model.AdtyGiftFulfillment, quota int64) {
	if row == nil || quota <= 0 {
		return
	}
	usd := decimal.NewFromInt(quota).Div(decimal.NewFromFloat(common.QuotaPerUnit)).Round(2).StringFixed(2)
	var customer model.User
	if err := model.DB.Select("id", "email", "setting", "status").First(&customer, row.UserId).Error; err != nil {
		common.SysLog(common.LogText("gift fulfillment: load customer %d: %s", row.UserId, err.Error()))
		return
	}
	setting := customer.GetSetting()
	subject := "Gift fulfilled"
	content := "The site gift for order " + row.TradeNo + " is credited: +" + usd + " USD (" + strconv.FormatInt(quota, 10) + " quota)."
	if stringsHasZH(notifyDeliveryLanguage(setting)) {
		subject = "赠金已履约"
		content = "订单 " + row.TradeNo + " 的站长赠金已到账：+" + usd + " USD（" + strconv.FormatInt(quota, 10) + " 额度）。"
	}
	if err := NotifyUser(customer.Id, customer.Email, setting, dto.NewNotify(dto.NotifyTypeChannelUpdate, subject, content, nil)); err != nil {
		common.SysLog(common.LogText("gift fulfillment: notify customer %d: %s", customer.Id, err.Error()))
	}
}

func stringsHasZH(lang string) bool {
	return len(lang) >= 2 && (lang == "zh-CN" || lang == "zh" || lang == "zhCN" || (len(lang) >= 2 && (lang[0] == 'z' || lang[0] == 'Z') && (lang[1] == 'h' || lang[1] == 'H')))
}

func prepareTopUpGiftOnCreditTx(tx *gorm.DB, ext *model.AdtyTopupOrder, priorStatus string, facts model.AdtyPaymentFacts) (int64, error) {
	totalBonus := topUpBonusQuota(ext)
	if tx == nil || ext == nil || totalBonus <= 0 {
		return totalBonus, nil
	}
	nonPending := priorStatus == common.TopUpStatusExpired || priorStatus == model.TopUpStatusCanceled || priorStatus == common.TopUpStatusFailed
	nowUnix := time.Now().Unix()
	// R3-013: Apply A.8 late-payment timing rules both after cancel/expiry and during the
	// pending expiry buffer (ExpireAt already elapsed or verified payment timestamp > ExpireAt),
	// covering both subsite orders (SiteId > 0) and main-site bonus orders (SiteId == 0).
	latePending := priorStatus == common.TopUpStatusPending && ext.ExpireAt > 0 &&
		(nowUnix > ext.ExpireAt || (facts.PaidTimeVerified && facts.PaidAt > ext.ExpireAt))
	if !nonPending && !latePending {
		return totalBonus, nil
	}
	var giftQuota int64
	bonus := decimal.Zero
	if ext.SiteId > 0 {
		bonus = adtyBonusUSDForSite(ext)
		if !bonus.IsPositive() {
			return totalBonus, nil
		}
		var err error
		giftQuota, err = siteGiftBonusQuota(ext)
		if err != nil {
			return 0, err
		}
	} else {
		if !ext.BonusUSD.IsPositive() {
			return totalBonus, nil
		}
		giftQuota = totalBonus
	}
	if giftQuota <= 0 {
		return totalBonus, nil
	}
	grantedWithoutWithheld := totalBonus - giftQuota
	if grantedWithoutWithheld < 0 {
		grantedWithoutWithheld = 0
	}
	// ISS-R1-0009 / R3-013: Payments completed after cancel/expiry (or after ExpireAt while pending)
	// require verified payment effective-time evidence inside the order window before auto-granting gifts.
	if ext.ExpireAt > 0 {
		if !facts.PaidTimeVerified || facts.PaidAt <= 0 {
			if priorStatus == common.TopUpStatusPending && ext.SiteId > 0 {
				if err := ReleaseAdtySiteGiftTx(tx, ext); err != nil {
					return 0, err
				}
			}
			reason := "payment completed after cancel or expiry without verified payment timestamp; pending manual review"
			if err := upsertGiftFulfillmentTx(tx, ext, giftQuota, model.AdtyGiftFulfillmentPendingReview, "pending_review", reason); err != nil {
				return 0, err
			}
			if err := tx.Model(&model.AdtyTopupOrder{}).Where("id = ?", ext.Id).Update("gift_fulfillment_status", model.AdtyGiftFulfillmentPendingReview).Error; err != nil {
				return 0, err
			}
			ext.GiftFulfillmentStatus = model.AdtyGiftFulfillmentPendingReview
			return grantedWithoutWithheld, nil
		}
		if facts.PaidAt > ext.ExpireAt {
			if priorStatus == common.TopUpStatusPending && ext.SiteId > 0 {
				if err := ReleaseAdtySiteGiftTx(tx, ext); err != nil {
					return 0, err
				}
			}
			if err := tx.Model(&model.AdtyTopupOrder{}).Where("id = ?", ext.Id).Update("gift_fulfillment_status", model.AdtyGiftFulfillmentCanceled).Error; err != nil {
				return 0, err
			}
			ext.GiftFulfillmentStatus = model.AdtyGiftFulfillmentCanceled
			return grantedWithoutWithheld, nil
		}
	}
	// Main-site bonus (SiteId == 0) has verified on-time payment evidence and needs no site guarantee.
	if ext.SiteId <= 0 {
		return totalBonus, nil
	}
	held, err := netGiftHoldTx(tx, ext.SiteId, ext.TradeNo)
	if err != nil {
		return 0, err
	}
	if !held.LessThan(bonus) {
		return totalBonus, nil
	}
	if err := reacquireAdtySiteGiftTx(tx, ext); err != nil {
		if !errors.Is(err, errGiftGuaranteeShort) && !errors.Is(err, ErrAdtySiteWalletInsufficient) {
			return 0, err
		}
		if err := upsertGiftFulfillmentTx(tx, ext, giftQuota, model.AdtyGiftFulfillmentPending, "pending", err.Error()); err != nil {
			return 0, err
		}
		if err := tx.Model(&model.AdtyTopupOrder{}).Where("id = ?", ext.Id).Update("gift_fulfillment_status", model.AdtyGiftFulfillmentPending).Error; err != nil {
			return 0, err
		}
		ext.GiftFulfillmentStatus = model.AdtyGiftFulfillmentPending
		return grantedWithoutWithheld, nil
	}
	if err := tx.Model(&model.AdtyTopupOrder{}).Where("id = ?", ext.Id).Updates(map[string]any{
		"gift_reserve_source":     ext.GiftReserveSource,
		"gift_reserve_earning":    ext.GiftReserveEarning,
		"gift_reserve_margin":     ext.GiftReserveMargin,
		"gift_fulfillment_status": "",
	}).Error; err != nil {
		return 0, err
	}
	return totalBonus, nil
}

func topUpBonusQuota(ext *model.AdtyTopupOrder) int64 {
	if ext == nil {
		return 0
	}
	bonus := ext.CreditQuota - ext.FaceQuota()
	if bonus < 0 {
		return 0
	}
	return bonus
}

func siteGiftBonusQuota(ext *model.AdtyTopupOrder) (int64, error) {
	if ext == nil {
		return 0, nil
	}
	bonus := adtyBonusUSDForSite(ext)
	if !bonus.IsPositive() {
		return 0, nil
	}
	quota, err := common.WalletQuotaFromDecimalStrict(bonus.Mul(common.QuotaPerUnitDecimal()).Floor())
	if err != nil {
		return 0, err
	}
	return int64(quota), nil
}

// adtyGiftNetHoldTypes is the typelist for remaining site-gift guarantee hold.
// gift_spend and gift_release both carry a negative HoldDelta.
func adtyGiftNetHoldTypes() []string {
	return []string{"gift_reserve", "gift_release", "gift_spend"}
}

func isAdtyGiftNetHoldType(typ string) bool {
	switch typ {
	case "gift_reserve", "gift_release", "gift_spend":
		return true
	default:
		return false
	}
}

func netGiftHoldTx(tx *gorm.DB, siteID int, tradeNo string) (decimal.Decimal, error) {
	var rows []model.AdtySiteWalletLedger
	if err := tx.Where("site_id = ? AND ref_type = ? AND ref_id = ? AND type IN ?",
		siteID, "topup", tradeNo, adtyGiftNetHoldTypes()).
		Find(&rows).Error; err != nil {
		return decimal.Zero, err
	}
	held := decimal.Zero
	for _, row := range rows {
		held = held.Add(row.HoldDelta)
	}
	return held, nil
}

func adtyProportionalGiftUSD(siteBonus decimal.Decimal, reclaimed, totalBonusQuota int64) decimal.Decimal {
	if reclaimed <= 0 || !siteBonus.IsPositive() {
		return decimal.Zero
	}
	if totalBonusQuota <= 0 || reclaimed >= totalBonusQuota {
		return siteBonus
	}
	return siteBonus.Mul(decimal.NewFromInt(reclaimed)).Div(decimal.NewFromInt(totalBonusQuota)).RoundFloor(8)
}

func adtyCompletedManualRefundBonusStatsTx(tx *gorm.DB, ext *model.AdtyTopupOrder) (hasRows bool, prevReclaimed, cumReclaimed int64, err error) {
	if tx == nil || ext == nil || ext.TradeNo == "" || !tx.Migrator().HasTable(&model.AdtyManualRefund{}) {
		return false, 0, 0, nil
	}
	var rows []model.AdtyManualRefund
	if err := tx.Where("trade_no = ? AND status = ?", ext.TradeNo, model.AdtyManualRefundStatusCompleted).Find(&rows).Error; err != nil {
		return false, 0, 0, err
	}
	if len(rows) == 0 {
		return false, 0, 0, nil
	}
	for _, r := range rows {
		cumReclaimed += max(r.BonusDeductedQuota, 0)
	}
	step := min(cumReclaimed, max(ext.RefundBonusQuota, 0))
	prevReclaimed = cumReclaimed - step
	return true, prevReclaimed, cumReclaimed, nil
}

func reacquireAdtySiteGiftTx(tx *gorm.DB, ext *model.AdtyTopupOrder) error {
	return reacquireAdtySiteGiftAmountTx(tx, ext, adtyBonusUSDForSite(ext))
}

func reacquireAdtySiteGiftAmountTx(tx *gorm.DB, ext *model.AdtyTopupOrder, bonus decimal.Decimal) error {
	if tx == nil || ext == nil || ext.SiteId <= 0 {
		return errGiftGuaranteeShort
	}
	maxBonus := adtyBonusUSDForSite(ext)
	if !bonus.IsPositive() || bonus.GreaterThan(maxBonus) {
		bonus = maxBonus
	}
	if !bonus.IsPositive() {
		return errGiftGuaranteeShort
	}
	if err := ensureAdtySiteWalletsTx(tx, ext.SiteId); err != nil {
		return err
	}
	var earning model.AdtySiteWallet
	if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND kind = ?", ext.SiteId, model.AdtySiteWalletEarnings).First(&earning).Error; err != nil {
		return err
	}
	held, err := netGiftHoldTx(tx, ext.SiteId, ext.TradeNo)
	if err != nil {
		return err
	}
	if held.IsNegative() {
		held = decimal.Zero
	}
	needed := bonus.Sub(held)
	if !needed.IsPositive() {
		return nil
	}
	fromEarning := earning.Balance.Sub(earning.FrozenUSD)
	if fromEarning.GreaterThan(needed) {
		fromEarning = needed
	}
	if fromEarning.IsNegative() {
		fromEarning = decimal.Zero
	}
	rest := needed.Sub(fromEarning)
	fromMargin := decimal.Zero
	if rest.IsPositive() {
		if !adty_setting.SiteBonusMarginFillAllowed(ext.SiteId) {
			return errGiftGuaranteeShort
		}
		var margin model.AdtySiteWallet
		if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND kind = ?", ext.SiteId, model.AdtySiteWalletMargin).First(&margin).Error; err != nil {
			return err
		}
		allowed := AdtyMarginNewDraw(AdtyMarginDrawInput{TierUSD: margin.CycleTierUSD, Balance: margin.Balance, Used: margin.CycleUsedUSD, Frozen: margin.FrozenUSD, ExcessFrozen: margin.ExcessFrozenUSD})
		if rest.GreaterThan(allowed) {
			return errGiftGuaranteeShort
		}
		fromMargin = rest
	}
	// Official snapshot used later by ReleaseAdtySiteGiftTx. Capture the
	// pre-increment base now so a shortfall top-up can add this reservation
	// onto the original official total instead of replacing it.
	addToExisting := held.IsPositive()
	origEarning, origMargin := ext.GiftReserveEarning, ext.GiftReserveMargin
	if addToExisting && !origEarning.IsPositive() && !origMargin.IsPositive() {
		heldEarning, heldMargin, err := netGiftHoldByKindTx(tx, ext.SiteId, ext.TradeNo)
		if err != nil {
			return err
		}
		origEarning, origMargin = heldEarning, heldMargin
	}
	key := "gift:" + ext.TradeNo
	already, err := adtyWalletIdempotencyExists(tx, key+":earn:reacquire", key+":margin:reacquire")
	if err != nil {
		return err
	}
	if fromEarning.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, ext.SiteId, model.AdtySiteWalletEarnings, decimal.Zero, AdtySiteWalletChange{
			Type: "gift_reserve", RefType: "topup", RefID: ext.TradeNo, IdempotencyKey: key + ":earn:reacquire",
			HoldDelta: fromEarning,
		}); err != nil {
			return err
		}
	}
	if fromMargin.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, ext.SiteId, model.AdtySiteWalletMargin, decimal.Zero, AdtySiteWalletChange{
			Type: "gift_reserve", RefType: "topup", RefID: ext.TradeNo, IdempotencyKey: key + ":margin:reacquire",
			HoldDelta: fromMargin,
		}); err != nil {
			return err
		}
	}
	if addToExisting {
		ext.GiftReserveEarning = origEarning.Add(fromEarning)
		ext.GiftReserveMargin = origMargin.Add(fromMargin)
	} else {
		// Full reacquire after cancel/expire: net hold is 0 because Release
		// already wrote gift_release. Set the snapshot to this reservation
		// only; do not add it onto a stale pre-release snapshot.
		ext.GiftReserveEarning = fromEarning
		ext.GiftReserveMargin = fromMargin
	}
	switch {
	case ext.GiftReserveEarning.IsPositive() && ext.GiftReserveMargin.IsPositive():
		ext.GiftReserveSource = "earning+margin"
	case ext.GiftReserveMargin.IsPositive():
		ext.GiftReserveSource = "margin"
	default:
		ext.GiftReserveSource = "earning"
	}
	if !already && (fromEarning.IsPositive() || fromMargin.IsPositive()) {
		if _, err := appendAdtyGiftOfficialAuditTx(tx, ext.UserId, ext.SiteId, ext.TradeNo, "adty.gift.reserve", "reacquire",
			map[string]any{"earning": "0", "margin": "0"},
			map[string]any{
				"trade_no": ext.TradeNo, "site_id": ext.SiteId, "bonus_usd": ext.BonusUSD.String(),
				"source":  ext.GiftReserveSource,
				"earning": fromEarning.String(), "margin": fromMargin.String(),
				"phase": "reacquire",
			}); err != nil {
			return err
		}
	}
	return nil
}

func upsertGiftFulfillmentTx(tx *gorm.DB, ext *model.AdtyTopupOrder, bonusQuota int64, status, action, reason string) error {
	event, _ := common.Marshal(map[string]any{
		"at":      time.Now().Unix(),
		"action":  action,
		"reason":  reason,
		"source":  ext.GiftReserveSource,
		"earning": ext.GiftReserveEarning.String(),
		"margin":  ext.GiftReserveMargin.String(),
	})
	var existing model.AdtyGiftFulfillment
	lookup := tx.Where("trade_no = ?", ext.TradeNo).Limit(1).Find(&existing)
	if lookup.Error != nil {
		return lookup.Error
	}
	if lookup.RowsAffected > 0 {
		if existing.Status == model.AdtyGiftFulfillmentPending || existing.Status == model.AdtyGiftFulfillmentPendingReview {
			return nil
		}
		// A later official fulfillment or cancel is not overwritten here.
		return nil
	}
	row := &model.AdtyGiftFulfillment{
		TradeNo:         ext.TradeNo,
		UserId:          ext.UserId,
		SiteId:          ext.SiteId,
		BonusUSD:        ext.BonusUSD,
		BonusQuota:      bonusQuota,
		OriginalSource:  ext.GiftReserveSource,
		OriginalEarning: ext.GiftReserveEarning,
		OriginalMargin:  ext.GiftReserveMargin,
		Status:          status,
		Reason:          reason,
		History:         "[" + string(event) + "]",
	}
	return tx.Create(row).Error
}

// adtyGiftFulfillmentResumeLimit caps one resume sweep. Pending rows are rare
// (one per order paid after cancel/expiry while the guarantee was short), so one
// bounded batch is enough per top-up; the next top-up picks up whatever is left.
const adtyGiftFulfillmentResumeLimit = 100

// ResumePendingAdtyGiftFulfillments 兑现该站点所有「待履约」赠金。
//
// 待履约（pending）是「客户已实付、站长承诺、但保障不足被扣下」的那部分赠金
// （ISS-R4-0076）。它此前没有出口：只有全额退款会把它转成 canceled，导致客户
// 付了钱、平台记着欠、却永远不会到账。站长补足保证金后保障就有了，这里按
// trade_no 重跑 reacquireAdtySiteGiftTx 取回保障，再把当时扣下的额度补进客户
// 的余额与批次，状态转 fulfilled。
//
// 保障仍然不足时整笔跳过：不写额度、不改状态、不写批次，留待下次补足。
// 每笔各起一个事务，一笔失败不影响同一站点的其它笔，也不会重复发放
// ResumePendingAdtyGiftFulfillments retries pending gift fulfillments for a site
// (including siteID == 0 for main-site bonus orders after refund/transient pauses).
// （pending → fulfilled 的迁移在行锁内只发生一次）。
func ResumePendingAdtyGiftFulfillments(siteID int) (int, error) {
	if siteID < 0 || model.DB == nil {
		return 0, nil
	}
	var rows []model.AdtyGiftFulfillment
	if err := model.DB.Where("site_id = ? AND status = ?", siteID, model.AdtyGiftFulfillmentPending).
		Order("id asc").Limit(adtyGiftFulfillmentResumeLimit).Find(&rows).Error; err != nil {
		return 0, err
	}
	fulfilled := 0
	for i := range rows {
		done, err := fulfillPendingAdtyGiftFulfillment(rows[i].TradeNo)
		if err != nil {
			if !errors.Is(err, errGiftGuaranteeShort) && !errors.Is(err, ErrAdtySiteWalletInsufficient) {
				common.SysLog(common.LogText("gift fulfillment: resume %s: %s", rows[i].TradeNo, err.Error()))
			}
			continue
		}
		if done {
			fulfilled++
		}
	}
	return fulfilled, nil
}

// fulfillPendingAdtyGiftFulfillment 兑现一笔待履约赠金。返回 true 表示这一笔
// 本来 pending、这次兑现成功。保障不足时返回 errGiftGuaranteeShort，
// 调用方据此保持 pending。
func fulfillPendingAdtyGiftFulfillment(tradeNo string) (bool, error) {
	if tradeNo == "" || model.DB == nil {
		return false, nil
	}
	var notify model.AdtyGiftFulfillment
	var granted int64
	var auditEntry *model.AuditLog
	fulfilled := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		// R3-003: Pre-read IDs unlocked, then acquire locks in canonical order:
		// AdtySite -> AdtyTopupOrder -> AdtyManualRefund -> User -> AdtySiteWallet -> AdtyGiftFulfillment.
		var peek model.AdtyGiftFulfillment
		if err := tx.Select("id", "user_id", "site_id", "status").
			Where("trade_no = ? AND status = ?", tradeNo, model.AdtyGiftFulfillmentPending).
			Limit(1).Find(&peek).Error; err != nil {
			return err
		}
		if peek.Id == 0 {
			return nil
		}
		if peek.SiteId > 0 {
			var site model.AdtySite
			if err := model.AdtyLockForUpdate(tx).Where("id = ?", peek.SiteId).Limit(1).Find(&site).Error; err != nil {
				return err
			}
		}
		var ext model.AdtyTopupOrder
		if err := model.AdtyLockForUpdate(tx).Where("trade_no = ? AND user_id = ?", tradeNo, peek.UserId).
			Limit(1).Find(&ext).Error; err != nil {
			return err
		}
		if ext.Id == 0 || ext.SiteId != peek.SiteId {
			return nil
		}
		// 本金已经全额退掉的单子不再补发：终结由退款钩子
		// （cancelPendingGiftFulfillmentOnFullRefundTx）负责，这里只兜底。
		// 部分退款不缩小这笔赠金——退款口径是「本金退、赠金不退」，赠金在实付时
		// 就已成立，扣下的那部分仍要兑现。
		if face := ext.FaceQuota(); face > 0 && ext.RefundQuota >= face {
			return nil
		}
		// Active H/U manual refunds hold the top-up under dispute, so pending
		// site gift fulfillment pauses until they settle or shrink (ISS-R1-0013).
		hasActiveRefund, err := model.HasActiveManualRefundOccupancyTx(tx, tradeNo)
		if err != nil {
			return err
		}
		if hasActiveRefund {
			return nil
		}
		var user model.User
		if err := model.AdtyLockForUpdate(tx).Select("id", "site_id").Where("id = ?", peek.UserId).First(&user).Error; err != nil {
			return err
		}
		var row model.AdtyGiftFulfillment
		if err := model.AdtyLockForUpdate(tx).Where("id = ? AND status = ?", peek.Id, model.AdtyGiftFulfillmentPending).
			Limit(1).Find(&row).Error; err != nil {
			return err
		}
		if row.Id == 0 {
			// 已兑现或已终结：重放的扫描不能再发一次。
			return nil
		}
		// 当时被扣下的正是「本单赠送额度 - 已发放额度」。pending 行记的 bonus
		// 额度是站长承诺额（可能已因部分退款缩小），本单赠送额度是实发上限，取小的那个才不会多发。
		owed := min(row.BonusQuota, topUpBonusQuota(&ext))
		if owed <= 0 {
			return nil
		}
		if ext.SiteId > 0 {
			targetBonusUSD := row.BonusUSD
			if !targetBonusUSD.IsPositive() {
				targetBonusUSD = adtyBonusUSDForSite(&ext)
			}
			if err := reacquireAdtySiteGiftAmountTx(tx, &ext, targetBonusUSD); err != nil {
				return err
			}
			// R5-053: Book site bonus expense in daily earnings upon actual deferred gift fulfillment.
			if targetBonusUSD.IsPositive() {
				if err := upsertAdtySiteEarningTx(tx, adtySiteEarningDayFn(), adtySiteEarningEntry{
					SiteId: ext.SiteId, SiteOwnerUserId: ext.SiteOwnerUserId, UserId: row.UserId, Kind: model.AdtySiteEarningBonus, Earning: targetBonusUSD.Neg(),
				}); err != nil {
					return err
				}
			}
		}
		before, err := model.AdtyCreditUserQuotaTx(tx, row.UserId, owed)
		if err != nil {
			return err
		}
		batchSiteID := row.SiteId
		if batchSiteID > 0 && user.SiteId == 0 {
			// R10-033: Customer migrated to main site after subsite close.
			batchSiteID = 0
		} else if batchSiteID > 0 && tx.Migrator().HasTable(&model.AdtySite{}) {
			var siteStatus string
			if err := tx.Model(&model.AdtySite{}).Select("status").Where("id = ?", batchSiteID).Limit(1).Scan(&siteStatus).Error; err != nil {
				return err
			}
			if siteStatus == model.AdtySiteStatusClosed {
				batchSiteID = 0
			}
		}
		if err := addAdtyGiftFulfilledBonusTx(tx, row.UserId, batchSiteID, tradeNo, owed); err != nil {
			return err
		}
		key := "gift-fulfill:" + common.Sha1([]byte(tradeNo))
		if err := model.InsertAdtyLedgerTx(tx, &model.AdtyBalanceLedger{
			UserId: row.UserId, DeltaQuota: owed, BalanceBefore: before, BalanceAfter: before + owed,
			Type: model.AdtyLedgerTypeGiftFulfillment, RefType: "topup", RefId: tradeNo,
			Reason: "site gift fulfilled after guarantee top-up", IdempotencyKey: &key,
		}); err != nil {
			return err
		}
		if err := tx.Model(&model.AdtyTopupOrder{}).Where("id = ?", ext.Id).Updates(map[string]any{
			"gift_reserve_source":     ext.GiftReserveSource,
			"gift_reserve_earning":    ext.GiftReserveEarning,
			"gift_reserve_margin":     ext.GiftReserveMargin,
			"gift_fulfillment_status": model.AdtyGiftFulfillmentFulfilled,
		}).Error; err != nil {
			return err
		}
		history := adtyGiftFulfillmentHistory(row.History, map[string]any{
			"at": time.Now().Unix(), "action": "fulfilled", "quota": owed,
			"source": ext.GiftReserveSource, "earning": ext.GiftReserveEarning.String(), "margin": ext.GiftReserveMargin.String(),
		})
		if err := tx.Model(&model.AdtyGiftFulfillment{}).Where("id = ?", row.Id).Updates(map[string]any{
			"status": model.AdtyGiftFulfillmentFulfilled, "reason": "fulfilled after guarantee top-up", "history": history,
		}).Error; err != nil {
			return err
		}
		var auditErr error
		auditEntry, auditErr = appendAdtyGiftOfficialAuditTx(tx, row.UserId, row.SiteId, tradeNo, "adty.gift.fulfill",
			"guarantee top-up released the pending gift",
			map[string]any{"status": model.AdtyGiftFulfillmentPending, "quota": 0},
			map[string]any{
				"trade_no": tradeNo, "status": model.AdtyGiftFulfillmentFulfilled, "quota": owed,
				"source": ext.GiftReserveSource, "earning": ext.GiftReserveEarning.String(), "margin": ext.GiftReserveMargin.String(),
			})
		if auditErr != nil {
			return auditErr
		}
		fulfilled = true
		granted = owed
		notify = row
		return nil
	})
	if err != nil {
		return false, err
	}
	if auditEntry != nil {
		deliverAdtyCommittedAudit(*auditEntry)
	}
	if fulfilled {
		model.AdtySyncCreditUserQuotaCache(notify.UserId, granted, "gift fulfillment")
		adtyNotifyGiftFulfillmentFulfilled(&notify, granted)
	}
	return fulfilled, nil
}

// addAdtyGiftFulfilledBonusTx 把补发的赠金加回本单的 bonus 批次。
//
// 不能直接用 CreateAdtyFundBatchesTx：同一 (user, source) 已有批次时它是空操作，
// 补发的额度会从「Σ批次 == 余额」里漏掉，后续 gift_spend 按比例释放保障也就错了。
// 所以先按 source 把 original/remaining 一起加上；本单从未落过 bonus 批次
// （赠金被整笔扣下）时才新建。
func addAdtyGiftFulfilledBonusTx(tx *gorm.DB, userID, siteID int, source string, quota int64) error {
	if tx == nil || userID <= 0 || source == "" || quota <= 0 {
		return nil
	}
	res := tx.Model(&model.AdtyFundBatch{}).
		Where("user_id = ? AND source = ? AND kind = ?", userID, source, "bonus").
		Updates(map[string]any{
			"original":  gorm.Expr("original + ?", quota),
			"remaining": gorm.Expr("remaining + ?", quota),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return nil
	}
	// 没有 bonus 批次：本单收钱时赠金被整笔扣下，当时只落了本金批次。
	// CreateAdtyFundBatchesTx 在这里不能用——它按 (user, source) 判重，
	// 本金批次在就算「已存在」直接返回，补发的额度会静默丢掉。
	siteIDPtr := siteID
	return tx.Create(&model.AdtyFundBatch{
		UserId:       userID,
		SiteId:       &siteIDPtr,
		Kind:         model.AdtyFundBatchKindBonus,
		GiftCategory: model.AdtyGiftCategoryPermanent,
		Status:       model.AdtyFundBatchStatusActive,
		Source:       source,
		Original:     quota,
		Remaining:    quota,
		CreatedAt:    common.GetTimestamp(),
	}).Error
}

// adtyGiftFulfillmentHistory appends one event to the row's JSON history.
func adtyGiftFulfillmentHistory(raw string, event map[string]any) string {
	body, err := common.Marshal(event)
	if err != nil {
		return raw
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return "[" + string(body) + "]"
	}
	if strings.HasSuffix(raw, "]") {
		return raw[:len(raw)-1] + "," + string(body) + "]"
	}
	return "[" + string(body) + "]"
}

func notifyPendingGiftFulfillmentAfterCredit(_ *model.TopUp, ext *model.AdtyTopupOrder, _ int) {
	if ext == nil {
		return
	}
	if ext.IsOwnerMargin() && ext.SiteId > 0 {
		if _, err := ResumePendingAdtyGiftFulfillments(ext.SiteId); err != nil {
			common.SysLog(common.LogText("resume pending gift fulfillments after owner margin top-up site=%d: %s", ext.SiteId, err.Error()))
		}
		return
	}
	if ext.GiftFulfillmentStatus != model.AdtyGiftFulfillmentPending && ext.GiftFulfillmentStatus != model.AdtyGiftFulfillmentPendingReview {
		return
	}
	adtyNotifyGiftFulfillmentPending(ext)
}

const adtyExpireReleaseBufferSeconds int64 = 60

// adtyOrderRequiresRemoteUnpaidConfirm reports whether an expired order had an
// active remote HTTPS gateway checkout/channel capable of answering payment status.
func adtyOrderRequiresRemoteUnpaidConfirm(tx *gorm.DB, ext *model.AdtyTopupOrder) (bool, error) {
	if ext == nil || tx == nil {
		return false, nil
	}
	switch ext.Provider {
	case model.PaymentProviderAdtyBEpusdt:
		if strings.TrimSpace(ext.GatewayTradeId) == "" || strings.TrimSpace(ext.PayAddress) == "" {
			return false, nil
		}
		endpoint, err := url.Parse(strings.TrimSpace(ext.PayURL))
		return err == nil && endpoint.Scheme == "https" && endpoint.Host != "" && endpoint.User == nil, nil
	case model.PaymentProviderAdtyEpay:
		if ext.ChannelId <= 0 {
			return false, nil
		}
		var channel model.AdtyEpayChannel
		if err := tx.Where("id = ?", ext.ChannelId).Limit(1).Find(&channel).Error; err != nil {
			return false, err
		}
		if channel.Id == 0 || channel.Status == model.AdtyEpayStatusRevoked {
			return false, nil
		}
		endpoint, err := url.Parse(strings.TrimSpace(channel.GatewayURL))
		if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil {
			return false, nil
		}
		return channel.MerchantId != "" && channel.MerchantKey != "", nil
	default:
		return false, nil
	}
}

// confirmTopUpUnpaidBeforeExpireTx enforces the post-expiry buffer and remote
// unpaid confirmation before ExpireAdtyTopUps releases site gift guarantees (ISS-R1-0010).
func confirmTopUpUnpaidBeforeExpireTx(tx *gorm.DB, _ *model.TopUp, ext *model.AdtyTopupOrder, now int64) (bool, error) {
	if ext == nil || ext.ExpireAt <= 0 {
		return true, nil
	}
	if now < ext.ExpireAt+adtyExpireReleaseBufferSeconds {
		return false, nil
	}
	requiresRemote, err := adtyOrderRequiresRemoteUnpaidConfirm(tx, ext)
	if err != nil {
		return false, err
	}
	if !requiresRemote {
		return true, nil
	}
	var latest model.AdtyOrderPaymentEvent
	err = tx.Where("trade_no = ? AND source = ?", ext.TradeNo, model.AdtyPaymentSourceQuery).
		Order("id desc").Limit(1).Find(&latest).Error
	if err != nil {
		return false, err
	}
	if latest.Id == 0 || latest.Outcome != "unpaid" {
		return false, nil
	}
	return ext.LastQueryAt >= ext.ExpireAt || latest.CreatedAt >= ext.ExpireAt, nil
}

func initGiftFulfillmentHooks() {
	model.AdtyPrepareTopUpGiftOnCreditTx = prepareTopUpGiftOnCreditTx
	model.AdtyAfterTopUpCreditCommitted = notifyPendingGiftFulfillmentAfterCredit
	model.AdtyAfterBonusSpentTx = applySiteGiftSpendOnBonusTx
	model.AdtyAfterBonusReleasedTx = releaseSiteGiftGuaranteeOnBonusReductionTx
	model.AdtyAfterBonusRefundedTx = reverseSiteGiftSpendOnBonusTx
	model.AdtyBeforeExpireReleaseTx = confirmTopUpUnpaidBeforeExpireTx
}

// effectiveSiteGiftBasisTx returns the actual granted site gift USD and quota
// basis for refund guarantee release and daily bonus reversal (R6-019). For
// deferred gifts shrunk by an earlier partial refund before fulfillment, it uses
// the fulfilled AdtyGiftFulfillment row rather than the unshrunk order fields.
func effectiveSiteGiftBasisTx(tx *gorm.DB, ext *model.AdtyTopupOrder) (decimal.Decimal, int64, error) {
	siteBonus := adtyBonusUSDForSite(ext)
	totalBonusQuota, err := siteGiftBonusQuota(ext)
	if err != nil {
		return decimal.Zero, 0, err
	}
	if tx != nil && ext != nil && ext.GiftFulfillmentStatus == model.AdtyGiftFulfillmentFulfilled && tx.Migrator().HasTable(&model.AdtyGiftFulfillment{}) {
		var row model.AdtyGiftFulfillment
		if err := tx.Where("trade_no = ? AND status = ?", ext.TradeNo, model.AdtyGiftFulfillmentFulfilled).Limit(1).Find(&row).Error; err != nil {
			return decimal.Zero, 0, err
		}
		if row.Id > 0 && row.BonusUSD.IsPositive() && row.BonusQuota > 0 {
			return row.BonusUSD, row.BonusQuota, nil
		}
	}
	return siteBonus, totalBonusQuota, nil
}

// releaseUnusedSiteGiftGuaranteeTx releases the unused portion of a paid
// order's site-gift guarantee back to the original earning and/or margin
// wallets. The unused amount follows the official reclaim: site gift ×
// min(G, R) ÷ G. A full reclaim clears any remaining net hold, including a
// hold re-acquired after cancel or expiry. Idempotent for one official refund.
func releaseUnusedSiteGiftGuaranteeTx(tx *gorm.DB, ext *model.AdtyTopupOrder) error {
	if tx == nil || ext == nil || ext.SiteId <= 0 {
		return nil
	}
	siteBonus, totalBonusQuota, err := effectiveSiteGiftBasisTx(tx, ext)
	if err != nil {
		return err
	}
	hasRows, _, cumReclaimed, err := adtyCompletedManualRefundBonusStatsTx(tx, ext)
	if err != nil {
		return err
	}
	var bonusBatchCount int64
	var remainingBonusQuota int64
	if tx.Migrator().HasTable(&model.AdtyFundBatch{}) {
		if err := tx.Model(&model.AdtyFundBatch{}).
			Where("user_id = ? AND source = ? AND kind = ?", ext.UserId, ext.TradeNo, "bonus").
			Count(&bonusBatchCount).Error; err != nil {
			return err
		}
		if bonusBatchCount > 0 {
			if err := tx.Model(&model.AdtyFundBatch{}).
				Where("user_id = ? AND source = ? AND kind = ?", ext.UserId, ext.TradeNo, "bonus").
				Select("COALESCE(SUM(remaining), 0)").
				Scan(&remainingBonusQuota).Error; err != nil {
				return err
			}
		}
	}
	bonusBatchDepleted := bonusBatchCount > 0 && remainingBonusQuota <= 0
	var unusedUSD decimal.Decimal
	var key string
	var reclaimedForCancel int64
	var denomForCancel int64
	var fullRelease bool
	if hasRows {
		denom := totalBonusQuota
		if denom <= 0 {
			denom = cumReclaimed
		}
		denomForCancel = denom
		reclaimedForCancel = cumReclaimed
		if !siteBonus.IsPositive() || cumReclaimed <= 0 {
			return cancelPendingGiftFulfillmentOnFullRefundTx(tx, ext, reclaimedForCancel, denomForCancel)
		}
		cumTarget := adtyProportionalGiftUSD(siteBonus, cumReclaimed, denom)
		var ledgers []model.AdtySiteWalletLedger
		if err := tx.Where("site_id = ? AND ref_type = ? AND ref_id = ? AND type = ? AND idempotency_key LIKE ?",
			ext.SiteId, "topup", ext.TradeNo, "gift_release", "%:refund-release").Find(&ledgers).Error; err != nil {
			return err
		}
		alreadyReleased := decimal.Zero
		for _, l := range ledgers {
			if l.HoldDelta.IsNegative() {
				alreadyReleased = alreadyReleased.Add(l.HoldDelta.Neg())
			}
		}
		unusedUSD = cumTarget.Sub(alreadyReleased)
		key = "gift:" + ext.TradeNo + ":refund-" + strconv.FormatInt(cumReclaimed, 10)
		fullRelease = (totalBonusQuota > 0 && cumReclaimed >= totalBonusQuota) || bonusBatchDepleted
	} else {
		bonusQuota := ext.RefundBonusQuota
		if bonusQuota <= 0 {
			return cancelPendingGiftFulfillmentOnFullRefundTx(tx, ext, 0, totalBonusQuota)
		}
		denom := totalBonusQuota
		if denom <= 0 {
			denom = bonusQuota
		}
		reclaimed := min(denom, min(bonusQuota, max(ext.RefundDeductedQuota, 0)))
		reclaimedForCancel = reclaimed
		denomForCancel = denom
		if !siteBonus.IsPositive() || reclaimed <= 0 {
			return nil
		}
		unusedUSD = adtyProportionalGiftUSD(siteBonus, reclaimed, denom)
		key = "gift:" + ext.TradeNo
		fullRelease = reclaimed == denom || bonusBatchDepleted
	}
	if !unusedUSD.IsPositive() && !fullRelease {
		return cancelPendingGiftFulfillmentOnFullRefundTx(tx, ext, reclaimedForCancel, denomForCancel)
	}
	heldEarning, heldMargin, err := netGiftHoldByKindTx(tx, ext.SiteId, ext.TradeNo)
	if err != nil {
		return err
	}
	held := heldEarning.Add(heldMargin)
	if !held.IsPositive() {
		return cancelPendingGiftFulfillmentOnFullRefundTx(tx, ext, reclaimedForCancel, denomForCancel)
	}
	toRelease := unusedUSD
	if toRelease.GreaterThan(held) || fullRelease {
		toRelease = held
	}
	if !toRelease.IsPositive() {
		return cancelPendingGiftFulfillmentOnFullRefundTx(tx, ext, reclaimedForCancel, denomForCancel)
	}
	relEarning := decimal.Zero
	if held.IsPositive() && heldEarning.IsPositive() {
		relEarning = toRelease.Mul(heldEarning).Div(held).RoundFloor(8)
		if relEarning.GreaterThan(heldEarning) {
			relEarning = heldEarning
		}
	}
	relMargin := toRelease.Sub(relEarning)
	if relMargin.GreaterThan(heldMargin) {
		relMargin = heldMargin
		relEarning = toRelease.Sub(relMargin)
		if relEarning.GreaterThan(heldEarning) {
			relEarning = heldEarning
		}
	}
	already, err := adtyWalletIdempotencyExists(tx, key+":earn:refund-release", key+":margin:refund-release")
	if err != nil {
		return err
	}
	// R6-018: Ensure canonical earnings -> margin lock order even for margin-only reserves.
	if relMargin.IsPositive() && !relEarning.IsPositive() && tx.Migrator().HasTable(&model.AdtySiteWallet{}) {
		var prelockEarnings model.AdtySiteWallet
		if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND kind = ?", ext.SiteId, model.AdtySiteWalletEarnings).Limit(1).Find(&prelockEarnings).Error; err != nil {
			return err
		}
	}
	if relEarning.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, ext.SiteId, model.AdtySiteWalletEarnings, decimal.Zero, AdtySiteWalletChange{
			Type: "gift_release", RefType: "topup", RefID: ext.TradeNo, IdempotencyKey: key + ":earn:refund-release",
			HoldDelta: relEarning.Neg(),
		}); err != nil {
			return err
		}
	}
	if relMargin.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, ext.SiteId, model.AdtySiteWalletMargin, decimal.Zero, AdtySiteWalletChange{
			Type: "gift_release", RefType: "topup", RefID: ext.TradeNo, IdempotencyKey: key + ":margin:refund-release",
			HoldDelta: relMargin.Neg(),
		}); err != nil {
			return err
		}
	}
	if !already && (relEarning.IsPositive() || relMargin.IsPositive()) {
		if _, err := appendAdtyGiftOfficialAuditTx(tx, ext.UserId, ext.SiteId, ext.TradeNo, "adty.gift.release", "unused gift guarantee released on refund",
			map[string]any{"earning": heldEarning.String(), "margin": heldMargin.String()},
			map[string]any{
				"trade_no": ext.TradeNo, "earning": relEarning.String(), "margin": relMargin.String(),
				"phase": "refund",
			}); err != nil {
			return err
		}
	}
	return cancelPendingGiftFulfillmentOnFullRefundTx(tx, ext, reclaimedForCancel, denomForCancel)
}

func netGiftHoldByKindTx(tx *gorm.DB, siteID int, tradeNo string) (earning, margin decimal.Decimal, err error) {
	var rows []model.AdtySiteWalletLedger
	if err = tx.Where("site_id = ? AND ref_type = ? AND ref_id = ? AND type IN ?",
		siteID, "topup", tradeNo, adtyGiftNetHoldTypes()).
		Find(&rows).Error; err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	for _, row := range rows {
		switch row.WalletKind {
		case model.AdtySiteWalletEarnings:
			earning = earning.Add(row.HoldDelta)
		case model.AdtySiteWalletMargin:
			margin = margin.Add(row.HoldDelta)
		}
	}
	if earning.IsNegative() {
		earning = decimal.Zero
	}
	if margin.IsNegative() {
		margin = decimal.Zero
	}
	return earning, margin, nil
}

func cancelPendingGiftFulfillmentOnFullRefundTx(tx *gorm.DB, ext *model.AdtyTopupOrder, reclaimed, bonusQuota int64) error {
	if ext == nil {
		return nil
	}
	fullPrincipal := ext.FaceQuota() > 0 && ext.RefundQuota >= ext.FaceQuota()
	fullGift := bonusQuota > 0 && reclaimed >= bonusQuota
	if !fullPrincipal && !fullGift {
		if ext.FaceQuota() <= 0 || ext.RefundQuota <= 0 || !tx.Migrator().HasTable(&model.AdtyGiftFulfillment{}) {
			return nil
		}
		var row model.AdtyGiftFulfillment
		if err := model.AdtyLockForUpdate(tx).
			Where("trade_no = ? AND status IN ?", ext.TradeNo, []string{model.AdtyGiftFulfillmentPending, model.AdtyGiftFulfillmentPendingReview}).
			Limit(1).Find(&row).Error; err != nil {
			return err
		}
		if row.Id == 0 {
			return nil
		}
		// R4-015: Support main-site (SiteId == 0) pending_review bonus shrinkage on partial refund.
		origBonusUSD := adtyBonusUSDForSite(ext)
		if ext.SiteId <= 0 {
			origBonusUSD = decimal.Max(ext.BonusUSD, decimal.Zero)
		}
		refundedUSD := decimal.NewFromInt(ext.RefundQuota).Div(common.QuotaPerUnitDecimal())
		reclaimedUSD := model.CumulativeGiftReclaim(origBonusUSD, ext.FaceUSD, decimal.Zero, refundedUSD)
		shrunkUSD := origBonusUSD.Sub(reclaimedUSD)
		if shrunkUSD.IsNegative() {
			shrunkUSD = decimal.Zero
		}
		shrunkQuotaDec, err := common.WalletQuotaFromDecimalStrict(shrunkUSD.Mul(common.QuotaPerUnitDecimal()).Floor())
		if err != nil {
			return err
		}
		shrunkQuota := int64(shrunkQuotaDec)
		if shrunkQuota <= 0 || !shrunkUSD.IsPositive() {
			if err := tx.Model(&model.AdtyGiftFulfillment{}).
				Where("id = ?", row.Id).
				Updates(map[string]any{"status": model.AdtyGiftFulfillmentCanceled, "reason": "partial refund left no remaining gift"}).Error; err != nil {
				return err
			}
			ext.GiftFulfillmentStatus = model.AdtyGiftFulfillmentCanceled
			return tx.Model(&model.AdtyTopupOrder{}).
				Where("id = ?", ext.Id).
				Update("gift_fulfillment_status", model.AdtyGiftFulfillmentCanceled).Error
		}
		if shrunkUSD.LessThan(row.BonusUSD) || shrunkQuota < row.BonusQuota {
			history := adtyGiftFulfillmentHistory(row.History, map[string]any{
				"at":           time.Now().Unix(),
				"action":       "shrunk_on_partial_refund",
				"bonus_usd":    shrunkUSD.String(),
				"bonus_quota":  shrunkQuota,
				"refund_quota": ext.RefundQuota,
			})
			return tx.Model(&model.AdtyGiftFulfillment{}).Where("id = ?", row.Id).Updates(map[string]any{
				"bonus_usd":   shrunkUSD,
				"bonus_quota": shrunkQuota,
				"history":     history,
			}).Error
		}
		return nil
	}
	res := tx.Model(&model.AdtyGiftFulfillment{}).
		Where("trade_no = ? AND status IN ?", ext.TradeNo, []string{model.AdtyGiftFulfillmentPending, model.AdtyGiftFulfillmentPendingReview}).
		Updates(map[string]any{"status": model.AdtyGiftFulfillmentCanceled, "reason": "full refund cancelled pending fulfillment"})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	if err := tx.Model(&model.AdtyTopupOrder{}).Where("id = ?", ext.Id).Update("gift_fulfillment_status", model.AdtyGiftFulfillmentCanceled).Error; err != nil {
		return err
	}
	ext.GiftFulfillmentStatus = model.AdtyGiftFulfillmentCanceled
	return nil
}

// preLockGiftSpendSiteWalletsTx locks all involved site earnings wallets in site_id ASC
// order followed by all involved site margin wallets in site_id ASC order (R6-008).
func preLockGiftSpendSiteWalletsTx(tx *gorm.DB, userID int, items []model.AdtyBonusSpend) error {
	if tx == nil || userID <= 0 || len(items) == 0 || !tx.Migrator().HasTable(&model.AdtySiteWallet{}) {
		return nil
	}
	sources := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.Source == "" {
			continue
		}
		if _, ok := seen[item.Source]; !ok {
			seen[item.Source] = struct{}{}
			sources = append(sources, item.Source)
		}
	}
	if len(sources) == 0 {
		return nil
	}
	var siteIDs []int
	if err := tx.Model(&model.AdtyTopupOrder{}).
		Distinct("site_id").
		Where("user_id = ? AND trade_no IN ? AND site_id > 0", userID, sources).
		Pluck("site_id", &siteIDs).Error; err != nil {
		return err
	}
	if len(siteIDs) == 0 {
		return nil
	}
	sort.Ints(siteIDs)
	var locked []model.AdtySiteWallet
	if err := model.AdtyLockForUpdate(tx).
		Where("site_id IN ? AND kind = ?", siteIDs, model.AdtySiteWalletEarnings).
		Order("site_id ASC").
		Find(&locked).Error; err != nil {
		return err
	}
	return model.AdtyLockForUpdate(tx).
		Where("site_id IN ? AND kind = ?", siteIDs, model.AdtySiteWalletMargin).
		Order("site_id ASC").
		Find(&locked).Error
}

func applySiteGiftSpendOnBonusTx(tx *gorm.DB, userID int, spends []model.AdtyBonusSpend) error {
	if err := preLockGiftSpendSiteWalletsTx(tx, userID, spends); err != nil {
		return err
	}
	for _, spend := range spends {
		if err := applySiteGiftSpendForSourceTx(tx, userID, spend.Source, spend.Quota); err != nil {
			return err
		}
	}
	return nil
}

func applySiteGiftSpendForSourceTx(tx *gorm.DB, userID int, tradeNo string, takenQuota int64) error {
	if tx == nil || userID <= 0 || tradeNo == "" || takenQuota <= 0 {
		return nil
	}
	var ext model.AdtyTopupOrder
	if err := tx.Where("trade_no = ? AND user_id = ?", tradeNo, userID).Limit(1).Find(&ext).Error; err != nil {
		return err
	}
	bonus := adtyBonusUSDForSite(&ext)
	if ext.Id == 0 || ext.SiteId <= 0 || !bonus.IsPositive() {
		return nil
	}
	if ext.GiftFulfillmentStatus == model.AdtyGiftFulfillmentPending || ext.GiftFulfillmentStatus == model.AdtyGiftFulfillmentPendingReview {
		return nil
	}
	heldEarning, heldMargin, err := netGiftHoldByKindTx(tx, ext.SiteId, ext.TradeNo)
	if err != nil {
		return err
	}
	held := heldEarning.Add(heldMargin)
	if !held.IsPositive() {
		return nil
	}
	siteQuota, err := siteGiftBonusQuota(&ext)
	if err != nil || siteQuota <= 0 {
		return err
	}
	var remaining int64
	if err := tx.Model(&model.AdtyFundBatch{}).
		Where("user_id = ? AND source = ? AND kind = ?", userID, tradeNo, "bonus").
		Select("COALESCE(SUM(remaining),0)").Scan(&remaining).Error; err != nil {
		return err
	}
	toSpend := bonus.Mul(decimal.NewFromInt(takenQuota)).Div(decimal.NewFromInt(siteQuota)).RoundFloor(8)
	if remaining <= 0 || toSpend.GreaterThan(held) {
		toSpend = held
	}
	if !toSpend.IsPositive() {
		return nil
	}
	relEarning := decimal.Zero
	if heldEarning.IsPositive() {
		relEarning = toSpend.Mul(heldEarning).Div(held).RoundFloor(8)
		if relEarning.GreaterThan(heldEarning) {
			relEarning = heldEarning
		}
	}
	relMargin := toSpend.Sub(relEarning)
	if relMargin.GreaterThan(heldMargin) {
		relMargin = heldMargin
		relEarning = toSpend.Sub(relMargin)
		if relEarning.GreaterThan(heldEarning) {
			relEarning = heldEarning
		}
	}
	var spentAfter int64
	if err := tx.Model(&model.AdtyFundBatch{}).
		Where("user_id = ? AND source = ? AND kind = ?", userID, tradeNo, "bonus").
		Select("COALESCE(SUM(original - remaining),0)").Scan(&spentAfter).Error; err != nil {
		return err
	}
	var priorRefunds int64
	if err := tx.Model(&model.AdtySiteWalletLedger{}).
		Where("site_id = ? AND ref_type = ? AND ref_id = ? AND type = ? AND delta > 0", ext.SiteId, "topup", tradeNo, "gift_spend").
		Count(&priorRefunds).Error; err != nil {
		return err
	}
	key := "gift:" + tradeNo + ":spend:" + strconv.FormatInt(spentAfter, 10)
	if priorRefunds > 0 {
		key += ":r" + strconv.FormatInt(priorRefunds, 10)
	}
	already, err := adtyWalletIdempotencyExists(tx, key+":earn", key+":margin")
	if err != nil {
		return err
	}
	if relEarning.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, ext.SiteId, model.AdtySiteWalletEarnings, relEarning.Neg(), AdtySiteWalletChange{
			Type: "gift_spend", RefType: "topup", RefID: tradeNo, IdempotencyKey: key + ":earn",
			HoldDelta: relEarning.Neg(),
		}); err != nil {
			return err
		}
	}
	if relMargin.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, ext.SiteId, model.AdtySiteWalletMargin, relMargin.Neg(), AdtySiteWalletChange{
			Type: "gift_spend", RefType: "topup", RefID: tradeNo, IdempotencyKey: key + ":margin",
			HoldDelta: relMargin.Neg(),
		}); err != nil {
			return err
		}
	}
	if !already && (relEarning.IsPositive() || relMargin.IsPositive()) {
		if _, err := appendAdtyGiftOfficialAuditTx(tx, userID, ext.SiteId, tradeNo, "adty.gift.spend", "",
			map[string]any{"earning": heldEarning.String(), "margin": heldMargin.String()},
			map[string]any{
				"trade_no": tradeNo, "earning": relEarning.String(), "margin": relMargin.String(),
				"taken_quota": takenQuota,
			}); err != nil {
			return err
		}
	}
	return nil
}

func releaseSiteGiftGuaranteeOnBonusReductionTx(tx *gorm.DB, userID int, releases []model.AdtyBonusSpend) error {
	if err := preLockGiftSpendSiteWalletsTx(tx, userID, releases); err != nil {
		return err
	}
	for _, item := range releases {
		if err := releaseSiteGiftGuaranteeForSourceTx(tx, userID, item.Source, item.Quota); err != nil {
			return err
		}
	}
	return nil
}

func releaseSiteGiftGuaranteeForSourceTx(tx *gorm.DB, userID int, tradeNo string, reducedQuota int64) error {
	if tx == nil || userID <= 0 || tradeNo == "" || reducedQuota <= 0 {
		return nil
	}
	var ext model.AdtyTopupOrder
	if err := tx.Where("trade_no = ? AND user_id = ?", tradeNo, userID).Limit(1).Find(&ext).Error; err != nil {
		return err
	}
	if ext.Id == 0 || ext.SiteId <= 0 {
		return nil
	}
	if ext.GiftFulfillmentStatus == model.AdtyGiftFulfillmentPending || ext.GiftFulfillmentStatus == model.AdtyGiftFulfillmentPendingReview {
		return nil
	}
	bonus, siteQuota, err := effectiveSiteGiftBasisTx(tx, &ext)
	if err != nil || !bonus.IsPositive() || siteQuota <= 0 {
		return err
	}
	heldEarning, heldMargin, err := netGiftHoldByKindTx(tx, ext.SiteId, ext.TradeNo)
	if err != nil {
		return err
	}
	held := heldEarning.Add(heldMargin)
	if !held.IsPositive() {
		return nil
	}
	var remaining int64
	if err := tx.Model(&model.AdtyFundBatch{}).
		Where("user_id = ? AND source = ? AND kind = ?", userID, tradeNo, model.AdtyFundBatchKindBonus).
		Select("COALESCE(SUM(remaining),0)").Scan(&remaining).Error; err != nil {
		return err
	}
	toRelease := bonus.Mul(decimal.NewFromInt(reducedQuota)).Div(decimal.NewFromInt(siteQuota)).RoundFloor(8)
	if remaining <= 0 || toRelease.GreaterThan(held) {
		toRelease = held
	}
	if !toRelease.IsPositive() {
		return nil
	}
	relEarning := decimal.Zero
	if heldEarning.IsPositive() {
		relEarning = toRelease.Mul(heldEarning).Div(held).RoundFloor(8)
		if relEarning.GreaterThan(heldEarning) {
			relEarning = heldEarning
		}
	}
	relMargin := toRelease.Sub(relEarning)
	if relMargin.GreaterThan(heldMargin) {
		relMargin = heldMargin
		relEarning = toRelease.Sub(relMargin)
		if relEarning.GreaterThan(heldEarning) {
			relEarning = heldEarning
		}
	}
	var spentAfter int64
	if err := tx.Model(&model.AdtyFundBatch{}).
		Where("user_id = ? AND source = ? AND kind = ?", userID, tradeNo, model.AdtyFundBatchKindBonus).
		Select("COALESCE(SUM(original - remaining),0)").Scan(&spentAfter).Error; err != nil {
		return err
	}
	var priorReleases int64
	if err := tx.Model(&model.AdtySiteWalletLedger{}).
		Where("site_id = ? AND ref_type = ? AND ref_id = ? AND type = ?", ext.SiteId, "topup", tradeNo, "gift_release").
		Count(&priorReleases).Error; err != nil {
		return err
	}
	key := "gift:" + tradeNo + ":adjust-release:" + strconv.FormatInt(spentAfter, 10) + ":n" + strconv.FormatInt(priorReleases, 10)
	already, err := adtyWalletIdempotencyExists(tx, key+":earn", key+":margin")
	if err != nil {
		return err
	}
	if relEarning.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, ext.SiteId, model.AdtySiteWalletEarnings, decimal.Zero, AdtySiteWalletChange{
			Type: "gift_release", RefType: "topup", RefID: tradeNo, IdempotencyKey: key + ":earn",
			HoldDelta: relEarning.Neg(),
		}); err != nil {
			return err
		}
	}
	if relMargin.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, ext.SiteId, model.AdtySiteWalletMargin, decimal.Zero, AdtySiteWalletChange{
			Type: "gift_release", RefType: "topup", RefID: tradeNo, IdempotencyKey: key + ":margin",
			HoldDelta: relMargin.Neg(),
		}); err != nil {
			return err
		}
	}
	if !already && (relEarning.IsPositive() || relMargin.IsPositive()) {
		if _, err := appendAdtyGiftOfficialAuditTx(tx, userID, ext.SiteId, tradeNo, "adty.gift.release", "unused gift guarantee released on bonus reduction",
			map[string]any{"earning": heldEarning.String(), "margin": heldMargin.String()},
			map[string]any{
				"trade_no": tradeNo, "earning": relEarning.String(), "margin": relMargin.String(),
				"reduced_quota": reducedQuota, "phase": "adjustment_release",
			}); err != nil {
			return err
		}
	}
	return nil
}

// reverseSiteGiftSpendOnBonusTx mirrors applySiteGiftSpendOnBonusTx when a settled
// non-expiring bonus share is reversed by a task/Realtime settlement refund (R3-004).
func reverseSiteGiftSpendOnBonusTx(tx *gorm.DB, userID int, operationKey string, refunds []model.AdtyBonusSpend) error {
	if err := preLockGiftSpendSiteWalletsTx(tx, userID, refunds); err != nil {
		return err
	}
	for _, item := range refunds {
		if err := reverseSiteGiftSpendForSourceTx(tx, userID, operationKey, item.Source, item.Quota); err != nil {
			return err
		}
	}
	return nil
}

func reverseSiteGiftSpendForSourceTx(tx *gorm.DB, userID int, operationKey, tradeNo string, refundedQuota int64) error {
	if tx == nil || userID <= 0 || tradeNo == "" || refundedQuota <= 0 {
		return nil
	}
	var ext model.AdtyTopupOrder
	if err := tx.Where("trade_no = ? AND user_id = ?", tradeNo, userID).Limit(1).Find(&ext).Error; err != nil {
		return err
	}
	bonus := adtyBonusUSDForSite(&ext)
	if ext.Id == 0 || ext.SiteId <= 0 || !bonus.IsPositive() {
		return nil
	}
	var rows []model.AdtySiteWalletLedger
	if err := tx.Where("site_id = ? AND ref_type = ? AND ref_id = ? AND type = ?", ext.SiteId, "topup", tradeNo, "gift_spend").
		Find(&rows).Error; err != nil {
		return err
	}
	spentEarning := decimal.Zero
	spentMargin := decimal.Zero
	for _, row := range rows {
		switch row.WalletKind {
		case model.AdtySiteWalletEarnings:
			spentEarning = spentEarning.Add(row.Delta.Neg())
		case model.AdtySiteWalletMargin:
			spentMargin = spentMargin.Add(row.Delta.Neg())
		}
	}
	if spentEarning.IsNegative() {
		spentEarning = decimal.Zero
	}
	if spentMargin.IsNegative() {
		spentMargin = decimal.Zero
	}
	totalSpent := spentEarning.Add(spentMargin)
	if !totalSpent.IsPositive() {
		return nil
	}
	siteQuota, err := siteGiftBonusQuota(&ext)
	if err != nil || siteQuota <= 0 {
		return err
	}
	var netSpentQuota int64
	if err := tx.Model(&model.AdtyFundBatch{}).
		Where("user_id = ? AND source = ? AND kind = ?", userID, tradeNo, "bonus").
		Select("COALESCE(SUM(original - remaining),0)").Scan(&netSpentQuota).Error; err != nil {
		return err
	}
	toRestore := bonus.Mul(decimal.NewFromInt(refundedQuota)).Div(decimal.NewFromInt(siteQuota)).RoundFloor(8)
	if netSpentQuota <= 0 || toRestore.GreaterThan(totalSpent) {
		toRestore = totalSpent
	}
	if !toRestore.IsPositive() {
		return nil
	}
	restEarning := decimal.Zero
	if spentEarning.IsPositive() {
		restEarning = toRestore.Mul(spentEarning).Div(totalSpent).RoundFloor(8)
		if restEarning.GreaterThan(spentEarning) {
			restEarning = spentEarning
		}
	}
	restMargin := toRestore.Sub(restEarning)
	if restMargin.GreaterThan(spentMargin) {
		restMargin = spentMargin
		restEarning = toRestore.Sub(restMargin)
		if restEarning.GreaterThan(spentEarning) {
			restEarning = spentEarning
		}
	}
	sum := sha256.Sum256([]byte(tradeNo + "|" + operationKey))
	key := "gift-rev:" + hex.EncodeToString(sum[:12])
	already, err := adtyWalletIdempotencyExists(tx, key+":earn", key+":margin")
	if err != nil {
		return err
	}
	if restEarning.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, ext.SiteId, model.AdtySiteWalletEarnings, restEarning, AdtySiteWalletChange{
			Type: "gift_spend", RefType: "topup", RefID: tradeNo, IdempotencyKey: key + ":earn",
			HoldDelta: restEarning, AllowOverdraft: true,
		}); err != nil {
			return err
		}
	}
	if restMargin.IsPositive() {
		if _, err := applyAdtySiteWalletChangeTx(tx, ext.SiteId, model.AdtySiteWalletMargin, restMargin, AdtySiteWalletChange{
			Type: "gift_spend", RefType: "topup", RefID: tradeNo, IdempotencyKey: key + ":margin",
			HoldDelta: restMargin, AllowOverdraft: true,
		}); err != nil {
			return err
		}
	}
	if !already && (restEarning.IsPositive() || restMargin.IsPositive()) {
		if _, err := appendAdtyGiftOfficialAuditTx(tx, userID, ext.SiteId, tradeNo, "adty.gift.spend_refund", "",
			map[string]any{"spent_earning": spentEarning.String(), "spent_margin": spentMargin.String()},
			map[string]any{
				"trade_no": tradeNo, "operation_key": operationKey,
				"earning": restEarning.String(), "margin": restMargin.String(),
				"refunded_quota": refundedQuota,
			}); err != nil {
			return err
		}
	}
	return nil
}

// ListAdtyGiftFulfillments returns gift fulfillment records for Root finance review (R2-039).
func ListAdtyGiftFulfillments(status string, siteID int, pageInfo *common.PageInfo) ([]model.AdtyGiftFulfillment, int64, error) {
	if model.DB == nil {
		return nil, 0, common.NewMessage("Database is unavailable")
	}
	if !model.DB.Migrator().HasTable(&model.AdtyGiftFulfillment{}) {
		return []model.AdtyGiftFulfillment{}, 0, nil
	}
	q := model.DB.Model(&model.AdtyGiftFulfillment{})
	status = strings.TrimSpace(status)
	if status != "" && status != "all" {
		switch status {
		case model.AdtyGiftFulfillmentPendingReview, model.AdtyGiftFulfillmentPending, model.AdtyGiftFulfillmentFulfilled, model.AdtyGiftFulfillmentCanceled:
			q = q.Where("status = ?", status)
		default:
			return nil, 0, common.NewMessage("Invalid parameters")
		}
	}
	if siteID > 0 {
		q = q.Where("site_id = ?", siteID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.AdtyGiftFulfillment
	if pageInfo == nil {
		pageInfo = &common.PageInfo{Page: 1, PageSize: 20}
	}
	if err := q.Order("id DESC").Offset(pageInfo.GetStartIdx()).Limit(pageInfo.PageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ReviewAdtyGiftFulfillment resolves a pending_review gift fulfillment record (R2-039).
// action="approve" transitions pending_review -> pending and immediately attempts fulfillment.
// action="reject" transitions pending_review -> canceled and syncs adty_topup_orders.
func ReviewAdtyGiftFulfillment(id int64, action, note string, operatorID int) (*model.AdtyGiftFulfillment, bool, error) {
	row, fulfilled, _, err := ReviewAdtyGiftFulfillmentWithAudit(id, action, note, operatorID)
	return row, fulfilled, err
}

func ReviewAdtyGiftFulfillmentWithAudit(id int64, action, note string, operatorID int) (*model.AdtyGiftFulfillment, bool, string, error) {
	if model.DB == nil || id <= 0 || operatorID <= 0 {
		return nil, false, "", common.NewMessage("Invalid parameters")
	}
	action = strings.ToLower(strings.TrimSpace(action))
	note = strings.TrimSpace(note)
	if action != "approve" && action != "reject" {
		return nil, false, "", common.NewMessage("Invalid parameters")
	}
	if action == "reject" && note == "" {
		return nil, false, "", common.NewMessage("Please enter a review reason")
	}
	var row model.AdtyGiftFulfillment
	var beforeStatus string
	var auditEntry *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		// R3-003: Pre-read IDs unlocked, then lock in canonical order:
		// AdtySite -> AdtyTopupOrder -> AdtyGiftFulfillment.
		var peek model.AdtyGiftFulfillment
		if err := tx.Select("id", "site_id", "trade_no").First(&peek, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return common.NewMessage("Order not found")
			}
			return err
		}
		if peek.SiteId > 0 {
			var site model.AdtySite
			if err := model.AdtyLockForUpdate(tx).Where("id = ?", peek.SiteId).Limit(1).Find(&site).Error; err != nil {
				return err
			}
		}
		var ext model.AdtyTopupOrder
		if err := model.AdtyLockForUpdate(tx).Where("trade_no = ?", peek.TradeNo).Limit(1).Find(&ext).Error; err != nil {
			return err
		}
		if err := model.AdtyLockForUpdate(tx).First(&row, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return common.NewMessage("Order not found")
			}
			return err
		}
		if row.Status != model.AdtyGiftFulfillmentPendingReview {
			return common.NewMessage("Only pending review records can be reviewed")
		}
		if action == "approve" {
			hasActiveRefund, err := model.HasActiveManualRefundOccupancyTx(tx, row.TradeNo)
			if err != nil {
				return err
			}
			if hasActiveRefund {
				return common.NewMessage("Complete or terminate the active refund before approving this gift")
			}
		}
		beforeStatus = row.Status
		nextStatus := model.AdtyGiftFulfillmentPending
		nextReason := "approved by root review"
		if note != "" {
			nextReason = note
		}
		if action == "reject" {
			nextStatus = model.AdtyGiftFulfillmentCanceled
		}
		history := adtyGiftFulfillmentHistory(row.History, map[string]any{
			"at":          time.Now().Unix(),
			"action":      "review_" + action,
			"operator_id": operatorID,
			"status":      nextStatus,
			"reason":      nextReason,
		})
		if err := tx.Model(&row).Updates(map[string]any{
			"status":  nextStatus,
			"reason":  nextReason,
			"history": history,
		}).Error; err != nil {
			return err
		}
		row.Status = nextStatus
		row.Reason = nextReason
		row.History = history
		if err := tx.Model(&model.AdtyTopupOrder{}).
			Where("trade_no = ?", row.TradeNo).
			Update("gift_fulfillment_status", nextStatus).Error; err != nil {
			return err
		}
		var auditErr error
		auditEntry, auditErr = appendAdtyGiftOfficialAuditTx(tx, operatorID, row.SiteId, row.TradeNo, "adty.gift.fulfillment.review", note,
			map[string]any{"id": row.Id, "status": beforeStatus},
			map[string]any{"id": row.Id, "trade_no": row.TradeNo, "action": action, "status": row.Status})
		return auditErr
	})
	if err != nil {
		return nil, false, "", err
	}
	eventID := ""
	if auditEntry != nil {
		eventID = auditEntry.EventId
		deliverAdtyCommittedAudit(*auditEntry)
	}
	fulfilled := false
	if action == "approve" {
		done, fErr := fulfillPendingAdtyGiftFulfillment(row.TradeNo)
		if fErr != nil && !errors.Is(fErr, errGiftGuaranteeShort) && !errors.Is(fErr, ErrAdtySiteWalletInsufficient) {
			common.SysLog(common.LogText("gift fulfillment: post-approve fulfill %s: %s", row.TradeNo, fErr.Error()))
		}
		fulfilled = done
		var refreshed model.AdtyGiftFulfillment
		if err := model.DB.First(&refreshed, row.Id).Error; err == nil {
			row = refreshed
		}
	}
	return &row, fulfilled, eventID, nil
}
