// SPDX-License-Identifier: MIT OR Apache-2.0

package typescript

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/codeshape"
	shapeconfig "github.com/RigelBuild/meissa/go/internal/codeshape/config"
)

const tsBiomeConfig = `{"linter":{"rules":{"complexity":{"noExcessiveCognitiveComplexity":` +
	`{"level":"off","options":{"maxAllowedComplexity":15}},` +
	`"noExcessiveLinesPerFunction":{"level":"off","options":{"maxLines":50}}}}}}`

func tsRepo(t *testing.T, source string) string {
	t.Helper()
	root := newFixtureRepo(t)
	writeConfigFiles(t, root, strings.Replace(validConfig, `".go", ".cue"`, `".go", ".cue", ".ts"`, 1), "files: {}\n")
	trackedFile(t, root, "biome.json", tsBiomeConfig)
	trackedFile(t, root, "x.ts", source)
	commitFiles(t, root)
	return root
}
func TestTSOnlyOffUsesOptions(t *testing.T) {
	root := tsRepo(t, "function f(x: boolean) {\n"+strings.Repeat("if (x) { console.log(x); }\n", 16)+
		strings.Repeat("console.log(x);\n", 40)+"}\n")
	binary, err := exec.LookPath("biome")
	if err != nil {
		t.Fatal(err)
	}
	report, err := runBiome(t.Context(), binary, root, []string{"x.ts"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, d := range report.Diagnostics {
		seen[d.Category] = true
	}
	if len(seen) != 2 {
		t.Fatalf("disabled rules did not both report: %#v", report.Diagnostics)
	}
}

func TestTSIgnoredFileIsSkipped(t *testing.T) {
	root := tsRepo(t, "function f() {}\n")
	config := strings.Replace(tsBiomeConfig, `{`, `{"files":{"includes":["**", "!ignored.ts"]},`, 1)
	if err := os.WriteFile(filepath.Join(root, "biome.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	trackedFile(t, root, "ignored.ts", "function ignored() {}\n")
	commitFiles(t, root)
	cfg, _, err := shapeconfig.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	values, skips, err := TSMetrics(t.Context(), root, cfg)
	if err != nil || len(skips) != 1 || skips[0].Path != "ignored.ts" || len(values[codeshape.MetricTSLines]["ignored.ts"]) != 0 {
		t.Fatalf("values=%v skips=%v err=%v", values, skips, err)
	}
}

func TestTSGeneratorAndUnicodePosition(t *testing.T) {
	source := []byte("const s = 'é😀'; function* generate() { yield 1; }")
	funcs, tree, err := parseTSFunctions("x.ts", source)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Release()
	fn, err := findTSFunction(funcs, 1, 19, source)
	if err != nil || fn.id != "generate" {
		t.Fatalf("function=%v err=%v", fn, err)
	}
}

func TestTSStructuralIdentity(t *testing.T) {
	ids := func(source string) []string {
		functions, tree, err := parseTSFunctions("x.ts", []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		defer tree.Release()
		result := []string{}
		for _, fn := range functions {
			if strings.Contains(fn.id, "big/") || strings.HasPrefix(fn.id, "A/") {
				result = append(result, fn.id)
			}
		}
		return result
	}
	a := ids(`function big() { xs.map(() => 1) } class A { run() {} }`)
	b := ids(`ys.map(() => 0); class B { run() {} } function big() { xs.map(() => 1) } class A { run() {} }`)
	if len(a) != 2 || strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("ids %v %v", a, b)
	}
}

func TestTSManyOffenders(t *testing.T) {
	var source strings.Builder
	for i := range 25 {
		fmt.Fprintf(&source, "function f%d() {\n%s}\n", i, strings.Repeat("console.log(1);\n", 51))
	}
	root := tsRepo(t, source.String())
	cfg, _, err := shapeconfig.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	values, _, err := TSMetrics(t.Context(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range values[codeshape.MetricTSLines]["x.ts"] {
		if e.Value > 50 {
			count++
		}
	}
	if count != 25 {
		t.Fatalf("got %d offenders", count)
	}
}
func TestTSCallbackIdentity(t *testing.T) {
	ids := func(source string) []string {
		funcs, tree, err := parseTSFunctions("x.ts", []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		defer tree.Release()
		var ids []string
		for _, f := range funcs {
			if strings.Contains(f.id, "suite/anonymous") {
				ids = append(ids, f.id)
			}
		}
		return ids
	}
	a := ids(`describe("suite", () => { helper(() => {}); helper(() => {}); });`)
	b := ids(`function other() {} describe("suite", () => { helper(() => {}); helper(() => {}); });`)
	if len(a) < 2 || strings.Join(a, ",") != strings.Join(b, ",") || a[len(a)-1] == a[len(a)-2] {
		t.Fatalf("unstable ids %v %v", a, b)
	}
}
func TestTSNestedDescribeIdentity(t *testing.T) {
	funcs, tree, err := parseTSFunctions("x.ts", []byte(
		`describe("outer", () => { describe("inner", () => { test("works", () => {}); }); });`,
	))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Release()
	for _, f := range funcs {
		if strings.Contains(f.id, "outer/inner/works/") {
			return
		}
	}
	t.Fatalf("missing nested title: %#v", funcs)
}
