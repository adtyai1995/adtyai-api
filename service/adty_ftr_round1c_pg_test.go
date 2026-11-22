package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ftrRound1cDB(t *testing.T, extra ...any) *gorm.DB {
	t.Helper()
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.AdtyBalanceLedger{}))
	if len(extra) > 0 {
		require.NoError(t, db.AutoMigrate(extra...))
	}
	return db
}

func ftrRound1cUser(t *testing.T, db *gorm.DB, name string, role int) model.User {
	t.Helper()
	user := model.User{
		Username:    name,
		Password:    "hashed-password",
		Role:        role,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		Email:       name + "@example.test",
		AuthVersion: 1,
		Quota:       500000,
	}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", 500000).Error)
	var stored model.User
	require.NoError(t, db.Select("id", "role", "status", "quota", "site_id", "group").First(&stored, user.Id).Error)
	require.Equal(t, role, stored.Role)
	require.Equal(t, common.UserStatusEnabled, stored.Status)
	require.Equal(t, 500000, stored.Quota)
	user.Role = stored.Role
	user.Status = stored.Status
	user.Quota = stored.Quota
	user.SiteId = stored.SiteId
	user.Group = stored.Group
	return user
}

func ftrRound1cNoMoney(t *testing.T, db *gorm.DB, userID, role int) {
	t.Helper()
	var stored model.User
	require.NoError(t, db.Select("id", "role", "quota").First(&stored, userID).Error)
	assert.Equal(t, role, stored.Role)
	assert.Equal(t, 500000, stored.Quota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

func ftrRound1cTicketDB(t *testing.T) *gorm.DB {
	t.Helper()
	return ftrRound1cDB(t,
		&model.AdtySite{},
		&model.AdtySiteStaff{},
		&model.AdtyTeamMember{},
		&model.AdtyTicket{},
		&model.AdtyTicketMessage{},
		&model.AdtyTicketEvent{},
	)
}

// ADTY-20-04: an active probe sample is stored and listed for the caller's group.
func TestFTRADTY2004RecordProbePG(t *testing.T) {
	db := ftrRound1cDB(t, &model.AdtyGroupHealthSample{}, &model.AdtyGroupHealthState{}, &model.AdtyGroupHealthAlert{})
	user := ftrRound1cUser(t, db, "ftr2004user", common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, user.Role)
	require.Equal(t, 1, user.Role)
	require.Equal(t, 0, user.SiteId)
	require.Equal(t, "default", user.Group)

	now := time.Now()
	require.NoError(t, RecordAdtyGroupProbe(0, "default", "ftr-2004-model", true, now))

	var samples []model.AdtyGroupHealthSample
	require.NoError(t, db.Where("site_id = ? AND group_name = ? AND model_name = ?", 0, "default", "ftr-2004-model").Find(&samples).Error)
	require.NotEmpty(t, samples)
	var probe *model.AdtyGroupHealthSample
	for i := range samples {
		if samples[i].Kind == model.AdtyGroupHealthProbe {
			probe = &samples[i]
			break
		}
	}
	require.NotNil(t, probe)
	assert.Equal(t, 0, probe.SiteId)
	assert.Equal(t, "default", probe.GroupName)
	assert.Equal(t, "ftr-2004-model", probe.ModelName)
	assert.Equal(t, model.AdtyGroupHealthProbe, probe.Kind)
	assert.Equal(t, "probe", probe.Kind)
	assert.Equal(t, model.AdtyGroupHealthSuccess, probe.Outcome)
	assert.Equal(t, "success", probe.Outcome)

	views, err := ListAdtyGroupHealthForUser(user.Id, user.Group, "ftr-2004-model", nil, now)
	require.NoError(t, err)
	var matched *AdtyGroupHealthView
	for i := range views {
		if views[i].Group == "default" {
			matched = &views[i]
			break
		}
	}
	require.NotNil(t, matched)
	assert.Equal(t, "default", matched.Group)
	assert.Greater(t, matched.ProbeSamples, 0)
	ftrRound1cNoMoney(t, db, user.Id, common.RoleCommonUser)
}

// ADTY-20-10: a common-user public reply, a root internal note, and a closed status are stored.
func TestFTRADTY2010TicketReplyNoteStatusPG(t *testing.T) {
	db := ftrRound1cTicketDB(t)
	user := ftrRound1cUser(t, db, "ftr2010user", common.RoleCommonUser)
	root := ftrRound1cUser(t, db, "ftr2010root", common.RoleRootUser)
	require.Equal(t, common.RoleCommonUser, user.Role)
	require.Equal(t, 1, user.Role)
	require.Equal(t, common.RoleRootUser, root.Role)

	created, err := CreateAdtyTicket(user.Id, AdtyTicketInput{
		Type:        "other",
		Title:       "Need a status update",
		Body:        "Opening request for account help",
		Idempotency: "ftr-2010-create",
	})
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, user.Id, created.UserId)
	assert.Equal(t, model.AdtyTicketNew, created.Status)

	const publicBody = "Please keep this public reply intact"
	reply, err := ReplyAdtyTicket(int64(user.Id), created.Id, publicBody, "ftr-2010-reply")
	require.NoError(t, err)
	require.NotNil(t, reply)

	var storedReply model.AdtyTicketMessage
	require.NoError(t, db.Where("ticket_id = ? AND id = ?", created.Id, reply.Id).First(&storedReply).Error)
	assert.Equal(t, user.Id, storedReply.AuthorId)
	assert.Equal(t, model.AdtyTicketVisibilityPublic, storedReply.Visibility)
	assert.Equal(t, "public", storedReply.Visibility)
	assert.Equal(t, publicBody, storedReply.Body)

	const noteBody = "Handler note stays internal"
	note, err := NoteAdtyTicket(root.Id, created.Id, noteBody, "ftr-2010-note")
	require.NoError(t, err)
	require.NotNil(t, note)

	var storedNote model.AdtyTicketMessage
	require.NoError(t, db.Where("ticket_id = ? AND id = ?", created.Id, note.Id).First(&storedNote).Error)
	assert.Equal(t, root.Id, storedNote.AuthorId)
	assert.Equal(t, model.AdtyTicketVisibilityNote, storedNote.Visibility)
	assert.Equal(t, "internal", storedNote.Visibility)
	assert.Equal(t, noteBody, storedNote.Body)
	assert.NotEqual(t, model.AdtyTicketVisibilityPublic, storedNote.Visibility)

	require.NoError(t, SetAdtyTicketStatus(root.Id, created.Id, "close", "", 0))
	var stored model.AdtyTicket
	require.NoError(t, db.First(&stored, created.Id).Error)
	assert.Equal(t, model.AdtyTicketClosed, stored.Status)
	assert.Equal(t, "closed", stored.Status)
	assert.Equal(t, user.Id, stored.UserId)

	ftrRound1cNoMoney(t, db, user.Id, common.RoleCommonUser)
	ftrRound1cNoMoney(t, db, root.Id, common.RoleRootUser)
}

// ADTY-20-09: root assigns a ticket to enabled site staff, then transfers it.
func TestFTRADTY2009AssignTicketPG(t *testing.T) {
	db := ftrRound1cTicketDB(t)
	customer := ftrRound1cUser(t, db, "ftr2009user", common.RoleCommonUser)
	root := ftrRound1cUser(t, db, "ftr2009root", common.RoleRootUser)
	first := ftrRound1cUser(t, db, "ftr2009adm1", common.RoleCommonUser)
	second := ftrRound1cUser(t, db, "ftr2009adm2", common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, customer.Role)
	require.Equal(t, 1, customer.Role)
	require.Equal(t, common.RoleRootUser, root.Role)
	require.Equal(t, common.RoleCommonUser, first.Role)
	require.Equal(t, common.RoleCommonUser, second.Role)
	require.Equal(t, common.UserStatusEnabled, first.Status)
	require.Equal(t, common.UserStatusEnabled, second.Status)

	site := model.AdtySite{OwnerUserId: root.Id, Subdomain: "ftr2009", Status: model.AdtySiteStatusActive, AgreementVer: AdtyCurrentSiteAgreementVersion()}
	require.NoError(t, db.Create(&site).Error)
	require.NotZero(t, site.Id)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", customer.Id).Update("site_id", site.Id).Error)

	for _, staffUser := range []model.User{first, second} {
		require.NoError(t, db.Model(&staffUser).Updates(map[string]any{"site_id": site.Id, "staff_site_id": site.Id}).Error)
		row := model.AdtySiteStaff{
			SiteId: site.Id, OwnerUserId: root.Id, UserId: staffUser.Id, Username: staffUser.Username,
			DisplayName: staffUser.Username, Role: "support", Status: "enabled", Permissions: `["tickets.manage"]`,
		}
		require.NoError(t, db.Create(&row).Error)
		var storedStaff model.AdtySiteStaff
		require.NoError(t, db.Where("user_id = ?", staffUser.Id).First(&storedStaff).Error)
		require.Equal(t, "enabled", storedStaff.Status)
		require.Equal(t, site.Id, storedStaff.SiteId)
	}

	created, err := CreateAdtyTicket(customer.Id, AdtyTicketInput{
		Type:        "account",
		Title:       "Please assign this ticket",
		Body:        "I need a person to handle this",
		Idempotency: "ftr-2009-create",
	})
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, customer.Id, created.UserId)
	assert.Equal(t, site.Id, created.SiteId)
	assert.Zero(t, created.AssigneeId)

	require.NoError(t, SetAdtyTicketStatus(root.Id, created.Id, "", "", first.Id))
	var assigned model.AdtyTicket
	require.NoError(t, db.First(&assigned, created.Id).Error)
	assert.Equal(t, first.Id, assigned.AssigneeId)
	assert.NotEqual(t, customer.Id, assigned.AssigneeId)
	assert.Equal(t, model.AdtyTicketInProgress, assigned.Status)

	require.NoError(t, SetAdtyTicketStatus(root.Id, created.Id, "", "", second.Id))
	var transferred model.AdtyTicket
	require.NoError(t, db.First(&transferred, created.Id).Error)
	assert.Equal(t, second.Id, transferred.AssigneeId)
	assert.NotEqual(t, first.Id, transferred.AssigneeId)
	assert.Equal(t, site.Id, transferred.SiteId)

	ftrRound1cNoMoney(t, db, customer.Id, common.RoleCommonUser)
	ftrRound1cNoMoney(t, db, root.Id, common.RoleRootUser)
	ftrRound1cNoMoney(t, db, first.Id, common.RoleCommonUser)
	ftrRound1cNoMoney(t, db, second.Id, common.RoleCommonUser)
}

// ADTY-19-03: root saves an independent translation endpoint. The token is sealed and not printed.
// rejectAdtyTranslationSelf rejects http and 127.0.0.1, so the stored address is an external https host and is not dialed.
func TestFTRADTY1903SaveOwnTranslationEndpointPG(t *testing.T) {
	const secret = "synthetic-encryption-key-for-local-tests-only"
	previous := common.CryptoSecret
	common.CryptoSecret = secret
	t.Cleanup(func() { common.CryptoSecret = previous })
	t.Setenv("CRYPTO_SECRET", secret)

	db := ftrRound1cDB(t, &model.AdtyTranslationConfig{})
	root := ftrRound1cUser(t, db, "ftr1903root", common.RoleRootUser)
	require.Equal(t, common.RoleRootUser, root.Role)

	protocol, err := adtyTranslationProtocolName("openai")
	require.NoError(t, err)
	require.Equal(t, "openai", protocol)
	const baseURL = "https://ftr-1903.translator.test"
	saved, err := SaveAdtyTranslationConfig(AdtyTranslationInput{
		Mode:        model.AdtyTranslationModeOwn,
		Protocol:    protocol,
		BaseURL:     baseURL,
		ModelName:   "ftr-1903-model",
		Token:       "ftr-1903-token",
		Enabled:     true,
		DailyQuota:  10,
		MaxChars:    1000,
		RetryLimit:  1,
		Concurrency: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, model.AdtyTranslationModeOwn, saved.Mode)
	assert.Equal(t, protocol, saved.Protocol)
	assert.Equal(t, baseURL, saved.BaseURL)
	assert.Equal(t, "ftr-1903-model", saved.ModelName)

	var stored model.AdtyTranslationConfig
	require.NoError(t, db.First(&stored, 1).Error)
	assert.Equal(t, 1, stored.Id)
	assert.Equal(t, model.AdtyTranslationModeOwn, stored.Mode)
	assert.Equal(t, "independent", stored.Mode)
	assert.Equal(t, protocol, stored.Protocol)
	assert.Equal(t, "openai", stored.Protocol)
	assert.Equal(t, baseURL, stored.BaseURL)
	assert.Equal(t, "ftr-1903-model", stored.ModelName)
	assert.NotEmpty(t, stored.TokenCipher)
	assert.True(t, stored.Enabled)
	assert.Equal(t, 10, stored.DailyQuota)
	assert.Equal(t, 1000, stored.MaxChars)
	assert.Equal(t, 1, stored.RetryLimit)
	assert.Equal(t, 1, stored.Concurrency)
	ftrRound1cNoMoney(t, db, root.Id, common.RoleRootUser)
}
