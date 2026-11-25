package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setTestBEpusdtConfig(t *testing.T, enabled bool, baseURL, secret string, tradeTypes []string) {
	t.Helper()
	prev := setting.GetBEpusdtSetting()
	apply := func(en bool, base, sec string, chains []string) {
		rawChains, err := common.Marshal(chains)
		require.NoError(t, err)
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
			setting.BEpusdtOptionKeyEnabled:    strconv.FormatBool(en),
			setting.BEpusdtOptionKeyBaseURL:    base,
			setting.BEpusdtOptionKeySecret:     sec,
			setting.BEpusdtOptionKeyTradeTypes: string(rawChains),
		}))
	}
	apply(enabled, baseURL, secret, tradeTypes)
	t.Cleanup(func() {
		apply(prev.Enabled, prev.BaseURL, prev.Secret, prev.TradeTypes)
	})
}

func TestCreateBEpusdtOrderContextVerifyOrderAndTimeoutBounds(t *testing.T) {
	var lastTimeout float64
	var respData map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/order/create-transaction", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var reqMap map[string]any
		require.NoError(t, json.Unmarshal(body, &reqMap))
		lastTimeout, _ = reqMap["timeout"].(float64)

		w.Header().Set("Content-Type", "application/json")
		raw, err := json.Marshal(map[string]any{
			"status_code": 200,
			"message":     "success",
			"data":        respData,
		})
		require.NoError(t, err)
		_, _ = w.Write(raw)
	}))
	defer server.Close()

	origClient := bepusdtHTTPClient
	bepusdtHTTPClient = func() *http.Client { return server.Client() }
	t.Cleanup(func() { bepusdtHTTPClient = origClient })

	setTestBEpusdtConfig(t, true, server.URL, "test-secret", []string{AdtyMethodUSDTTRC20, AdtyMethodUSDTBEP20})

	baseReq := BEpusdtCreateRequest{
		VerifyOrder: true,
		OrderID:     "EPU20261126001",
		AmountUSD:   decimal.RequireFromString("10"),
		TradeType:   AdtyMethodUSDTTRC20,
		NotifyURL:   "https://shop.example.com/api/user/bepusdt/notify",
		RedirectURL: "https://shop.example.com/wallet",
		Timeout:     600,
	}

	t.Run("VerifyOrder accepts v1.23 response omitting trade_type and fiat", func(t *testing.T) {
		respData = map[string]any{
			"order_id":        "EPU20261126001",
			"amount":          10,
			"trade_id":        "TR1001",
			"actual_amount":   "10.0001",
			"token":           "TReceiverAddr1",
			"expiration_time": 600,
			"payment_url":     "https://pay.example.com/pay/TR1001",
		}
		order, err := CreateBEpusdtOrderContext(context.Background(), baseReq)
		require.NoError(t, err)
		assert.Equal(t, "TR1001", order.TradeId)
		assert.Equal(t, "TReceiverAddr1", order.Address)
		assert.True(t, order.ActualAmount.Equal(decimal.RequireFromString("10.0001")))
		assert.Equal(t, float64(600), lastTimeout)
	})

	t.Run("VerifyOrder rejects mismatched non-empty trade_type or fiat", func(t *testing.T) {
		respData = map[string]any{
			"order_id":      "EPU20261126001",
			"trade_type":    AdtyMethodUSDTBEP20,
			"fiat":          "USD",
			"amount":        10,
			"trade_id":      "TR1002",
			"actual_amount": "10.0001",
			"token":         "TReceiverAddr1",
		}
		_, err := CreateBEpusdtOrderContext(context.Background(), baseReq)
		require.Error(t, err)

		respData["trade_type"] = AdtyMethodUSDTTRC20
		respData["fiat"] = "CNY"
		_, err = CreateBEpusdtOrderContext(context.Background(), baseReq)
		require.Error(t, err)
	})

	t.Run("timeout clamps below 180 to default 900 and above 3600 to 3600", func(t *testing.T) {
		respData = map[string]any{
			"order_id":      "EPU20261126001",
			"trade_type":    AdtyMethodUSDTTRC20,
			"fiat":          "USD",
			"amount":        10,
			"trade_id":      "TR1003",
			"actual_amount": "10.0001",
			"token":         "TReceiverAddr1",
		}
		lowReq := baseReq
		lowReq.Timeout = 120
		_, err := CreateBEpusdtOrder(lowReq)
		require.NoError(t, err)
		assert.Equal(t, float64(bepusdtTimeoutSeconds), lastTimeout)

		highReq := baseReq
		highReq.Timeout = 7200
		_, err = CreateBEpusdtOrderContext(context.Background(), highReq)
		require.NoError(t, err)
		assert.Equal(t, float64(3600), lastTimeout)
	})

	t.Run("CreateBEpusdtOrderContext honors canceled caller context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := CreateBEpusdtOrderContext(ctx, baseReq)
		require.Error(t, err)
	})
}

func TestBEpusdtPaymentDetectedIgnoresNegatedPhrases(t *testing.T) {
	for _, msg := range []string{
		"order is unpaid",
		"order is not paid",
		"not yet paid",
		"unpaid order cannot be processed",
		"no transfer found",
		"订单未支付",
		"用户未转账",
		"没有转账记录",
	} {
		assert.False(t, bepusdtPaymentDetected(BEpusdtGatewayError{StatusCode: 400, Message: msg}), "should not match: %q", msg)
	}
	for _, msg := range []string{
		"order already paid",
		"payment paid",
		"transfer detected on chain",
		"订单已支付",
		"检测到转账",
		"支付成功",
	} {
		assert.True(t, bepusdtPaymentDetected(BEpusdtGatewayError{StatusCode: 400, Message: msg}), "should match: %q", msg)
	}
}

func TestCancelBEpusdtOrderDecoupledFromEnabledAndStatusFallback(t *testing.T) {
	var infoStatus atomic.Int32
	var cancelMsg atomic.Value
	cancelMsg.Store("当前订单(TR900)状态不允许取消")

	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/order/cancel-transaction":
			msg := cancelMsg.Load().(string)
			if msg == "" {
				_, _ = w.Write([]byte(`{"status_code":200,"message":"success","data":{}}`))
				return
			}
			raw, _ := json.Marshal(map[string]any{"status_code": 400, "message": msg})
			_, _ = w.Write(raw)
		case "/api/v1/pay/info":
			st := int(infoStatus.Load())
			raw, _ := json.Marshal(map[string]any{
				"status_code": 200,
				"message":     "success",
				"data": map[string]any{
					"trade_id": "TR900",
					"status":   st,
				},
			})
			_, _ = w.Write(raw)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer tlsServer.Close()

	origClient := bepusdtHTTPClient
	bepusdtHTTPClient = func() *http.Client { return tlsServer.Client() }
	t.Cleanup(func() { bepusdtHTTPClient = origClient })

	// Explicitly disable new checkouts: CancelBEpusdtOrder must STILL work.
	setTestBEpusdtConfig(t, false, tlsServer.URL, "cancel-secret", []string{AdtyMethodUSDTTRC20})

	t.Run("cancel succeeds even when cfg.Enabled is false", func(t *testing.T) {
		cancelMsg.Store("")
		require.NoError(t, CancelBEpusdtOrder("TR900"))
	})

	t.Run("status disallows cancel resolves via verified HTTPS /api/v1/pay/info", func(t *testing.T) {
		cancelMsg.Store("当前订单(TR900)状态不允许取消")

		for _, paidStatus := range []int32{2, 5} {
			infoStatus.Store(paidStatus)
			err := CancelBEpusdtOrder("TR900")
			require.ErrorIs(t, err, ErrAdtyPaymentDetected, "remote status %d must return ErrAdtyPaymentDetected", paidStatus)
		}

		for _, terminalUnpaid := range []int32{3, 4, 6} {
			infoStatus.Store(terminalUnpaid)
			require.NoError(t, CancelBEpusdtOrder("TR900"), "remote status %d must return nil", terminalUnpaid)
		}

		// Remote status 1 (still waiting) fails closed.
		infoStatus.Store(1)
		err := CancelBEpusdtOrder("TR900")
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrAdtyPaymentDetected)
	})

	t.Run("plain HTTP gateway fails closed on status disallows cancel without trusting unsigned pay/info", func(t *testing.T) {
		var infoCalled atomic.Bool
		httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/api/v1/pay/info" {
				infoCalled.Store(true)
				_, _ = w.Write([]byte(`{"status_code":200,"data":{"trade_id":"TR900","status":4}}`))
				return
			}
			_, _ = w.Write([]byte(`{"status_code":400,"message":"当前订单(TR900)状态不允许取消"}`))
		}))
		defer httpServer.Close()

		bepusdtHTTPClient = func() *http.Client { return httpServer.Client() }
		setTestBEpusdtConfig(t, false, httpServer.URL, "cancel-secret", []string{AdtyMethodUSDTTRC20})

		err := CancelBEpusdtOrder("TR900")
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrAdtyPaymentDetected)
		assert.False(t, infoCalled.Load(), "must not query unsigned HTTP /api/v1/pay/info")
	})

	t.Run("CreateBEpusdtOrderContext still requires cfg.Enabled=true", func(t *testing.T) {
		setTestBEpusdtConfig(t, false, tlsServer.URL, "cancel-secret", []string{AdtyMethodUSDTTRC20})
		_, err := CreateBEpusdtOrderContext(context.Background(), BEpusdtCreateRequest{
			OrderID:   "EPU_DISABLED",
			AmountUSD: decimal.NewFromInt(10),
			TradeType: AdtyMethodUSDTTRC20,
		})
		require.ErrorIs(t, err, errBEpusdtNotConfigured)
	})
}
