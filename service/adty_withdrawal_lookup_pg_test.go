package service

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestWithdrawalLookupByOriginalUserSiteAndKeyPG(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	site, siteOwner, customer := availableSiteCommission(t, db, decimal.NewFromInt(9))
	var commission model.AdtySiteInviteCommission
	require.NoError(t, db.Where("inviter_id = ? AND site_id = ?", customer.Id, site.Id).First(&commission).Error)
	key := "recovery"
	var ledger model.AdtyBalanceLedger
	for i := 0; i < 2; i++ {
		require.NoError(t, ConvertAdtySiteInviteCommission(customer.Id, commission.Id, decimal.NewFromInt(1), key))
	}
	require.NoError(t, db.Where("idempotency_key = ?", fmt.Sprintf("site-comm-cvt:%d:%s", commission.Id, key)).First(&ledger).Error)
	input := AdtySiteInviteWithdrawalInput{UserID: customer.Id, SiteID: site.Id, Amount: decimal.NewFromInt(2), Method: "balance", IdempotencyKey: key}
	invite, err := CreateAdtySiteInviteWithdrawal(input)
	require.NoError(t, err)
	replay, err := CreateAdtySiteInviteWithdrawal(input)
	require.NoError(t, err)
	require.Equal(t, invite.Id, replay.Id)
	require.NoError(t, db.First(customer, customer.Id).Error)
	require.Equal(t, 1500000, customer.Quota)
	require.NoError(t, SaveAdtySiteWithdrawRules(AdtySiteWithdrawRules{MinUSD: "1", FeeUSD: "1", AlipayCNYRate: "7", USDTUSDRate: "1"}, siteOwner.Id))
	_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(20), AdtySiteWalletChange{Type: "earning_settlement", IdempotencyKey: "extra-earnings"})
	require.NoError(t, err)
	ownerInput := AdtySiteWithdrawInput{SiteID: site.Id, OwnerID: siteOwner.Id, AmountUSD: decimal.NewFromInt(4), Method: "alipay", AccountNo: "account", AccountName: "Owner", ExpectedRate: decimal.NewFromInt(7), IdempotencyKey: key}
	owner, err := CreateAdtySiteWithdrawal(ownerInput)
	require.NoError(t, err)
	require.NoError(t, SaveAdtySiteWithdrawRules(AdtySiteWithdrawRules{MinUSD: "1", FeeUSD: "2", AlipayCNYRate: "8", USDTUSDRate: "1"}, siteOwner.Id))
	ownerReplay, err := CreateAdtySiteWithdrawal(ownerInput)
	require.NoError(t, err)
	require.Equal(t, owner.Id, ownerReplay.Id)
	var freezeCount int64
	require.NoError(t, db.Model(&model.AdtySiteWithdrawal{}).Where("idempotency_key = ?", key).Count(&freezeCount).Error)
	require.EqualValues(t, 1, freezeCount)
	var wallet model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&wallet).Error)
	// Six dollars of invite commission coverage remain, plus the owner's four-dollar freeze.
	require.True(t, wallet.FrozenUSD.Equal(decimal.NewFromInt(10)), wallet.FrozenUSD.String())
	for _, tc := range []struct {
		name string
		user int
		want int64
		find func(int, int, string) (int64, error)
	}{
		{"conversion", customer.Id, ledger.Id, func(user, site int, key string) (int64, error) {
			return LookupSiteCommissionConversion(user, site, commission.Id, key)
		}},
		{"invite", customer.Id, invite.Id, LookupSiteInviteWithdrawal},
		{"owner", siteOwner.Id, owner.Id, LookupSiteOwnerWithdrawal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, err := tc.find(tc.user, site.Id, key)
			require.NoError(t, err)
			require.Equal(t, tc.want, id)
			for _, mismatch := range []struct {
				user, site int
				key        string
			}{{tc.user + 100, site.Id, key}, {tc.user, site.Id + 100, key}, {tc.user, site.Id, "unused"}} {
				id, err := tc.find(mismatch.user, mismatch.site, mismatch.key)
				require.NoError(t, err)
				require.Zero(t, id)
			}
		})
	}
}
