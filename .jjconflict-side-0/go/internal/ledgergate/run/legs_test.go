// SPDX-License-Identifier: MIT OR Apache-2.0
//
//nolint:err113,exhaustruct_v5 // Keep TS-derived cases direct and readable.
package run

import (
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
)

// gateLegsFiles is the common decision and record pair the leg tests build on.
func gateLegsFiles(extra map[string]string) map[string]string {
	files := map[string]string{
		gateDecisionPath: gateDecisionFile(gateDecisionPath, gateActive),
		gateRecordPath:   "# Record\n",
	}
	maps.Copy(files, extra)
	return files
}

// TestCitationsRejectPastEOFAndAcceptValid ports
// "citations reject past-EOF lines and accept valid references".
func TestCitationsRejectPastEOFAndAcceptValid(t *testing.T) {
	files := gateLegsFiles(map[string]string{
		gateRecordPath: "# Record\nSee `../source.md:3` here.\n",
		gateSourcePath: "# Source\n",
	})
	legs := core.Legs{Citations: true}
	if code, _, errs := runGate(t, newGateFake(files), gateConfig(legs)); code != 1 ||
		!strings.Contains(errs, "exceeds target line count") {
		t.Fatalf("exit = %d, want 1 with a line-count finding; stderr:\n%s", code, errs)
	}
	files[gateRecordPath] = "# Record\nSee `../source.md:1` here.\n"
	code, stdout, errs := runGate(t, newGateFake(files), gateConfig(legs))
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, errs)
	}
	if !strings.Contains(stdout, "1/1 citation(s) (0 unresolvable, 0 repo-ambiguous)") {
		t.Fatalf("stdout = %q, want the 1/1 citation tally", stdout)
	}
}

// TestCitationsParseInlineProseAndValidateEveryPart ports
// "citations parse inline prose and validate every comma-separated part".
func TestCitationsParseInlineProseAndValidateEveryPart(t *testing.T) {
	files := gateLegsFiles(map[string]string{
		gateRecordPath: "See note `../source.md:1,2` in this paragraph.\n",
		gateSourcePath: "# Source\nline two\n",
	})
	legs := core.Legs{Citations: true}
	code, stdout, errs := runGate(t, newGateFake(files), gateConfig(legs))
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, errs)
	}
	if !strings.Contains(stdout, "2/2 citation(s) (0 unresolvable, 0 repo-ambiguous)") {
		t.Fatalf("stdout = %q, want the 2/2 citation tally", stdout)
	}
	files[gateRecordPath] = "See note `../source.md:1,3` in this paragraph.\n"
	if code, _, errs := runGate(t, newGateFake(files), gateConfig(legs)); code != 1 ||
		!strings.Contains(errs, "source.md:3 exceeds target line count 2") {
		t.Fatalf("exit = %d, want 1 with the line-3 finding; stderr:\n%s", code, errs)
	}
	files[gateRecordPath] = "Note `../source.md:0,2-1,2`.\n"
	code, stdout, errs = runGate(t, newGateFake(files), gateConfig(legs))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 for skipped references; stderr:\n%s", code, errs)
	}
	if !strings.Contains(stdout, "1/1 citation(s) (0 unresolvable, 0 repo-ambiguous)") {
		t.Fatalf("stdout = %q, want the skipped-reference tally", stdout)
	}
}

// TestBareCitationFilenamesDoNotFallBackToRepoRoot ports
// "bare citation filenames do not fall back to the repository root".
func TestBareCitationFilenamesDoNotFallBackToRepoRoot(t *testing.T) {
	files := gateLegsFiles(map[string]string{
		gateRecordPath: "See `README.md:1` in this record.\n",
		"README.md":    "# Root README\n",
	})
	code, stdout, errs := runGate(t, newGateFake(files), gateConfig(core.Legs{Citations: true}))
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, errs)
	}
	if !strings.Contains(stdout, "0/1 citation(s) (1 unresolvable, 0 repo-ambiguous)") {
		t.Fatalf("stdout = %q, want the unresolvable tally", stdout)
	}
}

// TestConfiguredRootCitationPathsAreRepoAmbiguous ports
// "configured root citation paths are refused as repo-ambiguous".
func TestConfiguredRootCitationPathsAreRepoAmbiguous(t *testing.T) {
	files := gateLegsFiles(map[string]string{
		gateRecordPath:     "See `shared/README.md:1` in this record.\n",
		"shared/README.md": "# Shared README\n",
	})
	cfg := gateConfig(core.Legs{Citations: true})
	cfg.CitationAmbiguousPaths = []string{"shared/README.md"}
	code, stdout, errs := runGate(t, newGateFake(files), cfg)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, errs)
	}
	if !strings.Contains(stdout, "0/1 citation(s) (0 unresolvable, 1 repo-ambiguous)") {
		t.Fatalf("stdout = %q, want the repo-ambiguous tally", stdout)
	}
}

// TestCitationsIgnoreFencedNestedLookingButRejectUnclosedFence ports
// "citations ignore nested-looking fences but reject an unclosed fence".
func TestCitationsIgnoreFencedNestedLookingButRejectUnclosedFence(t *testing.T) {
	fenced := strings.Join([]string{
		"# Record",
		"````md",
		"```",
		"Citation `../ui/source.md:99` stays fenced.",
		"```",
		"````",
		"Valid inline `../source.md:1` outside the fence.",
		"",
	}, "\n")
	files := gateLegsFiles(map[string]string{
		gateRecordPath: fenced,
		gateSourcePath: "# Source\n",
	})
	legs := core.Legs{Citations: true}
	code, stdout, errs := runGate(t, newGateFake(files), gateConfig(legs))
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, errs)
	}
	if !strings.Contains(stdout, "1/1 citation(s) (0 unresolvable, 0 repo-ambiguous)") {
		t.Fatalf("stdout = %q, want the 1/1 citation tally", stdout)
	}
	files[gateRecordPath] = "# Record\n````md\n`../source.md:99`\n"
	code, _, errs = runGate(t, newGateFake(files), gateConfig(legs))
	if code != 1 || !strings.Contains(errs, "unclosed Markdown fence") {
		t.Fatalf("exit = %d, want 1 with the unclosed-fence finding; stderr:\n%s", code, errs)
	}
}

// TestEnabledProseLegsReportOneUnclosedFencePerFile ports
// "enabled prose legs report one unclosed fence per file".
func TestEnabledProseLegsReportOneUnclosedFencePerFile(t *testing.T) {
	files := gateLegsFiles(map[string]string{
		gateRecordPath: "# Record\n````md\nunfinished `../source.md:99`\n",
	})
	legs := core.Legs{Citations: true, RecordLinks: true, Errata: true}
	code, _, errs := runGate(t, newGateFake(files), gateConfig(legs))
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr:\n%s", code, errs)
	}
	if got := strings.Count(errs, "unclosed Markdown fence"); got != 1 {
		t.Fatalf("unclosed-fence findings = %d, want 1; stderr:\n%s", got, errs)
	}
}

// TestRecordLinksRejectDeadAnchorsAndAcceptSlugsOutsideFences ports
// "recordLinks reject dead anchors and accept heading slugs outside fences".
func TestRecordLinksRejectDeadAnchorsAndAcceptSlugsOutsideFences(t *testing.T) {
	files := gateLegsFiles(map[string]string{
		gateRecordPath: "# Record\n[link](../source.md#missing)\n",
		gateSourcePath: "# Real Heading\n```md\n# Hidden\n```\n",
	})
	legs := core.Legs{RecordLinks: true}
	if code, _, errs := runGate(t, newGateFake(files), gateConfig(legs)); code != 1 ||
		!strings.Contains(errs, "anchor not found") {
		t.Fatalf("exit = %d, want 1 with the anchor finding; stderr:\n%s", code, errs)
	}
	files[gateRecordPath] = "# Record\n[link](../source.md#real-heading)\n"
	if code, _, errs := runGate(t, newGateFake(files), gateConfig(legs)); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, errs)
	}
}

// TestRecordLinksRejectPathsEscapingRepoRoot ports "recordLinks reject paths escaping the repo root".
func TestRecordLinksRejectPathsEscapingRepoRoot(t *testing.T) {
	files := gateLegsFiles(map[string]string{
		gateRecordPath: "# Record\n[link](../../../../../outside.md#anchor)\n",
	})
	code, _, errs := runGate(t, newGateFake(files), gateConfig(core.Legs{RecordLinks: true}))
	if code != 1 || !strings.Contains(errs, "escapes the repository") {
		t.Fatalf("exit = %d, want 1 with the escape finding; stderr:\n%s", code, errs)
	}
}

// TestRecordLinksRejectPathsEscapingWithoutAnchor ports "recordLinks reject paths escaping without an anchor".
func TestRecordLinksRejectPathsEscapingWithoutAnchor(t *testing.T) {
	files := gateLegsFiles(map[string]string{
		gateRecordPath: "# Record\n[link](../../../../../outside.md)\n",
	})
	code, _, errs := runGate(t, newGateFake(files), gateConfig(core.Legs{RecordLinks: true}))
	if code != 1 || !strings.Contains(errs, "escapes the repository") {
		t.Fatalf("exit = %d, want 1 with the escape finding; stderr:\n%s", code, errs)
	}
}

// TestErrataRejectsStaleQuotesAndAcceptsOrderedExactQuotes ports
// "errata rejects stale quotes and accepts ordered exact quotes".
func TestErrataRejectsStaleQuotesAndAcceptsOrderedExactQuotes(t *testing.T) {
	errataText := func(quote string) string {
		return strings.Join([]string{
			"---",
			"id: DL-001",
			`decision: "Choose the stable option."`,
			`status: "Active (Reviewer, 2026-01-01)"`,
			"record: " + gateRecordLink,
			"---",
			"# Decision",
			"Errata: E1",
			"",
			"The earlier wording was wrong.",
			"",
			"## Errata",
			"### E1 — 2026-01-02 (Reviewer)",
			`Correction of "` + quote + `".`,
			"",
		}, "\n")
	}
	files := map[string]string{
		gateDecisionPath: errataText("gone text"),
		gateRecordPath:   "# Record\n",
	}
	legs := core.Legs{Errata: true}
	if code, _, errs := runGate(t, newGateFake(files), gateConfig(legs)); code != 1 ||
		!strings.Contains(errs, "no longer appears above the section") {
		t.Fatalf("exit = %d, want 1 with the stale-quote finding; stderr:\n%s", code, errs)
	}
	files[gateDecisionPath] = errataText("The earlier wording was wrong.")
	if code, _, errs := runGate(t, newGateFake(files), gateConfig(legs)); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, errs)
	}
}

// TestErrataValidatesHeadingsOrderingFinalH2AndMarkerIDs ports
// "errata validates headings, ordering, final H2, and marker IDs".
func TestErrataValidatesHeadingsOrderingFinalH2AndMarkerIDs(t *testing.T) {
	text := strings.Join([]string{
		"---",
		"id: DL-001",
		`decision: "Choose the stable option."`,
		`status: "Active (Reviewer, 2026-01-01)"`,
		"record: " + gateRecordLink,
		"---",
		"# Decision",
		"Errata: E2",
		`Prior wording includes "old text".`,
		"",
		"## Errata",
		"### E1 — 2026-01-02 (Reviewer)",
		`Correction of "old text".`,
		"",
	}, "\n")
	files := map[string]string{gateDecisionPath: text, gateRecordPath: "# Record\n"}
	code, _, errs := runGate(t, newGateFake(files), gateConfig(core.Legs{Errata: true}))
	if code != 1 || !strings.Contains(errs, "marker IDs must match") {
		t.Fatalf("exit = %d, want 1 with the marker-ID finding; stderr:\n%s", code, errs)
	}
}

// TestErrataRejectsMalformedHeadingsAndNonFinalErrataH2 ports
// "errata rejects malformed headings and a non-final Errata H2".
func TestErrataRejectsMalformedHeadingsAndNonFinalErrataH2(t *testing.T) {
	text := strings.Join([]string{
		"---",
		"id: DL-001",
		`decision: "Choose the stable option."`,
		`status: "Active (Reviewer, 2026-01-01)"`,
		"record: " + gateRecordLink,
		"---",
		"# Decision",
		"Errata: E1",
		`Prior wording includes "old text".`,
		"",
		"## Errata",
		"### E1 malformed",
		`Correction of "old text".`,
		"",
		"## More",
	}, "\n")
	files := map[string]string{gateDecisionPath: text, gateRecordPath: "# Record\n"}
	code, _, errs := runGate(t, newGateFake(files), gateConfig(core.Legs{Errata: true}))
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr:\n%s", code, errs)
	}
	for _, want := range []string{"must be the final H2", "malformed errata entry heading"} {
		if !strings.Contains(errs, want) {
			t.Fatalf("stderr missing %q:\n%s", want, errs)
		}
	}
}

// TestErrataDiagnostics ports the table "reports the expected errata diagnostic for %s".
func TestErrataDiagnostics(t *testing.T) {
	cases := []struct {
		name, message string
		lines         []string
	}{
		{
			name:    "missing Errata marker",
			message: "Errata section requires an Errata marker line",
			lines: []string{
				"# Decision",
				`Prior wording includes "old text".`,
				"## Errata",
				"### E1 — 2026-01-02 (Reviewer)",
				`Correction of "old text".`,
			},
		},
		{
			name:    "marker without section",
			message: "Errata marker requires an Errata section",
			lines:   []string{"# Decision", "Errata: E1", `Prior wording includes "old text".`},
		},
		{
			name:    "missing numbered entry",
			message: "errata IDs must be numbered E1..En in order",
			lines: []string{
				"# Decision",
				"Errata: E2",
				`Prior wording includes "old text".`,
				"## Errata",
				"### E2 — 2026-01-02 (Reviewer)",
				`Correction of "old text".`,
			},
		},
		{
			name:    "entry without quote",
			message: "errata entry requires a quoted wrong text",
			lines: []string{
				"# Decision",
				"Errata: E1",
				`Prior wording includes "old text".`,
				"## Errata",
				"### E1 — 2026-01-02 (Reviewer)",
				"Correction without a quote.",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := strings.Join(append([]string{
				"---",
				"id: DL-001",
				`decision: "Choose the stable option."`,
				`status: "Active (Reviewer, 2026-01-01)"`,
				"record: " + gateRecordLink,
				"---",
			}, tc.lines...), "\n")
			files := map[string]string{gateDecisionPath: text, gateRecordPath: "# Record\n"}
			code, _, errs := runGate(t, newGateFake(files), gateConfig(core.Legs{Errata: true}))
			if code != 1 || !strings.Contains(errs, tc.message) {
				t.Fatalf("exit = %d, want 1 with %q; stderr:\n%s", code, tc.message, errs)
			}
		})
	}
}

// TestMainIDsCompareMergeBaseAdditionsAgainstBaseTip ports
// "mainIds compare merge-base additions against base-tip IDs".
func TestMainIDsCompareMergeBaseAdditionsAgainstBaseTip(t *testing.T) {
	files := gateLegsFiles(nil)
	changed := core.Changed{Files: []string{gateDecisionPath}, Body: mo.Some(""), HeadBranch: "feature/test"}
	legs := core.Legs{MainIDs: true}

	duplicate := newGateFake(files)
	duplicate.changed = changed
	duplicate.base = []string{"docs/designs/decisions/server/DL-001.md"}
	code, _, errs := runGate(t, duplicate, gateConfig(legs))
	if code != 1 || !strings.Contains(errs, "already exists on base branch tip") {
		t.Fatalf("exit = %d, want 1 with the base-tip duplicate; stderr:\n%s", code, errs)
	}

	oldID := newGateFake(files)
	oldID.changed = changed
	oldID.mergeBase = []string{"docs/designs/decisions/server/DL-001.md"}
	oldID.base = []string{"docs/designs/decisions/server/DL-001.md"}
	if code, _, errs := runGate(t, oldID, gateConfig(legs)); code != 0 {
		t.Fatalf("exit = %d, want 0 for an id already on the merge base; stderr:\n%s", code, errs)
	}

	unused := newGateFake(files)
	unused.changed = changed
	unused.base = []string{"docs/designs/decisions/server/DL-500.md"}
	if code, _, errs := runGate(t, unused, gateConfig(legs)); code != 0 {
		t.Fatalf("exit = %d, want 0 for an unused id; stderr:\n%s", code, errs)
	}
}

// TestMainIDsFailClosedWhenListingFails ports "mainIds fail closed when %s listing fails".
func TestMainIDsFailClosedWhenListingFails(t *testing.T) {
	for _, failed := range []string{"merge-base", "base-tip"} {
		t.Run(failed, func(t *testing.T) {
			fake := newGateFake(gateLegsFiles(nil))
			fake.changed = core.Changed{Files: []string{gateDecisionPath}, Body: mo.Some(""), HeadBranch: "feature/test"}
			if failed == "merge-base" {
				fake.mergeBaseErr = errors.New("merge-base failed")
			} else {
				fake.baseErr = errors.New("base-tip failed")
			}
			code, _, errs := runGate(t, fake, gateConfig(core.Legs{MainIDs: true}))
			if code != 2 || !strings.Contains(errs, "cannot read base decision listing") {
				t.Fatalf("exit = %d, want 2 with the listing error; stderr:\n%s", code, errs)
			}
		})
	}
}

// TestAllExtraLegsStayOffByDefault ports "all extra legs stay off by default".
func TestAllExtraLegsStayOffByDefault(t *testing.T) {
	files := gateLegsFiles(map[string]string{
		gateRecordPath: "# Record\n`../source.md:99`\n[link](../source.md#missing)\n## Errata\n### E1 malformed\n",
		gateSourcePath: "# Source\n",
	})
	changed := core.Changed{Files: []string{gateDecisionPath}, Body: mo.Some(""), HeadBranch: "feature/test"}
	build := func() *gateFake {
		fake := newGateFake(files)
		fake.changed = changed
		fake.base = []string{"docs/designs/decisions/server/DL-001.md"}
		return fake
	}
	if code, _, errs := runGate(t, build(), gateConfig(core.Legs{})); code != 0 {
		t.Fatalf("exit = %d, want 0 with every leg off; stderr:\n%s", code, errs)
	}
	for _, legs := range []core.Legs{{Citations: true}, {Errata: true}, {RecordLinks: true}, {MainIDs: true}} {
		if code, _, errs := runGate(t, build(), gateConfig(legs)); code != 1 {
			t.Fatalf("legs %+v: exit = %d, want 1; stderr:\n%s", legs, code, errs)
		}
	}
}
