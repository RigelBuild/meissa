// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:exhaustruct_v5 // Keep TS-derived cases direct and readable.
package claim

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
	"github.com/RigelBuild/meissa/go/ledger"
)

// fakeClaimDeps answers the claim leg's seams in memory.
type fakeClaimDeps struct {
	mergeBase []string
	base      []string
	verdicts  map[string]Verdict
	fetchErr  error
	lanes     []string
	fetched   [][]string
}

func (d *fakeClaimDeps) ReadMergeBaseDecisionPaths(context.Context, string) ([]string, error) {
	return d.mergeBase, nil
}

func (d *fakeClaimDeps) ReadBaseDecisionPaths(context.Context, string) ([]string, error) {
	return d.base, nil
}

func (d *fakeClaimDeps) FetchClaims(_ context.Context, lane string, ids []string) (map[string]Verdict, error) {
	d.lanes = append(d.lanes, lane)
	d.fetched = append(d.fetched, ids)
	if d.fetchErr != nil {
		return nil, d.fetchErr
	}
	return d.verdicts, nil
}

func TestClaimLegSkippedWithoutPRBody(t *testing.T) {
	checked, violations, err := RunLeg(context.Background(), &fakeClaimDeps{}, claimLegConfig(), "main",
		core.Changed{HeadBranch: "feature/x"}, mo.None[core.PullRequestDetails](), nil)
	if err != nil || checked != 0 || len(violations) != 0 {
		t.Fatalf("RunLeg = %d, %v, %v; want 0, no violations, nil", checked, violations, err)
	}
}
func TestClaimLegNeedsHeadBranchWhenBodyPresent(t *testing.T) {
	_, _, err := RunLeg(context.Background(), &fakeClaimDeps{}, claimLegConfig(), "main",
		core.Changed{Body: mo.Some("body")}, mo.None[core.PullRequestDetails](), nil)
	if err == nil || !strings.Contains(err.Error(), "needs the PR head branch") {
		t.Fatalf("err = %v, want head-branch error", err)
	}
}

// claimLegConfig builds a minimal config with claim checks enabled.
func claimLegConfig() core.Config {
	return core.Config{
		DesignsRoot: "docs/designs", SurfaceDepth: 0, GovernedRoots: []string{"ui"},
		Counter: core.Counter{URL: "https://example.invalid/counter", Partition: "test"},
		Legs:    core.Legs{ClaimCheck: true},
	}
}

func TestClaimLegReportsNotLaneViolations(t *testing.T) {
	row := claimDecision("DL-004")
	deps := &fakeClaimDeps{verdicts: map[string]Verdict{"DL-004": VerdictNotLane}}
	checked, violations, err := RunLeg(context.Background(), deps, claimLegConfig(), "main",
		core.Changed{Body: mo.Some("body"), HeadBranch: "feature/x", Files: []string{row.Path}},
		mo.None[core.PullRequestDetails](), []ledger.DecisionRow{row})
	if err != nil || checked != 1 {
		t.Fatalf("RunLeg = %d, %v; want 1, nil", checked, err)
	}
	if len(violations) != 1 {
		t.Fatalf("violations = %d, want 1", len(violations))
	}
	if deps.lanes[0] != "feature/x" {
		t.Fatalf("lane = %q, want feature/x", deps.lanes[0])
	}
}

func TestClaimLegPropagatesServiceError(t *testing.T) {
	row := claimDecision("DL-004")
	serviceErr := rateLimitedError()
	deps := &fakeClaimDeps{fetchErr: serviceErr}
	_, _, err := RunLeg(context.Background(), deps, claimLegConfig(), "main",
		core.Changed{Body: mo.Some("body"), HeadBranch: "feature/x", Files: []string{row.Path}},
		mo.None[core.PullRequestDetails](), []ledger.DecisionRow{row})
	if err == nil || err.Error() != serviceErr.Error() {
		t.Fatalf("err = %v, want the original claim service error %v", err, serviceErr)
	}
}

func TestClaimLegWrapsCancellationAsUnreachableService(t *testing.T) {
	row := claimDecision("DL-004")
	deps := &fakeClaimDeps{fetchErr: context.Canceled}
	_, _, err := RunLeg(context.Background(), deps, claimLegConfig(), "main",
		core.Changed{Body: mo.Some("body"), HeadBranch: "feature/x", Files: []string{row.Path}},
		mo.None[core.PullRequestDetails](), []ledger.DecisionRow{row})
	var serviceErr *ServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Kind != "unreachable" ||
		!strings.Contains(err.Error(), "design-ledger-gate: cannot reach the DL claim service") {
		t.Fatalf("error = %v, want prefixed unreachable-service error", err)
	}
}

func TestLaneUsesOwnerForCrossRepositoryPRs(t *testing.T) {
	changed := core.Changed{HeadBranch: "feature/x"}
	if got := Lane(changed, mo.None[core.PullRequestDetails]()); got != "feature/x" {
		t.Fatalf("same-repo lane = %q, want feature/x", got)
	}
	cross := mo.Some(core.PullRequestDetails{
		IsCrossRepository:   mo.Some(true),
		HeadRepositoryOwner: mo.Some("fork-owner"),
	})
	if got := Lane(changed, cross); got != "fork-owner:feature/x" {
		t.Fatalf("cross-repo lane = %q, want fork-owner:feature/x", got)
	}
}
