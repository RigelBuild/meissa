// SPDX-License-Identifier: MIT OR Apache-2.0

package markdown

import "testing"

// TestProseWordsImageAlt pins how an image's alt text counts toward prose. The alt is the visible text
// of its inline children, so plain words, a decoded entity and emphasized words each count once.
func TestProseWordsImageAlt(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
		want     int
	}{
		{name: "plain alt words count", markdown: "![alpha beta](x.png)", want: 2},
		// "&" has no letter or digit, so it is not a word; "a" and "b" are.
		{name: "entity in alt is decoded", markdown: "![a &amp; b](x.png)", want: 2},
		{name: "emphasis in alt keeps its words", markdown: "![*alpha* beta](x.png)", want: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProseWords(tc.markdown, false); got != tc.want {
				t.Fatalf("ProseWords(%q) = %d, want %d", tc.markdown, got, tc.want)
			}
		})
	}
}
