package router

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

func TestTelegramUnbindRetainsHistoryAndScopePG(t *testing.T) {
	f := newOwnerFixture(t)
	pgtest.RecordServerVersion(t, f.db)
	require.NoError(t, f.db.AutoMigrate(&model.AuditIntent{}))
	other := f.customer(t, f.other, "telegram-other", "telegram-other@example.com")
	customer := f.customer(t, f.site, "telegram-customer", "telegram-customer@example.com")
	rows := []model.AdtyTelegramBinding{
		{UserId: f.owner.Id, Purpose: "user", TelegramUserId: 431, Status: "active"},
		{UserId: f.owner.Id, Purpose: "admin", SiteId: f.site.Id, TelegramUserId: 433, Status: "active"},
		{UserId: other.Id, Purpose: "user", SiteId: f.other.Id, TelegramUserId: 439, Status: "active"},
	}
	require.NoError(t, f.db.Create(&rows).Error)
	challenges := []model.AdtyTelegramChallenge{
		{UserId: f.owner.Id, Purpose: "user", CodeHash: "unbind-owner-user", ExpiresAt: time.Now().Add(time.Minute).Unix()},
		{UserId: f.owner.Id, Purpose: "admin", CodeHash: "unbind-owner-admin", ExpiresAt: time.Now().Add(time.Minute).Unix()},
		{UserId: other.Id, Purpose: "user", CodeHash: "unbind-other-user", ExpiresAt: time.Now().Add(time.Minute).Unix()},
	}
	require.NoError(t, f.db.Create(&challenges).Error)
	deliveries := []model.AdtyTelegramDelivery{
		{UserId: f.owner.Id, Purpose: "user", BindingId: rows[0].Id, EventKey: "unbind-pending", Status: "pending"},
		{UserId: f.owner.Id, Purpose: "user", BindingId: rows[0].Id, EventKey: "unbind-failed", Status: "failed"},
		{UserId: f.owner.Id, Purpose: "user", BindingId: rows[0].Id, EventKey: "unbind-sent", Status: "sent"},
		{UserId: f.owner.Id, Purpose: "admin", BindingId: rows[1].Id, EventKey: "unbind-admin", Status: "pending"},
		{UserId: other.Id, Purpose: "user", BindingId: rows[2].Id, EventKey: "unbind-other", Status: "pending"},
	}
	require.NoError(t, f.db.Create(&deliveries).Error)
	denied := f.siteJSON(t, http.MethodDelete, "/api/adty/telegram/me", "", "")
	require.NotEqual(t, http.StatusOK, denied.Code)
	response := f.call(t, http.MethodDelete, "/api/adty/telegram/me?purpose=admin&user_id="+fmt.Sprint(other.Id), `{"purpose":"admin"}`, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"success":true`)
	var saved []model.AdtyTelegramBinding
	require.NoError(t, f.db.Order("id").Find(&saved).Error)
	require.Len(t, saved, 3, "unbind keeps historical binding rows")
	require.Equal(t, "revoked", saved[0].Status)
	require.Equal(t, int64(431), saved[0].TelegramUserId)
	require.Equal(t, "active", saved[1].Status)
	require.Equal(t, "active", saved[2].Status)
	require.NoError(t, f.db.Order("id").Find(&challenges).Error)
	require.Positive(t, challenges[0].UsedAt)
	require.Zero(t, challenges[1].UsedAt)
	require.Zero(t, challenges[2].UsedAt)
	require.NoError(t, f.db.Order("id").Find(&deliveries).Error)
	for i, status := range []string{"skipped", "skipped", "sent", "pending", "pending"} {
		require.Equal(t, status, deliveries[i].Status)
	}
	var audit model.AuditLog
	require.NoError(t, model.LOG_DB.Where("action = ? AND user_id = ?", "telegram.unbind", f.owner.Id).First(&audit).Error)
	require.True(t, audit.Success)
	require.Contains(t, audit.BeforeValue, "active")
	require.Contains(t, audit.AfterValue, "revoked")
	repeat := f.call(t, http.MethodDelete, "/api/adty/telegram/me", "", "")
	require.Equal(t, http.StatusOK, repeat.Code, repeat.Body.String())
	session, err := service.CreateLoginSessionOnSite(customer.Id, f.site.Id, "password", "127.0.0.1", "telegram-scope")
	require.NoError(t, err)
	denied = f.siteJSON(t, http.MethodDelete, "/api/adty/site/telegram", "", session.AccessToken)
	require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
	// A failed durable audit must roll the binding, challenge and delivery back together.
	require.NoError(t, f.db.Migrator().DropTable(&model.AuditIntent{}))
	failed := f.call(t, http.MethodDelete, "/api/adty/site/telegram", "", "")
	require.Contains(t, failed.Body.String(), `"success":false`)
	require.NoError(t, f.db.Order("id").Find(&saved).Error)
	require.Equal(t, "active", saved[1].Status)
	require.NoError(t, f.db.Order("id").Find(&challenges).Error)
	require.Zero(t, challenges[1].UsedAt)
	require.NoError(t, f.db.Order("id").Find(&deliveries).Error)
	require.Equal(t, "pending", deliveries[3].Status)
	require.NoError(t, f.db.AutoMigrate(&model.AuditIntent{}))
	success := f.call(t, http.MethodDelete, "/api/adty/site/telegram", "", "")
	require.Equal(t, http.StatusOK, success.Code, success.Body.String())
	require.Contains(t, success.Body.String(), `"success":true`)
	require.NoError(t, f.db.Order("id").Find(&saved).Error)
	require.Equal(t, "revoked", saved[1].Status)
	require.Equal(t, "active", saved[2].Status)
}
