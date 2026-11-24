package service

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// DeleteAdtySiteWithAudit handles R-32 (#1) Root-only site deletion:
//   - Zero-data sites (no customers, orders, teams, ledgers, earnings, withdrawals,
//     vouchers, tickets, commissions, or non-zero wallets): hard-deleted atomically
//     with subdomain released and standalone empty owner disabled/removed.
//   - Sites with existing business or financial records: must be closed & migrated
//     first; once closed, soft-archived/hidden from the active site list while
//     preserving accounting ledgers, audit trails, and closed-site redirects.
func DeleteAdtySiteWithAudit(siteID, adminID int, reason string) (mode string, eventID string, err error) {
	if siteID <= 0 || adminID <= 0 {
		return "", "", common.NewMessage("Invalid parameters")
	}
	var auditEvent *model.AuditLog
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteID).First(&site).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		hasData, err := adtySiteHasBusinessDataTx(tx, siteID)
		if err != nil {
			return err
		}
		if hasData {
			if site.Status != model.AdtySiteStatusClosed {
				return common.NewMessage("This site has customers or financial records. Close and migrate the site first before removing it")
			}
			now := time.Now().Unix()
			if site.ArchivedHiddenAt == 0 {
				if err := tx.Model(&model.AdtySite{}).Where("id = ?", siteID).Update("archived_hidden_at", now).Error; err != nil {
					return err
				}
			}
			mode = "archived_hidden"
			auditEvent, err = recordSiteAuditWithIntentTx(tx, siteID, "archive_hide", adminID,
				map[string]any{"status": site.Status, "archived_hidden_at": site.ArchivedHiddenAt},
				map[string]any{"archived_hidden_at": now}, reason)
			return err
		}

		// Zero business/financial data: safe hard delete.
		oldSnapshot := map[string]any{
			"id": site.Id, "subdomain": site.Subdomain, "root_domain": site.RootDomain,
			"owner_user_id": site.OwnerUserId, "status": site.Status,
		}
		for _, tableModel := range []any{
			&model.AdtySiteWallet{},
			&model.AdtySiteDomain{},
			&model.AdtySitePrice{},
			&model.AdtySitePriceScope{},
			&model.AdtySiteReferralRate{},
			&model.AdtySiteStaff{},
		} {
			if tx.Migrator().HasTable(tableModel) {
				if err := tx.Where("site_id = ?", siteID).Delete(tableModel).Error; err != nil {
					return err
				}
			}
		}
		if err := tx.Delete(&model.AdtySite{}, siteID).Error; err != nil {
			return err
		}
		// Clean up standalone owner user if they have no other sites, balance, tokens, or orders.
		if site.OwnerUserId > 0 {
			_ = maybeDeleteEmptyStandaloneOwnerTx(tx, site.OwnerUserId)
		}
		mode = "hard_deleted"
		auditEvent, err = recordSiteAuditWithIntentTx(tx, siteID, "delete", adminID, oldSnapshot, map[string]any{"deleted": true}, reason)
		return err
	})
	if err != nil {
		return "", "", err
	}
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	InvalidateAdtySiteCache()
	return mode, eventID, nil
}

func adtySiteHasBusinessDataTx(tx *gorm.DB, siteID int) (bool, error) {
	checks := []struct {
		model any
		where string
		args  []any
	}{
		{&model.User{}, "site_id = ? OR inherited_from_site_id = ? OR staff_site_id = ?", []any{siteID, siteID, siteID}},
		{&model.AdtySiteCustomer{}, "site_id = ?", []any{siteID}},
		{&model.AdtyTopupOrder{}, "site_id = ?", []any{siteID}},
		{&model.AdtyTeam{}, "site_id = ? OR source_site_id = ?", []any{siteID, siteID}},
		{&model.AdtySiteWalletLedger{}, "site_id = ?", []any{siteID}},
		{&model.AdtySiteWallet{}, "site_id = ? AND (balance <> 0 OR frozen_usd <> 0)", []any{siteID}},
		{&model.AdtySiteWithdrawal{}, "site_id = ?", []any{siteID}},
		{&model.AdtySiteEarningDaily{}, "site_id = ?", []any{siteID}},
		{&model.AdtySiteEarningLine{}, "site_id = ?", []any{siteID}},
		{&model.AdtyVoucher{}, "source_site_id = ? OR target_site_id = ?", []any{siteID, siteID}},
		{&model.AdtyTicket{}, "site_id = ? OR handling_site_id = ?", []any{siteID, siteID}},
		{&model.AdtySiteInviteCommission{}, "site_id = ?", []any{siteID}},
		{&model.AdtySiteInviteWithdrawal{}, "site_id = ?", []any{siteID}},
	}
	for _, chk := range checks {
		if !tx.Migrator().HasTable(chk.model) {
			continue
		}
		var n int64
		if err := tx.Model(chk.model).Where(chk.where, chk.args...).Limit(1).Count(&n).Error; err != nil {
			return false, err
		}
		if n > 0 {
			return true, nil
		}
	}
	return false, nil
}

func maybeDeleteEmptyStandaloneOwnerTx(tx *gorm.DB, userID int) error {
	var user model.User
	if err := tx.Select("id", "role", "quota", "used_quota", "request_count").First(&user, userID).Error; err != nil {
		return err
	}
	if user.Role >= common.RoleAdminUser || user.Quota != 0 || user.UsedQuota != 0 || user.RequestCount != 0 {
		return nil
	}
	var otherSites int64
	if err := tx.Model(&model.AdtySite{}).Where("owner_user_id = ?", userID).Count(&otherSites).Error; err != nil || otherSites > 0 {
		return err
	}
	var orders int64
	if tx.Migrator().HasTable(&model.AdtyTopupOrder{}) {
		if err := tx.Model(&model.AdtyTopupOrder{}).Where("user_id = ?", userID).Count(&orders).Error; err != nil || orders > 0 {
			return err
		}
	}
	var tokens int64
	if tx.Migrator().HasTable(&model.Token{}) {
		if err := tx.Model(&model.Token{}).Where("user_id = ?", userID).Count(&tokens).Error; err != nil || tokens > 0 {
			return err
		}
	}
	return tx.Delete(&model.User{}, userID).Error
}
