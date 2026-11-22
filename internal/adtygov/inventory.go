package adtygov

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Unit is one plan 3.2.1 units.jsonl record generated from the current tree.
// This generator does not load or rewrite the external frozen denominator.
type Unit struct {
	UnitID     string   `json:"unit_id"`
	Kind       string   `json:"kind"`
	Path       string   `json:"path"`
	Dims       []string `json:"dims"`
	Slice      string   `json:"slice"`
	SourceHash string   `json:"source_hash"`
	State      string   `json:"state"`
	AuditSlice string   `json:"audit_slice,omitempty"`
	FeatureID  string   `json:"feature_id,omitempty"`
	RuleID     string   `json:"rule_id,omitempty"`
	Title      string   `json:"title,omitempty"`
	SourceLine int      `json:"source_line,omitempty"`
}

var backendDims = []string{"callchain", "tx", "perm", "error", "fund", "cache"}
var frontendDims = []string{"callchain", "perm", "error"}
var authDims = []string{"callchain", "perm"}
var dbDims = []string{"tx", "fund", "error"}

var skipInventoryDirNames = map[string]bool{
	".git":              true,
	"node_modules":      true,
	"dist":              true,
	"coverage":          true,
	".cache":            true,
	".rsbuild":          true,
	"test-results":      true,
	"playwright-report": true,
}

// EnumerateUnits walks the product repository and emits current review units.
func EnumerateUnits(root string) ([]Unit, error) {
	return enumerateUnits(root, nil)
}

func enumerateUnits(root string, scope *gitPlanScope) ([]Unit, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return nil, fmt.Errorf("repository root must contain go.mod: %w", err)
	}

	var units []Unit
	packages := map[string]*inventoryPackage{}
	seen := map[string]bool{}
	add := func(u Unit) error {
		if u.Path == "" || u.UnitID == "" || seen[u.UnitID] {
			return fmt.Errorf("inventory unit is invalid or duplicated: %s (%s)", u.UnitID, u.Path)
		}
		seen[u.UnitID] = true
		if u.AuditSlice == "" {
			u.AuditSlice = auditSliceForPath(u.Path)
		}
		units = append(units, u)
		return nil
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if scope != nil && rel != "." && !scope.paths[rel] {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if scope == nil && rel != "." && (skipInventoryDirNames[name] ||
				(strings.HasPrefix(name, ".") && rel != ".github" && rel != ".agents")) {
				return fs.SkipDir
			}
			if isFeatureDir(rel) {
				unit, err := featureDirUnit(root, rel, scope)
				if err != nil {
					return err
				}
				if err := add(unit); err != nil {
					return err
				}
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("inventory requires explicit review of symlink scope: %s", rel)
		}
		if strings.HasSuffix(rel, ".go") {
			unit, err := goFileUnit(root, rel)
			if err != nil {
				return err
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly)
			if err != nil {
				return fmt.Errorf("inventory Go package %s: %w", rel, err)
			}
			dir := filepath.ToSlash(filepath.Dir(rel))
			key := dir + "\x00" + parsed.Name.Name
			if packages[key] == nil {
				packages[key] = &inventoryPackage{path: dir, name: parsed.Name.Name}
			}
			packages[key].files = append(packages[key].files, unit)
			if !strings.HasSuffix(rel, "_test.go") {
				if err := add(unit); err != nil {
					return err
				}
				if strings.HasPrefix(rel, "router/") {
					groups, err := routerGroupUnits(root, unit)
					if err != nil {
						return err
					}
					for _, group := range groups {
						if err := add(group); err != nil {
							return err
						}
					}
				}
				return nil
			}
		}
		// Feature-directory units already include every nested source and fixture in their hash.
		if strings.HasPrefix(rel, "web/src/features/") {
			return nil
		}
		sum, err := hashFile(path)
		if err != nil {
			return err
		}
		pathHash := sha256.Sum256([]byte(rel))
		return add(Unit{UnitID: fmt.Sprintf("WU-ASSET-%s-%x", strings.NewReplacer("/", "-", ".", "_").Replace(rel), pathHash[:4]),
			Kind: "code", Path: rel, Dims: append([]string(nil), backendDims...), Slice: "GEN-ASSET", SourceHash: sum, State: "pending"})
	})
	if err != nil {
		return nil, err
	}
	for _, pkg := range packages {
		sort.Slice(pkg.files, func(i, j int) bool { return pkg.files[i].Path < pkg.files[j].Path })
		h := sha256.New()
		for _, file := range pkg.files {
			fmt.Fprintf(h, "%s %s\n", filepath.Base(file.Path), file.SourceHash)
		}
		identity := sha256.Sum256([]byte(pkg.path + "\x00" + pkg.name))
		pathID := strings.NewReplacer("/", "-", ".", "root").Replace(pkg.path)
		if err := add(Unit{UnitID: fmt.Sprintf("WU-PKG-%s-%s-%x", pathID, pkg.name, identity[:4]),
			Kind: "code", Path: pkg.path, Dims: append([]string(nil), backendDims...), Slice: "GEN-PKG",
			AuditSlice: "B00", Title: "Go package " + pkg.path + " (" + pkg.name + ")",
			SourceHash: fmt.Sprintf("sha256:%x", h.Sum(nil)), State: "pending"}); err != nil {
			return nil, err
		}
	}
	if scope != nil {
		for _, unit := range scope.additional {
			if err := add(unit); err != nil {
				return nil, err
			}
		}
		sort.Slice(units, func(i, j int) bool { return units[i].UnitID < units[j].UnitID })
		return units, nil
	}
	features, err := OfficialFeatures()
	if err != nil {
		return nil, err
	}
	checks := []string{"callchain", "tx", "perm", "idemp", "fund", "error", "retire"}
	featureHash := sha256.Sum256(officialFeaturesJSON)
	for _, feature := range features {
		if err := add(Unit{UnitID: "WU-FEATURE-" + feature.FeatureID, Kind: "feature", Path: "internal/adtygov/official_features.json",
			FeatureID: feature.FeatureID, Title: feature.Title, SourceLine: feature.SourceLine,
			Dims: append([]string(nil), checks...), Slice: "FEATURE", AuditSlice: featureAuditSlice(feature.FeatureID),
			SourceHash: fmt.Sprintf("sha256:%x", featureHash), State: "pending"}); err != nil {
			return nil, err
		}
	}
	rules, err := OfficialRuleMappings()
	if err != nil {
		return nil, err
	}
	ruleHash := sha256.Sum256(officialRuleIDsJSONL)
	for _, rule := range rules {
		if err := add(Unit{UnitID: "WU-RULE-" + rule.RuleID, Kind: "rule", Path: "internal/adtygov/official_rule_ids.jsonl",
			RuleID: rule.RuleID, Title: rule.Title, Dims: append([]string(nil), checks...), Slice: "RULE", AuditSlice: "B00",
			SourceHash: fmt.Sprintf("sha256:%x", ruleHash), State: "pending"}); err != nil {
			return nil, err
		}
	}
	sort.Slice(units, func(i, j int) bool { return units[i].UnitID < units[j].UnitID })
	return units, nil
}

type inventoryPackage struct {
	path  string
	name  string
	files []Unit
}

// routerGroupUnits keeps each registration expression, including nested and multiline calls.
// The parent file remains a separate unit so Use calls and inherited authentication are reviewed too.
func routerGroupUnits(root string, file Unit) ([]Unit, error) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(file.Path)), nil, 0)
	if err != nil {
		return nil, fmt.Errorf("inventory router groups %s: %w", file.Path, err)
	}
	var units []Unit
	type registrationScope struct {
		name string
		node ast.Node
	}
	var scopes []registrationScope
	for _, declaration := range parsed.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			if declaration.Body == nil {
				continue
			}
			name := declaration.Name.Name
			if declaration.Recv != nil {
				var receiver bytes.Buffer
				if err := format.Node(&receiver, fset, declaration.Recv.List[0].Type); err != nil {
					return nil, fmt.Errorf("inventory router receiver %s: %w", file.Path, err)
				}
				name = "(" + receiver.String() + ")." + name
			}
			scopes = append(scopes, registrationScope{name, declaration.Body})
		case *ast.GenDecl:
			if declaration.Tok != token.VAR {
				continue
			}
			for _, spec := range declaration.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				var names []string
				for _, name := range value.Names {
					names = append(names, name.Name)
				}
				scopes = append(scopes, registrationScope{"var " + strings.Join(names, ","), value})
			}
		}
	}
	for _, scope := range scopes {
		occurrences := map[string]int{}
		var formatErr error
		ast.Inspect(scope.node, func(node ast.Node) bool {
			if formatErr != nil {
				return false
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Group" || len(call.Args) == 0 {
				return true
			}
			var receiver, path, expression bytes.Buffer
			for _, output := range []struct {
				buffer *bytes.Buffer
				node   ast.Node
			}{{&receiver, selector.X}, {&path, call.Args[0]}, {&expression, call}} {
				if formatErr = format.Node(output.buffer, fset, output.node); formatErr != nil {
					return false
				}
			}
			key := scope.name + "\x00" + receiver.String() + "\x00" + path.String()
			occurrences[key]++
			identity := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", file.Path, key, occurrences[key])))
			units = append(units, Unit{UnitID: fmt.Sprintf("WU-AUTH-GROUP-%x", identity[:12]),
				Kind: "code", Path: file.Path, Dims: append([]string(nil), authDims...), Slice: "GEN-AUTH-GROUP",
				AuditSlice: "B00", Title: scope.name + ": " + expression.String(),
				SourceLine: fset.Position(call.Pos()).Line, SourceHash: file.SourceHash, State: "pending"})
			return true
		})
		if formatErr != nil {
			return nil, fmt.Errorf("inventory router expression %s: %w", file.Path, formatErr)
		}
	}
	return units, nil
}

func isFeatureDir(rel string) bool {
	const prefix = "web/src/features/"
	if !strings.HasPrefix(rel, prefix) {
		return false
	}
	rest := strings.TrimPrefix(rel, prefix)
	return rest != "" && !strings.Contains(rest, "/")
}

func goFileUnit(root, rel string) (Unit, error) {
	sum, err := hashFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return Unit{}, err
	}
	stem := strings.TrimSuffix(filepath.Base(rel), ".go")
	dir := filepath.ToSlash(filepath.Dir(rel))
	kind := "code"
	dims := backendDims
	slice := "GEN-BE"
	prefix := "WU-BE"
	switch {
	case dir == "router" || strings.HasPrefix(dir, "router/"):
		prefix = "WU-AUTH"
		dims = authDims
		slice = "GEN-AUTH"
	case isMigrationPath(rel):
		prefix = "WU-DB"
		dims = dbDims
		slice = "GEN-DB"
	}
	idDir := strings.ReplaceAll(dir, "/", "-")
	if idDir == "." {
		idDir = "root"
	}
	return Unit{
		UnitID:     fmt.Sprintf("%s-%s-%s", prefix, idDir, stem),
		Kind:       kind,
		Path:       rel,
		Dims:       append([]string(nil), dims...),
		Slice:      slice,
		SourceHash: sum,
		State:      "pending",
	}, nil
}

func isMigrationPath(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	return strings.Contains(base, "migrate") || strings.Contains(base, "migration")
}

func featureDirUnit(root, rel string, scope *gitPlanScope) (Unit, error) {
	sum, err := hashTree(filepath.Join(root, filepath.FromSlash(rel)), rel, scope)
	if err != nil {
		return Unit{}, err
	}
	name := filepath.Base(rel)
	return Unit{
		UnitID:     "WU-FE-" + name,
		Kind:       "code",
		Path:       rel,
		Dims:       append([]string(nil), frontendDims...),
		Slice:      "GEN-FE",
		SourceHash: sum,
		State:      "pending",
	}, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil)), nil
}

func hashTree(dir, featurePath string, scope *gitPlanScope) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if scope != nil && !scope.paths[filepath.ToSlash(filepath.Join(featurePath, rel))] {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if scope == nil && skipInventoryDirNames[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("inventory requires explicit review of symlink scope: %s", path)
		}
		sum, err := hashFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s %s\n", filepath.ToSlash(rel), sum)
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil)), nil
}

// auditSliceForPath assigns one official vertical owner; GEN-* stays as the legacy inventory category.
// Cross-cutting H01-H04 reviews still inspect their complete scope in addition to these owners.
func auditSliceForPath(path string) string {
	switch {
	case strings.Contains(path, "adty_site_domain") || strings.Contains(path, "adty_migrate"):
		return "S13"
	case strings.HasPrefix(path, "web/src/i18n/") || strings.HasPrefix(path, "i18n/"):
		return "H01"
	case path == "LICENSE" || path == "NOTICE" || path == "THIRD-PARTY-LICENSES.md" || strings.HasSuffix(path, "go.mod") || strings.HasSuffix(path, "go.sum") || path == "web/package.json" || path == "web/bun.lock":
		return "H03"
	case strings.HasPrefix(path, "router/") || strings.HasPrefix(path, "common/") || strings.HasPrefix(path, "constant/") || strings.HasPrefix(path, "setting/") && !strings.HasPrefix(path, "setting/model_setting/") || path == "main.go" || path == "model/main.go":
		return "B00"
	case strings.HasPrefix(path, "relay/") || strings.HasPrefix(path, "relaykit/") || strings.HasPrefix(path, "tokenkit/") || strings.HasPrefix(path, "plugins/tasks/") || strings.HasPrefix(path, "pkg/billingexpr/") || strings.HasPrefix(path, "dto/"):
		return "S03"
	case strings.HasPrefix(path, "deploy/") || strings.HasPrefix(path, "scripts/") || strings.HasPrefix(path, ".github/") || strings.HasPrefix(path, "internal/adtygov/") || strings.HasPrefix(path, "internal/pgtest/") || strings.Contains(path, "docker-compose") || strings.HasPrefix(path, "Dockerfile"):
		return "S13"
	}
	// These names identify the existing vertical modules, not product behavior or rule applicability.
	groups := []struct {
		slice string
		names []string
	}{
		{"S10", []string{"adty_voucher"}},
		{"S11", []string{"adty_site_staff", "adty_staff", "adty_quota_adjust", "audit_log", "audit_intent", "adty-admin-permissions", "adty-site-staff"}},
		{"S06", []string{"adty_team", "adty-team"}},
		{"S09", []string{"referral", "adty_site_invite", "adty_commission"}},
		{"S08", []string{"adty_site_finance", "adty_site_wallet", "adty_site_earning", "adty_site_withdraw", "adty_topup_refund", "adty_refund", "adty_fund_anomal", "admin_finance", "recharge-audit", "adty_order_trace"}},
		{"S04", []string{"adty_topup", "adty_epay", "adty_bepusdt", "bepusdt", "adty_payment", "adty-payment", "payment_scene"}},
		{"S05", []string{"adty_balance", "adty_fund", "adty_wallet", "adty-ledger", "features/wallet"}},
		{"S07", []string{"adty_site", "adty_tier", "adty_pricing", "adty-site", "adty-tier", "adty-price"}},
		{"S12", []string{"adty_notification", "adty_announcement", "adty_telegram", "adty_translation", "adty_ticket", "adty_glossary", "adty-notification", "adty-translation", "adty-ticket", "adty-docs", "adty-home", "features/legal"}},
		{"S02", []string{"model/token", "model/channel", "model/pricing", "model/model_", "model/adty_group", "service/adty_group", "setting/model_setting", "features/keys", "features/channels", "features/models", "features/pricing"}},
		{"S01", []string{"controller/user", "model/user", "controller/auth", "service/auth", "middleware/auth", "middleware/secure_verification", "service/security_verification", "service/passkey", "service/login", "service/email_binding", "features/auth", "features/security"}},
		{"S03", []string{"service/quota", "service/text_quota", "service/image_billing", "service/tiered_settle", "service/task_billing", "service/responses_usage", "model/billing", "model/quota"}},
	}
	for _, group := range groups {
		for _, name := range group.names {
			if strings.Contains(path, name) {
				return group.slice
			}
		}
	}
	return "B00"
}

func featureAuditSlice(id string) string {
	var group, item int
	if _, err := fmt.Sscanf(id, "ADTY-%02d-%02d", &group, &item); err != nil {
		return "B00"
	}
	slices := []string{"", "S12", "S01", "S05", "S02", "S02", "S03", "S03", "S04", "S04", "S07", "S07", "S08", "S06", "S11", "S10", "S09", "S08", "S12", "S12", "S12", "S01", "S12", "S13", "S13"}
	if group == 20 && item <= 6 {
		return "S02"
	}
	if group == 21 && item >= 7 {
		return "S08"
	}
	if group > 0 && group < len(slices) {
		return slices[group]
	}
	return "B00"
}
