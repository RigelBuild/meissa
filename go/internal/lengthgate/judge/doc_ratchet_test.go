// SPDX-License-Identifier: MIT OR Apache-2.0

package judge

import (
	"slices"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/lengthgate/model"
)

// designRecord returns a design record whose body follows a title heading.
func designRecord(body string) string {
	return "# Record\n\n" + body
}

// docFinding builds the DocFinding the TypeScript suite asserts for a judged doc.
func docFinding(path, class string, words, base int, target float64) model.DocFinding {
	return model.DocFinding{Path: path, DocClass: class, Words: words, BaseWords: base, Target: target}
}

// expectDocFindingsEmpty fails the test unless findings and notes are both empty.
func expectDocFindingsEmpty(t *testing.T, findings []model.Finding, notes []model.Note) {
	t.Helper()
	if len(findings) != 0 || len(notes) != 0 {
		t.Fatalf("findings = %#v, notes = %#v, want none", findings, notes)
	}
}

// hasNewDocFinding reports whether findings holds a doc finding with no base words.
func hasNewDocFinding(findings []model.Finding) bool {
	return slices.ContainsFunc(findings, func(f model.Finding) bool {
		doc, ok := f.(model.DocFinding)
		return ok && doc.BaseWords == 0
	})
}

// hasBadMarker reports whether findings holds any bad-marker finding.
func hasBadMarker(findings []model.Finding) bool {
	return slices.ContainsFunc(findings, func(f model.Finding) bool {
		_, ok := f.(model.BadMarkerFinding)
		return ok
	})
}

func TestDocRatchet(t *testing.T) {
	const record = "docs/designs/tools/record.md"

	t.Run("a new 2,600-word design record fails and a live valid marker passes", func(t *testing.T) {
		unmarked, _ := runDoc(record, "", designRecord(words(2599)), nil)
		findingPresent(t, unmarked, docFinding(record, "design record", 2600, 0, 2500))
		marked := "<!-- LENGTH §Plan: per-task interfaces readers need -->\n\n" +
			designRecord("## Plan\n\n"+words(2599))
		findings, _ := runDoc(record, "", marked, []int{1})
		if len(findings) != 0 {
			t.Fatalf("marked findings = %#v, want none", findings)
		}
	})

	t.Run("a doc marker naming a missing section is bad-marker", func(t *testing.T) {
		text := "<!-- LENGTH §Nope: per-task interfaces readers need -->\n\n## Plan\n\n" + words(2600)
		findings, _ := runDoc(record, "", text, []int{1})
		badMarkerPresent(t, findings, record, 1, "Nope")
	})

	t.Run("renaming an over-target record judges it as a new doc", func(t *testing.T) {
		text := designRecord(words(2600))
		rename := diffFile("docs/designs/tools/new.md", nil)
		rename.OldPath = "docs/designs/tools/old.md"
		rename.MetadataOnly = true
		findings, _ := File(docConfig, rename, text, text)
		if !hasNewDocFinding(findings) {
			t.Fatalf("findings = %#v, want a doc finding with BaseWords 0", findings)
		}
	})

	t.Run("a 3,000-word record trimmed to 2,900 passes", func(t *testing.T) {
		findings, notes := runDoc(record, designRecord(words(3000)), designRecord(words(2900)), nil)
		expectDocFindingsEmpty(t, findings, notes)
	})

	t.Run("an unchanged doc marker is not live, but editing its reason makes it live", func(t *testing.T) {
		before := "<!-- LENGTH §Plan: preserve the old interface details -->\n\n## Plan\n\n" + words(2500)
		unchanged := "<!-- LENGTH §Plan: preserve the old interface details -->\n\n## Plan\n\n" + words(2501)
		edited := "<!-- LENGTH §Plan: explain the new interface details -->\n\n## Plan\n\n" + words(2501)
		findings, _ := runDoc(record, before, unchanged, []int{5})
		findingPresent(t, findings, docFinding(record, "design record", 2502, 2501, 2500))
		live, _ := runDoc(record, before, edited, []int{1, 5})
		if len(live) != 0 {
			t.Fatalf("edited-marker findings = %#v, want none", live)
		}
	})

}

// TestDocRatchetMarkerNotes covers the unneeded-marker note on a comment block.
func TestDocRatchetMarkerNotes(t *testing.T) {
	t.Run("a marker below the comment ceiling produces an unneeded note", func(t *testing.T) {
		text := "// LENGTH: preserve bit layout details\n// one\n// two"
		findings, notes := File(commentConfig, diffFile("src/file.ts", []int{1}), "", text)
		if len(findings) != 0 {
			t.Fatalf("findings = %#v, want none", findings)
		}
		want := model.Note{Path: "src/file.ts", Line: 1}
		if !slices.Contains(notes, want) {
			t.Fatalf("notes = %#v, want %#v", notes, want)
		}
	})
}

// TestDocRatchetSpecs covers spec classes, inline marker mentions and disallowed markers.
func TestDocRatchetSpecs(t *testing.T) {
	t.Run("inline marker mention in a spec is not a bad marker", func(t *testing.T) {
		spec := "`<!-- LENGTH §Plan: preserve the old interface details -->`\n\n" + words(1600)
		findings, _ := runDoc("docs/specs/gate.md", "", spec, []int{1})
		if hasBadMarker(findings) {
			t.Fatalf("findings = %#v, want no bad-marker for an inline mention", findings)
		}
		if !slices.ContainsFunc(findings, func(f model.Finding) bool {
			doc, ok := f.(model.DocFinding)
			return ok && doc.DocClass == "spec"
		}) {
			t.Fatalf("findings = %#v, want a spec doc finding", findings)
		}
	})

	t.Run("inline marker mention does not invalidate a valid design marker", func(t *testing.T) {
		record2 := "<!-- LENGTH §Plan: preserve the old interface details -->\n# Record\n\n## Plan\n\n" +
			"Inline `<!-- LENGTH is not a marker -->`.\n\n" + words(2510)
		findings, notes := runDoc("docs/designs/tools/record.md", "", record2, []int{1})
		expectDocFindingsEmpty(t, findings, notes)
	})

	t.Run("a disallowed doc marker is reported and does not exempt an over-target spec", func(t *testing.T) {
		text := "<!-- LENGTH §Plan: split the chapters across files -->\n\n## Plan\n\n" + words(1599)
		findings, _ := runDoc("docs/specs/gate.md", "", text, []int{1})
		badMarkerPresent(t, findings, "docs/specs/gate.md", 1, "not allowed")
		findingPresent(t, findings, docFinding("docs/specs/gate.md", "spec", 1600, 0, 1500))
	})
}

// TestDocRatchetClassMatching covers which document class a path falls into.
func TestDocRatchetClassMatching(t *testing.T) {
	const record = "docs/designs/tools/record.md"

	t.Run("an excluded path is final and a class exclusion permits a later class match", func(t *testing.T) {
		excluded := model.Config{Extensions: []string{".ts"}, Exclude: []string{"docs/**"}, Docs: []model.DocClass{}}
		findings, notes := File(excluded, diffFile("docs/specs/private.md", nil), "", words(1600))
		expectDocFindingsEmpty(t, findings, notes)

		fallback := model.Config{Extensions: []string{".ts"}, Exclude: []string{}, Docs: []model.DocClass{
			{
				Name: "skipped", Include: []string{"docs/**/*.md"}, Exclude: []string{"docs/designs/**"},
				Target: 10, MarkerAllowed: true,
			},
			{Name: "fallback", Include: []string{"docs/designs/**/*.md"}, Exclude: []string{}, Target: 10, MarkerAllowed: true},
		}}
		classFindings, _ := File(fallback, diffFile(record, nil), "", words(20))
		findingPresent(t, classFindings, docFinding(record, "fallback", 20, 0, 10))
	})
}
