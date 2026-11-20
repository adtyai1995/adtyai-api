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

// ftrPaymentQuota is the buyer wallet before checkout. 1 USD = 500000 quota.
// Creating a payment order must not credit or debit it.
const ftrPaymentQuota = 500000

func ftrPaymentBuyer(t *testing.T, db *gorm.DB, name string) model.User {
	t.Helper()
	user := model.User{
		Username: name, Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", SiteId: 0, Quota: ftrPaymentQuota,
	}
	require.NoError(t, db.Create(&user).Error)
	return user
}

func ftrStoreMainAlipayBonus(t *testing.T, db *gorm.DB) {
	t.Helper()
	policy := map[string]string{
		adty_setting.TopupSettingKeyMethodBonus: `{"alipay":{"enabled":true,"rate":"0.02"}}`,
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.SaveOptionsTx(tx, policy)
	}))
}

func ftrCreateEpayAlipayOrder(t *testing.T, db *gorm.DB, buyer model.User, channel *model.AdtyEpayChannel, ref string) model.AdtyTopupOrder {
	t.Helper()
	current, err := model.GetAdtyEpayChannel(channel.Id)
	require.NoError(t, err)
	top, ext := paymentSceneOrder(buyer.Id, ref)
	top.PaymentProvider, top.PaymentMethod, top.Money = model.PaymentProviderAdtyEpay, "alipay", 730
	ext.Provider, ext.Method, ext.ChannelId = model.PaymentProviderAdtyEpay, "alipay", channel.Id
	ext.SiteId = 0
	ext.PayCurrency, ext.PayAmount = "CNY", decimal.NewFromInt(730)
	ext.FaceUSD = decimal.NewFromInt(100)
	require.NoError(t, CreateAdtyPaymentOrder(top, ext, "wallet_topup", current))

	var stored model.AdtyTopupOrder
	require.NoError(t, db.Where("trade_no = ?", top.TradeNo).First(&stored).Error)
	assert.Equal(t, buyer.Id, stored.UserId)
	assert.Equal(t, 0, stored.SiteId)
	assert.Equal(t, channel.Id, stored.ChannelId)
	assert.Equal(t, "alipay", stored.Method)
	assert.Equal(t, "alipay", stored.BonusMethod)
	assert.Equal(t, "0.02", stored.BonusRate.String())
	assert.Equal(t, "2", stored.BonusUSD.String())
	assert.Equal(t, "100", stored.FaceUSD.String())
	assert.EqualValues(t, 51000000, stored.CreditQuota)
	assert.Equal(t, common.TopUpStatusPending, topUpStatusOf(t, db, top.TradeNo))

	var quota int
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", buyer.Id).Select("quota").Scan(&quota).Error)
	assert.Equal(t, ftrPaymentQuota, quota, "order create must leave the 500000 quota wallet unchanged")
	return stored
}

func TestFTRADTY0806PaymentOrderCreatePG(t *testing.T) {
	db := prepareEpayDB(t)
	pgtest.RecordServerVersion(t, db)
	ftrStoreMainAlipayBonus(t, db)
	buyer := ftrPaymentBuyer(t, db, "ftr-0806-buyer")
	other := ftrPaymentBuyer(t, db, "ftr-0806-other")
	channel := seedEpayChannel(t, db, model.AdtyEpayChannel{
		Name: "ftr-0806", GatewayURL: "https://pay.example.test", MerchantId: "merchant-0806",
	}, alipay("qrcode"))

	stored := ftrCreateEpayAlipayOrder(t, db, buyer, channel, "create")
	assert.Equal(t, buyer.Role, common.RoleCommonUser)
	assert.NotEqual(t, other.Id, stored.UserId)

	var otherOrders int64
	require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("user_id = ?", other.Id).Count(&otherOrders).Error)
	assert.Zero(t, otherOrders)
	var otherQuota int
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", other.Id).Select("quota").Scan(&otherQuota).Error)
	assert.Equal(t, ftrPaymentQuota, otherQuota)
}

func TestFTRADTY0905TwoEpayMerchantsShareOneBonusRulePG(t *testing.T) {
	db := prepareEpayDB(t)
	pgtest.RecordServerVersion(t, db)
	ftrStoreMainAlipayBonus(t, db)
	buyer := ftrPaymentBuyer(t, db, "ftr-0905-buyer")
	first := seedEpayChannel(t, db, model.AdtyEpayChannel{
		Name: "merchant-a", GatewayURL: "https://a.example.test", MerchantId: "merchant-a",
	}, alipay("qrcode"))
	second := seedEpayChannel(t, db, model.AdtyEpayChannel{
		Name: "merchant-b", GatewayURL: "https://b.example.test", MerchantId: "merchant-b",
	}, alipay("qrcode"))
	require.NotEqual(t, first.Id, second.Id)
	require.NotEqual(t, first.MerchantId, second.MerchantId)

	left := ftrCreateEpayAlipayOrder(t, db, buyer, first, "merchant-a")
	right := ftrCreateEpayAlipayOrder(t, db, buyer, second, "merchant-b")
	assert.Equal(t, left.BonusRate.String(), right.BonusRate.String())
	assert.Equal(t, left.BonusUSD.String(), right.BonusUSD.String())
	assert.Equal(t, "0.02", left.BonusRate.String())
	assert.Equal(t, "2", right.BonusUSD.String())
	assert.NotEqual(t, left.ChannelId, right.ChannelId)

	var quota int
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", buyer.Id).Select("quota").Scan(&quota).Error)
	assert.Equal(t, ftrPaymentQuota, quota, "two merchant checkouts still leave 500000 quota unchanged")
}
