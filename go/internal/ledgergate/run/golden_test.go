// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:exhaustruct_v5,lll // Keep TS-derived fixtures direct; the goldens are the contract.
package run

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
)

const (
	goldenDecisionDir = "docs/designs/decisions"
	goldenRecordPath  = "docs/designs/ui/record/design.md"
	goldenServerRec   = "docs/designs/server/record/design.md"
	goldenOtherRec    = "docs/designs/server/other/design.md"
	goldenNextRec     = "docs/designs/ui/next/design.md"
	goldenSmallBody   = "# Record\n\nBody\n"
	goldenActive      = "Active (Matt, 2026-07-22)"
)

// goldenConfig is the TypeScript CONFIG fixture: depth zero, eight governed roots.
func goldenConfig() core.Config {
	return core.Config{
		DesignsRoot:   gateDesignsRoot,
		SurfaceDepth:  0,
		GovernedRoots: []string{"ui", "agent", "server", "meta", "infra", "observability", "repo", "gamma"},
		Counter:       core.Counter{URL: "https://example.invalid/counter", Partition: "test"},
	}
}

// goldenDecision builds one decision file in the TS decisionText shape.
func goldenDecision(id, status, record string) string {
	return strings.Join([]string{
		"---",
		"id: " + id,
		`decision: "Use the stable design."`,
		`status: "` + status + `"`,
		"record: " + record,
		"---",
		"# Decision",
		"",
		"Use the stable design.",
		"",
	}, "\n")
}

// goldenPath is the decision file for id in area, the TS decisionFile path.
func goldenPath(id, area string) string {
	return path.Join(goldenDecisionDir, area, id+".md")
}

// goldenFiles maps paths to text. Every path in it is readable, as the TS
// readRecord stub makes every record path readable.
type goldenFiles map[string]string

// goldenCase is one TS fixture. The golden holds the full stderr the TS CLI
// printed. listed names the paths the TS listDesignFiles returned; the rest of
// files are readable but not listed, as in the TS fixture.
type goldenCase struct {
	name     string
	files    goldenFiles
	listed   []string
	cfg      core.Config
	changed  core.Changed
	wantExit int
}

// goldenCases lists the 17 messages Once prints. The CLI config case is in cmd.
func goldenCases() []goldenCase {
	ui := goldenConfig()
	historical := goldenConfig()
	historical.HistoricalChain = []string{}
	uiDecision := goldenPath("DL-001", "ui")
	return []goldenCase{
		{name: "duplicate-decision-id", wantExit: 1, cfg: ui,
			files: goldenFiles{
				goldenPath("DL-001", "ui"):     goldenDecision("DL-001", goldenActive, "../../ui/record/design.md"),
				goldenPath("DL-001", "server"): goldenDecision("DL-001", goldenActive, "../../server/other/design.md"),
				goldenRecordPath:               goldenSmallBody,
				goldenOtherRec:                 goldenSmallBody,
			},
			listed: []string{goldenPath("DL-001", "ui"), goldenPath("DL-001", "server")}},
		{name: "supersession-missing-target", wantExit: 1, cfg: ui,
			files: goldenFiles{
				uiDecision:       goldenDecision("DL-001", "Superseded by DL-999 (Matt, 2026-07-22)", "../../ui/record/design.md"),
				goldenRecordPath: goldenSmallBody,
			},
			listed: []string{uiDecision}},
		{name: "superseded-by-itself", wantExit: 1, cfg: ui,
			files: goldenFiles{
				uiDecision:       goldenDecision("DL-001", "Superseded by DL-001 (Matt, 2026-07-22)", "../../ui/record/design.md"),
				goldenRecordPath: goldenSmallBody,
			},
			listed: []string{uiDecision}},
		{name: "supersession-cycle", wantExit: 1, cfg: ui,
			files: goldenFiles{
				goldenPath("DL-001", "ui"): goldenDecision("DL-001", "Superseded by DL-002 (Matt, 2026-07-22)", "../../ui/record/design.md"),
				goldenPath("DL-002", "ui"): goldenDecision("DL-002", "Superseded by DL-003 (Matt, 2026-07-22)", "../../ui/record/design.md"),
				goldenPath("DL-003", "ui"): goldenDecision("DL-003", "Superseded by DL-004 (Matt, 2026-07-22)", "../../ui/record/design.md"),
				goldenPath("DL-004", "ui"): goldenDecision("DL-004", "Superseded by DL-003 (Matt, 2026-07-22)", "../../ui/record/design.md"),
				goldenRecordPath:           goldenSmallBody,
			},
			listed: []string{goldenPath("DL-001", "ui"), goldenPath("DL-002", "ui"), goldenPath("DL-003", "ui"), goldenPath("DL-004", "ui")}},
		{name: "supersession-cycle-independent", wantExit: 1, cfg: ui,
			files: goldenFiles{
				goldenPath("DL-001", "ui"): goldenDecision("DL-001", "Superseded by DL-002 (Matt, 2026-07-22)", "../../ui/record/design.md"),
				goldenPath("DL-002", "ui"): goldenDecision("DL-002", "Superseded by DL-001 (Matt, 2026-07-22)", "../../ui/record/design.md"),
				goldenPath("DL-003", "ui"): goldenDecision("DL-003", "Superseded by DL-004 (Matt, 2026-07-22)", "../../ui/record/design.md"),
				goldenPath("DL-004", "ui"): goldenDecision("DL-004", "Superseded by DL-003 (Matt, 2026-07-22)", "../../ui/record/design.md"),
				goldenRecordPath:           goldenSmallBody,
			},
			listed: []string{goldenPath("DL-001", "ui"), goldenPath("DL-002", "ui"), goldenPath("DL-003", "ui"), goldenPath("DL-004", "ui")}},
		{name: "anchor-not-found", wantExit: 1, cfg: ui,
			files: goldenFiles{
				uiDecision:       goldenDecision("DL-001", goldenActive, "../../ui/record/design.md#missing"),
				goldenRecordPath: "# Present\n\nBody\n",
			},
			listed: []string{uiDecision}},
		{name: "record-link-does-not-resolve", wantExit: 1, cfg: ui,
			files:  goldenFiles{uiDecision: goldenDecision("DL-001", goldenActive, "../../ui/record/design.md")},
			listed: []string{uiDecision}},
		{name: "large-record-without-anchor", wantExit: 1, cfg: ui,
			files: goldenFiles{
				uiDecision:       goldenDecision("DL-001", goldenActive, "../../ui/record/design.md"),
				goldenRecordPath: strings.Repeat("x", 50*1024+1),
			},
			listed: []string{uiDecision}},
		{name: "decision-area-mismatch", wantExit: 1, cfg: ui,
			files: goldenFiles{
				uiDecision:      goldenDecision("DL-001", goldenActive, "../../server/record/design.md"),
				goldenServerRec: goldenSmallBody,
			},
			listed: []string{uiDecision}},
		{name: "malformed-or-prohibited-status", wantExit: 1, cfg: ui,
			files: goldenFiles{
				uiDecision:       goldenDecision("DL-001", goldenActive, "../../ui/record/design.md"),
				goldenRecordPath: "# Title\n\nStatus: Draft\n",
			},
			listed: []string{uiDecision, goldenRecordPath}},
		{name: "historical-outside-chain", wantExit: 1, cfg: historical,
			files: goldenFiles{
				uiDecision:       goldenDecision("DL-001", goldenActive, "../../ui/record/design.md"),
				goldenRecordPath: "# Title\n\nStatus: Historical\n",
			},
			listed: []string{uiDecision, goldenRecordPath}},
		{name: "status-disagrees-with-decision", wantExit: 1, cfg: ui,
			files: goldenFiles{
				uiDecision:       goldenDecision("DL-001", goldenActive, "../../ui/record/design.md"),
				goldenRecordPath: "# Title\n\nStatus: Superseded by ../next/design.md\n",
				goldenNextRec:    goldenSmallBody,
			},
			listed: []string{uiDecision, goldenRecordPath}},
		{name: "status-supersession-unresolved", wantExit: 1, cfg: ui,
			files: goldenFiles{
				uiDecision:       goldenDecision("DL-001", goldenActive, "../../ui/record/design.md"),
				goldenRecordPath: "# Title\n\nStatus: Superseded by ../next/design.md\n",
			},
			listed: []string{uiDecision, goldenRecordPath}},
		{name: "no-valid-decision-file", wantExit: 1, cfg: ui,
			files: goldenFiles{
				uiDecision:       goldenDecision("DL-001", goldenActive, "../../ui/record/design.md"),
				goldenServerRec:  "# Title\n",
				goldenRecordPath: goldenSmallBody,
			},
			listed: []string{uiDecision, goldenServerRec}},
		{name: "touch-coupling-declaration", wantExit: 1, cfg: ui,
			files:   goldenFiles{uiDecision: goldenDecision("DL-001", goldenActive, "../../ui/record/design.md"), goldenRecordPath: goldenSmallBody},
			listed:  []string{uiDecision, goldenRecordPath},
			changed: core.Changed{Files: []string{goldenRecordPath}, Body: mo.Some("unrelated text"), HeadBranch: "feature/change"}},
		{name: "touch-coupling-branch-prefix", wantExit: 1, cfg: ui,
			files:   goldenFiles{uiDecision: goldenDecision("DL-001", goldenActive, "../../ui/record/design.md"), goldenRecordPath: goldenSmallBody},
			listed:  []string{uiDecision, goldenRecordPath},
			changed: core.Changed{Files: []string{goldenRecordPath}, Body: mo.Some(""), HeadBranch: "feature/renovate/x"}},
		{name: "merge-conflict-marker", wantExit: 1, cfg: ui,
			files: goldenFiles{
				uiDecision:       goldenDecision("DL-001", goldenActive, "../../ui/record/design.md") + "\n<<<<<<< HEAD\n=======\n>>>>>>> theirs",
				goldenRecordPath: goldenSmallBody,
			},
			listed: []string{uiDecision}},
	}
}

// TestGoldenViolationMessages compares the full stderr of Once to each TS golden.
func TestGoldenViolationMessages(t *testing.T) {
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			fake := newGateFake(tc.files)
			fake.paths = slices.Clone(tc.listed)
			fake.changed = tc.changed
			code, stdout, stderr := runGate(t, fake, tc.cfg)
			if code != tc.wantExit {
				t.Fatalf("exit = %d, want %d; stderr:\n%s", code, tc.wantExit, stderr)
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want empty", stdout)
			}
			want, err := os.ReadFile(filepath.Join("..", "testdata", "ts-golden", tc.name+".txt"))
			if err != nil {
				t.Fatal(err)
			}
			if stderr != string(want) {
				t.Fatalf("stderr mismatch\n got: %q\nwant: %q", stderr, want)
			}
		})
	}
}
