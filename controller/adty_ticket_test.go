package controller

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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

func setupControllerTicketTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
	})
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.AdtySite{}, &model.AdtySiteStaff{}, &model.AdtyTeam{}, &model.AdtyTeamMember{},
		&model.AdtyTicket{}, &model.AdtyTicketMessage{}, &model.AdtyTicketImage{}, &model.AdtyTicketEvent{},
		&model.AdtyTicketPolicy{}, &model.AdtyTicketNotice{},
		&model.AdtyTelegramBot{}, &model.AdtyTelegramBinding{}, &model.AdtyTelegramDelivery{},
	))
	return db
}

func TestControllerTicketEndpointsEnforceIdempotencyAndImageAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupControllerTicketTestDB(t)
	restore := service.SetAdtyTicketImageDirForTest(t.TempDir())
	t.Cleanup(restore)

	userA := &model.User{Username: "ctrl-ticket-a", Password: "hashed-password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, SiteId: 1}
	userB := &model.User{Username: "ctrl-ticket-b", Password: "hashed-password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, SiteId: 2}
	require.NoError(t, db.Create(userA).Error)
	require.NoError(t, db.Create(userB).Error)

	// User A creates a ticket with Idempotency-Key "ctrl-shared-1".
	recA := httptest.NewRecorder()
	ctxA, _ := gin.CreateTestContext(recA)
	ctxA.Request = httptest.NewRequest(http.MethodPost, "/api/adty/tickets", strings.NewReader(`{"type":"billing","title":"User A Secret","body":"A body"}`))
	ctxA.Request.Header.Set("Content-Type", "application/json")
	ctxA.Request.Header.Set("Idempotency-Key", "ctrl-shared-1")
	ctxA.Set("id", userA.Id)
	PostAdtyTicket(ctxA)
	require.Contains(t, recA.Body.String(), `"success":true`)
	require.Contains(t, recA.Body.String(), `"User A Secret"`)

	// User B creates with the same Idempotency-Key "ctrl-shared-1" and must not receive User A's ticket.
	recB := httptest.NewRecorder()
	ctxB, _ := gin.CreateTestContext(recB)
	ctxB.Request = httptest.NewRequest(http.MethodPost, "/api/adty/tickets", strings.NewReader(`{"type":"api","title":"User B Own","body":"B body"}`))
	ctxB.Request.Header.Set("Content-Type", "application/json")
	ctxB.Request.Header.Set("Idempotency-Key", "ctrl-shared-1")
	ctxB.Set("id", userB.Id)
	PostAdtyTicket(ctxB)
	require.Contains(t, recB.Body.String(), `"success":true`)
	assert.Contains(t, recB.Body.String(), `"User B Own"`)
	assert.NotContains(t, recB.Body.String(), `"User A Secret"`)

	var ticketA model.AdtyTicket
	require.NoError(t, db.Where("user_id = ?", userA.Id).First(&ticketA).Error)

	// Upload an image to ticketA.
	var pngBuf bytes.Buffer
	require.NoError(t, png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	b64 := base64.StdEncoding.EncodeToString(pngBuf.Bytes())

	recUpload := httptest.NewRecorder()
	ctxUpload, _ := gin.CreateTestContext(recUpload)
	ctxUpload.Request = httptest.NewRequest(http.MethodPost, "/api/adty/tickets/"+strconv.FormatInt(ticketA.Id, 10)+"/images",
		strings.NewReader(`{"data":"`+b64+`","visibility":"public"}`))
	ctxUpload.Request.Header.Set("Content-Type", "application/json")
	ctxUpload.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(ticketA.Id, 10)}}
	ctxUpload.Set("id", userA.Id)
	PostAdtyTicketImage(ctxUpload)
	require.Contains(t, recUpload.Body.String(), `"success":true`)

	var imgRow model.AdtyTicketImage
	require.NoError(t, db.Where("ticket_id = ?", ticketA.Id).First(&imgRow).Error)
	assert.Len(t, imgRow.ObjectId, 32)

	// When image_view audit insert fails, GetAdtyTicketImage returns an error JSON instead of serving the file.
	const cbName = "ctrl:fail_image_view"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(cbName, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Schema != nil && tx.Statement.Schema.Table == "adty_ticket_events" {
			_ = tx.AddError(errors.New("event audit write failed"))
		}
	}))
	t.Cleanup(func() {
		_ = db.Callback().Create().Remove(cbName)
	})

	recView := httptest.NewRecorder()
	ctxView, _ := gin.CreateTestContext(recView)
	ctxView.Request = httptest.NewRequest(http.MethodGet, "/api/adty/tickets/"+strconv.FormatInt(ticketA.Id, 10)+"/images/"+strconv.FormatInt(imgRow.Id, 10), nil)
	ctxView.Params = gin.Params{
		{Key: "id", Value: strconv.FormatInt(ticketA.Id, 10)},
		{Key: "imageId", Value: strconv.FormatInt(imgRow.Id, 10)},
	}
	ctxView.Set("id", userA.Id)
	GetAdtyTicketImage(ctxView)
	assert.Contains(t, recView.Body.String(), `"success":false`)
	assert.NotEqual(t, "image/png", recView.Header().Get("Content-Type"))
}
