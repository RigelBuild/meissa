// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samber/mo"
)

const decisionDir = "docs/designs/decisions"

var (
	testCounter = Counter{URL: "https://example.invalid/counter", Partition: "test"}
	testConfig  = Config{
		DesignsRoot:            "docs/designs",
		SurfaceDepth:           0,
		GovernedRoots:          []string{"ui", "agent", "server", "meta", "infra", "observability", "repo", "gamma"},
		Surfaces:               nil,
		HistoricalChain:        nil,
		ExemptBranches:         nil,
		RecordStatusScope:      "all",
		CitationAmbiguousPaths: []string{},
		Legs:                   Legs{Citations: false, Errata: false, RecordLinks: false, MainIDs: false, ClaimCheck: false},
		Counter:                testCounter,
		RemediationDoc:         mo.None[string](),
	}
	depthOneConfig = Config{
		DesignsRoot:            "docs/designs",
		SurfaceDepth:           1,
		GovernedRoots:          nil,
		Surfaces:               nil,
		HistoricalChain:        nil,
		ExemptBranches:         nil,
		RecordStatusScope:      "all",
		CitationAmbiguousPaths: []string{},
		Legs:                   Legs{Citations: false, Errata: false, RecordLinks: false, MainIDs: false, ClaimCheck: false},
		Counter:                testCounter,
		RemediationDoc:         mo.None[string](),
	}
	noChange = Changed{Files: nil, Body: mo.None[string](), HeadBranch: ""}
)

func noPRDetails() mo.Option[PullRequestDetails] { return mo.None[PullRequestDetails]() }

func smallRecord(string) (RecordContent, bool) {
	return RecordContent{Headings: []string{"present"}, SizeBytes: 100}, true
}

func decisionAt(path, record string) string {
	id := strings.TrimSuffix(filepath.Base(path), ".md")
	return strings.Join([]string{
		"---", "id: " + id, `decision: "Choose the stable option."`,
		`status: "Active (Reviewer, 2026-01-01)"`, "record: " + record,
		"---", "# Decision", "", "Choose the stable option.", "",
	}, "\n")
}

type decisionOptions struct{ id, area, record, status, decision, path string }

func (o decisionOptions) withDefaults() decisionOptions {
	if o.id == "" {
		o.id = "DL-001"
	}
	if o.area == "" {
		o.area = "ui"
	}
	if o.record == "" {
		o.record = "../../" + o.area + "/record/design.md"
	}
	if o.status == "" {
		o.status = "Active (Matt, 2026-07-22)"
	}
	if o.decision == "" {
		o.decision = "Use the stable design."
	}
	if o.path == "" {
		o.path = decisionDir + "/" + o.area + "/" + o.id + ".md"
	}
	return o
}

func decisionText(o decisionOptions) string {
	o = o.withDefaults()
	return strings.Join([]string{
		"---", "id: " + o.id, "decision: " + jsonString(o.decision),
		"status: " + jsonString(o.status), "record: " + o.record, "---",
		"# Decision", "", o.decision, "",
	}, "\n")
}
func decisionOptionsWithStatus(id, status string) decisionOptions {
	return decisionOptions{id: id, area: "", record: "", status: status, decision: "", path: ""}
}

func decisionOptionsForRecord(record string) decisionOptions {
	return decisionOptions{id: "", area: "", record: record, status: "", decision: "", path: ""}
}

func jsonString(value string) string { return `"` + value + `"` }

func decisionFile(o decisionOptions) File {
	o = o.withDefaults()
	return File{Path: o.path, Text: decisionText(o)}
}

func corpusOf(options ...decisionOptions) DecisionCorpus {
	if len(options) == 0 {
		options = []decisionOptions{{id: "", area: "", record: "", status: "", decision: "", path: ""}}
	}
	files := make([]File, 0, len(options))
	for _, option := range options {
		files = append(files, decisionFile(option))
	}
	return BuildDecisionCorpus(files, nil)
}

func evaluateCorpus(
	corpus DecisionCorpus,
	records []RecordHeader,
	changed Changed,
	read func(string) (RecordContent, bool),
	cfg Config,
	details mo.Option[PullRequestDetails],
) []Violation {
	if read == nil {
		read = smallRecord
	}
	return Evaluate(corpus, records, changed, read, cfg, details)
}

func evalDefault(corpus DecisionCorpus, records []RecordHeader) []Violation {
	return evaluateCorpus(corpus, records, noChange, nil, testConfig, noPRDetails())
}

func recordOf(path, text string) RecordHeader {
	if path == "" {
		path = "docs/designs/ui/record/design.md"
	}
	if text == "" {
		text = "# Title\n"
	}
	return ParseRecordHeader(path, text)
}

func hasMessage(violations []Violation, substring string) bool {
	return slices.ContainsFunc(violations, func(v Violation) bool {
		return strings.Contains(v.Message, substring)
	})
}

func countMessage(violations []Violation, substring string) int {
	count := 0
	for _, v := range violations {
		if strings.Contains(v.Message, substring) {
			count++
		}
	}
	return count
}

func findMessage(violations []Violation, substring string) (Violation, bool) {
	for _, v := range violations {
		if strings.Contains(v.Message, substring) {
			return v, true
		}
	}
	return Violation{File: "", Line: 0, Message: ""}, false
}
func containsViolation(violations []Violation, want Violation) bool {
	return slices.Contains(violations, want)
}

func filterSupersessionCycles(violations []Violation) []Violation {
	var out []Violation
	for _, v := range violations {
		if strings.Contains(v.Message, "supersession cycle") {
			out = append(out, v)
		}
	}
	return out
}

func hasFile(violations []Violation, file string) bool {
	for _, v := range violations {
		if v.File == file {
			return true
		}
	}
	return false
}

func assertHeader(t *testing.T, got, want RecordHeader) {
	t.Helper()
	if got.Path != want.Path || got.Line != want.Line {
		t.Fatalf("header = %+v, want %+v", got, want)
	}
	gotLine, gotOK := got.StatusLine.Get()
	wantLine, wantOK := want.StatusLine.Get()
	if gotOK != wantOK || gotLine != wantLine {
		t.Fatalf("StatusLine = %v, want %v", got.StatusLine, want.StatusLine)
	}
}

func TestDecisionRetiredPassesWithoutSuccessor(t *testing.T) {
	if got := evalDefault(corpusOf(decisionOptionsWithStatus("", "Retired (Matt, 2026-08-23)")), nil); len(got) != 0 {
		t.Fatalf("violations = %+v, want none", got)
	}
	both := corpusOf(
		decisionOptionsWithStatus("DL-001", "Retired (Matt, 2026-08-23)"),
		decisionOptionsWithStatus("DL-002", "Retired (Matt, 2026-08-23)"),
	)
	if got := evalDefault(both, nil); len(got) != 0 {
		t.Fatalf("violations = %+v, want none", got)
	}
}

func TestDecisionStatusGrammarEnforcedByParser(t *testing.T) {
	malformed := BuildDecisionCorpus(
		[]File{decisionFile(decisionOptionsWithStatus("", "Draft"))},
		nil,
	)
	if len(malformed.Rows) != 0 {
		t.Fatalf("rows = %d, want 0", len(malformed.Rows))
	}
	if len(malformed.Malformed) == 0 {
		t.Fatal("no malformed decision recorded")
	}
	got := malformed.Malformed[0]
	if got.Path != decisionDir+"/ui/DL-001.md" || got.Line != keyLineStatus {
		t.Fatalf("malformed = %+v, want path %s line %d", got, decisionDir+"/ui/DL-001.md", keyLineStatus)
	}
}

func TestDuplicateIDsAcrossAreasPointToSecondIDKey(t *testing.T) {
	got := evalDefault(corpusOf(
		decisionOptions{id: "DL-001", area: "ui", record: "", status: "", decision: "", path: ""},
		decisionOptions{
			id: "DL-001", area: "server", record: "../../server/other/design.md", status: "", decision: "", path: "",
		},
	), nil)
	want := Violation{
		File: decisionDir + "/server/DL-001.md",
		Line: KeyLineID,
		Message: "DL-001: duplicate decision id (also defined in " + decisionDir +
			"/ui/DL-001.md)",
	}
	if !containsViolation(got, want) {
		t.Fatalf("violations = %+v, want %+v", got, want)
	}
}

func TestValidSupersessionPassesMissingTargetAndSelfFail(t *testing.T) {
	ok := corpusOf(
		decisionOptionsWithStatus("DL-001", "Superseded by DL-002 (Matt, 2026-07-22)"),
		decisionOptionsWithStatus("DL-002", ""),
	)
	if got := evalDefault(ok, nil); len(got) != 0 {
		t.Fatalf("violations = %+v, want none", got)
	}
	missing := evalDefault(corpusOf(decisionOptionsWithStatus(
		"DL-001", "Superseded by DL-999 (Matt, 2026-07-22)",
	)), nil)
	want := Violation{
		File:    decisionDir + "/ui/DL-001.md",
		Line:    keyLineStatus,
		Message: "DL-001: Superseded by DL-999, which is not a decision file",
	}
	if !containsViolation(missing, want) {
		t.Fatalf("violations = %+v, want %+v", missing, want)
	}
	self := evalDefault(corpusOf(decisionOptionsWithStatus(
		"DL-001", "Superseded by DL-001 (Matt, 2026-07-22)",
	)), nil)
	if len(self) != 1 || !strings.Contains(self[0].Message, "superseded by itself") {
		t.Fatalf("self violations = %+v, want one superseded-by-itself", self)
	}
}

func TestSupersessionCyclesReportedOnceAtLowestID(t *testing.T) {
	twoCycle := evalDefault(corpusOf(
		decisionOptionsWithStatus("DL-002", "Superseded by DL-001 (Matt, 2026-07-22)"),
		decisionOptionsWithStatus("DL-001", "Superseded by DL-002 (Matt, 2026-07-22)"),
	), nil)
	cycles := filterSupersessionCycles(twoCycle)
	if len(cycles) != 1 {
		t.Fatalf("two-cycle reports = %+v, want 1", cycles)
	}
	if cycles[0].File != decisionDir+"/ui/DL-001.md" || cycles[0].Line != keyLineStatus {
		t.Fatalf("two-cycle locus = %+v", cycles[0])
	}

	threeCycle := evalDefault(corpusOf(
		decisionOptionsWithStatus("DL-001", "Superseded by DL-002 (Matt, 2026-07-22)"),
		decisionOptionsWithStatus("DL-002", "Superseded by DL-003 (Matt, 2026-07-22)"),
		decisionOptionsWithStatus("DL-003", "Superseded by DL-001 (Matt, 2026-07-22)"),
	), nil)
	if got := filterSupersessionCycles(threeCycle); len(got) != 1 {
		t.Fatalf("three-cycle reports = %+v, want 1", got)
	}

	feedIn := evalDefault(corpusOf(
		decisionOptionsWithStatus("DL-001", "Superseded by DL-002 (Matt, 2026-07-22)"),
		decisionOptionsWithStatus("DL-002", "Superseded by DL-003 (Matt, 2026-07-22)"),
		decisionOptionsWithStatus("DL-003", "Superseded by DL-004 (Matt, 2026-07-22)"),
		decisionOptionsWithStatus("DL-004", "Superseded by DL-003 (Matt, 2026-07-22)"),
	), nil)
	loop := filterSupersessionCycles(feedIn)
	if len(loop) != 1 || loop[0].Message != "supersession cycle: DL-003 → DL-004 → DL-003" {
		t.Fatalf("feed-in loop = %+v", loop)
	}

	independent := evalDefault(corpusOf(
		decisionOptionsWithStatus("DL-001", "Superseded by DL-002 (Matt, 2026-07-22)"),
		decisionOptionsWithStatus("DL-002", "Superseded by DL-001 (Matt, 2026-07-22)"),
		decisionOptionsWithStatus("DL-003", "Superseded by DL-004 (Matt, 2026-07-22)"),
		decisionOptionsWithStatus("DL-004", "Superseded by DL-003 (Matt, 2026-07-22)"),
	), nil)
	if got := filterSupersessionCycles(independent); len(got) != 2 {
		t.Fatalf("independent cycle reports = %+v, want 2", got)
	}

	healthy := evalDefault(corpusOf(
		decisionOptionsWithStatus("DL-001", "Superseded by DL-002 (Matt, 2026-07-22)"),
		decisionOptionsWithStatus("DL-002", "Superseded by DL-003 (Matt, 2026-07-22)"),
		decisionOptionsWithStatus("DL-003", "Active (Matt, 2026-07-22)"),
	), nil)
	if len(healthy) != 0 {
		t.Fatalf("healthy chain violations = %+v, want none", healthy)
	}

	self := evalDefault(corpusOf(decisionOptionsWithStatus("DL-001", "Superseded by DL-001 (Matt, 2026-07-22)")), nil)
	if len(self) != 1 || !strings.Contains(self[0].Message, "superseded by itself") {
		t.Fatalf("self violations = %+v", self)
	}
	if hasMessage(self, "supersession cycle") {
		t.Fatalf("self-supersession also reported a cycle: %+v", self)
	}
}

func TestRecordResolutionAnchorsAndLargeRecords(t *testing.T) {
	anchored := BuildDecisionCorpus(
		[]File{decisionFile(decisionOptionsForRecord("../../ui/record/design.md#present"))},
		nil,
	)
	if got := evaluateCorpus(anchored, nil, noChange,
		func(string) (RecordContent, bool) {
			return RecordContent{Headings: []string{"present"}, SizeBytes: 60_000}, true
		}, testConfig, noPRDetails()); len(got) != 0 {
		t.Fatalf("anchored violations = %+v, want none", got)
	}
	if got := evaluateCorpus(corpusOf(), nil, noChange,
		func(string) (RecordContent, bool) {
			return RecordContent{Headings: nil, SizeBytes: 100}, true
		}, testConfig, noPRDetails()); len(got) != 0 {
		t.Fatalf("unanchored violations = %+v, want none", got)
	}
	for _, fence := range []string{"```", "~~~"} {
		fenced := RecordContentFromText(fence + "md\n# pseudo\n" + fence + "\n")
		dead := evaluateCorpus(corpusOf(decisionOptionsForRecord("../../ui/record/design.md#pseudo")), nil,
			noChange, func(string) (RecordContent, bool) { return fenced, true }, testConfig, noPRDetails())
		if !hasMessage(dead, "anchor not found") {
			t.Errorf("fence %s: dead anchor not reported: %+v", fence, dead)
		}
	}
	missingPath := evaluateCorpus(corpusOf(), nil, noChange,
		func(string) (RecordContent, bool) {
			return RecordContent{Headings: nil, SizeBytes: 0}, false
		}, testConfig, noPRDetails())
	if v, ok := findMessage(missingPath, "does not resolve"); !ok || v.Line != keyLineRecord {
		t.Fatalf("missing path violations = %+v, want does-not-resolve at record line", missingPath)
	}
	missingAnchor := evaluateCorpus(corpusOf(decisionOptionsForRecord("../../ui/record/design.md#missing")), nil,
		noChange, smallRecord, testConfig, noPRDetails())
	if !hasMessage(missingAnchor, "anchor not found") {
		t.Fatalf("missing anchor violations = %+v", missingAnchor)
	}
	large := evaluateCorpus(corpusOf(), nil, noChange,
		func(string) (RecordContent, bool) {
			return RecordContent{Headings: nil, SizeBytes: 50*1024 + 1}, true
		}, testConfig, noPRDetails())
	if !hasMessage(large, "large record") {
		t.Fatalf("large record violations = %+v", large)
	}
}

func TestDecisionAreaMustMatchRecordPath(t *testing.T) {
	mismatch := evalDefault(corpusOf(decisionOptionsForRecord("../../server/record/design.md")), nil)
	want := Violation{
		File:    decisionDir + "/ui/DL-001.md",
		Line:    keyLineRecord,
		Message: "DL-001: decision area must match its Record path (docs/designs/server/record/design.md)",
	}
	if !containsViolation(mismatch, want) {
		t.Fatalf("mismatch violations = %+v, want %+v", mismatch, want)
	}
	outside := evalDefault(corpusOf(decisionOptionsForRecord("../../../elsewhere/design.md")), nil)
	if !hasMessage(outside, "decision area must match") {
		t.Fatalf("outside violations = %+v", outside)
	}
}

func TestDecisionCompletenessRequiresAreaDecision(t *testing.T) {
	got := evalDefault(corpusOf(), []RecordHeader{recordOf("docs/designs/server/record/design.md", "")})
	if !hasMessage(got, "docs/designs/server/") || !hasMessage(got, "no valid decision file") {
		t.Fatalf("violations = %+v, want server area decision missing", got)
	}
}

func TestNoDecisionFilesForGovernedRecords(t *testing.T) {
	records := []RecordHeader{recordOf("docs/designs/ui/record/design.md", "# Title\n")}
	got := evalDefault(DecisionCorpus{Rows: nil, Malformed: nil, Strays: nil}, records)
	if !hasMessage(got, "no valid decision files were found") {
		t.Fatalf("violations = %+v, want missing decision files", got)
	}
}
