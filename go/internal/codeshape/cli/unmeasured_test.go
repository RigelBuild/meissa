// SPDX-License-Identifier: MIT OR Apache-2.0

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/codeshape"
	"github.com/RigelBuild/meissa/go/internal/codeshape/golang"
)

func TestUnmeasuredSkipFinding(t *testing.T) {
	skips := []codeshape.Skip{
		{Path: "plain.ts", Reason: "no functions", Remedy: "", Unmeasured: nil},
		{Path: "bad.ts", Reason: "parse error", Remedy: "", Unmeasured: &codeshape.UnmeasuredFunctions{
			Values: map[codeshape.Metric]int{codeshape.MetricTSLines: 70}, BiomeParseFailed: false}},
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

func TestUnparseableFileCannotRaiseBaseEntry(t *testing.T) {
	root := tsRepo(t, "export type * from './other';\n")
	base := "functions: {\"ts-lines\": {\"x.ts\": [{id: \"f\", value: 60, reason: \"split this function\"}]}}\n"
	trackedFile(t, root, "tools/code-shape-gate/allowlist.cue", base)
	commitFiles(t, root)
	trackedFile(t, root, "tools/code-shape-gate/allowlist.cue", strings.Replace(base, "60", "80", 1))
	for _, args := range [][]string{{root}, {"--write", root}} {
		var out, stderr strings.Builder
		code := Main(t.Context(), args, func(string) string { return "HEAD" }, &out, &stderr)
		if code != 1 || !strings.Contains(out.String(), "do not add or raise allowlist entries") {
			t.Fatalf("args=%v code=%d out=%q stderr=%q", args, code, out.String(), stderr.String())
		}
	}
}

func TestGoSkipFixNamesDefaultGOARCH(t *testing.T) {
	skips := []codeshape.Skip{{Path: "a_riscv64.go", Reason: "not in any measured Go build", Remedy: "",
		Unmeasured: &codeshape.UnmeasuredFunctions{Values: nil, BiomeParseFailed: false}}}
	var output strings.Builder
	findings, _, err := handleGoSkips(&output, skips)
	want := "not in any measured Go build; exclude it or include it in a default-GOARCH " +
		goosPhrase(golang.MeasuredGOOS()) + " Go build"
	if err != nil || len(findings) != 1 || findings[0].Fix != want {
		t.Fatalf("findings=%v want fix %q err=%v", findings, want, err)
	}
}

func TestGoosPhrase(t *testing.T) {
	cases := []struct {
		goos []string
		want string
	}{
		{[]string{"linux"}, "linux"},
		{[]string{"linux", "darwin"}, "linux or darwin"},
		{[]string{"freebsd", "linux", "darwin", "windows"}, "freebsd, linux, darwin or windows"},
	}
	for _, tc := range cases {
		if got := goosPhrase(tc.goos); got != tc.want {
			t.Fatalf("goosPhrase(%v) = %q, want %q", tc.goos, got, tc.want)
		}
	}
}
