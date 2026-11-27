package adty_setting

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadBonusPolicySnapshotTxUses8DecimalPrecision(t *testing.T) {
	SetAuthoritativeBonusDB(nil)
	t.Cleanup(func() {
		_ = ApplyMethodBonusOption(TopupSettingKeyMethodBonus, "{}")
	})

	// 1.00000005 * 0.01 = 0.0100000005 -> Round(8) = 0.01, while
	// 10 * 0.00012345 = 0.0012345 (7 decimals) -> Round(8) = 0.0012345 (Round(6) would have rounded to 0.001235!).
	require.NoError(t, ApplyMethodBonusOption(
		TopupSettingKeyMethodBonus,
		`{"usdt":{"enabled":true,"rate":"0.00012345"}}`,
	))

	face := decimal.RequireFromString("10.00000001")
	snap, err := LoadBonusPolicySnapshotTx(nil, 0, false, "usdt.trc20", face, false)
	require.NoError(t, err)
	assert.True(t, snap.Enabled)
	assert.Equal(t, "usdt", snap.CanonicalMethod)
	assert.True(t, snap.FaceUSD.Equal(face), "expected 8-decimal FaceUSD %s, got %s", face, snap.FaceUSD)
	expectedBonus := face.Mul(decimal.RequireFromString("0.00012345")).Round(8)
	assert.True(t, snap.BonusUSD.Equal(expectedBonus), "expected 8-decimal BonusUSD %s, got %s", expectedBonus, snap.BonusUSD)
	assert.Equal(t, "0.0012345", snap.BonusUSD.String())
}

func TestRechargeBonusOnlySupportsUSDT(t *testing.T) {
	SetAuthoritativeBonusDB(nil)
	t.Cleanup(func() {
		_ = ApplyMethodBonusOption(TopupSettingKeyMethodBonus, "{}")
		_ = ApplyMethodBonusOption(TopupSettingKeySiteMethodBonus, "{}")
	})

	// Seed legacy Alipay/WeChat rules alongside USDT in both main and subsite options.
	require.NoError(t, ApplyMethodBonusOption(
		TopupSettingKeyMethodBonus,
		`{"alipay":{"enabled":true,"rate":"0.05"},"wxpay":{"enabled":true,"rate":"0.04"},"usdt":{"enabled":true,"rate":"0.03"}}`,
	))
	require.NoError(t, ApplyMethodBonusOption(
		TopupSettingKeySiteMethodBonus,
		`{"7":{"alipay":{"enabled":true,"rate":"0.1"},"wxpay":{"enabled":true,"rate":"0.08"},"usdt":{"enabled":true,"rate":"0.06"}}}`,
	))

	// Read maps only expose USDT.
	mainMap := MainMethodBonusMap()
	assert.Equal(t, map[string]MethodBonusRule{"usdt": {Enabled: true, Rate: "0.03"}}, mainMap)
	siteMap := SiteMethodBonusMap(7)
	assert.Equal(t, map[string]MethodBonusRule{"usdt": {Enabled: true, Rate: "0.06"}}, siteMap)

	// Individual lookups ignore legacy Alipay/WeChat rules.
	_, ok := MainMethodBonus("alipay")
	assert.False(t, ok)
	_, ok = MainMethodBonus("wxpay")
	assert.False(t, ok)
	rule, ok := MainMethodBonus("usdt.erc20")
	assert.True(t, ok)
	assert.Equal(t, MethodBonusRule{Enabled: true, Rate: "0.03"}, rule)

	_, ok = SiteMethodBonus(7, "alipay")
	assert.False(t, ok)
	_, ok = SiteMethodBonus(7, "wxpay")
	assert.False(t, ok)
	siteRule, ok := SiteMethodBonus(7, "usdt.bep20")
	assert.True(t, ok)
	assert.Equal(t, MethodBonusRule{Enabled: true, Rate: "0.06"}, siteRule)

	// Policy snapshots grant zero bonus for Alipay/WeChat on both main site and subsite.
	face := decimal.NewFromInt(100)
	for _, siteID := range []int{0, 7} {
		for _, method := range []string{"alipay", "wxpay"} {
			snap, err := LoadBonusPolicySnapshotTx(nil, siteID, false, method, face, false)
			require.NoError(t, err)
			assert.False(t, snap.Enabled)
			assert.Empty(t, snap.CanonicalMethod)
			assert.True(t, snap.BonusUSD.IsZero())
		}
	}

	// Merge rejects Alipay/WeChat and strips legacy non-USDT keys when updating USDT.
	_, err := MergeMainMethodBonus("{}", "alipay", true, "0.05")
	require.Error(t, err)
	assert.True(t, IsBonusMethodValidationError(err))
	_, err = MergeSiteMethodBonus("{}", 7, "wxpay", true, "0.05")
	require.Error(t, err)
	assert.True(t, IsBonusMethodValidationError(err))

	mergedMain, err := MergeMainMethodBonus(`{"alipay":{"enabled":true,"rate":"0.05"},"usdt":{"enabled":false,"rate":"0"}}`, "usdt", true, "0.025")
	require.NoError(t, err)
	assert.NotContains(t, mergedMain, "alipay")
	assert.Contains(t, mergedMain, `"usdt"`)

	mergedSite, err := MergeSiteMethodBonus(`{"7":{"alipay":{"enabled":true,"rate":"0.1"},"wxpay":{"enabled":true,"rate":"0.08"}}}`, 7, "usdt.trc20", true, "0.04")
	require.NoError(t, err)
	assert.NotContains(t, mergedSite, "alipay")
	assert.NotContains(t, mergedSite, "wxpay")
	assert.Contains(t, mergedSite, `"usdt"`)
}
