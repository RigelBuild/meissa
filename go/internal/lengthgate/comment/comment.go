// SPDX-License-Identifier: MIT OR Apache-2.0

// Package comment reads LENGTH markers from source comments and pairs head lines with base lines.
package comment

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/RigelBuild/meissa/go/internal/jstext"
)

// Ceiling is the longest comment block a change may grow to without a marker.
const Ceiling = 4

const (
	// MarkerKeyword is the word that opens a LENGTH marker.
	MarkerKeyword = "LENGTH"
	// MinReasonWords is the fewest words a marker reason may hold.
	MinReasonWords = 3
)

// InvalidReason reports why a marker reason is too short.
func InvalidReason(reason string) string {
	if WordsInReason(reason) < MinReasonWords {
		return fmt.Sprintf("reason must contain at least %d words", MinReasonWords)
	}
	return ""
}

// IsWordByte reports whether c is an ASCII letter, digit or underscore.
func IsWordByte(c byte) bool {
	return c == '_' || ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// MarkerResult reports whether a comment has a valid or malformed LENGTH marker.
type MarkerResult struct {
	Found bool
	Error string
}

// Marker reads a LENGTH marker from the first line of a comment block.
func Marker(firstLine string) MarkerResult {
	rest, ok := commentPrefixRest(firstLine)
	if !ok {
		return MarkerResult{Found: false, Error: ""}
	}
	afterColon, ok := cutMarkerKeyword(jstext.Trim(rest))
	if !ok {
		return MarkerResult{Found: false, Error: ""}
	}
	content := TrimCommentClose(afterColon)
	if problem := InvalidReason(content); problem != "" {
		return MarkerResult{Found: true, Error: problem}
	}
	return MarkerResult{Found: true, Error: ""}
}

// WordsInReason counts reason words that hold a letter or digit.
func WordsInReason(reason string) int {
	count := 0
	for _, word := range jstext.Fields(jstext.Trim(reason)) {
		if HasLetterOrDigit(word) {
			count++
		}
	}
	return count
}

// HasLetterOrDigit reports whether value holds a Unicode letter or number.
func HasLetterOrDigit(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

// TrimCommentClose removes a trailing comment closer and the surrounding spaces.
func TrimCommentClose(value string) string {
	value = jstext.TrimRight(value)
	for _, suffix := range [...]string{"*/", "-->"} {
		if cut, ok := strings.CutSuffix(value, suffix); ok {
			value = cut
			break
		}
	}
	return jstext.Trim(value)
}

// commentPrefixRest drops one comment opener and the spaces after it.
// It reports false when the line opens no comment.
func commentPrefixRest(line string) (string, bool) {
	rest := jstext.TrimLeft(line)
	switch {
	case strings.HasPrefix(rest, "//"):
		rest = trimRun(rest, '/')
	case strings.HasPrefix(rest, "#"):
		rest = trimRun(rest, '#')
	case strings.HasPrefix(rest, "/*"):
		rest = trimRun(rest[1:], '*')
	case strings.HasPrefix(rest, "<!--"):
		rest = rest[len("<!--"):]
	case strings.HasPrefix(rest, "--"):
		rest = rest[len("--"):]
	case strings.HasPrefix(rest, ";"), strings.HasPrefix(rest, "%"):
		rest = rest[1:]
	default:
		return "", false
	}
	return jstext.TrimLeft(rest), true
}

// cutMarkerKeyword removes LENGTH and its colon from the marker content.
func cutMarkerKeyword(content string) (string, bool) {
	rest, ok := strings.CutPrefix(content, MarkerKeyword)
	if !ok {
		return "", false
	}
	return strings.CutPrefix(jstext.TrimLeft(rest), ":")
}

// trimRun drops the leading run of byte c from s.
func trimRun(s string, c byte) string {
	for len(s) > 0 && s[0] == c {
		s = s[1:]
	}
	return s
}
