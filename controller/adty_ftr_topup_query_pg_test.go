package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insertFTRSelfTopUp(t *testing.T, f ftrOrderFixture, user model.User, tradeNo, status, method string, created int64) {
	t.Helper()
	require.NoError(t, f.db.Create(&model.TopUp{
		UserId: user.Id, TradeNo: tradeNo, Amount: 8, Money: 1,
		Status: status, PaymentMethod: method, PaymentProvider: model.PaymentProviderAdtyEpay,
		CreateTime: created,
	}).Error)
}

func TestFTRADTY0308SelfTopupTradeNoQueryPG(t *testing.T) {
	f := newFTROrderFixture(t, "ftr-0308")
	insertFTRSelfTopUp(t, f, f.owner, "EPY770003", common.TopUpStatusSuccess, "alipay", 1893456000)
	insertFTRSelfTopUp(t, f, f.owner, "EPY770009", common.TopUpStatusSuccess, "wxpay", 1893456001)
	insertFTRSelfTopUp(t, f, f.other, "EPY770003-other", common.TopUpStatusSuccess, "alipay", 1893456002)

	ctx, rec := f.call(t, f.owner, http.MethodGet, "/api/adty/topup/self?trade_no=770003&p=1&page_size=10")
	GetAdtyTopUpSelf(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "EPY770003")
	assert.NotContains(t, body, "EPY770009")
	assert.NotContains(t, body, "EPY770003-other")
	assert.Contains(t, body, `"total":1`)
	assert.Equal(t, common.RoleCommonUser, f.owner.Role)
	f.assertWalletsUnchanged(t)
	var ledgers int64
	require.NoError(t, f.db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

func TestFTRADTY0309SelfTopupStatusPagePG(t *testing.T) {
	f := newFTROrderFixture(t, "ftr-0309")
	insertFTRSelfTopUp(t, f, f.owner, "ftr-0309-pending-old", common.TopUpStatusPending, "alipay", 1893456100)
	insertFTRSelfTopUp(t, f, f.owner, "ftr-0309-success", common.TopUpStatusSuccess, "alipay", 1893456101)
	insertFTRSelfTopUp(t, f, f.owner, "ftr-0309-pending-new", common.TopUpStatusPending, "wxpay", 1893456102)
	insertFTRSelfTopUp(t, f, f.other, "ftr-0309-other-pending", common.TopUpStatusPending, "alipay", 1893456103)

	ctx, rec := f.call(t, f.owner, http.MethodGet, "/api/adty/topup/self?status=pending&p=1&page_size=1")
	GetAdtyTopUpSelf(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr-0309-pending-new")
	assert.NotContains(t, body, "ftr-0309-pending-old")
	assert.NotContains(t, body, "ftr-0309-success")
	assert.NotContains(t, body, "ftr-0309-other-pending")
	assert.Contains(t, body, `"total":2`)

	page2, rec2 := f.call(t, f.owner, http.MethodGet, "/api/adty/topup/self?status=pending&p=2&page_size=1")
	GetAdtyTopUpSelf(page2)
	body2 := rec2.Body.String()
	require.Equal(t, http.StatusOK, rec2.Code, body2)
	require.Contains(t, body2, `"success":true`, body2)
	assert.Contains(t, body2, "ftr-0309-pending-old")
	assert.NotContains(t, body2, "ftr-0309-pending-new")
	assert.Contains(t, body2, `"total":2`)

	methodCtx, methodRec := f.call(t, f.owner, http.MethodGet, "/api/adty/topup/self?status=pending&payment_method=alipay&p=1&page_size=10")
	GetAdtyTopUpSelf(methodCtx)
	methodBody := methodRec.Body.String()
	require.Contains(t, methodBody, `"success":true`, methodBody)
	assert.Contains(t, methodBody, "ftr-0309-pending-old")
	assert.NotContains(t, methodBody, "ftr-0309-pending-new")
	assert.NotContains(t, methodBody, "ftr-0309-other-pending")
	assert.Contains(t, methodBody, `"total":1`)
	assert.Equal(t, common.RoleCommonUser, f.owner.Role)
	f.assertWalletsUnchanged(t)
	var ledgers int64
	require.NoError(t, f.db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

func TestFTRADTY0310UserLogHistoryPG(t *testing.T) {
	f := newFTROrderFixture(t, "ftr-0310")
	require.NoError(t, f.db.AutoMigrate(&model.Log{}))
	now := common.GetTimestamp()
	require.NoError(t, f.db.Create(&model.Log{
		UserId: f.owner.Id, CreatedAt: now, Type: model.LogTypeConsume,
		Content: "ftr-0310-own", Username: f.owner.Username, ModelName: "ftr-0310-model",
		Quota: 100, TokenName: "ftr-0310-key",
	}).Error)
	require.NoError(t, f.db.Create(&model.Log{
		UserId: f.other.Id, CreatedAt: now, Type: model.LogTypeConsume,
		Content: "ftr-0310-other", Username: f.other.Username, ModelName: "ftr-0310-hidden",
		Quota: 200, TokenName: "ftr-0310-other-key",
	}).Error)

	ctx, rec := f.call(t, f.owner, http.MethodGet, "/api/log/self?p=1&page_size=10")
	GetUserLogs(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr-0310-own")
	assert.NotContains(t, body, "ftr-0310-other")
	assert.Contains(t, body, `"total":1`)
	assert.Equal(t, common.RoleCommonUser, f.owner.Role)

	var stored model.Log
	require.NoError(t, f.db.Where("user_id = ? AND content = ?", f.owner.Id, "ftr-0310-own").First(&stored).Error)
	assert.Equal(t, 100, stored.Quota, "reading personal logs must not rewrite the stored usage quota")
	f.assertWalletsUnchanged(t)
	var ledgers int64
	require.NoError(t, f.db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
