package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func adtyWithdrawalReceipt(c *gin.Context, id int64, err error) {
	c.Header("Cache-Control", "no-store")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"id": id})
}

func GetAdtySiteInviteWithdrawalByKey(c *gin.Context) {
	siteID, _ := strconv.Atoi(c.Query("site_id"))
	id, err := service.LookupSiteInviteWithdrawal(c.GetInt("id"), siteID, c.GetHeader("Idempotency-Key"))
	adtyWithdrawalReceipt(c, id, err)
}

func GetAdtySiteCommissionConversionByKey(c *gin.Context) {
	siteID, _ := strconv.Atoi(c.Query("site_id"))
	commissionID, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	id, err := service.LookupSiteCommissionConversion(c.GetInt("id"), siteID, commissionID, c.GetHeader("Idempotency-Key"))
	adtyWithdrawalReceipt(c, id, err)
}

func GetAdtySiteWithdrawalByKey(c *gin.Context) {
	site, ok := adtyOwnerAccountingSite(c)
	if !ok {
		return
	}
	if expected, err := strconv.Atoi(c.Query("site_id")); err != nil || expected != site.Id {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	id, err := service.LookupSiteOwnerWithdrawal(c.GetInt("id"), site.Id, c.GetHeader("Idempotency-Key"))
	adtyWithdrawalReceipt(c, id, err)
}
