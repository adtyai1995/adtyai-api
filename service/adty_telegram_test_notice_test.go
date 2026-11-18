package service

import (
	"errors"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each bot test is its own delivery. The old fixed event key turned every
// test after the first into a silent duplicate, so the button stopped working.
func TestAdtyTelegramBotTestQueuesANewDeliveryEachTime(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")
	t.Setenv("ADTY_TELEGRAM_ALLOW_NETWORK", "")
	oldGet := AdtyTelegramGetMe
	AdtyTelegramGetMe = func(string) (int64, string, error) { return 424242, "admin_bot", nil }
	t.Cleanup(func() { AdtyTelegramGetMe = oldGet })

	db := prepareReferralDialectDB(t, "sqlite")
	root := referralUser(t, db, "tg-root")
	_, err := SaveAdtyTelegramBot(model.AdtyTelegramPurposeAdmin, "424242:ABCDEFGHIJKLMNOPQRSTUV", true)
	require.NoError(t, err)

	require.NoError(t, TestAdtyTelegramBot(model.AdtyTelegramPurposeAdmin, root.Id))
	require.NoError(t, TestAdtyTelegramBot(model.AdtyTelegramPurposeAdmin, root.Id))

	var deliveries int64
	require.NoError(t, db.Model(&model.AdtyTelegramDelivery{}).
		Where("event_key LIKE ?", "telegram-test:admin:"+strconv.Itoa(root.Id)+":%").Count(&deliveries).Error)
	assert.EqualValues(t, 2, deliveries)
	var bot model.AdtyTelegramBot
	require.NoError(t, db.Where("purpose = ?", model.AdtyTelegramPurposeAdmin).First(&bot).Error)
	assert.NotZero(t, bot.TestedAt)
}

func TestAdtyTelegramBotTestFailsWhenGetMeUnverifiedOrErrors(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")
	t.Setenv("ADTY_TELEGRAM_ALLOW_NETWORK", "")
	oldGet := AdtyTelegramGetMe
	AdtyTelegramGetMe = func(string) (int64, string, error) { return 0, "", ErrAdtyTelegramUnverified }
	t.Cleanup(func() { AdtyTelegramGetMe = oldGet })

	db := prepareReferralDialectDB(t, "sqlite")
	root := referralUser(t, db, "tg-unverified-root")
	_, err := SaveAdtyTelegramBot(model.AdtyTelegramPurposeAdmin, "424242:ABCDEFGHIJKLMNOPQRSTUV", true)
	require.NoError(t, err)

	// ISS-R1-0142: ErrAdtyTelegramUnverified must remain an error, must NOT set tested_at, and must NOT queue a test notice.
	err = TestAdtyTelegramBot(model.AdtyTelegramPurposeAdmin, root.Id)
	require.ErrorIs(t, err, ErrAdtyTelegramUnverified)

	var bot model.AdtyTelegramBot
	require.NoError(t, db.Where("purpose = ?", model.AdtyTelegramPurposeAdmin).First(&bot).Error)
	assert.Zero(t, bot.TestedAt)

	var deliveries int64
	require.NoError(t, db.Model(&model.AdtyTelegramDelivery{}).Count(&deliveries).Error)
	assert.Zero(t, deliveries)

	// Network/API error must also fail without updating tested_at or queueing.
	AdtyTelegramGetMe = func(string) (int64, string, error) { return 0, "", errors.New("getMe 401 unauthorized") }
	err = TestAdtyTelegramBot(model.AdtyTelegramPurposeAdmin, root.Id)
	require.ErrorContains(t, err, "401 unauthorized")
	require.NoError(t, db.Where("purpose = ?", model.AdtyTelegramPurposeAdmin).First(&bot).Error)
	assert.Zero(t, bot.TestedAt)
	require.NoError(t, db.Model(&model.AdtyTelegramDelivery{}).Count(&deliveries).Error)
	assert.Zero(t, deliveries)
}

func TestAdtyTelegramRejectsNonUserAdminPurposesEverywhere(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")
	t.Setenv("ADTY_TELEGRAM_ALLOW_NETWORK", "")
	db := prepareReferralDialectDB(t, "sqlite")
	user := referralUser(t, db, "tg-purpose-user")

	for _, badPurpose := range []string{"site:1", "site:sub", "owner", "root", ""} {
		_, err := SaveAdtyTelegramBot(badPurpose, "424242:ABCDEFGHIJKLMNOPQRSTUV", true)
		assert.ErrorIs(t, err, ErrAdtyTelegramInvalid, "SaveAdtyTelegramBot(%q)", badPurpose)

		err = TestAdtyTelegramBot(badPurpose, user.Id)
		assert.ErrorIs(t, err, ErrAdtyTelegramInvalid, "TestAdtyTelegramBot(%q)", badPurpose)

		_, _, _, err = StartAdtyTelegramBind(user.Id, badPurpose)
		assert.ErrorIs(t, err, ErrAdtyTelegramInvalid, "StartAdtyTelegramBind(%q)", badPurpose)

		_, err = HandleAdtyTelegramUpdate(badPurpose, "sec", AdtyTelegramUpdate{})
		assert.ErrorIs(t, err, ErrAdtyTelegramInvalid, "HandleAdtyTelegramUpdate(%q)", badPurpose)

		_, err = ConfirmAdtyTelegramBind(user.Id, badPurpose, "0123456789abcdef0123456789abcdef", 1001)
		assert.ErrorIs(t, err, ErrAdtyTelegramInvalid, "ConfirmAdtyTelegramBind(%q)", badPurpose)

		_, err = GetAdtyTelegramBind(user.Id, badPurpose)
		assert.ErrorIs(t, err, ErrAdtyTelegramInvalid, "GetAdtyTelegramBind(%q)", badPurpose)

		err = QueueAdtyTelegramNotice(user.Id, badPurpose, "通知\n\nNotice", "evt:"+badPurpose)
		assert.ErrorIs(t, err, ErrAdtyTelegramInvalid, "QueueAdtyTelegramNotice(%q)", badPurpose)

		err = QueueAdtyPublicTelegram("@chan", badPurpose, "通知\n\nNotice")
		assert.ErrorIs(t, err, ErrAdtyTelegramInvalid, "QueueAdtyPublicTelegram(%q)", badPurpose)
	}
}

func TestAdtyTelegramRetryRechecksBotBindingUserSiteRoleTicketAndPreventsReroute(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")
	t.Setenv("ADTY_TELEGRAM_ALLOW_NETWORK", "")
	oldGet := AdtyTelegramGetMe
	AdtyTelegramGetMe = func(token string) (int64, string, error) {
		if token == "424242:ABCDEFGHIJKLMNOPQRSTUV" {
			return 424242, "user_bot", nil
		}
		return 535353, "admin_bot", nil
	}
	t.Cleanup(func() { AdtyTelegramGetMe = oldGet })

	db := prepareReferralDialectDB(t, "sqlite")
	require.NoError(t, db.AutoMigrate(&model.AdtySiteStaff{}, &model.AdtyTicket{}, &model.AdtyTicketMessage{}, &model.AdtyTeam{}, &model.AdtyTeamMember{}))

	owner := referralUser(t, db, "tg-owner")
	site := &model.AdtySite{OwnerUserId: owner.Id, Subdomain: "tgsite", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(site).Error)

	cust := referralUser(t, db, "tg-cust")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", cust.Id).Update("site_id", site.Id).Error)

	staffUser := referralUser(t, db, "tg-staff")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", staffUser.Id).Updates(map[string]any{
		"site_id":       site.Id,
		"staff_site_id": site.Id,
	}).Error)
	staffRec := model.AdtySiteStaff{SiteId: site.Id, UserId: staffUser.Id, Status: "enabled"}
	require.NoError(t, db.Create(&staffRec).Error)

	userSaved, err := SaveAdtyTelegramBot(model.AdtyTelegramPurposeUser, "424242:ABCDEFGHIJKLMNOPQRSTUV", true)
	require.NoError(t, err)
	adminSaved, err := SaveAdtyTelegramBot(model.AdtyTelegramPurposeAdmin, "535353:ABCDEFGHIJKLMNOPQRSTUV", true)
	require.NoError(t, err)

	bindAccount := func(uid int, purpose, secret string, tgID int64) {
		code, _, _, err := StartAdtyTelegramBind(uid, purpose)
		require.NoError(t, err)
		var msg AdtyTelegramMessage
		msg.Text = "/start " + code
		msg.Chat.Id = tgID
		msg.Chat.Type = "private"
		msg.From.Id = tgID
		msg.From.Username = "u"
		reply, err := HandleAdtyTelegramUpdate(purpose, secret, AdtyTelegramUpdate{UpdateId: tgID, Message: &msg})
		require.NoError(t, err)
		require.Equal(t, adtyTelegramStaged, reply)
		_, err = ConfirmAdtyTelegramBind(uid, purpose, code, tgID)
		require.NoError(t, err)
	}

	bindAccount(cust.Id, model.AdtyTelegramPurposeUser, userSaved.WebhookSecret, 7001)
	bindAccount(staffUser.Id, model.AdtyTelegramPurposeAdmin, adminSaved.WebhookSecret, 8001)

	var sentChats []string
	oldSender := AdtyTelegramSender
	AdtyTelegramSender = func(_, chat, _ string) error {
		sentChats = append(sentChats, chat)
		return nil
	}
	t.Cleanup(func() { AdtyTelegramSender = oldSender })

	// 1. Re-binding customer to a new Telegram user ID (7002) must NOT reroute already-queued delivery for 7001.
	require.NoError(t, QueueAdtyTelegramNotice(cust.Id, model.AdtyTelegramPurposeUser, "旧提醒\n\nOld notice", "evt:rebind:1"))
	bindAccount(cust.Id, model.AdtyTelegramPurposeUser, userSaved.WebhookSecret, 7002)
	_, err = RetryAdtyTelegram()
	require.NoError(t, err)
	assert.Empty(t, sentChats, "queued message from before rebind must not be sent to old or new TG ID")
	var rebindRow model.AdtyTelegramDelivery
	require.NoError(t, db.Where("event_key = ?", "evt:rebind:1").First(&rebindRow).Error)
	assert.Equal(t, model.AdtyTelegramDeliverySkip, rebindRow.Status)

	// 2. Disabling bot prevents delivery at send time.
	require.NoError(t, QueueAdtyTelegramNotice(cust.Id, model.AdtyTelegramPurposeUser, "通知2\n\nNotice 2", "evt:bot-off"))
	_, err = SaveAdtyTelegramBot(model.AdtyTelegramPurposeUser, "", false)
	require.NoError(t, err)
	_, err = RetryAdtyTelegram()
	require.NoError(t, err)
	assert.Empty(t, sentChats, "disabled bot must not send queued delivery")
	_, err = SaveAdtyTelegramBot(model.AdtyTelegramPurposeUser, "", true)
	require.NoError(t, err)

	// 3. Disabling user prevents delivery at send time.
	require.NoError(t, QueueAdtyTelegramNotice(cust.Id, model.AdtyTelegramPurposeUser, "通知3\n\nNotice 3", "evt:user-disabled"))
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", cust.Id).Update("status", common.UserStatusDisabled).Error)
	_, err = RetryAdtyTelegram()
	require.NoError(t, err)
	assert.Empty(t, sentChats, "disabled user must not receive queued delivery")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", cust.Id).Update("status", common.UserStatusEnabled).Error)

	// 4. Ticket unassigned/reassigned away from staff or staff revoked prevents admin delivery at send time.
	ticket := model.AdtyTicket{
		SiteId:     site.Id,
		UserId:     cust.Id,
		AssigneeId: staffUser.Id,
		Status:     model.AdtyTicketInProgress,
		Title:      "Help",
	}
	require.NoError(t, db.Create(&ticket).Error)
	msg := model.AdtyTicketMessage{
		TicketId:   ticket.Id,
		AuthorId:   cust.Id,
		Visibility: model.AdtyTicketVisibilityPublic,
		Body:       "Ping",
		Dedupe:     "dedupe-1",
	}
	require.NoError(t, db.Create(&msg).Error)
	ticketKey := "ticket:" + strconv.FormatInt(ticket.Id, 10) + ":msg:" + strconv.FormatInt(msg.Id, 10)
	require.NoError(t, QueueAdtyTelegramNotice(staffUser.Id, model.AdtyTelegramPurposeAdmin, "工单新回复\n\nNew ticket reply", ticketKey))

	// Unassign the ticket before RetryAdtyTelegram runs.
	require.NoError(t, db.Model(&model.AdtyTicket{}).Where("id = ?", ticket.Id).Update("assignee_id", 0).Error)
	_, err = RetryAdtyTelegram()
	require.NoError(t, err)
	assert.Empty(t, sentChats, "unassigned staff must not receive queued ticket notice")
	var ticketDeliv model.AdtyTelegramDelivery
	require.NoError(t, db.Where("event_key = ?", ticketKey).First(&ticketDeliv).Error)
	assert.Equal(t, model.AdtyTelegramDeliverySkip, ticketDeliv.Status)

	// 5. Revoking staff status prevents admin delivery at send time.
	require.NoError(t, QueueAdtyTelegramNotice(staffUser.Id, model.AdtyTelegramPurposeAdmin, "后台提醒\n\nAdmin notice", "evt:staff-revoked"))
	require.NoError(t, db.Model(&model.AdtySiteStaff{}).Where("id = ?", staffRec.Id).Update("status", "disabled").Error)
	_, err = RetryAdtyTelegram()
	require.NoError(t, err)
	assert.Empty(t, sentChats, "revoked staff must not receive queued admin delivery")
}
