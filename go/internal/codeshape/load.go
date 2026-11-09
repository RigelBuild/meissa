// SPDX-License-Identifier: MIT OR Apache-2.0

// Package codeshape measures tracked source and directory sizes.
package codeshape

import (
	"embed"
	"fmt"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
)

//go:embed schema.cue
var schemaFiles embed.FS

// Metric identifies one measured code-shape dimension.
type Metric string

const (
	MetricFiles Metric = "files"
	MetricDirs  Metric = "dirs"
)

// Config contains the repository's file and directory thresholds.
type Config struct {
	FileLimit  int      `json:"fileLimit"`
	DirLimit   int      `json:"dirLimit"`
	Extensions []string `json:"extensions"`
	Exclude    []string `json:"exclude"`
}

// Entry records a measured value and its burn-down reason.
type Entry struct {
	Value  int    `json:"value"`
	Reason string `json:"reason"`
}

// FunctionEntry reserves the allowlist shape for the T2 function collectors.
type FunctionEntry struct {
	ID     string `json:"id"`
	Value  int    `json:"value"`
	Reason string `json:"reason"`
}

// Allowlist contains T1 file and directory entries plus the future function map.
// Present distinguishes a loaded base allowlist from head-only mode.
type Allowlist struct {
	Files     map[string]Entry                      `json:"files,omitempty"`
	Dirs      map[string]Entry                      `json:"dirs,omitempty"`
	Functions map[string]map[string][]FunctionEntry `json:"functions,omitempty"`
	Present   bool                                  `json:"-"`
}

// Snapshot contains current measurements. T1 leaves Functions empty.
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

func newCueContext() *cue.Context {
	return cuecontext.New()
}

func schemaValue(ctx *cue.Context) (cue.Value, error) {
	data, err := schemaFiles.ReadFile("schema.cue")
	if err != nil {
		return cue.Value{}, fmt.Errorf("read embedded schema: %w", err)
	}
	value := ctx.CompileBytes(data)
	if err := value.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("compile embedded schema: %w", err)
	}
	return value, nil
}

// Load reads and validates the repository's CUE configuration and allowlist.
func Load(root string) (Config, Allowlist, error) {
	configDir := filepath.Join(root, "tools", "code-shape-gate")
	ctx := cuecontext.New()
	schema, err := schemaValue(ctx)
	if err != nil {
		return Config{}, Allowlist{}, err
	}

	configValue, err := loadCUEFile(ctx, configDir, "config.cue")
	if err != nil {
		return Config{}, Allowlist{}, err
	}
	configSchema := schema.LookupPath(cue.MakePath(cue.Def("Config")))
	configValue = configValue.Unify(configSchema)
	if err := configValue.Err(); err != nil {
		return Config{}, Allowlist{}, fmt.Errorf("validate config.cue: %w", err)
	}
	var config Config
	if err := configValue.Decode(&config); err != nil {
		return Config{}, Allowlist{}, fmt.Errorf("decode config.cue: %w", err)
	}
	if err := validateConfig(config); err != nil {
		return Config{}, Allowlist{}, err
	}

	allowValue, err := loadCUEFile(ctx, configDir, "allowlist.cue")
	if err != nil {
		return Config{}, Allowlist{}, err
	}
	allowlist, err := decodeAllowlist(ctx, schema, allowValue, "allowlist.cue")
	if err != nil {
		return Config{}, Allowlist{}, err
	}
	allowlist.Present = true
	return config, allowlist, nil
}

func loadCUEFile(ctx *cue.Context, dir, name string) (cue.Value, error) {
	instances := load.Instances([]string{name}, &load.Config{
		Dir:     dir,
		Package: "_",
		Env:     []string{},
	})
	if len(instances) != 1 {
		return cue.Value{}, fmt.Errorf("load %s: expected one CUE instance, got %d", name, len(instances))
	}
	if err := instances[0].Err; err != nil {
		return cue.Value{}, fmt.Errorf("load %s: %w", name, err)
	}
	value := ctx.BuildInstance(instances[0])
	if err := value.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("evaluate %s: %w", name, err)
	}
	return value, nil
}

func decodeAllowlistBytes(ctx *cue.Context, schema cue.Value, data []byte, name string) (Allowlist, error) {
	value := ctx.CompileBytes(data)
	if err := value.Err(); err != nil {
		return Allowlist{}, fmt.Errorf("parse %s: %w", name, err)
	}
	return decodeAllowlist(ctx, schema, value, name)
}

func decodeAllowlist(ctx *cue.Context, schema, value cue.Value, name string) (Allowlist, error) {
	allowSchema := schema.LookupPath(cue.MakePath(cue.Def("Allowlist")))
	value = value.Unify(allowSchema)
	if err := value.Err(); err != nil {
		return Allowlist{}, fmt.Errorf("validate %s: %w", name, err)
	}
	var allowlist Allowlist
	if err := value.Decode(&allowlist); err != nil {
		return Allowlist{}, fmt.Errorf("decode %s: %w", name, err)
	}
	if allowlist.Files == nil {
		allowlist.Files = map[string]Entry{}
	}
	if allowlist.Dirs == nil {
		allowlist.Dirs = map[string]Entry{}
	}
	if allowlist.Functions == nil {
		allowlist.Functions = map[string]map[string][]FunctionEntry{}
	}
	return allowlist, nil
}

func validateConfig(config Config) error {
	if config.FileLimit != 500 || config.DirLimit != 10 {
		return fmt.Errorf("config thresholds must be fileLimit 500 and dirLimit 10")
	}
	for _, extension := range config.Extensions {
		if !strings.HasPrefix(extension, ".") || strings.ContainsAny(extension, "/\\") {
			return fmt.Errorf("invalid source extension %q", extension)
		}
	}
	for _, pattern := range config.Exclude {
		for _, segment := range strings.Split(pattern, "/") {
			if segment == "**" {
				continue
			}
			if _, err := filepath.Match(segment, ""); err != nil {
				return fmt.Errorf("invalid exclude glob %q: %w", pattern, err)
			}
		}
	}
	return nil
}
