package controller

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func workspaceTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{R: 12, G: 34, B: 56, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// ISS-R1-0118: PostAdtyTeamBindKey verifies that the requested :id matches the caller's active team.
func TestPostAdtyTeamBindKeyVerifiesRequestedTeamTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := modelManagementDB(t, "sqlite", "")
	require.NoError(t, model.MigrateAdtyTablesOn(db))
	require.NoError(t, db.AutoMigrate(&model.Token{}))

	leaderA := model.User{Username: "team-bind-leader-a", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	leaderB := model.User{Username: "team-bind-leader-b", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	memberA := model.User{Username: "team-bind-member-a", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&leaderA).Error)
	require.NoError(t, db.Create(&leaderB).Error)
	require.NoError(t, db.Create(&memberA).Error)

	teamA, err := service.CreateAdtyTeam(leaderA.Id, 0, "Team A", decimal.NewFromInt(1))
	require.NoError(t, err)
	teamB, err := service.CreateAdtyTeam(leaderB.Id, 0, "Team B", decimal.NewFromInt(1))
	require.NoError(t, err)

	_, err = service.JoinAdtyTeam(memberA.Id, teamA.Team.Id)
	require.ErrorIs(t, err, service.ErrAdtyTeamForbidden)
	employee, err := service.CreateAdtyTeamEmployee(leaderA.Id, teamA.Team.Id, "bind-emp-a", "StrongPassword123!", "Bind Emp")
	require.NoError(t, err)
	memberA.Id = employee.UserId

	token := model.Token{
		UserId:         memberA.Id,
		Key:            "team-bind-target-token-key",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}
	require.NoError(t, db.Create(&token).Error)

	callBind := func(userID int, targetTeamID int64, tokenID int) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		body := fmt.Sprintf(`{"token_id":%d}`, tokenID)
		c.Request = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/adty/teams/%d/keys", targetTeamID), strings.NewReader(body))
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", targetTeamID)}}
		c.Set("id", userID)
		PostAdtyTeamBindKey(c)
		var resp map[string]any
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &resp), w.Body.String())
		return resp
	}

	// Attempting to bind via another team's :id must be rejected and leave no binding on either team.
	mismatchResp := callBind(memberA.Id, teamB.Team.Id, token.Id)
	assert.Equal(t, false, mismatchResp["success"], mismatchResp)
	var boundCount int64
	require.NoError(t, db.Model(&model.AdtyTeamKey{}).Where("token_id = ?", token.Id).Count(&boundCount).Error)
	assert.Zero(t, boundCount)

	// Binding with the user's actual active team :id succeeds.
	okResp := callBind(memberA.Id, teamA.Team.Id, token.Id)
	require.Equal(t, true, okResp["success"], okResp)
	var teamKey model.AdtyTeamKey
	require.NoError(t, db.Where("token_id = ?", token.Id).First(&teamKey).Error)
	assert.Equal(t, teamA.Team.Id, teamKey.TeamId)
	assert.Equal(t, memberA.Id, teamKey.UserId)
	assert.Equal(t, model.AdtyTeamMemberActive, teamKey.Status)
}

// workspaceTicketImageGrantAt builds a credential at the verify MAC boundary.
// IssueAdtyTicketImageGrant rejects a non-positive remaining lifetime, so an
// expired sample must not be minted through that function.
func workspaceTicketImageGrantAt(userID int, ticketID, imageID, exp int64) string {
	return strconv.FormatInt(exp, 10) + "." + adtyTicketImageGrantMAC(userID, ticketID, imageID, exp)
}

// ISS-R1-0121: Ticket image access requires both a valid short-lived signed grant and live ticket authorization.
func TestGetAdtyTicketImageRequiresShortGrantAndLiveTicketPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := modelManagementDB(t, "sqlite", "")
	require.NoError(t, model.MigrateAdtyTablesOn(db))

	restoreDir := service.SetAdtyTicketImageDirForTest(t.TempDir())
	t.Cleanup(restoreDir)

	owner := model.User{Username: "ticket-img-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	customerA := model.User{Username: "ticket-img-cust-a", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	customerB := model.User{Username: "ticket-img-cust-b", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	staff := model.User{Username: "ticket-img-staff", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&customerA).Error)
	require.NoError(t, db.Create(&customerB).Error)
	require.NoError(t, db.Create(&staff).Error)

	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: "ticket-img-site", RootDomain: "sites.example.test", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, db.Model(&customerA).Update("site_id", site.Id).Error)
	require.NoError(t, db.Model(&customerB).Update("site_id", site.Id).Error)
	staffRow := model.AdtySiteStaff{
		SiteId:      site.Id,
		OwnerUserId: owner.Id,
		UserId:      staff.Id,
		Role:        "support",
		Status:      "enabled",
		Permissions: `["customers.read"]`,
	}
	require.NoError(t, db.Create(&staffRow).Error)

	ticketA, err := service.CreateAdtyTicket(customerA.Id, service.AdtyTicketInput{
		Type: "billing", Title: "Customer A ticket", Body: "see attached screenshot", Idempotency: "grant-test-a",
	})
	require.NoError(t, err)
	ticketB, err := service.CreateAdtyTicket(customerB.Id, service.AdtyTicketInput{
		Type: "billing", Title: "Customer B ticket", Body: "other ticket", Idempotency: "grant-test-b",
	})
	require.NoError(t, err)

	pngBytes := workspaceTestPNG(t)
	uploadW := httptest.NewRecorder()
	uploadC, _ := gin.CreateTestContext(uploadW)
	uploadBody := fmt.Sprintf(`{"data":%q,"visibility":"public"}`, base64.StdEncoding.EncodeToString(pngBytes))
	uploadC.Request = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/adty/tickets/%d/images", ticketA.Id), strings.NewReader(uploadBody))
	uploadC.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ticketA.Id)}}
	uploadC.Set("id", customerA.Id)
	PostAdtyTicketImage(uploadC)

	var uploadEnvelope map[string]any
	require.NoError(t, common.Unmarshal(uploadW.Body.Bytes(), &uploadEnvelope), uploadW.Body.String())
	require.Equal(t, true, uploadEnvelope["success"], uploadEnvelope)
	uploadData := uploadEnvelope["data"].(map[string]any)
	imageID := int64(uploadData["id"].(float64))
	issuedGrant, _ := uploadData["grant"].(string)
	require.NotEmpty(t, issuedGrant)
	_, hasUploadURL := uploadData["url"]
	assert.False(t, hasUploadURL, "upload must not return a grant URL")
	assertGrantStaysOutOfCookieAndLink(t, uploadW, issuedGrant)

	detailW := httptest.NewRecorder()
	detailC, _ := gin.CreateTestContext(detailW)
	detailC.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/adty/tickets/%d", ticketA.Id), nil)
	detailC.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ticketA.Id)}}
	detailC.Set("id", customerA.Id)
	GetAdtyTicket(detailC)

	var detailEnvelope map[string]any
	require.NoError(t, common.Unmarshal(detailW.Body.Bytes(), &detailEnvelope), detailW.Body.String())
	require.Equal(t, true, detailEnvelope["success"], detailEnvelope)
	detailData := detailEnvelope["data"].(map[string]any)
	_, hasImageURLs := detailData["image_urls"]
	assert.False(t, hasImageURLs, "detail must not return grant URLs")
	assert.NotContains(t, detailW.Body.String(), "?grant=")
	assert.NotContains(t, detailW.Body.String(), "grant=")
	assert.Empty(t, detailW.Result().Cookies())

	callGetImage := func(userID int, tID, imgID int64, rawQuery, headerGrant, cookieGrant string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		target := fmt.Sprintf("/api/adty/tickets/%d/images/%d", tID, imgID)
		if rawQuery != "" {
			target += "?" + rawQuery
		}
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if headerGrant != "" {
			req.Header.Set("X-Ticket-Image-Grant", headerGrant)
		}
		if cookieGrant != "" {
			req.AddCookie(&http.Cookie{
				Name:  fmt.Sprintf("adty_ticket_img_grant_%d_%d", tID, imgID),
				Value: cookieGrant,
			})
		}
		c.Request = req
		c.Params = gin.Params{
			{Key: "id", Value: fmt.Sprintf("%d", tID)},
			{Key: "imageId", Value: fmt.Sprintf("%d", imgID)},
		}
		c.Set("id", userID)
		GetAdtyTicketImage(c)
		return w
	}

	assertDenied := func(w *httptest.ResponseRecorder, msg string) {
		t.Helper()
		var env map[string]any
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &env), w.Body.String())
		assert.Equal(t, false, env["success"], msg)
		assert.NotEqual(t, "image/png", w.Header().Get("Content-Type"), msg)
	}

	assertImage := func(w *httptest.ResponseRecorder) {
		t.Helper()
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
		assert.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
		assert.Equal(t, pngBytes, w.Body.Bytes())
		assert.Empty(t, w.Result().Cookies())
	}

	grantW := callGetImage(customerA.Id, ticketA.Id, imageID, "action=grant", "", "")
	var grantEnvelope map[string]any
	require.NoError(t, common.Unmarshal(grantW.Body.Bytes(), &grantEnvelope), grantW.Body.String())
	require.Equal(t, true, grantEnvelope["success"], grantEnvelope)
	grantData := grantEnvelope["data"].(map[string]any)
	actionGrant, _ := grantData["grant"].(string)
	require.NotEmpty(t, actionGrant)
	_, hasGrantURL := grantData["url"]
	assert.False(t, hasGrantURL, "action=grant must return only the JSON credential")
	assert.NotContains(t, grantW.Body.String(), "?grant=")
	assert.NotContains(t, grantW.Body.String(), "grant=")
	assertGrantStaysOutOfCookieAndLink(t, grantW, actionGrant)

	assertDenied(callGetImage(customerA.Id, ticketA.Id, imageID, "", "", ""), "missing grant must be rejected")
	assertDenied(callGetImage(customerA.Id, ticketA.Id, imageID, "grant="+issuedGrant, "", ""), "query grant must not authorize a read")
	assertDenied(callGetImage(customerA.Id, ticketA.Id, imageID, "", "", issuedGrant), "cookie grant must not authorize a read")

	expiredGrant := workspaceTicketImageGrantAt(customerA.Id, ticketA.Id, imageID, time.Now().Add(-time.Minute).Unix())
	assertDenied(callGetImage(customerA.Id, ticketA.Id, imageID, "", expiredGrant, ""), "expired grant must be rejected")
	excessiveGrant := workspaceTicketImageGrantAt(customerA.Id, ticketA.Id, imageID, time.Now().Add(time.Hour).Unix())
	assertDenied(callGetImage(customerA.Id, ticketA.Id, imageID, "", excessiveGrant, ""), "excessive lifetime must be rejected")

	crossTicketGrant := workspaceTicketImageGrantAt(customerA.Id, ticketB.Id, imageID, time.Now().Add(time.Minute).Unix())
	assertDenied(callGetImage(customerA.Id, ticketA.Id, imageID, "", crossTicketGrant, ""), "cross-ticket grant must be rejected")
	crossImageGrant := workspaceTicketImageGrantAt(customerA.Id, ticketA.Id, imageID+999, time.Now().Add(time.Minute).Unix())
	assertDenied(callGetImage(customerA.Id, ticketA.Id, imageID, "", crossImageGrant, ""), "cross-image grant must be rejected")
	assertDenied(callGetImage(customerB.Id, ticketA.Id, imageID, "", issuedGrant, ""), "unauthorized user with stolen grant must be rejected")
	assertDenied(callGetImage(owner.Id, ticketA.Id, imageID, "", issuedGrant, ""), "grant bound to another user ID must be rejected")
	assertDenied(callGetImage(customerA.Id, ticketA.Id, imageID, "", issuedGrant+"00", ""), "tampered grant must be rejected")

	assertImage(callGetImage(customerA.Id, ticketA.Id, imageID, "", issuedGrant, ""))
	assertImage(callGetImage(customerA.Id, ticketA.Id, imageID, "grant=ignored", actionGrant, issuedGrant))

	staffGrantW := callGetImage(staff.Id, ticketA.Id, imageID, "action=grant", "", "")
	var staffGrantEnvelope map[string]any
	require.NoError(t, common.Unmarshal(staffGrantW.Body.Bytes(), &staffGrantEnvelope), staffGrantW.Body.String())
	require.Equal(t, true, staffGrantEnvelope["success"], staffGrantEnvelope)
	staffGrant, _ := staffGrantEnvelope["data"].(map[string]any)["grant"].(string)
	require.NotEmpty(t, staffGrant)
	assert.Empty(t, staffGrantW.Result().Cookies())
	assertImage(callGetImage(staff.Id, ticketA.Id, imageID, "", staffGrant, ""))

	require.NoError(t, db.Model(&model.AdtySiteStaff{}).Where("id = ?", staffRow.Id).Update("status", "disabled").Error)
	assertDenied(callGetImage(staff.Id, ticketA.Id, imageID, "", staffGrant, ""), "revoked staff must be denied even before short grant expires")

	var beforeFail int64
	require.NoError(t, db.Model(&model.AdtyTicketImage{}).Where("ticket_id = ?", ticketA.Id).Count(&beforeFail).Error)
	savedSecret := common.CryptoSecret
	t.Cleanup(func() { common.CryptoSecret = savedSecret })
	common.CryptoSecret = ""
	failW := httptest.NewRecorder()
	failC, _ := gin.CreateTestContext(failW)
	failC.Request = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/adty/tickets/%d/images", ticketA.Id), strings.NewReader(uploadBody))
	failC.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ticketA.Id)}}
	failC.Set("id", customerA.Id)
	PostAdtyTicketImage(failC)
	common.CryptoSecret = savedSecret
	var failEnvelope map[string]any
	require.NoError(t, common.Unmarshal(failW.Body.Bytes(), &failEnvelope), failW.Body.String())
	assert.Equal(t, false, failEnvelope["success"], "grant issuance failure must not be reported as success")
	assert.NotContains(t, failW.Body.String(), `"success":true`)
	assert.Empty(t, failW.Result().Cookies())
	var afterFail int64
	require.NoError(t, db.Model(&model.AdtyTicketImage{}).Where("ticket_id = ?", ticketA.Id).Count(&afterFail).Error)
	assert.Equal(t, beforeFail+1, afterFail, "the saved image must not be presented as a successful credential issue")
}

func assertGrantStaysOutOfCookieAndLink(t *testing.T, w *httptest.ResponseRecorder, grant string) {
	t.Helper()
	assert.Empty(t, w.Result().Cookies())
	assert.NotContains(t, w.Body.String(), "?grant=")
	assert.NotContains(t, w.Body.String(), "grant=")
	for key, values := range w.Header() {
		for _, value := range values {
			assert.NotContains(t, value, grant, key)
		}
	}
}
