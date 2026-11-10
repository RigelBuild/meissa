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
