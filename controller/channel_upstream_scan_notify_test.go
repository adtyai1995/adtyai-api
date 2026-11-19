package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The scheduled scan is the remaining ISS-R1-0179 call site. Delivery failure
// must leave the channel settings written before notification in place.
func TestRunChannelUpstreamModelUpdateTaskOncePersistsWatcherEvent(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousCache, previousRedis := common.MemoryCacheEnabled, common.RedisEnabled
	previousInterval := common.RequestInterval
	previousLimit := constant.NotifyLimitCount
	previousMaster := common.IsMasterNode
	previousWorker := system_setting.WorkerUrl
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousPermissions := service.AdtyAdminPermissionsEnabled()
	fetch := system_setting.GetFetchSetting()
	previousFetch := *fetch
	channelUpstreamModelUpdateNotifyState.Lock()
	previousNotifiedAt := channelUpstreamModelUpdateNotifyState.lastNotifiedAt
	previousChanged := channelUpstreamModelUpdateNotifyState.lastChangedChannels
	previousFailed := channelUpstreamModelUpdateNotifyState.lastFailedChannels
	channelUpstreamModelUpdateNotifyState.Unlock()
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.MemoryCacheEnabled, common.RedisEnabled = previousCache, previousRedis
		common.RequestInterval = previousInterval
		constant.NotifyLimitCount = previousLimit
		common.IsMasterNode = previousMaster
		system_setting.WorkerUrl = previousWorker
		common.SetDatabaseTypes(previousMain, previousLog)
		model.InitColumnNamesForTests()
		service.SetAdtyAdminPermissionsEnabled(previousPermissions)
		*fetch = previousFetch
		channelUpstreamModelUpdateNotifyState.Lock()
		channelUpstreamModelUpdateNotifyState.lastNotifiedAt = previousNotifiedAt
		channelUpstreamModelUpdateNotifyState.lastChangedChannels = previousChanged
		channelUpstreamModelUpdateNotifyState.lastFailedChannels = previousFailed
		channelUpstreamModelUpdateNotifyState.Unlock()
	})

	common.IsMasterNode = true
	service.SetAdtyAdminPermissionsEnabled(true)
	authzDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	authzSQL, err := authzDB.DB()
	require.NoError(t, err)
	authzSQL.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, authzSQL.Close()) })
	require.NoError(t, authzDB.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}, &model.AuthzActionCatalogState{}))
	require.NoError(t, authz.Init(authzDB))

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
	model.DB, model.LOG_DB = database, database
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	model.InitColumnNamesForTests()
	common.MemoryCacheEnabled, common.RedisEnabled = false, false
	common.RequestInterval = 0
	constant.NotifyLimitCount = 100
	system_setting.WorkerUrl = ""
	fetch.EnableSSRFProtection = true
	fetch.AllowPrivateIp = false
	require.NoError(t, i18n.Init())

	createUser := func(name string, upstreamOn bool) model.User {
		t.Helper()
		setting, err := common.Marshal(dto.UserSetting{
			NotifyType:                       dto.NotifyTypeWebhook,
			WebhookUrl:                       "http://127.0.0.1:9/adty-scan-hook",
			UpstreamModelUpdateNotifyEnabled: upstreamOn,
			Language:                         "zhCN",
		})
		require.NoError(t, err)
		user := model.User{Username: name, Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Setting: string(setting)}
		require.NoError(t, database.Create(&user).Error)
		return user
	}
	keep := createUser("scan-keep", true)
	off := createUser("scan-off", false)
	denied := createUser("scan-denied", true)
	require.True(t, authz.Can(keep.Id, common.RoleAdminUser, authz.ChannelRead))
	require.NoError(t, authz.SetUserPermissions(denied.Id, authz.PermissionsMap{
		authz.ResourceChannel: {authz.ActionRead: false},
	}))
	require.False(t, authz.Can(denied.Id, common.RoleAdminUser, authz.ChannelRead))

	changedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"kept-model"},{"id":"scan-added"}]}`))
	}))
	t.Cleanup(changedServer.Close)
	failedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(failedServer.Close)

	insertChecked := func(name, baseURL, models string) *model.Channel {
		t.Helper()
		channel := &model.Channel{
			Name: name, Type: constant.ChannelTypeOpenAI, Key: "sk-scan",
			Status: common.ChannelStatusEnabled, Group: "default", Models: models, BaseURL: &baseURL,
		}
		channel.SetOtherSettings(dto.ChannelOtherSettings{UpstreamModelUpdateCheckEnabled: true})
		require.NoError(t, channel.Insert())
		return channel
	}
	changed := insertChecked("scan-changed", changedServer.URL, "kept-model")
	failed := insertChecked("scan-failed", failedServer.URL, "kept-model")

	resetNotifyWindow := func() {
		t.Helper()
		channelUpstreamModelUpdateNotifyState.Lock()
		channelUpstreamModelUpdateNotifyState.lastNotifiedAt = 0
		channelUpstreamModelUpdateNotifyState.lastChangedChannels = 0
		channelUpstreamModelUpdateNotifyState.lastFailedChannels = 0
		channelUpstreamModelUpdateNotifyState.Unlock()
	}
	resetNotifyWindow()

	summary, err := runChannelUpstreamModelUpdateTaskOnce(context.Background(), true, false, nil)
	require.NoError(t, err)
	require.Equal(t, 2, summary.CheckedChannels)
	require.Equal(t, 1, summary.ChangedChannels)
	require.Equal(t, 1, summary.FailedChannels)
	require.Equal(t, 1, summary.DetectedAddModels)
	require.Equal(t, 0, summary.AutoAddedModels)

	reload := func(id int) *model.Channel {
		t.Helper()
		row, err := model.GetChannelById(id, true)
		require.NoError(t, err)
		return row
	}
	changedRow := reload(changed.Id)
	require.Equal(t, common.ChannelStatusEnabled, changedRow.Status)
	require.Equal(t, "kept-model", changedRow.Models)
	changedSettings := changedRow.GetOtherSettings()
	require.Greater(t, changedSettings.UpstreamModelUpdateLastCheckTime, int64(0))
	require.Equal(t, []string{"scan-added"}, changedSettings.UpstreamModelUpdateLastDetectedModels)
	failedRow := reload(failed.Id)
	require.Equal(t, common.ChannelStatusEnabled, failedRow.Status)
	require.Equal(t, "kept-model", failedRow.Models)
	require.Greater(t, failedRow.GetOtherSettings().UpstreamModelUpdateLastCheckTime, int64(0))

	noticesFor := func(userID int) []service.AdtyChannelWatcherNotice {
		t.Helper()
		rows, err := service.ListAdtyChannelWatcherNoticesForUser(userID)
		require.NoError(t, err)
		return rows
	}
	keepNotices := noticesFor(keep.Id)
	require.Len(t, keepNotices, 1)
	notice := keepNotices[0]
	require.NotEmpty(t, notice.EventId)
	require.Equal(t, dto.NotifyTypeChannelUpdate, notice.NotifyType)
	require.Equal(t, "model_group_anomaly", notice.Category)
	require.Equal(t, "zhCN", notice.Language)
	require.Equal(t, i18n.Translate("zhCN", i18n.MsgChannelUpstreamUpdateNotifySubject), notice.Title)
	require.Empty(t, noticesFor(off.Id))
	require.Empty(t, noticesFor(denied.Id))

	var outbox service.AdtyChannelWatcherOutbox
	require.NoError(t, database.Where("user_id = ? AND event_id = ?", keep.Id, notice.EventId).First(&outbox).Error)
	require.Equal(t, service.AdtyWatcherOutboxFailed, outbox.Status)
	require.GreaterOrEqual(t, outbox.AttemptCount, 1)
	var otherOutbox int64
	require.NoError(t, database.Model(&service.AdtyChannelWatcherOutbox{}).Where("user_id <> ?", keep.Id).Count(&otherOutbox).Error)
	require.EqualValues(t, 0, otherOutbox)

	inbox, err := service.GetAdtyNotificationInbox(keep.Id, nil, 0)
	require.NoError(t, err)
	var item map[string]any
	for _, candidate := range inbox.Announcements {
		if candidate["event_id"] == notice.EventId {
			item = candidate
			break
		}
	}
	require.NotNil(t, item)
	require.Equal(t, "channel_watcher", item["source"])
	require.Equal(t, notice.EventId, item["event_id"])
	require.Equal(t, dto.NotifyTypeChannelUpdate, item["notify_type"])

	attempts := outbox.AttemptCount
	summary, err = runChannelUpstreamModelUpdateTaskOnce(context.Background(), true, false, nil)
	require.NoError(t, err)
	require.Equal(t, 1, summary.ChangedChannels)
	require.Equal(t, 1, summary.FailedChannels)
	require.Len(t, noticesFor(keep.Id), 1)
	require.Equal(t, notice.EventId, noticesFor(keep.Id)[0].EventId)
	require.Equal(t, i18n.Translate("zhCN", i18n.MsgChannelUpstreamUpdateNotifySubject), noticesFor(keep.Id)[0].Title)
	var retried service.AdtyChannelWatcherOutbox
	require.NoError(t, database.Where("user_id = ? AND event_id = ?", keep.Id, notice.EventId).First(&retried).Error)
	require.Equal(t, attempts, retried.AttemptCount)
	require.Equal(t, "kept-model", reload(changed.Id).Models)
	require.Equal(t, common.ChannelStatusEnabled, reload(failed.Id).Status)
	resetNotifyWindow()
}
