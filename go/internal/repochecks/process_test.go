// SPDX-License-Identifier: MIT OR Apache-2.0

package repochecks

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"testing"
)

// processResult holds the exit status and captured streams of a child process.
type processResult struct {
	status int
	stdout string
	stderr string
}

// runProcess runs name in dir with env and returns its status and output.
// A nil env inherits the test process environment.
func runProcess(t *testing.T, dir string, env []string, name string, args ...string) processResult {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	result := processResult{}
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run %s: %v", name, err)
		}
		result.status = exitErr.ExitCode()
		if result.status < 0 {
			// A process killed by a signal has no exit code, so report it as a failure.
			result.status = 1
		}
	}
	result.stdout = stdout.String()
	result.stderr = stderr.String()
	return result
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFixtureFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
