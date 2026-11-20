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
		if err := model.DB.Model(&model.User{}).Where("id = ? AND site_id = ?", userID, site.Id).Count(&count).Error; err != nil { common.ApiError(c, err); return }
		if count == 0 { common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Customer not found")); return }
	}
	page, offset, limit := adtyPage(c)
	items, total, err := service.ListAdtySitePricingHistory(site.Id, userID, offset, limit)
	if err != nil { common.ApiError(c, err); return }
	page.SetTotal(int(total))
	page.SetItems(items)
	common.ApiSuccess(c, page)
}
