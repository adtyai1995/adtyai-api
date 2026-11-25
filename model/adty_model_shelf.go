package model

import (
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
)

// CatalogModelCallable reports whether live catalog rows (exact and rule-based)
// allow new calls for name, including after stripping routing modifiers and
// reasoning suffixes (R5-06).
// A name matching no catalog row stays callable through its channel; any
// resolved shelved row (status != 1) denies calls.
func CatalogModelCallable(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || DB == nil {
		return true
	}
	candidates := make([]string, 0, 3)
	addCandidate := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" && !slices.Contains(candidates, v) {
			candidates = append(candidates, v)
		}
	}
	addCandidate(name)
	addCandidate(hostreasoning.BaseModelName(name))
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
