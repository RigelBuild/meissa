// SPDX-License-Identifier: MIT OR Apache-2.0

package markdown

import (
	"strings"
	"unicode/utf8"

	"github.com/RigelBuild/meissa/go/internal/jstext"
)

// HTMLText strips HTML comments and script/style bodies, then turns other tags into spaces.
func HTMLText(html string) string {
	return stripTags(stripScriptStyle(stripComments(html)))
}

// stripComments replaces each HTML comment with a space; an unclosed comment runs to the end.
func stripComments(html string) string {
	var out strings.Builder
	for {
		start := strings.Index(html, "<!--")
		if start < 0 {
			out.WriteString(html)
			return out.String()
		}
		out.WriteString(html[:start])
		out.WriteString(" ")
		rest := html[start+len("<!--"):]
		_, after, found := strings.Cut(rest, "-->")
		if !found {
			return out.String()
		}
		html = after
	}
}

// stripScriptStyle replaces each script or style element with a space.
// An unclosed element runs to the end of the document.
func stripScriptStyle(html string) string {
	lower := asciiLower(html)
	var out strings.Builder
	pos := 0
	for {
		open, name := findTagOpen(html, lower, pos)
		if open < 0 {
			out.WriteString(html[pos:])
			return out.String()
		}
		out.WriteString(html[pos:open])
		out.WriteString(" ")
		pos = findScriptClose(html, lower, open+1+len(name), name)
	}
}

// findTagOpen returns the index and name of the next <script or <style opener, or -1.
func findTagOpen(html, lower string, from int) (int, string) {
	for i := from; i < len(lower); i++ {
		if lower[i] != '<' {
			continue
		}
		for _, name := range [...]string{"script", "style"} {
			if !strings.HasPrefix(lower[i+1:], name) {
				continue
			}
			after := i + 1 + len(name)
			if after < len(html) && endsTagName(html[after:]) {
				return i, name
			}
		}
	}
	return -1, ""
}

// endsTagName reports whether the text may follow a tag name: whitespace, "/" or ">".
func endsTagName(text string) bool {
	r, _ := utf8.DecodeRuneInString(text)
	return jstext.IsSpace(r) || r == '/' || r == '>'
}

// findScriptClose returns the index after the matching "</name\s*>", or len(html) when none exists.
func findScriptClose(html, lower string, from int, name string) int {
	closing := "</" + name
	for i := from; i <= len(lower); {
		idx := strings.Index(lower[i:], closing)
		if idx < 0 {
			return len(html)
		}
		at := i + idx + len(closing)
		next := skipSpaces(html, at)
		if next < len(html) && html[next] == '>' {
			return next + 1
		}
		i = i + idx + 1
	}
	return len(html)
}

// stripTags replaces each "<...>" run with a space. A "<" with no later ">" is text.
func stripTags(html string) string {
	var out strings.Builder
	pos := 0
	for {
		open := strings.IndexByte(html[pos:], '<')
		if open < 0 {
			break
		}
		open += pos
		closeAt := strings.IndexByte(html[open+1:], '>')
		if closeAt < 0 {
			break
		}
		out.WriteString(html[pos:open])
		out.WriteString(" ")
		pos = open + 1 + closeAt + 1
	}
	out.WriteString(html[pos:])
	return out.String()
}

// stripHeadingID removes a trailing "\s+{#id}\s*" anchor.
// The anchor opens at the first "{#" after the previous "}", like the regex's leftmost match.
func stripHeadingID(heading string) string {
	text := jstext.TrimRight(heading)
	if !strings.HasSuffix(text, "}") {
		return heading
	}
	body := text[:len(text)-1]
	from := strings.LastIndexByte(body, '}') + 1
	open := strings.Index(body[from:], "{#")
	if open < 0 {
		return heading
	}
	open += from
	if len(body) == open+2 {
		return heading
	}
	prefix := text[:open]
	trimmed := jstext.TrimRight(prefix)
	if len(trimmed) == len(prefix) {
		return heading
	}
	return trimmed
}

// NormalizeHeading matches headings: no anchor, no leading §, no backticks, single spaces, lower case.
func NormalizeHeading(heading string) string {
	text := stripHeadingID(heading)
	text = strings.TrimPrefix(text, "§")
	text = strings.ReplaceAll(text, "`", "")
	return jstext.ToLower(strings.Join(jstext.Fields(text), " "))
}

// skipSpaces returns the index of the first non-space rune at or after i.
func skipSpaces(text string, i int) int {
	for i < len(text) {
		r, size := utf8.DecodeRuneInString(text[i:])
		if !jstext.IsSpace(r) {
			break
		}
		i += size
	}
	return i
}

// asciiLower lowers only A-Z, so byte offsets in the result match the input.
func asciiLower(text string) string {
	b := []byte(text)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
