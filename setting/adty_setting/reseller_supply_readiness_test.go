package adty_setting

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"strings"
	"testing"
)

func TestResellerCanOpenWithSupplyCostsAndRootDomain(t *testing.T) {
	s := DefaultResellerSetting()
	cost, minimum := 0.8, 0.5
	s.Enabled, s.CostFactor, s.MinCostFactor = true, &cost, &minimum
	s.RootDomains = []string{"example.com"}
	if !s.Ready() {
		t.Fatal("reseller sites should be ready with supply terms and a root domain")
	}
	if err := ValidateResellerSetting(&s, false); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyRetailLimitExcludedFromAdminSettings(t *testing.T) {
	s := DefaultResellerSetting()
	ignored := 0.5
	s.MaxMarkup = &ignored
	mapped, err := config.ConfigToMap(&s)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"max_markup", "MaxMarkup"} {
		if _, ok := mapped[key]; ok {
			t.Errorf("retired field %s accepted by settings API", key)
		}
	}
	raw, err := common.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "max_markup") {
		t.Fatal("retired limit exposed in response")
	}
}
