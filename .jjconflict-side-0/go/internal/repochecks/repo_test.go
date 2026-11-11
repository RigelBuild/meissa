// SPDX-License-Identifier: MIT OR Apache-2.0

package repochecks

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestNoAggregateGrammarDependency(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "list", "-deps", "./...")
	cmd.Dir = moduleRoot(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, output)
	}
	for pkg := range strings.FieldsSeq(string(output)) {
		if pkg == "github.com/odvcencio/gotreesitter/grammars" {
			t.Fatal("aggregate gotreesitter grammar imported")
		}
	}
}

func TestConsumerBuildWithoutCGO(t *testing.T) {
	root := moduleRoot(t)
	dir := t.TempDir()
	module := "module consumer\n\ngo 1.26.0\n\n" +
		"require github.com/RigelBuild/meissa/go v0.0.0\n\n" +
		"replace github.com/RigelBuild/meissa/go => " + filepath.ToSlash(root) + "\n"
	err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(module), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	source := "package consumer\n\n" +
		"import \"github.com/RigelBuild/meissa/go/commentscan\"\n\n" +
		"func scan(text string) commentscan.Residues { return commentscan.ScanJSX(text) }\n"
	err = os.WriteFile(filepath.Join(dir, "consumer.go"), []byte(source), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=-mod=mod")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("consumer build: %v\n%s", err, output)
	}
}
