package service

import (
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupR41TestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.TopUp{},
		&model.AdtyTopupOrder{},
		&model.AdtyOrderPaymentEvent{},
		&model.AdtyFundBatch{},
		&model.AdtyBalanceLedger{},
		&model.PaymentOrphanEvent{},
		&model.AdtySite{},
		&model.AdtySiteCustomer{},
		&model.AdtySiteDomain{},
		&model.AdtySiteAudit{},
		&model.AdtySiteWallet{},
		&model.AdtySiteStaff{},
		&model.AdtySiteInviteCommission{},
		&model.AdtyTeam{},
		&model.AdtyTeamMember{},
		&model.AdtyTeamRegistrationLink{},
		&model.AuditLog{},
		&model.AuditIntent{},
		&model.Option{},
	))
	oldDB := model.DB
	model.DB = db
	InvalidateAdtySiteCache()
	t.Cleanup(func() {
		model.DB = oldDB
		InvalidateAdtySiteCache()
	})
	return db
}

func TestR41ExpiredOrderRejectsNonManualAutoCredit(t *testing.T) {
	db := setupR41TestDB(t)
	now := common.GetTimestamp()

	user := model.User{
		Id:       501,
		Username: "buyer501",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Quota:    0,
	}
	require.NoError(t, db.Create(&user).Error)

	// 1. Order with status=expired rejects non-manual callback.
	expiredTradeNo := "ADT_EXPIRED_501"
	require.NoError(t, db.Create(&model.TopUp{
		UserId:          user.Id,
		Amount:          500000,
		Money:           1,
		TradeNo:         expiredTradeNo,
		CreateTime:      now - 3600,
		Status:          common.TopUpStatusExpired,
		PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		PaymentMethod:   "usdt.trc20",
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo:        expiredTradeNo,
		UserId:         user.Id,
		Provider:       model.PaymentProviderAdtyBEpusdt,
		Method:         "usdt.trc20",
		FaceUSD:        decimal.NewFromInt(1),
		CreditQuota:    500000,
		PayAmount:      decimal.NewFromInt(1),
		PayCurrency:    "USDT",
		PayAddress:     "TExpiredAddr501",
		GatewayTradeId: "gw_exp_501",
		ExpireAt:       now - 600,
	}).Error)

	callbackFacts := model.AdtyPaymentFacts{
		Provider:       model.PaymentProviderAdtyBEpusdt,
		PaidAmount:     decimal.NewFromInt(1),
		PaidCurrency:   "USDT",
		PayAddress:     "TExpiredAddr501",
		GatewayTradeId: "gw_exp_501",
		TxHash:         "tx_late_501",
		Source:         model.AdtyPaymentSourceCallback,
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		_, _, _, _, txErr := model.CompleteAdtyTopUpTx(tx, expiredTradeNo, callbackFacts)
		return txErr
	})
	require.ErrorIs(t, err, model.ErrTopUpStatusInvalid)

	// 2. Order still marked pending but already past expire_at also rejects non-manual callback.
	overdueTradeNo := "ADT_OVERDUE_501"
	require.NoError(t, db.Create(&model.TopUp{
		UserId:          user.Id,
		Amount:          500000,
		Money:           1,
		TradeNo:         overdueTradeNo,
		CreateTime:      now - 3600,
		Status:          common.TopUpStatusPending,
		PaymentProvider: model.PaymentProviderAdtyBEpusdt,
		PaymentMethod:   "usdt.trc20",
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		TradeNo:        overdueTradeNo,
		UserId:         user.Id,
		Provider:       model.PaymentProviderAdtyBEpusdt,
		Method:         "usdt.trc20",
		FaceUSD:        decimal.NewFromInt(1),
		CreditQuota:    500000,
		PayAmount:      decimal.NewFromInt(1),
		PayCurrency:    "USDT",
		PayAddress:     "TOverdueAddr501",
		GatewayTradeId: "gw_over_501",
		ExpireAt:       now - 10,
	}).Error)
	overdueFacts := callbackFacts
	overdueFacts.PayAddress = "TOverdueAddr501"
	overdueFacts.GatewayTradeId = "gw_over_501"
	err = db.Transaction(func(tx *gorm.DB) error {
		_, _, _, _, txErr := model.CompleteAdtyTopUpTx(tx, overdueTradeNo, overdueFacts)
		return txErr
	})
	require.ErrorIs(t, err, model.ErrTopUpStatusInvalid)

	// 3. Root manual reconciliation still works after admin review.
	manualFacts := callbackFacts
	manualFacts.Source = model.AdtyPaymentSourceManual
	manualFacts.OperatorId = 1
	manualFacts.ActorRole = common.RoleRootUser
	err = db.Transaction(func(tx *gorm.DB) error {
		alreadyDone, credited, topUp, _, txErr := model.CompleteAdtyTopUpTx(tx, expiredTradeNo, manualFacts)
		if txErr != nil {
			return txErr
		}
		assert.False(t, alreadyDone)
		assert.Equal(t, 500000, credited)
		assert.Equal(t, common.TopUpStatusSuccess, topUp.Status)
		return nil
	})
	require.NoError(t, err)
}

func TestR41HardDeleteZeroDataSitePreservesOwnerAsRegularUser(t *testing.T) {
	db := setupR41TestDB(t)

	root := model.User{Id: 1, Username: "root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	owner := model.User{Id: 601, Username: "owner601", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, SiteId: 0}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&owner).Error)

	site := model.AdtySite{
		OwnerUserId: owner.Id,
		Subdomain:   "empty601",
		RootDomain:  "adtyai-fz.top",
		Status:      model.AdtySiteStatusActive,
	}
	require.NoError(t, db.Create(&site).Error)
	InvalidateAdtySiteCache()

	mode, _, err := DeleteAdtySiteWithAudit(site.Id, root.Id, "clean up unused subsite")
	require.NoError(t, err)
	assert.Equal(t, "hard_deleted", mode)

	// Site is hard-deleted.
	var siteCount int64
	require.NoError(t, db.Model(&model.AdtySite{}).Where("id = ?", site.Id).Count(&siteCount).Error)
	assert.Equal(t, int64(0), siteCount)

	// Empty standalone owner user is preserved and remains enabled as a regular user.
	var reloaded model.User
	require.NoError(t, db.First(&reloaded, owner.Id).Error)
	assert.Equal(t, common.UserStatusEnabled, reloaded.Status)
	assert.Equal(t, common.RoleCommonUser, reloaded.Role)
	assert.Equal(t, 0, reloaded.SiteId)
	assert.False(t, AdtyIsSiteOwner(owner.Id))
}

func TestR41OwnerSiteTeamCreationAndRegistrationLink(t *testing.T) {
	db := setupR41TestDB(t)

	owner := model.User{Id: 701, Username: "owner701", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, SiteId: 0}
	otherOwner := model.User{Id: 702, Username: "owner702", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, SiteId: 0}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&otherOwner).Error)

	site := model.AdtySite{
		OwnerUserId: owner.Id,
		Subdomain:   "team701",
		RootDomain:  "adtyai-fz.top",
		Status:      model.AdtySiteStatusActive,
	}
	otherSite := model.AdtySite{
		OwnerUserId: otherOwner.Id,
		Subdomain:   "team702",
		RootDomain:  "adtyai-fz.top",
		Status:      model.AdtySiteStatusActive,
	}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Create(&otherSite).Error)
	InvalidateAdtySiteCache()

	customer := model.User{Id: 711, Username: "cust711", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, SiteId: site.Id}
	foreignCust := model.User{Id: 712, Username: "cust712", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, SiteId: otherSite.Id}
	require.NoError(t, db.Create(&customer).Error)
	require.NoError(t, db.Create(&foreignCust).Error)

	// 1. Discount below 1.0 is rejected on subsites.
	_, _, _, err := CreateAdtyOwnerSiteTeamWithAudit(owner.Id, site.Id, customer.Id, "TooCheap", "0.8", "idem-cheap")
	require.Error(t, err)

	// 2. Foreign customer cannot be appointed leader on owner's site.
	_, _, _, err = CreateAdtyOwnerSiteTeamWithAudit(owner.Id, site.Id, foreignCust.Id, "CrossSite", "1.2", "idem-cross")
	require.Error(t, err)

	// 3. Valid owner team creation succeeds with idempotency replay and conflict protection.
	created, _, replayed, err := CreateAdtyOwnerSiteTeamWithAudit(owner.Id, site.Id, customer.Id, "Alpha Squad", "1.25", "idem-alpha")
	require.NoError(t, err)
	assert.False(t, replayed)
	assert.Equal(t, site.Id, created.Team.SiteId)
	assert.Equal(t, customer.Id, created.Team.LeaderUserId)
	assert.Equal(t, "1.25", created.Team.Discount.String())

	replayedCreated, _, replayed2, err := CreateAdtyOwnerSiteTeamWithAudit(owner.Id, site.Id, customer.Id, "Alpha Squad", "1.25", "idem-alpha")
	require.NoError(t, err)
	assert.True(t, replayed2)
	assert.Equal(t, created.Team.Id, replayedCreated.Team.Id)

	_, _, _, conflictErr := CreateAdtyOwnerSiteTeamWithAudit(owner.Id, site.Id, customer.Id, "Different Name", "1.25", "idem-alpha")
	require.True(t, errors.Is(conflictErr, model.ErrAdtyIdempotencyConflict))

	// 4. Owner can issue a subsite team registration link and a new leader can complete it on that subsite.
	issued, _, err := CreateAdtyOwnerSiteTeamRegistrationWithAudit(owner.Id, site.Id, 24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, site.Id, issued.SiteId)
	assert.NotEmpty(t, issued.Code)

	// Wrong host (main site) is rejected.
	mainSiteID := 0
	_, err = CompleteAdtyTeamRegistration(issued.Code, "newleader701", "StrongPass!123", "Reg Team", &mainSiteID)
	require.ErrorIs(t, err, ErrAdtyTeamForbidden)

	// Matching subsite host succeeds and creates leader + team bound to the subsite.
	regCreated, err := CompleteAdtyTeamRegistration(issued.Code, "newleader701", "StrongPass!123", "Reg Team", &site.Id)
	require.NoError(t, err)
	assert.Equal(t, site.Id, regCreated.Team.SiteId)
	assert.Equal(t, "Reg Team", regCreated.Team.Name)
	assert.Equal(t, "1", regCreated.Team.Discount.String())
}
