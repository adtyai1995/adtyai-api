package billingexpr

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimeHelpersUseFrozenAcceptedAtAndBusinessTimezone(t *testing.T) {
	t.Parallel()

	// 2026-06-15 15:59:59 UTC is 23:59:59 in Asia/Shanghai (weekday=Monday=1, month=6, day=15).
	accepted := time.Date(2026, 6, 15, 15, 59, 59, 0, time.UTC)
	expr := `hour() * 10000 + minute() * 100 + day() + weekday() * 1000000 + month() * 10000000`

	costShanghai, _, err := RunExprWithRequest(expr, TokenParams{}, RequestInput{
		AcceptedAt:       accepted,
		BusinessTimezone: "Asia/Shanghai",
	})
	require.NoError(t, err)
	assert.Equal(t, float64(6*10000000+1*1000000+23*10000+59*100+15), costShanghai)

	// Explicitly passing UTC overrides default BusinessTimezone while keeping the same frozen instant.
	costUTC, _, err := RunExprWithRequest(`hour("UTC") * 100 + minute("UTC")`, TokenParams{}, RequestInput{
		AcceptedAt:       accepted,
		BusinessTimezone: "Asia/Shanghai",
	})
	require.NoError(t, err)
	assert.Equal(t, float64(1559), costUTC)

	// Settlement via ComputeTieredQuotaWithRequest reuses BillingSnapshot.AcceptedAt and BusinessTimezone
	// even if wall-clock time has moved past midnight.
	tierExpr := `hour() >= 23 ? tier("night", p * 2) : tier("day", p * 10)`
	snap := &BillingSnapshot{
		BillingMode:      "tiered_expr",
		ModelName:        "time-model",
		ExprString:       tierExpr,
		ExprHash:         ExprHashString(tierExpr),
		GroupRatio:       1,
		EstimatedTier:    "night",
		QuotaPerUnit:     500000,
		AcceptedAt:       accepted.Unix(),
		BusinessTimezone: "Asia/Shanghai",
	}
	res, err := ComputeTieredQuota(snap, TokenParams{P: 1000, C: 0, Len: 1000})
	require.NoError(t, err)
	assert.Equal(t, "night", res.MatchedTier)
	assert.False(t, res.CrossedTier)
}
