package ratio_setting

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/shopspring/decimal"
)

// ratioJSONMap keeps a float64 map for display/JSON compatibility and a
// decimal map as the financial authority. JSON ingest parses numbers with
// json.Number so literals such as 0.1 are stored exactly. Legacy float
// values are converted once at the load boundary.
type ratioJSONMap struct {
	floats   *types.RWMap[string, float64]
	decimals *types.RWMap[string, decimal.Decimal]
}

func newRatioJSONMap(floats *types.RWMap[string, float64], decimals *types.RWMap[string, decimal.Decimal]) *ratioJSONMap {
	return &ratioJSONMap{floats: floats, decimals: decimals}
}

func (m *ratioJSONMap) UnmarshalJSON(b []byte) error {
	return m.loadJSON(string(b))
}

func (m *ratioJSONMap) MarshalJSON() ([]byte, error) {
	return m.floats.MarshalJSON()
}

func (m *ratioJSONMap) loadJSON(jsonStr string) error {
	decimals, err := parseDecimalObject(jsonStr)
	if err != nil {
		return err
	}
	m.replaceAll(decimals)
	return nil
}

func (m *ratioJSONMap) replaceAll(decimals map[string]decimal.Decimal) {
	if m.decimals == nil {
		m.decimals = types.NewRWMap[string, decimal.Decimal]()
	}
	if m.floats == nil {
		m.floats = types.NewRWMap[string, float64]()
	}
	floats := make(map[string]float64, len(decimals))
	for key, value := range decimals {
		floats[key] = value.InexactFloat64()
	}
	m.decimals.ReplaceAll(decimals)
	m.floats.ReplaceAll(floats)
}

func (m *ratioJSONMap) syncFromFloats() {
	all := m.floats.ReadAll()
	decimals := make(map[string]decimal.Decimal, len(all))
	for key, value := range all {
		decimals[key] = DecimalFromFloat(value)
	}
	m.decimals.ReplaceAll(decimals)
}

func (m *ratioJSONMap) getDecimal(name string) (decimal.Decimal, bool) {
	return m.decimals.Get(name)
}

type nestedRatioJSONMap struct {
	floats   *types.RWMap[string, map[string]float64]
	decimals *types.RWMap[string, map[string]decimal.Decimal]
}

func newNestedRatioJSONMap(floats *types.RWMap[string, map[string]float64], decimals *types.RWMap[string, map[string]decimal.Decimal]) *nestedRatioJSONMap {
	return &nestedRatioJSONMap{floats: floats, decimals: decimals}
}

func (m *nestedRatioJSONMap) UnmarshalJSON(b []byte) error {
	return m.loadJSON(string(b))
}

func (m *nestedRatioJSONMap) MarshalJSON() ([]byte, error) {
	return m.floats.MarshalJSON()
}

func (m *nestedRatioJSONMap) loadJSON(jsonStr string) error {
	decimals, err := parseNestedDecimalObject(jsonStr)
	if err != nil {
		return err
	}
	m.replaceAll(decimals)
	return nil
}

func (m *nestedRatioJSONMap) replaceAll(decimals map[string]map[string]decimal.Decimal) {
	if m.decimals == nil {
		m.decimals = types.NewRWMap[string, map[string]decimal.Decimal]()
	}
	if m.floats == nil {
		m.floats = types.NewRWMap[string, map[string]float64]()
	}
	floats := make(map[string]map[string]float64, len(decimals))
	for outer, inner := range decimals {
		converted := make(map[string]float64, len(inner))
		for key, value := range inner {
			converted[key] = value.InexactFloat64()
		}
		floats[outer] = converted
	}
	m.decimals.ReplaceAll(decimals)
	m.floats.ReplaceAll(floats)
}

func (m *nestedRatioJSONMap) syncFromFloats() {
	all := m.floats.ReadAll()
	decimals := make(map[string]map[string]decimal.Decimal, len(all))
	for outer, inner := range all {
		converted := make(map[string]decimal.Decimal, len(inner))
		for key, value := range inner {
			converted[key] = DecimalFromFloat(value)
		}
		decimals[outer] = converted
	}
	m.decimals.ReplaceAll(decimals)
}

func (m *nestedRatioJSONMap) getDecimal(userGroup, usingGroup string) (decimal.Decimal, bool) {
	inner, ok := m.decimals.Get(userGroup)
	if !ok {
		return decimal.Zero, false
	}
	ratio, ok := inner[usingGroup]
	return ratio, ok
}

func parseDecimalObject(jsonStr string) (map[string]decimal.Decimal, error) {
	raw, err := decodeJSONObject(jsonStr)
	if err != nil {
		return nil, err
	}
	out := make(map[string]decimal.Decimal, len(raw))
	for key, value := range raw {
		parsed, err := decimalFromJSONValue(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		out[key] = parsed
	}
	return out, nil
}

func parseNestedDecimalObject(jsonStr string) (map[string]map[string]decimal.Decimal, error) {
	raw, err := decodeJSONObject(jsonStr)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]decimal.Decimal, len(raw))
	for outer, value := range raw {
		innerRaw, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: want a JSON object of numbers", outer)
		}
		inner := make(map[string]decimal.Decimal, len(innerRaw))
		for key, item := range innerRaw {
			parsed, err := decimalFromJSONValue(item)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", outer, key, err)
			}
			inner[key] = parsed
		}
		out[outer] = inner
	}
	return out, nil
}

func decodeJSONObject(jsonStr string) (map[string]any, error) {
	raw, err := common.DecodeJsonObjectUseNumber(jsonStr)
	if err != nil {
		if err == io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("invalid trailing data")
		}
		return nil, err
	}
	return raw, nil
}

func decimalFromJSONValue(value any) (decimal.Decimal, error) {
	switch typed := value.(type) {
	case json.Number:
		return parseBoundedRatioDecimal(string(typed))
	case nil:
		return decimal.Zero, fmt.Errorf("numeric value required")
	default:
		return decimal.Zero, fmt.Errorf("numeric value required, got %T", value)
	}
}

var maxRatioDecimalAbs = decimal.New(1, 18)

// parseBoundedRatioDecimal parses a JSON ratio/price number while blocking
// shopspring/decimal giant-exponent CPU/OOM DoS. Unlike strict currency parsing,
// it allows up to 18 fractional digits and bounded scientific notation
// (exponent in [-18, 18]) so valid float64 values such as 1.643835616438356 or
// 5e-7 round-trip cleanly (R7-01).
func parseBoundedRatioDecimal(raw string) (decimal.Decimal, error) {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 64 {
		return decimal.Zero, fmt.Errorf("invalid ratio number")
	}
	idx := 0
	if s[idx] == '+' || s[idx] == '-' {
		idx++
		if idx >= len(s) {
			return decimal.Zero, fmt.Errorf("invalid ratio number")
		}
	}
	mantissa := s[idx:]
	expPart := ""
	if ePos := strings.IndexAny(mantissa, "eE"); ePos >= 0 {
		expPart = mantissa[ePos+1:]
		mantissa = mantissa[:ePos]
		if expPart == "" {
			return decimal.Zero, fmt.Errorf("invalid ratio exponent")
		}
	}
	if mantissa == "" {
		return decimal.Zero, fmt.Errorf("invalid ratio number")
	}
	dotSeen := false
	totalDigits := 0
	fracDigits := 0
	for i := 0; i < len(mantissa); i++ {
		ch := mantissa[i]
		if ch == '.' {
			if dotSeen {
				return decimal.Zero, fmt.Errorf("invalid ratio number")
			}
			dotSeen = true
			continue
		}
		if ch < '0' || ch > '9' {
			return decimal.Zero, fmt.Errorf("invalid ratio number")
		}
		totalDigits++
		if dotSeen {
			fracDigits++
		}
	}
	if totalDigits == 0 || totalDigits > 24 || fracDigits > 18 {
		return decimal.Zero, fmt.Errorf("ratio precision exceeds limit")
	}
	if expPart != "" {
		eIdx := 0
		if expPart[eIdx] == '+' || expPart[eIdx] == '-' {
			eIdx++
		}
		if eIdx >= len(expPart) || len(expPart)-eIdx > 4 {
			return decimal.Zero, fmt.Errorf("ratio exponent out of range")
		}
		for i := eIdx; i < len(expPart); i++ {
			if expPart[i] < '0' || expPart[i] > '9' {
				return decimal.Zero, fmt.Errorf("invalid ratio exponent")
			}
		}
		expVal, err := strconv.Atoi(expPart)
		if err != nil || expVal < -18 || expVal > 18 {
			return decimal.Zero, fmt.Errorf("ratio exponent out of range")
		}
	}
	parsed, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, err
	}
	if parsed.Exponent() < -36 || parsed.Exponent() > 36 || parsed.Abs().GreaterThan(maxRatioDecimalAbs) {
		return decimal.Zero, fmt.Errorf("ratio magnitude out of range")
	}
	return parsed, nil
}

// ValidateRatioFloat checks that a float64 ratio/price is finite, non-negative,
// and representable by parseBoundedRatioDecimal once JSON-encoded (R7-01).
func ValidateRatioFloat(v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return fmt.Errorf("ratio must be a finite, non-negative number")
	}
	_, err := parseBoundedRatioDecimal(strconv.FormatFloat(v, 'g', -1, 64))
	return err
}

// ValidateRatioJSONObject pre-validates a flat JSON object of non-negative
// pricing ratios before persisting it to options (R7-01).
func ValidateRatioJSONObject(jsonStr string) error {
	parsed, err := parseDecimalObject(jsonStr)
	if err != nil {
		return err
	}
	for name, ratio := range parsed {
		if ratio.IsNegative() {
			return fmt.Errorf("%s: ratio must be non-negative", name)
		}
	}
	return nil
}
