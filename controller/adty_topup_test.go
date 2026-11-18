package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustAdtyTopUpViewFromJSON(t *testing.T, payload map[string]any) model.AdtyTopUpView {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	var view model.AdtyTopUpView
	require.NoError(t, json.Unmarshal(raw, &view))
	return view
}

func TestGetAdtyTopUpSelfFiltersAndUserScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const authedUserID = 77

	dataset := []model.AdtyTopUpView{
		mustAdtyTopUpViewFromJSON(t, map[string]any{
			"id":               1,
			"user_id":          authedUserID,
			"trade_no":         "EPU770001",
			"payment_method":   "usdt.trc20",
			"payment_provider": "bepusdt",
			"status":           "success",
			"create_time":      int64(1700001000),
			"ext": map[string]any{
				"trade_no":           "EPU770001",
				"method":             "usdt.trc20",
				"payment_scene":      "wallet_topup",
				"payment_scene_name": "Wallet top-up",
				"face_usd":           "100",
			},
		}),
		mustAdtyTopUpViewFromJSON(t, map[string]any{
			"id":               2,
			"user_id":          authedUserID,
			"trade_no":         "EPY770002",
			"payment_method":   "alipay",
			"payment_provider": "epay",
			"status":           "pending",
			"create_time":      int64(1700002000),
			"ext": map[string]any{
				"trade_no":           "EPY770002",
				"method":             "alipay",
				"method_label":       "支付宝1",
				"payment_scene":      "wallet_topup",
				"payment_scene_name": "Wallet top-up",
				"face_usd":           "50",
			},
		}),
		mustAdtyTopUpViewFromJSON(t, map[string]any{
			"id":               3,
			"user_id":          authedUserID,
			"trade_no":         "EPY770003",
			"payment_method":   "wxpay",
			"payment_provider": "epay",
			"status":           "expired",
			"create_time":      int64(1700003000),
			"ext": map[string]any{
				"trade_no":           "EPY770003",
				"method":             "wxpay",
				"payment_scene":      "team_topup",
				"payment_scene_name": "Team top-up",
				"face_usd":           "20",
			},
		}),
		// Foreign user record that must never leak even if returned by a stub.
		mustAdtyTopUpViewFromJSON(t, map[string]any{
			"id":               4,
			"user_id":          999,
			"trade_no":         "EPU999004",
			"payment_method":   "usdt.trc20",
			"payment_provider": "bepusdt",
			"status":           "success",
			"create_time":      int64(1700001500),
		}),
	}

	origFn := listUserAdtyTopUpsFn
	var observedUserIDs []int
	listUserAdtyTopUpsFn = func(userID int, _ *common.PageInfo) ([]model.AdtyTopUpView, int64, error) {
		observedUserIDs = append(observedUserIDs, userID)
		return dataset, int64(len(dataset)), nil
	}
	t.Cleanup(func() {
		listUserAdtyTopUpsFn = origFn
	})

	callSelf := func(query string) (bool, int, []map[string]any) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		target := "/api/adty/topup/self"
		if query != "" {
			target += "?" + query
		}
		c.Request = httptest.NewRequest(http.MethodGet, target, nil)
		c.Set("id", authedUserID)
		GetAdtyTopUpSelf(c)

		var resp struct {
			Success bool `json:"success"`
			Data    struct {
				Total int              `json:"total"`
				Items []map[string]any `json:"items"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp), rec.Body.String())
		return resp.Success, resp.Data.Total, resp.Data.Items
	}

	// 1. Filter by status=success and attempt query user_id override
	ok, total, items := callSelf("status=success&user_id=999")
	require.True(t, ok)
	assert.Equal(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, "EPU770001", items[0]["trade_no"])
	assert.Equal(t, authedUserID, observedUserIDs[len(observedUserIDs)-1])

	// 2. Filter by payment_method=alipay
	ok, total, items = callSelf("payment_method=alipay")
	require.True(t, ok)
	assert.Equal(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, "EPY770002", items[0]["trade_no"])

	// 3. Filter by trade_no substring
	ok, total, items = callSelf("trade_no=770003")
	require.True(t, ok)
	assert.Equal(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, "EPY770003", items[0]["trade_no"])

	// 4. Filter by keyword matching method_label
	ok, total, items = callSelf("keyword=支付宝1")
	require.True(t, ok)
	assert.Equal(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, "EPY770002", items[0]["trade_no"])

	// 5. Filter by time range
	ok, total, items = callSelf("start_timestamp=1700001500&end_timestamp=1700002500")
	require.True(t, ok)
	assert.Equal(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, "EPY770002", items[0]["trade_no"])

	// 6. Invalid time range (start > end) returns error
	ok, _, _ = callSelf("start_timestamp=1700003000&end_timestamp=1700001000")
	assert.False(t, ok)

	// 7. Invalid timestamp format returns error
	ok, _, _ = callSelf("start_timestamp=not-a-timestamp")
	assert.False(t, ok)
}
