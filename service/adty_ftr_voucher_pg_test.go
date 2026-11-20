package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ftrVoucherDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := prepareReferralDialectDB(t, "postgres")
	pgtest.RecordServerVersion(t, db)
	ResetAdtyVoucherGuesses()
	t.Cleanup(ResetAdtyVoucherGuesses)
	return db
}

func ftrVoucherRoot(t *testing.T, db *gorm.DB, name string) *model.User {
	t.Helper()
	root := referralUser(t, db, name)
	require.NoError(t, db.Model(root).Update("role", common.RoleRootUser).Error)
	root.Role = common.RoleRootUser
	require.NoError(t, SetAdtyVoucherIssuanceLimit(root.Id, decimal.NewFromInt(40)))
	return root
}

func ftrVoucherSite(t *testing.T, db *gorm.DB, owner *model.User, subdomain string, earnings decimal.Decimal) model.AdtySite {
	t.Helper()
	site := model.AdtySite{
		OwnerUserId: owner.Id, Subdomain: subdomain, Status: model.AdtySiteStatusActive,
		AgreementVer: AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	if earnings.IsPositive() {
		_, err := ApplyAdtySiteWalletChange(site.Id, model.AdtySiteWalletEarnings, earnings, AdtySiteWalletChange{
			Type: "ftr_seed", Reason: "FTR voucher seed", IdempotencyKey: "ftr-voucher-seed-" + subdomain,
		})
		require.NoError(t, err)
	}
	return site
}

func ftrVoucherQuota(t *testing.T, db *gorm.DB, userID, want int) {
	t.Helper()
	var stored model.User
	require.NoError(t, db.Select("id", "quota", "role").First(&stored, userID).Error)
	assert.Equal(t, want, stored.Quota)
}

func ftrVoucherLiabilityFrozen(t *testing.T, db *gorm.DB) decimal.Decimal {
	t.Helper()
	var row model.AdtyVoucherLiability
	require.NoError(t, db.First(&row).Error)
	return row.FrozenUSD
}

// ADTY-15-01: Root issues a main-site user recharge code and freezes platform liability.
func TestFTRADTY1501RootIssuesMainUserVoucherPG(t *testing.T) {
	db := ftrVoucherDB(t)
	root := ftrVoucherRoot(t, db, "ftr1501-root")
	require.Equal(t, common.RoleRootUser, root.Role)

	issued, err := IssueAdtyVoucher(AdtyVoucherIssueInput{
		IssuerId: root.Id, Purpose: model.AdtyVoucherPurposeMainUser,
		AmountUSD: decimal.NewFromInt(5), Reason: "FTR 1501 main user",
	})
	require.NoError(t, err)
	require.Len(t, issued.Code, 26)
	assert.Equal(t, issued.Code[len(issued.Code)-4:], issued.CodeTail)
	assert.Equal(t, model.AdtyVoucherPurposeMainUser, issued.Purpose)

	var stored model.AdtyVoucher
	require.NoError(t, db.First(&stored, issued.Id).Error)
	assert.Equal(t, model.AdtyVoucherStatusPending, stored.Status)
	assert.Equal(t, model.AdtyVoucherSourcePlatform, stored.SourceKind)
	assert.Equal(t, issued.CodeTail, stored.CodeTail)
	assert.NotEqual(t, issued.Code, stored.CodeHash)
	assert.True(t, ftrVoucherLiabilityFrozen(t, db).Equal(decimal.NewFromInt(5)))
	ftrVoucherQuota(t, db, root.Id, 0)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-15-02: Root issues a margin code that only the designated site owner can redeem.
func TestFTRADTY1502RootIssuesSiteMarginVoucherPG(t *testing.T) {
	db := ftrVoucherDB(t)
	root := ftrVoucherRoot(t, db, "ftr1502-root")
	owner := referralUser(t, db, "ftr1502-owner")
	customer := referralUser(t, db, "ftr1502-customer")
	site := ftrVoucherSite(t, db, owner, "ftr1502", decimal.Zero)
	require.NoError(t, db.Model(customer).Update("site_id", site.Id).Error)

	issued, err := IssueAdtyVoucher(AdtyVoucherIssueInput{
		IssuerId: root.Id, Purpose: model.AdtyVoucherPurposeSiteMargin, SiteId: site.Id,
		DesignatedOwnerId: owner.Id, AmountUSD: decimal.NewFromInt(4), Reason: "FTR 1502 margin",
	})
	require.NoError(t, err)
	assert.Equal(t, model.AdtyVoucherPurposeSiteMargin, issued.Purpose)
	assert.Equal(t, owner.Id, issued.DesignatedOwnerId)
	_, err = RedeemAdtyVoucher(customer.Id, issued.Code)
	require.ErrorIs(t, err, ErrAdtyVoucherWrongAccount)
	redeemed, err := RedeemAdtyVoucher(owner.Id, issued.Code)
	require.NoError(t, err)
	assert.Equal(t, model.AdtyVoucherPurposeSiteMargin, redeemed.Purpose)
	var margin model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&margin).Error)
	assert.True(t, margin.Balance.Equal(decimal.NewFromInt(4)))
	ftrVoucherQuota(t, db, owner.Id, 0)
	ftrVoucherQuota(t, db, customer.Id, 0)
	assert.Equal(t, common.RoleRootUser, root.Role)
}

// ADTY-15-03: a main-site code is refused on a site account and accepted on a main-site account.
func TestFTRADTY1503MainVoucherAccountLimitPG(t *testing.T) {
	db := ftrVoucherDB(t)
	root := ftrVoucherRoot(t, db, "ftr1503-root")
	owner := referralUser(t, db, "ftr1503-owner")
	customer := referralUser(t, db, "ftr1503-customer")
	mainUser := referralUser(t, db, "ftr1503-main")
	site := ftrVoucherSite(t, db, owner, "ftr1503", decimal.Zero)
	require.NoError(t, db.Model(customer).Update("site_id", site.Id).Error)
	issued, err := IssueAdtyVoucher(AdtyVoucherIssueInput{
		IssuerId: root.Id, Purpose: model.AdtyVoucherPurposeMainUser,
		AmountUSD: decimal.NewFromInt(5), Reason: "FTR 1503 limit",
	})
	require.NoError(t, err)

	_, err = RedeemAdtyVoucher(customer.Id, issued.Code)
	require.ErrorIs(t, err, ErrAdtyVoucherWrongAccount)
	redeemed, err := RedeemAdtyVoucher(mainUser.Id, issued.Code)
	require.NoError(t, err)
	assert.EqualValues(t, 5*500000, redeemed.Quota)
	ftrVoucherQuota(t, db, mainUser.Id, 5*500000)
	ftrVoucherQuota(t, db, customer.Id, 0)
	assert.Equal(t, common.RoleRootUser, root.Role)
}

// ADTY-15-04: a site owner issues a customer principal code funded by the site.
func TestFTRADTY1504OwnerIssuesCustomerVoucherPG(t *testing.T) {
	db := ftrVoucherDB(t)
	owner := referralUser(t, db, "ftr1504-owner")
	require.Equal(t, common.RoleCommonUser, owner.Role)
	site := ftrVoucherSite(t, db, owner, "ftr1504", decimal.NewFromInt(30))

	issued, err := IssueAdtyVoucher(AdtyVoucherIssueInput{
		IssuerId: owner.Id, Purpose: model.AdtyVoucherPurposeSiteCustomer, SiteId: site.Id,
		AmountUSD: decimal.RequireFromString("10.50"), Reason: "FTR 1504 customer",
	})
	require.NoError(t, err)
	assert.Equal(t, model.AdtyVoucherPurposeSiteCustomer, issued.Purpose)
	assert.Equal(t, site.Id, issued.TargetSiteId)
	var stored model.AdtyVoucher
	require.NoError(t, db.First(&stored, issued.Id).Error)
	assert.Equal(t, model.AdtyVoucherStatusPending, stored.Status)
	assert.Equal(t, model.AdtyVoucherSourceEarnings, stored.SourceKind)
	assert.Equal(t, site.Id, stored.SourceSiteId)
	ftrVoucherQuota(t, db, owner.Id, 0)
}

// ADTY-15-05: a site owner issues a code that only that owner can redeem.
func TestFTRADTY1505OwnerIssuesSelfVoucherPG(t *testing.T) {
	db := ftrVoucherDB(t)
	owner := referralUser(t, db, "ftr1505-owner")
	customer := referralUser(t, db, "ftr1505-customer")
	site := ftrVoucherSite(t, db, owner, "ftr1505", decimal.NewFromInt(30))
	require.NoError(t, db.Model(customer).Update("site_id", site.Id).Error)

	issued, err := IssueAdtyVoucher(AdtyVoucherIssueInput{
		IssuerId: owner.Id, Purpose: model.AdtyVoucherPurposeSiteOwner, SiteId: site.Id,
		AmountUSD: decimal.NewFromInt(2), Reason: "FTR 1505 self",
	})
	require.NoError(t, err)
	assert.Equal(t, model.AdtyVoucherPurposeSiteOwner, issued.Purpose)
	_, err = RedeemAdtyVoucher(customer.Id, issued.Code)
	require.ErrorIs(t, err, ErrAdtyVoucherWrongAccount)
	redeemed, err := RedeemAdtyVoucher(owner.Id, issued.Code)
	require.NoError(t, err)
	assert.EqualValues(t, 2*500000, redeemed.Quota)
	ftrVoucherQuota(t, db, owner.Id, 2*500000)
	ftrVoucherQuota(t, db, customer.Id, 0)
	assert.Equal(t, common.RoleCommonUser, owner.Role)
}

// ADTY-15-06: issuing a site code freezes earnings without spending them yet.
func TestFTRADTY1506SiteVoucherFreezesEarningsPG(t *testing.T) {
	db := ftrVoucherDB(t)
	owner := referralUser(t, db, "ftr1506-owner")
	site := ftrVoucherSite(t, db, owner, "ftr1506", decimal.NewFromInt(30))

	_, err := IssueAdtyVoucher(AdtyVoucherIssueInput{
		IssuerId: owner.Id, Purpose: model.AdtyVoucherPurposeSiteCustomer, SiteId: site.Id,
		AmountUSD: decimal.RequireFromString("10.50"), Reason: "FTR 1506 freeze",
	})
	require.NoError(t, err)
	var earnings model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earnings).Error)
	assert.True(t, earnings.Balance.Equal(decimal.NewFromInt(30)))
	assert.True(t, earnings.FrozenUSD.Equal(decimal.RequireFromString("10.50")))
	ftrVoucherQuota(t, db, owner.Id, 0)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
	assert.Equal(t, common.RoleCommonUser, owner.Role)
}

// ADTY-15-07: the full code is returned once; later lists keep only the tail.
func TestFTRADTY1507VoucherListShowsTailOnlyPG(t *testing.T) {
	db := ftrVoucherDB(t)
	root := ftrVoucherRoot(t, db, "ftr1507-root")
	issued, err := IssueAdtyVoucher(AdtyVoucherIssueInput{
		IssuerId: root.Id, Purpose: model.AdtyVoucherPurposeMainUser,
		AmountUSD: decimal.NewFromInt(1), Reason: "FTR 1507 tail",
	})
	require.NoError(t, err)
	require.Len(t, issued.Code, 26)

	rows, total, err := ListAdtyVouchers(AdtyVoucherListFilter{IssuerId: root.Id}, 0, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, issued.Code[len(issued.Code)-4:], rows[0].CodeTail)
	assert.NotContains(t, rows[0].CodeHash, issued.Code)
	assert.NotEqual(t, issued.Code, rows[0].CodeTail)
	ftrVoucherQuota(t, db, root.Id, 0)
	assert.Equal(t, common.RoleRootUser, root.Role)
}

// ADTY-15-08: a code credits once, a replay returns the same trade, and a second user is refused.
func TestFTRADTY1508VoucherRedeemsOncePG(t *testing.T) {
	db := ftrVoucherDB(t)
	root := ftrVoucherRoot(t, db, "ftr1508-root")
	user := referralUser(t, db, "ftr1508-user")
	other := referralUser(t, db, "ftr1508-other")
	issued, err := IssueAdtyVoucher(AdtyVoucherIssueInput{
		IssuerId: root.Id, Purpose: model.AdtyVoucherPurposeMainUser,
		AmountUSD: decimal.NewFromInt(5), Reason: "FTR 1508 once",
	})
	require.NoError(t, err)

	redeemed, err := RedeemAdtyVoucher(user.Id, issued.Code)
	require.NoError(t, err)
	require.False(t, redeemed.Replayed)
	replay, err := RedeemAdtyVoucher(user.Id, issued.Code)
	require.NoError(t, err)
	assert.True(t, replay.Replayed)
	assert.Equal(t, redeemed.TradeNo, replay.TradeNo)
	_, err = RedeemAdtyVoucher(other.Id, issued.Code)
	require.ErrorIs(t, err, ErrAdtyVoucherUsed)
	ftrVoucherQuota(t, db, user.Id, 5*500000)
	ftrVoucherQuota(t, db, other.Id, 0)
	var stored model.AdtyVoucher
	require.NoError(t, db.First(&stored, issued.Id).Error)
	assert.Equal(t, model.AdtyVoucherStatusRedeemed, stored.Status)
	assert.Equal(t, common.RoleRootUser, root.Role)
}

// ADTY-15-09: a successful redeem writes one official voucher order.
func TestFTRADTY1509VoucherRedeemWritesOrderPG(t *testing.T) {
	db := ftrVoucherDB(t)
	root := ftrVoucherRoot(t, db, "ftr1509-root")
	user := referralUser(t, db, "ftr1509-user")
	issued, err := IssueAdtyVoucher(AdtyVoucherIssueInput{
		IssuerId: root.Id, Purpose: model.AdtyVoucherPurposeMainUser,
		AmountUSD: decimal.NewFromInt(5), Reason: "FTR 1509 order",
	})
	require.NoError(t, err)
	redeemed, err := RedeemAdtyVoucher(user.Id, issued.Code)
	require.NoError(t, err)

	var orders int64
	require.NoError(t, db.Model(&model.AdtyTopupOrder{}).Where("trade_no = ? AND provider = ?", redeemed.TradeNo, model.AdtyVoucherProvider).Count(&orders).Error)
	assert.Equal(t, int64(1), orders)
	var order model.AdtyTopupOrder
	require.NoError(t, db.Where("trade_no = ?", redeemed.TradeNo).First(&order).Error)
	assert.True(t, order.FaceUSD.Equal(decimal.NewFromInt(5)))
	assert.Equal(t, user.Id, order.UserId)
	ftrVoucherQuota(t, db, user.Id, 5*500000)
	assert.Equal(t, common.RoleRootUser, root.Role)
}

// ADTY-15-10: the issuer cancels an unused code and a later redeem is refused.
func TestFTRADTY1510CancelUnusedVoucherPG(t *testing.T) {
	db := ftrVoucherDB(t)
	owner := referralUser(t, db, "ftr1510-owner")
	site := ftrVoucherSite(t, db, owner, "ftr1510", decimal.NewFromInt(30))
	issued, err := IssueAdtyVoucher(AdtyVoucherIssueInput{
		IssuerId: owner.Id, Purpose: model.AdtyVoucherPurposeSiteOwner, SiteId: site.Id,
		AmountUSD: decimal.NewFromInt(2), Reason: "FTR 1510 cancel",
	})
	require.NoError(t, err)
	require.NoError(t, CancelAdtyVoucher(owner.Id, issued.Id))

	_, err = RedeemAdtyVoucher(owner.Id, issued.Code)
	require.ErrorIs(t, err, ErrAdtyVoucherCancelled)
	var stored model.AdtyVoucher
	require.NoError(t, db.First(&stored, issued.Id).Error)
	assert.Equal(t, model.AdtyVoucherStatusCancelled, stored.Status)
	var earnings model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earnings).Error)
	assert.True(t, earnings.FrozenUSD.IsZero())
	assert.True(t, earnings.Balance.Equal(decimal.NewFromInt(30)))
	ftrVoucherQuota(t, db, owner.Id, 0)
	assert.Equal(t, common.RoleCommonUser, owner.Role)
}
