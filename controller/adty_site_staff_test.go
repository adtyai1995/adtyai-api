package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdtySiteOwnerStaffOperationBindsActionTargetAndRequestDigest(t *testing.T) {
	var inputA, inputB service.AdtyStaffInput
	require.NoError(t, common.DecodeJson(strings.NewReader(`{"username":"staff_alice","password":"Password123!"}`), &inputA))
	require.NoError(t, common.DecodeJson(strings.NewReader(`{"username":"staff_bob","password":"Password123!"}`), &inputB))

	_, err := service.BindVerificationOperation(service.AdtySiteOwnerOperation(service.AdtySiteOwnerActionStaffCreate, 7))
	assert.Error(t, err, "staff mutation operation without request digest must be rejected by BindVerificationOperation")

	bindA, err := service.BindVerificationOperation(AdtySiteOwnerStaffOperation(service.AdtySiteOwnerActionStaffCreate, 7, inputA))
	require.NoError(t, err)
	bindAFromRaw, err := service.BindVerificationOperation(AdtySiteOwnerStaffOperation(service.AdtySiteOwnerActionStaffCreate, 7, `{"password":"Password123!","username":"staff_alice"}`))
	require.NoError(t, err)
	bindB, err := service.BindVerificationOperation(AdtySiteOwnerStaffOperation(service.AdtySiteOwnerActionStaffCreate, 7, inputB))
	require.NoError(t, err)
	bindDifferentTarget, err := service.BindVerificationOperation(AdtySiteOwnerStaffOperation(service.AdtySiteOwnerActionStaffCreate, 8, inputA))
	require.NoError(t, err)
	bindDifferentAction, err := service.BindVerificationOperation(AdtySiteOwnerStaffOperation(service.AdtySiteOwnerActionStaffUpdate, 7, inputA))
	require.NoError(t, err)

	assert.Equal(t, bindA.ContextHash, bindAFromRaw.ContextHash, "canonical digest must be stable across JSON key order")
	assert.NotEqual(t, bindA.ContextHash, bindB.ContextHash, "changing staff mutation payload must change proof context hash")
	assert.NotEqual(t, bindA.ContextHash, bindDifferentTarget.ContextHash, "changing target must change proof context hash")
	assert.NotEqual(t, bindA.ContextHash, bindDifferentAction.ContextHash, "changing action must change proof context hash")
}

func TestOwnerStaffMutationsRejectMissingOrTamperedDigestProof(t *testing.T) {
	gin.SetMode(gin.TestMode)

	origSiteResolver := adtyOwnerSiteResolver
	origStaffExists := adtyOwnerStaffExists
	origProofCheck := adtyOwnerStaffProofCheck
	origCreateFn := adtyOwnerStaffCreateFn
	origMutateFn := adtyOwnerStaffMutateFn
	t.Cleanup(func() {
		adtyOwnerSiteResolver = origSiteResolver
		adtyOwnerStaffExists = origStaffExists
		adtyOwnerStaffProofCheck = origProofCheck
		adtyOwnerStaffCreateFn = origCreateFn
		adtyOwnerStaffMutateFn = origMutateFn
	})

	adtyOwnerSiteResolver = func(c *gin.Context) (*model.AdtySite, bool) {
		return &model.AdtySite{Id: 12, OwnerUserId: 100}, true
	}
	adtyOwnerStaffExists = func(siteID, ownerUserID, staffID int) (bool, error) {
		return siteID == 12 && ownerUserID == 100 && staffID == 55, nil
	}

	issuedProofs := map[string]string{}
	adtyOwnerStaffProofCheck = func(c *gin.Context, expectedOperation service.VerificationOperation) bool {
		token := c.GetHeader("X-Security-Proof")
		boundOp, ok := issuedProofs[token]
		if !ok || boundOp != string(expectedOperation.Context) {
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Security verification required"))
			return false
		}
		delete(issuedProofs, token)
		return true
	}

	createdCount := 0
	mutatedCount := 0
	adtyOwnerStaffCreateFn = func(ownerUserID, siteID int, input service.AdtyStaffInput) (*service.AdtyStaffView, error) {
		createdCount++
		return &service.AdtyStaffView{AdtySiteStaff: model.AdtySiteStaff{Id: 55}}, nil
	}
	adtyOwnerStaffMutateFn = func(ownerUserID, siteID, staffID int, action string, input service.AdtyStaffInput) (*service.AdtyStaffView, error) {
		mutatedCount++
		return &service.AdtyStaffView{AdtySiteStaff: model.AdtySiteStaff{Id: staffID}}, nil
	}

	verifiedCreateBody := `{"username":"staff_one","password":"ValidPassword1!"}`
	tamperedCreateBody := `{"username":"staff_two","password":"ValidPassword1!"}`

	issuedProofs["proof-no-digest"] = string(service.AdtySiteOwnerOperation(service.AdtySiteOwnerActionStaffCreate, 12).Context)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("id", 100)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/adty/owner/staff", strings.NewReader(verifiedCreateBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Security-Proof", "proof-no-digest")
	PostAdtyOwnerStaff(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, 0, createdCount)

	issuedProofs["proof-create"] = string(AdtySiteOwnerStaffOperation(service.AdtySiteOwnerActionStaffCreate, 12, verifiedCreateBody).Context)
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Set("id", 100)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/adty/owner/staff", strings.NewReader(tamperedCreateBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Security-Proof", "proof-create")
	PostAdtyOwnerStaff(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, 0, createdCount)

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Set("id", 100)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/adty/owner/staff", strings.NewReader(verifiedCreateBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Security-Proof", "proof-create")
	PostAdtyOwnerStaff(c)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, createdCount)

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Set("id", 100)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/adty/owner/staff", strings.NewReader(verifiedCreateBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Security-Proof", "proof-create")
	PostAdtyOwnerStaff(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, 1, createdCount)

	verifiedUpdateBody := `{"username":"staff_one"}`
	tamperedUpdateBody := `{"username":"staff_escalated"}`
	issuedProofs["proof-update"] = string(AdtySiteOwnerStaffOperation(service.AdtySiteOwnerActionStaffUpdate, 55, verifiedUpdateBody).Context)

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Set("id", 100)
	c.Params = gin.Params{{Key: "id", Value: "55"}}
	c.Request = httptest.NewRequest(http.MethodPut, "/api/adty/owner/staff/55", strings.NewReader(tamperedUpdateBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Security-Proof", "proof-update")
	PutAdtyOwnerStaff(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, 0, mutatedCount)

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Set("id", 100)
	c.Params = gin.Params{{Key: "id", Value: "55"}}
	c.Request = httptest.NewRequest(http.MethodPut, "/api/adty/owner/staff/55", strings.NewReader(verifiedUpdateBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Security-Proof", "proof-update")
	PutAdtyOwnerStaff(c)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, mutatedCount)
}
