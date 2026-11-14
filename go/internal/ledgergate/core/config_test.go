// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:lll,noinlineerr // Keep TS-derived cases direct and readable.
package core

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadConfigRejectsInvalidShapes(t *testing.T) {
	cases := map[string]string{
		"unknown key":                  `{"designsRoot":"docs/designs","surfaceDepth":0,"governedRoots":["ui"],"counter":{"url":"x","partition":"p"},"unexpected":true}`,
		"wrong type":                   `{"designsRoot":"docs/designs","surfaceDepth":"0","governedRoots":["ui"],"counter":{"url":"x","partition":"p"}}`,
		"missing depth-zero roots":     `{"designsRoot":"docs/designs","surfaceDepth":0,"counter":{"url":"x","partition":"p"}}`,
		"unknown recordStatusScope":    `{"designsRoot":"docs/designs","surfaceDepth":1,"recordStatusScope":"some","counter":{"url":"x","partition":"p"}}`,
		"empty governed roots":         `{"designsRoot":"docs/designs","surfaceDepth":0,"governedRoots":[],"counter":{"url":"x","partition":"p"}}`,
		"empty exempt branch prefix":   `{"designsRoot":"docs/designs","surfaceDepth":1,"exemptBranches":[{"prefix":"","author":"bot[bot]"}],"counter":{"url":"x","partition":"p"}}`,
		"non-array surfaces":           `{"designsRoot":"docs/designs","surfaceDepth":1,"surfaces":42,"counter":{"url":"x","partition":"p"}}`,
		"empty surfaces":               `{"designsRoot":"docs/designs","surfaceDepth":1,"surfaces":[],"counter":{"url":"x","partition":"p"}}`,
		"multiple depth-zero surfaces": `{"designsRoot":"docs/designs","surfaceDepth":0,"governedRoots":["ui"],"surfaces":["alpha","beta"],"counter":{"url":"x","partition":"p"}}`,
		"duplicate surfaces":           `{"designsRoot":"docs/designs","surfaceDepth":1,"surfaces":["alpha","alpha"],"counter":{"url":"x","partition":"p"}}`,
		"surface path":                 `{"designsRoot":"docs/designs","surfaceDepth":1,"surfaces":["../alpha"],"counter":{"url":"x","partition":"p"}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(writeTemp(t, body)); err == nil {
				t.Fatalf("LoadConfig accepted %s", name)
			}
		})
	}
}

func TestLoadConfigRejectsLegacyExemptionKeys(t *testing.T) {
	for _, key := range []string{"exemptBranchPrefixes", "exemptAuthors"} {
		t.Run(key, func(t *testing.T) {
			body := fmt.Sprintf(`{"designsRoot":"docs/designs","surfaceDepth":1,"counter":{"url":"x","partition":"p"},%q:[]}`, key)
			_, err := LoadConfig(writeTemp(t, body))
			if err == nil || !strings.Contains(err.Error(), "use exemptBranches") {
				t.Fatalf("err = %v, want migration guidance", err)
			}
		})
	}
}

func TestLoadConfigRejectsMalformedExemptBranches(t *testing.T) {
	for _, entry := range []string{
		`{"prefix":"renovate/"}`,
		`{"prefix":"renovate/","author":""}`,
		`{"prefix":"renovate/","author":"bot[bot]","extra":true}`,
	} {
		t.Run(entry, func(t *testing.T) {
			body := `{"designsRoot":"docs/designs","surfaceDepth":1,"counter":{"url":"x","partition":"p"},"exemptBranches":[` + entry + `]}`
			if _, err := LoadConfig(writeTemp(t, body)); err == nil {
				t.Fatalf("LoadConfig accepted %s", entry)
			}
		})
	}
}

func TestLoadConfigDefaultsCitationAmbiguousPaths(t *testing.T) {
	cfg, err := LoadConfig(writeTemp(t, `{"designsRoot":"docs/designs","surfaceDepth":1,"counter":{"url":"x","partition":"p"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CitationAmbiguousPaths == nil || len(cfg.CitationAmbiguousPaths) != 0 {
		t.Fatalf("CitationAmbiguousPaths = %#v, want empty non-nil", cfg.CitationAmbiguousPaths)
	}
}

func TestLoadConfigAcceptsCitationAmbiguousPaths(t *testing.T) {
	cfg, err := LoadConfig(writeTemp(t, `{"designsRoot":"docs/designs","surfaceDepth":1,"citationAmbiguousPaths":["shared/README.md"],"counter":{"url":"x","partition":"p"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.CitationAmbiguousPaths) != 1 || cfg.CitationAmbiguousPaths[0] != "shared/README.md" {
		t.Fatalf("CitationAmbiguousPaths = %v", cfg.CitationAmbiguousPaths)
	}
}

func TestLoadConfigRejectsBadCitationAmbiguousPaths(t *testing.T) {
	cases := map[string]string{
		"non-string entry": `"citationAmbiguousPaths":["shared/README.md",3]`,
		"absolute entry":   `"citationAmbiguousPaths":["../shared/README.md"]`,
	}
	for name, field := range cases {
		t.Run(name, func(t *testing.T) {
			body := `{"designsRoot":"docs/designs","surfaceDepth":1,` + field + `,"counter":{"url":"x","partition":"p"}}`
			if _, err := LoadConfig(writeTemp(t, body)); err == nil {
				t.Fatalf("LoadConfig accepted %s", name)
			}
		})
	}
}

func TestLoadConfigAcceptsSurfacesForBothDepths(t *testing.T) {
	cfg, err := LoadConfig(writeTemp(t, `{"designsRoot":"docs/designs","surfaceDepth":0,"governedRoots":["ui"],"surfaces":["alpha"],"counter":{"url":"x","partition":"p"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Surfaces) != 1 || cfg.Surfaces[0] != "alpha" {
		t.Fatalf("depth-zero surfaces = %v", cfg.Surfaces)
	}
	cfg, err = LoadConfig(writeTemp(t, `{"designsRoot":"docs/designs","surfaceDepth":1,"surfaces":["alpha","beta"],"counter":{"url":"x","partition":"p"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Surfaces) != 2 || cfg.Surfaces[0] != "alpha" || cfg.Surfaces[1] != "beta" {
		t.Fatalf("depth-one surfaces = %v", cfg.Surfaces)
	}
}

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigDefaultsN1Keys(t *testing.T) {
	config := `{"designsRoot":"docs/designs","surfaceDepth":1,"counter":{"url":"x","partition":"p"}}`
	cfg, err := LoadConfig(writeTemp(t, config))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RequireSubArea || cfg.TitleScope != "off" || len(cfg.ArchiveRoots) != 0 {
		t.Fatalf("N1 defaults = sub-area %t, title scope %q, archive roots %v", cfg.RequireSubArea, cfg.TitleScope, cfg.ArchiveRoots)
	}
}

func TestLoadConfigRoundTripsN1Keys(t *testing.T) {
	config := `{"designsRoot":"docs/designs","surfaceDepth":1,"requireSubArea":true,"titleScope":"changed","archiveRoots":["docs/archive","docs/old"],"counter":{"url":"x","partition":"p"}}`
	cfg, err := LoadConfig(writeTemp(t, config))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.RequireSubArea || cfg.TitleScope != "changed" || !slices.Equal(cfg.ArchiveRoots, []string{"docs/archive", "docs/old"}) {
		t.Fatalf("N1 config did not round trip: %+v", cfg)
	}
}

func TestLoadConfigRejectsInvalidN1Keys(t *testing.T) {
	cases := map[string]string{
		"requireSubArea as string":    `"requireSubArea":"yes"`,
		"titleScope unknown":          `"titleScope":"some"`,
		"titleScope not a string":     `"titleScope":true`,
		"archiveRoots dotdot":         `"archiveRoots":["../x"]`,
		"archiveRoots non-string":     `"archiveRoots":["docs/archive",3]`,
		"archiveRoots trailing slash": `"archiveRoots":["docs/archive/"]`,
		"archiveRoots dot prefix":     `"archiveRoots":["./docs/archive"]`,
	}
	for name, field := range cases {
		t.Run(name, func(t *testing.T) {
			body := `{"designsRoot":"docs/designs","surfaceDepth":1,` + field + `,"counter":{"url":"x","partition":"p"}}`
			_, err := LoadConfig(writeTemp(t, body))
			if err == nil {
				t.Fatalf("LoadConfig accepted %s", name)
			}
		})
	}
}

func TestLoadConfigRejectsOverlappingArchiveRoots(t *testing.T) {
	cases := map[string]string{
		"equals designsRoot":   `"archiveRoots":["docs/designs"]`,
		"under designsRoot":    `"archiveRoots":["docs/designs/archive"]`,
		"contains designsRoot": `"archiveRoots":["docs"]`,
		"nested in another":    `"archiveRoots":["docs/archive","docs/archive/old"]`,
		"same root twice":      `"archiveRoots":["docs/archive","docs/archive"]`,
	}
	for name, field := range cases {
		t.Run(name, func(t *testing.T) {
			body := `{"designsRoot":"docs/designs","surfaceDepth":1,` + field + `,"counter":{"url":"x","partition":"p"}}`
			_, err := LoadConfig(writeTemp(t, body))
			if err == nil {
				t.Fatalf("LoadConfig accepted %s", name)
			}
		})
	}
}

func TestLoadConfigAcceptsArchiveRootsOnPathBoundary(t *testing.T) {
	config := `{"designsRoot":"docs/designs","surfaceDepth":1,"archiveRoots":["docs/designs-old","docs/archive"],"counter":{"url":"x","partition":"p"}}`
	cfg, err := LoadConfig(writeTemp(t, config))
	if err != nil {
		t.Fatalf("LoadConfig rejected a sibling directory: %v", err)
	}
	if !slices.Equal(cfg.ArchiveRoots, []string{"docs/designs-old", "docs/archive"}) {
		t.Fatalf("ArchiveRoots = %v", cfg.ArchiveRoots)
	}
}
