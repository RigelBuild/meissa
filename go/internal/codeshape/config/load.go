// SPDX-License-Identifier: MIT OR Apache-2.0

// Package config loads and renders code shape configuration.
package config

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

	"github.com/RigelBuild/meissa/go/internal/codeshape"
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

// DecodeBaseAllowlist decodes an allowlist from a base revision.
func DecodeBaseAllowlist(data []byte) (codeshape.Allowlist, error) {
	ctx := cuecontext.New()
	schema, err := schemaValue(ctx)
	if err != nil {
		return codeshape.Allowlist{}, err
	}
	return decodeAllowlistBytes(ctx, schema, data, "base allowlist")
}

// Load reads and validates the repository's CUE configuration and allowlist.
func Load(root string) (codeshape.Config, codeshape.Allowlist, error) {
	repository, err := os.OpenRoot(root)
	if err != nil {
		return codeshape.Config{}, codeshape.Allowlist{}, fmt.Errorf("open repository root: %w", err)
	}
	config, allowlist, loadErr := loadFromRoot(repository)
	closeErr := repository.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close repository root: %w", closeErr)
	}
	return config, allowlist, errors.Join(loadErr, closeErr)
}

func loadFromRoot(repository *os.Root) (codeshape.Config, codeshape.Allowlist, error) {
	ctx := cuecontext.New()
	schema, err := schemaValue(ctx)
	if err != nil {
		return codeshape.Config{}, codeshape.Allowlist{}, err
	}

	configValue, err := loadCUEFile(ctx, repository, "tools/code-shape-gate/config.cue")
	if err != nil {
		return codeshape.Config{}, codeshape.Allowlist{}, err
	}
	configSchema := schema.LookupPath(cue.MakePath(cue.Def("Config")))
	configValue = configValue.Unify(configSchema)
	err = configValue.Err()
	if err != nil {
		return codeshape.Config{}, codeshape.Allowlist{}, fmt.Errorf("validate config.cue: %w", err)
	}
	var config codeshape.Config
	err = configValue.Decode(&config)
	if err != nil {
		return codeshape.Config{}, codeshape.Allowlist{}, fmt.Errorf("decode config.cue: %w", err)
	}
	if !configValue.LookupPath(cue.ParsePath("functions")).Exists() {
		config.Functions = codeshape.FunctionLimits{
			Go: codeshape.GoFunctionLimits{Cognitive: 20, Lines: 60, Statements: 40},
			TS: codeshape.TSFunctionLimits{Cognitive: 15, Lines: 50},
		}
	}
	err = validateConfig(config)
	if err != nil {
		return codeshape.Config{}, codeshape.Allowlist{}, err
	}

	allowValue, err := loadCUEFile(ctx, repository, "tools/code-shape-gate/allowlist.cue")
	if err != nil {
		return codeshape.Config{}, codeshape.Allowlist{}, err
	}
	allowlist, err := decodeAllowlist(schema, allowValue, "allowlist.cue")
	if err != nil {
		return codeshape.Config{}, codeshape.Allowlist{}, err
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

func decodeAllowlistBytes(ctx *cue.Context, schema cue.Value, data []byte, name string) (codeshape.Allowlist, error) {
	value, err := compileCUEFile(ctx, data, name)
	if err != nil {
		return codeshape.Allowlist{}, err
	}
	return decodeAllowlist(schema, value, name)
}

func decodeAllowlist(schema, value cue.Value, name string) (codeshape.Allowlist, error) {
	allowSchema := schema.LookupPath(cue.MakePath(cue.Def("Allowlist")))
	value = value.Unify(allowSchema)
	err := value.Err()
	if err != nil {
		return codeshape.Allowlist{}, fmt.Errorf("validate %s: %w", name, err)
	}
	var allowlist codeshape.Allowlist
	err = value.Decode(&allowlist)
	if err != nil {
		return codeshape.Allowlist{}, fmt.Errorf("decode %s: %w", name, err)
	}
	if allowlist.Files == nil {
		allowlist.Files = map[string]codeshape.Entry{}
	}
	if allowlist.Dirs == nil {
		allowlist.Dirs = map[string]codeshape.Entry{}
	}
	if allowlist.Functions == nil {
		allowlist.Functions = map[codeshape.Metric]map[string][]codeshape.Entry{}
	}
	for metric, paths := range allowlist.Functions {
		if !codeshape.IsFunctionMetric(metric) {
			return codeshape.Allowlist{}, fmt.Errorf("%s: %w %q", name, errUnsupportedMetric, metric)
		}
		err := codeshape.ValidateFunctionEntryIDs(paths)
		if err != nil {
			return codeshape.Allowlist{}, fmt.Errorf("%s: %w", name, err)
		}
	}
	if entry, exists := allowlist.Dirs["."]; exists {
		if _, duplicate := allowlist.Dirs[""]; duplicate {
			return codeshape.Allowlist{}, fmt.Errorf("%s: %w", name, errDuplicateRoot)
		}
		allowlist.Dirs[""] = entry
		delete(allowlist.Dirs, ".")
	}
	return allowlist, nil
}

func validateConfig(config codeshape.Config) error {
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
