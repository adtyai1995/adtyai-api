package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func signEpayPlatformPayloadForClientTest(t *testing.T, platformPrivPEM string, now time.Time, payload map[string]any) []byte {
	t.Helper()
	payload["timestamp"] = strconv.FormatInt(now.Unix(), 10)
	stringFields := make(map[string]string, len(payload))
	for k, v := range payload {
		switch val := v.(type) {
		case string:
			stringFields[k] = val
		case int:
			stringFields[k] = strconv.Itoa(val)
		}
	}
	content, err := epayRSASignContent(stringFields, true)
	require.NoError(t, err)
	priv, err := ParseEpayRSAPrivateKey(platformPrivPEM)
	require.NoError(t, err)
	digest := sha256.Sum256([]byte(content))
	sigBytes, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
	require.NoError(t, err)
	payload["sign"] = base64.StdEncoding.EncodeToString(sigBytes)
	payload["sign_type"] = "RSA"
	raw, err := common.Marshal(payload)
	require.NoError(t, err)
	return raw
}

func TestCreateAdtyEpayOrderRSAHtmlFallbackToJump(t *testing.T) {
	fixedNow := time.Unix(1764000000, 0)
	setFixedEpayClock(t, fixedNow)

	merchantKeys := generateTestRSAKeyPair(t)
	platformKeys := generateTestRSAKeyPair(t)

	var methodsSeen []string
	var outTradeNos []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/pay/create", r.URL.Path)
		require.NoError(t, r.ParseForm())
		methodParam := r.PostForm.Get("method")
		methodsSeen = append(methodsSeen, methodParam)
		outTradeNos = append(outTradeNos, r.PostForm.Get("out_trade_no"))

		w.Header().Set("Content-Type", "application/json")
		if methodParam == "web" {
			_, _ = w.Write(signEpayPlatformPayloadForClientTest(t, platformKeys.pkcs8PEM, fixedNow, map[string]any{
				"code":     0,
				"trade_no": "GW_HTML_1",
				"pay_type": "html",
				"pay_info": "<form action='https://pay.example.com'></form>",
			}))
			return
		}
		_, _ = w.Write(signEpayPlatformPayloadForClientTest(t, platformKeys.pkcs8PEM, fixedNow, map[string]any{
			"code":     0,
			"trade_no": "GW_HTML_1",
			"pay_type": "jump",
			"pay_info": "https://pay.example.com/pay/submit/GW_HTML_1/",
		}))
	}))
	defer server.Close()

	origClient := adtyEpayHTTPClient
	adtyEpayHTTPClient = func() *http.Client { return server.Client() }
	t.Cleanup(func() { adtyEpayHTTPClient = origClient })

	channel := &model.AdtyEpayChannel{
		Id:                 11,
		GatewayURL:         server.URL,
		MerchantId:         "1001",
		SignType:           model.AdtyEpaySignTypeRSA,
		MerchantPrivateKey: merchantKeys.pkcs8Base64,
		PlatformPublicKey:  platformKeys.pkixBase64,
		OrderMode:          model.AdtyEpayOrderModeAPI,
		Status:             model.AdtyEpayStatusEnabled,
	}
	method := &model.AdtyEpayChannelMethod{Method: "alipay", TypeCode: "alipay", Enabled: true}
	req := AdtyEpayCreateRequest{
		TradeNo:   "EPY_HTML_FALLBACK",
		Name:      "Top-up",
		ClientIP:  "127.0.0.1",
		Device:    "pc",
		MoneyCNY:  decimal.RequireFromString("12.00"),
		NotifyURL: "https://shop.example.com/api/user/epay/notify/11",
		ReturnURL: "https://shop.example.com/wallet",
	}

	res, err := CreateAdtyEpayOrder(context.Background(), channel, method, req)
	require.NoError(t, err)
	assert.Equal(t, []string{"web", "jump"}, methodsSeen)
	assert.Equal(t, []string{"EPY_HTML_FALLBACK", "EPY_HTML_FALLBACK"}, outTradeNos)
	assert.Equal(t, "payurl", res.Mode)
	assert.Equal(t, "GW_HTML_1", res.GatewayTradeNo)
	assert.Equal(t, "https://pay.example.com/pay/submit/GW_HTML_1/", res.PayURL)
}

func TestEpayQueryExtendedStatusesAndNumericMD5Fields(t *testing.T) {
	fixedNow := time.Unix(1764000000, 0)
	setFixedEpayClock(t, fixedNow)

	merchantKeys := generateTestRSAKeyPair(t)
	platformKeys := generateTestRSAKeyPair(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mapi.php":
			// Unquoted numeric trade_no in MD5 create response.
			_, _ = w.Write([]byte(`{"code":1,"trade_no":202611260099,"payurl":"https://pay.example.com/p/99"}`))
		case "/api.php":
			st := r.URL.Query().Get("out_trade_no")
			switch st {
			case "STATUS_2", "STATUS_3", "STATUS_4":
				num := st[len(st)-1:]
				_, _ = w.Write([]byte(`{"code":1,"pid":1001,"trade_no":99887766,"out_trade_no":` + strconv.Quote(st) + `,"type":"alipay","money":"20.00","status":` + num + `}`))
			case "NUM_ID":
				_, _ = w.Write([]byte(`{"code":1,"pid":1001,"trade_no":202611260099,"out_trade_no":1234567890,"type":"alipay","money":"20.00","status":1,"endtime":` + strconv.FormatInt(fixedNow.Unix()+30, 10) + `}`))
			default:
				_, _ = w.Write([]byte(`{"code":1,"pid":1001,"trade_no":"GW9","out_trade_no":"UNKNOWN","type":"alipay","money":"20.00","status":9}`))
			}
		case "/api/pay/query":
			require.NoError(t, r.ParseForm())
			otn := r.PostForm.Get("out_trade_no")
			statusVal := 2
			switch otn {
			case "RSA_STATUS_3":
				statusVal = 3
			case "RSA_STATUS_4":
				statusVal = 4
			case "RSA_STATUS_9":
				statusVal = 9
			}
			_, _ = w.Write(signEpayPlatformPayloadForClientTest(t, platformKeys.pkcs8PEM, fixedNow, map[string]any{
				"code":         0,
				"pid":          "1001",
				"trade_no":     "GW_RSA_ST",
				"out_trade_no": otn,
				"type":         "alipay",
				"money":        "20.00",
				"status":       statusVal,
			}))
		}
	}))
	defer server.Close()

	origClient := adtyEpayHTTPClient
	adtyEpayHTTPClient = func() *http.Client { return server.Client() }
	t.Cleanup(func() { adtyEpayHTTPClient = origClient })

	md5Ch := &model.AdtyEpayChannel{
		Id: 1, GatewayURL: server.URL, MerchantId: "1001", MerchantKey: "sec",
		SignType: model.AdtyEpaySignTypeMD5, OrderMode: model.AdtyEpayOrderModeAPI, Status: model.AdtyEpayStatusEnabled,
	}
	rsaCh := &model.AdtyEpayChannel{
		Id: 2, GatewayURL: server.URL, MerchantId: "1001",
		SignType: model.AdtyEpaySignTypeRSA, MerchantPrivateKey: merchantKeys.pkcs8Base64,
		PlatformPublicKey: platformKeys.pkixBase64, OrderMode: model.AdtyEpayOrderModeAPI, Status: model.AdtyEpayStatusEnabled,
	}
	method := &model.AdtyEpayChannelMethod{Method: "alipay", TypeCode: "alipay", Enabled: true}

	t.Run("MD5 create accepts unquoted numeric trade_no", func(t *testing.T) {
		res, err := CreateAdtyEpayOrder(context.Background(), md5Ch, method, AdtyEpayCreateRequest{
			TradeNo: "EPY_NUM", Name: "Test", ClientIP: "127.0.0.1", Device: "pc",
			MoneyCNY: decimal.NewFromInt(20), NotifyURL: "https://shop.example.com/cb",
		})
		require.NoError(t, err)
		assert.Equal(t, "202611260099", res.GatewayTradeNo)
	})

	t.Run("MD5 query handles numeric trade_no/out_trade_no and statuses 2/3/4", func(t *testing.T) {
		qr, err := QueryAdtyEpayOrder(context.Background(), md5Ch, "NUM_ID")
		require.NoError(t, err)
		assert.True(t, qr.Paid)
		assert.Equal(t, "1", qr.RawStatus)
		assert.Equal(t, "202611260099", qr.GatewayTradeNo)
		assert.Equal(t, "1234567890", qr.OutTradeNo)
		assert.Equal(t, fixedNow.Unix()+30, qr.PaidAt)

		for _, tc := range []struct {
			outNo  string
			expect string
		}{
			{"STATUS_2", "2"},
			{"STATUS_3", "3"},
			{"STATUS_4", "4"},
		} {
			qr, err := QueryAdtyEpayOrder(context.Background(), md5Ch, tc.outNo)
			require.NoError(t, err)
			assert.False(t, qr.Paid)
			assert.Equal(t, tc.expect, qr.RawStatus)
			assert.Equal(t, "99887766", qr.GatewayTradeNo)
		}

		_, err = QueryAdtyEpayOrder(context.Background(), md5Ch, "UNKNOWN")
		require.Error(t, err)
	})

	t.Run("RSA query handles statuses 2/3/4 and rejects unknown status 9", func(t *testing.T) {
		for _, tc := range []struct {
			outNo  string
			expect string
		}{
			{"RSA_STATUS_2", "2"},
			{"RSA_STATUS_3", "3"},
			{"RSA_STATUS_4", "4"},
		} {
			qr, err := QueryAdtyEpayOrder(context.Background(), rsaCh, tc.outNo)
			require.NoError(t, err)
			assert.False(t, qr.Paid)
			assert.Equal(t, tc.expect, qr.RawStatus)
		}

		_, err := QueryAdtyEpayOrder(context.Background(), rsaCh, "RSA_STATUS_9")
		require.Error(t, err)
	})
}

func TestSubmitModeDefaultReturnURLPaidTimeAndRefreshSubmitURL(t *testing.T) {
	fixedNow := time.Unix(1764000000, 0)
	setFixedEpayClock(t, fixedNow)

	origCallback := operation_setting.GetCustomCallbackAddress()
	operation_setting.SetCustomCallbackAddress("https://callback.example.com/")
	t.Cleanup(func() { operation_setting.SetCustomCallbackAddress(origCallback) })

	merchantKeys := generateTestRSAKeyPair(t)
	platformKeys := generateTestRSAKeyPair(t)

	rsaSubmitCh := &model.AdtyEpayChannel{
		Id:                 21,
		GatewayURL:         "https://gateway.example.com",
		MerchantId:         "1001",
		SignType:           model.AdtyEpaySignTypeRSA,
		MerchantPrivateKey: merchantKeys.pkcs8Base64,
		PlatformPublicKey:  platformKeys.pkixBase64,
		OrderMode:          model.AdtyEpayOrderModeSubmit,
		Status:             model.AdtyEpayStatusEnabled,
	}
	md5SubmitCh := &model.AdtyEpayChannel{
		Id:          22,
		GatewayURL:  "https://gateway.example.com",
		MerchantId:  "1001",
		MerchantKey: "secret",
		SignType:    model.AdtyEpaySignTypeMD5,
		OrderMode:   model.AdtyEpayOrderModeSubmit,
		Status:      model.AdtyEpayStatusEnabled,
	}
	method := &model.AdtyEpayChannelMethod{Method: "alipay", TypeCode: "alipay", Enabled: true}
	req := AdtyEpayCreateRequest{
		TradeNo:   "EPY_SUBMIT_1",
		Name:      "Top-up",
		Device:    "pc",
		MoneyCNY:  decimal.RequireFromString("10.00"),
		NotifyURL: "https://callback.example.com/api/user/epay/notify/21",
		ReturnURL: "", // intentionally blank
	}

	t.Run("submit mode populates fallback return_url when blank", func(t *testing.T) {
		res, err := CreateAdtyEpayOrder(context.Background(), rsaSubmitCh, method, req)
		require.NoError(t, err)
		parsed, err := url.Parse(res.PayURL)
		require.NoError(t, err)
		assert.Equal(t, "https://callback.example.com/wallet", parsed.Query().Get("return_url"))
	})

	t.Run("AdtyEpayPaidTime uses adtyEpayNow with 60s forward skew tolerance", func(t *testing.T) {
		assert.Equal(t, fixedNow.Unix()+60, AdtyEpayPaidTime(strconv.FormatInt(fixedNow.Unix()+60, 10)))
		assert.Equal(t, int64(0), AdtyEpayPaidTime(strconv.FormatInt(fixedNow.Unix()+61, 10)))
		assert.Equal(t, fixedNow.Unix()+30, AdtyEpayPaidTime(fixedNow.Add(30*time.Second).UTC().Format(time.RFC3339)))
		assert.Equal(t, int64(0), AdtyEpayPaidTime(fixedNow.Add(61*time.Second).UTC().Format(time.RFC3339)))
	})

	t.Run("RefreshAdtyEpaySubmitURL refreshes stale/missing RSA submit URLs and keeps fresh/MD5 URLs", func(t *testing.T) {
		initial, err := CreateAdtyEpayOrder(context.Background(), rsaSubmitCh, method, req)
		require.NoError(t, err)

		// 119s later: still fresh, not refreshed.
		setFixedEpayClock(t, fixedNow.Add(119*time.Second))
		got, refreshed, err := RefreshAdtyEpaySubmitURL(rsaSubmitCh, method, req, initial.PayURL)
		require.NoError(t, err)
		assert.False(t, refreshed)
		assert.Equal(t, initial.PayURL, got)

		// 120s later: stale, re-signed with new timestamp.
		later := fixedNow.Add(120 * time.Second)
		setFixedEpayClock(t, later)
		got, refreshed, err = RefreshAdtyEpaySubmitURL(rsaSubmitCh, method, req, initial.PayURL)
		require.NoError(t, err)
		assert.True(t, refreshed)
		assert.NotEqual(t, initial.PayURL, got)
		parsed, err := url.Parse(got)
		require.NoError(t, err)
		assert.Equal(t, strconv.FormatInt(later.Unix(), 10), parsed.Query().Get("timestamp"))
		queryMap := make(map[string]string, len(parsed.Query()))
		for k, v := range parsed.Query() {
			if len(v) > 0 {
				queryMap[k] = v[0]
			}
		}
		ok, err := EpayVerifyRSA(queryMap, merchantKeys.pkixPEM)
		require.NoError(t, err)
		assert.True(t, ok)

		// Missing timestamp triggers refresh.
		got, refreshed, err = RefreshAdtyEpaySubmitURL(rsaSubmitCh, method, req, "https://gateway.example.com/api/pay/submit?pid=1001")
		require.NoError(t, err)
		assert.True(t, refreshed)
		assert.Contains(t, got, "timestamp=")

		// MD5 submit channel is never refreshed.
		got, refreshed, err = RefreshAdtyEpaySubmitURL(md5SubmitCh, method, req, "https://gateway.example.com/submit.php?pid=1001")
		require.NoError(t, err)
		assert.False(t, refreshed)
		assert.Equal(t, "https://gateway.example.com/submit.php?pid=1001", got)
	})
}
