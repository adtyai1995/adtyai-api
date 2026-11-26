package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
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
	// Phase 1: Atomically lock the site, check whether it is zero-data, transition
	// zero-data sites into the dedicated deleting state (blocking all traffic,
	// registrations, and writes), and acquire exclusive hard-delete leases on all
	// domains so external cloud cleanup cannot race with business writes (R3-04).
	leaseToken := fmt.Sprintf("hard_delete:%d:%d", siteID, time.Now().UnixNano())
	var preHasData bool
	var prevStatus string
	var transitionedToDeleting bool
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
			if err := model.AdtyLockForUpdate(tx).Where("site_id = ? AND status <> ?", siteID, model.AdtySiteDomainRemoved).Order("id ASC").Find(&domains).Error; err != nil {
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
					"desired_state": "removed",
					"lease_token":   leaseToken,
					"lease_until":   now + 180,
					"generation":    gorm.Expr("generation + 1"),
				}).Error; err != nil {
					return err
				}
			}
		}
		prevStatus = site.Status
		if site.Status != model.AdtySiteStatusDeleting {
			if err := tx.Model(&model.AdtySite{}).Where("id = ?", siteID).Update("status", model.AdtySiteStatusDeleting).Error; err != nil {
				return err
			}
			transitionedToDeleting = true
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	releaseDeleteLease := func() {
		if len(leasedDomainIDs) == 0 {
			return
		}
		_ = model.DB.Transaction(func(tx *gorm.DB) error {
			if tx.Migrator().HasTable(&model.AdtySiteDomain{}) {
				_ = tx.Model(&model.AdtySiteDomain{}).Where("id IN ? AND lease_token = ?", leasedDomainIDs, leaseToken).Updates(map[string]any{
					"lease_token": "",
					"lease_until": 0,
				}).Error
			}
			return nil
		})
	}
	if !preHasData {
		if transitionedToDeleting {
			InvalidateAdtySiteCache()
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		cleanupErr := cleanupAdtySiteCloudDomainsForHardDelete(cleanupCtx, siteID)
		if cleanupErr == nil {
			cleanupErr = cleanupAdtySiteSenderDomainsForHardDelete(cleanupCtx, siteID)
		}
		cancel()
		if cleanupErr != nil {
			// Keep status=deleting so Root can safely retry hard delete without
			// ever restoring a site whose cloud resources were partially torn down.
			releaseDeleteLease()
			return "", "", cleanupErr
		}
	}

	var auditEvent *model.AuditLog
	preservedOwnerID := 0
	cleanedOptions := map[string]string{}
	commissionMarginKey := commissionMarginOptionKey(siteID)
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		// R8-01: Pre-lock per-site option keys in sorted order BEFORE
		// adty_payment_scene_configs -> adty_sites so all transactions follow the
		// canonical DAG lock order: options (key ASC) -> adty_payment_scene_configs -> adty_sites -> adty_epay_channels.
		lockedOptVals := map[string]string{}
		lockedOptFound := map[string]bool{}
		if tx.Migrator().HasTable(&model.Option{}) {
			optKeys := []string{
				commissionMarginKey,
				adty_setting.TopupSettingKeySiteBonusMarginFill,
				adty_setting.TopupSettingKeySiteMethodBonus,
			}
			slices.Sort(optKeys)
			for _, key := range optKeys {
				raw, ok, readErr := model.ReadOptionForUpdate(tx, key)
				if readErr != nil {
					return readErr
				}
				lockedOptFound[key] = ok
				if ok {
					lockedOptVals[key] = raw
				}
			}
		}
		var sceneCfg *model.AdtyPaymentSceneConfig
		if tx.Migrator().HasTable(&model.AdtyPaymentSceneConfig{}) {
			var cfgErr error
			sceneCfg, cfgErr = model.GetAdtyPaymentSceneConfig(tx, true)
			if cfgErr != nil {
				return cfgErr
			}
		}
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
			&model.AdtySiteReferralAuditLog{},
			&model.AdtySiteStaff{},
			&model.AdtySiteMailConfig{},
			&model.AdtySiteSenderDomain{},
			&model.AdtySenderDomainCleanup{},
			&model.AdtySiteTierEvaluation{},
			&model.AdtySiteOwnership{},
			&model.AdtySettlementScan{},
			&model.AdtyTelegramBinding{},
			&model.AdtyTelegramChallenge{},
			&model.AdtyTelegramDelivery{},
			&AdtySystemEmailDelivery{},
			&model.AdtySiteLookup{},
			&model.AdtyNotificationRead{},
			&model.AdtyTranslationHead{},
		} {
			if tx.Migrator().HasTable(tableModel) {
				if err := tx.Where("site_id = ?", siteID).Delete(tableModel).Error; err != nil {
					return err
				}
			}
		}
		if tx.Migrator().HasTable(&model.AdtyNotificationPublication{}) {
			if err := tx.Where("issuer_site_id = ?", siteID).Delete(&model.AdtyNotificationPublication{}).Error; err != nil {
				return err
			}
		}
		// R7-28: Prune references to the hard-deleted site from payment scene configs
		// so future SaveAdtyPaymentScenes calls are never permanently blocked.
		if sceneCfg != nil && strings.TrimSpace(sceneCfg.Payload) != "" {
			var scenes []model.AdtyPaymentScene
			if err := common.UnmarshalJsonStr(sceneCfg.Payload, &scenes); err == nil {
				changed := false
				kept := make([]model.AdtyPaymentScene, 0, len(scenes))
				for _, sc := range scenes {
					if sc.SiteScope == "selected" && len(sc.SiteIDs) > 0 {
						filtered := make([]int, 0, len(sc.SiteIDs))
						for _, id := range sc.SiteIDs {
							if id == siteID {
								changed = true
								continue
							}
							filtered = append(filtered, id)
						}
						sc.SiteIDs = filtered
						if len(filtered) == 0 {
							continue
						}
					}
					kept = append(kept, sc)
				}
				if changed {
					payload, mErr := common.Marshal(kept)
					if mErr != nil {
						return mErr
					}
					if err := tx.Model(sceneCfg).Updates(map[string]any{
						"version":    sceneCfg.Version + 1,
						"payload":    string(payload),
						"updated_by": adminID,
						"updated_at": time.Now().Unix(),
					}).Error; err != nil {
						return err
					}
				}
			}
		}
		// Clean up per-site options so recycled or removed site IDs carry no stale policy.
		if lockedOptFound[commissionMarginKey] {
			if err := tx.Where(&model.Option{Key: commissionMarginKey}).Delete(&model.Option{}).Error; err != nil {
				return err
			}
			cleanedOptions[commissionMarginKey] = "0"
		}
		if rawFill, ok := lockedOptVals[adty_setting.TopupSettingKeySiteBonusMarginFill]; ok && strings.TrimSpace(rawFill) != "" {
			if mergedFill, mergeErr := adty_setting.MergeSiteBonusMarginFill(rawFill, siteID, false); mergeErr == nil && mergedFill != rawFill {
				if err := model.SaveOptionsTx(tx, map[string]string{adty_setting.TopupSettingKeySiteBonusMarginFill: mergedFill}); err != nil {
					return err
				}
				cleanedOptions[adty_setting.TopupSettingKeySiteBonusMarginFill] = mergedFill
			}
		}
		if rawMethod, ok := lockedOptVals[adty_setting.TopupSettingKeySiteMethodBonus]; ok && strings.TrimSpace(rawMethod) != "" {
			if mergedMethod, changed, rmErr := adty_setting.RemoveSiteMethodBonus(rawMethod, siteID); rmErr == nil && changed {
				if err := model.SaveOptionsTx(tx, map[string]string{adty_setting.TopupSettingKeySiteMethodBonus: mergedMethod}); err != nil {
					return err
				}
				cleanedOptions[adty_setting.TopupSettingKeySiteMethodBonus] = mergedMethod
			}
		}
		if err := tx.Delete(&model.AdtySite{}, siteID).Error; err != nil {
			return err
		}
		// R-41 #4: Always preserve the former owner account and restore them as a
		// regular main-site user when hard-deleting a zero-data subsite.
		if site.OwnerUserId > 0 {
			preservedOwnerID = site.OwnerUserId
		}
		mode = "hard_deleted"
		auditEvent, err = recordSiteAuditWithIntentTx(tx, siteID, "delete", adminID, oldSnapshot, map[string]any{"deleted": true}, reason)
		return err
	})
	if err != nil {
		if !preHasData {
			releaseDeleteLease()
		}
		return "", "", err
	}
	if len(cleanedOptions) > 0 {
		if err := model.ApplyOptionValues(cleanedOptions); err != nil {
			common.SysError(common.LogText("failed to sync cleaned site options after hard-deleting site %d: %v", siteID, err))
		}
	}
	if preservedOwnerID > 0 && common.RedisEnabled && common.RDB != nil {
		if err := model.InvalidateUserCache(preservedOwnerID); err != nil {
			common.SysError(common.LogText("failed to invalidate user cache after hard-deleting site %d for preserved owner %d: %v", siteID, preservedOwnerID, err))
		}
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
	if err := model.DB.WithContext(ctx).Where("site_id = ? AND status <> ?", siteID, model.AdtySiteDomainRemoved).Order("id ASC").Find(&domains).Error; err != nil {
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
		hadCFResource := hostnameID != "" || createAttempted
		if hostnameID == "" && createAttempted {
			if zone == "" || !cfg.CloudflareConfigured() {
				return common.NewMessage("Cloudflare is not configured; cannot clean up provisioned domain resources before hard delete")
			}
			candidate, err := AdtyCloudflare.findCustomHostname(ctx, zone, row.Hostname)
			if err != nil {
				return adtyDomainAutomationUserError(err)
			}
			if candidate != nil {
				if candidate.Metadata["site_id"] == strconv.Itoa(row.SiteId) &&
					candidate.Metadata["adty_domain_id"] == strconv.Itoa(row.Id) &&
					candidate.Metadata["adty_generation"] == adtyCustomHostnameGeneration(row) {
					hostnameID = candidate.Id
				} else {
					return common.NewMessage("Unrecorded Cloudflare creation requires reconciliation before cleanup")
				}
			}
		}
		if hostnameID != "" {
			if zone == "" || !cfg.CloudflareConfigured() {
				return common.NewMessage("Cloudflare is not configured; cannot clean up provisioned domain resources before hard delete")
			}
			if err := AdtyCloudflare.deleteCustomHostname(ctx, zone, hostnameID); err != nil {
				return adtyDomainAutomationUserError(err)
			}
			_ = model.DB.Model(&model.AdtySiteDomain{}).Where("id = ?", row.Id).Updates(map[string]any{
				"cf_hostname_id":       "",
				"cf_create_started_at": 0,
			}).Error
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
			if len(records) > 0 && dnsRoot == "" {
				return common.NewMessage("AliDNS root domain is not configured; cannot clean up DNS resources before hard delete")
			}
			for len(records) > 0 {
				rec := records[0]
				if rec.ID == "" {
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
		if row.TurnstileWidget != "" || (row.ManagedBy != "platform" && (hadCFResource || row.Status == model.AdtySiteDomainActive) && cfg.TurnstileConfigured()) {
			if err := cleanupAdtySiteDomainTurnstileForHardDelete(ctx, row.Id); err != nil {
				return adtyDomainAutomationUserError(err)
			}
		}
	}
	return nil
}

func cleanupAdtySiteDomainTurnstileForHardDelete(ctx context.Context, domainID int) error {
	unlock := adtyDomainLocks.Lock(domainID)
	defer unlock()

	var widgetID, hostname string
	found := false
	if err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current model.AdtySiteDomain
		if err := adtyLockDomainRow(tx, domainID, &current); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		widgetID = current.TurnstileWidget
		hostname = current.Hostname
		found = true
		return nil
	}); err != nil {
		return err
	}
	if !found {
		return nil
	}
	// External Turnstile API call runs OUTSIDE any DB transaction (R4-16).
	if err := adtyTurnstileRemoveDomain(ctx, widgetID, hostname); err != nil {
		return err
	}
	return model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current model.AdtySiteDomain
		if err := adtyLockDomainRow(tx, domainID, &current); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		return tx.Model(&model.AdtySiteDomain{}).Where("id = ?", domainID).Update("turnstile_widget", "").Error
	})
}

func cleanupAdtySiteSenderDomainsForHardDelete(ctx context.Context, siteID int) error {
	if model.DB == nil {
		return nil
	}
	if model.DB.Migrator().HasTable(&model.AdtySiteSenderDomain{}) {
		var rows []model.AdtySiteSenderDomain
		if err := model.DB.WithContext(ctx).
			Where("site_id = ? AND status <> ?", siteID, model.AdtySenderDomainDisabled).
			Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if strings.TrimSpace(row.ResendDomainId) == "" {
				continue
			}
			if err := deleteSenderDomainIfPresent(ctx, row.ResendDomainId); err != nil {
				return err
			}
			_ = model.DB.WithContext(ctx).Model(&model.AdtySiteSenderDomain{}).
				Where("site_id = ? AND resend_domain_id = ?", siteID, row.ResendDomainId).
				Updates(map[string]any{
					"status":        model.AdtySenderDomainDisabled,
					"cleanup_error": "",
					"retry_at":      0,
				}).Error
		}
	}
	if model.DB.Migrator().HasTable(&model.AdtySenderDomainCleanup{}) {
		var jobs []model.AdtySenderDomainCleanup
		if err := model.DB.WithContext(ctx).Where("site_id = ?", siteID).Find(&jobs).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			if strings.TrimSpace(job.RemoteID) != "" {
				if err := deleteSenderDomainIfPresent(ctx, job.RemoteID); err != nil {
					return err
				}
			}
			_ = model.DB.WithContext(ctx).Where("remote_id = ? AND site_id = ?", job.RemoteID, siteID).
				Delete(&model.AdtySenderDomainCleanup{}).Error
		}
	}
	return nil
}

func adtySiteHasBusinessDataTx(tx *gorm.DB, siteID int) (bool, error) {
	checks := []struct {
		model    any
		where    string
		args     []any
		unscoped bool
	}{
		{&model.User{}, "site_id = ? OR inherited_from_site_id = ? OR staff_site_id = ?", []any{siteID, siteID, siteID}, true},
		{&model.AdtySiteCustomer{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtyTopupOrder{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtyManualRefund{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtyFundBatch{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtyTeam{}, "site_id = ? OR source_site_id = ?", []any{siteID, siteID}, false},
		{&model.AdtySiteWalletLedger{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtySiteWallet{}, "site_id = ? AND (balance <> 0 OR frozen_usd <> 0 OR excess_frozen_usd <> 0)", []any{siteID}, false},
		{&model.AdtySiteWithdrawal{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtySiteEarningDaily{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtySiteEarningLine{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtySiteEarningFact{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtySiteSettlementSheet{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtySiteTrialGrant{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtyVoucher{}, "source_site_id = ? OR target_site_id = ?", []any{siteID, siteID}, false},
		{&model.AdtyTicket{}, "site_id = ? OR handling_site_id = ?", []any{siteID, siteID}, false},
		{&model.AdtySiteInvite{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtySiteInviteCommission{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtySiteInviteWithdrawal{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtyCommissionCoverageCancellation{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtySiteTierPrincipalAdjustment{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtyFinancialAdjustment{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtyGiftFulfillment{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtyOrderPaymentEvent{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtySiteEarningEvent{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtyCommissionRecovery{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtyAnomalyDisposition{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtyStaffBalanceRequest{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtyPriceRevision{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtySiteCloseMailBatch{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtySiteReport{}, "site_id = ?", []any{siteID}, false},
		{&model.AdtyUserSiteMigration{}, "source_site_id = ? OR target_site_id = ?", []any{siteID, siteID}, false},
		{&model.BillingOperation{}, "site_id = ?", []any{siteID}, false},
		{&model.Token{}, "owner_self_use_site_id = ?", []any{siteID}, true},
	}
	for _, chk := range checks {
		if !tx.Migrator().HasTable(chk.model) {
			continue
		}
		var n int64
		q := tx
		if chk.unscoped {
			q = tx.Unscoped()
		}
		if err := q.Model(chk.model).Where(chk.where, chk.args...).Limit(1).Count(&n).Error; err != nil {
			return false, err
		}
		if n > 0 {
			return true, nil
		}
	}
	return false, nil
}
