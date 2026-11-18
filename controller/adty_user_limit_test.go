package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPutAdtyUserLimitsEnforcesTargetRoleHierarchy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	users := map[int]*model.User{
		1:  {Id: 1, Username: "root", Role: common.RoleRootUser},
		2:  {Id: 2, Username: "admin-a", Role: common.RoleAdminUser},
		3:  {Id: 3, Username: "admin-b", Role: common.RoleAdminUser},
		10: {Id: 10, Username: "customer", Role: common.RoleCommonUser},
	}
	saved := map[int]model.AdtyUserLimit{}

	origGetTarget := adtyUserLimitGetTargetUser
	origSaveLimit := adtyUserLimitSaveLimit
	origGetLimit := adtyUserLimitGetLimit
	origAudit := adtyUserLimitRecordAudit
	t.Cleanup(func() {
		adtyUserLimitGetTargetUser = origGetTarget
		adtyUserLimitSaveLimit = origSaveLimit
		adtyUserLimitGetLimit = origGetLimit
		adtyUserLimitRecordAudit = origAudit
	})

	adtyUserLimitGetTargetUser = func(userID int) (*model.User, error) {
		u, ok := users[userID]
		if !ok {
			return nil, errors.New("record not found")
		}
		return u, nil
	}
	adtyUserLimitSaveLimit = func(limit model.AdtyUserLimit) error {
		saved[limit.UserId] = limit
		return nil
	}
	adtyUserLimitGetLimit = func(userID int) (model.AdtyUserLimit, error) {
		if limit, ok := saved[userID]; ok {
			return limit, nil
		}
		return model.AdtyUserLimit{UserId: userID, RPM: -1, TPM: -1, Concurrency: -1}, nil
	}
	adtyUserLimitRecordAudit = func(c *gin.Context, targetID int, action string, after map[string]any) {}

	performPut := func(actorID, actorRole int, targetID string, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", actorID)
		c.Set("role", actorRole)
		c.Params = gin.Params{{Key: "id", Value: targetID}}
		c.Request = httptest.NewRequest(http.MethodPut, "/api/user/"+targetID+"/limits", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		PutAdtyUserLimits(c)
		return w
	}

	w := performPut(2, common.RoleAdminUser, "1", `{"rpm":10,"tpm":1000,"concurrency":2}`)
	assert.Equal(t, http.StatusForbidden, w.Code)
	_, exists := saved[1]
	assert.False(t, exists, "Root user limits must not be modified by ordinary admin")

	w = performPut(2, common.RoleAdminUser, "3", `{"rpm":20,"tpm":2000,"concurrency":4}`)
	assert.Equal(t, http.StatusForbidden, w.Code)
	_, exists = saved[3]
	assert.False(t, exists, "Peer admin user limits must not be modified by ordinary admin")

	w = performPut(2, common.RoleAdminUser, "2", `{"rpm":30,"tpm":3000,"concurrency":5}`)
	assert.Equal(t, http.StatusForbidden, w.Code)
	_, exists = saved[2]
	assert.False(t, exists, "Self admin user limits must not be modified by ordinary admin")

	w = performPut(2, common.RoleAdminUser, "999", `{"rpm":10,"tpm":1000,"concurrency":2}`)
	assert.Equal(t, http.StatusNotFound, w.Code)
	_, exists = saved[999]
	assert.False(t, exists)

	w = performPut(2, common.RoleAdminUser, "10", `{"rpm":60,"tpm":60000,"concurrency":5}`)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, model.AdtyUserLimit{UserId: 10, RPM: 60, TPM: 60000, Concurrency: 5}, saved[10])

	w = performPut(1, common.RoleRootUser, "3", `{"rpm":120,"tpm":120000,"concurrency":10}`)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, model.AdtyUserLimit{UserId: 3, RPM: 120, TPM: 120000, Concurrency: 10}, saved[3])
}

func TestGetAdtyUserLimitsRejectsHigherOrEqualRoleForOrdinaryAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)

	users := map[int]*model.User{
		1:  {Id: 1, Username: "root", Role: common.RoleRootUser},
		2:  {Id: 2, Username: "admin-a", Role: common.RoleAdminUser},
		3:  {Id: 3, Username: "admin-b", Role: common.RoleAdminUser},
		10: {Id: 10, Username: "customer", Role: common.RoleCommonUser},
	}

	origGetTarget := adtyUserLimitGetTargetUser
	origGetLimit := adtyUserLimitGetLimit
	t.Cleanup(func() {
		adtyUserLimitGetTargetUser = origGetTarget
		adtyUserLimitGetLimit = origGetLimit
	})

	adtyUserLimitGetTargetUser = func(userID int) (*model.User, error) {
		u, ok := users[userID]
		if !ok {
			return nil, errors.New("record not found")
		}
		return u, nil
	}
	adtyUserLimitGetLimit = func(userID int) (model.AdtyUserLimit, error) {
		return model.AdtyUserLimit{UserId: userID, RPM: 50, TPM: 5000, Concurrency: 3}, nil
	}

	performGet := func(actorID, actorRole int, targetID string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", actorID)
		c.Set("role", actorRole)
		c.Params = gin.Params{{Key: "id", Value: targetID}}
		c.Request = httptest.NewRequest(http.MethodGet, "/api/user/"+targetID+"/limits", nil)
		GetAdtyUserLimits(c)
		return w
	}

	assert.Equal(t, http.StatusForbidden, performGet(2, common.RoleAdminUser, "1").Code)
	assert.Equal(t, http.StatusForbidden, performGet(2, common.RoleAdminUser, "3").Code)
	assert.Equal(t, http.StatusOK, performGet(2, common.RoleAdminUser, "10").Code)
	assert.Equal(t, http.StatusOK, performGet(1, common.RoleRootUser, "1").Code)
	assert.Equal(t, http.StatusOK, performGet(1, common.RoleRootUser, "3").Code)
}
