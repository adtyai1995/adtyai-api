package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 站点品牌、发件人、发件域名（任务 11.3）。

// fakeResendDomains 是假的 Resend /domains：POST 建域名（quotaFull 时返回 403 名额已满），GET 返回 status。
type fakeResendDomains struct {
	mu        sync.Mutex
	status    map[string]string
	deleted   []string
	quotaFull bool
	notFound  map[string]bool
	onGet     func()
}

func newFakeResendDomains(t *testing.T) *fakeResendDomains {
	t.Helper()
	fake := &fakeResendDomains{status: map[string]string{}, notFound: map[string]bool{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		require.Equal(t, "Bearer re_test", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/domains":
			if fake.quotaFull {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `{"statusCode":403,"name":"validation_error","message":"You have reached your domain limit."}`)
				return
			}
			var req struct {
				Name string `json:"name"`
			}
			require.NoError(t, common.Unmarshal(body, &req))
			id := "dom-" + req.Name
			fake.status[id] = "not_started"
			_, _ = io.WriteString(w, `{"id":"`+id+`","name":"`+req.Name+`","status":"not_started","records":[`+
				`{"record":"SPF","name":"send","type":"MX","value":"feedback-smtp.us-east-1.amazonses.com","ttl":"Auto","status":"not_started","priority":10},`+
				`{"record":"DKIM","name":"resend._domainkey","type":"TXT","value":"p=MIGf","ttl":"Auto","status":"not_started"}]}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/domains/"):
			id := strings.TrimPrefix(r.URL.Path, "/domains/")
			status := fake.status[id]
			hook := fake.onGet
			fake.mu.Unlock()
			if hook != nil {
				hook()
			}
			fake.mu.Lock()
			_, _ = io.WriteString(w, `{"id":"`+id+`","status":"`+status+`","records":[]}`)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/domains/"):
			id := strings.TrimPrefix(r.URL.Path, "/domains/")
			if fake.notFound[id] {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"statusCode":404,"name":"not_found","message":"Domain not found"}`)
				return
			}
			delete(fake.status, id)
			fake.deleted = append(fake.deleted, id)
			_, _ = io.WriteString(w, `{"object":"domain","id":"x","deleted":true}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	old := AdtyResend
	AdtyResend = &AdtyResendClient{BaseURL: server.URL, HTTP: server.Client(), APIKey: func() string { return "re_test" }}
	t.Cleanup(func() { AdtyResend = old })
	return fake
}

func (f *fakeResendDomains) set(id, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[id] = status
}

// prepareMailDB：分站库，打开站长发件，平台发件地址设好，主站地址 https://adtyai.com。
func prepareMailDB(t *testing.T) (*gorm.DB, *fakeResendDomains) {
	t.Helper()
	db := prepareSiteDB(t)
	adty_setting.GetResellerSetting().SenderDomainEnabled = true
	common.OptionMap["ResendApiKey"] = "re_test"
	previousProvider := common.OptionMap["EmailProvider"]
	common.OptionMap["EmailProvider"] = "resend"
	t.Cleanup(func() { common.OptionMap["EmailProvider"] = previousProvider })
	common.OptionMap["ResendFrom"] = "ADTY AI <noreply@mail.adtyai.com>"
	oldAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://adtyai.com"
	t.Cleanup(func() { system_setting.ServerAddress = oldAddress })
	return db, newFakeResendDomains(t)
}

func setSiteBrand(t *testing.T, db *gorm.DB, site *model.AdtySite, brand string) {
	t.Helper()
	require.NoError(t, db.Model(&model.AdtySite{}).Where("id = ?", site.Id).Update("brand", brand).Error)
	InvalidateAdtySiteCache()
}

func TestSenderFallsBackWhenDomainUnverified(t *testing.T) {
	db, fake := prepareMailDB(t)
	owner, site := openActiveSite(t, db, "sender")
	setSiteBrand(t, db, site, `{"name":"Sender AI"}`)

	// 没加发件域名：站名 + 平台发件地址
	assert.Equal(t, "Sender AI <noreply@mail.adtyai.com>", AdtySenderFor(AdtySiteById(site.Id)))

	domain, err := AddAdtySiteSenderDomain(context.Background(), owner.Id, "mail.sender.org")
	require.NoError(t, err)
	assert.Equal(t, model.AdtySenderDomainPending, domain.Status)
	assert.Contains(t, domain.Records, "resend._domainkey", "站长要看到 Resend 给的 DNS 记录")
	// 建好但没验证：仍用平台地址
	assert.Equal(t, "Sender AI <noreply@mail.adtyai.com>", AdtySenderFor(AdtySiteById(site.Id)))

	fake.set("dom-mail.sender.org", "verified")
	require.NoError(t, RunAdtySenderDomainCheckOnce(context.Background(), time.Now()))
	assert.Equal(t, "Sender AI <noreply@mail.sender.org>", AdtySenderFor(AdtySiteById(site.Id)))

	// 主站
	assert.Equal(t, "ADTY AI <noreply@mail.adtyai.com>", AdtySenderFor(nil))
}

func TestSenderUsesActualSMTPAddressEvenWithVerifiedDomain(t *testing.T) {
	db, fake := prepareMailDB(t)
	owner, site := openActiveSite(t, db, "mailshop")
	_, err := AddAdtySiteSenderDomain(context.Background(), owner.Id, "mail.shop.example")
	require.NoError(t, err)
	fake.set("dom-mail.shop.example", "verified")
	require.NoError(t, RunAdtySenderDomainCheckOnce(context.Background(), time.Now()))
	oldFrom := common.SMTPFrom
	t.Cleanup(func() { common.SMTPFrom = oldFrom })
	common.SMTPFrom = "accounts@shared.example"
	for _, provider := range []string{"smtp", ""} {
		common.OptionMap["EmailProvider"] = provider
		assert.Equal(t, "mailshop <accounts@shared.example>", AdtySenderFor(site), provider)
	}
}

func TestAccountEmailWithoutOwnedLogoKeepsSiteName(t *testing.T) {
	for _, path := range []string{"", "/uploads/site-assets/site-11-0123456789abcdef0123456789abcdef.png", "https://other.example/logo.png"} {
		site := &model.AdtySite{Id: 1, Brand: `{"name":"Shop & Co","logo_url":"` + path + `"}`}
		body := AdtyRenderMail(site, AdtyMailMessage{Title: "Confirm", Intro: "Use <this> & only this", Code: "012345", ActionURL: "javascript:bad", ActionLabel: "Do not show"})
		assert.Contains(t, body, `lang="en"`)
		assert.Contains(t, body, "Shop &amp; Co")
		assert.Contains(t, body, "Use &lt;this&gt; &amp; only this")
		assert.Contains(t, body, "<strong>012345</strong>")
		assert.NotContains(t, body, "Do not show")
		assert.NotContains(t, body, "<img")
	}
}

func TestSenderDomainCheckDoesNotOverwriteAChangedRow(t *testing.T) {
	db, fake := prepareMailDB(t)
	owner, site := openActiveSite(t, db, "race")
	domain, err := AddAdtySiteSenderDomain(context.Background(), owner.Id, "mail.race.org")
	require.NoError(t, err)
	fake.set(domain.ResendDomainId, "verified")
	fake.onGet = func() {
		require.NoError(t, db.Model(&model.AdtySiteSenderDomain{}).Where("site_id = ?", site.Id).Updates(map[string]any{
			"status": model.AdtySenderDomainDisabled, "resend_domain_id": "dom-new", "domain": "mail.new.org",
		}).Error)
	}

	require.NoError(t, RunAdtySenderDomainCheckOnce(context.Background(), time.Now()))

	var stored model.AdtySiteSenderDomain
	require.NoError(t, db.First(&stored, "site_id = ?", site.Id).Error)
	assert.Equal(t, model.AdtySenderDomainDisabled, stored.Status)
	assert.Equal(t, "dom-new", stored.ResendDomainId)
	assert.Contains(t, AdtySenderFor(AdtySiteById(site.Id)), "<noreply@mail.adtyai.com>")
}

func TestSenderDomainDisabledByAdmin(t *testing.T) {
	db, fake := prepareMailDB(t)
	owner, site := openActiveSite(t, db, "disable")
	_, err := AddAdtySiteSenderDomain(context.Background(), owner.Id, "mail.disable.org")
	require.NoError(t, err)
	fake.set("dom-mail.disable.org", "verified")
	require.NoError(t, RunAdtySenderDomainCheckOnce(context.Background(), time.Now()))
	require.Contains(t, AdtySenderFor(AdtySiteById(site.Id)), "@mail.disable.org")

	require.NoError(t, DisableAdtySiteSenderDomain(context.Background(), site.Id, 1))

	assert.Equal(t, []string{"dom-mail.disable.org"}, fake.deleted, "从 Resend 删除")
	assert.Contains(t, AdtySenderFor(AdtySiteById(site.Id)), "<noreply@mail.adtyai.com>", "回退到平台发件地址")
	var stored model.AdtySiteSenderDomain
	require.NoError(t, db.First(&stored, "site_id = ?", site.Id).Error)
	assert.Equal(t, model.AdtySenderDomainDisabled, stored.Status)
	assert.Equal(t, 1, stored.DisabledBy)

	// 站长发件总开关关了，已验证的也不用
	adty_setting.GetResellerSetting().SenderDomainEnabled = false
	_, err = AddAdtySiteSenderDomain(context.Background(), owner.Id, "mail.disable.org")
	assert.ErrorIs(t, err, ErrAdtySenderDomainOff)
}

func TestSenderUsesPlatformWhenSwitchedOff(t *testing.T) {
	db, fake := prepareMailDB(t)
	owner, site := openActiveSite(t, db, "switched")
	_, err := AddAdtySiteSenderDomain(context.Background(), owner.Id, "mail.switched.org")
	require.NoError(t, err)
	fake.set("dom-mail.switched.org", "verified")
	require.NoError(t, RunAdtySenderDomainCheckOnce(context.Background(), time.Now()))

	adty_setting.GetResellerSetting().SenderDomainEnabled = false
	assert.Contains(t, AdtySenderFor(AdtySiteById(site.Id)), "<noreply@mail.adtyai.com>")
}

func TestSenderDomainQuotaFull(t *testing.T) {
	db, fake := prepareMailDB(t)
	owner, _ := openActiveSite(t, db, "quota")
	fake.quotaFull = true

	_, err := AddAdtySiteSenderDomain(context.Background(), owner.Id, "mail.quota.org")
	assert.ErrorIs(t, err, ErrAdtySenderDomainQuotaFull)
}

func TestSenderDomainRejectsOwnAndInvalidDomains(t *testing.T) {
	db, _ := prepareMailDB(t)
	owner, _ := openActiveSite(t, db, "bad")
	for _, domain := range []string{"adtyai.com", "x.sites.example.com", "bad_domain.org", "nodot"} {
		_, err := AddAdtySiteSenderDomain(context.Background(), owner.Id, domain)
		assert.Error(t, err, domain)
	}
}

func TestBrandFieldsLengthAndPlainText(t *testing.T) {
	ok := AdtySiteBrandInput{Name: "Shop AI", Tagline: "Fast", Notice: "Hello", SupportContact: "t.me/shop", LogoURL: "/uploads/site-assets/1.png"}
	brand, err := NormalizeAdtySiteBrand(ok)
	require.NoError(t, err)
	assert.Equal(t, "Shop AI", brand.Name)

	cases := []struct {
		name  string
		input AdtySiteBrandInput
	}{
		{"站名超过 40 字", AdtySiteBrandInput{Name: strings.Repeat("名", 41)}},
		{"标语超过 80 字", AdtySiteBrandInput{Name: "A", Tagline: strings.Repeat("x", 81)}},
		{"公告超过 500 字", AdtySiteBrandInput{Name: "A", Notice: strings.Repeat("x", 501)}},
		{"客服超过 200 字", AdtySiteBrandInput{Name: "A", SupportContact: strings.Repeat("x", 201)}},
		{"Logo 不是站点资源", AdtySiteBrandInput{Name: "A", LogoURL: "javascript:alert(1)"}},
		{"Logo 是外链", AdtySiteBrandInput{Name: "A", LogoURL: "https://evil.example/x.png"}},
	}
	for _, tc := range cases {
		_, err := NormalizeAdtySiteBrand(tc.input)
		assert.Error(t, err, tc.name)
	}

	// 按纯文本存：HTML 原样保留为文本（前端按文本渲染），控制字符去掉，前后空白去掉
	brand, err = NormalizeAdtySiteBrand(AdtySiteBrandInput{Name: "  <b>Shop</b>\x00 ", Notice: "<script>x</script>"})
	require.NoError(t, err)
	assert.Equal(t, "<b>Shop</b>", brand.Name)
	assert.Equal(t, "<script>x</script>", brand.Notice)
	// 用在邮件 HTML 里时要转义
	assert.Contains(t, AdtyRenderMail(&model.AdtySite{Subdomain: "s", Brand: `{"name":"<b>Shop</b>"}`}, AdtyMailMessage{Title: "Account notice"}), "&lt;b&gt;Shop&lt;/b&gt;")
}

func TestBrandForSiteAndMain(t *testing.T) {
	db, _ := prepareMailDB(t)
	_, site := openActiveSite(t, db, "brand")
	setSiteBrand(t, db, site, `{"name":"Brand AI","tagline":"T","logo_url":"/uploads/site-assets/b.png"}`)

	brand := AdtyBrandFor(AdtySiteById(site.Id))
	assert.Equal(t, "Brand AI", brand.Name)
	assert.Equal(t, "https://brand.sites.example.com", brand.BaseURL)

	main := AdtyBrandFor(nil)
	assert.Equal(t, common.SystemName, main.Name)
	assert.Equal(t, "https://adtyai.com", main.BaseURL)

	// 没填站名时用子域名
	setSiteBrand(t, db, site, "")
	assert.Equal(t, "brand", AdtyBrandFor(AdtySiteById(site.Id)).Name)
}

func TestReplaceSenderDomainRollsBackResendWhenSaveFails(t *testing.T) {
	db, fake := prepareMailDB(t)
	owner, site := openActiveSite(t, db, "replace")
	oldDomain, err := AddAdtySiteSenderDomain(context.Background(), owner.Id, "mail.old-sender.org")
	require.NoError(t, err)

	const callback = "adty-test-fail-sender-replace"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "adty_site_sender_domains" {
			tx.AddError(errors.New("forced sender domain write failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })

	_, err = AddAdtySiteSenderDomain(context.Background(), owner.Id, "mail.new-sender.org")
	require.ErrorContains(t, err, "forced sender domain write failure")
	require.NoError(t, db.Callback().Update().Remove(callback))

	var stored model.AdtySiteSenderDomain
	require.NoError(t, db.Where("site_id = ?", site.Id).First(&stored).Error)
	assert.Equal(t, "mail.old-sender.org", stored.Domain)
	assert.Equal(t, oldDomain.ResendDomainId, stored.ResendDomainId)
	assert.NotContains(t, fake.deleted, oldDomain.ResendDomainId, "旧域名还在 Resend 上")
	assert.Contains(t, fake.deleted, "dom-mail.new-sender.org", "刚建的新域名要删掉")
	_, oldStillThere := fake.status[oldDomain.ResendDomainId]
	assert.True(t, oldStillThere)
	_, newLeftBehind := fake.status["dom-mail.new-sender.org"]
	assert.False(t, newLeftBehind)

	replaced, err := AddAdtySiteSenderDomain(context.Background(), owner.Id, "mail.new-sender.org")
	require.NoError(t, err)
	assert.Equal(t, "mail.new-sender.org", replaced.Domain)
	require.NoError(t, db.Where("site_id = ?", site.Id).First(&stored).Error)
	assert.Equal(t, "dom-mail.new-sender.org", stored.ResendDomainId)
	assert.Contains(t, fake.deleted, oldDomain.ResendDomainId, "库写成功后才删旧域名")
}

func TestReplaceSenderDomainIgnoresMissingOldDomain(t *testing.T) {
	db, fake := prepareMailDB(t)
	owner, site := openActiveSite(t, db, "missingold")
	oldDomain, err := AddAdtySiteSenderDomain(context.Background(), owner.Id, "mail.gone-sender.org")
	require.NoError(t, err)
	fake.notFound[oldDomain.ResendDomainId] = true

	replaced, err := AddAdtySiteSenderDomain(context.Background(), owner.Id, "mail.next-sender.org")
	require.NoError(t, err)
	assert.Equal(t, "mail.next-sender.org", replaced.Domain)
	var stored model.AdtySiteSenderDomain
	require.NoError(t, db.Where("site_id = ?", site.Id).First(&stored).Error)
	assert.Equal(t, replaced.ResendDomainId, stored.ResendDomainId)
	assert.Equal(t, model.AdtySenderDomainPending, stored.Status)
}

func TestClosingHookEmailsCustomers(t *testing.T) {
	db, _ := prepareMailDB(t)
	require.NoError(t, i18n.Init())
	_, site := openActiveSite(t, db, "closemail")
	customer := siteCustomer(t, db, site, "closemail-customer")
	var sent []string
	previous := AdtyDeliverSiteEmail
	AdtyDeliverSiteEmail = func(_ *model.AdtySite, subject, to, content string) error {
		sent = append(sent, to+":"+subject+":"+content)
		return nil
	}
	t.Cleanup(func() { AdtyDeliverSiteEmail = previous })
	site.CloseExecuteAt = 1_700_000_000
	require.NotNil(t, AdtyOnSiteClosing)
	AdtyOnSiteClosing(site)
	require.NotEmpty(t, sent)
	assert.Contains(t, sent[0], customer.Email)
	assert.Contains(t, sent[0], customer.Username)
	assert.NotContains(t, sent[0], "@example.net")
	var batch model.AdtySiteCloseMailBatch
	require.NoError(t, db.Where("site_id = ?", site.Id).First(&batch).Error)
	assert.Equal(t, site.Id, batch.SiteId)
	assert.Equal(t, 1, batch.RecipientCount)
	assert.Equal(t, 1, batch.SuccessCount)
	assert.Zero(t, batch.FailCount)
	assert.Equal(t, model.AdtySiteCloseMailRetryComplete, batch.RetryStatus)
	assert.NotZero(t, batch.SentAt)
}

func TestCloseMigrationMailExplainsMainSiteSignInInEnglish(t *testing.T) {
	previous := system_setting.ServerAddress
	system_setting.ServerAddress = "https://main.example.com"
	t.Cleanup(func() { system_setting.ServerAddress = previous })
	site := &model.AdtySite{Brand: `{"name":"Old & Co","support_contact":"help <support@example.com>"}`, CloseExecuteAt: 1_700_000_000}
	subject, body := renderAdtySiteCloseMigrationMail(site, &model.AdtySiteCloseMailRecipient{Username: "member<1>"})
	assert.Equal(t, "Old & Co is closing; sign in on the main site", subject)
	for _, text := range []string{`href="https://main.example.com"`, "member&lt;1&gt;", "help &lt;support@example.com&gt;", adtyMailWhen(site.CloseExecuteAt), "unique username", "password", "orders and balance", "same email", "create new API keys"} {
		assert.Contains(t, body, text)
	}
	assert.NotContains(t, body, "该分站客户")
}

func TestSitePricingNoticesUseEnglishForChineseAccounts(t *testing.T) {
	db, _ := prepareMailDB(t)
	owner, site := openActiveSite(t, db, "pricemail")
	customer := siteCustomer(t, db, site, "pricemail-customer")
	require.NoError(t, db.Model(&model.User{}).Where("id IN ?", []int{owner.Id, customer.Id}).Update("setting", `{"language":"zh-CN"}`).Error)
	type mail struct{ subject, to, body string }
	var sent []mail
	previous := AdtySendSiteEmailAsync
	AdtySendSiteEmailAsync = func(_ *model.AdtySite, subject, to, body string) { sent = append(sent, mail{subject, to, body}) }
	t.Cleanup(func() { AdtySendSiteEmailAsync = previous })
	const at = 1_700_000_000
	adtyNotifySiteMarkupIncrease(site, customer.Id, []model.AdtySitePrice{{Group: "pro", Model: "model<1>", Markup: *dec("1.4"), UpdatedAt: at}})
	site.PendingCostFactor, site.PendingCostFactorAt = dec("0.92"), at
	adtyNotifySiteCostFactorPending(site)
	adtyNotifySiteCostFactor(site)
	require.Len(t, sent, 3)
	assert.Equal(t, customer.Email, sent[0].to)
	assert.Contains(t, sent[0].subject, "Prices on pricemail")
	assert.Contains(t, sent[0].body, `href="https://pricemail.sites.example.com"`)
	assert.Contains(t, sent[0].body, "pro / model&lt;1&gt; → 1.4")
	assert.Contains(t, sent[0].body, adtyMailWhen(at))
	assert.Contains(t, sent[0].body, "Previously accepted requests and tasks keep their original price")
	assert.Equal(t, owner.Email, sent[1].to)
	assert.Contains(t, sent[1].body, "0.92")
	assert.Contains(t, sent[1].body, adtyMailWhen(at))
	assert.Contains(t, sent[1].body, "current factor applies")
	assert.Equal(t, owner.Email, sent[2].to)
	assert.Contains(t, sent[2].body, "now 0.8")
}

func TestClosedSiteDeletesSenderDomain(t *testing.T) {
	db, fake := prepareMailDB(t)
	owner, site := openActiveSite(t, db, "closesender")
	_, err := AddAdtySiteSenderDomain(context.Background(), owner.Id, "mail.closesender.org")
	require.NoError(t, err)

	adtyRemoveSenderDomainOfClosedSite(site)

	assert.Equal(t, []string{"dom-mail.closesender.org"}, fake.deleted)
}

func TestSiteCloseMailRootInspectionRetryAndAutoWorker(t *testing.T) {
	db, _ := prepareMailDB(t)
	require.NoError(t, i18n.Init())
	require.NoError(t, db.AutoMigrate(&model.AdtySiteAudit{}, &model.AuditLog{}))
	owner, site := openActiveSite(t, db, "retryroot")
	u1 := siteCustomer(t, db, site, "retryroot-u1")
	u2 := siteCustomer(t, db, site, "retryroot-u2")
	require.NoError(t, db.Model(&model.AdtySite{}).Where("id = ?", site.Id).Update("status", model.AdtySiteStatusClosing).Error)
	site.Status = model.AdtySiteStatusClosing

	failU2 := true
	sentByEmail := map[string]int{}
	previous := AdtyDeliverSiteEmail
	AdtyDeliverSiteEmail = func(_ *model.AdtySite, _, to, _ string) error {
		if failU2 && to == u2.Email {
			return errors.New("smtp temporary timeout")
		}
		sentByEmail[to]++
		return nil
	}
	t.Cleanup(func() { AdtyDeliverSiteEmail = previous })

	require.NoError(t, SendAdtySiteCloseMigrationMail(site))
	assert.Equal(t, 1, sentByEmail[u1.Email])
	assert.Equal(t, 0, sentByEmail[u2.Email])

	batches, total, err := ListAdtySiteCloseMailBatches(model.AdtySiteCloseMailRetryRetryable, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, batches, 1)
	assert.Equal(t, site.Id, batches[0].SiteId)
	assert.Equal(t, 2, batches[0].RecipientCount)
	assert.Equal(t, 1, batches[0].SuccessCount)
	assert.Equal(t, 1, batches[0].FailCount)
	require.Len(t, batches[0].FailedRecipients, 1)
	assert.Equal(t, u2.Id, batches[0].FailedRecipients[0].UserId)
	assert.Contains(t, batches[0].FailedRecipients[0].LastError, "smtp temporary timeout")

	failedOnlyView, err := GetAdtySiteCloseMailBatch(site.Id, model.AdtySiteCloseMailFailed)
	require.NoError(t, err)
	require.Len(t, failedOnlyView.Recipients, 1)
	assert.Equal(t, u2.Id, failedOnlyView.Recipients[0].UserId)

	// Background worker retries failed/pending recipients while u2 still fails, then succeeds once recovered.
	require.NoError(t, RunAdtySiteCloseMailRetryOnce(time.Now()))
	assert.Equal(t, 1, sentByEmail[u1.Email], "successful recipient must not be resent")
	assert.Equal(t, 0, sentByEmail[u2.Email])

	failU2 = false
	retriedView, err := RetryAdtySiteCloseMigrationMailByRoot(site.Id, owner.Id, false, "manual root retry")
	require.NoError(t, err)
	assert.Equal(t, model.AdtySiteCloseMailRetryComplete, retriedView.RetryStatus)
	assert.Equal(t, 2, retriedView.SuccessCount)
	assert.Equal(t, 0, retriedView.FailCount)
	assert.Empty(t, retriedView.FailedRecipients)
	assert.Equal(t, 1, sentByEmail[u1.Email])
	assert.Equal(t, 1, sentByEmail[u2.Email])

	var siteAudit model.AdtySiteAudit
	require.NoError(t, db.Where("site_id = ? AND action = ?", site.Id, "close_mail_retry").First(&siteAudit).Error)
	assert.Equal(t, "manual root retry", siteAudit.Reason)
}

func TestSiteCloseMailSnapshotIncludesMigratedUsersAndResumesPartialSnapshot(t *testing.T) {
	db, _ := prepareMailDB(t)
	require.NoError(t, i18n.Init())
	require.NoError(t, db.AutoMigrate(&model.AdtySiteCustomer{}, &model.AdtyUserSiteMigration{}))
	_, site := openActiveSite(t, db, "snapresume")
	u1 := siteCustomer(t, db, site, "snap-u1")
	u2 := siteCustomer(t, db, site, "snap-u2")
	u3 := siteCustomer(t, db, site, "snap-u3")
	staff := siteCustomer(t, db, site, "snap-staff")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", staff.Id).Update("staff_site_id", site.Id).Error)

	// Simulate u2 already migrated via inherited_from_site_id and u3 migrated via AdtyUserSiteMigration.
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", u2.Id).Updates(map[string]any{
		"site_id":                0,
		"inherited_from_site_id": site.Id,
	}).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", u3.Id).Updates(map[string]any{
		"site_id": 0,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyUserSiteMigration{
		UserId:       u3.Id,
		SourceSiteId: site.Id,
		TargetSiteId: 0,
		CreatedAt:    time.Now().Unix(),
	}).Error)

	// Simulate an interrupted earlier snapshot that only persisted u1 before crashing.
	batch := model.AdtySiteCloseMailBatch{
		SiteId:      site.Id,
		RetryStatus: model.AdtySiteCloseMailRetryNone,
		CreatedAt:   time.Now().Unix(),
		UpdatedAt:   time.Now().Unix(),
	}
	require.NoError(t, db.Create(&batch).Error)
	require.NoError(t, db.Create(&model.AdtySiteCloseMailRecipient{
		BatchId:   batch.Id,
		SiteId:    site.Id,
		UserId:    u1.Id,
		Username:  u1.Username,
		Email:     u1.Email,
		Status:    model.AdtySiteCloseMailSuccess,
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}).Error)

	sentByEmail := map[string]int{}
	previous := AdtyDeliverSiteEmail
	AdtyDeliverSiteEmail = func(_ *model.AdtySite, _, to, _ string) error {
		sentByEmail[to]++
		return nil
	}
	t.Cleanup(func() { AdtyDeliverSiteEmail = previous })

	require.NoError(t, RetryAdtySiteCloseMigrationMail(site))
	assert.Equal(t, 0, sentByEmail[u1.Email], "already successful u1 must not be resent")
	assert.Equal(t, 1, sentByEmail[u2.Email], "inherited/migrated u2 must be snapshotted and mailed")
	assert.Equal(t, 1, sentByEmail[u3.Email], "archived/migrated u3 must be snapshotted and mailed")
	assert.Equal(t, 0, sentByEmail[staff.Email], "staff account must be excluded from customer close mail")

	view, err := GetAdtySiteCloseMailBatch(site.Id, "")
	require.NoError(t, err)
	assert.Equal(t, 3, view.RecipientCount)
	assert.Equal(t, 3, view.SuccessCount)
	assert.Equal(t, 0, view.FailCount)
	assert.Equal(t, model.AdtySiteCloseMailRetryComplete, view.RetryStatus)
}

func TestSiteCloseMailExclusiveRecipientClaimBatchStatusAndResendIdempotency(t *testing.T) {
	db, _ := prepareMailDB(t)
	require.NoError(t, i18n.Init())
	_, site := openActiveSite(t, db, "claimmail")
	u1 := siteCustomer(t, db, site, "claim-u1")
	u2 := siteCustomer(t, db, site, "claim-u2")

	now := time.Now().Unix()
	batch, err := loadOrCreateAdtySiteCloseMailBatch(site.Id, now)
	require.NoError(t, err)
	require.NoError(t, ensureAdtySiteCloseMailRecipients(batch, site.Id, now, false))

	// While recipients are still pending (fail_count == 0, success_count == 0), batch must be retryable, NOT complete.
	require.NoError(t, refreshAdtySiteCloseMailBatchCounts(batch.Id, now))
	var refreshed model.AdtySiteCloseMailBatch
	require.NoError(t, db.First(&refreshed, batch.Id).Error)
	assert.Equal(t, model.AdtySiteCloseMailRetryRetryable, refreshed.RetryStatus)

	// Mark u1 as already in-flight ('sending'): normal retry must not double-send to u1, and batch stays retryable.
	require.NoError(t, db.Model(&model.AdtySiteCloseMailRecipient{}).
		Where("batch_id = ? AND user_id = ?", batch.Id, u1.Id).
		Update("status", AdtySiteCloseMailSending).Error)

	var idempotencyKeys []string
	var deliveredTo []string
	mailServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/emails" {
			idempotencyKeys = append(idempotencyKeys, r.Header.Get("Idempotency-Key"))
			var payload AdtyResendEmail
			body, _ := io.ReadAll(r.Body)
			_ = common.Unmarshal(body, &payload)
			deliveredTo = append(deliveredTo, payload.To...)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"email_1"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(mailServer.Close)
	oldResend := AdtyResend
	AdtyResend = &AdtyResendClient{BaseURL: mailServer.URL, HTTP: mailServer.Client(), APIKey: func() string { return "re_test" }}
	t.Cleanup(func() { AdtyResend = oldResend })
	common.OptionMap["EmailProvider"] = "resend"

	require.NoError(t, RetryAdtySiteCloseMigrationMail(site))
	assert.Equal(t, []string{u2.Email}, deliveredTo, "in-flight sending recipient u1 must not be double-sent")
	assert.Equal(t, []string{AdtySiteCloseMailIdempotencyKey(batch.Id, u2.Id)}, idempotencyKeys)

	require.NoError(t, db.First(&refreshed, batch.Id).Error)
	assert.Equal(t, model.AdtySiteCloseMailRetryRetryable, refreshed.RetryStatus, "batch with sending recipient must remain retryable")

	// Root manual retry with includeSending=true can reclaim a stuck sending recipient and complete the batch.
	_, err = RetryAdtySiteCloseMigrationMailByRoot(site.Id, 0, true, "reclaim stuck sending")
	require.NoError(t, err)
	assert.Equal(t, []string{u2.Email, u1.Email}, deliveredTo)
	require.NoError(t, db.First(&refreshed, batch.Id).Error)
	assert.Equal(t, model.AdtySiteCloseMailRetryComplete, refreshed.RetryStatus)
}
