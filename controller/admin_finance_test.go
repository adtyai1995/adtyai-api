package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// migrateAdtyUnifiedOrderSources creates every table the unified order UNION
// reads. A missing source fails the whole list, including callers that only
// inserted top-ups.
func migrateAdtyUnifiedOrderSources(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.TopUp{},
		&model.AdtyTopupOrder{},
		&model.AdtyEpayChannel{},
		&model.AdtySite{},
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
}

func TestRechargeAuditProviderCapabilitiesDatabaseMatrix(t *testing.T) {
	providers := []string{model.PaymentProviderAdtyBEpusdt, model.PaymentProviderAdtyEpay}
	for _, dialect := range []struct{ kind, env string }{{"sqlite", ""}, {"mysql", "TEST_MYSQL_DSN"}, {"postgres", "TEST_POSTGRES_DSN"}} {
		t.Run(dialect.kind, func(t *testing.T) {
			if dialect.env != "" && os.Getenv(dialect.env) == "" {
				t.Skip("database DSN not configured")
			}
			db := modelManagementDB(t, dialect.kind, os.Getenv(dialect.env))
			migrateAdtyUnifiedOrderSources(t, db)
			require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.AdtyTopupOrder{}, &model.AdtyEpayChannel{}))
			user := model.User{Username: "audit-provider-buyer", Role: common.RoleCommonUser}
			require.NoError(t, db.Create(&user).Error)
			for _, provider := range providers {
				for _, status := range []string{common.TopUpStatusPending, common.TopUpStatusSuccess} {
					tradeNo := provider + "-" + status
					require.NoError(t, db.Create(&model.TopUp{TradeNo: tradeNo, UserId: user.Id, PaymentProvider: provider, PaymentMethod: provider, Status: status}).Error)
					require.NoError(t, db.Create(&model.AdtyTopupOrder{TradeNo: tradeNo, UserId: user.Id, Provider: provider, Method: provider,
						FaceUSD: decimal.NewFromInt(2), PayAmount: decimal.NewFromInt(2), PayCurrency: "USD", CreditQuota: 1000000}).Error)
				}
			}
			for _, order := range []model.TopUp{
				{TradeNo: "missing-details", PaymentProvider: model.PaymentProviderAdtyEpay, Status: common.TopUpStatusPending},
				{TradeNo: "legacy-payment", PaymentProvider: "legacy-payment", Status: common.TopUpStatusPending},
				{TradeNo: "replaced-order", PaymentProvider: model.PaymentProviderAdtyBEpusdt, Status: model.TopUpStatusReplaced},
			} {
				order.UserId = user.Id
				require.NoError(t, db.Create(&order).Error)
			}
			require.NoError(t, db.Create(&model.AdtyTopupOrder{TradeNo: "replaced-order", UserId: user.Id, Provider: model.PaymentProviderAdtyBEpusdt}).Error)
			request := func(path string, handler gin.HandlerFunc, target any) {
				t.Helper()
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodGet, path, nil)
				c.Set("role", common.RoleRootUser)
				handler(c)
				require.Equal(t, http.StatusOK, recorder.Code)
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), target))
			}
			var summary struct {
				Success bool
				Data    struct {
					Providers    []string `json:"provider_option"`
					Capabilities []struct {
						Provider   string `json:"provider"`
						Query      bool   `json:"query"`
						Complete   bool   `json:"complete"`
						Refund     bool   `json:"refund"`
						RefundMode string `json:"refund_mode"`
					} `json:"provider_capabilities"`
				}
			}
			request("/summary", GetRechargeAuditSummary, &summary)
			require.True(t, summary.Success)
			assert.ElementsMatch(t, providers, summary.Data.Providers)
			require.Len(t, summary.Data.Capabilities, 2)
			for _, capability := range summary.Data.Capabilities {
				assert.True(t, capability.Query, capability.Provider)
				assert.True(t, capability.Complete, capability.Provider)
				assert.True(t, capability.Refund, capability.Provider)
				assert.Equal(t, "offline", capability.RefundMode, "ledger refunds do not refund the provider automatically")
			}
			for _, provider := range append(providers, "") {
				var listed struct {
					Success bool
					Data    struct {
						Total int
						Items []struct {
							TradeNo  string `json:"trade_no"`
							Provider string `json:"payment_provider"`
							Status   string
							Actions  struct{ Complete, Refund, Query bool } `json:"actions"`
						}
					}
				}
				request("/orders?page_size=50&provider="+provider, GetRechargeAudit, &listed)
				require.True(t, listed.Success)
				if provider == "" {
					assert.Equal(t, 7, listed.Data.Total)
				}
				for _, order := range listed.Data.Items {
					if provider != "" {
						assert.Equal(t, provider, order.Provider)
					}
					if order.TradeNo == "missing-details" || order.TradeNo == "legacy-payment" || order.TradeNo == "replaced-order" {
						assert.False(t, order.Actions.Complete, order.TradeNo)
						assert.False(t, order.Actions.Refund, order.TradeNo)
						assert.False(t, order.Actions.Query, order.TradeNo)
						continue
					}
					assert.Equal(t, order.Status == common.TopUpStatusPending, order.Actions.Complete, fmt.Sprint(order))
					assert.Equal(t, order.Status == common.TopUpStatusSuccess, order.Actions.Refund, fmt.Sprint(order))
					assert.Equal(t, order.Status == common.TopUpStatusPending, order.Actions.Query, fmt.Sprint(order))
				}
			}
		})
	}
}

func TestRechargeAuditSiteOwnershipDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct{ kind, env string }{{"sqlite", ""}, {"mysql", "TEST_MYSQL_DSN"}, {"postgres", "TEST_POSTGRES_DSN"}} {
		t.Run(dialect.kind, func(t *testing.T) {
			if dialect.env != "" && os.Getenv(dialect.env) == "" {
				t.Skip("database DSN not configured")
			}
			db := modelManagementDB(t, dialect.kind, os.Getenv(dialect.env))
			migrateAdtyUnifiedOrderSources(t, db)
			require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.AdtyTopupOrder{}, &model.AdtySite{}, &model.AdtyEpayChannel{}))
			for _, site := range []model.AdtySite{
				{Id: 7, Subdomain: "north", RootDomain: "sites.test", Brand: `{"name":"North Studio"}`},
				{Id: 8, Subdomain: "south", RootDomain: "sites.test", Brand: `{"name":"South Studio"}`},
			} {
				require.NoError(t, db.Create(&site).Error)
			}
			user := model.User{Username: "moved-order-buyer", Role: common.RoleCommonUser, SiteId: 7}
			require.NoError(t, db.Create(&user).Error)
			for i, fixture := range []struct {
				trade  string
				siteID int
				face   int64
			}{
				{"main-order", 0, 2}, {"north-original", 7, 3}, {"south-current", 8, 5},
				{"legacy-unknown", -1, 7}, {"historical-site", 99, 11},
			} {
				require.NoError(t, db.Create(&model.TopUp{TradeNo: fixture.trade, UserId: user.Id,
					PaymentProvider: model.PaymentProviderAdtyEpay, PaymentMethod: "alipay", Status: common.TopUpStatusSuccess,
					Amount: fixture.face, Money: float64(fixture.face), CreateTime: int64(i + 1), CompleteTime: int64(i + 10)}).Error)
				if fixture.siteID >= 0 {
					require.NoError(t, db.Create(&model.AdtyTopupOrder{TradeNo: fixture.trade, UserId: user.Id, SiteId: fixture.siteID,
						Provider: model.PaymentProviderAdtyEpay, FaceUSD: decimal.NewFromInt(fixture.face), BonusUSD: decimal.RequireFromString("0.5")}).Error)
				}
			}
			require.NoError(t, db.Model(&user).Update("site_id", 8).Error)
			type order struct {
				TradeNo   string `json:"trade_no"`
				SiteID    *int   `json:"site_id"`
				SiteName  string `json:"site_name"`
				OrderType string `json:"order_type"`
			}
			var listed struct {
				Success bool
				Data    struct {
					Total int
					Items []order
				}
			}
			modelManagementRequest(t, GetRechargeAudit, http.MethodGet, "/orders", nil, &listed)
			require.True(t, listed.Success)
			require.Equal(t, 5, listed.Data.Total)
			byTrade := map[string]order{}
			for _, item := range listed.Data.Items {
				byTrade[item.TradeNo] = item
				assert.Equal(t, "topup", item.OrderType)
			}
			for trade, expected := range map[string]int{"main-order": 0, "north-original": 7, "south-current": 8, "historical-site": 99} {
				assert.Equal(t, &expected, byTrade[trade].SiteID, trade)
			}
			assert.Equal(t, common.SystemName, byTrade["main-order"].SiteName)
			assert.Equal(t, "North Studio", byTrade["north-original"].SiteName)
			assert.Equal(t, "South Studio", byTrade["south-current"].SiteName)
			assert.Empty(t, byTrade["historical-site"].SiteName)
			assert.Nil(t, byTrade["legacy-unknown"].SiteID, "legacy ownership must not be inferred from the buyer's current site")
			assert.Empty(t, byTrade["legacy-unknown"].SiteName)
			for _, fixture := range []struct{ path, trade string }{
				{"/orders?site_id=0", "main-order"}, {"/orders?site_id=7&order_type=topup&status=success&keyword=original", "north-original"},
				{"/orders?site_id=99", "historical-site"},
			} {
				modelManagementRequest(t, GetRechargeAudit, http.MethodGet, fixture.path, nil, &listed)
				require.True(t, listed.Success, fixture.path)
				require.Equal(t, 1, listed.Data.Total, fixture.path)
				require.Len(t, listed.Data.Items, 1)
				assert.Equal(t, fixture.trade, listed.Data.Items[0].TradeNo)
			}
			var summary struct {
				Success bool
				Data    struct {
					Orders      int
					CreditedUSD decimal.Decimal `json:"credited_usd"`
					BonusUSD    decimal.Decimal `json:"bonus_usd"`
					Sites       []struct {
						SiteID   int    `json:"site_id"`
						SiteName string `json:"site_name"`
					} `json:"site_options"`
				}
			}
			modelManagementRequest(t, GetRechargeAuditSummary, http.MethodGet, "/summary?site_id=7&order_type=topup&status=success&keyword=original", nil, &summary)
			require.True(t, summary.Success)
			assert.Equal(t, 1, summary.Data.Orders)
			assert.Equal(t, "3", summary.Data.CreditedUSD.String())
			assert.Equal(t, "0.5", summary.Data.BonusUSD.String())
			require.Len(t, summary.Data.Sites, 4, "site choices remain available when the list is filtered")
			assert.Equal(t, 0, summary.Data.Sites[0].SiteID)
			assert.Equal(t, common.SystemName, summary.Data.Sites[0].SiteName)
			assert.Equal(t, 7, summary.Data.Sites[1].SiteID)
			assert.Equal(t, "North Studio", summary.Data.Sites[1].SiteName)
			assert.Equal(t, 99, summary.Data.Sites[3].SiteID)
			for _, filter := range []string{"site_id=-1", "site_id=invalid", "site_id=1.2", "site_id=999999999999999999999", "order_type=team"} {
				for _, handler := range []gin.HandlerFunc{GetRechargeAudit, GetRechargeAuditSummary} {
					var invalid struct{ Success bool }
					modelManagementRequest(t, handler, http.MethodGet, "/orders?"+filter, nil, &invalid)
					assert.False(t, invalid.Success, filter)
				}
			}
		})
	}
}

// TestRechargeAuditOwnerKeywordScopeDatabaseMatrix 站长视图的关键词只能命中响应里会返回的列：
// paid_tx_hash（manual 来源会被清空）和 gateway_trade_id（从不返回）不能当筛选面，
// 否则「有没有结果」就是一个逐字符还原字段值的 oracle。
func TestRechargeAuditOwnerKeywordScopeDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct{ kind, env string }{{"sqlite", ""}, {"mysql", "TEST_MYSQL_DSN"}, {"postgres", "TEST_POSTGRES_DSN"}} {
		t.Run(dialect.kind, func(t *testing.T) {
			if dialect.env != "" && os.Getenv(dialect.env) == "" {
				t.Skip("database DSN not configured")
			}
			db := modelManagementDB(t, dialect.kind, os.Getenv(dialect.env))
			migrateAdtyUnifiedOrderSources(t, db)
			require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.AdtyTopupOrder{}, &model.AdtySite{}, &model.AdtyEpayChannel{}))
			owner := model.User{Username: "keyword-scope-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&owner).Error)
			site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "keyword-scope", RootDomain: "sites.test",
				Status: model.AdtySiteStatusActive, AgreementVer: service.AdtyCurrentSiteAgreementVersion()}
			require.NoError(t, db.Create(&site).Error)
			buyer := model.User{Username: "keyword-scope-buyer", Role: common.RoleCommonUser, SiteId: site.Id}
			require.NoError(t, db.Create(&buyer).Error)
			t.Cleanup(service.InvalidateAdtySiteCache)
			for _, fixture := range []struct{ trade, txHash, gatewayID string }{
				{"scope-manual", "0xdeadbeefcafe", "gw-secret-manual"},
				{"scope-callback", "0xfeedface", "gw-secret-callback"},
			} {
				source := model.AdtyPaymentSourceManual
				if fixture.trade == "scope-callback" {
					source = "callback"
				}
				require.NoError(t, db.Create(&model.TopUp{TradeNo: fixture.trade, UserId: buyer.Id,
					PaymentProvider: model.PaymentProviderAdtyBEpusdt, PaymentMethod: "bepusdt",
					Status: common.TopUpStatusSuccess, Amount: 2, Money: 2, CreateTime: 1, CompleteTime: 2}).Error)
				require.NoError(t, db.Create(&model.AdtyTopupOrder{TradeNo: fixture.trade, UserId: buyer.Id, SiteId: site.Id,
					Provider: model.PaymentProviderAdtyBEpusdt, PaidTxHash: fixture.txHash, PaidSource: source,
					GatewayTradeId: fixture.gatewayID, FaceUSD: decimal.NewFromInt(2)}).Error)
			}
			request := func(handler gin.HandlerFunc, path string, setRole bool) struct {
				Success bool
				Data    struct {
					Total int
					Items []struct {
						TradeNo    string `json:"trade_no"`
						PaidTxHash string `json:"paid_tx_hash"`
					}
				}
			} {
				t.Helper()
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodGet, path, nil)
				if setRole {
					c.Set("role", common.RoleRootUser)
				} else {
					c.Set("id", owner.Id)
				}
				handler(c)
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				var listed struct {
					Success bool
					Data    struct {
						Total int
						Items []struct {
							TradeNo    string `json:"trade_no"`
							PaidTxHash string `json:"paid_tx_hash"`
						}
					}
				}
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &listed), recorder.Body.String())
				return listed
			}
			// 站长：只能在会返回的列上筛。
			require.Equal(t, 2, request(GetAdtyOwnerOrders, "/orders", false).Data.Total)
			assert.Equal(t, 1, request(GetAdtyOwnerOrders, "/orders?keyword=scope-manual", false).Data.Total)
			for _, secret := range []string{"deadbeefcafe", "gw-secret-manual", "gw-secret-callback", "feedface"} {
				assert.Equal(t, 0, request(GetAdtyOwnerOrders, "/orders?keyword="+secret, false).Data.Total,
					"owner keyword must not match a column the owner view never returns: "+secret)
			}
			// 后台视图不受影响：这两列照旧可筛，也照旧返回。
			manual := request(GetRechargeAudit, "/orders?keyword=deadbeefcafe", true)
			require.Equal(t, 1, manual.Data.Total)
			require.Len(t, manual.Data.Items, 1)
			assert.Equal(t, "0xdeadbeefcafe", manual.Data.Items[0].PaidTxHash)
			assert.Equal(t, 1, request(GetRechargeAudit, "/orders?keyword=gw-secret-callback", true).Data.Total)
			// 站长视图仍然清空 manual 来源的链上哈希。
			ownerList := request(GetAdtyOwnerOrders, "/orders?keyword=scope-manual", false)
			require.Len(t, ownerList.Data.Items, 1)
			assert.Empty(t, ownerList.Data.Items[0].PaidTxHash)
		})
	}
}

func TestUnifiedFinancialOrderCategoriesAndCallerScope(t *testing.T) {
	db := modelManagementDB(t, "sqlite", "")
	migrateAdtyUnifiedOrderSources(t, db)
	require.NoError(t, db.AutoMigrate(
		&model.TopUp{},
		&model.AdtyTopupOrder{},
		&model.AdtySite{},
		&model.AdtyEpayChannel{},
		&model.BillingOperation{},
		&model.AdtyVoucher{},
		&model.AdtyManualRefund{},
		&model.AdtySiteWithdrawal{},
		&model.AdtySiteInviteCommission{},
		&model.AdtyStaffBalanceRequest{},
	))
	owner7 := model.User{Username: "uni-owner-7", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner7).Error)
	site7 := model.AdtySite{
		Id: 7, OwnerUserId: owner7.Id, Subdomain: "uni-seven", RootDomain: "sites.test",
		Status: model.AdtySiteStatusActive, AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
		Brand: `{"name":"Seven Site"}`,
	}
	require.NoError(t, db.Create(&site7).Error)
	site8 := model.AdtySite{
		Id: 8, OwnerUserId: 999, Subdomain: "uni-eight", RootDomain: "sites.test",
		Status: model.AdtySiteStatusActive, AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
		Brand: `{"name":"Eight Site"}`,
	}
	require.NoError(t, db.Create(&site8).Error)
	t.Cleanup(service.InvalidateAdtySiteCache)

	buyer7 := model.User{Username: "uni-buyer-7", Role: common.RoleCommonUser, SiteId: 7}
	require.NoError(t, db.Create(&buyer7).Error)
	buyer8 := model.User{Username: "uni-buyer-8", Role: common.RoleCommonUser, SiteId: 8}
	require.NoError(t, db.Create(&buyer8).Error)

	// 1. Topup
	require.NoError(t, db.Create(&model.TopUp{
		TradeNo: "UNI-TOPUP-7", UserId: buyer7.Id, PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		PaymentMethod: "usdt.trc20", Status: common.TopUpStatusSuccess, Amount: 10, Money: 10, CreateTime: 100,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: "UNI-TOPUP-7", UserId: buyer7.Id, SiteId: 7, Provider: model.PaymentProviderAdtyBEpusdt,
		Method: "usdt.trc20", FaceUSD: decimal.NewFromInt(10), CreditQuota: 5_000_000,
	}).Error)

	// 2. API Consumption
	require.NoError(t, db.Create(&model.BillingOperation{
		OperationKey: "UNI-CONSUME-7", UserId: buyer7.Id, SiteId: 7,
		State: model.BillingSettled, AppliedQuota: 500_000, CreatedAt: 101, UpdatedAt: 101,
	}).Error)
	require.NoError(t, db.Create(&model.BillingOperation{
		OperationKey: "UNI-CONSUME-8", UserId: buyer8.Id, SiteId: 8,
		State: model.BillingSettled, AppliedQuota: 500_000, CreatedAt: 102, UpdatedAt: 102,
	}).Error)

	// 3. Voucher. Redeemed codes enter the unified list as a top-up whose
	// provider is adty_voucher, not as a bare adty_vouchers row.
	require.NoError(t, db.Create(&model.TopUp{
		TradeNo: "UNI-VOUCHER-7", UserId: buyer7.Id, PaymentProvider: model.AdtyVoucherProvider,
		PaymentMethod: model.AdtyVoucherPurposeSiteCustomer, Status: common.TopUpStatusSuccess,
		Amount: 5, Money: 5, CreateTime: 103,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: "UNI-VOUCHER-7", UserId: buyer7.Id, SiteId: 7, Provider: model.AdtyVoucherProvider,
		Method: model.AdtyVoucherPurposeSiteCustomer, FaceUSD: decimal.NewFromInt(5), CreditQuota: 2_500_000,
	}).Error)

	// 4. Refund
	require.NoError(t, db.Create(&model.AdtyManualRefund{
		RefundId: "UNI-REFUND-7", TradeNo: "UNI-TOPUP-7", UserId: buyer7.Id, SiteId: 7,
		Status: model.AdtyManualRefundStatusCompleted, RequestUSD: decimal.NewFromInt(2),
		RequestQuota: 1_000_000, CreatedAt: 104,
	}).Error)

	// 5. Withdrawal
	require.NoError(t, db.Create(&model.AdtySiteWithdrawal{
		SiteId: 7, OwnerUserId: owner7.Id, AmountUSD: decimal.NewFromInt(50),
		Method: "usdt_trc20", Status: "pending", CreatedAt: 105,
	}).Error)

	// 6. Commission
	require.NoError(t, db.Create(&model.AdtySiteInviteCommission{
		SiteId: 7, InviterId: buyer7.Id, InviteeId: buyer8.Id, TradeNo: "UNI-TOPUP-7",
		AmountUSD: decimal.NewFromInt(1), Status: "available", CreatedAt: 106,
	}).Error)

	// 7. Staff adjustment
	staffKey := "uni-staff-7"
	require.NoError(t, db.Create(&model.AdtyStaffBalanceRequest{
		SiteId: 7, StaffUserId: owner7.Id, CustomerUserId: buyer7.Id,
		Direction: model.AdtyStaffBalanceIncrease, AmountUSD: decimal.NewFromInt(3),
		Reason: "comp", BusinessRef: "TICKET-7", FundingSource: model.AdtyStaffFundingSourceEarnings,
		Status: model.AdtyStaffBalanceApproved, IdempotencyKey: &staffKey, CreatedAt: 107,
	}).Error)

	type orderItem struct {
		TradeNo   string `json:"trade_no"`
		OrderType string `json:"order_type"`
		SiteID    *int   `json:"site_id"`
	}
	type listResp struct {
		Success bool
		Data    struct {
			Total int         `json:"total"`
			Items []orderItem `json:"items"`
		}
	}

	// Root sees the seven seeded business categories plus the other site's consumption.
	var allListed listResp
	modelManagementRequest(t, GetRechargeAudit, http.MethodGet, "/orders?page_size=50", nil, &allListed)
	require.True(t, allListed.Success)
	require.Equal(t, 8, allListed.Data.Total)

	seededCategories := []string{
		OrderTypeTopup, OrderTypeConsumption, OrderTypeVoucher, OrderTypeRefund,
		OrderTypeWithdrawal, OrderTypeCommission, OrderTypeStaffAdjustment,
	}
	for _, cat := range seededCategories {
		var filtered listResp
		modelManagementRequest(t, GetRechargeAudit, http.MethodGet, "/orders?page_size=50&site_id=7&order_type="+cat, nil, &filtered)
		require.True(t, filtered.Success, cat)
		require.Equal(t, 1, filtered.Data.Total, "expected 1 item on site 7 for category "+cat)
		require.Equal(t, cat, filtered.Data.Items[0].OrderType)
	}

	// Site owner 7 sees only site 7's 7 items across all categories, never site 8's consumption.
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/orders?page_size=50", nil)
	c.Set("id", owner7.Id)
	GetAdtyOwnerOrders(c)
	require.Equal(t, http.StatusOK, rec.Code)
	var ownerListed listResp
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &ownerListed))
	require.True(t, ownerListed.Success)
	require.Equal(t, 7, ownerListed.Data.Total)
	for _, item := range ownerListed.Data.Items {
		require.NotNil(t, item.SiteID)
		require.Equal(t, 7, *item.SiteID)
	}
}

func TestSiteOwnerManualPartialRefundWorkflowAndScopeIsolation(t *testing.T) {
	common.CryptoSecret = "test-owner-refund-secret-32-bytes!!"
	db := modelManagementDB(t, "sqlite", "")
	migrateAdtyUnifiedOrderSources(t, db)
	require.NoError(t, db.AutoMigrate(
		&model.TopUp{},
		&model.AdtyTopupOrder{},
		&model.AdtySite{},
		&model.AdtySiteWallet{},
		&model.AdtySiteWalletLedger{},
		&model.AdtyBalanceLedger{},
		&model.AdtyFundBatch{},
		&model.AdtyFundAllocation{},
		&model.AdtyManualRefund{},
		&model.AdtySiteInviteCommission{},
		&model.AdtyCommissionRecovery{},
		&model.AdtyBillingHold{},
		&model.AuditLog{},
		&model.AuditIntent{},
		&model.UserSession{},
		&model.AuthFlow{},
		&model.TwoFA{},
		&model.PasskeyCredential{},
	))

	owner7 := model.User{Username: "rf-owner-7", Password: "owner-refund-password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(&owner7).Error)
	identity, _ := createAccessTokenTestSession(t, owner7.Id, "owner-7-refund")
	site7 := model.AdtySite{
		Id: 7, OwnerUserId: owner7.Id, Subdomain: "rf-seven", RootDomain: "sites.test",
		Status: model.AdtySiteStatusActive, AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site7).Error)
	require.NoError(t, db.Create(&model.AdtySiteWallet{
		SiteId: 7, Kind: model.AdtySiteWalletOperating, Balance: decimal.NewFromInt(10),
	}).Error)
	t.Cleanup(service.InvalidateAdtySiteCache)

	buyer := model.User{Username: "rf-buyer-7", Role: common.RoleCommonUser, SiteId: 7, Quota: 5_000_000}
	require.NoError(t, db.Create(&buyer).Error)
	require.NoError(t, db.Create(&model.AdtyFundBatch{
		UserId: buyer.Id, SiteId: model.AdtyFundBatchSiteID(7),
		Kind: model.AdtyFundBatchKindPrincipal, Source: "OWN-RF-7",
		Original: 5_000_000, Remaining: 5_000_000, Status: model.AdtyFundBatchStatusActive,
	}).Error)

	require.NoError(t, db.Create(&model.TopUp{
		TradeNo: "OWN-RF-7", UserId: buyer.Id, PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		PaymentMethod: "usdt.trc20", Status: common.TopUpStatusSuccess, Amount: 10, Money: 10, CreateTime: 200,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: "OWN-RF-7", UserId: buyer.Id, SiteId: 7, Provider: model.PaymentProviderAdtyBEpusdt,
		Method: "usdt.trc20", FaceUSD: decimal.NewFromInt(10), CreditQuota: 5_000_000,
		PayAmount: decimal.NewFromInt(10), PayCurrency: "USDT", PaidAmount: decimal.NewFromInt(10), PaidCurrency: "USDT",
	}).Error)
	require.NoError(t, db.Create(&model.TopUp{
		TradeNo: "OWN-RF-8", UserId: buyer.Id, PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		PaymentMethod: "usdt.trc20", Status: common.TopUpStatusSuccess, Amount: 10, Money: 10, CreateTime: 201,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: "OWN-RF-8", UserId: buyer.Id, SiteId: 8, Provider: model.PaymentProviderAdtyBEpusdt,
		Method: "usdt.trc20", FaceUSD: decimal.NewFromInt(10), CreditQuota: 5_000_000,
		PayAmount: decimal.NewFromInt(10), PayCurrency: "USDT", PaidAmount: decimal.NewFromInt(10), PaidCurrency: "USDT",
	}).Error)

	// 1. GetAdtyOwnerOrders enables Actions.Refund for own-site success order.
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/orders", nil)
	c.Set("id", owner7.Id)
	GetAdtyOwnerOrders(c)
	require.Equal(t, http.StatusOK, rec.Code)
	var ownerOrders struct {
		Success bool
		Data    struct {
			Items []struct {
				TradeNo string `json:"trade_no"`
				Actions struct {
					Complete bool `json:"complete"`
					Refund   bool `json:"refund"`
					Query    bool `json:"query"`
				} `json:"actions"`
			} `json:"items"`
		}
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &ownerOrders))
	require.Len(t, ownerOrders.Data.Items, 1)
	require.Equal(t, "OWN-RF-7", ownerOrders.Data.Items[0].TradeNo)
	require.True(t, ownerOrders.Data.Items[0].Actions.Refund)
	require.False(t, ownerOrders.Data.Items[0].Actions.Complete)
	require.False(t, ownerOrders.Data.Items[0].Actions.Query)

	// 2. Owner 7 cannot preview or refund an order belonging to Site 8.
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/orders/OWN-RF-8/refund-preview?refund_usd=4", nil)
	c.Params = gin.Params{{Key: "trade_no", Value: "OWN-RF-8"}}
	c.Set("id", owner7.Id)
	PreviewAdtyOwnerOrderRefund(c)
	require.Equal(t, http.StatusNotFound, rec.Code)

	// 3. Owner 7 previews partial refund ($4) on own-site order OWN-RF-7.
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/orders/OWN-RF-7/refund-preview?refund_usd=4", nil)
	c.Params = gin.Params{{Key: "trade_no", Value: "OWN-RF-7"}}
	c.Set("id", owner7.Id)
	PreviewAdtyOwnerOrderRefund(c)
	require.Equal(t, http.StatusOK, rec.Code)

	// 4. Submitting RefundAdtyOwnerOrder without security proof fails with 403.
	bodyBytes, _ := common.Marshal(map[string]any{
		"reason":          "partial refund requested by buyer",
		"idempotency_key": "own-rf-apply-1",
		"refund_usd":      "4",
	})
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/orders/OWN-RF-7/refund", bytes.NewReader(bodyBytes))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "trade_no", Value: "OWN-RF-7"}}
	c.Set("id", owner7.Id)
	c.Set("auth_identity", identity)
	RefundAdtyOwnerOrder(c)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// 5. Submitting RefundAdtyOwnerOrder with valid security proof succeeds and creates partial refund.
	op := service.AdtyOwnerOrderRefundOperation("OWN-RF-7", "partial refund requested by buyer", "4", nil)
	proof := issueSecurityEnrollmentProof(t, identity, op, service.VerificationMethodPassword)

	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/orders/OWN-RF-7/refund", bytes.NewReader(bodyBytes))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Security-Proof", proof)
	c.Params = gin.Params{{Key: "trade_no", Value: "OWN-RF-7"}}
	c.Set("id", owner7.Id)
	c.Set("auth_identity", identity)
	RefundAdtyOwnerOrder(c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var applyResp struct {
		Success bool
		Data    struct {
			Refund struct {
				RefundId     string          `json:"refund_id"`
				Status       string          `json:"status"`
				RequestUSD   decimal.Decimal `json:"request_usd"`
				RequestQuota int64           `json:"request_quota"`
			} `json:"refund"`
		}
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &applyResp))
	require.True(t, applyResp.Success)
	require.NotEmpty(t, applyResp.Data.Refund.RefundId)
	require.Equal(t, model.AdtyManualRefundStatusSubmitted, applyResp.Data.Refund.Status)
	require.True(t, decimal.NewFromInt(4).Equal(applyResp.Data.Refund.RequestUSD))
	require.EqualValues(t, 2_000_000, applyResp.Data.Refund.RequestQuota)
}
