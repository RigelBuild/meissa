// SPDX-License-Identifier: MIT OR Apache-2.0

package golang

import (
	"github.com/RigelBuild/meissa/go/internal/codeshape"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func newFixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "test@example.invalid"}, {"config", "user.name", "test"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
	}
	return root
}
func trackedFile(t *testing.T, root, path, content string) {
	t.Helper()
	path = filepath.Join(root, filepath.FromSlash(path))
	err := os.MkdirAll(filepath.Dir(path), 0755)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(path, []byte(content), 0644)
	if err != nil {
		t.Fatal(err)
	}
}
func commitFiles(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{{"add", "--all"}, {"commit", "-qm", "fixture"}} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
	}
}
func testConfig() codeshape.Config {
	return codeshape.Config{FileLimit: 500, DirLimit: 10, Extensions: []string{".go", ".cue"},
		Exclude: []string{}, DirLimits: map[string]codeshape.DirLimit{},
		Functions: codeshape.FunctionLimits{Go: codeshape.GoFunctionLimits{Cognitive: 0, Lines: 0, Statements: 0},
			TS: codeshape.TSFunctionLimits{Cognitive: 0, Lines: 0}}}
}

const validConfig = "fileLimit: 500\ndirLimit: 10\nextensions: [\".go\", \".cue\"]\nexclude: []\n" +
	"functions: {go: {cognitive: 20, lines: 60, statements: 40}, ts: {cognitive: 15, lines: 50}}\n"

func writeConfigFiles(t *testing.T, root, config, allowlist string) {
	t.Helper()
	dir := filepath.Join(root, "tools", "code-shape-gate")
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"config.cue": config, "allowlist.cue": allowlist} {
		err = os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)
		if err != nil {
			t.Fatal(err)
		}
	}
}
