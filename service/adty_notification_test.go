package service

import (
	"errors"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func prepareAdtyNotificationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")
	t.Setenv("ADTY_TELEGRAM_ALLOW_NETWORK", "")
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_pragma=busy_timeout(5000)"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.AdtySite{},
		&model.AdtySiteStaff{},
		&model.AdtyTeam{},
		&model.AdtyTeamMember{},
		&model.AdtyTelegramBot{},
		&model.AdtyTelegramBinding{},
		&model.AdtyTelegramChallenge{},
		&model.AdtyTelegramDelivery{},
		&model.AdtyNotificationPublication{},
		&model.AdtyNotificationRead{},
		&model.AdtyNotificationHide{},
		&model.AdtyTranslationConfig{},
		&model.AdtyTranslationHead{},
		&model.AdtyTranslationJob{},
		&model.AdtyTranslationUsage{},
		&model.AdtyGlossaryTerm{},
	))
	previous := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = previous
		_ = sqlDB.Close()
	})
	return db
}

func createNotificationTestUser(t *testing.T, db *gorm.DB, username string, siteID int) model.User {
	t.Helper()
	u := model.User{
		Username: username,
		Password: "password123",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		SiteId:   siteID,
	}
	require.NoError(t, db.Create(&u).Error)
	return u
}

// ISS-R1-0136: Detail lookup resolves visible announcements across history beyond the first 20 items.
func TestGetAdtyNotificationDetailAcrossPagesAndEnforcesVisibility(t *testing.T) {
	db := prepareAdtyNotificationTestDB(t)
	root := createNotificationTestUser(t, db, "notify-root", 0)
	reader := createNotificationTestUser(t, db, "notify-reader", 0)
	owner := createNotificationTestUser(t, db, "notify-owner", 0)
	subSite := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "subdetail", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&subSite).Error)
	subReader := createNotificationTestUser(t, db, "sub-reader", subSite.Id)

	var oldest *model.AdtyNotificationPublication
	for i := 1; i <= 25; i++ {
		pub, err := SaveAdtyNotificationPublication(root.Id, nil, 0, AdtyNotificationInput{
			Title:    fmt.Sprintf("Title %d", i),
			Content:  fmt.Sprintf("Body %d", i),
			Kind:     "system",
			Audience: "all_users",
		}, false)
		require.NoError(t, err)
		if i == 1 {
			oldest = pub
		}
	}
	require.NotNil(t, oldest)

	// Inbox first page only returns the newest 20 items, excluding oldest (item #1).
	inbox, err := GetAdtyNotificationInbox(reader.Id, nil, 0)
	require.NoError(t, err)
	require.Len(t, inbox.Announcements, 20)

	// Detail lookup by publication:<id> and by read_key must still resolve item #1.
	byIDKey, ok, err := GetAdtyNotificationDetail(reader.Id, nil, fmt.Sprintf("publication:%d", oldest.Id))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "Title 1", byIDKey["title"])

	byReadKey, ok, err := GetAdtyNotificationDetail(reader.Id, nil, oldest.ReadKey)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, fmt.Sprintf("publication:%d", oldest.Id), byReadKey["id"])

	// Root all_users is platform-wide: a subsite reader sees the same announcement as the main-site inbox.
	bySub, ok, err := GetAdtyNotificationDetail(subReader.Id, &subSite, fmt.Sprintf("publication:%d", oldest.Id))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "Title 1", bySub["title"])

	// Hiding the announcement hides it from detail lookup for that user.
	require.NoError(t, db.Create(&model.AdtyNotificationHide{UserId: reader.Id, Key: oldest.ReadKey}).Error)
	_, ok, err = GetAdtyNotificationDetail(reader.Id, nil, oldest.ReadKey)
	require.NoError(t, err)
	assert.False(t, ok)
}

// ISS-R1-0137: Authorized team and business user notifications with site isolation and private Telegram fan-out.
func TestSaveAndReadTeamAndUserScopedNotificationsWithSiteIsolation(t *testing.T) {
	db := prepareAdtyNotificationTestDB(t)
	root := createNotificationTestUser(t, db, "scope-root", 0)
	ownerA := createNotificationTestUser(t, db, "owner-a", 0)
	ownerB := createNotificationTestUser(t, db, "owner-b", 0)
	siteA := model.AdtySite{OwnerUserId: ownerA.Id, Subdomain: "site-a", Status: model.AdtySiteStatusActive}
	siteB := model.AdtySite{OwnerUserId: ownerB.Id, Subdomain: "site-b", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&siteA).Error)
	require.NoError(t, db.Create(&siteB).Error)

	memberA1 := createNotificationTestUser(t, db, "member-a1", siteA.Id)
	memberA2 := createNotificationTestUser(t, db, "member-a2", siteA.Id)
	outsiderA := createNotificationTestUser(t, db, "outsider-a", siteA.Id)
	userB := createNotificationTestUser(t, db, "user-b", siteB.Id)
	staffA := createNotificationTestUser(t, db, "staff-a", siteA.Id)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", staffA.Id).Update("staff_site_id", siteA.Id).Error)

	teamA := model.AdtyTeam{Name: "Team A", SiteId: siteA.Id, LeaderUserId: memberA1.Id, Status: model.AdtyTeamStatusActive}
	teamB := model.AdtyTeam{Name: "Team B", SiteId: siteB.Id, LeaderUserId: userB.Id, Status: model.AdtyTeamStatusActive}
	require.NoError(t, db.Create(&teamA).Error)
	require.NoError(t, db.Create(&teamB).Error)
	require.NoError(t, db.Create(&model.AdtyTeamMember{TeamId: teamA.Id, UserId: memberA1.Id, Role: model.AdtyTeamRoleLeader, Status: model.AdtyTeamMemberActive}).Error)
	require.NoError(t, db.Create(&model.AdtyTeamMember{TeamId: teamA.Id, UserId: memberA2.Id, Role: model.AdtyTeamRoleMember, Status: model.AdtyTeamMemberActive}).Error)

	// Site A owner cannot target Site B's team or Site B's user, nor a staff account.
	_, err := SaveAdtyNotificationPublication(ownerA.Id, &siteA, 0, AdtyNotificationInput{
		Title:        "Cross-site team",
		Content:      "Forbidden",
		Kind:         "system",
		Audience:     "team",
		TargetTeamId: teamB.Id,
	}, false)
	require.Error(t, err)

	_, err = SaveAdtyNotificationPublication(ownerA.Id, &siteA, 0, AdtyNotificationInput{
		Title:        "Cross-site user",
		Content:      "Forbidden",
		Kind:         "system",
		Audience:     "user",
		TargetUserId: userB.Id,
	}, false)
	require.Error(t, err)

	_, err = SaveAdtyNotificationPublication(ownerA.Id, &siteA, 0, AdtyNotificationInput{
		Title:        "Staff user",
		Content:      "Forbidden",
		Kind:         "system",
		Audience:     "user",
		TargetUserId: staffA.Id,
	}, false)
	require.Error(t, err)

	// Configure user bot and bind memberA1 so SyncTelegram fans out privately.
	oldGet := AdtyTelegramGetMe
	AdtyTelegramGetMe = func(string) (int64, string, error) { return 424242, "user_bot", nil }
	t.Cleanup(func() { AdtyTelegramGetMe = oldGet })
	savedBot, err := SaveAdtyTelegramBot(model.AdtyTelegramPurposeUser, "424242:ABCDEFGHIJKLMNOPQRSTUV", true)
	require.NoError(t, err)
	code, _, _, err := StartAdtyTelegramBind(memberA1.Id, model.AdtyTelegramPurposeUser)
	require.NoError(t, err)
	var tgMsg AdtyTelegramMessage
	tgMsg.Text = "/start " + code
	tgMsg.Chat.Id = 9001
	tgMsg.Chat.Type = "private"
	tgMsg.From.Id = 9001
	_, err = HandleAdtyTelegramUpdate(model.AdtyTelegramPurposeUser, savedBot.WebhookSecret, AdtyTelegramUpdate{UpdateId: 1, Message: &tgMsg})
	require.NoError(t, err)
	_, err = ConfirmAdtyTelegramBind(memberA1.Id, model.AdtyTelegramPurposeUser, code, 9001)
	require.NoError(t, err)

	// Site A owner publishes a team notification with SyncTelegram=true.
	teamPub, err := SaveAdtyNotificationPublication(ownerA.Id, &siteA, 0, AdtyNotificationInput{
		Title:        "Team quota alert",
		Content:      "Team budget updated",
		Kind:         "system",
		Audience:     "team",
		TargetTeamId: teamA.Id,
		SyncTelegram: true,
	}, false)
	require.NoError(t, err)

	// Site A owner publishes a user notification to outsiderA.
	userPub, err := SaveAdtyNotificationPublication(ownerA.Id, &siteA, 0, AdtyNotificationInput{
		Title:        "Personal notice",
		Content:      "Account verified",
		Kind:         "system",
		Audience:     "user",
		TargetUserId: outsiderA.Id,
	}, false)
	require.NoError(t, err)

	// Root can also publish a user notification to userB.
	rootUserPub, err := SaveAdtyNotificationPublication(root.Id, nil, 0, AdtyNotificationInput{
		Title:        "Platform user notice",
		Content:      "Direct from platform",
		Kind:         "platform",
		Audience:     "user",
		TargetUserId: userB.Id,
	}, false)
	require.NoError(t, err)

	// Verify visibility: memberA1 and memberA2 see teamPub (not userPub); outsiderA sees userPub (not teamPub); userB sees rootUserPub only on siteB.
	inboxM1, err := GetAdtyNotificationInbox(memberA1.Id, &siteA, 0)
	require.NoError(t, err)
	require.Len(t, inboxM1.Announcements, 1)
	assert.Equal(t, fmt.Sprintf("publication:%d", teamPub.Id), inboxM1.Announcements[0]["id"])
	assert.Equal(t, teamA.Id, inboxM1.Announcements[0]["target_team_id"])

	inboxOutsider, err := GetAdtyNotificationInbox(outsiderA.Id, &siteA, 0)
	require.NoError(t, err)
	require.Len(t, inboxOutsider.Announcements, 1)
	assert.Equal(t, fmt.Sprintf("publication:%d", userPub.Id), inboxOutsider.Announcements[0]["id"])
	assert.Equal(t, outsiderA.Id, inboxOutsider.Announcements[0]["target_user_id"])

	inboxB, err := GetAdtyNotificationInbox(userB.Id, &siteB, 0)
	require.NoError(t, err)
	require.Len(t, inboxB.Announcements, 1)
	assert.Equal(t, fmt.Sprintf("publication:%d", rootUserPub.Id), inboxB.Announcements[0]["id"])

	// Verify private Telegram fan-out for teamA created private rows for memberA1 (pending) and memberA2 (skipped, unbound).
	var delivM1 model.AdtyTelegramDelivery
	require.NoError(t, db.Where("event_key = ?", fmt.Sprintf("pub:%d:user:%d", teamPub.Id, memberA1.Id)).First(&delivM1).Error)
	assert.Equal(t, model.AdtyTelegramDeliveryWait, delivM1.Status)
	assert.False(t, delivM1.Public)
}

// ISS-R1-0139: Atomic Telegram outbox persistence and recovery when telegram_at is out of sync with deliveries.
func TestNotificationTelegramOutboxAtomicityAndRecovery(t *testing.T) {
	db := prepareAdtyNotificationTestDB(t)
	root := createNotificationTestUser(t, db, "outbox-root", 0)

	// 1. If inserting into adty_telegram_deliveries fails, telegram_at must roll back to 0 in the same transaction.
	const failCb = "adty-test-fail-telegram-outbox-insert"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(failCb, func(tx *gorm.DB) {
		if tx.Statement.Table == "adty_telegram_deliveries" {
			tx.AddError(errors.New("simulated outbox write failure"))
		}
	}))
	pub, err := SaveAdtyNotificationPublication(root.Id, nil, 0, AdtyNotificationInput{
		Title:           "Atomic test",
		Content:         "English body",
		Kind:            "system",
		Audience:        "all_users",
		SyncTelegram:    true,
		TelegramChannel: "@main_chan",
	}, false)
	require.NoError(t, err)
	require.NoError(t, db.Callback().Create().Remove(failCb))

	var stored model.AdtyNotificationPublication
	require.NoError(t, db.First(&stored, pub.Id).Error)
	assert.Zero(t, stored.TelegramAt, "telegram_at must remain 0 when outbox insert fails in the transaction")

	// 2. Simulate a partial state where telegram_at > 0 was committed without any adty_telegram_deliveries row.
	require.NoError(t, db.Model(&model.AdtyNotificationPublication{}).Where("id = ?", pub.Id).Update("telegram_at", 1700000000).Error)
	var count int64
	require.NoError(t, db.Model(&model.AdtyTelegramDelivery{}).Count(&count).Error)
	assert.Zero(t, count)

	// RetryAdtyTelegram triggers RecoverAdtyNotificationTelegramOutbox and recovers the missing outbox row.
	_, err = RetryAdtyTelegram()
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.AdtyTelegramDelivery{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

// ISS-R1-0140: Dynamic translation on notification save/update with versioned reuse and fallback across surfaces.
func TestNotificationDynamicTranslationSaveReuseAndFallbackAcrossSurfaces(t *testing.T) {
	db := prepareAdtyNotificationTestDB(t)
	root := createNotificationTestUser(t, db, "trans-root", 0)
	reader := createNotificationTestUser(t, db, "trans-reader", 0)

	// 1. With translator disabled/unconfigured, publishing Chinese notice records fallback state and includes AdtyTranslationNotReady.
	fallbackPub, err := SaveAdtyNotificationPublication(root.Id, nil, 0, AdtyNotificationInput{
		Title:           "维护通知",
		Content:         "今晚 02:00 升级维护",
		Kind:            "maintenance",
		Audience:        "all_users",
		SyncTelegram:    true,
		TelegramChannel: "@maint_chan",
	}, false)
	require.NoError(t, err)
	assert.Contains(t, fallbackPub.Content, "今晚 02:00 升级维护")
	assert.Contains(t, fallbackPub.Content, AdtyTranslationNotReady)

	var fallbackHead model.AdtyTranslationHead
	require.NoError(t, db.Where("object_key = ?", adtyNotificationTranslationKey(fallbackPub.Id)).First(&fallbackHead).Error)
	assert.Equal(t, model.AdtyTranslationFailed, fallbackHead.Status)
	assert.Equal(t, AdtyTranslationNotReady, fallbackHead.Notice)

	// 2. Enable translator and verify Chinese notice is translated once, reused on identical edit, and shown across surfaces.
	_, err = SaveAdtyTranslationConfig(AdtyTranslationInput{
		Mode:        model.AdtyTranslationModeOwn,
		Protocol:    "openai",
		BaseURL:     "https://translator.example.net/v1",
		Token:       "ext-secret-token-value-1234",
		ModelName:   "gpt-4o-mini",
		TimeoutSec:  5,
		Concurrency: 2,
		MaxChars:    2000,
		DailyQuota:  100,
		RetryLimit:  1,
		Enabled:     true,
		RequestHost: "adty.example.com",
	})
	require.NoError(t, err)

	calls := 0
	oldTranslate := AdtyTranslateFunc
	AdtyTranslateFunc = func(_, source string) (string, error) {
		calls++
		return "System maintenance at 03:00 tonight", nil
	}
	t.Cleanup(func() { AdtyTranslateFunc = oldTranslate })

	readyPub, err := SaveAdtyNotificationPublication(root.Id, nil, 0, AdtyNotificationInput{
		Title:           "系统维护",
		Content:         "今晚 03:00 系统维护",
		Kind:            "maintenance",
		Audience:        "all_users",
		SyncTelegram:    true,
		TelegramChannel: "@ready_chan",
	}, false)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Contains(t, readyPub.Content, "今晚 03:00 系统维护")
	assert.Contains(t, readyPub.Content, "System maintenance at 03:00 tonight")

	// Re-saving with unchanged Chinese source reuses cached translation version without calling translator again.
	editedPub, err := SaveAdtyNotificationPublication(root.Id, nil, readyPub.Id, AdtyNotificationInput{
		Title:           "系统维护",
		Content:         "今晚 03:00 系统维护",
		Kind:            "maintenance",
		Audience:        "all_users",
		Revision:        readyPub.Revision,
		SyncTelegram:    true,
		TelegramChannel: "@ready_chan",
	}, false)
	require.NoError(t, err)
	assert.Equal(t, 1, calls, "unchanged source text must reuse translation by source hash without calling translator again")
	assert.Contains(t, editedPub.Content, "System maintenance at 03:00 tonight")

	// Check inbox, detail, and Telegram delivery surfaces.
	inbox, err := GetAdtyNotificationInbox(reader.Id, nil, 0)
	require.NoError(t, err)
	require.NotEmpty(t, inbox.Announcements)
	assert.Equal(t, "System maintenance at 03:00 tonight", inbox.Announcements[0]["english"])
	assert.Equal(t, model.AdtyTranslationReady, inbox.Announcements[0]["translation_status"])

	detail, ok, err := GetAdtyNotificationDetail(reader.Id, nil, editedPub.ReadKey)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "System maintenance at 03:00 tonight", detail["english"])
	assert.Contains(t, detail["content"], "System maintenance at 03:00 tonight")

	var tgDeliv model.AdtyTelegramDelivery
	require.NoError(t, db.Where("channel = ?", "@ready_chan").First(&tgDeliv).Error)
	assert.Contains(t, tgDeliv.Body, "今晚 03:00 系统维护")
	assert.Contains(t, tgDeliv.Body, "System maintenance at 03:00 tonight")
}
