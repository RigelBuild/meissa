// SPDX-License-Identifier: MIT OR Apache-2.0

package typescript_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/codeshape/model"
	"github.com/RigelBuild/meissa/go/internal/codeshape/testsupport"
	"github.com/RigelBuild/meissa/go/internal/codeshape/typescript"
)

func TestSuppressionFindingsCoverBiomeGroups(t *testing.T) {
	for _, tc := range []struct {
		name, directive string
	}{
		{"bare lint", "// biome-ignore lint: reason"},
		{"complexity group", "// biome-ignore lint/complexity: reason"},
		{"all directive", "// biome-ignore-all lint/complexity: reason"},
		{"start directive", "// biome-ignore-start lint/complexity: reason"},
		{"single rule", "// biome-ignore lint/complexity/noExcessiveLinesPerFunction: reason"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := typescriptSuppressionLineFindings("src/a.ts", []byte(tc.directive+"\n"))
			if len(findings) != 2 || findings[0].Value != 1 || findings[0].Path != "src/a.ts:1" {
				t.Fatalf("findings = %#v", findings)
			}
		})
	}
}

func TestBiomeDriftChecksJSONCAndOverrides(t *testing.T) {
	root := testsupport.NewFixtureRepo(t)
	testsupport.TrackedFile(t, root, "src/a.ts", "function f() {}\n")
	testsupport.TrackedFile(t, root, "biome.jsonc", `{"linter":{"rules":{"complexity":{"noExcessiveCognitiveComplexity":{"level":"off","options":{"maxAllowedComplexity":15}},"noExcessiveLinesPerFunction":{"level":"off","options":{"maxLines":50}}}}},"overrides":[{"linter":{"rules":{"complexity":{"noExcessiveLinesPerFunction":{"level":"off","options":{"maxLines":3}}}}}}]}`)
	testsupport.CommitFiles(t, root)
	paths, err := trackedPaths(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	findings, err := typescript.SuppressionFindings(paths, model.Config{Extensions: []string{".ts"}, Functions: model.FunctionLimits{TS: model.TSFunctionLimits{Cognitive: 15, Lines: 50}}}, repository)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Path != "biome.jsonc" || findings[0].Limit != 50 {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestBiomeSuppressionFindingsDoNotReadOutsideRoot(t *testing.T) {
	root := testsupport.NewFixtureRepo(t)
	outside := filepath.Join(t.TempDir(), "outside.ts")
	if err := os.WriteFile(outside, []byte("// biome-ignore lint: reason\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.ts")); err != nil {
		t.Fatal(err)
	}
	repository, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if _, err := typescript.SuppressionFindings([]string{"escape.ts"}, model.Config{Extensions: []string{".ts"}}, repository); err == nil {
		t.Fatal("suppression scan followed symlink outside repository")
	}
}

func typescriptSuppressionLineFindings(path string, source []byte) []model.Finding {
	return typescript.ParseSuppressionsForTest(path, source)
}

func TestTSIdentityHandlesSameNamedMethodsAndNestedCallbacks(t *testing.T) {
	first := `function big() { xs.map(() => 1) } class A { run() {} } class B { run() {} }`
	second := `ys.map(() => 0); function big() { xs.map(() => 1) } class A { run() {} } class B { run() {} }`
	a, err := typescript.FunctionIDs("x.ts", []byte(first))
	if err != nil {
		t.Fatal(err)
	}
	b, err := typescript.FunctionIDs("x.ts", []byte(second))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(a, ","), "A/run") || !strings.Contains(strings.Join(a, ","), "B/run") || !strings.Contains(strings.Join(a, ","), "big/closure-1") {
		t.Fatalf("ids = %v", a)
	}
	if !strings.Contains(strings.Join(b, ","), "big/closure-1") {
		t.Fatalf("sibling insertion changed ids = %v", b)
	}
}
