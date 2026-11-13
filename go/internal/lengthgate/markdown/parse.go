// SPDX-License-Identifier: MIT OR Apache-2.0

// Package markdown parses documents with goldmark (GFM and footnotes) and walks their prose.
package markdown

import (
	"bytes"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// Doc is a parsed document plus the source its segments index into.
type Doc struct {
	root gast.Node
	// source is the BOM-free text with any front matter blanked, so byte offsets match the input.
	source []byte
	// frontMatterEnd is the 1-based line of the closing fence, or 0 when there is no front matter.
	frontMatterEnd int
}

// Parse parses GFM and footnotes. A leading BOM is dropped, and a YAML front matter block is
// blanked before parsing, so its lines never reach the tree.
func Parse(source []byte) Doc {
	// bom is the byte order mark micromark drops before parsing.
	bom := []byte("\uFEFF")
	source = bytes.TrimPrefix(source, bom)
	end := frontMatterEnd(source)
	blanked := blankFrontMatter(source, end)
	md := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.Footnote))
	root := md.Parser().Parse(text.NewReader(blanked))
	return Doc{root: root, source: blanked, frontMatterEnd: end}
}

// blankFrontMatter replaces lines 1..end with spaces. Line endings are kept, so line numbers and
// byte offsets still match the source. With end 0 the source is returned unchanged.
func blankFrontMatter(source []byte, end int) []byte {
	if end == 0 {
		return source
	}
	out := bytes.Clone(source)
	starts := lineStarts(source)
	for line := 1; line <= end; line++ {
		body, begin := lineBody(source, starts, line)
		for i := range body {
			out[begin+i] = ' '
		}
	}
	return out
}

// frontMatterEnd returns the last line of front matter, or 0 when there is none.
// The fences are "---" lines; a block with no closing fence is ordinary markdown.
func frontMatterEnd(source []byte) int {
	lines := bytes.Split(source, []byte("\n"))
	if len(lines) == 0 || !isFence(lines[0]) {
		return 0
	}
	for i := 1; i < len(lines); i++ {
		if isFence(lines[i]) {
			return i + 1
		}
	}
	return 0
}

// isFence reports whether line is "---" with optional trailing spaces, tabs or a CR.
func isFence(line []byte) bool {
	return string(bytes.TrimRight(line, " \t\r")) == "---"
}

// LineOf returns the 1-based line containing byte offset off, from the line-start table.
func LineOf(starts []int, off int) int {
	lo, hi := 0, len(starts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if starts[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1
}

// lineStarts returns the byte offset at which each line begins.
func lineStarts(source []byte) []int {
	starts := []int{0}
	for i, c := range source {
		if c == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// lineBody returns the 1-based line's bytes without its line ending, and the line's start offset.
func lineBody(source []byte, starts []int, line int) ([]byte, int) {
	begin := starts[line-1]
	end := len(source)
	if line < len(starts) {
		end = starts[line] - 1
	}
	return bytes.TrimSuffix(source[begin:end], []byte("\r")), begin
}
