// SPDX-License-Identifier: MIT OR Apache-2.0

package markdown

import (
	"strings"

	gast "github.com/yuin/goldmark/ast"

	"github.com/RigelBuild/meissa/go/internal/lengthgate/text"
)

// formatError is the one-line format a doc marker must follow.
const formatError = "doc marker must be one HTML comment line: <!-- LENGTH §Section: reason -->"

// MarkerResult is the outcome of scanning a document for its LENGTH marker. Line is the 1-based line of the
// first marker when Found is true, and Lines lists the 1-based line of every marker in document order.
// Callers use Lines to find the first marker that was added, as core.ts does.
type MarkerResult struct {
	Found   bool
	Line    int
	Lines   []int
	Section string
	Reason  string
	Error   string
}

// Marker finds the one LENGTH marker of a document. It returns Found false when none exists,
// and sets Error when the marker is malformed or names no existing "## " section.
func Marker(document string) MarkerResult {
	doc := Parse([]byte(document))
	result := markerFromDoc(doc)
	result.Lines = markerLines(doc)
	return result
}

// markerLines returns the 1-based line of every LENGTH marker block, in document order.
func markerLines(doc Doc) []int {
	blocks := markerBlocks(doc)
	starts := lineStarts(doc.source)
	lines := make([]int, 0, len(blocks))
	for _, block := range blocks {
		lines = append(lines, LineOf(starts, block.offset))
	}
	return lines
}

// markerFromDoc applies the marker rules to a parsed document.
func markerFromDoc(doc Doc) MarkerResult {
	markers := markerBlocks(doc)
	if len(markers) == 0 {
		return MarkerResult{Found: false, Line: 0, Lines: nil, Section: "", Reason: "", Error: ""}
	}
	starts := lineStarts(doc.source)
	markerLine := LineOf(starts, markers[0].offset)
	if len(markers) > 1 {
		return MarkerResult{
			Found: true, Line: markerLine, Lines: nil, Section: "", Reason: "",
			Error: "only one doc LENGTH marker is allowed",
		}
	}
	marker := markers[0]
	headings := level2Headings(doc)
	afterFrontMatter := markerLine > doc.frontMatterEnd
	beforeFirstHeading := len(headings) > 0 && LineOf(starts, headings[0].offset) > markerLine
	if !afterFrontMatter || !beforeFirstHeading {
		return MarkerResult{
			Found: true, Line: markerLine, Lines: nil, Section: "", Reason: "",
			Error: "doc marker must be after front matter, before the first ## heading, and name an existing section",
		}
	}
	if strings.ContainsAny(marker.value, "\r\n") {
		return MarkerResult{
			Found: true, Line: markerLine, Lines: nil, Section: "", Reason: "",
			Error: "doc LENGTH marker must be one line",
		}
	}
	body, ok := markerBody(marker.value)
	if !ok {
		return MarkerResult{Found: true, Line: markerLine, Lines: nil, Section: "", Reason: "", Error: formatError}
	}
	names := headingNames(headings)
	section, reason := splitSectionReason(body, names)
	if reasonErr := text.InvalidReason(reason); reasonErr != "" {
		return MarkerResult{Found: true, Line: markerLine, Lines: nil, Section: "", Reason: "", Error: reasonErr}
	}
	if section == "" || !names[NormalizeHeading(section)] {
		return MarkerResult{
			Found: true, Line: markerLine, Lines: nil, Section: "", Reason: "",
			Error: "doc marker section §" + section + " does not match an existing ## heading",
		}
	}
	return MarkerResult{Found: true, Line: markerLine, Lines: nil, Section: section, Reason: reason, Error: ""}
}

// markerBlock is one raw HTML block that begins with a LENGTH marker.
type markerBlock struct {
	value  string
	offset int
}

// markerBlocks returns every top-level HTML block whose text opens with "<!-- LENGTH".
func markerBlocks(doc Doc) []markerBlock {
	var blocks []markerBlock
	for node := doc.root.FirstChild(); node != nil; node = node.NextSibling() {
		html, ok := node.(*gast.HTMLBlock)
		if !ok || html.Lines().Len() == 0 {
			continue
		}
		value := strings.TrimSpace(htmlBlockText(html, doc.source))
		if !hasLengthPrefix(value) {
			continue
		}
		blocks = append(blocks, markerBlock{value: value, offset: html.Lines().At(0).Start})
	}
	return blocks
}

// hasLengthPrefix reports whether value opens with "<!--", optional space, then LENGTH as a whole word.
func hasLengthPrefix(value string) bool {
	rest, ok := strings.CutPrefix(value, "<!--")
	if !ok {
		return false
	}
	rest = strings.TrimLeft(rest, " \t\r\n")
	if !strings.HasPrefix(rest, text.Marker) {
		return false
	}
	after := rest[len(text.Marker):]
	return after == "" || !text.IsWordByte(after[0])
}

// markerBody returns the text between "§" and the closing "-->" of a single-line marker. The TypeScript
// pattern is `^<!--\s*LENGTH\s+§([^:]+:.*?)\s*-->$`: at least one space must follow LENGTH, and the body
// needs a colon preceded by a non-colon character. Otherwise the marker is malformed.
func markerBody(value string) (string, bool) {
	rest, ok := strings.CutPrefix(value, "<!--")
	if !ok {
		return "", false
	}
	rest = strings.TrimLeft(rest, " \t")
	rest, ok = strings.CutPrefix(rest, text.Marker)
	if !ok {
		return "", false
	}
	spaced := strings.TrimLeft(rest, " \t")
	if len(spaced) == len(rest) {
		return "", false
	}
	rest, ok = strings.CutPrefix(spaced, "§")
	if !ok {
		return "", false
	}
	trimmed := strings.TrimRight(rest, " \t")
	body, ok := strings.CutSuffix(trimmed, "-->")
	if !ok {
		return "", false
	}
	body = strings.TrimRight(body, " \t")
	if colon := strings.IndexByte(body, ':'); colon < 1 {
		return "", false
	}
	return body, true
}

// splitSectionReason splits "Section: reason". It prefers the longest colon-prefix that names a
// heading and leaves a valid reason, then any colon-prefix that names a heading, then the last colon.
func splitSectionReason(body string, names map[string]bool) (string, string) {
	colons := colonsDescending(body)
	split := -1
	for _, at := range colons {
		if names[NormalizeHeading(body[:at])] && text.InvalidReason(strings.TrimSpace(body[at+1:])) == "" {
			split = at
			break
		}
	}
	if split < 0 {
		for _, at := range colons {
			if names[NormalizeHeading(body[:at])] {
				split = at
				break
			}
		}
	}
	if split < 0 && len(colons) > 0 {
		split = colons[0]
	}
	if split < 0 {
		return strings.TrimSpace(body), ""
	}
	return strings.TrimSpace(body[:split]), strings.TrimSpace(body[split+1:])
}

// colonsDescending returns the byte index of each colon, last first.
func colonsDescending(body string) []int {
	var colons []int
	for at := len(body) - 1; at >= 0; at-- {
		if body[at] == ':' {
			colons = append(colons, at)
		}
	}
	return colons
}

// heading2 is a level-2 heading and its byte offset in the source.
type heading2 struct {
	text   string
	offset int
}

// headingNames returns the set of normalized level-2 heading texts.
func headingNames(headings []heading2) map[string]bool {
	names := make(map[string]bool, len(headings))
	for _, h := range headings {
		names[NormalizeHeading(h.text)] = true
	}
	return names
}

// level2Headings returns the level-2 headings in document order.
func level2Headings(doc Doc) []heading2 {
	var out []heading2
	var visit func(gast.Node)
	visit = func(node gast.Node) {
		if h, ok := node.(*gast.Heading); ok && h.Level == 2 {
			out = append(out, heading2{text: inlineText(h, doc.source), offset: headingOffset(h)})
		}
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			visit(child)
		}
	}
	visit(doc.root)
	return out
}
