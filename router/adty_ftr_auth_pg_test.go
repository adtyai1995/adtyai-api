package router

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	ftrRegisterQuota = 2500
	ftrSessionQuota  = 500000
)

var ftrMailCode = regexp.MustCompile(`<strong>([0-9a-fA-F]{6})</strong>`)

func pinFTRRegisterQuota(t *testing.T) {
	t.Helper()
	old := common.QuotaForNewUser
	common.QuotaForNewUser = ftrRegisterQuota
	t.Cleanup(func() { common.QuotaForNewUser = old })
}

func clearMainLegalTexts(t *testing.T) {
	t.Helper()
	legal := system_setting.GetLegalSettings()
	oldAgreement, oldPrivacy := legal.UserAgreement, legal.PrivacyPolicy
	legal.UserAgreement, legal.PrivacyPolicy = "", ""
	t.Cleanup(func() {
		legal.UserAgreement, legal.PrivacyPolicy = oldAgreement, oldPrivacy
	})
}

func assertFTRQuotaAndNoLedger(t *testing.T, f *siteFixture, userID, wantQuota int) {
	t.Helper()
	var stored model.User
	require.NoError(t, f.db.First(&stored, userID).Error)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	assert.Equal(t, wantQuota, stored.Quota)
	var ledgers int64
	require.NoError(t, f.db.Model(&model.AdtyBalanceLedger{}).Where("user_id = ?", userID).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

func TestFTRADTY0201MainSiteRegisterPG(t *testing.T) {
	f := newSiteFixture(t)
	pgtest.RecordServerVersion(t, f.db)
	pinFTRRegisterQuota(t)
	clearMainLegalTexts(t)

	resp := f.do(t, http.MethodPost, "main.example.com", "/api/user/register", `{"username":"ftr0201main","password":"correct-horse-1"}`)
	require.Contains(t, resp.Body.String(), `"success":true`, resp.Body.String())

	var user model.User
	require.NoError(t, f.db.Where("username = ?", "ftr0201main").First(&user).Error)
	assert.Zero(t, user.SiteId)
	assertFTRQuotaAndNoLedger(t, f, user.Id, ftrRegisterQuota)

	var affiliates int64
	require.NoError(t, f.db.Model(&model.ReferralAffiliate{}).Where("user_id = ?", user.Id).Count(&affiliates).Error)
	assert.EqualValues(t, 1, affiliates)
	var customers int64
	require.NoError(t, f.db.Model(&model.AdtySiteCustomer{}).Where("user_id = ?", user.Id).Count(&customers).Error)
	assert.Zero(t, customers)
	var logs int64
	require.NoError(t, f.db.Model(&model.Log{}).Where("user_id = ? AND type = ? AND content LIKE ?", user.Id, model.LogTypeSystem, "%New user sign-up bonus%").Count(&logs).Error)
	assert.EqualValues(t, 1, logs)
}

func TestFTRADTY0202SiteRegisterPG(t *testing.T) {
	f := newSiteFixture(t)
	pgtest.RecordServerVersion(t, f.db)
	pinFTRRegisterQuota(t)

	resp := f.do(t, http.MethodPost, "shop.sites.example.com", "/api/user/register", `{"username":"ftr0202site","password":"correct-horse-1"}`)
	require.Contains(t, resp.Body.String(), `"success":true`, resp.Body.String())

	var user model.User
	require.NoError(t, f.db.Where("username = ?", "ftr0202site").First(&user).Error)
	assert.Equal(t, f.site.Id, user.SiteId)
	assert.Equal(t, common.RoleCommonUser, user.Role)
	assertFTRQuotaAndNoLedger(t, f, user.Id, ftrRegisterQuota)

	var customer model.AdtySiteCustomer
	require.NoError(t, f.db.Where("user_id = ?", user.Id).First(&customer).Error)
	assert.Equal(t, f.site.Id, customer.SiteId)
	assert.Equal(t, model.AdtySiteCustomerActive, customer.Status)
	var affiliates int64
	require.NoError(t, f.db.Model(&model.ReferralAffiliate{}).Where("user_id = ?", user.Id).Count(&affiliates).Error)
	assert.Zero(t, affiliates)
}

func TestFTRADTY0203EmailVerificationRegisterPG(t *testing.T) {
	f := newSiteFixture(t)
	pgtest.RecordServerVersion(t, f.db)
	pinFTRRegisterQuota(t)
	clearMainLegalTexts(t)
	oldVerify := common.EmailVerificationEnabled
	common.EmailVerificationEnabled = true
	t.Cleanup(func() { common.EmailVerificationEnabled = oldVerify })

	var mailed string
	oldSend := service.AdtySendSiteEmailAsync
	service.AdtySendSiteEmailAsync = func(_ *model.AdtySite, _, to, content string) {
		assert.Equal(t, "ftr0203@example.com", to)
		mailed = content
	}
	t.Cleanup(func() { service.AdtySendSiteEmailAsync = oldSend })

	sent := f.do(t, http.MethodGet, "main.example.com", "/api/verification?email=ftr0203@example.com", "")
	require.Contains(t, sent.Body.String(), `"success":true`, sent.Body.String())
	match := ftrMailCode.FindStringSubmatch(mailed)
	require.Len(t, match, 2, mailed)
	code := match[1]
	key := model.SiteEmailCodeKey("ftr0203@example.com", 0)
	assert.True(t, common.VerifyCodeWithKey(key, code, common.EmailVerificationPurpose))

	body := `{"username":"ftr0203mail","password":"correct-horse-1","email":"ftr0203@example.com","verification_code":"` + code + `"}`
	registered := f.do(t, http.MethodPost, "main.example.com", "/api/user/register", body)
	require.Contains(t, registered.Body.String(), `"success":true`, registered.Body.String())
	assert.False(t, common.VerifyCodeWithKey(key, code, common.EmailVerificationPurpose), "register consumes the emailed code")

	var user model.User
	require.NoError(t, f.db.Where("username = ?", "ftr0203mail").First(&user).Error)
	assert.Equal(t, "ftr0203@example.com", user.Email)
	assert.Zero(t, user.SiteId)
	assertFTRQuotaAndNoLedger(t, f, user.Id, ftrRegisterQuota)
}

func TestFTRADTY0204MainSiteLoginPG(t *testing.T) {
	f := newSiteFixture(t)
	pgtest.RecordServerVersion(t, f.db)
	user := f.user(t, "ftr0204login", 0)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", ftrSessionQuota).Error)

	resp := f.do(t, http.MethodPost, "main.example.com", "/api/user/login", `{"username":"ftr0204login","password":"correct-horse-1"}`)
	var body ftrLoginBody
	require.NoError(t, common.Unmarshal(resp.Body.Bytes(), &body), resp.Body.String())
	require.True(t, body.Success, resp.Body.String())
	require.NotEmpty(t, body.Data.AccessToken)
	assert.Equal(t, common.RoleCommonUser, body.Data.User.Role)
	assert.Equal(t, user.Id, body.Data.User.ID)
	require.NotEmpty(t, body.Data.Session.SID)

	var session model.UserSession
	require.NoError(t, f.db.Where("sid = ? AND user_id = ?", body.Data.Session.SID, user.Id).First(&session).Error)
	assert.Equal(t, model.UserSessionStatusActive, session.Status)
	var stored model.User
	require.NoError(t, f.db.First(&stored, user.Id).Error)
	assert.Positive(t, stored.LastLoginAt)
	assertFTRQuotaAndNoLedger(t, f, user.Id, ftrSessionQuota)
}

func TestFTRADTY0205LogoutRevokesSessionPG(t *testing.T) {
	f := newSiteFixture(t)
	pgtest.RecordServerVersion(t, f.db)
	user := f.user(t, "ftr0205out", 0)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", ftrSessionQuota).Error)

	login := f.do(t, http.MethodPost, "main.example.com", "/api/user/login", `{"username":"ftr0205out","password":"correct-horse-1"}`)
	var body ftrLoginBody
	require.NoError(t, common.Unmarshal(login.Body.Bytes(), &body), login.Body.String())
	require.True(t, body.Success, login.Body.String())
	require.NotEmpty(t, body.Data.AccessToken)
	require.NotEmpty(t, body.Data.Session.SID)

	req := httptest.NewRequest(http.MethodPost, "/api/user/auth/logout", nil)
	req.Host = "main.example.com"
	req.Header.Set("Authorization", "Bearer "+body.Data.AccessToken)
	for _, cookie := range login.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	f.engine.ServeHTTP(rec, req)
	require.Contains(t, rec.Body.String(), `"success":true`, rec.Body.String())
	require.Contains(t, rec.Body.String(), body.Data.Session.SID)

	var session model.UserSession
	require.NoError(t, f.db.Where("sid = ?", body.Data.Session.SID).First(&session).Error)
	assert.Equal(t, model.UserSessionStatusRevoked, session.Status)
	assert.Equal(t, "logout", session.RevokedReason)
	assertFTRQuotaAndNoLedger(t, f, user.Id, ftrSessionQuota)
}

type ftrLoginBody struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		AccessToken string `json:"access_token"`
		User        struct {
			ID   int `json:"id"`
			Role int `json:"role"`
		} `json:"user"`
		Session struct {
			SID string `json:"sid"`
		} `json:"session"`
	} `json:"data"`
}
