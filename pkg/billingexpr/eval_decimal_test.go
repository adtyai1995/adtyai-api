package billingexpr

import (
	"testing"

	"github.com/expr-lang/expr/parser"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type testEmbeddedMeta struct {
	Promoted string `json:"promoted"`
}

type testPricingStruct struct {
	testEmbeddedMeta
	Name        string          `expr:"model_name" json:"json_name"`
	TierFactor  decimal.Decimal `json:"tier_factor"`
	FloatRate   float64         `json:"float_rate"`
	IntRate     int             `json:"int_rate"`
	Hidden      string          `expr:"-"`
	secret      string
	DangerousFn func() string
}

func (t testPricingStruct) SecretMethod() string {
	return "must-never-be-called"
}

func (t *testPricingStruct) PtrMethod() int {
	return 999
}

func evalParsedForTest(t *testing.T, src string, env *decimalEvalEnv) (any, error) {
	t.Helper()
	tree, err := parser.Parse(src)
	require.NoError(t, err)
	if env == nil {
		env = &decimalEvalEnv{}
	}
	env.src = src
	if env.trace == nil {
		env.trace = &TraceResult{}
	}
	return evalNode(tree.Node, env)
}

func TestEvalDecimalMemberAccessAndOptionalChaining(t *testing.T) {
	t.Run("map dot and bracket access with missing key returning nil", func(t *testing.T) {
		env := &decimalEvalEnv{
			extras: map[string]any{
				"meta": map[string]any{
					"foo": "bar",
					"num": 1.25,
				},
				"extra": map[string]any{
					"x": 42,
				},
				"decMap": map[string]decimal.Decimal{
					"rate": decimal.RequireFromString("0.1"),
				},
				"floatMap": map[string]float64{
					"rate": 0.2,
				},
				"intMap": map[string]int{
					"count": 3,
				},
				"numKeyMap": map[int]string{
					1: "one",
				},
			},
		}

		v, err := evalParsedForTest(t, `meta.foo`, env)
		require.NoError(t, err)
		require.Equal(t, "bar", v)

		v, err = evalParsedForTest(t, `extra["x"]`, env)
		require.NoError(t, err)
		require.Equal(t, decimal.NewFromInt(42), v)

		v, err = evalParsedForTest(t, `meta.missing == nil`, env)
		require.NoError(t, err)
		require.Equal(t, true, v)

		v, err = evalParsedForTest(t, `decMap.rate + floatMap.rate`, env)
		require.NoError(t, err)
		d, ok := v.(decimal.Decimal)
		require.True(t, ok)
		require.True(t, d.Equal(decimal.RequireFromString("0.3")), "got %s", d.String())

		v, err = evalParsedForTest(t, `intMap["count"] * meta.num`, env)
		require.NoError(t, err)
		d, ok = v.(decimal.Decimal)
		require.True(t, ok)
		require.True(t, d.Equal(decimal.RequireFromString("3.75")), "got %s", d.String())

		v, err = evalParsedForTest(t, `numKeyMap[1]`, env)
		require.NoError(t, err)
		require.Equal(t, "one", v)
	})

	t.Run("optional chaining and nil coalescing", func(t *testing.T) {
		envNil := &decimalEvalEnv{
			extras: map[string]any{
				"extra": nil,
			},
		}
		v, err := evalParsedForTest(t, `extra?.nested?.val ?? 0`, envNil)
		require.NoError(t, err)
		require.True(t, v.(decimal.Decimal).IsZero())

		envMidNil := &decimalEvalEnv{
			extras: map[string]any{
				"extra": map[string]any{
					"nested": nil,
				},
			},
		}
		v, err = evalParsedForTest(t, `extra?.nested?.val ?? 0`, envMidNil)
		require.NoError(t, err)
		require.True(t, v.(decimal.Decimal).IsZero())

		envChainNonOptAfterOpt := &decimalEvalEnv{
			extras: map[string]any{
				"extra": nil,
			},
		}
		v, err = evalParsedForTest(t, `extra?.nested.val ?? 7`, envChainNonOptAfterOpt)
		require.NoError(t, err)
		require.Equal(t, decimal.NewFromInt(7), v)

		envPresent := &decimalEvalEnv{
			extras: map[string]any{
				"extra": map[string]any{
					"nested": map[string]any{
						"val": 2.5,
					},
				},
			},
		}
		v, err = evalParsedForTest(t, `extra?.nested?.val ?? 0`, envPresent)
		require.NoError(t, err)
		require.True(t, v.(decimal.Decimal).Equal(decimal.RequireFromString("2.5")))

		// Non-nil zero value should NOT fall through to RHS of ??
		envZero := &decimalEvalEnv{
			extras: map[string]any{
				"extra": map[string]any{
					"nested": map[string]any{
						"val": 0,
					},
				},
			},
		}
		v, err = evalParsedForTest(t, `extra?.nested?.val ?? 9`, envZero)
		require.NoError(t, err)
		require.True(t, v.(decimal.Decimal).IsZero())
	})

	t.Run("slice array indexing and slicing", func(t *testing.T) {
		env := &decimalEvalEnv{
			extras: map[string]any{
				"tags":  []string{"alpha", "beta", "gamma"},
				"items": []any{10, 20, 30},
				"arr":   [3]int{7, 8, 9},
				"label": "abcdef",
			},
		}

		v, err := evalParsedForTest(t, `tags[0]`, env)
		require.NoError(t, err)
		require.Equal(t, "alpha", v)

		v, err = evalParsedForTest(t, `tags[-1]`, env)
		require.NoError(t, err)
		require.Equal(t, "gamma", v)

		v, err = evalParsedForTest(t, `arr[1]`, env)
		require.NoError(t, err)
		require.Equal(t, decimal.NewFromInt(8), v)

		v, err = evalParsedForTest(t, `len(items)`, env)
		require.NoError(t, err)
		require.Equal(t, decimal.NewFromInt(3), v)

		v, err = evalParsedForTest(t, `label[1:4]`, env)
		require.NoError(t, err)
		require.Equal(t, "bcd", v)

		v, err = evalParsedForTest(t, `len(tags[1:])`, env)
		require.NoError(t, err)
		require.Equal(t, decimal.NewFromInt(2), v)

		_, err = evalParsedForTest(t, `tags[99]`, env)
		require.Error(t, err)
	})
}

func TestEvalDecimalOperatorsAndBuiltins(t *testing.T) {
	env := &decimalEvalEnv{
		extras: map[string]any{
			"model": "gpt-6.1-sol",
			"items": []any{"a", "b", "c"},
			"nums":  []float64{0.1, 0.2, 0.3},
		},
	}

	v, err := evalParsedForTest(t, `model in ["gpt-6.1-sol", "b"]`, env)
	require.NoError(t, err)
	require.Equal(t, true, v)

	v, err = evalParsedForTest(t, `model not in ["a", "b"]`, env)
	require.NoError(t, err)
	require.Equal(t, true, v)

	v, err = evalParsedForTest(t, `"x" in {x: 1, y: 2}`, env)
	require.NoError(t, err)
	require.Equal(t, true, v)

	v, err = evalParsedForTest(t, `({a: 1, b: 2})["b"]`, env)
	require.NoError(t, err)
	require.Equal(t, decimal.NewFromInt(2), v)

	v, err = evalParsedForTest(t, `model contains "6.1"`, env)
	require.NoError(t, err)
	require.Equal(t, true, v)

	v, err = evalParsedForTest(t, `model startsWith "gpt-"`, env)
	require.NoError(t, err)
	require.Equal(t, true, v)

	v, err = evalParsedForTest(t, `model endsWith "-sol"`, env)
	require.NoError(t, err)
	require.Equal(t, true, v)

	v, err = evalParsedForTest(t, `model matches "^gpt-[0-9]+\\.[0-9]+"`, env)
	require.NoError(t, err)
	require.Equal(t, true, v)

	v, err = evalParsedForTest(t, `lower(upper(trim("  AbC  ")))`, env)
	require.NoError(t, err)
	require.Equal(t, "abc", v)

	v, err = evalParsedForTest(t, `int(3.9) + float("0.1") + float("0.2")`, env)
	require.NoError(t, err)
	require.True(t, v.(decimal.Decimal).Equal(decimal.RequireFromString("3.3")))

	v, err = evalParsedForTest(t, `string(round(1.235, 2))`, env)
	require.NoError(t, err)
	require.Equal(t, "1.24", v)

	v, err = evalParsedForTest(t, `min(nums) + max(nums)`, env)
	require.NoError(t, err)
	require.True(t, v.(decimal.Decimal).Equal(decimal.RequireFromString("0.4")))

	v, err = evalParsedForTest(t, `abs(-2.5) + ceil(1.1) + floor(1.9)`, env)
	require.NoError(t, err)
	require.True(t, v.(decimal.Decimal).Equal(decimal.RequireFromString("5.5")))
}

func TestEvalDecimalStructSafetyAndPrecision(t *testing.T) {
	item := &testPricingStruct{
		testEmbeddedMeta: testEmbeddedMeta{Promoted: "ok"},
		Name:             "claude-sonnet",
		TierFactor:       decimal.RequireFromString("0.1"),
		FloatRate:        0.2,
		IntRate:          3,
		Hidden:           "nope",
		secret:           "private",
		DangerousFn:      func() string { return "boom" },
	}
	env := &decimalEvalEnv{
		vars: map[string]decimal.Decimal{
			"p": decimal.NewFromInt(1000),
		},
		extras: map[string]any{
			"cfg":    item,
			"nilCfg": (*testPricingStruct)(nil),
		},
	}

	// expr tag lookup
	v, err := evalParsedForTest(t, `cfg.model_name`, env)
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet", v)

	// json tag lookup & decimal precision: 0.1 + 0.2 == 0.3 exactly
	v, err = evalParsedForTest(t, `(cfg.tier_factor + cfg.float_rate) * p`, env)
	require.NoError(t, err)
	require.True(t, v.(decimal.Decimal).Equal(decimal.RequireFromString("300")), "got %v", v)

	// promoted embedded field lookup
	v, err = evalParsedForTest(t, `cfg.promoted`, env)
	require.NoError(t, err)
	require.Equal(t, "ok", v)

	// nil struct pointer optional access
	v, err = evalParsedForTest(t, `nilCfg?.tier_factor ?? 1.5`, env)
	require.NoError(t, err)
	require.True(t, v.(decimal.Decimal).Equal(decimal.RequireFromString("1.5")))

	// Safety: MUST reject method calls, unexported fields, expr:"-" fields, and func fields.
	_, err = evalParsedForTest(t, `cfg.SecretMethod()`, env)
	require.Error(t, err)

	_, err = evalParsedForTest(t, `cfg.PtrMethod()`, env)
	require.Error(t, err)

	_, err = evalParsedForTest(t, `cfg.SecretMethod`, env)
	require.Error(t, err)

	_, err = evalParsedForTest(t, `cfg.secret`, env)
	require.Error(t, err)

	_, err = evalParsedForTest(t, `cfg.Hidden`, env)
	require.Error(t, err)

	_, err = evalParsedForTest(t, `cfg.DangerousFn`, env)
	require.Error(t, err)
}

func TestRunExprWithRequestMemberNodeAndParamAccess(t *testing.T) {
	exprStr := `(param("stream_options")?.include_usage ?? false) ? tier("stream", p * 2 + c * 4) : tier("base", p * 1 + c * 2)`

	cost, trace, err := RunExprWithRequest(exprStr, TokenParams{P: 1000, C: 500}, RequestInput{
		Body: []byte(`{"stream_options":{"include_usage":true},"messages":[{"role":"user"}]}`),
	})
	require.NoError(t, err)
	require.Equal(t, "stream", trace.MatchedTier)
	require.True(t, trace.CostDec.Equal(decimal.NewFromInt(4000)), "got %s", trace.CostDec.String())
	require.InDelta(t, 4000.0, cost, 1e-9)

	// Empty body (as in smoke test) must cleanly take fallback branch without MemberNode error.
	_, traceEmpty, err := RunExprWithRequest(exprStr, TokenParams{P: 1000, C: 500}, RequestInput{})
	require.NoError(t, err)
	require.Equal(t, "base", traceEmpty.MatchedTier)
	require.True(t, traceEmpty.CostDec.Equal(decimal.NewFromInt(2000)))

	// Usage map nested member access via u("meta").rate
	usageExpr := `tier("task", (u("meta")?.rate ?? 0.5) * (u("count") ?? 1))`
	_, traceUsage, err := RunExprWithRequest(usageExpr, TokenParams{}, RequestInput{
		Usage: map[string]any{
			"meta":  map[string]any{"rate": 0.1},
			"count": 3,
		},
	})
	require.NoError(t, err)
	require.True(t, traceUsage.CostDec.Equal(decimal.RequireFromString("0.3")), "got %s", traceUsage.CostDec.String())
}
