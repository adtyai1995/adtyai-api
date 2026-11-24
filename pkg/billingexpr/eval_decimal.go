package billingexpr

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/file"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

const (
	maxExprExponent           = 32
	maxExprIntermediateDigits = 64
	maxExprAbsExp             = 64
	maxExprRegexLen           = 1024
	maxExprRegexCacheSize     = 256
)

var (
	errNonFiniteExpr    = errors.New("expr result must be finite and non-negative")
	errOptionalChainNil = errors.New("optional chain nil")

	exprRegexCacheMu sync.RWMutex
	exprRegexCache   = make(map[string]*regexp.Regexp, 32)
)

func checkExprDecimalBounds(d decimal.Decimal) (decimal.Decimal, error) {
	if d.IsZero() {
		return d, nil
	}
	exp := int(d.Exponent())
	coeffDigits := len(d.Coefficient().String())
	if d.IsNegative() && coeffDigits > 0 {
		coeffDigits--
	}
	// Magnitude = coeffDigits + exp (approx log10(|d|) + 1); cap both magnitude and precision.
	if coeffDigits > maxExprIntermediateDigits*2 || exp > maxExprAbsExp || exp < -maxExprAbsExp || coeffDigits+exp > maxExprAbsExp {
		return decimal.Zero, errNonFiniteExpr
	}
	return d, nil
}

func validateExponentNodes(root ast.Node) error {
	var foundErr error
	ast.Walk(&root, &exponentValidator{err: &foundErr})
	return foundErr
}

type exponentValidator struct {
	err *error
}

func (v *exponentValidator) Visit(node *ast.Node) {
	if *v.err != nil || node == nil || *node == nil {
		return
	}
	bin, ok := (*node).(*ast.BinaryNode)
	if !ok || (bin.Operator != "**" && bin.Operator != "^") {
		return
	}
	if val, ok := literalNumericValue(bin.Right); ok {
		if math.IsNaN(val) || math.IsInf(val, 0) || math.Trunc(val) != val || math.Abs(val) > maxExprExponent {
			*v.err = fmt.Errorf("exponent must be an integer within [-%d, %d]", maxExprExponent, maxExprExponent)
		}
	}
}

func literalNumericValue(node ast.Node) (float64, bool) {
	switch n := node.(type) {
	case *ast.IntegerNode:
		return float64(n.Value), true
	case *ast.FloatNode:
		return n.Value, true
	case *ast.UnaryNode:
		if n.Operator == "-" {
			if v, ok := literalNumericValue(n.Node); ok {
				return -v, true
			}
		} else if n.Operator == "+" {
			return literalNumericValue(n.Node)
		}
	}
	return 0, false
}

type decimalEvalEnv struct {
	src             string
	vars            map[string]decimal.Decimal
	extras          map[string]any
	trace           *TraceResult
	headers         map[string]string
	body            []byte
	usage           map[string]any
	now             time.Time
	defaultTimezone string
	chainDepth      int
}

func evalBillingExpr(node ast.Node, src string, env *decimalEvalEnv) (decimal.Decimal, error) {
	if env == nil {
		env = &decimalEvalEnv{src: src}
	}
	out, err := evalNode(node, env)
	if err != nil {
		return decimal.Zero, err
	}
	cost, ok := valueAsDecimal(out)
	if !ok {
		return decimal.Zero, fmt.Errorf("expr result is %T, want a finite non-negative number", out)
	}
	if !decimalFiniteNonNegative(cost) {
		return decimal.Zero, errNonFiniteExpr
	}
	return cost, nil
}

func evalNode(node ast.Node, env *decimalEvalEnv) (any, error) {
	if node == nil {
		return nil, fmt.Errorf("expr run error: missing node")
	}
	switch n := node.(type) {
	case *ast.NilNode:
		return nil, nil
	case *ast.BoolNode:
		return n.Value, nil
	case *ast.StringNode:
		return n.Value, nil
	case *ast.BytesNode:
		return n.Value, nil
	case *ast.ConstantNode:
		return normalizeMemberValue(n.Value), nil
	case *ast.IntegerNode:
		return decimal.NewFromInt(int64(n.Value)), nil
	case *ast.FloatNode:
		return decimalFromFloatNode(env.src, n), nil
	case *ast.IdentifierNode:
		return evalIdentifier(n.Value, env)
	case *ast.UnaryNode:
		return evalUnary(n, env)
	case *ast.BinaryNode:
		return evalBinary(n, env)
	case *ast.ConditionalNode:
		cond, err := evalNode(n.Cond, env)
		if err != nil {
			return nil, err
		}
		ok, err := truthy(cond)
		if err != nil {
			return nil, err
		}
		if ok {
			return evalNode(n.Exp1, env)
		}
		return evalNode(n.Exp2, env)
	case *ast.ChainNode:
		return evalChain(n, env)
	case *ast.MemberNode:
		return evalMember(n, env)
	case *ast.ArrayNode:
		return evalArray(n, env)
	case *ast.MapNode:
		return evalMap(n, env)
	case *ast.PairNode:
		return evalNode(n.Value, env)
	case *ast.SliceNode:
		return evalSlice(n, env)
	case *ast.CallNode:
		return evalCall(n, env)
	case *ast.BuiltinNode:
		return evalBuiltin(n, env)
	default:
		return nil, fmt.Errorf("expr run error: unsupported expression node %T", node)
	}
}

func evalIdentifier(name string, env *decimalEvalEnv) (any, error) {
	if env != nil {
		if d, ok := env.vars[name]; ok {
			return d, nil
		}
		if env.extras != nil {
			if v, ok := env.extras[name]; ok {
				return normalizeMemberValue(v), nil
			}
		}
	}
	switch name {
	case requestRuleTraceFunction, requestRuleTraceIntFunction, "tier", "fixed", "header", "param", "u", "has", "hour", "minute", "weekday", "month", "day", "max", "min", "abs", "ceil", "floor", "round", "int", "float", "string", "lower", "upper", "trim":
		return nil, fmt.Errorf("expr run error: identifier %q cannot be used as a value", name)
	default:
		return nil, fmt.Errorf("expr run error: unknown identifier %q", name)
	}
}

func evalChain(n *ast.ChainNode, env *decimalEvalEnv) (any, error) {
	env.chainDepth++
	val, err := evalNode(n.Node, env)
	env.chainDepth--
	if errors.Is(err, errOptionalChainNil) {
		return nil, nil
	}
	return val, err
}

func evalMember(n *ast.MemberNode, env *decimalEvalEnv) (any, error) {
	if n.Method {
		return nil, fmt.Errorf("expr run error: method calls are not supported")
	}
	recv, err := evalNode(n.Node, env)
	if err != nil {
		return nil, err
	}
	if recv == nil {
		if n.Optional {
			if env.chainDepth > 0 {
				return nil, errOptionalChainNil
			}
			return nil, nil
		}
		return nil, fmt.Errorf("expr run error: cannot access member on nil")
	}
	prop, err := evalNode(n.Property, env)
	if err != nil {
		return nil, err
	}

	// Fast paths for common Go map types.
	switch m := recv.(type) {
	case map[string]any:
		if m == nil {
			if n.Optional && env.chainDepth > 0 {
				return nil, errOptionalChainNil
			}
			return nil, nil
		}
		key, ok := prop.(string)
		if !ok {
			return nil, fmt.Errorf("expr run error: map key must be a string, got %T", prop)
		}
		val, exists := m[key]
		if !exists {
			return nil, nil
		}
		return normalizeMemberValue(val), nil
	case map[string]decimal.Decimal:
		if m == nil {
			if n.Optional && env.chainDepth > 0 {
				return nil, errOptionalChainNil
			}
			return nil, nil
		}
		key, ok := prop.(string)
		if !ok {
			return nil, fmt.Errorf("expr run error: map key must be a string, got %T", prop)
		}
		val, exists := m[key]
		if !exists {
			return nil, nil
		}
		return val, nil
	case map[string]float64:
		if m == nil {
			if n.Optional && env.chainDepth > 0 {
				return nil, errOptionalChainNil
			}
			return nil, nil
		}
		key, ok := prop.(string)
		if !ok {
			return nil, fmt.Errorf("expr run error: map key must be a string, got %T", prop)
		}
		val, exists := m[key]
		if !exists {
			return nil, nil
		}
		return normalizeMemberValue(val), nil
	case map[string]int:
		if m == nil {
			if n.Optional && env.chainDepth > 0 {
				return nil, errOptionalChainNil
			}
			return nil, nil
		}
		key, ok := prop.(string)
		if !ok {
			return nil, fmt.Errorf("expr run error: map key must be a string, got %T", prop)
		}
		val, exists := m[key]
		if !exists {
			return nil, nil
		}
		return decimal.NewFromInt(int64(val)), nil
	case map[string]string:
		if m == nil {
			if n.Optional && env.chainDepth > 0 {
				return nil, errOptionalChainNil
			}
			return nil, nil
		}
		key, ok := prop.(string)
		if !ok {
			return nil, fmt.Errorf("expr run error: map key must be a string, got %T", prop)
		}
		val, exists := m[key]
		if !exists {
			return nil, nil
		}
		return val, nil
	}

	rv := reflect.ValueOf(recv)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			if n.Optional {
				if env.chainDepth > 0 {
					return nil, errOptionalChainNil
				}
				return nil, nil
			}
			return nil, fmt.Errorf("expr run error: cannot access member %v on nil", prop)
		}
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.Map:
		if rv.IsNil() {
			if n.Optional && env.chainDepth > 0 {
				return nil, errOptionalChainNil
			}
			return nil, nil
		}
		kv, ok := convertMapKey(prop, rv.Type().Key())
		if !ok {
			return nil, fmt.Errorf("expr run error: cannot use %T as key for %s", prop, rv.Type())
		}
		mv := rv.MapIndex(kv)
		if !mv.IsValid() {
			return nil, nil
		}
		return normalizeMemberValue(mv.Interface()), nil
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() && n.Optional {
			if env.chainDepth > 0 {
				return nil, errOptionalChainNil
			}
			return nil, nil
		}
		idx, ok := decimalAsInt(prop)
		if !ok {
			return nil, fmt.Errorf("expr run error: slice index must be an integer, got %T", prop)
		}
		length := rv.Len()
		if idx < 0 {
			idx = length + idx
		}
		if idx < 0 || idx >= length {
			return nil, fmt.Errorf("expr run error: index %d out of range [0:%d]", idx, length)
		}
		return normalizeMemberValue(rv.Index(idx).Interface()), nil
	case reflect.String:
		idx, ok := decimalAsInt(prop)
		if !ok {
			return nil, fmt.Errorf("expr run error: string index must be an integer, got %T", prop)
		}
		s := rv.String()
		length := len(s)
		if idx < 0 {
			idx = length + idx
		}
		if idx < 0 || idx >= length {
			return nil, fmt.Errorf("expr run error: index %d out of range [0:%d]", idx, length)
		}
		return string(s[idx]), nil
	case reflect.Struct:
		propName, ok := prop.(string)
		if !ok {
			return nil, fmt.Errorf("expr run error: struct property must be a string, got %T", prop)
		}
		fv, found := lookupExportedStructField(rv, propName)
		if !found {
			return nil, fmt.Errorf("expr run error: unknown struct field %q on %s", propName, rv.Type())
		}
		return normalizeMemberValue(fv.Interface()), nil
	default:
		return nil, fmt.Errorf("expr run error: cannot access member %v on %T", prop, recv)
	}
}

func evalArray(n *ast.ArrayNode, env *decimalEvalEnv) (any, error) {
	out := make([]any, len(n.Nodes))
	for i, item := range n.Nodes {
		v, err := evalNode(item, env)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func evalMap(n *ast.MapNode, env *decimalEvalEnv) (any, error) {
	out := make(map[string]any, len(n.Pairs))
	for _, raw := range n.Pairs {
		pair, ok := raw.(*ast.PairNode)
		if !ok {
			return nil, fmt.Errorf("expr run error: expected map pair node, got %T", raw)
		}
		k, err := evalNode(pair.Key, env)
		if err != nil {
			return nil, err
		}
		var keyStr string
		switch kv := k.(type) {
		case string:
			keyStr = kv
		case decimal.Decimal:
			keyStr = kv.String()
		default:
			keyStr = fmt.Sprint(k)
		}
		v, err := evalNode(pair.Value, env)
		if err != nil {
			return nil, err
		}
		out[keyStr] = v
	}
	return out, nil
}

func evalSlice(n *ast.SliceNode, env *decimalEvalEnv) (any, error) {
	target, err := evalNode(n.Node, env)
	if err != nil {
		return nil, err
	}
	if isNilValue(target) {
		return nil, fmt.Errorf("expr run error: cannot slice nil")
	}
	rv := reflect.ValueOf(target)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil, fmt.Errorf("expr run error: cannot slice nil")
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.String, reflect.Slice, reflect.Array:
		length := rv.Len()
		from := 0
		if n.From != nil {
			fv, err := evalNode(n.From, env)
			if err != nil {
				return nil, err
			}
			idx, ok := decimalAsInt(fv)
			if !ok {
				return nil, fmt.Errorf("expr run error: slice start must be an integer, got %T", fv)
			}
			from = idx
		}
		to := length
		if n.To != nil {
			tv, err := evalNode(n.To, env)
			if err != nil {
				return nil, err
			}
			idx, ok := decimalAsInt(tv)
			if !ok {
				return nil, fmt.Errorf("expr run error: slice end must be an integer, got %T", tv)
			}
			to = idx
		}
		if from < 0 {
			from = length + from
		}
		if from < 0 {
			from = 0
		}
		if to < 0 {
			to = length + to
		}
		if to < 0 {
			to = 0
		}
		if to > length {
			to = length
		}
		if from > to {
			from = to
		}
		if rv.Kind() == reflect.Array && !rv.CanAddr() {
			copyVal := reflect.New(rv.Type()).Elem()
			copyVal.Set(rv)
			rv = copyVal
		}
		return rv.Slice(from, to).Interface(), nil
	default:
		return nil, fmt.Errorf("expr run error: cannot slice %T", target)
	}
}

// lookupExportedStructField searches exported struct fields only (never methods),
// matching expr tag, json tag, or Go field name across direct and promoted embedded structs.
func lookupExportedStructField(rv reflect.Value, name string) (reflect.Value, bool) {
	queue := []reflect.Value{rv}
	for len(queue) > 0 {
		levelSize := len(queue)
		var next []reflect.Value
		for i := 0; i < levelSize; i++ {
			cur := queue[i]
			rt := cur.Type()
			// Pass 1: expr tag
			for j := 0; j < rt.NumField(); j++ {
				f := rt.Field(j)
				if !f.IsExported() {
					continue
				}
				tag := f.Tag.Get("expr")
				if tag == "-" || tag == "" {
					continue
				}
				if strings.Split(tag, ",")[0] == name {
					fv := cur.Field(j)
					if fv.CanInterface() && fv.Kind() != reflect.Func {
						return fv, true
					}
				}
			}
			// Pass 2: json tag
			for j := 0; j < rt.NumField(); j++ {
				f := rt.Field(j)
				if !f.IsExported() || f.Tag.Get("expr") == "-" {
					continue
				}
				tag := f.Tag.Get("json")
				if tag == "" {
					continue
				}
				tagName := strings.Split(tag, ",")[0]
				if tagName != "" && tagName != "-" && tagName == name {
					fv := cur.Field(j)
					if fv.CanInterface() && fv.Kind() != reflect.Func {
						return fv, true
					}
				}
			}
			// Pass 3: Go field name
			for j := 0; j < rt.NumField(); j++ {
				f := rt.Field(j)
				if !f.IsExported() || f.Tag.Get("expr") == "-" {
					continue
				}
				if f.Name == name {
					fv := cur.Field(j)
					if fv.CanInterface() && fv.Kind() != reflect.Func {
						return fv, true
					}
				}
			}
			// Queue embedded structs for the next BFS level.
			for j := 0; j < rt.NumField(); j++ {
				f := rt.Field(j)
				if !f.Anonymous {
					continue
				}
				fv := cur.Field(j)
				for fv.Kind() == reflect.Pointer {
					if fv.IsNil() {
						fv = reflect.Value{}
						break
					}
					fv = fv.Elem()
				}
				if fv.IsValid() && fv.Kind() == reflect.Struct {
					next = append(next, fv)
				}
			}
		}
		queue = next
	}
	return reflect.Value{}, false
}

func convertMapKey(prop any, keyType reflect.Type) (reflect.Value, bool) {
	if prop == nil {
		return reflect.Value{}, false
	}
	switch keyType.Kind() {
	case reflect.String:
		s, ok := prop.(string)
		if !ok {
			return reflect.Value{}, false
		}
		return reflect.ValueOf(s).Convert(keyType), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, ok := decimalAsInt64(prop)
		if !ok {
			return reflect.Value{}, false
		}
		v := reflect.New(keyType).Elem()
		if v.OverflowInt(i) {
			return reflect.Value{}, false
		}
		v.SetInt(i)
		return v, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u, ok := decimalAsUint64(prop)
		if !ok {
			return reflect.Value{}, false
		}
		v := reflect.New(keyType).Elem()
		if v.OverflowUint(u) {
			return reflect.Value{}, false
		}
		v.SetUint(u)
		return v, true
	case reflect.Bool:
		b, ok := prop.(bool)
		if !ok {
			return reflect.Value{}, false
		}
		return reflect.ValueOf(b).Convert(keyType), true
	default:
		pv := reflect.ValueOf(prop)
		if pv.IsValid() && pv.Type().AssignableTo(keyType) {
			return pv, true
		}
		return reflect.Value{}, false
	}
}

func normalizeMemberValue(v any) any {
	if v == nil {
		return nil
	}
	if d, ok := valueAsDecimal(v); ok {
		return d
	}
	switch x := v.(type) {
	case *decimal.Decimal:
		if x == nil {
			return nil
		}
		return *x
	case *float64:
		if x == nil {
			return nil
		}
		if d, ok := valueAsDecimal(*x); ok {
			return d
		}
		return *x
	case *float32:
		if x == nil {
			return nil
		}
		if d, ok := valueAsDecimal(*x); ok {
			return d
		}
		return *x
	case *int:
		if x == nil {
			return nil
		}
		return decimal.NewFromInt(int64(*x))
	case *int64:
		if x == nil {
			return nil
		}
		return decimal.NewFromInt(*x)
	case *string:
		if x == nil {
			return nil
		}
		return *x
	case *bool:
		if x == nil {
			return nil
		}
		return *x
	default:
		return v
	}
}

func isNilValue(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}

func evalUnary(n *ast.UnaryNode, env *decimalEvalEnv) (any, error) {
	v, err := evalNode(n.Node, env)
	if err != nil {
		return nil, err
	}
	switch n.Operator {
	case "!", "not":
		ok, err := truthy(v)
		if err != nil {
			return nil, err
		}
		return !ok, nil
	case "+", "-":
		d, ok := valueAsDecimal(v)
		if !ok {
			return nil, fmt.Errorf("expr run error: unary %s requires a number, got %T", n.Operator, v)
		}
		if n.Operator == "-" {
			return d.Neg(), nil
		}
		return d, nil
	default:
		return nil, fmt.Errorf("expr run error: unsupported unary operator %q", n.Operator)
	}
}

func evalBinary(n *ast.BinaryNode, env *decimalEvalEnv) (any, error) {
	switch n.Operator {
	case "&&", "and":
		left, err := evalNode(n.Left, env)
		if err != nil {
			return nil, err
		}
		ok, err := truthy(left)
		if err != nil {
			return nil, err
		}
		if !ok {
			return false, nil
		}
		right, err := evalNode(n.Right, env)
		if err != nil {
			return nil, err
		}
		return truthy(right)
	case "||", "or":
		left, err := evalNode(n.Left, env)
		if err != nil {
			return nil, err
		}
		ok, err := truthy(left)
		if err != nil {
			return nil, err
		}
		if ok {
			return true, nil
		}
		right, err := evalNode(n.Right, env)
		if err != nil {
			return nil, err
		}
		return truthy(right)
	case "??":
		left, err := evalNode(n.Left, env)
		if err != nil {
			return nil, err
		}
		if !isNilValue(left) {
			return left, nil
		}
		return evalNode(n.Right, env)
	}

	left, err := evalNode(n.Left, env)
	if err != nil {
		return nil, err
	}
	right, err := evalNode(n.Right, env)
	if err != nil {
		return nil, err
	}

	switch n.Operator {
	case "==", "!=", "<", "<=", ">", ">=":
		return compareValues(n.Operator, left, right)
	case "+", "-", "*", "/", "%", "**", "^":
		return evalArithmetic(n.Operator, left, right)
	case "in":
		return evalIn(left, right)
	case "not in":
		found, err := evalIn(left, right)
		if err != nil {
			return nil, err
		}
		return !found, nil
	case "contains", "startsWith", "endsWith":
		return evalStringBinary(n.Operator, left, right)
	case "matches":
		return evalMatches(left, right)
	default:
		return nil, fmt.Errorf("expr run error: unsupported operator %q", n.Operator)
	}
}

func evalIn(needle, haystack any) (bool, error) {
	if isNilValue(haystack) {
		return false, nil
	}
	rv := reflect.ValueOf(haystack)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return false, nil
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			elem := normalizeMemberValue(rv.Index(i).Interface())
			eq, err := compareValues("==", needle, elem)
			if err == nil && eq {
				return true, nil
			}
		}
		return false, nil
	case reflect.Map:
		if rv.IsNil() {
			return false, nil
		}
		kv, ok := convertMapKey(needle, rv.Type().Key())
		if !ok {
			return false, nil
		}
		return rv.MapIndex(kv).IsValid(), nil
	case reflect.Struct:
		name, ok := needle.(string)
		if !ok {
			return false, fmt.Errorf("expr run error: struct key for 'in' must be a string, got %T", needle)
		}
		_, found := lookupExportedStructField(rv, name)
		return found, nil
	default:
		return false, fmt.Errorf("expr run error: operator 'in' not defined on %T", haystack)
	}
}

func evalStringBinary(op string, left, right any) (bool, error) {
	rs, rOK := right.(string)
	if !rOK {
		return false, fmt.Errorf("expr run error: operator %s requires string right operand, got %T", op, right)
	}
	if isNilValue(left) {
		return false, nil
	}
	ls, lOK := left.(string)
	if !lOK {
		return false, fmt.Errorf("expr run error: operator %s requires string left operand, got %T", op, left)
	}
	switch op {
	case "contains":
		return strings.Contains(ls, rs), nil
	case "startsWith":
		return strings.HasPrefix(ls, rs), nil
	case "endsWith":
		return strings.HasSuffix(ls, rs), nil
	default:
		return false, fmt.Errorf("expr run error: unsupported string operator %q", op)
	}
}

func evalMatches(left, right any) (bool, error) {
	pattern, ok := right.(string)
	if !ok {
		return false, fmt.Errorf("expr run error: operator matches requires string pattern, got %T", right)
	}
	re, err := compileExprRegex(pattern)
	if err != nil {
		return false, err
	}
	if isNilValue(left) {
		return false, nil
	}
	ls, ok := left.(string)
	if !ok {
		return false, fmt.Errorf("expr run error: operator matches requires string left operand, got %T", left)
	}
	return re.MatchString(ls), nil
}

func compileExprRegex(pattern string) (*regexp.Regexp, error) {
	if len(pattern) > maxExprRegexLen {
		return nil, fmt.Errorf("expr run error: regex pattern exceeds maximum length %d", maxExprRegexLen)
	}
	exprRegexCacheMu.RLock()
	re, ok := exprRegexCache[pattern]
	exprRegexCacheMu.RUnlock()
	if ok {
		return re, nil
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("expr run error: invalid regex %q: %w", pattern, err)
	}
	exprRegexCacheMu.Lock()
	if len(exprRegexCache) >= maxExprRegexCacheSize {
		exprRegexCache = make(map[string]*regexp.Regexp, 32)
	}
	exprRegexCache[pattern] = compiled
	exprRegexCacheMu.Unlock()
	return compiled, nil
}

func evalArithmetic(op string, left, right any) (any, error) {
	if op == "+" {
		if ls, lOK := left.(string); lOK {
			if rs, rOK := right.(string); rOK {
				return ls + rs, nil
			}
		}
	}
	ld, lOK := valueAsDecimal(left)
	rd, rOK := valueAsDecimal(right)
	if !lOK || !rOK {
		return nil, fmt.Errorf("expr run error: operator %s requires numbers, got %T and %T", op, left, right)
	}
	switch op {
	case "+":
		return ld.Add(rd), nil
	case "-":
		return ld.Sub(rd), nil
	case "*":
		return checkExprDecimalBounds(ld.Mul(rd))
	case "/":
		if rd.IsZero() {
			return nil, errNonFiniteExpr
		}
		return checkExprDecimalBounds(ld.Div(rd))
	case "%":
		if rd.IsZero() {
			return nil, errNonFiniteExpr
		}
		return ld.Mod(rd), nil
	case "**", "^":
		if !rd.Equal(rd.Truncate(0)) || rd.Abs().GreaterThan(decimal.NewFromInt(maxExprExponent)) {
			return nil, fmt.Errorf("expr run error: exponent must be an integer within [-%d, %d]", maxExprExponent, maxExprExponent)
		}
		if ld.IsZero() && rd.IsNegative() {
			return nil, errNonFiniteExpr
		}
		if _, err := checkExprDecimalBounds(ld); err != nil {
			return nil, err
		}
		// Pre-check estimated magnitude before calling Pow to avoid huge big.Int allocation
		// on chained powers like ((((2**32)**32)**32)**32) (R2-038).
		expInt := rd.IntPart()
		if !ld.IsZero() && !ld.Abs().Equal(decimal.NewFromInt(1)) && expInt != 0 {
			f, _ := ld.Abs().Float64()
			if math.IsInf(f, 0) || math.IsNaN(f) || f <= 0 {
				return nil, errNonFiniteExpr
			}
			estLog10 := math.Abs(float64(expInt) * math.Log10(f))
			if math.IsInf(estLog10, 0) || math.IsNaN(estLog10) || estLog10 > float64(maxExprAbsExp) {
				return nil, errNonFiniteExpr
			}
		}
		return checkExprDecimalBounds(ld.Pow(rd))
	default:
		return nil, fmt.Errorf("expr run error: unsupported operator %q", op)
	}
}

func evalCall(n *ast.CallNode, env *decimalEvalEnv) (any, error) {
	ident, ok := n.Callee.(*ast.IdentifierNode)
	if !ok {
		return nil, fmt.Errorf("expr run error: unsupported callee %T", n.Callee)
	}
	args, err := evalArgs(n.Arguments, env)
	if err != nil {
		return nil, err
	}
	return evalFunction(ident.Value, args, env)
}

func evalBuiltin(n *ast.BuiltinNode, env *decimalEvalEnv) (any, error) {
	args, err := evalArgs(n.Arguments, env)
	if err != nil {
		return nil, err
	}
	return evalFunction(n.Name, args, env)
}

func evalArgs(nodes []ast.Node, env *decimalEvalEnv) ([]any, error) {
	out := make([]any, len(nodes))
	for i, node := range nodes {
		v, err := evalNode(node, env)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func evalFunction(name string, args []any, env *decimalEvalEnv) (any, error) {
	switch name {
	case "tier":
		if len(args) != 2 {
			return nil, fmt.Errorf("expr run error: tier expects 2 arguments")
		}
		label, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("expr run error: tier name must be a string, got %T", args[0])
		}
		cost, ok := valueAsDecimal(args[1])
		if !ok {
			return nil, fmt.Errorf("expr run error: tier value must be a number, got %T", args[1])
		}
		if env != nil && env.trace != nil {
			env.trace.MatchedTier = label
		}
		return cost, nil
	case "fixed":
		if len(args) != 1 {
			return nil, fmt.Errorf("expr run error: fixed expects 1 argument")
		}
		amount, ok := valueAsDecimal(args[0])
		if !ok {
			return nil, fmt.Errorf("expr run error: fixed amount must be a number, got %T", args[0])
		}
		if env != nil && env.trace != nil {
			env.trace.BillingUnit = BillingUnitRequest
			view := amount.InexactFloat64()
			env.trace.FixedPrice = &view
		}
		return amount.Mul(decimal.NewFromInt(1_000_000)), nil
	case requestRuleTraceFunction, requestRuleTraceIntFunction:
		if len(args) != 3 {
			return nil, fmt.Errorf("expr run error: internal trace expects 3 arguments")
		}
		index, ok := decimalAsInt(args[0])
		if !ok {
			return nil, fmt.Errorf("expr run error: internal trace index must be an integer, got %T", args[0])
		}
		matched, err := truthy(args[1])
		if err != nil {
			return nil, err
		}
		if matched && env != nil && env.trace != nil && index >= 0 && index < len(env.trace.RequestRules) {
			env.trace.RequestRules[index].Matched = true
		}
		if !matched {
			return decimal.NewFromInt(1), nil
		}
		multiplier, ok := valueAsDecimal(args[2])
		if !ok {
			return nil, fmt.Errorf("expr run error: request multiplier must be a number, got %T", args[2])
		}
		return multiplier, nil
	case "header":
		if len(args) != 1 {
			return nil, fmt.Errorf("expr run error: header expects 1 argument")
		}
		key, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("expr run error: header key must be a string, got %T", args[0])
		}
		if env == nil || env.headers == nil {
			return "", nil
		}
		return env.headers[strings.ToLower(strings.TrimSpace(key))], nil
	case "param":
		if len(args) != 1 {
			return nil, fmt.Errorf("expr run error: param expects 1 argument")
		}
		path, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("expr run error: param path must be a string, got %T", args[0])
		}
		path = strings.TrimSpace(path)
		if path == "" || env == nil || len(env.body) == 0 {
			return nil, nil
		}
		result := gjson.GetBytes(env.body, path)
		if !result.Exists() {
			return nil, nil
		}
		return normalizeMemberValue(result.Value()), nil
	case "u":
		if len(args) != 1 {
			return nil, fmt.Errorf("expr run error: u expects 1 argument")
		}
		key, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("expr run error: usage key must be a string, got %T", args[0])
		}
		if env == nil || env.usage == nil {
			return nil, nil
		}
		return normalizeMemberValue(env.usage[strings.TrimSpace(key)]), nil
	case "has":
		if len(args) != 2 {
			return nil, fmt.Errorf("expr run error: has expects 2 arguments")
		}
		substr, ok := args[1].(string)
		if !ok {
			return nil, fmt.Errorf("expr run error: has substring must be a string, got %T", args[1])
		}
		if isNilValue(args[0]) || substr == "" {
			return false, nil
		}
		return strings.Contains(fmt.Sprint(args[0]), substr), nil
	case "hour":
		tz, err := timezoneArg(args)
		if err != nil {
			return nil, err
		}
		return decimal.NewFromInt(int64(env.timeInZone(tz).Hour())), nil
	case "minute":
		tz, err := timezoneArg(args)
		if err != nil {
			return nil, err
		}
		return decimal.NewFromInt(int64(env.timeInZone(tz).Minute())), nil
	case "weekday":
		tz, err := timezoneArg(args)
		if err != nil {
			return nil, err
		}
		return decimal.NewFromInt(int64(env.timeInZone(tz).Weekday())), nil
	case "month":
		tz, err := timezoneArg(args)
		if err != nil {
			return nil, err
		}
		return decimal.NewFromInt(int64(env.timeInZone(tz).Month())), nil
	case "day":
		tz, err := timezoneArg(args)
		if err != nil {
			return nil, err
		}
		return decimal.NewFromInt(int64(env.timeInZone(tz).Day())), nil
	case "len":
		if len(args) != 1 {
			return nil, fmt.Errorf("expr run error: len expects 1 argument")
		}
		if isNilValue(args[0]) {
			return decimal.Zero, nil
		}
		rv := reflect.ValueOf(args[0])
		for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
			if rv.IsNil() {
				return decimal.Zero, nil
			}
			rv = rv.Elem()
		}
		switch rv.Kind() {
		case reflect.String:
			return decimal.NewFromInt(int64(utf8.RuneCountInString(rv.String()))), nil
		case reflect.Slice, reflect.Array, reflect.Map:
			return decimal.NewFromInt(int64(rv.Len())), nil
		default:
			return nil, fmt.Errorf("expr run error: len not supported on %T", args[0])
		}
	case "int":
		if len(args) != 1 {
			return nil, fmt.Errorf("expr run error: int expects 1 argument")
		}
		if d, ok := valueAsDecimal(args[0]); ok {
			return d.Truncate(0), nil
		}
		if s, ok := args[0].(string); ok {
			d, err := decimal.NewFromString(strings.TrimSpace(s))
			if err != nil {
				return nil, fmt.Errorf("expr run error: int cannot parse %q", s)
			}
			return d.Truncate(0), nil
		}
		if b, ok := args[0].(bool); ok {
			if b {
				return decimal.NewFromInt(1), nil
			}
			return decimal.Zero, nil
		}
		return nil, fmt.Errorf("expr run error: int cannot convert %T", args[0])
	case "float":
		if len(args) != 1 {
			return nil, fmt.Errorf("expr run error: float expects 1 argument")
		}
		if d, ok := valueAsDecimal(args[0]); ok {
			return d, nil
		}
		if s, ok := args[0].(string); ok {
			d, err := decimal.NewFromString(strings.TrimSpace(s))
			if err != nil {
				return nil, fmt.Errorf("expr run error: float cannot parse %q", s)
			}
			return d, nil
		}
		if b, ok := args[0].(bool); ok {
			if b {
				return decimal.NewFromInt(1), nil
			}
			return decimal.Zero, nil
		}
		return nil, fmt.Errorf("expr run error: float cannot convert %T", args[0])
	case "string":
		if len(args) != 1 {
			return nil, fmt.Errorf("expr run error: string expects 1 argument")
		}
		if isNilValue(args[0]) {
			return "", nil
		}
		if s, ok := args[0].(string); ok {
			return s, nil
		}
		if d, ok := args[0].(decimal.Decimal); ok {
			return d.String(), nil
		}
		return fmt.Sprint(args[0]), nil
	case "lower", "upper":
		if len(args) != 1 {
			return nil, fmt.Errorf("expr run error: %s expects 1 argument", name)
		}
		if isNilValue(args[0]) {
			return "", nil
		}
		s, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("expr run error: %s requires a string, got %T", name, args[0])
		}
		if name == "lower" {
			return strings.ToLower(s), nil
		}
		return strings.ToUpper(s), nil
	case "trim":
		if len(args) != 1 && len(args) != 2 {
			return nil, fmt.Errorf("expr run error: trim expects 1 or 2 arguments")
		}
		if isNilValue(args[0]) {
			return "", nil
		}
		s, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("expr run error: trim requires a string, got %T", args[0])
		}
		if len(args) == 1 {
			return strings.TrimSpace(s), nil
		}
		cutset, ok := args[1].(string)
		if !ok {
			return nil, fmt.Errorf("expr run error: trim cutset must be a string, got %T", args[1])
		}
		return strings.Trim(s, cutset), nil
	case "max", "min":
		if len(args) == 1 {
			rv := reflect.ValueOf(args[0])
			if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
				if rv.Len() == 0 {
					return nil, fmt.Errorf("expr run error: %s cannot operate on empty array", name)
				}
				best, ok := valueAsDecimal(normalizeMemberValue(rv.Index(0).Interface()))
				if !ok {
					return nil, fmt.Errorf("expr run error: %s requires numbers", name)
				}
				for i := 1; i < rv.Len(); i++ {
					cur, ok := valueAsDecimal(normalizeMemberValue(rv.Index(i).Interface()))
					if !ok {
						return nil, fmt.Errorf("expr run error: %s requires numbers", name)
					}
					if name == "max" {
						best = decimal.Max(best, cur)
					} else {
						best = decimal.Min(best, cur)
					}
				}
				return best, nil
			}
		}
		if len(args) < 2 {
			return nil, fmt.Errorf("expr run error: %s expects 2 arguments", name)
		}
		best, ok := valueAsDecimal(args[0])
		if !ok {
			return nil, fmt.Errorf("expr run error: %s requires numbers, got %T and %T", name, args[0], args[1])
		}
		for i := 1; i < len(args); i++ {
			cur, ok := valueAsDecimal(args[i])
			if !ok {
				return nil, fmt.Errorf("expr run error: %s requires numbers, got %T and %T", name, args[0], args[i])
			}
			if name == "max" {
				best = decimal.Max(best, cur)
			} else {
				best = decimal.Min(best, cur)
			}
		}
		return best, nil
	case "abs", "ceil", "floor":
		if len(args) != 1 {
			return nil, fmt.Errorf("expr run error: %s expects 1 argument", name)
		}
		d, ok := valueAsDecimal(args[0])
		if !ok {
			return nil, fmt.Errorf("expr run error: %s requires a number, got %T", name, args[0])
		}
		switch name {
		case "abs":
			return d.Abs(), nil
		case "ceil":
			return d.Ceil(), nil
		default:
			return d.Floor(), nil
		}
	case "round":
		if len(args) != 1 && len(args) != 2 {
			return nil, fmt.Errorf("expr run error: round expects 1 or 2 arguments")
		}
		d, ok := valueAsDecimal(args[0])
		if !ok {
			return nil, fmt.Errorf("expr run error: round requires a number, got %T", args[0])
		}
		if len(args) == 1 {
			return d.Round(0), nil
		}
		places, ok := decimalAsInt(args[1])
		if !ok || places < -16 || places > 16 {
			return nil, fmt.Errorf("expr run error: round precision must be an integer within [-16, 16]")
		}
		return d.Round(int32(places)), nil
	default:
		return nil, fmt.Errorf("expr run error: unknown function %q", name)
	}
}

func timezoneArg(args []any) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	if len(args) != 1 {
		return "", fmt.Errorf("expr run error: timezone helper expects 1 argument")
	}
	tz, ok := args[0].(string)
	if !ok {
		return "", fmt.Errorf("expr run error: timezone must be a string, got %T", args[0])
	}
	return tz, nil
}

func decimalFromFloatNode(src string, node *ast.FloatNode) decimal.Decimal {
	if d, ok := decimalFromLocation(src, node.Location()); ok {
		return d
	}
	return decimal.NewFromFloat(node.Value)
}

func decimalFromLocation(src string, loc file.Location) (decimal.Decimal, bool) {
	if loc.From < 0 || loc.To < loc.From || loc.To > len(src) {
		return decimal.Zero, false
	}
	lit := strings.ReplaceAll(src[loc.From:loc.To], "_", "")
	if lit == "" {
		return decimal.Zero, false
	}
	d, err := decimal.NewFromString(lit)
	if err != nil {
		return decimal.Zero, false
	}
	return d, true
}

func tokenParamsAsDecimals(params TokenParams, imageCount int) map[string]decimal.Decimal {
	return map[string]decimal.Decimal{
		"image_count": decimal.NewFromInt(int64(imageCount)),
		"p":           decimal.NewFromFloat(params.P),
		"c":           decimal.NewFromFloat(params.C),
		"len":         decimal.NewFromFloat(params.Len),
		"cr":          decimal.NewFromFloat(params.CR),
		"cc":          decimal.NewFromFloat(params.CC),
		"cc1h":        decimal.NewFromFloat(params.CC1h),
		"cc12h":       decimal.NewFromFloat(params.CC12h),
		"img":         decimal.NewFromFloat(params.Img),
		"img_cr":      decimal.NewFromFloat(params.ImgCR),
		"img_o":       decimal.NewFromFloat(params.ImgO),
		"ai":          decimal.NewFromFloat(params.AI),
		"ao":          decimal.NewFromFloat(params.AO),
	}
}

func valueAsDecimal(v any) (decimal.Decimal, bool) {
	switch x := v.(type) {
	case decimal.Decimal:
		return x, true
	case int:
		return decimal.NewFromInt(int64(x)), true
	case int8:
		return decimal.NewFromInt(int64(x)), true
	case int16:
		return decimal.NewFromInt(int64(x)), true
	case int32:
		return decimal.NewFromInt(int64(x)), true
	case int64:
		return decimal.NewFromInt(x), true
	case uint:
		return decimal.NewFromInt(int64(x)), true
	case uint8:
		return decimal.NewFromInt(int64(x)), true
	case uint16:
		return decimal.NewFromInt(int64(x)), true
	case uint32:
		return decimal.NewFromInt(int64(x)), true
	case uint64:
		if x > uint64(math.MaxInt64) {
			return decimal.Zero, false
		}
		return decimal.NewFromInt(int64(x)), true
	case float32:
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return decimal.Zero, false
		}
		return decimal.NewFromFloat(float64(x)), true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return decimal.Zero, false
		}
		return decimal.NewFromFloat(x), true
	default:
		return decimal.Zero, false
	}
}

func decimalAsInt(v any) (int, bool) {
	i64, ok := decimalAsInt64(v)
	if !ok {
		return 0, false
	}
	if i64 > math.MaxInt || i64 < math.MinInt {
		return 0, false
	}
	return int(i64), true
}

func decimalAsInt64(v any) (int64, bool) {
	d, ok := valueAsDecimal(v)
	if !ok || !d.Equal(d.Truncate(0)) {
		return 0, false
	}
	bi := d.BigInt()
	if !bi.IsInt64() {
		return 0, false
	}
	return bi.Int64(), true
}

func decimalAsUint64(v any) (uint64, bool) {
	d, ok := valueAsDecimal(v)
	if !ok || !d.Equal(d.Truncate(0)) || d.IsNegative() {
		return 0, false
	}
	bi := d.BigInt()
	if !bi.IsUint64() {
		return 0, false
	}
	return bi.Uint64(), true
}

func decimalFiniteNonNegative(d decimal.Decimal) bool {
	return !d.IsNegative()
}

func truthy(v any) (bool, error) {
	if isNilValue(v) {
		return false, nil
	}
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		return x != "", nil
	default:
		if d, ok := valueAsDecimal(v); ok {
			return !d.IsZero(), nil
		}
		return false, fmt.Errorf("expr run error: cannot use %T as a condition", v)
	}
}

func compareValues(op string, left, right any) (bool, error) {
	if op == "==" || op == "!=" {
		eq, ok := exactEquality(left, right)
		if ok {
			if op == "!=" {
				return !eq, nil
			}
			return eq, nil
		}
	}
	if ls, lOK := left.(string); lOK {
		if rs, rOK := right.(string); rOK {
			switch op {
			case "<":
				return ls < rs, nil
			case "<=":
				return ls <= rs, nil
			case ">":
				return ls > rs, nil
			case ">=":
				return ls >= rs, nil
			}
		}
	}
	ld, lOK := valueAsDecimal(left)
	rd, rOK := valueAsDecimal(right)
	if !lOK || !rOK {
		if op == "==" {
			return false, nil
		}
		if op == "!=" {
			return true, nil
		}
		return false, fmt.Errorf("expr run error: cannot compare %T and %T", left, right)
	}
	cmp := ld.Cmp(rd)
	switch op {
	case "==":
		return cmp == 0, nil
	case "!=":
		return cmp != 0, nil
	case "<":
		return cmp < 0, nil
	case "<=":
		return cmp <= 0, nil
	case ">":
		return cmp > 0, nil
	case ">=":
		return cmp >= 0, nil
	default:
		return false, fmt.Errorf("expr run error: unsupported operator %q", op)
	}
}

func exactEquality(left, right any) (eq bool, handled bool) {
	lNil := isNilValue(left)
	rNil := isNilValue(right)
	if lNil || rNil {
		return lNil && rNil, true
	}
	if ls, lok := left.(string); lok {
		rs, rok := right.(string)
		return rok && ls == rs, true
	}
	if _, rok := right.(string); rok {
		return false, true
	}
	if lb, lok := left.(bool); lok {
		rb, rok := right.(bool)
		return rok && lb == rb, true
	}
	if _, rok := right.(bool); rok {
		return false, true
	}
	return false, false
}
