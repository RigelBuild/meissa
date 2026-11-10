// SPDX-License-Identifier: MIT OR Apache-2.0

// Package cli implements the code shape gate command.
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

	"github.com/RigelBuild/meissa/go/internal/codeshape"
	shapeconfig "github.com/RigelBuild/meissa/go/internal/codeshape/config"
	"github.com/RigelBuild/meissa/go/internal/codeshape/golang"
	"github.com/RigelBuild/meissa/go/internal/codeshape/typescript"
)

var errCannotRaiseAllowlist = errors.New("--write cannot add or raise allowlist entries")

// Main runs the code-shape-gate CLI contract without exiting the process.
func Main(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		if !writeOutput(stdout, "usage: code-shape-gate [--write] [--seed] <root>\n") {
			return 2
		}
		return 0
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
	config, allow, err := shapeconfig.Load(root)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	err = reportUnmeasuredExtensions(stderr, config.Extensions)
	if err != nil {
		return 2
	}
	var base codeshape.Allowlist
	if baseRef := env("GATE_BASE"); baseRef != "" {
		base, err = loadBaseAllowlist(ctx, root, baseRef)
		if err != nil {
			writeOutput(stderr, "code-shape-gate: %v\n", err)
			return 2
		}
	}
	snapshot, err := codeshape.Inventory(ctx, root, config)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	suppressions, unmeasured, err := collectMeasurements(ctx, root, config, &snapshot, stderr)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	preserved := separateUnmeasured(&allow, unmeasured)
	if writeMode {
		return writeUpdated(root, config, allow, base, snapshot, seed, stdout, stderr, preserved, suppressions)
	}
	findings := append(codeshape.Judge(config, allow, base, snapshot), suppressions...)
	return printGateFindings(stdout, stderr, findings)
}

func printGateFindings(stdout, stderr io.Writer, findings []codeshape.Finding) int {
	codeshape.SortFindings(findings)
	err := writeFindings(stdout, findings)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: write output: %v\n", err)
		return 2
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
}

func reportUnmeasuredExtensions(output io.Writer, extensions []string) error {
	for _, extension := range extensions {
		switch extension {
		case ".go", ".ts", ".tsx", ".mts", ".cts":
			continue
		}
		_, err := fmt.Fprintf(output, "code-shape-gate: no function collector for %s\n", extension)
		if err != nil {
			return fmt.Errorf("write unmeasured extension: %w", err)
		}
	}
	return nil
}

func collectMeasurements(
	ctx context.Context, root string, config codeshape.Config, snapshot *codeshape.Snapshot, stderr io.Writer,
) ([]codeshape.Finding, []string, error) {
	functions, skips, err := golang.GoMetrics(ctx, root, config)
	if err != nil {
		return nil, nil, fmt.Errorf("collect Go metrics: %w", err)
	}
	goFindings, goPaths, err := handleGoSkips(stderr, skips)
	if err != nil {
		return nil, nil, err
	}
	tsFunctions, tsSkips, err := typescript.TSMetrics(ctx, root, config)
	if err != nil {
		return nil, nil, fmt.Errorf("collect TypeScript metrics: %w", err)
	}
	unmeasuredFindings, unmeasuredPaths, err := handleTSSkips(stderr, tsSkips, config)
	if err != nil {
		return nil, nil, err
	}
	functions[codeshape.MetricTSCognitive] = tsFunctions[codeshape.MetricTSCognitive]
	functions[codeshape.MetricTSLines] = tsFunctions[codeshape.MetricTSLines]
	snapshot.Functions = functions
	suppressions, err := golang.SuppressionFindings(ctx, root, config)
	if err != nil {
		return nil, nil, fmt.Errorf("collect Go suppressions: %w", err)
	}
	tsFindings, err := typescript.PolicyFindings(ctx, root, config)
	if err != nil {
		return nil, nil, fmt.Errorf("collect TypeScript policy: %w", err)
	}
	suppressions = append(suppressions, tsFindings...)
	suppressions = append(suppressions, goFindings...)
	suppressions = append(suppressions, unmeasuredFindings...)
	codeshape.SortFindings(suppressions)
	return suppressions, append(unmeasuredPaths, goPaths...), nil
}

func writeUpdated(
	root string,
	config codeshape.Config,
	allow, base codeshape.Allowlist,
	snapshot codeshape.Snapshot,
	seed bool,
	stdout, stderr io.Writer,
	preserved codeshape.FunctionMeasurements, policy ...[]codeshape.Finding,
) int {
	updated, changed, err := rewriteAllowlist(config, allow, snapshot, seed)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	findings := codeshape.Judge(config, updated, base, snapshot)
	ratchetFailures := len(findings) > 0
	if len(policy) > 0 {
		findings = append(findings, policy[0]...)
	}
	codeshape.SortFindings(findings)
	err = writeFindings(stdout, findings)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: write output: %v\n", err)
		return 2
	}
	restoreUnmeasured(&updated, preserved)
	if changed && !ratchetFailures {
		repository, openErr := os.OpenRoot(root)
		if openErr != nil {
			writeOutput(stderr, "code-shape-gate: open repository: %v\n", openErr)
			return 2
		}
		err = repository.WriteFile("tools/code-shape-gate/allowlist.cue", shapeconfig.Render(updated), 0o644)
		closeErr := repository.Close()
		err = errors.Join(err, closeErr)
		if err != nil {
			writeOutput(stderr, "code-shape-gate: write allowlist: %v\n", err)
			return 2
		}
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
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

func loadBaseAllowlist(ctx context.Context, root, ref string) (codeshape.Allowlist, error) {
	// ls-tree validates the ref and returns no paths for a missing allowlist.
	//nolint:gosec // The ref is an argument to a fixed git command.
	command := exec.CommandContext(ctx, "git", "ls-tree", "--name-only", ref, "--", "tools/code-shape-gate/allowlist.cue")
	command.Dir = root
	data, err := command.Output()
	if err != nil {
		return codeshape.Allowlist{}, gitBaseError(ref, err)
	}
	if len(data) == 0 {
		return codeshape.EmptyAllowlist(), nil
	}
	//nolint:gosec // The ref is an argument to a fixed git command.
	command = exec.CommandContext(ctx, "git", "show", "--end-of-options", ref+":tools/code-shape-gate/allowlist.cue")
	command.Dir = root
	data, err = command.Output()
	if err != nil {
		return codeshape.Allowlist{}, gitBaseError(ref, err)
	}
	allowlist, err := shapeconfig.DecodeBaseAllowlist(data)
	if err != nil {
		return codeshape.Allowlist{}, fmt.Errorf("decode base allowlist: %w", err)
	}
	allowlist.Present = true
	return allowlist, nil
}

func gitBaseError(ref string, err error) error {
	exitError, ok := errors.AsType[*exec.ExitError](err)
	if ok {
		return fmt.Errorf("read base allowlist at %q: %w: %s", ref, err, strings.TrimSpace(string(exitError.Stderr)))
	}
	return fmt.Errorf("read base allowlist at %q: %w", ref, err)
}

func rewriteAllowlist(
	cfg codeshape.Config, allow codeshape.Allowlist, snapshot codeshape.Snapshot, seed bool,
) (codeshape.Allowlist, bool, error) {
	updated := codeshape.Allowlist{
		Files:     make(map[string]codeshape.Entry, len(allow.Files)),
		Dirs:      make(map[string]codeshape.Entry, len(allow.Dirs)),
		Functions: make(codeshape.FunctionMeasurements),
		Present:   true,
	}
	maps.Copy(updated.Files, allow.Files)
	maps.Copy(updated.Dirs, allow.Dirs)
	changedFiles, fileErrors := rewriteEntries(updated.Files, snapshot.Files, cfg, codeshape.MetricFiles, seed)
	changedDirs, dirErrors := rewriteEntries(updated.Dirs, snapshot.Dirs, cfg, codeshape.MetricDirs, seed)
	changedFunctions, functionErrors := codeshape.RewriteFunctionEntries(
		updated.Functions, allow.Functions, snapshot.Functions, cfg, seed,
	)
	errs := make([]string, 0, len(fileErrors)+len(dirErrors))
	errs = append(errs, fileErrors...)
	errs = append(errs, dirErrors...)
	errs = append(errs, functionErrors...)
	changed := changedFiles || changedDirs || changedFunctions
	sort.Strings(errs)
	if len(errs) > 0 {
		return allow, false, fmt.Errorf("%w: %s", errCannotRaiseAllowlist, strings.Join(errs, "; "))
	}
	return updated, changed, nil
}

func rewriteEntries(
	entries map[string]codeshape.Entry,
	values map[string]int,
	cfg codeshape.Config,
	metric codeshape.Metric,
	seed bool,
) (bool, []string) {
	changed := false
	for path, entry := range entries {
		limit := cfg.LimitFor(metric, path)
		value, exists := values[path]
		if !exists || value <= limit {
			delete(entries, path)
			changed = true
		} else if value < entry.Value {
			entry.Value = value
			entries[path] = entry
			changed = true
		}
	}
	var errs []string
	for path, value := range values {
		limit := cfg.LimitFor(metric, path)
		if value <= limit {
			continue
		}
		entry, exists := entries[path]
		if exists && value <= entry.Value {
			continue
		}
		if seed {
			if !exists {
				entry.Reason = "seeded at rollout, burn down"
			}
			entry.Value = value
			entries[path] = entry
			changed = true
		} else {
			errMessage := fmt.Sprintf(
				"%s %s value %d needs --seed to add or raise the allowlist",
				codeshape.DisplayPath(metric, path),
				metric,
				value,
			)
			errs = append(errs, errMessage)
		}
	}
	return changed, errs
}

func writeOutput(output io.Writer, format string, args ...any) bool {
	_, err := fmt.Fprintf(output, format, args...)
	return err == nil
}

func writeFindings(output io.Writer, findings []codeshape.Finding) error {
	for _, finding := range findings {
		_, err := fmt.Fprintf(
			output,
			"%s %s value=%d limit=%d fix=%s\n",
			finding.Path,
			finding.Metric,
			finding.Value,
			finding.Limit,
			finding.Fix,
		)
		if err != nil {
			return fmt.Errorf("write finding: %w", err)
		}
	}
	return nil
}
