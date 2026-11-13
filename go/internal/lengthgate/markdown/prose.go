// SPDX-License-Identifier: MIT OR Apache-2.0

package markdown

import (
	"html"
	"strings"

	gast "github.com/yuin/goldmark/ast"
	gextast "github.com/yuin/goldmark/extension/ast"

	"github.com/RigelBuild/meissa/go/internal/jstext"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/text"
)

// ProseWords counts prose words in markdown. A leading BOM is dropped, as the TypeScript does.
// With dropErrata, the "## Errata" section and everything under it is not counted.
func ProseWords(markdown string, dropErrata bool) int {
	doc := Parse([]byte(markdown))
	count := 0
	blocks := append(proseBlocks(doc, dropErrata), orphanFootnoteTexts(doc, dropErrata)...)
	for _, token := range jstext.Fields(strings.Join(blocks, "\n")) {
		if text.HasLetterOrDigit(token) {
			count++
		}
	}
	return count
}

// proseBlocks returns the visible text of each prose block, in document order.
// Code, tables, link definitions and front matter are skipped. An "## Errata" heading that is a
// top-level child starts a skipped run, which ends at the next top-level heading of depth 1 or 2.
// A footnote definition is judged by its own source line, so goldmark moving it to the end of the
// document does not change whether Errata drops it.
func proseBlocks(doc Doc, dropErrata bool) []string {
	var blocks []string
	inErrata := false
	starts := lineStarts(doc.source)
	errata := errataRanges(doc, starts, dropErrata)
	var visit func(node gast.Node)
	visit = func(node gast.Node) {
		if h, ok := node.(*gast.Heading); ok && dropErrata && h.Parent() == doc.root {
			if h.Level <= 2 {
				inErrata = false
			}
			if h.Level == 2 && NormalizeHeading(inlineText(h, doc.source)) == "errata" {
				inErrata = true
				return
			}
		}
		// Footnotes are judged by their own line below, so the tree gate skips the footnote list and its items.
		_, isFootnoteList := node.(*gextast.FootnoteList)
		_, isFootnote := node.(*gextast.Footnote)
		if dropErrata && inErrata && !isFootnoteList && !isFootnote {
			return
		}
		switch n := node.(type) {
		case *gextast.FootnoteList:
			// goldmark gathers every referenced footnote here. Each definition is counted below,
			// by its own source line.
		case *gextast.Footnote:
			if !inRanges(errata, LineOf(starts, nodeOffset(n))) {
				blocks = append(blocks, inlineText(n, doc.source))
			}
			return
		case *gast.Heading, *gast.Paragraph, *gast.TextBlock:
			blocks = append(blocks, inlineText(n, doc.source))
			return
		case *gast.HTMLBlock:
			if body := strings.TrimSpace(HTMLText(htmlBlockText(n, doc.source))); body != "" {
				blocks = append(blocks, body)
			}
			return
		}
		if isSkippedBlock(node) {
			return
		}
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			visit(child)
		}
	}
	visit(doc.root)
	return blocks
}

// nodeOffset returns the byte offset of a block node's first line segment. A footnote definition has no
// segments of its own, so its first child block supplies the offset. It returns 0 when none has one.
func nodeOffset(node gast.Node) int {
	for n := node; n != nil; n = n.FirstChild() {
		if n.Lines().Len() > 0 {
			return n.Lines().At(0).Start
		}
	}
	return 0
}

// isSkippedBlock reports whether a block carries no prose: code and tables.
func isSkippedBlock(node gast.Node) bool {
	switch node.(type) {
	case *gast.FencedCodeBlock, *gast.CodeBlock, *gextast.Table:
		return true
	}
	return false
}

// inlineText returns the visible text of a block's inline children, skipping inline script and style bodies.
func inlineText(node gast.Node, source []byte) string {
	var out strings.Builder
	skipping := false
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if raw, ok := child.(*gast.RawHTML); ok {
			tag := rawHTMLText(raw, source)
			if opens, closesSame := scriptOpen(tag); opens {
				skipping = !closesSame
				continue
			}
			if scriptClose(tag) {
				skipping = false
				continue
			}
		}
		if !skipping {
			out.WriteString(inlineNodeText(child, source))
		}
	}
	return out.String()
}

// scriptOpen reports whether an inline raw tag opens a script or style element,
// and whether that same tag also closes it.
func scriptOpen(tag string) (bool, bool) {
	lower := strings.ToLower(strings.TrimLeft(tag, " \t\r\n"))
	for _, name := range []string{"script", "style"} {
		rest, ok := strings.CutPrefix(lower, "<"+name)
		if !ok || (rest != "" && !strings.ContainsRune(" \t\r\n/>", rune(rest[0]))) {
			continue
		}
		return true, strings.HasSuffix(strings.TrimRight(lower, " \t\r\n"), "</"+name+">")
	}
	return false, false
}

// scriptClose reports whether an inline raw tag closes a script or style element.
func scriptClose(tag string) bool {
	lower := strings.ToLower(strings.TrimLeft(tag, " \t\r\n"))
	for _, name := range []string{"script", "style"} {
		rest, ok := strings.CutPrefix(lower, "</"+name)
		if ok && (rest == "" || strings.ContainsRune(" \t\r\n>", rune(rest[0]))) {
			return true
		}
	}
	return false
}

// inlineNodeText returns the visible text of one inline node.
func inlineNodeText(node gast.Node, source []byte) string {
	switch n := node.(type) {
	case *gast.Text:
		return textValue(n, source)
	case *gast.String:
		return decodeEntities(string(n.Value))
	case *gast.CodeSpan:
		return codeSpanText(n, source)
	case *gast.AutoLink, *gextast.FootnoteLink:
		return ""
	case *gast.Image:
		return decodeEntities(imageAltText(n, source))
	case *gast.RawHTML:
		return " " + HTMLText(rawHTMLText(n, source)) + " "
	}
	return inlineText(node, source)
}

// decodeEntities decodes HTML character references, as mdast does for prose. goldmark leaves
// them raw in text segments, so "&mdash;" would otherwise count as a word.
func decodeEntities(value string) string {
	return html.UnescapeString(value)
}

// imageAltText returns an image's alt text: the visible text of its inline children, joined in order.
// It replaces goldmark's deprecated Image.Text, which concatenated the children's raw text. Each child
// now goes through inlineNodeText, so an entity or raw HTML in the alt counts as it does elsewhere.
func imageAltText(image *gast.Image, source []byte) string {
	var out strings.Builder
	for child := image.FirstChild(); child != nil; child = child.NextSibling() {
		out.WriteString(inlineNodeText(child, source))
	}
	return out.String()
}

// textValue returns a Text node's decoded bytes, with a space for a hard break and a newline for a soft break.
func textValue(n *gast.Text, source []byte) string {
	seg := n.Segment
	value := decodeEntities(string(seg.Value(source)))
	if n.HardLineBreak() {
		return value + " "
	}
	if n.SoftLineBreak() {
		return value + "\n"
	}
	return value
}

// codeSpanText returns the text of a code span's children. Code is not entity-decoded, as the TypeScript
// keeps code raw.
func codeSpanText(n *gast.CodeSpan, source []byte) string {
	var out strings.Builder
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if t, ok := child.(*gast.Text); ok {
			seg := t.Segment
			out.Write(seg.Value(source))
		}
	}
	return out.String()
}

// rawHTMLText returns the raw text of an inline raw HTML node, joined across its segments.
func rawHTMLText(n *gast.RawHTML, source []byte) string {
	var out strings.Builder
	for i := range n.Segments.Len() {
		seg := n.Segments.At(i)
		out.Write(seg.Value(source))
	}
	return out.String()
}

// htmlBlockText returns the raw lines of an HTML block, including its closing line when present.
func htmlBlockText(n *gast.HTMLBlock, source []byte) string {
	var out strings.Builder
	for i := range n.Lines().Len() {
		seg := n.Lines().At(i)
		out.Write(seg.Value(source))
	}
	if n.HasClosure() {
		closure := n.ClosureLine
		out.Write(closure.Value(source))
	}
	return out.String()
}
