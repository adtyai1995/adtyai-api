package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"gorm.io/gorm"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	adtyCloudflareZoneIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	errAdtyDomainLeaseLost      = errors.New("domain automation lease lost")
)

var AdtyAliDNS = func() *AdtyAliDNSClient {
	cfg := resellerSetting()
	return &AdtyAliDNSClient{AccessKeyID: cfg.AliyunAccessKeyID, AccessKeySecret: cfg.AliyunAccessKeySecret}
}

func adtyAllocatePlatformDomainsTx(tx *gorm.DB, site *model.AdtySite) error {
	cfg := resellerSetting()
	if !cfg.DomainAutomationEnabled {
		return nil
	}
	if site.RootDomain != cfg.AliyunDomain {
		return fmt.Errorf("Site root is outside the managed DNS domain")
	}
	for _, role := range []string{model.AdtySiteDomainRoleSite, model.AdtySiteDomainRoleBaseURL} {
		host := site.Subdomain + "." + site.RootDomain
		if role == model.AdtySiteDomainRoleBaseURL {
			host = fmt.Sprintf("api-s%d.%s", site.Id, site.RootDomain)
		}
		row := model.AdtySiteDomain{SiteId: site.Id, Hostname: host, Role: role, ManagedBy: "platform", DesiredState: "serve", Status: model.AdtySiteDomainPending, AutomationState: "queued", Generation: 1, ProviderZone: cfg.CloudflareZoneId, DNSRoot: cfg.AliyunDomain, DNSTarget: cfg.CnameTarget, MainlandTarget: cfg.DomainMainlandTarget, CreatedAt: time.Now().Unix()}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func AdtyDomainCloseAction(row model.AdtySiteDomain) string {
	// R5-046: Only already-active website domains transition to redirect_only on close.
	// Never-activated (pending/failed) website domains and all base_url domains are removed.
	if row.Role == model.AdtySiteDomainRoleSite && row.Status == model.AdtySiteDomainActive && row.DesiredState != "removed" {
		return "redirect_only"
	}
	return "removed"
}

func adtyCloseDomainIntentsTx(tx *gorm.DB, siteID int) error {
	if err := tx.Model(&model.AdtySiteDomain{}).
		Where("site_id = ? AND role = ? AND status = ? AND desired_state NOT IN ?", siteID, model.AdtySiteDomainRoleSite, model.AdtySiteDomainActive, []string{"redirect_only", "removed"}).
		Updates(map[string]any{"desired_state": "redirect_only", "automation_state": "queued", "next_run_at": 0, "generation": gorm.Expr("generation + 1")}).Error; err != nil {
		return err
	}
	if err := tx.Model(&model.AdtySiteDomain{}).
		Where("site_id = ? AND status <> ? AND desired_state <> ? AND NOT (role = ? AND status = ?)", siteID, model.AdtySiteDomainRemoved, "removed", model.AdtySiteDomainRoleSite, model.AdtySiteDomainActive).
		Updates(map[string]any{"desired_state": "removed", "automation_state": "queued", "next_run_at": 0, "generation": gorm.Expr("generation + 1")}).Error; err != nil {
		return err
	}
	return nil
}

// RunAdtyDomainAutomationOnce reconciles durable intent. Leases serialize cloud
// operations across instances without holding database transactions over HTTP.
func RunAdtyDomainAutomationOnce(ctx context.Context, now time.Time) error {
	started := time.Now()
	var rows []model.AdtySiteDomain
	nonDeletingSites := model.DB.Model(&model.AdtySite{}).Select("id").Where("status <> ?", model.AdtySiteStatusDeleting)
	if err := model.DB.WithContext(ctx).
		Where("automation_state <> ? AND status <> ? AND next_run_at <= ? AND lease_until <= ? AND site_id IN (?)",
			"", model.AdtySiteDomainRemoved, now.Unix(), now.Unix(), nonDeletingSites).
		Order("next_run_at, id").Limit(50).Find(&rows).Error; err != nil {
		return err
	}
	var failures []error
	for i := range rows {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		claimTime := now.Add(time.Since(started))
		row := &rows[i]
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		row.LeaseToken = hex.EncodeToString(nonce)
		claim := model.DB.Model(&model.AdtySiteDomain{}).Where("id = ? AND lease_until <= ? AND generation = ? AND next_run_at <= ? AND status <> ?", row.Id, claimTime.Unix(), row.Generation, claimTime.Unix(), model.AdtySiteDomainRemoved).Updates(map[string]any{"lease_token": row.LeaseToken, "lease_until": claimTime.Add(5 * time.Minute).Unix()})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			continue
		}
		var fresh model.AdtySiteDomain
		if err := model.DB.WithContext(ctx).
			Where("id = ? AND lease_token = ? AND generation = ?", row.Id, row.LeaseToken, row.Generation).
			First(&fresh).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		row = &fresh
		runctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err := adtyReconcileDomain(runctx, row)
		cancel()
		finished := now.Add(time.Since(started))
		updates := map[string]any{"lease_token": "", "lease_until": 0, "last_checked_at": finished.Unix(), "next_run_at": finished.Add(5 * time.Minute).Unix()}
		if err != nil {
			safeErr := adtyDomainAutomationUserError(err)
			updates["attempts"] = row.Attempts + 1
			updates["last_error"] = safeErr.Error()
			updates["automation_state"] = "retry"
			updates["next_run_at"] = finished.Add(time.Duration(min(3600, 30*(1<<min(row.Attempts, 7)))) * time.Second).Unix()
			failures = append(failures, fmt.Errorf("domain %d: %w", row.Id, err))
		} else {
			updates["attempts"] = 0
			updates["last_error"] = ""
		}
		// A close may have changed the intent during HTTP. Never overwrite its schedule.
		if err := model.DB.Model(&model.AdtySiteDomain{}).Where("id = ? AND lease_token = ? AND generation = ?", row.Id, row.LeaseToken, row.Generation).Updates(updates).Error; err != nil {
			return err
		}
		if err := model.DB.Model(&model.AdtySiteDomain{}).Where("id = ? AND lease_token = ? AND generation <> ?", row.Id, row.LeaseToken, row.Generation).Updates(map[string]any{"lease_token": "", "lease_until": 0, "next_run_at": 0}).Error; err != nil {
			return err
		}
	}
	if err := adtyBackfillActiveCustomBaseURLTurnstile(ctx, now.Add(time.Since(started))); err != nil {
		failures = append(failures, err)
	}
	InvalidateAdtySiteCache()
	return errors.Join(failures...)
}

func adtyBackfillActiveCustomBaseURLTurnstile(ctx context.Context, now time.Time) error {
	if !resellerSetting().TurnstileConfigured() {
		return nil
	}
	started := time.Now()
	var rows []model.AdtySiteDomain
	nonDeletingSites := model.DB.Model(&model.AdtySite{}).Select("id").Where("status <> ?", model.AdtySiteStatusDeleting)
	if err := model.DB.WithContext(ctx).
		Where("status = ? AND role = ? AND managed_by <> ? AND turnstile_widget = ? AND desired_state IN ? AND lease_until <= ? AND site_id IN (?)",
			model.AdtySiteDomainActive, model.AdtySiteDomainRoleBaseURL, "platform", "", []string{"", "serve"}, now.Unix(), nonDeletingSites).
		Order("id ASC").Limit(50).Find(&rows).Error; err != nil {
		return err
	}
	var failures []error
	for i := range rows {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		claimTime := now.Add(time.Since(started))
		row := &rows[i]
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		row.LeaseToken = hex.EncodeToString(nonce)
		claim := model.DB.Model(&model.AdtySiteDomain{}).
			Where("id = ? AND lease_until <= ? AND generation = ? AND status = ? AND turnstile_widget = ?",
				row.Id, claimTime.Unix(), row.Generation, model.AdtySiteDomainActive, "").
			Updates(map[string]any{"lease_token": row.LeaseToken, "lease_until": claimTime.Add(2 * time.Minute).Unix()})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			continue
		}
		runctx, cancel := context.WithTimeout(ctx, time.Minute)
		var widget string
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			var current model.AdtySiteDomain
			if err := adtyLockDomainRow(tx, row.Id, &current); err != nil {
				return err
			}
			if current.LeaseToken != row.LeaseToken || current.Generation != row.Generation {
				return errAdtyDomainLeaseLost
			}
			if current.Status != model.AdtySiteDomainActive ||
				current.Role != model.AdtySiteDomainRoleBaseURL ||
				current.ManagedBy == "platform" ||
				current.TurnstileWidget != "" ||
				(current.DesiredState != "" && current.DesiredState != "serve") {
				return nil
			}
			var site model.AdtySite
			if err := tx.Select("id", "status").First(&site, current.SiteId).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			if site.Status == model.AdtySiteStatusDeleting {
				return nil
			}
			var assignErr error
			widget, assignErr = adtyTurnstileAssign(runctx, tx, current.Hostname)
			if assignErr != nil {
				return assignErr
			}
			if widget != "" {
				res := tx.Model(&model.AdtySiteDomain{}).
					Where("id = ? AND lease_token = ? AND generation = ?", row.Id, row.LeaseToken, row.Generation).
					Update("turnstile_widget", widget)
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected != 1 {
					return errAdtyDomainLeaseLost
				}
				row.TurnstileWidget = widget
			}
			return nil
		})
		cancel()
		updates := map[string]any{"lease_token": "", "lease_until": 0}
		if err != nil {
			updates["last_error"] = adtyCloudflareUserError(err).Error()
			failures = append(failures, fmt.Errorf("domain %d turnstile backfill: %w", row.Id, err))
		} else if widget != "" {
			updates["last_error"] = ""
		}
		if err := model.DB.Model(&model.AdtySiteDomain{}).
			Where("id = ? AND lease_token = ? AND generation = ?", row.Id, row.LeaseToken, row.Generation).
			Updates(updates).Error; err != nil {
			return err
		}
		genChangedUpdates := map[string]any{"lease_token": "", "lease_until": 0, "next_run_at": 0}
		if err := model.DB.Model(&model.AdtySiteDomain{}).
			Where("id = ? AND lease_token = ? AND generation <> ?", row.Id, row.LeaseToken, row.Generation).
			Updates(genChangedUpdates).Error; err != nil {
			return err
		}
	}
	return errors.Join(failures...)
}

func adtySaveDomainResource(row *model.AdtySiteDomain, values map[string]any) error {
	result := model.DB.Model(&model.AdtySiteDomain{}).Where("id = ? AND lease_token = ?", row.Id, row.LeaseToken).Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("Domain worker lease was lost")
	}
	return nil
}

func adtyReconcileDomain(ctx context.Context, row *model.AdtySiteDomain) error {
	var site model.AdtySite
	if err := model.DB.WithContext(ctx).Select("id", "status").First(&site, row.SiteId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if site.Status == model.AdtySiteStatusDeleting {
		return nil
	}
	cfg := resellerSetting()
	if row.ProviderZone == "" {
		row.ProviderZone = cfg.CloudflareZoneId
		if err := adtySaveDomainResource(row, map[string]any{"provider_zone": row.ProviderZone}); err != nil {
			return err
		}
	}
	if row.ProviderZone != cfg.CloudflareZoneId {
		return common.NewMessage("Stored Cloudflare zone differs; restore credentials or explicitly migrate this domain")
	}
	if row.DNSRoot != "" && row.DNSRoot != cfg.AliyunDomain {
		return common.NewMessage("Stored DNS root differs; restore DNS configuration before continuing")
	}
	if row.DesiredState == "redirect_only" && row.TurnstileWidget != "" {
		if err := releaseRedirectWidget(ctx, row); err != nil {
			return err
		}
	}
	var records []AdtyDNSRecord
	if row.DNSRecords != "" {
		if err := common.Unmarshal([]byte(row.DNSRecords), &records); err != nil {
			return common.NewMessage("Stored DNS resource list is invalid")
		}
	}
	if row.DesiredState == "removed" {
		hostnameID := row.CFHostnameId
		createAttempted := row.CFCreateStartedAt > 0
		if strings.HasPrefix(hostnameID, adtyCloudResourcePendingPrefix) {
			hostnameID = ""
			createAttempted = true
		}
		if hostnameID == "" {
			var lookupAttempted bool
			var err error
			hostnameID, lookupAttempted, err = adtyLookupCloudResourceState(model.DB, row.ProviderZone, row)
			if err != nil {
				return err
			}
			createAttempted = createAttempted || lookupAttempted
		}
		if hostnameID == "" && createAttempted {
			candidate, err := AdtyCloudflare.findCustomHostname(ctx, row.ProviderZone, row.Hostname)
			if err != nil {
				return err
			}
			if candidate != nil {
				return common.NewMessage("Unrecorded Cloudflare creation requires reconciliation before cleanup")
			}
		}
		if hostnameID != "" {
			if err := AdtyCloudflare.deleteCustomHostname(ctx, row.ProviderZone, hostnameID); err != nil {
				return err
			}
		}
		if hostnameID != "" || createAttempted || row.CFHostnameId != "" {
			if err := model.DB.Where("domain_id = ? AND generation = ? AND zone = ?", row.Id, adtyCustomHostnameGeneration(row), row.ProviderZone).Delete(&model.AdtyDomainCloudResource{}).Error; err != nil {
				return err
			}
			if row.CFHostnameId != "" {
				row.CFHostnameId = ""
				if err := adtySaveDomainResource(row, map[string]any{"cf_hostname_id": ""}); err != nil {
					return err
				}
			}
		}
		for len(records) > 0 {
			if records[0].ID == "" {
				found, err := AdtyAliDNS().Find(ctx, row.DNSRoot, records[0])
				if err != nil {
					return err
				}
				if found != nil {
					records[0] = *found
				}
			}
			if records[0].ID != "" {
				if err := AdtyAliDNS().Delete(ctx, row.DNSRoot, records[0]); err != nil {
					return err
				}
			}
			records = records[1:]
			encoded, _ := common.Marshal(records)
			if err := adtySaveDomainResource(row, map[string]any{"dns_records": string(encoded)}); err != nil {
				return err
			}
		}
		return model.DB.Transaction(func(tx *gorm.DB) error {
			var current model.AdtySiteDomain
			if err := adtyLockDomainRow(tx, row.Id, &current); err != nil {
				return err
			}
			if current.LeaseToken != row.LeaseToken || current.Generation != row.Generation || current.DesiredState != "removed" {
				return errAdtyDomainLeaseLost
			}
			if current.Status == model.AdtySiteDomainRemoved {
				return nil
			}
			if current.TurnstileWidget != "" || (current.ManagedBy != "platform" && (hostnameID != "" || createAttempted || current.Status == model.AdtySiteDomainActive) && cfg.TurnstileConfigured()) {
				if err := adtyTurnstileRemoveDomain(ctx, tx, current.TurnstileWidget, current.Hostname); err != nil {
					return err
				}
			}
			res := tx.Model(&model.AdtySiteDomain{}).
				Where("id = ? AND lease_token = ? AND generation = ?", row.Id, row.LeaseToken, row.Generation).
				Updates(map[string]any{"status": model.AdtySiteDomainRemoved, "automation_state": "removed", "turnstile_widget": ""})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return errAdtyDomainLeaseLost
			}
			row.Status = model.AdtySiteDomainRemoved
			row.AutomationState = "removed"
			row.TurnstileWidget = ""
			return nil
		})
	}
	// Persist each successful provider resource immediately, even if close changes
	// the desired state while the request is in flight. The next run cleans it.
	ensure := func(wanted AdtyDNSRecord) error {
		index := -1
		for i, existing := range records {
			if existing.Name == wanted.Name && existing.Type == "CNAME" && existing.Line == wanted.Line && existing.Value != wanted.Value && existing.ID != "" {
				updated, err := AdtyAliDNS().Update(ctx, row.DNSRoot, existing, wanted.Value)
				if err != nil {
					return err
				}
				records[i] = updated
				encoded, _ := common.Marshal(records)
				return adtySaveDomainResource(row, map[string]any{"dns_records": string(encoded)})
			}
			if existing.Name == wanted.Name && existing.Type == wanted.Type && existing.Line == wanted.Line && existing.Value == wanted.Value {
				if existing.ID != "" {
					found, err := AdtyAliDNS().Find(ctx, row.DNSRoot, wanted)
					if err != nil {
						return err
					}
					if found == nil || found.ID != existing.ID {
						return fmt.Errorf("Managed DNS record is missing or changed; administrator reconciliation is required")
					}
					return nil
				}
				index = i
				break
			}
		}
		found, err := AdtyAliDNS().Find(ctx, row.DNSRoot, wanted)
		if err != nil {
			return err
		}
		if found != nil {
			if index < 0 {
				return fmt.Errorf("A matching unmanaged DNS record exists; administrator reconciliation is required")
			}
			records[index] = *found
			encoded, _ := common.Marshal(records)
			return adtySaveDomainResource(row, map[string]any{"dns_records": string(encoded)})
		}
		if index < 0 {
			index = len(records)
			records = append(records, wanted)
			encoded, _ := common.Marshal(records)
			if err := adtySaveDomainResource(row, map[string]any{"dns_records": string(encoded)}); err != nil {
				return err
			}
		}
		created, err := AdtyAliDNS().Add(ctx, row.DNSRoot, wanted)
		if err != nil {
			return err
		}
		records[index] = created
		encoded, _ := common.Marshal(records)
		return adtySaveDomainResource(row, map[string]any{"dns_records": string(encoded)})
	}
	if row.ManagedBy == "platform" {
		row.MainlandTarget, row.DNSTarget = cfg.DomainMainlandTarget, cfg.CnameTarget
		if err := adtySaveDomainResource(row, map[string]any{"mainland_target": row.MainlandTarget, "dns_target": row.DNSTarget}); err != nil {
			return err
		}
		for _, record := range []AdtyDNSRecord{{Type: "CNAME", Name: row.Hostname, Value: row.MainlandTarget, Line: "internal"}, {Type: "CNAME", Name: row.Hostname, Value: row.DNSTarget, Line: "oversea"}} {
			if err := ensure(record); err != nil {
				return err
			}
		}
	}
	if strings.HasPrefix(row.CFHostnameId, adtyCloudResourcePendingPrefix) {
		row.CFHostnameId = ""
		if err := adtySaveDomainResource(row, map[string]any{"cf_hostname_id": ""}); err != nil {
			return err
		}
	}
	if row.CFHostnameId == "" {
		hostnameID, _, err := adtyLookupCloudResourceState(model.DB, row.ProviderZone, row)
		if err != nil {
			return err
		}
		if hostnameID != "" {
			row.CFHostnameId = hostnameID
			if err := adtySaveDomainResource(row, map[string]any{"cf_hostname_id": row.CFHostnameId}); err != nil {
				return err
			}
		}
	}
	if row.CFHostnameId == "" {
		// Unknown existing resources need an explicit reconciliation, never deletion
		// based solely on the matching hostname.
		if row.ManagedBy != "platform" {
			if row.OwnershipToken == "" {
				return fmt.Errorf("Domain ownership verification is required")
			}
			matched, err := adtyOwnershipTXTMatches(ctx, row.Hostname, row.OwnershipToken)
			if err != nil {
				return fmt.Errorf("Domain ownership lookup failed")
			}
			if !matched {
				return fmt.Errorf("Domain ownership TXT is not verified")
			}
		}
		candidate, err := AdtyCloudflare.findCustomHostname(ctx, row.ProviderZone, row.Hostname)
		if err != nil {
			return err
		}
		if candidate != nil {
			return fmt.Errorf("An unrecorded Cloudflare hostname exists; administrator reconciliation is required")
		}
		if err := adtySaveDomainResource(row, map[string]any{"cf_create_started_at": time.Now().Unix()}); err != nil {
			return err
		}
		detail, err := AdtyCloudflare.createCustomHostname(ctx, row.ProviderZone, row)
		if err != nil {
			return err
		}
		if detail.Id == "" {
			return fmt.Errorf("Cloudflare did not return a hostname ID")
		}
		row.CFHostnameId = detail.Id
		if err := adtySaveDomainResource(row, map[string]any{"cf_hostname_id": detail.Id}); err != nil {
			return err
		}
	}
	detail, err := AdtyCloudflare.getCustomHostname(ctx, row.ProviderZone, row.CFHostnameId)
	if err != nil {
		return err
	}
	if !strings.EqualFold(detail.Hostname, row.Hostname) {
		return fmt.Errorf("Cloudflare hostname identity mismatch")
	}
	verify := verifyRecordsFor(row.Hostname, row.DNSTarget, detail)
	if row.ManagedBy == "platform" {
		var requested []adtyVerifyRecord
		if err := common.Unmarshal([]byte(verify), &requested); err != nil {
			return err
		}
		for _, record := range requested {
			if record.Type == "TXT" {
				if err := ensure(AdtyDNSRecord{Type: "TXT", Name: record.Name, Value: record.Value, Line: "default"}); err != nil {
					return err
				}
			}
		}
	}
	status, state := model.AdtySiteDomainPending, "validating"
	if detail.Status == "active" && detail.SSL.Status == "active" {
		status, state = model.AdtySiteDomainActive, "ready"
	}
	if row.DesiredState == "redirect_only" && status == model.AdtySiteDomainActive {
		state = "redirect_only"
	}
	if status == model.AdtySiteDomainActive &&
		row.ManagedBy != "platform" &&
		(row.Role == model.AdtySiteDomainRoleSite || row.Role == model.AdtySiteDomainRoleBaseURL) &&
		row.DesiredState == "serve" && row.TurnstileWidget == "" && cfg.TurnstileConfigured() {
		var widget string
		if err := model.DB.Transaction(func(tx *gorm.DB) error {
			var current model.AdtySiteDomain
			if err := adtyLockDomainRow(tx, row.Id, &current); err != nil {
				return err
			}
			if current.LeaseToken != row.LeaseToken || current.Generation != row.Generation {
				return errAdtyDomainLeaseLost
			}
			if current.Status == model.AdtySiteDomainRemoved ||
				current.ManagedBy == "platform" ||
				current.DesiredState != "serve" ||
				current.TurnstileWidget != "" {
				widget = current.TurnstileWidget
				return nil
			}
			var err error
			widget, err = adtyTurnstileAssign(ctx, tx, current.Hostname)
			if err != nil {
				return err
			}
			if widget != "" {
				res := tx.Model(&model.AdtySiteDomain{}).
					Where("id = ? AND lease_token = ? AND generation = ?", row.Id, row.LeaseToken, row.Generation).
					Update("turnstile_widget", widget)
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected != 1 {
					return errAdtyDomainLeaseLost
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if widget != "" {
			row.TurnstileWidget = widget
		}
	}
	// Keep an unverified serving host out of the route table.
	if status == model.AdtySiteDomainActive && row.DesiredState == "serve" {
		if err := adtyProbeDomainHTTPS(ctx, row); err != nil {
			result := model.DB.Model(&model.AdtySiteDomain{}).Where("id = ? AND generation = ? AND lease_token = ?", row.Id, row.Generation, row.LeaseToken).Updates(map[string]any{"status": model.AdtySiteDomainPending, "ssl_status": detail.SSL.Status, "verify_records": verify})
			return errors.Join(err, result.Error)
		}
	}
	result := model.DB.Model(&model.AdtySiteDomain{}).Where("id = ? AND generation = ? AND lease_token = ?", row.Id, row.Generation, row.LeaseToken).Updates(map[string]any{"status": status, "automation_state": state, "ssl_status": detail.SSL.Status, "verify_records": verify})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("Domain intent changed during reconciliation")
	}
	if state == "redirect_only" {
		InvalidateAdtySiteCache()
		return adtyProbeDomainHTTPS(ctx, row)
	}
	return nil
}

func adtyAliDNSUserError(err error) error {
	if err == nil {
		return nil
	}
	var msg *common.Message
	if errors.As(err, &msg) {
		return msg
	}
	common.SysError(common.LogText("aliyun dns request failed: %s", err.Error()))
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "invalidaccesskey") || strings.Contains(lower, "signaturedoesnotmatch") ||
		strings.Contains(lower, "forbidden") || strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "401") || strings.Contains(lower, "403") {
		return common.NewMessage("Aliyun DNS rejected the configured credentials")
	}
	return common.NewMessage("Aliyun DNS request failed. Check the DNS configuration and try again")
}

func adtyDomainAutomationUserError(err error) error {
	if err == nil {
		return nil
	}
	var msg *common.Message
	if errors.As(err, &msg) {
		return msg
	}
	var cfErr *AdtyCloudflareError
	if errors.As(err, &cfErr) {
		return adtyCloudflareUserError(err)
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "aliyun dns") || strings.Contains(lower, "alidns") {
		return adtyAliDNSUserError(err)
	}
	common.SysError(common.LogText("domain automation error: %s", err.Error()))
	return common.NewMessage("Domain automation failed. Please check the domain provider settings and try again")
}

func releaseRedirectWidget(ctx context.Context, row *model.AdtySiteDomain) error {
	cfg := resellerSetting()
	if cfg.CloudflareAccountId == "" {
		return common.NewMessage("Restore the Turnstile account to release the hostname")
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		var current model.AdtySiteDomain
		if err := adtyLockDomainRow(tx, row.Id, &current); err != nil {
			return err
		}
		if current.LeaseToken != row.LeaseToken || current.Generation != row.Generation {
			return errAdtyDomainLeaseLost
		}
		if current.DesiredState != "redirect_only" || current.TurnstileWidget == "" {
			return nil
		}
		if err := adtyTurnstileRemoveDomain(ctx, tx, current.TurnstileWidget, current.Hostname); err != nil {
			return err
		}
		res := tx.Model(&model.AdtySiteDomain{}).
			Where("id = ? AND lease_token = ? AND generation = ?", row.Id, row.LeaseToken, row.Generation).
			Update("turnstile_widget", "")
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errAdtyDomainLeaseLost
		}
		row.TurnstileWidget = ""
		return nil
	}); err != nil {
		return err
	}
	return nil
}

// CheckAdtyDomainProvider only reads provider configuration; it does not claim
// that reading configuration proves write privileges or public TLS readiness.
func CheckAdtyDomainProvider(ctx context.Context, provider string) error {
	cfg := adty_setting.GetResellerSetting()
	switch provider {
	case "aliyun":
		if err := AdtyAliDNS().Check(ctx, cfg.AliyunDomain); err != nil {
			return adtyAliDNSUserError(err)
		}
		return nil
	case "cloudflare":
		zoneID := strings.TrimSpace(cfg.CloudflareZoneId)
		if !adtyCloudflareZoneIDPattern.MatchString(zoneID) {
			return common.NewMessage("Invalid Cloudflare zone ID")
		}
		var fallback struct {
			Origin string `json:"origin"`
			Status string `json:"status"`
		}
		if err := AdtyCloudflare.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(zoneID)+"/custom_hostnames/fallback_origin", nil, &fallback); err != nil {
			return adtyCloudflareUserError(err)
		}
		if fallback.Origin != cfg.FallbackOrigin || fallback.Status != "active" {
			return common.NewMessage("Cloudflare fallback origin is not active or does not match configuration")
		}
		return nil
	default:
		return common.NewMessage("Unknown domain provider")
	}
}
