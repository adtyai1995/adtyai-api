package controller

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupControllerTicketTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	require.NotEmpty(t, dsn, "ticket regressions require isolated PostgreSQL")
	db := modelManagementDB(t, "postgres", dsn)
	pgtest.RecordServerVersion(t, db)
	oldSecret := common.CryptoSecret
	common.CryptoSecret = "local-ticket-regression-signing-key"
	t.Cleanup(func() { common.CryptoSecret = oldSecret })
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
	grant := controllerTicketImageGrant(t, userA.Id, ticketA.Id, imgRow.Id)
	ctxView.Request.Header.Set("X-Ticket-Image-Grant", grant)
	GetAdtyTicketImage(ctxView)
	assert.Contains(t, recView.Body.String(), `"success":false`)
	assert.NotEqual(t, "image/png", recView.Header().Get("Content-Type"))
}

func controllerTicketImageRequest(t *testing.T, userID int, ticketID, imageID int64, query, grant string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/adty/tickets/%d/images/%d%s", ticketID, imageID, query), nil)
	ctx.Request.Header.Set("X-Ticket-Image-Grant", grant)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(ticketID, 10)}, {Key: "imageId", Value: strconv.FormatInt(imageID, 10)}}
	ctx.Set("id", userID)
	GetAdtyTicketImage(ctx)
	return recorder
}

func controllerTicketImageGrant(t *testing.T, userID int, ticketID, imageID int64, scope ...string) string {
	t.Helper()
	query := "?action=grant"
	if len(scope) > 0 {
		query += "&scope=" + scope[0]
	}
	recorder := controllerTicketImageRequest(t, userID, ticketID, imageID, query, "")
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Grant     string `json:"grant"`
			ExpiresAt int64  `json:"expires_at"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response), recorder.Body.String())
	require.True(t, response.Success, recorder.Body.String())
	require.NotEmpty(t, response.Data.Grant)
	assert.Greater(t, response.Data.ExpiresAt, time.Now().Unix())
	assert.LessOrEqual(t, response.Data.ExpiresAt, time.Now().Unix()+300)
	assert.Empty(t, recorder.Result().Cookies())
	assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")
	return response.Data.Grant
}

func TestTicketImageGrantRequiresCurrentObjectScopedAuthorizationPG(t *testing.T) {
	db := setupControllerTicketTestDB(t)
	restore := service.SetAdtyTicketImageDirForTest(t.TempDir())
	t.Cleanup(restore)
	users := []model.User{
		{Username: "grant-customer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, SiteId: 41, AuthVersion: 1},
		{Username: "grant-other", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, SiteId: 42, AuthVersion: 1},
		{Username: "grant-staff", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, StaffSiteId: 41, SiteId: 41, AuthVersion: 1},
	}
	require.NoError(t, db.Create(&users).Error)
	owner := model.User{Username: "grant-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&model.AdtySite{Id: 41, OwnerUserId: owner.Id, Subdomain: "grant-site", Status: model.AdtySiteStatusActive, AgreementVer: service.AdtyCurrentSiteAgreementVersion()}).Error)
	staff := model.AdtySiteStaff{UserId: users[2].Id, OwnerUserId: owner.Id, SiteId: 41, Status: "enabled", Role: "support", Permissions: `["tickets.manage"]`}
	require.NoError(t, db.Create(&staff).Error)
	ticket := model.AdtyTicket{UserId: users[0].Id, SiteId: 41, Title: "Receipt", Status: model.AdtyTicketNew}
	require.NoError(t, db.Create(&ticket).Error)
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	public, err := service.AddAdtyTicketImage(users[0].Id, ticket.Id, model.AdtyTicketVisibilityPublic, encoded.Bytes())
	require.NoError(t, err)
	internal, err := service.AddAdtyTicketImage(users[2].Id, ticket.Id, model.AdtyTicketVisibilityNote, encoded.Bytes())
	require.NoError(t, err)

	t.Run("long_lived_session_without_image_grant_is_rejected", func(t *testing.T) {
		recorder := controllerTicketImageRequest(t, users[0].Id, ticket.Id, public.Id, "", "")
		assert.NotEqual(t, "image/png", recorder.Header().Get("Content-Type"))
		assert.Contains(t, recorder.Body.String(), `"success":false`)
	})
	t.Run("visible_image_grant_reads_and_audits", func(t *testing.T) {
		grant := controllerTicketImageGrant(t, users[0].Id, ticket.Id, public.Id)
		recorder := controllerTicketImageRequest(t, users[0].Id, ticket.Id, public.Id, "", grant)
		require.Equal(t, "image/png", recorder.Header().Get("Content-Type"))
		assert.Equal(t, encoded.Bytes(), recorder.Body.Bytes())
		assert.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
		var views int64
		require.NoError(t, db.Model(&model.AdtyTicketEvent{}).Where("ticket_id = ? AND actor_id = ? AND action = ?", ticket.Id, users[0].Id, "image_view").Count(&views).Error)
		assert.Equal(t, int64(1), views)
	})
	t.Run("grant_cannot_be_used_for_other_identity_or_object", func(t *testing.T) {
		grant := controllerTicketImageGrant(t, users[2].Id, ticket.Id, internal.Id, "staff")
		for _, input := range []struct {
			user          int
			ticket, image int64
			grant         string
		}{
			{users[0].Id, ticket.Id, internal.Id, grant},
			{users[2].Id, ticket.Id, public.Id, grant},
			{users[2].Id, ticket.Id + 1, internal.Id, grant},
			{users[2].Id, ticket.Id, internal.Id, "invalid-grant"},
		} {
			recorder := controllerTicketImageRequest(t, input.user, input.ticket, input.image, "", input.grant)
			assert.Contains(t, recorder.Body.String(), `"success":false`)
			assert.NotEqual(t, "image/png", recorder.Header().Get("Content-Type"))
		}
	})
	t.Run("invisible_image_cannot_get_a_grant", func(t *testing.T) {
		for _, input := range []struct {
			user  int
			image int64
		}{{users[0].Id, internal.Id}, {users[1].Id, public.Id}} {
			recorder := controllerTicketImageRequest(t, input.user, ticket.Id, input.image, "?action=grant", "")
			assert.Contains(t, recorder.Body.String(), `"success":false`)
		}
	})
	t.Run("revoked_staff_cannot_reuse_unexpired_grant", func(t *testing.T) {
		grant := controllerTicketImageGrant(t, users[2].Id, ticket.Id, internal.Id, "staff")
		require.NoError(t, db.Model(&staff).Update("status", "disabled").Error)
		recorder := controllerTicketImageRequest(t, users[2].Id, ticket.Id, internal.Id, "?scope=staff", grant)
		assert.Contains(t, recorder.Body.String(), `"success":false`)
		assert.NotEqual(t, "image/png", recorder.Header().Get("Content-Type"))
	})
	t.Run("missing_signing_secret_fails_closed", func(t *testing.T) {
		saved := common.CryptoSecret
		common.CryptoSecret = ""
		t.Cleanup(func() { common.CryptoSecret = saved })
		recorder := controllerTicketImageRequest(t, users[0].Id, ticket.Id, public.Id, "?action=grant", "")
		assert.Contains(t, recorder.Body.String(), `"success":false`)
	})
}

func TestTicketImageGrantRejectsExpiredOrExcessiveLifetimePG(t *testing.T) {
	db := setupControllerTicketTestDB(t)
	user := model.User{Username: "expiry-customer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	ticket := model.AdtyTicket{UserId: user.Id, Title: "Expiring image", Status: model.AdtyTicketNew}
	require.NoError(t, db.Create(&ticket).Error)
	restore := service.SetAdtyTicketImageDirForTest(t.TempDir())
	t.Cleanup(restore)
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	attachment, err := service.AddAdtyTicketImage(user.Id, ticket.Id, model.AdtyTicketVisibilityPublic, encoded.Bytes())
	require.NoError(t, err)
	// Sign explicit expiry values at the external credential boundary, without a clock sleep.
	for _, offset := range []int64{-1, 3600} {
		expiry := time.Now().Unix() + offset
		mac := hmac.New(sha256.New, []byte(common.CryptoSecret))
		_, err := fmt.Fprintf(mac, "adty-ticket-image\x00%d\x00%d\x00%d\x00%d", user.Id, ticket.Id, attachment.Id, expiry)
		require.NoError(t, err)
		grant := strconv.FormatInt(expiry, 10) + "." + hex.EncodeToString(mac.Sum(nil))
		recorder := controllerTicketImageRequest(t, user.Id, ticket.Id, attachment.Id, "", grant)
		assert.Contains(t, recorder.Body.String(), `"success":false`)
		assert.NotEqual(t, "image/png", recorder.Header().Get("Content-Type"))
	}
}
