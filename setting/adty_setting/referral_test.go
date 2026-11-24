package adty_setting

import (
	"slices"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func floatPtr(v float64) *float64 { return &v }

func TestNormalizeReferralWithdrawMethods(t *testing.T) {
	got, ok := NormalizeReferralWithdrawMethods([]string{"alipay", "USDT", "alipay"})
	if !ok || !slices.Equal(got, []string{"usdt", "alipay"}) {
		t.Fatalf("expected [usdt alipay], got %v (ok=%v)", got, ok)
	}
	empty, ok := NormalizeReferralWithdrawMethods([]string{})
	if !ok || empty == nil || len(empty) != 0 {
		t.Fatalf("expected non-nil empty slice, got %#v (ok=%v)", empty, ok)
	}
	if _, ok := NormalizeReferralWithdrawMethods([]string{"wechat"}); ok {
		t.Fatalf("expected wechat to be rejected")
	}
	if _, ok := NormalizeReferralWithdrawMethods([]string{"balance"}); ok {
		t.Fatalf("expected balance to be rejected from external cash methods")
	}
}

func TestNormalizeReferralWithdrawUSDTNetworks(t *testing.T) {
	got, ok := NormalizeReferralWithdrawUSDTNetworks([]string{"xlayer", "TRC20", "polygon", "bep20", "TRC20"})
	if !ok || !slices.Equal(got, []string{"TRC20", "BEP20", "POLYGON", "XLAYER"}) {
		t.Fatalf("expected canonical 4 chains, got %v (ok=%v)", got, ok)
	}
	if _, ok := NormalizeReferralWithdrawUSDTNetworks([]string{"ERC20"}); ok {
		t.Fatalf("expected unsupported chain ERC20 to be rejected")
	}
}

func TestReferralSettingEffectiveAndUsableWithdrawConfig(t *testing.T) {
	// Legacy unset: derives from configured params.
	legacyBoth := &ReferralSetting{
		MinWithdrawUSD:  floatPtr(10),
		USDTWithdrawFee: floatPtr(1),
	}
	methods, nets := legacyBoth.EffectiveWithdrawConfig(true)
	if !slices.Equal(methods, []string{"usdt", "alipay"}) {
		t.Fatalf("legacy both methods: got %v", methods)
	}
	if !slices.Equal(nets, []string{"TRC20", "BEP20", "POLYGON", "XLAYER"}) {
		t.Fatalf("legacy both nets: got %v", nets)
	}

	legacyUsdtOnly := &ReferralSetting{
		MinWithdrawUSD:  floatPtr(10),
		USDTWithdrawFee: floatPtr(1),
	}
	methods, nets = legacyUsdtOnly.EffectiveWithdrawConfig(false)
	if !slices.Equal(methods, []string{"usdt"}) || len(nets) != 4 {
		t.Fatalf("legacy usdt only: got %v / %v", methods, nets)
	}

	legacyAlipayOnly := &ReferralSetting{
		MinWithdrawUSD: floatPtr(10),
	}
	methods, nets = legacyAlipayOnly.EffectiveWithdrawConfig(true)
	if !slices.Equal(methods, []string{"alipay"}) || len(nets) != 0 {
		t.Fatalf("legacy alipay only: got %v / %v", methods, nets)
	}

	// Explicitly empty: disables all external methods even if params exist.
	explicitEmpty := &ReferralSetting{
		MinWithdrawUSD:       floatPtr(10),
		USDTWithdrawFee:      floatPtr(1),
		WithdrawMethods:      []string{},
		WithdrawUSDTNetworks: []string{"TRC20"},
	}
	methods, nets = explicitEmpty.UsableWithdrawConfig(true)
	if len(methods) != 0 || len(nets) != 0 {
		t.Fatalf("explicit empty should disable external methods, got %v / %v", methods, nets)
	}

	// Explicitly USDT only with 2 chains.
	explicitUsdt := &ReferralSetting{
		MinWithdrawUSD:       floatPtr(10),
		USDTWithdrawFee:      floatPtr(1),
		WithdrawMethods:      []string{"usdt"},
		WithdrawUSDTNetworks: []string{"XLAYER", "TRC20"},
	}
	methods, nets = explicitUsdt.UsableWithdrawConfig(true)
	if !slices.Equal(methods, []string{"usdt"}) || !slices.Equal(nets, []string{"TRC20", "XLAYER"}) {
		t.Fatalf("explicit usdt only: got %v / %v", methods, nets)
	}
}

func TestGetReferralSettingClonesSlices(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	origMethods := referralSetting.WithdrawMethods
	origNets := referralSetting.WithdrawUSDTNetworks
	referralSetting.WithdrawMethods = []string{"usdt"}
	referralSetting.WithdrawUSDTNetworks = []string{"TRC20"}
	common.OptionMapRWMutex.Unlock()
	defer func() {
		common.OptionMapRWMutex.Lock()
		referralSetting.WithdrawMethods = origMethods
		referralSetting.WithdrawUSDTNetworks = origNets
		common.OptionMapRWMutex.Unlock()
	}()

	snap := GetReferralSetting()
	snap.WithdrawMethods[0] = "mutated"
	snap.WithdrawUSDTNetworks[0] = "MUTATED"

	snap2 := GetReferralSetting()
	if !slices.Equal(snap2.WithdrawMethods, []string{"usdt"}) {
		t.Fatalf("expected cloned WithdrawMethods, got %v", snap2.WithdrawMethods)
	}
	if !slices.Equal(snap2.WithdrawUSDTNetworks, []string{"TRC20"}) {
		t.Fatalf("expected cloned WithdrawUSDTNetworks, got %v", snap2.WithdrawUSDTNetworks)
	}
}
