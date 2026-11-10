// SPDX-License-Identifier: MIT OR Apache-2.0

// Package typescript collects TypeScript function measurements.
package typescript

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/RigelBuild/meissa/go/internal/codeshape"
	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars/tsx"
	"github.com/odvcencio/gotreesitter/grammars/typescript"
	"github.com/titanous/json5"
)

const maxBiomeDiagnostics = 1_000_000

func biomeRuleMetrics() map[string]codeshape.Metric {
	return map[string]codeshape.Metric{
		"lint/complexity/noExcessiveCognitiveComplexity": codeshape.MetricTSCognitive,
		"lint/complexity/noExcessiveLinesPerFunction":    codeshape.MetricTSLines,
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
		Changed               int `json:"changed"`
		Unchanged             int `json:"unchanged"`
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
	node *gotreesitter.Node
	id   string
}

// TSMetrics measures Biome's function complexity and line count rules.
func TSMetrics(ctx context.Context, root string, cfg codeshape.Config) (
	map[codeshape.Metric]map[string][]codeshape.Entry, []codeshape.Skip, error,
) {
	paths, err := codeshape.TrackedPaths(ctx, root)
	if err != nil {
		return nil, nil, fmt.Errorf("collect TypeScript paths: %w", err)
	}
	sources, err := readTSSources(root, paths, cfg)
	if err != nil {
		return nil, nil, err
	}
	if len(sources) == 0 {
		return codeshape.NewFunctionMeasurements(), nil, nil
	}
	_, report, sourcePaths, err := prepareBiome(ctx, root, sources)
	if err != nil {
		return nil, nil, err
	}
	skips, err := biomeSkippedPaths(root, paths, sourcePaths, report, sources)
	if err != nil {
		return nil, nil, err
	}
	values := codeshape.NewFunctionMeasurements()
	files := make(map[string][]tsFunction, len(sources))
	for path, source := range sources {
		functions, tree, err := parseTSFunctions(path, source)
		if err != nil {
			skips = append(skips, codeshape.Skip{Path: path, Reason: err.Error(),
				Unmeasured: &codeshape.UnmeasuredFunctions{Values: map[codeshape.Metric]int{}, Ignored: false}})
			delete(sources, path)
			continue
		}
		defer tree.Release()
		files[path] = functions
		for _, function := range functions {
			codeshape.AppendFunctionValue(values, codeshape.MetricTSCognitive, path, function.id, 0)
			codeshape.AppendFunctionValue(values, codeshape.MetricTSLines, path, function.id, 0)
		}
	}
	err = applyBiomeIssues(report, files, sources, skips, values)
	if err != nil {
		return nil, nil, err
	}
	return values, skips, nil
}

func prepareBiome(ctx context.Context, root string, sources map[string][]byte) (string, biomeReport, []string, error) {
	binary, err := exec.LookPath("biome")
	if err != nil {
		return "", biomeReport{}, nil, fmt.Errorf("find biome: %w", err)
	}
	err = codeshape.CheckToolVersion(ctx, binary, codeshape.BiomeVersion)
	if err != nil {
		return "", biomeReport{}, nil, fmt.Errorf("check biome: %w", err)
	}
	var paths []string
	for path := range sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	report, err := runBiome(ctx, binary, root, paths)
	if err == nil && report.Summary.DiagnosticsNotPrinted != 0 {
		return "", biomeReport{}, nil, fmt.Errorf("%w: %d were not printed",
			errBiomeTruncated, report.Summary.DiagnosticsNotPrinted)
	}
	return binary, report, paths, err
}

func readTSSources(root string, paths []string, cfg codeshape.Config) (map[string][]byte, error) {
	sources := make(map[string][]byte)
	for _, path := range paths {
		if !isTypeScriptPath(path) || !codeshape.CountedPath(path, cfg) {
			continue
		}
		source, err := codeshape.ReadRepositoryFile(root, filepath.FromSlash(path))
		if err != nil {
			return nil, fmt.Errorf("read TypeScript source %q: %w", path, err)
		}
		sources[path] = source
	}
	return sources, nil
}

func applyBiomeIssues(report biomeReport, files map[string][]tsFunction, sources map[string][]byte,
	skips []codeshape.Skip, values map[codeshape.Metric]map[string][]codeshape.Entry,
) error {
	metrics := biomeRuleMetrics()
	for _, diagnostic := range report.Diagnostics {
		metric, relevant := metrics[diagnostic.Category]
		if !relevant {
			continue
		}
		path := filepath.ToSlash(diagnostic.Location.Path)
		functions, exists := files[path]
		if !exists {
			recorded, err := recordUnparseableIssue(skips, path, metric, diagnostic.Message)
			if err != nil {
				return err
			}
			if recorded {
				continue
			}
		}
		if _, included := sources[path]; !included && slices.ContainsFunc(skips, func(skip codeshape.Skip) bool {
			return skip.Path == path
		}) {
			continue
		}
		if !exists {
			return fmt.Errorf("%w %q", errBiomeUnknownSource, path)
		}
		function, err := findTSFunction(
			functions, diagnostic.Location.Start.Line, diagnostic.Location.Start.Column, sources[path],
		)
		if err != nil {
			return err
		}
		value, err := biomeValue(diagnostic.Message, metric)
		if err != nil {
			return err
		}
		codeshape.AppendFunctionValue(values, metric, path, function.id, value)
	}
	return nil
}

func runBiome(ctx context.Context, binary, root string, paths []string) (biomeReport, error) {
	args := make([]string, 0, 5+len(paths))
	args = append(args, "lint", "--reporter=json", "--max-diagnostics="+strconv.Itoa(maxBiomeDiagnostics),
		"--only=complexity/noExcessiveCognitiveComplexity", "--only=complexity/noExcessiveLinesPerFunction",
		"--no-errors-on-unmatched")
	args = append(args, paths...)
	command := exec.CommandContext(ctx, binary, args...) //nolint:gosec // G204: execute the biome linter
	command.Dir = root
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	var exitErr *exec.ExitError
	if runErr != nil && (!errors.As(runErr, &exitErr) || exitErr.ExitCode() != 1) {
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
	err = validateBiomeErrors(report)
	if err != nil {
		return biomeReport{}, err
	}
	return report, nil
}

func readBiomeConfig(root, path string) (map[string]any, error) {
	data, err := codeshape.ReadRepositoryFile(root, path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var config map[string]any
	err = json5.Unmarshal(data, &config)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
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

func biomeValue(message string, metric codeshape.Metric) (int, error) {
	fields := strings.Fields(message)
	for index, field := range fields {
		if index+1 < len(fields) && (metric == codeshape.MetricTSCognitive && field == "of" ||
			metric == codeshape.MetricTSLines && field == "lines") {
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
	assignTSFunctionIDs(functions, lang, source)
	return functions, tree, nil
}

func assignTSFunctionIDs(functions []tsFunction, lang *gotreesitter.Language, source []byte) {
	counts := make(map[string]int)
	for i := range functions {
		parentID := ""
		for j := i - 1; j >= 0; j-- {
			if functions[j].node.StartByte() <= functions[i].node.StartByte() &&
				functions[j].node.EndByte() >= functions[i].node.EndByte() {
				parentID = functions[j].id + "/"
				break
			}
		}
		classID := ""
		for parent := functions[i].node.Parent(); parent != nil; parent = parent.Parent() {
			if parent.Type(lang) == "class_declaration" || parent.Type(lang) == "class" {
				if name := parent.ChildByFieldName("name", lang); name != nil {
					classID = name.Text(source) + "/"
				}
				break
			}
		}
		base := parentID + classID + functions[i].id
		counts[base]++
		functions[i].id = base
		if counts[base] > 1 {
			functions[i].id += fmt.Sprintf("#%d", counts[base])
		}
	}
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
			callable.Text(source) == "it" || strings.HasPrefix(callable.Text(source), "describe.") ||
			strings.HasPrefix(callable.Text(source), "test.") || strings.HasPrefix(callable.Text(source), "it.")) {
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
		function := tsFunction{node: node, id: ""}
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
	case "function_declaration", "function_expression", "arrow_function", "method_definition",
		"generator_function_declaration", "generator_function":
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
	if child.Type(lang) != "string" &&
		(child.Type(lang) != "template_string" || strings.Contains(child.Text(source), "${")) {
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
	} else if parent := node.Parent(); parent != nil && (parent.Type(lang) == "variable_declarator" ||
		parent.Type(lang) == "public_field_definition" || parent.Type(lang) == "pair") {
		if variable := parent.ChildByFieldName("name", lang); variable != nil {
			functionName = variable.Text(source)
		}
		if key := parent.ChildByFieldName("key", lang); key != nil {
			functionName = key.Text(source)
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
	offset, err := tsOffset(line, column, source)
	if err != nil {
		return nil, err
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

func tsOffset(line, column int, source []byte) (int, error) {
	lineStart := 0
	for current := 1; current < line; current++ {
		next := bytes.IndexByte(source[lineStart:], '\n')
		if next < 0 {
			return 0, fmt.Errorf("biome position line %d is outside source: %w", line, errBiomeOutsideSource)
		}
		lineStart += next + 1
	}
	offset := lineStart
	for units := 1; units < column; {
		if offset >= len(source) || source[offset] == '\n' {
			return 0, fmt.Errorf("biome position %d:%d is outside source: %w", line, column, errBiomeOutsideSource)
		}
		_, size := utf8.DecodeRune(source[offset:])
		units++
		offset += size
	}
	if offset > len(source) {
		return 0, fmt.Errorf("biome position %d:%d is outside source: %w", line, column, errBiomeOutsideSource)
	}
	return offset, nil
}
