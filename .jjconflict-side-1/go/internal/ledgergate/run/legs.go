// SPDX-License-Identifier: MIT OR Apache-2.0

package run

import (
	"fmt"
	"math"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
)

// The optional validation legs read the design Markdown. Each pattern mirrors
// the TypeScript regex of the same role in packages/design-ledger-gate/index.ts.
// Backticks are written as \x60 because Go raw strings cannot hold one.
var (
	citationRE = regexp.MustCompile("\x60([^\x60" + core.JSSpaceChars + "]*?\\.md):(\\d+(?:-\\d+)?(?:" +
		core.JSSpace + "*," + core.JSSpace + "*\\d+(?:-\\d+)?)*)[^\x60]*\x60")
	citationRangeRE      = regexp.MustCompile("^" + core.JSSpace + "*(\\d+)(?:-(\\d+))?" + core.JSSpace + "*$")
	recordLinkRE         = regexp.MustCompile(`\]\(([^)]+)\)`)
	errataSectionRE      = regexp.MustCompile("^## Errata" + core.JSSpace + "*$")
	headingBlockRE       = regexp.MustCompile("^#{1,2}" + core.JSSpace)
	errataEntryRE        = regexp.MustCompile("^###" + core.JSSpace + "+E\\d")
	errataEntryHeadingRE = regexp.MustCompile("^###" + core.JSSpace + "+E(\\d+)" + core.JSSpace + "+\\x{2014}" +
		core.JSSpace + "+\\d{4}-\\d{2}-\\d{2}" + core.JSSpace + "+\\([^)]+\\)" + core.JSSpace + "*$")
	errataMarkerRE = regexp.MustCompile("^" + core.JSSpace + "*" + core.CIWord("errata") + core.JSSpace + "*:")
	quoteRE        = regexp.MustCompile(`"([^"]+)"`)
	markerSplitRE  = regexp.MustCompile(core.JSSpace + "(?:\\x{2014}|--|\\(" + core.CIWord("see") + `\b)`)
	markerIDRE     = regexp.MustCompile(`(?:^|,)` + core.JSSpace + `*\[?E(\d+)\b`)
)

// checkCitations validates the `file.md:line` citations in design Markdown.
func (g *gateRun) checkCitations() bool {
	if !g.cfg.Legs.Citations {
		return true
	}
	for _, file := range g.designMarkdown() {
		text, found, err := g.text(file)
		if err != nil {
			return g.failWith(err)
		}
		if !found {
			continue
		}
		for _, visible := range core.ScanMarkdown(text).Lines {
			for _, match := range citationRE.FindAllStringSubmatch(visible.Text, -1) {
				if !g.checkCitationRefs(file, visible.Line, match[1], match[2]) {
					return false
				}
			}
		}
		g.reportUnclosedFence(file, text)
	}
	return true
}

// checkCitationRefs checks each comma-separated line reference of one citation.
// A reference that is not a positive range is skipped, as in the TypeScript gate.
func (g *gateRun) checkCitationRefs(file string, line int, targetPath, references string) bool {
	for reference := range strings.SplitSeq(references, ",") {
		span := citationRangeRE.FindStringSubmatch(reference)
		if span == nil {
			continue
		}
		first := lineNumber(span[1])
		last := first
		if span[2] != "" {
			last = lineNumber(span[2])
		}
		if first == 0 || last == 0 || last < first {
			continue
		}
		g.citations.seen++
		relativeTarget := path.Join(path.Dir(file), targetPath)
		if !withinRepo(relativeTarget) {
			g.add(file, line, "citation target escapes the repository: "+targetPath)
			continue
		}
		target, found, err := g.deps.ReadText(relativeTarget)
		if err != nil {
			return g.failWith(err)
		}
		if found {
			g.checkCitationLineCount(file, line, targetPath, first, last, target)
			continue
		}
		if !strings.Contains(targetPath, "/") {
			g.citations.unresolvable++
			continue
		}
		rootTarget := path.Clean(targetPath)
		if slices.Contains(g.cfg.CitationAmbiguousPaths, rootTarget) {
			g.citations.repoAmbiguous++
			continue
		}
		if !withinRepo(rootTarget) {
			g.citations.unresolvable++
			continue
		}
		target, found, err = g.deps.ReadText(rootTarget)
		if err != nil {
			return g.failWith(err)
		}
		if !found {
			g.citations.unresolvable++
			continue
		}
		g.checkCitationLineCount(file, line, targetPath, first, last, target)
	}
	return true
}

func (g *gateRun) checkCitationLineCount(file string, line int, targetPath string, first, last int, target string) {
	g.citations.checked++
	count := lineCount(target)
	if first > count || last > count {
		label := fmt.Sprintf("%s:%d", targetPath, first)
		if last != first {
			label += fmt.Sprintf("-%d", last)
		}
		g.add(file, line, fmt.Sprintf("citation %s exceeds target line count %d", label, count))
	}
}

// checkRecordLinks validates the relative Markdown links in design Markdown.
func (g *gateRun) checkRecordLinks() bool {
	if !g.cfg.Legs.RecordLinks {
		return true
	}
	for _, file := range g.designMarkdown() {
		text, found, err := g.text(file)
		if err != nil {
			return g.failWith(err)
		}
		if !found {
			continue
		}
		for _, visible := range core.ScanMarkdown(text).Lines {
			for _, match := range recordLinkRE.FindAllStringSubmatch(visible.Text, -1) {
				if !g.checkRecordLink(file, visible.Line, match[1]) {
					return false
				}
			}
		}
		g.reportUnclosedFence(file, text)
	}
	return true
}

// checkRecordLink checks one link target. An anchor is checked only on a
// Markdown target, and only against the target's heading slugs.
func (g *gateRun) checkRecordLink(file string, line int, href string) bool {
	targetPath, anchor, hasAnchor := strings.Cut(href, "#")
	if targetPath == "" || path.IsAbs(targetPath) {
		return true
	}
	relativeTarget := path.Join(path.Dir(file), targetPath)
	if !withinRepo(relativeTarget) {
		g.add(file, line, "relative Markdown link escapes the repository: "+targetPath)
		return true
	}
	if !strings.HasSuffix(targetPath, ".md") || !hasAnchor {
		return true
	}
	target, found, err := g.deps.ReadText(relativeTarget)
	if err != nil {
		return g.failWith(err)
	}
	if found && !slices.Contains(core.RecordContentFromText(target).Headings, anchor) {
		g.add(file, line, fmt.Sprintf("record link anchor not found: %s#%s", targetPath, anchor))
	}
	return true
}

// checkErrata validates the Errata section of each design Markdown file.
func (g *gateRun) checkErrata() bool {
	if !g.cfg.Legs.Errata {
		return true
	}
	for _, file := range g.designMarkdown() {
		text, found, err := g.text(file)
		if err != nil {
			return g.failWith(err)
		}
		if !found {
			continue
		}
		g.violations = append(g.violations, validateErrata(file, text)...)
		g.reportUnclosedFence(file, text)
	}
	return true
}

// erratum is one `### E<n> — <date> (<author>)` entry of an Errata section.
type erratum struct {
	id          int
	headingLine int
	quote       string
	hasQuote    bool
}

// validateErrata mirrors validateErrata in index.ts. It reports the Errata
// section's heading, ordering, quotes and marker IDs.
func validateErrata(file, text string) []core.Violation {
	lines := strings.Split(text, "\n")
	visible := core.ScanMarkdown(text).Lines

	headingIndex := -1
	if item, ok := core.FirstMarkdownLine(visible, matchLine(errataSectionRE)); ok {
		headingIndex = item.Line - 1
	}
	h1Line := 0
	if item, ok := core.FirstMarkdownLine(visible, func(item core.MarkdownLine) bool { return core.IsH1(item.Text) }); ok {
		h1Line = item.Line
	}
	var h2Indexes []int
	for _, item := range visible {
		if core.IsH2(item.Text) {
			h2Indexes = append(h2Indexes, item.Line-1)
		}
	}
	finalH2 := len(h2Indexes) > 0 && h2Indexes[len(h2Indexes)-1] == headingIndex
	firstH2 := len(lines)
	if len(h2Indexes) > 0 {
		firstH2 = h2Indexes[0]
	}
	headerLine, hasHeader := core.FirstMarkdownLine(visible, func(item core.MarkdownLine) bool {
		return item.Line-1 < firstH2 && errataMarkerRE.MatchString(item.Text)
	})

	if headingIndex == -1 {
		if !hasHeader {
			return nil
		}
		return []core.Violation{{File: file, Line: headerLine.Line, Message: "Errata marker requires an Errata section"}}
	}
	var out []core.Violation
	if !finalH2 {
		out = append(out, core.Violation{File: file, Line: headingIndex + 1, Message: "Errata must be the final H2"})
	}

	var entries []*erratum
	var current *erratum
	malformedEntries := false
	for _, item := range visible {
		if item.Line <= headingIndex+1 {
			continue
		}
		if core.IsH2(item.Text) {
			break
		}
		if headingBlockRE.MatchString(item.Text) {
			current = nil
			continue
		}
		if errataEntryRE.MatchString(item.Text) {
			heading := errataEntryHeadingRE.FindStringSubmatch(item.Text)
			if heading == nil {
				out = append(out, core.Violation{File: file, Line: item.Line, Message: "malformed errata entry heading"})
				malformedEntries = true
				current = nil
				continue
			}
			current = &erratum{
				id: idNumber(heading[1]), headingLine: item.Line,
				quote: "", hasQuote: false,
			}
			entries = append(entries, current)
			continue
		}

		if current == nil || current.hasQuote {
			continue
		}
		if quote := quoteRE.FindStringSubmatch(item.Text); quote != nil {
			current.quote = quote[1]
			current.hasQuote = true
		}
	}

	above := strings.Join(lines[:headingIndex], "\n")
	for index, entry := range entries {
		if entry.id != index+1 {
			out = append(out, core.Violation{
				File: file, Line: entry.headingLine, Message: "errata IDs must be numbered E1..En in order",
			})
			malformedEntries = true
		}
		if !entry.hasQuote {
			out = append(out, core.Violation{
				File: file, Line: entry.headingLine, Message: "errata entry requires a quoted wrong text",
			})
			malformedEntries = true
		} else if !strings.Contains(above, entry.quote) {
			out = append(out, core.Violation{
				File: file, Line: entry.headingLine, Message: "errata quoted text no longer appears above the section",
			})
		}
	}

	switch {
	case !hasHeader:
		out = append(out, core.Violation{
			File: file, Line: max(1, h1Line+1), Message: "Errata section requires an Errata marker line",
		})
	case finalH2 && !malformedEntries:
		if message := markerMismatch(headerLine.Text, entries); message != "" {
			out = append(out, core.Violation{File: file, Line: headerLine.Line, Message: message})
		}
	}
	return out
}

// markerMismatch compares the IDs on an Errata marker line with the entries.
// It returns the finding text, or "" when the IDs match in order.
func markerMismatch(markerText string, entries []*erratum) string {
	marker := markerText[strings.Index(markerText, ":")+1:]
	if loc := markerSplitRE.FindStringIndex(marker); loc != nil {
		marker = marker[:loc[0]]
	}
	ids := make([]int, 0, len(entries))
	for _, match := range markerIDRE.FindAllStringSubmatch(marker, -1) {
		ids = append(ids, idNumber(match[1]))
	}
	mismatch := len(ids) != len(entries)
	for index := 0; !mismatch && index < len(ids); index++ {
		mismatch = ids[index] != entries[index].id
	}
	if mismatch {
		return "Errata marker IDs must match section entry IDs in order"
	}
	return ""
}

// matchLine adapts a regex to the exported MarkdownLine type.
func matchLine(re *regexp.Regexp) func(core.MarkdownLine) bool {
	return func(item core.MarkdownLine) bool { return re.MatchString(item.Text) }
}

// lineNumber reads a decimal line number. A value too large for int is
// returned as MaxInt, which is still above any real line count.
func lineNumber(digits string) int {
	value, err := strconv.Atoi(digits)
	if err != nil {
		return math.MaxInt
	}
	return value
}

// idNumber reads an errata ID. An unreadable ID is -1, which never matches an
// entry's position.
func idNumber(digits string) int {
	value, err := strconv.Atoi(digits)
	if err != nil {
		return -1
	}
	return value
}

// lineCount is the number of lines in a file. A trailing newline does not start
// another line.
func lineCount(text string) int {
	if text == "" {
		return 0
	}
	count := strings.Count(text, "\n") + 1
	if strings.HasSuffix(text, "\n") {
		count--
	}
	return count
}
