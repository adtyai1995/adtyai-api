package controller

import (
	"context"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"strings"
	"time"
)

func CheckAdtyDomainProvider(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		denyAdtyAdminAction(c)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
	defer cancel()
	if err := service.CheckAdtyDomainProvider(ctx, c.Param("provider")); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"readable": true, "write_verified": false})
}

func RetryAdtyDomainAutomation(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		denyAdtyAdminAction(c)
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	var domain model.AdtySiteDomain
	if err := model.DB.First(&domain, id).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if strings.TrimSpace(domain.AutomationState) == "" || domain.Status == model.AdtySiteDomainRemoved {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := model.DB.Model(&domain).Updates(map[string]any{"next_run_at": 0, "last_error": ""}).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"queued": true})
}
