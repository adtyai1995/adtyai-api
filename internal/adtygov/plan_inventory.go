package adtygov

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

type gitPlanScope struct {
	paths      map[string]bool
	additional []Unit
}

func gitScopeOutput(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	data, err := cmd.Output()
	if e, ok := err.(*exec.ExitError); ok {
		return nil, fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(string(e.Stderr)), err)
	}
	return data, err
}

// EnumeratePlanUnits takes its source membership from the authoritative Git worktree
// and its G/A rules and H feature definitions from the supplied current plan.
func EnumeratePlanUnits(root, planPath string) ([]Unit, error) {
	prefix, err := gitScopeOutput(root, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(prefix)) != 0 {
		return nil, fmt.Errorf("--root must identify the Git worktree root")
	}
	sources, err := gitScopeOutput(root, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	scope := gitPlanScope{paths: make(map[string]bool)}
	present := make(map[string]bool)
	for _, raw := range bytes.Split(sources, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		path := string(raw)
		if strings.HasSuffix(path, "/") {
			return nil, fmt.Errorf("explicit review required for nested worktree %q", path)
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
		if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) {
			continue // An unstaged removal is reconciled against HEAD below.
		}
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			continue // A tracked file can be replaced by a directory of new files.
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("explicit review required for non-regular source %q", path)
		}
		present[path] = true
		for p := path; p != "." && !scope.paths[p]; p = filepath.ToSlash(filepath.Dir(p)) {
			scope.paths[p] = true
		}
	}
	base, err := gitScopeOutput(root, "ls-tree", "-rz", "HEAD")
	if err != nil {
		return nil, err
	}
	for _, entry := range bytes.Split(base, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		meta, path, ok := strings.Cut(string(entry), "\t")
		if !ok {
			return nil, fmt.Errorf("invalid git ls-tree record")
		}
		if strings.HasPrefix(meta, "160000 ") {
			return nil, fmt.Errorf("explicit review required for Git submodule %q", path)
		}
		if !present[path] {
			digest := sha256.Sum256(entry)
			scope.additional = append(scope.additional, Unit{UnitID: fmt.Sprintf("WU-DELETED-%x", sha256.Sum256([]byte(path))),
				Kind: "code", Path: path, Dims: append([]string(nil), backendDims...), Slice: "GEN-DELETED",
				Title: "Removed from worktree: " + meta, SourceHash: fmt.Sprintf("sha256:%x", digest), State: "pending"})
		}
	}
	definitions, err := readPlanUnits(planPath)
	if err != nil {
		return nil, err
	}
	scope.additional = append(scope.additional, definitions...)
	return enumerateUnits(root, &scope)
}

var planRuleHeading = regexp.MustCompile(`^#{2,6} ([GA]\.[0-9]+(?:\.[0-9]+)*)[ \t]+(.+)$`)
var planFeatureLine = regexp.MustCompile(`^\s*- \[[ xX]\] \*\*(ADTY-[0-9]{2}-[0-9]{2})\*\*[：:]\s*(.+)$`)

func readPlanUnits(planPath string) ([]Unit, error) {
	data, err := os.ReadFile(planPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var units []Unit
	var active *Unit
	var start int
	flush := func(end int) {
		if active != nil {
			active.SourceHash = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(strings.Join(lines[start:end], "\n"))))
			units = append(units, *active)
			active = nil
		}
	}
	inBusiness, inFeatures := false, false
	featureIDs := map[string]bool{}
	families := map[byte]bool{}
	dims := []string{"callchain", "tx", "perm", "idemp", "fund", "error", "retire"}
	for i, line := range lines {
		if strings.HasPrefix(line, "# 卷一") {
			inBusiness = true
			continue
		}
		if inBusiness && strings.HasPrefix(line, "# 卷二") {
			flush(i)
			break
		}
		if !inBusiness {
			continue
		}
		match := planRuleHeading.FindStringSubmatch(line)
		if match != nil || strings.HasPrefix(line, "## ") {
			flush(i)
		}
		if match != nil {
			start = i
			id := match[1]
			families[id[0]] = true
			active = &Unit{UnitID: "WU-RULE-" + id, Kind: "rule", RuleID: id, Title: match[2],
				Path: filepath.Base(planPath), SourceLine: i + 1, Dims: dims, Slice: "RULE", AuditSlice: "B00", State: "pending"}
		}
		if strings.HasPrefix(line, "## 附录 H") {
			inFeatures = true
		}
		if inFeatures {
			if feature := planFeatureLine.FindStringSubmatch(line); feature != nil {
				id := feature[1]
				featureIDs[id] = true
				units = append(units, Unit{UnitID: "WU-FEATURE-" + id, Kind: "feature", FeatureID: id,
					Title: feature[2], Path: filepath.Base(planPath), SourceLine: i + 1, Dims: dims,
					Slice: "FEATURE", AuditSlice: featureAuditSlice(id), State: "pending", SourceHash: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(line)))})
			}
		}
	}
	flush(len(lines))
	if !families['G'] || !families['A'] || len(featureIDs) != 240 {
		return nil, fmt.Errorf("expected current volume one with G/A rules and 240 H feature IDs, found %d feature IDs", len(featureIDs))
	}
	for group := 1; group <= 24; group++ {
		for item := 1; item <= 10; item++ {
			id := fmt.Sprintf("ADTY-%02d-%02d", group, item)
			if !featureIDs[id] {
				return nil, fmt.Errorf("missing feature %s in current plan", id)
			}
		}
	}
	return units, nil
}
