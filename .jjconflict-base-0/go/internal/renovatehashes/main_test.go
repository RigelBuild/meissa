// SPDX-License-Identifier: MIT OR Apache-2.0

package renovatehashes

import (
	"context"
	"strings"
	"testing"
)

func TestMainUsage(t *testing.T) {
	t.Run("writes prefixed Git root error", func(t *testing.T) {
		stdout := new(strings.Builder)
		stderr := new(strings.Builder)
		code := mainWithRunner(t.Context(), nil, func(string) string { return "" }, stdout, stderr, failingRunner{})
		if code != 1 {
			t.Fatalf("Main exit = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "renovate-go-analysis: could not find Git root: unexpected command") {
			t.Fatalf("stderr = %q", stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q", stdout.String())
		}
	})
}

type failingRunner struct{}

func (failingRunner) Run(_ context.Context, _ string, _ ...string) (string, string, int, error) {
	return "", "unexpected command", 127, nil
}
