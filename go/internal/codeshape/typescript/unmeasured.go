// SPDX-License-Identifier: MIT OR Apache-2.0

package typescript

import (
	"fmt"
	"path/filepath"

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
		if _, ok := metrics[diagnostic.Category]; !ok {
			return fmt.Errorf("biome reported %s: %w", diagnostic.Category, errBiomeReported)
		}
		count++
	}
	if count != report.Summary.Errors {
		return fmt.Errorf("biome reported %d errors with %d diagnostics: %w", report.Summary.Errors, count, errBiomeReported)
	}
	return nil
}
