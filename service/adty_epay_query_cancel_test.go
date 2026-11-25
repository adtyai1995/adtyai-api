package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupEpayQueryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.AdtyEpayChannel{}, &model.AdtyEpayChannelMethod{}))

	oldDB := model.DB
	oldRedis := common.RedisEnabled
	oldMemCache := common.MemoryCacheEnabled
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		_ = sqlDB.Close()
	})
	return db
}

func TestIsEpayOrderAbsentMsg(t *testing.T) {
	for _, msg := range []string{
		"订单不存在",
		"该订单号不存在",
		"无此订单",
		"未找到订单",
		"订单未找到",
		"未找到该订单",
		"找不到订单",
		"查无此单",
		"Order Not Found",
		"order does not exist",
		"order doesn't exist",
	} {
		assert.True(t, isEpayOrderAbsentMsg(msg), "expected absent message to match: %q", msg)
	}
	for _, msg := range []string{
		"",
		"签名错误",
		"商户密钥无效",
		"invalid signature",
		"permission denied",
	} {
		assert.False(t, isEpayOrderAbsentMsg(msg), "expected non-absent error not to match: %q", msg)
	}
}

func TestEpayProviderQueryAllowsMissingPIDButRejectsMismatchedPID(t *testing.T) {
	db := setupEpayQueryTestDB(t)
	channel := &model.AdtyEpayChannel{
		Name:        "Alipay-1",
		GatewayURL:  "https://pay.example.com",
		MerchantId:  "1001",
		MerchantKey: "secret-key",
		SignType:    model.AdtyEpaySignTypeMD5,
		OrderMode:   model.AdtyEpayOrderModeAPI,
		Status:      model.AdtyEpayStatusEnabled,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.AdtyEpayChannelMethod{
		ChannelId:   channel.Id,
		Method:      "alipay",
		TypeCode:    "alipay",
		DisplayName: "Alipay-1",
		Enabled:     true,
	}).Error)

	ext := &model.AdtyTopupOrder{
		TradeNo:   "EPY18176551795614877",
		Provider:  model.PaymentProviderAdtyEpay,
		Method:    "alipay",
		ChannelId: channel.Id,
	}

	origQuery := adtyEpayQuery
	t.Cleanup(func() { adtyEpayQuery = origQuery })

	t.Run("unpaid response without pid succeeds and Cancel allows cancellation", func(t *testing.T) {
		adtyEpayQuery = func(ctx context.Context, ch *model.AdtyEpayChannel, outTradeNo string) (*AdtyEpayQueryResult, error) {
			return &AdtyEpayQueryResult{
				Paid:           false,
				TypeCode:       "alipay",
				GatewayTradeNo: "GW1",
				OutTradeNo:     outTradeNo,
				MerchantId:     "",
			}, nil
		}
		facts, err := (adtyEpayProvider{}).Query(context.Background(), ext)
		require.NoError(t, err)
		assert.Nil(t, facts)
		require.NoError(t, (adtyEpayProvider{}).Cancel(ext))
	})

	t.Run("paid response without pid returns verified facts and Cancel detects payment", func(t *testing.T) {
		adtyEpayQuery = func(ctx context.Context, ch *model.AdtyEpayChannel, outTradeNo string) (*AdtyEpayQueryResult, error) {
			return &AdtyEpayQueryResult{
				Paid:           true,
				MoneyCNY:       decimal.RequireFromString("67.00"),
				TypeCode:       "alipay",
				GatewayTradeNo: "GW2",
				OutTradeNo:     outTradeNo,
				MerchantId:     "",
			}, nil
		}
		facts, err := (adtyEpayProvider{}).Query(context.Background(), ext)
		require.NoError(t, err)
		require.NotNil(t, facts)
		assert.Equal(t, "GW2", facts.GatewayTradeId)
		assert.True(t, facts.PaidAmount.Equal(decimal.RequireFromString("67.00")))

		err = (adtyEpayProvider{}).Cancel(ext)
		require.ErrorIs(t, err, ErrAdtyPaymentDetected)
	})

	t.Run("response with mismatched pid is still strictly rejected", func(t *testing.T) {
		adtyEpayQuery = func(ctx context.Context, ch *model.AdtyEpayChannel, outTradeNo string) (*AdtyEpayQueryResult, error) {
			return &AdtyEpayQueryResult{
				Paid:           false,
				TypeCode:       "alipay",
				GatewayTradeNo: "GW3",
				OutTradeNo:     outTradeNo,
				MerchantId:     "9999",
			}, nil
		}
		_, err := (adtyEpayProvider{}).Query(context.Background(), ext)
		require.ErrorIs(t, err, model.ErrPaymentMethodMismatch)
	})
}

func TestQueryAdtyEpayOrderMD5ExtendedAbsentMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("out_trade_no") {
		case "ABSENT_CN":
			_, _ = w.Write([]byte(`{"code":-1,"msg":"无此订单"}`))
		case "ABSENT_EN":
			_, _ = w.Write([]byte(`{"code":-1,"msg":"Order does not exist"}`))
		default:
			_, _ = w.Write([]byte(`{"code":-1,"msg":"签名错误"}`))
		}
	}))
	defer server.Close()

	origClient := adtyEpayHTTPClient
	adtyEpayHTTPClient = func() *http.Client { return server.Client() }
	t.Cleanup(func() { adtyEpayHTTPClient = origClient })

	channel := &model.AdtyEpayChannel{
		Id:          1,
		GatewayURL:  server.URL,
		MerchantId:  "1001",
		MerchantKey: "secret",
		SignType:    model.AdtyEpaySignTypeMD5,
		Status:      model.AdtyEpayStatusEnabled,
	}

	_, err := QueryAdtyEpayOrder(context.Background(), channel, "ABSENT_CN")
	require.ErrorIs(t, err, errEpayOrderAbsent)

	_, err = QueryAdtyEpayOrder(context.Background(), channel, "ABSENT_EN")
	require.ErrorIs(t, err, errEpayOrderAbsent)

	_, err = QueryAdtyEpayOrder(context.Background(), channel, "SIGN_ERR")
	require.Error(t, err)
	assert.NotErrorIs(t, err, errEpayOrderAbsent)
}
