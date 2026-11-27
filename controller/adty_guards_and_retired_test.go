package controller

import (
	"bytes"
	"encoding/base64"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testTinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func setupGuardsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	tables := []any{
		&model.Option{},
		&model.User{},
		&model.AdtySite{},
		&model.AdtySiteStaff{},
		&model.AdtySiteInviteWithdrawal{},
		&model.AdtyBalanceLedger{},
		&model.AdtyGroup{},
		&model.AdtyTeam{},
		&model.AdtyTeamMember{},
		&model.Channel{},
		&model.Ability{},
		&model.Model{},
		&model.Vendor{},
	}
	tables = append(tables, model.ReferralTables()...)
	require.NoError(t, db.AutoMigrate(tables...))

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedis := common.RedisEnabled
	oldMemCache := common.MemoryCacheEnabled
	oldOptionMap := common.OptionMap

	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	model.DB = db
	model.LOG_DB = db
	model.InvalidatePricingCache()
	service.InvalidateAdtySiteCache()

	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
		model.InvalidatePricingCache()
		service.InvalidateAdtySiteCache()
		_ = sqlDB.Close()
	})

	return db
}

func TestUserReferralBlocksAdminAndRoot(t *testing.T) {
	db := setupGuardsTestDB(t)

	rootUser := model.User{Username: "guard_root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	adminUser := model.User{Username: "guard_admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	normalUser := model.User{Username: "guard_user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&rootUser).Error)
	require.NoError(t, db.Create(&adminUser).Error)
	require.NoError(t, db.Create(&normalUser).Error)

	router := gin.New()
	userGroup := router.Group("/api/user/referral", func(c *gin.Context) {
		uid, _ := strconv.Atoi(c.GetHeader("X-Test-User-ID"))
		role, _ := strconv.Atoi(c.GetHeader("X-Test-Role"))
		c.Set("id", uid)
		c.Set("role", role)
		c.Next()
	}, RejectAdminUserReferral)
	userGroup.GET("/profile", GetReferralProfile)
	userGroup.GET("/commissions", GetReferralCommissions)
	userGroup.POST("/upload", UploadReferralAsset)

	for _, tc := range []struct {
		name string
		user model.User
	}{
		{name: "admin", user: adminUser},
		{name: "root", user: rootUser},
	} {
		t.Run(tc.name+"_blocked_on_router_and_handler", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/user/referral/profile", nil)
			req.Header.Set("X-Test-User-ID", strconv.Itoa(tc.user.Id))
			req.Header.Set("X-Test-Role", strconv.Itoa(tc.user.Role))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusForbidden, rec.Code)

			var resp struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
			assert.False(t, resp.Success)
			assert.Equal(t, "Administrators cannot use user referral endpoints", resp.Message)

			// Direct handler call without router middleware must also reject.
			directRec := httptest.NewRecorder()
			directCtx, _ := gin.CreateTestContext(directRec)
			directCtx.Request = httptest.NewRequest(http.MethodGet, "/api/user/referral/profile", nil)
			directCtx.Set("id", tc.user.Id)
			directCtx.Set("role", tc.user.Role)
			GetReferralProfile(directCtx)
			assert.Equal(t, http.StatusForbidden, directRec.Code)

			var affCount int64
			require.NoError(t, db.Model(&model.ReferralAffiliate{}).Where("user_id = ?", tc.user.Id).Count(&affCount).Error)
			assert.Zero(t, affCount, "admin/root must never auto-create a referral affiliate row")
		})
	}

	t.Run("common_user_succeeds", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/user/referral/profile", nil)
		req.Header.Set("X-Test-User-ID", strconv.Itoa(normalUser.Id))
		req.Header.Set("X-Test-Role", strconv.Itoa(normalUser.Role))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		var resp struct {
			Success bool `json:"success"`
		}
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
		assert.True(t, resp.Success)

		var affCount int64
		require.NoError(t, db.Model(&model.ReferralAffiliate{}).Where("user_id = ?", normalUser.Id).Count(&affCount).Error)
		assert.Equal(t, int64(1), affCount)
	})
}

func TestPostAdtySiteWithdrawalQRRetiresAlipayQRButKeepsPaymentProof(t *testing.T) {
	t.Chdir(t.TempDir())
	db := setupGuardsTestDB(t)

	owner := model.User{Username: "qr_owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner).Error)
	site := model.AdtySite{
		OwnerUserId: owner.Id,
		Subdomain:   "qrowner",
		RootDomain:  "example.com",
		Status:      model.AdtySiteStatusActive,
	}
	require.NoError(t, db.Create(&site).Error)

	pngBytes, err := base64.StdEncoding.DecodeString(testTinyPNGBase64)
	require.NoError(t, err)

	makeUploadReq := func(purpose string) *http.Request {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		if purpose != "" {
			require.NoError(t, writer.WriteField("purpose", purpose))
		}
		part, createErr := writer.CreateFormFile("file", "test.png")
		require.NoError(t, createErr)
		_, writeErr := part.Write(pngBytes)
		require.NoError(t, writeErr)
		require.NoError(t, writer.Close())
		req := httptest.NewRequest(http.MethodPost, "/api/adty/site/withdrawals/qr", &buf)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		return req
	}

	for _, tc := range []struct {
		name    string
		purpose string
	}{
		{name: "default_purpose", purpose: ""},
		{name: "explicit_withdrawal_qr", purpose: model.ReferralAssetPurposeWithdrawalQR},
	} {
		t.Run(tc.name+"_returns_410", func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = makeUploadReq(tc.purpose)
			c.Set("id", owner.Id)
			c.Set("role", owner.Role)

			PostAdtySiteWithdrawalQR(c)

			assert.Equal(t, http.StatusGone, rec.Code)
			var assetCount int64
			require.NoError(t, db.Model(&model.ReferralAsset{}).Count(&assetCount).Error)
			assert.Zero(t, assetCount, "retired Alipay QR upload must not persist any asset")
		})
	}

	t.Run("payment_proof_remains_allowed_for_active_owner", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = makeUploadReq(model.ReferralAssetPurposePaymentProof)
		c.Set("id", owner.Id)
		c.Set("role", owner.Role)

		PostAdtySiteWithdrawalQR(c)

		assert.Equal(t, http.StatusOK, rec.Code)
		var resp struct {
			Success bool `json:"success"`
			Data    struct {
				URL string `json:"url"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
		assert.True(t, strings.HasPrefix(resp.Data.URL, "/api/referral-assets/"))

		var assetCount int64
		require.NoError(t, db.Model(&model.ReferralAsset{}).Where("purpose = ?", model.ReferralAssetPurposePaymentProof).Count(&assetCount).Error)
		assert.Equal(t, int64(1), assetCount)
	})
}

func TestRetiredSiteTermsSelfUseAndGroupHealthEndpointsReturn410(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name    string
		method  string
		target  string
		handler gin.HandlerFunc
	}{
		{name: "site_terms_accept", method: http.MethodPost, target: "/api/adty/site/terms/accept", handler: AcceptAdtySiteTerms},
		{name: "owner_earning_to_personal", method: http.MethodPost, target: "/api/adty/site/wallets/earning-to-personal", handler: PostOwnerPersonalTransfer},
		{name: "owner_self_use_balance", method: http.MethodGet, target: "/api/adty/site/wallets/self-use", handler: GetOwnerSelfUseBalance},
		{name: "owner_self_use_usage", method: http.MethodGet, target: "/api/adty/site/self-use/usage", handler: GetOwnerSelfUseUsage},
		{name: "owner_self_use_transfers", method: http.MethodGet, target: "/api/adty/site/self-use/transfers", handler: GetOwnerPrincipalTransfers},
		{name: "owner_self_use_keys", method: http.MethodPost, target: "/api/adty/site/self-use/keys", handler: AddOwnerSelfUseToken},
		{name: "admin_group_health_get", method: http.MethodGet, target: "/api/adty/admin/group-health", handler: GetAdtyAdminGroupHealth},
		{name: "admin_group_health_put", method: http.MethodPut, target: "/api/adty/admin/group-health", handler: PutAdtyGroupHealth},
		{name: "admin_group_health_probe", method: http.MethodPost, target: "/api/adty/admin/group-health/probe", handler: PostAdtyGroupProbe},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(tc.method, tc.target, nil)
			c.Set("id", 1)
			c.Set("role", common.RoleRootUser)

			tc.handler(c)

			assert.Equal(t, http.StatusGone, rec.Code)
			var resp struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
			assert.False(t, resp.Success)
			assert.NotEmpty(t, resp.Message)
		})
	}
}

func TestPricingAndUptimeIgnoreLegacyGroupGroupRatioAndAutoGroup(t *testing.T) {
	db := setupGuardsTestDB(t)

	oldGroupRatio := ratio_setting.GroupRatio2JSONString()
	oldGroupGroupRatio := ratio_setting.GroupGroupRatio2JSONString()
	oldUsableGroups := setting.UserUsableGroups2JSONString()
	oldAutoGroups := setting.AutoGroups2JsonString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateGroupRatioByJSONString(oldGroupRatio)
		_ = ratio_setting.UpdateGroupGroupRatioByJSONString(oldGroupGroupRatio)
		_ = setting.UpdateUserUsableGroupsByJSONString(oldUsableGroups)
		_ = setting.UpdateAutoGroupsByJsonString(oldAutoGroups)
	})

	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1.0,"vip":2.0}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"vip":{"default":0.25,"vip":0.5}}`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"auto":"Auto group","default":"Default","vip":"VIP"}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["default","vip"]`))

	vipUser := model.User{Username: "legacy_vip", Group: "vip", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&vipUser).Error)

	// 1. GetPricing must not apply GroupGroupRatio or inject "auto" or return auto_groups.
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/pricing", nil)
	c.Set("id", vipUser.Id)
	GetPricing(c)
	require.Equal(t, http.StatusOK, rec.Code)

	var pricingResp struct {
		Success     bool               `json:"success"`
		GroupRatio  map[string]float64 `json:"group_ratio"`
		UsableGroup map[string]string  `json:"usable_group"`
		AutoGroups  []string           `json:"auto_groups"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &pricingResp))
	assert.True(t, pricingResp.Success)
	assert.InDelta(t, 1.0, pricingResp.GroupRatio["default"], 1e-6)
	assert.InDelta(t, 2.0, pricingResp.GroupRatio["vip"], 1e-6)
	assert.NotContains(t, pricingResp.UsableGroup, "auto")
	assert.Empty(t, pricingResp.AutoGroups)

	// 2. Requesting ?group=auto must be rejected with 403.
	autoRec := httptest.NewRecorder()
	autoCtx, _ := gin.CreateTestContext(autoRec)
	autoCtx.Request = httptest.NewRequest(http.MethodGet, "/api/pricing?group=auto", nil)
	autoCtx.Set("id", vipUser.Id)
	GetPricing(autoCtx)
	assert.Equal(t, http.StatusForbidden, autoRec.Code)

	// 3. resolveUptimeGroupRatios must ignore stale GroupGroupRatio.
	uptimeCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	uptimeCtx.Request = httptest.NewRequest(http.MethodGet, "/api/uptime/status", nil)
	uptimeCtx.Set("id", vipUser.Id)
	ratios := resolveUptimeGroupRatios(uptimeCtx, 0, []UptimeGroupResult{
		{GroupID: "default"},
		{GroupID: "vip"},
	})
	assert.InDelta(t, 1.0, ratios["default"], 1e-6)
	assert.InDelta(t, 2.0, ratios["vip"], 1e-6)
}

func TestUserLedgerAndClosedSiteWithdrawalsPopulateActorUsernames(t *testing.T) {
	db := setupGuardsTestDB(t)

	rootUser := model.User{Username: "actor_root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	customer := model.User{Username: "actor_customer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	reviewer := model.User{Username: "actor_reviewer", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	payer := model.User{Username: "actor_payer", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	failer := model.User{Username: "actor_failer", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	legacyActor := model.User{Username: "actor_legacy", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	for _, u := range []*model.User{&rootUser, &customer, &reviewer, &payer, &failer, &legacyActor} {
		require.NoError(t, db.Create(u).Error)
	}

	// 1. User balance ledger returns operator_username for positive operator_id.
	opEntry := model.AdtyBalanceLedger{
		UserId:        customer.Id,
		DeltaQuota:    500000,
		BalanceBefore: 0,
		BalanceAfter:  500000,
		Type:          model.AdtyLedgerTypeManualAdjust,
		Reason:        "manual topup",
		OperatorId:    rootUser.Id,
	}
	sysEntry := model.AdtyBalanceLedger{
		UserId:        customer.Id,
		DeltaQuota:    100000,
		BalanceBefore: 500000,
		BalanceAfter:  600000,
		Type:          model.AdtyLedgerTypeTopUp,
		Reason:        "online topup",
		OperatorId:    0,
	}
	require.NoError(t, db.Create(&opEntry).Error)
	require.NoError(t, db.Create(&sysEntry).Error)

	ledgerRec := httptest.NewRecorder()
	ledgerCtx, _ := gin.CreateTestContext(ledgerRec)
	ledgerCtx.Request = httptest.NewRequest(http.MethodGet, "/api/adty/admin/users/"+strconv.Itoa(customer.Id)+"/ledger?p=1&page_size=20", nil)
	ledgerCtx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(customer.Id)}}
	ledgerCtx.Set("id", rootUser.Id)
	ledgerCtx.Set("role", rootUser.Role)
	GetAdtyUserLedger(ledgerCtx)
	require.Equal(t, http.StatusOK, ledgerRec.Code)

	var ledgerResp struct {
		Success bool `json:"success"`
		Data    struct {
			Total int `json:"total"`
			Items []struct {
				ID               int64  `json:"id"`
				OperatorID       int    `json:"operator_id"`
				OperatorUsername string `json:"operator_username"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(ledgerRec.Body.Bytes(), &ledgerResp))
	assert.True(t, ledgerResp.Success)
	require.Len(t, ledgerResp.Data.Items, 2)
	byID := map[int64]string{}
	for _, item := range ledgerResp.Data.Items {
		byID[item.ID] = item.OperatorUsername
	}
	assert.Equal(t, rootUser.Username, byID[opEntry.Id])
	assert.Empty(t, byID[sysEntry.Id])

	// 2. Closed-site commission withdrawals expose all actor username fields.
	closedSite := model.AdtySite{
		OwnerUserId: legacyActor.Id,
		Subdomain:   "closed-actors",
		RootDomain:  "example.com",
		Status:      model.AdtySiteStatusClosed,
	}
	require.NoError(t, db.Create(&closedSite).Error)
	withdrawal := model.AdtySiteInviteWithdrawal{
		SiteId:           closedSite.Id,
		UserId:           customer.Id,
		RequestedUSD:     decimal.NewFromInt(20),
		AmountUSD:        decimal.NewFromInt(20),
		NetUSD:           decimal.NewFromInt(20),
		Method:           "usdt",
		Network:          "TRC20",
		Status:           model.AdtySiteInviteWithdrawPaid,
		IdempotencyKey:   "closed-actor-test-1",
		ReviewedBy:       reviewer.Id,
		PaidBy:           payer.Id,
		FailureBy:        failer.Id,
		LegacyDecisionBy: legacyActor.Id,
	}
	require.NoError(t, db.Create(&withdrawal).Error)

	wRec := httptest.NewRecorder()
	wCtx, _ := gin.CreateTestContext(wRec)
	wCtx.Request = httptest.NewRequest(http.MethodGet, "/api/user/admin/referral/closed-site-withdrawals?p=1&page_size=20", nil)
	wCtx.Set("id", rootUser.Id)
	wCtx.Set("role", rootUser.Role)
	GetClosedSiteInviteWithdrawals(wCtx)
	require.Equal(t, http.StatusOK, wRec.Code)

	var wResp struct {
		Success bool `json:"success"`
		Data    struct {
			Items []struct {
				ID                       int64  `json:"id"`
				Username                 string `json:"username"`
				ReviewedByUsername       string `json:"reviewed_by_username"`
				PaidByUsername           string `json:"paid_by_username"`
				FailureByUsername        string `json:"failure_by_username"`
				LegacyDecisionByUsername string `json:"legacy_decision_by_username"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(wRec.Body.Bytes(), &wResp))
	assert.True(t, wResp.Success)
	require.Len(t, wResp.Data.Items, 1)
	row := wResp.Data.Items[0]
	assert.Equal(t, customer.Username, row.Username)
	assert.Equal(t, reviewer.Username, row.ReviewedByUsername)
	assert.Equal(t, payer.Username, row.PaidByUsername)
	assert.Equal(t, failer.Username, row.FailureByUsername)
	assert.Equal(t, legacyActor.Username, row.LegacyDecisionByUsername)
}

func TestPostAdtyRootExpiringGiftRetired(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("root gets 410 Gone", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/adty/admin/users/42/expiring-gifts", nil)
		ctx.Set("role", common.RoleRootUser)

		PostAdtyRootExpiringGift(ctx)

		assert.Equal(t, http.StatusGone, rec.Code)
		assert.Contains(t, rec.Body.String(), "Expiring trial gifts have been retired")
	})

	t.Run("non-root is rejected", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/adty/admin/users/42/expiring-gifts", nil)
		ctx.Set("role", common.RoleAdminUser)

		PostAdtyRootExpiringGift(ctx)

		assert.NotEqual(t, http.StatusGone, rec.Code)
		assert.Contains(t, rec.Body.String(), "permission")
	})
}

func TestRechargeBonusEndpointsUSDTOnly(t *testing.T) {
	db := setupGuardsTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AuditIntent{}, &model.AuditLog{}))

	oldBonusDB := model.DB
	adty_setting.SetAuthoritativeBonusDB(db)
	t.Cleanup(func() {
		adty_setting.SetAuthoritativeBonusDB(oldBonusDB)
		_ = adty_setting.ApplyMethodBonusOption(adty_setting.TopupSettingKeyMethodBonus, "{}")
		_ = adty_setting.ApplyMethodBonusOption(adty_setting.TopupSettingKeySiteMethodBonus, "{}")
	})

	require.NoError(t, db.Create(&model.Option{
		Key:   adty_setting.TopupSettingKeyMethodBonus,
		Value: `{"alipay":{"enabled":true,"rate":"0.05"},"wxpay":{"enabled":true,"rate":"0.04"},"usdt":{"enabled":true,"rate":"0.03"}}`,
	}).Error)

	// GET main-site bonus methods strips legacy Alipay/WeChat Pay keys.
	getRec := httptest.NewRecorder()
	getCtx, _ := gin.CreateTestContext(getRec)
	getCtx.Request = httptest.NewRequest(http.MethodGet, "/api/adty/admin/bonus-methods", nil)
	GetAdtyMainMethodBonus(getCtx)
	require.Equal(t, http.StatusOK, getRec.Code)
	var getResp struct {
		Success bool                                    `json:"success"`
		Data    map[string]adty_setting.MethodBonusRule `json:"data"`
	}
	require.NoError(t, common.Unmarshal(getRec.Body.Bytes(), &getResp))
	assert.True(t, getResp.Success)
	assert.Equal(t, map[string]adty_setting.MethodBonusRule{"usdt": {Enabled: true, Rate: "0.03"}}, getResp.Data)

	// PUT main-site rejects Alipay and WeChat Pay.
	for _, method := range []string{"alipay", "wxpay"} {
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodPut, "/api/adty/admin/bonus-methods", strings.NewReader(`{"method":"`+method+`","enabled":true,"rate":"0.05"}`))
		ctx.Set("id", 1)
		ctx.Set("role", common.RoleRootUser)
		PutAdtyMainMethodBonus(ctx)
		assert.Contains(t, rec.Body.String(), "Recharge bonus is only supported for USDT")
	}

	// PUT main-site accepts USDT and cleans legacy keys from options.
	putRec := httptest.NewRecorder()
	putCtx, _ := gin.CreateTestContext(putRec)
	putCtx.Request = httptest.NewRequest(http.MethodPut, "/api/adty/admin/bonus-methods", strings.NewReader(`{"method":"usdt","enabled":true,"rate":"0.025"}`))
	putCtx.Set("id", 1)
	putCtx.Set("role", common.RoleRootUser)
	PutAdtyMainMethodBonus(putCtx)
	require.Equal(t, http.StatusOK, putRec.Code)
	var putResp struct {
		Success bool                                    `json:"success"`
		Data    map[string]adty_setting.MethodBonusRule `json:"data"`
	}
	require.NoError(t, common.Unmarshal(putRec.Body.Bytes(), &putResp))
	assert.True(t, putResp.Success)
	assert.Equal(t, map[string]adty_setting.MethodBonusRule{"usdt": {Enabled: true, Rate: "0.025"}}, putResp.Data)

	var opt model.Option
	require.NoError(t, db.Where("key = ?", adty_setting.TopupSettingKeyMethodBonus).First(&opt).Error)
	assert.NotContains(t, opt.Value, "alipay")
	assert.NotContains(t, opt.Value, "wxpay")

	// PUT subsite rejects Alipay and WeChat Pay before step-up verification.
	owner := model.User{Username: "bonus_owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner).Error)
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "bonusonly", RootDomain: "example.com", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)

	for _, method := range []string{"alipay", "wxpay"} {
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodPut, "/api/adty/site/bonus-methods", strings.NewReader(`{"method":"`+method+`","enabled":true,"rate":"0.05"}`))
		ctx.Set("id", owner.Id)
		ctx.Set("role", owner.Role)
		PutAdtySiteMethodBonus(ctx)
		assert.Contains(t, rec.Body.String(), "Recharge bonus is only supported for USDT")
	}
}
