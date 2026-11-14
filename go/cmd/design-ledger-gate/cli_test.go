// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:exhaustruct_v5,lll // Keep TS-derived cases direct and readable.
package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
)

func TestMainUsage(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := Main(context.Background(), []string{"--help"}, func(string) string { return "" }, &stdout, &stderr); code != 0 {
		t.Fatalf("help exit = %d, want 0", code)
	}
	if stdout.String() != usage || stderr.Len() != 0 {
		t.Fatalf("help output = %q, stderr = %q; want %q and empty", stdout.String(), stderr.String(), usage)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Main(context.Background(), nil, func(string) string { return "" }, &stdout, &stderr); code != 2 {
		t.Fatalf("invalid args exit = %d, want 2", code)
	}
	if stderr.String() != usage || stdout.Len() != 0 {
		t.Fatalf("invalid args output = %q, stderr = %q; want empty and %q", stdout.String(), stderr.String(), usage)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestMainWriteFailuresDoNotChangeExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{name: "help", args: []string{"--help"}, want: 0},
		{name: "usage", want: 2},
		{name: "config", args: []string{"--config", filepath.Join(t.TempDir(), "missing.cue")}, want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Main(t.Context(), tc.args, func(string) string { return "" }, failingWriter{}, failingWriter{})
			if got != tc.want {
				t.Fatalf("Main exit = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestBaseRefFrom(t *testing.T) {
	for _, tc := range []struct {
		name string
		ref  string
		want string
	}{
		{name: "default", want: "main"},
		{name: "configured", ref: "release/1.x", want: "release/1.x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := baseRefFrom(func(key string) string {
				if key == "BASE_REF" {
					return tc.ref
				}
				return ""
			}); got != tc.want {
				t.Fatalf("baseRefFrom = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestListDesignFilesWalksFilesystem(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{
		"docs/designs/z.md",
		"docs/designs/nested/a.md",
		"docs/designs/.hidden/secret.md",
		"docs/designs/.hidden.md",
	} {
		path := filepath.Join(root, filepath.FromSlash(file))
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		if err != nil {
			t.Fatal(err)
		}
		err = os.WriteFile(path, []byte("body"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}
	deps := &gitDeps{root: root, cfg: core.Config{DesignsRoot: "docs/designs"}}
	got, err := deps.ListDesignFiles(context.Background())
	if err != nil {
		t.Fatalf("ListDesignFiles: %v", err)
	}
	want := []string{"docs/designs/nested/a.md", "docs/designs/z.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListDesignFiles = %v, want %v", got, want)
	}
}

func TestReadTextTreatsNonRegularPathsAsMissing(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "docs", "designs", "record-dir")
	err := os.MkdirAll(directory, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	deps := &gitDeps{root: root}
	text, found, err := deps.ReadText("docs/designs/record-dir")
	if err != nil || found || text != "" {
		t.Fatalf("ReadText(directory) = %q, %v, %v; want empty, not found, nil", text, found, err)
	}
}

func TestMainReportsDirectorySupersessionAsViolation(t *testing.T) {
	root := t.TempDir()
	decisionPath := filepath.Join(root, "docs/designs/decisions/ui/DL-001.md")
	recordPath := filepath.Join(root, "docs/designs/ui/record/design.md")
	archivePath := filepath.Join(root, "docs/designs/ui/record/archive")
	for _, directory := range []string{filepath.Dir(decisionPath), filepath.Dir(recordPath), archivePath} {
		err := os.MkdirAll(directory, 0o755)
		if err != nil {
			t.Fatal(err)
		}
	}
	decision := `---
id: DL-001
decision: "Choose the stable option."
status: "Active (Reviewer, 2026-01-01)"
record: ../../ui/record/design.md
---
# Decision

Choose the stable option.
`
	err := os.WriteFile(decisionPath, []byte(decision), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(recordPath, []byte("# Record\n\nStatus: Superseded by archive/\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "ledger.json")
	config := `{"designsRoot":"docs/designs","surfaceDepth":0,"governedRoots":["ui"],"counter":{"url":"https://counter.example.test","partition":"public-docs"}}`
	err = os.WriteFile(configPath, []byte(config), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := Main(context.Background(), []string{"--config", configPath}, func(key string) string {
		if key == "GATE_ROOT" {
			return root
		}
		return ""
	}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "Status supersession does not resolve to a record: archive/") {
		t.Fatalf("exit = %d, want supersession violation; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestListDesignFilesTreatsMissingRootAsEmpty(t *testing.T) {
	deps := &gitDeps{root: t.TempDir(), cfg: core.Config{DesignsRoot: "docs/designs"}}
	files, err := deps.ListDesignFiles(context.Background())
	if err != nil || len(files) != 0 {
		t.Fatalf("ListDesignFiles = %v, %v; want empty listing and nil error", files, err)
	}
}

func TestPRMetadataIsReused(t *testing.T) {
	reads := map[string]int{}
	deps := &gitDeps{env: func(key string) string {
		reads[key]++
		return ""
	}}
	_, err := deps.Changed(context.Background())
	if err != nil {
		t.Fatalf("Changed: %v", err)
	}
	_, err = deps.PullRequestDetails(context.Background())
	if err != nil {
		t.Fatalf("PullRequestDetails: %v", err)
	}
	if reads["REPO"] != 1 {
		t.Fatalf("REPO read count = %d, want 1", reads["REPO"])
	}
}

func TestMainBadArgumentsPrintUsageAndExitTwo(t *testing.T) {
	for name, args := range map[string][]string{
		"no args":          nil,
		"config missing":   {"--config"},
		"extra args":       {"--config", "a.cue", "extra"},
		"wrong flag":       {"--cfg", "a.cue"},
		"empty config arg": {"--config", ""},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			code := Main(t.Context(), args, func(string) string { return "" }, &stdout, &stderr)
			if code != 2 || stderr.String() != usage || stdout.Len() != 0 {
				t.Fatalf("code = %d, stdout = %q, stderr = %q; want 2, empty and usage", code, stdout.String(), stderr.String())
			}
		})
	}
}
