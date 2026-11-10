// SPDX-License-Identifier: MIT OR Apache-2.0

package renovatehashes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	sourceMarker = `hash = "sha256-`
	vendorMarker = `vendorHash = "sha256-`
)

func refreshTool(
	ctx context.Context,
	root string,
	entry Entry,
	text string,
	mode os.FileMode,
	run Runner,
	getenv func(string) string,
) (string, error) {
	if _, hasSource := entry.SourceAttr.Get(); hasSource {
		updated, err := refreshSourceTool(ctx, root, entry, text, mode, run, getenv)
		if err != nil {
			return "", err
		}
		text = updated
	}
	return refreshVendorHash(ctx, root, entry, text, mode, run)
}

func refreshSourceTool(
	ctx context.Context,
	root string,
	entry Entry,
	text string,
	mode os.FileMode,
	run Runner,
	getenv func(string) string,
) (string, error) {
	block, err := blockFor(text, entry)
	if err != nil {
		return "", err
	}
	updatedBlock, err := updateSourceVersion(ctx, entry, block, run, getenv)
	if err != nil {
		return "", err
	}
	text, err = replaceBlock(text, entry, updatedBlock)
	if err != nil {
		return "", err
	}
	writeErr := writeText(root, entry.File, text, mode)
	if writeErr != nil {
		return "", failf("could not write %s: %s", entry.File, writeErr)
	}
	sourceAttr, _ := entry.SourceAttr.Get()
	sourceHash, err := recomputeHash(ctx, root, entry, sourceMarker, sourceAttr, text, mode, run)
	if err != nil {
		return "", err
	}
	block, err = blockFor(text, entry)
	if err != nil {
		return "", err
	}
	block, err = rewriteHash(block, sourceMarker, sourceHash, entry)
	if err != nil {
		return "", err
	}
	text, err = replaceBlock(text, entry, block)
	if err != nil {
		return "", err
	}
	writeErr = writeText(root, entry.File, text, mode)
	if writeErr != nil {
		return "", failf("could not write %s: %s", entry.File, writeErr)
	}
	return text, nil
}

func updateSourceVersion(
	ctx context.Context,
	entry Entry,
	block string,
	run Runner,
	getenv func(string) string,
) (string, error) {
	if entry.Name == "golangci-lint" {
		return updateGolangciLintVersion(entry, block)
	}
	return updateNilawayVersion(ctx, entry, block, run, getenv)
}

func updateGolangciLintVersion(entry Entry, block string) (string, error) {
	version, err := fieldValue(block, "version", entry)
	if err != nil {
		return "", err
	}
	tag, err := golangciLintTag(version)
	if err != nil {
		return "", err
	}
	return rewriteField(block, "tag", tag, entry)
}

func updateNilawayVersion(
	ctx context.Context,
	entry Entry,
	block string,
	run Runner,
	getenv func(string) string,
) (string, error) {
	rev, err := fieldValue(block, "rev", entry)
	if err != nil {
		return "", err
	}
	date, err := nilawayVersionDate(ctx, rev, getenv("NILAWAY_FETCH_URL"), run)
	if err != nil {
		return "", err
	}
	return rewriteField(block, "version", "0-unstable-"+date, entry)
}

func refreshVendorHash(
	ctx context.Context,
	root string,
	entry Entry,
	text string,
	mode os.FileMode,
	run Runner,
) (string, error) {
	vendorHash, err := recomputeHash(ctx, root, entry, vendorMarker, entry.VendorAttr, text, mode, run)
	if err != nil {
		return "", err
	}
	block, err := blockFor(text, entry)
	if err != nil {
		return "", err
	}
	block, err = rewriteHash(block, vendorMarker, vendorHash, entry)
	if err != nil {
		return "", err
	}
	text, err = replaceBlock(text, entry, block)
	if err != nil {
		return "", err
	}
	return text, nil
}

func golangciLintTag(version string) (string, error) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`).MatchString(version) {
		return "", failf("invalid golangci-lint version '%s'", version)
	}
	return "v" + version, nil
}

func nilawayVersionDate(ctx context.Context, rev, fetchURL string, run Runner) (string, error) {
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(rev) {
		return "", failf("invalid nilaway revision %s", rev)
	}
	if fetchURL == "" {
		fetchURL = "https://github.com/uber-go/nilaway"
	}
	directory, err := os.MkdirTemp("", "renovate-go-analysis-")
	if err != nil {
		return "", failf("could not create temporary directory: %s", err)
	}
	defer func() { _ = os.RemoveAll(directory) }()
	_, stderr, exit, err := run.Run(ctx, "git", "-C", directory, "init", "-q")
	if err != nil || exit != 0 {
		if ctx.Err() != nil {
			return "", failf("interrupted by %s", context.Cause(ctx))
		}
		return "", failf("nilaway revision fetch failed: %s", commandOutput(err, stderr))
	}
	_, stderr, exit, err = run.Run(ctx, "git", "-C", directory, "fetch", "--depth=1", fetchURL, rev)
	if err != nil {
		if ctx.Err() != nil {
			return "", failf("interrupted by %s", context.Cause(ctx))
		}
		return "", withPrefix(err)
	}
	if exit != 0 {
		return "", failf("nilaway revision fetch failed: %s", stderr)
	}
	stdout, _, exit, err := runWithEnv(ctx, run, map[string]string{"TZ": "UTC"}, "git", "-C", directory,
		"log", "-1", "--date=format-local:%Y-%m-%d", "--format=%cd", "FETCH_HEAD")
	date := strings.TrimSpace(stdout)
	if err != nil || exit != 0 || !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(date) {
		if ctx.Err() != nil {
			return "", failf("interrupted by %s", context.Cause(ctx))
		}
		return "", failf("nilaway commit date lookup failed")
	}
	return date, nil
}

type envRunner interface {
	runWithEnv(
		ctx context.Context,
		extraEnv map[string]string,
		name string,
		args ...string,
	) (stdout, stderr string, exit int, err error)
}

func runWithEnv(
	ctx context.Context,
	run Runner,
	extraEnv map[string]string,
	name string,
	args ...string,
) (string, string, int, error) {
	if runner, ok := run.(envRunner); ok {
		return runner.runWithEnv(ctx, extraEnv, name, args...)
	}
	//nolint:wrapcheck // Preserve the runner's original error text.
	return run.Run(ctx, "env", append([]string{"TZ=UTC", name}, args...)...)
}

func resolveDrvPath(ctx context.Context, entry Entry, attr string, run Runner) (string, error) {
	var expression string
	if _, hasSource := entry.SourceAttr.Get(); hasSource {
		expression = "let flake = builtins.getFlake (toString ./.); " +
			"pkgs = flake.inputs.nixpkgs.legacyPackages.x86_64-linux; " +
			"tools = flake.lib.goAnalysis { inherit pkgs; goToolchain = pkgs.go; }; " +
			"in tools." + entry.Name + "." + attr + ".drvPath"
	} else {
		expression = "let flake = builtins.getFlake (toString ./.); " +
			"in flake.packages.x86_64-linux.tools.goModules.drvPath"
	}
	stdout, stderr, exit, err := run.Run(
		ctx, "nix", "eval", "--raw", "--impure", "--expr", expression,
	)
	if err != nil || exit != 0 {
		if ctx.Err() != nil {
			cause := context.Cause(ctx)
			return "", errors.Join(failf("interrupted by %s", cause), cause)
		}
		return "", failf("could not resolve %s.%s: %s", entry.Name, attr, commandOutput(err, stderr))
	}
	drvPath := strings.TrimSpace(stdout)
	if !strings.HasSuffix(drvPath, ".drv") {
		return "", failf("invalid derivation path for %s.%s", entry.Name, attr)
	}
	return filepath.Base(drvPath), nil
}

func recomputeHash(
	ctx context.Context,
	root string,
	entry Entry,
	marker string,
	hashAttr string,
	text string,
	mode os.FileMode,
	run Runner,
) (string, error) {
	block, err := blockFor(text, entry)
	if err != nil {
		return "", err
	}
	block, err = rewriteHash(block, marker, fakeSRI, entry)
	if err != nil {
		return "", err
	}
	updated, err := replaceBlock(text, entry, block)
	if err != nil {
		return "", err
	}
	writeErr := writeText(root, entry.File, updated, mode)
	if writeErr != nil {
		return "", failf("could not write %s: %s", entry.File, writeErr)
	}
	drvPath, err := resolveDrvPath(ctx, entry, hashAttr, run)
	if err != nil {
		return "", err
	}
	buildExpr := "let flake = builtins.getFlake (toString ./.); " +
		"in flake.packages.x86_64-linux.tools.goModules"
	if _, hasSource := entry.SourceAttr.Get(); hasSource {
		buildExpr = "let flake = builtins.getFlake (toString ./.); " +
			"pkgs = flake.inputs.nixpkgs.legacyPackages.x86_64-linux; " +
			"tools = flake.lib.goAnalysis { inherit pkgs; goToolchain = pkgs.go; }; " +
			"in tools." + entry.Name
	}
	stdout, stderr, exit, err := run.Run(
		ctx, "nix", "build", "--no-link", "--keep-going", "--impure", "--expr", buildExpr,
	)
	if err != nil {
		return "", withPrefix(err)
	}
	output := stdout + "\n" + stderr
	if exit == 0 {
		return "", failf("fake %s hash unexpectedly built successfully", hashAttr)
	}
	return parseGot(output, strings.TrimSuffix(drvPath, ".drv"))
}

func parseGot(output, drvFragment string) (string, error) {
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "hash mismatch in fixed-output derivation") || !strings.Contains(line, drvFragment) {
			continue
		}
		for _, candidate := range lines[i+1:] {
			if strings.Contains(candidate, "hash mismatch in fixed-output derivation") {
				break
			}
			match := regexp.MustCompile(`got:\s*(sha256-\S+)`).FindStringSubmatch(candidate)
			if len(match) == 2 {
				return match[1], nil
			}
		}
	}
	return "", failf("no got hash for %s: %s", drvFragment, output)
}

func blockFor(text string, entry Entry) (string, error) {
	if _, hasSource := entry.SourceAttr.Get(); !hasSource {
		return text, nil
	}
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), entry.Name+" = {") {
			start = i
			break
		}
	}
	if start < 0 {
		return "", failf("missing pin block for %s", entry.Name)
	}
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "};" {
			return strings.Join(lines[start:i+1], "\n"), nil
		}
	}
	return "", failf("missing pin block for %s", entry.Name)
}

func replaceBlock(text string, entry Entry, updated string) (string, error) {
	original, err := blockFor(text, entry)
	if err != nil {
		return "", err
	}
	if original == "" {
		return "", failf("cannot replace pin block for %s", entry.Name)
	}
	before, after, found := strings.Cut(text, original)
	if !found {
		return "", failf("cannot replace pin block for %s", entry.Name)
	}
	return before + updated + after, nil
}

func fieldValue(block, field string, entry Entry) (string, error) {
	pattern := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(field) + `\s*=\s*"([^"]+)"`)
	match := pattern.FindStringSubmatch(block)
	if len(match) != 2 {
		return "", failf("missing %s in %s block", field, entry.Name)
	}
	return match[1], nil
}

func rewriteField(block, field, value string, entry Entry) (string, error) {
	pattern := regexp.MustCompile(`(?m)^(\s*` + regexp.QuoteMeta(field) + `\s*=\s*")[^"]*("\s*;\s*)$`)
	if !pattern.MatchString(block) {
		return "", failf("missing %s in %s block", field, entry.Name)
	}
	return pattern.ReplaceAllStringFunc(block, func(line string) string {
		match := pattern.FindStringSubmatch(line)
		return match[1] + value + match[2]
	}), nil
}

func rewriteHash(block, marker, value string, entry Entry) (string, error) {
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		if !strings.Contains(line, marker) {
			continue
		}
		pattern := regexp.MustCompile(`sha256-[^"]*`)
		lines[i] = pattern.ReplaceAllStringFunc(line, func(string) string { return value })
		return strings.Join(lines, "\n"), nil
	}
	return "", failf("missing hash marker in %s block", entry.Name)
}

func fileHashValue(text, field string) (string, bool) {
	match := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(field) + `\s*=\s*"([^"]+)"`).FindStringSubmatch(text)
	if len(match) != 2 {
		return "", false
	}
	return match[1], true
}

func writeText(root, relative, text string, mode os.FileMode) error {
	//nolint:wrapcheck // Callers add the stable file-specific refresh diagnostic.
	return os.WriteFile(filepath.Join(root, filepath.FromSlash(relative)), []byte(text), mode)
}

func restoreFiles(root string, originals map[string]fileSnapshot) error {
	var restoreErrors []error
	for relative, snapshot := range originals {
		writeErr := os.WriteFile(filepath.Join(root, filepath.FromSlash(relative)), snapshot.data, snapshot.mode)
		if writeErr != nil {
			restoreErrors = append(restoreErrors, failf("could not restore %s: %s", relative, writeErr))
		}
	}
	return errors.Join(restoreErrors...)
}
