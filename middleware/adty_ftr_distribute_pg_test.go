package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ftrDistributeDB(t *testing.T) *gorm.DB {
	t.Helper()
	opened, err := model.OpenAdtyTestDatabase()
	if errors.Is(err, model.ErrAdtyTestDSNUnset) {
		t.Skip("ADTY_TEST_PG_DSN unset")
	}
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, opened.Close())
	})
	pgtest.RecordServerVersion(t, opened.DB)
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())
	oldCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		common.MemoryCacheEnabled = oldCache
	})
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	oldAllow := common.OptionMap["GroupModelAllow"]
	common.OptionMap["GroupModelAllow"] = ""
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap["GroupModelAllow"] = oldAllow
		common.OptionMapRWMutex.Unlock()
	})
	require.NoError(t, opened.DB.AutoMigrate(
		&model.User{},
		&model.Channel{},
		&model.Ability{},
		&model.Model{},
		&model.BillingOperation{},
		&model.AdtyBalanceLedger{},
	))
	return opened.DB
}

func ftrDistributeUser(t *testing.T, db *gorm.DB, name string) model.User {
	t.Helper()
	user := model.User{
		Username: name, Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", SiteId: 0, StaffSiteId: 0,
		Quota: 500000, Email: name + "@example.test", AuthVersion: 1,
	}
	require.NoError(t, db.Create(&user).Error)
	require.Equal(t, common.RoleCommonUser, user.Role)
	require.Zero(t, user.SiteId)
	require.Zero(t, user.StaffSiteId)
	return user
}

func ftrEnabledChannel(t *testing.T, db *gorm.DB, name, modelName string) model.Channel {
	t.Helper()
	channel := model.Channel{
		Type: constant.ChannelTypeOpenAI, Key: "sk-" + name, Status: common.ChannelStatusEnabled,
		Name: name, Models: modelName, Group: "default",
	}
	require.NoError(t, db.Create(&channel).Error)
	priority := int64(0)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: modelName, ChannelId: channel.Id, Enabled: true,
		Priority: &priority, Weight: 10,
	}).Error)
	return channel
}

func ftrDistributeCall(t *testing.T, buyer model.User, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(ctx, constant.ContextKeyUserId, buyer.Id)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "default")
	ctx.Set("role", buyer.Role)
	ctx.Set("username", buyer.Username)
	Distribute()(ctx)
	return ctx, recorder
}

func ftrDistributeAssertNoMoney(t *testing.T, db *gorm.DB, userID int) {
	t.Helper()
	var stored model.User
	require.NoError(t, db.Select("id", "role", "site_id", "staff_site_id", "quota").First(&stored, userID).Error)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	assert.Zero(t, stored.SiteId)
	assert.Zero(t, stored.StaffSiteId)
	assert.Equal(t, 500000, stored.Quota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
	var ops int64
	require.NoError(t, db.Model(&model.BillingOperation{}).Count(&ops).Error)
	assert.Zero(t, ops)
}

// ADTY-06-02: POST /v1/chat/completions parses the body model and keeps the context billing group.
// A body group of vip has no ability, so a successful selection must have used using-group default.
func TestFTRADTY0602ParseModelAndGroupPG(t *testing.T) {
	db := ftrDistributeDB(t)
	buyer := ftrDistributeUser(t, db, "ftr0602")
	channel := ftrEnabledChannel(t, db, "ftr0602-ch", "ftr-0602-parse")

	ctx, recorder := ftrDistributeCall(t, buyer, `{"model":"ftr-0602-parse","group":"vip"}`)

	require.False(t, ctx.IsAborted(), recorder.Body.String())
	require.Equal(t, http.StatusOK, ctx.Writer.Status(), recorder.Body.String())
	assert.Equal(t, "/v1/chat/completions", ctx.Request.URL.Path)
	assert.Equal(t, http.MethodPost, ctx.Request.Method)
	assert.Equal(t, "ftr-0602-parse", ctx.GetString("original_model"))
	assert.Equal(t, "default", common.GetContextKeyString(ctx, constant.ContextKeyUsingGroup))
	assert.Equal(t, channel.Id, common.GetContextKeyInt(ctx, constant.ContextKeyChannelId))
	var vipAbilities int64
	require.NoError(t, db.Model(&model.Ability{}).Where(`"group" = ?`, "vip").Count(&vipAbilities).Error)
	assert.Zero(t, vipAbilities)
	ftrDistributeAssertNoMoney(t, db, buyer.Id)
}

// ADTY-06-03: Distribute selects the enabled default-group channel and leaves the disabled channel unused.
func TestFTRADTY0603SelectEnabledChannelPG(t *testing.T) {
	db := ftrDistributeDB(t)
	buyer := ftrDistributeUser(t, db, "ftr0603")
	enabled := ftrEnabledChannel(t, db, "ftr0603-on", "ftr-0603-route")
	disabled := model.Channel{
		Type: constant.ChannelTypeOpenAI, Key: "sk-ftr0603-off", Status: common.ChannelStatusManuallyDisabled,
		Name: "ftr0603-off", Models: "ftr-0603-route", Group: "default",
	}
	require.NoError(t, db.Create(&disabled).Error)
	require.NotEqual(t, common.ChannelStatusEnabled, disabled.Status)

	ctx, recorder := ftrDistributeCall(t, buyer, `{"model":"ftr-0603-route"}`)

	require.False(t, ctx.IsAborted(), recorder.Body.String())
	require.Equal(t, http.StatusOK, ctx.Writer.Status(), recorder.Body.String())
	assert.Equal(t, "/v1/chat/completions", ctx.Request.URL.Path)
	assert.Equal(t, http.MethodPost, ctx.Request.Method)
	assert.Equal(t, "default", common.GetContextKeyString(ctx, constant.ContextKeyUsingGroup))
	selected := common.GetContextKeyInt(ctx, constant.ContextKeyChannelId)
	assert.Equal(t, enabled.Id, selected)
	assert.NotEqual(t, disabled.Id, selected)
	var stored model.Channel
	require.NoError(t, db.Select("id", "status", "group").First(&stored, selected).Error)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.Equal(t, "default", stored.Group)
	var ability model.Ability
	require.NoError(t, db.Where(`"group" = ? AND model = ? AND channel_id = ?`, "default", "ftr-0603-route", enabled.Id).First(&ability).Error)
	assert.True(t, ability.Enabled)
	var disabledAbilities int64
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", disabled.Id).Count(&disabledAbilities).Error)
	assert.Zero(t, disabledAbilities)
	ftrDistributeAssertNoMoney(t, db, buyer.Id)
}
