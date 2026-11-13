// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"strings"
	"testing"

	"github.com/samber/mo"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Hello, world!":     "hello-world",
		"Problem / Intent":  "problem--intent",
		"Approach — part 1": "approach--part-1",
	}
	for heading, want := range cases {
		if got := Slugify(heading); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", heading, got, want)
		}
	}
}

func TestParseStatusValueAccepts(t *testing.T) {
	cases := []struct {
		name string
		line string
		want StatusValue
	}{
		{"blockquoted Historical", "> Status: Historical", StatusHistorical{}},
		{"bold Historical", "**Status: Historical**", StatusHistorical{}},
		{"quoted bold Historical", "> **Status: Historical**", StatusHistorical{}},
		{"bold supersession", "**Status: Superseded by ../next/design.md**", StatusSuperseded{Path: "../next/design.md"}},
		{
			"case-insensitive key and free-text tail",
			"Status: Superseded by foo.md for wave runtime, pane placement…",
			StatusSuperseded{Path: "foo.md"},
		},
		{"bold key", "**Status**: Historical", StatusHistorical{}},
		{"spaced colon", "Status : Historical", StatusHistorical{}},
		{"bold key with the colon inside the bold", "**Status:** Historical", StatusHistorical{}},
		{
			"bold key with the colon inside the bold superseded",
			"> **Status:** Superseded by ../x.md",
			StatusSuperseded{Path: "../x.md"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseStatusValue(tc.line)
			if !ok {
				t.Fatalf("ParseStatusValue(%q) = absent, want %#v", tc.line, tc.want)
			}
			if got != tc.want {
				t.Fatalf("ParseStatusValue(%q) = %#v, want %#v", tc.line, got, tc.want)
			}
		})
	}
}

func TestParseStatusValueRejects(t *testing.T) {
	for _, line := range []string{"Status: Draft", "Status: Active", "**Status: Active**"} {
		if got, ok := ParseStatusValue(line); ok {
			t.Errorf("ParseStatusValue(%q) = %#v, want absent", line, got)
		}
	}
}

func TestParseRecordHeaderExposesProhibitedValues(t *testing.T) {
	for _, line := range []string{"**Status**: Draft", "Status : Draft"} {
		t.Run(line, func(t *testing.T) {
			parsed := ParseRecordHeader("record.md", "# Title\n\n"+line+"\n")
			got, ok := parsed.StatusLine.Get()
			if !ok || got != line {
				t.Fatalf("StatusLine = %v, want %q", parsed.StatusLine, line)
			}
			if _, valid := ParseStatusValue(got); valid {
				t.Fatalf("ParseStatusValue(%q) accepted a prohibited value", got)
			}
		})
	}
}

func TestParseRecordHeaderFindsStatusSlot(t *testing.T) {
	got := ParseRecordHeader("record.md", "Preamble\n# Title\n\nStatus: Historical\n")
	want := RecordHeader{Path: "record.md", StatusLine: mo.Some("Status: Historical"), Line: 4}
	assertHeader(t, got, want)
	assertHeader(t, ParseRecordHeader("record.md", "Preamble only\n"), RecordHeader{
		Path: "record.md", StatusLine: mo.None[string](), Line: 1,
	})
	assertHeader(t, ParseRecordHeader("record.md", "```md\n# Example\n```\nPreamble\n"), RecordHeader{
		Path: "record.md", StatusLine: mo.None[string](), Line: 1,
	})
	assertHeader(t, ParseRecordHeader("record.md", "# Title\n\nBody\n"), RecordHeader{
		Path: "record.md", StatusLine: mo.None[string](), Line: 2,
	})
}

func TestParseRecordHeaderFindsEmphasizedHeader(t *testing.T) {
	for _, line := range []string{"> Status: Historical", "**Status: Historical**", "> **Status: Historical**"} {
		t.Run(line, func(t *testing.T) {
			got := ParseRecordHeader("record.md", "# Title\n\n"+line+"\n").StatusLine
			if value, ok := got.Get(); !ok || value != line {
				t.Fatalf("StatusLine = %v, want %q", got, line)
			}
		})
	}
}

func TestRecordStatusHeaders(t *testing.T) {
	t.Run("rejects prohibited Status and restricts Historical only when configured", func(t *testing.T) {
		if !hasMessage(
			evalDefault(corpusOf(), []RecordHeader{recordOf("", "# Title\n\nStatus: Draft\n")}),
			"malformed or prohibited",
		) {
			t.Error("prohibited status not reported")
		}
		if got := evalDefault(corpusOf(), []RecordHeader{recordOf("", "# Title\n\nStatus: Historical\n")}); len(got) != 0 {
			t.Errorf("historical violations = %+v, want none", got)
		}
		cfg := testConfig
		cfg.HistoricalChain = []string{}
		got := evaluateCorpus(corpusOf(), []RecordHeader{recordOf("", "# Title\n\nStatus: Historical\n")},
			noChange, smallRecord, cfg, noPRDetails())
		if !hasMessage(got, "configured historical chain") {
			t.Errorf("chain violations = %+v", got)
		}
	})

	t.Run("recordStatusScope changed checks PR-changed records only", func(t *testing.T) {
		draft := recordOf("", "# Title\n\nStatus: Draft\n")
		scoped := testConfig
		scoped.RecordStatusScope = "changed"
		flagged := func(changed Changed) bool {
			return hasMessage(evaluateCorpus(corpusOf(), []RecordHeader{draft}, changed, smallRecord, scoped,
				noPRDetails()), "malformed or prohibited")
		}
		if flagged(noChange) {
			t.Error("unchanged record flagged in changed scope")
		}
		if !flagged(Changed{Files: []string{draft.Path}, Body: mo.Some(""), HeadBranch: "feature"}) {
			t.Error("changed record not flagged in changed scope")
		}
	})

	t.Run("Status key spacing forms remain prohibited", func(t *testing.T) {
		for _, header := range []string{"**Status**: Draft", "Status : Draft"} {
			parsed := recordOf("", "# Title\n\n"+header+"\n")
			if !hasMessage(evalDefault(corpusOf(), []RecordHeader{parsed}), "malformed or prohibited") {
				t.Errorf("%s not reported", header)
			}
		}
	})

	t.Run("checks record-level Superseded pointer and decision status agreement", func(t *testing.T) {
		statusHeader := recordOf(
			"docs/designs/ui/record/design.md",
			"# Title\n\nStatus: Superseded by ../next/design.md\n",
		)
		if !hasMessage(
			evalDefault(corpusOf(), []RecordHeader{statusHeader}),
			"disagrees with its decision status",
		) {
			t.Error("disagreement not reported")
		}
		matching := evalDefault(corpusOf(
			decisionOptionsWithStatus("DL-001", "Superseded by DL-002 (Matt, 2026-07-22)"),
			decisionOptions{id: "DL-002", area: "", record: "../../ui/next/design.md", status: "", decision: "", path: ""},
		), []RecordHeader{statusHeader})
		if hasMessage(matching, "disagrees") {
			t.Errorf("matching status reported disagreement: %+v", matching)
		}
		missing := evaluateCorpus(corpusOf(), []RecordHeader{statusHeader}, noChange,
			func(string) (RecordContent, bool) {
				return RecordContent{Headings: nil, SizeBytes: 0}, false
			}, testConfig, noPRDetails())
		if !hasMessage(missing, "Status supersession does not resolve") {
			t.Errorf("unresolved pointer not reported: %+v", missing)
		}
		nested := recordOf(
			"docs/designs/ui/nested/record/design.md",
			"# Title\n\nStatus: Superseded by ui/next/design.md\n",
		)
		nestedWrongBase := evaluateCorpus(corpusOf(), []RecordHeader{nested}, noChange,
			func(path string) (RecordContent, bool) {
				if path == "docs/designs/ui/ui/next/design.md" {
					return smallRecord(path)
				}
				return RecordContent{Headings: nil, SizeBytes: 0}, false
			}, testConfig, noPRDetails())
		if !hasMessage(nestedWrongBase, "Status supersession does not resolve") {
			t.Errorf("nested wrong base not reported: %+v", nestedWrongBase)
		}
	})
}

func TestBranchExemptionDoesNotSkipRecordStatusValidation(t *testing.T) {
	got := evaluateCorpus(corpusOf(), []RecordHeader{recordOf("", "# Title\n\nStatus: Draft\n")},
		Changed{Files: nil, Body: mo.None[string](), HeadBranch: "renovate/update"},
		nil, testConfig, noPRDetails())
	if !hasMessage(got, "malformed or prohibited") {
		t.Fatalf("violations = %+v, want malformed status", got)
	}
}

func TestSameRepoBotExemptionSkipsChangedScopeStatus(t *testing.T) {
	draft := recordOf("", "# Title\n\nStatus: Draft\n")
	scoped := testConfig
	scoped.RecordStatusScope = "changed"
	got := evaluateCorpus(corpusOf(), []RecordHeader{draft},
		Changed{Files: []string{draft.Path}, Body: mo.Some(""), HeadBranch: "renovate/x"},
		nil, scoped, mo.Some(PullRequestDetails{
			IsCrossRepository:   mo.Some(false),
			HeadRepositoryOwner: mo.None[string](),
			Author:              mo.Some("rigelbuild-renovate[bot]"),
		}))
	if len(got) != 0 {
		t.Fatalf("violations = %+v, want none", got)
	}
}
func TestRecordContentExcludesFencedPseudoHeadings(t *testing.T) {
	text := "# Real\n\n```text\n# Not real\n```\n"
	got := RecordContentFromText(text)
	if len(got.Headings) != 1 || got.Headings[0] != "real" {
		t.Fatalf("headings = %v, want [real]", got.Headings)
	}
	if got.SizeBytes != len(text) {
		t.Fatalf("SizeBytes = %d, want %d", got.SizeBytes, len(text))
	}
}

func TestDuplicateHeadingsResolveSuffixedAnchor(t *testing.T) {
	got := RecordContentFromText("## X\n\n## X\n").Headings
	if len(got) != 2 || got[0] != "x" || got[1] != "x-1" {
		t.Fatalf("headings = %v, want [x x-1]", got)
	}
}

func TestConflictMarkerViolations(t *testing.T) {
	t.Run("flags git and jj markers, including lengthened runs", func(t *testing.T) {
		text := strings.Join(
			[]string{"<<<<<<< HEAD", "=======", ">>>>>>> theirs", "%%%%%%%%%%% diff", "+++++++++++ side"},
			"\n",
		)
		got := ConflictMarkerViolations("decision.md", text)
		if len(got) != 5 {
			t.Fatalf("violations = %+v, want 5", got)
		}
		for index, violation := range got {
			if violation.Line != index+1 {
				t.Errorf("violation %d line = %d, want %d", index, violation.Line, index+1)
			}
		}
	})

	t.Run("ignores fenced examples and setext headings", func(t *testing.T) {
		text := strings.Join(
			[]string{"Heading", "==============", "```text", "<<<<<<< example", "```"}, "\n",
		)
		if got := ConflictMarkerViolations("record.md", text); len(got) != 0 {
			t.Fatalf("violations = %+v, want none", got)
		}
	})
}
