// SPDX-License-Identifier: MIT OR Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/internal/jstext"
	"github.com/RigelBuild/meissa/go/internal/ledgergate/claim"
	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
	"github.com/RigelBuild/meissa/go/internal/ledgergate/run"
)

// usage is the TypeScript usage line, printed for bad arguments.
const usage = "Usage: design-ledger-gate --config <path>\n"

// Main runs the design-ledger gate CLI and returns its exit code. Diagnostic
// writes are best-effort: a failed write never changes the exit code.
func Main(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "--help" {
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	}
	if len(args) != 2 || args[0] != "--config" || args[1] == "" {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	cfg, err := core.LoadConfig(args[1])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "design-ledger-gate: %s\n", err)
		return 2
	}
	root, err := scanRoot(ctx, env("GATE_ROOT"))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "design-ledger-gate: %s\n", err)
		return 2
	}
	deps := &gitDeps{
		root: root, cfg: cfg, env: env,
		client:      &http.Client{Transport: nil, CheckRedirect: nil, Jar: nil, Timeout: 0},
		baseRef:     baseRefFrom(env),
		metadata:    nil,
		metadataErr: nil,
	}
	return run.Once(ctx, deps, cfg, stdout, stderr)
}

// baseRefFrom returns BASE_REF, defaulting to "main".
func baseRefFrom(env func(string) string) string {
	if ref := env("BASE_REF"); ref != "" {
		return ref
	}
	return "main"
}

// scanRoot returns GATE_ROOT when set, else the git top-level directory.
func scanRoot(ctx context.Context, configured string) (string, error) {
	if configured != "" {
		absolute, err := filepath.Abs(configured)
		if err != nil {
			return "", fmt.Errorf("cannot find git root: %w", err)
		}
		return absolute, nil
	}
	output, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		//nolint:err113 // Preserve the TypeScript diagnostic.
		return "", fmt.Errorf("cannot find git root: %s", commandStderr(err))
	}
	return jstext.Trim(string(output)), nil
}

// commandStderr returns the stderr of a failed command, or its error text.
func commandStderr(err error) string {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return jstext.Trim(string(exitErr.Stderr))
	}
	return err.Error()
}

// gitDeps is the git and filesystem implementation of run.Deps.
type gitDeps struct {
	root        string
	cfg         core.Config
	env         func(string) string
	client      *http.Client
	baseRef     string
	metadata    *run.PRMetadata
	metadataErr error
}

// Root returns the repository root.
func (d *gitDeps) Root() string { return d.root }

// BaseRef returns the configured base ref.
func (d *gitDeps) BaseRef() string { return d.baseRef }

func (d *gitDeps) ReadText(file string) (string, bool, error) {
	path := filepath.Join(d.root, filepath.FromSlash(file))
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", file, err)
	}
	if !info.Mode().IsRegular() {
		return "", false, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // Read repository files selected by the gate.
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", file, err)
	}
	return string(data), true, nil
}

// ListDesignFiles lists the files under the designs root, sorted.
func (d *gitDeps) ListDesignFiles(_ context.Context) ([]string, error) {
	var files []string
	root := filepath.Join(d.root, filepath.FromSlash(d.cfg.DesignsRoot))
	_, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list design files under %s: %w", d.cfg.DesignsRoot, err)
	}
	err = filepath.WalkDir(root, func(file string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() && entry.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() {
			relative, err := filepath.Rel(d.root, file)
			if err != nil {
				return fmt.Errorf("relative path for %s: %w", file, err)
			}
			files = append(files, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list design files under %s: %w", d.cfg.DesignsRoot, err)
	}
	sort.Strings(files)
	return files, nil
}

// ReadMergeBaseDecisionPaths lists the decision files at the merge base of HEAD and origin/<baseRef>.
func (d *gitDeps) ReadMergeBaseDecisionPaths(ctx context.Context, baseRef string) ([]string, error) {
	mergeBase, err := gitOutput(ctx, d.root, "merge-base", "HEAD", "origin/"+baseRef)
	if err != nil {
		return nil, err
	}
	return d.decisionPathsAt(ctx, jstext.Trim(mergeBase))
}

// ReadBaseDecisionPaths lists the decision files at origin/<baseRef>.
func (d *gitDeps) ReadBaseDecisionPaths(ctx context.Context, baseRef string) ([]string, error) {
	return d.decisionPathsAt(ctx, "origin/"+baseRef)
}

func (d *gitDeps) FetchClaims(ctx context.Context, lane string, ids []string) (map[string]claim.Verdict, error) {
	return claim.NewClaimClient(d.cfg, d.client, clockReal{})(ctx, lane, ids)
}

// clockReal is the production Clock for the claim client.
type clockReal struct{}

func (clockReal) After(d time.Duration) <-chan time.Time { return time.After(d) }

// gitOutput runs git in dir and returns its stdout.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	//nolint:gosec // Run fixed git operations.
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	output, err := command.Output()
	if err != nil {
		//nolint:err113 // Preserve the TypeScript diagnostic.
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), commandStderr(err))
	}
	return string(output), nil
}

// splitLines splits command output on newlines and drops empty lines.
func splitLines(output string) []string {
	var lines []string
	for line := range strings.SplitSeq(output, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// Changed returns the PR's changed files, body and head branch. Outside a PR it
// returns no body, so the PR checks are skipped.
func (d *gitDeps) Changed(ctx context.Context) (core.Changed, error) {
	metadata, err := d.prMetadata(ctx)
	if err != nil {
		return core.Changed{}, err
	}
	return metadata.Changed, nil
}

// PullRequestDetails returns the PR metadata used for branch exemptions, or
// none outside a PR.
func (d *gitDeps) PullRequestDetails(ctx context.Context) (mo.Option[core.PullRequestDetails], error) {
	metadata, err := d.prMetadata(ctx)
	if err != nil {
		return mo.None[core.PullRequestDetails](), err
	}
	if metadata.Changed.Body.IsAbsent() {
		return mo.None[core.PullRequestDetails](), nil
	}
	return mo.Some(metadata.PullRequestDetails), nil
}

// decisionPathsAt lists the decision paths in one revision.
func (d *gitDeps) decisionPathsAt(ctx context.Context, revision string) ([]string, error) {
	output, err := gitOutput(ctx, d.root, "ls-tree", "-r", "--name-only", revision)
	if err != nil {
		return nil, err
	}
	var decisions []string
	for _, file := range splitLines(output) {
		if core.ClassifyDesignPath(file, d.cfg) == core.PathDecision {
			decisions = append(decisions, file)
		}
	}
	return decisions, nil
}

// prMetadata reads PR metadata once per gate run.
func (d *gitDeps) prMetadata(ctx context.Context) (run.PRMetadata, error) {
	if d.metadata != nil || d.metadataErr != nil {
		if d.metadata == nil {
			return run.PRMetadata{}, d.metadataErr
		}
		return *d.metadata, nil
	}
	metadata, err := d.readPRMetadata(ctx)
	if err != nil {
		d.metadataErr = err
		return run.PRMetadata{}, err
	}
	d.metadata = &metadata
	return metadata, nil
}

func (d *gitDeps) readPRMetadata(ctx context.Context) (run.PRMetadata, error) {
	switch prCtx := run.PRContextFrom(d.env).(type) {
	case run.PRContextPR:
		metadata, err := run.ReadPullRequest(ctx, prCtx.Repo, prCtx.PRNumber)
		if err != nil {
			return run.PRMetadata{}, err //nolint:wrapcheck // Preserve the TypeScript diagnostic.
		}
		return metadata, nil
	case run.PRContextWoodpecker:
		return run.PRMetadata{Changed: prCtx.Changed, PullRequestDetails: noPullRequestDetails()}, nil
	case run.PRContextError:
		return run.PRMetadata{}, prCtx
	default:
		return run.PRMetadata{
			Changed:            core.Changed{Files: nil, Body: mo.None[string](), HeadBranch: ""},
			PullRequestDetails: noPullRequestDetails(),
		}, nil
	}
}

// noPullRequestDetails is the empty details value for runs without a PR.
func noPullRequestDetails() core.PullRequestDetails {
	return core.PullRequestDetails{
		IsCrossRepository:   mo.None[bool](),
		HeadRepositoryOwner: mo.None[string](),
		Author:              mo.None[string](),
	}
}
