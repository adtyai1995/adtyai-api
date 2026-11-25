package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

func GetSubscription(c *gin.Context) {
	var remainQuota int
	var usedQuota int
	var err error
	var token *model.Token
	var expiredTime int64
	if common.GetDisplayTokenStatEnabled() {
		tokenId := c.GetInt("token_id")
		token, err = model.GetTokenById(tokenId)
		if err != nil {
			common.SysError(common.LogText("GetSubscription failed to load token %d: %s", tokenId, err.Error()))
			openAIError := types.OpenAIError{
				Message: "Failed to retrieve billing subscription",
				Type:    "upstream_error",
			}
			c.JSON(200, gin.H{
				"error": openAIError,
			})
			return
		}
		expiredTime = token.ExpiredTime
		remainQuota, usedQuota = model.TokenQuotaForBilling(token)
	} else {
		userId := c.GetInt("id")
		remainQuota, usedQuota, err = model.UserQuotaForBilling(userId)
	}
	if expiredTime <= 0 {
		expiredTime = 0
	}
	if err != nil {
		common.SysError(common.LogText("GetSubscription failed to load quota: %s", err.Error()))
		openAIError := types.OpenAIError{
			Message: "Failed to retrieve billing subscription",
			Type:    "upstream_error",
		}
		c.JSON(200, gin.H{
			"error": openAIError,
		})
		return
	}
	quota := remainQuota + usedQuota
	amount := float64(quota)
	// OpenAI 兼容接口中的 *_USD 字段含义保持“额度单位”对应值：
	// 我们将其解释为以“站点展示类型”为准：
	// - USD: 直接除以 QuotaPerUnitDecimal
	// - CNY: 先转 USD 再乘汇率
	// - TOKENS: 直接使用 tokens 数量
	switch operation_setting.GetQuotaDisplayType() {
	case operation_setting.QuotaDisplayTypeCNY:
		amount = decimal.NewFromInt(int64(quota)).Div(common.QuotaPerUnitDecimal()).Mul(decimal.NewFromFloat(operation_setting.GetUSDExchangeRate())).InexactFloat64()
	case operation_setting.QuotaDisplayTypeTokens:
		// amount 保持 tokens 数值
	default:
		amount = decimal.NewFromInt(int64(quota)).Div(common.QuotaPerUnitDecimal()).InexactFloat64()
	}
	if token != nil && token.UnlimitedQuota {
		amount = 100000000
	}
	subscription := OpenAISubscriptionResponse{
		Object:             "billing_subscription",
		HasPaymentMethod:   true,
		SoftLimitUSD:       amount,
		HardLimitUSD:       amount,
		SystemHardLimitUSD: amount,
		AccessUntil:        expiredTime,
	}
	c.JSON(200, subscription)
	return
}

func GetUsage(c *gin.Context) {
	var quota int
	var err error
	var token *model.Token
	if common.GetDisplayTokenStatEnabled() {
		tokenId := c.GetInt("token_id")
		token, err = model.GetTokenById(tokenId)
		if err != nil {
			common.SysError(common.LogText("GetUsage failed to load token %d: %s", tokenId, err.Error()))
			openAIError := types.OpenAIError{
				Message: "Failed to retrieve billing usage",
				Type:    "adtyai_error",
			}
			c.JSON(200, gin.H{
				"error": openAIError,
			})
			return
		}
		_, quota = model.TokenQuotaForBilling(token)
	} else {
		userId := c.GetInt("id")
		_, quota, err = model.UserQuotaForBilling(userId)
	}
	if err != nil {
		common.SysError(common.LogText("GetUsage failed to load quota: %s", err.Error()))
		openAIError := types.OpenAIError{
			Message: "Failed to retrieve billing usage",
			Type:    "adtyai_error",
		}
		c.JSON(200, gin.H{
			"error": openAIError,
		})
		return
	}
	amount := float64(quota)
	switch operation_setting.GetQuotaDisplayType() {
	case operation_setting.QuotaDisplayTypeCNY:
		amount = decimal.NewFromInt(int64(quota)).Div(common.QuotaPerUnitDecimal()).Mul(decimal.NewFromFloat(operation_setting.GetUSDExchangeRate())).InexactFloat64()
	case operation_setting.QuotaDisplayTypeTokens:
		// tokens 保持原值
	default:
		amount = decimal.NewFromInt(int64(quota)).Div(common.QuotaPerUnitDecimal()).InexactFloat64()
	}
	usage := OpenAIUsageResponse{
		Object:     "list",
		TotalUsage: amount * 100,
	}
	c.JSON(200, usage)
	return
}
