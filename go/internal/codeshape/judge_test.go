// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import (
	"strings"
	"testing"
)

func TestJudge(t *testing.T) {
	config := testConfig()
	t.Run("growth fails with grew", func(t *testing.T) {
		allow := Allowlist{
			Files: map[string]Entry{"large.go": {ID: "", Value: 501, Reason: "split by concern"}},
			Dirs:  map[string]Entry{}, Functions: nil, Present: false,
		}
		snapshot := Snapshot{
			Files:     map[string]int{"large.go": 502},
			Dirs:      map[string]int{},
			Functions: nil,
		}
		findings := Judge(config, allow, emptyAllowlist(), snapshot)
		if !hasFinding(findings, "large.go", MetricFiles, 502, 501, "grew") {
			t.Fatalf("missing grew finding: %#v", findings)
		}
	})
	t.Run("entry at limit fails with remove", func(t *testing.T) {
		allow := Allowlist{
			Files: map[string]Entry{"small.go": {ID: "", Value: 501, Reason: "split by concern"}},
			Dirs:  map[string]Entry{}, Functions: nil, Present: false,
		}
		snapshot := Snapshot{
			Files:     map[string]int{"small.go": 500},
			Dirs:      map[string]int{},
			Functions: nil,
		}
		findings := Judge(config, allow, emptyAllowlist(), snapshot)
		if !hasFinding(findings, "small.go", MetricFiles, 500, 500, "remove") {
			t.Fatalf("missing remove finding: %#v", findings)
		}
	})
	t.Run("missing entry fails with remove", func(t *testing.T) {
		allow := Allowlist{
			Files: map[string]Entry{"gone.go": {ID: "", Value: 501, Reason: "split by concern"}},
			Dirs:  map[string]Entry{}, Functions: nil, Present: false,
		}
		findings := Judge(config, allow, emptyAllowlist(), Snapshot{
			Files:     map[string]int{},
			Dirs:      map[string]int{},
			Functions: nil,
		})
		if !hasFinding(findings, "gone.go", MetricFiles, 0, 500, "remove") {
			t.Fatalf("missing stale entry finding: %#v", findings)
		}
	})
}

func TestJudgeBase(t *testing.T) {
	config := testConfig()
	t.Run("base rejects an added entry", func(t *testing.T) {
		allow := Allowlist{
			Files: map[string]Entry{"large.go": {ID: "", Value: 501, Reason: "split by concern"}},
			Dirs:  map[string]Entry{}, Functions: nil, Present: false,
		}
		base := emptyAllowlist()
		base.Present = true
		findings := Judge(config, allow, base, Snapshot{
			Files:     map[string]int{"large.go": 501},
			Dirs:      map[string]int{},
			Functions: nil,
		})
		if !hasFinding(findings, "large.go", MetricFiles, 501, 0, "do not add") {
			t.Fatalf("missing added-entry finding: %#v", findings)
		}
	})
	t.Run("base rejects raised entry", func(t *testing.T) {
		allow := Allowlist{
			Files: map[string]Entry{"large.go": {ID: "", Value: 502, Reason: "split by concern"}},
			Dirs:  map[string]Entry{}, Functions: nil, Present: false,
		}
		base := Allowlist{
			Files: map[string]Entry{"large.go": {ID: "", Value: 501, Reason: "split by concern"}},
			Dirs:  map[string]Entry{}, Functions: nil, Present: true,
		}
		findings := Judge(config, allow, base, Snapshot{
			Files:     map[string]int{"large.go": 502},
			Dirs:      map[string]int{},
			Functions: nil,
		})
		if !hasFinding(findings, "large.go", MetricFiles, 502, 501, "do not add") {
			t.Fatalf("missing raised-entry finding: %#v", findings)
		}
	})
	t.Run("base absence is head only", func(t *testing.T) {
		allow := Allowlist{
			Files: map[string]Entry{"large.go": {ID: "", Value: 501, Reason: "split by concern"}},
			Dirs:  map[string]Entry{}, Functions: nil, Present: false,
		}
		snapshot := Snapshot{
			Files:     map[string]int{"large.go": 501},
			Dirs:      map[string]int{},
			Functions: nil,
		}
		if findings := Judge(config, allow, emptyAllowlist(), snapshot); len(findings) != 0 {
			t.Fatalf("head-only findings = %#v", findings)
		}
	})
	t.Run("base rejects entries for missing subjects", func(t *testing.T) {
		allow := Allowlist{
			Files: map[string]Entry{"gone.go": {ID: "", Value: 501, Reason: "split by concern"}},
			Dirs:  map[string]Entry{}, Functions: nil, Present: false,
		}
		base := emptyAllowlist()
		base.Present = true
		findings := Judge(config, allow, base, Snapshot{
			Files:     map[string]int{},
			Dirs:      map[string]int{},
			Functions: nil,
		})
		if !hasFinding(findings, "gone.go", MetricFiles, 501, 0, "do not add") {
			t.Fatalf("missing base finding: %#v", findings)
		}
	})
}

func hasFinding(findings []Finding, path string, metric Metric, value, limit int, fragment string) bool {
	for _, finding := range findings {
		if finding.Path == path &&
			finding.Metric == metric &&
			finding.Value == value &&
			finding.Limit == limit &&
			strings.Contains(finding.Fix, fragment) {
			return true
		}
	}
	return false
}

func TestJudgeFunctionRatchets(t *testing.T) {
	cfg := testConfig()
	cfg.Functions.Go.Cognitive = 20
	entries := []Entry{{ID: "first", Value: 30, Reason: ""}, {ID: "second", Value: 30, Reason: ""}}
	allow := Allowlist{Files: nil, Dirs: nil,
		Functions: FunctionMeasurements{MetricGoCognitive: {"f.go": entries}}, Present: false}
	values := FunctionMeasurements{MetricGoCognitive: {"f.go": {
		{ID: "first", Value: 31, Reason: ""}, {ID: "second", Value: 29, Reason: ""},
	}}}
	findings := Judge(cfg, allow, emptyAllowlist(), Snapshot{Files: nil, Dirs: nil, Functions: values})
	if len(findings) != 1 || !hasFinding(findings, "f.go#first", MetricGoCognitive, 31, 30, "grew") {
		t.Fatalf("independent ratchets: %#v", findings)
	}
	values[MetricGoCognitive]["f.go"][0].Value = 30
	findings = Judge(cfg, allow, emptyAllowlist(), Snapshot{Files: nil, Dirs: nil, Functions: values})
	if len(findings) != 0 {
		t.Fatalf("unchanged and shrunk functions: %#v", findings)
	}
}

func TestJudgeFunctionRenameAndStale(t *testing.T) {
	cfg := testConfig()
	cfg.Functions.Go.Lines = 60
	base := Allowlist{Files: nil, Dirs: nil, Present: true,
		Functions: FunctionMeasurements{MetricGoLines: {"f.go": {{ID: "old", Value: 70, Reason: ""}}}}}
	head := Allowlist{Files: nil, Dirs: nil, Present: false, Functions: FunctionMeasurements{MetricGoLines: {"f.go": {
		{ID: "new", Value: 70, Reason: ""}, {ID: "old", Value: 70, Reason: ""},
	}}}}
	values := FunctionMeasurements{MetricGoLines: {"f.go": {{ID: "new", Value: 70, Reason: ""}}}}
	findings := Judge(cfg, head, base, Snapshot{Files: nil, Dirs: nil, Functions: values})
	if !hasFinding(findings, "f.go#old", MetricGoLines, 0, 60, "remove") ||
		!hasFinding(findings, "f.go#new", MetricGoLines, 70, 0, "do not add") {
		t.Fatalf("rename findings: %#v", findings)
	}
}
