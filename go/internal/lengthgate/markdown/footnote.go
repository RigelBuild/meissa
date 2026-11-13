// SPDX-License-Identifier: MIT OR Apache-2.0

package markdown

import (
	"bytes"
	"strings"

	gast "github.com/yuin/goldmark/ast"
)

// span is a half-open byte range of the source.
type span struct{ start, stop int }

// lineRange is a half-open range of 1-based line numbers.
type lineRange struct{ from, to int }

// orphanFootnoteTexts returns the visible text of each footnote definition that goldmark did not
// turn into a node. goldmark builds a Footnote only when a reference to it exists, so an orphan
// definition would vanish from the tree, while the TypeScript counts its text.
//
// A definition line starts with up to three spaces, then "[^label]:". Its text is the rest of that
// line plus the following non-blank lines that no block covers. A line a block covers is already
// counted by the walk, or is code, so it is never counted here. Front matter and errata are skipped.
func orphanFootnoteTexts(doc Doc, dropErrata bool) []string {
	source := doc.source
	starts := lineStarts(source)
	covered := coveredSpans(doc.root)
	errata := errataRanges(doc, starts, dropErrata)

	var texts []string
	for line := doc.frontMatterEnd + 1; line <= len(starts); line++ {
		body, begin := lineBody(source, starts, line)
		after, ok := footnoteDefinitionEnd(body)
		if !ok || inRanges(errata, line) || overlaps(covered, begin, begin+len(body)) {
			continue
		}
		parts := []string{strings.TrimSpace(string(body[after:]))}
		for next := line + 1; next <= len(starts); next++ {
			nextBody, nextBegin := lineBody(source, starts, next)
			if len(bytes.TrimSpace(nextBody)) == 0 || overlaps(covered, nextBegin, nextBegin+len(nextBody)) {
				break
			}
			if _, isDefinition := footnoteDefinitionEnd(nextBody); isDefinition {
				break
			}
			parts = append(parts, strings.TrimSpace(string(nextBody)))
			line = next
		}
		texts = append(texts, decodeEntities(strings.Join(parts, "\n")))
	}
	return texts
}

// footnoteDefinitionEnd reports whether line is a footnote definition start, and returns the
// offset just after its "]:" marker.
func footnoteDefinitionEnd(line []byte) (int, bool) {
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	if indent > 3 || !bytes.HasPrefix(line[indent:], []byte("[^")) {
		return 0, false
	}
	label := bytes.IndexByte(line[indent+2:], ']')
	if label <= 0 {
		return 0, false
	}
	colon := indent + 2 + label + 1
	if colon >= len(line) || line[colon] != ':' {
		return 0, false
	}
	return colon + 1, true
}

// coveredSpans returns the source range of every line segment that a block node in the tree owns.
// Inline nodes are skipped: goldmark panics when asked for their lines, and their text sits inside
// a block's segments anyway.
func coveredSpans(root gast.Node) []span {
	var out []span
	var visit func(gast.Node)
	visit = func(node gast.Node) {
		if node.Type() == gast.TypeBlock {
			lines := node.Lines()
			for i := range lines.Len() {
				seg := lines.At(i)
				out = append(out, span{seg.Start, seg.Stop})
			}
		}
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			visit(child)
		}
	}
	visit(root)
	return out
}

// overlaps reports whether any span shares a byte with [from, to).
func overlaps(spans []span, from, to int) bool {
	for _, s := range spans {
		if s.start < to && s.stop > from {
			return true
		}
	}
	return false
}

// errataRanges returns the line ranges of top-level "## Errata" sections. A section runs to the next
// top-level heading of depth 1 or 2, matching proseBlocks. It is empty when dropErrata is false.
func errataRanges(doc Doc, starts []int, dropErrata bool) []lineRange {
	if !dropErrata {
		return nil
	}
	var out []lineRange
	open := -1
	for node := doc.root.FirstChild(); node != nil; node = node.NextSibling() {
		h, ok := node.(*gast.Heading)
		if !ok {
			continue
		}
		line := LineOf(starts, headingOffset(h))
		if open >= 0 && h.Level <= 2 {
			out = append(out, lineRange{open, line})
			open = -1
		}
		if h.Level == 2 && NormalizeHeading(inlineText(h, doc.source)) == "errata" {
			open = line
		}
	}
	if open >= 0 {
		out = append(out, lineRange{open, len(starts) + 1})
	}
	return out
}

// inRanges reports whether line falls in any range.
func inRanges(ranges []lineRange, line int) bool {
	for _, r := range ranges {
		if line >= r.from && line < r.to {
			return true
		}
	}
	return false
}

// headingOffset returns the byte offset of a heading's first line segment.
func headingOffset(h *gast.Heading) int {
	if h.Lines().Len() == 0 {
		return 0
	}
	return h.Lines().At(0).Start
}
