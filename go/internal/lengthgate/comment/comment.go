// SPDX-License-Identifier: MIT OR Apache-2.0

// Package comment reads LENGTH markers from source comments and pairs head lines with base lines.
package comment

import (
	"strings"

	"github.com/RigelBuild/meissa/go/internal/jstext"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/text"
)

// Ceiling is the longest comment block a change may grow to without a marker.
const Ceiling = 4

// MarkerResult reports what a source comment's first line says about LENGTH.
type MarkerResult struct {
	Found  bool   // false means the line holds no marker
	Reason string // set when Found and Error is ""
	Error  string // set when Found and the marker is malformed
}

// Marker reads a LENGTH marker from the first line of a comment block.
func Marker(firstLine string) MarkerResult {
	rest, ok := commentPrefixRest(firstLine)
	if !ok {
		return MarkerResult{Found: false, Reason: "", Error: ""}
	}
	afterColon, ok := cutMarkerKeyword(jstext.Trim(rest))
	if !ok {
		return MarkerResult{Found: false, Reason: "", Error: ""}
	}
	content := text.TrimCommentClose(afterColon)
	if problem := text.InvalidReason(content); problem != "" {
		return MarkerResult{Found: true, Reason: "", Error: problem}
	}
	return MarkerResult{Found: true, Reason: content, Error: ""}
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

// cutMarkerKeyword removes "LENGTH" and the colon after it, and reports false
// when the content does not start that way.
func cutMarkerKeyword(content string) (string, bool) {
	rest, ok := strings.CutPrefix(content, text.Marker)
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
