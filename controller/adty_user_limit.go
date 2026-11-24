package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var (
	adtyUserLimitGetTargetUser = func(userID int) (*model.User, error) {
		return model.GetUserById(userID, false)
	}
	adtyUserLimitGetLimit         = model.GetAdtyUserLimit
	adtyUserLimitSaveLimit        = model.SaveAdtyUserLimit
	adtyUserLimitSaveLimitForUser = model.SaveAdtyUserLimitForActor
	adtyUserLimitRecordAudit      = func(c *gin.Context, targetID int, action string, after map[string]any) {
		if model.DB == nil {
			return
		}
		recordManageAuditFor(c, targetID, action, after)
	}
)

func adtyCheckUserLimitTargetScope(c *gin.Context, targetUserID int, requireTarget bool) bool {
	myRole := c.GetInt("role")
	actorID := c.GetInt("id")
	if myRole == 0 && actorID > 0 {
		if actor, err := adtyUserLimitGetTargetUser(actorID); err == nil && actor != nil {
			myRole = actor.Role
		}
	}
	targetUser, err := adtyUserLimitGetTargetUser(targetUserID)
	if err != nil || targetUser == nil {
		if !requireTarget && (myRole == common.RoleRootUser || (myRole == 0 && actorID == 0)) {
			return true
		}
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("User not found"))
		return false
	}
	if myRole != common.RoleRootUser {
		if myRole < common.RoleAdminUser || myRole <= targetUser.Role {
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to update user with same or higher role"))
			return false
		}
	}
	if requireTarget && isAccessTokenRequest(c) && targetUser.Role >= common.RoleAdminUser {
		denyAdtyAdminAction(c)
		return false
	}
	if hostSite := service.AdtySiteForRequest(c); hostSite != nil && hostSite.Id > 0 && myRole != common.RoleRootUser {
		if _, err := service.GetAdtyOwnerCustomer(hostSite, targetUserID); err != nil {
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
			return false
		}
	}
	return true
}

// GetAdtyUserLimits 返回一个用户的请求限制（任务 7.4，用户·查看）。-1 表示跟默认。
func GetAdtyUserLimits(c *gin.Context) {
	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil || userID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if !adtyCheckUserLimitTargetScope(c, userID, false) {
		return
	}
	limit, err := adtyUserLimitGetLimit(userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, limit)
}

// PutAdtyUserLimits 设一个用户的请求限制（任务 7.4，用户·编辑）。每项不小于 -1。
func PutAdtyUserLimits(c *gin.Context) {
	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil || userID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		RPM         int `json:"rpm"`
		TPM         int `json:"tpm"`
		Concurrency int `json:"concurrency"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil || body.RPM < -1 || body.TPM < -1 || body.Concurrency < -1 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if !adtyCheckUserLimitTargetScope(c, userID, true) {
		return
	}
	limit := model.AdtyUserLimit{UserId: userID, RPM: body.RPM, TPM: body.TPM, Concurrency: body.Concurrency}
	myRole := c.GetInt("role")
	if myRole == 0 && c.GetInt("id") > 0 {
		if actor, actorErr := adtyUserLimitGetTargetUser(c.GetInt("id")); actorErr == nil && actor != nil {
			myRole = actor.Role
		}
	}
	var saveErr error
	if model.DB != nil {
		saveErr = adtyUserLimitSaveLimitForUser(limit, myRole, isAccessTokenRequest(c))
	} else {
		saveErr = adtyUserLimitSaveLimit(limit)
	}
	if saveErr != nil {
		if errors.Is(saveErr, model.ErrAdminTargetSessionRequired) {
			denyAdtyAdminAction(c)
			return
		}
		if errors.Is(saveErr, model.ErrUserQuotaPermission) {
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to update user with same or higher role"))
			return
		}
		if errors.Is(saveErr, gorm.ErrRecordNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("User not found"))
			return
		}
		common.ApiError(c, saveErr)
		return
	}
	adtyUserLimitRecordAudit(c, userID, "adty.user.limits", map[string]any{"rpm": body.RPM, "tpm": body.TPM, "concurrency": body.Concurrency})
	common.ApiSuccess(c, limit)
}
