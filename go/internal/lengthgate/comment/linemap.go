// SPDX-License-Identifier: MIT OR Apache-2.0

package comment

import (
	"strings"

	"github.com/RigelBuild/meissa/go/internal/jstext"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/text"
	"github.com/RigelBuild/meissa/go/internal/linediff"
)

// fallbackKey strips leading comment tokens so a moved line pairs on its text.
// It returns "" when the stripped text holds no letter or digit.
func fallbackKey(line string) string {
	content := stripTokens(line)
	if text.HasLetterOrDigit(content) {
		return content
	}
	return ""
}

// stripTokens mirrors the TS fallback pattern: leading space, then one
// or more tokens each followed by space. A line with no leading token is unchanged.
func stripTokens(line string) string {
	rest := jstext.TrimLeft(line)
	stripped := false
	for {
		next, ok := cutFallbackToken(rest)
		if !ok {
			break
		}
		stripped = true
		rest = jstext.TrimLeft(next)
	}
	if !stripped {
		return line
	}
	return rest
}

// cutFallbackToken removes one leading token, trying the alternatives in TS order.
func cutFallbackToken(s string) (string, bool) {
	for _, token := range [...]string{"//", "#", "*", "--", ";", "/*", "*/"} {
		if rest, ok := strings.CutPrefix(s, token); ok {
			return rest, true
		}
	}
	return "", false
}

// splitLines normalizes line endings, splits on newlines and trims each line.
func splitLines(source string) []string {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	source = strings.ReplaceAll(source, "\r", "\n")
	lines := strings.Split(source, "\n")
	for index, line := range lines {
		lines[index] = jstext.Trim(line)
	}
	return lines
}

// keyCount is how often a fallback key occurs among unpaired lines, and the last line it occurs on.
type keyCount struct {
	count int
	line  int
}

// unpairedKeys counts fallback keys over the lines the diff did not pair.
func unpairedKeys(lines []string, mapped map[int]bool) map[string]keyCount {
	counts := map[string]keyCount{}
	for index, line := range lines {
		number := index + 1
		if mapped[number] || line == "" {
			continue
		}
		key := fallbackKey(line)
		if key == "" {
			continue
		}
		entry := counts[key]
		counts[key] = keyCount{count: entry.count + 1, line: number}
	}
	return counts
}

// LineMap pairs head lines with base lines: the diff alignment first, then
// unique fallback keys. The returned function reports false when a line is unpaired.
func LineMap(before, after string) func(headLine int) (int, bool) {
	beforeLines := splitLines(before)
	afterLines := splitLines(after)
	mapping := map[int]int{}
	mappedBefore := map[int]bool{}
	mappedAfter := map[int]bool{}
	beforeLine, afterLine := 1, 1
	for _, op := range linediff.Lines(beforeLines, afterLines) {
		switch op.Kind {
		case '+':
			afterLine += op.Count
		case '-':
			beforeLine += op.Count
		default:
			for offset := range op.Count {
				mapping[afterLine+offset] = beforeLine + offset
				mappedBefore[beforeLine+offset] = true
				mappedAfter[afterLine+offset] = true
			}
			beforeLine += op.Count
			afterLine += op.Count
		}
	}
	beforeCounts := unpairedKeys(beforeLines, mappedBefore)
	afterCounts := unpairedKeys(afterLines, mappedAfter)
	for key, base := range beforeCounts {
		head, ok := afterCounts[key]
		if base.count == 1 && ok && head.count == 1 {
			mapping[head.line] = base.line
		}
	}
	return func(headLine int) (int, bool) {
		line, ok := mapping[headLine]
		return line, ok
	}
}
