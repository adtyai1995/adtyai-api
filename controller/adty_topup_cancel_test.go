package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestCancelAdtyTopUpOrderAbortsWhenProviderFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := adtyTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.TopUp{}, &model.Log{}))
	require.NoError(t, model.MigrateAdtyTables())
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prevRedis })

	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status_code":500,"message":"gateway down"}`))
	}))
	t.Cleanup(failServer.Close)
	cfg := setting.GetBEpusdtSetting()
	saved := *cfg
	cfg.Enabled, cfg.BaseURL, cfg.Secret = true, failServer.URL, "secret"
	t.Cleanup(func() { *cfg = saved })
	_ = service.AdtyPaymentProviderByName(model.PaymentProviderAdtyBEpusdt)

	user := &model.User{
		Username: "cancel-fail-user", Password: "x", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
	}
	require.NoError(t, db.Create(user).Error)
	tradeNo := "EPU-CANCEL-FAIL-1"
	require.NoError(t, model.CreateAdtyTopUp(
		&model.TopUp{
			UserId: user.Id, Amount: 100, TradeNo: tradeNo,
			PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt,
			Status: common.TopUpStatusPending,
		},
		&model.AdtyTopupOrder{
			TradeNo: tradeNo, UserId: user.Id, Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
			FaceUSD: decimal.NewFromInt(100), CreditQuota: 50_000_000, PayAmount: decimal.NewFromInt(100),
			PayCurrency: "USDT", GatewayTradeId: "tid-cancel-fail-1", ExpireAt: common.GetTimestamp() + 900,
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
	require.False(t, body.Success, "ISS-R1-0010: provider cancel error must not be swallowed to locally cancel pending order")

	var reloaded model.TopUp
	require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&reloaded).Error)
	require.Equal(t, common.TopUpStatusPending, reloaded.Status)
}
