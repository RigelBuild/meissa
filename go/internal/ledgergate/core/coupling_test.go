// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"testing"

	"github.com/samber/mo"
)

func TestExemptBranchRequiresSameRepoBotAndConfiguredRule(t *testing.T) {
	rules := []ExemptBranchRule{{Prefix: "renovate/", Author: "renovate[bot]"}}
	cases := []struct {
		name   string
		author mo.Option[string]
		cross  mo.Option[bool]
		want   bool
	}{
		{"same repo matching bot", mo.Some("renovate[bot]"), mo.Some(false), true},
		{"app author alias", mo.Some("app/renovate"), mo.Some(false), true},
		{"cross repo matching bot", mo.Some("renovate[bot]"), mo.Some(true), false},
		{"unknown repo", mo.Some("renovate[bot]"), mo.None[bool](), false},
		{"unknown author", mo.None[string](), mo.Some(false), false},
		{"different bot", mo.Some("other[bot]"), mo.Some(false), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsExemptBranch("renovate/update", tc.author, tc.cross, rules); got != tc.want {
				t.Errorf("IsExemptBranch() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestTouchCouplingRequiresDecisionOrLedgerImpact(t *testing.T) {
	const changedRecord = "docs/designs/ui/record/design.md"
	missing := evaluateCorpus(corpusOf(), nil,
		Changed{Files: []string{changedRecord}, Body: mo.Some("unrelated text"), HeadBranch: "feature/change"},
		nil, testConfig, noPRDetails())
	if !hasMessage(missing, "changed decision file or Ledger-impact") {
		t.Fatalf("violations = %+v, want coupling message", missing)
	}
}

func TestNestedSupportingAndGammaRecordsCouple(t *testing.T) {
	for _, file := range []string{"docs/designs/infra/runtime/sample/microvm-v3.md", "docs/designs/gamma/x/design.md"} {
		got := evaluateCorpus(corpusOf(), nil,
			Changed{Files: []string{file}, Body: mo.Some("no declaration"), HeadBranch: "feature/change"},
			nil, testConfig, noPRDetails())
		if !hasMessage(got, "changed decision file or Ledger-impact") {
			t.Errorf("%s: violations = %+v, want coupling message", file, got)
		}
	}
}

func TestAnyChangedDecisionOrLedgerImpactSatisfiesCoupling(t *testing.T) {
	const changedRecord = "docs/designs/ui/record/design.md"
	if got := evaluateCorpus(corpusOf(), nil,
		Changed{
			Files: []string{changedRecord, decisionDir + "/server/DL-002.md"},
			Body:  mo.None[string](), HeadBranch: "feature/change",
		},
		nil, testConfig, noPRDetails()); len(got) != 0 {
		t.Fatalf("decision change: violations = %+v, want none", got)
	}
	if got := evaluateCorpus(corpusOf(), nil,
		Changed{Files: []string{changedRecord}, Body: mo.Some("Ledger-impact: none"), HeadBranch: "feature/change"},
		nil, testConfig, noPRDetails()); len(got) != 0 {
		t.Fatalf("ledger-impact: violations = %+v, want none", got)
	}
}

func TestLedgerImpactDeclarationVariants(t *testing.T) {
	const changedRecord = "docs/designs/ui/record/design.md"
	for _, body := range []string{"> Ledger-impact: none", "LEDGER-IMPACT: x"} {
		t.Run(body, func(t *testing.T) {
			got := evaluateCorpus(corpusOf(), nil,
				Changed{Files: []string{changedRecord}, Body: mo.Some(body), HeadBranch: "feature/change"},
				nil, testConfig, noPRDetails())
			if len(got) != 0 {
				t.Fatalf("violations = %+v, want none", got)
			}
		})
	}
}

func TestMiddleOfNameExemptPrefixDoesNotExempt(t *testing.T) {
	got := evaluateCorpus(corpusOf(), nil,
		Changed{
			Files: []string{"docs/designs/ui/record/design.md"},
			Body:  mo.None[string](), HeadBranch: "feature/renovate/x",
		},
		nil, testConfig, noPRDetails())
	if !hasMessage(got, "changed decision file") {
		t.Fatalf("violations = %+v, want coupling message", got)
	}
}

func TestEmptyChangedSetPasses(t *testing.T) {
	if got := evaluateCorpus(corpusOf(), nil, noChange, nil, testConfig, noPRDetails()); len(got) != 0 {
		t.Fatalf("violations = %+v, want none", got)
	}
}

func TestBotBranchWithoutPRMetadataDoesNotSkipCoupling(t *testing.T) {
	for _, branch := range []string{"renovate/update", "trunk-merge/pr-1/test"} {
		t.Run(branch, func(t *testing.T) {
			got := evaluateCorpus(corpusOf(), nil,
				Changed{Files: []string{"docs/designs/ui/record/design.md"}, Body: mo.Some(""), HeadBranch: branch},
				nil, testConfig, noPRDetails())
			if !hasMessage(got, "changed decision file") {
				t.Fatalf("violations = %+v, want coupling message", got)
			}
		})
	}
}

func TestSameRepoAuthorBoundExemptionPasses(t *testing.T) {
	cases := []struct{ branch, author string }{
		{"renovate/update", "rigelbuild-renovate[bot]"},
		{"trunk-merge/pr-1/test", "trunk-io[bot]"},
	}
	for _, tc := range cases {
		t.Run(tc.branch, func(t *testing.T) {
			got := evaluateCorpus(corpusOf(), nil,
				Changed{Files: []string{"docs/designs/ui/record/design.md"}, Body: mo.Some(""), HeadBranch: tc.branch},
				nil, testConfig,
				mo.Some(PullRequestDetails{
					IsCrossRepository:   mo.Some(false),
					HeadRepositoryOwner: mo.None[string](),
					Author:              mo.Some(tc.author),
				}),
			)
			if len(got) != 0 {
				t.Fatalf("violations = %+v, want none", got)
			}
		})
	}
}

func TestBotBranchWithUnknownOrWrongAuthorStillEnforcesCoupling(t *testing.T) {
	cases := []struct {
		name    string
		details PullRequestDetails
	}{
		{"wrong author", PullRequestDetails{
			IsCrossRepository:   mo.Some(false),
			HeadRepositoryOwner: mo.None[string](),
			Author:              mo.Some("person"),
		}},
		{"cross-repository PR", PullRequestDetails{
			IsCrossRepository:   mo.Some(true),
			HeadRepositoryOwner: mo.None[string](),
			Author:              mo.Some("rigelbuild-renovate[bot]"),
		}},
		{"unknown repository status", PullRequestDetails{
			IsCrossRepository:   mo.None[bool](),
			HeadRepositoryOwner: mo.None[string](),
			Author:              mo.Some("rigelbuild-renovate[bot]"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateCorpus(corpusOf(), nil,
				Changed{Files: []string{"docs/designs/ui/record/design.md"}, Body: mo.Some(""), HeadBranch: "renovate/x"},
				nil, testConfig, mo.Some(tc.details))
			if !hasMessage(got, "changed decision file") {
				t.Fatalf("violations = %+v, want coupling message", got)
			}
		})
	}
}

func TestUnknownCrossRepositoryStatusDoesNotExemptWithoutDetails(t *testing.T) {
	got := evaluateCorpus(corpusOf(), nil,
		Changed{Files: []string{"docs/designs/ui/record/design.md"}, Body: mo.Some(""), HeadBranch: "renovate/x"},
		nil, testConfig, noPRDetails())
	if !hasMessage(got, "changed decision file") {
		t.Fatalf("violations = %+v, want coupling message", got)
	}
}

func TestBotAuthorFormsMatch(t *testing.T) {
	cases := []struct{ author, configured string }{
		{"app/rigelbuild-renovate", "rigelbuild-renovate[bot]"},
		{"rigelbuild-renovate[bot]", "app/rigelbuild-renovate"},
	}
	for _, tc := range cases {
		t.Run(tc.author+"/"+tc.configured, func(t *testing.T) {
			cfg := testConfig
			cfg.ExemptBranches = []ExemptBranchRule{{Prefix: "renovate/", Author: tc.configured}}
			got := evaluateCorpus(corpusOf(), nil,
				Changed{Files: []string{"docs/designs/ui/record/design.md"}, Body: mo.Some(""), HeadBranch: "renovate/x"},
				nil, cfg, mo.Some(PullRequestDetails{
					IsCrossRepository:   mo.Some(false),
					HeadRepositoryOwner: mo.None[string](),
					Author:              mo.Some(tc.author),
				}))
			if len(got) != 0 {
				t.Fatalf("violations = %+v, want none", got)
			}
		})
	}
}
