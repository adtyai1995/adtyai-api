package billingexpr

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
)

// snapshotQuotaPerUnitDecimal is the financial authority for converting
// expression output into internal quota. Official USD books always enforce the
// fixed 1 USD = 500,000 scale (DEC-001).
func snapshotQuotaPerUnitDecimal(snap *BillingSnapshot) decimal.Decimal {
	if snap != nil && snap.QuotaPerUnit != 0 && snap.QuotaPerUnit != float64(common.FixedQuotaPerUnit) {
		common.SysError(common.LogText("ignoring non-standard snapshot quota_per_unit=%v; enforcing fixed %d", snap.QuotaPerUnit, common.FixedQuotaPerUnit))
	}
	return common.QuotaPerUnitDecimal()
}

func groupRatioDecimal(snap *BillingSnapshot) decimal.Decimal {
	if snap == nil {
		return decimal.NewFromInt(1)
	}
	if snap.GroupRatioDecimal != "" {
		if parsed, err := decimal.NewFromString(snap.GroupRatioDecimal); err == nil {
			return parsed
		}
	}
	return decimal.NewFromFloat(snap.GroupRatio)
}

// quotaConversionDecimal converts raw expression output to quota based on the
// expression version. Intermediate math stays in decimal; the caller rounds
// once when producing the billed integer amount.
func quotaConversionDecimal(exprOutput decimal.Decimal, snap *BillingSnapshot) decimal.Decimal {
	unit := snapshotQuotaPerUnitDecimal(snap)
	if snap != nil && snap.TaskUsageBilling {
		return exprOutput.Mul(unit)
	}
	return exprOutput.Div(decimal.NewFromInt(1_000_000)).Mul(unit)
}

// ComputeTieredQuota runs the Expr from a frozen BillingSnapshot against
// actual token counts and returns the settlement result.
func ComputeTieredQuota(snap *BillingSnapshot, params TokenParams) (TieredResult, error) {
	return ComputeTieredQuotaWithRequest(snap, params, RequestInput{})
}

func ComputeTieredQuotaWithRequest(snap *BillingSnapshot, params TokenParams, request RequestInput) (TieredResult, error) {
	if snap.TaskUsageBilling && UsesFixedPricingByHash(snap.ExprString, snap.ExprHash) {
		return TieredResult{}, fmt.Errorf("fixed pricing is not supported for task usage expressions")
	}
	if snap != nil && snap.AcceptedAt > 0 {
		request.AcceptedUnix = snap.AcceptedAt
		request.AcceptedAt = time.Unix(snap.AcceptedAt, 0).UTC()
	}
	if snap != nil && snap.BusinessTimezone != "" {
		request.BusinessTimezone = snap.BusinessTimezone
	}
	if snap != nil {
		if len(request.Headers) == 0 && len(snap.FrozenHeaders) > 0 {
			request.Headers = snap.FrozenHeaders
		}
		if len(request.Body) == 0 && len(request.FrozenParams) == 0 && len(snap.FrozenParams) > 0 {
			request.FrozenParams = snap.FrozenParams
		}
	}
	_, trace, err := RunExprByHashWithRequest(snap.ExprString, snap.ExprHash, params, request)
	if err != nil {
		return TieredResult{}, err
	}

	quotaBeforeGroupDec := quotaConversionDecimal(trace.CostDec, snap)
	afterGroup, clamp := common.QuotaFromDecimalChecked(quotaBeforeGroupDec.Mul(groupRatioDecimal(snap)))
	crossed := trace.MatchedTier != snap.EstimatedTier

	result := TieredResult{
		ImageCount:                trace.ImageCount,
		BillingUnit:               trace.BillingUnit,
		FixedPrice:                trace.FixedPrice,
		ActualQuotaBeforeGroup:    quotaBeforeGroupDec.InexactFloat64(),
		ActualQuotaBeforeGroupDec: quotaBeforeGroupDec,
		ActualQuotaAfterGroup:     afterGroup,
		MatchedTier:               trace.MatchedTier,
		RequestRules:              trace.RequestRules,
		CrossedTier:               crossed,
		Clamp:                     clamp,
	}
	if trace.BillingUnit == BillingUnitToken && UsedVarsByHash(snap.ExprString, snap.ExprHash)["img_cr"] {
		result.BillingTokens = &params
	}
	return result, nil
}
