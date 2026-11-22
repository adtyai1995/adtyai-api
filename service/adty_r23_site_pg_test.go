package service

import (
	"context"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/setting/adty_setting"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func r23SiteDB(t *testing.T) *gorm.DB {
	dsn := os.Getenv("ADTY_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL 18 via ADTY_TEST_PG_DSN")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	pool, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	var version int
	require.NoError(t, admin.Raw("SHOW server_version_num").Scan(&version).Error)
	require.GreaterOrEqual(t, version, 180000)
	require.Less(t, version, 190000)
	t.Logf("server_version_num=%d", version)
	schema := fmt.Sprintf("closing_operations_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() { admin.Exec("DROP SCHEMA " + schema + " CASCADE") })
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		require.NoError(t, err)
		q := u.Query()
		q.Set("search_path", schema)
		q.Set("client_encoding", "UTF8")
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " client_encoding=UTF8 search_path=" + schema
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	pg, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Close() })

	previousDB, previousType := model.DB, common.MainDatabaseType()
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	t.Cleanup(func() { model.DB = previousDB; common.SetMainDatabaseType(previousType); InvalidateAdtySiteCache() })
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.TopUp{}, &model.Token{}))
	require.NoError(t, model.MigrateAdtyTablesOn(db))
	return db
}

func TestR23AdvertisedDomainsAndRemoval(t *testing.T) {
	db := r23SiteDB(t)
	site := model.AdtySite{OwnerUserId: 29, Subdomain: "r23", RootDomain: "example.test", Status: "active"}
	require.NoError(t, db.Create(&site).Error)
	for _, role := range []string{"site", "base_url"} {
		for _, owner := range []string{"platform", "owner"} {
			hostname := "a-" + role + ".example.test"
			if owner == "owner" {
				hostname = "z-" + role + ".example.test"
			}
			domain := model.AdtySiteDomain{SiteId: site.Id, Hostname: hostname, Role: role, ManagedBy: owner, Status: "active", DesiredState: "serve", AutomationState: "ready"}
			require.NoError(t, db.Create(&domain).Error)
		}
	}
	InvalidateAdtySiteCache()
	require.Equal(t, "z-site.example.test", adtyPreferredCustomHost(site.Id))
	require.Contains(t, AdtySiteAPIBaseURL(&site), "z-base_url.")
	var domain model.AdtySiteDomain
	require.NoError(t, db.First(&domain, "hostname = ?", "z-site.example.test").Error)
	require.NoError(t, removeAdtySiteDomain(context.Background(), &domain))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return adtyCloseDomainIntentsTx(tx, site.Id) }))
	require.NoError(t, db.First(&domain, domain.Id).Error)
	require.Equal(t, "removed", domain.DesiredState)
	require.Empty(t, adtyPreferredCustomHost(site.Id))
}
func TestR23CloseReplay(t *testing.T) {
	db := r23SiteDB(t)
	site := model.AdtySite{OwnerUserId: 33, Subdomain: "replay", RootDomain: "example.test", Status: "active"}
	require.NoError(t, db.Create(&site).Error)
	_, err := ScheduleAdtySiteClose(site.Id, 1, AdtySiteClosePlan{Reason: "retiring"})
	require.NoError(t, err)
	deadline := time.Now().Add(24 * time.Hour).Unix()
	require.NoError(t, db.Model(&site).Update("close_execute_at", deadline).Error)
	require.NoError(t, db.Model(&model.AdtySiteCloseMailBatch{}).Where("site_id = ?", site.Id).Update("execute_at", deadline).Error)
	replay, err := ScheduleAdtySiteClose(site.Id, 1, AdtySiteClosePlan{Reason: "retiring"})
	require.NoError(t, err)
	require.Equal(t, deadline, replay.CloseExecuteAt)
	require.NotZero(t, replay.CloseMailBatchId)
	revised, err := ScheduleAdtySiteClose(site.Id, 1, AdtySiteClosePlan{ExpectedExecuteAt: deadline, ExecuteAt: deadline + 86400, Reason: "new time"})
	require.NoError(t, err)
	require.NotEqual(t, revised.CloseMailBatchId, replay.CloseMailBatchId)
	original, err := ScheduleAdtySiteClose(site.Id, 1, AdtySiteClosePlan{Reason: "retiring"})
	require.NoError(t, err)
	require.Equal(t, replay.CloseMailBatchId, original.CloseMailBatchId)
	require.Equal(t, deadline, original.CloseExecuteAt)
	require.NoError(t, db.First(&site, site.Id).Error)
	require.Equal(t, revised.CloseExecuteAt, site.CloseExecuteAt)
}

func TestR23BrandMergeAndMailRecovery(t *testing.T) {
	db := r23SiteDB(t)
	site := model.AdtySite{OwnerUserId: 41, Subdomain: "brand", RootDomain: "example.test", Status: "active"}
	require.NoError(t, db.Create(&site).Error)
	set := func(key, value, expected string) error {
		return SaveAdtyBrandPatch(site.Id, site.OwnerUserId, map[string]string{key: value}, map[string]string{key: expected}, nil)
	}
	require.NoError(t, set("notice", "holiday", ""))
	require.NoError(t, set("support_contact", "support", ""))
	require.Error(t, set("notice", "stale", ""))
	require.NoError(t, db.First(&site, site.Id).Error)
	require.Contains(t, site.Brand, "holiday")
	require.Contains(t, site.Brand, "support")
	batch := model.AdtySiteCloseMailBatch{SiteId: site.Id, RetryStatus: "retryable"}
	require.NoError(t, db.Create(&batch).Error)
	now := time.Now().Unix()
	recipient := model.AdtySiteCloseMailRecipient{SiteId: site.Id, BatchId: batch.Id, UserId: 42, Status: "sending", AttemptCount: 5, Provider: "smtp", LeaseUntil: now + 10}
	require.NoError(t, db.Create(&recipient).Error)
	_, won, err := claimAdtySiteCloseMailRecipient(recipient.Id, now, 5, true)
	require.NoError(t, err)
	require.False(t, won)
	_, won, err = claimAdtySiteCloseMailRecipient(recipient.Id, now+11, 5, false)
	require.NoError(t, err)
	require.False(t, won)
	require.NoError(t, db.First(&recipient, recipient.Id).Error)
	require.Equal(t, "review", recipient.Status)
	require.NoError(t, ResolveAdtyCloseMailRecipient(site.Id, 1, recipient.Id, 5, "retry", "provider confirmed missing"))
	_, won, err = claimAdtySiteCloseMailRecipient(recipient.Id, now+12, 5, false)
	require.NoError(t, err)
	require.True(t, won)
	require.NoError(t, db.First(&recipient, recipient.Id).Error)
	require.Equal(t, 6, recipient.AttemptCount)
}

func TestR23DomainProbeIdentity(t *testing.T) {
	db := r23SiteDB(t)
	site := model.AdtySite{OwnerUserId: 66, Subdomain: "probe", RootDomain: "example.test", Status: "active"}
	require.NoError(t, db.Create(&site).Error)
	row := model.AdtySiteDomain{SiteId: site.Id, Hostname: "edge.example.test", Role: "base_url", DesiredState: "serve", Status: "pending"}
	require.NoError(t, db.Create(&row).Error)
	id, err := AdtyDomainProbeForHost(row.Hostname)
	require.NoError(t, err)
	require.Equal(t, row.SiteId, id.SiteID)
	for _, bad := range []string{"hello", fmt.Sprintf(`{"site_id":%d,"role":"site","hostname":"%s"}`, site.Id, row.Hostname), fmt.Sprintf(`{"site_id":%d,"role":"base_url","hostname":"%s"}`, site.Id+1, row.Hostname)} {
		require.Error(t, checkAdtyDomainProbeResponse(&http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(bad))}, &row))
	}
	good, err := common.Marshal(id)
	require.NoError(t, err)
	require.NoError(t, checkAdtyDomainProbeResponse(&http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(good)))}, &row))
}

func TestR23ReplacedSenderCleanup(t *testing.T) {
	db := r23SiteDB(t)
	previous := *adty_setting.GetResellerSetting()
	adty_setting.GetResellerSetting().SenderDomainEnabled = true
	defer func() { *adty_setting.GetResellerSetting() = previous }()
	owner := model.User{Username: "r23sender", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner).Error)
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "sender", RootDomain: "example.test", Status: "active"}
	require.NoError(t, db.Create(&site).Error)
	old := model.AdtySiteSenderDomain{SiteId: site.Id, Domain: "old-sender.example.org", Status: "verified", ResendDomainId: "remote-old"}
	require.NoError(t, db.Create(&old).Error)
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" && r.URL.Path == "/domains" {
			fmt.Fprint(w, `{"id":"remote-new","name":"new-sender.example.org","status":"verified"}`)
			return
		}
		if r.Method == "DELETE" && r.URL.Path == "/domains/remote-old" {
			attempts++
			if attempts == 1 {
				w.WriteHeader(504)
				fmt.Fprint(w, `{"message":"timeout"}`)
			} else {
				fmt.Fprint(w, "{}")
			}
			return
		}
		t.Errorf("unexpected provider request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}))
	defer srv.Close()
	prev := AdtyResend
	defer func() { AdtyResend = prev }()
	AdtyResend = &AdtyResendClient{BaseURL: srv.URL, HTTP: srv.Client(), APIKey: func() string { return "fixture" }}
	updated, err := AddAdtySiteSenderDomain(context.Background(), owner.Id, "new-sender.example.org")
	require.NoError(t, err)
	require.Equal(t, "remote-new", updated.ResendDomainId)
	var count int64
	require.NoError(t, db.Model(&model.AdtySenderDomainCleanup{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, RunAdtySenderDomainCheckOnce(context.Background(), time.Now().Add(time.Hour)))
	require.Equal(t, 2, attempts)
	require.NoError(t, db.Model(&model.AdtySenderDomainCleanup{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.First(&old, "site_id = ?", site.Id).Error)
	require.Equal(t, "remote-new", old.ResendDomainId)
	require.Equal(t, "verified", old.Status)
}

func TestR23RedirectReleasesTurnstileSlot(t *testing.T) {
	db := r23SiteDB(t)
	original := *resellerSetting()
	defer func() { *resellerSetting() = original }()
	cfg := resellerSetting()
	cfg.CloudflareAccountId = "account"
	cfg.TurnstileWidgets = []adty_setting.ResellerTurnstileWidget{{Id: "widget"}}
	old := model.AdtySiteDomain{SiteId: 1, Hostname: "old.example.org", LeaseToken: "lease", TurnstileWidget: "widget", CFHostnameId: "keep-cert", DesiredState: "redirect_only", Status: "active"}
	require.NoError(t, db.Create(&old).Error)
	hosts := []string{old.Hostname}
	for i := 1; i < adtyTurnstileDomainsPerWidget; i++ {
		hosts = append(hosts, fmt.Sprintf("full-%d.example.org", i))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "PUT" {
			var input struct {
				Domains []string `json:"domains"`
			}
			if err := common.DecodeJson(r.Body, &input); err != nil {
				t.Error(err)
			}
			hosts = input.Domains
		}
		body, err := common.Marshal(map[string]any{"success": true, "result": map[string]any{"name": "test", "mode": "managed", "domains": hosts}})
		if err != nil {
			t.Error(err)
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()
	prev := AdtyCloudflare
	defer func() { AdtyCloudflare = prev }()
	AdtyCloudflare = &AdtyCloudflareClient{BaseURL: server.URL, HTTP: server.Client(), Token: func() string { return "test" }}
	assign := func() string {
		var widget string
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			widget, err = adtyTurnstileAssign(context.Background(), tx, "next.example.org")
			return err
		}))
		return widget
	}
	require.Empty(t, assign())
	require.NoError(t, releaseRedirectWidget(context.Background(), &old))
	require.Equal(t, "widget", assign())
	require.NotContains(t, hosts, old.Hostname)
	require.NoError(t, db.First(&old, old.Id).Error)
	require.Empty(t, old.TurnstileWidget)
	require.Equal(t, "keep-cert", old.CFHostnameId)
}
