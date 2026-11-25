package service

import (
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupPaymentSceneBonusTestDB(t *testing.T) *gorm.DB {
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
		&model.AdtyPaymentSceneConfig{},
		&model.AdtyEpayChannel{},
		&model.AdtyEpayChannelMethod{},
		&model.AdtySite{},
		&model.AdtySiteOwnership{},
		&model.AdtySiteWallet{},
		&model.AdtySiteWalletLedger{},
		&model.AdtySiteTier{},
		&model.AdtySiteInvite{},
		&model.AdtySiteReferralRate{},
		&model.AdtySiteInviteCommission{},
		&model.AdtyTeamMember{},
		&model.AdtyAnomalyDisposition{},
		&model.AuditIntent{},
		&model.AuditLog{},
	))

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedis := common.RedisEnabled
	oldMemCache := common.MemoryCacheEnabled
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	model.DB = db
	model.LOG_DB = db
	adty_setting.SetAuthoritativeBonusDB(db)
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		adty_setting.SetAuthoritativeBonusDB(oldDB)
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		_ = sqlDB.Close()
	})
	return db
}

func TestCreateAdtyPaymentOrderSkipsBonusForPausedAndClosingSubsites(t *testing.T) {
	db := setupPaymentSceneBonusTestDB(t)

	scenesPayload, err := common.Marshal([]model.AdtyPaymentScene{
		{
			Key:        model.AdtyBusinessWalletTopup,
			Name:       "Wallet Top-Up",
			Business:   model.AdtyBusinessWalletTopup,
			Enabled:    true,
			MinUSD:     decimal.NewFromInt(1),
			MaxUSD:     decimal.NewFromInt(10000),
			Providers:  []string{model.PaymentProviderAdtyEpay},
			Methods:    []string{AdtyMethodAlipay},
			Currencies: []string{"CNY"},
			SiteScope:  "all",
		},
	})
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.AdtyPaymentSceneConfig{
		ID:      1,
		Version: 1,
		Payload: string(scenesPayload),
	}).Error)

	channel := &model.AdtyEpayChannel{
		Name:        "epay-1",
		GatewayURL:  "https://pay.example.com",
		MerchantId:  "1001",
		MerchantKey: "secret",
		SignType:    model.AdtyEpaySignTypeMD5,
		OrderMode:   model.AdtyEpayOrderModeAPI,
		Status:      model.AdtyEpayStatusEnabled,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.AdtyEpayChannelMethod{
		ChannelId:   channel.Id,
		Method:      AdtyMethodAlipay,
		TypeCode:    "alipay",
		DisplayName: "Alipay",
		Enabled:     true,
		MinCNY:      decimal.NewFromInt(1),
		MaxCNY:      decimal.NewFromInt(100000),
	}).Error)
	require.NoError(t, db.Preload("Methods").First(channel, channel.Id).Error)

	// Seed site method bonus for sites 1 (active), 2 (paused), 3 (closing).
	require.NoError(t, db.Create(&model.Option{
		Key:   adty_setting.TopupSettingKeySiteMethodBonus,
		Value: `{"1":{"alipay":{"enabled":true,"rate":"0.1"}},"2":{"alipay":{"enabled":true,"rate":"0.1"}},"3":{"alipay":{"enabled":true,"rate":"0.1"}}}`,
	}).Error)

	cases := []struct {
		name           string
		siteID         int
		ownerID        int
		customerID     int
		status         string
		expectBonusUSD decimal.Decimal
		expectReserved decimal.Decimal
	}{
		{
			name:           "active subsite applies bonus and reserves gift",
			siteID:         1,
			ownerID:        101,
			customerID:     201,
			status:         model.AdtySiteStatusActive,
			expectBonusUSD: decimal.NewFromInt(10),
			expectReserved: decimal.NewFromInt(10),
		},
		{
			name:           "paused subsite gets zero bonus and zero reserve",
			siteID:         2,
			ownerID:        102,
			customerID:     202,
			status:         model.AdtySiteStatusPaused,
			expectBonusUSD: decimal.Zero,
			expectReserved: decimal.Zero,
		},
		{
			name:           "closing subsite gets zero bonus and zero reserve",
			siteID:         3,
			ownerID:        103,
			customerID:     203,
			status:         model.AdtySiteStatusClosing,
			expectBonusUSD: decimal.Zero,
			expectReserved: decimal.Zero,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, db.Create(&model.User{
				Id: tc.ownerID, Username: "owner_" + tc.status, Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
			}).Error)
			require.NoError(t, db.Create(&model.User{
				Id: tc.customerID, Username: "cust_" + tc.status, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, SiteId: tc.siteID,
			}).Error)
			require.NoError(t, db.Create(&model.AdtySite{
				Id: tc.siteID, OwnerUserId: tc.ownerID, Status: tc.status, Subdomain: "sub_" + tc.status,
			}).Error)
			require.NoError(t, EnsureAdtySiteWallets(tc.siteID))
			_, err := ApplyAdtySiteWalletChange(tc.siteID, model.AdtySiteWalletEarnings, decimal.NewFromInt(100), AdtySiteWalletChange{
				Type: "seed", RefType: "test", RefID: tc.status, IdempotencyKey: "seed:" + tc.status,
			})
			require.NoError(t, err)

			tradeNo := "ORD_" + strings.ToUpper(tc.status)
			topUp := &model.TopUp{
				UserId:          tc.customerID,
				Amount:          100,
				Money:           700,
				TradeNo:         tradeNo,
				PaymentMethod:   AdtyMethodAlipay,
				PaymentProvider: model.PaymentProviderAdtyEpay,
				CreateTime:      time.Now().Unix(),
				Status:          common.TopUpStatusPending,
			}
			ext := &model.AdtyTopupOrder{
				Business:    model.AdtyBusinessWalletTopup,
				TradeNo:     tradeNo,
				UserId:      tc.customerID,
				SiteId:      tc.siteID,
				Provider:    model.PaymentProviderAdtyEpay,
				Method:      AdtyMethodAlipay,
				ChannelId:   channel.Id,
				FaceUSD:     decimal.NewFromInt(100),
				PayAmount:   decimal.NewFromInt(700),
				PayCurrency: "CNY",
				ExpireAt:    time.Now().Unix() + 900,
			}
			err = CreateAdtyPaymentOrder(topUp, ext, model.AdtyBusinessWalletTopup, channel)
			require.NoError(t, err)

			assert.True(t, ext.BonusUSD.Equal(tc.expectBonusUSD), "expected BonusUSD=%s got=%s", tc.expectBonusUSD, ext.BonusUSD)
			assert.True(t, ext.GiftReserveEarning.Equal(tc.expectReserved), "expected GiftReserveEarning=%s got=%s", tc.expectReserved, ext.GiftReserveEarning)

			var earn model.AdtySiteWallet
			require.NoError(t, db.Where("site_id = ? AND kind = ?", tc.siteID, model.AdtySiteWalletEarnings).First(&earn).Error)
			assert.True(t, earn.FrozenUSD.Equal(tc.expectReserved), "expected wallet FrozenUSD=%s got=%s", tc.expectReserved, earn.FrozenUSD)
		})
	}
}
