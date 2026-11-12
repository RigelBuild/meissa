// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

// Config contains the repository's file, directory, and function thresholds.
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
type Allowlist struct {
	Files     map[string]Entry              `json:"files,omitempty"`
	Dirs      map[string]Entry              `json:"dirs,omitempty"`
	Functions map[Metric]map[string][]Entry `json:"functions,omitempty"`
	Present   bool                          `json:"-"`
}

// EmptyAllowlist creates an empty allowlist.
func EmptyAllowlist() Allowlist {
	return Allowlist{Files: map[string]Entry{}, Dirs: map[string]Entry{},
		Functions: map[Metric]map[string][]Entry{}, Present: false}
}

// Snapshot contains current measurements.
type Snapshot struct {
	Files     map[string]int
	Dirs      map[string]int
	Functions map[Metric]map[string][]Entry
}

// Finding describes one enforced size or ratchet violation.
type Finding struct {
	Path   string
	Metric Metric
	Value  int
	Limit  int
	Fix    string
}
