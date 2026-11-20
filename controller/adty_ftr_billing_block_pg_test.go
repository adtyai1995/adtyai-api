package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADTY-07-03: insufficient personal quota fails billing before any upstream call.
// Relay returns at PrepareRequestBilling and does not enter channel selection.
func TestFTRADTY0703InsufficientQuotaBlocksUpstreamPG(t *testing.T) {
	ftrRelayDB(t)
	ftrPinRelayPrice(t, `{"ftr-0703-block":0.02}`)
	buyer := model.User{
		Username: "ftr0703", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", SiteId: 0, StaffSiteId: 0, Quota: 100,
	}
	require.NoError(t, model.DB.Create(&buyer).Error)
	require.Equal(t, common.RoleCommonUser, buyer.Role)
	require.Zero(t, buyer.SiteId)
	token := model.Token{
		UserId: buyer.Id, Key: "sk-ftr0703", Name: "ftr0703", Status: common.TokenStatusEnabled,
		RemainQuota: 100000, UsedQuota: 0, UnlimitedQuota: false, ExpiredTime: -1, Group: "default",
	}
	require.NoError(t, model.DB.Create(&token).Error)

	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.Error(w, "upstream must not be called", http.StatusBadGateway)
	}))
	t.Cleanup(upstream.Close)

	ctx, recorder := ftrRelayContext(t, http.MethodPost, "/v1/chat/completions",
		`{"model":"ftr-0703-block","messages":[{"role":"user","content":"hi"}]}`,
		buyer, token, upstream.URL, "ftr-0703-block", constant.ChannelTypeOpenAI)
	Relay(ctx, types.RelayFormatOpenAI)

	require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
	assert.False(t, called, "upstream was called after billing should have stopped the request")
	assert.Equal(t, 100, ftrQuota(t, buyer.Id))
	var remain int
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", token.Id).Select("remain_quota").Scan(&remain).Error)
	assert.Equal(t, 100000, remain)
	var ops int64
	require.NoError(t, model.DB.Model(&model.BillingOperation{}).Count(&ops).Error)
	assert.Zero(t, ops)
	var ledgers int64
	require.NoError(t, model.DB.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
