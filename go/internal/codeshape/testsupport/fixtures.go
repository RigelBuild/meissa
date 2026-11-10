// SPDX-License-Identifier: MIT OR Apache-2.0

// Package testsupport provides isolated repositories for code-shape tests.
package testsupport

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/codeshape/model"
)

// NewFixtureRepo creates an initialized git repository in the test temp directory.
func NewFixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	command := exec.CommandContext(t.Context(), "git", "init", "-q")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	for _, args := range [][]string{{"config", "user.email", "test@example.invalid"}, {"config", "user.name", "test"}} {
		command = exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
		output, err = command.CombinedOutput()
		if err != nil {
			t.Fatalf("git config: %v: %s", err, output)
		}
	}
	return root
}

// TrackedFile writes a fixture file inside a test repository.
func TrackedFile(t *testing.T, root, path, content string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// CommitFiles adds all fixture files to the repository.
func CommitFiles(t *testing.T, root string) {
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

// Config returns the standard valid code-shape test configuration.
func Config() model.Config {
	return model.Config{
		FileLimit: 500, DirLimit: 10, Extensions: []string{".go", ".cue"}, Exclude: []string{},
		DirLimits: map[string]model.DirLimit{},
		Functions: model.FunctionLimits{
			Go: model.GoFunctionLimits{Cognitive: 20, Lines: 60, Statements: 40},
			TS: model.TSFunctionLimits{Cognitive: 15, Lines: 50},
		},
	}
}

// WriteConfigFiles writes a gate config and allowlist.
func WriteConfigFiles(t *testing.T, root, config, allowlist string) {
	t.Helper()
	dir := filepath.Join(root, "tools", "code-shape-gate")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{"config.cue": config, "allowlist.cue": allowlist} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
