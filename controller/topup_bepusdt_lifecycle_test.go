package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setTestBEpusdtConfig(t *testing.T, enabled bool, baseURL, secret string, tradeTypes []string) {
	t.Helper()
	old := setting.GetBEpusdtSetting()
	apply := func(en bool, urlStr, sec string, chains []string) {
		rawChains, _ := common.Marshal(chains)
		enStr := "false"
		if en {
			enStr = "true"
		}
		_ = config.GlobalConfig.LoadFromDB(map[string]string{
			setting.BEpusdtOptionKeyEnabled:    enStr,
			setting.BEpusdtOptionKeyBaseURL:    urlStr,
			setting.BEpusdtOptionKeySecret:     sec,
			setting.BEpusdtOptionKeyTradeTypes: string(rawChains),
		})
	}
	apply(enabled, baseURL, secret, tradeTypes)
	t.Cleanup(func() {
		apply(old.Enabled, old.BaseURL, old.Secret, old.TradeTypes)
	})
}

func TestBepusdtPermanentErrorIncludesTeamInvalid(t *testing.T) {
	wrapped := fmt.Errorf("%w: wallet quota limit exceeded", service.ErrAdtyTeamInvalid)
	assert.True(t, bepusdtPermanentError(wrapped))
	assert.True(t, bepusdtPermanentError(service.ErrAdtyTeamInvalid))
}

func TestBEpusdtNotifyRequiresBlockTxHashAndCreditsValidCallback(t *testing.T) {
	db := setupEpayLifecycleTestDB(t)
	now := time.Now().Unix()

	setTestBEpusdtConfig(t, true, "https://bep.example.com", "bep-test-secret", []string{"usdt.trc20"})

	require.NoError(t, db.Create(&model.User{
		Id:       71,
		Username: "usdt_buyer",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}).Error)

	seedPendingTopup(t, db, 71, "EPU_CB_TEST", model.PaymentProviderAdtyBEpusdt, "usdt.trc20", "wallet_topup",
		decimal.NewFromInt(10), decimal.Zero, decimal.RequireFromString("10.0005"), "USDT", now+900, 0, "TValidAddr", "")
	require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("trade_no = ?", "EPU_CB_TEST").Update("gateway_trade_id", "BEP_TRADE_1").Error)

	sendNotify := func(payload map[string]any) *httptest.ResponseRecorder {
		rawInit, err := common.Marshal(payload)
		require.NoError(t, err)
		normalized := map[string]any{}
		require.NoError(t, common.Unmarshal(rawInit, &normalized))
		payload["signature"] = service.BEpusdtSign(normalized, "bep-test-secret")
		body, err := common.Marshal(payload)
		require.NoError(t, err)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/user/bepusdt/notify", strings.NewReader(string(body)))
		BEpusdtTopUpNotify(c)
		return w
	}

	// Missing block_transaction_id must NOT credit; it queues an orphan review and replies 200 ok.
	w1 := sendNotify(map[string]any{
		"trade_id":      "BEP_TRADE_1",
		"order_id":      "EPU_CB_TEST",
		"amount":        10.0,
		"actual_amount": 10.0005,
		"token":         "TValidAddr",
		"status":        2,
	})
	assert.Equal(t, http.StatusOK, w1.Code)
	assert.Equal(t, "ok", w1.Body.String())

	var top model.TopUp
	require.NoError(t, db.Where("trade_no = ?", "EPU_CB_TEST").First(&top).Error)
	assert.Equal(t, common.TopUpStatusPending, top.Status)
	var orphans int64
	require.NoError(t, db.Model(&model.PaymentOrphanEvent{}).Where("reference_id = ?", "EPU_CB_TEST").Count(&orphans).Error)
	assert.Equal(t, int64(1), orphans)

	// Valid callback with block_transaction_id credits cleanly.
	w2 := sendNotify(map[string]any{
		"trade_id":             "BEP_TRADE_1",
		"order_id":             "EPU_CB_TEST",
		"amount":               10.0,
		"actual_amount":        10.0005,
		"token":                "TValidAddr",
		"block_transaction_id": "0xabc999",
		"status":               2,
	})
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "ok", w2.Body.String())

	require.NoError(t, db.Where("trade_no = ?", "EPU_CB_TEST").First(&top).Error)
	assert.Equal(t, common.TopUpStatusSuccess, top.Status)
}

func TestRequestBEpusdtPayOwnerMarginSkipsPersonalQuotaCapAndWritebackCancel(t *testing.T) {
	db := setupEpayLifecycleTestDB(t)

	seedTestPaymentScenes(t, db)

	var canceledCount atomic.Int32
	var cancelBeforeReturn atomic.Bool
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/order/create-transaction":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			orderID, _ := req["order_id"].(string)
			if cancelBeforeReturn.Load() && orderID != "" {
				// Simulate concurrent cancellation while gateway HTTP call is in flight.
				_ = db.Model(&model.TopUp{}).Where("trade_no = ?", orderID).Update("status", model.TopUpStatusCanceled).Error
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status_code": 200,
				"message":     "success",
				"data": map[string]any{
					"order_id":        orderID,
					"trade_type":      "usdt.trc20",
					"fiat":            "USD",
					"amount":          10,
					"trade_id":        "GW_BEP_" + orderID,
					"actual_amount":   "10.0012",
					"token":           "TTestAddress",
					"expiration_time": 900,
					"payment_url":     srv.URL + "/pay/1",
				},
			})
		case "/api/v1/order/cancel-transaction":
			canceledCount.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "message": "ok", "data": map[string]any{}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	setTestBEpusdtConfig(t, true, srv.URL, "test-secret", []string{"usdt.trc20"})

	require.NoError(t, db.Create(&model.User{
		Id:       72,
		Username: "owner72",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Quota:    common.MaxWalletQuota,
	}).Error)
	require.NoError(t, db.Create(&model.AdtySite{
		Id:          12,
		Subdomain:   "marginsite",
		OwnerUserId: 72,
		Status:      model.AdtySiteStatusActive,
	}).Error)

	t.Run("owner_margin_topup bypasses personal ValidateTopUpQuotaCapacity", func(t *testing.T) {
		cancelBeforeReturn.Store(false)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/user/bepusdt/pay", strings.NewReader(`{"business":"owner_margin_topup","amount":10,"chain":" usdt.trc20 "}`))
		c.Set("id", 72)
		RequestBEpusdtPay(c)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Success bool                `json:"success"`
			Data    model.AdtyTopUpView `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success, w.Body.String())
		require.NotNil(t, resp.Data.Ext)
		assert.Equal(t, "TTestAddress", resp.Data.Ext.PayAddress)
	})

	t.Run("stale writeback cancels remote BEpusdt order and returns clear user error", func(t *testing.T) {
		require.NoError(t, db.Create(&model.User{
			Id:       73,
			Username: "buyer73",
			Role:     common.RoleCommonUser,
			Status:   common.UserStatusEnabled,
		}).Error)
		cancelBeforeReturn.Store(true)
		canceledCount.Store(0)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/user/bepusdt/pay", strings.NewReader(`{"amount":10,"chain":"usdt.trc20"}`))
		c.Set("id", 73)
		RequestBEpusdtPay(c)
		assert.Equal(t, int32(1), canceledCount.Load())
		assert.Contains(t, w.Body.String(), "Order status changed before checkout finished. Please try again")
		assert.NotContains(t, w.Body.String(), "Only orders waiting for payment can be canceled")
	})
}
