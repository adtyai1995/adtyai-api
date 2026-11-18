package billingexpr

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

// RunExpr compiles (with cache) and executes an expression string.
// The environment exposes:
//   - p, c             — prompt / completion tokens (auto-excluding separately-priced sub-categories)
//   - len              — total input context length for tier conditions (never reduced by sub-category exclusion)
//   - cr, cc, cc1h, cc12h — cache read / creation / creation-1h / creation-12h tokens
//   - tier(name, value) — trace callback that records which tier matched
//   - max, min, abs, ceil, floor — standard math helpers
//
// Returns the resulting cost (before group ratio) and a TraceResult with
// side-channel info captured by tier() during execution. The float64 return
// is the inexact view of TraceResult.CostDec, which is the exact value
// handed to settlement.
func RunExpr(exprStr string, params TokenParams) (float64, TraceResult, error) {
	return RunExprWithRequest(exprStr, params, RequestInput{})
}

func RunExprWithRequest(exprStr string, params TokenParams, request RequestInput) (float64, TraceResult, error) {
	entry, err := compileEntryFromCacheByHash(exprStr, ExprHashString(exprStr))
	if err != nil {
		return 0, TraceResult{}, err
	}
	return runProgram(entry, params, request)
}

// RunExprByHash is like RunExpr but accepts a pre-computed hash for the cache
// lookup, avoiding a redundant SHA-256 computation when the caller already
// holds BillingSnapshot.ExprHash.
func RunExprByHash(exprStr, hash string, params TokenParams) (float64, TraceResult, error) {
	return RunExprByHashWithRequest(exprStr, hash, params, RequestInput{})
}

func RunExprByHashWithRequest(exprStr, hash string, params TokenParams, request RequestInput) (float64, TraceResult, error) {
	entry, err := compileEntryFromCacheByHash(exprStr, hash)
	if err != nil {
		return 0, TraceResult{}, err
	}
	return runProgram(entry, params, request)
}

func runProgram(entry *cachedEntry, params TokenParams, request RequestInput) (float64, TraceResult, error) {
	trace := TraceResult{
		BillingUnit:  BillingUnitToken,
		RequestRules: append([]RequestRuleTrace(nil), entry.requestRules...),
	}
	imageCount := 1
	if entry.usedVars["image_count"] {
		if request.ImageCount != nil {
			imageCount = *request.ImageCount
		}
		if imageCount < 1 || imageCount > dto.MaxImageN {
			return 0, trace, fmt.Errorf("image_count must be between 1 and %d", dto.MaxImageN)
		}
		trace.ImageCount = &imageCount
	}

	cost, err := evalBillingExpr(entry.tree, entry.body, &decimalEvalEnv{
		src:             entry.body,
		vars:            tokenParamsAsDecimals(params, imageCount),
		trace:           &trace,
		headers:         normalizeHeaders(request.Headers),
		body:            request.Body,
		usage:           request.Usage,
		now:             resolveEvalInstant(request),
		defaultTimezone: strings.TrimSpace(request.BusinessTimezone),
	})
	if err != nil {
		return 0, trace, err
	}
	trace.CostDec = cost
	trace.Cost = cost.InexactFloat64()
	return trace.Cost, trace, nil
}

func resolveEvalInstant(request RequestInput) time.Time {
	if !request.AcceptedAt.IsZero() {
		return request.AcceptedAt.UTC()
	}
	if request.AcceptedUnix > 0 {
		return time.Unix(request.AcceptedUnix, 0).UTC()
	}
	return time.Now().UTC()
}

func (env *decimalEvalEnv) timeInZone(tz string) time.Time {
	instant := time.Now().UTC()
	defaultTZ := ""
	if env != nil {
		if !env.now.IsZero() {
			instant = env.now
		}
		defaultTZ = env.defaultTimezone
	}
	return instantInZone(instant, tz, defaultTZ)
}

func instantInZone(instant time.Time, tz string, defaultTZ string) time.Time {
	if instant.IsZero() {
		instant = time.Now().UTC()
	}
	tz = strings.TrimSpace(tz)
	if tz == "" {
		tz = strings.TrimSpace(defaultTZ)
	}
	if tz == "" {
		return instant.UTC()
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return instant.UTC()
	}
	return instant.In(loc)
}

func timeInZone(tz string) time.Time {
	return instantInZone(time.Now().UTC(), tz, "")
}

func normalizeHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return map[string]string{}
	}
	normalized := make(map[string]string, len(headers))
	for key, value := range headers {
		k := strings.ToLower(strings.TrimSpace(key))
		v := strings.TrimSpace(value)
		if k == "" || v == "" {
			continue
		}
		normalized[k] = v
	}
	return normalized
}
