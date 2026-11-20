package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY1805CompleteTodoPG(t *testing.T) {
	db := ftrRound1PG(t)
	root := ftrRound1User(t, db, "ftr1805root", common.RoleRootUser)
	user := ftrRound1User(t, db, "ftr1805user", common.RoleCommonUser)
	other := ftrRound1User(t, db, "ftr1805other", common.RoleCommonUser)
	require.Equal(t, common.RoleRootUser, root.Role)
	require.Equal(t, common.RoleCommonUser, user.Role)
	require.Equal(t, 0, user.SiteId)
	ftrRound1PinQuota(t, db, root.Id)
	ftrRound1PinQuota(t, db, user.Id)
	ftrRound1PinQuota(t, db, other.Id)

	published, err := SaveAdtyNotificationPublication(root.Id, nil, 0, AdtyNotificationInput{
		Title:    "FTR1805 severe todo",
		Content:  "Complete this severe todo",
		Kind:     "platform",
		Audience: "all_users",
		Business: "severe_todo",
	}, false)
	require.NoError(t, err)
	require.NotNil(t, published)
	require.Equal(t, "published", published.Status)
	require.Equal(t, "severe_todo", published.Business)
	require.NotEmpty(t, published.ReadKey)

	item, err := EnsureAdtySevereTodoForVisiblePublication(user.Id, 0, published)
	require.NoError(t, err)
	require.NotNil(t, item)
	assert.Equal(t, AdtyTodoStatusPending, item.Status)
	assert.Equal(t, user.Id, item.UserId)
	assert.Equal(t, published.ReadKey, item.Key)

	require.NoError(t, CompleteAdtyTodo(user.Id, item.Key))
	require.Error(t, CompleteAdtyTodo(other.Id, item.Key))

	var stored AdtyTodoItem
	require.NoError(t, db.Where("user_id = ? AND key = ?", user.Id, item.Key).First(&stored).Error)
	assert.Equal(t, AdtyTodoStatusCompleted, stored.Status)
	assert.NotZero(t, stored.CompletedAt)
	assert.Equal(t, "manual complete", stored.ResolutionNote)
	assert.Equal(t, published.Id, stored.PublicationId)

	var otherRows int64
	require.NoError(t, db.Model(&AdtyTodoItem{}).Where("user_id = ? AND key = ?", other.Id, item.Key).Count(&otherRows).Error)
	assert.Zero(t, otherRows)

	var history []AdtyTodoStateHistory
	require.NoError(t, db.Where("user_id = ? AND key = ? AND to_status = ?", user.Id, item.Key, AdtyTodoStatusCompleted).Find(&history).Error)
	require.Len(t, history, 1)
	assert.Equal(t, AdtyTodoStatusPending, history[0].FromStatus)
	assert.Equal(t, user.Id, history[0].ActorId)

	var still model.AdtyNotificationPublication
	require.NoError(t, db.First(&still, published.Id).Error)
	assert.Equal(t, "published", still.Status)
	assert.Equal(t, published.ReadKey, still.ReadKey)
	ftrRound1AssertNoMoney(t, db, root.Id)
	ftrRound1AssertNoMoney(t, db, user.Id)
	ftrRound1AssertNoMoney(t, db, other.Id)
}

func TestFTRADTY1907NoticeTemplatePG(t *testing.T) {
	db := ftrRound1PG(t)
	root := ftrRound1User(t, db, "ftr1907root", common.RoleRootUser)
	user := ftrRound1User(t, db, "ftr1907user", common.RoleCommonUser)
	require.Equal(t, common.RoleRootUser, root.Role)
	require.Equal(t, 100, root.Role)
	ftrRound1PinQuota(t, db, root.Id)
	ftrRound1PinQuota(t, db, user.Id)

	const key = "ftr-1907-notice"
	saved, err := SaveAdtyNoticeTemplate(root.Id, root.Role, key, "充值到账", "订单 {{trade_no}} 已到账", "Top-up posted", "Order {{trade_no}} posted")
	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, key, saved.Key)
	assert.Equal(t, int64(1), saved.Revision)
	assert.Equal(t, root.Id, saved.UpdatedBy)

	_, err = SaveAdtyNoticeTemplate(user.Id, user.Role, key, "不能改", "不能改", "Denied", "Denied")
	require.Error(t, err)

	var stored AdtyBilingualNoticeTemplate
	require.NoError(t, db.Where("key = ?", key).First(&stored).Error)
	assert.Equal(t, "充值到账", stored.TitleZh)
	assert.Equal(t, "Top-up posted", stored.TitleEn)
	assert.Equal(t, int64(1), stored.Revision)

	zhTitle, zhBody, err := RenderAdtyNoticeTemplate(key, "zh-CN", map[string]string{"trade_no": "FTR1907"})
	require.NoError(t, err)
	assert.Equal(t, "充值到账", zhTitle)
	assert.Equal(t, "订单 FTR1907 已到账", zhBody)
	enTitle, enBody, err := RenderAdtyNoticeTemplate(key, "en", map[string]string{"trade_no": "FTR1907"})
	require.NoError(t, err)
	assert.Equal(t, "Top-up posted", enTitle)
	assert.Equal(t, "Order FTR1907 posted", enBody)
	ftrRound1AssertNoMoney(t, db, root.Id)
	ftrRound1AssertNoMoney(t, db, user.Id)
}

func TestFTRADTY1908GlossaryTermPG(t *testing.T) {
	db := ftrRound1PG(t)
	root := ftrRound1User(t, db, "ftr1908root", common.RoleRootUser)
	require.Equal(t, common.RoleRootUser, root.Role)
	require.Equal(t, 0, root.SiteId)
	ftrRound1PinQuota(t, db, root.Id)
	require.NoError(t, db.AutoMigrate(&model.AdtyGlossaryTerm{}))

	require.NoError(t, SaveAdtyGlossaryTerm("额度", "en", "quota"))
	require.NoError(t, SaveAdtyGlossaryTerm("额度", "en", "credit quota"))

	var rows []model.AdtyGlossaryTerm
	require.NoError(t, db.Where("source = ? AND locale = ?", "额度", "en").Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, "credit quota", rows[0].Target)
	ftrRound1AssertNoMoney(t, db, root.Id)
}

func TestFTRADTY2002HealthWindowsPG(t *testing.T) {
	db := ftrRound1PG(t)
	user := ftrRound1User(t, db, "ftr2002user", common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, user.Role)
	require.Equal(t, 0, user.SiteId)
	require.Equal(t, "default", user.Group)
	ftrRound1PinQuota(t, db, user.Id)

	now := time.Now().UTC()
	views, err := ListAdtyGroupHealthForUser(user.Id, user.Group, "ftr-2002-model", nil, now)
	require.NoError(t, err)
	matched := ftrRound1dDefaultHealth(t, views)
	assert.Equal(t, "default", matched.Group)
	assert.Equal(t, "platform", matched.Scope)
	starts := AdtyHealthWindowStarts(now)
	require.Len(t, starts, 96)
	require.Len(t, matched.Series, 96)
	assert.Equal(t, starts[0], matched.Series[0].Ts)
	assert.Equal(t, starts[95], matched.Series[95].Ts)
	assert.Equal(t, int64(15*60), matched.Series[1].Ts-matched.Series[0].Ts)
	ftrRound1AssertNoMoney(t, db, user.Id)
}

func TestFTRADTY2006HealthNoSampleAndStalePG(t *testing.T) {
	db := ftrRound1PG(t)
	user := ftrRound1User(t, db, "ftr2006user", common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, user.Role)
	require.Equal(t, 0, user.SiteId)
	ftrRound1PinQuota(t, db, user.Id)

	now := time.Now().UTC()
	assert.Equal(t, "no_sample", HealthSampleStatus(0, 0, now.Unix(), 6*3600))
	views, err := ListAdtyGroupHealthForUser(user.Id, user.Group, "ftr-2006-empty", nil, now)
	require.NoError(t, err)
	empty := ftrRound1dDefaultHealth(t, views)
	assert.Equal(t, model.AdtyGroupHealthNoData, empty.Status)
	assert.Zero(t, empty.RequestSamples)
	assert.Zero(t, empty.ProbeSamples)

	staleAt := now.Add(-7 * time.Hour)
	require.NoError(t, RecordAdtyGroupHealth(0, "default", "ftr-2006-stale", model.AdtyGroupHealthRequest, model.AdtyGroupHealthSuccess, staleAt))
	require.NoError(t, db.Model(&model.AdtyGroupHealthState{}).Where("id = ?", 1).Update("heartbeat_at", now.Unix()).Error)
	assert.Equal(t, "stale", HealthSampleStatus(1, staleAt.Unix(), now.Unix(), 6*3600))

	staleViews, err := ListAdtyGroupHealthForUser(user.Id, user.Group, "ftr-2006-stale", nil, now)
	require.NoError(t, err)
	stale := ftrRound1dDefaultHealth(t, staleViews)
	assert.Equal(t, model.AdtyGroupHealthStale, stale.Status)
	assert.Equal(t, 1, stale.RequestSamples)
	assert.Equal(t, staleAt.Unix(), stale.UpdatedAt)
	ftrRound1AssertNoMoney(t, db, user.Id)
}
