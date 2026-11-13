// SPDX-License-Identifier: MIT OR Apache-2.0

package markdown

import (
	"strconv"
	"strings"
	"testing"
)

// words returns count placeholder words, each with a letter and a digit.
func words(count int) string {
	parts := make([]string, 0, count)
	for i := range count {
		parts = append(parts, "word"+strconv.Itoa(i+1))
	}
	return strings.Join(parts, " ")
}

func TestProseWordsCharacterReferences(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
		want     int
	}{
		// The TypeScript uses mdast, which decodes character references before counting.
		{"named dash entity separates words", "foo &mdash; bar", 2},
		{"non-breaking space entity splits words", "a&nbsp;b", 2},
		{"ampersand entity is not a word", "one &amp; two", 2},
		{"arrow entity is not a word", "left &rarr; right", 2},
		{"numeric reference decodes", "x&#32;y", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProseWords(tc.markdown, false); got != tc.want {
				t.Fatalf("ProseWords(%q) = %d, want %d", tc.markdown, got, tc.want)
			}
		})
	}
}

func TestProseWordsBOMAndLabels(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
		want     int
	}{
		{"a BOM does not hide link labels", "\uFEFF[alpha beta](https://x.io) gamma", 3},
		{"custom style-* elements keep their text", "a <style-guide>alpha beta</style-guide> b", 4},
		{"multiline style-* element keeps its text", "<style-guide>\nalpha beta\n</style-guide>\n\na b", 4},
		{"inline code counts as prose", "Run `foo` and `bar` now.", 5},
		{"inline tags separate words", "alpha<br>beta", 2},
		{"block HTML keeps its prose", "<div>\n" + words(5) + "\n</div>", 5},
		{"HTML comment text never counts", "<!-- note --> visible words here", 3},
		{"HTML comment with a > inside stays hidden", "alpha <!-- a > hidden words --> beta", 2},
		{"script body is not prose", "<script>\nconst a = b; let c = d;\n</script>", 0},
		{"bare angle-bracket URL is a target", "see <https://example.com/a/b> now", 2},
		{"inline script body is not prose", "alpha <script>let a = b;</script> beta", 2},
		{"www autolink is not prose", "see www.example.com/a now", 2},
		{"email autolink is not prose", "mail <foo@example.com> now", 2},
		{"bare email is prose", "mail foo@example.com now", 2},
		{"URL label counts", "[https://example.com/path](https://example.com/path)", 1},
		{"Errata in a list does not drop later prose", "- ## Errata\n\n" + words(3), 4},
		{"punctuation tokens count only with a letter or digit", "foo-bar/baz x.y -- ->", 2},
		{"setext heading with pipes is not a table", "a | b\n---\nmore | words", 4},
		{"footnote definition text is counted", "[^1]: some note text", 3},
		{"four-space fenced code under a list item is excluded", "10. Step\n\n    ```ts\n    " + words(40) + "\n    ```", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProseWords(tc.markdown, false); got != tc.want {
				t.Fatalf("ProseWords = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestProseWordsFrontMatterAndFences(t *testing.T) {
	markdown := strings.Join([]string{
		"---",
		"title: " + words(20),
		"---",
		"Counted " + words(2) + " [label](https://example.test/target) and [ref][destination].",
		"",
		"[destination]: https://example.test/reference-target",
		"",
		"```ts",
		words(40),
		"```",
		"<!--",
		words(30),
		"-->",
		"| Heading | More |",
		"| --- | --- |",
		"| " + words(20) + " | " + words(20) + " |",
		"| Single column |",
		"| --- |",
		"| hidden words |",
		"## Errata",
		words(50),
	}, "\n")
	if got := ProseWords(markdown, true); got != 6 {
		t.Fatalf("ProseWords = %d, want 6", got)
	}
}

func TestProseWordsInlineMarkerMentionCounts(t *testing.T) {
	markdown := "HTML comments open with `<!--` in markdown.\n\n## Body\n\n" + words(3000)
	got := ProseWords(markdown, false)
	if got < 3000 || got >= 3010 {
		t.Fatalf("ProseWords = %d, want in [3000, 3010)", got)
	}
}

// An unclosed front matter fence is not front matter. micromark parses the text as markdown, so the
// words stay counted. Before the fix the whole document vanished and an over-target doc passed.
func TestProseWordsUnclosedFenceIsNotFrontMatter(t *testing.T) {
	markdown := "---\n" + words(40)
	if got := ProseWords(markdown, false); got != 40 {
		t.Fatalf("ProseWords = %d, want 40", got)
	}
}

// A fence with trailing spaces opens front matter, matching micromark, so its title is not prose.
func TestProseWordsTrailingSpaceFenceIsFrontMatter(t *testing.T) {
	markdown := "---  \ntitle: " + words(20) + "\n---  \nCounted " + words(2)
	if got := ProseWords(markdown, false); got != 3 {
		t.Fatalf("ProseWords = %d, want 3", got)
	}
}

// Errata drops a footnote by its own source line. goldmark moves every referenced footnote to the
// end of the document, so the drop must follow the definition's line, not its tree position.
func TestProseWordsErrataFootnotes(t *testing.T) {
	before := "# Record\n\nBody text[^a] here.\n\n[^a]: " + words(3) + "\n\n## Errata\n\n" + words(5)
	if got := ProseWords(before, true); got != 7 {
		t.Fatalf("footnote before Errata: ProseWords = %d, want 7", got)
	}
	inside := "# Record\n\nBody text[^a] here.\n\n## Errata\n\n[^a]: " + words(3) + "\n"
	if got := ProseWords(inside, true); got != 4 {
		t.Fatalf("footnote defined inside Errata: ProseWords = %d, want 4", got)
	}
}
