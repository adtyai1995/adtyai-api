package service

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AdtySiteTierModeAuto   = "auto"
	AdtySiteTierModeManual = "manual"
)

var (
	ErrAdtyOperatingWalletHidden         = errors.New("operating wallet hidden from site owner")
	ErrAdtySiteWalletInvalidKind         = errors.New("invalid site wallet kind")
	ErrAdtySiteWalletInvalidOperation    = errors.New("invalid site wallet operation")
	ErrAdtySiteWalletInsufficient        = errors.New("insufficient site wallet funds")
	ErrAdtySiteWalletIdempotencyConflict = errors.New("site wallet idempotency key reused for a different operation")
	ErrAdtySiteTierInvalid               = errors.New("invalid site tier")
	ErrAdtySiteTierNotFound              = errors.New("site tier not found")
	ErrAdtySiteTierMode                  = errors.New("invalid site tier mode")
	ErrAdtySiteWalletIdempotencyKey      = errors.New("site wallet idempotency key is required")
)

type AdtySiteWalletChange struct {
	Type             string
	RefType          string
	RefID            string
	Reason           string
	OperatorID       int
	IdempotencyKey   string
	AllowOverdraft   bool
	OpenMarginCycle  bool
	MarginExcessOnly bool
	CycleTier        decimal.Decimal
	ReleaseCycleUsed decimal.Decimal
	HoldDelta        decimal.Decimal
}

// AdtyOwnerFundingSplit records the earnings-first + excess-margin breakdown
// of one site-owner-funded operation.
type AdtyOwnerFundingSplit struct {
	EarningsUSD decimal.Decimal `json:"earnings_usd"`
	MarginUSD   decimal.Decimal `json:"margin_usd"`
	TotalUSD    decimal.Decimal `json:"total_usd"`
}

func (s AdtyOwnerFundingSplit) SourceKind() string {
	switch {
	case s.EarningsUSD.IsPositive() && s.MarginUSD.IsPositive():
		return model.AdtyVoucherSourceMixed
	case s.MarginUSD.IsPositive():
		return model.AdtyVoucherSourceMargin
	default:
		return model.AdtyVoucherSourceEarnings
	}
}

func adtySiteWalletKinds() []string {
	return []string{model.AdtySiteWalletOperating, model.AdtySiteWalletEarnings, model.AdtySiteWalletMargin}
}

func validAdtySiteWalletKind(kind string) bool {
	for _, candidate := range adtySiteWalletKinds() {
		if kind == candidate {
			return true
		}
	}
	return false
}

func ensureAdtySiteWalletsTx(tx *gorm.DB, siteID int) error {
	if siteID <= 0 {
		return ErrAdtySiteNotFound
	}
	var count int64
	if err := tx.Model(&model.AdtySite{}).Where("id = ?", siteID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrAdtySiteNotFound
	}
	rows := make([]model.AdtySiteWallet, 0, len(adtySiteWalletKinds()))
	for _, kind := range adtySiteWalletKinds() {
		rows = append(rows, model.AdtySiteWallet{SiteId: siteID, Kind: kind, Currency: "USD"})
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

// EnsureAdtySiteWallets creates the three site wallets and is safe to call for
// both newly created sites and sites upgraded from an older schema.
func EnsureAdtySiteWallets(siteID int) error {
	return model.DB.Transaction(func(tx *gorm.DB) error { return ensureAdtySiteWalletsTx(tx, siteID) })
}

// ListAdtySiteWallets returns all three wallets without creating financial
// state during a read. Sites predating the wallet migration show zero balances
// until a real business operation initializes their wallets.
func ListAdtySiteWallets(siteID int) ([]model.AdtySiteWallet, error) {
	if _, err := GetAdtySite(siteID); err != nil {
		return nil, err
	}
	var stored []model.AdtySiteWallet
	if err := model.DB.Where("site_id = ?", siteID).Find(&stored).Error; err != nil {
		return nil, err
	}
	byKind := make(map[string]model.AdtySiteWallet, len(stored))
	for _, wallet := range stored {
		byKind[wallet.Kind] = wallet
	}
	wallets := make([]model.AdtySiteWallet, 0, len(adtySiteWalletKinds()))
	for _, kind := range adtySiteWalletKinds() {
		wallet, found := byKind[kind]
		if !found {
			wallet = model.AdtySiteWallet{SiteId: siteID, Kind: kind, Currency: "USD"}
		}
		if kind == model.AdtySiteWalletMargin && !wallet.RequiredBalance.IsPositive() {
			if floor, floorErr := adtyMarginRequiredFloorTx(model.DB, siteID, &wallet); floorErr == nil && floor.IsPositive() {
				wallet.RequiredBalance = floor
			}
		}
		wallets = append(wallets, wallet)
	}
	return wallets, nil
}

// ListAdtySiteWalletLedger always applies the server-selected site boundary,
// including to the total count. An empty kind selects all three wallets.
func ListAdtySiteWalletLedger(siteID int, kind string, offset, limit int) ([]model.AdtySiteWalletLedger, int64, error) {
	return listAdtySiteWalletLedger(siteID, kind, offset, limit, false)
}

// ListAdtyOwnerSiteWalletLedger is the site-owner projection. The operating
// wallet remains in the database and in Root responses.
func ListAdtyOwnerSiteWalletLedger(siteID int, kind string, offset, limit int) ([]model.AdtySiteWalletLedger, int64, error) {
	if kind == model.AdtySiteWalletOperating {
		return nil, 0, ErrAdtyOperatingWalletHidden
	}
	if err := ReviewAdtyPooledSiteFinance(model.DB, siteID); err != nil {
		return nil, 0, err
	}
	return listAdtySiteWalletLedger(siteID, kind, offset, limit, true)
}

func listAdtySiteWalletLedger(siteID int, kind string, offset, limit int, hideOperating bool) ([]model.AdtySiteWalletLedger, int64, error) {
	if kind != "" && !validAdtySiteWalletKind(kind) {
		return nil, 0, ErrAdtySiteWalletInvalidKind
	}
	if offset < 0 || limit <= 0 {
		return nil, 0, ErrAdtySiteWalletInvalidOperation
	}
	if _, err := GetAdtySite(siteID); err != nil {
		return nil, 0, err
	}
	query := model.DB.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ?", siteID)
	if kind != "" {
		query = query.Where("wallet_kind = ?", kind)
	} else if hideOperating {
		query = query.Where("wallet_kind <> ?", model.AdtySiteWalletOperating)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]model.AdtySiteWalletLedger, 0)
	err := query.Order("id DESC").Offset(offset).Limit(min(limit, 100)).Find(&items).Error
	return items, total, err
}

func sameAdtySiteWalletOperation(entry *model.AdtySiteWalletLedger, siteID int, kind string, delta decimal.Decimal, change AdtySiteWalletChange) bool {
	return entry.SiteId == siteID && entry.WalletKind == kind && entry.Delta.Equal(delta) && entry.HoldDelta.Equal(change.HoldDelta) && entry.Type == change.Type &&
		entry.RefType == change.RefType && entry.RefId == change.RefID && entry.Reason == change.Reason &&
		entry.OperatorId == change.OperatorID && entry.AllowOverdraft == change.AllowOverdraft
}

// Wallet amounts must be exactly representable by the shared decimal(28,8)
// columns; otherwise some databases silently round and others reject writes.
func validAdtySiteWalletAmount(amount decimal.Decimal) bool {
	return amount.Equal(amount.Truncate(8)) && amount.Abs().LessThan(decimal.New(1, 20))
}

// preLockAdtySiteWalletsTx ensures site wallets exist and locks the requested
// wallet kinds in canonical order (earnings -> margin -> operating) to prevent
// cross-transaction AB-BA deadlocks.
func preLockAdtySiteWalletsTx(tx *gorm.DB, siteID int, kinds ...string) error {
	if tx == nil || siteID <= 0 || len(kinds) == 0 || !tx.Migrator().HasTable(&model.AdtySiteWallet{}) {
		return nil
	}
	if err := ensureAdtySiteWalletsTx(tx, siteID); err != nil {
		return err
	}
	want := make(map[string]struct{}, len(kinds))
	for _, k := range kinds {
		want[k] = struct{}{}
	}
	for _, kind := range []string{model.AdtySiteWalletEarnings, model.AdtySiteWalletMargin, model.AdtySiteWalletOperating} {
		if _, ok := want[kind]; !ok {
			continue
		}
		var w model.AdtySiteWallet
		if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND kind = ?", siteID, kind).First(&w).Error; err != nil {
			return err
		}
	}
	return nil
}

func applyAdtySiteWalletChangeTx(tx *gorm.DB, siteID int, kind string, delta decimal.Decimal, change AdtySiteWalletChange) (*model.AdtySiteWalletLedger, error) {
	if (delta.IsZero() && change.HoldDelta.IsZero()) || (!delta.IsZero() && !validAdtySiteWalletAmount(delta)) || (!change.HoldDelta.IsZero() && !validAdtySiteWalletAmount(change.HoldDelta)) {
		return nil, ErrAdtySiteWalletInvalidOperation
	}
	var existing model.AdtySiteWalletLedger
	lookup := tx.Where("idempotency_key = ?", change.IdempotencyKey).Limit(1).Find(&existing)
	if lookup.Error != nil {
		return nil, lookup.Error
	}
	if lookup.RowsAffected > 0 {
		if !sameAdtySiteWalletOperation(&existing, siteID, kind, delta, change) {
			return nil, ErrAdtySiteWalletIdempotencyConflict
		}
		return &existing, nil
	}
	if err := model.RejectRestrictedAdtyFundWrite(tx, model.AdtyFundWrite{
		SiteID: siteID, OrderRef: change.RefID,
	}); err != nil {
		return nil, err
	}
	if err := ensureAdtySiteWalletsTx(tx, siteID); err != nil {
		return nil, err
	}
	var wallet model.AdtySiteWallet
	if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND kind = ?", siteID, kind).First(&wallet).Error; err != nil {
		return nil, err
	}
	// Another request may have committed this operation while this one
	// waited for the wallet lock. Recheck before testing the new balance;
	// a successful debit replay must still succeed when funds are now zero.
	// A locking read also sees current committed rows under MySQL's
	// repeatable-read isolation instead of the initial lookup snapshot.
	lookup = model.AdtyLockForUpdate(tx).Where("idempotency_key = ?", change.IdempotencyKey).Limit(1).Find(&existing)
	if lookup.Error != nil {
		return nil, lookup.Error
	}
	if lookup.RowsAffected > 0 {
		if !sameAdtySiteWalletOperation(&existing, siteID, kind, delta, change) {
			return nil, ErrAdtySiteWalletIdempotencyConflict
		}
		return &existing, nil
	}
	if err := model.RejectRestrictedAdtyFundWrite(tx, model.AdtyFundWrite{
		SiteID: siteID, OrderRef: change.RefID,
	}); err != nil {
		return nil, err
	}
	// Personal withdrawals and transfers may use only this owner's share
	// of a historical site's earnings. The wallet lock serializes every hold.
	if kind == model.AdtySiteWalletEarnings && (change.Type == "earning_withdraw_freeze" || change.Type == ownerTransfer || change.Type == "earning_to_margin" || change.Type == "voucher_freeze" || change.Type == "owner_customer_balance" || change.Type == "owner_team_balance" || change.Type == "staff_balance" || change.Type == "financial_adjustment") {
		ownerID := change.OperatorID
		if ownerID == 0 {
			var err error
			ownerID, err = model.CurrentAdtySiteFinancialOwnerTx(tx, siteID)
			if err != nil {
				return nil, err
			}
		}
		available, _, err := ownerEarningsWalletTx(tx, siteID, ownerID, &wallet)
		if err != nil {
			return nil, err
		}
		if available.LessThan(change.HoldDelta.Sub(delta)) {
			return nil, ErrAdtySiteWalletInsufficient
		}
	}
	before := wallet.Balance
	after := before.Add(delta)
	frozenAfter := wallet.FrozenUSD
	excessFrozenAfter := wallet.ExcessFrozenUSD
	if kind == model.AdtySiteWalletMargin && change.MarginExcessOnly {
		excessFrozenAfter = excessFrozenAfter.Add(change.HoldDelta)
	} else {
		frozenAfter = frozenAfter.Add(change.HoldDelta)
	}
	if !validAdtySiteWalletAmount(after) || frozenAfter.IsNegative() || !validAdtySiteWalletAmount(frozenAfter) ||
		excessFrozenAfter.IsNegative() || !validAdtySiteWalletAmount(excessFrozenAfter) {
		return nil, ErrAdtySiteWalletInvalidOperation
	}
	totalFrozenAfter := frozenAfter.Add(excessFrozenAfter)
	cycleUsed := wallet.CycleUsedUSD
	cycleTier := wallet.CycleTierUSD
	if kind == model.AdtySiteWalletMargin && change.MarginExcessOnly {
		// Excess-margin funding (owner manual customer/team balance top-up,
		// approved staff balance increase, and site vouchers) preserves 100%
		// of the site level's required margin floor while allowing 100% of
		// excess balance above that floor without consuming the 90% gift/commission cycle.
		if after.IsNegative() || after.Sub(totalFrozenAfter).IsNegative() {
			return nil, ErrAdtySiteWalletInsufficient
		}
		netNewUse := change.HoldDelta.Sub(delta)
		if netNewUse.IsPositive() && !change.AllowOverdraft {
			floor, err := adtyMarginRequiredFloorTx(tx, siteID, &wallet)
			if err != nil {
				return nil, err
			}
			if after.Sub(totalFrozenAfter).LessThan(floor) {
				return nil, ErrAdtySiteWalletInsufficient
			}
		}
	} else {
		minimum := decimal.Zero
		if kind == model.AdtySiteWalletMargin {
			minimum = wallet.RequiredBalance
			if wallet.CycleTierUSD.IsPositive() {
				minimum = wallet.CycleTierUSD.Sub(AdtyMarginCycleLimit(wallet.CycleTierUSD))
			}
		}
		netNewUse := change.HoldDelta.Sub(delta)
		if (delta.IsNegative() || change.HoldDelta.IsPositive()) && (kind == model.AdtySiteWalletOperating || !change.AllowOverdraft) {
			// R4-011: For non-operating wallets (earnings/margin), settling an already-reserved
			// hold (delta < 0, HoldDelta <= delta, netNewUse <= 0) does not reduce free balance
			// and must still succeed even when prior AllowOverdraft settlements left Balance < FrozenUSD.
			// Operating wallets remain strictly non-negative per DEC-004.
			if kind == model.AdtySiteWalletOperating || netNewUse.IsPositive() {
				if after.IsNegative() || after.Sub(totalFrozenAfter).IsNegative() {
					return nil, ErrAdtySiteWalletInsufficient
				}
			}
			if netNewUse.IsPositive() && after.Sub(totalFrozenAfter).LessThan(minimum) {
				return nil, ErrAdtySiteWalletInsufficient
			}
		}
		if kind == model.AdtySiteWalletMargin && delta.IsNegative() && cycleTier.IsPositive() && !change.AllowOverdraft {
			newDraw := delta.Abs()
			if change.HoldDelta.IsNegative() {
				consumed := decimal.Min(change.HoldDelta.Abs(), newDraw)
				newDraw = newDraw.Sub(consumed)
			}
			allowed := AdtyMarginNewDraw(AdtyMarginDrawInput{
				TierUSD: cycleTier, Balance: before, Used: cycleUsed, Frozen: wallet.FrozenUSD, ExcessFrozen: wallet.ExcessFrozenUSD,
			})
			if newDraw.GreaterThan(allowed) {
				return nil, ErrAdtySiteWalletInsufficient
			}
			cycleUsed = cycleUsed.Add(delta.Abs())
		}
		if kind == model.AdtySiteWalletMargin && delta.IsPositive() && change.Type == "gift_spend" && cycleUsed.IsPositive() {
			cycleUsed = decimal.Max(decimal.Zero, cycleUsed.Sub(delta))
		}
		if kind == model.AdtySiteWalletMargin && delta.IsZero() && change.HoldDelta.IsPositive() && cycleTier.IsPositive() && !change.AllowOverdraft {
			allowed := AdtyMarginNewDraw(AdtyMarginDrawInput{
				TierUSD: cycleTier, Balance: before, Used: cycleUsed, Frozen: wallet.FrozenUSD, ExcessFrozen: wallet.ExcessFrozenUSD,
			})
			if change.HoldDelta.GreaterThan(allowed) {
				return nil, ErrAdtySiteWalletInsufficient
			}
		}
	}
	if kind == model.AdtySiteWalletMargin && delta.IsPositive() && change.OpenMarginCycle {
		// RequiredBalance is updated by tier assignment/configuration under
		// this same wallet lock. Never use a controller's earlier tier read.
		cycleTier = wallet.RequiredBalance
		if change.CycleTier.IsPositive() {
			cycleTier = change.CycleTier
		}
		cycleUsed = decimal.Zero
	}
	if kind == model.AdtySiteWalletMargin && delta.IsPositive() && change.ReleaseCycleUsed.IsPositive() && !change.OpenMarginCycle {
		cycleUsed = cycleUsed.Sub(change.ReleaseCycleUsed)
		if cycleUsed.IsNegative() {
			cycleUsed = decimal.Zero
		}
	}
	updates := map[string]any{
		"balance": after, "frozen_usd": frozenAfter, "excess_frozen_usd": excessFrozenAfter, "cycle_tier_usd": cycleTier, "cycle_used_usd": cycleUsed,
	}
	var cycleBefore, cycleAfter *string
	if kind == model.AdtySiteWalletMargin {
		var err error
		cycleBefore, err = snapshotAdtyMarginCycle(wallet)
		if err != nil {
			return nil, err
		}
		current := wallet
		current.Balance, current.FrozenUSD, current.ExcessFrozenUSD = after, frozenAfter, excessFrozenAfter
		current.CycleTierUSD, current.CycleUsedUSD = cycleTier, cycleUsed
		if delta.IsPositive() && change.OpenMarginCycle {
			current.CycleNumber++
			current.CycleStartedAt = time.Now().Unix()
			current.CycleInitialUSD = &after
			updates["cycle_number"] = current.CycleNumber
			updates["cycle_started_at"] = current.CycleStartedAt
			updates["cycle_initial_usd"] = after
		}
		cycleAfter, err = snapshotAdtyMarginCycle(current)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Model(&model.AdtySiteWallet{}).Where("id = ?", wallet.Id).Updates(updates).Error; err != nil {
		return nil, err
	}
	result := &model.AdtySiteWalletLedger{
		SiteId: siteID, WalletId: wallet.Id, WalletKind: kind, Delta: delta, HoldDelta: change.HoldDelta,
		BalanceBefore: before, BalanceAfter: after, Type: change.Type,
		RefType: change.RefType, RefId: change.RefID, Reason: change.Reason,
		OperatorId: change.OperatorID, AllowOverdraft: change.AllowOverdraft, IdempotencyKey: change.IdempotencyKey,
		MarginCycleBefore: cycleBefore, MarginCycleAfter: cycleAfter,
	}
	if err := tx.Create(result).Error; err != nil {
		return nil, err
	}
	if _, err := appendAdtyMarginWalletOfficialAuditTx(tx, result, change, wallet.FrozenUSD, frozenAfter, cycleUsed, cycleTier); err != nil {
		return nil, err
	}
	return result, nil
}

// ApplyAdtySiteWalletChange changes one wallet and appends one immutable ledger
// row. The idempotency key is mandatory because every financial mutation must
// have a stable replay boundary.
// OpenAdtyMarginCycle credits the frozen tier and starts a 90% draw cycle.
func OpenAdtyMarginCycle(siteID int, tier decimal.Decimal, key string) error {
	if !tier.IsPositive() {
		return ErrAdtySiteWalletInvalidOperation
	}
	if err := EnsureAdtySiteWallets(siteID); err != nil {
		return err
	}
	_, err := ApplyAdtySiteWalletChange(siteID, model.AdtySiteWalletMargin, tier, AdtySiteWalletChange{
		Type: "margin_cycle_open", IdempotencyKey: key, OpenMarginCycle: true, CycleTier: tier,
	})
	return err
}

// CollectAdtyFundAnomalies compares wallet ledgers, open shortfall holds, and the margin cycle cap,
// and independently detects the remaining G.13.4 kinds. It records a new open disposition for
// each detected item and does not invent a tolerance, repair money, or apply a write restriction.
func CollectAdtyFundAnomalies(siteID int) ([]string, error) {
	items, err := detectAdtyFundAnomalies(siteID)
	if err != nil {
		return nil, err
	}
	kinds := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		if err := upsertOpenAdtyAnomalyDisposition(item); err != nil {
			return nil, err
		}
		if _, ok := seen[item.Kind]; ok {
			continue
		}
		seen[item.Kind] = struct{}{}
		kinds = append(kinds, item.Kind)
	}
	return kinds, nil
}

// ListAdtyAnomalyDispositions returns review rows for one site. It does not create any.
// Rows recorded with SiteId are listed directly. Legacy rows used the site id as Ref.
func ListAdtyAnomalyDispositions(siteID int) ([]model.AdtyAnomalyDisposition, error) {
	rows, _, err := PageAdtyAnomalyDispositions(siteID, "", "", "", 0, 50)
	return rows, err
}

func PageAdtyAnomalyDispositions(siteID int, status, order, kind string, offset, limit int) ([]model.AdtyAnomalyDisposition, int64, error) {
	if siteID < 0 || offset < 0 || limit < 1 || (status != "" && status != "open" && status != "reviewing" && status != "closed") {
		return nil, 0, common.NewMessage("Invalid parameters")
	}
	q := model.DB.Model(&model.AdtyAnomalyDisposition{})
	if siteID == 0 {
		q = q.Where("site_id = 0 AND (team_id > 0 OR ref LIKE ? OR order_ref <> '' OR account_id > 0)", "main:%")
	} else {
		q = q.Where("(site_id = ? OR (site_id = 0 AND team_id = 0 AND ref = ?))", siteID, strconv.Itoa(siteID))
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if order = strings.TrimSpace(order); order != "" {
		q = q.Where("order_ref = ?", order)
	}
	if kind = strings.TrimSpace(kind); kind != "" {
		q = q.Where("kind = ?", kind)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.AdtyAnomalyDisposition
	err := q.Order("CASE status WHEN 'open' THEN 0 WHEN 'reviewing' THEN 1 ELSE 2 END, CASE WHEN status = 'closed' THEN -id ELSE id END").Offset(offset).Limit(min(limit, 100)).Find(&rows).Error
	return rows, total, err
}

// ReviewAdtyAnomalyDisposition records a Root review. It does not move wallets or customer quota.
// Setting status to reviewing restricts related fund writes for the recorded account, order, or
// site; closing the item lifts that restriction.
func ReviewAdtyAnomalyDisposition(id int64, operatorID int, status, note string) error {
	_, err := ReviewAdtyAnomalyDispositionWithAudit(id, operatorID, status, note)
	return err
}

func ReviewAdtyAnomalyDispositionWithAudit(id int64, operatorID int, status, note string) (string, error) {
	status, note = strings.TrimSpace(status), strings.TrimSpace(note)
	if id <= 0 || operatorID <= 0 || (status != "reviewing" && status != "closed") {
		return "", common.NewMessage("Invalid parameters")
	}
	if status == "closed" && note == "" {
		return "", common.NewMessage("Invalid parameters")
	}
	var auditEvent *model.AuditLog
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var row model.AdtyAnomalyDisposition
		if err := model.AdtyLockForUpdate(tx).First(&row, id).Error; err != nil {
			return err
		}
		if row.Status == "closed" || (row.Status == "reviewing" && status != "closed") || (row.Status != "open" && row.Status != "reviewing") {
			return common.NewMessage("Invalid parameters")
		}
		if err := tx.Model(&row).Updates(map[string]any{
			"status": status, "note": note, "operator_id": operatorID,
		}).Error; err != nil {
			return err
		}
		if err := notifyAdtyAnomalyDispositionTx(tx, row, operatorID, status, note); err != nil {
			return err
		}
		var auditErr error
		auditEvent, auditErr = appendAdtyOfficialAuditTx(tx, operatorID, row.SiteId, "anomaly_disposition", strconv.FormatInt(row.Id, 10),
			"adty.anomaly_disposition.review", note, "",
			map[string]any{"status": row.Status, "note": row.Note},
			map[string]any{"status": status, "note": note, "kind": row.Kind, "order_ref": row.OrderRef},
			model.AuditResultSuccess)
		return auditErr
	})
	if err != nil {
		return "", err
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	return eventID, nil
}

func notifyAdtyAnomalyDispositionTx(tx *gorm.DB, row model.AdtyAnomalyDisposition, operatorID int, status, note string) error {
	recipientID, targetSiteID, includeInternalNote := resolveAdtyAnomalyDispositionRecipientTx(tx, row, operatorID)
	if recipientID <= 0 {
		return nil
	}
	publisher := "root"
	var operator model.User
	if err := tx.Select("username").First(&operator, operatorID).Error; err == nil && operator.Username != "" {
		publisher = operator.Username
	}
	content := "资金异常处置已记录。Fund anomaly review recorded. " + status
	if includeInternalNote && note != "" {
		content += ": " + note
	}
	now := time.Now().Unix()
	notice := model.AdtyNotificationPublication{
		IssuerSiteId:    0,
		PublisherId:     operatorID,
		PublisherName:   publisher,
		Title:           "资金异常处置",
		Content:         content,
		Kind:            "system",
		Audience:        "user",
		TargetSiteId:    targetSiteID,
		TargetSiteName:  "user:" + strconv.Itoa(recipientID),
		Business:        "fund_anomaly",
		TelegramChannel: adtyPrivateTelegramMarker,
		Status:          "published",
		Revision:        1,
		PublishedAt:     now,
		UpdatedAt:       now,
		UpdatedBy:       operatorID,
		ReadKey:         adtyNotificationVersion("anomaly:" + strconv.FormatInt(row.Id, 10) + ":" + status + ":user:" + strconv.Itoa(recipientID)),
	}
	if err := tx.Create(&notice).Error; err != nil {
		return err
	}
	if tx.Migrator().HasTable(&model.AdtyTelegramDelivery{}) &&
		tx.Migrator().HasTable(&model.AdtyTelegramBinding{}) &&
		tx.Migrator().HasTable(&model.AdtyTelegramBot{}) {
		if err := queueAdtyPrivatePublicationTelegramTx(tx, &notice); err != nil {
			return err
		}
	}
	return nil
}

func resolveAdtyAnomalyDispositionRecipientTx(tx *gorm.DB, row model.AdtyAnomalyDisposition, operatorID int) (recipientID, targetSiteID int, includeInternalNote bool) {
	siteID := row.SiteId
	if siteID <= 0 && row.TeamId == 0 {
		if parsed, err := strconv.Atoi(strings.TrimSpace(row.Ref)); err == nil && parsed > 0 {
			siteID = parsed
		}
	}
	if siteID <= 0 && tx.Migrator().HasTable(&model.AdtyTopupOrder{}) {
		for _, candidate := range []string{strings.TrimSpace(row.OrderRef), strings.TrimSpace(row.Ref)} {
			if candidate == "" {
				continue
			}
			var orders []model.AdtyTopupOrder
			if err := tx.Select("site_id", "user_id").Where("trade_no = ?", candidate).Limit(1).Find(&orders).Error; err == nil && len(orders) > 0 {
				if orders[0].SiteId > 0 {
					siteID = orders[0].SiteId
				}
				if row.AccountId <= 0 && orders[0].UserId > 0 {
					row.AccountId = orders[0].UserId
				}
				break
			}
		}
	}
	if siteID > 0 {
		var sites []model.AdtySite
		if err := tx.Select("id", "owner_user_id").Where("id = ?", siteID).Limit(1).Find(&sites).Error; err == nil && len(sites) > 0 && sites[0].OwnerUserId > 0 {
			recipientID = sites[0].OwnerUserId
		}
	}
	if recipientID <= 0 && row.AccountId > 0 {
		recipientID = row.AccountId
	}
	if recipientID <= 0 && operatorID > 0 {
		recipientID = operatorID
		includeInternalNote = true
	}
	if recipientID <= 0 {
		return 0, 0, false
	}
	var users []model.User
	if err := tx.Select("id", "site_id", "staff_site_id", "role").Where("id = ?", recipientID).Limit(1).Find(&users).Error; err == nil && len(users) > 0 {
		if users[0].StaffSiteId > 0 {
			return 0, 0, false
		}
		targetSiteID = users[0].SiteId
		if users[0].Role >= common.RoleAdminUser {
			includeInternalNote = true
		}
	}
	return recipientID, targetSiteID, includeInternalNote
}

// TransferAdtyEarningToMargin opens a current-target cycle only after the
// earnings debit and the actual margin credit succeed in the same transaction.
func TransferAdtyEarningToMargin(siteID, operator int, amount decimal.Decimal, key string) error {
	if !amount.IsPositive() || strings.TrimSpace(key) == "" {
		return ErrAdtySiteWalletInvalidOperation
	}
	earnKey, marginKey, err := adtyEarningToMarginKeys(siteID, key)
	if err != nil {
		return err
	}
	if err := EnsureAdtySiteWallets(siteID); err != nil {
		return err
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		// R5-050: Lock AdtySite BEFORE earnings -> margin wallets to obey canonical lock order
		// and reject non-replay transfers from closing/paused/closed sites or non-owners.
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).First(&site, siteID).Error; err != nil {
			return err
		}
		var existingEarn model.AdtySiteWalletLedger
		earnLookup := tx.Where("idempotency_key = ?", earnKey).Limit(1).Find(&existingEarn)
		if earnLookup.Error != nil {
			return earnLookup.Error
		}
		if earnLookup.RowsAffected == 0 {
			if site.OwnerUserId != operator || site.Status != model.AdtySiteStatusActive {
				return common.NewMessage("Site is not active")
			}
		}
		if _, err := applyAdtySiteWalletChangeTx(tx, siteID, model.AdtySiteWalletEarnings, amount.Neg(), AdtySiteWalletChange{
			Type: "earning_to_margin", IdempotencyKey: earnKey, OperatorID: operator,
		}); err != nil {
			return err
		}
		var margin model.AdtySiteWallet
		if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND kind = ?", siteID, model.AdtySiteWalletMargin).First(&margin).Error; err != nil {
			return err
		}
		// A replay returns the existing pair even if Root has since removed
		// the tier; it never opens another cycle or depends on current funds.
		var existing model.AdtySiteWalletLedger
		lookup := tx.Where("idempotency_key = ?", marginKey).Limit(1).Find(&existing)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 {
			var configured int64
			if err := tx.Model(&model.AdtySiteTier{}).Where("code = ? AND enabled = ?", site.TierCode, true).Count(&configured).Error; err != nil {
				return err
			}
			if configured == 0 || !margin.RequiredBalance.IsPositive() {
				return common.NewMessage("Margin tier is not configured")
			}
		}
		_, err := applyAdtySiteWalletChangeTx(tx, siteID, model.AdtySiteWalletMargin, amount, AdtySiteWalletChange{
			Type: "earning_to_margin", IdempotencyKey: marginKey, OperatorID: operator,
			OpenMarginCycle: true,
		})
		return err
	}); err != nil {
		return err
	}
	// 转账已经提交，兑现失败不回滚它；未兑现的仍是 pending。
	if _, err := ResumePendingAdtyGiftFulfillments(siteID); err != nil {
		common.SysLog(common.LogText("gift fulfillment: resume after margin top-up site %d: %s", siteID, err.Error()))
	}
	if err := ResumeAdtySiteInviteCoverage(siteID, time.Now()); err != nil {
		common.SysLog(common.LogText("site invite coverage: resume after margin top-up site %d: %s", siteID, err.Error()))
	}
	return nil
}

// CreditAdtySiteMarginByRoot records a direct Root-authorized margin deposit
// without fabricating a main-site wallet debit, opens a new 90% margin cycle,
// and resumes pending gift fulfillments after commit.
func CreditAdtySiteMarginByRoot(siteID, rootUserID int, amount decimal.Decimal, reason, key string) (*model.AdtySiteWalletLedger, error) {
	ledger, _, _, err := CreditAdtySiteMarginByRootWithAudit(siteID, rootUserID, amount, reason, key)
	return ledger, err
}

func CreditAdtySiteMarginByRootWithAudit(siteID, rootUserID int, amount decimal.Decimal, reason, key string) (*model.AdtySiteWalletLedger, string, bool, error) {
	reason = strings.TrimSpace(reason)
	key = strings.TrimSpace(key)
	if siteID <= 0 || rootUserID <= 0 || !amount.IsPositive() || !validAdtySiteWalletAmount(amount) ||
		reason == "" || len([]rune(reason)) > 200 || key == "" || len(key) > 64 {
		return nil, "", false, ErrAdtySiteWalletInvalidOperation
	}
	scopedKey := "site:" + strconv.Itoa(siteID) + ":margin-root:" + key
	if len(scopedKey) > 128 {
		return nil, "", false, ErrAdtySiteWalletIdempotencyKey
	}
	if err := EnsureAdtySiteWallets(siteID); err != nil {
		return nil, "", false, err
	}
	var ledger *model.AdtySiteWalletLedger
	var auditEvent *model.AuditLog
	var replayed bool
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).First(&site, siteID).Error; err != nil {
			return err
		}
		if site.Status == model.AdtySiteStatusClosed {
			return common.NewMessage("Site is closed")
		}
		var margin model.AdtySiteWallet
		if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND kind = ?", siteID, model.AdtySiteWalletMargin).First(&margin).Error; err != nil {
			return err
		}
		var existing model.AdtySiteWalletLedger
		lookup := tx.Where("idempotency_key = ?", scopedKey).Limit(1).Find(&existing)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected > 0 {
			if existing.SiteId != siteID || existing.WalletKind != model.AdtySiteWalletMargin || !existing.Delta.Equal(amount) || existing.Reason != reason {
				return model.ErrAdtyIdempotencyConflict
			}
			ledger = &existing
			replayed = true
			return nil
		}
		if !margin.RequiredBalance.IsPositive() && strings.TrimSpace(site.TierCode) != "" {
			var tier model.AdtySiteTier
			if err := tx.Where("code = ? AND enabled = ?", site.TierCode, true).First(&tier).Error; err == nil && tier.DepositUSD.IsPositive() {
				if err := tx.Model(&margin).Update("required_balance", tier.DepositUSD).Error; err != nil {
					return err
				}
				margin.RequiredBalance = tier.DepositUSD
			}
		}
		var err error
		ledger, err = applyAdtySiteWalletChangeTx(tx, siteID, model.AdtySiteWalletMargin, amount, AdtySiteWalletChange{
			Type:            "margin_root_credit",
			RefType:         "root_margin",
			RefID:           key,
			Reason:          reason,
			OperatorID:      rootUserID,
			IdempotencyKey:  scopedKey,
			OpenMarginCycle: true,
		})
		if err != nil {
			return err
		}
		auditEvent, err = recordSiteAuditWithIntentTx(tx, siteID, "margin.root_credit", rootUserID,
			map[string]any{"margin_balance": ledger.BalanceBefore.String()},
			map[string]any{"margin_balance": ledger.BalanceAfter.String(), "amount_usd": amount.String(), "owner_user_id": site.OwnerUserId, "reason": reason, "ledger_id": ledger.Id},
			reason)
		return err
	}); err != nil {
		return nil, "", false, err
	}
	eventID := ""
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	if !replayed {
		if _, err := ResumePendingAdtyGiftFulfillments(siteID); err != nil {
			common.SysLog(common.LogText("gift fulfillment: resume after root margin credit site %d: %s", siteID, err.Error()))
		}
		if err := ResumeAdtySiteInviteCoverage(siteID, time.Now()); err != nil {
			common.SysLog(common.LogText("site invite coverage: resume after root margin credit site %d: %s", siteID, err.Error()))
		}
	}
	return ledger, eventID, replayed, nil
}

// adtyEarningToMarginKeys scopes the client idempotency key to one site. The
// ledger has a single global unique index on idempotency_key, so an unscoped
// client string would let one site fail, pre-empt, or probe another site's
// transfers. The 128 limit is the column width.
func adtyEarningToMarginKeys(siteID int, key string) (string, string, error) {
	scoped := "site:" + strconv.Itoa(siteID) + ":" + strings.TrimSpace(key)
	earn, margin := scoped+":earn", scoped+":margin"
	if len(earn) > 128 || len(margin) > 128 {
		return "", "", ErrAdtySiteWalletIdempotencyKey
	}
	return earn, margin, nil
}

func ApplyAdtySiteWalletChange(siteID int, kind string, delta decimal.Decimal, change AdtySiteWalletChange) (*model.AdtySiteWalletLedger, error) {
	if !validAdtySiteWalletKind(kind) {
		return nil, ErrAdtySiteWalletInvalidKind
	}
	if siteID <= 0 || delta.IsZero() || !validAdtySiteWalletAmount(delta) || strings.TrimSpace(change.Type) == "" {
		return nil, ErrAdtySiteWalletInvalidOperation
	}
	change.IdempotencyKey = strings.TrimSpace(change.IdempotencyKey)
	if change.IdempotencyKey == "" || len(change.IdempotencyKey) > 128 {
		return nil, ErrAdtySiteWalletIdempotencyKey
	}
	change.RefType = strings.TrimSpace(change.RefType)
	change.RefID = strings.TrimSpace(change.RefID)

	var result *model.AdtySiteWalletLedger
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = applyAdtySiteWalletChangeTx(tx, siteID, kind, delta, change)
		return err
	})
	if err != nil {
		if adtyUniqueViolation(err) {
			var existing model.AdtySiteWalletLedger
			lookup := model.DB.Where("idempotency_key = ?", change.IdempotencyKey).Limit(1).Find(&existing)
			if lookup.Error == nil && lookup.RowsAffected > 0 {
				if sameAdtySiteWalletOperation(&existing, siteID, kind, delta, change) {
					return &existing, nil
				}
				return nil, ErrAdtySiteWalletIdempotencyConflict
			}
		}
		return nil, err
	}
	return result, nil
}

func ValidateAdtySiteTier(tier model.AdtySiteTier) error {
	if strings.TrimSpace(tier.Code) == "" || utf8.RuneCountInString(strings.TrimSpace(tier.Code)) > 32 || strings.TrimSpace(tier.Name) == "" {
		return ErrAdtySiteTierInvalid
	}
	if tier.SortOrder < 0 || tier.MinEffectiveUsers < 0 {
		return ErrAdtySiteTierInvalid
	}
	for _, amount := range []decimal.Decimal{tier.MinMonthlyUSD, tier.MinPrincipalUSD, tier.MinConsumptionUSD, tier.DepositUSD} {
		if amount.IsNegative() || !amount.Equal(amount.Truncate(8)) || amount.GreaterThanOrEqual(decimal.New(1, 12)) {
			return ErrAdtySiteTierInvalid
		}
	}
	// Supply discount is defined per site level and must be a positive decimal(10,6).
	if tier.CostFactor == nil || !tier.CostFactor.IsPositive() ||
		tier.CostFactor.GreaterThanOrEqual(decimal.New(1, 4)) || !tier.CostFactor.Equal(tier.CostFactor.Truncate(6)) {
		return ErrAdtySiteTierInvalid
	}
	return nil
}

// HasReadyAdtySiteTier reports whether at least one enabled site level with a
// positive supply discount exists.
func HasReadyAdtySiteTier(db *gorm.DB) (bool, error) {
	if db == nil {
		db = model.DB
	}
	if db == nil {
		return false, nil
	}
	var count int64
	if err := db.Model(&model.AdtySiteTier{}).
		Where("enabled = ? AND cost_factor IS NOT NULL AND cost_factor > 0", true).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func lowestEnabledAdtySiteTier(tiers []model.AdtySiteTier) *model.AdtySiteTier {
	var lowest *model.AdtySiteTier
	for i := range tiers {
		tier := &tiers[i]
		if !tier.Enabled || tier.CostFactor == nil || !tier.CostFactor.IsPositive() {
			continue
		}
		if lowest == nil || adtySiteTierHigher(*lowest, *tier) {
			lowest = tier
		}
	}
	return lowest
}

func defaultAdtySiteTierTx(tx *gorm.DB) (*model.AdtySiteTier, error) {
	var tiers []model.AdtySiteTier
	if err := tx.Where("enabled = ? AND cost_factor IS NOT NULL AND cost_factor > 0", true).Find(&tiers).Error; err != nil {
		return nil, err
	}
	return lowestEnabledAdtySiteTier(tiers), nil
}

// SaveAdtySiteTiers upserts definitions by their stable code. It intentionally
// does not delete omitted rows, so a partial admin update cannot silently
// remove a level that is still assigned to a site.
func SaveAdtySiteTiers(tiers []model.AdtySiteTier) error {
	return saveAdtySiteTiers(tiers, false)
}

func UpdateAdtySiteTiers(tiers []model.AdtySiteTier) error {
	return saveAdtySiteTiers(tiers, true)
}

func saveAdtySiteTiers(tiers []model.AdtySiteTier, existingOnly bool) error {
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		seen := map[string]struct{}{}
		for _, input := range tiers {
			input.Code = strings.TrimSpace(input.Code)
			input.Name = strings.TrimSpace(input.Name)
			input.MinEffectiveUsers = 0
			input.MinConsumptionUSD = decimal.Zero
			if _, ok := seen[input.Code]; ok {
				return ErrAdtySiteTierInvalid
			}
			seen[input.Code] = struct{}{}
			if err := ValidateAdtySiteTier(input); err != nil {
				return err
			}
			var current model.AdtySiteTier
			find := tx.Where("code = ?", input.Code).Limit(1).Find(&current)
			if find.Error != nil {
				return find.Error
			}
			if find.RowsAffected == 0 {
				if existingOnly {
					return ErrAdtySiteTierNotFound
				}
				if err := tx.Create(&input).Error; err != nil {
					return err
				}
				continue
			}
			if err := tx.Model(&model.AdtySiteTier{}).Where("id = ?", current.Id).Updates(map[string]any{
				"name": input.Name, "min_monthly_usd": input.MinMonthlyUSD,
				"min_effective_users": 0, "min_principal_usd": input.MinPrincipalUSD,
				"min_consumption_usd": decimal.Zero, "cost_factor": input.CostFactor,
				"deposit_usd": input.DepositUSD, "enabled": input.Enabled,
				"sort_order": input.SortOrder,
			}).Error; err != nil {
				return err
			}
			// Fixed mode fixes the assigned level, not an obsolete copy of
			// that level's Root-managed financial terms.
			if err := tx.Model(&model.AdtySite{}).Where("tier_code = ?", input.Code).Updates(map[string]any{
				"tier_cost_factor":         input.CostFactor,
				"pending_tier_cost_factor": nil, "pending_tier_cost_factor_at": 0,
			}).Error; err != nil {
				return err
			}
			sites := tx.Model(&model.AdtySite{}).Select("id").Where("tier_code = ?", input.Code)
			if err := tx.Model(&model.AdtySiteWallet{}).
				Where("kind = ? AND site_id IN (?)", model.AdtySiteWalletMargin, sites).
				Update("required_balance", input.DepositUSD).Error; err != nil {
				return err
			}
		}
		// R3-026: Never allow disabling the last ready site tier while reseller is enabled
		// or non-closed subsites exist, which would wipe tier_cost_factor and brick billing.
		hasReady, err := HasReadyAdtySiteTier(tx)
		if err != nil {
			return err
		}
		if !hasReady {
			var openSites int64
			if tx.Migrator().HasTable(&model.AdtySite{}) {
				if err := tx.Model(&model.AdtySite{}).
					Where("status <> ?", model.AdtySiteStatusClosed).
					Count(&openSites).Error; err != nil {
					return err
				}
			}
			if adty_setting.GetResellerSetting().Enabled || openSites > 0 {
				return common.NewMessage("Keep at least one enabled site level with a positive supply discount while reseller sites are active")
			}
		}
		return nil
	})
	if err == nil {
		InvalidateAdtySiteCache()
	}
	return err
}

// CreateAdtySiteTiers reserves each code atomically before it can be assigned.
func CreateAdtySiteTiers(tiers []model.AdtySiteTier) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		for _, tier := range tiers {
			tier.Id = 0
			tier.Name, tier.Code = strings.TrimSpace(tier.Name), strings.TrimSpace(tier.Code)
			tier.MinEffectiveUsers = 0
			tier.MinConsumptionUSD = decimal.Zero
			if err := ValidateAdtySiteTier(tier); err != nil {
				return err
			}
			result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "code"}}, DoNothing: true}).Create(&tier)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return ErrAdtySiteTierConflict
			}
		}
		return nil
	})
}

var ErrAdtySiteTierConflict = common.NewMessage("This level code already exists")

func ListAdtySiteTiers() ([]model.AdtySiteTier, error) {
	var tiers []model.AdtySiteTier
	err := model.DB.Order("min_principal_usd, sort_order, id").Find(&tiers).Error
	return tiers, err
}

// AdtySiteTierStats is the lifetime snapshot used to choose a site grade.
// Only cumulative effective customer top-up principal (PrincipalUSD = max(0,
// OnlinePrincipalUSD + AdjustmentUSD)) gates upgrade/downgrade.
type AdtySiteTierStats struct {
	EffectiveUsers     int64           `json:"effective_users"`
	OnlinePrincipalUSD decimal.Decimal `json:"online_principal_usd"`
	AdjustmentUSD      decimal.Decimal `json:"adjustment_usd"`
	PrincipalUSD       decimal.Decimal `json:"principal_usd"`
	ConsumptionUSD     decimal.Decimal `json:"consumption_usd"`
}

func (stats AdtySiteTierStats) Meets(tier model.AdtySiteTier) bool {
	return !stats.PrincipalUSD.LessThan(tier.MinPrincipalUSD)
}

func adtySiteTierHigher(a, b model.AdtySiteTier) bool {
	if !a.MinPrincipalUSD.Equal(b.MinPrincipalUSD) {
		return a.MinPrincipalUSD.GreaterThan(b.MinPrincipalUSD)
	}
	if a.SortOrder != b.SortOrder {
		return a.SortOrder > b.SortOrder
	}
	return a.Id > b.Id
}

// SelectAdtySiteTier returns the highest enabled grade whose three lifetime
// thresholds are all met and whose supply discount is positive. Monthly spend is not a criterion.
func SelectAdtySiteTier(tiers []model.AdtySiteTier, stats AdtySiteTierStats) *model.AdtySiteTier {
	var selected *model.AdtySiteTier
	for i := range tiers {
		tier := &tiers[i]
		if !tier.Enabled || !tier.CostFactor.IsPositive() || !stats.Meets(*tier) {
			continue
		}
		if selected == nil || adtySiteTierHigher(*tier, *selected) {
			selected = tier
		}
	}
	return selected
}

func adtySiteTierCountsAsPrincipal(order model.AdtyTopupOrder, ownerUserID int) bool {
	// Completely exclude all owner recharges/transfers (margin top-up, owner
	// self-use API top-up, margin vouchers, owner vouchers, or any order placed
	// by the site owner themselves).
	if order.IsOwnerBusiness() {
		return false
	}
	if ownerUserID > 0 && order.UserId == ownerUserID {
		return false
	}
	if order.Provider == model.AdtyVoucherProvider {
		return order.Method == model.AdtyVoucherPurposeSiteCustomer
	}
	return model.IsAdtyPaymentProvider(order.Provider)
}

func loadAdtySiteTierStatsTx(tx *gorm.DB, siteID int) (AdtySiteTierStats, error) {
	var stats AdtySiteTierStats
	var site model.AdtySite
	if err := tx.Select("id", "owner_user_id", "tier_principal_adjustment_usd").Where("id = ?", siteID).Limit(1).Find(&site).Error; err != nil {
		return stats, err
	}
	if err := tx.Model(&model.User{}).
		Where("site_id = ? AND status = ? AND staff_site_id = 0", siteID, common.UserStatusEnabled).
		Count(&stats.EffectiveUsers).Error; err != nil {
		return stats, err
	}

	var orders []model.AdtyTopupOrder
	credited := tx.Model(&model.TopUp{}).Select("trade_no").Where("status IN ?", []string{common.TopUpStatusSuccess, model.TopUpStatusRefunded})
	if err := tx.Where("site_id = ? AND trade_no IN (?)", siteID, credited).Find(&orders).Error; err != nil {
		return stats, err
	}
	principal := decimal.Zero
	for i := range orders {
		if !adtySiteTierCountsAsPrincipal(orders[i], site.OwnerUserId) {
			continue
		}
		principal = principal.Add(adtyRetainedTopUpUSD(&orders[i]))
	}
	stats.OnlinePrincipalUSD = principal
	stats.AdjustmentUSD = site.TierPrincipalAdjustmentUSD
	effective := principal.Add(site.TierPrincipalAdjustmentUSD)
	if effective.IsNegative() {
		effective = decimal.Zero
	}
	stats.PrincipalUSD = effective

	// R10-021: Sum incremental net billed quota across request/direct/task/mj
	// so Realtime WSS (direct:* + request:*) and task:*:adjust are never double-counted
	// and downward task/mj refunds properly reduce net site consumption.
	var billed int64
	if err := tx.Model(&model.BillingOperation{}).
		Where("site_id = ? AND COALESCE(payer_kind,'') <> ?", siteID, model.BillingPayerOwner).
		Where("(state = ? AND kind IN ?) OR (state = ? AND kind IN ?)",
			model.BillingSettled, []string{"request", "direct", "task", "mj_charge"},
			model.BillingRefunded, []string{"request", "task", "mj_refund"},
		).
		Select(`COALESCE(SUM(CASE
			WHEN state = 'settled' AND kind = 'request' THEN applied_quota - stream_wallet_quota
			WHEN state = 'settled' AND kind = 'direct' THEN applied_quota - reserved_quota
			WHEN state = 'settled' AND kind = 'task' THEN applied_quota - reserved_quota
			WHEN state = 'settled' AND kind = 'mj_charge' THEN applied_quota
			WHEN state = 'refunded' AND kind = 'request' THEN -stream_wallet_quota
			WHEN state = 'refunded' AND kind = 'task' THEN applied_quota - reserved_quota
			WHEN state = 'refunded' AND kind = 'mj_refund' THEN -reserved_quota
			ELSE 0 END), 0)`).
		Scan(&billed).Error; err != nil {
		return stats, err
	}
	if billed < 0 {
		billed = 0
	}
	stats.ConsumptionUSD = QuotaToUSD(billed)
	return stats, nil
}

// ComputeAdtySiteTierStats returns the site's lifetime A.10 totals.
func ComputeAdtySiteTierStats(siteID int) (AdtySiteTierStats, error) {
	if siteID <= 0 {
		return AdtySiteTierStats{}, ErrAdtySiteTierInvalid
	}
	return loadAdtySiteTierStatsTx(model.DB, siteID)
}

// AdtySiteTierPeriodID is the idempotent period for a statistic timestamp.
func AdtySiteTierPeriodID(now int64) string {
	if now < 0 {
		now = 0
	}
	return time.Unix(now, 0).UTC().Format("2006-01-02")
}

func applyAdtySiteTierAssignmentTx(tx *gorm.DB, siteID int, selected *model.AdtySiteTier, now int64) error {
	updates := map[string]any{
		"tier_code": "", "tier_mode": AdtySiteTierModeAuto, "tier_updated_at": now,
		"pending_tier_cost_factor": nil, "pending_tier_cost_factor_at": 0,
	}
	required := decimal.Zero
	if selected != nil {
		updates["tier_code"] = selected.Code
		updates["tier_cost_factor"] = selected.CostFactor
		required = selected.DepositUSD
	} else {
		updates["tier_cost_factor"] = nil
	}
	if err := tx.Model(&model.AdtySiteWallet{}).Where("site_id = ? AND kind = ?", siteID, model.AdtySiteWalletMargin).
		Update("required_balance", required).Error; err != nil {
		return err
	}
	return tx.Model(&model.AdtySite{}).Where("id = ?", siteID).Updates(updates).Error
}

func recordAdtySiteTierEvaluationTx(tx *gorm.DB, siteID int, periodID string, now int64, stats AdtySiteTierStats, code, previous string, warned bool) error {
	return tx.Create(&model.AdtySiteTierEvaluation{
		SiteId: siteID, PeriodId: periodID, StatAt: now,
		EffectiveUsers: stats.EffectiveUsers, PrincipalUSD: stats.PrincipalUSD, ConsumptionUSD: stats.ConsumptionUSD,
		TierCode: code, PreviousTierCode: previous, Warned: warned,
	}).Error
}

// EvaluateAdtySiteTierAt is the same evaluation keyed by an explicit period ID.
// Re-running the same site and period does not repeat the grade write, margin
// mutation, or lowest-grade warning.
func EvaluateAdtySiteTierAt(siteID int, now int64, periodID string) (*model.AdtySite, error) {
	periodID = strings.TrimSpace(periodID)
	if siteID <= 0 || periodID == "" || len(periodID) > 32 {
		return nil, ErrAdtySiteTierInvalid
	}
	var site model.AdtySite
	var warn bool
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteID).First(&site).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		if err := ensureAdtySiteWalletsTx(tx, siteID); err != nil {
			return err
		}
		if site.TierMode == AdtySiteTierModeManual {
			return nil
		}
		var existing model.AdtySiteTierEvaluation
		found := tx.Where("site_id = ? AND period_id = ?", siteID, periodID).Limit(1).Find(&existing)
		if found.Error != nil {
			return found.Error
		}
		if found.RowsAffected > 0 {
			return nil
		}
		stats, err := loadAdtySiteTierStatsTx(tx, siteID)
		if err != nil {
			return err
		}
		var tiers []model.AdtySiteTier
		if err := tx.Where("enabled = ?", true).Find(&tiers).Error; err != nil {
			return err
		}
		// R3-026: Bail out without wiping existing site tier assignment if no enabled tiers exist.
		if len(tiers) == 0 {
			return nil
		}
		selected := SelectAdtySiteTier(tiers, stats)
		warn = selected == nil && len(tiers) > 0
		if selected == nil {
			selected = lowestEnabledAdtySiteTier(tiers)
		}
		previous := site.TierCode
		next := ""
		if selected != nil {
			next = selected.Code
		}
		changed := previous != next
		if changed {
			if err := applyAdtySiteTierAssignmentTx(tx, siteID, selected, now); err != nil {
				return err
			}
		}
		if err := recordAdtySiteTierEvaluationTx(tx, siteID, periodID, now, stats, next, previous, warn); err != nil {
			return err
		}
		if !changed && !warn {
			return nil
		}
		site = model.AdtySite{}
		return tx.Where("id = ?", siteID).First(&site).Error
	})
	if err != nil {
		return nil, err
	}
	if warn {
		notifyAdtySiteTierBelowMinimum(siteID, periodID)
	}
	InvalidateAdtySiteCache()
	return &site, nil
}

func notifyAdtySiteTierBelowMinimum(siteID int, periodID string) {
	adtyNotifyRootAdmins(func(lang string) (string, string) {
		if strings.HasPrefix(strings.ToLower(lang), "zh") {
			return "分站等级未达最低档", "分站 #" + strconv.Itoa(siteID) + " 开站以来客户累计净充值本金未达到任一已启用等级的门槛，仅预警，未关站。周期 " + periodID + "。"
		}
		return "Site grade below the lowest configured level", "Site #" + strconv.Itoa(siteID) + " does not meet the cumulative net customer top-up principal threshold of any enabled grade. The site was not closed. Period " + periodID + "."
	})
}

// EvaluateDueAdtySiteTiers runs one auto-evaluation period for sites that are
// not manually pinned. Re-runs of the same period are idempotent.
func EvaluateDueAdtySiteTiers(now int64) error {
	periodID := AdtySiteTierPeriodID(now)
	var ids []int
	if err := model.DB.Model(&model.AdtySite{}).
		Where("status IN ?", []string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused, model.AdtySiteStatusClosing}).
		Where("tier_mode IS NULL OR tier_mode = ? OR tier_mode = ?", "", AdtySiteTierModeAuto).
		Pluck("id", &ids).Error; err != nil {
		return err
	}
	var first error
	for _, id := range ids {
		if _, err := EvaluateAdtySiteTierAt(id, now, periodID); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func startAdtySiteTierEvaluationTask() {
	go func() {
		run := func() {
			if err := EvaluateDueAdtySiteTiers(time.Now().Unix()); err != nil {
				common.SysError(common.LogText("evaluate site tiers: %s", err.Error()))
			}
		}
		run()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			run()
		}
	}()
}

func init() {
	adtyMasterTasks = append(adtyMasterTasks, startAdtySiteTierEvaluationTask)
}

func SetAdtySiteTier(siteID int, code, mode string, now int64) (*model.AdtySite, error) {
	mode = strings.TrimSpace(mode)
	code = strings.TrimSpace(code)
	if mode != AdtySiteTierModeAuto && mode != AdtySiteTierModeManual {
		return nil, ErrAdtySiteTierMode
	}
	if mode == AdtySiteTierModeManual && code == "" {
		return nil, ErrAdtySiteTierNotFound
	}
	var site model.AdtySite
	costChanged := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteID).First(&site).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		oldFactor, oldHasFactor := AdtySiteCostFactor(&site)
		// Read the definition after acquiring the site lock so a concurrent
		// Root configuration update cannot be overwritten with an old copy.
		var selected *model.AdtySiteTier
		if code != "" {
			selected = &model.AdtySiteTier{}
			if err := tx.Where("code = ? AND enabled = ?", code, true).First(selected).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAdtySiteTierNotFound
				}
				return err
			}
			if !selected.CostFactor.IsPositive() {
				return ErrAdtySiteTierInvalid
			}
		} else if mode == AdtySiteTierModeAuto {
			stats, err := loadAdtySiteTierStatsTx(tx, siteID)
			if err != nil {
				return err
			}
			var tiers []model.AdtySiteTier
			if err := tx.Where("enabled = ?", true).Find(&tiers).Error; err != nil {
				return err
			}
			selected = SelectAdtySiteTier(tiers, stats)
			if selected == nil {
				selected = lowestEnabledAdtySiteTier(tiers)
			}
			if selected != nil {
				code = selected.Code
			}
		}
		if selected == nil || !selected.CostFactor.IsPositive() {
			return common.NewMessage("Keep at least one enabled site level with a positive supply discount while reseller sites are active")
		}
		if err := ensureAdtySiteWalletsTx(tx, siteID); err != nil {
			return err
		}
		updates := map[string]any{
			"tier_code": code, "tier_mode": mode, "tier_updated_at": now,
			"tier_cost_factor": selected.CostFactor,
			"pending_tier_cost_factor": nil, "pending_tier_cost_factor_at": 0,
		}
		if err := tx.Model(&model.AdtySiteWallet{}).Where("site_id = ? AND kind = ?", siteID, model.AdtySiteWalletMargin).
			Update("required_balance", selected.DepositUSD).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.AdtySite{}).Where("id = ?", siteID).Updates(updates).Error; err != nil {
			return err
		}
		site = model.AdtySite{}
		if err := tx.Where("id = ?", siteID).First(&site).Error; err != nil {
			return err
		}
		newFactor, newHasFactor := AdtySiteCostFactor(&site)
		costChanged = oldHasFactor != newHasFactor || !newFactor.Equal(oldFactor)
		return nil
	})
	if err != nil {
		return nil, err
	}
	InvalidateAdtySiteCache()
	if costChanged && AdtyOnSiteCostFactorChanged != nil {
		AdtyOnSiteCostFactorChanged(&site)
	}
	return &site, nil
}
