package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type r37LegalEnv struct {
	db            *gorm.DB
	legalSetting  *adty_setting.LegalSetting
	siteSetting   *adty_setting.SiteSetting
	legacyLegal   *system_setting.LegalSettings
	consoleConfig *console_setting.ConsoleSetting
}

func setupR37LegalTest(t *testing.T) *r37LegalEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.AdtySite{},
		&model.AdtySiteDomain{},
		&model.AdtySiteAudit{},
		&model.AdtySiteReferralRate{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	oldDB := model.DB
	model.DB = db
	service.InvalidateAdtySiteCache()

	legalPtr, ok := config.GlobalConfig.Get("adty_legal_setting").(*adty_setting.LegalSetting)
	if !ok || legalPtr == nil {
		t.Fatalf("adty_legal_setting not registered")
	}
	sitePtr, ok := config.GlobalConfig.Get("adty_site_setting").(*adty_setting.SiteSetting)
	if !ok || sitePtr == nil {
		t.Fatalf("adty_site_setting not registered")
	}
	legacyPtr, ok := config.GlobalConfig.Get("legal").(*system_setting.LegalSettings)
	if !ok || legacyPtr == nil {
		t.Fatalf("legal setting not registered")
	}
	consolePtr, ok := config.GlobalConfig.Get("console_setting").(*console_setting.ConsoleSetting)
	if !ok || consolePtr == nil {
		t.Fatalf("console_setting not registered")
	}

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	oldAbout, hadAbout := common.OptionMap["About"]
	oldLegal := *legalPtr
	oldSite := *sitePtr
	oldLegacy := *legacyPtr
	oldConsole := *consolePtr

	*legalPtr = adty_setting.LegalSetting{
		UserAgreement: map[string]string{},
		PrivacyPolicy: map[string]string{},
		RefundPolicy:  map[string]string{},
	}
	*sitePtr = adty_setting.SiteSetting{}
	*legacyPtr = system_setting.LegalSettings{}
	consolePtr.AnnouncementsEnabled = false
	consolePtr.ApiInfoEnabled = false
	consolePtr.FAQEnabled = false
	common.OptionMap["About"] = "Main site about content"
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		*legalPtr = oldLegal
		*sitePtr = oldSite
		*legacyPtr = oldLegacy
		*consolePtr = oldConsole
		if hadAbout {
			common.OptionMap["About"] = oldAbout
		} else {
			delete(common.OptionMap, "About")
		}
		common.OptionMapRWMutex.Unlock()
		model.DB = oldDB
		service.InvalidateAdtySiteCache()
	})

	return &r37LegalEnv{
		db:            db,
		legalSetting:  legalPtr,
		siteSetting:   sitePtr,
		legacyLegal:   legacyPtr,
		consoleConfig: consolePtr,
	}
}

func makeR37GinContext(method, target string, site *model.AdtySite, headers map[string]string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	if site != nil {
		c.Set(service.AdtySiteContextKey, site)
	}
	return c, w
}

func decodeStatusResponse(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status HTTP %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	if err := common.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal status: %v (body=%s)", err, w.Body.String())
	}
	if !resp.Success {
		t.Fatalf("expected success=true, body=%s", w.Body.String())
	}
	return resp.Data
}

func decodePublicContent(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("content HTTP %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Success bool   `json:"success"`
		Data    string `json:"data"`
	}
	if err := common.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal public content: %v (body=%s)", err, w.Body.String())
	}
	if !resp.Success {
		t.Fatalf("expected success=true, body=%s", w.Body.String())
	}
	return resp.Data
}

func decodeAdtySiteResponse(t *testing.T, w *httptest.ResponseRecorder) adtySiteView {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("adty site HTTP %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Success bool         `json:"success"`
		Data    adtySiteView `json:"data"`
	}
	if err := common.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal adty site: %v (body=%s)", err, w.Body.String())
	}
	if !resp.Success {
		t.Fatalf("expected success=true, body=%s", w.Body.String())
	}
	return resp.Data
}

func TestR37LegalMainSiteStatusAndFallback(t *testing.T) {
	env := setupR37LegalTest(t)

	// Main site /api/status reports user_agreement_enabled: true when ONLY adty_legal_setting has zh-CN
	// and legacy legal.user_agreement is empty.
	common.OptionMapRWMutex.Lock()
	env.legalSetting.UserAgreement = map[string]string{"zh-CN": "主站用户协议"}
	env.legalSetting.PrivacyPolicy = map[string]string{}
	env.legacyLegal.UserAgreement = ""
	env.legacyLegal.PrivacyPolicy = ""
	common.OptionMapRWMutex.Unlock()

	c, w := makeR37GinContext(http.MethodGet, "/api/status", nil, nil)
	GetStatus(c)
	data := decodeStatusResponse(t, w)
	if data["user_agreement_enabled"] != true {
		t.Fatalf("want user_agreement_enabled=true from adty_legal_setting zh-CN, got %v", data["user_agreement_enabled"])
	}
	if data["privacy_policy_enabled"] != false {
		t.Fatalf("want privacy_policy_enabled=false when empty, got %v", data["privacy_policy_enabled"])
	}
	if !service.AdtyLegalConsentRequiredForRequest(c) {
		t.Fatalf("want AdtyLegalConsentRequiredForRequest=true on main site with zh-CN user agreement")
	}

	// GetAdtyLegal fallback order: requested lang -> en -> zh-CN -> first non-empty (sorted) -> legacy.
	common.OptionMapRWMutex.Lock()
	env.legalSetting.UserAgreement = map[string]string{
		"en":    "Main EN Agreement",
		"zh-CN": "主站中文协议",
	}
	env.legalSetting.PrivacyPolicy = map[string]string{
		"zh-CN": "主站中文隐私",
	}
	env.legalSetting.RefundPolicy = map[string]string{
		"ja": "JA Refund",
		"fr": "FR Refund",
	}
	env.legacyLegal.UserAgreement = "Legacy User Agreement"
	env.legacyLegal.PrivacyPolicy = "Legacy Privacy Policy"
	common.OptionMapRWMutex.Unlock()

	callLegal := func(doc, query string, site *model.AdtySite, headers map[string]string) string {
		target := "/api/adty/legal/" + doc
		if query != "" {
			target += "?" + query
		}
		gc, rec := makeR37GinContext(http.MethodGet, target, site, headers)
		gc.Params = gin.Params{{Key: "doc", Value: doc}}
		GetAdtyLegal(gc)
		return decodePublicContent(t, rec)
	}

	// Exact normalized zh match (zh_CN -> zh-CN).
	if got := callLegal("user-agreement", "lang=zh_CN", nil, nil); got != "主站中文协议" {
		t.Fatalf("lang=zh_CN: want 主站中文协议, got %q", got)
	}
	// English request -> en.
	if got := callLegal("user-agreement", "lang=en-US", nil, nil); got != "Main EN Agreement" {
		t.Fatalf("lang=en-US: want Main EN Agreement, got %q", got)
	}
	// Missing en -> falls back to zh-CN.
	if got := callLegal("privacy-policy", "lang=en", nil, nil); got != "主站中文隐私" {
		t.Fatalf("privacy-policy lang=en fallback to zh-CN: want 主站中文隐私, got %q", got)
	}
	// Missing en & zh-CN -> falls back to first non-empty key in sorted order (fr before ja).
	if got := callLegal("refund-policy", "lang=en", nil, nil); got != "FR Refund" {
		t.Fatalf("refund-policy sorted fallback: want FR Refund, got %q", got)
	}
	// Legacy fallback only when multilingual map is empty.
	common.OptionMapRWMutex.Lock()
	env.legalSetting.PrivacyPolicy = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	if got := callLegal("privacy-policy", "lang=en", nil, nil); got != "Legacy Privacy Policy" {
		t.Fatalf("legacy fallback when multilingual empty: want Legacy Privacy Policy, got %q", got)
	}
	// /api/user-agreement respects Accept-Language when ?lang= is omitted.
	uaCtx, uaRec := makeR37GinContext(http.MethodGet, "/api/user-agreement", nil, map[string]string{"Accept-Language": "zh-CN,zh;q=0.9"})
	GetUserAgreement(uaCtx)
	if got := decodePublicContent(t, uaRec); got != "主站中文协议" {
		t.Fatalf("GetUserAgreement Accept-Language zh-CN: want 主站中文协议, got %q", got)
	}
}

func TestR37LegalSubsiteStrictIsolationAndBrandingCleanup(t *testing.T) {
	env := setupR37LegalTest(t)

	// Populate ALL main site legal docs + refund policy toggle + legacy legal texts.
	common.OptionMapRWMutex.Lock()
	env.legalSetting.UserAgreement = map[string]string{"en": "Main UA", "zh-CN": "主站协议"}
	env.legalSetting.PrivacyPolicy = map[string]string{"en": "Main PP", "zh-CN": "主站隐私"}
	env.legalSetting.RefundPolicy = map[string]string{"en": "Main RP", "zh-CN": "主站退款"}
	env.legacyLegal.UserAgreement = "Legacy Main UA"
	env.legacyLegal.PrivacyPolicy = "Legacy Main PP"
	env.siteSetting.RefundPolicyEnabled = true
	common.OptionMapRWMutex.Unlock()

	// Create subsite with empty LegalDocs and legacy brand JSON containing tagline & theme_color.
	legacyBrand, err := common.Marshal(map[string]string{
		"name":        "Sub Brand",
		"title":       "Sub Title",
		"tagline":     "Legacy Subsite Tagline",
		"theme_color": "#ff00aa",
	})
	if err != nil {
		t.Fatalf("marshal brand: %v", err)
	}
	subsite := model.AdtySite{
		OwnerUserId: 1001,
		Subdomain:   "iso",
		RootDomain:  "platform.test",
		Status:      model.AdtySiteStatusActive,
		Brand:       string(legacyBrand),
		LegalDocs:   "",
	}
	if err := env.db.Create(&subsite).Error; err != nil {
		t.Fatalf("create subsite: %v", err)
	}
	service.InvalidateAdtySiteCache()

	// 1. Subsite /api/status must have user_agreement_enabled=false, privacy_policy_enabled=false,
	//    and MUST NOT contain theme_color.
	statusCtx, statusRec := makeR37GinContext(http.MethodGet, "/api/status", &subsite, nil)
	GetStatus(statusCtx)
	statusData := decodeStatusResponse(t, statusRec)
	if statusData["user_agreement_enabled"] != false {
		t.Fatalf("subsite with empty LegalDocs: want user_agreement_enabled=false, got %v", statusData["user_agreement_enabled"])
	}
	if statusData["privacy_policy_enabled"] != false {
		t.Fatalf("subsite with empty LegalDocs: want privacy_policy_enabled=false, got %v", statusData["privacy_policy_enabled"])
	}
	if _, exists := statusData["theme_color"]; exists {
		t.Fatalf("subsite /api/status must NOT contain theme_color override, got %v", statusData["theme_color"])
	}

	// 2. Subsite /api/about must return "" even when brand JSON has legacy tagline & main site About is set.
	aboutCtx, aboutRec := makeR37GinContext(http.MethodGet, "/api/about", &subsite, nil)
	GetAbout(aboutCtx)
	if got := decodePublicContent(t, aboutRec); got != "" {
		t.Fatalf("subsite /api/about: want empty string, got %q", got)
	}

	// 3. Subsite /api/adty/site must report refund_policy_enabled=false even though main site has true.
	siteCtx, siteRec := makeR37GinContext(http.MethodGet, "/api/adty/site", &subsite, nil)
	GetAdtySite(siteCtx)
	siteView := decodeAdtySiteResponse(t, siteRec)
	if siteView.RefundPolicyEnabled {
		t.Fatalf("subsite with empty LegalDocs: want refund_policy_enabled=false, got true")
	}

	// 4. All legal endpoints on subsite must return "" (strict isolation from main site).
	for _, doc := range []string{"user-agreement", "privacy-policy", "refund-policy"} {
		gc, rec := makeR37GinContext(http.MethodGet, "/api/adty/legal/"+doc+"?lang=en", &subsite, nil)
		gc.Params = gin.Params{{Key: "doc", Value: doc}}
		GetAdtyLegal(gc)
		if got := decodePublicContent(t, rec); got != "" {
			t.Fatalf("subsite /api/adty/legal/%s with empty LegalDocs: want empty, got %q", doc, got)
		}
	}
	uaCtx, uaRec := makeR37GinContext(http.MethodGet, "/api/user-agreement?lang=en", &subsite, nil)
	GetUserAgreement(uaCtx)
	if got := decodePublicContent(t, uaRec); got != "" {
		t.Fatalf("subsite /api/user-agreement with empty LegalDocs: want empty, got %q", got)
	}
	ppCtx, ppRec := makeR37GinContext(http.MethodGet, "/api/privacy-policy?lang=zh-CN", &subsite, nil)
	GetPrivacyPolicy(ppCtx)
	if got := decodePublicContent(t, ppRec); got != "" {
		t.Fatalf("subsite /api/privacy-policy with empty LegalDocs: want empty, got %q", got)
	}

	// 5. AdtyLegalConsentRequiredForRequest must be false for empty subsite even when main site is true.
	if service.AdtyLegalConsentRequiredForRequest(statusCtx) {
		t.Fatalf("want AdtyLegalConsentRequiredForRequest=false for subsite with empty LegalDocs")
	}
	mainCtx, _ := makeR37GinContext(http.MethodGet, "/api/user/register", nil, nil)
	if !service.AdtyLegalConsentRequiredForRequest(mainCtx) {
		t.Fatalf("want AdtyLegalConsentRequiredForRequest=true for main site with populated legal docs")
	}
}

func TestR37LegalSubsiteSaveAndServeOwnDocs(t *testing.T) {
	env := setupR37LegalTest(t)

	// Main site has everything empty / disabled.
	common.OptionMapRWMutex.Lock()
	env.siteSetting.RefundPolicyEnabled = false
	common.OptionMapRWMutex.Unlock()

	subsite := model.AdtySite{
		OwnerUserId: 2001,
		Subdomain:   "customlegal",
		RootDomain:  "platform.test",
		Status:      model.AdtySiteStatusActive,
	}
	if err := env.db.Create(&subsite).Error; err != nil {
		t.Fatalf("create subsite: %v", err)
	}

	// Reject wrong owner.
	if err := service.SaveAdtySiteLegal(subsite.Id, 9999, model.AdtySiteLegalDocs{}); !errors.Is(err, service.ErrAdtySiteNotOwner) {
		t.Fatalf("wrong owner: want ErrAdtySiteNotOwner, got %v", err)
	}
	// Reject oversized doc (> 200 KB).
	oversized := strings.Repeat("a", service.AdtySiteLegalDocMaxBytes+1)
	if err := service.SaveAdtySiteLegal(subsite.Id, 2001, model.AdtySiteLegalDocs{
		UserAgreement: map[string]string{"en": oversized},
	}); err == nil {
		t.Fatalf("expected error for oversized legal document")
	}

	// Save valid multilingual docs (including non-formal key "fr" which must be dropped, and "zh_CN" normalized to "zh-CN").
	err := service.SaveAdtySiteLegal(subsite.Id, 2001, model.AdtySiteLegalDocs{
		UserAgreement:       map[string]string{"en": "Subsite EN UA", "zh_CN": "分站中文协议", "fr": "Should drop"},
		PrivacyPolicy:       map[string]string{"zh-CN": "分站中文隐私"},
		RefundPolicy:        map[string]string{"en": "Subsite EN Refund"},
		RefundPolicyEnabled: true,
	})
	if err != nil {
		t.Fatalf("SaveAdtySiteLegal failed: %v", err)
	}

	// Verify audit row recorded.
	var audits int64
	if err := env.db.Model(&model.AdtySiteAudit{}).Where("site_id = ? AND action = ? AND operator_id = ?", subsite.Id, "legal_update", 2001).Count(&audits).Error; err != nil {
		t.Fatalf("count legal_update audits: %v", err)
	}
	if audits != 1 {
		t.Fatalf("want 1 legal_update audit row, got %d", audits)
	}

	// Reload persisted subsite.
	var reloaded model.AdtySite
	if err := env.db.First(&reloaded, subsite.Id).Error; err != nil {
		t.Fatalf("reload subsite: %v", err)
	}
	parsed := service.AdtySiteLegalFor(&reloaded)
	if _, hasFR := parsed.UserAgreement["fr"]; hasFR {
		t.Fatalf("non-formal language fr must be dropped on save, got %+v", parsed.UserAgreement)
	}
	if parsed.UserAgreement["zh-CN"] != "分站中文协议" {
		t.Fatalf("want normalized zh-CN user agreement, got %+v", parsed.UserAgreement)
	}

	// Subsite /api/status reports enabled flags = true.
	statusCtx, statusRec := makeR37GinContext(http.MethodGet, "/api/status", &reloaded, nil)
	GetStatus(statusCtx)
	statusData := decodeStatusResponse(t, statusRec)
	if statusData["user_agreement_enabled"] != true || statusData["privacy_policy_enabled"] != true {
		t.Fatalf("want subsite legal flags true, got ua=%v pp=%v", statusData["user_agreement_enabled"], statusData["privacy_policy_enabled"])
	}

	// Subsite /api/adty/site reports refund_policy_enabled = true even while main site has false.
	siteCtx, siteRec := makeR37GinContext(http.MethodGet, "/api/adty/site", &reloaded, nil)
	GetAdtySite(siteCtx)
	if !decodeAdtySiteResponse(t, siteRec).RefundPolicyEnabled {
		t.Fatalf("want subsite refund_policy_enabled=true")
	}

	// Subsite legal doc fallback: requested lang -> en -> zh-CN.
	checkSubLegal := func(doc, lang, want string) {
		t.Helper()
		gc, rec := makeR37GinContext(http.MethodGet, "/api/adty/legal/"+doc+"?lang="+lang, &reloaded, nil)
		gc.Params = gin.Params{{Key: "doc", Value: doc}}
		GetAdtyLegal(gc)
		if got := decodePublicContent(t, rec); got != want {
			t.Fatalf("subsite %s lang=%s: want %q, got %q", doc, lang, want, got)
		}
	}
	checkSubLegal("user-agreement", "zh-CN", "分站中文协议")
	checkSubLegal("user-agreement", "en", "Subsite EN UA")
	checkSubLegal("privacy-policy", "en", "分站中文隐私")       // en missing -> zh-CN fallback
	checkSubLegal("refund-policy", "zh-CN", "Subsite EN Refund") // zh-CN missing -> en fallback

	// AdtyLegalConsentRequiredForRequest: true on subsite, false on empty main site.
	if !service.AdtyLegalConsentRequiredForRequest(statusCtx) {
		t.Fatalf("want AdtyLegalConsentRequiredForRequest=true for subsite with own legal docs")
	}
	mainCtx, _ := makeR37GinContext(http.MethodGet, "/api/user/register", nil, nil)
	if service.AdtyLegalConsentRequiredForRequest(mainCtx) {
		t.Fatalf("want AdtyLegalConsentRequiredForRequest=false for empty main site")
	}

	// Closing site must reject SaveAdtySiteLegal.
	if err := env.db.Model(&reloaded).Update("status", model.AdtySiteStatusClosing).Error; err != nil {
		t.Fatalf("set closing: %v", err)
	}
	if err := service.SaveAdtySiteLegal(subsite.Id, 2001, parsed); !errors.Is(err, service.ErrAdtySiteClosing) {
		t.Fatalf("closing site: want ErrAdtySiteClosing, got %v", err)
	}
}
