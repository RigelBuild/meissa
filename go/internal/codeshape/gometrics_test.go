// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	cfg.Functions = FunctionLimits{Go: GoFunctionLimits{Cognitive: 20, Lines: 60, Statements: 40},
		TS: TSFunctionLimits{Cognitive: 0, Lines: 0}}
	values, _, err := GoMetrics(t.Context(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range values[MetricGoStatements]["fixture.go"] {
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
	report, err := runGolangci(t.Context(), binary, root, ".", "funlen", 10, 10, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Issues) != 25 {
		t.Fatalf("one funlen run reported %d issues for 25 functions exceeding both limits", len(report.Issues))
	}
}

func TestGoFunctionIdentity(t *testing.T) {
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
	findings, err := goSuppressionFindings(t.Context(), root, testConfig())
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
func runGoGate(t *testing.T, root string) (int, string, string) {
	t.Helper()
	var out, errout strings.Builder
	code := Main(t.Context(), []string{root}, func(string) string { return "" }, &out, &errout)
	return code, out.String(), errout.String()
}
func TestGoLimitsCLI(t *testing.T) {
	var source strings.Builder
	source.WriteString("package p\nfunc Cognitive(x int) {\n")
	for range 21 {
		source.WriteString("if x > 0 { println(x) }\n")
	}
	source.WriteString("}\nfunc Lines() {\n")
	for range 60 {
		source.WriteString("\n")
	}
	source.WriteString("println(1)\n}\nfunc Statements() {\n")
	for range 41 {
		source.WriteString("println(1)\n")
	}
	source.WriteString("}\n")
	root := goTestRepo(t, source.String())
	code, out, errout := runGoGate(t, root)
	if code != 1 || !strings.Contains(out, "go-cognitive value=21") ||
		!strings.Contains(out, "go-lines value=61") || !strings.Contains(out, "go-statements value=41") {
		t.Fatalf("code=%d out=%s stderr=%s", code, out, errout)
	}
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
		entries := values[MetricGoStatements][path]
		if len(entries) != 1 || entries[0].Value != 41 {
			t.Fatalf("%s: %#v", path, entries)
		}
	}
	if len(values[MetricGoStatements]) != 2 {
		t.Fatalf("unexpected paths: %#v", values[MetricGoStatements])
	}
}

func TestGoExcludedAndUnbuiltFiles(t *testing.T) {
	root := goTestRepo(t, "package p\nfunc Parent() {}\n")
	offender := "package fixture\nfunc Large() {\n" + strings.Repeat("println(1)\n", 41) + "}\n"
	trackedFile(t, root, "fixtures/x.go", offender)
	trackedFile(t, root, "excluded/x.go", offender)
	trackedFile(t, root, "tagged.go", "//go:build integration\n\n"+offender)
	trackedFile(t, root, "platform_windows.go", offender)
	commitFiles(t, root)
	cfg := testConfig()
	cfg.Exclude = []string{"excluded/**"}
	values, skips, err := GoMetrics(t.Context(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 2 || len(values[MetricGoStatements]) != 1 {
		t.Fatalf("skips=%v measurements=%v", skips, values[MetricGoStatements])
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
	_, err = goSuppressionFindings(t.Context(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
}
func TestGoMissingBinary(t *testing.T) {
	root := goTestRepo(t, "package p\nfunc F() {}\n")
	dir := t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	err = os.Symlink(git, filepath.Join(dir, "git"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	code, _, errout := runGoGate(t, root)
	if code != 2 || !strings.Contains(errout, "golangci-lint") {
		t.Fatalf("code=%d stderr=%s", code, errout)
	}
}
func fakeGoLint(t *testing.T, output string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s' '" + output + "'\nexit 1\n"
	err := os.WriteFile(filepath.Join(dir, "golangci-lint"), []byte(script), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
func TestGoTruncatedOutput(t *testing.T) {
	root := goTestRepo(t, "package p\nfunc F() {}\n")
	fakeGoLint(t, `{"Issues":[`)
	code, _, errout := runGoGate(t, root)
	if code != 2 || !strings.Contains(errout, "parse golangci-lint") {
		t.Fatalf("code=%d stderr=%s", code, errout)
	}
}
func TestGoUnparseableIssue(t *testing.T) {
	root := goTestRepo(t, "package p\nfunc F() {}\n")
	fakeGoLint(t, `{"Issues":[{"FromLinter":"gocognit","Text":"unexpected",`+
		`"Pos":{"Filename":"x.go","Line":2,"Column":1}}],`+
		`"Report":{"Linters":[{"Name":"gocognit","Enabled":true}]}}`)
	code, _, errout := runGoGate(t, root)
	if code != 2 || !strings.Contains(errout, "cannot parse") {
		t.Fatalf("code=%d stderr=%s", code, errout)
	}
}
func TestGoBareAndAllSuppressions(t *testing.T) {
	root := goTestRepo(t, "package p\n//nolint\n//nolint:all\nfunc F() {}\n")
	findings, err := goSuppressionFindings(t.Context(), root, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []int{2, 3} {
		for _, metric := range []Metric{MetricGoCognitive, MetricGoLines} {
			found := false
			for _, finding := range findings {
				if finding.Path == fmt.Sprintf("x.go:%d", line) && finding.Value == line && finding.Metric == metric {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s line %d: %#v", metric, line, findings)
			}
		}
	}
}
