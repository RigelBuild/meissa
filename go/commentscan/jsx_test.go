// SPDX-License-Identifier: MIT OR Apache-2.0

package commentscan

import (
	"github.com/odvcencio/gotreesitter/grammars/tsx"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScanJSX(t *testing.T) {
	tests := []struct {
		name, input string
		want        []string
		ambiguous   bool
	}{
		{"a // inside element text stays in the residue",
			"const E = () => <a>x//y</a>; // real", []string{"const E = () => <a>x//y</a>;"}, false},
		{"element text beginning with // is not stripped",
			"const E = () => <a>//lead</a>;", []string{"const E = () => <a>//lead</a>;"}, false},
		{"block comment markers in JSX text are not stripped",
			"const E = (y) => <a>{y}/* not */ text</a>;",
			[]string{"const E = (y) => <a>{y}/* not */ text</a>;"}, false},
		{"JSX expression comments are stripped",
			"const E = () => <div>{/* inner */}a</div>;", []string{"const E = () => <div>{}a</div>;"}, false},
		{"trailing comments are stripped after JSX",
			"const E = () => <a>text</a>; // note", []string{"const E = () => <a>text</a>;"}, false},
		{"normalizes CRLF before scanning JSX",
			"const E = () => (\r\n  <a>x</a>\r\n);\r\n// note\r\n",
			[]string{"const E = () => (", "  <a>x</a>", ");", "", ""}, false},
		{"generic arrows are not treated as JSX",
			"const f = <T,>(x: T) => x; // note", []string{"const f = <T,>(x: T) => x;"}, false},
		{"regex literals containing */ remain intact",
			`block.replace(/\s*\n\s*/g, " "); // note`, []string{`block.replace(/\s*\n\s*/g, " ");`}, false},
		{"template literal hashes remain content",
			"const s = `\n  # in shell\n`; // real\nx();",
			[]string{"const s = `", "  # in shell", "`;", "x();"}, false},
		{"TypeScript line directives stay in the residue",
			"// @ts-expect-error legacy shape", []string{"// @ts-expect-error legacy shape"}, false},
		{"ordinary block comments are removed",
			"/* prose */ const n = 1;", []string{" const n = 1;"}, false},
		{"multiline comments preserve source line numbering",
			"/*\n * note\n */\nconst n = 1;", []string{"", "", "", "const n = 1;"}, false},
		{"ampersands inside JSX attribute strings do not invalidate comments",
			`const item = <a href="/item?tab=code&file=src/App.tsx" />; // note`,
			[]string{`const item = <a href="/item?tab=code&file=src/App.tsx" />;`}, false},
		{"a parse error is ambiguous", "const E = () => <a>unclosed;",
			[]string{"const E = () => <a>unclosed;"}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ScanJSX(test.input)
			if got.Ambiguous != test.ambiguous || !reflect.DeepEqual(got.Residues, test.want) {
				t.Fatalf("ScanJSX() = %+v, want residues %#v ambiguous %t", got, test.want, test.ambiguous)
			}
		})
	}
}
func TestScanJSXReviewCases(t *testing.T) {
	t.Run("block-form Biome directives stay in the residue", func(t *testing.T) {
		text := strings.Join([]string{
			"const E = () => (",
			"  /* biome-ignore lint/a11y/useSemanticElements: reason */",
			`  <div role="button">x</div>`,
			");",
		}, "\n")
		got := ScanJSX(text)
		if strings.TrimSpace(got.Residues[1]) != "/* biome-ignore lint/a11y/useSemanticElements: reason */" {
			t.Fatalf("directive residue = %q", got.Residues[1])
		}
	})

	t.Run("comment width does not change code residue", func(t *testing.T) {
		wide := ScanJSX("const n = f(/* a longer note */ 1);")
		short := ScanJSX("const n = f(/* note */ 1);")
		if wide.Residues[0] != short.Residues[0] {
			t.Fatalf("comment width changed residue: wide %q, short %q", wide.Residues[0], short.Residues[0])
		}
	})

	t.Run("scanText routes TSX through the parser", func(t *testing.T) {
		got, err := ScanText("tsx", "const E = () => <a>x//y</a>;")
		if err != nil {
			t.Fatal(err)
		}
		if got.Residues[0] != "const E = () => <a>x//y</a>;" {
			t.Fatalf("TSX residue = %q", got.Residues[0])
		}
	})
}
func TestScanJSXReviewProbes(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		residues  []string
		ambiguous bool
	}{
		{
			"a string type annotation is not a string literal",
			"// note\nlet s: string = 'x';",
			[]string{"", "let s: string = 'x';"},
			false,
		},
		{
			"string comment extends beyond its closing quote",
			`const x = <a href="a&b /*" data-x={evil()} c="*/" />;`,
			[]string{`const x = <a href="a&b /*" data-x={evil()} c="*/" />;`},
			true,
		},
		{
			"block comment text stays in a string",
			`const x = <a href="a&b /* c */ d" />; // e`,
			[]string{`const x = <a href="a&b /* c */ d" />;`},
			false,
		},
		{
			"single quoted attribute comment text stays in a string",
			`const x = <a href='a&b /* c */' />;`,
			[]string{`const x = <a href='a&b /* c */' />;`},
			false,
		},
		{
			"comment text after newline stays in attribute string",
			"const x = <a href=\"a&b\n/* c */ d\" />;",
			[]string{"const x = <a href=\"a&b", "/* c */ d\" />;"},
			false,
		},
		{"raw newline in double quoted string", "f(\"a\n/* real */ b\");", []string{"f(\"a", "/* real */ b\");"}, true},
		{
			"raw newline in single quoted string",
			"const a = 'abc\n// real\n';",
			[]string{"const a = 'abc", "// real", "';"},
			true,
		},
		{"invalid escape 8", `const s = '\8';`, []string{`const s = '\8';`}, true},
		{"invalid escape 9", `const s = '\9';`, []string{`const s = '\9';`}, true},
		{"legacy octal escape", `const s = "\01";`, []string{`const s = "\01";`}, true},
		{"short Unicode escape", `const s = "\u12";`, []string{`const s = "\u12";`}, true},
		{"out of range Unicode escape", `const s = "\u{110000}";`, []string{`const s = "\u{110000}";`}, true},
		{"lone JSX attribute ampersand fails closed", `<a b="&" />`, []string{`<a b="&" />`}, true},
		{"trailing JSX attribute ampersand fails closed", `<a b="x&" />`, []string{`<a b="x&" />`}, true},
		{
			"query ampersand attribute fails closed",
			`<a href="?q=1&r=/* keep */" />`, []string{`<a href="?q=1&r=/* keep */" />`}, true,
		},
		{
			"line comment marker ampersand attribute fails closed",
			`<a href="a&b // c" />`, []string{`<a href="a&b // c" />`}, true,
		},
		{
			"regex after comparison and comment apostrophe stays intact",
			"if (a <b) { /* it's */ r = (c&/[/*]/.test(s)); keep(); x = ([/* */]); u = 'z'; }",
			[]string{"if (a <b) {  r = (c&/[/*]/.test(s)); keep(); x = ([]); u = 'z'; }"},
			false,
		},
		{
			"for loop comparison and logical ampersand stay intact",
			"for (let i = 0; i<n; i++) { // it's fine\n  if (a && b) run();\n}",
			[]string{"for (let i = 0; i<n; i++) {", "  if (a && b) run();", "}"},
			false,
		},
		{
			"generic call comparison and logical ampersand stay intact",
			"new Map<string, number>(); // it's fine\nif (a && b) run();",
			[]string{"new Map<string, number>();", "if (a && b) run();"},
			false,
		},
		{
			"arrow prop before ampersand attribute",
			`<a onClick={() => go()} title="&" />; // c`,
			[]string{`<a onClick={() => go()} title="&" />; // c`},
			true,
		},
		{
			"greater-equal expression before ampersand attribute",
			`<a b={x >= 1} c="&" />; // c`,
			[]string{`<a b={x >= 1} c="&" />; // c`},
			true,
		},
		{
			"JSX expression comment with apostrophe before later attribute",
			`<a b={/* it's */ c && d} e='z' />; // c`,
			[]string{`<a b={ c && d} e='z' />;`},
			false,
		},
		{
			"template prop before ampersand attribute",
			"<a b={`it's`} c=\"a&b\" d={e && f} />; // c",
			[]string{"<a b={`it's`} c=\"a&b\" d={e && f} />;"},
			false,
		},
		{
			"invalid short hex escape",
			`const s = '\x4'; // c`,
			[]string{`const s = '\x4'; // c`},
			true,
		},
		{
			"invalid hex escape character",
			`const s = '\x4g'; // c`,
			[]string{`const s = '\x4g'; // c`},
			true,
		},
		{
			"untagged template legacy escape 8",
			"const s = `\\8`; // c",
			[]string{"const s = `\\8`; // c"},
			true,
		},
		{
			"untagged template legacy octal escape",
			"const s = `\\01`; // c",
			[]string{"const s = `\\01`; // c"},
			true,
		},
		{
			"untagged template out-of-range Unicode escape",
			"const s = `\\u{110000}`; // c",
			[]string{"const s = `\\u{110000}`; // c"},
			true,
		},
		{
			"untagged template escape after substitution",
			"const s = `x ${a} \\8`; // c",
			[]string{"const s = `x ${a} \\8`; // c"},
			true,
		},
		{
			"tagged template legacy escape stays clean",
			"const s = String.raw`\\8`; // c",
			[]string{"const s = String.raw`\\8`;"},
			false,
		},
		{
			"tagged template octal escape stays clean",
			"const s = String.raw`\\01`; // c",
			[]string{"const s = String.raw`\\01`;"},
			false,
		},
		{
			"tagged template Unicode escape stays clean",
			"const s = String.raw`\\u{110000}`; // c",
			[]string{"const s = String.raw`\\u{110000}`;"},
			false,
		},
		{
			"template Unicode escape accepts long leading zeroes",
			"const s = `\\u{0000000041}`; // c",
			[]string{"const s = `\\u{0000000041}`;"},
			false,
		},
		{
			"string Unicode escape accepts long leading zeroes",
			"const s = \"\\u{0000000041}\"; // c",
			[]string{"const s = \"\\u{0000000041}\";"},
			false,
		},
		{
			"escaped quote ends a JSX attribute string",
			`const x = <a href="a&b\" c="/*" />; // c`,
			[]string{`const x = <a href="a&b\" c="/*" />; // c`},
			true,
		},
		{
			"ampersand in ordinary code stays unchanged",
			"const value = a & b; // c",
			[]string{"const value = a & b;"},
			false,
		},
		{
			"ampersands in template fragments stay content",
			"const s = `a&b`; // c",
			[]string{"const s = `a&b`;"},
			false,
		},
		{
			"escaped attribute quote does not widen string overrun",
			`const x = <a href="a&b\" c="/*" x={evil()} y="*/" />;`,
			[]string{`const x = <a href="a&b\" c="/*" x={evil()} y="*/" />;`},
			true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ScanJSX(test.input)
			if got.Ambiguous != test.ambiguous || !reflect.DeepEqual(got.Residues, test.residues) {
				t.Fatalf("ScanJSX() = %+v, want residues %#v ambiguous %t", got, test.residues, test.ambiguous)
			}
		})
	}
}
func TestJSXParseTextLeavesAmpersandsOutsideAttributeStrings(t *testing.T) {
	source := []byte(`<a title="a&b" />; const n = a & b;`)
	tree, err := jsxParser().Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Release()

	if !jsxParseText(source, tree.RootNode(), tsx.Language()) {
		t.Fatal("jsxParseText() did not normalize the JSX attribute ampersand")
	}
	want := []byte(`<a title="axb" />; const n = a & b;`)
	if !reflect.DeepEqual(source, want) {
		t.Fatalf("normalized text = %q, want %q", source, want)
	}
}

func TestScanJSXCorpus(t *testing.T) {
	paths, err := filepath.Glob("testdata/corpus/tsx/*.tsx")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			text, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := ScanJSX(string(text))
			if got.Ambiguous {
				t.Fatal("corpus fixture parsed ambiguously")
			}
			if len(got.Residues) != len(strings.Split(string(text), "\n")) {
				t.Fatalf("residue line count = %d", len(got.Residues))
			}
		})
	}
}
