package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestSavedTelegramTemplatesDriveReplyAndTestNotice(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")
	t.Setenv("ADTY_TELEGRAM_ALLOW_NETWORK", "")
	oldGet := AdtyTelegramGetMe
	AdtyTelegramGetMe = func(string) (int64, string, error) { return 424242, "admin_bot", nil }
	t.Cleanup(func() { AdtyTelegramGetMe = oldGet })

	db := prepareReferralDialectDB(t, "sqlite")
	root := referralUser(t, db, "tg-template-root")
	require.NoError(t, db.Model(root).Update("role", common.RoleRootUser).Error)

	_, err := SaveAdtyNoticeTemplate(root.Id, common.RoleRootUser, "telegram_bot_reply", "机器人", "请回网站操作", "Bot", "Use the website")
	require.NoError(t, err)
	_, err = SaveAdtyNoticeTemplate(root.Id, common.RoleRootUser, "telegram_bind_staged", "已暂存", "请回网站确认绑定", "Staged", "Confirm the bind on the website")
	require.NoError(t, err)
	_, err = SaveAdtyNoticeTemplate(root.Id, common.RoleRootUser, "telegram_test_notice", "测试标题", "自定义测试通知", "Test title", "Custom test notice")
	require.NoError(t, err)

	saved, err := SaveAdtyTelegramBot(model.AdtyTelegramPurposeAdmin, "424242:ABCDEFGHIJKLMNOPQRSTUV", true)
	require.NoError(t, err)
	var msg AdtyTelegramMessage
	msg.Text = "hello"
	msg.Chat.Type = "private"
	msg.From.Id = 9001
	reply, err := HandleAdtyTelegramUpdate(model.AdtyTelegramPurposeAdmin, saved.WebhookSecret, AdtyTelegramUpdate{UpdateId: 1, Message: &msg})
	require.NoError(t, err)
	require.Equal(t, "请回网站操作\n\nUse the website", reply)

	code, _, _, err := StartAdtyTelegramBind(root.Id, model.AdtyTelegramPurposeAdmin)
	require.NoError(t, err)
	msg.Text = "/start " + code
	msg.From.Username = "root"
	staged, err := HandleAdtyTelegramUpdate(model.AdtyTelegramPurposeAdmin, saved.WebhookSecret, AdtyTelegramUpdate{UpdateId: 2, Message: &msg})
	require.NoError(t, err)
	require.Equal(t, "请回网站确认绑定\n\nConfirm the bind on the website", staged)

	require.NoError(t, TestAdtyTelegramBot(model.AdtyTelegramPurposeAdmin, root.Id))
	var delivery model.AdtyTelegramDelivery
	require.NoError(t, db.Order("id DESC").First(&delivery).Error)
	require.Equal(t, "自定义测试通知\n\nCustom test notice", delivery.Body)
}
