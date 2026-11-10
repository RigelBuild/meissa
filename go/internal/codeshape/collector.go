// SPDX-License-Identifier: MIT OR Apache-2.0

// Package codeshape defines code-shape measurements and judgments.
package codeshape

import "context"

// TrackedPaths lists tracked repository paths.
func TrackedPaths(ctx context.Context, root string) ([]string, error) { return trackedPaths(ctx, root) }

// CountedPath reports whether the configuration includes a path.
func CountedPath(path string, cfg Config) bool { return countedPath(path, cfg) }

// MatchesAny reports whether a path matches a pattern.
func MatchesAny(path string, patterns []string) bool { return matchesAny(path, patterns) }

// NewFunctionMeasurements initializes the function metric maps.
func NewFunctionMeasurements() map[Metric]map[string][]Entry { return newFunctionMeasurements() }

// AppendFunctionValue records a function measurement.
func AppendFunctionValue(values map[Metric]map[string][]Entry, metric Metric, path, id string, value int) {
	appendFunctionValue(values, metric, path, id, value)
}

// EmptyAllowlist creates an empty allowlist.
func EmptyAllowlist() Allowlist { return emptyAllowlist() }

// RewriteFunctionEntries updates the function ratchet entries.
func RewriteFunctionEntries(updated, allow, values FunctionMeasurements, cfg Config, seed bool) (bool, []string) {
	return rewriteFunctionEntries(updated, allow, values, cfg, seed)
}

// LimitFor returns the limit for a path and metric.
func (cfg Config) LimitFor(metric Metric, path string) int { return cfg.limitFor(metric, path) }

// DisplayPath formats a path for a finding.
func DisplayPath(metric Metric, path string) string { return displayPath(metric, path) }

// SortFindings orders findings by path and metric.
func SortFindings(findings []Finding) { sortFindings(findings) }

// IsFunctionMetric reports whether a metric measures functions.
func IsFunctionMetric(metric Metric) bool { return isFunctionMetric(metric) }

// ValidateFunctionEntryIDs checks function entry identifiers.
func ValidateFunctionEntryIDs(entries map[string][]Entry) error {
	return validateFunctionEntryIDs(entries)
}

// FunctionMetrics lists supported function metrics.
func FunctionMetrics() []Metric { return functionMetrics() }

// SortedFunctionEntries returns function entries ordered by identifier.
func SortedFunctionEntries(entries []Entry) []Entry { return sortedFunctionEntries(entries) }

// SortedEntryPaths returns the sorted keys of a path map.
func SortedEntryPaths[V any](entries map[string]V) []string { return sortedEntryPaths(entries) }

// LimitFor returns the threshold for a function metric.
func (limits FunctionLimits) LimitFor(metric Metric) int { return limits.limitFor(metric) }
