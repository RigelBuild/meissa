// Package jstext contains Go helpers that preserve JavaScript text semantics.
// SPDX-License-Identifier: MIT OR Apache-2.0
package jstext

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// IsSpace reports whether r is ECMAScript whitespace.
func IsSpace(r rune) bool {
	if r >= '\t' && r <= '\r' || r == ' ' || r == '\u00a0' || r == '\ufeff' || r == '\u2028' || r == '\u2029' {
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

// Fields splits s around runs of ECMAScript whitespace.
func Fields(s string) []string {
	fields := make([]string, 0)
	start := -1
	for i, r := range s {
		if IsSpace(r) {
			if start >= 0 {
				fields = append(fields, s[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		fields = append(fields, s[start:])
	}
	return fields
}

// Trim removes ECMAScript whitespace from both ends of s.
func Trim(s string) string {
	start := 0
	for start < len(s) {
		r, size := utf8.DecodeRuneInString(s[start:])
		if !IsSpace(r) {
			break
		}
		start += size
	}
	end := len(s)
	for end > start {
		r, size := utf8.DecodeLastRuneInString(s[:end])
		if !IsSpace(r) {
			break
		}
		end -= size
	}
	return s[start:end]
}

// ToLower applies JavaScript toLowerCase semantics for Go's differing mappings.
func ToLower(text string) string {
	if !strings.ContainsAny(text, "Σİ") {
		return strings.ToLower(text)
	}

	runes := []rune(text)
	var lowered strings.Builder
	lowered.Grow(len(text))
	for i, r := range runes {
		switch r {
		case '\u0130':
			lowered.WriteString("i\u0307")
		case '\u03a3':
			if isFinalSigma(runes, i) {
				lowered.WriteRune('\u03c2')
			} else {
				lowered.WriteRune('\u03c3')
			}
		default:
			lowered.WriteRune(unicode.ToLower(r))
		}
	}
	return lowered.String()
}

func isFinalSigma(runes []rune, index int) bool {
	precededByCased := false
	for i := index - 1; i >= 0; i-- {
		if isCaseIgnorable(runes[i]) {
			continue
		}
		precededByCased = isCased(runes[i])
		break
	}
	if !precededByCased {
		return false
	}

	for _, r := range runes[index+1:] {
		if isCaseIgnorable(r) {
			continue
		}
		return !isCased(r)
	}
	return true
}

func isCased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r)
}

func isCaseIgnorable(r rune) bool {
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk) {
		return true
	}
	switch r {
	case '\'', ':', '.', '\u00b7', '\u0387', '\u055f', '\u05f4',
		'\u2018', '\u2019', '\u2024', '\u2027', '\ufe13', '\ufe52',
		'\ufe55', '\uff07', '\uff0e', '\uff1a':
		return true
	default:
		return false
	}
}
