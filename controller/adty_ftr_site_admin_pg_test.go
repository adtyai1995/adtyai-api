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

func ftrEnableReseller(t *testing.T) {
	t.Helper()
	saved := *adty_setting.GetResellerSetting()
	t.Cleanup(func() {
		*adty_setting.GetResellerSetting() = saved
		service.InvalidateAdtySiteCache()
	})
	cfg := adty_setting.GetResellerSetting()
	*cfg = adty_setting.DefaultResellerSetting()
	cfg.Enabled = true
	cfg.RootDomains = []string{"sites.example.com"}
	cfg.CostFactor = new(0.8)
	cfg.MinCostFactor = new(0.5)
	cfg.MaxMarkup = new(3.0)
	cfg.TrialGrantMaxUSD = new(5.0)
	cfg.TrialGrantDailyUSD = new(20.0)
	service.InvalidateAdtySiteCache()
}

// ADTY-10-01: Root opens a site through POST /api/adty/admin/sites.
func TestFTRADTY1001AdminOpensSitePG(t *testing.T) {
	db := ftrAccountDB(t)
	ftrEnableReseller(t)
	root := model.User{
		Username: "ftr1001-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	owner := model.User{
		Username: "ftr1001-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&owner).Error)
	require.Equal(t, common.RoleRootUser, root.Role)

	ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/adty/admin/sites", map[string]any{
		"user_id": owner.Id, "subdomain": "ftr1001",
	}, root.Id)
	ctx.Set("role", root.Role)
	ctx.Set("username", root.Username)
	CreateAdtyAdminSite(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr1001")

	var stored model.AdtySite
	require.NoError(t, db.Where("subdomain = ?", "ftr1001").First(&stored).Error)
	assert.Equal(t, owner.Id, stored.OwnerUserId)
	assert.Equal(t, model.AdtySiteStatusActive, stored.Status)
	assert.Equal(t, model.AdtySiteOpenedByAdmin, stored.OpenedBy)
	assert.Equal(t, "sites.example.com", stored.RootDomain)
	var audits int64
	require.NoError(t, db.Model(&model.AdtySiteAudit{}).Where("site_id = ? AND action = ?", stored.Id, "open").Count(&audits).Error)
	assert.Equal(t, int64(1), audits)
	ftrAssertQuotaAndNoLedger(t, db, root.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, owner.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-10-08: Root saves a site grade through PUT /api/adty/admin/reseller/tiers.
func TestFTRADTY1008RootSavesSiteTiersPG(t *testing.T) {
	db := ftrAccountDB(t)
	root := model.User{
		Username: "ftr1008-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&root).Error)
	require.Equal(t, common.RoleRootUser, root.Role)

	ctx, rec := newAuthenticatedContext(t, http.MethodPut, "/api/adty/admin/reseller/tiers", map[string]any{
		"tiers": []map[string]any{{
			"code": "ftr1008", "name": "FTR 1008", "enabled": true, "sort_order": 1,
			"min_effective_users": 1, "min_monthly_usd": "0", "min_principal_usd": "0",
			"min_consumption_usd": "0", "deposit_usd": "0",
		}},
	}, root.Id)
	ctx.Set("role", root.Role)
	ctx.Set("username", root.Username)
	PutAdtySiteTiers(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr1008")

	var stored model.AdtySiteTier
	require.NoError(t, db.Where("code = ?", "ftr1008").First(&stored).Error)
	assert.Equal(t, "FTR 1008", stored.Name)
	assert.True(t, stored.Enabled)
	assert.Equal(t, int64(1), stored.MinEffectiveUsers)
	assert.True(t, stored.DepositUSD.IsZero())
	ftrAssertQuotaAndNoLedger(t, db, root.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-10-10: Root schedules a close that the main site inherits.
func TestFTRADTY1010RootSchedulesMainSiteClosePG(t *testing.T) {
	db := ftrAccountDB(t)
	ftrEnableReseller(t)
	root := model.User{
		Username: "ftr1010-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	owner := model.User{
		Username: "ftr1010-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&owner).Error)
	site, err := service.OpenAdtySite(owner.Id, "ftr1010", model.AdtySiteOpenedByAdmin, service.AdtySitePreset{})
	require.NoError(t, err)
	require.Equal(t, model.AdtySiteStatusActive, site.Status)

	ctx, rec := newAuthenticatedContext(t, http.MethodPut, "/api/adty/admin/sites/"+strconv.Itoa(site.Id)+"/status", map[string]any{
		"status": model.AdtySiteStatusClosed, "transfer_to": 0, "immediate": false, "reason": "FTR 1010 main site inherits",
	}, root.Id)
	ctx.Set("role", root.Role)
	ctx.Set("username", root.Username)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(site.Id)}}
	SetAdtyAdminSiteStatus(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)

	var stored model.AdtySite
	require.NoError(t, db.First(&stored, site.Id).Error)
	assert.Equal(t, model.AdtySiteStatusClosing, stored.Status)
	assert.Equal(t, 0, stored.CloseTransferTo)
	assert.Equal(t, owner.Id, stored.OwnerUserId)
	assert.Equal(t, common.RoleRootUser, root.Role)
	ftrAssertQuotaAndNoLedger(t, db, root.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, owner.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-17-05: an admin reads one order and its business trace.
func TestFTRADTY1705AdminOrderTracePG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(&model.AdtySite{}, &model.TopUp{}, &model.AdtyTopupOrder{}))
	admin := model.User{
		Username: "ftr1705-admin", Password: "hashed-password", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	buyer := model.User{
		Username: "ftr1705-buyer", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&admin).Error)
	require.NoError(t, db.Create(&buyer).Error)
	require.Equal(t, common.RoleAdminUser, admin.Role)
	site := model.AdtySite{
		OwnerUserId: admin.Id, Subdomain: "ftr1705", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: buyer.Id, TradeNo: "ftr-1705-order", Amount: ftrAccountQuota,
		Status: common.TopUpStatusSuccess, PaymentProvider: model.PaymentProviderAdtyEpay,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyTopupOrder{
		UserId: buyer.Id, TradeNo: "ftr-1705-order", SiteId: site.Id, Provider: model.PaymentProviderAdtyEpay,
	}).Error)

	ctx, rec := newAuthenticatedContext(t, http.MethodGet, "/api/user/admin/finance/recharge-audit/ftr-1705-order?section=payments&p=1&page_size=10", nil, admin.Id)
	ctx.Set("role", admin.Role)
	ctx.Set("username", admin.Username)
	ctx.Params = gin.Params{{Key: "trade_no", Value: "ftr-1705-order"}}
	GetRechargeAuditOrder(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr-1705-order")
	assert.Contains(t, body, "topup")
	ftrAssertQuotaAndNoLedger(t, db, admin.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, buyer.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// ADTY-17-08: Root reads a site wallet ledger through the admin route.
func TestFTRADTY1708AdminSiteWalletLedgerPG(t *testing.T) {
	db := ftrAccountDB(t)
	root := model.User{
		Username: "ftr1708-root", Password: "hashed-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&root).Error)
	require.Equal(t, common.RoleRootUser, root.Role)
	site := model.AdtySite{
		OwnerUserId: root.Id, Subdomain: "ftr1708", Status: model.AdtySiteStatusActive,
		AgreementVer: service.AdtyCurrentSiteAgreementVersion(),
	}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Create(&model.AdtySiteWalletLedger{
		SiteId: site.Id, WalletKind: model.AdtySiteWalletEarnings, Type: "query",
		RefId: "ftr1708", Reason: "ftr1708 ledger row", IdempotencyKey: "ftr1708-ledger",
		Delta: decimal.Zero, HoldDelta: decimal.Zero, BalanceBefore: decimal.Zero, BalanceAfter: decimal.Zero,
	}).Error)

	ctx, rec := newAuthenticatedContext(t, http.MethodGet, "/api/adty/admin/sites/"+strconv.Itoa(site.Id)+"/wallet-ledgers?p=1&page_size=10", nil, root.Id)
	ctx.Set("role", root.Role)
	ctx.Set("username", root.Username)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(site.Id)}}
	GetAdtyAdminSiteWalletLedger(ctx)
	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code, body)
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "ftr1708 ledger row")
	var rows int64
	require.NoError(t, db.Model(&model.AdtySiteWalletLedger{}).Where("site_id = ?", site.Id).Count(&rows).Error)
	assert.Equal(t, int64(1), rows)
	ftrAssertQuotaAndNoLedger(t, db, root.Id, ftrAccountQuota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}
