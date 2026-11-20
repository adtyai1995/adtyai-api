package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ftrPinCNYQuote pins the opened quote case: USD display, Price 7.3, minimum 1.
// Credit still uses the fixed 500000 scale, not the float QuotaPerUnit.
func ftrPinCNYQuote(t *testing.T) {
	t.Helper()
	general := operation_setting.GetGeneralSetting()
	oldDisplay := general.QuotaDisplayType
	oldPrice, oldMin := operation_setting.Price, operation_setting.MinTopUp
	general.QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	operation_setting.Price = 7.3
	operation_setting.MinTopUp = 1
	ClearAdtyMethodBonusForTest(t)
	t.Cleanup(func() {
		general.QuotaDisplayType = oldDisplay
		operation_setting.Price, operation_setting.MinTopUp = oldPrice, oldMin
	})
}

func ftrAssertCNYQuote(t *testing.T, method string) {
	t.Helper()
	db := ftrRound1PG(t)
	user := ftrRound1User(t, db, "ftr-cny-"+method, common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, user.Role)
	require.Equal(t, "default", user.Group)
	ftrRound1PinQuota(t, db, user.Id)
	ftrPinCNYQuote(t)

	quote, err := AdtyQuoteForSite(nil, user.Group, 100, method)
	require.NoError(t, err)
	assert.True(t, quote.FaceUSD.Equal(decimal.NewFromInt(100)))
	assert.Equal(t, AdtyCurrencyCNY, quote.PayCurrency)
	assert.Equal(t, "7.3", quote.PriceCNYPerUSD.String())
	assert.Equal(t, "730.00", quote.PayAmount.StringFixed(2))
	assert.EqualValues(t, 50_000_000, quote.CreditQuota)
	assert.True(t, quote.BonusUSD.IsZero())
	ftrRound1AssertNoMoney(t, db, user.Id)
}

// ADTY-08-04: alipay payable CNY is FaceUSD 100 times Price 7.3.
// The checkout route is POST /api/adty/topup/quote; this case calls the quote function that route uses.
func TestFTRADTY0804AlipayCNYQuotePG(t *testing.T) {
	ftrAssertCNYQuote(t, AdtyMethodAlipay)
}

// ADTY-08-05: wechat uses the same CNY formula on its own call.
// The checkout route is POST /api/adty/topup/quote; this case calls the quote function that route uses.
func TestFTRADTY0805WechatCNYQuotePG(t *testing.T) {
	ftrAssertCNYQuote(t, AdtyMethodWxpay)
}
