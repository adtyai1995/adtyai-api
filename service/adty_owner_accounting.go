package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const ownerTransfer = "owner_self_use_transfer"

var ErrAdtyOwnerWalletReview = common.NewMessage("Contact the platform to reconcile this historical site account")

// Pooled balances and before/after ledger snapshots have no personal owner.
func ReviewAdtyPooledSiteFinance(tx *gorm.DB, siteID int) error {
	var count int64
	if err := tx.Model(&model.AdtySiteOwnership{}).Where("site_id = ?", siteID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrAdtyOwnerWalletReview
	}
	return nil
}

// Rebuild personal balances from original settlements, withdrawals, transfers,
// orders and voucher issuers. An unexplained movement or opening balance needs
// platform reconciliation before either owner can spend the shared wallet.
func ownerEarningsWalletTx(tx *gorm.DB, siteID, ownerID int, wallet *model.AdtySiteWallet) (decimal.Decimal, decimal.Decimal, error) {
	if err := ReviewAdtyPooledSiteFinance(tx, siteID); err != ErrAdtyOwnerWalletReview {
		return wallet.Balance.Sub(wallet.FrozenUSD), wallet.FrozenUSD, err
	}
	first, err := model.ResolveAdtySiteFinancialOwnerTx(tx, siteID, 0)
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	var rows []struct {
		OwnerID int
		Balance decimal.Decimal
		Frozen  decimal.Decimal
	}
	err = tx.Raw(`
        SELECT COALESCE(owner_id,-1) AS owner_id, SUM(balance) AS balance, SUM(frozen) AS frozen
        FROM (
            SELECT COALESCE(NULLIF(site_owner_user_id,0),?) AS owner_id, earning AS balance, 0 AS frozen
            FROM adty_site_earning_daily
            WHERE site_id = ? AND status = ? AND kind IN ?
            UNION ALL
            SELECT COALESCE(NULLIF(owner_user_id,0),?), CASE WHEN status = ? THEN -amount_usd ELSE 0 END,
                CASE WHEN status IN ? THEN amount_usd ELSE 0 END
            FROM adty_site_withdrawals WHERE site_id = ?
            UNION ALL
            SELECT CASE
                WHEN l.type IN ? THEN NULLIF(l.operator_id,0)
                WHEN l.ref_type IN ('topup','site_invite') AND o.id IS NOT NULL THEN COALESCE(NULLIF(o.site_owner_user_id,0),?)
                WHEN v.id IS NOT NULL THEN NULLIF(v.issuer_id,0)
                END, l.delta, l.hold_delta
            FROM adty_site_wallet_ledgers l
            LEFT JOIN adty_topup_orders o ON l.ref_type IN ('topup','site_invite') AND o.trade_no = l.ref_id AND o.site_id = l.site_id
            LEFT JOIN adty_vouchers v ON l.ref_type = 'voucher' AND v.source_site_id = l.site_id
                AND l.idempotency_key IN (CONCAT('voucher-freeze:',v.code_hash),CONCAT('voucher-release:',v.code_hash),CONCAT('voucher-spend:',v.code_hash))
            WHERE l.site_id = ? AND l.wallet_kind = ? AND l.type NOT IN ?
        ) AS facts GROUP BY owner_id`,
		first, siteID, model.AdtySiteEarningAvailable, []string{model.AdtySiteEarningUsage, model.AdtySiteEarningRefund},
		first, model.AdtySiteWithdrawPaid, []string{model.AdtySiteWithdrawPending, model.AdtySiteWithdrawPaying}, siteID,
		[]string{ownerTransfer, "earning_to_margin", "owner_customer_balance", "owner_team_balance", "staff_balance", "financial_adjustment"}, first, siteID, model.AdtySiteWalletEarnings,
		[]string{"site_earning_settlement", "earning_withdraw_freeze", "earning_withdraw_release", "earning_withdraw_paid"}).Scan(&rows).Error
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	var total, held, available, frozen decimal.Decimal
	for _, row := range rows {
		if row.OwnerID <= 0 || row.Frozen.IsNegative() {
			return decimal.Zero, decimal.Zero, ErrAdtyOwnerWalletReview
		}
		total, held = total.Add(row.Balance), held.Add(row.Frozen)
		if row.OwnerID == ownerID {
			available, frozen = row.Balance.Sub(row.Frozen), row.Frozen
		}
	}
	if !total.Equal(wallet.Balance) || !held.Equal(wallet.FrozenUSD) {
		return decimal.Zero, decimal.Zero, ErrAdtyOwnerWalletReview
	}
	return available, frozen, nil
}
