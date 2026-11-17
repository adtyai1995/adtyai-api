package service

import (
	"context"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEpayProviderQueryUsesTrustedHTTPSAndOrderBinding(t *testing.T) {
	db := prepareEpayDB(t)
	paid, wrongOrder, wrongType, wrongAmount, redirect := false, false, false, false, false
	requests := 0
	leaked := 0
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked++
		_, _ = w.Write([]byte(`{"code":1,"status":0}`))
	}))
	t.Cleanup(redirectTarget.Close)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if redirect {
			http.Redirect(w, r, redirectTarget.URL+"?"+r.URL.RawQuery, http.StatusFound)
			return
		}
		assert.Equal(t, "/api.php", r.URL.Path)
		assert.Equal(t, "order", r.URL.Query().Get("act"))
		assert.Equal(t, "EPY-admin-query", r.URL.Query().Get("out_trade_no"))
		assert.Equal(t, "query-merchant", r.URL.Query().Get("pid"))
		assert.Equal(t, "query-secret", r.URL.Query().Get("key"))
		status, tradeNo, method, amount := 0, "EPY-admin-query", "alipay", "730.00"
		if paid {
			status = 1
		}
		if wrongOrder {
			tradeNo = "another-order"
		}
		if wrongType {
			method = "wxpay"
		}
		if wrongAmount {
			amount = "729.99"
		}
		body, err := common.Marshal(map[string]any{"code": 1, "status": status, "out_trade_no": tradeNo,
			"pid": "query-merchant", "trade_no": "gw-query", "type": method, "money": amount})
		require.NoError(t, err)
		_, err = w.Write(body)
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	oldClient := adtyEpayHTTPClient
	adtyEpayHTTPClient = server.Client
	t.Cleanup(func() { adtyEpayHTTPClient = oldClient })
	channel := seedEpayChannel(t, db, model.AdtyEpayChannel{Name: "admin-query", GatewayURL: server.URL,
		MerchantId: "query-merchant", MerchantKey: "query-secret"}, alipay("qrcode"))
	seedEpayOrder(t, db, channel.Id, "EPY-admin-query", common.GetTimestamp())
	require.NoError(t, model.UpdateAdtyTopupOrderFields("EPY-admin-query", map[string]any{"gateway_trade_id": "gw-query"}))
	ext, err := model.GetAdtyTopupOrder("EPY-admin-query")
	require.NoError(t, err)
	_, querySupported := AdtyPaymentProviderByName(model.PaymentProviderAdtyEpay).(AdtyPaymentOrderQuerier)
	require.True(t, querySupported, "Epay must expose its authenticated query capability")
	require.NoError(t, RecoverAdtyHostedPayment(t.Context(), ext))
	assert.Equal(t, common.TopUpStatusPending, topUpStatusOf(t, db, ext.TradeNo))
	paid = true
	for _, failure := range []string{"order", "method", "amount", "http"} {
		wrongOrder, wrongType, wrongAmount = failure == "order", failure == "method", failure == "amount"
		if failure == "http" {
			require.NoError(t, db.Model(&model.AdtyEpayChannel{}).Where("id = ?", channel.Id).Update("gateway_url", "http://pay.example.test").Error)
		}
		before := requests
		require.Error(t, RecoverAdtyHostedPayment(t.Context(), ext), failure)
		assert.Equal(t, common.TopUpStatusPending, topUpStatusOf(t, db, ext.TradeNo), failure)
		if failure == "http" {
			assert.Equal(t, before, requests, "unencrypted query must not be sent")
		}
	}
	require.NoError(t, db.Model(&model.AdtyEpayChannel{}).Where("id = ?", channel.Id).Update("gateway_url", server.URL).Error)
	wrongOrder, wrongType, wrongAmount = false, false, false
	redirect = true
	require.Error(t, RecoverAdtyHostedPayment(t.Context(), ext))
	assert.Zero(t, leaked, "HTTPS query credentials must never be forwarded to a redirect")
	redirect = false
	for range 2 {
		require.NoError(t, RecoverAdtyHostedPaymentAs(t.Context(), ext, 88))
	}
	var buyer model.User
	require.NoError(t, db.First(&buyer, ext.UserId).Error)
	assert.Equal(t, 50000000, buyer.Quota)
	var credits int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Where("ref_id = ?", ext.TradeNo).Count(&credits).Error)
	assert.EqualValues(t, 1, credits)
	var history []model.AdtyOrderPaymentEvent
	require.NoError(t, db.Where("trade_no = ?", ext.TradeNo).Order("id").Find(&history).Error)
	require.Len(t, history, 8)
	assert.Equal(t, "unpaid", history[0].Outcome)
	for i, event := range history[1:6] {
		expected := "failed"
		if i == 2 {
			expected = "rejected"
		}
		assert.Equal(t, expected, event.Outcome)
		assert.True(t, event.PaidAmount.IsZero(), "failed query amounts are not trusted payment facts")
		assert.Empty(t, event.PaidCurrency)
	}
	assert.Equal(t, "credited", history[6].Outcome)
	assert.Equal(t, "replayed", history[7].Outcome)
	assert.Equal(t, 88, history[6].OperatorId)
	assert.Equal(t, 88, history[7].OperatorId)
}

func TestEpaySignMatchesGoEpay(t *testing.T) {
	params := map[string]string{"pid": "1001", "type": "alipay", "out_trade_no": "EPY1", "money": "730.00", "name": "Top-up", "notify_url": "https://a/n", "sign_type": "MD5", "empty": ""}
	want := epay.GenerateParams(params, "secret-key")["sign"]
	got, err := EpaySign(params, "secret-key")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestEpaySignRejectsAmpersand(t *testing.T) {
	_, err := EpaySign(map[string]string{"name": "a&b", "pid": "1"}, "k")
	require.Error(t, err)
	_, err = EpaySign(map[string]string{"a=b": "1"}, "k")
	require.Error(t, err)
}

func TestEpayVerifyRejectsTampered(t *testing.T) {
	params := map[string]string{"pid": "1001", "money": "730.00", "out_trade_no": "EPY1", "trade_status": "TRADE_SUCCESS"}
	sign, err := EpaySign(params, "k")
	require.NoError(t, err)
	params["sign"] = sign
	assert.True(t, EpayVerify(params, "k"))
	params["money"] = "1.00"
	assert.False(t, EpayVerify(params, "k"))
	params["money"] = "730.00"
	assert.False(t, EpayVerify(params, "other-key"))
}

func TestAdtyEpayChannelKeyNotSerialized(t *testing.T) {
	raw, err := common.Marshal(model.AdtyEpayChannel{Name: "c", MerchantKey: "super-secret"})
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "super-secret")
}

func seedEpayChannel(t *testing.T, db *gorm.DB, channel model.AdtyEpayChannel, methods ...model.AdtyEpayChannelMethod) *model.AdtyEpayChannel {
	t.Helper()
	if channel.Status == "" {
		channel.Status = model.AdtyEpayStatusEnabled
	}
	if channel.OrderMode == "" {
		channel.OrderMode = model.AdtyEpayOrderModeAPI
	}
	if channel.MerchantKey == "" {
		channel.MerchantKey = "key"
	}
	channel.Methods = methods
	require.NoError(t, model.SaveAdtyEpayChannel(&channel, false))
	return &channel
}

func alipay(returnType string) model.AdtyEpayChannelMethod {
	return model.AdtyEpayChannelMethod{Method: "alipay", Enabled: true, ReturnType: returnType}
}

func prepareEpayDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}, &model.AuditLog{}))
	require.NoError(t, model.MigrateAdtyTables())
	oldRand := adtyEpayRand
	adtyEpayRand = func() *rand.Rand { return rand.New(rand.NewPCG(1, 2)) }
	t.Cleanup(func() { adtyEpayRand = oldRand })
	return db
}

func TestPickPriorityThenWeight(t *testing.T) {
	prepareEpayDB(t)
	low := seedEpayChannel(t, nil, model.AdtyEpayChannel{Name: "low", Priority: 1, Weight: 100}, alipay("qrcode"))
	high := seedEpayChannel(t, nil, model.AdtyEpayChannel{Name: "high", Priority: 9, Weight: 1}, alipay("qrcode"))
	picked, _, err := PickAdtyEpayChannel("alipay", decimal.NewFromInt(10), "pc", nil, common.GetTimestamp())
	require.NoError(t, err)
	assert.Equal(t, high.Id, picked.Id)
	picked, _, err = PickAdtyEpayChannel("alipay", decimal.NewFromInt(10), "pc", map[int]bool{high.Id: true}, common.GetTimestamp())
	require.NoError(t, err)
	assert.Equal(t, low.Id, picked.Id)
	_, _, err = PickAdtyEpayChannel("wxpay", decimal.NewFromInt(10), "pc", nil, common.GetTimestamp())
	require.ErrorIs(t, err, ErrAdtyEpayNoChannel)
}

func TestPickDesktopPrefersQRCode(t *testing.T) {
	prepareEpayDB(t)
	seedEpayChannel(t, nil, model.AdtyEpayChannel{Name: "link", Priority: 5, Weight: 1000}, alipay("payurl"))
	qr := seedEpayChannel(t, nil, model.AdtyEpayChannel{Name: "qr", Priority: 5, Weight: 1}, alipay("qrcode"))
	for range 5 {
		picked, _, err := PickAdtyEpayChannel("alipay", decimal.NewFromInt(10), "pc", nil, common.GetTimestamp())
		require.NoError(t, err)
		assert.Equal(t, qr.Id, picked.Id)
	}
}

func TestPickRespectsAmountRangeAndDailyLimit(t *testing.T) {
	prepareEpayDB(t)
	limited := alipay("qrcode")
	limited.MinCNY, limited.MaxCNY, limited.DailyLimitCNY = decimal.NewFromInt(5), decimal.NewFromInt(500), decimal.NewFromInt(1000)
	channel := seedEpayChannel(t, nil, model.AdtyEpayChannel{Name: "limited", Priority: 1, Weight: 1}, limited)
	_, _, err := PickAdtyEpayChannel("alipay", decimal.NewFromInt(4), "pc", nil, common.GetTimestamp())
	require.ErrorIs(t, err, ErrAdtyEpayNoChannel, "below the minimum")
	_, _, err = PickAdtyEpayChannel("alipay", decimal.NewFromInt(501), "pc", nil, common.GetTimestamp())
	require.ErrorIs(t, err, ErrAdtyEpayNoChannel, "above the maximum")
	oldSum := adtyEpaySumToday
	adtyEpaySumToday = func(int, string, int64) (decimal.Decimal, error) { return decimal.NewFromInt(900), nil }
	t.Cleanup(func() { adtyEpaySumToday = oldSum })
	_, _, err = PickAdtyEpayChannel("alipay", decimal.NewFromInt(200), "pc", nil, common.GetTimestamp())
	require.ErrorIs(t, err, ErrAdtyEpayNoChannel, "over the daily limit")
	picked, _, err := PickAdtyEpayChannel("alipay", decimal.NewFromInt(100), "pc", nil, common.GetTimestamp())
	require.NoError(t, err)
	assert.Equal(t, channel.Id, picked.Id)
}

func TestSumAdtyEpayPaidTodayCountsOnlySuccess(t *testing.T) {
	db := prepareEpayDB(t)
	channel := seedEpayChannel(t, db, model.AdtyEpayChannel{Name: "sum", Priority: 1, Weight: 1}, alipay("qrcode"))
	now := common.GetTimestamp()
	for i, status := range []string{common.TopUpStatusSuccess, common.TopUpStatusSuccess, common.TopUpStatusPending} {
		tradeNo := "EPY-sum-" + string(rune('a'+i))
		require.NoError(t, model.CreateAdtyTopUp(
			&model.TopUp{UserId: 1, TradeNo: tradeNo, PaymentProvider: model.PaymentProviderAdtyEpay, Status: status, CompleteTime: now},
			&model.AdtyTopupOrder{TradeNo: tradeNo, Provider: model.PaymentProviderAdtyEpay, Method: "alipay", ChannelId: channel.Id, PayAmount: decimal.NewFromInt(100)},
		))
	}
	sum, err := model.SumAdtyEpayPaidToday(channel.Id, "alipay", adtyDayStart(now))
	require.NoError(t, err)
	assert.Equal(t, "200", sum.String())
}

func seedEpayOrder(t *testing.T, db *gorm.DB, channelID int, tradeNo string, createdAt int64) {
	t.Helper()
	user := &model.User{Username: "u-" + tradeNo, Password: "hashed-password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
	require.NoError(t, db.Create(user).Error)
	require.NoError(t, model.CreateAdtyTopUp(
		&model.TopUp{UserId: user.Id, Amount: 100, TradeNo: tradeNo, PaymentMethod: "alipay", PaymentProvider: model.PaymentProviderAdtyEpay, Status: common.TopUpStatusPending},
		&model.AdtyTopupOrder{TradeNo: tradeNo, UserId: user.Id, Provider: model.PaymentProviderAdtyEpay, Method: "alipay", ChannelId: channelID,
			FaceUSD: decimal.NewFromInt(100), CreditQuota: 50_000_000, PayAmount: decimal.RequireFromString("730.00"), PayCurrency: "CNY",
			TopupGroupRatio: decimal.NewFromInt(1), ExpireAt: createdAt + 600},
	))
	require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("trade_no = ?", tradeNo).Update("created_at", createdAt).Error)
}

func stubEpayQuery(t *testing.T, result *AdtyEpayQueryResult) *int {
	t.Helper()
	calls := 0
	old := adtyEpayQuery
	adtyEpayQuery = func(context.Context, *model.AdtyEpayChannel, string) (*AdtyEpayQueryResult, error) {
		calls++
		return result, nil
	}
	t.Cleanup(func() { adtyEpayQuery = old })
	return &calls
}

func topUpStatusOf(t *testing.T, db *gorm.DB, tradeNo string) string {
	t.Helper()
	var topUp model.TopUp
	require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&topUp).Error)
	return topUp.Status
}

func TestEpayRecoveryBindsPaymentIdentity(t *testing.T) {
	for _, entry := range []string{"automatic", "manual"} {
		for _, status := range []string{common.TopUpStatusPending, common.TopUpStatusExpired} {
			for _, test := range []struct {
				name, order, merchant, gateway, storedGateway string
				wantCredit                                    bool
			}{
				{"valid_known_gateway", "EPY-identity", "identity-merchant", "gw-identity", "gw-identity", true},
				{"valid_unknown_gateway", "EPY-identity", "identity-merchant", "gw-identity", "", true},
				{"wrong_order", "another-order", "identity-merchant", "gw-identity", "gw-identity", false},
				{"empty_order", "", "identity-merchant", "gw-identity", "gw-identity", false},
				{"wrong_merchant", "EPY-identity", "another-merchant", "gw-identity", "gw-identity", false},
				{"empty_merchant", "EPY-identity", "", "gw-identity", "gw-identity", false},
				{"wrong_gateway", "EPY-identity", "identity-merchant", "another-gateway", "gw-identity", false},
				{"empty_gateway", "EPY-identity", "identity-merchant", "", "", false},
				{"blank_gateway", "EPY-identity", "identity-merchant", "   ", "", false},
			} {
				t.Run(entry+"/"+status+"/"+test.name, func(t *testing.T) {
					db := prepareReferralDB(t)
					pgtest.RecordServerVersion(t, db)
					settings := adty_setting.GetEpaySetting()
					oldAfter, oldGrace := settings.QueryAfterSeconds, settings.ExpiredQuerySeconds
					settings.QueryAfterSeconds = 120
					settings.ExpiredQuerySeconds = 3600
					t.Cleanup(func() { settings.QueryAfterSeconds, settings.ExpiredQuerySeconds = oldAfter, oldGrace })
					var mutex sync.Mutex
					reply := map[string]any{"code": 1, "status": 1, "out_trade_no": test.order,
						"pid": test.merchant, "trade_no": test.gateway, "type": "alipay", "money": "730.00"}
					server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
						assert.Equal(t, "/api.php", request.URL.Path)
						assert.Equal(t, "EPY-identity", request.URL.Query().Get("out_trade_no"))
						assert.Equal(t, "identity-merchant", request.URL.Query().Get("pid"))
						mutex.Lock()
						body, err := common.Marshal(reply)
						mutex.Unlock()
						if !assert.NoError(t, err) {
							http.Error(w, "fixture response unavailable", http.StatusInternalServerError)
							return
						}
						_, err = w.Write(body)
						assert.NoError(t, err)
					}))
					t.Cleanup(server.Close)
					oldClient := adtyEpayHTTPClient
					adtyEpayHTTPClient = server.Client
					t.Cleanup(func() { adtyEpayHTTPClient = oldClient })
					channel := seedEpayChannel(t, db, model.AdtyEpayChannel{Name: "identity", GatewayURL: server.URL,
						MerchantId: "identity-merchant", MerchantKey: "identity-secret"}, alipay("qrcode"))
					now := common.GetTimestamp()
					created := now - 300
					if status == common.TopUpStatusExpired {
						created = now - 1000
					}
					seedEpayOrder(t, db, channel.Id, "EPY-identity", created)
					if status == common.TopUpStatusExpired {
						require.NoError(t, db.Model(&model.TopUp{}).Where("trade_no = ?", "EPY-identity").Update("status", status).Error)
					}
					require.NoError(t, model.UpdateAdtyTopupOrderFields("EPY-identity", map[string]any{"gateway_trade_id": test.storedGateway}))
					ext, err := model.GetAdtyTopupOrder("EPY-identity")
					require.NoError(t, err)
					if entry == "automatic" {
						runAdtyEpayFollowUpQueries(now)
					} else {
						err = RecoverAdtyHostedPayment(t.Context(), ext)
						if test.wantCredit {
							require.NoError(t, err)
						} else {
							assert.Error(t, err, "unbound facts must not be treated as a verified receipt")
						}
					}
					var user model.User
					require.NoError(t, db.First(&user, ext.UserId).Error)
					var credits int64
					require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Where("ref_id = ?", ext.TradeNo).Count(&credits).Error)
					if test.wantCredit {
						assert.Equal(t, common.TopUpStatusSuccess, topUpStatusOf(t, db, ext.TradeNo))
						assert.Equal(t, 50_000_000, user.Quota)
						assert.EqualValues(t, 1, credits)
					} else {
						assert.Equal(t, status, topUpStatusOf(t, db, ext.TradeNo))
						assert.Zero(t, user.Quota, "same amount alone cannot authorize any credit")
						assert.Zero(t, credits)
						var stored model.AdtyTopupOrder
						require.NoError(t, db.First(&stored, ext.Id).Error)
						assert.Equal(t, test.storedGateway, stored.GatewayTradeId)
						assert.Empty(t, stored.PaidTxHash)
						var attempt model.AdtyOrderPaymentEvent
						require.NoError(t, db.Where("trade_no = ? AND source = ? AND outcome = ?", ext.TradeNo, model.AdtyPaymentSourceQuery, "failed").First(&attempt).Error)
						assert.Equal(t, ext.Provider, attempt.Provider)
						assert.Equal(t, ext.UserId, attempt.UserId)
						if entry == "automatic" {
							var review model.PaymentOrphanEvent
							require.NoError(t, db.Where("provider = ? AND reference_id = ? AND event_type = ?", ext.Provider, ext.TradeNo, "payment_query").First(&review).Error)
							assert.Equal(t, model.PaymentOrphanStatusPendingReview, review.Status)
							assert.Equal(t, test.storedGateway, review.SessionID)
						}
					}
					// Correct evidence can subsequently recover the same original order.
					mutex.Lock()
					reply["out_trade_no"], reply["pid"], reply["trade_no"] = "EPY-identity", "identity-merchant", "gw-identity"
					mutex.Unlock()
					if entry == "automatic" {
						runAdtyEpayFollowUpQueries(now + 61)
					} else {
						require.NoError(t, RecoverAdtyHostedPayment(t.Context(), ext))
					}
					var recovered model.User
					require.NoError(t, db.First(&recovered, ext.UserId).Error)
					assert.Equal(t, 50_000_000, recovered.Quota)
					require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Where("ref_id = ?", ext.TradeNo).Count(&credits).Error)
					assert.EqualValues(t, 1, credits, "rejection, correction and replay have one financial effect")
				})
			}
		}
	}
}

func TestEpayRecoveryCannotReuseAnotherOrdersReceipt(t *testing.T) {
	db := prepareReferralDB(t)
	pgtest.RecordServerVersion(t, db)
	settings := adty_setting.GetEpaySetting()
	oldAfter := settings.QueryAfterSeconds
	settings.QueryAfterSeconds = 120
	t.Cleanup(func() { settings.QueryAfterSeconds = oldAfter })
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		// The gateway mistakenly returns order A's complete response for both lookups.
		_, err := w.Write([]byte(`{"code":1,"status":1,"out_trade_no":"EPY-bound-a","pid":"bound-merchant","trade_no":"gw-bound-a","type":"alipay","money":"730.00"}`))
		assert.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	oldClient := adtyEpayHTTPClient
	adtyEpayHTTPClient = server.Client
	t.Cleanup(func() { adtyEpayHTTPClient = oldClient })
	channel := seedEpayChannel(t, db, model.AdtyEpayChannel{Name: "bound", GatewayURL: server.URL,
		MerchantId: "bound-merchant", MerchantKey: "bound-secret"}, alipay("qrcode"))
	now := common.GetTimestamp()
	for _, suffix := range []string{"a", "b"} {
		seedEpayOrder(t, db, channel.Id, "EPY-bound-"+suffix, now-300)
		require.NoError(t, model.UpdateAdtyTopupOrderFields("EPY-bound-"+suffix, map[string]any{"gateway_trade_id": "gw-bound-" + suffix}))
	}
	runAdtyEpayFollowUpQueries(now)
	runAdtyEpayFollowUpQueries(now + 61)
	for _, test := range []struct {
		trade, status string
		quota         int
		credits       int64
	}{
		{"EPY-bound-a", common.TopUpStatusSuccess, 50_000_000, 1},
		{"EPY-bound-b", common.TopUpStatusPending, 0, 0},
	} {
		ext, err := model.GetAdtyTopupOrder(test.trade)
		require.NoError(t, err)
		var user model.User
		require.NoError(t, db.First(&user, ext.UserId).Error)
		assert.Equal(t, test.quota, user.Quota, test.trade)
		assert.Equal(t, test.status, topUpStatusOf(t, db, test.trade))
		var credits int64
		require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Where("ref_id = ?", test.trade).Count(&credits).Error)
		assert.Equal(t, test.credits, credits)
	}
}

func TestEpayRecoveryAndCallbackShareOneCredit(t *testing.T) {
	db := prepareReferralDB(t)
	pgtest.RecordServerVersion(t, db)
	settings := adty_setting.GetEpaySetting()
	oldAfter := settings.QueryAfterSeconds
	settings.QueryAfterSeconds = 120
	t.Cleanup(func() { settings.QueryAfterSeconds = oldAfter })
	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(resume) }) }
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		close(entered)
		<-resume
		_, err := w.Write([]byte(`{"code":1,"status":1,"out_trade_no":"EPY-race-query","pid":"race-merchant","trade_no":"gw-race-query","type":"alipay","money":"730.00"}`))
		assert.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	oldClient := adtyEpayHTTPClient
	adtyEpayHTTPClient = server.Client
	t.Cleanup(func() { adtyEpayHTTPClient = oldClient })
	channel := seedEpayChannel(t, db, model.AdtyEpayChannel{Name: "race-query", GatewayURL: server.URL,
		MerchantId: "race-merchant", MerchantKey: "race-secret"}, alipay("qrcode"))
	now := common.GetTimestamp()
	seedEpayOrder(t, db, channel.Id, "EPY-race-query", now-300)
	require.NoError(t, model.UpdateAdtyTopupOrderFields("EPY-race-query", map[string]any{"gateway_trade_id": "gw-race-query"}))
	finished := make(chan struct{})
	go func() { defer close(finished); runAdtyEpayFollowUpQueries(now) }()
	t.Cleanup(func() { release(); <-finished })
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("query did not reach the gateway boundary")
	}
	already, err := model.CompleteAdtyTopUp("EPY-race-query", model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyEpay, Method: "alipay", PaidAmount: decimal.NewFromInt(730), PaidCurrency: "CNY",
		GatewayTradeId: "gw-race-query", TxHash: "gw-race-query", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)
	assert.False(t, already)
	release()
	<-finished
	ext, err := model.GetAdtyTopupOrder("EPY-race-query")
	require.NoError(t, err)
	var user model.User
	require.NoError(t, db.First(&user, ext.UserId).Error)
	assert.Equal(t, 50_000_000, user.Quota)
	var credits int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Where("ref_id = ?", ext.TradeNo).Count(&credits).Error)
	assert.EqualValues(t, 1, credits)
	assert.Equal(t, common.TopUpStatusSuccess, topUpStatusOf(t, db, ext.TradeNo))
}

func TestEpayFollowUpQuery(t *testing.T) {
	db := prepareEpayDB(t)
	https := seedEpayChannel(t, db, model.AdtyEpayChannel{Name: "https", GatewayURL: "https://pay.example", MerchantId: "followup-merchant", Priority: 1, Weight: 1}, alipay("qrcode"))
	plain := seedEpayChannel(t, db, model.AdtyEpayChannel{Name: "http", GatewayURL: "http://pay.example", Priority: 1, Weight: 1}, alipay("qrcode"))
	now := common.GetTimestamp()

	t.Run("credits a paid order", func(t *testing.T) {
		seedEpayOrder(t, db, https.Id, "EPY-q-paid", now-300)
		stubEpayQuery(t, &AdtyEpayQueryResult{Paid: true, MoneyCNY: decimal.RequireFromString("730"), TypeCode: "alipay", GatewayTradeNo: "G1", OutTradeNo: "EPY-q-paid", MerchantId: "followup-merchant"})
		runAdtyEpayFollowUpQueries(now)
		assert.Equal(t, common.TopUpStatusSuccess, topUpStatusOf(t, db, "EPY-q-paid"))
	})
	t.Run("checks money and type", func(t *testing.T) {
		seedEpayOrder(t, db, https.Id, "EPY-q-money", now-300)
		stubEpayQuery(t, &AdtyEpayQueryResult{Paid: true, MoneyCNY: decimal.RequireFromString("1"), TypeCode: "alipay", GatewayTradeNo: "G-money", OutTradeNo: "EPY-q-money", MerchantId: "followup-merchant"})
		runAdtyEpayFollowUpQueries(now)
		assert.Equal(t, common.TopUpStatusPending, topUpStatusOf(t, db, "EPY-q-money"))
		seedEpayOrder(t, db, https.Id, "EPY-q-type", now-300)
		stubEpayQuery(t, &AdtyEpayQueryResult{Paid: true, MoneyCNY: decimal.RequireFromString("730"), TypeCode: "wxpay", GatewayTradeNo: "G-type", OutTradeNo: "EPY-q-type", MerchantId: "followup-merchant"})
		runAdtyEpayFollowUpQueries(now + 61)
		assert.Equal(t, common.TopUpStatusPending, topUpStatusOf(t, db, "EPY-q-type"))
	})
	t.Run("skips young orders", func(t *testing.T) {
		seedEpayOrder(t, db, https.Id, "EPY-q-young", now-30)
		calls := stubEpayQuery(t, &AdtyEpayQueryResult{Paid: true, MoneyCNY: decimal.RequireFromString("730"), TypeCode: "alipay"})
		require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("trade_no <> ?", "EPY-q-young").Update("last_query_at", now+10_000).Error)
		runAdtyEpayFollowUpQueries(now)
		assert.Zero(t, *calls)
		assert.Equal(t, common.TopUpStatusPending, topUpStatusOf(t, db, "EPY-q-young"))
	})
	t.Run("skips http gateways", func(t *testing.T) {
		seedEpayOrder(t, db, plain.Id, "EPY-q-http", now-300)
		calls := stubEpayQuery(t, &AdtyEpayQueryResult{Paid: true, MoneyCNY: decimal.RequireFromString("730"), TypeCode: "alipay"})
		// Only the http order is due for a query.
		require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("trade_no <> ?", "EPY-q-http").Update("last_query_at", now+10_000).Error)
		runAdtyEpayFollowUpQueries(now)
		assert.Zero(t, *calls)
		assert.Equal(t, common.TopUpStatusPending, topUpStatusOf(t, db, "EPY-q-http"))
	})
}

func TestEpayFollowUpQueriesExpiredOrdersInsideGrace(t *testing.T) {
	db := prepareEpayDB(t)
	setting := adty_setting.GetEpaySetting()
	oldGrace := setting.ExpiredQuerySeconds
	setting.ExpiredQuerySeconds = 3600
	t.Cleanup(func() { setting.ExpiredQuerySeconds = oldGrace })
	https := seedEpayChannel(t, db, model.AdtyEpayChannel{Name: "https-expired", GatewayURL: "https://pay.example", MerchantId: "expired-merchant", Priority: 1, Weight: 1}, alipay("qrcode"))
	now := common.GetTimestamp()

	seedEpayOrder(t, db, https.Id, "EPY-exp-paid", now-1000)
	require.NoError(t, db.Model(&model.TopUp{}).Where("trade_no = ?", "EPY-exp-paid").Update("status", common.TopUpStatusExpired).Error)
	stubEpayQuery(t, &AdtyEpayQueryResult{Paid: true, MoneyCNY: decimal.RequireFromString("730"), TypeCode: "alipay", GatewayTradeNo: "G-exp", OutTradeNo: "EPY-exp-paid", MerchantId: "expired-merchant"})
	runAdtyEpayFollowUpQueries(now)
	assert.Equal(t, common.TopUpStatusSuccess, topUpStatusOf(t, db, "EPY-exp-paid"))
	var payer model.User
	require.NoError(t, db.Where("username = ?", "u-EPY-exp-paid").First(&payer).Error)
	assert.Equal(t, 50_000_000, payer.Quota)

	seedEpayOrder(t, db, https.Id, "EPY-exp-old", now-20_000)
	require.NoError(t, db.Model(&model.TopUp{}).Where("trade_no = ?", "EPY-exp-old").Update("status", common.TopUpStatusExpired).Error)
	require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("trade_no <> ?", "EPY-exp-old").Update("last_query_at", now+10_000).Error)
	calls := stubEpayQuery(t, &AdtyEpayQueryResult{Paid: true, MoneyCNY: decimal.RequireFromString("730"), TypeCode: "alipay"})
	runAdtyEpayFollowUpQueries(now)
	assert.Zero(t, *calls)
	assert.Equal(t, common.TopUpStatusExpired, topUpStatusOf(t, db, "EPY-exp-old"))
}

func TestEpayUnpaidStreak(t *testing.T) {
	db := prepareEpayDB(t)
	setting := adty_setting.GetEpaySetting()
	oldThreshold := setting.UnpaidThreshold
	setting.UnpaidThreshold = 2
	t.Cleanup(func() { setting.UnpaidThreshold = oldThreshold })
	notified := 0
	oldNotify := adtyNotifyRootAdmins
	adtyNotifyRootAdmins = func(render func(string) (string, string)) {
		notified++
		subject, body := render("zh-CN")
		assert.Contains(t, subject, "streak")
		assert.Contains(t, body, "2", "the body has the unpaid count")
	}
	t.Cleanup(func() { adtyNotifyRootAdmins = oldNotify })
	channel := seedEpayChannel(t, db, model.AdtyEpayChannel{Name: "streak", Priority: 1, Weight: 1}, alipay("qrcode"))
	now := common.GetTimestamp()
	seedEpayOrder(t, db, channel.Id, "EPY-s1", now)
	seedEpayOrder(t, db, channel.Id, "EPY-s2", now)
	seedEpayOrder(t, db, channel.Id, "EPY-s3", now)

	countAdtyEpayUnpaid([]string{"EPY-s1"})
	countAdtyEpayUnpaid([]string{"EPY-s1"})
	stored, err := model.GetAdtyEpayChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, 1, stored.UnpaidStreak, "an order is counted once")

	_, err = model.CompleteAdtyTopUp("EPY-s3", model.AdtyPaymentFacts{Provider: model.PaymentProviderAdtyEpay, PaidAmount: decimal.RequireFromString("730"), PaidCurrency: "CNY", Source: model.AdtyPaymentSourceCallback}, "")
	require.NoError(t, err)
	stored, _ = model.GetAdtyEpayChannel(channel.Id)
	assert.Zero(t, stored.UnpaidStreak, "a paid order resets the streak")

	countAdtyEpayUnpaid([]string{"EPY-s1", "EPY-s2"})
	stored, _ = model.GetAdtyEpayChannel(channel.Id)
	assert.Equal(t, 1, stored.UnpaidStreak, "EPY-s1 was already counted")
	seedEpayOrder(t, db, channel.Id, "EPY-s4", now)
	countAdtyEpayUnpaid([]string{"EPY-s4"})
	stored, _ = model.GetAdtyEpayChannel(channel.Id)
	assert.Equal(t, model.AdtyEpayStatusMaintenance, stored.Status)
	assert.Equal(t, model.AdtyEpayReasonUnpaid, stored.StatusReason)
	assert.Equal(t, 1, notified)
}

func TestEpayUnpaidIgnoresOrderCreditedAfterExpiry(t *testing.T) {
	db := prepareEpayDB(t)
	channel := seedEpayChannel(t, db, model.AdtyEpayChannel{Name: "late-pay", Priority: 1, Weight: 1}, alipay("qrcode"))
	now := common.GetTimestamp()
	seedEpayOrder(t, db, channel.Id, "EPY-late", now)
	seedEpayOrder(t, db, channel.Id, "EPY-still", now)
	require.NoError(t, db.Model(&model.TopUp{}).Where("trade_no IN ?", []string{"EPY-late", "EPY-still"}).Update("status", common.TopUpStatusExpired).Error)

	_, err := model.CompleteAdtyTopUp("EPY-late", model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyEpay, Method: "alipay",
		PaidAmount: decimal.RequireFromString("730.00"), PaidCurrency: "CNY", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)

	countAdtyEpayUnpaid([]string{"EPY-late", "EPY-still"})
	stored, err := model.GetAdtyEpayChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, 1, stored.UnpaidStreak)
	var counted bool
	require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("trade_no = ?", "EPY-late").Pluck("unpaid_counted", &counted).Error)
	assert.False(t, counted)
}

func TestEpayProbe(t *testing.T) {
	db := prepareEpayDB(t)
	setting := adty_setting.GetEpaySetting()
	oldEnabled := setting.ProbeEnabled
	setting.ProbeEnabled = true
	t.Cleanup(func() { setting.ProbeEnabled = oldEnabled })
	probeErr := error(nil)
	oldProbe := adtyEpayProbe
	adtyEpayProbe = func(context.Context, *model.AdtyEpayChannel) error { return probeErr }
	t.Cleanup(func() { adtyEpayProbe = oldProbe })

	auto := seedEpayChannel(t, db, model.AdtyEpayChannel{Name: "auto", ProbeMode: "order", Priority: 1, Weight: 1}, alipay("qrcode"))
	manual := seedEpayChannel(t, db, model.AdtyEpayChannel{Name: "manual", ProbeMode: "order", Priority: 1, Weight: 1}, alipay("qrcode"))
	require.NoError(t, model.SetAdtyEpayChannelStatus(manual.Id, model.AdtyEpayStatusMaintenance, model.AdtyEpayReasonManual))

	now := common.GetTimestamp()
	probeErr = assert.AnError
	runAdtyEpayProbes(now)
	stored, _ := model.GetAdtyEpayChannel(auto.Id)
	assert.Equal(t, model.AdtyEpayStatusMaintenance, stored.Status)
	assert.Equal(t, model.AdtyEpayReasonProbe, stored.StatusReason)

	probeErr = nil
	for i := 1; i <= 3; i++ {
		runAdtyEpayProbes(now + int64(i)*600)
	}
	stored, _ = model.GetAdtyEpayChannel(auto.Id)
	assert.Equal(t, model.AdtyEpayStatusEnabled, stored.Status, "recovers after three good probes")
	stored, _ = model.GetAdtyEpayChannel(manual.Id)
	assert.Equal(t, model.AdtyEpayStatusMaintenance, stored.Status, "manual maintenance is never lifted")
}
