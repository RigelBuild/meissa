// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:err113,exhaustruct_v5 // Keep TS-derived cases direct and readable.
package dllist

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
)

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errors.New("writer closed") }

// fixtureOpts are the knobs decisionFixture varies.
type fixtureOpts struct {
	surface, area, decision, status, root, layout string
}

func withSurface(s string) func(*fixtureOpts)  { return func(o *fixtureOpts) { o.surface = s } }
func withArea(a string) func(*fixtureOpts)     { return func(o *fixtureOpts) { o.area = a } }
func withDecision(d string) func(*fixtureOpts) { return func(o *fixtureOpts) { o.decision = d } }
func withStatus(s string) func(*fixtureOpts)   { return func(o *fixtureOpts) { o.status = s } }
func withRoot(r string) func(*fixtureOpts)     { return func(o *fixtureOpts) { o.root = r } }
func withFlatLayout() func(*fixtureOpts)       { return func(o *fixtureOpts) { o.layout = "flat" } }

// decisionFixture builds one decision file in the layout the TS tests use.
func decisionFixture(id string, opts ...func(*fixtureOpts)) DecisionFile {
	o := fixtureOpts{
		surface:  "tools",
		area:     "general",
		decision: "Decision " + id,
		status:   "Active (Matt, 2026-10-07)",
		root:     "docs/designs",
		layout:   "surface",
	}
	for _, opt := range opts {
		opt(&o)
	}
	path := fmt.Sprintf("%s/decisions/%s/%s.md", o.root, o.area, id)
	if o.layout == "surface" {
		path = fmt.Sprintf("%s/%s/decisions/%s/%s.md", o.root, o.surface, o.area, id)
	}
	text := strings.Join([]string{
		"---",
		"id: " + id,
		`decision: "` + o.decision + `"`,
		`status: "` + o.status + `"`,
		"record: ../../rec.md#approach",
		"---",
		"",
	}, "\n")
	return DecisionFile{Path: path, Text: text}
}

// corpus is the shared listing fixture.
func corpus() []DecisionFile {
	return []DecisionFile{
		decisionFixture("DL-500", withSurface("platform"), withArea("secrets")),
		decisionFixture("DL-002", withDecision("Use moon for task graphs")),
		decisionFixture("DL-099", withSurface("agents"), withStatus("Superseded by DL-500 (Matt, 2026-10-08)")),
		decisionFixture("DL-003", withArea("ledger"), withStatus("Retired (Matt, 2026-10-08)")),
	}
}

// idsOf returns the first field of each listing line, the decision id.
func idsOf(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, strings.Fields(line)[0])
	}
	return out
}

// depth1 is the two-level config the surface-layout fixtures use.
func depth1() core.Config {
	return core.Config{SurfaceDepth: 1, Surfaces: []string{"tools", "agents", "platform"}}
}

// TestListDecisions covers listDecisions.
func TestListDecisions(t *testing.T) {
	t.Run("one line per decision, sorted by id number", func(t *testing.T) {
		lines, errs := ListDecisions(corpus(), Filters{}, depth1())
		if len(errs) != 0 {
			t.Fatalf("errors = %v, want none", errs)
		}
		want := []string{"DL-002", "DL-003", "DL-099", "DL-500"}
		if got := idsOf(lines); !reflect.DeepEqual(got, want) {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	})

	t.Run("a line carries status, location, decision and record", func(t *testing.T) {
		lines, _ := ListDecisions(corpus(), Filters{Grep: "moon"}, depth1())
		if len(lines) != 1 {
			t.Fatalf("lines = %v, want one", lines)
		}
		want := "DL-002  Active  tools/general  Use moon for task graphs  → docs/designs/tools/rec.md#approach"
		if lines[0] != want {
			t.Fatalf("line = %q, want %q", lines[0], want)
		}
	})

	t.Run("filters combine and grep searches only printed text", func(t *testing.T) {
		lines, _ := ListDecisions(corpus(), Filters{Surface: "tools"}, depth1())
		if got := idsOf(lines); !reflect.DeepEqual(got, []string{"DL-002", "DL-003"}) {
			t.Fatalf("surface tools ids = %v", got)
		}
		lines, _ = ListDecisions(corpus(), Filters{Surface: "tools", Status: "retired"}, depth1())
		if got := idsOf(lines); !reflect.DeepEqual(got, []string{"DL-003"}) {
			t.Fatalf("surface+status ids = %v", got)
		}
		lines, _ = ListDecisions(corpus(), Filters{Grep: "tools/general"}, depth1())
		if len(lines) == 0 {
			t.Fatal("grep over the printed location found nothing")
		}
	})

	t.Run("a layout without a surface level lists the area alone", func(t *testing.T) {
		flat := decisionFixture("DL-010", withArea("ci"), withFlatLayout())
		lines, _ := ListDecisions([]DecisionFile{flat}, Filters{}, core.Config{SurfaceDepth: 0, Surfaces: []string{"tools"}})
		if len(lines) != 1 || !strings.Contains(lines[0], "  ci  ") {
			t.Fatalf("lines = %v, want area-only location", lines)
		}
	})

	t.Run("a malformed file is reported, not dropped silently", func(t *testing.T) {
		bad := DecisionFile{Path: "docs/designs/tools/decisions/general/DL-004.md", Text: "no front matter\n"}
		lines, errs := ListDecisions(append(corpus(), bad), Filters{}, depth1())
		if len(errs) != 1 || !strings.HasPrefix(errs[0], "docs/designs/tools/decisions/general/DL-004.md:") {
			t.Fatalf("errs = %v, want one path-prefixed error", errs)
		}
		if len(lines) != 4 {
			t.Fatalf("lines = %d, want the 4 good decisions", len(lines))
		}
	})
}

// TestParseArgs covers parseArgs.
func TestParseArgs(t *testing.T) {
	t.Run("defaults to the consumer config and accepts filters", func(t *testing.T) {
		got, err := ParseArgs(
			[]string{"--surface", "tools", "--area", "general", "--status", "Active", "--grep", "moon", "--glob", "x/*.md"},
		)
		if err != nil {
			t.Fatal(err)
		}
		if got.ConfigPath != "design-ledger.config.json" {
			t.Fatalf("ConfigPath = %q, want the default", got.ConfigPath)
		}
		want := Filters{Surface: "tools", Area: "general", Status: "active", Grep: "moon"}
		if got.Filters != want || got.Glob != "x/*.md" {
			t.Fatalf("ParseArgs = %#v, want filters %#v and glob x/*.md", got, want)
		}
	})

	t.Run("rejects bad input", func(t *testing.T) {
		_, err := ParseArgs([]string{"--status", "live"})
		if err == nil || err.Error() != "--status must be one of active, superseded, retired" {
			t.Fatalf("err = %v", err)
		}
		_, err = ParseArgs([]string{"--bogus"})
		if err == nil {
			t.Fatal("unknown flag accepted")
		}
		_, err = ParseArgs([]string{"--config", "a", "--config", "b"})
		if err == nil {
			t.Fatal("duplicate --config accepted")
		}
	})
}

// tree writes files under a fresh directory and a config for it.
func tree(t *testing.T, files []DecisionFile, designsRoot string, surfaceDepth int) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		full := filepath.Join(root, filepath.FromSlash(f.Path))
		err := os.MkdirAll(filepath.Dir(full), 0o755)
		if err != nil {
			t.Fatal(err)
		}
		err = os.WriteFile(full, []byte(f.Text), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}
	governed := ""
	surfaces := `"surfaces":["tools","agents","platform"]`
	if surfaceDepth == 0 {
		governed = `"governedRoots":["ui"],`
		surfaces = `"surfaces":["tools"]`
	}
	config := fmt.Sprintf(
		`{"designsRoot":%q,"surfaceDepth":%d,%s%s,`+
			`"counter":{"url":"https://counter.example.test","partition":"public-docs"}}`,
		designsRoot, surfaceDepth, governed, surfaces,
	)
	err := os.WriteFile(filepath.Join(root, "design-ledger.config.json"), []byte(config), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// call runs the CLI in root and returns its exit code and output.
func call(t *testing.T, argv []string, root string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), argv, root, nil, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestRun covers run, the CLI behaviour the TS run tests check.
func TestRun(t *testing.T) {
	t.Run("uses config-driven roots for surface-depth 1 and depth 0 layouts", func(t *testing.T) {
		root := tree(t, corpus(), "docs/designs", 1)
		if code, out, _ := call(t, nil, root); code != 0 || strings.Count(out, "\n") != 4 {
			t.Fatalf("depth 1: code=%d out=%q", code, out)
		}
		flat := tree(t, []DecisionFile{decisionFixture("DL-011", withArea("ci"), withFlatLayout())}, "docs/designs", 0)
		if code, _, _ := call(t, []string{"--surface", "tools"}, flat); code != 0 {
			t.Fatalf("depth 0 with --surface: code=%d", code)
		}
	})

	t.Run("a non-default designsRoot drives the default glob", func(t *testing.T) {
		root := tree(
			t,
			[]DecisionFile{decisionFixture("DL-011", withArea("ci"), withRoot("ledger-tree"), withFlatLayout())},
			"ledger-tree",
			0,
		)
		if code, out, _ := call(t, nil, root); code != 0 || !strings.HasPrefix(out, "DL-011") {
			t.Fatalf("code=%d out=%q", code, out)
		}
	})

	t.Run("depth-zero surface filtering requires one configured surface", func(t *testing.T) {
		root := t.TempDir()
		cfg := `{"designsRoot":"docs/designs","surfaceDepth":0,"governedRoots":["ui"],` +
			`"counter":{"url":"https://counter.example.test","partition":"public-docs"}}`
		err := os.WriteFile(filepath.Join(root, "design-ledger.config.json"), []byte(cfg), 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if code, _, errOut := call(t, []string{"--surface", "krypton"}, root); code != 2 ||
			!strings.Contains(errOut, "requires exactly one configured surface") {
			t.Fatalf("code=%d err=%q", code, errOut)
		}
	})

	t.Run("a decision file read error is reported with exit 2", func(t *testing.T) {
		root := tree(t, []DecisionFile{decisionFixture("DL-013", withArea("ci"), withFlatLayout())}, "docs/designs", 0)
		file := filepath.Join(root, "docs/designs/decisions/ci/DL-013.md")
		err := os.Chmod(file, 0o000)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(file, 0o600) })
		if code, _, _ := call(t, nil, root); code != 2 {
			t.Fatalf("code=%d, want 2 for an unreadable file", code)
		}
	})

	t.Run("an unreadable decisions directory returns exit 2", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root can read mode-000 directories")
		}
		root := tree(t, []DecisionFile{
			decisionFixture("DL-015", withArea("ci"), withFlatLayout()),
		}, "docs/designs", 0)
		directory := filepath.Join(root, "docs/designs/decisions")
		err := os.Chmod(directory, 0o000)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			err := os.Chmod(directory, 0o755)
			if err != nil {
				t.Errorf("restore decisions directory permissions: %v", err)
			}
		})
		if code, _, errOut := call(t, nil, root); code != 2 || !strings.Contains(errOut, "cannot scan or read") {
			t.Fatalf("code=%d err=%q, want exit 2 with scan/read diagnostic", code, errOut)
		}
	})

	t.Run("depth-zero surface filtering selects the configured surface", func(t *testing.T) {
		root := tree(
			t,
			[]DecisionFile{decisionFixture("DL-014", withArea("ci"), withFlatLayout())},
			"docs/designs",
			0,
		)
		cfg := `{"designsRoot":"docs/designs","surfaceDepth":0,"governedRoots":["ui"],"surfaces":["tools"],` +
			`"counter":{"url":"https://counter.example.test","partition":"public-docs"}}`
		err := os.WriteFile(filepath.Join(root, "design-ledger.config.json"), []byte(cfg), 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if code, out, _ := call(t, []string{"--surface", "tools"}, root); code != 0 ||
			!strings.HasPrefix(out, "DL-014") {
			t.Fatalf("code=%d out=%q", code, out)
		}
	})

	t.Run("an explicit glob overrides the layout-derived pattern", func(t *testing.T) {
		root := tree(
			t,
			[]DecisionFile{decisionFixture("DL-010", withArea("ci"), withRoot("ledger"), withFlatLayout())},
			"docs/designs",
			0,
		)
		if code, out, _ := call(t, []string{"--glob", "ledger/decisions/*/DL-*.md"}, root); code != 0 ||
			!strings.HasPrefix(out, "DL-010") {
			t.Fatalf("code=%d out=%q", code, out)
		}
	})

	t.Run("a glob matching nothing fails instead of looking like an empty ledger", func(t *testing.T) {
		root := tree(t, []DecisionFile{decisionFixture("DL-010", withArea("ci"), withFlatLayout())}, "docs/designs", 0)
		if code, _, errOut := call(t, []string{"--glob", "nothing/*.md"}, root); code != 1 ||
			!strings.Contains(errOut, "no decision files match") {
			t.Fatalf("code=%d err=%q", code, errOut)
		}
	})

	t.Run("a filter matching nothing still succeeds", func(t *testing.T) {
		root := tree(t, corpus(), "docs/designs", 1)
		if code, out, errOut := call(t, []string{"--grep", "no such text"}, root); code != 0 || out != "" || errOut != "" {
			t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
		}
	})

	t.Run("a malformed file prints the rest and exits 1", func(t *testing.T) {
		files := append(corpus(), DecisionFile{Path: "docs/designs/tools/decisions/general/DL-004.md", Text: "broken\n"})
		root := tree(t, files, "docs/designs", 1)
		code, out, errOut := call(t, nil, root)
		if code != 1 || strings.Count(out, "\n") != 4 || !strings.Contains(errOut, "DL-004.md:") {
			t.Fatalf("code=%d out-lines=%d err=%q", code, strings.Count(out, "\n"), errOut)
		}
	})

	t.Run("help goes to stdout with exit 0; a usage error exits 2", func(t *testing.T) {
		root := tree(t, corpus(), "docs/designs", 1)
		if code, out, errOut := call(t, []string{"--help"}, root); code != 0 ||
			strings.TrimSpace(out) != strings.TrimSpace(usage) || errOut != "" {
			t.Fatalf("help: code=%d out=%q err=%q", code, out, errOut)
		}
		if code, _, errOut := call(t, []string{"--bogus"}, root); code != 2 ||
			!strings.Contains(errOut, "unknown argument --bogus") {
			t.Fatalf("usage: code=%d err=%q", code, errOut)
		}
	})

	t.Run("configuration read errors use usage exit 2", func(t *testing.T) {
		root := t.TempDir()
		if code, _, errOut := call(t, nil, root); code != 2 || !strings.HasPrefix(errOut, "dl-list: ") {
			t.Fatalf("code=%d err=%q", code, errOut)
		}
	})

	t.Run("resolves an absolute config path without joining it to root", func(t *testing.T) {
		root := tree(t, corpus(), "docs/designs", 1)
		configPath := filepath.Join(t.TempDir(), "external-config.json")
		data, err := os.ReadFile(filepath.Join(root, "design-ledger.config.json"))
		if err != nil {
			t.Fatal(err)
		}
		err = os.WriteFile(configPath, data, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		code := run(context.Background(), []string{"--config", configPath}, root, nil, io.Discard, io.Discard)
		if code != 0 {
			t.Fatalf("code=%d, want 0 for absolute config path", code)
		}
	})

	t.Run("returns a failed exit when listing output cannot be written", func(t *testing.T) {
		root := tree(t, corpus(), "docs/designs", 1)
		code := run(context.Background(), nil, root, nil, errorWriter{}, io.Discard)
		if code != 1 {
			t.Fatalf("code=%d, want 1 for output failure", code)
		}
	})
}
