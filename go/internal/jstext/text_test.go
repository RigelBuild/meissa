package jstext

import (
	"reflect"
	"testing"
)

func TestIsSpace(t *testing.T) {
	for _, test := range []struct {
		name string
		r    rune
		want bool
	}{
		{name: "ASCII space", r: ' ', want: true},
		{name: "tab", r: '\t', want: true},
		{name: "line feed", r: '\n', want: true},
		{name: "carriage return", r: '\r', want: true},
		{name: "vertical tab", r: '\v', want: true},
		{name: "form feed", r: '\f', want: true},
		{name: "no-break space", r: '\u00a0', want: true},
		{name: "byte order mark", r: '\ufeff', want: true},
		{name: "line separator", r: '\u2028', want: true},
		{name: "paragraph separator", r: '\u2029', want: true},
		{name: "Zs separator", r: '\u3000', want: true},
		{name: "next line is not ECMAScript whitespace", r: '\u0085', want: false},
		{name: "letter is not whitespace", r: 'a', want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsSpace(test.r); got != test.want {
				t.Errorf("IsSpace(%U) = %t, want %t", test.r, got, test.want)
			}
		})
	}
}

func TestFields(t *testing.T) {
	for _, test := range []struct {
		name string
		text string
		want []string
	}{
		{name: "ordinary fields", text: "one two\tthree", want: []string{"one", "two", "three"}},
		{name: "ECMAScript whitespace", text: "\ufeffone\u00a0two\u2028three\u3000", want: []string{"one", "two", "three"}},
		{name: "empty string", text: "", want: []string{}},
		{name: "only whitespace", text: " \t\ufeff", want: []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := Fields(test.text); !reflect.DeepEqual(got, test.want) {
				t.Errorf("Fields(%q) = %#v, want %#v", test.text, got, test.want)
			}
		})
	}
}

func TestTrim(t *testing.T) {
	for _, test := range []struct {
		name string
		text string
		want string
	}{
		{name: "ordinary whitespace", text: " \tvalue\r\n", want: "value"},
		{name: "ECMAScript whitespace", text: "\ufeff\u00a0value\u2028\u3000", want: "value"},
		{name: "embedded whitespace stays", text: "a\u00a0b", want: "a\u00a0b"},
		{name: "empty string", text: "", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := Trim(test.text); got != test.want {
				t.Errorf("Trim(%q) = %q, want %q", test.text, got, test.want)
			}
		})
	}
}
