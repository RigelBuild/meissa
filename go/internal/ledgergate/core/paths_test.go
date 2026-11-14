// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"testing"

	"github.com/samber/mo"
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
		decisionDir + "/README.md":           PathDecisionsReadme,
		decisionDir + "/ui/DL-001.md":        PathDecision,
		decisionDir + "/ui/nested/DL-001.md": PathMisplaced,
		decisionDir + "/ui/notes.txt":        PathMisplaced,
		decisionDir + "/DL-001.md":           PathMisplaced,
		decisionDir + "/foo.md":              PathMisplaced,
		decisionDir + "/ui/DECISIONS.md":     PathLegacyLedger,
		"docs/designs/DECISIONS.md":          PathLegacyLedger,
		"docs/designs/server/DECISIONS.md":   PathLegacyLedger,
		"docs/designs/ui/DL-009.md":          PathMisplaced,
		"docs/designs/ui/record/design.md":   PathOther,
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
