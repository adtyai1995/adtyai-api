package service

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSiteInviteCashWithdrawalIsReviewedByThatSiteOwner(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, owner, inviter := availableSiteCommission(t, db, decimal.NewFromInt(3))
	otherOwner := referralUser(t, db, "wd-outsider-"+strconv.Itoa(site.Id))
	otherSite := &model.AdtySite{OwnerUserId: otherOwner.Id, Subdomain: "wd-out-" + strconv.Itoa(site.Id), RootDomain: "sites.example.com", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(otherSite).Error)
	require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, site.OwnerUserId, AdtySiteMarketingUpdate{MinUSD: new("1"), FeeUSD: new("0.1"), CNYRate: new("7")}))
	row, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: inviter.Id, SiteID: site.Id, Amount: decimal.NewFromInt(2), Method: "usdt",
		Network: "TRC20", AccountNo: "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb", IdempotencyKey: "wd-1",
	})
	require.NoError(t, err)
	require.Equal(t, "pending", row.Status)
	var commission model.AdtySiteInviteCommission
	require.NoError(t, db.Where("site_id = ? AND inviter_id = ?", site.Id, inviter.Id).First(&commission).Error)
	require.True(t, commission.FrozenUSD.Equal(decimal.NewFromInt(2)))
	require.True(t, commission.WithdrawnUSD.IsZero())
	require.NoError(t, db.First(inviter, inviter.Id).Error)
	require.Equal(t, 0, inviter.Quota)
	require.Error(t, ReviewAdtySiteInviteWithdrawal(otherOwner.Id, row.Id, true, ""))
	require.Error(t, ReviewAdtySiteInviteWithdrawal(inviter.Id, row.Id, true, ""))
	require.NoError(t, ReviewAdtySiteInviteWithdrawal(owner.Id, row.Id, true, ""))
	require.NoError(t, MarkAdtySiteInviteWithdrawalPaid(owner.Id, row.Id, "tx-1", "paid"))
	require.NoError(t, db.First(&commission, commission.Id).Error)
	require.True(t, commission.FrozenUSD.IsZero())
	require.True(t, commission.WithdrawnUSD.Equal(decimal.NewFromInt(2)))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, "paid", row.Status)
	require.Equal(t, owner.Id, row.ReviewedBy)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("type = ?", "site_earning").Count(&ledgers).Error)
	require.Zero(t, ledgers)
	var earning model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.NewFromInt(3)))
	require.True(t, earning.FrozenUSD.Equal(decimal.NewFromInt(1)))
	var deducts int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("type = ?", "commission_deduct").Count(&deducts).Error)
	require.Zero(t, deducts)
}

func TestSiteInviteWithdrawalRejectReleasesTheFreeze(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, owner, inviter := availableSiteCommission(t, db, decimal.NewFromInt(2))
	require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, site.OwnerUserId, AdtySiteMarketingUpdate{MinUSD: new("1"), FeeUSD: new("0.1"), CNYRate: new("7")}))
	row, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: inviter.Id, SiteID: site.Id, Amount: decimal.NewFromInt(2), Method: "alipay",
		AccountName: "Ada", QRImageURL: "/api/referral-assets/ada.png", ExpectedCNYRate: decimal.NewFromInt(7),
		IdempotencyKey: "wd-reject",
	})
	require.NoError(t, err)
	require.NoError(t, ReviewAdtySiteInviteWithdrawal(owner.Id, row.Id, false, "no"))
	var commission model.AdtySiteInviteCommission
	require.NoError(t, db.Where("inviter_id = ?", inviter.Id).First(&commission).Error)
	require.True(t, commission.FrozenUSD.IsZero())
	require.True(t, commission.WithdrawnUSD.IsZero())
	require.True(t, commission.EarningsHoldUSD.Equal(decimal.NewFromInt(2)))
	var earning model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.NewFromInt(2)))
	require.True(t, earning.FrozenUSD.Equal(decimal.NewFromInt(2)))
	require.Error(t, MarkAdtySiteInviteWithdrawalPaid(owner.Id, row.Id, "", "late"))
}

func TestSiteInviteBalanceWithdrawalCreditsQuotaWithoutWithdrawnCash(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, _, inviter := availableSiteCommission(t, db, decimal.NewFromInt(2))
	row, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: inviter.Id, SiteID: site.Id, Amount: decimal.RequireFromString("1.5"), Method: "balance",
		IdempotencyKey: "wd-balance",
	})
	require.NoError(t, err)
	require.Equal(t, "paid", row.Status)
	var commission model.AdtySiteInviteCommission
	require.NoError(t, db.Where("inviter_id = ?", inviter.Id).First(&commission).Error)
	require.True(t, commission.ConvertedUSD.Equal(decimal.RequireFromString("1.5")))
	require.True(t, commission.WithdrawnUSD.IsZero())
	require.True(t, commission.FrozenUSD.IsZero())
	require.NoError(t, db.First(inviter, inviter.Id).Error)
	require.Equal(t, 750000, inviter.Quota)
	var withdrawLots []model.AdtyFundBatch
	require.NoError(t, db.Where("user_id = ? AND source = ?", inviter.Id, fmt.Sprintf("site-comm-wd:%d:%d", commission.Id, row.Id)).Find(&withdrawLots).Error)
	require.Len(t, withdrawLots, 1)
	require.Equal(t, "principal", withdrawLots[0].Kind)
	require.EqualValues(t, 750000, withdrawLots[0].Remaining)
	var earning model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.RequireFromString("0.5")))
	require.True(t, earning.FrozenUSD.Equal(decimal.RequireFromString("0.5")))
}

func TestApprovedSiteInviteWithdrawalCanFailWithoutReleasingTheSiteHold(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, owner, inviter := availableSiteCommission(t, db, decimal.NewFromInt(2))
	require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, site.OwnerUserId, AdtySiteMarketingUpdate{MinUSD: new("1"), FeeUSD: new("0.1"), CNYRate: new("7")}))
	row, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: inviter.Id, SiteID: site.Id, Amount: decimal.NewFromInt(2), Method: "usdt",
		Network: "TRC20", AccountNo: "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb", IdempotencyKey: "wd-fail",
	})
	require.NoError(t, err)
	require.NoError(t, ReviewAdtySiteInviteWithdrawal(owner.Id, row.Id, true, ""))
	require.NoError(t, ReviewAdtySiteInviteWithdrawal(owner.Id, row.Id, false, "payout failed"))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, "rejected", row.Status)
	var commission model.AdtySiteInviteCommission
	require.NoError(t, db.Where("inviter_id = ?", inviter.Id).First(&commission).Error)
	require.True(t, commission.FrozenUSD.IsZero())
	require.True(t, commission.WithdrawnUSD.IsZero())
	require.True(t, commission.EarningsHoldUSD.Equal(decimal.NewFromInt(2)))
	var earning model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.NewFromInt(2)))
	require.True(t, earning.FrozenUSD.Equal(decimal.NewFromInt(2)))
	require.Error(t, MarkAdtySiteInviteWithdrawalPaid(owner.Id, row.Id, "late", ""))
	again, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: inviter.Id, SiteID: site.Id, Amount: decimal.NewFromInt(2), Method: "balance",
		IdempotencyKey: "wd-fail-balance",
	})
	require.NoError(t, err)
	require.Equal(t, "paid", again.Status)
}

func TestMixedRefundLeavesTheRemainingSiteHoldAssignable(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, owner, inviter := availableSiteCommission(t, db, decimal.NewFromInt(4))
	require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, site.OwnerUserId, AdtySiteMarketingUpdate{MinUSD: new("1"), FeeUSD: new("0"), CNYRate: new("7")}))
	_, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: inviter.Id, SiteID: site.Id, Amount: decimal.NewFromInt(1), Method: "balance",
		IdempotencyKey: "mix-balance",
	})
	require.NoError(t, err)
	cash, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: inviter.Id, SiteID: site.Id, Amount: decimal.NewFromInt(1), Method: "usdt",
		Network: "TRC20", AccountNo: "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb", IdempotencyKey: "mix-cash",
	})
	require.NoError(t, err)
	require.NoError(t, ReviewAdtySiteInviteWithdrawal(owner.Id, cash.Id, true, ""))
	require.NoError(t, MarkAdtySiteInviteWithdrawalPaid(owner.Id, cash.Id, "tx-mix", "paid"))
	var commission model.AdtySiteInviteCommission
	require.NoError(t, db.Where("inviter_id = ?", inviter.Id).First(&commission).Error)
	ext := &model.AdtyTopupOrder{
		TradeNo: commission.TradeNo, UserId: commission.InviteeId, SiteId: site.Id,
		FaceUSD: decimal.NewFromInt(40), CreditQuota: 40 * 500000, RefundQuota: 20 * 500000,
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.AdtyAfterTopUpRefundedTx(tx, &model.TopUp{TradeNo: commission.TradeNo, UserId: commission.InviteeId}, ext)
	}))
	require.NoError(t, db.First(&commission, commission.Id).Error)
	require.True(t, commission.EarningsHoldUSD.Equal(decimal.NewFromInt(1)))
	require.True(t, SiteInviteCommissionWithdrawable(&commission, time.Unix(commission.AvailableAt, 0)))
	require.NoError(t, ConvertAdtySiteInviteCommission(inviter.Id, commission.Id, decimal.NewFromInt(1), "mix-rest"))
	var earning model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.NewFromInt(2)))
	require.True(t, earning.FrozenUSD.IsZero())
}

func TestSiteInviteUSDTWithdrawalRefusesWhenItsOwnRulesAreUnset(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, _, inviter := availableSiteCommission(t, db, decimal.NewFromInt(2))
	_, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: inviter.Id, SiteID: site.Id, Amount: decimal.NewFromInt(2), Method: "usdt",
		Network: "TRC20", AccountNo: "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb", IdempotencyKey: "wd-unset",
	})
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Model(&model.AdtySiteInviteWithdrawal{}).Count(&count).Error)
	require.Zero(t, count)
}

func availableSiteCommission(t *testing.T, db *gorm.DB, amount decimal.Decimal) (*model.AdtySite, *model.User, *model.User) {
	t.Helper()
	suffix := strconv.FormatInt(time.Now().UnixNano()%1_000_000, 10)
	owner := referralUser(t, db, "wd-owner-"+suffix)
	inviter := referralUser(t, db, "wd-inviter-"+suffix)
	invitee := referralUser(t, db, "wd-invitee-"+suffix)
	site := &model.AdtySite{OwnerUserId: owner.Id, Subdomain: "wd-" + suffix, RootDomain: "sites.example.com", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(site).Error)
	require.NoError(t, db.Model(inviter).Update("site_id", site.Id).Error)
	require.NoError(t, db.Model(invitee).Update("site_id", site.Id).Error)
	require.NoError(t, db.Create(&model.AdtySiteReferralRate{SiteId: site.Id, Rate: decimal.RequireFromString("0.1"), Enabled: true}).Error)
	require.NoError(t, db.Create(&model.AdtySiteInvite{SiteId: site.Id, InviterId: inviter.Id, InviteeId: invitee.Id, Rate: decimal.RequireFromString("0.1")}).Error)
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, amount, AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "wd-earn-" + suffix,
	})
	require.NoError(t, err)
	require.NoError(t, setInviteCoverageForTest(site, "10", "1"))
	face := amount.Mul(decimal.NewFromInt(10))
	ext := siteInviteOrder(t, site.Id, invitee.Id, "WD-"+suffix, face)
	var commission model.AdtySiteInviteCommission
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := RecordAdtySiteInviteCommissionTx(tx, ext, time.Unix(1_700_000_000, 0)); err != nil {
			return err
		}
		if err := tx.Where("trade_no = ?", ext.TradeNo).First(&commission).Error; err != nil {
			return err
		}
		return CoverAdtySiteInviteCommissionTx(tx, commission.Id, time.Unix(1_700_000_100, 0))
	}))
	require.NoError(t, db.First(&commission, commission.Id).Error)
	require.NoError(t, ReleaseAdtySiteInviteCommissions(time.Unix(commission.AvailableAt, 0)))
	require.NoError(t, db.Create(&model.ReferralAsset{
		OwnerUserId: inviter.Id, Purpose: model.ReferralAssetPurposeWithdrawalQR,
		StoragePath: "ada.png", ContentType: "image/png", CreatedBy: "user",
	}).Error)
	return site, owner, inviter
}

func TestSiteInviteWithdrawalRejectsWeChatAndAliases(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := prepareReferralDialectDB(t, dialect)
			site, _, inviter := availableSiteCommission(t, db, decimal.NewFromInt(4))
			require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, site.OwnerUserId, AdtySiteMarketingUpdate{MinUSD: new("1"), FeeUSD: new("0.1"), CNYRate: new("7")}))
			for i, method := range []string{"wechat", "WeChat", "WECHAT", "wxpay", "weixin", "微信", "微信支付"} {
				_, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
					UserID: inviter.Id, SiteID: site.Id, Amount: decimal.NewFromInt(1), Method: method,
					AccountName: "Ada", QRImageURL: "/api/referral-assets/ada.png", ExpectedCNYRate: decimal.NewFromInt(7),
					IdempotencyKey: "wd-wechat-" + dialect + "-" + strconv.Itoa(i),
				})
				require.Error(t, err, method)
				require.Contains(t, err.Error(), "Unsupported withdrawal method", method)
			}
			var count int64
			require.NoError(t, db.Model(&model.AdtySiteInviteWithdrawal{}).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestSiteInviteWithdrawalAllowsOfficialMethodsAndRulesCatalog(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := prepareReferralDialectDB(t, dialect)
			site, _, inviter := availableSiteCommission(t, db, decimal.NewFromInt(6))

			unset, err := LoadAdtyCustomerSiteInviteWithdrawRules(inviter.Id)
			require.NoError(t, err)
			require.False(t, unset.Configured)
			require.Equal(t, []string{"balance"}, unset.Methods)

			require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, site.OwnerUserId, AdtySiteMarketingUpdate{MinUSD: new("1"), FeeUSD: new("0.1"), CNYRate: new("7")}))
			view, err := LoadAdtyCustomerSiteInviteWithdrawRules(inviter.Id)
			require.NoError(t, err)
			require.True(t, view.Configured)
			require.Equal(t, []string{"balance", "usdt", "alipay"}, view.Methods)
			require.NotContains(t, view.Methods, "wechat")

			balance, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
				UserID: inviter.Id, SiteID: site.Id, Amount: decimal.NewFromInt(1), Method: "balance",
				IdempotencyKey: "wd-ok-balance-" + dialect,
			})
			require.NoError(t, err)
			require.Equal(t, "balance", balance.Method)
			require.Equal(t, "paid", balance.Status)

			usdt, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
				UserID: inviter.Id, SiteID: site.Id, Amount: decimal.NewFromInt(2), Method: "usdt",
				Network: "TRC20", AccountNo: "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb",
				IdempotencyKey: "wd-ok-usdt-" + dialect,
			})
			require.NoError(t, err)
			require.Equal(t, "usdt", usdt.Method)
			require.Equal(t, "pending", usdt.Status)

			alipay, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
				UserID: inviter.Id, SiteID: site.Id, Amount: decimal.NewFromInt(2), Method: "alipay",
				AccountName: "Ada", QRImageURL: "/api/referral-assets/ada.png", ExpectedCNYRate: decimal.NewFromInt(7),
				IdempotencyKey: "wd-ok-alipay-" + dialect,
			})
			require.NoError(t, err)
			require.Equal(t, "alipay", alipay.Method)
			require.Equal(t, "pending", alipay.Status)

			var count int64
			require.NoError(t, db.Model(&model.AdtySiteInviteWithdrawal{}).Count(&count).Error)
			require.Equal(t, int64(3), count)
		})
	}
}

func TestHistoricalSiteInviteWeChatWithdrawalCanStillBeReviewedAndPaid(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := prepareReferralDialectDB(t, dialect)
			site, owner, inviter := availableSiteCommission(t, db, decimal.NewFromInt(2))
			require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, site.OwnerUserId, AdtySiteMarketingUpdate{MinUSD: new("1"), FeeUSD: new("0.1"), CNYRate: new("7")}))
			historical := &model.AdtySiteInviteWithdrawal{
				SiteId: site.Id, UserId: inviter.Id, AmountUSD: decimal.NewFromInt(2),
				NetUSD: decimal.NewFromInt(2), Method: "wechat", Status: model.AdtySiteInviteWithdrawPending,
				AccountName: "Ada", QRImageURL: "/api/referral-assets/ada.png", Rate: "7",
				PayoutAmount: "14.00", PayoutCurrency: "CNY",
				IdempotencyKey: "wd-hist-wechat-" + dialect,
			}
			require.NoError(t, db.Create(historical).Error)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				return allocateSiteInviteWithdrawalTx(tx, historical, false)
			}))
			require.NoError(t, ReviewAdtySiteInviteWithdrawal(owner.Id, historical.Id, true, ""))
			require.NoError(t, MarkAdtySiteInviteWithdrawalPaid(owner.Id, historical.Id, "", "paid"))
			require.NoError(t, db.First(historical, historical.Id).Error)
			require.Equal(t, "wechat", historical.Method)
			require.Equal(t, "paid", historical.Status)
			var commission model.AdtySiteInviteCommission
			require.NoError(t, db.Where("inviter_id = ?", inviter.Id).First(&commission).Error)
			require.True(t, commission.WithdrawnUSD.Equal(decimal.NewFromInt(2)))
		})
	}
}

func TestSiteInviteWithdrawalRejectsForeignOrUnregisteredQR(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, _, inviter := availableSiteCommission(t, db, decimal.NewFromInt(4))
	stranger := referralUser(t, db, "site-wd-stranger")
	require.NoError(t, SaveAdtyOwnerSiteMarketing(site.Id, site.OwnerUserId, AdtySiteMarketingUpdate{MinUSD: new("1"), FeeUSD: new("0.1"), CNYRate: new("7")}))
	require.NoError(t, db.Create(&model.ReferralAsset{
		OwnerUserId: stranger.Id, Purpose: model.ReferralAssetPurposeWithdrawalQR, StoragePath: "stranger.png", ContentType: "image/png", CreatedBy: "user",
	}).Error)
	require.NoError(t, db.Create(&model.ReferralAsset{
		OwnerUserId: inviter.Id, Purpose: model.ReferralAssetPurposePaymentProof, StoragePath: "inviter-proof.png", ContentType: "image/png", CreatedBy: "admin",
	}).Error)

	_, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: inviter.Id, SiteID: site.Id, Amount: decimal.NewFromInt(2), Method: "alipay",
		AccountName: "Ada", QRImageURL: "/api/referral-assets/stranger.png", ExpectedCNYRate: decimal.NewFromInt(7), IdempotencyKey: "site-qr-foreign",
	})
	require.Error(t, err)

	_, err = CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: inviter.Id, SiteID: site.Id, Amount: decimal.NewFromInt(2), Method: "alipay",
		AccountName: "Ada", QRImageURL: "/api/referral-assets/inviter-proof.png", ExpectedCNYRate: decimal.NewFromInt(7), IdempotencyKey: "site-qr-proof",
	})
	require.Error(t, err)

	_, err = CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: inviter.Id, SiteID: site.Id, Amount: decimal.NewFromInt(2), Method: "alipay",
		AccountName: "Ada", QRImageURL: "/api/referral-assets/missing-site.png", ExpectedCNYRate: decimal.NewFromInt(7), IdempotencyKey: "site-qr-missing",
	})
	require.Error(t, err)
}

func TestSiteInviteWithdrawalToBalanceRequiresIntegralQuota(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	site, _, inviter := availableSiteCommission(t, db, decimal.NewFromInt(2))
	for i, amount := range []string{"0.000001", "0.000001", "0.00000099"} {
		_, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
			UserID: inviter.Id, SiteID: site.Id, Amount: decimal.RequireFromString(amount),
			Method: "balance", IdempotencyKey: "wd-half-" + strconv.Itoa(i),
		})
		require.Error(t, err, "fractional quota must not be rounded into spendable funds")
	}

	// $0.0000012 at QuotaPerUnit=500000 is 0.6 quota (non-integral) and must be rejected.
	_, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: inviter.Id, SiteID: site.Id, Amount: decimal.RequireFromString("0.0000012"),
		Method: "balance", IdempotencyKey: "wd-frac-quota-1",
	})
	require.Error(t, err)

	// Sub-8-decimal amounts must also be rejected.
	_, err = CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: inviter.Id, SiteID: site.Id, Amount: decimal.RequireFromString("0.000002004"),
		Method: "balance", IdempotencyKey: "wd-sub8-quota-1",
	})
	require.Error(t, err)

	var user model.User
	require.NoError(t, db.First(&user, inviter.Id).Error)
	require.Equal(t, 0, user.Quota, "rejected non-integral quota transfers must not credit user quota")

	// Exact 1-quota amount ($0.000002 = 1 quota at QuotaPerUnit=500000) succeeds.
	wd, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: inviter.Id, SiteID: site.Id, Amount: decimal.RequireFromString("0.000002"),
		Method: "balance", IdempotencyKey: "wd-exact-quota-1",
	})
	require.NoError(t, err)
	require.Equal(t, model.AdtySiteInviteWithdrawPaid, wd.Status)
	require.NoError(t, db.First(&user, inviter.Id).Error)
	require.Equal(t, 1, user.Quota)
	replay, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: inviter.Id, SiteID: site.Id, Amount: decimal.RequireFromString("0.000002"),
		Method: "balance", IdempotencyKey: "wd-exact-quota-1",
	})
	require.NoError(t, err)
	require.Equal(t, wd.Id, replay.Id)
	require.NoError(t, db.First(&user, inviter.Id).Error)
	require.Equal(t, 1, user.Quota)
	var rows int64
	require.NoError(t, db.Model(&model.AdtySiteInviteWithdrawal{}).Where("user_id = ?", inviter.Id).Count(&rows).Error)
	require.EqualValues(t, 1, rows)
}
