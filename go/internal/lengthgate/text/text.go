// SPDX-License-Identifier: MIT OR Apache-2.0

// Package text holds the LENGTH marker rules that both comment and document markers share.
package text

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/RigelBuild/meissa/go/internal/jstext"
)

const (
	// Marker is the keyword that opens a LENGTH marker in a comment or document.
	Marker = "LENGTH"
	// MinReasonWords is the fewest words a marker reason must hold.
	MinReasonWords = 3
)

// InvalidReason returns the problem with a marker reason, or "" when it is valid.
func InvalidReason(reason string) string {
	if WordsInReason(reason) < MinReasonWords {
		return fmt.Sprintf("reason must contain at least %d words", MinReasonWords)
	}
	return ""
}

// WordsInReason counts the words of a reason that hold a letter or a digit.
func WordsInReason(reason string) int {
	count := 0
	for _, word := range jstext.Fields(jstext.Trim(reason)) {
		if HasLetterOrDigit(word) {
			count++
		}
	}
	return count
}

// HasLetterOrDigit reports whether text holds a Unicode letter (L) or number (N).
func HasLetterOrDigit(text string) bool {
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

// IsWordByte reports whether c is an ASCII letter, digit or underscore.
func IsWordByte(c byte) bool {
	return c == '_' || ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// TrimCommentClose removes a trailing "*/" or "-->" and the spaces around it.
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
