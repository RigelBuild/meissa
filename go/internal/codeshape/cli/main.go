// SPDX-License-Identifier: MIT OR Apache-2.0

// Package cli exposes the code-shape gate command orchestration.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"cuelang.org/go/cue/cuecontext"

	"github.com/RigelBuild/meissa/go/internal/codeshape/allowlist"
	"github.com/RigelBuild/meissa/go/internal/codeshape/golang"
	"github.com/RigelBuild/meissa/go/internal/codeshape/inventory"
	"github.com/RigelBuild/meissa/go/internal/codeshape/judge"
	"github.com/RigelBuild/meissa/go/internal/codeshape/model"
	"github.com/RigelBuild/meissa/go/internal/codeshape/typescript"
)

var errCannotRaiseAllowlist = errors.New("--write cannot add or raise allowlist entries")

// Main runs the code-shape gate command without exiting the process.
func Main(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int {
	if helpRequested(args) {
		return printHelp(stdout)
	}
	writeMode, seed, root, ok := parseArgs(args)
	if !ok {
		writeOutput(stderr, "usage: code-shape-gate [--write] [--seed] <root>\n")
		return 2
	}
	root, err := filepath.Abs(root)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	config, allow, err := allowlist.Load(root)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	base, err := loadRequestedBase(ctx, root, env)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	paths, err := inventory.TrackedPaths(ctx, root)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	repository, err := os.OpenRoot(root)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: open repository: %v\n", err)
		return 2
	}
	defer repository.Close()
	snapshot, err := inventory.Inventory(paths, config, repository)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	functions, skips, err := collectFunctionMeasurements(ctx, root, paths, config, repository)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	for _, skip := range skips {
		if !writeOutput(stderr, "code-shape-gate: skip %s: %s\n", skip.Path, skip.Reason) {
			return 2
		}
	}
	snapshot.Functions = functions
	findings, err := collectPolicyFindings(paths, config, repository)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	if writeMode {
		return writeUpdated(root, config, allow, base, snapshot, seed, findings, stdout, stderr)
	}
	findings = append(judge.Judge(config, allow, base, snapshot), findings...)
	sortFindings(findings)
	return printFindings(stdout, stderr, findings)
}

func collectFunctionMeasurements(ctx context.Context, root string, paths []string, cfg model.Config, repository *os.Root) (model.FunctionMeasurements, []model.Skip, error) {
	goValues, goSkips, err := golang.Metrics(ctx, root, paths, cfg, repository)
	if err != nil {
		return nil, nil, err
	}
	tsValues, tsSkips, err := typescript.Metrics(ctx, root, paths, cfg, repository)
	if err != nil {
		return nil, nil, err
	}
	for _, metric := range []model.Metric{model.MetricTSCognitive, model.MetricTSLines} {
		goValues[metric] = tsValues[metric]
	}
	return goValues, append(goSkips, tsSkips...), nil
}

func collectPolicyFindings(paths []string, cfg model.Config, repository *os.Root) ([]model.Finding, error) {
	goFindings, err := golang.SuppressionFindings(paths, cfg, repository)
	if err != nil {
		return nil, err
	}
	tsFindings, err := typescript.SuppressionFindings(paths, cfg, repository)
	if err != nil {
		return nil, err
	}
	return append(goFindings, tsFindings...), nil
}

func helpRequested(args []string) bool {
	return len(args) == 1 && (args[0] == "--help" || args[0] == "-h")
}

func printHelp(stdout io.Writer) int {
	if !writeOutput(stdout, "usage: code-shape-gate [--write] [--seed] <root>\n") {
		return 2
	}
	return 0
}

func loadRequestedBase(ctx context.Context, root string, env func(string) string) (model.Allowlist, error) {
	ref := env("GATE_BASE")
	if ref == "" {
		return model.Allowlist{}, nil
	}
	return loadBaseAllowlist(ctx, root, ref)
}

func printFindings(stdout, stderr io.Writer, findings []model.Finding) int {
	if err := writeFindings(stdout, findings); err != nil {
		writeOutput(stderr, "code-shape-gate: write output: %v\n", err)
		return 2
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
}

func writeUpdated(root string, config model.Config, allow, base model.Allowlist, snapshot model.Snapshot, seed bool,
	suppressions []model.Finding, stdout, stderr io.Writer) int {
	updated, changed, err := rewriteAllowlist(config, allow, snapshot, seed)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	findings := append(judge.Judge(config, updated, base, snapshot), suppressions...)
	sortFindings(findings)
	if len(findings) > 0 {
		return printFindings(stdout, stderr, findings)
	}
	if changed {
		if err := writeAllowlist(root, updated); err != nil {
			writeOutput(stderr, "code-shape-gate: write allowlist: %v\n", err)
			return 2
		}
	}
	return 0
}

func writeAllowlist(root string, updated model.Allowlist) error {
	repository, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}
	writeErr := repository.WriteFile("tools/code-shape-gate/allowlist.cue", allowlist.Render(updated), 0o644)
	closeErr := repository.Close()
	return errors.Join(writeErr, closeErr)
}

func parseArgs(args []string) (bool, bool, string, bool) {
	var writeMode, seed bool
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--write":
			if writeMode {
				return false, false, "", false
			}
			writeMode = true
		case "--seed":
			if seed {
				return false, false, "", false
			}
			seed = true
		default:
			return false, false, "", false
		}
		args = args[1:]
	}
	if len(args) != 1 || args[0] == "" || seed && !writeMode {
		return false, false, "", false
	}
	return writeMode, seed, args[0], true
}

func loadBaseAllowlist(ctx context.Context, root, ref string) (model.Allowlist, error) {
	command := exec.CommandContext(ctx, "git", "ls-tree", "--name-only", ref, "--", "tools/code-shape-gate/allowlist.cue")
	command.Dir = root
	data, err := command.Output()
	if err != nil {
		return model.Allowlist{}, gitBaseError(ref, err)
	}
	if len(data) == 0 {
		return emptyAllowlist(), nil
	}
	command = exec.CommandContext(ctx, "git", "show", "--end-of-options", ref+":tools/code-shape-gate/allowlist.cue")
	command.Dir = root
	data, err = command.Output()
	if err != nil {
		return model.Allowlist{}, gitBaseError(ref, err)
	}
	cueContext := cuecontext.New()
	schema, err := allowlistSchema(cueContext)
	if err != nil {
		return model.Allowlist{}, err
	}
	parsed, err := parseBaseAllowlist(cueContext, schema, data)
	if err != nil {
		return model.Allowlist{}, err
	}
	parsed.Present = true
	return parsed, nil
}

func gitBaseError(ref string, err error) error {
	exitError, ok := errors.AsType[*exec.ExitError](err)
	if ok {
		return fmt.Errorf("read base allowlist at %q: %w: %s", ref, err, strings.TrimSpace(string(exitError.Stderr)))
	}
	return fmt.Errorf("read base allowlist at %q: %w", ref, err)
}

func rewriteAllowlist(config model.Config, allow, snapshot model.Allowlist, values model.Snapshot, seed bool) (model.Allowlist, bool, error) {
	_ = snapshot
	updated := model.Allowlist{Files: make(map[string]model.Entry, len(allow.Files)),
		Dirs: make(map[string]model.Entry, len(allow.Dirs)), Functions: model.NewFunctionMeasurements(), Present: true}
	maps.Copy(updated.Files, allow.Files)
	maps.Copy(updated.Dirs, allow.Dirs)
	changedFiles, fileErrors := rewriteEntries(updated.Files, values.Files, config, model.MetricFiles, seed)
	changedDirs, dirErrors := rewriteEntries(updated.Dirs, values.Dirs, config, model.MetricDirs, seed)
	changedFunctions, functionErrors := model.RewriteFunctionEntries(updated.Functions, allow.Functions, values.Functions, config, seed)
	errs := append(fileErrors, dirErrors...)
	errs = append(errs, functionErrors...)
	sort.Strings(errs)
	if len(errs) > 0 {
		return allow, false, fmt.Errorf("%w: %s", errCannotRaiseAllowlist, strings.Join(errs, "; "))
	}
	return updated, changedFiles || changedDirs || changedFunctions, nil
}

func rewriteEntries(entries map[string]model.Entry, values map[string]int, cfg model.Config, metric model.Metric, seed bool) (bool, []string) {
	changed := dropClearedEntries(entries, values, cfg, metric)
	added, errs := addFunctionEntries(entries, values, cfg, metric, seed)
	return changed || added, errs
}

func dropClearedEntries(entries map[string]model.Entry, values map[string]int, cfg model.Config, metric model.Metric) bool {
	changed := false
	for path, entry := range entries {
		value, exists := values[path]
		if !exists || value <= limitFor(cfg, metric, path) {
			delete(entries, path)
			changed = true
		} else if value < entry.Value {
			entry.Value = value
			entries[path] = entry
			changed = true
		}
	}
	return changed
}

func addFunctionEntries(entries map[string]model.Entry, values map[string]int, cfg model.Config, metric model.Metric, seed bool) (bool, []string) {
	changed := false
	var errs []string
	for path, value := range values {
		limit := limitFor(cfg, metric, path)
		if value <= limit {
			continue
		}
		entry, exists := entries[path]
		if exists && value <= entry.Value {
			continue
		}
		if !seed {
			errs = append(errs, fmt.Sprintf("%s %s value %d needs --seed to add or raise the allowlist", path, metric, value))
			continue
		}
		if !exists {
			entry.Reason = "seeded at rollout, burn down"
		}
		entry.Value = value
		entries[path] = entry
		changed = true
	}
	return changed, errs
}

func limitFor(cfg model.Config, metric model.Metric, path string) int {
	if metric != model.MetricDirs {
		return cfg.FileLimit
	}
	limit := 0
	for pattern, entry := range cfg.DirLimits {
		if inventory.MatchGlob(pattern, path) && (limit == 0 || entry.Limit < limit) {
			limit = entry.Limit
		}
	}
	if limit == 0 {
		return cfg.DirLimit
	}
	return limit
}

func emptyAllowlist() model.Allowlist {
	return model.Allowlist{Files: map[string]model.Entry{}, Dirs: map[string]model.Entry{},
		Functions: model.NewFunctionMeasurements()}
}

func writeOutput(output io.Writer, format string, args ...any) bool {
	_, err := fmt.Fprintf(output, format, args...)
	return err == nil
}

func writeFindings(output io.Writer, findings []model.Finding) error {
	for _, finding := range findings {
		if _, err := fmt.Fprintf(output, "%s %s value=%d limit=%d fix=%s\n", finding.Path, finding.Metric,
			finding.Value, finding.Limit, finding.Fix); err != nil {
			return fmt.Errorf("write finding: %w", err)
		}
	}
	return nil
}

func sortFindings(findings []model.Finding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Metric < findings[j].Metric
	})
}
