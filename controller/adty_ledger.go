package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// GetAdtyUserLedger 分页列出一个用户的余额流水（任务 7.2，用户·查看）：退款、人工调额、佣金转余额。
func GetAdtyUserLedger(c *gin.Context) {
	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil || userID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	myRole := c.GetInt("role")
	targetUser, err := model.GetUserByIdIncludingDeleted(userID, false)
	if err != nil || targetUser == nil {
		if myRole != common.RoleRootUser {
			common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("User not found"))
			return
		}
	} else if !canManageTargetRole(myRole, targetUser.Role) {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to access users of same or higher level"))
		return
	}
	pageInfo := common.GetPageQuery(c)
	rows, total, err := model.ListAdtyLedgers(userID, pageInfo)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(rows)
	common.ApiSuccess(c, pageInfo)
}
