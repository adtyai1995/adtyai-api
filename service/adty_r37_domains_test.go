package service

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/glebarez/sqlite"
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
	oldDelay := adtyCustomHostnameRecordDelay
	model.DB = db
	adtyCustomHostnameRecordDelay = 0
	adtyLookupOwnershipTXT = func(ctx context.Context, name string) ([]string, error) {
		return nil, &net.DNSError{IsNotFound: true}
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
	assertVerifyRecordsHaveCNAMEAndTXT(t, domains[0], "cname.platform.test")
	assertVerifyRecordsHaveCNAMEAndTXT(t, domains[1], "cname.platform.test")

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
	createR37TestSite(t, db, 401, "delta")
	ctx := context.Background()

	firstSite, err := AddAdtySiteDomainWithRole(ctx, 401, "web1.delta.org", model.AdtySiteDomainRoleSite)
	if err != nil {
		t.Fatalf("first site domain: %v", err)
	}
	// Second site domain must be blocked even though MaxCustomDomainsPerSite=5.
	if _, err := AddAdtySiteDomainWithRole(ctx, 401, "web2.delta.org", model.AdtySiteDomainRoleSite); !errors.Is(err, ErrAdtyCustomDomainRoleLimit) {
		t.Fatalf("second site domain: want ErrAdtyCustomDomainRoleLimit, got %v", err)
	}
	// First base_url domain is still allowed.
	if _, err := AddAdtySiteDomainWithRole(ctx, 401, "api1.delta.org", model.AdtySiteDomainRoleBaseURL); err != nil {
		t.Fatalf("first base_url domain: %v", err)
	}
	// Second base_url domain must be blocked.
	if _, err := AddAdtySiteDomainWithRole(ctx, 401, "api2.delta.org", model.AdtySiteDomainRoleBaseURL); !errors.Is(err, ErrAdtyCustomDomainRoleLimit) {
		t.Fatalf("second base_url domain: want ErrAdtyCustomDomainRoleLimit, got %v", err)
	}

	// After marking the existing site domain failed, retrying the SAME hostname reuses it.
	if err := db.Model(&model.AdtySiteDomain{}).Where("id = ?", firstSite.Id).Update("status", model.AdtySiteDomainFailed).Error; err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	retried, err := AddAdtySiteDomainWithRole(ctx, 401, "web1.delta.org", model.AdtySiteDomainRoleSite)
	if err != nil {
		t.Fatalf("retry failed site domain: %v", err)
	}
	if retried.Id != firstSite.Id || retried.Status != model.AdtySiteDomainPending {
		t.Fatalf("expected reused pending row #%d, got %+v", firstSite.Id, retried)
	}

	// After removing it, a new site domain is allowed.
	if err := db.Model(&model.AdtySiteDomain{}).Where("id = ?", firstSite.Id).Update("status", model.AdtySiteDomainRemoved).Error; err != nil {
		t.Fatalf("mark removed: %v", err)
	}
	replacement, err := AddAdtySiteDomainWithRole(ctx, 401, "web2.delta.org", model.AdtySiteDomainRoleSite)
	if err != nil {
		t.Fatalf("replacement site domain: %v", err)
	}
	if replacement.Hostname != "web2.delta.org" || replacement.Role != model.AdtySiteDomainRoleSite {
		t.Fatalf("unexpected replacement domain: %+v", replacement)
	}
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
