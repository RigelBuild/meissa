// SPDX-License-Identifier: MIT OR Apache-2.0

package golang

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/codeshape/inventory"
	"github.com/RigelBuild/meissa/go/internal/codeshape/model"
)

const highFunlenLimit = 1_000_000_000

var (
	gocognitValue          = regexp.MustCompile(`cognitive complexity ([0-9]+) of func`)
	funlenValue            = regexp.MustCompile(`\(([0-9]+) > [0-9]+\)`)
	errGoLintReport        = errors.New("reported an error")
	errGoLintMissing       = errors.New("measurement omitted")
	errGoMetricParse       = errors.New("cannot parse value")
	errGoMetricInvalid     = errors.New("invalid value")
	errGoSourceUnknown     = errors.New("unknown Go diagnostic source")
	errGoSourceNoFunctions = errors.New("go diagnostic source has no functions")
	errGoPosition          = errors.New("invalid Go diagnostic position")
	errGoOutsideFunction   = errors.New("go diagnostic outside any function")
)

//nolint:tagliatelle // external wire format
type golangciReport struct {
	Issues []struct {
		FromLinter string `json:"FromLinter"`
		Text       string `json:"Text"`
		Pos        struct {
			Filename string `json:"Filename"`
			Line     int    `json:"Line"`
			Column   int    `json:"Column"`
		} `json:"Pos"`
	} `json:"Issues"`
	Report struct {
		Error   string `json:"Error"`
		Linters []struct {
			Name    string `json:"Name"`
			Enabled bool   `json:"Enabled"`
		} `json:"Linters"`
	} `json:"Report"`
}

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

type goLimits struct {
	metric     model.Metric
	linter     string
	lines      int
	statements int
}

// Metrics measures functions in every included Go module.
func Metrics(ctx context.Context, root string, paths []string, cfg model.Config, repository *os.Root) (model.FunctionMeasurements, []model.Skip, error) {
	modules := goModules(paths, cfg)
	if len(modules) == 0 {
		return model.NewFunctionMeasurements(), nil, nil
	}
	binary, err := exec.LookPath("golangci-lint")
	if err != nil {
		return nil, nil, fmt.Errorf("find golangci-lint: %w", err)
	}
	measurements := model.NewFunctionMeasurements()
	var skips []model.Skip
	for _, module := range modules {
		files, skipped, err := parseGoSources(root, module, paths, cfg, repository)
		if err != nil {
			return nil, nil, err
		}
		skips = append(skips, skipped...)
		if len(files) == 0 {
			continue
		}
		if err := initializeMeasurements(measurements, files); err != nil {
			return nil, nil, err
		}
		for _, limits := range goLimits(cfg) {
			if err := measureModule(ctx, root, module, binary, cfg, files, measurements, limits); err != nil {
				return nil, nil, err
			}
		}
	}
	return measurements, skips, nil
}

func initializeMeasurements(measurements model.FunctionMeasurements, files map[string]goSourceFile) error {
	for _, file := range files {
		for _, function := range file.functions {
			for _, metric := range []model.Metric{model.MetricGoCognitive, model.MetricGoLines, model.MetricGoStatements} {
				if err := model.AppendFunctionValue(measurements, metric, function.path, function.id, 0); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func goLimits(cfg model.Config) []goLimits {
	return []goLimits{
		{metric: model.MetricGoCognitive, linter: "gocognit"},
		{metric: model.MetricGoLines, linter: "funlen", lines: cfg.Functions.Go.Lines, statements: highFunlenLimit},
		{metric: model.MetricGoStatements, linter: "funlen", lines: highFunlenLimit, statements: cfg.Functions.Go.Statements},
	}
}

func measureModule(ctx context.Context, root, module, binary string, cfg model.Config,
	files map[string]goSourceFile, measurements model.FunctionMeasurements, limits goLimits) error {
	report, err := runGolangci(ctx, binary, root, module, limits.linter, limits.lines, limits.statements, cfg)
	if err != nil {
		return err
	}
	for _, issue := range report.Issues {
		if issue.FromLinter != limits.linter {
			return fmt.Errorf("golangci-lint typecheck issue in %q: %s", issue.Pos.Filename, issue.Text)
		}
		path := resolveReportedPath(root, module, issue.Pos.Filename)
		if !goSourceIncluded(path, cfg) {
			continue
		}
		value, err := metricValue(issue.Text, limits.metric)
		if err != nil {
			return err
		}
		path, function, err := findGoFunction(root, module, files, issue.Pos.Filename, issue.Pos.Line, issue.Pos.Column)
		if errors.Is(err, errGoSourceUnknown) && !includedSourcePath(path, files, cfg) {
			continue
		}
		if err != nil {
			return err
		}
		if err := model.SetFunctionDiagnostic(measurements, limits.metric, path, function, value); err != nil {
			return err
		}
	}
	return nil
}

func resolveReportedPath(root, module, reported string) string {
	if filepath.IsAbs(reported) {
		relative, err := filepath.Rel(root, reported)
		if err == nil {
			return filepath.ToSlash(relative)
		}
		return filepath.ToSlash(reported)
	}
	path := filepath.ToSlash(reported)
	if module != "." {
		return filepath.ToSlash(filepath.Join(module, path))
	}
	return path
}

func includedSourcePath(path string, files map[string]goSourceFile, cfg model.Config) bool {
	return goSourceIncluded(path, cfg) && files[path].set != nil
}

func trackedPaths(ctx context.Context, root string) ([]string, error) {
	command := exec.CommandContext(ctx, "git", "ls-files", "-z")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list tracked files: %w", err)
	}
	paths := strings.Split(string(output), "\x00")
	if len(paths) > 0 && paths[len(paths)-1] == "" {
		paths = paths[:len(paths)-1]
	}
	return paths, nil
}

func goModules(paths []string, cfg model.Config) []string {
	modules := make([]string, 0)
	for _, path := range paths {
		if filepath.Base(path) == "go.mod" && !excludedFunctionPath(path, cfg) {
			modules = append(modules, filepath.ToSlash(filepath.Dir(path)))
		}
	}
	sort.Strings(modules)
	return modules
}

func excludedFunctionPath(path string, cfg model.Config) bool {
	for segment := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		if segment == "testdata" || segment == "fixtures" {
			return true
		}
	}
	return inventory.MatchesAny(filepath.ToSlash(path), cfg.Exclude)
}

func parseGoSources(root, module string, paths []string, cfg model.Config, repository *os.Root) (map[string]goSourceFile, []model.Skip, error) {
	files := make(map[string]goSourceFile)
	var skips []model.Skip
	modules := goModules(paths, cfg)
	for _, path := range paths {
		if !goSourceIncluded(path, cfg) || !belongsToModule(path, module, modules) {
			continue
		}
		buildContext := build.Default
		buildContext.CgoEnabled = false
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		matches, err := buildContext.MatchFile(filepath.Dir(fullPath), filepath.Base(fullPath))
		if err != nil {
			return nil, nil, fmt.Errorf("check Go build constraints %q: %w", path, err)
		}
		if !matches {
			skips = append(skips, model.Skip{Path: path, Reason: "not in default Go build"})
			continue
		}
		source, err := repository.ReadFile(filepath.FromSlash(path))
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

func belongsToModule(path, module string, modules []string) bool {
	slashPath := filepath.ToSlash(path)
	if module != "." && !strings.HasPrefix(slashPath, strings.TrimSuffix(module, "/")+"/") {
		return false
	}
	for _, dir := range modules {
		if dir != "." && dir != module && (module == "." || strings.HasPrefix(dir, module+"/")) &&
			strings.HasPrefix(slashPath, dir+"/") {
			return false
		}
	}
	return true
}

func goSourceIncluded(path string, cfg model.Config) bool {
	return filepath.Ext(path) == ".go" && !excludedFunctionPath(path, cfg) && inventory.CountedPath(filepath.ToSlash(path), cfg)
}

func goFunctions(file *ast.File, set *token.FileSet) []goFunction {
	functions := collectGoFunctions(file, set)
	sort.Slice(functions, func(i, j int) bool { return functions[i].node.Pos() < functions[j].node.Pos() })
	parents := goFunctionParents(functions)
	assignGoFunctionIDs(functions, parents)
	return functions
}

func collectGoFunctions(file *ast.File, set *token.FileSet) []goFunction {
	topLevel := topLevelFunctionNames(file)
	var functions []goFunction
	ast.Inspect(file, func(node ast.Node) bool {
		switch function := node.(type) {
		case *ast.FuncDecl:
			functions = append(functions, goFunction{node: function, base: functionName(function, set)})
		case *ast.FuncLit:
			functions = append(functions, goFunction{node: function, base: topLevel[function]})
		}
		return true
	})
	return functions
}

func topLevelFunctionNames(file *ast.File) map[*ast.FuncLit]string {
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
			for index, expr := range value.Values {
				if literal, ok := expr.(*ast.FuncLit); ok && index < len(value.Names) {
					topLevel[literal] = value.Names[index].Name
				}
			}
		}
	}
	return topLevel
}

func functionName(function *ast.FuncDecl, set *token.FileSet) string {
	name := function.Name.Name
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return name
	}
	var receiver bytes.Buffer
	if err := format.Node(&receiver, set, function.Recv.List[0].Type); err != nil {
		return name
	}
	return "(" + receiver.String() + ")." + name
}

func goFunctionParents(functions []goFunction) []int {
	parents := make([]int, len(functions))
	for index := range parents {
		parents[index] = -1
	}
	for index, function := range functions {
		if function.base != "" {
			continue
		}
		for candidate, other := range functions {
			if candidate == index || other.node.Pos() > function.node.Pos() || other.node.End() < function.node.End() {
				continue
			}
			if parents[index] < 0 || other.node.End()-other.node.Pos() < functions[parents[index]].node.End()-functions[parents[index]].node.Pos() {
				parents[index] = candidate
			}
		}
	}
	return parents
}

func assignGoFunctionIDs(functions []goFunction, parents []int) {
	closureCounts, duplicates, totals := map[int]int{}, map[string]int{}, map[string]int{}
	for _, function := range functions {
		if function.base != "" {
			totals[function.base]++
		}
	}
	for index := range functions {
		if functions[index].base != "" {
			functions[index].id = numberedGoName(functions[index].base, totals, duplicates)
			continue
		}
		owner := parents[index]
		closureCounts[owner]++
		name := "closure-" + strconv.Itoa(closureCounts[owner])
		if owner >= 0 {
			functions[index].id = functions[owner].id + "#" + name
		} else {
			functions[index].id = name
		}
	}
}

func numberedGoName(name string, totals, duplicates map[string]int) string {
	if totals[name] <= 1 {
		return name
	}
	duplicates[name]++
	return name + "#" + strconv.Itoa(duplicates[name])
}

func runGolangci(ctx context.Context, binary, root, module, linter string, lines, statements int, cfg model.Config) (golangciReport, error) {
	configuration := fmt.Sprintf("version: '2'\nlinters:\n  default: none\n  enable:\n    - %s\n"+
		"  settings:\n    gocognit:\n      min-complexity: %d\n    funlen:\n"+
		"      lines: %d\n      statements: %d\n      ignore-comments: true\n"+
		"issues:\n  max-issues-per-linter: 0\n  max-same-issues: 0\n",
		linter, cfg.Functions.Go.Cognitive, lines, statements)
	temporary, err := os.CreateTemp("", "code-shape-golangci-*.yml")
	if err != nil {
		return golangciReport{}, fmt.Errorf("create golangci-lint config: %w", err)
	}
	report, runErr := runGolangciWithConfig(ctx, binary, root, module, linter, configuration, temporary)
	removeErr := os.Remove(temporary.Name())
	if removeErr != nil {
		removeErr = fmt.Errorf("remove golangci-lint config: %w", removeErr)
	}
	return report, errors.Join(runErr, removeErr)
}

func runGolangciWithConfig(ctx context.Context, binary, root, module, linter, configuration string, temporary *os.File) (golangciReport, error) {
	if _, err := temporary.WriteString(configuration); err != nil {
		closeErr := temporary.Close()
		return golangciReport{}, fmt.Errorf("write golangci-lint config: %w", errors.Join(err, closeErr))
	}
	if err := temporary.Close(); err != nil {
		return golangciReport{}, fmt.Errorf("close golangci-lint config: %w", err)
	}
	//nolint:gosec // G204: execute the configured golangci-lint binary
	command := exec.CommandContext(ctx, binary, "run", "--config", temporary.Name(), "--enable-only", linter,
		"--output.json.path", "stdout", "--path-mode", "abs", "--show-stats=false", "--uniq-by-line=false",
		"--max-issues-per-linter", "0", "--max-same-issues", "0", "./...")
	command.Dir = filepath.Join(root, filepath.FromSlash(module))
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	runErr := command.Run()
	if runErr != nil {
		var exitError *exec.ExitError
		if !errors.As(runErr, &exitError) || exitError.ExitCode() != 1 {
			return golangciReport{}, fmt.Errorf("golangci-lint %s failed in %q: %w: %s", linter, module, runErr, strings.TrimSpace(stderr.String()))
		}
	}
	var report golangciReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		return golangciReport{}, fmt.Errorf("parse golangci-lint %s output in %q: %w", linter, module, err)
	}
	if report.Report.Error != "" {
		return golangciReport{}, fmt.Errorf("golangci-lint %s %w: %s", linter, errGoLintReport, report.Report.Error)
	}
	if runErr != nil && len(report.Issues) == 0 {
		return golangciReport{}, fmt.Errorf("golangci-lint %s failed without issues: %w", linter, runErr)
	}
	if !hasEnabledLinter(report, linter) {
		return golangciReport{}, fmt.Errorf("golangci-lint output omitted enabled %s measurement: %w", linter, errGoLintMissing)
	}
	return report, nil
}

func hasEnabledLinter(report golangciReport, name string) bool {
	for _, entry := range report.Report.Linters {
		if entry.Name == name && entry.Enabled {
			return true
		}
	}
	return false
}

func metricValue(text string, metric model.Metric) (int, error) {
	pattern := funlenValue
	if metric == model.MetricGoCognitive {
		pattern = gocognitValue
	}
	matches := pattern.FindStringSubmatch(text)
	if len(matches) < 2 {
		return 0, fmt.Errorf("cannot parse %s value from %q: %w", metric, text, errGoMetricParse)
	}
	value, err := strconv.Atoi(matches[1])
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid %s value in %q: %w", metric, text, errGoMetricInvalid)
	}
	return value, nil
}

func findGoFunction(root, module string, files map[string]goSourceFile, reportedPath string, line, column int) (string, string, error) {
	path := resolveReportedPath(root, module, reportedPath)
	source, exists := files[path]
	if !exists {
		return path, "", fmt.Errorf("go diagnostic references unknown source %q: %w", reportedPath, errGoSourceUnknown)
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
	return functionAtPosition(path, source, position, line, column)
}

func functionAtPosition(path string, source goSourceFile, position token.Pos, line, column int) (string, string, error) {
	var match *goFunction
	var smallest token.Pos
	for index := range source.functions {
		function := &source.functions[index]
		if function.node.Pos() <= position && position <= function.node.End() {
			span := function.node.End() - function.node.Pos()
			if match == nil || span < smallest {
				match, smallest = function, span
			}
		}
	}
	if match == nil {
		return "", "", fmt.Errorf("go diagnostic at %s:%d:%d is outside any function: %w", path, line, column, errGoOutsideFunction)
	}
	return path, match.id, nil
}
