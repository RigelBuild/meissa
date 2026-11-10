// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	allow := emptyAllowlist()
	allow.Files["gone.go"] = Entry{ID: "", Value: 501, Reason: "split by concern"}
	snapshot := Snapshot{Files: map[string]int{}, Dirs: map[string]int{}, Functions: nil}
	var stdout, stderr strings.Builder
	code := writeUpdated(root, testConfig(), allow, emptyAllowlist(), snapshot, false, &stdout, &stderr)
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
	entries := map[string]Entry{"large.go": {ID: "", Value: 501, Reason: "keep curated reason"}}
	_, errs := rewriteEntries(entries, map[string]int{"large.go": 502}, testConfig(), MetricFiles, true)
	if len(errs) != 0 || entries["large.go"].Reason != "keep curated reason" || entries["large.go"].Value != 502 {
		t.Fatalf("entries=%#v errors=%v", entries, errs)
	}
}
