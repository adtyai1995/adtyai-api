package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"

	"fmt"
	"image"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestAdtyTicketWorkspacesPostgres(t *testing.T) {
	dsn := os.Getenv("ADTY_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set ADTY_TEST_PG_DSN to an isolated PostgreSQL 18.x database URL")
	}
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
	schemaDSN := dsn
	if strings.Contains(dsn, "://") {
		uri, err := url.Parse(dsn)
		require.NoError(t, err)
		params := uri.Query()
		params.Set("search_path", schema)
		uri.RawQuery = params.Encode()
		schemaDSN = uri.String()
	} else {
		schemaDSN += " search_path=" + schema
	}
	db, err := gorm.Open(postgres.Open(schemaDSN))
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
		&model.AdtyTopupOrder{}, &model.AdtyManualRefund{}, &model.AdtyBalanceLedger{}, &model.AdtyVoucher{}, &model.AdtyTeamKey{}, &model.Log{}, &model.BillingOperation{}, &model.AdtyNotificationPublication{}, &model.AdtyNotificationRead{}, &model.AdtyNotificationHide{}, &model.AdtyGlossaryTerm{}, &model.AdtyTranslationUsage{}, &model.AdtyTranslationConfig{}, &model.AdtyTranslationHead{}, &model.AdtyTranslationJob{}, &model.AdtyTelegramBot{}, &model.AdtyTelegramChallenge{}, &model.AdtyTelegramBinding{}, &model.AdtyTelegramDelivery{}, &model.CasbinRule{}, &model.AuditIntent{}, &model.AuditLog{}))
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
		input := AdtyTicketInput{Type: issue, Title: issue, Body: "question"}
		if issue == "api" {
			input.Audience = "team"
		}
		created, err := CreateAdtyTicket(customer, input)
		require.NoError(t, err)
		require.Equal(t, issue, created.Type)
		if issue == "api" {
			require.Equal(t, team.Id, created.TeamId)
		} else {
			require.Zero(t, created.TeamId, "a personal issue must be private")
		}
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
		{owner, "personal", 0}, {owner, "owner", 105}, {customer, "personal", 105}, {leader, "personal", 0}, {leader, "team", 1}, {root, "platform", 107},
	} {
		got, err := ListAdtyTicketPage(check.user, "", 1, 20, check.scope)
		require.NoError(t, err)
		require.Equal(t, check.total, got.Total)
	}
	for _, check := range []struct {
		user  int
		scope string
	}{{staff, "owner"}, {ungranted, "staff"}, {admin, "platform"}} {
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
	t.Run("retries, inbox, attachment ownership and terminal states", func(t *testing.T) {
		require.NoError(t, db.Create(&model.Log{UserId: customer, RequestId: "req-test"}).Error)
		input := AdtyTicketInput{Title: "Repeat", Body: "Question", Idempotency: "repeat-create", RelatedKind: "request", RelatedRef: "req-test"}
		one, err := CreateAdtyTicket(customer, input)
		require.NoError(t, err)
		again, err := CreateAdtyTicket(customer, input)
		require.NoError(t, err)
		require.Equal(t, one.Id, again.Id)
		page, err := SearchAdtyTicketPage(owner, "", "req-test", 0, 1, 20, "owner")
		require.NoError(t, err)
		require.EqualValues(t, 1, page.Total)
		empty, err := SearchAdtyTicketPage(other, "", "req-test", 0, 1, 20, "personal")
		require.NoError(t, err)
		require.Zero(t, empty.Total)
		require.NoError(t, SetAdtyTicketStatus(owner, one.Id, "", "", staff, "owner"))
		msg, err := ReplyAdtyTicket(staff, one.Id, "Please add the timestamp", "r1", "staff")
		require.NoError(t, err)
		replay, err := ReplyAdtyTicket(staff, one.Id, msg.Body, "r1", "staff")
		require.NoError(t, err)
		require.Equal(t, msg.Id, replay.Id)
		image, err := AttachAdtyTicketImage(staff, one.Id, msg.Id, "public", img.Bytes(), "staff")
		require.NoError(t, err)
		require.Equal(t, msg.Id, image.MessageId)
		_, err = AttachAdtyTicketImage(owner, one.Id, msg.Id, "public", img.Bytes(), "owner")
		require.Error(t, err)
		_, err = AttachAdtyTicketImage(staff, first.Id, msg.Id, "public", img.Bytes(), "staff")
		require.Error(t, err)
		d, err := GetAdtyTicket(customer, one.Id, "personal")
		require.NoError(t, err)
		require.Equal(t, model.AdtyTicketWaitingUser, d.Ticket.Status)
		inbox, err := GetAdtyNotificationInbox(customer, &site, 0)
		require.NoError(t, err)
		var key string
		for _, item := range inbox.Announcements {
			if item["ticket_path"] == fmt.Sprintf("/profile#case-%d", one.Id) && item["title"] == fmt.Sprintf("工单 #%d 新回复 / Ticket #%d new reply", one.Id, one.Id) {
				key = item["read_key"].(string)
			}
		}
		require.NotEmpty(t, key)
		require.NoError(t, MarkAdtyNotificationInboxRead(customer, &site, []string{key}))
		require.Error(t, MarkAdtyNotificationInboxRead(other, &site, []string{key}))
		updated, err := GetAdtyNotificationInbox(customer, &site, 0)
		require.NoError(t, err)
		require.Equal(t, inbox.UnreadPublications-1, updated.UnreadPublications)
		require.Contains(t, updated.ReadKeys, key)
		_, err = ReplyAdtyTicket(customer, one.Id, "10:15 UTC", "c1", "personal")
		require.NoError(t, err)
		d, err = GetAdtyTicket(customer, one.Id, "personal")
		require.NoError(t, err)
		require.Equal(t, model.AdtyTicketInProgress, d.Ticket.Status)
		require.NoError(t, SetAdtyTicketStatus(owner, one.Id, "resolve", "", 0, "owner"))
		_, err = ReplyAdtyTicket(customer, one.Id, "late", "c2", "personal")
		require.Error(t, err)
		require.NoError(t, SetAdtyTicketStatus(customer, one.Id, "reopen", "", 0, "personal"))
		_, err = ReplyAdtyTicket(customer, one.Id, "new evidence", "c2", "personal")
		require.NoError(t, err)
	})
	t.Run("only authorized records may be linked", func(t *testing.T) {
		for _, item := range []struct {
			ref      string
			payer    int64
			audience string
		}{{"personal-request", 0, "personal"}, {"team-request", team.Id, "team"}} {
			require.NoError(t, db.Create(&model.BillingOperation{Kind: "request", OperationKey: "request:" + item.ref, UserId: customer, PayerTeamId: item.payer}).Error)
			audience, err := AdtyTicketRequestAudience(customer, item.ref)
			require.NoError(t, err)
			require.Equal(t, item.audience, audience)
			tk, err := CreateAdtyTicket(customer, AdtyTicketInput{Title: "Usage", Body: "Question", RelatedKind: "request", RelatedRef: item.ref, Audience: audience})
			require.NoError(t, err)
			require.Equal(t, item.payer, tk.TeamId)
			_, err = AdtyTicketRequestAudience(other, item.ref)
			require.Error(t, err)
		}
		personal := model.AdtyTopupOrder{TradeNo: "private-order", UserId: other, SiteId: 12, RefundedAt: 1}
		shared := model.AdtyTopupOrder{TradeNo: "team-order", UserId: customer, TeamId: team.Id, SiteId: site.Id}
		require.NoError(t, db.Create(&personal).Error)
		require.NoError(t, db.Create(&shared).Error)
		for _, kind := range []string{"topup", "refund", "team_order", "team_refund", "ledger", "request", "voucher", "team_balance", "team_key", "unsupported"} {
			input := AdtyTicketInput{Title: "Question", Body: "Private information", RelatedKind: kind, RelatedRef: personal.TradeNo}
			if strings.HasPrefix(kind, "team_") {
				input.Audience = "team"
			}
			_, err := CreateAdtyTicket(customer, input)
			require.ErrorIs(t, err, ErrAdtyTicketInvalid, kind)
		}
		_, err := CreateAdtyTicket(customer, AdtyTicketInput{Title: "Team", Body: "Request", RelatedKind: "topup", RelatedRef: shared.TradeNo})
		require.ErrorIs(t, err, ErrAdtyTicketInvalid)
		ok, err := CreateAdtyTicket(customer, AdtyTicketInput{Title: "Team", Body: "Request", RelatedKind: "team_order", RelatedRef: shared.TradeNo, Audience: "team"})
		require.NoError(t, err)
		require.Equal(t, team.Id, ok.TeamId)
		ok, err = CreateAdtyTicket(other, AdtyTicketInput{Title: "Refund", Body: "Request", RelatedKind: "refund", RelatedRef: personal.TradeNo})
		require.NoError(t, err)
		require.Zero(t, ok.TeamId)
	})

	t.Run("terminal retention, translation fallback and policy byte boundaries", func(t *testing.T) {
		one, err := CreateAdtyTicket(customer, AdtyTicketInput{Title: "Retention", Body: "Request"})
		require.NoError(t, err)
		// Force the reservation write to fail after inserting the reply.
		// Neither the public message nor its notification may escape rollback.
		require.NoError(t, db.Exec("ALTER TABLE adty_translation_jobs ADD CONSTRAINT simulate_job_failure CHECK (status <> 'pending') NOT VALID").Error)
		_, err = ReplyAdtyTicket(staff, one.Id, "请补充请求编号。", "zh", "staff")
		require.Error(t, err)
		var partial int64
		require.NoError(t, db.Model(&model.AdtyTicketMessage{}).Where("ticket_id=? AND author_id=?", one.Id, staff).Count(&partial).Error)
		require.Zero(t, partial)
		require.NoError(t, db.Model(&model.AdtyTicketNotice{}).Where("ticket_id=? AND action='reply'", one.Id).Count(&partial).Error)
		require.Zero(t, partial)
		require.NoError(t, db.Exec("ALTER TABLE adty_translation_jobs DROP CONSTRAINT simulate_job_failure").Error)
		msg, err := ReplyAdtyTicket(staff, one.Id, "请补充请求编号。", "zh", "staff")
		require.NoError(t, err)
		d, err := GetAdtyTicket(customer, one.Id, "personal")
		require.NoError(t, err)
		reply := d.Messages[len(d.Messages)-1]
		require.Equal(t, msg.Id, reply.Id)
		require.Equal(t, model.AdtyTranslationPending, reply.TranslationStatus)
		var job model.AdtyTranslationJob
		require.NoError(t, db.Where("object_key=?", ticketTranslationKey(msg.Id)).First(&job).Error)
		require.Equal(t, model.AdtyTranslationPending, job.Status)
		// Simulate a process loss just after claim. The lease stops duplicate
		// attempts while active and the next worker can recover it after expiry.
		lease := time.Now().Add(time.Minute)
		require.NoError(t, db.Model(&model.AdtyTranslationHead{}).Where("object_key=?", job.ObjectKey).Update("lease_until", lease.Unix()).Error)
		require.NoError(t, processPendingTicketTranslations(time.Now()))
		require.NoError(t, db.First(&job, job.Id).Error)
		require.Equal(t, model.AdtyTranslationPending, job.Status)
		require.NoError(t, processPendingTicketTranslations(lease.Add(time.Second)))
		require.NoError(t, db.First(&job, job.Id).Error)
		require.Equal(t, model.AdtyTranslationFailed, job.Status)
		// A pre-upgrade reply can be missing its head and job. Restart repair is idempotent.
		require.NoError(t, db.Where("object_key=?", job.ObjectKey).Delete(&model.AdtyTranslationJob{}).Error)
		require.NoError(t, db.Where("object_key=?", job.ObjectKey).Delete(&model.AdtyTranslationHead{}).Error)
		require.NoError(t, backfillTicketReplyTranslations())
		require.NoError(t, backfillTicketReplyTranslations())
		var repaired int64
		require.NoError(t, db.Model(&model.AdtyTranslationJob{}).Where("object_key=? AND status=?", job.ObjectKey, model.AdtyTranslationPending).Count(&repaired).Error)
		require.EqualValues(t, 1, repaired)
		require.NoError(t, db.Create(&model.AdtyTranslationConfig{Id: 1, Enabled: true, ModelName: "ticket-test"}).Error)
		previous := AdtyTranslateFunc
		defer func() { AdtyTranslateFunc = previous }()
		calls := 0
		AdtyTranslateFunc = func(modelName, source string) (string, error) {
			calls++
			require.Equal(t, msg.Body, source)
			return "Please share the request ID.", nil
		}
		require.NoError(t, processPendingTicketTranslations(time.Now()))
		require.NoError(t, processPendingTicketTranslations(time.Now()))
		require.NoError(t, db.Model(&model.AdtyTranslationHead{}).Where("object_key=?", job.ObjectKey).Updates(map[string]any{"status": model.AdtyTranslationFailed, "english": ""}).Error)
		queued, err := RetryAdtyTranslationJob(job.ObjectKey, 1)
		require.NoError(t, err)
		require.Equal(t, model.AdtyTranslationPending, queued.Status)
		require.Equal(t, 1, calls, "manual retry only queues a ticket translation")
		require.NoError(t, processPendingTicketTranslations(time.Now()))
		require.Equal(t, 2, calls)
		require.NotEmpty(t, reply.Body)
		d, err = GetAdtyTicket(customer, one.Id, "personal")
		require.NoError(t, err)
		require.Equal(t, "Please share the request ID.", d.Messages[len(d.Messages)-1].English)
		policy := adtyTicketPolicy()
		policy.MaxBytes = img.Len()
		require.NoError(t, SaveAdtyTicketImagePolicy(policy))
		q, err := ListAdtyTicketPage(customer, "", 1, 20, "personal")
		require.NoError(t, err)
		require.Equal(t, img.Len(), q.ImagePolicy.MaxBytes)
		_, err = AttachAdtyTicketImage(staff, one.Id, msg.Id, "public", img.Bytes(), "staff")
		require.NoError(t, err)
		_, err = AttachAdtyTicketImage(staff, one.Id, msg.Id, "public", append(img.Bytes(), 0), "staff")
		require.Error(t, err)
		require.NoError(t, SetAdtyTicketStatus(owner, one.Id, "close", "", 0, "owner"))
		require.NoError(t, db.Model(&model.AdtyTicketEvent{}).Where("ticket_id=? AND action='status' AND detail=?", one.Id, model.AdtyTicketClosed).Update("created_at", 1).Error)
		require.NoError(t, SetAdtyTicketStatus(customer, one.Id, "reopen", "", 0, "personal"))
		removed, _, err := deleteOneExpiredSupportTicket(context.Background(), one.Id, time.Now().Unix()+1)
		require.NoError(t, err)
		require.False(t, removed)
		require.NoError(t, SetAdtyTicketStatus(owner, one.Id, "close", "", 0, "owner"))
		removed, _, err = deleteOneExpiredSupportTicket(context.Background(), one.Id, time.Now().Unix()+1)
		require.NoError(t, err)
		require.True(t, removed)
		for _, record := range []any{&model.AdtyTranslationHead{}, &model.AdtyTranslationJob{}} {
			var copies int64
			require.NoError(t, db.Model(record).Where("object_key=?", ticketTranslationKey(msg.Id)).Count(&copies).Error)
			require.Zero(t, copies)
		}
	})

	t.Run("team follow-up reaches current assignee and remains fixed through transfer", func(t *testing.T) {
		shared, err := CreateAdtyTicket(customer, AdtyTicketInput{Audience: "team", Type: "api", Title: "Team Key", Body: "Team question"})
		require.NoError(t, err)
		require.NoError(t, SetAdtyTicketStatus(owner, shared.Id, "", "", staff, "owner"))
		msg, err := ReplyAdtyTicket(leader, shared.Id, "Team evidence", "tl", "team")
		require.NoError(t, err)
		var target model.AdtyTicketNotice
		require.NoError(t, db.Where("ticket_id=? AND action='reply'", shared.Id).First(&target).Error)
		require.Equal(t, staff, target.UserId)
		require.NoError(t, SetAdtyTicketStatus(owner, shared.Id, "", "", owner, "owner"))
		require.NoError(t, db.Where("event_key=?", target.EventKey).Delete(&model.AdtyTelegramDelivery{}).Error)
		require.NoError(t, db.Model(&target).Update("telegram_pending", true).Error)
		recovered, err := SyncAdtyTicketEventNotices()
		require.NoError(t, err)
		require.Equal(t, 1, recovered)
		recovered, err = SyncAdtyTicketEventNotices()
		require.NoError(t, err)
		require.Zero(t, recovered)
		var delivery model.AdtyTelegramDelivery
		require.NoError(t, db.Where("event_key=?", target.EventKey).First(&delivery).Error)
		require.Equal(t, staff, delivery.UserId)
		require.Equal(t, target.CreatedAt, delivery.CreatedAt)
		d, err := GetAdtyTicket(customer, shared.Id, "personal")
		require.NoError(t, err)
		require.Equal(t, msg.Id, d.Messages[len(d.Messages)-1].Id)
		var rootNotices int64
		require.NoError(t, db.Model(&model.AdtyTicketNotice{}).Where("user_id=? AND site_id=0 AND purpose=?", root, model.AdtyTelegramPurposeAdmin).Count(&rootNotices).Error)
		require.Positive(t, rootNotices)
	})

	t.Run("R23 departed creator loses team access and deliveries", func(t *testing.T) {
		tk, err := CreateAdtyTicket(customer, AdtyTicketInput{Audience: "team", Title: "Shared", Body: "Private material"})
		require.NoError(t, err)
		im, err := AddAdtyTicketImage(leader, tk.Id, "public", img.Bytes(), "team")
		require.NoError(t, err)
		member := db.Model(&model.AdtyTeamMember{}).Where("user_id=?", customer)
		require.NoError(t, member.Update("status", "removed").Error)
		defer member.Update("status", "active")
		_, err = ReplyAdtyTicket(staff, tk.Id, "Follow up", "departed", "staff")
		require.NoError(t, err)
		_, err = GetAdtyTicket(customer, tk.Id, "personal")
		require.Error(t, err)
		_, _, err = OpenAdtyTicketImage(customer, tk.Id, im.Id, "personal")
		require.Error(t, err)
		var notices int64
		require.NoError(t, db.Model(&model.AdtyTicketNotice{}).Where("ticket_id=? AND user_id=? AND action='reply'", tk.Id, customer).Count(&notices).Error)
		require.Zero(t, notices)
		var recipient model.AdtyTicketNotice
		require.NoError(t, db.Where("ticket_id=? AND user_id=? AND action='reply'", tk.Id, leader).First(&recipient).Error)
		item, ok, err := GetAdtyNotificationDetail(leader, &site, recipient.ReadKey)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, fmt.Sprintf("/profile#case-%d-team", tk.Id), item["ticket_path"])
		_, err = GetAdtyTicket(customer, first.Id, "personal")
		require.NoError(t, err)
	})
	t.Run("replay create after closure migration", func(t *testing.T) {
		input := AdtyTicketInput{Title: "Original", Body: "Lost response", Idempotency: "migration"}
		tk, err := CreateAdtyTicket(other, input)
		require.NoError(t, err)
		require.NoError(t, db.Model(&model.User{}).Where("id=?", other).Update("site_id", 0).Error)
		defer db.Model(&model.User{}).Where("id=?", other).Update("site_id", 12)
		retry, err := CreateAdtyTicket(other, input)
		require.NoError(t, err)
		require.Equal(t, tk.Id, retry.Id)
	})
	t.Run("failed request diagnostic survives log expiry", func(t *testing.T) {
		log := model.Log{UserId: customer, RequestId: "diagnostic", CreatedAt: 123, Type: model.LogTypeError, ModelName: "chat", Group: "default", Other: `{"error_type":"rate_limit","api_key":"do-not-copy"}`}
		require.NoError(t, db.Create(&log).Error)
		tk, err := CreateAdtyTicket(customer, AdtyTicketInput{Title: "Failure", Body: "Investigate", RelatedKind: "request", RelatedRef: log.RequestId})
		require.NoError(t, err)
		require.NoError(t, db.Delete(&log).Error)
		d, err := GetAdtyTicket(customer, tk.Id, "personal")
		require.NoError(t, err)
		require.Equal(t, "chat", d.Ticket.RelatedModel)
		require.Equal(t, "default", d.Ticket.RelatedGroup)
		require.Equal(t, "rate_limit", d.Ticket.RelatedError)
		require.EqualValues(t, 123, d.Ticket.RelatedAt)
	})
	t.Run("revoked assignee returns to pending and permits a new claim", func(t *testing.T) {
		tk, err := CreateAdtyTicket(customer, AdtyTicketInput{Title: "Evidence", Body: "Request"})
		require.NoError(t, err)
		d, err := GetAdtyTicket(owner, tk.Id, "owner")
		require.NoError(t, err)
		require.Contains(t, d.Actions, "assign")
		require.NotContains(t, d.Actions, "in_progress")
		require.NoError(t, SetAdtyTicketStatus(owner, tk.Id, "", "", staff, "owner"))
		require.NoError(t, SetAdtyTicketStatus(owner, tk.Id, "waiting_user", "", 0, "owner"))
		member := db.Model(&model.AdtySiteStaff{}).Where("user_id=?", staff)
		require.NoError(t, member.Update("status", "disabled").Error)
		defer member.Update("status", "enabled")
		_, err = AttachAdtyTicketImageWithKey(customer, tk.Id, 0, "public", img.Bytes(), "evidence", "personal")
		require.NoError(t, err)
		d, err = GetAdtyTicket(owner, tk.Id, "owner")
		require.NoError(t, err)
		require.Equal(t, model.AdtyTicketPendingAssignment, d.Ticket.Status)
		require.Zero(t, d.Ticket.AssigneeId)
		require.NoError(t, SetAdtyTicketStatus(owner, tk.Id, "", "", owner, "owner"))
	})
	t.Run("business hints only list authorized open issues", func(t *testing.T) {
		ref := "order-for-open-cases"
		order := model.AdtyTopupOrder{UserId: customer, TradeNo: ref}
		require.NoError(t, db.Create(&order).Error)
		input := AdtyTicketInput{Title: "First question", Body: "Help", RelatedKind: "topup", RelatedRef: ref}
		first, err := CreateAdtyTicket(customer, input)
		require.NoError(t, err)
		second, err := CreateAdtyTicket(customer, input)
		require.NoError(t, err)
		require.NotEqual(t, first.Id, second.Id)
		require.NoError(t, SetAdtyTicketStatus(owner, first.Id, "close", "", 0, "owner"))
		rows, err := RelatedOpenAdtyTickets(customer, "personal", "topup", ref)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, second.Id, rows[0].Id)
		hidden, err := RelatedOpenAdtyTickets(other, "personal", "topup", ref)
		require.NoError(t, err)
		require.Empty(t, hidden)
	})
	t.Run("keyed reopen replays after preview and a later close", func(t *testing.T) {
		tk, err := CreateAdtyTicket(customer, AdtyTicketInput{Title: "Reopen", Body: "Evidence"})
		require.NoError(t, err)
		im, err := AddAdtyTicketImage(customer, tk.Id, "public", img.Bytes(), "personal")
		require.NoError(t, err)
		require.NoError(t, SetAdtyTicketStatus(owner, tk.Id, "close", "", 0, "owner"))
		var closure model.AdtyTicketEvent
		require.NoError(t, db.Where("ticket_id=? AND action='status'", tk.Id).Last(&closure).Error)
		retry := func() error {
			return SetAdtyTicketStatusWithKey(customer, tk.Id, "reopen", "", 0, "reopen-operation", closure.Id, "personal")
		}
		require.NoError(t, retry())
		_, _, err = OpenAdtyTicketImage(customer, tk.Id, im.Id, "personal")
		require.NoError(t, err)
		require.NoError(t, retry())
		require.NoError(t, SetAdtyTicketStatus(owner, tk.Id, "close", "", 0, "owner"))
		require.NoError(t, retry())
		require.Error(t, SetAdtyTicketStatusWithKey(customer, tk.Id, "reopen", "", 0, "stale-first-attempt", closure.Id, "personal"))
		d, err := GetAdtyTicket(customer, tk.Id, "personal")
		require.NoError(t, err)
		require.Equal(t, "closed", d.Ticket.Status)
		require.Equal(t, []string{"reopen"}, d.Actions)
	})
	t.Run("retention commits before unlink and retries blocked files", func(t *testing.T) {
		tk, err := CreateAdtyTicket(customer, AdtyTicketInput{Title: "Expiry", Body: "Two images"})
		require.NoError(t, err)
		dir := adtyTicketImageDir()
		first := writeRetentionTicketImage(t, dir, tk.Id, "file1.png", []byte("first"))
		second := writeRetentionTicketBlockedImage(t, dir, tk.Id, "blocked")
		require.NoError(t, SetAdtyTicketStatus(owner, tk.Id, "close", "", 0, "owner"))
		ctx := context.Background()
		require.NoError(t, db.Exec("CREATE TABLE retention_hold (ticket_id bigint REFERENCES adty_tickets(id))").Error)
		require.NoError(t, db.Exec("INSERT INTO retention_hold VALUES (?)", tk.Id).Error)
		remove := func() (bool, bool, error) { return deleteOneExpiredSupportTicket(ctx, tk.Id, time.Now().Unix()+2) }
		_, _, err = remove()
		require.Error(t, err)
		require.FileExists(t, filepath.Join(dir, first.Storage))
		require.DirExists(t, filepath.Join(dir, second.Storage))
		require.NoError(t, db.Exec("DROP TABLE retention_hold").Error)
		deleted, _, err := remove()
		require.NoError(t, err)
		require.True(t, deleted)
		n, err := cleanExpiredTicketImages(ctx, 1)
		require.NoError(t, err)
		require.EqualValues(t, 1, n)
		require.NoFileExists(t, filepath.Join(dir, first.Storage))
		require.NoError(t, os.Remove(filepath.Join(dir, second.Storage, "keep")))
		n, err = cleanExpiredTicketImages(ctx, 1)
		require.NoError(t, err)
		require.Zero(t, n)
		var count int64
		require.NoError(t, db.Model(&model.AdtyTicketImage{}).Where("ticket_id=?", tk.Id).Count(&count).Error)
		require.Zero(t, count)
	})
	t.Run("R23 public screenshot reopens the queue once", func(t *testing.T) {
		tk, err := CreateAdtyTicket(customer, AdtyTicketInput{Title: "Screenshot", Body: "Request"})
		require.NoError(t, err)
		_, err = ReplyAdtyTicket(staff, tk.Id, "Please attach a screenshot", "ask", "staff")
		require.NoError(t, err)
		file, err := AttachAdtyTicketImageWithKey(customer, tk.Id, 0, "public", img.Bytes(), "file-op", "personal")
		require.NoError(t, err)
		retried, err := AttachAdtyTicketImageWithKey(customer, tk.Id, 0, "public", img.Bytes(), "file-op", "personal")
		require.NoError(t, err)
		require.Equal(t, file.Id, retried.Id)
		require.NoError(t, err)
		d, err := GetAdtyTicket(customer, tk.Id, "personal")
		require.NoError(t, err)
		require.Equal(t, model.AdtyTicketPendingAssignment, d.Ticket.Status)
		require.NotZero(t, file.Id)
		var n int64
		require.NoError(t, db.Model(&model.AdtyTicketNotice{}).Where("ticket_id=? AND action=? AND user_id=?", tk.Id, "image_upload", owner).Count(&n).Error)
		require.EqualValues(t, 1, n)
	})
	t.Run("R23 retention keeps fixed recipient inbox history", func(t *testing.T) {
		tk, err := CreateAdtyTicket(customer, AdtyTicketInput{Title: "Confidential", Body: "private body"})
		require.NoError(t, err)
		var notice model.AdtyTicketNotice
		require.NoError(t, db.Where("ticket_id=? AND user_id=?", tk.Id, customer).First(&notice).Error)
		require.NoError(t, MarkAdtyNotificationInboxRead(customer, &site, []string{notice.ReadKey}))
		require.NoError(t, SetAdtyTicketStatus(owner, tk.Id, "close", "", 0, "owner"))
		removed, _, err := deleteOneExpiredSupportTicket(context.Background(), tk.Id, time.Now().Unix()+1)
		require.NoError(t, err)
		require.True(t, removed)
		item, ok, err := GetAdtyNotificationDetail(customer, &site, notice.ReadKey)
		require.NoError(t, err)
		require.True(t, ok)
		require.Empty(t, item["ticket_path"])
		require.NotContains(t, item["content"], "private body")
		inbox, err := GetAdtyNotificationInbox(customer, &site, 0)
		require.NoError(t, err)
		require.Contains(t, inbox.ReadKeys, notice.ReadKey)
		require.NoError(t, HideAdtyNotificationForUser(customer, notice.ReadKey))
		_, ok, err = GetAdtyNotificationDetail(customer, &site, notice.ReadKey)
		require.NoError(t, err)
		require.False(t, ok)
	})
	t.Run("R23 historical roles are verified from ticket events", func(t *testing.T) {
		tk, err := CreateAdtyTicket(customer, AdtyTicketInput{Title: "Legacy", Body: "Hi"})
		require.NoError(t, err)
		reply, err := ReplyAdtyTicket(staff, tk.Id, "客服回复", "old", "staff")
		require.NoError(t, err)
		_, err = ReplyAdtyTicket(customer, tk.Id, "客户回复", "answer", "personal")
		require.NoError(t, err)
		_, err = NoteAdtyTicket(staff, tk.Id, "备注", "old-note", "staff")
		require.NoError(t, err)
		require.NoError(t, db.Where("object_key=?", ticketTranslationKey(reply.Id)).Delete(&model.AdtyTranslationHead{}).Error)
		require.NoError(t, db.Where("object_key=?", ticketTranslationKey(reply.Id)).Delete(&model.AdtyTranslationJob{}).Error)
		require.NoError(t, db.Model(&model.AdtyTicketMessage{}).Where("ticket_id=?", tk.Id).Update("author_role", "").Error)
		require.NoError(t, backfillTicketReplyTranslations())
		d, err := GetAdtyTicket(customer, tk.Id, "personal")
		require.NoError(t, err)
		require.Len(t, d.Messages, 3)
		require.Equal(t, "customer", d.Messages[0].AuthorRole)
		require.Equal(t, "support", d.Messages[1].AuthorRole)
		require.Equal(t, model.AdtyTranslationPending, d.Messages[1].TranslationStatus)
		require.Equal(t, "customer", d.Messages[2].AuthorRole)
		d, err = GetAdtyTicket(owner, tk.Id, "owner")
		require.NoError(t, err)
		require.Equal(t, "support", d.Messages[3].AuthorRole)
		unknown := model.AdtyTicketMessage{TicketId: tk.Id, AuthorId: other, Visibility: "public", Body: "历史记录", Dedupe: "unattributed-archive"}
		require.NoError(t, db.Create(&unknown).Error)
		require.NoError(t, backfillTicketReplyTranslations())
		d, err = GetAdtyTicket(customer, tk.Id, "personal")
		require.NoError(t, err)
		require.Equal(t, "unconfirmed", d.Messages[len(d.Messages)-1].AuthorRole)
		require.Empty(t, d.Messages[len(d.Messages)-1].TranslationStatus)
	})
	t.Run("R23 staff opens and hides only a visible receipt", func(t *testing.T) {
		tk, err := CreateAdtyTicket(customer, AdtyTicketInput{Title: "Receipt", Body: "Request"})
		require.NoError(t, err)
		require.NoError(t, SetAdtyTicketStatus(owner, tk.Id, "", "", staff, "owner"))
		var mine, another model.AdtyTicketNotice
		require.NoError(t, db.Where("ticket_id=? AND user_id=?", tk.Id, staff).First(&mine).Error)
		require.NoError(t, db.Where("ticket_id=? AND user_id=?", tk.Id, owner).First(&another).Error)
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set(AdtySiteContextKey, &site)
		ctx.Request = httptest.NewRequest("POST", "/api/adty/notifications/hide", nil)
		_, ok, err := GetAdtyNotificationDetail(staff, &site, mine.ReadKey)
		require.NoError(t, err)
		require.True(t, ok)
		require.Error(t, HideAdtyNotificationForUserFromRequest(ctx, staff, another.ReadKey))
		require.NoError(t, HideAdtyNotificationForUserFromRequest(ctx, staff, mine.ReadKey))
		_, ok, err = GetAdtyNotificationDetail(staff, &site, mine.ReadKey)
		require.NoError(t, err)
		require.False(t, ok)
	})
	t.Run("R23 main support bot bind delivery and permission revocation", func(t *testing.T) {
		t.Setenv("CRYPTO_SECRET", strings.Repeat("t", 32))
		require.NoError(t, CheckAdtyPlatformTicketAccess(admin))
		require.Error(t, CheckAdtyPlatformTicketAccess(mainUser))
		token, err := model.SealAdtyPurposeSecret("telegram:admin", "test-token")
		require.NoError(t, err)
		secret := sha256.Sum256([]byte("test-webhook"))
		bot := model.AdtyTelegramBot{Purpose: "admin", Username: "test_bot", IdentityHash: "test-identity", Enabled: true, TokenCipher: token, WebhookHash: hex.EncodeToString(secret[:])}
		require.NoError(t, db.Create(&bot).Error)
		code, _, _, err := StartAdtyTelegramBind(admin, "admin")
		require.NoError(t, err)
		message := AdtyTelegramMessage{Text: "/start " + code}
		message.Chat.Type = "private"
		message.From.Id = 12345
		_, err = HandleAdtyTelegramUpdate("admin", "test-webhook", AdtyTelegramUpdate{Message: &message})
		require.NoError(t, err)
		_, err = ConfirmAdtyTelegramBind(admin, "admin", code, message.From.Id)
		require.NoError(t, err)
		tk, err := CreateAdtyTicket(mainUser, AdtyTicketInput{Title: "Unassigned", Body: "Request"})
		require.NoError(t, err)
		var notice model.AdtyTicketNotice
		require.NoError(t, db.Where("ticket_id=? AND user_id=?", tk.Id, admin).First(&notice).Error)
		var delivery model.AdtyTelegramDelivery
		require.NoError(t, db.Where("event_key=?", notice.EventKey).First(&delivery).Error)
		require.Equal(t, model.AdtyTelegramDeliveryWait, delivery.Status)
		_, chat, skip, fail := authorizeAndResolveAdtyTelegramDelivery(&delivery)
		require.Equal(t, "12345", chat)
		require.Empty(t, skip)
		require.Empty(t, fail)
		require.NoError(t, db.Exec("ALTER TABLE adty_telegram_bindings RENAME TO temporarily_unavailable_bindings").Error)
		_, _, skip, fail = authorizeAndResolveAdtyTelegramDelivery(&delivery)
		require.Empty(t, skip)
		require.NotEmpty(t, fail)
		require.NoError(t, db.Exec("ALTER TABLE temporarily_unavailable_bindings RENAME TO adty_telegram_bindings").Error)
		_, chat, skip, fail = authorizeAndResolveAdtyTelegramDelivery(&delivery)
		require.Equal(t, "12345", chat)
		require.Empty(t, skip)
		require.Empty(t, fail)
		require.NoError(t, db.Model(&rule).Update("v2", "read").Error)
		defer db.Model(&rule).Update("v2", "manage_all")
		require.Error(t, CheckAdtyPlatformTicketAccess(admin))
		_, chat, skip, fail = authorizeAndResolveAdtyTelegramDelivery(&delivery)
		require.Empty(t, chat)
		require.Equal(t, "business access revoked", skip)
		require.Empty(t, fail)
	})
	require.NoError(t, db.Delete(&rule).Error)
	_, _, err = OpenAdtyTicketImage(admin, first.Id, attachment.Id, "platform")
	require.Error(t, err)
	require.NoError(t, db.Model(&model.AdtySiteStaff{}).Where("user_id = ?", staff).Update("permissions", "[]").Error)
	_, _, err = OpenAdtyTicketImage(staff, first.Id, attachment.Id, "staff")
	require.Error(t, err)
	require.Error(t, SetAdtyTicketStatus(owner, first.Id, "", "", staff, "owner"))

	t.Run("main site inherits handling and the same team retains its history", func(t *testing.T) {
		var readNotice model.AdtyTicketNotice
		require.NoError(t, db.Where("user_id=? AND ticket_id=?", customer, first.Id).First(&readNotice).Error)
		require.NoError(t, MarkAdtyNotificationInboxRead(customer, &site, []string{readNotice.ReadKey}))
		for i := 0; i < 2; i++ {
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return transferSiteTicketsToMainTx(tx, site.Id) }))
		}
		var handoffs int64
		require.NoError(t, db.Model(&model.AdtyTicketNotice{}).Where("action='site_handoff' AND ticket_id=? AND user_id=?", first.Id, root).Count(&handoffs).Error)
		require.EqualValues(t, 1, handoffs)
		require.NoError(t, db.Model(&team).Update("site_id", 0).Error)
		require.NoError(t, db.Model(&model.User{}).Where("id IN ?", []int{leader, customer}).Update("site_id", 0).Error)
		require.NoError(t, db.Model(&site).Update("status", model.AdtySiteStatusClosed).Error)
		inbox, err := GetAdtyNotificationInbox(customer, nil, 0)
		require.NoError(t, err)
		for !slices.Contains(inbox.ReadKeys, readNotice.ReadKey) && inbox.NextTicketBeforeId > 0 {
			inbox, err = GetAdtyNotificationInbox(customer, nil, -1, inbox.NextTicketBeforeId)
			require.NoError(t, err)
		}
		require.Contains(t, inbox.ReadKeys, readNotice.ReadKey)
		require.NoError(t, db.Create(&model.CasbinRule{Ptype: "p", V0: authz.UserSubject(admin), V1: authz.ResourceAdtyTicket, V2: "manage", V3: "allow"}).Error)
		shared, err := ListAdtyTicketPage(leader, "", 1, 20, "team")
		require.NoError(t, err)
		require.NotEmpty(t, shared.Tickets)
		item := shared.Tickets[0]
		require.Equal(t, site.Id, item.SiteId)
		require.Zero(t, item.HandlingSite())
		_, err = GetAdtyTicket(admin, item.Id, "platform")
		require.NoError(t, err)
		_, err = GetAdtyTicket(staff, item.Id, "staff")
		require.Error(t, err)
		_, err = GetAdtyTicket(owner, item.Id, "owner")
		require.Error(t, err)
	})
}
