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

func TestLanguageTableSharesEntries(t *testing.T) {
	t.Parallel()

	shell := langByID()[LanguageShell]
	if langByExt()[".sh"] != shell {
		t.Fatal(`langByExt()[".sh"] is not the langByID()["shell"] entry`)
	}
	if langByExt()[".bash"] != shell || langByExt()[".zsh"] != shell {
		t.Fatal("shell extensions do not share one entry")
	}
	interp := shell.Strings[2].InterpLang
	if interp == nil || interp != languages.shellInterp || langByID()[LanguageShell].Strings[2].InterpLang != interp {
		t.Fatal("shell InterpLang is not the shared shell-interpolation entry")
	}
	if langByID()[LanguageHTML].Expr.Lang != langByID()[LanguageTS] {
		t.Fatal("HTML Expr.Lang is not the shared TS entry")
	}
}
