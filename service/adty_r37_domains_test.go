package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupR37DomainTest(t *testing.T, configure func(*adty_setting.ResellerSetting)) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.AdtySite{},
		&model.AdtySiteDomain{},
		&model.AdtySiteAudit{},
		&model.AdtyDomainCloudResource{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	oldDB := model.DB
	oldLookup := adtyLookupOwnershipTXT
	oldLookupCNAME := adtyLookupOwnershipCNAME
	oldDelay := adtyCustomHostnameRecordDelay
	model.DB = db
	adtyCustomHostnameRecordDelay = 0
	adtyLookupOwnershipTXT = func(ctx context.Context, name string) ([]string, error) {
		return nil, &net.DNSError{IsNotFound: true}
	}
	adtyLookupOwnershipCNAME = func(ctx context.Context, name string) (string, error) {
		return "", &net.DNSError{IsNotFound: true}
	}

	rsPtr, ok := config.GlobalConfig.Get(adty_setting.ResellerSettingModule).(*adty_setting.ResellerSetting)
	if !ok || rsPtr == nil {
		t.Fatalf("reseller setting module not registered")
	}
	common.OptionMapRWMutex.Lock()
	oldRS := *rsPtr
	nextRS := adty_setting.DefaultResellerSetting()
	nextRS.CustomDomainEnabled = true
	nextRS.CloudflareApiToken = "test-cf-token"
	nextRS.CloudflareZoneId = "test-zone-id"
	nextRS.CnameTarget = "cname.platform.test"
	nextRS.RootDomains = []string{"platform.test"}
	nextRS.MaxCustomDomainsPerSite = 2
	nextRS.CustomDomainCreatesPerHour = 10
	if configure != nil {
		configure(&nextRS)
	}
	*rsPtr = nextRS
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		model.DB = oldDB
		adtyLookupOwnershipTXT = oldLookup
		adtyLookupOwnershipCNAME = oldLookupCNAME
		adtyCustomHostnameRecordDelay = oldDelay
		common.OptionMapRWMutex.Lock()
		*rsPtr = oldRS
		common.OptionMapRWMutex.Unlock()
	})

	return db
}

func createR37TestSite(t *testing.T, db *gorm.DB, ownerID int, subdomain string) model.AdtySite {
	t.Helper()
	site := model.AdtySite{
		OwnerUserId: ownerID,
		Subdomain:   subdomain,
		RootDomain:  "platform.test",
		Status:      model.AdtySiteStatusActive,
	}
	if err := db.Create(&site).Error; err != nil {
		t.Fatalf("create site %s: %v", subdomain, err)
	}
	return site
}

func assertVerifyRecordsHaveCNAMEOnly(t *testing.T, d model.AdtySiteDomain, expectedCNAME string) {
	t.Helper()
	var records []adtyVerifyRecord
	if err := common.Unmarshal([]byte(d.VerifyRecords), &records); err != nil {
		t.Fatalf("unmarshal verify_records for %s: %v (raw=%s)", d.Hostname, err, d.VerifyRecords)
	}
	if len(records) != 1 || records[0].Type != "CNAME" || records[0].Name != d.Hostname || records[0].Value != expectedCNAME {
		t.Fatalf("expected CNAME-only record for %s -> %s, got %+v", d.Hostname, expectedCNAME, records)
	}
}

func assertVerifyRecordsHaveCNAMEAndTXT(t *testing.T, d model.AdtySiteDomain, expectedCNAME string) {
	t.Helper()
	var records []adtyVerifyRecord
	if err := common.Unmarshal([]byte(d.VerifyRecords), &records); err != nil {
		t.Fatalf("unmarshal verify_records for %s: %v (raw=%s)", d.Hostname, err, d.VerifyRecords)
	}
	var hasCNAME, hasTXT bool
	for _, r := range records {
		switch r.Type {
		case "CNAME":
			if r.Name == d.Hostname && r.Value == expectedCNAME {
				hasCNAME = true
			}
		case "TXT":
			if r.Name == adtyOwnershipTXTName(d.Hostname) && r.Value == d.OwnershipToken && d.OwnershipToken != "" {
				hasTXT = true
			}
		}
	}
	if !hasCNAME || !hasTXT {
		t.Fatalf("expected CNAME and TXT records for %s, got %+v", d.Hostname, records)
	}
}

func TestR37DomainPairValidation(t *testing.T) {
	db := setupR37DomainTest(t, nil)
	createR37TestSite(t, db, 101, "alpha")
	ctx := context.Background()

	if _, err := AddAdtySiteDomainPair(ctx, 101, "", "api.example.org"); !errors.Is(err, ErrAdtyCustomDomainPairRequired) {
		t.Fatalf("missing site host: want ErrAdtyCustomDomainPairRequired, got %v", err)
	}
	if _, err := AddAdtySiteDomainPair(ctx, 101, "   ", "api.example.org"); !errors.Is(err, ErrAdtyCustomDomainPairRequired) {
		t.Fatalf("blank site host: want ErrAdtyCustomDomainPairRequired, got %v", err)
	}
	if _, err := AddAdtySiteDomainPair(ctx, 101, "site.example.org", ""); !errors.Is(err, ErrAdtyCustomDomainPairRequired) {
		t.Fatalf("missing api host: want ErrAdtyCustomDomainPairRequired, got %v", err)
	}
	if _, err := AddAdtySiteDomainPair(ctx, 101, "site.example.org", "  \t "); !errors.Is(err, ErrAdtyCustomDomainPairRequired) {
		t.Fatalf("blank api host: want ErrAdtyCustomDomainPairRequired, got %v", err)
	}
	if _, err := AddAdtySiteDomainPair(ctx, 101, "App.Example.ORG.", "app.example.org"); !errors.Is(err, ErrAdtyCustomDomainPairSame) {
		t.Fatalf("identical hostnames: want ErrAdtyCustomDomainPairSame, got %v", err)
	}
	if _, err := AddAdtySiteDomainPair(ctx, 101, "example.org", "api.example.org"); !errors.Is(err, ErrAdtyCustomDomainInvalid) {
		t.Fatalf("apex site host: want ErrAdtyCustomDomainInvalid, got %v", err)
	}
}

func TestR37DomainPairAtomicRollbackWhenTaken(t *testing.T) {
	db := setupR37DomainTest(t, nil)
	site1 := createR37TestSite(t, db, 201, "one")
	site2 := createR37TestSite(t, db, 202, "two")
	ctx := context.Background()

	// Seed site1 with two occupied hostnames.
	if _, err := AddAdtySiteDomainPair(ctx, 201, "site1.customer.org", "api1.customer.org"); err != nil {
		t.Fatalf("seed site1 pair: %v", err)
	}

	// Case 1: API hostname is already taken by site1 -> neither domain is created for site2.
	if _, err := AddAdtySiteDomainPair(ctx, 202, "site2.customer.org", "api1.customer.org"); !errors.Is(err, ErrAdtyCustomDomainTaken) {
		t.Fatalf("taken api host: want ErrAdtyCustomDomainTaken, got %v", err)
	}
	var count int64
	if err := db.Model(&model.AdtySiteDomain{}).Where("site_id = ?", site2.Id).Count(&count).Error; err != nil {
		t.Fatalf("count site2 domains: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected atomic rollback (0 domains for site2), found %d", count)
	}
	var auditCount int64
	if err := db.Model(&model.AdtySiteAudit{}).Where("site_id = ?", site2.Id).Count(&auditCount).Error; err != nil {
		t.Fatalf("count site2 audits: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("expected atomic rollback (0 audits for site2), found %d", auditCount)
	}

	// Case 2: Site hostname is already taken by site1 -> neither domain is created for site2.
	if _, err := AddAdtySiteDomainPair(ctx, 202, "site1.customer.org", "api2.customer.org"); !errors.Is(err, ErrAdtyCustomDomainTaken) {
		t.Fatalf("taken site host: want ErrAdtyCustomDomainTaken, got %v", err)
	}
	if err := db.Model(&model.AdtySiteDomain{}).Where("site_id = ?", site2.Id).Count(&count).Error; err != nil {
		t.Fatalf("count site2 domains after reverse check: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected atomic rollback (0 domains for site2), found %d", count)
	}
	// Ensure site1 rows were untouched.
	var site1Count int64
	if err := db.Model(&model.AdtySiteDomain{}).Where("site_id = ?", site1.Id).Count(&site1Count).Error; err != nil {
		t.Fatalf("count site1 domains: %v", err)
	}
	if site1Count != 2 {
		t.Fatalf("expected site1 to keep 2 domains, got %d", site1Count)
	}
}

func TestR37DomainPairCreateAndRoleSlotCap(t *testing.T) {
	db := setupR37DomainTest(t, nil)
	site := createR37TestSite(t, db, 301, "gamma")
	ctx := context.Background()

	// Platform-managed domains must NOT occupy custom domain slots or quota.
	platformRows := []model.AdtySiteDomain{
		{SiteId: site.Id, Hostname: "gamma.platform.test", Role: model.AdtySiteDomainRoleSite, ManagedBy: "platform", Status: model.AdtySiteDomainActive},
		{SiteId: site.Id, Hostname: "api-s1.platform.test", Role: model.AdtySiteDomainRoleBaseURL, ManagedBy: "platform", Status: model.AdtySiteDomainActive},
	}
	if err := db.Create(&platformRows).Error; err != nil {
		t.Fatalf("seed platform domains: %v", err)
	}

	domains, err := AddAdtySiteDomainPair(ctx, 301, "portal.client.org", "api.client.org")
	if err != nil {
		t.Fatalf("AddAdtySiteDomainPair failed: %v", err)
	}
	if len(domains) != 2 {
		t.Fatalf("expected 2 domains, got %d", len(domains))
	}
	if domains[0].Hostname != "portal.client.org" || domains[0].Role != model.AdtySiteDomainRoleSite || domains[0].Status != model.AdtySiteDomainPending {
		t.Fatalf("unexpected site domain: %+v", domains[0])
	}
	if domains[1].Hostname != "api.client.org" || domains[1].Role != model.AdtySiteDomainRoleBaseURL || domains[1].Status != model.AdtySiteDomainPending {
		t.Fatalf("unexpected api domain: %+v", domains[1])
	}
	assertVerifyRecordsHaveCNAMEOnly(t, domains[0], "cname.platform.test")
	assertVerifyRecordsHaveCNAMEOnly(t, domains[1], "cname.platform.test")

	var audits int64
	if err := db.Model(&model.AdtySiteAudit{}).Where("site_id = ? AND action = ?", site.Id, adtySiteDomainCreateAuditAction).Count(&audits).Error; err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if audits != 2 {
		t.Fatalf("expected 2 domain_create audits, got %d", audits)
	}

	// Adding another pair while both slots are occupied must fail with ErrAdtyCustomDomainRoleLimit.
	if _, err := AddAdtySiteDomainPair(ctx, 301, "portal2.client.org", "api2.client.org"); !errors.Is(err, ErrAdtyCustomDomainRoleLimit) {
		t.Fatalf("occupied slots pair: want ErrAdtyCustomDomainRoleLimit, got %v", err)
	}
	// Adding a single domain for an occupied role must also fail with ErrAdtyCustomDomainRoleLimit.
	if _, err := AddAdtySiteDomainWithRole(ctx, 301, "portal2.client.org", model.AdtySiteDomainRoleSite); !errors.Is(err, ErrAdtyCustomDomainRoleLimit) {
		t.Fatalf("occupied site slot single: want ErrAdtyCustomDomainRoleLimit, got %v", err)
	}
	if _, err := AddAdtySiteDomainWithRole(ctx, 301, "api2.client.org", model.AdtySiteDomainRoleBaseURL); !errors.Is(err, ErrAdtyCustomDomainRoleLimit) {
		t.Fatalf("occupied base_url slot single: want ErrAdtyCustomDomainRoleLimit, got %v", err)
	}

	// Idempotent replay with the exact same pair returns the existing rows without creating extra audits.
	replayed, err := AddAdtySiteDomainPair(ctx, 301, "portal.client.org", "api.client.org")
	if err != nil {
		t.Fatalf("idempotent pair replay failed: %v", err)
	}
	if len(replayed) != 2 || replayed[0].Id != domains[0].Id || replayed[1].Id != domains[1].Id {
		t.Fatalf("expected same ids [%d,%d], got %+v", domains[0].Id, domains[1].Id, replayed)
	}
	if err := db.Model(&model.AdtySiteAudit{}).Where("site_id = ? AND action = ?", site.Id, adtySiteDomainCreateAuditAction).Count(&audits).Error; err != nil {
		t.Fatalf("recount audits: %v", err)
	}
	if audits != 2 {
		t.Fatalf("expected idempotent replay to keep 2 audits, got %d", audits)
	}
}

func TestR37DomainSingleRoleLimitIndependentOfTotalCap(t *testing.T) {
	db := setupR37DomainTest(t, func(rs *adty_setting.ResellerSetting) {
		rs.MaxCustomDomainsPerSite = 5
	})
	site := createR37TestSite(t, db, 401, "delta")
	ctx := context.Background()

	// An empty site with no custom domains must reject single-domain binding for either role.
	_, err := AddAdtySiteDomainWithRole(ctx, 401, "web1.delta.org", model.AdtySiteDomainRoleSite)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "configure both website and API custom domains together first")
	_, err = AddAdtySiteDomainWithRole(ctx, 401, "api1.delta.org", model.AdtySiteDomainRoleBaseURL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "configure both website and API custom domains together first")
	var initialCount int64
	require.NoError(t, db.Model(&model.AdtySiteDomain{}).Where("site_id = ?", site.Id).Count(&initialCount).Error)
	assert.Zero(t, initialCount)

	// Bind the initial site + base_url pair together first.
	pair, err := AddAdtySiteDomainPair(ctx, 401, "web1.delta.org", "api1.delta.org")
	require.NoError(t, err)
	require.Len(t, pair, 2)
	firstSite := pair[0]
	assert.Equal(t, "web1.delta.org", firstSite.Hostname)
	assert.Equal(t, model.AdtySiteDomainRoleSite, firstSite.Role)
	assert.Equal(t, "api1.delta.org", pair[1].Hostname)
	assert.Equal(t, model.AdtySiteDomainRoleBaseURL, pair[1].Role)

	// Second site domain and second base_url domain must both be blocked by per-role slot caps
	// even though MaxCustomDomainsPerSite=5.
	_, err = AddAdtySiteDomainWithRole(ctx, 401, "web2.delta.org", model.AdtySiteDomainRoleSite)
	require.ErrorIs(t, err, ErrAdtyCustomDomainRoleLimit)
	_, err = AddAdtySiteDomainWithRole(ctx, 401, "api2.delta.org", model.AdtySiteDomainRoleBaseURL)
	require.ErrorIs(t, err, ErrAdtyCustomDomainRoleLimit)

	// After marking the existing site domain failed, retrying the SAME hostname reuses it.
	require.NoError(t, db.Model(&model.AdtySiteDomain{}).Where("id = ?", firstSite.Id).Update("status", model.AdtySiteDomainFailed).Error)
	retried, err := AddAdtySiteDomainWithRole(ctx, 401, "web1.delta.org", model.AdtySiteDomainRoleSite)
	require.NoError(t, err)
	assert.Equal(t, firstSite.Id, retried.Id)
	assert.Equal(t, model.AdtySiteDomainPending, retried.Status)

	// After removing it (while api1.delta.org still keeps the site non-empty), a replacement site domain is allowed,
	// and a subsequent site domain is again rejected by the single-slot cap.
	require.NoError(t, db.Model(&model.AdtySiteDomain{}).Where("id = ?", firstSite.Id).Update("status", model.AdtySiteDomainRemoved).Error)
	replacement, err := AddAdtySiteDomainWithRole(ctx, 401, "web2.delta.org", model.AdtySiteDomainRoleSite)
	require.NoError(t, err)
	assert.Equal(t, "web2.delta.org", replacement.Hostname)
	assert.Equal(t, model.AdtySiteDomainRoleSite, replacement.Role)

	_, err = AddAdtySiteDomainWithRole(ctx, 401, "web3.delta.org", model.AdtySiteDomainRoleSite)
	require.ErrorIs(t, err, ErrAdtyCustomDomainRoleLimit)
}

func TestR37DomainPairQuotaAndRateLimits(t *testing.T) {
	ctx := context.Background()

	t.Run("quota_less_than_two", func(t *testing.T) {
		db := setupR37DomainTest(t, func(rs *adty_setting.ResellerSetting) {
			rs.MaxCustomDomainsPerSite = 1
		})
		site := createR37TestSite(t, db, 501, "eps1")
		if _, err := AddAdtySiteDomainPair(ctx, 501, "web.eps1.org", "api.eps1.org"); !errors.Is(err, ErrAdtyCustomDomainLimit) {
			t.Fatalf("want ErrAdtyCustomDomainLimit, got %v", err)
		}
		var count int64
		db.Model(&model.AdtySiteDomain{}).Where("site_id = ?", site.Id).Count(&count)
		if count != 0 {
			t.Fatalf("expected 0 domains created on quota error, got %d", count)
		}
	})

	t.Run("hourly_rate_less_than_two", func(t *testing.T) {
		db := setupR37DomainTest(t, func(rs *adty_setting.ResellerSetting) {
			rs.CustomDomainCreatesPerHour = 1
		})
		site := createR37TestSite(t, db, 502, "eps2")
		if _, err := AddAdtySiteDomainPair(ctx, 502, "web.eps2.org", "api.eps2.org"); !errors.Is(err, ErrAdtyCustomDomainRate) {
			t.Fatalf("want ErrAdtyCustomDomainRate, got %v", err)
		}
		var count int64
		db.Model(&model.AdtySiteDomain{}).Where("site_id = ?", site.Id).Count(&count)
		if count != 0 {
			t.Fatalf("expected 0 domains created on rate error, got %d", count)
		}
	})
}

func TestCheckAdtySiteDomainNowOwnerTrigger(t *testing.T) {
	db := setupR37DomainTest(t, func(rs *adty_setting.ResellerSetting) {
		rs.DomainAutomationEnabled = true
	})
	oldCF := AdtyCloudflare
	oldProbe := adtyProbeDomainHTTPS
	oldCooldown := adtyOwnerDomainCheckCooldown
	t.Cleanup(func() {
		AdtyCloudflare = oldCF
		adtyProbeDomainHTTPS = oldProbe
		adtyOwnerDomainCheckCooldown = oldCooldown
	})

	adtyProbeDomainHTTPS = func(context.Context, *model.AdtySiteDomain) error { return nil }
	cfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones/test-zone-id/custom_hostnames":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/zones/test-zone-id/custom_hostnames":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result": map[string]any{
					"id":       "cf-host-check-1",
					"hostname": "api.checknow.org",
					"status":   "active",
					"ssl":      map[string]any{"status": "active"},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/zones/test-zone-id/custom_hostnames/cf-host-check-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result": map[string]any{
					"id":       "cf-host-check-1",
					"hostname": "api.checknow.org",
					"status":   "active",
					"ssl":      map[string]any{"status": "active"},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(cfSrv.Close)
	AdtyCloudflare = &AdtyCloudflareClient{
		BaseURL: cfSrv.URL,
		HTTP:    cfSrv.Client(),
		Token:   func() string { return "test-cf-token" },
	}

	site := createR37TestSite(t, db, 601, "checksite")
	createR37TestSite(t, db, 602, "othersite")
	ctx := context.Background()

	pair, err := AddAdtySiteDomainPair(ctx, 601, "www.checknow.org", "api.checknow.org")
	require.NoError(t, err)
	require.Len(t, pair, 2)
	apiDomain := pair[1]

	// Put the domain into long retry backoff (+1 hour) and stale last_checked_at.
	require.NoError(t, db.Model(&model.AdtySiteDomain{}).Where("id = ?", apiDomain.Id).Updates(map[string]any{
		"automation_state": "retry",
		"attempts":         6,
		"next_run_at":      time.Now().Add(time.Hour).Unix(),
		"last_checked_at":  time.Now().Add(-time.Minute).Unix(),
	}).Error)

	// 1. Immediate check bypasses 1-hour backoff and surfaces missing CNAME right away.
	checked, err := CheckAdtySiteDomainNow(ctx, 601, apiDomain.Id)
	require.NoError(t, err)
	assert.Equal(t, model.AdtySiteDomainPending, checked.Status)
	assert.Equal(t, "retry", checked.AutomationState)
	assert.Equal(t, "Domain CNAME is not pointing to the target yet", checked.LastError)
	assert.Empty(t, checked.LeaseToken)
	assert.Zero(t, checked.LeaseUntil)

	// 2. Immediate repeat call within cooldown is rejected.
	_, err = CheckAdtySiteDomainNow(ctx, 601, apiDomain.Id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Please wait")

	// 3. Cross-site owner is blocked.
	_, err = CheckAdtySiteDomainNow(ctx, 602, apiDomain.Id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Domain not found")

	// 4. Platform-managed domain is blocked.
	platformDom := model.AdtySiteDomain{
		SiteId:          site.Id,
		Hostname:        "checksite.platform.test",
		Role:            model.AdtySiteDomainRoleSite,
		ManagedBy:       "platform",
		DesiredState:    "serve",
		AutomationState: "retry",
		Status:          model.AdtySiteDomainPending,
		CreatedAt:       time.Now().Unix(),
	}
	require.NoError(t, db.Create(&platformDom).Error)
	_, err = CheckAdtySiteDomainNow(ctx, 601, platformDom.Id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Platform domains are managed by the platform")

	// 5. Once CNAME propagates and cooldown passes, CheckAdtySiteDomainNow activates immediately without any TXT record.
	adtyLookupOwnershipCNAME = func(ctx context.Context, name string) (string, error) {
		if name == apiDomain.Hostname {
			return "cname.platform.test.", nil
		}
		return "", &net.DNSError{IsNotFound: true}
	}
	require.NoError(t, db.Model(&model.AdtySiteDomain{}).Where("id = ?", apiDomain.Id).Update("last_checked_at", time.Now().Add(-time.Minute).Unix()).Error)

	activated, err := CheckAdtySiteDomainNow(ctx, 601, apiDomain.Id)
	require.NoError(t, err)
	assert.Equal(t, model.AdtySiteDomainActive, activated.Status)
	assert.Equal(t, "ready", activated.AutomationState)
	assert.Empty(t, activated.LastError)
	assert.Zero(t, activated.Attempts)
	assert.Equal(t, "cf-host-check-1", activated.CFHostnameId)
	assertVerifyRecordsHaveCNAMEOnly(t, *activated, "cname.platform.test")

	// 6. Failed domain cannot be checked directly.
	require.NoError(t, db.Model(&model.AdtySiteDomain{}).Where("id = ?", pair[0].Id).Update("status", model.AdtySiteDomainFailed).Error)
	_, err = CheckAdtySiteDomainNow(ctx, 601, pair[0].Id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed verification")
}

func TestR48CrossSiteReclaimStillRequiresTXTProof(t *testing.T) {
	db := setupR37DomainTest(t, func(rs *adty_setting.ResellerSetting) {
		rs.DomainAutomationEnabled = true
	})
	oldCF := AdtyCloudflare
	oldProbe := adtyProbeDomainHTTPS
	t.Cleanup(func() {
		AdtyCloudflare = oldCF
		adtyProbeDomainHTTPS = oldProbe
	})
	adtyProbeDomainHTTPS = func(context.Context, *model.AdtySiteDomain) error { return nil }
	cfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones/test-zone-id/custom_hostnames":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/zones/test-zone-id/custom_hostnames":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result": map[string]any{
					"id":                     "cf-reclaim-1",
					"hostname":               "www.reclaim.org",
					"status":                 "pending",
					"ownership_verification": map[string]any{"type": "txt", "name": "_cf-custom-hostname.www.reclaim.org", "value": "cf-optional-txt"},
					"ssl":                    map[string]any{"status": "pending_validation"},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/zones/test-zone-id/custom_hostnames/cf-reclaim-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result": map[string]any{
					"id":                     "cf-reclaim-1",
					"hostname":               "www.reclaim.org",
					"status":                 "pending",
					"ownership_verification": map[string]any{"type": "txt", "name": "_cf-custom-hostname.www.reclaim.org", "value": "cf-optional-txt"},
					"ssl":                    map[string]any{"status": "pending_validation"},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(cfSrv.Close)
	AdtyCloudflare = &AdtyCloudflareClient{
		BaseURL: cfSrv.URL,
		HTTP:    cfSrv.Client(),
		Token:   func() string { return "test-cf-token" },
	}

	site1 := createR37TestSite(t, db, 701, "former")
	createR37TestSite(t, db, 702, "claimer")
	ctx := context.Background()

	// Former site previously bound and removed www.reclaim.org (simulating a dangling CNAME).
	require.NoError(t, db.Create(&model.AdtySiteDomain{
		SiteId:    site1.Id,
		Hostname:  "www.reclaim.org",
		Role:      model.AdtySiteDomainRoleSite,
		ManagedBy: "owner",
		Status:    model.AdtySiteDomainRemoved,
	}).Error)

	// Dangling CNAME still points to platform CNAME target.
	adtyLookupOwnershipCNAME = func(ctx context.Context, name string) (string, error) {
		if name == "www.reclaim.org" || name == "api.reclaim.org" {
			return "cname.platform.test.", nil
		}
		return "", &net.DNSError{IsNotFound: true}
	}

	pair, err := AddAdtySiteDomainPair(ctx, 702, "www.reclaim.org", "api.reclaim.org")
	require.NoError(t, err)
	require.Len(t, pair, 2)

	// Reclaimed hostname must strictly require TXT + CNAME; fresh api.reclaim.org only needs CNAME.
	assertVerifyRecordsHaveCNAMEAndTXT(t, pair[0], "cname.platform.test")
	assertVerifyRecordsHaveCNAMEOnly(t, pair[1], "cname.platform.test")

	// CNAME alone must NOT allow cross-site takeover of www.reclaim.org.
	checked, err := CheckAdtySiteDomainNow(ctx, 702, pair[0].Id)
	require.NoError(t, err)
	assert.Equal(t, model.AdtySiteDomainPending, checked.Status)
	assert.Equal(t, "Domain ownership TXT is not verified", checked.LastError)
	assert.Empty(t, checked.CFHostnameId)

	// Once TXT is verified, CF Custom Hostname is created and verify_records does NOT expose Cloudflare's optional ownership TXT.
	adtyLookupOwnershipTXT = func(ctx context.Context, name string) ([]string, error) {
		if name == adtyOwnershipTXTName("www.reclaim.org") {
			return []string{pair[0].OwnershipToken}, nil
		}
		return nil, &net.DNSError{IsNotFound: true}
	}
	require.NoError(t, db.Model(&model.AdtySiteDomain{}).Where("id = ?", pair[0].Id).Update("last_checked_at", time.Now().Add(-time.Minute).Unix()).Error)
	verified, err := CheckAdtySiteDomainNow(ctx, 702, pair[0].Id)
	require.NoError(t, err)
	assert.Equal(t, "cf-reclaim-1", verified.CFHostnameId)
	assert.Equal(t, "validating", verified.AutomationState)
	assertVerifyRecordsHaveCNAMEOnly(t, *verified, "cname.platform.test")
}
