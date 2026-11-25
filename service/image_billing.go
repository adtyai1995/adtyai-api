package service

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// PrepareImageBillingForRequest reserves the effective outbound image quantity
// before each attempt, including channel retries and parameter overrides. The
// client request body stays frozen; only the independent quantity is refreshed.
func PrepareImageBillingForRequest(c *gin.Context, info *relaycommon.RelayInfo, count int) *types.NewAPIError {
	if count < 1 || count > dto.MaxImageN {
		return types.NewErrorWithStatusCode(fmt.Errorf("image_count must be an integer between 1 and %d", dto.MaxImageN), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	info.ImageRequestCount = count
	var quota int
	var err error
	if snap := info.TieredBillingSnapshot; snap != nil && snap.BillingMode == "tiered_expr" {
		// Token-only image expressions must not acquire a quantity multiplier.
		if snap.EstimatedImageCount == nil {
			return nil
		}
		request := billingexpr.RequestInput{}
		if info.BillingRequestInput != nil {
			request = *info.BillingRequestInput
		}
		if snap.AcceptedAt > 0 {
			request.AcceptedAt = time.Unix(snap.AcceptedAt, 0).UTC()
			request.AcceptedUnix = snap.AcceptedAt
			request.BusinessTimezone = snap.BusinessTimezone
		}
		request.ImageCount = &count
		_, trace, runErr := billingexpr.RunExprByHashWithRequest(snap.ExprString, snap.ExprHash, billingexpr.TokenParams{
			P: float64(snap.EstimatedPromptTokens), C: float64(snap.EstimatedCompletionTokens), Len: float64(snap.EstimatedPromptTokens),
		}, request)
		if runErr != nil {
			return types.NewErrorWithStatusCode(runErr, types.ErrorCodeModelPriceError, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		unitDec := common.QuotaPerUnitDecimal()
		if snap.QuotaPerUnit != 0 && snap.QuotaPerUnit != float64(common.FixedQuotaPerUnit) {
			unitDec = decimal.NewFromFloat(snap.QuotaPerUnit)
		}
		beforeGroupDec := trace.CostDec.Div(decimal.NewFromInt(1_000_000)).Mul(unitDec)
		if trace.BillingUnit != billingexpr.BillingUnitRequest && snap.PreConsumeMultiplier != 0 {
			beforeGroupDec = beforeGroupDec.Mul(decimal.NewFromFloat(snap.PreConsumeMultiplier))
		}
		groupRatioDec := info.PriceData.GroupRatioInfo.BilledGroupRatio()
		quota, err = common.QuotaFromDecimalStrict(beforeGroupDec.Mul(groupRatioDec))
		if err == nil {
			snap.EstimatedImageCount = trace.ImageCount
			snap.EstimatedQuotaBeforeGroup = beforeGroupDec.InexactFloat64()
			snap.EstimatedQuotaBeforeGroupDecimal = beforeGroupDec.String()
			snap.EstimatedQuotaAfterGroup = quota
			snap.EstimatedTier = trace.MatchedTier
			snap.EstimatedBillingUnit = trace.BillingUnit
			snap.EstimatedFixedPrice = trace.FixedPrice
			snap.GroupRatio = info.PriceData.GroupRatioInfo.GroupRatio
			snap.GroupRatioDecimal = groupRatioDec.String()
		}
	} else {
		quantity := 1
		if info.PriceData.UsePrice {
			quantity = count
		}
		// Overwrite the per-attempt ratio so a failed attempt cannot leak its
		// quantity into another channel.
		info.PriceData.AddOtherRatio("n", float64(quantity))
		baseDec := decimal.NewFromFloat(info.ImageQuotaBeforeGroup)
		if info.PriceData.UsePrice {
			baseDec = info.PriceData.BilledModelPrice().Mul(common.QuotaPerUnitDecimal())
		}
		totalDec := info.PriceData.ApplyOtherRatiosToDecimal(baseDec.Mul(info.PriceData.GroupRatioInfo.BilledGroupRatio()))
		quota, err = common.QuotaFromDecimalStrict(totalDec)
	}
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeModelPriceError, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	info.PriceData.QuotaToPreConsume = quota
	if quota == 0 && info.Billing == nil {
		return nil
	}
	info.PriceData.FreeModel = false
	if info.Billing == nil {
		return PreConsumeBilling(c, quota, info)
	}
	if err := info.Billing.Reserve(quota); err != nil {
		var apiErr *types.NewAPIError
		if errors.As(err, &apiErr) {
			return apiErr
		}
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
	info.FinalPreConsumedQuota = info.Billing.GetPreConsumedQuota()
	return nil
}
