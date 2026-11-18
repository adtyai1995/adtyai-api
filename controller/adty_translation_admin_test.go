package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAdtyTranslationAdminTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_pragma=busy_timeout(5000)"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	oldMain, oldLog := model.DB, model.LOG_DB
	oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis := common.RedisEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldMain, oldLog
		common.SetDatabaseTypes(oldMainType, oldLogType)
		common.RedisEnabled = oldRedis
		_ = sqlDB.Close()
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.AuditLog{}, &model.Option{}))
	require.NoError(t, model.MigrateAdtyTables())
	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "root",
		Password: "password123",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
	}).Error)
	return db
}

func TestAdtyTranslationAdminTasksRetryAndEditHandlers(t *testing.T) {
	setupAdtyTranslationAdminTestDB(t)

	_, err := service.SaveAdtyTranslationConfig(service.AdtyTranslationInput{
		Mode:      model.AdtyTranslationModeSet,
		ModelName: "translator",
		Enabled:   true,
	})
	require.NoError(t, err)

	oldTranslate := service.AdtyTranslateFunc
	service.AdtyTranslateFunc = func(_, _ string) (string, error) {
		return "", errors.New("upstream error")
	}
	t.Cleanup(func() { service.AdtyTranslateFunc = oldTranslate })

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("id", 1)
		c.Next()
	})
	engine.POST("/api/adty/admin/translation/run", PostAdtyTranslationRun)
	engine.GET("/api/adty/admin/translation/tasks", GetAdtyTranslationTasks)
	engine.POST("/api/adty/admin/translation/retry", PostAdtyTranslationRetry)
	engine.POST("/api/adty/admin/translation/complete", PostAdtyTranslationComplete)
	engine.PUT("/api/adty/admin/translation/edit", PutAdtyTranslationEdit)

	// 1. Run fails and persists failed job
	runBody, _ := json.Marshal(map[string]any{
		"object_key": "notice:ctrl-1",
		"source":     "金额 12.50 USD，订单 A123456，不退款",
	})
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/adty/admin/translation/run", bytes.NewReader(runBody)))
	require.Equal(t, http.StatusOK, rec.Code)

	// 2. List failed tasks
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/adty/admin/translation/tasks?status=failed", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var listResp struct {
		Success bool `json:"success"`
		Data    struct {
			Tasks []service.AdtyTranslationTaskView `json:"tasks"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listResp))
	require.True(t, listResp.Success)
	require.Len(t, listResp.Data.Tasks, 1)
	assert.Equal(t, "notice:ctrl-1", listResp.Data.Tasks[0].ObjectKey)
	assert.Equal(t, 1, listResp.Data.Tasks[0].Version)
	assert.Equal(t, model.AdtyTranslationFailed, listResp.Data.Tasks[0].Status)

	// 3. Retry same object_key and version
	service.AdtyTranslateFunc = func(_, _ string) (string, error) {
		return "Amount 12.50 USD order A123456 is not refunded", nil
	}
	retryBody, _ := json.Marshal(map[string]any{
		"object_key": "notice:ctrl-1",
		"version":    1,
	})
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/adty/admin/translation/retry", bytes.NewReader(retryBody)))
	require.Equal(t, http.StatusOK, rec.Code)
	var retryResp struct {
		Success bool                          `json:"success"`
		Data    service.AdtyTranslationResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &retryResp))
	require.True(t, retryResp.Success)
	assert.Equal(t, model.AdtyTranslationReady, retryResp.Data.Status)
	assert.Equal(t, 1, retryResp.Data.Version)

	// 4. Manual edit
	editBody, _ := json.Marshal(map[string]any{
		"object_key": "notice:ctrl-1",
		"english":    "Manual override 12.50 USD order A123456 is not refunded",
	})
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/adty/admin/translation/edit", bytes.NewReader(editBody)))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAdtyTranslationAdminGlossaryCRUDHandlers(t *testing.T) {
	setupAdtyTranslationAdminTestDB(t)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("id", 1)
		c.Next()
	})
	engine.GET("/api/adty/admin/translation/glossary", GetAdtyTranslationGlossary)
	engine.POST("/api/adty/admin/translation/glossary", PostAdtyTranslationGlossary)
	engine.PUT("/api/adty/admin/translation/glossary/:id", PutAdtyTranslationGlossary)
	engine.DELETE("/api/adty/admin/translation/glossary/:id", DeleteAdtyTranslationGlossary)

	// Create
	createBody, _ := json.Marshal(map[string]any{
		"source": "算力券",
		"locale": "en",
		"target": "compute credit",
	})
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/adty/admin/translation/glossary", bytes.NewReader(createBody)))
	require.Equal(t, http.StatusOK, rec.Code)
	var createdResp struct {
		Success bool                   `json:"success"`
		Data    model.AdtyGlossaryTerm `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &createdResp))
	require.True(t, createdResp.Success)
	require.NotZero(t, createdResp.Data.Id)

	// Update
	updateBody, _ := json.Marshal(map[string]any{
		"source": "算力券",
		"locale": "en",
		"target": "compute voucher",
	})
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/adty/admin/translation/glossary/"+strconv.FormatInt(createdResp.Data.Id, 10), bytes.NewReader(updateBody)))
	require.Equal(t, http.StatusOK, rec.Code)

	// List
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/adty/admin/translation/glossary?locale=en", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var listResp struct {
		Success bool `json:"success"`
		Data    struct {
			Terms []model.AdtyGlossaryTerm `json:"terms"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listResp))
	require.True(t, listResp.Success)
	require.Len(t, listResp.Data.Terms, 1)
	assert.Equal(t, "compute voucher", listResp.Data.Terms[0].Target)

	// Delete
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/adty/admin/translation/glossary/"+strconv.FormatInt(createdResp.Data.Id, 10), nil))
	require.Equal(t, http.StatusOK, rec.Code)
}
