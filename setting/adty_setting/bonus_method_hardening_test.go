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
		`{"alipay":{"enabled":true,"rate":"0.00012345"}}`,
	))

	face := decimal.RequireFromString("10.00000001")
	snap, err := LoadBonusPolicySnapshotTx(nil, 0, false, "alipay", face, false)
	require.NoError(t, err)
	assert.True(t, snap.Enabled)
	assert.True(t, snap.FaceUSD.Equal(face), "expected 8-decimal FaceUSD %s, got %s", face, snap.FaceUSD)
	expectedBonus := face.Mul(decimal.RequireFromString("0.00012345")).Round(8)
	assert.True(t, snap.BonusUSD.Equal(expectedBonus), "expected 8-decimal BonusUSD %s, got %s", expectedBonus, snap.BonusUSD)
	assert.Equal(t, "0.0012345", snap.BonusUSD.String())
}
