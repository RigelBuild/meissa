// SPDX-License-Identifier: MIT OR Apache-2.0

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/codeshape"
)

func TestUnmeasuredSkipFinding(t *testing.T) {
	skips := []codeshape.Skip{
		{Path: "plain.ts", Reason: "no functions", Unmeasured: nil},
		{Path: "bad.ts", Reason: "parse error", Unmeasured: &codeshape.UnmeasuredFunctions{
			Values: map[codeshape.Metric]int{codeshape.MetricTSLines: 70}, Ignored: false}},
	}
	var output strings.Builder
	findings, paths, err := handleTSSkips(&output, skips, testConfig())
	if err != nil || len(findings) != 1 || findings[0].Path != "bad.ts" || findings[0].Value != 70 ||
		len(paths) != 1 || paths[0] != "bad.ts" {
		t.Fatalf("findings=%v paths=%v err=%v", findings, paths, err)
	}
	if !strings.Contains(output.String(), "skip plain.ts: no functions") {
		t.Fatal(output.String())
	}
}

func TestUnparseableTypeScriptKeepsAllowlist(t *testing.T) {
	root := tsRepo(t, "export type * from './other';\nfunction f() {\n"+strings.Repeat("console.log(1);\n", 51)+"}\n")
	allow := "functions: {\"ts-lines\": {\"x.ts\": [{id: \"f\", value: 80, reason: \"split this function\"}]}}\n"
	trackedFile(t, root, "tools/code-shape-gate/allowlist.cue", allow)
	var out, stderr strings.Builder
	code := Main(t.Context(), []string{"--write", root}, func(string) string { return "" }, &out, &stderr)
	data, err := os.ReadFile(filepath.Join(root, "tools/code-shape-gate/allowlist.cue"))
	if err != nil || code != 1 || !strings.Contains(out.String(), "unparseable by the function collector") ||
		string(data) != allow {
		t.Fatalf("code=%d out=%q stderr=%q allow=%q err=%v", code, out.String(), stderr.String(), data, err)
	}
}
