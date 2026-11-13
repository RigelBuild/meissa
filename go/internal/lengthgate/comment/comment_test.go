// SPDX-License-Identifier: MIT OR Apache-2.0

package comment

import (
	"strconv"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/lengthgate/text"
)

// TestFixedPolicy pins the constants the design record fixes, as the TypeScript suite does.
func TestFixedPolicy(t *testing.T) {
	if Ceiling != 4 || text.Marker != "LENGTH" || text.MinReasonWords != 3 {
		t.Fatalf("policy = %d %q %d, want 4 LENGTH 3", Ceiling, text.Marker, text.MinReasonWords)
	}
}

// TestCommentMarker ports the TypeScript source comment marker cases.
func TestCommentMarker(t *testing.T) {
	t.Run("parses a marker reason", func(t *testing.T) {
		got := Marker("// LENGTH: bit layout of the header")
		if !got.Found || got.Error != "" || got.Reason != "bit layout of the header" {
			t.Fatalf("Marker = %+v, want reason %q", got, "bit layout of the header")
		}
	})

	t.Run("a short reason is an error that names the three-word minimum", func(t *testing.T) {
		got := Marker("# LENGTH: why")
		if !got.Found || !strings.Contains(got.Error, "3 words") {
			t.Fatalf("Marker = %+v, want an error containing %q", got, "3 words")
		}
	})

	t.Run("an ordinary comment is not a marker", func(t *testing.T) {
		if got := Marker("// ordinary note"); got.Found {
			t.Fatalf("Marker = %+v, want not found", got)
		}
	})

	t.Run("a LENGTH prefix without a colon is ordinary comment text", func(t *testing.T) {
		if got := Marker("// LENGTH must stay a power of two."); got.Found {
			t.Fatalf("Marker = %+v, want not found", got)
		}
	})
}

// TestLineMapPairing ports the TypeScript line pairing cases.
func TestLineMapPairing(t *testing.T) {
	t.Run("pairs trimmed equal lines using the diff alignment", func(t *testing.T) {
		lineOf := LineMap("first\n  stable\nlast", "new\n\tstable\nlast")
		if _, ok := lineOf(1); ok {
			t.Fatalf("line 1 paired, want unpaired")
		}
		for _, head := range []int{2, 3} {
			if got, ok := lineOf(head); !ok || got != head {
				t.Fatalf("line %d = (%d, %t), want (%d, true)", head, got, ok, head)
			}
		}
	})

	t.Run("uniquely pairs moved lines the diff left unmatched", func(t *testing.T) {
		before := joinComments(10, "A") + "\n\n" + joinComments(6, "B")
		after := joinComments(6, "B") + "\n\n" + joinComments(11, "A")
		lineOf := LineMap(before, after)
		if got, ok := lineOf(1); !ok || got != 12 {
			t.Fatalf("head 1 = (%d, %t), want (12, true)", got, ok)
		}
		if got, ok := lineOf(8); !ok || got != 1 {
			t.Fatalf("head 8 = (%d, %t), want (1, true)", got, ok)
		}
	})
}

// joinComments returns count line comments labelled label, numbered from 1.
func joinComments(count int, label string) string {
	lines := make([]string, 0, count)
	for i := range count {
		lines = append(lines, "// "+label+" "+strconv.Itoa(i+1))
	}
	return strings.Join(lines, "\n")
}
