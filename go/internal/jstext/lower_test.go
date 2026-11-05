package jstext

import "testing"

func TestToLower(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "ordinary lowercase", text: "Component.TSX", want: "component.tsx"},
		{name: "word-final sigma", text: "ΟΣ", want: "ος"},
		{name: "sigma before cased letter", text: "ΟΣΑ", want: "οσα"},
		{name: "sigma before case-ignorable marks", text: "ΟΣ\u0301", want: "ος\u0301"},
		{name: "capital dotted I", text: "İ", want: "i\u0307"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ToLower(test.text); got != test.want {
				t.Errorf("ToLower(%q) = %q, want %q", test.text, got, test.want)
			}
		})
	}
}
