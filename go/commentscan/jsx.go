// SPDX-License-Identifier: MIT OR Apache-2.0

package commentscan

import (
	"slices"
	"strings"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars/tsx"
)

type commentRange struct {
	start int
	end   int
}

func jsxParser() *gotreesitter.Parser {
	return gotreesitter.NewParser(tsx.Language())
}

// isStringLiteral excludes the anonymous `string` keyword token of type annotations.
func isStringLiteral(node *gotreesitter.Node, lang *gotreesitter.Language) bool {
	return node.IsNamed() && node.Type(lang) == "string"
}

func jsxCommentRanges(
	node *gotreesitter.Node,
	lang *gotreesitter.Language,
	source []byte,
	inJSXText, inString, inJSXAttribute bool,
	stringEnd uint32,
	ranges *[]commentRange,
) {
	if node == nil {
		return
	}
	if isStringLiteral(node, lang) {
		inString = true
		stringEnd = jsxStringEnd(node, lang, source, node.EndByte())
		parent := node.Parent()
		inJSXAttribute = parent != nil && parent.Type(lang) == "jsx_attribute"
	}
	inJSXText = inJSXText || node.Type(lang) == "jsx_text"
	nodeType := node.Type(lang)
	if nodeType == "comment" && !inJSXText && (!inString || !inJSXAttribute && node.EndByte() > stringEnd) {
		*ranges = append(*ranges, commentRange{int(node.StartByte()), int(node.EndByte())})
	}
	for i := range node.ChildCount() {
		jsxCommentRanges(node.Child(i), lang, source, inJSXText, inString, inJSXAttribute, stringEnd, ranges)
	}
}

// jsxHasUntrustedParseErrors ignores parse errors contained by a string.
// Tree-sitter can expose string text as comment nodes; nodes beyond the actual
// closing quote make the tree's comment locations untrustworthy.
func jsxHasUntrustedParseErrors(
	node *gotreesitter.Node,
	lang *gotreesitter.Language,
	source []byte,
	inString bool,
	stringEnd uint32,
) bool {
	if node == nil {
		return false
	}
	if isStringLiteral(node, lang) {
		inString = true
		stringEnd = jsxStringEnd(node, lang, source, node.EndByte())
	}
	if node.IsMissing() || node.IsError() && (!inString || node.EndByte() > stringEnd) {
		return true
	}
	for i := range node.ChildCount() {
		if jsxHasUntrustedParseErrors(node.Child(i), lang, source, inString, stringEnd) {
			return true
		}
	}
	return false
}

// jsxHasStringOverrun checks the unmodified parse tree before attribute
// ampersands are normalized for tree-sitter.
func jsxHasStringOverrun(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte) bool {
	if node == nil {
		return false
	}
	if isStringLiteral(node, lang) && jsxStringHasOverrun(node, lang, source) {
		return true
	}
	for i := range node.ChildCount() {
		if jsxHasStringOverrun(node.Child(i), lang, source) {
			return true
		}
	}
	return false
}

func jsxStringHasOverrun(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte) bool {
	stringEnd := jsxStringEnd(node, lang, source, node.EndByte())
	parent := node.Parent()
	if parent == nil || parent.Type(lang) != "jsx_attribute" {
		return false
	}
	for i := range node.ChildCount() {
		child := node.Child(i)
		if child == nil {
			continue
		}
		if child.Type(lang) == "comment" && child.StartByte() < stringEnd && child.EndByte() > stringEnd {
			return true
		}
		if jsxStringHasOverrun(child, lang, source) {
			return true
		}
	}
	return false
}
func jsxStringEnd(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte, fallback uint32) uint32 {
	start := node.StartByte()
	if uint64(start) >= uint64(len(source)) || source[int(start)] != '\'' && source[int(start)] != '"' {
		return fallback
	}
	parent := node.Parent()
	jsxAttribute := parent != nil && parent.Type(lang) == "jsx_attribute"
	quote := source[int(start)]
	for i := int(start) + 1; i < len(source); i++ {
		if source[i] == '\\' && !jsxAttribute {
			i++
			continue
		}
		if source[i] == quote {
			if uint64(i) >= uint64(^uint32(0)) {
				return fallback
			}
			return uint32(i + 1)
		}
	}
	return fallback
}

func jsxStringParseDiagnostic(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte) bool {
	if node == nil {
		return false
	}
	if isStringLiteral(node, lang) && jsxStringNeedsDiagnostic(node, lang) {
		start, end := node.StartByte(), node.EndByte()
		if uint64(end) > uint64(len(source)) || start >= end {
			return true
		}
		if jsxStringHasParseDiagnostic(source[int(start):int(end)]) {
			return true
		}
	}
	if jsxTemplateNeedsDiagnostic(node, lang) && jsxTemplateHasParseDiagnostic(node, lang, source) {
		return true
	}

	for i := range node.ChildCount() {
		if jsxStringParseDiagnostic(node.Child(i), lang, source) {
			return true
		}
	}
	return false
}

func jsxStringNeedsDiagnostic(node *gotreesitter.Node, lang *gotreesitter.Language) bool {
	parent := node.Parent()
	return parent == nil || parent.Type(lang) != "jsx_attribute"
}

func jsxTemplateNeedsDiagnostic(node *gotreesitter.Node, lang *gotreesitter.Language) bool {
	if !node.IsNamed() || node.Type(lang) != "template_string" {
		return false
	}
	parent := node.Parent()
	return parent == nil || parent.Type(lang) != "call_expression"
}

func jsxTemplateHasParseDiagnostic(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte) bool {
	if node == nil {
		return false
	}
	if node.IsNamed() && node.Type(lang) == "escape_sequence" {
		start, end := node.StartByte(), node.EndByte()
		if uint64(end) > uint64(len(source)) || start >= end {
			return true
		}
		if jsxEscapeHasParseDiagnostic(source[int(start):int(end)]) {
			return true
		}
	}
	for i := range node.ChildCount() {
		if jsxTemplateHasParseDiagnostic(node.Child(i), lang, source) {
			return true
		}
	}
	return false
}

func jsxEscapeHasParseDiagnostic(escape []byte) bool {
	if len(escape) < 2 || escape[0] != '\\' {
		return true
	}
	escaped := escape[1]
	switch {
	case escaped == '8' || escaped == '9', escaped >= '1' && escaped <= '7':
		return true
	case escaped == '0' && len(escape) > 2 && escape[2] >= '0' && escape[2] <= '9':
		return true
	case escaped == 'x':
		if len(escape) != 4 {
			return true
		}
		if _, ok := jsxHexValue(escape[2]); !ok {
			return true
		}
		if _, ok := jsxHexValue(escape[3]); !ok {
			return true
		}
	case escaped == 'u':
		if len(escape) == 6 {
			for _, c := range escape[2:] {
				if _, ok := jsxHexValue(c); !ok {
					return true
				}
			}
			return false
		}
		if len(escape) < 5 || escape[2] != '{' || escape[len(escape)-1] != '}' {
			return true
		}
		value := uint32(0)
		for _, c := range escape[3 : len(escape)-1] {
			hex, ok := jsxHexValue(c)
			if !ok {
				return true
			}
			value = value*16 + uint32(hex)
			if value > 0x10ffff {
				return true
			}
		}
		return len(escape) == 4
	}
	return false
}

func jsxStringHasParseDiagnostic(literal []byte) bool {
	if len(literal) < 2 || literal[0] != '\'' && literal[0] != '"' {
		return true
	}
	quote := literal[0]
	for i := 1; i < len(literal); i++ {
		c := literal[i]
		if c == quote {
			return false
		}
		if c == '\n' || c == '\r' {
			return true
		}
		if c != '\\' {
			continue
		}
		i++
		if i >= len(literal) {
			return true
		}
		escaped := literal[i]
		if escaped == '\n' || escaped == '\r' {
			if escaped == '\r' && i+1 < len(literal) && literal[i+1] == '\n' {
				i++
			}
			continue
		}
		switch {
		case escaped == '8' || escaped == '9', escaped >= '1' && escaped <= '7':
			return true
		case escaped == '0' && i+1 < len(literal)-1 && literal[i+1] >= '0' && literal[i+1] <= '9':
			return true
		case escaped == 'x':
			if i+2 >= len(literal)-1 {
				return true
			}
			if _, ok := jsxHexValue(literal[i+1]); !ok {
				return true
			}
			if _, ok := jsxHexValue(literal[i+2]); !ok {
				return true
			}
			i += 2
		case escaped == 'u':
			end, invalid := jsxUnicodeEscapeEnd(literal, i)
			if invalid {
				return true
			}
			i = end
		}
	}
	return true
}

func jsxUnicodeEscapeEnd(literal []byte, escape int) (int, bool) {
	if escape+1 >= len(literal)-1 {
		return 0, true
	}
	if literal[escape+1] != '{' {
		end := escape + 4
		if end >= len(literal) {
			return 0, true
		}
		for i := escape + 1; i <= end; i++ {
			if _, ok := jsxHexValue(literal[i]); !ok {
				return 0, true
			}
		}
		return end, false
	}
	value := uint32(0)
	digits := 0
	end := escape + 2
	for end < len(literal)-1 && literal[end] != '}' {
		hex, ok := jsxHexValue(literal[end])
		if !ok {
			return 0, true
		}
		value = value*16 + uint32(hex)
		digits++
		if value > 0x10ffff {
			return 0, true
		}
		end++
	}
	return end, digits == 0 || end >= len(literal)-1
}

func jsxHexValue(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func jsxParseText(text []byte, root *gotreesitter.Node, lang *gotreesitter.Language) bool {
	var ranges []commentRange
	jsxAttributeStringRanges(root, lang, &ranges)
	changed := false
	for _, span := range ranges {
		if span.start < 0 || span.end > len(text) || span.start >= span.end {
			continue
		}
		for i := span.start; i < span.end; i++ {
			if text[i] == '&' {
				text[i] = 'x'
				changed = true
			}
		}
	}
	return changed
}

func jsxAttributeStringRanges(node *gotreesitter.Node, lang *gotreesitter.Language, ranges *[]commentRange) {
	if node == nil {
		return
	}
	if isStringLiteral(node, lang) {
		parent := node.Parent()
		if parent != nil && parent.Type(lang) == "jsx_attribute" {
			*ranges = append(*ranges, commentRange{start: int(node.StartByte()), end: int(node.EndByte())})
		}
	}
	for i := range node.ChildCount() {
		jsxAttributeStringRanges(node.Child(i), lang, ranges)
	}
}

// ScanJSX deletes non-directive comments from TSX/JSX text and splits it into lines.
func ScanJSX(text string) Residues {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	raw := []byte(text)
	parser := jsxParser()
	rawTree, err := parser.Parse(raw)
	if err != nil {
		return Residues{Residues: strings.Split(text, "\n"), Ambiguous: true}
	}
	if jsxHasStringOverrun(rawTree.RootNode(), tsx.Language(), raw) {
		rawTree.Release()
		return Residues{Residues: strings.Split(text, "\n"), Ambiguous: true}
	}
	parsed := raw
	tree := rawTree
	if jsxParseText(parsed, rawTree.RootNode(), tsx.Language()) {
		rawTree.Release()
		tree, err = parser.Parse(parsed)
		if err != nil {
			return Residues{Residues: strings.Split(text, "\n"), Ambiguous: true}
		}
	}
	defer tree.Release()
	root := tree.RootNode()
	if jsxHasUntrustedParseErrors(root, tsx.Language(), parsed, false, 0) ||
		jsxStringParseDiagnostic(root, tsx.Language(), parsed) {
		return Residues{Residues: strings.Split(text, "\n"), Ambiguous: true}
	}

	ranges := make([]commentRange, 0)
	jsxCommentRanges(root, tsx.Language(), parsed, false, false, false, 0, &ranges)
	ranges = slices.DeleteFunc(ranges, func(span commentRange) bool {
		return tsDirective.MatchString(text[span.start:span.end])
	})
	slices.SortFunc(ranges, func(a, b commentRange) int { return a.start - b.start })

	var out strings.Builder
	out.Grow(len(text))
	at := 0
	for _, span := range ranges {
		if span.start < at {
			continue
		}
		out.WriteString(text[at:span.start])
		for _, b := range text[span.start:span.end] {
			if b == '\n' {
				out.WriteByte('\n')
			}
		}
		at = span.end
	}
	out.WriteString(text[at:])
	lines := strings.Split(out.String(), "\n")
	for i := range lines {
		lines[i] = trimTrailing(lines[i])
	}
	return Residues{Residues: lines, Ambiguous: false}
}
