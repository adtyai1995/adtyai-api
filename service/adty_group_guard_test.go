package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAdtyGroupOptionGroupModelAllowSchemaAndPreservation(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	require.NoError(t, db.AutoMigrate(&model.AdtyGroup{}, &model.Option{}, &model.AuditLog{}, &model.AuditIntent{}))
	require.NoError(t, db.Unscoped().Where("1 = 1").Delete(&model.AdtyGroup{}).Error)
	for _, id := range []string{"default", "gExisting", "other"} {
		require.NoError(t, db.Create(&model.AdtyGroup{
			ID:    id,
			Names: model.AdtyLangText{"en": id},
			Descs: model.AdtyLangText{},
		}).Error)
	}

	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{
		"GroupModelAllow": `{"gExisting":["gpt-4o"]}`,
	}
	t.Cleanup(func() { common.OptionMap = previousOptions })
	require.NoError(t, db.Create(&model.Option{
		Key:   "GroupModelAllow",
		Value: `{"gExisting":["gpt-4o"]}`,
	}).Error)

	// Valid active group list and explicit clear semantics succeed.
	require.NoError(t, ValidateAdtyGroupOption("GroupModelAllow", `{"gExisting":["gpt-4","gpt-4o"],"other":["claude-3"]}`))
	require.NoError(t, ValidateAdtyGroupOption("GroupModelAllow", `{}`))
	require.NoError(t, ValidateAdtyGroupOption("GroupModelAllow", ``))

	// Malformed structures and unknown/auto groups are rejected before write.
	for _, bad := range []string{
		`{"gExisting":"bad"}`,
		`{"gExisting":123}`,
		`{"gExisting":null}`,
		`{"gExisting":{}}`,
		`{"gExisting":[123]}`,
		`{"gExisting":[""]}`,
		`{"gExisting":["   "]}`,
		`["gExisting"]`,
		`null`,
		`not-json`,
		`{"auto":["gpt-4"]}`,
		`{"unknown":["gpt-4"]}`,
	} {
		require.Error(t, ValidateAdtyGroupOption("GroupModelAllow", bad), "expected ValidateAdtyGroupOption to reject %s", bad)
		require.Error(t, model.UpdateOption("GroupModelAllow", bad), "expected UpdateOption to reject %s", bad)
	}

	// Prior working option remains intact in memory and DB, so other groups are not denied.
	assert.JSONEq(t, `{"gExisting":["gpt-4o"]}`, common.OptionMap["GroupModelAllow"])
	var stored model.Option
	require.NoError(t, db.Where("key = ?", "GroupModelAllow").First(&stored).Error)
	assert.JSONEq(t, `{"gExisting":["gpt-4o"]}`, stored.Value)
	assert.True(t, GroupModelAllowed("gExisting", "gpt-4o"))
	assert.False(t, GroupModelAllowed("gExisting", "gpt-3.5"))
	assert.True(t, GroupModelAllowed("other", "gpt-3.5"))

	// If runtime state is manually corrupted, GroupModelAllowed remains fail-closed.
	common.OptionMap["GroupModelAllow"] = `{"gExisting":"bad"}`
	assert.False(t, GroupModelAllowed("gExisting", "gpt-4o"))
	assert.False(t, GroupModelAllowed("other", "gpt-4o"))
}

func TestMigrateAdtyGroupUpdatesSiteAllowedGroupsAndGMACache(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	require.NoError(t, db.AutoMigrate(&model.AdtyGroup{}, &model.AdtySite{}, &model.Option{}, &model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}))
	require.NoError(t, db.Unscoped().Where("1 = 1").Delete(&model.AdtyGroup{}).Error)
	for _, id := range []string{"default", "gFrom", "gTo"} {
		require.NoError(t, db.Create(&model.AdtyGroup{
			ID:    id,
			Names: model.AdtyLangText{"en": id},
			Descs: model.AdtyLangText{},
		}).Error)
	}

	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{
		"GroupModelAllow": `{"gFrom":["gpt-4"]}`,
	}
	t.Cleanup(func() { common.OptionMap = previousOptions })
	require.NoError(t, db.Create(&model.Option{
		Key:   "GroupModelAllow",
		Value: `{"gFrom":["gpt-4"]}`,
	}).Error)

	site := &model.AdtySite{
		OwnerUserId:   1,
		Subdomain:     "mig-site",
		RootDomain:    "sites.example.com",
		Status:        model.AdtySiteStatusActive,
		AllowedGroups: `["gFrom"]`,
	}
	require.NoError(t, db.Create(site).Error)
	InvalidateAdtySiteCache()
	require.True(t, SiteGroupAllowed(site.Id, "gFrom"))
	require.False(t, SiteGroupAllowed(site.Id, "gTo"))

	moved, err := model.MigrateAdtyGroup("gFrom", "gTo")
	require.NoError(t, err)
	assert.Equal(t, int64(1), moved.Sites)

	assert.True(t, SiteGroupAllowed(site.Id, "gTo"), "site allow-list cache and DB must permit gTo after migration")
	assert.False(t, SiteGroupAllowed(site.Id, "gFrom"))
	assert.True(t, GroupModelAllowed("gTo", "gpt-4"))
	assert.False(t, GroupModelAllowed("gTo", "other-model"))
}
