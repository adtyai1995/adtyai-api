package controller

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func DeleteAdtyTelegramBinding(c *gin.Context) {
	purpose := model.AdtyTelegramPurposeUser
	if strings.Contains(c.FullPath(), "/site/") {
		if _, ok := adtyOwnerSiteOr403(c); !ok {
			return
		}
		purpose = model.AdtyTelegramPurposeAdmin
	}
	audit := prepareManageAudit(c, c.GetInt("id"), "telegram.unbind", map[string]any{"purpose": purpose})
	if err := service.RevokeAdtyTelegramBind(c.GetInt("id"), purpose, &audit); err != nil {
		common.ApiError(c, err)
		return
	}
	deliverCommittedAudits(c, []model.AuditLog{audit})
	common.ApiSuccess(c, gin.H{"purpose": purpose, "status": "revoked", "telegram_user_id": 0})
}
