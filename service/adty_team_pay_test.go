package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"context"
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestTeamTopupStartsEpayWithoutGift(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	previousPrice := operation_setting.Price
	operation_setting.Price = 7.2
	t.Cleanup(func() { operation_setting.Price = previousPrice })
	leader := referralUser(t, db, "team-epay-leader")
	created, err := CreateAdtyTeam(leader.Id, 0, "Pay", decimal.NewFromInt(1))
	require.NoError(t, err)
	seedEpayChannel(t, db, model.AdtyEpayChannel{
		Name: "team-pay", GatewayURL: "https://pay.example.com", MerchantId: "m1",
		OrderMode: model.AdtyEpayOrderModeSubmit,
	}, alipay("submit"))

	order, err := StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "alipay", 0, "team-epay-1", "127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, created.Team.Id, order.TeamId)
	require.True(t, order.BonusUSD.IsZero())
	require.True(t, order.BonusRate.IsZero())
	require.EqualValues(t, 500000, order.CreditQuota)
	require.Equal(t, "CNY", order.PayCurrency)
	require.True(t, order.PayAmount.Equal(decimal.RequireFromString("7.20")))
	require.Contains(t, order.PayURL, "https://pay.example.com/submit.php")
	require.Equal(t, model.PaymentProviderAdtyEpay, order.Provider)
	require.Equal(t, "alipay", order.Method)
	require.Greater(t, order.ExpireAt, order.CreatedAt)

	replay, err := StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "alipay", 0, "team-epay-1", "127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, order.TradeNo, replay.TradeNo)
}

// 负责人选了哪个易支付通道就只用哪个：这个通道下不了单就提示换一个支付方式，不会换到别的通道；
// 订单记下单时的显示名称。
func TestTeamTopupUsesOnlyTheChosenEpayChannel(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	previousPrice := operation_setting.Price
	operation_setting.Price = 7.2
	t.Cleanup(func() { operation_setting.Price = previousPrice })
	leader := referralUser(t, db, "team-chosen-leader")
	created, err := CreateAdtyTeam(leader.Id, 0, "Chosen", decimal.NewFromInt(1))
	require.NoError(t, err)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":-1,"msg":"busy"}`))
	}))
	t.Cleanup(broken.Close)
	brokenChannel := seedEpayChannel(t, db, model.AdtyEpayChannel{
		Name: "team-broken", GatewayURL: broken.URL, MerchantId: "m-broken", Priority: 9,
	}, alipay("qrcode"))
	chosen := seedEpayChannel(t, db, model.AdtyEpayChannel{
		Name: "team-chosen", GatewayURL: "https://pay.example.com", MerchantId: "m-chosen",
		OrderMode: model.AdtyEpayOrderModeSubmit, Priority: 1,
	}, alipay("submit"))
	require.NoError(t, db.Model(&model.AdtyEpayChannelMethod{}).Where("channel_id = ?", chosen.Id).Update("display_name", "支付宝2").Error)

	_, err = StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "alipay", brokenChannel.Id, "team-chosen-1", "127.0.0.1")
	require.ErrorIs(t, err, ErrAdtyEpayChosenChannelUnavailable)
	var orders []model.AdtyTopupOrder
	require.NoError(t, db.Where("team_id = ?", created.Team.Id).Find(&orders).Error)
	for _, order := range orders {
		require.Equal(t, brokenChannel.Id, order.ChannelId, "a chosen channel never falls back to another one")
	}

	order, err := StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "alipay", chosen.Id, "team-chosen-2", "127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, chosen.Id, order.ChannelId)
	require.Equal(t, "支付宝2", order.MethodLabel)
}

func TestTeamTopupStartsExistingBEpusdtWithoutGift(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "data": map[string]any{
			"trade_id": "tid-team", "order_id": req["order_id"], "trade_type": req["trade_type"], "fiat": "USD", "amount": req["amount"],
			"actual_amount": "1.01", "token": "TAddr", "expiration_time": 900, "status": 1, "payment_url": "https://pay.example/usdt",
		}})
	}))
	t.Cleanup(server.Close)
	cfg := setting.GetBEpusdtSetting()
	saved := *cfg
	cfg.Enabled, cfg.BaseURL, cfg.Secret, cfg.TradeTypes = true, server.URL, "team-bepusdt-secret", []string{"usdt.trc20"}
	t.Cleanup(func() { *cfg = saved })

	leader := referralUser(t, db, "team-usdt-leader")
	created, err := CreateAdtyTeam(leader.Id, 0, "USDT", decimal.NewFromInt(1))
	require.NoError(t, err)
	order, err := StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "usdt.trc20", 0, "team-usdt-1", "127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, created.Team.Id, order.TeamId)
	require.Equal(t, model.PaymentProviderAdtyBEpusdt, order.Provider)
	require.Equal(t, "usdt.trc20", order.Method)
	require.True(t, order.BonusUSD.IsZero())
	require.True(t, order.BonusRate.IsZero())
	require.EqualValues(t, 500000, order.CreditQuota)
	require.Equal(t, "TAddr", order.PayAddress)
	require.Equal(t, "https://pay.example/usdt", order.PayURL)

	replay, err := StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "usdt.trc20", 0, "team-usdt-1", "127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, order.TradeNo, replay.TradeNo)
}

func TestTeamTopupSubsiteNoGiftQuoteIgnoresPersonalGiftCoverage(t *testing.T) {
	_, err := model.OfficialPostgresAcceptanceDSN()
	require.NoError(t, err)
	db := prepareSiteDB(t)
	previousPrice := operation_setting.Price
	operation_setting.Price = 7.2
	t.Cleanup(func() { operation_setting.Price = previousPrice })

	_, site := openActiveSite(t, db, "team-subsite")
	leader := siteCustomer(t, db, site, "team-subsite-leader")

	created, err := CreateAdtyTeam(leader.Id, site.Id, "SubsitePay", decimal.NewFromInt(1))
	require.NoError(t, err)

	// Configure a subsite personal payment method gift rule whose bonus exceeds subsite margin (0%)
	// and has margin fill disabled, so personal quote fails with SiteBonusEarningCovers error.
	topupCfg := adty_setting.GetTopupSetting()
	savedAuth := topupCfg.SiteMethodAuth
	savedBonus := topupCfg.SiteMethodBonus
	savedFill := topupCfg.SiteBonusMarginFill
	topupCfg.SiteMethodAuth = map[int][]string{site.Id: {"alipay"}}
	topupCfg.SiteMethodBonus = map[int]map[string]adty_setting.MethodBonusRule{
		site.Id: {
			"alipay": {Enabled: true, Rate: "0.20"},
		},
	}
	topupCfg.SiteBonusMarginFill = map[int]bool{site.Id: false}
	t.Cleanup(func() {
		topupCfg.SiteMethodAuth = savedAuth
		topupCfg.SiteMethodBonus = savedBonus
		topupCfg.SiteBonusMarginFill = savedFill
	})

	_, err = AdtyQuoteForSite(site, "default", 1, "alipay")
	require.EqualError(t, err, "Site gift reserve is not covered by the earning wallet")

	seedEpayChannel(t, db, model.AdtyEpayChannel{
		Name: "team-subsite-epay", GatewayURL: "https://pay.example.com", MerchantId: "m-sub",
		OrderMode: model.AdtyEpayOrderModeSubmit,
	}, alipay("submit"))

	order, err := StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "alipay", 0, "team-subsite-nogift-1", "127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, created.Team.Id, order.TeamId)
	require.True(t, order.BonusUSD.IsZero())
	require.True(t, order.BonusRate.IsZero())
	require.EqualValues(t, 500000, order.CreditQuota)
}

func TestTeamTopupEndpointIdempotencyAcrossProviders(t *testing.T) {
	db := prepareReferralDB(t)
	previousPrice := operation_setting.Price
	operation_setting.Price = 7.2
	t.Cleanup(func() { operation_setting.Price = previousPrice })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status_code":200,"message":"success","data":{"trade_id":"tid-team-cross","order_id":"team","amount":"1","actual_amount":"1.01","token":"TAddr","expiration_time":900,"status":1,"payment_url":"https://pay.example/usdt"}}`))
	}))
	t.Cleanup(server.Close)
	cfg := setting.GetBEpusdtSetting()
	saved := *cfg
	cfg.Enabled, cfg.BaseURL, cfg.Secret, cfg.TradeTypes = true, server.URL, "team-bepusdt-secret", []string{"usdt.trc20"}
	t.Cleanup(func() { *cfg = saved })

	seedEpayChannel(t, db, model.AdtyEpayChannel{
		Name: "team-cross-epay", GatewayURL: "https://pay.example.com", MerchantId: "m-cross",
		OrderMode: model.AdtyEpayOrderModeSubmit,
	}, alipay("submit"))

	leader := referralUser(t, db, "team-cross-leader")
	created, err := CreateAdtyTeam(leader.Id, 0, "CrossIdem", decimal.NewFromInt(1))
	require.NoError(t, err)

	// 1. Start Epay order with idempotency key "shared-key-1"
	epayOrder, err := StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "alipay", 0, "shared-key-1", "127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, model.PaymentProviderAdtyEpay, epayOrder.Provider)

	// 2. Replaying same key with BEpusdt must fail with ErrAdtyTeamInvalid (not create a second order)
	_, err = StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "usdt.trc20", 0, "shared-key-1", "127.0.0.1")
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)

	// 3. Replaying same key with CreateAdtyTeamTopupOrder must also fail with ErrAdtyTeamInvalid
	_, err = CreateAdtyTeamTopupOrder(leader.Id, created.Team.Id, decimal.NewFromInt(1), "shared-key-1")
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)

	var count int64
	require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("team_id = ?", created.Team.Id).Count(&count).Error)
	require.EqualValues(t, 1, count, "only one team topup order may exist for the shared idempotency key")
}

func TestTeamTopupRejectsWithinProviderMethodChange(t *testing.T) {
	db := prepareReferralDB(t)
	previousPrice := operation_setting.Price
	operation_setting.Price = 7.2
	t.Cleanup(func() { operation_setting.Price = previousPrice })
	leader := referralUser(t, db, "team-method-leader")
	created, err := CreateAdtyTeam(leader.Id, 0, "Method", decimal.NewFromInt(1))
	require.NoError(t, err)
	seedEpayChannel(t, db, model.AdtyEpayChannel{
		Name: "team-methods", GatewayURL: "https://pay.example.com", MerchantId: "m-methods",
		OrderMode: model.AdtyEpayOrderModeSubmit,
	}, alipay("submit"), model.AdtyEpayChannelMethod{Method: "wxpay", Enabled: true, ReturnType: "submit"})

	order, err := StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "alipay", 0, "method-key-1", "127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, "alipay", order.Method)

	_, err = StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "wxpay", 0, "method-key-1", "127.0.0.1")
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)

	var count int64
	require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("team_id = ?", created.Team.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestTeamTopupBlankMethodDoesNotOpenASecondOrder(t *testing.T) {
	db := prepareReferralDB(t)
	previousPrice := operation_setting.Price
	operation_setting.Price = 7.2
	t.Cleanup(func() { operation_setting.Price = previousPrice })
	leader := referralUser(t, db, "team-blank-leader")
	created, err := CreateAdtyTeam(leader.Id, 0, "Blank", decimal.NewFromInt(1))
	require.NoError(t, err)
	seedEpayChannel(t, db, model.AdtyEpayChannel{
		Name: "team-blank-epay", GatewayURL: "https://pay.example.com", MerchantId: "m-blank",
		OrderMode: model.AdtyEpayOrderModeSubmit,
	}, alipay("submit"))

	_, err = CreateAdtyTeamTopupOrder(leader.Id, created.Team.Id, decimal.NewFromInt(1), "blank-key-1")
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)
	// A historically recorded channel-less order still owns its key.
	blank := model.AdtyTopupOrder{TradeNo: adtyTeamTopupTradeNo("TTEAM", created.Team.Id, "blank-key-1"),
		TeamId: created.Team.Id, UserId: leader.Id, Provider: model.PaymentProviderAdtyEpay, Method: "team", FaceUSD: decimal.NewFromInt(1)}
	require.NoError(t, db.Create(&blank).Error)
	_, err = StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "alipay", 0, "blank-key-1", "127.0.0.1")
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)

	_, err = StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "", 0, "blank-empty", "127.0.0.1")
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)

	epayOrder, err := StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "alipay", 0, "blank-key-2", "127.0.0.1")
	require.NoError(t, err)
	_, err = CreateAdtyTeamTopupOrder(leader.Id, created.Team.Id, decimal.NewFromInt(1), "blank-key-2")
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)
	_, err = StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), " ", 0, "blank-key-2", "127.0.0.1")
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)

	var count int64
	require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("team_id = ?", created.Team.Id).Count(&count).Error)
	require.EqualValues(t, 2, count)
	require.NotEqual(t, blank.TradeNo, epayOrder.TradeNo)
}

func TestTeamTopupIdempotencyRejectsChannelPinChange(t *testing.T) {
	db := prepareReferralDB(t)
	previousPrice := operation_setting.Price
	operation_setting.Price = 7.2
	t.Cleanup(func() { operation_setting.Price = previousPrice })
	leader := referralUser(t, db, "team-pin-leader")
	created, err := CreateAdtyTeam(leader.Id, 0, "Pin", decimal.NewFromInt(1))
	require.NoError(t, err)
	high := seedEpayChannel(t, db, model.AdtyEpayChannel{
		Name: "team-pin-high", GatewayURL: "https://pay.example.com", MerchantId: "m-high", Priority: 9,
		OrderMode: model.AdtyEpayOrderModeSubmit,
	}, alipay("submit"))
	low := seedEpayChannel(t, db, model.AdtyEpayChannel{
		Name: "team-pin-low", GatewayURL: "https://pay.example.com", MerchantId: "m-low", Priority: 1,
		OrderMode: model.AdtyEpayOrderModeSubmit,
	}, alipay("submit"))

	auto, err := StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "alipay", 0, "pin-key-1", "127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, high.Id, auto.ChannelId)
	_, err = StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "alipay", low.Id, "pin-key-1", "127.0.0.1")
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)
	replay, err := StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "alipay", 0, "pin-key-1", "127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, auto.TradeNo, replay.TradeNo)

	pinned, err := StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "alipay", low.Id, "pin-key-2", "127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, low.Id, pinned.ChannelId)
	_, err = StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "alipay", high.Id, "pin-key-2", "127.0.0.1")
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)
	returned, err := StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "alipay", 0, "pin-key-2", "127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, pinned.TradeNo, returned.TradeNo)

	var count int64
	require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("team_id = ?", created.Team.Id).Count(&count).Error)
	require.EqualValues(t, 2, count)
}

func TestTeamTopupConcurrentSameKeyReturnsOneOrder(t *testing.T) {
	db := prepareReferralDB(t)
	previousPrice := operation_setting.Price
	operation_setting.Price = 7.2
	t.Cleanup(func() { operation_setting.Price = previousPrice })
	leader := referralUser(t, db, "team-conc-leader")
	created, err := CreateAdtyTeam(leader.Id, 0, "Conc", decimal.NewFromInt(1))
	require.NoError(t, err)
	seedEpayChannel(t, db, model.AdtyEpayChannel{
		Name: "team-conc-epay", GatewayURL: "https://pay.example.com", MerchantId: "m-conc",
		OrderMode: model.AdtyEpayOrderModeSubmit,
	}, alipay("submit"))

	var wg sync.WaitGroup
	type call struct {
		order *model.AdtyTopupOrder
		err   error
	}
	results := make([]call, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			order, err := StartAdtyTeamTopupPayment(leader.Id, created.Team.Id, decimal.NewFromInt(1), "alipay", 0, "conc-key-1", "127.0.0.1")
			results[i] = call{order, err}
		}(i)
	}
	wg.Wait()

	var count int64
	require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("team_id = ?", created.Team.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)
	tradeNo := ""
	for _, result := range results {
		if result.err != nil {
			require.ErrorIs(t, result.err, ErrAdtyTeamInvalid)
			continue
		}
		require.NotNil(t, result.order)
		if tradeNo == "" {
			tradeNo = result.order.TradeNo
		}
		require.Equal(t, tradeNo, result.order.TradeNo)
	}
	require.NotEmpty(t, tradeNo)
}

func TestTeamUnicodeNameLength(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	leader := referralUser(t, db, "team-name")
	name := strings.Repeat("组", 64)
	team, err := CreateAdtyTeam(leader.Id, 0, name, decimal.NewFromInt(1))
	require.NoError(t, err)
	require.NoError(t, UpdateAdtyTeamProfile(leader.Id, team.Team.Id, strings.Repeat("𠮷", 64)))
	require.Error(t, UpdateAdtyTeamProfile(leader.Id, team.Team.Id, name+"a"))
}

func TestTeamQuotaReturnsAfterTemporaryExhaustion(t *testing.T) {
	db := prepareReferralDB(t)
	leader := referralUser(t, db, "team-key-quota")
	team, err := CreateAdtyTeam(leader.Id, 0, "Team", decimal.NewFromInt(1))
	require.NoError(t, err)
	require.NoError(t, CreditAdtyTeam(int64(leader.Id), team.Team.Id, 100, "key-test"))
	token := model.Token{UserId: leader.Id, Key: "team-exhaustion", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 50}
	require.NoError(t, token.InsertWithLimit(10))
	require.NoError(t, BindAdtyTeamKey(leader.Id, token.Id, team.Team.Id))
	_, err = model.ReserveBillingOperation(model.BillingOperation{OperationKey: "request:key-exhaustion", TokenId: token.Id, UserId: leader.Id}, 50)
	require.NoError(t, err)
	_, err = model.ValidateUserToken(token.Key)
	require.ErrorIs(t, err, model.ErrTokenInvalid)
	require.NoError(t, model.QueueBillingSettlement("request:key-exhaustion", 20, false))
	_, _, err = model.ApplyBillingSettlement("request:key-exhaustion")
	require.NoError(t, err)
	validated, err := model.ValidateUserToken(token.Key)
	require.NoError(t, err)
	require.Equal(t, 30, validated.RemainQuota)
	require.NoError(t, db.Model(&token).Update("status", common.TokenStatusDisabled).Error)
	_, err = model.ValidateUserToken(token.Key)
	require.ErrorIs(t, err, model.ErrTokenInvalid)
}

func TestTeamScopedSceneAndHistoricalReplay(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	leader := referralUser(t, db, "team-scene")
	team, err := CreateAdtyTeam(leader.Id, 0, "Policy", decimal.NewFromInt(1))
	require.NoError(t, err)
	seedEpayChannel(t, db, model.AdtyEpayChannel{Name: "scope", GatewayURL: "https://pay.example.com", MerchantId: "m", OrderMode: model.AdtyEpayOrderModeSubmit}, alipay("submit"))
	amount := decimal.NewFromInt(2)
	previous, err := StartAdtyTeamTopupPayment(leader.Id, team.Team.Id, amount, "alipay", 0, "old", "")
	require.NoError(t, err)
	require.Equal(t, "wallet_topup", previous.PaymentScene)
	config, err := GetAdtyPaymentScenes()
	require.NoError(t, err)
	custom := config.Scenes[0]
	custom.Business, custom.Key, custom.Name, custom.MaxUSD = model.AdtyBusinessTeamTopup, "team_custom", "Team custom", decimal.NewFromInt(1)
	custom.TeamIDs = []int64{team.Team.Id}
	scenes := append(config.Scenes, custom)
	bytes, err := json.Marshal(scenes)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.AdtyPaymentSceneConfig{}).Where("id=1").Update("payload", string(bytes)).Error)
	_, err = StartAdtyTeamTopupPayment(leader.Id, team.Team.Id, amount, "alipay", 0, "new", "", "team_custom")
	require.Error(t, err)
	_, err = StartAdtyTeamTopupPayment(leader.Id, team.Team.Id, amount, "alipay", 0, "bypass", "", "wallet_topup")
	require.Error(t, err)
	order, err := StartAdtyTeamTopupPayment(leader.Id, team.Team.Id, decimal.NewFromInt(1), "alipay", 0, "new-valid", "", "team_custom")
	require.NoError(t, err)
	require.Equal(t, "team_custom", order.PaymentScene)
	require.Contains(t, order.PaymentSceneSnapshot, "team_custom")
	replay, err := StartAdtyTeamTopupPayment(leader.Id, team.Team.Id, amount, "alipay", 0, "old", "", "wallet_topup")
	require.NoError(t, err)
	require.Equal(t, previous.TradeNo, replay.TradeNo)
	personalPending, err := model.ListUserPendingAdtyTradeNos(leader.Id)
	require.NoError(t, err)
	require.Empty(t, personalPending, "opening a personal payment cannot replace a pending team order")
	outsider := referralUser(t, db, "team-outsider")
	otherTeam, err := CreateAdtyTeam(outsider.Id, 0, "Other", decimal.NewFromInt(1))
	require.NoError(t, err)
	other, err := StartAdtyTeamTopupPayment(outsider.Id, otherTeam.Team.Id, amount, "alipay", 0, "other-team", "", "wallet_topup")
	require.NoError(t, err)
	require.Equal(t, "wallet_topup", other.PaymentScene)

}

func TestTeamHistoricalEpayQuery(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	leader := referralUser(t, db, "team-old-epay")
	team, err := CreateAdtyTeam(leader.Id, 0, "Team", decimal.NewFromInt(1))
	require.NoError(t, err)
	channel := seedEpayChannel(t, db, model.AdtyEpayChannel{Name: "historical", GatewayURL: "https://pay.example.com", MerchantId: "m", OrderMode: model.AdtyEpayOrderModeSubmit}, alipay("submit"))
	now := common.GetTimestamp()
	results := map[string]string{}
	for _, state := range []string{"paid", "unpaid", "unknown"} {
		order, err := StartAdtyTeamTopupPayment(leader.Id, team.Team.Id, decimal.NewFromInt(1), "alipay", channel.Id, state, "")
		require.NoError(t, err)
		require.NoError(t, db.Model(order).Updates(map[string]any{"expire_at": 0, "created_at": now - 86400}).Error)
		results[order.TradeNo] = state
	}
	orig := adtyEpayQuery
	t.Cleanup(func() { adtyEpayQuery = orig })
	adtyEpayQuery = func(_ context.Context, _ *model.AdtyEpayChannel, trade string) (*AdtyEpayQueryResult, error) {
		if results[trade] == "unknown" {
			return nil, errors.New("no response")
		}
		var ext model.AdtyTopupOrder
		require.NoError(t, db.Where("trade_no=?", trade).First(&ext).Error)
		return &AdtyEpayQueryResult{Paid: results[trade] == "paid", MoneyCNY: ext.PayAmount, TypeCode: "alipay", GatewayTradeNo: "gateway-" + trade, OutTradeNo: trade, MerchantId: "m"}, nil
	}
	for i := 0; i < 2; i++ {
		runAdtyEpayFollowUpQueries(now)
		_, err := model.ExpireAdtyTopUps(now, 100)
		require.NoError(t, err)
	}
	for trade, state := range results {
		if state == "paid" {
			require.Equal(t, common.TopUpStatusSuccess, topUpStatusOf(t, db, trade))
		} else if state == "unpaid" {
			require.Equal(t, common.TopUpStatusExpired, topUpStatusOf(t, db, trade))
		}
		if state == "unknown" {
			require.Equal(t, common.TopUpStatusPending, topUpStatusOf(t, db, trade))
			var count int64
			require.NoError(t, db.Model(&model.PaymentOrphanEvent{}).Where("reference_id=?", trade).Count(&count).Error)
			require.EqualValues(t, 1, count)
		}
	}
	var stored model.AdtyTeam
	require.NoError(t, db.First(&stored, team.Team.Id).Error)
	require.EqualValues(t, 500000, stored.SharedQuota)
}

func TestTeamAnomalyPageOlderOpen(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	require.NoError(t, db.Create(&model.AdtyAnomalyDisposition{TeamId: 1, SiteId: 7, Ref: "old", OrderRef: "order-old", Kind: "team_principal_review", Status: "open"}).Error)
	for i := 0; i < 55; i++ {
		require.NoError(t, db.Create(&model.AdtyAnomalyDisposition{SiteId: 7, TeamId: 2, Kind: "closed_team_topup", Status: "closed"}).Error)
	}
	rows, total, err := PageAdtyAnomalyDispositions(7, "", "", "", 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 56, total)
	require.Equal(t, "old", rows[0].Ref)
	rows, total, err = PageAdtyAnomalyDispositions(7, "open", "order-old", "team_principal_review", 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	rows, _, err = PageAdtyAnomalyDispositions(7, "closed", "", "", 40, 20)
	require.NoError(t, err)
	require.Len(t, rows, 15)
}

func TestTeamLotFIFOUsesVerifiedTimeAndOrderIdentifier(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	leader := referralUser(t, db, "team-fifo")
	team, err := CreateAdtyTeam(leader.Id, 0, "FIFO", decimal.NewFromInt(1))
	require.NoError(t, err)
	for _, item := range []struct {
		trade string
		paid  int64
	}{{"B", 20}, {"Z", 10}, {"A", 20}} {
		require.NoError(t, applyAdtyTeamQuotaTx(db, &team.Team, 10, "topup", item.trade, leader.Id, item.paid))
	}
	require.NoError(t, applyAdtyTeamQuotaTx(db, &team.Team, -15, "spend", "fifo", leader.Id))
	var lots []model.AdtyTeamFundLot
	require.NoError(t, db.Where("team_id=?", team.Team.Id).Order("source").Find(&lots).Error)
	require.Len(t, lots, 3)
	require.EqualValues(t, 5, lots[0].Available)
	require.EqualValues(t, 10, lots[1].Available)
	require.Zero(t, lots[2].Available)
}
