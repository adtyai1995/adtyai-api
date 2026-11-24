package controller

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type r37APIResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func setupR37ControllerTestDB(t *testing.T) (*gorm.DB, model.User, model.User, model.ReferralAffiliate) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	tables := []any{
		&model.Option{},
		&model.User{},
		&model.Log{},
		&model.AuditLog{},
		&model.AuditIntent{},
		&model.AdtySite{},
		&model.AdtySiteStaff{},
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

	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemCache
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
		_ = sqlDB.Close()
	})

	admin := model.User{
		Username:    "r37_root_admin",
		DisplayName: "Root Admin",
		Role:        common.RoleRootUser,
		Status:      common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&admin).Error)

	target := model.User{
		Username:    "r37_affiliate_user",
		DisplayName: "Affiliate User",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&target).Error)

	aff := model.ReferralAffiliate{
		UserId:             target.Id,
		InviteCode:         "R37MAIN01",
		Status:             model.ReferralAffiliateStatusApproved,
		AcquisitionEnabled: true,
		SettlementEnabled:  true,
		WithdrawalEnabled:  true,
	}
	require.NoError(t, db.Create(&aff).Error)

	acc := model.ReferralCommissionAccount{
		AffiliateId:        aff.Id,
		UserId:             target.Id,
		SettlementCurrency: "USD",
		AvailableAmount:    100,
	}
	require.NoError(t, db.Create(&acc).Error)

	return db, admin, target, aff
}

func invokeUpdateReferralAffiliate(t *testing.T, admin model.User, targetUserID int, rawBody string) (int, r37APIResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("id", admin.Id)
	c.Set("role", admin.Role)
	c.Set("username", admin.Username)
	c.Params = gin.Params{{Key: "user_id", Value: strconv.Itoa(targetUserID)}}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/referral/affiliates/"+strconv.Itoa(targetUserID), strings.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "r37-controller-test")
	req.RemoteAddr = "198.51.100.7:12345"
	c.Request = req

	UpdateReferralAffiliate(c)

	var resp r37APIResponse
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp), "raw body: %s", rec.Body.String())
	return rec.Code, resp
}

func TestR37MainSiteUpdateReferralAffiliateRateOverrideAndClear(t *testing.T) {
	db, admin, target, aff := setupR37ControllerTestDB(t)

	// 1. Set a fractional custom rate override (0.2%) and a whole percentage (20%).
	for _, rateVal := range []float64{0.2, 20} {
		payload, err := common.Marshal(map[string]any{
			"action": "rate",
			"rate":   rateVal,
			"reason": "custom tier",
		})
		require.NoError(t, err)
		code, resp := invokeUpdateReferralAffiliate(t, admin, target.Id, string(payload))
		require.Equal(t, http.StatusOK, code)
		require.True(t, resp.Success, "message=%s", resp.Message)

		var reloaded model.ReferralAffiliate
		require.NoError(t, db.First(&reloaded, aff.Id).Error)
		require.NotNil(t, reloaded.RateOverride)
		assert.InDelta(t, rateVal, *reloaded.RateOverride, 1e-6)
	}

	// 2. Clear rate_override back to nil (SQL NULL in DB).
	code, resp := invokeUpdateReferralAffiliate(t, admin, target.Id, `{"action":"rate","rate":null,"reason":"restore default"}`)
	require.Equal(t, http.StatusOK, code)
	require.True(t, resp.Success, "message=%s", resp.Message)

	var reloaded model.ReferralAffiliate
	require.NoError(t, db.First(&reloaded, aff.Id).Error)
	assert.Nil(t, reloaded.RateOverride, "clearing rate must store NULL in DB")

	var isNull int
	require.NoError(t, db.Raw("SELECT CASE WHEN rate_override IS NULL THEN 1 ELSE 0 END FROM referral_affiliates WHERE id = ?", aff.Id).Scan(&isNull).Error)
	assert.Equal(t, 1, isNull, "rate_override column must be SQL NULL")

	// 3. Verify audit logs recorded non-empty OldValue and NewValue for all 3 updates.
	var logs []model.ReferralAdminAuditLog
	require.NoError(t, db.Where("affiliate_id = ?", aff.Id).Order("id ASC").Find(&logs).Error)
	require.Len(t, logs, 3)

	for _, entry := range logs {
		assert.Equal(t, "rate", entry.Action)
		assert.Equal(t, target.Id, entry.TargetUserId)
		assert.Equal(t, admin.Id, entry.AdminUserId)
		assert.NotEmpty(t, strings.TrimSpace(entry.OldValue))
		assert.NotEmpty(t, strings.TrimSpace(entry.NewValue))
	}

	var clearOld map[string]any
	require.NoError(t, common.Unmarshal([]byte(logs[2].OldValue), &clearOld))
	assert.Equal(t, float64(20), clearOld["rate_override"])

	var clearNew map[string]any
	require.NoError(t, common.Unmarshal([]byte(logs[2].NewValue), &clearNew))
	assert.Nil(t, clearNew["rate"])
	assert.Equal(t, "restore default", logs[2].Reason)
}

func TestR37MainSiteUpdateReferralAffiliateRejectsInvalidRates(t *testing.T) {
	db, admin, target, aff := setupR37ControllerTestDB(t)

	// Seed an initial valid rate_override so we can confirm rejected calls leave DB untouched.
	seed := 12.5
	require.NoError(t, db.Model(&model.ReferralAffiliate{}).Where("id = ?", aff.Id).Update("rate_override", seed).Error)

	// Main-site affiliate rates are percentages in [0, 100]; negative, >100, and non-finite JSON tokens must fail.
	cases := []struct {
		name string
		body string
	}{
		{name: "negative", body: `{"action":"rate","rate":-0.1}`},
		{name: "negative large", body: `{"action":"rate","rate":-5}`},
		{name: "over 100", body: `{"action":"rate","rate":100.01}`},
		{name: "way over 100", body: `{"action":"rate","rate":150}`},
		{name: "NaN token", body: `{"action":"rate","rate":NaN}`},
		{name: "Infinity token", body: `{"action":"rate","rate":Infinity}`},
		{name: "NaN string", body: `{"action":"rate","rate":"NaN"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, resp := invokeUpdateReferralAffiliate(t, admin, target.Id, tc.body)
			assert.False(t, resp.Success, "expected invalid rate %s to fail", tc.name)
		})
	}

	// Verify DB rate_override remained unchanged and no audit log was written.
	var reloaded model.ReferralAffiliate
	require.NoError(t, db.First(&reloaded, aff.Id).Error)
	require.NotNil(t, reloaded.RateOverride)
	assert.InDelta(t, seed, *reloaded.RateOverride, 1e-6)

	var auditCount int64
	require.NoError(t, db.Model(&model.ReferralAdminAuditLog{}).Where("affiliate_id = ?", aff.Id).Count(&auditCount).Error)
	assert.Zero(t, auditCount)

	// Boundary values 0 and 100 must succeed.
	for _, valid := range []float64{0, 100} {
		require.False(t, math.IsNaN(valid))
		payload, err := common.Marshal(map[string]any{"action": "rate", "rate": valid})
		require.NoError(t, err)
		_, resp := invokeUpdateReferralAffiliate(t, admin, target.Id, string(payload))
		assert.True(t, resp.Success, "boundary rate %v should succeed: %s", valid, resp.Message)
	}
}

func TestR37MainSiteUpdateReferralAffiliateDisableRestoreAndFreezes(t *testing.T) {
	db, admin, target, aff := setupR37ControllerTestDB(t)

	// 1. disable -> restore
	_, resp := invokeUpdateReferralAffiliate(t, admin, target.Id, `{"action":"disable","reason":"policy breach"}`)
	require.True(t, resp.Success, resp.Message)

	var reloaded model.ReferralAffiliate
	require.NoError(t, db.First(&reloaded, aff.Id).Error)
	assert.Equal(t, model.ReferralAffiliateStatusDisabled, reloaded.Status)
	assert.Equal(t, admin.Id, reloaded.DisabledBy)
	assert.Greater(t, reloaded.DisabledAt, int64(0))

	_, resp = invokeUpdateReferralAffiliate(t, admin, target.Id, `{"action":"restore","reason":"appeal approved"}`)
	require.True(t, resp.Success, resp.Message)

	require.NoError(t, db.First(&reloaded, aff.Id).Error)
	assert.Equal(t, model.ReferralAffiliateStatusApproved, reloaded.Status)
	assert.Equal(t, 0, reloaded.DisabledBy)
	assert.Equal(t, int64(0), reloaded.DisabledAt)

	// 2. freeze_settlement -> restore_settlement (clearing BOTH risk_reason and risk_note)
	// Seed an automated risk_reason (e.g. batch_register) to confirm restore_settlement clears both columns.
	require.NoError(t, db.Model(&model.ReferralAffiliate{}).Where("id = ?", aff.Id).Update("risk_reason", model.ReferralRiskBatchRegister).Error)

	_, resp = invokeUpdateReferralAffiliate(t, admin, target.Id, `{"action":"freeze_settlement","reason":"manual risk freeze"}`)
	require.True(t, resp.Success, resp.Message)

	require.NoError(t, db.First(&reloaded, aff.Id).Error)
	assert.False(t, reloaded.SettlementEnabled)
	assert.Equal(t, model.ReferralRiskBatchRegister, reloaded.RiskReason)
	assert.Equal(t, "manual risk freeze", reloaded.RiskNote)

	_, resp = invokeUpdateReferralAffiliate(t, admin, target.Id, `{"action":"restore_settlement","reason":"risk cleared"}`)
	require.True(t, resp.Success, resp.Message)

	require.NoError(t, db.First(&reloaded, aff.Id).Error)
	assert.True(t, reloaded.SettlementEnabled)
	assert.Empty(t, reloaded.RiskReason, "restore_settlement must clear risk_reason")
	assert.Empty(t, reloaded.RiskNote, "restore_settlement must clear risk_note")

	// 3. freeze_withdrawal -> restore_withdrawal
	_, resp = invokeUpdateReferralAffiliate(t, admin, target.Id, `{"action":"freeze_withdrawal","reason":"payout audit"}`)
	require.True(t, resp.Success, resp.Message)

	require.NoError(t, db.First(&reloaded, aff.Id).Error)
	assert.False(t, reloaded.WithdrawalEnabled)

	_, resp = invokeUpdateReferralAffiliate(t, admin, target.Id, `{"action":"restore_withdrawal","reason":"audit passed"}`)
	require.True(t, resp.Success, resp.Message)

	require.NoError(t, db.First(&reloaded, aff.Id).Error)
	assert.True(t, reloaded.WithdrawalEnabled)

	// 4. Verify ReferralAdminAuditLog entries for all 6 actions have non-empty OldValue and NewValue.
	var logs []model.ReferralAdminAuditLog
	require.NoError(t, db.Where("affiliate_id = ?", aff.Id).Order("id ASC").Find(&logs).Error)
	expectedActions := []string{
		"disable",
		"restore",
		"freeze_settlement",
		"restore_settlement",
		"freeze_withdrawal",
		"restore_withdrawal",
	}
	require.Len(t, logs, len(expectedActions))
	for i, wantAction := range expectedActions {
		assert.Equal(t, wantAction, logs[i].Action)
		assert.Equal(t, target.Id, logs[i].TargetUserId)
		assert.Equal(t, admin.Id, logs[i].AdminUserId)
		assert.NotEmpty(t, strings.TrimSpace(logs[i].OldValue), "action=%s old_value must not be empty", wantAction)
		assert.NotEmpty(t, strings.TrimSpace(logs[i].NewValue), "action=%s new_value must not be empty", wantAction)

		var oldMap, newMap map[string]any
		require.NoError(t, common.Unmarshal([]byte(logs[i].OldValue), &oldMap), "action=%s old_value must be valid JSON", wantAction)
		require.NoError(t, common.Unmarshal([]byte(logs[i].NewValue), &newMap), "action=%s new_value must be valid JSON", wantAction)
		assert.Equal(t, wantAction, newMap["action"])
	}
}
