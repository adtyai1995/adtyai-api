package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY0304SelfTopupListPG(t *testing.T) {
	f := newFTROrderFixture(t, "ftr-0304")
	require.NoError(t, f.db.Create(&model.TopUp{
		UserId: f.owner.Id, TradeNo: "ftr-0304-own", Amount: 8,
		Status: common.TopUpStatusSuccess, PaymentProvider: model.PaymentProviderAdtyEpay,
	}).Error)
	require.NoError(t, f.db.Create(&model.TopUp{
		UserId: f.other.Id, TradeNo: "ftr-0304-other", Amount: 9,
		Status: common.TopUpStatusSuccess, PaymentProvider: model.PaymentProviderAdtyEpay,
	}).Error)

	ctx, rec := f.call(t, f.owner, http.MethodGet, "/api/adty/topup/self?p=1&page_size=10")
	GetAdtyTopUpSelf(ctx)
	body := rec.Body.String()
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr-0304-own")
	assert.NotContains(t, body, "ftr-0304-other")
	assert.Equal(t, common.RoleCommonUser, f.owner.Role)
	f.assertWalletsUnchanged(t)
	var ledgers int64
	require.NoError(t, f.db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
