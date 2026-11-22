package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/internal/adtygov"
	"github.com/stretchr/testify/require"
)

func TestAuthoritativeInventory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write := func(base, path, contents string) {
		t.Helper()
		target := filepath.Join(base, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0755))
		require.NoError(t, os.WriteFile(target, []byte(contents), 0644))
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		data, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", data)
	}
	git("init", "-q")
	write(root, "go.mod", "module fixture\n")
	write(root, ".gitignore", "*.log\n")
	write(root, "old.txt", "to remove\n")
	write(root, "index-only.txt", "staged deletion\n")
	write(root, "replaced", "file before directory\n")
	git("add", ".")
	git("-c", "user.name=Fixture", "-c", "user.email=f@example.test", "commit", "-qm", "fixture")
	git("rm", "--quiet", "index-only.txt")
	require.NoError(t, os.Remove(filepath.Join(root, "old.txt")))
	require.NoError(t, os.Remove(filepath.Join(root, "replaced")))
	write(root, "replaced/note.txt", "replacement directory\n")
	write(root, "notes.txt", "new work\n")
	write(root, "runtime.log", "generated\n")
	write(root, "web/src/features/demo/ui.ts", "export const value = 1\n")
	var features strings.Builder
	for g := 1; g <= 24; g++ {
		for i := 1; i <= 10; i++ {
			fmt.Fprintf(&features, "- [ ] **ADTY-%02d-%02d**：feature\n", g, i)
		}
	}
	plan := "# 卷一：业务规则\n### G.1 Rule\noriginal\n### A.15 Parent\n### A.15.1 Lifecycle\ncurrent\n## 附录 H：清单\n" + features.String() + "# 卷二：保护\n"
	read := func(planText string) map[string]adtygov.Unit {
		t.Helper()
		write(outside, "plan.md", planText)
		output := filepath.Join(outside, "units.jsonl")
		require.NoError(t, run("enumerate-units", []string{"--root", root, "--plan", filepath.Join(outside, "plan.md"), "-o", output}))
		b, err := os.ReadFile(output)
		require.NoError(t, err)
		units := map[string]adtygov.Unit{}
		for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
			var u adtygov.Unit
			require.NoError(t, json.Unmarshal([]byte(line), &u))
			units[u.UnitID] = u
		}
		return units
	}
	units := read(plan)
	require.Contains(t, units, "WU-RULE-A.15.1")
	paths := map[string]bool{}
	for _, unit := range units {
		paths[unit.Path] = true
	}
	require.Contains(t, paths, "notes.txt")
	require.Contains(t, paths, "old.txt")
	require.Contains(t, paths, "index-only.txt")
	require.Contains(t, paths, "replaced")
	require.Contains(t, paths, "replaced/note.txt")
	require.NotContains(t, paths, "runtime.log")
	write(root, "web/src/features/demo/runtime.log", "ignored inside a feature\n")
	updated := read(strings.Replace(plan, "current\n", "revised\n", 1))
	require.NotEqual(t, units["WU-RULE-A.15.1"].SourceHash, updated["WU-RULE-A.15.1"].SourceHash)
	require.Equal(t, units["WU-RULE-G.1"].SourceHash, updated["WU-RULE-G.1"].SourceHash)
	require.Equal(t, units["WU-FE-demo"].SourceHash, updated["WU-FE-demo"].SourceHash)
}
