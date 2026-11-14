// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:exhaustruct_v5,lll // Partial decision fixtures keep each case focused.
package core

import (
	"testing"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/ledger"
)

func TestResolveRecordRelative(t *testing.T) {
	cases := []struct {
		name, record, pointer, want string
		ok                          bool
	}{
		{"nested sibling", "ui/nested/record/design.md", "../sibling/design.md", "ui/nested/sibling/design.md", true},
		{"cross-bucket", "ui/record/design.md", "../../server/next/design.md", "server/next/design.md", true},
		{"flat record", "ui/record.md", "other.md", "ui/other.md", true},
		{"escape above root", "ui/record/design.md", "../../../escape.md", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveRecordRelative(tc.record, tc.pointer)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("resolveRecordRelative = (%q, %t), want (%q, %t)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestClassifyDesignPath(t *testing.T) {
	cases := map[string]DesignPathKind{
		decisionDir + "/README.md":                     PathDecisionsReadme,
		decisionDir + "/ui/DL-001.md":                  PathDecision,
		decisionDir + "/ui/nested/DL-001.md":           PathDecision,
		decisionDir + "/ui/notes.txt":                  PathMisplaced,
		decisionDir + "/DL-001.md":                     PathMisplaced,
		decisionDir + "/foo.md":                        PathMisplaced,
		decisionDir + "/ui/DECISIONS.md":               PathLegacyLedger,
		"docs/designs/DECISIONS.md":                    PathLegacyLedger,
		"docs/designs/server/DECISIONS.md":             PathLegacyLedger,
		"docs/designs/ui/DL-009.md":                    PathMisplaced,
		"docs/designs/ui/record/design.md":             PathOther,
		"docs/archive/platform/decisions/ui/DL-900.md": PathOther,
	}
	for file, want := range cases {
		if got := ClassifyDesignPath(file, testConfig); got != want {
			t.Errorf("ClassifyDesignPath(%q) = %q, want %q", file, got, want)
		}
	}
}

func TestDecisionTreeIsNotGovernedRecord(t *testing.T) {
	if TouchesRecord(decisionDir+"/ui/DL-001.md", testConfig) {
		t.Error("decision file touches a record")
	}
	if TouchesRecord("docs/designs/DECISIONS.md", testConfig) {
		t.Error("DECISIONS.md touches a record")
	}
}

func TestTouchesRecord(t *testing.T) {
	cases := map[string]bool{
		"docs/designs/ui/example.md":                      true,
		"docs/designs/ui/example/design.md":               true,
		"docs/designs/ui/example/other.md":                true,
		"docs/designs/infra/runtime/sample/microvm-v3.md": true,
		"docs/designs/gamma/x/design.md":                  true,
		"docs/designs/CONTRIBUTING.md":                    false,
		"docs/designs/nope/example.md":                    false,
		"docs/designs/ui/subgroup/flat.md":                true,
		"docs/designs/not-governed/record/design.md":      false,
		"docs/designs/ui/":                                false,
		"docs/not-designs/ui/record/design.md":            false,
		"docs/designs/ui/record/image.png":                false,
	}
	for path, want := range cases {
		if got := TouchesRecord(path, testConfig); got != want {
			t.Errorf("TouchesRecord(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestTouchesRecordAtDepthOne(t *testing.T) {
	cases := map[string]bool{
		"docs/designs/alpha/README.md":              false,
		"docs/designs/alpha/record.md":              true,
		"docs/designs/alpha/record/README.md":       true,
		"docs/designs/alpha/decisions/ui/DL-001.md": false,
	}
	for path, want := range cases {
		if got := TouchesRecord(path, depthOneConfig); got != want {
			t.Errorf("TouchesRecord(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestTouchesRecordDepthZeroAreaReadmeIsRecord(t *testing.T) {
	if !TouchesRecord("docs/designs/ui/README.md", testConfig) {
		t.Error("governed area README is not a record at depth zero")
	}
}

func TestReadmeOnlySurfaceNeedsNoDecision(t *testing.T) {
	violations := evaluateCorpus(BuildDecisionCorpus(nil, nil),
		[]RecordHeader{{Path: "docs/designs/alpha/README.md", StatusLine: mo.None[string](), Line: 0}},
		noChange, nil, depthOneConfig, noPRDetails())
	if len(violations) != 0 {
		t.Fatalf("violations = %+v, want none", violations)
	}
}

func TestDepthOneLayoutRequiresDecisionForSurfaceWithRecords(t *testing.T) {
	firstDecision := "docs/designs/alpha/decisions/ui/DL-001.md"
	alphaRecord := "docs/designs/alpha/ui/record.md"
	betaRecord := "docs/designs/beta/ui/record.md"
	if got := ClassifyDesignPath(firstDecision, depthOneConfig); got != PathDecision {
		t.Fatalf("ClassifyDesignPath = %q, want decision", got)
	}
	corpus := BuildDecisionCorpus([]File{{
		Path: firstDecision,
		Text: decisionAt(firstDecision, "../../../ui/record.md"),
	}}, nil)
	failures := evaluateCorpus(corpus,
		[]RecordHeader{recordOf(alphaRecord, ""), recordOf(betaRecord, "")},
		noChange, nil, depthOneConfig, noPRDetails())
	if !hasFile(failures, "docs/designs/beta/decisions") {
		t.Fatalf("violations = %+v, want beta decision missing", failures)
	}
	if hasFile(failures, "docs/designs/alpha/decisions") {
		t.Fatalf("violations = %+v, alpha should pass", failures)
	}
}

func TestDepthOneDecisionMayCiteRecordOnAnotherSurface(t *testing.T) {
	path := "docs/designs/alpha/decisions/ui/DL-001.md"
	corpus := BuildDecisionCorpus([]File{decisionFile(decisionOptions{
		id: "", area: "", record: "../../../beta/record/design.md", status: "", decision: "", path: path,
	})}, nil)
	got := evaluateCorpus(corpus, nil, noChange, smallRecord, depthOneConfig, noPRDetails())
	if hasMessage(got, "decision area must match") {
		t.Fatalf("violations = %+v, want no area mismatch", got)
	}
}

func TestStrayPathsReported(t *testing.T) {
	corpus := BuildDecisionCorpus(nil, []StrayPath{
		{Path: "docs/designs/DECISIONS.md", Kind: PathLegacyLedger},
		{Path: "docs/designs/ui/DL-009.md", Kind: PathMisplaced},
	})
	got := evalDefault(corpus, nil)
	if !hasMessage(got, "DECISIONS.md is retired: decision files belong in the configured decisions tree") {
		t.Errorf("legacy stray not reported: %+v", got)
	}
	if countMessage(got, "misplaced design file") != 1 {
		t.Errorf("misplaced stray count wrong: %+v", got)
	}
}

func TestN1DecisionLocation(t *testing.T) {
	cfg := testConfig
	cfg.ArchiveRoots = []string{"docs/archive"}
	cases := []struct {
		name    string
		file    string
		wantOK  bool
		wantLoc Location
	}{
		{"flat decision", decisionDir + "/ui/DL-001.md", true, Location{Surface: "", Area: "ui", Sub: ""}},
		{"nested decision", decisionDir + "/ui/record/DL-001.md", true, Location{Surface: "", Area: "ui", Sub: "record"}},
		{
			"deeper than one sub-area", decisionDir + "/ui/record/deep/DL-001.md", false,
			Location{Surface: "", Area: "", Sub: ""},
		},
		{"non-decision file", "docs/designs/ui/record/design.md", false, Location{Surface: "", Area: "", Sub: ""}},
		{"archive root path", "docs/archive/ui/decisions/ui/record/DL-900.md", true, Location{Surface: "", Area: "ui", Sub: "record"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, ok := DecisionLocation(test.file, cfg)
			if ok != test.wantOK {
				t.Fatalf("DecisionLocation(%q) ok = %v, want %v", test.file, ok, test.wantOK)
			}
			if ok && got != test.wantLoc {
				t.Fatalf("DecisionLocation(%q) = %+v, want %+v", test.file, got, test.wantLoc)
			}
		})
	}
}

func TestDecisionLocationArchiveLayouts(t *testing.T) {
	depthZeroArchive := testConfig
	depthZeroArchive.ArchiveRoots = []string{"docs/archive"}
	depthOneArchive := depthOneConfig
	depthOneArchive.ArchiveRoots = []string{"docs/archive"}
	noArchiveRoots := testConfig
	noArchiveRoots.ArchiveRoots = []string{}

	cases := []struct {
		name    string
		file    string
		cfg     Config
		wantLoc Location
		wantOK  bool
	}{
		{
			name:    "depth zero flat archive decision",
			file:    "docs/archive/platform/records/decisions/ui/DL-900.md",
			cfg:     depthZeroArchive,
			wantLoc: Location{Surface: "", Area: "ui", Sub: ""},
			wantOK:  true,
		},
		{
			name:    "depth zero archived sub-area",
			file:    "docs/archive/platform/records/decisions/ui/record/DL-901.md",
			cfg:     depthZeroArchive,
			wantLoc: Location{Surface: "", Area: "ui", Sub: "record"},
			wantOK:  true,
		},
		{
			name:    "depth one flat archive decision",
			file:    "docs/archive/platform/decisions/ui/DL-902.md",
			cfg:     depthOneArchive,
			wantLoc: Location{Surface: "platform", Area: "ui", Sub: ""},
			wantOK:  true,
		},
		{
			name:    "depth one archived sub-area after preserved prefix",
			file:    "docs/archive/platform/records/decisions/ui/record/DL-903.md",
			cfg:     depthOneArchive,
			wantLoc: Location{Surface: "platform", Area: "ui", Sub: "record"},
			wantOK:  true,
		},
		{
			name:   "path outside archive root boundary",
			file:   "docs/archive-old/decisions/ui/DL-904.md",
			cfg:    depthZeroArchive,
			wantOK: false,
		},
		{
			name:   "archive roots empty",
			file:   "docs/archive/decisions/ui/DL-905.md",
			cfg:    noArchiveRoots,
			wantOK: false,
		},
		{
			name:   "too deep under archive decisions",
			file:   "docs/archive/decisions/ui/record/deep/DL-906.md",
			cfg:    depthZeroArchive,
			wantOK: false,
		},
		{
			name:   "non-decision archive basename",
			file:   "docs/archive/decisions/ui/record/notes.md",
			cfg:    depthZeroArchive,
			wantOK: false,
		},
		{
			name:   "bad archived sub-area slug",
			file:   "docs/archive/decisions/ui/BadSlug/DL-907.md",
			cfg:    depthZeroArchive,
			wantOK: false,
		},
		{
			name:    "live decision remains located",
			file:    "docs/designs/decisions/ui/DL-908.md",
			cfg:     depthZeroArchive,
			wantLoc: Location{Surface: "", Area: "ui", Sub: ""},
			wantOK:  true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, ok := DecisionLocation(test.file, test.cfg)
			if ok != test.wantOK {
				t.Fatalf("DecisionLocation(%q) ok = %v, want %v", test.file, ok, test.wantOK)
			}
			if ok && got != test.wantLoc {
				t.Fatalf("DecisionLocation(%q) = %+v, want %+v", test.file, got, test.wantLoc)
			}
		})
	}
}

func TestClassifyDesignPathRejectsDeeperThanOneSubArea(t *testing.T) {
	file := decisionDir + "/ui/nested/deep/DL-001.md"
	if got := ClassifyDesignPath(file, testConfig); got != PathMisplaced {
		t.Fatalf("ClassifyDesignPath(%q) = %q, want misplaced", file, got)
	}
}

func TestN1DecisionLocationDepthOne(t *testing.T) {
	got, ok := DecisionLocation("docs/designs/platform/decisions/ui/record/DL-001.md", depthOneConfig)
	if !ok || got != (Location{Surface: "platform", Area: "ui", Sub: "record"}) {
		t.Fatalf("nested DecisionLocation = %+v ok=%v", got, ok)
	}
	flat, ok := DecisionLocation("docs/designs/platform/decisions/ui/DL-001.md", depthOneConfig)
	if !ok || flat != (Location{Surface: "platform", Area: "ui", Sub: ""}) {
		t.Fatalf("flat DecisionLocation = %+v ok=%v", flat, ok)
	}
}

func TestN1RequireSubAreaFailsFlatFileAndDefaultPasses(t *testing.T) {
	flat := decisionOptions{path: decisionDir + "/ui/DL-001.md", record: "../../ui/record/design.md"}
	strict := testConfig
	strict.RequireSubArea = true
	if got := evaluateCorpus(corpusOf(flat), nil, noChange, nil, strict, mo.None[PullRequestDetails]()); !hasMessage(got, "must sit in a record sub-area (requireSubArea)") {
		t.Fatalf("flat file under requireSubArea not reported: %+v", got)
	}
	if got := evalDefault(corpusOf(flat), nil); hasMessage(got, "requireSubArea") {
		t.Fatalf("flat file under default flagged: %+v", got)
	}
}

func TestDecisionSubAreaMustBeRecordSlug(t *testing.T) {
	good := decisionFile(decisionOptions{path: decisionDir + "/ui/record/DL-001.md", record: "../../ui/record/design.md"})
	if got := evaluateCorpus(BuildDecisionCorpus([]File{good}, nil), nil, noChange, nil, testConfig, mo.None[PullRequestDetails]()); hasMessage(got, "must be the Record slug") {
		t.Fatalf("matching sub-area flagged: %+v", got)
	}
	bad := decisionFile(decisionOptions{path: decisionDir + "/ui/other/DL-001.md", record: "../../ui/record/design.md"})
	got := evaluateCorpus(BuildDecisionCorpus([]File{bad}, nil), nil, noChange, nil, testConfig, mo.None[PullRequestDetails]())
	if !hasMessage(got, "decision sub-area other must be the Record slug record") {
		t.Fatalf("mismatched sub-area not reported: %+v", got)
	}
}

func TestN1PlacementFromRecordAcceptsMultiRecordDecision(t *testing.T) {
	got := evaluateCorpus(corpusOf(decisionOptions{
		path:   decisionDir + "/ui/record/DL-001.md",
		record: "../../ui/record/design.md",
	}), []RecordHeader{
		ParseRecordHeader("docs/designs/ui/record/design.md", "# Record\n\nBody.\n"),
		ParseRecordHeader("docs/designs/ui/other/design.md", "# Record\n\nSee [DL-001](../../decisions/ui/record/DL-001.md).\n"),
	}, noChange, nil, testConfig, mo.None[PullRequestDetails]())
	if hasMessage(got, "must be the Record slug") {
		t.Fatalf("canonical placement flagged with a second citer: %+v", got)
	}
}

func TestN1PlacementJudgedFromCanonicalNotSecondCiter(t *testing.T) {
	got := evaluateCorpus(corpusOf(decisionOptions{
		path:   decisionDir + "/ui/other/DL-001.md",
		record: "../../ui/record/design.md",
	}), []RecordHeader{
		ParseRecordHeader("docs/designs/ui/record/design.md", "# Record\n\nBody.\n"),
		ParseRecordHeader("docs/designs/ui/other/design.md", "# Record\n\nSee [DL-001](../../decisions/ui/record/DL-001.md).\n"),
	}, noChange, nil, testConfig, mo.None[PullRequestDetails]())
	if !hasMessage(got, "decision sub-area other must be the Record slug record") {
		t.Fatalf("placement under the second citer not reported: %+v", got)
	}
}

func TestN1PlacementFailsWhenBasenameChangesWithoutMovingDecision(t *testing.T) {
	got := evaluateCorpus(corpusOf(decisionOptions{
		path:   decisionDir + "/ui/record/DL-001.md",
		record: "../../ui/record/fork-releases.md",
	}), nil, noChange, nil, testConfig, mo.None[PullRequestDetails]())
	if !hasMessage(got, "decision sub-area record must be the Record slug fork-releases") {
		t.Fatalf("basename-changing move not reported: %+v", got)
	}
}

const depthOneDecision = "docs/designs/platform/decisions/ui/record/DL-001.md"

func TestN1DepthOneNestedDecisionPassesWhenSubIsSlug(t *testing.T) {
	got := evaluateCorpus(corpusOf(decisionOptions{path: depthOneDecision, record: "../../../ui/record/design.md"}), nil, noChange, nil, depthOneConfig, mo.None[PullRequestDetails]())
	if len(got) != 0 {
		t.Fatalf("depth-one nested decision with slug sub-area flagged: %+v", got)
	}
}

func TestN1DepthOneSubAreaMismatchIsReported(t *testing.T) {
	got := evaluateCorpus(corpusOf(decisionOptions{path: "docs/designs/platform/decisions/ui/other/DL-001.md", record: "../../../ui/record/design.md"}), nil, noChange, nil, depthOneConfig, mo.None[PullRequestDetails]())
	if !hasMessage(got, "decision sub-area other must be the Record slug record") {
		t.Fatalf("depth-one sub-area mismatch not reported: %+v", got)
	}
}

func TestN1DepthOneRequireSubAreaFailsFlatFile(t *testing.T) {
	cfg := depthOneConfig
	cfg.RequireSubArea = true
	got := evaluateCorpus(corpusOf(decisionOptions{
		path:   "docs/designs/platform/decisions/ui/DL-001.md",
		record: "../../ui/record/design.md",
	}), nil, noChange, nil, cfg, mo.None[PullRequestDetails]())
	if !hasMessage(got, "decision file must sit in a record sub-area (requireSubArea)") {
		t.Fatalf("depth-one flat decision under requireSubArea not reported: %+v", got)
	}
}

func TestN1PlacementFlatDesignRecordKeepsParentSlug(t *testing.T) {
	if slug := ledger.RecordSlug("docs/designs/platform/oss-forks/fork-releases/design.md"); slug != "fork-releases" {
		t.Fatalf("RecordSlug(design.md) = %q, want fork-releases", slug)
	}
	got := evaluateCorpus(corpusOf(decisionOptions{
		path:   decisionDir + "/ui/record/DL-001.md",
		record: "../../ui/record/design.md",
	}), nil, noChange, nil, testConfig, mo.None[PullRequestDetails]())
	if hasMessage(got, "must be the Record slug") {
		t.Fatalf("decision under parent-directory slug flagged: %+v", got)
	}
}

func TestN1TitleScopeMatrix(t *testing.T) {
	untitled := decisionOptions{path: decisionDir + "/ui/record/DL-001.md", record: "../../ui/record/design.md"}
	changedFile := Changed{Files: []string{untitled.path}, Body: mo.None[string](), HeadBranch: ""}
	cases := []struct {
		name    string
		scope   string
		changed Changed
		fails   bool
	}{
		{"changed fails an untitled changed file", "changed", changedFile, true},
		{"changed passes an untitled unchanged file", "changed", noChange, false},
		{"all fails an untitled changed file", "all", changedFile, true},
		{"all fails an untitled unchanged file", "all", noChange, true},
		{"off passes an untitled changed file", "off", changedFile, false},
		{"off passes an untitled unchanged file", "off", noChange, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cfg := testConfig
			cfg.TitleScope = test.scope
			got := evaluateCorpus(corpusOf(untitled), nil, test.changed, nil, cfg, mo.None[PullRequestDetails]())
			if hasMessage(got, "decision needs a title") != test.fails {
				t.Fatalf("scope %s violations = %+v, fails want %v", test.scope, got, test.fails)
			}
		})
	}
}

func TestArchivedSupersessionFailsWhenArchiveRootsSet(t *testing.T) {
	cfg := testConfig
	cfg.ArchiveRoots = []string{"docs/archive"}
	row, err := ledger.ParseDecisionFile(
		"docs/archive/ui/decisions/ui/record/DL-900.md",
		[]byte(decisionAt("docs/archive/ui/decisions/ui/record/DL-900.md", "../../../../../../designs/ui/record/design.md")),
	)
	if err != nil {
		t.Fatalf("archived fixture did not parse: %v", err)
	}
	corpus := corpusOf(decisionOptions{id: "DL-001", status: "Superseded by DL-900 (Matt, 2026-07-22)"})
	corpus.Archived = []ledger.DecisionRow{row}
	got := evaluateCorpus(corpus, nil, noChange, nil, cfg, mo.None[PullRequestDetails]())
	want := "DL-001: live decision cannot be superseded by an archived decision (docs/archive/ui/decisions/ui/record/DL-900.md)"
	if !hasMessage(got, want) {
		t.Fatalf("violations = %+v, want %q", got, want)
	}
}

func TestArchivedSupersessionPassesWithoutArchiveRoots(t *testing.T) {
	corpus := corpusOf(decisionOptions{id: "DL-001", status: "Superseded by DL-900 (Matt, 2026-07-22)"})
	got := evalDefault(corpus, nil)
	if hasMessage(got, "cannot be superseded by an archived decision") {
		t.Fatalf("archived message fired with archiveRoots empty: %+v", got)
	}
	if !hasMessage(got, "DL-001: Superseded by DL-900, which is not a decision file") {
		t.Fatalf("violations = %+v, want existing not-a-decision message", got)
	}
}

func TestN1ArchiveRecordPassesOnlyWithArchiveRootsAndTarget(t *testing.T) {
	archived := decisionOptions{
		path: decisionDir + "/ui/record/DL-001.md", record: "../../../../archive/ui/record/design.md",
	}
	archiveRead := func(path string) (RecordContent, bool) {
		return RecordContent{Headings: []string{"present"}, SizeBytes: 100}, path == "docs/archive/ui/record/design.md"
	}
	withRoots := testConfig
	withRoots.ArchiveRoots = []string{"docs/archive"}
	got := evaluateCorpus(corpusOf(archived), nil, noChange, archiveRead, withRoots, mo.None[PullRequestDetails]())
	if hasMessage(got, "Record path must be under") {
		t.Fatalf("archive record with archiveRoots and target flagged: %+v", got)
	}
	withoutRoots := testConfig
	got = evaluateCorpus(corpusOf(archived), nil, noChange, archiveRead, withoutRoots, mo.None[PullRequestDetails]())
	if !hasMessage(got, "Record path must be under docs/designs or an archive root") {
		t.Fatalf("archive record without archiveRoots not reported: %+v", got)
	}
	missingTarget := func(string) (RecordContent, bool) { return RecordContent{}, false }
	got = evaluateCorpus(corpusOf(archived), nil, noChange, missingTarget, withRoots, mo.None[PullRequestDetails]())
	if !hasMessage(got, "Record path must be under docs/designs or an archive root") {
		t.Fatalf("archive record with a missing target not reported: %+v", got)
	}
}
