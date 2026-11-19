package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY0510ChannelKeyProtocolMappingAndHealthPG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	previousCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previousCache })
	require.NoError(t, db.AutoMigrate(&User{}, &Channel{}, &Ability{}))

	const quota = 500000
	user := User{Username: "ftr-0510", Password: "hashed-password", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Quota: quota}
	require.NoError(t, db.Create(&user).Error)
	mapping := `{"ftr-0510-model":"upstream-0510"}`
	channel := Channel{
		Name: "ftr-0510", Key: "sk-ftr-0510", Status: common.ChannelStatusEnabled,
		Type: constant.ChannelTypeOpenAI, Models: "ftr-0510-model", ModelMapping: &mapping, Group: "default",
	}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&Ability{Group: "default", Model: "ftr-0510-model", ChannelId: channel.Id, Enabled: true}).Error)

	changed := UpdateChannelStatus(channel.Id, channel.Key, common.ChannelStatusAutoDisabled, "provider rejected key")
	require.True(t, changed)

	var stored Channel
	require.NoError(t, db.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.Status)
	assert.Equal(t, "sk-ftr-0510", stored.Key)
	assert.Equal(t, constant.ChannelTypeOpenAI, stored.Type)
	require.NotNil(t, stored.ModelMapping)
	assert.JSONEq(t, mapping, *stored.ModelMapping)
	assert.Equal(t, "provider rejected key", stored.GetOtherInfo()["status_reason"])

	var ability Ability
	require.NoError(t, db.Where("channel_id = ?", channel.Id).First(&ability).Error)
	assert.False(t, ability.Enabled, "disabling the channel must stop its model from being served")

	var storedQuota int
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Select("quota").Scan(&storedQuota).Error)
	assert.Equal(t, quota, storedQuota)
}
