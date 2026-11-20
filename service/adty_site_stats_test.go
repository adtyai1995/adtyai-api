package service

import (
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetAdtySiteBalancesReadsSiteEarningsWalletAndSiteWithdrawals(t *testing.T) {
	db := prepareSiteDB(t)
	owner, site := openActiveSite(t, db, "stats-bal-"+strconv.FormatInt(time.Now().UnixNano()%1_000_000, 10))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))

	// Put unrelated money into the owner's personal ReferralCommissionAccount to prove GetAdtySiteBalances ignores it.
	affiliate, err := Referral.EnsureAffiliate(owner.Id)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.ReferralCommissionAccount{}).Where("affiliate_id = ?", affiliate.Id).Updates(map[string]any{
		"available_amount": 999,
		"frozen_amount":    888,
		"withdrawn_amount": 777,
	}).Error)

	// Credit $50 to the site's earnings wallet and freeze $12 on it.
	_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(50), AdtySiteWalletChange{
		Type: "site_earning_settlement", IdempotencyKey: "stats-earn-" + strconv.Itoa(site.Id),
	})
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := applyAdtySiteWalletChangeTx(tx, site.Id, model.AdtySiteWalletEarnings, decimal.Zero, AdtySiteWalletChange{
			Type: "earning_withdraw_freeze", HoldDelta: decimal.NewFromInt(12), IdempotencyKey: "stats-hold-" + strconv.Itoa(site.Id),
		})
		return err
	}))

	// Record one paid site withdrawal ($20) and one pending site withdrawal ($12).
	require.NoError(t, db.Create(&[]model.AdtySiteWithdrawal{
		{
			SiteId: site.Id, AmountUSD: decimal.NewFromInt(20), FeeUSD: decimal.NewFromInt(1), NetUSD: decimal.NewFromInt(19),
			Method: "usdt", Status: model.AdtySiteWithdrawPaid, IdempotencyKey: "stats-wd-paid-" + strconv.Itoa(site.Id),
		},
		{
			SiteId: site.Id, AmountUSD: decimal.NewFromInt(12), FeeUSD: decimal.NewFromInt(1), NetUSD: decimal.NewFromInt(11),
			Method: "usdt", Status: model.AdtySiteWithdrawPending, IdempotencyKey: "stats-wd-pend-" + strconv.Itoa(site.Id),
		},
	}).Error)

	balances, err := GetAdtySiteBalances(site)
	require.NoError(t, err)
	require.NotNil(t, balances)
	assert.True(t, balances.AvailableUSD.Equal(decimal.NewFromInt(38)), "available_usd must be site earnings balance ($50) minus frozen ($12) = $38, got %s", balances.AvailableUSD)
	assert.True(t, balances.FrozenUSD.Equal(decimal.NewFromInt(12)), "frozen_usd must be $12 from site earnings wallet, got %s", balances.FrozenUSD)
	assert.True(t, balances.WithdrawnUSD.Equal(decimal.NewFromInt(20)), "withdrawn_usd must sum paid AdtySiteWithdrawal rows ($20), got %s", balances.WithdrawnUSD)
}
