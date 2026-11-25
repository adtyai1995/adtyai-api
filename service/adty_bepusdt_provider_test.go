package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBEpusdtProviderQueryConfirmingEVMCaseAndOverpayment(t *testing.T) {
	var payload map[string]any
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/pay/info", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		raw, err := json.Marshal(map[string]any{
			"status_code": 200,
			"message":     "success",
			"data":        payload,
		})
		require.NoError(t, err)
		_, _ = w.Write(raw)
	}))
	defer server.Close()

	origClient := bepusdtHTTPClient
	bepusdtHTTPClient = func() *http.Client { return server.Client() }
	t.Cleanup(func() { bepusdtHTTPClient = origClient })

	setTestBEpusdtConfig(t, false, server.URL, "secret", []string{AdtyMethodUSDTBEP20, AdtyMethodUSDTTRC20})

	evmOrder := &model.AdtyTopupOrder{
		TradeNo:         "EPU20261126010",
		Provider:        model.PaymentProviderAdtyBEpusdt,
		Method:          AdtyMethodUSDTBEP20,
		FaceUSD:         decimal.NewFromInt(10),
		TopupGroupRatio: decimal.NewFromInt(1),
		PayAmount:       decimal.RequireFromString("10.0005"),
		PayCurrency:     "USDT",
		PayAddress:      "0xAbCdEf012345678901234567890123456789aBcD",
		GatewayTradeId:  "TR_EVM_1",
		PayURL:          server.URL + "/pay/checkout/TR_EVM_1",
	}

	t.Run("status 5 returns ErrAdtyPaymentConfirming instead of nil unpaid", func(t *testing.T) {
		payload = map[string]any{
			"trade_id":      evmOrder.GatewayTradeId,
			"order_id":      evmOrder.TradeNo,
			"trade_type":    evmOrder.Method,
			"status":        5,
			"money":         "10",
			"actual_amount": "10.0005",
			"token":         "0xabcdef012345678901234567890123456789abcd",
			"fiat":          "USD",
		}
		facts, err := (adtyBEpusdtProvider{}).Query(context.Background(), evmOrder)
		assert.Nil(t, facts)
		require.ErrorIs(t, err, ErrAdtyPaymentConfirming)
	})

	t.Run("EVM token matches case-insensitively and allows has_prefix overpayment", func(t *testing.T) {
		payload = map[string]any{
			"trade_id":      evmOrder.GatewayTradeId,
			"order_id":      evmOrder.TradeNo,
			"trade_type":    evmOrder.Method,
			"status":        2,
			"money":         "10.00",
			"actual_amount": "10.0099",
			"token":         "0xabcdef012345678901234567890123456789abcd",
			"fiat":          "USD",
		}
		facts, err := (adtyBEpusdtProvider{}).Query(context.Background(), evmOrder)
		require.NoError(t, err)
		require.NotNil(t, facts)
		assert.True(t, facts.PaidAmount.Equal(decimal.RequireFromString("10.0099")))
	})

	t.Run("underpayment is strictly rejected", func(t *testing.T) {
		payload = map[string]any{
			"trade_id":      evmOrder.GatewayTradeId,
			"order_id":      evmOrder.TradeNo,
			"trade_type":    evmOrder.Method,
			"status":        2,
			"money":         "10.00",
			"actual_amount": "10.0004",
			"token":         "0xabcdef012345678901234567890123456789abcd",
			"fiat":          "USD",
		}
		_, err := (adtyBEpusdtProvider{}).Query(context.Background(), evmOrder)
		require.ErrorIs(t, err, model.ErrPaymentAmountMismatch)
	})

	t.Run("TRC20 token comparison stays case-sensitive", func(t *testing.T) {
		trcOrder := *evmOrder
		trcOrder.Method = AdtyMethodUSDTTRC20
		trcOrder.PayAddress = "TAbCdEf123456"
		payload = map[string]any{
			"trade_id":      trcOrder.GatewayTradeId,
			"order_id":      trcOrder.TradeNo,
			"trade_type":    trcOrder.Method,
			"status":        2,
			"money":         "10.00",
			"actual_amount": "10.0005",
			"token":         "tabcdef123456",
			"fiat":          "USD",
		}
		_, err := (adtyBEpusdtProvider{}).Query(context.Background(), &trcOrder)
		require.ErrorIs(t, err, model.ErrPaymentMethodMismatch)
	})
}
