// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import (
	"errors"
	"fmt"
	"slices"
	"sort"
)

// Skip records a source file that could not be measured.
type Skip struct {
	Path   string
	Reason string
}

var errFunctionID = errors.New("invalid function allowlist id")

// FunctionLimits holds per-language function thresholds.
type FunctionLimits struct {
	Go GoFunctionLimits `json:"go"`
	TS TSFunctionLimits `json:"ts"`
}

// GoFunctionLimits holds Go function thresholds.
type GoFunctionLimits struct {
	Cognitive  int `json:"cognitive"`
	Lines      int `json:"lines"`
	Statements int `json:"statements"`
}

// TSFunctionLimits holds TypeScript function thresholds.
type TSFunctionLimits struct {
	Cognitive int `json:"cognitive"`
	Lines     int `json:"lines"`
}

func (limits FunctionLimits) limitFor(metric Metric) int {
	switch metric {
	case MetricGoCognitive:
		return limits.Go.Cognitive
	case MetricGoLines:
		return limits.Go.Lines
	case MetricGoStatements:
		return limits.Go.Statements
	case MetricTSCognitive:
		return limits.TS.Cognitive
	case MetricTSLines:
		return limits.TS.Lines
	case MetricFiles, MetricDirs:
		return 0
	default:
		return 0
	}
}

func isFunctionMetric(metric Metric) bool {
	return slices.Contains(functionMetrics(), metric)
}

func functionMetrics() []Metric {
	return []Metric{MetricGoCognitive, MetricGoLines, MetricGoStatements, MetricTSCognitive, MetricTSLines}
}
func newFunctionMeasurements() map[Metric]map[string][]Entry {
	values := make(map[Metric]map[string][]Entry)
	for _, metric := range functionMetrics() {
		values[metric] = make(map[string][]Entry)
	}
	return values
}

func appendFunctionValue(values map[Metric]map[string][]Entry, metric Metric, path, id string, value int) {
	entries := values[metric][path]
	for index, entry := range entries {
		if entry.ID == id {
			if value > entry.Value {
				entries[index].Value = value
			}
			values[metric][path] = entries
			return
		}
	}
	values[metric][path] = append(entries, Entry{ID: id, Value: value, Reason: ""})
}

func sortedFunctionEntries(entries []Entry) []Entry {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	return sorted
}

func validateFunctionEntryIDs(entries map[string][]Entry) error {
	for path, functions := range entries {
		seen := make(map[string]bool, len(functions))
		for _, entry := range functions {
			if entry.ID == "" {
				return fmt.Errorf("%w: function allowlist entry for %q has an empty id", errFunctionID, path)
			}
			if seen[entry.ID] {
				return fmt.Errorf("%w: function allowlist entry for %q repeats id %q", errFunctionID, path, entry.ID)
			}
			seen[entry.ID] = true
		}
	}
	return nil
}

func rewriteFunctionEntries(updated, allow, values FunctionMeasurements, cfg Config, seed bool) (bool, []string) {
	changed := false
	errs := []string{}
	for _, metric := range functionMetrics() {
		paths := map[string][]Entry{}
		for path, entries := range allow[metric] {
			for _, entry := range entries {
				current, exists := functionEntry(values[metric][path], entry.ID)
				if !exists || current.Value <= cfg.Functions.limitFor(metric) {
					changed = true
					continue
				}
				if current.Value < entry.Value {
					entry.Value = current.Value
					changed = true
				}
				paths[path] = append(paths[path], entry)
			}
		}
		for path, entries := range values[metric] {
			for _, current := range entries {
				if current.Value <= cfg.Functions.limitFor(metric) {
					continue
				}
				entry, exists := functionEntry(paths[path], current.ID)
				if exists && current.Value <= entry.Value {
					continue
				}
				if !seed {
					errs = append(errs, fmt.Sprintf(
						"%s#%s %s value %d needs --seed to add or raise the allowlist", path, current.ID, metric, current.Value,
					))
					continue
				}
				if !exists {
					entry = Entry{ID: current.ID, Value: 0, Reason: "seeded at rollout, burn down"}
				}
				entry.Value = current.Value
				if exists {
					for i := range paths[path] {
						if paths[path][i].ID == current.ID {
							paths[path][i] = entry
						}
					}
				} else {
					paths[path] = append(paths[path], entry)
				}
				changed = true
			}
		}
		if len(paths) > 0 {
			updated[metric] = paths
		}
	}
	return changed, errs
}
