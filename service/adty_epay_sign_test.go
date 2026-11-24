package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testRSAKeyPair struct {
	pkcs8PEM    string
	pkcs8Base64 string
	pkcs1PEM    string
	pkcs1Base64 string
	pkixPEM     string
	pkixBase64  string
	pubPKCS1PEM string
	pubPKCS1B64 string
}

func generateTestRSAKeyPair(t *testing.T) testRSAKeyPair {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pkcs8DER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	pkcs1DER := x509.MarshalPKCS1PrivateKey(key)
	pkixDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	pubPKCS1DER := x509.MarshalPKCS1PublicKey(&key.PublicKey)

	return testRSAKeyPair{
		pkcs8PEM:    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8DER})),
		pkcs8Base64: base64.StdEncoding.EncodeToString(pkcs8DER),
		pkcs1PEM:    string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: pkcs1DER})),
		pkcs1Base64: base64.StdEncoding.EncodeToString(pkcs1DER),
		pkixPEM:     string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pkixDER})),
		pkixBase64:  base64.StdEncoding.EncodeToString(pkixDER),
		pubPKCS1PEM: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: pubPKCS1DER})),
		pubPKCS1B64: base64.StdEncoding.EncodeToString(pubPKCS1DER),
	}
}

func setFixedEpayClock(t *testing.T, now time.Time) {
	t.Helper()
	orig := adtyEpayNow
	adtyEpayNow = func() time.Time { return now }
	t.Cleanup(func() { adtyEpayNow = orig })
}

func TestEpayMD5SignAndVerify(t *testing.T) {
	params := map[string]string{
		"pid":          "1001",
		"type":         "alipay",
		"out_trade_no": "EPY20261124001",
		"money":        "12.34",
		"trade_status": "TRADE_SUCCESS",
		"empty":        "",
	}
	before := maps.Clone(params)
	sign, err := EpaySign(params, "md5-secret")
	require.NoError(t, err)
	require.NotEmpty(t, sign)
	assert.Equal(t, before, params, "EpaySign must not mutate caller params")

	signed := maps.Clone(params)
	signed["sign"] = sign
	signed["sign_type"] = "MD5"
	assert.True(t, EpayVerify(signed, "md5-secret"))
	assert.False(t, EpayVerify(signed, "wrong-secret"))

	tampered := maps.Clone(signed)
	tampered["money"] = "99.99"
	assert.False(t, EpayVerify(tampered, "md5-secret"))

	ambiguous := maps.Clone(signed)
	ambiguous["name"] = "item&money=0.01"
	assert.False(t, EpayVerify(ambiguous, "md5-secret"))

	_, err = EpaySign(map[string]string{"name": "a&b=1"}, "md5-secret")
	require.Error(t, err)
}

func TestEpayRSAKeyParsingAndSignVerify(t *testing.T) {
	fixedNow := time.Unix(1764000000, 0)
	setFixedEpayClock(t, fixedNow)

	pair := generateTestRSAKeyPair(t)
	cases := []struct {
		name       string
		privateKey string
		publicKey  string
	}{
		{name: "PKCS8 PEM + PKIX PEM", privateKey: pair.pkcs8PEM, publicKey: pair.pkixPEM},
		{name: "PKCS8 raw Base64 + PKIX raw Base64", privateKey: pair.pkcs8Base64, publicKey: pair.pkixBase64},
		{name: "PKCS1 PEM + PKCS1 PEM", privateKey: pair.pkcs1PEM, publicKey: pair.pubPKCS1PEM},
		{name: "PKCS1 wrapped Base64 + PKCS1 wrapped Base64", privateKey: pair.pkcs1Base64[:32] + "\n" + pair.pkcs1Base64[32:], publicKey: pair.pubPKCS1B64[:32] + "\n" + pair.pubPKCS1B64[32:]},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]string{
				"pid":          "2001",
				"type":         "wxpay",
				"out_trade_no": "EPY20261124002",
				"money":        "0",
				"blank":        "   \t\n",
				"timestamp":    strconv.FormatInt(fixedNow.Unix(), 10),
			}
			sig, err := EpaySignRSA(params, tc.privateKey)
			require.NoError(t, err)
			require.NotEmpty(t, sig)

			callback := maps.Clone(params)
			callback["sign"] = sig
			callback["sign_type"] = "RSA"
			ok, err := EpayVerifyRSA(callback, tc.publicKey)
			require.NoError(t, err)
			assert.True(t, ok)
		})
	}

	t.Run("invalid keys rejected", func(t *testing.T) {
		_, err := ParseEpayRSAPrivateKey("")
		require.ErrorIs(t, err, ErrInvalidEpayRSAKey)
		_, err = ParseEpayRSAPrivateKey("not-a-key")
		require.ErrorIs(t, err, ErrInvalidEpayRSAKey)
		_, err = ParseEpayRSAPrivateKey(pair.pkixPEM)
		require.ErrorIs(t, err, ErrInvalidEpayRSAKey)

		_, err = ParseEpayRSAPublicKey("")
		require.ErrorIs(t, err, ErrInvalidEpayRSAKey)
		_, err = ParseEpayRSAPublicKey("not-a-key")
		require.ErrorIs(t, err, ErrInvalidEpayRSAKey)
		_, err = ParseEpayRSAPublicKey(pair.pkcs8PEM)
		require.ErrorIs(t, err, ErrInvalidEpayRSAKey)
	})
}

func TestEpayCallbackVerificationSecurity(t *testing.T) {
	fixedNow := time.Unix(1764000000, 0)
	setFixedEpayClock(t, fixedNow)

	merchantKeys := generateTestRSAKeyPair(t)
	platformKeys := generateTestRSAKeyPair(t)

	rsaChannel := &model.AdtyEpayChannel{
		Id:                 1,
		MerchantId:         "merchant_a",
		MerchantKey:        "leftover-md5-key",
		SignType:           model.AdtyEpaySignTypeRSA,
		MerchantPrivateKey: merchantKeys.pkcs8Base64,
		PlatformPublicKey:  platformKeys.pkixBase64,
		Status:             model.AdtyEpayStatusEnabled,
	}
	md5Channel := &model.AdtyEpayChannel{
		Id:                 2,
		MerchantId:         "merchant_a",
		MerchantKey:        "md5-secret",
		SignType:           model.AdtyEpaySignTypeMD5,
		MerchantPrivateKey: merchantKeys.pkcs8Base64,
		PlatformPublicKey:  platformKeys.pkixBase64,
		Status:             model.AdtyEpayStatusEnabled,
	}
	// Simulated platform signer: signs callbacks with platform private key.
	platformSigner := &model.AdtyEpayChannel{
		MerchantId:         "merchant_a",
		SignType:           model.AdtyEpaySignTypeRSA,
		MerchantPrivateKey: platformKeys.pkcs8PEM,
		PlatformPublicKey:  merchantKeys.pkixPEM,
	}

	baseFields := map[string]string{
		"type":         "alipay",
		"money":        "10.00",
		"name":         "Top-up EPY1",
		"out_trade_no": "EPY1",
		"trade_no":     "GW1",
		"trade_status": "TRADE_SUCCESS",
	}

	validRSA, err := EpaySignChannelParams(platformSigner, baseFields)
	require.NoError(t, err)
	assert.True(t, EpayVerifyCallback(rsaChannel, validRSA))

	t.Run("tampered RSA payload rejected", func(t *testing.T) {
		tampered := maps.Clone(validRSA)
		tampered["money"] = "100.00"
		assert.False(t, EpayVerifyCallback(rsaChannel, tampered))
	})

	t.Run("signed with merchant private key instead of platform key rejected", func(t *testing.T) {
		selfSigned, err := EpaySignChannelParams(rsaChannel, baseFields)
		require.NoError(t, err)
		assert.False(t, EpayVerifyCallback(rsaChannel, selfSigned))
	})

	t.Run("wrong pid rejected on RSA callback (#8422)", func(t *testing.T) {
		otherMerchantPlatform := &model.AdtyEpayChannel{
			MerchantId:         "merchant_b",
			SignType:           model.AdtyEpaySignTypeRSA,
			MerchantPrivateKey: platformKeys.pkcs8PEM,
			PlatformPublicKey:  merchantKeys.pkixPEM,
		}
		otherMerchantCallback, err := EpaySignChannelParams(otherMerchantPlatform, baseFields)
		require.NoError(t, err)
		// Cryptographic signature is genuinely signed by the same platform private key,
		// so only strict pid validation prevents cross-merchant replay.
		ok, err := EpayVerifyRSA(otherMerchantCallback, rsaChannel.PlatformPublicKey)
		require.NoError(t, err)
		require.True(t, ok)
		assert.False(t, EpayVerifyCallback(rsaChannel, otherMerchantCallback))

		missingPID := maps.Clone(otherMerchantCallback)
		delete(missingPID, "pid")
		assert.False(t, EpayVerifyCallback(rsaChannel, missingPID))
	})

	t.Run("cross-sign-type callbacks rejected", func(t *testing.T) {
		md5Signed, err := EpaySignChannelParams(md5Channel, baseFields)
		require.NoError(t, err)
		assert.True(t, EpayVerifyCallback(md5Channel, md5Signed))

		// Legacy MD5 callback without sign_type stays accepted for MD5 channel.
		md5NoSignType := maps.Clone(md5Signed)
		delete(md5NoSignType, "sign_type")
		assert.True(t, EpayVerifyCallback(md5Channel, md5NoSignType))

		// MD5 callback sent to RSA channel (even with leftover MD5 key) must fail.
		md5WithLeftover := maps.Clone(baseFields)
		md5WithLeftover["pid"] = rsaChannel.MerchantId
		leftoverSig, err := EpaySign(md5WithLeftover, rsaChannel.MerchantKey)
		require.NoError(t, err)
		md5WithLeftover["sign"] = leftoverSig
		md5WithLeftover["sign_type"] = "MD5"
		assert.False(t, EpayVerifyCallback(rsaChannel, md5WithLeftover))

		// RSA-signed callback claiming MD5 or omitting sign_type must fail on RSA channel.
		rsaWrongType := maps.Clone(validRSA)
		rsaWrongType["sign_type"] = "MD5"
		assert.False(t, EpayVerifyCallback(rsaChannel, rsaWrongType))

		rsaEmptyType := maps.Clone(validRSA)
		delete(rsaEmptyType, "sign_type")
		assert.False(t, EpayVerifyCallback(rsaChannel, rsaEmptyType))

		// RSA callback sent to MD5 channel must fail.
		assert.False(t, EpayVerifyCallback(md5Channel, validRSA))
	})

	t.Run("RSA timestamp drift rejected", func(t *testing.T) {
		stale := maps.Clone(baseFields)
		stale["pid"] = rsaChannel.MerchantId
		stale["timestamp"] = strconv.FormatInt(fixedNow.Add(-6*time.Minute).Unix(), 10)
		staleSig, err := EpaySignRSA(stale, platformKeys.pkcs8PEM)
		require.NoError(t, err)
		stale["sign"] = staleSig
		stale["sign_type"] = "RSA"
		ok, err := EpayVerifyRSA(stale, rsaChannel.PlatformPublicKey)
		assert.False(t, ok)
		require.ErrorIs(t, err, ErrInvalidEpayTimestamp)
		assert.False(t, EpayVerifyCallback(rsaChannel, stale))

		missingTS := maps.Clone(baseFields)
		missingTS["pid"] = rsaChannel.MerchantId
		missingSig, err := EpaySignRSA(missingTS, platformKeys.pkcs8PEM)
		require.NoError(t, err)
		missingTS["sign"] = missingSig
		missingTS["sign_type"] = "RSA"
		ok, err = EpayVerifyRSA(missingTS, rsaChannel.PlatformPublicKey)
		assert.False(t, ok)
		require.ErrorIs(t, err, ErrInvalidEpayTimestamp)
		assert.False(t, EpayVerifyCallback(rsaChannel, missingTS))
	})

	t.Run("revoked RSA channel rejected", func(t *testing.T) {
		revoked := *rsaChannel
		revoked.Status = model.AdtyEpayStatusRevoked
		assert.False(t, EpayVerifyCallback(&revoked, validRSA))
	})

	t.Run("ambiguous separator rejected on RSA callback", func(t *testing.T) {
		ambiguous := maps.Clone(validRSA)
		ambiguous["name"] = "item&money=0.01"
		assert.False(t, EpayVerifyCallback(rsaChannel, ambiguous))
	})
}

func TestEpayChannelIdentityAndRSAClientFlows(t *testing.T) {
	fixedNow := time.Unix(1764000000, 0)
	setFixedEpayClock(t, fixedNow)

	merchantKeys := generateTestRSAKeyPair(t)
	platformKeys := generateTestRSAKeyPair(t)

	t.Run("SamePaymentIdentity covers SignType and RSA keys", func(t *testing.T) {
		a := &model.AdtyEpayChannel{
			Id:                 1,
			GatewayURL:         "https://pay.example.com",
			MerchantId:         "1001",
			SignType:           model.AdtyEpaySignTypeRSA,
			MerchantPrivateKey: merchantKeys.pkcs8Base64,
			PlatformPublicKey:  platformKeys.pkixBase64,
			OrderMode:          model.AdtyEpayOrderModeAPI,
			Methods:            []model.AdtyEpayChannelMethod{{Method: "alipay", TypeCode: "alipay"}},
		}
		b := *a
		b.Methods = append([]model.AdtyEpayChannelMethod(nil), a.Methods...)
		assert.True(t, a.SamePaymentIdentity(&b))
		assert.True(t, a.HasCredentials())

		b.SignType = model.AdtyEpaySignTypeMD5
		assert.False(t, a.SamePaymentIdentity(&b))

		c := *a
		c.Methods = append([]model.AdtyEpayChannelMethod(nil), a.Methods...)
		c.PlatformPublicKey = merchantKeys.pkixBase64
		assert.False(t, a.SamePaymentIdentity(&c))

		legacy := &model.AdtyEpayChannel{Id: 2, MerchantKey: "k"}
		assert.Equal(t, model.AdtyEpaySignTypeMD5, legacy.EffectiveSignType())
		assert.False(t, legacy.IsRSA())
		assert.True(t, legacy.HasCredentials())
	})

	// Mock Epay v2 RSA gateway verifying merchant requests and signing JSON responses.
	signPlatformJSON := func(t *testing.T, payload map[string]any) []byte {
		t.Helper()
		payload["timestamp"] = strconv.FormatInt(fixedNow.Unix(), 10)
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
		// Sign with platform private key (matching verifyEpayRSA with allowAmpersandValues=true).
		priv, err := ParseEpayRSAPrivateKey(platformKeys.pkcs8PEM)
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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		formMap := make(map[string]string, len(r.PostForm))
		for k, v := range r.PostForm {
			if len(v) > 0 {
				formMap[k] = v[0]
			}
		}
		// Verify merchant signature using merchant public key.
		ok, err := EpayVerifyRSA(formMap, merchantKeys.pkixPEM)
		require.NoError(t, err)
		require.True(t, ok, "gateway must receive valid merchant RSA signature")
		require.Equal(t, "RSA", formMap["sign_type"])
		require.Equal(t, "1001", formMap["pid"])

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/pay/create":
			_, _ = w.Write(signPlatformJSON(t, map[string]any{
				"code":     0,
				"trade_no": "GW20261124001",
				"pay_type": "jump",
				"pay_info": "https://cashier.example.com/pay?a=1&b=2",
			}))
		case "/api/pay/query":
			if formMap["out_trade_no"] == "MISSING_ORDER" {
				raw, _ := common.Marshal(map[string]any{"code": -1, "msg": "订单不存在"})
				_, _ = w.Write(raw)
				return
			}
			_, _ = w.Write(signPlatformJSON(t, map[string]any{
				"code":         0,
				"pid":          "1001",
				"trade_no":     "GW20261124001",
				"out_trade_no": formMap["out_trade_no"],
				"type":         "alipay",
				"money":        "15.50",
				"status":       1,
				"endtime":      strconv.FormatInt(fixedNow.Unix(), 10),
			}))
		case "/api/merchant/info":
			_, _ = w.Write(signPlatformJSON(t, map[string]any{
				"code":   0,
				"pid":    1001,
				"status": 1,
			}))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	origClient := adtyEpayHTTPClient
	adtyEpayHTTPClient = func() *http.Client { return server.Client() }
	t.Cleanup(func() { adtyEpayHTTPClient = origClient })

	channel := &model.AdtyEpayChannel{
		Id:                 10,
		GatewayURL:         server.URL,
		MerchantId:         "1001",
		SignType:           model.AdtyEpaySignTypeRSA,
		MerchantPrivateKey: merchantKeys.pkcs8Base64,
		PlatformPublicKey:  platformKeys.pkixBase64,
		OrderMode:          model.AdtyEpayOrderModeSubmit,
		ProbeMode:          "query",
		Status:             model.AdtyEpayStatusEnabled,
	}
	method := &model.AdtyEpayChannelMethod{Method: "alipay", TypeCode: "alipay", Enabled: true}
	req := AdtyEpayCreateRequest{
		TradeNo:   "EPY20261124001",
		Name:      "Top-up EPY20261124001",
		ClientIP:  "127.0.0.1",
		Device:    "pc",
		MoneyCNY:  decimal.RequireFromString("15.50"),
		NotifyURL: "https://shop.example.com/api/user/epay/notify/10",
		ReturnURL: "https://shop.example.com/pay/result/token",
	}

	t.Run("RSA submit order builds /api/pay/submit link", func(t *testing.T) {
		res, err := CreateAdtyEpayOrder(context.Background(), channel, method, req)
		require.NoError(t, err)
		assert.Equal(t, "submit", res.Mode)
		parsedURL, err := url.Parse(res.PayURL)
		require.NoError(t, err)
		assert.Equal(t, "/api/pay/submit", parsedURL.Path)
		queryMap := make(map[string]string, len(parsedURL.Query()))
		for k, v := range parsedURL.Query() {
			if len(v) > 0 {
				queryMap[k] = v[0]
			}
		}
		assert.Equal(t, "RSA", queryMap["sign_type"])
		assert.Equal(t, "1001", queryMap["pid"])
		ok, err := EpayVerifyRSA(queryMap, merchantKeys.pkixPEM)
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("RSA API order calls /api/pay/create and verifies signed response", func(t *testing.T) {
		apiChannel := *channel
		apiChannel.OrderMode = model.AdtyEpayOrderModeAPI
		res, err := CreateAdtyEpayOrder(context.Background(), &apiChannel, method, req)
		require.NoError(t, err)
		assert.Equal(t, "payurl", res.Mode)
		assert.Equal(t, "GW20261124001", res.GatewayTradeNo)
		assert.Equal(t, "https://cashier.example.com/pay?a=1&b=2", res.PayURL)
	})

	t.Run("RSA query order verifies paid response and handles missing order", func(t *testing.T) {
		qr, err := QueryAdtyEpayOrder(context.Background(), channel, "EPY20261124001")
		require.NoError(t, err)
		assert.True(t, qr.Paid)
		assert.Equal(t, "1001", qr.MerchantId)
		assert.Equal(t, "GW20261124001", qr.GatewayTradeNo)
		assert.Equal(t, "EPY20261124001", qr.OutTradeNo)
		assert.Equal(t, "alipay", qr.TypeCode)
		assert.True(t, qr.MoneyCNY.Equal(decimal.RequireFromString("15.50")))

		_, err = QueryAdtyEpayOrder(context.Background(), channel, "MISSING_ORDER")
		require.ErrorIs(t, err, errEpayOrderAbsent)
	})

	t.Run("RSA probe succeeds for merchant info and order modes", func(t *testing.T) {
		require.NoError(t, ProbeAdtyEpayChannel(context.Background(), channel))
		orderProbe := *channel
		orderProbe.ProbeMode = "order"
		require.NoError(t, ProbeAdtyEpayChannel(context.Background(), &orderProbe))
	})

	t.Run("RSA response verification rejects tampered JSON response", func(t *testing.T) {
		tamperedRaw := signPlatformJSON(t, map[string]any{
			"code":     0,
			"trade_no": "GW1",
			"pay_type": "jump",
			"pay_info": "https://cashier.example.com/pay?a=1&b=2",
		})
		var body map[string]any
		require.NoError(t, common.Unmarshal(tamperedRaw, &body))
		body["pay_info"] = "https://evil.example.com/steal"
		modified, err := common.Marshal(body)
		require.NoError(t, err)
		_, err = EpayVerifyRSAResponse(channel, modified)
		require.ErrorIs(t, err, ErrInvalidEpaySignature)
	})
}
