// SPDX-License-Identifier: BSD-3-Clause

package linediff

import (
	"slices"
	"testing"
)

// Golden cases are the output of jsdiff 9.0.0 diffArrays for the same inputs. Tie-breaks matter:
// length-gate pairs comment lines by this alignment, so a different tie changes baseLines.
func TestLinesMatchesJSDiffGoldens(t *testing.T) {
	cases := []struct {
		name          string
		before, after []string
		want          []Op
	}{
		{name: "empty both", before: nil, after: nil, want: nil},
		{
			name:   "identical",
			before: []string{"a", "b", "c"},
			after:  []string{"a", "b", "c"},
			want:   []Op{{Kind: '=', Count: 3}},
		},
		{
			name:   "pure insert into empty",
			before: nil,
			after:  []string{"a", "b"},
			want:   []Op{{Kind: '+', Count: 2}},
		},
		{
			name:   "pure delete to empty",
			before: []string{"a", "b"},
			after:  nil,
			want:   []Op{{Kind: '-', Count: 2}},
		},
		{
			name:   "pure insert in middle",
			before: []string{"a", "c"},
			after:  []string{"a", "b", "c"},
			want:   []Op{{Kind: '=', Count: 1}, {Kind: '+', Count: 1}, {Kind: '=', Count: 1}},
		},
		{
			name:   "pure delete in middle",
			before: []string{"a", "b", "c"},
			after:  []string{"a", "c"},
			want:   []Op{{Kind: '=', Count: 1}, {Kind: '-', Count: 1}, {Kind: '=', Count: 1}},
		},
		{
			name:   "interleaved",
			before: []string{"a", "b", "c", "d"},
			after:  []string{"a", "x", "c", "y"},
			want: []Op{
				{Kind: '=', Count: 1}, {Kind: '-', Count: 1}, {Kind: '+', Count: 1},
				{Kind: '=', Count: 1}, {Kind: '-', Count: 1}, {Kind: '+', Count: 1},
			},
		},
		{
			name:   "repeated-line tie",
			before: []string{"a", "a", "b"},
			after:  []string{"a", "b", "a"},
			want: []Op{
				{Kind: '=', Count: 1}, {Kind: '-', Count: 1}, {Kind: '=', Count: 1}, {Kind: '+', Count: 1},
			},
		},
		{
			name:   "moved block",
			before: []string{"A1", "A2", "B1"},
			after:  []string{"B1", "A1", "A2"},
			want:   []Op{{Kind: '+', Count: 1}, {Kind: '=', Count: 2}, {Kind: '-', Count: 1}},
		},
		{
			name:   "all replaced",
			before: []string{"x", "y"},
			after:  []string{"p", "q", "r"},
			want:   []Op{{Kind: '-', Count: 2}, {Kind: '+', Count: 3}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Lines(tc.before, tc.after)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("Lines = %#v, want %#v", got, tc.want)
			}
		})
	}
}
