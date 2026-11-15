package commentscan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/samber/mo"
)

func changesStructure(before, after string) bool {
	_, same := CompareMarkdown(before, after).(VerdictSame)
	return !same
}

func TestMarkdownStructureFingerprinting(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "rewording prose preserves structure", run: func(t *testing.T) {
			t.Helper()
			if changesStructure("# Title\n\nFirst sentence.\nIt continues.", "# Title\n\nReworded sentence.") {
				t.Fatal("prose change altered structure")
			}
		}},
		{name: "fenced code and fence info remain structural", run: func(t *testing.T) {
			t.Helper()
			if !changesStructure("```bash\necho old\n```", "```bash\necho new\n```") {
				t.Fatal("fence contents did not alter structure")
			}
			if !changesStructure("```bash\nrun()\n```", "```sh\nrun()\n```") {
				t.Fatal("fence info did not alter structure")
			}
		}},
		{name: "tilde fences preserve their fingerprint character", run: func(t *testing.T) {
			t.Helper()
			want := []MarkdownItem{{Kind: "fenced-code", Value: "~3[js]\nx"}}
			if got := MarkdownFingerprint("~~~js\nx\n~~~"); !reflect.DeepEqual(got, want) {
				t.Fatalf("fingerprint = %#v, want %#v", got, want)
			}
		}},
		{name: "different fence characters produce a mismatch", run: func(t *testing.T) {
			t.Helper()
			got, ok := CompareMarkdown("~~~\nx\n~~~", "```\nx\n```").(VerdictMismatch)
			if !ok {
				t.Fatalf("got %T, want VerdictMismatch", CompareMarkdown("~~~\nx\n~~~", "```\nx\n```"))
			}
			before, hasBefore := got.Before.Get()
			after, hasAfter := got.After.Get()
			if got.Index != 0 || !hasBefore || !hasAfter ||
				before != (MarkdownItem{Kind: "fenced-code", Value: "~3[]\nx"}) ||
				after != (MarkdownItem{Kind: "fenced-code", Value: "`3[]\nx"}) {
				t.Fatalf("got %#v", got)
			}
		}},
		{name: "fence content is not parsed as headings", run: func(t *testing.T) {
			t.Helper()
			before := "Intro prose.\n\n```py\n# payload\nx = 1\n```"
			if hasMarkdownKind(MarkdownFingerprint(before), "heading") {
				t.Fatal("fence content parsed as heading")
			}
			if changesStructure(before, "Intro changed.\n\n```py\n# payload\nx = 1\n```") {
				t.Fatal("prose edit altered structure")
			}
		}},
		{name: "editing a hash line inside a fence changes structure", run: func(t *testing.T) {
			t.Helper()
			if !changesStructure("```py\n# keep me\n```", "```py\n# changed\n```") {
				t.Fatal("fence content did not alter structure")
			}
		}},
		{name: "heading text and level are structural", run: func(t *testing.T) {
			t.Helper()
			if !changesStructure("## Install steps", "## Setup steps") || !changesStructure("## Install", "### Install") {
				t.Fatal("heading edit did not alter structure")
			}
		}},
		{name: "link destinations are structural but link text is prose", run: func(t *testing.T) {
			t.Helper()
			if !changesStructure("See [docs](./old.md).", "See [docs](./new.md).") {
				t.Fatal("destination edit was missed")
			}
			if changesStructure("See [docs](./x.md).", "See [guide](./x.md).") {
				t.Fatal("link text edit altered structure")
			}
		}},
		{name: "inline code spans and rumdl directives are structural", run: func(t *testing.T) {
			t.Helper()
			if !changesStructure("Run `oldCmd` now.", "Run `newCmd` now.") {
				t.Fatal("code span edit was missed")
			}
			if !changesStructure("<!-- rumdl-disable MD013 -->", "<!-- rumdl-disable MD033 -->") {
				t.Fatal("directive edit was missed")
			}
		}},
		{name: "ordinary HTML comments are prose", run: func(t *testing.T) {
			t.Helper()
			if changesStructure("<!-- note -->\n\nprose", "<!-- another note -->\n\nreworded prose") {
				t.Fatal("ordinary comment altered structure")
			}
		}},
		{name: "front matter and indented code are structural", run: func(t *testing.T) {
			t.Helper()
			if !changesStructure("---\ntitle: A\n---\nbody", "---\ntitle: B\n---\nreworded") {
				t.Fatal("front matter edit was missed")
			}
			if !changesStructure("prose\n\n    code one", "prose\n\n    code two") {
				t.Fatal("indented code edit was missed")
			}
		}},
		{name: "reference destinations and autolinks are structural", run: func(t *testing.T) {
			t.Helper()
			if !changesStructure("[tool][t]\n\n[t]: https://a.example", "[tool][t]\n\n[t]: https://b.example") {
				t.Fatal("reference edit was missed")
			}
			if !changesStructure("<https://a.example>", "<https://b.example>") {
				t.Fatal("autolink edit was missed")
			}
		}},
		{name: "tables and Setext heading levels are structural", run: func(t *testing.T) {
			t.Helper()
			if !changesStructure("| A | B |\n| --- | --- |", "| X | B |\n| --- | --- |") {
				t.Fatal("table header edit was missed")
			}
			if !changesStructure("Title\n====", "Title\n----") {
				t.Fatal("Setext level edit was missed")
			}
		}},
		{name: "comparison reports the changed heading entries", run: func(t *testing.T) {
			t.Helper()
			got, ok := CompareMarkdown("## Install", "## Setup").(VerdictMismatch)
			if !ok {
				t.Fatalf("got %T, want VerdictMismatch", CompareMarkdown("## Install", "## Setup"))
			}
			before, hasBefore := got.Before.Get()
			after, hasAfter := got.After.Get()
			if got.Index != 0 || !hasBefore || !hasAfter ||
				before != (MarkdownItem{Kind: "heading", Value: "H2 Install"}) ||
				after != (MarkdownItem{Kind: "heading", Value: "H2 Setup"}) {
				t.Fatalf("got %#v", got)
			}
		}},
		{name: "an unclosed comment is malformed", run: func(t *testing.T) {
			t.Helper()
			if got := ScanMarkdown("<!-- unfinished").Defect; got != "an HTML comment opened and never closed" {
				t.Fatalf("got %q", got)
			}
		}},
		{name: "unclosed front matter and fences are malformed", run: func(t *testing.T) {
			t.Helper()
			if got := ScanMarkdown("---\ntitle: A").Defect; got != "front matter opened with `---` and never closed" {
				t.Fatalf("got %q", got)
			}
			if got := ScanMarkdown("```\ncode").Defect; got != "a fenced code block opened and never closed" {
				t.Fatalf("got %q", got)
			}
		}},
		{name: "a malformed side is reported before structural comparison", run: func(t *testing.T) {
			t.Helper()
			got, ok := CompareMarkdown("<!-- unfinished", "<!-- changed").(VerdictMalformed)
			if !ok || got.Side != "before" {
				t.Fatalf("got %#v", got)
			}
		}},
		{name: "bare URLs and raw HTML attributes are structural", run: func(t *testing.T) {
			t.Helper()
			if !changesStructure("see https://a.example/x here", "see https://b.example/x here") {
				t.Fatal("bare URL edit was missed")
			}
			if !changesStructure(`<a href="/a">x</a>`, `<a href="/b">x</a>`) {
				t.Fatal("HTML attribute edit was missed")
			}
		}},
		{name: "bare URLs stop at JavaScript nonbreaking whitespace", run: func(t *testing.T) {
			t.Helper()
			want := []MarkdownItem{{Kind: "autolink", Value: "https://a.example/x"}}
			if got := MarkdownFingerprint("https://a.example/x\u00a0y"); !reflect.DeepEqual(got, want) {
				t.Fatalf("fingerprint = %#v, want %#v", got, want)
			}
			if _, ok := CompareMarkdown("https://a.example/x\u00a0y", "https://a.example/x\u00a0q").(VerdictSame); !ok {
				t.Fatal("nonbreaking-space suffix changed the URL fingerprint")
			}
		}},
		{name: "thematic markers accept JavaScript nonbreaking whitespace", run: func(t *testing.T) {
			t.Helper()
			want := []MarkdownItem{{Kind: "block-marker", Value: "thematic:* *\u00a0*"}}
			if got := MarkdownFingerprint("* *\u00a0*"); !reflect.DeepEqual(got, want) {
				t.Fatalf("fingerprint = %#v, want %#v", got, want)
			}
			got, ok := CompareMarkdown("* *\u00a0*", "text").(VerdictMismatch)
			if !ok {
				t.Fatalf("got %T, want VerdictMismatch", CompareMarkdown("* *\u00a0*", "text"))
			}
			before, hasBefore := got.Before.Get()
			if got.Index != 0 || !hasBefore || !got.After.IsAbsent() || before != want[0] {
				t.Fatalf("got %#v", got)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func hasMarkdownKind(items []MarkdownItem, kind string) bool {
	for _, item := range items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}

type markdownGolden struct {
	Revision    string         `json:"revision"`
	Path        string         `json:"path"`
	Corpus      string         `json:"corpus"`
	Fingerprint []MarkdownItem `json:"fingerprint"`
	Comparison  struct {
		Kind string `json:"kind"`
	} `json:"comparison"`
}

func TestMarkdownGoldens(t *testing.T) {
	goldens, err := filepath.Glob("testdata/ts-golden/markdown/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(goldens) == 0 {
		t.Fatal("no Markdown goldens found")
	}
	for _, path := range goldens {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var golden markdownGolden
			err = json.Unmarshal(data, &golden)
			if err != nil {
				t.Fatal(err)
			}
			text, err := os.ReadFile(filepath.Join("testdata/corpus/markdown", filepath.Base(golden.Corpus)))
			if err != nil {
				t.Fatal(err)
			}
			if got := MarkdownFingerprint(string(text)); !reflect.DeepEqual(got, golden.Fingerprint) {
				limit := min(len(got), len(golden.Fingerprint))
				for i := range limit {
					if got[i] != golden.Fingerprint[i] {
						t.Fatalf("fingerprint differs at %d: Go %#v, TS %#v", i, got[i], golden.Fingerprint[i])
					}
				}
				t.Fatalf("fingerprint lengths differ: Go %d, TS %d", len(got), len(golden.Fingerprint))
			}
			if verdict := CompareMarkdown(string(text), string(text)); golden.Comparison.Kind != "same" {
				t.Fatalf("TS self-comparison kind %q, got %T", golden.Comparison.Kind, verdict)
			} else if _, ok := verdict.(VerdictSame); !ok {
				t.Fatalf("self-comparison = %T, want VerdictSame", verdict)
			}
		})
	}
}

var _ mo.Option[MarkdownItem]
