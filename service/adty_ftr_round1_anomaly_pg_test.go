package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY2108DuplicateCreditAndRefundPG(t *testing.T) {
	db := prepareSiteDB(t)
	pgtest.RecordServerVersion(t, db)
	_, site := openActiveSite(t, db, "ftr2108")
	require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{TradeNo: "ftr2108-credit", SiteId: site.Id, UserId: 1, Provider: "epay", Source: "callback", Outcome: "credited"}).Error)
	require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{TradeNo: "ftr2108-credit", SiteId: site.Id, UserId: 1, Provider: "epay", Source: "callback", Outcome: "credited"}).Error)
	require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{TradeNo: "ftr2108-once", SiteId: site.Id, UserId: 1, Provider: "epay", Source: "callback", Outcome: "credited"}).Error)
	require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{TradeNo: "ftr2108-once", SiteId: site.Id, UserId: 1, Provider: "epay", Source: "callback", Outcome: "replayed"}).Error)
	require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{TradeNo: "ftr2108-refund", SiteId: site.Id, UserId: 1, Provider: "epay", Source: "callback", Outcome: "received", EventType: "refund.succeeded"}).Error)
	require.NoError(t, db.Create(&model.AdtyOrderPaymentEvent{TradeNo: "ftr2108-refund", SiteId: site.Id, UserId: 1, Provider: "epay", Source: "callback", Outcome: "received", EventType: "refund.succeeded"}).Error)

	kinds, err := CollectAdtyFundAnomalies(site.Id)
	require.NoError(t, err)
	require.Contains(t, kinds, AdtyAnomalyDuplicateCredit)
	require.Contains(t, kinds, AdtyAnomalyDuplicateRefund)
}

func TestFTRADTY2109GiftAndMarginAnomalyPG(t *testing.T) {
	db := prepareSiteDB(t)
	pgtest.RecordServerVersion(t, db)
	_, marginSite := openActiveSite(t, db, "ftr2109-margin")
	require.NoError(t, OpenAdtyMarginCycle(marginSite.Id, decimal.RequireFromString("100"), "ftr2109-cycle"))
	require.NoError(t, db.Model(&model.AdtySiteWallet{}).Where("site_id = ? AND kind = ?", marginSite.Id, model.AdtySiteWalletMargin).Update("cycle_used_usd", "91").Error)
	marginKinds, err := CollectAdtyFundAnomalies(marginSite.Id)
	require.NoError(t, err)
	require.Contains(t, marginKinds, AdtyAnomalyMargin)

	_, giftSite := openActiveSite(t, db, "ftr2109-gift")
	customer := siteCustomer(t, db, giftSite, "ftr2109-buyer")
	trade := "FTR2109-SHORT"
	require.NoError(t, db.Create(&model.TopUp{
		UserId: customer.Id, Amount: 20, TradeNo: trade,
		PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		Status: common.TopUpStatusSuccess,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: trade, UserId: customer.Id, SiteId: giftSite.Id,
		Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
		FaceUSD: decimal.NewFromInt(20), BonusUSD: decimal.NewFromInt(3),
		CreditQuota: 11_500_000, GiftReserveEarning: decimal.NewFromInt(3),
	}).Error)
	require.NoError(t, db.Create(&model.AdtyFundBatch{
		UserId: customer.Id, SiteId: model.AdtyFundBatchSiteID(giftSite.Id),
		Kind: model.AdtyFundBatchKindBonus, Source: trade,
		Original: 1_500_000, Remaining: 1_000_000,
	}).Error)
	require.NoError(t, db.Create(&model.AdtySiteWalletLedger{
		SiteId: giftSite.Id, WalletKind: model.AdtySiteWalletEarnings, Type: "gift_reserve",
		RefType: "topup", RefId: trade, HoldDelta: decimal.RequireFromString("0.5"),
		IdempotencyKey: "ftr2109-gr",
	}).Error)

	gifts, err := detectInsufficientGiftGuarantees(giftSite.Id)
	require.NoError(t, err)
	require.Len(t, gifts, 1)
	require.Equal(t, AdtyAnomalyGiftGuaranteeShort, gifts[0].Kind)
	require.Equal(t, trade, gifts[0].Ref)
	require.Equal(t, "1.5", gifts[0].Difference)
}
