package router

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const bepusdtTestSecret = "bepusdt-test-secret"

type bepusdtGateway struct {
	mu                   sync.Mutex
	createStatus         int
	cancelStatus         int
	cancelMessage        string
	dropCancel           bool
	creates              int
	cancels              []string
	lastRedirect         string
	beforeCreateResponse func(string)
}

type bepusdtGatewaySnapshot struct {
	creates      int
	cancels      []string
	lastRedirect string
}

func (gw *bepusdtGateway) snapshot() bepusdtGatewaySnapshot {
	gw.mu.Lock()
	defer gw.mu.Unlock()
	return bepusdtGatewaySnapshot{
		creates:      gw.creates,
		cancels:      append([]string(nil), gw.cancels...),
		lastRedirect: gw.lastRedirect,
	}
}

// useFakeBEpusdt 配好 BEpusdt 并把网关指向一个假服务。
func useFakeBEpusdt(t *testing.T) *bepusdtGateway {
	t.Helper()
	gw := &bepusdtGateway{createStatus: 200, cancelStatus: 200}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := map[string]any{}
		_ = common.Unmarshal(raw, &body)
		gw.mu.Lock()
		defer gw.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/cancel-transaction") {
			gw.cancels = append(gw.cancels, body["trade_id"].(string))
			if gw.dropCancel {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
				return
			}
			message := gw.cancelMessage
			if message == "" {
				message = "x"
			}
			_, _ = w.Write([]byte(`{"status_code":` + strconv.Itoa(gw.cancelStatus) + `,"message":"` + message + `","data":{}}`))
			return
		}
		gw.creates++
		if redirect, ok := body["redirect_url"].(string); ok {
			gw.lastRedirect = redirect
		}
		if gw.createStatus != 200 {
			_, _ = w.Write([]byte(`{"status_code":400,"message":"no address","data":null}`))
			return
		}
		if gw.beforeCreateResponse != nil {
			gw.beforeCreateResponse(body["order_id"].(string))
		}
		_, _ = w.Write([]byte(`{"status_code":200,"message":"success","data":{"trade_id":"tid-` + body["order_id"].(string) +
			`","order_id":"` + body["order_id"].(string) + `","amount":"100","actual_amount":"100.01","token":"TAddr","expiration_time":900,"status":1,"payment_url":""}}`))
	}))
	t.Cleanup(server.Close)
	cfg := setting.GetBEpusdtSetting()
	saved := *cfg
	cfg.Enabled, cfg.BaseURL, cfg.Secret, cfg.TradeTypes = true, server.URL, bepusdtTestSecret, []string{"usdt.trc20"}
	t.Cleanup(func() { *cfg = saved })
	return gw
}

// signedNotify 按网关的规则签一个回调。
func signedNotify(values map[string]any) string {
	raw, _ := common.Marshal(values)
	decoded := map[string]any{}
	_ = common.Unmarshal(raw, &decoded)
	values["signature"] = service.BEpusdtSign(decoded, bepusdtTestSecret)
	out, _ := common.Marshal(values)
	return string(out)
}

func notifyFor(tradeNo string, status int, paid float64, address string) string {
	return signedNotify(map[string]any{
		"trade_id": "tid-" + tradeNo, "order_id": tradeNo, "amount": 100, "actual_amount": paid,
		"token": address, "block_transaction_id": "0xhash", "status": status,
	})
}

func payBEpusdt(t *testing.T, f *adtyGuardFixture, token string) model.AdtyTopUpView {
	t.Helper()
	resp := adtyJSON(t, f.engine, http.MethodPost, "/api/user/bepusdt/pay", `{"amount":100,"chain":"usdt.trc20"}`, token)
	require.Equal(t, http.StatusOK, resp.Code)
	return decodeAdtyData[model.AdtyTopUpView](t, resp.Body)
}

func bepusdtBuyer(t *testing.T, f *adtyGuardFixture, name string) (string, int) {
	t.Helper()
	token := adtySessionToken(t, f.db, name, common.RoleCommonUser)
	var user model.User
	require.NoError(t, f.db.Where("username = ?", name).First(&user).Error)
	return token, user.Id
}

func quotaOf(t *testing.T, f *adtyGuardFixture, id int) int {
	t.Helper()
	var user model.User
	require.NoError(t, f.db.First(&user, id).Error)
	return user.Quota
}

func TestBEpusdtPayRedirectsThroughSignedResult(t *testing.T) {
	f := newAdtyGuardFixture(t)
	gw := useFakeBEpusdt(t)
	token, _ := bepusdtBuyer(t, f, "usdt-redirect")
	view := payBEpusdt(t, f, token)
	snapshot := gw.snapshot()
	assert.Contains(t, snapshot.lastRedirect, "/pay/result/"+view.TradeNo)
	assert.NotContains(t, snapshot.lastRedirect, "/wallet")
}

func TestBEpusdtPayCreatesOrderWithBonus(t *testing.T) {
	f := newAdtyGuardFixture(t)
	setRouterBonusRates(t)
	useFakeBEpusdt(t)
	token, _ := bepusdtBuyer(t, f, "usdt-buyer")
	view := payBEpusdt(t, f, token)
	require.NotNil(t, view.Ext)
	assert.True(t, strings.HasPrefix(view.TradeNo, "EPU"))
	assert.Equal(t, "TAddr", view.Ext.PayAddress)
	assert.Equal(t, "100.01", view.Ext.PayAmount.String(), "pay amount comes from the gateway")
	assert.Equal(t, "2.5", view.Ext.BonusUSD.String())
	assert.EqualValues(t, 51_250_000, view.Ext.CreditQuota)
	assert.Positive(t, view.Ext.ExpireAt)
}

func TestBEpusdtPayEarlyCallbackPreservesGatewayDetailsAndLockOrder(t *testing.T) {
	f := newAdtyGuardFixture(t)
	setRouterBonusRates(t)
	gw := useFakeBEpusdt(t)
	token, userID := bepusdtBuyer(t, f, "usdt-early-callback")
	before := quotaOf(t, f, userID)
	callback := make(chan string, 1)
	gw.mu.Lock()
	gw.beforeCreateResponse = func(tradeNo string) {
		req := httptest.NewRequest(http.MethodPost, "/api/user/bepusdt/notify", strings.NewReader(notifyFor(tradeNo, 2, 100.01, "TAddr")))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		f.engine.ServeHTTP(resp, req)
		callback <- resp.Body.String()
	}
	gw.mu.Unlock()
	checked, topUpLocked := false, false
	require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("bepusdt-response-lock-order", func(tx *gorm.DB) {
		fields, ok := tx.Statement.Dest.(map[string]any)
		if !ok || tx.Statement.Table != "adty_topup_orders" || fields["pay_address"] == nil {
			return
		}
		checked = true
		topUpLocked = f.db.Transaction(func(other *gorm.DB) error {
			return other.Exec("SELECT id FROM top_ups WHERE user_id = ? FOR UPDATE NOWAIT", userID).Error
		}) != nil
	}))
	t.Cleanup(func() { _ = f.db.Callback().Update().Remove("bepusdt-response-lock-order") })
	view := payBEpusdt(t, f, token)
	require.Equal(t, "ok", <-callback)
	require.True(t, checked)
	require.True(t, topUpLocked, "response writeback must lock top-up before extension, matching callback order")
	require.Equal(t, common.TopUpStatusSuccess, view.Status)
	require.Equal(t, "TAddr", view.Ext.PayAddress)
	require.Equal(t, "100.01", view.Ext.PayAmount.String())
	require.Equal(t, "tid-"+view.TradeNo, view.Ext.GatewayTradeId)
	require.Equal(t, "0xhash", view.Ext.PaidTxHash)
	require.Equal(t, before+51_250_000, quotaOf(t, f, userID))
}

// 网关下单失败时订单改成 failed。2026-11-01 取消了支付合规确认，原来「没确认时不下单」那一段删掉了：
// 不确认也能下单由 TestAdtyWalletTopUpAPIs 覆盖。
func TestBEpusdtPayHandlesGatewayFailure(t *testing.T) {
	f := newAdtyGuardFixture(t)
	gw := useFakeBEpusdt(t)
	token, userID := bepusdtBuyer(t, f, "usdt-fail")
	gw.mu.Lock()
	gw.createStatus = 400
	gw.mu.Unlock()
	failed := adtyJSON(t, f.engine, http.MethodPost, "/api/user/bepusdt/pay", `{"amount":100,"chain":"usdt.trc20"}`, token)
	assert.Contains(t, failed.Body.String(), `"success":false`)
	var topUp model.TopUp
	require.NoError(t, f.db.Where("user_id = ?", userID).First(&topUp).Error)
	assert.Equal(t, common.TopUpStatusFailed, topUp.Status)
}

func TestBEpusdtNotify(t *testing.T) {
	f := newAdtyGuardFixture(t)
	setRouterBonusRates(t)
	useFakeBEpusdt(t)
	token, userID := bepusdtBuyer(t, f, "usdt-notify")
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/user/bepusdt/notify", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		f.engine.ServeHTTP(resp, req)
		return resp
	}
	reviews := func(tradeNo string) int64 {
		var count int64
		require.NoError(t, f.db.Model(&model.PaymentOrphanEvent{}).Where("reference_id = ?", tradeNo).Count(&count).Error)
		return count
	}

	t.Run("bad signature", func(t *testing.T) {
		view := payBEpusdt(t, f, token)
		body := strings.Replace(notifyFor(view.TradeNo, 2, 100.01, "TAddr"), `"status":2`, `"status":2,"extra":"x"`, 1)
		resp := post(body)
		assert.Equal(t, http.StatusBadRequest, resp.Code)
	})

	t.Run("waiting status does not credit", func(t *testing.T) {
		before := quotaOf(t, f, userID)
		view := payBEpusdt(t, f, token)
		resp := post(notifyFor(view.TradeNo, 1, 100.01, "TAddr"))
		assert.Equal(t, http.StatusOK, resp.Code)
		assert.Equal(t, "ok", resp.Body.String())
		assert.Equal(t, before, quotaOf(t, f, userID))
	})

	t.Run("credits once on duplicate callbacks", func(t *testing.T) {
		before := quotaOf(t, f, userID)
		view := payBEpusdt(t, f, token)
		for range 2 {
			resp := post(notifyFor(view.TradeNo, 2, 100.01, "TAddr"))
			assert.Equal(t, "ok", resp.Body.String())
		}
		assert.Equal(t, before+51_250_000, quotaOf(t, f, userID))
	})

	t.Run("late callback after expiry and after failure", func(t *testing.T) {
		for _, status := range []string{common.TopUpStatusExpired, common.TopUpStatusFailed} {
			before := quotaOf(t, f, userID)
			view := payBEpusdt(t, f, token)
			require.NoError(t, f.db.Model(&model.TopUp{}).Where("trade_no = ?", view.TradeNo).Update("status", status).Error)
			resp := post(notifyFor(view.TradeNo, 2, 100.01, "TAddr"))
			assert.Equal(t, "ok", resp.Body.String(), status)
			assert.Equal(t, before+51_250_000, quotaOf(t, f, userID), status)
		}
	})

	t.Run("amount mismatch queues review and acks", func(t *testing.T) {
		before := quotaOf(t, f, userID)
		view := payBEpusdt(t, f, token)
		resp := post(notifyFor(view.TradeNo, 2, 99.5, "TAddr"))
		assert.Equal(t, "ok", resp.Body.String())
		assert.Equal(t, before, quotaOf(t, f, userID))
		assert.EqualValues(t, 1, reviews(view.TradeNo))
	})

	t.Run("address mismatch queues review", func(t *testing.T) {
		view := payBEpusdt(t, f, token)
		resp := post(notifyFor(view.TradeNo, 2, 100.01, "TOther"))
		assert.Equal(t, "ok", resp.Body.String())
		assert.EqualValues(t, 1, reviews(view.TradeNo))
	})

	t.Run("missing order queues review", func(t *testing.T) {
		resp := post(notifyFor("EPU-missing", 2, 100.01, "TAddr"))
		assert.Equal(t, "ok", resp.Body.String())
		assert.EqualValues(t, 1, reviews("EPU-missing"))
	})

	t.Run("quota limit queues review", func(t *testing.T) {
		view := payBEpusdt(t, f, token)
		require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", userID).Update("quota", common.MaxWalletQuota-1).Error)
		resp := post(notifyFor(view.TradeNo, 2, 100.01, "TAddr"))
		assert.Equal(t, "ok", resp.Body.String())
		assert.EqualValues(t, 1, reviews(view.TradeNo))
		require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", userID).Update("quota", 0).Error)
	})
}

func TestBEpusdtCancelCallsGateway(t *testing.T) {
	f := newAdtyGuardFixture(t)
	gw := useFakeBEpusdt(t)
	token, _ := bepusdtBuyer(t, f, "usdt-cancel")
	view := payBEpusdt(t, f, token)

	gw.mu.Lock()
	gw.cancelStatus = 400
	gw.cancelMessage = "order already canceled"
	gw.mu.Unlock()
	closed := adtyJSON(t, f.engine, http.MethodPost, "/api/adty/topup/orders/"+view.TradeNo+"/cancel", "", token)
	assert.Contains(t, closed.Body.String(), `"success":true`, closed.Body.String())
	var stored model.TopUp
	require.NoError(t, f.db.Where("trade_no = ?", view.TradeNo).First(&stored).Error)
	assert.Equal(t, model.TopUpStatusCanceled, stored.Status)

	paid := payBEpusdt(t, f, token)
	gw.mu.Lock()
	gw.cancelMessage = "transfer detected"
	gw.mu.Unlock()
	failed := adtyJSON(t, f.engine, http.MethodPost, "/api/adty/topup/orders/"+paid.TradeNo+"/cancel", "", token)
	assert.Contains(t, failed.Body.String(), "A transfer has been detected")
	stored = model.TopUp{}
	require.NoError(t, f.db.Where("trade_no = ?", paid.TradeNo).First(&stored).Error)
	assert.Equal(t, common.TopUpStatusPending, stored.Status)

	fresh, _ := bepusdtBuyer(t, f, "usdt-cancel-fresh")
	droppedOrder := payBEpusdt(t, f, fresh)
	gw.mu.Lock()
	gw.dropCancel = true
	gw.mu.Unlock()
	dropped := adtyJSON(t, f.engine, http.MethodPost, "/api/adty/topup/orders/"+droppedOrder.TradeNo+"/cancel", "", fresh)
	assert.Contains(t, dropped.Body.String(), `"success":true`, dropped.Body.String())
	stored = model.TopUp{}
	require.NoError(t, f.db.Where("trade_no = ?", droppedOrder.TradeNo).First(&stored).Error)
	assert.Equal(t, model.TopUpStatusCanceled, stored.Status)
	assert.Equal(t, []string{"tid-" + view.TradeNo, "tid-" + paid.TradeNo, "tid-" + droppedOrder.TradeNo}, gw.snapshot().cancels)
}

func TestPaymentReviewPersistenceControlsCallbackAcknowledgement(t *testing.T) {
	for _, protocol := range []struct {
		name          string
		provider      string
		method        string
		ack           string
		failureStatus int
	}{
		{"bepusdt", model.PaymentProviderAdtyBEpusdt, http.MethodPost, "ok", http.StatusInternalServerError},
		{"epay_get", model.PaymentProviderAdtyEpay, http.MethodGet, "success", http.StatusOK},
		{"epay_post", model.PaymentProviderAdtyEpay, http.MethodPost, "success", http.StatusOK},
	} {
		t.Run(protocol.name, func(t *testing.T) {
			for _, unknown := range []bool{true, false} {
				name := "amount_mismatch"
				if unknown {
					name = "unknown_order"
				}
				t.Run(name, func(t *testing.T) {
					f := newAdtyGuardFixture(t)
					pgtest.RecordServerVersion(t, f.db)
					setRouterBonusRates(t)
					token, userID := bepusdtBuyer(t, f, "review-buyer")
					tradeNo := "unknown-review-order"
					var channel *model.AdtyEpayChannel
					if protocol.provider == model.PaymentProviderAdtyBEpusdt {
						useFakeBEpusdt(t)
						if !unknown {
							tradeNo = payBEpusdt(t, f, token).TradeNo
						}
					} else {
						gateway := newFakeEpay(t, epayQR)
						channel = seedRouterEpayChannel(t, "review", gateway.server.URL, model.AdtyEpayOrderModeAPI, 1)
						if !unknown {
							tradeNo = decodeAdtyData[model.AdtyTopUpView](t, epayPay(t, f, token).Body).TradeNo
						}
					}
					path, body, contentType := "/api/user/bepusdt/notify", notifyFor(tradeNo, 2, 99.5, "TAddr"), "application/json"
					if channel != nil {
						params := map[string]string{"pid": channel.MerchantId, "type": "alipay", "trade_no": "G" + tradeNo,
							"out_trade_no": tradeNo, "money": "729.99", "trade_status": "TRADE_SUCCESS"}
						signature, err := service.EpaySign(params, channel.MerchantKey)
						require.NoError(t, err)
						params["sign"], params["sign_type"] = signature, "MD5"
						form := url.Values{}
						for key, value := range params {
							form.Set(key, value)
						}
						path, body, contentType = "/api/user/epay/notify/"+strconv.Itoa(channel.Id), form.Encode(), "application/x-www-form-urlencoded"
						if protocol.method == http.MethodGet {
							path, body = path+"?"+body, ""
						}
					}
					send := func() *httptest.ResponseRecorder {
						req := httptest.NewRequest(protocol.method, path, strings.NewReader(body))
						req.Header.Set("Content-Type", contentType)
						response := httptest.NewRecorder()
						f.engine.ServeHTTP(response, req)
						return response
					}
					before := quotaOf(t, f, userID)
					// A real PostgreSQL constraint rejects the required INSERT while
					// order lookup and receipt recording remain available.
					require.NoError(t, f.db.Exec("ALTER TABLE adty_payment_orphan_events ADD CONSTRAINT test_review_unavailable CHECK (false)").Error)
					t.Cleanup(func() {
						assert.NoError(t, f.db.Exec("ALTER TABLE adty_payment_orphan_events DROP CONSTRAINT IF EXISTS test_review_unavailable").Error)
					})
					failed := send()
					assert.Equal(t, protocol.failureStatus, failed.Code)
					assert.Equal(t, "fail", failed.Body.String(), "a receipt cannot acknowledge a payment whose review did not persist")
					var reviews []model.PaymentOrphanEvent
					require.NoError(t, f.db.Where("reference_id = ?", tradeNo).Find(&reviews).Error)
					assert.Empty(t, reviews)
					require.NoError(t, f.db.Exec("ALTER TABLE adty_payment_orphan_events DROP CONSTRAINT test_review_unavailable").Error)
					for attempt := range 2 {
						response := send()
						assert.Equal(t, http.StatusOK, response.Code)
						assert.Equal(t, protocol.ack, response.Body.String())
						var stored []model.PaymentOrphanEvent
						require.NoError(t, f.db.Where("reference_id = ?", tradeNo).Find(&stored).Error)
						require.Len(t, stored, 1, "redelivery must preserve one durable reconciliation record")
						assert.Equal(t, model.PaymentOrphanStatusPendingReview, stored[0].Status)
						assert.Equal(t, protocol.provider, stored[0].Provider)
						if attempt == 0 {
							reviews = stored
						} else {
							assert.Equal(t, reviews[0].ID, stored[0].ID)
						}
					}
					assert.Equal(t, before, quotaOf(t, f, userID))
					for _, table := range []string{"adty_balance_ledgers", "adty_fund_batches"} {
						var count int64
						require.NoError(t, f.db.Table(table).Where("user_id = ?", userID).Count(&count).Error)
						assert.Zero(t, count, table)
					}
					var credited int64
					require.NoError(t, f.db.Model(&model.AdtyOrderPaymentEvent{}).Where("trade_no = ? AND outcome IN ?", tradeNo, []string{"credited", "replayed"}).Count(&credited).Error)
					assert.Zero(t, credited, "received/rejected events are not payment proof")
					if unknown {
						var count int64
						require.NoError(t, f.db.Model(&model.TopUp{}).Where("trade_no = ?", tradeNo).Count(&count).Error)
						assert.Zero(t, count, "a callback must not fabricate an order")
					} else {
						var topUp model.TopUp
						require.NoError(t, f.db.Where("trade_no = ?", tradeNo).First(&topUp).Error)
						assert.Equal(t, common.TopUpStatusPending, topUp.Status)
						ext, err := model.GetAdtyTopupOrder(tradeNo)
						require.NoError(t, err)
						require.NotNil(t, ext)
						assert.True(t, ext.PaidAmount.IsZero())
						assert.Empty(t, ext.PaidCurrency)
						assert.Empty(t, ext.PaidSource)
					}
				})
			}
		})
	}
}
