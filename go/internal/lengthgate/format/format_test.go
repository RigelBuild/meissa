// SPDX-License-Identifier: MIT OR Apache-2.0

package format

import (
	"slices"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/lengthgate/model"
)

// docConfig mirrors the document classes the TypeScript DOC_CONFIG uses for format output.
var docConfig = model.Config{
	Extensions: []string{".ts"},
	Exclude:    nil,
	Docs: []model.DocClass{
		{
			Name: "spec", Include: []string{"docs/specs/**/*.md"}, Exclude: []string{},
			Target: 1500, MarkerAllowed: false,
		},
		{
			Name: "design record", Include: []string{"docs/designs/**/*.md"}, Exclude: []string{},
			Target: 2500, MarkerAllowed: true,
		},
		{
			Name: "repo doc", Include: []string{"docs/**/*.md"}, Exclude: []string{"docs/designs/**"},
			Target: 1500, MarkerAllowed: true,
		},
	},
}

func TestFormatSpecOutputNamesTargetAndSuggestsSplit(t *testing.T) {
	finding := model.DocFinding{
		Path:      "docs/specs/gate.md",
		DocClass:  "spec",
		Words:     1600,
		BaseWords: 0,
		Target:    1500,
	}
	lines := Finding(finding, docConfig)
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %#v", lines)
	}
	if !strings.Contains(Finding(model.DocFinding{
		Path: "docs/specs/gate.md", DocClass: "repo doc", Words: 1600, BaseWords: 0, Target: 1500,
	}, docConfig)[1], "LENGTH marker") {
		t.Fatalf("repo doc fix line must mention a LENGTH marker")
	}
	if !strings.Contains(lines[0], "(spec target 1,500)") {
		t.Fatalf("header %q must name the target as 1,500", lines[0])
	}
	if !strings.Contains(strings.ToLower(lines[1]), "split") {
		t.Fatalf("fix line %q must recommend splitting", lines[1])
	}
	if strings.Contains(strings.ToLower(lines[1]), "marker") {
		t.Fatalf("spec fix line %q must not mention a marker", lines[1])
	}
}

func TestFormatBadMarkerAndUnscannableOutput(t *testing.T) {
	bad := model.BadMarkerFinding{Path: "src/file.ts", Line: 4, Reason: "reason must have 3 words"}
	wantBad := []string{
		"src/file.ts:4: bad LENGTH marker: reason must have 3 words",
		"Fix the reason or § section, or remove the marker.",
	}
	if got := Finding(bad, docConfig); !slices.Equal(got, wantBad) {
		t.Fatalf("bad-marker output = %#v, want %#v", got, wantBad)
	}
	unscannable := model.UnscannableFinding{Path: "src/file.ts"}
	got := Finding(unscannable, docConfig)
	if len(got) != 2 {
		t.Fatalf("unscannable output = %#v", got)
	}
	noScannerMessage := got[0] == "src/file.ts: no scanner could place comments"
	hasScannerAdvice := strings.Contains(got[1], "add scanner support")
	if !noScannerMessage || !hasScannerAdvice {
		t.Fatalf("unscannable output = %#v", got)
	}
}

func TestFormatCommentOutput(t *testing.T) {
	got := Finding(model.CommentFinding{Path: "src/file.ts", Line: 3, Lines: 5, BaseLines: 0}, docConfig)
	want := []string{
		"src/file.ts:3: 5-line comment block (ceiling 4)",
		"Trim it to 4 lines, or add a LENGTH marker with a reason.",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("comment output = %#v, want %#v", got, want)
	}
}
