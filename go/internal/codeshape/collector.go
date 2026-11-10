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
