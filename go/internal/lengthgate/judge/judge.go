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
	// A mode-only change leaves the file's text alone, so there is nothing to judge.
	if file.MetadataOnly && len(file.Hunks) == 0 && file.OldPath == file.NewPath {
		return nil, nil
	}
	if file.Deleted || MatchesAny(file.Path, cfg.Exclude) {
		return nil, nil
	}
	ext := commentscan.ExtOf(file.Path)
	scanComments := slices.Contains(cfg.Extensions, ext)
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

// judgeComments applies the comment ratchet and returns findings, notes and a skip flag.
func judgeComments(file commentscan.FileDiff, before, after, ext string) ([]model.Finding, []model.Note, bool) {
	afterScan, ok := commentscan.CommentBlocks(after, ext)
	if !ok || afterScan.Ambiguous {
		return []model.Finding{model.UnscannableFinding{Path: file.Path}}, nil, true
	}
	if isGeneratedHeader(after, afterScan.Blocks) {
		return nil, nil, true
	}
	beforeBlocks := baseCommentBlocks(file, before, ext)
	markers, findings, notes := liveCommentMarkers(file, afterScan.Blocks)
	if fileExcess(afterScan.Blocks, markers) > fileExcess(beforeBlocks, nil) {
		findings = append(findings, excessCommentFindings(file, before, after, afterScan.Blocks, beforeBlocks, markers)...)
	}
	return findings, notes, false
}

func baseCommentBlocks(file commentscan.FileDiff, before, ext string) []commentscan.CommentBlock {
	beforeScan, ok := commentscan.CommentBlocks(sameFileText(file, before), ext)
	if !ok || beforeScan.Ambiguous {
		return nil
	}
	return beforeScan.Blocks
}

func liveCommentMarkers(file commentscan.FileDiff, blocks []commentscan.CommentBlock) (map[int]bool, []model.Finding, []model.Note) {
	addedLines := changedHeadLines(file)
	markers := map[int]bool{}
	var findings []model.Finding
	var notes []model.Note
	for _, block := range blocks {
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
		markers[block.Start] = true
		if block.Lines <= comment.Ceiling {
			notes = append(notes, model.Note{Path: file.Path, Line: block.Start})
		}
	}
	return markers, findings, notes
}

func excessCommentFindings(
	file commentscan.FileDiff, before, after string, blocks, beforeBlocks []commentscan.CommentBlock, markers map[int]bool,
) []model.Finding {
	lineMapping := comment.LineMap(before, after)
	var findings []model.Finding
	for _, block := range blocks {
		if block.Doc || block.Lines <= comment.Ceiling || markers[block.Start] {
			continue
		}
		baseLines := baseLinesFor(block, lineMapping, beforeBlocks)
		if block.Lines > baseLines {
			findings = append(findings, model.CommentFinding{
				Path: file.Path, Line: block.Start, Lines: block.Lines, BaseLines: baseLines,
			})
		}
	}
	return findings
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
// Only the block's own lines are checked, so code after a short doc comment is never a header.
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

// generatedPattern matches a generated-code marker on a word boundary.
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

// liveMarkerLine returns the line a doc marker finding reports on, and whether that marker is live.
// The first added marker line is live; without one, the report falls back to the first marker, which is not.
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
