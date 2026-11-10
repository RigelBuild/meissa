// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import (
	"sort"
)

// Judge compares current measurements with thresholds and allowlists.
func Judge(cfg Config, allow, baseAllow Allowlist, snap Snapshot) []Finding {
	findings := make([]Finding, 0)
	findings = append(findings, judgeMetric(cfg, snap.Files, allow.Files, MetricFiles)...)
	findings = append(findings, judgeMetric(cfg, snap.Dirs, allow.Dirs, MetricDirs)...)
	findings = append(findings, judgeFunctions(cfg, snap.Functions, allow.Functions)...)
	findings = append(findings, judgeBaseEntries(allow, baseAllow)...)
	for _, path := range SortedEntryPaths(allow.Files) {
		if _, exists := snap.Files[path]; !exists {
			findings = append(findings, Finding{
				Path:   path,
				Metric: MetricFiles,
				Value:  0,
				Limit:  cfg.FileLimit,
				Fix:    "remove the stale allowlist entry",
			})
		}
	}
	for _, path := range SortedEntryPaths(allow.Dirs) {
		if _, exists := snap.Dirs[path]; !exists {
			findings = append(findings, Finding{
				Path:   displayDir(path),
				Metric: MetricDirs,
				Value:  0,
				Limit:  cfg.LimitFor(MetricDirs, path),
				Fix:    "remove the stale allowlist entry",
			})
		}
	}
	for metric, paths := range allow.Functions {
		for path, entries := range paths {
			for _, entry := range entries {
				if !functionExists(snap.Functions[metric][path], entry.ID) {
					findings = append(findings, Finding{
						Path: path + "#" + entry.ID, Metric: metric, Value: 0,
						Limit: cfg.Functions.LimitFor(metric), Fix: "remove the stale allowlist entry",
					})
				}
			}
		}
	}
	SortFindings(findings)
	return findings
}

func judgeMetric(cfg Config, values map[string]int, entries map[string]Entry, metric Metric) []Finding {
	paths := SortedEntryPaths(values)
	findings := make([]Finding, 0)
	for _, path := range paths {
		value := values[path]
		limit := cfg.LimitFor(metric, path)
		entry, listed := entries[path]
		if !listed {
			if value > limit {
				findings = append(findings, Finding{
					Path:   DisplayPath(metric, path),
					Metric: metric,
					Value:  value,
					Limit:  limit,
					Fix:    fixForPath(cfg, metric, path),
				})
			}
			continue
		}
		if value <= limit {
			findings = append(findings, Finding{
				Path:   DisplayPath(metric, path),
				Metric: metric,
				Value:  value,
				Limit:  limit,
				Fix:    "remove the allowlist entry after splitting or shrinking",
			})
			continue
		}
		if value > entry.Value {
			findings = append(findings, Finding{
				Path:   DisplayPath(metric, path),
				Metric: metric,
				Value:  value,
				Limit:  entry.Value,
				Fix:    "grew: split the offender instead of growing its recorded value",
			})
		}
	}
	return findings
}

func judgeFunctions(cfg Config, values, entries map[Metric]map[string][]Entry) []Finding {
	findings := make([]Finding, 0)
	for _, metric := range FunctionMetrics() {
		paths := SortedEntryPaths(values[metric])
		for _, path := range paths {
			for _, current := range values[metric][path] {
				limit := cfg.Functions.LimitFor(metric)
				previous, listed := functionEntry(entries[metric][path], current.ID)
				findingPath := path + "#" + current.ID
				if !listed {
					if current.Value > limit {
						findings = append(findings, Finding{
							Path: findingPath, Metric: metric, Value: current.Value,
							Limit: limit, Fix: "split this function by concern",
						})
					}
					continue
				}
				if current.Value <= limit {
					findings = append(findings, Finding{
						Path: findingPath, Metric: metric, Value: current.Value,
						Limit: limit, Fix: "remove the allowlist entry after splitting or shrinking",
					})
				} else if current.Value > previous.Value {
					findings = append(findings, Finding{
						Path: findingPath, Metric: metric, Value: current.Value,
						Limit: previous.Value, Fix: "grew: split the offender instead of growing its recorded value",
					})
				}
			}
		}
	}
	return findings
}

func judgeBaseEntries(allow, base Allowlist) []Finding {
	if !base.Present {
		return nil
	}
	findings := []Finding{}
	for _, group := range []struct {
		metric     Metric
		head, base map[string]Entry
	}{{MetricFiles, allow.Files, base.Files}, {MetricDirs, allow.Dirs, base.Dirs}} {
		for path, entry := range group.head {
			previous, exists := group.base[path]
			if !exists || entry.Value > previous.Value {
				findings = append(findings, Finding{
					Path: DisplayPath(group.metric, path), Metric: group.metric,
					Value: entry.Value, Limit: previous.Value,
					Fix: "do not add or raise allowlist entries in pull requests",
				})
			}
		}
	}
	for metric, paths := range allow.Functions {
		for path, entries := range paths {
			for _, entry := range entries {
				previous, exists := functionEntry(base.Functions[metric][path], entry.ID)
				if !exists || entry.Value > previous.Value {
					findings = append(findings, Finding{
						Path: path + "#" + entry.ID, Metric: metric,
						Value: entry.Value, Limit: previous.Value,
						Fix: "do not add or raise allowlist entries in pull requests",
					})
				}
			}
		}
	}
	return findings
}

func functionEntry(entries []Entry, id string) (Entry, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return Entry{ID: "", Value: 0, Reason: ""}, false
}
func functionExists(entries []Entry, id string) bool {
	_, exists := functionEntry(entries, id)
	return exists
}

// LimitFor returns the limit for a path and metric.
func (cfg Config) LimitFor(metric Metric, path string) int {
	if metric != MetricDirs {
		return cfg.FileLimit
	}
	limit := 0
	for pattern, entry := range cfg.DirLimits {
		if matchGlob(pattern, path) && (limit == 0 || entry.Limit < limit) {
			limit = entry.Limit
		}
	}
	if limit == 0 {
		return cfg.DirLimit
	}
	return limit
}

func fixForPath(cfg Config, metric Metric, path string) string {
	if metric == MetricDirs && cfg.LimitFor(metric, path) != cfg.DirLimit {
		return "reorganize the record's decisions"
	}
	return fixFor(metric)
}

// SortedEntryPaths returns sorted path map keys.
func SortedEntryPaths[V any](entries map[string]V) []string {
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// SortFindings orders findings by path and metric.
func SortFindings(findings []Finding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Metric < findings[j].Metric
	})
}

// DisplayPath formats a path for a finding.
func DisplayPath(metric Metric, path string) string {
	if metric == MetricDirs {
		return displayDir(path)
	}
	return path
}

func displayDir(path string) string {
	if path == "" {
		return "."
	}
	return path
}

func fixFor(metric Metric) string {
	if metric == MetricDirs {
		return "split the directory by concern"
	}
	return "split this file by concern"
}
