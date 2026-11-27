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
		`{"7":{"alipay":{"enabled":true,"rate":"0.1"},"wxpay":{"enabled":true,"rate":"0.1"},"usdt":{"enabled":true,"rate":"0.1"}}}`,
	))
	require.NoError(t, adty_setting.ApplyMethodBonusOption(
		adty_setting.TopupSettingKeyMethodBonus,
		`{"alipay":{"enabled":true,"rate":"0.05"},"wxpay":{"enabled":true,"rate":"0.05"},"usdt":{"enabled":true,"rate":"0.05"}}`,
	))

	face := decimal.NewFromInt(100)

	// Active subsite gets configured USDT bonus across USDT chains.
	rate, bonus, err := AdtyPaymentMethodBonus(&model.AdtySite{Id: 7, Status: model.AdtySiteStatusActive}, AdtyMethodUSDTTRC20, face)
	require.NoError(t, err)
	assert.True(t, rate.Equal(decimal.RequireFromString("0.1")))
	assert.True(t, bonus.Equal(decimal.NewFromInt(10)))

	rate, bonus, err = AdtyPaymentMethodBonus(&model.AdtySite{Id: 7, Status: model.AdtySiteStatusActive}, AdtyMethodUSDTERC20, face)
	require.NoError(t, err)
	assert.True(t, rate.Equal(decimal.RequireFromString("0.1")))
	assert.True(t, bonus.Equal(decimal.NewFromInt(10)))

	// Paused subsite gets zero bonus.
	rate, bonus, err = AdtyPaymentMethodBonus(&model.AdtySite{Id: 7, Status: model.AdtySiteStatusPaused}, AdtyMethodUSDTTRC20, face)
	require.NoError(t, err)
	assert.True(t, rate.IsZero())
	assert.True(t, bonus.IsZero())

	// Closing subsite gets zero bonus.
	rate, bonus, err = AdtyPaymentMethodBonus(&model.AdtySite{Id: 7, Status: model.AdtySiteStatusClosing}, AdtyMethodUSDTTRC20, face)
	require.NoError(t, err)
	assert.True(t, rate.IsZero())
	assert.True(t, bonus.IsZero())

	// Main site (site == nil) still gets main-site USDT bonus.
	rate, bonus, err = AdtyPaymentMethodBonus(nil, AdtyMethodUSDTTRC20, face)
	require.NoError(t, err)
	assert.True(t, rate.Equal(decimal.RequireFromString("0.05")))
	assert.True(t, bonus.Equal(decimal.NewFromInt(5)))

	// Legacy Alipay and WeChat Pay rules never produce a bonus on either main site or subsites.
	for _, site := range []*model.AdtySite{nil, {Id: 7, Status: model.AdtySiteStatusActive}} {
		for _, method := range []string{AdtyMethodAlipay, AdtyMethodWxpay} {
			rate, bonus, err = AdtyPaymentMethodBonus(site, method, face)
			require.NoError(t, err)
			assert.True(t, rate.IsZero(), "expected zero rate for %s", method)
			assert.True(t, bonus.IsZero(), "expected zero bonus for %s", method)
		}
	}

	// Active rate projection only ever exposes USDT.
	assert.Equal(t, map[string]string{"usdt": "0.05"}, AdtyActiveMethodBonusRates(0, 0, model.AdtyBusinessWalletTopup))
	assert.Equal(t, map[string]string{"usdt": "0.1"}, AdtyActiveMethodBonusRates(0, 7, model.AdtyBusinessWalletTopup))
}
