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
				Limit:  cfg.limitFor(MetricDirs, path),
				Fix:    "remove the stale allowlist entry",
			})
		}
	}
	sortFindings(findings)
	return findings
}

func judgeMetric(cfg Config, values map[string]int, entries map[string]Entry, metric Metric) []Finding {
	paths := sortedEntryPaths(values)
	findings := make([]Finding, 0)
	for _, path := range paths {
		value := values[path]
		limit := cfg.limitFor(metric, path)
		entry, listed := entries[path]
		if !listed {
			if value > limit {
				findings = append(findings, Finding{
					Path:   displayPath(metric, path),
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

func (cfg Config) limitFor(metric Metric, path string) int {
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
	if metric == MetricDirs && cfg.limitFor(metric, path) != cfg.DirLimit {
		return "reorganize the record's decisions"
	}
	return fixFor(metric)
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
	return findings
}

func sortedEntryPaths[V any](entries map[string]V) []string {
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
