// Package jstext contains Go helpers that preserve JavaScript text semantics.
// SPDX-License-Identifier: MIT OR Apache-2.0
package jstext

import (
	"strings"
	"unicode"
)

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
