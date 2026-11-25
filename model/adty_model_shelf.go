package model

import (
	"slices"
	"strings"

	kitreasoning "github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
)

// CatalogModelCallable reports whether live catalog rows (exact and rule-based)
// allow new calls for name, including after stripping routing modifiers and
// reasoning suffixes (R5-06 / R6-08).
// A name matching no catalog row stays callable through its channel; any
// resolved shelved row (status != 1) denies calls.
func CatalogModelCallable(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || DB == nil {
		return true
	}
	candidates := make([]string, 0, 6)
	addCandidate := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" && !slices.Contains(candidates, v) {
			candidates = append(candidates, v)
		}
	}
	addCandidate(name)
	addCandidate(kitreasoning.ParseModelModifiers(name).Base)
	for _, canonical := range hostreasoning.CanonicalBillingModelNames(name) {
		addCandidate(canonical)
	}
	base := hostreasoning.BaseModelName(name)
	addCandidate(base)
	if deepSeekBase, _, _, ok := kitreasoning.ParseDeepSeekV4ThinkingSuffix(base); ok && !model_setting.ShouldPreserveThinkingSuffix(base) {
		addCandidate(deepSeekBase)
	}
	addCandidate(ratio_setting.RoutingMatchModelName(name))

	var rows []Model
	if err := DB.Select("id", "model_name", "name_rule", "status").
		Where("name_rule <> ? OR model_name IN ?", NameRuleExact, candidates).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return false
	}
	if len(rows) == 0 {
		return true
	}
	resolved := resolveModelMetadata(rows, candidates)
	for _, cand := range candidates {
		if policy := resolved[cand]; policy != nil && policy.Status != 1 {
			return false
		}
	}
	return true
}
