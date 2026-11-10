// SPDX-License-Identifier: MIT OR Apache-2.0

// Package golang collects Go function measurements.
package golang

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/format"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/codeshape"
)

const highFunlenLimit = 1_000_000_000

var (
	errGoLintReport        = errors.New("reported an error")
	errGoSourceUnknown     = errors.New("unknown Go diagnostic source")
	errGoSourceNoFunctions = errors.New("go diagnostic source has no functions")
	errGoPosition          = errors.New("invalid Go diagnostic position")
	errGoOutsideFunction   = errors.New("go diagnostic outside any function")
)

type goSourceFile struct {
	set       *token.FileSet
	functions []goFunction
}

type goFunction struct {
	node ast.Node
	path string
	base string
	id   string
}

// GoMetrics measures function complexity and length in every included module.
func GoMetrics(ctx context.Context, root string, cfg codeshape.Config) (
	map[codeshape.Metric]map[string][]codeshape.Entry, []codeshape.Skip, error,
) {
	paths, err := codeshape.TrackedPaths(ctx, root)
	if err != nil {
		return nil, nil, fmt.Errorf("collect Go paths: %w", err)
	}
	modules := goModules(paths, cfg)
	if len(modules) == 0 {
		return codeshape.NewFunctionMeasurements(), nil, nil
	}
	binary, err := exec.LookPath("golangci-lint")
	if err != nil {
		return nil, nil, fmt.Errorf("find golangci-lint: %w", err)
	}
	err = codeshape.CheckToolVersion(ctx, binary, codeshape.GolangciVersion)
	if err != nil {
		return nil, nil, fmt.Errorf("check golangci-lint: %w", err)
	}
	measurements := codeshape.NewFunctionMeasurements()
	skips := []codeshape.Skip{}
	for _, module := range modules {
		files, skipped, err := parseGoSources(root, module, paths, cfg)
		if err != nil {
			return nil, nil, err
		}
		skips = append(skips, skipped...)
		if len(files) == 0 {
			continue
		}
		err = measureGoModule(ctx, binary, root, module, cfg, files, measurements)
		if err != nil {
			return nil, nil, err
		}
	}
	return measurements, skips, nil
}

func measureGoModule(ctx context.Context, binary, root, module string, cfg codeshape.Config,
	files map[string]goSourceFile, measurements map[codeshape.Metric]map[string][]codeshape.Entry,
) error {
	limits := []struct {
		metric            codeshape.Metric
		linter            string
		lines, statements int
	}{
		{metric: codeshape.MetricGoCognitive, linter: "gocognit", lines: 0, statements: 0},
		{metric: codeshape.MetricGoLines, linter: "funlen", lines: cfg.Functions.Go.Lines, statements: highFunlenLimit},
		{metric: codeshape.MetricGoStatements, linter: "funlen", lines: highFunlenLimit,
			statements: cfg.Functions.Go.Statements},
	}
	for _, file := range files {
		for _, function := range file.functions {
			for _, metric := range []codeshape.Metric{
				codeshape.MetricGoCognitive, codeshape.MetricGoLines, codeshape.MetricGoStatements,
			} {
				codeshape.AppendFunctionValue(measurements, metric, function.path, function.id, 0)
			}
		}
	}
	for _, limit := range limits {
		report, err := runGolangci(ctx, binary, root, module, limit.linter, limit.lines, limit.statements, cfg)
		if err != nil {
			return err
		}
		err = applyGoIssues(root, module, files, cfg, limit.metric, limit.linter, report, measurements)
		if err != nil {
			return err
		}
	}
	return nil
}

func applyGoIssues(root, module string, files map[string]goSourceFile, cfg codeshape.Config,
	metric codeshape.Metric, linter string, report golangciReport,
	measurements map[codeshape.Metric]map[string][]codeshape.Entry,
) error {
	for _, issue := range report.Issues {
		if issue.FromLinter != linter {
			return fmt.Errorf("golangci-lint %s reported %s issue: %s: %w", linter, issue.FromLinter, issue.Text,
				errGoLintReport)
		}
		path := issue.Pos.Filename
		if filepath.IsAbs(path) {
			var err error
			path, err = filepath.Rel(root, path)
			if err != nil {
				return fmt.Errorf("relativize Go diagnostic path: %w", err)
			}
		} else {
			path = filepath.Join(module, path)
		}
		path = filepath.ToSlash(path)
		if excludedFunctionPath(path, cfg) || !codeshape.CountedPath(path, cfg) {
			continue
		}
		value, err := metricValue(issue.Text, metric)
		if err != nil {
			return err
		}
		path, function, err := findGoFunction(root, module, files, issue.Pos.Filename, issue.Pos.Line, issue.Pos.Column)
		if err != nil {
			return err
		}
		codeshape.AppendFunctionValue(measurements, metric, path, function, value)
	}
	return nil
}

func goModules(paths []string, cfg codeshape.Config) []string {
	modules := make([]string, 0)
	for _, path := range paths {
		if filepath.Base(path) != "go.mod" || excludedFunctionPath(path, cfg) {
			continue
		}
		modules = append(modules, filepath.ToSlash(filepath.Dir(path)))
	}
	sort.Strings(modules)
	return modules
}

func excludedFunctionPath(path string, cfg codeshape.Config) bool {
	for segment := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		if segment == "testdata" || segment == "fixtures" {
			return true
		}
	}
	return codeshape.MatchesAny(filepath.ToSlash(path), cfg.Exclude)
}

func parseGoSources(root, module string, paths []string, cfg codeshape.Config) (
	map[string]goSourceFile, []codeshape.Skip, error,
) {
	files := make(map[string]goSourceFile)
	skips := []codeshape.Skip{}
	buildContext := build.Default
	buildContext.CgoEnabled = false
	modules := make([]string, 0)
	for _, path := range paths {
		if filepath.Base(path) == "go.mod" {
			modules = append(modules, filepath.ToSlash(filepath.Dir(path)))
		}
	}
	sort.Strings(modules)
	for _, path := range paths {
		if !eligibleGoSource(path, module, modules, cfg) {
			continue
		}
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		matches, err := buildContext.MatchFile(filepath.Dir(fullPath), filepath.Base(fullPath))
		if err != nil {
			return nil, nil, fmt.Errorf("check Go build constraints %q: %w", path, err)
		}
		if !matches {
			skips = append(skips, codeshape.Skip{Path: path, Reason: "not in default Go build"})
			continue
		}
		source, err := codeshape.ReadRepositoryFile(root, filepath.FromSlash(path))
		if err != nil {
			return nil, nil, fmt.Errorf("read Go source %q: %w", path, err)
		}
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, source, parser.ParseComments|parser.AllErrors)
		if err != nil {
			return nil, nil, fmt.Errorf("parse Go source %q: %w", path, err)
		}
		functions := goFunctions(file, set)
		for index := range functions {
			functions[index].path = path
		}
		files[path] = goSourceFile{set: set, functions: functions}
	}
	return files, skips, nil
}

func eligibleGoSource(path, module string, modules []string, cfg codeshape.Config) bool {
	if filepath.Ext(path) != ".go" || excludedFunctionPath(path, cfg) {
		return false
	}
	if module != "." && !strings.HasPrefix(filepath.ToSlash(path), strings.TrimSuffix(module, "/")+"/") {
		return false
	}
	return !nestedModulePath(path, module, modules) && codeshape.CountedPath(filepath.ToSlash(path), cfg)
}

func nestedModulePath(path, module string, modules []string) bool {
	for _, dir := range modules {
		if dir != "." && dir != module && (module == "." || strings.HasPrefix(dir, module+"/")) &&
			strings.HasPrefix(filepath.ToSlash(path), dir+"/") {
			return true
		}
	}
	return false
}

func goFunctions(file *ast.File, set *token.FileSet) []goFunction {
	functions := make([]goFunction, 0)
	topLevel := topLevelLiterals(file)
	ast.Inspect(file, func(node ast.Node) bool {
		switch function := node.(type) {
		case *ast.FuncDecl:
			name := function.Name.Name
			if function.Recv != nil && len(function.Recv.List) > 0 {
				var receiver bytes.Buffer
				err := format.Node(&receiver, set, function.Recv.List[0].Type)
				if err == nil {
					name = "(" + receiver.String() + ")." + name
				}
			}
			functions = append(functions, goFunction{node: function, path: "", base: name, id: ""})
		case *ast.FuncLit:
			functions = append(functions, goFunction{node: function, path: "", base: topLevel[function], id: ""})
		}
		return true
	})
	sort.Slice(functions, func(i, j int) bool { return functions[i].node.Pos() < functions[j].node.Pos() })
	assignGoFunctionIDs(functions, goFunctionParents(functions))
	return functions
}

func topLevelLiterals(file *ast.File) map[*ast.FuncLit]string {
	topLevel := make(map[*ast.FuncLit]string)
	for _, decl := range file.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range group.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, expr := range value.Values {
				if literal, ok := expr.(*ast.FuncLit); ok && i < len(value.Names) {
					topLevel[literal] = value.Names[i].Name
				}
			}
		}
	}
	return topLevel
}

func goFunctionParents(functions []goFunction) []int {
	parent := make([]int, len(functions))
	for index := range parent {
		parent[index] = -1
	}
	for index, function := range functions {
		if function.base != "" {
			continue
		}
		parentSpan := token.Pos(0)
		for candidate, other := range functions {
			if candidate == index || other.node.Pos() > function.node.Pos() || other.node.End() < function.node.End() {
				continue
			}
			span := other.node.End() - other.node.Pos()
			if parent[index] == -1 || span < parentSpan {
				parent[index] = candidate
				parentSpan = span
			}
		}
	}
	return parent
}

func assignGoFunctionIDs(functions []goFunction, parent []int) {
	closureCounts := make(map[int]int)
	duplicates := make(map[string]int)
	totals := make(map[string]int)
	for _, function := range functions {
		if function.base != "" {
			totals[function.base]++
		}
	}
	for index := range functions {
		if functions[index].base != "" {
			name := functions[index].base
			if totals[name] > 1 {
				duplicates[name]++
				name += "#" + strconv.Itoa(duplicates[name])
			}
			functions[index].id = name
			continue
		}
		owner := parent[index]
		closureCounts[owner]++
		name := "closure-" + strconv.Itoa(closureCounts[owner])
		if owner >= 0 {
			functions[index].id = functions[owner].id + "#" + name
		} else {
			functions[index].id = name
		}
	}
}

func findGoFunction(
	root, module string, files map[string]goSourceFile, reportedPath string, line, column int,
) (string, string, error) {
	path := filepath.ToSlash(reportedPath)
	if filepath.IsAbs(reportedPath) {
		relative, err := filepath.Rel(root, reportedPath)
		if err != nil {
			return "", "", fmt.Errorf("relativize Go diagnostic path: %w", err)
		}
		path = filepath.ToSlash(relative)
	} else if module != "." {
		candidate := filepath.ToSlash(filepath.Join(module, path))
		if _, exists := files[candidate]; exists {
			path = candidate
		}
	}
	source, exists := files[path]
	if !exists {
		return "", "", fmt.Errorf("go diagnostic references unknown source %q: %w", reportedPath, errGoSourceUnknown)
	}
	if len(source.functions) == 0 {
		return "", "", fmt.Errorf("go diagnostic references %q, which has no functions: %w", path, errGoSourceNoFunctions)
	}
	tokenFile := source.set.File(source.functions[0].node.Pos())
	if line < 1 || line > tokenFile.LineCount() || column < 1 {
		return "", "", fmt.Errorf("go diagnostic has invalid position %s:%d:%d: %w", path, line, column, errGoPosition)
	}
	position := tokenFile.LineStart(line) + token.Pos(column-1)
	if position > token.Pos(tokenFile.Base()+tokenFile.Size()) {
		return "", "", fmt.Errorf("go diagnostic has invalid position %s:%d:%d: %w", path, line, column, errGoPosition)
	}
	var match *goFunction
	var smallest token.Pos
	for index := range source.functions {
		function := &source.functions[index]
		if function.node.Pos() <= position && position <= function.node.End() {
			span := function.node.End() - function.node.Pos()
			if match == nil || span < smallest {
				match = function
				smallest = span
			}
		}
	}
	if match == nil {
		return "", "", fmt.Errorf("go diagnostic at %s:%d:%d is outside any function: %w",
			path, line, column, errGoOutsideFunction)
	}
	return path, match.id, nil
}
