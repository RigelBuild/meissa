// SPDX-License-Identifier: MIT OR Apache-2.0

package claim

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
	"github.com/RigelBuild/meissa/go/ledger"
)

// Lane names the lane a PR's claims are checked against. Cross-repository PRs
// use "<owner>:<branch>".
func Lane(changed core.Changed, details mo.Option[core.PullRequestDetails]) string {
	lane := changed.HeadBranch
	if pr, ok := details.Get(); ok {
		cross, hasCross := pr.IsCrossRepository.Get()
		owner, hasOwner := pr.HeadRepositoryOwner.Get()
		if hasCross && cross && hasOwner && owner != "" {
			lane = owner + ":" + changed.HeadBranch
		}
	}
	return lane
}

// LegDeps provides the repository and claim-service reads for one claim leg.
type LegDeps interface {
	ReadMergeBaseDecisionPaths(ctx context.Context, baseRef string) ([]string, error)
	ReadBaseDecisionPaths(ctx context.Context, baseRef string) ([]string, error)
	FetchClaims(ctx context.Context, lane string, ids []string) (map[string]Verdict, error)
}

// RunLeg checks each new decision id against the claim service. It returns the
// number checked and the findings. A service error is returned to the caller.
func RunLeg(
	ctx context.Context,
	deps LegDeps,
	cfg core.Config,
	baseRef string,
	changed core.Changed,
	details mo.Option[core.PullRequestDetails],
	rows []ledger.DecisionRow,
) (int, []core.Violation, error) {
	if !cfg.Legs.ClaimCheck {
		return 0, nil, nil
	}
	if _, hasBody := changed.Body.Get(); !hasBody {
		return 0, nil, nil
	}
	if core.IsExempt(changed, details, cfg) {
		return 0, nil, nil
	}
	if changed.HeadBranch == "" {
		return 0, nil, errClaimNeedsHeadBranch
	}
	mergeBase, err := deps.ReadMergeBaseDecisionPaths(ctx, baseRef)
	if err != nil {
		return 0, nil, fmt.Errorf("design-ledger-gate: cannot read base decision listing: %w", err)
	}
	base, err := deps.ReadBaseDecisionPaths(ctx, baseRef)
	if err != nil {
		return 0, nil, fmt.Errorf("design-ledger-gate: cannot read base decision listing: %w", err)
	}
	newRows := CheckIDs(rows, changed.Files, idSet(mergeBase), idSet(base))
	if len(newRows) == 0 {
		return 0, nil, nil
	}
	ids := make([]string, len(newRows))
	for i, row := range newRows {
		ids[i] = row.ID
	}
	lane := Lane(changed, details)
	verdicts, err := deps.FetchClaims(ctx, lane, ids)
	if err != nil {
		if _, ok := errors.AsType[*ServiceError](err); ok {
			return 0, nil, err //nolint:wrapcheck // ServiceError text is the TypeScript diagnostic.
		}
		baseURL := strings.TrimRight(cfg.Counter.URL, "/")
		return 0, nil, unreachableError(baseURL, err.Error())
	}
	return len(newRows), CheckClaims(newRows, lane, verdicts), nil
}

func idSet(paths []string) map[string]struct{} {
	ids := make(map[string]struct{}, len(paths))
	for _, file := range paths {
		ids[strings.TrimSuffix(path.Base(file), ".md")] = struct{}{}
	}
	return ids
}
