package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupModelAllowListRejectsModelsOutsideTheLiveOption(t *testing.T) {
	previous := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() { common.OptionMap = previous })

	assert.True(t, GroupModelAllowed("vip", "gpt-4"))
	common.OptionMap["GroupModelAllow"] = `{"vip":["gpt-4","gpt-4o"]}`
	assert.True(t, GroupModelAllowed("vip", "gpt-4"))
	assert.False(t, GroupModelAllowed("vip", "hidden-model"))
	assert.True(t, GroupModelAllowed("default", "hidden-model"), "a group with no list keeps the live catalog")
	assert.True(t, GroupModelAllowed("auto", "hidden-model"))
}

func TestSiteGroupAuthorizationUsesTheSiteRecord(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site := &model.AdtySite{OwnerUserId: 1, Subdomain: "group-auth", RootDomain: "sites.example.com", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(site).Error)
	assert.True(t, SiteGroupAllowed(site.Id, "vip"))

	require.NoError(t, db.Model(site).Update("allowed_groups", `["default","vip"]`).Error)
	assert.True(t, SiteGroupAllowed(site.Id, "vip"))
	assert.False(t, SiteGroupAllowed(site.Id, "svip"))
	assert.True(t, SiteGroupAllowed(0, "svip"))
}

func TestAutoChannelSelectionEnforcesSiteAndGroupModelAllowLists(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, err := model.OfficialPostgresAcceptanceDSN()
	require.NoError(t, err)
	db := prepareReferralDB(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))

	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() { common.OptionMap = previousOptions })

	previousMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = previousMemoryCache })

	previousAutoGroups := setting.AutoGroups2JsonString()
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["vip","default"]`))
	t.Cleanup(func() { _ = setting.UpdateAutoGroupsByJsonString(previousAutoGroups) })

	previousUsable := setting.UserUsableGroups2JSONString()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	t.Cleanup(func() { _ = setting.UpdateUserUsableGroupsByJSONString(previousUsable) })
	previousRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2}`))
	t.Cleanup(func() { _ = ratio_setting.UpdateGroupRatioByJSONString(previousRatios) })

	vipCh := &model.Channel{
		Name:   "vip-ch",
		Type:   constant.ChannelTypeOpenAI,
		Key:    "sk-vip",
		Status: common.ChannelStatusEnabled,
		Models: "gpt-4o,vip-blocked-model",
		Group:  "vip",
	}
	defaultCh := &model.Channel{
		Name:   "default-ch",
		Type:   constant.ChannelTypeOpenAI,
		Key:    "sk-default",
		Status: common.ChannelStatusEnabled,
		Models: "gpt-4o,vip-blocked-model",
		Group:  "default",
	}
	require.NoError(t, db.Create(vipCh).Error)
	require.NoError(t, db.Create(defaultCh).Error)
	require.NoError(t, vipCh.AddAbilities(nil))
	require.NoError(t, defaultCh.AddAbilities(nil))
	require.NoError(t, model.InitChannelCache())

	site := &model.AdtySite{
		OwnerUserId:   1,
		Subdomain:     "auto-auth",
		RootDomain:    "sites.example.com",
		Status:        model.AdtySiteStatusActive,
		AllowedGroups: `["default"]`,
	}
	require.NoError(t, db.Create(site).Error)
	InvalidateAdtySiteCache()

	newSelectionContext := func(siteID int, group string) *gin.Context {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		(&model.UserBase{Group: "default", SiteId: siteID}).WriteContext(c)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
		common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
		return c
	}
	newAutoContext := func(siteID int) *gin.Context { return newSelectionContext(siteID, "auto") }

	assert.False(t, SelectedBillingGroupAllowed(newAutoContext(site.Id), "auto", "gpt-4o"))
	assert.False(t, SelectedBillingGroupAllowed(newAutoContext(site.Id), "", "gpt-4o"))
	assert.False(t, SelectedBillingGroupAllowed(newAutoContext(site.Id), "vip", "gpt-4o"))
	assert.True(t, SelectedBillingGroupAllowed(newAutoContext(site.Id), "default", "gpt-4o"))

	main := newSelectionContext(0, "vip")
	ch, selectedGroup, selectErr := SelectChannelForRequest(main, "gpt-4o", &RetryParam{
		Ctx: main, TokenGroup: "vip", ModelName: "gpt-4o", Retry: common.GetPointer(0),
	})
	require.Nil(t, selectErr)
	require.NotNil(t, ch)
	assert.Equal(t, "vip", selectedGroup)
	assert.Equal(t, vipCh.Id, ch.Id)

	fixed := newSelectionContext(site.Id, "vip")
	ch, _, selectErr = SelectChannelForRequest(fixed, "gpt-4o", &RetryParam{
		Ctx: fixed, TokenGroup: "vip", ModelName: "gpt-4o", Retry: common.GetPointer(0),
	})
	assert.Nil(t, ch)
	if assert.NotNil(t, selectErr) {
		assert.True(t, selectErr.NoAvailableChannel)
	}

	// 1. Normal Auto selection skips site-disallowed group "vip" and selects "default".
	cSite := newAutoContext(site.Id)
	ch, selectedGroup, selectErr = SelectChannelForRequest(cSite, "gpt-4o", &RetryParam{
		Ctx:        cSite,
		TokenGroup: "auto",
		ModelName:  "gpt-4o",
		Retry:      common.GetPointer(0),
	})
	require.Nil(t, selectErr)
	require.NotNil(t, ch)
	assert.Equal(t, "default", selectedGroup)
	assert.Equal(t, defaultCh.Id, ch.Id)
	assert.Equal(t, "default", common.GetContextKeyString(cSite, constant.ContextKeyAutoGroup))

	// 2. Normal Auto selection skips group whose GroupModelAllow blocks the requested model.
	common.OptionMap["GroupModelAllow"] = `{"vip":["gpt-4o"],"default":["gpt-4o","vip-blocked-model"]}`
	cModel := newAutoContext(0)
	ch, selectedGroup, selectErr = SelectChannelForRequest(cModel, "vip-blocked-model", &RetryParam{
		Ctx:        cModel,
		TokenGroup: "auto",
		ModelName:  "vip-blocked-model",
		Retry:      common.GetPointer(0),
	})
	require.Nil(t, selectErr)
	require.NotNil(t, ch)
	assert.Equal(t, "default", selectedGroup)
	assert.Equal(t, defaultCh.Id, ch.Id)

	// 3. When no auto candidate satisfies both site and model allow-lists, selection fails closed.
	common.OptionMap["GroupModelAllow"] = `{"vip":["gpt-4o"],"default":["gpt-4o"]}`
	cBlocked := newAutoContext(site.Id)
	ch, _, selectErr = SelectChannelForRequest(cBlocked, "vip-blocked-model", &RetryParam{
		Ctx:        cBlocked,
		TokenGroup: "auto",
		ModelName:  "vip-blocked-model",
		Retry:      common.GetPointer(0),
	})
	assert.Nil(t, ch)
	if assert.NotNil(t, selectErr) {
		assert.True(t, selectErr.NoAvailableChannel)
	}

	// 4. Session-affinity Auto path also enforces both site and model allow-lists on the resolved group.
	common.OptionMap["GroupModelAllow"] = `{"vip":["gpt-4o"],"default":["gpt-4o","vip-blocked-model"]}`
	affinitySetting := operation_setting.GetChannelAffinitySetting()
	previousAffinity := *affinitySetting
	t.Cleanup(func() { *affinitySetting = previousAffinity })
	affinitySetting.Enabled = true
	affinitySetting.DefaultTTLSeconds = 600
	affinitySetting.Rules = []operation_setting.ChannelAffinityRule{
		{
			Name:       "group-scope-regression",
			ModelRegex: []string{".*"},
			KeySources: []operation_setting.ChannelAffinityKeySource{
				{Type: "context_int", Key: "affinity_session_id"},
			},
			TTLSeconds: 600,
		},
	}

	// Seed affinity cache pointing at vipCh for session 101.
	cSeed := newAutoContext(0)
	cSeed.Set("affinity_session_id", 101)
	_, _ = GetPreferredChannelByAffinity(cSeed, "gpt-4o", "auto")
	cSeed.Set("channel_id", vipCh.Id)
	RecordChannelAffinity(cSeed, vipCh.Id)
	t.Cleanup(func() { ClearCurrentChannelAffinityCache(cSeed) })
	bound, ok := GetPreferredChannelByAffinity(cSeed, "gpt-4o", "auto")
	require.True(t, ok)
	require.Equal(t, vipCh.Id, bound)

	// On a site that only allows "default", affinity must NOT select vipCh under group "vip";
	// it must fall back to an allowed group ("default").
	cAffinitySite := newAutoContext(site.Id)
	cAffinitySite.Set("affinity_session_id", 101)
	ch, selectedGroup, selectErr = SelectChannelForRequest(cAffinitySite, "gpt-4o", &RetryParam{
		Ctx:        cAffinitySite,
		TokenGroup: "auto",
		ModelName:  "gpt-4o",
		Retry:      common.GetPointer(0),
	})
	require.Nil(t, selectErr)
	require.NotNil(t, ch)
	assert.Equal(t, "default", selectedGroup)
	assert.Equal(t, defaultCh.Id, ch.Id)

	// Seed affinity cache pointing at vipCh for model "vip-blocked-model" on session 102.
	cSeedModel := newAutoContext(0)
	cSeedModel.Set("affinity_session_id", 102)
	_, _ = GetPreferredChannelByAffinity(cSeedModel, "vip-blocked-model", "auto")
	cSeedModel.Set("channel_id", vipCh.Id)
	RecordChannelAffinity(cSeedModel, vipCh.Id)
	t.Cleanup(func() { ClearCurrentChannelAffinityCache(cSeedModel) })
	bound, ok = GetPreferredChannelByAffinity(cSeedModel, "vip-blocked-model", "auto")
	require.True(t, ok)
	require.Equal(t, vipCh.Id, bound)

	// Because "vip" does not allow "vip-blocked-model" in GroupModelAllow, affinity must skip "vip"
	// and fall back to "default".
	cAffinityModel := newAutoContext(0)
	cAffinityModel.Set("affinity_session_id", 102)
	ch, selectedGroup, selectErr = SelectChannelForRequest(cAffinityModel, "vip-blocked-model", &RetryParam{
		Ctx:        cAffinityModel,
		TokenGroup: "auto",
		ModelName:  "vip-blocked-model",
		Retry:      common.GetPointer(0),
	})
	require.Nil(t, selectErr)
	require.NotNil(t, ch)
	assert.Equal(t, "default", selectedGroup)
	assert.Equal(t, defaultCh.Id, ch.Id)

	for _, tc := range []struct {
		name, group, model string
		siteID, channelID  int
		wantGroup          string
	}{
		{name: "pin rejects site exclusion", group: "vip", model: "gpt-4o", siteID: site.Id, channelID: vipCh.Id},
		{name: "pin rejects model exclusion", group: "vip", model: "vip-blocked-model", channelID: vipCh.Id},
		{name: "auto pin rejects excluded concrete group", group: "auto", model: "gpt-4o", siteID: site.Id, channelID: vipCh.Id},
		{name: "auto pin resolves allowed concrete group", group: "auto", model: "gpt-4o", siteID: site.Id, channelID: defaultCh.Id, wantGroup: "default"},
		{name: "main site pin stays available", group: "vip", model: "gpt-4o", channelID: vipCh.Id, wantGroup: "vip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newSelectionContext(tc.siteID, tc.group)
			GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: tc.channelID, Source: dto.PinSourceToken, Rank: dto.PinRankToken})
			selected, group, selectionErr := SelectChannelForRequest(c, tc.model, &RetryParam{Ctx: c, TokenGroup: tc.group, ModelName: tc.model})
			if tc.wantGroup == "" {
				assert.Nil(t, selected)
				require.NotNil(t, selectionErr)
				assert.Equal(t, http.StatusForbidden, selectionErr.StatusCode)
				return
			}
			require.Nil(t, selectionErr)
			require.NotNil(t, selected)
			assert.Equal(t, tc.channelID, selected.Id)
			assert.Equal(t, tc.wantGroup, group)
		})
	}

	t.Run("retry rechecks current scope", func(t *testing.T) {
		c := newAutoContext(0)
		params := &RetryParam{Ctx: c, TokenGroup: "auto", ModelName: "gpt-4o", Retry: common.GetPointer(0)}
		selected, group, err := CacheGetRandomSatisfiedChannel(params)
		require.NoError(t, err)
		require.NotNil(t, selected)
		require.Equal(t, "vip", group)
		common.SetContextKey(c, constant.ContextKeyUserSiteId, site.Id)
		params.IncreaseRetry()
		selected, group, err = CacheGetRandomSatisfiedChannel(params)
		require.NoError(t, err)
		require.NotNil(t, selected)
		assert.Equal(t, "default", group)
		assert.Equal(t, defaultCh.Id, selected.Id)
	})
}
