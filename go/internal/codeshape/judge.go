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
	findings = append(findings, judgeBaseEntries(allow, baseAllow)...)
	for _, path := range sortedEntryPaths(allow.Files) {
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
	for _, path := range sortedEntryPaths(allow.Dirs) {
		if _, exists := snap.Dirs[path]; !exists {
			findings = append(findings, Finding{
				Path:   displayDir(path),
				Metric: MetricDirs,
				Value:  0,
				Limit:  cfg.DirLimit,
				Fix:    "remove the stale allowlist entry",
			})
		}
	}
	sortFindings(findings)
	return findings
}

func judgeMetric(cfg Config, values map[string]int, entries map[string]Entry, metric Metric) []Finding {
	limit := cfg.FileLimit
	if metric == MetricDirs {
		limit = cfg.DirLimit
	}
	paths := make([]string, 0, len(values))
	for path := range values {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	findings := make([]Finding, 0)
	for _, path := range paths {
		value := values[path]
		entry, listed := entries[path]
		if !listed {
			if value > limit {
				findings = append(findings, Finding{
					Path:   displayPath(metric, path),
					Metric: metric,
					Value:  value,
					Limit:  limit,
					Fix:    fixFor(metric),
				})
			}
			continue
		}
		if value <= limit {
			findings = append(findings, Finding{
				Path:   displayPath(metric, path),
				Metric: metric,
				Value:  value,
				Limit:  limit,
				Fix:    "remove the allowlist entry after splitting or shrinking",
			})
			continue
		}
		if value > entry.Value {
			findings = append(findings, Finding{
				Path:   displayPath(metric, path),
				Metric: metric,
				Value:  value,
				Limit:  entry.Value,
				Fix:    "grew: split the offender instead of growing its recorded value",
			})
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
					Path:   displayPath(group.metric, path),
					Metric: group.metric,
					Value:  entry.Value,
					Limit:  previous.Value,
					Fix:    "do not add or raise allowlist entries in pull requests",
				})
			}
		}
	}
	return append(findings, judgeBaseFunctions(allow, base)...)
}

func judgeBaseFunctions(allow, base Allowlist) []Finding {
	findings := []Finding{}
	for metric, paths := range allow.Functions {
		for path, entries := range paths {
			for _, entry := range entries {
				found, previous := findFunction(base.Functions[metric][path], entry.ID)
				if !found || entry.Value > previous {
					findings = append(findings, Finding{
						Path:   path + ":" + entry.ID,
						Metric: Metric(metric),
						Value:  entry.Value,
						Limit:  previous,
						Fix:    "do not add or raise allowlist entries in pull requests",
					})
				}
			}
		}
	}
	return findings
}

func findFunction(entries []FunctionEntry, id string) (bool, int) {
	for _, entry := range entries {
		if entry.ID == id {
			return true, entry.Value
		}
	}
	return false, 0
}

func sortedEntryPaths(entries map[string]Entry) []string {
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func sortFindings(findings []Finding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Metric < findings[j].Metric
	})
}

func displayPath(metric Metric, path string) string {
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
