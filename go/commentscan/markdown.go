// SPDX-License-Identifier: MIT OR Apache-2.0

package commentscan

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/RigelBuild/meissa/go/internal/jstext"
	"github.com/samber/mo"
)

// MarkdownItem is one load-bearing Markdown structure entry.
type MarkdownItem struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// MarkdownScan contains a structural fingerprint and an optional defect.
type MarkdownScan struct {
	Items  []MarkdownItem
	Defect string
}

// MarkdownVerdict is the sealed result of comparing two Markdown structures.
type MarkdownVerdict interface{ isMarkdownVerdict() }

// VerdictSame reports equal, well-formed Markdown fingerprints.
type VerdictSame struct{}

func (VerdictSame) isMarkdownVerdict() {}

// VerdictMismatch reports the first differing fingerprint entry.
type VerdictMismatch struct {
	Index         int
	Before, After mo.Option[MarkdownItem]
}

func (VerdictMismatch) isMarkdownVerdict() {}

// VerdictMalformed reports a side whose structural fingerprint is unreliable.
type VerdictMalformed struct {
	Side, Reason string
}

func (VerdictMalformed) isMarkdownVerdict() {}

var directiveRE = regexp.MustCompile(`^(rumdl|markdownlint|prettier-ignore)`)
var frontOpenRE = regexp.MustCompile(`^---[\t-\r \p{Zs}\x{2028}\x{2029}\x{feff}]*$`)
var frontCloseRE = regexp.MustCompile(`^(---|\.\.\.)[\t-\r \p{Zs}\x{2028}\x{2029}\x{feff}]*$`)
var fenceOpenRE = regexp.MustCompile(`^(?: {0,3}(?:> ?|(?:[-*+] |\d+[.)] ))*)(` + "`" + `{3,}|~{3,})(.*)$`)
var fenceCloseRE = regexp.MustCompile(`^(?: {0,3}(?:> ?|(?:[-*+] |\d+[.)] ))*)(` + "`" + `{3,}|~{3,})[ \t]*$`)
var alignmentCharsRE = regexp.MustCompile(`^[|:\-` + "`" + `\t-\r \p{Zs}\x{2028}\x{2029}\x{feff}` + `]+$`)
var alignmentCellRE = regexp.MustCompile(
	`^[\t-\r \p{Zs}\x{2028}\x{2029}\x{feff}]*:?-+:?` +
		`[\t-\r \p{Zs}\x{2028}\x{2029}\x{feff}]*$`,
)
var paragraphBlankRE = regexp.MustCompile(`^[\t-\r \p{Zs}\x{2028}\x{2029}\x{feff}]*$`)
var paragraphFenceRE = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
var paragraphHeadingRE = regexp.MustCompile(`^ {0,3}#{1,6}([\t-\r \p{Zs}\x{2028}\x{2029}\x{feff}]|$)`)
var paragraphListRE = regexp.MustCompile(`^ {0,3}([-*+]|\d{1,9}[.)])([\t-\r \p{Zs}\x{2028}\x{2029}\x{feff}]|$)`)
var paragraphQuoteRE = regexp.MustCompile(`^ {0,3}>`)
var paragraphRefRE = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:`)
var scriptStyleRE = regexp.MustCompile(
	`(?i)^<(script|style)\b[^>]*>(?:` + jsSpace + `|` + jsNonSpace + `)*</(script|style)>`,
)
var htmlTagStartRE = regexp.MustCompile(`^</?[A-Za-z]`)
var autolinkURIRE = regexp.MustCompile(`^<([a-zA-Z][a-zA-Z0-9+.-]{1,31}:[^<>` + jsSpaceChars + `]*)>`)
var autolinkEmailRE = regexp.MustCompile(
	`^<([^<>` + jsSpaceChars + `@]+@[^<>` + jsSpaceChars + `]+\.[^<>` + jsSpaceChars + `]+)>`,
)
var bareURLRE = regexp.MustCompile(`^https?://[^` + jsSpaceChars + `<>\[\]]+`)
var referenceDefRE = regexp.MustCompile(`^ {0,3}\[([^\]]+)\]:[ \t]*(.+)$`)
var headingRE = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*))?$`)
var headingEndHashRE = regexp.MustCompile(`[ \t]+#+[ \t]*$`)
var blockquoteRE = regexp.MustCompile(`^ {0,3}(>+)[ \t]?`)
var thematicRE = regexp.MustCompile(
	`^ {0,3}(?:(?:\*` + jsSpace + `*){3,}|(?:-` + jsSpace + `*){3,}|(?:_` + jsSpace + `*){3,})$`,
)
var setextRE = regexp.MustCompile(`^ {0,3}(=+|-+)[ \t]*$`)
var listMarkerRE = regexp.MustCompile(`^ {0,3}([-*+]|\d{1,9}[.)])([ \t]|$)`)

func indentWidth(line string) int {
	w := 0
	for _, r := range line {
		switch r {
		case ' ':
			w++
		case '\t':
			w += 4
		default:
			return w
		}
	}
	return w
}

func normalizeLabel(label string) string {
	fields := jstext.Fields(jstext.ToLower(jstext.Trim(label)))
	return strings.Join(fields, " ")
}

func isAlignmentRow(line string) bool {
	t := jstext.Trim(line)
	if t == "" || !alignmentCharsRE.MatchString(t) {
		return false
	}
	cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(t, "|"), "|"), "|")
	for _, cell := range cells {
		if !alignmentCellRE.MatchString(cell) {
			return false
		}
	}
	return len(cells) > 0
}

func isParagraph(line string) bool {
	if paragraphBlankRE.MatchString(line) || paragraphFenceRE.MatchString(line) ||
		paragraphHeadingRE.MatchString(line) || paragraphListRE.MatchString(line) ||
		paragraphQuoteRE.MatchString(line) || paragraphRefRE.MatchString(line) {
		return false
	}
	return indentWidth(line) < 4
}

func backtickRun(s string, i int) int {
	j := i
	for j < len(s) && s[j] == '`' {
		j++
	}
	return j - i
}

func findCodeClose(s string, from, size int) int {
	for i := from; i < len(s); {
		if s[i] == '`' {
			run := backtickRun(s, i)
			if run == size {
				return i
			}
			i += run
		} else {
			_, n := utf8.DecodeRuneInString(s[i:])
			i += n
		}
	}
	return -1
}

func matchBracket(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

type markdownDest struct {
	target string
	end    int
}

func parseAngleDest(s string, at int) (string, int, bool) {
	i := at + 1
	target := strings.Builder{}
	for i < len(s) && s[i] != '>' {
		if s[i] == '\\' {
			if i+1 < len(s) {
				target.WriteByte(s[i+1])
			}
			i += 2
			continue
		}
		target.WriteByte(s[i])
		i++
	}
	if i >= len(s) {
		return "", 0, false
	}
	return target.String(), i + 1, true
}
func parseInlineDest(s string, paren int) (markdownDest, bool) {
	i := paren + 1
	for i < len(s) && jstext.IsSpace(rune(s[i])) {
		i++
	}
	target := strings.Builder{}
	if i < len(s) && s[i] == '<' {
		target, end, ok := parseAngleDest(s, i)
		if !ok {
			return markdownDest{target: "", end: 0}, false
		}
		for end < len(s) && s[end] != ')' {
			_, n := utf8.DecodeRuneInString(s[end:])
			end += n
		}
		if end >= len(s) {
			return markdownDest{target: "", end: 0}, false
		}
		return markdownDest{target: target, end: end + 1}, true
	}
	depth := 0
	for i < len(s) {
		ch := s[i]
		if ch == '\\' {
			target.WriteByte(ch)
			i++
			if i < len(s) {
				target.WriteByte(s[i])
				i++
			}
			continue
		}
		if jstext.IsSpace(rune(ch)) {
			break
		}
		if ch == '(' {
			depth++
		}
		if ch == ')' {
			if depth == 0 {
				break
			}
			depth--
		}
		target.WriteByte(ch)
		i++
	}
	for i < len(s) && s[i] != ')' {
		_, n := utf8.DecodeRuneInString(s[i:])
		i += n
	}
	if i >= len(s) {
		return markdownDest{target: "", end: 0}, false
	}
	return markdownDest{target: target.String(), end: i + 1}, true
}

func matchBareURL(line string, at int) (string, int, bool) {
	prefix := bareURLRE.FindString(line[at:])
	if prefix == "" {
		return "", at, false
	}
	depth, end := 0, 0
	for end < len(prefix) {
		switch prefix[end] {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				goto done
			}
			depth--
		}
		end++
	}
done:
	if depth != 0 {
		return "", at, false
	}
	uri := strings.TrimRight(prefix[:end], ".,;:!? ")
	if uri == "" {
		return "", at, false
	}
	return uri, at + len(uri), true
}

type linkHit struct {
	kind, value string
	end         int
}

func parseLink(line string, open int, refs map[string]struct{}) (linkHit, bool) {
	if line[open] != '[' {
		return linkHit{kind: "", value: "", end: 0}, false
	}
	bracketEnd := matchBracket(line, open)
	if bracketEnd < 0 {
		return linkHit{kind: "", value: "", end: 0}, false
	}
	text := line[open+1 : bracketEnd]
	after := byte(0)
	if bracketEnd+1 < len(line) {
		after = line[bracketEnd+1]
	}
	if after == '(' {
		dest, ok := parseInlineDest(line, bracketEnd+1)
		return linkHit{kind: "link-target", value: dest.target, end: dest.end}, ok
	}
	if after == '[' {
		if idClose := strings.IndexByte(line[bracketEnd+2:], ']'); idClose >= 0 {
			id := jstext.Trim(line[bracketEnd+2 : bracketEnd+2+idClose])
			if id == "" {
				id = text
			}
			return linkHit{kind: "link-ref", value: normalizeLabel(id), end: bracketEnd + 3 + idClose}, true
		}
	}
	norm := normalizeLabel(text)
	if _, ok := refs[norm]; ok {
		return linkHit{kind: "link-ref", value: norm, end: bracketEnd + 1}, true
	}
	return linkHit{kind: "", value: "", end: 0}, false
}

func refDefTarget(rest string) string {
	s := jstext.Trim(rest)
	if strings.HasPrefix(s, "<") {
		if e := strings.IndexByte(s, '>'); e >= 0 {
			return s[1:e]
		}
		return s
	}
	for i, r := range s {
		if jstext.IsSpace(r) {
			return s[:i]
		}
	}
	return s
}

func collectRefLabels(lines []string) map[string]struct{} {
	labels := make(map[string]struct{})
	start, inFence, fenceChar, fenceLen := 0, false, byte(0), 0
	if len(lines) > 0 && frontOpenRE.MatchString(lines[0]) {
		for i := 1; i < len(lines); i++ {
			if frontCloseRE.MatchString(lines[i]) {
				start = i + 1
				break
			}
		}
	}
	for _, line := range lines[start:] {
		if inFence {
			match := fenceCloseRE.FindStringSubmatch(line)
			if len(match) > 1 && match[1][0] == fenceChar && len(match[1]) >= fenceLen {
				inFence = false
			}
			continue
		}
		match := fenceOpenRE.FindStringSubmatch(line)
		if len(match) > 2 {
			bar, info := match[1], jstext.Trim(match[2])
			if bar[0] != '`' || !strings.Contains(info, "`") {
				inFence, fenceChar, fenceLen = true, bar[0], len(bar)
				continue
			}
		}
		if match := referenceDefRE.FindStringSubmatch(line); len(match) > 1 {
			labels[normalizeLabel(match[1])] = struct{}{}
		}
	}
	return labels
}

func fenceOpen(line string) []string  { return fenceOpenRE.FindStringSubmatch(line) }
func fenceClose(line string) []string { return fenceCloseRE.FindStringSubmatch(line) }

func markdownScan(text string) MarkdownScan {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	items := make([]MarkdownItem, 0)
	refs := collectRefLabels(lines)
	defect := ""
	inComment := false
	var commentBuf strings.Builder
	finishComment := func() {
		inner := jstext.Trim(commentBuf.String())
		if directiveRE.MatchString(inner) {
			items = append(items, MarkdownItem{Kind: "directive", Value: "<!-- " + inner + " -->"})
		}
		commentBuf.Reset()
		inComment = false
	}
	htmlTagAt := func(line string, from int) string {
		if !htmlTagStartRE.MatchString(line[from:]) {
			return ""
		}
		var quote byte
		for j := from + 1; j < len(line); j++ {
			ch := line[j]
			if quote != 0 {
				if ch == quote {
					quote = 0
				}
				continue
			}
			switch ch {
			case '"', '\'':
				quote = ch
			case '>':
				return line[from : j+1]
			}
		}
		return ""
	}
	inlineScan := func(line string) {
		for i := 0; i < len(line); {
			if inComment {
				end := strings.Index(line[i:], "-->")
				if end < 0 {
					commentBuf.WriteString(line[i:])
					commentBuf.WriteByte('\n')
					return
				}
				commentBuf.WriteString(line[i : i+end])
				finishComment()
				i += end + 3
				continue
			}
			if match := scriptStyleRE.FindStringSubmatch(line[i:]); len(match) > 0 {
				items = append(items, MarkdownItem{Kind: "raw-html", Value: match[0]})
				i += len(match[0])
				continue
			}
			if tag := htmlTagAt(line, i); tag != "" {
				items = append(items, MarkdownItem{Kind: "raw-html", Value: tag})
				i += len(tag)
				continue
			}
			if line[i] == '`' {
				run := backtickRun(line, i)
				codeEnd := findCodeClose(line, i+run, run)
				if codeEnd >= 0 {
					items = append(items, MarkdownItem{Kind: "code-span", Value: line[i+run : codeEnd]})
					i = codeEnd + run
				} else {
					i += run
				}
				continue
			}
			if line[i] == '<' {
				if strings.HasPrefix(line[i:], "<!--") {
					inComment = true
					i += 4
					continue
				}
				if match := autolinkURIRE.FindStringSubmatchIndex(line[i:]); match != nil {
					items = append(items, MarkdownItem{Kind: "autolink", Value: line[i+match[2] : i+match[3]]})
					i += match[1]
					continue
				}
				if match := autolinkEmailRE.FindStringSubmatchIndex(line[i:]); match != nil {
					items = append(items, MarkdownItem{Kind: "autolink", Value: line[i+match[2] : i+match[3]]})
					i += match[1]
					continue
				}
				i++
				continue
			}
			if line[i] == '[' || line[i] == '!' && i+1 < len(line) && line[i+1] == '[' {
				open := i
				if line[i] == '!' {
					open++
				}
				if hit, ok := parseLink(line, open, refs); ok {
					items = append(items, MarkdownItem{Kind: hit.kind, Value: hit.value})
					i = hit.end
					continue
				}
			}
			if uri, end, ok := matchBareURL(line, i); ok {
				items = append(items, MarkdownItem{Kind: "autolink", Value: uri})
				i = end
				continue
			}
			_, size := utf8.DecodeRuneInString(line[i:])
			i += size
		}
	}
	inFence, fenceChar, fenceLen, fenceInfo := false, byte(0), 0, ""
	fenceBody := make([]string, 0)
	prevBlank, listActive := true, false
	start := 0
	if len(lines) > 0 && frontOpenRE.MatchString(lines[0]) {
		for j := 1; j < len(lines); j++ {
			if frontCloseRE.MatchString(lines[j]) {
				items = append(items, MarkdownItem{Kind: "front-matter", Value: strings.Join(lines[1:j], "\n")})
				start = j + 1
				break
			}
		}
		if start == 0 {
			defect = "front matter opened with `---` and never closed"
		}
	}
	flushFence := func(closed bool) {
		if !closed {
			if defect == "" {
				defect = "a fenced code block opened and never closed"
			}
			return
		}
		fenceValue := string(fenceChar) + strconv.Itoa(fenceLen) + "[" + fenceInfo + "]\n" + strings.Join(fenceBody, "\n")
		items = append(items, MarkdownItem{Kind: "fenced-code", Value: fenceValue})
		inFence, fenceBody = false, make([]string, 0)
	}
	for i := start; i < len(lines); i++ {
		line := lines[i]
		if inFence {
			match := fenceClose(line)
			if len(match) > 1 && match[1][0] == fenceChar && len(match[1]) >= fenceLen {
				flushFence(true)
			} else {
				fenceBody = append(fenceBody, line)
			}
			prevBlank = false
			continue
		}
		if inComment {
			inlineScan(line)
			prevBlank = false
			continue
		}
		open := fenceOpen(line)
		if len(open) > 2 {
			bar, info := open[1], jstext.Trim(open[2])
			if bar[0] != '`' || !strings.Contains(info, "`") {
				inFence, fenceChar, fenceLen, fenceInfo, fenceBody, prevBlank =
					true, bar[0], len(bar), info, make([]string, 0), false
				continue
			}
		}
		if paragraphBlankRE.MatchString(line) {
			prevBlank = true
			continue
		}
		if match := headingRE.FindStringSubmatch(line); len(match) > 1 {
			raw := ""
			if len(match) > 2 {
				raw = match[2]
			}
			raw = jstext.Trim(headingEndHashRE.ReplaceAllString(raw, ""))
			items = append(items, MarkdownItem{Kind: "heading", Value: "H" + strconv.Itoa(len(match[1])) + " " + raw})
			prevBlank, listActive = false, false
			continue
		}
		var next string
		hasNext := i+1 < len(lines)
		if hasNext {
			next = lines[i+1]
		}
		if hasNext && isAlignmentRow(next) && (strings.Contains(next, "|") || strings.Contains(line, "|")) {
			items = append(
				items,
				MarkdownItem{Kind: "table-header", Value: jstext.Trim(line)},
				MarkdownItem{Kind: "table-delimiter", Value: jstext.Trim(next)},
			)
			i++
			prevBlank = false
			continue
		}
		blockquote := blockquoteRE.FindStringSubmatch(line)
		thematic := thematicRE.FindString(line)
		if hasNext && setextRE.MatchString(next) && isParagraph(line) {
			level := 2
			if strings.HasPrefix(jstext.Trim(next), "=") {
				level = 1
			}
			items = append(items, MarkdownItem{Kind: "heading", Value: "H" + strconv.Itoa(level) + " " + jstext.Trim(line)})
			i++
			prevBlank, listActive = false, false
			continue
		}
		if len(blockquote) > 1 {
			items = append(items, MarkdownItem{Kind: "block-marker", Value: "blockquote:" + blockquote[1]})
			inlineScan(line)
			prevBlank = false
			continue
		}
		if thematic != "" {
			items = append(items, MarkdownItem{Kind: "block-marker", Value: "thematic:" + jstext.Trim(thematic)})
			prevBlank, listActive = false, false
			continue
		}
		if rd := referenceDefRE.FindStringSubmatch(line); len(rd) > 2 {
			items = append(items, MarkdownItem{Kind: "ref-def", Value: normalizeLabel(rd[1]) + " -> " + refDefTarget(rd[2])})
			prevBlank = false
			continue
		}
		if listMarker := listMarkerRE.FindStringSubmatch(line); len(listMarker) > 1 {
			items = append(items, MarkdownItem{Kind: "block-marker", Value: "list:" + listMarker[1]})
			listActive = true
			inlineScan(line)
			prevBlank = false
			continue
		}
		if prevBlank && !listActive && indentWidth(line) >= 4 {
			body := []string{line}
			for i+1 < len(lines) {
				nx := lines[i+1]
				if paragraphBlankRE.MatchString(nx) {
					k := i + 1
					for k < len(lines) && paragraphBlankRE.MatchString(lines[k]) {
						k++
					}
					if k < len(lines) && indentWidth(lines[k]) >= 4 {
						for b := i + 1; b <= k; b++ {
							body = append(body, lines[b])
						}
						i = k
						continue
					}
					break
				}
				if indentWidth(nx) >= 4 {
					body = append(body, nx)
					i++
					continue
				}
				break
			}
			items = append(items, MarkdownItem{Kind: "indented-code", Value: strings.Join(body, "\n")})
			prevBlank = false
			continue
		}
		if indentWidth(line) == 0 {
			listActive = false
		}
		inlineScan(line)
		prevBlank = false
	}
	if inFence {
		flushFence(false)
	}
	if inComment && defect == "" {
		defect = "an HTML comment opened and never closed"
	}
	return MarkdownScan{Items: items, Defect: defect}
}

// ScanMarkdown returns the Markdown fingerprint and defect, if present.
func ScanMarkdown(text string) MarkdownScan { return markdownScan(text) }

// MarkdownFingerprint returns only the structural fingerprint.
func MarkdownFingerprint(text string) []MarkdownItem { return markdownScan(text).Items }

// CompareMarkdown compares the structural fingerprints of two documents.
func CompareMarkdown(before, after string) MarkdownVerdict {
	a, b := markdownScan(before), markdownScan(after)
	if a.Defect != "" {
		return VerdictMalformed{Side: "before", Reason: a.Defect}
	}
	if b.Defect != "" {
		return VerdictMalformed{Side: "after", Reason: b.Defect}
	}
	limit := max(len(a.Items), len(b.Items))
	for i := range limit {
		var x, y mo.Option[MarkdownItem]
		if i < len(a.Items) {
			x = mo.Some(a.Items[i])
		}
		if i < len(b.Items) {
			y = mo.Some(b.Items[i])
		}
		if x.IsAbsent() || y.IsAbsent() {
			return VerdictMismatch{Index: i, Before: x, After: y}
		}
		left, _ := x.Get()
		right, _ := y.Get()
		if left.Kind != right.Kind || left.Value != right.Value {
			return VerdictMismatch{Index: i, Before: x, After: y}
		}
	}
	return VerdictSame{}
}
