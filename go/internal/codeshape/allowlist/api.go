// SPDX-License-Identifier: MIT OR Apache-2.0

// Package allowlist loads and renders the code-shape gate configuration.
package allowlist

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

	"github.com/RigelBuild/meissa/go/internal/codeshape/model"
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

// Load reads and validates the repository's CUE configuration and allowlist.
func Load(root string) (model.Config, model.Allowlist, error) {
	repository, err := os.OpenRoot(root)
	if err != nil {
		return model.Config{}, model.Allowlist{}, fmt.Errorf("open repository root: %w", err)
	}
	config, allowlist, loadErr := loadFromRoot(repository)
	closeErr := repository.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close repository root: %w", closeErr)
	}
	return config, allowlist, errors.Join(loadErr, closeErr)
}

func loadFromRoot(repository *os.Root) (model.Config, model.Allowlist, error) {
	ctx := cuecontext.New()
	schema, err := schemaValue(ctx)
	if err != nil {
		return model.Config{}, model.Allowlist{}, err
	}
	configValue, err := loadCUEFile(ctx, repository, "tools/code-shape-gate/config.cue")
	if err != nil {
		return model.Config{}, model.Allowlist{}, err
	}
	configValue = configValue.Unify(schema.LookupPath(cue.MakePath(cue.Def("Config"))))
	if err = configValue.Err(); err != nil {
		return model.Config{}, model.Allowlist{}, fmt.Errorf("validate config.cue: %w", err)
	}
	var config model.Config
	if err = configValue.Decode(&config); err != nil {
		return model.Config{}, model.Allowlist{}, fmt.Errorf("decode config.cue: %w", err)
	}
	if config.Functions == (model.FunctionLimits{}) {
		config.Functions = model.FunctionLimits{
			Go: model.GoFunctionLimits{Cognitive: 20, Lines: 60, Statements: 40},
			TS: model.TSFunctionLimits{Cognitive: 15, Lines: 50},
		}
	}
	if err = validateConfig(config); err != nil {
		return model.Config{}, model.Allowlist{}, err
	}
	allowValue, err := loadCUEFile(ctx, repository, "tools/code-shape-gate/allowlist.cue")
	if err != nil {
		return model.Config{}, model.Allowlist{}, err
	}
	allowlist, err := decodeAllowlist(schema, allowValue, "allowlist.cue")
	if err != nil {
		return model.Config{}, model.Allowlist{}, err
	}
	allowlist.Present = true
	return config, allowlist, nil
}

func schemaValue(ctx *cue.Context) (cue.Value, error) {
	data, err := schemaFiles.ReadFile("schema.cue")
	if err != nil {
		return cue.Value{}, fmt.Errorf("read embedded schema: %w", err)
	}
	value := ctx.CompileBytes(data)
	if err = value.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("compile embedded schema: %w", err)
	}
	return value, nil
}

func loadCUEFile(ctx *cue.Context, repository *os.Root, name string) (cue.Value, error) {
	data, err := repository.ReadFile(name)
	if err != nil {
		return cue.Value{}, fmt.Errorf("read %s: %w", name, err)
	}
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
	if err = value.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("evaluate %s: %w", name, err)
	}
	return value, nil
}

func decodeAllowlist(schema, value cue.Value, name string) (model.Allowlist, error) {
	value = value.Unify(schema.LookupPath(cue.MakePath(cue.Def("Allowlist"))))
	if err := value.Err(); err != nil {
		return model.Allowlist{}, fmt.Errorf("validate %s: %w", name, err)
	}
	var allowlist model.Allowlist
	if err := value.Decode(&allowlist); err != nil {
		return model.Allowlist{}, fmt.Errorf("decode %s: %w", name, err)
	}
	if allowlist.Files == nil {
		allowlist.Files = map[string]model.Entry{}
	}
	if allowlist.Dirs == nil {
		allowlist.Dirs = map[string]model.Entry{}
	}
	if allowlist.Functions == nil {
		allowlist.Functions = model.NewFunctionMeasurements()
	}
	for metric, paths := range allowlist.Functions {
		if !model.IsFunctionMetric(metric) {
			return model.Allowlist{}, fmt.Errorf("%s: %w %q", name, errUnsupportedMetric, metric)
		}
		if err := model.ValidateFunctionEntryIDs(paths); err != nil {
			return model.Allowlist{}, fmt.Errorf("%s: %w", name, err)
		}
	}
	if entry, exists := allowlist.Dirs["."]; exists {
		if _, duplicate := allowlist.Dirs[""]; duplicate {
			return model.Allowlist{}, fmt.Errorf("%s: %w", name, errDuplicateRoot)
		}
		allowlist.Dirs[""] = entry
		delete(allowlist.Dirs, ".")
	}
	return allowlist, nil
}

func validateConfig(config model.Config) error {
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
			if _, err := filepath.Match(segment, ""); err != nil {
				return fmt.Errorf("invalid exclude glob %q: %w", pattern, err)
			}
		}
	}
	return nil
}
