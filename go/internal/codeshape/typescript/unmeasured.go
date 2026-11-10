// SPDX-License-Identifier: MIT OR Apache-2.0

package typescript

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/codeshape"
)

func recordUnparseableIssue(skips []codeshape.Skip, path string, metric codeshape.Metric, message string) (
	bool, error,
) {
	for _, skip := range skips {
		if skip.Path != path || skip.Unmeasured == nil {
			continue
		}
		value, err := biomeValue(message, metric)
		if err != nil {
			return true, err
		}
		skip.Unmeasured.Values[metric] = max(skip.Unmeasured.Values[metric], value)
		return true, nil
	}
	return false, nil
}
func isTypeScriptPath(path string) bool {
	switch filepath.Ext(path) {
	case ".ts", ".tsx", ".mts", ".cts":
		return true
	default:
		return false
	}
}

func validateBiomeErrors(report biomeReport) error {
	count := 0
	metrics := biomeRuleMetrics()
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Severity != "error" {
			continue
		}
		if _, ok := metrics[diagnostic.Category]; !ok && diagnostic.Category != "parse" &&
			!strings.HasPrefix(diagnostic.Category, "parse/") {
			return fmt.Errorf("biome reported %s: %w", diagnostic.Category, errBiomeReported)
		}
		count++
	}
	if count != report.Summary.Errors {
		return fmt.Errorf("biome reported %d errors with %d diagnostics: %w", report.Summary.Errors, count, errBiomeReported)
	}
	return nil
}

func biomeParseSkips(report biomeReport, sources map[string][]byte) ([]codeshape.Skip, error) {
	var skips []codeshape.Skip
	seen := map[string]bool{}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Category != "parse" && !strings.HasPrefix(diagnostic.Category, "parse/") {
			continue
		}
		path := filepath.ToSlash(diagnostic.Location.Path)
		if seen[path] {
			continue
		}
		seen[path] = true
		if _, ok := sources[path]; !ok {
			return nil, fmt.Errorf("%w %q", errBiomeUnknownSource, path)
		}
		delete(sources, path)
		skips = append(skips, codeshape.Skip{Path: path, Reason: "Biome parse error: " + diagnostic.Message,
			Unmeasured: &codeshape.UnmeasuredFunctions{Values: map[codeshape.Metric]int{}, Ignored: true}})
	}
	return skips, nil
}
