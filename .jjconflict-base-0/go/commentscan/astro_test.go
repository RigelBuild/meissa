// SPDX-License-Identifier: MIT OR Apache-2.0

package commentscan

import (
	"reflect"
	"strings"
	"testing"
)

const astroTestPage = `---
// a note above the title
const title = "Home";
---
<!-- a note about the header -->
<h1>{title}</h1>
<p>see http://example.com/a//b for details</p>
{/* a JSX-style note */}
<style>
  /* a note about the rule */
  h1 { color: red; }
</style>
<script>
  // a note about the handler
  document.querySelector("h1");
</script>`

func TestSegmentAstro(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []LanguageID
		ok    bool
	}{
		{"splits frontmatter, template and islands", astroTestPage,
			[]LanguageID{"html", "ts", "html", "html", "css", "html", "ts", "html"}, true},
		{"ambiguous island body retains segments",
			"<style>\n/* unfinished\n</style>\n<p>after</p>", []LanguageID{"html", "css", "html"}, true},
		{"ambiguous template retains its segment",
			"<p>x</p>\n<!-- unfinished", []LanguageID{"html"}, true},
		{"literal brace in JSX prose from the report",
			"<p>Use '{' to open <b>a block</b>.</p>", nil, false},
		{"an island tag sharing a line with body text is unmodelled",
			"<script>alert(1)\n</script>", nil, false},
		{"an unclosed frontmatter fence is unmodelled", "---\nconst value = 1;", nil, false},
		{"one-line empty islands are template text",
			"<script is:inline set:html={json}></script>", []LanguageID{"html"}, true},
		{"script mentions in HTML comments are prose",
			"<!-- <script> example -->\n<p>after</p>", []LanguageID{"html"}, true},
		{"an unclosed island is unmodelled", "<style>\na { color: red; }", nil, false},
		{"script tags inside attribute strings do not open islands",
			`<p data-tpl="<script>alert(1)</script>">x</p>` + "\n<!-- note -->\n<p>after</p>",
			[]LanguageID{"html"}, true},
		{"an opening tag with a trailing comment still opens its island",
			"<script> <!-- note -->\n// comment\nrun();\n</script>",
			[]LanguageID{"html", "ts", "html"}, true},
		{"a live open tag with trailing body text is unmodelled",
			"<div><script>alert(1)\n</script></div>", nil, false},
		{"multiline markup less-than is not an opening tag",
			"{items.map(i => (\n  1 <2 && i\n))} // c", []LanguageID{"html"}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := SegmentAstro(test.input)
			if ok != test.ok {
				t.Fatalf("SegmentAstro ok = %t, want %t", ok, test.ok)
			}
			if test.want == nil {
				if got != nil {
					t.Fatalf("segments = %#v, want nil", got)
				}
				return
			}
			langs := make([]LanguageID, len(got))
			for i := range got {
				langs[i] = got[i].Lang.ID
			}
			if !reflect.DeepEqual(langs, test.want) {
				t.Fatalf("segment languages = %v, want %v", langs, test.want)
			}
		})
	}
}

func TestSegmentAstroPreservesTSLines(t *testing.T) {
	const input = "---\r\nconst a = 1;\r\n---\r\n<p>x</p>\r\n"
	segments, ok := SegmentAstro(input)
	if !ok {
		t.Fatal("SegmentAstro returned false for CRLF input")
	}
	want := []Segment{
		{Lang: langByID()[LanguageHTML], StartLine: 0, Lines: []string{"---\r"}},
		{Lang: langByID()[LanguageTS], StartLine: 1, Lines: []string{"const a = 1;\r"}},
		{Lang: langByID()[LanguageHTML], StartLine: 2, Lines: []string{"---\r"}},
		{Lang: langByID()[LanguageHTML], StartLine: 3, Lines: []string{"<p>x</p>\r", ""}},
	}
	if !reflect.DeepEqual(segments, want) {
		t.Fatalf("SegmentAstro() = %#v, want %#v", segments, want)
	}
}

func TestOneLineIsland(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "matching script tags", input: "<script>x</script>", want: true},
		{name: "matching style tags", input: "<style type=\"text/css\">x</style>", want: true},
		{name: "JavaScript whitespace before attributes", input: "<script\u00a0is:inline>x</script>", want: true},
		{name: "mismatched tag names", input: "<script></style>", want: false},
		{name: "tag name suffix", input: "<scriptx></script>", want: false},
		{name: "attributes require whitespace", input: "<script=x></script>", want: false},
		{name: "body cannot contain less-than", input: "<script><i></script>", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := oneLineIsland(test.input); got != test.want {
				t.Fatalf("oneLineIsland(%q) = %t, want %t", test.input, got, test.want)
			}
		})
	}
}

func TestAstroReportProbeInputs(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		residues     []string
		ambiguous    bool
		segmentLangs []LanguageID
		segmentsOK   bool
	}{
		{
			name:         "markup prose brace",
			input:        "<p>Use '{' to open <b>a block</b>.</p>",
			residues:     []string{"<p>Use '{' to open <b>a block</b>.</p>"},
			ambiguous:    true,
			segmentLangs: nil,
			segmentsOK:   false,
		},
		{
			name:         "code string brace",
			input:        "<code>\"{\"</code>",
			residues:     []string{"<code>\"{\"</code>"},
			ambiguous:    true,
			segmentLangs: nil,
			segmentsOK:   false,
		},
		{
			name:         "title brace and open tag",
			input:        "<p title=\"{\">x <b>",
			residues:     []string{"<p title=\"{\">x <b>"},
			ambiguous:    true,
			segmentLangs: nil,
			segmentsOK:   false,
		},
		{
			name:         "less-than comparison on continuation",
			input:        "{a\n  < 3 ? b : c} // x",
			residues:     []string{"{a", "  < 3 ? b : c} // x"},
			ambiguous:    false,
			segmentLangs: []LanguageID{LanguageHTML},
			segmentsOK:   true,
		},
		{
			name:         "less-than comparison in multiline arrow expression",
			input:        "{items.map(i => (\n  1 <2 && i\n))} // c",
			residues:     []string{"{items.map(i => (", "  1 <2 && i", "))} // c"},
			ambiguous:    false,
			segmentLangs: []LanguageID{LanguageHTML},
			segmentsOK:   true,
		},
		{
			name:         "mismatched island tags",
			input:        "<script></style>\n<p>x</p>",
			residues:     []string{"<script></style>", "<p>x</p>"},
			ambiguous:    true,
			segmentLangs: nil,
			segmentsOK:   false,
		},
		{
			name:         "Unicode whitespace island",
			input:        "<script\u00a0is:inline>x</script>",
			residues:     []string{"<script\u00a0is:inline>x</script>"},
			segmentLangs: []LanguageID{LanguageHTML},
			ambiguous:    false,
			segmentsOK:   true,
		},
		{
			name:         "ambiguous island retains segments",
			input:        "<style>\n/* unfinished\n</style>\n<p>after</p>",
			residues:     []string{"<style>", "/* unfinished", "</style>", "<p>after</p>"},
			ambiguous:    true,
			segmentLangs: []LanguageID{LanguageHTML, LanguageCSS, LanguageHTML},
			segmentsOK:   true,
		},
		{
			name:         "ambiguous template retains its segment",
			input:        "<p>x</p>\n<!-- unfinished",
			residues:     []string{"<p>x</p>", "<!-- unfinished"},
			ambiguous:    true,
			segmentLangs: []LanguageID{LanguageHTML},
			segmentsOK:   true,
		},
		{
			name:         "CRLF lines remain in segments",
			input:        "---\r\nconst a = 1;\r\n---\r\n<p>x</p>\r\n",
			residues:     []string{"---", "const a = 1;", "---", "<p>x</p>", ""},
			segmentLangs: []LanguageID{LanguageHTML, LanguageTS, LanguageHTML, LanguageHTML},
			ambiguous:    false,
			segmentsOK:   true,
		},
		{
			name:         "unterminated script string matches TypeScript",
			input:        "<script>\nconst s = \"x\n</script>",
			residues:     []string{"<script>", "const s = \"x", "</script>"},
			segmentLangs: []LanguageID{LanguageHTML, LanguageTS, LanguageHTML},
			ambiguous:    false,
			segmentsOK:   true,
		},
		{
			name:         "unterminated frontmatter string matches TypeScript",
			input:        "---\nconst a = \"x\n---\n<p/>\n",
			residues:     []string{"---", "const a = \"x", "---", "<p/>", ""},
			segmentLangs: []LanguageID{LanguageHTML, LanguageTS, LanguageHTML, LanguageHTML},
			ambiguous:    false,
			segmentsOK:   true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ScanAstro(test.input)
			if got.Ambiguous != test.ambiguous || !reflect.DeepEqual(got.Residues, test.residues) {
				t.Fatalf("ScanAstro() = %+v, want residues %#v ambiguous %t", got, test.residues, test.ambiguous)
			}
			segments, ok := SegmentAstro(test.input)
			if ok != test.segmentsOK {
				t.Fatalf("SegmentAstro ok = %t, want %t", ok, test.segmentsOK)
			}
			if test.segmentLangs == nil {
				if segments != nil {
					t.Fatalf("SegmentAstro segments = %#v, want nil", segments)
				}
				return
			}
			langs := make([]LanguageID, len(segments))
			for i := range segments {
				langs[i] = segments[i].Lang.ID
			}
			if !reflect.DeepEqual(langs, test.segmentLangs) {
				t.Fatalf("segment languages = %v, want %v", langs, test.segmentLangs)
			}
		})
	}
}

func TestScanAstro(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      []string
		ambiguous bool
	}{
		{"strips comments while retaining code in every region", astroTestPage,
			[]string{"---", "", `const title = "Home";`, "---", "",
				"<h1>{title}</h1>", "<p>see http://example.com/a//b for details</p>", "{}",
				"<style>", "", "  h1 { color: red; }", "</style>", "<script>", "",
				`  document.querySelector("h1");`, "</script>"}, false},
		{"a template ending inside an HTML comment is ambiguous",
			"<p>x</p>\n<!-- unfinished", []string{"<p>x</p>", "<!-- unfinished"}, true},
		{"an island body left mid-construct is ambiguous",
			"<style>\n/* unfinished\n</style>\n<p>after</p>",
			[]string{"<style>", "/* unfinished", "</style>", "<p>after</p>"}, true},
		{"markup expressions preserve JSX text",
			strings.Join([]string{
				"---", "const items = [];", "---", "<ul>",
				"{items.map((item) => <a href={item.url}>see http://x//y</a>)}",
				"</ul>", "<p>after</p>",
			}, "\n"),
			[]string{"---", "const items = [];", "---", "<ul>",
				"{items.map((item) => <a href={item.url}>see http://x//y</a>)}",
				"</ul>", "<p>after</p>"}, false},
		{"multiline markup expressions preserve source line count",
			strings.Join([]string{
				"---", "const items = [];", "---", "<ul>", "  {items.map((item) => (",
				"    <a href={item.url}>see http://x//y for details</a>", "  ))}", "</ul>", "<p>after</p>",
			}, "\n"),
			[]string{"---", "const items = [];", "---", "<ul>", "  {items.map((item) => (",
				"    <a href={item.url}>see http://x//y for details</a>", "  ))}", "</ul>", "<p>after</p>"}, false},
		{"comments inside multiline markup expressions are stripped",
			"<ul>\n  {items.map((item) => (\n    /* note */\n    <a href={item.url}>x//y</a>\n  ))}\n</ul>",
			[]string{"<ul>", "  {items.map((item) => (", "", "    <a href={item.url}>x//y</a>", "  ))}", "</ul>"}, false},
		{"malformed multiline expressions are ambiguous",
			"<ul>\n  {items.map((item) => (\n    <a>x</a>",
			[]string{"<ul>", "  {items.map((item) => (", "    <a>x</a>"}, true},
		{"template text after a markup expression's closing brace stays template",
			strings.Join([]string{
				"<p>", "\t{url ? (", "\t\t<a href={url}>{label}</a>", "\t) : (", "\t\tlabel",
				"\t)}, or email // not a comment", `	<a href="mailto:x">x</a>.`, "</p>",
			}, "\n"),
			[]string{"<p>", "\t{url ? (", "\t\t<a href={url}>{label}</a>", "\t) : (", "\t\tlabel",
				"\t)}, or email // not a comment", `	<a href="mailto:x">x</a>.`, "</p>"}, false},
		{"plain brace expressions use the lexical scanner",
			"<p>{title}</p>\n<p>after</p>", []string{"<p>{title}</p>", "<p>after</p>"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ScanAstro(test.input)
			if got.Ambiguous != test.ambiguous || !reflect.DeepEqual(got.Residues, test.want) {
				t.Fatalf("ScanAstro() = %+v, want %#v ambiguous %t", got, test.want, test.ambiguous)
			}
		})
	}
}

func TestScanAstroTSParityCases(t *testing.T) {
	t.Run("// in template prose is not a comment", func(t *testing.T) {
		got := ScanAstro(astroTestPage)
		if got.Ambiguous || got.Residues[6] != "<p>see http://example.com/a//b for details</p>" {
			t.Fatalf("ScanAstro() = %+v", got)
		}
	})
	t.Run("frontmatter fences and island tags remain in residue", func(t *testing.T) {
		got := ScanAstro(astroTestPage)
		for line, want := range map[int]string{
			0:  "---",
			3:  "---",
			8:  "<style>",
			11: "</style>",
			12: "<script>",
			15: "</script>",
		} {
			if got.Residues[line] != want {
				t.Errorf("residue[%d] = %q, want %q", line, got.Residues[line], want)
			}
		}
	})
	t.Run("an unmodelled shape is ambiguous", func(t *testing.T) {
		if !ScanAstro("<script>alert(1)\n</script>").Ambiguous {
			t.Fatal("unmodelled island shape is not ambiguous")
		}
	})
	t.Run("tag-like text and braces in strings do not misroute markup", func(t *testing.T) {
		if got := ScanAstro("<p>{`literal <span> and { braces`}</p>"); got.Ambiguous {
			t.Fatalf("tag-like string was ambiguous: %+v", got)
		}
	})
	t.Run("a comment inside an expression with trailing text is still stripped", func(t *testing.T) {
		got := ScanAstro("<p>\n{<a>x</a> /* note */}, or\n</p>")
		if got.Ambiguous || got.Residues[1] != "{<a>x</a> }, or" {
			t.Fatalf("ScanAstro() = %+v", got)
		}
	})
	t.Run("a later expression in the template tail stays template", func(t *testing.T) {
		got := ScanAstro("<p>\n{<a>x</a>}, or email {name}\n</p>")
		if got.Ambiguous || got.Residues[1] != "{<a>x</a>}, or email {name}" {
			t.Fatalf("ScanAstro() = %+v", got)
		}
	})
}

func TestScanAstroCRLFInput(t *testing.T) {
	t.Run("matches LF residues and line count", func(t *testing.T) {
		crlf := ScanAstro(strings.ReplaceAll(astroTestPage, "\n", "\r\n"))
		lf := ScanAstro(astroTestPage)
		if crlf.Ambiguous != lf.Ambiguous || !reflect.DeepEqual(crlf.Residues, lf.Residues) {
			t.Fatalf("CRLF scan = %+v, LF scan = %+v", crlf, lf)
		}
		if len(crlf.Residues) != len(strings.Split(astroTestPage, "\n")) {
			t.Fatalf("CRLF residue line count = %d", len(crlf.Residues))
		}
	})
}
