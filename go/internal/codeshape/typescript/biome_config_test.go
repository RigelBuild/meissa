// SPDX-License-Identifier: MIT OR Apache-2.0

package typescript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/codeshape"
	shapeconfig "github.com/RigelBuild/meissa/go/internal/codeshape/config"
)

func TestGateBiomeConfig(t *testing.T) {
	limits := codeshape.FunctionLimits{Go: codeshape.GoFunctionLimits{Cognitive: 20, Lines: 60, Statements: 40},
		TS: codeshape.TSFunctionLimits{Cognitive: 15, Lines: 50}}
	name, err := writeGateBiomeConfig(t.TempDir(), limits, json.RawMessage(`{"unsafeParameterDecoratorsEnabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	err = json.Unmarshal(data, &config)
	if err != nil {
		t.Fatal(err)
	}
	rules := nestedMap(config, "linter", "rules", "complexity")
	lines := nestedMap(rules, "noExcessiveLinesPerFunction", "options")
	cognitive := nestedMap(rules, "noExcessiveCognitiveComplexity", "options")
	if lines["maxLines"] != float64(50) || cognitive["maxAllowedComplexity"] != float64(15) ||
		nestedMap(config, "javascript", "parser")["unsafeParameterDecoratorsEnabled"] != true {
		t.Fatalf("config=%s", data)
	}
}

func TestGateBiomeIgnoresRepositoryLimit(t *testing.T) {
	root := tsRepo(t, "function f() {\n"+strings.Repeat("console.log(1);\n", 51)+"}\n")
	data, err := os.ReadFile(filepath.Join(root, "biome.json"))
	if err != nil {
		t.Fatal(err)
	}
	trackedFile(t, root, "biome.json", strings.Replace(string(data), `"maxLines":50`, `"maxLines":1000`, 1))
	commitFiles(t, root)
	cfg, _, err := shapeconfig.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	values, _, err := TSMetrics(t.Context(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	entries := values[codeshape.MetricTSLines]["x.ts"]
	if len(entries) != 1 || entries[0].Value <= 50 {
		t.Fatalf("entries=%v", entries)
	}
}
