package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFTRADTY0801DollarLedgerPG books a paid alipay order in USD.
// Face 100 USD plus the 0.02 alipay bonus is 102 USD, which is 51000000 quota.
func TestFTRADTY0801DollarLedgerPG(t *testing.T) {
	db := prepareEpayDB(t)
	pgtest.RecordServerVersion(t, db)
	ftrStoreMainAlipayBonus(t, db)
	buyer := ftrPaymentBuyer(t, db, "ftr-0801-buyer")
	other := ftrPaymentBuyer(t, db, "ftr-0801-other")
	channel := seedEpayChannel(t, db, model.AdtyEpayChannel{
		Name: "ftr-0801", GatewayURL: "https://pay.example.test", MerchantId: "merchant-0801",
	}, alipay("qrcode"))
	stored := ftrCreateEpayAlipayOrder(t, db, buyer, channel, "ledger")

	facts := model.AdtyPaymentFacts{
		Provider:       model.PaymentProviderAdtyEpay,
		Method:         "alipay",
		PaidAmount:     decimal.NewFromInt(730),
		PaidCurrency:   "CNY",
		GatewayTradeId: "gw-0801",
		TxHash:         "gw-0801",
		Source:         model.AdtyPaymentSourceCallback,
	}
	already, err := model.CompleteAdtyTopUp(stored.TradeNo, facts, "127.0.0.1")
	require.NoError(t, err)
	assert.False(t, already)

	var paid model.TopUp
	require.NoError(t, db.Where("trade_no = ?", stored.TradeNo).First(&paid).Error)
	assert.Equal(t, buyer.Id, paid.UserId)
	assert.Equal(t, common.TopUpStatusSuccess, paid.Status)
	assert.Equal(t, common.RoleCommonUser, buyer.Role)

	const creditedQuota = 51000000
	const walletAfter = ftrPaymentQuota + creditedQuota
	var quota int
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", buyer.Id).Select("quota").Scan(&quota).Error)
	assert.Equal(t, walletAfter, quota)
	assert.True(t, QuotaToUSD(creditedQuota).Equal(decimal.NewFromInt(102)), "51000000 quota is 102 USD at 1 USD = 500000 quota")

	var ledgers []model.AdtyBalanceLedger
	require.NoError(t, db.Where("user_id = ? AND ref_id = ?", buyer.Id, stored.TradeNo).Find(&ledgers).Error)
	require.Len(t, ledgers, 1)
	assert.Equal(t, model.AdtyLedgerTypeTopUp, ledgers[0].Type)
	assert.EqualValues(t, creditedQuota, ledgers[0].DeltaQuota)
	assert.EqualValues(t, ftrPaymentQuota, ledgers[0].BalanceBefore)
	assert.EqualValues(t, walletAfter, ledgers[0].BalanceAfter)
	assert.True(t, QuotaToUSD(ledgers[0].DeltaQuota).Equal(decimal.NewFromInt(102)))

	replayed, err := model.CompleteAdtyTopUp(stored.TradeNo, facts, "127.0.0.1")
	require.NoError(t, err)
	assert.True(t, replayed)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", buyer.Id).Select("quota").Scan(&quota).Error)
	assert.Equal(t, walletAfter, quota, "replaying the same payment must not credit the 102 USD again")
	var ledgerCount int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Where("user_id = ? AND ref_id = ?", buyer.Id, stored.TradeNo).Count(&ledgerCount).Error)
	assert.EqualValues(t, 1, ledgerCount)

	var otherQuota int
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", other.Id).Select("quota").Scan(&otherQuota).Error)
	assert.Equal(t, ftrPaymentQuota, otherQuota)
	var otherLedgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Where("user_id = ?", other.Id).Count(&otherLedgers).Error)
	assert.Zero(t, otherLedgers)
}
