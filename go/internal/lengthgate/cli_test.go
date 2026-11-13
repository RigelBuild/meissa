// SPDX-License-Identifier: MIT OR Apache-2.0

package lengthgate_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildCLI compiles cmd/length-gate into a temporary directory and returns the binary path.
func buildCLI(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "length-gate")
	command := exec.CommandContext(t.Context(), "go", "build", "-o", binary,
		"github.com/RigelBuild/meissa/go/cmd/length-gate")
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}
	return binary
}

// writeConfig writes the comment-gate configuration the CLI loads with --config.
func writeConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "length-gate.json")
	const body = `{"extensions":[".ts"],"exclude":["vendor/**"],` +
		`"docs":[{"name":"spec","include":["docs/specs/**/*.md"],"exclude":[],"target":1500,"markerAllowed":false}]}`
	err := os.WriteFile(path, []byte(body), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMainEndToEnd(t *testing.T) {
	t.Run("spawns the CLI and prints a failing finding", func(t *testing.T) {
		binary := buildCLI(t)
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		makePullRequest(t, repository, longComment())
		config := writeConfig(t)
		command := exec.CommandContext(t.Context(), binary, "--config", config)
		command.Dir = repository
		command.Env = append(gitEnv(t),
			"GATE_ROOT="+repository,
			"LENGTH_GATE_EVENT=pull_request",
			"LENGTH_GATE_BASE=main",
			"LENGTH_GATE_HEAD=feature",
		)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		err := command.Run()
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
			t.Fatalf("run error = %v, want exit 1", err)
		}
		if !strings.Contains(stderr.String(), "src/file.ts:1:") {
			t.Fatalf("stderr %q does not contain src/file.ts:1:", stderr.String())
		}
	})
}
