package jstext

import "unicode/utf8"

// TrimLeft removes ECMAScript whitespace from the start of s.
func TrimLeft(s string) string {
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if !IsSpace(r) {
			return s
		}
		s = s[size:]
	}
	return s
}

// TrimRight removes ECMAScript whitespace from the end of s.
func TrimRight(s string) string {
	for len(s) > 0 {
		r, size := utf8.DecodeLastRuneInString(s)
		if !IsSpace(r) {
			return s
		}
		s = s[:len(s)-size]
	}
	return s
}
