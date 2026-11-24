package controller

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetModelPricingConfig(c *gin.Context) {
	snapshot, err := model.GetModelPricingSnapshot(c.QueryArray("model"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, snapshot)
}

func GetModelPricingIssues(c *gin.Context) {
	issues, err := model.GetModelPricingIssues("")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, issues)
}

func PreviewModelPricingConversion(c *gin.Context) {
	markIdempotentReplayNoAudit(c)
	var request struct {
		ModelName string              `json:"model_name"`
		Pricing   model.PricingValues `json:"pricing"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	preview, err := model.PreviewModelPricingConversion(request.ModelName, request.Pricing)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, preview)
}

func PreviewModelPricing(c *gin.Context) {
	markIdempotentReplayNoAudit(c)
	var request struct {
		ModelName string              `json:"model_name"`
		Pricing   model.PricingValues `json:"pricing"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	preview, err := model.PreviewModelPricing(request.ModelName, request.Pricing)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, model.ModelPricingDescription{
		Effective:      preview,
		CacheWriteMode: model.ResolveCacheWriteMode(request.ModelName, request.Pricing),
		BillingDetails: model.ResolveLegacyBillingDetails(request.ModelName, preview, request.Pricing),
	})
}

func UpdateModelPricingConfig(c *gin.Context) {
	var request struct {
		Changes []model.ModelPricingChange `json:"changes"`
		Partial bool                       `json:"partial"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	partialParam := strings.TrimSpace(c.Query("partial"))
	partial := request.Partial || strings.EqualFold(partialParam, "true") || partialParam == "1"
	if partial {
		if len(request.Changes) == 0 {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Select model pricing changes before saving"))
			return
		}
		updated, failed, err := model.UpdateModelPricingPartial(request.Changes)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if len(updated) > 0 {
			recordManageAudit(c, "model.pricing.update", map[string]any{
				"models":        updated,
				"partial":       true,
				"updated_count": len(updated),
				"failed_count":  len(failed),
			})
		}
		common.ApiSuccess(c, gin.H{
			"updated_models": updated,
			"failed_models":  failed,
			"updated_count":  len(updated),
			"failed_count":   len(failed),
		})
		return
	}
	if err := model.UpdateModelPricing(request.Changes); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, model.ErrModelPricingConflict) {
			status = http.StatusConflict
		}
		common.ApiErrorStatus(c, status, err)
		return
	}
	names := make([]string, 0, len(request.Changes))
	for _, change := range request.Changes {
		names = append(names, change.ModelName)
	}
	recordManageAudit(c, "model.pricing.update", map[string]any{"models": names})
	common.ApiSuccess(c, gin.H{"updated_models": names})
}
