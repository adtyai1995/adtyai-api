package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFTRADTY0906PaymentBonusSnapshotPG(t *testing.T) {
	db := prepareEpayDB(t)
	pgtest.RecordServerVersion(t, db)
	ftrStoreMainAlipayBonus(t, db)
	buyer := ftrPaymentBuyer(t, db, "ftr-0906-buyer")
	channel := seedEpayChannel(t, db, model.AdtyEpayChannel{
		Name: "ftr-0906", GatewayURL: "https://pay.example.test", MerchantId: "merchant-0906",
	}, alipay("qrcode"))

	stored := ftrCreateEpayAlipayOrder(t, db, buyer, channel, "snapshot")
	var snapshot struct {
		BonusAuthorized bool            `json:"bonus_authorized"`
		BonusEnabled    bool            `json:"bonus_enabled"`
		BonusMethod     string          `json:"bonus_method"`
		BonusRate       decimal.Decimal `json:"bonus_rate"`
		BonusUSD        decimal.Decimal `json:"bonus_usd"`
		FaceUSD         decimal.Decimal `json:"face_usd"`
		MarginAllowed   bool            `json:"margin_allowed"`
	}
	require.NoError(t, common.UnmarshalJsonStr(stored.PaymentSceneSnapshot, &snapshot))
	assert.True(t, snapshot.BonusAuthorized)
	assert.True(t, snapshot.BonusEnabled)
	assert.Equal(t, "alipay", snapshot.BonusMethod)
	assert.Equal(t, "0.02", snapshot.BonusRate.String())
	assert.Equal(t, "2", snapshot.BonusUSD.String())
	assert.Equal(t, "100", snapshot.FaceUSD.String())
	assert.False(t, snapshot.MarginAllowed)
	assert.Equal(t, stored.FaceUSD.Mul(stored.BonusRate).Round(8).String(), stored.BonusUSD.String())

	teamLeader := ftrPaymentBuyer(t, db, "ftr-0906-leader")
	created, err := CreateAdtyTeam(teamLeader.Id, 0, "FTR0906", decimal.NewFromInt(1))
	require.NoError(t, err)
	current, err := model.GetAdtyEpayChannel(channel.Id)
	require.NoError(t, err)
	top, ext := paymentSceneOrder(teamLeader.Id, "team-bonus")
	top.PaymentProvider, top.PaymentMethod, top.Money = model.PaymentProviderAdtyEpay, "alipay", 730
	ext.Provider, ext.Method, ext.ChannelId = model.PaymentProviderAdtyEpay, "alipay", channel.Id
	ext.SiteId = 0
	ext.TeamId = created.Team.Id
	ext.PayCurrency, ext.PayAmount = "CNY", decimal.NewFromInt(730)
	ext.FaceUSD = decimal.NewFromInt(100)
	require.NoError(t, CreateAdtyPaymentOrder(top, ext, "wallet_topup", current))
	var teamOrder model.AdtyTopupOrder
	require.NoError(t, db.Where("trade_no = ?", top.TradeNo).First(&teamOrder).Error)
	assert.Equal(t, created.Team.Id, teamOrder.TeamId)
	assert.True(t, teamOrder.BonusUSD.IsZero())
	assert.True(t, teamOrder.BonusRate.IsZero())
	assert.EqualValues(t, 50_000_000, teamOrder.CreditQuota)
	var teamSnapshot struct {
		BonusAuthorized bool            `json:"bonus_authorized"`
		BonusEnabled    bool            `json:"bonus_enabled"`
		BonusUSD        decimal.Decimal `json:"bonus_usd"`
		FaceUSD         decimal.Decimal `json:"face_usd"`
	}
	require.NoError(t, common.UnmarshalJsonStr(teamOrder.PaymentSceneSnapshot, &teamSnapshot))
	assert.False(t, teamSnapshot.BonusAuthorized)
	assert.False(t, teamSnapshot.BonusEnabled)
	assert.True(t, teamSnapshot.BonusUSD.IsZero())
	assert.Equal(t, "100", teamSnapshot.FaceUSD.String())
	var leaderQuota int
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", teamLeader.Id).Select("quota").Scan(&leaderQuota).Error)
	assert.Equal(t, ftrPaymentQuota, leaderQuota)
}

func TestFTRADTY0907SiteGiftReserveSourcePG(t *testing.T) {
	db := prepareSiteDB(t)
	pgtest.RecordServerVersion(t, db)
	_, site := openActiveSite(t, db, "ftr0907")
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.RequireFromString("4"), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "ftr0907-earn",
	})
	require.NoError(t, err)
	require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.RequireFromString("20"), "ftr0907-cycle"))
	adty_setting.AuthorizeSiteBonusMarginFill(site.Id, true)
	t.Cleanup(func() { adty_setting.AuthorizeSiteBonusMarginFill(site.Id, false) })

	ext := &model.AdtyTopupOrder{
		TradeNo: "FTR0907-GIFT", SiteId: site.Id, BonusUSD: decimal.RequireFromString("10"),
		Method: "usdt.trc20", BonusMethod: "usdt", BonusExpiresAt: 0,
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return ReserveAdtySiteGiftTx(tx, ext)
	}))
	require.Equal(t, "earning+margin", ext.GiftReserveSource)
	require.True(t, ext.GiftReserveEarning.Equal(decimal.RequireFromString("4")))
	require.True(t, ext.GiftReserveMargin.Equal(decimal.RequireFromString("6")))
	earning, margin := mustSiteGiftWallets(t, db, site.Id)
	require.True(t, earning.Balance.Equal(decimal.RequireFromString("4")))
	require.True(t, earning.FrozenUSD.Equal(decimal.RequireFromString("4")))
	require.True(t, margin.Balance.Equal(decimal.RequireFromString("20")))
	require.True(t, margin.FrozenUSD.Equal(decimal.RequireFromString("6")))
	assertGiftLedger(t, db, site.Id, "gift_reserve", "FTR0907-GIFT", model.AdtySiteWalletEarnings, "0", "4")
	assertGiftLedger(t, db, site.Id, "gift_reserve", "FTR0907-GIFT", model.AdtySiteWalletMargin, "0", "6")
}
