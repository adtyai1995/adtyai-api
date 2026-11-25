package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdtyPaymentMethodBonusRequiresActiveSubsite(t *testing.T) {
	oldDB := model.DB
	oldOptionMap := common.OptionMap
	model.DB = nil
	adty_setting.SetAuthoritativeBonusDB(nil)
	t.Cleanup(func() {
		model.DB = oldDB
		adty_setting.SetAuthoritativeBonusDB(oldDB)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
		_ = adty_setting.ApplyMethodBonusOption(adty_setting.TopupSettingKeySiteMethodBonus, "{}")
		_ = adty_setting.ApplyMethodBonusOption(adty_setting.TopupSettingKeyMethodBonus, "{}")
	})

	require.NoError(t, adty_setting.ApplyMethodBonusOption(
		adty_setting.TopupSettingKeySiteMethodBonus,
		`{"7":{"alipay":{"enabled":true,"rate":"0.1"}}}`,
	))
	require.NoError(t, adty_setting.ApplyMethodBonusOption(
		adty_setting.TopupSettingKeyMethodBonus,
		`{"alipay":{"enabled":true,"rate":"0.05"}}`,
	))

	face := decimal.NewFromInt(100)

	// Active subsite gets configured bonus.
	rate, bonus, err := AdtyPaymentMethodBonus(&model.AdtySite{Id: 7, Status: model.AdtySiteStatusActive}, AdtyMethodAlipay, face)
	require.NoError(t, err)
	assert.True(t, rate.Equal(decimal.RequireFromString("0.1")))
	assert.True(t, bonus.Equal(decimal.NewFromInt(10)))

	// Paused subsite gets zero bonus.
	rate, bonus, err = AdtyPaymentMethodBonus(&model.AdtySite{Id: 7, Status: model.AdtySiteStatusPaused}, AdtyMethodAlipay, face)
	require.NoError(t, err)
	assert.True(t, rate.IsZero())
	assert.True(t, bonus.IsZero())

	// Closing subsite gets zero bonus.
	rate, bonus, err = AdtyPaymentMethodBonus(&model.AdtySite{Id: 7, Status: model.AdtySiteStatusClosing}, AdtyMethodAlipay, face)
	require.NoError(t, err)
	assert.True(t, rate.IsZero())
	assert.True(t, bonus.IsZero())

	// Main site (site == nil) still gets main-site bonus.
	rate, bonus, err = AdtyPaymentMethodBonus(nil, AdtyMethodAlipay, face)
	require.NoError(t, err)
	assert.True(t, rate.Equal(decimal.RequireFromString("0.05")))
	assert.True(t, bonus.Equal(decimal.NewFromInt(5)))
}
