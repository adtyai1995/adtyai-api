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

const adtyActiveDomainProbeDemoteThreshold = 3

var adtyOwnerDomainCheckCooldown = 10 * time.Second

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
		return common.NewMessage("Site root is outside the managed DNS domain")
	}
	for _, role := range []string{model.AdtySiteDomainRoleSite, model.AdtySiteDomainRoleBaseURL} {
		host := site.Subdomain + "." + site.RootDomain
		mainlandTarget := strings.TrimSpace(cfg.DomainMainlandTarget)
		if role == model.AdtySiteDomainRoleBaseURL {
			host = fmt.Sprintf("api-s%d.%s", site.Id, site.RootDomain)
			mainlandTarget = ""
		}
		row := model.AdtySiteDomain{SiteId: site.Id, Hostname: host, Role: role, ManagedBy: "platform", DesiredState: "serve", Status: model.AdtySiteDomainPending, AutomationState: "queued", Generation: 1, ProviderZone: cfg.CloudflareZoneId, DNSRoot: cfg.AliyunDomain, DNSTarget: strings.TrimSpace(cfg.CnameTarget), MainlandTarget: mainlandTarget, CreatedAt: time.Now().Unix()}
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
	changed := false
	defer func() {
		if changed {
			InvalidateAdtySiteCache()
		}
	}()
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
		rowChanged, err := adtyReconcileDomain(runctx, row)
		cancel()
		if rowChanged {
			changed = true
		}
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
	backfillChanged, err := adtyBackfillActiveCustomBaseURLTurnstile(ctx, now.Add(time.Since(started)))
	if backfillChanged {
		changed = true
	}
	if err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

// CheckAdtySiteDomainNow runs a single immediate DNS/Cloudflare verification check
// for an owner's pending custom domain, bypassing retry backoff while preserving
// ownership checks, active-site checks, per-domain cooldown, and lease isolation.
func CheckAdtySiteDomainNow(ctx context.Context, ownerUserID int, domainID int) (*model.AdtySiteDomain, error) {
	if domainID <= 0 {
		return nil, common.NewMessage("Invalid parameters")
	}
	site, err := AdtyOwnerSite(ownerUserID)
	if errors.Is(err, ErrAdtySiteNotOwner) {
		return nil, ErrAdtySiteNotFound
	}
	if err != nil {
		return nil, err
	}
	if site.Status != model.AdtySiteStatusActive {
		return nil, ErrAdtySiteStatus
	}
	var row model.AdtySiteDomain
	if err := model.DB.WithContext(ctx).
		Where("id = ? AND site_id = ?", domainID, site.Id).
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, common.NewMessage("Domain not found")
		}
		return nil, err
	}
	if row.ManagedBy == "platform" {
		return nil, common.NewMessage("Platform domains are managed by the platform")
	}
	if row.Status == model.AdtySiteDomainRemoved || row.DesiredState == "removed" {
		return nil, common.NewMessage("Domain not found")
	}
	if row.Status == model.AdtySiteDomainActive {
		return &row, nil
	}
	if row.Status != model.AdtySiteDomainPending {
		return nil, common.NewMessage("This domain failed verification. Delete it and add it again.")
	}
	now := time.Now()
	if row.LeaseUntil > now.Unix() {
		return nil, common.NewMessage("A DNS check is already in progress for this domain. Please try again in a few seconds.")
	}
	hasBeenChecked := row.LastCheckedAt > 0 &&
		(row.LastCheckedAt > row.CreatedAt || row.Attempts > 0 || strings.TrimSpace(row.LastError) != "" || strings.TrimSpace(row.CFHostnameId) != "")
	if hasBeenChecked && adtyOwnerDomainCheckCooldown > 0 {
		cooldownSec := int64(adtyOwnerDomainCheckCooldown / time.Second)
		elapsed := now.Unix() - row.LastCheckedAt
		if elapsed >= 0 && elapsed < cooldownSec {
			return nil, common.NewMessage("Please wait {{seconds}} seconds before checking DNS again", map[string]any{"seconds": cooldownSec - elapsed})
		}
	}

	if strings.TrimSpace(row.AutomationState) == "" {
		if !resellerSetting().CloudflareConfigured() {
			return nil, common.NewMessage("Custom domain service is not configured")
		}
		runctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		changed, checkErr := adtyCheckDomainSnapshot(runctx, &row, now)
		cancel()
		if changed {
			InvalidateAdtySiteCache()
		}
		if checkErr != nil {
			return nil, adtyDomainAutomationUserError(checkErr)
		}
		var updated model.AdtySiteDomain
		if err := model.DB.WithContext(ctx).Where("id = ? AND site_id = ?", row.Id, site.Id).First(&updated).Error; err != nil {
			return &row, nil
		}
		return &updated, nil
	}

	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	leaseToken := hex.EncodeToString(nonce)
	claim := model.DB.WithContext(ctx).Model(&model.AdtySiteDomain{}).
		Where("id = ? AND site_id = ? AND lease_until <= ? AND generation = ? AND status = ?",
			row.Id, site.Id, now.Unix(), row.Generation, model.AdtySiteDomainPending).
		Updates(map[string]any{
			"lease_token": leaseToken,
			"lease_until": now.Add(2 * time.Minute).Unix(),
			"next_run_at": 0,
		})
	if claim.Error != nil {
		return nil, claim.Error
	}
	if claim.RowsAffected == 0 {
		return nil, common.NewMessage("A DNS check is already in progress for this domain. Please try again in a few seconds.")
	}
	var fresh model.AdtySiteDomain
	if err := model.DB.WithContext(ctx).
		Where("id = ? AND lease_token = ? AND generation = ?", row.Id, leaseToken, row.Generation).
		First(&fresh).Error; err != nil {
		return nil, err
	}

	runctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	rowChanged, recErr := adtyReconcileDomain(runctx, &fresh)
	cancel()

	finished := time.Now()
	updates := map[string]any{
		"lease_token":     "",
		"lease_until":     0,
		"last_checked_at": finished.Unix(),
		"next_run_at":     finished.Add(5 * time.Minute).Unix(),
	}
	if recErr != nil {
		safeErr := adtyDomainAutomationUserError(recErr)
		updates["attempts"] = fresh.Attempts + 1
		updates["last_error"] = safeErr.Error()
		updates["automation_state"] = "retry"
		updates["next_run_at"] = finished.Add(time.Duration(min(3600, 30*(1<<min(fresh.Attempts, 7)))) * time.Second).Unix()
	} else {
		updates["attempts"] = 0
		updates["last_error"] = ""
	}
	if err := model.DB.Model(&model.AdtySiteDomain{}).
		Where("id = ? AND lease_token = ? AND generation = ?", fresh.Id, fresh.LeaseToken, fresh.Generation).
		Updates(updates).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Model(&model.AdtySiteDomain{}).
		Where("id = ? AND lease_token = ? AND generation <> ?", fresh.Id, fresh.LeaseToken, fresh.Generation).
		Updates(map[string]any{"lease_token": "", "lease_until": 0, "next_run_at": 0}).Error; err != nil {
		return nil, err
	}
	if rowChanged {
		InvalidateAdtySiteCache()
	}
	var updated model.AdtySiteDomain
	if err := model.DB.WithContext(ctx).Where("id = ? AND site_id = ?", fresh.Id, site.Id).First(&updated).Error; err != nil {
		return &fresh, nil
	}
	return &updated, nil
}

func adtyBackfillActiveCustomBaseURLTurnstile(ctx context.Context, now time.Time) (bool, error) {
	if !resellerSetting().TurnstileConfigured() {
		return false, nil
	}
	started := time.Now()
	var rows []model.AdtySiteDomain
	nonDeletingSites := model.DB.Model(&model.AdtySite{}).Select("id").Where("status <> ?", model.AdtySiteStatusDeleting)
	if err := model.DB.WithContext(ctx).
		Where("status = ? AND role = ? AND managed_by <> ? AND turnstile_widget = ? AND desired_state IN ? AND lease_until <= ? AND site_id IN (?)",
			model.AdtySiteDomainActive, model.AdtySiteDomainRoleBaseURL, "platform", "", []string{"", "serve"}, now.Unix(), nonDeletingSites).
		Order("id ASC").Limit(50).Find(&rows).Error; err != nil {
		return false, err
	}
	changed := false
	var failures []error
	for i := range rows {
		if err := ctx.Err(); err != nil {
			return changed, errors.Join(append(failures, err)...)
		}
		claimTime := now.Add(time.Since(started))
		row := &rows[i]
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			return changed, err
		}
		row.LeaseToken = hex.EncodeToString(nonce)
		claim := model.DB.Model(&model.AdtySiteDomain{}).
			Where("id = ? AND lease_until <= ? AND generation = ? AND status = ? AND turnstile_widget = ?",
				row.Id, claimTime.Unix(), row.Generation, model.AdtySiteDomainActive, "").
			Updates(map[string]any{"lease_token": row.LeaseToken, "lease_until": claimTime.Add(2 * time.Minute).Unix()})
		if claim.Error != nil {
			return changed, claim.Error
		}
		if claim.RowsAffected == 0 {
			continue
		}
		runctx, cancel := context.WithTimeout(ctx, time.Minute)
		widget, err := adtyBackfillOneBaseURLTurnstile(runctx, row)
		cancel()
		updates := map[string]any{"lease_token": "", "lease_until": 0}
		if err != nil {
			updates["last_error"] = adtyCloudflareUserError(err).Error()
			failures = append(failures, fmt.Errorf("domain %d turnstile backfill: %w", row.Id, err))
		} else if widget != "" {
			updates["last_error"] = ""
			changed = true
		}
		if err := model.DB.Model(&model.AdtySiteDomain{}).
			Where("id = ? AND lease_token = ? AND generation = ?", row.Id, row.LeaseToken, row.Generation).
			Updates(updates).Error; err != nil {
			return changed, err
		}
		genChangedUpdates := map[string]any{"lease_token": "", "lease_until": 0, "next_run_at": 0}
		if err := model.DB.Model(&model.AdtySiteDomain{}).
			Where("id = ? AND lease_token = ? AND generation <> ?", row.Id, row.LeaseToken, row.Generation).
			Updates(genChangedUpdates).Error; err != nil {
			return changed, err
		}
	}
	return changed, errors.Join(failures...)
}

func adtyBackfillOneBaseURLTurnstile(ctx context.Context, row *model.AdtySiteDomain) (string, error) {
	unlock := adtyDomainLocks.Lock(row.Id)
	defer unlock()

	// Tx1: Verify lease, eligibility, and site state without holding locks over HTTP.
	var hostname string
	eligible := false
	if err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		hostname = current.Hostname
		eligible = true
		return nil
	}); err != nil {
		return "", err
	}
	if !eligible {
		return "", nil
	}

	// External Turnstile API call runs OUTSIDE any DB transaction (R4-16).
	widget, err := adtyTurnstileAssign(ctx, hostname)
	if err != nil || widget == "" {
		return "", err
	}

	// Tx2: Re-verify lease, generation, eligibility, and persist turnstile_widget.
	compensateRemove := false
	savedWidget := ""
	err = model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current model.AdtySiteDomain
		if err := adtyLockDomainRow(tx, row.Id, &current); err != nil {
			return err
		}
		if current.LeaseToken != row.LeaseToken {
			return errAdtyDomainLeaseLost
		}
		if current.Generation != row.Generation {
			if current.Status == model.AdtySiteDomainRemoved || current.DesiredState == "removed" || current.DesiredState == "redirect_only" {
				compensateRemove = true
			}
			return errAdtyDomainLeaseLost
		}
		if current.Status != model.AdtySiteDomainActive ||
			current.Role != model.AdtySiteDomainRoleBaseURL ||
			current.ManagedBy == "platform" ||
			(current.DesiredState != "" && current.DesiredState != "serve") {
			if current.Status == model.AdtySiteDomainRemoved || current.DesiredState == "removed" || current.DesiredState == "redirect_only" {
				compensateRemove = true
			}
			return nil
		}
		var site model.AdtySite
		if err := tx.Select("id", "status").First(&site, current.SiteId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				compensateRemove = true
				return nil
			}
			return err
		}
		if site.Status == model.AdtySiteStatusDeleting {
			compensateRemove = true
			return nil
		}
		if current.TurnstileWidget != "" {
			savedWidget = current.TurnstileWidget
			return nil
		}
		res := tx.Model(&model.AdtySiteDomain{}).
			Where("id = ? AND lease_token = ? AND generation = ?", row.Id, row.LeaseToken, row.Generation).
			Update("turnstile_widget", widget)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errAdtyDomainLeaseLost
		}
		savedWidget = widget
		return nil
	})
	if compensateRemove {
		_ = adtyTurnstileRemoveDomain(ctx, widget, hostname)
	}
	if err != nil {
		return "", err
	}
	if savedWidget != "" {
		row.TurnstileWidget = savedWidget
	}
	return savedWidget, nil
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

func adtyReconcileDomain(ctx context.Context, row *model.AdtySiteDomain) (bool, error) {
	unlock := adtyDomainLocks.Lock(row.Id)
	defer unlock()

	changed := false
	var site model.AdtySite
	if err := model.DB.WithContext(ctx).Select("id", "status").First(&site, row.SiteId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if site.Status == model.AdtySiteStatusDeleting {
		return false, nil
	}
	cfg := resellerSetting()
	if row.ProviderZone == "" {
		row.ProviderZone = cfg.CloudflareZoneId
		if err := adtySaveDomainResource(row, map[string]any{"provider_zone": row.ProviderZone}); err != nil {
			return changed, err
		}
	}
	if row.ProviderZone != cfg.CloudflareZoneId {
		return changed, common.NewMessage("Stored Cloudflare zone differs; restore credentials or explicitly migrate this domain")
	}
	if row.DNSRoot != "" && row.DNSRoot != cfg.AliyunDomain {
		return changed, common.NewMessage("Stored DNS root differs; restore DNS configuration before continuing")
	}
	if row.DesiredState == "redirect_only" && row.TurnstileWidget != "" {
		prevWidget := row.TurnstileWidget
		if err := releaseRedirectWidget(ctx, row); err != nil {
			return changed, err
		}
		if row.TurnstileWidget != prevWidget {
			changed = true
		}
	}
	var records []AdtyDNSRecord
	if row.DNSRecords != "" {
		if err := common.Unmarshal([]byte(row.DNSRecords), &records); err != nil {
			return changed, common.NewMessage("Stored DNS resource list is invalid")
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
				return changed, err
			}
			createAttempted = createAttempted || lookupAttempted
		}
		if hostnameID == "" && createAttempted {
			candidate, err := AdtyCloudflare.findCustomHostname(ctx, row.ProviderZone, row.Hostname)
			if err != nil {
				return changed, err
			}
			if candidate != nil {
				return changed, common.NewMessage("Unrecorded Cloudflare creation requires reconciliation before cleanup")
			}
		}
		if hostnameID != "" {
			if err := AdtyCloudflare.deleteCustomHostname(ctx, row.ProviderZone, hostnameID); err != nil {
				return changed, err
			}
		}
		if hostnameID != "" || createAttempted || row.CFHostnameId != "" {
			if err := model.DB.Where("domain_id = ? AND generation = ? AND zone = ?", row.Id, adtyCustomHostnameGeneration(row), row.ProviderZone).Delete(&model.AdtyDomainCloudResource{}).Error; err != nil {
				return changed, err
			}
			if row.CFHostnameId != "" || row.CFCreateStartedAt > 0 {
				row.CFHostnameId = ""
				row.CFCreateStartedAt = 0
				if err := adtySaveDomainResource(row, map[string]any{"cf_hostname_id": "", "cf_create_started_at": 0}); err != nil {
					return changed, err
				}
			}
		}
		for len(records) > 0 {
			if records[0].ID == "" {
				found, err := AdtyAliDNS().Find(ctx, row.DNSRoot, records[0])
				if err != nil {
					return changed, err
				}
				if found != nil {
					records[0] = *found
				}
			}
			if records[0].ID != "" {
				if err := AdtyAliDNS().Delete(ctx, row.DNSRoot, records[0]); err != nil {
					return changed, err
				}
			}
			records = records[1:]
			encoded, _ := common.Marshal(records)
			if err := adtySaveDomainResource(row, map[string]any{"dns_records": string(encoded)}); err != nil {
				return changed, err
			}
		}
		var alreadyRemoved, needTurnstileRemove bool
		var widgetID, hostname string
		if err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var current model.AdtySiteDomain
			if err := adtyLockDomainRow(tx, row.Id, &current); err != nil {
				return err
			}
			if current.LeaseToken != row.LeaseToken || current.Generation != row.Generation || current.DesiredState != "removed" {
				return errAdtyDomainLeaseLost
			}
			if current.Status == model.AdtySiteDomainRemoved {
				alreadyRemoved = true
				return nil
			}
			needTurnstileRemove = current.TurnstileWidget != "" || (current.ManagedBy != "platform" && (hostnameID != "" || createAttempted || current.Status == model.AdtySiteDomainActive) && cfg.TurnstileConfigured())
			widgetID = current.TurnstileWidget
			hostname = current.Hostname
			return nil
		}); err != nil {
			return changed, err
		}
		if alreadyRemoved {
			return changed, nil
		}
		// External Turnstile removal runs OUTSIDE any DB transaction (R4-16).
		if needTurnstileRemove {
			if err := adtyTurnstileRemoveDomain(ctx, widgetID, hostname); err != nil {
				return changed, err
			}
		}
		removed := false
		if err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
			res := tx.Model(&model.AdtySiteDomain{}).
				Where("id = ? AND lease_token = ? AND generation = ?", row.Id, row.LeaseToken, row.Generation).
				Updates(map[string]any{
					"status":              model.AdtySiteDomainRemoved,
					"automation_state":    "removed",
					"turnstile_widget":    "",
					"edge_cert_status":    "",
					"edge_cert_error":     "",
					"edge_cert_not_after": 0,
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return errAdtyDomainLeaseLost
			}
			if err := adtyInvalidateCustomEdgeCertTx(tx, row.Id, current.Hostname); err != nil {
				return err
			}
			removed = true
			row.Status = model.AdtySiteDomainRemoved
			row.AutomationState = "removed"
			row.TurnstileWidget = ""
			row.EdgeCertStatus = ""
			row.EdgeCertError = ""
			row.EdgeCertNotAfter = 0
			return nil
		}); err != nil {
			return changed, err
		}
		if removed {
			changed = true
		}
		return changed, nil
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
						return common.NewMessage("Managed DNS record is missing or changed; administrator reconciliation is required")
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
				return common.NewMessage("A matching unmanaged DNS record exists; administrator reconciliation is required")
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
		prevDNSTarget, prevMainlandTarget := row.DNSTarget, row.MainlandTarget
		row.DNSTarget = strings.TrimSpace(cfg.CnameTarget)
		if row.Role == model.AdtySiteDomainRoleSite {
			row.MainlandTarget = strings.TrimSpace(cfg.DomainMainlandTarget)
		} else {
			row.MainlandTarget = ""
		}
		if err := adtySaveDomainResource(row, map[string]any{"mainland_target": row.MainlandTarget, "dns_target": row.DNSTarget}); err != nil {
			return changed, err
		}
		if row.DNSTarget != prevDNSTarget || row.MainlandTarget != prevMainlandTarget {
			changed = true
		}
		wanted := []AdtyDNSRecord{{Type: "CNAME", Name: row.Hostname, Value: row.DNSTarget, Line: "default"}}
		if row.Role == model.AdtySiteDomainRoleSite && row.MainlandTarget != "" {
			wanted = append(wanted, AdtyDNSRecord{Type: "CNAME", Name: row.Hostname, Value: row.MainlandTarget, Line: "internal"})
		}
		for _, record := range wanted {
			if err := ensure(record); err != nil {
				return changed, err
			}
		}
		for i := 0; i < len(records); {
			existing := records[i]
			if existing.Type != "CNAME" {
				i++
				continue
			}
			keep := false
			for _, w := range wanted {
				if existing.Name == w.Name && existing.Line == w.Line {
					keep = true
					break
				}
			}
			if keep {
				i++
				continue
			}
			if existing.ID == "" {
				found, err := AdtyAliDNS().Find(ctx, row.DNSRoot, existing)
				if err != nil {
					return changed, err
				}
				if found != nil {
					existing = *found
				}
			}
			if existing.ID != "" {
				if err := AdtyAliDNS().Delete(ctx, row.DNSRoot, existing); err != nil {
					return changed, err
				}
			}
			records = append(records[:i], records[i+1:]...)
			encoded, _ := common.Marshal(records)
			if err := adtySaveDomainResource(row, map[string]any{"dns_records": string(encoded)}); err != nil {
				return changed, err
			}
		}
	}
	prevCFHostnameID := row.CFHostnameId
	if strings.HasPrefix(row.CFHostnameId, adtyCloudResourcePendingPrefix) {
		row.CFHostnameId = ""
		if err := adtySaveDomainResource(row, map[string]any{"cf_hostname_id": ""}); err != nil {
			return changed, err
		}
	}
	if row.CFHostnameId == "" {
		hostnameID, _, err := adtyLookupCloudResourceState(model.DB, row.ProviderZone, row)
		if err != nil {
			return changed, err
		}
		if hostnameID != "" {
			row.CFHostnameId = hostnameID
			if err := adtySaveDomainResource(row, map[string]any{"cf_hostname_id": row.CFHostnameId}); err != nil {
				return changed, err
			}
			if row.CFHostnameId != prevCFHostnameID {
				changed = true
			}
		}
	}
	if row.CFHostnameId == "" {
		// Unknown existing resources need an explicit reconciliation, never deletion
		// based solely on the matching hostname.
		if row.ManagedBy != "platform" {
			if row.OwnershipToken == "" {
				return changed, common.NewMessage("Domain ownership verification is required")
			}
			matched, err := adtyOwnershipTXTMatches(ctx, row.Hostname, row.OwnershipToken)
			if err != nil {
				return changed, common.NewMessage("Domain ownership lookup failed")
			}
			if !matched {
				return changed, common.NewMessage("Domain ownership TXT is not verified")
			}
		}
		candidate, err := AdtyCloudflare.findCustomHostname(ctx, row.ProviderZone, row.Hostname)
		if err != nil {
			return changed, err
		}
		if candidate != nil {
			return changed, common.NewMessage("An unrecorded Cloudflare hostname exists; administrator reconciliation is required")
		}
		startedAt := time.Now().Unix()
		if err := adtySaveDomainResource(row, map[string]any{"cf_create_started_at": startedAt}); err != nil {
			return changed, err
		}
		row.CFCreateStartedAt = startedAt
		detail, err := AdtyCloudflare.createCustomHostname(ctx, row.ProviderZone, row)
		if err != nil {
			return changed, err
		}
		if detail.Id == "" {
			return changed, common.NewMessage("Cloudflare did not return a hostname ID")
		}
		row.CFHostnameId = detail.Id
		if err := adtySaveDomainResource(row, map[string]any{"cf_hostname_id": detail.Id}); err != nil {
			return changed, err
		}
		if row.CFHostnameId != prevCFHostnameID {
			changed = true
		}
	}
	detail, err := AdtyCloudflare.getCustomHostname(ctx, row.ProviderZone, row.CFHostnameId)
	if err != nil {
		return changed, err
	}
	if !strings.EqualFold(detail.Hostname, row.Hostname) {
		return changed, common.NewMessage("Cloudflare hostname identity mismatch")
	}
	verify := verifyRecordsFor(row.Hostname, row.DNSTarget, detail)
	if row.ManagedBy == "platform" {
		var requested []adtyVerifyRecord
		if err := common.Unmarshal([]byte(verify), &requested); err != nil {
			return changed, err
		}
		for _, record := range requested {
			if record.Type == "TXT" {
				if err := ensure(AdtyDNSRecord{Type: "TXT", Name: record.Name, Value: record.Value, Line: "default"}); err != nil {
					return changed, err
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
		prevWidget := row.TurnstileWidget
		var widget, hostname string
		needAssign := false
		if err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
			needAssign = true
			hostname = current.Hostname
			return nil
		}); err != nil {
			return changed, err
		}
		// External Turnstile assignment runs OUTSIDE any DB transaction (R4-16).
		if needAssign {
			assigned, assignErr := adtyTurnstileAssign(ctx, hostname)
			if assignErr != nil {
				return changed, assignErr
			}
			if assigned != "" {
				compensateRemove := false
				txErr := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
					var current model.AdtySiteDomain
					if err := adtyLockDomainRow(tx, row.Id, &current); err != nil {
						return err
					}
					if current.LeaseToken != row.LeaseToken {
						return errAdtyDomainLeaseLost
					}
					if current.Generation != row.Generation {
						if current.Status == model.AdtySiteDomainRemoved || current.DesiredState == "removed" || current.DesiredState == "redirect_only" {
							compensateRemove = true
						}
						return errAdtyDomainLeaseLost
					}
					if current.Status == model.AdtySiteDomainRemoved ||
						current.ManagedBy == "platform" ||
						current.DesiredState != "serve" {
						if current.Status == model.AdtySiteDomainRemoved || current.DesiredState == "removed" || current.DesiredState == "redirect_only" {
							compensateRemove = true
						}
						widget = current.TurnstileWidget
						return nil
					}
					if current.TurnstileWidget != "" {
						widget = current.TurnstileWidget
						return nil
					}
					res := tx.Model(&model.AdtySiteDomain{}).
						Where("id = ? AND lease_token = ? AND generation = ?", row.Id, row.LeaseToken, row.Generation).
						Update("turnstile_widget", assigned)
					if res.Error != nil {
						return res.Error
					}
					if res.RowsAffected != 1 {
						return errAdtyDomainLeaseLost
					}
					widget = assigned
					return nil
				})
				if compensateRemove {
					_ = adtyTurnstileRemoveDomain(ctx, assigned, hostname)
				}
				if txErr != nil {
					return changed, txErr
				}
			}
		}
		if widget != "" {
			row.TurnstileWidget = widget
		}
		if row.TurnstileWidget != prevWidget {
			changed = true
		}
	}
	// Keep an unverified serving host out of the route table, but require
	// consecutive probe failures before demoting an already-active domain so a
	// single transient network or edge error does not cause an instant 404.
	if status == model.AdtySiteDomainActive && row.DesiredState == "serve" {
		if err := adtyProbeDomainHTTPS(ctx, row); err != nil {
			nextStatus := model.AdtySiteDomainPending
			if row.Status == model.AdtySiteDomainActive && row.Attempts+1 < adtyActiveDomainProbeDemoteThreshold {
				nextStatus = model.AdtySiteDomainActive
			}
			if nextStatus != row.Status {
				changed = true
			}
			result := model.DB.Model(&model.AdtySiteDomain{}).Where("id = ? AND generation = ? AND lease_token = ?", row.Id, row.Generation, row.LeaseToken).Updates(map[string]any{"status": nextStatus, "ssl_status": detail.SSL.Status, "verify_records": verify})
			return changed, errors.Join(err, result.Error)
		}
	}
	result := model.DB.Model(&model.AdtySiteDomain{}).Where("id = ? AND generation = ? AND lease_token = ?", row.Id, row.Generation, row.LeaseToken).Updates(map[string]any{"status": status, "automation_state": state, "ssl_status": detail.SSL.Status, "verify_records": verify})
	if result.Error != nil {
		return changed, result.Error
	}
	if result.RowsAffected != 1 {
		return changed, fmt.Errorf("Domain intent changed during reconciliation")
	}
	if status != row.Status || state != row.AutomationState {
		changed = true
	}
	if state == "redirect_only" {
		if changed {
			InvalidateAdtySiteCache()
		}
		return changed, adtyProbeDomainHTTPS(ctx, row)
	}
	return changed, nil
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
	var widgetID, hostname string
	needRemove := false
	if err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		widgetID = current.TurnstileWidget
		hostname = current.Hostname
		needRemove = true
		return nil
	}); err != nil {
		return err
	}
	if !needRemove {
		return nil
	}
	// External Turnstile removal runs OUTSIDE any DB transaction (R4-16).
	if err := adtyTurnstileRemoveDomain(ctx, widgetID, hostname); err != nil {
		return err
	}
	if err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
