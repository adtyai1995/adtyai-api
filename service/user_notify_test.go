package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// A low-balance reminder goes out once, on the charge that takes a wallet or
// subscription balance below the threshold. A balance already below it earns
// nothing more until a top-up lifts it back above the line. The site switch
// and the user's switch both turn reminders off; a user who never chose gets
// them.
func TestQuotaReminderFiresOncePerDrop(t *testing.T) {
	previousEnabled, previousThreshold := common.QuotaRemindEnabled, common.QuotaRemindThreshold
	t.Cleanup(func() {
		common.QuotaRemindEnabled, common.QuotaRemindThreshold = previousEnabled, previousThreshold
	})
	common.QuotaRemindThreshold = 1000
	on, off := true, false
	// changes move the balance from start: negative ones are charges, which
	// may remind; positive ones are top-ups. want holds one entry per charge.
	for _, tc := range []struct {
		name        string
		siteEnabled bool
		setting     dto.UserSetting
		start       int64
		changes     []int64
		want        []bool
	}{
		{
			name:        "never chosen: one reminder per drop, again after a top-up",
			siteEnabled: true,
			start:       1500,
			changes:     []int64{-400, -200, -300, +1500, -1200},
			want:        []bool{false, true, false, true},
		},
		{
			name:        "switched on with its own threshold",
			siteEnabled: true,
			setting:     dto.UserSetting{QuotaWarningEnabled: &on, QuotaWarningThreshold: 5000},
			start:       6000,
			changes:     []int64{-500, -600, -2000},
			want:        []bool{false, true, false},
		},
		{
			name:        "a charge that lands exactly on the threshold does not remind",
			siteEnabled: true,
			start:       1200,
			changes:     []int64{-200, -1},
			want:        []bool{false, true},
		},
		{
			name:        "user switched off",
			siteEnabled: true,
			setting:     dto.UserSetting{QuotaWarningEnabled: &off},
			start:       1500,
			changes:     []int64{-600},
			want:        []bool{false},
		},
		{
			name:    "site switched off",
			setting: dto.UserSetting{QuotaWarningEnabled: &on},
			start:   1500,
			changes: []int64{-600},
			want:    []bool{false},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			common.QuotaRemindEnabled = tc.siteEnabled
			got := make([]bool, 0, len(tc.want))
			balance := tc.start
			for _, change := range tc.changes {
				if change < 0 {
					got = append(got, quotaReminderDue(tc.setting, balance, balance+change))
				}
				balance += change
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// Channel notices go to every enabled administrator whose channel switch is
// on, in that administrator's saved language. Until an administrator chooses,
// root receives them and other administrators do not; other users never do.
func TestChannelNoticesFollowEachAdministratorsSwitch(t *testing.T) {
	previousDB := model.DB
	previousCache, previousRedis := common.MemoryCacheEnabled, common.RedisEnabled
	previousNotifyLimit := constant.NotifyLimitCount
	previousClient, previousWorker := httpClient, system_setting.WorkerUrl
	fetch := system_setting.GetFetchSetting()
	previousFetch := *fetch
	t.Cleanup(func() {
		model.DB = previousDB
		common.MemoryCacheEnabled, common.RedisEnabled = previousCache, previousRedis
		constant.NotifyLimitCount = previousNotifyLimit
		httpClient, system_setting.WorkerUrl = previousClient, previousWorker
		*fetch = previousFetch
	})
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}))
	model.DB = database
	common.MemoryCacheEnabled, common.RedisEnabled = false, false
	constant.NotifyLimitCount = 100

	var mutex sync.Mutex
	received := map[string][]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload WebhookPayload
		if err := common.DecodeJson(r.Body, &payload); err == nil {
			mutex.Lock()
			recipient := strings.TrimPrefix(r.URL.Path, "/")
			received[recipient] = append(received[recipient], payload.Type+" "+payload.Title)
			mutex.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	httpClient, system_setting.WorkerUrl = server.Client(), ""
	fetch.EnableSSRFProtection = false

	on, off := true, false
	for _, user := range []struct {
		name     string
		role     int
		status   int
		receives *bool
		language string
	}{
		{"root-never-chose", common.RoleRootUser, common.UserStatusEnabled, nil, ""},
		{"root-turned-off", common.RoleRootUser, common.UserStatusEnabled, &off, ""},
		{"admin-never-chose", common.RoleAdminUser, common.UserStatusEnabled, nil, ""},
		{"admin-turned-on", common.RoleAdminUser, common.UserStatusEnabled, &on, "zhCN"},
		{"disabled-admin-turned-on", common.RoleAdminUser, common.UserStatusDisabled, &on, ""},
		{"user-with-switch-stored-on", common.RoleCommonUser, common.UserStatusEnabled, &on, ""},
	} {
		setting, err := common.Marshal(dto.UserSetting{
			NotifyType:           dto.NotifyTypeWebhook,
			WebhookUrl:           server.URL + "/" + user.name,
			ChannelNotifyEnabled: user.receives,
			Language:             user.language,
		})
		require.NoError(t, err)
		require.NoError(t, database.Create(&model.User{Username: user.name, Role: user.role, Status: user.status, Setting: string(setting)}).Error)
	}
	channel := &model.Channel{Name: "notice-review", Key: "fixture-key", Type: 1, Status: common.ChannelStatusEnabled, Group: "default", Models: "test-model"}
	require.NoError(t, channel.Insert())

	DisableChannel(types.ChannelError{ChannelId: channel.Id, ChannelName: channel.Name, AutoBan: true}, "upstream failed")
	EnableChannel(channel.Id, "", channel.Name)
	NotifyChannelWatchers(dto.NotifyTypeChannelTest, func(lang string) (string, string) {
		return i18n.Translate(lang, i18n.MsgChannelTestCompletedSubject), i18n.Translate(lang, i18n.MsgChannelTestCompletedContent)
	})

	disabledType := formatNotifyType(channel.Id, common.ChannelStatusAutoDisabled)
	enabledType := formatNotifyType(channel.Id, common.ChannelStatusEnabled)
	assert.Equal(t, map[string][]string{
		"root-never-chose": {
			fmt.Sprintf(`%s Channel "notice-review" (#%d) has been disabled`, disabledType, channel.Id),
			fmt.Sprintf(`%s Channel "notice-review" (#%d) has been enabled`, enabledType, channel.Id),
			"channel_test Channel test completed",
		},
		"admin-turned-on": {
			fmt.Sprintf("%s 通道「notice-review」（#%d）已被禁用", disabledType, channel.Id),
			fmt.Sprintf("%s 通道「notice-review」（#%d）已被启用", enabledType, channel.Id),
			"channel_test 通道测试完成",
		},
	}, received)
	var notice AdtyChannelWatcherNotice
	require.NoError(t, database.Where("notify_type = ? AND language = ?", dto.NotifyTypeChannelTest, "zhCN").First(&notice).Error)
	var outbox AdtyChannelWatcherOutbox
	require.NoError(t, database.Where("event_id = ? AND user_id = ?", notice.EventId, notice.UserId).First(&outbox).Error)
	assert.Equal(t, "通道测试完成", notice.Title)
	assert.Equal(t, "Channel test completed", outbox.EmailTitle)
}

func TestChannelWatcherDurableOutboxIdempotencyRetryAndPermissionCheck(t *testing.T) {
	previousDB := model.DB
	previousCache, previousRedis := common.MemoryCacheEnabled, common.RedisEnabled
	previousNotifyLimit := constant.NotifyLimitCount
	previousClient, previousWorker := httpClient, system_setting.WorkerUrl
	fetch := system_setting.GetFetchSetting()
	previousFetch := *fetch
	t.Cleanup(func() {
		model.DB = previousDB
		common.MemoryCacheEnabled, common.RedisEnabled = previousCache, previousRedis
		constant.NotifyLimitCount = previousNotifyLimit
		httpClient, system_setting.WorkerUrl = previousClient, previousWorker
		*fetch = previousFetch
	})
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}, &model.AdtyNotificationPublication{}))
	model.DB = database
	common.MemoryCacheEnabled, common.RedisEnabled = false, false
	constant.NotifyLimitCount = 100

	var mutex sync.Mutex
	failWebhook := true
	received := map[string][]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		shouldFail := failWebhook
		mutex.Unlock()
		if shouldFail {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		var payload WebhookPayload
		if err := common.DecodeJson(r.Body, &payload); err == nil {
			mutex.Lock()
			recipient := strings.TrimPrefix(r.URL.Path, "/")
			received[recipient] = append(received[recipient], payload.Type+"|"+payload.Title)
			mutex.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	httpClient, system_setting.WorkerUrl = server.Client(), ""
	fetch.EnableSSRFProtection = false

	on, off := true, false
	rootSetting, err := common.Marshal(dto.UserSetting{
		NotifyType:           dto.NotifyTypeWebhook,
		WebhookUrl:           server.URL + "/root",
		ChannelNotifyEnabled: &on,
		Language:             "en",
	})
	require.NoError(t, err)
	adminSetting, err := common.Marshal(dto.UserSetting{
		NotifyType:           dto.NotifyTypeWebhook,
		WebhookUrl:           server.URL + "/admin",
		ChannelNotifyEnabled: &on,
		Language:             "zh-CN",
	})
	require.NoError(t, err)

	rootUser := model.User{Username: "durable-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Setting: string(rootSetting)}
	adminUser := model.User{Username: "durable-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Setting: string(adminSetting)}
	require.NoError(t, database.Create(&rootUser).Error)
	require.NoError(t, database.Create(&adminUser).Error)

	// Root customizes the bilingual template for channel_test; watcher notices consume it.
	_, err = SaveAdtyNoticeTemplate(
		rootUser.Id,
		common.RoleRootUser,
		dto.NotifyTypeChannelTest,
		"[系统模板] 渠道探测异常汇总",
		"渠道健康探测发现异常分组",
		"[System Template] Channel probe summary",
		"Channel health probe detected anomalous groups",
	)
	require.NoError(t, err)

	// Initial dispatch while webhook fails: authoritative in-site notices are persisted and outbox enters "failed".
	sent := NotifyChannelWatchersWithEventID("evt-channel-test-1", dto.NotifyTypeChannelTest, func(lang string) (string, string) {
		return "default-subj", "default-body"
	})
	assert.Equal(t, 0, sent)

	rootNotices, err := ListAdtyChannelWatcherNoticesForUser(rootUser.Id)
	require.NoError(t, err)
	require.Len(t, rootNotices, 1)
	assert.Equal(t, "[System Template] Channel probe summary", rootNotices[0].Title)

	adminNotices, err := ListAdtyChannelWatcherNoticesForUser(adminUser.Id)
	require.NoError(t, err)
	require.Len(t, adminNotices, 1)
	assert.Equal(t, "[系统模板] 渠道探测异常汇总", adminNotices[0].Title)

	var failedRows []AdtyChannelWatcherOutbox
	require.NoError(t, database.Where("event_id = ?", "evt-channel-test-1").Order("user_id ASC").Find(&failedRows).Error)
	require.Len(t, failedRows, 2)
	assert.Equal(t, AdtyWatcherOutboxFailed, failedRows[0].Status)
	assert.Equal(t, AdtyWatcherOutboxFailed, failedRows[1].Status)

	// Before retry runs, adminUser turns off the notification switch -> retry must re-verify live permission/switch and revoke.
	revokedSetting, err := common.Marshal(dto.UserSetting{
		NotifyType:           dto.NotifyTypeWebhook,
		WebhookUrl:           server.URL + "/admin",
		ChannelNotifyEnabled: &off,
		Language:             "zh-CN",
	})
	require.NoError(t, err)
	require.NoError(t, database.Model(&model.User{}).Where("id = ?", adminUser.Id).Update("setting", string(revokedSetting)).Error)

	mutex.Lock()
	failWebhook = false
	mutex.Unlock()

	retried := RetryFailedChannelWatcherNotices()
	assert.Equal(t, 1, retried)
	assert.Equal(t, map[string][]string{
		"root": {"channel_test|[System Template] Channel probe summary"},
	}, received)

	var rootOutbox, adminOutbox AdtyChannelWatcherOutbox
	require.NoError(t, database.Where("event_id = ? AND user_id = ?", "evt-channel-test-1", rootUser.Id).First(&rootOutbox).Error)
	require.NoError(t, database.Where("event_id = ? AND user_id = ?", "evt-channel-test-1", adminUser.Id).First(&adminOutbox).Error)
	assert.Equal(t, AdtyWatcherOutboxDelivered, rootOutbox.Status)
	assert.Equal(t, AdtyWatcherOutboxRevoked, adminOutbox.Status)

	// Duplicate event with the same event_id is idempotent and does not re-send.
	dupSent := NotifyChannelWatchersWithEventID("evt-channel-test-1", dto.NotifyTypeChannelTest, func(lang string) (string, string) {
		return "default-subj", "default-body"
	})
	assert.Equal(t, 0, dupSent)
	assert.Len(t, received["root"], 1)
}

func TestChannelWatcherRetryHandlerIsRegistered(t *testing.T) {
	var found ScheduledSystemTaskHandler
	for _, handler := range registeredSystemTaskHandlers() {
		if handler.Type() != adtyChannelWatcherRetryTaskType {
			continue
		}
		scheduled, ok := handler.(ScheduledSystemTaskHandler)
		require.True(t, ok)
		found = scheduled
	}
	require.NotNil(t, found)
	assert.Equal(t, 60*time.Second, found.Interval())
}

func TestChannelStatusEventIDIsStableAndInboxReadsWatcherNotice(t *testing.T) {
	previousDB := model.DB
	previousCache, previousRedis := common.MemoryCacheEnabled, common.RedisEnabled
	t.Cleanup(func() {
		model.DB = previousDB
		common.MemoryCacheEnabled, common.RedisEnabled = previousCache, previousRedis
	})
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, database.AutoMigrate(
		&model.Channel{}, &model.Ability{}, &model.User{},
		&model.AdtyNotificationPublication{}, &model.AdtyNotificationRead{}, &model.AdtyNotificationHide{},
	))
	model.DB = database
	common.MemoryCacheEnabled, common.RedisEnabled = false, false

	root := model.User{Username: "watcher-inbox-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, database.Create(&root).Error)
	channel := &model.Channel{Name: "stable-status", Key: "fixture-key", Type: 1, Status: common.ChannelStatusEnabled, Group: "default", Models: "test-model"}
	require.NoError(t, channel.Insert())
	require.True(t, model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusAutoDisabled, "upstream failed"))
	eventID := channelStatusEventID(channel.Id, common.ChannelStatusAutoDisabled)
	assert.Equal(t, eventID, channelStatusEventID(channel.Id, common.ChannelStatusAutoDisabled))
	assert.True(t, strings.HasPrefix(eventID, fmt.Sprintf("channel_status:%d:%d:", channel.Id, common.ChannelStatusAutoDisabled)))

	NotifyChannelWatchersWithEventID(eventID, formatNotifyType(channel.Id, common.ChannelStatusAutoDisabled), func(string) (string, string) {
		return "Channel disabled", "Channel disabled body"
	})
	again := NotifyChannelWatchersWithEventID(eventID, formatNotifyType(channel.Id, common.ChannelStatusAutoDisabled), func(string) (string, string) {
		return "Channel disabled", "Channel disabled body"
	})
	assert.Equal(t, 0, again)
	var notices int64
	require.NoError(t, database.Model(&AdtyChannelWatcherNotice{}).Where("user_id = ? AND event_id = ?", root.Id, eventID).Count(&notices).Error)
	assert.EqualValues(t, 1, notices)

	inbox, err := GetAdtyNotificationInbox(root.Id, nil, 0)
	require.NoError(t, err)
	found := false
	var readKey string
	for _, item := range inbox.Announcements {
		if item["event_id"] == eventID {
			found = true
			readKey, _ = item["read_key"].(string)
		}
	}
	require.True(t, found)
	require.NotEmpty(t, readKey)
	detail, ok, err := GetAdtyNotificationDetail(root.Id, nil, readKey)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "Channel disabled", detail["title"])

	require.NoError(t, database.Model(&AdtyChannelWatcherOutbox{}).Where("event_id = ? AND user_id = ?", eventID, root.Id).Updates(map[string]any{
		"status":        AdtyWatcherOutboxFailed,
		"next_retry_at": time.Now().Unix() + 3600,
	}).Error)
	assert.Equal(t, 0, RetryDueChannelWatcherNotices(time.Now().Unix()))
	var still AdtyChannelWatcherOutbox
	require.NoError(t, database.Where("event_id = ? AND user_id = ?", eventID, root.Id).First(&still).Error)
	assert.Equal(t, AdtyWatcherOutboxFailed, still.Status)
}
