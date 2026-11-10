// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

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
)

const highFunlenLimit = 1_000_000_000

var (
	gocognitValue          = regexp.MustCompile(`cognitive complexity ([0-9]+) of func`)
	funlenValue            = regexp.MustCompile(`\(([0-9]+) > [0-9]+\)`)
	errEmptyGoModule       = errors.New("has no included source files")
	errEmptyGoMeasurement  = errors.New("go function measurement is empty")
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

// Skip records a source file that could not be measured.
type Skip struct {
	Path   string
	Reason string
}

// GoMetrics measures function complexity and length in every included module.
func GoMetrics(ctx context.Context, root string, cfg Config) (map[Metric]map[string][]Entry, []Skip, error) {
	paths, err := trackedPaths(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	modules := goModules(paths, cfg)
	if len(modules) == 0 {
		return newFunctionMeasurements(), nil, nil
	}
	binary, err := exec.LookPath("golangci-lint")
	if err != nil {
		return nil, nil, fmt.Errorf("find golangci-lint: %w", err)
	}
	measurements := newFunctionMeasurements()
	skips := []Skip{}
	for _, module := range modules {
		files, skipped, err := parseGoSources(root, module, paths, cfg)
		if err != nil {
			return nil, nil, err
		}
		skips = append(skips, skipped...)
		if len(files) == 0 {
			continue
		}
		limits := []struct {
			metric     Metric
			linter     string
			lines      int
			statements int
		}{
			{metric: MetricGoCognitive, linter: "gocognit", lines: 0, statements: 0},
			{metric: MetricGoLines, linter: "funlen", lines: cfg.Functions.Go.Lines, statements: highFunlenLimit},
			{metric: MetricGoStatements, linter: "funlen", lines: highFunlenLimit, statements: cfg.Functions.Go.Statements},
		}
		for _, file := range files {
			for _, function := range file.functions {
				for _, metric := range []Metric{MetricGoCognitive, MetricGoLines, MetricGoStatements} {
					appendFunctionValue(measurements, metric, function.path, function.id, 0)
				}
			}
		}
		for _, limit := range limits {
			report, err := runGolangci(ctx, binary, root, module, limit.linter, limit.lines, limit.statements, cfg)
			if err != nil {
				return nil, nil, err
			}
			for _, issue := range report.Issues {
				path := issue.Pos.Filename
				if filepath.IsAbs(path) {
					path, err = filepath.Rel(root, path)
					if err != nil {
						return nil, nil, fmt.Errorf("relativize Go diagnostic path: %w", err)
					}
				} else {
					path = filepath.Join(module, path)
				}
				path = filepath.ToSlash(path)
				if excludedFunctionPath(path, cfg) || !countedPath(path, cfg) {
					continue
				}
				value, err := metricValue(issue.Text, limit.metric)
				if err != nil {
					return nil, nil, err
				}
				path, function, err := findGoFunction(root, module, files, issue.Pos.Filename, issue.Pos.Line, issue.Pos.Column)
				if err != nil {
					return nil, nil, err
				}
				appendFunctionValue(measurements, limit.metric, path, function, value)
			}
		}
	}
	return measurements, skips, nil
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

func goModules(paths []string, cfg Config) []string {
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

func excludedFunctionPath(path string, cfg Config) bool {
	for segment := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		if segment == "testdata" || segment == "fixtures" {
			return true
		}
	}
	return matchesAny(filepath.ToSlash(path), cfg.Exclude)
}

func parseGoSources(root, module string, paths []string, cfg Config) (map[string]goSourceFile, []Skip, error) {
	files := make(map[string]goSourceFile)
	skips := []Skip{}
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
		if filepath.Ext(path) != ".go" || excludedFunctionPath(path, cfg) {
			continue
		}
		if module != "." && !strings.HasPrefix(filepath.ToSlash(path), strings.TrimSuffix(module, "/")+"/") {
			continue
		}
		nested := false
		for _, dir := range modules {
			if dir != "." && dir != module && (module == "." || strings.HasPrefix(dir, module+"/")) &&
				strings.HasPrefix(filepath.ToSlash(path), dir+"/") {
				nested = true
				break
			}
		}
		if nested {
			continue
		}
		if !countedPath(filepath.ToSlash(path), cfg) {
			continue
		}
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		matches, err := buildContext.MatchFile(filepath.Dir(fullPath), filepath.Base(fullPath))
		if err != nil {
			return nil, nil, fmt.Errorf("check Go build constraints %q: %w", path, err)
		}
		if !matches {
			skips = append(skips, Skip{Path: path, Reason: "not in default Go build"})
			continue
		}
		//nolint:gosec // G304: path from the repo's tracked file list
		source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
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

func goFunctions(file *ast.File, set *token.FileSet) []goFunction {
	functions := make([]goFunction, 0)
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
			functions = append(functions, goFunction{node: function, path: "", base: "", id: ""})
		}
		return true
	})
	sort.Slice(functions, func(i, j int) bool { return functions[i].node.Pos() < functions[j].node.Pos() })
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
	return functions
}

func runGolangci(
	ctx context.Context,
	binary, root, module, linter string,
	lines, statements int,
	cfg Config,
) (golangciReport, error) {
	configuration := fmt.Sprintf(
		"version: '2'\nlinters:\n  default: none\n  enable:\n    - %s\n"+
			"  settings:\n    gocognit:\n      min-complexity: %d\n    funlen:\n"+
			"      lines: %d\n      statements: %d\n      ignore-comments: true\n"+
			"issues:\n  max-issues-per-linter: 0\n  max-same-issues: 0\n",
		linter,
		cfg.Functions.Go.Cognitive,
		lines,
		statements,
	)
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

func runGolangciWithConfig(
	ctx context.Context, binary, root, module, linter, configuration string, temporary *os.File,
) (golangciReport, error) {
	var err error
	_, err = temporary.WriteString(configuration)
	if err != nil {
		closeErr := temporary.Close()
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close golangci-lint config: %w", closeErr))
		}
		return golangciReport{}, fmt.Errorf("write golangci-lint config: %w", err)
	}
	err = temporary.Close()
	if err != nil {
		return golangciReport{}, fmt.Errorf("close golangci-lint config: %w", err)
	}
	//nolint:gosec // G204: execute the configured golangci-lint binary
	command := exec.CommandContext(ctx, binary, "run", "--config", temporary.Name(), "--enable-only", linter,
		"--output.json.path", "stdout", "--path-mode", "abs", "--show-stats=false", "--uniq-by-line=false",
		"--max-issues-per-linter", "0", "--max-same-issues", "0", "./...")
	command.Dir = filepath.Join(root, filepath.FromSlash(module))
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	if runErr != nil {
		var exitError *exec.ExitError
		if !errors.As(runErr, &exitError) || exitError.ExitCode() != 1 {
			return golangciReport{}, fmt.Errorf("golangci-lint %s failed in %q: %w: %s",
				linter, module, runErr, strings.TrimSpace(stderr.String()))
		}
	}
	var report golangciReport
	err = json.Unmarshal(stdout.Bytes(), &report)
	if err != nil {
		return golangciReport{}, fmt.Errorf("parse golangci-lint %s output in %q: %w", linter, module, err)
	}
	if report.Report.Error != "" {
		return golangciReport{}, fmt.Errorf("golangci-lint %s %w: %s", linter, errGoLintReport, report.Report.Error)
	}
	if runErr != nil && len(report.Issues) == 0 {
		return golangciReport{}, fmt.Errorf("golangci-lint %s failed without issues: %w", linter, runErr)
	}
	enabled := false
	for _, entry := range report.Report.Linters {
		if entry.Name == linter && entry.Enabled {
			enabled = true
		}
	}
	if !enabled {
		return golangciReport{}, fmt.Errorf("golangci-lint output omitted enabled %s measurement: %w",
			linter, errGoLintMissing)
	}
	return report, nil
}

func metricValue(text string, metric Metric) (int, error) {
	pattern := funlenValue
	if metric == MetricGoCognitive {
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

func hasAnyFunctionMeasurement(values map[Metric]map[string][]Entry) bool {
	for _, paths := range values {
		for _, entries := range paths {
			if len(entries) > 0 {
				return true
			}
		}
	}
	return false
}
