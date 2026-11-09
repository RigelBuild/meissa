package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

const testPath = "docs/designs/decisions/meta/DL-297.md"

const good = "---\n" +
	"id: DL-297\n" +
	"decision: \"Exactly two docs repos | one public\"\n" +
	"status: \"Active (Matt, 2026-10-07)\"\n" +
	"record: ../../meta/public-docs-repo.md#approach\n" +
	"---\n\nOptional rationale body.\n"

func parseForTest(t *testing.T, src []byte, filePath string) DecisionRow {
	t.Helper()
	row, err := ParseDecisionFile(filePath, src)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func replaceGood(from, to string) string {
	return strings.Replace(good, from, to, 1)
}

func reorderedKeys() string {
	keyOrder := regexp.MustCompile(`(decision: .*?)\n(status: .*)`)
	return keyOrder.ReplaceAllString(good, "$2\n$1")
}

var rejectionInputs = map[string]string{
	"content before the block":                "x\n" + good,
	"an unclosed block":                       strings.Replace(good, "\n---\n\nOptional", "\n\nOptional", 1),
	"an unterminated quote":                   replaceGood(`"Exactly two docs repos | one public"`, `"unterminated`),
	"a YAML-only \\x escape":                  replaceGood("Exactly two", `\x41`),
	"a literal tab":                           replaceGood("Exactly two", "a\tb"),
	"a decoded newline":                       replaceGood("Exactly two", `a\nb`),
	"a decoded line separator":                replaceGood("Exactly two", `a\u2028b`),
	"an anchored record":                      replaceGood("record: ", "record: &a "),
	"lone surrogate":                          replaceGood("Exactly two", `\uD800`),
	"a tagged id":                             replaceGood("id: DL-297", "id: !!str DL-297"),
	"a trailing comment on the id":            replaceGood("id: DL-297", "id: DL-297 # c"),
	"a duplicate key":                         replaceGood("record:", "id: DL-297\nrecord:"),
	"an extra key":                            replaceGood("record:", "owner: x\nrecord:"),
	"a missing key":                           strings.Replace(good, "status: \"Active (Matt, 2026-10-07)\"\n", "", 1),
	"keys out of order":                       reorderedKeys(),
	"an unquoted decision":                    replaceGood(`"Exactly two docs repos | one public"`, "Exactly two"),
	"a single-quoted status":                  replaceGood(`"Active (Matt, 2026-10-07)"`, "'Active (Matt, 2026-10-07)'"),
	"a quoted id":                             replaceGood("id: DL-297", `id: "DL-297"`),
	"a width-drifted id":                      replaceGood("id: DL-297", "id: DL-0297"),
	"an id that disagrees with the file name": replaceGood("id: DL-297", "id: DL-298"),
	"an empty decision":                       replaceGood(`"Exactly two docs repos | one public"`, `"  "`),
	"a status without provenance":             replaceGood(`"Active (Matt, 2026-10-07)"`, `"Active"`),
	"a superseded target of the wrong width":  replaceGood("Active (Matt", "Superseded by DL-30 (Matt"),
	"a record that is not markdown":           replaceGood("public-docs-repo.md", "public-docs-repo.txt"),
	"an absolute record":                      replaceGood("../../meta/public-docs-repo.md", "/docs/x.md"),
	"a URL record":                            replaceGood("../../meta/public-docs-repo.md", "https://x/y.md"),
	"a non-slug anchor":                       replaceGood("#approach", "#Approach Two"),
	"an uppercase anchor":                     replaceGood("#approach", "#Approach"),
	"a record with a pipe":                    replaceGood("public-docs-repo", "a|b"),
	"a record with a paren":                   replaceGood("public-docs-repo", "a)b"),
	"a record with a space":                   replaceGood("public-docs-repo", "a b"),
	"a record with a query":                   replaceGood("../../meta/public-docs-repo", "?a"),
	"a nameless record":                       replaceGood("../../meta/public-docs-repo.md", ".md"),
	"a record above the repo root":            replaceGood("../../", "../../../../../../"),
}

func TestParseDecisionFile(t *testing.T) {
	got := parseForTest(t, []byte(good), testPath)
	want := DecisionRow{
		Path:         testPath,
		Line:         2,
		ID:           "DL-297",
		Decision:     "Exactly two docs repos | one public",
		Status:       "Active (Matt, 2026-10-07)",
		RecordRaw:    "../../meta/public-docs-repo.md#approach",
		RecordPath:   "docs/designs/meta/public-docs-repo.md",
		RecordAnchor: "approach",
	}
	if got != want {
		t.Fatalf("ParseDecisionFile() = %#v, want %#v", got, want)
	}
}

func TestParseDecisionFileLargeID(t *testing.T) {
	filePath := "docs/designs/decisions/meta/DL-1000.md"
	text := replaceGood("id: DL-297", "id: DL-1000")
	text = strings.Replace(text, "Active (Matt, 2026-10-07)", "Superseded by DL-1001 (Matt, 2026-10-07)", 1)
	got := parseForTest(t, []byte(text), filePath)
	if got.ID != "DL-1000" || got.Status != "Superseded by DL-1001 (Matt, 2026-10-07)" {
		t.Fatalf("ParseDecisionFile() ID/status = %q/%q", got.ID, got.Status)
	}
}

func TestParseDecisionFileBOMEscapesAndNoAnchor(t *testing.T) {
	text := replaceGood(`"Exactly two docs repos | one public"`, `"say \"two\""`)
	text = strings.Replace(text, "#approach", "", 1)
	got := parseForTest(t, []byte("\ufeff"+text), testPath)
	if got.Decision != `say "two"` || got.RecordAnchor != "" {
		t.Fatalf("ParseDecisionFile() decision/anchor = %q/%q", got.Decision, got.RecordAnchor)
	}
}

func TestParseDecisionFileNonASCIIAnchor(t *testing.T) {
	got := parseForTest(t, []byte(replaceGood("#approach", "#décision-2026")), testPath)
	if got.RecordAnchor != "décision-2026" {
		t.Fatalf("RecordAnchor = %q, want décision-2026", got.RecordAnchor)
	}
}

func TestParseDecisionFileStatusVariants(t *testing.T) {
	for _, test := range []struct {
		name string
		from string
		want string
	}{
		{name: "superseded", from: "Active (Matt", want: "Superseded by DL-300 (Matt"},
		{name: "retired", from: "Active (Matt", want: "Retired (Matt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := parseForTest(t, []byte(replaceGood(test.from, test.want)), testPath)
			if !strings.HasPrefix(got.Status, strings.Split(test.want, " (")[0]) {
				t.Fatalf("Status = %q, want prefix %q", got.Status, test.want)
			}
		})
	}
}

func TestParseDecisionFileTSReasonGoldens(t *testing.T) {
	for _, test := range readReasons(t).Fixtures {
		t.Run(test.Name, func(t *testing.T) {
			text := rejectionInputs[test.Name]
			if text == "" {
				t.Fatalf("no rejection input for %q", test.Name)
			}
			_, err := ParseDecisionFile(test.Path, []byte(text))
			var malformed *MalformedDecision
			if !errors.As(err, &malformed) {
				t.Fatalf("ParseDecisionFile() error = %v, want *MalformedDecision", err)
			}
			if malformed.Path != test.Path || malformed.Line != test.Line || !reasonMatches(malformed.Reason, test.Reason) {
				t.Fatalf("MalformedDecision = %#v, want path=%q line=%d reason=%q", malformed, test.Path, test.Line, test.Reason)
			}
		})
	}
}

func TestParseDecisionFileCRLF(t *testing.T) {
	lf := parseForTest(t, []byte(good), testPath)
	crlf := parseForTest(t, []byte(strings.ReplaceAll(good, "\n", "\r\n")), testPath)
	if crlf != lf {
		t.Fatalf("CRLF row = %#v, LF row = %#v", crlf, lf)
	}
}

// The TS oracle (Bun) rejects each of these at the key line, because JS `.` stops at them.
func TestParseDecisionFileJSLineTerminatorInKeyLine(t *testing.T) {
	for name, sep := range map[string]string{"CR": "\r", "U+2028": "\u2028", "U+2029": "\u2029"} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseDecisionFile(testPath, []byte(replaceGood("id: DL-297", "id: DL-297"+sep+"x")))
			var malformed *MalformedDecision
			if !errors.As(err, &malformed) || malformed.Line != 2 || malformed.Reason != "expected key `id` at line 2" {
				t.Fatalf("ParseDecisionFile() error = %v, want line 2 expected key `id`", err)
			}
		})
	}
}

func TestParseDecisionFileLoneSurrogate(t *testing.T) {
	golden := readGolden[fixtureIndexGolden](t, "fixture-index.json")
	var got string
	err := json.Unmarshal([]byte(`"`+golden.LoneSurrogate+`"`), &got)
	if err != nil {
		t.Fatal(err)
	}
	if got != "�" {
		t.Errorf("lone-surrogate JSON decoding = %q, want replacement character", got)
	}
	text := replaceGood("Exactly two", `\uD800`)
	_, err = ParseDecisionFile(testPath, []byte(text))
	var malformed *MalformedDecision
	if !errors.As(err, &malformed) || !strings.HasPrefix(malformed.Reason, "malformed YAML:") {
		t.Fatalf("ParseDecisionFile() error = %v, want YAML error after JSON surrogate decode", err)
	}
}

func TestParseDecisionFileMalformedYAMLPrefixGolden(t *testing.T) {
	var want string
	for _, golden := range readReasons(t).Fixtures {
		if golden.Name == "lone surrogate" {
			want = golden.Reason
			break
		}
	}
	if want != "malformed YAML:" {
		t.Fatalf("YAML reason golden = %q, want malformed YAML prefix", want)
	}
	text := replaceGood("Exactly two", `\uD800`)
	_, err := ParseDecisionFile(testPath, []byte(text))
	var malformed *MalformedDecision
	if !errors.As(err, &malformed) || !strings.HasPrefix(malformed.Reason, want) {
		t.Fatalf("ParseDecisionFile() error = %v, want YAML prefix %q", err, want)
	}
}

func parseTextArchive(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	for len(archive) > 0 {
		if !bytes.HasPrefix(archive, []byte("-- ")) {
			t.Fatalf("invalid txtar header near %q", archive[:min(len(archive), 32)])
		}
		headerEnd := bytes.IndexByte(archive, '\n')
		if headerEnd < 0 || headerEnd < 6 || !bytes.HasSuffix(archive[:headerEnd], []byte(" --")) {
			t.Fatal("invalid txtar member header")
		}
		name := string(archive[3 : headerEnd-3])
		archive = archive[headerEnd+1:]
		nextHeader := bytes.Index(archive, []byte("\n-- "))
		if nextHeader < 0 {
			files[name] = bytes.TrimSuffix(archive, []byte("\n"))
			break
		}
		files[name] = archive[:nextHeader]
		archive = archive[nextHeader+1:]
	}
	return files
}

type reasonCase struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

type reasonGolden struct {
	Fixtures []reasonCase `json:"fixtures"`
}

type fixtureIndexGolden struct {
	Index         string `json:"index"`
	BackslashPipe string `json:"backslashPipe"`
	CodeSpan      string `json:"codeSpan"`
	LoneSurrogate string `json:"loneSurrogate"`
}

type docsIndexGolden struct {
	Files int    `json:"files"`
	Rows  int    `json:"rows"`
	Index string `json:"index"`
}

type corpusFile struct {
	Path   string `json:"path"`
	Corpus string `json:"corpus"`
}

type corpusManifest struct {
	Files []corpusFile `json:"files"`
}

func readReasons(t *testing.T) reasonGolden {
	t.Helper()
	return readGolden[reasonGolden](t, "reasons.json")
}

func reasonMatches(got, want string) bool {
	return want == "malformed YAML:" && strings.HasPrefix(got, want) || got == want
}

func readGolden[T any](t *testing.T, file string) T {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "ts-golden", file))
	if err != nil {
		t.Fatal(err)
	}
	var golden T
	err = json.Unmarshal(data, &golden)
	if err != nil {
		t.Fatalf("decode %s: %v", file, err)
	}
	return golden
}

func TestRenderDecisionIndex(t *testing.T) {
	row := func(id, decision string) DecisionRow {
		text := replaceGood("id: DL-297", "id: "+id)
		got := parseForTest(t, []byte(text), "docs/designs/decisions/meta/"+id+".md")
		got.Decision = decision
		return got
	}
	t.Run("a backslash before a pipe stays inside its cell", func(t *testing.T) {
		cells := renderCells(t, RenderDecisionIndex([]DecisionRow{row("DL-297", `a \| b`)}))
		if len(cells) != 4 || cells[1] != `a \| b` {
			t.Fatalf("rendered cells = %#v, want four cells and escaped pipe", cells)
		}
	})
	t.Run("a backslash inside a code span is not doubled", func(t *testing.T) {
		cells := renderCells(t, RenderDecisionIndex([]DecisionRow{row("DL-297", "use `C:\\tmp` path")}))
		if len(cells) != 4 || cells[1] != `use <code>C:\tmp</code> path` {
			t.Fatalf("rendered cells = %#v, want code span with one backslash", cells)
		}
	})
	rows := []DecisionRow{row("DL-297", "a | b"), row("DL-030", "d DL-030"), row("DL-100", "d DL-100")}
	want := "| ID | Decision | Status | Record |\n|---|---|---|---|\n" +
		"| [DL-030](DL-030.md) | d DL-030 | Active (Matt, 2026-10-07) | " +
		"[../../meta/public-docs-repo.md#approach](../../meta/public-docs-repo.md#approach) |\n" +
		"| [DL-100](DL-100.md) | d DL-100 | Active (Matt, 2026-10-07) | " +
		"[../../meta/public-docs-repo.md#approach](../../meta/public-docs-repo.md#approach) |\n" +
		"| [DL-297](DL-297.md) | a \\| b | Active (Matt, 2026-10-07) | " +
		"[../../meta/public-docs-repo.md#approach](../../meta/public-docs-repo.md#approach) |\n"
	if got := RenderDecisionIndex(rows); got != want {
		t.Errorf("RenderDecisionIndex() = %q, want %q", got, want)
	}
	reversed := RenderDecisionIndex([]DecisionRow{rows[2], rows[1], rows[0]})
	if got := RenderDecisionIndex(rows); got != reversed {
		t.Errorf("RenderDecisionIndex() depends on input order:\n%s\n%s", got, reversed)
	}
	late := row("DL-001", "d DL-001")
	late.Path = "docs/designs/decisions/zeta/DL-001.md"
	early := row("DL-002", "d DL-002")
	early.Path = "docs/designs/decisions/alpha/DL-002.md"
	lines := strings.Split(RenderDecisionIndex([]DecisionRow{early, late}), "\n")
	if !strings.HasPrefix(lines[2], "| [DL-001]") || !strings.HasPrefix(lines[3], "| [DL-002]") {
		t.Fatalf("index lines = %#v, want numeric id order", lines)
	}
}

func TestRenderDecisionIndexFixtureGoldens(t *testing.T) {
	golden := readGolden[fixtureIndexGolden](t, "fixture-index.json")
	row := func(id, decision string) DecisionRow {
		text := replaceGood("id: DL-297", "id: "+id)
		got := parseForTest(t, []byte(text), "docs/designs/decisions/meta/"+id+".md")
		got.Decision = decision
		return got
	}
	rows := []DecisionRow{row("DL-297", "a | b"), row("DL-030", "d DL-030"), row("DL-100", "d DL-100")}
	if got := RenderDecisionIndex(rows); got != golden.Index {
		t.Errorf("fixture index differs from TS golden: got %q", got)
	}
	if got := RenderDecisionIndex([]DecisionRow{row("DL-297", `a \| b`)}); got != golden.BackslashPipe {
		t.Errorf("backslash-pipe index differs from TS golden: got %q", got)
	}
	if got := RenderDecisionIndex([]DecisionRow{row("DL-297", "use `C:\\tmp` path")}); got != golden.CodeSpan {
		t.Errorf("code span index differs from TS golden: got %q", got)
	}
	var surrogate string
	err := json.Unmarshal([]byte(`"`+golden.LoneSurrogate+`"`), &surrogate)
	if err != nil {
		t.Fatal(err)
	}
	if surrogate != "�" {
		t.Errorf("lone-surrogate JSON decoding = %q, want replacement character", surrogate)
	}
}

func TestRenderDecisionIndexDocsGolden(t *testing.T) {
	manifest := readGolden[corpusManifest](t, "../corpus/docs-manifest.json")
	archive, err := os.ReadFile("testdata/corpus/docs-decisions.txtar")
	if err != nil {
		t.Fatal(err)
	}
	files := parseTextArchive(t, archive)
	rows := make([]DecisionRow, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		contents, ok := files[file.Corpus]
		if !ok {
			t.Fatalf("archive missing %s", file.Corpus)
		}
		row, err := ParseDecisionFile(file.Path, contents)
		if err != nil {
			t.Fatalf("ParseDecisionFile(%s): %v", file.Path, err)
		}
		rows = append(rows, row)
	}
	golden := readGolden[docsIndexGolden](t, "docs-index.json")
	if len(rows) != golden.Rows || len(rows) != golden.Files {
		t.Fatalf("parsed %d docs rows, golden covers %d files", len(rows), golden.Files)
	}
	if got := RenderDecisionIndex(rows); got != golden.Index {
		t.Fatalf("docs index differs from TS golden: got %d bytes, want %d", len(got), len(golden.Index))
	}
}

func renderCells(t *testing.T, markdown string) []string {
	t.Helper()
	var rendered bytes.Buffer
	parser := goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithRendererOptions(html.WithUnsafe()))
	err := parser.Convert([]byte(markdown), &rendered)
	if err != nil {
		t.Fatal(err)
	}
	rows := regexp.MustCompile(`(?s)<tr>(.*?)</tr>`).FindAllStringSubmatch(rendered.String(), -1)
	if len(rows) < 2 {
		return nil
	}
	cells := regexp.MustCompile(`(?s)<t[dh]>(.*?)</t[dh]>`).FindAllStringSubmatch(rows[1][1], -1)
	result := make([]string, len(cells))
	for index, cell := range cells {
		result[index] = cell[1]
	}
	return result
}

func TestParseDecisionFileDocsCorpus(t *testing.T) {
	manifest := readGolden[corpusManifest](t, "../corpus/docs-manifest.json")
	archive, err := os.ReadFile("testdata/corpus/docs-decisions.txtar")
	if err != nil {
		t.Fatal(err)
	}
	files := parseTextArchive(t, archive)
	for _, file := range manifest.Files {
		t.Run(path.Base(file.Path), func(t *testing.T) {
			contents, ok := files[file.Corpus]
			if !ok {
				t.Fatalf("archive missing %s", file.Corpus)
			}
			row, err := ParseDecisionFile(file.Path, contents)
			if err != nil {
				t.Fatalf("ParseDecisionFile(%s): %v", file.Path, err)
			}
			if row.ID == "" || row.Path != file.Path {
				t.Fatalf("parsed row = %#v", row)
			}
		})
	}
}
