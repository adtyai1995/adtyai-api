package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestCancelAdtyTopUpOrderFollowsGatewayArrival(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.TopUp{}, &model.Log{}))
	require.NoError(t, model.MigrateAdtyTables())
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prevRedis })

	user := &model.User{
		Username: "cancel-fail-user", Password: "x", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
	}
	require.NoError(t, db.Create(user).Error)

	cancel := func(t *testing.T, message string, status int, tradeNo string) (bool, string, string) {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"status_code":` + strconv.Itoa(status) + `,"message":"` + message + `"}`))
		}))
		t.Cleanup(server.Close)
		cfg := setting.GetBEpusdtSetting()
		saved := *cfg
		cfg.Enabled, cfg.BaseURL, cfg.Secret = true, server.URL, "secret"
		t.Cleanup(func() { *cfg = saved })
		_ = service.AdtyPaymentProviderByName(model.PaymentProviderAdtyBEpusdt)
		require.NoError(t, model.CreateAdtyTopUp(
			&model.TopUp{
				UserId: user.Id, Amount: 100, TradeNo: tradeNo,
				PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt,
				Status: common.TopUpStatusPending,
			},
			&model.AdtyTopupOrder{
				TradeNo: tradeNo, UserId: user.Id, Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
				FaceUSD: decimal.NewFromInt(100), CreditQuota: 50_000_000, PayAmount: decimal.NewFromInt(100),
				PayCurrency: "USDT", GatewayTradeId: "tid-" + tradeNo, ExpireAt: common.GetTimestamp() + 900,
			},
		))
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/adty/topup/orders/"+tradeNo+"/cancel", nil)
		c.Params = gin.Params{{Key: "trade_no", Value: tradeNo}}
		c.Set("id", user.Id)
		CancelAdtyTopUpOrder(c)
		var body struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body), recorder.Body.String())
		var reloaded model.TopUp
		require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&reloaded).Error)
		return body.Success, body.Message, reloaded.Status
	}

	t.Run("gateway down still cancels locally", func(t *testing.T) {
		success, _, status := cancel(t, "gateway down", http.StatusInternalServerError, "EPU-CANCEL-DOWN-1")
		require.True(t, success)
		require.Equal(t, model.TopUpStatusCanceled, status)
	})
	t.Run("detected transfer stays pending", func(t *testing.T) {
		success, message, status := cancel(t, "transfer detected", http.StatusBadRequest, "EPU-CANCEL-PAID-1")
		require.False(t, success)
		require.Contains(t, message, "A transfer has been detected")
		require.Equal(t, common.TopUpStatusPending, status)
	})
}
