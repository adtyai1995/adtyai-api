package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setServiceTestBEpusdt(t *testing.T, enabled bool, baseURL, secret string, tradeTypes []string) {
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

func setServiceTestUSDTOrderMinutes(t *testing.T, minutes int) {
	t.Helper()
	old := adty_setting.GetTopupSetting().USDTOrderMinutes
	_ = config.GlobalConfig.LoadFromDB(map[string]string{
		adty_setting.TopupSettingKeyUSDTOrderMinutes: strconv.Itoa(minutes),
	})
	t.Cleanup(func() {
		_ = config.GlobalConfig.LoadFromDB(map[string]string{
			adty_setting.TopupSettingKeyUSDTOrderMinutes: strconv.Itoa(old),
		})
	})
}

func setupTeamPayLifecycleTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&model.Option{},
		&model.User{},
		&model.TopUp{},
		&model.AdtyTopupOrder{},
		&model.AdtyEpayChannel{},
		&model.AdtyEpayChannelMethod{},
		&model.AdtyPaymentSceneConfig{},
		&model.AdtySite{},
		&model.AdtySiteDomain{},
		&model.AdtySiteStaff{},
		&model.AdtyTeam{},
		&model.AdtyTeamMember{},
		&model.AdtyTeamLedger{},
		&model.AdtyOrderPaymentEvent{},
		&model.PaymentOrphanEvent{},
		&model.ReferralAffiliate{},
		&model.ReferralBinding{},
		&model.ReferralCommission{},
		&model.ReferralCommissionJob{},
		&model.AdtySiteInviteCommission{},
		&model.AdtyGiftFulfillment{},
		&model.Log{},
	))

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedis := common.RedisEnabled
	oldMem := common.MemoryCacheEnabled
	oldPrice := operation_setting.GetPrice()
	oldMinTopUp := operation_setting.GetMinTopUp()
	oldEpayHTTP := adtyEpayHTTPClient
	oldBEpusdtHTTP := bepusdtHTTPClient
	oldEpayNow := adtyEpayNow
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	model.DB = db
	model.LOG_DB = db
	operation_setting.SetPrice(7.0)
	operation_setting.SetMinTopUp(1)

	scenes := []model.AdtyPaymentScene{
		{
			Key: "team_topup", Name: "Team", Business: model.AdtyBusinessTeamTopup, Enabled: true,
			Providers: []string{model.PaymentProviderAdtyBEpusdt, model.PaymentProviderAdtyEpay},
			Methods:   []string{"alipay", "usdt.trc20"}, SiteScope: "all",
			MinUSD: decimal.NewFromInt(1), MaxUSD: decimal.NewFromInt(10000000),
		},
		{
			Key: "wallet_topup", Name: "Wallet", Business: model.AdtyBusinessWalletTopup, Enabled: true,
			Providers: []string{model.PaymentProviderAdtyBEpusdt, model.PaymentProviderAdtyEpay},
			Methods:   []string{"alipay", "usdt.trc20"}, SiteScope: "all",
			MinUSD: decimal.NewFromInt(1), MaxUSD: decimal.NewFromInt(10000000),
		},
	}
	scenesBytes, err := common.Marshal(scenes)
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.AdtyPaymentSceneConfig{
		ID:      1,
		Version: 1,
		Payload: string(scenesBytes),
	}).Error)

	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMem
		operation_setting.SetPrice(oldPrice)
		operation_setting.SetMinTopUp(oldMinTopUp)
		adtyEpayHTTPClient = oldEpayHTTP
		bepusdtHTTPClient = oldBEpusdtHTTP
		adtyEpayNow = oldEpayNow
		_ = sqlDB.Close()
	})
	return db
}

func TestAdtyTeamQuoteEnforcesMaxWalletQuota(t *testing.T) {
	setupTeamPayLifecycleTestDB(t)
	nearFullTeam := &model.AdtyTeam{
		Id:          1,
		SharedQuota: int64(common.MaxWalletQuota) - common.QuotaPerUnitDecimal().IntPart()*5,
	}
	_, err := adtyTeamQuote(nearFullTeam, "default", 5, "usdt.trc20")
	require.NoError(t, err)

	_, err = adtyTeamQuote(nearFullTeam, "default", 6, "usdt.trc20")
	assert.ErrorIs(t, err, model.ErrTopUpQuotaLimitExceeded)
}

func TestAdtyUSDTOrderTimeoutBoundsAndTickRace(t *testing.T) {
	setupTeamPayLifecycleTestDB(t)

	setServiceTestUSDTOrderMinutes(t, 1)
	assert.Equal(t, int64(180), adtyUSDTOrderTimeoutSeconds())

	setServiceTestUSDTOrderMinutes(t, 120)
	assert.Equal(t, int64(3600), adtyUSDTOrderTimeoutSeconds())

	setServiceTestUSDTOrderMinutes(t, 0)
	assert.Equal(t, int64(bepusdtTimeoutSeconds), adtyUSDTOrderTimeoutSeconds())

	var seenTimeout int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if v, ok := req["timeout"].(float64); ok {
			seenTimeout = int(v)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status_code": 200,
			"message":     "success",
			"data": map[string]any{
				"order_id":        req["order_id"],
				"trade_type":      "usdt.trc20",
				"fiat":            "USD",
				"amount":          10,
				"trade_id":        "BEP_TICK_1",
				"actual_amount":   "10.0009",
				"token":           "TTickAddr",
				"expiration_time": 180,
				"payment_url":     "https://bep.example.com/p/1",
			},
		})
	}))
	defer srv.Close()

	// Include leading/trailing whitespace in BaseURL to verify TrimSpace handling.
	setServiceTestBEpusdt(t, true, "  "+srv.URL+"/  ", "secret", []string{"usdt.trc20"})

	ext := &model.AdtyTopupOrder{
		TradeNo:         "TTU_TICK_1",
		Provider:        model.PaymentProviderAdtyBEpusdt,
		Method:          "usdt.trc20",
		PayAmount:       decimal.NewFromInt(10),
		ExpireAt:        time.Now().Unix() + 179, // 1 second ticked after 180s creation
		CheckoutGateway: srv.URL,
	}

	// On initial checkout (replay=false), remaining=179 bumps to 180 instead of failing.
	fields, err := recoverTeamCheckout(context.Background(), ext, nil, "127.0.0.1", "pc", false)
	require.NoError(t, err)
	assert.Equal(t, 180, seenTimeout)
	assert.Equal(t, "TTickAddr", fields["pay_address"])

	// On replay (replay=true), remaining < 180 fails with Order expired.
	_, err = recoverTeamCheckout(context.Background(), ext, nil, "127.0.0.1", "pc", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Order expired")
}

func TestRecoverTeamCheckoutOmittedPIDAndReturnTypeUpdate(t *testing.T) {
	db := setupTeamPayLifecycleTestDB(t)

	var seenDevice string
	var queryPID string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php":
			resp := map[string]any{
				"code":         1,
				"status":       0,
				"out_trade_no": r.URL.Query().Get("out_trade_no"),
				"trade_no":     "GW_Q_1",
				"type":         "alipay",
				"money":        "70.00",
			}
			if queryPID != "" {
				resp["pid"] = queryPID
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "/mapi.php":
			_ = r.ParseForm()
			seenDevice = r.FormValue("device")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":     1,
				"trade_no": "GW_MAPI_1",
				"qrcode":   "https://qr.alipay.com/test123",
			})
		}
	}))
	defer srv.Close()

	// Point query transport to test server's TLS client via custom http client for test.
	// Note: QueryAdtyEpayOrder enforces verified TLS via cloneAdtyVerifiedPaymentTransport,
	// so inject the test server's RootCA into adtyEpayHTTPClient.
	adtyEpayHTTPClient = func() *http.Client { return srv.Client() }

	ch := &model.AdtyEpayChannel{
		Name:        "TeamEpay",
		GatewayURL:  srv.URL,
		MerchantId:  "3001",
		MerchantKey: "epay-key",
		SignType:    model.AdtyEpaySignTypeMD5,
		OrderMode:   model.AdtyEpayOrderModeAPI,
		Status:      model.AdtyEpayStatusEnabled,
	}
	require.NoError(t, db.Create(ch).Error)
	cm := &model.AdtyEpayChannelMethod{
		ChannelId: ch.Id,
		Method:    "alipay",
		TypeCode:  "alipay",
		Enabled:   true,
	}
	require.NoError(t, db.Create(cm).Error)
	ch.Methods = []model.AdtyEpayChannelMethod{*cm}

	ext := &model.AdtyTopupOrder{
		TradeNo:   "TTEP_PID_1",
		Provider:  model.PaymentProviderAdtyEpay,
		Method:    "alipay",
		PayAmount: decimal.NewFromInt(70),
		ChannelId: ch.Id,
	}

	// Non-replay API call updates method.ReturnType to "qrcode" and forwards device="mobile".
	fields, err := recoverTeamCheckout(context.Background(), ext, ch, "127.0.0.1", "mobile", false)
	require.NoError(t, err)
	assert.Equal(t, "mobile", seenDevice)
	assert.Equal(t, "https://qr.alipay.com/test123", fields["qr_code"])

	var refreshedMethod model.AdtyEpayChannelMethod
	require.NoError(t, db.First(&refreshedMethod, cm.Id).Error)
	assert.Equal(t, "qrcode", refreshedMethod.ReturnType)

	// Replay query with omitted pid succeeds (falls back to signed submit checkout).
	queryPID = ""
	chCopy := *ch
	chCopy.OrderMode = model.AdtyEpayOrderModeAPI
	fields, err = recoverTeamCheckout(context.Background(), ext, &chCopy, "127.0.0.1", "pc", true)
	require.NoError(t, err)
	assert.Contains(t, fields["pay_url"], "/submit.php?")
	assert.Equal(t, "GW_Q_1", fields["gateway_trade_id"])

	// Replay query with mismatched non-empty pid still rejects with ErrPaymentMethodMismatch.
	queryPID = "9999"
	chCopy2 := *ch
	chCopy2.OrderMode = model.AdtyEpayOrderModeAPI
	_, err = recoverTeamCheckout(context.Background(), ext, &chCopy2, "127.0.0.1", "pc", true)
	assert.ErrorIs(t, err, model.ErrPaymentMethodMismatch)
}

func TestTeamTopupLifecycleReuseCapFailCleanupResumeExpiryAndRSARefresh(t *testing.T) {
	db := setupTeamPayLifecycleTestDB(t)
	now := time.Now().Unix()

	require.NoError(t, db.Create(&model.User{
		Id:       101,
		Username: "team_leader",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		SiteId:   0,
	}).Error)
	team := &model.AdtyTeam{
		Id:           10,
		SiteId:       0,
		LeaderUserId: 101,
		Name:         "Alpha Team",
		Discount:     decimal.NewFromInt(1),
		Status:       model.AdtyTeamStatusActive,
	}
	require.NoError(t, db.Create(team).Error)
	require.NoError(t, db.Create(&model.AdtyTeamMember{
		TeamId: 10,
		UserId: 101,
		Role:   model.AdtyTeamRoleLeader,
		Status: model.AdtyTeamMemberActive,
	}).Error)

	var failGateway atomic.Bool
	var bepCreateCalls atomic.Int32
	bepSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/order/create-transaction" {
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "message": "ok"})
			return
		}
		bepCreateCalls.Add(1)
		if failGateway.Load() {
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 500, "message": "upstream down"})
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		orderID, _ := req["order_id"].(string)
		amt, _ := req["amount"].(float64)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status_code": 200,
			"message":     "success",
			"data": map[string]any{
				"order_id":        orderID,
				"trade_type":      "usdt.trc20",
				"fiat":            "USD",
				"amount":          amt,
				"trade_id":        "GW_" + orderID,
				"actual_amount":   fmt.Sprintf("%.4f", amt+0.0023),
				"token":           "TTeamAddr",
				"expiration_time": 900,
				"payment_url":     "https://bep.example.com/pay/" + orderID,
			},
		})
	}))
	defer bepSrv.Close()

	setServiceTestBEpusdt(t, true, " "+bepSrv.URL+" ", "bep-sec", []string{"usdt.trc20"})

	// 1. Wrong requestSiteID is forbidden.
	_, err := StartAdtyTeamTopupPaymentWithOptions(AdtyTeamTopupStartOptions{
		LeaderID:      101,
		TeamID:        10,
		RequestSiteID: 99,
		AmountUSD:     decimal.NewFromInt(10),
		Method:        "usdt.trc20",
		Key:           "site-mismatch-key",
	})
	assert.ErrorIs(t, err, ErrAdtyTeamForbidden)

	// 2. Initial gateway failure marks local order failed (not stuck pending).
	failGateway.Store(true)
	_, err = StartAdtyTeamTopupPaymentWithOptions(AdtyTeamTopupStartOptions{
		LeaderID:  101,
		TeamID:    10,
		AmountUSD: decimal.NewFromInt(10),
		Method:    "usdt.trc20",
		Key:       "fail-first-key",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAdtyPaymentGatewayUnavailable)
	var failedTop model.TopUp
	require.NoError(t, db.Where("trade_no = ?", adtyTeamTopupTradeNo("TTU", 10, "fail-first-key")).First(&failedTop).Error)
	assert.Equal(t, common.TopUpStatusFailed, failedTop.Status)

	// Resuming a failed order returns clear terminal error.
	_, err = ResumeAdtyTeamTopupPayment(101, 10, failedTop.TradeNo, "127.0.0.1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed")

	// 3. Successful BEpusdt team checkout updates top_ups.money to ActualAmount and trims CheckoutGateway.
	failGateway.Store(false)
	created, err := StartAdtyTeamTopupPaymentWithOptions(AdtyTeamTopupStartOptions{
		LeaderID:  101,
		TeamID:    10,
		AmountUSD: decimal.NewFromInt(10),
		Method:    "usdt.trc20",
		Key:       "ok-key-1",
	})
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, "10.0023", created.PayAmount.StringFixed(4))
	assert.Equal(t, bepSrv.URL, created.CheckoutGateway)
	var createdTop model.TopUp
	require.NoError(t, db.Where("trade_no = ?", created.TradeNo).First(&createdTop).Error)
	assert.InDelta(t, 10.0023, createdTop.Money, 0.00001)

	// 4. Different idempotency key with same amount+method reuses existing ready pending order without calling gateway again.
	callsBefore := bepCreateCalls.Load()
	reused, err := StartAdtyTeamTopupPaymentWithOptions(AdtyTeamTopupStartOptions{
		LeaderID:  101,
		TeamID:    10,
		AmountUSD: decimal.NewFromInt(10),
		Method:    "usdt.trc20",
		Key:       "ok-key-2-reuse",
	})
	require.NoError(t, err)
	assert.Equal(t, created.TradeNo, reused.TradeNo)
	assert.Equal(t, callsBefore, bepCreateCalls.Load())

	// 5. Resuming expired BEpusdt order fails with "Order expired".
	require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("trade_no = ?", created.TradeNo).Update("expire_at", now-5).Error)
	_, err = ResumeAdtyTeamTopupPayment(101, 10, created.TradeNo, "127.0.0.1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Order expired")

	// 6. Active pending cap (10 per team) enforced, while expired pending order does not count.
	for i := 0; i < 10; i++ {
		tr := fmt.Sprintf("TTU_CAP_%d", i)
		require.NoError(t, db.Create(&model.TopUp{
			UserId: 101, Amount: int64(20 + i), Money: float64(20 + i), TradeNo: tr,
			PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt,
			CreateTime: now, Status: common.TopUpStatusPending,
		}).Error)
		require.NoError(t, db.Create(&model.AdtyTopupOrder{
			Business: model.AdtyBusinessTeamTopup, TradeNo: tr, UserId: 101, TeamId: 10,
			Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
			FaceUSD: decimal.NewFromInt(int64(20 + i)), PayAmount: decimal.NewFromInt(int64(20 + i)),
			PayCurrency: "USDT", PayAddress: "TAddr", GatewayTradeId: "GW" + tr,
			ExpireAt: now + 900, CheckoutStage: "ready",
		}).Error)
	}
	_, err = StartAdtyTeamTopupPaymentWithOptions(AdtyTeamTopupStartOptions{
		LeaderID:  101,
		TeamID:    10,
		AmountUSD: decimal.NewFromInt(99),
		Method:    "usdt.trc20",
		Key:       "over-cap-key",
	})
	assert.ErrorIs(t, err, model.ErrAdtyTooManyPendingOrders)

	// 7. ResumeAdtyTeamTopupPayment refreshes stale (>120s) RSA submit-mode PayURL.
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privKey)}))
	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	require.NoError(t, err)
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))

	rsaCh := &model.AdtyEpayChannel{
		Name:               "RSA-Submit",
		GatewayURL:         "https://rsa.example.com",
		MerchantId:         "5001",
		SignType:           model.AdtyEpaySignTypeRSA,
		MerchantPrivateKey: privPEM,
		PlatformPublicKey:  pubPEM,
		OrderMode:          model.AdtyEpayOrderModeSubmit,
		Status:             model.AdtyEpayStatusEnabled,
	}
	require.NoError(t, db.Create(rsaCh).Error)
	rsaMethod := &model.AdtyEpayChannelMethod{
		ChannelId: rsaCh.Id,
		Method:    "alipay",
		TypeCode:  "alipay",
		Enabled:   true,
	}
	require.NoError(t, db.Create(rsaMethod).Error)

	staleTS := time.Now().Unix() - 240
	stalePayURL := fmt.Sprintf("https://rsa.example.com/api/pay/submit?pid=5001&out_trade_no=TTEP_RSA_STALE&timestamp=%d&sign=old&sign_type=RSA", staleTS)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: 101, Amount: 15, Money: 105, TradeNo: "TTEP_RSA_STALE",
		PaymentMethod: "alipay", PaymentProvider: model.PaymentProviderAdtyEpay,
		CreateTime: now, Status: common.TopUpStatusPending,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		Business: model.AdtyBusinessTeamTopup, TradeNo: "TTEP_RSA_STALE", UserId: 101, TeamId: 10,
		Provider: model.PaymentProviderAdtyEpay, Method: "alipay", ChannelId: rsaCh.Id,
		FaceUSD: decimal.NewFromInt(15), PayAmount: decimal.NewFromInt(105),
		PayCurrency: "CNY", PayURL: stalePayURL, ExpireAt: now + 600, CheckoutStage: "ready",
	}).Error)

	refreshed, err := ResumeAdtyTeamTopupPayment(101, 10, "TTEP_RSA_STALE", "127.0.0.1", "pc")
	require.NoError(t, err)
	assert.NotEqual(t, stalePayURL, refreshed.PayURL)
	parsed, err := url.Parse(refreshed.PayURL)
	require.NoError(t, err)
	newTS, err := strconv.ParseInt(parsed.Query().Get("timestamp"), 10, 64)
	require.NoError(t, err)
	assert.Greater(t, newTS, staleTS+120)

	persisted, err := model.GetAdtyTopupOrder("TTEP_RSA_STALE")
	require.NoError(t, err)
	assert.Equal(t, refreshed.PayURL, persisted.PayURL)
}

func TestAdtyPayResultTargetRoutesTeamTopupToMyTeam(t *testing.T) {
	db := setupTeamPayLifecycleTestDB(t)
	now := time.Now().Unix()

	require.NoError(t, db.Create(&model.User{
		Id:       201,
		Username: "team_main_leader",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		SiteId:   0,
	}).Error)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: 201, Amount: 10, Money: 70, TradeNo: "TTEP_RET_MAIN",
		PaymentMethod: "alipay", PaymentProvider: model.PaymentProviderAdtyEpay,
		CreateTime: now, Status: common.TopUpStatusPending,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		Business: model.AdtyBusinessTeamTopup, TradeNo: "TTEP_RET_MAIN", UserId: 201, TeamId: 55,
		Provider: model.PaymentProviderAdtyEpay, Method: "alipay",
	}).Error)

	returnURL := AdtyPayResultURL("TTEP_RET_MAIN")
	idx := strings.LastIndex(returnURL, adtyPayResultPath)
	require.Greater(t, idx, -1)
	token := returnURL[idx+len(adtyPayResultPath):]
	target := AdtyPayResultTarget(token)
	assert.True(t, strings.HasSuffix(target, "/my-team?trade_no=TTEP_RET_MAIN"), "unexpected team pay result target: %s", target)
}
