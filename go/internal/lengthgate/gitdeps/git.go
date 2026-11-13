// SPDX-License-Identifier: MIT OR Apache-2.0

// Package gitdeps reads revisions, files, and diffs from a Git repository.
package gitdeps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/jstext"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/errs"
	"github.com/samber/mo"
)

// Deps reads the Git state one gate run needs. Each method mirrors one field of git.ts Deps, except that
// FetchBase and MergeBase use the base and fetch branch bound by NewGitDeps.
type Deps interface {
	Root() string
	Resolve(ctx context.Context, revision string) (string, bool, error)
	FetchBase(ctx context.Context) error
	MergeBase(ctx context.Context) (string, error)
	Diff(ctx context.Context, base, head string) (string, error)
	ReadAt(ctx context.Context, revision, path string) (string, bool, error)
	ListFiles(ctx context.Context, revision string) ([]string, error)
}

var (
	errNoCompleteMergeBase = errors.New("no merge base after fetching complete repository history")
	errNoDeepenedMergeBase = errors.New("no merge base after deepening shallow history")
	errShallowUnknown      = errors.New("cannot determine whether repository is shallow")
	errNoMergeBase         = errors.New("no merge base")
	errNoBaseRevision      = errors.New("no base revision bound; " +
		"set LENGTH_GATE_BASE, LENGTH_GATE_FETCH or use merge mode")
	errNoFetchBranch = errors.New("no fetch branch bound for origin")
)

// gitOutput is the result of one git child process that ran to completion.
type gitOutput struct {
	stdout   []byte
	stderr   []byte
	exitCode int
}

// runGit runs git in root. Only a failure to start git is an error; a nonzero exit is data.
func runGit(ctx context.Context, root string, args ...string) (gitOutput, error) {
	argv := append([]string{"-C", root}, args...)
	// G204: fixed executable; arguments are argv, not a shell string.
	command := exec.CommandContext(ctx, "git", argv...) //nolint:gosec
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return gitOutput{stdout: stdout.Bytes(), stderr: stderr.Bytes(), exitCode: 0}, nil
	}
	exitError, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		return gitOutput{}, fmt.Errorf("start git: %w", err)
	}
	return gitOutput{stdout: stdout.Bytes(), stderr: stderr.Bytes(), exitCode: exitError.ExitCode()}, nil
}

// decodeText matches TextDecoder: a leading BOM is dropped and invalid UTF-8 becomes U+FFFD.
func decodeText(raw []byte) string {
	return strings.TrimPrefix(strings.ToValidUTF8(string(raw), "\uFFFD"), "\uFEFF")
}

// commandFailure is the error for a git child that exited nonzero.
func commandFailure(out gitOutput, command string) error {
	if detail := jstext.Trim(decodeText(out.stderr)); detail != "" {
		return errs.MessageError(detail)
	}
	return errs.Messagef("git %s exited %d", command, out.exitCode)
}

// GitTopLevel returns the top-level directory of the repository that contains root.
func GitTopLevel(ctx context.Context, root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", root, err)
	}
	out, err := runGit(ctx, absolute, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if out.exitCode != 0 {
		return "", commandFailure(out, "rev-parse")
	}
	return jstext.TrimRight(decodeText(out.stdout)), nil
}

type gitDeps struct {
	root         string
	headRevision string
	baseSpec     mo.Option[string]
	fetchBranch  mo.Option[string]
}

// NewGitDeps returns Git-backed Deps for root, which must be absolute (GitTopLevel returns one).
// head is the commit under test. base is the revision MergeBase compares with head; merge mode passes
// head^1. fetch is the origin branch FetchBase fetches. Shallow deepening stops once head shares history
// with the base. The two writers are unused: git reads never print, and RunOnce owns all output.
func NewGitDeps(root string, _, _ io.Writer, head string, base, fetch mo.Option[string]) Deps {
	return gitDeps{root: root, headRevision: head, baseSpec: base, fetchBranch: fetch}
}

func (d gitDeps) Root() string { return d.root }

func (d gitDeps) Resolve(ctx context.Context, revision string) (string, bool, error) {
	return d.nullable(ctx, "rev-parse", "--verify", "--quiet", revision+"^{commit}")
}

// MergeBase returns the merge base of the bound base revision and the head.
func (d gitDeps) MergeBase(ctx context.Context) (string, error) {
	base, ok := d.baseRevision()
	if !ok {
		return "", errNoBaseRevision
	}
	sha, found, err := d.nullable(ctx, "merge-base", base, d.headRevision)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%w for %s and %s", errNoMergeBase, base, d.headRevision)
	}
	return sha, nil
}

func (d gitDeps) Diff(ctx context.Context, base, head string) (string, error) {
	// Pin output-shaping config so repository settings cannot empty or reshape the diff.
	out, err := runGit(ctx, d.root,
		"-c", "core.quotePath=false",
		"-c", "diff.suppressBlankEmpty=false",
		"diff", "--no-color", "--no-ext-diff", "--no-textconv",
		"--submodule=short",
		// A gitlink has no text to judge.
		"--ignore-submodules=all",
		"--src-prefix=a/", "--dst-prefix=b/",
		base+"..."+head,
	)
	if err != nil {
		return "", err
	}
	if out.exitCode != 0 {
		return "", commandFailure(out, "diff")
	}
	return decodeText(out.stdout), nil
}

func (d gitDeps) ReadAt(ctx context.Context, revision, path string) (string, bool, error) {
	blob, err := runGit(ctx, d.root, "cat-file", "blob", revision+":"+path)
	if err != nil {
		return "", false, err
	}
	if blob.exitCode == 0 {
		return decodeText(blob.stdout), true, nil
	}
	// A gitlink (for example a file replaced by a submodule) has no text to judge.
	entry, err := runGit(ctx, d.root, "--literal-pathspecs", "ls-tree", revision, "--", path)
	if err != nil {
		return "", false, err
	}
	mode, _, _ := strings.Cut(decodeText(entry.stdout), " ")
	if entry.exitCode == 0 && mode == "160000" {
		return "", true, nil
	}
	return "", false, nil
}

func (d gitDeps) ListFiles(ctx context.Context, revision string) ([]string, error) {
	out, err := runGit(ctx, d.root, "ls-tree", "-r", "-z", revision)
	if err != nil {
		return nil, err
	}
	if out.exitCode != 0 {
		return nil, commandFailure(out, "ls-tree")
	}
	// Keep blobs only; a submodule gitlink has no readable content.
	paths := []string{}
	for entry := range strings.SplitSeq(decodeText(out.stdout), "\x00") {
		tab := strings.IndexByte(entry, '\t')
		fields := strings.Split(entry, " ")
		if tab == -1 || len(fields) < 2 || fields[1] != "blob" {
			continue
		}
		paths = append(paths, entry[tab+1:])
	}
	return paths, nil
}

// FetchBase fetches the bound fetch branch from origin and stops once it shares history with the head.
func (d gitDeps) FetchBase(ctx context.Context) error {
	branch, ok := d.fetchBranch.Get()
	if !ok {
		return errNoFetchBranch
	}
	shallow, found, err := d.nullable(ctx, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return err
	}
	if !found {
		return errShallowUnknown
	}
	refspec := "+refs/heads/" + branch + ":refs/remotes/origin/" + branch
	// Deepen in steps first: a PR's merge base is usually near, and full history can be hundreds of MB.
	for _, step := range []string{"--deepen=200", "--deepen=1000", "--unshallow"} {
		var depth []string
		if shallow == "true" {
			depth = []string{step}
		}
		err := d.fetch(ctx, refspec, depth...)
		if err != nil {
			return err
		}
		base, found, err := d.nullable(ctx, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch+"^{commit}")
		if err != nil {
			return err
		}
		if !found {
			return errs.Messagef("fetched branch origin/%s did not resolve", branch)
		}
		merged, err := d.reachesBase(ctx, base)
		if err != nil {
			return err
		}
		if merged {
			return nil
		}
		stillShallow, _, err := d.nullable(ctx, "rev-parse", "--is-shallow-repository")
		if err != nil {
			return err
		}
		if stillShallow != "true" {
			return errNoCompleteMergeBase
		}
	}
	return errNoDeepenedMergeBase
}

// fetch runs one fetch. A deepen also fetches the head, because a deepen moves only the fetched tips' shallow boundary.
func (d gitDeps) fetch(ctx context.Context, refspec string, depth ...string) error {
	head, found, err := d.Resolve(ctx, d.headRevision)
	if err != nil {
		return err
	}
	argv := append([]string{"fetch", "--no-tags"}, depth...)
	argv = append(argv, "origin", refspec)
	if len(depth) > 0 && found {
		argv = append(argv, head)
	}
	out, err := runGit(ctx, d.root, argv...)
	if err != nil {
		return err
	}
	if out.exitCode != 0 {
		return commandFailure(out, "fetch")
	}
	return nil
}

// reachesBase reports whether the head resolves and shares history with base.
func (d gitDeps) reachesBase(ctx context.Context, base string) (bool, error) {
	head, found, err := d.Resolve(ctx, d.headRevision)
	if err != nil || !found {
		return false, err
	}
	_, merged, err := d.nullable(ctx, "merge-base", base, head)
	return merged, err
}

// nullable returns trimmed stdout when git exits 0. A nonzero exit means absent, not an error.
func (d gitDeps) nullable(ctx context.Context, args ...string) (string, bool, error) {
	out, err := runGit(ctx, d.root, args...)
	if err != nil {
		return "", false, err
	}
	if out.exitCode != 0 {
		return "", false, nil
	}
	return jstext.Trim(decodeText(out.stdout)), true, nil
}

// baseRevision is the revision MergeBase compares with head: the bound base, else the fetched branch.
func (d gitDeps) baseRevision() (string, bool) {
	if base, ok := d.baseSpec.Get(); ok {
		return base, true
	}
	if branch, ok := d.fetchBranch.Get(); ok {
		return "refs/remotes/origin/" + branch, true
	}
	return "", false
}
