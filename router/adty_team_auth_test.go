package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const teamAuthMainHost = "localhost"

type teamAuthFixture struct {
	db                      *gorm.DB
	engine                  *gin.Engine
	siteA, siteB            *model.AdtySite
	root, ownerA, ownerB    *model.User
	mainUser, mainLeader    *model.User
	leaderA, memberA, userA *model.User
	leaderB, staffA         *model.User
	rootTok, ownerATok      string
	ownerBTok, mainUserTok  string
	mainLeaderTok           string
	leaderATok, memberATok  string
	userATok, leaderBTok    string
	staffATok               string
}

type teamAuthCreated struct {
	Team   model.AdtyTeam
	Member model.AdtyTeamMember
}

func newTeamAuthFixture(t *testing.T) *teamAuthFixture {
	t.Helper()
	disableAdtyRedis(t)
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.Token{}, &model.AuthFlow{}, &model.TwoFA{}, &model.PasskeyCredential{},
		&model.AuditLog{}, &model.AuditIntent{}, &model.Log{}, &model.Option{}, &model.TopUp{}, &model.UserAccessToken{}, &model.AdtyUserFingerprint{}))
	require.NoError(t, model.MigrateAdtyTables())
	cfg := withResellerSetting(t)
	cfg.Enabled, cfg.RootDomains = true, []string{"sites.example.com"}
	cfg.CostFactor, cfg.MinCostFactor, cfg.MaxMarkup = new(0.8), new(0.5), new(3.0)
	cfg.MaxSiteBonusExtra, cfg.TrialGrantMaxUSD, cfg.TrialGrantDailyUSD = new(2.0), new(5.0), new(20.0)
	require.NoError(t, i18n.Init())
	gin.SetMode(gin.TestMode)

	f := &teamAuthFixture{db: db}
	f.root = f.createUser(t, "team-auth-root", common.RoleRootUser, 0, 0)
	f.mainUser = f.createUser(t, "team-auth-main-user", common.RoleCommonUser, 0, 0)
	f.mainLeader = f.createUser(t, "team-auth-main-leader", common.RoleCommonUser, 0, 0)
	f.ownerA = f.createUser(t, "team-auth-owner-a", common.RoleCommonUser, 0, 0)
	f.ownerB = f.createUser(t, "team-auth-owner-b", common.RoleCommonUser, 0, 0)

	siteA, err := service.OpenAdtySite(f.ownerA.Id, "team-auth-a", model.AdtySiteOpenedByAdmin, service.AdtySitePreset{})
	require.NoError(t, err)
	siteB, err := service.OpenAdtySite(f.ownerB.Id, "team-auth-b", model.AdtySiteOpenedByAdmin, service.AdtySitePreset{})
	require.NoError(t, err)
	current := service.AdtyCurrentSiteAgreementVersion()
	require.NoError(t, db.Model(&model.AdtySite{}).Where("id IN ?", []int{siteA.Id, siteB.Id}).Update("agreement_ver", current).Error)
	require.NoError(t, db.First(siteA, siteA.Id).Error)
	require.NoError(t, db.First(siteB, siteB.Id).Error)
	siteA.AgreementVer, siteB.AgreementVer = current, current
	f.siteA, f.siteB = siteA, siteB
	require.Equal(t, 0, f.ownerA.SiteId)

	f.leaderA = f.createUser(t, "team-auth-leader-a", common.RoleCommonUser, siteA.Id, 0)
	f.memberA = f.createUser(t, "team-auth-member-a", common.RoleCommonUser, siteA.Id, 0)
	f.userA = f.createUser(t, "team-auth-user-a", common.RoleCommonUser, siteA.Id, 0)
	f.leaderB = f.createUser(t, "team-auth-leader-b", common.RoleCommonUser, siteB.Id, 0)
	f.staffA = f.createUser(t, "team-auth-staff-a", common.RoleCommonUser, siteA.Id, siteA.Id)

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		if requestSite := service.AdtySiteForHost(c.Request.Host); requestSite != nil {
			c.Set(service.AdtySiteContextKey, requestSite)
		}
		c.Next()
	})
	SetApiRouter(engine)
	f.engine = engine

	f.rootTok = f.session(t, f.root)
	f.ownerATok = f.session(t, f.ownerA)
	f.ownerBTok = f.session(t, f.ownerB)
	f.mainUserTok = f.session(t, f.mainUser)
	f.mainLeaderTok = f.session(t, f.mainLeader)
	f.leaderATok = f.session(t, f.leaderA)
	f.memberATok = f.session(t, f.memberA)
	f.userATok = f.session(t, f.userA)
	f.leaderBTok = f.session(t, f.leaderB)
	f.staffATok = f.session(t, f.staffA)
	return f
}

func (f *teamAuthFixture) createUser(t *testing.T, username string, role, siteID, staffSiteID int) *model.User {
	t.Helper()
	user := &model.User{
		Username: username, Password: "hashed-password", Role: role, Status: common.UserStatusEnabled,
		Group: "default", AuthVersion: 1, SiteId: siteID, StaffSiteId: staffSiteID, Email: username + "@example.com",
	}
	require.NoError(t, f.db.Create(user).Error)
	return user
}

// session 签发的会话必须绑定到用户实际操作的那台主机（A.4.4 / DEC-023）。
// 站点成员用自身 SiteId；站长自身 SiteId 为 0，但他在自己站点的主机上操作，
// 所以按 owner_user_id 反查他的站点。主站用户仍是 0。
func (f *teamAuthFixture) session(t *testing.T, user *model.User) string {
	t.Helper()
	bundle, err := service.CreateLoginSessionOnSite(user.Id, teamAuthIssuedSite(t, f.db, user), "password", "127.0.0.1", "team-auth-test")
	require.NoError(t, err)
	require.NotEmpty(t, bundle.AccessToken)
	return bundle.AccessToken
}

func teamAuthIssuedSite(t *testing.T, db *gorm.DB, user *model.User) int {
	t.Helper()
	if user == nil {
		return 0
	}
	if user.SiteId != 0 {
		return user.SiteId
	}
	var site model.AdtySite
	if err := db.Where("owner_user_id = ?", user.Id).Limit(1).Find(&site).Error; err != nil {
		t.Fatalf("look up owned site for %s: %v", user.Username, err)
	}
	return site.Id
}

func (f *teamAuthFixture) siteAHost() string { return service.AdtySiteDisplayHost(f.siteA) }
func (f *teamAuthFixture) siteBHost() string { return service.AdtySiteDisplayHost(f.siteB) }

func (f *teamAuthFixture) call(t *testing.T, method, host, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	return adtyJSONOnHost(t, f.engine, host, method, path, body, token)
}

func (f *teamAuthFixture) teamCount(t *testing.T) int64 {
	t.Helper()
	var n int64
	require.NoError(t, f.db.Model(&model.AdtyTeam{}).Count(&n).Error)
	return n
}

func (f *teamAuthFixture) decodeCreated(t *testing.T, rec *httptest.ResponseRecorder) teamAuthCreated {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return decodeAdtyData[teamAuthCreated](t, rec.Body)
}

func teamAuthRejected(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var envelope struct {
		Success bool `json:"success"`
	}
	if rec.Body.Len() > 0 && json.Valid(rec.Body.Bytes()) {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
		assert.False(t, envelope.Success, rec.Body.String())
		return
	}
	assert.NotEqual(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestOrdinaryUserCannotSelfCreateTeamOrDiscount(t *testing.T) {
	f := newTeamAuthFixture(t)
	start := f.teamCount(t)
	body := `{"name":"Self Serve","discount":"0.50"}`
	for _, tc := range []struct{ name, host, token string }{
		{"main user on main host", teamAuthMainHost, f.mainUserTok},
		{"site customer on own host", f.siteAHost(), f.userATok},
		{"site leader on own host", f.siteAHost(), f.leaderATok},
		{"staff on own host", f.siteAHost(), f.staffATok},
		{"owner on own host via old path", f.siteAHost(), f.ownerATok},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.call(t, http.MethodPost, tc.host, "/api/adty/teams", body, tc.token)
			teamAuthRejected(t, rec)
		})
	}
	assert.Equal(t, start, f.teamCount(t))
}

func TestRootCreatesMainSiteTeamWithDesignatedLeader(t *testing.T) {
	f := newTeamAuthFixture(t)
	rec := f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams",
		fmt.Sprintf(`{"name":"Main Desk","leader_id":%d,"discount":"0.80"}`, f.mainLeader.Id), f.rootTok)
	created := f.decodeCreated(t, rec)
	assert.Equal(t, 0, created.Team.SiteId)
	assert.Equal(t, f.mainLeader.Id, created.Team.LeaderUserId)
	assert.NotEqual(t, f.root.Id, created.Team.LeaderUserId)
	assert.True(t, created.Team.Discount.Equal(decimal.RequireFromString("0.80")))
	assert.Equal(t, model.AdtyTeamRoleLeader, created.Member.Role)
	assert.Equal(t, f.mainLeader.Id, created.Member.UserId)

	defaulted := f.decodeCreated(t, f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams",
		fmt.Sprintf(`{"name":"Default Price","leader_id":%d}`, f.mainUser.Id), f.rootTok))
	assert.Equal(t, 0, defaulted.Team.SiteId)
	assert.Equal(t, f.mainUser.Id, defaulted.Team.LeaderUserId)
	assert.True(t, defaulted.Team.Discount.Equal(decimal.NewFromInt(1)))

	denied := f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams",
		fmt.Sprintf(`{"name":"Not Root","leader_id":%d,"discount":"0.80"}`, f.mainLeader.Id), f.mainUserTok)
	teamAuthRejected(t, denied)
}

func TestOnlyRootCreatesSiteTeamWithDesignatedLeader(t *testing.T) {
	f := newTeamAuthFixture(t)
	start := f.teamCount(t)

	teamAuthRejected(t, f.call(t, http.MethodPost, f.siteAHost(), "/api/adty/site/teams",
		fmt.Sprintf(`{"name":"Owner Denied","leader_id":%d,"discount":"0.90"}`, f.leaderA.Id), f.ownerATok))
	rec := f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams",
		fmt.Sprintf(`{"name":"Site Desk","site_id":%d,"leader_id":%d,"discount":"0.90"}`, f.siteA.Id, f.leaderA.Id), f.rootTok)
	created := f.decodeCreated(t, rec)
	assert.Equal(t, f.siteA.Id, created.Team.SiteId)
	assert.NotEqual(t, 0, created.Team.SiteId)
	assert.Equal(t, f.leaderA.Id, created.Team.LeaderUserId)
	assert.NotEqual(t, f.ownerA.Id, created.Team.LeaderUserId)
	assert.True(t, created.Team.Discount.Equal(decimal.RequireFromString("0.90")))

	// Owner cannot use the Root main-site create path.
	teamAuthRejected(t, f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams",
		fmt.Sprintf(`{"name":"Main From Owner","leader_id":%d,"discount":"1"}`, f.mainLeader.Id), f.ownerATok))

	// Other site's owner, staff, and ordinary users cannot create on this site.
	for _, tc := range []struct{ name, host, token string }{
		{"other owner on site A host", f.siteAHost(), f.ownerBTok},
		{"other owner creating via own site does not touch site A", f.siteBHost(), f.ownerBTok},
		{"staff on site A", f.siteAHost(), f.staffATok},
		{"customer on site A", f.siteAHost(), f.userATok},
		{"main user on site A", f.siteAHost(), f.mainUserTok},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := f.teamCount(t)
			body := fmt.Sprintf(`{"name":"Denied","leader_id":%d,"discount":"0.90"}`, f.leaderA.Id)
			// Owner B on site B would designate a site-A leader, which must fail.
			if tc.token == f.ownerBTok && tc.host == f.siteBHost() {
				body = fmt.Sprintf(`{"name":"Denied","leader_id":%d,"discount":"0.90"}`, f.leaderA.Id)
			}
			teamAuthRejected(t, f.call(t, http.MethodPost, tc.host, "/api/adty/site/teams", body, tc.token))
			if tc.token == f.ownerBTok && tc.host == f.siteBHost() {
				assert.Equal(t, before, f.teamCount(t), "owner B must not create a team with a site A leader")
			}
		})
	}
	assert.Equal(t, start+1, f.teamCount(t))
}

func TestDesignatedLeaderMustBeEligibleSameSiteUser(t *testing.T) {
	f := newTeamAuthFixture(t)
	start := f.teamCount(t)
	ok := f.decodeCreated(t, f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams",
		fmt.Sprintf(`{"name":"Site Desk","site_id":%d,"leader_id":%d,"discount":"0.90"}`, f.siteA.Id, f.leaderA.Id), f.rootTok))
	require.Equal(t, f.leaderA.Id, ok.Team.LeaderUserId)

	for _, tc := range []struct {
		name     string
		leaderID int
	}{
		{"missing user", 9_000_001},
		{"other site user", f.leaderB.Id},
		{"staff", f.staffA.Id},
		{"already leads a team", f.leaderA.Id},
		{"main-site user", f.mainLeader.Id},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"name":"Bad Leader","site_id":%d,"leader_id":%d,"discount":"0.90"}`, f.siteA.Id, tc.leaderID)
			rec := f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams", body, f.rootTok)
			teamAuthRejected(t, rec)
			if tc.name == "missing user" || tc.name == "other site user" {
				assert.NotContains(t, rec.Body.String(), "record not found")
			}
		})
	}

	// A current member also cannot be designated as a new team's leader.
	invite := f.call(t, http.MethodPost, f.siteAHost(), fmt.Sprintf("/api/adty/teams/%d/invites", ok.Team.Id), "", f.leaderATok)
	require.Equal(t, http.StatusOK, invite.Code, invite.Body.String())
	code := decodeAdtyData[struct {
		Code string `json:"code"`
	}](t, invite.Body).Code
	teamAuthRejected(t, f.call(t, http.MethodPost, f.siteAHost(), "/api/adty/teams/accept", fmt.Sprintf(`{"code":%q}`, code), f.memberATok))
	registered := f.call(t, http.MethodPost, f.siteAHost(), "/api/adty/team-employees/register",
		fmt.Sprintf(`{"code":%q,"username":"already-member","password":"Employee-password-2026"}`, code), "")
	require.Equal(t, http.StatusOK, registered.Code, registered.Body.String())
	employee := decodeAdtyData[struct {
		UserID int `json:"user_id"`
	}](t, registered.Body)
	teamAuthRejected(t, f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams",
		fmt.Sprintf(`{"name":"Already Member","site_id":%d,"leader_id":%d,"discount":"0.90"}`, f.siteA.Id, employee.UserID), f.rootTok))
	assert.Equal(t, start+1, f.teamCount(t))
}

func TestOnlyRootOrAuthorizedOwnerCanSetProtocolDiscount(t *testing.T) {
	f := newTeamAuthFixture(t)
	mainTeam := f.decodeCreated(t, f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams",
		fmt.Sprintf(`{"name":"Main Desk","leader_id":%d,"discount":"1"}`, f.mainLeader.Id), f.rootTok))
	siteTeam := f.decodeCreated(t, f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams",
		fmt.Sprintf(`{"name":"Site Desk","site_id":%d,"leader_id":%d,"discount":"0.90"}`, f.siteA.Id, f.leaderA.Id), f.rootTok))

	okMain := f.call(t, http.MethodPut, teamAuthMainHost, fmt.Sprintf("/api/adty/admin/teams/%d/agreement-price", mainTeam.Team.Id),
		`{"discount":"0.70"}`, f.rootTok)
	require.Equal(t, http.StatusOK, okMain.Code, okMain.Body.String())
	okSite := f.call(t, http.MethodPut, f.siteAHost(), fmt.Sprintf("/api/adty/site/teams/%d/discount", siteTeam.Team.Id),
		`{"discount":"0.85"}`, f.ownerATok)
	require.Equal(t, http.StatusOK, okSite.Code, okSite.Body.String())

	teamAuthRejected(t, f.call(t, http.MethodPut, teamAuthMainHost, fmt.Sprintf("/api/adty/admin/teams/%d/agreement-price", mainTeam.Team.Id),
		`{"discount":"0.60"}`, f.mainUserTok))
	teamAuthRejected(t, f.call(t, http.MethodPut, teamAuthMainHost, fmt.Sprintf("/api/adty/admin/teams/%d/agreement-price", mainTeam.Team.Id),
		`{"discount":"0.60"}`, f.mainLeaderTok))
	teamAuthRejected(t, f.call(t, http.MethodPut, f.siteAHost(), fmt.Sprintf("/api/adty/site/teams/%d/discount", siteTeam.Team.Id),
		`{"discount":"0.95"}`, f.userATok))
	teamAuthRejected(t, f.call(t, http.MethodPut, f.siteAHost(), fmt.Sprintf("/api/adty/site/teams/%d/discount", siteTeam.Team.Id),
		`{"discount":"0.95"}`, f.leaderATok))
	teamAuthRejected(t, f.call(t, http.MethodPut, f.siteAHost(), fmt.Sprintf("/api/adty/site/teams/%d/discount", siteTeam.Team.Id),
		`{"discount":"0.95"}`, f.staffATok))
	teamAuthRejected(t, f.call(t, http.MethodPut, f.siteAHost(), fmt.Sprintf("/api/adty/site/teams/%d/discount", siteTeam.Team.Id),
		`{"discount":"0.95"}`, f.ownerBTok))
	// Root agreement-price is main-site only.
	teamAuthRejected(t, f.call(t, http.MethodPut, teamAuthMainHost, fmt.Sprintf("/api/adty/admin/teams/%d/agreement-price", siteTeam.Team.Id),
		`{"discount":"0.70"}`, f.rootTok))

	var storedMain, storedSite model.AdtyTeam
	require.NoError(t, f.db.First(&storedMain, mainTeam.Team.Id).Error)
	require.NoError(t, f.db.First(&storedSite, siteTeam.Team.Id).Error)
	assert.True(t, storedMain.Discount.Equal(decimal.RequireFromString("0.70")))
	assert.True(t, storedSite.Discount.Equal(decimal.RequireFromString("0.85")))
}

func TestAuthorizedTeamInviteAcceptAndMineStillWork(t *testing.T) {
	f := newTeamAuthFixture(t)
	created := f.decodeCreated(t, f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams",
		fmt.Sprintf(`{"name":"Site Desk","site_id":%d,"leader_id":%d,"discount":"0.90"}`, f.siteA.Id, f.leaderA.Id), f.rootTok))

	mine := f.call(t, http.MethodGet, f.siteAHost(), "/api/adty/teams/mine", "", f.leaderATok)
	require.Equal(t, http.StatusOK, mine.Code, mine.Body.String())
	assert.Contains(t, mine.Body.String(), "Site Desk")
	assert.Contains(t, mine.Body.String(), `"role":"leader"`)

	invite := f.call(t, http.MethodPost, f.siteAHost(), fmt.Sprintf("/api/adty/teams/%d/invites", created.Team.Id), "", f.leaderATok)
	require.Equal(t, http.StatusOK, invite.Code, invite.Body.String())
	code := decodeAdtyData[struct {
		Code string `json:"code"`
	}](t, invite.Body).Code
	require.NotEmpty(t, code)

	teamAuthRejected(t, f.call(t, http.MethodPost, f.siteAHost(), "/api/adty/teams/accept", fmt.Sprintf(`{"code":%q}`, code), f.memberATok))
	registered := f.call(t, http.MethodPost, f.siteAHost(), "/api/adty/team-employees/register",
		fmt.Sprintf(`{"code":%q,"username":"invite-employee","password":"Employee-password-2026"}`, code), "")
	require.Equal(t, http.StatusOK, registered.Code, registered.Body.String())
	employeeID := decodeAdtyData[struct {
		UserID int `json:"user_id"`
	}](t, registered.Body).UserID
	var employee model.User
	require.NoError(t, f.db.First(&employee, employeeID).Error)

	memberMine := f.call(t, http.MethodGet, f.siteAHost(), "/api/adty/teams/mine", "", f.session(t, &employee))
	require.Equal(t, http.StatusOK, memberMine.Code, memberMine.Body.String())
	assert.Contains(t, memberMine.Body.String(), "Site Desk")
	assert.Contains(t, memberMine.Body.String(), `"role":"member"`)
}

func TestAuthorizedTeamInviteTTLAndRevoke(t *testing.T) {
	f := newTeamAuthFixture(t)
	created := f.decodeCreated(t, f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams",
		fmt.Sprintf(`{"name":"Invite Desk","site_id":%d,"leader_id":%d,"discount":"0.90"}`, f.siteA.Id, f.leaderA.Id), f.rootTok))

	now := time.Now().Unix()
	defaultInvite := f.call(t, http.MethodPost, f.siteAHost(), fmt.Sprintf("/api/adty/teams/%d/invites", created.Team.Id), "", f.leaderATok)
	require.Equal(t, http.StatusOK, defaultInvite.Code, defaultInvite.Body.String())
	defaultIssued := decodeAdtyData[service.AdtyTeamInviteIssue](t, defaultInvite.Body)
	require.NotEmpty(t, defaultIssued.Code)
	assert.InDelta(t, now+int64(service.AdtyTeamInviteDefaultTTL.Seconds()), defaultIssued.ExpiresAt, 5)
	assert.NotContains(t, defaultInvite.Body.String(), "code_hash")

	customInvite := f.call(t, http.MethodPost, f.siteAHost(), fmt.Sprintf("/api/adty/teams/%d/invites", created.Team.Id), `{"ttl_seconds":3600}`, f.leaderATok)
	require.Equal(t, http.StatusOK, customInvite.Code, customInvite.Body.String())
	customIssued := decodeAdtyData[service.AdtyTeamInviteIssue](t, customInvite.Body)
	assert.InDelta(t, now+3600, customIssued.ExpiresAt, 5)
	assert.Less(t, customIssued.ExpiresAt, defaultIssued.ExpiresAt)

	teamAuthRejected(t, f.call(t, http.MethodPost, f.siteAHost(), fmt.Sprintf("/api/adty/teams/%d/invites", created.Team.Id), `{"ttl_seconds":0}`, f.leaderATok))
	teamAuthRejected(t, f.call(t, http.MethodPost, f.siteAHost(), fmt.Sprintf("/api/adty/teams/%d/invites", created.Team.Id), `{"ttl_seconds":-1}`, f.leaderATok))

	toRevoke := f.call(t, http.MethodPost, f.siteAHost(), fmt.Sprintf("/api/adty/teams/%d/invites", created.Team.Id), `{"ttl_seconds":7200}`, f.leaderATok)
	require.Equal(t, http.StatusOK, toRevoke.Code, toRevoke.Body.String())
	revocable := decodeAdtyData[service.AdtyTeamInviteIssue](t, toRevoke.Body)

	for _, tc := range []struct{ name, token string }{
		{"member", f.memberATok},
		{"ordinary user", f.userATok},
		{"staff", f.staffATok},
		{"other site leader", f.leaderBTok},
		{"site owner", f.ownerATok},
		{"root", f.rootTok},
	} {
		t.Run("unauthorized revoke "+tc.name, func(t *testing.T) {
			teamAuthRejected(t, f.call(t, http.MethodPost, f.siteAHost(),
				fmt.Sprintf("/api/adty/teams/%d/invites/%d/revoke", created.Team.Id, revocable.Id), "", tc.token))
		})
		t.Run("unauthorized list "+tc.name, func(t *testing.T) {
			teamAuthRejected(t, f.call(t, http.MethodGet, f.siteAHost(),
				fmt.Sprintf("/api/adty/teams/%d/invites", created.Team.Id), "", tc.token))
		})
	}

	revoked := f.call(t, http.MethodPost, f.siteAHost(),
		fmt.Sprintf("/api/adty/teams/%d/invites/%d/revoke", created.Team.Id, revocable.Id), "", f.leaderATok)
	require.Equal(t, http.StatusOK, revoked.Code, revoked.Body.String())
	view := decodeAdtyData[service.AdtyTeamInviteView](t, revoked.Body)
	assert.Equal(t, model.AdtyTeamInviteStatusRevoked, view.Status)
	assert.Equal(t, f.leaderA.Id, view.CreatedBy)
	assert.Equal(t, f.leaderA.Id, view.RevokedBy)
	teamAuthRejected(t, f.call(t, http.MethodPost, f.siteAHost(), "/api/adty/teams/accept",
		fmt.Sprintf(`{"code":%q}`, revocable.Code), f.memberATok))
	teamAuthRejected(t, f.call(t, http.MethodPost, f.siteAHost(), "/api/adty/team-employees/register",
		fmt.Sprintf(`{"code":%q,"username":"revoked-employee","password":"Employee-password-2026"}`, revocable.Code), ""))

	usedInvite := f.call(t, http.MethodPost, f.siteAHost(), fmt.Sprintf("/api/adty/teams/%d/invites", created.Team.Id), "", f.leaderATok)
	require.Equal(t, http.StatusOK, usedInvite.Code, usedInvite.Body.String())
	used := decodeAdtyData[service.AdtyTeamInviteIssue](t, usedInvite.Body)
	registered := f.call(t, http.MethodPost, f.siteAHost(), "/api/adty/team-employees/register",
		fmt.Sprintf(`{"code":%q,"username":"used-employee","password":"Employee-password-2026"}`, used.Code), "")
	require.Equal(t, http.StatusOK, registered.Code, registered.Body.String())
	employeeID := decodeAdtyData[struct {
		UserID int `json:"user_id"`
	}](t, registered.Body).UserID
	teamAuthRejected(t, f.call(t, http.MethodPost, f.siteAHost(),
		fmt.Sprintf("/api/adty/teams/%d/invites/%d/revoke", created.Team.Id, used.Id), "", f.leaderATok))
	var employee model.User
	require.NoError(t, f.db.First(&employee, employeeID).Error)
	memberMine := f.call(t, http.MethodGet, f.siteAHost(), "/api/adty/teams/mine", "", f.session(t, &employee))
	require.Equal(t, http.StatusOK, memberMine.Code, memberMine.Body.String())
	assert.Contains(t, memberMine.Body.String(), `"role":"member"`)

	listed := f.call(t, http.MethodGet, f.siteAHost(), fmt.Sprintf("/api/adty/teams/%d/invites", created.Team.Id), "", f.leaderATok)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	assert.Contains(t, listed.Body.String(), `"status":"revoked"`)
	assert.Contains(t, listed.Body.String(), `"status":"used"`)
	assert.Contains(t, listed.Body.String(), `"status":"outstanding"`)
	assert.NotContains(t, listed.Body.String(), "code_hash")
	assert.NotContains(t, listed.Body.String(), revocable.Code)
	assert.NotContains(t, listed.Body.String(), used.Code)
}

func TestTeamRepairBindRejectsWrongTargetWithoutWriting(t *testing.T) {
	f := newTeamAuthFixture(t)
	created := f.decodeCreated(t, f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams",
		fmt.Sprintf(`{"name":"Target Desk","leader_id":%d}`, f.mainLeader.Id), f.rootTok))
	key := &model.Token{UserId: f.mainLeader.Id, Key: "team-target-key", Name: "Target key", Status: common.TokenStatusEnabled, ExpiredTime: -1}
	require.NoError(t, f.db.Create(key).Error)
	body := fmt.Sprintf(`{"token_id":%d}`, key.Id)
	teamAuthRejected(t, f.call(t, http.MethodPost, teamAuthMainHost,
		fmt.Sprintf("/api/adty/teams/%d/keys", created.Team.Id+9000), body, f.mainLeaderTok))
	var count int64
	require.NoError(t, f.db.Model(&model.AdtyTeamKey{}).Where("token_id = ?", key.Id).Count(&count).Error)
	assert.Zero(t, count)
	bound := f.call(t, http.MethodPost, teamAuthMainHost, fmt.Sprintf("/api/adty/teams/%d/keys", created.Team.Id), body, f.mainLeaderTok)
	require.Contains(t, bound.Body.String(), `"success":true`)
	var binding model.AdtyTeamKey
	require.NoError(t, f.db.Where("token_id = ?", key.Id).First(&binding).Error)
	assert.Equal(t, created.Team.Id, binding.TeamId)
}

func TestTeamRepairEmployeeLifecycleDoesNotMintPersonalFunds(t *testing.T) {
	f := newTeamAuthFixture(t)
	savedGift := common.QuotaForNewUser
	common.QuotaForNewUser = 500_000
	t.Cleanup(func() { common.QuotaForNewUser = savedGift })
	created := f.decodeCreated(t, f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams",
		fmt.Sprintf(`{"name":"Employee Desk","site_id":%d,"leader_id":%d}`, f.siteA.Id, f.leaderA.Id), f.rootTok))
	path := fmt.Sprintf("/api/adty/teams/%d/employees", created.Team.Id)
	body := `{"username":"team-employee-one","password":"Employee-password-2026","display_name":"Employee One"}`
	teamAuthRejected(t, f.call(t, http.MethodPost, f.siteAHost(), path, body, f.userATok))
	rec := f.call(t, http.MethodPost, f.siteAHost(), path, body, f.leaderATok)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	view := decodeAdtyData[struct { UserID int `json:"user_id"`; Independent bool `json:"independent_employee"` }](t, rec.Body)
	require.Positive(t, view.UserID)
	assert.True(t, view.Independent)
	var user model.User
	require.NoError(t, f.db.First(&user, view.UserID).Error)
	assert.Zero(t, user.Quota)
	assert.Zero(t, user.QuotaPending)
	assert.Zero(t, user.StaffSiteId)
	assert.Equal(t, f.siteA.Id, user.SiteId)
	assert.True(t, common.ValidatePasswordAndHash("Employee-password-2026", user.Password))
	assert.NotContains(t, rec.Body.String(), "Employee-password-2026")
	oldSession := f.session(t, &user)
	key := &model.Token{UserId: user.Id, Key: "team-employee-key", Name: "Employee key", Status: common.TokenStatusEnabled, ExpiredTime: -1}
	require.NoError(t, f.db.Create(key).Error)
	bound := f.call(t, http.MethodPost, f.siteAHost(), fmt.Sprintf("/api/adty/teams/%d/keys", created.Team.Id), fmt.Sprintf(`{"token_id":%d}`, key.Id), oldSession)
	require.Contains(t, bound.Body.String(), `"success":true`)
	memberPath := fmt.Sprintf("/api/adty/teams/%d/members/%d", created.Team.Id, user.Id)
	disabled := f.call(t, http.MethodPost, f.siteAHost(), memberPath+"/disable", "", f.leaderATok)
	require.Contains(t, disabled.Body.String(), `"success":true`)
	teamAuthRejected(t, f.call(t, http.MethodGet, f.siteAHost(), "/api/adty/teams/mine", "", oldSession))
	enabled := f.call(t, http.MethodPost, f.siteAHost(), memberPath+"/enable", "", f.leaderATok)
	require.Contains(t, enabled.Body.String(), `"success":true`)
	teamAuthRejected(t, f.call(t, http.MethodGet, f.siteAHost(), "/api/adty/teams/mine", "", oldSession))
	require.NoError(t, f.db.First(key, key.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, key.Status)
	deleted := f.call(t, http.MethodPost, f.siteAHost(), memberPath+"/delete", "", f.leaderATok)
	require.Contains(t, deleted.Body.String(), `"success":true`)
	teamAuthRejected(t, f.call(t, http.MethodPost, f.siteAHost(), memberPath+"/enable", "", f.leaderATok))
	require.NoError(t, f.db.First(&user, user.Id).Error)
	assert.Equal(t, common.UserStatusDisabled, user.Status)
	var member model.AdtyTeamMember
	require.NoError(t, f.db.Where("team_id = ? AND user_id = ?", created.Team.Id, user.Id).First(&member).Error)
	assert.Equal(t, "deleted", member.Status)
}

func TestTeamRepairAnonymousInviteRegistrationIsSingleUseAndHostBound(t *testing.T) {
	f := newTeamAuthFixture(t)
	created := f.decodeCreated(t, f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams",
		fmt.Sprintf(`{"name":"Invite Employee Desk","site_id":%d,"leader_id":%d}`, f.siteA.Id, f.leaderA.Id), f.rootTok))
	issued := decodeAdtyData[service.AdtyTeamInviteIssue](t, f.call(t, http.MethodPost, f.siteAHost(),
		fmt.Sprintf("/api/adty/teams/%d/invites", created.Team.Id), "", f.leaderATok).Body)
	body := fmt.Sprintf(`{"code":%q,"username":"invited-employee","password":"Invite-password-2026"}`, issued.Code)
	path := "/api/adty/team-employees/register"
	teamAuthRejected(t, f.call(t, http.MethodPost, f.siteBHost(), path, body, ""))
	registered := f.call(t, http.MethodPost, f.siteAHost(), path, body, "")
	require.Equal(t, http.StatusOK, registered.Code, registered.Body.String())
	view := decodeAdtyData[struct { UserID int `json:"user_id"`; Independent bool `json:"independent_employee"` }](t, registered.Body)
	require.Positive(t, view.UserID)
	assert.True(t, view.Independent)
	teamAuthRejected(t, f.call(t, http.MethodPost, f.siteAHost(), path, strings.Replace(body, "invited-employee", "replayed-employee", 1), ""))
	var invite model.AdtyTeamInvite
	require.NoError(t, f.db.First(&invite, issued.Id).Error)
	assert.Equal(t, view.UserID, invite.UsedBy)
	var user model.User
	require.NoError(t, f.db.First(&user, view.UserID).Error)
	assert.Zero(t, user.Quota)
	assert.Equal(t, f.siteA.Id, user.SiteId)
}

func TestTeamRepairLeaderLeaveClosesAndPreservesSharedPrincipal(t *testing.T) {
	f := newTeamAuthFixture(t)
	created := f.decodeCreated(t, f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams",
		fmt.Sprintf(`{"name":"Closing Desk","leader_id":%d}`, f.mainLeader.Id), f.rootTok))
	require.NoError(t, service.CreditAdtyTeam(int64(f.mainLeader.Id), created.Team.Id, 50_000_000, "team-leave-principal"))
	key := &model.Token{UserId: f.mainLeader.Id, Key: "team-leave-key", Name: "Leaving key", Status: common.TokenStatusEnabled, ExpiredTime: -1}
	require.NoError(t, f.db.Create(key).Error)
	bound := f.call(t, http.MethodPost, teamAuthMainHost, fmt.Sprintf("/api/adty/teams/%d/keys", created.Team.Id), fmt.Sprintf(`{"token_id":%d}`, key.Id), f.mainLeaderTok)
	require.Contains(t, bound.Body.String(), `"success":true`)
	left := f.call(t, http.MethodPost, teamAuthMainHost, fmt.Sprintf("/api/adty/teams/%d/leave", created.Team.Id), "", f.mainLeaderTok)
	require.Contains(t, left.Body.String(), `"success":true`)
	var team model.AdtyTeam
	require.NoError(t, f.db.First(&team, created.Team.Id).Error)
	assert.Equal(t, model.AdtyTeamStatusClosed, team.Status)
	assert.Equal(t, int64(50_000_000), team.SharedQuota)
	assert.Equal(t, f.mainLeader.Id, team.LeaderUserId)
	require.NoError(t, f.db.First(key, key.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, key.Status)
	var active int64
	require.NoError(t, f.db.Model(&model.AdtyTeamMember{}).Where("team_id = ? AND status = ?", team.Id, model.AdtyTeamMemberActive).Count(&active).Error)
	assert.Zero(t, active)
}

func TestTeamRepairTopupKeyDoesNotCreateAnotherProviderOrder(t *testing.T) {
	f := newTeamAuthFixture(t)
	oldMin, oldPrice := operation_setting.MinTopUp, operation_setting.Price
	operation_setting.MinTopUp, operation_setting.Price = 1, 7
	t.Cleanup(func() { operation_setting.MinTopUp, operation_setting.Price = oldMin, oldPrice })
	created := f.decodeCreated(t, f.call(t, http.MethodPost, teamAuthMainHost, "/api/adty/admin/teams",
		fmt.Sprintf(`{"name":"Payment Desk","leader_id":%d}`, f.mainLeader.Id), f.rootTok))
	epay := newFakeEpay(t, epayQR)
	channel := seedRouterEpayChannel(t, "team-repair", epay.server.URL, model.AdtyEpayOrderModeAPI, 1)
	bepusdt := useFakeBEpusdt(t)
	path := fmt.Sprintf("/api/adty/teams/%d/topups", created.Team.Id)
	request := func(body, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Host = teamAuthMainHost
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+f.mainLeaderTok)
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		f.engine.ServeHTTP(rec, req)
		return rec
	}
	body := fmt.Sprintf(`{"amount_usd":"100","method":"alipay","channel_id":%d}`, channel.Id)
	first := request(body, "one-team-checkout")
	require.Contains(t, first.Body.String(), `"success":true`)
	order := decodeAdtyData[model.AdtyTopupOrder](t, first.Body)
	replayed := request(body, "one-team-checkout")
	require.Contains(t, replayed.Body.String(), `"success":true`)
	assert.Equal(t, order.TradeNo, decodeAdtyData[model.AdtyTopupOrder](t, replayed.Body).TradeNo)
	autoReplayed := request(`{"amount_usd":"100","method":"alipay"}`, "one-team-checkout")
	require.Contains(t, autoReplayed.Body.String(), `"success":true`)
	assert.Equal(t, order.TradeNo, decodeAdtyData[model.AdtyTopupOrder](t, autoReplayed.Body).TradeNo)
	teamAuthRejected(t, request(fmt.Sprintf(`{"amount_usd":"100","method":"alipay","channel_id":%d}`, channel.Id+1000), "one-team-checkout"))
	teamAuthRejected(t, request(`{"amount_usd":"101","method":"alipay"}`, "one-team-checkout"))
	teamAuthRejected(t, request(`{"amount_usd":"100","method":"wxpay"}`, "one-team-checkout"))
	teamAuthRejected(t, request(`{"amount_usd":"100","method":"usdt.trc20"}`, "one-team-checkout"))
	teamAuthRejected(t, request(`{"amount_usd":"100"}`, "one-team-checkout"))
	var count int64
	require.NoError(t, f.db.Model(&model.AdtyTopupOrder{}).Where("team_id = ?", created.Team.Id).Count(&count).Error)
	assert.Equal(t, int64(1), count)
	assert.Zero(t, bepusdt.snapshot().creates)
	epay.mu.Lock()
	assert.Len(t, epay.orders, 1)
	epay.mu.Unlock()
}

func TestTeamRepairEmployeeAuditSurvivesLogStoreOutage(t *testing.T) {
	f := newTeamAuthFixture(t)
	require.NoError(t, f.db.AutoMigrate(&model.AuditIntent{}))
	team, err := service.CreateAdtyTeam(f.leaderA.Id, f.siteA.Id, "Audited employees", decimal.NewFromInt(1))
	require.NoError(t, err)
	logDB := model.LOG_DB
	model.LOG_DB = nil
	t.Cleanup(func() { model.LOG_DB = logDB })
	employee, err := service.CreateAdtyTeamEmployee(f.leaderA.Id, team.Team.Id, "audited-employee", "Employee-password-2026", "Employee")
	require.NoError(t, err)
	require.NoError(t, service.DisableAdtyTeamMember(f.leaderA.Id, team.Team.Id, employee.UserId))
	require.NoError(t, service.EnableAdtyTeamMember(f.leaderA.Id, team.Team.Id, employee.UserId))
	require.NoError(t, service.DeleteAdtyTeamMember(f.leaderA.Id, team.Team.Id, employee.UserId))
	var intents []model.AuditIntent
	require.NoError(t, f.db.Order("id").Find(&intents).Error)
	require.Len(t, intents, 4, "employee mutations must retain audit evidence in the primary transaction")
	var actions []string
	for _, intent := range intents {
		var event model.AuditLog
		require.NoError(t, common.UnmarshalJsonStr(intent.Payload, &event))
		assert.Equal(t, f.leaderA.Id, event.UserId)
		require.NotNil(t, event.SiteId)
		assert.Equal(t, f.siteA.Id, *event.SiteId)
		assert.Equal(t, fmt.Sprint(employee.UserId), event.ObjectId)
		assert.Equal(t, model.AuditResultSuccess, event.Result)
		if event.Action == "adty.team.member.enable" {
			assert.Contains(t, event.BeforeValue, `"status":"disabled"`)
			assert.Contains(t, event.AfterValue, `"status":"active"`)
		}
		assert.NotContains(t, intent.Payload, "Employee-password-2026")
		assert.Zero(t, intent.DeliveredAt)
		actions = append(actions, event.Action)
	}
	assert.Equal(t, []string{"adty.team.member.create", "adty.team.member.disable", "adty.team.member.enable", "adty.team.member.delete"}, actions)
	model.LOG_DB = logDB
	for _, intent := range intents {
		require.NoError(t, model.DeliverAuditIntent(context.Background(), intent.EventID))
		require.NoError(t, model.DeliverAuditIntent(context.Background(), intent.EventID))
	}
	var count int64
	require.NoError(t, logDB.Model(&model.AuditLog{}).Where("action IN ? AND object_id = ?", actions, fmt.Sprint(employee.UserId)).Count(&count).Error)
	assert.Equal(t, int64(4), count)
}

func TestTeamRepairAuditFailureRollsBackEmployeeAndInvite(t *testing.T) {
	f := newTeamAuthFixture(t)
	require.NoError(t, f.db.AutoMigrate(&model.AuditIntent{}))
	team, err := service.CreateAdtyTeam(f.leaderA.Id, f.siteA.Id, "Atomic employees", decimal.NewFromInt(1))
	require.NoError(t, err)
	employee, err := service.CreateAdtyTeamEmployee(f.leaderA.Id, team.Team.Id, "atomic-employee", "Employee-password-2026", "Employee")
	require.NoError(t, err)
	invite, err := service.CreateAdtyTeamInvite(f.leaderA.Id, team.Team.Id, time.Hour)
	require.NoError(t, err)
	const callback = "test:team-audit-intent-failure"
	require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_intents" {
			tx.AddError(assert.AnError)
		}
	}))
	t.Cleanup(func() { require.NoError(t, f.db.Callback().Create().Remove(callback)) })
	_, err = service.CreateAdtyTeamEmployee(f.leaderA.Id, team.Team.Id, "must-not-exist", "Employee-password-2026", "")
	require.Error(t, err)
	_, err = service.RegisterAdtyTeamEmployee(invite.Code, "must-not-register", "Employee-password-2026", "", f.siteA.Id)
	require.Error(t, err)
	require.Error(t, service.DisableAdtyTeamMember(f.leaderA.Id, team.Team.Id, employee.UserId))
	require.Error(t, service.DeleteAdtyTeamMember(f.leaderA.Id, team.Team.Id, employee.UserId))
	var count int64
	require.NoError(t, f.db.Model(&model.User{}).Where("username IN ?", []string{"must-not-exist", "must-not-register"}).Count(&count).Error)
	assert.Zero(t, count)
	var storedInvite model.AdtyTeamInvite
	require.NoError(t, f.db.First(&storedInvite, invite.Id).Error)
	assert.Zero(t, storedInvite.UsedBy)
	var member model.AdtyTeamMember
	require.NoError(t, f.db.Where("user_id = ?", employee.UserId).Take(&member).Error)
	assert.Equal(t, model.AdtyTeamMemberActive, member.Status)
	var user model.User
	require.NoError(t, f.db.First(&user, employee.UserId).Error)
	assert.Equal(t, common.UserStatusEnabled, user.Status)
	assert.Equal(t, int64(1), user.AuthVersion)
}

func TestTeamRepairLegacyTopupRejectsProviderChange(t *testing.T) {
	f := newTeamAuthFixture(t)
	team, err := service.CreateAdtyTeam(f.mainLeader.Id, 0, "Legacy checkout", decimal.NewFromInt(1))
	require.NoError(t, err)
	const key = "legacy-endpoint-key"
	legacy := &model.AdtyTopupOrder{TradeNo: "TTEP" + common.Sha1([]byte(key))[:20], TeamId: team.Team.Id, UserId: f.mainLeader.Id,
		Provider: model.PaymentProviderAdtyEpay, Method: "alipay", FaceUSD: decimal.NewFromInt(100)}
	require.NoError(t, f.db.Create(legacy).Error)
	_, err = service.CreateAdtyTeamTopupOrder(f.mainLeader.Id, team.Team.Id, decimal.NewFromInt(100), key)
	require.Error(t, err, "historical Epay orders claim the same endpoint key as direct topups")
	var count int64
	require.NoError(t, f.db.Model(&model.AdtyTopupOrder{}).Where("team_id = ?", team.Team.Id).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestTeamRepairCrossProviderConcurrentCreateClaimsOneOrder(t *testing.T) {
	f := newTeamAuthFixture(t)
	team, err := service.CreateAdtyTeam(f.mainLeader.Id, 0, "Concurrent checkout", decimal.NewFromInt(1))
	require.NoError(t, err)
	const suffix = "0123456789abcdef0123"
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, method := range []struct{ prefix, provider, method string }{
		{"TTEP", model.PaymentProviderAdtyEpay, "alipay"},
		{"TTU", model.PaymentProviderAdtyBEpusdt, "usdt.trc20"},
	} {
		group.Go(func() {
			<-start
			order := &model.TopUp{TradeNo: method.prefix + suffix, UserId: f.mainLeader.Id, Status: common.TopUpStatusPending,
				PaymentProvider: method.provider, PaymentMethod: method.method}
			ext := &model.AdtyTopupOrder{TradeNo: order.TradeNo, TeamId: team.Team.Id, UserId: f.mainLeader.Id, Provider: method.provider, Method: method.method}
			results <- model.CreateAdtyTopUpWithPolicy(order, ext, nil, func(tx *gorm.DB) error {
				return model.ValidateAdtyTeamTopupCreateTx(tx, ext)
			})
		})
	}
	close(start)
	group.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else {
			assert.ErrorIs(t, err, model.ErrAdtyIdempotencyConflict)
		}
	}
	assert.Equal(t, 1, succeeded)
	var count int64
	require.NoError(t, f.db.Model(&model.AdtyTopupOrder{}).Where("team_id = ?", team.Team.Id).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestTeamRepairEmployeeInviteFailuresKeepCodeAndIdentityUnchanged(t *testing.T) {
	f := newTeamAuthFixture(t)
	team, err := service.CreateAdtyTeam(f.leaderA.Id, f.siteA.Id, "Employee invite boundaries", decimal.NewFromInt(1))
	require.NoError(t, err)
	for _, state := range []string{"expired", "revoked", "existing-identity"} {
		t.Run(state, func(t *testing.T) {
			invite, err := service.CreateAdtyTeamInvite(f.leaderA.Id, team.Team.Id, time.Hour)
			require.NoError(t, err)
			username := "invite-boundary"
			switch state {
			case "expired":
				require.NoError(t, f.db.Model(&model.AdtyTeamInvite{}).Where("id = ?", invite.Id).Update("expires_at", time.Now().Add(-time.Hour).Unix()).Error)
			case "revoked":
				_, err = service.RevokeAdtyTeamInvite(f.leaderA.Id, team.Team.Id, invite.Id)
				require.NoError(t, err)
			case "existing-identity":
				username = f.userA.Username
			}
			var before int64
			require.NoError(t, f.db.Model(&model.User{}).Count(&before).Error)
			_, err = service.RegisterAdtyTeamEmployee(invite.Code, username, "Employee-password-2026", "", f.siteA.Id)
			require.Error(t, err)
			var after int64
			require.NoError(t, f.db.Model(&model.User{}).Count(&after).Error)
			assert.Equal(t, before, after)
			var stored model.AdtyTeamInvite
			require.NoError(t, f.db.First(&stored, invite.Id).Error)
			assert.Zero(t, stored.UsedBy)
			assert.Zero(t, stored.UsedAt)
		})
	}
}

func TestTeamRepairLeaderCannotUndoAnIndependentAdminRevocation(t *testing.T) {
	f := newTeamAuthFixture(t)
	team, err := service.CreateAdtyTeam(f.leaderA.Id, f.siteA.Id, "Revocation boundary", decimal.NewFromInt(1))
	require.NoError(t, err)
	employee, err := service.CreateAdtyTeamEmployee(f.leaderA.Id, team.Team.Id, "revoked-employee", "Employee-password-2026", "")
	require.NoError(t, err)
	require.NoError(t, service.DisableAdtyTeamMember(f.leaderA.Id, team.Team.Id, employee.UserId))
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := model.IncrementUserAuthVersionWithTx(tx, employee.UserId)
		return err
	}))
	require.ErrorIs(t, service.EnableAdtyTeamMember(f.leaderA.Id, team.Team.Id, employee.UserId), service.ErrAdtyTeamForbidden)
	var user model.User
	require.NoError(t, f.db.First(&user, employee.UserId).Error)
	assert.Equal(t, common.UserStatusDisabled, user.Status)
	assert.Equal(t, int64(3), user.AuthVersion)
	var member model.AdtyTeamMember
	require.NoError(t, f.db.Where("team_id = ? AND user_id = ?", team.Team.Id, user.Id).Take(&member).Error)
	assert.Equal(t, model.AdtyTeamMemberDisabled, member.Status)
}

func TestTeamRepairEmployeeLeaveCannotReuseIdentityOnSecondTeam(t *testing.T) {
	f := newTeamAuthFixture(t)
	team, err := service.CreateAdtyTeam(f.leaderA.Id, f.siteA.Id, "Original employee team", decimal.NewFromInt(1))
	require.NoError(t, err)
	other, err := service.CreateAdtyTeam(f.userA.Id, f.siteA.Id, "Other team", decimal.NewFromInt(1))
	require.NoError(t, err)
	employee, err := service.CreateAdtyTeamEmployee(f.leaderA.Id, team.Team.Id, "leaving-employee", "Employee-password-2026", "")
	require.NoError(t, err)
	var user model.User
	require.NoError(t, f.db.First(&user, employee.UserId).Error)
	oldSession := f.session(t, &user)
	require.NoError(t, service.LeaveAdtyTeam(user.Id, team.Team.Id))
	teamAuthRejected(t, f.call(t, http.MethodGet, f.siteAHost(), "/api/adty/teams/mine", "", oldSession))
	_, err = service.JoinAdtyTeam(user.Id, other.Team.Id)
	require.Error(t, err)
	_, err = service.CreateAdtyStaff(f.ownerA.Id, f.siteA.Id, service.AdtyStaffInput{
		Username: user.Username, DisplayName: "Cannot reuse", Password: "Employee-password-2026", Role: "support",
	})
	require.Error(t, err)
	require.NoError(t, service.EnableAdtyTeamMember(f.leaderA.Id, team.Team.Id, user.Id))
	_, err = service.JoinAdtyTeam(user.Id, other.Team.Id)
	require.Error(t, err)
	require.NoError(t, service.CloseAdtyTeam(f.leaderA.Id, team.Team.Id))
	require.NoError(t, f.db.First(&user, user.Id).Error)
	assert.Equal(t, common.UserStatusDisabled, user.Status)
	assert.Zero(t, user.StaffSiteId)
	var memberships int64
	require.NoError(t, f.db.Model(&model.AdtyTeamMember{}).Where("user_id = ?", user.Id).Count(&memberships).Error)
	assert.Equal(t, int64(1), memberships)
}

func TestTeamRepairEmployeeFieldsSurviveRepeatedMigration(t *testing.T) {
	f := newTeamAuthFixture(t)
	team, err := service.CreateAdtyTeam(f.mainLeader.Id, 0, "Migration team", decimal.NewFromInt(1))
	require.NoError(t, err)
	employee, err := service.CreateAdtyTeamEmployee(f.mainLeader.Id, team.Team.Id, "migration-employee", "Employee-password-2026", "")
	require.NoError(t, err)
	require.NoError(t, service.DisableAdtyTeamMember(f.mainLeader.Id, team.Team.Id, employee.UserId))
	for range 2 {
		require.NoError(t, model.MigrateAdtyTables())
	}
	var member model.AdtyTeamMember
	require.NoError(t, f.db.Where("user_id = ?", employee.UserId).Take(&member).Error)
	assert.True(t, member.IndependentEmployee)
	assert.Equal(t, model.AdtyTeamMemberDisabled, member.Status)
	assert.Equal(t, int64(2), member.DisabledAuthVersion)
	assert.Zero(t, member.DeletedAt)
	require.NoError(t, service.EnableAdtyTeamMember(f.mainLeader.Id, team.Team.Id, employee.UserId))
}

func TestTeamRepairSameTopupKeyIsIndependentAcrossTeamsAndLegacyOrders(t *testing.T) {
	f := newTeamAuthFixture(t)
	first, err := service.CreateAdtyTeam(f.mainLeader.Id, 0, "First checkout", decimal.NewFromInt(1))
	require.NoError(t, err)
	second, err := service.CreateAdtyTeam(f.mainUser.Id, 0, "Second checkout", decimal.NewFromInt(1))
	require.NoError(t, err)
	const key = "shared-key-across-teams"
	legacy := &model.AdtyTopupOrder{TradeNo: "TTEP" + common.Sha1([]byte(key))[:20], TeamId: first.Team.Id, UserId: f.mainLeader.Id,
		Provider: model.PaymentProviderAdtyEpay, Method: "alipay", FaceUSD: decimal.NewFromInt(100)}
	require.NoError(t, f.db.Create(legacy).Error)
	created, err := service.CreateAdtyTeamTopupOrder(f.mainUser.Id, second.Team.Id, decimal.NewFromInt(100), key)
	require.NoError(t, err)
	assert.Equal(t, second.Team.Id, created.TeamId)
	assert.NotEqual(t, legacy.TradeNo, created.TradeNo)
	replayed, err := service.CreateAdtyTeamTopupOrder(f.mainUser.Id, second.Team.Id, decimal.NewFromInt(100), key)
	require.NoError(t, err)
	assert.Equal(t, created.TradeNo, replayed.TradeNo)
	_, err = service.CreateAdtyTeamTopupOrder(f.mainLeader.Id, first.Team.Id, decimal.NewFromInt(100), key)
	require.Error(t, err)
	var count int64
	require.NoError(t, f.db.Model(&model.AdtyTopupOrder{}).Count(&count).Error)
	assert.Equal(t, int64(2), count)
}
