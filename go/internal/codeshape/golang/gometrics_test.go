// SPDX-License-Identifier: MIT OR Apache-2.0

package golang

import (
	"fmt"
	"go/parser"
	"go/token"
	"os/exec"
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
	trackedFile(t, root, "platform_windows.go", offender)
	commitFiles(t, root)
	cfg := testConfig()
	cfg.Exclude = []string{"excluded/**"}
	values, skips, err := GoMetrics(t.Context(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 2 || len(values[codeshape.MetricGoStatements]) != 1 {
		t.Fatalf("skips=%v measurements=%v", skips, values[codeshape.MetricGoStatements])
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
