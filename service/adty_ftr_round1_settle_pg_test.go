package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY0706SettleDeltaPG(t *testing.T) {
	db := ftrPreconsumeDB(t)
	pgtest.RecordServerVersion(t, db)
	user, token, ctx, info := ftrPreconsumeCaller(t, db, "ftr0706", "ftr0706-settle")

	require.Error(t, SettleBilling(ctx, info, -1))
	var before model.User
	require.NoError(t, db.Select("quota").First(&before, user.Id).Error)
	require.Equal(t, 1_000_000, before.Quota)

	require.Nil(t, PreConsumeBilling(ctx, 10000, info))
	require.NoError(t, SettleBilling(ctx, info, 7500))

	var stored model.User
	require.NoError(t, db.Select("quota").First(&stored, user.Id).Error)
	assert.Equal(t, 992500, stored.Quota)
	var storedToken model.Token
	require.NoError(t, db.First(&storedToken, token.Id).Error)
	assert.Equal(t, 7500, storedToken.UsedQuota)
	assert.Equal(t, 92500, storedToken.RemainQuota)
	op, err := model.GetBillingOperation("request:ftr0706-settle")
	require.NoError(t, err)
	require.NotNil(t, op)
	assert.Equal(t, 7500, op.ActualQuota)
	assert.Equal(t, 10000, op.ReservedQuota)
}

func TestFTRADTY0610RelayRetryDecisionPG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	gin.SetMode(gin.TestMode)
	upstream := func(status int) *types.NewAPIError {
		return types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, status)
	}
	cases := []struct {
		name    string
		err     *types.NewAPIError
		retries int
		setup   func(*gin.Context)
		want    string
	}{
		{name: "nil", retries: 1, want: "stop"},
		{name: "pinned", err: upstream(http.StatusTooManyRequests), retries: 1, setup: func(c *gin.Context) {
			GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: 1, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt})
		}, want: "stop"},
		{name: "skip", err: types.NewErrorWithStatusCode(errors.New("local"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry()), retries: 1, want: "stop"},
		{name: "budget", err: upstream(http.StatusTooManyRequests), retries: 0, want: "stop"},
		{name: "2xx", err: upstream(http.StatusOK), retries: 1, want: "stop"},
		{name: "channel", err: types.NewError(errors.New("no key"), types.ErrorCodeChannelNoAvailableKey), retries: 0, want: "retry"},
		{name: "status", err: upstream(http.StatusTooManyRequests), retries: 1, want: "retry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if tc.setup != nil {
				tc.setup(c)
			}
			got := DecideRelayRetry(c, tc.err, tc.retries)
			assert.Equal(t, tc.want, got.Action)
		})
	}
}

func TestFTRADTY2107BalanceLedgerGapPG(t *testing.T) {
	db := prepareSiteDB(t)
	pgtest.RecordServerVersion(t, db)
	_, site := openActiveSite(t, db, "ftr2107")
	customer := siteCustomer(t, db, site, "ftr2107-buyer")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", customer.Id).
		Updates(map[string]any{"quota": 1_000_000, "quota_pending": 0}).Error)
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	require.NoError(t, db.Model(&model.AdtySiteWallet{}).
		Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).
		Update("balance", decimal.RequireFromString("5")).Error)

	gap := ReconcileOperatingWallet(decimal.RequireFromString("2"), decimal.Zero)
	require.Equal(t, "-2", gap.String())

	items, err := detectExistingAdtyFundAnomalies(site.Id)
	require.NoError(t, err)
	var found bool
	for _, item := range items {
		if item.Kind == AdtyAnomalyBalanceLedgerGap {
			found = true
			require.Equal(t, "-2", item.Difference)
			require.Contains(t, item.Evidence, `"customer_principal":"2"`)
			require.Contains(t, item.Evidence, `"ledger_sum"`)
		}
	}
	require.True(t, found)

	var earningBefore model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earningBefore).Error)
	kinds, err := CollectAdtyFundAnomalies(site.Id)
	require.NoError(t, err)
	require.Contains(t, kinds, AdtyAnomalyBalanceLedgerGap)
	rows, err := ListAdtyAnomalyDispositions(site.Id)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	require.Equal(t, "open", rows[0].Status)

	var earningAfter model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earningAfter).Error)
	require.True(t, earningAfter.Balance.Equal(earningBefore.Balance))
	var quota int
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", customer.Id).Select("quota").Scan(&quota).Error)
	require.Equal(t, 1_000_000, quota)
}
