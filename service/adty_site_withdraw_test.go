package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestSiteOwnerWithdrawalRefusesWhenRulesUnset(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "withdraw-unset")
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.RequireFromString("50"), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-unset",
	})
	require.NoError(t, err)
	_, err = CreateAdtySiteWithdrawal(AdtySiteWithdrawInput{
		SiteID: site.Id, OwnerID: owner.Id, AmountUSD: decimal.RequireFromString("20"),
		Method: "alipay", AccountName: "Ada", AccountNo: "ada@example.com",
		ExpectedRate: decimal.RequireFromString("7.2"), IdempotencyKey: "wd-unset",
	})
	require.ErrorIs(t, err, ErrAdtySiteWithdrawUnset)
}

func TestSiteOwnerWithdrawalFreezesEarningsAndRejectsRelease(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "withdraw-ok")
	require.NoError(t, SaveAdtySiteWithdrawRules(AdtySiteWithdrawRules{
		MinUSD: "10", FeeUSD: "1", AlipayCNYRate: "7.20", USDTUSDRate: "1",
	}, owner.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.RequireFromString("50"), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-ok",
	})
	require.NoError(t, err)
	row, err := CreateAdtySiteWithdrawal(AdtySiteWithdrawInput{
		SiteID: site.Id, OwnerID: owner.Id, AmountUSD: decimal.RequireFromString("20"),
		Method: "alipay", AccountName: "Ada", AccountNo: "ada@example.com",
		ExpectedRate: decimal.RequireFromString("7.20"), IdempotencyKey: "wd-ok",
	})
	require.NoError(t, err)
	require.Equal(t, "pending", row.Status)
	require.Equal(t, "7.20", row.Rate)
	require.True(t, row.FeeUSD.Equal(decimal.RequireFromString("1")))
	var earning model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.RequireFromString("50")), "ISS-R1-0018: balance stays 50 while frozen")
	require.True(t, earning.FrozenUSD.Equal(decimal.RequireFromString("20")), "ISS-R1-0018: frozen increases by 20")

	require.NoError(t, MarkAdtySiteWithdrawalPaying(row.Id, 1))
	require.ErrorIs(t, CancelAdtySiteWithdrawal(site.Id, owner.Id, row.Id), ErrAdtySiteWithdrawLocked)
	require.NoError(t, RejectAdtySiteWithdrawal(row.Id, 1, "bad account"))
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.RequireFromString("50")))
	require.True(t, earning.FrozenUSD.IsZero(), "ISS-R1-0018: reject releases freeze without mutating balance")
}

func TestSiteOwnerWithdrawalPaidKeepsFreezeAndRecordsProof(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "withdraw-paid")
	require.NoError(t, SaveAdtySiteWithdrawRules(AdtySiteWithdrawRules{
		MinUSD: "10", FeeUSD: "1", AlipayCNYRate: "7.20", USDTUSDRate: "1.00",
	}, owner.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.RequireFromString("40"), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-paid",
	})
	require.NoError(t, err)
	row, err := CreateAdtySiteWithdrawal(AdtySiteWithdrawInput{
		SiteID: site.Id, OwnerID: owner.Id, AmountUSD: decimal.RequireFromString("12"),
		Method: "usdt", Network: "TRC20", AccountNo: "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb",
		ExpectedRate: decimal.RequireFromString("1.00"), IdempotencyKey: "wd-paid",
	})
	require.NoError(t, err)
	require.NoError(t, MarkAdtySiteWithdrawalPaying(row.Id, 9))
	require.NoError(t, MarkAdtySiteWithdrawalPaid(row.Id, 9, "txid-1", "manual payout"))
	var saved model.AdtySiteWithdrawal
	require.NoError(t, db.First(&saved, row.Id).Error)
	require.Equal(t, "paid", saved.Status)
	require.Equal(t, "txid-1", saved.TxID)
	var earning model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.RequireFromString("28")))
	require.True(t, earning.FrozenUSD.IsZero(), "ISS-R1-0018: paid settles freeze into spent")
	var paidLedger model.AdtySiteWalletLedger
	require.NoError(t, db.Where("site_id = ? AND type = ? AND ref_id = ?", site.Id, "earning_withdraw_paid", "wd-paid").First(&paidLedger).Error)
	require.True(t, paidLedger.Delta.Equal(decimal.RequireFromString("-12")))
	require.True(t, paidLedger.HoldDelta.Equal(decimal.RequireFromString("-12")))
}

func TestSiteOwnerWithdrawalIdempotencyRejectsPayloadMutation(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "withdraw-idem")
	require.NoError(t, SaveAdtySiteWithdrawRules(AdtySiteWithdrawRules{
		MinUSD: "10", FeeUSD: "1", AlipayCNYRate: "7.20", USDTUSDRate: "1.00",
	}, owner.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.RequireFromString("50"), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-idem",
	})
	require.NoError(t, err)
	base := AdtySiteWithdrawInput{
		SiteID: site.Id, OwnerID: owner.Id, AmountUSD: decimal.RequireFromString("15"),
		Method: "usdt", Network: "TRC20", AccountNo: "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb",
		ExpectedRate: decimal.RequireFromString("1.00"), IdempotencyKey: "wd-idem-1",
	}
	first, err := CreateAdtySiteWithdrawal(base)
	require.NoError(t, err)
	replay, err := CreateAdtySiteWithdrawal(base)
	require.NoError(t, err)
	require.Equal(t, first.Id, replay.Id)

	diffAddr := base
	diffAddr.AccountNo = "TNPeeaaFB7K9cmo4uQpcU32zGK8G1NYqeL"
	_, err = CreateAdtySiteWithdrawal(diffAddr)
	require.ErrorIs(t, err, model.ErrAdtyIdempotencyConflict)

	diffNet := base
	diffNet.Network = "BEP20"
	diffNet.AccountNo = "0x1111111111111111111111111111111111111111"
	_, err = CreateAdtySiteWithdrawal(diffNet)
	require.ErrorIs(t, err, model.ErrAdtyIdempotencyConflict)
}
