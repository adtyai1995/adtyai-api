package service

import (
	"context"
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

type stubHostedQuerier struct {
	fn func(ctx context.Context, ext *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error)
}

func (s stubHostedQuerier) Query(ctx context.Context, ext *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error) {
	return s.fn(ctx, ext)
}

func setupHostedRecoveryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.TopUp{},
		&model.AdtyTopupOrder{},
		&model.AdtyOrderPaymentEvent{},
		&model.PaymentOrphanEvent{},
	))

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

func TestRecoverAdtyHostedPaymentConfirmingMismatchAndScanFilter(t *testing.T) {
	db := setupHostedRecoveryTestDB(t)

	assert.True(t, isPermanentAdtyTopUpCreditReject(model.ErrTopUpQuotaLimitExceeded))

	t.Run("ErrAdtyPaymentConfirming records confirming attempt and creates no orphan review", func(t *testing.T) {
		var recordedStatus string
		origAttempt := recordAdtyHostedPaymentAttempt
		origProvider := adtyHostedRecoveryProviderByName
		t.Cleanup(func() {
			recordAdtyHostedPaymentAttempt = origAttempt
			adtyHostedRecoveryProviderByName = origProvider
		})
		recordAdtyHostedPaymentAttempt = func(tradeNo, provider, source, status string, operatorID int) error {
			recordedStatus = status
			return nil
		}
		adtyHostedRecoveryProviderByName = func(name string) any {
			return stubHostedQuerier{fn: func(ctx context.Context, ext *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error) {
				return nil, ErrAdtyPaymentConfirming
			}}
		}

		ext := &model.AdtyTopupOrder{
			TradeNo:        "EPU_CONF_1",
			Provider:       model.PaymentProviderAdtyBEpusdt,
			GatewayTradeId: "GW_CONF_1",
		}
		err := RecoverAdtyHostedPaymentAs(context.Background(), ext, 7)
		require.ErrorIs(t, err, ErrAdtyPaymentConfirming)
		assert.Equal(t, "confirming", recordedStatus)

		var orphanCount int64
		require.NoError(t, db.Model(&model.PaymentOrphanEvent{}).Where("reference_id = ?", "EPU_CONF_1").Count(&orphanCount).Error)
		assert.Zero(t, orphanCount)
	})

	t.Run("query mismatch queues hosted-query orphan event", func(t *testing.T) {
		var recordedStatus string
		origAttempt := recordAdtyHostedPaymentAttempt
		origProvider := adtyHostedRecoveryProviderByName
		t.Cleanup(func() {
			recordAdtyHostedPaymentAttempt = origAttempt
			adtyHostedRecoveryProviderByName = origProvider
		})
		recordAdtyHostedPaymentAttempt = func(tradeNo, provider, source, status string, operatorID int) error {
			recordedStatus = status
			return nil
		}
		adtyHostedRecoveryProviderByName = func(name string) any {
			return stubHostedQuerier{fn: func(ctx context.Context, ext *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error) {
				return nil, model.ErrPaymentAmountMismatch
			}}
		}

		ext := &model.AdtyTopupOrder{
			TradeNo:        "EPU_MISMATCH_1",
			Provider:       model.PaymentProviderAdtyBEpusdt,
			GatewayTradeId: "GW_MISMATCH_1",
		}
		err := RecoverAdtyHostedPayment(context.Background(), ext)
		require.ErrorIs(t, err, model.ErrPaymentAmountMismatch)
		assert.Equal(t, "failed", recordedStatus)

		var orphan model.PaymentOrphanEvent
		require.NoError(t, db.Where("event_id = ?", "hosted-query:EPU_MISMATCH_1").First(&orphan).Error)
		assert.Equal(t, model.PaymentOrphanStatusPendingReview, orphan.Status)
		assert.Equal(t, "GW_MISMATCH_1", orphan.SessionID)
	})

	t.Run("recoverAdtyHostedPayments skips failed orders with missing remote checkout fields", func(t *testing.T) {
		now := int64(1764000000)
		// Failed order that never created a remote order (empty gateway_trade_id / pay_address / pay_url).
		require.NoError(t, db.Create(&model.TopUp{
			UserId: 1, TradeNo: "EPU_FAILED_EMPTY", PaymentProvider: model.PaymentProviderAdtyBEpusdt,
			PaymentMethod: AdtyMethodUSDTTRC20, Status: common.TopUpStatusFailed, CreateTime: now - 300,
		}).Error)
		require.NoError(t, db.Create(&model.AdtyTopupOrder{
			TradeNo: "EPU_FAILED_EMPTY", UserId: 1, Provider: model.PaymentProviderAdtyBEpusdt,
			Method: AdtyMethodUSDTTRC20, PayAmount: decimal.NewFromInt(10), PayCurrency: "USDT",
			CreatedAt: now - 300, LastQueryAt: 0,
		}).Error)

		// Order with complete remote checkout fields should be scanned.
		require.NoError(t, db.Create(&model.TopUp{
			UserId: 1, TradeNo: "EPU_FAILED_REMOTE", PaymentProvider: model.PaymentProviderAdtyBEpusdt,
			PaymentMethod: AdtyMethodUSDTTRC20, Status: common.TopUpStatusFailed, CreateTime: now - 300,
		}).Error)
		require.NoError(t, db.Create(&model.AdtyTopupOrder{
			TradeNo: "EPU_FAILED_REMOTE", UserId: 1, Provider: model.PaymentProviderAdtyBEpusdt,
			Method: AdtyMethodUSDTTRC20, PayAmount: decimal.NewFromInt(10), PayCurrency: "USDT",
			GatewayTradeId: "GW_OK", PayAddress: "TAddr1", PayURL: "https://pay.example.com/p/1",
			CreatedAt: now - 300, LastQueryAt: 0,
		}).Error)

		var scanned []string
		origAttempt := recordAdtyHostedPaymentAttempt
		origProvider := adtyHostedRecoveryProviderByName
		t.Cleanup(func() {
			recordAdtyHostedPaymentAttempt = origAttempt
			adtyHostedRecoveryProviderByName = origProvider
		})
		recordAdtyHostedPaymentAttempt = func(tradeNo, provider, source, status string, operatorID int) error { return nil }
		adtyHostedRecoveryProviderByName = func(name string) any {
			return stubHostedQuerier{fn: func(ctx context.Context, ext *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error) {
				scanned = append(scanned, ext.TradeNo)
				return nil, nil
			}}
		}

		recoverAdtyHostedPayments(now)
		assert.Equal(t, []string{"EPU_FAILED_REMOTE"}, scanned)
	})
}
