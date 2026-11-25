package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupSettlementSheetControllerTestDB(t *testing.T) (*gorm.DB, model.AdtySite, model.AdtySiteSettlementSheet) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&model.AdtySite{},
		&model.AdtySiteOwnership{},
		&model.AdtySiteSettlementSheet{},
		&model.AdtySettlementRevision{},
		&model.AdtySettlementCoverageAdjustment{},
	))

	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		_ = sqlDB.Close()
	})

	site := model.AdtySite{
		OwnerUserId: 501,
		Subdomain:   "sheet-alpha",
		RootDomain:  "platform.test",
		Status:      model.AdtySiteStatusActive,
	}
	require.NoError(t, db.Create(&site).Error)

	sheet := model.AdtySiteSettlementSheet{
		SiteId:           site.Id,
		BusinessDay:      "2026-11-20",
		Timezone:         "Asia/Shanghai",
		DayStart:         1795104000,
		DayEnd:           1795190400,
		EventWatermark:   1795189999,
		IncludedCount:    7,
		BatchId:          "site-settlement:1:2026-11-20",
		TopupFaceUSD:     decimal.RequireFromString("100"),
		PaidUSD:          decimal.RequireFromString("100"),
		PaidByCurrency:   "{}",
		ConsumptionUSD:   decimal.RequireFromString("42.5"),
		SupplyCostUSD:    decimal.RequireFromString("30"),
		SpreadUSD:        decimal.RequireFromString("12.5"),
		EarningsDeltaUSD: decimal.RequireFromString("12.5"),
		GeneratedAt:      1795190500,
	}
	require.NoError(t, db.Create(&sheet).Error)

	legacy := model.AdtySettlementCoverageAdjustment{
		SheetId:        sheet.Id,
		BeforeUSD:      decimal.RequireFromString("5"),
		AfterUSD:       decimal.RequireFromString("2"),
		EventWatermark: 1795190111,
		CreatedAt:      1795190200,
	}
	require.NoError(t, db.Create(&legacy).Error)

	beforeSnap := sheet
	afterSnap := sheet
	afterSnap.ConsumptionUSD = decimal.RequireFromString("45.0")
	afterSnap.EventWatermark = 1795190333
	beforeBytes, err := common.Marshal(beforeSnap)
	require.NoError(t, err)
	afterBytes, err := common.Marshal(afterSnap)
	require.NoError(t, err)

	rev := model.AdtySettlementRevision{
		SheetId:   sheet.Id,
		Before:    beforeBytes,
		After:     afterBytes,
		Sources:   []string{"topup/ORD-001", "earning/99"},
		CreatedAt: 1795190900,
	}
	require.NoError(t, db.Create(&rev).Error)

	return db, site, sheet
}

func invokeSettlementSheetHandler(t *testing.T, handler gin.HandlerFunc, userID, role int, targetURL string) (int, map[string]common.RawMessage) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("id", userID)
	c.Set("role", role)
	c.Request = httptest.NewRequest(http.MethodGet, targetURL, nil)

	handler(c)

	var envelope struct {
		Success bool              `json:"success"`
		Message string            `json:"message"`
		Data    common.RawMessage `json:"data"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &envelope), "raw body: %s", rec.Body.String())
	require.True(t, envelope.Success, "message=%s body=%s", envelope.Message, rec.Body.String())
	return rec.Code, map[string]common.RawMessage{"data": envelope.Data}
}

func TestSettlementSheetOwnerStripsInternalAuditFieldsWhileAdminKeepsThem(t *testing.T) {
	_, site, sheet := setupSettlementSheetControllerTestDB(t)
	sheetIDStr := strconv.FormatInt(sheet.Id, 10)
	siteIDStr := strconv.Itoa(site.Id)

	// 1. Owner detail response omits batch_id, event_watermark, legacy_coverage[].event_watermark,
	// revisions[].sources, and nested before/after batch_id & event_watermark.
	code, env := invokeSettlementSheetHandler(t, GetAdtyOwnerSettlementSheets, site.OwnerUserId, common.RoleCommonUser,
		"/api/adty/site/settlement-sheets?sheet_id="+sheetIDStr)
	require.Equal(t, http.StatusOK, code)

	var ownerDetail map[string]common.RawMessage
	require.NoError(t, common.Unmarshal(env["data"], &ownerDetail))
	assert.NotContains(t, ownerDetail, "batch_id")
	assert.NotContains(t, ownerDetail, "event_watermark")
	assert.NotContains(t, ownerDetail, "sources")
	assert.Contains(t, ownerDetail, "business_day")
	assert.Contains(t, ownerDetail, "consumption_usd")

	var ownerLegacy []map[string]common.RawMessage
	require.NoError(t, common.Unmarshal(ownerDetail["legacy_coverage"], &ownerLegacy))
	require.Len(t, ownerLegacy, 1)
	assert.NotContains(t, ownerLegacy[0], "event_watermark")
	assert.Contains(t, ownerLegacy[0], "before_usd")
	assert.Contains(t, ownerLegacy[0], "after_usd")

	var ownerRevs []map[string]common.RawMessage
	require.NoError(t, common.Unmarshal(ownerDetail["revisions"], &ownerRevs))
	require.Len(t, ownerRevs, 1)
	assert.NotContains(t, ownerRevs[0], "sources")
	assert.Contains(t, ownerRevs[0], "id")
	assert.Contains(t, ownerRevs[0], "created_at")

	for _, snapKey := range []string{"before", "after"} {
		var snap map[string]common.RawMessage
		require.NoError(t, common.Unmarshal(ownerRevs[0][snapKey], &snap))
		assert.NotContains(t, snap, "batch_id", "owner revision %s must omit batch_id", snapKey)
		assert.NotContains(t, snap, "event_watermark", "owner revision %s must omit event_watermark", snapKey)
		assert.NotContains(t, snap, "sources", "owner revision %s must omit sources", snapKey)
		assert.Contains(t, snap, "consumption_usd")
	}

	// 2. Owner unpaginated array response also omits those fields.
	code, env = invokeSettlementSheetHandler(t, GetAdtyOwnerSettlementSheets, site.OwnerUserId, common.RoleCommonUser,
		"/api/adty/site/settlement-sheets")
	require.Equal(t, http.StatusOK, code)

	var ownerList []map[string]common.RawMessage
	require.NoError(t, common.Unmarshal(env["data"], &ownerList))
	require.Len(t, ownerList, 1)
	assert.NotContains(t, ownerList[0], "batch_id")
	assert.NotContains(t, ownerList[0], "event_watermark")
	assert.NotContains(t, ownerList[0], "sources")

	var ownerListRevs []map[string]common.RawMessage
	require.NoError(t, common.Unmarshal(ownerList[0]["revisions"], &ownerListRevs))
	require.Len(t, ownerListRevs, 1)
	assert.NotContains(t, ownerListRevs[0], "sources")

	// 3. Root admin detail response preserves batch_id, event_watermark, legacy event_watermark,
	// revisions[].sources, and nested revision snapshot fields verbatim.
	code, env = invokeSettlementSheetHandler(t, GetAdtyAdminSettlementSheets, 1, common.RoleRootUser,
		"/api/adty/admin/settlement-sheets?site_id="+siteIDStr+"&sheet_id="+sheetIDStr)
	require.Equal(t, http.StatusOK, code)

	var adminDetail model.AdtySiteSettlementSheet
	require.NoError(t, common.Unmarshal(env["data"], &adminDetail))
	assert.Equal(t, sheet.BatchId, adminDetail.BatchId)
	assert.Equal(t, sheet.EventWatermark, adminDetail.EventWatermark)
	require.Len(t, adminDetail.LegacyCoverage, 1)
	assert.Equal(t, int64(1795190111), adminDetail.LegacyCoverage[0].EventWatermark)
	require.Len(t, adminDetail.Revisions, 1)
	assert.Equal(t, []string{"topup/ORD-001", "earning/99"}, adminDetail.Revisions[0].Sources)

	var adminAfter model.AdtySiteSettlementSheet
	require.NoError(t, common.Unmarshal(adminDetail.Revisions[0].After, &adminAfter))
	assert.Equal(t, sheet.BatchId, adminAfter.BatchId)
	assert.Equal(t, int64(1795190333), adminAfter.EventWatermark)

	// 4. Root admin unpaginated list response also preserves internal audit fields.
	code, env = invokeSettlementSheetHandler(t, GetAdtyAdminSettlementSheets, 1, common.RoleRootUser,
		"/api/adty/admin/settlement-sheets?site_id="+siteIDStr)
	require.Equal(t, http.StatusOK, code)

	var adminList []model.AdtySiteSettlementSheet
	require.NoError(t, common.Unmarshal(env["data"], &adminList))
	require.Len(t, adminList, 1)
	assert.Equal(t, sheet.BatchId, adminList[0].BatchId)
	assert.Equal(t, sheet.EventWatermark, adminList[0].EventWatermark)
	require.Len(t, adminList[0].Revisions, 1)
	assert.Equal(t, []string{"topup/ORD-001", "earning/99"}, adminList[0].Revisions[0].Sources)
}
