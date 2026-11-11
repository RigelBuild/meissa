package commentscan

import (
	"reflect"
	"strings"
	"testing"
)

func blockWant(t *testing.T, text, ext string) BlockScan {
	t.Helper()
	got, ok := CommentBlocks(text, ext)
	if !ok {
		t.Fatalf("CommentBlocks has no scanner for %s", ext)
	}
	return got
}

func blockDocs(blocks []CommentBlock) []bool {
	docs := make([]bool, len(blocks))
	for i, block := range blocks {
		docs[i] = block.Doc
	}
	return docs
}

func wantDocs(t *testing.T, got, want []bool) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("doc flags = %v, want %v", got, want)
	}
}

func TestGoTokens(t *testing.T) {
	tests := []struct {
		name   string
		source string
		kind   goTokenKind
		value  string
	}{
		{name: "Unicode numbers continue an identifier", source: "nameⅧ", kind: goIdentifier, value: "nameⅧ"},
		{name: "Unicode symbol preserves the rune", source: "§", kind: goSymbol, value: "§"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tokens := goTokens(test.source)
			if len(tokens) != 1 || tokens[0].kind != test.kind || tokens[0].value != test.value {
				t.Fatalf("goTokens(%q) = %+v, want one %v token %q", test.source, tokens, test.kind, test.value)
			}
		})
	}
}

func TestCommentBlocks(t *testing.T) {
	t.Run("a five-line comment run is one block and a blank line splits runs", func(t *testing.T) {
		got := blockWant(t, "// one\n// two\n// three\n// four\n// five\n\n// next\npackage p", ".go")
		want := []CommentBlock{{Start: 1, Lines: 5, First: "", Doc: false}, {Start: 7, Lines: 1, First: "", Doc: false}}
		if len(got.Blocks) != len(want) || got.Blocks[0].Start != want[0].Start || got.Blocks[0].Lines != want[0].Lines ||
			got.Blocks[1].Start != want[1].Start || got.Blocks[1].Lines != want[1].Lines {
			t.Fatalf("blocks = %+v", got.Blocks)
		}
	})
	t.Run("a go:embed directive ends the comment block", func(t *testing.T) {
		got := blockWant(t, "// one\n// two\n//go:embed data.txt\n// three\npackage p", ".go")
		if len(got.Blocks) != 2 || got.Blocks[0].Start != 1 || got.Blocks[0].Lines != 2 ||
			got.Blocks[1].Start != 4 || got.Blocks[1].Lines != 1 {
			t.Fatalf("blocks = %+v", got.Blocks)
		}
	})
	t.Run("slash doc comments and block doc comments are doc blocks", func(t *testing.T) {
		got := blockWant(t, "/// doc\nfunc f() {}\n\n/** block */\nfunc g() {}", ".ts")
		wantDocs(t, blockDocs(got.Blocks), []bool{true, true})
	})
	t.Run("Go package comments are doc comments regardless of package case", func(t *testing.T) {
		for _, name := range []string{"p", "Private", "MixedCase"} {
			if got := blockWant(t, "// package note\npackage "+name, ".go").Blocks[0].Doc; !got {
				t.Errorf("package %q was not doc", name)
			}
		}
	})
	t.Run("Go exported and unexported standalone declarations classify", func(t *testing.T) {
		declarations := []string{
			"func Exported() {}", "func (r *T) Exported() {}", "const Exported = 1",
			"type Exported struct{}", "var Exported int",
		}
		text := ""
		for _, declaration := range declarations {
			if text != "" {
				text += "\n\n"
			}
			text += "// note\n" + declaration
		}
		wantDocs(t, blockDocs(blockWant(t, text, ".go").Blocks), []bool{true, true, true, true, true})
		unexported := []string{
			"func hidden() {}", "func (r *T) hidden() {}", "const hidden = 1",
			"type hidden struct{}", "var hidden int",
		}
		text = ""
		for _, declaration := range unexported {
			if text != "" {
				text += "\n\n"
			}
			text += "// note\n" + declaration
		}
		wantDocs(t, blockDocs(blockWant(t, text, ".go").Blocks), []bool{false, false, false, false, false})
	})
	t.Run("a blank line detaches Go declaration comments", func(t *testing.T) {
		wantDocs(t, blockDocs(blockWant(t, "// note\n\nfunc Exported() {}", ".go").Blocks), []bool{false})
	})
	t.Run("Go grouped declarations are exported when any spec name is exported", func(t *testing.T) {
		text := "// group\nconst (\n\tprivate = 1\n\tExported = 2\n)\n\n// spec\nvar (\n\tValue, hidden int\n)"
		wantDocs(t, blockDocs(blockWant(t, text, ".go").Blocks), []bool{true, true})
		wantDocs(t, blockDocs(blockWant(t, "// private group\nvar (\n\ta int\n\tb int\n)", ".go").Blocks), []bool{false})
	})
	t.Run("Go group comments classify exported names at the keyword and spec", func(t *testing.T) {
		text := strings.Join([]string{
			"// exported const group\nconst (\n  hidden = 1\n  Exported = 2\n)",
			"// const spec\nconst (\n  // exported spec doc\n  ExportedSpec = 3\n)",
			"// private const group\nconst (\n  hidden = 4\n)",
			"// private const spec\nconst (\n  // private spec doc\n  hiddenSpec = 5\n)",
			"// exported var group\nvar (\n  hiddenVar int\n  ExportedVar int\n)",
			"// var spec\nvar (\n  // exported spec doc\n  ExportedVarSpec int\n)",
			"// private var group\nvar (\n  hiddenVar int\n)",
			"// private var spec\nvar (\n  // private spec doc\n  hiddenVarSpec int\n)",
			"// exported type group\ntype (\n  hiddenType struct{}\n  ExportedType struct{}\n)",
			"// type spec\ntype (\n  // exported spec doc\n  ExportedTypeSpec struct{}\n)",
			"// private type group\ntype (\n  hiddenType struct{}\n)",
			"// private type spec\ntype (\n  // private spec doc\n  hiddenTypeSpec struct{}\n)",
		}, "\n")
		wantDocs(t, blockDocs(blockWant(t, text, ".go").Blocks), []bool{
			true, true, true, false, false, false,
			true, true, true, false, false, false,
			true, true, true, false, false, false,
		})
	})
	t.Run("Go group detection ignores parentheses inside string literals", func(t *testing.T) {
		got := blockWant(t, `// group
const (
  hidden = ")"
  // )
  Exported = 1
)`, ".go")
		if len(got.Blocks) == 0 || !got.Blocks[0].Doc {
			t.Fatalf("first group block = %+v", got.Blocks)
		}
	})
	t.Run("Go multiple names are exported if any name is exported", func(t *testing.T) {
		text := "// var exported\nvar a, B int\n\n// var private\nvar a, b int\n\n" +
			"// const exported\nconst a, B = 1, 2\n\n// const private\nconst a, b = 1, 2"
		wantDocs(t, blockDocs(blockWant(t, text, ".go").Blocks), []bool{true, false, true, false})
	})
	t.Run("nested struct fields do not make a Go type group exported", func(t *testing.T) {
		got := blockWant(t, "// group\ntype (\n  hidden struct {\n    Exported int\n  }\n)", ".go")
		wantDocs(t, blockDocs(got.Blocks), []bool{false})
	})
	t.Run("Go grouped funcs and nested fields do not end group scanning", func(t *testing.T) {
		text := "// hidden group\ntype (\n  hidden func(\n    x int,\n  ) (\n    result int\n  )\n" +
			"  Exported struct{}\n)\n\n// field\ntype (\n  hidden struct {\n    Exported int\n  }\n)"
		wantDocs(t, blockDocs(blockWant(t, text, ".go").Blocks), []bool{true, false})
	})
	t.Run("an exported spec that opens parentheses still exports its group", func(t *testing.T) {
		text := "// group\nvar (\n  Exported func(\n    x int,\n  )\n)"
		wantDocs(t, blockDocs(blockWant(t, text, ".go").Blocks), []bool{true})
	})
	t.Run("Go exported names use Unicode uppercase code points", func(t *testing.T) {
		text := "// supplementary\nvar 𐐀value int\n\n// titlecase\nvar ǅvalue int"
		wantDocs(t, blockDocs(blockWant(t, text, ".go").Blocks), []bool{true, false})
	})
	t.Run("Go grouped multi-name const and var specs classify any exported name", func(t *testing.T) {
		text := "// const exported\nconst (\n  a, B = 1, 2\n)\n\n// const private\nconst (\n  a, b = 1, 2\n)\n\n" +
			"// var exported\nvar (\n  a, B int\n)\n\n// var private\nvar (\n  a, b int\n)"
		wantDocs(t, blockDocs(blockWant(t, text, ".go").Blocks), []bool{true, false, true, false})
	})
	t.Run("proto scopes open when declaration braces start on the next line", func(t *testing.T) {
		text := "message M\n{\n  // field\n  string value = 1;\n}\nservice S\n{\n  // rpc\n" +
			"  rpc Call (Req) returns (Res);\n}\nenum E\n{\n  // value\n  ZERO = 0;\n}"
		wantDocs(t, blockDocs(blockWant(t, text, ".proto").Blocks), []bool{true, true, true})
	})
	t.Run("Unicode whitespace separates proto declaration tokens", func(t *testing.T) {
		text := "message\u00a0M {\n  // field\n  string value = 1;\n}"
		wantDocs(t, blockDocs(blockWant(t, text, ".proto").Blocks), []bool{true})
	})
	t.Run("cgo recognizes standalone and grouped imports with semicolons", func(t *testing.T) {
		for _, text := range []string{
			"package p\nimport \"C\";", "package p\nimport (\n\"C\"\n)",
			"package p\nimport (\n\"fmt\"\n\"C\"\n)", "package p\nimport (\n\"C\";\n)",
		} {
			if !IsCgo(text) {
				t.Errorf("IsCgo(%q) = false", text)
			}
		}
	})
	t.Run("raw strings containing import C do not classify as cgo", func(t *testing.T) {
		if IsCgo("package p\nvar source = `\nimport \"C\"\n`") {
			t.Fatal("raw string classified as cgo")
		}
	})
	t.Run("Go grouped multi-name spec comments classify exported names", func(t *testing.T) {
		text := "const (\n  // exported const spec\n  a, B = 1, 2\n  // private const spec\n  a, b = 3, 4\n)\n" +
			"var (\n  // exported var spec\n  a, B int\n  // private var spec\n  a, b int\n)"
		wantDocs(t, blockDocs(blockWant(t, text, ".go").Blocks), []bool{true, false, true, false})
	})
	t.Run("Go semicolon insertion keeps continued declaration names together", func(t *testing.T) {
		for _, text := range []string{"// doc\nvar a,\nB int", "// doc\nvar (\n a,\n B int\n)"} {
			wantDocs(t, blockDocs(blockWant(t, text, ".go").Blocks), []bool{true})
		}
	})
	t.Run("Go declaration classification uses original tokens and keyword lines", func(t *testing.T) {
		for _, text := range []string{
			"// var comment\nvar/* c */Exported int", "// func comment\nfunc/* c */Exported() {}",
			"// var comment\nvar\nExported int", "// func comment\nfunc\nExported() {}",
			"// package comment\npackage\np",
		} {
			wantDocs(t, blockDocs(blockWant(t, text, ".go").Blocks), []bool{true})
		}
	})
	t.Run("the first Go declaration on a line determines its doc class", func(t *testing.T) {
		for _, text := range []string{
			"// doc\nvar (hidden int); var (Exported int)", "// doc\nvar hidden int; var Exported int",
			"// doc\nvar hidden int; var (Exported int)",
		} {
			wantDocs(t, blockDocs(blockWant(t, text, ".go").Blocks), []bool{false})
		}
	})
	t.Run("proto declaration matching treats block comments as whitespace", func(t *testing.T) {
		text := "// message\nmessage/* c */M {\n  // field\n  string value = 1;\n}"
		wantDocs(t, blockDocs(blockWant(t, text, ".proto").Blocks), []bool{true, true})
	})
	t.Run("proto declarations may span lines around a block comment", func(t *testing.T) {
		text := "// message\n" + "message /* c\n*/ M\n{\n  // field\n  string value = 1; // tail"
		wantDocs(t, blockDocs(blockWant(t, text, ".proto").Blocks), []bool{true, true})
	})
	t.Run("local declarations do not classify as package documentation", func(t *testing.T) {
		text := "func run() {\n  // local var\n" +
			"  var Exported int\n  // local const group\n  const (\n    Exported = 1\n  )\n}"
		wantDocs(t, blockDocs(blockWant(t, text, ".go").Blocks), []bool{false, false})
	})
	t.Run("Go directives in trailing comments do not create cgo imports", func(t *testing.T) {
		if IsCgo("package p\nvar x = 1 //go:generate echo import \"C\"") {
			t.Fatal("trailing comment classified as cgo")
		}
	})
	t.Run("cgo tokenization supports same-line and grouped separators", func(t *testing.T) {
		for _, text := range []string{
			"package p; import \"C\"", "import (\"C\";)", "import (\"C\")",
			"package p\n//go:generate tool `\nimport \"C\"",
		} {
			if !IsCgo(text) {
				t.Errorf("IsCgo(%q) = false", text)
			}
		}
	})
	t.Run("grouped inline Go specs classify the first exported name", func(t *testing.T) {
		wantDocs(t, blockDocs(blockWant(t, "// g\nvar (Exported int;)", ".go").Blocks), []bool{true})
	})
	t.Run("proto enum value comments attach to enum values", func(t *testing.T) {
		wantDocs(t, blockDocs(blockWant(t, "enum E {\n  // doc\n  ZERO = 0;\n}", ".proto").Blocks), []bool{true})
	})
	t.Run("proto comments attach to declarations without an intervening blank line", func(t *testing.T) {
		text := "// Header\n\nsyntax = \"proto3\";\n// Message\nmessage M {\n  // Field\n  string value = 1;\n}\n// enum\n" +
			"enum E { ZERO = 0; }\n// Service\nservice Other {\n  // Method\n  rpc Call (Req) returns (Res);\n}"
		wantDocs(t, blockDocs(blockWant(t, text, ".proto").Blocks), []bool{false, true, true, true, true, true})
	})
	t.Run("single-quoted braces do not end a proto message scope", func(t *testing.T) {
		text := "message M {\n  string value = 1 [default = '}'];\n  // Field\n  string next = 2;\n}"
		wantDocs(t, blockDocs(blockWant(t, text, ".proto").Blocks), []bool{true})
	})
	t.Run("proto blank lines detach comment blocks", func(t *testing.T) {
		text := "// header\n\nsyntax = \"proto3\";\n\n// detached\n\nmessage M {}"
		wantDocs(t, blockDocs(blockWant(t, text, ".proto").Blocks), []bool{false, false})
	})
	t.Run("comment markers inside proto strings are not comments", func(t *testing.T) {
		got := blockWant(t, "option java_package = \"example//path\";\n// note\nmessage M {}", ".proto")
		if len(got.Blocks) != 1 {
			t.Fatalf("blocks = %+v", got.Blocks)
		}
		wantDocs(t, blockDocs(got.Blocks), []bool{true})
	})
	t.Run("unknown extensions return null", func(t *testing.T) {
		if _, ok := CommentBlocks("// note", ".unknown"); ok {
			t.Fatal("unknown extension had a scanner")
		}
	})
	t.Run("cgo files are ambiguous for both import forms", func(t *testing.T) {
		for _, text := range []string{
			"package p\n/* preamble */\nimport \"C\"", "package p\nimport (\n\t\"fmt\"\n\t\"C\"\n)",
		} {
			if !IsCgo(text) {
				t.Errorf("not recognized as cgo: %q", text)
			}
			got := blockWant(t, text, ".go")
			if !got.Ambiguous {
				t.Errorf("not ambiguous: %+v", got)
			}
		}
	})
	t.Run("non-cgo imports do not classify as cgo", func(t *testing.T) {
		if IsCgo("package p\nimport \"fmt\"") || IsCgo("package p\nimport (\n\"fmt\"\n)") {
			t.Fatal("non-C import classified as cgo")
		}
	})
	t.Run("generated headers exempt all comment blocks when found in the first five lines", func(t *testing.T) {
		text := "// Code generated by tool. DO NOT EDIT.\npackage p\n// ordinary note\nconst value = 1\n// trailing note"
		wantDocs(t, blockDocs(blockWant(t, text, ".go").Blocks), []bool{true, true, true})
	})
	t.Run("@generated in the first five lines exempts separated comment runs", func(t *testing.T) {
		got := blockWant(t, "// @generated\nconst value = 1;\n// generated prose", ".ts")
		wantDocs(t, blockDocs(got.Blocks), []bool{true, true})
	})
	t.Run("a generated marker on line five exempts comment blocks", func(t *testing.T) {
		text := "const a = 1;\nconst b = 2;\nconst c = 3;\nconst d = 4;\n// @generated\n// ordinary note"
		wantDocs(t, blockDocs(blockWant(t, text, ".ts").Blocks), []bool{true})
	})
	t.Run("a generated marker after line five does not exempt comment blocks", func(t *testing.T) {
		text := "const a = 1;\nconst b = 2;\nconst c = 3;\nconst d = 4;\nconst e = 5;\n// @generated\n// ordinary note"
		wantDocs(t, blockDocs(blockWant(t, text, ".ts").Blocks), []bool{false})
	})
	t.Run("generated marker inside a code string does not exempt the file", func(t *testing.T) {
		wantDocs(t, blockDocs(blockWant(t, "const m = \"@generated\";\n// ordinary note", ".ts").Blocks), []bool{false})
	})
	t.Run("multibyte code residue does not expose generated marker text", func(t *testing.T) {
		text := "const s = \"é @generated\";\n// ordinary note"
		wantDocs(t, blockDocs(blockWant(t, text, ".ts").Blocks), []bool{false})
	})
	t.Run("Rust @generated directive exempts the file", func(t *testing.T) {
		text := "// @generated by tool\nfn value() {}\n// ordinary note"
		wantDocs(t, blockDocs(blockWant(t, text, ".rs").Blocks), []bool{true})
	})
	t.Run("Rust generated marker text inside a string is not a directive", func(t *testing.T) {
		text := "const MESSAGE: &str = \"// @generated\";\n// ordinary note"
		wantDocs(t, blockDocs(blockWant(t, text, ".rs").Blocks), []bool{false})
	})
	t.Run("CRLF YAML blocks retain their one-based source line numbers", func(t *testing.T) {
		got := blockWant(t, "script: |\r\n  # payload\r\n  text\r\n# note\r\nnext: 1", ".yaml")
		if len(got.Blocks) != 1 || got.Blocks[0].Start != 4 || got.Blocks[0].Lines != 1 {
			t.Fatalf("blocks = %+v", got.Blocks)
		}
	})
}
