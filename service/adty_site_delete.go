package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// DeleteAdtySiteWithAudit handles R-32 (#1) Root-only site deletion:
//   - Zero-data sites (no customers, orders, teams, ledgers, earnings, withdrawals,
//     vouchers, tickets, commissions, or non-zero wallets): hard-deleted atomically
//     after reserving the site and its domain automation leases, cleaning up any
//     provisioned Cloudflare/AliDNS/Turnstile resources so the subdomain can be
//     safely reused, and removing/revoking any standalone empty owner.
//   - Sites with existing business or financial records: must be closed & migrated
//     first; once closed, soft-archived/hidden from the active site list while
//     preserving accounting ledgers, audit trails, and closed-site redirects.
func DeleteAdtySiteWithAudit(siteID, adminID int, reason string) (mode string, eventID string, err error) {
	if siteID <= 0 || adminID <= 0 {
		return "", "", common.NewMessage("Invalid parameters")
	}
	// Phase 1: Atomically lock the site, check whether it is zero-data, pause
	// customer traffic, and acquire exclusive hard-delete leases on all its domains
	// so concurrent registrations/orders or domain automation workers cannot race
	// with external cloud resource cleanup (TOCTOU guard).
	leaseToken := fmt.Sprintf("hard_delete:%d:%d", siteID, time.Now().UnixNano())
	var preHasData bool
	var prevStatus string
	var statusPausedForDelete bool
	var leasedDomainIDs []int
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var site model.AdtySite
		if err := model.AdtyLockForUpdate(tx).Where("id = ?", siteID).First(&site).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdtySiteNotFound
			}
			return err
		}
		hasData, checkErr := adtySiteHasBusinessDataTx(tx, siteID)
		if checkErr != nil {
			return checkErr
		}
		preHasData = hasData
		if hasData {
			return nil
		}
		now := time.Now().Unix()
		if tx.Migrator().HasTable(&model.AdtySiteDomain{}) {
			var domains []model.AdtySiteDomain
			if err := model.AdtyLockForUpdate(tx).Where("site_id = ?", siteID).Order("id ASC").Find(&domains).Error; err != nil {
				return err
			}
			for _, dom := range domains {
				if dom.LeaseUntil > now && dom.LeaseToken != "" {
					return common.NewMessage("Domain automation is in progress for this site; please try again shortly")
				}
				leasedDomainIDs = append(leasedDomainIDs, dom.Id)
			}
			if len(leasedDomainIDs) > 0 {
				if err := tx.Model(&model.AdtySiteDomain{}).Where("id IN ?", leasedDomainIDs).Updates(map[string]any{
					"lease_token": leaseToken,
					"lease_until": now + 180,
					"generation":  gorm.Expr("generation + 1"),
				}).Error; err != nil {
					return err
				}
			}
		}
		prevStatus = site.Status
		if site.Status != model.AdtySiteStatusPaused && site.Status != model.AdtySiteStatusClosed && site.Status != model.AdtySiteStatusClosing {
			if err := tx.Model(&model.AdtySite{}).Where("id = ?", siteID).Update("status", model.AdtySiteStatusPaused).Error; err != nil {
				return err
			}
			statusPausedForDelete = true
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	releaseDeleteReservation := func() {
		_ = model.DB.Transaction(func(tx *gorm.DB) error {
			if statusPausedForDelete && prevStatus != "" {
				_ = tx.Model(&model.AdtySite{}).Where("id = ? AND status = ?", siteID, model.AdtySiteStatusPaused).Update("status", prevStatus).Error
			}
			if len(leasedDomainIDs) > 0 && tx.Migrator().HasTable(&model.AdtySiteDomain{}) {
				_ = tx.Model(&model.AdtySiteDomain{}).Where("id IN ? AND lease_token = ?", leasedDomainIDs, leaseToken).Updates(map[string]any{
					"lease_token": "",
					"lease_until": 0,
				}).Error
			}
			return nil
		})
		if statusPausedForDelete {
			InvalidateAdtySiteCache()
		}
	}
	if !preHasData {
		if statusPausedForDelete {
			InvalidateAdtySiteCache()
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		cleanupErr := cleanupAdtySiteCloudDomainsForHardDelete(cleanupCtx, siteID)
		cancel()
		if cleanupErr != nil {
			releaseDeleteReservation()
			return "", "", cleanupErr
		}
	}

	var auditEvent *model.AuditLog
	deletedOwnerID := 0
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
			"owner_user_id": site.OwnerUserId, "status": prevStatus,
		}
		if oldSnapshot["status"] == "" {
			oldSnapshot["status"] = site.Status
		}
		var domainIDs []int
		if tx.Migrator().HasTable(&model.AdtySiteDomain{}) {
			if err := tx.Model(&model.AdtySiteDomain{}).Where("site_id = ?", siteID).Pluck("id", &domainIDs).Error; err != nil {
				return err
			}
		}
		if len(domainIDs) > 0 && tx.Migrator().HasTable(&model.AdtyDomainCloudResource{}) {
			if err := tx.Where("domain_id IN ?", domainIDs).Delete(&model.AdtyDomainCloudResource{}).Error; err != nil {
				return err
			}
		}
		for _, tableModel := range []any{
			&model.AdtySiteWallet{},
			&model.AdtySiteDomain{},
			&model.AdtySitePrice{},
			&model.AdtySitePriceScope{},
			&model.AdtySiteReferralRate{},
			&model.AdtySiteReferralAffiliate{},
			&model.AdtySiteStaff{},
			&model.AdtySiteMailConfig{},
			&model.AdtySiteSenderDomain{},
			&model.AdtySenderDomainCleanup{},
			&model.AdtySiteTierEvaluation{},
			&model.AdtySiteOwnership{},
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
			deleted, ownerErr := maybeDeleteEmptyStandaloneOwnerTx(tx, site.OwnerUserId)
			if ownerErr != nil {
				return ownerErr
			}
			if deleted {
				deletedOwnerID = site.OwnerUserId
			}
		}
		mode = "hard_deleted"
		auditEvent, err = recordSiteAuditWithIntentTx(tx, siteID, "delete", adminID, oldSnapshot, map[string]any{"deleted": true}, reason)
		return err
	})
	if err != nil {
		if !preHasData {
			releaseDeleteReservation()
		}
		return "", "", err
	}
	if deletedOwnerID > 0 {
		model.AdtyInvalidateUserCaches([]int{deletedOwnerID})
		_, _ = model.RevokeAllUserSessions(deletedOwnerID, "site_deleted")
		model.InvalidateUserTokensCache(deletedOwnerID)
	}
	if auditEvent != nil {
		eventID = auditEvent.EventId
		deliverAdtyCommittedAudit(*auditEvent)
	}
	InvalidateAdtySiteCache()
	return mode, eventID, nil
}

func cleanupAdtySiteCloudDomainsForHardDelete(ctx context.Context, siteID int) error {
	if model.DB == nil || !model.DB.Migrator().HasTable(&model.AdtySiteDomain{}) {
		return nil
	}
	var domains []model.AdtySiteDomain
	if err := model.DB.WithContext(ctx).Where("site_id = ?", siteID).Order("id ASC").Find(&domains).Error; err != nil {
		return err
	}
	if len(domains) == 0 {
		return nil
	}
	cfg := resellerSetting()
	for i := range domains {
		row := &domains[i]
		zone := strings.TrimSpace(row.ProviderZone)
		if zone == "" {
			zone = strings.TrimSpace(cfg.CloudflareZoneId)
		}
		hostnameID := strings.TrimSpace(row.CFHostnameId)
		createAttempted := row.CFCreateStartedAt > 0
		if strings.HasPrefix(hostnameID, adtyCloudResourcePendingPrefix) {
			hostnameID = ""
			createAttempted = true
		}
		if hostnameID == "" && model.DB.Migrator().HasTable(&model.AdtyDomainCloudResource{}) {
			foundID, lookupAttempted, err := adtyLookupCloudResourceState(model.DB, zone, row)
			if err != nil {
				return err
			}
			hostnameID = foundID
			createAttempted = createAttempted || lookupAttempted
		}
		if hostnameID == "" && createAttempted && zone != "" && cfg.CloudflareConfigured() {
			candidate, err := AdtyCloudflare.findCustomHostname(ctx, zone, row.Hostname)
			if err != nil {
				return adtyDomainAutomationUserError(err)
			}
			if candidate != nil {
				hostnameID = candidate.Id
			}
		}
		if hostnameID != "" {
			if zone == "" || !cfg.CloudflareConfigured() {
				return common.NewMessage("Cloudflare is not configured; cannot clean up provisioned domain resources before hard delete")
			}
			if err := AdtyCloudflare.deleteCustomHostname(ctx, zone, hostnameID); err != nil {
				return adtyDomainAutomationUserError(err)
			}
			_ = model.DB.Model(&model.AdtySiteDomain{}).Where("id = ?", row.Id).Update("cf_hostname_id", "").Error
			if model.DB.Migrator().HasTable(&model.AdtyDomainCloudResource{}) {
				_ = model.DB.Where("domain_id = ?", row.Id).Delete(&model.AdtyDomainCloudResource{}).Error
			}
		}
		if strings.TrimSpace(row.DNSRecords) != "" {
			var records []AdtyDNSRecord
			if err := common.Unmarshal([]byte(row.DNSRecords), &records); err != nil {
				return common.NewMessage("Stored DNS resource list is invalid")
			}
			dnsRoot := strings.TrimSpace(row.DNSRoot)
			if dnsRoot == "" {
				dnsRoot = strings.TrimSpace(cfg.AliyunDomain)
			}
			for len(records) > 0 {
				rec := records[0]
				if rec.ID == "" && dnsRoot != "" {
					found, err := AdtyAliDNS().Find(ctx, dnsRoot, rec)
					if err != nil {
						return adtyDomainAutomationUserError(err)
					}
					if found != nil {
						rec = *found
					}
				}
				if rec.ID != "" {
					if err := AdtyAliDNS().Delete(ctx, dnsRoot, rec); err != nil {
						return adtyDomainAutomationUserError(err)
					}
				}
				records = records[1:]
				encoded, _ := common.Marshal(records)
				_ = model.DB.Model(&model.AdtySiteDomain{}).Where("id = ?", row.Id).Update("dns_records", string(encoded)).Error
			}
		}
		if row.TurnstileWidget != "" {
			if err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := adtyTurnstileRemoveDomain(ctx, tx, row.TurnstileWidget, row.Hostname); err != nil {
					return err
				}
				return tx.Model(&model.AdtySiteDomain{}).Where("id = ?", row.Id).Update("turnstile_widget", "").Error
			}); err != nil {
				return adtyDomainAutomationUserError(err)
			}
		}
	}
	return nil
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
		{&model.AdtyManualRefund{}, "site_id = ?", []any{siteID}},
		{&model.AdtyFundBatch{}, "site_id = ?", []any{siteID}},
		{&model.AdtyTeam{}, "site_id = ? OR source_site_id = ?", []any{siteID, siteID}},
		{&model.AdtySiteWalletLedger{}, "site_id = ?", []any{siteID}},
		{&model.AdtySiteWallet{}, "site_id = ? AND (balance <> 0 OR frozen_usd <> 0)", []any{siteID}},
		{&model.AdtySiteWithdrawal{}, "site_id = ?", []any{siteID}},
		{&model.AdtySiteEarningDaily{}, "site_id = ?", []any{siteID}},
		{&model.AdtySiteEarningLine{}, "site_id = ?", []any{siteID}},
		{&model.AdtySiteSettlementSheet{}, "site_id = ?", []any{siteID}},
		{&model.AdtySiteTrialGrant{}, "site_id = ?", []any{siteID}},
		{&model.AdtyVoucher{}, "source_site_id = ? OR target_site_id = ?", []any{siteID, siteID}},
		{&model.AdtyTicket{}, "site_id = ? OR handling_site_id = ?", []any{siteID, siteID}},
		{&model.AdtySiteInviteCommission{}, "site_id = ?", []any{siteID}},
		{&model.AdtySiteInviteWithdrawal{}, "site_id = ?", []any{siteID}},
		{&model.AdtySiteTierPrincipalAdjustment{}, "site_id = ?", []any{siteID}},
		{&model.AdtyFinancialAdjustment{}, "site_id = ?", []any{siteID}},
		{&model.AdtyGiftFulfillment{}, "site_id = ?", []any{siteID}},
		{&model.AdtyOrderPaymentEvent{}, "site_id = ?", []any{siteID}},
		{&model.AdtySiteEarningEvent{}, "site_id = ?", []any{siteID}},
		{&model.AdtyCommissionRecovery{}, "site_id = ?", []any{siteID}},
		{&model.AdtyAnomalyDisposition{}, "site_id = ?", []any{siteID}},
		{&model.AdtyStaffBalanceRequest{}, "site_id = ?", []any{siteID}},
		{&model.AdtyPriceRevision{}, "site_id = ?", []any{siteID}},
		{&model.AdtySiteCloseMailBatch{}, "site_id = ?", []any{siteID}},
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

func maybeDeleteEmptyStandaloneOwnerTx(tx *gorm.DB, userID int) (bool, error) {
	var user model.User
	if err := tx.Select("id", "role", "quota", "used_quota", "request_count").First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if user.Role >= common.RoleAdminUser || user.Quota != 0 || user.UsedQuota != 0 || user.RequestCount != 0 {
		return false, nil
	}
	var otherSites int64
	if err := tx.Model(&model.AdtySite{}).Where("owner_user_id = ?", userID).Count(&otherSites).Error; err != nil || otherSites > 0 {
		return false, err
	}
	var orders int64
	if tx.Migrator().HasTable(&model.AdtyTopupOrder{}) {
		if err := tx.Model(&model.AdtyTopupOrder{}).Where("user_id = ?", userID).Count(&orders).Error; err != nil || orders > 0 {
			return false, err
		}
	}
	var tokens int64
	if tx.Migrator().HasTable(&model.Token{}) {
		if err := tx.Model(&model.Token{}).Where("user_id = ?", userID).Count(&tokens).Error; err != nil || tokens > 0 {
			return false, err
		}
	}
	if _, err := model.IncrementUserAuthVersionWithTx(tx, userID); err != nil {
		return false, err
	}
	if err := tx.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"status":                  common.UserStatusDisabled,
		"access_token":            nil,
		"access_token_created_at": nil,
	}).Error; err != nil {
		return false, err
	}
	if tx.Migrator().HasTable(&model.UserAccessToken{}) {
		if err := tx.Where("user_id = ?", userID).Delete(&model.UserAccessToken{}).Error; err != nil {
			return false, err
		}
	}
	if err := tx.Delete(&model.User{}, userID).Error; err != nil {
		return false, err
	}
	return true, nil
}
