package service

import (
	"github.com/shopspring/decimal"
	"testing"
)

func TestTeamStoredPayRatioPrecision(t *testing.T) {
	for raw, want := range map[string]bool{"0.00001": false, "0.12345": false, "0": false, "0.0001": true, "1": true, "0.80000": true} {
		if got := adtySiteTeamPayRatioConfigValid(decimal.RequireFromString(raw)); got != want {
			t.Errorf("ratio %s accepted = %t, want %t", raw, got, want)
		}
	}
}
