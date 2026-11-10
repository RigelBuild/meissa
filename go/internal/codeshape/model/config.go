// SPDX-License-Identifier: MIT OR Apache-2.0

package model

import (
    "path/filepath"
    "strings"
)
// FunctionLimits contains per-language function thresholds.
type FunctionLimits struct {
	Go GoFunctionLimits `json:"go"`
	TS TSFunctionLimits `json:"ts"`
}

// GoFunctionLimits contains Go function thresholds.
type GoFunctionLimits struct {
	Cognitive int `json:"cognitive"`
	Lines int `json:"lines"`
	Statements int `json:"statements"`
}

// TSFunctionLimits contains TypeScript function thresholds.
type TSFunctionLimits struct {
	Cognitive int `json:"cognitive"`
	Lines int `json:"lines"`
}

// LimitFor returns a threshold for a function metric.
func (limits FunctionLimits) LimitFor(metric Metric) int {
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
	default:
		return 0
	}
}
// Config contains file, directory, exclusion, and function thresholds.
type Config struct {
	FileLimit  int                 `json:"fileLimit"`
	DirLimit   int                 `json:"dirLimit"`
	Extensions []string            `json:"extensions"`
	Exclude    []string            `json:"exclude"`
	DirLimits  map[string]DirLimit `json:"dirLimits"`
	Functions  FunctionLimits      `json:"functions"`
}

// DirLimit records a directory width exception and its reason.
type DirLimit struct {
	Limit  int    `json:"limit"`
	Reason string `json:"reason"`
}

// Entry records a measured value and its burn-down reason.
type Entry struct {
	ID     string `json:"id,omitempty"`
	Value  int    `json:"value"`
	Reason string `json:"reason"`
}

// Allowlist contains file, directory, and function ratchet entries.
// Present distinguishes a loaded base allowlist from head-only mode.
type Allowlist struct {
	Files     map[string]Entry     `json:"files,omitempty"`
	Dirs      map[string]Entry     `json:"dirs,omitempty"`
	Functions FunctionMeasurements `json:"functions,omitempty"`
	Present   bool                 `json:"-"`
}

// Snapshot contains current measurements.
type Snapshot struct {
	Files     map[string]int
	Dirs      map[string]int
	Functions FunctionMeasurements
}

// Finding describes one enforced size or ratchet violation.
type Finding struct {
	Path   string
	Metric Metric
	Value  int
	Limit  int
	Fix    string
}

// Skip records a source file that could not be measured.
type Skip struct {
	Path   string
	Reason string
}
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

func matchGlob(pattern, path string) bool {
    patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
    pathParts := strings.Split(strings.Trim(path, "/"), "/")
    return matchGlobParts(patternParts, pathParts)
}

func matchGlobParts(pattern, path []string) bool {
    if len(pattern) == 0 {
        return len(path) == 0
    }
    if pattern[0] == "**" {
        return matchGlobParts(pattern[1:], path) || len(path) > 0 && matchGlobParts(pattern, path[1:])
    }
    if len(path) == 0 {
        return false
    }
    matched, err := filepath.Match(pattern[0], path[0])
    return err == nil && matched && matchGlobParts(pattern[1:], path[1:])
}
// StandardConfig returns default gate thresholds for tools and tests.
func StandardConfig() Config {
    return Config{
        FileLimit: 500,
        DirLimit: 10,
        Extensions: []string{".go", ".cue"},
        Exclude: []string{},
        DirLimits: map[string]DirLimit{},
        Functions: FunctionLimits{
            Go: GoFunctionLimits{Cognitive: 20, Lines: 60, Statements: 40},
            TS: TSFunctionLimits{Cognitive: 15, Lines: 50},
        },
    }
}
