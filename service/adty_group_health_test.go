package service

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteAdtyGroupProbeUsesIndependentIdentityWithNoBillingOrKeySideEffects(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	require.NoError(t, db.AutoMigrate(
		&model.AdtySite{},
		&model.AdtyGroup{},
		&model.Channel{},
		&model.Ability{},
		&model.AdtyBalanceLedger{},
		&model.AdtyFundBatch{},
		&model.AdtyTeamLedger{},
		&model.AdtySiteWallet{},
		&model.AdtySiteWalletLedger{},
		&model.AdtySiteEarningDaily{},
		&model.AdtySiteInviteCommission{},
		&model.AdtySiteTierEvaluation{},
		&model.Log{},
	))
	require.NoError(t, db.Create(&model.AdtyGroup{
		ID:    "vip",
		Names: model.AdtyLangText{"en": "VIP"},
		Descs: model.AdtyLangText{},
	}).Error)

	site := &model.AdtySite{
		OwnerUserId:   10,
		Subdomain:     "probe-site",
		RootDomain:    "sites.example.com",
		Status:        model.AdtySiteStatusActive,
		AllowedGroups: `["vip"]`,
	}
	require.NoError(t, db.Create(site).Error)
	InvalidateAdtySiteCache()

	customer := referralUser(t, db, "probe-customer")
	require.NoError(t, db.Model(customer).Updates(map[string]any{
		"site_id": customer.SiteId,
		"quota":   500000,
		"group":   "vip",
	}).Error)
	customerToken := &model.Token{
		UserId: customer.Id,
		Key:    "sk-customer-secret-token",
		Name:   "cust-key",
		Group:  "vip",
		Status: common.TokenStatusEnabled,
	}
	require.NoError(t, db.Create(customerToken).Error)

	// Independent probe identity rejects non-zero user IDs and customer tokens.
	require.Error(t, ValidateAdtyGroupProbeIdentity("other-identity", 0, "upstream-key"))
	require.Error(t, ValidateAdtyGroupProbeIdentity(AdtyGroupProbeIdentity, customer.Id, "upstream-key"))
	require.Error(t, ValidateAdtyGroupProbeIdentity(AdtyGroupProbeIdentity, 0, customerToken.Key))
	require.NoError(t, ValidateAdtyGroupProbeIdentity(AdtyGroupProbeIdentity, AdtyGroupProbeUserID, "upstream-provider-secret"))

	baseURL := "https://upstream.probe.example/v1"
	channel := &model.Channel{
		Name:    "VIP Probe Channel",
		Type:    1,
		Key:     "upstream-provider-secret",
		BaseURL: &baseURL,
		Models:  "gpt-4o",
		Group:   "vip",
		Status:  common.ChannelStatusEnabled,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "vip",
		Model:     "gpt-4o",
		ChannelId: channel.Id,
		Enabled:   true,
	}).Error)

	var seenIdentity, seenAuth string
	oldClient := adtyTranslationHTTPClient
	t.Cleanup(func() { adtyTranslationHTTPClient = oldClient })
	adtyTranslationHTTPClient = func(_ time.Duration) *http.Client {
		return &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				seenIdentity = req.Header.Get(AdtyGroupProbeHeader)
				seenAuth = req.Header.Get("Authorization")
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"pong"}}]}`)),
				}, nil
			}),
		}
	}

	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	probed, err := RunAdtyGroupProbesOnce(now)
	require.NoError(t, err)
	assert.Equal(t, 1, probed)
	assert.Equal(t, AdtyGroupProbeIdentity, seenIdentity)
	assert.Equal(t, "Bearer upstream-provider-secret", seenAuth)

	// Both platform (site 0) and active site allowing "vip" receive the probe sample.
	platformRows, err := listAdtyGroupHealth(0, "gpt-4o", now)
	require.NoError(t, err)
	require.Len(t, platformRows, 1)
	assert.Equal(t, model.AdtyGroupHealthOK, platformRows[0].Status)
	assert.Equal(t, 1, platformRows[0].ProbeSamples)
	require.NotNil(t, platformRows[0].ProbeRate)
	assert.InDelta(t, 1.0, *platformRows[0].ProbeRate, 0.001)

	siteRows, err := listAdtyGroupHealth(site.Id, "gpt-4o", now)
	require.NoError(t, err)
	require.Len(t, siteRows, 1)
	assert.Equal(t, "site", siteRows[0].Scope)
	assert.Equal(t, model.AdtyGroupHealthOK, siteRows[0].Status)
	assert.Equal(t, 1, siteRows[0].ProbeSamples)
	require.NotNil(t, siteRows[0].ProbeRate)
	assert.InDelta(t, 1.0, *siteRows[0].ProbeRate, 0.001)

	// Verify zero side effects on customer quota, token group, ledgers, commissions, or achievements.
	var afterUser model.User
	require.NoError(t, db.First(&afterUser, customer.Id).Error)
	assert.EqualValues(t, 500000, afterUser.Quota)
	var afterToken model.Token
	require.NoError(t, db.First(&afterToken, customerToken.Id).Error)
	assert.Equal(t, "vip", afterToken.Group)

	for _, tableModel := range []any{
		&model.AdtyBalanceLedger{},
		&model.AdtyFundBatch{},
		&model.AdtyTeamLedger{},
		&model.AdtySiteWalletLedger{},
		&model.AdtySiteEarningDaily{},
		&model.AdtySiteInviteCommission{},
		&model.AdtySiteTierEvaluation{},
		&model.Log{},
	} {
		var cnt int64
		require.NoError(t, db.Model(tableModel).Count(&cnt).Error)
		assert.Zero(t, cnt)
	}
}

func TestExecuteAdtyGroupProbeDistinguishesClientRefusalFromUpstreamFailure(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	baseURL := "https://upstream.probe.example/v1"
	channel := &model.Channel{
		Name:    "Refusal Probe Channel",
		Type:    1,
		Key:     "upstream-key",
		BaseURL: &baseURL,
		Models:  "gpt-4o",
		Group:   "vip",
		Status:  common.ChannelStatusEnabled,
	}
	require.NoError(t, db.Create(channel).Error)

	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	oldDispatch := AdtyGroupProbeDispatchFunc
	t.Cleanup(func() { AdtyGroupProbeDispatchFunc = oldDispatch })

	// 1. Client refusal (HTTP 400 invalid_request_error) must not count as an upstream failure.
	AdtyGroupProbeDispatchFunc = func(_ *model.Channel, _, _, _, _ string) AdtyGroupProbeResult {
		return AdtyGroupProbeResult{
			StatusCode: http.StatusBadRequest,
			ErrorCode:  "invalid_request",
			ErrorType:  "invalid_request_error",
		}
	}
	sample, err := ExecuteAdtyGroupProbe(0, "vip", "gpt-4o", now)
	require.NoError(t, err)
	assert.Equal(t, model.AdtyGroupHealthCustomer, sample.Outcome)

	rows, err := listAdtyGroupHealth(0, "gpt-4o", now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 0, rows[0].ProbeSamples, "client refusal must not count in probe failure samples")
	assert.Equal(t, model.AdtyGroupHealthNoData, rows[0].Status)

	// 2. Upstream failure (HTTP 503 service_unavailable) counts as probe failure and degrades group health.
	AdtyGroupProbeDispatchFunc = func(_ *model.Channel, _, _, _, _ string) AdtyGroupProbeResult {
		return AdtyGroupProbeResult{
			StatusCode: http.StatusServiceUnavailable,
			ErrorCode:  "service_unavailable",
			ErrorType:  "server_error",
		}
	}
	failSample, err := ExecuteAdtyGroupProbe(0, "vip", "gpt-4o", now.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, model.AdtyGroupHealthUpstream, failSample.Outcome)

	rows, err = listAdtyGroupHealth(0, "gpt-4o", now.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 1, rows[0].ProbeSamples)
	require.NotNil(t, rows[0].ProbeRate)
	assert.InDelta(t, 0.0, *rows[0].ProbeRate, 0.001)
	assert.Equal(t, model.AdtyGroupHealthDegraded, rows[0].Status)
}
