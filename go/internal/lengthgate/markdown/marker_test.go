// SPDX-License-Identifier: MIT OR Apache-2.0

package markdown

import "testing"

const (
	posErr    = "doc marker must be after front matter, before the first ## heading, and name an existing section"
	oneLine   = "doc LENGTH marker must be one line"
	formatErr = "doc marker must be one HTML comment line: <!-- LENGTH §Section: reason -->"
	onlyOne   = "only one doc LENGTH marker is allowed"
)

func TestMarkerAccepted(t *testing.T) {
	doc := "# Title\n\n<!-- LENGTH §Body: the reason is long enough here -->\n\n## Body\n\ntext\n"
	got := Marker(doc)
	if !got.Found || got.Error != "" {
		t.Fatalf("Marker = %+v, want found and no error", got)
	}
	if got.Section != "Body" || got.Reason != "the reason is long enough here" {
		t.Fatalf("Section/Reason = %q/%q", got.Section, got.Reason)
	}
}

func TestMarkerNone(t *testing.T) {
	if got := Marker("# Title\n\n## Body\n\ntext\n"); got.Found {
		t.Fatalf("Marker = %+v, want not found", got)
	}
}

// The TypeScript checks the marker's error cases in this order. Each case isolates one check.
func TestMarkerErrorOrder(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "two markers report the multiple-marker error first",
			doc:  "<!-- LENGTH §A: reason words here -->\n\n<!-- LENGTH §A: more words here -->\n\n## A\n",
			want: onlyOne,
		},
		{
			name: "marker after the first heading is a position error",
			doc:  "## A\n\n<!-- LENGTH §A: reason words here -->\n",
			want: posErr,
		},
		{
			name: "marker with no heading at all is a position error",
			doc:  "# Title\n\n<!-- LENGTH §A: reason words here -->\n",
			want: posErr,
		},
		{
			name: "multi-line marker is a one-line error, not a format error",
			doc:  "# T\n\n<!--\nLENGTH §A: reason words here\n-->\n\n## A\n",
			want: oneLine,
		},
		{
			name: "missing space after LENGTH is a format error",
			doc:  "# T\n\n<!-- LENGTHX §A: reason words here -->\n\n## A\n",
			want: "",
		},
		{
			name: "no colon in the body is a format error",
			doc:  "# T\n\n<!-- LENGTH §A reason words here -->\n\n## A\n",
			want: formatErr,
		},
		{
			name: "section that names no heading is a section error",
			doc:  "# T\n\n<!-- LENGTH §Missing: reason words here -->\n\n## A\n",
			want: "doc marker section §Missing does not match an existing ## heading",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Marker(tc.doc)
			if tc.want == "" {
				// "LENGTHX" is not a marker at all, so nothing is found and nothing errors.
				if got.Found {
					t.Fatalf("Marker = %+v, want not found", got)
				}
				return
			}
			if got.Error != tc.want {
				t.Fatalf("Error = %q, want %q", got.Error, tc.want)
			}
		})
	}
}

// The body needs whitespace after LENGTH and a colon that is not at index zero, matching the TypeScript
// pattern `^<!--\s*LENGTH\s+§([^:]+:.*?)\s*-->$`.
func TestMarkerBodyGrammar(t *testing.T) {
	cases := []struct {
		name  string
		value string
		ok    bool
	}{
		{"space-separated LENGTH and section", "<!-- LENGTH §A: reason -->", true},
		{"colon at index zero is malformed", "<!-- LENGTH §: reason -->", false},
		{"no whitespace between LENGTH and the section sign is malformed", "<!--LENGTH§A: reason-->", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := markerBody(tc.value)
			if ok != tc.ok {
				t.Fatalf("markerBody(%q) ok = %v, want %v", tc.value, ok, tc.ok)
			}
		})
	}
}
