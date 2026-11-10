// SPDX-License-Identifier: MIT OR Apache-2.0

// Package codeshape measures tracked source and directory sizes.
package codeshape

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/parser"
)

var (
	errInvalidThresholds = errors.New("config thresholds must be fileLimit 500 and dirLimit 10")
	errInvalidExtension  = errors.New("invalid source extension")
	errImportsForbidden  = errors.New("imports are not permitted")
	errDuplicateRoot     = errors.New("duplicate root directory entries")
	errUnsupportedMetric = errors.New("unsupported function metric")
	errFunctionLimits    = errors.New("function limits must be positive")
)

//go:embed schema.cue
var schemaFiles embed.FS

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
// Present distinguishes a loaded base allowlist from head-only mode.
type Allowlist struct {
	Files     map[string]Entry              `json:"files,omitempty"`
	Dirs      map[string]Entry              `json:"dirs,omitempty"`
	Functions map[Metric]map[string][]Entry `json:"functions,omitempty"`
	Present   bool                          `json:"-"`
}

func emptyAllowlist() Allowlist {
	return Allowlist{
		Files:     map[string]Entry{},
		Dirs:      map[string]Entry{},
		Functions: map[Metric]map[string][]Entry{},
		Present:   false,
	}
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

func schemaValue(ctx *cue.Context) (cue.Value, error) {
	data, err := schemaFiles.ReadFile("schema.cue")
	if err != nil {
		return cue.Value{}, fmt.Errorf("read embedded schema: %w", err)
	}
	value := ctx.CompileBytes(data)
	err = value.Err()
	if err != nil {
		return cue.Value{}, fmt.Errorf("compile embedded schema: %w", err)
	}
	return value, nil
}

// Load reads and validates the repository's CUE configuration and allowlist.
func Load(root string) (Config, Allowlist, error) {
	repository, err := os.OpenRoot(root)
	if err != nil {
		return Config{}, Allowlist{}, fmt.Errorf("open repository root: %w", err)
	}
	config, allowlist, loadErr := loadFromRoot(repository)
	closeErr := repository.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close repository root: %w", closeErr)
	}
	return config, allowlist, errors.Join(loadErr, closeErr)
}

func loadFromRoot(repository *os.Root) (Config, Allowlist, error) {
	ctx := cuecontext.New()
	schema, err := schemaValue(ctx)
	if err != nil {
		return Config{}, Allowlist{}, err
	}

	configValue, err := loadCUEFile(ctx, repository, "tools/code-shape-gate/config.cue")
	if err != nil {
		return Config{}, Allowlist{}, err
	}
	configSchema := schema.LookupPath(cue.MakePath(cue.Def("Config")))
	configValue = configValue.Unify(configSchema)
	err = configValue.Err()
	if err != nil {
		return Config{}, Allowlist{}, fmt.Errorf("validate config.cue: %w", err)
	}
	var config Config
	err = configValue.Decode(&config)
	if err != nil {
		return Config{}, Allowlist{}, fmt.Errorf("decode config.cue: %w", err)
	}
	err = validateConfig(config)
	if err != nil {
		return Config{}, Allowlist{}, err
	}

	allowValue, err := loadCUEFile(ctx, repository, "tools/code-shape-gate/allowlist.cue")
	if err != nil {
		return Config{}, Allowlist{}, err
	}
	allowlist, err := decodeAllowlist(schema, allowValue, "allowlist.cue")
	if err != nil {
		return Config{}, Allowlist{}, err
	}
	allowlist.Present = true
	return config, allowlist, nil
}

func loadCUEFile(ctx *cue.Context, repository *os.Root, name string) (cue.Value, error) {
	data, err := repository.ReadFile(name)
	if err != nil {
		return cue.Value{}, fmt.Errorf("read %s: %w", name, err)
	}
	return compileCUEFile(ctx, data, name)
}

func compileCUEFile(ctx *cue.Context, data []byte, name string) (cue.Value, error) {
	file, err := parser.ParseFile(name, data)
	if err != nil {
		return cue.Value{}, fmt.Errorf("parse %s: %w", name, err)
	}
	for _, declaration := range file.Decls {
		if _, ok := declaration.(*ast.ImportDecl); ok {
			return cue.Value{}, fmt.Errorf("%s: %w", name, errImportsForbidden)
		}
	}
	value := ctx.BuildFile(file)
	err = value.Err()
	if err != nil {
		return cue.Value{}, fmt.Errorf("evaluate %s: %w", name, err)
	}
	return value, nil
}

func decodeAllowlistBytes(ctx *cue.Context, schema cue.Value, data []byte, name string) (Allowlist, error) {
	value, err := compileCUEFile(ctx, data, name)
	if err != nil {
		return Allowlist{}, err
	}
	return decodeAllowlist(schema, value, name)
}

func decodeAllowlist(schema, value cue.Value, name string) (Allowlist, error) {
	allowSchema := schema.LookupPath(cue.MakePath(cue.Def("Allowlist")))
	value = value.Unify(allowSchema)
	err := value.Err()
	if err != nil {
		return Allowlist{}, fmt.Errorf("validate %s: %w", name, err)
	}
	var allowlist Allowlist
	err = value.Decode(&allowlist)
	if err != nil {
		return Allowlist{}, fmt.Errorf("decode %s: %w", name, err)
	}
	if allowlist.Files == nil {
		allowlist.Files = map[string]Entry{}
	}
	if allowlist.Dirs == nil {
		allowlist.Dirs = map[string]Entry{}
	}
	if allowlist.Functions == nil {
		allowlist.Functions = map[Metric]map[string][]Entry{}
	}
	for metric, paths := range allowlist.Functions {
		if !isFunctionMetric(metric) {
			return Allowlist{}, fmt.Errorf("%s: %w %q", name, errUnsupportedMetric, metric)
		}
		err := validateFunctionEntryIDs(paths)
		if err != nil {
			return Allowlist{}, fmt.Errorf("%s: %w", name, err)
		}
	}
	if entry, exists := allowlist.Dirs["."]; exists {
		if _, duplicate := allowlist.Dirs[""]; duplicate {
			return Allowlist{}, fmt.Errorf("%s: %w", name, errDuplicateRoot)
		}
		allowlist.Dirs[""] = entry
		delete(allowlist.Dirs, ".")
	}
	return allowlist, nil
}

func validateConfig(config Config) error {
	if config.FileLimit != 500 || config.DirLimit != 10 {
		return errInvalidThresholds
	}
	if config.Functions.Go.Cognitive <= 0 || config.Functions.Go.Lines <= 0 || config.Functions.Go.Statements <= 0 ||
		config.Functions.TS.Cognitive <= 0 || config.Functions.TS.Lines <= 0 {
		return errFunctionLimits
	}
	for _, extension := range config.Extensions {
		if !strings.HasPrefix(extension, ".") || strings.ContainsAny(extension, "/\\") {
			return fmt.Errorf("%w %q", errInvalidExtension, extension)
		}
	}
	for _, pattern := range config.Exclude {
		for segment := range strings.SplitSeq(pattern, "/") {
			if segment == "**" {
				continue
			}
			_, err := filepath.Match(segment, "")
			if err != nil {
				return fmt.Errorf("invalid exclude glob %q: %w", pattern, err)
			}
		}
	}
	return nil
}
