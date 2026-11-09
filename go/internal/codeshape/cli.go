// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Main runs the code-shape-gate CLI contract without exiting the process.
func Main(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(stdout, "usage: code-shape-gate [--write] [--seed] <root>")
		return 0
	}
	writeMode, seed, root, ok := parseArgs(args)
	if !ok {
		fmt.Fprintln(stderr, "usage: code-shape-gate [--write] [--seed] <root>")
		return 2
	}
	config, allow, err := Load(root)
	if err != nil {
		fmt.Fprintf(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	var base Allowlist
	if baseRef := env("GATE_BASE"); baseRef != "" {
		base, err = loadBaseAllowlist(ctx, root, baseRef)
		if err != nil {
			fmt.Fprintf(stderr, "code-shape-gate: %v\n", err)
			return 2
		}
	}
	snapshot, err := Inventory(ctx, root, config)
	if err != nil {
		fmt.Fprintf(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	findings := Judge(config, allow, base, snapshot)
	if baseFindings := judgeBaseEntries(allow, base); len(baseFindings) > 0 {
		sortFindings(baseFindings)
		if err := writeFindings(stdout, baseFindings); err != nil {
			fmt.Fprintf(stderr, "code-shape-gate: write output: %v\n", err)
			return 2
		}
		return 1
	}
	if writeMode {
		return writeUpdated(root, config, allow, base, snapshot, seed, stdout, stderr)
	}
	if err := writeFindings(stdout, findings); err != nil {
		fmt.Fprintf(stderr, "code-shape-gate: write output: %v\n", err)
		return 2
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
}

func writeUpdated(root string, config Config, allow, base Allowlist, snapshot Snapshot, seed bool, stdout, stderr io.Writer) int {
	updated, changed, err := rewriteAllowlist(config, allow, snapshot, seed)
	if err != nil {
		fmt.Fprintf(stderr, "code-shape-gate: %v\n", err)
		return 2
	}
	findings := Judge(config, updated, base, snapshot)
	if err := writeFindings(stdout, findings); err != nil {
		fmt.Fprintf(stderr, "code-shape-gate: write output: %v\n", err)
		return 2
	}
	if len(findings) > 0 {
		return 1
	}
	if changed {
		path := filepath.Join(root, "tools", "code-shape-gate", "allowlist.cue")
		if err := os.WriteFile(path, Render(updated), 0o644); err != nil {
			fmt.Fprintf(stderr, "code-shape-gate: write allowlist: %v\n", err)
			return 2
		}
	}
	return 0
}

func parseArgs(args []string) (writeMode, seed bool, root string, ok bool) {
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
	command := exec.CommandContext(ctx, "git", "show", ref+":tools/code-shape-gate/allowlist.cue")
	command.Dir = root
	data, err := command.Output()
	if err != nil {
		if isMissingBasePath(err) {
			return Allowlist{}, nil
		}
		return Allowlist{}, fmt.Errorf("read base allowlist at %q: %w", ref, err)
	}
	cueContext := newCueContext()
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

func isMissingBasePath(err error) bool {
	exitError, ok := err.(*exec.ExitError)
	return ok && strings.Contains(string(exitError.Stderr), "does not exist")
}

func rewriteAllowlist(cfg Config, allow Allowlist, snapshot Snapshot, seed bool) (Allowlist, bool, error) {
	updated := Allowlist{
		Files:     make(map[string]Entry, len(allow.Files)),
		Dirs:      make(map[string]Entry, len(allow.Dirs)),
		Functions: allow.Functions,
		Present:   true,
	}
	copyEntries(updated.Files, allow.Files)
	copyEntries(updated.Dirs, allow.Dirs)
	changedFiles, fileErrors := rewriteEntries(updated.Files, snapshot.Files, cfg.FileLimit, MetricFiles, seed)
	changedDirs, dirErrors := rewriteEntries(updated.Dirs, snapshot.Dirs, cfg.DirLimit, MetricDirs, seed)
	errs := append(fileErrors, dirErrors...)
	changed := changedFiles || changedDirs
	sort.Strings(errs)
	if len(errs) > 0 {
		return allow, false, fmt.Errorf("--write cannot add or raise allowlist entries: %s", strings.Join(errs, "; "))
	}
	return updated, changed, nil
}

func rewriteEntries(entries map[string]Entry, values map[string]int, limit int, metric Metric, seed bool) (bool, []string) {
	changed := false
	for path, entry := range entries {
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
		if value <= limit {
			continue
		}
		entry, exists := entries[path]
		if exists && value <= entry.Value {
			continue
		}
		if seed {
			entries[path] = Entry{Value: value, Reason: "seeded at rollout, burn down"}
			changed = true
		} else {
			errs = append(errs, fmt.Sprintf("%s %s value %d needs --seed to add or raise the allowlist", displayPath(metric, path), metric, value))
		}
	}
	return changed, errs
}

func copyEntries(dst, src map[string]Entry) {
	for path, entry := range src {
		dst[path] = entry
	}
}

func writeFindings(output io.Writer, findings []Finding) error {
	for _, finding := range findings {
		if _, err := fmt.Fprintf(output, "%s %s value=%d limit=%d fix=%s\n", finding.Path, finding.Metric, finding.Value, finding.Limit, finding.Fix); err != nil {
			return err
		}
	}
	return nil
}
