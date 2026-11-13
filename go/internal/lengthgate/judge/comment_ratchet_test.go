// SPDX-License-Identifier: MIT OR Apache-2.0

package judge

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/lengthgate/model"
)

// commentConfig gates TypeScript comments only, as the TypeScript COMMENT_CONFIG does.
var commentConfig = model.Config{
	Extensions: []string{".ts"},
	Exclude:    []string{},
	Docs:       []model.DocClass{},
}

// judgeComment runs the comment ratchet on a TypeScript file whose changed head lines are changed.
func judgeComment(before, after string, changed []int) ([]model.Finding, []model.Note) {
	return File(commentConfig, diffFile("src/file.ts", changed), before, after)
}

// comments returns count line comments numbered from 1, each prefixed by indent.
func comments(count int, indent, label string) string {
	lines := make([]string, 0, count)
	for i := range count {
		lines = append(lines, fmt.Sprintf("%s// %s %d", indent, label, i+1))
	}
	return strings.Join(lines, "\n")
}

// commentPresent fails the test unless want is among findings.
func commentPresent(t *testing.T, findings []model.Finding, want model.Finding) {
	t.Helper()
	if !slices.Contains(findings, want) {
		t.Fatalf("findings %#v lack %#v", findings, want)
	}
}

// expectNoFindings fails the test when findings is not empty.
func expectNoFindings(t *testing.T, findings []model.Finding) {
	t.Helper()
	if len(findings) != 0 {
		t.Fatalf("findings = %#v, want none", findings)
	}
}

func TestCommentRatchet(t *testing.T) {
	t.Run("a new five-line block fails, while a valid live marker passes", func(t *testing.T) {
		unmarked, _ := judgeComment("", comments(5, "", "line"), nil)
		commentPresent(t, unmarked, model.CommentFinding{Path: "src/file.ts", Line: 1, Lines: 5, BaseLines: 0})
		marked, _ := judgeComment("", "// LENGTH: bit layout of the header\n"+comments(4, "", "line"), []int{1})
		expectNoFindings(t, marked)
	})

	t.Run("a short comment marker is a bad-marker finding", func(t *testing.T) {
		findings, _ := judgeComment("", "// LENGTH: why\n"+comments(4, "", "line"), []int{1})
		commentPresent(t, findings, model.BadMarkerFinding{
			Path: "src/file.ts", Line: 1, Reason: "reason must contain at least 3 words",
		})
	})

	t.Run("a ten-line block cut to eight passes and growth reports its base length", func(t *testing.T) {
		shrunk, _ := judgeComment(comments(10, "", "line"), comments(8, "", "line"), nil)
		expectNoFindings(t, shrunk)
		grown, _ := judgeComment(comments(10, "", "line"), comments(11, "", "line"), nil)
		commentPresent(t, grown, model.CommentFinding{Path: "src/file.ts", Line: 1, Lines: 11, BaseLines: 10})
	})

	t.Run("joining two three-line blocks into six fails", func(t *testing.T) {
		before := comments(3, "", "line") + "\n\n" + comments(3, "", "other")
		after := comments(3, "", "line") + "\n" + comments(3, "", "other")
		findings, _ := judgeComment(before, after, nil)
		commentPresent(t, findings, model.CommentFinding{Path: "src/file.ts", Line: 1, Lines: 6, BaseLines: 3})
	})

	t.Run("an untouched long block in a changed file passes", func(t *testing.T) {
		before := comments(6, "", "line") + "\n\nconst oldValue = 1;"
		after := comments(6, "", "line") + "\n\nconst newValue = 1;"
		findings, _ := judgeComment(before, after, []int{8})
		expectNoFindings(t, findings)
	})

	t.Run("reindent, same-file movement, and a full reworded trim pass", func(t *testing.T) {
		base := comments(10, "", "line")
		reindented, _ := judgeComment(base, comments(10, "    ", "line"), nil)
		expectNoFindings(t, reindented)
		moved, _ := judgeComment(base+"\n\nconst value = 1;", "const value = 1;\n\n"+base, nil)
		expectNoFindings(t, moved)
		reworded, _ := judgeComment(base, comments(8, "", "rewritten"), nil)
		expectNoFindings(t, reworded)
	})

	t.Run("a trim can fund a new long block in the same file", func(t *testing.T) {
		before := comments(10, "", "line")
		after := comments(4, "", "line") + "\n\n" + comments(6, "", "new")
		findings, _ := judgeComment(before, after, nil)
		expectNoFindings(t, findings)
	})

	t.Run("a ten-line block moved to a new file fails", func(t *testing.T) {
		file := diffFile("src/new.ts", []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
		file.OldPath = "src/old.ts"
		findings, _ := File(commentConfig, file, comments(10, "", "line"), comments(10, "", "line"))
		commentPresent(t, findings, model.CommentFinding{Path: "src/new.ts", Line: 1, Lines: 10, BaseLines: 0})
	})

	t.Run("a pure rename carrying a long block fails, but a mode-only change passes", func(t *testing.T) {
		rename := diffFile("src/new.ts", nil)
		rename.OldPath = "src/old.ts"
		rename.MetadataOnly = true
		before, after := comments(10, "", "line"), comments(10, "", "line")
		findings, _ := File(commentConfig, rename, before, after)
		commentPresent(t, findings, model.CommentFinding{Path: "src/new.ts", Line: 1, Lines: 10, BaseLines: 0})
		mode := diffFile("src/file.ts", nil)
		mode.MetadataOnly = true
		modeFindings, _ := File(commentConfig, mode, before, after)
		expectNoFindings(t, modeFindings)
	})

	t.Run("a marker line must change before it exempts a growing comment block", func(t *testing.T) {
		before := "// LENGTH: preserve old protocol details\n" + comments(9, "", "line")
		unchanged := "// LENGTH: preserve old protocol details\n" + comments(10, "", "line")
		edited := "// LENGTH: document new protocol details\n" + comments(10, "", "line")
		findings, _ := judgeComment(before, unchanged, []int{11})
		commentPresent(t, findings, model.CommentFinding{Path: "src/file.ts", Line: 1, Lines: 11, BaseLines: 10})
		exempt, _ := judgeComment(before, edited, []int{1, 11})
		expectNoFindings(t, exempt)
	})

	t.Run("excluded and generated-header files are not judged", func(t *testing.T) {
		excludedCfg := model.Config{
			Extensions: []string{".ts"}, Exclude: []string{"vendor/**"}, Docs: []model.DocClass{},
		}
		excluded, excludedNotes := File(
			excludedCfg, diffFile("vendor/generated.ts", []int{1}), "", comments(8, "", "line"),
		)
		expectNoFindings(t, excluded)
		if len(excludedNotes) != 0 {
			t.Fatalf("excluded notes = %#v, want none", excludedNotes)
		}
		generatedSource := "// Code generated by tool. DO NOT EDIT.\n" + comments(7, "", "line")
		generated, _ := judgeComment("", generatedSource, nil)
		expectNoFindings(t, generated)
	})

	t.Run("ambiguous source is unscannable and clean source passes", func(t *testing.T) {
		unscannable, _ := judgeComment("", `const value = "unfinished`, nil)
		commentPresent(t, unscannable, model.UnscannableFinding{Path: "src/file.ts"})
		clean, _ := judgeComment("", "const value = 1;", nil)
		expectNoFindings(t, clean)
	})

	t.Run("metadata-only diffs with hunks still judge added comment blocks", func(t *testing.T) {
		changed := make([]int, 10)
		for i := range changed {
			changed[i] = i + 1
		}
		file := diffFile("src/file.ts", changed)
		file.MetadataOnly = true
		findings, _ := File(commentConfig, file, "", comments(10, "", "line"))
		commentPresent(t, findings, model.CommentFinding{Path: "src/file.ts", Line: 1, Lines: 10, BaseLines: 0})
	})

	t.Run("an unscannable base is treated as having no comment blocks", func(t *testing.T) {
		before := "const s = \"unclosed\nconst t = 1;\n"
		findings, notes := judgeComment(before, "const value = 1;", nil)
		expectNoFindings(t, findings)
		if len(notes) != 0 {
			t.Fatalf("notes = %#v, want none", notes)
		}
	})

	t.Run("a moved block keeps its base pairing when another block grows", func(t *testing.T) {
		before := comments(10, "", "A") + "\n\n" + comments(6, "", "B")
		after := comments(6, "", "B") + "\n\n" + comments(11, "", "A")
		findings, _ := judgeComment(before, after, nil)
		commentPresent(t, findings, model.CommentFinding{Path: "src/file.ts", Line: 8, Lines: 11, BaseLines: 10})
	})

	t.Run("a trim to eight cannot fund a new nine-line block", func(t *testing.T) {
		before := comments(10, "", "line")
		after := comments(8, "", "line") + "\n\n" + comments(9, "", "new")
		changed := make([]int, 9)
		for i := range changed {
			changed[i] = i + 10
		}
		findings, _ := judgeComment(before, after, changed)
		commentPresent(t, findings, model.CommentFinding{Path: "src/file.ts", Line: 10, Lines: 9, BaseLines: 0})
	})

	t.Run("unique fallback does not pair bare Go comment lines", func(t *testing.T) {
		baseLines := strings.Split(comments(10, "", "A"), "\n")
		baseLines[6] = "//"
		addedLines := strings.Split(comments(9, "", "B"), "\n")
		addedLines[6] = "//"
		kept := make([]string, 0, len(baseLines)-1)
		for i, line := range baseLines {
			if i != 6 {
				kept = append(kept, line)
			}
		}
		after := strings.Join(kept, "\n") + "\n\n" + strings.Join(addedLines, "\n")
		changed := make([]int, 9)
		for i := range changed {
			changed[i] = i + 11
		}
		goCfg := model.Config{Extensions: []string{".go"}, Exclude: []string{}, Docs: []model.DocClass{}}
		file := diffFile("src/file.go", changed)
		findings, _ := File(goCfg, file, strings.Join(baseLines, "\n"), after)
		commentPresent(t, findings, model.CommentFinding{
			Path: "src/file.go", Line: 11, Lines: 9, BaseLines: 0,
		})
	})
	t.Run("marking part of a split block does not fail an unrelated block that stays in budget", func(t *testing.T) {
		tail := "// a 9\n// a 10\n// a 11\n// a 12"
		before := comments(12, "", "a") +
			"\nconst x = 1;\n\n" + comments(10, "", "b") + "\nconst y = 1;"
		after := comments(8, "", "a") +
			"\nconst x = 1;\n\n// LENGTH: keeps wire layout notes\n" + tail +
			"\nconst w = 1;\n\n" + comments(11, "", "b") + "\nconst y = 1;"
		findings, _ := judgeComment(before, after, []int{11, 15, 16, 28})
		expectNoFindings(t, findings)
	})

	t.Run("a pure rename parsed from git keeps the long block failing", func(t *testing.T) {
		file := diffFile("src/new.ts", nil)
		file.OldPath = "src/old.ts"
		file.MetadataOnly = true
		findings, _ := File(commentConfig, file, comments(10, "", "line"), comments(10, "", "line"))
		commentPresent(t, findings, model.CommentFinding{Path: "src/new.ts", Line: 1, Lines: 10, BaseLines: 0})
	})

	t.Run("the first matching doc class wins", func(t *testing.T) {
		cfg := model.Config{Extensions: []string{}, Exclude: []string{}, Docs: []model.DocClass{
			{Name: "first", Include: []string{"docs/**/*.md"}, Exclude: []string{}, Target: 100, MarkerAllowed: true},
			{
				Name: "second", Include: []string{"docs/designs/**/*.md"}, Exclude: []string{},
				Target: 10, MarkerAllowed: true,
			},
		}}
		findings, _ := File(cfg, diffFile("docs/designs/tools/record.md", nil), "", words(20))
		expectNoFindings(t, findings)
	})
}
