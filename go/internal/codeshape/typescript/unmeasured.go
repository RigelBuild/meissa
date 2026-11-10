// SPDX-License-Identifier: MIT OR Apache-2.0

package typescript

import (
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
