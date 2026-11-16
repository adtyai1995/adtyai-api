package adtygov

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed official_features.json
var officialFeaturesJSON []byte

// OfficialFeature preserves one frozen Appendix H requirement, not its implementation status.
type OfficialFeature struct {
	FeatureID  string `json:"feature_id"`
	Title      string `json:"title"`
	Group      string `json:"group"`
	SourceLine int    `json:"source_line"`
}

// OfficialFeatures reads the versioned Appendix H catalog used by the original inventory CLI.
func OfficialFeatures() ([]OfficialFeature, error) {
	var catalog struct {
		SchemaVersion string            `json:"schema_version"`
		DocVersion    string            `json:"doc_version"`
		PlanSHA256    string            `json:"plan_sha256"`
		Features      []OfficialFeature `json:"features"`
	}
	if err := json.Unmarshal(officialFeaturesJSON, &catalog); err != nil {
		return nil, fmt.Errorf("feature catalog: %w", err)
	}
	if catalog.SchemaVersion != "2.0" || catalog.DocVersion != "v2.7-unified" || len(catalog.PlanSHA256) != 64 || len(catalog.Features) != 240 {
		return nil, fmt.Errorf("feature catalog must identify the frozen v2.7 240-feature scope")
	}
	seen := map[string]bool{}
	for _, feature := range catalog.Features {
		if feature.FeatureID == "" || feature.Title == "" || feature.Group == "" || feature.SourceLine <= 0 || seen[feature.FeatureID] {
			return nil, fmt.Errorf("feature catalog has an invalid or duplicate requirement: %s", feature.FeatureID)
		}
		seen[feature.FeatureID] = true
	}
	return catalog.Features, nil
}
