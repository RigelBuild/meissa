// SPDX-License-Identifier: MIT OR Apache-2.0

package golang

import (
	"fmt"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/codeshape"
)

func TestGoCollectorFixture(t *testing.T) {
	root := newFixtureRepo(t)
	trackedFile(t, root, "go.mod", "module example.com/fixture\n\ngo 1.25\n")
	var source strings.Builder
	source.WriteString("package fixture\n")
	for i := range 25 {
		fmt.Fprintf(&source, "func F%d() {\n", i)
		for range 41 {
			source.WriteString("println(1)\n")
		}
		source.WriteString("}\n")
	}
	trackedFile(t, root, "fixture.go", source.String())
	commitFiles(t, root)
	cfg := testConfig()
	cfg.Functions = codeshape.FunctionLimits{Go: codeshape.GoFunctionLimits{Cognitive: 20, Lines: 60, Statements: 40},
		TS: codeshape.TSFunctionLimits{Cognitive: 0, Lines: 0}}
	values, _, err := GoMetrics(t.Context(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range values[codeshape.MetricGoStatements]["fixture.go"] {
		if entry.Value > 40 {
			count++
		}
	}
	if count != 25 {
		t.Fatalf("got %d offenders, want 25: %#v", count, values)
	}
	binary, err := exec.LookPath("golangci-lint")
	if err != nil {
		t.Fatal(err)
	}
	report, err := runGolangci(t.Context(), binary, root, ".", runtime.GOOS, "funlen", 10, 10, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Issues) != 25 {
		t.Fatalf("one funlen run reported %d issues for 25 functions exceeding both limits", len(report.Issues))
	}
}

func TestGoInitIDsStableWhenDifferentFunctionAdded(t *testing.T) {
	ids := func(source string) []string {
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, "x.go", source, 0)
		if err != nil {
			t.Fatal(err)
		}
		functions := goFunctions(file, set)
		result := []string{}
		for _, fn := range functions {
			if strings.HasPrefix(fn.id, "init") {
				result = append(result, fn.id)
			}
		}
		return result
	}
	a := ids("package p\nfunc init() {}\nfunc init() {}")
	b := ids("package p\nfunc other() {}\nfunc init() {}\nfunc init() {}")
	if len(a) != 2 || a[0] == a[1] || strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("unstable ids: %v %v", a, b)
	}
}

func TestGoTopLevelLiteralIdentity(t *testing.T) {
	ids := func(source string) []goFunction {
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, "x.go", source, 0)
		if err != nil {
			t.Fatal(err)
		}
		return goFunctions(file, set)
	}
	a := ids("package p\nvar target = func() {}")
	b := ids("package p\nvar other = func() {}\nvar target = func() {}")
	if a[0].id != "target" || b[1].id != a[0].id {
		t.Fatalf("ids %v %v", a, b)
	}
}

func TestGoSuppressionFinding(t *testing.T) {
	root := newFixtureRepo(t)
	trackedFile(t, root, "go.mod", "module example.com/x\n\ngo 1.25\n")
	trackedFile(t, root, "x.go", "package p\n//nolint:gocognit,funlen\n//nolint:all\n"+
		"//nolint\n//nolint:gocyclo\n//nolint:unused\nfunc x() {}\n")
	commitFiles(t, root)
	findings, err := SuppressionFindings(t.Context(), root, testConfig())
	if err != nil || len(findings) != 7 || findings[0].Path != "x.go:2" {
		t.Fatalf("findings=%v err=%v", findings, err)
	}
}
func goTestRepo(t *testing.T, source string) string {
	t.Helper()
	root := newFixtureRepo(t)
	writeConfigFiles(t, root, validConfig, "files: {}\n")
	trackedFile(t, root, "go.mod", "module example.com/fixture\n\ngo 1.25\n")
	trackedFile(t, root, "x.go", source)
	commitFiles(t, root)
	return root
}
func TestGoNestedModules(t *testing.T) {
	root := goTestRepo(t, "package p\nfunc Parent() {\n"+strings.Repeat("println(1)\n", 41)+"}\n")
	trackedFile(t, root, "child/go.mod", "module example.com/child\n\ngo 1.25\n")
	trackedFile(t, root, "child/child.go", "package child\nfunc Child() {\n"+strings.Repeat("println(1)\n", 41)+"}\n")
	commitFiles(t, root)
	values, _, err := GoMetrics(t.Context(), root, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"x.go", "child/child.go"} {
		entries := values[codeshape.MetricGoStatements][path]
		if len(entries) != 1 || entries[0].Value != 41 {
			t.Fatalf("%s: %#v", path, entries)
		}
	}
	if len(values[codeshape.MetricGoStatements]) != 2 {
		t.Fatalf("unexpected paths: %#v", values[codeshape.MetricGoStatements])
	}
}

func TestGoExcludedAndUnbuiltFiles(t *testing.T) {
	root := goTestRepo(t, "package p\nfunc Parent() {}\n")
	offender := "package fixture\nfunc Large() {\n" + strings.Repeat("println(1)\n", 41) + "}\n"
	trackedFile(t, root, "fixtures/x.go", offender)
	trackedFile(t, root, "excluded/x.go", offender)
	trackedFile(t, root, "tagged.go", "//go:build integration\n\n"+offender)
	trackedFile(t, root, "platform_windows.go", strings.Replace(offender, "package fixture", "package p", 1))
	commitFiles(t, root)
	cfg := testConfig()
	cfg.Exclude = []string{"excluded/**"}
	values, skips, err := GoMetrics(t.Context(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 1 || skips[0].Path != "tagged.go" || len(values[codeshape.MetricGoStatements]) != 2 {
		t.Fatalf("skips=%v measurements=%v", skips, values[codeshape.MetricGoStatements])
	}
}

func TestGoOtherGOOSFilesMeasured(t *testing.T) {
	root := goTestRepo(t, "package p\nfunc Parent() {\n"+strings.Repeat("println(1)\n", 41)+"}\n")
	body := "() {\n" + strings.Repeat("println(1)\n", 42) + "}\n"
	trackedFile(t, root, "x_darwin.go", "package p\nfunc Darwin"+body)
	trackedFile(t, root, "x_windows.go", "package p\nfunc Windows"+body)
	trackedFile(t, root, "both.go", "//go:build darwin || windows\n\npackage p\nfunc Both"+body)
	commitFiles(t, root)
	values, skips, err := GoMetrics(t.Context(), root, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 0 {
		t.Fatalf("skips=%v", skips)
	}
	want := map[string]int{"x.go": 41, "x_darwin.go": 42, "x_windows.go": 42, "both.go": 42}
	statements := values[codeshape.MetricGoStatements]
	if len(statements) != len(want) {
		t.Fatalf("unexpected paths: %#v", statements)
	}
	for path, value := range want {
		for _, metric := range []codeshape.Metric{
			codeshape.MetricGoStatements, codeshape.MetricGoLines, codeshape.MetricGoCognitive,
		} {
			if len(values[metric][path]) != 1 {
				t.Fatalf("%s %s: %#v", metric, path, values[metric][path])
			}
		}
		if entries := statements[path]; entries[0].Value != value {
			t.Fatalf("%s: %#v", path, entries)
		}
	}
}
func TestGoExcludedModule(t *testing.T) {
	root := goTestRepo(t, "package p\nfunc Parent() {}\n")
	trackedFile(t, root, "excluded/go.mod", "module example.com/excluded\n\ngo 1.25\n")
	trackedFile(t, root, "excluded/x.go", "not valid go")
	commitFiles(t, root)
	cfg := testConfig()
	cfg.Exclude = []string{"excluded/**"}
	_, _, err := GoMetrics(t.Context(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = SuppressionFindings(t.Context(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
}
func TestGoBareAndAllSuppressions(t *testing.T) {
	root := goTestRepo(t, "package p\n//nolint\n//nolint:all\nfunc F() {}\n")
	findings, err := SuppressionFindings(t.Context(), root, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []int{2, 3} {
		for _, metric := range []codeshape.Metric{codeshape.MetricGoCognitive, codeshape.MetricGoLines} {
			found := false
			for _, finding := range findings {
				if finding.Path == fmt.Sprintf("x.go:%d", line) && finding.Value == 1 && finding.Metric == metric {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s line %d: %#v", metric, line, findings)
			}
		}
	}
}

func TestGoLoneOtherGOOSFileMeasured(t *testing.T) {
	root := goTestRepo(t, "package p\nfunc Parent() {}\n")
	trackedFile(t, root, "x_darwin.go", "package p\nfunc Darwin() {\n"+strings.Repeat("println(1)\n", 41)+"}\n")
	commitFiles(t, root)
	cfg := testConfig()
	cfg.Functions = codeshape.FunctionLimits{Go: codeshape.GoFunctionLimits{Cognitive: 20, Lines: 60, Statements: 40},
		TS: codeshape.TSFunctionLimits{Cognitive: 0, Lines: 0}}
	values, skips, err := GoMetrics(t.Context(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 0 {
		t.Fatalf("skips=%v", skips)
	}
	entries := values[codeshape.MetricGoStatements]["x_darwin.go"]
	if len(entries) != 1 || entries[0].Value != 41 {
		t.Fatalf("x_darwin.go: %#v", entries)
	}
}

func TestGoOtherGOOSCgoFileUnmeasured(t *testing.T) {
	other := "darwin"
	if build.Default.GOOS == other {
		other = "windows"
	}
	path := "c_" + other + ".go"
	root := goTestRepo(t, "package p\nfunc Parent() {}\n")
	trackedFile(t, root, path, "package p\nimport \"C\"\nfunc Cgo() {\n"+strings.Repeat("println(1)\n", 41)+"}\n")
	commitFiles(t, root)
	values, skips, err := GoMetrics(t.Context(), root, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 1 || skips[0].Path != path || skips[0].Unmeasured == nil ||
		!strings.Contains(skips[0].Reason, "cgo") {
		t.Fatalf("skips=%#v", skips)
	}
	if entries := values[codeshape.MetricGoStatements][path]; len(entries) != 0 {
		t.Fatalf("cgo file measured: %#v", entries)
	}
}

func TestGoFilesAssignedToHostThenFirstMatch(t *testing.T) {
	root := goTestRepo(t, "package p\nfunc Parent() {}\n")
	trackedFile(t, root, "both.go", "//go:build darwin || windows\n\npackage p\nfunc Both() {}\n")
	commitFiles(t, root)
	files, skips, err := parseGoSources(root, ".", []string{"both.go", "x.go"}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 0 {
		t.Fatalf("skips=%v", skips)
	}
	if got := files["x.go"].goos; got != build.Default.GOOS {
		t.Fatalf("x.go measured under %q, want host %q", got, build.Default.GOOS)
	}
	// MeasuredGOOS order is host, then linux, darwin, windows: first match wins.
	want := "darwin"
	if build.Default.GOOS == "windows" {
		want = "windows"
	}
	if got := files["both.go"].goos; got != want {
		t.Fatalf("both.go measured under %q, want %q", got, want)
	}
}

func TestGoOnlyHostPassWithoutOtherGOOSFiles(t *testing.T) {
	root := goTestRepo(t, "package p\nfunc Parent() {}\n")
	log := filepath.Join(t.TempDir(), "goos.log")
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'golangci-lint has version 2.15.0'; exit 0; fi\n" +
		"echo \"$GOOS\" >> '" + log + "'\n" +
		"printf '%s' '{\"Issues\":[],\"Report\":{\"Linters\":[" +
		"{\"Name\":\"gocognit\",\"Enabled\":true},{\"Name\":\"funlen\",\"Enabled\":true}]}}'\n"
	err := os.WriteFile(filepath.Join(dir, "golangci-lint"), []byte(script), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, _, err = GoMetrics(t.Context(), root, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for goos := range strings.FieldsSeq(string(data)) {
		seen[goos] = true
	}
	if len(seen) != 1 || !seen[build.Default.GOOS] {
		t.Fatalf("golangci-lint ran for GOOS %v, want only host %q", seen, build.Default.GOOS)
	}
}

// TestGoSharedFileCountedOnceUnderItsAssignedGOOS pins the count-once invariant:
// a file built for two GOOS sets is assigned to one of them, and a pass for the
// other GOOS must not record a value for it. The fake linter reports a larger
// value under GOOS=windows; only the assignment filter keeps it out.
func TestGoSharedFileCountedOnceUnderItsAssignedGOOS(t *testing.T) {
	root := goTestRepo(t, "package p\nfunc Parent() {}\n")
	trackedFile(t, root, "both.go", "//go:build darwin || windows\n\npackage p\nfunc Both() {\n"+
		strings.Repeat("println(1)\n", 41)+"}\n")
	// A windows-only file makes the windows pass run, so it can offer both.go a value.
	trackedFile(t, root, "w_windows.go", "package p\nfunc W() {}\n")
	commitFiles(t, root)
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'golangci-lint has version 2.15.0'; exit 0; fi\n" +
		"linter=\"\"\nwhile [ $# -gt 0 ]; do if [ \"$1\" = --enable-only ]; then linter=$2; fi; shift; done\n" +
		"if [ \"$linter\" != funlen ]; then " +
		"printf '%s' '{\"Issues\":[],\"Report\":{\"Linters\":[{\"Name\":\"'\"$linter\"'\",\"Enabled\":true}]}}'; " +
		"exit 0; fi\n" +
		"value=41\nif [ \"$GOOS\" = windows ]; then value=99; fi\n" +
		"printf '%s' \"{\\\"Issues\\\":[{\\\"FromLinter\\\":\\\"funlen\\\",\\\"Text\\\":" +
		"\\\"Func Both is too long ($value > 40)\\\"," +
		"\\\"Pos\\\":{\\\"Filename\\\":\\\"both.go\\\",\\\"Line\\\":4,\\\"Column\\\":1}}],\\\"Report\\\":{\\\"Linters\\\":" +
		"[{\\\"Name\\\":\\\"funlen\\\",\\\"Enabled\\\":true}]}}\"\n" +
		"exit 1\n"
	err := os.WriteFile(filepath.Join(dir, "golangci-lint"), []byte(script), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	values, _, err := GoMetrics(t.Context(), root, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	entries := values[codeshape.MetricGoStatements]["both.go"]
	if len(entries) != 1 || entries[0].Value == 99 {
		t.Fatalf("other GOOS pass leaked into both.go: %#v", entries)
	}
}

// TestGoHostCgoFileStaysMeasured pins the host-GOOS cgo behavior: a file built
// for the host GOOS is measured as before, not reported as unmeasured. Its
// CGO_ENABLED=0 measurement stays a silent 0, as it was on base.
func TestGoHostCgoFileStaysMeasured(t *testing.T) {
	path := "c_" + build.Default.GOOS + ".go"
	root := goTestRepo(t, "package p\nfunc Parent() {}\n")
	trackedFile(t, root, path, "package p\nimport \"C\"\nfunc Cgo() {}\n")
	commitFiles(t, root)
	files, skips, err := parseGoSources(root, ".", []string{path, "x.go"}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 0 {
		t.Fatalf("host cgo file skipped: %#v", skips)
	}
	if got := files[path]; got.goos != build.Default.GOOS || len(got.functions) != 1 {
		t.Fatalf("%s: goos=%q functions=%d", path, got.goos, len(got.functions))
	}
}

func TestGolangciCacheDirUsesCallerRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GOLANGCI_LINT_CACHE", root)
	dir, err := golangciCacheDir("darwin")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "darwin"); dir != want {
		t.Fatalf("cache dir %q, want %q", dir, want)
	}
	_, err = os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
}

func TestGolangciCacheDirDefaultsToUserCache(t *testing.T) {
	t.Setenv("GOLANGCI_LINT_CACHE", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	base, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := golangciCacheDir("darwin")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "golangci-lint-code-shape", "darwin"); dir != want {
		t.Fatalf("cache dir %q, want %q", dir, want)
	}
}
