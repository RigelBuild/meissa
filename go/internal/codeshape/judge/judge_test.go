// SPDX-License-Identifier: MIT OR Apache-2.0

package judge_test

import (
	"testing"

	"github.com/RigelBuild/meissa/go/internal/codeshape/judge"
	"github.com/RigelBuild/meissa/go/internal/codeshape/model"
	"github.com/RigelBuild/meissa/go/internal/codeshape/testsupport"
)

func TestJudgeReportsFileAndFunctionFindings(t *testing.T) {
	cfg := testsupport.Config()
	allow := model.Allowlist{Files: map[string]model.Entry{}, Dirs: map[string]model.Entry{}}
	snapshot := model.Snapshot{
		Files: map[string]int{"large.go": 501}, Dirs: map[string]int{},
		Functions: model.FunctionMeasurements{model.MetricGoCognitive: {"large.go": {{ID: "work", Value: 21}}}},
	}
	findings := judge.Judge(cfg, allow, model.Allowlist{}, snapshot)
	if len(findings) != 2 || findings[0].Metric != model.MetricFiles || findings[1].Metric != model.MetricGoCognitive {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestJudgeEnforcesBaseFunctionRatchets(t *testing.T) {
	cfg := testsupport.Config()
	base := model.Allowlist{Present: true, Functions: model.FunctionMeasurements{
		model.MetricGoLines: {"large.go": {{ID: "work", Value: 70}}},
	}}
	head := model.Allowlist{Functions: model.FunctionMeasurements{
		model.MetricGoLines: {"large.go": {{ID: "work", Value: 71}}},
	}}
	snapshot := model.Snapshot{Files: map[string]int{}, Dirs: map[string]int{}, Functions: head.Functions}
	findings := judge.Judge(cfg, head, base, snapshot)
	if len(findings) != 1 || findings[0].Limit != 70 {
		t.Fatalf("findings = %#v", findings)
	}
}
