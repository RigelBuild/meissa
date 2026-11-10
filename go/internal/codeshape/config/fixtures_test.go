// SPDX-License-Identifier: MIT OR Apache-2.0

package config

import (
	"os/exec"
	"testing"
)

func newFixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "git", "init", "-q")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	return root
}
