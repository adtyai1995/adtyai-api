package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetAdtyBusinessTimezone(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	view, err := service.LoadAdtyBusinessTimezone()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func PutAdtyBusinessTimezone(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	var body struct {
		Timezone string `json:"timezone"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.SaveAdtyBusinessTimezone(body.Timezone); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"saved": true})
}

func GetAdtyOwnerSettlementSheets(c *gin.Context) {
	site, ok := adtyOwnerAccountingSite(c)
	if !ok {
		return
	}
	if err := service.ReviewAdtyPooledSiteFinance(model.DB, site.Id); err != nil {
		common.ApiError(c, err)
		return
	}
	serveSettlementSheets(c, site.Id)
}

func GetAdtyAdminSettlementSheets(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	siteID := 0
	if raw := c.Query("site_id"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		siteID = id
	}
	serveSettlementSheets(c, siteID)
}

func serveSettlementSheets(c *gin.Context, siteID int) {
	if raw, ok := c.GetQuery("sheet_id"); ok {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		row, err := service.GetSettlementSheet(siteID, id)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		common.ApiSuccess(c, row)
		return
	}
	if c.Query("p") != "" || c.Query("business_day") != "" || c.Query("page_size") != "" {
		page := common.GetPageQuery(c)
		if err := service.PageSettlementSheets(siteID, c.Query("business_day"), page); err != nil {
			common.ApiError(c, err)
			return
		}
		common.ApiSuccess(c, page)
		return
	}
	// Earlier API clients expect an array when pagination is omitted.
	if siteID > 0 {
		rows, err := service.ListAdtySiteSettlementSheets(siteID)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		common.ApiSuccess(c, rows)
		return
	}
	rows, err := service.ListAllAdtySiteSettlementSheets()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}
