package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ErrAdtyFundBatchShortfall is returned when FIFO lots cannot cover a debit.
var ErrAdtyFundBatchShortfall = errors.New("fund batches cannot cover the debit")

// ErrAdtyFundBatchTableMissing is returned when fund batches cannot be written
// or consumed because the batch table is absent.
var ErrAdtyFundBatchTableMissing = errors.New("adty fund batch table is missing")

const (
	AdtyFundBatchKindPrincipal     = "principal"
	AdtyFundBatchKindBonus         = "bonus"
	AdtyFundBatchKindExpiringBonus = "expiring_bonus"

	AdtyGiftCategoryPermanent = "permanent_bonus"
	AdtyGiftCategoryExpiring  = "expiring_bonus"

	AdtyFundBatchStatusActive    = "active"
	AdtyFundBatchStatusExhausted = "exhausted"
	AdtyFundBatchStatusExpired   = "expired"

	AdtyLedgerTypeGiftExpire       = "gift_expire"
	AdtyLedgerTypeRootExpiringGift = "root_expiring_gift"
)

func adtyFundBatchShortfallError(uncovered int64) error {
	return fmt.Errorf("%w: uncovered %d", ErrAdtyFundBatchShortfall, uncovered)
}

// AdtyFundBatch is one recharge principal, permanent recharge bonus, or
// time-limited Root gift lot (DEC-031 / G.3 / A.12).
//
// SiteId 是这批额度归属的站（0 是主站）。A.12.1 要求批次自带站点；
// 指针为 nil 表示「证不出站点」：历史行与没有订单/凭据可查的补建额度留 NULL，
// 不写推断值——推断值一旦落库就再也分不清真假。
type AdtyFundBatch struct {
	Id           int64  `json:"id"`
	UserId       int    `json:"user_id" gorm:"uniqueIndex:idx_adty_fund_batch_user_source_kind,priority:1;index"`
	SiteId       *int   `json:"site_id,omitempty" gorm:"index"`
	Kind         string `json:"kind" gorm:"type:varchar(16);uniqueIndex:idx_adty_fund_batch_user_source_kind,priority:3;index"`
	GiftCategory string `json:"gift_category,omitempty" gorm:"type:varchar(32);index"`
	Source       string `json:"source" gorm:"type:varchar(64);uniqueIndex:idx_adty_fund_batch_user_source_kind,priority:2;index"`
	Original     int64  `json:"original"`
	Remaining    int64  `json:"remaining"`
	ExpiredQuota int64  `json:"expired_quota,omitempty" gorm:"not null;default:0"`
	ExpiresAt    *int64 `json:"expires_at,omitempty" gorm:"index"`
	ExpiredAt    int64  `json:"expired_at,omitempty" gorm:"not null;default:0"`
	Status       string `json:"status,omitempty" gorm:"type:varchar(16);index;not null;default:'active'"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (AdtyFundBatch) TableName() string { return "adty_fund_batches" }

// IsExpiringGift reports whether this batch is a time-limited gift lot (DEC-031)
// rather than principal or permanent recharge bonus.
func (b AdtyFundBatch) IsExpiringGift() bool {
	if b.Kind == AdtyFundBatchKindPrincipal {
		return false
	}
	if b.Kind == AdtyFundBatchKindExpiringBonus || b.GiftCategory == AdtyGiftCategoryExpiring {
		return true
	}
	return b.ExpiresAt != nil && *b.ExpiresAt > 0
}

// adtyMissingFundBatchKinds 返回 wanted 里按 (user_id, source, kind) 还不存在的那些行——
// 与唯一索引 idx_adty_fund_batch_user_source_kind 同一个维度。
func adtyMissingFundBatchKinds(tx *gorm.DB, userID int, source string, wanted []AdtyFundBatch) ([]AdtyFundBatch, error) {
	kinds := make([]string, 0, len(wanted))
	for _, row := range wanted {
		kinds = append(kinds, row.Kind)
	}
	var present []string
	if err := tx.Model(&AdtyFundBatch{}).
		Where("user_id = ? AND source = ? AND kind IN ?", userID, source, kinds).
		Pluck("kind", &present).Error; err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(present))
	for _, kind := range present {
		seen[kind] = true
	}
	missing := make([]AdtyFundBatch, 0, len(wanted))
	for _, row := range wanted {
		if !seen[row.Kind] {
			missing = append(missing, row)
		}
	}
	return missing, nil
}

// CreateAdtyFundBatchesTx records the principal and bonus lots for one credit.
// A replay of the same source does not insert a second pair.
// 站点不可知的调用方用它：批次落 NULL，不猜。
//
// 幂等判据是 (user_id, source, kind)，和唯一索引同维度：已经记过的 kind 不会重复插入，
// 但同一 source 下还缺的 kind 会补建。旧版只用 (user_id, source) 计数，existing > 0
// 就 return nil，于是「先只传 principal、后补 bonus」这种调用被静默吞掉、函数还返回成功
// （ISS-R4-0025）。
//
// 调用约束：一个 source 对应一次入账事件。同一 source 下分两次调用、各传一种 kind 是
// 允许的（第二次补建缺的那一种）；但本函数只按行去重，不比较金额——重复调用不会把
// remaining 改成新金额。新增调用点若自带新 source，必须自己保证这个 source 只有一个
// 「首次写入者」，否则第二次调用会被当成重放。
func CreateAdtyFundBatchesTx(tx *gorm.DB, userID int, source string, principal, bonus int64) error {
	return CreateAdtyFundBatchesWithSiteTx(tx, userID, nil, source, principal, bonus)
}

// AdtyFundBatchSiteID 把已知站点变成写进批次的指针。0 是主站，是具体值而不是
// 「未知」；未知只能传 nil。
func AdtyFundBatchSiteID(siteID int) *int { return &siteID }

// CreateAdtyFundBatchesWithSiteTx 同 CreateAdtyFundBatchesTx，并记下这批额度的
// 站点（ISS-R4-0047）。
//
// 站点是批次自带的属性，不是每次查询时反推的：站迁移时批次必须跟着客户一起搬
// （见 AdtySetUsersSiteTx），所以站点一旦写入就由账本自己维护。
func CreateAdtyFundBatchesWithSiteTx(tx *gorm.DB, userID int, siteID *int, source string, principal, bonus int64) error {
	if tx == nil || userID <= 0 || source == "" {
		return nil
	}
	if err := adtyRequireFundBatchTable(tx); err != nil {
		return err
	}
	now := common.GetTimestamp()
	wanted := make([]AdtyFundBatch, 0, 2)
	if principal > 0 {
		wanted = append(wanted, AdtyFundBatch{
			UserId:    userID,
			SiteId:    siteID,
			Kind:      AdtyFundBatchKindPrincipal,
			Source:    source,
			Original:  principal,
			Remaining: principal,
			Status:    AdtyFundBatchStatusActive,
			CreatedAt: now,
		})
	}
	if bonus > 0 {
		wanted = append(wanted, AdtyFundBatch{
			UserId:       userID,
			SiteId:       siteID,
			Kind:         AdtyFundBatchKindBonus,
			GiftCategory: AdtyGiftCategoryPermanent,
			Source:       source,
			Original:     bonus,
			Remaining:    bonus,
			Status:       AdtyFundBatchStatusActive,
			CreatedAt:    now,
		})
	}
	if len(wanted) == 0 {
		return nil
	}
	missing, err := adtyMissingFundBatchKinds(tx, userID, source, wanted)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	// Insert under a SAVEPOINT so a concurrent unique conflict does not
	// abort the caller's PostgreSQL transaction.
	err = tx.Transaction(func(inner *gorm.DB) error {
		return inner.Create(&missing).Error
	})
	if err == nil {
		return nil
	}
	if !isAdtyUniqueViolation(err) {
		return err
	}
	// 并发下另一个事务先插了其中一行。只有「这一笔想要的每种 kind 现在都在库里」
	// 才算幂等成功；缺任何一种就是这笔入账少落了一行，必须返回错误让调用方回滚，
	// 不能像旧代码那样把冲突一律吞成 nil。
	still, queryErr := adtyMissingFundBatchKinds(tx, userID, source, wanted)
	if queryErr != nil {
		return queryErr
	}
	if len(still) == 0 {
		return nil
	}
	return fmt.Errorf("adty fund batch insert conflicted and %d kind(s) are still missing: %w", len(still), err)
}

// CreateAdtyExpiringBonusBatchTx records one time-limited gift lot (DEC-031)
// distinct from permanent recharge bonus.
func CreateAdtyExpiringBonusBatchTx(tx *gorm.DB, userID int, siteID *int, source string, bonusQuota int64, expiresAt int64) (*AdtyFundBatch, error) {
	if tx == nil {
		return nil, errors.New("transaction is required")
	}
	source = strings.TrimSpace(source)
	if userID <= 0 || source == "" || bonusQuota <= 0 || expiresAt <= 0 {
		return nil, errors.New("invalid expiring gift batch parameters")
	}
	if err := adtyRequireFundBatchTable(tx); err != nil {
		return nil, err
	}
	var existing AdtyFundBatch
	err := tx.Where("user_id = ? AND source = ? AND kind IN ?", userID, source, []string{AdtyFundBatchKindBonus, AdtyFundBatchKindExpiringBonus}).
		First(&existing).Error
	if err == nil {
		if existing.Original != bonusQuota || existing.ExpiresAt == nil || *existing.ExpiresAt != expiresAt || !existing.IsExpiringGift() {
			return nil, ErrAdtyIdempotencyConflict
		}
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	exp := expiresAt
	row := AdtyFundBatch{
		UserId:       userID,
		SiteId:       siteID,
		Kind:         AdtyFundBatchKindBonus,
		GiftCategory: AdtyGiftCategoryExpiring,
		Source:       source,
		Original:     bonusQuota,
		Remaining:    bonusQuota,
		ExpiresAt:    &exp,
		Status:       AdtyFundBatchStatusActive,
		CreatedAt:    common.GetTimestamp(),
	}
	createErr := tx.Transaction(func(inner *gorm.DB) error {
		return inner.Create(&row).Error
	})
	if createErr != nil {
		if isAdtyUniqueViolation(createErr) {
			if qErr := tx.Where("user_id = ? AND source = ? AND kind IN ?", userID, source, []string{AdtyFundBatchKindBonus, AdtyFundBatchKindExpiringBonus}).
				First(&existing).Error; qErr == nil {
				if existing.Original != bonusQuota || existing.ExpiresAt == nil || *existing.ExpiresAt != expiresAt || !existing.IsExpiringGift() {
					return nil, ErrAdtyIdempotencyConflict
				}
				return &existing, nil
			}
		}
		return nil, createErr
	}
	return &row, nil
}

// AdtyRootExpiringGiftGrantInput describes a manual Root expiring gift grant (DEC-031).
type AdtyRootExpiringGiftGrantInput struct {
	UserID         int
	OperatorID     int
	OperatorRole   int
	SiteID         *int
	AmountUSD      decimal.Decimal
	Quota          int64
	ExpiresAt      int64
	Reason         string
	IdempotencyKey string
}

// GrantAdtyRootExpiringGift issues a time-limited Root gift lot to a customer
// in a single database transaction and refreshes the user cache.
func GrantAdtyRootExpiringGift(input AdtyRootExpiringGiftGrantInput) (*AdtyFundBatch, error) {
	if DB == nil {
		return nil, errors.New("database is not initialized")
	}
	var batch *AdtyFundBatch
	err := DB.Transaction(func(tx *gorm.DB) error {
		var txErr error
		batch, txErr = GrantAdtyRootExpiringGiftTx(tx, input)
		return txErr
	})
	if err != nil {
		return nil, err
	}
	_ = invalidateUserCache(input.UserID)
	_ = PublishUserAuthCache(input.UserID)
	return batch, nil
}

// GrantAdtyRootExpiringGiftTx issues a time-limited Root gift lot inside tx,
// crediting the customer's wallet and writing an immutable balance ledger entry.
func GrantAdtyRootExpiringGiftTx(tx *gorm.DB, input AdtyRootExpiringGiftGrantInput) (*AdtyFundBatch, error) {
	batch, _, err := GrantAdtyRootExpiringGiftWithReplayTx(tx, input)
	return batch, err
}

// GrantAdtyRootExpiringGiftWithReplayTx issues a time-limited Root gift lot
// inside tx and reports whether the request matched an existing idempotent grant.
func GrantAdtyRootExpiringGiftWithReplayTx(tx *gorm.DB, input AdtyRootExpiringGiftGrantInput) (*AdtyFundBatch, bool, error) {
	if tx == nil {
		return nil, false, errors.New("transaction is required")
	}
	if input.OperatorID <= 0 || input.OperatorRole != common.RoleRootUser {
		return nil, false, errors.New("only Root can issue expiring gifts")
	}
	if input.UserID <= 0 {
		return nil, false, errors.New("invalid user id")
	}
	if input.ExpiresAt <= 0 {
		return nil, false, errors.New("expires_at is required for expiring gift")
	}
	idemKey := strings.TrimSpace(input.IdempotencyKey)
	if idemKey == "" || len(idemKey) > 255 {
		return nil, false, errors.New("idempotency key is required")
	}
	quota := input.Quota
	if quota <= 0 && input.AmountUSD.IsPositive() {
		converted, err := common.WalletQuotaFromDecimalStrict(input.AmountUSD.Mul(common.QuotaPerUnitDecimal()))
		if err != nil || converted <= 0 {
			return nil, false, errors.New("invalid gift amount")
		}
		quota = int64(converted)
	}
	if quota <= 0 || quota > int64(common.MaxWalletQuota) {
		return nil, false, errors.New("gift quota must be positive")
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "root expiring gift"
	}
	if len(reason) > 1024 {
		return nil, false, errors.New("gift reason is too long")
	}
	sum := sha256.Sum256([]byte(idemKey))
	source := fmt.Sprintf("root_gift:%x", sum[:24])
	// The same user lock serializes grants, charging and expiration. Recheck
	// idempotency only after acquiring it so a concurrent replay sees the commit.
	balanceBefore, _, _, err := loadLockedUserLogicalQuotaTx(tx, input.UserID)
	if err != nil {
		return nil, false, err
	}
	var customer User
	if err := tx.Select("id", "site_id", "staff_site_id", "role").First(&customer, input.UserID).Error; err != nil {
		return nil, false, err
	}
	if customer.Role >= common.RoleAdminUser || customer.StaffSiteId > 0 || input.SiteID != nil && *input.SiteID != customer.SiteId {
		return nil, false, errors.New("gift target must be a customer in the selected site")
	}
	if tx.Migrator().HasTable(&AdtyTeamMember{}) {
		var empCount int64
		if err := tx.Model(&AdtyTeamMember{}).Where("user_id = ? AND independent_employee = ?", customer.Id, true).Count(&empCount).Error; err != nil {
			return nil, false, err
		}
		if empCount > 0 {
			return nil, false, errors.New("gift target must be a customer in the selected site")
		}
	}
	siteID := AdtyFundBatchSiteID(customer.SiteId)

	existingLedger, err := FindAdtyBalanceLedgerByKeyTx(tx, source)
	if err != nil {
		return nil, false, err
	}
	if existingLedger != nil {
		if existingLedger.UserId != input.UserID || existingLedger.Type != AdtyLedgerTypeRootExpiringGift || existingLedger.DeltaQuota != quota || existingLedger.Reason != reason || existingLedger.OperatorId != input.OperatorID {
			return nil, false, ErrAdtyIdempotencyConflict
		}
		batch, err := CreateAdtyExpiringBonusBatchTx(tx, input.UserID, siteID, source, quota, input.ExpiresAt)
		return batch, true, err
	}
	loc := AdtyBusinessLocation(tx)
	todayStart := AdtyGiftExpiryMidnightUnix(common.GetTimestamp(), loc)
	minExpiresAt := time.Unix(todayStart, 0).In(loc).AddDate(0, 0, 1).Unix()
	if input.ExpiresAt < minExpiresAt {
		return nil, false, errors.New("gift expiry must be at least tomorrow in business timezone")
	}
	batch, err := CreateAdtyExpiringBonusBatchTx(tx, input.UserID, siteID, source, quota, input.ExpiresAt)
	if err != nil {
		return nil, false, err
	}
	if _, err := AdtyCreditUserQuotaTx(tx, input.UserID, quota); err != nil {
		return nil, false, err
	}
	ledgerKey := source
	if err := RecordAdtyBalanceLedgerTx(tx, &AdtyBalanceLedger{
		UserId:         input.UserID,
		DeltaQuota:     quota,
		BalanceBefore:  balanceBefore,
		BalanceAfter:   balanceBefore + quota,
		Type:           AdtyLedgerTypeRootExpiringGift,
		RefType:        "root_gift",
		RefId:          source,
		Reason:         reason,
		OperatorId:     input.OperatorID,
		IdempotencyKey: &ledgerKey,
	}); err != nil {
		return nil, false, err
	}
	return batch, false, nil
}

// AdtyGiftExpirationItem summarizes one expired gift batch cleared by the daily task.
type AdtyGiftExpirationItem struct {
	BatchID      int64  `json:"batch_id"`
	UserID       int    `json:"user_id"`
	Source       string `json:"source"`
	Original     int64  `json:"original"`
	Consumed     int64  `json:"consumed"`
	ExpiredQuota int64  `json:"expired_quota"`
	LedgerID     int64  `json:"ledger_id"`
}

// AdtyGiftExpiryMidnightUnix returns the 00:00:00 Unix timestamp of the calendar
// day containing expiresAtUnix in loc (defaults to configured business timezone
// when loc is nil — R1-077).
func AdtyGiftExpiryMidnightUnix(expiresAtUnix int64, loc *time.Location) int64 {
	if loc == nil {
		loc = AdtyBusinessLocation(nil)
	}
	t := time.Unix(expiresAtUnix, 0).In(loc)
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc).Unix()
}

// ExpireAdtyGiftBatches runs the daily post-midnight expiration sweep at now,
// clearing unconsumed remaining quota on expired gift batches and recording
// a gift_expire balance ledger row per batch (DEC-031).
// Each batch runs in its own short transaction (R1-028) so one user's shortfall
// or lock error never rolls back expiration across the rest of the platform.
func ExpireAdtyGiftBatches(now time.Time) ([]AdtyGiftExpirationItem, error) {
	if DB == nil {
		return nil, nil
	}
	if err := adtyRequireFundBatchTable(DB); err != nil {
		return nil, err
	}
	if err := AdtyFundAllocationTableReady(DB); err != nil {
		return nil, err
	}
	loc := now.Location()
	if loc == nil || loc == time.Local || loc == time.UTC {
		loc = AdtyBusinessLocation(DB)
		now = now.In(loc)
	}
	nowUnix := now.Unix()
	dayStart := AdtyGiftExpiryMidnightUnix(nowUnix, loc)
	nextDay := time.Unix(dayStart, 0).In(loc).AddDate(0, 0, 1).Unix()
	var candidates []AdtyFundBatch
	if err := DB.
		Select("id", "user_id").
		Where("expires_at IS NOT NULL AND expires_at > 0 AND expires_at < ? AND remaining > 0", nextDay).
		Order("user_id ASC, id ASC").
		Find(&candidates).Error; err != nil {
		return nil, err
	}
	items := make([]AdtyGiftExpirationItem, 0, len(candidates))
	seenUsers := map[int]struct{}{}
	var firstErr error
	for _, cand := range candidates {
		var item *AdtyGiftExpirationItem
		txErr := DB.Transaction(func(tx *gorm.DB) error {
			var err error
			item, err = expireSingleAdtyGiftBatchTx(tx, cand.Id, now, loc)
			return err
		})
		if txErr != nil {
			common.SysError(common.LogText("expire gift batch id=%d user=%d failed (isolated): %s", cand.Id, cand.UserId, txErr.Error()))
			if firstErr == nil {
				firstErr = txErr
			}
			continue
		}
		if item == nil {
			continue
		}
		items = append(items, *item)
		if _, ok := seenUsers[item.UserID]; !ok {
			seenUsers[item.UserID] = struct{}{}
			_ = invalidateUserCache(item.UserID)
			_ = PublishUserAuthCache(item.UserID)
		}
	}
	return items, firstErr
}

// ExpireAdtyGiftBatchesAt runs ExpireAdtyGiftBatches at the given Unix timestamp
// in the configured business timezone (R1-077).
func ExpireAdtyGiftBatchesAt(nowUnix int64) ([]AdtyGiftExpirationItem, error) {
	return ExpireAdtyGiftBatches(time.Unix(nowUnix, 0).In(AdtyBusinessLocation(DB)))
}

func expireSingleAdtyGiftBatchTx(tx *gorm.DB, batchID int64, now time.Time, loc *time.Location) (*AdtyGiftExpirationItem, error) {
	nowUnix := now.Unix()
	var stub AdtyFundBatch
	if err := tx.Select("id", "user_id").First(&stub, batchID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	balanceBefore, _, _, err := loadLockedUserLogicalQuotaTx(tx, stub.UserId)
	if err != nil {
		return nil, err
	}
	var batch AdtyFundBatch
	if err := lockForUpdate(tx).First(&batch, batchID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if !batch.IsExpiringGift() || batch.ExpiresAt == nil || *batch.ExpiresAt <= 0 || batch.Remaining <= 0 {
		return nil, nil
	}
	midnightCutoff := AdtyGiftExpiryMidnightUnix(*batch.ExpiresAt, loc)
	if nowUnix < midnightCutoff {
		return nil, nil
	}
	heldByBatch, err := outstandingAdtyFundAllocationByBatchTx(tx, batch.UserId)
	if err != nil {
		return nil, err
	}
	held := heldByBatch[batch.Id]
	clearQuota := batch.Remaining - held
	if clearQuota < 0 {
		clearQuota = 0
	}
	consumed := batch.Original - batch.Remaining - batch.ExpiredQuota
	if consumed < 0 {
		consumed = 0
	}
	if clearQuota == 0 {
		return nil, nil
	}
	idemKey := fmt.Sprintf("gift_expire:batch:%d:through:%d", batch.Id, batch.ExpiredQuota+clearQuota)
	debitQuota := clearQuota
	if debitQuota > balanceBefore {
		return nil, ErrAdtyFundBatchShortfall
	}
	if _, debited, err := AdtyDebitUserQuotaTx(tx, batch.UserId, debitQuota); err != nil {
		return nil, err
	} else if debited != debitQuota {
		return nil, ErrAdtyFundBatchShortfall
	}
	ledger := &AdtyBalanceLedger{
		UserId:         batch.UserId,
		DeltaQuota:     -debitQuota,
		BalanceBefore:  balanceBefore,
		BalanceAfter:   balanceBefore - debitQuota,
		Type:           AdtyLedgerTypeGiftExpire,
		RefType:        "fund_batch",
		RefId:          fmt.Sprintf("batch:%d|source:%s", batch.Id, batch.Source),
		Reason:         fmt.Sprintf("expired gift batch %d (source=%s, expired_quota=%d)", batch.Id, batch.Source, clearQuota),
		IdempotencyKey: &idemKey,
	}
	if err := RecordAdtyBalanceLedgerTx(tx, ledger); err != nil {
		return nil, err
	}
	nextRemaining := batch.Remaining - clearQuota
	nextStatus := AdtyFundBatchStatusExpired
	if nextRemaining > 0 {
		nextStatus = AdtyFundBatchStatusActive
	}
	if err := tx.Model(&AdtyFundBatch{}).
		Where("id = ? AND remaining = ?", batch.Id, batch.Remaining).
		Updates(map[string]any{
			"remaining":     nextRemaining,
			"expired_quota": batch.ExpiredQuota + clearQuota,
			"expired_at":    nowUnix,
			"status":        nextStatus,
		}).Error; err != nil {
		return nil, err
	}
	return &AdtyGiftExpirationItem{
		BatchID:      batch.Id,
		UserID:       batch.UserId,
		Source:       batch.Source,
		Original:     batch.Original,
		Consumed:     consumed,
		ExpiredQuota: clearQuota,
		LedgerID:     ledger.Id,
	}, nil
}

// ExpireAdtyGiftBatchesTx clears unconsumed quota on all expiring gift batches
// whose expiration cutoff has passed as of now inside tx. Already-consumed
// portions are never cleared again.
func ExpireAdtyGiftBatchesTx(tx *gorm.DB, now time.Time) ([]AdtyGiftExpirationItem, error) {
	if tx == nil {
		return nil, nil
	}
	if err := adtyRequireFundBatchTable(tx); err != nil {
		return nil, err
	}
	if err := AdtyFundAllocationTableReady(tx); err != nil {
		return nil, err
	}
	loc := now.Location()
	if loc == nil || loc == time.Local || loc == time.UTC {
		loc = AdtyBusinessLocation(tx)
		now = now.In(loc)
	}
	nowUnix := now.Unix()
	var candidates []AdtyFundBatch
	dayStart := AdtyGiftExpiryMidnightUnix(nowUnix, loc)
	nextDay := time.Unix(dayStart, 0).In(loc).AddDate(0, 0, 1).Unix()
	if err := tx.
		Select("id", "user_id").
		Where("expires_at IS NOT NULL AND expires_at > 0 AND expires_at < ? AND remaining > 0", nextDay).
		Order("user_id ASC, id ASC").
		Find(&candidates).Error; err != nil {
		return nil, err
	}
	items := make([]AdtyGiftExpirationItem, 0, len(candidates))
	for i := range candidates {
		item, err := expireSingleAdtyGiftBatchTx(tx, candidates[i].Id, now, loc)
		if err != nil {
			return nil, err
		}
		if item != nil {
			items = append(items, *item)
		}
	}
	return items, nil
}

// isAdtyFundBatchExpiredAt reports whether batch is an expiring gift lot whose
// business-midnight cutoff has already passed at nowUnix (R12-027).
func isAdtyFundBatchExpiredAt(batch *AdtyFundBatch, nowUnix int64, loc *time.Location) bool {
	if batch == nil {
		return false
	}
	if batch.Status == AdtyFundBatchStatusExpired {
		return true
	}
	if !batch.IsExpiringGift() || batch.ExpiresAt == nil || *batch.ExpiresAt <= 0 {
		return false
	}
	return nowUnix >= AdtyGiftExpiryMidnightUnix(*batch.ExpiresAt, loc)
}

// ExpireOverdueAdtyGiftBatchesForUserTx synchronously expires any overdue
// expiring-gift batches for an already-locked user inside tx (R12-027).
// Each batch runs in a savepoint so restricted-write / shortfall conditions
// never roll back live billing settlements.
func ExpireOverdueAdtyGiftBatchesForUserTx(tx *gorm.DB, userID int, now time.Time) (int64, error) {
	if tx == nil || userID <= 0 ||
		!tx.Migrator().HasTable(&AdtyFundBatch{}) ||
		!tx.Migrator().HasTable(&AdtyFundAllocation{}) ||
		!tx.Migrator().HasTable(&AdtyBalanceLedger{}) {
		return 0, nil
	}
	loc := now.Location()
	if loc == nil || loc == time.Local || loc == time.UTC {
		loc = AdtyBusinessLocation(tx)
		now = now.In(loc)
	}
	nowUnix := now.Unix()
	dayStart := AdtyGiftExpiryMidnightUnix(nowUnix, loc)
	nextDay := time.Unix(dayStart, 0).In(loc).AddDate(0, 0, 1).Unix()
	var candidates []AdtyFundBatch
	if err := tx.Select("id").
		Where("user_id = ? AND expires_at IS NOT NULL AND expires_at > 0 AND expires_at < ? AND remaining > 0", userID, nextDay).
		Order("id ASC").
		Find(&candidates).Error; err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	var expiredTotal int64
	for _, cand := range candidates {
		var item *AdtyGiftExpirationItem
		spErr := tx.Transaction(func(sp *gorm.DB) error {
			var err error
			item, err = expireSingleAdtyGiftBatchTx(sp, cand.Id, now, loc)
			return err
		})
		if spErr != nil {
			if errors.Is(spErr, ErrAdtyFundWriteRestricted) || errors.Is(spErr, ErrAdtyFundBatchShortfall) {
				continue
			}
			return expiredTotal, spErr
		}
		if item != nil {
			expiredTotal += item.ExpiredQuota
		}
	}
	return expiredTotal, nil
}

// overdueUnsweptExpiringGiftQuotaTx sums remaining unheld quota on expiring
// gift batches whose midnight cutoff has already passed (e.g., if a reviewing
// restriction prevented sweeping them), so spendable balance never includes them.
func overdueUnsweptExpiringGiftQuotaTx(tx *gorm.DB, userID int, now time.Time) (int64, error) {
	if tx == nil || userID <= 0 || !tx.Migrator().HasTable(&AdtyFundBatch{}) {
		return 0, nil
	}
	loc := AdtyBusinessLocation(tx)
	now = now.In(loc)
	nowUnix := now.Unix()
	dayStart := AdtyGiftExpiryMidnightUnix(nowUnix, loc)
	nextDay := time.Unix(dayStart, 0).In(loc).AddDate(0, 0, 1).Unix()
	var batches []AdtyFundBatch
	if err := tx.Where("user_id = ? AND expires_at IS NOT NULL AND expires_at > 0 AND expires_at < ? AND remaining > 0", userID, nextDay).
		Order("id ASC").
		Find(&batches).Error; err != nil {
		return 0, err
	}
	if len(batches) == 0 {
		return 0, nil
	}
	heldByBatch := map[int64]int64{}
	if tx.Migrator().HasTable(&AdtyFundAllocation{}) {
		var err error
		heldByBatch, err = outstandingAdtyFundAllocationByBatchTx(tx, userID)
		if err != nil {
			return 0, err
		}
	}
	var overdue int64
	for i := range batches {
		if !isAdtyFundBatchExpiredAt(&batches[i], nowUnix, loc) {
			continue
		}
		unheld := batches[i].Remaining - heldByBatch[batches[i].Id]
		if unheld > 0 {
			overdue += unheld
		}
	}
	return overdue, nil
}

// adtyMoveFundBatchesToSiteTx 把迁移用户的批次一起搬到目标站（ISS-R4-0047）。
//
// 站迁移以前不需要碰批次表：那张表里根本没有站点字段。加了 site_id 之后不动它，
// 迁移完就是「客户在新站、本金挂在旧站」，按站点算本金负债会把两个站同时算错。
//
// 只搬 site_id 等于原站的行：NULL 是「当时就证不出站点」的历史行，搬过去只是把
// 未知换成错的已知。表不存在说明这套库还没建批次表，没有批次可搬，不是错误。
func adtyMoveFundBatchesToSiteTx(tx *gorm.DB, userIDs []int, fromSite, toSite int) error {
	if tx == nil || len(userIDs) == 0 || fromSite == toSite {
		return nil
	}
	if !tx.Migrator().HasTable(&AdtyFundBatch{}) {
		return nil
	}
	return tx.Model(&AdtyFundBatch{}).
		Where("user_id IN ? AND site_id = ?", userIDs, fromSite).
		Update("site_id", toSite).Error
}

// adtyLegacyBatchSource 是「没有订单来源的额度」那一条批次的固定 source。
// 每个用户只有一条（唯一索引 user_id + source + kind），补额度是往上加，
// 不是新插一行——否则第二次补建就会撞唯一索引。
const adtyLegacyBatchSource = "legacy"

// ensureAdtyLegacyBatchTx 把「没有批次背书」的额度补进那条 legacy 批次。
//
// topUp 是本次要补的额度。已经存在就加在 remaining/original 上（幂等：补过之后
// 再算一次 short 会是 0）；不存在才新建。
//
// 走 SAVEPOINT：并发下两个事务可能同时判断「还没有 legacy 批次」，唯一索引会让
// 其中一个失败。冲突时重读并补额度，不算错误——否则并发的第二笔扣费会整笔失败。
func ensureAdtyLegacyBatchTx(tx *gorm.DB, userID int, topUp int64) error {
	if tx == nil || userID <= 0 || topUp <= 0 {
		return nil
	}
	apply := func(db *gorm.DB) error {
		result := db.Model(&AdtyFundBatch{}).
			Where("user_id = ? AND source = ? AND kind = ?", userID, adtyLegacyBatchSource, "principal").
			Updates(map[string]any{
				"remaining": gorm.Expr("remaining + ?", topUp),
				"original":  gorm.Expr("original + ?", topUp),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			return nil
		}
		// 补建的是「没有批次背书的额度」，站点无从证明，留 NULL 而不是拿
		// 用户当前站点顶上。
		return db.Create(&AdtyFundBatch{
			UserId: userID, Kind: "principal", Source: adtyLegacyBatchSource,
			Original: topUp, Remaining: topUp,
		}).Error
	}
	err := tx.Transaction(apply)
	if err != nil && isAdtyUniqueViolation(err) {
		// 另一个事务刚建好这一行：补同样的额度，然后照常消费。
		return tx.Transaction(apply)
	}
	return err
}

// AdtyAfterPrincipalSpentTx runs in the charge transaction after non-legacy
// principal lots are reduced. Gift consumption does not call it.
//
// operationKey 是这笔消费的业务身份：结算路径传 operation key，直接扣费
// （没有 operation 可言）传空串。实现方用它在站点钱包流水上做幂等键——
// 同一笔 operation 重放不能把站长该付的钱扣两遍。
var AdtyAfterPrincipalSpentTx func(tx *gorm.DB, userID int, operationKey string, principalQuota int64) error

// adtyNotifyPrincipalSpentTx hands a charge's non-legacy principal portion to
// the site side. Every settle exit goes through it, so one settled operation
// debits the site operating wallet exactly once.
func adtyNotifyPrincipalSpentTx(tx *gorm.DB, userID int, operationKey string, principalSpent int64) error {
	if principalSpent <= 0 || AdtyAfterPrincipalSpentTx == nil {
		return nil
	}
	return AdtyAfterPrincipalSpentTx(tx, userID, operationKey, principalSpent)
}

// AdtyBonusSpend is one source's bonus lot reduction in a usage charge.
type AdtyBonusSpend struct {
	Source string
	Quota  int64
}

// AdtyAfterBonusSpentTx runs after bonus lots are consumed by real API usage
// charges so the matching site gift-guarantee is spent (gift_spend) to cover
// platform API cost.
var AdtyAfterBonusSpentTx func(tx *gorm.DB, userID int, spends []AdtyBonusSpend) error

// AdtyAfterBonusReleasedTx runs when unspent permanent bonus lots are revoked
// by official adjustments, staff balance decreases, or quiet non-usage debits,
// releasing the site gift-guarantee freeze (gift_release, delta = 0) without
// debiting the site owner's wallet balance.
var AdtyAfterBonusReleasedTx func(tx *gorm.DB, userID int, releases []AdtyBonusSpend) error

// AdtyAfterBonusRefundedTx runs inside the settlement transaction when a
// settled non-expiring bonus share is reversed (e.g. failed task refund or
// downward Realtime WSS settlement), restoring prior gift_spend on the
// originating site's Earnings/Margin wallets (R3-004).
var AdtyAfterBonusRefundedTx func(tx *gorm.DB, userID int, operationKey string, refunds []AdtyBonusSpend) error

func adtyNotifyBonusSpentTx(tx *gorm.DB, userID int, bonusBySource map[string]int64) error {
	if len(bonusBySource) == 0 || AdtyAfterBonusSpentTx == nil {
		return nil
	}
	sources := make([]string, 0, len(bonusBySource))
	for source := range bonusBySource {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	spends := make([]AdtyBonusSpend, 0, len(sources))
	for _, source := range sources {
		if quota := bonusBySource[source]; quota > 0 {
			spends = append(spends, AdtyBonusSpend{Source: source, Quota: quota})
		}
	}
	if len(spends) == 0 {
		return nil
	}
	return AdtyAfterBonusSpentTx(tx, userID, spends)
}

func adtyNotifyBonusReleasedTx(tx *gorm.DB, userID int, bonusBySource map[string]int64) error {
	if len(bonusBySource) == 0 || AdtyAfterBonusReleasedTx == nil {
		return nil
	}
	sources := make([]string, 0, len(bonusBySource))
	for source := range bonusBySource {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	releases := make([]AdtyBonusSpend, 0, len(sources))
	for _, source := range sources {
		if quota := bonusBySource[source]; quota > 0 {
			releases = append(releases, AdtyBonusSpend{Source: source, Quota: quota})
		}
	}
	if len(releases) == 0 {
		return nil
	}
	return AdtyAfterBonusReleasedTx(tx, userID, releases)
}

func adtyNotifyBonusRefundedTx(tx *gorm.DB, userID int, operationKey string, bonusBySource map[string]int64) error {
	if len(bonusBySource) == 0 || AdtyAfterBonusRefundedTx == nil {
		return nil
	}
	sources := make([]string, 0, len(bonusBySource))
	for source := range bonusBySource {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	refunds := make([]AdtyBonusSpend, 0, len(sources))
	for _, source := range sources {
		if quota := bonusBySource[source]; quota > 0 {
			refunds = append(refunds, AdtyBonusSpend{Source: source, Quota: quota})
		}
	}
	if len(refunds) == 0 {
		return nil
	}
	return AdtyAfterBonusRefundedTx(tx, userID, operationKey, refunds)
}

// ConsumeAdtyFundBatchesTx spends principal lots before bonus lots.
// A user with no lots gets one legacy principal lot for the pre-charge balance,
// then this charge consumes from that lot. If lots still cannot cover the debit,
// it returns ErrAdtyFundBatchShortfall so the surrounding wallet transaction
// can roll back.
func ConsumeAdtyFundBatchesTx(tx *gorm.DB, userID int, amount, balanceBefore int64) error {
	_, err := consumeAdtyFundBatchesTx(tx, userID, amount, balanceBefore, true, "")
	return err
}

// ConsumeAdtyFundBatchesQuietTx spends lots in FIFO order so remaining tracks
// a non-charge wallet debit. It does not fire the principal-spent hook; that
// hook is for usage charges, not official adjustments or staff approvals.
// It does fire the bonus-spent hook so an approved or official reduction that
// consumes top-up bonus lots can release the corresponding site gift guarantee.
func ConsumeAdtyFundBatchesQuietTx(tx *gorm.DB, userID int, amount, balanceBefore int64) error {
	_, err := consumeAdtyFundBatchesTx(tx, userID, amount, balanceBefore, false, "")
	return err
}

// ConsumeAdtyFundBatchesInOrderTx consumes up to amount in canonical batch order
// (principal -> expiring bonus by earliest expiry -> permanent bonus) and returns
// the backed quota consumed.
func ConsumeAdtyFundBatchesInOrderTx(tx *gorm.DB, userID int, amount, balanceBefore int64) (int64, error) {
	return consumeAdtyFundBatchesTx(tx, userID, amount, balanceBefore, true, "")
}

// RestoreAdtyFundBatchesInReverseOrderTx restores up to amount back onto a user's
// consumed fund batches in reverse consumption order (permanent bonus latest-first,
// expiring bonus latest-expiry-first, principal latest-first).
func RestoreAdtyFundBatchesInReverseOrderTx(tx *gorm.DB, userID int, amount int64) (int64, error) {
	if tx == nil || userID <= 0 || amount <= 0 {
		return 0, nil
	}
	if err := adtyRequireFundBatchTable(tx); err != nil {
		return 0, err
	}
	var rows []AdtyFundBatch
	if err := lockForUpdate(tx).
		Where("user_id = ? AND remaining < original AND status <> ?", userID, AdtyFundBatchStatusExpired).
		Find(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	sortAdtyFundBatches(rows)
	restored := int64(0)
	left := amount
	for i := len(rows) - 1; i >= 0 && left > 0; i-- {
		capacity := rows[i].Original - rows[i].Remaining - rows[i].ExpiredQuota
		if capacity <= 0 {
			continue
		}
		giveBack := min(capacity, left)
		res := tx.Model(&AdtyFundBatch{}).
			Where("id = ? AND remaining = ?", rows[i].Id, rows[i].Remaining).
			Updates(map[string]any{
				"remaining": gorm.Expr("remaining + ?", giveBack),
				"status":    AdtyFundBatchStatusActive,
			})
		if res.Error != nil {
			return restored, res.Error
		}
		if res.RowsAffected != 1 {
			return restored, ErrAdtyFundBatchShortfall
		}
		restored += giveBack
		left -= giveBack
	}
	return restored, nil
}

// consumeAdtyFundBatchesTx returns how much of the debit was backed by lots that
// existed before any legacy top-up, not how much was debited. The difference is
// the drift: money that sat on the account with no lot behind it. Callers that
// only need the error discard it; settlement records it as fund_batch_gap.
//
// operationKey is passed through to the principal-spent hook so a settlement
// whose operation has no shares still debits the site operating wallet exactly
// once. Direct charges have no operation identity and pass "".
// adtyEffectiveLegacyBalanceTx adjusts balanceBefore for unscoped callers whose
// balanceBefore is raw users.quota (which still includes overdue unswept expiring
// gift quota and pending owner-refund holds that have not yet decremented
// users.quota). Scoped billing callers already run ownerScopedWallet and attach
// ownerLotScopeKey/ownerWalletPreAdjustedKey, so they are never double-subtracted (R14-024).
func adtyEffectiveLegacyBalanceTx(tx *gorm.DB, userID int, balanceBefore int64, rows []AdtyFundBatch, heldByBatch map[int64]int64, loc *time.Location, nowUnix int64) (int64, error) {
	if balanceBefore <= 0 || tx == nil {
		return max(0, balanceBefore), nil
	}
	if tx.Statement != nil && tx.Statement.Context != nil {
		if _, hasScope := tx.Statement.Context.Value(ownerLotScopeKey{}).(int); hasScope {
			return balanceBefore, nil
		}
		if preAdjusted, _ := tx.Statement.Context.Value(ownerWalletPreAdjustedKey{}).(bool); preAdjusted {
			return balanceBefore, nil
		}
	}
	var overdueUnheld int64
	for i := range rows {
		if rows[i].Status == AdtyFundBatchStatusExpired || rows[i].Remaining <= 0 {
			continue
		}
		if rows[i].IsExpiringGift() && rows[i].ExpiresAt != nil && *rows[i].ExpiresAt > 0 {
			if loc == nil {
				loc = AdtyBusinessLocation(tx)
				nowUnix = time.Now().In(loc).Unix()
			}
			if isAdtyFundBatchExpiredAt(&rows[i], nowUnix, loc) {
				if unheld := rows[i].Remaining - heldByBatch[rows[i].Id]; unheld > 0 {
					overdueUnheld += unheld
				}
			}
		}
	}
	var ownerRefundHeld int64
	if tx.Migrator().HasTable(&AdtyFundAllocation{}) {
		if err := tx.Model(&AdtyFundAllocation{}).
			Where("user_id = ? AND operation_id LIKE ? AND held > settled", userID, ownerRefundOperationPrefix+"%").
			Select("COALESCE(SUM(held-settled),0)").
			Scan(&ownerRefundHeld).Error; err != nil {
			return 0, err
		}
	}
	return max(0, balanceBefore-overdueUnheld-ownerRefundHeld), nil
}

func consumeAdtyFundBatchesTx(tx *gorm.DB, userID int, amount, balanceBefore int64, notifyPrincipalSpent bool, operationKey string) (int64, error) {
	if tx == nil || userID <= 0 || amount <= 0 {
		return 0, nil
	}
	if err := adtyRequireFundBatchTable(tx); err != nil {
		return 0, err
	}
	var rows []AdtyFundBatch
	if err := eligibleFundLots(tx, userID).Find(&rows).Error; err != nil {
		return 0, err
	}
	heldByBatch, err := outstandingAdtyFundAllocationByBatchTx(tx, userID)
	if err != nil {
		return 0, err
	}
	loc := AdtyBusinessLocation(tx)
	nowUnix := time.Now().In(loc).Unix()
	// 批次总量与「这次业务之前」的余额之间的差，是**没有批次背书的额度**：
	// 升级前的余额从来没有对应的行，退款（IncreaseUserQuota）也不会补行。
	// 这部分要落到那条唯一的 legacy 批次上，否则扣费一定失败。
	// R13-014 / R14-024: 必须排除已过期未清零的到期赠金以及已被并发请求在途占用的额度；
	// 对未走 ownerScopedWallet 的非作用域调用方，也必须从 effectiveBalanceBefore 中扣除
	// 未清零过期赠金与 owner-refund 冻结，防止把过期赠金或退款占用铸造成永久 legacy 本金。
	var lotTotal int64
	// realRemaining 记下**补建之前**每个批次真正有多少可扣额度。补建之后 remaining
	// 里有一部分是刚凭空写进去的，不能算进「批次背书了多少」。
	realRemaining := make(map[int64]int64, len(rows))
	for i := range rows {
		if rows[i].Status == AdtyFundBatchStatusExpired || isAdtyFundBatchExpiredAt(&rows[i], nowUnix, loc) {
			continue
		}
		avail := rows[i].Remaining - heldByBatch[rows[i].Id]
		if avail <= 0 {
			continue
		}
		lotTotal += avail
		realRemaining[rows[i].Id] = avail
	}
	effectiveBalanceBefore, err := adtyEffectiveLegacyBalanceTx(tx, userID, balanceBefore, rows, heldByBatch, loc, nowUnix)
	if err != nil {
		return 0, err
	}
	if short := effectiveBalanceBefore - lotTotal; short > 0 {
		// 补建是必须的：存量用户升级前的额度从来没有对应行，退款
		// （IncreaseUserQuota）也不会补行，不补建就一笔都扣不出去。
		//
		// 但**补建不等于「这批钱有批次背书」**。补出去的那部分正是账本漂移，
		// 必须由调用方作为 fund_batch_gap 记下来——否则「批次永远等于余额」，
		// 对账永远读到 0，这一列等于没加。所以返回值报的是补建前的真实覆盖，
		// 不是实际扣掉的额度。
		if err := ensureAdtyLegacyBatchTx(tx, userID, short); err != nil {
			return 0, err
		}
	}
	// 只消费还有剩余的批次；上面补过之后重新读，避免拿旧快照算。
	if err := eligibleFundLots(tx, userID).Where("remaining > 0").Find(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, adtyFundBatchShortfallError(amount)
	}
	sortAdtyFundBatches(rows)
	principalTaken := int64(0)
	taken := int64(0)
	backed := int64(0)
	bonusBySource := map[string]int64{}
	for i := range rows {
		if amount == 0 {
			break
		}
		if rows[i].Status == AdtyFundBatchStatusExpired || isAdtyFundBatchExpiredAt(&rows[i], nowUnix, loc) {
			continue
		}
		avail := rows[i].Remaining - heldByBatch[rows[i].Id]
		if avail <= 0 {
			continue
		}
		take := avail
		if take > amount {
			take = amount
		}
		// 本次从这一行扣走的额度里，有多少是补建之前就真在那儿的。
		// 逐行取 min 而不是「taken - short」：扣费是从真实批次先扣的，
		// 当本次扣费小于缺口时（比如余额 50、批次 20、只扣 10），
		// taken - short 会算成 -20，把一次完全有背书的扣费报成漂移。
		if real := realRemaining[rows[i].Id]; real > 0 {
			backed += take
			if take > real {
				backed -= take - real
			}
		}
		// 扣减交给 deductAdtyFundBatchTx 的同一套三件套（WHERE remaining >= ? +
		// gorm.Expr("remaining - ?") + RowsAffected 校验）：WHERE 挡「余额不足」，
		// Expr 挡「丢更新」——不把内存里读到的绝对值写回去。这里不再依赖
		// 「调用方已锁 users 行」这个未声明的约定，那只是当前的巧合，不是保证。
		if err := deductAdtyFundBatchTx(tx, rows[i].Id, take); err != nil {
			return backed, err
		}
		if rows[i].Kind == AdtyFundBatchKindPrincipal && rows[i].Source != adtyLegacyBatchSource {
			principalTaken += take
		}
		if !rows[i].IsExpiringGift() && rows[i].Kind == AdtyFundBatchKindBonus && rows[i].Source != "" && take > 0 {
			bonusBySource[rows[i].Source] += take
		}
		amount -= take
		taken += take
	}
	if amount > 0 {
		return backed, adtyFundBatchShortfallError(amount)
	}
	// R4-005: Invoke bonus hooks (earnings -> margin) BEFORE principal hooks
	// (operating) to preserve canonical adty_site_wallets lock ordering.
	// Usage charges (notifyPrincipalSpent == true) realize gift_spend; quiet
	// non-usage reductions release the unspent guarantee freeze (gift_release).
	if notifyPrincipalSpent {
		if err := adtyNotifyBonusSpentTx(tx, userID, bonusBySource); err != nil {
			return backed, err
		}
	} else {
		if err := adtyNotifyBonusReleasedTx(tx, userID, bonusBySource); err != nil {
			return backed, err
		}
	}
	if notifyPrincipalSpent && principalTaken > 0 {
		if err := adtyNotifyPrincipalSpentTx(tx, userID, operationKey, principalTaken); err != nil {
			return backed, err
		}
	}
	_ = taken
	return backed, nil
}

// AdtyAfterPrincipalReducedTx debits the subsite operating wallet when a
// subsite customer's principal batch is reduced by a financial adjustment or
// staff balance decrease (R1-007 / A.11 / DEC-004).
var AdtyAfterPrincipalReducedTx func(tx *gorm.DB, siteID int, quota int64, refType, refID, idemKey string) error

// ReduceAdtyAdjustmentBatchTx debits only the approved, named lot. The enclosing
// wallet transaction rolls back if this lot cannot cover the complete reduction.
func ReduceAdtyAdjustmentBatchTx(tx *gorm.DB, userID, siteID int, source, kind string, amount int64) error {
	if amount <= 0 || source == "" || (kind != AdtyFundBatchKindPrincipal && kind != AdtyFundBatchKindBonus && kind != AdtyFundBatchKindExpiringBonus) {
		return common.NewMessage("A specific available fund batch is required")
	}
	var batch AdtyFundBatch
	q := lockForUpdate(tx).Where("user_id = ? AND source = ?", userID, source)
	if siteID == 0 {
		q = q.Where("(site_id = 0 OR site_id IS NULL)")
	} else {
		q = q.Where("site_id = ?", siteID)
	}
	switch kind {
	case AdtyFundBatchKindExpiringBonus:
		// Expiring gifts are created with kind='bonus' and gift_category='expiring_bonus' (R1-029).
		q = q.Where("((kind = ? AND gift_category = ?) OR kind = ?)", AdtyFundBatchKindBonus, AdtyGiftCategoryExpiring, AdtyFundBatchKindExpiringBonus)
	case AdtyFundBatchKindBonus:
		q = q.Where("kind = ? AND (gift_category IS NULL OR gift_category <> ?)", AdtyFundBatchKindBonus, AdtyGiftCategoryExpiring)
	default:
		q = q.Where("kind = ?", kind)
	}
	if err := q.First(&batch).Error; err != nil {
		return err
	}
	nowUnix := common.GetTimestamp()
	if batch.Status == AdtyFundBatchStatusExpired || batch.ExpiredAt > 0 ||
		(batch.ExpiresAt != nil && *batch.ExpiresAt > 0 && nowUnix >= AdtyGiftExpiryMidnightUnix(*batch.ExpiresAt, AdtyBusinessLocation(tx))) {
		return ErrAdtyFundBatchShortfall
	}
	held, err := outstandingAdtyFundAllocationByBatchTx(tx, userID)
	if err != nil {
		return err
	}
	if batch.Remaining-held[batch.Id] < amount {
		return ErrAdtyFundBatchShortfall
	}
	if err := deductAdtyFundBatchTx(tx, batch.Id, amount); err != nil {
		return err
	}
	if batch.Kind == AdtyFundBatchKindBonus && !batch.IsExpiringGift() {
		return adtyNotifyBonusReleasedTx(tx, userID, map[string]int64{source: amount})
	}
	return nil
}

// ReclaimAdtyFundBatchesBySourceTx reduces remaining on the lots of one credit
// source. Principal and bonus are reduced independently and capped at each
// kind's remaining. It does not invent lots when none were recorded.
func ReclaimAdtyFundBatchesBySourceTx(tx *gorm.DB, userID int, source string, principal, bonus int64) (int64, error) {
	if tx == nil || userID <= 0 || source == "" || (principal <= 0 && bonus <= 0) {
		return 0, nil
	}
	if err := adtyRequireFundBatchTable(tx); err != nil {
		return 0, err
	}
	var rows []AdtyFundBatch
	if err := tx.Where("user_id = ? AND source = ? AND remaining > 0", userID, source).Find(&rows).Error; err != nil {
		return 0, err
	}
	heldByBatch, err := outstandingAdtyFundAllocationByBatchTx(tx, userID)
	if err != nil {
		return 0, err
	}
	taken := int64(0)
	for i := range rows {
		var want *int64
		switch rows[i].Kind {
		case AdtyFundBatchKindPrincipal:
			want = &principal
		case AdtyFundBatchKindBonus, AdtyFundBatchKindExpiringBonus:
			want = &bonus
		default:
			continue
		}
		if *want <= 0 {
			continue
		}
		// Never seize balance currently held by in-flight billing allocations (ISS-R1-0012).
		take := rows[i].Remaining - heldByBatch[rows[i].Id]
		if take <= 0 {
			continue
		}
		if take > *want {
			take = *want
		}
		// 同上：条件更新 + 原子减法 + RowsAffected 校验，缺一件就分别是
		// 「扣成负数」或「并发覆盖掉别人已提交的扣减」。
		if err := deductAdtyFundBatchTx(tx, rows[i].Id, take); err != nil {
			return taken, err
		}
		*want -= take
		taken += take
	}
	return taken, nil
}

// applyAdtyWalletDebitToBatchesTx keeps the batch ledger in step with a wallet
// debit. It first reclaims the named source's principal and bonus, then
// FIFO-consumes any leftover so total remaining tracks the wallet. If lots
// still cannot cover the debit, consume returns ErrAdtyFundBatchShortfall.
// A leftover after reclaim does not fire the consumption hook; that hook is
// for charges.
func applyAdtyWalletDebitToBatchesTx(tx *gorm.DB, userID int, source string, principal, bonus, deducted, balanceBefore int64) error {
	if deducted <= 0 {
		return nil
	}
	taken, err := ReclaimAdtyFundBatchesBySourceTx(tx, userID, source, principal, bonus)
	if err != nil {
		return err
	}
	leftover := deducted - taken
	if leftover <= 0 {
		return nil
	}
	remainingBefore := balanceBefore - taken
	if remainingBefore < 0 {
		remainingBefore = 0
	}
	_, err = consumeAdtyFundBatchesTx(tx, userID, leftover, remainingBefore, false, "")
	return err
}

func adtyRequireFundBatchTable(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&AdtyFundBatch{}) {
		return ErrAdtyFundBatchTableMissing
	}
	return nil
}

func sortAdtyFundBatches(rows []AdtyFundBatch) {
	for i := 1; i < len(rows); i++ {
		item := rows[i]
		j := i
		for j > 0 && adtyFundBatchLess(item, rows[j-1]) {
			rows[j] = rows[j-1]
			j--
		}
		rows[j] = item
	}
}

func adtyFundBatchOrderRank(row AdtyFundBatch) int {
	if row.Kind == AdtyFundBatchKindPrincipal {
		return 0
	}
	if row.IsExpiringGift() {
		return 1
	}
	return 2
}

// adtyFundBatchLess enforces the DEC-031 / A.12.2 consumption order:
//  1. Principal lots (FIFO by CreatedAt, Id)
//  2. Time-limited gift lots (ordered by ExpiresAt ASC, then Id)
//  3. Permanent recharge bonus lots (FIFO by CreatedAt, Id)
func adtyFundBatchLess(a, b AdtyFundBatch) bool {
	rankA := adtyFundBatchOrderRank(a)
	rankB := adtyFundBatchOrderRank(b)
	if rankA != rankB {
		return rankA < rankB
	}
	if rankA == 1 {
		expA := int64(0)
		if a.ExpiresAt != nil {
			expA = *a.ExpiresAt
		}
		expB := int64(0)
		if b.ExpiresAt != nil {
			expB = *b.ExpiresAt
		}
		if expA != expB {
			return expA < expB
		}
		return a.Id < b.Id
	}
	if a.CreatedAt != b.CreatedAt {
		return a.CreatedAt < b.CreatedAt
	}
	return a.Id < b.Id
}

// AdtyFundBatchKindRemaining is the remaining quota of each lot category for one user.
// Bonus holds permanent recharge gift quota; ExpiringBonus holds time-limited gift quota (DEC-031).
type AdtyFundBatchKindRemaining struct {
	Principal     int64 `json:"principal"`
	Bonus         int64 `json:"bonus"`
	ExpiringBonus int64 `json:"expiring_bonus"`
	NextExpiresAt int64 `json:"next_expires_at,omitempty"`
	HasLots       bool  `json:"has_lots"`
}

// TotalBonus returns the combined remaining gift quota (permanent + expiring).
func (r AdtyFundBatchKindRemaining) TotalBonus() int64 {
	return r.Bonus + r.ExpiringBonus
}

// SumAdtyFundBatchRemainingByKind totals remaining principal, permanent bonus,
// and time-limited gift lots. It does not invent lots and does not include
// promised or not-yet-granted bonus.
func SumAdtyFundBatchRemainingByKind(userID int) (AdtyFundBatchKindRemaining, error) {
	sums, err := SumAdtyFundBatchRemainingByKindForUsers([]int{userID})
	if err != nil {
		return AdtyFundBatchKindRemaining{}, err
	}
	return sums[userID], nil
}

// SumAdtyFundBatchRemainingByKindForUsers totals remaining principal, permanent
// bonus, and expiring bonus lots per user.
func SumAdtyFundBatchRemainingByKindForUsers(userIDs []int) (map[int]AdtyFundBatchKindRemaining, error) {
	out := map[int]AdtyFundBatchKindRemaining{}
	if DB == nil || len(userIDs) == 0 {
		return out, nil
	}
	ids := uniquePositiveIDs(userIDs)
	if len(ids) == 0 {
		return out, nil
	}
	if err := adtyRequireFundBatchTable(DB); err != nil {
		return nil, err
	}
	var rows []AdtyFundBatch
	if err := DB.Model(&AdtyFundBatch{}).
		Where("user_id IN ? AND kind IN ?", ids, []string{
			AdtyFundBatchKindPrincipal,
			AdtyFundBatchKindBonus,
			AdtyFundBatchKindExpiringBonus,
		}).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		current := out[row.UserId]
		current.HasLots = true
		switch {
		case row.Kind == AdtyFundBatchKindPrincipal:
			current.Principal += row.Remaining
		case row.IsExpiringGift():
			current.ExpiringBonus += row.Remaining
			if row.Remaining > 0 && row.ExpiresAt != nil && *row.ExpiresAt > 0 {
				if current.NextExpiresAt == 0 || *row.ExpiresAt < current.NextExpiresAt {
					current.NextExpiresAt = *row.ExpiresAt
				}
			}
		default:
			current.Bonus += row.Remaining
		}
		out[row.UserId] = current
	}
	return out, nil
}

func uniquePositiveIDs(ids []int) []int {
	seen := map[int]struct{}{}
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// FindAdtyBalanceLedgerByKeyTx aliases FindAdtyLedgerByIdemKeyTx.
func FindAdtyBalanceLedgerByKeyTx(tx *gorm.DB, key string) (*AdtyBalanceLedger, error) {
	return FindAdtyLedgerByIdemKeyTx(tx, key)
}

// RecordAdtyBalanceLedgerTx aliases InsertAdtyLedgerTx.
func RecordAdtyBalanceLedgerTx(tx *gorm.DB, entry *AdtyBalanceLedger) error {
	return InsertAdtyLedgerTx(tx, entry)
}

// loadLockedUserLogicalQuotaTx locks the user row and returns logical balance, raw quota, and pending quota.
func loadLockedUserLogicalQuotaTx(tx *gorm.DB, userID int) (logicalQuota, rawQuota, pendingQuota int64, err error) {
	var user User
	if err := lockForUpdate(tx).Select("id", "quota", "quota_pending", "checkin_quota").First(&user, userID).Error; err != nil {
		return 0, 0, 0, err
	}
	bal, err := userWalletBalance(user.Quota, user.QuotaPending)
	if err != nil {
		return 0, 0, 0, err
	}
	return int64(bal), int64(user.Quota), int64(user.QuotaPending), nil
}
