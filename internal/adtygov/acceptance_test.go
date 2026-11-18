package adtygov

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOfficialH24GovernanceLanding(t *testing.T) {
	root := repoRoot(t)

	require.True(t, HasLandedEntry(CapInventory))
	require.True(t, HasLandedEntry(CapRuleMapping))
	require.True(t, HasLandedEntry(CapGlobalReview))
	require.True(t, HasLandedEntry(CapIssueManifest))
	require.True(t, HasLandedEntry(CapRCAFix))
	require.True(t, HasLandedEntry(CapCleanup))
	require.True(t, HasLandedEntry(CapRereview))
	require.True(t, HasLandedEntry(CapRetiredChain), "ADTY-24-06 inventory is landed in-repo")
	require.Equal(t, []string{CapRetiredChain, CapSmoke, CapScale}, NotClaimedCapabilities)
	for _, id := range NotClaimedCapabilities {
		require.False(t, ClaimedClosed(id), "%s must not be claimed closed", id)
	}
	require.False(t, HasLandedEntry(CapSmoke))
	require.False(t, HasLandedEntry(CapScale))

	t.Run("ADTY-24-01 inventory", func(t *testing.T) {
		units, err := EnumerateUnits(root)
		require.NoError(t, err)
		require.NotEmpty(t, units)

		var voucher *Unit
		for i := range units {
			if units[i].Path == "service/adty_voucher.go" {
				voucher = &units[i]
				break
			}
		}
		require.NotNil(t, voucher, "generator must emit the current path service/adty_voucher.go")
		require.Equal(t, "WU-BE-service-adty_voucher", voucher.UnitID)
		require.Equal(t, "code", voucher.Kind)
		require.NotEmpty(t, voucher.Dims)
		require.NotEmpty(t, voucher.Slice)
		require.True(t, strings.HasPrefix(voucher.SourceHash, "sha256:"))
		require.Len(t, voucher.SourceHash, len("sha256:")+64)
		require.Equal(t, "pending", voucher.State)

		var feature *Unit
		for i := range units {
			if units[i].Path == "web/src/features/adty-notifications" {
				feature = &units[i]
				break
			}
		}
		require.NotNil(t, feature, "generator must emit web/src/features/adty-notifications")
		require.Equal(t, "WU-FE-adty-notifications", feature.UnitID)
		require.Equal(t, "GEN-FE", feature.Slice)
	})

	t.Run("ADTY-24-02 rule mapping", func(t *testing.T) {
		rules, err := OfficialRuleMappings()
		require.NoError(t, err)
		byID := map[string]RuleMapping{}
		for _, rule := range rules {
			byID[rule.RuleID] = rule
			require.NotEmpty(t, rule.Title)
			require.Equal(t, "volume1", rule.Source)
			require.Equal(t, officialCheckOrder, rule.Checkpoints)
		}
		require.Contains(t, byID, "G.8")
		require.Contains(t, byID, "A.18.1")
		require.Contains(t, byID, "G.13")
		require.GreaterOrEqual(t, len(rules), 90)
	})

	t.Run("ADTY-24-03 review runner", func(t *testing.T) {
		checks, err := OfficialChecks()
		require.NoError(t, err)
		got := make([]string, 0, len(checks))
		for _, check := range checks {
			got = append(got, check.ID)
		}
		require.Equal(t, officialCheckOrder, got)

		run, err := NewReviewRun("h24-landing")
		require.NoError(t, err)
		require.Equal(t, CapGlobalReview, run.CapabilityID)
		require.Equal(t, ReviewStatusRecordedNotExecuted, run.Status)
		require.False(t, run.FullReviewPassed)
		require.Equal(t, officialCheckOrder, run.Checks)

		var buf bytes.Buffer
		require.NoError(t, WriteFindingsJSONL(&buf, []Finding{{
			FindingID: "F-EXAMPLE-PENDING",
			UnitID:    "WU-BE-service-adty_voucher",
			CheckID:   "CHK-FUND",
			Result:    FindingResultPending,
			Path:      "service/adty_voucher.go",
			Evidence:  "recorded, not a full-review pass",
		}}))
		findings, err := ReadFindingsJSONL(&buf)
		require.NoError(t, err)
		require.Len(t, findings, 1)
		require.Equal(t, FindingResultPending, findings[0].Result)
	})

	t.Run("ADTY-24-04 issue manifest", func(t *testing.T) {
		raw := `{
			"issue_id":"ISS-EXAMPLE-0001","type":"confirmed_defect","severity":"P3",
			"rule_id":"A.18.1","path":"service/adty_remaining_gaps.go",
			"expected":"delete behavior keeps an audit record",
			"actual":"no official audit","root_cause":"hide path skipped the official writer",
			"status":"confirmed"
		}`
		records, err := ValidateIssueManifest(strings.NewReader(compactJSONL(raw)))
		require.NoError(t, err)
		require.Equal(t, []string{"ISS-EXAMPLE-0001"}, ListIssueIDs(records))
		_, err = ValidateIssueManifest(strings.NewReader(`{"issue_id":"x"}`))
		require.Error(t, err)
	})

	t.Run("ADTY-24-05 rca linkage", func(t *testing.T) {
		rec := RCARecord{
			IssueID:     "ISS-EXAMPLE-0001",
			RootCause:   "official writer was not called on hide",
			FixEvidence: "acceptance covers first hide writes one official audit",
			Commit:      "0000000000000000000000000000000000000000",
		}
		require.NoError(t, ValidateRCA(rec))
		require.Error(t, ValidateRCA(RCARecord{IssueID: "ISS-EXAMPLE-0001"}))
	})

	t.Run("ADTY-24-07 cleanup campaign", func(t *testing.T) {
		campaign := OfficialCleanupCampaign()
		require.Equal(t, CapCleanup, campaign.CapabilityID)
		require.False(t, campaign.Executed)
		require.False(t, campaign.MassDeleted)
		require.Contains(t, strings.Join(campaign.Tools, "\n"), "knip")
	})

	t.Run("ADTY-24-08 rereview entry", func(t *testing.T) {
		entry := NewRereviewEntry()
		require.Equal(t, CapRereview, entry.CapabilityID)
		require.NotEmpty(t, entry.InvokedAs)
		require.False(t, entry.PhaseDExecuted)
		require.False(t, entry.ProjectConverged)
		require.False(t, entry.SmokeClaimed)
		require.False(t, entry.ScaleClaimed)
	})

	t.Run("ADTY-24-06 retired inventory matches current source tree", func(t *testing.T) {
		inv := OfficialRetiredChainInventory()
		var desktop *RetiredObject
		for i := range inv.Objects {
			obj := &inv.Objects[i]
			for _, rel := range obj.VerifiedAbsent {
				_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
				require.True(t, os.IsNotExist(err), "verified_absent path %s for %s must not exist in current tree", rel, obj.ID)
			}
			if obj.ID == "RETIRE-desktop-electron" {
				desktop = obj
			}
		}
		require.NotNil(t, desktop)
		require.Contains(t, desktop.RelatedIssues, "ISS-R1-0063")
		require.Contains(t, desktop.Leftover, "database-step.tsx")
		require.Contains(t, desktop.Leftover, "window.electron")
		require.Contains(t, desktop.Leftover, "no live window.electron leftover")
		require.NotContains(t, desktop.Leftover, "still reads")
		require.NotContains(t, desktop.Notes, "still reads")

		setupDir := filepath.Join(root, "web", "src", "features", "setup")
		err := filepath.Walk(setupDir, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info.IsDir() {
				return walkErr
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			text := strings.ToLower(string(raw))
			require.NotContains(t, text, "window.electron", "%s must not contain window.electron", path)
			require.NotContains(t, text, "electron", "%s must not reference electron", path)
			return nil
		})
		require.NoError(t, err)
	})
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "go.mod not found from %s", dir)
		dir = parent
	}
}

func compactJSONL(raw string) string {
	var rec IssueRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return raw
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return raw
	}
	return string(b) + "\n"
}

// 正式分母必须由同一个枚举入口生成，功能和规则不得由手工数字替代。
func TestEnumerateUnitsIncludesFrozenFeatureAndRuleDenominators(t *testing.T) {
	units, err := EnumerateUnits(repoRoot(t))
	require.NoError(t, err)
	byKind := map[string]map[string]Unit{}
	seen := map[string]bool{}
	for _, unit := range units {
		require.False(t, seen[unit.UnitID], "unit IDs must be unique")
		seen[unit.UnitID] = true
		if byKind[unit.Kind] == nil {
			byKind[unit.Kind] = map[string]Unit{}
		}
		byKind[unit.Kind][unit.UnitID] = unit
	}
	t.Run("all 240 official features", func(t *testing.T) {
		require.Len(t, byKind["feature"], 240)
		for group := 1; group <= 24; group++ {
			for item := 1; item <= 10; item++ {
				id := fmt.Sprintf("WU-FEATURE-ADTY-%02d-%02d", group, item)
				require.Contains(t, byKind["feature"], id)
				require.NotEmpty(t, byKind["feature"][id].Dims)
				require.NotEmpty(t, byKind["feature"][id].SourceHash)
			}
		}
	})
	t.Run("every official G and A rule", func(t *testing.T) {
		rules, err := OfficialRuleMappings()
		require.NoError(t, err)
		require.Len(t, byKind["rule"], len(rules))
		for _, rule := range rules {
			require.Contains(t, byKind["rule"], "WU-RULE-"+rule.RuleID)
		}
	})
}

func TestEnumerateUnitsIncludesCrossLayerAssets(t *testing.T) {
	units, err := EnumerateUnits(repoRoot(t))
	require.NoError(t, err)
	paths := map[string]bool{}
	for _, unit := range units {
		paths[unit.Path] = true
	}
	for _, path := range []string{
		".github/workflows/ci.yml", "docker-compose.yml", "scripts/adty/check.sh",
		"web/src/components/copy-button.tsx", "model/adty_topup_order_test.go",
		"middleware/testdata/adty_admin_routes.txt", "LICENSE", "go.mod",
	} {
		require.True(t, paths[path], "review denominator must cover %s", path)
	}
	foundTaskPlugin := false
	for path := range paths {
		if strings.HasPrefix(path, "plugins/tasks/") && strings.HasSuffix(path, ".js") {
			foundTaskPlugin = true
		}
	}
	require.True(t, foundTaskPlugin, "task plugin source must be in the denominator")
}

func TestEnumerateUnitsKeepsVendoredReviewInstructions(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n"), 0o644))
	instructions := ".agents/skills/shadcn-ui/vendor/shadcn/README.md"
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, instructions)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, instructions), []byte("Required component reuse instructions\n"), 0o644))
	units, err := EnumerateUnits(root)
	require.NoError(t, err)
	var found bool
	for _, unit := range units {
		if unit.Path == instructions {
			found = true
			require.NotEmpty(t, unit.SourceHash)
		}
	}
	require.True(t, found, "vendored review instructions are repository assets, not installed dependencies")
}

func TestEnumerateUnitsAssignsDomainAndMigrationToS13(t *testing.T) {
	units, err := EnumerateUnits(repoRoot(t))
	require.NoError(t, err)
	owners := map[string]string{}
	for _, unit := range units {
		owners[unit.Path] = unit.AuditSlice
	}
	for _, path := range []string{"model/adty_site_domain.go", "model/adty_migrate.go"} {
		require.Equal(t, "S13", owners[path], "plan 3.1 assigns domain and migration assets to S13")
	}
}

// 文件路径覆盖不能代替3.2.1要求的package与注册组独立身份。
func TestEnumerateUnitsRetainsFilesAndEmitsPackagesAndRouterGroups(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":             "module fixture\n",
		"main.go":            "package main\nfunc main() {}\n",
		"service/fixture.go": "package service\nfunc Work() {}\n",
		"router/routes.go": `package router
func Register(apiRouter *Router) {
	// ignored.Group("/comment")
	users := apiRouter.Group("/users", UserAuth())
	users.Group(
		"/settings",
		WriteAuth(),
	)
	Handle(apiRouter.Group("", AdminAuth()))
}

`,
	}
	for path, content := range files {
		full := filepath.Join(root, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	units, err := EnumerateUnits(root)
	require.NoError(t, err)
	packages := map[string]Unit{}
	var groups []Unit
	var originalRouterFile bool
	for _, unit := range units {
		switch unit.Slice {
		case "GEN-PKG":
			packages[unit.Path] = unit
		case "GEN-AUTH-GROUP":
			groups = append(groups, unit)
		case "GEN-AUTH":
			originalRouterFile = originalRouterFile || unit.Path == "router/routes.go"
		}
	}
	require.True(t, originalRouterFile, "原文件级单元必须保留")
	t.Run("native Go packages", func(t *testing.T) {
		require.Len(t, packages, 3, "根目录和两个子目录均须有原生Go package单元")
		for _, path := range []string{".", "service", "router"} {
			require.Contains(t, packages, path)
			require.NotEmpty(t, packages[path].SourceHash)
		}
	})
	t.Run("native router groups", func(t *testing.T) {
		require.Len(t, groups, 3, "注释不算组；多行和嵌入调用的Group不能遗漏")
		seen := map[string]bool{}
		for _, group := range groups {
			require.False(t, seen[group.UnitID])
			seen[group.UnitID] = true
			require.Equal(t, "router/routes.go", group.Path)
			require.Equal(t, "B00", group.AuditSlice)
			require.Positive(t, group.SourceLine)
			require.Contains(t, group.Title, "Register")
			require.Contains(t, group.Title, ".Group(")
			require.NotEmpty(t, group.SourceHash)
		}
	})
	before := packages["service"].SourceHash
	require.NoError(t, os.WriteFile(filepath.Join(root, "service/fixture.go"), []byte("package service\nfunc Work() { panic(\"changed\") }\n"), 0o644))
	updated, err := EnumerateUnits(root)
	require.NoError(t, err)
	found := false
	for _, unit := range updated {
		if unit.Slice == "GEN-PKG" && unit.Path == "service" {
			found = true
			require.NotEqual(t, before, unit.SourceHash, "包内代码变化必须使该包来源摘要失效")
		}
	}
	require.True(t, found, "更新后仍须保留该package单元")
}

func TestEnumerateUnitsKeepsTestOnlyPackagesAndAllRegistrationDeclarations(t *testing.T) {
	cases := []struct {
		name     string
		files    map[string]string
		packages []string
		groups   int
	}{
		{name: "test-only external-test and nested-module packages", files: map[string]string{
			"only/fixture_test.go":  "package only\n",
			"mixed/fixture.go":      "package mixed\n",
			"mixed/fixture_test.go": "package mixed_test\n",
			"nested/go.mod":         "module nested\n",
			"nested/fixture.go":     "package nested\n",
		}, packages: []string{"Go package only (only)", "Go package mixed (mixed)", "Go package mixed (mixed_test)", "Go package nested (nested)"}},
		{name: "package-level registration function", files: map[string]string{
			"router/routes.go": "package router\nvar Register = func(r *Router) { r.Group(\"/global\") }\n",
		}, packages: []string{"Go package router (router)"}, groups: 1},
		{name: "same method name on distinct receivers", files: map[string]string{
			"router/routes.go": "package router\nfunc (s *First) Register(r *Router) { r.Group(\"/same\") }\nfunc (s *Second) Register(r *Router) { r.Group(\"/same\") }\n",
		}, packages: []string{"Go package router (router)"}, groups: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n"), 0o644))
			for path, content := range tc.files {
				full := filepath.Join(root, path)
				require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
				require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
			}
			units, err := EnumerateUnits(root)
			require.NoError(t, err)
			var packages []string
			groups := 0
			for _, unit := range units {
				if unit.Slice == "GEN-PKG" {
					packages = append(packages, unit.Title)
				}
				if unit.Slice == "GEN-AUTH-GROUP" {
					groups++
				}
			}
			require.ElementsMatch(t, tc.packages, packages)
			require.Equal(t, tc.groups, groups)
		})
	}
}
