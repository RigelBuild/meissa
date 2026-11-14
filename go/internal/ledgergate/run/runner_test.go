// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:err113,exhaustruct_v5 // Keep TS-derived cases direct and readable.
package run

import (
	"context"
	"errors"
	"github.com/samber/mo"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/claim"
	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
)

const (
	gateDesignsRoot  = "docs/designs"
	gateDecisionPath = "docs/designs/decisions/ui/DL-001.md"
	gateRecordPath   = "docs/designs/ui/record/design.md"
	gateSourcePath   = "docs/designs/ui/source.md"
	gateRecordLink   = "../../ui/record/design.md"
	gateActive       = "Active (Reviewer, 2026-01-01)"
	gateBody         = "# Decision\n\nChoose the stable option.\n"
)

// gateFake is the Deps for Once tests. A file missing from files reads as not found.
type gateFake struct {
	files        map[string]string
	paths        []string
	listErr      error
	listCalls    int
	readErrs     map[string]error
	changed      core.Changed
	changedErr   error
	details      mo.Option[core.PullRequestDetails]
	detailsErr   error
	mergeBase    []string
	mergeBaseErr error
	base         []string
	baseErr      error
	claims       map[string]claim.Verdict
	claimLane    string
	claimIDs     []string
}

// newGateFake lists every file it holds, in sorted order.
func newGateFake(files map[string]string) *gateFake {
	paths := make([]string, 0, len(files))
	for file := range files {
		paths = append(paths, file)
	}
	slices.Sort(paths)
	return &gateFake{files: files, paths: paths}
}

func (f *gateFake) Root() string { return "/repo" }

func (f *gateFake) ReadText(file string) (string, bool, error) {
	text, found := f.files[file]
	readErr := f.readErrs[file]
	if readErr != nil {
		return "", false, readErr
	}
	return text, found, nil
}

func (f *gateFake) BaseRef() string { return "main" }
func (f *gateFake) ListDesignFiles(context.Context) ([]string, error) {
	f.listCalls++
	return f.paths, f.listErr
}

func (f *gateFake) ReadMergeBaseDecisionPaths(context.Context, string) ([]string, error) {
	return f.mergeBase, f.mergeBaseErr
}

func (f *gateFake) ReadBaseDecisionPaths(context.Context, string) ([]string, error) {
	return f.base, f.baseErr
}

func (f *gateFake) Changed(context.Context) (core.Changed, error) { return f.changed, f.changedErr }

func (f *gateFake) FetchClaims(_ context.Context, lane string, ids []string) (map[string]claim.Verdict, error) {
	f.claimLane = lane
	f.claimIDs = append([]string(nil), ids...)
	return f.claims, nil
}

func (f *gateFake) PullRequestDetails(context.Context) (mo.Option[core.PullRequestDetails], error) {
	return f.details, f.detailsErr
}

// gateConfig is the TypeScript validConfig fixture: depth zero, governed area ui.
func gateConfig(legs core.Legs) core.Config {
	return core.Config{
		DesignsRoot:   gateDesignsRoot,
		SurfaceDepth:  0,
		GovernedRoots: []string{"ui"},
		Counter:       core.Counter{URL: "https://example.invalid/counter", Partition: "test"},
		Legs:          legs,
	}
}

// gateDecisionFile builds a decision file with the given status and body.
func gateDecisionFile(file, status string) string {
	id := strings.TrimSuffix(path.Base(file), ".md")
	return strings.Join([]string{
		"---",
		"id: " + id,
		`decision: "Choose the stable option."`,
		`status: "` + status + `"`,
		"record: " + gateRecordLink,
		"---",
		gateBody,
	}, "\n")
}

// runGate runs Once and returns its exit code, stdout and stderr.
func runGate(t *testing.T, deps Deps, cfg core.Config) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	code := Once(context.Background(), deps, cfg, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestRunOnceAppliesMatchingBotMetadataToDeclarationLegs ports
// "runOnce applies matching same-repo bot metadata to declaration legs".
func TestRunOnceAppliesMatchingBotMetadataToDeclarationLegs(t *testing.T) {
	fake := newGateFake(map[string]string{
		gateDecisionPath: gateDecisionFile(gateDecisionPath, gateActive),
		gateRecordPath:   "# Record\n\nStatus: Draft\n",
	})
	fake.changed = core.Changed{Files: []string{gateRecordPath}, Body: mo.Some(""), HeadBranch: "renovate/update"}
	fake.details = mo.Some(core.PullRequestDetails{
		IsCrossRepository: mo.Some(false),
		Author:            mo.Some("rigelbuild-renovate[bot]"),
	})
	cfg := gateConfig(core.Legs{})
	cfg.RecordStatusScope = "changed"
	cfg.ExemptBranches = []core.ExemptBranchRule{{Prefix: "renovate/", Author: "rigelbuild-renovate[bot]"}}
	if code, _, errs := runGate(t, fake, cfg); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, errs)
	}
}

// TestRunOnceListsOnceAndLogsCounts ports "valid fixture lists once and logs decision and record counts".
func TestRunOnceListsOnceAndLogsCounts(t *testing.T) {
	fake := newGateFake(map[string]string{
		gateDecisionPath:                   gateDecisionFile(gateDecisionPath, gateActive),
		gateRecordPath:                     "# Record\n\nBody\n",
		"docs/designs/decisions/README.md": "# Decisions\n",
	})
	code, stdout, errs := runGate(t, fake, gateConfig(core.Legs{}))
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, errs)
	}
	want := "design-ledger-gate: OK — 1 decision file(s), 1 record(s) status-checked; " +
		"PR checks skipped (no PR context); citation checks off; claim checks off.\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if fake.listCalls != 1 {
		t.Fatalf("listCalls = %d, want 1", fake.listCalls)
	}
}

// TestRunOnceChangedStatusScopeSkipsRecordsWithoutPRContext ports
// "changed Status scope skips records without PR context".
func TestRunOnceChangedStatusScopeSkipsRecordsWithoutPRContext(t *testing.T) {
	fake := newGateFake(map[string]string{
		gateDecisionPath: gateDecisionFile(gateDecisionPath, gateActive),
		gateRecordPath:   "# Record\n\nStatus: Draft\n",
	})
	cfg := gateConfig(core.Legs{})
	cfg.RecordStatusScope = "changed"
	code, stdout, errs := runGate(t, fake, cfg)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, errs)
	}
	if !strings.Contains(stdout, "record Status skipped (no PR context)") {
		t.Fatalf("stdout = %q, want the skipped-status note", stdout)
	}
}

// TestRunOnceRejectsReintroducedDecisionsMD ports "rejects a reintroduced DECISIONS.md".
func TestRunOnceRejectsReintroducedDecisionsMD(t *testing.T) {
	fake := newGateFake(map[string]string{
		gateDecisionPath: gateDecisionFile(gateDecisionPath, gateActive),
		gateRecordPath:   "# Record\n\nBody\n",
	})
	fake.paths = []string{gateDecisionPath, gateRecordPath, "docs/designs/DECISIONS.md"}
	code, _, errs := runGate(t, fake, gateConfig(core.Legs{}))
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr:\n%s", code, errs)
	}
	want := "DECISIONS.md is retired: decision files belong in the configured decisions tree"
	if !strings.Contains(errs, want) {
		t.Fatalf("stderr missing %q:\n%s", want, errs)
	}
}

// TestRunOnceReportsMisplacedPaths ports "reports misplaced decision-tree paths and DL files outside it".
func TestRunOnceReportsMisplacedPaths(t *testing.T) {
	fake := newGateFake(map[string]string{
		gateDecisionPath: gateDecisionFile(gateDecisionPath, gateActive),
		gateRecordPath:   "# Record\n\nBody\n",
	})
	fake.paths = []string{
		gateDecisionPath,
		gateRecordPath,
		"docs/designs/decisions/ui/extra.txt",
		"docs/designs/server/DL-009.md",
	}
	code, _, errs := runGate(t, fake, gateConfig(core.Legs{}))
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr:\n%s", code, errs)
	}
	if got := strings.Count(errs, "misplaced design file"); got != 2 {
		t.Fatalf("misplaced findings = %d, want 2; stderr:\n%s", got, errs)
	}
}

// TestRunOnceEmptyDesignsTreeFailsClosed ports "an empty designs tree fails closed".
func TestRunOnceEmptyDesignsTreeFailsClosed(t *testing.T) {
	code, _, errs := runGate(t, newGateFake(map[string]string{}), gateConfig(core.Legs{}))
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr:\n%s", code, errs)
	}
	if !strings.Contains(errs, "no files under docs/designs") {
		t.Fatalf("stderr = %q, want the empty-tree message", errs)
	}
}

// TestRunOnceReadmeOnlySurfacePassesAtDepthOne ports "a README-only surface passes at depth one".
func TestRunOnceReadmeOnlySurfacePassesAtDepthOne(t *testing.T) {
	fake := newGateFake(map[string]string{
		"docs/designs/alpha/README.md": "# Alpha\n",
	})
	cfg := core.Config{
		DesignsRoot:  gateDesignsRoot,
		SurfaceDepth: 1,
		Counter:      core.Counter{URL: "https://example.invalid/counter", Partition: "test"},
	}
	if code, _, errs := runGate(t, fake, cfg); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, errs)
	}
}

// TestRunOnceZeroDecisionFilesIsViolation ports "zero valid decision files is a violation".
func TestRunOnceZeroDecisionFilesIsViolation(t *testing.T) {
	fake := newGateFake(map[string]string{gateRecordPath: "# Record\n"})
	code, _, errs := runGate(t, fake, gateConfig(core.Legs{}))
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr:\n%s", code, errs)
	}
	if !strings.Contains(errs, "no valid decision files were found") {
		t.Fatalf("stderr = %q, want the zero-decision message", errs)
	}
}

// TestRunOnceMalformedDecisionReportedAtParserLine ports "malformed decision is reported at the parser line".
func TestRunOnceMalformedDecisionReportedAtParserLine(t *testing.T) {
	fake := newGateFake(map[string]string{
		gateDecisionPath: gateDecisionFile(gateDecisionPath, "Draft"),
		gateRecordPath:   "# Record\n",
	})
	code, _, errs := runGate(t, fake, gateConfig(core.Legs{}))
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr:\n%s", code, errs)
	}
	if want := gateDecisionPath + ":4: malformed decision file"; !strings.Contains(errs, want) {
		t.Fatalf("stderr missing %q:\n%s", want, errs)
	}
}

// TestRunOnceReportsDecisionConflictMarkers ports "checks decision conflict markers".
func TestRunOnceReportsDecisionConflictMarkers(t *testing.T) {
	decision := gateDecisionFile(gateDecisionPath, gateActive) +
		"\n<<<<<<< HEAD\n=======\n>>>>>>> theirs"
	fake := newGateFake(map[string]string{
		gateDecisionPath: decision,
		gateRecordPath:   "# Record\n",
	})
	code, _, errs := runGate(t, fake, gateConfig(core.Legs{}))
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr:\n%s", code, errs)
	}
	if got := strings.Count(errs, "unresolved merge conflict marker"); got != 3 {
		t.Fatalf("conflict findings = %d, want 3; stderr:\n%s", got, errs)
	}
}

// TestRunOnceRecordStatusStillRunsAndListingErrorsFailClosed ports
// "record Status checks still run and listing errors fail closed".
func TestRunOnceRecordStatusStillRunsAndListingErrorsFailClosed(t *testing.T) {
	status := newGateFake(map[string]string{
		gateDecisionPath: gateDecisionFile(gateDecisionPath, gateActive),
		gateRecordPath:   "# Record\n\nStatus: Draft\n",
	})
	code, _, errs := runGate(t, status, gateConfig(core.Legs{}))
	if code != 1 || !strings.Contains(errs, "malformed or prohibited") {
		t.Fatalf("exit = %d, want 1 with a prohibited-Status finding; stderr:\n%s", code, errs)
	}
	failed := newGateFake(map[string]string{})
	failed.listErr = errors.New("boom")
	code, _, errs = runGate(t, failed, gateConfig(core.Legs{}))
	if code != 2 || !strings.Contains(errs, "boom") {
		t.Fatalf("exit = %d, want 2 with the listing error; stderr:\n%s", code, errs)
	}
}

func TestRunOncePrintsChangedErrorVerbatim(t *testing.T) {
	metadataErr := func() error {
		_, err := ParsePRMetadata([]byte(`[]`))
		return err
	}()
	fake := newGateFake(map[string]string{
		gateDecisionPath: gateDecisionFile(gateDecisionPath, gateActive),
		gateRecordPath:   "# Record\n\nBody\n",
	})
	fake.changedErr = metadataErr
	code, _, stderr := runGate(t, fake, gateConfig(core.Legs{}))
	if code != 2 || stderr != "design-ledger-gate: Pull request metadata must be an object\n" {
		t.Fatalf("exit = %d, stderr = %q; want exact metadata diagnostic", code, stderr)
	}
}

func TestRunOnceRunsClaimCheckAndReportsCount(t *testing.T) {
	const decisionPath = "docs/designs/decisions/ui/DL-002.md"
	fake := newGateFake(map[string]string{
		gateDecisionPath: gateDecisionFile(gateDecisionPath, gateActive),
		decisionPath:     gateDecisionFile(decisionPath, gateActive),
		gateRecordPath:   "# Record\n\nBody\n",
	})
	fake.changed = core.Changed{Files: []string{decisionPath}, Body: mo.Some("body"), HeadBranch: "feature/x"}
	fake.claims = map[string]claim.Verdict{"DL-002": claim.VerdictLane}
	code, stdout, stderr := runGate(t, fake, gateConfig(core.Legs{ClaimCheck: true}))
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, stderr)
	}
	want := "design-ledger-gate: OK — 2 decision file(s), 1 record(s) status-checked; " +
		"PR checks enabled; citation checks off; claims: 1 new id(s) checked for feature/x.\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if fake.claimLane != "feature/x" || len(fake.claimIDs) != 1 || fake.claimIDs[0] != "DL-002" {
		t.Fatalf("claim request = %q %v, want feature/x [DL-002]", fake.claimLane, fake.claimIDs)
	}
}

func TestRunOnceReturnsRecordReadError(t *testing.T) {
	const target = "docs/designs/ui/record/missing.md"
	fake := newGateFake(map[string]string{
		gateDecisionPath: gateDecisionFile(gateDecisionPath, gateActive),
		gateRecordPath:   "# Record\n\nStatus: Superseded by missing.md\n",
	})
	fake.readErrs = map[string]error{target: errors.New("permission denied")}
	code, _, stderr := runGate(t, fake, gateConfig(core.Legs{}))
	if code != 2 || !strings.Contains(stderr, "cannot read "+target+": permission denied") {
		t.Fatalf("exit = %d, want 2 with the record read error; stderr:\n%s", code, stderr)
	}
}
