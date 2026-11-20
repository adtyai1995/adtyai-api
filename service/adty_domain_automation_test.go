package service

import (
	"context"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestDomainAutomationFreeSaaSRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &body))
		require.NotContains(t, body, "custom_metadata", "free SaaS must not require paid metadata")
		require.Equal(t, "shop.example.com", body["hostname"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"result":{"id":"cf-1","hostname":"shop.example.com"}}`))
	}))
	defer server.Close()
	client := &AdtyCloudflareClient{BaseURL: server.URL, HTTP: server.Client(), Token: func() string { return "test-token" }}
	_, err := client.createCustomHostname(context.Background(), "zone", &model.AdtySiteDomain{Hostname: "shop.example.com"})
	require.NoError(t, err)
}

func TestDomainAutomationLifecycleKeepsWebsiteAndCleansAPI(t *testing.T) {
	oldProbe := adtyProbeDomainHTTPS
	adtyProbeDomainHTTPS = func(context.Context, *model.AdtySiteDomain) error { return nil }
	t.Cleanup(func() { adtyProbeDomainHTTPS = oldProbe })
	db, fake := prepareDomainDB(t)
	cfg := adty_setting.GetResellerSetting()
	cfg.DomainAutomationEnabled = true
	cfg.AliyunDomain = "sites.example.com"
	cfg.DomainMainlandTarget = "fast.example.com"
	oldDNS := AdtyAliDNS
	t.Cleanup(func() { AdtyAliDNS = oldDNS })
	records := map[string]url.Values{}
	nextID := 0
	dns := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		w.Header().Set("Content-Type", "application/json")
		switch r.Form.Get("Action") {
		case "DescribeDomainRecords":
			items := []map[string]string{}
			for id, v := range records {
				items = append(items, map[string]string{"RecordId": id, "RR": v.Get("RR"), "Type": v.Get("Type"), "Value": v.Get("Value"), "Line": v.Get("Line")})
			}
			encoded, _ := common.Marshal(map[string]any{"DomainRecords": map[string]any{"Record": items}})
			_, _ = w.Write(encoded)
		case "AddDomainRecord":
			nextID++
			id := fmt.Sprint(nextID)
			records[id] = r.Form
			_, _ = fmt.Fprintf(w, `{"RecordId":%q}`, id)
		case "DescribeDomainRecordInfo":
			id := r.Form.Get("RecordId")
			v, ok := records[id]
			if !ok {
				_, _ = w.Write([]byte(`{"Code":"InvalidRecordId.NotFound"}`))
				return
			}
			encoded, _ := common.Marshal(map[string]string{"RecordId": id, "DomainName": v.Get("DomainName"), "RR": v.Get("RR"), "Type": v.Get("Type"), "Value": v.Get("Value"), "Line": v.Get("Line")})
			_, _ = w.Write(encoded)
		case "DeleteDomainRecord":
			delete(records, r.Form.Get("RecordId"))
			_, _ = w.Write([]byte(`{}`))
		case "UpdateDomainRecord":
			id := r.Form.Get("RecordId")
			records[id].Set("Value", r.Form.Get("Value"))
			_, _ = fmt.Fprintf(w, `{"RecordId":%q}`, id)
		default:
			t.Errorf("unexpected AliDNS action %s", r.Form.Get("Action"))
			w.WriteHeader(400)
		}
	}))
	defer dns.Close()
	AdtyAliDNS = func() *AdtyAliDNSClient {
		return &AdtyAliDNSClient{Endpoint: dns.URL, HTTP: dns.Client(), AccessKeyID: "test", AccessKeySecret: "test"}
	}
	_, site := openActiveSite(t, db, "lifecycle")
	now := time.Now()
	require.NoError(t, RunAdtyDomainAutomationOnce(t.Context(), now))
	require.Len(t, fake.hostnames, 2)
	fake.setStatus("cf-1", "active", "active")
	fake.setStatus("cf-2", "active", "active")
	require.NoError(t, RunAdtyDomainAutomationOnce(t.Context(), now.Add(6*time.Minute)))
	var domains []model.AdtySiteDomain
	require.NoError(t, db.Where("site_id = ?", site.Id).Order("role").Find(&domains).Error)
	require.Equal(t, model.AdtySiteDomainActive, domains[0].Status)
	require.Equal(t, model.AdtySiteDomainActive, domains[1].Status)
	// Reconcile a changed optimized target without duplicating exact records.
	cfg.DomainMainlandTarget = "new-fast.example.com"
	require.NoError(t, RunAdtyDomainAutomationOnce(t.Context(), now.Add(12*time.Minute)))
	for _, v := range records {
		if v.Get("Line") == "internal" {
			require.Equal(t, cfg.DomainMainlandTarget, v.Get("Value"))
		}
	}
	before := len(records)
	require.NoError(t, adtyCloseDomainIntentsTx(db, site.Id))
	require.NoError(t, RunAdtyDomainAutomationOnce(t.Context(), now.Add(18*time.Minute)))
	require.NoError(t, db.Where("site_id = ?", site.Id).Order("role").Find(&domains).Error)
	require.Equal(t, model.AdtySiteDomainRemoved, domains[0].Status)
	require.Equal(t, "redirect_only", domains[1].AutomationState)
	require.Len(t, fake.hostnames, 1)
	require.Less(t, len(records), before)
	require.NotEmpty(t, records)
	require.NoError(t, RunAdtyDomainAutomationOnce(t.Context(), now.Add(24*time.Minute)))
	require.Len(t, fake.hostnames, 1)
}

func TestDomainAutomationStableHostMapping(t *testing.T) {
	db, _ := prepareDomainDB(t)
	cfg := adty_setting.GetResellerSetting()
	cfg.DomainAutomationEnabled = true
	cfg.AliyunDomain = "sites.example.com"
	_, site := openActiveSite(t, db, "mapping")
	api := fmt.Sprintf("api-s%d.sites.example.com", site.Id)
	require.NoError(t, db.Model(&model.AdtySiteDomain{}).Where("site_id = ?", site.Id).Update("status", model.AdtySiteDomainActive).Error)
	InvalidateAdtySiteCache()
	require.Equal(t, api, loadAdtySiteSnapshot().defaultAPI[site.Id])
	require.NotNil(t, AdtySiteForHost(api))
	require.NoError(t, db.Model(&model.AdtySite{}).Where("id = ?", site.Id).Update("status", model.AdtySiteStatusClosed).Error)
	require.Equal(t, model.AdtySiteStatusClosed, AdtySiteForHost(api).Status, "another instance close must override the local cache")
	// A hostname reassigned on another instance cannot use the previous site's cache.
	require.NoError(t, db.Model(&model.AdtySiteDomain{}).Where("hostname = ?", api).Update("site_id", site.Id+100).Error)
	require.Nil(t, AdtySiteForHost(api))
}

func TestDomainAutomationCloseWinsLateActivation(t *testing.T) {
	db, fake := prepareDomainDB(t)
	cfg := adty_setting.GetResellerSetting()
	cfg.DomainAutomationEnabled = true
	cfg.AliyunDomain = "sites.example.com"
	_, site := openActiveSite(t, db, "late-close")
	var domain model.AdtySiteDomain
	require.NoError(t, db.Where("site_id = ? AND role = ?", site.Id, model.AdtySiteDomainRoleSite).First(&domain).Error)
	// An external hostname needs no platform DNS fixture; this test isolates the
	// generation transition while HTTPS is in flight.
	require.NoError(t, db.Model(&domain).Updates(map[string]any{"managed_by": "owner", "cf_hostname_id": "late", "provider_zone": cfg.CloudflareZoneId, "dns_root": "", "lease_token": "worker"}).Error)
	require.NoError(t, db.First(&domain, domain.Id).Error)
	fake.hostnames["late"] = &fakeHostname{hostname: domain.Hostname, status: "active", sslStatus: "active"}
	old := adtyProbeDomainHTTPS
	t.Cleanup(func() { adtyProbeDomainHTTPS = old })
	adtyProbeDomainHTTPS = func(context.Context, *model.AdtySiteDomain) error { return adtyCloseDomainIntentsTx(db, site.Id) }
	require.NoError(t, adtyReconcileDomain(t.Context(), &domain))
	var current model.AdtySiteDomain
	require.NoError(t, db.First(&current, domain.Id).Error)
	require.Equal(t, "redirect_only", current.DesiredState)
	require.Equal(t, "queued", current.AutomationState)
	require.Greater(t, current.Generation, domain.Generation)
}

func TestDomainAutomationAssignsStablePairAndCloseIntent(t *testing.T) {
	db, _ := prepareDomainDB(t)
	cfg := adty_setting.GetResellerSetting()
	cfg.DomainAutomationEnabled = true
	cfg.AliyunDomain = "sites.example.com"
	cfg.AliyunAccessKeyID = "id"
	cfg.AliyunAccessKeySecret = "secret"
	cfg.DomainMainlandTarget = "fast.example.com"
	_, site := openActiveSite(t, db, "auto-shop")
	var domains []model.AdtySiteDomain
	require.NoError(t, db.Where("site_id = ?", site.Id).Order("role").Find(&domains).Error)
	require.Len(t, domains, 2)
	require.Equal(t, fmt.Sprintf("api-s%d.sites.example.com", site.Id), domains[0].Hostname)
	require.Equal(t, "auto-shop.sites.example.com", domains[1].Hostname)
	require.Equal(t, "platform", domains[0].ManagedBy)
	require.Equal(t, "serve", domains[0].DesiredState)
	require.NoError(t, adtyCloseDomainIntentsTx(db, site.Id))
	require.NoError(t, db.Where("site_id = ?", site.Id).Order("role").Find(&domains).Error)
	require.Equal(t, "removed", domains[0].DesiredState)
	require.Equal(t, "redirect_only", domains[1].DesiredState)
	generation := domains[1].Generation
	require.NoError(t, adtyCloseDomainIntentsTx(db, site.Id))
	require.NoError(t, db.First(&domains[1], domains[1].Id).Error)
	require.Equal(t, generation, domains[1].Generation, "duplicate close callbacks must not invalidate another worker")
}

func TestDomainAutomationRefusesChangedDNS(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		if r.Form.Get("Action") != "DescribeDomainRecordInfo" {
			writes++
			t.Error("must not modify manual record")
		}
		_, _ = w.Write([]byte(`{"DomainName":"sites.example.com","RR":"shop","Type":"CNAME","Line":"internal","Value":"manual.example.com"}`))
	}))
	defer server.Close()
	client := &AdtyAliDNSClient{Endpoint: server.URL, HTTP: server.Client(), AccessKeyID: "test", AccessKeySecret: "secret"}
	record := AdtyDNSRecord{ID: "one", Name: "shop.sites.example.com", Type: "CNAME", Line: "internal", Value: "old.example.com"}
	require.ErrorContains(t, client.Delete(t.Context(), "sites.example.com", record), "deletion refused")
	_, err := client.Update(t.Context(), "sites.example.com", record, "new.example.com")
	require.ErrorContains(t, err, "update refused")
	require.Zero(t, writes)
}

func TestDomainAutomationOwnerCannotDeletePlatformDomain(t *testing.T) {
	db, _ := prepareDomainDB(t)
	cfg := adty_setting.GetResellerSetting()
	cfg.DomainAutomationEnabled = true
	cfg.AliyunDomain = "sites.example.com"
	owner, site := openActiveSite(t, db, "protected")
	var domain model.AdtySiteDomain
	require.NoError(t, db.Where("site_id = ?", site.Id).First(&domain).Error)
	require.ErrorContains(t, RemoveAdtySiteDomain(t.Context(), owner.Id, domain.Id), "managed by the platform")
}

func TestDomainAutomationAliDNSFormAndScope(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.NoError(t, r.ParseForm())
		require.Equal(t, "AddDomainRecord", r.Form.Get("Action"))
		require.Equal(t, "sites.example.com", r.Form.Get("DomainName"))
		require.Equal(t, "_acme-challenge.shop", r.Form.Get("RR"))
		require.Equal(t, "default", r.Form.Get("Line"))
		require.Equal(t, "TXT", r.Form.Get("Type"))
		require.Equal(t, "HMAC-SHA1", r.Form.Get("SignatureMethod"))
		require.NotEmpty(t, r.Form.Get("Signature"))
		_, _ = w.Write([]byte(`{"RecordId":"dns-1","RequestId":"request-1"}`))
	}))
	defer server.Close()
	client := &AdtyAliDNSClient{Endpoint: server.URL, HTTP: server.Client(), AccessKeyID: "test-id", AccessKeySecret: "test-secret"}
	record, err := client.Add(context.Background(), "sites.example.com", AdtyDNSRecord{Type: "TXT", Name: "_acme-challenge.shop.sites.example.com", Value: "validation", Line: "default"})
	require.NoError(t, err)
	require.Equal(t, "dns-1", record.ID)
	_, err = client.Add(context.Background(), "sites.example.com", AdtyDNSRecord{Type: "TXT", Name: "evil-sites.example.com", Value: "x", Line: "default"})
	require.Error(t, err)
	require.Equal(t, 1, calls)
}
