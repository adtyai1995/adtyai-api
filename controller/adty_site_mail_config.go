package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// GetAdtySiteMailConfig returns the site owner's outbound email configuration
// without exposing plaintext credentials.
func GetAdtySiteMailConfig(c *gin.Context) {
	if _, ok := adtyOwnerSiteOr403(c); !ok {
		return
	}
	cfg, err := service.GetAdtySiteMailConfig(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, cfg)
}

// PutAdtySiteMailConfig validates and persists the site owner's outbound email
// configuration (platform default, custom SMTP, or Resend API).
func PutAdtySiteMailConfig(c *gin.Context) {
	if _, ok := adtyOwnerSiteOr403(c); !ok {
		return
	}
	var body service.AdtySiteMailConfigUpdate
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	cfg, err := service.SaveAdtySiteMailConfig(c.Request.Context(), c.GetInt("id"), body)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, cfg)
}

// PostAdtySiteMailConfigTest sends a test email using the site's currently
// saved email configuration.
func PostAdtySiteMailConfigTest(c *gin.Context) {
	if _, ok := adtyOwnerSiteOr403(c); !ok {
		return
	}
	var body struct {
		To string `json:"to"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.TestAdtySiteMailConfig(c.Request.Context(), c.GetInt("id"), body.To); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"sent": true})
}
