package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdtyTeamTopupSanitizedGatewayErrorsAndLedgersEndpoints(t *testing.T) {
	db := setupEpayLifecycleTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AdtyTeamLedger{}))
	now := time.Now().Unix()

	seedTestPaymentScenes(t, db)

	require.NoError(t, db.Create(&model.User{Id: 401, Username: "leader401", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.User{Id: 402, Username: "member402", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.AdtyTeam{
		Id: 40, SiteId: 0, LeaderUserId: 401, Name: "EndpointTeam",
		Discount: decimal.NewFromInt(1), Status: model.AdtyTeamStatusActive,
	}).Error)

	// Gateway returns a raw BEpusdtGatewayError with sensitive upstream message.
	bepSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status_code": 400,
			"message":     "internal SQL error on 10.0.0.5:5432 secret-token",
		})
	}))
	defer bepSrv.Close()

	setTestBEpusdtConfig(t, true, bepSrv.URL, "bep-sec", []string{"usdt.trc20"})

	t.Run("PostAdtyTeamTopup sanitizes raw BEpusdtGatewayError", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/adty/teams/40/topups",
			strings.NewReader(`{"amount_usd":"10","method":"usdt.trc20","device":"mobile"}`))
		c.Request.Header.Set("Idempotency-Key", "team-err-key-1")
		c.Params = gin.Params{{Key: "id", Value: "40"}}
		c.Set("id", 401)
		PostAdtyTeamTopup(c)

		body := w.Body.String()
		assert.Contains(t, body, "The payment gateway is unavailable. Please try again later")
		assert.NotContains(t, body, "10.0.0.5")
		assert.NotContains(t, body, "secret-token")
		assert.NotContains(t, body, "bepusdt status")
	})

	t.Run("GetAdtyTeamTopups returns enriched backward-compatible fields and pagination", func(t *testing.T) {
		require.NoError(t, db.Create(&model.TopUp{
			UserId: 401, Amount: 25, Money: 175, TradeNo: "TTEP_LIST_1",
			PaymentMethod: "alipay", PaymentProvider: model.PaymentProviderAdtyEpay,
			CreateTime: now, Status: common.TopUpStatusSuccess,
		}).Error)
		require.NoError(t, db.Create(&model.AdtyTopupOrder{
			Business: model.AdtyBusinessTeamTopup, TradeNo: "TTEP_LIST_1", UserId: 401, TeamId: 40,
			Provider: model.PaymentProviderAdtyEpay, Method: "alipay",
			FaceUSD: decimal.NewFromInt(25), PayAmount: decimal.NewFromInt(175), PayCurrency: "CNY",
			ExpireAt: now + 600,
		}).Error)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/adty/teams/40/topups?status=success&page=1&page_size=10", nil)
		c.Params = gin.Params{{Key: "id", Value: "40"}}
		c.Set("id", 401)
		GetAdtyTeamTopups(c)
		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool `json:"success"`
			Data    struct {
				Total    int64                   `json:"total"`
				Page     int                     `json:"page"`
				PageSize int                     `json:"page_size"`
				Items    []adtyTeamTopupResponse `json:"items"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
		assert.Equal(t, int64(1), resp.Data.Total)
		assert.Equal(t, 1, resp.Data.Page)
		assert.Equal(t, 10, resp.Data.PageSize)
		require.Len(t, resp.Data.Items, 1)
		assert.Equal(t, "TTEP_LIST_1", resp.Data.Items[0].TradeNo)
		assert.Equal(t, common.TopUpStatusSuccess, resp.Data.Items[0].Status)
		assert.Equal(t, "leader401", resp.Data.Items[0].Username)
		require.NotNil(t, resp.Data.Items[0].Ext)
	})

	t.Run("GetAdtyTeamLedgers returns paginated ledgers for leader and rejects non-leader", func(t *testing.T) {
		require.NoError(t, db.Create(&model.AdtyTeamLedger{
			TeamId: 40, DeltaQuota: 500000, BalanceBefore: 0, BalanceAfter: 500000,
			Type: "topup", RefId: "TTEP_LIST_1", ActorUserId: 401, IdempotencyKey: "topup:TTEP_LIST_1",
		}).Error)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/adty/teams/40/ledgers?p=1&page_size=5", nil)
		c.Params = gin.Params{{Key: "id", Value: "40"}}
		c.Set("id", 401)
		GetAdtyTeamLedgers(c)
		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool `json:"success"`
			Data    struct {
				Total    int64                    `json:"total"`
				Page     int                      `json:"page"`
				PageSize int                      `json:"page_size"`
				Items    []adtyTeamLedgerResponse `json:"items"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
		assert.Equal(t, int64(1), resp.Data.Total)
		assert.Equal(t, 1, resp.Data.Page)
		assert.Equal(t, 5, resp.Data.PageSize)
		require.Len(t, resp.Data.Items, 1)
		assert.Equal(t, "leader401", resp.Data.Items[0].ActorUsername)

		// Non-leader rejected.
		w2 := httptest.NewRecorder()
		c2, _ := gin.CreateTestContext(w2)
		c2.Request = httptest.NewRequest(http.MethodGet, "/api/adty/teams/40/ledgers", nil)
		c2.Params = gin.Params{{Key: "id", Value: "40"}}
		c2.Set("id", 402)
		GetAdtyTeamLedgers(c2)
		assert.Contains(t, w2.Body.String(), `"success":false`)
	})
}
