package controller

import (
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY0107AnnouncementListAndDetailPG(t *testing.T) {
	db := ftrAccountDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.AdtyNotificationPublication{},
		&model.AdtyNotificationRead{},
		&model.AdtyNotificationHide{},
	))
	reader := model.User{
		Username: "ftr0107", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	other := model.User{
		Username: "ftr0107b", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: ftrAccountQuota,
	}
	require.NoError(t, db.Create(&reader).Error)
	require.NoError(t, db.Create(&other).Error)
	readKey := "ftr0107" + strings.Repeat("b", 64-len("ftr0107"))
	require.Len(t, readKey, 64)
	const title = "FTR0107 platform announcement"
	require.NoError(t, db.Create(&model.AdtyNotificationPublication{
		IssuerSiteId: 0, PublisherId: reader.Id, PublisherName: reader.Username,
		Audience: "all_users", Kind: "system", Title: title,
		Content: "The announcement body is visible.", Status: "published",
		Revision: 1, ReadKey: readKey, PublishedAt: 1, UpdatedAt: 1, UpdatedBy: reader.Id,
	}).Error)
	require.NoError(t, db.Create(&model.AdtyNotificationPublication{
		IssuerSiteId: 0, PublisherId: reader.Id, PublisherName: reader.Username,
		Audience: "all_users", Kind: "system", Title: "FTR0107 draft hidden",
		Content: "draft must stay out of the list", Status: "draft",
		Revision: 1, ReadKey: "ftr0107draft" + strings.Repeat("c", 64-len("ftr0107draft")),
		PublishedAt: 1, UpdatedAt: 1, UpdatedBy: reader.Id,
	}).Error)

	ctx, rec := newAuthenticatedContext(t, http.MethodGet, "/api/adty/notifications", nil, reader.Id)
	ctx.Set("role", reader.Role)
	GetAdtyNotifications(ctx)
	body := rec.Body.String()
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, title)
	assert.Contains(t, body, readKey)
	assert.NotContains(t, body, "FTR0107 draft hidden")
	assert.Equal(t, common.RoleCommonUser, reader.Role)

	detailCtx, detailRec := newAuthenticatedContext(t, http.MethodGet, "/api/adty/notifications/announcements/"+readKey, nil, reader.Id)
	detailCtx.Set("role", reader.Role)
	detailCtx.Params = gin.Params{{Key: "key", Value: readKey}}
	GetAdtyAnnouncement(detailCtx)
	detail := detailRec.Body.String()
	require.Contains(t, detail, `"success":true`, detail)
	assert.Contains(t, detail, title)
	assert.Contains(t, detail, "The announcement body is visible.")
	assert.NotContains(t, detail, "FTR0107 draft hidden")

	otherCtx, otherRec := newAuthenticatedContext(t, http.MethodGet, "/api/adty/notifications", nil, other.Id)
	otherCtx.Set("role", other.Role)
	GetAdtyNotifications(otherCtx)
	otherBody := otherRec.Body.String()
	require.Contains(t, otherBody, `"success":true`, otherBody)
	assert.Contains(t, otherBody, title)
	assert.NotContains(t, otherBody, "FTR0107 draft hidden")

	ftrAssertQuotaAndNoLedger(t, db, reader.Id, ftrAccountQuota)
	ftrAssertQuotaAndNoLedger(t, db, other.Id, ftrAccountQuota)
}
