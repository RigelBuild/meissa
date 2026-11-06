package commentscan

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func scanWant(t *testing.T, lang LanguageID, text string) Residues {
	t.Helper()
	got, err := ScanText(lang, text)
	if err != nil {
		t.Fatalf("ScanText(%q): %v", lang, err)
	}
	return got
}

func residuesWant(t *testing.T, lang LanguageID, text string, want []string) {
	t.Helper()
	got := scanWant(t, lang, text)
	if !reflect.DeepEqual(got.Residues, want) {
		t.Errorf("residues = %#v, want %#v", got.Residues, want)
	}
}

func TestScanText(t *testing.T) {
	t.Run("Rust nested block comments close at the right depth", func(t *testing.T) {
		got := scanWant(t, "rust", "/* outer /* inner */ code()\nstill comment */ real()")
		if got.Residues[0] != "" || strings.TrimSpace(got.Residues[1]) != "real()" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("Go block comments do not nest", func(t *testing.T) {
		if got := strings.TrimSpace(scanWant(t, "go", "/* outer /* inner */ code()").Residues[0]); got != "code()" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("Rust lifetimes do not open character literals", func(t *testing.T) {
		residuesWant(t, "rust", "fn f<'a>(x: &'a str) {} // note", []string{"fn f<'a>(x: &'a str) {}"})
	})
	t.Run("Rust character literals count Unicode runes", func(t *testing.T) {
		residuesWant(t, "rust", `['é','"'] // note`, []string{`['é','"']`})
	})
	t.Run("escaped quotes do not close strings early", func(t *testing.T) {
		residuesWant(t, "ts", `const s = "a\"b"; // note`, []string{`const s = "a\"b";`})
	})
	t.Run("Python triple-quoted strings are code", func(t *testing.T) {
		residuesWant(t, "python", `"""a docstring"""`, []string{`"""a docstring"""`})
	})
	t.Run("unterminated single-line strings are ambiguous", func(t *testing.T) {
		if !scanWant(t, "toml", "value = 'unfinished\nnext = 1").Ambiguous {
			t.Fatal("want ambiguous")
		}
	})
	t.Run("unclosed block comments are ambiguous", func(t *testing.T) {
		if !scanWant(t, "ts", "/* unfinished").Ambiguous {
			t.Fatal("want ambiguous")
		}
	})
	t.Run("YAML block scalars preserve their body and resume after dedent", func(t *testing.T) {
		residuesWant(
			t,
			"yaml",
			"script: |\n  # payload\n  text\n# note\nnext: 1",
			[]string{"script: |", "  # payload", "  text", "", "next: 1"},
		)
	})
	t.Run("YAML block scalars preserve comment payload with CRLF input", func(t *testing.T) {
		got := scanWant(t, "yaml", "script: |\r\n  # payload\r\n  text\r\n# note\r\nnext: 1")
		if !reflect.DeepEqual(got.Residues, []string{"script: |", "  # payload", "  text", "", "next: 1"}) || got.Ambiguous {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("YAML apostrophes in plain scalars do not open strings", func(t *testing.T) {
		residuesWant(t, "yaml", "text: it's fine\nother: '# value'", []string{"text: it's fine", "other: '# value'"})
	})
	t.Run("TOML multiline strings keep hash-prefixed payload", func(t *testing.T) {
		residuesWant(
			t,
			"toml",
			"input = \"\"\"\n# payload\n\"\"\"\n# note",
			[]string{"input = \"\"\"", "# payload", "\"\"\"", ""},
		)
	})
	t.Run("SQL dollar-quoted bodies keep comments as payload", func(t *testing.T) {
		residuesWant(t, "sql", "DO $$\n-- payload\nEND $$;\n-- note", []string{"DO $$", "-- payload", "END $$;", ""})
	})
	t.Run("SQL tagged dollar quotes close only on their tag", func(t *testing.T) {
		residuesWant(
			t,
			"sql",
			"DO $body$\n-- payload\nSELECT $$ not a close $$;\n$body$;\n-- note",
			[]string{"DO $body$", "-- payload", "SELECT $$ not a close $$;", "$body$;", ""},
		)
	})
	t.Run("shell heredoc content stays code and scanning resumes", func(t *testing.T) {
		residuesWant(
			t,
			"shell",
			"cat <<EOF\n# payload\nEOF\n# note\necho done",
			[]string{"cat <<EOF", "# payload", "EOF", "", "echo done"},
		)
	})
	t.Run("shell parameter expansion hashes are code", func(t *testing.T) {
		parameter := "$" + "{x#prefix}"
		residuesWant(t, "shell", "echo "+parameter+" # note", []string{"echo " + parameter})
	})
	t.Run("regex literals after return are not comments", func(t *testing.T) {
		got := scanWant(t, "ts", `return /[\/]/;`+"\nconst x = 1; // note")
		if got.Ambiguous || !strings.Contains(got.Residues[0], `return /[\/]/;`) {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("an unterminated interpolation remains ambiguous", func(t *testing.T) {
		if !scanWant(t, "nix", `"${unfinished"`).Ambiguous {
			t.Fatal("want ambiguous")
		}
	})
	t.Run("TS template literals keep shell-style comments as code", func(t *testing.T) {
		residuesWant(
			t,
			"ts",
			strings.Join([]string{"const script = `", "  # payload", "`; // note"}, "\n"),
			[]string{"const script = `", "  # payload", "`;"},
		)
	})
	t.Run("nested Nix interpolation strings keep hashes as code", func(t *testing.T) {
		got := scanWant(t, "nix", `x = "a ${g "b#c"} d";`)
		if got.Ambiguous || got.Residues[0] != `x = "a ${g "b#c"} d";` {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("Nix interpolation restores comment scanning after indented strings", func(t *testing.T) {
		text := strings.Join(
			[]string{`"${pkgs.writeShellScript "task" ''`, "  # payload", `''}"`, "# note", "value = 1;"},
			"\n",
		)
		got := scanWant(t, "nix", text)
		if got.Ambiguous || got.Residues[3] != "" || got.Residues[4] != "value = 1;" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("comments inside Nix interpolation are stripped", func(t *testing.T) {
		residuesWant(t, "nix", `x = "pre ${ /*n*/ v } post";`, []string{`x = "pre ${  v } post";`})
	})
	t.Run("an escaped Nix interpolation opener stays inside the string", func(t *testing.T) {
		text := strings.Join([]string{"value = ''", "  echo ''${VALUE}", "''; # note", "next = 2;"}, "\n")
		if got := scanWant(t, "nix", text); got.Residues[3] != "next = 2;" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("nested Nix braces do not close interpolation early", func(t *testing.T) {
		text := strings.Join([]string{`"pre ${ { a = 1; }.a } post" # note`, "next = 2;"}, "\n")
		got := scanWant(t, "nix", text)
		if got.Residues[0] != `"pre ${ { a = 1; }.a } post"` || got.Residues[1] != "next = 2;" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("KDL slashdash stays in the code residue", func(t *testing.T) {
		residuesWant(t, "kdl", `/-bind "x" { Quit; }`, []string{`/-bind "x" { Quit; }`})
	})
	t.Run("CSS URL targets treat comment markers as content", func(t *testing.T) {
		residuesWant(t, "css", "src: url(/fonts/a/*b); color: red;", []string{"src: url(/fonts/a/*b); color: red;"})
	})
	t.Run("protobuf single-quoted strings containing comment markers stay code", func(t *testing.T) {
		residuesWant(t, "proto", "option java_package = 'a//b'; // note", []string{"option java_package = 'a//b';"})
	})
	t.Run("protobuf strings containing comment markers stay code", func(t *testing.T) {
		residuesWant(
			t,
			"proto",
			`option java_package = "example//pkg"; // note`,
			[]string{`option java_package = "example//pkg";`},
		)
	})
}

func TestResidueScannerFor(t *testing.T) {
	t.Run("returns no scanner for unknown extensions", func(t *testing.T) {
		if _, ok := ResidueScannerFor(".unknown"); ok {
			t.Fatal("scanner unexpectedly exists")
		}
	})
	t.Run("TSX stays unrouted until M2: TSX, JSX and Astro have no lexical scanner", func(t *testing.T) {
		for _, ext := range []string{".tsx", ".jsx", ".astro"} {
			if _, ok := ResidueScannerFor(ext); ok {
				t.Errorf("%s must remain unrouted", ext)
			}
		}
	})
	t.Run("routes proto to the lexical scanner", func(t *testing.T) {
		scan, ok := ResidueScannerFor(".proto")
		if !ok {
			t.Fatal("missing proto scanner")
		}
		if got := scan("// note\nsyntax = \"proto3\";"); got.Residues[0] != "" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("TSX stays unrouted until M2: CRLF input does not route TSX, JSX or Astro", func(t *testing.T) {
		for _, ext := range []string{".tsx", ".jsx", ".astro"} {
			if _, ok := ResidueScannerFor(ext); ok {
				t.Errorf("%s must remain unrouted", ext)
			}
		}
	})
	t.Run("normalizes CRLF block scalar input", func(t *testing.T) {
		scan, ok := ResidueScannerFor(".yaml")
		if !ok {
			t.Fatal("missing yaml scanner")
		}
		got := scan("script: |\r\n  # payload\r\n  text\r\n# note\r\nnext: 1")
		if !reflect.DeepEqual(got.Residues, []string{"script: |", "  # payload", "  text", "", "next: 1"}) {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestScanConfigLanguages(t *testing.T) {
	t.Run("# markers inside double- and single-quoted values stay code", func(t *testing.T) {
		residuesWant(t, "yaml", `value: "a#b" # note`, []string{`value: "a#b"`})
		residuesWant(t, "yaml", "value: 'a#b' # note", []string{"value: 'a#b'"})
	})
	t.Run("JavaScript whitespace before a quote still allows the quote to open", func(t *testing.T) {
		residuesWant(t, "yaml", "value:\u00a0'a # b' # note", []string{"value:\u00a0'a # b'"})
	})
	t.Run("hashes in URL scalars and apostrophes are not comment starts", func(t *testing.T) {
		residuesWant(t, "yaml", "url: http://a#f", []string{"url: http://a#f"})
		residuesWant(t, "yaml", "text: it's fine\nnext: '# value'", []string{"text: it's fine", "next: '# value'"})
	})
	t.Run("block scalar bodies are payload and dedent resumes scanning", func(t *testing.T) {
		residuesWant(
			t,
			"yaml",
			"script: |\n  # payload\n  line\n# note\nnext: 1",
			[]string{"script: |", "  # payload", "  line", "", "next: 1"},
		)
	})
	t.Run("tags and anchors before block scalar indicators are supported", func(t *testing.T) {
		residuesWant(t, "yaml", "value: !!str |\n  # payload\nnext: 1", []string{"value: !!str |", "  # payload", "next: 1"})
	})
	t.Run("templated lines are not comments", func(t *testing.T) {
		residuesWant(t, "yaml", "{{ if .Values.x }} # value", []string{"{{ if .Values.x }} # value"})
	})
	t.Run("hash markers in basic and literal strings stay code", func(t *testing.T) {
		residuesWant(t, "toml", `value = "a#b" # note`, []string{`value = "a#b"`})
		residuesWant(t, "toml", "value = 'a#b' # note", []string{"value = 'a#b'"})
	})
	t.Run("multiline string payload stays code", func(t *testing.T) {
		residuesWant(
			t,
			"toml",
			"input = \"\"\"\n# payload\n\"\"\"\n# note",
			[]string{"input = \"\"\"", "# payload", "\"\"\"", ""},
		)
	})
}

func TestCSS(t *testing.T) {
	t.Run("// is not a comment in CSS", func(t *testing.T) {
		residuesWant(t, "css", "a { width: calc(1px // 2); }", []string{"a { width: calc(1px // 2); }"})
	})
	t.Run("quoted URL targets keep block comment markers as content", func(t *testing.T) {
		residuesWant(t, "css", `src: url("/fonts/a/*b.otf"); /* note */`, []string{`src: url("/fonts/a/*b.otf");`})
	})
	t.Run("unquoted URL targets keep comment markers as content", func(t *testing.T) {
		got := scanWant(t, "css", "src: url(/fonts/a/*b.otf); color: red;")
		if got.Ambiguous || got.Residues[0] != "src: url(/fonts/a/*b.otf); color: red;" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("uppercase URL function names are recognized", func(t *testing.T) {
		residuesWant(t, "css", "src: URL(/a/*b.otf); color: red;", []string{"src: URL(/a/*b.otf); color: red;"})
	})
	t.Run("escaped parentheses do not close unquoted URL targets", func(t *testing.T) {
		residuesWant(t, "css", `src: url(/a\)b/*c.otf); color: red;`, []string{`src: url(/a\)b/*c.otf); color: red;`})
	})
	t.Run("comments following a quoted URL are stripped", func(t *testing.T) {
		got := scanWant(t, "css", `src: url("/fonts/a(b).otf");`+"\n/* note */\na { color: red; }")
		want := []string{`src: url("/fonts/a(b).otf");`, "", "a { color: red; }"}
		if got.Ambiguous || !reflect.DeepEqual(got.Residues, want) {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("CSS suppression comments remain in code residue", func(t *testing.T) {
		residuesWant(t, "css", "/* stylelint-disable-next-line rule */", []string{"/* stylelint-disable-next-line rule */"})
		residuesWant(
			t,
			"css",
			"/* biome-ignore lint/style/noX: reason */",
			[]string{"/* biome-ignore lint/style/noX: reason */"},
		)
	})
}

func TestKDLResidues(t *testing.T) {
	t.Run("line and block comment markers in strings remain content", func(t *testing.T) {
		residuesWant(t, "kdl", `url "http://a/b" // note`, []string{`url "http://a/b"`})
		residuesWant(t, "kdl", `p #"a//b"# // note`, []string{`p #"a//b"#`})
	})
	t.Run("slashdash remains code", func(t *testing.T) {
		residuesWant(t, "kdl", `/-bind "x" { Quit; }`, []string{`/-bind "x" { Quit; }`})
	})
	t.Run("block comments nest", func(t *testing.T) {
		text := "/* outer /* inner */ still comment */ node 1"
		if got := strings.TrimSpace(scanWant(t, "kdl", text).Residues[0]); got != "node 1" {
			t.Errorf("got %q", got)
		}
	})
}

func TestUnclosedLexicalConstructs(t *testing.T) {
	unfinished := []struct {
		name string
		lang LanguageID
		text string
	}{
		{"Go block comment", "go", "/* unfinished"},
		{"TypeScript template", "ts", "`unfinished ${ x"},
		{"Rust block comment", "rust", "/* unfinished"},
		{"Nix interpolation", "nix", `"${unfinished"`},
		{"Python triple string", "python", `"""unfinished`},
		{"TOML multiline string", "toml", `value = """unfinished`},
		{"YAML quoted scalar", "yaml", `value: "unfinished`},
		{"shell heredoc", "shell", "cat <<EOF\npayload"},
		{"SQL dollar quote", "sql", "DO $$\npayload"},
		{"KDL block comment", "kdl", "/* unfinished"},
		{"CSS URL", "css", "src: url(/fonts/a.otf"},
		{"HTML attribute value", "html", `<p data-x="unfinished`},
		{"KDL raw string", "kdl", `value #"unfinished`},
		{"Proto block comment", "proto", "/* unfinished"},
		{"Proto string", "proto", `option java_package = "unfinished`},
	}
	for _, test := range unfinished {
		t.Run(test.name+" is ambiguous", func(t *testing.T) {
			if !scanWant(t, test.lang, test.text).Ambiguous {
				t.Fatal("want ambiguous")
			}
		})
	}
	t.Run("YAML block scalars may end at EOF", func(t *testing.T) {
		if scanWant(t, "yaml", "script: |\n  echo value").Ambiguous {
			t.Fatal("unexpected ambiguity")
		}
	})
	t.Run("unterminated single-line strings are ambiguous", func(t *testing.T) {
		for _, test := range []struct {
			lang LanguageID
			text string
		}{
			{"go", `value = "unfinished`},
			{"ts", `const value = "unfinished`},
			{"toml", "value = 'unfinished"},
			{"sql", "SELECT 'unfinished"},
		} {
			if !scanWant(t, test.lang, test.text).Ambiguous {
				t.Errorf("%s was not ambiguous", test.lang)
			}
		}
	})
	t.Run("closed strings and comments are not ambiguous", func(t *testing.T) {
		ts := scanWant(t, "ts", `const value = "closed";`+"\n// note")
		rust := scanWant(t, "rust", "/* closed */\nfn run() {}")
		if ts.Ambiguous || rust.Ambiguous {
			t.Fatal("closed source is ambiguous")
		}
	})
}

func TestShellResidues(t *testing.T) {
	t.Run("# in quotes and command substitutions is payload", func(t *testing.T) {
		residuesWant(t, "shell", `echo '# value' "quoted # value"`, []string{`echo '# value' "quoted # value"`})
		if got := scanWant(t, "shell", `echo "$(printf %s '# value')"`); !strings.Contains(got.Residues[0], "# value") {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("hashes open comments only at a shell word start", func(t *testing.T) {
		residuesWant(t, "shell", "echo hi;# note", []string{"echo hi;"})
		residuesWant(t, "shell", "echo hi#suffix", []string{"echo hi#suffix"})
		parameter := "$" + "{x#prefix}"
		residuesWant(t, "shell", "echo "+parameter, []string{"echo " + parameter})
	})
	t.Run("heredoc body stays code and following comments resume", func(t *testing.T) {
		residuesWant(
			t,
			"shell",
			"cat <<EOF\n# payload\nEOF\n# note\necho done",
			[]string{"cat <<EOF", "# payload", "EOF", "", "echo done"},
		)
	})
	t.Run("quoted and tab-stripped heredoc delimiters close their bodies", func(t *testing.T) {
		residuesWant(t, "shell", "cat <<'EOF'\n# payload\nEOF\n# note", []string{"cat <<'EOF'", "# payload", "EOF", ""})
		residuesWant(t, "shell", "cat <<-EOF\n# payload\n\tEOF\n# note", []string{"cat <<-EOF", "# payload", "\tEOF", ""})
	})
	t.Run("here strings do not open heredoc frames", func(t *testing.T) {
		text := `read x <<<"$y"` + "\n# a real comment\necho after"
		residuesWant(t, "shell", text, []string{`read x <<<"$y"`, "", "echo after"})
	})
	t.Run("arithmetic shifts do not open heredoc frames", func(t *testing.T) {
		for _, expr := range []string{"n=$(( a << b ))", "n=$(( a << 2 ))"} {
			residuesWant(t, "shell", expr+"\n# a real comment\necho after", []string{expr, "", "echo after"})
		}
	})
}

func TestSQLResidues(t *testing.T) {
	t.Run("hash-like tokens in strings and positional parameters are code", func(t *testing.T) {
		residuesWant(t, "sql", "SELECT 'a -- b'; -- note", []string{"SELECT 'a -- b';"})
		// Parts keep the TS input byte-identical without tripping unqueryvet on a test fixture.
		star := "*"
		residuesWant(
			t,
			"sql",
			"SELECT "+star+" FROM t WHERE id = $1; -- note",
			[]string{"SELECT " + star + " FROM t WHERE id = $1;"},
		)
	})
	t.Run("doubled quotes and nested block comments are handled", func(t *testing.T) {
		residuesWant(t, "sql", "SELECT 'it''s -- payload'; -- note", []string{"SELECT 'it''s -- payload';"})
		text := "/* outer /* inner */ still comment */ SELECT 1;"
		if got := strings.TrimSpace(scanWant(t, "sql", text).Residues[0]); got != "SELECT 1;" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("tagged dollar quotes keep their body and close on matching tag", func(t *testing.T) {
		residuesWant(
			t,
			"sql",
			"DO $fn$\n-- payload\nSELECT $$ not close $$;\n$fn$;\n-- note",
			[]string{"DO $fn$", "-- payload", "SELECT $$ not close $$;", "$fn$;", ""},
		)
	})
}

func TestCSSURLMatcher(t *testing.T) {
	tests := []struct {
		name, text string
		want       bool
	}{
		{"unquoted URL", "url(/a/*b)", true},
		{"uppercase URL", "URL(/a)", true},
		{"quoted target after whitespace", "url( \"a\")", false},
		{"not URL", "urlish(a)", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, got := cssURLOpen(test.text)
			if got != test.want {
				t.Errorf("cssURLOpen(%q) = %t, want %t", test.text, got, test.want)
			}
		})
	}
}

func TestStickyMatchers(t *testing.T) {
	t.Run("GO_IDENTIFIER does not start with a digit", func(t *testing.T) {
		tokens := goTokens("7NaN")
		if len(tokens) != 1 || tokens[0].kind == goIdentifier {
			t.Fatalf("goTokens(7NaN) = %+v, want no identifier token", tokens)
		}
	})
	t.Run("GO_IDENTIFIER matches at the suffix start", func(t *testing.T) {
		tokens := goTokens("name!")
		if len(tokens) < 1 || tokens[0].kind != goIdentifier || tokens[0].value != "name" {
			t.Fatalf("goTokens(name!) = %+v, want identifier name", tokens)
		}
	})
	tests := []struct {
		name    string
		pattern *regexp.Regexp
		text    string
		want    string
	}{
		{name: "heredoc opener matches the current suffix", pattern: shellHeredoc, text: "x<<EOF", want: ""},
		{
			name:    "tagged SQL opener matches the current suffix",
			pattern: langSQL().Strings[1].OpenRe,
			text:    "x$body$",
			want:    "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ""
			if matches := matchAt(test.pattern, test.text, 0); matches != nil {
				got = matches[0]
			}
			if got != test.want {
				t.Errorf("matchAt(%q) = %q, want %q", test.text, got, test.want)
			}
		})
	}
}
