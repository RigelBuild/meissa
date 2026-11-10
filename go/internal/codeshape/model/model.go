// SPDX-License-Identifier: MIT OR Apache-2.0

// Package model holds the data types used by the code-shape gate.
package model

import (
	"fmt"
	"sort"
)

// Metric identifies one measured code-shape dimension.
type Metric string

// Metric constants identify the supported code-shape dimensions.
const (
	MetricFiles        Metric = "files"
	MetricDirs         Metric = "dirs"
	MetricGoCognitive  Metric = "go-cognitive"
	MetricGoLines      Metric = "go-lines"
	MetricGoStatements Metric = "go-statements"
	MetricTSCognitive  Metric = "ts-cognitive"
	MetricTSLines      Metric = "ts-lines"
)

// FunctionMeasurements maps each metric and source path to individual functions.
type FunctionMeasurements map[Metric]map[string][]Entry

type functionIDError struct{}

func (functionIDError) Error() string { return "invalid function allowlist id" }

var errFunctionID error = functionIDError{}

// FunctionMetrics returns the supported function metrics in stable order.
func FunctionMetrics() []Metric {
	return []Metric{MetricGoCognitive, MetricGoLines, MetricGoStatements, MetricTSCognitive, MetricTSLines}
}

// IsFunctionMetric reports whether metric has a function collector.
func IsFunctionMetric(metric Metric) bool {
	for _, candidate := range FunctionMetrics() {
		if metric == candidate {
			return true
		}
	}
	return false
}

// NewFunctionMeasurements creates initialized maps for every function metric.
func NewFunctionMeasurements() FunctionMeasurements {
	values := make(FunctionMeasurements, len(FunctionMetrics()))
	for _, metric := range FunctionMetrics() {
		values[metric] = make(map[string][]Entry)
	}
	return values
}

// AppendFunctionValue appends one function value and rejects duplicate ids.
func AppendFunctionValue(values FunctionMeasurements, metric Metric, path, id string, value int) error {
	entries := values[metric][path]
	for _, entry := range entries {
		if entry.ID == id {
			return fmt.Errorf("duplicate %s measurement for %s#%s", metric, path, id)
		}
	}
	values[metric][path] = append(entries, Entry{ID: id, Value: value})
	return nil
}

// SetFunctionDiagnostic sets one diagnostic on an initialized function.
func SetFunctionDiagnostic(values FunctionMeasurements, metric Metric, path, id string, value int) error {
	entries := values[metric][path]
	for index := range entries {
		if entries[index].ID != id {
			continue
		}
		if entries[index].Value != 0 {
			return fmt.Errorf("duplicate %s diagnostic for %s#%s", metric, path, id)
		}
		entries[index].Value = value
		values[metric][path] = entries
		return nil
	}
	return fmt.Errorf("missing %s function measurement for %s#%s", metric, path, id)
}

// FunctionEntry finds one function entry by id.
func FunctionEntry(entries []Entry, id string) (Entry, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return Entry{}, false
}

// SortedFunctionEntries returns function entries sorted by id.
func SortedFunctionEntries(entries []Entry) []Entry {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	return sorted
}

// ValidateFunctionEntryIDs checks that allowlist function ids are non-empty and unique.
func ValidateFunctionEntryIDs(entries map[string][]Entry) error {
	for path, functions := range entries {
		seen := make(map[string]bool, len(functions))
		for _, entry := range functions {
			if entry.ID == "" || seen[entry.ID] {
				return fmt.Errorf("%w: function allowlist entry for %q has invalid or repeated id %q", errFunctionID, path, entry.ID)
			}
			seen[entry.ID] = true
		}
	}
	return nil
}

// RewriteFunctionEntries applies the allowlist ratchet and optional seeding.
func RewriteFunctionEntries(updated, allow, values FunctionMeasurements, cfg Config, seed bool) (bool, []string) {
	changed := false
	var errs []string
	for _, metric := range FunctionMetrics() {
		paths, dropped := retainedFunctionEntries(allow[metric], values[metric], cfg, metric)
		changed = changed || dropped
		added, addErrors := seedFunctionEntries(paths, values[metric], cfg, metric, seed)
		changed = changed || added
		errs = append(errs, addErrors...)
		if len(paths) > 0 {
			updated[metric] = paths
		}
	}
	return changed, errs
}

func retainedFunctionEntries(allow, values map[string][]Entry, cfg Config, metric Metric) (map[string][]Entry, bool) {
	paths := make(map[string][]Entry, len(allow))
	changed := false
	for path, entries := range allow {
		for _, entry := range entries {
			current, exists := FunctionEntry(values[path], entry.ID)
			if !exists || current.Value <= cfg.Functions.LimitFor(metric) {
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
	return paths, changed
}

func seedFunctionEntries(paths, values map[string][]Entry, cfg Config, metric Metric, seed bool) (bool, []string) {
	changed := false
	var errs []string
	for path, entries := range values {
		for _, current := range entries {
			if current.Value <= cfg.Functions.LimitFor(metric) {
				continue
			}
			entry, exists := FunctionEntry(paths[path], current.ID)
			if exists && current.Value <= entry.Value {
				continue
			}
			if !seed {
				errs = append(errs, fmt.Sprintf("%s#%s %s value %d needs --seed to add or raise the allowlist",
					path, current.ID, metric, current.Value))
				continue
			}
			entry = seededFunctionEntry(entry, current, exists)
			replaceFunctionEntry(paths, path, entry, exists)
			changed = true
		}
	}
	return changed, errs
}

func seededFunctionEntry(entry, current Entry, exists bool) Entry {
	if !exists {
		entry = Entry{ID: current.ID, Reason: "seeded at rollout, burn down"}
	}
	entry.Value = current.Value
	return entry
}

func replaceFunctionEntry(paths map[string][]Entry, path string, entry Entry, exists bool) {
	if !exists {
		paths[path] = append(paths[path], entry)
		return
	}
	for index := range paths[path] {
		if paths[path][index].ID == entry.ID {
			paths[path][index] = entry
			return
		}
	}
}

