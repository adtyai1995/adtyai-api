package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ftrOrderQuota is the fixture wallet. 1 USD = 500000 quota. Reads must not change it.
const ftrOrderQuota = 500000

type ftrOrderFixture struct {
	db    *gorm.DB
	owner model.User
	other model.User
}

func newFTROrderFixture(t *testing.T, name string) ftrOrderFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })

	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.TopUp{},
		&model.AdtyTopupOrder{},
		&model.BillingOperation{},
		&model.AdtyManualRefund{},
		&model.AdtySiteWithdrawal{},
		&model.AdtySiteInviteWithdrawal{},
		&model.ReferralWithdrawal{},
		&model.AdtySiteInviteCommission{},
		&model.ReferralCommission{},
		&model.AdtyStaffBalanceRequest{},
		&model.AdtyBalanceLedger{},
	))
	owner := model.User{
		Username: name + "-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Quota: ftrOrderQuota,
	}
	other := model.User{
		Username: name + "-other", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Quota: ftrOrderQuota,
	}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&other).Error)
	return ftrOrderFixture{db: db, owner: owner, other: other}
}

func (f ftrOrderFixture) call(t *testing.T, user model.User, method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	ctx, rec := newAuthenticatedContext(t, method, target, nil, user.Id)
	ctx.Set("role", user.Role)
	ctx.Set("username", user.Username)
	return ctx, rec
}

func (f ftrOrderFixture) assertWalletsUnchanged(t *testing.T) {
	t.Helper()
	for _, user := range []model.User{f.owner, f.other} {
		var quota int
		require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Select("quota").Scan(&quota).Error)
		assert.Equal(t, ftrOrderQuota, quota, "user %d wallet must stay at 500000 quota", user.Id)
	}
}

func TestFTRADTY0303UserBalanceLedgerQueryPG(t *testing.T) {
	f := newFTROrderFixture(t, "ftr-0303-ledger")
	own := model.AdtyBalanceLedger{
		UserId: f.owner.Id, DeltaQuota: ftrOrderQuota, BalanceBefore: 0, BalanceAfter: ftrOrderQuota,
		Type: model.AdtyLedgerTypeTopUp, RefType: "topup", RefId: "ftr-0303-own",
	}
	other := model.AdtyBalanceLedger{
		UserId: f.other.Id, DeltaQuota: 250000, BalanceBefore: 0, BalanceAfter: 250000,
		Type: model.AdtyLedgerTypeTopUp, RefType: "topup", RefId: "ftr-0303-other",
	}
	require.NoError(t, f.db.Create(&own).Error)
	require.NoError(t, f.db.Create(&other).Error)

	admin := f.owner
	admin.Role = common.RoleAdminUser
	ctx, rec := f.call(t, admin, http.MethodGet, "/api/adty/admin/users/"+strconv.Itoa(f.owner.Id)+"/ledger?p=1&page_size=10")
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(f.owner.Id)}}
	GetAdtyUserLedger(ctx)
	body := rec.Body.String()
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr-0303-own")
	assert.Contains(t, body, strconv.Itoa(ftrOrderQuota))
	assert.NotContains(t, body, "ftr-0303-other")
	assert.Equal(t, common.RoleAdminUser, admin.Role)

	foreignCtx, foreignRec := f.call(t, admin, http.MethodGet, "/api/adty/admin/users/"+strconv.Itoa(f.other.Id)+"/ledger?p=1&page_size=10")
	foreignCtx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(f.other.Id)}}
	GetAdtyUserLedger(foreignCtx)
	foreignBody := foreignRec.Body.String()
	require.Contains(t, foreignBody, `"success":true`, foreignBody)
	assert.Contains(t, foreignBody, "ftr-0303-other")
	assert.NotContains(t, foreignBody, "ftr-0303-own")

	var rows int64
	require.NoError(t, f.db.Model(&model.AdtyBalanceLedger{}).Count(&rows).Error)
	assert.EqualValues(t, 2, rows, "reading the ledger must not insert another balance row")
	f.assertWalletsUnchanged(t)
}

func TestFTRADTY0305SelfConsumptionOrdersPG(t *testing.T) {
	f := newFTROrderFixture(t, "ftr-0305")
	own := model.BillingOperation{
		OperationKey: "ftr-0305-own-op", UserId: f.owner.Id, Kind: "request", State: "settled",
		AppliedQuota: ftrOrderQuota, ReferenceId: 11, PayerTeamId: 0, SiteId: 0,
	}
	other := model.BillingOperation{
		OperationKey: "ftr-0305-other-op", UserId: f.other.Id, Kind: "request", State: "settled",
		AppliedQuota: 250000, ReferenceId: 12, PayerTeamId: 0, SiteId: 0,
	}
	require.NoError(t, f.db.Create(&own).Error)
	require.NoError(t, f.db.Create(&other).Error)

	ctx, rec := f.call(t, f.owner, http.MethodGet, "/api/adty/orders?order_type=consumption&p=1&page_size=10")
	GetAdtySelfOrders(ctx)
	body := rec.Body.String()
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr-0305-own-op")
	assert.Contains(t, body, `"order_type":"consumption"`)
	assert.Contains(t, body, `"status":"settled"`)
	assert.NotContains(t, body, "ftr-0305-other-op")
	assert.Equal(t, common.RoleCommonUser, f.owner.Role)

	otherCtx, otherRec := f.call(t, f.other, http.MethodGet, "/api/adty/orders?order_type=consumption&p=1&page_size=10")
	GetAdtySelfOrders(otherCtx)
	otherBody := otherRec.Body.String()
	require.Contains(t, otherBody, `"success":true`, otherBody)
	assert.Contains(t, otherBody, "ftr-0305-other-op")
	assert.NotContains(t, otherBody, "ftr-0305-own-op")

	var stored model.BillingOperation
	require.NoError(t, f.db.First(&stored, "operation_key = ?", own.OperationKey).Error)
	assert.Equal(t, ftrOrderQuota, stored.AppliedQuota, "listing consumption orders must not re-apply the settled charge")
	f.assertWalletsUnchanged(t)
}

func insertFTRTopUp(t *testing.T, db *gorm.DB, userID int, tradeNo, status, method string, face string) {
	t.Helper()
	amount := decimal.RequireFromString(face)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: userID, TradeNo: tradeNo, Amount: ftrOrderQuota, Money: 1,
		PaymentMethod: method, PaymentProvider: "epay", Status: status, CreateTime: 1893456000,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		UserId: userID, TradeNo: tradeNo, Provider: "epay", Method: method,
		FaceUSD: amount, CreditQuota: ftrOrderQuota, PayAmount: amount, PayCurrency: "CNY",
		Business: "wallet_topup",
	}).Error)
}

func TestFTRADTY0306SelfOrderDetailPG(t *testing.T) {
	f := newFTROrderFixture(t, "ftr-0306")
	insertFTRTopUp(t, f.db, f.owner.Id, "ftr-0306-own", common.TopUpStatusSuccess, "alipay", "1")
	insertFTRTopUp(t, f.db, f.other.Id, "ftr-0306-other", common.TopUpStatusSuccess, "wxpay", "1")

	ctx, rec := f.call(t, f.owner, http.MethodGet, "/api/adty/topup/ftr-0306-own")
	ctx.Params = gin.Params{{Key: "trade_no", Value: "ftr-0306-own"}}
	GetAdtyTopUpOrder(ctx)
	body := rec.Body.String()
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr-0306-own")
	assert.Contains(t, body, `"status":"success"`)
	assert.NotContains(t, body, "ftr-0306-other")
	assert.Equal(t, common.RoleCommonUser, f.owner.Role)

	foreignCtx, foreignRec := f.call(t, f.other, http.MethodGet, "/api/adty/topup/ftr-0306-own")
	foreignCtx.Params = gin.Params{{Key: "trade_no", Value: "ftr-0306-own"}}
	GetAdtyTopUpOrder(foreignCtx)
	foreignBody := foreignRec.Body.String()
	assert.Contains(t, foreignBody, "Order not found")
	assert.NotContains(t, foreignBody, `"success":true`)

	var count int64
	require.NoError(t, f.db.Model(&model.TopUp{}).Count(&count).Error)
	assert.EqualValues(t, 2, count, "reading an order must not create another top-up row")
	f.assertWalletsUnchanged(t)
}

func TestFTRADTY0307OrderPaymentStatusPG(t *testing.T) {
	f := newFTROrderFixture(t, "ftr-0307")
	insertFTRTopUp(t, f.db, f.owner.Id, "ftr-0307-pending", common.TopUpStatusPending, "alipay", "1")
	insertFTRTopUp(t, f.db, f.owner.Id, "ftr-0307-paid", common.TopUpStatusSuccess, "wxpay", "1")

	pendingCtx, pendingRec := f.call(t, f.owner, http.MethodGet, "/api/adty/topup/ftr-0307-pending")
	pendingCtx.Params = gin.Params{{Key: "trade_no", Value: "ftr-0307-pending"}}
	GetAdtyTopUpOrder(pendingCtx)
	pendingBody := pendingRec.Body.String()
	require.Contains(t, pendingBody, `"success":true`, pendingBody)
	assert.Contains(t, pendingBody, `"status":"pending"`)
	assert.Contains(t, pendingBody, "alipay")

	paidCtx, paidRec := f.call(t, f.owner, http.MethodGet, "/api/adty/topup/ftr-0307-paid")
	paidCtx.Params = gin.Params{{Key: "trade_no", Value: "ftr-0307-paid"}}
	GetAdtyTopUpOrder(paidCtx)
	paidBody := paidRec.Body.String()
	require.Contains(t, paidBody, `"success":true`, paidBody)
	assert.Contains(t, paidBody, `"status":"success"`)
	assert.Contains(t, paidBody, "wxpay")
	assert.NotContains(t, paidBody, `"status":"pending"`)

	var pending model.TopUp
	require.NoError(t, f.db.Where("trade_no = ?", "ftr-0307-pending").First(&pending).Error)
	assert.Equal(t, common.TopUpStatusPending, pending.Status, "viewing payment status must not mark a pending order paid")
	f.assertWalletsUnchanged(t)
}

func TestFTRADTY0306MissingOrderStaysUnpaidPG(t *testing.T) {
	f := newFTROrderFixture(t, "ftr-0306-missing")
	ctx, rec := f.call(t, f.owner, http.MethodGet, "/api/adty/topup/missing-trade")
	ctx.Params = gin.Params{{Key: "trade_no", Value: "missing-trade"}}
	GetAdtyTopUpOrder(ctx)
	body := rec.Body.String()
	assert.Contains(t, body, "Order not found")
	var count int64
	require.NoError(t, f.db.Model(&model.TopUp{}).Count(&count).Error)
	assert.Zero(t, count)
	f.assertWalletsUnchanged(t)
}
