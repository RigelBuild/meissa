// SPDX-License-Identifier: MIT OR Apache-2.0

package commentscan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TS is clean, Go fails closed because the raw tree has no named string under jsx_attribute.
var failClosedDivergences = map[string]struct{}{
	"jsx/finding5-ampersand-before-line-comment.tsx": {},
	"jsx/finding5-lone-ampersand.tsx":                {},
	"jsx/finding5-query-ampersand.tsx":               {},
	"jsx/finding5-trailing-ampersand.tsx":            {},
}

type scannerCorpusGolden struct {
	Revision string            `json:"revision"`
	Path     string            `json:"path"`
	Corpus   string            `json:"corpus"`
	Scan     scannerCorpusScan `json:"scan"`
}

type scannerCorpusScan struct {
	Residues  []string `json:"residues"`
	Ambiguous bool     `json:"ambiguous"`
}

func TestCorpus(t *testing.T) {
	tests := []struct {
		name      string
		extension string
		scan      func(string) Residues
	}{
		{name: "tsx", extension: ".tsx", scan: ScanJSX},
		{name: "jsx", extension: ".tsx", scan: ScanJSX},
		{name: "astro", extension: ".astro", scan: ScanAstro},
	}
	for _, test := range tests {
		paths, err := filepath.Glob(filepath.Join("testdata", "corpus", test.name, "*"+test.extension))
		if err != nil {
			t.Fatal(err)
		}
		if len(paths) == 0 {
			t.Fatalf("no %s corpus files found", test.name)
		}
		for _, path := range paths {
			caseName := filepath.ToSlash(filepath.Join(test.name, filepath.Base(path)))
			t.Run(caseName, func(t *testing.T) {
				err := t.Context().Err()
				if err != nil {
					t.Fatal(err)
				}
				input, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				goldenPath := filepath.Join("testdata", "ts-golden", test.name, filepath.Base(path)+".json")
				goldenData, err := os.ReadFile(goldenPath)
				if err != nil {
					t.Fatal(err)
				}
				var golden scannerCorpusGolden
				err = json.Unmarshal(goldenData, &golden)
				if err != nil {
					t.Fatal(err)
				}
				got := test.scan(string(input))
				want := Residues{Residues: golden.Scan.Residues, Ambiguous: golden.Scan.Ambiguous}
				if _, ok := failClosedDivergences[caseName]; ok {
					if golden.Scan.Ambiguous {
						t.Fatal("fail-closed divergence golden is no longer clean")
					}
					want.Ambiguous = true
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("scan differs from %s: got %+v, want %+v", goldenPath, got, want)
				}
				if strings.HasPrefix(filepath.Base(path), "broken-") && !got.Ambiguous {
					t.Fatal("broken corpus input is not ambiguous")
				}
			})
		}
	}
}
