package adtygov

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type designCard struct {
	FeatureID         string           `json:"feature_id"`
	RequirementRefs   []string         `json:"requirement_refs"`
	CurrentState      string           `json:"current_state"`
	SourceSymbols     []map[string]any `json:"source_symbols"`
	TestFuncNames     []string         `json:"test_func_names"`
	DBMoneyInvariants []string         `json:"db_money_invariants"`
	EvidenceID        string           `json:"evidence_id"`
	NAReason          string           `json:"na_reason"`
}

type catalogFeature struct {
	FeatureID         string           `json:"feature_id"`
	Title             string           `json:"title"`
	Group             string           `json:"group"`
	SourceLine        int              `json:"source_line"`
	FTRID             string           `json:"ftr_id"`
	CurrentState      string           `json:"current_state"`
	ExecutionStatus   string           `json:"execution_status"`
	ExistenceOnly     bool             `json:"existence_only"`
	RequirementRefs   []string         `json:"requirement_refs"`
	SourceSymbols     []map[string]any `json:"source_symbols"`
	TestFuncNames     []string         `json:"test_func_names"`
	DBMoneyInvariants []string         `json:"db_money_invariants"`
	EvidenceID        string           `json:"evidence_id"`
	NAReason          string           `json:"na_reason"`
	DesignCard        *designCard      `json:"design_card"`
}

type featureCatalog struct {
	SchemaVersion string           `json:"schema_version"`
	DocVersion    string           `json:"doc_version"`
	PlanSHA256    string           `json:"plan_sha256"`
	Features      []catalogFeature `json:"features"`
}

func ftrIDFor(featureID string) (string, error) {
	var group, index int
	if _, err := fmt.Sscanf(featureID, "ADTY-%d-%d", &group, &index); err != nil {
		return "", err
	}
	if group < 1 || group > 24 || index < 1 || index > 10 {
		return "", fmt.Errorf("feature %s is outside H.01-H.24", featureID)
	}
	return fmt.Sprintf("FTR-%03d", (group-1)*10+index), nil
}

func TestOfficialFeatureCardsStayNotExecutableUntilEvidence(t *testing.T) {
	raw, err := os.ReadFile("official_features.json")
	require.NoError(t, err)
	var catalog featureCatalog
	require.NoError(t, json.Unmarshal(raw, &catalog))
	require.Equal(t, "2.0", catalog.SchemaVersion)
	require.Equal(t, "v2.7-unified", catalog.DocVersion)
	require.Len(t, catalog.PlanSHA256, 64)
	require.Len(t, catalog.Features, 240)

	seenFeature := map[string]struct{}{}
	seenFTR := map[string]struct{}{}
	states := map[string]struct{}{
		"implemented": {},
		"partial":     {},
		"missing":     {},
		"conflict":    {},
		"retired":     {},
		"unknown":     {},
	}
	for _, feature := range catalog.Features {
		require.NotContains(t, seenFeature, feature.FeatureID)
		seenFeature[feature.FeatureID] = struct{}{}
		wantFTR, err := ftrIDFor(feature.FeatureID)
		require.NoError(t, err)
		require.Equal(t, wantFTR, feature.FTRID)
		require.NotContains(t, seenFTR, feature.FTRID)
		seenFTR[feature.FTRID] = struct{}{}
		require.NotEmpty(t, feature.Title)
		require.NotEmpty(t, feature.Group)
		require.Positive(t, feature.SourceLine)
		_, ok := states[feature.CurrentState]
		require.True(t, ok, feature.FeatureID)
		require.Equal(t, "not_executable", feature.ExecutionStatus, feature.FeatureID)
		require.False(t, feature.ExistenceOnly, feature.FeatureID)
		require.Empty(t, feature.EvidenceID, feature.FeatureID)
		require.NotEmpty(t, strings.TrimSpace(feature.NAReason), feature.FeatureID)
		require.NotNil(t, feature.DesignCard, feature.FeatureID)
		card := feature.DesignCard
		require.Equal(t, feature.FeatureID, card.FeatureID)
		require.Equal(t, feature.CurrentState, card.CurrentState)
		require.Equal(t, feature.RequirementRefs, card.RequirementRefs)
		require.Equal(t, feature.TestFuncNames, card.TestFuncNames)
		require.Equal(t, feature.DBMoneyInvariants, card.DBMoneyInvariants)
		require.Empty(t, card.EvidenceID)
		require.Equal(t, feature.NAReason, card.NAReason)
		require.NotNil(t, card.SourceSymbols)
		require.NotNil(t, card.RequirementRefs)
		require.NotNil(t, card.TestFuncNames)
		require.NotNil(t, card.DBMoneyInvariants)
	}

	for group := 1; group <= 24; group++ {
		for index := 1; index <= 10; index++ {
			_, ok := seenFeature[fmt.Sprintf("ADTY-%02d-%02d", group, index)]
			require.True(t, ok)
		}
	}
	conflict := featureByID(t, catalog.Features, "ADTY-08-03")
	require.Equal(t, "conflict", conflict.CurrentState)
	require.Empty(t, conflict.TestFuncNames)
	for _, id := range []string{"ADTY-24-09", "ADTY-24-10"} {
		feature := featureByID(t, catalog.Features, id)
		require.Empty(t, feature.TestFuncNames)
		require.Equal(t, "not_executable", feature.ExecutionStatus)
	}
}

func featureByID(t *testing.T, features []catalogFeature, id string) catalogFeature {
	t.Helper()
	for _, feature := range features {
		if feature.FeatureID == id {
			return feature
		}
	}
	t.Fatalf("missing %s", id)
	return catalogFeature{}
}
