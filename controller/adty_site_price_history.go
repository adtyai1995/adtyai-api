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
	if !ok {
		return
	}
	userID := 0
	if c.Param("id") != "" {
		var valid bool
		userID, valid = adtyPathId(c)
		if !valid {
			return
		}
		var count int64
		if err := model.DB.Model(&model.User{}).Where("id = ? AND site_id = ? AND staff_site_id = 0", userID, site.Id).Count(&count).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		if count == 0 {
			common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Customer not found"))
			return
		}
		hasTeamMemberTable, tableErr := model.AdtyTableExists(model.DB, &model.AdtyTeamMember{})
		if tableErr != nil {
			common.ApiError(c, tableErr)
			return
		}
		if hasTeamMemberTable {
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
	if err != nil {
		common.ApiError(c, err)
		return
	}
	type priceHistoryView struct {
		model.AdtyPriceRevision
		ActorUsername string `json:"actor_username,omitempty"`
		Username      string `json:"username,omitempty"`
	}
	lookupIDs := make([]int, 0, len(items)*2)
	for _, item := range items {
		if item.ActorId > 0 {
			lookupIDs = append(lookupIDs, item.ActorId)
		}
		if item.UserId > 0 {
			lookupIDs = append(lookupIDs, item.UserId)
		}
	}
	usernames := model.LookupUsernamesByIDs(model.DB, lookupIDs)
	views := make([]priceHistoryView, 0, len(items))
	for _, item := range items {
		views = append(views, priceHistoryView{
			AdtyPriceRevision: item,
			ActorUsername:     usernames[item.ActorId],
			Username:          usernames[item.UserId],
		})
	}
	page.SetTotal(int(total))
	page.SetItems(views)
	common.ApiSuccess(c, page)
}
