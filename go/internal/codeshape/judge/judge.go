// SPDX-License-Identifier: MIT OR Apache-2.0

package judge

import (
	"sort"

	"github.com/RigelBuild/meissa/go/internal/codeshape/inventory"
	"github.com/RigelBuild/meissa/go/internal/codeshape/model"
)

// Judge compares current measurements with thresholds and allowlists.
func Judge(cfg model.Config, allow, baseAllow model.Allowlist, snap model.Snapshot) []model.Finding {
	findings := append(judgeMetric(cfg, snap.Files, allow.Files, model.MetricFiles),
		judgeMetric(cfg, snap.Dirs, allow.Dirs, model.MetricDirs)...)
	findings = append(findings, judgeFunctions(cfg, snap.Functions, allow.Functions)...)
	findings = append(findings, judgeBaseEntries(allow, baseAllow)...)
	findings = append(findings, staleEntries(cfg, allow, snap)...)
	sortFindings(findings)
	return findings
}

func staleEntries(cfg model.Config, allow model.Allowlist, snap model.Snapshot) []model.Finding {
	var findings []model.Finding
	for path := range allow.Files {
		if _, exists := snap.Files[path]; !exists {
			findings = append(findings, model.Finding{Path: path, Metric: model.MetricFiles,
				Limit: cfg.FileLimit, Fix: "remove the stale allowlist entry"})
		}
	}
	for path := range allow.Dirs {
		if _, exists := snap.Dirs[path]; !exists {
			findings = append(findings, model.Finding{Path: displayDir(path), Metric: model.MetricDirs,
				Limit: cfgLimit(cfg, model.MetricDirs, path), Fix: "remove the stale allowlist entry"})
		}
	}
	for metric, paths := range allow.Functions {
		for path, entries := range paths {
			for _, entry := range entries {
				if _, exists := model.FunctionEntry(snap.Functions[metric][path], entry.ID); !exists {
					findings = append(findings, model.Finding{Path: path + "#" + entry.ID, Metric: metric,
						Limit: cfg.Functions.LimitFor(metric), Fix: "remove the stale allowlist entry"})
				}
			}
		}
	}
	return findings
}

func judgeMetric(cfg model.Config, values map[string]int, entries map[string]model.Entry, metric model.Metric) []model.Finding {
	paths := sortedPaths(values)
	var findings []model.Finding
	for _, path := range paths {
		value, limit := values[path], cfgLimit(cfg, metric, path)
		entry, listed := entries[path]
		if !listed && value > limit {
			findings = append(findings, model.Finding{Path: displayPath(metric, path), Metric: metric,
				Value: value, Limit: limit, Fix: fixForPath(cfg, metric, path)})
			continue
		}
		if !listed {
			continue
		}
		if value <= limit {
			findings = append(findings, model.Finding{Path: displayPath(metric, path), Metric: metric,
				Value: value, Limit: limit, Fix: "remove the allowlist entry after splitting or shrinking"})
		} else if value > entry.Value {
			findings = append(findings, model.Finding{Path: displayPath(metric, path), Metric: metric,
				Value: value, Limit: entry.Value, Fix: "grew: split the offender instead of growing its recorded value"})
		}
	}
	return findings
}

func judgeFunctions(cfg model.Config, values, entries model.FunctionMeasurements) []model.Finding {
	var findings []model.Finding
	for _, metric := range model.FunctionMetrics() {
		for _, path := range sortedPaths(values[metric]) {
			for _, current := range values[metric][path] {
				previous, listed := model.FunctionEntry(entries[metric][path], current.ID)
				findings = append(findings, judgeFunction(cfg, metric, path, current, previous, listed)...)
			}
		}
	}
	return findings
}

func judgeFunction(cfg model.Config, metric model.Metric, path string, current, previous model.Entry, listed bool) []model.Finding {
	limit, findingPath := cfg.Functions.LimitFor(metric), path+"#"+current.ID
	if !listed && current.Value > limit {
		return []model.Finding{{Path: findingPath, Metric: metric, Value: current.Value, Limit: limit,
			Fix: "split this function by concern"}}
	}
	if !listed {
		return nil
	}
	if current.Value <= limit {
		return []model.Finding{{Path: findingPath, Metric: metric, Value: current.Value, Limit: limit,
			Fix: "remove the allowlist entry after splitting or shrinking"}}
	}
	if current.Value > previous.Value {
		return []model.Finding{{Path: findingPath, Metric: metric, Value: current.Value, Limit: previous.Value,
			Fix: "grew: split the offender instead of growing its recorded value"}}
	}
	return nil
}

func judgeBaseEntries(allow, base model.Allowlist) []model.Finding {
	if !base.Present {
		return nil
	}
	var findings []model.Finding
	for _, group := range []struct {
		metric model.Metric
		head   map[string]model.Entry
		base   map[string]model.Entry
	}{{model.MetricFiles, allow.Files, base.Files}, {model.MetricDirs, allow.Dirs, base.Dirs}} {
		for path, entry := range group.head {
			previous, exists := group.base[path]
			if !exists || entry.Value > previous.Value {
				findings = append(findings, model.Finding{Path: displayPath(group.metric, path), Metric: group.metric,
					Value: entry.Value, Limit: previous.Value, Fix: "do not add or raise allowlist entries in pull requests"})
			}
		}
	}
	for metric, paths := range allow.Functions {
		for path, entries := range paths {
			for _, entry := range entries {
				previous, exists := model.FunctionEntry(base.Functions[metric][path], entry.ID)
				if !exists || entry.Value > previous.Value {
					findings = append(findings, model.Finding{Path: path + "#" + entry.ID, Metric: metric,
						Value: entry.Value, Limit: previous.Value, Fix: "do not add or raise allowlist entries in pull requests"})
				}
			}
		}
	}
	return findings
}

func cfgLimit(cfg model.Config, metric model.Metric, path string) int {
	if metric != model.MetricDirs {
		return cfg.FileLimit
	}
	limit := 0
	for pattern, entry := range cfg.DirLimits {
		if inventory.MatchGlob(pattern, path) && (limit == 0 || entry.Limit < limit) {
			limit = entry.Limit
		}
	}
	if limit == 0 {
		return cfg.DirLimit
	}
	return limit
}

func fixForPath(cfg model.Config, metric model.Metric, path string) string {
	if metric == model.MetricDirs && cfgLimit(cfg, metric, path) != cfg.DirLimit {
		return "reorganize the record's decisions"
	}
	if metric == model.MetricDirs {
		return "split the directory by concern"
	}
	return "split this file by concern"
}

func displayPath(metric model.Metric, path string) string {
	if metric == model.MetricDirs {
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

func sortedPaths[V any](entries map[string]V) []string {
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func sortFindings(findings []model.Finding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Metric < findings[j].Metric
	})
}
