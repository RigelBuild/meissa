// SPDX-License-Identifier: MIT OR Apache-2.0

package typescript

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/codeshape"
	"github.com/bmatcuk/doublestar/v4"
)

var errBiomeScope = errors.New("invalid or inconsistent Biome scope")

type biomeScope struct {
	dir    string
	config map[string]any
}

func biomeScopes(root string, paths []string) ([]biomeScope, error) {
	var scopes []biomeScope
	for _, name := range paths {
		if path.Base(name) != "biome.json" && path.Base(name) != "biome.jsonc" {
			continue
		}
		config, err := readBiomeConfig(root, name)
		if err != nil {
			return nil, err
		}
		scopes = append(scopes, biomeScope{dir: path.Dir(name), config: config})
	}
	return scopes, nil
}

func biomeIncludes(raw any, name string) (bool, error) {
	patterns, ok := raw.([]any)
	if !ok {
		return true, nil
	}
	included := false
	for _, item := range patterns {
		pattern, ok := item.(string)
		if !ok {
			return false, fmt.Errorf("include pattern: %w", errBiomeScope)
		}
		negated := strings.HasPrefix(pattern, "!")
		pattern = strings.TrimPrefix(pattern, "!")
		matched, err := doublestar.Match(pattern, name)
		if err != nil {
			return false, fmt.Errorf("invalid Biome glob %q: %w", pattern, err)
		}
		if matched {
			included = !negated
		}
	}
	return included, nil
}

func biomeExcluded(name string, scopes []biomeScope) (bool, error) {
	enabled := true
	for _, scope := range scopes {
		relative := name
		if scope.dir != "." {
			prefix := scope.dir + "/"
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			relative = strings.TrimPrefix(name, prefix)
		}
		files, _ := scope.config["files"].(map[string]any)
		included, err := biomeIncludes(files["includes"], relative)
		if err != nil {
			return false, err
		}
		if !included {
			return true, nil
		}
		linter, _ := scope.config["linter"].(map[string]any)
		if value, ok := linter["enabled"].(bool); ok {
			enabled = value
		}
		enabled, err = biomeOverrideEnabled(scope.config, relative, enabled)
		if err != nil {
			return false, err
		}
	}
	return !enabled, nil
}

func biomeOverrideEnabled(config map[string]any, name string, enabled bool) (bool, error) {
	overrides, _ := config["overrides"].([]any)
	for _, raw := range overrides {
		override, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		applies, err := biomeIncludes(override["includes"], name)
		if err != nil {
			return false, err
		}
		linter, _ := override["linter"].(map[string]any)
		if value, ok := linter["enabled"].(bool); applies && ok {
			enabled = value
		}
	}
	return enabled, nil
}

func biomeSkippedPaths(root string, tracked, paths []string, sources map[string][]byte) (
	[]codeshape.Skip, []string, []string, error,
) {
	scopes, err := biomeScopes(root, tracked)
	if err != nil {
		return nil, nil, nil, err
	}
	var skips []codeshape.Skip
	var included, ignored []string
	for _, name := range paths {
		excluded, err := biomeExcluded(name, scopes)
		if err != nil {
			return nil, nil, nil, err
		}
		if excluded {
			ignored = append(ignored, name)
			delete(sources, name)
			skips = append(skips, codeshape.Skip{Path: name, Reason: "ignored by Biome",
				Unmeasured: &codeshape.UnmeasuredFunctions{Values: map[codeshape.Metric]int{}, Ignored: true}})
		} else {
			included = append(included, name)
		}
	}
	return skips, included, ignored, nil
}

func runBiomePartition(ctx context.Context, binary, root string, paths []string, expected int) (biomeReport, error) {
	if len(paths) == 0 {
		return biomeReport{Summary: nil, Diagnostics: nil}, nil
	}
	report, err := runBiome(ctx, binary, root, paths)
	if err != nil {
		return biomeReport{}, err
	}
	if report.Summary.DiagnosticsNotPrinted != 0 {
		return biomeReport{}, fmt.Errorf("%w: %d were not printed", errBiomeTruncated, report.Summary.DiagnosticsNotPrinted)
	}
	if report.Summary.Changed+report.Summary.Unchanged != expected {
		return biomeReport{}, fmt.Errorf("processed count differs from configuration: %w", errBiomeScope)
	}
	return report, nil
}
