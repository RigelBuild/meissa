// SPDX-License-Identifier: MIT OR Apache-2.0

package lengthgate

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/commentscan"
	"github.com/RigelBuild/meissa/go/internal/jstext"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/format"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/gitdeps"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/judge"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/model"
)

// GateEnv is the gate input, read from flags and the LENGTH_GATE_* variables.
type GateEnv struct {
	Event       string
	Base        mo.Option[string]
	Fetch       mo.Option[string]
	Head        string
	Merge       bool
	All         bool
	SkipClasses []string
}

// RunOnce judges the change. Notes go to stdout, findings to stderr. It returns the exit code:
// 0 clean or skipped, 1 findings, 2 usage, config or Git failure.
func RunOnce(ctx context.Context, deps gitdeps.Deps, cfg model.Config, env GateEnv, stdout, stderr io.Writer) int {
	selected, err := withSkippedClasses(cfg, env.SkipClasses, env.All)
	if err != nil {
		return failWith(stderr, err)
	}
	code, err := runGate(ctx, deps, selected, env, stdout, stderr)
	if err != nil {
		return failWith(stderr, err)
	}
	return code
}

func runGate(
	ctx context.Context, deps gitdeps.Deps, cfg model.Config, env GateEnv, stdout, stderr io.Writer,
) (int, error) {
	if !env.All && env.Event != "pull_request" {
		return 0, writeLine(stdout, "length-gate: skipping event "+env.Event)
	}
	if env.All {
		return runAll(ctx, deps, cfg, env.Head, stdout, stderr)
	}
	return runRange(ctx, deps, cfg, env, stdout, stderr)
}

// runAll judges every gated file at head as if each line were new.
func runAll(
	ctx context.Context, deps gitdeps.Deps, cfg model.Config, head string, stdout, stderr io.Writer,
) (int, error) {
	headRevision, err := resolveRevision(ctx, deps, head, "head")
	if err != nil {
		return 0, err
	}
	paths, err := deps.ListFiles(ctx, headRevision)
	if err != nil {
		return 0, err
	}
	var findings []model.Finding
	for _, path := range paths {
		fileFindings, err := judgeWhole(ctx, deps, cfg, headRevision, path, stdout)
		if err != nil {
			return 0, err
		}
		findings = append(findings, fileFindings...)
	}
	return finish(findings, cfg, stdout, stderr)
}

// runRange judges the files that changed between the base and the head.
func runRange(
	ctx context.Context, deps gitdeps.Deps, cfg model.Config, env GateEnv, stdout, stderr io.Writer,
) (int, error) {
	base, head, err := pullRange(ctx, deps, env)
	if err != nil {
		return 0, err
	}
	mergeBase, err := deps.MergeBase(ctx, base, head)
	if err != nil {
		return 0, err
	}
	rawDiff, err := deps.Diff(ctx, mergeBase, head)
	if err != nil {
		return 0, err
	}
	if jstext.Trim(rawDiff) == "" {
		return 0, writeLine(stdout, "length-gate: nothing changed")
	}
	findings, err := judgeDiff(ctx, deps, cfg, rawDiff, mergeBase, head, stdout)
	if err != nil {
		return 0, err
	}
	return finish(findings, cfg, stdout, stderr)
}

// withSkippedClasses drops the named document classes. Skipping needs --all, and every name must exist.
func withSkippedClasses(cfg model.Config, names []string, all bool) (model.Config, error) {
	if len(names) == 0 {
		return cfg, nil
	}
	if !all {
		return model.Config{}, model.MessageError("--skip-class is allowed only with --all")
	}
	known := make(map[string]bool, len(cfg.Docs))
	for _, class := range cfg.Docs {
		known[class.Name] = true
	}
	for _, name := range names {
		if !known[name] {
			return model.Config{}, model.Messagef("unknown document class %q", name)
		}
	}
	docs := make([]model.DocClass, 0, len(cfg.Docs))
	for _, class := range cfg.Docs {
		if !slices.Contains(names, class.Name) {
			docs = append(docs, class)
		}
	}
	cfg.Docs = docs
	return cfg, nil
}

// pullRange validates pull-request revisions and returns the resolved base and head commits.
func pullRange(ctx context.Context, deps gitdeps.Deps, env GateEnv) (string, string, error) {
	if !env.Merge {
		return pullRangeFromBase(ctx, deps, env)
	}

	head, err := resolveRevision(ctx, deps, env.Head, "head")
	if err != nil {
		return "", "", err
	}
	_, hasSecondParent, err := deps.Resolve(ctx, head+"^2")
	if err != nil {
		return "", "", err
	}
	if !hasSecondParent {
		return "", "", model.MessageError("merge mode requires a head with at least two parents; no second parent found")
	}
	base, found, err := deps.Resolve(ctx, head+"^1")
	if err != nil {
		return "", "", err
	}
	if !found {
		return "", "", model.MessageError("merge mode head has no first parent")
	}
	return base, head, nil
}

func pullRangeFromBase(ctx context.Context, deps gitdeps.Deps, env GateEnv) (string, string, error) {
	_, hasBase := env.Base.Get()
	_, hasFetch := env.Fetch.Get()
	if !hasBase && !hasFetch {
		return "", "", model.MessageError("pull_request requires LENGTH_GATE_BASE, LENGTH_GATE_FETCH or LENGTH_GATE_MERGE=1")
	}
	var base string
	var err error
	if revision, ok := env.Base.Get(); ok {
		base, err = resolveRevision(ctx, deps, revision, "base")
	} else {
		branch, _ := env.Fetch.Get()
		base, err = deps.FetchBase(ctx, branch)
	}
	if err != nil {
		return "", "", err
	}
	head, err := resolveRevision(ctx, deps, env.Head, "head")
	if err != nil {
		return "", "", err
	}
	return base, head, nil
}

func resolveRevision(ctx context.Context, deps gitdeps.Deps, revision, label string) (string, error) {
	sha, found, err := deps.Resolve(ctx, revision)
	if err != nil {
		return "", err
	}
	if !found {
		return "", model.Messagef("%s revision %q does not resolve", label, revision)
	}
	return sha, nil
}

// judgeWhole judges one file at head as if every line were added.
func judgeWhole(
	ctx context.Context,
	deps gitdeps.Deps,
	cfg model.Config,
	headRevision, path string,
	stdout io.Writer,
) ([]model.Finding, error) {
	if !judge.IsGated(path, cfg) {
		return nil, nil
	}
	text, err := readRequired(ctx, deps, headRevision, path, "head")
	if err != nil {
		return nil, err
	}
	findings, notes := judge.File(cfg, allAddedFile(path, text), "", text)
	return findings, reportNotes(stdout, notes)
}

// judgeDiff judges every changed, gated file in rawDiff against its merge-base and head text.
func judgeDiff(
	ctx context.Context,
	deps gitdeps.Deps,
	cfg model.Config,
	rawDiff, mergeBase, headRevision string,
	stdout io.Writer,
) ([]model.Finding, error) {
	var findings []model.Finding
	for _, file := range commentscan.ParseDiff(rawDiff) {
		fileFindings, err := judgeChanged(ctx, deps, cfg, file, mergeBase, headRevision, stdout)
		if err != nil {
			return nil, err
		}
		findings = append(findings, fileFindings...)
	}
	return findings, nil
}

func judgeChanged(
	ctx context.Context,
	deps gitdeps.Deps,
	cfg model.Config,
	file commentscan.FileDiff,
	mergeBase, headRevision string,
	stdout io.Writer,
) ([]model.Finding, error) {
	if file.Deleted || !judge.IsGated(file.Path, cfg) {
		return nil, nil
	}
	beforePath := file.OldPath
	if beforePath == "" {
		beforePath = file.Path
	}
	afterPath := file.NewPath
	if afterPath == "" {
		afterPath = file.Path
	}
	before := ""
	if !file.Added {
		var err error
		before, err = readRequired(ctx, deps, mergeBase, beforePath, "merge base")
		if err != nil {
			return nil, err
		}
	}
	after, err := readRequired(ctx, deps, headRevision, afterPath, "head")
	if err != nil {
		return nil, err
	}
	findings, notes := judge.File(cfg, file, before, after)
	return findings, reportNotes(stdout, notes)
}

// readRequired reads path at revision and fails when Git has no text for it.
func readRequired(ctx context.Context, deps gitdeps.Deps, revision, path, where string) (string, error) {
	text, found, err := deps.ReadAt(ctx, revision, path)
	if err != nil {
		return "", err
	}
	if !found {
		return "", model.Messagef("cannot read changed file %q at %s", path, where)
	}
	return text, nil
}

// allAddedFile presents a whole file as one added hunk, so every line is judged.
func allAddedFile(path, text string) commentscan.FileDiff {
	normalized := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(normalized, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	file := commentscan.FileDiff{
		Path: path, OldPath: path, NewPath: path,
		Hunks: nil, Added: false, Deleted: false, Binary: false, MetadataOnly: false,
	}
	if len(lines) == 0 {
		return file
	}
	hunk := commentscan.Hunk{Lines: make([]commentscan.DiffLine, 0, len(lines))}
	for index, line := range lines {
		hunk.Lines = append(hunk.Lines, commentscan.DiffLine{
			Kind:  '+',
			Text:  line,
			OldNo: 0,
			NewNo: index + 1,
		})
	}
	file.Hunks = []commentscan.Hunk{hunk}
	return file
}

// finish reports the findings and returns 1, or logs "clean" and returns 0.
func finish(findings []model.Finding, cfg model.Config, stdout, stderr io.Writer) (int, error) {
	if len(findings) == 0 {
		return 0, writeLine(stdout, "length-gate: clean")
	}
	for _, finding := range findings {
		for _, line := range format.Finding(finding, cfg) {
			err := writeLine(stderr, line)
			if err != nil {
				return 0, err
			}
		}
	}
	return 1, nil
}

func reportNotes(stdout io.Writer, notes []model.Note) error {
	for _, note := range notes {
		err := writeLine(stdout, fmt.Sprintf("%s:%d: unneeded LENGTH marker", note.Path, note.Line))
		if err != nil {
			return err
		}
	}
	return nil
}

func writeLine(w io.Writer, line string) error {
	_, err := io.WriteString(w, line+"\n")
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}
