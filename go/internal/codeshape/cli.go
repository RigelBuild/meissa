// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

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
	config, allow, err := Load(root)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	var base Allowlist
	if baseRef := env("GATE_BASE"); baseRef != "" {
		base, err = loadBaseAllowlist(ctx, root, baseRef)
		if err != nil {
			writeOutput(stderr, "code-shape-gate: %v\n", err)
			return 2
		}
	}
	snapshot, err := Inventory(ctx, root, config)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	functions, err := GoMetrics(ctx, root, config)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	tsFunctions, err := TSMetrics(ctx, root, config)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	functions[MetricTSCognitive] = tsFunctions[MetricTSCognitive]
	functions[MetricTSLines] = tsFunctions[MetricTSLines]
	snapshot.Functions = functions
	suppressions, err := goSuppressionFindings(ctx, root, config)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	tsFindings, err := tsPolicyFindings(ctx, root, config)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	suppressions = append(suppressions, tsFindings...)
	sortFindings(suppressions)
	if len(suppressions) > 0 {
		err := writeFindings(stdout, suppressions)
		if err != nil {
			writeOutput(stderr, "code-shape-gate: write output: %v\n", err)
			return 2
		}
		return 1
	}
	if writeMode {
		return writeUpdated(root, config, allow, base, snapshot, seed, stdout, stderr)
	}
	findings := Judge(config, allow, base, snapshot)
	err = writeFindings(stdout, findings)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: write output: %v\n", err)
		return 2
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
}

func writeUpdated(
	root string,
	config Config,
	allow, base Allowlist,
	snapshot Snapshot,
	seed bool,
	stdout, stderr io.Writer,
) int {
	updated, changed, err := rewriteAllowlist(config, allow, snapshot, seed)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	findings := Judge(config, updated, base, snapshot)
	err = writeFindings(stdout, findings)
	if err != nil {
		writeOutput(stderr, "code-shape-gate: write output: %v\n", err)
		return 2
	}
	if len(findings) > 0 {
		return 1
	}
	if changed {
		repository, openErr := os.OpenRoot(root)
		if openErr != nil {
			writeOutput(stderr, "code-shape-gate: open repository: %v\n", openErr)
			return 2
		}
		err = repository.WriteFile("tools/code-shape-gate/allowlist.cue", Render(updated), 0o644)
		closeErr := repository.Close()
		err = errors.Join(err, closeErr)
		if err != nil {
			writeOutput(stderr, "code-shape-gate: write allowlist: %v\n", err)
			return 2
		}
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

func loadBaseAllowlist(ctx context.Context, root, ref string) (Allowlist, error) {
	// ls-tree validates the ref and returns no paths for a missing allowlist.
	//nolint:gosec // The ref is an argument to a fixed git command.
	command := exec.CommandContext(ctx, "git", "ls-tree", "--name-only", ref, "--", "tools/code-shape-gate/allowlist.cue")
	command.Dir = root
	data, err := command.Output()
	if err != nil {
		return Allowlist{}, gitBaseError(ref, err)
	}
	if len(data) == 0 {
		return emptyAllowlist(), nil
	}
	//nolint:gosec // The ref is an argument to a fixed git command.
	command = exec.CommandContext(ctx, "git", "show", "--end-of-options", ref+":tools/code-shape-gate/allowlist.cue")
	command.Dir = root
	data, err = command.Output()
	if err != nil {
		return Allowlist{}, gitBaseError(ref, err)
	}
	cueContext := cuecontext.New()
	schema, err := schemaValue(cueContext)
	if err != nil {
		return Allowlist{}, err
	}
	allowlist, err := decodeAllowlistBytes(cueContext, schema, data, "base allowlist")
	if err != nil {
		return Allowlist{}, err
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

func rewriteAllowlist(cfg Config, allow Allowlist, snapshot Snapshot, seed bool) (Allowlist, bool, error) {
	updated := Allowlist{
		Files:     make(map[string]Entry, len(allow.Files)),
		Dirs:      make(map[string]Entry, len(allow.Dirs)),
		Functions: make(FunctionMeasurements),
		Present:   true,
	}
	maps.Copy(updated.Files, allow.Files)
	maps.Copy(updated.Dirs, allow.Dirs)
	changedFiles, fileErrors := rewriteEntries(updated.Files, snapshot.Files, cfg, MetricFiles, seed)
	changedDirs, dirErrors := rewriteEntries(updated.Dirs, snapshot.Dirs, cfg, MetricDirs, seed)
	changedFunctions, functionErrors := rewriteFunctionEntries(
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
	entries map[string]Entry,
	values map[string]int,
	cfg Config,
	metric Metric,
	seed bool,
) (bool, []string) {
	changed := false
	for path, entry := range entries {
		limit := cfg.limitFor(metric, path)
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
		limit := cfg.limitFor(metric, path)
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
				displayPath(metric, path),
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

func writeFindings(output io.Writer, findings []Finding) error {
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
