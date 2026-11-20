package controller

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ftrPinProcessOptions(t *testing.T) {
	t.Helper()
	previous := common.OptionMap
	copied := map[string]string{}
	for key, value := range common.OptionMap {
		copied[key] = value
	}
	common.OptionMap = copied
	savedTopup := *adty_setting.GetTopupSetting()
	savedReferral := *adty_setting.GetReferralSetting()
	t.Cleanup(func() {
		common.OptionMap = previous
		*adty_setting.GetTopupSetting() = savedTopup
		*adty_setting.GetReferralSetting() = savedReferral
	})
}

func ftrSecurityIdentity(t *testing.T, user *model.User) service.AuthIdentity {
	t.Helper()
	require.NoError(t, model.PublishUserAuthCache(user.Id))
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "ftr-security")
	require.NoError(t, err)
	identity, err := service.ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)
	return identity
}

func ftrCallVerify(t *testing.T, identity service.AuthIdentity, role int, body map[string]any) (int, string) {
	t.Helper()
	ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/verify", body, identity.UserID)
	ctx.Set("role", role)
	ctx.Set("username", "ftr-security")
	ctx.Set("session_id", identity.SessionID)
	ctx.Set("auth_version", identity.UserAuthVersion)
	ctx.Set("session_version", identity.SessionVersion)
	UniversalVerify(ctx)
	return rec.Code, rec.Body.String()
}

// ADTY-09-01: Root turns on a main-site payment-method gift through PUT /api/adty/admin/bonus-methods.
func TestFTRADTY0901RootEnablesMethodBonusPG(t *testing.T) {
	db := ftrAccountDB(t)
	ftrPinProcessOptions(t)
	root := model.User{
		Username: "ftr0901-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&root).Error)
	require.Equal(t, common.RoleRootUser, root.Role)

	ctx, rec := newAuthenticatedContext(t, http.MethodPut, "/api/adty/admin/bonus-methods", map[string]any{
		"method": "alipay", "enabled": true, "rate": "0.02",
	}, root.Id)
	ctx.Set("role", root.Role)
	ctx.Set("username", root.Username)
	PutAdtyMainMethodBonus(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, `"enabled":true`)

	var stored model.Option
	require.NoError(t, db.Where("key = ?", adty_setting.TopupSettingKeyMethodBonus).First(&stored).Error)
	assert.Contains(t, stored.Value, `"alipay"`)
	assert.Contains(t, stored.Value, `"enabled":true`)
	ftrAssertQuotaAndNoLedger(t, db, root.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-09-02: Root stores a payment-method gift rate through the same admin route.
func TestFTRADTY0902RootSetsMethodBonusRatePG(t *testing.T) {
	db := ftrAccountDB(t)
	ftrPinProcessOptions(t)
	root := model.User{
		Username: "ftr0902-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&root).Error)
	require.Equal(t, common.RoleRootUser, root.Role)

	ctx, rec := newAuthenticatedContext(t, http.MethodPut, "/api/adty/admin/bonus-methods", map[string]any{
		"method": "wxpay", "enabled": true, "rate": "0.05",
	}, root.Id)
	ctx.Set("role", root.Role)
	ctx.Set("username", root.Username)
	PutAdtyMainMethodBonus(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, `"rate":"0.05"`)

	var stored model.Option
	require.NoError(t, db.Where("key = ?", adty_setting.TopupSettingKeyMethodBonus).First(&stored).Error)
	assert.Contains(t, stored.Value, `"wxpay"`)
	assert.Contains(t, stored.Value, `"rate":"0.05"`)
	ftrAssertQuotaAndNoLedger(t, db, root.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-09-03: Root authorizes one payment method for a site.
func TestFTRADTY0903RootAuthorizesSiteBonusMethodPG(t *testing.T) {
	db := ftrAccountDB(t)
	ftrPinProcessOptions(t)
	root := model.User{
		Username: "ftr0903-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&root).Error)
	require.Equal(t, common.RoleRootUser, root.Role)
	site := model.AdtySite{
		OwnerUserId: root.Id, Subdomain: "ftr0903", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)

	ctx, rec := newAuthenticatedContext(t, http.MethodPut, "/api/adty/admin/sites/"+strconv.Itoa(site.Id)+"/bonus-methods", map[string]any{
		"method": "alipay", "allowed": true,
	}, root.Id)
	ctx.Set("role", root.Role)
	ctx.Set("username", root.Username)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(site.Id)}}
	PutAdtySiteBonusMethodAuth(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, `"allowed":true`)

	var stored model.Option
	require.NoError(t, db.Where("key = ?", adty_setting.TopupSettingKeySiteMethodAuth).First(&stored).Error)
	assert.Contains(t, stored.Value, "alipay")
	assert.True(t, adty_setting.SiteMethodBonusAllowed(site.Id, "alipay"))
	ftrAssertQuotaAndNoLedger(t, db, root.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-09-04: the site owner saves a gift rule only after Root has authorized the method.
func TestFTRADTY0904OwnerSetsAuthorizedBonusPG(t *testing.T) {
	db := ftrAccountDB(t)
	ftrPinProcessOptions(t)
	owner := model.User{
		Username: "ftr0904-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&owner).Error)
	require.Equal(t, common.RoleCommonUser, owner.Role)
	site := model.AdtySite{
		OwnerUserId: owner.Id, Subdomain: "ftr0904", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	auth, err := adty_setting.MergeSiteMethodAuth("", site.Id, "alipay", true)
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Option{Key: adty_setting.TopupSettingKeySiteMethodAuth, Value: auth}).Error)

	ctx, rec := newAuthenticatedContext(t, http.MethodPut, "/api/adty/site/bonus-methods", map[string]any{
		"method": "alipay", "enabled": true, "rate": "0.03",
	}, owner.Id)
	ctx.Set("role", owner.Role)
	ctx.Set("username", owner.Username)
	PutAdtySiteMethodBonus(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, strconv.Itoa(site.Id))

	var stored model.Option
	require.NoError(t, db.Where("key = ?", adty_setting.TopupSettingKeySiteMethodBonus).First(&stored).Error)
	assert.Contains(t, stored.Value, "alipay")
	assert.Contains(t, stored.Value, `"rate":"0.03"`)
	ftrAssertQuotaAndNoLedger(t, db, owner.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-16-02: an admin saves the main-site referral default rate.
func TestFTRADTY1602AdminSavesReferralSettingsPG(t *testing.T) {
	db := ftrAccountDB(t)
	ftrPinProcessOptions(t)
	admin := model.User{
		Username: "ftr1602-admin", Password: "hashed-password", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&admin).Error)
	require.Equal(t, common.RoleAdminUser, admin.Role)

	ctx, rec := newAuthenticatedContext(t, http.MethodPut, "/api/user/admin/referral/settings", map[string]any{
		"default_rate": 10, "freeze_days": 7, "enabled": false,
	}, admin.Id)
	ctx.Set("role", admin.Role)
	ctx.Set("username", admin.Username)
	UpdateReferralSettings(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)

	var stored model.Option
	require.NoError(t, db.Where("key = ?", "adty_referral_setting.default_rate").First(&stored).Error)
	assert.Equal(t, "10", stored.Value)
	var freeze model.Option
	require.NoError(t, db.Where("key = ?", "adty_referral_setting.freeze_days").First(&freeze).Error)
	assert.Equal(t, "7", freeze.Value)
	ftrAssertQuotaAndNoLedger(t, db, admin.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-17-10: Root reads one site's staff balance requests without changing money.
func TestFTRADTY1710RootListsStaffBalanceRequestsPG(t *testing.T) {
	db := ftrAccountDB(t)
	root := model.User{
		Username: "ftr1710-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	customer := model.User{
		Username: "ftr1710-user", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&customer).Error)
	require.Equal(t, common.RoleRootUser, root.Role)
	site := model.AdtySite{
		OwnerUserId: root.Id, Subdomain: "ftr1710", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	key := "ftr1710"
	require.NoError(t, db.Create(&model.AdtyStaffBalanceRequest{
		SiteId: site.Id, StaffUserId: root.Id, CustomerUserId: customer.Id,
		Direction: model.AdtyStaffBalanceIncrease, AmountUSD: decimal.RequireFromString("1.00"),
		Reason: "FTR 1710 query", BusinessRef: "ftr1710-ref", OrderRef: "ftr1710-order",
		FundingSource: model.AdtyStaffFundingSourceEarnings, Status: model.AdtyStaffBalancePending,
		IdempotencyKey: &key,
	}).Error)

	ctx, rec := newAuthenticatedContext(t, http.MethodGet, "/api/adty/admin/balance-requests?site_id="+strconv.Itoa(site.Id), nil, root.Id)
	ctx.Set("role", root.Role)
	ctx.Set("username", root.Username)
	GetAdtyAdminBalanceRequests(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "FTR 1710 query")
	assert.Contains(t, body, "ftr1710-ref")
	var rows int64
	require.NoError(t, db.Model(&model.AdtyStaffBalanceRequest{}).Where("site_id = ?", site.Id).Count(&rows).Error)
	assert.Equal(t, int64(1), rows)
	ftrAssertQuotaAndNoLedger(t, db, root.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, customer.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-21-01: a signed-in user passes password verification on POST /api/verify.
func TestFTRADTY2101PasswordVerificationPG(t *testing.T) {
	db := ftrAccountDB(t)
	previous := common.PasswordLoginEnabled
	common.PasswordLoginEnabled = true
	t.Cleanup(func() { common.PasswordLoginEnabled = previous })
	password, err := common.Password2Hash("enrollment-password")
	require.NoError(t, err)
	user := &model.User{
		Username: "ftr2101", Password: password, Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(user).Error)
	require.Equal(t, common.RoleCommonUser, user.Role)
	identity := ftrSecurityIdentity(t, user)

	code, body := ftrCallVerify(t, identity, user.Role, map[string]any{
		"method": "password", "scope": "2fa.setup", "password": "enrollment-password",
	})
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, `"method":"password"`)
	assert.Contains(t, body, "2fa.setup")
	ftrAssertQuotaAndNoLedger(t, db, user.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-21-03: Root completes the same verification route without a credential challenge.
func TestFTRADTY2103RootExemptVerificationPG(t *testing.T) {
	db := ftrAccountDB(t)
	root := &model.User{
		Username: "ftr2103-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(root).Error)
	require.Equal(t, common.RoleRootUser, root.Role)
	identity := ftrSecurityIdentity(t, root)

	code, body := ftrCallVerify(t, identity, root.Role, map[string]any{
		"method": "root_exempt", "scope": "2fa.setup",
	})
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, `"method":"root_exempt"`)
	assert.Contains(t, body, "2fa.setup")
	ftrAssertQuotaAndNoLedger(t, db, root.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
