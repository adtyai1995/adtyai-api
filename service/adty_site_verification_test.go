package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAdtyOwnerWithdrawalDigestNormalizes8DecimalExpectedRate(t *testing.T) {
	// With 8-decimal parsing, trailing zeros on an 8-decimal rate canonicalize identically
	// to the stripped decimal representation (matching controller ParseBoundedDecimalString(..., 12, 8)).
	d1 := AdtyOwnerActionRequestDigest(AdtySiteOwnerActionWithdrawalCreate, AdtyOwnerWithdrawalDigestInput{
		AmountUSD:      "100.00",
		Method:         "usdt",
		Network:        "TRC20",
		AccountNo:      "TAddr123",
		ExpectedRate:   "0.99876540",
		IdempotencyKey: "wd-8dec-1",
	})
	d2 := AdtyOwnerActionRequestDigest(AdtySiteOwnerActionWithdrawalCreate, AdtyOwnerWithdrawalDigestInput{
		AmountUSD:      "100",
		Method:         "USDT",
		Network:        "trc20",
		AccountNo:      "TAddr123",
		ExpectedRate:   "0.9987654",
		IdempotencyKey: "wd-8dec-1",
	})
	assert.NotEmpty(t, d1)
	assert.Equal(t, d1, d2, "8-decimal expected_rate must normalize identically between step-up proof and controller")

	// Distinct 8th decimal digit produces a distinct digest.
	d3 := AdtyOwnerActionRequestDigest(AdtySiteOwnerActionWithdrawalCreate, AdtyOwnerWithdrawalDigestInput{
		AmountUSD:      "100",
		Method:         "usdt",
		Network:        "TRC20",
		AccountNo:      "TAddr123",
		ExpectedRate:   "0.99876543",
		IdempotencyKey: "wd-8dec-1",
	})
	assert.NotEqual(t, d1, d3)
}
