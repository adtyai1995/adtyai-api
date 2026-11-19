package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY0504ModelInputOutputPricePG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.RedisEnabled = oldRedis
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	require.NoError(t, db.AutoMigrate(&User{}, &Option{}))

	const quota = 500000
	user := User{Username: "ftr-0504", Password: "hashed-password", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Quota: quota}
	require.NoError(t, db.Create(&user).Error)

	oldPrice := ratio_setting.ModelPrice2JSONString()
	oldCompletion := ratio_setting.CompletionRatio2JSONString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelPriceByJSONString(oldPrice)
		_ = ratio_setting.UpdateCompletionRatioByJSONString(oldCompletion)
	})

	require.NoError(t, UpdateModelPricingOptions(map[string]string{
		"ModelPrice":      `{"ftr-price-model":1.25}`,
		"CompletionRatio": `{"ftr-price-model":2}`,
	}))

	var price Option
	require.NoError(t, db.Where(commonKeyCol+" = ?", "ModelPrice").First(&price).Error)
	assert.Contains(t, price.Value, `"ftr-price-model":1.25`)
	var completion Option
	require.NoError(t, db.Where(commonKeyCol+" = ?", "CompletionRatio").First(&completion).Error)
	assert.Contains(t, completion.Value, `"ftr-price-model":2`)

	err := UpdateModelPricingOptions(map[string]string{"ModelPrice": `{"ftr-price-model":-1}`})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "finite, non-negative number")

	require.NoError(t, db.Where(commonKeyCol+" = ?", "ModelPrice").First(&price).Error)
	assert.Contains(t, price.Value, `"ftr-price-model":1.25`, "a rejected negative price must not replace the saved input price")
	var storedQuota int
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Select("quota").Scan(&storedQuota).Error)
	assert.Equal(t, quota, storedQuota)
}
