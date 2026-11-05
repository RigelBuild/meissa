package commentscan

import "testing"

func TestIndentScalar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "single chomping modifier and indent indicator", text: "|-2", want: true},
		{name: "duplicate chomping modifier", text: "|-+2", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := indentScalar.MatchString(test.text); got != test.want {
				t.Errorf("indentScalar.MatchString(%q) = %t, want %t", test.text, got, test.want)
			}
		})
	}
}
