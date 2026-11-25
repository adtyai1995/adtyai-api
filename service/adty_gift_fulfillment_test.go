package service

import (
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupGiftFulfillmentFixTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	tables := []any{
		&model.Option{},
		&model.User{},
		&model.TopUp{},
		&model.AdtyTopupOrder{},
		&model.AdtyOrderPaymentEvent{},
		&model.AdtyEpayChannel{},
		&model.AdtyEpayChannelMethod{},
		&model.AdtyFundBatch{},
		&model.AdtyFundAllocation{},
		&model.AdtyGiftFulfillment{},
		&model.AdtyManualRefund{},
		&model.AdtyBalanceLedger{},
		&model.AdtyAnomalyDisposition{},
		&model.AuditIntent{},
		&model.AuditLog{},
		&model.AdtySite{},
		&model.AdtySiteOwnership{},
		&model.AdtySiteWallet{},
		&model.AdtySiteWalletLedger{},
		&model.AdtySiteEarningDaily{},
		&model.AdtySiteTier{},
	}
	require.NoError(t, db.AutoMigrate(tables...))

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedis := common.RedisEnabled
	oldMemCache := common.MemoryCacheEnabled
	oldOptionMap := common.OptionMap
	oldBepURL := setting.GetBEpusdtSetting().BaseURL

	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	model.DB = db
	model.LOG_DB = db
	t.Cleanup(func() {
		_ = model.ApplyOptionValues(map[string]string{setting.BEpusdtOptionKeyBaseURL: oldBepURL})
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
		_ = sqlDB.Close()
	})
	return db
}

func TestConfirmTopUpUnpaidBeforeExpire_BEpusdtAndEpayFixes(t *testing.T) {
	db := setupGiftFulfillmentFixTestDB(t)
	expireAt := int64(1_700_000_000)

	t.Run("BEpusdt host mismatch or cleared BaseURL allows expiry", func(t *testing.T) {
		ext := &model.AdtyTopupOrder{
			TradeNo:        "BEP_HOST_CHG",
			Provider:       model.PaymentProviderAdtyBEpusdt,
			GatewayTradeId: "GW-1",
			PayAddress:     "TAddr123",
			PayURL:         "https://old-bep.example.com/pay/GW-1",
			ExpireAt:       expireAt,
		}
		require.NoError(t, model.ApplyOptionValues(map[string]string{
			setting.BEpusdtOptionKeyBaseURL: "https://new-bep.example.com",
		}))
		allow, err := confirmTopUpUnpaidBeforeExpireTx(db, nil, ext, expireAt+120)
		require.NoError(t, err)
		assert.True(t, allow, "switched BEpusdt BaseURL host should not trap old order in pending")

		require.NoError(t, model.ApplyOptionValues(map[string]string{
			setting.BEpusdtOptionKeyBaseURL: "",
		}))
		allow, err = confirmTopUpUnpaidBeforeExpireTx(db, nil, ext, expireAt+120)
		require.NoError(t, err)
		assert.True(t, allow, "cleared BEpusdt BaseURL should not trap old order in pending")
	})

	t.Run("BEpusdt matching host blocks expiry on confirming even past 24h cap", func(t *testing.T) {
		require.NoError(t, model.ApplyOptionValues(map[string]string{
			setting.BEpusdtOptionKeyBaseURL: "https://bep.example.com",
		}))
		ext := &model.AdtyTopupOrder{
			TradeNo:        "BEP_CONFIRMING",
			Provider:       model.PaymentProviderAdtyBEpusdt,
			GatewayTradeId: "GW-2",
			PayAddress:     "TAddr456",
			PayURL:         "https://bep.example.com/pay/GW-2",
			ExpireAt:       expireAt,
		}
		require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{
			TradeNo:   ext.TradeNo,
			Provider:  ext.Provider,
			Source:    model.AdtyPaymentSourceQuery,
			Outcome:   "confirming",
			CreatedAt: expireAt + 10,
		}).Error)

		allow, err := confirmTopUpUnpaidBeforeExpireTx(db, nil, ext, expireAt+120)
		require.NoError(t, err)
		assert.False(t, allow, "confirming outcome must block expiry")

		allow, err = confirmTopUpUnpaidBeforeExpireTx(db, nil, ext, expireAt+25*3600)
		require.NoError(t, err)
		assert.False(t, allow, "confirming outcome must remain blocked even past 24h cap")
	})

	t.Run("BEpusdt matching host allows expiry on post-expiry unpaid or after 24h failed queries", func(t *testing.T) {
		require.NoError(t, model.ApplyOptionValues(map[string]string{
			setting.BEpusdtOptionKeyBaseURL: "https://bep.example.com",
		}))
		ext := &model.AdtyTopupOrder{
			TradeNo:        "BEP_FAILED_CAP",
			Provider:       model.PaymentProviderAdtyBEpusdt,
			GatewayTradeId: "GW-3",
			PayAddress:     "TAddr789",
			PayURL:         "https://bep.example.com/pay/GW-3",
			ExpireAt:       expireAt,
		}
		require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{
			TradeNo:   ext.TradeNo,
			Provider:  ext.Provider,
			Source:    model.AdtyPaymentSourceQuery,
			Outcome:   "failed",
			CreatedAt: expireAt + 10,
		}).Error)

		allow, err := confirmTopUpUnpaidBeforeExpireTx(db, nil, ext, expireAt+120)
		require.NoError(t, err)
		assert.False(t, allow, "failed query within 24h should still block expiry")

		allow, err = confirmTopUpUnpaidBeforeExpireTx(db, nil, ext, expireAt+24*3600)
		require.NoError(t, err)
		assert.True(t, allow, "persistently failing queries past 24h cap should allow expiry")

		extUnpaid := &model.AdtyTopupOrder{
			TradeNo:        "BEP_UNPAID_OK",
			Provider:       model.PaymentProviderAdtyBEpusdt,
			GatewayTradeId: "GW-4",
			PayAddress:     "TAddr999",
			PayURL:         "https://bep.example.com/pay/GW-4",
			ExpireAt:       expireAt,
		}
		require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{
			TradeNo:   extUnpaid.TradeNo,
			Provider:  extUnpaid.Provider,
			Source:    model.AdtyPaymentSourceQuery,
			Outcome:   "unpaid",
			CreatedAt: expireAt + 5,
		}).Error)
		allow, err = confirmTopUpUnpaidBeforeExpireTx(db, nil, extUnpaid, expireAt+120)
		require.NoError(t, err)
		assert.True(t, allow)
	})

	t.Run("Epay disabled, maintenance zero-gift, 30m zero-gift cap, and 24h reserved-gift cap", func(t *testing.T) {
		chDisabled := &model.AdtyEpayChannel{
			Name: "disabled", GatewayURL: "https://epay.example.com", MerchantId: "1001",
			MerchantKey: "secret", SignType: model.AdtyEpaySignTypeMD5, Status: model.AdtyEpayStatusDisabled,
		}
		chMaint := &model.AdtyEpayChannel{
			Name: "maint", GatewayURL: "https://epay.example.com", MerchantId: "1002",
			MerchantKey: "secret", SignType: model.AdtyEpaySignTypeMD5, Status: model.AdtyEpayStatusMaintenance,
		}
		chEnabled := &model.AdtyEpayChannel{
			Name: "enabled", GatewayURL: "https://epay.example.com", MerchantId: "1003",
			MerchantKey: "secret", SignType: model.AdtyEpaySignTypeMD5, Status: model.AdtyEpayStatusEnabled,
		}
		require.NoError(t, db.Create(chDisabled).Error)
		require.NoError(t, db.Create(chMaint).Error)
		require.NoError(t, db.Create(chEnabled).Error)

		// Disabled channel allows expiry even with site gift reserve.
		extDisabled := &model.AdtyTopupOrder{
			TradeNo: "EPY_DISABLED", Provider: model.PaymentProviderAdtyEpay, ChannelId: chDisabled.Id,
			GiftReserveEarning: decimal.NewFromInt(5), ExpireAt: expireAt,
		}
		allow, err := confirmTopUpUnpaidBeforeExpireTx(db, nil, extDisabled, expireAt+120)
		require.NoError(t, err)
		assert.True(t, allow, "disabled epay channel must allow expiry")

		// Maintenance channel allows expiry for zero-gift order, blocks for reserved-gift order within 24h.
		extMaintZero := &model.AdtyTopupOrder{
			TradeNo: "EPY_MAINT_ZERO", Provider: model.PaymentProviderAdtyEpay, ChannelId: chMaint.Id,
			ExpireAt: expireAt,
		}
		allow, err = confirmTopUpUnpaidBeforeExpireTx(db, nil, extMaintZero, expireAt+120)
		require.NoError(t, err)
		assert.True(t, allow, "maintenance epay channel must allow zero-gift expiry")

		extMaintGift := &model.AdtyTopupOrder{
			TradeNo: "EPY_MAINT_GIFT", Provider: model.PaymentProviderAdtyEpay, ChannelId: chMaint.Id,
			GiftReserveEarning: decimal.NewFromInt(2), ExpireAt: expireAt,
		}
		allow, err = confirmTopUpUnpaidBeforeExpireTx(db, nil, extMaintGift, expireAt+120)
		require.NoError(t, err)
		assert.False(t, allow, "maintenance epay channel with site gift reserve should still confirm within 24h")

		// Enabled channel zero-gift: requires confirm during initial 30m window, expires at >= 30m.
		extEnabledZero := &model.AdtyTopupOrder{
			TradeNo: "EPY_EN_ZERO", Provider: model.PaymentProviderAdtyEpay, ChannelId: chEnabled.Id,
			ExpireAt: expireAt,
		}
		allow, err = confirmTopUpUnpaidBeforeExpireTx(db, nil, extEnabledZero, expireAt+120)
		require.NoError(t, err)
		assert.False(t, allow, "enabled epay zero-gift within 30m still requires remote unpaid confirm")

		allow, err = confirmTopUpUnpaidBeforeExpireTx(db, nil, extEnabledZero, expireAt+30*60)
		require.NoError(t, err)
		assert.True(t, allow, "enabled epay zero-gift past 30m should expire cleanly")

		// Enabled channel with site gift reserve: blocked at 30m, allowed at 24h cap.
		extEnabledGift := &model.AdtyTopupOrder{
			TradeNo: "EPY_EN_GIFT", Provider: model.PaymentProviderAdtyEpay, ChannelId: chEnabled.Id,
			GiftReserveMargin: decimal.NewFromInt(3), ExpireAt: expireAt,
		}
		allow, err = confirmTopUpUnpaidBeforeExpireTx(db, nil, extEnabledGift, expireAt+30*60)
		require.NoError(t, err)
		assert.False(t, allow, "enabled epay with site gift reserve must stay protected at 30m")

		allow, err = confirmTopUpUnpaidBeforeExpireTx(db, nil, extEnabledGift, expireAt+24*3600)
		require.NoError(t, err)
		assert.True(t, allow, "enabled epay with site gift reserve should expire at 24h max-age cap")
	})
}

func TestSiteGiftSpendReverseAndAdjustmentReversalFixes(t *testing.T) {
	db := setupGiftFulfillmentFixTestDB(t)
	now := time.Now().Unix()

	owner := model.User{Id: 10, Username: "owner10", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	customer := model.User{Id: 20, Username: "cust20", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, SiteId: 1}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&customer).Error)
	site := model.AdtySite{Id: 1, OwnerUserId: owner.Id, Status: model.AdtySiteStatusActive, Subdomain: "sub1"}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, EnsureAdtySiteWallets(site.Id))

	// Fund site earnings wallet with $50.
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(50), AdtySiteWalletChange{
		Type: "seed", RefType: "test", RefID: "seed-1", IdempotencyKey: "seed:earn:1",
	})
	require.NoError(t, err)

	// P2-7: Deferred gift shrunk on partial refund from $10 (5M quota) to $5 (2.5M quota), then fulfilled.
	tradeNo := "SHRUNK_GIFT_ORDER"
	siteIDPtr := site.Id
	ext := model.AdtyTopupOrder{
		TradeNo:               tradeNo,
		UserId:                customer.Id,
		SiteId:                site.Id,
		SiteOwnerUserId:       owner.Id,
		FaceUSD:               decimal.NewFromInt(100),
		BonusUSD:              decimal.NewFromInt(10),
		CreditQuota:           55_000_000,
		GiftFulfillmentStatus: model.AdtyGiftFulfillmentFulfilled,
		CreatedAt:             now,
	}
	require.NoError(t, db.Create(&ext).Error)
	require.NoError(t, db.Create(&model.AdtyGiftFulfillment{
		TradeNo:    tradeNo,
		SiteId:     site.Id,
		UserId:     customer.Id,
		Status:     model.AdtyGiftFulfillmentFulfilled,
		BonusUSD:   decimal.NewFromInt(5),
		BonusQuota: 2_500_000,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyFundBatch{
		UserId:       customer.Id,
		SiteId:       &siteIDPtr,
		Kind:         model.AdtyFundBatchKindBonus,
		GiftCategory: model.AdtyGiftCategoryPermanent,
		Source:       tradeNo,
		Original:     2_500_000,
		Remaining:    1_250_000, // 1.25M (50% of shrunk 2.5M) already spent
		Status:       model.AdtyFundBatchStatusActive,
	}).Error)
	// Hold $5 on earnings wallet matching the shrunk fulfilled guarantee.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := applyAdtySiteWalletChangeTx(tx, site.Id, model.AdtySiteWalletEarnings, decimal.Zero, AdtySiteWalletChange{
			Type: "gift_reserve", RefType: "topup", RefID: tradeNo, IdempotencyKey: "gift:" + tradeNo + ":earn",
			HoldDelta: decimal.NewFromInt(5),
		})
		return err
	}))

	// Spend 1.25M (50% of shrunk 2.5M basis) -> must spend $2.50, not $1.25.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return applySiteGiftSpendForSourceTx(tx, customer.Id, tradeNo, 1_250_000)
	}))
	var earnWallet model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earnWallet).Error)
	assert.True(t, earnWallet.Balance.Equal(decimal.RequireFromString("47.5")), "expected 47.5 after $2.50 gift_spend, got %s", earnWallet.Balance)
	assert.True(t, earnWallet.FrozenUSD.Equal(decimal.RequireFromString("2.5")), "expected 2.5 frozen after $2.50 gift_spend, got %s", earnWallet.FrozenUSD)

	// Reverse 625k (25% of shrunk 2.5M basis) -> must restore $1.25.
	require.NoError(t, db.Model(&model.AdtyFundBatch{}).
		Where("user_id = ? AND source = ? AND kind = ?", customer.Id, tradeNo, model.AdtyFundBatchKindBonus).
		Update("remaining", 1_875_000).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return reverseSiteGiftSpendForSourceTx(tx, customer.Id, "op-rev-1", tradeNo, 625_000)
	}))
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earnWallet).Error)
	assert.True(t, earnWallet.Balance.Equal(decimal.RequireFromString("48.75")), "expected 48.75 after $1.25 restore, got %s", earnWallet.Balance)
	assert.True(t, earnWallet.FrozenUSD.Equal(decimal.RequireFromString("3.75")), "expected 3.75 frozen after $1.25 restore, got %s", earnWallet.FrozenUSD)

	// P2-11: Direct Root bonus adjustment release books AdtySiteEarningBonusReversal for released guarantee.
	adjTrade := "ADJ_BONUS_ORDER"
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo:         adjTrade,
		UserId:          customer.Id,
		SiteId:          site.Id,
		SiteOwnerUserId: owner.Id,
		FaceUSD:         decimal.NewFromInt(100),
		BonusUSD:        decimal.NewFromInt(10),
		CreditQuota:     55_000_000,
		CreatedAt:       now,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyFundBatch{
		UserId:       customer.Id,
		SiteId:       &siteIDPtr,
		Kind:         model.AdtyFundBatchKindBonus,
		GiftCategory: model.AdtyGiftCategoryPermanent,
		Source:       adjTrade,
		Original:     5_000_000,
		Remaining:    5_000_000,
		Status:       model.AdtyFundBatchStatusActive,
	}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := applyAdtySiteWalletChangeTx(tx, site.Id, model.AdtySiteWalletEarnings, decimal.Zero, AdtySiteWalletChange{
			Type: "gift_reserve", RefType: "topup", RefID: adjTrade, IdempotencyKey: "gift:" + adjTrade + ":earn",
			HoldDelta: decimal.NewFromInt(10),
		})
		return err
	}))

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.ReduceAdtyAdjustmentBatchTx(tx, customer.Id, site.Id, adjTrade, model.AdtyFundBatchKindBonus, 2_500_000)
	}))

	var reversals []model.AdtySiteEarningDaily
	require.NoError(t, db.Where("site_id = ? AND user_id = ? AND kind = ?", site.Id, customer.Id, model.AdtySiteEarningBonusReversal).Find(&reversals).Error)
	require.Len(t, reversals, 1)
	assert.True(t, reversals[0].Earning.Equal(decimal.NewFromInt(5)), "expected +5 bonus_reversal on direct adjustment release, got %s", reversals[0].Earning)
}
