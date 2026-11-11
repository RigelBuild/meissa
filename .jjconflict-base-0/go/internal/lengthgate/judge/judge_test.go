// SPDX-License-Identifier: MIT OR Apache-2.0

package judge

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/commentscan"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/model"
)

// docConfig mirrors the TypeScript DOC_CONFIG used by the doc ratchet tests.
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

// diffFile builds a FileDiff for path whose changed head lines are changed.
func diffFile(path string, changed []int) commentscan.FileDiff {
	file := commentscan.FileDiff{
		Path: path, OldPath: path, NewPath: path, Hunks: nil,
		Added: false, Deleted: false, Binary: false, MetadataOnly: false,
	}
	if len(changed) > 0 {
		hunk := commentscan.Hunk{Lines: nil}
		for _, newNo := range changed {
			hunk.Lines = append(hunk.Lines, commentscan.DiffLine{
				Kind: '+', Text: "", OldNo: 0, NewNo: newNo,
			})
		}
		file.Hunks = []commentscan.Hunk{hunk}
	}
	return file
}

// runDoc runs the document ratchet on path with the given before and after text.
func runDoc(path, before, after string, changed []int) ([]model.Finding, []model.Note) {
	return File(docConfig, diffFile(path, changed), before, after)
}

// words returns count placeholder words.
func words(count int) string {
	parts := make([]string, 0, count)
	for i := range count {
		parts = append(parts, fmt.Sprintf("word%d", i+1))
	}
	return strings.Join(parts, " ")
}

// findingPresent fails the test unless want is among findings.
func findingPresent(t *testing.T, findings []model.Finding, want model.Finding) {
	t.Helper()
	if !slices.Contains(findings, want) {
		t.Fatalf("findings %#v lack %#v", findings, want)
	}
}

// badMarkerPresent fails the test unless a bad-marker finding names path, line and a reason containing text.
func badMarkerPresent(t *testing.T, findings []model.Finding, path string, line int, text string) {
	t.Helper()
	for _, finding := range findings {
		bad, ok := finding.(model.BadMarkerFinding)
		if ok && bad.Path == path && bad.Line == line && strings.Contains(bad.Reason, text) {
			return
		}
	}
	t.Fatalf("findings %#v lack a bad marker at %s:%d containing %q", findings, path, line, text)
}

func TestDocMarkerLivenessAndClasses(t *testing.T) {
	t.Run("a newly added second doc marker is bad-marker even when the first is unchanged", func(t *testing.T) {
		marker := "<!-- LENGTH §Plan: preserve the old interface details -->"
		before := marker + "\n\n## Plan\n\n" + words(10)
		after := marker + "\n<!-- LENGTH §Plan: add more interface details -->\n\n## Plan\n\n" + words(10)
		findings, _ := runDoc("docs/designs/tools/record.md", before, after, []int{2})
		badMarkerPresent(t, findings, "docs/designs/tools/record.md", 2, "only one doc LENGTH marker is allowed")
	})

	t.Run("a disallowed doc marker is reported and does not exempt an over-target spec", func(t *testing.T) {
		text := "<!-- LENGTH §Plan: split the chapters across files -->\n\n## Plan\n\n" + words(1599)
		findings, _ := runDoc("docs/specs/gate.md", "", text, []int{1})
		badMarkerPresent(t, findings, "docs/specs/gate.md", 1, "not allowed")
		findingPresent(t, findings, model.DocFinding{
			Path: "docs/specs/gate.md", DocClass: "spec", Words: 1600, BaseWords: 0, Target: 1500,
		})
	})
}
