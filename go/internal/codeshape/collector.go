// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import "context"

func TrackedPaths(ctx context.Context, root string) ([]string, error) { return trackedPaths(ctx, root) }
func CountedPath(path string, cfg Config) bool                        { return countedPath(path, cfg) }
func MatchesAny(path string, patterns []string) bool                  { return matchesAny(path, patterns) }
func NewFunctionMeasurements() map[Metric]map[string][]Entry          { return newFunctionMeasurements() }
func AppendFunctionValue(values map[Metric]map[string][]Entry, metric Metric, path, id string, value int) {
	appendFunctionValue(values, metric, path, id, value)
}
func EmptyAllowlist() Allowlist { return emptyAllowlist() }

func RewriteFunctionEntries(updated, allow, values FunctionMeasurements, cfg Config, seed bool) (bool, []string) {
	return rewriteFunctionEntries(updated, allow, values, cfg, seed)
}

func (cfg Config) LimitFor(metric Metric, path string) int { return cfg.limitFor(metric, path) }
func DisplayPath(metric Metric, path string) string        { return displayPath(metric, path) }
func SortFindings(findings []Finding)                      { sortFindings(findings) }
func IsFunctionMetric(metric Metric) bool                  { return isFunctionMetric(metric) }
func ValidateFunctionEntryIDs(entries map[string][]Entry) error {
	return validateFunctionEntryIDs(entries)
}
func FunctionMetrics() []Metric                             { return functionMetrics() }
func SortedFunctionEntries(entries []Entry) []Entry         { return sortedFunctionEntries(entries) }
func SortedEntryPaths[V any](entries map[string]V) []string { return sortedEntryPaths(entries) }
func (limits FunctionLimits) LimitFor(metric Metric) int    { return limits.limitFor(metric) }
