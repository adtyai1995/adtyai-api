package billingexpr

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/expr-lang/expr/vm"
	"github.com/tidwall/gjson"
)

const maxCacheSize = 256

// DefaultExprVersion is used when an expression string has no version prefix.
const DefaultExprVersion = 1

const (
	requestRuleTraceFunction    = "_trace"
	requestRuleTraceIntFunction = "_trace_int"
)

// ParseExprVersion extracts the version tag and body from an expression string.
// Format: "v1:tier(...)" → version=1, body="tier(...)".
// No prefix defaults to DefaultExprVersion.
func ParseExprVersion(exprStr string) (version int, body string) {
	if strings.HasPrefix(exprStr, "v1:") {
		return 1, exprStr[3:]
	}
	return DefaultExprVersion, exprStr
}

// requestRulePatcher adds trace side effects to existing request multipliers
// without changing the stored expression or its numeric result.
type requestRulePatcher struct {
	requestRules         []RequestRuleTrace
	restrictedIdentifier string
}

func (p *requestRulePatcher) Visit(node *ast.Node) {
	if identifier, ok := (*node).(*ast.IdentifierNode); ok {
		switch identifier.Value {
		case requestRuleTraceFunction, requestRuleTraceIntFunction:
			p.restrictedIdentifier = identifier.Value
		}
		return
	}

	conditional, ok := (*node).(*ast.ConditionalNode)
	if !ok || !conditional.Ternary || !usesRequestProbe(conditional.Cond) {
		return
	}
	multiplier, ok := requestRuleNumber(conditional.Exp1)
	fallback, fallbackOK := requestRuleNumber(conditional.Exp2)
	if !ok || !fallbackOK || fallback != 1 {
		return
	}

	ruleIndex := len(p.requestRules)
	p.requestRules = append(p.requestRules, RequestRuleTrace{
		Cond:       conditional.Cond.String(),
		Multiplier: multiplier,
	})

	traceFunction := requestRuleTraceFunction
	// Keep the original literal node so decimal evaluation can recover the
	// source text of multipliers that are not exactly representable in float64.
	multiplierNode := conditional.Exp1
	if _, multiplierIsInt := conditional.Exp1.(*ast.IntegerNode); multiplierIsInt {
		if _, fallbackIsInt := conditional.Exp2.(*ast.IntegerNode); fallbackIsInt {
			traceFunction = requestRuleTraceIntFunction
		}
	}

	ast.Patch(node, &ast.CallNode{
		Callee: &ast.IdentifierNode{Value: traceFunction},
		Arguments: []ast.Node{
			&ast.IntegerNode{Value: ruleIndex},
			conditional.Cond,
			multiplierNode,
		},
	})
}

func requestRuleNumber(node ast.Node) (float64, bool) {
	switch value := node.(type) {
	case *ast.IntegerNode:
		return float64(value.Value), true
	case *ast.FloatNode:
		return value.Value, true
	default:
		return 0, false
	}
}

func usesRequestProbe(node ast.Node) bool {
	return ast.Find(node, func(node ast.Node) bool {
		identifier, ok := node.(*ast.IdentifierNode)
		if !ok {
			return false
		}
		switch identifier.Value {
		case "param", "header", "hour", "minute", "weekday", "month", "day":
			return true
		default:
			return false
		}
	}) != nil
}

type cachedEntry struct {
	prog           *vm.Program
	tree           ast.Node
	body           string
	bodyRunes      []rune
	usedVars       map[string]bool
	usedUsageKeys  map[string]bool
	usedHeaderKeys map[string]bool
	usedParamPaths map[string]bool
	requestRules   []RequestRuleTrace
	version        int
	fixedPricing   bool
}

var (
	cacheMu sync.RWMutex
	cache   = make(map[string]*cachedEntry, 64)
)

// compileEnvPrototypeV1 is the v1 type-checking prototype used at compile time.
var compileEnvPrototypeV1 = map[string]any{
	"image_count": float64(1),
	"p":           float64(0),
	"c":           float64(0),
	"len":         float64(0),
	"cr":          float64(0),
	"cc":          float64(0),
	"cc1h":        float64(0),
	"cc12h":       float64(0),
	"img":         float64(0),
	"img_cr":      float64(0),
	"img_o":       float64(0),
	"ai":          float64(0),
	"ao":          float64(0),
	"tier":        func(string, float64) float64 { return 0 },
	"fixed":       func(float64) float64 { return 0 },
	"_trace":      func(int, bool, float64) float64 { return 1 },
	"_trace_int":  func(int, bool, int) int { return 1 },
	"header":      func(string) string { return "" },
	"param":       func(string) any { return nil },
	"u":           func(string) any { return nil },
	"has":         func(any, string) bool { return false },
	"abs":         math.Abs,
	"ceil":        math.Ceil,
	"floor":       math.Floor,
}

// lenBuiltinPatcher restores builtin len(...) calls that parser.ParseWithConfig
// parses as CallNode{Callee: IdentifierNode{"len"}} because compileEnvPrototypeV1
// also exposes bare "len" as the float64 context-length variable.
type lenBuiltinPatcher struct{}

func (lenBuiltinPatcher) Visit(node *ast.Node) {
	call, ok := (*node).(*ast.CallNode)
	if !ok {
		return
	}
	callee, ok := call.Callee.(*ast.IdentifierNode)
	if !ok || callee.Value != "len" {
		return
	}
	ast.Patch(node, &ast.BuiltinNode{
		Name:      "len",
		Arguments: call.Arguments,
	})
}

var roundCompileOption = expr.Function(
	"round",
	func(params ...any) (any, error) { return float64(0), nil },
	new(func(float64) float64),
	new(func(int) float64),
	new(func(float64, int) float64),
	new(func(int, int) float64),
	new(func(float64, float64) float64),
	new(func(int, float64) float64),
)

func newTimezoneCompileOption(name string) expr.Option {
	return expr.Function(
		name,
		func(params ...any) (any, error) { return 0, nil },
		new(func() int),
		new(func(string) int),
	)
}

var supportedCompileBuiltins = []string{
	"len", "min", "max", "int", "float", "string", "lower", "upper", "trim",
}

var supportedExprCallNames = map[string]bool{
	"tier": true, "fixed": true, "header": true, "param": true, "u": true, "has": true,
	"hour": true, "minute": true, "weekday": true, "month": true, "day": true,
	"max": true, "min": true, "abs": true, "ceil": true, "floor": true, "round": true,
	"len": true, "int": true, "float": true, "string": true, "lower": true, "upper": true, "trim": true,
}

var supportedExprBuiltinNames = map[string]bool{
	"len": true, "min": true, "max": true, "int": true, "float": true, "string": true,
	"lower": true, "upper": true, "trim": true, "round": true, "abs": true, "ceil": true, "floor": true,
}

var nonValueExprIdentifiers = map[string]bool{
	"tier": true, "fixed": true, "header": true, "param": true, "u": true, "has": true,
	"hour": true, "minute": true, "weekday": true, "month": true, "day": true,
	"max": true, "min": true, "abs": true, "ceil": true, "floor": true, "round": true,
	"int": true, "float": true, "string": true, "lower": true, "upper": true, "trim": true,
}

type exprTreeValidator struct {
	err         error
	callCallees map[ast.Node]bool
}

func (v *exprTreeValidator) Visit(node *ast.Node) {
	if v.err != nil || node == nil || *node == nil {
		return
	}
	switch n := (*node).(type) {
	case *ast.NilNode, *ast.BoolNode, *ast.StringNode, *ast.ConstantNode,
		*ast.IntegerNode, *ast.FloatNode, *ast.ConditionalNode, *ast.ChainNode,
		*ast.MemberNode, *ast.ArrayNode, *ast.MapNode, *ast.PairNode, *ast.SliceNode:
		return
	case *ast.IdentifierNode:
		if n.Value == "$env" {
			v.err = fmt.Errorf("identifier %q is not supported", n.Value)
			return
		}
		if nonValueExprIdentifiers[n.Value] && !v.callCallees[n] {
			v.err = fmt.Errorf("identifier %q cannot be used as a value", n.Value)
		}
	case *ast.UnaryNode:
		switch n.Operator {
		case "!", "not", "+", "-":
		default:
			v.err = fmt.Errorf("unsupported unary operator %q", n.Operator)
		}
	case *ast.BinaryNode:
		switch n.Operator {
		case "&&", "and", "||", "or", "??",
			"==", "!=", "<", "<=", ">", ">=",
			"+", "-", "*", "/", "%", "**", "^",
			"in", "not in", "contains", "startsWith", "endsWith", "matches":
		default:
			v.err = fmt.Errorf("unsupported operator %q", n.Operator)
		}
	case *ast.BuiltinNode:
		if !supportedExprBuiltinNames[n.Name] {
			v.err = fmt.Errorf("unsupported builtin %q", n.Name)
		}
	case *ast.CallNode:
		ident, ok := n.Callee.(*ast.IdentifierNode)
		if !ok {
			v.err = fmt.Errorf("unsupported call target %T", n.Callee)
			return
		}
		if !supportedExprCallNames[ident.Value] {
			v.err = fmt.Errorf("unsupported function %q", ident.Value)
		}
	default:
		v.err = fmt.Errorf("unsupported expression syntax %T", *node)
	}
}

func validateSupportedExprTree(root ast.Node) error {
	callees := make(map[ast.Node]bool)
	ast.Find(root, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallNode); ok && call.Callee != nil {
			callees[call.Callee] = true
		}
		return false
	})
	v := &exprTreeValidator{callCallees: callees}
	ast.Walk(&root, v)
	return v.err
}

func isNumericReflectKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

const maxMinMaxValidationDepth = 16

func validateMinMaxConstantValue(name string, val any, depth int) error {
	if depth > maxMinMaxValidationDepth {
		return fmt.Errorf("invalid argument for %s (nesting depth exceeded)", name)
	}
	if val == nil {
		return fmt.Errorf("invalid argument for %s (type nil)", name)
	}
	rv := reflect.ValueOf(val)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return fmt.Errorf("invalid argument for %s (type nil)", name)
		}
		rv = rv.Elem()
	}
	if isNumericReflectKind(rv.Kind()) {
		return nil
	}
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		if rv.Len() == 0 {
			return fmt.Errorf("invalid argument for %s (empty array)", name)
		}
		for i := 0; i < rv.Len(); i++ {
			if err := validateMinMaxConstantValue(name, rv.Index(i).Interface(), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("invalid argument for %s (type %T)", name, val)
}

func validateMinMaxArgType(name string, arg ast.Node) error {
	return validateMinMaxArgTypeDepth(name, arg, 0)
}

func validateMinMaxArgTypeDepth(name string, arg ast.Node, depth int) error {
	if depth > maxMinMaxValidationDepth {
		return fmt.Errorf("invalid argument for %s (nesting depth exceeded)", name)
	}
	if arg == nil {
		return fmt.Errorf("invalid argument for %s (type nil)", name)
	}
	if _, isNil := arg.(*ast.NilNode); isNil || arg.Nature().Nil {
		return fmt.Errorf("invalid argument for %s (type nil)", name)
	}
	if arr, ok := arg.(*ast.ArrayNode); ok {
		if len(arr.Nodes) == 0 {
			return fmt.Errorf("invalid argument for %s (empty array)", name)
		}
		for _, elem := range arr.Nodes {
			if err := validateMinMaxArgTypeDepth(name, elem, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if cn, ok := arg.(*ast.ConstantNode); ok {
		return validateMinMaxConstantValue(name, cn.Value, depth)
	}
	t := arg.Type()
	if t == nil {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if isNumericReflectKind(t.Kind()) || t.Kind() == reflect.Interface {
		return nil
	}
	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		elem := t.Elem()
		for elem.Kind() == reflect.Pointer || elem.Kind() == reflect.Slice || elem.Kind() == reflect.Array {
			elem = elem.Elem()
		}
		if isNumericReflectKind(elem.Kind()) || elem.Kind() == reflect.Interface {
			return nil
		}
	}
	return fmt.Errorf("invalid argument for %s (type %s)", name, t)
}

func validateCompiledMinMaxArgs(root ast.Node) error {
	var err error
	ast.Find(root, func(node ast.Node) bool {
		if err != nil {
			return true
		}
		bn, ok := node.(*ast.BuiltinNode)
		if !ok || (bn.Name != "min" && bn.Name != "max") {
			return false
		}
		if len(bn.Arguments) == 0 {
			err = fmt.Errorf("not enough arguments to call %s", bn.Name)
			return true
		}
		for _, arg := range bn.Arguments {
			if argErr := validateMinMaxArgType(bn.Name, arg); argErr != nil {
				err = argErr
				return true
			}
		}
		return false
	})
	return err
}

func getCompileEnv(version int) map[string]any {
	switch version {
	default:
		return compileEnvPrototypeV1
	}
}

// CompileFromCache compiles an expression string, using a cached program when
// available. The cache is keyed by the SHA-256 hex digest of the expression.
func CompileFromCache(exprStr string) (*vm.Program, error) {
	return compileFromCacheByHash(exprStr, ExprHashString(exprStr))
}

// CompileFromCacheByHash is like CompileFromCache but accepts a pre-computed
// hash, useful when the caller already has the BillingSnapshot.ExprHash.
func CompileFromCacheByHash(exprStr, hash string) (*vm.Program, error) {
	return compileFromCacheByHash(exprStr, hash)
}

func compileFromCacheByHash(exprStr, hash string) (*vm.Program, error) {
	entry, err := compileEntryFromCacheByHash(exprStr, hash)
	if err != nil {
		return nil, err
	}
	return entry.prog, nil
}

func compileEntryFromCacheByHash(exprStr, hash string) (*cachedEntry, error) {
	cacheMu.RLock()
	if entry, ok := cache[hash]; ok {
		cacheMu.RUnlock()
		return entry, nil
	}
	cacheMu.RUnlock()

	version, body := ParseExprVersion(exprStr)
	// Validate before optimization so unreachable fixed-price branches cannot
	// bypass validation or a host's unsupported-protocol checks.
	tree, err := parser.Parse(body)
	if err != nil {
		return nil, fmt.Errorf("expr compile error: %w", err)
	}
	if err := validateExponentNodes(tree.Node); err != nil {
		return nil, fmt.Errorf("expr compile error: %w", err)
	}
	if err := validateSupportedExprTree(tree.Node); err != nil {
		return nil, fmt.Errorf("expr compile error: %w", err)
	}
	fixedPricing := ast.Find(tree.Node, func(node ast.Node) bool {
		identifier, ok := node.(*ast.IdentifierNode)
		return ok && identifier.Value == "fixed"
	}) != nil
	if fixedPricing {
		if err := validateFixedPricingTree(tree.Node); err != nil {
			return nil, fmt.Errorf("expr compile error: %w", err)
		}
	}
	patcher := &requestRulePatcher{}
	ast.Walk(&tree.Node, patcher)
	if patcher.restrictedIdentifier != "" {
		return nil, fmt.Errorf("expr compile error: identifier %q is reserved for internal use", patcher.restrictedIdentifier)
	}
	// Compile still type-checks and accepts the same programs; financial
	// evaluation uses the patched parse tree with exact decimals instead of
	// the float64 VM program.
	compileOpts := make([]expr.Option, 0, len(supportedCompileBuiltins)+11)
	compileOpts = append(compileOpts,
		expr.Env(getCompileEnv(version)),
		expr.DisableAllBuiltins(),
	)
	for _, builtinName := range supportedCompileBuiltins {
		compileOpts = append(compileOpts, expr.EnableBuiltin(builtinName))
	}
	compileOpts = append(compileOpts,
		expr.Patch(lenBuiltinPatcher{}),
		expr.Patch(&requestRulePatcher{}),
		roundCompileOption,
		newTimezoneCompileOption("hour"),
		newTimezoneCompileOption("minute"),
		newTimezoneCompileOption("weekday"),
		newTimezoneCompileOption("month"),
		newTimezoneCompileOption("day"),
		expr.AsFloat64(),
	)
	prog, err := expr.Compile(body, compileOpts...)
	if err != nil {
		return nil, fmt.Errorf("expr compile error: %w", err)
	}
	if err := validateCompiledMinMaxArgs(prog.Node()); err != nil {
		return nil, fmt.Errorf("expr compile error: %w", err)
	}

	entry := &cachedEntry{
		prog:           prog,
		tree:           tree.Node,
		body:           body,
		bodyRunes:      []rune(body),
		usedVars:       extractUsedVars(prog),
		usedUsageKeys:  extractUsedUsageKeys(prog),
		usedHeaderKeys: extractUsedCallStringArgs(tree.Node, "header", true),
		usedParamPaths: extractUsedCallStringArgs(tree.Node, "param", false),
		requestRules:   patcher.requestRules,
		version:        version,
		fixedPricing:   fixedPricing,
	}
	cacheMu.Lock()
	if len(cache) >= maxCacheSize {
		cache = make(map[string]*cachedEntry, 64)
	}
	cache[hash] = entry
	cacheMu.Unlock()

	return entry, nil
}

// ExprVersion returns the version of a cached expression. Returns DefaultExprVersion
// if the expression hasn't been compiled yet or is empty.
func ExprVersion(exprStr string) int {
	if exprStr == "" {
		return DefaultExprVersion
	}
	hash := ExprHashString(exprStr)
	cacheMu.RLock()
	if entry, ok := cache[hash]; ok {
		cacheMu.RUnlock()
		return entry.version
	}
	cacheMu.RUnlock()
	v, _ := ParseExprVersion(exprStr)
	return v
}

func extractUsedVars(prog *vm.Program) map[string]bool {
	vars := make(map[string]bool)
	node := prog.Node()
	ast.Find(node, func(n ast.Node) bool {
		if id, ok := n.(*ast.IdentifierNode); ok {
			switch id.Value {
			case requestRuleTraceFunction, requestRuleTraceIntFunction:
				return false
			}
			vars[id.Value] = true
		}
		return false
	})
	return vars
}

func extractUsedUsageKeys(prog *vm.Program) map[string]bool {
	return extractUsedCallStringArgs(prog.Node(), "u", false)
}

func extractUsedCallStringArgs(root ast.Node, fnName string, lower bool) map[string]bool {
	keys := make(map[string]bool)
	ast.Find(root, func(node ast.Node) bool {
		call, ok := node.(*ast.CallNode)
		if !ok || len(call.Arguments) != 1 {
			return false
		}
		callee, ok := call.Callee.(*ast.IdentifierNode)
		if !ok || callee.Value != fnName {
			return false
		}
		literal, ok := call.Arguments[0].(*ast.StringNode)
		if !ok {
			return false
		}
		val := strings.TrimSpace(literal.Value)
		if lower {
			val = strings.ToLower(val)
		}
		if val != "" {
			keys[val] = true
		}
		return false
	})
	return keys
}

const maxFrozenRequestValueBytes = 4096

// FreezeRequestInputs extracts only the headers and body param paths referenced
// by exprStr so asynchronous task completion can re-evaluate request rules
// deterministically without persisting full request payloads.
func FreezeRequestInputs(exprStr, hash string, request RequestInput) (map[string]string, map[string]any) {
	if exprStr == "" {
		return nil, nil
	}
	if hash == "" {
		hash = ExprHashString(exprStr)
	}
	entry, err := compileEntryFromCacheByHash(exprStr, hash)
	if err != nil || entry == nil {
		return nil, nil
	}
	var frozenHeaders map[string]string
	if len(entry.usedHeaderKeys) > 0 {
		normHeaders := normalizeHeaders(request.Headers)
		for key := range entry.usedHeaderKeys {
			if val, ok := normHeaders[key]; ok && val != "" && len(val) <= maxFrozenRequestValueBytes {
				if frozenHeaders == nil {
					frozenHeaders = make(map[string]string, len(entry.usedHeaderKeys))
				}
				frozenHeaders[key] = val
			}
		}
	}
	var frozenParams map[string]any
	if len(entry.usedParamPaths) > 0 {
		for path := range entry.usedParamPaths {
			if len(request.Body) > 0 {
				res := gjson.GetBytes(request.Body, path)
				if res.Exists() && len(res.Raw) <= maxFrozenRequestValueBytes {
					if frozenParams == nil {
						frozenParams = make(map[string]any, len(entry.usedParamPaths))
					}
					frozenParams[path] = res.Value()
				}
			} else if request.FrozenParams != nil {
				if val, ok := request.FrozenParams[path]; ok {
					if frozenParams == nil {
						frozenParams = make(map[string]any, len(entry.usedParamPaths))
					}
					frozenParams[path] = val
				}
			}
		}
	}
	return frozenHeaders, frozenParams
}

// UsedVars returns the set of identifier names referenced by an expression.
// The result is cached alongside the compiled program. Returns nil for empty input.
func UsedVars(exprStr string) map[string]bool {
	return UsedVarsByHash(exprStr, ExprHashString(exprStr))
}

// UsedVarsByHash reuses the digest captured by the host's billing snapshot.
func UsedVarsByHash(exprStr, hash string) map[string]bool {
	if exprStr == "" {
		return nil
	}
	cacheMu.RLock()
	if entry, ok := cache[hash]; ok {
		cacheMu.RUnlock()
		return entry.usedVars
	}
	cacheMu.RUnlock()

	// Compile (and cache) to populate usedVars
	if _, err := compileFromCacheByHash(exprStr, hash); err != nil {
		return nil
	}
	cacheMu.RLock()
	entry, ok := cache[hash]
	cacheMu.RUnlock()
	if ok {
		return entry.usedVars
	}
	return nil
}

// UsedUsageKeys returns literal keys referenced by u("...") calls. Calls with
// dynamic arguments are intentionally omitted because they cannot be
// validated statically.
func UsedUsageKeys(exprStr string) map[string]bool {
	if exprStr == "" {
		return nil
	}
	hash := ExprHashString(exprStr)
	cacheMu.RLock()
	if entry, ok := cache[hash]; ok {
		cacheMu.RUnlock()
		return entry.usedUsageKeys
	}
	cacheMu.RUnlock()

	if _, err := compileFromCacheByHash(exprStr, hash); err != nil {
		return nil
	}
	cacheMu.RLock()
	entry, ok := cache[hash]
	cacheMu.RUnlock()
	if ok {
		return entry.usedUsageKeys
	}
	return nil
}

// InvalidateCache clears the compiled-expression cache.
// Called when billing rules are updated.
func InvalidateCache() {
	cacheMu.Lock()
	cache = make(map[string]*cachedEntry, 64)
	cacheMu.Unlock()
}
