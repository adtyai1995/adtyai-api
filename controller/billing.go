package controller

import (
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

func resolveBillingQuotaForToken(c *gin.Context) (remainQuota int, usedQuota int, token *model.Token, expiredTime int64, isTokenStat bool, err error) {
	tokenId := c.GetInt("token_id")
	token, err = model.GetTokenById(tokenId)
	if err != nil {
		return 0, 0, nil, 0, false, err
	}
	if token.OwnerSelfUseSiteId > 0 {
		return 0, 0, token, 0, false, common.NewMessage("Billing query is not available for this key")
	}
	expiredTime = token.ExpiredTime
	if common.GetDisplayTokenStatEnabled() || !token.AllowUserQuotaQuery {
		remainQuota, usedQuota = model.TokenQuotaForBilling(token)
		return remainQuota, usedQuota, token, expiredTime, true, nil
	}
	teamQuota, isTeamKey, teamErr := service.AdtyTeamSharedQuotaForToken(token.Id)
	if teamErr != nil {
		return 0, 0, token, expiredTime, false, teamErr
	}
	if isTeamKey {
		remainQuota = int(min(max(teamQuota, int64(0)), int64(math.MaxInt)))
		usedQuota = token.UsedQuota
		return remainQuota, usedQuota, token, expiredTime, false, nil
	}
	userId := c.GetInt("id")
	remainQuota, usedQuota, err = model.UserQuotaForBilling(userId)
	return remainQuota, usedQuota, token, expiredTime, false, err
}

func GetSubscription(c *gin.Context) {
	remainQuota, usedQuota, token, expiredTime, isTokenStat, err := resolveBillingQuotaForToken(c)
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
	if isTokenStat && token != nil && token.UnlimitedQuota {
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
	_, quota, _, _, _, err := resolveBillingQuotaForToken(c)
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
