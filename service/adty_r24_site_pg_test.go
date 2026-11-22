package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestR24SiteTierCodeBoundary(t *testing.T) {
	for _, code := range []string{strings.Repeat("站", 32), strings.Repeat("a", 31) + "🙂"} {
		require.NoError(t, ValidateAdtySiteTier(model.AdtySiteTier{Code: code, Name: "Standard"}))
		require.Error(t, ValidateAdtySiteTier(model.AdtySiteTier{Code: code + "b", Name: "Standard"}))
	}
}

func TestR24SiteDomains(t *testing.T) {
	db := r23SiteDB(t)
	prior := *resellerSetting()
	defer func() { *resellerSetting() = prior }()
	resellerSetting().TurnstileWidgets = []adty_setting.ResellerTurnstileWidget{{Id: "one", SiteKey: "first"}, {Id: "two", SiteKey: "second"}}
	resellerSetting().TurnstileWidgetSecret = map[string]string{"one": "a", "two": "b"}
	site := model.AdtySite{Subdomain: "shop", RootDomain: "example.test", OwnerUserId: 17, Status: "active"}
	require.NoError(t, db.Create(&site).Error)
	web := model.AdtySiteDomain{SiteId: site.Id, Hostname: "shop.example.test", ManagedBy: "platform", Role: "site", Status: "active", DesiredState: "serve", TurnstileWidget: "two"}
	require.NoError(t, db.Create(&web).Error)
	t.Run("default website bound pair", func(t *testing.T) {
		require.Equal(t, AdtyTurnstileChoice{SiteKey: "second", Secret: "b"}, AdtyTurnstileFor(&site, "SHOP.EXAMPLE.TEST:443"))
	})
	api := model.AdtySiteDomain{SiteId: site.Id, Hostname: "api.example.test", ManagedBy: "platform", Role: "base_url", Status: "pending", DesiredState: "serve"}
	require.NoError(t, db.Create(&api).Error)
	for _, stage := range []struct {
		status, desired string
		ready           bool
	}{{"pending", "serve", false}, {"active", "serve", true}, {"active", "removed", false}} {
		require.NoError(t, db.Model(&api).Updates(map[string]any{"status": stage.status, "desired_state": stage.desired}).Error)
		InvalidateAdtySiteCache()
		require.Equal(t, stage.ready, AdtySiteForHost(api.Hostname) != nil)
		require.Equal(t, stage.ready, AdtySiteAPIBaseURL(&site) != "")
	}
}

func TestR24SiteLegacyAPIWidget(t *testing.T) {
	db := r23SiteDB(t)
	prior := *resellerSetting()
	defer func() { *resellerSetting() = prior }()
	cfg := resellerSetting()
	cfg.CloudflareAccountId = "fixture"
	cfg.TurnstileWidgets = []adty_setting.ResellerTurnstileWidget{{Id: "first"}}
	api := model.AdtySiteDomain{Hostname: "api.example.test", SiteId: 55, Role: "base_url", Status: "active", TurnstileWidget: "first"}
	require.NoError(t, db.Create(&api).Error)
	web := model.AdtySiteDomain{Hostname: "web.example.test", SiteId: 55, Role: "site", Status: "active"}
	require.NoError(t, db.Create(&web).Error)
	hosts := []string{api.Hostname}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			var in struct{ Domains []string }
			if err := common.DecodeJson(r.Body, &in); err != nil {
				t.Error(err)
			}
			hosts = in.Domains
		}
		body, err := common.Marshal(map[string]any{"success": true, "result": map[string]any{"name": "test", "mode": "managed", "domains": hosts}})
		if err != nil {
			t.Error(err)
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	previous := AdtyCloudflare
	defer func() { AdtyCloudflare = previous }()
	AdtyCloudflare = &AdtyCloudflareClient{BaseURL: srv.URL, HTTP: srv.Client(), Token: func() string { return "test" }}
	for _, row := range []*model.AdtySiteDomain{&api, &web} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return adtyAssignCheckedDomainTurnstile(context.Background(), tx, row) }))
	}
	require.ElementsMatch(t, []string{web.Hostname}, hosts)
	require.NoError(t, db.First(&api, api.Id).Error)
	require.Empty(t, api.TurnstileWidget)
}

func TestR24SiteLegacySchedule(t *testing.T) {
	db := r23SiteDB(t)
	deadline := time.Now().Add(72 * time.Hour).Unix()
	site := model.AdtySite{OwnerUserId: 17, Status: "closing", Subdomain: "legacy", RootDomain: "example.test", CloseExecuteAt: deadline}
	require.NoError(t, db.Create(&site).Error)
	batch := model.AdtySiteCloseMailBatch{SiteId: site.Id, Revision: 1}
	require.NoError(t, db.Create(&batch).Error)
	_, err := ScheduleAdtySiteClose(site.Id, 1, AdtySiteClosePlan{ExpectedExecuteAt: deadline, ExecuteAt: deadline + 3600, Reason: "rescheduled"})
	require.NoError(t, err)
	original, err := ScheduleAdtySiteClose(site.Id, 1, AdtySiteClosePlan{Reason: "initial"})
	require.NoError(t, err)
	require.Equal(t, batch.Id, original.CloseMailBatchId)
	require.Equal(t, deadline, original.CloseExecuteAt)
}

func TestR24SiteHistoricalRemoval(t *testing.T) {
	db := r23SiteDB(t)
	owner := 25
	old := model.AdtySite{OwnerUserId: owner, Subdomain: "old", RootDomain: "example.test", Status: "closed"}
	require.NoError(t, db.Create(&old).Error)
	next := model.AdtySite{OwnerUserId: owner, Subdomain: "new", RootDomain: "example.test", Status: "active"}
	require.NoError(t, db.Create(&next).Error)
	domain := model.AdtySiteDomain{SiteId: old.Id, Hostname: "shop.example.org", Role: "site", ManagedBy: "owner", Status: "active", DesiredState: "redirect_only", AutomationState: "ready"}
	require.NoError(t, db.Create(&domain).Error)
	platform := model.AdtySiteDomain{SiteId: old.Id, Hostname: "old.example.test", Role: "site", ManagedBy: "platform", Status: "active", DesiredState: "redirect_only"}
	require.NoError(t, db.Create(&platform).Error)
	require.Error(t, RemoveAdtyArchivedWebsiteDomain(context.Background(), owner, old.Id, platform.Id))
	listed, err := ListAdtyArchivedWebsiteDomains(owner, old.Id)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, domain.Id, listed[0].Id)
	require.Error(t, RemoveAdtyArchivedWebsiteDomain(context.Background(), owner, next.Id, domain.Id))
	require.Error(t, RemoveAdtyArchivedWebsiteDomain(context.Background(), 99, old.Id, domain.Id))
	require.NoError(t, RemoveAdtyArchivedWebsiteDomain(context.Background(), owner, old.Id, domain.Id))
	require.NoError(t, db.First(&domain, domain.Id).Error)
	require.Equal(t, "removed", domain.DesiredState)
}

func TestR24SiteDuplicateCreationPreservesAssignedTerms(t *testing.T) {
	db := r23SiteDB(t)
	tier := model.AdtySiteTier{Code: "standard", Name: "Standard", DepositUSD: decimal.NewFromInt(51), Enabled: true}
	require.NoError(t, CreateAdtySiteTiers([]model.AdtySiteTier{tier}))
	site := model.AdtySite{OwnerUserId: 77, Subdomain: "test", RootDomain: "example.test", TierCode: tier.Code}
	require.NoError(t, db.Create(&site).Error)
	wallet := model.AdtySiteWallet{SiteId: site.Id, Kind: model.AdtySiteWalletMargin, RequiredBalance: tier.DepositUSD}
	require.NoError(t, db.Create(&wallet).Error)
	tier.Name = "replacement"
	tier.DepositUSD = decimal.NewFromInt(100)
	require.ErrorIs(t, CreateAdtySiteTiers([]model.AdtySiteTier{tier}), ErrAdtySiteTierConflict)
	require.NoError(t, db.First(&tier, "code = ?", "standard").Error)
	require.Equal(t, "Standard", tier.Name)
	require.NoError(t, db.First(&wallet, "site_id = ?", site.Id).Error)
	require.Equal(t, "51", wallet.RequiredBalance.String())
}
func TestR24SiteResendUnknown(t *testing.T) {
	db := r23SiteDB(t)
	site := model.AdtySite{OwnerUserId: 54, Subdomain: "mail", RootDomain: "example.test", Status: "closing"}
	require.NoError(t, db.Create(&site).Error)
	b := model.AdtySiteCloseMailBatch{SiteId: site.Id}
	require.NoError(t, db.Create(&b).Error)
	r := model.AdtySiteCloseMailRecipient{SiteId: site.Id, BatchId: b.Id, UserId: 171, Email: "test@example.org", Status: "pending"}
	require.NoError(t, db.Create(&r).Error)
	old := AdtyResend
	defer func() { AdtyResend = old }()
	common.OptionMapRWMutex.Lock()
	prev := common.OptionMap
	common.OptionMap = map[string]string{"EmailProvider": "resend", "ResendApiKey": "fixture"}
	common.OptionMapRWMutex.Unlock()
	defer func() { common.OptionMapRWMutex.Lock(); common.OptionMap = prev; common.OptionMapRWMutex.Unlock() }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer srv.Close()
	AdtyResend = &AdtyResendClient{BaseURL: srv.URL, HTTP: srv.Client(), APIKey: func() string { return "fixture" }}
	require.NoError(t, sendAdtySiteCloseMigrationMail(&site, true))
	require.NoError(t, db.First(&r, r.Id).Error)
	require.Equal(t, "review", r.Status)
	common.OptionMapRWMutex.Lock()
	common.OptionMap["EmailProvider"] = "smtp"
	common.OptionMapRWMutex.Unlock()
	_, claimed, err := claimAdtySiteCloseMailRecipient(r.Id, time.Now().Add(25*time.Hour).Unix(), 5, true)
	require.NoError(t, err)
	require.False(t, claimed)
	var attempt model.AdtySiteCloseMailAttempt
	require.NoError(t, db.First(&attempt, "recipient_id = ?", r.Id).Error)
	require.Equal(t, "resend", attempt.Provider)
	require.Equal(t, "review", attempt.Outcome)
}

func TestR24SiteOrphanedNewSenderCleanup(t *testing.T) {
	db := r23SiteDB(t)
	cfg := resellerSetting()
	prior := cfg.SenderDomainEnabled
	cfg.SenderDomainEnabled = true
	defer func() { cfg.SenderDomainEnabled = prior }()
	site := model.AdtySite{OwnerUserId: 75, Subdomain: "sender", RootDomain: "example.test", Status: "active"}
	require.NoError(t, db.Create(&site).Error)
	old := model.AdtySiteSenderDomain{SiteId: site.Id, Domain: "old.example.org", Status: "verified", ResendDomainId: "remote-old"}
	require.NoError(t, db.Create(&old).Error)
	require.NoError(t, db.Exec("ALTER TABLE adty_site_sender_domains ADD CHECK (resend_domain_id <> 'remote-new')").Error)
	deletes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			fmt.Fprint(w, `{"id":"remote-new","status":"verified"}`)
			return
		}
		if r.Method == "DELETE" && r.URL.Path == "/domains/remote-new" {
			deletes++
			if deletes == 1 {
				w.WriteHeader(504)
			}
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}))
	defer srv.Close()
	previous := AdtyResend
	defer func() { AdtyResend = previous }()
	AdtyResend = &AdtyResendClient{BaseURL: srv.URL, HTTP: srv.Client(), APIKey: func() string { return "test" }}
	_, err := AddAdtySiteSenderDomain(context.Background(), 75, "new.example.org")
	require.Error(t, err)
	var cleanup model.AdtySenderDomainCleanup
	require.NoError(t, db.First(&cleanup, "remote_id = ?", "remote-new").Error)
	require.NoError(t, cleanReplacedSenderDomains(context.Background(), time.Now().Add(time.Hour).Unix(), 0))
	require.Equal(t, 2, deletes)
	require.NoError(t, db.First(&old, "site_id = ?", site.Id).Error)
	require.Equal(t, "remote-old", old.ResendDomainId)
	require.True(t, errors.Is(db.First(&cleanup, "remote_id = ?", "remote-new").Error, gorm.ErrRecordNotFound))
}

func TestR24SiteMigrationNormalizesHostAndPreservesUnknownDeadline(t *testing.T) {
	db := r23SiteDB(t)
	option := model.Option{Key: adty_setting.ResellerSettingModule + ".unknown_host_mode", Value: "main"}
	require.NoError(t, db.Create(&option).Error)
	site := model.AdtySite{OwnerUserId: 23, Status: "closing", Subdomain: "legacy", RootDomain: "example.test", CloseExecuteAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, db.Create(&site).Error)
	b := model.AdtySiteCloseMailBatch{SiteId: site.Id, Revision: 1}
	second := model.AdtySiteCloseMailBatch{SiteId: site.Id, Revision: 2, ExecuteAt: site.CloseExecuteAt}
	require.NoError(t, db.Create(&b).Error)
	require.NoError(t, db.Create(&second).Error)
	require.NoError(t, model.MigrateAdtyTablesOn(db))
	require.NoError(t, db.First(&option).Error)
	require.Equal(t, "reject", option.Value)
	replay, err := ScheduleAdtySiteClose(site.Id, 1, AdtySiteClosePlan{})
	require.NoError(t, err)
	require.Equal(t, b.Id, replay.CloseMailBatchId)
	require.Zero(t, replay.CloseExecuteAt)
}

func TestR24SitePreviewMatchesClosureIntents(t *testing.T) {
	db := r23SiteDB(t)
	for _, target := range []struct{ role, desired, want string }{{"site", "serve", "redirect_only"}, {"site", "removed", "removed"}, {"base_url", "serve", "removed"}} {
		d := model.AdtySiteDomain{SiteId: 9, Hostname: target.role + "-" + target.desired + ".example.test", Role: target.role, DesiredState: target.desired, Status: "active"}
		require.NoError(t, db.Create(&d).Error)
		require.Equal(t, target.want, AdtyDomainCloseAction(d))
	}
	require.NoError(t, adtyCloseDomainIntentsTx(db, 9))
	var rows []model.AdtySiteDomain
	require.NoError(t, db.Where("site_id = ?", 9).Order("id").Find(&rows).Error)
	require.Equal(t, "redirect_only", rows[0].DesiredState)
	require.Equal(t, "removed", rows[1].DesiredState)
	require.Equal(t, "removed", rows[2].DesiredState)
}
