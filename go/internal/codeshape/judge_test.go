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
			Files:     map[string]Entry{"large.go": {Value: 501, Reason: "split by concern"}},
			Dirs:      map[string]Entry{},
			Functions: map[string]map[string][]FunctionEntry{},
			Present:   false,
		}
		snapshot := Snapshot{
			Files:     map[string]int{"large.go": 502},
			Dirs:      map[string]int{},
			Functions: map[Metric]map[string][]Entry{},
		}
		findings := Judge(config, allow, emptyAllowlist(), snapshot)
		if !hasFinding(findings, "large.go", MetricFiles, 502, 501, "grew") {
			t.Fatalf("missing grew finding: %#v", findings)
		}
	})
	t.Run("entry at limit fails with remove", func(t *testing.T) {
		allow := Allowlist{
			Files:     map[string]Entry{"small.go": {Value: 501, Reason: "split by concern"}},
			Dirs:      map[string]Entry{},
			Functions: map[string]map[string][]FunctionEntry{},
			Present:   false,
		}
		snapshot := Snapshot{
			Files:     map[string]int{"small.go": 500},
			Dirs:      map[string]int{},
			Functions: map[Metric]map[string][]Entry{},
		}
		findings := Judge(config, allow, emptyAllowlist(), snapshot)
		if !hasFinding(findings, "small.go", MetricFiles, 500, 500, "remove") {
			t.Fatalf("missing remove finding: %#v", findings)
		}
	})
	t.Run("missing entry fails with remove", func(t *testing.T) {
		allow := Allowlist{
			Files:     map[string]Entry{"gone.go": {Value: 501, Reason: "split by concern"}},
			Dirs:      map[string]Entry{},
			Functions: map[string]map[string][]FunctionEntry{},
			Present:   false,
		}
		findings := Judge(config, allow, emptyAllowlist(), Snapshot{
			Files:     map[string]int{},
			Dirs:      map[string]int{},
			Functions: map[Metric]map[string][]Entry{},
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
			Files:     map[string]Entry{"large.go": {Value: 501, Reason: "split by concern"}},
			Dirs:      map[string]Entry{},
			Functions: map[string]map[string][]FunctionEntry{},
			Present:   false,
		}
		snapshot := Snapshot{
			Files:     map[string]int{"large.go": 501},
			Dirs:      map[string]int{},
			Functions: map[Metric]map[string][]Entry{},
		}
		base := emptyAllowlist()
		base.Present = true
		findings := Judge(config, allow, base, snapshot)
		if !hasFinding(findings, "large.go", MetricFiles, 501, 0, "do not add") {
			t.Fatalf("missing added-entry finding: %#v", findings)
		}
	})
	t.Run("base rejects raised entry", func(t *testing.T) {
		allow := Allowlist{
			Files:     map[string]Entry{"large.go": {Value: 502, Reason: "split by concern"}},
			Dirs:      map[string]Entry{},
			Functions: map[string]map[string][]FunctionEntry{},
			Present:   false,
		}
		base := Allowlist{
			Files:     map[string]Entry{"large.go": {Value: 501, Reason: "split by concern"}},
			Dirs:      map[string]Entry{},
			Functions: map[string]map[string][]FunctionEntry{},
			Present:   true,
		}
		snapshot := Snapshot{
			Files:     map[string]int{"large.go": 502},
			Dirs:      map[string]int{},
			Functions: map[Metric]map[string][]Entry{},
		}
		findings := Judge(config, allow, base, snapshot)
		if !hasFinding(findings, "large.go", MetricFiles, 502, 501, "do not add") {
			t.Fatalf("missing raised-entry finding: %#v", findings)
		}
	})
	t.Run("base absence is head only", func(t *testing.T) {
		allow := Allowlist{
			Files:     map[string]Entry{"large.go": {Value: 501, Reason: "split by concern"}},
			Dirs:      map[string]Entry{},
			Functions: map[string]map[string][]FunctionEntry{},
			Present:   false,
		}
		snapshot := Snapshot{
			Files:     map[string]int{"large.go": 501},
			Dirs:      map[string]int{},
			Functions: map[Metric]map[string][]Entry{},
		}
		if findings := Judge(config, allow, emptyAllowlist(), snapshot); len(findings) != 0 {
			t.Fatalf("head-only findings = %#v", findings)
		}
	})
	t.Run("base rejects entries for missing subjects", func(t *testing.T) {
		allow := Allowlist{
			Files:     map[string]Entry{"gone.go": {Value: 501, Reason: "split by concern"}},
			Dirs:      map[string]Entry{},
			Functions: map[string]map[string][]FunctionEntry{},
			Present:   false,
		}
		base := emptyAllowlist()
		base.Present = true
		snapshot := Snapshot{
			Files:     map[string]int{},
			Dirs:      map[string]int{},
			Functions: map[Metric]map[string][]Entry{},
		}
		findings := Judge(config, allow, base, snapshot)
		if !hasFinding(findings, "gone.go", MetricFiles, 501, 0, "do not add") {
			t.Fatalf("missing base finding: %#v", findings)
		}
	})
	t.Run("base rejects function additions", func(t *testing.T) {
		allow := Allowlist{
			Files: map[string]Entry{},
			Dirs:  map[string]Entry{},
			Functions: map[string]map[string][]FunctionEntry{
				"go-lines": {
					"a.go": {{ID: "f", Value: 61, Reason: "split by concern"}},
				},
			},
			Present: false,
		}
		base := emptyAllowlist()
		base.Present = true
		snapshot := Snapshot{
			Files:     map[string]int{},
			Dirs:      map[string]int{},
			Functions: map[Metric]map[string][]Entry{},
		}
		findings := Judge(config, allow, base, snapshot)
		if !hasFinding(findings, "a.go:f", Metric("go-lines"), 61, 0, "do not add") {
			t.Fatalf("missing function base finding: %#v", findings)
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
