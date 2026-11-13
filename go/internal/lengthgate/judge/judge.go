// SPDX-License-Identifier: MIT OR Apache-2.0

// Package judge applies the comment and document ratchets to changed files.
package judge

import (
	"regexp"
	"slices"
	"strings"

	"github.com/RigelBuild/meissa/go/commentscan"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/comment"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/markdown"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/model"
	"github.com/bmatcuk/doublestar/v4"
)

// File applies the comment and document ratchets to one changed file.
// Before and after are the file text at the base and head revisions.
func File(cfg model.Config, file commentscan.FileDiff, before, after string) ([]model.Finding, []model.Note) {
	// A mode-only change does not touch the file's text, so the TypeScript skipped it.
	if file.MetadataOnly && len(file.Hunks) == 0 && file.OldPath == file.NewPath {
		return nil, nil
	}
	if file.Deleted || MatchesAny(file.Path, cfg.Exclude) {
		return nil, nil
	}
	ext := commentscan.ExtOf(file.Path)
	scanComments := containsString(cfg.Extensions, ext)
	docClass := DocClassFor(file.Path, cfg.Docs)
	if !scanComments && docClass == nil {
		return nil, nil
	}
	if file.Binary {
		return []model.Finding{model.UnscannableFinding{Path: file.Path}}, nil
	}
	var findings []model.Finding
	var notes []model.Note
	if scanComments {
		commentFindings, commentNotes, skip := judgeComments(file, before, after, ext)
		if skip {
			return commentFindings, commentNotes
		}
		findings = append(findings, commentFindings...)
		notes = append(notes, commentNotes...)
	}
	if docClass != nil {
		docFindings, docNotes := judgeDoc(file, *docClass, before, after)
		findings = append(findings, docFindings...)
		notes = append(notes, docNotes...)
	}
	return findings, notes
}

// judgeComments applies the comment ratchet. skip reports that the file is done: an unscannable head
// returns its finding at once, and a generated file is not judged.
func judgeComments(file commentscan.FileDiff, before, after, ext string) ([]model.Finding, []model.Note, bool) {
	afterScan, ok := commentscan.CommentBlocks(after, ext)
	if !ok || afterScan.Ambiguous {
		return []model.Finding{model.UnscannableFinding{Path: file.Path}}, nil, true
	}
	if isGeneratedHeader(after, afterScan.Blocks) {
		return nil, nil, true
	}
	beforeBlocks := []commentscan.CommentBlock{}
	if beforeScan, ok := commentscan.CommentBlocks(sameFileText(file, before), ext); ok && !beforeScan.Ambiguous {
		beforeBlocks = beforeScan.Blocks
	}
	addedLines := changedHeadLines(file)
	var findings []model.Finding
	var notes []model.Note
	liveMarkers := map[int]bool{}
	for _, block := range afterScan.Blocks {
		if block.Doc || !addedLines[block.Start] {
			continue
		}
		marker := comment.Marker(block.First)
		if !marker.Found {
			continue
		}
		if marker.Error != "" {
			findings = append(findings, model.BadMarkerFinding{Path: file.Path, Line: block.Start, Reason: marker.Error})
			continue
		}
		liveMarkers[block.Start] = true
		if block.Lines <= comment.Ceiling {
			notes = append(notes, model.Note{Path: file.Path, Line: block.Start})
		}
	}
	if fileExcess(afterScan.Blocks, liveMarkers) > fileExcess(beforeBlocks, nil) {
		lineMapping := comment.LineMap(before, after)
		for _, block := range afterScan.Blocks {
			if block.Doc || block.Lines <= comment.Ceiling || liveMarkers[block.Start] {
				continue
			}
			baseLines := baseLinesFor(block, lineMapping, beforeBlocks)
			if block.Lines > baseLines {
				findings = append(findings, model.CommentFinding{
					Path: file.Path, Line: block.Start, Lines: block.Lines, BaseLines: baseLines,
				})
			}
		}
	}
	return findings, notes, false
}

// sameFileText returns the base text when the file was not renamed, and "" for a new or renamed file.
func sameFileText(file commentscan.FileDiff, before string) string {
	if file.OldPath != file.NewPath {
		return ""
	}
	return before
}

// baseLinesFor returns the longest base comment block that any head line of block maps to.
func baseLinesFor(
	block commentscan.CommentBlock, lineMapping func(int) (int, bool), beforeBlocks []commentscan.CommentBlock,
) int {
	baseLines := 0
	for line := block.Start; line < block.Start+block.Lines; line++ {
		baseLine, ok := lineMapping(line)
		if !ok {
			continue
		}
		for _, base := range beforeBlocks {
			if base.Doc || baseLine < base.Start || baseLine >= base.Start+base.Lines {
				continue
			}
			baseLines = max(baseLines, base.Lines)
		}
	}
	return baseLines
}

// generatedWindow is how many leading lines a generated-code marker may sit in.
const generatedWindow = 5

// isGeneratedHeader reports whether a doc block in the first five lines carries a generated-code marker.
// It checks only the block's own lines, as the TypeScript does, so code that follows a short doc comment
// is never read as a header.
func isGeneratedHeader(text string, blocks []commentscan.CommentBlock) bool {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	for _, block := range blocks {
		if !block.Doc || block.Start > generatedWindow {
			continue
		}
		end := min(generatedWindow, block.Start-1+block.Lines, len(lines))
		if slices.ContainsFunc(lines[block.Start-1:end], generatedHeader) {
			return true
		}
	}
	return false
}

// generatedPattern is the TypeScript GENERATED_HEADER expression, matched on a word boundary.
var generatedPattern = regexp.MustCompile(`Code generated\b.*DO NOT EDIT\.|@generated\b`)

// generatedHeader reports whether line carries a generated-code marker.
func generatedHeader(line string) bool {
	return generatedPattern.MatchString(line)
}

// changedHeadLines returns the head line numbers of added lines.
func changedHeadLines(file commentscan.FileDiff) map[int]bool {
	changed := map[int]bool{}
	for _, hunk := range file.Hunks {
		for _, line := range hunk.Lines {
			if line.Kind == '+' {
				changed[line.NewNo] = true
			}
		}
	}
	return changed
}

// fileExcess sums the lines above the ceiling over non-doc blocks without a live marker.
func fileExcess(blocks []commentscan.CommentBlock, liveMarkers map[int]bool) int {
	excess := 0
	for _, block := range blocks {
		if block.Doc || liveMarkers[block.Start] {
			continue
		}
		excess += max(0, block.Lines-comment.Ceiling)
	}
	return excess
}

// MatchesAny reports whether filePath matches any glob in globs. LoadConfig validated every pattern
// with doublestar.ValidatePattern, so Match cannot fail here and its error is not reachable.
func MatchesAny(filePath string, globs []string) bool {
	for _, glob := range globs {
		// LoadConfig validated every glob, so Match cannot return a pattern error here.
		if matched, _ := doublestar.Match(glob, filePath); matched {
			return true
		}
	}
	return false
}

// DocClassFor returns the first document class whose include matches and whose exclude does not.
func DocClassFor(filePath string, classes []model.DocClass) *model.DocClass {
	for i := range classes {
		if MatchesAny(filePath, classes[i].Include) && !MatchesAny(filePath, classes[i].Exclude) {
			return &classes[i]
		}
	}
	return nil
}

// containsString reports whether values holds s.
func containsString(values []string, s string) bool {
	return slices.Contains(values, s)
}

// judgeDoc applies the document ratchet to one file in a document class.
func judgeDoc(file commentscan.FileDiff, class model.DocClass, before, after string) ([]model.Finding, []model.Note) {
	base := sameFileText(file, before)
	if base == after {
		return nil, nil
	}
	marker := markdown.Marker(after)
	markerLine, markerLive := liveMarkerLine(marker, changedHeadLines(file))

	var findings []model.Finding
	var notes []model.Note
	var liveDoc *markdown.MarkerResult
	if markerLive {
		switch {
		case !class.MarkerAllowed:
			findings = append(findings, model.BadMarkerFinding{
				Path: file.Path, Line: markerLine, Reason: "LENGTH markers are not allowed for this doc class",
			})
		case marker.Error != "":
			findings = append(findings, model.BadMarkerFinding{Path: file.Path, Line: markerLine, Reason: marker.Error})
		default:
			liveDoc = &marker
		}
	}

	dropErrata := class.Name == "design record"
	wordsAfter := markdown.ProseWords(after, dropErrata)
	wordsBefore := markdown.ProseWords(base, dropErrata)
	if liveDoc != nil && float64(wordsAfter) <= class.Target {
		notes = append(notes, model.Note{Path: file.Path, Line: markerLine})
	}
	if float64(wordsAfter) > class.Target && wordsAfter > wordsBefore && liveDoc == nil {
		findings = append(findings, model.DocFinding{
			Path: file.Path, DocClass: class.Name, Words: wordsAfter, BaseWords: wordsBefore, Target: class.Target,
		})
	}
	return findings, notes
}

// liveMarkerLine returns the line a doc marker finding is reported on, and whether the marker is live. As in
// core.ts, the first marker line that was added is the live marker. Without one, the report falls back to the
// first marker and the marker is not live.
func liveMarkerLine(marker markdown.MarkerResult, added map[int]bool) (int, bool) {
	for _, line := range marker.Lines {
		if added[line] {
			return line, true
		}
	}
	if len(marker.Lines) > 0 {
		return marker.Lines[0], false
	}
	return 0, false
}

// IsGated reports whether File would judge path, so unrelated or binary files are never read.
// It mirrors File's exclude and class rules. Patterns were validated at load, so matching has no error.
func IsGated(path string, cfg model.Config) bool {
	if MatchesAny(path, cfg.Exclude) {
		return false
	}
	if slices.Contains(cfg.Extensions, commentscan.ExtOf(path)) {
		return true
	}
	return DocClassFor(path, cfg.Docs) != nil
}
