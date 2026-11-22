// SPDX-License-Identifier: MIT OR Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoldenConfigLegacyExemptKey compares the CLI's stderr for a legacy
// exemption key to the TypeScript golden for the same config.
func TestGoldenConfigLegacyExemptKey(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.json")
	body := `{"designsRoot":"docs/designs","surfaceDepth":1,` +
		`"counter":{"url":"x","partition":"p"},"exemptBranchPrefixes":[]}`
	err := os.WriteFile(config, []byte(body), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := Main(t.Context(), []string{"--config", config}, func(string) string { return "" }, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr:\n%s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	want, err := os.ReadFile(filepath.Join(
		"..", "..", "internal", "ledgergate", "testdata", "ts-golden", "config-legacy-exempt-key.txt",
	))
	if err != nil {
		t.Fatal(err)
	}
	if stderr.String() != string(want) {
		t.Fatalf("stderr mismatch\n got: %q\nwant: %q", stderr.String(), want)
	}
}
