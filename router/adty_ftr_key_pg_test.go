package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ftrMainLogin(t *testing.T, f *siteFixture, username string) ftrLoginBody {
	t.Helper()
	resp := f.do(t, http.MethodPost, "main.example.com", "/api/user/login", `{"username":"`+username+`","password":"correct-horse-1"}`)
	var body ftrLoginBody
	require.NoError(t, common.Unmarshal(resp.Body.Bytes(), &body), resp.Body.String())
	require.True(t, body.Success, resp.Body.String())
	require.NotEmpty(t, body.Data.AccessToken)
	assert.Equal(t, common.RoleCommonUser, body.Data.User.Role)
	return body
}

func ftrAuthed(t *testing.T, f *siteFixture, method, path, body, accessToken string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Host = "main.example.com"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	rec := httptest.NewRecorder()
	f.engine.ServeHTTP(rec, req)
	return rec
}

func ftrPinQuota(t *testing.T, f *siteFixture, userID int) {
	t.Helper()
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", userID).Update("quota", ftrSessionQuota).Error)
}

func ftrEnsureAuditIntent(t *testing.T, f *siteFixture) {
	t.Helper()
	require.NoError(t, f.db.AutoMigrate(&model.AuditIntent{}))
}

func TestFTRADTY0401CreateAPIKeyPG(t *testing.T) {
	f := newSiteFixture(t)
	pgtest.RecordServerVersion(t, f.db)
	ftrEnsureAuditIntent(t, f)
	user := f.user(t, "ftr0401key", 0)
	ftrPinQuota(t, f, user.Id)
	login := ftrMainLogin(t, f, user.Username)

	created := ftrAuthed(t, f, http.MethodPost, "/api/token/", `{"name":"ftr-key","expired_time":-1,"unlimited_quota":true,"group":"default"}`, login.Data.AccessToken)
	require.Contains(t, created.Body.String(), `"success":true`, created.Body.String())

	var token model.Token
	require.NoError(t, f.db.Where("user_id = ? AND name = ?", user.Id, "ftr-key").First(&token).Error)
	assert.Equal(t, user.Id, token.UserId)
	assert.True(t, token.UnlimitedQuota)
	assert.Equal(t, int64(-1), token.ExpiredTime)
	assert.NotEmpty(t, token.Key)
	assertFTRQuotaAndNoLedger(t, f, user.Id, ftrSessionQuota)
}

func TestFTRADTY0402ListAPIKeysPG(t *testing.T) {
	f := newSiteFixture(t)
	pgtest.RecordServerVersion(t, f.db)
	ftrEnsureAuditIntent(t, f)
	user := f.user(t, "ftr0402key", 0)
	ftrPinQuota(t, f, user.Id)
	login := ftrMainLogin(t, f, user.Username)
	created := ftrAuthed(t, f, http.MethodPost, "/api/token/", `{"name":"ftr-list","expired_time":-1,"unlimited_quota":true,"group":"default"}`, login.Data.AccessToken)
	require.Contains(t, created.Body.String(), `"success":true`, created.Body.String())

	listed := ftrAuthed(t, f, http.MethodGet, "/api/token/", "", login.Data.AccessToken)
	require.Contains(t, listed.Body.String(), `"success":true`, listed.Body.String())
	assert.Contains(t, listed.Body.String(), "ftr-list")
	var count int64
	require.NoError(t, f.db.Model(&model.Token{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	assertFTRQuotaAndNoLedger(t, f, user.Id, ftrSessionQuota)
}

func TestFTRADTY0206ProfileRoutePG(t *testing.T) {
	f := newSiteFixture(t)
	pgtest.RecordServerVersion(t, f.db)
	user := f.user(t, "ftr0206rt", 0)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Updates(map[string]any{
		"quota":        ftrSessionQuota,
		"display_name": "Old Route",
	}).Error)
	login := ftrMainLogin(t, f, user.Username)

	view := ftrAuthed(t, f, http.MethodGet, "/api/user/self", "", login.Data.AccessToken)
	require.Contains(t, view.Body.String(), `"success":true`, view.Body.String())
	assert.Contains(t, view.Body.String(), `"display_name":"Old Route"`)
	assert.Contains(t, view.Body.String(), `"role":1`)

	edit := ftrAuthed(t, f, http.MethodPut, "/api/user/self", `{"display_name":"Route Name"}`, login.Data.AccessToken)
	require.Contains(t, edit.Body.String(), `"success":true`, edit.Body.String())

	var stored model.User
	require.NoError(t, f.db.First(&stored, user.Id).Error)
	assert.Equal(t, "Route Name", stored.DisplayName)
	assertFTRQuotaAndNoLedger(t, f, user.Id, ftrSessionQuota)
}
