package service

import (
	"testing"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 员工给客户加款必须先有出资（`ISS-R4-0075`）。
//
// 修前 increase 分支只做「加客户额度 + 建一条无资金来源的批次」，全函数零出资
// 调用——于是一个员工可以让平台凭空给客户加钱，批次表还把这笔钱记成「有背书」。
func TestStaffBalanceIncreaseIsRejectedWithoutSiteFundsPG(t *testing.T) {
	db, owner, _, request := newStaffBalanceFundingFixture(t, "no-funds")
	pgtest.RecordServerVersion(t, db)

	var before model.User
	require.NoError(t, db.First(&before, request.CustomerUserId).Error)

	err := ReviewAdtyStaffBalanceRequest(owner.Id, "owner", request.Id, true)
	require.Error(t, err, "站点三个钱包都是 0 时，加款必须被拒")
	assert.ErrorIs(t, err, ErrAdtySiteWalletInsufficient)

	var after model.User
	require.NoError(t, db.First(&after, request.CustomerUserId).Error)
	assert.EqualValues(t, before.Quota, after.Quota, "被拒时客户余额一分不动")

	var reviewed model.AdtyStaffBalanceRequest
	require.NoError(t, db.First(&reviewed, request.Id).Error)
	assert.Equal(t, model.AdtyStaffBalancePending, reviewed.Status, "整笔事务回滚，请求仍待审")
}

// 收益钱包够 → 加款从收益钱包扣除并向运营钱包等额补足本金背书；原有客户本金背书不变，客户消费后运营钱包回落至原值。
func TestStaffBalanceIncreaseIsFundedBySiteWalletPG(t *testing.T) {
	db, owner, site, request := newStaffBalanceFundingFixture(t, "funded")
	pgtest.RecordServerVersion(t, db)

	const existingOperating = 200
	const amountUSD = 100
	require.NoError(t, fundStaffBalanceWallet(t, site.Id, model.AdtySiteWalletOperating, existingOperating, "funded-operating-existing"))
	require.NoError(t, fundStaffBalanceWallet(t, site.Id, model.AdtySiteWalletEarnings, amountUSD, "funded-earnings-seed"))

	err := ReviewAdtyStaffBalanceRequest(owner.Id, "owner", request.Id, true)
	require.NoError(t, err)

	var after model.User
	require.NoError(t, db.First(&after, request.CustomerUserId).Error)
	assert.EqualValues(t, int64(amountUSD)*500_000, after.Quota, "客户拿到 $100 对应额度")

	var earnings model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earnings).Error)
	assert.True(t, earnings.Balance.IsZero(), "收益钱包被扣到 0：$100 由收益钱包出资")

	var operating model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletOperating).First(&operating).Error)
	assert.True(t, operating.Balance.Equal(decimal.NewFromInt(existingOperating+amountUSD)),
		"运营钱包不仅未被当成盈余扣掉，反而增加 $100 以 1:1 背书新客户本金")
	assert.True(t, ReconcileOperatingWallet(decimal.NewFromInt(existingOperating+amountUSD), operating.Balance).IsZero())

	// 客户后续消费 $100 时，运营扣款钩子只消耗刚刚补足的那 $100，原有 $200 客户本金背书分毫不差。
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.AdtyAfterPrincipalSpentTx(tx, request.CustomerUserId, "op-spend-after-staff", int64(amountUSD)*500_000)
	}))
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletOperating).First(&operating).Error)
	assert.True(t, operating.Balance.Equal(decimal.NewFromInt(existingOperating)))
	assert.True(t, ReconcileOperatingWallet(decimal.NewFromInt(existingOperating), operating.Balance).IsZero())

	var reviewed model.AdtyStaffBalanceRequest
	require.NoError(t, db.First(&reviewed, request.Id).Error)
	assert.Equal(t, model.AdtyStaffBalanceApproved, reviewed.Status)
}

// 仅运营钱包有现有客户本金，收益和保证金都不足 → 整笔拒绝，绝不挪用现有客户运营本金背书。
func TestStaffBalanceIncreaseRollsBackWhenSiteFundsAreShortPG(t *testing.T) {
	db, owner, site, request := newStaffBalanceFundingFixture(t, "short")
	pgtest.RecordServerVersion(t, db)

	require.NoError(t, fundStaffBalanceWallet(t, site.Id, model.AdtySiteWalletOperating, 500, "short-operating-only"))
	require.NoError(t, fundStaffBalanceWallet(t, site.Id, model.AdtySiteWalletEarnings, 50, "short-earnings-seed"))

	var before model.User
	require.NoError(t, db.First(&before, request.CustomerUserId).Error)

	err := ReviewAdtyStaffBalanceRequest(owner.Id, "owner", request.Id, true)
	require.ErrorIs(t, err, ErrAdtySiteWalletInsufficient, "只有 $50 收益且无可用保证金，不能挪用运营钱包 $500 来加 $100")

	var after model.User
	require.NoError(t, db.First(&after, request.CustomerUserId).Error)
	assert.EqualValues(t, before.Quota, after.Quota, "客户余额不变")

	var earnings model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletEarnings).First(&earnings).Error)
	assert.True(t, earnings.Balance.Equal(decimal.NewFromInt(50)), "收益钱包也没有被部分扣款")

	var operating model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletOperating).First(&operating).Error)
	assert.True(t, operating.Balance.Equal(decimal.NewFromInt(500)), "现有客户运营本金背书分毫不差")
}

// 收益不够但保证金超出底线与周期限额足够时，由保证金出资并补足运营钱包，仍尊重 10% 底线。
func TestStaffBalanceIncreaseFallsBackToMarginRespectingFloorPG(t *testing.T) {
	db, owner, site, request := newStaffBalanceFundingFixture(t, "margin-fallback")
	pgtest.RecordServerVersion(t, db)

	// $200 required balance / cycle limit reserves $20 (10%) floor; only $180 usable.
	require.NoError(t, OpenAdtyMarginCycle(site.Id, decimal.NewFromInt(200), "margin-cycle-seed"))
	require.NoError(t, ReviewAdtyStaffBalanceRequest(owner.Id, "owner", request.Id, true))

	var margin model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&margin).Error)
	assert.True(t, margin.Balance.Equal(decimal.NewFromInt(100)))

	var operating model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletOperating).First(&operating).Error)
	assert.True(t, operating.Balance.Equal(decimal.NewFromInt(100)))

	// A second $100 request only has $80 drawable above $20 floor -> must be rejected.
	second := model.AdtyStaffBalanceRequest{
		SiteId: site.Id, StaffUserId: request.StaffUserId, CustomerUserId: request.CustomerUserId,
		Direction: model.AdtyStaffBalanceIncrease, AmountUSD: decimal.NewFromInt(100),
		Status: model.AdtyStaffBalancePending,
	}
	require.NoError(t, db.Create(&second).Error)
	err := ReviewAdtyStaffBalanceRequest(owner.Id, "owner", second.Id, true)
	require.ErrorIs(t, err, ErrAdtySiteWalletInsufficient)
}

// —— 夹具 ——

// newStaffBalanceFundingFixture 造一个站点 + 员工 + 客户 + 一笔待审的 $100 加款请求，
// **不给站点钱包放钱**（放钱的用例自己调 fundStaffBalanceWallet）。
func newStaffBalanceFundingFixture(t *testing.T, tag string) (*gorm.DB, *model.User, model.AdtySite, model.AdtyStaffBalanceRequest) {
	t.Helper()
	db := announcementTestDB(t)
	owner := ticketTestUser(t, db, "fund-"+tag+"-owner", 1)
	staff := ticketTestUser(t, db, "fund-"+tag+"-staff", 1)
	customer := ticketTestUser(t, db, "fund-"+tag+"-customer", 1)
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "fund-" + tag,
		Status: model.AdtySiteStatusActive, AgreementVer: AdtyCurrentSiteAgreementVersion()}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", customer.Id).Update("site_id", site.Id).Error)
	require.NoError(t, db.Create(&model.AdtySiteStaff{
		SiteId: site.Id, OwnerUserId: owner.Id, UserId: staff.Id, Status: "enabled", Role: "support",
	}).Error)
	require.NoError(t, EnsureAdtySiteWallets(site.Id))

	request := model.AdtyStaffBalanceRequest{
		SiteId: site.Id, StaffUserId: staff.Id, CustomerUserId: customer.Id,
		Direction: model.AdtyStaffBalanceIncrease, AmountUSD: decimal.NewFromInt(100),
		Status: model.AdtyStaffBalancePending,
	}
	require.NoError(t, db.Create(&request).Error)
	return db, owner, site, request
}

func fundStaffBalanceWallet(t *testing.T, siteID int, kind string, amount int64, key string) error {
	t.Helper()
	_, err := ApplyAdtySiteWalletChange(siteID, kind, decimal.NewFromInt(amount), AdtySiteWalletChange{
		Type: "seed", RefType: "test", RefID: key, IdempotencyKey: key,
	})
	return err
}
