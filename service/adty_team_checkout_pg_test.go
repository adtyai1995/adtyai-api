package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func testTeamCheckoutRecovery(t *testing.T, db *gorm.DB, teamID int64, leaderID int) {
	// Lose the first response before or after the merchant accepts it. The only
	// restart inputs are the persisted order and the gateway's order state.
	var mu sync.Mutex
	accepted := map[string]string{}
	attempts := map[string]int{}
	beforeTrade := adtyTeamTopupTradeNo("TTEP", teamID, "before")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/mapi.php":
			trade, money := r.FormValue("out_trade_no"), r.FormValue("money")
			attempts[trade]++
			if attempts[trade] == 1 {
				if trade != beforeTrade {
					accepted[trade] = money
				}
				http.Error(w, "response lost", http.StatusBadGateway)
				return
			}
			accepted[trade] = money
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "trade_no": "remote-" + trade, "qrcode": "qr-" + trade})
		case "/api.php":
			trade := r.URL.Query().Get("out_trade_no")
			money, ok := accepted[trade]
			if !ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"code": -1, "msg": "order not found"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "status": 0, "pid": "merchant", "out_trade_no": trade, "trade_no": "remote-" + trade, "type": "alipay", "money": money})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	originalClient := adtyEpayHTTPClient
	adtyEpayHTTPClient = server.Client
	defer func() { adtyEpayHTTPClient = originalClient }()
	channel := model.AdtyEpayChannel{Name: "Mock", GatewayURL: server.URL, MerchantId: "merchant", MerchantKey: "test", Status: model.AdtyEpayStatusEnabled, OrderMode: model.AdtyEpayOrderModeAPI, Methods: []model.AdtyEpayChannelMethod{{Method: "alipay", Enabled: true}}}
	require.NoError(t, db.Create(&channel).Error)
	amount := decimal.NewFromInt(20)
	_, err := StartAdtyTeamTopupPayment(leaderID, teamID, amount, "", channel.Id, "empty", "127.0.0.1")
	require.Error(t, err)
	for _, key := range []string{"before", "after"} {
		trade := adtyTeamTopupTradeNo("TTEP", teamID, key)
		_, err := StartAdtyTeamTopupPayment(leaderID, teamID, amount, "alipay", channel.Id, key, "127.0.0.1")
		require.Error(t, err)
		view, err := model.GetUserAdtyTopUp(trade, leaderID)
		require.NoError(t, err)
		require.Equal(t, "submitting", view.Ext.CheckoutStage)
		require.Equal(t, common.TopUpStatusPending, view.Status)
		var ext *model.AdtyTopupOrder
		if key == "before" {
			ext, err = StartAdtyTeamTopupPayment(leaderID, teamID, amount, "alipay", channel.Id, key, "127.0.0.1")
		} else {
			ext, err = ResumeAdtyTeamTopupPayment(leaderID, teamID, trade, "127.0.0.1")
		}
		require.NoError(t, err)
		require.Equal(t, "ready", ext.CheckoutStage)
		require.Equal(t, trade, ext.TradeNo)
		if key == "before" {
			require.Equal(t, "qr-"+trade, ext.QRCode)
		} else {
			require.Contains(t, ext.PayURL, "out_trade_no="+trade)
		}
		facts := model.AdtyPaymentFacts{Provider: ext.Provider, Method: ext.Method, PaidAmount: ext.PayAmount, PaidCurrency: "CNY", TxHash: "remote-" + trade, Source: model.AdtyPaymentSourceCallback}
		for i := 0; i < 2; i++ {
			replayed, err := model.CompleteAdtyTopUp(trade, facts, "")
			require.NoError(t, err)
			require.Equal(t, i > 0, replayed)
		}
		var events int64
		require.NoError(t, db.Model(&model.AdtyTeamLedger{}).Where("ref_id = ? AND type = 'topup'", trade).Count(&events).Error)
		require.EqualValues(t, 1, events)
	}
	require.NoError(t, db.Model(&channel).Update("status", model.AdtyEpayStatusDisabled).Error)
	_, err = StartAdtyTeamTopupPayment(leaderID, teamID, amount, "alipay", channel.Id, "disabled", "127.0.0.1")
	require.Error(t, err)
	var rejected int64
	require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("trade_no IN ?", []string{adtyTeamTopupTradeNo("TTEP", teamID, "disabled"), adtyTeamTopupTradeNo("TTEAM", teamID, "empty")}).Count(&rejected).Error)
	require.Zero(t, rejected)
	var beRequests int
	bep := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		beRequests++
		if beRequests == 1 {
			http.Error(w, "lost response", 502)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "data": map[string]any{
			"trade_id": "bep-original", "order_id": req["order_id"], "fiat": "USD", "amount": req["amount"], "trade_type": req["trade_type"],
			"actual_amount": "20.01", "token": "test-address", "expiration_time": 900, "payment_url": "https://example.test/checkout/bep-original",
		}})
	}))
	defer bep.Close()
	cfg := setting.GetBEpusdtSetting()
	prev := *cfg
	defer func() { *cfg = prev }()
	*cfg = setting.BEpusdtSetting{Enabled: true, Secret: "test", BaseURL: bep.URL, TradeTypes: []string{"usdt.trc20"}}
	_, err = StartAdtyTeamTopupPayment(leaderID, teamID, amount, "usdt.trc20", 0, "bep", "")
	require.Error(t, err)
	bepTrade := adtyTeamTopupTradeNo("TTU", teamID, "bep")
	ext, err := ResumeAdtyTeamTopupPayment(leaderID, teamID, bepTrade, "")
	require.NoError(t, err)
	require.Equal(t, bepTrade, ext.TradeNo)
	require.Equal(t, "bep-original", ext.GatewayTradeId)
	require.Equal(t, "test-address", ext.PayAddress)
	facts := model.AdtyPaymentFacts{Provider: ext.Provider, Method: ext.Method, PaidAmount: ext.PayAmount, PaidCurrency: "USDT", FiatAmount: amount, PayAddress: ext.PayAddress, GatewayTradeId: ext.GatewayTradeId, TxHash: "bep-hash", Source: model.AdtyPaymentSourceCallback}
	for i := 0; i < 2; i++ {
		replayed, err := model.CompleteAdtyTopUp(bepTrade, facts, "")
		require.NoError(t, err)
		require.Equal(t, i > 0, replayed)
	}
}
