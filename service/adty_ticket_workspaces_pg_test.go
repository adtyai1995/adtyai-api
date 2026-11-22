package service

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestAdtyTicketWorkspacesPostgres(t *testing.T) {
	dsn := os.Getenv("ADTY_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set ADTY_TEST_PG_DSN to an isolated PostgreSQL 18.x database URL")
	}
	uri, err := url.Parse(dsn)
	require.NoError(t, err)
	base, err := gorm.Open(postgres.Open(dsn))
	require.NoError(t, err)
	basePool, err := base.DB()
	require.NoError(t, err)
	defer basePool.Close()
	var version int
	require.NoError(t, base.Raw("SHOW server_version_num").Scan(&version).Error)
	require.GreaterOrEqual(t, version, 180000)
	require.Less(t, version, 190000)
	t.Logf("server_version_num=%d", version)
	schema := fmt.Sprintf("ticket_workspaces_%d", time.Now().UnixNano())
	require.NoError(t, base.Exec("CREATE SCHEMA "+schema).Error)
	defer base.Exec("DROP SCHEMA " + schema + " CASCADE")
	params := uri.Query()
	params.Set("search_path", schema)
	uri.RawQuery = params.Encode()
	db, err := gorm.Open(postgres.Open(uri.String()))
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	oldDB, oldLog := model.DB, model.LOG_DB
	defer common.SetDatabaseTypes(common.MainDatabaseType(), common.LogDatabaseType())
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	model.DB, model.LOG_DB = db, db
	defer func() { model.DB, model.LOG_DB = oldDB, oldLog }()
	restore := SetAdtyTicketImageDirForTest(t.TempDir())
	defer restore()
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.AdtySite{}, &model.AdtySiteStaff{}, &model.AdtyTeam{}, &model.AdtyTeamMember{},
		&model.AdtyTicket{}, &model.AdtyTicketMessage{}, &model.AdtyTicketEvent{}, &model.AdtyTicketImage{}, &model.AdtyTicketPolicy{}, &model.AdtyTicketNotice{},
		&model.AdtyTelegramBot{}, &model.AdtyTelegramBinding{}, &model.AdtyTelegramDelivery{}, &model.CasbinRule{}, &model.AuditIntent{}, &model.AuditLog{}))
	authz.AdtyRegisterResources()
	const (
		owner     = 1
		staff     = 2
		customer  = 3
		leader    = 4
		other     = 5
		mainUser  = 6
		admin     = 7
		root      = 8
		ungranted = 9
	)
	users := []model.User{
		{Id: owner}, {Id: staff, SiteId: 11, StaffSiteId: 11}, {Id: customer, SiteId: 11}, {Id: leader, SiteId: 11}, {Id: other, SiteId: 12}, {Id: mainUser}, {Id: admin, Role: common.RoleAdminUser}, {Id: root, Role: common.RoleRootUser}, {Id: ungranted, SiteId: 11, StaffSiteId: 11},
	}
	for i := range users {
		users[i].Username = fmt.Sprintf("user%d", users[i].Id)
		users[i].DisplayName = users[i].Username
		users[i].Email = users[i].Username + "@example.org"
		users[i].Status = common.UserStatusEnabled
		if users[i].Role == 0 {
			users[i].Role = common.RoleCommonUser
		}
	}
	require.NoError(t, db.Create(&users).Error)
	site := model.AdtySite{Id: 11, Subdomain: "tickets", OwnerUserId: owner, Status: model.AdtySiteStatusActive, AgreementVer: AdtyCurrentSiteAgreementVersion()}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Create(&[]model.AdtySiteStaff{
		{UserId: staff, OwnerUserId: owner, Role: "support", SiteId: site.Id, Status: "enabled", Permissions: `["tickets.manage"]`},
		{UserId: ungranted, OwnerUserId: owner, Role: "support", SiteId: site.Id, Status: "enabled", Permissions: `["customers.read"]`},
	}).Error)
	team := model.AdtyTeam{SiteId: site.Id, LeaderUserId: leader, Status: model.AdtyTeamStatusActive}
	require.NoError(t, db.Create(&team).Error)
	require.NoError(t, db.Create(&[]model.AdtyTeamMember{
		{TeamId: team.Id, UserId: customer, Role: model.AdtyTeamRoleMember, Status: "active"}, {TeamId: team.Id, UserId: leader, Role: model.AdtyTeamRoleLeader, Status: "active"},
	}).Error)

	var first *model.AdtyTicket
	for _, issue := range []string{"billing", "api", "account", "other"} {
		created, err := CreateAdtyTicket(customer, AdtyTicketInput{Type: issue, Title: issue, Body: "question"})
		require.NoError(t, err)
		require.Equal(t, issue, created.Type)
		require.Equal(t, team.Id, created.TeamId)
		if first == nil {
			first = created
		}
	}
	history := make([]model.AdtyTicket, 101)
	for i := range history {
		history[i] = model.AdtyTicket{SiteId: 11, UserId: customer, Title: "Historical", Type: "other", Status: model.AdtyTicketNew}
	}
	require.NoError(t, db.Create(&history).Error)
	external, err := CreateAdtyTicket(other, AdtyTicketInput{Title: "Other site", Body: "question"})
	require.NoError(t, err)
	_, err = CreateAdtyTicket(mainUser, AdtyTicketInput{Title: "Main site", Body: "question"})
	require.NoError(t, err)
	historical, err := ListAdtyTicketPage(staff, "pending", 6, 20, "staff")
	require.NoError(t, err)
	require.Len(t, historical.Tickets, 5)
	require.EqualValues(t, 105, historical.Total)
	require.EqualValues(t, historical.Total, historical.Badges.Pending)
	require.Equal(t, first.Id, historical.Tickets[4].Id)
	for _, check := range []struct {
		user  int
		scope string
		total int64
	}{
		{owner, "personal", 0}, {owner, "owner", 105}, {customer, "personal", 105}, {leader, "personal", 0}, {leader, "team", 4}, {root, "platform", 107},
	} {
		got, err := ListAdtyTicketPage(check.user, "", 1, 20, check.scope)
		require.NoError(t, err)
		require.Equal(t, check.total, got.Total)
	}
	for _, check := range []struct {
		user  int
		scope string
	}{{staff, "owner"}, {ungranted, "staff"}, {customer, "team"}, {admin, "platform"}} {
		_, err := ListAdtyTicketPage(check.user, "", 1, 20, check.scope)
		require.Error(t, err)
	}
	_, err = GetAdtyTicket(owner, external.Id, "owner")
	require.Error(t, err)
	_, err = GetAdtyTicket(staff, first.Id, "personal")
	require.Error(t, err)
	_, err = GetAdtyTicket(other, first.Id, "personal")
	require.Error(t, err)

	detail, err := GetAdtyTicket(owner, first.Id, "owner")
	require.NoError(t, err)
	var ids []int
	for _, handler := range detail.Assignees {
		ids = append(ids, handler.Id)
	}
	require.ElementsMatch(t, []int{owner, staff}, ids)
	require.Error(t, SetAdtyTicketStatus(owner, first.Id, "", "", ungranted, "owner"))
	require.NoError(t, SetAdtyTicketStatus(owner, first.Id, "", "", staff, "owner"))
	require.NoError(t, SetAdtyTicketStatus(staff, first.Id, "", "", owner, "staff"))
	require.NoError(t, SetAdtyTicketStatus(staff, first.Id, "", "urgent", 0, "staff"))
	urgent, err := ListAdtyTicketPage(owner, "urgent", 1, 20, "owner")
	require.NoError(t, err)
	require.EqualValues(t, 1, urgent.Badges.Urgent)
	require.Equal(t, owner, urgent.Tickets[0].AssigneeId)
	_, err = NoteAdtyTicket(staff, first.Id, "internal", "note-1", "staff")
	require.NoError(t, err)
	_, err = ReplyAdtyTicket(staff, first.Id, "public", "reply-1", "staff")
	require.NoError(t, err)
	var img bytes.Buffer
	require.NoError(t, png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	attachment, err := AddAdtyTicketImage(staff, first.Id, "internal", img.Bytes(), "staff")
	require.NoError(t, err)
	_, _, err = OpenAdtyTicketImage(staff, first.Id, attachment.Id, "staff")
	require.NoError(t, err)
	customerDetail, err := GetAdtyTicket(customer, first.Id, "personal")
	require.NoError(t, err)
	require.Len(t, customerDetail.Messages, 2)
	require.Empty(t, customerDetail.Images)
	require.Empty(t, customerDetail.Assignees)
	for _, event := range customerDetail.Events {
		require.NotContains(t, []string{"note", "image_upload", "image_view"}, event.Action)
	}
	_, _, err = OpenAdtyTicketImage(customer, first.Id, attachment.Id, "personal")
	require.Error(t, err)
	detail, err = GetAdtyTicket(owner, first.Id, "owner")
	require.NoError(t, err)
	for _, action := range []string{"assign", "transfer", "priority", "note", "reply"} {
		require.True(t, slices.ContainsFunc(detail.Events, func(ev model.AdtyTicketEvent) bool { return ev.Action == action }), action)
	}
	rule := model.CasbinRule{Ptype: "p", V0: authz.UserSubject(admin), V1: authz.ResourceAdtyTicket, V2: "manage", V3: "allow"}
	require.NoError(t, db.Create(&rule).Error)
	mainQueue, err := ListAdtyTicketPage(admin, "", 1, 20, "platform")
	require.NoError(t, err)
	require.EqualValues(t, 1, mainQueue.Total)
	require.NoError(t, db.Model(&rule).Update("v2", "manage_all").Error)
	globalQueue, err := ListAdtyTicketPage(admin, "", 1, 20, "platform")
	require.NoError(t, err)
	require.EqualValues(t, 107, globalQueue.Total)
	for _, token := range []struct {
		grants []string
		total  int64
	}{
		{[]string{"profile:read"}, -1},
		{[]string{"profile:read", "adty_ticket:manage"}, 1},
		{[]string{"profile:read", "adty_ticket:manage_all"}, 107},
	} {
		scope := AdtyTicketTokenScope("platform", "GET", token.grants)
		page, err := ListAdtyTicketPage(admin, "", 1, 20, scope)
		if token.total < 0 {
			require.ErrorIs(t, err, ErrAdtyTicketForbidden)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, token.total, page.Total)
	}
	require.Equal(t, "denied", AdtyTicketTokenScope("owner", "POST", []string{"wallet:read"}))
	require.Equal(t, "owner", AdtyTicketTokenScope("owner", "GET", []string{"wallet:read"}))
	_, _, err = OpenAdtyTicketImage(admin, first.Id, attachment.Id, "platform")
	require.NoError(t, err)
	require.NoError(t, db.Delete(&rule).Error)
	_, _, err = OpenAdtyTicketImage(admin, first.Id, attachment.Id, "platform")
	require.Error(t, err)
	require.NoError(t, db.Model(&model.AdtySiteStaff{}).Where("user_id = ?", staff).Update("permissions", "[]").Error)
	_, _, err = OpenAdtyTicketImage(staff, first.Id, attachment.Id, "staff")
	require.Error(t, err)
	require.Error(t, SetAdtyTicketStatus(owner, first.Id, "", "", staff, "owner"))
}
