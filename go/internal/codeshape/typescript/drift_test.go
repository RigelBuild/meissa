// SPDX-License-Identifier: MIT OR Apache-2.0

package typescript

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/codeshape"
)

func TestBiomeDriftMissingOptions(t *testing.T) {
	cfg := codeshape.Config{FileLimit: 500, DirLimit: 10, Extensions: []string{}, Exclude: []string{},
		DirLimits: map[string]codeshape.DirLimit{}, Functions: codeshape.FunctionLimits{
			Go: codeshape.GoFunctionLimits{Cognitive: 20, Lines: 60, Statements: 40},
			TS: codeshape.TSFunctionLimits{Cognitive: 15, Lines: 50}}}
	findings := biomeDriftFindings("biome.json", map[string]any{}, cfg, true)
	if len(findings) != 2 {
		t.Fatalf("findings = %#v", findings)
	}
	config := map[string]any{"linter": map[string]any{"rules": map[string]any{"complexity": map[string]any{
		"noExcessiveCognitiveComplexity": map[string]any{
			"level": "off", "options": map[string]any{"maxAllowedComplexity": 15}},
		"noExcessiveLinesPerFunction": map[string]any{"level": "off", "options": map[string]any{"maxLines": 50}},
	}}}}
	findings = biomeDriftFindings("biome.json", config, cfg, true)
	if len(findings) != 2 {
		t.Fatalf("disabled rules: %#v", findings)
	}
}

func TestBiomeJSONCAndMissingConfig(t *testing.T) {
	root := t.TempDir()
	_, err := readBiomeConfig(root, "biome.json")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected missing error: %v", err)
	}
	err = os.WriteFile(filepath.Join(root, "biome.jsonc"), []byte("{/* comment */}"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = readBiomeConfig(root, "biome.jsonc")
	if err != nil {
		t.Fatal(err)
	}
}

func TestTrackedBiomeDrift(t *testing.T) {
	root := newFixtureRepo(t)
	trackedFile(t, root, "src/x.ts", "function f() {}\n")
	trackedFile(t, root, "nested/biome.jsonc", `{"linter":{"rules":{"complexity":`+
		`{"noExcessiveLinesPerFunction":{"level":"off","options":{"maxLines":50}}}}}}`)
	trackedFile(t, root, "biome.json", `{"linter":{"rules":{"complexity":{`+
		`"noExcessiveCognitiveComplexity":{"level":"warn","options":{"maxAllowedComplexity":15}},`+
		`"noExcessiveLinesPerFunction":{"level":"warn","options":{"maxLines":50}}}}},`+
		`"overrides":[{"linter":{"rules":{"complexity":`+
		`{"noExcessiveLinesPerFunction":{"level":"off","options":{"maxLines":50}}}}}}]}`)
	commitFiles(t, root)
	cfg := codeshape.Config{FileLimit: 500, DirLimit: 10, Extensions: []string{".ts"}, Exclude: []string{},
		DirLimits: map[string]codeshape.DirLimit{}, Functions: codeshape.FunctionLimits{
			Go: codeshape.GoFunctionLimits{Cognitive: 20, Lines: 60, Statements: 40},
			TS: codeshape.TSFunctionLimits{Cognitive: 15, Lines: 50}}}
	findings, err := PolicyFindings(t.Context(), root, cfg)
	if err != nil || len(findings) != 2 {
		t.Fatalf("findings=%#v err=%v", findings, err)
	}
	paths := map[string]bool{}
	for _, finding := range findings {
		paths[finding.Path] = true
	}
	if !paths["biome.json#overrides[0]"] || !paths["nested/biome.jsonc"] {
		t.Fatalf("wrong drift paths: %#v", findings)
	}
}
