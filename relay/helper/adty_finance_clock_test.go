package helper

import (
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAdmissionClockSurvivesRetry(t *testing.T) {
	accepted := time.Date(2026, 11, 22, 1, 0, 0, 0, time.UTC)
	input := billingexpr.RequestInput{}
	local, err := service.FreezeAdtyBillingClock(&input, nil, accepted)
	require.NoError(t, err)
	require.Equal(t, "2026-11-22T09:00:00+08:00", local)
	retry, err := ResolveIncomingBillingExprRequestInput(nil, &relaycommon.RelayInfo{BillingRequestInput: &input, StartTime: accepted.Add(24 * time.Hour)})
	require.NoError(t, err)
	require.Equal(t, accepted, retry.AcceptedAt)
	require.Equal(t, input.BusinessTimezone, retry.BusinessTimezone)
	_, trace, err := billingexpr.RunExprByHashWithRequest(`hour("") == 9 ? tier("local", fixed(2)) : tier("other", fixed(1))`, "", billingexpr.TokenParams{}, retry)
	require.NoError(t, err)
	require.Equal(t, "local", trace.MatchedTier)
	expr := `hour("") == 9 ? tier("local", fixed(2)) : tier("other", fixed(1))`
	snap := billingexpr.BillingSnapshot{AcceptedAt: input.AcceptedUnix, BusinessTimezone: input.BusinessTimezone, ExprString: expr, ExprHash: billingexpr.ExprHashString(expr), GroupRatio: 1, QuotaPerUnit: 500000}
	settled, err := billingexpr.ComputeTieredQuotaWithRequest(&snap, billingexpr.TokenParams{}, billingexpr.RequestInput{AcceptedAt: accepted.Add(24 * time.Hour), BusinessTimezone: "UTC"})
	require.NoError(t, err)
	require.Equal(t, "local", settled.MatchedTier)
	require.Equal(t, 1_000_000, settled.ActualQuotaAfterGroup)
}
