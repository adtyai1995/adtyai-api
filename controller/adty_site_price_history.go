package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetAdtyOwnerPricingHistory(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok { return }
	userID := 0
	if c.Param("id") != "" {
		var valid bool
		userID, valid = adtyPathId(c)
		if !valid { return }
		var count int64
		if err := model.DB.Model(&model.User{}).Where("id = ? AND site_id = ? AND staff_site_id = 0", userID, site.Id).Count(&count).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		if count == 0 {
			common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Customer not found"))
			return
		}
		if model.DB.Migrator().HasTable(&model.AdtyTeamMember{}) {
			var empCount int64
			if err := model.DB.Model(&model.AdtyTeamMember{}).Where("user_id = ? AND independent_employee = ?", userID, true).Count(&empCount).Error; err != nil {
				common.ApiError(c, err)
				return
			}
			if empCount > 0 {
				common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Customer not found"))
				return
			}
		}
	}
	page, offset, limit := adtyPage(c)
	items, total, err := service.ListAdtySitePricingHistory(site.Id, userID, offset, limit)
	if err != nil { common.ApiError(c, err); return }
	page.SetTotal(int(total))
	page.SetItems(items)
	common.ApiSuccess(c, page)
}
