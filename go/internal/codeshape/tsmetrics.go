// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars/tsx"
	"github.com/odvcencio/gotreesitter/grammars/typescript"
	"github.com/titanous/json5"
)

const maxBiomeDiagnostics = 1_000_000

func biomeRuleMetrics() map[string]Metric {
	return map[string]Metric{
		"lint/complexity/noExcessiveCognitiveComplexity": MetricTSCognitive,
		"lint/complexity/noExcessiveLinesPerFunction":    MetricTSLines,
	}
}

var (
	errBiomeTruncated       = errors.New("biome truncated diagnostics")
	errBiomeUnknownSource   = errors.New("biome diagnostic references unknown source")
	errBiomeMissingOutput   = errors.New("parse biome output: missing summary or diagnostics")
	errBiomeReported        = errors.New("biome reported errors")
	errBiomeValue           = errors.New("cannot parse biome value")
	errTSSyntax             = errors.New("syntax errors")
	errBiomePosition        = errors.New("invalid biome source position")
	errBiomeOutsideSource   = errors.New("biome position outside source")
	errBiomeOutsideFunction = errors.New("biome position outside any function")
)

type biomeReport struct {
	Summary *struct {
		DiagnosticsNotPrinted int `json:"diagnosticsNotPrinted"`
		Errors                int `json:"errors"`
	} `json:"summary"`
	Diagnostics []struct {
		Category string `json:"category"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
		Location struct {
			Path  string `json:"path"`
			Start struct {
				Line   int `json:"line"`
				Column int `json:"column"`
			} `json:"start"`
		} `json:"location"`
	} `json:"diagnostics"`
}

type tsFunction struct {
	node   *gotreesitter.Node
	id     string
	parent *tsFunction
}

// TSMetrics measures Biome's function complexity and line count rules.
func TSMetrics(ctx context.Context, root string, cfg Config) (map[Metric]map[string][]Entry, error) {
	paths, err := trackedPaths(ctx, root)
	if err != nil {
		return nil, err
	}
	sources := make(map[string][]byte)
	for _, path := range paths {
		if !isTypeScriptPath(path) || !countedPath(path, cfg) {
			continue
		}
		//nolint:gosec // G304: path from the repo's tracked file list
		source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, fmt.Errorf("read TypeScript source %q: %w", path, err)
		}
		sources[path] = source
	}
	if len(sources) == 0 {
		return newFunctionMeasurements(), nil
	}
	binary, err := exec.LookPath("biome")
	if err != nil {
		return nil, fmt.Errorf("find biome: %w", err)
	}
	var sourcePaths []string
	for path := range sources {
		sourcePaths = append(sourcePaths, path)
	}
	sort.Strings(sourcePaths)
	report, err := runBiome(ctx, binary, root, sourcePaths)
	if err != nil {
		return nil, err
	}
	if report.Summary.DiagnosticsNotPrinted != 0 {
		return nil, fmt.Errorf("%w: %d were not printed", errBiomeTruncated, report.Summary.DiagnosticsNotPrinted)
	}
	values := newFunctionMeasurements()
	files := make(map[string][]tsFunction, len(sources))
	for path, source := range sources {
		functions, tree, err := parseTSFunctions(path, source)
		if err != nil {
			return nil, err
		}
		defer tree.Release()
		files[path] = functions
		for _, function := range functions {
			appendFunctionValue(values, MetricTSCognitive, path, function.id, 0)
			appendFunctionValue(values, MetricTSLines, path, function.id, 0)
		}
	}
	metrics := biomeRuleMetrics()
	for _, diagnostic := range report.Diagnostics {
		metric, relevant := metrics[diagnostic.Category]
		if !relevant {
			continue
		}
		path := filepath.ToSlash(diagnostic.Location.Path)
		functions, exists := files[path]
		if !exists {
			return nil, fmt.Errorf("%w %q", errBiomeUnknownSource, path)
		}
		function, err := findTSFunction(
			functions, diagnostic.Location.Start.Line, diagnostic.Location.Start.Column, sources[path],
		)
		if err != nil {
			return nil, err
		}
		value, err := biomeValue(diagnostic.Message, metric)
		if err != nil {
			return nil, err
		}
		appendFunctionValue(values, metric, path, function.id, value)
	}
	return values, nil
}

func isTypeScriptPath(path string) bool {
	switch filepath.Ext(path) {
	case ".ts", ".tsx", ".mts", ".cts":
		return true
	default:
		return false
	}
}

func runBiome(ctx context.Context, binary, root string, paths []string) (biomeReport, error) {
	args := make([]string, 0, 5+len(paths))
	args = append(args, "lint", "--reporter=json", "--max-diagnostics="+strconv.Itoa(maxBiomeDiagnostics),
		"--only=complexity/noExcessiveCognitiveComplexity", "--only=complexity/noExcessiveLinesPerFunction")
	args = append(args, paths...)
	command := exec.CommandContext(ctx, binary, args...) //nolint:gosec // G204: execute the biome linter
	command.Dir = root
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	if runErr != nil {
		return biomeReport{}, fmt.Errorf("biome lint failed: %w: %s", runErr, strings.TrimSpace(stderr.String()))
	}
	var report biomeReport
	err := json.Unmarshal(stdout.Bytes(), &report)
	if err != nil {
		return biomeReport{}, fmt.Errorf("parse biome output: %w", err)
	}
	if report.Summary == nil || report.Diagnostics == nil {
		return biomeReport{}, errBiomeMissingOutput
	}
	if report.Summary.Errors > 0 {
		return biomeReport{}, fmt.Errorf("biome reported %d errors: %w", report.Summary.Errors, errBiomeReported)
	}
	return report, nil
}

func readBiomeConfig(root string) (map[string]any, error) {
	data, err := os.ReadFile(filepath.Join(root, "biome.json")) //nolint:gosec // G304: read repo config
	if err != nil {
		return nil, fmt.Errorf("read biome.json: %w", err)
	}
	var config map[string]any
	err = json5.Unmarshal(data, &config)
	if err != nil {
		return nil, fmt.Errorf("parse biome.json: %w", err)
	}
	return config, nil
}

func nestedMap(value map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		nested, ok := value[key].(map[string]any)
		if !ok {
			return nil
		}
		value = nested
	}
	return value
}

func numericValue(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case int64:
		return int(number)
	case float64:
		return int(number)
	default:
		return 0
	}
}

func biomeValue(message string, metric Metric) (int, error) {
	fields := strings.Fields(message)
	for index, field := range fields {
		if index+1 < len(fields) && (metric == MetricTSCognitive && field == "of" ||
			metric == MetricTSLines && field == "lines") {
			value, err := strconv.Atoi(strings.Trim(fields[index+1], ".()"))
			if err != nil {
				return 0, fmt.Errorf("parse biome value: %w", err)
			}
			return value, nil
		}
	}
	return 0, fmt.Errorf("cannot parse %s value from %q: %w", metric, message, errBiomeValue)
}

func parseTSFunctions(path string, source []byte) ([]tsFunction, *gotreesitter.Tree, error) {
	lang := typescript.Language()
	if filepath.Ext(path) == ".tsx" {
		lang = tsx.Language()
	}
	tree, err := gotreesitter.NewParser(lang).Parse(source)
	if err != nil {
		return nil, nil, fmt.Errorf("parse TypeScript %q: %w", path, err)
	}
	root := tree.RootNode()
	if root.HasErrorOrMissing() {
		tree.Release()
		return nil, nil, fmt.Errorf("parse TypeScript %q: %w", path, errTSSyntax)
	}
	functions := make([]tsFunction, 0)
	collectTSFunctions(root, lang, nil, nil, &functions, source)
	counts := make(map[string]int)
	for _, function := range functions {
		counts[function.id]++
	}
	seen := make(map[string]int)
	for i := range functions {
		id := functions[i].id
		if counts[id] > 1 {
			seen[id]++
			functions[i].id = fmt.Sprintf("%s#%d", id, seen[id])
		}
	}
	return functions, tree, nil
}

func collectTSFunctions(
	node *gotreesitter.Node,
	lang *gotreesitter.Language,
	titlePath []string,
	parent *tsFunction,
	functions *[]tsFunction,
	source []byte,
) {
	if node == nil {
		return
	}
	if node.Type(lang) == "call_expression" {
		callable := node.ChildByFieldName("function", lang)
		if callable != nil && (callable.Text(source) == "describe" || callable.Text(source) == "test" ||
			callable.Text(source) == "it") {
			arguments := node.ChildByFieldName("arguments", lang)
			title := firstStringArgument(arguments, lang, source)
			callback := lastFunctionArgument(arguments, lang)
			if title != "" && callback != nil {
				nextPath := append(append([]string(nil), titlePath...), title)
				collectTSFunctions(callback, lang, nextPath, parent, functions, source)
				return
			}
		}
	}
	if isTSFunctionNode(node, lang) {
		function := tsFunction{node: node, id: "", parent: parent}
		function.id = tsFunctionID(node, lang, titlePath, source)
		*functions = append(*functions, function)
		parent = &(*functions)[len(*functions)-1]
	}
	for index := range node.ChildCount() {
		collectTSFunctions(node.Child(index), lang, titlePath, parent, functions, source)
	}
}

func isTSFunctionNode(node *gotreesitter.Node, lang *gotreesitter.Language) bool {
	switch node.Type(lang) {
	case "function_declaration", "function_expression", "arrow_function", "method_definition":
		return true
	default:
		return false
	}
}

func firstStringArgument(arguments *gotreesitter.Node, lang *gotreesitter.Language, source []byte) string {
	if arguments == nil {
		return ""
	}
	if arguments.NamedChildCount() == 0 {
		return ""
	}
	child := arguments.NamedChild(0)
	if child.Type(lang) != "string" {
		return ""
	}
	return strings.Trim(strings.TrimSpace(child.Text(source)), "`\"'")
}

func lastFunctionArgument(arguments *gotreesitter.Node, lang *gotreesitter.Language) *gotreesitter.Node {
	if arguments == nil {
		return nil
	}
	for index := arguments.NamedChildCount() - 1; index >= 0; index-- {
		child := arguments.NamedChild(index)
		if isTSFunctionNode(child, lang) {
			return child
		}
	}
	return nil
}

func tsFunctionID(node *gotreesitter.Node, lang *gotreesitter.Language, titlePath []string, source []byte) string {
	name := node.ChildByFieldName("name", lang)
	functionName := "anonymous"
	if name != nil {
		functionName = name.Text(source)
	} else if parent := node.Parent(); parent != nil && parent.Type(lang) == "variable_declarator" {
		if variable := parent.ChildByFieldName("name", lang); variable != nil {
			functionName = variable.Text(source)
		}
	}
	if len(titlePath) > 0 {
		functionName = strings.Join(titlePath, "/") + "/" + functionName
	}
	return functionName
}

func findTSFunction(functions []tsFunction, line, column int, source []byte) (*tsFunction, error) {
	if line < 1 || column < 1 {
		return nil, errBiomePosition
	}
	lineStart := 0
	for current := 1; current < line; current++ {
		next := bytes.IndexByte(source[lineStart:], '\n')
		if next < 0 {
			return nil, fmt.Errorf("biome position line %d is outside source: %w", line, errBiomeOutsideSource)
		}
		lineStart += next + 1
	}
	offset := lineStart + column - 1
	if offset > len(source) {
		return nil, fmt.Errorf("biome position %d:%d is outside source: %w", line, column, errBiomeOutsideSource)
	}
	var match *tsFunction
	var smallest uint32
	for index := range functions {
		function := &functions[index]
		if int64(function.node.StartByte()) <= int64(offset) && int64(offset) <= int64(function.node.EndByte()) {
			span := function.node.EndByte() - function.node.StartByte()
			if match == nil || span < smallest {
				match = function
				smallest = span
			}
		}
	}
	if match == nil {
		return nil, fmt.Errorf("biome position %d:%d is outside any function: %w", line, column, errBiomeOutsideFunction)
	}
	return match, nil
}
