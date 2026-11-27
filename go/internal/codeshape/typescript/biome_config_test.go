// SPDX-License-Identifier: MIT OR Apache-2.0

package typescript

import (
	"encoding/json"
	"errors"
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
	name, err := writeGateBiomeConfig(t.TempDir(), limits,
		json.RawMessage(`{"unsafeParameterDecoratorsEnabled":true}`), 1048576)
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

func TestGateBiomeMeasuresLargeFile(t *testing.T) {
	root := tsRepo(t, "function large() {\n"+strings.Repeat(" ", 1100000)+"\n}\n")
	cfg, _, err := shapeconfig.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	values, _, err := TSMetrics(t.Context(), root, cfg)
	if err != nil || len(values[codeshape.MetricTSLines]["x.ts"]) != 1 {
		t.Fatalf("values=%v err=%v", values, err)
	}
}

func TestValidateBiomeErrorsIncludesRejectedDiagnostic(t *testing.T) {
	const diagnosticPath = "src/example.ts"
	const diagnosticMessage = "unable to parse suppression directive"
	const reportJSON = `{
		"summary": {"errors": 1},
		"diagnostics": [{
			"category": "suppressions/parse",
			"severity": "error",
			"message": "unable to parse suppression directive",
			"location": {"path": "src/example.ts"}
		}]
	}`

	var report biomeReport
	err := json.Unmarshal([]byte(reportJSON), &report)
	if err != nil {
		t.Fatal(err)
	}

	err = validateBiomeErrors(report)
	if !errors.Is(err, errBiomeReported) {
		t.Fatalf("validateBiomeErrors() error = %v, want wrapped %v", err, errBiomeReported)
	}
	if !strings.Contains(err.Error(), diagnosticPath) || !strings.Contains(err.Error(), diagnosticMessage) {
		t.Fatalf(
			"validateBiomeErrors() error = %q, want path %q and message %q",
			err,
			diagnosticPath,
			diagnosticMessage,
		)
	}
}