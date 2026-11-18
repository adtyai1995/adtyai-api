package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func prepareControllerNotificationDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")
	t.Setenv("ADTY_TELEGRAM_ALLOW_NETWORK", "")
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_pragma=busy_timeout(5000)"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.AdtySite{},
		&model.AdtySiteStaff{},
		&model.AdtyTeam{},
		&model.AdtyTeamMember{},
		&model.AdtyTelegramBot{},
		&model.AdtyTelegramBinding{},
		&model.AdtyTelegramDelivery{},
		&model.AdtyNotificationPublication{},
		&model.AdtyNotificationRead{},
		&model.AdtyNotificationHide{},
		&model.AdtyTranslationConfig{},
		&model.AdtyTranslationHead{},
		&model.AdtyTranslationJob{},
		&model.AdtyTranslationUsage{},
		&model.AdtyGlossaryTerm{},
	))
	previous := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = previous
		_ = sqlDB.Close()
	})
	return db
}

func TestGetAdtyAnnouncementControllerResolvesBeyondFirstPageAndTeamUserScope(t *testing.T) {
	db := prepareControllerNotificationDB(t)
	root := model.User{Username: "ctrl-root", Password: "password123", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&root).Error)
	user1 := model.User{Username: "ctrl-u1", Password: "password123", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user1).Error)
	user2 := model.User{Username: "ctrl-u2", Password: "password123", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user2).Error)

	// Create 25 announcements so announcement #1 is on page 2 (beyond first 20 items).
	var firstID int
	var firstReadKey string
	for i := 1; i <= 25; i++ {
		body, _ := json.Marshal(map[string]any{
			"title":    fmt.Sprintf("Notice %d", i),
			"content":  fmt.Sprintf("Content %d", i),
			"kind":     "platform",
			"audience": "all_users",
		})
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", root.Id)
		c.Set("notification_root", true)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/adty/admin/notifications", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		WriteAdtyManagedNotification(c)
		require.Equal(t, http.StatusOK, w.Code)
		if i == 1 {
			var resp struct {
				Success bool                              `json:"success"`
				Data    model.AdtyNotificationPublication `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.True(t, resp.Success)
			firstID = resp.Data.Id
			var firstRow model.AdtyNotificationPublication
			require.NoError(t, db.First(&firstRow, firstID).Error)
			firstReadKey = firstRow.ReadKey
		}
	}

	// Publish a user-targeted announcement for user1 via controller.
	userBody, _ := json.Marshal(map[string]any{
		"title":          "Direct to User 1",
		"content":        "Private content",
		"kind":           "system",
		"audience":       "user",
		"target_user_id": user1.Id,
	})
	wUser := httptest.NewRecorder()
	cUser, _ := gin.CreateTestContext(wUser)
	cUser.Set("id", root.Id)
	cUser.Set("notification_root", true)
	cUser.Request = httptest.NewRequest(http.MethodPost, "/api/adty/admin/notifications", bytes.NewReader(userBody))
	cUser.Request.Header.Set("Content-Type", "application/json")
	WriteAdtyManagedNotification(cUser)
	require.Equal(t, http.StatusOK, wUser.Code)
	var userPubResp struct {
		Success bool                              `json:"success"`
		Data    model.AdtyNotificationPublication `json:"data"`
	}
	require.NoError(t, json.Unmarshal(wUser.Body.Bytes(), &userPubResp))
	require.True(t, userPubResp.Success)

	// GetAdtyAnnouncement for item #1 (page 2) succeeds by both publication:<id> and read_key.
	for _, lookupKey := range []string{fmt.Sprintf("publication:%d", firstID), firstReadKey} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", user1.Id)
		c.Params = gin.Params{{Key: "key", Value: lookupKey}}
		c.Request = httptest.NewRequest(http.MethodGet, "/api/adty/notifications/announcements/"+lookupKey, nil)
		GetAdtyAnnouncement(c)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "Notice 1")
	}

	// User 1 can fetch their direct notification detail; User 2 gets 404 Not Found.
	directKey := fmt.Sprintf("publication:%d", userPubResp.Data.Id)
	w1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(w1)
	c1.Set("id", user1.Id)
	c1.Params = gin.Params{{Key: "key", Value: directKey}}
	c1.Request = httptest.NewRequest(http.MethodGet, "/api/adty/notifications/announcements/"+directKey, nil)
	GetAdtyAnnouncement(c1)
	require.Equal(t, http.StatusOK, w1.Code)
	assert.Contains(t, w1.Body.String(), "Direct to User 1")

	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Set("id", user2.Id)
	c2.Params = gin.Params{{Key: "key", Value: directKey}}
	c2.Request = httptest.NewRequest(http.MethodGet, "/api/adty/notifications/announcements/"+directKey, nil)
	GetAdtyAnnouncement(c2)
	require.Equal(t, http.StatusNotFound, w2.Code)

	// Verify service package reference is used cleanly.
	_, _, _ = service.GetAdtyNotificationDetail(user1.Id, nil, directKey)
}
