package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAdtyTeamSharedBalanceAndSingleMembership(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	leader := referralUser(t, db, "team-leader")
	member := referralUser(t, db, "team-member")
	other := referralUser(t, db, "team-other")
	site := model.AdtySite{OwnerUserId: leader.Id, Subdomain: "team-site", Status: model.AdtySiteStatusActive, CostFactor: decimalPtr("0.80")}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(leader).Update("site_id", site.Id).Error)
	require.NoError(t, db.Model(member).Update("site_id", site.Id).Error)

	created, err := CreateAdtyTeam(leader.Id, site.Id, "Ops", decimal.RequireFromString("0.90"))
	require.NoError(t, err)
	_, err = CreateAdtyTeam(leader.Id, site.Id, "Second", decimal.NewFromInt(1))
	require.ErrorIs(t, err, ErrAdtyTeamBusy)
	_, err = JoinAdtyTeam(other.Id, created.Team.Id)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	joined, err := CreateAdtyTeamEmployee(leader.Id, created.Team.Id, "ops-emp", "StrongPassword123!", "Ops Emp")
	require.NoError(t, err)
	assert.Equal(t, model.AdtyTeamRoleMember, joined.Role)
	member.Id = joined.UserId
	_, err = JoinAdtyTeam(member.Id, created.Team.Id)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = CreateAdtyTeamEmployee(leader.Id, created.Team.Id, "ops-emp", "StrongPassword123!", "Ops Emp")
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)

	require.NoError(t, CreditAdtyTeam(int64(leader.Id), created.Team.Id, 1_000_000, "credit-1"))
	require.ErrorIs(t, CreditAdtyTeam(int64(member.Id), created.Team.Id, 1, "credit-member"), ErrAdtyTeamForbidden)
	require.NoError(t, SpendAdtyTeam(member.Id, 400_000, "call-1"))
	require.ErrorIs(t, SpendAdtyTeam(member.Id, 700_000, "call-2"), ErrAdtyTeamFunds)
	var team model.AdtyTeam
	require.NoError(t, db.First(&team, created.Team.Id).Error)
	assert.EqualValues(t, 600_000, team.SharedQuota)
	var personal model.User
	require.NoError(t, db.First(&personal, member.Id).Error)
	assert.Zero(t, personal.Quota)

	require.NoError(t, CloseAdtyTeam(leader.Id, created.Team.Id))
	require.NoError(t, db.First(&team, created.Team.Id).Error)
	assert.Equal(t, model.AdtyTeamStatusClosed, team.Status)
	assert.EqualValues(t, 600_000, team.SharedQuota)
	require.ErrorIs(t, SpendAdtyTeam(member.Id, 1, "after-close"), ErrAdtyTeamClosed)
}

func TestAdtyTeamInviteKeyAndCloseKeepSharedBalance(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	require.NoError(t, db.AutoMigrate(&model.Token{}))
	leader := referralUser(t, db, "team-invite-leader")
	member := referralUser(t, db, "team-invite-member")
	require.NoError(t, db.Model(leader).Update("site_id", 0).Error)
	require.NoError(t, db.Model(member).Update("site_id", 0).Error)
	created, err := CreateAdtyTeam(leader.Id, 0, "Main", decimal.NewFromInt(1))
	require.NoError(t, err)
	issued, err := CreateAdtyTeamInvite(leader.Id, created.Team.Id, time.Hour)
	require.NoError(t, err)
	_, err = AcceptAdtyTeamInvite(member.Id, issued.Code)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	joined, err := RegisterAdtyTeamEmployee(issued.Code, "invite-employee", "StrongPassword123!", "Invite Employee", 0)
	require.NoError(t, err)
	assert.True(t, joined.IndependentEmployee)
	member.Id = joined.UserId
	_, err = AcceptAdtyTeamInvite(member.Id, issued.Code)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	token := model.Token{UserId: member.Id, Key: "team-key-secret", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, BindAdtyTeamKey(member.Id, token.Id))
	require.NoError(t, CreditAdtyTeam(int64(leader.Id), created.Team.Id, 500000, "team-credit"))
	handled, err := reserveAdtyTeamQuotaHook(db, created.Team.Id, member.Id, 200000, "request:team-1")
	require.NoError(t, err)
	assert.True(t, handled)
	var team model.AdtyTeam
	require.NoError(t, db.First(&team, created.Team.Id).Error)
	assert.EqualValues(t, 300000, team.SharedQuota)
	var personal model.User
	require.NoError(t, db.First(&personal, member.Id).Error)
	assert.Zero(t, personal.Quota)

	require.NoError(t, closeAdtyTeamForDeletedLeader(db, leader.Id))
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)
	require.NoError(t, db.First(&team, created.Team.Id).Error)
	assert.Equal(t, model.AdtyTeamStatusClosed, team.Status)
	assert.EqualValues(t, 300000, team.SharedQuota)
}

func TestAdtyTeamMineUsesTheSignedInSite(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	leader := referralUser(t, db, "team-mine-leader")
	_, err := CreateAdtyTeamForCurrentUser(leader.Id, "Desk", decimal.RequireFromString("0.80"))
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	var leftover int64
	require.NoError(t, db.Model(&model.AdtyTeam{}).Count(&leftover).Error)
	assert.Zero(t, leftover)
	created, err := CreateAdtyTeam(leader.Id, 0, "Desk", decimal.RequireFromString("0.80"))
	require.NoError(t, err)
	assert.Equal(t, 0, created.Team.SiteId)
	mine, err := GetAdtyTeamMine(leader.Id)
	require.NoError(t, err)
	require.NotNil(t, mine)
	assert.Equal(t, model.AdtyTeamRoleLeader, mine.Role)
	assert.Equal(t, "Desk", mine.Name)
	empty, err := GetAdtyTeamMine(0)
	require.NoError(t, err)
	assert.Nil(t, empty)
}

func TestAdtyTeamProtocolPriceUsesBoundKey(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	require.NoError(t, db.AutoMigrate(&model.Token{}))
	leader := referralUser(t, db, "team-price-leader")
	member := referralUser(t, db, "team-price-member")
	created, err := CreateAdtyTeam(leader.Id, 0, "Price", decimal.RequireFromString("0.50"))
	require.NoError(t, err)
	joined, err := CreateAdtyTeamEmployee(leader.Id, created.Team.Id, "price-emp", "StrongPassword123!", "Price Emp")
	require.NoError(t, err)
	member.Id = joined.UserId
	token := model.Token{UserId: member.Id, Key: "team-price-key", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, BindAdtyTeamKey(member.Id, token.Id))

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	common.SetContextKey(ctx, constant.ContextKeyTokenId, token.Id)
	marked, k := AdtyApplySiteMarkup(ctx, member.Id, "default", 2, "")
	assert.Equal(t, 1.0, k)
	assert.InDelta(t, 1.0, marked, 0.000001)
	assert.False(t, AdtyTeamPriceBlocked(ctx))

	personal, _ := gin.CreateTestContext(httptest.NewRecorder())
	personal.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	unchanged, _ := AdtyApplySiteMarkup(personal, member.Id, "default", 2, "")
	assert.InDelta(t, 2.0, unchanged, 0.000001)
}

func decimalPtr(value string) *decimal.Decimal {
	parsed := decimal.RequireFromString(value)
	return &parsed
}

func TestCreateAdtyTeamFollowsG2PayRatioFormula(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	owner := referralUser(t, db, "g2-create-owner")
	// d=0.80. Discounted price >= supply iff stored k × payment ratio >= 1.
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "g2-create", Status: model.AdtySiteStatusActive, CostFactor: decimalPtr("0.80")}
	require.NoError(t, db.Create(&site).Error)

	// k=1.5: ratio 0.70 is below CostFactor, but 1.5×0.70=1.05 so discounted >= supply.
	leaderAbove := referralUser(t, db, "g2-create-above")
	require.NoError(t, db.Model(leaderAbove).Update("site_id", site.Id).Error)
	above, err := CreateAdtyTeam(leaderAbove.Id, site.Id, "Above", decimal.RequireFromString("0.70"))
	require.NoError(t, err)
	require.True(t, above.Team.Discount.Equal(decimal.RequireFromString("0.70")))

	// Markup ratio 0 (stored k=1): any ratio < 1 makes discounted < supply.
	// Billing already charges max(supply, discounted), so config must accept it.
	leaderBelow := referralUser(t, db, "g2-create-below")
	require.NoError(t, db.Model(leaderBelow).Update("site_id", site.Id).Error)
	below, err := CreateAdtyTeam(leaderBelow.Id, site.Id, "Below", decimal.RequireFromString("0.50"))
	require.NoError(t, err)
	require.True(t, below.Team.Discount.Equal(decimal.RequireFromString("0.50")))

	leaderBad := referralUser(t, db, "g2-create-bad")
	require.NoError(t, db.Model(leaderBad).Update("site_id", site.Id).Error)
	_, err = CreateAdtyTeam(leaderBad.Id, site.Id, "Bad", decimal.Zero)
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)
	_, err = CreateAdtyTeam(leaderBad.Id, site.Id, "Bad", decimal.RequireFromString("1.01"))
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)
}

func TestAdtyTeamCreateRequiresRootOrAuthorizedOwner(t *testing.T) {
	db := prepareReferralDB(t)
	root := referralUser(t, db, "auth-root")
	require.NoError(t, db.Model(root).Update("role", common.RoleRootUser).Error)
	root.Role = common.RoleRootUser
	mainLeader := referralUser(t, db, "auth-main-leader")
	mainUser := referralUser(t, db, "auth-main-user")
	admin := referralUser(t, db, "auth-admin")
	require.NoError(t, db.Model(admin).Update("role", common.RoleAdminUser).Error)

	ownerA := referralUser(t, db, "auth-owner-a")
	ownerB := referralUser(t, db, "auth-owner-b")
	siteA := model.AdtySite{OwnerUserId: ownerA.Id, Subdomain: "auth-a", Status: model.AdtySiteStatusActive, CostFactor: decimalPtr("0.80")}
	siteB := model.AdtySite{OwnerUserId: ownerB.Id, Subdomain: "auth-b", Status: model.AdtySiteStatusActive, CostFactor: decimalPtr("0.80")}
	require.NoError(t, db.Create(&siteA).Error)
	require.NoError(t, db.Create(&siteB).Error)
	// Owners stay on the main site (users.site_id = 0). Using that field as the
	// team site would wrongly place an owner-created team on the main site.
	require.Equal(t, 0, ownerA.SiteId)
	leaderA := referralUser(t, db, "auth-leader-a")
	memberA := referralUser(t, db, "auth-member-a")
	leaderB := referralUser(t, db, "auth-leader-b")
	staffA := referralUser(t, db, "auth-staff-a")
	require.NoError(t, db.Model(leaderA).Update("site_id", siteA.Id).Error)
	require.NoError(t, db.Model(memberA).Update("site_id", siteA.Id).Error)
	require.NoError(t, db.Model(leaderB).Update("site_id", siteB.Id).Error)
	require.NoError(t, db.Model(staffA).Updates(map[string]any{"site_id": siteA.Id, "staff_site_id": siteA.Id}).Error)
	leaderA.SiteId, memberA.SiteId, leaderB.SiteId = siteA.Id, siteA.Id, siteB.Id
	staffA.SiteId, staffA.StaffSiteId = siteA.Id, siteA.Id

	teamCount := func() int64 {
		t.Helper()
		var n int64
		require.NoError(t, db.Model(&model.AdtyTeam{}).Count(&n).Error)
		return n
	}
	start := teamCount()

	_, err := CreateAdtyTeamForCurrentUser(mainUser.Id, "Self", decimal.RequireFromString("0.50"))
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = CreateAdtyTeamForCurrentUser(leaderA.Id, "Self", decimal.RequireFromString("0.90"))
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = CreateAdtyMainTeamByRoot(mainUser.Id, mainLeader.Id, "Denied", "0.80")
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = CreateAdtyMainTeamByRoot(admin.Id, mainLeader.Id, "Denied", "0.80")
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = CreateAdtyOwnerSiteTeam(mainUser.Id, siteA.Id, leaderA.Id, "Denied", "0.90")
	require.Error(t, err)
	assert.Equal(t, start, teamCount())

	created, err := CreateAdtyMainTeamByRoot(root.Id, mainLeader.Id, "Main Desk", "0.80")
	require.NoError(t, err)
	assert.Equal(t, 0, created.Team.SiteId)
	assert.Equal(t, mainLeader.Id, created.Team.LeaderUserId)
	assert.NotEqual(t, root.Id, created.Team.LeaderUserId)
	assert.True(t, created.Team.Discount.Equal(decimal.RequireFromString("0.80")))

	defaulted, err := CreateAdtyMainTeamByRoot(root.Id, mainUser.Id, "Default Price", "")
	require.NoError(t, err)
	assert.Equal(t, 0, defaulted.Team.SiteId)
	assert.Equal(t, mainUser.Id, defaulted.Team.LeaderUserId)
	assert.True(t, defaulted.Team.Discount.Equal(decimal.NewFromInt(1)))

	_, err = CreateAdtyOwnerSiteTeam(ownerA.Id, siteA.Id, leaderA.Id, "Site Desk", "0.90")
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = CreateAdtySiteTeamByRoot(ownerA.Id, siteA.Id, leaderA.Id, "Site Desk", "0.90")
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = CreateAdtySiteTeamByRoot(admin.Id, siteA.Id, leaderA.Id, "Site Desk", "0.90")
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)

	owned, err := CreateAdtySiteTeamByRoot(root.Id, siteA.Id, leaderA.Id, "Site Desk", "0.90")
	require.NoError(t, err)
	assert.Equal(t, siteA.Id, owned.Team.SiteId)
	assert.NotEqual(t, 0, owned.Team.SiteId)
	assert.Equal(t, leaderA.Id, owned.Team.LeaderUserId)
	assert.NotEqual(t, ownerA.Id, owned.Team.LeaderUserId)
	assert.True(t, owned.Team.Discount.Equal(decimal.RequireFromString("0.90")))

	_, err = CreateAdtyOwnerSiteTeam(ownerA.Id, 0, mainLeader.Id, "Main From Owner", "1")
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = CreateAdtyOwnerSiteTeam(ownerA.Id, siteB.Id, leaderB.Id, "Other Site", "0.90")
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = CreateAdtyOwnerSiteTeam(ownerB.Id, siteA.Id, leaderA.Id, "Cross Owner", "0.90")
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = CreateAdtyOwnerSiteTeam(staffA.Id, siteA.Id, memberA.Id, "Staff Create", "0.90")
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)

	missingID := 9_000_001
	_, err = CreateAdtySiteTeamByRoot(root.Id, siteA.Id, missingID, "Missing", "0.90")
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = CreateAdtySiteTeamByRoot(root.Id, siteA.Id, leaderB.Id, "Wrong Site Leader", "0.90")
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = CreateAdtySiteTeamByRoot(root.Id, siteA.Id, staffA.Id, "Staff Leader", "0.90")
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = CreateAdtySiteTeamByRoot(root.Id, siteA.Id, leaderA.Id, "Already Leader", "0.90")
	require.ErrorIs(t, err, ErrAdtyTeamBusy)

	busyMember, err := CreateAdtyTeamEmployee(leaderA.Id, owned.Team.Id, "site-emp", "StrongPassword123!", "Site Emp")
	require.NoError(t, err)
	assert.Equal(t, model.AdtyTeamRoleMember, busyMember.Role)
	_, err = CreateAdtySiteTeamByRoot(root.Id, siteA.Id, busyMember.UserId, "Already Member", "0.90")
	require.ErrorIs(t, err, ErrAdtyTeamBusy)

	require.NoError(t, SaveAdtyMainTeamAgreementPrice(created.Team.Id, "0.70"))
	require.Error(t, SaveAdtyMainTeamAgreementPrice(owned.Team.Id, "0.70"))
	require.NoError(t, SaveAdtyOwnerSiteTeamDiscount(siteA.Id, ownerA.Id, owned.Team.Id, "0.85"))
	require.Error(t, SaveAdtyOwnerSiteTeamDiscount(siteA.Id, ownerB.Id, owned.Team.Id, "0.95"))
	require.Error(t, SaveAdtyOwnerSiteTeamDiscount(siteA.Id, mainUser.Id, owned.Team.Id, "0.95"))
	require.Error(t, SaveAdtyOwnerSiteTeamDiscount(siteA.Id, leaderA.Id, owned.Team.Id, "0.95"))
}

func TestAdtyTeamInviteRevokeTTLAndAuthorization(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	leader := referralUser(t, db, "invite-rev-leader")
	member := referralUser(t, db, "invite-rev-member")
	otherLeader := referralUser(t, db, "invite-rev-other-leader")
	staff := referralUser(t, db, "invite-rev-staff")
	require.NoError(t, db.Model(staff).Updates(map[string]any{"staff_site_id": 1}).Error)
	require.NoError(t, db.Model(leader).Update("site_id", 0).Error)
	require.NoError(t, db.Model(member).Update("site_id", 0).Error)
	require.NoError(t, db.Model(otherLeader).Update("site_id", 0).Error)
	created, err := CreateAdtyTeam(leader.Id, 0, "Invite Desk", decimal.NewFromInt(1))
	require.NoError(t, err)
	otherTeam, err := CreateAdtyTeam(otherLeader.Id, 0, "Other", decimal.NewFromInt(1))
	require.NoError(t, err)

	now := time.Now().Unix()
	defaultIssued, err := CreateAdtyTeamInvite(leader.Id, created.Team.Id, AdtyTeamInviteDefaultTTL)
	require.NoError(t, err)
	require.NotEmpty(t, defaultIssued.Code)
	assert.Equal(t, leader.Id, defaultIssued.CreatedBy)
	assert.Equal(t, model.AdtyTeamInviteStatusOutstanding, defaultIssued.Status)
	assert.InDelta(t, now+int64(AdtyTeamInviteDefaultTTL.Seconds()), defaultIssued.ExpiresAt, 3)

	customIssued, err := CreateAdtyTeamInvite(leader.Id, created.Team.Id, 2*time.Hour)
	require.NoError(t, err)
	assert.InDelta(t, now+int64((2*time.Hour).Seconds()), customIssued.ExpiresAt, 3)
	assert.NotEqual(t, defaultIssued.ExpiresAt, customIssued.ExpiresAt)

	_, err = CreateAdtyTeamInvite(leader.Id, created.Team.Id, 0)
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)
	_, err = CreateAdtyTeamInvite(leader.Id, created.Team.Id, -time.Hour)
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)
	_, err = CreateAdtyTeamInvite(leader.Id, created.Team.Id, 7*24*time.Hour+time.Second)
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)
	_, err = CreateAdtyTeamInvite(member.Id, created.Team.Id, time.Hour)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = CreateAdtyTeamInvite(otherLeader.Id, created.Team.Id, time.Hour)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = CreateAdtyTeamInvite(staff.Id, created.Team.Id, time.Hour)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)

	expired, err := CreateAdtyTeamInvite(leader.Id, created.Team.Id, time.Second)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.AdtyTeamInvite{}).Where("id = ?", expired.Id).Update("expires_at", time.Now().Add(-time.Second).Unix()).Error)
	_, err = AcceptAdtyTeamInvite(member.Id, expired.Code)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = RegisterAdtyTeamEmployee(expired.Code, "expired-employee", "StrongPassword123!", "Expired", 0)
	require.Error(t, err)

	toRevoke, err := CreateAdtyTeamInvite(leader.Id, created.Team.Id, time.Hour)
	require.NoError(t, err)
	_, err = RevokeAdtyTeamInvite(member.Id, created.Team.Id, toRevoke.Id)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = RevokeAdtyTeamInvite(otherLeader.Id, created.Team.Id, toRevoke.Id)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = RevokeAdtyTeamInvite(staff.Id, created.Team.Id, toRevoke.Id)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = RevokeAdtyTeamInvite(otherLeader.Id, otherTeam.Team.Id, toRevoke.Id)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)

	revoked, err := RevokeAdtyTeamInvite(leader.Id, created.Team.Id, toRevoke.Id)
	require.NoError(t, err)
	assert.Equal(t, model.AdtyTeamInviteStatusRevoked, revoked.Status)
	assert.Equal(t, leader.Id, revoked.CreatedBy)
	assert.Equal(t, leader.Id, revoked.RevokedBy)
	assert.NotZero(t, revoked.RevokedAt)
	_, err = AcceptAdtyTeamInvite(member.Id, toRevoke.Code)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = RegisterAdtyTeamEmployee(toRevoke.Code, "revoked-employee", "StrongPassword123!", "Revoked", 0)
	require.Error(t, err)
	var stillOpen int64
	require.NoError(t, db.Model(&model.AdtyTeamMember{}).Where("user_id = ? AND status = ?", member.Id, model.AdtyTeamMemberActive).Count(&stillOpen).Error)
	assert.Zero(t, stillOpen)

	used, err := CreateAdtyTeamInvite(leader.Id, created.Team.Id, time.Hour)
	require.NoError(t, err)
	_, err = AcceptAdtyTeamInvite(member.Id, used.Code)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	registered, err := RegisterAdtyTeamEmployee(used.Code, "used-employee", "StrongPassword123!", "Used", 0)
	require.NoError(t, err)
	_, err = RevokeAdtyTeamInvite(leader.Id, created.Team.Id, used.Id)
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)
	var membership model.AdtyTeamMember
	require.NoError(t, db.Where("user_id = ? AND team_id = ?", registered.UserId, created.Team.Id).First(&membership).Error)
	assert.Equal(t, model.AdtyTeamMemberActive, membership.Status)
	_, err = AcceptAdtyTeamInvite(registered.UserId, used.Code)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)

	listed, err := ListAdtyTeamInvites(leader.Id, created.Team.Id)
	require.NoError(t, err)
	require.NotEmpty(t, listed)
	_, err = ListAdtyTeamInvites(member.Id, created.Team.Id)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = ListAdtyTeamInvites(staff.Id, created.Team.Id)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	statuses := map[int64]string{}
	for _, row := range listed {
		statuses[row.Id] = row.Status
		assert.Equal(t, created.Team.Id, row.TeamId)
		assert.Equal(t, leader.Id, row.CreatedBy)
	}
	assert.Equal(t, model.AdtyTeamInviteStatusRevoked, statuses[toRevoke.Id])
	assert.Equal(t, model.AdtyTeamInviteStatusUsed, statuses[used.Id])
	assert.Equal(t, model.AdtyTeamInviteStatusExpired, statuses[expired.Id])
	assert.Equal(t, model.AdtyTeamInviteStatusOutstanding, statuses[customIssued.Id])

	requireTeamInviteAudit(t, defaultIssued.Id, "adty.team.invite.create", leader.Id, defaultIssued.Code)
	requireTeamInviteAudit(t, toRevoke.Id, "adty.team.invite.revoke", leader.Id, toRevoke.Code)
	var acceptLog model.AuditLog
	require.NoError(t, model.DB.Where("object_type = ? AND object_id = ? AND action = ?", "team_invite", strconv.FormatInt(used.Id, 10), "adty.team.invite.accept").First(&acceptLog).Error)
	assert.Equal(t, model.AuditResultSuccess, acceptLog.Result)
	assert.True(t, acceptLog.Success)
	assert.Equal(t, registered.UserId, acceptLog.UserId, "only the new employee consumes the invite")
	assert.NotEqual(t, member.Id, acceptLog.UserId)
	assert.Contains(t, acceptLog.AfterValue, `"used_by":`+strconv.Itoa(registered.UserId))
	assert.Contains(t, acceptLog.AfterValue, `"independent_employee":true`)
	assert.NotContains(t, acceptLog.AfterValue, used.Code)
	assert.NotContains(t, acceptLog.BeforeValue, used.Code)
	assert.NotContains(t, acceptLog.Content, used.Code)
}

func requireTeamInviteAudit(t *testing.T, inviteId int64, action string, actorId int, secret string) {
	t.Helper()
	var log model.AuditLog
	require.NoError(t, model.DB.Where("object_type = ? AND object_id = ? AND action = ?", "team_invite", strconv.FormatInt(inviteId, 10), action).First(&log).Error)
	assert.Equal(t, model.AuditResultSuccess, log.Result)
	assert.True(t, log.Success)
	assert.Equal(t, actorId, log.UserId)
	assert.Contains(t, log.AfterValue, "created_by")
	assert.Contains(t, log.AfterValue, `"result":"success"`)
	assert.NotContains(t, log.AfterValue, "code_hash")
	assert.NotContains(t, log.BeforeValue, secret)
	assert.NotContains(t, log.AfterValue, secret)
	assert.NotContains(t, log.Content, secret)
}

func TestInheritAdtyTeamsOnCloseRecordsSourceSiteAndKeepsIdentity(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "team-src")
	_, other := openActiveSite(t, db, "team-other")
	leader := siteCustomer(t, db, site, "team-src-leader")
	member := siteCustomer(t, db, site, "team-src-member")
	created, err := CreateAdtyTeam(leader.Id, site.Id, "Migrating", decimal.RequireFromString("0.90"))
	require.NoError(t, err)
	teamID := created.Team.Id
	joined, err := CreateAdtyTeamEmployee(leader.Id, teamID, "src-emp", "StrongPassword123!", "Src Emp")
	require.NoError(t, err)
	assert.Equal(t, model.AdtyTeamRoleMember, joined.Role)
	member.Id = joined.UserId

	token := model.Token{UserId: member.Id, Key: "team-src-key", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, BindAdtyTeamKey(member.Id, token.Id))

	historical := model.AdtyTeam{
		SiteId: 0, LeaderUserId: leader.Id, Name: "legacy-main",
		Discount: decimal.NewFromInt(1), Status: model.AdtyTeamStatusActive,
	}
	require.NoError(t, db.Create(&historical).Error)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return InheritAdtyTeamsOnCloseTx(tx, site.Id)
	}))

	var inherited model.AdtyTeam
	require.NoError(t, db.First(&inherited, teamID).Error)
	assert.Equal(t, teamID, inherited.Id)
	assert.Zero(t, inherited.SiteId)
	assert.Equal(t, site.Id, inherited.SourceSiteId)
	assert.Equal(t, model.AdtyTeamStatusActive, inherited.Status)
	assert.Zero(t, inherited.ClosedAt)
	assert.Equal(t, leader.Id, inherited.LeaderUserId)

	var members []model.AdtyTeamMember
	require.NoError(t, db.Where("team_id = ?", teamID).Order("id").Find(&members).Error)
	require.Len(t, members, 2)
	assert.Equal(t, leader.Id, members[0].UserId)
	assert.Equal(t, model.AdtyTeamRoleLeader, members[0].Role)
	assert.Equal(t, model.AdtyTeamMemberActive, members[0].Status)
	assert.Equal(t, member.Id, members[1].UserId)
	assert.Equal(t, model.AdtyTeamRoleMember, members[1].Role)
	assert.Equal(t, model.AdtyTeamMemberActive, members[1].Status)

	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)
	assert.False(t, token.DeletedAt.Valid)
	var key model.AdtyTeamKey
	require.NoError(t, db.Where("token_id = ?", token.Id).First(&key).Error)
	assert.Equal(t, model.AdtyTeamMemberLeft, key.Status)

	var leftover model.AdtyTeam
	require.NoError(t, db.First(&leftover, historical.Id).Error)
	assert.Zero(t, leftover.SiteId)
	assert.Zero(t, leftover.SourceSiteId)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return InheritAdtyTeamsOnCloseTx(tx, site.Id)
	}))
	require.NoError(t, db.First(&inherited, teamID).Error)
	assert.Zero(t, inherited.SiteId)
	assert.Equal(t, site.Id, inherited.SourceSiteId)
	assert.Equal(t, model.AdtyTeamStatusActive, inherited.Status)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)

	require.NoError(t, db.Model(&inherited).Update("site_id", site.Id).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return InheritAdtyTeamsOnCloseTx(tx, site.Id)
	}))
	require.NoError(t, db.First(&inherited, teamID).Error)
	assert.Zero(t, inherited.SiteId)
	assert.Equal(t, site.Id, inherited.SourceSiteId)

	require.NoError(t, db.Model(&inherited).Updates(map[string]any{"site_id": site.Id, "source_site_id": other.Id}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return InheritAdtyTeamsOnCloseTx(tx, site.Id)
	}))
	require.NoError(t, db.First(&inherited, teamID).Error)
	assert.Zero(t, inherited.SiteId)
	assert.Equal(t, other.Id, inherited.SourceSiteId)

	require.NoError(t, db.Model(&inherited).Updates(map[string]any{"site_id": 0, "source_site_id": site.Id}).Error)

	_, err = ScheduleAdtySiteClose(site.Id, 1, AdtySiteClosePlan{TransferTo: other.Id, Immediate: true})
	assert.ErrorIs(t, err, ErrAdtySiteTransfer)

	closed, err := ScheduleAdtySiteClose(site.Id, 1, AdtySiteClosePlan{Immediate: true})
	require.NoError(t, err)
	assert.Equal(t, model.AdtySiteStatusClosed, closed.Status)
	assert.Zero(t, closed.CloseTransferTo)

	require.NoError(t, db.First(&inherited, teamID).Error)
	assert.Equal(t, teamID, inherited.Id)
	assert.Zero(t, inherited.SiteId)
	assert.Equal(t, site.Id, inherited.SourceSiteId)
	assert.Equal(t, model.AdtyTeamStatusActive, inherited.Status)
	assert.Equal(t, leader.Id, inherited.LeaderUserId)

	require.NoError(t, db.Where("team_id = ?", teamID).Order("id").Find(&members).Error)
	require.Len(t, members, 2)
	assert.Equal(t, leader.Id, members[0].UserId)
	assert.Equal(t, member.Id, members[1].UserId)
	assert.Equal(t, model.AdtyTeamMemberActive, members[0].Status)
	assert.Equal(t, model.AdtyTeamMemberActive, members[1].Status)

	require.NoError(t, db.Unscoped().First(&token, token.Id).Error)
	assert.True(t, token.DeletedAt.Valid)
	require.NoError(t, db.Where("token_id = ?", token.Id).First(&key).Error)
	assert.Equal(t, model.AdtyTeamMemberLeft, key.Status)

	require.NoError(t, db.First(&leftover, historical.Id).Error)
	assert.Zero(t, leftover.SiteId)
	assert.Zero(t, leftover.SourceSiteId)

	var storedLeader, storedMember model.User
	require.NoError(t, db.First(&storedLeader, leader.Id).Error)
	require.NoError(t, db.First(&storedMember, member.Id).Error)
	assert.Zero(t, storedLeader.SiteId)
	assert.Zero(t, storedMember.SiteId)
	assert.Equal(t, leader.Id, storedLeader.Id)
	assert.Equal(t, member.Id, storedMember.Id)
	assert.NotEqual(t, other.Id, storedLeader.SiteId)
	assert.NotEqual(t, other.Id, storedMember.SiteId)

	mine, err := GetAdtyTeamMine(leader.Id)
	require.NoError(t, err)
	require.NotNil(t, mine)
	assert.Equal(t, teamID, mine.TeamId)
	assert.Zero(t, mine.SiteId)
	assert.Equal(t, site.Id, mine.SourceSiteId)
	assert.Equal(t, model.AdtyTeamStatusActive, mine.Status)

	require.NoError(t, db.Model(&inherited).Updates(map[string]any{"site_id": 0, "source_site_id": 0}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return InheritAdtyTeamsOnCloseTx(tx, site.Id)
	}))
	require.NoError(t, db.First(&inherited, teamID).Error)
	assert.Zero(t, inherited.SiteId)
	assert.Zero(t, inherited.SourceSiteId)
}

func TestBackfillAdtyTeamSourceSiteIDDoesNotChangeClosePath(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "team-bf-src")
	leader := siteCustomer(t, db, site, "team-bf-leader")

	// Historical main-site team with an independently recorded origin.
	known := model.AdtyTeam{
		SiteId: 0, LeaderUserId: leader.Id, Name: "known-historical",
		Discount: decimal.NewFromInt(1), Status: model.AdtyTeamStatusActive,
	}
	require.NoError(t, db.Create(&known).Error)
	require.NoError(t, db.Create(&model.AdtyTeamRegistrationLink{
		SiteId: site.Id, CodeHash: "hash-known-close", CreatedBy: leader.Id,
		UsedBy: leader.Id, UsedAt: time.Now().Unix(), TeamId: known.Id,
	}).Error)

	// Native main-site team owned by the same later-inherited leader, no recorded team origin.
	legacy := model.AdtyTeam{
		SiteId: 0, LeaderUserId: leader.Id, Name: "legacy-main",
		Discount: decimal.NewFromInt(1), Status: model.AdtyTeamStatusActive,
	}
	require.NoError(t, db.Create(&legacy).Error)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return InheritAdtyTeamsOnCloseTx(tx, site.Id)
	}))

	load := func(id int64) model.AdtyTeam {
		t.Helper()
		var row model.AdtyTeam
		require.NoError(t, db.Where("id = ?", id).Take(&row).Error)
		return row
	}

	stored := load(known.Id)
	assert.Zero(t, stored.SiteId)
	assert.Zero(t, stored.SourceSiteId, "close must not itself backfill an already-on-main empty source")
	stored = load(legacy.Id)
	assert.Zero(t, stored.SiteId)
	assert.Zero(t, stored.SourceSiteId)

	require.NoError(t, model.BackfillAdtyTeamSourceSiteID(db))

	stored = load(known.Id)
	assert.Zero(t, stored.SiteId)
	assert.Equal(t, site.Id, stored.SourceSiteId)
	stored = load(legacy.Id)
	assert.Zero(t, stored.SourceSiteId, "must not guess a source for a native main-site team")

	require.NoError(t, model.BackfillAdtyTeamSourceSiteID(db))
	stored = load(known.Id)
	assert.Equal(t, site.Id, stored.SourceSiteId)
	stored = load(legacy.Id)
	assert.Zero(t, stored.SourceSiteId)
}

func TestTeamKeyLifecycleRejectsRevokedRemovedAndClosedTeamKeys(t *testing.T) {
	for _, tc := range []struct {
		name   string
		revoke func(t *testing.T, leaderID, memberID int, teamID int64, tokenID int)
	}{
		{
			name: "revoked_key",
			revoke: func(t *testing.T, leaderID, _ int, teamID int64, tokenID int) {
				require.NoError(t, RevokeAdtyTeamKey(leaderID, teamID, tokenID))
			},
		},
		{
			name: "removed_member",
			revoke: func(t *testing.T, leaderID, memberID int, teamID int64, _ int) {
				require.NoError(t, DisableAdtyTeamMember(leaderID, teamID, memberID))
			},
		},
		{
			name: "closed_team",
			revoke: func(t *testing.T, leaderID, _ int, teamID int64, _ int) {
				require.NoError(t, CloseAdtyTeam(leaderID, teamID))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := prepareReferralDB(t)
			require.NoError(t, db.AutoMigrate(&model.Token{}, &model.BillingOperation{}, &model.BillingOperationMovement{}))
			leader := referralUser(t, db, "tk-life-leader-"+tc.name)
			member := referralUser(t, db, "tk-life-member-"+tc.name)
			require.NoError(t, db.Model(member).Update("quota", 500_000).Error)

			created, err := CreateAdtyTeam(leader.Id, 0, "Lifecycle", decimal.NewFromInt(1))
			require.NoError(t, err)
			empName := map[string]string{"revoked_key": "life-rk", "removed_member": "life-rm", "closed_team": "life-ct"}[tc.name]
			joined, err := CreateAdtyTeamEmployee(leader.Id, created.Team.Id, empName, "StrongPassword123!", "Life")
			require.NoError(t, err)
			member.Id = joined.UserId
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", member.Id).Update("quota", 500_000).Error)
			require.NoError(t, CreditAdtyTeam(int64(leader.Id), created.Team.Id, 1_000_000, "init-credit-"+tc.name))

			token := model.Token{
				UserId: member.Id, Key: "tk-life-" + tc.name, Name: "team-key",
				Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true,
			}
			require.NoError(t, db.Create(&token).Error)
			require.NoError(t, BindAdtyTeamKey(member.Id, token.Id))

			tc.revoke(t, leader.Id, member.Id, created.Team.Id, token.Id)

			// Simulate the token owner re-enabling token.status directly.
			require.NoError(t, db.Model(&model.Token{}).Where("id = ?", token.Id).Update("status", common.TokenStatusEnabled).Error)
			_, authErr := model.ValidateUserToken(token.Key)
			assert.ErrorIs(t, authErr, model.ErrTokenInvalid, "authentication must recheck team revocation from SQL")

			quota, isTeamKey, err := adtyTeamSharedQuotaForToken(token.Id)
			require.NoError(t, err)
			assert.True(t, isTeamKey, "historical team key binding must still be recognized as a team key")
			assert.Zero(t, quota, "revoked/removed/closed team key must have zero usable quota")

			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			common.SetContextKey(ctx, constant.ContextKeyTokenId, token.Id)
			info := &relaycommon.RelayInfo{
				UserId: member.Id, TokenId: token.Id, TokenKey: token.Key, TokenUnlimited: true,
				RequestId: "req-life-" + tc.name, StartTime: time.Now(), UsingGroup: "default", UserGroup: "default",
				PriceData: hosttypes.PriceData{UsePrice: true, ModelPrice: 1, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}},
			}
			session, apiErr := NewBillingSession(ctx, info, 50_000)
			require.Nil(t, session)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusForbidden, apiErr.StatusCode)

			_, err = model.ReserveBillingOperation(model.BillingOperation{
				OperationKey: "request:direct-life-" + tc.name, UserId: member.Id, TokenId: token.Id,
			}, 50_000)
			require.Error(t, err)

			handled, hookErr := reserveAdtyTeamQuotaHook(db, created.Team.Id, member.Id, 50_000, "request:hook-life-"+tc.name)
			assert.True(t, handled)
			require.Error(t, hookErr)

			var storedMember model.User
			require.NoError(t, db.First(&storedMember, member.Id).Error)
			assert.Equal(t, 500_000, storedMember.Quota, "must never fall back to personal funds")

			var storedTeam model.AdtyTeam
			require.NoError(t, db.First(&storedTeam, created.Team.Id).Error)
			assert.EqualValues(t, 1_000_000, storedTeam.SharedQuota, "team balance must remain untouched")
		})
	}
}

func TestTeamReservationCumulativeEventIdentitySurvivesInterveningBalanceChange(t *testing.T) {
	db := prepareReferralDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}))
	leader := referralUser(t, db, "res-id-leader")
	member := referralUser(t, db, "res-id-member")
	created, err := CreateAdtyTeam(leader.Id, 0, "ReserveIdentity", decimal.NewFromInt(1))
	require.NoError(t, err)
	joined, err := CreateAdtyTeamEmployee(leader.Id, created.Team.Id, "res-emp", "StrongPassword123!", "Res Emp")
	require.NoError(t, err)
	member.Id = joined.UserId
	require.NoError(t, CreditAdtyTeam(int64(leader.Id), created.Team.Id, 100_000, "res-id-credit"))

	// First reservation increment on op-1: 20,000 -> team balance becomes 80,000.
	handled, err := reserveAdtyTeamQuotaHook(db, created.Team.Id, member.Id, 20_000, "request:op-1")
	require.NoError(t, err)
	assert.True(t, handled)

	// Intervening refund on another operation restores team balance to 100,000.
	handled, applied, err := settleAdtyTeamQuotaHook(db, created.Team.Id, member.Id, -20_000, "request:op-other")
	require.NoError(t, err)
	assert.True(t, handled)
	assert.Equal(t, -20_000, applied)

	// Second reservation increment on op-1 (+20,000, cumulative 40,000) produces
	// the exact same afterBalance (80,000) but must have a distinct ledger identity.
	handled, err = reserveAdtyTeamQuotaHook(db, created.Team.Id, member.Id, 20_000, "request:op-1")
	require.NoError(t, err)
	assert.True(t, handled)

	var team model.AdtyTeam
	require.NoError(t, db.First(&team, created.Team.Id).Error)
	assert.EqualValues(t, 80_000, team.SharedQuota)

	var ledgers []model.AdtyTeamLedger
	require.NoError(t, db.Where("team_id = ? AND type = ? AND ref_id = ?", created.Team.Id, "reserve", "request:op-1").Order("id ASC").Find(&ledgers).Error)
	require.Len(t, ledgers, 2)
	assert.Equal(t, "reserve:request:op-1:20000", ledgers[0].IdempotencyKey)
	assert.Equal(t, "reserve:request:op-1:40000", ledgers[1].IdempotencyKey)
}

func TestTeamBindSerializesWithMemberRemovalAndTeamClose(t *testing.T) {
	db := prepareReferralDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}))
	leader := referralUser(t, db, "bind-lock-leader")
	member := referralUser(t, db, "bind-lock-member")
	created, err := CreateAdtyTeam(leader.Id, 0, "BindLock", decimal.NewFromInt(1))
	require.NoError(t, err)
	joined, err := CreateAdtyTeamEmployee(leader.Id, created.Team.Id, "bind-emp", "StrongPassword123!", "Bind Emp")
	require.NoError(t, err)
	member.Id = joined.UserId

	tok1 := model.Token{UserId: member.Id, Key: "bind-lock-tok-1", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	tok2 := model.Token{UserId: member.Id, Key: "bind-lock-tok-2", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, db.Create(&tok1).Error)
	require.NoError(t, db.Create(&tok2).Error)

	// Wrong target team ID must be rejected.
	require.ErrorIs(t, BindAdtyTeamKey(member.Id, tok1.Id, created.Team.Id+999), ErrAdtyTeamForbidden)

	require.NoError(t, DisableAdtyTeamMember(leader.Id, created.Team.Id, member.Id))
	require.ErrorIs(t, BindAdtyTeamKey(member.Id, tok1.Id), ErrAdtyTeamForbidden)

	var activeCount int64
	require.NoError(t, db.Model(&model.AdtyTeamKey{}).Where("team_id = ? AND user_id = ? AND status = ?", created.Team.Id, member.Id, model.AdtyTeamMemberActive).Count(&activeCount).Error)
	assert.Zero(t, activeCount)

	require.NoError(t, EnableAdtyTeamMember(leader.Id, created.Team.Id, member.Id))
	require.NoError(t, BindAdtyTeamKey(member.Id, tok1.Id, created.Team.Id))

	require.NoError(t, CloseAdtyTeam(leader.Id, created.Team.Id))
	require.Error(t, BindAdtyTeamKey(member.Id, tok2.Id))
	require.NoError(t, db.Model(&model.AdtyTeamKey{}).Where("team_id = ? AND status = ?", created.Team.Id, model.AdtyTeamMemberActive).Count(&activeCount).Error)
	assert.Zero(t, activeCount)
}

func TestTeamBindConcurrentRevocationRejectsStaleMembership(t *testing.T) {
	for _, action := range []string{"remove_member", "close_team"} {
		t.Run(action, func(t *testing.T) {
			db := prepareReferralDB(t)
			leader := referralUser(t, db, "race-leader")
			member := referralUser(t, db, "race-member")
			created, err := CreateAdtyTeam(leader.Id, 0, "ConcurrentBind", decimal.NewFromInt(1))
			require.NoError(t, err)
			joined, err := CreateAdtyTeamEmployee(leader.Id, created.Team.Id, "race-emp", "StrongPassword123!", "Race Emp")
			require.NoError(t, err)
			member.Id = joined.UserId
			token := model.Token{UserId: member.Id, Key: "race-team-key", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
			require.NoError(t, db.Create(&token).Error)

			readMembership := make(chan struct{})
			releaseBinding := make(chan struct{})
			var paused atomic.Bool
			var release sync.Once
			// Pause after the optimistic membership read, before any binding write.
			// Revocation must commit while that old active row is in memory.
			const callback = "test:pause_team_binding"
			require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
				if row, ok := tx.Statement.Dest.(*model.AdtyTeamMember); ok && row.UserId == member.Id && tx.Statement.Table == "adty_team_members" && paused.CompareAndSwap(false, true) {
					close(readMembership)
					<-releaseBinding
				}
			}))
			t.Cleanup(func() {
				release.Do(func() { close(releaseBinding) })
				_ = db.Callback().Query().Remove(callback)
			})
			bound := make(chan error, 1)
			go func() { bound <- BindAdtyTeamKey(member.Id, token.Id) }()
			select {
			case <-readMembership:
			case <-time.After(10 * time.Second):
				t.Fatal("binding never reached its membership read")
			}
			if action == "remove_member" {
				err = DisableAdtyTeamMember(leader.Id, created.Team.Id, member.Id)
			} else {
				err = CloseAdtyTeam(leader.Id, created.Team.Id)
			}
			release.Do(func() { close(releaseBinding) })
			require.NoError(t, err)
			select {
			case err = <-bound:
				require.Error(t, err, "revocation committed before binding resumed")
			case <-time.After(10 * time.Second):
				t.Fatal("binding did not finish after revocation")
			}
			var active int64
			require.NoError(t, db.Model(&model.AdtyTeamKey{}).Where("token_id = ? AND status = ?", token.Id, model.AdtyTeamMemberActive).Count(&active).Error)
			assert.Zero(t, active, "revocation cannot leave an active binding behind")
		})
	}
}

func TestTeamAdditionalReserveSerializesWithSiteMigration(t *testing.T) {
	db := prepareSiteDB(t)
	_, site := openActiveSite(t, db, "reserve-migration")
	leader := siteCustomer(t, db, site, "reserve-mig-leader")
	created, err := CreateAdtyTeam(leader.Id, site.Id, "Migration reserve", decimal.NewFromInt(1))
	require.NoError(t, err)
	token := model.Token{UserId: leader.Id, Key: "reserve-migration-key", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, BindAdtyTeamKey(leader.Id, token.Id))
	require.NoError(t, CreditAdtyTeam(int64(leader.Id), created.Team.Id, 100_000, "migration-opening"))
	spec := model.BillingOperation{OperationKey: "request:migration-reserve", UserId: leader.Id, TokenId: token.Id, TokenUnlimited: true}
	_, err = model.ReserveBillingOperation(spec, 20_000)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	userLocked := make(chan struct{})
	tokensLocked := make(chan struct{})
	releaseMigration := make(chan struct{})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(releaseMigration) }) })
	migrated := make(chan error, 1)
	go func() {
		migrated <- db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var user model.User
			if err := model.AdtyLockForUpdate(tx).First(&user, leader.Id).Error; err != nil {
				return err
			}
			close(userLocked)
			select {
			case <-releaseMigration:
			case <-ctx.Done():
				return ctx.Err()
			}
			if _, err := model.AdtySetUsersSiteTx(tx, site.Id, 0); err != nil {
				return err
			}
			return InheritAdtyTeamsOnCloseTx(tx, site.Id)
		})
	}()
	select {
	case <-userLocked:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	// A user-first reserve waits behind migration. A team-first reserve holds
	// the team until migration holds the token, exposing the inverse order.
	const callback = "test:team_reserve_migration"
	var reached atomic.Bool
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if _, locked := tx.Statement.Clauses["FOR"]; !locked {
			return
		}
		if tx.Statement.Table == "users" && reached.CompareAndSwap(false, true) {
			release.Do(func() { close(releaseMigration) })
		}
	}))
	var tokensOnce sync.Once
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback+":after", func(tx *gorm.DB) {
		if _, locked := tx.Statement.Clauses["FOR"]; !locked || tx.Error != nil {
			return
		}
		if _, ok := tx.Statement.Dest.(*[]model.Token); ok {
			tokensOnce.Do(func() { close(tokensLocked) })
		}
		if team, ok := tx.Statement.Dest.(*model.AdtyTeam); ok && team.Id == created.Team.Id && reached.CompareAndSwap(false, true) {
			release.Do(func() { close(releaseMigration) })
			select {
			case <-tokensLocked:
			case <-ctx.Done():
			}
		}
	}))
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove(callback)
		_ = db.Callback().Query().Remove(callback + ":after")
	})
	originalDB := model.DB
	model.DB = db.WithContext(ctx)
	t.Cleanup(func() { model.DB = originalDB })
	_, reserveErr := model.ReserveBillingOperation(spec, 40_000)
	select {
	case err = <-migrated:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, reserveErr)
	require.True(t, reached.Load())
	var team model.AdtyTeam
	require.NoError(t, db.First(&team, created.Team.Id).Error)
	assert.Zero(t, team.SiteId)
	assert.EqualValues(t, 60_000, team.SharedQuota)
	var personal model.User
	require.NoError(t, db.First(&personal, leader.Id).Error)
	assert.Equal(t, leader.Quota, personal.Quota)
}

func TestClosedTeamPaidTopupFulfillmentPreservesPrincipal(t *testing.T) {
	db := prepareReferralDB(t)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.AdtyTopupOrder{}, &model.AdtyOrderPaymentEvent{}, &model.PaymentOrphanEvent{}, &model.AdtyAnomalyDisposition{}))
	leader := referralUser(t, db, "closed-topup-leader")
	other := referralUser(t, db, "closed-topup-other")
	created, err := CreateAdtyTeam(leader.Id, 0, "ClosedTopup", decimal.NewFromInt(1))
	require.NoError(t, err)

	channel := seedEpayChannel(t, db, model.AdtyEpayChannel{
		Name: "closed-team-epay", GatewayURL: "https://pay.example.com", MerchantId: "m-closed",
		OrderMode: model.AdtyEpayOrderModeSubmit,
	}, alipay("submit"))

	order, err := StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(10), "alipay", channel.Id, "closed-team-paid", "127.0.0.1")
	require.NoError(t, err)

	// Close the team before the payment callback arrives.
	require.NoError(t, CloseAdtyTeam(leader.Id, created.Team.Id))

	facts := model.AdtyPaymentFacts{
		Provider:       model.PaymentProviderAdtyEpay,
		Method:         "alipay",
		PaidAmount:     order.PayAmount,
		PaidCurrency:   "CNY",
		GatewayTradeId: "gw-closed-1",
		TxHash:         "gw-closed-1",
		Source:         model.AdtyPaymentSourceCallback,
	}
	already, credited, _, _, err := model.CompleteAdtyTopUpTx(db, order.TradeNo, facts)
	require.NoError(t, err)
	assert.False(t, already)
	assert.Equal(t, 5_000_000, credited)

	var storedTeam model.AdtyTeam
	require.NoError(t, db.First(&storedTeam, created.Team.Id).Error)
	assert.Equal(t, model.AdtyTeamStatusClosed, storedTeam.Status)
	assert.EqualValues(t, 5_000_000, storedTeam.SharedQuota)
	already, _, _, _, err = model.CompleteAdtyTopUpTx(db, order.TradeNo, facts)
	require.NoError(t, err)
	assert.True(t, already)
	require.NoError(t, db.First(&storedTeam, created.Team.Id).Error)
	assert.EqualValues(t, 5_000_000, storedTeam.SharedQuota)
	var ledgers []model.AdtyTeamLedger
	require.NoError(t, db.Where("team_id = ? AND type = ? AND ref_id = ?", created.Team.Id, "topup", order.TradeNo).Find(&ledgers).Error)
	require.Len(t, ledgers, 1)
	assert.EqualValues(t, 5_000_000, ledgers[0].DeltaQuota)
	var personal model.User
	require.NoError(t, db.First(&personal, leader.Id).Error)
	assert.Equal(t, leader.Quota, personal.Quota)

	var dispositions []model.AdtyAnomalyDisposition
	require.NoError(t, db.Where("kind = ? AND ref = ?", "closed_team_topup", order.TradeNo).Find(&dispositions).Error)
	require.Len(t, dispositions, 1)
	assert.Equal(t, "open", dispositions[0].Status)

	// Mismatched leader must fail with a permanent status error for review routing.
	badTopUp := &model.TopUp{UserId: other.Id, Amount: 10, TradeNo: "TTEPbadleader00000001"}
	badExt := &model.AdtyTopupOrder{TradeNo: badTopUp.TradeNo, UserId: other.Id, TeamId: created.Team.Id}
	err = creditAdtyTeamTopUpTx(db, badTopUp, badExt, 5_000_000)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	require.ErrorIs(t, err, model.ErrTopUpStatusInvalid)
}

func TestIndependentTeamEmployeeLifecycleAndIsolation(t *testing.T) {
	db := prepareReferralDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.AdtySiteStaff{}))
	leader := referralUser(t, db, "emp-life-leader")
	otherLeader := referralUser(t, db, "emp-life-leader2")
	owner := referralUser(t, db, "emp-life-owner")
	staff := referralUser(t, db, "emp-life-staff")
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "emp-site", Status: model.AdtySiteStatusActive, CostFactor: decimalPtr("0.80")}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(staff).Updates(map[string]any{"site_id": site.Id, "staff_site_id": site.Id}).Error)
	require.NoError(t, db.Create(&model.AdtySiteStaff{
		SiteId: site.Id, OwnerUserId: owner.Id, UserId: staff.Id, Username: staff.Username,
		DisplayName: "Staff", Role: "support", Status: "enabled", Version: 1,
	}).Error)

	team1, err := CreateAdtyTeam(leader.Id, 0, "EmpTeam1", decimal.NewFromInt(1))
	require.NoError(t, err)
	team2, err := CreateAdtyTeam(otherLeader.Id, 0, "EmpTeam2", decimal.NewFromInt(1))
	require.NoError(t, err)

	// Site owner and site staff cannot join as team employees.
	_, err = JoinAdtyTeam(owner.Id, team1.Team.Id)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
	_, err = JoinAdtyTeam(staff.Id, team1.Team.Id)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)

	// Leader creates an independent team employee directly.
	emp1, err := CreateAdtyTeamEmployee(leader.Id, team1.Team.Id, "teamemp01", "StrongPassword123!", "Emp One")
	require.NoError(t, err)
	assert.True(t, emp1.IndependentEmployee)
	assert.Equal(t, model.AdtyTeamMemberActive, emp1.Status)

	// Leader invites and registers a second independent team employee via invite code.
	invite, err := CreateAdtyTeamInvite(leader.Id, team1.Team.Id, time.Hour)
	require.NoError(t, err)
	emp2, err := RegisterAdtyTeamEmployee(invite.Code, "teamemp02", "StrongPassword123!", "Emp Two", 0)
	require.NoError(t, err)
	assert.True(t, emp2.IndependentEmployee)
	assert.Equal(t, model.AdtyTeamMemberActive, emp2.Status)

	tok := model.Token{UserId: emp1.UserId, Key: "emp1-team-key", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, db.Create(&tok).Error)
	require.NoError(t, BindAdtyTeamKey(emp1.UserId, tok.Id))

	// Disable/remove emp1: revokes keys, disables login account, and still blocks joining team2.
	require.NoError(t, DisableAdtyTeamMember(leader.Id, team1.Team.Id, emp1.UserId))
	var emp1User model.User
	require.NoError(t, db.First(&emp1User, emp1.UserId).Error)
	assert.Equal(t, common.UserStatusDisabled, emp1User.Status)
	_, err = JoinAdtyTeam(emp1.UserId, team2.Team.Id)
	require.Error(t, err)

	// Re-enable emp1 on team1: user and membership become active again, old key stays disabled.
	require.NoError(t, EnableAdtyTeamMember(leader.Id, team1.Team.Id, emp1.UserId))
	require.NoError(t, db.First(&emp1User, emp1.UserId).Error)
	assert.Equal(t, common.UserStatusEnabled, emp1User.Status)
	require.NoError(t, db.First(&tok, tok.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, tok.Status)

	// Delete emp1: permanently marks deleted, disables user, cannot be re-enabled.
	require.NoError(t, DeleteAdtyTeamMember(leader.Id, team1.Team.Id, emp1.UserId))
	require.ErrorIs(t, EnableAdtyTeamMember(leader.Id, team1.Team.Id, emp1.UserId), ErrAdtyTeamForbidden)
	_, err = JoinAdtyTeam(emp1.UserId, team1.Team.Id)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)
}

func TestLeaderLeaveClosesTeamAndPreservesSharedBalance(t *testing.T) {
	db := prepareReferralDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}))
	leader := referralUser(t, db, "leader-exit-ld")
	member := referralUser(t, db, "leader-exit-mem")
	created, err := CreateAdtyTeam(leader.Id, 0, "LeaderExit", decimal.NewFromInt(1))
	require.NoError(t, err)
	joined, err := CreateAdtyTeamEmployee(leader.Id, created.Team.Id, "exit-emp", "StrongPassword123!", "Exit Emp")
	require.NoError(t, err)
	member.Id = joined.UserId
	require.NoError(t, CreditAdtyTeam(int64(leader.Id), created.Team.Id, 750_000, "leader-exit-credit"))

	tok := model.Token{UserId: member.Id, Key: "leader-exit-key", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, db.Create(&tok).Error)
	require.NoError(t, BindAdtyTeamKey(member.Id, tok.Id))

	require.NoError(t, LeaveAdtyTeam(leader.Id, created.Team.Id))

	var team model.AdtyTeam
	require.NoError(t, db.First(&team, created.Team.Id).Error)
	assert.Equal(t, model.AdtyTeamStatusClosed, team.Status)
	assert.NotZero(t, team.ClosedAt)
	assert.Equal(t, leader.Id, team.LeaderUserId, "leadership must not transfer to another member")
	assert.EqualValues(t, 750_000, team.SharedQuota, "shared funds must be preserved on closure")

	var activeMembers int64
	require.NoError(t, db.Model(&model.AdtyTeamMember{}).Where("team_id = ? AND status = ?", team.Id, model.AdtyTeamMemberActive).Count(&activeMembers).Error)
	assert.Zero(t, activeMembers)

	require.NoError(t, db.First(&tok, tok.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, tok.Status)
}
