package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupVoucherControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
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
	service.ResetAdtyVoucherGuesses()
	t.Cleanup(func() {
		service.ResetAdtyVoucherGuesses()
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		_ = sqlDB.Close()
	})
	return db
}

func callVoucherHandler(handler gin.HandlerFunc, userID, role int, body string) (int, map[string]any) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", userID)
	c.Set("role", role)
	handler(c)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

func TestPostAdtyVoucherRedeemAndSiteMarginVoucherRedeemIsolation(t *testing.T) {
	db := setupVoucherControllerTestDB(t)

	root := model.User{Id: 1, Username: "vc_root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	owner := model.User{Id: 2, Username: "vc_owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	customer := model.User{Id: 3, Username: "vc_cust", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&customer).Error)

	site := model.AdtySite{Id: 5, OwnerUserId: owner.Id, Status: model.AdtySiteStatusActive, Subdomain: "vc5"}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, service.EnsureAdtySiteWallets(site.Id))
	require.NoError(t, service.SetAdtyVoucherIssuanceLimit(root.Id, decimal.NewFromInt(10000)))

	marginV, err := service.IssueAdtyVoucher(service.AdtyVoucherIssueInput{
		IssuerId: root.Id, Purpose: model.AdtyVoucherPurposeSiteMargin, SiteId: site.Id,
		DesignatedOwnerId: owner.Id, AmountUSD: decimal.NewFromInt(50), Reason: "margin test",
	})
	require.NoError(t, err)

	mainV, err := service.IssueAdtyVoucher(service.AdtyVoucherIssueInput{
		IssuerId: root.Id, Purpose: model.AdtyVoucherPurposeMainUser,
		AmountUSD: decimal.NewFromInt(12), Reason: "main user test",
	})
	require.NoError(t, err)

	// Customer endpoint rejects site_margin voucher for owner.
	_, resp := callVoucherHandler(PostAdtyVoucherRedeem, owner.Id, owner.Role, `{"code":"`+marginV.Code+`"}`)
	assert.Equal(t, false, resp["success"])
	assert.Equal(t, service.ErrAdtyVoucherDenied.Error(), resp["message"])

	// Margin endpoint rejects non-owner with 403.
	code, _ := callVoucherHandler(PostAdtySiteMarginVoucherRedeem, customer.Id, customer.Role, `{"code":"`+marginV.Code+`"}`)
	assert.Equal(t, http.StatusForbidden, code)

	// Margin endpoint rejects main_user voucher even when called by site owner.
	_, resp = callVoucherHandler(PostAdtySiteMarginVoucherRedeem, owner.Id, owner.Role, `{"code":"`+mainV.Code+`"}`)
	assert.Equal(t, false, resp["success"])

	// Margin endpoint redeems site_margin voucher cleanly.
	code, resp = callVoucherHandler(PostAdtySiteMarginVoucherRedeem, owner.Id, owner.Role, `{"code":"`+marginV.Code+`"}`)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, true, resp["success"])

	// Customer endpoint redeems main_user voucher cleanly.
	code, resp = callVoucherHandler(PostAdtyVoucherRedeem, customer.Id, customer.Role, `{"code":"`+mainV.Code+`"}`)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, true, resp["success"])
}

func TestPostAdtyVoucherPreValidates2DecimalsAnd100kCapBeforeProof(t *testing.T) {
	db := setupVoucherControllerTestDB(t)

	root := model.User{Id: 1, Username: "vp_root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	owner := model.User{Id: 2, Username: "vp_owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&owner).Error)
	site := model.AdtySite{Id: 6, OwnerUserId: owner.Id, Status: model.AdtySiteStatusActive, Subdomain: "vp6"}
	require.NoError(t, db.Create(&site).Error)

	badAmounts := []string{"10.123", "0", "-5", "100000.01"}
	for _, amt := range badAmounts {
		t.Run("site_voucher_rejects_"+amt, func(t *testing.T) {
			body := `{"purpose":"site_customer","amount_usd":"` + amt + `","count":1,"reason":"test","intent_key":"ik-1"}`
			_, resp := callVoucherHandler(PostAdtySiteVoucher, owner.Id, owner.Role, body)
			assert.Equal(t, false, resp["success"])
			assert.Equal(t, "Invalid parameters", resp["message"], "amount %s must fail pre-validation before step-up proof", amt)
		})
		t.Run("admin_voucher_rejects_"+amt, func(t *testing.T) {
			body := `{"purpose":"main_user","amount_usd":"` + amt + `","count":1,"reason":"test","intent_key":"ik-2"}`
			_, resp := callVoucherHandler(PostAdtyAdminVoucher, root.Id, root.Role, body)
			assert.Equal(t, false, resp["success"])
			assert.Equal(t, "Invalid parameters", resp["message"], "amount %s must fail pre-validation", amt)
		})
	}
}
