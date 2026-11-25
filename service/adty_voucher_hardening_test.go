package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupVoucherScopeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&model.Option{},
		&model.User{},
		&model.TopUp{},
		&model.AdtyTopupOrder{},
		&model.AdtyFundBatch{},
		&model.AdtyBalanceLedger{},
		&model.AdtyVoucher{},
		&model.AdtyVoucherLiability{},
		&model.AdtyVoucherCloseGrant{},
		&model.AdtySite{},
		&model.AdtySiteOwnership{},
		&model.AdtySiteTier{},
		&model.AdtySiteWallet{},
		&model.AdtySiteWalletLedger{},
		&model.AdtyGiftFulfillment{},
		&model.AdtySiteInviteCommission{},
		&model.AdtyTeamMember{},
		&model.AdtyAnomalyDisposition{},
		&model.AuditIntent{},
		&model.AuditLog{},
	))

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedis := common.RedisEnabled
	oldMemCache := common.MemoryCacheEnabled
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	model.DB = db
	model.LOG_DB = db
	ResetAdtyVoucherGuesses()
	t.Cleanup(func() {
		ResetAdtyVoucherGuesses()
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		_ = sqlDB.Close()
	})
	return db
}

func TestRedeemAdtyVoucherWithScopeChannelIsolation(t *testing.T) {
	db := setupVoucherScopeTestDB(t)

	root := model.User{Id: 1, Username: "v_root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	owner := model.User{Id: 2, Username: "v_owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	otherOwner := model.User{Id: 3, Username: "v_other_owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	customer := model.User{Id: 4, Username: "v_customer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&otherOwner).Error)
	require.NoError(t, db.Create(&customer).Error)

	site := model.AdtySite{Id: 10, OwnerUserId: owner.Id, Status: model.AdtySiteStatusActive, Subdomain: "vsite10"}
	otherSite := model.AdtySite{Id: 11, OwnerUserId: otherOwner.Id, Status: model.AdtySiteStatusActive, Subdomain: "vsite11"}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Create(&otherSite).Error)
	require.NoError(t, EnsureAdtySiteWallets(site.Id))
	require.NoError(t, EnsureAdtySiteWallets(otherSite.Id))
	require.NoError(t, SetAdtyVoucherIssuanceLimit(root.Id, decimal.NewFromInt(10000)))

	marginIssued, err := IssueAdtyVoucher(AdtyVoucherIssueInput{
		IssuerId: root.Id, Purpose: model.AdtyVoucherPurposeSiteMargin, SiteId: site.Id,
		DesignatedOwnerId: owner.Id, AmountUSD: decimal.NewFromInt(25), Reason: "margin seed",
	})
	require.NoError(t, err)

	mainIssued, err := IssueAdtyVoucher(AdtyVoucherIssueInput{
		IssuerId: root.Id, Purpose: model.AdtyVoucherPurposeMainUser,
		AmountUSD: decimal.NewFromInt(10), Reason: "customer voucher",
	})
	require.NoError(t, err)

	// 1. Designated owner trying to redeem site_margin via customer scope is rejected with ErrAdtyVoucherDenied.
	_, err = RedeemAdtyVoucherWithScope(owner.Id, 0, marginIssued.Code, []string{model.AdtyVoucherPurposeMainUser, model.AdtyVoucherPurposeSiteCustomer})
	require.ErrorIs(t, err, ErrAdtyVoucherDenied)

	// 2. Non-owner trying to redeem site_margin via customer scope receives ErrAdtyVoucherWrongAccount (no oracle leak).
	_, err = RedeemAdtyVoucherWithScope(customer.Id, 0, marginIssued.Code, []string{model.AdtyVoucherPurposeMainUser, model.AdtyVoucherPurposeSiteCustomer})
	require.ErrorIs(t, err, ErrAdtyVoucherWrongAccount)

	// 3. Margin scope with mismatched requestSiteID is rejected.
	_, err = RedeemAdtyVoucherWithScope(owner.Id, otherSite.Id, marginIssued.Code, []string{model.AdtyVoucherPurposeSiteMargin})
	require.ErrorIs(t, err, ErrAdtyVoucherWrongAccount)

	// 4. Customer voucher cannot be redeemed through the margin-only scope.
	_, err = RedeemAdtyVoucherWithScope(customer.Id, 0, mainIssued.Code, []string{model.AdtyVoucherPurposeSiteMargin})
	require.ErrorIs(t, err, ErrAdtyVoucherMarginOnly)

	// 5. Designated owner redeeming site_margin via margin scope succeeds and credits site margin wallet.
	redeemedMargin, err := RedeemAdtyVoucherWithScope(owner.Id, site.Id, marginIssued.Code, []string{model.AdtyVoucherPurposeSiteMargin})
	require.NoError(t, err)
	assert.Equal(t, model.AdtyVoucherPurposeSiteMargin, redeemedMargin.Purpose)
	assert.False(t, redeemedMargin.Replayed)

	var marginWallet model.AdtySiteWallet
	require.NoError(t, db.Where("site_id = ? AND kind = ?", site.Id, model.AdtySiteWalletMargin).First(&marginWallet).Error)
	assert.True(t, marginWallet.Balance.Equal(decimal.NewFromInt(25)))

	// 6. Replaying already-redeemed site_margin via customer scope is still rejected with ErrAdtyVoucherDenied.
	_, err = RedeemAdtyVoucherWithScope(owner.Id, 0, marginIssued.Code, []string{model.AdtyVoucherPurposeMainUser, model.AdtyVoucherPurposeSiteCustomer})
	require.ErrorIs(t, err, ErrAdtyVoucherDenied)

	// 7. Replaying already-redeemed site_margin via margin scope succeeds as replay.
	replayedMargin, err := RedeemAdtyVoucherWithScope(owner.Id, site.Id, marginIssued.Code, []string{model.AdtyVoucherPurposeSiteMargin})
	require.NoError(t, err)
	assert.True(t, replayedMargin.Replayed)

	// 8. Customer redeeming main_user voucher via customer scope succeeds.
	redeemedMain, err := RedeemAdtyVoucherWithScope(customer.Id, 0, mainIssued.Code, []string{model.AdtyVoucherPurposeMainUser, model.AdtyVoucherPurposeSiteCustomer})
	require.NoError(t, err)
	assert.Equal(t, model.AdtyVoucherPurposeMainUser, redeemedMain.Purpose)
}
