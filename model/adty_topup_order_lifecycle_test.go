package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTopupOrderLifecycleTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&User{},
		&TopUp{},
		&AdtyTopupOrder{},
		&AdtyFundBatch{},
		&AdtyBalanceLedger{},
		&AdtyOrderPaymentEvent{},
		&PaymentOrphanEvent{},
		&Log{},
		&AdtySite{},
		&AdtyTeam{},
	))

	oldDB := DB
	oldLogDB := LOG_DB
	oldRedis := common.RedisEnabled
	oldMem := common.MemoryCacheEnabled
	oldAfter := AdtyAfterTopUpCompleted
	oldCreditCommitted := AdtyAfterTopUpCreditCommitted
	oldCreditedTx := AdtyAfterTopUpCreditedTx
	oldPrepGift := AdtyPrepareTopUpGiftOnCreditTx
	oldBeforeCreate := AdtyBeforeTopUpCreateTx
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	AdtyAfterTopUpCompleted = nil
	AdtyAfterTopUpCreditCommitted = nil
	AdtyAfterTopUpCreditedTx = nil
	AdtyPrepareTopUpGiftOnCreditTx = nil
	AdtyBeforeTopUpCreateTx = nil
	DB = db
	LOG_DB = db
	t.Cleanup(func() {
		DB = oldDB
		LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMem
		AdtyAfterTopUpCompleted = oldAfter
		AdtyAfterTopUpCreditCommitted = oldCreditCommitted
		AdtyAfterTopUpCreditedTx = oldCreditedTx
		AdtyPrepareTopUpGiftOnCreditTx = oldPrepGift
		AdtyBeforeTopUpCreateTx = oldBeforeCreate
		_ = sqlDB.Close()
	})
	return db
}

func TestCheckAdtyPaymentFactsHardening(t *testing.T) {
	t.Run("epay requires non-empty matching gateway trade id and exact pay amount", func(t *testing.T) {
		ext := &AdtyTopupOrder{
			Provider:       PaymentProviderAdtyEpay,
			PayAmount:      decimal.RequireFromString("10.00"),
			GatewayTradeId: "GW1001",
		}
		err := checkAdtyPaymentFacts(ext, AdtyPaymentFacts{
			Provider:       PaymentProviderAdtyEpay,
			PaidAmount:     decimal.NewFromInt(10),
			GatewayTradeId: "",
			Source:         AdtyPaymentSourceCallback,
		})
		assert.ErrorIs(t, err, ErrPaymentMethodMismatch)

		err = checkAdtyPaymentFacts(ext, AdtyPaymentFacts{
			Provider:       PaymentProviderAdtyEpay,
			PaidAmount:     decimal.NewFromInt(10),
			GatewayTradeId: "GW9999",
			Source:         AdtyPaymentSourceCallback,
		})
		assert.ErrorIs(t, err, ErrPaymentMethodMismatch)

		err = checkAdtyPaymentFacts(ext, AdtyPaymentFacts{
			Provider:       PaymentProviderAdtyEpay,
			PaidAmount:     decimal.RequireFromString("10.004"),
			GatewayTradeId: "GW1001",
			Source:         AdtyPaymentSourceCallback,
		})
		assert.ErrorIs(t, err, ErrPaymentAmountMismatch)

		err = checkAdtyPaymentFacts(ext, AdtyPaymentFacts{
			Provider:       PaymentProviderAdtyEpay,
			PaidAmount:     decimal.NewFromInt(10),
			GatewayTradeId: "GW1001",
			Source:         AdtyPaymentSourceCallback,
		})
		assert.NoError(t, err)
	})

	t.Run("bepusdt requires address, trade id, callback tx hash, and exact fiat", func(t *testing.T) {
		ext := &AdtyTopupOrder{
			Provider:        PaymentProviderAdtyBEpusdt,
			FaceUSD:         decimal.NewFromInt(10),
			TopupGroupRatio: decimal.NewFromInt(1),
			PayAmount:       decimal.RequireFromString("10.0007"),
			PayAddress:      "TAddr123",
			GatewayTradeId:  "BEP1001",
		}
		valid := AdtyPaymentFacts{
			Provider:       PaymentProviderAdtyBEpusdt,
			FiatAmount:     decimal.RequireFromString("10.00"),
			PaidAmount:     decimal.RequireFromString("10.0007"),
			PayAddress:     "TAddr123",
			GatewayTradeId: "BEP1001",
			TxHash:         "0xtxhash",
			Source:         AdtyPaymentSourceCallback,
		}
		assert.NoError(t, checkAdtyPaymentFacts(ext, valid))

		missingAddr := valid
		missingAddr.PayAddress = " "
		assert.ErrorIs(t, checkAdtyPaymentFacts(ext, missingAddr), ErrPaymentMethodMismatch)

		missingGW := valid
		missingGW.GatewayTradeId = ""
		assert.ErrorIs(t, checkAdtyPaymentFacts(ext, missingGW), ErrPaymentMethodMismatch)

		missingTxOnCallback := valid
		missingTxOnCallback.TxHash = ""
		assert.ErrorIs(t, checkAdtyPaymentFacts(ext, missingTxOnCallback), ErrPaymentMethodMismatch)

		// Query source does not receive block_transaction_id from /api/v1/pay/info.
		missingTxOnQuery := valid
		missingTxOnQuery.TxHash = ""
		missingTxOnQuery.Source = AdtyPaymentSourceQuery
		assert.NoError(t, checkAdtyPaymentFacts(ext, missingTxOnQuery))

		fiatDrift := valid
		fiatDrift.FiatAmount = decimal.RequireFromString("10.004")
		assert.ErrorIs(t, checkAdtyPaymentFacts(ext, fiatDrift), ErrPaymentAmountMismatch)
	})
}

func TestCompleteAdtyTopUpBackfillAndDuplicateReviewErrorPropagation(t *testing.T) {
	db := setupTopupOrderLifecycleTestDB(t)
	require.NoError(t, db.Create(&User{
		Id:       11,
		Username: "alice",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}).Error)

	creditQuota := decimal.NewFromInt(10).Mul(common.QuotaPerUnitDecimal()).IntPart()
	require.NoError(t, db.Create(&TopUp{
		UserId:          11,
		Amount:          10,
		Money:           10,
		TradeNo:         "EPU_BACKFILL_1",
		PaymentMethod:   "usdt.trc20",
		PaymentProvider: PaymentProviderAdtyBEpusdt,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}).Error)
	require.NoError(t, db.Create(&AdtyTopupOrder{
		Business:        AdtyBusinessWalletTopup,
		TradeNo:         "EPU_BACKFILL_1",
		UserId:          11,
		Provider:        PaymentProviderAdtyBEpusdt,
		Method:          "usdt.trc20",
		FaceUSD:         decimal.NewFromInt(10),
		CreditQuota:     creditQuota,
		PayAmount:       decimal.NewFromInt(10),
		PayCurrency:     "USDT",
		TopupGroupRatio: decimal.NewFromInt(1),
		ExpireAt:        time.Now().Unix() + 600,
	}).Error)

	alreadyDone, err := CompleteAdtyTopUp("EPU_BACKFILL_1", AdtyPaymentFacts{
		Provider:       PaymentProviderAdtyBEpusdt,
		Method:         "usdt.trc20",
		FiatAmount:     decimal.NewFromInt(10),
		PaidAmount:     decimal.NewFromInt(10),
		PaidCurrency:   "USDT",
		PayAddress:     "TBackfilledAddr",
		GatewayTradeId: "GW_BACKFILLED",
		TxHash:         "tx-first",
		Source:         AdtyPaymentSourceCallback,
	}, "127.0.0.1")
	require.NoError(t, err)
	assert.False(t, alreadyDone)

	ext, err := GetAdtyTopupOrder("EPU_BACKFILL_1")
	require.NoError(t, err)
	require.NotNil(t, ext)
	assert.Equal(t, "TBackfilledAddr", ext.PayAddress)
	assert.Equal(t, "GW_BACKFILLED", ext.GatewayTradeId)

	// Second callback with different voucher returns ErrAdtyDuplicatePayment and records orphan event.
	_, err = CompleteAdtyTopUp("EPU_BACKFILL_1", AdtyPaymentFacts{
		Provider:       PaymentProviderAdtyBEpusdt,
		Method:         "usdt.trc20",
		FiatAmount:     decimal.NewFromInt(10),
		PaidAmount:     decimal.NewFromInt(10),
		PaidCurrency:   "USDT",
		PayAddress:     "TBackfilledAddr",
		GatewayTradeId: "GW_BACKFILLED",
		TxHash:         "tx-second-dup",
		Source:         AdtyPaymentSourceCallback,
	}, "127.0.0.1")
	assert.ErrorIs(t, err, ErrAdtyDuplicatePayment)
	var orphanCount int64
	require.NoError(t, db.Model(&PaymentOrphanEvent{}).Where("reference_id = ? AND event_type = ?", "EPU_BACKFILL_1", "duplicate_payment").Count(&orphanCount).Error)
	assert.Equal(t, int64(1), orphanCount)

	// If duplicate-payment audit recording fails on DB error, propagate that error instead of swallowing it.
	require.NoError(t, db.Migrator().DropTable(&PaymentOrphanEvent{}))
	_, err = CompleteAdtyTopUp("EPU_BACKFILL_1", AdtyPaymentFacts{
		Provider:       PaymentProviderAdtyBEpusdt,
		Method:         "usdt.trc20",
		FiatAmount:     decimal.NewFromInt(10),
		PaidAmount:     decimal.NewFromInt(10),
		PaidCurrency:   "USDT",
		PayAddress:     "TBackfilledAddr",
		GatewayTradeId: "GW_BACKFILLED",
		TxHash:         "tx-third-dup",
		Source:         AdtyPaymentSourceCallback,
	}, "127.0.0.1")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrAdtyDuplicatePayment)
}

func TestListUserAdtyTopUpsFilteredAndAtomicPendingCap(t *testing.T) {
	db := setupTopupOrderLifecycleTestDB(t)
	now := time.Now().Unix()

	seed := func(userID int, tradeNo, provider, method, status, business, label string, teamID int64, siteID int, createTime int64, withExt bool) {
		require.NoError(t, db.Create(&TopUp{
			UserId:          userID,
			Amount:          10,
			Money:           10,
			TradeNo:         tradeNo,
			PaymentMethod:   method,
			PaymentProvider: provider,
			CreateTime:      createTime,
			Status:          status,
		}).Error)
		if withExt {
			require.NoError(t, db.Create(&AdtyTopupOrder{
				Business:    business,
				TradeNo:     tradeNo,
				UserId:      userID,
				TeamId:      teamID,
				SiteId:      siteID,
				Provider:    provider,
				Method:      method,
				MethodLabel: label,
				ExpireAt:    now + 600,
			}).Error)
		}
	}

	seed(21, "WALLET_EPY_1", PaymentProviderAdtyEpay, "alipay", common.TopUpStatusSuccess, AdtyBusinessWalletTopup, "Alipay-Fast", 0, 0, now-300, true)
	seed(21, "WALLET_LEGACY_1", PaymentProviderAdtyBEpusdt, "usdt.trc20", TopUpStatusCanceled, "", "", 0, 0, now-200, true)
	seed(21, "WALLET_NOEXT_1", PaymentProviderAdtyEpay, "wxpay", common.TopUpStatusPending, "", "", 0, 0, now-150, false)
	seed(21, "TEAM_ORDER_1", PaymentProviderAdtyEpay, "alipay", common.TopUpStatusSuccess, AdtyBusinessTeamTopup, "", 7, 0, now-100, true)
	seed(21, "MARGIN_ORDER_1", PaymentProviderAdtyBEpusdt, "usdt.bep20", common.TopUpStatusSuccess, AdtyBusinessOwnerMarginTopup, "", 0, 5, now-50, true)
	seed(21, "MARGIN_VOUCHER_1", AdtyVoucherProvider, AdtyVoucherPurposeSiteMargin, common.TopUpStatusSuccess, "", "", 0, 5, now-40, true)
	seed(21, "USER_VOUCHER_1", AdtyVoucherProvider, AdtyVoucherPurposeMainUser, common.TopUpStatusSuccess, AdtyBusinessWalletTopup, "", 0, 0, now-30, true)
	seed(99, "OTHER_USER_1", PaymentProviderAdtyEpay, "alipay", common.TopUpStatusSuccess, AdtyBusinessWalletTopup, "", 0, 0, now-10, true)

	// Wallet scope excludes team, owner margin, and margin voucher.
	walletItems, total, err := ListUserAdtyTopUpsFiltered(21, AdtyUserTopUpListFilter{
		Business: AdtyBusinessWalletTopup,
		Limit:    20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	gotWallet := make([]string, 0, len(walletItems))
	for _, it := range walletItems {
		gotWallet = append(gotWallet, it.TradeNo)
	}
	assert.Equal(t, []string{"USER_VOUCHER_1", "WALLET_NOEXT_1", "WALLET_LEGACY_1", "WALLET_EPY_1"}, gotWallet)

	// Owner margin scope includes online margin topup + margin voucher on site 5.
	marginItems, marginTotal, err := ListUserAdtyTopUpsFiltered(21, AdtyUserTopUpListFilter{
		Business:    AdtyBusinessOwnerMarginTopup,
		OwnerSiteID: 5,
		Limit:       20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), marginTotal)
	assert.Equal(t, "MARGIN_VOUCHER_1", marginItems[0].TradeNo)
	assert.Equal(t, "MARGIN_ORDER_1", marginItems[1].TradeNo)

	// All business + method=usdt + status=cancelled (UK spelling normalized) + keyword + time range.
	usdtCanceled, usdtTotal, err := ListUserAdtyTopUpsFiltered(21, AdtyUserTopUpListFilter{
		Business:       "all",
		Method:         "usdt",
		Status:         "cancelled",
		StartTimestamp: now - 250,
		EndTimestamp:   now - 180,
		Limit:          10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), usdtTotal)
	assert.Equal(t, "WALLET_LEGACY_1", usdtCanceled[0].TradeNo)

	// Method label keyword search.
	labelItems, labelTotal, err := ListUserAdtyTopUpsFiltered(21, AdtyUserTopUpListFilter{
		Business: AdtyBusinessWalletTopup,
		Keyword:  "alipay-fast",
		Limit:    10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), labelTotal)
	assert.Equal(t, "WALLET_EPY_1", labelItems[0].TradeNo)

	// Atomic MaxActivePending enforcement in CreateAdtyTopUpWithPolicy.
	for i := 0; i < 2; i++ {
		tradeNo := fmt.Sprintf("CAP_USER_%d", i)
		err := CreateAdtyTopUp(&TopUp{
			UserId: 33, Amount: 10, Money: 10, TradeNo: tradeNo,
			PaymentMethod: "usdt.trc20", PaymentProvider: PaymentProviderAdtyBEpusdt,
			CreateTime: now, Status: common.TopUpStatusPending,
		}, &AdtyTopupOrder{
			Business: AdtyBusinessWalletTopup, TradeNo: tradeNo, UserId: 33,
			Provider: PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
			ExpireAt: now + 600, MaxActivePending: 2,
		})
		require.NoError(t, err)
	}
	err = CreateAdtyTopUp(&TopUp{
		UserId: 33, Amount: 10, Money: 10, TradeNo: "CAP_USER_OVER",
		PaymentMethod: "usdt.trc20", PaymentProvider: PaymentProviderAdtyBEpusdt,
		CreateTime: now, Status: common.TopUpStatusPending,
	}, &AdtyTopupOrder{
		Business: AdtyBusinessWalletTopup, TradeNo: "CAP_USER_OVER", UserId: 33,
		Provider: PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
		ExpireAt: now + 600, MaxActivePending: 2,
	})
	assert.ErrorIs(t, err, ErrAdtyTooManyPendingOrders)

	assert.True(t, (&AdtyTopupOrder{TeamId: 1}).IsTeamTopup())
	assert.True(t, (&AdtyTopupOrder{Business: AdtyBusinessTeamTopup}).IsTeamTopup())
	assert.False(t, (&AdtyTopupOrder{Business: AdtyBusinessWalletTopup}).IsTeamTopup())
	assert.False(t, (*AdtyTopupOrder)(nil).IsTeamTopup())
}
