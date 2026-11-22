package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSiteCommissionFreezeBlocksALaterEarningsDebit(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(5), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-block",
	})
	require.NoError(t, err)
	require.NoError(t, setInviteCoverageForTest(site, "10", "1"))
	ext := siteInviteOrder(t, site.Id, invitee.Id, "SITE-BLOCK", decimal.NewFromInt(30))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := RecordAdtySiteInviteCommissionTx(tx, ext, time.Unix(1, 0)); err != nil {
			return err
		}
		var row model.AdtySiteInviteCommission
		if err := tx.Where("trade_no = ?", ext.TradeNo).First(&row).Error; err != nil {
			return err
		}
		return CoverAdtySiteInviteCommissionTx(tx, row.Id, time.Unix(2, 0))
	}))
	_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(-3), AdtySiteWalletChange{
		Type: "earning_withdraw_freeze", IdempotencyKey: "blocked-withdraw",
	})
	require.ErrorIs(t, err, ErrAdtySiteWalletInsufficient)
	_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(-2), AdtySiteWalletChange{
		Type: "earning_withdraw_freeze", IdempotencyKey: "open-withdraw",
	})
	require.NoError(t, err)
}

func TestSiteInviteCommissionKeepsObservationDaysFromRecordTime(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(5), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-snapshot",
	})
	require.NoError(t, err)
	require.NoError(t, setInviteCoverageForTest(site, "10", "7"))
	ext := siteInviteOrder(t, site.Id, invitee.Id, "SITE-DAYS", decimal.NewFromInt(30))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return RecordAdtySiteInviteCommissionTx(tx, ext, time.Unix(1_700_000_000, 0))
	}))
	var row model.AdtySiteInviteCommission
	require.NoError(t, db.Where("trade_no = ?", "SITE-DAYS").First(&row).Error)
	require.Equal(t, int64(7), row.ObservationDays)
	require.NoError(t, setInviteCoverageForTest(site, "10", "30"))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return CoverAdtySiteInviteCommissionTx(tx, row.Id, time.Unix(1_700_000_100, 0))
	}))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, int64(7), row.ObservationDays)
	require.Equal(t, int64(1_700_000_100+7*86400), row.AvailableAt)
}

func TestLegacySiteInviteCommissionStaysPendingUntilOwnerConfiguresCoverage(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(20), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-seed",
	})
	require.NoError(t, err)

	// Before site-specific policies, an eligible order could freeze its
	// beneficiary and rate while its legacy coverage conditions were unset.
	require.NoError(t, db.Model(site).Updates(map[string]any{"pending_coverage_cap": nil, "observation_days": nil}).Error)
	require.NoError(t, db.Model(&model.AdtySiteReferralRate{}).Where("site_id = ?", site.Id).Update("legacy_enabled", true).Error)
	ext := &model.AdtyTopupOrder{TradeNo: "SITE-1", UserId: invitee.Id, SiteId: site.Id, FaceUSD: decimal.NewFromInt(30)}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return RecordAdtySiteInviteCommissionTx(tx, ext, time.Unix(1_700_000_000, 0))
	}))
	var row model.AdtySiteInviteCommission
	require.NoError(t, db.Where("trade_no = ?", "SITE-1").First(&row).Error)
	require.Equal(t, "pending_coverage", row.Status)
	require.True(t, row.AmountUSD.Equal(decimal.RequireFromString("3")))
	require.False(t, SiteInviteCommissionWithdrawable(&row, time.Unix(1_700_000_000, 0)))
	var wallet model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&wallet).Error)
	require.True(t, wallet.Balance.Equal(decimal.NewFromInt(20)), "unset coverage rules must not spend earnings")

	require.NoError(t, setInviteCoverageForTest(site, "10", "7"))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return CoverAdtySiteInviteCommissionTx(tx, row.Id, time.Unix(1_700_000_100, 0))
	}))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, "covered", row.Status)
	require.Equal(t, int64(1_700_000_100+7*86400), row.AvailableAt)
	require.False(t, SiteInviteCommissionWithdrawable(&row, time.Unix(1_700_000_100, 0)))
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&wallet).Error)
	require.True(t, wallet.Balance.Equal(decimal.NewFromInt(20)))
	require.True(t, wallet.FrozenUSD.Equal(decimal.NewFromInt(3)))

	require.NoError(t, ReleaseAdtySiteInviteCommissions(time.Unix(row.AvailableAt, 0)))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, "available", row.Status)
	require.True(t, SiteInviteCommissionWithdrawable(&row, time.Unix(row.AvailableAt, 0)))
	var user model.User
	require.NoError(t, db.First(&user, invitee.Id).Error)
	require.Equal(t, 0, user.Quota, "making commission available must not credit personal quota")
}

var siteInviteSeed int64

func seedSiteInvite(t *testing.T, db *gorm.DB, rate decimal.Decimal) (*model.AdtySite, *model.User) {
	t.Helper()
	suffix := strconv.FormatInt(atomic.AddInt64(&siteInviteSeed, 1), 10)
	owner := referralUser(t, db, "comm-owner-"+suffix)
	inviter := referralUser(t, db, "comm-inviter-"+suffix)
	invitee := referralUser(t, db, "comm-invitee-"+suffix)
	site := &model.AdtySite{OwnerUserId: owner.Id, Subdomain: "comm-" + suffix, RootDomain: "sites.example.com", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(site).Error)
	require.NoError(t, db.Model(inviter).Update("site_id", site.Id).Error)
	require.NoError(t, db.Model(invitee).Update("site_id", site.Id).Error)
	require.NoError(t, db.Create(&model.AdtySiteInvite{
		SiteId: site.Id, InviterId: inviter.Id, InviteeId: invitee.Id, Rate: rate,
	}).Error)
	require.NoError(t, db.Create(&model.AdtySiteReferralRate{SiteId: site.Id, Rate: rate, Enabled: true}).Error)
	require.NoError(t, setInviteCoverageForTest(site, "100", "7"))
	return site, invitee
}

func setInviteCoverageForTest(site *model.AdtySite, cap, days string) error {
	return SaveAdtyOwnerSiteMarketing(site.Id, site.OwnerUserId, AdtySiteMarketingUpdate{PendingCoverageCap: &cap, ObservationDays: &days})
}

func siteInviteOrder(t *testing.T, siteID, inviteeID int, tradeNo string, face decimal.Decimal) *model.AdtyTopupOrder {
	t.Helper()
	ext := &model.AdtyTopupOrder{TradeNo: tradeNo, UserId: inviteeID, SiteId: siteID, FaceUSD: face}
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error { return PrepareAdtySiteInviteCommissionSnapshotTx(tx, nil, ext) }))
	return ext
}

func TestSiteInviteCommissionMarginFillsShortfallOnlyAfterRootAuthorizesThatBusiness(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(1), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-short",
	})
	require.NoError(t, err)
	require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.NewFromInt(100), "commission-margin-cycle"))
	adty_setting.AuthorizeSiteBonusMarginFill(site.Id, true)
	require.NoError(t, setInviteCoverageForTest(site, "10", "7"))

	ext := siteInviteOrder(t, site.Id, invitee.Id, "SITE-M", decimal.NewFromInt(30))
	var row model.AdtySiteInviteCommission
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := RecordAdtySiteInviteCommissionTx(tx, ext, time.Unix(1_700_000_000, 0)); err != nil {
			return err
		}
		if err := tx.Where("trade_no = ?", "SITE-M").First(&row).Error; err != nil {
			return err
		}
		return CoverAdtySiteInviteCommissionTx(tx, row.Id, time.Unix(1_700_000_100, 0))
	}))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, "pending_coverage", row.Status)
	require.True(t, row.CoveredUSD.Equal(decimal.NewFromInt(1)))
	var margin model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&margin).Error)
	require.True(t, margin.Balance.Equal(decimal.NewFromInt(100)), "gift margin authorization must not spend margin on commission")

	require.NoError(t, AuthorizeAdtySiteCommissionMargin(site.Id, true))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return CoverAdtySiteInviteCommissionTx(tx, row.Id, time.Unix(1_700_000_200, 0))
	}))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, "covered", row.Status)
	require.True(t, row.CoveredUSD.Equal(decimal.RequireFromString("3")))
	require.Equal(t, int64(1_700_000_200+7*86400), row.AvailableAt)
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&margin).Error)
	require.True(t, margin.Balance.Equal(decimal.NewFromInt(100)))
	require.True(t, margin.FrozenUSD.Equal(decimal.NewFromInt(2)))
	require.True(t, margin.CycleUsedUSD.IsZero())
	var ledger model.AdtySiteWalletLedger
	require.NoError(t, db.Where("idempotency_key = ?", "commission:SITE-M:margin").First(&ledger).Error)
	require.Equal(t, "commission_freeze", ledger.Type)
	require.Equal(t, model.AdtySiteWalletMargin, ledger.WalletKind)
	require.True(t, ledger.Delta.IsZero())
	require.True(t, ledger.HoldDelta.Equal(decimal.NewFromInt(2)))

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return CoverAdtySiteInviteCommissionTx(tx, row.Id, time.Unix(1_700_000_300, 0))
	}))
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&margin).Error)
	require.True(t, margin.Balance.Equal(decimal.NewFromInt(100)))
	require.True(t, margin.FrozenUSD.Equal(decimal.NewFromInt(2)))

	extBoth := siteInviteOrder(t, site.Id, invitee.Id, "SITE-M2", decimal.NewFromInt(30))
	var both model.AdtySiteInviteCommission
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := RecordAdtySiteInviteCommissionTx(tx, extBoth, time.Unix(1_700_000_400, 0)); err != nil {
			return err
		}
		if err := tx.Where("trade_no = ?", "SITE-M2").First(&both).Error; err != nil {
			return err
		}
		return CoverAdtySiteInviteCommissionTx(tx, both.Id, time.Unix(1_700_000_400, 0))
	}))
	require.NoError(t, db.First(&both, both.Id).Error)
	require.Equal(t, "covered", both.Status)
	require.True(t, both.CoveredUSD.Equal(decimal.RequireFromString("3")))
	var earning model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.NewFromInt(1)))
	require.True(t, earning.FrozenUSD.Equal(decimal.NewFromInt(1)))
	var marginAfter model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&marginAfter).Error)
	require.True(t, marginAfter.Balance.Equal(decimal.NewFromInt(100)))
	require.True(t, marginAfter.FrozenUSD.Equal(decimal.NewFromInt(5)))
}

func TestSiteInviteCommissionMarginStopsAtTheOpenCycle(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(1), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-cycle",
	})
	require.NoError(t, err)
	require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.NewFromInt(100), "cycle-room"))
	require.NoError(t, db.Model(&model.AdtySiteWallet{}).Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).Update("cycle_used_usd", "89").Error)
	require.NoError(t, setInviteCoverageForTest(site, "10", "7"))
	require.NoError(t, AuthorizeAdtySiteCommissionMargin(site.Id, true))

	ext := siteInviteOrder(t, site.Id, invitee.Id, "SITE-CYCLE", decimal.NewFromInt(30))
	var row model.AdtySiteInviteCommission
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := RecordAdtySiteInviteCommissionTx(tx, ext, time.Unix(1_700_000_000, 0)); err != nil {
			return err
		}
		if err := tx.Where("trade_no = ?", "SITE-CYCLE").First(&row).Error; err != nil {
			return err
		}
		return CoverAdtySiteInviteCommissionTx(tx, row.Id, time.Unix(1_700_000_100, 0))
	}))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, "pending_coverage", row.Status)
	require.True(t, row.CoveredUSD.Equal(decimal.NewFromInt(2)))
	var margin model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&margin).Error)
	require.True(t, margin.Balance.Equal(decimal.NewFromInt(100)))
	require.True(t, margin.FrozenUSD.Equal(decimal.NewFromInt(1)))
	require.True(t, margin.CycleUsedUSD.Equal(decimal.NewFromInt(89)))

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return CoverAdtySiteInviteCommissionTx(tx, row.Id, time.Unix(1_700_000_200, 0))
	}))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, "pending_coverage", row.Status)
	var marginAgain model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&marginAgain).Error)
	require.True(t, marginAgain.Balance.Equal(decimal.NewFromInt(100)))
	require.True(t, marginAgain.FrozenUSD.Equal(decimal.NewFromInt(1)))
}

func TestSiteInviteCommissionRefundClawsBackByCompletedPrincipalRatio(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	require.NoError(t, db.AutoMigrate(model.ReferralTables()...))
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(1), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-claw",
	})
	require.NoError(t, err)
	require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.NewFromInt(100), "claw-cycle"))
	require.NoError(t, setInviteCoverageForTest(site, "10", "7"))
	require.NoError(t, AuthorizeAdtySiteCommissionMargin(site.Id, true))

	ext := siteInviteOrder(t, site.Id, invitee.Id, "SITE-CLAW", decimal.NewFromInt(30))
	ext.CreditQuota = 30 * 500000
	var row model.AdtySiteInviteCommission
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := RecordAdtySiteInviteCommissionTx(tx, ext, time.Unix(1_700_000_000, 0)); err != nil {
			return err
		}
		if err := tx.Where("trade_no = ?", "SITE-CLAW").First(&row).Error; err != nil {
			return err
		}
		return CoverAdtySiteInviteCommissionTx(tx, row.Id, time.Unix(1_700_000_100, 0))
	}))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, "covered", row.Status)

	ext.RefundQuota = 15 * 500000
	topUp := &model.TopUp{TradeNo: "SITE-CLAW", UserId: invitee.Id}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.AdtyAfterTopUpRefundedTx(tx, topUp, ext)
	}))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.True(t, row.ClawedUSD.Equal(decimal.RequireFromString("1.5")))
	require.True(t, row.RecoveredUSD.Equal(decimal.RequireFromString("1.5")))
	require.Equal(t, "covered", row.Status)
	var releaseLedger model.AdtySiteWalletLedger
	require.NoError(t, db.Where("type = ? AND wallet_kind = ?", "commission_release", model.AdtySiteWalletMargin).First(&releaseLedger).Error)
	require.True(t, releaseLedger.Delta.IsZero())
	require.True(t, releaseLedger.HoldDelta.IsNegative())
	var margin model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&margin).Error)
	require.True(t, margin.Balance.Equal(decimal.NewFromInt(100)))
	require.True(t, margin.FrozenUSD.Equal(decimal.NewFromInt(1)))
	require.True(t, margin.CycleUsedUSD.IsZero())
	var earning model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.NewFromInt(1)))
	require.True(t, earning.FrozenUSD.Equal(decimal.RequireFromString("0.5")))
	var user model.User
	require.NoError(t, db.First(&user, invitee.Id).Error)
	require.Equal(t, 0, user.Quota)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.AdtyAfterTopUpRefundedTx(tx, topUp, ext)
	}))
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&margin).Error)
	require.True(t, margin.Balance.Equal(decimal.NewFromInt(100)))
	require.True(t, margin.FrozenUSD.Equal(decimal.NewFromInt(1)))

	ext.RefundQuota = 30 * 500000
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.AdtyAfterTopUpRefundedTx(tx, topUp, ext)
	}))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.True(t, row.ClawedUSD.Equal(decimal.RequireFromString("3")))
	require.True(t, row.RecoveredUSD.Equal(decimal.RequireFromString("3")))
	require.Equal(t, "clawed_back", row.Status)
	require.False(t, SiteInviteCommissionWithdrawable(&row, time.Unix(row.AvailableAt, 0)))
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&margin).Error)
	require.True(t, margin.Balance.Equal(decimal.NewFromInt(100)))
	require.True(t, margin.FrozenUSD.IsZero())
	require.True(t, margin.CycleUsedUSD.IsZero())
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.NewFromInt(1)))
	require.True(t, earning.FrozenUSD.IsZero())
	require.NoError(t, db.First(&user, invitee.Id).Error)
	require.Equal(t, 0, user.Quota)
}

func TestRefundPageFactsUseRecordedLotsAndOriginalRate(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	buyer := referralUser(t, db, "refund-facts-"+strconv.FormatInt(time.Now().UnixNano()%1_000_000, 10))
	require.NoError(t, db.Model(buyer).Update("quota_pending", int64(500000)).Error)
	trade := "FACTS-" + strconv.Itoa(buyer.Id)
	require.NoError(t, db.Create(&model.TopUp{TradeNo: trade, UserId: buyer.Id, Status: common.TopUpStatusSuccess}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo: trade, UserId: buyer.Id, SiteId: 4, FaceUSD: decimal.NewFromInt(100),
		PaidAmount: decimal.NewFromInt(700), PaidCurrency: "CNY", PayCurrency: "CNY",
		PriceSnapshot: decimal.NewFromInt(7), BonusUSD: decimal.NewFromInt(10),
		CreditQuota: 110 * 500000, RefundQuota: 5 * 500000,
	}).Error)
	require.NoError(t, db.Create(&[]model.AdtyFundBatch{
		{UserId: buyer.Id, Kind: "principal", Source: trade, Original: 100 * 500000, Remaining: 40 * 500000},
		{UserId: buyer.Id, Kind: "bonus", Source: trade, Original: 10 * 500000, Remaining: 4 * 500000},
	}).Error)
	facts, err := model.LoadAdtyRefundPageFacts(trade, decimal.NewFromInt(10))
	require.NoError(t, err)
	require.Equal(t, trade, facts.TradeNo)
	require.Equal(t, 4, facts.SiteId)
	require.Equal(t, buyer.Id, facts.UserId)
	require.Equal(t, "100", facts.FaceUSD)
	require.Equal(t, "700", facts.PaidAmount)
	require.Equal(t, "CNY", facts.PaidCurrency)
	require.Equal(t, "7", facts.OrderRate)
	require.Equal(t, "60", facts.PrincipalConsumedUSD)
	require.Equal(t, "40", facts.PrincipalRemainingUSD)
	require.Equal(t, int64(500000), facts.PendingQuota)
	require.Equal(t, int64(5*500000), facts.RefundOccupiedQuota)
	require.Equal(t, "10", facts.BonusUSD)
	require.Equal(t, int64(6*500000), facts.BonusUsedQuota)
	require.Equal(t, int64(4*500000), facts.BonusRemainingQuota)
	require.Equal(t, "70", facts.ForeignReference)
}

func TestRefundPreviewShowsIssuedWithdrawnAndExpectedClawback(t *testing.T) {
	issued, withdrawn, expected := model.SiteInviteCommissionRefundPreview(
		decimal.NewFromInt(3), decimal.NewFromInt(1), decimal.RequireFromString("0.5"), 15*500000, 30*500000,
	)
	require.Equal(t, "3", issued)
	require.Equal(t, "1", withdrawn)
	require.Equal(t, "1", expected)
	_, _, none := model.SiteInviteCommissionRefundPreview(decimal.Zero, decimal.Zero, decimal.Zero, 1, 1)
	require.Equal(t, "0", none)
}

func TestSiteInviteBatchRegisterUsesOnlyASavedRootThreshold(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, inviter, invitees := seedBatchInvite(t, db, 3)
	for i, invitee := range invitees {
		require.NoError(t, BindSiteInviteFromIP(site.Id, inviter.Id, invitee.Id, "ip-a"))
		var count int64
		require.NoError(t, db.Model(&model.AdtySiteInvite{}).Where("inviter_id = ?", inviter.Id).Count(&count).Error)
		require.Equal(t, int64(i+1), count)
	}
	require.NoError(t, SaveAdtySiteInviteBatchPolicy("2", "24"))
	other := referralUser(t, db, "batch-other-"+strconv.Itoa(site.Id))
	require.NoError(t, db.Model(other).Update("site_id", site.Id).Error)
	require.Error(t, BindSiteInviteFromIP(site.Id, inviter.Id, other.Id, "ip-a"))
	var blocked model.AdtySiteInvite
	require.Error(t, db.Where("invitee_id = ?", other.Id).First(&blocked).Error)
	var anomaly model.AdtyAnomalyDisposition
	require.NoError(t, db.Where("kind = ? AND ref = ?", "batch_register", strconv.Itoa(site.Id)).First(&anomaly).Error)
	require.Equal(t, "open", anomaly.Status)
	allowed := referralUser(t, db, "batch-ok-"+strconv.Itoa(site.Id))
	require.NoError(t, db.Model(allowed).Update("site_id", site.Id).Error)
	require.NoError(t, BindSiteInviteFromIP(site.Id, inviter.Id, allowed.Id, "ip-b"))
	var wallets int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Count(&wallets).Error)
	require.Zero(t, wallets)
}

func seedBatchInvite(t *testing.T, db *gorm.DB, n int) (*model.AdtySite, *model.User, []*model.User) {
	t.Helper()
	suffix := strconv.FormatInt(atomic.AddInt64(&siteInviteSeed, 1), 10)
	owner := referralUser(t, db, "batch-owner-"+suffix)
	inviter := referralUser(t, db, "batch-inviter-"+suffix)
	site := &model.AdtySite{OwnerUserId: owner.Id, Subdomain: "batch-" + suffix, RootDomain: "sites.example.com", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(site).Error)
	require.NoError(t, db.Model(inviter).Update("site_id", site.Id).Error)
	require.NoError(t, db.Create(&model.AdtySiteReferralRate{SiteId: site.Id, Rate: decimal.RequireFromString("0.1"), Enabled: true}).Error)
	invitees := make([]*model.User, 0, n)
	for i := 0; i < n; i++ {
		user := referralUser(t, db, "batch-invitee-"+suffix+"-"+strconv.Itoa(i))
		require.NoError(t, db.Model(user).Update("site_id", site.Id).Error)
		invitees = append(invitees, user)
	}
	return site, inviter, invitees
}

func TestWithdrawnSiteInviteCommissionRefundRecordsLiabilityWithoutDebitingQuota(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(3), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-withdrawn-" + strconv.Itoa(site.Id),
	})
	require.NoError(t, err)
	require.NoError(t, setInviteCoverageForTest(site, "10", "7"))
	ext := siteInviteOrder(t, site.Id, invitee.Id, "SITE-WD-"+strconv.Itoa(site.Id), decimal.NewFromInt(30))
	ext.CreditQuota = 30 * 500000
	var row model.AdtySiteInviteCommission
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := RecordAdtySiteInviteCommissionTx(tx, ext, time.Unix(1_700_000_000, 0)); err != nil {
			return err
		}
		if err := tx.Where("trade_no = ?", ext.TradeNo).First(&row).Error; err != nil {
			return err
		}
		return CoverAdtySiteInviteCommissionTx(tx, row.Id, time.Unix(1_700_000_100, 0))
	}))
	require.NoError(t, db.Model(&row).Updates(map[string]any{
		"withdrawn_usd": decimal.NewFromInt(1), "earnings_hold_usd": decimal.NewFromInt(2),
	}).Error)
	require.NoError(t, db.Model(&model.AdtySiteWallet{}).Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).
		Update("frozen_usd", decimal.NewFromInt(2)).Error)
	ext.RefundQuota = 30 * 500000
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.AdtyAfterTopUpRefundedTx(tx, &model.TopUp{TradeNo: ext.TradeNo, UserId: invitee.Id}, ext)
	}))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.True(t, row.WithdrawnUSD.Equal(decimal.NewFromInt(1)))
	require.True(t, row.RecoveredUSD.Equal(decimal.NewFromInt(2)))
	require.True(t, row.RecoveryUSD.IsZero())
	var liability model.AdtyCommissionRecovery
	require.NoError(t, db.Where("commission_id = ?", row.Id).First(&liability).Error)
	require.True(t, liability.AmountUSD.Equal(decimal.NewFromInt(1)))
	require.Equal(t, "open", liability.Status)
	require.Equal(t, "withdrawn", liability.Kind)
	var earning model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.NewFromInt(3)))
	require.True(t, earning.FrozenUSD.IsZero())
	var user model.User
	require.NoError(t, db.First(&user, row.InviterId).Error)
	require.Equal(t, 0, user.Quota)
}

func TestSiteInviteCommissionRefundCancelsUncoveredAmountWithoutMovingWallets(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	require.NoError(t, db.AutoMigrate(model.ReferralTables()...))
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	require.NoError(t, setInviteCoverageForTest(site, "10", "7"))
	ext := siteInviteOrder(t, site.Id, invitee.Id, "SITE-OPEN", decimal.NewFromInt(30))
	ext.CreditQuota = 30 * 500000
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return RecordAdtySiteInviteCommissionTx(tx, ext, time.Unix(1, 0))
	}))
	ext.RefundQuota = 15 * 500000
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.AdtyAfterTopUpRefundedTx(tx, &model.TopUp{TradeNo: "SITE-OPEN", UserId: invitee.Id}, ext)
	}))
	var row model.AdtySiteInviteCommission
	require.NoError(t, db.Where("trade_no = ?", "SITE-OPEN").First(&row).Error)
	require.Equal(t, "pending_coverage", row.Status)
	require.True(t, row.ClawedUSD.Equal(decimal.RequireFromString("1.5")))
	require.True(t, row.RecoveredUSD.IsZero())
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("type = ?", "commission_deduct").Count(&ledgers).Error)
	require.Zero(t, ledgers)
	var wallets []model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ?", site.Id).Find(&wallets).Error)
	for _, wallet := range wallets {
		require.True(t, wallet.Balance.IsZero(), wallet.Kind)
	}
}

func TestSiteInviteCommissionConvertsAvailableRemainderToInviterQuota(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(3), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-convert",
	})
	require.NoError(t, err)
	require.NoError(t, setInviteCoverageForTest(site, "10", "7"))
	ext := siteInviteOrder(t, site.Id, invitee.Id, "SITE-CVT", decimal.NewFromInt(30))
	var row model.AdtySiteInviteCommission
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := RecordAdtySiteInviteCommissionTx(tx, ext, time.Unix(1_700_000_000, 0)); err != nil {
			return err
		}
		if err := tx.Where("trade_no = ?", "SITE-CVT").First(&row).Error; err != nil {
			return err
		}
		return CoverAdtySiteInviteCommissionTx(tx, row.Id, time.Unix(1_700_000_100, 0))
	}))
	require.NoError(t, db.First(&row, row.Id).Error)
	var invite model.AdtySiteInvite
	require.NoError(t, db.Where("site_id = ? AND invitee_id = ?", site.Id, invitee.Id).First(&invite).Error)
	require.Error(t, ConvertAdtySiteInviteCommission(invite.InviterId, row.Id, decimal.NewFromInt(1), "cvt-1"))

	require.NoError(t, ReleaseAdtySiteInviteCommissions(time.Unix(row.AvailableAt, 0)))
	require.NoError(t, ConvertAdtySiteInviteCommission(invite.InviterId, row.Id, decimal.NewFromInt(1), "cvt-1"))
	require.NoError(t, ConvertAdtySiteInviteCommission(invite.InviterId, row.Id, decimal.NewFromInt(1), "cvt-1"))
	var inviter model.User
	require.NoError(t, db.First(&inviter, invite.InviterId).Error)
	require.Equal(t, 500000, inviter.Quota)
	var convertLots []model.AdtyFundBatch
	require.NoError(t, db.Where("user_id = ? AND source = ?", inviter.Id, "site-comm-cvt:"+strconv.FormatInt(row.Id, 10)+":cvt-1").Find(&convertLots).Error)
	require.Len(t, convertLots, 1)
	require.Equal(t, "principal", convertLots[0].Kind)
	require.EqualValues(t, 500000, convertLots[0].Remaining)
	require.NoError(t, db.First(&row, row.Id).Error)
	require.True(t, row.ConvertedUSD.Equal(decimal.NewFromInt(1)))
	var inviteeUser model.User
	require.NoError(t, db.First(&inviteeUser, invitee.Id).Error)
	require.Equal(t, 0, inviteeUser.Quota)
	var earning model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.NewFromInt(2)))
	require.True(t, earning.FrozenUSD.Equal(decimal.NewFromInt(2)))
	require.Error(t, ConvertAdtySiteInviteCommission(invitee.Id, row.Id, decimal.NewFromInt(1), "cvt-other"))

	ext.CreditQuota = 30 * 500000
	ext.RefundQuota = 30 * 500000
	require.NoError(t, db.AutoMigrate(model.ReferralTables()...))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.AdtyAfterTopUpRefundedTx(tx, &model.TopUp{TradeNo: "SITE-CVT", UserId: invitee.Id}, ext)
	}))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.True(t, row.ClawedUSD.Equal(decimal.NewFromInt(3)))
	require.True(t, row.RecoveredUSD.Equal(decimal.NewFromInt(2)))
	require.True(t, row.RecoveryUSD.IsZero())
	require.True(t, row.ConvertedClawedUSD.Equal(decimal.NewFromInt(1)))
	require.NoError(t, db.First(&inviter, invite.InviterId).Error)
	require.Equal(t, 0, inviter.Quota)
	require.NoError(t, db.Where("user_id = ? AND source = ?", inviter.Id, "site-comm-cvt:"+strconv.FormatInt(row.Id, 10)+":cvt-1").Find(&convertLots).Error)
	require.Len(t, convertLots, 1)
	require.EqualValues(t, 0, convertLots[0].Remaining, "converted commission clawback must reclaim fund batch remaining quota")
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.NewFromInt(2)))
	require.True(t, earning.FrozenUSD.IsZero())
}

func TestResumeSiteInviteCoverageFundsOriginalRowsInOrder(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(3), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-resume",
	})
	require.NoError(t, err)
	require.NoError(t, setInviteCoverageForTest(site, "2", "7"))

	older := siteInviteOrder(t, site.Id, invitee.Id, "SITE-OLD", decimal.NewFromInt(30))
	newer := siteInviteOrder(t, site.Id, invitee.Id, "SITE-NEW", decimal.NewFromInt(30))
	var oldRow, newRow model.AdtySiteInviteCommission
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := RecordAdtySiteInviteCommissionTx(tx, older, time.Unix(1_700_000_000, 0)); err != nil {
			return err
		}
		if err := RecordAdtySiteInviteCommissionTx(tx, newer, time.Unix(1_700_000_010, 0)); err != nil {
			return err
		}
		if err := tx.Where("trade_no = ?", "SITE-OLD").First(&oldRow).Error; err != nil {
			return err
		}
		return CoverAdtySiteInviteCommissionTx(tx, oldRow.Id, time.Unix(1_700_000_100, 0))
	}))
	require.NoError(t, db.First(&oldRow, oldRow.Id).Error)
	require.Equal(t, "pending_coverage", oldRow.Status)
	var earning model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.NewFromInt(3)))
	require.True(t, earning.FrozenUSD.IsZero(), "a backlog at the cap does not freeze earnings on one row")

	require.NoError(t, ResumeAdtySiteInviteCoverage(site.Id, time.Unix(1_700_000_200, 0)))
	require.NoError(t, db.First(&oldRow, oldRow.Id).Error)
	require.NoError(t, db.Where("trade_no = ?", "SITE-NEW").First(&newRow).Error)
	require.Equal(t, "covered", oldRow.Status)
	require.Equal(t, "pending_coverage", newRow.Status)
	require.Equal(t, int64(1_700_000_200+7*86400), oldRow.AvailableAt)
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.NewFromInt(3)))
	require.True(t, earning.FrozenUSD.Equal(decimal.NewFromInt(3)))
	var count int64
	require.NoError(t, db.Model(&model.AdtySiteInviteCommission{}).Count(&count).Error)
	require.Equal(t, int64(2), count)

	require.NoError(t, ResumeAdtySiteInviteCoverage(site.Id, time.Unix(1_700_000_300, 0)))
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.NewFromInt(3)))
	require.True(t, earning.FrozenUSD.Equal(decimal.NewFromInt(3)))
	require.NoError(t, db.Model(&model.AdtySiteInviteCommission{}).Count(&count).Error)
	require.Equal(t, int64(2), count)
}

func TestResumeSiteInviteCoverageCompletesPartialFreezeAfterTopUp(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(1), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-partial",
	})
	require.NoError(t, err)
	require.NoError(t, setInviteCoverageForTest(site, "10", "7"))

	ext := siteInviteOrder(t, site.Id, invitee.Id, "SITE-PARTIAL", decimal.NewFromInt(30))
	var row model.AdtySiteInviteCommission
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := RecordAdtySiteInviteCommissionTx(tx, ext, time.Unix(1_700_000_000, 0)); err != nil {
			return err
		}
		if err := tx.Where("trade_no = ?", "SITE-PARTIAL").First(&row).Error; err != nil {
			return err
		}
		return CoverAdtySiteInviteCommissionTx(tx, row.Id, time.Unix(1_700_000_100, 0))
	}))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, "pending_coverage", row.Status)
	require.True(t, row.CoveredUSD.Equal(decimal.NewFromInt(1)))
	require.True(t, row.EarningsHoldUSD.Equal(decimal.NewFromInt(1)))
	var earning model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.NewFromInt(1)))
	require.True(t, earning.FrozenUSD.Equal(decimal.NewFromInt(1)))
	var first model.AdtySiteWalletLedger
	require.NoError(t, db.Where("idempotency_key = ?", "commission:SITE-PARTIAL:earnings").First(&first).Error)
	require.True(t, first.HoldDelta.Equal(decimal.NewFromInt(1)))

	_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(2), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-topup",
	})
	require.NoError(t, err)

	require.NoError(t, ResumeAdtySiteInviteCoverage(site.Id, time.Unix(1_700_000_200, 0)))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, "covered", row.Status)
	require.True(t, row.CoveredUSD.Equal(decimal.NewFromInt(3)))
	require.True(t, row.EarningsHoldUSD.Equal(decimal.NewFromInt(3)))
	require.Equal(t, int64(1_700_000_200+7*86400), row.AvailableAt)
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.NewFromInt(3)))
	require.True(t, earning.FrozenUSD.Equal(decimal.NewFromInt(3)))
	var rest model.AdtySiteWalletLedger
	require.NoError(t, db.Where("idempotency_key = ?", "commission:SITE-PARTIAL:earnings:3").First(&rest).Error)
	require.True(t, rest.HoldDelta.Equal(decimal.NewFromInt(2)))
	var count int64
	require.NoError(t, db.Model(&model.AdtySiteInviteCommission{}).Count(&count).Error)
	require.Equal(t, int64(1), count)

	require.NoError(t, ResumeAdtySiteInviteCoverage(site.Id, time.Unix(1_700_000_300, 0)))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, "covered", row.Status)
	require.True(t, row.CoveredUSD.Equal(decimal.NewFromInt(3)))
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.Balance.Equal(decimal.NewFromInt(3)))
	require.True(t, earning.FrozenUSD.Equal(decimal.NewFromInt(3)))
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).
		Where("type = ? AND ref_id = ?", "commission_freeze", "SITE-PARTIAL").Count(&ledgers).Error)
	require.Equal(t, int64(2), ledgers)
}

func TestResumeSiteInviteCoverageCompletesPartialMarginFreezeAfterTopUp(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(1), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-margin-partial",
	})
	require.NoError(t, err)
	require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.NewFromInt(100), "margin-partial-cycle"))
	require.NoError(t, db.Model(&model.AdtySiteWallet{}).Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).Update("cycle_used_usd", "89").Error)
	require.NoError(t, setInviteCoverageForTest(site, "10", "7"))
	require.NoError(t, AuthorizeAdtySiteCommissionMargin(site.Id, true))

	ext := siteInviteOrder(t, site.Id, invitee.Id, "SITE-MPART", decimal.NewFromInt(30))
	var row model.AdtySiteInviteCommission
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := RecordAdtySiteInviteCommissionTx(tx, ext, time.Unix(1_700_000_000, 0)); err != nil {
			return err
		}
		if err := tx.Where("trade_no = ?", "SITE-MPART").First(&row).Error; err != nil {
			return err
		}
		return CoverAdtySiteInviteCommissionTx(tx, row.Id, time.Unix(1_700_000_100, 0))
	}))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, "pending_coverage", row.Status)
	require.True(t, row.CoveredUSD.Equal(decimal.NewFromInt(2)))
	require.True(t, row.EarningsHoldUSD.Equal(decimal.NewFromInt(1)))
	require.True(t, row.MarginHoldUSD.Equal(decimal.NewFromInt(1)))
	var first model.AdtySiteWalletLedger
	require.NoError(t, db.Where("idempotency_key = ?", "commission:SITE-MPART:margin").First(&first).Error)
	require.True(t, first.HoldDelta.Equal(decimal.NewFromInt(1)))

	require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.NewFromInt(100), "margin-partial-topup"))
	require.NoError(t, ResumeAdtySiteInviteCoverage(site.Id, time.Unix(1_700_000_200, 0)))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, "covered", row.Status)
	require.True(t, row.CoveredUSD.Equal(decimal.NewFromInt(3)))
	require.True(t, row.EarningsHoldUSD.Equal(decimal.NewFromInt(1)))
	require.True(t, row.MarginHoldUSD.Equal(decimal.NewFromInt(2)))
	var rest model.AdtySiteWalletLedger
	require.NoError(t, db.Where("idempotency_key = ?", "commission:SITE-MPART:margin:2").First(&rest).Error)
	require.True(t, rest.HoldDelta.Equal(decimal.NewFromInt(1)))
	var margin model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&margin).Error)
	require.True(t, margin.Balance.Equal(decimal.NewFromInt(200)))
	require.True(t, margin.FrozenUSD.Equal(decimal.NewFromInt(2)))

	require.NoError(t, ResumeAdtySiteInviteCoverage(site.Id, time.Unix(1_700_000_300, 0)))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.Equal(t, "covered", row.Status)
	require.True(t, row.MarginHoldUSD.Equal(decimal.NewFromInt(2)))
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&margin).Error)
	require.True(t, margin.FrozenUSD.Equal(decimal.NewFromInt(2)))
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).
		Where("type = ? AND ref_id = ? AND wallet_kind = ?", "commission_freeze", "SITE-MPART", model.AdtySiteWalletMargin).Count(&ledgers).Error)
	require.Equal(t, int64(2), ledgers)
}

func TestSiteReferralOffDoesNotRecordCommissionOnThatSite(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, SetAdtyOwnerSiteReferralEnabled(site.Id, site.OwnerUserId, false))
	ext := siteInviteOrder(t, site.Id, invitee.Id, "SITE-OFF", decimal.NewFromInt(30))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return RecordAdtySiteInviteCommissionTx(tx, ext, time.Unix(1, 0))
	}))
	var count int64
	require.NoError(t, db.Model(&model.AdtySiteInviteCommission{}).Count(&count).Error)
	require.Zero(t, count)

	other, otherInvitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	otherOrder := siteInviteOrder(t, other.Id, otherInvitee.Id, "SITE-ON", decimal.NewFromInt(20))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return RecordAdtySiteInviteCommissionTx(tx, otherOrder, time.Unix(2, 0))
	}))
	var otherRow model.AdtySiteInviteCommission
	require.NoError(t, db.Where("trade_no = ?", "SITE-ON").First(&otherRow).Error)
	require.Equal(t, other.Id, otherRow.SiteId)

	require.NoError(t, SetAdtyOwnerSiteReferralEnabled(site.Id, site.OwnerUserId, true))
	ext = siteInviteOrder(t, site.Id, invitee.Id, "SITE-REENABLED", decimal.NewFromInt(30))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return RecordAdtySiteInviteCommissionTx(tx, ext, time.Unix(3, 0))
	}))
	var restored model.AdtySiteInviteCommission
	require.NoError(t, db.Where("trade_no = ?", "SITE-REENABLED").First(&restored).Error)
	require.Equal(t, site.Id, restored.SiteId)
}

func TestInviterSeesPendingSiteCommissionBeforeItIsWithdrawable(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	ext := siteInviteOrder(t, site.Id, invitee.Id, "SITE-SEE", decimal.NewFromInt(30))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return RecordAdtySiteInviteCommissionTx(tx, ext, time.Unix(1_700_000_000, 0))
	}))
	var invite model.AdtySiteInvite
	require.NoError(t, db.Where("site_id = ? AND invitee_id = ?", site.Id, invitee.Id).First(&invite).Error)
	rows, err := ListAdtySiteInviteCommissions(invite.InviterId, time.Unix(1_700_000_000, 0))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "pending_coverage", rows[0].Status)
	require.Equal(t, "SITE-SEE", rows[0].TradeNo)
	require.False(t, SiteInviteCommissionWithdrawable(&rows[0], time.Unix(1_700_000_000, 0)))
}

func TestBindSiteInviteRejectsCrossSiteAndPaidCustomers(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, _ := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	var invite model.AdtySiteInvite
	require.NoError(t, db.Where("site_id = ?", site.Id).First(&invite).Error)
	outsider := referralUser(t, db, "other-site-inviter")
	sameSite := referralUser(t, db, "same-site-invitee")
	require.NoError(t, db.Model(sameSite).Update("site_id", site.Id).Error)
	require.Error(t, BindSiteInviteFromIP(site.Id, outsider.Id, sameSite.Id, ""))
	var created int64
	require.NoError(t, db.Model(&model.AdtySiteInvite{}).Where("invitee_id = ?", sameSite.Id).Count(&created).Error)
	require.Zero(t, created)

	require.NoError(t, db.Create(&model.TopUp{
		UserId: sameSite.Id, Amount: 5, Money: 5, TradeNo: "fresh-paid",
		Status: common.TopUpStatusSuccess,
	}).Error)
	require.Error(t, BindSiteInviteFromIP(site.Id, invite.InviterId, sameSite.Id, ""))
	require.NoError(t, db.Model(&model.AdtySiteInvite{}).Where("invitee_id = ?", sameSite.Id).Count(&created).Error)
	require.Zero(t, created)
}

func TestSiteInviteCommissionIgnoresTeamTopups(t *testing.T) {
	db := prepareReferralDialectDB(t, "sqlite")
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	ext := siteInviteOrder(t, site.Id, invitee.Id, "TEAM-1", decimal.NewFromInt(10))
	ext.TeamId = 9
	require.NoError(t, PrepareAdtySiteInviteCommissionSnapshotTx(db, nil, ext))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return RecordAdtySiteInviteCommissionTx(tx, ext, time.Unix(1, 0))
	}))
	var count int64
	require.NoError(t, db.Model(&model.AdtySiteInviteCommission{}).Count(&count).Error)
	require.Zero(t, count)
	_ = common.QuotaPerUnit
}

func TestRefundPreviewItemizesSplitCommissionToMatchAfterRefundSplitter(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(4), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-split-" + strconv.Itoa(site.Id),
	})
	require.NoError(t, err)
	var invite model.AdtySiteInvite
	require.NoError(t, db.Where("site_id = ? AND invitee_id = ?", site.Id, invitee.Id).First(&invite).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", invite.InviterId).Update("quota", 3*500000).Error)
	trade := "SITE-SPLIT-" + strconv.Itoa(site.Id)
	row := model.AdtySiteInviteCommission{
		SiteId: site.Id, InviterId: invite.InviterId, InviteeId: invitee.Id, TradeNo: trade,
		FaceUSD: decimal.NewFromInt(100), Rate: decimal.RequireFromString("0.10"),
		AmountUSD: decimal.NewFromInt(10), EarningsHoldUSD: decimal.NewFromInt(4),
		ConvertedUSD: decimal.NewFromInt(3), WithdrawnUSD: decimal.NewFromInt(2),
		Status: model.AdtySiteCommissionAvailable,
	}
	require.NoError(t, db.Create(&row).Error)
	require.NoError(t, db.Model(&model.AdtySiteWallet{}).Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).
		Update("frozen_usd", decimal.NewFromInt(4)).Error)
	ext := siteInviteOrder(t, site.Id, invitee.Id, trade, decimal.NewFromInt(100))
	ext.CreditQuota = 100 * 500000
	ext.RefundQuota = 100 * 500000

	preview, err := model.LoadSiteInviteCommissionRefundPreview(db, trade, ext.RefundQuota, ext.FaceQuota())
	require.NoError(t, err)
	require.NotNil(t, preview)
	require.Equal(t, "4", preview.ExpectedReleaseUSD)
	require.Equal(t, "3", preview.ExpectedConvertedClawbackUSD)
	require.Equal(t, "2", preview.ExpectedWithdrawnRecoveryUSD)
	require.Equal(t, "1", preview.ExpectedPendingCoverageCancelUSD)
	require.NotEqual(t, preview.ExpectedReleaseUSD, preview.ExpectedConvertedClawbackUSD)
	require.NotEqual(t, preview.ExpectedConvertedClawbackUSD, preview.ExpectedWithdrawnRecoveryUSD)
	require.NotEqual(t, preview.ExpectedWithdrawnRecoveryUSD, preview.ExpectedPendingCoverageCancelUSD)
	require.Equal(t, "inviter", preview.WithdrawnRecoveryLiable)
	got := map[string]string{}
	for _, state := range preview.States {
		got[state.State] = state.AmountUSD
		require.NotEqual(t, model.AdtySiteCommissionCovered, state.State)
	}
	require.Equal(t, "4", got[model.AdtySiteCommissionStateOnHold])
	require.Equal(t, "1", got[model.AdtySiteCommissionStatePendingCoverage])
	require.Equal(t, "3", got[model.AdtySiteCommissionStateConverted])
	require.Equal(t, "2", got[model.AdtySiteCommissionStateWithdrawnReleased])

	views, err := model.PresentSiteInviteCommissions(db, []model.AdtySiteInviteCommission{row})
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.Equal(t, preview.States, views[0].OfficialStates)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.AdtyAfterTopUpRefundedTx(tx, &model.TopUp{TradeNo: trade, UserId: invitee.Id}, ext)
	}))
	require.NoError(t, db.First(&row, row.Id).Error)
	require.True(t, row.RecoveredUSD.Equal(decimal.RequireFromString(preview.ExpectedReleaseUSD)))
	require.True(t, row.ConvertedClawedUSD.Equal(decimal.RequireFromString(preview.ExpectedConvertedClawbackUSD)))
	require.True(t, row.ClawedUSD.Equal(decimal.RequireFromString(preview.ExpectedClawbackUSD)))
	var liability model.AdtyCommissionRecovery
	require.NoError(t, db.Where("commission_id = ? AND kind = ?", row.Id, "withdrawn").First(&liability).Error)
	require.True(t, liability.AmountUSD.Equal(decimal.RequireFromString(preview.ExpectedWithdrawnRecoveryUSD)))
	require.Equal(t, "withdrawn", liability.Kind)
	var inviter model.User
	require.NoError(t, db.First(&inviter, invite.InviterId).Error)
	require.Equal(t, 0, inviter.Quota)
	var earning model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
	require.True(t, earning.FrozenUSD.IsZero())
}

func paidSiteCommissionOrderForTest(t *testing.T, siteID, userID int, trade string) {
	t.Helper()
	top, ext := paymentSceneOrder(userID, trade)
	ext.SiteId, ext.FaceUSD, ext.PayAmount, ext.CreditQuota = siteID, decimal.NewFromInt(30), decimal.NewFromInt(30), 15_000_000
	ext.TopupGroupRatio = decimal.NewFromInt(1)
	require.NoError(t, model.CreateAdtyTopUp(top, ext))
	_, err := model.CompleteAdtyTopUp(top.TradeNo, model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(30), FiatAmount: decimal.NewFromInt(30),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)
}

func TestConvertedSiteCommissionClawbackReclaimsFundBatches(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	require.NoError(t, db.AutoMigrate(model.ReferralTables()...))
	require.NoError(t, db.AutoMigrate(&model.AdtySiteInviteWithdrawal{}, &model.AdtySiteInviteWithdrawalItem{}))
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	require.NoError(t, setInviteCoverageForTest(site, "100", "7"))

	var invite model.AdtySiteInvite
	require.NoError(t, db.Where("site_id = ? AND invitee_id = ?", site.Id, invitee.Id).First(&invite).Error)

	paidSiteCommissionOrderForTest(t, site.Id, invitee.Id, "SITE-BATCH-CLAW")
	trade := fmt.Sprintf("scene-%d-SITE-BATCH-CLAW", invitee.Id)
	now := time.Unix(1_700_000_000, 0)
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(10), AdtySiteWalletChange{
		Type: "earning_settlement", IdempotencyKey: "earn-batch-claw-" + strconv.Itoa(site.Id),
	})
	require.NoError(t, err)
	var row model.AdtySiteInviteCommission
	require.NoError(t, db.Where("trade_no = ?", trade).First(&row).Error)
	require.NoError(t, ResumeAdtySiteInviteCoverage(site.Id, now))
	require.NoError(t, ReleaseAdtySiteInviteCommissions(now.Add(8*24*time.Hour)))

	// Convert $1 via ConvertAdtySiteInviteCommission and $2 via CreateAdtySiteInviteWithdrawal(method="balance").
	require.NoError(t, ConvertAdtySiteInviteCommission(invite.InviterId, row.Id, decimal.NewFromInt(1), "cvt-lot"))
	wd, err := CreateAdtySiteInviteWithdrawal(AdtySiteInviteWithdrawalInput{
		UserID: invite.InviterId, SiteID: site.Id, Amount: decimal.NewFromInt(2),
		Method: "balance", IdempotencyKey: "wd-bal-lot",
	})
	require.NoError(t, err)

	var totalBefore int64
	require.NoError(t, db.Model(&model.AdtyFundBatch{}).Where("user_id = ?", invite.InviterId).
		Select("COALESCE(SUM(remaining), 0)").Scan(&totalBefore).Error)
	require.EqualValues(t, 3*500_000, totalBefore)

	// Inviter spends $0.50 (250,000 quota) before the invitee's order is refunded.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		before, debited, err := model.AdtyDebitUserQuotaTx(tx, invite.InviterId, 250_000)
		if err != nil {
			return err
		}
		return model.ConsumeAdtyFundBatchesQuietTx(tx, invite.InviterId, debited, before)
	}))

	operator := referralUser(t, db, "claw-root")
	require.NoError(t, db.Model(operator).Update("role", common.RoleRootUser).Error)
	injected := errors.New("commission fund-batch update failed")
	const callback = "test:commission-claw-batch-failure"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == (model.AdtyFundBatch{}).TableName() {
			var count int64
			check := tx.Session(&gorm.Session{NewDB: true}).Model(&model.AdtyBalanceLedger{}).
				Where("user_id = ? AND reason = ?", invite.InviterId, "site invite commission clawback").Count(&count)
			if check.Error != nil {
				tx.AddError(check.Error)
			} else if count > 0 {
				tx.AddError(injected)
			}
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })
	_, err = completeManualRefundForTest(t, trade, "regression refund", operator.Id, "claw-refund", decimal.NewFromInt(30))
	require.ErrorIs(t, err, injected)
	require.NoError(t, db.Callback().Update().Remove(callback))
	var unchanged model.User
	require.NoError(t, db.First(&unchanged, invite.InviterId).Error)
	require.EqualValues(t, 1_250_000, unchanged.Quota, "batch failure rolls back commission quota clawback")
	var clawLedgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Where("user_id = ? AND reason = ?", invite.InviterId, "site invite commission clawback").Count(&clawLedgers).Error)
	require.Zero(t, clawLedgers, "failed clawback leaves no successful balance ledger")
	_, err = completeManualRefundForTest(t, trade, "regression refund", operator.Id, "claw-refund", decimal.NewFromInt(30))
	require.NoError(t, err)
	_, err = completeManualRefundForTest(t, trade, "regression refund", operator.Id, "claw-refund", decimal.NewFromInt(30))
	require.NoError(t, err)

	var inviter model.User
	require.NoError(t, db.First(&inviter, invite.InviterId).Error)
	require.Equal(t, 0, inviter.Quota)

	var totalAfter int64
	require.NoError(t, db.Model(&model.AdtyFundBatch{}).Where("user_id = ?", invite.InviterId).
		Select("COALESCE(SUM(remaining), 0)").Scan(&totalAfter).Error)
	require.EqualValues(t, 0, totalAfter, "all converted commission fund batches must be reclaimed on clawback")

	var wdBatch model.AdtyFundBatch
	require.NoError(t, db.Where("user_id = ? AND source = ?", invite.InviterId, fmt.Sprintf("site-invite-wd:%d", wd.Id)).First(&wdBatch).Error)
	require.EqualValues(t, 0, wdBatch.Remaining)

	var recovery model.AdtyCommissionRecovery
	require.NoError(t, db.Where("commission_id = ? AND kind = ?", row.Id, "converted").First(&recovery).Error)
	require.True(t, recovery.AmountUSD.Equal(decimal.RequireFromString("0.5")), "unrecovered $0.50 shortfall must be recorded as converted recovery")
}

func TestPendingSiteCommissionCoverageUsesPostRefundNetEntitlement(t *testing.T) {
	for _, tc := range []struct {
		name, initial, covered, recovered, refund, claw string
	}{
		{"uncovered", "0", "1.5", "0", "15", "1.5"},
		{"partially_covered", "1", "2", "0.5", "15", "1.5"},
		{"uncovered_full_refund", "0", "0", "0", "30", "3"},
		{"partially_covered_full_refund", "1", "1", "1", "30", "3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := prepareReferralDialectDB(t, "postgres")
			require.NoError(t, db.AutoMigrate(model.ReferralTables()...))
			site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
			require.NoError(t, EnsureAdtySiteWallets(site.Id))
			require.NoError(t, setInviteCoverageForTest(site, "100", "7"))

			var invite model.AdtySiteInvite
			require.NoError(t, db.Where("site_id = ? AND invitee_id = ?", site.Id, invitee.Id).First(&invite).Error)

			if tc.initial != "0" {
				_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.RequireFromString(tc.initial), AdtySiteWalletChange{
					Type: "earning_settlement", IdempotencyKey: "initial-coverage",
				})
				require.NoError(t, err)
			}
			// Both zero coverage and a partial hold must cancel only the refunded entitlement.
			paidSiteCommissionOrderForTest(t, site.Id, invitee.Id, "SITE-NET-COV")
			trade := fmt.Sprintf("scene-%d-SITE-NET-COV", invitee.Id)
			now := time.Unix(1_700_000_000, 0)
			var row model.AdtySiteInviteCommission
			require.NoError(t, db.Where("trade_no = ?", trade).First(&row).Error)
			require.Equal(t, model.AdtySiteCommissionPendingCoverage, row.Status)
			require.Equal(t, tc.initial, row.CoveredUSD.String())

			// Refund principal while the commission is still pending_coverage.
			operator := referralUser(t, db, "net-root")
			require.NoError(t, db.Model(operator).Update("role", common.RoleRootUser).Error)
			_, err := completeManualRefundForTest(t, trade, "coverage refund", operator.Id, "net-refund", decimal.RequireFromString(tc.refund))
			require.NoError(t, err)
			_, err = completeManualRefundForTest(t, trade, "coverage refund", operator.Id, "net-refund", decimal.RequireFromString(tc.refund))
			require.NoError(t, err, "refund replay must not release the hold twice")
			require.NoError(t, db.First(&row, row.Id).Error)
			require.Equal(t, tc.claw, row.ClawedUSD.String())
			require.Equal(t, tc.recovered, row.RecoveredUSD.String())

			// Site now earns $5 in its earnings wallet and resumes coverage.
			_, err = ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(5), AdtySiteWalletChange{
				Type: "earning_settlement", IdempotencyKey: "earn-net-cov-" + strconv.Itoa(site.Id),
			})
			require.NoError(t, err)
			require.NoError(t, ResumeAdtySiteInviteCoverage(site.Id, now.Add(time.Hour)))

			require.NoError(t, db.First(&row, row.Id).Error)
			if tc.refund == "30" {
				require.Equal(t, model.AdtySiteCommissionClawedBack, row.Status)
				require.True(t, row.EarningsHoldUSD.IsZero())
				var earning model.AdtySiteWallet
				require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
				require.True(t, earning.FrozenUSD.IsZero())
				require.Error(t, ConvertAdtySiteInviteCommission(invite.InviterId, row.Id, decimal.NewFromInt(1), "canceled-net"))
				return
			}
			require.Equal(t, model.AdtySiteCommissionCovered, row.Status)
			require.Equal(t, tc.covered, row.CoveredUSD.String(), "coverage preserves the released historical hold without funding canceled entitlement")
			require.True(t, row.EarningsHoldUSD.Equal(decimal.RequireFromString("1.5")), "earnings_hold_usd must only freeze $1.5, got %s", row.EarningsHoldUSD)

			var earning model.AdtySiteWallet
			require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earning).Error)
			require.True(t, earning.FrozenUSD.Equal(decimal.RequireFromString("1.5")), "site wallet frozen_usd must be $1.5, got %s", earning.FrozenUSD)

			require.NoError(t, ReleaseAdtySiteInviteCommissions(now.Add(9*24*time.Hour)))
			require.Error(t, ConvertAdtySiteInviteCommission(invite.InviterId, row.Id, decimal.NewFromInt(2), "over-net"))
			require.NoError(t, ConvertAdtySiteInviteCommission(invite.InviterId, row.Id, decimal.RequireFromString("1.5"), "exact-net"))
		})
	}
}

func TestConvertedSiteCommissionRefundPreservesInFlightLots(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	require.NoError(t, db.AutoMigrate(model.ReferralTables()...))
	site, buyer := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	require.NoError(t, setInviteCoverageForTest(site, "100", "7"))
	paidSiteCommissionOrderForTest(t, site.Id, buyer.Id, "inflight-claw")
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(10), AdtySiteWalletChange{Type: "earning_settlement", IdempotencyKey: "inflight-earning"})
	require.NoError(t, err)
	trade := fmt.Sprintf("scene-%d-inflight-claw", buyer.Id)
	var row model.AdtySiteInviteCommission
	require.NoError(t, db.Where("trade_no = ?", trade).First(&row).Error)
	now := time.Unix(1_700_000_000, 0)
	require.NoError(t, ResumeAdtySiteInviteCoverage(site.Id, now))
	require.NoError(t, ReleaseAdtySiteInviteCommissions(now.Add(8*24*time.Hour)))
	require.NoError(t, ConvertAdtySiteInviteCommission(row.InviterId, row.Id, decimal.NewFromInt(3), "inflight-convert"))
	reserved, err := model.TryReserveUserQuotaForOperation(row.InviterId, 250_000, "commission-inflight")
	require.NoError(t, err)
	require.True(t, reserved)
	operator := referralUser(t, db, "inflight-root")
	require.NoError(t, db.Model(operator).Update("role", common.RoleRootUser).Error)
	_, err = completeManualRefundForTest(t, trade, "partial commission refund", operator.Id, "inflight-half", decimal.NewFromInt(15))
	require.NoError(t, err)
	var inviter model.User
	require.NoError(t, db.First(&inviter, row.InviterId).Error)
	require.EqualValues(t, 500_000, inviter.Quota)
	var remaining, held int64
	require.NoError(t, db.Model(&model.AdtyFundBatch{}).Where("user_id = ?", row.InviterId).Select("COALESCE(SUM(remaining), 0)").Scan(&remaining).Error)
	require.NoError(t, db.Model(&model.AdtyFundAllocation{}).Where("user_id = ?", row.InviterId).Select("COALESCE(SUM(held - settled), 0)").Scan(&held).Error)
	require.EqualValues(t, 750_000, remaining)
	require.EqualValues(t, 250_000, held, "refund cannot seize the running request's allocation")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		backed, err := model.SettleAdtyFundAllocationsTx(tx, row.InviterId, "commission-inflight", 250_000, 500_000)
		if err == nil && backed != 250_000 {
			return fmt.Errorf("unexpected backed quota: %d", backed)
		}
		return err
	}))
	_, err = completeManualRefundForTest(t, trade, "remaining commission refund", operator.Id, "inflight-rest", decimal.NewFromInt(15))
	require.NoError(t, err)
	require.NoError(t, db.First(&inviter, row.InviterId).Error)
	require.Zero(t, inviter.Quota)
	require.NoError(t, db.Model(&model.AdtyFundBatch{}).Where("user_id = ?", row.InviterId).Select("COALESCE(SUM(remaining), 0)").Scan(&remaining).Error)
	require.Zero(t, remaining)
	var recovery model.AdtyCommissionRecovery
	require.NoError(t, db.Where("commission_id = ? AND kind = ?", row.Id, "converted").First(&recovery).Error)
	require.Equal(t, "0.5", recovery.AmountUSD.String())
}

func TestPaymentOrderBonusUsesCurrentDatabasePolicy(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	require.NoError(t, db.AutoMigrate(model.ReferralTables()...))
	old := *setting.GetBEpusdtSetting()
	*setting.GetBEpusdtSetting() = setting.BEpusdtSetting{Enabled: true, BaseURL: "https://bonus.example", Secret: "synthetic-bonus-secret", TradeTypes: []string{"usdt.trc20"}}
	t.Cleanup(func() { *setting.GetBEpusdtSetting() = old })
	site, buyer := seedSiteInvite(t, db, decimal.Zero)
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(20), AdtySiteWalletChange{Type: "earning_settlement", IdempotencyKey: "bonus-earning"})
	require.NoError(t, err)
	config, err := GetAdtyPaymentScenes()
	require.NoError(t, err)
	key := config.Scenes[0].Key
	policy := map[string]string{
		adty_setting.TopupSettingKeySiteMethodAuth:  fmt.Sprintf(`{"%d":["usdt"]}`, site.Id),
		adty_setting.TopupSettingKeySiteMethodBonus: fmt.Sprintf(`{"%d":{"usdt":{"enabled":true,"rate":"0.05"}}}`, site.Id),
	}
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error { return model.SaveOptionsTx(tx, policy) }))
	for i, want := range []struct {
		bonus string
		quota int64
	}{{"5", 52_500_000}, {"0", 50_000_000}} {
		top, ext := paymentSceneOrder(buyer.Id, fmt.Sprintf("bonus-%d", i))
		ext.SiteId, ext.BonusRate, ext.BonusUSD, ext.CreditQuota = site.Id, decimal.RequireFromString("0.99"), decimal.NewFromInt(99), 99_500_000
		require.NoError(t, CreateAdtyPaymentOrder(top, ext, key))
		require.Equal(t, want.bonus, ext.BonusUSD.String())
		require.EqualValues(t, want.quota, ext.CreditQuota)
		if i == 0 {
			require.Equal(t, "5", ext.GiftReserveEarning.String())
			policy[adty_setting.TopupSettingKeySiteMethodAuth] = `{}`
			require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error { return model.SaveOptionsTx(tx, policy) }))
			_, err = model.CompleteAdtyTopUp(top.TradeNo, model.AdtyPaymentFacts{Provider: "bepusdt", PaidAmount: decimal.NewFromInt(100), FiatAmount: decimal.NewFromInt(100), PaidCurrency: "USDT", Source: "callback"}, "")
			require.NoError(t, err)
			stored, err := model.GetAdtyTopupOrder(top.TradeNo)
			require.NoError(t, err)
			require.Equal(t, "5", stored.BonusUSD.String(), "existing promise survives revocation")
		}
	}
}

func TestSiteReferralOrderPolicySnapshotFrozenAtOrderCreation(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	require.NoError(t, db.AutoMigrate(model.ReferralTables()...))
	site, invitee := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	require.NoError(t, setInviteCoverageForTest(site, "100", "7"))

	var invite model.AdtySiteInvite
	require.NoError(t, db.Where("site_id = ? AND invitee_id = ?", site.Id, invitee.Id).First(&invite).Error)

	// 1. Order created while eligible at 10% rate and 7 observation days.
	credit := int64(30 * 500_000)
	require.NoError(t, model.CreateAdtyTopUp(
		&model.TopUp{UserId: invitee.Id, Amount: 30, TradeNo: "SNAP-ELIGIBLE-1", PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt, Status: common.TopUpStatusPending},
		&model.AdtyTopupOrder{
			TradeNo: "SNAP-ELIGIBLE-1", UserId: invitee.Id, Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
			FaceUSD: decimal.NewFromInt(30), CreditQuota: credit, PayAmount: decimal.NewFromInt(30), PayCurrency: "USDT",
			TopupGroupRatio: decimal.NewFromInt(1), ExpireAt: time.Now().Unix() + 900, SiteId: site.Id,
		},
	))

	// Mutate rate (10% -> 2%), disable site referral, and change observation days (7 -> 30) BEFORE payment completes.
	require.NoError(t, SaveAdtyOwnerSiteReferralRate(site.Id, site.OwnerUserId, "0.02"))
	require.NoError(t, db.Model(&model.AdtySiteReferralRate{}).Where("site_id = ?", site.Id).Update("enabled", false).Error)
	require.NoError(t, setInviteCoverageForTest(site, "100", "30"))

	// 2. Second order created while referral is disabled for a customer who has no invite yet.
	uninvited := referralUser(t, db, "snap-uninvited")
	require.NoError(t, db.Model(uninvited).Update("site_id", site.Id).Error)
	require.NoError(t, model.CreateAdtyTopUp(
		&model.TopUp{UserId: uninvited.Id, Amount: 30, TradeNo: "SNAP-INELIGIBLE-2", PaymentMethod: "usdt.trc20", PaymentProvider: model.PaymentProviderAdtyBEpusdt, Status: common.TopUpStatusPending},
		&model.AdtyTopupOrder{
			TradeNo: "SNAP-INELIGIBLE-2", UserId: uninvited.Id, Provider: model.PaymentProviderAdtyBEpusdt, Method: "usdt.trc20",
			FaceUSD: decimal.NewFromInt(30), CreditQuota: credit, PayAmount: decimal.NewFromInt(30), PayCurrency: "USDT",
			TopupGroupRatio: decimal.NewFromInt(1), ExpireAt: time.Now().Unix() + 900, SiteId: site.Id,
		},
	))

	// Now re-enable referral and bind an invite for uninvited BEFORE completing SNAP-INELIGIBLE-2.
	require.NoError(t, db.Model(&model.AdtySiteReferralRate{}).Where("site_id = ?", site.Id).Update("enabled", true).Error)
	require.NoError(t, db.Create(&model.AdtySiteInvite{
		SiteId: site.Id, InviterId: invite.InviterId, InviteeId: uninvited.Id, Rate: decimal.RequireFromString("0.10"),
	}).Error)

	// Complete both orders.
	_, err := model.CompleteAdtyTopUp("SNAP-ELIGIBLE-1", model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(30), FiatAmount: decimal.NewFromInt(30),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)

	_, err = model.CompleteAdtyTopUp("SNAP-INELIGIBLE-2", model.AdtyPaymentFacts{
		Provider: model.PaymentProviderAdtyBEpusdt, PaidAmount: decimal.NewFromInt(30), FiatAmount: decimal.NewFromInt(30),
		PaidCurrency: "USDT", Source: model.AdtyPaymentSourceCallback,
	}, "")
	require.NoError(t, err)

	var comm1 model.AdtySiteInviteCommission
	require.NoError(t, db.Where("trade_no = ?", "SNAP-ELIGIBLE-1").First(&comm1).Error)
	require.True(t, comm1.Rate.Equal(decimal.RequireFromString("0.10")), "order 1 must keep its creation-time 10%% rate, got %s", comm1.Rate)
	require.True(t, comm1.AmountUSD.Equal(decimal.NewFromInt(3)), "order 1 must keep its creation-time $3 amount, got %s", comm1.AmountUSD)
	require.Equal(t, int64(7), comm1.ObservationDays, "order 1 must keep its creation-time 7 observation days")

	var count2 int64
	require.NoError(t, db.Model(&model.AdtySiteInviteCommission{}).Where("trade_no = ?", "SNAP-INELIGIBLE-2").Count(&count2).Error)
	require.Zero(t, count2, "order 2 created before invite binding / while disabled must not earn commission")
}

func TestPaymentOrderBonusDatabaseMarginAuthorization(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	require.NoError(t, db.AutoMigrate(model.ReferralTables()...))
	oldProvider, oldTopup := *setting.GetBEpusdtSetting(), *adty_setting.GetTopupSetting()
	*setting.GetBEpusdtSetting() = setting.BEpusdtSetting{Enabled: true, BaseURL: "https://bonus.example", Secret: "synthetic-bonus-secret", TradeTypes: []string{"usdt.trc20"}}
	t.Cleanup(func() { *setting.GetBEpusdtSetting() = oldProvider; *adty_setting.GetTopupSetting() = oldTopup })
	site, buyer := seedSiteInvite(t, db, decimal.Zero)
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.NewFromInt(20), "bonus-margin-cycle"))
	config, err := GetAdtyPaymentScenes()
	require.NoError(t, err)
	for i, allowed := range []bool{true, false} {
		policy := map[string]string{
			adty_setting.TopupSettingKeySiteMethodAuth:      fmt.Sprintf(`{"%d":["usdt"]}`, site.Id),
			adty_setting.TopupSettingKeySiteMethodBonus:     fmt.Sprintf(`{"%d":{"usdt":{"enabled":true,"rate":"0.05"}}}`, site.Id),
			adty_setting.TopupSettingKeySiteBonusMarginFill: fmt.Sprintf(`{"%d":%t}`, site.Id, allowed),
		}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return model.SaveOptionsTx(tx, policy) }))
		adty_setting.GetTopupSetting().SiteBonusMarginFill = map[int]bool{site.Id: !allowed}
		top, ext := paymentSceneOrder(buyer.Id, fmt.Sprintf("margin-auth-%d", i))
		ext.SiteId, ext.BonusUSD, ext.BonusRate, ext.CreditQuota = site.Id, decimal.NewFromInt(5), decimal.RequireFromString("0.05"), 52_500_000
		err = CreateAdtyPaymentOrder(top, ext, config.Scenes[0].Key)
		if allowed {
			require.NoError(t, err)
			require.Equal(t, "5", ext.GiftReserveMargin.String())
		} else {
			require.Error(t, err)
			var count int64
			require.NoError(t, db.Model(&model.TopUp{}).Where("trade_no = ?", top.TradeNo).Count(&count).Error)
			require.Zero(t, count, "revoked SQL margin authorization must roll back the entire order")
		}
	}
	_, margin := mustSiteGiftWallets(t, db, site.Id)
	require.Equal(t, "5", margin.FrozenUSD.String())
}

func TestPaymentOrderBonusChangesStayScoped(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	require.NoError(t, db.AutoMigrate(model.ReferralTables()...))
	old := *setting.GetBEpusdtSetting()
	*setting.GetBEpusdtSetting() = setting.BEpusdtSetting{Enabled: true, BaseURL: "https://bonus.example", Secret: "synthetic-bonus-secret", TradeTypes: []string{"usdt.trc20"}}
	t.Cleanup(func() { *setting.GetBEpusdtSetting() = old })
	site, buyer := seedSiteInvite(t, db, decimal.Zero)
	mainBuyer := referralUser(t, db, "main-bonus-buyer")
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(20), AdtySiteWalletChange{Type: "earning_settlement", IdempotencyKey: "scoped-bonus-earning"})
	require.NoError(t, err)
	config, err := GetAdtyPaymentScenes()
	require.NoError(t, err)
	for i, tc := range []struct {
		siteEnabled, mainEnabled                 bool
		siteRate, mainRate, siteBonus, mainBonus string
		siteQuota, mainQuota                     int64
	}{
		{true, true, "0.05", "0.02", "5", "2", 52_500_000, 51_000_000},
		{true, true, "0.03", "0.04", "3", "4", 51_500_000, 52_000_000},
		{false, false, "0.03", "0.04", "0", "0", 50_000_000, 50_000_000},
	} {
		policy := map[string]string{
			adty_setting.TopupSettingKeySiteMethodAuth:  fmt.Sprintf(`{"%d":["usdt"]}`, site.Id),
			adty_setting.TopupSettingKeySiteMethodBonus: fmt.Sprintf(`{"%d":{"usdt":{"enabled":%t,"rate":"%s"}}}`, site.Id, tc.siteEnabled, tc.siteRate),
			adty_setting.TopupSettingKeyMethodBonus:     fmt.Sprintf(`{"usdt":{"enabled":%t,"rate":"%s"}}`, tc.mainEnabled, tc.mainRate),
		}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return model.SaveOptionsTx(tx, policy) }))
		for _, target := range []struct {
			siteID, userID int
			bonus          string
			quota          int64
		}{
			{site.Id, buyer.Id, tc.siteBonus, tc.siteQuota}, {0, mainBuyer.Id, tc.mainBonus, tc.mainQuota},
		} {
			top, ext := paymentSceneOrder(target.userID, fmt.Sprintf("scope-%d", i))
			ext.SiteId = target.siteID
			require.NoError(t, CreateAdtyPaymentOrder(top, ext, config.Scenes[0].Key))
			require.Equal(t, target.bonus, ext.BonusUSD.String())
			require.Equal(t, target.quota, ext.CreditQuota)
		}
	}
}

func TestPaymentOrderWaitsForBonusAuthorizationCommit(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	require.NoError(t, db.AutoMigrate(model.ReferralTables()...))
	old := *setting.GetBEpusdtSetting()
	*setting.GetBEpusdtSetting() = setting.BEpusdtSetting{Enabled: true, BaseURL: "https://bonus.example", Secret: "synthetic-bonus-secret", TradeTypes: []string{"usdt.trc20"}}
	t.Cleanup(func() { *setting.GetBEpusdtSetting() = old })
	site, buyer := seedSiteInvite(t, db, decimal.Zero)
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, decimal.NewFromInt(10), AdtySiteWalletChange{Type: "earning_settlement", IdempotencyKey: "concurrent-bonus-earning"})
	require.NoError(t, err)
	policy := map[string]string{
		adty_setting.TopupSettingKeySiteMethodAuth:  fmt.Sprintf(`{"%d":["usdt"]}`, site.Id),
		adty_setting.TopupSettingKeySiteMethodBonus: fmt.Sprintf(`{"%d":{"usdt":{"enabled":true,"rate":"0.05"}}}`, site.Id),
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return model.SaveOptionsTx(tx, policy) }))
	config, err := GetAdtyPaymentScenes()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	writer := db.WithContext(ctx).Begin()
	require.NoError(t, writer.Error)
	defer writer.Rollback()
	_, err = model.LockOptionValueTx(writer, adty_setting.TopupSettingKeySiteMethodAuth)
	require.NoError(t, err)
	require.NoError(t, writer.Model(&model.Option{}).Where("key = ?", adty_setting.TopupSettingKeySiteMethodAuth).Update("value", `{}`).Error)
	var writerPID int
	require.NoError(t, writer.Raw("SELECT pg_backend_pid()").Scan(&writerPID).Error)
	top, ext := paymentSceneOrder(buyer.Id, "concurrent-bonus")
	ext.SiteId, ext.BonusRate, ext.BonusUSD, ext.CreditQuota = site.Id, decimal.RequireFromString("0.05"), decimal.NewFromInt(5), 52_500_000
	done := make(chan error, 1)
	go func() { done <- CreateAdtyPaymentOrder(top, ext, config.Scenes[0].Key) }()
	waited := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		var count int64
		err = db.Raw("SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database() AND ? = ANY(pg_blocking_pids(pid))", writerPID).Scan(&count).Error
		if err != nil || count > 0 {
			waited = count > 0
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	commitErr := writer.Commit().Error
	select {
	case createErr := <-done:
		require.NoError(t, createErr)
	case <-ctx.Done():
		t.Fatal("order did not resume after authorization commit")
	}
	require.NoError(t, err)
	require.NoError(t, commitErr)
	require.True(t, waited, "order must wait for the Root authorization row lock")
	require.True(t, ext.BonusUSD.IsZero())
	require.EqualValues(t, 50_000_000, ext.CreditQuota)
}

func TestSiteReferralSnapshotMigrationPreservesOrders(t *testing.T) {
	db := prepareReferralDialectDB(t, "postgres")
	site, buyer := seedSiteInvite(t, db, decimal.RequireFromString("0.10"))
	require.NoError(t, setInviteCoverageForTest(site, "100", "7"))
	// Simulate a pre-column pending order in this test's isolated PostgreSQL schema.
	require.NoError(t, db.Migrator().DropColumn(&model.AdtyTopupOrder{}, "SiteReferralSnapshot"))
	legacy := model.AdtyTopupOrder{TradeNo: "legacy-snapshot-migration", UserId: buyer.Id, SiteId: site.Id, FaceUSD: decimal.NewFromInt(17), CreditQuota: 8_500_000}
	require.NoError(t, db.Omit("SiteReferralSnapshot").Create(&legacy).Error)
	require.NoError(t, model.MigrateAdtyTables())
	var stored model.AdtyTopupOrder
	require.NoError(t, db.First(&stored, legacy.Id).Error)
	require.Equal(t, "", stored.SiteReferralSnapshot, "migration must not invent a historical policy")
	require.Equal(t, "17", stored.FaceUSD.String())
	top, ext := paymentSceneOrder(buyer.Id, "migration-new")
	ext.SiteId = site.Id
	require.NoError(t, model.CreateAdtyTopUp(top, ext))
	var created model.AdtyTopupOrder
	require.NoError(t, db.Where("trade_no = ?", top.TradeNo).First(&created).Error)
	require.NotEmpty(t, created.SiteReferralSnapshot)
	require.NoError(t, model.MigrateAdtyTables())
	var repeated model.AdtyTopupOrder
	require.NoError(t, db.First(&repeated, created.Id).Error)
	require.Equal(t, created.SiteReferralSnapshot, repeated.SiteReferralSnapshot)
	require.NoError(t, db.First(&stored, legacy.Id).Error)
	require.Empty(t, stored.SiteReferralSnapshot)
	require.Equal(t, "17", stored.FaceUSD.String())
	var version string
	require.NoError(t, db.Raw("SHOW server_version_num").Scan(&version).Error)
	t.Logf("snapshot migration PostgreSQL server_version_num=%s", version)
}
