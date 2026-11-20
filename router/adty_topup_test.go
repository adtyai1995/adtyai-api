package router

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUnifiedOrdersIncludeScopedConsumptionPG(t *testing.T) {
	f := newAdtyGuardFixture(t)
	buyerToken := adtySessionToken(t, f.db, "unified-order-customer", common.RoleCommonUser)
	otherToken := adtySessionToken(t, f.db, "unified-order-other", common.RoleCommonUser)
	var buyer, other model.User
	require.NoError(t, f.db.Where("username = ?", "unified-order-customer").First(&buyer).Error)
	require.NoError(t, f.db.Where("username = ?", "unified-order-other").First(&other).Error)
	// Read-only fixtures at the same timestamp exercise the true total and
	// the stable tie-break beyond the first 500 rows.
	const at = 1_789_000_001
	ops := make([]model.BillingOperation, 0, 503)
	for i := range 501 {
		ops = append(ops, model.BillingOperation{OperationKey: fmt.Sprintf("request:unified-%03d", i),
			UserId: buyer.Id, Kind: "request", State: model.BillingSettled, AppliedQuota: 500_000, CreatedAt: at})
	}
	ops = append(ops,
		model.BillingOperation{OperationKey: "request:foreign-site", SiteId: 99, UserId: other.Id, Kind: "request", State: model.BillingSettled, AppliedQuota: 500_000, CreatedAt: at},
		model.BillingOperation{OperationKey: "request:reserved", UserId: buyer.Id, Kind: "request", State: model.BillingReserved, ReservedQuota: 500_000, CreatedAt: at},
	)
	require.NoError(t, f.db.CreateInBatches(&ops, 100).Error)
	seedRouterOrder(t, f.db, buyer.Id, "unified-topup", common.TopUpStatusPending, at)
	const adminOrders = "/api/user/admin/finance/recharge-audit"
	const selfOrders = "/api/adty/orders"
	type row struct {
		OrderType string `json:"order_type"`
		TradeNo   string `json:"trade_no"`
		UserID    int    `json:"user_id"`
		FaceUSD   string `json:"face_usd"`
	}
	type orders struct {
		Total int   `json:"total"`
		Items []row `json:"items"`
	}
	get := func(path, token string) orders {
		t.Helper()
		response := adtyJSON(t, f.engine, http.MethodGet, path, "", token)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		return decodeAdtyData[orders](t, response.Body)
	}

	first := get(adminOrders+"?order_type=consumption&site_id=0&status=settled&page_size=1", f.root)
	require.Equal(t, 501, first.Total)
	require.Equal(t, []row{{OrderType: "consumption", TradeNo: "request:unified-500", UserID: buyer.Id, FaceUSD: "1"}}, first.Items)
	last := get(adminOrders+"?order_type=consumption&site_id=0&page_size=1&p=501", f.root)
	require.Equal(t, 501, last.Total)
	require.Len(t, last.Items, 1)
	assert.Equal(t, "request:unified-000", last.Items[0].TradeNo)
	filtered := get(adminOrders+"?site_id=0&keyword=unified-000", f.root)
	require.Equal(t, 1, filtered.Total)
	assert.Equal(t, last.Items, filtered.Items)
	assert.Equal(t, 502, get(adminOrders+"?site_id=0", f.root).Total)
	self := get(selfOrders+"?order_type=consumption&user_id="+fmt.Sprint(other.Id), buyerToken)
	assert.Zero(t, self.Total)
	assert.Equal(t, last.Items, get(selfOrders+"?order_type=consumption&keyword=unified-000", buyerToken).Items)

	const detailQuery = "/request:unified-000?order_type=consumption"
	type detail struct {
		Order row `json:"order"`
	}
	for _, entry := range []struct{ path, token string }{{adminOrders, f.root}, {selfOrders, buyerToken}} {
		response := adtyJSON(t, f.engine, http.MethodGet, entry.path+detailQuery, "", entry.token)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, last.Items[0], decodeAdtyData[detail](t, response.Body).Order)
	}
	assert.Equal(t, http.StatusNotFound, adtyJSON(t, f.engine, http.MethodGet, selfOrders+detailQuery, "", otherToken).Code)
	assert.Equal(t, http.StatusForbidden, adtyJSON(t, f.engine, http.MethodGet, adminOrders+detailQuery, "", f.token).Code)
}

func TestUnifiedOrdersSummaryAcceptsUnifiedFiltersPG(t *testing.T) {
	f := newAdtyGuardFixture(t)
	seedRouterOrder(t, f.db, 1, "unified-summary-topup", common.TopUpStatusSuccess, 1)
	seedRouterOrder(t, f.db, 1, "unified-summary-voucher", common.TopUpStatusSuccess, 1)
	require.NoError(t, f.db.Model(&model.TopUp{}).Where("trade_no = ?", "unified-summary-voucher").Update("payment_provider", model.AdtyVoucherProvider).Error)
	for _, spec := range []struct {
		filter string
		count int
	}{{"?status=settled", 0}, {"?order_type=topup&status=settled", 0}, {"?order_type=consumption&status=settled", 0}, {"?order_type=topup", 1}, {"?order_type=voucher", 1}} {
		response := adtyJSON(t, f.engine, http.MethodGet, "/api/user/admin/finance/recharge-audit/summary"+spec.filter, "", f.root)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		data := decodeAdtyData[struct { Orders int `json:"orders"` }](t, response.Body)
		assert.Equal(t, spec.count, data.Orders, spec.filter)
	}
}

func TestUnifiedOrdersKeepCategoriesWithinOriginalSitePG(t *testing.T) {
	f := newOwnerFixture(t)
	buyer := f.customer(t, f.site, "unified-site-buyer", "unified-buyer@example.com")
	foreign := f.customer(t, f.other, "unified-foreign-buyer", "unified-foreign@example.com")
	for _, spec := range []struct {
		user, site int
		suffix     string
	}{{buyer.Id, f.site.Id, "mine"}, {foreign.Id, f.other.Id, "foreign"}} {
		trade := "unified-" + spec.suffix
		seedRouterOrder(t, f.db, spec.user, trade, common.TopUpStatusPending, 1)
		require.NoError(t, f.db.Model(&model.AdtyTopupOrder{}).Where("trade_no = ?", trade).Update("site_id", spec.site).Error)
		voucher := "voucher-" + spec.suffix
		seedRouterOrder(t, f.db, spec.user, voucher, common.TopUpStatusSuccess, 1)
		require.NoError(t, f.db.Model(&model.TopUp{}).Where("trade_no = ?", voucher).Update("payment_provider", model.AdtyVoucherProvider).Error)
		require.NoError(t, f.db.Model(&model.AdtyTopupOrder{}).Where("trade_no = ?", voucher).Update("site_id", spec.site).Error)
		require.NoError(t, f.db.Create(&model.BillingOperation{OperationKey: "request:" + spec.suffix, UserId: spec.user, SiteId: spec.site,
			Kind: "request", State: model.BillingSettled, AppliedQuota: 500_000}).Error)
		require.NoError(t, f.db.Create(&model.AdtyManualRefund{RefundId: "refund-" + spec.suffix, TradeNo: trade,
			UserId: spec.user, SiteId: spec.site, Status: "submitted", RequestQuota: 500_000, RequestUSD: decimal.NewFromInt(1), Reason: "private-admin-reason"}).Error)
		require.NoError(t, f.db.Create(&model.AdtySiteWithdrawal{SiteId: spec.site, OwnerUserId: spec.user, Status: "submitted",
			AmountUSD: decimal.NewFromInt(1), Method: "usdt", ProofNote: "private-proof", IdempotencyKey: "unified-wd-" + spec.suffix}).Error)
		require.NoError(t, f.db.Create(&model.AdtySiteInviteCommission{SiteId: spec.site, InviterId: spec.user, InviteeId: spec.user, TradeNo: trade,
			Status: "pending", FaceUSD: decimal.NewFromInt(1), Rate: decimal.RequireFromString("0.1"), AmountUSD: decimal.RequireFromString("0.1")}).Error)
		require.NoError(t, f.db.Create(&model.AdtyStaffBalanceRequest{SiteId: spec.site, StaffUserId: f.owner.Id, CustomerUserId: spec.user,
			Direction: "increase", Status: "pending", AmountUSD: decimal.NewFromInt(1)}).Error)
	}
	require.NoError(t, model.AdtySetUserSiteTx(f.db, buyer.Id, f.other.Id))
	type item struct {
		OrderType  string `json:"order_type"`
		TradeNo    string `json:"trade_no"`
		UserID     int    `json:"user_id"`
		SourceType string `json:"source_type"`
	}
	type page struct {
		Total int
		Items []item
	}
	for _, kind := range []string{"topup", "voucher", "consumption", "refund", "withdrawal", "commission", "staff_adjustment"} {
		response := f.call(t, http.MethodGet, "/api/adty/site/orders?order_type="+kind, "", "")
		list := decodeAdtyData[page](t, response.Body)
		require.Equal(t, 1, list.Total, kind+": "+response.Body.String())
		require.Len(t, list.Items, 1)
		assert.Equal(t, kind, list.Items[0].OrderType)
		assert.Equal(t, buyer.Id, list.Items[0].UserID)
		assert.NotContains(t, response.Body.String(), "private-")
		path := "/api/adty/site/orders/" + url.PathEscape(list.Items[0].TradeNo) + "?order_type=" + kind + "&source_type=" + list.Items[0].SourceType
		detail := f.call(t, http.MethodGet, path, "", "")
		require.Equal(t, http.StatusOK, detail.Code, kind+": "+detail.Body.String())
		assert.Contains(t, detail.Body.String(), `"success":true`, kind+": "+detail.Body.String())
		assert.NotContains(t, detail.Body.String(), "private-")
		assert.Equal(t, http.StatusNotFound, f.call(t, http.MethodGet, path+"&site_id="+fmt.Sprint(f.other.Id), "", "").Code)
	}
	all := decodeAdtyData[page](t, f.call(t, http.MethodGet, "/api/adty/site/orders", "", "").Body)
	assert.Equal(t, 7, all.Total)
	assert.Zero(t, decodeAdtyData[page](t, f.call(t, http.MethodGet, "/api/adty/site/orders?site_id="+fmt.Sprint(f.other.Id), "", "").Body).Total)
}

func TestUnifiedOrdersTraceLinkedFactsPG(t *testing.T) {
	f := newOwnerFixture(t)
	buyer := f.customer(t, f.site, "trace-unified", "trace-unified@example.com")
	const trade = "trace-unified-topup"
	seedRouterOrder(t, f.db, buyer.Id, trade, common.TopUpStatusSuccess, 1001)
	require.NoError(t, f.db.Model(&model.AdtyTopupOrder{}).Where("trade_no = ?", trade).Update("site_id", f.site.Id).Error)
	require.NoError(t, f.db.Create(&model.AdtyFundBatch{UserId: buyer.Id, SiteId: &f.site.Id, Source: trade, Kind: "principal", Original: 500000, Remaining: 400000}).Error)
	require.NoError(t, f.db.Create(&model.AdtyFundBatch{UserId: buyer.Id, SiteId: &f.site.Id, Source: "unrelated-secret", Kind: "principal", Original: 500000, Remaining: 500000}).Error)
	refund := model.AdtyManualRefund{RefundId: "trace-unified-refund", TradeNo: trade, UserId: buyer.Id, SiteId: f.site.Id,
		Status: "completed", RequestUSD: decimal.NewFromInt(1), RequestQuota: 500000, ReclaimIdemKey: "trace-unified-reclaim", PayoutVoucher: "private-payout-voucher"}
	require.NoError(t, f.db.Create(&refund).Error)
	key := refund.ReclaimIdemKey
	require.NoError(t, f.db.Create(&model.AdtyBalanceLedger{UserId: buyer.Id, RefType: "topup", RefId: trade, Type: "refund", DeltaQuota: -100000,
		IdempotencyKey: &key, Reason: "private-refund-reason"}).Error)
	otherKey := "trace-unrelated-reclaim"
	require.NoError(t, f.db.Create(&model.AdtyBalanceLedger{UserId: buyer.Id, RefType: "topup", RefId: trade, Type: "refund", DeltaQuota: -200000,
		IdempotencyKey: &otherKey, Reason: "unrelated-secret"}).Error)
	type entry struct {
		Kind, Reference, Note string
		DeltaQuota            int64 `json:"delta_quota"`
	}
	type trace struct {
		Total    int
		Items    []entry
		Sections []string
	}
	get := func(path string) trace {
		t.Helper()
		response := f.call(t, http.MethodGet, path, "", "")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), "unrelated-secret")
		assert.NotContains(t, response.Body.String(), "private-")
		return decodeAdtyData[trace](t, response.Body)
	}
	funds := get("/api/adty/site/orders/" + trade + "?order_type=topup&section=funds")
	require.Equal(t, 1, funds.Total)
	require.Len(t, funds.Items, 1)
	assert.Equal(t, int64(400000), funds.Items[0].DeltaQuota)
	refunds := get("/api/adty/site/orders/" + trade + "?order_type=topup&section=refunds")
	require.Equal(t, 1, refunds.Total)
	assert.Equal(t, refund.RefundId, refunds.Items[0].Reference)
	wallet := get("/api/adty/site/orders/" + refund.RefundId + "?order_type=refund&section=wallet")
	require.Equal(t, 1, wallet.Total)
	assert.Equal(t, int64(-100000), wallet.Items[0].DeltaQuota)
}

func TestUnifiedOrdersStaffPermissionIntersectionPG(t *testing.T) {
	f := newOwnerFixture(t)
	buyer := f.customer(t, f.site, "orders-staff-buyer", "orders-staff@example.com")
	foreign := f.customer(t, f.other, "orders-staff-foreign", "orders-foreign@example.com")
	require.NoError(t, f.db.Model(&model.AdtySiteCustomer{}).Where("user_id = ?", buyer.Id).Update("joined_at", 1000).Error)
	for _, at := range []int64{999, 1001} {
		trade := fmt.Sprintf("staff-orders-%d", at)
		seedRouterOrder(t, f.db, buyer.Id, trade, common.TopUpStatusSuccess, at)
		require.NoError(t, f.db.Model(&model.TopUp{}).Where("trade_no = ?", trade).Update("create_time", at).Error)
		require.NoError(t, f.db.Model(&model.AdtyTopupOrder{}).Where("trade_no = ?", trade).Updates(map[string]any{"site_id": f.site.Id, "paid_tx_hash": "private-payment-evidence"}).Error)
		require.NoError(t, f.db.Create(&model.BillingOperation{OperationKey: fmt.Sprintf("request:staff-%d", at), UserId: buyer.Id, SiteId: f.site.Id, Kind: "request", State: model.BillingSettled, AppliedQuota: 500000, CreatedAt: at}).Error)
	}
	grants := []string{"customers.read", "customers.topups"}
	member, err := service.CreateAdtyStaff(f.owner.Id, f.site.Id, service.AdtyStaffInput{Username: "orders-employee", DisplayName: "Order reader", Password: "orders-employee-password-42", Role: "finance", Permissions: &grants})
	require.NoError(t, err)
	login, err := service.CreateLoginSessionOnSite(member.UserId, f.site.Id, "password", "127.0.0.1", "orders-staff")
	require.NoError(t, err)
	path := fmt.Sprintf("/api/adty/staff/customers/%d/orders", buyer.Id)
	list := f.siteJSON(t, http.MethodGet, path, "", login.AccessToken)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	assert.Contains(t, list.Body.String(), `"total":1`)
	assert.Contains(t, list.Body.String(), "staff-orders-1001")
	for _, hidden := range []string{"staff-orders-999", "request:staff", "private-payment", "paid_tx_hash", "channel_id"} {
		assert.NotContains(t, list.Body.String(), hidden)
	}
	assert.Equal(t, http.StatusForbidden, f.siteJSON(t, http.MethodGet, path+"?order_type=consumption", "", login.AccessToken).Code)
	assert.Equal(t, http.StatusNotFound, f.siteJSON(t, http.MethodGet, fmt.Sprintf("/api/adty/staff/customers/%d/orders", foreign.Id), "", login.AccessToken).Code)
	detail := f.siteJSON(t, http.MethodGet, path+"/staff-orders-1001?order_type=topup", "", login.AccessToken)
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	assert.NotContains(t, detail.Body.String(), "private-payment")
	assert.Equal(t, http.StatusForbidden, f.siteJSON(t, http.MethodGet, path+"?channel_id=1", "", login.AccessToken).Code)
	assert.Equal(t, http.StatusNotFound, f.siteJSON(t, http.MethodGet, path+"/staff-orders-999?order_type=topup", "", login.AccessToken).Code)
	assert.Equal(t, http.StatusForbidden, f.siteJSON(t, http.MethodGet, path+"?order_type=refund", "", login.AccessToken).Code)
	var readAudits int64
	require.NoError(t, f.db.Model(&model.AdtySiteAudit{}).Where("operator_id = ? AND action = ?", member.UserId, "staff.orders.read").Count(&readAudits).Error)
	assert.Equal(t, int64(2), readAudits)
	require.NoError(t, f.db.Model(&model.AdtySiteStaff{}).Where("id = ?", member.Id).Update("permissions", `["customers.read","customers.usage"]`).Error)
	usage := f.siteJSON(t, http.MethodGet, path+"?order_type=consumption", "", login.AccessToken)
	require.Equal(t, http.StatusOK, usage.Code, usage.Body.String())
	assert.Contains(t, usage.Body.String(), `"total":1`)
	assert.Contains(t, usage.Body.String(), "request:staff-1001")
	assert.Equal(t, http.StatusForbidden, f.siteJSON(t, http.MethodGet, path+"?order_type=topup", "", login.AccessToken).Code)
	require.NoError(t, f.db.Model(&model.AdtySite{}).Where("id = ?", f.site.Id).Update("visibility", `{"customer_usage":"off"}`).Error)
	assert.Equal(t, http.StatusForbidden, f.siteJSON(t, http.MethodGet, path+"?order_type=consumption", "", login.AccessToken).Code)
}

func TestUnifiedOrdersWithdrawalAndCommissionLinksPG(t *testing.T) {
	f := newOwnerFixture(t)
	root := adtySessionToken(t, f.db, "unified-trace-root", common.RoleRootUser)
	withdrawal := model.AdtySiteWithdrawal{SiteId: f.site.Id, OwnerUserId: f.owner.Id, AmountUSD: decimal.NewFromInt(1),
		Status: model.AdtySiteWithdrawPending, IdempotencyKey: "private-withdrawal-key", ProofNote: "private-withdrawal-proof"}
	require.NoError(t, f.db.Create(&withdrawal).Error)
	require.NoError(t, f.db.Create(&model.AdtySiteWalletLedger{SiteId: f.site.Id, WalletKind: "earnings", Type: "earning_withdraw_freeze",
		RefType: "site_withdraw", RefId: withdrawal.IdempotencyKey, IdempotencyKey: "trace-wallet-row", Reason: "private-wallet-reason"}).Error)
	require.NoError(t, f.db.Create(&model.AdtySiteWalletLedger{SiteId: f.site.Id, WalletKind: "earnings", Type: "earning_withdraw_freeze",
		RefType: "site_withdraw", RefId: "unrelated-private-key", IdempotencyKey: "trace-unrelated-row"}).Error)
	commission := model.AdtySiteInviteCommission{SiteId: f.site.Id, InviterId: f.owner.Id, InviteeId: f.owner.Id, TradeNo: "linked-source-topup",
		AmountUSD: decimal.NewFromInt(1), Status: model.AdtySiteCommissionCovered}
	require.NoError(t, f.db.Create(&commission).Error)
	inviteWithdrawal := model.AdtySiteInviteWithdrawal{SiteId: f.site.Id, UserId: f.owner.Id, AmountUSD: decimal.NewFromInt(1),
		Status: model.AdtySiteInviteWithdrawPending, IdempotencyKey: "private-invite-key"}
	require.NoError(t, f.db.Create(&inviteWithdrawal).Error)
	require.NoError(t, f.db.Create(&model.AdtySiteInviteWithdrawalItem{WithdrawalId: inviteWithdrawal.Id, CommissionId: commission.Id, AmountUSD: decimal.NewFromInt(1)}).Error)
	type trace struct {
		Total int
		Items []struct{ Reference string }
	}
	const base = "/api/user/admin/finance/recharge-audit/"
	response := adtyJSON(t, f.engine, http.MethodGet, fmt.Sprintf("%sWD-SITE-%d?order_type=withdrawal&section=site_wallet", base, withdrawal.Id), "", root)
	data := decodeAdtyData[trace](t, response.Body)
	require.Equal(t, 1, data.Total, response.Body.String())
	assert.NotContains(t, response.Body.String(), "private-")
	response = f.call(t, http.MethodGet, fmt.Sprintf("/api/adty/site/orders/WD-INV-%d?order_type=withdrawal&section=commissions", inviteWithdrawal.Id), "", "")
	data = decodeAdtyData[trace](t, response.Body)
	require.Equal(t, 1, data.Total, response.Body.String())
	require.Len(t, data.Items, 1)
	assert.Equal(t, "linked-source-topup", data.Items[0].Reference)
	assert.NotContains(t, response.Body.String(), "private-")
}

func TestOwnerManualRefundUsesOriginalSiteOrderPG(t *testing.T) {
	f := newOwnerFixture(t)
	buyer := f.customer(t, f.site, "owner-refund-buyer", "owner-refund@example.com")
	foreignBuyer := f.customer(t, f.other, "owner-refund-other", "owner-refund-other@example.com")
	const trade, foreignTrade, secondTrade = "owner-refund-original", "owner-refund-foreign", "owner-refund-second"
	seed := func(order string, user, siteID int) {
		t.Helper()
		const provider, method = model.PaymentProviderAdtyBEpusdt, "usdt.trc20"
		require.NoError(t, model.CreateAdtyTopUp(
			&model.TopUp{TradeNo: order, UserId: user, Amount: 2, Money: 2, Status: common.TopUpStatusPending, PaymentProvider: provider, PaymentMethod: method},
			&model.AdtyTopupOrder{TradeNo: order, UserId: user, SiteId: siteID, Provider: provider, Method: method,
				FaceUSD: decimal.NewFromInt(2), CreditQuota: 1_000_000, PayAmount: decimal.NewFromInt(2),
				PayCurrency: "USDT", PriceSnapshot: decimal.NewFromInt(1)},
		))
		replay, err := model.CompleteAdtyTopUp(order, model.AdtyPaymentFacts{Provider: provider, Method: method,
			PaidAmount: decimal.NewFromInt(2), FiatAmount: decimal.NewFromInt(2), PaidCurrency: "USDT",
			TxHash: "payment-" + order, Source: model.AdtyPaymentSourceCallback}, "")
		require.NoError(t, err)
		require.False(t, replay)
	}
	seed(trade, buyer.Id, f.site.Id)
	seed(secondTrade, buyer.Id, f.site.Id)
	seed(foreignTrade, foreignBuyer.Id, f.other.Id)
	const selfUseTrade = "owner-refund-self-use"
	require.NoError(t, model.CreateAdtyTopUp(
		&model.TopUp{TradeNo: selfUseTrade, UserId: buyer.Id, Amount: 2, Money: 2, Status: common.TopUpStatusPending, PaymentProvider: model.PaymentProviderAdtyBEpusdt},
		&model.AdtyTopupOrder{TradeNo: selfUseTrade, UserId: buyer.Id, SiteId: f.site.Id, Business: model.AdtyBusinessOwnerSelfUseTopup,
			Provider: model.PaymentProviderAdtyBEpusdt, FaceUSD: decimal.NewFromInt(2), CreditQuota: 1_000_000},
	))
	assert.Equal(t, http.StatusForbidden, f.call(t, http.MethodGet, "/api/adty/site/orders/"+selfUseTrade+"/refund-preview", "", "").Code)
	_, selfUseErr := model.ApplyAdtyTopUpManualRefund(selfUseTrade, "unused", f.owner.Id, "self-use-forbidden", decimal.NewFromInt(1), f.site.Id)
	require.Error(t, selfUseErr, "the transactional owner guard also excludes self-use orders")
	require.NoError(t, model.AdtySetUserSiteTx(f.db, buyer.Id, f.other.Id))
	const ownerOrders = "/api/adty/site/orders/"
	const original = ownerOrders + trade
	response := f.call(t, http.MethodGet, original+"/refund-preview?refund_usd=1", "", "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	preview := decodeAdtyData[model.AdtyTopUpRefundResult](t, response.Body)
	require.NotNil(t, preview.Facts)
	assert.Equal(t, f.site.Id, preview.Facts.SiteId)
	assert.Equal(t, "1", preview.Facts.ForeignReference)
	assert.Equal(t, "2", preview.Facts.PrincipalRemainingUSD)
	assert.Nil(t, preview.Ledger)
	assert.Equal(t, http.StatusNotFound, f.call(t, http.MethodGet, ownerOrders+foreignTrade+"/refund-preview", "", "").Code)
	plain := adtySessionToken(t, f.db, "owner-refund-non-owner", common.RoleCommonUser)
	assert.Equal(t, http.StatusForbidden, adtyJSON(t, f.engine, http.MethodGet, original+"/refunds", "", plain).Code)
	readOnlyToken := createAdtyScopedToken(t, f.owner.Id, "wallet:read")
	assert.Equal(t, http.StatusForbidden, adtyJSON(t, f.engine, http.MethodPost, original+"/refund", "{}", readOnlyToken).Code)

	const reason = "return one unused dollar"
	const refundID = "owner-partial-refund"
	applyBody := func(reason, amount string) string {
		return fmt.Sprintf(`{"reason":%q,"refund_usd":%q,"idempotency_key":%q}`, reason, amount, refundID)
	}
	issueProof := func(op service.VerificationOperation, scope string) string {
		op.Scope = scope
		return financeUserProof(t, f.owner, service.VerificationMethodPassword, op)
	}
	newApplyProof := func() string {
		return issueProof(service.AdtyTopUpRefundOperation(trade, reason, "1", nil), "site.order.refund")
	}
	for _, tc := range []struct{ path, reason, amount string }{
		{original, reason, "1.5"},
		{original, "different reason", "1"},
		{ownerOrders + secondTrade, reason, "1"},
	} {
		res := f.call(t, http.MethodPost, tc.path+"/refund", applyBody(tc.reason, tc.amount), newApplyProof())
		assert.Equal(t, http.StatusForbidden, res.Code)
		assert.Contains(t, res.Body.String(), "SECURITY_PROOF_CONTEXT_MISMATCH")
	}
	proof := newApplyProof()
	res := f.call(t, http.MethodPost, original+"/refund", applyBody(reason, "1"), proof)
	applied := decodeAdtyData[model.AdtyManualRefundResult](t, res.Body)
	require.NotNil(t, applied.Refund)
	require.NotNil(t, applied.Occupancy)
	assert.Equal(t, "submitted", applied.Refund.Status)
	assert.Equal(t, refundID, applied.Refund.RefundId)
	assert.Equal(t, int64(500_000), applied.Occupancy.InProgressQuota)
	assert.Zero(t, applied.Occupancy.CompletedQuota)
	res = f.call(t, http.MethodPost, original+"/refund", applyBody(reason, "1"), proof)
	assert.Equal(t, http.StatusForbidden, res.Code)
	assert.Contains(t, res.Body.String(), "SECURITY_PROOF_CONSUMED")

	stepPath := original + "/refunds/" + refundID + "/"
	step := func(stepName, body, note string, payout service.AdtyManualRefundStepPayout) model.AdtyManualRefundResult {
		t.Helper()
		op := service.AdtyTopUpManualRefundStepOperation(trade, refundID, stepName, note, payout)
		res := f.call(t, http.MethodPost, stepPath+stepName, body, issueProof(op, "site.order.refund_step"))
		require.Equal(t, http.StatusOK, res.Code, res.Body.String())
		return decodeAdtyData[model.AdtyManualRefundResult](t, res.Body)
	}
	res = f.call(t, http.MethodPost, stepPath+"approve", "{}", "")
	assert.Equal(t, http.StatusForbidden, res.Code)
	assert.Contains(t, res.Body.String(), "SECURITY_PROOF_REQUIRED")
	approved := step("approve", "{}", "", service.AdtyManualRefundStepPayout{})
	assert.Equal(t, "approved", approved.Refund.Status)
	reclaimed := step("reclaim", "{}", "", service.AdtyManualRefundStepPayout{})
	assert.Equal(t, int64(500_000), reclaimed.Refund.DeductedPrincipalQuota)
	assert.Nil(t, reclaimed.Ledger)
	for _, name := range []string{"bonus", "commission"} {
		assert.Equal(t, "approved", step(name, "{}", "", service.AdtyManualRefundStepPayout{}).Refund.Status)
	}
	unknown := step("payout", `{"status":"unknown","idempotency_key":"bank-pending"}`, "", service.AdtyManualRefundStepPayout{Status: "unknown"})
	assert.Equal(t, int64(500_000), unknown.Occupancy.UnknownQuota)
	assert.Zero(t, unknown.Occupancy.InProgressQuota)
	paidAt := common.GetTimestamp()
	paid := step("payout", fmt.Sprintf(`{"status":"success","currency":"CNY","amount":"7","paid_at":%d,"voucher":"offline-123","idempotency_key":"bank-confirmed"}`, paidAt), "",
		service.AdtyManualRefundStepPayout{Status: "success", Currency: "CNY", Amount: "7", PaidAt: paidAt, Voucher: "offline-123"})
	assert.Equal(t, "approved", paid.Refund.Status)
	assert.Zero(t, paid.Occupancy.CompletedQuota)
	assert.Equal(t, "CNY", paid.Refund.PayoutCurrency)
	assert.Equal(t, "7", paid.Refund.PayoutAmount.String())
	assert.Equal(t, "offline-123", paid.Refund.PayoutVoucher)
	completed := step("complete", "{}", "", service.AdtyManualRefundStepPayout{})
	assert.Equal(t, "completed", completed.Refund.Status)
	assert.Equal(t, int64(500_000), completed.Occupancy.CompletedQuota)
	assert.Equal(t, int64(500_000), completed.Occupancy.RemainingQuota)
	assert.Zero(t, completed.Occupancy.InProgressQuota+completed.Occupancy.UnknownQuota)
	var saved model.AdtyManualRefund
	require.NoError(t, f.db.Where("refund_id = ?", refundID).First(&saved).Error)
	assert.Equal(t, f.owner.Id, saved.ApplyOperatorId)
	assert.Equal(t, f.owner.Id, saved.PayoutOperatorId)
	assert.Equal(t, f.owner.Id, saved.CompleteOperatorId)
	var after model.User
	require.NoError(t, f.db.First(&after, buyer.Id).Error)
	assert.Equal(t, 2_500_000, after.Quota)
}

func TestStaffAdjustmentRequiresConcreteProvenancePG(t *testing.T) {
	f := newOwnerFixture(t)
	require.NoError(t, f.db.AutoMigrate(&model.AuditIntent{}))
	buyer := f.customer(t, f.site, "staff-provenance-buyer", "staff-provenance@example.com")
	staff, err := service.CreateAdtyStaff(f.owner.Id, f.site.Id, service.AdtyStaffInput{Username: "provenance-staff",
		DisplayName: "Finance", Password: "employee-password-42", Role: "finance"})
	require.NoError(t, err)
	login, err := service.CreateLoginSessionOnSite(staff.UserId, f.site.Id, "password", "127.0.0.1", "staff-provenance-test")
	require.NoError(t, err)
	body := fmt.Sprintf(`{"customer_id":%d,"direction":"increase","amount_usd":"1","idempotency_key":"staff-provenance-missing"}`, buyer.Id)
	response := f.siteJSON(t, http.MethodPost, "/api/adty/staff/balance-requests", body, login.AccessToken)
	require.Contains(t, response.Body.String(), `"success":false`, response.Body.String())
	var count int64
	require.NoError(t, f.db.Model(&model.AdtyStaffBalanceRequest{}).Where("staff_user_id = ?", staff.UserId).Count(&count).Error)
	require.Zero(t, count)
	const orderRef = "staff-provenance-order"
	require.NoError(t, model.CreateAdtyTopUp(
		&model.TopUp{TradeNo: orderRef, UserId: buyer.Id, Amount: 2, Money: 2, Status: common.TopUpStatusPending, PaymentProvider: model.PaymentProviderAdtyBEpusdt},
		&model.AdtyTopupOrder{TradeNo: orderRef, UserId: buyer.Id, SiteId: f.site.Id, Provider: model.PaymentProviderAdtyBEpusdt,
			FaceUSD: decimal.NewFromInt(2), CreditQuota: 1_000_000, PayAmount: decimal.NewFromInt(2), PayCurrency: "USDT"},
	))
	_, err = model.CompleteAdtyTopUp(orderRef, model.AdtyPaymentFacts{Provider: model.PaymentProviderAdtyBEpusdt,
		PaidAmount: decimal.NewFromInt(2), FiatAmount: decimal.NewFromInt(2), PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback}, "")
	require.NoError(t, err)
	makeBody := func(key, reason, order, source, direction, kind, batch string) string {
		return fmt.Sprintf(`{"customer_id":%d,"direction":%q,"amount_usd":"1","idempotency_key":%q,"reason":%q,"order_ref":%q,"funding_source":%q,"batch_kind":%q,"batch_source":%q}`, buyer.Id, direction, key, reason, order, source, kind, batch)
	}
	for _, invalid := range []string{
		makeBody("missing-reason", "", orderRef, "site_earnings", "increase", "", ""),
		makeBody("missing-order", "correction", "", "site_earnings", "increase", "", ""),
		makeBody("fake-order", "correction", "not-an-order", "site_earnings", "increase", "", ""),
		makeBody("operating-not-source", "correction", orderRef, "operating", "increase", "", ""),
		makeBody("fifo-not-source", "correction", orderRef, "fifo_batches", "decrease", "principal", orderRef),
	} {
		rejected := f.siteJSON(t, http.MethodPost, "/api/adty/staff/balance-requests", invalid, login.AccessToken)
		require.Contains(t, rejected.Body.String(), `"success":false`, rejected.Body.String())
	}
	valid := makeBody("staff-provenance-valid", "correct original order", orderRef, "site_earnings", "increase", "", "")
	created := f.siteJSON(t, http.MethodPost, "/api/adty/staff/balance-requests", valid, login.AccessToken)
	request := decodeAdtyData[model.AdtyStaffBalanceRequest](t, created.Body)
	require.Equal(t, "correct original order", request.Reason)
	require.Equal(t, orderRef, request.OrderRef)
	require.Equal(t, staff.UserId, request.StaffUserId)
	replayed := decodeAdtyData[model.AdtyStaffBalanceRequest](t, f.siteJSON(t, http.MethodPost, "/api/adty/staff/balance-requests", valid, login.AccessToken).Body)
	require.Equal(t, request.Id, replayed.Id)
	conflict := f.siteJSON(t, http.MethodPost, "/api/adty/staff/balance-requests", strings.Replace(valid, "correct original order", "changed reason", 1), login.AccessToken)
	require.Contains(t, conflict.Body.String(), `"success":false`)
	_, err = service.ApplyAdtySiteWalletChange(f.site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(2),
		service.AdtySiteWalletChange{Type: "test", RefType: "test", RefID: "staff-source-seed", IdempotencyKey: "staff-source-seed"})
	require.NoError(t, err)
	var before model.User
	require.NoError(t, f.db.First(&before, buyer.Id).Error)
	require.Error(t, service.ReviewAdtyStaffBalanceRequest(staff.UserId, "member", request.Id, true))
	require.NoError(t, service.ReviewAdtyStaffBalanceRequest(f.owner.Id, "owner", request.Id, true))
	var approved model.AdtyStaffBalanceRequest
	require.NoError(t, f.db.First(&approved, request.Id).Error)
	require.EqualValues(t, before.Quota+500_000, approved.AfterQuota)
	require.EqualValues(t, before.Quota, approved.BeforeQuota)
	require.Positive(t, approved.LedgerId)
	var ledger model.AdtyBalanceLedger
	require.NoError(t, f.db.First(&ledger, approved.LedgerId).Error)
	require.Equal(t, "staff_balance", ledger.RefType)
	require.Equal(t, fmt.Sprintf("%d", request.Id), ledger.RefId)
	require.Equal(t, request.Reason, ledger.Reason)
	require.Error(t, service.ReviewAdtyStaffBalanceRequest(f.owner.Id, "owner", request.Id, true))
	decreaseBody := makeBody("staff-provenance-decrease", "recover original principal", orderRef, "specified_batch", "decrease", "principal", orderRef)
	decrease := decodeAdtyData[model.AdtyStaffBalanceRequest](t, f.siteJSON(t, http.MethodPost, "/api/adty/staff/balance-requests", decreaseBody, login.AccessToken).Body)
	var originalBatch model.AdtyFundBatch
	require.NoError(t, f.db.Where("user_id = ? AND source = ? AND kind = ?", buyer.Id, orderRef, "principal").First(&originalBatch).Error)
	require.NoError(t, f.db.Create(&model.AdtyFundAllocation{UserId: buyer.Id, BatchId: originalBatch.Id, OperationId: "staff-source-held", Held: 750_000}).Error)
	require.ErrorIs(t, service.ReviewAdtyStaffBalanceRequest(f.owner.Id, "owner", decrease.Id, true), model.ErrAdtyFundBatchShortfall)
	var untouched model.AdtyStaffBalanceRequest
	require.NoError(t, f.db.First(&untouched, decrease.Id).Error)
	require.Equal(t, model.AdtyStaffBalancePending, untouched.Status)
	require.NoError(t, f.db.Model(&model.AdtyFundAllocation{}).Where("operation_id = ?", "staff-source-held").Update("settled", 750_000).Error)
	require.NoError(t, service.ReviewAdtyStaffBalanceRequest(f.owner.Id, "owner", decrease.Id, true))
	require.NoError(t, f.db.First(&originalBatch, originalBatch.Id).Error)
	require.EqualValues(t, 500_000, originalBatch.Remaining)
	var final model.User
	require.NoError(t, f.db.First(&final, buyer.Id).Error)
	require.Equal(t, before.Quota, final.Quota)
}

func TestDirectAdjustmentRequiresOriginalFinancialReferencePG(t *testing.T) {
	f := newAdtyGuardFixture(t)
	require.NoError(t, f.db.AutoMigrate(&model.AdtyFinancialAdjustment{}, &model.AuditIntent{}))
	adtySessionToken(t, f.db, "adjustment-reference-buyer", common.RoleCommonUser)
	var buyer model.User
	require.NoError(t, f.db.Where("username = ?", "adjustment-reference-buyer").First(&buyer).Error)
	body := fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"add","value":500000,"reason":"correct original order","idempotency_key":"adjustment-reference-missing"}`, buyer.Id)
	response := adtyJSON(t, f.engine, http.MethodPost, "/api/user/manage", body, f.root)
	require.Contains(t, response.Body.String(), `"success":false`, response.Body.String())
	var after model.User
	require.NoError(t, f.db.First(&after, buyer.Id).Error)
	require.Equal(t, buyer.Quota, after.Quota)
	const orderRef = "adjustment-reference-original"
	seedRouterOrder(t, f.db, buyer.Id, orderRef, common.TopUpStatusPending, common.GetTimestamp()+900)
	_, err := model.CompleteAdtyTopUp(orderRef, model.AdtyPaymentFacts{Provider: model.PaymentProviderAdtyBEpusdt, Source: model.AdtyPaymentSourceManual}, "verified original")
	require.NoError(t, err)
	var root model.User
	require.NoError(t, f.db.Where("username = ?", "guard-root").First(&root).Error)
	makeBody := func(key, order, mode, source, batch string) string {
		kind := ""
		if batch != "" { kind = "principal" }
		return fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":%q,"value":500000,"reason":"correct original order","idempotency_key":%q,"order_ref":%q,"funding_source":%q,"batch_kind":%q,"batch_source":%q,"approver_id":99999,"executor_id":99999,"site_id":99999}`, buyer.Id, mode, key, order, source, kind, batch)
	}
	invalid := adtyJSON(t, f.engine, http.MethodPost, "/api/user/manage", makeBody("bad-original", "nonexistent", "add", "platform_correction", ""), f.root)
	require.Contains(t, invalid.Body.String(), `"success":false`)
	validBody := makeBody("adjustment-reference-valid", orderRef, "add", "platform_correction", "")
	valid := adtyJSON(t, f.engine, http.MethodPost, "/api/user/manage", validBody, f.root)
	require.Contains(t, valid.Body.String(), `"success":true`, valid.Body.String())
	var adjustment model.AdtyFinancialAdjustment
	require.NoError(t, f.db.Where("idempotency_key = ?", "adjustment-reference-valid").First(&adjustment).Error)
	require.Equal(t, orderRef, adjustment.OrderRef)
	require.Equal(t, root.Id, adjustment.ApproverId)
	require.Equal(t, root.Id, adjustment.ExecutorId)
	require.Zero(t, adjustment.SiteId)
	require.EqualValues(t, 500000, adjustment.BalanceAfter-adjustment.BalanceBefore)
	require.Positive(t, adjustment.LedgerId)
	var entry model.AdtyBalanceLedger
	require.NoError(t, f.db.First(&entry, adjustment.LedgerId).Error)
	require.Equal(t, "financial_adjustment", entry.RefType)
	require.Equal(t, fmt.Sprintf("%d", adjustment.Id), entry.RefId)
	require.NotEmpty(t, adjustment.ApprovalRef)
	var intents int64
	require.NoError(t, f.db.Model(&model.AuditIntent{}).Where("payload LIKE ?", "%financial_adjustment%").Count(&intents).Error)
	require.EqualValues(t, 1, intents)
	require.Contains(t, adtyJSON(t, f.engine, http.MethodPost, "/api/user/manage", validBody, f.root).Body.String(), `"success":true`)
	require.Contains(t, adtyJSON(t, f.engine, http.MethodPost, "/api/user/manage", strings.Replace(validBody, "correct original order", "changed reason", 1), f.root).Body.String(), `"success":false`)
	decrease := makeBody("adjustment-reference-decrease", orderRef, "subtract", "specified_batch", orderRef)
	require.Contains(t, adtyJSON(t, f.engine, http.MethodPost, "/api/user/manage", decrease, f.root).Body.String(), `"success":true`)
	var reduced model.AdtyFinancialAdjustment
	require.NoError(t, f.db.Where("idempotency_key = ?", "adjustment-reference-decrease").First(&reduced).Error)
	require.EqualValues(t, -500000, reduced.DeltaQuota)
	require.Equal(t, "decrease", reduced.Direction)
}

func TestExpiringRootGiftRouteRequiresRootBoundProofAndCreditsOnlyGift(t *testing.T) {
	f := newAdtyGuardFixture(t)
	adtySessionToken(t, f.db, "expiry-buyer", common.RoleCommonUser)
	var buyer, root model.User
	require.NoError(t, f.db.Where("username = ?", "expiry-buyer").First(&buyer).Error)
	require.NoError(t, f.db.Where("username = ?", "guard-root").First(&root).Error)
	expires := time.Now().Add(48 * time.Hour).Unix()
	path := fmt.Sprintf("/api/adty/admin/users/%d/expiring-gifts", buyer.Id)
	body := fmt.Sprintf(`{"amount_usd":"2","expires_at":%d,"reason":"trial","idempotency_key":"root-trial-route"}`, expires)
	missingProof := adtyJSON(t, f.engine, http.MethodPost, path, body, f.root)
	require.NotEqual(t, http.StatusNotFound, missingProof.Code, "Root gift route must exist")
	require.Contains(t, missingProof.Body.String(), `"success":false`)
	denied := adtyJSON(t, f.engine, http.MethodPost, path, body, f.token)
	require.Contains(t, denied.Body.String(), `"success":false`)
	context := fmt.Sprintf(`{"user_id":%d,"amount_usd":"2","expires_at":%d,"reason":"trial","idempotency_key":"root-trial-route"}`, buyer.Id, expires)
	op := service.VerificationOperation{Scope: "adty.root_gift.grant", Context: []byte(context)}
	proof := financeUserProof(t, root, service.VerificationMethodRootExempt, op)
	created := adtyJSONWithProof(t, f.engine, http.MethodPost, path, body, f.root, proof)
	require.Contains(t, created.Body.String(), `"success":true`, created.Body.String())
	var stored model.User
	require.NoError(t, f.db.First(&stored, buyer.Id).Error)
	require.Equal(t, buyer.Quota+1_000_000, stored.Quota)
	usedProof := adtyJSONWithProof(t, f.engine, http.MethodPost, path, body, f.root, proof)
	require.Contains(t, usedProof.Body.String(), `"success":false`, "a consumed proof must not authorize another write")
	var count int64
	require.NoError(t, f.db.Model(&model.TopUp{}).Where("user_id = ?", buyer.Id).Count(&count).Error)
	require.Zero(t, count, "Root gifts do not manufacture paid top-ups")
	proof = financeUserProof(t, root, service.VerificationMethodRootExempt, op)
	replay := adtyJSONWithProof(t, f.engine, http.MethodPost, path, body, f.root, proof)
	require.Contains(t, replay.Body.String(), `"success":true`, replay.Body.String())
	require.NoError(t, f.db.First(&stored, buyer.Id).Error)
	require.Equal(t, buyer.Quota+1_000_000, stored.Quota)
	var lots []model.AdtyFundBatch
	require.NoError(t, f.db.Where("user_id = ?", buyer.Id).Find(&lots).Error)
	require.Len(t, lots, 1)
	require.NotNil(t, lots[0].ExpiresAt)
	require.Equal(t, expires, *lots[0].ExpiresAt)
}

func TestExpiringRootGiftRejectsChangedExpiredProofAndAuditFailure(t *testing.T) {
	f := newAdtyGuardFixture(t)
	adtySessionToken(t, f.db, "expiry-proof-buyer", common.RoleCommonUser)
	var buyer, root model.User
	require.NoError(t, f.db.Where("username = ?", "expiry-proof-buyer").First(&buyer).Error)
	require.NoError(t, f.db.Where("username = ?", "guard-root").First(&root).Error)
	expires := time.Now().Add(48 * time.Hour).Unix()
	input := service.AdtyRootGiftContext{UserID: buyer.Id, AmountUSD: "2", ExpiresAt: expires, Reason: "trial", IdempotencyKey: "proof-bound-gift"}
	operation := service.AdtyRootGiftOperation(input)
	path := fmt.Sprintf("/api/adty/admin/users/%d/expiring-gifts", buyer.Id)
	for _, field := range []string{"amount", "expiry", "reason", "key", "target"} {
		t.Run(field, func(t *testing.T) {
			changed := input
			target := path
			switch field {
			case "amount":
				changed.AmountUSD = "3"
			case "expiry":
				changed.ExpiresAt++
			case "reason":
				changed.Reason = "another trial"
			case "key":
				changed.IdempotencyKey = "another-key"
			case "target":
				target = fmt.Sprintf("/api/adty/admin/users/%d/expiring-gifts", root.Id)
			}
			body, err := common.Marshal(changed)
			require.NoError(t, err)
			proof := financeUserProof(t, root, service.VerificationMethodRootExempt, operation)
			response := adtyJSONWithProof(t, f.engine, http.MethodPost, target, string(body), f.root, proof)
			require.Contains(t, response.Body.String(), `"success":false`)
		})
	}
	body, err := common.Marshal(input)
	require.NoError(t, err)
	proof := financeUserProof(t, root, service.VerificationMethodRootExempt, operation)
	require.NoError(t, f.db.Model(&model.AuthFlow{}).Where("purpose = ? AND user_id = ? AND consumed_at IS NULL", model.AuthFlowPurposeSecurityProof, root.Id).Update("expires_at", time.Now().Add(-time.Second)).Error)
	expired := adtyJSONWithProof(t, f.engine, http.MethodPost, path, string(body), f.root, proof)
	require.Contains(t, expired.Body.String(), `"success":false`)

	const callback = "test:root-gift-audit-failure"
	attempted := false
	require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_intents" {
			attempted = true
			_ = tx.AddError(errors.New("audit unavailable"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, f.db.Callback().Create().Remove(callback)) })
	proof = financeUserProof(t, root, service.VerificationMethodRootExempt, operation)
	failed := adtyJSONWithProof(t, f.engine, http.MethodPost, path, string(body), f.root, proof)
	require.Contains(t, failed.Body.String(), `"success":false`)
	require.True(t, attempted, "exercise the durable audit write inside the grant transaction")
	var stored model.User
	require.NoError(t, f.db.First(&stored, buyer.Id).Error)
	require.Equal(t, buyer.Quota, stored.Quota)
	for _, row := range []any{&model.AdtyFundBatch{}, &model.AdtyBalanceLedger{}} {
		var count int64
		require.NoError(t, f.db.Model(row).Where("user_id = ?", buyer.Id).Count(&count).Error)
		require.Zero(t, count, "rejected or rolled-back grants leave no monetary facts")
	}
}

func TestOwnerTopupPaymentRoutesPG(t *testing.T) {
	for _, provider := range []string{model.PaymentProviderAdtyEpay, model.PaymentProviderAdtyBEpusdt} {
		t.Run(provider, func(t *testing.T) {
			f := newOwnerFixture(t)
			setRouterBonusRates(t)
			oldRatio := common.TopupGroupRatio2JSONString()
			require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":0.5}`))
			t.Cleanup(func() { require.NoError(t, common.UpdateTopupGroupRatioByJSONString(oldRatio)) })
			login, err := service.CreateLoginSessionOnSite(f.owner.Id, f.site.Id, "password", "127.0.0.1", "owner-checkout-test")
			require.NoError(t, err)
			mainToken := f.token
			f.token = login.AccessToken
			var channel *model.AdtyEpayChannel
			path, method := "/api/user/bepusdt/pay", "usdt.trc20"
			if provider == model.PaymentProviderAdtyEpay {
				gateway := newFakeEpay(t, epayQR)
				channel = seedRouterEpayChannel(t, "owner-online", gateway.server.URL, model.AdtyEpayOrderModeAPI, 1)
				path, method = "/api/adty/epay/pay", "alipay"
			} else {
				useFakeBEpusdt(t)
			}
			// With only the seeded ordinary scene, neither provider may turn a
			// subsite owner checkout into an ordinary main-wallet order.
			ordinaryBody := fmt.Sprintf(`{"amount":100,"method":%q,"chain":%q}`, method, method)
			for _, business := range []string{"", model.AdtyBusinessWalletTopup, model.AdtyBusinessOwnerSelfUseTopup} {
				request := ordinaryBody
				if business != "" {
					request = fmt.Sprintf(`{"business":%q,`, business) + ordinaryBody[1:]
				}
				require.Contains(t, f.siteJSON(t, http.MethodPost, path, request, f.token).Body.String(), `"success":false`)
			}
			var ordersBeforeMain int64
			require.NoError(t, f.db.Model(&model.TopUp{}).Where("user_id = ?", f.owner.Id).Count(&ordersBeforeMain).Error)
			require.Zero(t, ordersBeforeMain)
			mainOrder := decodeAdtyData[model.AdtyTopUpView](t, adtyJSON(t, f.engine, http.MethodPost, path, ordinaryBody, mainToken).Body)
			require.Equal(t, model.AdtyBusinessWalletTopup, mainOrder.Ext.Business)
			require.Equal(t, "0.5", mainOrder.Ext.TopupGroupRatio.String())
			scenes := []model.AdtyPaymentScene{{Key: "owner_checkout", Name: "Owner checkout", Business: model.AdtyBusinessOwnerSelfUseTopup, Enabled: true, Providers: []string{provider}, SiteScope: "selected", SiteIDs: []int{f.site.Id}, UserTypes: []string{"site_owner"}, MinUSD: decimal.NewFromInt(50), MaxUSD: decimal.NewFromInt(100)}}
			payload, err := common.Marshal(scenes)
			require.NoError(t, err)
			require.NoError(t, f.db.Model(&model.AdtyPaymentSceneConfig{}).Where("id = 1").Update("payload", string(payload)).Error)
			info := decodeAdtyData[struct {
				Scenes []service.AdtyPaymentSceneView `json:"scenes"`
			}](t, f.siteJSON(t, http.MethodGet, "/api/adty/topup/info?business=owner_self_use_topup", "", f.token).Body)
			require.Len(t, info.Scenes, 1)
			require.NotEmpty(t, info.Scenes[0].Methods)
			for _, amount := range []int{49, 101} {
				body := fmt.Sprintf(`{"business":"owner_self_use_topup","payment_scene":"owner_checkout","amount":%d,"method":%q,"chain":%q}`, amount, method, method)
				response := f.siteJSON(t, http.MethodPost, path, body, f.token)
				require.Contains(t, response.Body.String(), `"success":false`)
			}
			body := fmt.Sprintf(`{"business":"owner_self_use_topup","payment_scene":"owner_checkout","amount":100,"method":%q,"chain":%q,"site_id":%d}`, method, method, f.other.Id)
			quote := decodeAdtyData[struct {
				Quote     service.AdtyQuote
				Available bool
			}](t, f.siteJSON(t, http.MethodPost, "/api/adty/topup/quote", body, f.token).Body)
			require.True(t, quote.Available)
			require.Equal(t, "1", quote.Quote.TopupGroupRatio.String())
			require.True(t, quote.Quote.BonusUSD.IsZero())
			ordinary := model.TopUp{UserId: f.owner.Id, TradeNo: "ordinary-pending", PaymentProvider: provider, Status: common.TopUpStatusPending}
			require.NoError(t, f.db.Create(&ordinary).Error)
			require.NoError(t, f.db.Create(&model.AdtyTopupOrder{UserId: f.owner.Id, TradeNo: ordinary.TradeNo, Provider: provider, Business: model.AdtyBusinessWalletTopup, ExpireAt: time.Now().Unix() + 900}).Error)
			view := decodeAdtyData[model.AdtyTopUpView](t, f.siteJSON(t, http.MethodPost, path, body, f.token).Body)
			require.NotNil(t, view.Ext)
			require.Equal(t, model.AdtyBusinessOwnerSelfUseTopup, view.Ext.Business)
			stored, err := model.GetAdtyTopupOrder(view.TradeNo)
			require.NoError(t, err)
			require.Equal(t, f.site.Id, stored.SiteId, "request body cannot choose the credited site")
			require.EqualValues(t, 50000000, view.Ext.CreditQuota)
			require.NoError(t, f.db.First(&ordinary, ordinary.Id).Error)
			require.Equal(t, common.TopUpStatusPending, ordinary.Status)
			resultURL := service.AdtyPayResultURL(view.TradeNo)
			parsed, err := url.Parse(resultURL)
			require.NoError(t, err)
			target := service.AdtyPayResultTarget(strings.TrimPrefix(parsed.Path, "/pay/result/"))
			require.Equal(t, service.AdtySitePublicBaseURL(f.site)+"/site-owner/earnings?trade_no="+view.TradeNo, target)
			require.NoError(t, f.db.Model(&model.AdtyPaymentSceneConfig{}).Where("id = 1").Update("payload", "[]").Error)
			require.Contains(t, f.siteJSON(t, http.MethodPost, path, body, f.token).Body.String(), `"success":false`, "changed policy applies to the next order")
			for range 2 {
				if channel != nil {
					values := map[string]string{"pid": channel.MerchantId, "out_trade_no": view.TradeNo, "trade_no": view.Ext.GatewayTradeId, "type": "alipay", "money": view.Ext.PayAmount.StringFixed(2), "trade_status": "TRADE_SUCCESS"}
					require.Equal(t, "success", epayCallback(t, &adtyGuardFixture{engine: f.engine}, channel.Id, channel.MerchantKey, values))
				} else {
					response := adtyJSON(t, f.engine, http.MethodPost, "/api/user/bepusdt/notify", notifyFor(view.TradeNo, 2, 100.01, "TAddr"), "")
					require.Equal(t, "ok", response.Body.String())
				}
			}
			available, err := model.AdtyOwnerPrincipalAvailable(f.db, f.owner.Id, f.site.Id)
			require.NoError(t, err)
			require.EqualValues(t, 50000000, available, "frozen order is credited once despite a policy change")
			history := decodeAdtyData[struct {
				Total int
				Items []model.AdtyTopUpView
			}](t, f.siteJSON(t, http.MethodGet, "/api/adty/topup/self?business=owner_self_use_topup", "", f.token).Body)
			require.Len(t, history.Items, 1)
			require.Equal(t, view.TradeNo, history.Items[0].TradeNo)
			orders := decodeAdtyData[struct {
				Items []struct {
					TradeNo  string `json:"trade_no"`
					Business string `json:"business"`
					Actions  struct {
						Refund bool `json:"refund"`
					} `json:"actions"`
				}
			}](t, f.siteJSON(t, http.MethodGet, "/api/adty/site/orders?trade_no="+view.TradeNo, "", f.token).Body)
			require.Len(t, orders.Items, 1)
			require.Equal(t, model.AdtyBusinessOwnerSelfUseTopup, orders.Items[0].Business)
			require.False(t, orders.Items[0].Actions.Refund, "owner customer-refund actions must exclude self-use deposits")
			for _, suffix := range []string{"refund-preview", "refund", "refunds/example/approve", "refunds/example/reclaim", "refunds/example/bonus", "refunds/example/commission", "refunds/example/payout", "refunds/example/complete", "refunds/example/terminate"} {
				method := http.MethodPost
				if suffix == "refund-preview" {
					method = http.MethodGet
				}
				response := f.siteJSON(t, method, "/api/adty/site/orders/"+view.TradeNo+"/"+suffix, `{"refund_usd":"1"}`, f.token)
				require.Equal(t, http.StatusForbidden, response.Code, suffix+": "+response.Body.String())
			}
			require.NoError(t, f.db.Model(&model.AdtyPaymentSceneConfig{}).Where("id = 1").Update("payload", string(payload)).Error)
			late := decodeAdtyData[model.AdtyTopUpView](t, f.siteJSON(t, http.MethodPost, path, body, f.token).Body)
			require.NoError(t, f.db.Model(f.site).Update("status", model.AdtySiteStatusClosed).Error)
			if channel != nil {
				values := map[string]string{"pid": channel.MerchantId, "out_trade_no": late.TradeNo, "trade_no": late.Ext.GatewayTradeId, "type": "alipay", "money": late.Ext.PayAmount.StringFixed(2), "trade_status": "TRADE_SUCCESS"}
				require.Equal(t, "success", epayCallback(t, &adtyGuardFixture{engine: f.engine}, channel.Id, channel.MerchantKey, values))
			} else {
				response := adtyJSON(t, f.engine, http.MethodPost, "/api/user/bepusdt/notify", notifyFor(late.TradeNo, 2, 100.01, "TAddr"), "")
				require.Equal(t, "ok", response.Body.String())
			}
			available, err = model.AdtyOwnerPrincipalAvailable(f.db, f.owner.Id, f.site.Id)
			require.NoError(t, err)
			require.EqualValues(t, 50000000, available)
			var reviews int64
			require.NoError(t, f.db.Model(&model.PaymentOrphanEvent{}).Where("reference_id = ? AND status = ?", late.TradeNo, model.PaymentOrphanStatusPendingReview).Count(&reviews).Error)
			require.Positive(t, reviews, "closed-site late payments remain visible to finance")
		})
	}
}

func TestPaymentScenesConstrainNativeEpayOrders(t *testing.T) {
	f := newAdtyGuardFixture(t)
	setRouterBonusRates(t)
	shortEpayTimeouts(t)
	allowedGateway := newFakeEpay(t, epayQR)
	forbiddenGateway := newFakeEpay(t, func(url.Values) (int, string, time.Duration) {
		t.Error("the unapproved channel must never receive an order")
		return 200, `{}`, 0
	})
	allowed := seedRouterEpayChannel(t, "scene-allowed", allowedGateway.server.URL, model.AdtyEpayOrderModeAPI, 1)
	forbidden := seedRouterEpayChannel(t, "scene-forbidden", forbiddenGateway.server.URL, model.AdtyEpayOrderModeAPI, 99)
	buyer, userID := bepusdtBuyer(t, f, "scene-epay-buyer")
	body := fmt.Sprintf(`{"expected_version":1,"scenes":[{"key":"limited","name":"Limited checkout","business":"wallet_topup","enabled":true,"providers":["adty_epay"],"methods":["alipay"],"channel_ids":[%d],"currencies":["CNY"],"site_scope":"main","site_ids":[],"user_types":["main_customer"],"min_usd":"50","max_usd":"100"}]}`, allowed.Id)
	saved := adtyJSON(t, f.engine, http.MethodPut, "/api/adty/admin/payment-scenes", body, f.root)
	require.Equal(t, http.StatusOK, saved.Code, saved.Body.String())
	for _, input := range []string{
		`{"amount":100,"method":"alipay"}`,
		`{"amount":49,"method":"alipay","payment_scene":"limited"}`,
		`{"amount":101,"method":"alipay","payment_scene":"limited"}`,
		`{"amount":100,"method":"wxpay","payment_scene":"limited"}`,
		`{"amount":100,"method":"alipay","payment_scene":"missing"}`,
	} {
		response := adtyJSON(t, f.engine, http.MethodPost, "/api/adty/epay/pay", input, buyer)
		assert.Contains(t, response.Body.String(), `"success":false`, input)
	}
	var count int64
	require.NoError(t, f.db.Model(&model.TopUp{}).Where("user_id = ?", userID).Count(&count).Error)
	require.Zero(t, count, "denied native requests cannot create orders")
	forbiddenChoice := fmt.Sprintf(`{"amount":100,"method":"alipay","payment_scene":"limited","channel_id":%d}`, forbidden.Id)
	assert.Contains(t, adtyJSON(t, f.engine, http.MethodPost, "/api/adty/epay/pay", forbiddenChoice, buyer).Body.String(), `"success":false`)
	info := decodeAdtyData[struct {
		Scenes []service.AdtyPaymentSceneView `json:"scenes"`
	}](t, adtyJSON(t, f.engine, http.MethodGet, "/api/adty/topup/info", "", buyer).Body)
	for _, scene := range info.Scenes {
		if scene.Key != "limited" {
			continue
		}
		for _, method := range scene.Methods {
			assert.NotEqual(t, forbidden.Id, method.ChannelId, "a channel outside the scene is not offered")
		}
	}
	valid := `{"amount":100,"method":"alipay","payment_scene":"limited"}`
	require.NoError(t, f.db.Model(&model.AdtyEpayChannelMethod{}).Where("channel_id = ? AND method = ?", allowed.Id, "alipay").Update("min_cny", 999999).Error)
	quoted := adtyJSON(t, f.engine, http.MethodPost, "/api/adty/topup/quote", valid, buyer)
	quotedData := decodeAdtyData[struct {
		Available bool `json:"available"`
	}](t, quoted.Body)
	assert.False(t, quotedData.Available, "a channel outside the scene cannot make a quote available")
	require.NoError(t, f.db.Model(&model.AdtyEpayChannelMethod{}).Where("channel_id = ? AND method = ?", allowed.Id, "alipay").Update("min_cny", 0).Error)
	response := adtyJSON(t, f.engine, http.MethodPost, "/api/adty/epay/pay", valid, buyer)
	view := decodeAdtyData[model.AdtyTopUpView](t, response.Body)
	require.NotNil(t, view.Ext)
	assert.Equal(t, allowed.Id, view.Ext.ChannelId)
	var frozen map[string]any
	raw, err := common.Marshal(view.Ext)
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(raw, &frozen))
	assert.Equal(t, "limited", frozen["payment_scene"])
	assert.EqualValues(t, 2, frozen["payment_scene_version"])
	assert.NotContains(t, frozen, "payment_scene_snapshot", "policy audience details stay private")
}

func TestPaymentScenesRequireRootAndVersionedChanges(t *testing.T) {
	f := newAdtyGuardFixture(t)
	const path = "/api/adty/admin/payment-scenes"
	initial := adtyJSON(t, f.engine, http.MethodGet, path, "", f.root)
	require.Equal(t, http.StatusOK, initial.Code, initial.Body.String())
	config := decodeAdtyData[struct {
		Version int64 `json:"version"`
		Scenes  []struct {
			Key string `json:"key"`
		} `json:"scenes"`
	}](t, initial.Body)
	require.EqualValues(t, 1, config.Version)
	require.Len(t, config.Scenes, 1)
	assert.Equal(t, "wallet_topup", config.Scenes[0].Key)
	buyer := adtySessionToken(t, f.db, "scene-buyer", common.RoleCommonUser)
	for _, token := range []string{"", buyer, f.token} {
		response := adtyJSON(t, f.engine, http.MethodGet, path, "", token)
		assert.Contains(t, []int{http.StatusForbidden, http.StatusUnauthorized}, response.Code)
	}
	var root model.User
	require.NoError(t, f.db.Where("username = ?", "guard-root").First(&root).Error)
	readPAT := createAdtyScopedToken(t, root.Id, "option:read")
	assert.Equal(t, http.StatusOK, adtyJSON(t, f.engine, http.MethodGet, path, "", readPAT).Code)
	const disabled = `{"expected_version":1,"scenes":[{"key":"wallet_topup","name":"Wallet top-up","business":"wallet_topup","enabled":false,"providers":["bepusdt"],"methods":[],"channel_ids":[],"currencies":["USDT"],"site_scope":"all","site_ids":[],"user_types":[],"min_usd":"1","max_usd":"1000"}]}`
	assert.Equal(t, http.StatusForbidden, adtyJSON(t, f.engine, http.MethodPut, path, disabled, readPAT).Code)
	saved := adtyJSON(t, f.engine, http.MethodPut, path, disabled, f.root)
	require.Equal(t, http.StatusOK, saved.Code, saved.Body.String())
	assert.Contains(t, saved.Body.String(), `"version":2`)
	assert.Equal(t, http.StatusConflict, adtyJSON(t, f.engine, http.MethodPut, path, disabled, f.root).Code)
	info := adtyJSON(t, f.engine, http.MethodGet, "/api/adty/topup/info", "", buyer)
	data := decodeAdtyData[struct {
		Scenes  []any `json:"scenes"`
		Methods []any `json:"methods"`
	}](t, info.Body)
	assert.Empty(t, data.Scenes)
	assert.Empty(t, data.Methods)
	var audits int64
	require.NoError(t, f.db.Model(&model.AuditLog{}).Where("action = ?", "adty.payment_scenes.update").Count(&audits).Error)
	assert.EqualValues(t, 1, audits, "only a committed configuration creates an audit")
}

func TestVerifiedNonPaidCallbacksRemainTraceable(t *testing.T) {
	f := newAdtyGuardFixture(t)
	var root model.User
	require.NoError(t, f.db.Where("username = ?", "guard-root").First(&root).Error)
	seedRouterOrder(t, f.db, root.Id, "callback-history", common.TopUpStatusPending, common.GetTimestamp()+600)
	cfg := setting.GetBEpusdtSetting()
	previousSecret := cfg.Secret
	cfg.Secret = "synthetic-callback-history-key"
	t.Cleanup(func() { cfg.Secret = previousSecret })
	for _, status := range []int{1, 3} {
		values := map[string]any{"order_id": "callback-history", "status": status}
		values["signature"] = service.BEpusdtSign(values, cfg.Secret)
		payload, err := common.Marshal(values)
		require.NoError(t, err)
		response := adtyJSON(t, f.engine, http.MethodPost, "/api/user/bepusdt/notify", string(payload), "")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}
	var events []model.AdtyOrderPaymentEvent
	require.NoError(t, f.db.Where("trade_no = ?", "callback-history").Order("id").Find(&events).Error)
	require.Len(t, events, 2)
	assert.Equal(t, "received", events[0].Outcome)
	assert.Equal(t, "callback", events[0].Source)
	var buyer model.User
	require.NoError(t, f.db.First(&buyer, root.Id).Error)
	assert.Zero(t, buyer.Quota)
}

// financeQueryProvider 给真实的易支付提供方换上可控的查单结果，
// 这样查单接口的权限和「只入账一次」不依赖外部网关。
type financeQueryProvider struct {
	service.AdtyPaymentProvider
	query func(*model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error)
}

func (p financeQueryProvider) Query(_ context.Context, ext *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error) {
	return p.query(ext)
}

func TestFinanceQueryRequiresWritePermissionAndCreditsOnce(t *testing.T) {
	f := newAdtyGuardFixture(t)
	channel := seedRouterEpayChannel(t, "finance-query", "https://query.example", model.AdtyEpayOrderModeAPI, 1)
	buyer := adtySessionToken(t, f.db, "query-buyer", common.RoleCommonUser)
	var user model.User
	require.NoError(t, f.db.Where("username = ?", "query-buyer").First(&user).Error)
	order := model.TopUp{UserId: user.Id, TradeNo: "EPY-admin-query", Amount: 2, Money: 14.6,
		PaymentProvider: model.PaymentProviderAdtyEpay, PaymentMethod: "alipay", Status: common.TopUpStatusExpired}
	ext := model.AdtyTopupOrder{TradeNo: order.TradeNo, UserId: user.Id, Provider: order.PaymentProvider, Method: order.PaymentMethod,
		FaceUSD: decimal.NewFromInt(2), CreditQuota: 1000000, PayAmount: decimal.RequireFromString("14.60"), PayCurrency: "CNY",
		TopupGroupRatio: decimal.NewFromInt(1), GatewayTradeId: "gw-query", ChannelId: channel.Id}
	require.NoError(t, model.CreateAdtyTopUp(&order, &ext))
	queries := 0
	paid, wrongAmount, gatewayFails := false, false, false
	real := service.AdtyPaymentProviderByName(model.PaymentProviderAdtyEpay)
	require.NotNil(t, real)
	service.RegisterAdtyPaymentProvider(financeQueryProvider{AdtyPaymentProvider: real, query: func(queried *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error) {
		queries++
		require.Equal(t, order.TradeNo, queried.TradeNo)
		if gatewayFails {
			return nil, errors.New("query failed with secret query-string-key")
		}
		if !paid {
			return nil, nil
		}
		amount := decimal.RequireFromString("14.60")
		if wrongAmount {
			amount = decimal.RequireFromString("14.61")
		}
		return &model.AdtyPaymentFacts{Provider: model.PaymentProviderAdtyEpay, Method: "alipay", PaidAmount: amount, PaidCurrency: "CNY", GatewayTradeId: "gw-query"}, nil
	}})
	t.Cleanup(func() { service.RegisterAdtyPaymentProvider(real) })
	path := "/api/adty/admin/topup/" + order.TradeNo + "/query"
	f.grant(t, authz.AdtyFinanceRead)
	readPAT := createAdtyScopedToken(t, f.admin.Id, "adty_finance:read")
	for _, credential := range []string{f.token, readPAT, buyer, ""} {
		response := adtyJSON(t, f.engine, http.MethodPost, path, `{}`, credential)
		assert.Contains(t, []int{http.StatusForbidden, http.StatusUnauthorized}, response.Code)
	}
	assert.Zero(t, queries)
	f.grant(t, authz.AdtyFinanceRead, authz.AdtyFinanceManualCredit)
	assert.Equal(t, http.StatusForbidden, adtyJSON(t, f.engine, http.MethodPost, path, `{}`, readPAT).Code)
	writePAT := createAdtyScopedToken(t, f.admin.Id, "adty_finance:manual_credit")
	for _, scenario := range []struct {
		name                string
		paid, wrong, failed bool
		success             bool
	}{
		{"unconfirmed", false, false, false, true}, {"gateway failure", false, false, true, false},
		{"amount mismatch", true, true, false, false},
	} {
		paid, wrongAmount, gatewayFails = scenario.paid, scenario.wrong, scenario.failed
		queryOp := service.AdtyTopUpQueryOperation(order.TradeNo)
		response := adtyJSONWithProof(t, f.engine, http.MethodPost, path, `{}`, writePAT, financeProofForCredential(t, f, writePAT, queryOp))
		var body struct {
			Success bool
			Data    struct{ Status string }
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
		assert.Equal(t, scenario.success, body.Success, scenario.name)
		assert.NotContains(t, response.Body.String(), "query-string-key")
		require.NoError(t, f.db.First(&user, user.Id).Error)
		assert.Zero(t, user.Quota, scenario.name)
		if scenario.success {
			assert.Equal(t, common.TopUpStatusExpired, body.Data.Status)
		}
	}
	paid, wrongAmount, gatewayFails = true, false, false
	for _, credential := range []string{writePAT, f.token, f.root} {
		queryOp := service.AdtyTopUpQueryOperation(order.TradeNo)
		response := adtyJSONWithProof(t, f.engine, http.MethodPost, path, `{}`, credential, financeProofForCredential(t, f, credential, queryOp))
		require.Contains(t, response.Body.String(), `"success":true`, response.Body.String())
		assert.Contains(t, response.Body.String(), `"status":"success"`)
	}
	require.NoError(t, f.db.First(&user, user.Id).Error)
	assert.Equal(t, 1000000, user.Quota)
	var credits int64
	require.NoError(t, f.db.Model(&model.AdtyBalanceLedger{}).Where("ref_id = ?", order.TradeNo).Count(&credits).Error)
	assert.EqualValues(t, 1, credits)
	var audits []model.AuditLog
	require.NoError(t, f.db.Where("action = ?", "adty.topup.query").Find(&audits).Error)
	assert.GreaterOrEqual(t, len(audits), 2)
	for _, audit := range audits {
		assert.NotContains(t, fmt.Sprint(audit), "query-string-key")
	}
	f.grant(t, authz.AdtyFinanceRead)
	assert.Equal(t, http.StatusForbidden, adtyJSON(t, f.engine, http.MethodPost, path, `{}`, writePAT).Code)
	seedRouterOrder(t, f.db, user.Id, "bepusdt-no-query", common.TopUpStatusPending, common.GetTimestamp()+900)
	unsupported := adtyJSONWithProof(t, f.engine, http.MethodPost, "/api/adty/admin/topup/bepusdt-no-query/query", `{}`, f.root, financeProofForCredential(t, f, f.root, service.AdtyTopUpQueryOperation("bepusdt-no-query")))
	assert.Contains(t, unsupported.Body.String(), `"success":false`)
	require.NoError(t, f.db.Create(&model.TopUp{UserId: user.Id, TradeNo: "epay-missing-details", PaymentProvider: model.PaymentProviderAdtyEpay, PaymentMethod: "alipay", Status: common.TopUpStatusPending}).Error)
	require.NotPanics(t, func() {
		missing := adtyJSONWithProof(t, f.engine, http.MethodPost, "/api/adty/admin/topup/epay-missing-details/query", `{}`, f.root, financeProofForCredential(t, f, f.root, service.AdtyTopUpQueryOperation("epay-missing-details")))
		assert.Equal(t, http.StatusOK, missing.Code)
		assert.Contains(t, missing.Body.String(), `"success":false`)
	})
}

type fakeAdtyProvider struct {
	name      string
	cancelErr error
	canceled  []string
}

func (p *fakeAdtyProvider) Name() string { return p.name }
func (p *fakeAdtyProvider) Methods() []service.AdtyMethodInfo {
	return []service.AdtyMethodInfo{{Id: "usdt.trc20", Kind: "usdt", Available: true}}
}
func (p *fakeAdtyProvider) AvailableFor(string, *service.AdtyQuote) bool { return true }
func (p *fakeAdtyProvider) Cancel(ext *model.AdtyTopupOrder) error {
	p.canceled = append(p.canceled, ext.TradeNo)
	return p.cancelErr
}

// setRouterBonusRates 打开「按支付方式加赠 2.5%」，结束时还原。
//
// 金额档位加赠已经退役：新订单的赠金只按支付方式快照计算
// （`setting/adty_setting/topup.go` 的 BonusRates 注释、
// `service/adty_topup_quote.go` 的 adtyBonusFromRates、AGENTS.md 的同名条款）。
// 这个 helper 以前写 BonusRates，等于让测试断言一条不参与计价的老规则，
// 于是所有走它的用例都在要求实现「恢复退役机制」——红灯的原因在测试这边。
//
// 两种支付方式都设，覆盖这批用例里出现的 epay（alipay）与 BEpusdt（usdt.*）：
// AdtyBonusMethod 会把 usdt.trc20 / usdt.bep20 归一成 usdt。
func setRouterBonusRates(t *testing.T) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	oldOptions := common.OptionMap
	common.OptionMap = make(map[string]string, len(oldOptions))
	for key, value := range oldOptions {
		common.OptionMap[key] = value
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptions
		common.OptionMapRWMutex.Unlock()
	})
	setting := adty_setting.GetTopupSetting()
	old := setting.MethodBonus
	t.Cleanup(func() { setting.MethodBonus = old })
	for _, method := range []string{"usdt", "alipay"} {
		base, err := common.Marshal(adty_setting.GetTopupSetting().MethodBonus)
		require.NoError(t, err)
		value, err := adty_setting.MergeMainMethodBonus(string(base), method, true, "0.025")
		require.NoError(t, err)
		require.NoError(t, model.UpdateOption(adty_setting.TopupSettingKeyMethodBonus, value))
	}
}

func seedRouterOrder(t *testing.T, db *gorm.DB, userID int, tradeNo, status string, expireAt int64) {
	t.Helper()
	require.NoError(t, model.CreateAdtyTopUp(
		&model.TopUp{UserId: userID, Amount: 100, TradeNo: tradeNo, PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt, CreateTime: 1, Status: status},
		&model.AdtyTopupOrder{TradeNo: tradeNo, UserId: userID, Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
			FaceUSD: decimal.NewFromInt(100), BonusUSD: decimal.RequireFromString("2.5"), CreditQuota: 51_250_000,
			PayAmount: decimal.NewFromInt(100), PayCurrency: "USDT", TopupGroupRatio: decimal.NewFromInt(1), ExpireAt: expireAt},
	))
}

func TestAdtyWalletTopUpAPIs(t *testing.T) {
	f := newAdtyGuardFixture(t)
	setRouterBonusRates(t)
	provider := &fakeAdtyProvider{name: model.PaymentProviderAdtyBEpusdt}
	real := service.AdtyPaymentProviderByName(model.PaymentProviderAdtyBEpusdt)
	service.RegisterAdtyPaymentProvider(provider)
	t.Cleanup(func() { service.RegisterAdtyPaymentProvider(real) })
	owner := adtySessionToken(t, f.db, "wallet-owner", common.RoleCommonUser)
	other := adtySessionToken(t, f.db, "wallet-other", common.RoleCommonUser)
	var ownerUser model.User
	require.NoError(t, f.db.Where("username = ?", "wallet-owner").First(&ownerUser).Error)
	now := common.GetTimestamp()
	seedRouterOrder(t, f.db, ownerUser.Id, "EPU-wallet-old", common.TopUpStatusPending, now+600)
	seedRouterOrder(t, f.db, ownerUser.Id, "EPU-wallet-new", common.TopUpStatusPending, now+900)
	seedRouterOrder(t, f.db, ownerUser.Id, "EPU-wallet-gone", common.TopUpStatusPending, now-10)
	seedRouterOrder(t, f.db, ownerUser.Id, "EPU-wallet-paid", common.TopUpStatusSuccess, now+900)

	info := adtyJSON(t, f.engine, http.MethodGet, "/api/adty/topup/info", "", owner)
	require.Equal(t, http.StatusOK, info.Code)
	body := decodeAdtyData[struct {
		Tiers   []struct{ Amount int } `json:"tiers"`
		Pending *struct {
			TradeNo string `json:"trade_no"`
		} `json:"pending"`
	}](t, info.Body)
	assert.Len(t, body.Tiers, len(operation_setting.GetPaymentSetting().AmountOptions), "tiers are the amount options set in the admin")
	require.NotNil(t, body.Pending)
	assert.Equal(t, "EPU-wallet-new", body.Pending.TradeNo, "the newest unexpired pending order")

	quote := adtyJSON(t, f.engine, http.MethodPost, "/api/adty/topup/quote", `{"amount":150,"method":"usdt.trc20"}`, owner)
	quoted := decodeAdtyData[struct {
		Quote struct {
			BonusUSD decimal.Decimal `json:"bonus_usd"`
		} `json:"quote"`
		Available bool `json:"available"`
	}](t, quote.Body)
	assert.Equal(t, "3.75", quoted.Quote.BonusUSD.String())
	assert.True(t, quoted.Available)

	notMine := adtyJSON(t, f.engine, http.MethodGet, "/api/adty/topup/orders/EPU-wallet-new", "", other)
	assert.Equal(t, http.StatusNotFound, notMine.Code)

	cancelPaid := adtyJSON(t, f.engine, http.MethodPost, "/api/adty/topup/orders/EPU-wallet-paid/cancel", "", owner)
	assert.Contains(t, cancelPaid.Body.String(), `"success":false`)

	provider.cancelErr = service.ErrAdtyPaymentDetected
	failed := adtyJSON(t, f.engine, http.MethodPost, "/api/adty/topup/orders/EPU-wallet-new/cancel", "", owner)
	assert.Contains(t, failed.Body.String(), `"success":false`)
	assert.Contains(t, failed.Body.String(), "A transfer has been detected")
	var stored model.TopUp
	require.NoError(t, f.db.Where("trade_no = ?", "EPU-wallet-new").First(&stored).Error)
	assert.Equal(t, common.TopUpStatusPending, stored.Status)

	provider.cancelErr = errors.New("dial tcp: connection refused")
	unknown := adtyJSON(t, f.engine, http.MethodPost, "/api/adty/topup/orders/EPU-wallet-new/cancel", "", owner)
	assert.Contains(t, unknown.Body.String(), `"success":false`, unknown.Body.String())
	require.NoError(t, f.db.Where("trade_no = ?", "EPU-wallet-new").First(&stored).Error)
	assert.Equal(t, common.TopUpStatusPending, stored.Status, "unknown gateway state must keep the order pending")

	provider.cancelErr = nil
	canceled := adtyJSON(t, f.engine, http.MethodPost, "/api/adty/topup/orders/EPU-wallet-new/cancel", "", owner)
	assert.Contains(t, canceled.Body.String(), `"success":true`, canceled.Body.String())
	require.NoError(t, f.db.Where("trade_no = ?", "EPU-wallet-new").First(&stored).Error)
	assert.Equal(t, model.TopUpStatusCanceled, stored.Status)

	open := adtyJSON(t, f.engine, http.MethodPost, "/api/adty/topup/quote", `{"amount":150,"method":"usdt.trc20"}`, owner)
	assert.Contains(t, open.Body.String(), `"success":true`, open.Body.String())
}

func TestAdtyTopUpMaintenanceExpiresOrders(t *testing.T) {
	f := newAdtyGuardFixture(t)
	adtySessionToken(t, f.db, "expire-owner", common.RoleCommonUser)
	var user model.User
	require.NoError(t, f.db.Where("username = ?", "expire-owner").First(&user).Error)
	seedRouterOrder(t, f.db, user.Id, "EPU-expire", common.TopUpStatusPending, 100)
	service.RunAdtyTopUpMaintenanceOnce(common.GetTimestamp())
	var stored model.TopUp
	require.NoError(t, f.db.Where("trade_no = ?", "EPU-expire").First(&stored).Error)
	assert.Equal(t, common.TopUpStatusExpired, stored.Status)
}

func TestFinanceActionsRequirePermissions(t *testing.T) {
	f := newAdtyGuardFixture(t)
	var user model.User
	adtySessionToken(t, f.db, "finance-buyer", common.RoleCommonUser)
	require.NoError(t, f.db.Where("username = ?", "finance-buyer").First(&user).Error)
	seedRouterOrder(t, f.db, user.Id, "EPU-finance", common.TopUpStatusPending, common.GetTimestamp()+900)

	f.grant(t, authz.AdtyFinanceRead)
	audit := adtyJSON(t, f.engine, http.MethodGet, "/api/user/admin/finance/recharge-audit", "", f.token)
	assert.Contains(t, audit.Body.String(), `"EPU-finance"`)
	complete := adtyJSON(t, f.engine, http.MethodPost, "/api/adty/admin/topup/EPU-finance/complete", `{"proof":"0xabc","note":"checked"}`, f.token)
	assert.Equal(t, http.StatusForbidden, complete.Code)
	refund := adtyJSON(t, f.engine, http.MethodPost, "/api/adty/admin/topup/EPU-finance/refund", `{"reason":"x","idempotency_key":"k1","refund_quota":1}`, f.token)
	assert.Equal(t, http.StatusForbidden, refund.Code)

	f.grant(t, authz.AdtyFinanceRead, authz.AdtyFinanceManualCredit, authz.AdtyFinanceRefund)
	missingProof := adtyJSON(t, f.engine, http.MethodPost, "/api/adty/admin/topup/EPU-finance/complete", `{"note":"checked"}`, f.token)
	assert.Contains(t, missingProof.Body.String(), `"success":false`)
	complete = adtyJSON(t, f.engine, http.MethodPost, "/api/adty/admin/topup/EPU-finance/complete", `{"proof":"0xabc","note":"checked"}`, f.token)
	assert.Equal(t, http.StatusForbidden, complete.Code)
	assert.Contains(t, complete.Body.String(), "SECURITY_PROOF_REQUIRED")
	complete = adtyJSONWithProof(t, f.engine, http.MethodPost, "/api/adty/admin/topup/EPU-finance/complete", `{"proof":"0xabc","note":"checked"}`, f.token, financeAdminProof(t, f, service.AdtyTopUpManualCompleteOperation("EPU-finance", "0xabc", "checked")))
	require.Contains(t, complete.Body.String(), `"success":true`, complete.Body.String())
	var stored model.User
	require.NoError(t, f.db.First(&stored, user.Id).Error)
	assert.Equal(t, 51_250_000, stored.Quota, "manual completion credits face plus bonus")
	ext, err := model.GetAdtyTopupOrder("EPU-finance")
	require.NoError(t, err)
	assert.Equal(t, "0xabc", ext.PaidTxHash)
	assert.Equal(t, model.AdtyPaymentSourceManual, ext.PaidSource)
	var audits int64
	require.NoError(t, f.db.Model(&model.AuditLog{}).Where("action = ?", "adty.topup.manual_complete").Count(&audits).Error)
	assert.EqualValues(t, 1, audits)

	preview := adtyJSON(t, f.engine, http.MethodGet, "/api/adty/admin/topup/EPU-finance/refund-preview", "", f.token)
	require.Contains(t, preview.Body.String(), `"success":true`, preview.Body.String())
	require.Contains(t, preview.Body.String(), `"refund_quota":50000000`)
	refund = adtyJSON(t, f.engine, http.MethodPost, "/api/adty/admin/topup/EPU-finance/refund", `{"reason":"duplicate","idempotency_key":"refund-1","refund_usd":"100"}`, f.token)
	assert.Equal(t, http.StatusForbidden, refund.Code)
	assert.Contains(t, refund.Body.String(), "SECURITY_PROOF_REQUIRED")
	refund = adtyJSONWithProof(t, f.engine, http.MethodPost, "/api/adty/admin/topup/EPU-finance/refund", `{"reason":"duplicate","idempotency_key":"refund-1","refund_usd":"100"}`, f.token, financeAdminProof(t, f, service.AdtyTopUpRefundOperation("EPU-finance", "duplicate", "100", nil)))
	require.Contains(t, refund.Body.String(), `"success":true`, refund.Body.String())
	assert.Contains(t, refund.Body.String(), `"refund_id":"refund-1"`)
	assert.Contains(t, refund.Body.String(), `"request_quota":50000000`)
	assert.Contains(t, refund.Body.String(), `"status":"submitted"`)
	assert.Contains(t, refund.Body.String(), `"in_progress_quota":50000000`)
	require.NoError(t, f.db.First(&stored, user.Id).Error)
	assert.Equal(t, 51_250_000, stored.Quota, "apply occupies the cap but does not debit")

	summary := adtyJSON(t, f.engine, http.MethodGet, "/api/user/admin/finance/recharge-audit/summary", "", f.token)
	assert.Contains(t, summary.Body.String(), `"currency":"USD"`)
}

func TestRefundQuotaPOSTAppliesManualRefundWithoutCompleting(t *testing.T) {
	f := newAdtyGuardFixture(t)
	adtySessionToken(t, f.db, "quota-buyer", common.RoleCommonUser)
	var user model.User
	require.NoError(t, f.db.Where("username = ?", "quota-buyer").First(&user).Error)
	seedRouterOrder(t, f.db, user.Id, "EPU-quota-apply", common.TopUpStatusPending, common.GetTimestamp()+900)
	f.grant(t, authz.AdtyFinanceRead, authz.AdtyFinanceManualCredit, authz.AdtyFinanceRefund)
	complete := adtyJSONWithProof(t, f.engine, http.MethodPost, "/api/adty/admin/topup/EPU-quota-apply/complete", `{"proof":"0xabc","note":"checked"}`, f.token, financeAdminProof(t, f, service.AdtyTopUpManualCompleteOperation("EPU-quota-apply", "0xabc", "checked")))
	require.Contains(t, complete.Body.String(), `"success":true`, complete.Body.String())
	var funded model.User
	require.NoError(t, f.db.First(&funded, user.Id).Error)
	startQuota := funded.Quota
	require.Positive(t, startQuota)

	quota := int64(25_000_000)
	body := `{"reason":"duplicate","idempotency_key":"refund-quota-1","refund_quota":25000000}`
	refund := adtyJSONWithProof(t, f.engine, http.MethodPost, "/api/adty/admin/topup/EPU-quota-apply/refund", body, f.token, financeAdminProof(t, f, service.AdtyTopUpRefundOperation("EPU-quota-apply", "duplicate", "", &quota)))
	require.Contains(t, refund.Body.String(), `"success":true`, refund.Body.String())
	assert.Contains(t, refund.Body.String(), `"refund_id":"refund-quota-1"`)
	assert.Contains(t, refund.Body.String(), `"request_quota":25000000`)
	assert.Contains(t, refund.Body.String(), `"status":"submitted"`)
	assert.Contains(t, refund.Body.String(), `"in_progress_quota":25000000`)
	assert.NotContains(t, refund.Body.String(), `"status":"completed"`)
	require.NoError(t, f.db.First(&funded, user.Id).Error)
	assert.Equal(t, startQuota, funded.Quota, "refund_quota apply occupies the cap but does not debit")
	ext, err := model.GetAdtyTopupOrder("EPU-quota-apply")
	require.NoError(t, err)
	assert.Zero(t, ext.RefundQuota, "completed S is not written at apply")
}

func TestPaymentOrphanReviewRequiresWritePermissionAndEvidence(t *testing.T) {
	f := newAdtyGuardFixture(t)
	event := &model.PaymentOrphanEvent{Provider: model.PaymentProviderAdtyEpay, ReferenceID: "review-router-order", EventID: "reversal:router-review", EventType: "dispute.open"}
	require.NoError(t, model.RecordPaymentOrphanEvent(event))
	path := fmt.Sprintf("/api/user/admin/finance/payment-orphans/%d/resolve", event.ID)
	body := `{"status":"pending_review","trade_no":"review-router-order","external_amount":"0","external_currency":"USD","proof":"provider-dispute-open","note":"not an actual completed reversal"}`
	f.grant(t, authz.AdtyFinanceRead)
	denied := adtyJSON(t, f.engine, http.MethodPost, path, body, f.token)
	require.Equal(t, http.StatusForbidden, denied.Code)
	f.grant(t, authz.AdtyFinanceRead, authz.AdtyFinanceManualCredit)
	assert.Contains(t, adtyJSON(t, f.engine, http.MethodPost, path, body, f.token).Body.String(), "SECURITY_PROOF_REQUIRED")
	invalidBody := `{"status":"refunded","note":"clicked refunded"}`
	missingEvidence := adtyJSONWithProof(t, f.engine, http.MethodPost, path, invalidBody, f.token, financeAdminProof(t, f, financeResolveOp(event.ID, invalidBody)))
	require.Contains(t, missingEvidence.Body.String(), `"success":false`)
	result := adtyJSONWithProof(t, f.engine, http.MethodPost, path, body, f.token, financeAdminProof(t, f, financeResolveOp(event.ID, body)))
	require.Contains(t, result.Body.String(), `"success":true`, result.Body.String())
	require.Contains(t, result.Body.String(), `"external_amount":"0"`)
	listed := adtyJSON(t, f.engine, http.MethodGet, "/api/user/admin/finance/payment-orphans?status=pending_review", "", f.token)
	require.Contains(t, listed.Body.String(), `"proof":"provider-dispute-open"`)
	var reviews int64
	require.NoError(t, f.db.Model(&model.PaymentOrphanReview{}).Count(&reviews).Error)
	require.EqualValues(t, 1, reviews)
}

func TestOrphanCreditRunsBonusAndHooks(t *testing.T) {
	f := newAdtyGuardFixture(t)
	channel := seedRouterEpayChannel(t, "orphan-credit", "https://query.example", model.AdtyEpayOrderModeAPI, 1)
	f.grant(t, authz.AdtyFinanceRead, authz.AdtyFinanceManualCredit)
	var user model.User
	adtySessionToken(t, f.db, "orphan-buyer", common.RoleCommonUser)
	require.NoError(t, f.db.Where("username = ?", "orphan-buyer").First(&user).Error)

	hookCalls := 0
	old := model.AdtyAfterTopUpCreditedTx
	model.AdtyAfterTopUpCreditedTx = func(*gorm.DB, *model.TopUp, *model.AdtyTopupOrder) error { hookCalls++; return nil }
	t.Cleanup(func() { model.AdtyAfterTopUpCreditedTx = old })

	seedRouterOrder(t, f.db, user.Id, "EPU-orphan", common.TopUpStatusExpired, 100)
	require.NoError(t, model.UpdateAdtyTopupOrderFields("EPU-orphan", map[string]any{"pay_address": "TAddr", "gateway_trade_id": "gw-1"}))
	usdt := &model.PaymentOrphanEvent{Provider: model.PaymentProviderAdtyBEpusdt, EventType: "callback", ReferenceID: "EPU-orphan",
		Reason: "temporary failure", Payload: `{"actual_amount":100,"amount":100,"token":"TAddr","trade_id":"gw-1","block_transaction_id":"0xtx"}`}
	require.NoError(t, model.RecordPaymentOrphanEvent(usdt))

	require.NoError(t, model.CreateAdtyTopUp(
		&model.TopUp{UserId: user.Id, Amount: 100, TradeNo: "EPY-orphan", PaymentMethod: "alipay", PaymentProvider: model.PaymentProviderAdtyEpay, CreateTime: 1, Status: common.TopUpStatusPending},
		&model.AdtyTopupOrder{TradeNo: "EPY-orphan", UserId: user.Id, Provider: model.PaymentProviderAdtyEpay, Method: "alipay", ChannelId: channel.Id,
			FaceUSD: decimal.NewFromInt(100), BonusUSD: decimal.RequireFromString("2.5"), CreditQuota: 51_250_000,
			PayAmount: decimal.RequireFromString("730"), PayCurrency: "CNY", TopupGroupRatio: decimal.NewFromInt(1), ExpireAt: 100},
	))
	epay := &model.PaymentOrphanEvent{Provider: model.PaymentProviderAdtyEpay, EventType: "callback", ReferenceID: "EPY-orphan",
		Reason: "temporary failure", Payload: `{"money":"730.00","trade_no":"2026103100001"}`}
	require.NoError(t, model.RecordPaymentOrphanEvent(epay))

	for _, event := range []*model.PaymentOrphanEvent{usdt, epay} {
		path := fmt.Sprintf("/api/user/admin/finance/payment-orphans/%d/credit", event.ID)
		resp := adtyJSONWithProof(t, f.engine, http.MethodPost, path, `{"note":"verified"}`, f.token, financeAdminProof(t, f, service.AdtyPaymentOrphanCreditOperation(event.ID, "verified")))
		require.Contains(t, resp.Body.String(), `"success":true`, resp.Body.String())
	}
	var stored model.User
	require.NoError(t, f.db.First(&stored, user.Id).Error)
	assert.Equal(t, 2*51_250_000, stored.Quota, "both orders credited with their bonus")
	assert.Equal(t, 2, hookCalls)

	var orphan model.PaymentOrphanEvent
	require.NoError(t, f.db.First(&orphan, usdt.ID).Error)
	assert.Equal(t, model.PaymentOrphanStatusCredited, orphan.Status)
	again := adtyJSONWithProof(t, f.engine, http.MethodPost, fmt.Sprintf("/api/user/admin/finance/payment-orphans/%d/credit", usdt.ID), `{}`, f.token, financeAdminProof(t, f, service.AdtyPaymentOrphanCreditOperation(usdt.ID, "")))
	assert.Contains(t, again.Body.String(), `"success":false`)
}

func financeAdminProof(t *testing.T, f *adtyGuardFixture, operation service.VerificationOperation) string {
	t.Helper()
	return financeUserProof(t, f.admin, service.VerificationMethodPassword, operation)
}

func financeUserProof(t *testing.T, user model.User, method string, operation service.VerificationOperation) string {
	t.Helper()
	return financeIdentityProof(t, service.AuthIdentity{UserID: user.Id, SessionID: "sid-" + user.Username, UserAuthVersion: user.AuthVersion, SessionVersion: 1}, method, operation)
}

func financeIdentityProof(t *testing.T, identity service.AuthIdentity, method string, operation service.VerificationOperation) string {
	t.Helper()
	binding, err := service.BindVerificationOperation(operation)
	require.NoError(t, err)
	proof, _, err := service.IssueSecurityProof(identity, method, binding)
	require.NoError(t, err)
	return proof
}

func financeProofForCredential(t *testing.T, f *adtyGuardFixture, credential string, operation service.VerificationOperation) string {
	t.Helper()
	if strings.HasPrefix(credential, model.AccessTokenPrefix) {
		var token model.UserAccessToken
		require.NoError(t, f.db.Where("token_hash = ?", model.AccessTokenFingerprint(credential)).First(&token).Error)
		var user model.User
		require.NoError(t, f.db.First(&user, token.UserId).Error)
		method := service.VerificationMethodPassword
		if user.Role == common.RoleRootUser {
			method = service.VerificationMethodRootExempt
		}
		return financeIdentityProof(t, service.AuthIdentity{
			UserID: user.Id, SessionID: model.AccessTokenSessionID(token.Id),
			UserAuthVersion: user.AuthVersion, SessionVersion: model.AccessTokenSessionVersion,
		}, method, operation)
	}
	if credential == f.token {
		return financeAdminProof(t, f, operation)
	}
	if credential == f.root {
		var root model.User
		require.NoError(t, f.db.Where("username = ?", "guard-root").First(&root).Error)
		return financeUserProof(t, root, service.VerificationMethodRootExempt, operation)
	}
	t.Fatalf("unknown finance credential")
	return ""
}

func financeResolveOp(id int64, body string) service.VerificationOperation {
	var review model.PaymentOrphanReviewRequest
	_ = common.UnmarshalJsonStr(body, &review)
	return service.AdtyPaymentOrphanResolveOperation(id, review.Status, review.TradeNo, review.ExternalAmount, review.ExternalCurrency, review.Proof, review.Note, review.DismissalReason)
}

func TestFinanceRefundAndCompleteRequireSecurityProof(t *testing.T) {
	f := newAdtyGuardFixture(t)
	adtySessionToken(t, f.db, "proof-buyer", common.RoleCommonUser)
	var buyer model.User
	require.NoError(t, f.db.Where("username = ?", "proof-buyer").First(&buyer).Error)
	var root model.User
	require.NoError(t, f.db.Where("username = ?", "guard-root").First(&root).Error)

	seedRouterOrder(t, f.db, buyer.Id, "EPU-proof-complete", common.TopUpStatusPending, common.GetTimestamp()+900)
	seedRouterOrder(t, f.db, buyer.Id, "EPU-proof-refund", common.TopUpStatusPending, common.GetTimestamp()+900)
	seedRouterOrder(t, f.db, buyer.Id, "EPU-proof-other", common.TopUpStatusPending, common.GetTimestamp()+900)
	seedRouterOrder(t, f.db, buyer.Id, "EPU-proof-root", common.TopUpStatusPending, common.GetTimestamp()+900)

	completePath := "/api/adty/admin/topup/EPU-proof-complete/complete"
	completeBody := `{"proof":"0xabc","note":"checked"}`
	completeOp := service.AdtyTopUpManualCompleteOperation("EPU-proof-complete", "0xabc", "checked")

	f.grant(t, authz.AdtyFinanceRead)
	denied := adtyJSONWithProof(t, f.engine, http.MethodPost, completePath, completeBody, f.token, financeAdminProof(t, f, completeOp))
	assert.Equal(t, http.StatusForbidden, denied.Code)
	assert.NotContains(t, denied.Body.String(), "SECURITY_PROOF")
	var pending model.TopUp
	require.NoError(t, f.db.Where("trade_no = ?", "EPU-proof-complete").First(&pending).Error)
	assert.Equal(t, common.TopUpStatusPending, pending.Status)

	f.grant(t, authz.AdtyFinanceRead, authz.AdtyFinanceManualCredit, authz.AdtyFinanceRefund)
	missing := adtyJSON(t, f.engine, http.MethodPost, completePath, completeBody, f.token)
	assert.Equal(t, http.StatusForbidden, missing.Code)
	assert.Contains(t, missing.Body.String(), "SECURITY_PROOF_REQUIRED")
	require.NoError(t, f.db.Where("trade_no = ?", "EPU-proof-complete").First(&pending).Error)
	assert.Equal(t, common.TopUpStatusPending, pending.Status)

	wrongOrder := financeAdminProof(t, f, service.AdtyTopUpManualCompleteOperation("EPU-proof-other", "0xabc", "checked"))
	assert.Contains(t, adtyJSONWithProof(t, f.engine, http.MethodPost, completePath, completeBody, f.token, wrongOrder).Body.String(), "SECURITY_PROOF_CONTEXT_MISMATCH")
	wrongScope := financeAdminProof(t, f, service.AdtyTopUpRefundOperation("EPU-proof-complete", "checked", "100", nil))
	assert.Contains(t, adtyJSONWithProof(t, f.engine, http.MethodPost, completePath, completeBody, f.token, wrongScope).Body.String(), "SECURITY_PROOF_SCOPE_MISMATCH")
	require.NoError(t, f.db.Where("trade_no = ?", "EPU-proof-complete").First(&pending).Error)
	assert.Equal(t, common.TopUpStatusPending, pending.Status)

	okProof := financeAdminProof(t, f, completeOp)
	complete := adtyJSONWithProof(t, f.engine, http.MethodPost, completePath, completeBody, f.token, okProof)
	require.Contains(t, complete.Body.String(), `"success":true`, complete.Body.String())
	replay := adtyJSONWithProof(t, f.engine, http.MethodPost, completePath, completeBody, f.token, okProof)
	assert.Contains(t, replay.Body.String(), "SECURITY_PROOF_CONSUMED")

	// Credit a separate order so refund proof tests can mutate a funded wallet once.
	refundSetup := adtyJSONWithProof(t, f.engine, http.MethodPost, "/api/adty/admin/topup/EPU-proof-refund/complete", completeBody, f.token, financeAdminProof(t, f, service.AdtyTopUpManualCompleteOperation("EPU-proof-refund", "0xabc", "checked")))
	require.Contains(t, refundSetup.Body.String(), `"success":true`, refundSetup.Body.String())
	var funded model.User
	require.NoError(t, f.db.First(&funded, buyer.Id).Error)
	startQuota := funded.Quota
	require.Equal(t, 2*51_250_000, startQuota)

	preview := adtyJSON(t, f.engine, http.MethodGet, "/api/adty/admin/topup/EPU-proof-refund/refund-preview", "", f.token)
	require.Contains(t, preview.Body.String(), `"success":true`, preview.Body.String())
	require.Contains(t, preview.Body.String(), `"refund_quota":50000000`)

	refundPath := "/api/adty/admin/topup/EPU-proof-refund/refund"
	refundBody := `{"reason":"duplicate","idempotency_key":"refund-proof-1","refund_usd":"100"}`
	refundOp := service.AdtyTopUpRefundOperation("EPU-proof-refund", "duplicate", "100", nil)
	assert.Contains(t, adtyJSON(t, f.engine, http.MethodPost, refundPath, refundBody, f.token).Body.String(), "SECURITY_PROOF_REQUIRED")
	assert.Contains(t, adtyJSONWithProof(t, f.engine, http.MethodPost, refundPath, refundBody, f.token, financeAdminProof(t, f, service.AdtyTopUpRefundOperation("EPU-proof-other", "duplicate", "100", nil))).Body.String(), "SECURITY_PROOF_CONTEXT_MISMATCH")
	assert.Contains(t, adtyJSONWithProof(t, f.engine, http.MethodPost, refundPath, refundBody, f.token, financeAdminProof(t, f, service.AdtyTopUpRefundOperation("EPU-proof-refund", "duplicate", "50", nil))).Body.String(), "SECURITY_PROOF_CONTEXT_MISMATCH")
	assert.Contains(t, adtyJSONWithProof(t, f.engine, http.MethodPost, refundPath, refundBody, f.token, financeAdminProof(t, f, service.AdtyTopUpManualCompleteOperation("EPU-proof-refund", "0xabc", "checked"))).Body.String(), "SECURITY_PROOF_SCOPE_MISMATCH")
	require.NoError(t, f.db.First(&funded, buyer.Id).Error)
	assert.Equal(t, startQuota, funded.Quota, "rejected proofs must not refund")

	refundProof := financeAdminProof(t, f, refundOp)
	refund := adtyJSONWithProof(t, f.engine, http.MethodPost, refundPath, refundBody, f.token, refundProof)
	require.Contains(t, refund.Body.String(), `"success":true`, refund.Body.String())
	assert.Contains(t, refund.Body.String(), `"refund_id":"refund-proof-1"`)
	assert.Contains(t, refund.Body.String(), `"request_quota":50000000`)
	assert.Contains(t, refund.Body.String(), `"status":"submitted"`)
	assert.Contains(t, adtyJSONWithProof(t, f.engine, http.MethodPost, refundPath, refundBody, f.token, refundProof).Body.String(), "SECURITY_PROOF_CONSUMED")

	var before int64
	require.NoError(t, f.db.Model(&model.AuditLog{}).Where("action = ?", "adty.topup.manual_complete").Count(&before).Error)
	rootComplete := adtyJSONWithProof(t, f.engine, http.MethodPost, "/api/adty/admin/topup/EPU-proof-root/complete", completeBody, f.root, financeUserProof(t, root, service.VerificationMethodRootExempt, service.AdtyTopUpManualCompleteOperation("EPU-proof-root", "0xabc", "checked")))
	require.Contains(t, rootComplete.Body.String(), `"success":true`, rootComplete.Body.String())
	var after int64
	require.NoError(t, f.db.Model(&model.AuditLog{}).Where("action = ?", "adty.topup.manual_complete").Count(&after).Error)
	assert.Equal(t, before+1, after)
	var audit model.AuditLog
	require.NoError(t, f.db.Where("action = ?", "adty.topup.manual_complete").Order("id desc").First(&audit).Error)
	assert.Equal(t, root.Id, audit.UserId)
	assert.Contains(t, audit.Content, "EPU-proof-root")
	requirements, err := service.GetVerificationRequirements(service.AuthIdentity{UserID: root.Id, SessionID: "sid-" + root.Username, UserAuthVersion: root.AuthVersion, SessionVersion: 1}, service.VerificationScopeAdtyTopUpManualComplete)
	require.NoError(t, err)
	require.Len(t, requirements.Methods, 1)
	assert.Equal(t, service.VerificationMethodRootExempt, requirements.Methods[0].Method)

	f.grant(t, authz.AdtyFinanceRead, authz.AdtyFinanceManualCredit)
	quota := int64(1)
	deniedRefund := adtyJSONWithProof(t, f.engine, http.MethodPost, refundPath, `{"reason":"x","idempotency_key":"k-denied","refund_quota":1}`, f.token, financeAdminProof(t, f, service.AdtyTopUpRefundOperation("EPU-proof-refund", "x", "", &quota)))
	assert.Equal(t, http.StatusForbidden, deniedRefund.Code)
	assert.NotContains(t, deniedRefund.Body.String(), "SECURITY_PROOF")
}

func TestFinanceCreditResolveAndQueryRequireSecurityProof(t *testing.T) {
	f := newAdtyGuardFixture(t)
	adtySessionToken(t, f.db, "proof-orphan-buyer", common.RoleCommonUser)
	var buyer model.User
	require.NoError(t, f.db.Where("username = ?", "proof-orphan-buyer").First(&buyer).Error)
	var root model.User
	require.NoError(t, f.db.Where("username = ?", "guard-root").First(&root).Error)

	seedRouterOrder(t, f.db, buyer.Id, "EPU-proof-query", common.TopUpStatusPending, common.GetTimestamp()+900)
	require.NoError(t, model.UpdateAdtyTopupOrderFields("EPU-proof-query", map[string]any{"pay_address": "TAddr", "gateway_trade_id": "gw-proof"}))

	seedRouterOrder(t, f.db, buyer.Id, "EPU-proof-orphan", common.TopUpStatusExpired, 100)
	require.NoError(t, model.UpdateAdtyTopupOrderFields("EPU-proof-orphan", map[string]any{"pay_address": "TAddr", "gateway_trade_id": "gw-orphan"}))
	orphan := &model.PaymentOrphanEvent{Provider: model.PaymentProviderAdtyBEpusdt, EventType: "callback", ReferenceID: "EPU-proof-orphan",
		Reason: "temporary failure", Payload: `{"actual_amount":100,"amount":100,"token":"TAddr","trade_id":"gw-orphan","block_transaction_id":"0xtx"}`}
	require.NoError(t, model.RecordPaymentOrphanEvent(orphan))
	other := &model.PaymentOrphanEvent{Provider: model.PaymentProviderAdtyBEpusdt, EventType: "callback", ReferenceID: "EPU-proof-orphan-other",
		Reason: "temporary failure", Payload: `{"actual_amount":100,"amount":100,"token":"TAddr","trade_id":"gw-other","block_transaction_id":"0xother"}`}
	require.NoError(t, model.RecordPaymentOrphanEvent(other))
	reviewEvent := &model.PaymentOrphanEvent{Provider: model.PaymentProviderAdtyEpay, ReferenceID: "review-proof-order", EventID: "reversal:router-proof", EventType: "dispute.open"}
	require.NoError(t, model.RecordPaymentOrphanEvent(reviewEvent))

	creditPath := fmt.Sprintf("/api/user/admin/finance/payment-orphans/%d/credit", orphan.ID)
	creditBody := `{"note":"verified"}`
	creditOp := service.AdtyPaymentOrphanCreditOperation(orphan.ID, "verified")
	resolvePath := fmt.Sprintf("/api/user/admin/finance/payment-orphans/%d/resolve", reviewEvent.ID)
	resolveBody := `{"status":"pending_review","trade_no":"review-proof-order","external_amount":"0","external_currency":"USD","proof":"provider-dispute-open","note":"not an actual completed reversal"}`
	resolveOp := financeResolveOp(reviewEvent.ID, resolveBody)
	queryPath := "/api/adty/admin/topup/EPU-proof-query/query"
	queryOp := service.AdtyTopUpQueryOperation("EPU-proof-query")

	f.grant(t, authz.AdtyFinanceRead)
	deniedCredit := adtyJSONWithProof(t, f.engine, http.MethodPost, creditPath, creditBody, f.token, financeAdminProof(t, f, creditOp))
	assert.Equal(t, http.StatusForbidden, deniedCredit.Code)
	assert.NotContains(t, deniedCredit.Body.String(), "SECURITY_PROOF")
	deniedResolve := adtyJSONWithProof(t, f.engine, http.MethodPost, resolvePath, resolveBody, f.token, financeAdminProof(t, f, resolveOp))
	assert.Equal(t, http.StatusForbidden, deniedResolve.Code)
	assert.NotContains(t, deniedResolve.Body.String(), "SECURITY_PROOF")
	deniedQuery := adtyJSONWithProof(t, f.engine, http.MethodPost, queryPath, `{}`, f.token, financeAdminProof(t, f, queryOp))
	assert.Equal(t, http.StatusForbidden, deniedQuery.Code)
	assert.NotContains(t, deniedQuery.Body.String(), "SECURITY_PROOF")

	f.grant(t, authz.AdtyFinanceRead, authz.AdtyFinanceManualCredit)
	assert.Contains(t, adtyJSON(t, f.engine, http.MethodPost, creditPath, creditBody, f.token).Body.String(), "SECURITY_PROOF_REQUIRED")
	assert.Contains(t, adtyJSON(t, f.engine, http.MethodPost, resolvePath, resolveBody, f.token).Body.String(), "SECURITY_PROOF_REQUIRED")
	assert.Contains(t, adtyJSON(t, f.engine, http.MethodPost, queryPath, `{}`, f.token).Body.String(), "SECURITY_PROOF_REQUIRED")

	assert.Contains(t, adtyJSONWithProof(t, f.engine, http.MethodPost, creditPath, creditBody, f.token, financeAdminProof(t, f, service.AdtyPaymentOrphanCreditOperation(other.ID, "verified"))).Body.String(), "SECURITY_PROOF_CONTEXT_MISMATCH")
	assert.Contains(t, adtyJSONWithProof(t, f.engine, http.MethodPost, creditPath, creditBody, f.token, financeAdminProof(t, f, service.AdtyTopUpQueryOperation("EPU-proof-orphan"))).Body.String(), "SECURITY_PROOF_SCOPE_MISMATCH")
	var pending model.PaymentOrphanEvent
	require.NoError(t, f.db.First(&pending, orphan.ID).Error)
	assert.NotEqual(t, model.PaymentOrphanStatusCredited, pending.Status)

	assert.Contains(t, adtyJSONWithProof(t, f.engine, http.MethodPost, resolvePath, resolveBody, f.token, financeAdminProof(t, f, financeResolveOp(other.ID, resolveBody))).Body.String(), "SECURITY_PROOF_CONTEXT_MISMATCH")
	assert.Contains(t, adtyJSONWithProof(t, f.engine, http.MethodPost, queryPath, `{}`, f.token, financeAdminProof(t, f, service.AdtyTopUpQueryOperation("EPU-proof-orphan"))).Body.String(), "SECURITY_PROOF_CONTEXT_MISMATCH")

	okCredit := financeAdminProof(t, f, creditOp)
	credit := adtyJSONWithProof(t, f.engine, http.MethodPost, creditPath, creditBody, f.token, okCredit)
	require.Contains(t, credit.Body.String(), `"success":true`, credit.Body.String())
	replayCredit := adtyJSONWithProof(t, f.engine, http.MethodPost, creditPath, creditBody, f.token, okCredit)
	assert.Contains(t, replayCredit.Body.String(), "SECURITY_PROOF_CONSUMED")

	okResolve := financeAdminProof(t, f, resolveOp)
	resolve := adtyJSONWithProof(t, f.engine, http.MethodPost, resolvePath, resolveBody, f.token, okResolve)
	require.Contains(t, resolve.Body.String(), `"success":true`, resolve.Body.String())
	replayResolve := adtyJSONWithProof(t, f.engine, http.MethodPost, resolvePath, resolveBody, f.token, okResolve)
	assert.Contains(t, replayResolve.Body.String(), "SECURITY_PROOF_CONSUMED")

	okQuery := financeAdminProof(t, f, queryOp)
	queried := adtyJSONWithProof(t, f.engine, http.MethodPost, queryPath, `{}`, f.token, okQuery)
	assert.NotContains(t, queried.Body.String(), "SECURITY_PROOF", queried.Body.String())
	replayQuery := adtyJSONWithProof(t, f.engine, http.MethodPost, queryPath, `{}`, f.token, okQuery)
	assert.Contains(t, replayQuery.Body.String(), "SECURITY_PROOF_CONSUMED")

	rootQuery := adtyJSONWithProof(t, f.engine, http.MethodPost, queryPath, `{}`, f.root, financeUserProof(t, root, service.VerificationMethodRootExempt, queryOp))
	assert.NotContains(t, rootQuery.Body.String(), "SECURITY_PROOF", rootQuery.Body.String())
	requirements, err := service.GetVerificationRequirements(service.AuthIdentity{UserID: root.Id, SessionID: "sid-" + root.Username, UserAuthVersion: root.AuthVersion, SessionVersion: 1}, service.VerificationScopeAdtyTopUpQuery)
	require.NoError(t, err)
	require.Len(t, requirements.Methods, 1)
	assert.Equal(t, service.VerificationMethodRootExempt, requirements.Methods[0].Method)
}
