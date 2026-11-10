// SPDX-License-Identifier: MIT OR Apache-2.0

package cli

import (
	"fmt"
	"github.com/RigelBuild/meissa/go/internal/codeshape"
	shapeconfig "github.com/RigelBuild/meissa/go/internal/codeshape/config"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnmeasuredExtensions(t *testing.T) {
	var output strings.Builder
	err := reportUnmeasuredExtensions(&output, []string{".go", ".ts", ".js", ".cue"})
	if err != nil || output.String() != "code-shape-gate: no function collector for .js\n"+
		"code-shape-gate: no function collector for .cue\n" {
		t.Fatalf("output=%q err=%v", output.String(), err)
	}
}

func TestMainBaseModes(t *testing.T) {
	for _, tc := range []struct {
		name, base, head, ref string
		want                  int
	}{
		{name: "absent at base", base: "", head: "files: {}\n", ref: "HEAD", want: 0},
		{name: "raised", base: `files: "large.go": {value: 501, reason: "split by concern"}`,
			head: `files: "large.go": {value: 502, reason: "split by concern"}`, ref: "HEAD", want: 1},
		{name: "lowered", base: `files: "large.go": {value: 503, reason: "split by concern"}`,
			head: `files: "large.go": {value: 502, reason: "split by concern"}`, ref: "HEAD", want: 0},
		{name: "invalid ref", base: "files: {}\n", head: "files: {}\n", ref: "missing-ref", want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newFixtureRepo(t)
			writeConfigFiles(t, root, validConfig, tc.base)
			if tc.base == "" {
				err := os.Remove(filepath.Join(root, "tools/code-shape-gate/allowlist.cue"))
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "raised" || tc.name == "lowered" {
				trackedFile(t, root, "large.go", strings.Repeat("line\n", 502))
			}
			commitFiles(t, root)
			writeConfigFiles(t, root, validConfig, tc.head)
			var stdout, stderr strings.Builder
			code := Main(t.Context(), []string{root}, func(string) string { return tc.ref }, &stdout, &stderr)
			if code != tc.want {
				t.Fatalf("code=%d stdout=%q stderr=%q want=%d", code, stdout.String(), stderr.String(), tc.want)
			}
			if tc.name == "invalid ref" && !strings.Contains(stderr.String(), "missing-ref") {
				t.Fatalf("git diagnostic missing: %q", stderr.String())
			}
		})
	}
}

func TestMainRelativeRoot(t *testing.T) {
	var source strings.Builder
	source.WriteString("package p\nfunc Large() {\n")
	for range 41 {
		source.WriteString("println(1)\n")
	}
	source.WriteString("}\n")
	root := goTestRepo(t, source.String())
	t.Chdir(root)
	var stdout, stderr strings.Builder
	code := Main(t.Context(), []string{"."}, func(string) string { return "" }, &stdout, &stderr)
	if code != 1 || !strings.Contains(stdout.String(), "x.go") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestMainRejectsEscapingAllowlist(t *testing.T) {
	root := newFixtureRepo(t)
	writeConfigFiles(t, root, validConfig, "files: {}\n")
	outside := filepath.Join(t.TempDir(), "allowlist.cue")
	content := []byte(`files: "gone.go": {value: 501, reason: "split by concern"}`)
	err := os.WriteFile(outside, content, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "tools/code-shape-gate/allowlist.cue")
	err = os.Remove(path)
	if err != nil {
		t.Fatal(err)
	}
	err = os.Symlink(outside, path)
	if err != nil {
		t.Fatal(err)
	}
	commitFiles(t, root)
	var stdout, stderr strings.Builder
	code := Main(t.Context(), []string{"--write", root}, func(string) string { return "" }, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	data, err := os.ReadFile(outside)
	if err != nil || !strings.Contains(string(data), "gone.go") {
		t.Fatalf("outside changed: %q %v", data, err)
	}
}

func TestWriteUpdatedRejectsEscapingAllowlist(t *testing.T) {
	root := newFixtureRepo(t)
	writeConfigFiles(t, root, validConfig, "files: {}\n")
	outside := filepath.Join(t.TempDir(), "allowlist.cue")
	err := os.WriteFile(outside, []byte("unchanged"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "tools/code-shape-gate/allowlist.cue")
	err = os.Remove(path)
	if err != nil {
		t.Fatal(err)
	}
	err = os.Symlink(outside, path)
	if err != nil {
		t.Fatal(err)
	}
	allow := codeshape.EmptyAllowlist()
	allow.Files["gone.go"] = codeshape.Entry{ID: "", Value: 501, Reason: "split by concern"}
	snapshot := codeshape.Snapshot{Files: map[string]int{}, Dirs: map[string]int{}, Functions: nil}
	var stdout, stderr strings.Builder
	code := writeUpdated(root, testConfig(), allow, codeshape.EmptyAllowlist(), snapshot, false, &stdout, &stderr, nil)
	data, err := os.ReadFile(outside)
	if code != 2 || err != nil || string(data) != "unchanged" {
		t.Fatalf("code=%d outside=%q error=%v stderr=%q", code, data, err, stderr.String())
	}
}

func TestParseArgs(t *testing.T) {
	for _, args := range [][]string{
		{"--bad", "."}, {"--write", "--write", "."}, {"--write", "--seed", "--seed", "."},
		{"--seed", "."}, {".", "extra"},
	} {
		if _, _, _, ok := parseArgs(args); ok {
			t.Fatalf("accepted %q", args)
		}
	}
	var out, errout strings.Builder
	code := Main(t.Context(), []string{"--help"}, func(string) string { return "" }, &out, &errout)
	if code != 0 || !strings.Contains(out.String(), "usage:") {
		t.Fatalf("help: %d %q", code, out.String())
	}
}

func TestMainPrintsBaseAndMeasuredFindings(t *testing.T) {
	root := newFixtureRepo(t)
	writeConfigFiles(t, root, validConfig, "files: {}\n")
	commitFiles(t, root)
	writeConfigFiles(t, root, validConfig, `files: "listed.go": {value: 501, reason: "split by concern"}`)
	trackedFile(t, root, "listed.go", strings.Repeat("line\n", 501))
	trackedFile(t, root, "unlisted.go", strings.Repeat("line\n", 501))
	cmd := exec.CommandContext(t.Context(), "git", "-C", root, "add", "--all")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git add: %s %v", output, err)
	}
	var stdout, stderr strings.Builder
	code := Main(t.Context(), []string{root}, func(string) string { return "HEAD" }, &stdout, &stderr)
	if code != 1 || !strings.Contains(stdout.String(), "unlisted.go") ||
		!strings.Contains(stdout.String(), "do not add") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestSeedPreservesReason(t *testing.T) {
	entries := map[string]codeshape.Entry{"large.go": {ID: "", Value: 501, Reason: "keep curated reason"}}
	_, errs := rewriteEntries(entries, map[string]int{"large.go": 502}, testConfig(), codeshape.MetricFiles, true)
	if len(errs) != 0 || entries["large.go"].Reason != "keep curated reason" || entries["large.go"].Value != 502 {
		t.Fatalf("entries=%#v errors=%v", entries, errs)
	}
}
func TestConfiguredDirectoryWidths(t *testing.T) {
	for _, tc := range []struct {
		name, config, want string
		count              int
	}{
		{
			name: "within limit", config: `dirLimits: "wide": {limit: 50, reason: "record decisions together"}`,
			count: 30, want: "",
		},
		{
			name: "over limit", config: `dirLimits: "wide": {limit: 50, reason: "record decisions together"}`,
			count: 51, want: "wide dirs value=51 limit=50 fix=reorganize the record's decisions\n",
		},
		{
			name: "overlapping limits",
			config: `dirLimits: {"wide": {limit: 50, reason: "record decisions together"}, ` +
				`"w*": {limit: 20, reason: "record decisions together"}}`,
			count: 21, want: "wide dirs value=21 limit=20 fix=reorganize the record's decisions\n",
		},
		{name: "excluded files", config: `exclude: ["wide/**"]`, count: 300, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newFixtureRepo(t)
			config := strings.Replace(validConfig, "exclude: []", tc.config, 1)
			if !strings.Contains(tc.config, "exclude:") {
				config += "\nexclude: []\n"
			}
			writeConfigFiles(t, root, config, "files: {}\ndirs: {}\n")
			for index := range tc.count {
				trackedFile(t, root, fmt.Sprintf("wide/file-%03d.txt", index), "text\n")
			}
			commitFiles(t, root)
			var stdout, stderr strings.Builder
			status := Main(t.Context(), []string{root}, func(string) string { return "" }, &stdout, &stderr)
			wantStatus := 0
			if tc.want != "" {
				wantStatus = 1
			}
			if status != wantStatus || stdout.String() != tc.want {
				t.Fatalf("status=%d stdout=%q stderr=%q, want status=%d stdout=%q",
					status, stdout.String(), stderr.String(), wantStatus, tc.want)
			}
		})
	}
}
func TestMainModes(t *testing.T) {
	t.Run("write refuses to add without seed", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, validConfig, "files: {}\ndirs: {}\n")
		trackedFile(t, root, "large.go", strings.Repeat("line\n", 501))
		commitFiles(t, root)
		var stdout, stderr strings.Builder
		code := Main(t.Context(), []string{"--write", root}, func(string) string { return "" }, &stdout, &stderr)
		if code != 2 || !strings.Contains(stderr.String(), "needs --seed") {
			t.Fatalf("Main = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("seed writes raised rollout entries", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, validConfig, "files: {}\ndirs: {}\n")
		trackedFile(t, root, "large.go", strings.Repeat("line\n", 501))
		commitFiles(t, root)
		var stdout, stderr strings.Builder
		code := Main(t.Context(), []string{"--write", "--seed", root}, func(string) string { return "" }, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("Main = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		_, allowlist, err := shapeconfig.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		entry, exists := allowlist.Files["large.go"]
		if !exists || entry.Value != 501 || entry.Reason != "seeded at rollout, burn down" {
			t.Fatalf("seeded entry = %#v, exists=%v", entry, exists)
		}
	})
}
func TestMainWriteUpdates(t *testing.T) {
	t.Run("write lowers entries", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, validConfig, "files: \"large.go\": {value: 502, reason: \"split by concern\"}\n")
		trackedFile(t, root, "large.go", strings.Repeat("line\n", 501))
		commitFiles(t, root)
		var stdout, stderr strings.Builder
		code := Main(t.Context(), []string{"--write", root}, func(string) string { return "" }, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("Main = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		_, allowlist, err := shapeconfig.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if got := allowlist.Files["large.go"].Value; got != 501 {
			t.Fatalf("lowered value = %d, want 501", got)
		}
	})
	t.Run("write removes entries at limit", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, validConfig, "files: \"small.go\": {value: 501, reason: \"split by concern\"}\n")
		trackedFile(t, root, "small.go", strings.Repeat("line\n", 500))
		commitFiles(t, root)
		var stdout, stderr strings.Builder
		code := Main(t.Context(), []string{"--write", root}, func(string) string { return "" }, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("Main = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		_, allowlist, err := shapeconfig.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := allowlist.Files["small.go"]; exists {
			t.Fatal("--write retained an entry now within the limit")
		}
	})
	t.Run("base rejects a seeded added entry", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, validConfig, "files: {}\ndirs: {}\n")
		trackedFile(t, root, "large.go", strings.Repeat("line\n", 501))
		commitFiles(t, root)
		var stdout, stderr strings.Builder
		code := Main(t.Context(), []string{"--write", "--seed", root}, func(key string) string {
			if key == "GATE_BASE" {
				return "HEAD"
			}
			return ""
		}, &stdout, &stderr)
		if code != 1 || !strings.Contains(stdout.String(), "do not add or raise") {
			t.Fatalf("Main = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
}
func TestRewriteFunctionEntries(t *testing.T) {
	cfg := testConfig()
	cfg.Functions.Go.Lines = 60
	allow := codeshape.Allowlist{Files: nil, Dirs: nil, Present: false,
		Functions: codeshape.FunctionMeasurements{codeshape.MetricGoLines: {"f.go": {
			{ID: "lower", Value: 80, Reason: "split by concern"},
			{ID: "remove", Value: 70, Reason: "split by concern"},
		}}}}
	values := codeshape.FunctionMeasurements{codeshape.MetricGoLines: {"f.go": {
		{ID: "lower", Value: 70, Reason: ""}, {ID: "new", Value: 61, Reason: ""},
	}}}
	_, _, err := rewriteAllowlist(cfg, allow, codeshape.Snapshot{Files: nil, Dirs: nil, Functions: values}, false)
	if err == nil {
		t.Fatal("write accepted a new offender")
	}
	updated, changed, err := rewriteAllowlist(
		cfg, allow, codeshape.Snapshot{Files: nil, Dirs: nil, Functions: values}, true,
	)
	if err != nil || !changed {
		t.Fatalf("seed: changed=%v err=%v", changed, err)
	}
	got := updated.Functions[codeshape.MetricGoLines]["f.go"]
	if len(got) != 2 {
		t.Fatalf("seed entries: %#v", got)
	}
	lower, ok := functionEntry(got, "lower")
	if !ok || lower.Value != 70 || lower.Reason != "split by concern" {
		t.Fatalf("lowered entry: %#v", lower)
	}
	added, ok := functionEntry(got, "new")
	if !ok || added.Value != 61 || added.Reason == "" {
		t.Fatalf("seeded entry: %#v", added)
	}
}
