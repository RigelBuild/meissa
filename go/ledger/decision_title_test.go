// SPDX-License-Identifier: MIT OR Apache-2.0

package ledger

import (
	"errors"
	"strings"
	"testing"
)

// withTitle appends an optional title line after the record key.
func withTitle(title string) string {
	return strings.Replace(good, "\n---\n\nOptional", "\ntitle: "+title+"\n---\n\nOptional", 1)
}

// withTitleQuoted appends a double-quoted title line after the record key.
func withTitleQuoted(title string) string {
	return withTitle(`"` + title + `"`)
}

func TestParseDecisionFileTitle(t *testing.T) {
	t.Run("an absent title is the empty string", func(t *testing.T) {
		got := parseForTest(t, []byte(good), testPath)
		if got.Title != "" {
			t.Fatalf("Title = %q, want empty", got.Title)
		}
	})
	t.Run("a quoted title of 8 characters parses", func(t *testing.T) {
		got := parseForTest(t, []byte(withTitleQuoted("Eightchr")), testPath)
		if got.Title != "Eightchr" {
			t.Fatalf("Title = %q, want Eightchr", got.Title)
		}
	})
	t.Run("an uppercase token without an issue-key suffix parses", func(t *testing.T) {
		title := "Use HTTP2 for transport"
		got := parseForTest(t, []byte(withTitleQuoted(title)), testPath)
		if got.Title != title {
			t.Fatalf("Title = %q, want %q", got.Title, title)
		}
	})
	t.Run("a title of 60 characters parses", func(t *testing.T) {
		title := strings.Repeat("a", 60)
		got := parseForTest(t, []byte(withTitleQuoted(title)), testPath)
		if got.Title != title {
			t.Fatalf("Title length = %d, want 60", len(got.Title))
		}
	})
	t.Run("a title of 61 characters is rejected", func(t *testing.T) {
		_, err := ParseDecisionFile(testPath, []byte(withTitleQuoted(strings.Repeat("a", 61))))
		var malformed *MalformedDecision
		if !errors.As(err, &malformed) || malformed.Reason != "`title` must be 8 to 60 characters" {
			t.Fatalf("ParseDecisionFile() error = %v, want 61-character title rejection", err)
		}
	})
	for _, test := range []struct {
		name   string
		text   string
		reason string
	}{
		{
			name:   "a plain unquoted title",
			text:   withTitle("Compaction reuses the model"),
			reason: "`title` must be a one-line double-quoted string",
		},
		{
			name:   "a title of 7 characters",
			text:   withTitleQuoted("abcdefg"),
			reason: "`title` must be 8 to 60 characters",
		},
		{
			name:   "a title that holds a decision id",
			text:   withTitleQuoted("Supersedes DL-300 on compaction"),
			reason: "`title` must not hold an id, issue key or date",
		},
		{
			name:   "a title that holds an issue key",
			text:   withTitleQuoted("Compaction work for RIG-6807"),
			reason: "`title` must not hold an id, issue key or date",
		},
		{
			name:   "an explicit empty title",
			text:   withTitleQuoted(""),
			reason: "`title` must be 8 to 60 characters",
		},
		{
			name:   "a title that holds a non-RIG issue key",
			text:   withTitleQuoted("Adopt ENG-12 rollout plan"),
			reason: "`title` must not hold an id, issue key or date",
		},
		{
			name:   "a title that holds a date",
			text:   withTitleQuoted("Compaction decided 2026-10-30"),
			reason: "`title` must not hold an id, issue key or date",
		},
		{
			name:   "a title that ends with a period",
			text:   withTitleQuoted("Compaction reuses the model."),
			reason: "`title` must not end with a period",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseDecisionFile(testPath, []byte(test.text))
			var malformed *MalformedDecision
			if !errors.As(err, &malformed) || malformed.Reason != test.reason {
				t.Fatalf("ParseDecisionFile() error = %v, want reason %q", err, test.reason)
			}
		})
	}
	t.Run("a four-key file with the new count message", func(t *testing.T) {
		text := strings.Replace(good, "status: \"Active (Matt, 2026-10-07)\"\n", "", 1)
		_, err := ParseDecisionFile(testPath, []byte(text))
		var malformed *MalformedDecision
		want := "front matter must hold id, decision, status, record and optional title"
		if !errors.As(err, &malformed) || malformed.Reason != want {
			t.Fatalf("ParseDecisionFile() error = %v, want reason %q", err, want)
		}
	})
}

// TestRecordSlug pins the slug rules: design.md takes its parent directory,
// any other base name is lowercased without .md, and README.md gives readme.
func TestRecordSlug(t *testing.T) {
	cases := []struct {
		recordPath string
		want       string
	}{
		{"docs/designs/platform/oss-forks/fork-releases/design.md", "fork-releases"},
		{"docs/designs/product/rigel/README.md", "readme"},
		{"docs/designs/ui/Public-Docs.md", "public-docs"},
	}
	for _, test := range cases {
		if got := RecordSlug(test.recordPath); got != test.want {
			t.Fatalf("RecordSlug(%q) = %q, want %q", test.recordPath, got, test.want)
		}
	}
}
