// SPDX-License-Identifier: MIT OR Apache-2.0

package cli

import (
	. "github.com/RigelBuild/meissa/go/internal/codeshape"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func newFixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	command := exec.CommandContext(t.Context(), "git", "init", "-q")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	command = exec.CommandContext(t.Context(), "git", "-C", root, "config", "user.email", "test@example.invalid")
	output, err = command.CombinedOutput()
	if err != nil {
		t.Fatalf("git config: %v: %s", err, output)
	}
	command = exec.CommandContext(t.Context(), "git", "-C", root, "config", "user.name", "test")
	output, err = command.CombinedOutput()
	if err != nil {
		t.Fatalf("git config: %v: %s", err, output)
	}
	return root
}

func trackedFile(t *testing.T, root, path, content string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(path))
	err := os.MkdirAll(filepath.Dir(fullPath), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(fullPath, []byte(content), 0o644)
	if err != nil {
		t.Fatal(err)
	}
}

func commitFiles(t *testing.T, root string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", "-C", root, "add", "--all")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git add: %v: %s", err, output)
	}
	command = exec.CommandContext(t.Context(), "git", "-C", root, "commit", "-qm", "fixture")
	output, err = command.CombinedOutput()
	if err != nil {
		t.Fatalf("git commit: %v: %s", err, output)
	}
}
func testConfig() Config {
	return Config{
		FileLimit: 500, DirLimit: 10, Extensions: []string{".go", ".cue"},
		Exclude: []string{}, DirLimits: map[string]DirLimit{},
		Functions: FunctionLimits{Go: GoFunctionLimits{Cognitive: 0, Lines: 0, Statements: 0},
			TS: TSFunctionLimits{Cognitive: 0, Lines: 0}},
	}
}

const validConfig = "fileLimit: 500\ndirLimit: 10\nextensions: [\".go\", \".cue\"]\nexclude: []\n" +
	"functions: {go: {cognitive: 20, lines: 60, statements: 40}, ts: {cognitive: 15, lines: 50}}\n"

func writeConfigFiles(t *testing.T, root, config, allowlist string) {
	t.Helper()
	dir := filepath.Join(root, "tools", "code-shape-gate")
	err := os.MkdirAll(dir, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"config.cue": config, "allowlist.cue": allowlist} {
		err = os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
		if err != nil {
			t.Fatal(err)
		}
	}
}
func functionEntry(entries []Entry, id string) (Entry, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return Entry{ID: "", Value: 0, Reason: ""}, false
}
