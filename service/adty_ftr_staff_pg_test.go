package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ftrStaffSite(t *testing.T, db *gorm.DB, ownerID int, subdomain string) model.AdtySite {
	t.Helper()
	site := model.AdtySite{
		OwnerUserId:  ownerID,
		Subdomain:    subdomain,
		Status:       model.AdtySiteStatusActive,
		AgreementVer: AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	return site
}

// ADTY-10-09: evaluation writes the grade that the site's lifetime totals meet.
func TestFTRADTY1009EvaluateSiteTierPG(t *testing.T) {
	db := ftrRound1PG(t)
	owner := ftrRound1User(t, db, "ftr1009owner", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)
	site := ftrStaffSite(t, db, owner.Id, "ftr1009")
	require.NoError(t, db.AutoMigrate(&model.TopUp{}))
	require.NoError(t, SaveAdtySiteTiers([]model.AdtySiteTier{{
		Code: "ftr1009", Name: "FTR 1009", Enabled: true, SortOrder: 1,
		MinEffectiveUsers: 0, DepositUSD: decimal.Zero,
	}}))

	evaluated, err := EvaluateAdtySiteTierAt(site.Id, 1_800_000_000, "ftr1009-period")
	require.NoError(t, err)
	require.NotNil(t, evaluated)
	assert.Equal(t, "ftr1009", evaluated.TierCode)
	assert.Equal(t, AdtySiteTierModeAuto, evaluated.TierMode)

	var stored model.AdtySite
	require.NoError(t, db.First(&stored, site.Id).Error)
	assert.Equal(t, "ftr1009", stored.TierCode)
	var evals int64
	require.NoError(t, db.Model(&model.AdtySiteTierEvaluation{}).Where("site_id = ? AND period_id = ?", site.Id, "ftr1009-period").Count(&evals).Error)
	assert.Equal(t, int64(1), evals)
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrRound1AssertNoMoney(t, db, owner.Id)
}

// ADTY-14-01: the site owner creates an independent staff account.
func TestFTRADTY1401OwnerCreatesStaffPG(t *testing.T) {
	db := ftrRound1PG(t)
	owner := ftrRound1User(t, db, "ftr1401owner", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)
	site := ftrStaffSite(t, db, owner.Id, "ftr1401")

	view, err := CreateAdtyStaff(owner.Id, site.Id, AdtyStaffInput{
		Username: "ftr1401staff", DisplayName: "Support One", Password: "employee-password-42", Role: "support",
	})
	require.NoError(t, err)
	assert.Equal(t, site.Id, view.SiteId)
	assert.Equal(t, owner.Id, view.OwnerUserId)
	assert.Equal(t, "support", view.Role)
	assert.Equal(t, "enabled", view.Status)
	assert.Contains(t, view.Permissions, "customers.read")

	var user model.User
	require.NoError(t, db.First(&user, view.UserId).Error)
	assert.Equal(t, common.RoleCommonUser, user.Role)
	assert.Equal(t, site.Id, user.SiteId)
	assert.Equal(t, site.Id, user.StaffSiteId)
	assert.Equal(t, common.UserStatusEnabled, user.Status)
	var audits int64
	require.NoError(t, db.Model(&model.AdtySiteAudit{}).Where("site_id = ? AND action = ?", site.Id, AdtySiteOwnerActionStaffCreate).Count(&audits).Error)
	assert.Equal(t, int64(1), audits)
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrRound1AssertNoMoney(t, db, owner.Id)
}

// ADTY-14-02: the owner deletes a staff account and the login stays disabled.
func TestFTRADTY1402OwnerDeletesStaffPG(t *testing.T) {
	db := ftrRound1PG(t)
	owner := ftrRound1User(t, db, "ftr1402owner", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)
	site := ftrStaffSite(t, db, owner.Id, "ftr1402")
	created, err := CreateAdtyStaff(owner.Id, site.Id, AdtyStaffInput{
		Username: "ftr1402staff", DisplayName: "Support", Password: "employee-password-42", Role: "support",
	})
	require.NoError(t, err)

	deleted, err := MutateAdtyStaff(owner.Id, site.Id, created.Id, AdtySiteOwnerActionStaffDelete, AdtyStaffInput{ExpectedVersion: created.Version})
	require.NoError(t, err)
	assert.Equal(t, "deleted", deleted.Status)

	var stored model.AdtySiteStaff
	require.NoError(t, db.First(&stored, created.Id).Error)
	assert.Equal(t, "deleted", stored.Status)
	var user model.User
	require.NoError(t, db.First(&user, created.UserId).Error)
	assert.Equal(t, common.UserStatusDisabled, user.Status)
	assert.Equal(t, common.RoleCommonUser, user.Role)
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrRound1AssertNoMoney(t, db, owner.Id)
}

// ADTY-14-03: a finance role receives the read grants, not a handwritten balance change.
func TestFTRADTY1403StaffRoleGrantsPG(t *testing.T) {
	db := ftrRound1PG(t)
	owner := ftrRound1User(t, db, "ftr1403owner", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)
	site := ftrStaffSite(t, db, owner.Id, "ftr1403")

	view, err := CreateAdtyStaff(owner.Id, site.Id, AdtyStaffInput{
		Username: "ftr1403staff", DisplayName: "Finance", Password: "employee-password-42", Role: "finance",
	})
	require.NoError(t, err)
	assert.Equal(t, "finance", view.Role)
	assert.Contains(t, view.Permissions, "customers.read")
	assert.Contains(t, view.Permissions, "customers.balance")
	assert.NotContains(t, view.Permissions, "customers.disable")

	var stored model.AdtySiteStaff
	require.NoError(t, db.First(&stored, view.Id).Error)
	assert.Equal(t, "finance", stored.Role)
	assert.Contains(t, stored.Permissions, "customers.balance")
	var user model.User
	require.NoError(t, db.First(&user, view.UserId).Error)
	assert.Equal(t, common.RoleCommonUser, user.Role)
	assert.Equal(t, site.Id, user.StaffSiteId)
	ftrRound1AssertNoMoney(t, db, owner.Id)
}

// ADTY-14-04: the owner changes a staff role and the stored grants follow.
func TestFTRADTY1404OwnerChangesStaffRolePG(t *testing.T) {
	db := ftrRound1PG(t)
	owner := ftrRound1User(t, db, "ftr1404owner", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)
	site := ftrStaffSite(t, db, owner.Id, "ftr1404")
	created, err := CreateAdtyStaff(owner.Id, site.Id, AdtyStaffInput{
		Username: "ftr1404staff", DisplayName: "Support", Password: "employee-password-42", Role: "support",
	})
	require.NoError(t, err)

	updated, err := MutateAdtyStaff(owner.Id, site.Id, created.Id, AdtySiteOwnerActionStaffUpdate, AdtyStaffInput{
		DisplayName: "Finance", Role: "finance", Status: "enabled", ExpectedVersion: created.Version,
	})
	require.NoError(t, err)
	assert.Equal(t, "finance", updated.Role)
	assert.Contains(t, updated.Permissions, "customers.balance")

	var stored model.AdtySiteStaff
	require.NoError(t, db.First(&stored, created.Id).Error)
	assert.Equal(t, "finance", stored.Role)
	assert.Equal(t, created.Version+1, stored.Version)
	assert.Equal(t, common.RoleCommonUser, owner.Role)
	ftrRound1AssertNoMoney(t, db, owner.Id)
}

// ADTY-14-05: disabling staff bumps the auth version and the old session cannot pass.
func TestFTRADTY1405DisableStaffRevokesAccessPG(t *testing.T) {
	db := ftrRound1PG(t)
	owner := ftrRound1User(t, db, "ftr1405owner", common.RoleCommonUser)
	ftrRound1PinQuota(t, db, owner.Id)
	site := ftrStaffSite(t, db, owner.Id, "ftr1405")
	created, err := CreateAdtyStaff(owner.Id, site.Id, AdtyStaffInput{
		Username: "ftr1405staff", DisplayName: "Support", Password: "employee-password-42", Role: "support",
	})
	require.NoError(t, err)
	var before model.User
	require.NoError(t, db.Select("id", "auth_version").First(&before, created.UserId).Error)

	_, err = MutateAdtyStaff(owner.Id, site.Id, created.Id, AdtySiteOwnerActionStaffUpdate, AdtyStaffInput{
		DisplayName: "Support", Role: "support", Status: "disabled", ExpectedVersion: created.Version,
	})
	require.NoError(t, err)
	var user model.User
	require.NoError(t, db.First(&user, created.UserId).Error)
	assert.Equal(t, common.UserStatusDisabled, user.Status)
	assert.Greater(t, user.AuthVersion, before.AuthVersion)
	_, _, err = AdtyStaffAccess(created.UserId, before.AuthVersion, "customers.read")
	assert.ErrorIs(t, err, ErrAdtyStaffDenied)
	assert.Equal(t, common.RoleCommonUser, user.Role)
	ftrRound1AssertNoMoney(t, db, owner.Id)
}
