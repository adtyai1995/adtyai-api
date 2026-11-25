package service

import (
	"context"
	"errors"
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

func setupEpayHealthTestDB(t *testing.T) *gorm.DB {
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
		&model.Log{},
		&model.AuditLog{},
		&model.AdtyBalanceLedger{},
		&model.AdtyFundBatch{},
		&model.AdtyTopupOrder{},
		&model.AdtyEpayChannel{},
		&model.AdtyEpayChannelMethod{},
		&model.AdtyOrderPaymentEvent{},
		&model.AdtySiteInviteCommission{},
		&model.PaymentOrphanEvent{},
	))

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedis := common.RedisEnabled
	oldMemCache := common.MemoryCacheEnabled
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	model.DB = db
	model.LOG_DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		_ = sqlDB.Close()
	})
	return db
}

func TestEpayFollowUpCanceledFailedRecoveryOrphanClosingAndTeamHTTP(t *testing.T) {
	db := setupEpayHealthTestDB(t)
	now := int64(1764000000)

	user := &model.User{Username: "epay_health_user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(user).Error)

	httpsCh := &model.AdtyEpayChannel{
		Name: "HTTPS-Ch", GatewayURL: "https://pay.example.com", MerchantId: "1001", MerchantKey: "sec",
		SignType: model.AdtyEpaySignTypeMD5, OrderMode: model.AdtyEpayOrderModeAPI, Status: model.AdtyEpayStatusEnabled,
	}
	require.NoError(t, db.Create(httpsCh).Error)
	require.NoError(t, db.Create(&model.AdtyEpayChannelMethod{
		ChannelId: httpsCh.Id, Method: "alipay", TypeCode: "alipay", Enabled: true,
	}).Error)

	httpCh := &model.AdtyEpayChannel{
		Name: "HTTP-Ch", GatewayURL: "http://pay.example.com", MerchantId: "1002", MerchantKey: "sec",
		SignType: model.AdtyEpaySignTypeMD5, OrderMode: model.AdtyEpayOrderModeAPI, Status: model.AdtyEpayStatusEnabled,
	}
	require.NoError(t, db.Create(httpCh).Error)
	require.NoError(t, db.Create(&model.AdtyEpayChannelMethod{
		ChannelId: httpCh.Id, Method: "alipay", TypeCode: "alipay", Enabled: true,
	}).Error)

	// 1. Canceled order within window that actually got paid -> should recover and auto-close legacy/unified pending reviews.
	require.NoError(t, db.Create(&model.TopUp{
		UserId: user.Id, TradeNo: "EPY_CANCELED_PAID", PaymentProvider: model.PaymentProviderAdtyEpay,
		PaymentMethod: "alipay", Status: model.TopUpStatusCanceled, CreateTime: now - 300,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: "EPY_CANCELED_PAID", UserId: user.Id, Provider: model.PaymentProviderAdtyEpay,
		Method: "alipay", ChannelId: httpsCh.Id, FaceUSD: decimal.NewFromInt(10),
		CreditQuota: 10 * common.QuotaPerUnitDecimal().IntPart(), PayAmount: decimal.NewFromInt(70), PayCurrency: "CNY",
		CreatedAt: now - 300, ExpireAt: now - 100,
	}).Error)
	for _, evID := range []string{"hosted-query:EPY_CANCELED_PAID", "epay-query:EPY_CANCELED_PAID", "team-query:EPY_CANCELED_PAID"} {
		require.NoError(t, db.Create(&model.PaymentOrphanEvent{
			Provider: model.PaymentProviderAdtyEpay, EventID: evID, EventType: "payment_query",
			ReferenceID: "EPY_CANCELED_PAID", Status: model.PaymentOrphanStatusPendingReview,
		}).Error)
	}

	// 2. Failed order within window whose payment amount mismatches -> permanent credit rejection queues hosted-query orphan AND records rejected attempt.
	require.NoError(t, db.Create(&model.TopUp{
		UserId: user.Id, TradeNo: "EPY_FAILED_REJECT", PaymentProvider: model.PaymentProviderAdtyEpay,
		PaymentMethod: "alipay", Status: common.TopUpStatusFailed, CreateTime: now - 300,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: "EPY_FAILED_REJECT", UserId: user.Id, Provider: model.PaymentProviderAdtyEpay,
		Method: "alipay", ChannelId: httpsCh.Id, FaceUSD: decimal.NewFromInt(10),
		CreditQuota: 10 * common.QuotaPerUnitDecimal().IntPart(), PayAmount: decimal.NewFromInt(70), PayCurrency: "CNY",
		CreatedAt: now - 300, ExpireAt: now - 100,
	}).Error)

	// 3. Team order on HTTP channel past deadline -> must NOT queue team-query orphan event.
	require.NoError(t, db.Create(&model.TopUp{
		UserId: user.Id, TradeNo: "EPY_TEAM_HTTP", PaymentProvider: model.PaymentProviderAdtyEpay,
		PaymentMethod: "alipay", Status: common.TopUpStatusExpired, CreateTime: now - 900,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: "EPY_TEAM_HTTP", UserId: user.Id, Provider: model.PaymentProviderAdtyEpay,
		Method: "alipay", ChannelId: httpCh.Id, TeamId: 5, FaceUSD: decimal.NewFromInt(10),
		CreditQuota: 10 * common.QuotaPerUnitDecimal().IntPart(), PayAmount: decimal.NewFromInt(70), PayCurrency: "CNY",
		CreatedAt: now - 900, ExpireAt: now - 100,
	}).Error)

	// 4. Team order on HTTPS channel past deadline whose query errors -> queues deterministic team-query:<trade_no>.
	require.NoError(t, db.Create(&model.TopUp{
		UserId: user.Id, TradeNo: "EPY_TEAM_HTTPS", PaymentProvider: model.PaymentProviderAdtyEpay,
		PaymentMethod: "alipay", Status: common.TopUpStatusExpired, CreateTime: now - 900,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: "EPY_TEAM_HTTPS", UserId: user.Id, Provider: model.PaymentProviderAdtyEpay,
		Method: "alipay", ChannelId: httpsCh.Id, TeamId: 5, FaceUSD: decimal.NewFromInt(10),
		CreditQuota: 10 * common.QuotaPerUnitDecimal().IntPart(), PayAmount: decimal.NewFromInt(70), PayCurrency: "CNY",
		CreatedAt: now - 900, ExpireAt: now - 100,
	}).Error)

	origQuery := adtyEpayQuery
	t.Cleanup(func() { adtyEpayQuery = origQuery })
	adtyEpayQuery = func(ctx context.Context, ch *model.AdtyEpayChannel, outTradeNo string) (*AdtyEpayQueryResult, error) {
		switch outTradeNo {
		case "EPY_CANCELED_PAID":
			return &AdtyEpayQueryResult{
				Paid: true, RawStatus: "1", MoneyCNY: decimal.NewFromInt(70),
				TypeCode: "alipay", GatewayTradeNo: "GW_REC_1", OutTradeNo: outTradeNo, MerchantId: ch.MerchantId,
			}, nil
		case "EPY_FAILED_REJECT":
			return &AdtyEpayQueryResult{
				Paid: true, RawStatus: "1", MoneyCNY: decimal.NewFromInt(1), // mismatches 70 CNY
				TypeCode: "alipay", GatewayTradeNo: "GW_REJ_1", OutTradeNo: outTradeNo, MerchantId: ch.MerchantId,
			}, nil
		case "EPY_TEAM_HTTPS":
			return nil, errors.New("gateway timeout")
		default:
			return nil, errEpayOrderAbsent
		}
	}

	runAdtyEpayFollowUpQueries(now)

	// Verify canceled paid order recovered and all three orphan event IDs closed.
	var recovered model.TopUp
	require.NoError(t, db.Where("trade_no = ?", "EPY_CANCELED_PAID").First(&recovered).Error)
	assert.Equal(t, common.TopUpStatusSuccess, recovered.Status)
	var remainingPending int64
	require.NoError(t, db.Model(&model.PaymentOrphanEvent{}).
		Where("reference_id = ? AND status = ?", "EPY_CANCELED_PAID", model.PaymentOrphanStatusPendingReview).
		Count(&remainingPending).Error)
	assert.Zero(t, remainingPending)

	// Verify failed rejected order queued under hosted-query: AND logged rejected attempt.
	var rejOrphan model.PaymentOrphanEvent
	require.NoError(t, db.Where("event_id = ?", "hosted-query:EPY_FAILED_REJECT").First(&rejOrphan).Error)
	assert.Equal(t, model.PaymentOrphanStatusPendingReview, rejOrphan.Status)
	var rejAttempt model.AdtyOrderPaymentEvent
	require.NoError(t, db.Where("trade_no = ? AND outcome = ?", "EPY_FAILED_REJECT", "rejected").First(&rejAttempt).Error)

	// Verify team HTTP did NOT create orphan event, while team HTTPS created deterministic team-query: event.
	var httpOrphanCount int64
	require.NoError(t, db.Model(&model.PaymentOrphanEvent{}).Where("reference_id = ?", "EPY_TEAM_HTTP").Count(&httpOrphanCount).Error)
	assert.Zero(t, httpOrphanCount)
	var httpsTeamOrphan model.PaymentOrphanEvent
	require.NoError(t, db.Where("event_id = ?", "team-query:EPY_TEAM_HTTPS").First(&httpsTeamOrphan).Error)
	assert.Equal(t, "team_payment_query", httpsTeamOrphan.EventType)
}

func TestUserCancelDoesNotIncrementEpayUnpaidStreak(t *testing.T) {
	db := setupEpayHealthTestDB(t)

	// User-initiated cancel hook must not be wired to countAdtyEpayUnpaid (prevents single-user channel DoS).
	assert.Nil(t, AdtyCanceledHook)

	ch := &model.AdtyEpayChannel{
		Name: "StreakCh", GatewayURL: "https://pay.example.com", MerchantId: "1001", MerchantKey: "sec",
		SignType: model.AdtyEpaySignTypeMD5, OrderMode: model.AdtyEpayOrderModeAPI,
		Status: model.AdtyEpayStatusEnabled, UnpaidThreshold: 2,
	}
	require.NoError(t, db.Create(ch).Error)

	require.NoError(t, db.Create(&model.TopUp{
		UserId: 1, TradeNo: "EPY_EXP_1", PaymentProvider: model.PaymentProviderAdtyEpay,
		PaymentMethod: "alipay", Status: common.TopUpStatusExpired,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: "EPY_EXP_1", UserId: 1, Provider: model.PaymentProviderAdtyEpay, Method: "alipay", ChannelId: ch.Id,
	}).Error)

	// Expired orders still increment unpaid_streak via AdtyExpiredHook.
	require.NotNil(t, AdtyExpiredHook)
	AdtyExpiredHook([]model.ExpiredAdtyTopUp{{TradeNo: "EPY_EXP_1", Provider: model.PaymentProviderAdtyEpay}})

	updated, err := model.GetAdtyEpayChannel(ch.Id)
	require.NoError(t, err)
	assert.Equal(t, 1, updated.UnpaidStreak)
	assert.Equal(t, model.AdtyEpayStatusEnabled, updated.Status)
}
