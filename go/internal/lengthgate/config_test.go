// SPDX-License-Identifier: MIT OR Apache-2.0

package lengthgate

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/lengthgate/judge"
)

// writeTempConfig writes text to a config file in a fresh directory and returns its path.
func writeTempConfig(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "length-gate.json")
	err := os.WriteFile(path, []byte(text), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// The error strings below are the TypeScript contract. Each case isolates one rule in config.ts.
func TestLoadConfigRejectsWithTypeScriptErrors(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "unknown root key",
			text: `{"extensions": [".ts"], "exclude": [], "docs": [], "bogus": 1}`,
			want: `Unknown key "bogus" in config`,
		},
		{
			name: "unknown key inside a document class",
			text: `{"extensions": [], "exclude": [], "docs": [` +
				`{"name": "spec", "include": [], "exclude": [], "target": 1, "markerAllowed": false, "extra": true}]}`,
			want: `Unknown key "extra" in docs[0]`,
		},
		{
			name: "extensions must be an array",
			text: `{"extensions": ".ts", "exclude": [], "docs": []}`,
			want: "extensions must be an array of strings",
		},
		{
			name: "an extension entry must be a string",
			text: `{"extensions": [1], "exclude": [], "docs": []}`,
			want: "extensions[0] must be a string",
		},
		{
			name: "a document class must be an object",
			text: `{"extensions": [], "exclude": [], "docs": ["spec"]}`,
			want: "docs[0] must be an object",
		},
		{
			name: "docs must be an array",
			text: `{"extensions": [], "exclude": [], "docs": {}}`,
			want: "docs must be an array of document classes",
		},
		{
			name: "a class name must be a string",
			text: `{"extensions": [], "exclude": [], "docs": [` +
				`{"name": 3, "include": [], "exclude": [], "target": 1, "markerAllowed": false}]}`,
			want: "docs[0].name must be a string",
		},
		{
			name: "a class target must be a number",
			text: `{"extensions": [], "exclude": [], "docs": [` +
				`{"name": "spec", "include": [], "exclude": [], "target": "1500", "markerAllowed": false}]}`,
			want: "docs[0].target must be a finite number",
		},
		{
			name: "a class markerAllowed must be a boolean",
			text: `{"extensions": [], "exclude": [], "docs": [` +
				`{"name": "spec", "include": [], "exclude": [], "target": 1, "markerAllowed": "no"}]}`,
			want: "docs[0].markerAllowed must be a boolean",
		},
		{
			name: "an unscanned extension is rejected",
			text: `{"extensions": [".md"], "exclude": [], "docs": []}`,
			want: `extensions includes ".md" but no scanner supports it`,
		},
		{
			name: "an invalid exclude glob is rejected at load",
			text: `{"extensions": [], "exclude": ["["], "docs": []}`,
			want: "exclude[0] is not a valid glob",
		},
		{
			name: "an invalid include glob is rejected at load",
			text: `{"extensions": [], "exclude": [], "docs": [` +
				`{"name": "spec", "include": ["["], "exclude": [], "target": 1, "markerAllowed": false}]}`,
			want: "docs[0].include[0] is not a valid glob",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfig(writeTempConfig(t, tc.text))
			if err == nil {
				t.Fatalf("LoadConfig accepted %s", tc.text)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLoadConfigMissingFileNamesPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	_, err := LoadConfig(path)
	missingConfig := err == nil || !strings.Contains(err.Error(), "Cannot load config")
	namesConfigPath := err != nil && strings.Contains(err.Error(), path)
	if missingConfig || !namesConfigPath {
		t.Fatalf("error = %v, want it to name the path", err)
	}
}

func TestLoadConfigAcceptsValidConfigInDocumentOrder(t *testing.T) {
	text := `{
		"extensions": [".ts"],
		"exclude": ["vendor/**"],
		"docs": [
			{"name": "spec", "include": ["docs/specs/**/*.md"], "exclude": [], "target": 1500, "markerAllowed": false},
			{"name": "repo doc", "include": ["docs/**/*.md"], "exclude": ["docs/designs/**"],
				"target": 1500, "markerAllowed": true}
		]
	}`
	cfg, err := LoadConfig(writeTempConfig(t, text))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !slices.Equal(cfg.Extensions, []string{".ts"}) || !slices.Equal(cfg.Exclude, []string{"vendor/**"}) {
		t.Fatalf("extensions/exclude = %v/%v", cfg.Extensions, cfg.Exclude)
	}
	if len(cfg.Docs) != 2 || cfg.Docs[0].Name != "spec" || cfg.Docs[1].Name != "repo doc" {
		t.Fatalf("docs = %+v, want spec then repo doc", cfg.Docs)
	}
}

// The first matching document class wins, as the TypeScript docClassFor does.
func TestFirstMatchingDocClassWins(t *testing.T) {
	classes := docConfig.Docs
	if got := judge.DocClassFor("docs/specs/gate.md", classes); got == nil || got.Name != "spec" {
		t.Fatalf("docClassFor(specs) = %+v, want spec", got)
	}
	if got := judge.DocClassFor("docs/designs/record.md", classes); got == nil || got.Name != "design record" {
		t.Fatalf("docClassFor(designs) = %+v, want design record", got)
	}
	if got := judge.DocClassFor("docs/notes/guide.md", classes); got == nil || got.Name != "repo doc" {
		t.Fatalf("docClassFor(notes) = %+v, want repo doc", got)
	}
}
