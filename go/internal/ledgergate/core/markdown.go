// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/internal/jstext"
	"github.com/RigelBuild/meissa/go/ledger"
)

// File is one repository file read for the corpus.
type File struct {
	Path string
	Text string
}

// DecisionCorpus holds the parsed decision files and the stray design files.
// Archived holds decisions under archiveRoots. Live checks never see them;
// they are only the targets a live decision's supersession may not name.
type DecisionCorpus struct {
	Rows      []ledger.DecisionRow
	Archived  []ledger.DecisionRow
	Malformed []ledger.MalformedDecision
	Strays    []StrayPath
}

// RecordHeader is the parsed `Status:` slot of one Record.
// StatusLine is absent when the Record has no Status line.
type RecordHeader struct {
	Path       string
	StatusLine mo.Option[string]
	Line       int
}

// RecordContent holds the anchors and byte size of one Record.
type RecordContent struct {
	Headings  []string
	SizeBytes int
}

// Changed is the set of files and PR metadata the gate evaluates.
// Body is absent when the PR body is unknown.
type Changed struct {
	Files      []string
	Body       mo.Option[string]
	HeadBranch string
}

// StatusValue is a parsed `Status:` value: Historical or Superseded.
type StatusValue interface{ isStatusValue() }

// StatusHistorical is `Status: Historical`.
type StatusHistorical struct{}

// StatusSuperseded is `Status: Superseded by <path>`.
type StatusSuperseded struct{ Path string }

func (StatusHistorical) isStatusValue() {}
func (StatusSuperseded) isStatusValue() {}

// PullRequestDetails is the PR metadata used for branch exemptions.
// Absent fields are unknown and never exempt a branch.
type PullRequestDetails struct {
	IsCrossRepository   mo.Option[bool]
	HeadRepositoryOwner mo.Option[string]
	Author              mo.Option[string]
}

// Violation is one gate finding. Line 0 means no specific line.
type Violation struct {
	File    string
	Line    int
	Message string
}

// BuildDecisionCorpus parses each decision file and keeps the stray design files.
// A file that fails validation is recorded as malformed, not returned as an error.
func BuildDecisionCorpus(files []File, strays []StrayPath) DecisionCorpus {
	corpus := DecisionCorpus{
		Rows:      []ledger.DecisionRow{},
		Archived:  []ledger.DecisionRow{},
		Malformed: []ledger.MalformedDecision{},
		Strays:    append([]StrayPath{}, strays...),
	}
	for _, file := range files {
		row, err := ledger.ParseDecisionFile(file.Path, []byte(file.Text))
		if err == nil {
			corpus.Rows = append(corpus.Rows, row)
			continue
		}
		corpus.Malformed = append(corpus.Malformed, malformedFrom(file.Path, err))
	}
	return corpus
}

// malformedFrom returns the parser's malformed record. ParseDecisionFile
// returns only that type; any other error keeps its text with line 0.
func malformedFrom(path string, err error) ledger.MalformedDecision {
	malformed, ok := errors.AsType[*ledger.MalformedDecision](err)
	if ok {
		return *malformed
	}
	return ledger.MalformedDecision{Path: path, Line: 0, Reason: err.Error()}
}

// JSSpaceChars lists ECMAScript whitespace characters. RE2's `\s` is ASCII-only.
const JSSpaceChars = `\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// CIWord builds a case-insensitive ASCII literal for an ECMAScript-style regex.
// It avoids the `(?i)` flag, which folds U+017F to `s`; ECMAScript's non-unicode
// `/i` does not.
func CIWord(word string) string {
	var b strings.Builder
	for _, r := range word {
		if r >= 'a' && r <= 'z' {
			fmt.Fprintf(&b, "[%c%c]", r-'a'+'A', r)
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(r)))
	}
	return b.String()
}

// Regex fragments mirror ECMAScript classes RE2 does not share.
const (
	// JSSpace is the RE2 fragment matching ECMAScript whitespace.
	JSSpace    = `[` + JSSpaceChars + `]`
	jsNonSpace = `[^` + JSSpaceChars + `]`
	// jsDot is ECMAScript `.`, which also stops at \r, U+2028 and U+2029.
	jsDot = `[^\r\n\x{2028}\x{2029}]`
)

// normalizeLineTerminators maps the ECMAScript line terminators onto \n so a
// Go `(?m)` pattern sees the same line starts and ends as a JS `/m` pattern.
func normalizeLineTerminators(text string) string {
	return strings.NewReplacer("\r", "\n", "\u2028", "\n", "\u2029", "\n").Replace(text)
}

var (
	fenceOpenRE    = regexp.MustCompile("^ {0,3}(\x60{3,}|~{3,})(" + jsDot + "*)$")
	fenceCloseRE   = regexp.MustCompile("^ {0,3}(\x60+|~+)[ \t]*$")
	h1RE           = regexp.MustCompile("^#" + JSSpace)
	h2RE           = regexp.MustCompile("^##" + JSSpace)
	statusHeaderRE = regexp.MustCompile("^" + JSSpace + "*(?:>" + JSSpace + "*)*(?:\\*\\*)?" +
		CIWord("status") + "(?:\\*\\*)?" + JSSpace + "*:")
	statusRE = regexp.MustCompile("^" + JSSpace + "*>?" + JSSpace + "*(?:\\*\\*)?" +
		CIWord("status") + "(?:\\*\\*)?" + JSSpace + "*:(?:\\*\\*)?" + JSSpace + "*(?:\\*\\*)?" +
		"(?:(" + CIWord("historical") + ")|" + CIWord("superseded by ") + "(" + jsNonSpace + "+))" +
		"(?:\\*\\*)?(?:[" + JSSpaceChars + ".,;:()\u2014-]" + jsDot + "*)?$")
	ledgerImpactRE = regexp.MustCompile(`(?m)^` + JSSpace + `*>?` + JSSpace + `*` +
		CIWord("ledger-impact") + `:` + JSSpace + `*` + jsNonSpace + `.*$`)
	conflictFenceRE  = regexp.MustCompile("^" + JSSpace + "*(\x60\x60\x60|~~~)")
	conflictMarkerRE = regexp.MustCompile("^(<{7,}|>{7,}|%{7,}|\\+{7,}|={7})(" + JSSpace + "|$)")
	headingRE        = regexp.MustCompile("^#{1,6}" + JSSpace + "+(" + jsDot + "+?)" + JSSpace + "*#*" + JSSpace + "*$")
	supersededRE     = regexp.MustCompile(`^Superseded by (DL-(?:\d{3}|[1-9]\d{3,})) \(.+, \d{4}-\d{2}-\d{2}\)$`)
)

// IsH1 reports whether line has the shape of a Markdown H1 heading.
func IsH1(line string) bool { return h1RE.MatchString(line) }

// IsH2 reports whether line has the shape of a Markdown H2 heading.
func IsH2(line string) bool { return h2RE.MatchString(line) }

// largeRecordBytes is the size above which a Record link needs an anchor.
const largeRecordBytes = 50 * 1024

// MarkdownLine is one visible line and its one-based number.
type MarkdownLine struct {
	Line int
	Text string
}

// MarkdownScan holds lines outside fenced code blocks and any unclosed opener line.
type MarkdownScan struct {
	Lines         []MarkdownLine
	OpenFenceLine int
}

type fenceState struct {
	char   byte
	length int
	line   int
}

func closesFence(line string, fence fenceState) bool {
	closing := fenceCloseRE.FindStringSubmatch(line)
	return closing != nil && closing[1][0] == fence.char && len(closing[1]) >= fence.length
}

// ScanMarkdown mirrors markdownLinesOutsideFences in the TypeScript source.
func ScanMarkdown(text string) MarkdownScan {
	var visible []MarkdownLine
	var fence *fenceState
	for index, raw := range strings.Split(text, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if fence != nil {
			if closesFence(line, *fence) {
				fence = nil
			}
			continue
		}
		opener := fenceOpenRE.FindStringSubmatch(line)
		if opener == nil {
			visible = append(visible, MarkdownLine{Line: index + 1, Text: line})
			continue
		}
		marker, info := opener[1], opener[2]
		if marker[0] == '`' && strings.Contains(info, "`") {
			visible = append(visible, MarkdownLine{Line: index + 1, Text: line})
			continue
		}
		fence = &fenceState{char: marker[0], length: len(marker), line: index + 1}
	}
	scan := MarkdownScan{Lines: visible, OpenFenceLine: 0}
	if fence != nil {
		scan.OpenFenceLine = fence.line
	}
	return scan
}

// Slugify mirrors the GitHub heading anchor rule used by the TypeScript gate.
func Slugify(heading string) string {
	var b strings.Builder
	for _, r := range jstext.ToLower(jstext.Trim(heading)) {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r), r == '_', r == '-':
			b.WriteRune(r)
		case jstext.IsSpace(r):
			b.WriteRune('-')
		}
	}
	return b.String()
}

// HeadingSlugs returns the unique GitHub heading anchors in document order.
// A repeat gets the first free `-N` suffix, as GitHub does.
func HeadingSlugs(text string) []string {
	var slugs []string
	seen := map[string]bool{}
	for _, visible := range ScanMarkdown(text).Lines {
		heading := headingRE.FindStringSubmatch(visible.Text)
		if heading == nil {
			continue
		}
		base := Slugify(heading[1])
		slug := base
		for suffix := 1; seen[slug]; suffix++ {
			slug = fmt.Sprintf("%s-%d", base, suffix)
		}
		seen[slug] = true
		slugs = append(slugs, slug)
	}
	return slugs
}

// ParseStatusValue reads one `Status:` line. It reports false for a line that is
// not a Historical or Superseded status.
func ParseStatusValue(line string) (StatusValue, bool) {
	match := statusRE.FindStringSubmatch(line)
	if match == nil {
		return nil, false
	}
	if match[1] != "" {
		return StatusHistorical{}, true
	}
	target := strings.TrimRightFunc(match[2], func(r rune) bool {
		return strings.ContainsRune(".,;:()*-\u2014", r)
	})
	if target == "" {
		return nil, false
	}
	return StatusSuperseded{Path: target}, true
}

// ParseRecordHeader finds the `Status:` line in a Record's first H1 section.
// Fenced text is ignored.
func ParseRecordHeader(path, text string) RecordHeader {
	scan := ScanMarkdown(text)
	h1, found := FirstMarkdownLine(scan.Lines, func(line MarkdownLine) bool { return IsH1(line.Text) })
	if !found {
		return RecordHeader{Path: path, StatusLine: mo.None[string](), Line: 1}
	}
	nextH2 := math.MaxInt
	if h2, ok := FirstMarkdownLine(scan.Lines, func(line MarkdownLine) bool {
		return line.Line > h1.Line && IsH2(line.Text)
	}); ok {
		nextH2 = h2.Line
	}
	status, ok := FirstMarkdownLine(scan.Lines, func(line MarkdownLine) bool {
		return line.Line > h1.Line && line.Line < nextH2 && statusHeaderRE.MatchString(line.Text)
	})
	if !ok {
		return RecordHeader{Path: path, StatusLine: mo.None[string](), Line: h1.Line + 1}
	}
	return RecordHeader{
		Path:       path,
		StatusLine: mo.Some(jstext.TrimRight(status.Text)),
		Line:       status.Line,
	}
}

// FirstMarkdownLine returns the first visible line matching predicate.
func FirstMarkdownLine(lines []MarkdownLine, match func(MarkdownLine) bool) (MarkdownLine, bool) {
	for _, line := range lines {
		if match(line) {
			return line, true
		}
	}
	return MarkdownLine{Line: 0, Text: ""}, false
}

// RecordContentFromText returns the anchors and byte size of one Record.
func RecordContentFromText(text string) RecordContent {
	return RecordContent{Headings: HeadingSlugs(text), SizeBytes: len(text)}
}

// ConflictMarkerViolations reports unresolved merge markers outside fences.
func ConflictMarkerViolations(file, text string) []Violation {
	var out []Violation
	inFence := false
	for index, line := range strings.Split(text, "\n") {
		if conflictFenceRE.MatchString(line) {
			inFence = !inFence
		}
		if inFence {
			continue
		}
		marker := conflictMarkerRE.FindStringSubmatch(line)
		if marker != nil {
			out = append(out, Violation{
				File:    file,
				Line:    index + 1,
				Message: "unresolved merge conflict marker: " + marker[1],
			})
		}
	}
	return out
}

// hasLedgerImpact reports whether a PR body declares a Ledger-impact line.
func hasLedgerImpact(body string) bool {
	return ledgerImpactRE.MatchString(normalizeLineTerminators(body))
}
